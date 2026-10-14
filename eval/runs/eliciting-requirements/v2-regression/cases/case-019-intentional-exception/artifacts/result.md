# Requirements Elicitation Package

## Understanding

**Requested outcome:** The export service should make authorized reports
downloadable under normal conditions, and must block downloads while a report
is under legal hold, surfacing the hold state to the client.

**Proposed system boundary:** The export service is the sole responsible
system. It receives download requests from clients and enforces both
authorization and legal-hold policy before serving or denying a report file.

**Actors:** Client (the requester of a download).

**Key domain terms interpreted from source:**
- *Authorized report* — a report for which the requesting client holds the
  necessary authorization.
- *Legal hold* — a formal constraint that prevents disposition or disclosure of
  a report, applied externally to the export service.
- *Legal-hold state* — observable status information the service returns to
  the client when a hold is active.

**Relationship between R1 and R2:** The owner explicitly designates R2 as an
intentional exception to R1. When both conditions apply (authorized report AND
legal hold), R2 governs and R1 does not require download. This resolves the
apparent conflict at the requirements level; no further priority question
remains for that intersection.

---

## Clarifying Questions

**Q1 (material — R1 implementability):** What does "available for download"
mean at the service boundary? Does the export service return the report file
directly in the response, or does it return a pre-signed URL or redirect to a
storage location? Two reasonable implementations differ in visible behavior
here.

**Q2 (material — R1 trigger):** Is "makes available" a continuous state
(the report remains accessible without a fresh request) or a per-request
event (each client request causes the service to serve the file)? The EARS
pattern differs between the two.

**Q3 (material — R2 response detail):** What specifically does "show the
legal-hold state" require at the boundary? Is it an error code, a structured
response field, a human-readable message, or some combination? Two
implementers will differ.

**Q4 (material — R2 response detail):** Is the denial of the download
accompanied by any HTTP status code or protocol signal that a verifier could
observe independently of the message body?

**Q5 (boundary — R2):** Can a single report be subject to multiple concurrent
legal holds? If yes, does the service need to report how many holds are active,
or only that at least one is active?

**Q6 (gap — authorization failure):** What shall the export service do when
a client requests a report it is not authorized for? R1 addresses authorized
reports only; the unauthorized path is undefined.

---

## Candidate Requirements

### Candidate 1 (from R1)

- **Status:** needs clarification
- **Template:** event-driven (provisional; see Q1 and Q2)
- **Requirement (provisional):** When a client requests download of an
  authorized report, the export service shall return the report to the client.
- **Source intent and rationale:** R1 states the baseline obligation: authorized
  reports must be downloadable. Rationale is access enablement for legitimate
  clients.

**Implementability contract**
- System boundary: export service external API boundary (protocol unspecified)
- Responsible system: export service
- Actor: client
- Affected interfaces: client-facing download endpoint; report storage
  (read side)
- Inputs and data definitions: identity of the report; client identity and
  authorization credential — definitions not supplied
- Preconditions, state, feature scope, and trigger: report exists; client is
  authorized for that report; report is NOT under legal hold (per R2 exception,
  owner-confirmed); trigger is a client download request
- Observable normal, alternate, and failure response: normal — report content
  (or access mechanism) returned; alternate — not specified; failure — not
  specified
- Quantities, units, limits, timing, ordering, and tolerances: none supplied;
  **open**
- Domain terms, dependencies, and unresolved decisions: "authorized" — not
  defined (Q1); "available for download" delivery mechanism **open** (Q1, Q2);
  authorization check mechanism not defined

**Verification contract**
- Setup, state, input data, and stimulus: an authorized report not under legal
  hold exists; client presents valid authorization; client issues a download
  request
- Observable evidence or oracle: the response at the service boundary contains
  (or provides access to) the report content
- Expected result: report is delivered to the client
- Pass/fail criteria, including scope, thresholds, units, and timing: delivery
  mechanism **open** (Q1); timing criteria **open**
- Boundary, negative, and failure cases: behavior when report does not exist —
  **open**; behavior when authorization is invalid — **open** (Q6)
- Required data, instrumentation, assessment, or external evidence: test report
  in authorized state; valid client credential
- Observable at the named boundary alone: yes (once delivery mechanism is
  resolved)
- If no, internal evidence needed and rationale: N/A

---

### Candidate 2 (from R2)

- **Status:** needs clarification
- **Template:** complex (While … when …)
- **Requirement (provisional):** While an authorized report is under legal
  hold, when a client requests a download of that report, the export service
  shall deny the download and shall return the legal-hold state to the client.
- **Source intent and rationale:** R2 is the owner-designated exception to R1.
  When a legal hold is active, client access is blocked and the hold status is
  surfaced. Rationale is compliance: the legal hold supersedes the general
  access entitlement.

> **Note:** The single source sentence contains two independently testable
> responses — denial of download, and return of the legal-hold state. They are
> drafted as two obligations above joined with `and shall` to preserve the
> source unit. If the host requires one `shall` per requirement, split into
> Candidate 2a (denial) and Candidate 2b (state disclosure).

**Implementability contract**
- System boundary: export service external API boundary
- Responsible system: export service
- Actor: client
- Affected interfaces: client-facing download endpoint
- Inputs and data definitions: legal-hold status is an attribute of the report;
  mechanism by which the service knows a hold is active — not defined
- Preconditions, state, feature scope, and trigger: precondition — report is
  under legal hold (state); trigger — client download request for that report;
  report is also authorized (the hold applies to authorized reports per source)
- Observable normal, alternate, and failure response: normal for this path —
  download denied; legal-hold state returned; form of "legal-hold state" at
  boundary **open** (Q3, Q4)
- Quantities, units, limits, timing, ordering, and tolerances: none supplied
- Domain terms, dependencies, and unresolved decisions: "legal hold" — applied
  by which authority and how communicated to the service — **open**; "show the
  legal-hold state" — format and channel **open** (Q3, Q4); multiple concurrent
  holds **open** (Q5)

**Verification contract**
- Setup, state, input data, and stimulus: authorized report marked as under
  legal hold; client with valid authorization issues a download request
- Observable evidence or oracle: (a) response does not contain or provide
  access to the report; (b) response contains a representation of the
  legal-hold state
- Expected result: download is denied; legal-hold state is present in the
  response
- Pass/fail criteria, including scope, thresholds, units, and timing: specific
  observable form of denial (e.g., HTTP status) **open** (Q4); specific form
  of legal-hold state **open** (Q3)
- Boundary, negative, and failure cases: report with multiple concurrent holds
  **open** (Q5); behavior when hold is lifted between request receipt and
  response — **open**
- Required data, instrumentation, assessment, or external evidence: report
  fixture in legal-hold state; client credential
- Observable at the named boundary alone: yes (once response form is resolved)
- If no, internal evidence needed and rationale: N/A

---

## Suggested Supporting Requirements

**S1 — Failure-path companion (related to Candidate 1):** When a client
requests a report it is not authorized for, the export service shall deny the
download without disclosing report content.
*Reason:* R1 addresses authorized reports; the unauthorized path is undefined.
Without this, two implementers may differ on whether the service silently
ignores the request, returns an error, or discloses existence of the report.

**S2 — Boundary companion (related to Candidate 2):** When the legal-hold
state of a report changes from held to released, the export service shall treat
subsequent download requests for that report as no longer subject to the
legal-hold denial.
*Reason:* R2 defines behavior during a hold but does not define when that
behavior ends. Without a lifecycle exit condition, verifiers cannot determine
when Candidate 1 re-applies.

**S3 — Optional consideration (related to Candidates 1 and 2):** The export
service may record an audit entry each time a download is granted or denied,
including the reason for denial.
*Reason:* Legal-hold compliance commonly requires an audit trail. The source
does not mention auditing; raise with the owner before adding.

---

## Consistency Findings

**Finding 1 — Intentional exception (Candidate 1 vs. Candidate 2):**
At first reading, Candidate 1 (grant download for authorized reports) and
Candidate 2 (deny download when authorized report is under legal hold) appear
to conflict at the intersection of authorized + held reports.

**Resolution:** The owner explicitly states that R2 is an exception to R1.
This is an **intentional exception**; no conflict remains. The EARS precondition
`While an authorized report is under legal hold` in Candidate 2 narrows the
scope so that Candidate 1 applies only when no legal hold is active. The
exception is owner-confirmed and does not require further resolution.

**Finding 2 — Gap:** Neither R1 nor R2 defines the service's response when a
client requests a report it is not authorized for. This is an undefined
behavior gap. See S1.

**Finding 3 — Gap:** The lifecycle of the legal-hold state (how a hold is
entered or released, and what the service observes to determine this) is
undefined. This gap affects both implementability and verification of Candidate
2.

---

## Assumptions

**A1:** "Authorized" in R1 and R2 refers to the same authorization concept —
the client has permission to access the specific report. This assumption is
provisional; Q1 asks about the delivery mechanism but authorization definition
also needs confirmation.

**A2:** The legal-hold precondition in Candidate 2 is an attribute of the
report, not of the client or the session, meaning any client requesting a held
report receives the denial, regardless of individual privilege level. This is
the natural reading but should be confirmed.

**A3:** The trigger for both candidates is an explicit, discrete client
download request arriving at the service boundary. If "available for download"
in R1 means a persistent published state rather than a per-request response,
the pattern for Candidate 1 changes to state-driven.

---

## Rationale and Context

R1 establishes the general access obligation: authorized clients can obtain
authorized reports. R2 carves out a compliance constraint: legal hold overrides
the general access right. The owner's explicit designation of R2 as an
exception to R1 is the operative precedence rule and has been preserved without
modification. The two candidates together form a consistent, non-contradictory
set once the exception is recorded — the primary unresolved issues are
definitional (delivery mechanism, response format, legal-hold state
representation) rather than structural conflicts between the requirements.

---

## Review Status

| Candidate | Status | Blocking item |
|-----------|--------|---------------|
| Candidate 1 (R1) | needs clarification | Q1 (delivery mechanism), Q2 (state vs. event), authorization definition undefined, failure path undefined |
| Candidate 2 (R2) | needs clarification | Q3 (legal-hold state format), Q4 (denial signal), Q5 (multiple holds), legal-hold lifecycle undefined |

No candidate is `ready for review`. Both are held at `needs clarification`
because material items in each contract remain open. Resolving Q1–Q6 and
confirming A1–A3 would allow both candidates to advance to `candidate` status,
subject to final contract review.
