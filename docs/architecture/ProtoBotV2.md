# ProtoBot V2: A Minimal Specification-Driven Factory

> **Status:** Proposal · November 2026
>
> V2 keeps V1's thesis — prose becomes EARS requirements, and EARS
> requirements are the authoritative artifact from which code is derived —
> and deletes the platform built around it. The EARS statement is the full
> obligation; ordinary tests are derived evidence. Implementation converges
> on the current approved specification; it is not organized into per-change
> delivery units. This document describes the proposed system. It is not yet
> implemented.
>
> **Publication scope:** Nonadopted clean-slate V2 discussion proposal.
> Existing V1 specifications and accepted ADRs remain the current contracts.
> Merging this document records the proposal; it does not adopt V2, supersede
> V1, or authorize changes to current tooling, permissions, or workflow.
> Prescriptive statements below describe the proposed V2 system only.
> Repository paths, including the proposed layout, are repository-root-relative,
> not relative to this document's location under `docs/architecture/`.

**Contents:**

- [Summary](#summary)
- [Why V2](#why-v2)
- [Goals and non-goals](#goals-and-non-goals)
- [Core principles](#core-principles)
- [Repository layout](#repository-layout)
- [Artifacts](#artifacts)
- [The flow](#the-flow)
- [Demonstration and handoff](#demonstration-and-handoff)
- [Components](#components)
- [Invariants and enforcement](#invariants-and-enforcement)
- [Regeneration and delta experiment](#regeneration-and-delta-experiment)
- [What V2 drops from V1](#what-v2-drops-from-v1)
- [Template and instantiation](#template-and-instantiation)
- [Operating model](#operating-model)
- [Adoption path](#adoption-path)
- [Deferred items](#deferred-items)
- [Open questions](#open-questions)
- [Relationship to V1](#relationship-to-v1)
- [Appendix A: Path taxonomy](#appendix-a-path-taxonomy)
- [Appendix B: Reference checks](#appendix-b-reference-checks)
- [Appendix C: Coordinator execution contract](#appendix-c-coordinator-execution-contract)
- [Related Documents](#related-documents)

## Summary

ProtoBot V2 is a small, Git-native workflow for turning prose into
specifications, and specifications into code. It has three pieces:

1. **Authoritative artifacts** — `Vision.md`, `Architecture.md`, EARS
   requirement records, and interface records, all stored as plain files in
   the repository.
2. **Validation and evidence** — a validator and CI checks that validate
   specification structure, immutable requirement text, references, and
   ordinary test evidence.
3. **Coordination and agents** — a repository-owned GitHub Actions caller and
   local Fullsend coordinator harness that reconcile current evidence into
   managed implementation issues and admit scoped parallel Fullsend coders; a
   drafting-table agent; and Fullsend's coder and reviewer. GitHub Issues
   carry coordination and ownership only.

Everything else is Git, GitHub Issues, Actions, branch protection, and
Fullsend. There is no separate work-item service, no lifecycle state machine,
no lease manager, no database, and no per-change manifest. The current
approved specification is the target. Implementation moves toward it in
whatever increments a coder can manage within its assigned work.

## Why V2

In V1, ProtoBot accumulated an elaborate control plane:

- a work-management store (WMS) to track work-item lifecycles across external
  issue trackers, guarded by contract versions and fencing tokens (monotonic
  counters used to reject stale writes from superseded worker leases);
- a Job Site execution service that orchestrated isolated worker agent
  sandboxes;
- a source-control manager (SCM) dedicated to mediating Git and Git-host
  operations;
- backend translators;
- a projection access-control layer (projection ACLs) that filtered repository
  views according to agent role;
- an evaluation corpus for benchmarking agent performance; and
- an impact-analysis algorithm to assess downstream effects of changes.

Each piece was defensible in isolation. Together, they serialized work, forced
every change through complex lifecycle machinery, dominated throughput with
review and rebase churn, and made the system difficult to understand.

V2 asserts that Git already provides the durable primitives that V1 rebuilt:
immutable history, branches, merge approval, access control, and an audit
trail. V2 keeps specification validation, traceability, and agent handoff.

V2 replaces the external WMS with ordinary GitHub Issues for coordination. It
uses a single thin, repository-owned Actions caller and local coordinator
scripts within existing Fullsend execution for serialized reconciliation and
admission. GitHub Issues carry assignment and ownership, not specification
authority.

The notion of a persistent change set is gone. In V2, a specification pull
request encompasses a proposed change to the specifications, visible as a diff.
That diff has no meaning once the pull request merges; it simply becomes part
of the system's specifications. Git history and merged specification pull
requests are historical artifacts.

What matters in the repository is the entire specification, as it currently
exists on the default branch (`main`). The specification on `main` is complete
and current.

Specification changes do not correspond one-to-one with a delivery unit or
implementation pull request. The implementation converges to satisfy the
specification over one or more implementation pull requests. Git history, the
specification diff, and derived test evidence provide provenance and
demonstrate progress toward full convergence.

## Goals and non-goals

### Goals

- Turn prose issues into precise, reviewable EARS requirements.
- Make the specification, not the code, the artifact humans maintain.
- Keep code in principle regenerable from the specification.
- Make every active requirement traceable to ordinary test evidence or an
  explicit untested gap.
- Make progress visible through current test evidence, with issue state
  coordinating but not proving it.
- Coordinate implementation work through ordinary GitHub Issues and Fullsend,
  without a custom coordination service.
- Run locally for one person, and scale to a team through Git and GitHub
  coordination.
- Be small enough that one person can understand the whole system in a day.

### Non-goals

- A custom work-item or runtime orchestration service. Ordinary GitHub Issues,
  Actions, and Fullsend dispatch are in scope.
- A general-purpose lifecycle state machine.
- Multi-tracker abstraction (GitHub is the first and only backend).
- Per-change delivery artifacts, manifests, or one-issue-per-delivery
  bookkeeping.
- Treating a managed issue or green CI run as proof of full convergence.
- Enforcing behavioral obligations beyond what Git and CI can check;
  coordination labels are not behavioral evidence.
- Guaranteeing that regeneration produces identical code. Regeneration must
  reproduce _behavior_ and constraints, not incidental implementation.

## Core principles

### The EARS statement is the full obligation

A human reviews and approves requirement text. The EARS statement is the
complete normative specification of the obligation: behavior or explicit
implementation constraint. Every condition, threshold, input, and observable
outcome belongs in that text.

`Vision.md`, `Architecture.md`, interface descriptions, metadata, and comments
organize, reference, or explain requirements. They cannot impose an unstated
condition or alter requirement meaning. If domain applicability truly
constrains the obligation, the requirement text says so. Only the requirement
text is normative; no other artifact adds a condition or outcome.

Requirement IDs, EARS type, `applies_to`, relationships, and status are
indexing and lifecycle metadata, not an alternate normative specification.

Tests, test inputs, drivers, helpers, and assertions are derived
interpretations of requirement text. They are reviewed against the text, not
treated as its source.

### Requirement text is immutable; replacement gets a new ID

An approved requirement's parsed `text` value is immutable. Reformatting YAML
is allowed only when the parsed string is unchanged. Any text change —
including wording-only edits — under an existing ID is rejected. There is no
semantic-edit exception.

To replace an obligation, add a new requirement ID whose `text` states the new
obligation. Add `supersedes` from the new record to the old ID, and retire the
old record in the same specification PR. The old record remains in history. IDs
are never deleted or reused for a different obligation. A new requirement with
no predecessor simply gets a new ID.

Amendment is disallowed for convergence detection. In-place amendment would
leave the old tests and implementation unchanged and passing. Nothing would
surface that the system no longer converges on the amended obligation.
Replacement instead produces two mechanically findable states at the same
time: tests tagged to a retired ID, and an active requirement with no tests.
Because those states appear together, useful tests can be edited slightly
and re-tagged.

Descriptive metadata or `status` may change without a new ID, but those changes
cannot amend the obligation. Retirement does not require deleting behavior
unless an active requirement forbids it. IDs are unique and never allocated
from a shared counter; sequential allocation races under concurrency.

### The current approved specification is the target

The complete specification on the default branch is the authority. Three
things are distinct:

- the **approved target specification** — active requirement text,
  interfaces, and narratives;
- the **derived evidence** — ordinary tests associated with requirement IDs
  and their results at an exact commit;
- the **demonstrated implementation** — the active requirements whose
  implementation and passing test evidence an independent reviewer judges
  adequate against the requirement text.

Every active requirement is continuously in force. Ordinary CI runs the
existing suite. Failing tests block implementation. A new active requirement
with no tests is an allowed specification-to-code gap and creates work; the
whole program need not be complete before an incremental code PR merges.

Mechanical coverage records associated collected tests and their results.
Positive coverage requires at least one associated test to execute and pass.
Complete passing results require every associated test to execute and pass with
no ignored failure or skip. Neither alone establishes adequate evidence of the
full obligation: the independent reviewer judges adequacy against the
requirement text. Missing tests are an untested gap. Skipped, unexecuted,
collection-error, missing, or incomplete evidence cannot count as satisfaction.

### Convergence, not delivery units

Writing code is iterative convergence on a program that meets the current
specification. A source issue, managed issue, or evidence gap provides
prioritization or coordination context, not behavior authority. There is no
per-change manifest, one-issue delivery rule, or persistent planning gate. A
coder may keep a transient plan while working; it is not committed or checked.

Progress is evidenced by current tests and review of what they adequately
demonstrate against requirement text. Green results alone do not certify
convergence.

### Merge is approval

There is no separate approval state. A specification change is approved when
its pull request merges to the default branch. A pull request's status is
derived from Git; it is never stored as a lifecycle field.

### Specification, evidence, and coordination have separate authority

Specification text in Git determines obligations. The evidence report for an
exact main SHA exposes mechanical coverage and result gaps. Independent review
judges whether the evidence adequately demonstrates behavior. GitHub Issues
hold assignments, requirement scope, dependencies, runner and pull-request
links, and progress. Issues are authoritative for coordination only; they
never add or override a requirement.

CI behavioral and structural checks remain tracker-free. The coordinator
intentionally reads GitHub. There is no separate lifecycle service: ordinary
issue labels and open/closed state, one natively serialized Actions caller, and
a local coordinator harness in Fullsend are the coordination substrate.
Serialization covers coordination, not the parallel coder runs.

## Repository layout

```text
Vision.md                       # authoritative: purpose, users, outcomes
Architecture.md                 # authoritative: components, interfaces; organizes constraint references
spec/
  requirements/
    REQ-<SCOPE>-<NNNNN>.yaml    # authoritative: one file per requirement
  interfaces/
    <interface-name>.yaml       # authoritative: named external interfaces
src/                            # derived: implementation
tests/                          # derived: ordinary tests, helpers, drivers
ci/                             # process: task definitions for validation and checks
.github/workflows/              # process: thin GitHub-event workflows that delegate to ci/ task definitions
.fullsend/                      # process: coordinator harness, agent, pre/post scripts
AGENTS.md                       # process: repository and agent instructions
.agents/                        # process: agent definitions and skills
```

The layout is intentional: `Vision.md`, `Architecture.md`, and `spec/` are the
specification side; `src/` and `tests/` are the implementation side; `ci/`,
`.github/workflows/`, `.fullsend/`, `AGENTS.md`, and `.agents/` are process.
Tests are ordinary derived implementation code, tagged with requirement IDs
where they demonstrate a requirement.

Managed implementation issues live in the tracker. They are authoritative for
coordination only, never for specification content.

## Artifacts

### Requirement record

```yaml
id: REQ-API-00002
type: event-driven
text: >-
  When an unauthenticated client sends GET /health, the service shall return
  HTTP 200 with a JSON body whose status field is "ok".
applies_to:
  interfaces:
    - health-api
  scopes:
    - api
relationships:
  - type: related-to
    target: REQ-API-00001
status: active
```

| Field | Meaning |
| --- | --- |
| `id` | Stable, unique, immutable. Format `REQ-<SCOPE>-<NNNNN>`. |
| `type` | EARS pattern: `ubiquitous`, `event-driven`, `state-driven`, `unwanted-behavior`, `optional-feature`, `complex`. |
| `text` | The complete EARS obligation. Exactly one `shall`. |
| `applies_to` | At least one interface or scope. Applicability metadata, not a second obligation. |
| `relationships` | `depends-on`, `supersedes`, `related-to` only. |
| `status` | `active` or `retired`. Record lifecycle only, not delivery state. |

There is no implemented flag. Whether a requirement is demonstrated is
judged from ordinary test evidence and independent review; delivery status
is not stored on the record.

A specification PR must not merge conflicting active requirements. Resolve
the conflict before merge, using new text under a new ID and retirement of
conflicting predecessors where appropriate. The validator structurally
rejects `conflicts-with` and other unsupported relationship types. Human and
agent semantic review separately detect and resolve conflicting obligations.
Omitting a conflict relationship does not make conflicting text acceptable.

### Interface record

An interface record has a stable name, a kind such as `http`, and a
description explaining the boundary. Interfaces are named so that
`applies_to.interfaces` resolves. The description does not add conditions or
outcomes absent from requirement text.

### Replacement, retirement, and test tags

Every text edit creates a new requirement ID. The new record supersedes the
old ID, and the old record is retired in the same specification PR. A
replacement changes the obligation; old tests do not count toward the new
ID.

```yaml
# retained old record
id: REQ-API-00002
type: event-driven
text: >-
  When an unauthenticated client sends GET /health, the service shall return
  HTTP 200 with a JSON body whose status field is "ok".
applies_to:
  interfaces:
    - health-api
  scopes:
    - api
relationships: []
status: retired
# new record
id: REQ-API-00003
type: event-driven
text: >-
  When an unauthenticated client sends GET /health, the service shall return
  HTTP 200 with a JSON body whose status field is "healthy".
applies_to:
  interfaces:
    - health-api
  scopes:
    - api
relationships:
  - type: supersedes
    target: REQ-API-00002
status: active
```

Tests may keep a retired ID tag and remain runnable historical evidence, but
they provide no coverage of the new or any current ID. A specification-only
replacement or retirement may merge without changing tests. Retired-ID tags
remain valid historical references, and the code or test PR adds current
coverage. A code or test PR adds the new association or replaces the obsolete
test when the implementation changes. An implementation PR may also modify or
remove tests tagged to a retired ID. Their associated specification no longer
applies. Illustrative runner notation, not a selected framework:

```text
# old: @req:REQ-API-00002
# new: @req:REQ-API-00003
```

A shared test may carry several requirement IDs when justified. When a
requirement retires, the implementation PR must not leave a retired tag as
the only apparent association while an active requirement loses coverage
unnoticed.

### Tests and requirement evidence report

Tests live under implementation paths such as `tests/` and follow project
conventions. They are ordinary derived tests, not a separate normative
artifact. Tests that demonstrate a requirement carry a direct
`@req:<ID>` association. Other unit or internal tests may be untagged.
Drivers, helpers, fixtures, and other test support code are derived
implementation too.

The **evidence report** is minimal script-generated CI output. For each active
requirement ID, it lists associated collected tests and their results at the
exact commit SHA. Coder, reviewer, and coordinator agent use it to detect gaps
deterministically. It is regenerated from the checkout and test run. It is not
committed, not an additional authority or ledger, and not a semantic
completeness proof.

- Positive coverage requires at least one associated test that executes
  and passes.
- Complete passing results require every associated test to execute and pass,
  with no ignored failure or skip. This is mechanical coverage, not a judgment
  that the full obligation is adequately demonstrated.
- Missing tests are an untested implementation gap.
- A failing test is a failure.
- Skipped, unexecuted, collection-error, missing, or incomplete evidence cannot
  count as satisfaction.
- Tests tagged only to retired IDs remain runnable historical evidence but
  do not satisfy an active ID.
- Unknown requirement IDs in test tags fail reference validation.

Passing a finite suite does not mechanically prove that the EARS statement is
fully covered. The independent reviewer judges test adequacy from the
requirement text first. The tag records association; it does not certify
fidelity.

### Source provenance

A specification pull request description records intent, source issue links,
and a source-ask checklist. Each ask maps to a new or existing active
requirement or an explicit human-approved exclusion; unrecorded existing
behavior is not authority. The editable checklist is a planning hint, never
proof of approval or completion. Exclusions link to an explicit GitHub human
review or comment identifying the exact ask and exclusion. Trusted preflight
verifies its content, actual human author, and write, maintain, or admin
permission via the GitHub API, not an author's claim or quoted approval. Git
diff and history show the delta. There is no per-change artifact; a managed
issue or source link is coordination context, not behavior authority.

A new project begins from IdeaBot, the idea-capture and research tool that
precedes ProtoBot in the Hermes pipeline. The initial specification PR of a
new project extends this provenance. The IdeaBot artifacts — the markdown
research files and the Jira-based discussion — are copied manually into the
Drafting Table session, not committed to the repository. No Jira
integration or new service is introduced. The initial specification PR
links the IdeaBot artifacts where they are hosted and records a short note
mapping key constraints and assumptions from that research to where they
ended up. The note states which became requirements, which assumptions
still need validation, and which items were explicitly excluded. The
purpose is to preserve incoming intent for review of the large first PR.
EARS requirement text is the only behavior authority. Intent left only in
the research would otherwise disappear from implementation. Only the
mapping note and the links enter the repository.

### Managed implementation issue

A managed implementation issue is the coordination record for a bounded
assignment. Its scope is requirement IDs, not test IDs. It may cover gaps
from several specification PRs, and it may require several implementation
PRs. One specification PR may produce several managed issues.

| Field | Meaning |
| --- | --- |
| Intent | One human-readable outcome. |
| Requirements | Machine-readable requirement IDs in scope. |
| Dependencies | Blockers and other issues that must finish first. |
| Active assignment | An `assignment-active` label holds the issue's requirement scope while work is live or dispatch is ambiguous. |
| Evidence links | Relevant spec PR or revision, main report SHA, runs, PRs. |
| Coordination label | `queued`, `ready-to-code`, `blocked`, `needs-replan`, or `superseded`. |

Requirement IDs are coordination and ownership metadata, not behavior
authority. Tests are not allocated separately: a test may demonstrate several
requirements, and shared test or source files are implementation coupling
handled by review and CI. Historical assignment or report revision is context
only; a replacement requirement does not pin old behavior.

## The flow

```text
prose issue / user goal
    │
    │  human and drafting-table agent work interactively
    ▼
specification pull request
    │  requirements, interfaces, narratives; optional process changes
    │  no implementation; source-ask checklist; relevant CI; human review
    ▼
default branch (approved current specification)
    │
    │  main full suite produces the evidence report for the exact SHA
    ▼
trusted main evidence workflow completes (including failing tests)
    │  workflow_run plus coder-run, unmerged-close, and live-assignment events
    │  workflow_dispatch on main remains the fallback
    ▼
repository-owned caller (fixed non-cancelling concurrency group)
    │  calls existing pinned upstream Fullsend reusable dispatch
    ▼
local coordinator harness (existing retro identity, mint and Vertex inference)
    │  trusted pre: latest main + exact report, issues, runs and PRs
    │  snapshot-only agent; trusted post validates and applies
    ▼
managed implementation issues
    │  queued/blocked assignments with requirement-ID scopes
    ▼
admission checks dependencies/overlap; marks assignment-active
    │
    ▼
App-token ready-to-code ──▶ Fullsend coder (parallel disjoint assignments)
    │  coder works within its assigned requirement scope
    ▼
implementation PR(s): source and ordinary tests, non-closing references
    │  review; current-candidate full suite; evidence report
    ▼
merge ──▶ main evidence workflow ──▶ same coordinator
    │  completion only from merged-main evidence
    │  source issue auto-closes only after all approved asks are verified
    │  on current main: adequate implementation or human-approved exclusion
    ▼
implementation complete
```

Implementation completion is not demonstration readiness; see
[Demonstration and handoff](#demonstration-and-handoff) for what follows.

### Step by step

1. **Prose issue or goal.** A desired outcome; a prioritization hint, not an
   authoritative scope.
2. **Drafting.** The agent elicits complete EARS statements, constraints, and
   interfaces, and records a source-ask checklist mapping each ask to a new
   or existing active requirement or an explicit human-approved exclusion. It
   writes no implementation. For a new project, the initial Drafting Table
   session (V1's Sketching session) takes the IdeaBot artifacts as its input
   basis for `Vision.md`, `Architecture.md`, and the initial requirements.
   They are copied manually into the session, not committed to the
   repository.
3. **Specification PR and main evidence.** Specification-profile CI and human
   review; optional process changes also run relevant process checks. No
   implementation paths or premature source-issue closing directives. Merge is
   approval. Main runs the full ordinary suite for the exact SHA.
4. **Reconciliation.** Completion of the trusted main evidence workflow
   triggers the repository-owned caller, even if tests failed. Coder-run
   completion or failure, unmerged close of a linked pull request, and state
   changes on issues or pull requests relevant to live assignments also
   trigger it. Its local coordinator harness runs inside existing trusted
   Fullsend execution, validates the latest-main report, groups or splits
   requirement-ID work, records dependencies and evidence links, preserves
   active assignments, and reports unassigned gaps. Missing or incomplete
   evidence cannot support closure or evidence-driven admission. Manual
   `workflow_dispatch` on main remains the fallback for ambiguous dispatch
   and anything the event set does not cover.
5. **Admission and dispatch.** Serialized admission checks dependencies and
   live work, marks the issue's scope `assignment-active`, then dispatches
   through an App-token `ready-to-code` label. Fullsend coders run in parallel
   on disjoint assignments; they do not self-select the backlog.
6. **Implementation.** The coder works within its assigned requirement scope,
   writes code and ordinary tests, and opens a PR against the latest merge
   candidate.
7. **Completion.** A merge produces main evidence and triggers the coordinator.
   Issues close only when every still-active scoped requirement has adequate
   tests that execute and pass in merged-main evidence. The system also
   auto-closes a source issue once agent judgment and mechanical results verify
   every approved ask is adequately implemented on current main or explicitly
   excluded with human approval. Ambiguous or missing mappings keep it open for
   reassessment; retirement alone is not delivery. A failed run or unmerged PR
   close with no new main evidence triggers the caller to reassess existing
   evidence and live work.

**Defects.** A defect is either a gap in what the tests demonstrate or a code
bug in behavior the specification already covers. A gap enters the
specification flow: a human-reviewed PR adds, replaces, or retires
requirements. A bug already covered by adequate requirements and tests may take
the code-only maintenance path ([Coder](#coder)) without a specification change.

## Demonstration and handoff

Implementation completion and demonstration readiness are distinct. Managed
issues close on adequate merged-main evidence for their scoped requirements.
A closed managed issue or a green CI run is not proof of full convergence
([Goals and non-goals](#goals-and-non-goals)). Meeting all active
requirements likewise does not establish that a person can run the intended
end-to-end demonstration.

V2 replaces V1's demonstration-artifact manifest under
`.protobot/attestations/demos/` with the demonstration-readiness record and
the tag-and-release handoff boundary.

### Demonstration readiness

V2 defines a lightweight demonstration-readiness checkpoint. The checkpoint
is an end-to-end acceptance test, sufficient for the V2 MVP. It records
three things:

- a reproducible procedure: setup, commands with sample data, and expected
  results;
- the demonstrated revision, recorded as the exact SHA; and
- the known limitations of the demonstration.

For a CLI prototype, a person follows the setup, runs the commands with the
sample data, and sees the expected result.

Stakeholder feedback that changes the desired behavior returns through the
[Drafting Table](#drafting-table) as a source issue. It is not a code
defect. This checkpoint does not change automatic closure of managed
implementation issues.

### TransferBot handoff

TransferBot follows ProtoBot in the Hermes pipeline. It transfers the
prototype's specifications and test expectations, not its code, to a product
team. When a prototype is selected for that handoff, the handoff boundary is
a Git tag on the demonstrated revision
([Demonstration readiness](#demonstration-readiness)), published as a GitHub
release.

The release retains:

- the specification revision;
- the interface definitions;
- the evidence report for the exact tagged commit ([Tests and requirement
  evidence report](#tests-and-requirement-evidence-report));
- the independent review conclusions; and
- links to the known gaps.

The attached evidence report must match the tagged commit exactly. This
shows TransferBot exactly what was demonstrated. A release-triggered job can
attach the exact-SHA report automatically.

CI artifacts expire. Prototype code is disposable, and current main moves
past the demonstrated revision. A link to current main or to an expiring CI
artifact would not suffice. The release is a durable, revision-specific
boundary. It introduces no per-change manifest and no new service.

## Components

### Drafting Table

The interactive specification editor. V2 keeps the V1 experience —
elicitation, requirement authoring, interface selection, and explicit
approval — and drops the external WMS backend. The Drafting Table writes files
and opens a pull request. It has no lifecycle state to manage because merge is
approval.

### Validator

Rework `ears-manager` into the V2 validator by retaining useful validation code
and replacing its V1 contract. Its record parsing, EARS shape checks, ID and
reference checks, relationship validation, and deterministic diagnostics are
useful foundations for a small local-and-CI tool. The existing `ears-manager`
does not implement V2 as-is; preserving its name, CLI, or schema compatibility
is not a constraint, and no competing validator is required.

The validator reads the plain `spec/` layout and reduced records in
[Artifacts](#artifacts) and checks:

- record schema and required fields;
- EARS shape: `text` matches the EARS pattern of its declared `type`
  (`ubiquitous`, `event-driven`, `state-driven`, `unwanted-behavior`,
  `optional-feature`, `complex`) and contains exactly one `shall`; a
  statement that does not match its declared pattern fails validation;
- identifier format, uniqueness, no deletion, and no reuse;
- referential integrity: `applies_to.interfaces`, relationship targets, and
  test `@req:<ID>` tags all resolve;
- relationship types restricted to `depends-on`, `supersedes`, and
  `related-to`; `conflicts-with` is structurally rejected;
- `depends-on` and `supersedes` graphs are acyclic;
- requirement `text` immutability against the default base for existing IDs;
- replacement and retirement rules: every replacement has a new ID,
  `supersedes`, and a retired predecessor in the same specification change;
- test tags may reference retired IDs, but unknown IDs fail.

It does not check tracker issues or issue-closed state. It takes the checkout
and comparison base as arguments. It is deterministic, network-free, reads only
the checkout, and never reads the tracker. The CI evidence script separately
collects tests and results. Neither it nor the structural validator judges
semantic consistency or test adequacy; those are agent and human review
responsibilities.

The EARS shape check is deliberately narrow. The validator does not judge
vagueness or requirement quality. V2 rejects banned-word lists as a false
sense of security. Requirement quality relies on the
`eliciting-requirements` skill and Drafting Table review. Evaluations of
that skill should treat vagueness as an explicit target.

Structural relationship validation does not detect semantic conflicts. Human
and agent review must resolve conflicting active requirements before a
specification PR merges ([Requirement record](#requirement-record)).

Useful starting points are `ears-manager/internal/specvalidation/validator.go`,
`ears-manager/internal/specvalidation/relationships.go`, and
`ears-manager/internal/records/records.go`. V2 replaces V1 metadata and
storage assumptions: no verification, provenance, or creation metadata
requirements, project registry or digests, change sets, or impact, projection,
or lifecycle machinery. It also removes `conflicts-with`. Add parsed-text
comparison against the base, no deletion or reuse, new-ID, `supersedes`, and
retirement enforcement, and test-tag references. Enforce exactly one `shall`
explicitly; the existing V1 EARS regex checks are not sufficient for that
rule. Keep rejecting `depends-on` and `supersedes` cycles; V1's
`validateAcyclicGraph` in `relationships.go` already implements that check.

### Specification PR check

The split rule is an always-running gate over one shared taxonomy
([Appendix A: Path taxonomy](#appendix-a-path-taxonomy)).

1. A pull request must not contain both Specification-class and
   Implementation-class paths. Such a mixed diff fails before any profile is
   selected. Process-class paths may accompany either class.
2. A specification pull request may change Specification-class paths and
   optional Process-class paths, and requires human review. Relevant process
   checks also run; the product suite is not mandatory unless a process check
   needs it.
3. A pull request with no Specification-class paths uses the implementation
   profile, including process-only changes. Process paths require human
   approval and relevant process checks whichever profile is selected.

Tests are implementation. They are not human-frozen. The protected boundary is
requirement text; the reviewer independently evaluates whether generated code
and tests are adequate for that text.

The specification PR description records intent, source issue links, and the
source-ask checklist from [Source provenance](#source-provenance). Git diff
and history show the delta.

Semantic review is separate from these structural rules. Git detects textual
conflicts, not a semantic conflict between different requirement records or
between a new requirement and existing behavior. The author and reviewer account
for every source ask and review consistency against the latest target
specification. Conflicting active requirements must be resolved before merge,
with new text, new IDs, and retirement where appropriate; they cannot be
retained as `conflicts-with` relationships. This is human and agent judgment:
deterministic checks can reject unsupported relationship types but cannot
guarantee that semantic conflicts are caught. When the base changes relevant
context before merge, the author updates the change and the reviewer
re-reviews it.

Replacement and retirement carry an ownership duty. When a specification PR
replaces or retires a requirement, the change's author — the Drafting Table
session or the human author — must review every active requirement connected
to the retired record. The review covers the requirements in the retired
record's `relationships` list and the other active requirements that apply to
its interfaces. The author must adjust each connected requirement or
explicitly leave it unchanged; the human reviewer decides each case. A
replacement that leaves a contradicting neighbor unexamined does not satisfy
semantic review.

### Repository-owned coordinator and Fullsend integration

Authenticated coordinator work runs inside the **existing trusted Fullsend
reusable workflow**, using its existing `retro` identity, hosted mint, and
Vertex inference. A thin repository-owned Actions caller handles triggers and
serialization. **New ProtoBot-local code** supplies the standalone coordinator
harness and pre/post scripts; Fullsend does not already implement this
reconciliation. No changes to mint configuration, hosting, roles, App
permissions, or Fullsend version are needed. The inference credential
boundary is selected and verified at adoption
([Appendix C: Coordinator execution
contract](#appendix-c-coordinator-execution-contract)).

| Piece | Responsibility |
| --- | --- |
| New native caller | Validate trigger and coordinator status issue; call pinned upstream dispatch; serialize the entire invocation. No App tokens. |
| Existing Fullsend runner | Mint existing role credentials, run pre/agent/post, provide existing pi/Vertex inference and sandbox lifecycle. |
| New trusted pre-script | Verify current main and evidence; read GitHub state into a credential-free snapshot. |
| New coordinator agent | Snapshot-only, read-only planning: propose grouping, admission, and closure judgments as structured data; no authenticated GitHub access. |
| New trusted post-script | Deterministically validate proposals and apply allowed issue mutations, including coder dispatch and verified closure. |

**Run flow.** All coordinator entry and recovery paths use the native caller:

1. The caller triggers when the configured main evidence workflow completes,
   including failing tests (`workflow_run: completed`). It also triggers
   when a dispatched coder run completes or fails. It also triggers when a
   pull request linked to a live assignment closes unmerged, and when an
   issue or pull request relevant to live assignments changes state. Manual
   `workflow_dispatch` on main remains the fallback for ambiguous dispatch
   and anything the event set does not cover. Install the caller on main
   through a human-authenticated PR: existing minted credentials cannot
   write workflows.
2. Workflow-level fixed repository concurrency with `cancel-in-progress: false`
   covers the whole called reusable workflow, through preflight, agent, and
   post. Newer pending runs replace older pending runs; every run rereads
   current state. The trigger events therefore cannot pile up runs.
   Evidence and coder workflows remain outside this group.
3. The caller sends one fixed custom-poller matrix entry to the existing pinned
   upstream workflow, selecting the local harness and existing `retro` role.
   One human-created stable coordinator status issue supplies the
   runner-required issue URL; it is not a managed assignment or source issue.
   Exact inputs and credential boundaries are in [Appendix C: Coordinator
   execution contract](#appendix-c-coordinator-execution-contract).
4. Trusted preflight verifies the caller and scripts SHA, checkout, and latest
   main agree, then verifies the triggering event is an allowed one. Whatever
   the trigger, it identifies the latest completed main evidence run and
   verifies that run's workflow, repository, branch, and run SHA. It downloads
   the report by artifact ID from that verified run, checking report SHA and
   completeness. Preflight defers
   without inference when nothing material changed since the last processed
   run. It detects this by comparing a digest of the staged state against the
   digest recorded by the last run that mutated coordination state. Artifacts
   and model output are data, never executable instructions.
5. After successful inference and sandbox teardown, trusted post validates
   result schema and proposed actions before any coordination mutation.
   Invalid schema permits no assignment, dispatch, or closure mutations. It
   rechecks latest main before apply and each closure
   ([Completion and closure](#completion-and-closure)), deferring if state
   advanced. Neither model assertions nor issue text becomes behavior
   authority.

Failing tests in a complete report are valid gaps. Missing, incomplete, or
infrastructure or collection-error evidence is unknown and cannot support
closure or evidence-driven admission. The main evidence job therefore always
uploads its report when collection completes, including when tests failed
([Evidence report and CI](#evidence-report-and-ci)); GitHub Actions otherwise
skips later steps after a failed step. A completed failing main run yields a
complete failing report, not missing evidence. Failed coder runs, unmerged
closes, and live-assignment state changes trigger the caller directly. Each
run reads existing latest-main evidence and live activity; ambiguous dispatch
remains active ([Scoped parallel admission and assignment
lifetime](#scoped-parallel-admission-and-assignment-lifetime)). No report
polling or exactly-once protocol is required.

Coder dispatch uses an App-token `ready-to-code` label, not `GITHUB_TOKEN`.
Preserve upstream coder preflight guards when adding assignment checks. Disable
built-in automatic triage and its automatic coding promotion
(`TRIAGE_AUTO_CODE`); human intake and drafting own source issues, while
coordinator-admitted coding remains enabled. The status issue is excluded from
triage, backlog, `ready-to-code` and `assignment-active` labels, and source or
managed issue closure.

**Readiness.** An [existing successful retro run][existing-retro-run] proves
mint and Vertex inference through this exact upstream pin; its generic custom
harness job was skipped, so it does not prove the coordinator. Build and smoke
test the local composition, including trigger bootstrap, SHA and evidence
checks, sandbox isolation, structured output, serialization through post
completion, App-token dispatch, assignment recovery, non-closing output, and
closure. No end-to-end coordinator deployment success is claimed.

References: [Actions concurrency][actions-concurrency],
[`workflow_run` trust boundary][workflow-run],
[`GITHUB_TOKEN` event restrictions][token-events], and the
[pinned Fullsend runner][fullsend-runner].

[actions-concurrency]: https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency
[workflow-run]: https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run
[token-events]: https://docs.github.com/en/actions/concepts/security/github_token
[fullsend-runner]: https://github.com/fullsend-ai/fullsend/blob/3454a2e4ee2b667e2ed71fa9d384edc6de3d4f74/.github/workflows/reusable-dispatch.yml#L1673-L1878
[existing-retro-run]: https://github.com/redhat-et/ProtoBot/actions/runs/41005357719
[auto-close-setting]: https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/managing-auto-closing-issues
[squash-settings]: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/configuring-commit-squashing-for-pull-requests

### Planning, issue scope, and queue reconciliation

The coordinator agent proposes grouped work, priorities, dependencies, and
combine or split changes from active requirement text, the evidence report,
existing managed issues, live runs, and open PRs. A deterministic wrapper
validates requirement references, scope overlap, current evidence, queued-only
rewrites, and dispatch preconditions; it does not claim semantic grouping.

When reconciling, the coordinator consumes the reviewer's routing tokens
([Reviewer](#reviewer)). It opens or links a source issue for each
`review-intent-gap` finding. It marks the managed issue `needs-replan` and
replans for each `review-bad-plan` finding.

Only managed implementation issues may be rewritten. Every gap is accounted for
by a queued, blocked, or active issue or reported unassigned. Preserve active
assignments; edit only unstarted issues. A superseded issue is marked
`superseded`, not completed, and only after replacements exist. Evidence and
independent adequacy review support closure; coordinator agent assertions
alone cannot establish completion.

Planning may be partial or fail; report unassigned work and recover on the next
run or manually. A managed issue may span specification PRs and need several
implementation PRs. Partial preparatory or test progress does not satisfy a
requirement, and there is no issue-per-specification-PR count.

A replacement requirement retires the old ID. The coordinator must explicitly
replan or supersede the old assignment; it must not silently transfer old
evidence, assignment, or test tags to the new ID. The new ID is uncovered
until tests demonstrate it.

### Scoped parallel admission and assignment lifetime

1. The sole serialized coordinator checks dependencies, requirement scope,
   active assignment labels, live runs, and open PRs. At most one active
   assignment may hold a requirement ID; overlapping work cannot dispatch.
   If the dependency graph of scoped work contains a cycle, admission does
   not dispatch. The coordinator marks involved managed issues `blocked` or
   `needs-replan`, and reports the cycle for human replanning.
2. Before dispatch, it marks the managed issue `assignment-active`, holding
   that issue's requirement scope, then applies `ready-to-code` with an App
   token. No separate reservation record or lease protocol is needed.
3. The Fullsend runner preflights the current assignment and scope, preserving
   upstream input, existing-PR, and subissue guards. Manual `/fs-code` uses the
   same preflight and cannot bypass admission. Scope stays frozen while a live
   run or linked PR exists. A coder job ending alone does not release it.
4. The coordinator records explicit issue, run, and PR links, including
   non-closing and partial PRs. Fullsend's closing-keyword-only PR discovery is
   not sufficient. A partial merge leaves remaining work open; inspect current
   main evidence and live activity before continuing, closing, or releasing the
   assignment.
5. If a scoped requirement retires or is replaced, stop new dispatch, mark
   `needs-replan`, and notify coder and reviewer. Keep the assignment active
   until existing activity is settled; then replan or supersede it. Never
   transfer old evidence to the replacement ID. Current specification governs
   merge, not the old assignment text.
6. When the latest main evidence report contains failures, the coordinator
   admits the failing requirement IDs as a single serialized repair
   assignment: one managed issue covering the currently failing IDs. A
   failing ID already held by a live assignment is not folded into the
   repair issue: that assignment is paused as `needs-replan`, and the ID
   joins the repair issue only after its live run and any open pull request
   are settled. While main is red, no implementation pull request can pass
   the full-suite gate, so admission dispatches no other work until main is
   green again. A repair pull request must fix every failing ID it covers
   in one pull request.

Disjoint requirement scopes may still share code or tests. Git, review, and the
current-candidate suite handle that coupling, with dependencies for known
coupled work. Same-issue retrigger cancels the existing Fullsend run, so never
blindly reapply `ready-to-code`. On failed or cancelled runs, unmerged closes,
partial writes, or ambiguous dispatch, retain the active label until a
coordinator run establishes run and PR state and safely retries, blocks, or
releases work. Failed runs and unmerged closes trigger that run automatically.
Manual `workflow_dispatch` on main remains the fallback for ambiguous dispatch
and anything the event set does not cover. No TTL, unbounded retry loop, or
exactly-once claim is needed.

### Completion and closure

A managed issue closes only when every still-active scoped requirement has
adequate tests that all execute and pass in the ordinary latest-main report.
Partial evidence keeps the issue open. A retired or replaced scope is
superseded, not delivered. Old issue text and evidence never override the
current specification. Closure and removal of `assignment-active` must not
orphan a live run or open PR; settle stale activity before releasing the
assignment or closing.

For closure, “current main” means the exact latest main SHA observed and
revalidated at decision time. Recheck HEAD before each issue closure, not just
once per batch; defer closure if main is known to have advanced. Record the
evaluated SHA and supporting evidence and reviewer links in the closure
rationale or comment. GitHub cannot atomically transact main HEAD and the
Issues API; no atomic merge lock or reopening lifecycle is required. A closed
issue records that determination, not a promise about every later commit;
subsequent commits and gaps follow normal reconciliation.

The template disables the repository's [**Auto-close issues with merged linked
pull requests** setting][auto-close-setting] under **Settings > General >
Issues**. Linked-PR merges must not close source or managed issues; the
coordinator owns verified closure. This setting is not assumed to cover
commit-message closing keywords. Keep `closes_issue:false` and non-closing
references. A deterministic template checker rejects source and managed issue
closing directives in PR titles, PR bodies, commit subjects, and commit bodies
that feed the default-branch merge commit. Stock Fullsend takes the PR title
from the first commit subject and strips only a trailing body footer, so its
existing stripping is insufficient. The initial template selects squash merge
with explicitly configured [PR-title/PR-body message sources][squash-settings]
so checked fields match automatic merge content. Human-edited final messages
follow the same procedural non-closing rule; pre-merge CI cannot guard
subsequent human edits, and no merge lock is introduced.

Source issues auto-close when the system verifies that every approved source
ask is adequately implemented on current main or has an explicit human-approved
exclusion verified as described in [Source provenance](#source-provenance).
The independent Fullsend reviewer ([Reviewer](#reviewer)), not the
implementing coder, verifies each ask against approved requirement text and
current implementation and evidence. Use the current source issue and checklist
reviewed with the specification, never trusting its declared mapping as proof.
Reuse applicable merge-time adequacy review of current requirements, code, and
tests with source mapping and mechanical checks; coordinator agent assertions
alone do not authorize closure. Changed or unclear asks, missing mappings, or
replaced-ID ambiguity need reassessment; absent or stale adequate review keeps
the issue open pending review. Exclusion approval is verified through trusted
preflight, not quoted claims ([Source provenance](#source-provenance)). Closure
remains automatic once sufficient review and evidence exist; there is no new
human closure gate or ledger. Retirement alone is not delivery, and neither a
specification merge nor one implementation PR establishes completion.

These non-closing controls apply to specification and implementation PRs.
Manual closure of a managed issue does not permit a duplicate assignment while
a live run or open PR remains: coordination considers live work, not just open
issues. Adequate tests remain an agent reviewer judgment, not an automated
completeness proof.

### Coder

Fullsend's code agent. Inputs: current default specification, evidence report,
assigned managed issue, repository, and conventions. The issue supplies
requirement scope; the specification supplies behavior. A source issue or
comment is a hint, not scope; a stale hint does not override the current
specification.

The coder selects an increment within its assignment. A transient plan is
optional and uncommitted. It may add tests for a subset of scoped requirements,
stage toward a larger assigned change, or prepare code without yet demonstrating
a requirement. It does not work another active issue.

It derives design, file layout, tests, drivers, and helpers; tests and revises,
and opens a PR against the latest merge candidate. It may add, rewrite,
refactor, split, combine, remove, or retag ordinary tests and leave retired ID
tags on historical tests, including changes needed when another requirement is
introduced. It must not edit requirement text.

All active requirements constrain the change; untested requirements remain
gaps. The coder commits locally; a runner-side post-script opens the PR; the
agent cannot push or merge. A maintenance or preparatory PR may demonstrate no
new requirement, must pass the current suite, and cannot claim full completion
from green CI. Coordination bookkeeping belongs to the repository-owned
coordinator; the coder needs no separate service, lease, retry loop, or
workflow state machine.

### Reviewer

Fullsend's review agent. It starts from the requirement text, derives expected
behavior and evidence independently, and then evaluates both the
implementation and the tests. It checks that test inputs and assertions are
faithful interpretations of the EARS statement. It judges the adequacy of the
changed behavior and its associated tests against the relevant active
requirement text. It also checks that the change does not regress
already-demonstrated requirements. It must not require the pull request to
demonstrate every currently untested active requirement.

The reviewer posts each substantive finding as a review comment on the
implementation PR, and the comment's first line carries exactly one routing
token:

- `review-patch` — The defect is in the changed code or tests; the coder
  fixes it in the same PR. It needs no coordinator action.
- `review-intent-gap` — The requirement text itself is wrong, missing, or
  ambiguous; the fix belongs in the specification flow. The reviewer does
  not open issues; the coordinator opens or links a source issue for the
  finding on its next pass.
- `review-bad-plan` — The assignment itself is wrong; the managed issue is
  marked `needs-replan` and replanned.

Test bodies are not frozen. The reviewer may accept rewritten, replaced, or
removed tests when the remaining suite demonstrates the active text. This
creates a deliberate assurance tradeoff: automated tags show association, not
semantic fidelity. The reviewer must judge test adequacy, and no automated tag
gate proves that a finite suite captures the full requirement.

The reviewer works against the latest merge candidate and current
specification. If the specification changed after the coder began, the
implementation and tests are updated and re-reviewed. A stale issue comment is
planning context, not authority.

### Evidence report and CI

The minimal script-generated evidence report implements the contract in
[Tests and requirement evidence report](#tests-and-requirement-evidence-report):
per active requirement ID, associated collected tests and exact-SHA results,
emitted as CI output for coder, reviewer, and coordinator agent. It is not
committed authority or a semantic completeness proof. Mechanical gap detection
is useful even though the agent reviewer must separately judge adequacy.
Missing tests are gaps; failing tests fail; skipped, unexecuted,
collection-error, missing, or incomplete evidence does not satisfy.

Per-PR and main suites run isolated from the privileged coordinator, with
minimum read-only GitHub permissions and a fresh checkout and runtime without
persisted credentials. Implementation code receives no repository secrets or
App, deployment, or model credentials. Evidence output remains derived and
agent-reviewed, not trusted semantic proof. This isolation preserves existing
Fullsend authorization and eligible code/test PR auto-merge, without a new
human contributor gate.

Ordinary CI runs the full suite, failing on test failures and unknown-ID tags.
Retired-ID tags are allowed and remain runnable historical evidence. A new
active requirement with no tests may merge as a specification gap. Repair of
an already-failing default branch is a single serialized assignment ([Scoped
parallel admission and assignment
lifetime](#scoped-parallel-admission-and-assignment-lifetime)); bare green CI
does not certify requirements. Main runs the full suite for the exact-SHA
report consumed by the coordinator after evidence workflow completion; the
template names the evidence workflow and job, and the current workflow is not
assumed to produce it. The named main evidence job always writes the per-ID
report. It uploads the report whenever collection completes, including when
tests failed. For example, use an `if: always()` upload step after the test
step, or split the test and upload jobs. A missing artifact or a collection
error is incomplete, unknown evidence. A present report with failing results
is a complete failing report. Backlog reporting lists untested and failing
active requirements; priority, ownership, and aging policy are open.

### Protected paths

All Specification-class path changes and process changes require human
approval. Process changes also run relevant process checks in either profile. A
pull request that modifies those paths cannot receive automatic agent approval.
Tests are implementation paths and are not protected by the requirement-text
boundary. An unambiguous ordinary code or test pull request is eligible for the
normal implementation auto-approval and auto-merge path after checks, with an
independent reviewer judging test and implementation adequacy.

The protection boundary is immutable requirement text, not human-frozen test
assertions. This is a deliberate tradeoff: tests may change, but they must
still be adequate interpretations of the current EARS statement.

### Check profiles

The split rule is an always-running classify/aggregate gate evaluated before any
conditional profile. Any diff containing both Specification-class and
Implementation-class paths fails the gate; no profile runs. Process paths may
accompany either class. After the gate passes, any diff with
Specification-class paths runs the specification profile. Every diff with no
Specification-class path — including process-only, process plus code, docs, and
source/tests — runs the implementation profile. Process paths require human
approval and relevant process checks in either profile.

| Profile | Selected when | Runs | Does not run |
| --- | --- | --- | --- |
| Specification | any Specification-class paths, optionally with Process-class paths; no Implementation-class paths | schema; identifiers; EARS shape by declared `type`; immutable `text`; allowed relationships and references; `depends-on` and `supersedes` acyclicity; replacement and retirement rules; test-tag reference check allowing retired IDs; relevant process checks; optionally the ordinary suite for an informational report | mandatory product suite, unless a relevant process check needs it |
| Implementation | no Specification-class paths (including process-only changes) | split gate already passed; full ordinary suite; evidence report; relevant process checks | specification text mutation |

The specification profile may skip the suite for speed unless a relevant
process check needs it; without a test run it produces no evidence report.
Adding process changes to a specification PR does not itself make the product
suite mandatory. Main always runs the full suite. The implementation profile
runs the full suite and produces the current evidence report.

CI mechanics:

- classify paths with rename detection disabled, checking both source and
  destination paths;
- per-PR and merge-group runs validate the actual combined candidate against
  the current base; the per-PR split rule remains per-PR, and an old green run
  is not merge evidence;
- one always-running aggregate gate job is the only required status check. A
  skipped job behind `if:` can report success; a path-filtered workflow can
  leave a check pending. The aggregate gate ensures selected checks actually
  succeeded;
- workflow files under `.github/workflows/` are thin, human-reviewed event
  bindings; Fullsend agents cannot modify them. The check logic lives in
  `ci/` task definitions executed by an ordinary task runner, for example
  `make` or `just`. Agents can therefore update checks in a limited manner
  through reviewed `ci/` changes without touching workflow files.

## Invariants and enforcement

| Invariant | Mechanism | Failure if absent |
| --- | --- | --- |
| Requirement `text` is immutable; every text edit gets a new ID, and replacement adds `supersedes` plus a retired old record in the same specification PR | Validator against default base | Silent semantic edits or rewritten history |
| Specification-class and Implementation-class paths never mix; Process-class paths may accompany either; specification changes are human-reviewed | Always-running split gate plus protected paths | Mixed changes hide implementation in specification review |
| Any diff with no Specification-class path uses the implementation profile; process changes need human approval and relevant process checks in either profile | Profile selection plus protected paths | Process changes auto-approved or unchecked |
| Only `depends-on`, `supersedes`, and `related-to` relationships are allowed; conflicting active obligations must be resolved before merge | Structural validator rejects unsupported types; human and agent semantic review resolves conflicts | Conflicting obligations retained as an approved target |
| The `depends-on` and `supersedes` graphs are acyclic | Validator graph check | Cycles strand work with no dispatch path or recovery |
| Active requirements have no false completion: missing tests are gaps; complete passing tagged results are mechanical coverage, not adequate reviewed evidence | Evidence report plus independent review | Untested, partial, or inadequate evidence mistaken for demonstrated |
| Tests are derived; adequacy is reviewer judgment from EARS text | Review | Tag association mistaken for fidelity |
| At most one active assignment holds a requirement ID; scope is frozen while work is live or dispatch ambiguous | Sole native caller in fixed non-cancelling workflow-level concurrency group covering upstream pre/agent/post; `assignment-active` plus issue scope; runner preflight | Duplicate ownership and conflicting coder work |
| When the latest main evidence report contains failures, repair of the failing requirement IDs is a single serialized assignment, and parallel dispatch resumes only from a green main | Coordinator admission rule plus the full-suite merge gate | Parallel repair PRs deadlock under the full-suite gate |
| A managed issue closes only on adequate reviewed latest-main evidence, and issue text never overrides current specification | Coordinator, evidence report, independent review, current-candidate CI | Closure from stale scope or stale runs |
| Source issues auto-close only after every approved ask is verified on current main as adequately implemented or explicitly human-excluded | Independent reviewer, verified exclusions, source-ask mapping, and mechanical results ([Completion and closure](#completion-and-closure)) | Retirement, stale mappings, or partial implementation mistaken for delivery |
| PR merge does not automatically close source or managed issues; the coordinator owns verified closure | Linked-PR auto-close disabled; checked squash-message inputs and procedural non-closing rule for human edits ([Completion and closure](#completion-and-closure)) | Merge bypasses evidence-based closure |

## Regeneration and delta experiment

A system is **regenerable** when its required behavior and constraints can be
reproduced from its authoritative artifacts alone.

**Full snapshot regeneration** is a manual experiment, not a second validation
mode. It proceeds from an approved target snapshot:

1. Expose only active immutable requirements, narratives and indexes,
   interfaces, and declared process and toolchain inputs. Remove all derived
   implementation: `src/`, `tests/` including tests, helpers, and drivers, and
   any implementation elsewhere.
2. Run the coder to derive all implementation and executable tests directly from
   requirement text. Do not use historical issues, coordination metadata, old
   code, old tests, or a single pull request's scope as input.
3. The reviewer independently evaluates the generated tests against the
   requirement text.
4. Build and run the ordinary full suite.
5. Success requires every active requirement ID to have adequate associated
   tests that execute and pass, with no untested, skipped, failing, or unknown
   evidence. Remaining untested requirements mean an incomplete experiment,
   not success.

Regeneration is a test of specification sufficiency and coder/reviewer
behavior, not proof that a finite suite captures full requirement meaning. The
model can fool itself with weak tests. No claim of mechanical completeness is
made.

**Delta experiment.** Choose any two approved snapshots and a baseline
implementation; implement the changed or current target obligations and run
the ordinary full suite, updating tests as needed. It is a design experiment,
not full regeneration or a merge gate, and may leave other requirements
untested.

Regeneration and delta experiments are manual. No scheduler or ledger is
required.

## What V2 drops from V1

| V1 component | Disposition | Why |
| --- | --- | --- |
| WMS lifecycle, leases, fencing tokens, contract versions | Dropped | Merge is approval; Git plus branch protection is sufficient. |
| Materializer service | Replaced | The repository-owned coordinator groups and splits queued work and controls admission. |
| Job Site, source-control manager, backend translators | Dropped | Fullsend, Git, and the forge cover execution and source control. |
| Impact assessment, projection ACLs, aggregate digests | Dropped as subsystems | Human semantic review and path classes cover the need; digests serialize work. |
| V1 change-set records and manifests | Dropped | V2 has no per-change delivery artifact; current specification and evidence are authority. |
| Evaluation corpus | Deferred | Useful later; not load-bearing for the thesis. |
| Test catalog | Replaced | Direct requirement tags plus the evidence report. |
| `conflicts-with` relationship | Dropped | Resolve conflicting active obligations before merge; structural rejection and semantic review are distinct. |

V2 retains V1's vocabulary where it still applies: requirement, interface,
EARS pattern, and the `depends-on`, `supersedes`, and `related-to`
relationship types.

## Template and instantiation

A new project starts by creating a repository from a template. The template
contains:

- the methodology: `AGENTS.md`, agent definitions, skills;
- the process: validator, specification PR check, thin repository-owned
  coordinator caller and local pre/post scripts ([Repository-owned
  coordinator and Fullsend
  integration](#repository-owned-coordinator-and-fullsend-integration)),
  Fullsend dispatch configuration, and isolated evidence CI
  ([Evidence report and CI](#evidence-report-and-ci));
- pinned tool versions, recorded by digest;
- the existing pinned upstream Fullsend reusable workflow and standalone
  `.fullsend/` coordinator harness, using existing `retro` mint credentials
  and pi/Vertex model configuration without service or secret changes
  ([Appendix C: Coordinator execution
  contract](#appendix-c-coordinator-execution-contract));
  a human creates its stable status issue and installs the native caller
  through a human-authenticated PR;
- the initial test runner and `@req:<ID>` tag extraction;
- managed-issue configuration: only the serialized coordinator marks
  `assignment-active` and sets `ready-to-code` using an App token; runner
  preflight checks the assignment and preserves upstream guards;
- built-in automatic triage and its automatic promotion to coding
  (`TRIAGE_AUTO_CODE`) disabled; human intake and drafting handle source
  issues, while coordinator-triggered Fullsend coding remains enabled;
- linked-PR auto-close disabled, non-closing source/managed issue links,
  `closes_issue:false`, and a deterministic checker over PR titles/bodies and
  commit subjects/bodies; squash merge explicitly uses PR-title/PR-body message
  sources, with the same non-closing rule for human edits
  ([Completion and closure](#completion-and-closure));
- an empty `Vision.md` and `Architecture.md`.

It does not contain a sample specification set. Sample content is a separate
example repository, so instantiating a project never inherits requirements that
describe someone else's system.

## Operating model

**Single player.** One person, local clone, agent, and pull request. No custom
services; validator runs locally and in CI, reconciliation and dispatch use the
repository-owned coordinator and Fullsend.

**Multi player.** Parallel people and agents. Admission gives disjoint
requirement-ID assignments; disjoint scopes do not imply disjoint code or
tests, so Git, review, and the current-candidate suite integrate. Only the
coordinator is serialized; coders have no global execution lock.

**Coordination.** Managed issues, active assignment labels and requirement
scopes, and one natively serialized Actions caller running local coordinator
pre/agent/post inside existing trusted Fullsend execution. Evidence
completion, coder-run completion or failure, unmerged closes, and
live-assignment state changes trigger it. Manual `workflow_dispatch` on main
remains the fallback for ambiguous dispatch and anything the event set does
not cover. Issue comments and links are ordinary GitHub notifications, not a
lifecycle service. Built-in automatic triage and its
automatic promotion to coding (`TRIAGE_AUTO_CODE`) are off; human intake and
drafting precede coordinator admission. Coordinator-triggered Fullsend coding
remains enabled.

**Auto-merge.** Ordinary implementation and test PRs passing checks and
touching no protected paths; never Specification-class or process changes.

**Merge path.** One ordinary safe path; per-PR and merge-group runs validate the
current candidate. Initial squash-message sources and non-closing checks are
configured as described in [Completion and closure](#completion-and-closure);
linked-PR auto-close is disabled. No separate queue orchestration.

**Source issues.** The system auto-closes them when agent judgment and
mechanical test results verify every approved source ask is adequately
implemented on current main or explicitly human-excluded. Missing mappings or
replaced-ID ambiguity stay open for reassessment; retirement is not delivery.
Specification and implementation merges follow the non-closing controls in
[Completion and closure](#completion-and-closure); the coordinator owns
verified closure. No human closure gate is required.

## Adoption path

This document is a proposal; nothing here authorizes implementation. If V2
is adopted, rollout proceeds in independently gated stages, each separately
reviewed. Issue #346 tracks the discussion and adoption scope.

1. **Proceed decision.** Record the decision to build and evaluate V2 within
   a stated scope. V1 specifications and accepted ADRs remain the current
   contracts until V2 is adopted and supersedes them.
2. **Validator.** Rework `ears-manager` into the V2 validator, retaining
   useful validation code and replacing its V1 contract
   ([Validator](#validator)). It is deterministic and network-free, so it is
   testable locally without any service.
3. **Template.** Build the repository template: layout, path taxonomy, check
   profiles, non-closing controls, and Fullsend dispatch configuration
   ([Template and instantiation](#template-and-instantiation)).
4. **Coordinator composition.** Build and smoke test the local coordinator
   composition per the readiness list ([Repository-owned
   coordinator and Fullsend
   integration](#repository-owned-coordinator-and-fullsend-integration)),
   using the existing mint configuration and the selected inference
   credential boundary.
5. **Pilot.** Instantiate the template for one real prototype, single player
   first, then multi player. The pilot exercises the full loop — prose issue
   to specification to converging implementation to demonstration readiness —
   and exercises the TransferBot handoff boundary
   ([TransferBot handoff](#transferbot-handoff)).
6. **Evaluate.** Regeneration and delta experiments test specification
   sufficiency ([Regeneration and delta
   experiment](#regeneration-and-delta-experiment)). The pilot outcome decides
   whether V2 replaces V1 or informs a revised design.

## Deferred items

The improvements below are acknowledged as valuable and deliberately excluded
from the V2 MVP. Issue #346 records them for post-MVP consideration.

- **Parallel coder slot cap.** Limit the number of concurrently active coder
  assignments. GitHub Actions concurrency groups support only one active run
  per group, so a slot cap needs a different mechanism. This is deferred
  pending that analysis.
- **Priority labels.** A `p1`–`p4` label scheme with defined criteria to
  order admission. Blocking dependencies already handle hard sequencing.
  This is deferred until a need is observed.
- **Run fault classification.** Fault classes, attempt counts, and named
  exits for runs that end badly. This is deferred pending analysis of what
  Fullsend and GitHub Actions can support.
- **Source-issue state.** Labels could follow a source ask as it progresses,
  and one link comment could list its requirement IDs and managed issues.
- **Read-only triage agent.** Closing duplicates and promoting bug reports
  directly to the implementation phase. Human intake and drafting continue
  to own issues that need specification modification.
- **Web Drafting Table.** A hosted Web Drafting Table (see open question 5)
  could pull IdeaBot and Jira artifacts directly into a Drafting Table
  session instead of manual copying. The manual mechanism is described in
  [Source provenance](#source-provenance).
- **Reference provenance in drafting.** For documents added to a Drafting
  Table session, record which source motivated each requirement and why it
  was created. A human can then quickly confirm or correct the intent.

## Open questions

1. **Test runner, tag extraction, and evidence reporting.** Which ordinary test
   runner and `@req:<ID>` extraction should the template adopt, and how should
   the evidence report represent untested, failing, skipped, and incomplete
   evidence? Gherkin and `@req:` tags are illustrative, not selected.
2. **Semantic review and test adequacy.** Human review against the current
   specification cannot eliminate overlapping requirements or weak tests. How
   much consistency aid is worth building, and how should reviewers judge
   whether a finite suite adequately demonstrates an EARS statement?
3. **Issue grouping quality and coupling.** Disjoint requirement scopes do not
   prove disjoint code or tests. How good must grouping be, and how much
   coupling should be serialized through dependencies rather than left to
   review and the full suite?
4. **Coordinator smoke tests.** The local caller, harness, and pre/post
   scripts remain proposed. Prove default-branch trigger bootstrap,
   snapshot-only inference and output retrieval, trusted exact-main evidence
   handling, native serialization through upstream post completion, App-token
   dispatch, assignment preflight and recovery, non-closing output, and
   evidence-based closure end to end using the existing mint configuration
   and the selected inference credential boundary. No Fullsend upgrade is a
   prerequisite; initial built-in automatic triage and its automatic promotion
   to coding (`TRIAGE_AUTO_CODE`) are disabled, not coordinator-triggered
   Fullsend coding.
5. **Remaining V1 scope dispositions.** How should V2 address Specification
   Toolkit packaging in the [Template and
   instantiation](#template-and-instantiation) methodology, Kits (V1's
   versioned imports of proposed EARS/interface content and Inspector
   definitions) and Kit source imports, the hosted Web deployment mode
   (including the Web Drafting Table and its session state), Gate approval
   records (V1's authorization checkpoints at mutation boundaries in hosted
   modes), the deployment-level registry (V1's per-deployment project
   registry), and their related OAuth 2.1 and Bridge/Gate constraints (V1's
   credential-isolation pattern required only in hosted modes)? Should it
   retain V1's reserved `project` applicability scope for project-wide and
   environmental requirements? These are unresolved future V2 scope
   questions, not decisions to retain, drop, or adopt those V1 capabilities
   or constraints.
6. **Planning model.** Should planning run once per source issue, immediately
   after its specification PR merges, with the plan posted as a comment that
   a later replan can redo? Or should the coordinator keep reconciling the
   whole open queue on each pass? Per-source-issue planning bounds
   managed-issue size and makes each plan visible, but risks splitting work
   that belongs in one assignment. Whole-queue reconciliation is stable, but
   churns issues that are merely waiting. Also unresolved: when a replan may
   re-cut existing queued issues, and how coder pickup is gated against a
   list that is about to be re-cut.
7. **Scheduler shape.** Should the coordinator remain one Fullsend agent
   invoked by the trigger events, or become a plain scheduled job whose
   deterministic logic decides when to invoke the agent? A plain job cannot
   perform semantic grouping; the agent requires inference for it. Whether a
   time-based poll earns its inference cost is unresolved.

## Relationship to V1

V2 is a clean-slate design derived from V1's thesis, not a migration plan. There
is no V1 schema or CLI backward-compatibility requirement, cutover obligation,
or requirement that V1 and V2 coexist. V1 specifications and accepted ADRs
remain the current contracts unless separately superseded by an adopted
decision; this proposal does not authorize that transition. For the proposed V2
design, salvage useful V1 ideas and code freely — including validation code and
Drafting Table interaction patterns — without retaining their old contracts or
surrounding machinery.

## Appendix A: Path taxonomy

The taxonomy is defined once and shared by every proposed V2 check. It does not
classify paths or govern checks in the current V1 repository.

| Class | Paths | Role in split |
| --- | --- | --- |
| Specification | `Vision.md`, `Architecture.md`, `spec/**` | must not mix with Implementation |
| Process (protected) | `ci/**`, `.github/**`, `AGENTS.md`, `CLAUDE.md`, `opencode.json`, `.agents/**`, `.claude/**`, `.opencode/**`, `.fullsend/**` | may accompany either class; human-protected; relevant process checks |
| Implementation | everything else not listed above, including `src/**` and `tests/**` | must not mix with Specification |

The split rule forbids only Specification-class plus Implementation-class paths
in one PR. Process-class paths may accompany either. Any diff with
Specification-class paths runs the specification profile; optional process
changes require relevant process checks, not a mandatory product suite unless a
process check needs it. Every diff with no Specification-class path —
process-only, process plus code, docs, or source/tests — runs the implementation
profile. Process paths require human approval in either profile. Tests are
implementation, not specification.

## Appendix B: Reference checks

The specification PR check is a validator mode, not a grep. In outline:

1. Read changed paths with rename detection disabled, on both sides of the
   merge-base diff.
2. If any Specification-class path and any Implementation-class path changed,
   fail. Otherwise, a diff with Specification-class paths runs the specification
   profile, optionally including Process-class paths; a diff with no
   Specification-class path runs the implementation profile, including
   process-only changes. Process paths require human approval and relevant
   process checks whichever profile is selected.
3. For the specification profile, validate schema, identifiers, EARS shape
   by declared `type`, immutable `text`, allowed relationships, references,
   `depends-on` and `supersedes` acyclicity, replacement and retirement
   rules, and test tags allowing retired IDs. Run relevant process checks. The
   ordinary suite is optional for an informational report unless a process
   check needs it. Semantic consistency remains a human and agent review
   responsibility.
4. For an implementation diff, run the full ordinary suite and evidence report
   for the current specification; failing tests and unknown-ID tags fail.

The traceability tag is runner-specific; the contract is that a test
demonstrating a requirement carries its ID. One syntax:

```python
# @req:REQ-API-00002
```

## Appendix C: Coordinator execution contract

### Caller and fixed matrix

Call `fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml` at
`3454a2e4ee2b667e2ed71fa9d384edc6de3d4f74` (v0.45.0), using its supported
custom-poller `matrix` input, not the standard shim's event normalization. The
caller constructs exactly one entry; recovery cannot supply arbitrary matrix
entries or roles.

| Matrix field | Value |
| --- | --- |
| `agent` | `protobot-coordinator` |
| `role` | `retro` |
| `source_repo` | `redhat-et/ProtoBot` here; the current repository in an instantiated template |
| `event_type` | `workflow_dispatch` |
| `event_payload` | Serialized JSON string containing `issue.number` and `issue.html_url` for the configured coordinator status issue |
| `status_repo` | Current repository, matching the coordinator status issue |
| `status_number` | Configured coordinator status issue number, rendered as a string for the execution reference |

Store the status issue number in local process configuration or a repository
variable, not mint settings. Validate that the issue belongs to the current
repository. Normal Fullsend status and run links use this same issue, subject to
existing repository `status_notifications` settings. It is ordinary
coordination context, not delivery work, behavior or evidence authority, or a
ledger; its exclusions are defined in [Repository-owned coordinator and
Fullsend integration](#repository-owned-coordinator-and-fullsend-integration).

Pass the same `mint_url`, `gcp_region`, `install_mode`, and existing GCP
variables and secrets as the installed shim. Retain its reusable-job permission
envelope, including `id-token: write`; these GitHub workflow permissions do not
add mint App permissions. No native job mints or receives App tokens, and no
token returns to the caller through outputs or artifacts.

### Local agent and standalone harness

Register an enabled agent in `.fullsend/config.yaml` with these values:

| Agent field | Value |
| --- | --- |
| `name` | `protobot-coordinator` |
| `source` | `harness/protobot-coordinator.yaml` |
| `runtime` | `pi` |
| `model` | `google-vertex/gemini-3.8-flash`, matching the current retro route |

The standalone harness uses `role: retro`,
`agent: agents/protobot-coordinator.md`,
`pre_script: scripts/pre-protobot-coordinator.sh`, and
`post_script: scripts/post-protobot-coordinator.sh`, with
`privilege_levels: {pre_script: read, runtime: read, post_script: write}`.
The `harness/`, `agents/`, and `scripts/` values in this appendix are
relative to `.fullsend/`, so they resolve under `.fullsend/**` (Process
class in the path taxonomy).
Omit both `trigger` and `base`: no inherited retro prompts, scripts, or
triggers. Also omit CEL `overlays`: this minimal payload has no
`_normalized_event`, so event-dependent overlays are outside this contract.
The [ordinary dispatcher][harness-enumeration] excludes triggerless harnesses
from shim events and slash commands; the explicit matrix still resolves the
enabled registered agent. Template policy permits no other coordinator
entrypoint; human-reviewed process changes could deliberately bypass it, so
this is not a new enforcement framework. Preserve upstream same-issue
cancellation, but give parent and child different concurrency groups.

### Trusted state and credential boundary

The generic job loads configuration and scripts at caller `github.sha`, while
the target `source_repo` separately defaults to main; there is no matrix
`source_ref` field. Trusted preflight compares caller `github.sha`, target
checkout HEAD, and API latest main, aborting or deferring on mismatch. Only
trusted default-main scripts execute. Preflight stages the credential-free
specification, exact-main report, issues, runs, PRs, and verified review and
exclusion data in the target checkout for sandbox upload
([Repository-owned coordinator and Fullsend
integration](#repository-owned-coordinator-and-fullsend-integration)).
Before upload, exclude or strip persisted Git checkout credentials,
token-bearing configuration or helper files, and host credential files; preserve
trusted host pre/post credentials outside the uploaded tree. Environment
filtering alone does not establish isolation.

Existing [phase privilege handling][phase-privileges] supplies host
`GH_TOKEN` for pre/post and mints a runtime read token on the host; post
remints at write level and fails closed if that level cannot be obtained. The
[existing retro write role][retro-role] includes contents read, Actions read,
issues write, pull requests write, and metadata read. This is broader than
issue-only access: the new trusted post-script restricts its use to allowed
issue mutations and status, not PR writes. Keep GitHub tokens out of the agent
sandbox, artifacts, logs, and untrusted content. The independently dispatched
Fullsend reviewer never receives coordinator credentials.

The sandbox inference configuration follows the selected mechanism. Under
the proxied mechanism, the sandbox holds no inference credential and reaches
the model only through the trusted-host proxy. Under the scoped-WIF
mechanism, the sandbox receives the inference WIF credential, which **does
enter the sandbox**, scoped to the inference API alone. In both cases,
include no GitHub provider, GitHub-token-containing host-file or
environment mappings, or authenticated GitHub proxy.

Staged issue, pull request, run, and evidence text is untrusted data.
Implementation PRs eligible for auto-merge can carry attacker-controlled test
names and output into that staged text. The coordinator agent treats it as
data, never as instructions.

Inference credentials must carry no authority beyond calling the model.
Before adoption, select and verify one of two mechanisms, mirroring the
existing live-mint-policy verification prerequisite. The preferred mechanism,
if supported without an upstream Fullsend upgrade, keeps inference
credentials on the trusted host. The sandbox then reaches the model only
through a proxied call. Otherwise, the inference WIF principal is scoped to
the inference API alone, with no other GCP or GitHub authority. All
coordinator agent judgments, including ask and closure verification, use only
staged data and model access. Product suites remain separate and secret-free
([Evidence report and CI](#evidence-report-and-ci)).

After successful inference and sandbox teardown, the trusted post-script
validates result schema and proposed actions before any coordination mutation,
then enforces the rules in
[Planning, issue scope, and queue reconciliation](#planning-issue-scope-and-queue-reconciliation)
through [Completion and closure](#completion-and-closure).
Invalid schema permits no assignment, dispatch, or closure mutations; model
output never authorizes an unchecked mutation.

The documented open-source [mint trust boundary][mint-trust] checks repository
or owner and `job_workflow_ref` host or basename, not caller `workflow_ref`,
`workflow_sha`, `event_name`, or `job_workflow_sha`. However, the live-hosted
mint policy was not queried directly. Before adoption, confirm whether the live
deployment binds or restricts caller workflow identity. If the hosted mint
policy does not bind the caller workflow, caller authorization must be
established by restricting execution to the reviewed caller workflow on the
default branch (protected branch and PR merge rules) and validating caller ref
before minting, rather than assuming no caller allowlist is required. Generated
repository inference WIF binds `assertion.repository`, not a workflow ref.
Validate the new local composition through smoke tests ([Repository-owned
coordinator and Fullsend
integration](#repository-owned-coordinator-and-fullsend-integration))
without changing hosted mint configuration, roles, permissions, or hosts;
the selected inference credential mechanism governs inference configuration.

[harness-enumeration]: https://github.com/fullsend-ai/fullsend/blob/3454a2e4ee2b667e2ed71fa9d384edc6de3d4f74/internal/harnessdispatch/enumerate.go#L76-L94
[phase-privileges]: https://github.com/fullsend-ai/fullsend/blob/3454a2e4ee2b667e2ed71fa9d384edc6de3d4f74/internal/harness/privilege.go#L9-L51
[retro-role]: https://github.com/fullsend-ai/fullsend/blob/3454a2e4ee2b667e2ed71fa9d384edc6de3d4f74/internal/mintcore/github.go#L79-L112
[mint-trust]: https://github.com/fullsend-ai/fullsend/blob/3454a2e4ee2b667e2ed71fa9d384edc6de3d4f74/internal/mintcore/claims.go#L105-L237

---

## Related Documents

- [Vision](../vision.md) — Purpose, intended users, and desired outcomes.
- [Architecture](../architecture.md) — External interfaces, persistent
  state, and environmental constraints.
- [Overview](overview.md) — Guiding principles, workflow, and platform.
- [System Components](components.md) — Component architecture, interfaces,
  and cross-cutting concerns.
- [Related Work](related-work.md) — Internal and external projects informing the
  design.
- [Open Design Questions](open-questions.md) — Unresolved V1 design questions
  across all areas.
