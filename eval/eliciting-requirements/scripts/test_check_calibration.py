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

    def test_scores_complete_rejects_bool_and_out_of_range(self) -> None:
        case_ids = ["c1"]

        def _make_scores(
            sem: Any, disc: Any, id_a: str = "rev-a", id_b: str = "rev-b"
        ) -> dict[str, Any]:
            return {
                "reviewers": [
                    {
                        "id": "reviewer-a",
                        "identity": id_a,
                        "cases": [_filled_score("c1", sem, disc)],
                    },
                    {
                        "id": "reviewer-b",
                        "identity": id_b,
                        "cases": [_filled_score("c1", sem, disc)],
                    },
                ]
            }

        # Valid scores 1-5 with distinct identities pass
        self.assertTrue(cc.scores_complete(_make_scores(3, 4), case_ids))

        # Bool values rejected
        self.assertFalse(cc.scores_complete(_make_scores(True, 4), case_ids))
        self.assertFalse(cc.scores_complete(_make_scores(3, False), case_ids))

        # Out-of-range integer scores rejected
        self.assertFalse(cc.scores_complete(_make_scores(0, 3), case_ids))
        self.assertFalse(cc.scores_complete(_make_scores(6, 3), case_ids))
        self.assertFalse(cc.scores_complete(_make_scores(3, 0), case_ids))
        self.assertFalse(cc.scores_complete(_make_scores(3, 99), case_ids))

        # Identical reviewer identities rejected
        self.assertFalse(
            cc.scores_complete(_make_scores(3, 3, id_a="same", id_b="same"), case_ids)
        )

    def test_trusted_with_complete_status_but_null_agreement_fails(self) -> None:
        sample = {
            "cases": [{"id": "c1"}],
            "reviewers": [{"id": "reviewer-a"}, {"id": "reviewer-b"}],
        }
        artifact = {"trusted": True, "status": "complete"}
        scores = {
            "reviewers": [
                {
                    "id": "reviewer-a",
                    "identity": "reviewer-1",
                    "cases": [_filled_score("c1", 4, 4)],
                },
                {
                    "id": "reviewer-b",
                    "identity": "reviewer-2",
                    "cases": [_filled_score("c1", 4, 4)],
                },
            ]
        }
        errors = cc.check_trusted_gate(
            artifact,
            sample,
            scores,
            {"status": "complete", "reviewer_reviewer": None},
            {"status": "complete"},
            {"calibration": {"status": "complete"}},
            {"status": "trusted", "run": {"human_calibration": "trusted"}},
        )
        self.assertTrue(any("trusted: true" in error for error in errors))
        self.assertTrue(
            any(
                "agreement.yaml must record non-null reviewer_reviewer" in error
                for error in errors
            )
        )

    def test_trusted_with_disagreeing_agreement_payload_fails(self) -> None:
        sample = {
            "cases": [{"id": "c1"}],
            "reviewers": [{"id": "reviewer-a"}, {"id": "reviewer-b"}],
        }
        artifact = {"trusted": True, "status": "complete"}
        scores = {
            "reviewers": [
                {
                    "id": "reviewer-a",
                    "identity": "reviewer-1",
                    "cases": [_filled_score("c1", 4, 4)],
                },
                {
                    "id": "reviewer-b",
                    "identity": "reviewer-2",
                    "cases": [_filled_score("c1", 4, 4)],
                },
            ]
        }
        bogus_agreement = {
            "status": "complete",
            "reviewer_reviewer": {
                "cases": ["c1"],
                "semantic_quality": {"percent_agreement": 0.0, "cohens_kappa": 0.0},
                "review_discipline": {"percent_agreement": 0.0, "cohens_kappa": 0.0},
                "critical_failure": {"percent_agreement": 0.0, "disagreements": []},
            },
        }
        errors = cc.check_trusted_gate(
            artifact,
            sample,
            scores,
            bogus_agreement,
            {"status": "complete"},
            {"calibration": {"status": "complete"}},
            {"status": "trusted", "run": {"human_calibration": "trusted"}},
        )
        self.assertTrue(
            any("does not match computed agreement" in error for error in errors)
        )

    def test_human_calibration_not_trusted_does_not_error(self) -> None:
        sample = {
            "cases": [{"id": "c1"}],
            "reviewers": [{"id": "reviewer-a"}, {"id": "reviewer-b"}],
        }
        artifact = {"trusted": False, "status": "pending-human-scoring"}
        scores = {
            "reviewers": [
                {"id": "reviewer-a", "identity": cc.PENDING_IDENTITY, "cases": []},
                {"id": "reviewer-b", "identity": cc.PENDING_IDENTITY, "cases": []},
            ]
        }
        for value in (
            "not trusted",
            "not trusted; sample recorded in calibration/v1/",
            "untrusted",
        ):
            errors = cc.check_trusted_gate(
                artifact,
                sample,
                scores,
                {"status": "pending-scores"},
                {"status": "pending-scores"},
                {"calibration": {"status": "pending"}},
                {
                    "status": "live-evaluation-snapshot",
                    "run": {"human_calibration": value},
                },
            )
            self.assertEqual(errors, [])

        errors_trusted = cc.check_trusted_gate(
            artifact,
            sample,
            scores,
            {"status": "pending-scores"},
            {"status": "pending-scores"},
            {"calibration": {"status": "pending"}},
            {
                "status": "live-evaluation-snapshot",
                "run": {"human_calibration": "trusted"},
            },
        )
        self.assertTrue(
            any(
                "claims trusted calibration too early" in error
                for error in errors_trusted
            )
        )


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

    def test_collect_errors_invokes_agreement_computation_on_filled_sheet(self) -> None:
        sample = cc.load_yaml(cc.calibration_dir(self.tmpdir) / "sample.yaml")
        case_ids = [case["id"] for case in sample["cases"]]
        filled_scores = {
            "reviewers": [
                {
                    "id": "reviewer-a",
                    "identity": "reviewer-1",
                    "cases": [_filled_score(cid, 4, 4) for cid in case_ids],
                },
                {
                    "id": "reviewer-b",
                    "identity": "reviewer-2",
                    "cases": [_filled_score(cid, 4, 4) for cid in case_ids],
                },
            ]
        }
        _dump(cc.calibration_dir(self.tmpdir) / "scores.yaml", filled_scores)
        errors = cc.collect_errors(self.tmpdir)
        self.assertTrue(
            any(
                "agreement.yaml must record non-null reviewer_reviewer" in e
                for e in errors
            ),
            errors,
        )

        agreement_path = cc.calibration_dir(self.tmpdir) / "agreement.yaml"
        agreement = cc.load_yaml(agreement_path)
        agreement["reviewer_reviewer"] = cc.agreement_from_scores(filled_scores)
        _dump(agreement_path, agreement)
        errors = cc.collect_errors(self.tmpdir)
        self.assertEqual(errors, [])

    def test_missing_baseline_files_reports_error_without_crash(self) -> None:
        manifest_path = cc.baseline_dir(self.tmpdir) / "manifest.yaml"
        manifest_path.unlink()
        errors = cc.collect_errors(self.tmpdir)
        self.assertTrue(
            any("baselines/v1/manifest.yaml is missing" in e for e in errors),
            errors,
        )


if __name__ == "__main__":
    unittest.main()
