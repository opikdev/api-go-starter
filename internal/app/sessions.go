package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/store"
)

const sessionCookieName = "__Host-session"

func sessionCookie(token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

func clearedSessionCookie() *http.Cookie { return sessionCookie("", -1) }

func (h *handlers) CreateSession(ctx context.Context, request api.CreateSessionRequestObject) (api.CreateSessionResponseObject, error) {
	email := strings.ToLower(string(request.Body.Email))
	succeeded, err := h.limiter.startLogin(ctx, email)
	var limited *problem.Error
	if errors.As(err, &limited) {
		if _, verifyErr := auth.VerifyPassword(request.Body.Password, h.dummyHash); verifyErr != nil {
			return nil, fmt.Errorf("verify the timing-equalizer password: %w", verifyErr)
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	user, err := h.queries.FindUserByEmail(ctx, email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find user: %w", err)
	}
	stored := h.dummyHash
	if found {
		stored = user.PasswordHash
	}
	matches, err := auth.VerifyPassword(request.Body.Password, stored)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !found || !matches {
		return nil, problem.New(problem.InvalidCredentials, "The email or password is wrong.")
	}
	if err := succeeded(ctx); err != nil {
		return nil, err
	}

	token, hash, err := auth.NewToken()
	if err != nil {
		return nil, fmt.Errorf("create token: %w", err)
	}
	expiresAt, err := h.queries.CreateSession(ctx, store.CreateSessionParams{
		UserID: user.ID, TokenHash: hash, TtlHours: h.sessionTTLHours,
	})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	response := api.CreateSession201JSONResponse{
		Body:    api.Session{ExpiresAt: expiresAt.UTC()},
		Headers: api.CreateSession201ResponseHeaders{CacheControl: ptr("no-store")},
	}
	if request.Body.Delivery != nil && *request.Body.Delivery == api.Cookie {
		response.Headers.SetCookie = ptr(sessionCookie(token, int(h.sessionTTLHours)*3600).String())
	} else {
		response.Body.Token = &token
	}
	return response, nil
}

func (h *handlers) DeleteCurrentSession(ctx context.Context, _ api.DeleteCurrentSessionRequestObject) (api.DeleteCurrentSessionResponseObject, error) {
	if err := h.queries.DeleteSession(ctx, currentUser(ctx).SessionID); err != nil {
		return nil, fmt.Errorf("delete session: %w", err)
	}
	return api.DeleteCurrentSession204Response{
		Headers: api.DeleteCurrentSession204ResponseHeaders{SetCookie: ptr(clearedSessionCookie().String())},
	}, nil
}
