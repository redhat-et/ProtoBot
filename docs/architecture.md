# ProtoBot: Architecture

> Design document — draft, September 2026
>
> Part of ProtoBot's initial Sketch — defines the system's external
> interfaces, pluggable boundaries, persistent state, and
> environmental constraints.

**Contents:**

- [Purpose](#purpose)
- [External Interface Inventory](#external-interface-inventory)
- [User-Facing Interfaces](#user-facing-interfaces)
- [Specification Toolkit](#specification-toolkit)
- [`ears-manager` CLI](#ears-manager-cli)
- [WMS Adapter API](#wms-adapter-api)
- [Validation Rules](#validation-rules)
- [Project Repository](#project-repository)
- [Job Site Handoff Boundary](#job-site-handoff-boundary)
- [Drafting Table Boundary](#drafting-table-boundary)
- [Persistent State](#persistent-state)
- [Environmental Constraints](#environmental-constraints)
- [Interface Specification Approach](#interface-specification-approach)
- [Related Documents](#related-documents)

## Purpose

This document is the Architecture artifact of ProtoBot's initial
Sketch. It defines the second level of ProtoBot's own
[specification hierarchy](architecture/overview.md#specification-hierarchy):
the system's external interfaces, their types, and the approach
for specifying each one.

The Architecture answers: _What does ProtoBot look like from the
outside?_ Internal decomposition — how components are built,
which libraries they use, how they structure their code — is an
implementation concern left to the Building phase. What belongs
here are the boundaries where an independent party could write
an implementation against the contract.

For ProtoBot's Vision (the first level of the Sketch), see the
[Overview](architecture/overview.md).

---

## External Interface Inventory

The inventory below lists every external interface: component
boundaries, persistent state (which outlives any single run and
requires its own read/write contract), and pipeline endpoints.
Environmental constraints are listed in a
[separate table](#environmental-constraints).

| # | Interface | Type | Provider | Consumers |
| --- | --- | --- | --- | --- |
| 1 | Drafting Table ↔ User (TUI) | REPL | Drafting Table | — |
| 2 | Drafting Table ↔ User (Web) | Web GUI | Drafting Table | — |
| 3 | Specification Toolkit ↔ Agent Harness | Agent skill package | Specification Toolkit | Drafting Table (TUI), Drafting Table (Web) |
| 4 | `ears-manager` CLI | CLI | `ears-manager` | Specification Toolkit, CI, Job Site |
| 5 | WMS Adapter API | Network service | WMS Adapter | Drafting Table, Job Site |
| 6 | Validation Rules | Linkable library or declarative rule set _(open — see [components.md](architecture/components.md#validation-rules))_ | Validation Rules | Drafting Table, Job Site, WMS Adapter |
| 7 | Project repository (Git) | Persistent state | Project | Drafting Table, Job Site, `ears-manager`, CI |
| 8 | Job Site intake (change-set registration, true-bug intake) | CLI + webhook | Job Site | CI hooks, Git hooks, maintainers |
| 9 | Claim coordinator | Persistent state | WMS Adapter | Job Site |
| 10 | Kit source | Package source | Kits | Drafting Table |
| 11 | IdeaBot handoff _(manual, Q4)_ | Pipeline input | IdeaBot | Drafting Table |
| 12 | Prototype and demo artifacts | Pipeline output | Job Site | TransferBot, stakeholders |

Most interfaces are described in dedicated sections below.
The Job Site intake interface and the claim coordinator are
captured in the [Persistent State](#persistent-state) section
rather than in standalone sections.

**Kit source** has three interfaces (see
[components.md — Kits](architecture/components.md#kits)): import into
`ears-manager` as a proposed change set with Kit provenance,
resolution of pinned source/version/digest, and Inspector
definition activation via project policy review. The
specification approach is a versioned import manifest
(source/version/digest/provenance).

---

## User-Facing Interfaces

ProtoBot has two user-facing interface implementations, both
serving the Drafting Table's Sketching and Dimensioning phases.
They share the same Specification Toolkit, Validation Rules, and
WMS Adapter API — the difference is the frontend and hosting
model, not the domain logic.

### TUI Drafting Table (REPL)

The TUI Drafting Table is a terminal-based conversational
interface that uses an existing coding agent (OpenCode or Claude
Code) as the agent harness. The user launches it locally; the
harness runs on their machine.

**Interface type:** REPL — an interactive session where the user
and agent exchange messages, with the agent invoking tools
(via the Specification Toolkit) to read and write specification
artifacts.

**Interaction surface:**

- Conversational input/output between human and agent
- Agent-initiated tool calls to `ears-manager` and the WMS
  Adapter, mediated by the Specification Toolkit's tool
  definitions
- Display of work-item status from the WMS Adapter
- Blocked-work-item notifications via a pull model: on session
  start, the harness checks the WMS for blocked items and
  presents them

**Scope for specification:** The REPL interface type has no
established IDL. The contract is defined by the Specification
Toolkit's skills and prompts (which govern what the agent can
do) and the tool definitions (which govern how the agent
interacts with `ears-manager` and the WMS Adapter). The user's
interaction surface is the conversational protocol — what
questions the agent asks, how it presents suggestions, and how
approval flows.

### Web Drafting Table (Web GUI)

The Web Drafting Table is a browser-based interface with a
hosted agent runtime running server-side.

**Interface type:** Web GUI — a persistent web application with
push notifications for blocked work items and Job Site status
updates.

**Interaction surface:**

- Same conversational and tool-calling capabilities as the TUI
- Push notifications for blocked work items (via persistent
  browser connection)
- No local setup required

**Session state:** The Web Drafting Table is a shared,
multi-tenant service that must manage per-user session state
(conversation history, in-progress change sets, and tool
execution context). Session state is an additional persistent
store not required by the TUI, where the local harness
process owns the session implicitly.

**Authentication and authorization:** As a shared service, the
Web Drafting Table requires authn/authz at the application
boundary. Users authenticate via OAuth 2.1 (Red Hat SSO /
Keycloak — see [Environmental
Constraints](#environmental-constraints)). Authorization
determines which projects and specification artifacts a user
may access and which WMS operations they may perform
(e.g., only authorized maintainers update business priority).
The credential isolation pattern (Bridge/Gate) applies: the
Web Drafting Table's agent runtime never sees user
credentials directly.

**Scope for specification:** The Web GUI interface type has no
established IDL for ProtoBot's use case. The web interface
shares the Specification Toolkit's domain logic with the TUI;
the additional specification surface is the session
management, authentication/authorization model, and
notification/push model.

---

## Specification Toolkit

The Specification Toolkit is the portable domain logic that any
Drafting Table implementation loads to become capable of driving
Sketching and Dimensioning. It is not a running service — it is
a set of skills, tool definitions, and prompts consumed by the
agent harness.

**Interface type:** Agent skill package — the Toolkit is loaded
into the agent harness at session start and provides structured
instructions and tool schemas. Nothing links to it as a library;
the contract is Markdown skills, JSON tool schemas, and prompts.

**External contract:**

- **Skills:** Structured instructions for Sketching (elicit
  Vision, enumerate interfaces, identify types) and
  Dimensioning (translate Architecture into EARS requirements,
  surface spec gaps, handle each EARS pattern type).
- **Tool definitions:** MCP tool schemas or API client code for
  `ears-manager` operations (see the
  [`ears-manager` CLI table](#ears-manager-cli)) and WMS Adapter
  operations (create/read/update work items, query blocked
  items).
- **Prompts:** System prompts, templates, and reference material
  including EARS pattern definitions, the interface-type
  taxonomy, gap-closing heuristics, and the specification
  hierarchy.
- **Trace output:** Structured trace data (replayable inputs,
  outputs, and decision records) for every agentic operation,
  enabling component-level evaluability. The trace format is
  an open design question.

**Harness contract:** The Toolkit must work in any compatible
agent harness — OpenCode, Claude Code, a hosted web runtime, or
future harnesses. It depends on standard tool execution and
prompt loading; no harness-specific APIs beyond those.

**Runtime dependencies:** The WMS Adapter API (for work-item
lifecycle state), the `ears-manager` binary (for all
specification reads and writes), and a Git working tree.

See [System Components — Specification
Toolkit](architecture/components.md#specification-toolkit) for design details.

---

## `ears-manager` CLI

`ears-manager` is the programmatic interface to the
specification store. It abstracts the underlying file format,
manages change sets, and enforces EARS methodology rules
deterministically.

**Interface type:** CLI — a statically linked Go binary with a
stable subcommand surface.

**External contract:**

| Subcommand | Purpose |
| --- | --- |
| `requirement add` | Add a new EARS requirement with applicability metadata. |
| `requirement list` | List requirements (filterable). |
| `requirement show` | Show a requirement by ID. |
| `requirement update` | Modify an existing requirement through a change set. |
| `requirement retire` | Retire a requirement through a change set. |
| `interface add` | Register a new interface in the Architecture. |
| `interface list` | List interfaces (filterable). |
| `interface show` | Show an interface by ID. |
| `interface update` | Modify an existing interface through a change set. |
| `artifact put` | Create/update a registered artifact (Vision, Architecture, IDL). |
| `artifact get` | Read a registered artifact by kind or ID. |
| `artifact list` | List registered artifacts. |
| `change-set create` | Create a new proposed change set. |
| `change-set list` | List change sets (filterable). |
| `change-set show` | Show a change set by ID. |
| `change-set compare` | Compare a change set with the current Schematic. |
| `change-set update` | Update a proposed change set. |
| `check` | Validate spec files: EARS formatting, required fields, referential integrity. CI gate. |
| `impact` | Produce potentially applicable requirements from scope intersections. |

**Callers:** The Specification Toolkit (via agent tool calls),
CI pipelines (`ears-manager check` as a merge gate), the Job
Site (resolving requirements at a specification commit), and
humans directly.

**File system interface:** `ears-manager` reads and writes
specification data on disk (under paths registered in
`.protobot/project.yaml`). The on-disk format is an external
interface because requirements data must be forward
upgradeable as `ears-manager` evolves. Each store carries a
schema version; `ears-manager` refuses to operate on data
at a version newer than its own, and forward migration
happens through a reviewed change set (see
[Persistent State — Specification store](#specification-store-git)).
Callers must not parse or write these files directly — the
CLI is the stable contract; the storage format may change
between versions.

**Design constraint:** Deterministic, not AI-driven.
`ears-manager` is conventional code — a linter, validator, and
CRUD tool. The agent decides _what_ requirements to write;
`ears-manager` ensures they are well-formed.

See [System Components —
`ears-manager`](architecture/components.md#ears-manager) for subcommand
details and validation rules.

---

## WMS Adapter API

The WMS Adapter is a thin, pluggable integration layer between
ProtoBot and the user's chosen work management backend.

**Interface type:** Network service — a stable API that the
Drafting Table and Job Site both call. One adapter
implementation is active per project.

**External contract:**

- **Requests:** Create, read, refine, and query backlog
  requests. Update business priority (authorized maintainer
  only).
- **Materialization:** Atomically create or return a build work
  item by stable idempotency key, writing the complete contract
  in one durable operation.
- **Lifecycle transitions:** Atomic update of work-item state.
  Every state mutation is atomic and returns the new state or
  a conflict.
- **Queries:** Read items by ID, state, dependency, owner, or
  idempotency key.
- **Git references:** Record specification and code commits,
  content branches, merge commits, and reconciliation state.
- **Conformance evidence:** Associate delivery-obligation
  requirement IDs with immutable verification artifacts.
- **Finding ledger:** Create, append, and query findings with
  stable idempotency keys and expected-version semantics.

**Pluggable backends:**

| Backend | Native concept |
| --- | --- |
| GitHub Issues/Projects | Issue per build work item |
| GitLab | Issue per build work item |
| Jira | Issue per build work item |
| Beads | Bead per build work item |
| Trello | Card per build work item |

Every adapter must provide the same atomic materialization and
transition semantics. Backend-specific translators remain
deliberately thin; shared Validation Rules enforce lifecycle
logic at the API boundary.

**Deployment topology:**

| Mode | WMS Adapter | Job Site and sandbox | Token source |
| --- | --- | --- | --- |
| Single-player | Local process over MCP stdio | Local process; sandbox via portable rootless-OCI/microVM profile | User's own Git host token (no OAuth 2.1 infrastructure required) |
| Multi-player | Network service (one per project) | Hosted service; sandbox via Fullsend/OpenShell | OAuth 2.1 via Alcove Bridge/Gate pattern |
| Web | Network service (shared deployment) | Hosted service; sandbox via Fullsend/OpenShell | OAuth 2.1 via Alcove Bridge/Gate pattern |

In single-player mode the adapter, Job Site, and sandbox all run
on the developer's machine without requiring a cluster
([Overview — Single-player mode](architecture/overview.md#single-player-mode)).
OAuth 2.1 with Bridge/Gate is required only in the hosted modes.

See [System Components — WMS
Adapter](architecture/components.md#wms-adapter) for the full API surface
and lifecycle state machine.

---

## Validation Rules

Validation Rules are the domain logic that enforces
well-formedness on work-item state transitions.

**Interface type:** Linkable library or declarative rule set
_(open — the packaging is not yet decided)_ — consumed by the
Drafting Table, Job Site, and WMS Adapter boundary.

**External contract:**

- **Caller library/schema:** Drafting Table, Materializer, Job
  Site, and Finding Router evaluate proposed commands for early
  diagnostics before the WMS write.
- **WMS write boundary:** Receives a command, trusted
  authorization context, expected state/version/fencing token,
  and current record. Returns the allowed transition or a
  structured rejection.
- **Rule version:** Every decision records the exact
  rule/policy version for replay and audit.

**Scope boundary with `ears-manager`:**

- `ears-manager` handles _specification-level_ validation:
  EARS formatting, required metadata, referential integrity,
  and file format consistency.
- Validation Rules handle _lifecycle_ validation: work-item
  state transitions, pipeline entry point determination, and
  readiness checks.

See [System Components — Validation
Rules](architecture/components.md#validation-rules) for details.

---

## Project Repository

The project repository is the canonical Git repository for a
ProtoBot project. All project content — approved specifications,
in-progress code and tests, change-set manifests, and
attestation artifacts — lives in Git.

**Interface type:** Persistent state — Git is an external
interface because it outlives any single run, requires an
upgrade/rollback path, and is read and written by multiple
components.

**External contract:**

- **Control namespace:** ProtoBot mandates a `.protobot/`
  directory with project configuration, projection manifest,
  policy, Kit locks, change-set manifests, test catalog, and
  attestation artifacts.
- **Branch conventions:** Contributor/change-set branches for
  draft specifications, `wi/` branches for in-progress work
  items, and `main` for approved state.
- **Write gates:** `ears-manager` is the exclusive write gate
  for registered specification artifacts. The Job Site owns
  the test catalog and attestation paths. CI rejects edits
  by a component or Worker outside its owned/allowlisted
  paths.
- **Merge strategy:** Merge commits (not squash or rebase) to
  preserve the iteration DAG for evaluability.

**Callers:** The Drafting Table (branches, commits, PRs), the
Job Site (integration branches, merges), `ears-manager`
(specification working tree), and CI (validation gates).

See [System Components — Content Storage
Model](architecture/components.md#content-storage-model) for the full
content model and merge strategy.

---

## Job Site Handoff Boundary

The Job Site is the autonomous execution engine for Building
and Inspecting. This Architecture intentionally limits its
scope to the handoff boundary — what crosses the line between
interactive and autonomous work. The Job Site's internal
components, worker topology, execution backends, and sandbox
architecture are detailed in the [System
Components](architecture/components.md#job-site) design document and are
out of scope for this Sketch.

**Pipeline input (what seeds ProtoBot):**

- **IdeaBot handoff** _(manual, Q4)_ — IdeaBot artifacts seed
  the first Sketching session. Until Q4 is resolved, this
  handoff is manual.

**Handoff inputs (what the Job Site receives):**

- **Job Site intake events** — change-set registration (on
  merge, a registration hook calls the Job Site with the
  change-set ID, merge commit, and materialization key; in
  single-player mode, a local
  `register-approved-change-set` command) and true-bug intake
  (a bug report with violated requirement IDs and affected
  scope, without a change set or PR). Both are external
  entry points (CI hooks, Git hooks, maintainers).
- **Build work items** from the WMS Adapter in
  `ready-for-building` state, claimed via atomic
  compare-and-swap.
- **Specification content** from the project repository at the
  immutable specification commit recorded in the work-item
  contract.
- **Changed and applicable requirement IDs** from the
  work-item contract, resolved through `ears-manager`.
- **Project policy** from `.protobot/policy.yaml` (required
  Inspectors, sandbox profile, scheduling policy).
- **Projection manifest** from `.protobot/projection.yaml`
  (deny-by-default path classification for Worker
  repositories).

**Handoff outputs (what the Job Site produces):**

- **Merged code and tests** on the `wi/` branch, merged to
  main on completion — [pending
  Q13](architecture/open-questions.md#q13-autonomous-merge-and-hu-02).
- **Conformance evidence** — immutable verification artifacts
  naming requirement IDs, specification commits, and tested
  candidate commits.
- **Finding Ledger snapshots and inspection reports** committed
  under the attestation namespace before merge.
- **Work-item state transitions** written to the WMS Adapter
  (building → inspecting → merging → completed, or → blocked
  for escalation).
- **Escalation issues** opened in the project repository when
  undefined behavior is discovered.
- **Prototype and demo artifacts** — the working, tested,
  inspected prototype and its demonstration artifacts (manifest
  under `.protobot/attestations/demos/`). These are the
  pipeline's primary product, consumed by TransferBot and
  stakeholders.
- **Structured trace data** — replayable inputs, outputs, and
  decision records for every agentic operation, enabling
  component-level evaluability. The trace format is an open
  design question.

**Interfaces consumed by the Job Site:**

- WMS Adapter API — lifecycle transitions, finding ledger,
  conformance evidence
- `ears-manager` — requirement resolution and validation at
  the specification commit
- Project repository — source checkout, integration branches,
  merge to main
- Validation Rules — pre-write transition checks

---

## Drafting Table Boundary

The Drafting Table is where the human sits down with an AI
agent to define what ProtoBot will build. This section
describes the boundary in enough detail to guide the downstream
design decisions for issues #28–#34: user experience,
OpenCode-plus-skill strawman, `ears-manager` integration, WMS
Adapter integration, Validation Rules integration, and
Git/project-repository integration.

### User-facing interaction surface

The Drafting Table supports three interaction phases:

1. **Sketching** — the user describes what they want to build.
   The agent structures the intent into a Vision statement and
   an Architecture (this document's own format). The user
   approves or revises each level.

2. **Backlog refinement** — the agent and a human maintainer
   refine requests: find duplicates, classify each as
   _undefined_, _changes_, or _contradicts_ existing EARS,
   and identify affected interfaces and scopes. The
   classification determines the pipeline entry point
   (Dimensioning for _undefined_ and _changes_; Building
   directly for _contradicts_ / true bugs). See
   [User Interaction Flow — Request Backlog and
   Refinement](architecture/user-interaction-flow.md#request-backlog-and-refinement).

3. **Dimensioning** — the user and agent produce precise EARS
   requirements for each interface identified in the
   Architecture. The agent proposes requirements, surfaces
   gaps, runs impact analysis, and the user reviews the
   resulting change set.

In all phases, the user's role is to provide intent, answer
clarifying questions, and approve artifacts. The agent's role
is to structure, suggest, validate, and persist through
governed tool integrations.

On session start, the TUI Drafting Table pulls blocked work
items from the WMS and presents them so the user can resolve
them (add a requirement or approve an out-of-scope
declaration). The Web Drafting Table pushes these
notifications via a persistent browser connection or an
external channel (Q2).

### OpenCode-plus-skill strawman

The first TUI Drafting Table implementation loads the
Specification Toolkit as OpenCode skills. This strawman
defines the initial integration model:

```text
┌─────────────────────────────────────┐
│  OpenCode (agent harness)           │
│                                     │
│  ┌───────────────────────────────┐  │
│  │  Specification Toolkit        │  │
│  │  (skills + prompts + tools)   │  │
│  └───────┬──────────┬────────────┘  │
│          │          │               │
│    ┌─────┴───┐ ┌────┴────────┐     │
│    │ears-mgr │ │ WMS Adapter │     │
│    │  (CLI)  │ │  (API/MCP)  │     │
│    └─────────┘ └─────────────┘     │
│                                     │
│  ┌───────────────────────────────┐  │
│  │  Git (project repository)     │  │
│  └───────────────────────────────┘  │
└─────────────────────────────────────┘
```

- **Skills** provide structured instructions for each phase:
  how to conduct Sketching, how to conduct Dimensioning, how
  to handle each EARS pattern type, and how to surface gaps.
- **Tool definitions** (MCP tool schemas) provide the agent
  with governed access to `ears-manager` and the WMS Adapter.
  The agent invokes tools through the harness's standard tool
  execution mechanism.
- **Prompts** provide system context: EARS pattern definitions,
  the interface-type taxonomy, gap-closing heuristics, and
  the specification hierarchy.
- **Git operations** use the harness's existing file and shell
  tools to create branches, commit artifacts produced through
  `ears-manager`, and open PRs.

The Web Drafting Table replaces OpenCode with a hosted agent
runtime but loads the same Specification Toolkit.

### Governed tool integrations

The Drafting Table agent interacts with external systems
exclusively through governed tools defined by the
Specification Toolkit. The agent never edits specification
files directly — all reads and writes go through
`ears-manager`.

**Enforcement:** This rule is enforced structurally, not by
prompting alone. The mandatory enforcement layers are:

- **Branch protection on `main`** — prevents direct pushes;
  all changes require a reviewed PR.
- **`ears-manager check` as a CI merge gate** — validates
  specification well-formedness, required metadata, and
  referential integrity before merge.
- **Path ownership in CI** — rejects edits by a component or
  Worker outside its owned or allowlisted paths (CODEOWNERS
  and required reviews).

Optional early enforcement: harness permission rules that
deny writes under specification paths at the tool level,
catching violations before they reach CI.

**Tool governance principles:**

- **All specification mutations go through `ears-manager`.**
  The Drafting Table creates, reads, updates, and retires
  requirements and interfaces via `ears-manager` subcommands.
  The Drafting Table never writes specification files directly
  to the working tree.
- **All work-item mutations go through the WMS Adapter API.**
  The Drafting Table reads work-item state for display, queries
  blocked items, and submits reviewed resolutions. Validation
  Rules run at the API boundary before any mutation is applied.
- **Git operations are explicit.** The Drafting Table creates
  branches, commits `ears-manager` outputs, and opens PRs. It
  does not push directly to main in multi-player mode.

**Tool inventory:**

| Tool | System | Operations |
| --- | --- | --- |
| `ears-manager requirement *` | Specification store | Add, list, show, update, retire requirements |
| `ears-manager interface *` | Specification store | Add, list, show, update interfaces |
| `ears-manager artifact put/get/list` | Specification store | Manage Vision, Architecture, IDL artifacts |
| `ears-manager change-set *` | Specification store | Create, list, show, compare, update change sets |
| `ears-manager impact` | Specification store | Generate applicable-requirement candidates |
| `ears-manager check` | Specification store | Validate spec well-formedness |
| WMS query | WMS Adapter | Read work-item state, query blocked items |
| WMS request create/refine/link | WMS Adapter | Create, refine, and link backlog requests |
| WMS submit resolution | WMS Adapter | Submit reviewed change-set references for blocked items |
| Git branch/commit/PR | Project repository | Create branches, commit artifacts, open PRs |

### Data and control flow

The following diagram shows how data moves through the
Drafting Table during a Dimensioning session:

```text
User ──── intent / approval ──────→ Agent
Agent ──── clarifying questions ──→ User
Agent ──── requirement add ───────→ Spec Store (working tree)
Agent ──── ears-manager impact ───→ Spec Store (candidate list)
Agent ──── WMS query ─────────────→ WMS Backend (blocked items)
Agent ──── present change set ────→ User
User ──── approve change set ─────→ Agent
Agent ──── git commit / PR ───────→ Project Repository
Reviewer ── merge PR ─────────────→ main (approved Schematic)
Materializer ── create work item ─→ WMS Backend
```

**Control flow invariants:**

- The user approves every specification delta before it lands
  on main. The agent proposes; the user decides.
- `ears-manager` validates every mutation before it is written.
  The agent cannot bypass formatting, metadata, or referential
  integrity checks.
- The WMS Adapter validates every lifecycle transition at the
  write boundary using shared Validation Rules. The agent
  cannot move a work item to an invalid state.
- Materialization is idempotent: an approved change set
  produces exactly one build work item (or returns the
  existing one).

---

## Persistent State

Persistent state outlives any single run and requires its own
interface contract. ProtoBot has six categories of persistent
state. Each store carries a schema version (in
`.protobot/project.yaml`); when a tool encounters data at a
version newer than its own, it refuses to operate rather than
silently corrupting state. Forward migration happens through a
reviewed change set, not automatically.

### Specification store (Git)

All registered specification artifacts — Vision, Architecture,
EARS requirements, interface definitions, change-set manifests
— live in the project's Git repository. `ears-manager` is the
exclusive write gate. The storage format is abstracted behind
`ears-manager`'s CLI; callers never parse specification files
directly.

**Where it lives:** The project repository, under paths
registered in `.protobot/project.yaml`.

**Schema owner:** `ears-manager`. On version mismatch,
`ears-manager` refuses and reports the expected version.

### Work-item lifecycle state (WMS backend)

Build work-item state — pipeline phase, blocked/ready status,
owner/lease, dependencies, fencing tokens, finding-ledger
events, and conformance-evidence references — lives in the
WMS backend (GitHub Issues, Jira, Beads, etc.) accessed
through the WMS Adapter API.

**Where it lives:** The configured WMS backend, one per
project.

**Schema owner:** WMS Adapter. On version mismatch, the
adapter refuses the operation.

### Project configuration (`.protobot/`)

Project identity, artifact paths, projection manifests,
policy, Kit locks, test catalog, and attestation metadata live
in the `.protobot/` control namespace in the project
repository.

**Where it lives:** The project repository under `.protobot/`.

**Schema owner:** `ears-manager` (for `project.yaml` and
specification paths) and the Job Site (for the test catalog
and attestation paths). On version mismatch, the owning tool
refuses.

### Claim coordinator

The claim coordinator provides atomic compare-and-swap
semantics for work-item claims and finding-ledger outbox
operations on backends that lack native conditional updates
(GitHub, GitLab, Jira, Trello). It holds leases and fencing
tokens that determine whether two Job Sites can claim the
same item.

**Where it lives:**

| Mode | Location |
| --- | --- |
| Single-player | Local process or in-memory (single claimant) |
| Multi-player | Shared durable store alongside the WMS backend |
| Web | Same as multi-player |

**Schema owner:** WMS Adapter. Reconciliation with the
backend is the adapter's responsibility.

### Evidence and artifact store

Content-addressed raw evidence (integration artifacts,
inspection reports) and large demo artifacts (video, images)
referenced by digest. The conformance evidence that the
completion envelope references lives here; losing it breaks
the evidence chain.

**Where it lives:**

| Mode | Location |
| --- | --- |
| Single-player | Local filesystem under `.protobot/attestations/` |
| Multi-player | Approved object/OCI storage, referenced by digest |
| Web | Same as multi-player |

**Schema owner:** Job Site. Retention policy is a project
policy decision in `.protobot/policy.yaml`.

### Deployment-level registry

A hosted ProtoBot deployment serves many projects and adapter
configurations. The deployment-level registry tracks project
registrations, adapter configurations, and shared
infrastructure state. It lives outside any single project's
`.protobot/`.

**Where it lives:** The deployment's own configuration store,
outside the project repository.

**Schema owner:** The deployment operator.

See [System Components — Content Storage
Model](architecture/components.md#content-storage-model) for the full
content model.

---

## Environmental Constraints

Environmental constraints dictate implementation choices
without being external interfaces themselves. They belong in
the Architecture so the Building phase does not make
incompatible decisions.

| Constraint | Rationale |
| --- | --- |
| `ears-manager`: Go, static binary | Zero runtime dependencies across all deployment contexts — dev containers, CI runners, sandboxes, local machines. |
| First Drafting Table harness: OpenCode | OpenCode's model-provider flexibility and skill system provide the fastest path to a working TUI Drafting Table. |
| First Job Site backend: Fullsend / OpenShell | Fullsend is the closest peer in the GE Agentic SDLC Working Group. OpenShell provides kernel-enforced sandboxing. Fallback: a portable rootless-OCI/microVM profile for platforms where OpenShell is unavailable (e.g., `restricted-v2` — no `CAP_SYS_ADMIN`). |
| Prototype outputs: UBI + Hummingbird images | Lightweight, fast-turnaround demo builds on Red Hat certified base images. |
| Authentication: OAuth 2.1 | ESS-required baseline. MCP/API servers terminate inbound client tokens and use server-owned credentials downstream (Alcove Bridge/Gate pattern). |
| Credential isolation: Bridge/Gate pattern | Agent processes never see real credentials. Bridges pre-fetch tokens; Gates inject them at the network boundary. |
| Evaluability from day one | Every agentic component records replayable inputs/outputs and structured traces, enabling component-level testing and measurable improvement. This is an architectural constraint, not retrofitted. |
| No CRDs or operators on Managed Platform Plus (IdeaBot ADR-0021) | Inherited constraint. The Web Drafting Table needs a deployment target that does not depend on CRDs or operators. |
| Identity: Red Hat SSO / Keycloak (IdeaBot ADR-0012) | Inherited constraint. The OAuth 2.1 issuer for hosted modes is Red Hat SSO (Keycloak). |
| Prototype scope: not every prototype is a container | The set of supported output types will expand over time. Initial types should support bootstrapping (CLI tools, Go binaries). |

---

## Interface Specification Approach

The table below maps each interface type to a specification
approach, following the interface-type taxonomy defined in the
[Specification
Hierarchy](architecture/user-interaction-flow.md#specification-hierarchy).
Interfaces that share a type share the same specification
approach.

| Interface | Type | Specification approach |
| --- | --- | --- |
| `ears-manager` CLI | CLI | `usage` (jdx.dev) / docopt / `wasi:cli` — [evaluation pending (Q18)](architecture/open-questions.md#q18-cli-interface-spec-evaluation) |
| WMS Adapter API | Network service | Smithy or OpenAPI |
| Specification Toolkit | Agent skill package | Skill manifest + MCP tool schemas (JSON Schema) |
| Validation Rules | Linkable library or declarative rule set | WIT or declarative state-machine schema _(open — see [components.md](architecture/components.md#validation-rules))_ |
| Drafting Table (TUI) | REPL | Specification Toolkit skills and prompts define the interaction protocol |
| Drafting Table (Web) | Web GUI | Open gap — Web GUI specification approach not yet established |
| Project repository | Persistent state | JSON Schema for each `.protobot/` file + `ears-manager` CLI contract |
| Kit source | Package source | Versioned import manifest with source/version/digest/provenance |

Interface specifications are produced at level 3 of the
specification hierarchy. Each interface specification defines
its stable external contract boundary using the approach above,
and individual EARS requirements (level 4) are written against
that contract.

---

## Related Documents

- [Overview](architecture/overview.md) — What ProtoBot is, guiding principles,
  and workflow summary
- [System Components](architecture/components.md) — Component architecture,
  interfaces, and cross-cutting concerns
- [User Interaction Flow](architecture/user-interaction-flow.md) — Phase details
  and sequence diagrams
- [Drafting Table UX](architecture/drafting-table-ux.md) — Stable
  interaction contract for the first local Drafting Table
- [Open Design Questions](architecture/open-questions.md) — Unresolved
  design questions across all areas
- [Related Work](architecture/related-work.md) — Red Hat internal projects,
  external factory projects, and lessons learned
