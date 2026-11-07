# The Server owns the wire contract and the Client's types are generated from it

**Status:** accepted

The Client and the Server exchange JSON on 22 paths (`/health`, `/status`,
`/pair*`, `/admin/*`, `/transcribe`). The Server describes none of those
responses: every handler returns `dict[str, Any]`, no route declares a
`response_model`, and the ten schemas FastAPI emits are all request bodies. The
generated OpenAPI therefore says only that each 2xx response is an object with
`additionalProperties: true`. The Client mirrors those same payloads by hand:
32 wire DTOs and 765 `json:` tags across `client/internal/servers`,
`transcribe`, and `trust`.

Nothing checks the two definitions against each other. The Go tests encode the
Server's payloads themselves, so they can only prove the Client agrees with the
test author; the Python tests assert the very dicts the Server builds, so they
can only prove the Server agrees with itself. A field rename on either side
passes both suites and surfaces as an empty value in the UI. Mixed versions do
happen in practice — [ADR 0012](0012-local-server-hard-lifecycle.md) even has the
Client fall back when a Server predates an endpoint — so the two halves of the
wire are not guaranteed to be built together.

The Server is the producer and the source of truth. Every JSON response gets a
Pydantic response model and routes declare it with `response_model`; FastAPI's
schema is committed as `server/openapi.json` by a mise task. The Client's wire
types are generated from that schema into `client/internal/api` and nothing
else is generated: transport, TLS pinning, the Device token, multipart upload,
warning callbacks, and error classification stay hand-written because they are
policy, not shape. CI regenerates the schema and the Go types and fails on a
diff, so a change to the contract is visible in the pull request that makes it.
Payloads that are genuinely open-ended — the effective config behind
`/admin/config`, where keys come from the user's YAML — keep
`additionalProperties: true` and are guarded by golden fixtures instead.

## Considered options

- **(a) Chosen — typed Server responses, generated Client types.** One source of
  truth, a check that already exists for free in CI, and no new language in the
  repository. The Client keeps the code that encodes security and lifecycle
  policy.
- **(b) TypeSpec as the single spec-first source.** Rejected: TypeSpec has
  client emitters only for JavaScript, Python, Java, and C#, and server emitters
  only for C# and JavaScript, so neither of this project's two languages can be
  generated. It would add a third definition (`.tsp`, Pydantic, Go structs) and
  a Node toolchain to a repository pinned to Go and `uv`.
- **(c) Hand-written DTOs plus shared golden fixtures.** Rejected as the only
  mechanism: it detects drift but leaves the Server untyped, keeps all 765 tags,
  and cannot catch a rename until someone edits the fixture by hand. Retained
  where the payload is genuinely dynamic.
- **(d) Generate the whole Client from OpenAPI.** Rejected: certificate pinning,
  the Device token, multipart assembly, warning delivery, and the distinction
  between the Server's error codes are decisions, not generated shape.
- **(e) Keep OpenAPI uncommitted and rely on the existing tests.** Rejected: no
  test today crosses the language boundary, so it would keep the present state.

## Consequences

- `response_model` validates and **filters**: an undeclared key silently leaves
  the response. A contract test asserts that each handler's payload keys equal
  its model's fields, so a forgotten field fails CI instead of disappearing in
  production.
- Redaction stays part of the contract: `/admin/config` models secrets as the
  `"***"` placeholder, and the existing redaction test keeps guarding it.
- `basedpyright` can check the Server's response values, and a breaking change
  appears as an OpenAPI diff in the pull request rather than as a bug report.
- `oapi-codegen` becomes a pinned development tool in `mise.toml`. It adds no
  runtime dependency: the generated Go file is committed and the Client still
  builds with `go build`.
- Generated files churn on every shape change. They are updated in a separate
  `spec:` commit and marked `linguist-generated` so review stays readable.
- Runtime compatibility is unchanged: the `/health` preflight and the Client's
  version check remain the mechanism that protects mixed-version installs.
  The schema check catches drift at development time, not at run time.
- Swagger UI can be re-enabled for diagnostics later because the schema becomes
  meaningful. It is not part of this decision.
