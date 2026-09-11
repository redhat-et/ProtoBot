---
type: skill-eval
skill: eliciting-requirements
analyzed_at: 2026-09-08
skill_hash: a855508aa19a
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
independent held-out corpus. Confirmed failures are added there before the
skill is changed. Issue #63 tracks curating and calibrating a truly held-out
sample. Live harness runs retain their case outputs, traces, costs, latency,
and human calibration artifacts under the run ID; a new baseline is a new
directory and never replaces an earlier one.
