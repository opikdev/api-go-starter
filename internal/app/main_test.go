package app_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/opikdev/api-go-starter/internal/apiclient"
	"github.com/opikdev/api-go-starter/internal/app"
	"github.com/opikdev/api-go-starter/internal/config"
	"github.com/opikdev/api-go-starter/internal/database"
)

const testPassword = "correct-horse-battery"

var (
	appURL   string
	ownerURL string
	appPool  *pgxpool.Pool
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	owner, teardown, err := startDatabase(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start database:", err)
		return 1
	}
	defer teardown()
	ownerURL = owner

	if err := database.Migrate(ctx, ownerURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	parsed, _ := url.Parse(ownerURL)
	parsed.User = url.UserPassword("api_app", "api_app_dev_password")
	appURL = parsed.String()
	appPool, err = database.Open(ctx, appURL, 5000, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer appPool.Close()
	return m.Run()
}

func initSQL() (string, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docker", "postgres", "init.sql"))
	return string(data), err
}

func startDatabase(ctx context.Context) (string, func(), error) {
	sql, err := initSQL()
	if err != nil {
		return "", nil, err
	}
	if admin := os.Getenv("TEST_DATABASE_URL"); admin != "" {
		return temporaryDatabase(ctx, admin, sql)
	}
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("api"),
		postgres.WithUsername("api_owner"),
		postgres.WithPassword("api_owner_dev_password"),
		postgres.WithInitScripts(filepath.Join("..", "..", "docker", "postgres", "init.sql")),
		testcontainers.WithEnv(map[string]string{"PGOPTIONS": "-c api.app_password=api_app_dev_password"}),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", nil, err
	}
	connection, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", nil, err
	}
	return connection, func() { _ = container.Terminate(context.Background()) }, nil
}

func temporaryDatabase(ctx context.Context, admin, initSQL string) (string, func(), error) {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = conn.Close(ctx) }()
	var version int
	if err := conn.QueryRow(ctx, "select current_setting('server_version_num')::int").Scan(&version); err != nil {
		return "", nil, err
	}
	if version < 180000 {
		return "", nil, fmt.Errorf("TEST_DATABASE_URL must point to Postgres 18 or later, which has uuidv7()")
	}
	name := "api_test_" + randomHex(6)
	if _, err := conn.Exec(ctx, "create database "+name); err != nil {
		return "", nil, err
	}
	parsed, _ := url.Parse(admin)
	parsed.Path = "/" + name
	target := parsed.String()
	created, err := pgx.Connect(ctx, target)
	if err != nil {
		return "", nil, err
	}
	_, err = created.Exec(ctx, "set api.app_password = 'api_app_dev_password';\n"+initSQL)
	_ = created.Close(ctx)
	if err != nil {
		return "", nil, err
	}
	return target, func() {
		cleanup, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = cleanup.Exec(context.Background(), "drop database if exists "+name+" with (force)")
			_ = cleanup.Close(context.Background())
		}
	}, nil
}

func randomIP() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	copy(raw[:], []byte{0x20, 0x01, 0x0d, 0xb8})
	return netip.AddrFrom16(raw).String()
}

func randomHex(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

type testServer struct {
	*httptest.Server
	app  *app.Server
	logs *strings.Builder
}

func newServer(t *testing.T, overrides map[string]string) *testServer {
	t.Helper()
	env := map[string]string{
		"DATABASE_URL":               appURL,
		"RATE_LIMIT_PER_MINUTE":      "100000",
		"AUTH_RATE_LIMIT_PER_MINUTE": "100000",
		"DOCS_ENABLED":               "true",
	}
	for key, value := range overrides {
		env[key] = value
	}
	cfg, err := config.Load(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewJSONHandler(&lockedWriter{w: logs}, nil))
	pool := appPool
	if url, ok := overrides["DATABASE_URL"]; ok {
		pool, err = database.Open(context.Background(), url, 200, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
	}
	handler, err := app.New(cfg, pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testServer{Server: server, app: handler, logs: logs}
}

type lockedWriter struct {
	w  io.Writer
	mu chan struct{}
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	if l.mu == nil {
		l.mu = make(chan struct{}, 1)
	}
	l.mu <- struct{}{}
	defer func() { <-l.mu }()
	return l.w.Write(p)
}

type clientOptions struct {
	token string
	ip    string
}

func (s *testServer) client(t *testing.T, opts clientOptions) *apiclient.ClientWithResponses {
	t.Helper()
	ip := opts.ip
	if ip == "" {
		ip = randomIP()
	}
	client, err := apiclient.NewClientWithResponses(s.URL, apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		if r.Header.Get("X-Forwarded-For") == "" {
			r.Header.Set("X-Forwarded-For", ip)
		}
		if opts.token != "" {
			r.Header.Set("Authorization", "Bearer "+opts.token)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type signedUp struct {
	email  string
	token  string
	userID string
}

func uniqueEmail() string { return "user-" + randomHex(8) + "@example.com" }

func (s *testServer) signUp(t *testing.T) signedUp {
	t.Helper()
	ctx := context.Background()
	email := uniqueEmail()
	c := s.client(t, clientOptions{})
	created, err := c.CreateUserWithResponse(ctx, apiclient.Credentials{Email: openapi_types.Email(email), Password: testPassword})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("sign up: %v %s", err, created.Body)
	}
	session, err := c.CreateSessionWithResponse(ctx, apiclient.CreateSession{Email: openapi_types.Email(email), Password: testPassword})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("log in: %v %s", err, session.Body)
	}
	return signedUp{email: email, token: *session.JSON201.Token, userID: created.JSON201.Id}
}

type rawResponse struct {
	status  int
	headers http.Header
	body    string
}

func (s *testServer) raw(t *testing.T, method, path, body string, headers map[string]string) rawResponse {
	t.Helper()
	request, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Forwarded-For", randomIP())
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
	return rawResponse{status: response.StatusCode, headers: response.Header, body: string(data)}
}

func expectProblem(t *testing.T, status int, contentType string, body []byte, wantStatus int, slug string) {
	t.Helper()
	if status != wantStatus || !strings.Contains(string(body), `"type":"urn:problem:`+slug+`"`) {
		t.Fatalf("want %d %s, got %d: %s", wantStatus, slug, status, body)
	}
	if !strings.HasPrefix(contentType, "application/problem+json") {
		t.Fatalf("content type = %q", contentType)
	}
}

func expectFieldError(t *testing.T, body, field, detail string) {
	t.Helper()
	var parsed struct {
		Errors []struct{ Field, Detail string } `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, e := range parsed.Errors {
		if e.Field == field && e.Detail == detail {
			return
		}
	}
	t.Fatalf("want %s %q in %s", field, detail, body)
}
