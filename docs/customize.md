# Customize the starter

## Rename the project

Projects created with `pik new` are already renamed, including the Go module path. To rename a clone by hand, replace the text under each `from` in `pik.yaml`, in the files listed below it. Then replace the database names in `compose.yaml`, `.env.example`, and `docker/postgres/init.sql`.

## Add a feature

Copy the shape of `notes`. Work from the contract down, and let the compiler tell you what's missing:

1. Add the paths, schemas, and responses to `api/openapi.yaml`. Reuse the shared `components/responses`, and add `security: [{bearerAuth: []}]` to every operation that needs a user.
2. Add a migration, such as `migrations/00002_projects.sql`, with `-- +goose Up` and `-- +goose Down` sections. Give the table a `uuid` primary key that defaults to `uuidv7()`, and a `user_id` column.
3. Write the queries in `internal/store/queries/projects.sql`. Filter every query by `user_id`.
4. Run `make generate`. The build now fails until you implement the new `api.StrictServerInterface` methods.
5. Implement them on `handlers` in a new `internal/app/projects.go`. Format IDs with `publicid`, and return `*problem.Error` for every failure.
6. Add tests next to `internal/app/notes_test.go`, using the generated client in `internal/apiclient`.
7. Run `make verify`, and add the new error types to `docs/errors.md` if there are any.

To put the auth rate limit on a new public operation, add its operation ID to `publicAuthOperations` in `internal/app/server.go`.

## Remove the notes feature

1. Delete the `/v1/notes` paths and the note schemas from `api/openapi.yaml`.
2. Delete `internal/store/queries/notes.sql`, then run `make generate`.
3. Add a migration that drops `notes`, and remove the notes from `Seed` in `internal/database/database.go`.
4. Delete the note handlers in `internal/app/handlers.go`, `internal/app/notes_test.go`, and the note steps in `test/e2e/stack_test.go`.
5. Run `make verify`.

## Remove idempotency

Remove the `Idempotency-Key` parameter from `createNote` in the spec, and run `make generate`. Then insert the note directly in `CreateNote`, delete `createIdempotently`, and add a migration that drops `idempotency_keys`. Remove its queries from `internal/store/queries/platform.sql` and its cleanup from `internal/app/cleanup.go`.

## Remove tracing

Delete `internal/telemetry/` and its call in `cmd/api/main.go`. Then remove `otelhttp` from `internal/app/server.go` and the `otelpgx` tracer from `internal/database/database.go`, and run `go mod tidy`. Error logging keeps working.
