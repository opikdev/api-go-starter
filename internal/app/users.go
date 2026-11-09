package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/publicid"
	"github.com/opikdev/api-go-starter/internal/store"
)

func presentUser(id, email string, createdAt time.Time) api.User {
	return api.User{Id: id, Email: email, CreatedAt: createdAt.UTC()}
}

func (h *handlers) CreateUser(ctx context.Context, request api.CreateUserRequestObject) (api.CreateUserResponseObject, error) {
	email := strings.ToLower(string(request.Body.Email))
	hash, err := auth.HashPassword(request.Body.Password)
	if err != nil {
		return nil, err
	}
	user, err := h.queries.CreateUser(ctx, store.CreateUserParams{Email: email, PasswordHash: hash})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, problem.New(problem.EmailTaken, email+" is already registered. Log in instead.")
	}
	if err != nil {
		return nil, err
	}
	return api.CreateUser201JSONResponse(presentUser(publicid.Format(publicid.User, user.ID), user.Email, user.CreatedAt)), nil
}

func (h *handlers) GetCurrentUser(ctx context.Context, _ api.GetCurrentUserRequestObject) (api.GetCurrentUserResponseObject, error) {
	user := currentUser(ctx)
	return api.GetCurrentUser200JSONResponse(presentUser(publicid.Format(publicid.User, user.ID), user.Email, user.CreatedAt)), nil
}
