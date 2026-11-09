package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/store"
)

type handlers struct {
	pool            *pgxpool.Pool
	queries         *store.Queries
	sessionTTLHours int32
	dummyHash       string
}

var _ api.StrictServerInterface = (*handlers)(nil)

func newHandlers(pool *pgxpool.Pool, sessionTTLHours int32) (*handlers, error) {
	dummyHash, err := auth.HashPassword("timing-equalizer")
	if err != nil {
		return nil, err
	}
	return &handlers{pool: pool, queries: store.New(pool), sessionTTLHours: sessionTTLHours, dummyHash: dummyHash}, nil
}

func ptr[T any](value T) *T { return &value }

func validationProblem(field, detail string) *problem.Error {
	return &problem.Error{
		Slug:   problem.ValidationFailed,
		Detail: invalidFieldsDetail,
		Errors: []problem.FieldError{{Field: field, Detail: detail}},
	}
}
