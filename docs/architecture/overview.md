# ProtoBot: Overview

> Design document — draft, July 2026

**Contents:**

- [What is ProtoBot?](#what-is-protobot)
- [Who is it for?](#who-is-it-for)
- [Guiding principles](#guiding-principles)
- [EARS requirements](#ears-the-requirements-format)
- [Single-player and multi-player modes](#single-player-and-multi-player-modes)
- [Workflow and terminology](#workflow-and-terminology)
- [Platform](#platform)
- [Bootstrapping strategy](#bootstrapping-strategy)
- [Detailed design documents](#detailed-design-documents)

## What is ProtoBot?

ProtoBot takes requirement specifications and generates working
prototypes suitable for customer demos, not final products. It is
the second tool in the Hermes pipeline, following IdeaBot (idea
capture and research) and preceding TransferBot (transfer of
specifications and test expectations to product teams).

The pipeline:

```text
IdeaBot → ProtoBot → TransferBot
(idea)    (prototype)  (product transfer)
```

ProtoBot's input is a set of structured requirements (EARS format).
Its output is a working, tested, inspected prototype plus
demonstration artifacts.

## Who is it for?

ProtoBot serves Red Hat teams that need to validate ideas quickly.
The primary workflow:

1. A stakeholder uses IdeaBot to research and refine an idea.
2. ProtoBot turns that idea into a working prototype with enough
   quality for customer demonstrations.
3. TransferBot hands the prototype's specifications and test
   expectations (not the code) to a product team for real
   implementation.

The prototype is disposable by design. Its purpose is to validate
the idea and gather feedback, not to ship to production.

### IdeaBot handoff

The initial handoff from IdeaBot to ProtoBot is manual. A new
project is started by providing IdeaBot's research artifacts in the
initial Sketching session to seed the project. Automated integration
can come later.

## Guiding principles

### The specification is the product

We don't write code, and we don't read code. We don't write tests,
and we don't read tests. Everything is guided by the specification.

The human's involvement is defining _what_ the system should do —
precisely, unambiguously, in structured EARS requirements. Everything
after that (test generation, code generation, review, merge) is
fully autonomous. If the prototype is wrong, we fix the
authoritative side of the mismatch: desired behavior changes go through
the specification, while code that contradicts an approved requirement
is regenerated or fixed against that requirement. If we need to start
over, we discard everything except the specification and regenerate.

This is the "spec-as-source" principle: the specification is
sufficient to define the system, and everything else is a
regenerable artifact.

### Observable behavior is all that matters

Requirements define the system's interaction with the external world
through its interfaces. Internal implementation is an autonomous
agent's concern. State is treated as external to the system (it
outlives any single run), which means implementations can be
upgraded or replaced without changing the specification.

### Enforce constraints structurally, not through trust

Constraints on agent behavior (no shortcuts, no mocking, no oracle
gaming) are enforced at the sandbox and tooling level, not through
prompting alone. The architecture assumes agents will take the
fastest path to "done" and designs the environment so that path is
also the correct one.

### Build for evaluability from day one

Every agentic component must be independently evaluable. If you
can't feed it a known input and measure its output, you can't
improve it. This is an architectural constraint, not a nice-to-have.

---

## EARS: The Requirements Format

ProtoBot uses EARS (Easy Approach to Requirements Syntax), a
lightweight method developed at Rolls-Royce for writing unambiguous
natural-language requirements. EARS constrains every requirement
into one of six keyword-driven templates:

| Pattern | Template | When to use |
| --- | --- | --- |
| **Ubiquitous** | The \<system\> shall \<response\> | Always-active requirements |
| **Event-driven** | When \<trigger\>, the \<system\> shall \<response\> | Triggered by a discrete event |
| **State-driven** | While \<state\>, the \<system\> shall \<response\> | Active throughout a state |
| **Optional feature** | Where \<feature\>, the \<system\> shall \<response\> | Only when a feature is present |
| **Unwanted behavior** | If \<trigger\>, then the \<system\> shall \<response\> | Handling errors and failures |
| **Complex** | Combination of the above | Multiple conditions |

EARS is precise enough that an agent can act on it, yet accessible
enough that non-engineers can read and validate it. Requirements
stay in structured natural language — no Gherkin, no executable
scenarios, no code.

**Why EARS and not Gherkin?** Gherkin's structure (feature
descriptions, `Rule:` statements) is largely non-executable and
serves only to group tests. It doesn't pull its weight as a
specification format for our use case. ProtoBot allows arbitrary
test frameworks, chosen to be compatible with whatever
implementation language a given prototype uses, with EARS
requirements feeding test generation directly.

**References:**

- [Alistair Mavin's EARS page](https://alistairmavin.com/ears/)
- Mavin, A., Wilkinson, P., Harwood, A. & Novak, M. (2009). "Easy
  Approach to Requirements Syntax (EARS)." _Proceedings of the 17th
  IEEE International Requirements Engineering Conference_, pp.
  317–322. DOI: 10.1109/RE.2009.9
- [Jama Software — Adopting the EARS Notation](https://www.jamasoftware.com/requirements-management-guide/writing-requirements/adopting-the-ears-notation-to-improve-requirements-engineering/)

---

## Single-Player and Multi-Player Modes

ProtoBot must support two deployment modes as first-class options,
not an afterthought:

### Single-player mode

A single developer runs ProtoBot locally or against their own repo.
They have write access and can push specs directly to main (or
merge their own PRs). Approval atomically materializes a build work
item in the WMS, and the Job Site claims that work item the same way it
does in multi-player mode. Minimal ceremony.

This is the on-ramp. Individual developer adoption is the pathway
to broader enterprise deployment. ProtoBot must work without
requiring an OpenShift/Kubernetes cluster.

### Multi-player mode

Multiple contributors work through a standard PR workflow. A
contributor writes specs via the Drafting Table, opens a PR against
main, and a reviewer merges it. The approved change set lands on main
and idempotently materializes a complete build work item. It enters
`waiting` when dependencies remain, `ready-for-building` otherwise, or
is omitted when the approved change set declares no implementation
effect. The Job Site claims and builds ready items autonomously.

This mode uses GitHub's native permission model: PR merge is the
approval gate, governed by branch protection rules, CODEOWNERS,
and required reviews. No custom labels or permission schemes
needed.

Both modes use the same components (Drafting Table, Specification
Toolkit, WMS Adapter, Job Site). The difference is ceremony, not
architecture.

---

## Workflow and Terminology

ProtoBot follows a construction metaphor with four phases:

```text
 Interactive (Human + Agent)          Autonomous (Agent-only)
┌─────────────┬──────────────┐    ┌──────────────┬─────────────┐
│  Sketching  │ Dimensioning │    │   Building   │  Inspecting │
│             │              │    │              │             │
│  Produces:  │  Produces:   │    │  Produces:   │  Produces:  │
│  Sketch     │  Schematic   │    │  Code+Tests  │  Inspection │
│  (Vision +  │  (Approved   │    │              │  Report     │
│  Arch.)     │  EARS reqs)  │    │              │             │
└─────────────┴──────┬───────┘    └──────┬───────┴─────────────┘
                     │                   │
              Human review          Fully autonomous
              boundary here
```

### Terminology

| Term | Meaning |
| --- | --- |
| **Sketching** | Human + agent define what to build (Vision) and its external boundaries (Architecture). |
| **Dimensioning** | Human + agent produce precise EARS requirements for each interface. This is the most time-consuming interactive work. |
| **Building** | Agents generate tests and code concurrently from approved EARS requirements. Neither sees the other's output (dual-model isolation). |
| **Inspecting** | Independent Inspector agents review the work. Defects go back to Building for rework. |
| **Sketch** | The artifact from Sketching: a Vision statement + Architecture. |
| **Schematic** | The artifact from Dimensioning: the complete set of approved EARS requirements. This is the human review boundary. |
| **Project** | One prototype and its canonical Git repository, specification history, WMS configuration, and policy. A ProtoBot deployment may serve many projects; project does not mean one ProtoBot installation. |
| **Request** | A backlog record for user intent, rationale, business priority, ownership, and relationships before or alongside specification refinement. |
| **Change set** | A reviewed specification transaction that records requirements added, revised, or retired and unchanged requirements that remain applicable. It is immutable after approval and does not own the requirements it mentions. |
| **Changed requirement** | A requirement added, revised, or retired by a change set. |
| **Applicable requirement** | An unchanged requirement that constrains implementation of a change set and must be verified by the resulting build work. |
| **Drafting Table** | The environment where the human and agent collaborate (Sketching + Dimensioning). Can be a web UI or a TUI (e.g., OpenCode). |
| **Job Site** | The autonomous execution engine that runs Building + Inspecting. |
| **Build work item** | The WMS coordination and Job Site execution unit. It freezes changed and applicable requirements at an immutable specification commit and owns all mutable workflow state. |
| **Conformance evidence** | Immutable verification artifacts for a requirement at a specification and tested-candidate commit, paired by WMS completion metadata with the resulting merge commit. It is not mutable requirement status. |
| **Kit** | A reusable, versioned import. Current known contents are proposed EARS/interfaces and Inspector definitions; exact packaging and additional capabilities remain open. |
| **Worker** | An agent that generates tests (Worker A) or code (Worker B) during Building. |
| **Inspector** | An agent that reviews work during Inspecting (Security, Test Completeness, Code Quality, etc.). |

### Specification hierarchy

Specifications are produced at four levels, each more specific
than the last:

1. **Vision** (once per project) — _What_ are we building? _Who_
   for? _Why?_
2. **Architecture** (once-ish) — What are the external interfaces?
   What types? (API, CLI, GUI, etc.) Persistent state counts as an
   interface.
3. **Interface** (infrequent) — A stable external contract boundary,
   specified using an approach appropriate to its type.
4. **Requirement** (often) — Individual EARS requirements.

The top two levels are set once at project start. Interface and
Requirement are where ongoing interactive work happens.

A **change set** is not another hierarchy level and is not a durable
feature grouping. It is the review and audit unit for a specification
delta. A requirement can be introduced by one change set, revised by
another, and retired by a third while retaining the same stable identity.
The current Schematic is the result of applying all approved change sets.

Requirements do not carry `pending`, `in-progress`, or `implemented`
workflow markers. Those markers conflate coordination with conformance
and become unreliable whenever later work affects an existing
requirement. The WMS tracks build work-item lifecycle instead, while
completion records immutable, commit-scoped conformance evidence.

---

## Platform

The interactive Drafting Table uses **OpenCode** as its first agent
harness, with the Specification Toolkit loaded as skills. The autonomous
Job Site targets **Fullsend** as its first execution backend. Fullsend's
OpenShell-based sandbox runs Workers and Inspectors through a ProtoBot
backend adapter; a direct **OpenShell** adapter is the fallback and the
starting point if ProtoBot implements a custom Job Site. A portable
rootless-OCI/microVM containment profile with external network and
credential brokers is the independent sandbox fallback when OpenShell
itself is unavailable or incompatible with the target platform.

ProtoBot depends on a backend-neutral execution and sandbox contract,
not on Fullsend or OpenShell APIs. Fullsend does not replace ProtoBot's
change-set/WMS control plane, role-projected repositories, dual-worker
integration, Triage sanitizer, or conformance evidence. A backend that
cannot pass the contract's isolation, network, and credential acceptance
tests cannot run autonomous work.

OpenCode's model-provider flexibility applies directly to the Drafting
Table. Job Site harnesses are backend-pluggable and need not use OpenCode;
provider support is validated per backend, including Vertex AI, internal
providers, and Praxis (Red Hat's AI Gateway).

Prototype outputs are built on UBI + Hummingbird images for
lightweight, fast-turnaround demo builds — but not every prototype
is a container. The set of supported output types will expand over
time (see Bootstrapping Strategy below).

---

## Bootstrapping Strategy

ProtoBot will be used to build ProtoBot.

The interactive work starts locally in OpenCode with draft Specification
Toolkit skills. The first Job Site implementation is a Fullsend
integration spike; the direct OpenShell adapter preserves a local/custom
path and tests the backend abstraction. The first work packages will
likely be single PRs. From there, we build alternate implementations and
work toward an OpenShift-hosted Job Site with auto-dispatch.

The initial prototype output types should be chosen to support this
bootstrapping. Like the interface-type taxonomy, we start with a
limited set and expand from there — the priority is whatever
ProtoBot's own components need (CLI tools, Go binaries, etc.).

---

## Detailed Design Documents

- [User Interaction Flow](user-interaction-flow.md) — Phase
  details, sequence diagrams, testing strategy, and incremental
  development.
- [System Components](components.md) — Component architecture,
  interfaces, the content storage model, multi-player workflow,
  and cross-cutting concerns.
- [Open Design Questions](open-questions.md) — Unresolved design
  questions across all areas.
- [Related Work](related-work.md) — Red Hat internal projects,
  external factory projects, and lessons learned.
