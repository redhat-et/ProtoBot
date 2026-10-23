# Drafting Table OpenCode fixture

Run the deterministic headless replay against the pinned OpenCode 1.18.30
binary:

```sh
PYTHONDONTWRITEBYTECODE=1 OPENCODE_BIN=/path/to/opencode-1.18.30/bin/opencode \
  python -m unittest discover -s tests/drafting-table -p 'test_*.py' -v
```

The driver builds the real `ears-manager`, `source-control-manager`, and
`drafting-table-guard` binaries from the existing Go modules, provisions an
isolated temporary Git project, and uses a loopback-only recorded model endpoint
plus recording WMS and `gh` stubs. It never edits the checkout or registered
specification artifacts. The contract fixture inputs, expected state, and
deferred steps are in
`docs/architecture/fixtures/drafting-table-opencode-golden.jsonl`.

The replay runs four scenarios, each twice, and compares each scenario's
semantic summary for byte stability:

- `approved-handoff` — the original #77 path: resume reads before writes,
  candidate review, validation failure, guard refusals, the Job Site
  lifecycle refusal (the role is never offered the tool, and the guard
  refuses it with rule 4), the apply_patch guard vectors, non-role
  redirections, and one explicit approved handoff (commit + publish +
  pull-request create, no merge).
- `rejection-revision` — an explicit rejection at the approval boundary
  makes no tool call and leaves WMS, Git-host, and baseline state
  unchanged; the requested Vision revision goes through
  `ears-manager artifact put` in a fresh session and stays uncommitted.
- `stale-state` — the default branch moves between session pause and
  resume; the resume detects the move from governed reads, publish fails
  `DEFAULT_MOVED`, and the refresh sequence (refresh, base-commit rebind,
  impact, validation, commit, publish) ends in one pull request, one
  host-side merge, and one idempotent `register-approved-change-set`.
- `wms-unavailable` — a session started with the WMS stub stopped
  (`--fail-start`) marks blocked work unavailable and continues drafting;
  OpenCode offers no WMS tool while the server is down, so the turn
  records no WMS call at all.

The actual `ears-manager artifact list` implementation is used by the guard
and verified directly; there is no CLI compatibility wrapper in this fixture.
The temporary `build` profile allows Bash so non-role redirection vectors reach
the guard. OpenCode 1.18.30 does not register a native `apply_patch` tool in the
resolved agent tool set. The fixture starts Drafting Table from a subdirectory,
then sends the documented `apply_patch` hook shape directly to the built guard
in both roles. It asserts root-relative `docs/vision.md` refusal and byte
preservation. A native OpenCode `apply_patch` invocation remains deferred; no
checked-out binding permission is changed.

Stubs: `wms_stub.py` serves the twelve Drafting Table WMS operations plus the
Job Site lifecycle operations `claim` and `record_merge` (advertised but never
offered to the role), and `--fail-start` records a failed start and exits
without serving. `gh_stub.py` records pull-request state and performs the
host-side merge on `pr merge` when `DT_GH_STATE` and `DT_GH_ORIGIN` are set.
`register_stub.py` is the local `register-approved-change-set` stand-in: it
reads the merge commit through the SCM's `approved-merge` face and appends one
durable, idempotent registration record to `DT_REG_LOG`.

The recorded demonstration (one approved path, one rejected path) is
`docs/architecture/fixtures/drafting-table-opencode-demo.md`, regenerated with:

```sh
PYTHONDONTWRITEBYTECODE=1 OPENCODE_BIN=/path/to/opencode-1.18.30/bin/opencode \
  python3 tests/drafting-table/render_demo.py
```

CI runs the same replay in the `drafting-table` job of
`.github/workflows/ci-workflow.yaml`, which also runs the guard module's Go
tests and verifies the pinned OpenCode binary by digest.
