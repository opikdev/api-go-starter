-- name: CreateUser :one
INSERT INTO users (email, password_hash) VALUES ($1, $2)
ON CONFLICT (email) DO NOTHING
RETURNING *;

-- name: FindUserByEmail :one
SELECT * FROM users WHERE email = $1;
