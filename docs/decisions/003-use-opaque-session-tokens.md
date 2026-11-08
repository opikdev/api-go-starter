# 003. Use opaque session tokens

- **Status**: accepted
- **Context**: The API needs auth that works from one command, and logout must take effect at once. JWTs need a signing secret and can't be revoked without a deny list.
- **Decision**: Issue random bearer tokens, store only their SHA-256 hash in Postgres, and hash passwords with argon2id from `golang.org/x/crypto`, using the OWASP minimum parameters.
- **Consequences**:
  - Logout and expiry work immediately, and a database leak doesn't expose usable tokens.
  - There's no secret to configure, and no auth library to depend on.
  - Each authenticated request runs one extra query.
  - Social login or SSO would need a library or an identity provider.
