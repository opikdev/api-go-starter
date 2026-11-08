-- name: InsertNote :one
INSERT INTO notes (user_id, title, body) VALUES ($1, $2, $3) RETURNING *;

-- name: FindNote :one
SELECT * FROM notes WHERE id = $1 AND user_id = $2;

-- name: ListNotes :many
SELECT * FROM notes
WHERE user_id = $1 AND (sqlc.narg(cursor)::uuid IS NULL OR id < sqlc.narg(cursor)::uuid)
ORDER BY id DESC
LIMIT $2;

-- name: UpdateNote :one
UPDATE notes
SET title = coalesce(sqlc.narg(title), title),
    body = coalesce(sqlc.narg(body), body),
    version = version + 1,
    updated_at = now()
WHERE id = $1 AND user_id = $2 AND version = $3
RETURNING *;

-- name: DeleteNote :execrows
DELETE FROM notes WHERE id = $1 AND user_id = $2;
