# 004. Keep rate limits in Postgres

- **Status**: accepted
- **Context**: Limits must hold across instances, or each new instance adds to the budget. In-memory counters can't do that, and Redis would be a second service to run.
- **Decision**: Count requests in a fixed one-minute window with an atomic upsert on `rate_limits`.
- **Consequences**:
  - Limits are shared across instances, with no new infrastructure.
  - Each `/v1` request adds a write, so move to Redis if this shows up under load.
  - The API fails closed: if Postgres is down, `/v1` returns 500, not unlimited access.
