"""Unit tests for the evaluation corpus contract validator."""

from __future__ import annotations

import copy
import shutil
import tempfile
import unittest
from pathlib import Path

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

    def test_result_invariants_and_identities_enforced(self) -> None:
        fixtures, results = validate.discover_examples(validate.CORPUS_ROOT)
        by_name = {path.name: path for path in results}
        job_base = validate._load_yaml(by_name["job-site-cli-sync.yaml"])
        req_base = validate._load_yaml(by_name["requirements-cli-sync.yaml"])

        # 1. job_site.pass cannot be true with holdout failures
        broken_holdout = copy.deepcopy(job_base)
        broken_holdout["scores"]["job_site"]["holdout_checks"].update(
            passed=0, failed=1, total=1
        )
        broken_holdout["scores"]["job_site"]["pass"] = True
        errs = validate._check_result_semantics(broken_holdout, fixtures)
        self.assertIn(
            "job_site.pass cannot be true when holdout_checks has failures",
            errs,
        )

        # 2. Stage pass cannot be true when critical_failures are present
        broken_crit = copy.deepcopy(req_base)
        broken_crit["critical_failures"] = [
            {
                "category": "execution-failure",
                "detail": "critical error",
                "evidence": "failure logged",
            }
        ]
        self.assertEqual(
            validate._iter_errors(
                validate._validator("result.schema.json"), broken_crit
            ),
            [],
        )
        errs = validate._check_result_semantics(broken_crit, fixtures)
        self.assertIn(
            "requirements.pass cannot be true when critical_failures is non-empty",
            errs,
        )

        # 3. Check counts must satisfy passed + failed == total
        broken_counts = copy.deepcopy(job_base)
        broken_counts["scores"]["job_site"]["visible_checks"]["passed"] = 5
        errs = validate._check_result_semantics(broken_counts, fixtures)
        self.assertTrue(any("check counts" in e for e in errs), errs)

        # 4. Result fixture_id and corpus_revision must match inputs
        split_fid = copy.deepcopy(req_base)
        split_fid["reproducibility"]["inputs"]["fixture_id"] = "api-registry"
        errs = validate._check_result_semantics(split_fid, fixtures)
        self.assertTrue(any("fixture_id" in e for e in errs), errs)

        split_rev = copy.deepcopy(req_base)
        split_rev["reproducibility"]["inputs"]["corpus_revision"] = "v2"
        errs = validate._check_result_semantics(split_rev, fixtures)
        self.assertTrue(any("corpus_revision" in e for e in errs), errs)

        # 5. inputs.fixture_id must follow the hyphenated id pattern
        invalid_fid_schema = copy.deepcopy(req_base)
        invalid_fid_schema["reproducibility"]["inputs"]["fixture_id"] = "Invalid_ID!"
        schema_errs = validate._iter_errors(
            validate._validator("result.schema.json"), invalid_fid_schema
        )
        self.assertTrue(any("fixture_id" in e for e in schema_errs), schema_errs)

    def test_interface_runtime_bindings_and_type_matching(self) -> None:
        fixture = validate._load_yaml(CLI_FIXTURE)

        # Missing runtime binding for an interface
        unbound = copy.deepcopy(fixture)
        unbound["visible"]["architecture"]["external_interfaces"].append(
            {"id": "extra-api", "type": "network-service", "description": "extra"}
        )
        errs = validate._check_refs(unbound, CLI_FIXTURE.parent)
        self.assertTrue(any("missing binding" in e for e in errs), errs)

        # Duplicate interface_runtime entries
        dup_runtime = copy.deepcopy(fixture)
        dup_runtime["visible"]["interface_runtime"].append(
            copy.deepcopy(dup_runtime["visible"]["interface_runtime"][0])
        )
        errs = validate._check_refs(dup_runtime, CLI_FIXTURE.parent)
        self.assertTrue(any("unique" in e for e in errs), errs)

        # Runtime type mismatch with architecture interface type
        mismatched_type = copy.deepcopy(fixture)
        mismatched_type["visible"]["interface_runtime"][0]["type"] = "network-service"
        errs = validate._check_refs(mismatched_type, CLI_FIXTURE.parent)
        self.assertTrue(any("does not match" in e for e in errs), errs)

    def test_job_site_input_schema_strictness(self) -> None:
        fixture = validate._load_yaml(CLI_FIXTURE)
        job_site = validate.project_job_site_input(fixture)
        validator = validate._validator("job-site-input.schema.json")

        self.assertEqual(validate._iter_errors(validator, job_site), [])

        # Empty vision must fail schema validation
        empty_vision = copy.deepcopy(job_site)
        empty_vision["visible"]["vision"] = {}
        self.assertTrue(validate._iter_errors(validator, empty_vision))

        # Empty architecture must fail schema validation
        empty_arch = copy.deepcopy(job_site)
        empty_arch["visible"]["architecture"] = {}
        self.assertTrue(validate._iter_errors(validator, empty_arch))

        # Unconstrained requirement must fail schema validation
        empty_req = copy.deepcopy(job_site)
        empty_req["visible"]["requirements"] = [{}]
        self.assertTrue(validate._iter_errors(validator, empty_req))

    def test_holdout_symlink_and_containment_enforced(self) -> None:
        fixture = validate._load_yaml(CLI_FIXTURE)
        with tempfile.TemporaryDirectory() as tmp_dir:
            tmp_fixture_dir = Path(tmp_dir) / "cli-sync"
            shutil.copytree(CLI_FIXTURE.parent, tmp_fixture_dir)
            outside_file = Path(tmp_dir) / "secret.yaml"
            outside_file.write_text("secret: true\n", encoding="utf-8")

            # Symlink pointing outside fixture tree is rejected
            symlink_outside = tmp_fixture_dir / "holdout" / "outside.yaml"
            symlink_outside.symlink_to(outside_file)
            broken_symlink = copy.deepcopy(fixture)
            broken_symlink["holdout"]["assets"][0]["path"] = "holdout/outside.yaml"
            errs = validate._check_refs(broken_symlink, tmp_fixture_dir)
            self.assertTrue(any("path is a symlink" in e for e in errs), errs)
            self.assertTrue(any("escapes holdout directory" in e for e in errs), errs)

            # Projections skip reading symlinked outside file
            proj_errs = validate._check_projections(broken_symlink, tmp_fixture_dir)
            self.assertEqual(proj_errs, [])

            # Symlink pointing inside holdout is rejected
            symlink_inside = tmp_fixture_dir / "holdout" / "inside.yaml"
            symlink_inside.symlink_to(tmp_fixture_dir / "holdout" / "checks.yaml")
            broken_inside = copy.deepcopy(fixture)
            broken_inside["holdout"]["assets"][0]["path"] = "holdout/inside.yaml"
            errs_inside = validate._check_refs(broken_inside, tmp_fixture_dir)
            self.assertTrue(
                any("path is a symlink" in e for e in errs_inside), errs_inside
            )


if __name__ == "__main__":
    unittest.main()
