-- +goose NO TRANSACTION
-- Serves the ON DELETE CASCADE from users to sessions, which looks sessions up by user_id.
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_sessions_user_id" ON "sessions" USING btree ("user_id");

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS "idx_sessions_user_id";
