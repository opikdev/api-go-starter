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
	"github.com/jackc/pgx/v5/pgconn"
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

// statusClientClosedRequest is nginx's convention, so access logs tell a client that left apart from a server error.
const statusClientClosedRequest = 499

var (
	errUnauthorized = errors.New("missing or invalid session token")
	errStaleCookie  = fmt.Errorf("%w: invalid or expired session cookie", errUnauthorized)
)

const crossOriginDetail = "Cross-origin requests that change data are blocked. Send the request from the same origin, or add the origin to CORS_ORIGINS."

var publicAuthOperations = map[string]bool{"CreateUser": true, "CreateSession": true}

// Server is the API's HTTP handler and its shared state.
type Server struct {
	handler  http.Handler
	handlers *handlers
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Drain makes /readyz return 503, so load balancers stop sending new requests, while the server keeps
// answering the ones that still arrive.
func (s *Server) Drain() { s.handlers.draining.Store(true) }

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

	queries := store.New(pool)
	limiter := rateLimiter{
		queries:        queries,
		trustProxyHops: cfg.TrustProxyHops,
		global:         rateLimitPolicy{name: "global", quota: cfg.RateLimitPerMinute, windowSeconds: 60},
		auth:           rateLimitPolicy{name: "auth", quota: cfg.AuthRateLimitPerMinute, windowSeconds: 60},
		login:          rateLimitPolicy{name: "login", quota: cfg.LoginFailureLimit, windowSeconds: cfg.LoginFailureWindowSeconds},
	}
	h, err := newHandlers(pool, cfg.SessionTTLHours, limiter)
	if err != nil {
		return nil, err
	}
	errorHandler := newErrorHandler(logger)

	authLimit := func(next api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		if !publicAuthOperations[operationID] {
			return next
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if err := limiter.check(w, r, limiter.auth); err != nil {
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
				return authenticate(ctx, queries, input.RequestValidationInput.Request, input.SecuritySchemeName)
			},
		},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, _ nethttpmiddleware.ErrorHandlerOpts) {
			errorHandler(w, r, fromValidation(err))
		},
	})

	crossOrigin := http.NewCrossOriginProtection()
	for _, origin := range cfg.CORSOrigins {
		if err := crossOrigin.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("CORS_ORIGINS: %w", err)
		}
	}
	crossOrigin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		problem.Write(w, r, problem.New(problem.Forbidden, crossOriginDetail))
	}))

	apiHandler := validator(routes)
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := limiter.check(w, r, limiter.global); err != nil {
			errorHandler(w, r, err)
			return
		}
		withBodyLimit(apiHandler).ServeHTTP(w, r)
	})
	protected := crossOrigin.Handler(limited)
	v1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r.WithContext(withRateLimitHeaders(r.Context())))
	})

	top := http.NewServeMux()
	if cfg.DocsEnabled {
		if err := registerDocs(top, spec.Info.Title, specJSON); err != nil {
			return nil, err
		}
	}
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
	return &Server{handler: handler, handlers: h}, nil
}

// authenticate checks one security scheme. A request with an Authorization header
// can only pass bearerAuth, so a bad header never falls back to the cookie.
func authenticate(ctx context.Context, queries *store.Queries, r *http.Request, scheme string) error {
	header := r.Header.Get("Authorization")
	authScheme, token, found := strings.Cut(header, " ")
	found = found && strings.EqualFold(authScheme, "Bearer")
	failure := errUnauthorized
	if scheme == "cookieAuth" {
		cookie, err := r.Cookie(sessionCookieName)
		if header != "" || err != nil {
			return errUnauthorized
		}
		token, found, failure = cookie.Value, true, errStaleCookie
	}
	if !found || token == "" {
		return failure
	}
	session, err := queries.FindActiveSession(ctx, auth.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return failure
	}
	if err != nil {
		return fmt.Errorf("find session: %w", err)
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
		if p := constraintProblem(err); p != nil {
			logger.WarnContext(r.Context(), "A database constraint rejected the request", "requestId", requestID(r.Context()), "err", err)
			problem.Write(w, r, p)
			return
		}
		if r.Context().Err() != nil && errors.Is(err, context.Canceled) {
			logger.Debug("Client closed the request", "requestId", requestID(r.Context()), "err", err)
			w.WriteHeader(statusClientClosedRequest)
			return
		}
		logger.Error("Unhandled error", "requestId", requestID(r.Context()), "err", err)
		span := trace.SpanFromContext(r.Context())
		span.RecordError(err)
		span.SetStatus(codes.Error, "unhandled error")
		problem.Write(w, r, internalError(r))
	}
}

// Postgres SQLSTATE codes for constraint violations.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// constraintProblem maps a constraint violation that no handler expected to a problem, without naming the
// constraint or quoting SQL.
func constraintProblem(err error) *problem.Error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	switch pgErr.Code {
	case pgUniqueViolation, pgForeignKeyViolation:
		return problem.New(problem.Conflict, "The change conflicts with the current data, such as a record another request just created or deleted. Fetch the current state, then retry.")
	case pgCheckViolation:
		return &problem.Error{
			Slug:       problem.ValidationFailed,
			Detail:     "A value breaks a data rule. Check the fields against the API docs, then retry.",
			HTTPStatus: http.StatusUnprocessableEntity,
		}
	}
	return nil
}

func internalError(r *http.Request) *problem.Error {
	return problem.New(problem.InternalError,
		fmt.Sprintf("Something went wrong on our side. Retry, and quote request %s if it persists.", requestID(r.Context())))
}
