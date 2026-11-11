# 006. Cookie sessions for same-origin browser apps

- **Status**: accepted
- **Context**: A browser app that keeps a bearer token in JavaScript loses it to any XSS bug. A cookie keeps the token out of JavaScript, but the browser then sends it on its own, so the API needs CSRF protection. The Hono, Go, and FastAPI starters must share one contract, so a frontend works with any of them.
- **Decision**: `POST /v1/sessions` with `delivery: "cookie"` sets the same opaque token as a `__Host-session` cookie that's `HttpOnly`, `Secure`, `SameSite=Strict`, and `Path=/`, with no `Domain`. The auth middleware reads the cookie only when there's no `Authorization` header. Every `/v1` request that changes data goes through `http.CrossOriginProtection`, which rejects it when `Sec-Fetch-Site` or `Origin` shows another origin that isn't in `CORS_ORIGINS`. Bearer tokens stay the default.
- **Consequences**:
  - JavaScript never sees a cookie session token, so XSS can't steal it, only use it while the page is open.
  - `__Host-` stops a subdomain or plain-HTTP page from setting or replacing the cookie. `SameSite=Strict` keeps it off cross-site requests, and the `Sec-Fetch-Site` check blocks the rest, including login CSRF, with no token for the client to manage.
  - The browser app must be on the API's origin, behind a reverse proxy, because CORS stays without credentials. Cross-site browser requests that change data are rejected unless their origin is in `CORS_ORIGINS`.
  - Bearer clients, such as CLIs, servers, and mobile apps, work as before and don't need a proxy. A cookie is a poor fit for them, so it stays opt-in.
  - Both modes share one session table, so expiry, revocation, and logout behave the same.
