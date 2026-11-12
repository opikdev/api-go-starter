# Deploy to production

Work through this page before the first production deploy. [Operate the API](operations.md) lists every setting.

## Release an image

Before you tag a release, set `info.version` in `api/openapi.yaml` to the new version and run `make generate`, so `/openapi.json` reports it. Pushing a `vX.Y.Z` tag runs every CI check, including the end-to-end and migration checks. If they pass, it builds the image, fails on any fixable HIGH or CRITICAL vulnerability that Trivy finds, then publishes it for amd64 and arm64 to `ghcr.io`, with an SBOM and a signed build provenance attestation. Verify an image before you run it:

```sh
gh attestation verify oci://ghcr.io/<OWNER>/<REPO>:<VERSION> --owner <OWNER>
```

## Terminate TLS in front of the API

- Serve the API only over HTTPS, with TLS ending at your load balancer or reverse proxy. The API speaks plain HTTP behind it.
- The API always sends `Strict-Transport-Security`. Browsers honor it only over HTTPS, so make sure no plain-HTTP route reaches the API, or a browser that saw it once refuses that host over HTTP for 180 days.
- Set `TRUST_PROXY_HOPS` to the number of proxies that append to `X-Forwarded-For`, usually `1`. Too low and every client shares one rate limit. Too high and clients can pick their own address.
- Cookie sessions need the browser app and the API on one HTTPS origin. See [Call the API from a browser](client.md#from-a-browser).

## Connect to Postgres over TLS

Verify the server's certificate, so nobody in the middle can read or change queries. Put the CA bundle in the image or a mounted secret, then add it to both URLs:

```text
postgres://api_app:<PASSWORD>@db.example.com:5432/api?sslmode=verify-full&sslrootcert=/etc/ssl/certs/db-ca.pem
```

## Create the roles and keep secrets out of the image

- `api_owner` owns the schema and runs migrations. Use the role your provider creates with the database, and set its password in the provider's console or CLI.
- `api_app` reads and writes rows but can't change the schema. Create it, or rotate its password, by running `docker/postgres/init.sql` as `api_owner`. The file takes the password from a setting, so you never edit it:

  ```sh
  PGOPTIONS="-c api.app_password=$API_APP_PASSWORD" psql "$MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 -f docker/postgres/init.sql
  ```

- Keep `DATABASE_URL` and `MIGRATION_DATABASE_URL` in your platform's secret store, such as Kubernetes Secrets, AWS Secrets Manager, or Fly secrets, and inject them as environment variables. Never bake them into the image: `.dockerignore` already keeps `.env` out of the build.
- Give the API only `DATABASE_URL`, and the migration job only `MIGRATION_DATABASE_URL`.

## Size the pool

Keep `DATABASE_POOL_MAX` × replicas, plus the migration job and your admin sessions, below Postgres's `max_connections`. With the default of 10, 8 replicas need about 90.

Don't put PgBouncer in transaction mode in front of the API. The cleanup job and `migrate` hold session-level advisory locks, and the pool sets `statement_timeout` when it connects, which transaction pooling breaks. Use session mode, or raise `max_connections` instead.

## Migrate, then roll out

1. Run `/api migrate` with `MIGRATION_DATABASE_URL`, as one job per deploy, such as a Kubernetes Job or a release command, before the new version takes traffic. It holds a Postgres advisory lock for the whole run, so a second run waits, then finds nothing to do, and running it from every replica is safe too.
2. Roll out the new image with `DATABASE_URL`.
3. Send traffic when `GET /readyz` returns 200. Use `GET /healthz`, or the image's built-in `HEALTHCHECK`, for liveness. It stays 200 during a database outage, so restarts don't make an outage worse.

## Drain on shutdown

On `SIGTERM`, `/readyz` returns 503 for `SHUTDOWN_DRAIN_SECONDS` (default 5) while the API keeps serving. Then it stops taking connections and gives in-flight requests `SHUTDOWN_TIMEOUT_SECONDS` (default 30) to finish, stops the cleanup job, closes the pool, and flushes telemetry.

- Set the platform's grace period above the sum, such as `terminationGracePeriodSeconds: 40` in Kubernetes or `stop_grace_period: 40s` in Compose. Otherwise it kills the API mid-request.
- Set `SHUTDOWN_DRAIN_SECONDS` above the time your load balancer takes to notice a failing `/readyz`, which is its check interval times its failure threshold.
- Set the load balancer's deregistration delay, such as an AWS target group's `deregistration_delay.timeout_seconds`, to at least `SHUTDOWN_TIMEOUT_SECONDS`, so it doesn't cut connections the API is still finishing.

## Decide on the docs

`DOCS_ENABLED` is off by default, so `/docs` and `/openapi.json` return 404 in production. Leave it off, or turn it on knowingly: it publishes every route and schema. See [ADR 008](decisions/008-turn-off-api-docs-by-default.md).

## Send telemetry

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to your collector, and `OTEL_SERVICE_NAME` to tell environments apart. Traces and metrics then flow over OTLP/HTTP, and every log line from a request carries its `trace_id`. See [Trace, measure, and log](operations.md#trace-measure-and-log).

## Back up the database

Turn on your provider's point-in-time recovery, and test a restore. See [Back up the database](operations.md#back-up-the-database) for self-hosted Postgres.
