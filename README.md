# API Go starter

A production-ready Go API you can run with one command and customize with coding agents. It uses the standard library router, a contract-first OpenAPI spec, sqlc, and Postgres, and it's already wired with auth, rate limits, tests, and tracing.

[![CI](https://github.com/opikdev/api-go-starter/actions/workflows/ci.yml/badge.svg)](https://github.com/opikdev/api-go-starter/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Features

- **Contract first:** `api/openapi.yaml` is the source of truth. `oapi-codegen` generates a typed server and client from it, and every request is validated against it. Docs are at `/docs`.
- **Standard library:** `net/http` routing, `log/slog` logs, and no web framework.
- **Typed SQL:** queries are plain SQL in `internal/store/queries/`, compiled to Go by sqlc and run with pgx.
- **Auth:** registration, login, and revocable session tokens, with argon2id password hashes and per-user data access.
- **Reliable by default:** rate limits, idempotency keys, optimistic concurrency, and cursor pagination.
- **Consistent errors:** RFC 9457 Problem Details everywhere. See the [error reference](docs/errors.md).
- **Safe data layer:** goose migrations embedded in the binary, constraints in the database, and an app role that can't change the schema.
- **Ready to operate:** a distroless non-root image, health checks, JSON logs, and OpenTelemetry traces.
- **Agent-ready:** `AGENTS.md`, skills, and checks that run on every commit.
- **Strict gates:** golangci-lint, the race detector, 80% coverage, architecture tests, and real end-to-end tests.

## Quick start

You only need Docker:

```sh
git clone https://github.com/opikdev/api-go-starter
cd api-go-starter
docker compose up --build --wait api
```

The API is now at <http://localhost:3000>, with interactive docs at <http://localhost:3000/docs>. Log in with the fake seed user:

```sh
curl -X POST localhost:3000/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@example.com","password":"demo-password-123"}'
```

Send the returned token to list the seeded notes:

```sh
curl localhost:3000/v1/notes -H 'Authorization: Bearer <YOUR_TOKEN>'
```

If port 3000 or 5432 is taken, set `PORT` or `POSTGRES_PORT` before running Compose.

## Usage

To work on the code, you need Go 1.27 or later, Make, and [golangci-lint](https://golangci-lint.run/) v2:

```sh
cp .env.example .env
docker compose up --wait db
make migrate seed run
```

`make run`, `make migrate`, and `make seed` load `.env`, but variables you've already set win. To use your own Postgres instead of Compose, run `docker/postgres/init.sql` on it once as a superuser, then point both URLs in `.env` at it.

| Task | Command |
|---|---|
| Run lint, generated-code checks, tests with coverage, and repo checks | `make verify` |
| Test the real image end to end | `make e2e` |
| Regenerate code after changing the spec or a query | `make generate` |
| Apply migrations, then add demo data | `make migrate seed` |
| Run the API | `make run` |

Tests start their own Postgres, so Docker must be running. Without Docker, point them at a Postgres 18 server instead:

```sh
TEST_DATABASE_URL=postgres://<SUPERUSER>@localhost:5432/postgres?sslmode=disable make verify
```

Each run creates a temporary database and drops it afterward, so existing data is never touched. The user needs permission to create databases and roles.

To add a feature, rename the project, or remove a module, see [Customize the starter](docs/customize.md). To call the API from code, see [Call the API](docs/client.md). Before you deploy, read [Operate the API](docs/operations.md).

## Project structure

| Path | Purpose |
|---|---|
| `api/openapi.yaml` | The API contract. Change it first |
| `cmd/api/` | The binary: `serve`, `migrate`, `seed`, and `healthcheck` |
| `internal/app/` | Handlers, middleware, and integration tests |
| `internal/api/`, `internal/apiclient/` | Server and client code generated from the spec |
| `internal/store/` | SQL queries and the code sqlc generates from them |
| `migrations/` | SQL migrations, which are append-only |
| `test/e2e/` | Tests that run the real Docker image |
| `docs/` | [Architecture](docs/architecture.md), guides, and [decisions](docs/decisions/) |

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request, and follow the [code of conduct](CODE_OF_CONDUCT.md).

## Security

Report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).

## License

Released under the [MIT License](LICENSE).
