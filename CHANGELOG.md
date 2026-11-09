# Changelog

All notable changes to this repo are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed

- Handlers are split by feature into `health.go`, `users.go`, `sessions.go`, and `notes.go` in `internal/app/`, so a new feature can copy `notes.go`.

### Fixed

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
