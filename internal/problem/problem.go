// Package problem writes RFC 9457 Problem Details responses.
package problem

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Slug identifies a problem type. Clients switch on urn:problem:<slug>.
type Slug string

const (
	ValidationFailed     Slug = "validation-failed"
	InvalidCredentials   Slug = "invalid-credentials" //nolint:gosec // A problem type name, not a credential.
	Unauthorized         Slug = "unauthorized"
	NotFound             Slug = "not-found"
	MethodNotAllowed     Slug = "method-not-allowed"
	EmailTaken           Slug = "email-taken"
	VersionMismatch      Slug = "version-mismatch"
	PayloadTooLarge      Slug = "payload-too-large"
	UnsupportedMediaType Slug = "unsupported-media-type"
	IdempotencyKeyReused Slug = "idempotency-key-reused"
	PreconditionRequired Slug = "precondition-required"
	RateLimited          Slug = "rate-limited"
	InternalError        Slug = "internal-error"
	ServiceUnavailable   Slug = "service-unavailable"
)

var catalog = map[Slug]struct {
	status int
	title  string
}{
	ValidationFailed:     {http.StatusBadRequest, "Request validation failed"},
	InvalidCredentials:   {http.StatusUnauthorized, "Invalid credentials"},
	Unauthorized:         {http.StatusUnauthorized, "Authentication required"},
	NotFound:             {http.StatusNotFound, "Resource not found"},
	MethodNotAllowed:     {http.StatusMethodNotAllowed, "Method not allowed"},
	EmailTaken:           {http.StatusConflict, "Email already registered"},
	VersionMismatch:      {http.StatusPreconditionFailed, "Resource version mismatch"},
	PayloadTooLarge:      {http.StatusRequestEntityTooLarge, "Payload too large"},
	UnsupportedMediaType: {http.StatusUnsupportedMediaType, "Unsupported media type"},
	IdempotencyKeyReused: {http.StatusUnprocessableEntity, "Idempotency key reused"},
	PreconditionRequired: {http.StatusPreconditionRequired, "Precondition required"},
	RateLimited:          {http.StatusTooManyRequests, "Too many requests"},
	InternalError:        {http.StatusInternalServerError, "Internal server error"},
	ServiceUnavailable:   {http.StatusServiceUnavailable, "Service unavailable"},
}

// FieldError describes one invalid field.
type FieldError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// Error is a failure the API reports to the client.
type Error struct {
	Slug    Slug
	Detail  string
	Errors  []FieldError
	Headers map[string]string
	Cause   error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Slug, e.Detail) }

func (e *Error) Unwrap() error { return e.Cause }

// New builds a problem with a detail message the client can act on.
func New(slug Slug, detail string) *Error {
	return &Error{Slug: slug, Detail: detail}
}

// Status returns the HTTP status for the problem.
func (e *Error) Status() int { return catalog[e.Slug].status }

type body struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail"`
	Instance string       `json:"instance"`
	Errors   []FieldError `json:"errors,omitempty"`
}

// Write sends the problem as application/problem+json.
func Write(w http.ResponseWriter, r *http.Request, e *Error) {
	entry := catalog[e.Slug]
	for name, value := range e.Headers {
		w.Header().Set(name, value)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(entry.status)
	_ = json.NewEncoder(w).Encode(body{
		Type:     "urn:problem:" + string(e.Slug),
		Title:    entry.title,
		Status:   entry.status,
		Detail:   e.Detail,
		Instance: r.URL.Path,
		Errors:   e.Errors,
	})
}
