package app_test

import (
	"context"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/opikdev/api-go-starter/internal/apiclient"
)

func TestRateLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("blocks login after the auth limit, with Retry-After", func(t *testing.T) {
		s := newServer(t, map[string]string{"AUTH_RATE_LIMIT_PER_MINUTE": "3", "TRUST_PROXY_HOPS": "1"})
		c := s.client(t, clientOptions{ip: randomIP()})
		login := func() *apiclient.CreateSessionResponse {
			res, _ := c.CreateSessionWithResponse(ctx, apiclient.CreateSession{Email: openapi_types.Email(uniqueEmail()), Password: testPassword})
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
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "2", "TRUST_PROXY_HOPS": "1"})
		ip := randomIP()
		s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip})
		s.raw(t, "GET", "/v1/users/me", "", map[string]string{"X-Forwarded-For": ip})
		res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip})
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 429, "rate-limited")
	})

	t.Run("reports each policy in the IETF RateLimit headers", func(t *testing.T) {
		token := newServer(t, nil).signUp(t).token
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "5", "AUTH_RATE_LIMIT_PER_MINUTE": "3", "TRUST_PROXY_HOPS": "1"})
		res := s.raw(t, "GET", "/v1/users/me", "", map[string]string{"Authorization": "Bearer " + token, "X-Forwarded-For": randomIP()})
		match := regexp.MustCompile(`^"global";r=4;t=(\d+)$`).FindStringSubmatch(res.headers.Get("RateLimit"))
		if res.headers.Get("RateLimit-Policy") != `"global";q=5;w=60` || match == nil {
			t.Fatalf("headers = %v", res.headers)
		}
		if reset, _ := strconv.Atoi(match[1]); reset < 1 || reset > 60 {
			t.Fatalf("t = %d, outside the 60-second window", reset)
		}
		for _, old := range []string{"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset"} {
			if res.headers.Get(old) != "" {
				t.Errorf("still sends %s", old)
			}
		}
		login := s.raw(t, "POST", "/v1/sessions", `{"email":"`+uniqueEmail()+`","password":"`+testPassword+`"}`, map[string]string{"X-Forwarded-For": randomIP()})
		if login.headers.Get("RateLimit-Policy") != `"global";q=5;w=60, "auth";q=3;w=60` ||
			!regexp.MustCompile(`^"global";r=4;t=\d+, "auth";r=2;t=\d+$`).MatchString(login.headers.Get("RateLimit")) {
			t.Fatalf("headers = %v", login.headers)
		}
	})

	t.Run("describes only the exceeded policy on a 429", func(t *testing.T) {
		s := newServer(t, map[string]string{"AUTH_RATE_LIMIT_PER_MINUTE": "1", "TRUST_PROXY_HOPS": "1"})
		ip := randomIP()
		body := `{"email":"` + uniqueEmail() + `","password":"` + testPassword + `"}`
		s.raw(t, "POST", "/v1/sessions", body, map[string]string{"X-Forwarded-For": ip})
		res := s.raw(t, "POST", "/v1/sessions", body, map[string]string{"X-Forwarded-For": ip})
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 429, "rate-limited")
		match := regexp.MustCompile(`^"auth";r=0;t=(\d+)$`).FindStringSubmatch(res.headers.Get("RateLimit"))
		if match == nil || res.headers.Get("Retry-After") != match[1] || res.headers.Get("RateLimit-Policy") != `"global";q=100000;w=60, "auth";q=1;w=60` {
			t.Fatalf("headers = %v", res.headers)
		}
	})

	t.Run("counts concurrent requests exactly", func(t *testing.T) {
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "5", "TRUST_PROXY_HOPS": "1"})
		ip := randomIP()
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
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "1", "TRUST_PROXY_HOPS": "1"})
		ip := randomIP()
		s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip})
		if res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip}); res.status != 429 {
			t.Fatalf("status = %d", res.status)
		}
		if _, err := appPool.Exec(ctx, "update rate_limits set reset_at = now() - interval '1 second' where key = $1", "global:"+prefix64(ip)); err != nil {
			t.Fatal(err)
		}
		if res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": ip}); res.status != 401 {
			t.Fatalf("status = %d", res.status)
		}
	})

	t.Run("ignores X-Forwarded-For by default", func(t *testing.T) {
		s := newServer(t, nil)
		spoofed := randomIP()
		s.raw(t, "GET", "/v1/notes", "", map[string]string{"X-Forwarded-For": spoofed})
		var spoofedRows, remoteRows int
		_ = appPool.QueryRow(ctx, "select count(*) from rate_limits where key = $1", "global:"+spoofed).Scan(&spoofedRows)
		_ = appPool.QueryRow(ctx, "select count(*) from rate_limits where key = 'global:127.0.0.1'").Scan(&remoteRows)
		if spoofedRows != 0 || remoteRows != 1 {
			t.Fatalf("counted the spoofed address %d times and the remote address %d times", spoofedRows, remoteRows)
		}
	})

	t.Run("never limits health checks", func(t *testing.T) {
		s := newServer(t, map[string]string{"RATE_LIMIT_PER_MINUTE": "1", "TRUST_PROXY_HOPS": "1"})
		ip := randomIP()
		for range 3 {
			if res := s.raw(t, "GET", "/healthz", "", map[string]string{"X-Forwarded-For": ip}); res.status != 200 {
				t.Fatalf("status = %d", res.status)
			}
		}
	})
}

func prefix64(ip string) string {
	return netip.PrefixFrom(netip.MustParseAddr(ip), 64).Masked().String()
}

func TestLoginFailureLimit(t *testing.T) {
	s := newServer(t, map[string]string{"LOGIN_FAILURE_LIMIT": "2", "TRUST_PROXY_HOPS": "1"})
	login := func(email, password string) rawResponse {
		return s.raw(t, "POST", "/v1/sessions", `{"email":"`+email+`","password":"`+password+`"}`, map[string]string{"X-Forwarded-For": randomIP()})
	}

	t.Run("blocks an account after its failed logins, from any address", func(t *testing.T) {
		email := s.signUp(t).email
		for range 3 {
			if res := login(email, testPassword); res.status != 201 || strings.Contains(res.headers.Get("RateLimit-Policy"), "login") {
				t.Fatalf("a successful login was limited or advertised the login policy: %d %v", res.status, res.headers)
			}
		}
		for range 2 {
			if res := login(email, "wrong-password-123"); res.status != 401 {
				t.Fatalf("status = %d", res.status)
			}
		}
		blocked := login(strings.ToUpper(email), testPassword)
		expectProblem(t, blocked.status, blocked.headers.Get("Content-Type"), []byte(blocked.body), 429, "rate-limited")
		match := regexp.MustCompile(`^"login";r=0;t=(\d+)$`).FindStringSubmatch(blocked.headers.Get("RateLimit"))
		if match == nil || blocked.headers.Get("Retry-After") != match[1] || !strings.HasSuffix(blocked.headers.Get("RateLimit-Policy"), `, "login";q=2;w=900`) {
			t.Fatalf("headers = %v", blocked.headers)
		}
	})

	t.Run("blocks an unknown email the same way", func(t *testing.T) {
		email := uniqueEmail()
		login(email, testPassword)
		login(email, testPassword)
		blocked := login(email, testPassword)
		expectProblem(t, blocked.status, blocked.headers.Get("Content-Type"), []byte(blocked.body), 429, "rate-limited")
	})

	t.Run("stores only a hash of the email", func(t *testing.T) {
		email := uniqueEmail()
		login(email, testPassword)
		var plain int
		_ = appPool.QueryRow(context.Background(), "select count(*) from rate_limits where key like '%' || $1 || '%'", email).Scan(&plain)
		if plain != 0 {
			t.Fatal("the rate limit key contains the email")
		}
	})
}
