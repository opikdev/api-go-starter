-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, expires_at)
VALUES ($1, $2, now() + make_interval(hours => sqlc.arg(ttl_hours)::int))
RETURNING expires_at;

-- name: FindActiveSession :one
SELECT sessions.id AS session_id, users.id, users.email, users.created_at
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = $1 AND sessions.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;
