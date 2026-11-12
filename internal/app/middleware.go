package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/opikdev/api-go-starter/internal/problem"
)

const maxBodyBytes = 100 * 1024

type contextKey int

const (
	requestIDKey contextKey = iota
	authKey
)

// AuthenticatedUser is the caller behind a valid session token.
type AuthenticatedUser struct {
	ID        uuid.UUID
	Email     string
	CreatedAt time.Time
	SessionID uuid.UUID
}

type authState struct{ user *AuthenticatedUser }

func currentUser(ctx context.Context) AuthenticatedUser {
	state, _ := ctx.Value(authKey).(*authState)
	if state == nil || state.user == nil {
		panic("currentUser called on an unauthenticated route; check its security in api/openapi.yaml")
	}
	return *state.user
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 16)
		_, _ = rand.Read(raw)
		id := hex.EncodeToString(raw)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func withAccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Info("Request completed",
			"requestId", requestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"durationMs", time.Since(started).Milliseconds(),
		)
	})
}

// withRecovery turns a panic into a logged, traced internal-error response.
func withRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(recovered)
			}
			err := fmt.Errorf("panic: %v", recovered)
			logger.Error("Panic recovered", "requestId", requestID(r.Context()), "err", err, "stack", string(debug.Stack()))
			span := trace.SpanFromContext(r.Context())
			span.RecordError(err, trace.WithStackTrace(true))
			span.SetStatus(codes.Error, "panic")
			problem.Write(w, r, internalError(r))
		}()
		next.ServeHTTP(w, r)
	})
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Strict-Transport-Security", "max-age=15552000; includeSubDomains")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// exposedHeaders lists every response header a browser client may read beyond the CORS-safelisted ones.
const exposedHeaders = "ETag, Location, X-Request-Id, Retry-After, RateLimit, RateLimit-Policy, Idempotency-Replayed, WWW-Authenticate"

func withCORS(origins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && strings.HasPrefix(r.URL.Path, "/v1/") && slices.Contains(origins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, If-Match")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Access-Control-Expose-Headers", exposedHeaders)
		}
		next.ServeHTTP(w, r)
	})
}

type discardBody struct{ http.ResponseWriter }

func (discardBody) Write(b []byte) (int, error) { return len(b), nil }

// withHeadAsGet answers HEAD like GET without a body, so the contract only needs GET routes.
func withHeadAsGet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		get := r.Clone(r.Context())
		get.Method = http.MethodGet
		next.ServeHTTP(discardBody{w}, get)
	})
}

func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBodyBytes {
			problem.Write(w, r, problem.New(problem.PayloadTooLarge, fmt.Sprintf("The request body is over the %d-byte limit.", maxBodyBytes)))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func withSpanName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Pattern != "" {
			trace.SpanFromContext(r.Context()).SetName(r.Pattern)
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP takes the address the trusted proxy nearest the client saw: the hops-th
// X-Forwarded-For entry from the right. With no trusted proxies, or an unusable entry, it uses RemoteAddr.
func clientIP(r *http.Request, hops int) string {
	if hops > 0 {
		var forwarded []string
		for _, value := range r.Header.Values("X-Forwarded-For") {
			for _, entry := range strings.Split(value, ",") {
				forwarded = append(forwarded, strings.TrimSpace(entry))
			}
		}
		if len(forwarded) > 0 {
			if addr, err := netip.ParseAddr(forwarded[max(0, len(forwarded)-hops)]); err == nil {
				return addr.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
