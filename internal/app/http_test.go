package app_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"sigs.k8s.io/yaml"
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

	withCORS := newServer(t, map[string]string{"CORS_ORIGINS": "https://app.example"})
	t.Run("allows a listed origin", func(t *testing.T) {
		res := withCORS.raw(t, "GET", "/v1/notes", "", map[string]string{"Origin": "https://app.example"})
		if res.headers.Get("Access-Control-Allow-Origin") != "https://app.example" {
			t.Fatalf("headers = %v", res.headers)
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
			public := op.OperationID == "createUser" || op.OperationID == "createSession"
			if !public && (op.Security == nil || len(*op.Security) == 0 || op.Responses.Value("401") == nil) {
				t.Errorf("%s %s must require bearerAuth and document 401", method, path)
			}
		}
	}

	docs := s.raw(t, "GET", "/docs", "", nil)
	if docs.status != 200 || !strings.Contains(docs.body, "swagger-ui") {
		t.Fatalf("docs = %d", docs.status)
	}
}
