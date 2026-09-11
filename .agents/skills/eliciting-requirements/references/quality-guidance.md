# Quality Guidance

Read this reference when supporting requirements or controlled-language
quality needs more detail than the main skill workflow.

## Controlled-Language Discipline

Use ASD-STE100 Simplified Technical English as a source of general writing
principles only. Do not reproduce its dictionary or rule text, claim
compliance or certification, or force software requirements into aerospace
maintenance vocabulary.

Prefer:

- One idea per sentence.
- Short sentences and active voice.
- Explicit subjects and concrete verbs.
- Consistent tense and defined domain terms.
- A roughly 20-25 word length as a prompt to inspect complexity, not a hard
  limit.

Do not suppress uncertainty. `Unclear`, `may`, `probably`, alternatives, and
provisional interpretations are valid surrounding language when the source is
uncertain. Ask for resolution instead of converting uncertainty into an
unsupported requirement.

Keep these implementation details out of the normative sentence unless they
are themselves an externally visible contract:

- Internal class, function, module, prompt, or agent names.
- Database tables, schemas, indexes, caches, or queue choices.
- Frameworks, programming languages, libraries, or deployment mechanisms.
- A particular algorithm, data structure, test framework, or file layout.

## Supporting Requirements

Look for behavior required to make the source request complete, implementable,
and verifiable. A suggestion must include the source behavior it relates to,
the missing behavior, and a short reason. Keep every suggestion outside the
candidate requirement list until the user accepts it.

Use one of these relationship labels:

- **Required companion:** needed for the source behavior to be complete.
- **Failure-path companion:** defines a material failure or recovery path.
- **Boundary companion:** defines an implied limit or edge case.
- **Interface companion:** defines behavior at a system or external boundary.
- **Operational companion:** defines an observable operational quality implied
  by the request.
- **Optional consideration:** plausible but not justified by the current
  request; ask whether it belongs in scope.

Ground suggestions in the request. Consider these areas only when relevant:

- Normal, alternate, negative, and recovery paths.
- Validation, missing or malformed values, duplicates, and boundaries.
- State entry, exit, transition, reset, and lifecycle behavior.
- Authentication, authorization, ownership, and permission failures.
- Persistence, consistency, idempotence, ordering, and concurrency.
- Dependency failures, timeouts, retries, cancellation, and partial
  completion.
- Performance, capacity, latency, availability, rate limits, and resource
  exhaustion.
- Security, privacy, auditability, retention, and sensitive-data handling.
- User feedback, accessibility, localization, compatibility, and observability.

Do not dump this list into the response. Do not invent a feature because it is
common in another system.

## Evaluation Expectations

The versioned Agent Eval Harness corpus covers all six templates, near-misses,
minimal pairs, vague and implementation-specific prose, supporting
requirements, readiness gating, conflicts, duplicates, gaps, precedence,
uncertainty, host metadata, multiple domains, malformed EARS, and implementation
temptations. A revision retains prior baselines, adds confirmed failures to the
regression corpus, and must pass deterministic structural and semantic judges.
