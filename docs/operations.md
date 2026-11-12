# Operate the API

## Configure it

Set these on the API process. `.env.example` lists every option, with values for local development. The API refuses to start if a value is invalid, and names the variable without echoing it.

| Variable | Type | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | URL | Required | Connection for the `api_app` role |
| `MIGRATION_DATABASE_URL` | URL | Required for `migrate` | Connection for the `api_owner` role. `seed` uses it, or `DATABASE_URL` if it's unset |
| `PORT` | number | `3000` | Listen port |
| `LOG_LEVEL` | string | `info` | `debug`, `info`, `warn`, or `error` |
| `DATABASE_STATEMENT_TIMEOUT_MS` | number | `5000` | Cancels slow queries |
| `DATABASE_POOL_MAX` | number, 1 or more | `10` | Most connections the API opens. See below |
| `CORS_ORIGINS` | list | Empty | Comma-separated browser origins, such as `https://app.example`, that may call the API and send requests that change data. Empty disables CORS |
| `TRUST_PROXY_HOPS` | number | `0` | How many proxies in front of the API append to `X-Forwarded-For`. See below |
| `SESSION_TTL_HOURS` | number, 1 to 9600 | `168` | Session lifetime. At most 400 days, because browsers cap a cookie's `Max-Age` there |
| `RATE_LIMIT_PER_MINUTE` | number | `300` | Per client, across `/v1` |
| `AUTH_RATE_LIMIT_PER_MINUTE` | number | `10` | Per client, on registration and login |
| `LOGIN_FAILURE_LIMIT` | number | `5` | Failed logins per account before login returns 429, from any address |
| `LOGIN_FAILURE_WINDOW_SECONDS` | number, 1 to 86400 | `900` | Window for `LOGIN_FAILURE_LIMIT` |
| `SHUTDOWN_DRAIN_SECONDS` | number, 0 to 300 | `5` | After `SIGTERM`, how long `/readyz` returns 503 while the API keeps serving, so load balancers stop sending traffic first |
| `SHUTDOWN_TIMEOUT_SECONDS` | number, 30 to 3600 | `30` | Then, how long in-flight requests get to finish. At least the 30-second write timeout |
| `DOCS_ENABLED` | boolean | `false` | Serves Swagger UI at `/docs` and the spec at `/openapi.json`. Off, both return 404. Compose and `.env.example` turn it on for local development |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | URL | Empty | Turns on trace, metric, and error export. The exporters also read the other standard `OTEL_EXPORTER_OTLP_*` variables, and `OTEL_METRIC_EXPORT_INTERVAL`, in milliseconds, which defaults to 60000 |
| `OTEL_SERVICE_NAME` | string | `api-go-starter` | Service name on exported traces and metrics |
| `SEED_DEMO_DATA` | boolean | `false` | Set to `true` to let `seed` add the fake demo user. Compose and `.env.example` set it for local development |

The pool size comes from the first of these that's set: `DATABASE_POOL_MAX`, then `pool_max_conns` in `DATABASE_URL`, then `10`. Other pool and connection parameters in `DATABASE_URL`, such as `connect_timeout`, are kept, and `connect_timeout` defaults to 5 seconds.

Set `TRUST_PROXY_HOPS` to the number of proxies you run in front of the API, or every client behind a load balancer shares one rate limit:

- `0` ignores `X-Forwarded-For` and uses the TCP peer address. Use it when clients connect directly.
- `N` takes the Nth address from the right of `X-Forwarded-For`, after joining every header value with commas. With fewer addresses than `N`, it takes the leftmost. If that entry is empty or not an IP address, it uses the TCP peer address.

Set it no higher than your real proxy count, because clients can put any addresses they like at the left of the header. `TRUST_PROXY` was removed, and the API refuses to start while it's set. `TRUST_PROXY=true` is `TRUST_PROXY_HOPS=1`.

Rate limits count an IPv4 client by its address and an IPv6 client by its /64, so rotating addresses inside one network doesn't reset a limit. Each `/v1` response lists the limits that apply in `RateLimit-Policy`, such as `"global";q=300;w=60`, and what's left of each in `RateLimit`, such as `"global";r=238;t=25`. See [ADR 007](decisions/007-key-rate-limits-by-network-and-account.md).

## Set up production roles

Replace the dev passwords in `docker/postgres/init.sql`. Run the file once as the role that owns the schema, so tables created by migrations are granted to `api_app`.

## Deploy

Pushing a `vX.Y.Z` tag runs every CI check, then publishes an image for amd64 and arm64 to `ghcr.io` if they pass. The binary runs every step, so deploy in this order:

1. Run `/api migrate` with `MIGRATION_DATABASE_URL` set to the owner role, as one job per deploy, before the new version takes traffic. It holds a Postgres advisory lock for the whole run, so a second run waits, then finds nothing to do. Running it from every replica is safe too.
2. Roll out the new image with `DATABASE_URL` set to `api_app`.
3. Send traffic when `GET /readyz` returns 200.

Use `/healthz` for liveness, or the image's built-in `HEALTHCHECK`. It stays 200 during a database outage, so restarts don't make an outage worse. On `SIGTERM` or `SIGINT`, `/readyz` returns 503 for `SHUTDOWN_DRAIN_SECONDS` while the API keeps serving. Then it stops taking connections, gives in-flight requests `SHUTDOWN_TIMEOUT_SECONDS` to finish, stops the cleanup job, closes the pool, and flushes telemetry. A second signal stops it at once.

## Change the schema safely

Treat migrations as append-only and forward-only. `scripts/check-migrations.sh`, which `make verify` and CI run, fails when a migration that exists on `main` is changed, deleted, or renamed, or when two migrations share a version. Each file has a goose `Down` section for local use, but in production, undo a change with a new migration, or restore from a backup if data was lost.

- Rename or retype a column in steps: add the new one, backfill, switch the code, then drop the old one in a later deploy.
- Use `CREATE INDEX CONCURRENTLY` on large tables, in a migration marked `-- +goose NO TRANSACTION`.
- Read the SQL before merging, because a small diff can take a long lock.
- `migrate` sets `lock_timeout` to 5 seconds, so a migration that can't get its table lock fails instead of queueing every query on that table behind it. Rerun it when traffic is lower.

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

## Trace, measure, and log

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to send traces and metrics to any OTLP/HTTP backend.

- **Traces:** each request gets a span named after its route, such as `GET /v1/notes/{id}`, with its database queries nested inside. The hourly cleanup job gets a span of its own. Unexpected errors are recorded on the span, so backends with error tracking can group them. `/healthz` and `/readyz` aren't traced.
- **Metrics:** HTTP server metrics such as `http.server.request.duration`, database call durations, and pool metrics such as `pgxpool.acquired_connections`, every `OTEL_METRIC_EXPORT_INTERVAL`. Shutdown flushes the last batch.
- **Logs:** JSON on stdout. A line written during a traced request or job has `trace_id` and `span_id`, so you can jump from a log line to its trace. Health checks log at `debug`, so they don't flood `info`.
