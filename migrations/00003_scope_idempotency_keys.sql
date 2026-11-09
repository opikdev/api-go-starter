-- +goose NO TRANSACTION
-- Scopes idempotency keys to an endpoint, so one key can't replay another endpoint's response.
-- Every key so far came from POST /v1/notes, so existing rows get that endpoint.
-- +goose Up
ALTER TABLE "idempotency_keys" ADD COLUMN IF NOT EXISTS "endpoint" text DEFAULT 'POST /v1/notes' NOT NULL;
ALTER TABLE "idempotency_keys" ALTER COLUMN "endpoint" DROP DEFAULT;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "uq_idempotency_keys_user_id_endpoint_key" ON "idempotency_keys" USING btree ("user_id", "endpoint", "key");
ALTER TABLE "idempotency_keys" ADD CONSTRAINT "uq_idempotency_keys_user_id_endpoint_key" UNIQUE USING INDEX "uq_idempotency_keys_user_id_endpoint_key";
ALTER TABLE "idempotency_keys" DROP CONSTRAINT IF EXISTS "uq_idempotency_keys_user_id_key";

-- +goose Down
DELETE FROM "idempotency_keys";
ALTER TABLE "idempotency_keys" ADD CONSTRAINT "uq_idempotency_keys_user_id_key" UNIQUE ("user_id", "key");
ALTER TABLE "idempotency_keys" DROP CONSTRAINT "uq_idempotency_keys_user_id_endpoint_key";
ALTER TABLE "idempotency_keys" DROP COLUMN "endpoint";
