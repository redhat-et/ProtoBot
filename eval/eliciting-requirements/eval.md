---
type: skill-eval
skill: eliciting-requirements
analyzed_at: 2026-09-08
skill_hash: a855508aa19a
scoring_guide_updated_at: 2026-10-14
---

# Skill Evaluation Analysis

## Purpose

The skill turns a rough goal, feature request, interface description, existing
requirement, or requirement set into a host-independent review package. The
package contains EARS candidates, focused questions, grounded supporting
requirements, assumptions, rationale, verification contracts, and consistency
findings.

## Inputs

Each case supplies a `prompt` in `input.yaml`. The prompt may contain prose,
one requirement, or a set with host metadata. It also asks the evaluated agent
to write the same final package to `artifacts/result.md` so the harness can
collect a file artifact while still scoring the visible response.

`annotations.yaml` records expected semantic properties, readiness, corpus
partition, provenance, difficulty, disallowed behavior, and rationale.
`reference.md` is a human-calibration reference. It describes properties and
acceptable variants rather than exact wording.

## Outputs

The package is a markdown response with these sections:

- Understanding
- Clarifying Questions
- Candidate Requirements
- Suggested Supporting Requirements
- Consistency Findings
- Assumptions
- Rationale and Context
- Review Status

Each candidate has an EARS template, status, rationale, implementability
contract, and verification contract. A candidate may use the explicit
`unresolved` template value when pattern selection is blocked; it must then be
`needs clarification` and explain the missing decision. No candidate is ready
for review unless both contracts pass.

## Quality Criteria

- Correctly distinguish ubiquitous, event-driven, state-driven, optional
  feature, unwanted behavior, and complex EARS patterns.
- Preserve intent and uncertainty without silently inventing facts.
- Keep requirements observable and implementation-independent.
- Ask targeted questions for missing boundary, trigger, state, measurement,
  failure, and terminology information.
- Suggest only grounded companion behavior and label its relationship.
- Detect conflicts, overlaps, duplicates, gaps, and precedence questions with
  affected candidates and consequences.
- Keep host metadata while avoiding a host-specific workflow.
- Reject broad quality claims and incomplete contracts as needing clarification.

## Judges

The inline judges cover exit status, portable section structure, readiness
gating, expected template labels, and canonical EARS syntax. The two rubric
judges assess semantic intent preservation and review discipline. The baseline
records category floors for template selection, readiness gating, consistency,
leakage, controlled language, and domain diversity; a revision cannot hide a
critical structural failure behind a semantic average.

## Regression Method

Visible development cases live in `dataset/cases/`. Known regression cases
live in `dataset/regression/` and use `eval-regression.yaml`; they are not an
independent held-out corpus. Confirmed failures are added to
`dataset/regression/` before the skill is changed. Issue #63 records the
calibration sample under `calibration/v1/`. Independent human scoring,
adjudication, and held-out promotion remain pending; the v1 baseline is not
trusted until that work completes. Live harness runs retain their case
outputs, traces, costs, latency, and human calibration artifacts under the
run ID; a new baseline is a new directory and never replaces an earlier one.

## Automated Case Gates

The inline judges are binary gates. Record the exact rationale for every
failure. A rubric score never overrides a failed deterministic gate.

| Judge | Pass when | Fail when |
| --- | --- | --- |
| `execution_succeeded` | The case exits with code `0`. | The case times out, crashes, or exits non-zero. |
| `package_structure` | All eight portable sections are present: `Understanding`, `Clarifying Questions`, `Candidate Requirements`, `Suggested Supporting Requirements`, `Consistency Findings`, `Assumptions`, `Rationale and Context`, and `Review Status`. | A required section is absent or its role is unclear. |
| `readiness_gate` | The output matches the annotated readiness: clarification cases contain no ready candidate; mixed cases contain both ready and clarification candidates; ready cases contain only ready candidates and both contracts for every candidate. | The output is falsely promoted, over-gated, or missing a required contract. |
| `ears_structure` | Each normative candidate uses an expected template or accepted alternative, has exactly one `shall`, and handles explicitly blocked or unresolved drafting as allowed. | A required pattern is malformed or absent, no required normative `shall` exists, or a normative sentence contains multiple `shall` clauses. |
| `ears_template_coverage` | Each expected label, or one label from each accepted alternative group, appears in the candidate package. | A required label or every allowed alternative is missing. |
| `category_critical_gate` | The category-specific safety behavior is present: ambiguity is gated, consistency issues are named, implementation leakage is discussed, and ready cases state readiness. | The required critical behavior is absent. |

The checks are intentionally lightweight string and structure checks. In
particular, a readiness check may count contract headings without proving that
the contract fields are complete, and template coverage may find a label in
the wrong section. Human scoring must inspect the candidate and its contracts
against the annotations; do not treat a passing string check as semantic proof.

## Human Calibration Score Schema

The calibration file is `calibration/v1/scores.yaml`. It is the source of
truth for what reviewers record. For **each sampled case and each reviewer**,
fill only these fields:

```yaml
semantic_quality: 1        # integer from 1 through 5
review_discipline: 1       # integer from 1 through 5
critical_failure: false    # boolean
false_ready: false         # boolean
notes: "short property-based observations"
```

Do not add or substitute per-category numeric fields in `scores.yaml`.
Template selection, readiness, supporting requirements, consistency,
controlled language, host independence, and verification are **diagnostic
lenses** used to justify the two numeric scores and the two flags. Mention
those lenses in `notes` when they explain the score.

Score against `annotations.yaml` and the property-based `reference.md`, not an
exact gold response. Equivalent EARS wording is acceptable when it preserves
the same boundary, condition, response, quantity, uncertainty, and
verification meaning.

### `semantic_quality` — integer 1 through 5

This score summarizes intent preservation, EARS meaning, readiness, supporting
requirements, consistency analysis, and verification quality.

| Score | Required interpretation |
| --- | --- |
| **5** | All applicable required findings and reference properties are satisfied; the EARS meaning is correct; contracts are complete; uncertainty is preserved; no disallowed behavior occurs; and no material omission remains. |
| **4** | The core behavior and readiness decision are correct. Only minor, non-blocking omissions, redundancy, or presentation issues remain. |
| **3** | The package is useful and substantially correct, but has one material, locally repairable omission or unclear contract field. It must not contain a false-ready decision. |
| **2** | Multiple material omissions, a major misclassification, implementation leakage, suppressed uncertainty, or an unsafe readiness decision is present. |
| **1** | The response changes intent, invents behavior or policy, claims unsupported approval, is unusable, or exhibits a critical disallowed behavior. |

When assigning the score, check these lenses as evidence rather than storing
separate scores:

- **Template and EARS:** correct pattern, splitting, conditions, and one
  `shall` per normative candidate.
- **Precision and intent:** boundary, actor, inputs, responses, quantities,
  timing, exclusions, terminology, and source uncertainty are preserved.
- **Readiness:** annotated `needs_clarification`, `mixed`, and
  `ready_for_review` states are respected and both contracts are meaningful.
- **Supporting requirements:** companions are grounded, relationship-labeled,
  and separate from primary candidates.
- **Consistency:** conflicts, duplicates, gaps, overlaps, precedence, and
  intentional exceptions are correctly identified without unauthorized
  resolution.
- **Verification:** the setup, observable boundary, oracle, expected result,
  pass/fail rule, quantities, edge cases, instrumentation, and observability
  limits are usable by an independent verifier.

### `review_discipline` — integer 1 through 5

This score summarizes separation of certainty levels, host independence,
assumptions, suggestions, findings, contracts, and host-owned review status.

| Score | Required interpretation |
| --- | --- |
| **5** | Understanding, questions, candidates, suggestions, assumptions, rationale, findings, contracts, and review status are clearly separated; uncertainty remains visible; host metadata is preserved; no approval, lifecycle, storage, or implementation workflow is invented. |
| **4** | The review package is disciplined and host-independent, with only a minor presentation, metadata, or certainty-separation omission. |
| **3** | One certainty level is mixed, one contract field is unclear, or host-specific context is handled ambiguously, but the package remains recoverable and does not claim approval. |
| **2** | The response suppresses uncertainty, mixes suggestions with candidates, invents workflow or approval, or introduces material host-specific behavior. |
| **1** | The response claims approval, silently resolves a conflict, prescribes implementation as a requirement, or omits the portable package. |

### `false_ready` — boolean

Set `false_ready: true` when a case presents a candidate as `ready for review`
while a material implementability or verification contract item remains open,
unresolved, pending, or incomplete. Also set it when a
`needs_clarification` case contains a ready candidate. A complete case marked
`needs clarification` is over-gated but is not false-ready.

### `critical_failure` — boolean

Set `critical_failure: true` for any `false_ready` result, regardless of the
two numeric scores. Also set it for another issue that must not pass regardless
of aggregate score, such as changed intent, invented approval or policy,
normative implementation leakage, unauthorized conflict resolution, a missed
direct conflict, or a failed critical structural gate. Explain the reason in
`notes`.

Set `critical_failure: false` only when no such release-blocking behavior is
present. A high semantic score does not make a true critical flag harmless.

### `notes` — string

Write short, property-based observations. Cite the candidate, question,
contract, or finding that supports the scores and flags. Do not paste the full
package and do not demand exact reference wording.

## Case Review Procedure

1. Confirm reviewer identities and `assigned_at` values in `sample.yaml` before
   scoring. Do not score a pending reviewer slot.
2. Read the case input, annotations, property-based reference, generated
   package, final response, and deterministic judge results.
3. Record evidence for semantic quality and review discipline before comparing
   with another reviewer.
4. Set `false_ready` and then `critical_failure`; the latter must be true when
   the former is true.
5. Write the five fields for the case in `scores.yaml`; do not add category
   score keys.
6. Compare reviewers by field. Adjudicate critical flags separately from the
   numeric scores.

The case coverage in the calibration sample is:

| Cases | Primary evidence to inspect |
| --- | --- |
| 001–006 | EARS pattern selection and complex splitting |
| 007, 015 | Implementation leakage and measurable behavior |
| 008, 009, 023, 024 | False-ready rejection and both readiness contracts |
| 010 | Grounded supporting requirements and mixed readiness |
| 011, 013, 018, 019 | Conflict, duplicate, gap, deadline, and intentional-exception analysis |
| 012 | Host metadata and host independence |
| 014 | Uncertainty preservation |
| 016 | Malformed EARS and minimal-pair diagnosis |
| 017 | Failure and retry lifecycle |
| 020, 021 | Controlled-language and instruction-separation discipline |
| 022 | CLI-domain preservation and dry-run observability |

## Agreement and Adjudication

The calibration sample contains 15 cases: 11 development and 4 regression.
Use the configured percent-agreement and Cohen's kappa method in
`agreement.yaml`.

- Compare `critical_failure` and `false_ready` flags separately from the
  numeric scores.
- Adjudicate every critical-flag disagreement, every score difference larger
  than one point, and every level-1/2 versus level-4/5 disagreement.
- Use 100% critical-flag agreement, at least 80% percent agreement, and κ ≥
  0.60 for each numeric score as the minimum calibration target.
- Record whether each disagreement is a rubric, judge, skill, or corpus
  correction in `adjudication.yaml`.
- Do not mark the baseline trusted until identities, scores, agreement,
  adjudication, and required judge comparisons are complete.

Regression cases use the same absolute scoring rubric and thresholds as
development cases, but their named trap is a non-regression contract. They are
not an independent held-out sample. An intentional exception is not a
conflict merely because two responses differ; score whether the exception and
its scope are preserved.
