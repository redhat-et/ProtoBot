# Requirements Elicitation Package

## Understanding

**Requested outcome:** A search system that returns matching records quickly,
presenting the newest matching records to the user after a search is submitted.

**Proposed system boundary:** The search system — the component that accepts a
user search submission and returns an ordered list of matching records.

**Design ideas embedded in the source (separated from obligations):**
The source proposes two specific implementation mechanisms:
- A Redis cache with a five-minute TTL
- GraphQL as the query interface

These are design choices, not externally observable behaviors. A verifier
cannot observe "Redis" or "GraphQL" at the user-facing boundary; they can
observe response latency, result ordering, and result freshness. These
implementation details are kept out of the normative sentences below.

**Behavioral obligations extracted from the source:**
1. When a user submits a search, the search system shall return matching
   records.
2. The results shall be ordered so that the newest matching records appear
   first.
3. Results shall return quickly (unquantified — flagged as needing
   clarification).

---

## Clarifying Questions

**Q1 (Candidate 1 — response time).** The source says results should return
"quickly." What is the maximum acceptable elapsed time, measured from when the
user submits a search to when the first results are visible? What threshold
distinguishes pass from fail, and does it differ for cached versus uncached
responses?

**Q2 (Candidate 2 — sort order definition).** "Newest" matching records: which
timestamp defines recency — record creation time, last-modification time, or
another field? If two records share the same timestamp, what is the tie-break
rule?

**Q3 (Candidate 2 — result set scope).** Should all matching records be
returned and sorted, or should the system return only a subset (for example, a
first page)? If a subset, what determines which records are included before
sorting?

**Q4 (Candidate 1 — what constitutes a "matching" record).** What makes a
record match a search? Is matching defined by full-text, field-level
comparison, prefix, relevance score, or another rule? The answer determines
whether the response is verifiable.

**Q5 (Candidate 1 — empty results).** When a submitted search matches no
records, what shall the system return? An empty result set, an informational
message, or some other response?

**Q6 (general — stale results acceptability).** The source implies results may
be served from a cache. Is it acceptable to return results that do not reflect
records created or modified after the cache was populated? If so, what is the
maximum acceptable staleness window?

---

## Candidate Requirements

### Candidate 1

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** When a user submits a search, the search system shall return
  all records that match the submitted search criteria within [unresolved:
  maximum response time].
- **Source intent and rationale:** The source requests that search results
  "return quickly." The user-visible obligation is that the system responds to
  a submitted search with matching records. The mechanism proposed in the source
  (Redis cache, GraphQL) is a design idea, not an observable behavior; it is
  kept out of the normative sentence. "Quickly" is unquantified and blocks the
  verification contract.
- **Implementability contract**
  - System boundary: The search system, at the interface visible to the
    submitting user.
  - Responsible system: Search system.
  - Actor: User (submitting a search).
  - Affected interfaces: The user-facing search interface (submission and
    response).
  - Inputs and data definitions: A submitted search — the specific form
    (keyword string, structured query, filters) is unresolved (see Q4). The
    record set to search is assumed to be the system's persistent record store;
    scope is unresolved.
  - Preconditions, state, feature scope, and trigger: Trigger is user
    submission of a search. No precondition other than the system being
    operational is stated; authentication or authorization requirements are
    unresolved.
  - Observable normal, alternate, and failure response: Normal — a set of
    matching records is returned. Alternate — no matching records exist (see
    Q5, unresolved). Failure — system or dependency error response behavior is
    unresolved.
  - Quantities, units, limits, timing, ordering, and tolerances: Maximum
    acceptable elapsed time from submission to first results visible is
    unresolved (Q1). Whether the response is a complete set or a page is
    unresolved (Q3).
  - Domain terms, dependencies, and unresolved decisions: "Matching" is
    undefined (Q4). "Quickly" is unquantified (Q1). Acceptable result staleness
    is unresolved (Q6).
- **Verification contract**
  - Setup, state, input data, and stimulus: A populated record store; a user
    submits a search query.
  - Observable evidence or oracle: A response containing a set of records is
    returned at the user-facing boundary; elapsed time is measurable from
    submission to response receipt.
  - Expected result: The returned records all satisfy the match criterion;
    elapsed time is within the threshold.
  - Pass/fail criteria, including scope, thresholds, units, and timing:
    Unresolved — the response-time threshold (Q1) and the definition of
    "match" (Q4) must be established before pass/fail can be determined.
  - Boundary, negative, and failure cases: Zero-result searches (Q5),
    maximum record-set size, and dependency-failure behavior are unresolved.
  - Required data, instrumentation, assessment, or external evidence: A timer
    measuring elapsed time from submission event to response receipt at the
    user boundary; a defined match oracle.
  - Observable at the named boundary alone: yes
  - If no, internal evidence needed and rationale: N/A

---

### Candidate 2

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** When a user submits a search, the search system shall
  present the matching records ordered by [unresolved: creation time |
  last-modification time | other timestamp] from newest to oldest.
- **Source intent and rationale:** The source states results should show "the
  newest matching records." This is a sort-order obligation on the result
  presentation. "Newest" is ambiguous — the timestamp field and tie-break rule
  are unresolved (Q2). "Show the newest" could also mean return only the top-N
  newest rather than all matches sorted; that scope question is unresolved (Q3).
- **Implementability contract**
  - System boundary: The search system, at the user-facing response boundary.
  - Responsible system: Search system.
  - Actor: User (receiving search results).
  - Affected interfaces: The user-facing result presentation interface.
  - Inputs and data definitions: The set of records matching the submitted
    search (as defined by Candidate 1). Each record must expose a timestamp
    field whose definition is unresolved (Q2).
  - Preconditions, state, feature scope, and trigger: Trigger is the same
    user search submission as Candidate 1. The sort obligation applies to the
    result set produced in response to that submission.
  - Observable normal, alternate, and failure response: Normal — records are
    presented with the newest first. Alternate — a single matching record
    requires no ordering; an empty result set requires no ordering. Tie-break
    behavior for equal timestamps is unresolved (Q2).
  - Quantities, units, limits, timing, ordering, and tolerances: The timestamp
    field and its unit (epoch ms, ISO 8601, etc.) are unresolved. Whether all
    matches or only top-N are presented is unresolved (Q3).
  - Domain terms, dependencies, and unresolved decisions: "Newest" (Q2), scope
    of returned results (Q3), tie-break rule (Q2).
- **Verification contract**
  - Setup, state, input data, and stimulus: A record store containing multiple
    matching records with distinct and equal timestamps; a user submits a search
    that matches those records.
  - Observable evidence or oracle: The ordered sequence of records in the
    response, inspectable at the user-facing boundary.
  - Expected result: Records appear in descending timestamp order (newest
    first); tie-break ordering is consistent with the agreed rule.
  - Pass/fail criteria, including scope, thresholds, units, and timing:
    Unresolved — the timestamp field definition (Q2) and tie-break rule must be
    established. Pass requires that the first record in the response has a
    timestamp ≥ all others in the same response.
  - Boundary, negative, and failure cases: Single matching record; all records
    sharing the same timestamp; empty result set.
  - Required data, instrumentation, assessment, or external evidence: Access to
    the timestamp field value of each returned record at the response boundary,
    or an agreed oracle that can evaluate ordering correctness.
  - Observable at the named boundary alone: yes, provided the timestamp field
    is included in the response.
  - If no, internal evidence needed and rationale: N/A

---

## Suggested Supporting Requirements

**S1 — Failure-path companion (Candidate 1).**
*Missing behavior:* What the search system shall do when a dependency required
to produce results is unavailable (e.g., the record store is unreachable).
*Reason:* Without a defined failure response, two implementers will choose
different visible behaviors (error message, partial results, silent empty set),
making the system unverifiable at the boundary.
*Suggested form (provisional):* If the search system cannot retrieve records
within [unresolved: timeout], then the search system shall return an error
response that does not present partial results as complete.

**S2 — Boundary companion (Candidate 1).**
*Missing behavior:* Behavior when the submitted search is empty, malformed, or
exceeds a defined length limit.
*Reason:* The source does not constrain the input. An implementer must decide
unilaterally whether to reject, interpret, or pass through these inputs.
*Suggested form (provisional):* If the submitted search criteria are empty or
malformed, then the search system shall return an informative response that
identifies the input problem without exposing internal system detail.

**S3 — Operational companion (Candidate 1).**
*Missing behavior:* A quantified response-time obligation.
*Reason:* "Quickly" is the stated goal. Without a measurable threshold the
requirement is unverifiable. This companion captures the performance obligation
once Q1 is answered.
*Suggested form (provisional, pending Q1):* When a user submits a search, the
search system shall return the first results within [agreed threshold, e.g.,
2 seconds] as measured from submission to first result visible at the
user-facing boundary.

**S4 — Optional consideration (Candidate 1).**
*Missing behavior:* Whether results served from a cache may be stale relative
to the current record store state, and what the maximum acceptable staleness
window is.
*Reason:* The source implies caching. If stale results are permissible, this
is a visible behavioral contract (users may not see records created after the
cache was populated). If not permissible, the cache TTL is an internal tuning
detail with no behavioral relevance.
*Ask:* Is serving results that do not reflect records created or modified
within the last [N minutes] acceptable? (See Q6.)

---

## Consistency Findings

**Finding 1 — Gap: quantified performance obligation is absent.**
Candidate 1 references a response-time goal inherited from "quickly" in the
source, but no measurable threshold appears in either candidate. S3 is
suggested to close this gap once Q1 is answered.

**Finding 2 — Overlap risk: Candidates 1 and 2 share the same trigger.**
Both candidates are triggered by "user submits a search." They describe
different responses (record retrieval vs. result ordering) and are intentionally
split because they can be implemented and verified independently. No conflict;
the split is deliberate. The combined behavior is: retrieve matching records
AND present them ordered newest-first.

**Finding 3 — Precedence question: cache staleness vs. result completeness.**
If a caching layer is used (the source's stated design intent), results
returned by Candidate 1 may not include records added after the cache was
populated. This creates a potential tension between the completeness of
"all matching records" (Candidate 1) and any staleness window accepted in S4.
Resolution requires an answer to Q6 and an explicit staleness bound or a
requirement that results reflect the state of the record store at the time of
submission.

**Finding 4 — Gap: empty-result behavior is undefined.**
Neither candidate specifies what the system returns when no records match the
submitted search. S2 partially addresses malformed input; the empty-result
case is covered by Q5 and needs an explicit requirement.

---

## Assumptions

**A1.** "Redis cache" and "GraphQL" in the source are design proposals, not
externally observable behavioral obligations. They are treated as rationale for
the performance goal and are excluded from normative sentences. If either is
intended as an external interface contract (e.g., the client is required to
speak GraphQL), that changes the system boundary and an interface companion
requirement would be needed.

**A2.** "Newest matching records" is interpreted as a sort order over the full
matching result set (newest first), not as a filter that returns only the
top-N most recent records. This interpretation may be wrong; Q3 seeks
confirmation.

**A3.** "After a user submits a search" is interpreted as a discrete
event-driven trigger, not an ongoing state. If the system is expected to
continuously update results as new records arrive (live/streaming search), the
pattern would change to state-driven or complex, and additional behavior would
need to be defined.

**A4.** The system boundary is the user-facing search interface. If the search
system also exposes a machine-to-machine API boundary, additional interface
companion requirements may be needed.

---

## Rationale and Context

The source mixes two kinds of content:

1. **Observable behavioral goals** — return matching records quickly, show the
   newest first. These are the basis for Candidates 1 and 2.

2. **Implementation design ideas** — use Redis with a five-minute TTL, use
   GraphQL. These explain *why* the requester expects good performance but do
   not themselves describe user-visible behavior. A verifier cannot observe
   "Redis" or "GraphQL" at the user boundary. Including them in normative
   sentences would constrain the implementation without constraining the
   behavior, which is the opposite of good requirements practice.

The five-minute TTL, if it becomes a behavioral contract (e.g., users must not
see records more than five minutes out of date), should be captured as a
staleness bound in a supporting requirement (see S4 and Q6), not as an
implementation detail.

---

## Review Status

| Candidate | Status | Blocking item |
|-----------|--------|---------------|
| Candidate 1 | needs clarification | Q1 (response-time threshold), Q4 (match definition), Q5 (empty-result response), Q6 (staleness) |
| Candidate 2 | needs clarification | Q2 (timestamp field and tie-break), Q3 (full set vs. top-N) |

Neither candidate is `ready for review`. Both verification contracts contain
unresolved items that prevent a verifier from determining pass or fail. The
primary blocking questions are Q1 (performance threshold) and Q2 (sort key
definition). Once those are answered and the contracts are completed, the
candidates should be re-evaluated against both the implementability and
verification gates before advancing.
