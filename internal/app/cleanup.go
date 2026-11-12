package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/opikdev/api-go-starter/internal/store"
)

const cleanupBatchSize = 1000

// RunCleanup deletes expired rows every interval until ctx is done.
func RunCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupOnce(ctx, pool, logger)
		}
	}
}

func cleanupOnce(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	ctx, span := otel.Tracer("github.com/opikdev/api-go-starter/internal/app").Start(ctx, "cleanup expired rows")
	defer span.End()
	if err := DeleteExpiredRows(ctx, pool); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "cleanup failed")
		logger.ErrorContext(ctx, "Cleanup failed", "err", err)
	}
}

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
