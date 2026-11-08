package app

import (
	"context"

	"github.com/opikdev/api-go-starter/internal/store"
)

// DeleteExpiredRows removes expired sessions, idempotency keys, and rate limit windows.
func DeleteExpiredRows(ctx context.Context, queries *store.Queries) error {
	if err := queries.DeleteExpiredSessions(ctx); err != nil {
		return err
	}
	if err := queries.DeleteExpiredIdempotencyKeys(ctx); err != nil {
		return err
	}
	return queries.DeleteExpiredRateLimits(ctx)
}
