#!/usr/bin/env python3
"""Tests for check_calibration.py."""

from __future__ import annotations

import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any

import yaml

SCRIPTS = Path(__file__).resolve().parent
if str(SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SCRIPTS))

import check_calibration as cc

EVAL_ROOT = cc.EVAL_ROOT


def _dump(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(yaml.safe_dump(data, sort_keys=False), encoding="utf-8")


def _filled_score(
    case_id: str,
    semantic: int,
    discipline: int,
    *,
    false_ready: bool = False,
    critical: bool | None = None,
) -> dict[str, Any]:
    if critical is None:
        critical = false_ready
    return {
        "id": case_id,
        "semantic_quality": semantic,
        "review_discipline": discipline,
        "critical_failure": critical,
        "false_ready": false_ready,
        "notes": "",
    }


class EffectiveCriticalTests(unittest.TestCase):
    def test_false_ready_is_critical_regardless_of_score(self) -> None:
        score = _filled_score("case-x", 5, 5, false_ready=True, critical=False)
        self.assertTrue(cc.effective_critical(score))

    def test_explicit_critical_without_false_ready(self) -> None:
        score = _filled_score("case-x", 2, 2, false_ready=False, critical=True)
        self.assertTrue(cc.effective_critical(score))

    def test_high_score_without_false_ready_is_not_critical(self) -> None:
        score = _filled_score("case-x", 5, 5, false_ready=False, critical=False)
        self.assertFalse(cc.effective_critical(score))


class AgreementTests(unittest.TestCase):
    def test_perfect_agreement(self) -> None:
        scores = {
            "reviewers": [
                {
                    "id": "reviewer-a",
                    "cases": [
                        _filled_score("c1", 4, 5),
                        _filled_score("c2", 2, 2, false_ready=True),
                    ],
                },
                {
                    "id": "reviewer-b",
                    "cases": [
                        _filled_score("c1", 4, 5),
                        _filled_score("c2", 2, 2, false_ready=True),
                    ],
                },
            ]
        }
        result = cc.agreement_from_scores(scores)
        self.assertEqual(result["semantic_quality"]["percent_agreement"], 1.0)
        self.assertEqual(result["semantic_quality"]["cohens_kappa"], 1.0)
        self.assertEqual(result["critical_failure"]["percent_agreement"], 1.0)
        self.assertEqual(result["critical_failure"]["disagreements"], [])

    def test_false_ready_disagreement_is_reported(self) -> None:
        scores = {
            "reviewers": [
                {
                    "id": "reviewer-a",
                    "cases": [_filled_score("c1", 5, 5, false_ready=True)],
                },
                {
                    "id": "reviewer-b",
                    "cases": [_filled_score("c1", 5, 5, false_ready=False)],
                },
            ]
        }
        result = cc.agreement_from_scores(scores)
        self.assertEqual(result["semantic_quality"]["percent_agreement"], 1.0)
        self.assertEqual(result["critical_failure"]["percent_agreement"], 0.0)
        self.assertEqual(
            result["critical_failure"]["disagreements"],
            [
                {
                    "id": "c1",
                    "reviewer_a_critical": True,
                    "reviewer_b_critical": False,
                }
            ],
        )


class LiveArtifactTests(unittest.TestCase):
    def test_recorded_sample_passes(self) -> None:
        errors = cc.collect_errors(EVAL_ROOT)
        self.assertEqual(errors, [])

    def test_sample_covers_required_dimensions(self) -> None:
        sample = cc.load_yaml(cc.calibration_dir(EVAL_ROOT) / "sample.yaml")
        errors = cc.check_sample(EVAL_ROOT, sample)
        self.assertEqual(errors, [])

    def test_baseline_is_not_trusted(self) -> None:
        artifact = cc.load_yaml(cc.calibration_dir(EVAL_ROOT) / "artifact.yaml")
        self.assertIs(artifact.get("trusted"), False)
        results = cc.load_yaml(cc.baseline_dir(EVAL_ROOT) / "results.yaml")
        self.assertEqual(results["calibration"]["status"], "pending")


class TrustedGateTests(unittest.TestCase):
    def test_trusted_without_scores_fails(self) -> None:
        sample = {
            "cases": [{"id": "c1"}],
            "reviewers": [{"id": "reviewer-a"}, {"id": "reviewer-b"}],
        }
        artifact = {"trusted": True, "status": "pending-human-scoring"}
        scores = {
            "reviewers": [
                {"id": "reviewer-a", "identity": cc.PENDING_IDENTITY, "cases": []},
                {"id": "reviewer-b", "identity": cc.PENDING_IDENTITY, "cases": []},
            ]
        }
        errors = cc.check_trusted_gate(
            artifact,
            sample,
            scores,
            {"status": "pending-scores"},
            {"status": "pending-scores"},
            {"calibration": {"status": "pending"}},
            {"status": "live-evaluation-snapshot", "run": {}},
        )
        self.assertTrue(any("trusted: true" in error for error in errors))


class SampleMutationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmpdir = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmpdir, ignore_errors=True)
        shutil.copytree(EVAL_ROOT / "dataset", self.tmpdir / "dataset")
        shutil.copytree(EVAL_ROOT / "calibration", self.tmpdir / "calibration")
        shutil.copytree(EVAL_ROOT / "baselines", self.tmpdir / "baselines")

    def test_dropping_ubiquitous_fails_coverage(self) -> None:
        sample_path = cc.calibration_dir(self.tmpdir) / "sample.yaml"
        sample = cc.load_yaml(sample_path)
        sample["cases"] = [
            case for case in sample["cases"] if case["id"] != "case-001-ubiquitous"
        ]
        _dump(sample_path, sample)
        scores = cc.load_yaml(cc.calibration_dir(self.tmpdir) / "scores.yaml")
        scores["reviewers"][0]["cases"] = [
            row
            for row in scores["reviewers"][0]["cases"]
            if row["id"] != "case-001-ubiquitous"
        ]
        scores["reviewers"][1]["cases"] = [
            row
            for row in scores["reviewers"][1]["cases"]
            if row["id"] != "case-001-ubiquitous"
        ]
        _dump(cc.calibration_dir(self.tmpdir) / "scores.yaml", scores)
        errors = cc.collect_errors(self.tmpdir)
        self.assertTrue(
            any("ears-pattern:ubiquitous" in error for error in errors),
            errors,
        )

    def test_score_sheet_false_ready_must_be_critical(self) -> None:
        scores_path = cc.calibration_dir(self.tmpdir) / "scores.yaml"
        scores = cc.load_yaml(scores_path)
        scores["reviewers"][0]["cases"][0]["false_ready"] = True
        scores["reviewers"][0]["cases"][0]["critical_failure"] = False
        _dump(scores_path, scores)
        errors = cc.collect_errors(self.tmpdir)
        self.assertTrue(
            any("false_ready without critical_failure" in error for error in errors),
            errors,
        )


if __name__ == "__main__":
    unittest.main()
