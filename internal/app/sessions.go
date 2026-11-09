package app

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/store"
)

func (h *handlers) CreateSession(ctx context.Context, request api.CreateSessionRequestObject) (api.CreateSessionResponseObject, error) {
	email := strings.ToLower(string(request.Body.Email))
	user, err := h.queries.FindUserByEmail(ctx, email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	stored := h.dummyHash
	if found {
		stored = user.PasswordHash
	}
	matches, err := auth.VerifyPassword(request.Body.Password, stored)
	if err != nil {
		return nil, err
	}
	if !found || !matches {
		return nil, problem.New(problem.InvalidCredentials, "The email or password is wrong.")
	}

	token, hash, err := auth.NewToken()
	if err != nil {
		return nil, err
	}
	expiresAt, err := h.queries.CreateSession(ctx, store.CreateSessionParams{
		UserID: user.ID, TokenHash: hash, TtlHours: h.sessionTTLHours,
	})
	if err != nil {
		return nil, err
	}
	return api.CreateSession201JSONResponse{
		Body:    api.Session{Token: token, ExpiresAt: expiresAt.UTC()},
		Headers: api.CreateSession201ResponseHeaders{CacheControl: ptr("no-store")},
	}, nil
}

func (h *handlers) DeleteCurrentSession(ctx context.Context, _ api.DeleteCurrentSessionRequestObject) (api.DeleteCurrentSessionResponseObject, error) {
	if err := h.queries.DeleteSession(ctx, currentUser(ctx).SessionID); err != nil {
		return nil, err
	}
	return api.DeleteCurrentSession204Response{}, nil
}
