package app_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/opikdev/api-go-starter/internal/apiclient"
)

func TestRateLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("blocks login after the auth limit, with Retry-After", func(t *testing.T) {
		s := newServer(t, map[string]string{"AUTH_RATE_LIMIT_PER_MINUTE": "3"})
		c := s.client(t, clientOptions{ip: "auth-" + randomHex(6)})
		login := func() *apiclient.CreateSessionResponse {
			res, _ := c.CreateSessionWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(uniqueEmail()), Password: testPassword})
			return res
		}
		for range 3 {
			if res := login(); res.StatusCode() != 401 {
				t.Fatalf("status = %d", res.StatusCode())
			}
		}
		blocked := login()
		expectProblem(t, blocked.StatusCode(), blocked.HTTPResponse.Header.Get("Content-Type"), blocked.Body, 429, "rate-limited")
		retry, _ := strconv.Atoi(blocked.HTTPResponse.Header.Get("Retry-After"))
		if retry < 1 || retry > 60 {
			t.Fatalf("Retry-After = %d", retry)
		}
	})

	t.Run("applies the global limit to every /v1 route", func(t *testing.T) {
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "2"})
		ip := "global-" + randomHex(6)
		s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip})
		s.raw(t, "GET", "/v1/users/me", "", map[string]string{"X-Forwarded-For": ip})
		res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip})
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 429, "rate-limited")
	})

	t.Run("reports the remaining budget", func(t *testing.T) {
		token := newServer(t, nil).signUp(t).token
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "5"})
		res := s.raw(t, "GET", "/v1/users/me", "", map[string]string{"Authorization": "Bearer " + token, "X-Forwarded-For": "budget-" + randomHex(6)})
		if res.headers.Get("RateLimit-Limit") != "5" || res.headers.Get("RateLimit-Remaining") != "4" {
			t.Fatalf("headers = %v", res.headers)
		}
	})

	t.Run("counts concurrent requests exactly", func(t *testing.T) {
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "5"})
		ip := "burst-" + randomHex(6)
		statuses := make([]int, 12)
		var wg sync.WaitGroup
		for i := range statuses {
			wg.Go(func() {
				statuses[i] = s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip}).status
			})
		}
		wg.Wait()
		limited := 0
		for _, status := range statuses {
			if status == 429 {
				limited++
			}
		}
		if limited != 7 {
			t.Fatalf("limited %d of 12: %v", limited, statuses)
		}
	})

	t.Run("starts a new window once the old one ends", func(t *testing.T) {
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "1"})
		ip := "window-" + randomHex(6)
		s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip})
		if res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip}); res.status != 429 {
			t.Fatalf("status = %d", res.status)
		}
		if _, err := appPool.Exec(ctx, "update rate_limits set reset_at = now() - interval '1 second' where key = $1", "global:"+ip); err != nil {
			t.Fatal(err)
		}
		if res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip}); res.status != 401 {
			t.Fatalf("status = %d", res.status)
		}
	})

	t.Run("never limits health checks", func(t *testing.T) {
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "1"})
		ip := "health-" + randomHex(6)
		for range 3 {
			if res := s.raw(t, "GET", "/healthz", "", map[string]string{"X-Forwarded-For": ip}); res.status != 200 {
				t.Fatalf("status = %d", res.status)
			}
		}
	})
}
