# Calibration Protocol

This directory records independent human calibration for the
eliciting-requirements evaluation baseline in `baselines/v1`.

Issue #63 requires two independent reviewers to assess a documented
sample from both development and regression partitions against
property-based references, not exact wording.

## Status

The sample and reviewer slots are recorded. Reviewer identities,
scores, agreement, and adjudication are pending. The v1 baseline is
_not_ trusted.

## Before scoring

1. Maintainers assign two independent reviewers in `sample.yaml` and
   `artifact.yaml`.
2. Do not score until both identities and `assigned_at` values are
   recorded.
3. Score the generated package for each sampled case against that
   case's `reference.md`. Do not reward exact wording.

## Generated packages

Reviewers score the packages produced by the recorded harness runs,
not a re-authored gold file.

- Development run: `v2` (`eval.yaml`)
- Regression run: `v1-regression` (`eval-regression.yaml`)

Those artifacts live under the run paths in
`baselines/v1/manifest.yaml`. They are not committed here. If they
are unavailable, re-run the commands in that manifest, record the new
run ID in `artifact.yaml`, and score that run.

## Scoring

For each sampled case, set:

- `semantic_quality`: integer 1-5, same rubric as the semantic judge
- `review_discipline`: integer 1-5, same rubric as the review judge
- `false_ready`: true when a needs-clarification case is marked ready
  for review, or a ready claim hides a missing contract
- `critical_failure`: true for any false-ready result, and for any
  other case that must not pass regardless of average score
- `notes`: short property-based observations

A false-ready result is a critical failure regardless of aggregate
score. If `false_ready` is true, `critical_failure` must also be
true.

## After both reviewers score

1. Run
   `python3 eval/eliciting-requirements/scripts/check_calibration.py`.
2. Store reviewer-reviewer agreement in `agreement.yaml`. Compare
   critical-failure flags separately from the 1-5 scores.
3. Compare each reviewer with the deterministic judges and, when
   per-case semantic scores are available from the harness run, with
   the semantic judges.
4. Record adjudication notes in `adjudication.yaml`. Identify rubric,
   judge, skill, or corpus corrections.
5. Add confirmed failures to `dataset/held-out/` before changing the
   skill or promoting a new baseline.
6. Keep `baselines/v1/` unchanged as the prior snapshot. A later
   trusted baseline is a new directory.

Do not set `trusted: true` until steps 1-5 are complete.

## Sample coverage

The recorded sample covers all six EARS patterns, complex-pattern
splitting, false-ready rejection, both readiness contracts,
consistency analysis, implementation leakage, uncertainty, host
independence, and controlled-language findings.

| Case | Partition | Primary coverage |
| --- | --- | --- |
| case-001-ubiquitous | development | ubiquitous |
| case-002-event-driven | development | event-driven |
| case-003-state-driven | development | state-driven |
| case-004-optional-feature | development | optional feature |
| case-005-unwanted-behavior | development | unwanted behavior |
| case-006-complex-splitting | development | complex splitting |
| case-007-rough-api | development | leakage, false-ready |
| case-008-vague-ui | development | false-ready rejection |
| case-011-conflicting-set | development | consistency analysis |
| case-012-host-independent | development | host independence |
| case-023-ready-complete | development | both readiness contracts |
| case-014-uncertainty | regression | uncertainty |
| case-015-implementation-temptation | regression | leakage, false-ready |
| case-020-referent-and-vacuous | regression | controlled language |
| case-024-ready-regression | regression | both readiness contracts |
