-- +goose Up
CREATE TABLE "idempotency_keys" (
	"id" uuid DEFAULT uuidv7() NOT NULL,
	"user_id" uuid NOT NULL,
	"key" text NOT NULL,
	"request_hash" text NOT NULL,
	"response_body" jsonb NOT NULL,
	"expires_at" timestamp with time zone NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "pk_idempotency_keys" PRIMARY KEY("id"),
	CONSTRAINT "uq_idempotency_keys_user_id_key" UNIQUE("user_id","key")
);

CREATE TABLE "notes" (
	"id" uuid DEFAULT uuidv7() NOT NULL,
	"user_id" uuid NOT NULL,
	"title" text NOT NULL,
	"body" text DEFAULT '' NOT NULL,
	"version" integer DEFAULT 1 NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "pk_notes" PRIMARY KEY("id"),
	CONSTRAINT "chk_notes_title_length" CHECK (char_length("notes"."title") between 1 and 200),
	CONSTRAINT "chk_notes_body_length" CHECK (char_length("notes"."body") <= 10000),
	CONSTRAINT "chk_notes_version_positive" CHECK ("notes"."version" > 0)
);

CREATE TABLE "rate_limits" (
	"id" uuid DEFAULT uuidv7() NOT NULL,
	"key" text NOT NULL,
	"count" integer DEFAULT 1 NOT NULL,
	"reset_at" timestamp with time zone NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "pk_rate_limits" PRIMARY KEY("id"),
	CONSTRAINT "uq_rate_limits_key" UNIQUE("key")
);

CREATE TABLE "sessions" (
	"id" uuid DEFAULT uuidv7() NOT NULL,
	"user_id" uuid NOT NULL,
	"token_hash" text NOT NULL,
	"expires_at" timestamp with time zone NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "pk_sessions" PRIMARY KEY("id"),
	CONSTRAINT "uq_sessions_token_hash" UNIQUE("token_hash")
);

CREATE TABLE "users" (
	"id" uuid DEFAULT uuidv7() NOT NULL,
	"email" text NOT NULL,
	"password_hash" text NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "pk_users" PRIMARY KEY("id"),
	CONSTRAINT "uq_users_email" UNIQUE("email"),
	CONSTRAINT "chk_users_email_lowercase" CHECK ("users"."email" = lower("users"."email"))
);

ALTER TABLE "idempotency_keys" ADD CONSTRAINT "fk_idempotency_keys_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "notes" ADD CONSTRAINT "fk_notes_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "sessions" ADD CONSTRAINT "fk_sessions_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;
CREATE INDEX "idx_idempotency_keys_expires_at" ON "idempotency_keys" USING btree ("expires_at");
CREATE INDEX "idx_notes_user_id_id" ON "notes" USING btree ("user_id","id" DESC NULLS LAST);
CREATE INDEX "idx_rate_limits_reset_at" ON "rate_limits" USING btree ("reset_at");
CREATE INDEX "idx_sessions_expires_at" ON "sessions" USING btree ("expires_at");
-- +goose Down
DROP TABLE rate_limits;
DROP TABLE idempotency_keys;
DROP TABLE notes;
DROP TABLE sessions;
DROP TABLE users;
