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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/opikdev/api-go-starter/internal/api"
	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/publicid"
	"github.com/opikdev/api-go-starter/internal/store"
)

const defaultPageSize = 20

var ifMatchPattern = regexp.MustCompile(`^"(\d+)"$`)

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

// validateNoteFields checks the rules the schema can't express, skipping nil fields. Trim the title first.
func validateNoteFields(title, body *string) error {
	var errs []problem.FieldError
	if title != nil && *title == "" {
		errs = append(errs, problem.FieldError{Field: "title", Detail: "must not be blank"})
	}
	if title != nil && strings.ContainsRune(*title, 0) {
		errs = append(errs, problem.FieldError{Field: "title", Detail: "must not contain NUL characters"})
	}
	if body != nil && strings.ContainsRune(*body, 0) {
		errs = append(errs, problem.FieldError{Field: "body", Detail: "must not contain NUL characters"})
	}
	if len(errs) > 0 {
		return &problem.Error{Slug: problem.ValidationFailed, Detail: invalidFieldsDetail, Errors: errs}
	}
	return nil
}

func noteNotFound(id string) *problem.Error {
	return problem.New(problem.NotFound, fmt.Sprintf("Note %s does not exist.", id))
}

func (h *handlers) CreateNote(ctx context.Context, request api.CreateNoteRequestObject) (api.CreateNoteResponseObject, error) {
	userID := currentUser(ctx).ID
	input := store.InsertNoteParams{UserID: userID, Title: strings.TrimSpace(request.Body.Title)}
	if request.Body.Body != nil {
		input.Body = *request.Body.Body
	}
	if err := validateNoteFields(&input.Title, &input.Body); err != nil {
		return nil, err
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
		return api.Note{}, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := h.queries.WithTx(tx)

	var claimID pgtype.UUID
	if key != nil {
		payload, err := json.Marshal(struct{ Title, Body string }{input.Title, input.Body})
		if err != nil {
			return api.Note{}, false, fmt.Errorf("encode idempotent request: %w", err)
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
			return api.Note{}, false, fmt.Errorf("claim idempotency key: %w", err)
		}
		claimID = pgtype.UUID{Bytes: id, Valid: true}
	}

	row, err := queries.InsertNote(ctx, input)
	if err != nil {
		return api.Note{}, false, fmt.Errorf("insert note: %w", err)
	}
	note := presentNote(row)
	if claimID.Valid {
		body, err := json.Marshal(note)
		if err != nil {
			return api.Note{}, false, fmt.Errorf("encode idempotent response: %w", err)
		}
		if err := queries.SaveIdempotentResponse(ctx, store.SaveIdempotentResponseParams{ID: claimID.Bytes, ResponseBody: body}); err != nil {
			return api.Note{}, false, fmt.Errorf("save idempotent response: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return api.Note{}, false, fmt.Errorf("commit note: %w", err)
	}
	return note, false, nil
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
		return nil, fmt.Errorf("list notes: %w", err)
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
		return nil, fmt.Errorf("find note: %w", err)
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
	}
	if err := validateNoteFields(patch.Title, patch.Body); err != nil {
		return nil, err
	}

	userID := currentUser(ctx).ID
	row, err := h.queries.UpdateNote(ctx, store.UpdateNoteParams{ID: id, UserID: userID, Version: version, Title: patch.Title, Body: patch.Body})
	if errors.Is(err, pgx.ErrNoRows) {
		current, findErr := h.queries.FindNote(ctx, store.FindNoteParams{ID: id, UserID: userID})
		if errors.Is(findErr, pgx.ErrNoRows) {
			return nil, noteNotFound(request.Id)
		}
		if findErr != nil {
			return nil, fmt.Errorf("find note after a failed update: %w", findErr)
		}
		return nil, problem.New(problem.VersionMismatch,
			fmt.Sprintf("Note %s is at version %d. Fetch it again, then retry.", request.Id, current.Version))
	}
	if err != nil {
		return nil, fmt.Errorf("update note: %w", err)
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
		return nil, fmt.Errorf("delete note: %w", err)
	}
	if deleted == 0 {
		return nil, noteNotFound(request.Id)
	}
	return api.DeleteNote204Response{}, nil
}
