# Agent guide

A production-ready Go API: standard library `net/http`, a contract-first OpenAPI spec, sqlc, and Postgres, with auth, rate limits, and Problem Details errors. `notes` is the sample feature to copy or replace.

| Path | Purpose |
|---|---|
| [docs/architecture.md](docs/architecture.md) | How requests flow and which package does what |
| [docs/customize.md](docs/customize.md) | How to add a feature, rename the project, or remove a module |
| [docs/client.md](docs/client.md) | How to call the API from Go or other languages |
| [docs/errors.md](docs/errors.md) | Every problem type the API can return |
| [docs/deploy.md](docs/deploy.md) | Go-live checklist: TLS, roles and secrets, pool size, migrations, and shutdown |
| [docs/operations.md](docs/operations.md) | Configuration, schema changes, backups, and telemetry |
| [docs/decisions/](docs/decisions/) | Why it's built this way |
| `.agents/skills/` | Step-by-step guides for recurring tasks |
| `.mcp.json` | MCP servers for this project |

## Commands

| Task | Command |
|---|---|
| Run everything | `docker compose up --build --wait api` |
| Run the API on your machine | `docker compose up --wait db`, then `make migrate seed run` |
| Run all gates | `make verify` |
| Test against the real Docker image | `make e2e` |
| Regenerate server, client, and queries | `make generate` |

## Rules

- Don't commit until `make verify` passes. Tests need Docker, or `TEST_DATABASE_URL` set to a Postgres 18 server. If neither is available, stop and report that instead of committing.
- Run `make verify` before calling work done. For changes to Docker, Compose, or startup, run `make e2e` too.
- If the check blocks you, fix the cause. Never edit `.agents/check.sh`, `.githooks/`, or the harness workflow to get past it, and never use `--no-verify`. If the request can't be done without that, stop and ask.
- After cloning, run `git config core.hooksPath .githooks` once to turn on the hook.
- Change the API in `api/openapi.yaml` first, then run `make generate` and implement the new `api.StrictServerInterface` methods. Never edit `*.gen.go` or sqlc output by hand.
- Write queries in `internal/store/queries/`, then run `make generate`. Commit generated code in the same change, or CI fails.
- Add a new numbered file in `migrations/` for every schema change. Migrations are append-only, so never edit, delete, or rename one that's committed. `make verify` and CI fail otherwise.
- Return `*problem.Error` for failures the client can act on, and add new types to `internal/problem/problem.go`, the spec, and `docs/errors.md`. Wrap every other error with `fmt.Errorf("<op>: %w", err)`, so it's logged with context and the client gets a 500.
- Scope every query on a resource to the caller's user ID. Return 404, not 403, for someone else's resource.
- Keep imports pointing one way, as `internal/arch_test.go` enforces. `store` and `database` never import `net/http`.
- If `.baseline.sha256` exists, don't edit the files it lists. They come from [opikdev/starters](https://github.com/opikdev/starters).
- Keep this file a short map, and put detail in `docs/`.
- Fix any doc your change makes wrong, in the same change.
- Record significant decisions with the `record-decision` skill.
- Back important rules with a lint rule, hook, or CI check.
