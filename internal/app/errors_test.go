package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
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
