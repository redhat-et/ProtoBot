# WMS Implementations

Each WMS adapter implementation belongs in its own native component under
this directory, for example:

```text
wms/github/
wms/jira/
```

Implementations may use Go, Python, TypeScript, Rust, or another appropriate
language and own their build and test configuration.

The Go module in this directory contains:

- `adapter/` — backend-neutral wire types (`CallRequest` / `Result`) and
  operation surfaces shared by translators;
- `validation/` — the pure `validation-rules/v1` lifecycle evaluator;
- `memory/` — the in-memory fake adapter used by the lifecycle and
  Drafting Table conformance and fixture tests;
- `github/` — the first backend translator over GitHub Issues (issue #68).
  Lifecycle rules stay in `validation/`; GitHub code only persists ProtoBot
  JSON in issue bodies and uses an in-process claim coordinator for
  compare-and-swap. Tests use a fake GitHub client (no live credentials).

Run the Go checks from this directory with `go test ./...` and `go vet ./...`.

The backend-neutral WMS failure vocabulary includes `UNKNOWN_MUTATION` for
writes whose result cannot be established. The in-memory adapter does not
produce that result: its operations commit atomically under the adapter lock,
so it has no external write whose outcome can be indeterminate. Backend
adapters must implement the documented reconciliation behavior where needed.
