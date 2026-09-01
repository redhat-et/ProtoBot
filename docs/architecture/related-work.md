# ProtoBot: Related Work

> Design document — draft, July 2026

**Contents:**

- [Red Hat Internal Projects](#red-hat-internal-projects)
- [IdeaBot](#ideabot-hermes-pipeline)
- [Forge](#forge-red-hat-israel--openstackshift-on-stack)
- [Fullsend](#fullsend-pnt-devops)
- [Alcove](#alcove-rhel--core-platforms)
- [Rehor / Dev Bot](#rehor--dev-bot-hybrid-platforms)
- [OpenShell](#openshell-nvidia-with-red-hat-as-contributormaintainer)
- [Eval infrastructure](#eval-hub-and-agent-eval-harness)
- [External Factory Projects](#external-factory-projects)
- [StrongDM Attractor](#strongdm-attractor)
- [Gas Town / Beads](#gas-town--beads-steve-yegge)
- [SqueakyClean AI](#squeakyclean-ai)
- [Swarm Forge](#swarm-forge-robert-c-martin)
- [Unbound Force](#unbound-force-jay-flowers)
- [Reimplementation analysis](#agentic-reimplementation-projects-cross-cutting-analysis)
- [Related Documents](#related-documents)

This document surveys projects — both internal to Red Hat and
external — that are relevant to ProtoBot's design. For each, we
describe what it is, what makes it interesting, and what concepts
ProtoBot should consider adopting.

---

## Red Hat Internal Projects

### IdeaBot (Hermes Pipeline)

IdeaBot is the first tool in the Hermes pipeline. It facilitates
AI-assisted idea discovery, research, refinement, synthesis, and
submission to Jira. V1.0 released July 2, 2026. The team uses
AI-assisted development with Claude — developers do not write code
by hand. Claude generates Gherkin feature files that drive
implementation.

**What makes it relevant:** IdeaBot is ProtoBot's immediate upstream
and shares the same team. Its architecture and hard-won lessons
directly inform ProtoBot's design.

#### Lessons learned from IdeaBot's EARS/Gherkin pipeline

IdeaBot uses a strict `ears-gherkin-dev` skill pipeline: EARS
requirements drive Gherkin scenario generation, which drives
implementation. After producing 100+ Gherkin features and 2,300+
step definitions, two recurring problems surfaced:

1. **Implementation details leak into the EARS specification
   itself.** This is more severe than the well-known "step
   definition drift" problem in Gherkin. Even the requirements layer
   — supposedly implementation-agnostic — was found to contain
   references to specific internal structures, function names, and
   implementation choices. This means the leakage isn't confined to
   the Gherkin/step-definition boundary; the requirements level
   isn't immune either.

2. **Unspecified behavior gets silently filled in by agent
   assumption.** When a spec doesn't cover a case, the coding agent
   guesses during implementation. This produces two failure modes:
   - The guess is wrong — undesired behavior ships.
   - The guess "sticks" — users start depending on the
     inferred-but-never-specified behavior, and a later change
     "corrects" it since the spec never locked it down. Users are
     upset by a behavior change that, from the spec's point of view,
     was never a promise. This is Hyrum's Law applied to
     agent-generated code: unspecified behavior becomes de facto
     contract the moment users can observe it.

An architecture portability rewrite experiment (July 2026) — taking
an existing component's feature files and having an agent
reimplement it in a different language — found that Claude inserted
comments into source files purely to satisfy string-matching
assertions in the test suite rather than reimplementing the
described behavior. This is a live example of oracle gaming when the
test suite is visible to the generating agent.

**What ProtoBot should adopt:**

- **Aggressive gap surfacing during Dimensioning.** The single
  biggest mitigation for lesson #2 is to catch unspecified behavior
  _before_ the autonomous phase begins, not after.
- **Dual-model isolation.** The oracle-gaming observation directly
  validates the design decision that Worker A (tests) must never see
  Worker B's (code) output, and vice versa.
- **EARS without Gherkin.** ProtoBot drops Gherkin entirely. Much of
  Gherkin's structure is non-executable and serves only to group
  tests. EARS requirements feed test generation directly, with no
  Gherkin intermediary. Arbitrary test frameworks (compatible with
  the prototype's implementation language) replace Cucumber.
- **Structured spec storage via `ears-manager`.** Specifications
  must live in a structured, queryable store — not as prose files
  that agents re-read and re-interpret after every context
  compaction.
- **Clear ticket boundaries.** IdeaBot's auto-generated
  implementation backlog had significant overlap and ambiguity
  between tickets, leading to duplicated effort. Whatever process
  generates ProtoBot's implementation tickets should enforce clear,
  non-overlapping boundaries up front.

#### Reusable architecture decisions

IdeaBot has 23 ADRs (Architecture Decision Records) at
[redhat-et/hermes](https://github.com/redhat-et/hermes). Several
transfer directly to ProtoBot:

- **Credential isolation** (ADR-0005/0006): Token Service +
  auth-proxy sidecar, adapted from Alcove's Bridge/Gate pattern.
  The main agent process never sees real credentials.
- **Platform constraints** (ADR-0021): Managed Platform Plus
  tenants cannot install CRDs/operators. Applies equally to
  ProtoBot.
- **Base images** (ADR-0009): Red Hat certified UBI images only.
- **Identity** (ADR-0012): Red Hat SSO (Keycloak) via
  `keycloak-connect`.

### Forge (Red Hat Israel — OpenStack/Shift On Stack)

Forge is an open-source (MIT), AI-integrated SDLC orchestrator
built by the Shift On Stack / OpenStack team. It automates the path
from Jira ticket to merged pull request. GitHub:
[forge-sdlc/forge](https://github.com/forge-sdlc).

**What makes it unique:** Forge has a mature, production-tested
autonomous pipeline: a 37-node LangGraph state machine with 4
human-approval gates, sandboxed Podman execution, CI fix loops (up
to 5 retries), and AI self-review (2 passes). It has 18 real
merged PRs from its bot account. Scored 29/35 on the GE Agentic
SDLC Working Group's HAERCVM framework.

**What makes it interesting for ProtoBot:** Forge covers the hardest
parts of ProtoBot's autonomous phase — code generation with
decomposition, sandboxed execution, CI feedback, and approval gates.
Its interface contract aligns naturally: IdeaBot writes to Jira;
Forge reads from Jira.

**Gaps relative to ProtoBot's needs:**

1. No interactive pre-generation phase (ProtoBot needs EARS
   requirement review before building).
2. No project bootstrapping (Forge works within existing repos;
   ProtoBot may scaffold from zero).
3. No infrastructure artifacts (Forge generates application code
   only; ProtoBot needs deployment manifests, CI pipelines).
4. Jira-only intake — ProtoBot requires GitHub support.

**What ProtoBot should consider adopting:**

- **The review/fix loop pattern:** local self-review (2 passes) →
  CI fix loop (5 retries) → AI review → human gate. This is a
  concrete, already-tested pattern for bounding the triage/fix
  cycle.
- **Workflow-first design:** the orchestrator owns the lifecycle,
  not the agents. Agents perform bounded work within stages.
  Controlled boundaries: agents never directly write to external
  systems; all mutations happen at explicit workflow steps.
- **Skills as markdown:** Forge's skills system uses markdown
  instruction files that customize agent behavior per workflow
  stage, with team-maintained defaults and project/repo/stack
  overrides. This matches ProtoBot's planned Specification Toolkit
  approach.

### Fullsend (PnT DevOps)

Fullsend is an open-source (Apache 2.0) autonomous agentic SDLC
platform led by PnT DevOps. It deploys purpose-built agents across
GitHub repos for triage, code generation, review, fix, prioritize,
and retrospective. Website: [fullsend.sh](https://fullsend.sh/).
GitHub: [fullsend-ai/fullsend](https://github.com/fullsend-ai/fullsend).

**What makes it unique:** Fullsend is GitHub-native end-to-end
(also supports GitLab and Forgejo). It runs as a GitHub Action or
standalone CLI. Six specialized agent types, each following a
strict three-phase execution model: pre-script (full permissions)
→ sandbox (restricted, structured JSON output) → post-script
(elevated permissions for mutations). The agent never has direct
write access to the repository. 65 releases as of July 2026.

**What makes it interesting for ProtoBot:** Fullsend is the closest
peer to ProtoBot in the GE Agentic SDLC Working Group's portfolio.
It runs on OpenShell for sandboxing and is GitHub-native, matching
ProtoBot's own requirements. Scored 33/35 on HAERCVM.

**ProtoBot status:** Fullsend is the first implementation target for the
autonomous Job Site. ProtoBot remains the control plane for change sets,
WMS materialization, role-projected Worker repositories, dual-worker
integration/Triage, and conformance evidence. Fullsend supplies execution
and may host Inspector fan-out, but adoption depends on its effective
OpenShell boundary passing ProtoBot's sandbox contract. A direct
OpenShell adapter remains the fallback/custom Job Site path. This is an
integration experiment, not a decision to make Fullsend's GitHub-native
work-item model or current agent runtimes part of ProtoBot's architecture.
A portable rootless-OCI/microVM profile with external egress and
credential brokers is the independent sandbox fallback if OpenShell
cannot satisfy a target platform.

**What ProtoBot should consider adopting:**

- **The pre/sandbox/post execution model.** Separating privilege
  levels by phase (prepare → execute in sandbox → apply mutations
  with elevated permissions) is a clean, auditable pattern for
  enforcing the "agent never writes directly" constraint.
- **Review as a multi-dimensional fan-out.** Fullsend's Review agent
  launches parallel sub-agents across six review dimensions rather
  than running a single monolithic review pass. This matches
  ProtoBot's Inspector roster design.

### Alcove (RHEL / Core Platforms)

Alcove is a Kubernetes-native platform for sandboxed AI agent
execution, part of the GE Agentic SDLC Working Group's RHEL
federation. GitHub: `alcove-ai/alcove`.

**What makes it unique:** Alcove's Bridge/Gate credential
architecture separates credential storage (Bridge: encrypted store,
pre-fetches tokens at dispatch time) from credential injection
(Gate: sidecar that replaces dummy tokens with real ones so the
agent process never sees actual credentials).

**What makes it interesting for ProtoBot:** IdeaBot already adapted
Alcove's Bridge/Gate pattern for its Token Service and auth-proxy
sidecar. This is a proven credential-isolation pattern directly
transferable to ProtoBot.

**What ProtoBot should consider adopting:**

- **The Bridge/Gate pattern** for credential isolation in any
  pod-per-session or pod-per-run agent architecture.

### Rehor / Dev Bot (Hybrid Platforms)

Rehor is an autonomous development agent built by Hybrid Platforms.
It runs in a continuous polling loop: searches Jira for unassigned
tickets, claims a ticket, selects a persona (frontend, backend,
operator, config, CVE, or tooling), implements the change, opens a
PR, and maintains it through review cycles — all without human
intervention. 285 PRs as of June 2026. Scored 33/35 on HAERCVM
(tied for highest in the Working Group).

**What makes it interesting for ProtoBot:** Rehor is the most
autonomous system in the GE Working Group's portfolio —
agent-as-primary-developer with humans reduced to reviewers. It
demonstrates that the "autonomous phase" model
ProtoBot targets is achievable in production.

**What ProtoBot should consider adopting:**

- **Persona-based routing.** Rehor selects a specialized persona
  per ticket based on content. ProtoBot's Inspector roster (Security,
  Test Completeness, Code Quality) is a similar specialization
  pattern.
- **Persistent RAG memory.** Rehor maintains a pgvector-backed
  memory server so context carries across runs rather than starting
  fresh each time. ProtoBot's `ears-manager` serves a related
  purpose (structured spec access across sessions).

### OpenShell (NVIDIA, with Red Hat as contributor/maintainer)

OpenShell is an open-source sandbox runtime for autonomous AI
agents. It provides kernel-enforced, policy-governed execution
environments. Apache 2.0. GitHub:
[NVIDIA/OpenShell](https://github.com/NVIDIA/OpenShell).

**What makes it unique:** Deny-all-by-default posture across five
enforcement layers (Landlock filesystem restrictions, seccomp
syscall filtering, network namespace isolation, per-binary
OPA/Rego network policy, and L7 HTTP inspection via TLS
interception). Credentials are never stored inside the sandbox; an
inference routing proxy injects them at the network boundary.
Supports Docker, Podman, MicroVM, and Kubernetes backends.

**What makes it interesting for ProtoBot:** OpenShell underlies the first
Fullsend Job Site backend and is the direct fallback for a custom Job
Site. Its per-binary, per-destination L7 policy model allows whitelisting
package registries and documentation sites while blocking exfiltration,
a requirement for ProtoBot when scaffolding projects from zero. The
architecture nevertheless depends on a backend-neutral sandbox contract;
OpenShell guarantees are accepted only after end-to-end validation.
The independent sandbox fallback is a portable rootless-OCI/microVM
profile with external policy and credential brokers, not merely a second
way to invoke OpenShell.

**What ProtoBot should consider adopting:**

- **OpenShell through Fullsend first, direct OpenShell second** behind one
  execution/sandbox contract.
- **Policy-as-code.** OpenShell's declarative YAML policies with
  three enforcement tiers (YAML authoring, OPA/Rego runtime, Z3/SMT
  formal verification) are the mechanism for ProtoBot's "enforce
  constraints structurally" principle.
- **Standalone mode support.** OpenShell's Mode 1 ("sandbox the
  entire agent") targets dev laptops with no cluster required,
  directly supporting ProtoBot's single-player mode requirement.

### Eval Hub and Agent Eval Harness

**Eval Hub** is a centralized evaluation platform maintained by the
ACE team for assessing AI models, agents, and skills. MLflow-based,
with three-dimensional scoring (capability, AI risk, performance).

**Agent Eval Harness** (`opendatahub-io/agent-eval-harness`) is a
skill-evaluation tool from AI Engineering. It provides a closed-loop
skill quality cycle: analyze a skill → generate test cases → run
headlessly → judge outputs → improve the skill.

**What makes them interesting for ProtoBot:** ProtoBot's
evaluability requirement means every agentic component must be
independently evaluable. These are existing Red Hat eval
infrastructure that ProtoBot should evaluate rather than building
from scratch. The Agent Eval Harness's closed-loop remediation
(`/eval-optimize`: identify failures, edit skill, re-run, check for
regressions) is directly relevant to ProtoBot's self-improvement
loop concept.

---

## External "Factory" Projects

### StrongDM Attractor

Attractor is the execution engine of StrongDM's software factory.
It is a graph-based pipeline orchestrator that uses Graphviz DOT
syntax to define multi-stage AI workflows as directed graphs. Each
node is a task; edges can be natural-language predicates evaluated
by the LLM.

GitHub: [strongdm/attractor](https://github.com/strongdm/attractor)
(Apache 2.0, 1,300+ stars). The repo contains **zero code** — only
three NLSpec markdown files totaling ~6,500 lines. The spec _is_
the product; code is generated from it.

**What makes it unique:** StrongDM used Attractor in production to
build security and access-management software — 3 engineers, ~1M
lines of production code, 3.5 PRs per engineer per day, $1,000+/day
in token costs per engineer. The spec-only release spawned 18+
independent implementations across 12+ languages, empirically
validating the "spec in, software out" model.

**Key concepts for ProtoBot:**

- **NLSpec (Natural Language Specifications)** as the source of
  truth. Code is a transitory artifact. This is the same
  "spec-as-source" principle ProtoBot uses with EARS.
- **Scenarios as holdout evaluation.** End-to-end user stories are
  stored outside the codebase as holdout sets, preventing agents
  from gaming the test suite. This is the strongest form of the
  oracle-isolation principle that drives ProtoBot's dual-model
  isolation.
- **Shift Work.** Separating interactive specification from
  non-interactive execution — the same two-phase
  (Sketching/Dimensioning vs. Building/Inspecting) split ProtoBot
  uses.
- **Satisfaction as a probabilistic metric.** Replacing boolean
  pass/fail with a probabilistic measure of how well the
  implementation satisfies the spec. Worth considering as a richer
  signal than pass/fail for ProtoBot's Inspectors.

### Gas Town / Beads (Steve Yegge)

Beads is a distributed graph issue tracker purpose-built for AI
agents. Gas Town (now in maintenance) and its successor Gas City
are multi-agent orchestration platforms built on top of Beads.
Created by Steve Yegge (ex-Amazon, ex-Google, ex-Grab).

- Beads: [gastownhall/beads](https://github.com/gastownhall/beads)
  (~25.5k stars)
- Gas Town: [gastownhall/gastown](https://github.com/gastownhall/gastown)
  (~17.2k stars, maintenance mode)
- Gas City: [gastownhall/gascity](https://github.com/gastownhall/gascity)
  (~1k stars, actively developed)

**What makes it unique:** Beads solves the "agent dementia" problem.
Yegge's original approach — hierarchical markdown plan files —
failed catastrophically: agents re-decomposed the same work into
new plans after every context compaction. He accumulated 605
markdown plan files before abandoning the approach and deleting 70k
lines of plan-management code. Beads replaced this with structured,
git-backed, queryable work state using JSONL in git backed by a
version-controlled SQL database (Dolt).

Gas City is explicitly positioned as "Kubernetes for coding agents"
— it coordinates unreliable workers through a control plane with a
shared source of truth (Beads as the equivalent of etcd).

**Key concepts for ProtoBot:**

- **Structured, queryable work state beats markdown plans for agent
  memory across sessions.** This directly validates `ears-manager`'s
  existence and its design principle of being a structured,
  queryable store rather than prose files.
- **Never rely on a single agent's self-report of "done."** Gas
  City's explicit rule — never deploy one agent alone for anything
  that matters, always have 2-3 agents catching each other's
  mistakes — reinforces the dual-model isolation and Inspector
  patterns.
- **The Refinery merge queue.** A Bors-style bisecting merge queue
  where agents never push directly to main. Batches merge requests,
  runs verification gates, and bisects to isolate failures when a
  batch goes red. Directly relevant to ProtoBot's CI design for
  managing concurrent work items.
- **Standalone vs. cluster as a first-class, swappable choice.** Gas
  City's runtime-provider abstraction (tmux, subprocess, exec, K8s)
  supports both local single-player and cluster-based deployment as
  swappable backends. This validates ProtoBot's requirement that
  both modes be first-class.
- **Agent shortcut-taking near context limits.** Yegge documented
  agents near their context limit hiding shortcuts (disabling tests,
  mocking databases instead of fixing connections) to declare
  victory. Beads' mitigation: kill/restart agents after each small
  task, keeping them near the start of their context window.

### SqueakyClean AI

SqueakyClean is an open-source (Apache 2.0) agentic code generation
tool that produces buildable, testable applications from a single
declarative input (a ProblemSpec). It describes itself as
"opinionated, semi-deterministic agentic software development."

GitHub:
[garciaalan186/squeaky-clean](https://github.com/garciaalan186/squeaky-clean)

**What makes it unique:** A three-tier agent model with cost-tiered
model routing: a large model makes architectural decisions (Tier A),
a manager orchestrates module-level work (Tier B), and many small,
cheap parallel agents generate one file each from ~200 chars of spec
(Tier C). Between tiers, the Squib DSL serves as a machine-checkable
intermediate representation — validated before any code is generated
(DAG check, export check, dependency rule enforcement).

**Key concepts for ProtoBot:**

- **An architectural DSL as inter-agent protocol.** Squib eliminates
  ambiguity between agent tiers, solving the "LLM telephone game"
  problem where instructions degrade as they pass between agents.
  ProtoBot's `ears-manager` serves a similar role — enforcing
  structure on the specification so agents can't reinterpret it.
- **Cost-tiered model routing.** Expensive frontier models for
  high-level architecture; cheap compact models for per-file code
  generation. ProtoBot's architecture could benefit from the same
  pattern — Sketching and triage may warrant a reasoning model while
  Workers could run on faster, cheaper models.
- **Structural validation before generation.** DAG validation,
  dependency rule enforcement, and cross-module export checking
  happen before any code is generated, catching architectural
  errors cheaply.
- **Bidirectional capability.** SqueakyClean also works in reverse,
  recovering architecture from legacy codebases. This is relevant to
  the prototype-to-product transfer question: could ProtoBot's specs
  be recovered from an existing codebase rather than only authored
  from scratch?

### Swarm Forge (Robert C. Martin)

Swarm Forge is a tmux-based multi-agent orchestration platform by
Robert C. Martin ("Uncle Bob"). It coordinates swarms of AI coding
agents — each in its own git worktree and tmux session — through
file-based handoffs and a layered constitution prompt system.

GitHub:
[unclebob/swarm-forge](https://github.com/unclebob/swarm-forge)
(1.2k stars)

**What makes it unique:** Three configurable "packs" with
increasing role specialization (2-pack, 4-pack, 6-pack). The
six-pack includes: specifier → coder → cleaner → architect
(mutation hardening + structural review) → hardener → QA. Each
role runs in its own git worktree. Agents communicate exclusively
through a daemon-mediated, file-based handoff system — two message
types only (git commit references and short notes), not prose.

**Key concepts for ProtoBot:**

- **Mutation testing as a structural gatekeeper.** The "architect"
  role installs the language mutation tool and uses it to "cover the
  uncovered, and kill survivors" — closing test gaps before a
  dedicated hardener role runs a heavier final mutation pass. This
  is a concrete implementation of the mutation-testing-as-hidden-gate
  pattern ProtoBot designs for.
- **File-based, structured handoffs between agents.** Agents never
  share context or read each other's conversation history. State
  transfer happens through committed code and structured messages,
  not through shared memory — enforcing the same kind of isolation
  ProtoBot's dual-model design requires.
- **Role specialization is config-driven, not hardcoded.** Projects
  choose which roles to include and which model backend each role
  uses. ProtoBot's Inspector roster could benefit from a similar
  configuration-driven approach.

### Unbound Force (Jay Flowers)

Unbound Force is an open-source (Apache 2.0) AI agent swarm for
software engineering. It wraps OpenCode with structured agent
personas, specification pipelines, quality gates, and multi-agent
coordination tooling. 5 role-specialized personas, 8-phase
spec-to-code pipeline.

GitHub org: [unbound-force](https://github.com/unbound-force)

**What makes it unique:** An 8-phase, hard-gated pipeline from
spec to code with a multi-agent "review council" (5+ specialized
reviewers: Guard, Architect, Adversary, Testing, SRE, Curator).
It includes purpose-built supporting tools: Gaze (contract coverage
and CRAP-score analysis), Dewey (knowledge graph MCP server), and
Replicator (multi-agent coordination via git worktrees and file
reservations). An adoption report claims the team tripled output
over 6 months.

**Key concepts for ProtoBot:**

- **The review council pattern.** Five specialized reviewers running
  in parallel, each with a different focus. This is a concrete,
  existing implementation of ProtoBot's Inspector roster concept.
- **Contract coverage analysis (Gaze).** A tool that measures how
  well tests cover the specification contracts, not just code lines.
  This is directly relevant to ProtoBot's Test Completeness
  Inspector.
- **Knowledge graph for agent context (Dewey).** A persistent,
  structured context store (not just RAG) that agents query to
  understand project conventions and history. Complementary to
  `ears-manager` but focused on implementation context rather than
  specification state.

### Agentic Reimplementation Projects (Cross-Cutting Analysis)

Several major agentic reimplementation projects provide empirical
evidence for ProtoBot's design decisions:

| Project | Scale | Key finding |
| --- | --- | --- |
| MirrorCode (Epoch AI + METR, 2026) | 25 programs, up to 61K LoC | Success rate varies by model (Opus 4.7: 56%, GPT-5.5: 44%); cheating rates measurable (GPT-5.5: 24%, Gemini: 31%, Opus: 0%). |
| Anthropic C compiler (Feb 2026) | 100K lines, 16 parallel agents | GCC torture tests as validation oracle; $20K over 2 weeks. |
| Bun Zig→Rust rewrite (2026) | 535K LoC, 64 concurrent agents | 6,778 commits in 11 days; $165K. Zero tests skipped — but 13,044 `unsafe` blocks vs. 73 in comparable hand-written Rust. |
| Google Ads migrations (2025) | 500M+ line codebase, 39 migrations | LLMs alone via simple prompting are insufficient; combination of AST + heuristics + LLMs needed. |

**The cross-cutting finding:** Success is primarily determined by
the quality and independence of the validation infrastructure, not
by the capability of the code-generating model. Projects with
strong, independent oracles (existing test suites, reference
implementations) succeed at much higher rates than those without.

**Key concepts for ProtoBot:**

- **Oracle availability predicts success.** Hidden holdout
  evaluation (test suite invisible to the generator) is the
  strongest validation pattern. Visible test suites are exploitable
  (agents optimize against the literal test, not the intent). No
  oracle leads to the worst outcomes. This is the strongest external
  validation of ProtoBot's dual-model isolation design.
- **Architectural lock-in is a real failure mode.** In the MirrorCode
  benchmark, Opus 4.6 correctly identified that a program required
  lazy evaluation 192 times across ~9,000 messages but never
  performed the architectural rewrite, burning ~900M tokens on the
  wrong foundation. ProtoBot's triage mechanism must be able to
  detect and break out of this pattern.
- **The trust bottleneck shifts from generation to verification.**
  If the test suite is insufficient to catch problems in the
  original code, it's also insufficient for the generated code.
  Mutation testing addresses exactly this gap.

---

## Related Documents

- [Overview](overview.md) — What ProtoBot is, guiding principles,
  and workflow summary
- [User Interaction Flow](user-interaction-flow.md) — Phase details
  and sequence diagrams
- [System Components](components.md) — Component architecture,
  interfaces, and cross-cutting concerns
- [Open Design Questions](open-questions.md) — Unresolved design
  questions across all areas
