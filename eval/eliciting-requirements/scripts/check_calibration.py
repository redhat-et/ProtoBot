#!/usr/bin/env python3
"""Validate eliciting-requirements calibration artifacts for issue #63.

The v1 baseline is not trusted until two independent reviewers score the
recorded sample, agreement is computed, and adjudication is complete.
This checker enforces that gate and the sample-coverage contract.
"""

from __future__ import annotations

import argparse
import sys
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

try:
    import yaml
except ImportError:
    sys.exit(
        "ERROR: PyYAML is required but not installed.\n"
        "       Install with:  pip install pyyaml"
    )

EVAL_ROOT = Path(__file__).resolve().parent.parent
CALIBRATION_ID = "v1"

EARS_PATTERNS = (
    "ubiquitous",
    "event-driven",
    "state-driven",
    "optional feature",
    "unwanted behavior",
    "complex",
)

REQUIRED_DIMENSIONS = (
    "complex-pattern-splitting",
    "false-ready-rejection",
    "readiness-contract:implementability",
    "readiness-contract:verification",
    "consistency-analysis",
    "implementation-leakage",
    "uncertainty",
    "host-independence",
    "controlled-language",
)

REQUIRED_PARTITIONS = ("development", "regression")
REQUIRED_READINESS = ("needs_clarification", "ready_for_review")
PENDING_IDENTITY = "pending-maintainer-assignment"


def load_yaml(path: Path) -> Any:
    """Load a YAML mapping or list from *path*."""
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def calibration_dir(root: Path) -> Path:
    return root / "calibration" / CALIBRATION_ID


def baseline_dir(root: Path) -> Path:
    return root / "baselines" / CALIBRATION_ID


def _as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, list):
        return value
    return [value]


def _lower_join(items: Iterable[Any]) -> str:
    return " ".join(str(item).lower() for item in items)


def derived_covers(case_id: str, annotations: Mapping[str, Any]) -> set[str]:
    """Return coverage tags justified by a case's annotations."""
    covers: set[str] = set()
    templates = [str(item) for item in _as_list(annotations.get("expected_templates"))]
    for group in _as_list(annotations.get("template_alternatives")):
        templates.extend(str(item) for item in _as_list(group))
    for template in templates:
        if template and template != "unresolved":
            covers.add(f"ears-pattern:{template}")

    category = str(annotations.get("category", ""))
    category_map = {
        "consistency-analysis": "consistency-analysis",
        "elicitation-and-leakage": "implementation-leakage",
        "uncertainty-preservation": "uncertainty",
        "host-independence": "host-independence",
        "controlled-language": "controlled-language",
    }
    if category in category_map:
        covers.add(category_map[category])

    findings = _lower_join(_as_list(annotations.get("required_findings")))
    disallowed = _lower_join(_as_list(annotations.get("disallowed_behaviors")))
    if "complex" in templates and ("split" in case_id or "split" in findings):
        covers.add("complex-pattern-splitting")
    if (
        "false readiness" in findings
        or "false-ready" in findings
        or ("ready" in disallowed and "mark" in disallowed)
        or "present the claim as ready" in disallowed
        or "claim ready" in disallowed
    ):
        covers.add("false-ready-rejection")

    readiness = str(annotations.get("readiness", ""))
    if readiness:
        covers.add(f"readiness:{readiness}")
    if readiness in {"ready_for_review", "mixed"}:
        covers.add("readiness-contract:implementability")
        covers.add("readiness-contract:verification")
    return covers


def effective_critical(score: Mapping[str, Any]) -> bool:
    """A false-ready result is a critical failure regardless of score."""
    if score.get("false_ready") is True:
        return True
    return score.get("critical_failure") is True


def _reviewer_block(scores: Mapping[str, Any], reviewer_id: str) -> Mapping[str, Any]:
    for reviewer in _as_list(scores.get("reviewers")):
        if reviewer.get("id") == reviewer_id:
            return reviewer
    return {}


def scores_complete(scores: Mapping[str, Any], case_ids: Iterable[str]) -> bool:
    """Return True when both reviewers have a full per-case score sheet."""
    wanted = list(case_ids)
    identities: list[str] = []
    for reviewer_id in ("reviewer-a", "reviewer-b"):
        block = _reviewer_block(scores, reviewer_id)
        identity = str(block.get("identity", PENDING_IDENTITY)).strip()
        if not identity or identity == PENDING_IDENTITY:
            return False
        identities.append(identity)
        by_id = {item.get("id"): item for item in _as_list(block.get("cases"))}
        for case_id in wanted:
            row = by_id.get(case_id)
            if not isinstance(row, Mapping):
                return False
            sem = row.get("semantic_quality")
            if (
                isinstance(sem, bool)
                or not isinstance(sem, int)
                or sem not in range(1, 6)
            ):
                return False
            disc = row.get("review_discipline")
            if (
                isinstance(disc, bool)
                or not isinstance(disc, int)
                or disc not in range(1, 6)
            ):
                return False
            if not isinstance(row.get("critical_failure"), bool):
                return False
            if not isinstance(row.get("false_ready"), bool):
                return False
            if (
                row.get("false_ready") is True
                and row.get("critical_failure") is not True
            ):
                return False
    return len(set(identities)) >= 2


def _payloads_match(actual: Any, expected: Any) -> bool:
    """Recursively compare agreement payloads allowing floating-point tolerance."""
    if isinstance(actual, Mapping) and isinstance(expected, Mapping):
        if set(actual.keys()) != set(expected.keys()):
            return False
        return all(_payloads_match(actual[k], expected[k]) for k in actual)
    if isinstance(actual, list) and isinstance(expected, list):
        if len(actual) != len(expected):
            return False
        return all(_payloads_match(a, b) for a, b in zip(actual, expected, strict=True))
    if (
        isinstance(actual, (int, float))
        and isinstance(expected, (int, float))
        and not isinstance(actual, bool)
        and not isinstance(expected, bool)
    ):
        return abs(float(actual) - float(expected)) < 1e-3
    return actual == expected


def check_agreement_payload(
    scores: Mapping[str, Any], agreement: Mapping[str, Any]
) -> list[str]:
    """Validate agreement.yaml reviewer_reviewer payload against scores."""
    errors: list[str] = []
    rev_rev = agreement.get("reviewer_reviewer")
    if rev_rev is None:
        errors.append(
            "agreement.yaml must record non-null reviewer_reviewer when scores are complete"
        )
        return errors
    if not isinstance(rev_rev, Mapping):
        errors.append("agreement.yaml reviewer_reviewer must be a mapping")
        return errors
    crit = rev_rev.get("critical_failure")
    if not isinstance(crit, Mapping) or "disagreements" not in crit:
        errors.append(
            "agreement.yaml reviewer_reviewer must include populated critical_failure comparison"
        )
    try:
        computed = agreement_from_scores(scores)
    except (ValueError, KeyError, TypeError) as exc:
        errors.append(f"failed to compute agreement from scores: {exc}")
        return errors
    if not _payloads_match(rev_rev, computed):
        errors.append(
            "agreement.yaml reviewer_reviewer does not match computed agreement from scores"
        )
    return errors


def calibration_complete(
    sample: Mapping[str, Any],
    artifact: Mapping[str, Any],
    scores: Mapping[str, Any],
    agreement: Mapping[str, Any],
    adjudication: Mapping[str, Any],
) -> bool:
    """Return True when human scoring and adjudication have finished."""
    if artifact.get("status") != "complete":
        return False
    if agreement.get("status") != "complete":
        return False
    if adjudication.get("status") != "complete":
        return False
    case_ids = [case["id"] for case in _as_list(sample.get("cases"))]
    if not scores_complete(scores, case_ids):
        return False
    return not check_agreement_payload(scores, agreement)


def cohens_kappa(left: list[int], right: list[int]) -> float:
    """Return Cohen's kappa for two equal-length integer rating lists."""
    if len(left) != len(right) or not left:
        raise ValueError("Cohen's kappa requires two non-empty equal-length lists")
    size = len(left)
    observed = sum(a == b for a, b in zip(left, right, strict=True)) / size
    categories = sorted(set(left) | set(right))
    expected = 0.0
    for category in categories:
        expected += (left.count(category) / size) * (right.count(category) / size)
    if expected == 1.0:
        return 1.0 if observed == 1.0 else 0.0
    return (observed - expected) / (1.0 - expected)


def percent_agreement(left: list[Any], right: list[Any]) -> float:
    if len(left) != len(right) or not left:
        raise ValueError("percent agreement requires two non-empty equal-length lists")
    return sum(a == b for a, b in zip(left, right, strict=True)) / len(left)


def agreement_from_scores(scores: Mapping[str, Any]) -> dict[str, Any]:
    """Compute reviewer-reviewer agreement, treating false-ready as critical."""
    reviewer_a = _reviewer_block(scores, "reviewer-a")
    reviewer_b = _reviewer_block(scores, "reviewer-b")
    cases_a = {item["id"]: item for item in _as_list(reviewer_a.get("cases"))}
    cases_b = {item["id"]: item for item in _as_list(reviewer_b.get("cases"))}
    shared = sorted(set(cases_a) & set(cases_b))
    if not shared:
        raise ValueError("No shared scored cases")
    semantic_a = [cases_a[cid]["semantic_quality"] for cid in shared]
    semantic_b = [cases_b[cid]["semantic_quality"] for cid in shared]
    discipline_a = [cases_a[cid]["review_discipline"] for cid in shared]
    discipline_b = [cases_b[cid]["review_discipline"] for cid in shared]
    critical_a = [effective_critical(cases_a[cid]) for cid in shared]
    critical_b = [effective_critical(cases_b[cid]) for cid in shared]
    disagreements = [
        {
            "id": cid,
            "reviewer_a_critical": effective_critical(cases_a[cid]),
            "reviewer_b_critical": effective_critical(cases_b[cid]),
        }
        for cid in shared
        if effective_critical(cases_a[cid]) != effective_critical(cases_b[cid])
    ]
    return {
        "cases": shared,
        "semantic_quality": {
            "percent_agreement": percent_agreement(semantic_a, semantic_b),
            "cohens_kappa": cohens_kappa(semantic_a, semantic_b),
        },
        "review_discipline": {
            "percent_agreement": percent_agreement(discipline_a, discipline_b),
            "cohens_kappa": cohens_kappa(discipline_a, discipline_b),
        },
        "critical_failure": {
            "percent_agreement": percent_agreement(critical_a, critical_b),
            "disagreements": disagreements,
        },
    }


def check_sample(root: Path, sample: Mapping[str, Any]) -> list[str]:
    """Return errors if the recorded sample does not meet issue #63 coverage."""
    errors: list[str] = []
    if sample.get("selected_before_scoring") is not True:
        errors.append("sample.yaml must set selected_before_scoring: true")
    reviewers = _as_list(sample.get("reviewers"))
    reviewer_ids = [item.get("id") for item in reviewers]
    if reviewer_ids != ["reviewer-a", "reviewer-b"]:
        errors.append(
            "sample.yaml must record reviewer-a and reviewer-b before scoring"
        )

    cases = _as_list(sample.get("cases"))
    if len(cases) < 2:
        errors.append("sample.yaml must list at least two cases")

    observed: set[str] = set()
    partitions: set[str] = set()
    readiness: set[str] = set()
    seen_ids: set[str] = set()
    for case in cases:
        case_id = str(case.get("id", ""))
        rel_path = str(case.get("path", ""))
        if not case_id or not rel_path:
            errors.append("each sample case needs id and path")
            continue
        if case_id in seen_ids:
            errors.append(f"duplicate sample case: {case_id}")
        seen_ids.add(case_id)
        case_dir = root / rel_path
        for name in ("input.yaml", "annotations.yaml", "reference.md"):
            if not (case_dir / name).is_file():
                errors.append(f"{case_id} is missing {name}")
        annotations_path = case_dir / "annotations.yaml"
        if not annotations_path.is_file():
            continue
        annotations = load_yaml(annotations_path)
        if not isinstance(annotations, Mapping):
            errors.append(f"{case_id} annotations.yaml is not a mapping")
            continue
        partition = str(annotations.get("partition", ""))
        partitions.add(partition)
        declared_partition = str(case.get("partition", ""))
        if declared_partition and declared_partition != partition:
            errors.append(
                f"{case_id} partition {declared_partition!r} does not match "
                f"annotations {partition!r}"
            )
        readiness.add(str(annotations.get("readiness", "")))
        justified = derived_covers(case_id, annotations)
        if case.get("critical_false_ready") is True:
            justified.add("false-ready-rejection")
        declared = {str(item) for item in _as_list(case.get("covers"))}
        unexplained = declared - justified
        if unexplained:
            errors.append(
                f"{case_id} declares unjustified coverage: "
                + ", ".join(sorted(unexplained))
            )
        observed.update(declared)
        observed.update(justified)

    for pattern in EARS_PATTERNS:
        tag = f"ears-pattern:{pattern}"
        if tag not in observed:
            errors.append(f"sample is missing {tag}")
    for dimension in REQUIRED_DIMENSIONS:
        if dimension not in observed:
            errors.append(f"sample is missing {dimension}")
    for partition in REQUIRED_PARTITIONS:
        if partition not in partitions:
            errors.append(f"sample is missing partition {partition}")
    for value in REQUIRED_READINESS:
        if value not in readiness:
            errors.append(f"sample is missing readiness {value}")
    return errors


def check_trusted_gate(
    artifact: Mapping[str, Any],
    sample: Mapping[str, Any],
    scores: Mapping[str, Any],
    agreement: Mapping[str, Any],
    adjudication: Mapping[str, Any],
    results: Mapping[str, Any],
    manifest: Mapping[str, Any],
) -> list[str]:
    """Forbid marking the baseline trusted before calibration completes."""
    errors: list[str] = []
    complete = calibration_complete(sample, artifact, scores, agreement, adjudication)
    if artifact.get("trusted") is True and not complete:
        errors.append(
            "artifact.yaml sets trusted: true before scoring and adjudication complete"
        )
        case_ids = [case["id"] for case in _as_list(sample.get("cases"))]
        if scores_complete(scores, case_ids):
            errors.extend(check_agreement_payload(scores, agreement))
    calibration = results.get("calibration")
    if isinstance(calibration, Mapping):
        status = str(calibration.get("status", "pending"))
        if status not in {"pending", "complete"}:
            errors.append(f"unexpected results calibration status: {status}")
        if status == "complete" and not complete:
            errors.append(
                "results.yaml marks calibration complete before scoring finishes"
            )
    human = str(manifest.get("run", {}).get("human_calibration", "")).lower()
    if "trusted" in human and not complete:
        errors.append("baselines/v1 manifest claims trusted calibration too early")
    if manifest.get("status") == "trusted" and not complete:
        errors.append("baselines/v1 status is trusted before calibration completes")
    return errors


def check_baseline_retained(root: Path) -> list[str]:
    """The prior baseline and per-case evidence pointers must remain."""
    errors: list[str] = []
    manifest_path = baseline_dir(root) / "manifest.yaml"
    results_path = baseline_dir(root) / "results.yaml"
    if not manifest_path.is_file():
        errors.append("baselines/v1/manifest.yaml is missing")
    if not results_path.is_file():
        errors.append("baselines/v1/results.yaml is missing")
    return errors


def check_score_sheet(
    sample: Mapping[str, Any], scores: Mapping[str, Any]
) -> list[str]:
    """Score sheets must list every sampled case for both reviewers."""
    errors: list[str] = []
    sample_ids = [case["id"] for case in _as_list(sample.get("cases"))]
    reviewers = _as_list(scores.get("reviewers"))
    reviewer_ids = [item.get("id") for item in reviewers]
    if reviewer_ids != ["reviewer-a", "reviewer-b"]:
        errors.append("scores.yaml must contain reviewer-a and reviewer-b")
        return errors
    if len(reviewers) == 2:
        id_a = str(reviewers[0].get("identity", "")).strip()
        id_b = str(reviewers[1].get("identity", "")).strip()
        if (
            id_a
            and id_b
            and id_a != PENDING_IDENTITY
            and id_b != PENDING_IDENTITY
            and id_a == id_b
        ):
            errors.append("reviewer-a and reviewer-b must have distinct identities")
    for reviewer in reviewers:
        reviewer_id = reviewer.get("id")
        case_ids = [item.get("id") for item in _as_list(reviewer.get("cases"))]
        if case_ids != sample_ids:
            errors.append(f"{reviewer_id} cases do not match the recorded sample order")
        for item in _as_list(reviewer.get("cases")):
            false_ready = item.get("false_ready")
            critical = item.get("critical_failure")
            if false_ready is True and critical is False:
                errors.append(
                    f"{reviewer_id} {item.get('id')} sets false_ready without "
                    "critical_failure"
                )
    return errors


def collect_errors(root: Path) -> list[str]:
    """Run every calibration check against files under *root*."""
    errors: list[str] = []
    cal_dir = calibration_dir(root)
    required_files = {
        "sample.yaml": cal_dir / "sample.yaml",
        "artifact.yaml": cal_dir / "artifact.yaml",
        "scores.yaml": cal_dir / "scores.yaml",
        "agreement.yaml": cal_dir / "agreement.yaml",
        "adjudication.yaml": cal_dir / "adjudication.yaml",
        "protocol.md": cal_dir / "protocol.md",
    }
    for label, path in required_files.items():
        if not path.is_file():
            errors.append(f"missing {label}")
    baseline_errors = check_baseline_retained(root)
    errors.extend(baseline_errors)
    if errors:
        return errors

    sample = load_yaml(required_files["sample.yaml"])
    artifact = load_yaml(required_files["artifact.yaml"])
    scores = load_yaml(required_files["scores.yaml"])
    agreement = load_yaml(required_files["agreement.yaml"])
    adjudication = load_yaml(required_files["adjudication.yaml"])
    manifest = load_yaml(baseline_dir(root) / "manifest.yaml")
    results = load_yaml(baseline_dir(root) / "results.yaml")

    errors.extend(check_sample(root, sample))
    errors.extend(check_score_sheet(sample, scores))
    case_ids = [case["id"] for case in _as_list(sample.get("cases"))]
    if scores_complete(scores, case_ids):
        errors.extend(check_agreement_payload(scores, agreement))
    errors.extend(
        check_trusted_gate(
            artifact, sample, scores, agreement, adjudication, results, manifest
        )
    )
    held_out = root / "dataset" / "held-out"
    if not (held_out / "README.md").is_file():
        errors.append("dataset/held-out/README.md is missing")
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        type=Path,
        default=EVAL_ROOT,
        help="eliciting-requirements eval directory",
    )
    args = parser.parse_args(argv)
    errors = collect_errors(args.root)
    if errors:
        print("calibration check failed:")
        for error in errors:
            print(f"  - {error}")
        return 1
    print("calibration check passed: sample recorded, baseline not trusted")
    return 0


if __name__ == "__main__":
    sys.exit(main())
