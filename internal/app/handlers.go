package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/publicid"
	"github.com/opikdev/api-go-starter/internal/store"
)

const defaultPageSize = 20

var ifMatchPattern = regexp.MustCompile(`^"(\d+)"$`)

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

func presentUser(id, email string, createdAt time.Time) api.User {
	return api.User{Id: id, Email: email, CreatedAt: createdAt.UTC()}
}

func presentNote(note store.Note) api.Note {
	return api.Note{
		Id:        publicid.Format(publicid.Note, note.ID),
		Title:     note.Title,
		Body:      note.Body,
		Version:   int(note.Version),
		CreatedAt: note.CreatedAt.UTC(),
		UpdatedAt: note.UpdatedAt.UTC(),
	}
}

func etag(version int) *string { return ptr(fmt.Sprintf(`"%d"`, version)) }

func noteNotFound(id string) *problem.Error {
	return problem.New(problem.NotFound, fmt.Sprintf("Note %s does not exist.", id))
}

func (h *handlers) GetLiveness(context.Context, api.GetLivenessRequestObject) (api.GetLivenessResponseObject, error) {
	return api.GetLiveness200JSONResponse{Status: api.Ok}, nil
}

func (h *handlers) GetReadiness(ctx context.Context, _ api.GetReadinessRequestObject) (api.GetReadinessResponseObject, error) {
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

func (h *handlers) GetCurrentUser(ctx context.Context, _ api.GetCurrentUserRequestObject) (api.GetCurrentUserResponseObject, error) {
	user := currentUser(ctx)
	return api.GetCurrentUser200JSONResponse(presentUser(publicid.Format(publicid.User, user.ID), user.Email, user.CreatedAt)), nil
}

func (h *handlers) CreateNote(ctx context.Context, request api.CreateNoteRequestObject) (api.CreateNoteResponseObject, error) {
	userID := currentUser(ctx).ID
	input := store.InsertNoteParams{UserID: userID, Title: strings.TrimSpace(request.Body.Title)}
	if request.Body.Body != nil {
		input.Body = *request.Body.Body
	}
	if strings.ContainsRune(input.Title, 0) || strings.ContainsRune(input.Body, 0) {
		return nil, validationProblem("body", "must not contain NUL characters")
	}
	if input.Title == "" {
		return nil, validationProblem("title", "must not be blank")
	}

	note, replayed, err := h.createIdempotently(ctx, request.Params.IdempotencyKey, input)
	if err != nil {
		return nil, err
	}
	headers := api.CreateNote201ResponseHeaders{ETag: etag(note.Version), Location: ptr("/v1/notes/" + note.Id)}
	if replayed {
		headers.IdempotencyReplayed = ptr("true")
	}
	return api.CreateNote201JSONResponse{Body: note, Headers: headers}, nil
}

func (h *handlers) createIdempotently(ctx context.Context, key *string, input store.InsertNoteParams) (api.Note, bool, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return api.Note{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := h.queries.WithTx(tx)

	var claimID pgtype.UUID
	if key != nil {
		payload, err := json.Marshal(struct{ Title, Body string }{input.Title, input.Body})
		if err != nil {
			return api.Note{}, false, err
		}
		sum := sha256.Sum256(payload)
		requestHash := hex.EncodeToString(sum[:])
		id, err := queries.ClaimIdempotencyKey(ctx, store.ClaimIdempotencyKeyParams{UserID: input.UserID, Key: *key, RequestHash: requestHash})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			original, err := queries.FindIdempotencyKey(ctx, store.FindIdempotencyKeyParams{UserID: input.UserID, Key: *key})
			if err != nil {
				return api.Note{}, false, fmt.Errorf("read idempotency key %q: %w", *key, err)
			}
			if original.RequestHash != requestHash {
				return api.Note{}, false, problem.New(problem.IdempotencyKeyReused, "This Idempotency-Key was used with a different request body. Use a new key.")
			}
			var note api.Note
			if err := json.Unmarshal(original.ResponseBody, &note); err != nil {
				return api.Note{}, false, fmt.Errorf("decode stored response for idempotency key %q: %w", *key, err)
			}
			return note, true, nil
		case err != nil:
			return api.Note{}, false, err
		}
		claimID = pgtype.UUID{Bytes: id, Valid: true}
	}

	row, err := queries.InsertNote(ctx, input)
	if err != nil {
		return api.Note{}, false, err
	}
	note := presentNote(row)
	if claimID.Valid {
		body, err := json.Marshal(note)
		if err != nil {
			return api.Note{}, false, err
		}
		if err := queries.SaveIdempotentResponse(ctx, store.SaveIdempotentResponseParams{ID: claimID.Bytes, ResponseBody: body}); err != nil {
			return api.Note{}, false, err
		}
	}
	return note, false, tx.Commit(ctx)
}

func (h *handlers) ListNotes(ctx context.Context, request api.ListNotesRequestObject) (api.ListNotesResponseObject, error) {
	limit := defaultPageSize
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	var cursor pgtype.UUID
	if request.Params.Cursor != nil {
		id, ok := publicid.Parse(publicid.Note, *request.Params.Cursor)
		if !ok {
			return nil, validationProblem("cursor", "isn't a valid cursor. Use meta.nextCursor from a previous response")
		}
		cursor = pgtype.UUID{Bytes: id, Valid: true}
	}

	rows, err := h.queries.ListNotes(ctx, store.ListNotesParams{UserID: currentUser(ctx).ID, Limit: int32(limit + 1), Cursor: cursor})
	if err != nil {
		return nil, err
	}
	page := api.NoteList{Data: make([]api.Note, 0, limit)}
	page.Meta.Limit = limit
	for i, row := range rows {
		if i == limit {
			page.Meta.NextCursor = ptr(page.Data[limit-1].Id)
			break
		}
		page.Data = append(page.Data, presentNote(row))
	}
	return api.ListNotes200JSONResponse(page), nil
}

func (h *handlers) GetNote(ctx context.Context, request api.GetNoteRequestObject) (api.GetNoteResponseObject, error) {
	id, ok := publicid.Parse(publicid.Note, request.Id)
	if !ok {
		return nil, noteNotFound(request.Id)
	}
	row, err := h.queries.FindNote(ctx, store.FindNoteParams{ID: id, UserID: currentUser(ctx).ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, noteNotFound(request.Id)
	}
	if err != nil {
		return nil, err
	}
	note := presentNote(row)
	return api.GetNote200JSONResponse{Body: note, Headers: api.GetNote200ResponseHeaders{ETag: etag(note.Version)}}, nil
}

func parseIfMatch(header *string) (int32, error) {
	if header == nil {
		return 0, problem.New(problem.PreconditionRequired, "Send the If-Match header with the ETag from your last read of this note.")
	}
	match := ifMatchPattern.FindStringSubmatch(*header)
	if match == nil {
		return 0, problem.New(problem.VersionMismatch, fmt.Sprintf("If-Match %s is not a current ETag.", *header))
	}
	version, err := strconv.ParseInt(match[1], 10, 32)
	if err != nil {
		return 0, problem.New(problem.VersionMismatch, fmt.Sprintf("If-Match %s is not a current ETag.", *header))
	}
	return int32(version), nil
}

func (h *handlers) UpdateNote(ctx context.Context, request api.UpdateNoteRequestObject) (api.UpdateNoteResponseObject, error) {
	version, err := parseIfMatch(request.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	id, ok := publicid.Parse(publicid.Note, request.Id)
	if !ok {
		return nil, noteNotFound(request.Id)
	}
	patch := request.Body
	if patch.Title == nil && patch.Body == nil {
		return nil, validationProblem("body", "needs title, body, or both")
	}
	if patch.Title != nil {
		patch.Title = ptr(strings.TrimSpace(*patch.Title))
		if *patch.Title == "" {
			return nil, validationProblem("title", "must not be blank")
		}
	}
	for field, value := range map[string]*string{"title": patch.Title, "body": patch.Body} {
		if value != nil && strings.ContainsRune(*value, 0) {
			return nil, validationProblem(field, "must not contain NUL characters")
		}
	}

	userID := currentUser(ctx).ID
	row, err := h.queries.UpdateNote(ctx, store.UpdateNoteParams{ID: id, UserID: userID, Version: version, Title: patch.Title, Body: patch.Body})
	if errors.Is(err, pgx.ErrNoRows) {
		current, findErr := h.queries.FindNote(ctx, store.FindNoteParams{ID: id, UserID: userID})
		if errors.Is(findErr, pgx.ErrNoRows) {
			return nil, noteNotFound(request.Id)
		}
		if findErr != nil {
			return nil, findErr
		}
		return nil, problem.New(problem.VersionMismatch,
			fmt.Sprintf("Note %s is at version %d. Fetch it again, then retry.", request.Id, current.Version))
	}
	if err != nil {
		return nil, err
	}
	note := presentNote(row)
	return api.UpdateNote200JSONResponse{Body: note, Headers: api.UpdateNote200ResponseHeaders{ETag: etag(note.Version)}}, nil
}

func (h *handlers) DeleteNote(ctx context.Context, request api.DeleteNoteRequestObject) (api.DeleteNoteResponseObject, error) {
	id, ok := publicid.Parse(publicid.Note, request.Id)
	if !ok {
		return nil, noteNotFound(request.Id)
	}
	deleted, err := h.queries.DeleteNote(ctx, store.DeleteNoteParams{ID: id, UserID: currentUser(ctx).ID})
	if err != nil {
		return nil, err
	}
	if deleted == 0 {
		return nil, noteNotFound(request.Id)
	}
	return api.DeleteNote204Response{}, nil
}

func validationProblem(field, detail string) *problem.Error {
	return &problem.Error{
		Slug:   problem.ValidationFailed,
		Detail: invalidFieldsDetail,
		Errors: []problem.FieldError{{Field: field, Detail: detail}},
	}
}
