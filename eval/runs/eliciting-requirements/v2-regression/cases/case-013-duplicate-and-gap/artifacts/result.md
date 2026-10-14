# Requirements Elicitation Package

## Understanding

**Requested outcome:** The account service sends a password-reset message to a
user within a bounded time after the user requests a password reset.

**Proposed system boundary:** The account service — specifically its outbound
notification path — at the point where it dispatches the reset message to an
external delivery channel.

**Source material:** Two supplied requirements (R1, R2) and a prose gap
statement naming three missing behaviors.

**Information sorting:**

| Source statement | Kind |
|---|---|
| R1 — account service sends a "reset message" within 60 seconds | Behavioral obligation (candidate) |
| R2 — account service sends a "password-reset email" within 1 minute | Behavioral obligation — likely duplicate of R1; medium differs slightly |
| "No behavior for an unavailable mail provider" | Identified gap — failure-path obligation |
| "No behavior for repeated requests" | Identified gap — boundary/lifecycle obligation |
| "No behavior for an expired reset link" | Identified gap — state/lifecycle obligation |

**Key observations before drafting:**

1. **Duplicate or near-duplicate:** R1 ("reset message" / 60 seconds) and R2
   ("password-reset email" / 1 minute) describe the same trigger, the same
   responsible system, and the same timing limit expressed in different units.
   They differ only in the word used for the outbound artifact ("reset message"
   vs. "password-reset email"). This is an **overlap / near-duplicate** finding.
   The intended behavior is ambiguous: are both terms referring to an email, or
   is "reset message" a broader channel (SMS, push, in-app)? This must be
   resolved before either can be marked ready.

2. **Timing units are identical in magnitude** (60 s = 1 min) but inconsistently
   expressed. A single canonical form should be chosen.

3. **Three gaps** are acknowledged by the submitter: unavailable mail provider,
   repeated requests, and expired reset link. None of these have requirements.

---

## Clarifying Questions

**Q1 (Duplicate / channel scope — blocks R1 and R2):**
R1 uses "reset message" and R2 uses "password-reset email." Do both requirements
describe the same delivery channel (email), making one of them redundant? Or
does "reset message" intentionally cover additional channels (e.g., SMS or push
notification)? If multiple channels are intended, is the 60-second deadline per
channel, or does it apply to all channels collectively?

**Q2 (Timing — blocks R1 and R2):**
Should the 60-second / 1-minute deadline be expressed as a single canonical
value and unit (e.g., 60 seconds)? If so, is the deadline measured from when
the account service *receives* the user's request or from when the service
*begins processing* it?

**Q3 (Mail-provider failure — blocks SR-A):**
When the external mail provider is unavailable, what shall the account service
do? Candidates include: retry with a defined schedule, queue the message for
later delivery, return an error to the calling interface, or notify the user
through an alternate channel. Which behavior is required, and what retry limit
or timeout applies?

**Q4 (Repeated requests — blocks SR-B):**
When a user submits multiple password-reset requests before the first reset link
is used or expires, shall the account service honor each request independently,
invalidate prior pending links, or reject subsequent requests within a window?
If requests are rate-limited, what is the limit and the response when it is
exceeded?

**Q5 (Reset-link expiry — blocks SR-C):**
When a user attempts to use a reset link after it has expired, what shall the
account service do? Candidates include: return an error message, redirect to a
new-request flow, or silently ignore the attempt. What expiry duration is
intended?

**Q6 (Audit / confirmation — optional):**
Is the account service required to record an event (e.g., a log entry or audit
trail) when a reset message is dispatched, when delivery fails, or when a reset
link is used or expires? This is not implied by R1 or R2 but is commonly
required for security.

---

## Candidate Requirements

### Candidate 1 (from R1)

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** When a user requests a password reset, the account service
  shall dispatch a password-reset [message | email — see Q1] within 60 seconds
  of receiving the request.
- **Source intent and rationale:** R1 states that the account service must act
  within 60 seconds of a reset request. The intent is a bounded delivery
  latency so that users receive the reset credential promptly.
- **Implementability contract**
  - System boundary: Account service, outbound notification interface.
  - Responsible system: Account service.
  - Actor: User (initiates the request at an unspecified interface — web form,
    API, etc.).
  - Affected interfaces: Interface from the account service to the external
    mail (or messaging) provider; interface back to the calling client.
  - Inputs and data definitions: A password-reset request for an identified
    user account. "Reset message" is undefined — could be email, SMS, or
    another channel (Q1 open). "User" identity and lookup not defined in R1/R2.
  - Preconditions, state, feature scope, and trigger: Trigger is the user
    submitting a password-reset request. No precondition on account state is
    stated (e.g., account active, unblocked).
  - Observable normal, alternate, and failure response: Normal — reset message
    dispatched within 60 seconds. Alternate and failure — undefined (Q3, Q4
    open).
  - Quantities, units, limits, timing, ordering, and tolerances: 60 seconds
    (R1) = 1 minute (R2); start of interval is open (Q2). No tolerance stated.
  - Domain terms, dependencies, and unresolved decisions: "Reset message" vs.
    "password-reset email" (Q1 open). Measurement start point (Q2 open).
    Channel scope (Q1 open). Duplicate with Candidate 2 until Q1 is resolved.
- **Verification contract**
  - Setup, state, input data, and stimulus: A valid user account exists.
    A password-reset request is submitted to the account service.
  - Observable evidence or oracle: A reset message is received on the target
    delivery channel; the timestamp of receipt is compared against the
    timestamp of the request submission.
  - Expected result: The reset message arrives at the delivery channel boundary
    within 60 seconds.
  - Pass/fail criteria, including scope, thresholds, units, and timing: Pass if
    dispatch occurs ≤ 60 s from request receipt. Fail otherwise. Measurement
    point at account-service outbound boundary is unresolved — internal dispatch
    vs. confirmed delivery to provider is unclear.
  - Boundary, negative, and failure cases: No reset message sent (failure).
    Message sent after 60 s (failure). Delivery channel unavailable — not
    specified (Q3 open). Duplicate requests — not specified (Q4 open).
  - Required data, instrumentation, assessment, or external evidence:
    Timestamps at request receipt and at message dispatch. Access to the
    account-service outbound log or delivery confirmation from the provider.
  - Observable at the named boundary alone: No — the 60-second constraint
    requires a timestamp at both the request-receipt boundary and the dispatch
    boundary; provider delivery confirmation may also be needed depending on
    how "dispatch" is defined.
  - If no, internal evidence needed and rationale: Request-receipt timestamp
    (internal to account service) and dispatch timestamp (at the provider
    handoff point) are both needed to measure compliance.

---

### Candidate 2 (from R2)

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** When a user requests a password reset, the account service
  shall dispatch a password-reset email within 60 seconds of receiving the
  request.
- **Source intent and rationale:** R2 states the account service must send a
  password-reset *email* within 1 minute (= 60 seconds). The channel is
  specified as email. Likely describes the same behavior as R1 with a more
  specific channel term.
- **Implementability contract**
  - System boundary: Account service, outbound email interface.
  - Responsible system: Account service.
  - Actor: User.
  - Affected interfaces: Account service to external email provider (SMTP,
    API, etc.).
  - Inputs and data definitions: Password-reset request for an identified
    user with a reachable email address. Email address validity and lookup not
    defined.
  - Preconditions, state, feature scope, and trigger: Same trigger as
    Candidate 1. No stated precondition on account state.
  - Observable normal, alternate, and failure response: Normal — email
    dispatched within 60 seconds. Alternate and failure — undefined (Q3 open).
  - Quantities, units, limits, timing, ordering, and tolerances: 1 minute
    stated in source; canonical value is 60 seconds pending Q2. Measurement
    start open (Q2 open).
  - Domain terms, dependencies, and unresolved decisions: "Password-reset
    email" is more specific than R1's "reset message." Relationship to R1 is
    open (Q1). Measurement start (Q2 open). Possible duplicate of Candidate 1.
- **Verification contract**
  - Setup, state, input data, and stimulus: Valid user account with a
    registered email address. Password-reset request submitted to the account
    service.
  - Observable evidence or oracle: A password-reset email is received in the
    target inbox; dispatch or delivery timestamp is compared to request
    timestamp.
  - Expected result: Email dispatched within 60 seconds of request receipt.
  - Pass/fail criteria, including scope, thresholds, units, and timing: Pass if
    dispatch ≤ 60 s. Fail otherwise. "Dispatch" vs. "delivery" boundary is
    open (Q2).
  - Boundary, negative, and failure cases: No email sent. Email sent after
    deadline. Email provider unavailable (Q3 open). User has no email address
    registered — not stated. Repeated requests (Q4 open).
  - Required data, instrumentation, assessment, or external evidence:
    Request-receipt and dispatch timestamps; provider delivery log or test
    mailbox.
  - Observable at the named boundary alone: No — same dual-timestamp
    constraint as Candidate 1.
  - If no, internal evidence needed and rationale: Internal request-receipt
    timestamp and outbound dispatch timestamp both required.

---

## Suggested Supporting Requirements

**SR-A — Mail-provider unavailability (Failure-path companion)**
*Related to:* Candidates 1 and 2 (normal dispatch path).
*Missing behavior:* When the external mail provider is unavailable at the time
the account service attempts to dispatch the reset message, the account service
has no defined response. Without this, the 60-second obligation has no defined
failure path and implementers will choose inconsistent behavior.
*Suggested candidate (provisional, pending Q3):*
> If the external mail provider is unavailable when the account service
> attempts to dispatch a password-reset message, then the account service shall
> [retry / queue / return an error — to be determined by answer to Q3].

**SR-B — Repeated reset requests (Boundary companion)**
*Related to:* Candidates 1 and 2 (trigger scope).
*Missing behavior:* When a user submits a second (or further) password-reset
request before the first link is consumed or expires, the account service has
no defined behavior. Without this, concurrent reset tokens could exist,
creating a security ambiguity.
*Suggested candidate (provisional, pending Q4):*
> While a password-reset request is pending for an account, when a new
> password-reset request is received for the same account, the account service
> shall [invalidate the prior link / reject the new request / honor both —
> to be determined by answer to Q4].

**SR-C — Expired reset link (State/lifecycle companion)**
*Related to:* Candidates 1 and 2 (lifecycle of the reset credential).
*Missing behavior:* When a user attempts to use a reset link after it has
expired, the account service has no defined response. Without a defined expiry
behavior, the reset credential's lifecycle is incomplete.
*Suggested candidate (provisional, pending Q5):*
> If a user submits an expired password-reset link, then the account service
> shall [return an error / redirect — to be determined by answer to Q5].
> (Requires SR-D: the expiry duration must be defined.)

**SR-D — Reset-link expiry duration (Required companion for SR-C)**
*Related to:* SR-C.
*Missing behavior:* The duration for which a reset link remains valid is not
stated anywhere in R1 or R2. Without it, SR-C cannot be verified and
implementers will choose arbitrary values.
*Suggested candidate (provisional, pending Q5):*
> The account service shall invalidate a password-reset link [N minutes /
> hours] after it is dispatched.

**SR-E — Audit event for dispatch (Optional consideration)**
*Related to:* Candidates 1 and 2.
*Reason:* Password-reset flows are a common target for account-takeover
attacks. Recording a timestamped event when a reset message is dispatched
supports security monitoring and incident investigation. This is not implied
by R1 or R2; ask whether it is in scope (Q6).

---

## Consistency Findings

### Finding 1 — Duplicate / Overlap: R1 and R2

- **Affected items:** R1 (Candidate 1), R2 (Candidate 2).
- **Condition:** Both requirements share an identical trigger (user requests a
  password reset), identical responsible system (account service), and
  identical timing constraint (60 seconds = 1 minute). They differ only in
  the label for the outbound artifact: "reset message" (R1) vs.
  "password-reset email" (R2).
- **Consequence:** If "reset message" and "password-reset email" denote the
  same thing, the requirements are near-identical duplicates that will diverge
  as they are maintained separately. If "reset message" is intentionally
  broader (e.g., covers SMS or push), the two requirements overlap on the
  email channel and may impose two independent obligations, risking double
  dispatch.
- **Resolution options:**
  1. Confirm that both refer to email and consolidate into a single
     requirement using the more specific term "password-reset email."
  2. Confirm that "reset message" covers multiple channels, remove the overlap
     by scoping each requirement to its channel, and express one requirement
     per channel or one requirement that applies to all configured channels.
- **Blocks:** Both candidates remain `needs clarification` until Q1 is
  answered and this finding is resolved.

### Finding 2 — Gap: No failure path for external provider unavailability

- **Affected items:** Candidates 1 and 2.
- **Condition:** Both candidates require dispatch within 60 seconds, but no
  requirement exists for what happens if the external provider is unavailable
  during that window.
- **Consequence:** An implementer cannot make a conforming choice. A verifier
  cannot determine pass or fail for a test with a simulated provider outage.
- **Resolution options:** Add SR-A with the answer to Q3.

### Finding 3 — Gap: No behavior for repeated reset requests

- **Affected items:** Candidates 1 and 2.
- **Condition:** The trigger "user requests a password reset" does not specify
  whether it applies once per pending reset or on every invocation.
- **Consequence:** Multiple valid reset tokens could coexist, and their
  precedence is undefined. Two implementers could produce different security
  postures.
- **Resolution options:** Add SR-B with the answer to Q4.

### Finding 4 — Gap: No reset-link lifecycle behavior

- **Affected items:** Candidates 1 and 2 (downstream of dispatch).
- **Condition:** Neither requirement defines how long a dispatched reset link
  remains valid or what happens when an expired link is submitted.
- **Consequence:** The reset flow is incomplete. A user who receives a link
  but delays acting has no defined outcome.
- **Resolution options:** Add SR-C and SR-D with the answers to Q5.

---

## Assumptions

**A1 (Provisional):** "Reset message" in R1 and "password-reset email" in R2
refer to the same delivery channel (email). This interpretation is provisional
pending the answer to Q1. If the assumption is wrong, separate channel-scoped
candidates are needed.

**A2 (Provisional):** The 60-second and 1-minute timing constraints in R1 and
R2 are identical in magnitude. The canonical unit going forward is 60 seconds.
Pending Q2, the measurement start is assumed to be receipt of the user's
request at the account service boundary.

**A3 (Provisional):** "Dispatch" means the account service has handed the
message to the external provider's API or interface. It does not mean confirmed
delivery to the recipient's inbox. This must be confirmed to make the
verification contract pass.

---

## Rationale and Context

R1 and R2 appear to be independently authored statements of the same
requirement — a bounded delivery latency for the password-reset credential.
The inconsistent terminology ("reset message" vs. "password-reset email") is
the primary source of ambiguity. Neither statement addresses failure paths,
which are essential for a security-sensitive flow. The submitter correctly
identified the three gap areas; none of them has a corresponding requirement,
so the set is incomplete for implementation.

The most important action is to resolve Q1 (duplicate/channel scope) and then
Q3–Q5 (failure paths). Q2 (measurement start) is needed to make the
verification contracts complete.

---

## Review Status

| Item | Status | Blocking issue |
|---|---|---|
| Candidate 1 (R1) | needs clarification | Q1 (channel), Q2 (timing start), Q3 (failure path); Finding 1 |
| Candidate 2 (R2) | needs clarification | Q1 (channel/duplicate), Q2 (timing start), Q3 (failure path); Finding 1 |
| SR-A | suggestion — not a candidate | Pending Q3 |
| SR-B | suggestion — not a candidate | Pending Q4 |
| SR-C | suggestion — not a candidate | Pending Q5 |
| SR-D | suggestion — not a candidate | Pending Q5 |
| SR-E | optional consideration | Pending Q6 |
| Finding 1 (duplicate) | open | Q1 must be answered |
| Finding 2 (gap: provider failure) | open | SR-A not yet accepted |
| Finding 3 (gap: repeated requests) | open | SR-B not yet accepted |
| Finding 4 (gap: link lifecycle) | open | SR-C/SR-D not yet accepted |

No candidate is `ready for review`. All candidates remain at
`needs clarification` pending the answers to Q1 and Q2 at minimum, and the
identification of which gap-filling supporting requirements are in scope.
