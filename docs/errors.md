# Error reference

Every error is `application/problem+json` per [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457). Switch on `type`, because the other fields can change.

```json
{
  "type": "urn:problem:validation-failed",
  "title": "Request validation failed",
  "status": 400,
  "detail": "The request has invalid fields. See `errors` for each one, then fix them and retry.",
  "instance": "/v1/users",
  "errors": [
    { "field": "password", "detail": "must be at least 12 characters" }
  ]
}
```

| Type | Status | Meaning |
|---|---|---|
| `urn:problem:validation-failed` | 400 | A field is invalid, the JSON is malformed, or `limit` is over 100 |
| `urn:problem:invalid-credentials` | 401 | Wrong email or password |
| `urn:problem:unauthorized` | 401 | The token is missing, invalid, expired, or revoked |
| `urn:problem:not-found` | 404 | The route or resource doesn't exist, or isn't yours |
| `urn:problem:method-not-allowed` | 405 | The route exists, but not with this method |
| `urn:problem:email-taken` | 409 | The email is already registered |
| `urn:problem:version-mismatch` | 412 | `If-Match` doesn't match the note's current version |
| `urn:problem:payload-too-large` | 413 | The body is over 100 KiB |
| `urn:problem:unsupported-media-type` | 415 | The body isn't sent as `application/json` |
| `urn:problem:idempotency-key-reused` | 422 | The `Idempotency-Key` was used with a different body |
| `urn:problem:precondition-required` | 428 | `If-Match` is missing |
| `urn:problem:rate-limited` | 429 | Too many requests. Wait for `Retry-After` seconds |
| `urn:problem:internal-error` | 500 | Unexpected failure. Quote the request ID in `detail` |
| `urn:problem:service-unavailable` | 503 | Postgres is unreachable |

To add a type, add its slug and status to `internal/problem/problem.go`, a response to `api/openapi.yaml`, and a row here.
