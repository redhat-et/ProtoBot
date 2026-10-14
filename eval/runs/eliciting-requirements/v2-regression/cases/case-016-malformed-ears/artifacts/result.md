# Requirements Elicitation Package

## Understanding

Three source statements were supplied for evaluation. Each is presented as an
EARS-style requirement but contains a structural or semantic defect that
prevents it from being used as-is.

**Q1:** `When, the system shall respond.`
Both the trigger and the response are absent. The keyword `When` is present but
its clause is empty, and `respond` names no observable behavior. Nothing is
implementable or verifiable.

**Q2:** `While the user clicks Submit, the portal shall display the dashboard.`
`While` asserts a state that persists throughout; a click is a discrete event,
not an enduring state. The pattern keyword is wrong for the behavior described.
Additional detail about which dashboard and what "display" means is also
missing.

**Q3:** `Where an administrator deletes an account, then the account system
shall erase all data.`
`Where` signals an optional product feature, not a runtime action. Deleting an
account is a runtime event performed by an actor, not a configuration-time
capability. `Erase all data` is unbounded and unverifiable without scope,
timing, and confirmation of what data is covered. The `then` keyword is
borrowed from the unwanted-behavior (`If … then`) pattern and does not belong
here.

**Proposed system boundaries:** As stated — "the system" (Q1), "the portal"
(Q2), "the account system" (Q3). Clarification is needed for Q1 and Q2 because
the name "the system" and the response "respond" are not specific enough to
identify a real boundary.

---

## Clarifying Questions

**Q1 questions (blocking — must resolve before a candidate can be drafted):**

1. What is the name or identity of the responsible system?
2. What discrete event or condition triggers this obligation? (Examples: a
   user submits a request, a sensor reading arrives, a session times out.)
3. What observable response is required? (Examples: returns a status code,
   displays a confirmation message, writes a record.) What is the measurable
   form of that response, including any timing constraint?

**Q2 questions (blocking — pattern and response detail):**

4. Is the intended obligation triggered by the *act of clicking* Submit (a
   discrete event → `When`) or by a state that persists *while* something else
   is true (an enduring state → `While`)? This determines the correct pattern.
5. Which dashboard is displayed — is it the same dashboard for every user, or
   does it depend on the user's role, permissions, or prior context?
6. What does "display" require? (Examples: the dashboard becomes the active
   view, a modal overlay appears, a redirect occurs.) Is there a timing
   constraint on how quickly it must appear?
7. Are there preconditions, such as the user being authenticated or a form
   being valid, that must be true before this obligation applies?

**Q3 questions (blocking — pattern, scope, and verifiability):**

8. Is account deletion an optional product feature (included only in some
   product configurations → `Where`) or a runtime action any administrator can
   perform at any time (→ `When`)? This determines the correct pattern.
9. What does "all data" cover? (Examples: the account record only, all
   associated user-generated content, audit logs, backups, third-party copies.)
   Is any data retained for legal, audit, or regulatory reasons?
10. Is data erasure synchronous (complete before the system confirms deletion to
    the administrator) or asynchronous (queued for later processing)?
11. Is there a confirmation step before erasure begins, and what happens if the
    administrator cancels mid-process or the operation fails partway through?

---

## Candidate Requirements

### Candidate 1 — derived from Q1

- **Status:** needs clarification
- **Template:** unresolved
- **Requirement:** *(blocked — cannot draft without a named system, a trigger,
  and an observable response; see Clarifying Questions 1–3)*
- **Source intent and rationale:** The source asserts that some system must
  respond to some event. Intent and rationale cannot be determined from the
  source text alone.
- **Implementability contract**
  - System boundary: unresolved — "the system" does not identify a real
    boundary
  - Responsible system: unresolved
  - Actor: unresolved
  - Affected interfaces: unresolved
  - Inputs and data definitions: unresolved — no trigger or input is named
  - Preconditions, state, feature scope, and trigger: unresolved — `When,` has
    an empty clause
  - Observable normal, alternate, and failure response: unresolved —
    `respond` is not an observable behavior
  - Quantities, units, limits, timing, ordering, and tolerances: unresolved
  - Domain terms, dependencies, and unresolved decisions: all material terms
    are unresolved
- **Verification contract**
  - Setup, state, input data, and stimulus: unresolved
  - Observable evidence or oracle: unresolved
  - Expected result: unresolved
  - Pass/fail criteria, including scope, thresholds, units, and timing:
    unresolved
  - Boundary, negative, and failure cases: unresolved
  - Required data, instrumentation, assessment, or external evidence:
    unresolved
  - Observable at the named boundary alone: unresolved
  - If no, internal evidence needed and rationale: unresolved

---

### Candidate 2 — derived from Q2

- **Status:** needs clarification
- **Template:** event-driven (provisional, pending answer to Question 4)
- **Requirement (provisional):** `When the user clicks Submit [precondition
  unknown], the portal shall display [dashboard identity unresolved] to the
  user [timing unresolved].`
- **Source intent and rationale:** A user action on a Submit control should
  cause the portal to show a dashboard. The source uses `While`, which denotes
  an enduring state in EARS; a click is a discrete event, so the probable
  correct pattern is event-driven (`When`). This interpretation is provisional
  pending Question 4. The correct pattern cannot be confirmed without the
  user's answer.
- **Implementability contract**
  - System boundary: the portal
  - Responsible system: the portal
  - Actor: the user
  - Affected interfaces: the portal's user interface (screen or view layer)
  - Inputs and data definitions: the Submit click event; the identity of the
    dashboard to display is unresolved (Question 5)
  - Preconditions, state, feature scope, and trigger: the trigger is the user
    clicking Submit; whether authentication, form validity, or other
    preconditions apply is unresolved (Question 7)
  - Observable normal, alternate, and failure response: normal — the portal
    displays the dashboard; alternate and failure behavior (invalid form, unauthenticated
    user, dashboard unavailable) are unresolved
  - Quantities, units, limits, timing, ordering, and tolerances: no timing
    constraint is stated; whether one is required is unresolved (Question 6)
  - Domain terms, dependencies, and unresolved decisions: "dashboard" —
    identity and scope unresolved (Question 5); "display" — interaction mode
    unresolved (Question 6); correct EARS pattern unresolved (Question 4)
- **Verification contract**
  - Setup, state, input data, and stimulus: a session with the portal open at a
    page containing a Submit control; user clicks Submit
  - Observable evidence or oracle: the portal's user interface
  - Expected result: the named dashboard becomes the active view (or equivalent
    — unresolved)
  - Pass/fail criteria, including scope, thresholds, units, and timing: the
    dashboard must be visible in the portal after the click; timing threshold
    unresolved
  - Boundary, negative, and failure cases: click when form is invalid,
    unauthenticated, or dashboard is unavailable — behavior unresolved
  - Required data, instrumentation, assessment, or external evidence: an
    observer or automated agent able to inspect the portal's active view
  - Observable at the named boundary alone: yes, if the dashboard's presence is
    detectable at the portal's UI boundary
  - If no, internal evidence needed and rationale: n/a (provisional)

---

### Candidate 3 — derived from Q3

- **Status:** needs clarification
- **Template:** event-driven (provisional, pending answer to Question 8)
- **Requirement (provisional):** `When an administrator deletes an account, the
  account system shall [erase — scope, timing, and confirmation unresolved]
  [data scope unresolved] associated with that account.`
- **Source intent and rationale:** When an administrator removes an account,
  the account system must eliminate associated data. The source uses `Where`,
  which in EARS marks an optional product feature, not a runtime action
  performed by an actor. Account deletion is almost certainly a runtime event,
  making event-driven (`When`) the provisional correct pattern. This
  interpretation is provisional pending Question 8. The `then` keyword is
  borrowed from the unwanted-behavior pattern and is removed; it does not apply
  here. "All data" is too broad to implement or verify without scope defined in
  Questions 9–11.
- **Implementability contract**
  - System boundary: the account system
  - Responsible system: the account system
  - Actor: an administrator
  - Affected interfaces: the account system's data store and any downstream
    systems holding the account's data
  - Inputs and data definitions: the account identifier to delete; the set of
    data covered by "erase all data" is unresolved (Question 9)
  - Preconditions, state, feature scope, and trigger: trigger — administrator
    deletes an account; whether a confirmation step is required is unresolved
    (Question 11); whether this is a runtime action or an optional feature is
    unresolved (Question 8)
  - Observable normal, alternate, and failure response: normal — data is erased;
    synchronous vs. asynchronous is unresolved (Question 10); failure mid-erase
    and partial completion behavior are unresolved (Question 11)
  - Quantities, units, limits, timing, ordering, and tolerances: completeness
    of erasure and timing are unresolved (Questions 9–10)
  - Domain terms, dependencies, and unresolved decisions: "erase" — does this
    mean logical deletion, physical deletion, cryptographic erasure? unresolved;
    "all data" — scope unresolved (Question 9); correct EARS pattern unresolved
    (Question 8); retention exceptions unresolved (Question 9)
- **Verification contract**
  - Setup, state, input data, and stimulus: an existing account with associated
    data; an administrator issues a delete command for that account
  - Observable evidence or oracle: inspection of the account system's boundary
    (API, UI, or audit log) to confirm the data is no longer accessible or
    present
  - Expected result: the data covered by the erasure scope is no longer
    retrievable through any interface the account system exposes
  - Pass/fail criteria, including scope, thresholds, units, and timing: data
    within the agreed scope is absent or inaccessible; scope and timing
    threshold are unresolved
  - Boundary, negative, and failure cases: partial deletion (system fails
    mid-erase), administrator lacks permission, account already deleted,
    concurrent deletion requests — all behavior unresolved
  - Required data, instrumentation, assessment, or external evidence: access to
    the account system's data interfaces before and after deletion; possibly a
    data-inventory or audit log
  - Observable at the named boundary alone: partially — logical absence may be
    observable at the API; physical erasure may require internal evidence
  - If no, internal evidence needed and rationale: if physical erasure is
    required, an internal audit log, storage scan, or erasure certificate is
    needed because the API cannot confirm physical deletion

---

## Suggested Supporting Requirements

**Relating to Candidate 2 (portal / Submit / dashboard):**

1. **Failure-path companion** — *Source: Candidate 2.* If the portal cannot
   retrieve or render the dashboard after Submit is clicked, then the portal
   shall display a message that informs the user the dashboard is unavailable
   and offers a recovery action. *Reason:* the source says nothing about what
   happens when the dashboard cannot be shown; a user clicking Submit with no
   response or a blank screen is a material failure path.

2. **Required companion** — *Source: Candidate 2.* While a form validation
   error is present, the portal shall prevent submission and display the
   error to the user before navigation occurs. *Reason:* if the dashboard is
   displayed regardless of form validity, data integrity may be compromised; if
   submission is blocked, this behavior needs its own requirement.

**Relating to Candidate 3 (account system / delete / erase):**

3. **Failure-path companion** — *Source: Candidate 3.* If the account system
   fails to complete erasure of account data, then the account system shall
   record the failure, retain the account in a recoverable state, and notify
   [actor or system — unresolved] of the incomplete operation. *Reason:*
   partial erasure is a privacy and data-integrity risk; the source is silent
   on this path.

4. **Operational companion** — *Source: Candidate 3.* When an administrator
   deletes an account, the account system shall produce an auditable record
   of the deletion event, including the account identifier, the administrator
   identity, and the timestamp. *Reason:* erasure of personal or sensitive data
   is typically subject to audit requirements; the source does not address
   traceability.

5. **Boundary companion** — *Source: Candidate 3.* When an administrator
   attempts to delete an account that does not exist or has already been
   deleted, the account system shall return a response that indicates the
   account was not found without performing any data modification. *Reason:*
   the source is silent on idempotence and double-deletion behavior.

---

## Consistency Findings

**Finding 1 — Gap (Q1 vs. Q2 and Q3):** Q1 names no system, trigger, or
response. It cannot be compared to Q2 or Q3 for consistency. The gap is total;
no meaningful cross-check is possible until Questions 1–3 are answered.

**Finding 2 — No conflict between Q2 and Q3 (as drafted):** The portal (Q2)
and the account system (Q3) appear to be different systems with distinct
triggers and responsibilities. No conflict or overlap is visible at this level
of detail.

**Finding 3 — Potential gap across Q2 and Q3 (authorization):** Neither Q2
nor Q3 states any authorization requirement. It is unresolved whether any
actor may click Submit or delete an account, or whether role or permission
constraints apply. If both systems share an authentication/authorization
mechanism, a consistency requirement may be needed.

---

## Assumptions

1. **A1 (Q2 — pattern):** The word `While` in Q2 is a source error; the
   intended pattern is event-driven (`When`). This is treated as provisional
   until Question 4 is answered.

2. **A2 (Q3 — pattern):** The word `Where` in Q3 is a source error; account
   deletion is a runtime action, not an optional product feature. The intended
   pattern is event-driven (`When`). This is treated as provisional until
   Question 8 is answered.

3. **A3 (Q3 — `then`):** The word `then` in Q3 is borrowed from the
   unwanted-behavior (`If … then`) pattern. Because no unwanted condition is
   stated, `then` is treated as a structural error and is removed from the
   provisional candidate sentence.

4. **A4 (Q1 — empty clause):** The empty `When,` clause in Q1 is treated as a
   structural placeholder, not as a deliberate ubiquitous or unconditional
   requirement. If the intent was a ubiquitous obligation, the source would not
   use `When`.

---

## Rationale and Context

All three source statements use EARS keywords but fail to satisfy the
structural rules of the pattern they invoke:

- **Q1** uses `When` without a trigger clause and `respond` without observable
  content. The statement is a syntactic shell with no implementable or
  verifiable meaning.

- **Q2** uses `While` for a discrete click event. In EARS, `While` marks a
  state that endures; a button click is a boundary event. Using the wrong
  pattern produces a requirement that could be misread as demanding continuous
  dashboard display throughout some undefined state rather than a one-time
  navigation response.

- **Q3** uses `Where` for a runtime administrative action. In EARS, `Where`
  marks a requirement that applies only when an optional capability is included
  in the product. An administrator deleting an account is not an optional
  feature; it is an actor performing a runtime operation. The `then` keyword
  from the `If … then` pattern also migrated in without the required unwanted
  condition, further mixing two patterns.

No source rationale was provided for any statement. The rationale above is
inferred from the observable intent of each statement.

---

## Review Status

| Label       | Status               | Blocking issues                                                                    |
|-------------|----------------------|------------------------------------------------------------------------------------|
| Candidate 1 | needs clarification  | System name, trigger clause, and observable response are all absent (Q1–Q3)        |
| Candidate 2 | needs clarification  | Pattern unconfirmed; dashboard identity, display mode, preconditions, and timing unresolved (Q4–Q7) |
| Candidate 3 | needs clarification  | Pattern unconfirmed; erasure scope, timing, synchrony, failure behavior, and retention exceptions unresolved (Q8–Q11) |

No candidate reaches `candidate` or `ready for review` status. All three
require answers to the blocking clarifying questions before a normative EARS
sentence can be confirmed.
