# 008. Turn off the API docs by default

- **Status**: accepted
- **Context**: `/docs` and `/openapi.json` publish every route and schema, which helps an attacker map the API, and Swagger UI loaded scripts from a CDN, so a compromised CDN could run code on the API's origin. The Hono, Go, and FastAPI starters must behave the same.
- **Decision**: Serve both only when `DOCS_ENABLED=true`, which local development sets. Embed Swagger UI's files in the binary from the `github.com/swaggest/swgui` module, pinned in `go.mod`, and serve the page under a CSP that allows only this origin.
- **Consequences**:
  - A production deploy publishes no docs unless someone turns them on on purpose.
  - `/docs` loads nothing from another origin, and Renovate updates Swagger UI with the other Go modules.
  - The module repackages Swagger UI, so its version can trail the upstream release by a few weeks.
  - Projects that relied on `/docs` in production must set `DOCS_ENABLED=true`.
