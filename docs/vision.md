# ProtoBot: Vision

> Initial Sketch artifact — September 2026

**Contents:**

- [Purpose](#purpose)
- [Intended users](#intended-users)
- [Desired outcomes](#desired-outcomes)
- [Role in the pipeline](#role-in-the-pipeline)
- [What is a successful prototype](#what-is-a-successful-prototype)
- [Prototype vs. production boundary](#prototype-vs-production-boundary)
- [Self-hosting: the first project](#self-hosting-the-first-project)
- [Non-goals](#non-goals)
- [Assumptions](#assumptions)
- [Unresolved design decisions](#unresolved-design-decisions)

---

## Purpose

ProtoBot is a spec-first software development system. It takes
structured requirement specifications and generates working, tested,
inspected prototypes suitable for customer demonstrations, not final
products.

People define _what_ the system should do in precise, structured
EARS requirements. Everything after that is fully autonomous: test
generation, code generation, independent inspection, and merge.
If the prototype is wrong, we fix the authoritative side of the
mismatch: desired behavior changes go through the specification,
while code that contradicts an approved requirement is regenerated
or fixed against that requirement. If we need to start over, we
discard everything except the specification and regenerate.

The specification is the durable asset. The code is a disposable,
regenerable artifact. Conformance evidence links a specific
implementation to a specific specification commit rather than marking
requirements permanently "done."

---

## Intended users

ProtoBot serves Red Hat teams that need to validate ideas quickly
before committing to full product implementation.

The primary users are:

- **Stakeholders and architects** who have validated an idea through
  IdeaBot and need a working prototype for customer demonstrations
  and feedback.
- **Engineers** who define observable system behavior through
  structured specifications rather than by writing code directly.
- **Product teams** who receive TransferBot's output: the
  specifications and test expectations that describe what the
  prototype proved, not the prototype code itself.

ProtoBot must work for a single developer running locally
(single-player mode) as well as for teams collaborating through a
standard PR workflow (multi-player mode). Individual developer
adoption is the on-ramp to broader enterprise deployment. ProtoBot
must work without requiring an OpenShift or Kubernetes cluster.

---

## Desired outcomes

A successful ProtoBot deployment achieves the following:

1. **Idea validation.** Stakeholders can show a working prototype to
   customers and gather concrete feedback on whether the idea solves
   a real problem.
2. **Specification-driven development.** The human's effort goes into
   defining precise, unambiguous requirements. The system handles
   implementation autonomously.
3. **Independent verification.** Tests and code are generated
   independently by separate agents that cannot see each other's
   output. Correctness is established when independently generated
   tests pass against independently generated code.
4. **Traceable conformance.** Every requirement has clear provenance
   (who authored it, when, which PR merged it) and commit-scoped
   conformance evidence (which implementation satisfied it, verified
   by which tests, at which specification commit).
5. **Evaluable components.** Every agentic component in the system
   can be fed a known input, measured against a known output, and
   independently improved. This is an architectural constraint, not
   a nice-to-have.
6. **Clean handoff.** TransferBot can receive the specifications and
   test expectations from a completed prototype and transfer them to
   a product team for real implementation. The product team gets
   precisely defined requirements, not throwaway code.

---

## Role in the pipeline

ProtoBot is the second tool in the Hermes pipeline:

```text
IdeaBot --> ProtoBot --> TransferBot
(idea)     (prototype)   (product transfer)
```

- **IdeaBot** captures and researches ideas, refines them through
  AI-assisted discovery, and produces structured research artifacts.
- **ProtoBot** turns those ideas into working, tested, inspected
  prototypes with enough quality for customer demonstrations.
- **TransferBot** hands the prototype's specifications and test
  expectations (not the code) to a product team for real
  implementation.

The initial handoff from IdeaBot to ProtoBot is manual: a new
project is started by providing IdeaBot's research artifacts in the
initial Sketching session to seed the project. Automated integration
can come later.

The handoff from ProtoBot to TransferBot is the specifications
and conformance evidence, not the prototype's source code. The
prototype is disposable by design. Its purpose is to validate the
idea and gather feedback.

---

## What is a successful prototype

A prototype is successful when it meets all of the following
criteria:

1. **Observable behavior matches the specification.** Every approved
   EARS requirement has conformance evidence: independently generated
   tests pass against independently generated code at a specific,
   immutable specification commit.
2. **Independent inspection passes.** Security, test completeness,
   code quality, and spec conformance inspectors have reviewed the
   work. All findings are resolved (defects fixed, spec gaps
   addressed, or explicitly declared out of scope with independent
   confirmation).
3. **Demonstrations are possible.** The prototype produces working
   demonstration artifacts (recorded terminal sessions, screenshots,
   self-verifying documents, or animated GIFs as appropriate to the
   interface types) that show the system working as specified.
4. **The specification is transferable.** The EARS requirements,
   interface definitions, and conformance evidence are structured,
   complete, and suitable for handoff to TransferBot and ultimately
   to a product team.

A successful prototype does _not_ require:

- Production-grade performance, scalability, or reliability
- Complete error handling for edge cases the specification does not
  cover
- Deployment infrastructure beyond what is needed for demonstration
- Long-term maintainability of the generated code

---

## Prototype vs. production boundary

The prototype is an artifact that proves an idea works. It is not
production software. The boundary is:

| Prototype (ProtoBot's output) | Production (product team's responsibility) |
| --- | --- |
| Validates that the idea is feasible and useful | Delivers a reliable, maintained product |
| Covers specified behavior exhaustively | Covers unspecified edge cases, degraded modes, and operational concerns |
| Generated code, regenerable from specification | Hand-maintained or separately generated code, owned long-term |
| Demo-quality (works for customer demonstrations) | Production-quality (works under real load, real failures, real adversaries) |
| Disposable: specification survives, code does not | Durable: both specification and code are maintained |
| Tested by independently generated test suites | Tested by the product team's test strategy, seeded by transferred expectations |
| Single deployment target for demo purposes | Multi-environment deployment, CI/CD, rollback, monitoring |

The prototype's durable output is the specification and the evidence
that the specification can be satisfied by an implementation. The
code itself is deliberately disposable. TransferBot transfers
requirements and test expectations, not code.

---

## Self-hosting: the first project

ProtoBot's initial project is to build itself. This bootstrapping
strategy serves two purposes: it produces a working system, and it
validates that system under real-world conditions during
construction.

### Scope of the first project

The initial self-hosting effort builds the components ProtoBot needs
to function:

1. **Specification Toolkit** -- the portable skills, tools, and
   prompts that encode how to do Sketching and Dimensioning, loadable
   into any compatible agent harness.
2. **`ears-manager`** -- the CLI tool for managing the structured
   specification store (EARS requirements, interfaces, change sets).
3. **WMS Adapter** -- a thin integration layer over the chosen work
   management backend, starting with GitHub Issues.
4. **Job Site** -- the autonomous execution engine, starting with a
   Fullsend integration; a direct OpenShell adapter as the fallback.
5. **Validation Rules** -- shared domain logic for work item
   lifecycle transitions.

The interactive work starts locally in OpenCode with draft
Specification Toolkit skills. The first Job Site implementation is
a Fullsend integration spike; the direct OpenShell adapter preserves
a local/custom path and tests the backend abstraction. The initial
prototype output types should be whatever ProtoBot's own components
need (CLI tools, Go binaries, etc.).

### Non-goals

The following are explicitly out of scope for the first self-hosting
project:

1. **Web Drafting Table.** The TUI Drafting Table (OpenCode or Claude
   Code with Specification Toolkit skills) is sufficient for
   bootstrapping. A web UI can be built later as a separate project.
2. **Multiple WMS backends.** The first project targets GitHub
   Issues. Jira, GitLab, Beads, and Trello adapters are future work.
3. **Automated IdeaBot integration.** The handoff from IdeaBot is
   manual for the first project.
4. **Kit ecosystem.** Kits are a reusable import mechanism. The first
   project uses project-local specifications and Inspector
   definitions directly.
5. **Multi-project hosting.** The first project is one ProtoBot
   instance building one project (itself). Multi-tenancy is future
   work.
6. **Production deployment infrastructure.** ProtoBot runs locally
   or in CI. An OpenShift-hosted Job Site with auto-dispatch is a
   later milestone.
7. **All prototype output types.** The initial set covers what
   ProtoBot itself needs (CLI tools, Go binaries). Web UIs, native
   GUIs, and other output types expand over time.
8. **Formal compliance certification.** HU-02 compliance and AIA
   submission are tracked as open questions, not first-project
   deliverables.

---

## Assumptions

The following assumptions underlie this Vision. If any prove false,
the affected design areas will need revisiting:

1. **EARS is sufficient for ProtoBot's own specifications.** The six
   EARS patterns can express the requirements for ProtoBot's
   components (CLI tools, APIs, agent behavior). If ProtoBot's own
   specifications strain EARS, the format or tooling will need
   extension.
2. **Dual-model isolation is practical.** Generating tests and code
   independently from the same specification, where neither Worker
   sees the other's output, produces correct and useful prototypes.
   IdeaBot's oracle-gaming observation and external reimplementation
   evidence support this, but ProtoBot will be the first system to
   use it end-to-end.
3. **Agents can triage test failures reliably.** The Building phase
   depends on a triage mechanism that correctly attributes failures
   to tests, code, or both. The mechanism's design is an open
   question, but the architecture assumes one can be built.
4. **Specification gaps can be surfaced proactively.** The
   Dimensioning phase depends on the agent aggressively identifying
   unspecified behavior before the autonomous phase begins.
   IdeaBot's experience with Hyrum's Law (unspecified behavior
   becoming de facto contract) shows this is critical.
5. **OpenShell or a compatible sandbox provides adequate isolation.**
   The security model assumes kernel-enforced, deny-by-default
   execution environments. If OpenShell cannot satisfy the sandbox
   contract on a target platform, the portable OCI/microVM fallback
   must be built.
6. **GitHub's permission model is sufficient for the approval gate.**
   The multi-player workflow uses PR merge as the human approval
   boundary, relying on branch protection rules, CODEOWNERS, and
   required reviews.

---

## Unresolved design decisions

The following decisions are deferred and tracked as open questions.
Each affects the Vision's scope or the first project's
implementation:

1. **Triage mechanism** ([Q15][q15]). How test failures are
   attributed to tests, code, or both during Building.
2. **IdeaBot handoff format** ([Q4][q4]). How much of the
   Vision/Architecture can be pre-populated from IdeaBot's output.
3. **HU-02 compliance** ([Q13][q13]). Whether PR merge as the human
   checkpoint satisfies Red Hat's AIA requirements for autonomous
   code generation.
4. **Requirements storage format** ([Q7][q7]). Whether JSONL or
   another format is best for structured, git-friendly requirement
   storage.
5. **Kit package format** ([Q5][q5]). How reusable imports are
   packaged, signed, and distributed.
6. **Spec gap surfacing UX** ([Q1][q1]). How the agent presents
   unspecified behaviors during Dimensioning.

These are recorded in the full
[open design questions](architecture/open-questions.md) document.

[q1]: architecture/open-questions.md#q1-spec-gap-surfacing-ux
[q4]: architecture/open-questions.md#q4-ideabot-handoff-format
[q5]: architecture/open-questions.md#q5-kit-package-and-future-capabilities
[q7]: architecture/open-questions.md#q7-requirements-storage-format
[q13]: architecture/open-questions.md#q13-hu-02-compliance
[q15]: architecture/open-questions.md#q15-phase-3-triage-mechanism

---

## Related documents

- [Architecture overview](architecture/overview.md) -- guiding
  principles, EARS format, workflow, and platform
- [System components](architecture/components.md) -- component
  architecture, interfaces, and cross-cutting concerns
- [User interaction flow](architecture/user-interaction-flow.md) --
  phase details, sequence diagrams, and testing strategy
- [Open design questions](architecture/open-questions.md) --
  unresolved decisions across all areas
- [Related work](architecture/related-work.md) -- internal and
  external projects informing the design
