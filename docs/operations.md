# Operate the API

## Configure it

Set these on the API process. `.env.example` lists every option with a safe default. The API refuses to start if a value is invalid, and names the variable without echoing it.

| Variable | Type | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | URL | Required | Connection for the `api_app` role |
| `MIGRATION_DATABASE_URL` | URL | Required for `migrate` | Connection for the `api_owner` role |
| `PORT` | number | `3000` | Listen port |
| `LOG_LEVEL` | string | `info` | `debug`, `info`, `warn`, or `error` |
| `DATABASE_STATEMENT_TIMEOUT_MS` | number | `5000` | Cancels slow queries |
| `CORS_ORIGINS` | list | Empty | Comma-separated browser origins. Empty disables CORS |
| `TRUST_PROXY` | boolean | `false` | Set to `true` only behind a proxy that appends to `X-Forwarded-For` |
| `SESSION_TTL_HOURS` | number | `168` | Session lifetime |
| `RATE_LIMIT_PER_MINUTE` | number | `300` | Per client, across `/v1` |
| `AUTH_RATE_LIMIT_PER_MINUTE` | number | `10` | Per client, on registration and login |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | URL | Empty | Turns on trace and error export |
| `APP_ENV` | string | Empty | Set to `production` to stop `seed` from adding demo data |

Set `TRUST_PROXY` correctly, or every client behind a load balancer shares one rate limit.

## Set up production roles

Replace the dev passwords in `docker/postgres/init.sql`. Run the file once as the role that owns the schema, so tables created by migrations are granted to `api_app`.

## Deploy

Pushing a `vX.Y.Z` tag publishes an image for amd64 and arm64 to `ghcr.io`. The binary runs every step, so deploy in this order:

1. Run `/api migrate` with `MIGRATION_DATABASE_URL` set to the owner role.
2. Roll out the new image with `DATABASE_URL` set to `api_app`.
3. Send traffic when `GET /readyz` returns 200.

Use `/healthz` for liveness, or the image's built-in `HEALTHCHECK`. It stays 200 during a database outage, so restarts don't make an outage worse. On `SIGTERM`, the API stops taking connections and finishes in-flight requests before it exits.

## Change the schema safely

Treat migrations as append-only and forward-only. Each file has a goose `Down` section for local use, but in production, undo a change with a new migration, or restore from a backup if data was lost.

- Rename or retype a column in steps: add the new one, backfill, switch the code, then drop the old one in a later deploy.
- Use `CREATE INDEX CONCURRENTLY` on large tables, in a migration marked `-- +goose NO TRANSACTION`.
- Read the SQL before merging, because a small diff can take a long lock.

## Back up the database

Use your provider's point-in-time recovery. For a self-hosted database, schedule `pg_dump` and copy the output off the host:

```sh
pg_dump --format=custom --file=api.dump "$MIGRATION_DATABASE_URL"
```

Restore into a new database, then point the API at it, so a bad backup can't overwrite good data:

```sh
pg_restore --no-owner --dbname="$NEW_DATABASE_URL" api.dump
```

Test a restore regularly, because an untested backup isn't a backup.

## Trace requests and errors

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to send traces to any OTLP/HTTP backend. Each request gets a span named after its route, such as `GET /v1/notes/{id}`, with its database queries nested inside. Unexpected errors are recorded on the span, so backends with error tracking can group them.
