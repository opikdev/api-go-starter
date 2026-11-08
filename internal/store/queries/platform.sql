-- name: HitRateLimit :one
INSERT INTO rate_limits (key, reset_at) VALUES ($1, now() + interval '1 minute')
ON CONFLICT (key) DO UPDATE SET
  count = CASE WHEN rate_limits.reset_at <= now() THEN 1 ELSE rate_limits.count + 1 END,
  reset_at = CASE WHEN rate_limits.reset_at <= now() THEN now() + interval '1 minute' ELSE rate_limits.reset_at END,
  updated_at = now()
RETURNING count, reset_at;

-- name: ClaimIdempotencyKey :one
INSERT INTO idempotency_keys (user_id, key, request_hash, response_body, expires_at)
VALUES ($1, $2, $3, '{}', now() + interval '24 hours')
ON CONFLICT (user_id, key) DO NOTHING
RETURNING id;

-- name: FindIdempotencyKey :one
SELECT request_hash, response_body FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: SaveIdempotentResponse :exec
UPDATE idempotency_keys SET response_body = $2, updated_at = now() WHERE id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- name: DeleteExpiredIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE expires_at <= now();

-- name: DeleteExpiredRateLimits :exec
DELETE FROM rate_limits WHERE reset_at <= now();

-- name: Ping :one
SELECT 1::int;
