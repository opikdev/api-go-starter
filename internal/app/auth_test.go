package app_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/opikdev/api-go-starter/internal/apiclient"
	"github.com/opikdev/api-go-starter/internal/publicid"
)

func TestRegistration(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()

	t.Run("creates a user and hides the password", func(t *testing.T) {
		email := uniqueEmail()
		res, err := s.client(t, clientOptions{}).CreateUserWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(email), Password: testPassword})
		if err != nil || res.StatusCode() != 201 {
			t.Fatalf("%v %s", err, res.Body)
		}
		if !regexp.MustCompile(`^usr_[0-9a-f]{32}$`).MatchString(res.JSON201.Id) || res.JSON201.Email != email {
			t.Fatalf("user = %+v", res.JSON201)
		}
		if strings.Contains(string(res.Body), "password") {
			t.Fatal("response mentions the password")
		}
	})

	t.Run("stores an argon2id hash, not the password", func(t *testing.T) {
		user := s.signUp(t)
		var hash string
		if err := appPool.QueryRow(ctx, "select password_hash from users where email = $1", user.email).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, testPassword) {
			t.Fatalf("hash = %q", hash)
		}
	})

	t.Run("lowercases the email and treats case variants as one user", func(t *testing.T) {
		email := uniqueEmail()
		c := s.client(t, clientOptions{})
		res, _ := c.CreateUserWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(strings.ToUpper(email)), Password: testPassword})
		if res.JSON201.Email != email {
			t.Fatalf("email = %s", res.JSON201.Email)
		}
		again, _ := c.CreateUserWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(email), Password: testPassword})
		expectProblem(t, again.StatusCode(), again.HTTPResponse.Header.Get("Content-Type"), again.Body, 409, "email-taken")
	})

	for label, c := range map[string]struct{ body, field, detail string }{
		"a short password":  {`{"email":"a@example.com","password":"short"}`, "password", "must be at least 12 characters"},
		"a long password":   {`{"email":"a@example.com","password":"` + strings.Repeat("x", 129) + `"}`, "password", "must be at most 128 characters"},
		"a bad email":       {`{"email":"nope","password":"` + testPassword + `"}`, "email", "must be a valid email"},
		"a named email":     {`{"email":"Eve <eve@example.com>","password":"` + testPassword + `"}`, "email", "must be a valid email"},
		"a missing email":   {`{"password":"` + testPassword + `"}`, "email", "is required"},
		"a very long email": {`{"email":"` + strings.Repeat("a", 250) + `@example.com","password":"` + testPassword + `"}`, "email", "must be at most 254 characters"},
		"an unknown field":  {`{"email":"a@example.com","password":"` + testPassword + `","isAdmin":true}`, "isAdmin", "isn't a known field"},
		"a wrong type":      {`{"email":5,"password":"` + testPassword + `"}`, "email", "must be string"},
		"malformed JSON":    {`{"email":`, "body", "isn't valid JSON"},
	} {
		t.Run("rejects "+label, func(t *testing.T) {
			res := s.raw(t, "POST", "/v1/users", c.body, nil)
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 400, "validation-failed")
			expectFieldError(t, res.body, c.field, c.detail)
		})
	}

	t.Run("lists every invalid field at once", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/users", `{"email":"nope","password":"short","isAdmin":true}`, nil)
		expectFieldError(t, res.body, "email", "must be a valid email")
		expectFieldError(t, res.body, "password", "must be at least 12 characters")
		expectFieldError(t, res.body, "isAdmin", "isn't a known field")
	})
}

func TestSessions(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()

	t.Run("logs in with a token that isn't stored in plain text", func(t *testing.T) {
		user := s.signUp(t)
		res, _ := s.client(t, clientOptions{}).CreateSessionWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(user.email), Password: testPassword})
		if res.StatusCode() != 201 || res.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %v", res.StatusCode(), res.HTTPResponse.Header)
		}
		if !res.JSON201.ExpiresAt.After(time.Now()) {
			t.Fatal("expiry is in the past")
		}
		var count int
		_ = appPool.QueryRow(ctx, "select count(*) from sessions where token_hash = $1", res.JSON201.Token).Scan(&count)
		if count != 0 {
			t.Fatal("token stored in plain text")
		}
	})

	t.Run("gives a wrong password and an unknown email the same answer", func(t *testing.T) {
		user := s.signUp(t)
		for _, creds := range []apiclient.Credentials{
			{Email: openapi_types.Email(user.email), Password: "wrong-horse-battery"},
			{Email: openapi_types.Email(uniqueEmail()), Password: testPassword},
		} {
			res, _ := s.client(t, clientOptions{}).CreateSessionWithResponse(ctx, creds)
			expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 401, "invalid-credentials")
			if !strings.Contains(string(res.Body), "The email or password is wrong.") {
				t.Fatalf("detail: %s", res.Body)
			}
		}
	})
}

func TestAuthentication(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()

	t.Run("returns the current user", func(t *testing.T) {
		user := s.signUp(t)
		res, _ := s.client(t, clientOptions{token: user.token}).GetCurrentUserWithResponse(ctx)
		if res.StatusCode() != 200 || res.JSON200.Id != user.userID {
			t.Fatalf("%d %s", res.StatusCode(), res.Body)
		}
	})

	for label, header := range map[string]string{"no token": "", "a wrong token": "Bearer not-a-token", "the wrong scheme": "Basic abc"} {
		t.Run("rejects "+label, func(t *testing.T) {
			headers := map[string]string{}
			if header != "" {
				headers["Authorization"] = header
			}
			res := s.raw(t, "GET", "/v1/users/me", "", headers)
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 401, "unauthorized")
			if res.headers.Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q", res.headers.Get("WWW-Authenticate"))
			}
		})
	}

	t.Run("rejects an expired session", func(t *testing.T) {
		user := s.signUp(t)
		id, _ := publicid.Parse(publicid.User, user.userID)
		if _, err := appPool.Exec(ctx, "update sessions set expires_at = now() - interval '1 second' where user_id = $1", id); err != nil {
			t.Fatal(err)
		}
		res, _ := s.client(t, clientOptions{token: user.token}).GetCurrentUserWithResponse(ctx)
		expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 401, "unauthorized")
	})

	t.Run("revokes only the current session on logout", func(t *testing.T) {
		user := s.signUp(t)
		second, _ := s.client(t, clientOptions{}).CreateSessionWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(user.email), Password: testPassword})
		out, _ := s.client(t, clientOptions{token: user.token}).DeleteCurrentSessionWithResponse(ctx)
		if out.StatusCode() != 204 {
			t.Fatalf("logout: %d", out.StatusCode())
		}
		gone, _ := s.client(t, clientOptions{token: user.token}).GetCurrentUserWithResponse(ctx)
		if gone.StatusCode() != 401 {
			t.Fatalf("revoked token still works: %d", gone.StatusCode())
		}
		other, _ := s.client(t, clientOptions{token: second.JSON201.Token}).GetCurrentUserWithResponse(ctx)
		if other.StatusCode() != 200 {
			t.Fatalf("other session: %d", other.StatusCode())
		}
	})
}
