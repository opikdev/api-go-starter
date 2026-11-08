# 002. Use sqlc, pgx, and goose

- **Status**: accepted
- **Context**: The data layer needs typed queries, Postgres features such as `ON CONFLICT` and `uuidv7()`, and migrations that ship with the binary. An ORM hides the SQL that agents and reviewers need to read.
- **Decision**: Write queries in SQL and generate Go with sqlc, run them with pgx and `pgxpool`, and embed goose SQL migrations in the binary.
- **Consequences**:
  - Every query is reviewable SQL, and type errors show up at compile time.
  - One image runs `serve`, `migrate`, and `seed`, so deploys need no extra tools.
  - Dynamic queries, such as optional filters, need `sqlc.narg` or hand-written code.
  - A schema change needs a migration and `make generate` in the same change.
