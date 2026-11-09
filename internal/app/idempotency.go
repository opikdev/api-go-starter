package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/opikdev/api-go-starter/internal/problem"
	"github.com/opikdev/api-go-starter/internal/store"
)

// claimIdempotencyKey claims a key for one user on one endpoint, such as "POST /v1/notes". Call it in the
// transaction that does the work. If the key was already used there with the same request, it returns the
// stored response to replay instead of a claim.
func claimIdempotencyKey(ctx context.Context, queries *store.Queries, userID uuid.UUID, endpoint, key string, request any) (uuid.UUID, []byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("encode idempotent request: %w", err)
	}
	sum := sha256.Sum256(payload)
	requestHash := hex.EncodeToString(sum[:])

	id, err := queries.ClaimIdempotencyKey(ctx, store.ClaimIdempotencyKeyParams{UserID: userID, Endpoint: endpoint, Key: key, RequestHash: requestHash})
	if err == nil {
		return id, nil, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, fmt.Errorf("claim idempotency key: %w", err)
	}
	original, err := queries.FindIdempotencyKey(ctx, store.FindIdempotencyKeyParams{UserID: userID, Endpoint: endpoint, Key: key})
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("read idempotency key %q: %w", key, err)
	}
	if original.RequestHash != requestHash {
		return uuid.Nil, nil, problem.New(problem.IdempotencyKeyReused, "This Idempotency-Key was used with a different request body. Use a new key.")
	}
	return uuid.Nil, original.ResponseBody, nil
}

// saveIdempotentResponse stores the response a later replay of the claimed key returns.
func saveIdempotentResponse(ctx context.Context, queries *store.Queries, claimID uuid.UUID, response any) error {
	body, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	if err := queries.SaveIdempotentResponse(ctx, store.SaveIdempotentResponseParams{ID: claimID, ResponseBody: body}); err != nil {
		return fmt.Errorf("save idempotent response: %w", err)
	}
	return nil
}
