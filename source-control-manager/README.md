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

## Job Site projection fixture

`internal/jobsite` is the standalone Job Site projection seam for
issue #78. It exports Worker A, Worker B, and a private Integration
repository from a source commit and a version-1 projection policy,
and it validates PatchBundle v1 imports. It is not a Drafting Table
SCM face. Sandbox enforcement, the integration loop, and Fullsend
mapping remain #79, #69, and #172.

`go test ./internal/jobsite` is the mandatory local isolation fixture.

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
