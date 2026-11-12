package app

import (
	"context"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/problem"
)

func (h *handlers) GetLiveness(context.Context, api.GetLivenessRequestObject) (api.GetLivenessResponseObject, error) {
	return api.GetLiveness200JSONResponse{Status: api.Ok}, nil
}

func (h *handlers) GetReadiness(ctx context.Context, _ api.GetReadinessRequestObject) (api.GetReadinessResponseObject, error) {
	if h.draining.Load() {
		return nil, problem.New(problem.ServiceUnavailable, "The API is shutting down. Send requests to another instance.")
	}
	if _, err := h.queries.Ping(ctx); err != nil {
		return nil, &problem.Error{
			Slug:    problem.ServiceUnavailable,
			Detail:  "Postgres is unreachable. Retry shortly.",
			Headers: map[string]string{"Retry-After": "5"},
			Cause:   err,
		}
	}
	return api.GetReadiness200JSONResponse{Status: api.Ok}, nil
}
