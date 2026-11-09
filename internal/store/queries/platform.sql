-- name: HitRateLimit :one
INSERT INTO rate_limits (key, reset_at) VALUES ($1, now() + interval '1 minute')
ON CONFLICT (key) DO UPDATE SET
  count = CASE WHEN rate_limits.reset_at <= now() THEN 1 ELSE rate_limits.count + 1 END,
  reset_at = CASE WHEN rate_limits.reset_at <= now() THEN now() + interval '1 minute' ELSE rate_limits.reset_at END,
  updated_at = now()
RETURNING count, reset_at;

-- name: ClaimIdempotencyKey :one
INSERT INTO idempotency_keys (user_id, endpoint, key, request_hash, response_body, expires_at)
VALUES ($1, $2, $3, $4, '{}', now() + interval '24 hours')
ON CONFLICT (user_id, endpoint, key) DO NOTHING
RETURNING id;

-- name: FindIdempotencyKey :one
SELECT request_hash, response_body FROM idempotency_keys WHERE user_id = $1 AND endpoint = $2 AND key = $3;

-- name: SaveIdempotentResponse :exec
UPDATE idempotency_keys SET response_body = $2, updated_at = now() WHERE id = $1;

-- name: TryLockCleanup :one
SELECT pg_try_advisory_lock(hashtext('delete-expired-rows')) AS locked;

-- name: UnlockCleanup :one
SELECT pg_advisory_unlock(hashtext('delete-expired-rows')) AS unlocked;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE id IN (SELECT id FROM sessions WHERE expires_at <= now() LIMIT $1);

-- name: DeleteExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE id IN (SELECT id FROM idempotency_keys WHERE expires_at <= now() LIMIT $1);

-- name: DeleteExpiredRateLimits :execrows
DELETE FROM rate_limits WHERE id IN (SELECT id FROM rate_limits WHERE reset_at <= now() LIMIT $1);

-- name: Ping :one
SELECT 1::int;
