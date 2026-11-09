package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/opikdev/api-go-starter/internal/store"
)

const cleanupBatchSize = 1000

// DeleteExpiredRows removes expired sessions, idempotency keys, and rate limit windows in batches, so each
// statement stays under the statement timeout. It does nothing while another replica holds the cleanup lock.
func DeleteExpiredRows(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire a connection for cleanup: %w", err)
	}
	defer conn.Release()
	queries := store.New(conn)

	locked, err := queries.TryLockCleanup(ctx)
	if err != nil {
		return fmt.Errorf("take the cleanup lock: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() {
		if _, err := queries.UnlockCleanup(context.WithoutCancel(ctx)); err != nil {
			// Closing the session releases the lock, so a failed unlock can't block every later cleanup.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
	}()

	for table, deleteBatch := range map[string]func(context.Context, int32) (int64, error){
		"sessions":         queries.DeleteExpiredSessions,
		"idempotency keys": queries.DeleteExpiredIdempotencyKeys,
		"rate limits":      queries.DeleteExpiredRateLimits,
	} {
		for {
			deleted, err := deleteBatch(ctx, cleanupBatchSize)
			if err != nil {
				return fmt.Errorf("delete expired %s: %w", table, err)
			}
			if deleted < cleanupBatchSize {
				break
			}
		}
	}
	return nil
}
