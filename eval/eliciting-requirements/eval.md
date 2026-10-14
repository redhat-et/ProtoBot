---
type: skill-eval
skill: eliciting-requirements
analyzed_at: 2026-09-08
skill_hash: a855508aa19a
scoring_guide_updated_at: 2026-10-14
---

# Eliciting Requirements Skill Evaluation Guide

## 1. Purpose

This evaluation measures whether the `eliciting-requirements` skill turns a
rough goal, feature request, interface description, existing requirement, or
requirement set into a host-independent review package.

The package should contain:

- EARS candidate requirements;
- focused clarifying questions;
- grounded supporting requirements;
- assumptions and rationale;
- implementability and verification contracts; and
- consistency findings.

The evaluation is property-based. It accepts equivalent wording when the
output preserves the same boundary, condition, response, quantity,
uncertainty, and verification meaning. It does not require one exact answer.

## 2. Corpus and Configuration

| Partition | Config | Dataset | Purpose |
| --- | --- | --- | --- |
| Development | `eval.yaml` | `dataset/cases/` | Visible cases used to assess and improve the skill. |
| Regression | `eval-regression.yaml` | `dataset/regression/` | Known failure cases; do not tune the development corpus to hide them. |
| Held-out | A separate future config | `dataset/held-out/` | Independent evaluation; currently empty and must not be described as evaluated. |

`baselines/v1/` and `calibration/v1/` are snapshot and calibration versions.
They are not required to match a run ID. A run ID identifies one harness
execution; a baseline or calibration directory records a maintained snapshot
that may refer to one or more runs.

The calibration sample contains 15 cases: 11 development cases and 4
regression cases. The current development and regression datasets may contain
more cases than that sample.

## 3. Case Contract

Each case contains:

- `input.yaml` — the source prompt given to the evaluated skill;
- `annotations.yaml` — expected readiness, templates or alternatives,
  required findings, disallowed behaviors, category, partition, provenance,
  and difficulty; and
- `reference.md` — property-based human-calibration guidance.

The evaluated agent is asked to return the package in its final response and
write the same package to `artifacts/result.md`. The required portable package
sections are:

1. Understanding
2. Clarifying Questions
3. Candidate Requirements
4. Suggested Supporting Requirements
5. Consistency Findings
6. Assumptions
7. Rationale and Context
8. Review Status

The runner temporarily removes `annotations.yaml` and `reference.md` from the
case workspace and denies OpenCode read, grep, and glob access to those paths.
Reviewers may read them after the run. A missing artifact is a collection
failure; do not score the final response as if the artifact existed.

## 4. Run the Evaluation

From the repository root, use the model identifier accepted by the active
runner. The current development config uses `anthropic/claude-sonnet-4-6`.

```text
/eval-setup --skip-mlflow
/eval-run --config eval/eliciting-requirements/eval.yaml --model anthropic/claude-sonnet-4-6 --run-id v2
/eval-review --config eval/eliciting-requirements/eval.yaml --run-id v2
```

Run the regression corpus separately:

```text
/eval-run --config eval/eliciting-requirements/eval-regression.yaml --model anthropic/claude-sonnet-4-6 --run-id v2-regression
/eval-review --config eval/eliciting-requirements/eval-regression.yaml --run-id v2-regression
```

The harness stores runs by the skill under test, so both configs may produce
paths under `eval/runs/eliciting-requirements/<run-id>/`. Keep the automated
development and regression run outputs, summaries, and judge reports separate;
do not combine them into one automated run or score report.

The human calibration sample is the intentional exception: `sample.yaml`
contains 11 development cases and 4 regression cases, and
`calibration/v1/scores.yaml` stores both partitions under each independent
reviewer block. Keep the partition and case IDs from `sample.yaml`; do not
create a single averaged score that loses which partition a case belongs to.

Each run should retain `summary.yaml`, `run_result.json`, `collection.json`,
per-case outputs and judge findings, traces, latency, cost data when
available, and any human review artifacts. A new baseline is a new directory;
never overwrite the prior baseline.

## 5. Automated Scoring

The inline judges are binary gates. A rubric score cannot override a failed
deterministic gate.

| Judge | Pass when | Fail when |
| --- | --- | --- |
| `execution_succeeded` | The case exits with code `0`. | The case times out, crashes, or exits non-zero. |
| `package_structure` | All eight required package sections are present. | A required section is absent or its role is unclear. |
| `readiness_gate` | The package matches annotated readiness: clarification cases have no ready candidate; mixed cases have both ready and clarification candidates; ready cases have only ready candidates and both contracts for every candidate. | The package is falsely promoted, over-gated, or missing a required contract. |
| `ears_structure` | Each normative candidate uses an expected template or accepted alternative, has exactly one `shall`, and handles explicitly blocked or unresolved drafting as allowed. | A required pattern is malformed or absent, no required normative `shall` exists, or a normative sentence contains multiple `shall` clauses. |
| `ears_template_coverage` | Each expected label, or one label from each accepted alternative group, appears in the candidate package. | A required label or every allowed alternative is missing. |
| `category_critical_gate` | The category-specific safety behavior is present: ambiguity is gated, consistency issues are named, implementation leakage is discussed, and ready cases state readiness. | The required critical behavior is absent. |

These checks are intentionally lightweight. For example, a readiness check may
count contract headings without proving their contents, and template coverage
may find a label in the wrong section. Human reviewers must inspect the
candidate and contracts against the annotations.

The configured numeric judges are:

- `semantic_quality`, scored from 1 to 5, with a configured minimum mean of
  3.5; and
- `review_discipline`, scored from 1 to 5, with a configured minimum mean of
  3.5.

All deterministic thresholds are `min_pass_rate: 1.0`.

## 6. Human Calibration Score Sheet

The source of truth for reviewer fields is
`calibration/v1/scores.yaml`. For every sampled case and every reviewer, fill
only these fields:

```yaml
semantic_quality: 1        # integer from 1 through 5
review_discipline: 1       # integer from 1 through 5
critical_failure: false    # boolean
false_ready: false         # boolean
notes: "short property-based observations"
```

Do not add per-category numeric score fields to `scores.yaml`. Template
selection, readiness, supporting requirements, consistency, controlled
language, host independence, and verification are evidence lenses used to
justify the two numeric scores and the two flags.

### `semantic_quality` rubric

This score summarizes intent preservation, EARS meaning, readiness, supporting
requirements, consistency analysis, and verification quality.

| Score | Interpretation |
| --- | --- |
| **5** | All applicable required findings and reference properties are satisfied; the EARS meaning is correct; contracts are complete; uncertainty is preserved; no disallowed behavior occurs; and no material omission remains. |
| **4** | The core behavior and readiness decision are correct. Only minor, non-blocking omissions, redundancy, or presentation issues remain. |
| **3** | The package is useful and substantially correct, but has one material, locally repairable omission or unclear contract field. It must not contain a false-ready decision. |
| **2** | Multiple material omissions, a major misclassification, implementation leakage, suppressed uncertainty, or an unsafe readiness decision is present. |
| **1** | The response changes intent, invents behavior or policy, claims unsupported approval, is unusable, or exhibits a critical disallowed behavior. |

Use these evidence lenses when choosing the score:

- correct EARS pattern, splitting, conditions, and one `shall` per candidate;
- preserved boundaries, actors, inputs, responses, quantities, timing,
  exclusions, terminology, and uncertainty;
- correct annotated readiness and meaningful contracts;
- grounded, relationship-labeled supporting requirements kept separate;
- conflicts, duplicates, gaps, overlaps, precedence, and intentional
  exceptions correctly analyzed; and
- independently observable verification setup, oracle, pass/fail rule,
  quantities, edge cases, instrumentation, and limits.

### `review_discipline` rubric

This score summarizes separation of certainty levels, host independence,
assumptions, suggestions, findings, contracts, and host-owned review status.

| Score | Interpretation |
| --- | --- |
| **5** | Understanding, questions, candidates, suggestions, assumptions, rationale, findings, contracts, and review status are clearly separated; uncertainty remains visible; host metadata is preserved; no approval, lifecycle, storage, or implementation workflow is invented. |
| **4** | The review package is disciplined and host-independent, with only a minor presentation, metadata, or certainty-separation omission. |
| **3** | One certainty level is mixed, one contract field is unclear, or host-specific context is handled ambiguously, but the package remains recoverable and does not claim approval. |
| **2** | The response suppresses uncertainty, mixes suggestions with candidates, invents workflow or approval, or introduces material host-specific behavior. |
| **1** | The response claims approval, silently resolves a conflict, prescribes implementation as a requirement, or omits the portable package. |

### Boolean fields and notes

- Set `false_ready: true` when a `needs_clarification` case contains a ready
  candidate or a ready claim hides an open material contract. A complete case
  marked `needs clarification` is over-gated, not false-ready.
- Set `critical_failure: true` for every false-ready result, regardless of
  numeric score. Also use it for another issue that must not pass regardless
  of average score, such as changed intent, invented approval or policy,
  normative implementation leakage, unauthorized conflict resolution, a
  missed direct conflict, or a failed critical structural gate.
- If `false_ready` is true, `critical_failure` must also be true.
- `notes` must contain short property-based observations tied to a candidate,
  question, contract, or finding. Do not paste the full package or require
  exact reference wording.

## 7. Calibration Workflow

Follow these steps in order. Reviewers may work separately; the
independence requirement applies to judgments, not to the final YAML file.

### Before either reviewer scores

1. A maintainer assigns two independent reviewers in
   `calibration/v1/sample.yaml` and `calibration/v1/artifact.yaml`.
2. In both files, replace `pending-maintainer-assignment` in each reviewer’s
   `identity`, set `assigned_at` to an ISO-8601 UTC timestamp, and set the
   sample reviewer `status` to `assigned`.
3. Keep the stable reviewer slot IDs `reviewer-a` and `reviewer-b` unless all
   calibration references are deliberately changed together.
4. Mirror the same identities and assignment timestamps in
   `calibration/v1/scores.yaml`. Do not score while either assignment is
   pending.

### Case Review Procedure

For each sampled case, each reviewer independently:

1. Reads `input.yaml`, `annotations.yaml`, `reference.md`, the generated
   package, the final response, and deterministic judge results.
2. Scores `semantic_quality` and `review_discipline` using the rubrics above.
3. Sets `false_ready`, then `critical_failure`; the latter must be true when
   the former is true.
4. Writes those five fields to the matching case row in that reviewer’s
   `scores.yaml` block. Do not add category score keys.
5. Writes concise property-based evidence in `notes`.

Reviewers must not compare or reconcile scores while working. After a reviewer
completes every sampled case, that reviewer sets their reviewer-level
`scored_at` in `scores.yaml` to an ISO-8601 UTC timestamp.

### After both reviewers finish

Do not merge two judgments into one average. If reviewers worked on separate
systems, merge **file structure only**: place the complete `reviewer-a` block
and complete `reviewer-b` block under `scores.yaml:reviewers`, preserving each
reviewer’s identity, timestamps, case rows, scores, flags, and notes. Do not
overwrite one block with the other or replace the two values with an average.

Agreement and adjudication are manual maintainer steps. The repository does
not provide a command that writes `agreement.yaml` or `adjudication.yaml`.
`check_calibration.py` validates those files and computes the expected
reviewer-agreement payload internally; it does not populate, merge, or edit
them.

1. **Populate `agreement.yaml` manually.** Set its `status` to `complete` only
   after both score sheets are complete. Fill `reviewer_reviewer` with the
   shared case IDs, percent agreement and Cohen’s κ for both numeric fields,
   and exact-match agreement plus per-case disagreements for effective
   `critical_failure` flags. Compare `false_ready` separately.

   The expected shape is:

   ```yaml
   status: complete
   reviewer_reviewer:
     cases: [case-001-ubiquitous, ...]
     semantic_quality:
       percent_agreement: 0.0
       cohens_kappa: 0.0
     review_discipline:
       percent_agreement: 0.0
       cohens_kappa: 0.0
     critical_failure:
       percent_agreement: 1.0
       disagreements:
         - id: case-008-vague-ui
           reviewer_a_critical: true
           reviewer_b_critical: false
   reviewer_deterministic_judges: <per-case comparison mapping>
   reviewer_semantic_judges: <per-case comparison mapping when available>
   ```

   Do not replace `reviewer_reviewer` with an average score. The checker
   recomputes the expected reviewer-agreement payload internally and rejects a
   mismatching payload. It does not calculate or write the YAML for you.
2. **Populate judge comparisons manually.** Fill
   `reviewer_deterministic_judges` from the per-case deterministic findings and
   `reviewer_semantic_judges` when per-case semantic scores exist. Never
   fabricate missing judge evidence.
3. **Validate the score sheet and agreement.** From the repository root, run:

   ```bash
   python3 eval/eliciting-requirements/scripts/check_calibration.py
   ```

   A non-zero result must be fixed before continuing.
4. **Adjudicate manually in `adjudication.yaml`.** Record notes for every
   critical-flag disagreement and material numeric disagreement. Classify each
   correction under `rubric_corrections`, `judge_corrections`,
   `skill_corrections`, or `corpus_corrections`; place confirmed new cases in
   `regression_additions`.

   Each adjudication note must identify the case and the decision. For example:

   ```yaml
   status: complete
   notes:
     - id: case-008-vague-ui
       issue: critical_flag_disagreement
       decision: critical_failure_true
       rationale: The clarification case contains a ready candidate.
   rubric_corrections: []
   judge_corrections: []
   skill_corrections: []
   corpus_corrections: []
   regression_additions: []
   held_out_additions: []
   ```

   A note is required for every critical-flag disagreement listed in
   `agreement.yaml`; a case ID must be present so the checker can match the
   note to the disagreement. Keep independent reviewer scores unchanged and
   record the adjudicated decision here.
5. Set `adjudication.yaml:status` to `complete`, then run the checker again:

   ```bash
   python3 eval/eliciting-requirements/scripts/check_calibration.py
   ```

6. Add confirmed calibration failures to `dataset/regression/` before changing
   the skill. Rerun development and regression evaluations after the change.
7. After all scores, agreement, adjudication, and judge comparisons are valid,
   set the calibration artifacts to `complete`. Keep `baselines/v1/` unchanged.
   Set `artifact.yaml:trusted` to `true` only after maintainer acceptance; do
   not set it merely because reviewers entered scores.

## 8. Completion Criteria

Calibration is complete only when:

- both reviewer identities and assignment timestamps are recorded in
  `sample.yaml`, `artifact.yaml`, and mirrored in `scores.yaml`;
- both reviewers scored every sampled case;
- both reviewer-level `scored_at` fields are set;
- `false_ready` implies `critical_failure` for every case;
- `agreement.yaml` matches the two independent score blocks;
- reviewer-to-judge comparisons are populated when judge evidence exists;
- every critical disagreement has an adjudication note;
- the checker exits successfully; and
- confirmed failures are routed to the regression corpus before a skill change
  or new trusted baseline.
