# ADR-0005: Commit Hook Framework and Execution Environments

> Status: **Accepted** — November 2026

**Contents:**

- [Context](#context)
- [Decision](#decision)
- [Consequences](#consequences)
- [Related Documents](#related-documents)

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

The local Markdown hook never ran. The gate fails because a VCS clone is
required, not because the sandbox has no network. Consequently, while
`.pre-commit-config.yaml` contains remote repositories, a direct
`pre-commit` run fails unless a restored cache is present. Marking remote
hooks with `stages: [manual]` does not avoid the clone. Pre-commit still
cloned the repository during default execution. Therefore, this split requires
two distinct configuration files.

### The merged cache bootstrap

Main now seeds `PRE_COMMIT_HOME` before the sandbox starts, and restores the
archive inside `scripts/lint.py`. That makes `python scripts/lint.py` work
offline. It does not make upstream skill step 9b work, because step 9b calls
`pre-commit` directly and never restores the archive. The archive is built
before the agent edits the hook configuration, so it cannot cover a hook
added during the run. The bootstrap makes the wrapper work, but it does not
make the forced path work.

### The sandbox is partially online

The Fullsend `fullsend-package-registries` profile allows `pip`, `pip3`,
`python`, `python3`, `python3.*`, `uv`, `uvx`, `go`, and `pre-commit`.
The profile also allows network access to `pypi.org`, `files.pythonhosted.org`,
`proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com`,
`registry.npmjs.org`, and `registry.yarnpkg.com`. VCS clones still fail
because of the git TLS defect tracked in `fullsend#9694` and `agents#2105`.
Consequently, an invariant of no network access is incorrect.
The correct invariant is no VCS access, because forbidding PyPI access would
exile 21 working hooks.

### The toolchain inversion

The sandbox provides Go 1.26.0, but failing runner runs reported `go 1.24.13`.
The authoritative gate runs on `ubuntu-24.04` without `actions/setup-go`.
All five modules in `go.work` declare `go 1.25.12`. Version skew is not limited
to Go. CI pins `pre-commit` 4.6.2 through `PRE_COMMIT_VERSION` in
`scripts/lint.py`, while Fullsend ships 4.5.1 in the sandbox and installs 4.5.1
on the runner.

### Vendoring does not supply a toolchain

Pull request #555 added `validator/vendor/` in commit `2e93904`. Subsequent run
`44622772546` failed with `go: download go1.25.12: toolchain not available`.
Vendoring resolved module lookups, but it did not provide the Go toolchain.

### Why the gate is kept

ProtoBot rejected an empty or absent root configuration. Fullsend work in this
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
   `.pre-commit-config.yaml` is the gate configuration. It contains no
   VCS-cloned repository. Every entry is `repo: local`. A hook may use
   `language: python` with pinned `additional_dependencies`, or
   `language: system` with a standard-library `python3` entry. A
   `language: system` hook imports only the standard library.
   `.github/pre-commit-ci.yaml` contains every hook that needs a VCS clone,
   the Go toolchain, or a default-branch ref. Two commands cover four
   environments. The sandbox and the runner gate run `pre-commit run --files
   <changed>` against the gate configuration, which is also what upstream skill
   step 9b does. CI and developers run `scripts/lint.py`, which runs both
   configurations. `AGENTS.md` must point agents at the direct `pre-commit run`
   invocation, because the wrapper is not the sandbox path.
2. The gate invariant: `pre-commit run --all-files` against
   `.pre-commit-config.yaml` passes with a fresh `PRE_COMMIT_HOME` when VCS
   access is denied and `pypi.org`, `files.pythonhosted.org`,
   `proxy.golang.org`, and `sum.golang.org` are reachable. The gate provides
   `python3`, `git`, and the `pre-commit` version Fullsend installs. A fresh
   cache proves that no pre-materialized VCS clone is required.
3. A `gate-rehearsal` job in `ci-success` enforces the invariant. It runs on
   `ubuntu-24.04`, the `runner_image` from `.github/workflows/fullsend.yaml`. It
   runs no toolchain setup step. It installs the `pre-commit` version Fullsend
   installs, which one repository constant records. It first fails on any
   `.pre-commit-config.yaml` entry that breaks item 1, and it names that entry.
   It then runs `pre-commit run --all-files` directly, twice: once in the job
   environment, and once under `env -i` to catch hooks that read environment
   variables. Before each run it denies VCS access, and it fails unless these
   probes confirm the conditions: a `git ls-remote` against `github.com` fails;
   `git rev-parse --verify refs/heads/main` and `git rev-parse --verify
   refs/remotes/origin/main` both fail; `go`, `gofmt`, `node`, `npm` and `uvx`
   do not resolve; and `PRE_COMMIT_HOME` is empty. It fails on any skipped hook
   or any skipped test. It requires each checker to fail its negative fixture
   with the expected diagnostic. CI runs the same rehearsal on `ubuntu-latest`
   with the same probes. A hook leaves the gate only for one of three causes: a
   VCS clone, the Go toolchain, or a default-branch ref. Each hook that left the
   gate keeps an equivalent check in a job in `ci-success`. The rehearsal
   invokes `pre-commit` directly, as the Fullsend gate does, and
   `scripts/test_lint.py` permits that single invocation.
4. Every `repo: local` hook stays in the gate unless this table names it. The
   table lists only the exceptions.

   | Hook | Placement | Cause |
   | --- | --- | --- |
   | `markdownlint-cli2`, `hadolint`, `renovate-config-validator` | CI | Clones a VCS repository |
   | `gofmt`, `go-vet`, `spec-validator`, `test-spec-validator` | CI | Needs the Go toolchain |
   | `adr-amendments` | CI | Needs a default-branch ref |
   | `baseline-ratchet` | CI | Needs a default-branch ref |
   | `harness-integrity` with `--online-if-ci` | CI | Uses the network when `CI` is set |
   | `zizmor` | CI | Uses the network when `GH_TOKEN` is set |
   | `eval-corpus-schema`, `test-pre-commit-cache`, `test-select-comparison-base` | CI | Imports a module outside the standard library without declaring it |

   The gate keeps `harness-integrity` without `--online-if-ci`. A rehearsal
   result overrides the table.
5. Go checks run in CI. The `pre-commit` job runs `scripts/lint.py`, so it must
   install Go with `actions/setup-go` from `validator/go.mod` before that run.
   The existing `validator` job does not cover it, because each job runs on its
   own runner. Two obligations remain. `gofmt` and `go vet` must cover every
   module in `go.work`. Today's CI checks exclude `drafting-table`. The CI
   replacement for `spec-validator` must resolve an explicit default-branch ref
   and fail when it cannot.
6. No hook depends on the pre-seeded cache. The bootstrap merged in pull
   request #532 stays as an optimisation for explicit `scripts/lint.py` runs.
   It is not load-bearing, and retiring it is a follow-up. No pre-materialized
   artifact is required for the gate.

### Scope and non-goals

In scope is the gate configuration, the sandbox hook set, and the CI merge gate.
Out of scope is developer use of the validator, `docs/architecture.md`, the
Vision artifact, and any Toolkit capability. No registered specification
artifact changes, so no `ears-manager artifact put` is required.

## Consequences

### Accepted risk

The gate keeps the formatters, linters, and scanners. It loses markdownlint,
hadolint, renovate validation, `zizmor`, and the Go checks. An agent can open
a pull request with defects from those excluded checks, and CI blocks merge.
Secret scanning remains covered by the Fullsend post-script gitleaks step.
Issue #524 records that pull request #523 was committed with hooks not run.
Pull request #496 reached open state and failed CI on `ruff-format`.

### AGENTS.md reconciliation

`AGENTS.md:3-12` requires pre-commit and `go vet` before commit. Only `go vet`
leaves the agent path. The cutover must amend `AGENTS.md` to describe the gate
configuration, point Go checks at CI, and point agents at direct `pre-commit
run`. In addition, `adr-amendments` and `baseline-ratchet` move to CI, so
`AGENTS.md` Rule 7 and the context-budget rule are then enforced at merge, not
before the pull request.

### Cutover order

Cutover ships as one atomic change across five steps: create
`.github/pre-commit-ci.yaml`, teach `scripts/lint.py` to run both
configurations, move the three VCS hooks and the four Go hooks out of the gate
configuration, update `scripts/test_lint.py` to permit the single direct
`pre-commit` invocation in `gate-rehearsal`, and add the `gate-rehearsal`
job and wire it into `ci-success`, while amending `AGENTS.md` and the sandbox
instruction. Then prove the rehearsal on the resulting tree. Rollback reverts
the cutover commit as a whole. A hook returns to the gate only when it passes
`gate-rehearsal`.

### Follow-ups

| Follow-up | Rationale | Owner surface |
| --- | --- | --- |
| Base-aware CI validator | Resolves an explicit default-branch ref and fails when it cannot, so deletions are checked. | CI workflow, scripts |
| Renovate and CI cache keys | Add `managerFilePatterns` for `.github/pre-commit-ci.yaml`, and hash both configurations for the CI cache key. | `.github/` |
| Convert the three `language: system` hooks | Give `eval-corpus-schema`, `test-pre-commit-cache`, and `test-select-comparison-base` `language: python` with pinned `pyyaml` and `jsonschema`, then return them to the gate. | `.pre-commit-config.yaml` |
| Retire the cache bootstrap | The gate no longer depends on the archive, and removing it deletes the runner warm step and the restore path. | `.fullsend/`, `scripts/` |

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
