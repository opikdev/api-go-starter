package app

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecovery(t *testing.T) {
	t.Run("answers a panic with a logged internal-error problem", func(t *testing.T) {
		var logs strings.Builder
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		handler := withRequestID(withAccessLog(logger, withRecovery(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}))))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/notes", nil))

		if w.Code != 500 || !strings.Contains(w.Body.String(), `"type":"urn:problem:internal-error"`) || strings.Contains(w.Body.String(), "boom") {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		id := w.Header().Get("X-Request-Id")
		for _, want := range []string{`"msg":"Panic recovered"`, `"err":"panic: boom"`, `"stack":"goroutine`, `"requestId":"` + id + `"`, `"status":500`} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("logs lack %s: %s", want, logs.String())
			}
		}
	})

	t.Run("lets http.ErrAbortHandler through", func(t *testing.T) {
		handler := withRecovery(slog.New(slog.DiscardHandler), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		}))
		defer func() {
			if err, ok := recover().(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				t.Fatalf("recovered %v", err)
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		t.Fatal("the abort was swallowed")
	})
}
