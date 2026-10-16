# Drafting Table guard

`drafting-table-guard` is the standalone Go module for the shared Drafting
Table tool-call guard. This release implements the OpenCode 1.18.30 tool
vocabulary row; other harnesses are not accepted.

The OpenCode row recognizes `edit`, `write`, and `apply_patch` as file-writing
tools (`apply_patch` receives its target paths in `patchText`), `bash` as the
shell tool, and `read`, `glob`, `grep`, and `skill` for reads and skill loads.

Build and run it with:

```sh
go build -o drafting-table-guard ./cmd/drafting-table-guard
# Or install the executable under its contract name on PATH.
go install ./cmd/drafting-table-guard
printf '%s\n' '{"hook_event_name":"PreToolUse","session_id":"fixture","cwd":"/project","tool_name":"bash","tool_input":{"command":"date -u +%Y-%m-%dT%H:%M:%SZ"}}' |
  ./drafting-table-guard --harness opencode --role drafting-table
```

Allow decisions exit `0` with no output. Refusals and inspection failures exit
`2` and write one actionable line to standard error. The process does not
write files or open network connections. It reads the project configuration,
adapter manifest, and `ears-manager` registry views for each project call;
its Git subprocesses are read-only and bounded by a timeout.

Tests use local temporary Git repositories and fake `ears-manager` responses:

```sh
go test ./...
go vet ./...
```
