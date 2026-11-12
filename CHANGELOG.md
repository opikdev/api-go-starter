# Changelog

All notable changes to this repo are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Failed logins are limited per account, from any address: after `LOGIN_FAILURE_LIMIT` (default 5) failures in `LOGIN_FAILURE_WINDOW_SECONDS` (default 900), `POST /v1/sessions` returns 429 `rate-limited` without checking the password, whether or not the account exists. A successful login doesn't count. See [ADR 007](docs/decisions/007-key-rate-limits-by-network-and-account.md).

- `migrate` holds a Postgres advisory lock for the whole run, so concurrent runs, such as one per replica, wait in turn instead of failing. It also sets `lock_timeout` to 5 seconds for migration DDL.

- Graceful drain on `SIGTERM` or `SIGINT`: `/readyz` returns 503 for `SHUTDOWN_DRAIN_SECONDS` (default 5) while requests are still served, then in-flight requests get `SHUTDOWN_TIMEOUT_SECONDS` (default 30, up from a fixed 10, and never below the write timeout) before the cleanup job, pool, and telemetry close. Compose sets `stop_grace_period: 40s`.

- The `urn:problem:conflict` problem type, with status 409. A unique or foreign key violation that no handler expected returns it instead of a 500, and a check violation returns 422 `validation-failed`, with no constraint name or SQL in `detail`.
- `docs/customize.md` shows how to load related data for a page without a query per row.
- OpenTelemetry metrics over OTLP/HTTP whenever traces are exported, with the same `OTEL_*` settings: HTTP server metrics, database call durations, and pool metrics. Shutdown flushes them.
- Log lines written during a traced request or the cleanup job have `trace_id` and `span_id`. The cleanup job is traced.
- `api/openapi.yaml` documents every status the middleware can return, with shared responses for 413 and 500, and shared headers: `X-Request-Id` on every response, and `Cache-Control`, `RateLimit`, and `RateLimit-Policy` on every `/v1` response. A test fails when an operation misses one.
- `pik new` renames the README title and the spec's `info.title`, which also titles `/docs`, resets `info.version` to `0.1.0` and `CHANGELOG.md` to an empty `[Unreleased]` section, and regenerates the server so it embeds the renamed spec.

### Changed

- `info.title` in the spec is the project name, `api-go-starter`, and `info.version` is the project version, `0.3.0`, instead of `1.0.0`.

- **Breaking:** rate limit headers follow draft-ietf-httpapi-ratelimit-headers. Every `/v1` response sends `RateLimit-Policy`, such as `"global";q=300;w=60`, and `RateLimit`, such as `"global";r=238;t=25`, listing the auth policy too on registration and login. On a 429, `RateLimit` and `Retry-After` describe only the policy that ran out. `RateLimit-Limit`, `RateLimit-Remaining`, and `RateLimit-Reset` are gone.

- **Breaking:** `/docs` and `/openapi.json` return 404 unless `DOCS_ENABLED=true`. Compose, the dev container, and `.env.example` turn it on. See [ADR 008](docs/decisions/008-turn-off-api-docs-by-default.md).

- `/healthz` and `/readyz` are logged at `debug` instead of `info`, and aren't traced.

### Fixed

- An oversized chunked body on a protected route returns 413 `payload-too-large`, not a logged 500. Other body read errors return 400.
- `PATCH /v1/notes/{id}` runs its update and the version lookup behind a mismatch in one transaction.
- An idempotency key past its 24-hour expiry counts as unused, so it's claimed again instead of replaying an old response.
- The idempotency fingerprint hashes the method, path, and the whole validated body as canonical JSON, so a field added later is covered. A replay of a request sent before this release gets 422 `idempotency-key-reused` instead.
- The `Authorization` scheme matches `Bearer` in any case, as RFC 9110 requires.

### Security

- Every `/v1` response sends `Cache-Control: no-store`, not just login.
- Every response except `/docs` sends `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`.
- `/docs` serves Swagger UI from files embedded in the binary, with no CDN, under `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`.
- Rate limits count an IPv6 client by its /64, and an IPv4-mapped IPv6 address as its IPv4 address, so rotating addresses inside one network no longer resets a limit.

## [0.3.0] - 2026-11-11

### Added

- The README explains how to start a project with `pik new` or GitHub's **Use this template**. `pik new` removes that section from the project it creates.
- Cookie sessions for a browser app on the API's origin. `POST /v1/sessions` takes an optional `delivery`. With `"cookie"`, it sets the token as an `HttpOnly`, `Secure`, `SameSite=Strict` `__Host-session` cookie and leaves `token` out of the body. With `"body"`, the default, it works as before. See [ADR 006](docs/decisions/006-cookie-sessions-for-same-origin-browser-apps.md).
- Protected routes accept the `__Host-session` cookie, as the `cookieAuth` scheme, when the request has no `Authorization` header. An invalid or expired cookie gets a 401 that clears it, and `DELETE /v1/sessions/current` always clears it.
- The `urn:problem:forbidden` problem type, with status 403.

### Changed

- `SESSION_TTL_HOURS` accepts up to 9600 (400 days), up from 8760, matching the longest cookie `Max-Age` browsers keep. The API refuses to start above that, so a session cookie never expires before its session.

### Security

- Every `/v1` request that changes data goes through Go's `http.CrossOriginProtection`, before auth and rate limits. Bearer clients that send no `Origin` or `Sec-Fetch-Site` are unaffected, but cross-site browser requests that change data now get 403 `forbidden` unless their origin is in `CORS_ORIGINS`.
- The API refuses to start when a `CORS_ORIGINS` entry isn't a bare origin such as `https://app.example`, with no path or trailing slash. Such an entry never matched a browser's `Origin` before.

## [0.2.0] - 2026-11-09

### Added

- `scripts/check-migrations.sh` fails when a migration on `main` is changed, deleted, or renamed, or when two migrations share a version. `make verify` and CI run it. Without an `origin/main` or `main` ref, it skips with a note.
- Migration `00002` adds `idx_sessions_user_id`, so deleting a user doesn't scan `sessions`. It builds the index concurrently, outside a transaction.
- `DATABASE_POOL_MAX` sets the pool size. It wins over `pool_max_conns` in `DATABASE_URL`, which wins over the default of 10. A `connect_timeout` in the URL is no longer overridden.

### Changed

- The release workflow runs the CI checks first and publishes the image only if they pass.
- **Breaking:** `TRUST_PROXY_HOPS` replaces `TRUST_PROXY`. It takes the Nth `X-Forwarded-For` address from the right, so the API works behind more than one proxy. Set `TRUST_PROXY_HOPS=1` for the old `TRUST_PROXY=true`. The API refuses to start while `TRUST_PROXY` is set.
- **Breaking:** `seed` adds demo data only when `SEED_DEMO_DATA=true`, and `APP_ENV` is gone. Compose and `.env.example` set the flag for local development.
- Every command reads its settings through `internal/config`, and `docs/operations.md` documents `OTEL_SERVICE_NAME` and `SEED_DEMO_DATA`.
- Handlers are split by feature into `health.go`, `users.go`, `sessions.go`, and `notes.go` in `internal/app/`, so a new feature can copy `notes.go`.
- The agent harness is v0.3.0. Its check also fails when the harness workflow or pre-commit hook is missing or no longer runs the check.
- `renovate.json` belongs to the project and extends the shared baseline preset. Generated projects extend only `config:recommended`.

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
