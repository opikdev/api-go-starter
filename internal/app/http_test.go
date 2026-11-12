package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"sigs.k8s.io/yaml"

	"github.com/opikdev/api-go-starter/internal/app"
	"github.com/opikdev/api-go-starter/internal/config"
)

func TestErrorResponses(t *testing.T) {
	s := newServer(t, nil)

	t.Run("returns Problem Details for an unknown route", func(t *testing.T) {
		res := s.raw(t, "GET", "/v1/nope", "", nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 404, "not-found")
		if !strings.Contains(res.body, `"instance":"/v1/nope"`) {
			t.Fatalf("body = %s", res.body)
		}
	})

	t.Run("rejects an unsupported method", func(t *testing.T) {
		res := s.raw(t, "PUT", "/v1/notes", `{}`, nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 405, "method-not-allowed")
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/users", `{ nope`, nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 400, "validation-failed")
	})

	t.Run("rejects an oversized body", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/users", strings.Repeat("x", 100*1024+1), nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 413, "payload-too-large")
	})

	t.Run("rejects an oversized chunked body with 413 on a protected route", func(t *testing.T) {
		token := s.signUp(t).token
		for _, path := range []string{"/v1/notes", "/v1/users"} {
			body := io.MultiReader(strings.NewReader(`{"title":"`), strings.NewReader(strings.Repeat("x", 200*1024)), strings.NewReader(`"}`))
			request, err := http.NewRequest("POST", s.URL+path, body)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if request.ContentLength != 0 {
				t.Fatalf("the request has a Content-Length of %d, so it isn't chunked", request.ContentLength)
			}
			expectProblem(t, response.StatusCode, response.Header.Get("Content-Type"), data, 413, "payload-too-large")
		}
		if strings.Contains(s.logs.String(), `"level":"ERROR"`) {
			t.Fatalf("a client mistake was logged as an error: %s", s.logs.String())
		}
	})

	for label, contentType := range map[string]string{"text/plain": "text/plain", "a form, as curl -d sends by default": "application/x-www-form-urlencoded"} {
		t.Run("rejects "+label+" with 415, not 500", func(t *testing.T) {
			token := s.signUp(t).token
			res := s.raw(t, "POST", "/v1/notes", "title=x", map[string]string{"Authorization": "Bearer " + token, "Content-Type": contentType})
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 415, "unsupported-media-type")
			if strings.Contains(s.logs.String(), "Unhandled error") {
				t.Fatal("a client mistake was logged as a server error")
			}
		})
	}

	t.Run("answers HEAD like GET, without a body", func(t *testing.T) {
		head := s.raw(t, "HEAD", "/healthz", "", nil)
		get := s.raw(t, "GET", "/healthz", "", nil)
		if head.status != get.status || head.headers.Get("Content-Type") != get.headers.Get("Content-Type") || head.body != "" {
			t.Fatalf("HEAD %d %q vs GET %d", head.status, head.body, get.status)
		}
	})

	t.Run("hides internals and logs the real error", func(t *testing.T) {
		broken := newServer(t, map[string]string{"DATABASE_URL": "postgres://nobody:wrong-secret@127.0.0.1:1/none"})
		res := broken.raw(t, "GET", "/v1/notes", "", nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 500, "internal-error")
		if !strings.Contains(res.body, "Something went wrong on our side") || strings.Contains(res.body, "wrong-secret") || strings.Contains(res.body, "connect") {
			t.Fatalf("body leaks internals: %s", res.body)
		}
		if !strings.Contains(broken.logs.String(), "Unhandled error") {
			t.Fatal("the real error wasn't logged")
		}
	})
}

func TestHeadersAndCORS(t *testing.T) {
	s := newServer(t, nil)

	t.Run("sets security headers and a request id", func(t *testing.T) {
		res := s.raw(t, "GET", "/healthz", "", nil)
		if res.headers.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(res.headers.Get("Strict-Transport-Security"), "max-age") || res.headers.Get("X-Request-Id") == "" {
			t.Fatalf("headers = %v", res.headers)
		}
	})

	t.Run("sends a strict CSP and keeps /v1 responses out of caches", func(t *testing.T) {
		health := s.raw(t, "GET", "/healthz", "", nil)
		notes := s.raw(t, "GET", "/v1/notes", "", nil)
		for _, res := range []rawResponse{health, notes} {
			if res.headers.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
				t.Fatalf("CSP = %q", res.headers.Get("Content-Security-Policy"))
			}
		}
		if notes.status != 401 || notes.headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("%d Cache-Control = %q", notes.status, notes.headers.Get("Cache-Control"))
		}
		token := s.signUp(t).token
		if res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"Authorization": "Bearer " + token}); res.headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("Cache-Control = %q", res.headers.Get("Cache-Control"))
		}
	})

	withCORS := newServer(t, map[string]string{"CORS_ORIGINS": "https://app.example"})
	t.Run("allows a listed origin", func(t *testing.T) {
		res := withCORS.raw(t, "GET", "/v1/notes", "", map[string]string{"Origin": "https://app.example"})
		if res.headers.Get("Access-Control-Allow-Origin") != "https://app.example" {
			t.Fatalf("headers = %v", res.headers)
		}
	})
	t.Run("exposes the headers a browser client reads", func(t *testing.T) {
		res := withCORS.raw(t, "GET", "/v1/notes", "", map[string]string{"Origin": "https://app.example"})
		exposed := strings.Split(res.headers.Get("Access-Control-Expose-Headers"), ", ")
		for _, header := range []string{"ETag", "Location", "X-Request-Id", "Retry-After", "RateLimit", "RateLimit-Policy", "Idempotency-Replayed", "WWW-Authenticate"} {
			if !slices.Contains(exposed, header) {
				t.Errorf("%s isn't exposed: %v", header, exposed)
			}
		}
	})
	t.Run("answers a preflight", func(t *testing.T) {
		res := withCORS.raw(t, "OPTIONS", "/v1/notes", "", map[string]string{"Origin": "https://app.example", "Access-Control-Request-Method": "POST"})
		if res.status != 204 || !strings.Contains(res.headers.Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Fatalf("%d %v", res.status, res.headers)
		}
	})
	t.Run("never allows an unlisted origin", func(t *testing.T) {
		res := withCORS.raw(t, "GET", "/v1/notes", "", map[string]string{"Origin": "https://evil.example"})
		if value := res.headers.Get("Access-Control-Allow-Origin"); value != "" {
			t.Fatalf("allowed %q", value)
		}
	})
	t.Run("sends no CORS headers by default", func(t *testing.T) {
		res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"Origin": "https://app.example"})
		if res.headers.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS on by default")
		}
	})
}

func TestHealth(t *testing.T) {
	s := newServer(t, nil)
	if res := s.raw(t, "GET", "/healthz", "", nil); res.status != 200 || strings.TrimSpace(res.body) != `{"status":"ok"}` {
		t.Fatalf("%d %s", res.status, res.body)
	}
	if res := s.raw(t, "GET", "/readyz", "", nil); res.status != 200 {
		t.Fatalf("readyz = %d", res.status)
	}

	broken := newServer(t, map[string]string{"DATABASE_URL": "postgres://nobody:nope@127.0.0.1:1/none"})
	ready := broken.raw(t, "GET", "/readyz", "", nil)
	expectProblem(t, ready.status, ready.headers.Get("Content-Type"), []byte(ready.body), 503, "service-unavailable")
	if ready.headers.Get("Retry-After") != "5" {
		t.Fatalf("Retry-After = %q", ready.headers.Get("Retry-After"))
	}
	if res := broken.raw(t, "GET", "/healthz", "", nil); res.status != 200 {
		t.Fatal("liveness failed while Postgres is down")
	}
}

func TestDraining(t *testing.T) {
	s := newServer(t, nil)
	token := s.signUp(t).token
	s.app.Drain()

	ready := s.raw(t, "GET", "/readyz", "", nil)
	expectProblem(t, ready.status, ready.headers.Get("Content-Type"), []byte(ready.body), 503, "service-unavailable")
	if res := s.raw(t, "GET", "/healthz", "", nil); res.status != 200 {
		t.Fatalf("healthz = %d while draining", res.status)
	}
	if res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"Authorization": "Bearer " + token}); res.status != 200 {
		t.Fatalf("a request while draining got %d: %s", res.status, res.body)
	}
}

func TestLogging(t *testing.T) {
	s := newServer(t, nil)
	user := s.signUp(t)
	s.raw(t, "GET", "/v1/users/me", "", map[string]string{"Authorization": "Bearer " + user.token})
	logs := s.logs.String()
	if strings.Contains(logs, user.token) || strings.Contains(logs, testPassword) {
		t.Fatal("logs contain a secret")
	}
	if !strings.Contains(logs, `"msg":"Request completed"`) || !strings.Contains(logs, `"status":200`) || !strings.Contains(logs, `"requestId"`) {
		t.Fatalf("logs = %s", logs)
	}
}

func TestOpenAPI(t *testing.T) {
	s := newServer(t, nil)
	res := s.raw(t, "GET", "/openapi.json", "", nil)
	var served map[string]any
	if err := json.Unmarshal([]byte(res.body), &served); err != nil {
		t.Fatal(err)
	}
	if served["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v", served["openapi"])
	}

	source, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("api/openapi.yaml is invalid: %v", err)
	}
	asJSON, _ := yaml.YAMLToJSON(source)
	var fromFile map[string]any
	_ = json.Unmarshal(asJSON, &fromFile)
	if len(fromFile["paths"].(map[string]any)) != len(served["paths"].(map[string]any)) {
		t.Fatal("the served spec doesn't match api/openapi.yaml. Run go generate ./...")
	}

	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if op.OperationID == "" || op.Summary == "" {
				t.Errorf("%s %s needs an operationId and summary", method, path)
			}
			if !strings.HasPrefix(path, "/v1") {
				continue
			}
			if op.Responses.Value("429") == nil {
				t.Errorf("%s %s doesn't document 429", method, path)
			}
			if method != "GET" && op.Responses.Value("403") == nil {
				t.Errorf("%s %s changes data, so it must document 403 for blocked cross-origin requests", method, path)
			}
			public := op.OperationID == "createUser" || op.OperationID == "createSession"
			var alternatives []string
			if op.Security != nil {
				for _, requirement := range *op.Security {
					for name := range requirement {
						alternatives = append(alternatives, name)
					}
				}
			}
			if !public && (!slices.Equal(alternatives, []string{"bearerAuth", "cookieAuth"}) || op.Responses.Value("401") == nil) {
				t.Errorf("%s %s must accept bearerAuth or cookieAuth and document 401", method, path)
			}
		}
	}

}

func TestDocs(t *testing.T) {
	s := newServer(t, nil)

	t.Run("serves Swagger UI from this origin under a strict CSP", func(t *testing.T) {
		docs := s.raw(t, "GET", "/docs", "", nil)
		if docs.status != 200 || !strings.Contains(docs.body, `<script src="/docs/swagger-ui-bundle.js">`) || strings.Contains(docs.body, "https://") {
			t.Fatalf("docs = %d %s", docs.status, docs.body)
		}
		want := "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
		if docs.headers.Get("Content-Security-Policy") != want {
			t.Fatalf("CSP = %q", docs.headers.Get("Content-Security-Policy"))
		}
		for path, contentType := range map[string]string{"/docs/init.js": "text/javascript", "/docs/swagger-ui-bundle.js": "text/javascript", "/docs/swagger-ui.css": "text/css"} {
			res := s.raw(t, "GET", path, "", nil)
			if res.status != 200 || !strings.HasPrefix(res.headers.Get("Content-Type"), contentType) || len(res.body) == 0 {
				t.Errorf("%s = %d %q", path, res.status, res.headers.Get("Content-Type"))
			}
		}
	})

	t.Run("sends Swagger UI gzipped only to clients that accept it", func(t *testing.T) {
		plain := s.raw(t, "GET", "/docs/swagger-ui.css", "", map[string]string{"Accept-Encoding": "identity"})
		zipped := s.raw(t, "GET", "/docs/swagger-ui.css", "", map[string]string{"Accept-Encoding": "gzip"})
		if plain.headers.Get("Content-Encoding") != "" || !strings.Contains(plain.body, ".swagger-ui") {
			t.Fatalf("identity: %v", plain.headers)
		}
		if zipped.headers.Get("Content-Encoding") != "gzip" || !strings.HasPrefix(zipped.body, "\x1f\x8b") {
			t.Fatalf("gzip: %v", zipped.headers)
		}
	})

	t.Run("titles the page after the spec", func(t *testing.T) {
		source, err := os.ReadFile("../../api/openapi.yaml")
		if err != nil {
			t.Fatal(err)
		}
		spec, err := openapi3.NewLoader().LoadFromData(source)
		if err != nil {
			t.Fatal(err)
		}
		if docs := s.raw(t, "GET", "/docs", "", nil); !strings.Contains(docs.body, "<title>"+spec.Info.Title+"</title>") {
			t.Fatalf("docs = %s", docs.body)
		}
	})

	t.Run("hides the docs and spec unless DOCS_ENABLED is true", func(t *testing.T) {
		off := newServer(t, map[string]string{"DOCS_ENABLED": "false"})
		for _, path := range []string{"/docs", "/openapi.json", "/docs/swagger-ui-bundle.js"} {
			res := off.raw(t, "GET", path, "", nil)
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 404, "not-found")
		}
	})
}

func TestCrossOriginProtection(t *testing.T) {
	s := newServer(t, map[string]string{"CORS_ORIGINS": "https://app.example"})
	session := s.cookieLogin(t, s.signUp(t).email)

	for label, c := range map[string]struct {
		method  string
		headers map[string]string
		status  int
	}{
		"allows a safe method from another site":         {"GET", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, 200},
		"allows a trusted origin":                        {"POST", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://app.example"}, 201},
		"allows Sec-Fetch-Site same-origin":              {"POST", map[string]string{"Sec-Fetch-Site": "same-origin"}, 201},
		"allows Sec-Fetch-Site none":                     {"POST", map[string]string{"Sec-Fetch-Site": "none"}, 201},
		"blocks Sec-Fetch-Site same-site":                {"POST", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://www.app.example"}, 403},
		"blocks a cross-site cookie POST":                {"POST", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, 403},
		"allows a client that sends neither header":      {"POST", nil, 201},
		"allows an Origin that matches Host":             {"POST", map[string]string{"Origin": s.URL}, 201},
		"blocks an Origin that doesn't match Host":       {"POST", map[string]string{"Origin": "https://evil.example"}, 403},
		"blocks an untrusted Origin on an older browser": {"DELETE", map[string]string{"Origin": "https://app.example.evil"}, 403},
	} {
		t.Run(label, func(t *testing.T) {
			body := ""
			if c.method == "POST" {
				body = `{"title":"From a browser"}`
			}
			path := "/v1/notes"
			if c.method == "DELETE" {
				path = "/v1/sessions/current"
			}
			res := s.raw(t, c.method, path, body, withSession(session, c.headers))
			if c.status != 403 {
				if res.status != c.status {
					t.Fatalf("%d %s", res.status, res.body)
				}
				return
			}
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 403, "forbidden")
			if !strings.Contains(res.body, "Cross-origin requests that change data are blocked. Send the request from the same origin, or add the origin to CORS_ORIGINS.") {
				t.Fatalf("detail: %s", res.body)
			}
		})
	}

	t.Run("blocks a cross-site login before checking credentials", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/sessions", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"})
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 403, "forbidden")
	})

	t.Run("refuses to start with an origin it can't trust", func(t *testing.T) {
		cfg, err := config.Load(func(name string) string {
			return map[string]string{"DATABASE_URL": appURL, "CORS_ORIGINS": "https://app.example/"}[name]
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.New(cfg, appPool, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "CORS_ORIGINS") {
			t.Fatalf("err = %v", err)
		}
	})
}
