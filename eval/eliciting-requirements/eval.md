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

## How to Judge a Case

### 1. Use the correct evidence

Review one case at a time. Read the following in this order:

1. `input.yaml` — the source request and the task given to the evaluated skill.
2. `annotations.yaml` — expected readiness, templates or alternatives,
   required findings, disallowed behaviors, category, and provenance.
3. `reference.md` — the property-based human-calibration reference. It is
   evidence about intent and acceptable variants, not an exact answer key.
4. The collected `artifacts/result.md` and the final response captured by the
   runner.
5. The case's judge results and rationale in `summary.yaml`.

Do not reward exact wording. Score whether the output preserves the source
intent and satisfies the annotated properties. Equivalent EARS sentences are
acceptable when they keep the same boundary, condition, response, quantity,
uncertainty, and verification meaning.

The evaluated agent must not see `annotations.yaml` or `reference.md` while it
runs. A reviewer may read them after execution. A missing artifact is a
collection failure, not an invitation to score the final response as if the
artifact existed.

### 2. Apply deterministic gates before qualitative scoring

The inline judges are binary case gates. Record the exact rationale; do not
turn a failed gate into a partial pass because a rubric judge liked the prose.

| Judge | Pass when | Fail when |
| --- | --- | --- |
| `execution_succeeded` | The case exits with code `0`. | The case times out, crashes, or exits non-zero. |
| `package_structure` | All eight portable sections are present: `Understanding`, `Clarifying Questions`, `Candidate Requirements`, `Suggested Supporting Requirements`, `Consistency Findings`, `Assumptions`, `Rationale and Context`, and `Review Status`. | Any required section is absent or merged so that its role is unclear. |
| `readiness_gate` | The output matches the annotated readiness: `needs clarification` has no ready candidate; `mixed` has at least one `ready for review` and one `needs clarification` candidate; `ready_for_review` has all candidates ready and both contracts visible for every candidate. | Readiness is falsely promoted, overly gated for a ready case, or a candidate lacks an implementability or verification contract. |
| `ears_structure` | Each normative candidate uses an expected template or an accepted alternative, its sentence contains exactly one `shall`, and explicitly blocked/unresolved drafting is handled as allowed by the judge. | A required pattern is absent or malformed, no normative `shall` sentence exists when one is required, or a normative sentence contains multiple `shall` obligations. |
| `ears_template_coverage` | The expected template label, or one label from each accepted alternative group, appears in the package. | A required label or every allowed alternative is missing. |
| `category_critical_gate` | The category-specific safety behavior is present: ambiguity is gated, consistency issues are named, implementation leakage is discussed, and ready cases state readiness. | The case omits the required critical behavior. |

`semantic_quality` and `review_discipline` are numeric rubric judges. They do
not override a failed deterministic gate. A case with a semantic score of 5
can still fail the evaluation if, for example, the readiness gate is false or
an EARS sentence contains two `shall` clauses.

### 3. Score the qualitative categories from 1 to 5

Use integer scores only. Score each category independently before assigning
the two rubric-judge scores. Cite the candidate, question, contract, or
finding that supports the score. Do not average away a critical failure.

#### A. Template selection and EARS form

This category covers `ubiquitous`, `event-driven`, `state-driven`, `optional
feature`, `unwanted behavior`, `complex`, and `unresolved`, as well as
splitting and one-`shall` syntax.

| Score | Anchor |
| --- | --- |
| **5** | Every applicable obligation has the right template; state, event, optional capability, unwanted condition, and complex combinations are distinguished correctly; independent obligations are split; each normative candidate has one `shall`; unresolved pattern selection is explicitly gated. |
| **4** | The main templates and conditions are correct. There is only a minor label, wording, or splitting issue that does not change the requirement's meaning or readiness. |
| **3** | The output is useful but has one material template or splitting error, such as treating a state as an event or combining two independently verifiable obligations. The error is locally repairable. |
| **2** | Multiple template choices are wrong, a key condition or trigger is lost, or a malformed normative sentence creates a significant interpretation risk. |
| **1** | The package has no usable EARS structure, changes the source condition/response, or consistently treats implementation prose as an EARS requirement. |

#### B. Precision and intent preservation

This category covers the named system boundary, actors, inputs, conditions,
responses, quantities, timing, exclusions, terminology, and uncertainty.

| Score | Anchor |
| --- | --- |
| **5** | All material source obligations, constraints, exclusions, and host metadata are preserved; questions target real ambiguity; assumptions are explicit; no behavior is invented or silently resolved. |
| **4** | The intended behavior is preserved with only minor omissions, redundant questions, or a non-blocking terminology gap. No material fact is changed. |
| **3** | The package is useful but omits one material boundary, condition, measurement, failure path, or term, or presents one provisional interpretation too confidently. |
| **2** | Several material facts are omitted or invented; implementation choices leak into normative requirements; or uncertainty is converted into an unsupported decision. |
| **1** | The response changes the requested behavior, invents policy, makes a false-ready claim, or is not usable as a requirements package. |

#### C. Readiness gating and contracts

This category covers the distinction between `needs clarification`, `candidate`,
and `ready for review`, and the completeness of implementability and
verification contracts.

| Score | Anchor |
| --- | --- |
| **5** | Readiness exactly matches the annotation; every candidate has complete boundary, input, precondition, trigger, response, quantity, and terminology fields plus setup, oracle, expected result, pass/fail, boundary-case, instrumentation, and observability fields; open material questions remain gated. |
| **4** | The readiness decision is correct and both contracts are substantially complete, with only a minor non-blocking field or presentation omission. |
| **3** | The output identifies the right uncertainty but has one incomplete contract field or a borderline status decision; it does not falsely approve a materially incomplete requirement. |
| **2** | A ready candidate is presented despite an open material contract, a clarification case contains a ready candidate, a mixed case lacks one required status, or multiple contract fields are missing. |
| **1** | The output ignores the readiness gate, claims approval, or supplies no meaningful implementability or verification contract. |

#### D. Supporting requirements

This category covers companions suggested outside the primary candidate list.
Each suggestion should have a grounded relationship label such as
failure-path, boundary, required, operational, or optional consideration.

| Score | Anchor |
| --- | --- |
| **5** | Suggestions cover the material adjacent behavior identified by the source, are traceable to a candidate or gap, use relationship labels, remain outside the normative candidate list, and do not invent policy. |
| **4** | Suggestions are grounded and separated, with only one minor missing companion or weak rationale. |
| **3** | Some useful companions are present, but one material failure, boundary, dependency, persistence, or operational concern is missing or the separation is ambiguous. |
| **2** | Suggestions are generic, unrelated, implementation-prescriptive, or turn an unapproved policy into a normative requirement. |
| **1** | Suggestions replace the requested elicitation, introduce substantial unsupported behavior, or are absent where the case explicitly requires companion analysis. |

#### E. Consistency analysis

This category covers conflicts, duplicates, overlaps, gaps, vacuous wording,
precedence, intentional exceptions, and their consequences.

| Score | Anchor |
| --- | --- |
| **5** | The package identifies each material issue, names affected candidates or source statements, explains the observable consequence, and asks for resolution without silently choosing a priority. Intentional exceptions are distinguished from conflicts. |
| **4** | The main consistency issue is correctly identified and scoped; only a minor consequence, affected identifier, or resolution option is missing. |
| **3** | The output notices an issue but describes it shallowly, misses one material gap/overlap, or does not fully connect it to readiness. |
| **2** | A major conflict or duplicate is missed, misclassified, or resolved without authority; a clear gap is treated as harmless. |
| **1** | The package claims consistency or approval despite an obvious conflict, or provides no usable consistency findings for a case that requires them. |

#### F. Review discipline, host independence, and certainty separation

This category covers separation of understanding, questions, candidates,
suggestions, assumptions, rationale, findings, contracts, and host-owned review
status. It also covers preserving host metadata without inventing ProtoBot or
another product workflow.

| Score | Anchor |
| --- | --- |
| **5** | Every certainty level is clearly separated; assumptions remain provisional; suggestions are not approved requirements; review status is host-owned; host metadata is preserved; no approval, lifecycle, storage, or implementation workflow is invented. |
| **4** | Separation and host independence are clear with one minor presentation or metadata omission. |
| **3** | One certainty level is mixed, one contract field is unclear, or host-specific context is handled ambiguously, but the response remains recoverable. |
| **2** | The response suppresses uncertainty, invents workflow or approval, mixes suggestions with candidates, or introduces material host-specific behavior. |
| **1** | The response claims approval, silently resolves a conflict, prescribes implementation as a requirement, or omits the portable package. |

#### G. Verification quality and observability

This category is a focused review of whether the verification contract can be
used by an independent verifier at the named boundary. It supports the
readiness and semantic scores; it is not a replacement for the deterministic
readiness gate.

| Score | Anchor |
| --- | --- |
| **5** | Verification setup, observable boundary, oracle, expected result, pass/fail rule, timing/quantity measure, boundary cases, instrumentation, and observability limits are explicit and implementable without guessing. |
| **4** | The contract is independently usable with one minor missing detail that does not change the oracle or pass/fail decision. |
| **3** | The intended verification is clear but one material oracle, boundary case, measurement, or observability limitation is incomplete. |
| **2** | Verification depends on an unspecified internal implementation, lacks a reliable pass/fail rule, or omits several material setup/oracle fields. |
| **1** | No usable verification contract is present, or the proposed check cannot observe the stated requirement at any named boundary. |

### 4. Convert category scores into rubric-judge scores

The configured rubric judges use the same 1–5 scale:

- `semantic_quality` summarizes template selection, precision, readiness,
  supporting requirements, consistency, and verification quality.
- `review_discipline` summarizes separation of certainty levels, host
  independence, assumptions, suggestions, findings, contracts, and
  host-owned review status.

Use these overall anchors after reviewing the detailed categories:

| Score | `semantic_quality` / `review_discipline` decision rule |
| --- | --- |
| **5** | All material required findings are present, no disallowed behavior occurs, all critical category scores are 5, and any remaining issue is purely stylistic or optional. |
| **4** | The package is correct and useful with one or more minor omissions or presentation issues; no material intent, readiness, consistency, or host-independence failure exists. |
| **3** | The package is partially useful but has one material omission, incomplete contract, shallow consistency finding, or mixed certainty level. It is not unsafe enough for a 1–2, but needs revision. |
| **2** | There are multiple material omissions, an unsafe readiness decision, implementation leakage, suppressed uncertainty, or a major missed finding. |
| **1** | Intent is changed, approval or policy is invented, uncertainty is silently resolved, the package is absent/unusable, or a critical disallowed behavior dominates the response. |

Do not award a 5 because the prose is thorough if a deterministic gate fails.
Do not lower a score merely because the wording differs from `reference.md`.
The configured aggregate thresholds are a mean of at least 3.5 for each
numeric judge; structural and readiness judges must meet their 100% floors.

### 5. Case-level decision and report format

For each case, record:

```text
Case: <case directory>
Partition: development | regression | held-out
Deterministic gates: pass/fail, with the failed judge and rationale
Template/EARS: <1-5> — evidence
Precision/intent: <1-5> — evidence
Readiness/contracts: <1-5> — evidence
Supporting requirements: <1-5> — evidence
Consistency analysis: <1-5> — evidence
Review discipline/host independence: <1-5> — evidence
Verification/observability: <1-5> — evidence
Semantic quality: <1-5>
Review discipline: <1-5>
Case decision: pass | revise | critical failure
Reviewer note: <specific evidence and suggested correction>
```

The case decision is `pass` only when all required deterministic gates pass and
the qualitative review finds no material issue. Use `revise` for a useful
package with a correctable material omission. Use `critical failure` for a
false-ready decision, changed intent, invented approval/policy, implementation
leakage in a normative requirement, missed direct conflict, or failed critical
structural gate.

When the score and a deterministic gate disagree, report both values and keep
the gate failure. For example, a semantically excellent package can still be a
critical structural failure if a normative candidate contains two `shall`
obligations. A regression case passing means that the known failure mode did
not recur; it does not turn the regression corpus into an independent held-out
sample.

### 6. Calibration and adjudication

Two reviewers should score the same calibration sample independently before
using the rubric for a baseline. Reviewers should record evidence before
discussing scores. Discuss disagreements by category, not by overall average:

1. Identify the exact sentence, contract field, question, or finding at issue.
2. Compare it with the `annotations.yaml` property and `reference.md`
   acceptable variants.
3. Decide whether the issue is material, minor, or stylistic.
4. Apply the category anchor and record the adjudication rationale.

Do not tune the visible development corpus to improve a known regression case.
A confirmed calibration failure is added to `dataset/regression/`, then the
skill and both corpora are rerun separately. Keep the original run and
baseline directories intact; review notes belong in the run's `review.yaml`.

### 7. Borderline examples from this evaluation

- A candidate that correctly analyzes an unwanted condition but labels the
  candidate only `Complex` can receive a high semantic score while still
  failing `ears_structure` when the annotated contract requires the explicit
  `Unwanted behavior` label.
- A mixed-readiness case with excellent questions still fails
  `readiness_gate` if it has no `ready for review` candidate.
- A conflict analysis can be semantically correct while failing `ears_structure`
  if one normative sentence contains two `shall` clauses. Split the
  independently verifiable obligations or explicitly block drafting until the
  conflict is resolved.
- A regression case with a deliberate exception is not a conflict merely
  because two responses differ. Score whether the output preserves the stated
  exception and its scope instead of forcing a precedence decision.

## Category Applicability and Case-Specific Focus

The `category` in `annotations.yaml` identifies the primary failure mode for a
case. Apply the cross-cutting A–G scores above to every applicable case, then
give the primary category extra weight in the rationale. Mark a category `N/A`
when the annotations contain no property for it; exclude `N/A` categories from
means rather than treating them as zero.

| Primary category | Cases | What the reviewer must establish |
| --- | --- | --- |
| `template-selection` | 001–006 | The selected EARS meaning, conditions, splitting, and one-`shall` form are correct. |
| `readiness-gating` | 008, 009, 023, 024 | Material uncertainty is gated, complete contracts are required for readiness, and complete cases are not over-gated. |
| `consistency-analysis` | 011, 013, 018, 019 | Conflicts, duplicates, gaps, inconsistent deadlines, and intentional exceptions are distinguished and not silently resolved. |
| `elicitation-and-leakage` | 007, 015 | Design ideas stay out of normative requirements and observable behavior is elicited instead. |
| `controlled-language` | 020, 021 | Vague referents, passive or vacuous language, `etc.`, embedded instructions, and defined terms are handled without claiming certification. |
| `domain-diversity` | 022 | The CLI boundary, command, flags, dry-run behavior, and no-modification oracle are preserved. |
| `supporting-requirements` | 010 | Invalid-input, duplicate-submission, persistence, and dependency-failure companions are grounded and separate. |
| `host-independence` | 012 | Supplied identifiers, tags, trace, API boundary, timing boundary, and host-independent behavior are preserved. |
| `uncertainty-preservation` | 014 | `probably`, `may`, and undecided owner policy remain uncertainty rather than becoming invented requirements. |
| `malformed-and-minimal-pairs` | 016 | Malformed EARS, state/event distinctions, and ambiguous deletion semantics are identified. |
| `failure-and-lifecycle` | 017 | Normal persistence is separated from retry/recovery, with bounded and observable lifecycle behavior. |

For the primary category, retain the universal 1–5 anchors in the earlier
tables and apply these case-specific interpretations:

- **`template-selection`:** A 5 distinguishes every required EARS pattern and
  splits independent obligations. A 3 has one locally repairable pattern or
  splitting error. A 2 loses a material trigger/state or has multiple wrong
  patterns. A 1 changes the condition/response or has no usable EARS form.
- **`readiness-gating`:** A 5 exactly matches annotated readiness with complete
  contracts. A 3 has one incomplete non-critical field without false readiness.
  A 2 contains false-ready, over-gates a complete case, or fails a mixed-status
  requirement. A 1 ignores the gate or claims approval.
- **`consistency-analysis`:** A 5 names affected requirements, consequences,
  and resolution options for every issue. A 3 identifies the issue but is
  shallow or incomplete. A 2 misclassifies or silently resolves a major issue.
  A 1 claims consistency or approval despite a direct conflict.
- **`elicitation-and-leakage`:** A 5 separates technologies and design ideas
  from observable requirements and asks for measurable behavior. A 3 has one
  material unanswered question. A 2 places technology in a normative candidate
  or invents `quickly`/`gracefully`. A 1 approves design choices as
  requirements.
- **`controlled-language`:** A 5 identifies every vague referent, passive or
  vacuous phrase, embedded instruction, and defined term while preserving the
  `quiescent-window`. A 3 misses one language defect. A 2 invents an actor or
  policy or partially follows the embedded instruction. A 1 approves the text
  or claims controlled-language certification.
- **`domain-diversity`:** A 5 preserves the CLI command, valid path,
  preview-only behavior, changed-file report, and no-modification oracle. A 3
  omits one
  material CLI oracle detail. A 2 changes the interface domain or treats
  dry-run as an internal implementation detail. A 1 changes the behavior.
- **`supporting-requirements`:** A 5 keeps primary requirements separate from
  grounded invalid-input, duplicate, persistence, and dependency-failure
  companions. A 3 misses one material companion. A 2 adds generic or
  unrelated suggestions or approves idempotency policy. A 1 replaces
  elicitation with unsupported behavior.
- **`host-independence`:** A 5 preserves `BILL-REQ-42`, both tags, the trace,
  API and timing boundaries, amount/currency constraints, and terminal
  `indeterminate` semantics without host workflow terms. A 3 omits one
  material boundary detail. A 2 introduces storage, approval, or Drafting Table
  behavior. A 1 replaces the identifier or claims host approval.
- **`uncertainty-preservation`:** A 5 keeps uncertain source language and asks
  for the missing measure, threshold, boundary, and warning behavior. A 3
  handles one uncertainty weakly. A 2 turns uncertainty into `shall` or picks
  a threshold. A 1 resolves the policy or marks it ready.
- **`malformed-and-minimal-pairs`:** A 5 diagnoses each malformed statement,
  distinguishes a click event from an ongoing state, and questions `Where` and
  deletion scope. A 3 incompletely diagnoses one statement. A 2 accepts a
  malformed statement or treats clicking as a state. A 1 accepts the set as
  ready or invents deletion scope.
- **`failure-and-lifecycle`:** A 5 separates persistence from recovery and asks
  about retry bounds, backoff, cancellation, duplicate effects, partial
  completion, terminal failure, and observable evidence. A 3 misses one
  material lifecycle question. A 2 invents infinite retries or retry policy. A
  1 approves unsafe retry semantics.

The score of **4** for any primary category is the universal minor-issue
anchor: the core behavior is correct, the readiness decision is safe, and only
non-blocking omissions or presentation issues remain.

## Aggregation and Critical Flags

The current YAML judges aggregate the two numeric scores by mean, but a human
review must also preserve the weakest applicable category:

- `semantic_quality` is anchored by the lowest applicable score among template
  selection, precision/intent, readiness, supporting requirements,
  consistency, verification, and the primary category.
- `review_discipline` is anchored by the lowest applicable score among review
  discipline, host independence, certainty separation, assumptions,
  suggestions, findings, contracts, and the primary category.
- Do not average away a category score of 1 or 2.
- A human case `pass` requires every deterministic gate to pass, no material
  issue, and both overall scores at least 4. A score of 3 is useful but needs
  revision; it does not meet the case-level pass rule.
- Set `false_ready: true` whenever a material contract is unresolved but a
  candidate is marked `ready for review`. Every `false_ready` result also sets
  `critical_failure: true`.
- Set `critical_failure: true` for any failed critical deterministic gate,
  changed intent, invented approval or policy, normative implementation
  leakage, missed direct conflict, or unauthorized conflict resolution.

The configured run-level thresholds remain the machine contract: deterministic
judges require a 100% pass rate and numeric judges require a mean of at least
3.5. A run can meet a numeric mean while still failing release because one
case has a critical deterministic failure.

## Calibration Protocol

The calibration sample contains 15 cases: 11 development cases and 4
regression cases. Two reviewers should score the same sample independently
before trusting a baseline. Record `semantic_quality`, `review_discipline`,
`false_ready`, and `critical_failure` separately for every case.

1. Assign distinct reviewer identities and assignment timestamps.
2. Score the generated package against the annotations and property-based
   reference, not against an authored gold answer.
3. Record evidence tied to a candidate, question, contract, or finding before
   comparing reviewers.
4. Compare critical flags separately from numeric scores.
5. Adjudicate every critical-flag disagreement, every score difference larger
   than one point, and every level-1/2 versus level-4/5 disagreement.
6. Use the configured percent-agreement and Cohen's kappa method. Treat 100%
   critical-flag agreement, at least 80% percent agreement, and κ ≥ 0.60 for
   each numeric score as the minimum calibration target.
7. Record whether each disagreement was a rubric, judge, skill, or corpus
   correction. Do not mark the baseline trusted until adjudication is complete.

Regression cases use the same absolute rubric and thresholds as development
cases, but their named trap is a non-regression contract. They are not an
independent held-out sample. In particular, an intentional exception is not a
conflict merely because two responses differ; score whether the exception and
its scope are preserved.
 