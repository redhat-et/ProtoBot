# Requirements Elicitation Package

## Understanding

**Requested outcome:** The report service must return a report within a defined
time limit whenever a valid report request arrives.

**Proposed system boundary:** The report service — the component that receives
a valid report request at its public interface and returns the corresponding
report to the caller.

**Input artifact identification:**

| Label | Text | Kind |
|-------|------|------|
| R1 | "When a valid report request arrives, the report service shall return the report within 1 second." | Behavioral obligation (event-driven, with timing constraint) |
| R2 | "When a valid report request arrives, the report service shall return the report within 5 seconds." | Behavioral obligation (event-driven, with timing constraint) |

Both R1 and R2 assert the same trigger (`a valid report request arrives`),
the same system (`the report service`), and the same response (`return the
report`), but they specify contradictory deadlines: **1 second** versus
**5 seconds**. The user has confirmed that both apply to the same service
boundary and workload. This is a direct conflict. Neither requirement can be
accepted as-is; both are held at `needs clarification` until the conflict is
resolved.

---

## Clarifying Questions

**Q1 (Conflict resolution — blocking):** R1 and R2 require the same response
for the same event on the same system but specify different deadlines (1 s and
5 s). They cannot both be correct as written. Which of the following best
describes the intent?

- **(a) One deadline replaces the other.** If so, which deadline is correct —
  1 second or 5 seconds — and which requirement should be superseded?
- **(b) The two deadlines apply to different workload or report classes** (for
  example, simple vs. complex reports, small vs. large datasets, cached vs.
  uncached results). If so, how should each class be identified at the service
  boundary so that a verifier can distinguish them?
- **(c) One deadline is a target (goal) and the other is a hard limit (floor
  or ceiling).** If so, which is which, and what are the consequences of
  meeting only the weaker constraint?
- **(d) The two requirements belong to different operating modes or
  configurations** (for example, normal load vs. degraded mode). If so, how
  is each mode defined and signalled at the boundary?

*This question must be answered before either candidate can advance beyond
`needs clarification`.*

**Q2 (Definition of "valid report request"):** What makes a report request
valid? Specifically: is validity determined by the service itself, by an
upstream system, or by both? What observable property at the service boundary
distinguishes a valid request from an invalid one?

**Q3 (Definition of "return the report"):** Does "return the report" mean the
full report payload is delivered in the same synchronous response, or is an
acknowledgement with a reference (e.g., a job ID for asynchronous retrieval)
also acceptable? The deadline meaning differs materially between the two
interpretations.

**Q4 (Deadline measurement):** From which point is the deadline measured —
the moment the service receives the request, the moment the service begins
processing, or some other point? To which point is it measured — the moment
the first byte of the response is sent, or the moment the complete response is
delivered?

**Q5 (Concurrent and high-load behavior):** Does the deadline apply to every
individual report request independently, or only when the service is operating
below a defined load threshold? If the deadline applies under all conditions,
what happens when it cannot be met (is there a failure-path obligation)?

---

## Candidate Requirements

### Candidate 1 (derived from R1)

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** *(blocked — conflict with Candidate 2; see Consistency
  Findings)*

  Provisional (do not implement until conflict is resolved):
  > When a valid report request arrives, the report service shall return the
  > report within 1 second.

- **Source intent and rationale:** R1 asserts a 1-second response deadline for
  any valid report request to the report service. The rationale for choosing
  1 second over 5 seconds is not stated in the source material.

- **Implementability contract**
  - System boundary: The public interface of the report service where valid
    report requests are received and reports are returned.
  - Responsible system: The report service.
  - Actor: Any caller that submits a valid report request.
  - Affected interfaces: The request-receipt interface and the
    report-return interface of the report service.
  - Inputs and data definitions: "Valid report request" — definition open
    (see Q2). "Report" — payload definition open (see Q3).
  - Preconditions, state, feature scope, and trigger: Trigger is the arrival
    of a valid report request. No precondition or feature scope stated.
  - Observable normal, alternate, and failure response: Normal — report
    returned within 1 second. Alternate and failure paths undefined (see Q5).
  - Quantities, units, limits, timing, ordering, and tolerances: 1 second.
    Measurement start and end points open (see Q4). Tolerance undefined.
  - Domain terms, dependencies, and unresolved decisions: **Direct conflict
    with R2 (5-second deadline for the same trigger and system). Resolution
    required (see Q1).** "Valid report request" undefined (Q2). Delivery
    mode undefined (Q3). Measurement points undefined (Q4). Failure-path
    behavior undefined (Q5).

- **Verification contract**
  - Setup, state, input data, and stimulus: Submit a valid report request to
    the report service.
  - Observable evidence or oracle: The elapsed time between the defined start
    point and the defined end point of the service's response.
  - Expected result: The complete report is returned within 1 second.
  - Pass/fail criteria, including scope, thresholds, units, and timing: Pass
    if elapsed time ≤ 1 second; fail otherwise. Measurement points and
    tolerance are open (Q4).
  - Boundary, negative, and failure cases: Behavior at exactly 1 second
    undefined. Behavior when the deadline cannot be met is undefined (Q5).
  - Required data, instrumentation, assessment, or external evidence: A
    clock or timer observable at the service boundary; definition of "valid
    report request" for test setup; definition of "return the report" to
    identify the end event.
  - Observable at the named boundary alone: yes — provided measurement
    points are defined.
  - If no, internal evidence needed and rationale: N/A (conditional on Q4
    resolution).

---

### Candidate 2 (derived from R2)

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** *(blocked — conflict with Candidate 1; see Consistency
  Findings)*

  Provisional (do not implement until conflict is resolved):
  > When a valid report request arrives, the report service shall return the
  > report within 5 seconds.

- **Source intent and rationale:** R2 asserts a 5-second response deadline for
  any valid report request to the report service. The rationale for choosing
  5 seconds over 1 second is not stated in the source material.

- **Implementability contract**
  - System boundary: The public interface of the report service where valid
    report requests are received and reports are returned.
  - Responsible system: The report service.
  - Actor: Any caller that submits a valid report request.
  - Affected interfaces: The request-receipt interface and the
    report-return interface of the report service.
  - Inputs and data definitions: "Valid report request" — definition open
    (see Q2). "Report" — payload definition open (see Q3).
  - Preconditions, state, feature scope, and trigger: Trigger is the arrival
    of a valid report request. No precondition or feature scope stated.
  - Observable normal, alternate, and failure response: Normal — report
    returned within 5 seconds. Alternate and failure paths undefined (see Q5).
  - Quantities, units, limits, timing, ordering, and tolerances: 5 seconds.
    Measurement start and end points open (see Q4). Tolerance undefined.
  - Domain terms, dependencies, and unresolved decisions: **Direct conflict
    with R1 (1-second deadline for the same trigger and system). Resolution
    required (see Q1).** "Valid report request" undefined (Q2). Delivery
    mode undefined (Q3). Measurement points undefined (Q4). Failure-path
    behavior undefined (Q5).

- **Verification contract**
  - Setup, state, input data, and stimulus: Submit a valid report request to
    the report service.
  - Observable evidence or oracle: The elapsed time between the defined start
    point and the defined end point of the service's response.
  - Expected result: The complete report is returned within 5 seconds.
  - Pass/fail criteria, including scope, thresholds, units, and timing: Pass
    if elapsed time ≤ 5 seconds; fail otherwise. Measurement points and
    tolerance are open (Q4).
  - Boundary, negative, and failure cases: Behavior at exactly 5 seconds
    undefined. Behavior when the deadline cannot be met is undefined (Q5).
  - Required data, instrumentation, assessment, or external evidence: A
    clock or timer observable at the service boundary; definition of "valid
    report request" for test setup; definition of "return the report" to
    identify the end event.
  - Observable at the named boundary alone: yes — provided measurement
    points are defined.
  - If no, internal evidence needed and rationale: N/A (conditional on Q4
    resolution).

---

## Suggested Supporting Requirements

**SR-A — Failure-path companion (for whichever deadline is confirmed)**
*Source behavior:* The confirmed deadline requirement (Candidate 1 or 2).
*Missing behavior:* No requirement defines what the report service shall do
when it cannot return the report within the deadline.
*Reason:* Without a failure-path obligation, two implementers could choose
different visible behaviors (silent timeout, error response, partial result,
retry) and both would be compliant. This creates unverifiable behavior.
*Suggested form (provisional):* `If the report service cannot return the
report within <confirmed deadline>, then the report service shall return an
error response to the caller indicating that the request could not be
completed within the required time.`

**SR-B — Boundary companion (for whichever deadline is confirmed)**
*Source behavior:* The confirmed deadline requirement.
*Missing behavior:* No requirement defines what "valid report request" means
at the service boundary.
*Reason:* Without this definition, a verifier cannot construct a conforming
test stimulus, and an implementer cannot determine which requests the deadline
applies to. If the deadline applies only to valid requests, invalid-request
behavior is also undefined.
*Suggested form (provisional):* A separate requirement or definition
specifying the observable properties that distinguish a valid report request
from an invalid one, and the service's required response to an invalid request.

**SR-C — Operational companion (conditional on Q5)**
*Source behavior:* The confirmed deadline requirement.
*Missing behavior:* No requirement constrains the workload or concurrency
conditions under which the deadline must hold.
*Reason:* Without a stated load condition, a verifier cannot determine whether
a deadline breach under high concurrency is a failure or within the allowed
operating envelope. This is an optional consideration — ask whether a maximum
concurrent-request or throughput bound is in scope.

---

## Consistency Findings

### Finding 1 — **Conflict (blocking)**

| Property | Candidate 1 (R1) | Candidate 2 (R2) |
|----------|-----------------|-----------------|
| System | Report service | Report service |
| Trigger | Valid report request arrives | Valid report request arrives |
| Response | Return the report | Return the report |
| Deadline | **1 second** | **5 seconds** |

**Consequence:** Both requirements apply to exactly the same trigger, system,
and response. No system can simultaneously satisfy a 1-second deadline and a
5-second deadline as independent normative obligations for the same event —
satisfying the 1-second constraint satisfies both, but the 5-second constraint
adds no independent normative content; if the 1-second constraint is binding,
the 5-second figure is vacuous. If the 5-second constraint is binding and the
1-second figure is a goal rather than an obligation, then R1 is not a
normative requirement as written. In either case, the two cannot coexist as
independently normative requirements without one being vacuous or one being
violated by a conforming implementation.

**Resolution options:**

1. **Supersede:** Confirm that one deadline is correct and withdraw the other.
   Identify which requirement is superseded and update traceability.
2. **Partition by workload class:** Introduce a property of the request (e.g.,
   report type, data volume, cache state) that routes requests to the
   1-second or 5-second obligation. Both candidates are then re-scoped and
   can coexist without conflict.
3. **Reframe as target and limit:** Designate one deadline as a performance
   goal (non-normative) and the other as the normative hard limit. The
   goal-level statement should be removed from the requirement set or clearly
   labelled non-normative.
4. **Operating-mode split:** If one deadline applies in a degraded or
   maintenance mode, introduce an explicit state precondition so the two
   obligations are mutually exclusive.

*Neither candidate can advance to `candidate` or `ready for review` until
this conflict is resolved.*

---

## Assumptions

**A1:** Both R1 and R2 are intended to be normative obligations, not one being
a goal and the other a limit, unless the user confirms otherwise (see Q1-c).

**A2:** "Return the report" means a complete, usable report payload is
delivered in the response — not merely an acknowledgement or a reference for
asynchronous retrieval — unless the user confirms otherwise (see Q3).

**A3:** The deadline is measured from request receipt to complete response
delivery at the service boundary, unless the user defines different measurement
points (see Q4).

**A4:** The deadline applies to each individual report request independently
of concurrent load, unless the user introduces a load condition (see Q5).

---

## Rationale and Context

The core problem is a **direct deadline conflict** on a single observable
behavior. Such conflicts are common when requirements originate from different
sources (e.g., a UX team setting a user-experience target and an operations
team setting an infrastructure SLA), when a requirement is revised without
retiring the prior version, or when different stakeholders have different
mental models of what "the report service" processes.

The resolution must come from the requester: the skill cannot invent a
precedence rule. The most important action is answering Q1. All other
questions (Q2–Q5) are secondary but will block the readiness gate for any
resulting candidates if left unanswered.

---

## Review Status

| Candidate | Status | Blocking item |
|-----------|--------|---------------|
| Candidate 1 (R1, 1 s) | **needs clarification** | Conflict with R2 (Finding 1); Q1 unanswered; Q2, Q3, Q4, Q5 open |
| Candidate 2 (R2, 5 s) | **needs clarification** | Conflict with R1 (Finding 1); Q1 unanswered; Q2, Q3, Q4, Q5 open |

**No candidate is ready for review.** The blocking conflict (Finding 1) must
be resolved before either candidate can be refined, and Q2–Q5 must be
answered before either can pass the readiness gate.
