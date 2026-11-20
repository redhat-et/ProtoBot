# ADR-0005: Commit Hook Framework and Execution Environments

> Status: **Accepted** — November 2026

## Context

### Observed failures

Four Fullsend runs for issues #442, #512, and #520 ended with
`FAIL: pre-commit-blocked` on `spec-validator`. Failures split between
toolchain version skew and module lookups that `spec-validator` disables with
`GOPROXY=off`.

| Issue | Run | Error message |
| --- | --- | --- |
| #442 | 44587146936 | `go: go.mod requires go >= 1.25.12 (running go 1.24.13; GOTOOLCHAIN=local)` |
| #512 | 44589278831 | `go: downloading gopkg.in/yaml.v3 v3.0.1 / module lookup disabled by GOPROXY=off` |
| #520 | 44617568693 | Version skew error (`requires go >= 1.25.12`) |
| #520 | 44755416843 | Network disabled error (`module lookup disabled by GOPROXY=off`) |

### The gate cannot work while it clones

Pre-commit initializes every remote repository in its configuration before it
evaluates any `files:` pattern. In a test with an empty cache and no network
access, `pre-commit run --files foo.md` failed immediately:

```text
[INFO] Initializing environment for https://github.com/pre-commit/pre-commit-hooks
CalledProcessError: ('git', 'fetch', 'origin', '--tags')
fatal: unable to access ... Could not connect to server
```

The local Markdown hook never ran. Consequently, while `.pre-commit-config.yaml`
contains remote repositories, a run that calls `pre-commit` directly fails
offline unless something restores a pre-seeded cache first. Marking remote
hooks with `stages: [manual]` does not avoid the clone. Pre-commit still cloned
the repository during default execution. Therefore, this split requires two
distinct configuration files.

### The merged cache bootstrap

Main now seeds `PRE_COMMIT_HOME` before the sandbox starts, and restores the
archive inside `scripts/lint.py`. That makes `python scripts/lint.py` work
offline. It does not make the skill's step 9b work, because step 9b calls
`pre-commit` directly and never restores the archive. The archive is built
before the agent edits the hook configuration, so it cannot cover a hook
added during the run. The bootstrap makes the wrapper work, but it does not
make the forced path work.

### The toolchain inversion

The sandbox provides Go 1.26.0, but failing runner runs reported `go 1.24.13`.
The authoritative gate runs on `ubuntu-24.04` without `actions/setup-go`.
All five modules in `go.work` declare `go 1.25.12`. Version skew is not limited
to Go. CI pins `pre-commit` 4.6.2 (`scripts/lint.py:55`), while Fullsend ships
4.5.1 in the sandbox and installs 4.5.1 on the runner.

### Vendoring does not supply a toolchain

Pull request #555 added `validator/vendor/` in commit `2e93904`. Subsequent run
`44622772546` failed with `go: download go1.25.12: toolchain not available`.
Vendoring resolved module lookups, but it did not provide the Go toolchain.

### Why the gate is kept

An empty or absent root configuration was rejected because Fullsend work in this
repository frequently changes hook scripts, and the gate self-tests catch those
defects before a pull request opens. A pre-materialized artifact is permitted
only where CI verifies it by regeneration, and the gate uses none.

### Why husky and lefthook were rejected

The Fullsend gate runs only `pre-commit`. Husky sets `core.hooksPath` and
conflicts with the commit path (`agents#577`). Lefthook would replace
`validation_loop.script`. The framework choice is orthogonal to every observed
failure.

## Decision

1. ProtoBot keeps `pre-commit`, and the hook set splits at one boundary.
   `.pre-commit-config.yaml` is the gate configuration. Every entry in it is
   `repo: local`, every hook uses `language: system`, and every `entry` begins
   with `python3 scripts/`. `.github/pre-commit-ci.yaml` contains every other
   hook and is excluded from the gate. There are three entry points. The sandbox
   and the runner gate run `pre-commit run --files <changed>` against the gate
   configuration only, which is also what upstream skill step 9b does. CI and
   developers run `scripts/lint.py`, which runs both configurations. `AGENTS.md`
   must point agents at the direct `pre-commit run` invocation, because the
   wrapper is not the sandbox path.
2. On each commit that CI accepts, `pre-commit run --all-files` against
   `.pre-commit-config.yaml` passes in the gate environment. The gate
   environment has an empty, writable `PRE_COMMIT_HOME`. It has no network. It
   is a shallow git work tree with no default-branch ref. It provides `python3`
   3.12 with the standard library only, `git` 2.28 or later, and the
   `pre-commit` version Fullsend installs. Any values of `CI`, `GITHUB_ACTIONS`
   and `GITHUB_BASE_REF` are allowed.
3. A `gate-rehearsal` job in `ci-success` enforces the invariant. It runs on
   `ubuntu-24.04`, the `runner_image` from `.github/workflows/fullsend.yaml`. It
   runs no toolchain setup step. It installs the `pre-commit` version Fullsend
   installs, which one repository constant records. It first fails on any
   `.pre-commit-config.yaml` entry that breaks Decision 1, and it names that
   entry. It then runs `pre-commit run --all-files` directly inside a network
   namespace with no route to any destination, and with `PATH` limited to the
   directories that provide `git` and `python3`. It repeats the run once under
   `env -i` to catch hooks that depend on environment variables. `env -i` alone
   isolates neither network access nor the set of callable programs. Before each
   run it fails unless isolation is proven: a connection attempt to any address
   fails; `go`, `gofmt`, `node`, `npm` and `uv` do not resolve; `python3 -c
   'import yaml, jsonschema'` fails; `git rev-parse --verify refs/heads/main`
   and `git rev-parse --verify refs/remotes/origin/main` both fail; and
   `PRE_COMMIT_HOME` is empty. It fails on any skipped hook or any skipped
   test. It requires each checker to fail its negative fixture with the expected
   diagnostic. A hook leaves the gate only for one of four causes: network, an
   executable other than `git` or `python3`, a module outside the standard
   library, or a missing default-branch ref. Each hook that left the gate keeps
   an equivalent check in a job in `ci-success`, and carries a `# gate-excluded:
   <cause>` comment in `.github/pre-commit-ci.yaml`. The rehearsal invokes
   `pre-commit` directly, as the Fullsend gate does, and `scripts/test_lint.py`
   permits that single invocation.
4. At cutover, each `repo: local` hook is placed as this table shows. A
   rehearsal result overrides the table.

   | Hook | Placement | Cause |
   | --- | --- | --- |
   | `check-portable-null-device`, `validator-spec-parity`, `validator-fixtures` | Gate | Reads the working tree only |
   | `test-check-portable-null-device`, `test-validator-spec-parity`, `test-validator-fixtures`, `test-adr-amendments`, `test-harness-integrity`, `test-baseline-ratchet`, `test-lint-wrapper`, `test-pre-commit-cache` | Gate | Uses temporary repositories and mocks only |
   | `harness-integrity` without `--online-if-ci` | Gate | Checks structure offline |
   | `harness-integrity` with `--online-if-ci` | CI | Uses the network when `CI` is set |
   | `adr-amendments` | CI | Needs a default-branch ref |
   | `baseline-ratchet` | CI | Needs a default-branch ref or network |
   | `eval-corpus-schema` | CI | Needs the `yaml` and `jsonschema` modules |
   | `test-select-comparison-base` | CI | Needs the `yaml` module |
   | `gofmt`, `go-vet`, `spec-validator`, `test-spec-validator` | CI | Needs the `go` executable |

   Remote repositories move to CI.
5. Go checks run in CI. The `pre-commit` job runs `scripts/lint.py`, so it must
   install Go with `actions/setup-go` from `validator/go.mod` before that run.
   The existing `validator` job does not cover it, because each job runs on its
   own runner. Two obligations remain. `gofmt` and `go vet` must cover every
   module in `go.work`, which excludes `drafting-table` today. The CI
   replacement for `spec-validator` must resolve an explicit default-branch ref
   and fail when it cannot.
6. No hook depends on the pre-seeded cache. The bootstrap merged in pull
   request #532 stays as an optimisation for explicit `scripts/lint.py` runs.
   Once the gate configuration contains no remote repositories, the bootstrap
   is not load-bearing, and retiring it is a follow-up. No pre-materialized
   artifact is required for the gate.

### Scope and non-goals

In scope is the gate configuration, the sandbox hook set, and the CI merge gate.
Out of scope is developer use of the validator, `docs/architecture.md`, the
Vision artifact, and any Toolkit capability. No registered specification
artifact changes, so no `ears-manager artifact put` is required.

## Consequences

### Accepted risk

The gate no longer runs third-party linters, formatters, scanners, or Go checks.
An agent can open a pull request with lint or formatting defects, and CI blocks
merge. Secret scanning remains covered by the Fullsend post-script gitleaks
step. Pull request #496 reached open state and failed CI on `ruff-format`.
Issue #524 records that pull request #523 was committed with hooks not run.

### AGENTS.md reconciliation

`AGENTS.md:3-12` requires pre-commit and `go vet` before commit. The cutover
must amend it to describe the gate configuration, point Go checks at CI, and
point agents at direct `pre-commit run`. In addition, `adr-amendments` and
`baseline-ratchet` move to CI, so `AGENTS.md` Rule 7 and the context-budget
rule are then enforced at merge, not before the pull request.

### Cutover order

Cutover ships as one atomic change across five steps: create
`.github/pre-commit-ci.yaml`, teach `scripts/lint.py` to run both
configurations, update `scripts/test_lint.py` to permit the single direct
`pre-commit` invocation in `gate-rehearsal`, move the classified hooks, and add
the `gate-rehearsal` job and wire it into `ci-success`, while amending
`AGENTS.md` and the sandbox instruction. Then prove the rehearsal on the
resulting tree. Rollback reverts the cutover commit as a whole, and a hook
returns to the gate only when it passes `gate-rehearsal`.

### Follow-ups

| Follow-up | Rationale | Owner surface |
| --- | --- | --- |
| Base-aware CI validator | Resolves an explicit default-branch ref and fails when it cannot, so deletions are checked. | CI workflow, scripts |
| Renovate and CI cache keys | Add `managerFilePatterns` for `.github/pre-commit-ci.yaml`, and hash both configurations for the CI cache key. | `.github/` |
| Retire the cache bootstrap | Once the gate contains no remote repositories, the archive is dead weight, and removing it deletes the runner warm step and the restore path. | `.fullsend/`, `scripts/` |

### What would change this decision

ProtoBot will re-evaluate this decision if Fullsend pins the gate environment,
sandbox git egress works reliably, or Fullsend supports a non-pre-commit gate.
A path combining the bootstrap with `agents#2091`, where Fullsend lets a
repository name its own offline lint command, is rejected because it depends on
an upstream change and keeps a per-run archive build in the critical path.

## Related Documents

- [ADR-0001: One-File-Per-Record YAML Storage Format](0001-requirements-storage-format.md)
- [ADR-0002: EARS Specification Record Schema](0002-ears-specification-record-schema.md)
- [ADR-0003: EARS Manager Storage Layout and Schema Version Keys](0003-ears-manager-storage-layout.md)
- [ADR-0004: Adopt ProtoBot V2](0004-adopt-protobot-v2.md)
- [Issue #442: Define ADR supersession, rename, and deletion lifecycle in AGENTS.md Rule 7 and check_adr_amendments.py](https://github.com/redhat-et/ProtoBot/issues/442)
- [Issue #512: Govern module-level README files under AGENTS.md sibling entry and terminology rules](https://github.com/redhat-et/ProtoBot/issues/512)
- [Issue #520: Enforce baseline ceiling retention and warn-threshold parity in check_baseline_ratchet.py](https://github.com/redhat-et/ProtoBot/issues/520)
- [Issue #524: Bootstrap pre-commit hook cache for sandboxed agent runs](https://github.com/redhat-et/ProtoBot/issues/524)
- [Issue #544: Vendor validator dependencies or scope spec-validator hook to prevent offline build failures in pre-commit](https://github.com/redhat-et/ProtoBot/issues/544)
- [Issue #550: Convert the PyPI-capable cloned pre-commit hooks to repo: local](https://github.com/redhat-et/ProtoBot/issues/550)
- [PR #496: fix(#442): define ADR supersession, rename, and deletion lifecycle](https://github.com/redhat-et/ProtoBot/pull/496)
- [PR #523: docs(#512): govern module README files under AGENTS.md sibling rules](https://github.com/redhat-et/ProtoBot/pull/523)
- [PR #555: fix(#550): convert GitHub-cloning pre-commit hooks to repo: local](https://github.com/redhat-et/ProtoBot/pull/555)
- [fullsend#9694: Sandbox TLS environment dropping](https://github.com/fullsend-ai/fullsend/issues/9694)
- [agents#2105: Sandbox git TLS configuration](https://github.com/fullsend-ai/agents/issues/2105)
- [agents#577: Husky hook conflict in commit path](https://github.com/fullsend-ai/agents/issues/577)
- [agents#2091: AGENTS.md offline lint runner configuration](https://github.com/fullsend-ai/agents/issues/2091)
