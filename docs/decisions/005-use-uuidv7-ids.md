# 005. Use UUIDv7 IDs

- **Status**: accepted
- **Context**: IDs must be opaque to clients, and cursor pagination needs a stable sort order. Postgres 18 generates UUIDv7 natively.
- **Decision**: Use `uuidv7()` primary keys, and expose them as prefixed strings such as `note_0190a7e4…`.
- **Consequences**:
  - Sorting by ID gives newest-first order, so a cursor is simply the last ID.
  - Inserts stay fast, because values are close to sequential.
  - IDs reveal their creation time, which `createdAt` already exposes.
  - The project needs Postgres 18 or later.
