# 001. Use the standard library with a contract-first spec

- **Status**: accepted
- **Context**: Since Go 1.22, `net/http` routes by method and path pattern, so a router library adds little. The API also needs an OpenAPI spec that can't drift from the code, and typed request handling.
- **Decision**: Write the spec by hand in `api/openapi.yaml`. Generate a strict server and a client with `oapi-codegen`, serve them on `http.ServeMux`, and validate every `/v1` request with kin-openapi before it reaches a handler.
- **Consequences**:
  - The spec is the source of truth. A missing or mistyped handler fails to compile, and `make check-generated` fails if generated code is stale.
  - Validation, auth requirements, and error responses come from one file.
  - Changing an endpoint means editing YAML first, which is slower for quick experiments.
  - There's no framework to upgrade, only the standard library and two generators.
