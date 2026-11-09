# Changelog

All notable changes to this repo are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Migration `00002` adds `idx_sessions_user_id`, so deleting a user doesn't scan `sessions`. It builds the index concurrently, outside a transaction.
- `DATABASE_POOL_MAX` sets the pool size. It wins over `pool_max_conns` in `DATABASE_URL`, which wins over the default of 10. A `connect_timeout` in the URL is no longer overridden.

### Changed

- **Breaking:** `TRUST_PROXY_HOPS` replaces `TRUST_PROXY`. It takes the Nth `X-Forwarded-For` address from the right, so the API works behind more than one proxy. Set `TRUST_PROXY_HOPS=1` for the old `TRUST_PROXY=true`. The API refuses to start while `TRUST_PROXY` is set.
- **Breaking:** `seed` adds demo data only when `SEED_DEMO_DATA=true`, and `APP_ENV` is gone. Compose and `.env.example` set the flag for local development.
- Every command reads its settings through `internal/config`, and `docs/operations.md` documents `OTEL_SERVICE_NAME` and `SEED_DEMO_DATA`.
- Handlers are split by feature into `health.go`, `users.go`, `sessions.go`, and `notes.go` in `internal/app/`, so a new feature can copy `notes.go`.

### Fixed

- At most `GOMAXPROCS` argon2id hashes or checks run at once, so a burst of registrations or logins can't use 19 MiB each without limit.
- The hourly cleanup deletes expired rows in batches of 1,000 under an advisory lock, so one replica runs it at a time and a large backlog can't hit the statement timeout.
- CORS responses send `Access-Control-Expose-Headers` on the actual response, not only the preflight, and expose `ETag`, `Location`, `X-Request-Id`, `Retry-After`, the `RateLimit-*` headers, `Idempotency-Replayed`, and `WWW-Authenticate`.
- Idempotency keys are scoped to the user and the endpoint, not just the user, so a key reused on a new idempotent endpoint can't replay a note. **Deploy note:** migration `00003` replaces the unique constraint, so idempotent `POST`s on replicas still running 0.1.x fail until they're replaced.
- A request whose client disconnected is logged at debug level with status 499, instead of as an unhandled 500. Handlers wrap unexpected errors with the operation that failed.
- Creating a note with a NUL character in `title` reports the `title` field, not `body`. Create and update share one note-field check.
- `docs/customize.md` and `docs/architecture.md` name the `bearerAuth` security scheme the spec defines, instead of `bearer`, which failed every request with a 500.
- A panic in a handler is logged with its stack and request ID, recorded on the trace, and answered with `internal-error`, instead of dropping the connection. `http.Server` errors go to the JSON log.
- A `*problem.Error` with a slug that isn't in the catalog is sent as `internal-error` with status 500, and logged, instead of panicking on `WriteHeader(0)`.

## [0.1.1] - 2026-11-08

### Fixed

- `pik new` no longer renames the e2e test's module imports twice when the module path contains the project name.

## [0.1.0] - 2026-11-08

### Added

- Go API on the standard library router, with a contract-first OpenAPI 3.1 spec, a generated strict server and client, and request validation.
- Problem Details errors and a sample notes feature.
- Session auth with argon2id, rate limits, idempotency keys, and optimistic concurrency.
- Postgres with pgx, sqlc queries, embedded goose migrations, seed data, and separate app and owner roles.
- `TEST_DATABASE_URL`, which runs tests against an existing Postgres 18 server instead of Docker.
- Distroless Docker image, Compose stack, and dev container.
- Structured logs, OpenTelemetry tracing, and error recording.
- `pik.yaml`, so `pik new api-go-starter` can create projects from this starter.
