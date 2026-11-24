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

## Amendment (November 2026)

**[Amended November 2026, #585]**

On 2026-11-23, an independent peer review gave this record a "Major revisions"
rating. The decision direction stands, but the enforcement text must change.
Issue #585 tracks this amendment to resolve review defects D1 through D23.

### Resolution map

| Defect | Resolution |
| --- | --- |
| D1 | Skipped-hook definition, `### Rehearsal` |
| D2 | Negative-fixture rule, `### Rehearsal` |
| D3 | `### Cutover order` |
| D4 | Five-cause rule, `### Placement rule` |
| D5 | `pygrep` language, `### Placement rule` |
| D6 | Deny rule and probes, `### Rehearsal` |
| D7 | Run environment, `### Rehearsal` |
| D8 | Endpoint list, `### Gate invariant` |
| D9 | `gofmt` and `go vet`, `### Base-aware validation` |
| D10 | `drafting-table` coverage, `### Base-aware validation` |
| D11 | Base resolution, `### Base-aware validation` |
| D12 | Bootstrap scope, `### Bootstrap` |
| D13 | Version constants, `### Rehearsal` |
| D14 | Install conditions and clone trace, `### Gate invariant` |
| D15 | Accepted risk, `### Accepted risk` |
| D16 | Matrix labels and test allowlist, `### Rehearsal` |
| D17 | Terminology and scope, `### Terminology, scope, and prose corrections` |
| D18 | Pinned upstream facts, `### Gate invariant` |
| D19 | Hook count, `### Placement rule` |
| D20 | `CLAUDE.md` symlink, `### Terminology, scope, and prose corrections` |
| D21 | Prose corrections, `### Terminology, scope, and prose corrections` |
| D22 | Follow-up links, `### Follow-ups` |
| D23 | IdeaBot qualifier, `docs/architecture/related-work.md:111` |

### Placement rule (extends items 1, 3, and 4)

It extends Decision items 1, 3, and 4.
One rule governs every hook:

- A hook stays in the gate configuration when it meets none of the five causes.
- A hook moves to the CI configuration only for one of five causes:
  1. It needs a VCS clone.
  2. It needs the Go toolchain.
  3. It needs a default-branch ref.
  4. It uses the network when an environment variable enables the network.
  5. It imports a module outside the Python standard library and does not
     declare the dependency.

A hook that needs a secret token has cause 4.
A pass without the token is not evidence that the hook works.
Permitted gate languages are `python` with pinned `additional_dependencies`,
`system` with a standard-library `python3` entry, and `pygrep` with an inline
pattern entry and no dependency.
The `pygrep` language is built into `pre-commit`.
The gate configuration permits `text-unicode-replacement-char`
(`.pre-commit-config.yaml:158-161`).

The predicate for cause 4 is `CI=true`, not "`CI` is set".
`CI=false` and an unset `CI` environment variable select offline mode
(`scripts/check_harness_integrity.py:298-300`).

The sentence "A rehearsal result overrides the table" means that a failed
rehearsal is a reason to investigate.
It does not by itself move a hook out of the gate configuration.
A hook moves out only when it meets one of the five causes and a named job in
`ci-success` keeps an equivalent check.

The statement in Context that forbidding PyPI access would exile 21 working
hooks is superseded by this amendment.
At commit `eb021b7`, `.pre-commit-config.yaml` has 44 hook entries: 20 using
`language: python`, 20 using `language: system`, 1 using `language: pygrep`,
and 3 remote entries.
It has 20 Python hooks, not 21.
At commit `4269c68`, `.pre-commit-config.yaml` has 44 hook entries: 22 using
`language: python`, 18 using `language: system`, 1 using `language: pygrep`,
and 3 remote entries. After cutover, the gate configuration keeps 31 local
hooks: 20 python, 1 pygrep, and 10 system.

The following table supersedes the table in Decision item 4:

| Hook | Placement | Cause |
| --- | --- | --- |
| `markdownlint-cli2` | CI | Needs a VCS clone |
| `hadolint` | CI | Needs a VCS clone |
| `renovate-config-validator` | CI | Needs a VCS clone |
| `gofmt` | CI | Needs the Go toolchain |
| `go-vet` | CI | Needs the Go toolchain |
| `spec-validator` | CI | Needs the Go toolchain |
| `test-spec-validator` | CI | Needs the Go toolchain |
| `adr-amendments` | CI | Needs a default-branch ref |
| `baseline-ratchet` | CI | Needs a default-branch ref |
| `harness-integrity` with `--online-if-ci` | CI | Uses the network when an environment variable enables the network |
| `harness-integrity` without `--online-if-ci` | Gate | No cause applies |
| `zizmor` | CI | Uses the network when an environment variable enables the network |
| `eval-corpus-schema` | CI | Imports a module outside the Python standard library and does not declare the dependency |
| `test-pre-commit-cache` | CI | Imports a module outside the Python standard library and does not declare the dependency |
| `test-select-comparison-base` | CI | Imports a module outside the Python standard library and does not declare the dependency |
| `text-unicode-replacement-char` | Gate | No cause applies |

Every CI row names one of the five causes. The two Gate rows name no cause.
This amendment supersedes the item 4 rule. With this amendment, the rule reads:
every `repo: local` hook stays in the gate configuration unless this table or a
later amendment gives it the placement CI.
For `harness-integrity`, the variable is `CI` and the value is `true`.
For `zizmor`, the variable is `GH_TOKEN` and any set value enables the online
audits.

### Gate invariant (extends item 2)

It extends Decision item 2.
Decision item 2 required `proxy.golang.org` and `sum.golang.org` to be
reachable.
No gate hook invokes Go, and the rehearsal requires that `go` does not resolve.
Therefore, `proxy.golang.org` and `sum.golang.org` are removed from the
reachable list.
The gate invariant keeps `pypi.org` and `files.pythonhosted.org`.
The invariant is narrower than the sandbox profile because the profile list is
permissive, whereas the gate invariant lists only endpoints that gate hooks
need.

The clone trace in Context under "The gate cannot work while it clones" names
`pre-commit/pre-commit-hooks`. PR #550 converted that repository to
`repo: local` entries (`.pre-commit-config.yaml:67-258`).
The trace records a historical observation.

The upstream facts are pinned to verified revisions:

- The registry profile is at `fullsend-ai/fullsend@v0.56.1` in
  `internal/scaffold/fullsend-repo/profiles/fullsend-package-registries.yaml:19-25`.
  The profile allows `pypi.org` and `files.pythonhosted.org`. Lines 7-43 also
  allow Go and npm registries.
- Upstream skill step 9b is defined in `fullsend-ai/agents@v0.56.1` at
  `skills/code-implementation/SKILL.md:596-827`. Line 658 executes
  `pre-commit run --files <all-your-changed-files>`.
- In `fullsend-ai/agents@v0.56.1` at `scripts/post-code.sh:1286-1292`, the
  runner installs `pre-commit==4.5.1` only when `pre-commit` is absent from
  PATH. When that install fails, lines 1294-1297 emit a warning and skip the
  authoritative check without failing the Fullsend pre-commit step.
- The Fullsend sandbox image at `fullsend-ai/fullsend@v0.56.1` in
  `images/sandbox/Containerfile:465-478` unconditionally installs
  `pre-commit==4.5.1` during image build.
- In Fullsend, `validation_loop.script` names the runner-side validation
  script. The script is the per-iteration success gate and runs after the agent
  exits. The pinned base harness `agents@v0.56.1` `harness/code.yaml:80-85`
  names `scripts/validate-code-output.sh`. That script checks the output schema
  and then runs the Fullsend pre-commit step. The local files
  `.fullsend/harness/code.yaml` and `.fullsend/harness/fix.yaml` do not set
  `validation_loop`. Both inherit it from the pinned base.

### Rehearsal (extends item 3)

It extends Decision item 3.
In Decision item 3, the five-cause rule supersedes the three-cause sentence:

- A hook stays in the gate configuration when it meets none of the five causes.
- A hook moves to the CI configuration only for one of five causes:
  1. It needs a VCS clone.
  2. It needs the Go toolchain.
  3. It needs a default-branch ref.
  4. It uses the network when an environment variable enables the network.
  5. It imports a module outside the Python standard library and does not
     declare the dependency.

A skipped hook is defined as a hook that reports `(no files to check)Skipped`,
or that reports a skip for any other reason.
At commit `4269c68`, no tracked `*.toml` or `*.xml` files exist.
The rehearsal fails on a skipped hook, except for `check-toml` and `check-xml`
when they report no matching files.
This exception list is closed.
A new hook requires a tracked matching file or an amendment.
The rehearsal does not rely on process exit codes for skips because
`pre-commit` exits 0 when hooks skip.
The job records each skipped hook with its skip reason.
The rehearsal fails unless every skipped hook is `check-toml` or `check-xml` and
reports `(no files to check)Skipped`.

Negative fixture rules apply exclusively to repository-owned checkers.
These checkers are hooks whose entry runs `scripts/check_*.py` or a
`scripts/test_*.py` module.
Each repository-owned checker that has a `test-*` hook has one negative
fixture.
The fixture must produce a nonzero exit and a named diagnostic.
The `test-*` hook that covers the checker must assert that diagnostic.
A hook that runs a repository-owned test module provides its own negative
fixture.
The verified checker table at commit `4269c68` is:

| Hook | Test hook | Negative test | Expected diagnostic |
| --- | --- | --- | --- |
| `adr-amendments` | `test-adr-amendments` | `scripts/test_check_adr_amendments.py:1071` | `rewritten or replaced in place` |
| `baseline-ratchet` | `test-baseline-ratchet` | `scripts/test_check_baseline_ratchet.py:57` | `ceiling for AGENTS.md increased` |
| `harness-integrity` (offline) | `test-harness-integrity` | `scripts/test_check_harness_integrity.py:154` | `missing top-level base entry` |
| `check-portable-null-device` | `test-check-portable-null-device` | `scripts/test_check_portable_null_device.py:279` | `os.DevNull` |
| `validator-spec-parity` | `test-validator-spec-parity` | `scripts/test_check_validator_spec_parity.py:557` | `sample.error` |
| `validator-fixtures` | `test-validator-fixtures` | `scripts/test_check_validator_fixtures.py:602` | `prohibited` |
| `spec-validator` | `test-spec-validator` | `scripts/test_check_spec_validator.py:102` | `spec validator failed` |
| `test-pre-commit-cache` | itself | `scripts/test_pre_commit_cache.py:294` | `cannot run the pinned upstream pre-script` |
| `test-select-comparison-base` | itself | `scripts/test_select_comparison_base.py:244` | `is not present in this checkout` |
| `test-lint-wrapper` | itself | `scripts/test_lint.py:3321` | `pre-commit install-hooks` |

`scripts/test_select_comparison_base.py:244` asserts only the `::error::`
prefix. Cutover step 12 extends that assertion to the message.

At commit `4269c68`, `eval-corpus-schema` has no test hook.
Issue #580 requires a negative fixture for `eval-corpus-schema` when it converts
the hook to `language: python`.
Until issue #580 lands, the gate configuration does not claim negative-fixture
coverage for `eval-corpus-schema`.
The `gofmt` and `go-vet` hooks run `scripts/check_gofmt.py` and
`scripts/check_govet.py`.
They have no `test-*` hook and they leave the gate configuration, so the
negative-fixture rule does not cover them.
A formatter that changes a file is a failure, not a skip.
The rehearsal runs on a clean checkout and fails when a hook modifies any file.
A formatter must not run on the working tree.
The clause "any skipped test" requires that every `test-*` hook matches at
least one tracked file.
A `test-*` hook that reports a skip fails the rehearsal.
A `test-*` hook passes only when its output reports no skipped test.
The job reads the summary line.
The unittest summary form is "OK (skipped=N)".
The pytest summary form is "N skipped".
The rehearsal fails when N is greater than zero.
For example, `scripts/test_check_spec_validator.py` skips its end-to-end tests
when `go` is absent (:81, :200-201), and that run does not count as a pass.

The rehearsal executes two runs on each runner label: one in the job
environment, and one under `env -i`.
Each run receives:

- `PATH` set to a job-created directory holding only commands the gate
  configuration needs: `git`, `python3`, `pre-commit`, and POSIX utilities.
- `HOME` set to a job-created empty directory.
- `PRE_COMMIT_HOME` set to a job-created empty directory. The job asserts that
  the directory exists and holds no entries. An unset variable is not evidence.
- `GIT_ALLOW_PROTOCOL=file`. This setting denies `https` and `ssh` transports
  independently of any probe.

Before each run, the job executes deny steps:

- Check out a detached HEAD.
- Remove `refs/heads/main` and `refs/remotes/origin/main`.
- Set `GIT_ALLOW_PROTOCOL=file`.

Before each run, the job executes probes with stated expectations:

- `git ls-remote https://github.com/pre-commit/pre-commit-hooks` fails with a
  transport-denied error. The `git ls-remote` probe does not distinguish VCS
  denial from an invalid repository. The deny rule establishes the condition.
  The probe only confirms that the deny rule is active.
- `git rev-parse --verify refs/heads/main` fails.
- `git rev-parse --verify refs/remotes/origin/main` fails.
- `go`, `gofmt`, `node`, `npm`, and `uvx` do not resolve under the restricted
  `PATH`.
- Positive control: `git clone file://<tmp>/seed` succeeds. This proves that
  local Git operates and denial covers only network transports.
- Positive control: `python3 -m pip download --no-deps --dest <tmp> iniconfig`
  succeeds. This proves that PyPI access operates.

The rehearsal does not probe `https://raw.githubusercontent.com` or
`https://api.github.com`.
The deny rule covers Git transports only, so an HTTPS request to either host
can succeed.
Neither host is part of the invariant.
A hook that needs either host has cause 4.
No container or firewall step is required.

After cutover, `scripts/lint.py` holds two named constants:

- `PRE_COMMIT_VERSION = "4.6.2"`, the wrapper and CI version.
- `FULLSEND_PRE_COMMIT_VERSION = "4.5.1"`, the version that the sandbox image
  ships and the runner post-script installs when no executable resolves.

The cutover adds `gate-rehearsal`, which installs
`FULLSEND_PRE_COMMIT_VERSION`.
`PRECOMMIT_VERSION` in `scripts/pre_commit_cache.py:76` is deleted when
issue #581 retires the bootstrap.
Until then, it must equal `FULLSEND_PRE_COMMIT_VERSION`.
The two pins are intentional.
The rehearsal tests the version that the Fullsend pre-commit step runs.
The wrapper tests the version that CI pins.

The rehearsal runs as a matrix job named `gate-rehearsal` with labels
`ubuntu-24.04` and `ubuntu-latest`.
`ubuntu-24.04` matches the Fullsend `runner_image` from
`.github/workflows/fullsend.yaml:73`.
`ubuntu-latest` matches the image that the CI `pre-commit` job uses
(`.github/workflows/ci-workflow.yaml:24`).
Each matrix job executes both run environments.
The cutover adds `gate-rehearsal` to the `needs` list
(`.github/workflows/ci-workflow.yaml:421-427`).
`scripts/test_lint.py:3747-3750` changes from forbidding every direct run to
permitting direct `pre-commit run` invocations exclusively in the
`gate-rehearsal` job.
The test names the job and exact commands.

### Base-aware validation (extends item 5)

It extends Decision item 5.

In Consequences, the AGENTS.md reconciliation subsection stated that only
`go vet` leaves the agent path. This amendment supersedes that sentence.
`AGENTS.md:11-12` requires both `gofmt` and `go vet` before committing.
The `gofmt` and `go-vet` hooks leave the agent path and move to the CI
configuration.

Decision item 5 stated that today's CI checks exclude `drafting-table`.
That statement is true of dedicated module jobs in
`.github/workflows/ci-workflow.yaml`.
However, all-files `gofmt` and `go-vet` hooks match `\.go$` files across the
repository (`.pre-commit-config.yaml:259-270`).
`scripts/check_govet.py:30-49` maps `drafting-table/guard.go` to its module
directory.
The cutover must preserve that all-files coverage.
Issue #560 owns standardizing Go module CI checks and adding a dedicated module
job for `drafting-table`.

Base-aware validation is resolved across CI jobs:

- The `validator` job already resolves an event-specific base and fails closed
  (`scripts/select_comparison_base.py:125-155`). It requires no change.
- The cutover ships base resolution for the `pre-commit` job.
  The job runs `scripts/select_comparison_base.py` with `DEFAULT_BRANCH`,
  `PULL_REQUEST_BASE_SHA`, `MERGE_GROUP_BASE_SHA`, and `PUSH_BEFORE_SHA`, and
  records `merge_base` and `exact_base`.
  It exports `GITHUB_BASE_SHA` from `merge_base` and `PROTOBOT_VALIDATOR_BASE`
  from `exact_base`.
  It exports `BASELINE_RATCHET_BASE_REF` and `PRE_COMMIT_FROM_REF` from
  `exact_base` when `exact_base` is set, and from `merge_base` otherwise.
  A pull request or merge group uses `merge_base`.
  A default-branch push uses `exact_base`.
  A push to another branch uses `merge_base` against the default branch.
  The job checks out enough history to contain the base commit.
- Issue #578 removes the `HEAD` fallback in `scripts/check_spec_validator.py`
  and makes a missing base fail. Until issue #578 lands, the cutover passes an
  explicit base, so the fallback is not used.
- On a pull request, the `pre-commit` job fails when it cannot resolve a base.
  On a push, `scripts/check_baseline_ratchet.py:641-643` may skip with exit
  code 0 on a shallow checkout. The job reports the skip and does not claim
  ratchet coverage. A skip is not a pass.

### Bootstrap (extends item 6)

It extends Decision item 6.

The pre-commit cache archive covers the gate configuration only
(`scripts/pre_commit_cache.py:611-614`).
It cannot supply the 3 remote repository revisions that the CI configuration
needs (`.pre-commit-config.yaml:31-52`).
An offline `scripts/lint.py` run covers the gate configuration only.
The CI configuration requires network access or its own warm cache.
After cutover, when `.github/pre-commit-ci.yaml` is absent on a branch, the
wrapper treats it as an empty configuration and skips it.
After cutover, a deletion-only change runs no hooks for deleted files.
The wrapper reports a zero-file run and exits 0.
After cutover, the wrapper executes `pre-commit validate-config` and
hook-environment preflight on each present configuration.
After cutover, the `--check-parity` flag validates each present configuration.
The cutover does not seed the CI configuration in the archive.
Issue #581 retires the pre-commit cache bootstrap.

### Accepted risk (extends Consequences)

It extends Consequences.

The accepted risk list in Consequences is extended to name every hook and mode
that leaves the gate configuration:

- `markdownlint-cli2`
- `hadolint`
- `renovate-config-validator`
- `gofmt`
- `go-vet`
- `spec-validator`
- `test-spec-validator`
- `adr-amendments`
- `baseline-ratchet`
- `harness-integrity` with `--online-if-ci`
- `zizmor`
- `eval-corpus-schema`
- `test-pre-commit-cache`
- `test-select-comparison-base`

The cutover does not keep `zizmor` in the gate configuration.
It does not keep an offline mode either.
While `scripts/lint.py:19-20` and `:1710` set `ZIZMOR_NO_ONLINE_AUDITS=true`
for offline runs, offline mode disables 5 online audits (`ref-confusion`,
`ref-version-mismatch`, `known-vulnerable-actions`, `impostor-commit`, and
`stale-action-refs`) and degrades 2 others (`artipacked` and `typosquat-uses`).
The gate configuration does not keep a weaker duplicate check.
The cutover exports `GH_TOKEN` to the `pre-commit` job.
With that token the online audits execute at merge time.
Without the token, 5 audits skip even in online mode.
For each excluded check, a defect can reach an open pull request.
The `ci-success` merge check blocks merge until defects are resolved.

### Cutover order (supersedes the Cutover order subsection)

It supersedes the Cutover order subsection in Consequences.
The cutover executes as one atomic change across 15 steps:

1. Create `.github/pre-commit-ci.yaml` with these 14 placements:
   `markdownlint-cli2`, `hadolint`, `renovate-config-validator`, `gofmt`,
   `go-vet`, `spec-validator`, `test-spec-validator`, `adr-amendments`,
   `baseline-ratchet`, the `--online-if-ci` mode of `harness-integrity`,
   `zizmor`, `eval-corpus-schema`, `test-pre-commit-cache`, and
   `test-select-comparison-base`. The 3 remote entries at
   `.pre-commit-config.yaml:31-52` move to the new file.
2. Remove the 13 placements that leave the gate configuration from
   `.pre-commit-config.yaml`. Remove the `--online-if-ci` mode from the
   `harness-integrity` entry. The offline mode of `harness-integrity` stays in
   the gate configuration.
3. In `.github/pre-commit-ci.yaml`, extend the `zizmor` `files:` regex to
   match `.github/pre-commit-ci.yaml`.
4. Teach `scripts/lint.py` to read both configuration files and run one
   preflight per configuration.
5. Add `actions/setup-go` to the `pre-commit` job in
   `.github/workflows/ci-workflow.yaml`, using the Go version from
   `validator/go.mod`, before the `scripts/lint.py` step.
6. Export `GH_TOKEN: ${{ github.token }}` to the `pre-commit` job before the
   `scripts/lint.py` step.
7. Install `pyyaml` and `jsonschema` for `python3` in the `pre-commit` job
   before the `scripts/lint.py` step. Pin both versions in
   `.github/workflows/ci-workflow.yaml`.
8. Add `fetch-depth: 0` to the `pre-commit` job checkout, or fetch the selected
   base commit.
9. Bind `DEFAULT_BRANCH`, `PULL_REQUEST_BASE_SHA`, `MERGE_GROUP_BASE_SHA`, and
   `PUSH_BEFORE_SHA` to `scripts/select_comparison_base.py`, and export
   `GITHUB_BASE_SHA`, `PROTOBOT_VALIDATOR_BASE`, `BASELINE_RATCHET_BASE_REF`,
   and `PRE_COMMIT_FROM_REF` as the Base-aware validation subsection states.
10. Update `scripts/test_lint.py:3747-3750` to permit direct `pre-commit run`
    invocations exclusively in the `gate-rehearsal` job.
11. Add the `gate-rehearsal` job to `.github/workflows/ci-workflow.yaml` and
    add it to the `ci-success` `needs` list at lines 421-427.
12. Extend the `test-select-comparison-base` negative fixture to assert the
    message `is not present in this checkout`
    (`scripts/select_comparison_base.py:141`).
13. Amend `AGENTS.md:3-12` to describe the gate configuration, point agents at
    direct `pre-commit run --files <changed>`, and send `gofmt` and `go-vet` to
    CI. Correct `.fullsend/README.md:11` (the no-egress claim) and
    `.fullsend/README.md:76-78` (the `scripts/lint.py` claim). Verify that the
    `CLAUDE.md` symlink remains intact.
14. Prove the rehearsal passes on the resulting tree across both runner labels.
15. Roll back the cutover commit as a whole on failure. A hook returns to the
    gate configuration only after it passes `gate-rehearsal`.

`CLAUDE.md` is a tracked symlink to `AGENTS.md`.
See the `CLAUDE.md` parity item in the terminology subsection.

The cutover implementation is tracked in issue #582.
The cutover depends on issue #579 for two configuration updates.
Issue #579 adds a `pre-commit` manager block for `.github/pre-commit-ci.yaml`
in `.github/renovate.json5`, joining the skillsaw manager at lines 23-25.
Issue #579 also hashes both configuration files in the CI cache key at
`.github/workflows/ci-workflow.yaml:54`.
Until issue #579 lands, Renovate does not validate `.github/pre-commit-ci.yaml`,
and the CI cache key hashes one file.

### Follow-ups (supersedes the Follow-ups table)

This subsection supersedes the Follow-ups table in Consequences with linked
issue tracking:

| Follow-up | Rationale | Owner surface | Issue |
| --- | --- | --- | --- |
| Base-aware CI validator | Resolves an explicit default-branch ref and fails when it cannot, so deletions are checked. | CI workflow, scripts | #578 |
| Renovate and CI cache keys | Add a `pre-commit` manager block for `.github/pre-commit-ci.yaml` (joining the skillsaw manager at `.github/renovate.json5:23-25`), and hash both configurations for the CI cache key. | `.github/` | #579 |
| Convert the three `language: system` hooks | Give `eval-corpus-schema`, `test-pre-commit-cache`, and `test-select-comparison-base` `language: python` with pinned `pyyaml` and `jsonschema`, then return them to the gate configuration. | `.pre-commit-config.yaml` | #580 |
| Retire the cache bootstrap | The gate no longer depends on the archive, and removing it deletes the runner warm step and the restore path. | `.fullsend/`, `scripts/` | #581 |
| Standardize Go module CI checks | Add dedicated module CI jobs for `drafting-table` and unify module checks. | CI workflow | #560 |
| Implement the cutover | Implement the atomic 15-step cutover order. | Repository | #582 |

Issues #442, #512, and #520 in Related Documents record Fullsend runs in which
the Fullsend pre-commit step failed (see the Observed failures table in
Context).
Related Documents: [PR #532](https://github.com/redhat-et/ProtoBot/pull/532)
introduced the cache bootstrap described in Context. This amendment records
the link here because the ADR-immutability checker permits insertions only
inside a dated amendment block.

### Terminology, scope, and prose corrections

This subsection clarifies terminology, scope, and prose across Context,
Decision item 1, and Consequences.
It extends Decision item 1 and Consequences:

- Three distinct terms name three distinct concepts:
  1. "gate configuration" refers to `.pre-commit-config.yaml`.
  2. "Fullsend pre-commit step" refers to the runner step executing
     `pre-commit run --files`.
  3. "`ci-success` merge check" refers to the GitHub Actions merge gate defined
     in `.github/workflows/ci-workflow.yaml:417-440`.
- Scope boundary: This ADR governs repository process for the ProtoBot
  repository. It does not govern the product
  [Job Site Sandbox Contract](../architecture/jobsite-sandbox.md).
- Execution environments: The 4 execution environments referenced in Decision
  item 1 are the contributor sandbox, the Fullsend runner gate, CI, and the
  developer workstation.
- Forced path: The forced path is the direct
  `pre-commit run --files <changed>` invocation that upstream skill step 9b
  runs without `scripts/lint.py`.
- Punctuation and spelling: Procedural sentences contain no semicolons. The
  amendment uses US spelling, including "optimization". Accepted text keeps its
  original spelling because accepted text is immutable.
- `CLAUDE.md` parity: `CLAUDE.md` is a tracked symlink to `AGENTS.md` with mode
  120000. Changes to `AGENTS.md` automatically apply to `CLAUDE.md`. Cutover
  step 13 verifies that the symlink remains intact.
- IdeaBot reference: This amendment updates the bare reference at
  `docs/architecture/related-work.md:111` to `IdeaBot ADR-0005/0006`. This
  distinguishes IdeaBot architecture records from ProtoBot ADR-0005.

**[Amended November 2026, #582]**

The cutover rehearsal proved two hook placements that the #585 placement table
does not name:

- `test-adr-amendments` moves to CI under Cause 3. It tests
  `scripts/check_adr_amendments.py`, which resolves and fetches a
  default-branch ref from `origin`. In the gate, with VCS denied and the
  default-branch refs removed, it fails with
  `ERROR: cannot resolve default-branch baseline ref to check against`.
  A named job in `ci-success` (the `pre-commit` job) keeps the equivalent
  check.
- `test-check-gate-invariant` runs in CI under Cause 5. It imports PyYAML, a
  module outside the Python standard library, without declaring it. It tests
  `scripts/check_gate_invariant.py`, which enforces this decision.

The gate configuration keeps 30 local hooks: 20 `language: python`, 1
`language: pygrep`, and 9 `language: system`. This supersedes the 31-hook count
in the #585 placement rule, because `test-adr-amendments` moves to CI.

`check-toml` and `check-xml` remain in the gate configuration under the #585
closed skip exception. The rehearsal records each skipped hook and allows only
their `(no files to check)Skipped` result.

The following placement table records these two hooks:

| Hook | Placement | Cause |
| --- | --- | --- |
| `test-adr-amendments` | CI | Cause 3: Needs a default-branch ref |
| `test-check-gate-invariant` | CI | Cause 5: Imports a module outside the Python standard library and does not declare the dependency |

This amendment references `.pre-commit-config.yaml`,
`.github/pre-commit-ci.yaml`, `scripts/check_gate_invariant.py`, and
`.github/workflows/ci-workflow.yaml`.

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
