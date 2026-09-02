# ProtoBot: Initial Sketch

> Approved Sketch — the Vision and Architecture artifact from
> the Sketching phase.

**Contents:**

- [Vision](#vision)
- [Architecture](#architecture)
- [External-Interface Inventory](#external-interface-inventory)
- [Deferred Decisions and Follow-Up Work](#deferred-decisions-and-follow-up-work)
- [Detailed Design Documents](#detailed-design-documents)

---

## Vision

### What is ProtoBot?

ProtoBot takes requirement specifications and generates working
prototypes suitable for customer demonstrations, not final
products. It is the second tool in the Hermes pipeline,
following IdeaBot (idea capture and research) and preceding
TransferBot (transfer of specifications and test expectations
to product teams).

The pipeline:

```text
IdeaBot → ProtoBot → TransferBot
(idea)    (prototype)  (product transfer)
```

ProtoBot's input is a set of structured requirements in EARS
(Easy Approach to Requirements Syntax) format. Its output is a
working, tested, inspected prototype plus demonstration
artifacts.

### Who is it for?

ProtoBot serves Red Hat teams that need to validate ideas
quickly. The primary workflow:

1. A stakeholder uses IdeaBot to research and refine an idea.
2. ProtoBot turns that idea into a working prototype with
   enough quality for customer demonstrations.
3. TransferBot hands the prototype's specifications and test
   expectations (not the code) to a product team for real
   implementation.

### Why does it exist?

There is a speed gap between validating an idea and committing
to a full production implementation. Ideas stall when the cost
of building a convincing proof of concept is too high relative
to the confidence in the idea. ProtoBot closes that gap by
making prototype generation fast and specification-driven, so
teams can validate with real, working software instead of
slide decks and mockups.

### Role in the Hermes pipeline

ProtoBot sits between IdeaBot and TransferBot. Each tool owns
a distinct phase of the idea-to-product lifecycle:

- **IdeaBot** captures ideas, conducts research, refines scope,
  and produces structured research artifacts. Its output seeds
  ProtoBot's Sketching phase.
- **ProtoBot** turns those ideas into working prototypes. Its
  output is working code, a complete specification history, and
  demonstration artifacts.
- **TransferBot** takes ProtoBot's specifications and test
  expectations — not the prototype code — and transfers them
  to a product team for production implementation.

The initial IdeaBot-to-ProtoBot handoff is manual: IdeaBot's
research artifacts are provided in the initial Sketching session
to seed the project. Automated integration can follow later.

### What is a successful prototype?

A successful prototype is:

- **Working software.** It runs, accepts input, and produces
  observable output through its declared interfaces. It is not
  a mockup, wireframe, or simulation.
- **Tested against its specification.** Every approved EARS
  requirement has corresponding test evidence. The tests were
  generated independently from the code (dual-model isolation)
  and verified by independent Inspector agents.
- **Inspected.** Security, test completeness, code quality, and
  specification conformance have been reviewed by autonomous
  Inspectors. Findings are resolved or explicitly dispositioned.
- **Demonstrable.** It includes demonstration artifacts
  (screenshots, recordings, self-verifying documents) that show
  the prototype working without requiring the reviewer to build
  and run it.
- **Specification-complete.** The full specification history —
  Vision, Architecture, EARS requirements, change sets, and
  inspection reports — is preserved in the project repository
  and available for transfer.

A successful prototype is _not_ a partial implementation that
"shows the idea." It is a complete, verified realization of the
approved specification, bounded by the scope of that
specification rather than by implementation shortcuts.

### Boundary between prototype and production

Prototypes are disposable by design. Their purpose is to
validate the idea and gather feedback, not to ship to
production. The boundary is:

- **What transfers:** the specification (EARS requirements,
  interface definitions, test expectations) and the conformance
  evidence. These are the durable artifacts.
- **What does not transfer:** the prototype code. Product teams
  reimplement from the specification using their own
  architecture, language choices, and production standards.
- **Why the code is disposable:** prototype code is generated to
  satisfy the specification as cheaply as possible. It is not
  optimized for maintainability, operational excellence, or
  long-term evolution. Treating it as production code would
  undermine the speed advantage that makes prototyping
  worthwhile.
- **The specification is the product.** If the prototype is
  wrong, we fix the specification and regenerate. If we need to
  start over, we discard everything except the specification
  and regenerate. The specification is sufficient to define the
  system; everything else is a regenerable artifact.

### Guiding principles

**The specification is the product.** We don't write code, and
we don't read code. We don't write tests, and we don't read
tests. Everything is guided by the specification. The human's
involvement is defining _what_ the system should do — precisely,
unambiguously, in structured EARS requirements. Everything after
that is fully autonomous.

**Observable behavior is all that matters.** Requirements define
the system's interaction with the external world through its
interfaces. Internal implementation is an autonomous agent's
concern. State is external to the system (it outlives any single
run), so implementations can be upgraded or replaced without
changing the specification.

**Enforce constraints structurally, not through trust.** The
architecture assumes agents will take the fastest path to "done"
and designs the environment so that path is also the correct
one. Sandbox-level constraints, dual-model isolation, and
independent inspection replace prompting-based trust.

**Build for evaluability from day one.** Every agentic component
must be independently evaluable. If you can't feed it a known
input and measure its output, you can't improve it.

### Non-goals

The following are explicitly out of scope for ProtoBot:

- **Production software delivery.** ProtoBot does not produce
  production-ready code, deployment infrastructure, or
  operational runbooks. TransferBot and product teams own that.
- **Long-term code maintenance.** Prototype code is not
  maintained across releases. Specification changes produce new
  prototypes, not incremental patches to existing ones (though
  incremental development within a prototype's lifecycle is
  supported).
- **General-purpose coding agent.** ProtoBot is not a
  general-purpose code generation tool. It generates prototypes
  from structured specifications using a specific methodology.
  Unstructured "build me X" requests are not supported.
- **Replacing human judgment on _what_ to build.** ProtoBot
  automates the _how_ (code and test generation) after the human
  has specified the _what_ (EARS requirements). It does not
  decide product direction, prioritize features, or resolve
  business trade-offs.

### Bootstrapping strategy

ProtoBot will be used to build ProtoBot. The interactive work
starts locally in OpenCode with draft Specification Toolkit
skills. The first autonomous execution targets are Fullsend and
direct OpenShell adapters.

The initial prototype output types should be chosen to support
this bootstrapping — the priority is whatever ProtoBot's own
components need (CLI tools, Go binaries, etc.).

### Assumptions

- **EARS is sufficient for prototype-grade specifications.**
  EARS provides enough structure for agents to generate tests
  and code, while remaining accessible to non-engineers. If a
  specification gap is found during autonomous execution, it
  blocks and escalates to the user rather than guessing.
- **Dual-model isolation prevents oracle gaming.** Generating
  tests and code from the same specification using separate
  models that cannot see each other's output produces
  meaningful correctness evidence. This is validated by
  IdeaBot's architecture portability experiment and external
  reimplementation studies.
- **The spec-as-source principle is viable.** Discarding and
  regenerating code from the specification is practical at
  prototype scale. This assumption breaks at production scale,
  which is why TransferBot transfers specifications rather than
  code.

---

## Architecture

### Design approach

The Architecture describes the system's **external interfaces**
— its boundary with the outside world. Internal decomposition
(services, modules, components) is an implementation detail left
to the Building phase. The user specifies _what the system looks
like from the outside_, not how it's structured internally.

"External" means contractually stable, not just user-facing. An
external interface is any boundary where an independent party
could write an implementation against the contract. This
includes pluggable component boundaries (the WMS interface, the
execution backend) even when they feel "internal."

Persistent state is an external interface. State exists outside
the system, outlives any single run, and requires an
upgrade/rollback path. Environmental constraints that dictate
internal choices (e.g., required frameworks or base images) are
captured as environmental requirements, distinct from interface
definitions.

### Component overview

ProtoBot has seven primary logical components:

```text
 ┌───────────────────────────────────────────────────┐
 │                    ProtoBot                       │
 │                                                   │
 │  ┌──────────────┐  ┌─────────────────────────┐   │
 │  │   Drafting    │  │  Specification Toolkit  │   │
 │  │    Table      │◄─┤  (skills/tools/prompts) │   │
 │  │  (Web / TUI)  │  └─────────────────────────┘   │
 │  └──────┬───────┘                                 │
 │         │            ┌──────┐                     │
 │         │            │ Kits │ (versioned imports)  │
 │         │            └──────┘                     │
 │  ┌──────▼───────┐  ┌──────────────────┐           │
 │  │ ears-manager  │  │ Validation Rules │           │
 │  │  (spec CLI)   │  │ (shared logic)   │           │
 │  └──────┬───────┘  └────────┬─────────┘           │
 │         │                   │                     │
 │  ┌──────▼───────────────────▼─────┐               │
 │  │         WMS Adapter            │               │
 │  │  (pluggable backend layer)     │               │
 │  └──────┬─────────────────────────┘               │
 │         │                                         │
 │  ┌──────▼───────┐                                 │
 │  │   Job Site   │  (autonomous execution engine)  │
 │  │  [handoff    │                                 │
 │  │   boundary]  │                                 │
 │  └──────────────┘                                 │
 └───────────────────────────────────────────────────┘
           │                    │
    ┌──────▼──────┐    ┌───────▼───────┐
    │  Project    │    │  WMS Backend  │
    │  Git Repo   │    │  (Issues/     │
    │             │    │   Jira/Beads) │
    └─────────────┘    └───────────────┘
```

1. **Drafting Table** — The environment where the human and an
   AI agent collaborate during Sketching and Dimensioning.
2. **Specification Toolkit** — Portable skills, tools, and
   prompts shared across all Drafting Table implementations.
3. **Kits** — Versioned, reusable imports for standard concerns.
4. **`ears-manager`** — CLI tool and single write gate for the
   specification store.
5. **WMS Adapter** — Pluggable integration layer over the
   chosen work management backend.
6. **Validation Rules** — Shared domain logic for work-item
   state transitions.
7. **Job Site** — Autonomous execution engine for Building and
   Inspecting phases. (Internal design deferred; see
   [Deferred Decisions](#deferred-decisions-and-follow-up-work).)

### Drafting Table

The Drafting Table is where the human sits down with an AI
agent to do Sketching (defining Vision and Architecture) and
Dimensioning (producing EARS requirements). It also surfaces
blocked work items from the Job Site and lets the user
resolve them.

A Drafting Table implementation consists of a **frontend**
(what the user sees) and an **agent harness** (what runs the
model and executes tools). Different implementations provide
both, but all load the same Specification Toolkit and talk to
the same WMS Adapter API.

#### Reference implementations

**Web Drafting Table** — A browser-based UI with a hosted agent
runtime. Provides push notifications for blocked work items,
persistent sessions, and no local setup.

**TUI Drafting Table** — A terminal-based interface using an
existing coding agent (OpenCode, Claude Code) as the harness.
The Drafting Table is the Specification Toolkit loaded into the
harness plus WMS connectivity. Blocked-work notifications use a
pull model: the harness checks the WMS on session start and
presents any items needing attention.

#### OpenCode-plus-skill strawman

The first Drafting Table implementation uses **OpenCode** as the
agent harness with the Specification Toolkit loaded as skills.
This gives ProtoBot an immediately functional interactive
environment without building a custom frontend or agent runtime.

The Specification Toolkit skills encode:

- How to conduct a Sketching session (elicit Vision, enumerate
  interfaces, identify interface types).
- How to conduct a Dimensioning session (translate Architecture
  into EARS requirements, surface spec gaps, handle each EARS
  pattern type).
- MCP tool schemas for the WMS Adapter and `ears-manager`.
- System prompts, EARS pattern definitions, the interface-type
  taxonomy, and gap-closing heuristics.

OpenCode's model-provider flexibility applies directly: the
Drafting Table can use any supported provider.

#### Interfaces

- **To WMS Adapter:** reads work-item state, queries blocked
  items, submits reviewed resolutions.
- **To `ears-manager`:** reads and writes every registered
  specification artifact. The Drafting Table never edits spec
  files directly.
- **To project repo:** creates branches, commits, and opens PRs
  containing specification artifacts.
- **To user:** conversational interface for Sketching and
  Dimensioning; displays Job Site status; surfaces blocked
  work items.
- **From IdeaBot:** receives research artifacts as input to
  Sketching (handoff format is an open question).

### Specification Toolkit

The Specification Toolkit is the portable domain logic that any
Drafting Table implementation loads to become capable of driving
Sketching and Dimensioning. It is not a running service — it is
a shared asset consumed by the agent harness.

#### Contents

- **Skills** — structured instructions for the agent: how to
  run Sketching and Dimensioning sessions.
- **Tool definitions** — MCP tool schemas or API client code
  for the WMS Adapter and `ears-manager`.
- **Prompts** — system prompts, templates, EARS pattern
  definitions, the interface-type taxonomy, gap-closing
  heuristics, and the specification hierarchy.

#### Design principles

- **Harness-agnostic.** Works in any compatible agent harness.
  No dependencies on harness-specific APIs beyond standard tool
  execution and prompt loading.
- **Two runtime dependencies: WMS Adapter and `ears-manager`.**
  Work-item state lives in the WMS; specification content lives
  in the project's git repo via `ears-manager`.
- **Versioned and testable.** Changes to EARS patterns,
  gap-closing heuristics, or the specification hierarchy are
  testable independently of any Drafting Table implementation.

### Kits

A **Kit** is a reusable, versioned import for a standard project
concern (e.g., Python conventions, Kubernetes deployment, CI).
Currently understood Kit contents:

- **EARS/interface content:** proposed interface definitions and
  requirements for the concern.
- **Inspector definitions:** specialist Inspectors and activation
  rules relevant to that concern.

Importing a Kit never changes an approved project implicitly.
Content becomes a normal proposed change set with provenance and
human review. Exact package format and additional capabilities
remain open.

### `ears-manager`

`ears-manager` is a CLI tool that serves as the programmatic
interface to the specification store. It abstracts the underlying
file format, manages change sets, and enforces EARS methodology
rules deterministically.

#### Callers

- **Agents** (via the Specification Toolkit) during Dimensioning
  and during Job Site execution.
- **CI** — `ears-manager check` runs on every branch push to
  validate spec well-formedness.
- **Humans** — direct CLI use for inspection or changes without
  involving an agent.

#### Key properties

- **Deterministic, not AI-driven.** Conventional code — a
  linter, validator, and CRUD tool — not an LLM. The agent
  decides what requirements to write; `ears-manager` ensures
  they are well-formed.
- **Statically linked Go binary.** Zero runtime dependencies.
  Works identically across all deployment contexts.
- **Format-agnostic interface.** Callers interact via
  subcommands, not by reading/writing files directly. The
  storage format can change without breaking callers.
- **Single write gate.** All registered specification
  reads/writes go through `ears-manager`. No other component
  modifies spec files directly.

#### What it validates

- EARS formatting (each requirement matches a pattern).
- Required metadata (stable ID, applicability selectors,
  pattern type, provenance, verification mode).
- Referential integrity (requirement-to-interface references,
  explicit relationships, change-set references).
- Artifact governance (registered path, digest, owner, and
  validator for every specification artifact).
- File format consistency.

#### Subcommands

| Subcommand | Purpose |
| --- | --- |
| `check` | Validate all spec files for CI gates |
| `add requirement` | Add a new EARS requirement |
| `add interface` | Register a new interface |
| `artifact put` | Create/update a registered artifact |
| `artifact get` | Read a registered artifact |
| `change-set` | Create, inspect, update a change set |
| `compare` | Compare a change set with the Schematic |
| `impact` | Produce applicable requirement candidates |
| `list` | List requirements, interfaces, or change sets |
| `show` | Show a single entity by ID |
| `update` | Modify an existing entity in a change set |
| `retire` | Retire a requirement in a change set |

### WMS Adapter

The WMS Adapter is a thin, pluggable integration layer between
ProtoBot and the user's chosen work management backend. It
translates ProtoBot's build work-item model to/from the
backend's native concepts and handles durable, atomic
operations. Shared Validation Rules run at its API boundary.

One adapter implementation is active per project.

#### Supported backends

| Backend | Native concept |
| --- | --- |
| GitHub Issues/Projects | One issue per build work item |
| GitLab | One issue per build work item |
| Jira | One issue per build work item |
| Beads | One bead per build work item |
| Trello | One card per build work item |

Each backend must provide atomic work-item claim semantics
(conditional state update), either natively or through an
external coordinator.

#### Responsibilities

- Store and retrieve work-item lifecycle state.
- Store and retrieve request backlog metadata.
- Persist materialized work items idempotently.
- Transition work items atomically with expected-state checks.
- Translate between ProtoBot's model and the backend's native
  model.
- Expose a uniform API so callers don't know which backend is
  in use.

The adapter does **not** store work-item content (specs, code,
tests), determine pipeline entry points, decide work-item
readiness, or push work to the Job Site.

### Validation Rules

Validation Rules are the domain logic that enforces
well-formedness on work items and state transitions. They are
a shared library or declarative rule set — not a running
service.

**Scope boundary with `ears-manager`:**

- **`ears-manager`** handles _specification-level_ validation:
  EARS formatting, metadata, referential integrity.
- **Validation Rules** handle _lifecycle_ validation: work-item
  state transitions, pipeline entry points, readiness checks.

The Drafting Table and Job Site apply Validation Rules before
writes for early feedback. The WMS API boundary then atomically
verifies the expected state and allowed transition before
mutating the backend.

### Content storage model

Project content lives in the **project's git repo**, not in the
issue tracker. Approved specifications live on main. Each build
work item gets its own branch for code and tests; the issue
tracker holds work-item lifecycle state and immutable Git
references.

#### Required control namespace

ProtoBot mandates a `.protobot/` control namespace:

| Path | Purpose |
| --- | --- |
| `.protobot/project.yaml` | Project identity and artifact paths |
| `.protobot/projection.yaml` | Worker projection path classes |
| `.protobot/policy.yaml` | Inspector roster and policy |
| `.protobot/kits.lock` | Kit provenance locks |
| `.protobot/change-sets/` | Approved change-set manifests |
| `.protobot/test-catalog.jsonl` | Stable test IDs and metadata |
| `.protobot/attestations/` | Finding snapshots and evidence |

Source and test trees follow project conventions. The committed
projection manifest is authoritative. CI rejects edits outside
a component's owned paths.

#### Key properties

- Work-item state tracks progress; requirements have no workflow
  markers.
- Impact is approved with intent: each change set records
  changed and applicable requirement sets.
- Conformance is commit-scoped evidence, not a global marker.
- Sketch updates follow the same branch-review-merge lifecycle
  as any other change.
- CI validates branches (spec format, well-formedness,
  consistency).
- Concurrent work items don't conflict until merge.

### Job Site (handoff boundary)

The Job Site is the autonomous execution engine that runs the
Building and Inspecting phases. It pulls ready work items from
the WMS, generates tests and code concurrently from approved
EARS requirements using dual-model isolation, runs independent
Inspector agents, and produces working, tested, inspected
prototypes.

At this level, the Job Site:

- **Accepts** build work items in `ready-for-building` state,
  each carrying a frozen specification commit, changed and
  applicable requirement IDs, and a complete work-item
  contract.
- **Produces** working code, passing tests, sealed inspection
  reports, conformance evidence, and demonstration artifacts.
- **Merges** completed work to main and records the resulting
  merge commit in the WMS.
- **Escalates** undefined behavior or omitted applicable
  requirements back to the Drafting Table by blocking the work
  item and creating a linked issue.

The Job Site's internal components — control plane, Worker
topology, Triage mechanism, projection system, mutation
testing, Inspector orchestration, Finding Ledger, and
Fullsend/OpenShell integration — are **deferred** from this
Sketch. Their design is tracked as follow-up work. See
[Deferred Decisions](#deferred-decisions-and-follow-up-work).

### Environmental constraints

- **Drafting Table harness:** OpenCode is the first harness,
  with the Specification Toolkit loaded as skills. OpenCode's
  model-provider flexibility applies directly.
- **Autonomous execution:** Fullsend is the first Job Site
  backend, using OpenShell-based sandboxing. A direct OpenShell
  adapter is the fallback. A portable rootless-OCI/microVM
  containment profile is the independent sandbox fallback.
- **Deployment modes:** Both single-player (local, no cluster)
  and multi-player (GitHub PR workflow) are first-class. The
  difference is ceremony, not architecture.
- **Prototype outputs:** Built on UBI + Hummingbird images
  initially. Not every prototype is a container; output types
  will expand.
- **`ears-manager`:** Statically linked Go binary with zero
  runtime dependencies.
- **Compliance:** OAuth 2.1 baseline per ESS requirements.
  AIA/HU-02 compliance gate at PR merge time (human approves
  specifications; everything after is autonomous execution of
  approved intent). Confirmation of HU-02 satisfaction for
  autonomous code merge is an open question.

### Pluggable boundaries

ProtoBot is designed with explicit pluggable boundaries to
support swapping implementations without architectural changes:

- **Drafting Table implementations** — web UI, TUI (OpenCode,
  Claude Code), or future harnesses. All share the Specification
  Toolkit and WMS Adapter API.
- **WMS backends** — GitHub Issues, GitLab, Jira, Beads,
  Trello. One active per project; the adapter API is the stable
  contract.
- **Execution backends** — Fullsend, direct OpenShell, or the
  portable OCI/microVM profile. All must pass the same sandbox
  acceptance suite.
- **Model providers** — inherited from the harness (Drafting
  Table) or configured per backend (Job Site). No architectural
  coupling to a specific provider.

### Persistent state

ProtoBot has two categories of persistent state:

| Category | Where it lives | What it contains |
| --- | --- | --- |
| Specification content | Project git repo (main branch) | Vision, Architecture, EARS requirements, interface definitions, change-set manifests, conformance evidence |
| Work-item lifecycle | WMS backend (Issues/Jira/Beads) | Pipeline phase, blocked status, owner/lease, dependencies, Git references, finding events |

ProtoBot mandates no other persistent stores. The
`.protobot/` control namespace lives in the git repo.
Configuration never contains credentials. The Drafting Table
and Job Site are stateless beyond their runtime sessions —
all durable state is in git or the WMS.

---

## External-Interface Inventory

This inventory lists every external interface that ProtoBot
exposes, along with its type and specification approach.

### User-facing interfaces

| Interface | Type | Spec approach |
| --- | --- | --- |
| Drafting Table (Web) | Web GUI | _(open gap — no IDL)_ |
| Drafting Table (TUI) | CLI/REPL | OpenCode skill system |
| Demonstration artifacts | File-based | Manifest schema |

### Programmatic interfaces

| Interface | Type | Spec approach |
| --- | --- | --- |
| `ears-manager` CLI | CLI | `usage` / docopt _(needs evaluation)_ |
| WMS Adapter API | API (internal) | Smithy / OpenAPI |
| Specification Toolkit tools | MCP tools | MCP tool schemas |
| Validation Rules | Library | Code/schema _(needs evaluation)_ |
| Kit import interface | File + API | _(format open)_ |

### Data/state interfaces

| Interface | Type | Spec approach |
| --- | --- | --- |
| `.protobot/` control namespace | File-based | YAML/JSONL schemas |
| Specification store (via `ears-manager`) | File-based | `ears-manager` format spec |
| Project repository (Git) | Git | Branch naming + merge conventions |
| WMS backend integration | Backend-specific | Adapter per backend |

### Pluggable component boundaries

| Boundary | Type | Spec approach |
| --- | --- | --- |
| Drafting Table ↔ Specification Toolkit | Skill/tool loading | Harness skill format |
| Drafting Table ↔ WMS Adapter | API | Smithy / OpenAPI |
| Drafting Table ↔ `ears-manager` | CLI | CLI contract |
| Job Site ↔ WMS Adapter | API | Smithy / OpenAPI |
| Job Site ↔ `ears-manager` | CLI | CLI contract |
| Job Site ↔ execution backend | Sandbox contract | Acceptance test suite |
| Job Site ↔ project repo | Git | Branch + merge conventions |

### Inter-pipeline interfaces

| Interface | Type | Spec approach |
| --- | --- | --- |
| IdeaBot → ProtoBot (Sketching input) | File handoff | _(format open — Q4)_ |
| ProtoBot → TransferBot (spec transfer) | File + git | Specification format |

---

## Deferred Decisions and Follow-Up Work

The following are explicitly deferred from this Sketch and
tracked as follow-up design work.

### Deferred from Architecture

- **Job Site internal design.** The Job Site's control plane,
  Worker topology, Triage mechanism, projection system,
  finding Ledger, mutation testing pipeline, Inspector
  orchestration, and Fullsend/OpenShell integration depth are
  all deferred. The existing
  [detailed design documents](#detailed-design-documents)
  contain substantial draft material on these topics, but they
  are not part of this approved Sketch.
- **Individual EARS requirements.** This Sketch defines the
  Architecture — the external interfaces and their types.
  The actual EARS requirements for each interface are produced
  during Dimensioning (the next phase).
- **Interface-level specifications.** Detailed specifications
  for each interface (OpenAPI schemas, CLI usage definitions,
  MCP tool schemas) are produced during Dimensioning, not
  Sketching.

### Open design questions

These questions are captured rather than silently resolved. See
[Open Design Questions](architecture/open-questions.md) for the
full list with context.

**Interactive phase:**

- Q1: Spec gap surfacing UX during Dimensioning.
- Q2: Async escalation notification channels.
- Q3: Multi-interface orchestration during Dimensioning.
- Q4: IdeaBot handoff format.
- Q5: Kit package format and future capabilities.

**Specification storage:**

- Q7: Requirements storage format (JSONL vs. alternatives).

**Building phase:**

- Q8: Building merge infrastructure.
- Q9: Fraction of requirements needing implementation-aware
  tests.
- Q10: Mutation scale and operator policy.
- Q15: Phase 3 triage mechanism design.
- Q19: Applicability metadata and semantic impact coverage.
- Q20: Latest-conformance derived view.

**Inspecting phase:**

- Q11: Test Completeness Inspector placement.
- Q12: Inspector roster per project type.
- Q14: Finding Ledger backend and retention.

**Compliance:**

- Q13: HU-02 compliance for autonomous code merge.

**Interface specifications:**

- Q18: CLI interface spec evaluation.

---

## Detailed Design Documents

The following documents contain additional design detail beyond
this Sketch. They are draft material that informed the Sketch
and will continue to evolve as design decisions are made.

- [Overview](architecture/overview.md) — Guiding principles,
  EARS format, deployment modes, workflow terminology, platform
  choices, and bootstrapping strategy.
- [User Interaction Flow](architecture/user-interaction-flow.md)
  — Phase details, sequence diagrams, testing strategy,
  change types, and incremental development model.
- [System Components](architecture/components.md) — Component
  boundaries, interfaces, the content storage model,
  multi-player workflow, and cross-cutting concerns.
- [Open Design Questions](architecture/open-questions.md) —
  Unresolved design questions across all areas.
- [Related Work](architecture/related-work.md) — Red Hat
  internal projects, external factory projects, and lessons
  learned.
