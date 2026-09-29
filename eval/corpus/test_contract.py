"""Unit tests for the evaluation corpus contract validator."""

from __future__ import annotations

import copy
import unittest

import validate
import yaml

CLI_FIXTURE = validate.EXAMPLES_DIR / "cli-sync" / "fixture.yaml"


class FixtureContractTests(unittest.TestCase):
    def test_examples_validate(self) -> None:
        errors = validate.validate_tree(validate.CORPUS_ROOT)
        self.assertEqual(errors, [])

    def test_cli_api_web_archetypes_present(self) -> None:
        fixtures, _results = validate.discover_examples(validate.CORPUS_ROOT)
        archetypes = {
            fixture_id: validate._load_yaml(path)["archetype"]
            for fixture_id, path in fixtures.items()
        }
        self.assertEqual(archetypes["cli-sync"], "cli")
        self.assertEqual(archetypes["api-registry"], "network-service")
        self.assertEqual(archetypes["web-status"], "web-gui")

    def test_missing_golden_artifacts_fail(self) -> None:
        fixture = validate._load_yaml(CLI_FIXTURE)
        validator = validate._validator("fixture.schema.json")
        for field in (
            "vision",
            "architecture",
            "requirements",
            "interface_runtime",
        ):
            broken = copy.deepcopy(fixture)
            del broken["visible"][field]
            errors = validate._iter_errors(validator, broken)
            self.assertTrue(errors, field)

    def test_holdout_isolated_from_worker_and_elicitation(self) -> None:
        fixture = validate._load_yaml(CLI_FIXTURE)
        elicitation = validate.project_elicitation_input(fixture)
        job_site = validate.project_job_site_input(fixture)
        self.assertNotIn("holdout", elicitation)
        self.assertNotIn("visible", elicitation)
        self.assertNotIn("holdout", job_site)
        self.assertNotIn("project_description", job_site)
        worker_files = validate.worker_visible_files(CLI_FIXTURE.parent)
        self.assertFalse(
            any("holdout" in path.parts for path in worker_files),
            worker_files,
        )
        job_site_dump = yaml.safe_dump(job_site, sort_keys=True)
        self.assertNotIn("EVAL-HOLDOUT-", job_site_dump)
        self.assertNotIn("holdout", job_site_dump)
        elicitation_errors = validate._iter_errors(
            validate._validator("elicitation-input.schema.json"),
            elicitation,
        )
        job_site_errors = validate._iter_errors(
            validate._validator("job-site-input.schema.json"),
            job_site,
        )
        self.assertEqual(elicitation_errors, [])
        self.assertEqual(job_site_errors, [])
        leaked = copy.deepcopy(elicitation)
        leaked["holdout"] = fixture["holdout"]
        self.assertTrue(
            validate._iter_errors(
                validate._validator("elicitation-input.schema.json"),
                leaked,
            )
        )

    def test_result_stage_attribution(self) -> None:
        fixtures, results = validate.discover_examples(validate.CORPUS_ROOT)
        by_name = {path.name: path for path in results}
        req = validate._load_yaml(by_name["requirements-cli-sync.yaml"])
        job = validate._load_yaml(by_name["job-site-cli-sync.yaml"])
        e2e = validate._load_yaml(by_name["end-to-end-cli-sync.yaml"])
        self.assertEqual(set(req["scores"]), {"requirements"})
        self.assertEqual(set(job["scores"]), {"job_site"})
        self.assertEqual(set(e2e["scores"]), {"requirements", "job_site", "end_to_end"})
        mixed = copy.deepcopy(req)
        mixed["scores"]["job_site"] = job["scores"]["job_site"]
        errors = validate._iter_errors(validate._validator("result.schema.json"), mixed)
        self.assertTrue(any("job_site" in item for item in errors), errors)
        self.assertEqual(
            validate.validate_result(by_name["requirements-cli-sync.yaml"], fixtures),
            [],
        )
        self.assertEqual(
            validate.validate_result(by_name["job-site-cli-sync.yaml"], fixtures), []
        )
        self.assertEqual(
            validate.validate_result(by_name["end-to-end-cli-sync.yaml"], fixtures), []
        )

    def test_local_execution_needs_no_cluster_or_credentials(self) -> None:
        fixture = validate._load_yaml(CLI_FIXTURE)
        for runtime in fixture["visible"]["interface_runtime"]:
            self.assertFalse(runtime["hosted_cluster"])
            self.assertEqual(runtime["credentials"], "none")
        _fixtures, results = validate.discover_examples(validate.CORPUS_ROOT)
        for path in results:
            env = validate._load_yaml(path)["reproducibility"]["environment"]
            self.assertEqual(env["mode"], "local")
            self.assertFalse(env["hosted_cluster"])
            self.assertFalse(env["external_service_credentials"])

    def test_negative_cases_fail_deterministically(self) -> None:
        self.assertEqual(validate.run_negative_checks(), [])


if __name__ == "__main__":
    unittest.main()
