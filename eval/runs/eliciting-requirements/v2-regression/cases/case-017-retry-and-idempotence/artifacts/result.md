# Requirements Elicitation Package

## Understanding

**Requested outcome:** The profile service must durably persist a user profile
whenever a save is requested, and must keep retrying automatically after
network failures until the save ultimately succeeds.

**Proposed system boundary:** The **profile service** — a named service that
accepts profile-save requests from an unspecified caller and interacts with an
unspecified persistence back-end over a network.

**Actors and external systems identified:**
- Caller (unspecified — may be a user, client application, or another service)
- Profile service (responsible system)
- Network (transport between the profile service and the persistence back-end)
- Persistence back-end (unspecified — may be a database, remote store, or
  external API)

**Source language interpreted:**
- "shall persist" → normative obligation; treated as EARS `shall`.
- "If the network fails" → unwanted condition; maps to EARS `If … then`.
- "retry as needed until the save succeeds" → the source phrase "as needed" is
  flagged as vague (see questions below); the termination condition ("until the
  save succeeds") is a behavioral intent preserved in the draft.
- No mention of idempotence, retry limits, backoff policy, caller notification,
  or timeout. These are material open questions.

---

## Clarifying Questions

**Q1 — Retry termination and limits (highest value):** The source says "retry
as needed until the save succeeds," which implies unbounded retries. Must the
service retry indefinitely, or is there a maximum retry count or elapsed-time
limit after which it stops and reports permanent failure? The answer determines
whether a finite-limit requirement or a notification-on-exhaustion requirement
is needed.

**Q2 — Retry timing and strategy:** Is there a required minimum or maximum
interval between retries (e.g., a fixed delay, an exponential back-off policy,
or a maximum delay cap)? Without this, two implementers will choose different
observable behavior.

**Q3 — Caller response on initial request:** When a save is requested and the
profile service accepts it, what response, if any, must the service return to
the caller before the retries resolve? Options include: (a) no response until
the save is confirmed durable, (b) an immediate acknowledgment that the save is
queued, or (c) something else. This determines whether a separate interface
requirement is needed.

**Q4 — Caller notification on eventual success or failure:** After retrying,
must the profile service notify the caller (or any other system) when the save
eventually succeeds? Must it notify when retries are exhausted without success?
If so, through what interface?

**Q5 — Scope of "network fails":** Does "network fails" mean only transport
failures (e.g., connection refused, timeout, DNS failure), or does it also
include transient error responses from the persistence back-end (e.g., HTTP
503, rate-limit error)? This affects the trigger definition.

**Q6 — Idempotence:** If the network fails after the persistence back-end has
already written the profile but before an acknowledgment is returned, a retry
will re-send the same data. Must the profile service ensure that re-sent saves
do not create duplicate records or overwrite a later version? This is a
material correctness concern.

**Q7 — Definition of "profile save request":** What constitutes a valid profile
save request at the service boundary? Is a request always valid, or can it be
malformed or unauthorized? If the latter, a validation or authorization
companion may be needed.

---

## Candidate Requirements

### Candidate 1

- **Status:** needs clarification
- **Template:** Event-driven
- **Requirement:** When a profile save is requested, the profile service shall
  persist the profile to the persistence back-end.
- **Source intent and rationale:** The source obligation is that a save request
  causes the profile service to write the profile durably. This is the primary
  happy-path behavior.
- **Implementability contract**
  - System boundary: Profile service external API (inbound save request
    interface).
  - Responsible system: Profile service.
  - Actor: Unspecified caller (Q7 open — may be a user agent or another
    service).
  - Affected interfaces: Inbound save-request interface; outbound interface to
    persistence back-end.
  - Inputs and data definitions: A "profile save request" — definition of valid
    content, identity, and format is unspecified (Q7 open).
  - Preconditions, state, feature scope, and trigger: Trigger is receipt of a
    profile save request. No precondition is stated. Assumed: request is valid
    and the service is operational.
  - Observable normal, alternate, and failure response: Normal — profile is
    persisted. Alternate and failure paths covered by Candidate 2. What the
    service returns to the caller is open (Q3 open).
  - Quantities, units, limits, timing, ordering, and tolerances: None stated.
    No durability or latency measure specified.
  - Domain terms, dependencies, and unresolved decisions: "profile", "persist",
    "persistence back-end" undefined. Q3, Q7 open.
- **Verification contract**
  - Setup, state, input data, and stimulus: Submit a valid profile save request
    to the profile service boundary.
  - Observable evidence or oracle: Observe that the profile data is retrievable
    from the persistence back-end after the service processes the request.
  - Expected result: Profile data matches the submitted content and is durable
    (survives a service restart or equivalent).
  - Pass/fail criteria, including scope, thresholds, units, and timing: Pass if
    profile is retrievable and matches input. Fail if data is absent or
    corrupted. No timing threshold defined (open).
  - Boundary, negative, and failure cases: Behavior when the request is invalid
    or unauthorized is unspecified (Q7 open).
  - Required data, instrumentation, assessment, or external evidence: Read-back
    of stored profile from the persistence back-end after the operation.
  - Observable at the named boundary alone: No — persistence must be confirmed
    via the back-end or a read-back interface.
  - If no, internal evidence needed and rationale: A read-back query to the
    persistence back-end (or equivalent durable-storage observation) is needed
    to verify the write completed.

---

### Candidate 2

- **Status:** needs clarification
- **Template:** Unwanted behavior
- **Requirement:** If a network failure occurs during a profile save attempt,
  then the profile service shall retry the save until the save succeeds.
- **Source intent and rationale:** The source explicitly names network failure
  as an unwanted condition and states that the service must retry until success.
  The phrase "as needed" is preserved as "until the save succeeds" per source
  intent, but is flagged because it implies unbounded retries (Q1 open).
- **Implementability contract**
  - System boundary: Profile service — interaction with the persistence
    back-end over the network.
  - Responsible system: Profile service.
  - Actor: Network (undesired condition actor); profile service (retry actor).
  - Affected interfaces: Outbound interface from profile service to persistence
    back-end.
  - Inputs and data definitions: The same profile data submitted in the
    original save request; "network failure" scope is open (Q5 open).
  - Preconditions, state, feature scope, and trigger: A prior save attempt for
    a profile has been initiated and a network failure has been detected.
  - Observable normal, alternate, and failure response: Retry is initiated.
    Service continues retrying until success. What happens if retries are
    exhausted (if a limit exists) is open (Q1 open). Retry interval and backoff
    are open (Q2 open).
  - Quantities, units, limits, timing, ordering, and tolerances: Retry count,
    maximum retries, delay between retries, maximum elapsed time — all open
    (Q1, Q2 open).
  - Domain terms, dependencies, and unresolved decisions: "network failure"
    scope open (Q5). Retry termination condition beyond "succeeds" open (Q1).
    Idempotence of re-sent saves open (Q6).
- **Verification contract**
  - Setup, state, input data, and stimulus: Configure the network path between
    the profile service and the persistence back-end to produce a failure
    (e.g., simulate connection refusal or timeout). Submit a profile save
    request.
  - Observable evidence or oracle: Observe that the profile service issues at
    least one additional save attempt after the initial failure, and that the
    profile is eventually persisted when the network is restored.
  - Expected result: Profile is durably stored after network is restored.
    Service did not stop retrying while network failure persisted.
  - Pass/fail criteria, including scope, thresholds, units, and timing: Pass if
    profile is stored after recovery; fail if the service stops retrying before
    success with no intervening terminal condition. No maximum retry count or
    timing threshold defined (Q1, Q2 open) — cannot state a complete criterion.
  - Boundary, negative, and failure cases: Behavior when network never recovers
    (permanent failure) is undefined (Q1 open). Idempotent re-send behavior
    undefined (Q6 open). Behavior when back-end returns a non-network error
    (e.g., authorization failure) during retry is undefined (Q5 open).
  - Required data, instrumentation, assessment, or external evidence: Network
    fault injection capability; log or observable counter of retry attempts;
    read-back confirmation of persisted profile.
  - Observable at the named boundary alone: No — retry behavior requires
    observing outbound traffic or internal retry signals; persistence requires
    back-end read-back.
  - If no, internal evidence needed and rationale: Retry attempt count and
    timing must be observable (via logs, metrics, or intercepted outbound
    calls) to verify retries occurred. Back-end read-back needed for
    persistence confirmation.

---

## Suggested Supporting Requirements

**S1 — Failure-path companion (relates to Candidate 2):** If retries are
bounded (Q1 resolution), a companion is needed: *If the profile service
exhausts the maximum retry count [or elapsed time] without a successful save,
then the profile service shall [notify the caller / raise an observable error /
mark the save as permanently failed].* The exact response depends on Q1 and Q4.
Keep outside candidates until Q1 and Q4 are answered.

**S2 — Boundary companion (relates to Candidate 2 — idempotence):** If a retry
re-sends a profile that was already written but not acknowledged (Q6), the
service must not create duplicate records or corrupt an existing profile. A
companion would require the profile service to ensure that repeated save
attempts for the same profile produce the same stored state. Keep outside
candidates until Q6 is answered.

**S3 — Operational companion (relates to Candidate 2 — retry timing):** A
retry strategy (interval, backoff, jitter) may be required to prevent overload
of the persistence back-end during sustained network failure. Keep outside
candidates until Q2 is answered.

**S4 — Interface companion (relates to Candidate 1 — caller acknowledgment):**
If the caller must be informed of the outcome (success or failure), a companion
is needed to define what the profile service returns at the inbound boundary
and when. Keep outside candidates until Q3 and Q4 are answered.

**S5 — Optional consideration (observability):** Retry attempts, delays, and
final outcomes may need to be observable through logs or metrics for operational
monitoring. Ask whether this is in scope before drafting.

---

## Consistency Findings

**Finding 1 — Gap: retry termination undefined.** Candidate 2 requires the
service to retry "until the save succeeds" with no stated upper bound. If the
network never recovers, the service retries forever. This is either a valid
design choice (unbounded retries) or a gap (a finite limit and failure
notification are missing). Resolution: answer Q1 and, if bounded, add S1 as a
candidate.

**Finding 2 — Gap: idempotence of retried saves.** Neither candidate addresses
what happens when the persistence back-end writes the profile but the
acknowledgment is lost. A retry in that state could create a duplicate write.
This is a material correctness gap. Resolution: answer Q6 and, if needed, add
S2 as a candidate.

**Finding 3 — Gap: scope of "network failure" (trigger definition).** Candidate
2's unwanted condition is partially undefined (Q5). If back-end transient
errors (non-transport failures) are excluded, retries will not occur for a
class of failures the source may have intended to cover. Resolution: answer Q5
and narrow or broaden the trigger definition accordingly.

**Finding 4 — Gap: caller-facing behavior undefined.** Neither candidate
specifies what the profile service returns to the caller before, during, or
after retries (Q3, Q4). A caller cannot distinguish a queued retry from a
silent failure without a defined response at the inbound boundary. Resolution:
answer Q3 and Q4, then add S4 as a candidate if applicable.

No conflicts or duplicates found among the two candidates.

---

## Assumptions

**A1 (provisional):** "Profile" refers to a named user or entity data record
that the profile service is responsible for storing. No lifecycle state (e.g.,
draft, published, deleted) is assumed. Ask if lifecycle states affect when a
save may be requested.

**A2 (provisional):** The profile service communicates with exactly one
persistence back-end over a network. If multiple back-ends or a local fallback
exist, the retry logic may apply differently.

**A3 (provisional):** "Network fails" in the source refers to transport-layer
failures (connection failure, timeout) between the profile service and the
persistence back-end, not failures internal to the service or failures of the
persistence back-end itself (e.g., data corruption, authorization rejection).
This may need narrowing or broadening — see Q5.

**A4 (provisional):** The source phrase "retry as needed until the save
succeeds" is interpreted as requiring at least one retry after a failure. It
is not interpreted as specifying a particular retry algorithm, count, or delay.
These are treated as open design decisions — see Q1 and Q2.

---

## Rationale and Context

The source expresses two distinct obligations joined in a single prose passage:
(1) a primary obligation to persist a profile when a save is requested, and
(2) a recovery obligation to retry after network failure until success. These
are separated into Candidate 1 (event-driven, happy path) and Candidate 2
(unwanted behavior, recovery path) because they are independently testable and
may be independently negotiated.

The phrase "as needed" in the source is a vague qualifier. It has been
preserved as "until the save succeeds" in Candidate 2 to match the only
concrete termination condition stated. However, this produces an unbounded
retry loop (Finding 1), which may not be the intended behavior. The flag is
preserved rather than silently resolved by adding a retry limit, because the
source does not authorize one.

Idempotence (S2, Finding 2) is raised because network retries on
at-least-once delivery are a canonical source of duplicate writes; this is a
material correctness concern even though the source does not name it.

---

## Review Status

| Candidate | Status | Blocking items |
|-----------|--------|----------------|
| Candidate 1 | needs clarification | Q3 (caller response), Q7 (valid request definition) |
| Candidate 2 | needs clarification | Q1 (retry termination/limit), Q2 (retry timing), Q5 (failure scope), Q6 (idempotence) |

Neither candidate is ready for review. Both require answers to the questions
listed above before their implementability and verification contracts can be
completed. No candidates have been approved. This package is an elicitation
draft only.
