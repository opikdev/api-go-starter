# Customize the starter

## Rename the project

Projects created with `pik new` are already renamed, including the Go module path. To rename a clone by hand, replace the text under each `from` in `pik.yaml`, in the files listed below it. Then replace the database names in `compose.yaml`, `.env.example`, and `docker/postgres/init.sql`.

## Add a feature

Copy the shape of `notes`: its handlers are in `internal/app/notes.go` and its tests in `internal/app/notes_test.go`. Work from the contract down, and let the compiler tell you what's missing:

1. Add the paths, schemas, and responses to `api/openapi.yaml`. Reuse the shared `components/responses`, and add `security: [{bearerAuth: []}, {cookieAuth: []}]` to every operation that needs a user. Document a `403` with the shared `Forbidden` response on every operation that changes data.
2. Add a migration with the next number, such as `migrations/00004_projects.sql`, with `-- +goose Up` and `-- +goose Down` sections. Give the table a `uuid` primary key that defaults to `uuidv7()`, and a `user_id` column.
3. Write the queries in `internal/store/queries/projects.sql`. Filter every query by `user_id`.
4. Run `make generate`. The build now fails until you implement the new `api.StrictServerInterface` methods.
5. Implement them on `handlers` in a new `internal/app/projects.go`. Format IDs with `publicid`. Return `*problem.Error` for failures the client can act on, and wrap other errors with `fmt.Errorf("<op>: %w", err)`.
6. Add tests next to `internal/app/notes_test.go`, using the generated client in `internal/apiclient`.
7. Run `make verify`, and add the new error types to `docs/errors.md` if there are any.

To make a new `POST` idempotent, call `claimIdempotencyKey` and `saveIdempotentResponse` from `internal/app/idempotency.go` in the handler's transaction, as `createIdempotently` does. Pass the operation's method and path, such as `POST /v1/projects`, and the request body as validated. The key's fingerprint covers the whole body, so new fields need no change there.

Run every handler that writes more than one statement, or reads after a write to explain a failure, in one transaction, as `updateNote` does.

### Load related data

`presentNote` turns one row into one response and runs no queries. Keep it that way when a resource gains related data, such as a note's tags, because a query per row turns a page of 100 into 101 queries:

- For one-to-one data, `JOIN` it into the list query, so each row arrives complete.
- For one-to-many data, load the page first, then fetch the related rows for every ID on it in one query, such as `SELECT * FROM note_tags WHERE note_id = ANY(sqlc.arg(note_ids)::uuid[])`. Group them by note ID in Go, then pass each group to the presenter.

To put the auth rate limit on a new public operation, add its operation ID to `publicAuthOperations` in `internal/app/server.go`.

## Remove the notes feature

1. Delete the `/v1/notes` paths and the note schemas from `api/openapi.yaml`.
2. Delete `internal/store/queries/notes.sql`, then run `make generate`.
3. Add a migration that drops `notes`, and remove the notes from `Seed` in `internal/database/database.go`.
4. Delete `internal/app/notes.go`, `internal/app/notes_test.go`, and the note steps in `test/e2e/stack_test.go`.
5. Run `make verify`.

## Remove idempotency

Remove the `Idempotency-Key` parameter from `createNote` in the spec, and run `make generate`. Then insert the note directly in `CreateNote`, delete `createIdempotently` and `internal/app/idempotency.go`, and add a migration that drops `idempotency_keys`. Remove its queries from `internal/store/queries/platform.sql` and its cleanup from `internal/app/cleanup.go`.

## Remove telemetry

Delete `internal/telemetry/` and its calls in `cmd/api/main.go`. Then remove `otelhttp` from `internal/app/server.go`, the cleanup span from `internal/app/cleanup.go`, and the `otelpgx` tracer and `RecordStats` from `internal/database/database.go`, and run `go mod tidy`. Error logging keeps working.
