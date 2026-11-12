# 007. Key rate limits by network and limit failed logins per account

- **Status**: accepted
- **Context**: A client with IPv6 usually holds a whole /64, so a per-address limit lets it rotate addresses for free. A per-IP auth limit also can't stop password guessing spread across many addresses. The Hono, Go, and FastAPI starters must send the same headers and statuses.
- **Decision**: Count IPv4 clients by address and IPv6 clients by their /64, with IPv4-mapped IPv6 counted as IPv4. Also count failed logins per account, keyed by a SHA-256 of the normalized email, in the same Postgres table. Report every policy with the `RateLimit` and `RateLimit-Policy` headers from draft-ietf-httpapi-ratelimit-headers.
- **Consequences**:
  - Rotating addresses inside one /64 no longer resets a limit, but clients behind one /64 share a budget.
  - After `LOGIN_FAILURE_LIMIT` failures in `LOGIN_FAILURE_WINDOW_SECONDS`, the account's logins return 429 from every address, whether or not it exists, so the limit never reveals an account. An attacker can lock a known email out for one window, which is the usual trade-off.
  - Each login counts as a failure before the password check and is taken back when it succeeds, so concurrent guesses can't all slip under the limit.
  - The email is never stored in plain text in `rate_limits`.
  - The old `RateLimit-Limit`, `RateLimit-Remaining`, and `RateLimit-Reset` headers are gone, which breaks clients that read them.
