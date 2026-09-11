# EARS and Review Reference

Read this reference when the main skill's compact workflow needs detailed
pattern selection, readiness contracts, supporting requirements, consistency
analysis, or response formatting.

## Pattern Selection

Choose an EARS pattern for its meaning, not for a keyword in the source.

- Use `When` for a discrete boundary event, not an enduring state.
- Use `While` for a state-driven requirement. Interpret `During` as an ongoing
  state or precondition in source material, but normalize it to `While` in the
  drafted requirement.
- Use `Where` only for an optional capability included in the product, not an
  ordinary runtime branch.
- Use `If ... then` for an unwanted failure, error, disturbance, deviation, or
  recovery condition.
- Use the ubiquitous form only when no condition limits the obligation.
- Use the complex form only when all conditions are genuinely required for one
  obligation. Split independent obligations instead.

If the pattern cannot be selected without guessing, use `unresolved`, ask the
pattern question, and keep the candidate `needs clarification`.

### Canonical examples

1. **Ubiquitous:** `The account service shall record the account identifier
   for every accepted account.` The obligation is unconditional.
2. **Event-driven:** `When a client submits a valid order, the order service
   shall return an order identifier.` Submission is a discrete event.
3. **State-driven:** `While the device is in maintenance mode, the controller
   shall inhibit remote actuation.` The obligation lasts for the state.
4. **Optional feature:** `Where offline export is included, the export
   service shall provide the current report as a downloadable file.` The
   behavior depends on product configuration.
5. **Unwanted behavior:** `If an authorization check fails, then the gateway
   shall deny the requested operation without disclosing protected data.`
6. **Complex:** `While the aircraft is on-ground, when reverse thrust is
   commanded, the control system shall enable deployment of the thrust
   reverser.` Both conditions are needed for this response.

An in-flight reverse-thrust command is a separate unwanted or state/event
requirement, not an exception hidden in the on-ground sentence.

When an unwanted condition is nested in a state-and-event scope, prefer a
separate `If ... then` requirement. Do not call a `While ... when ...`
sentence unwanted behavior without the unwanted-condition form.

## Candidate Quality

Draft the smallest EARS sentence that preserves intent. If a contract is
incomplete but the pattern is known, show a provisional sentence with the
unresolved condition or value explicitly marked and use `needs clarification`.

Check:

- System boundary.
- Responsible system.
- Actor.
- Affected interfaces.
- Explicit trigger, state, feature scope, or unwanted condition.
- Observable response with a concrete verb.
- Defined or questioned inputs, identities, data, domain, and lifecycle terms.
- Quantities, units, limits, ranges, rates, ordering, deadlines, durations, and
  tolerances when material.
- Alternate, failure, cancellation, timeout, retry, recovery, and partial
  completion behavior when material.
- Empty, minimum, maximum, duplicate, concurrent, and out-of-order cases when
  implied.
- No internal classes, functions, storage, frameworks, prompts, or algorithms
  in the normative sentence.
- No passive voice that hides the actor, unclear pronouns, unbounded lists,
  mixed rationale/design/obligation, stacked clauses, or nominalizations that
  hide independent decisions.

Use temporary labels such as `Candidate 1` for unlabeled input. Preserve host
IDs, tags, and trace links exactly. When revising an existing requirement show
the source, the proposed revision, and the risk removed.

## Readiness Gate

Do not mark `ready for review` unless both contracts pass. This status is not
approval.

### Implementability contract

Record, or explicitly mark not applicable:

- System boundary.
- Responsible system.
- Actor.
- Affected interfaces.
- Inputs and data definitions.
- Preconditions, state, feature scope, and trigger.
- Observable normal, alternate, and failure response.
- Quantities, units, limits, timing, ordering, and tolerances.
- Domain terms and dependencies an implementer would otherwise guess.
- Material unresolved decisions and conflicts.

The contract fails when a material item is unknown or two reasonable
implementations could differ in visible behavior.

### Verification contract

Record:

- Setup, state, input data, and stimulus.
- Observable evidence or oracle.
- Expected result.
- Explicit pass/fail criteria, including scope, thresholds, units, and timing.
- Boundary, negative, and failure cases when material.
- Required data, instrumentation, assessment, or external evidence.
- Whether the requirement is observable at the named boundary alone; if not,
  the internal evidence needed and why.

The contract fails when an evaluator cannot tell what to observe, what result
is expected, or what counts as pass or fail.

Describe observability without choosing a host-specific verification mode. A
host may map the answer to its own verification metadata.

Use exactly these statuses:

- `needs clarification`: a material question, conflict, undefined term, or
  incomplete contract remains.
- `candidate`: both contracts are complete, but the host has not approved the
  draft.
- `ready for review`: both contracts pass and no material finding blocks
  review; this is not approval.

These statuses describe the elicitation response. They are not persisted
requirement lifecycle states.

Use `needs clarification` for an incomplete contract. These claims fail the
gate until refined:

- `The UI must be responsive.` Ask for interaction, workload, device scope,
  response-time measure, and pass/fail observation.
- `The system must not contain security vulnerabilities.` Ask for threat scope,
  vulnerability classes, assessment method, severity threshold, and release
  boundary. Decompose it into observable behaviors and scoped assurance
  criteria.

Flag `quickly`, `appropriately`, `user-friendly`, `as needed`, `normally`,
`securely`, `support`, `handle`, `etc.`, and `reasonable` unless each has an
agreed measure. Preserve the source rationale separately.

## Supporting Requirements

Suggestions must name the source behavior, missing behavior, relationship, and
reason. Keep them outside the candidate list until accepted.

The relationship labels below describe the role of a suggested companion in
this response. They are not a persistent requirements-store relationship
vocabulary. Do not invent stable IDs or persisted edges; preserve host-supplied
metadata and let the host map these labels when it defines a mapping. When
revising an identified requirement, show its source and proposed revision
rather than inferring a relationship that the host did not provide.

Use these relationship labels:

- **Required companion:** needed for completeness.
- **Failure-path companion:** defines material failure or recovery.
- **Boundary companion:** defines an implied limit or edge case.
- **Interface companion:** defines behavior at a system or external boundary.
- **Operational companion:** defines an observable operational quality implied
  by the request.
- **Optional consideration:** plausible but not justified; ask whether it is in
  scope.

Consider normal, alternate, negative, recovery, validation, missing or
malformed values, duplicates, boundaries, lifecycle, authentication,
authorization, ownership, persistence, consistency, idempotence, ordering,
concurrency, dependency failures, timeouts, retries, cancellation, partial
completion, performance, capacity, latency, availability, rate limits,
security, privacy, auditability, retention, accessibility, localization,
compatibility, and observability only when grounded in the source.

## Consistency Analysis

Normalize each candidate by system, feature scope, preconditions, states,
trigger or unwanted condition, response, timing, quantities, ranges, ordering,
quality constraints, and host or temporary ID. Compare conditions semantically,
not only textually.

Report these finding kinds when present:

- **Conflict:** applicable requirements cannot both be satisfied.
- **Overlap:** requirements may describe the same behavior.
- **Duplicate:** same or near-identical requirements may drift.
- **Gap:** an important behavior is undefined.
- **Precedence question:** exception or evaluation order is undefined.
- **Intentional exception:** an explicit state, feature, priority, or exception
  resolves an apparent conflict.
- **Infeasible, vacuous, or unreachable:** the condition or obligation cannot
  be meaningfully exercised.

Check contradictory responses, normal/failure overlap, state/event overlap,
inconsistent units/ranges/deadlines/retries/cardinalities, allow/forbid
contradictions, optional-feature conflicts, incompatible interface contracts,
and missing boundary/failure/lifecycle behavior.

Every finding shows affected labels or excerpts, conditions, consequence, and
focused resolution options. Options include narrowing, splitting, an explicit
exception, precedence, consolidation, or asking the user to choose. Never
invent priority.

## Portable Response

Use these exact headings unless the host supplies a compatible format:

```text
# Requirements Elicitation Package
## Understanding
## Clarifying Questions
## Candidate Requirements
### Candidate 1
- Status: needs clarification | candidate | ready for review
- Template: one of six patterns | unresolved
- Requirement: EARS sentence, or explicit blocked-drafting note
- Source intent and rationale
- Implementability contract
  - System boundary:
  - Responsible system:
  - Actor:
  - Affected interfaces:
  - Inputs and data definitions:
  - Preconditions, state, feature scope, and trigger:
  - Observable normal, alternate, and failure response:
  - Quantities, units, limits, timing, ordering, and tolerances:
  - Domain terms, dependencies, and unresolved decisions:
- Verification contract
  - Setup, state, input data, and stimulus:
  - Observable evidence or oracle:
  - Expected result:
  - Pass/fail criteria, including scope, thresholds, units, and timing:
  - Boundary, negative, and failure cases:
  - Required data, instrumentation, assessment, or external evidence:
  - Observable at the named boundary alone: yes | no
  - If no, internal evidence needed and rationale:
## Suggested Supporting Requirements
## Consistency Findings
## Assumptions
## Rationale and Context
## Review Status
```

Do not omit empty sections. Preserve host metadata without inventing approval
states or storage formats. For non-interactive use, return unanswered
questions and explicit statuses. Do not turn verification contracts into
Gherkin or implementation-specific test steps.
