# ProtoBot: Open Design Questions

> Design document — draft, July 2026

**Contents:**

- [Interactive phase](#interactive-phase)
- [Specification storage](#specification-storage)
- [Building phase](#building-phase)
- [Inspecting phase](#inspecting-phase)
- [Compliance](#compliance)
- [Interface specifications](#interface-specifications)
- [Related Documents](#related-documents)

Questions that are not yet resolved. Updated as decisions are made.

---

## Interactive phase

### Q1: Spec gap surfacing UX

How does the agent present unspecified
   behaviors during Dimensioning? Inline suggestions? Separate "gap
   report"? What's the interaction pattern for the user to say "out of
   scope" vs. "add a requirement for that"?

### Q2: Async requirement suggestion delivery

When the autonomous
   phase discovers unspecified behavior, it blocks the work item and
   escalates to the user (decided). The remaining questions: what
   notification channel delivers the escalation (email, Slack,
   webhook)? Is the TUI's pull-on-start model fast enough for all
   escalation types, or do some need the web UI's push model? See
   [Drafting Table](components.md#drafting-table).

### Q3: Multi-interface orchestration

When a project has many
   interfaces (API + CLI + web GUI), does the user dimension them
   sequentially or can they jump between interfaces? Is there a
   dependency graph?

### Q4: IdeaBot handoff format

The handoff mechanism is decided
   (manual: IdeaBot artifacts seed the initial Sketching session;
   automated integration later). Remaining question: how much of the
    Vision/Architecture can be pre-populated from IdeaBot's output?

### Q5: Kit package and future capabilities

Kits are versioned imports;
   their currently understood contents are proposed EARS/interfaces and
   Inspector definitions. Exact package/signature/dependency format and
   whether experience justifies skills, build/test conventions, projection
   defaults, mutation operators, or internal test controls remain open.
   See [Kits](components.md#kits).

## Specification storage

### Q7: Requirements storage format

JSONL is tentatively chosen for
   git-friendliness, but only works if requirements are independent
   records. Need to evaluate whether cross-references and immutable
   change-set history make a different format necessary.
   `ears-manager` abstracts the
   format, so this can be deferred. See the detailed note in
   [Phase 2](user-interaction-flow.md#phase-2-dimensioning).

## Building phase

### Q8: Building merge infrastructure

Worker B must produce a
   runnable artifact (container, listening API, executable CLI) before
   Worker A's tests can run. The merge step needs build/start
   orchestration, not just file concatenation. See
    [Phase 3](user-interaction-flow.md#phase-3-building-autonomous).

### Q9: Isolated vs. implementation-aware tests

A three-category
    taxonomy is established: (1) isolated interface tests (default,
    dual-model isolation holds), (2) implementation-aware tests generated
    by a separate read-only-code/test-only Worker, and (3) mutation
    testing (hidden audit). Requirement metadata records the mode and
    rationale; implementation-aware tests require isolated coverage where
    possible plus independent Test Completeness, Spec Conformance, and
    mutation gates. Remaining questions: what fraction of requirements
    require category 2, and which project types need standard internal
    test-control interfaces? See
   [Phase 3](user-interaction-flow.md#phase-3-building-autonomous).

### Q10: Mutation scale and operator policy

Inspector-only disposition
    is decided: `test-gap` must be killed, `spec-gap` blocks for
    Dimensioning, and `equivalent`/`tooling-invalid` require independent
    confirmation; no score or accepted-risk bypass exists. Remaining
    questions: which operators run per language, how are expensive
    mutants sampled or scheduled, what are Inspector retry budgets, and
    when is an operator version globally quarantined? See
    [Phase 3](user-interaction-flow.md#phase-3-building-autonomous).

### Q15: Phase 3 triage mechanism

When tests fail after merging
    Worker A's tests with Worker B's code, who or what decides
    whether the fault is in the tests, the code, or both? Options
    identified: another agent, a heuristic, or a hybrid. Forge's
    pattern (self-review 2 passes → CI fix loop 5 retries → AI
    review → human gate) is a concrete reference. The triage
    mechanism has access to both Workers' outputs inside the private
    integration environment, but a decided sanitizer restricts Worker
    feedback to status, fault class, requirement-level reasons, and
    recipient-owned diagnostics. The remaining question is how the
    triage decision itself is made. See
    [Phase 3](user-interaction-flow.md#phase-3-building-autonomous)
    and [Forge](related-work.md#forge-red-hat-israel--openstackshift-on-stack).

### Q19: Applicability metadata and semantic impact coverage

Change
    sets now distinguish changed requirements from applicable unchanged
    requirements. Every requirement has at least one machine-queryable
    applicability selector. Most name interfaces; project-wide and
    environmental requirements use an explicit project selector.
    Optional narrower scope selectors and explicit relationships improve
    precision.
    `ears-manager impact` mechanically generates conservative
    candidates, the Dimensioning agent supplements them semantically,
    and the user records reviewed applicable/not-applicable dispositions
    with the changed set. The minimum relationship vocabulary is decided:
    `depends-on`, `conflicts-with`, `supersedes`, and `related-to`.
    Remaining questions: what controlled vocabulary or selector model
    expresses capability/resource scope, which additional domain-specific
    relationships prove necessary, and how do evals measure obligations
    missed by semantic impact analysis?
    See
    [ongoing obligations](user-interaction-flow.md#ongoing-obligations).

### Q20: Latest-conformance derived view

Evidence fields and storage
    boundaries are decided: repo-resident artifacts bind requirement,
    specification, tested candidate, and supporting evidence; the WMS
    completion envelope adds the resulting merge commit. How should a
    convenient "latest known conformance" index be derived, cached, and
    invalidated without turning it back into mutable requirement state?

## Inspecting phase

### Q11: Test Completeness Inspector placement

Should test
    completeness checking happen in Phase 3 (Building, so gaps are
    caught and filled earlier in the loop) or Phase 4 (Inspecting,
    as an independent review)? Placing it in Phase 3 means faster
    feedback but the checker is no longer independent of the Workers.
    See [Phase 4](user-interaction-flow.md#phase-4-inspecting-autonomous).

### Q12: Inspector roster per project type

Not every Inspector is
    relevant for every prototype. A CLI tool doesn't need an
    Accessibility Inspector; a library doesn't need a Security
    Inspector scanning for OWASP web vulnerabilities. Architecture-
    derived defaults, Kit-proposed Inspectors, and explicit reviewed
    project policy are established inputs. The remaining question is
    precedence/conflict handling when those inputs disagree. See
    [Phase 4](user-interaction-flow.md#phase-4-inspecting-autonomous)
    and [Swarm Forge](related-work.md#swarm-forge-robert-c-martin).

### Q14: Finding Ledger backend and retention

The required finding
    schema, append-only event model, atomic idempotent writes, stable IDs,
    JSONL snapshot, rendered report view, and sanitized routing are
    decided. Remaining questions: which WMS backends can map this natively,
    when is an external coordinator required, and how long are raw
    content-addressed evidence blobs retained? See
    [Phase 4](user-interaction-flow.md#phase-4-inspecting-autonomous).

## Compliance

### Q13: HU-02 compliance

The multi-player workflow places the
    human checkpoint at PR merge time (when specs land on main).
    Everything after is autonomous execution of approved intent.
    This may satisfy HU-02, since the human explicitly approved the
    specifications that drive all subsequent autonomous action.
    However, this needs confirmation — ProtoBot's autonomous code
    generation likely rates "High risk" on the AI Agent Risk
    Evaluator, which may make an additional checkpoint unavoidable.

## Interface specifications

### Q18: CLI interface spec evaluation

`usage` (jdx.dev), docopt,
    and `wasi:cli` are listed as candidates for CLI interface
    specification. Need to evaluate which (if any) is suitable for
    ProtoBot's needs. See the interface-type taxonomy in
    [Specification Hierarchy](user-interaction-flow.md#specification-hierarchy).

---

## Related Documents

- [Overview](overview.md) — What ProtoBot is, guiding principles,
  and workflow summary
- [User Interaction Flow](user-interaction-flow.md) — Phase details
  and sequence diagrams
- [System Components](components.md) — Component architecture,
  interfaces, and cross-cutting concerns
- [Related Work](related-work.md) — Red Hat internal projects,
  external factory projects, and lessons learned
