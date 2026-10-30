# Source Control Manager

`source-control-manager` is the one deterministic component that turns the
user's decision about a change set into Git and Git host state. The design
is [the Source Control Manager document][design]; this directory implements
it (issue #160).

```text
source-control-manager [--output human|json] <operation> [options]
source-control-manager serve --face drafting-table [--transport stdio]
source-control-manager --version
```

## What it serves

- **The Drafting Table face**, as an MCP server over stdio: `repo_state`,
  `branch_init`, `branch_resume`, `commit`, `publish`, and `refresh`. The
  server is dual-era: a modern client gets MCP revision 2026-07-28, and a
  client that opens with `initialize` gets 2025-11-25. The results are the
  same in both eras.
- **The approved-state read face**, as the CLI subcommand `approved-merge
  --change-set CS-NNNNN`, for `register-approved-change-set` and the
  Materializer.
- **The same operations as CLI subcommands** (`repo-state`, `branch-init`,
  and so on), with the same checks as an MCP call.

It runs `git`, `gh`, and `ears-manager` as child processes with argument
lists, never through a shell. Every Git command runs with an empty hooks
directory of its own, `core.fsmonitor` off, and literal pathspecs. A commit
is built in a private index and moved onto the branch with a
compare-and-swap `git update-ref`.

## Job Site Worker and Triage fixture

`internal/jobsite` implements the backend-neutral Worker/Triage fixture
loop (#69), projection isolation, and the `jobsite-sandbox/v1` local test
adapter. `RunFixtureLoop` accepts a `LoopRequest` that pins the source
commit, projection policy, approved requirement IDs, sandbox resource
limits, deterministic Worker/test/Triage callbacks, and a `LoopWMS`
implementation. The in-memory WMS supplies the local lifecycle fixture;
authorization, state versions, fencing tokens, and idempotency remain
enforced at the WMS boundary.

Each cycle exports fresh Worker A (tests), Worker B (implementation), and
private Integration repositories. Workers edit only their role projection
through a sandbox `Session`. The loop validates path-restricted PatchBundle
v1 imports, preserves contributions on private integration-side branches,
merges them with merge commits, and runs the supplied Integration test.
Accepted patch audit records are pinned in the same atomic Git ref
transaction as the integration commits. Audit JSON sidecars are derived
views; `RecoverCycleAudits` rebuilds them from those private Git records
before another import. Rollback restores refs before removing sidecars
and reports cleanup failures. Imports and recovery for the same Integration
repository run serially under its single Job Site owner.
Fixture Triage represents test faults, implementation faults, both faults,
and specification gaps. Later cycles dispatch only the Workers selected
by the preceding sanitized feedback.

`SanitizeFeedback` permits only outcome, fault classification, approved
requirement IDs, retry budget, and generic requirement-level reasons and
revision requests. Test faults route to Worker A; implementation faults
route to Worker B; both faults route to both. Free-text reasons, raw logs,
and raw Inspector findings are retained privately and always discarded
from Worker feedback. Unsupported classifications, unapproved requirement
IDs, stale or invalid patches, sandbox errors, test-runner errors, and WMS
rejections fail closed. An ordinary failing test is a nonzero test status
with no runner error.

`MaxRetries` excludes the initial attempt: zero allows one cycle, and N
allows at most N + 1 cycles. Retryable failures leave the work item in
`building`. A passing suite invokes `tests-pass` and moves it to
`inspecting`; it does not complete the work item. A specification gap
invokes `raise-spec-question` and moves it to `blocked`. Exhausting retries
ends the fixture with `retry-exhausted` and a recorded escalation while
leaving the work item in `building`; it grants no abandonment authority.
Each `RunID` is single-use. The loop rejects a replayed claim before
dispatch; resuming a prior run requires reconciliation outside this fixture.

The new `OutputDir` is private (mode `0700`). Its `cycle-NNNN.json` records
and `report.json` (mode `0600`) retain patch provenance, sandbox cleanup
audits, test output, raw Triage input, sanitizer decisions, feedback, and
authoritative WMS transitions. Incomplete cycles also produce evidence.
`VerifyCycleEvidence` verifies the cycle records' canonical JSON hash
chain. These records and Integration repositories are control-plane data;
only `WorkerFeedback` crosses the Worker boundary.

Run the local projection, sandbox, and Worker/Triage fixture tests with:

```sh
go test ./internal/jobsite
```

This package is separate from the Drafting Table SCM face. In-process
callbacks and the local adapter prove the fixture contract; they do not
enable autonomous model execution. Real model execution and the Fullsend
adapter's integration-level isolation checks remain later work (#172).

## Not in this build

- **The hosted face behind the Gate.** `serve --transport streamable-http`
  exits non-zero and never listens. The hosted face is out of the scope of
  #160.
- **A bundled `ears-manager` CLI.** `ears-manager` is a separate executable
  and is not bundled with `source-control-manager`. The SCM invokes it through
  `ears-manager --output json`; [`internal/ears`](internal/ears/ears.go)
  documents the fields the SCM consumes from its results.

## Build and test

```sh
go build ./cmd/source-control-manager
go test ./...
```

The ordinary `go test ./...` run replays the golden fixture,
[`source-control-manager-golden.jsonl`][fixture], against the built binary:
it starts `serve`, calls the tools as an MCP client, and compares every
result. Stubs stand in for `gh` ([`internal/testing/ghstub`][ghstub]) and
`ears-manager` ([`internal/testing/earsstub`][earsstub]). CI keeps this
stub-backed run and separately builds the workspace `ears-manager` binary
before replaying the fixture against it. The hosted-face fixture rows remain
skipped.

To run the real-binary replay locally:

```sh
(cd ../ears-manager && go build -o /tmp/ears-manager ./cmd/ears-manager)
SCM_FIXTURE_EARS_MANAGER=/tmp/ears-manager go test -count=1 -v ./internal/golden
```

That mode skips the three stub-only checks: scm-22 (protected path), scm-35
(directory path), and protected names in another letter case. The hosted-face
fixture rows remain skipped in both modes.

[design]: ../docs/architecture/source-control-manager.md
[fixture]: ../docs/architecture/fixtures/source-control-manager-golden.jsonl
[ghstub]: internal/testing/ghstub/main.go
[earsstub]: internal/testing/earsstub/main.go
