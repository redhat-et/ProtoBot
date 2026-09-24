# WMS Implementations

Each WMS adapter implementation belongs in its own native component under
this directory, for example:

```text
wms/github/
wms/jira/
```

Implementations may use Go, Python, TypeScript, Rust, or another appropriate
language and own their build and test configuration.

The Go module in this directory contains two backend-neutral components:

- `validation/` — the pure `validation-rules/v1` lifecycle evaluator;
- `memory/` — an atomic in-memory WMS adapter for lifecycle and Drafting
  Table conformance tests.

Run the Go checks from this directory with `go test ./...` and `go vet ./...`.

The backend-neutral WMS failure vocabulary includes `UNKNOWN_MUTATION` for
writes whose result cannot be established. The in-memory adapter does not
produce that result: its operations commit atomically under the adapter lock,
so it has no external write whose outcome can be indeterminate. Backend
adapters must implement the documented reconciliation behavior where needed.
