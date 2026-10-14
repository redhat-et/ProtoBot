# Requirements Elicitation Package

## Understanding

**Requested outcome:** The report should display a warning to users when its
content may be out of date (stale). The product owner has not yet decided two
material facts: (1) how old a report must be before it is considered stale, and
(2) whether the warning is mandatory (always shown when the threshold is met) or
optional (can be dismissed or suppressed).

**Proposed system boundary:** The report (a named system component or UI
surface) is responsible for displaying the staleness warning. The report is
treated as the single responsible system unless additional information
establishes that a separate service, scheduler, or notification subsystem owns
this behavior.

**Source type:** This is a partial goal statement with two explicitly
unresolved product decisions. It is not a normative obligation yet. The
uncertainty is acknowledged by the product owner and must not be resolved by
assumption.

---

## Clarifying Questions

**Q1 — Staleness threshold (blocking)**
What age threshold makes a report stale? For example: more than 24 hours since
last data refresh, more than N business days since generation, or some other
measurable criterion? The implementability contract and verification contract
cannot be completed without this value and its units.

**Q2 — Mandatory vs. optional warning (blocking)**
Must the warning appear whenever the threshold is met (mandatory, with no
user-dismissal path), or may the user dismiss or suppress it? This determines
whether the requirement is an unconditional obligation or a conditional one, and
whether a separate requirement is needed to govern dismissal.

**Q3 — Warning content and form**
What does the warning communicate? For example, must it state the data age, the
threshold, a last-refresh timestamp, or only a generic "may be stale" label?
This affects implementability and verification.

**Q4 — Report identity**
Does "the report" refer to a single report type, every report in the system, or
a configurable subset? If multiple report types exist, does the staleness rule
apply uniformly?

**Q5 — What clock or event defines age?**
Is age measured from the time the underlying data was last refreshed, the time
the report was generated, or another observable event? Two implementers may
choose differently without this definition.

---

## Candidate Requirements

### Candidate 1

- **Status:** needs clarification
- **Template:** unresolved — cannot select between Event-driven, State-driven,
  Unwanted behavior, or Ubiquitous without knowing whether the warning is
  mandatory, what triggers it, and what threshold defines "stale."
- **Requirement:** *(Drafting is blocked. A provisional sketch is shown below
  for review only — it is not a normative sentence.)*

  > Provisional sketch (not normative): While the report data age exceeds
  > [THRESHOLD — undefined], the report shall display a staleness warning.

  The bracketed value is a material open decision. The EARS pattern, the
  normative sentence, and both contracts cannot be finalized until Q1 and Q2
  are answered.

- **Source intent and rationale:** The product owner wants users to know when
  the report content may not reflect current data, reducing the risk of
  decisions based on stale information. The product owner has not committed to
  a threshold or to whether the warning is always shown.

- **Implementability contract**
  - System boundary: The report surface (UI, document, or API response — form
    not yet established).
  - Responsible system: The report.
  - Actor: The user viewing the report.
  - Affected interfaces: The report display interface (visual, document, or
    API). Specific interface not yet defined.
  - Inputs and data definitions: A measurable "data age" value — the elapsed
    time since an observable reference event (Q5). **Open: reference event
    undefined.**
  - Preconditions, state, feature scope, and trigger: The condition that makes
    a report stale. **Open: staleness threshold and units undefined (Q1).**
  - Observable normal, alternate, and failure response: Display of a staleness
    warning. **Open: mandatory vs. optional undefined (Q2); warning content
    undefined (Q3).**
  - Quantities, units, limits, timing, ordering, and tolerances: **Open:
    threshold value and units undefined (Q1).**
  - Domain terms, dependencies, and unresolved decisions: "Stale," "warning,"
    "data age," "mandatory," "report" — all unresolved. See Q1–Q5.

- **Verification contract**
  - Setup, state, input data, and stimulus: **Open: cannot specify setup
    without the threshold (Q1) and the reference event (Q5).**
  - Observable evidence or oracle: The presence or absence of a staleness
    warning on the report surface.
  - Expected result: Warning is displayed when data age exceeds the threshold.
    **Open: pass/fail boundary undefined.**
  - Pass/fail criteria, including scope, thresholds, units, and timing:
    **Open: threshold undefined (Q1).**
  - Boundary, negative, and failure cases: Case where age equals threshold
    exactly (boundary); case where age is below threshold (warning absent);
    case where age data is unavailable. All unresolvable without Q1 and Q5.
  - Required data, instrumentation, assessment, or external evidence: A
    verifiable timestamp or elapsed-time value observable at the report
    boundary.
  - Observable at the named boundary alone: yes — the warning is a visible
    property of the report surface, provided the age value is also observable
    there.
  - If no, internal evidence needed and rationale: N/A (boundary-observable,
    subject to the age value being surfaced).

---

## Suggested Supporting Requirements

**SR-A — Boundary companion** (relates to Candidate 1)
*Missing behavior:* What the report shall do when the data age cannot be
determined (e.g., no timestamp is available). Without this, two implementers
would make different choices: one suppresses the warning, one shows it
unconditionally.
*Suggestion:* If the report cannot determine whether its data is current, then
the report shall display a staleness warning.
*Reason:* Absence of an age value is arguably the most conservative case for
staleness and should be explicitly addressed before implementation.

**SR-B — Required companion** (relates to Candidate 1, conditional on Q2)
*Missing behavior:* If the warning is determined to be dismissible (Q2 =
optional), a requirement is needed to define how and for how long a user may
suppress the warning, and whether the warning reappears on the next session or
the next threshold crossing.
*Suggestion:* Add a companion requirement governing dismissal scope, persistence,
and re-display conditions once the mandatory/optional decision is made.
*Reason:* Without this companion, the dismissal behavior is undefined and
two implementers will produce different results.

**SR-C — Operational companion** (relates to Candidate 1)
*Missing behavior:* The report data-age value must be observable at the report
boundary for the staleness condition to be verifiable. If the age is not
currently surfaced, a requirement to expose it may be needed.
*Suggestion:* Consider whether a separate requirement is needed for the report
to expose the data-refresh timestamp (or computed age) so that the staleness
condition can be independently verified.
*Reason:* Without an observable age value, the staleness warning cannot be
verified by an evaluator examining only the report boundary.

---

## Consistency Findings

**Finding 1 — Gap: threshold is undefined**
Candidate 1 cannot be reconciled with any other system behavior because the
staleness threshold is not defined. If a threshold is later introduced in a
different requirement (e.g., a data-refresh SLA), it should be confirmed
whether the staleness definition here must align with that value.

**Finding 2 — Precedence question: mandatory vs. optional**
If the warning is optional (Q2), an explicit precedence is needed between the
"show warning" obligation and any user preference or role-based permission to
suppress it. Without this, two valid interpretations conflict: one always shows
the warning to all users, one respects user-level suppression settings.

No conflicts or duplicates exist in the current candidate set because only one
candidate has been drafted.

---

## Assumptions

**A1 (provisional):** "The report" refers to a single, identifiable system
component or UI surface. If multiple report types exist, this assumption must
be revisited.

**A2 (provisional):** "Age" is a duration measured from a single observable
reference event (e.g., last data refresh). If age is derived from multiple
events, the calculation rule must be defined.

**A3 (provisional):** The staleness condition is evaluated at report-display
time, not at a scheduled interval. This assumption affects whether the
requirement is event-driven (triggered by display) or state-driven (maintained
continuously). It must be confirmed by the product owner.

---

## Rationale and Context

The product owner's intent is to protect users from acting on outdated report
data. The warning is the proposed mechanism. The product owner has explicitly
deferred two decisions: the staleness threshold and whether the warning is
mandatory. These are not implementation details; they are normative policy
choices that determine the EARS pattern, the normative sentence, and the
verification criteria. They must be resolved before a normative requirement
can be finalized.

The source statement "the report probably should show a warning" uses modal
language ("probably," "should") that signals deliberation, not an obligation.
This has been treated as a goal in need of clarification, not a normative
`shall` obligation.

---

## Review Status

| Item | Status | Blocking reason |
|---|---|---|
| Candidate 1 | needs clarification | Q1 (threshold), Q2 (mandatory/optional), Q3 (content), Q5 (reference event) all open; both contracts incomplete |
| SR-A | suggestion | Pending acceptance and Q1/Q5 resolution |
| SR-B | suggestion | Pending Q2 decision |
| SR-C | suggestion | Pending Q4/interface definition |

**No candidate is ready for review.** All candidates and supporting
requirements remain blocked until the product owner answers at minimum Q1
(staleness threshold) and Q2 (mandatory vs. optional). Answering Q3
(warning content) and Q5 (reference event) is also required to pass the
implementability and verification contracts.
