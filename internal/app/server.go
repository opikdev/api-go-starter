// Package app wires the HTTP server: middleware, validation, auth, and handlers.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/config"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/store"
)

var errUnauthorized = errors.New("missing or invalid session token")

var publicAuthOperations = map[string]bool{"CreateUser": true, "CreateSession": true}

// Server is the API's HTTP handler and its shared state.
type Server struct {
	handler http.Handler
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// New builds the HTTP handler for the API.
func New(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*Server, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load OpenAPI spec: %w", err)
	}
	spec.Servers = nil
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI spec: %w", err)
	}

	h, err := newHandlers(pool, cfg.SessionTTLHours)
	if err != nil {
		return nil, err
	}
	queries := store.New(pool)
	limiter := rateLimiter{queries: queries, trustProxy: cfg.TrustProxy}
	errorHandler := newErrorHandler(logger)

	authLimit := func(next api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		if !publicAuthOperations[operationID] {
			return next
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if err := limiter.check(r, w, "auth", cfg.AuthRateLimitPerMinute); err != nil {
				return nil, err
			}
			return next(ctx, w, r, request)
		}
	}
	strict := api.NewStrictHandlerWithOptions(h, []api.StrictMiddlewareFunc{authLimit}, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  func(w http.ResponseWriter, r *http.Request, err error) { errorHandler(w, r, invalidRequest(err)) },
		ResponseErrorHandlerFunc: errorHandler,
	})

	routes := http.NewServeMux()
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter:       routes,
		Middlewares:      []api.MiddlewareFunc{withSpanName},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { errorHandler(w, r, invalidRequest(err)) },
	})

	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{
			MultiError:              true,
			SchemaValidationOptions: schemaValidationOptions,
			AuthenticationFunc: func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
				return authenticate(ctx, queries, input.RequestValidationInput.Request)
			},
		},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, _ nethttpmiddleware.ErrorHandlerOpts) {
			errorHandler(w, r, fromValidation(err))
		},
	})

	apiHandler := validator(routes)
	v1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		if err := limiter.check(r, w, "global", cfg.RateLimitPerMinute); err != nil {
			errorHandler(w, r, err)
			return
		}
		withBodyLimit(apiHandler).ServeHTTP(w, r)
	})

	top := http.NewServeMux()
	top.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(specJSON)
	})
	top.HandleFunc("GET /docs", serveDocs)
	top.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &authState{}
		v1.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authKey, state)))
	}))

	handler := withHeadAsGet(top)
	handler = withCORS(cfg.CORSOrigins, handler)
	handler = withSecurityHeaders(handler)
	handler = withRecovery(logger, handler)
	handler = withAccessLog(logger, handler)
	handler = withRequestID(handler)
	handler = otelhttp.NewHandler(handler, "http.request")
	return &Server{handler: handler}, nil
}

func authenticate(ctx context.Context, queries *store.Queries, r *http.Request) error {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || token == "" {
		return errUnauthorized
	}
	session, err := queries.FindActiveSession(ctx, auth.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	state, _ := r.Context().Value(authKey).(*authState)
	state.user = &AuthenticatedUser{ID: session.ID, Email: session.Email, CreatedAt: session.CreatedAt, SessionID: session.SessionID}
	return nil
}

func newErrorHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var p *problem.Error
		if errors.As(err, &p) {
			if !problem.Known(p.Slug) {
				logger.Error("Unknown problem type, sent as internal-error", "requestId", requestID(r.Context()), "slug", p.Slug)
			} else if p.Cause != nil && p.Status() >= http.StatusInternalServerError {
				logger.Warn(p.Detail, "requestId", requestID(r.Context()), "err", p.Cause)
			}
			problem.Write(w, r, p)
			return
		}
		logger.Error("Unhandled error", "requestId", requestID(r.Context()), "err", err)
		span := trace.SpanFromContext(r.Context())
		span.RecordError(err)
		span.SetStatus(codes.Error, "unhandled error")
		problem.Write(w, r, internalError(r))
	}
}

func internalError(r *http.Request) *problem.Error {
	return problem.New(problem.InternalError,
		fmt.Sprintf("Something went wrong on our side. Retry, and quote request %s if it persists.", requestID(r.Context())))
}

const docsPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>API Go starter</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.34.1/swagger-ui.css"></head>
<body><div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.34.1/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({ url: '/openapi.json', dom_id: '#swagger-ui' });</script>
</body></html>`

func serveDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsPage))
}
