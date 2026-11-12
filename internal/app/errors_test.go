package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestErrorHandlerTreatsAGoneClientAsNoError(t *testing.T) {
	var logs strings.Builder
	handle := newErrorHandler(slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := httptest.NewRecorder()
	handle(w, httptest.NewRequest("GET", "/v1/notes", nil).WithContext(ctx), fmt.Errorf("list notes: %w", context.Canceled))
	if w.Code != 499 || logs.Len() != 0 {
		t.Fatalf("%d, logs: %s", w.Code, logs.String())
	}

	w = httptest.NewRecorder()
	handle(w, httptest.NewRequest("GET", "/v1/notes", nil), fmt.Errorf("list notes: %w", context.Canceled))
	if w.Code != 500 || !strings.Contains(logs.String(), "Unhandled error") {
		t.Fatalf("a cancellation the client didn't cause was hidden: %d, logs: %s", w.Code, logs.String())
	}
}

func TestErrorHandlerMapsConstraintViolations(t *testing.T) {
	var logs strings.Builder
	handle := newErrorHandler(slog.New(slog.NewJSONHandler(&logs, nil)))
	for code, want := range map[string]struct {
		status int
		slug   string
	}{
		"23505": {409, "conflict"},
		"23503": {409, "conflict"},
		"23514": {422, "validation-failed"},
		"42P01": {500, "internal-error"},
	} {
		w := httptest.NewRecorder()
		cause := &pgconn.PgError{Code: code, ConstraintName: "uq_secret_constraint", Message: "duplicate key in notes_secret_table"}
		handle(w, httptest.NewRequest("POST", "/v1/notes", nil), fmt.Errorf("insert note: %w", cause))
		body := w.Body.String()
		if w.Code != want.status || !strings.Contains(body, `"type":"urn:problem:`+want.slug+`"`) {
			t.Errorf("%s: %d %s", code, w.Code, body)
		}
		if strings.Contains(body, "secret") {
			t.Errorf("%s leaks the constraint or SQL: %s", code, body)
		}
	}
}
