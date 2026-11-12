package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestLogHandlerAddsTraceContext(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(LogHandler(slog.NewJSONHandler(&logs, nil))).With("requestId", "abc")
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36},
		SpanID:  trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7},
	})

	logger.InfoContext(trace.ContextWithSpanContext(context.Background(), span), "traced")
	logger.InfoContext(context.Background(), "untraced")

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if !strings.Contains(lines[0], `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"`) || !strings.Contains(lines[0], `"requestId":"abc"`) {
		t.Fatalf("traced line = %s", lines[0])
	}
	if strings.Contains(lines[1], "trace_id") {
		t.Fatalf("untraced line = %s", lines[1])
	}
}
