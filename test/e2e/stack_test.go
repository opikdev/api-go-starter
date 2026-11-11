//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/opikdev/api-go-starter/internal/apiclient"
)

const (
	port     = "3150"
	dbPort   = "5550"
	otlpPort = "4330"
	project  = "api-go-starter-e2e"
	baseURL  = "http://127.0.0.1:" + port
)

type collector struct {
	mu     sync.Mutex
	traces bytes.Buffer
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.URL.Path == "/v1/traces" {
		c.mu.Lock()
		c.traces.Write(body)
		c.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func (c *collector) contains(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.traces.Bytes(), []byte(text))
}

var traces = &collector{}

func compose(t *testing.T, args ...string) string {
	t.Helper()
	out, err := composeErr(args...)
	if err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, out)
	}
	return out
}

func composeErr(args ...string) (string, error) {
	cmd := exec.Command("docker", append([]string{"compose", "-f", "../../compose.yaml"}, args...)...)
	cmd.Env = append(os.Environ(),
		"COMPOSE_PROJECT_NAME="+project,
		"PORT="+port,
		"POSTGRES_PORT="+dbPort,
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:"+otlpPort,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s", timeout)
}

func status(path string) int {
	response, err := http.Get(baseURL + path)
	if err != nil {
		return 0
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func TestMain(m *testing.M) {
	listener, err := net.Listen("tcp", "0.0.0.0:"+otlpPort)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	go func() { _ = http.Serve(listener, traces) }()

	_, _ = composeErr("down", "--volumes", "--remove-orphans")
	if out, err := composeErr("up", "--build", "--detach", "--wait", "api"); err != nil {
		fmt.Fprintln(os.Stderr, out)
		os.Exit(1)
	}
	code := m.Run()
	_, _ = composeErr("down", "--volumes", "--remove-orphans")
	os.Exit(code)
}

func login(t *testing.T, client *apiclient.ClientWithResponses, email, password string) string {
	t.Helper()
	res, err := client.CreateSessionWithResponse(context.Background(), apiclient.CreateSession{Email: openapi_types.Email(email), Password: password})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("login: %v %s", err, res.Body)
	}
	return *res.JSON201.Token
}

func authed(t *testing.T, token string) *apiclient.ClientWithResponses {
	t.Helper()
	client, err := apiclient.NewClientWithResponses(baseURL, apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func send(t *testing.T, method, path, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	return response, string(data)
}

func TestStack(t *testing.T) {
	ctx := context.Background()
	anonymous, _ := apiclient.NewClientWithResponses(baseURL)

	t.Run("is ready after one command", func(t *testing.T) {
		if code := status("/readyz"); code != 200 {
			t.Fatalf("readyz = %d", code)
		}
	})

	t.Run("reports the API container as healthy and non-root", func(t *testing.T) {
		var state struct{ Health string }
		_ = json.Unmarshal([]byte(compose(t, "ps", "api", "--format", "json")), &state)
		if state.Health != "healthy" {
			t.Fatalf("health = %q", state.Health)
		}
		inspect, err := exec.Command("docker", "inspect", "--format", "{{.Config.User}}", project+"-api-1").Output()
		if err != nil || strings.TrimSpace(string(inspect)) != "nonroot:nonroot" {
			t.Fatalf("user = %q %v", inspect, err)
		}
	})

	t.Run("logs in with the seeded demo user and reads fake notes", func(t *testing.T) {
		token := login(t, anonymous, "demo@example.com", "demo-password-123")
		notes, _ := authed(t, token).ListNotesWithResponse(ctx, nil)
		if notes.JSON200 == nil || len(notes.JSON200.Data) != 2 {
			t.Fatalf("notes = %s", notes.Body)
		}
	})

	t.Run("runs the whole note lifecycle over real HTTP", func(t *testing.T) {
		email := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
		created, _ := anonymous.CreateUserWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(email), Password: "correct-horse-battery"})
		if created.StatusCode() != 201 {
			t.Fatalf("register: %s", created.Body)
		}
		client := authed(t, login(t, anonymous, email, "correct-horse-battery"))
		note, _ := client.CreateNoteWithResponse(ctx, nil, apiclient.CreateNote{Title: "From the outside"})
		title := "Changed"
		updated, _ := client.UpdateNoteWithResponse(ctx, note.JSON201.Id, nil, apiclient.UpdateNote{Title: &title},
			func(_ context.Context, r *http.Request) error { r.Header.Set("If-Match", `"1"`); return nil })
		if updated.JSON200 == nil || updated.JSON200.Version != 2 {
			t.Fatalf("update: %s", updated.Body)
		}
		if deleted, _ := client.DeleteNoteWithResponse(ctx, note.JSON201.Id); deleted.StatusCode() != 204 {
			t.Fatalf("delete: %d", deleted.StatusCode())
		}
	})

	t.Run("runs a cookie session for a same-origin browser app", func(t *testing.T) {
		login, body := send(t, "POST", "/v1/sessions", `{"email":"demo@example.com","password":"demo-password-123","delivery":"cookie"}`, nil)
		cookies := login.Cookies()
		if login.StatusCode != 201 || len(cookies) != 1 || strings.Contains(body, "token") {
			t.Fatalf("login: %d %v %s", login.StatusCode, login.Header, body)
		}
		session := cookies[0]
		if session.Name != "__Host-session" || session.Path != "/" || !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie = %+v", session)
		}
		cookie := map[string]string{"Cookie": session.Name + "=" + session.Value}
		if notes, body := send(t, "GET", "/v1/notes", "", cookie); notes.StatusCode != 200 {
			t.Fatalf("notes: %d %s", notes.StatusCode, body)
		}
		forged, body := send(t, "POST", "/v1/notes", `{"title":"Forged"}`, map[string]string{"Cookie": cookie["Cookie"], "Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"})
		if forged.StatusCode != 403 || !strings.Contains(body, "urn:problem:forbidden") {
			t.Fatalf("cross-site write: %d %s", forged.StatusCode, body)
		}
		if out, _ := send(t, "DELETE", "/v1/sessions/current", "", cookie); out.StatusCode != 204 || len(out.Cookies()) != 1 || out.Cookies()[0].MaxAge >= 0 {
			t.Fatalf("logout: %d %v", out.StatusCode, out.Header)
		}
		if gone, _ := send(t, "GET", "/v1/users/me", "", cookie); gone.StatusCode != 401 {
			t.Fatalf("revoked cookie still works: %d", gone.StatusCode)
		}
	})

	t.Run("writes structured JSON logs without credentials", func(t *testing.T) {
		logs := compose(t, "logs", "--no-log-prefix", "api")
		if strings.Contains(logs, "demo-password-123") || strings.Contains(logs, "Bearer") {
			t.Fatal("logs contain credentials")
		}
		found := false
		for _, line := range strings.Split(logs, "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "Request completed" && entry["requestId"] != nil {
				found = true
			}
		}
		if !found {
			t.Fatal("no structured request log")
		}
	})

	t.Run("exports route-named spans with database spans", func(t *testing.T) {
		waitFor(t, 20*time.Second, func() bool { return traces.contains("GET /v1/notes") && traces.contains("api-go-starter") })
		waitFor(t, 20*time.Second, func() bool { return traces.contains("SELECT") || traces.contains("query") })
	})

	t.Run("reports a database outage, records the error, and recovers", func(t *testing.T) {
		compose(t, "stop", "db")
		if code := status("/healthz"); code != 200 {
			t.Fatalf("healthz = %d during the outage", code)
		}
		if code := status("/readyz"); code != 503 {
			t.Fatalf("readyz = %d during the outage", code)
		}
		response, err := http.Get(baseURL + "/v1/notes")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 500 || !strings.Contains(string(body), "urn:problem:internal-error") || strings.Contains(string(body), "api_app") {
			t.Fatalf("outage response: %d %s", response.StatusCode, body)
		}
		waitFor(t, 20*time.Second, func() bool { return traces.contains("exception") })

		compose(t, "start", "db")
		waitFor(t, 60*time.Second, func() bool { return status("/readyz") == 200 })
	})

	t.Run("keeps data across a restart", func(t *testing.T) {
		compose(t, "restart", "api")
		waitFor(t, 30*time.Second, func() bool { return status("/readyz") == 200 })
		login(t, anonymous, "demo@example.com", "demo-password-123")
	})

	t.Run("shuts down cleanly on SIGTERM", func(t *testing.T) {
		compose(t, "stop", "api")
		if code := strings.TrimSpace(compose(t, "ps", "--all", "api", "--format", "{{.ExitCode}}")); code != "0" {
			t.Fatalf("exit code = %s", code)
		}
	})
}
