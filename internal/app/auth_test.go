package app_test

import (
	"context"
	"encoding/json"
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
		res, _ := s.client(t, clientOptions{}).CreateSessionWithResponse(ctx, apiclient.CreateSession{Email: openapi_types.Email(user.email), Password: testPassword})
		if res.StatusCode() != 201 || res.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %v", res.StatusCode(), res.HTTPResponse.Header)
		}
		if !res.JSON201.ExpiresAt.After(time.Now()) {
			t.Fatal("expiry is in the past")
		}
		var count int
		_ = appPool.QueryRow(ctx, "select count(*) from sessions where token_hash = $1", *res.JSON201.Token).Scan(&count)
		if count != 0 {
			t.Fatal("token stored in plain text")
		}
	})

	t.Run("gives a wrong password and an unknown email the same answer", func(t *testing.T) {
		user := s.signUp(t)
		for _, creds := range []apiclient.CreateSession{
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

	t.Run("matches the Bearer scheme case-insensitively", func(t *testing.T) {
		user := s.signUp(t)
		for _, scheme := range []string{"bearer", "BEARER"} {
			res := s.raw(t, "GET", "/v1/users/me", "", map[string]string{"Authorization": scheme + " " + user.token})
			if res.status != 200 {
				t.Fatalf("%s: %d %s", scheme, res.status, res.body)
			}
		}
	})

	for label, header := range map[string]string{"no token": "", "a wrong token": "Bearer not-a-token", "the wrong scheme": "Basic abc", "a scheme with no token": "Bearer"} {
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
		second, _ := s.client(t, clientOptions{}).CreateSessionWithResponse(ctx, apiclient.CreateSession{Email: openapi_types.Email(user.email), Password: testPassword})
		out, _ := s.client(t, clientOptions{token: user.token}).DeleteCurrentSessionWithResponse(ctx)
		if out.StatusCode() != 204 {
			t.Fatalf("logout: %d", out.StatusCode())
		}
		gone, _ := s.client(t, clientOptions{token: user.token}).GetCurrentUserWithResponse(ctx)
		if gone.StatusCode() != 401 {
			t.Fatalf("revoked token still works: %d", gone.StatusCode())
		}
		other, _ := s.client(t, clientOptions{token: *second.JSON201.Token}).GetCurrentUserWithResponse(ctx)
		if other.StatusCode() != 200 {
			t.Fatalf("other session: %d", other.StatusCode())
		}
	})
}

const clearedSessionCookie = "__Host-session=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict"

var sessionCookiePattern = regexp.MustCompile(`^__Host-session=([A-Za-z0-9_-]{43}); Path=/; Max-Age=604800; HttpOnly; Secure; SameSite=Strict$`)

func loginBody(email, delivery string) string {
	return `{"email":"` + email + `","password":"` + testPassword + `","delivery":"` + delivery + `"}`
}

func (s *testServer) cookieLogin(t *testing.T, email string) string {
	t.Helper()
	res := s.raw(t, "POST", "/v1/sessions", loginBody(email, "cookie"), nil)
	match := sessionCookiePattern.FindStringSubmatch(res.headers.Get("Set-Cookie"))
	if res.status != 201 || match == nil {
		t.Fatalf("%d %q %s", res.status, res.headers.Get("Set-Cookie"), res.body)
	}
	return match[1]
}

func withSession(value string, headers map[string]string) map[string]string {
	all := map[string]string{"Cookie": "__Host-session=" + value}
	for key, header := range headers {
		all[key] = header
	}
	return all
}

func TestCookieSessions(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()

	t.Run("logs in with a host-only HttpOnly cookie and no token in the body", func(t *testing.T) {
		user := s.signUp(t)
		res := s.raw(t, "POST", "/v1/sessions", loginBody(user.email, "cookie"), nil)
		if res.status != 201 || res.headers.Get("Cache-Control") != "no-store" || !sessionCookiePattern.MatchString(res.headers.Get("Set-Cookie")) {
			t.Fatalf("%d %v %s", res.status, res.headers, res.body)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(res.body), &body); err != nil {
			t.Fatal(err)
		}
		if _, hasToken := body["token"]; hasToken || body["expiresAt"] == nil {
			t.Fatalf("body = %s", res.body)
		}
	})

	t.Run("still returns the token in the body when delivery is body", func(t *testing.T) {
		user := s.signUp(t)
		res := s.raw(t, "POST", "/v1/sessions", loginBody(user.email, "body"), nil)
		var body struct {
			Token     string `json:"token"`
			ExpiresAt string `json:"expiresAt"`
		}
		_ = json.Unmarshal([]byte(res.body), &body)
		if res.status != 201 || body.Token == "" || body.ExpiresAt == "" || res.headers.Get("Set-Cookie") != "" || res.headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %v %s", res.status, res.headers, res.body)
		}
	})

	t.Run("issues a new session on every login", func(t *testing.T) {
		user := s.signUp(t)
		first, second := s.cookieLogin(t, user.email), s.cookieLogin(t, user.email)
		if first == second {
			t.Fatal("two logins share a session")
		}
	})

	t.Run("rejects an unknown delivery", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/sessions", loginBody("a@example.com", "header"), nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 400, "validation-failed")
		expectFieldError(t, res.body, "delivery", "must be one of [body cookie]")
	})

	t.Run("keeps delivery off registration", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/users", loginBody(uniqueEmail(), "cookie"), nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 400, "validation-failed")
		expectFieldError(t, res.body, "delivery", "isn't a known field")
	})

	t.Run("authenticates with only the cookie", func(t *testing.T) {
		user := s.signUp(t)
		res := s.raw(t, "GET", "/v1/users/me", "", withSession(s.cookieLogin(t, user.email), nil))
		if res.status != 200 || !strings.Contains(res.body, user.userID) {
			t.Fatalf("%d %s", res.status, res.body)
		}
	})

	t.Run("uses the Authorization header over the cookie", func(t *testing.T) {
		bearer, cookie := s.signUp(t), s.signUp(t)
		res := s.raw(t, "GET", "/v1/users/me", "", withSession(s.cookieLogin(t, cookie.email), map[string]string{"Authorization": "Bearer " + bearer.token}))
		if res.status != 200 || !strings.Contains(res.body, bearer.userID) {
			t.Fatalf("%d %s", res.status, res.body)
		}
	})

	t.Run("never falls back to the cookie when the header is invalid", func(t *testing.T) {
		user := s.signUp(t)
		res := s.raw(t, "GET", "/v1/users/me", "", withSession(s.cookieLogin(t, user.email), map[string]string{"Authorization": "Bearer not-a-token"}))
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 401, "unauthorized")
		if res.headers.Get("Set-Cookie") != "" {
			t.Fatalf("cleared a valid cookie: %q", res.headers.Get("Set-Cookie"))
		}
	})

	t.Run("rejects an expired cookie and clears it", func(t *testing.T) {
		user := s.signUp(t)
		session := s.cookieLogin(t, user.email)
		id, _ := publicid.Parse(publicid.User, user.userID)
		if _, err := appPool.Exec(ctx, "update sessions set expires_at = now() - interval '1 second' where user_id = $1", id); err != nil {
			t.Fatal(err)
		}
		res := s.raw(t, "GET", "/v1/users/me", "", withSession(session, nil))
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 401, "unauthorized")
		if res.headers.Get("Set-Cookie") != clearedSessionCookie || res.headers.Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("headers = %v", res.headers)
		}
		if !strings.Contains(res.body, "or log in with `delivery: \\\"cookie\\\"` from the same origin") {
			t.Fatalf("detail: %s", res.body)
		}
	})

	t.Run("logs out with the cookie, revokes the session, and clears the cookie", func(t *testing.T) {
		user := s.signUp(t)
		session := s.cookieLogin(t, user.email)
		out := s.raw(t, "DELETE", "/v1/sessions/current", "", withSession(session, nil))
		if out.status != 204 || out.headers.Get("Set-Cookie") != clearedSessionCookie {
			t.Fatalf("%d %v", out.status, out.headers)
		}
		gone := s.raw(t, "GET", "/v1/users/me", "", withSession(session, nil))
		expectProblem(t, gone.status, gone.headers.Get("Content-Type"), []byte(gone.body), 401, "unauthorized")
		if gone.headers.Get("Set-Cookie") != clearedSessionCookie {
			t.Fatalf("Set-Cookie = %q", gone.headers.Get("Set-Cookie"))
		}
	})

	t.Run("clears the cookie on a bearer logout too", func(t *testing.T) {
		out := s.raw(t, "DELETE", "/v1/sessions/current", "", map[string]string{"Authorization": "Bearer " + s.signUp(t).token})
		if out.status != 204 || out.headers.Get("Set-Cookie") != clearedSessionCookie {
			t.Fatalf("%d %v", out.status, out.headers)
		}
	})
}
