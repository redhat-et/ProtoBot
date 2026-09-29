#!/usr/bin/env python3
"""Deterministic validation for the evaluation corpus contract.

Validates version-1 fixture, holdout, projection, and result documents
without a hosted cluster, Git host, or external service credentials.
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import sys
from collections.abc import Iterator
from pathlib import Path
from typing import Any

try:
    import yaml
except ImportError:
    sys.exit("ERROR: PyYAML is required but not installed.")

try:
    from jsonschema import Draft202012Validator
except ImportError:
    sys.exit("ERROR: jsonschema is required but not installed.")

CORPUS_ROOT = Path(__file__).resolve().parent
SCHEMA_DIR = CORPUS_ROOT / "schema"
EXAMPLES_DIR = CORPUS_ROOT / "examples"
SUPPORTED_SCHEMA_VERSION = 1
ELICITATION_KEYS = (
    "schema_version",
    "fixture_id",
    "corpus_revision",
    "archetype",
    "title",
    "project_description",
)
JOB_SITE_KEYS = (
    "schema_version",
    "fixture_id",
    "corpus_revision",
    "archetype",
    "title",
    "visible",
)
WORKER_FORBIDDEN_PARTS = frozenset({"holdout"})
HOLDOUT_TOKEN = "EVAL-HOLDOUT-"


def _load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def _load_yaml(path: Path) -> Any:
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _sha256(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def _validator(name: str) -> Draft202012Validator:
    schema = _load_json(SCHEMA_DIR / name)
    Draft202012Validator.check_schema(schema)
    return Draft202012Validator(schema)


def _iter_errors(validator: Draft202012Validator, instance: Any) -> list[str]:
    return [
        f"{list(error.absolute_path)}: {error.message}"
        for error in validator.iter_errors(instance)
    ]


def _interface_ids(fixture: dict[str, Any]) -> set[str]:
    architecture = fixture["visible"]["architecture"]
    return {item["id"] for item in architecture["external_interfaces"]}


def _requirement_ids(fixture: dict[str, Any]) -> set[str]:
    return {item["id"] for item in fixture["visible"]["requirements"]}


def project_elicitation_input(fixture: dict[str, Any]) -> dict[str, Any]:
    """Return the generator-visible elicitation projection."""
    return {key: copy.deepcopy(fixture[key]) for key in ELICITATION_KEYS}


def project_job_site_input(fixture: dict[str, Any]) -> dict[str, Any]:
    """Return the Worker-visible Job Site projection."""
    return {key: copy.deepcopy(fixture[key]) for key in JOB_SITE_KEYS}


def worker_visible_files(fixture_dir: Path) -> list[Path]:
    """Return files a Worker projection may mount from a fixture directory."""
    visible: list[Path] = []
    for path in sorted(fixture_dir.rglob("*")):
        if not path.is_file():
            continue
        parts = set(path.relative_to(fixture_dir).parts)
        if parts & WORKER_FORBIDDEN_PARTS:
            continue
        visible.append(path)
    return visible


def _check_refs(fixture: dict[str, Any], fixture_dir: Path) -> list[str]:
    errors: list[str] = []
    interfaces = _interface_ids(fixture)
    requirements = _requirement_ids(fixture)

    if len(interfaces) != len(
        fixture["visible"]["architecture"]["external_interfaces"]
    ):
        errors.append("architecture.external_interfaces ids are not unique")
    if len(requirements) != len(fixture["visible"]["requirements"]):
        errors.append("visible.requirements ids are not unique")

    archetype = fixture["archetype"]
    if not any(
        item["type"] == archetype
        for item in fixture["visible"]["architecture"]["external_interfaces"]
    ):
        errors.append(
            f"architecture.external_interfaces has no interface of type {archetype}"
        )

    for req in fixture["visible"]["requirements"]:
        for interface_id in req.get("applies_to", {}).get("interfaces", []):
            if interface_id not in interfaces:
                errors.append(
                    f"{req['id']} applies_to unknown interface {interface_id}"
                )

    for runtime in fixture["visible"]["interface_runtime"]:
        if runtime["interface_id"] not in interfaces:
            errors.append(
                f"interface_runtime names unknown interface {runtime['interface_id']}"
            )
        if runtime["hosted_cluster"]:
            errors.append(f"{runtime['interface_id']} requires a hosted cluster")
        if runtime["credentials"] != "none":
            errors.append(f"{runtime['interface_id']} requires external credentials")

    seen_checks: set[str] = set()
    for check in fixture["visible"]["reference_behavior_checks"]:
        if check["id"] in seen_checks:
            errors.append(f"duplicate visible check {check['id']}")
        seen_checks.add(check["id"])
        if check["interface_id"] not in interfaces:
            errors.append(f"{check['id']} names unknown interface")
        for req_id in check["requirement_ids"]:
            if req_id not in requirements:
                errors.append(f"{check['id']} names unknown requirement {req_id}")

    holdout_ids: set[str] = set()
    for asset in fixture["holdout"]["assets"]:
        if asset["id"] in holdout_ids:
            errors.append(f"duplicate holdout asset {asset['id']}")
        holdout_ids.add(asset["id"])
        if asset["interface_id"] not in interfaces:
            errors.append(f"{asset['id']} names unknown interface")
        for req_id in asset["requirement_ids"]:
            if req_id not in requirements:
                errors.append(f"{asset['id']} names unknown requirement {req_id}")
        path = Path(asset["path"])
        if path.is_absolute() or ".." in path.parts or path.parts[:1] != ("holdout",):
            errors.append(f"{asset['id']} path is not a holdout/ relative path")
            continue
        asset_path = fixture_dir / path
        if not asset_path.is_file():
            errors.append(f"{asset['id']} missing file {path}")
            continue
        digest = _sha256(asset_path)
        if digest != asset["digest"]:
            errors.append(
                f"{asset['id']} digest mismatch: recorded {asset['digest']}, "
                f"computed {digest}"
            )
        holdout_doc = _load_yaml(asset_path)
        holdout_errors = _iter_errors(
            _validator("holdout-asset.schema.json"), holdout_doc
        )
        errors.extend(f"{asset['id']} {item}" for item in holdout_errors)
        if isinstance(holdout_doc, dict):
            if holdout_doc.get("fixture_id") != fixture["fixture_id"]:
                errors.append(f"{asset['id']} fixture_id does not match the fixture")
            check_ids = {item["id"] for item in holdout_doc.get("checks", [])}
            if asset["id"] not in check_ids:
                errors.append(f"{asset['id']} is not present in {path}")
    return errors


def _check_projections(fixture: dict[str, Any], fixture_dir: Path) -> list[str]:
    errors: list[str] = []
    elicitation = project_elicitation_input(fixture)
    job_site = project_job_site_input(fixture)
    errors.extend(
        f"elicitation-input {item}"
        for item in _iter_errors(
            _validator("elicitation-input.schema.json"), elicitation
        )
    )
    errors.extend(
        f"job-site-input {item}"
        for item in _iter_errors(_validator("job-site-input.schema.json"), job_site)
    )
    elicitation_dump = yaml.safe_dump(elicitation, sort_keys=True)
    job_site_dump = yaml.safe_dump(job_site, sort_keys=True)
    for needle in ("VIS-", "HO-", "REQ-"):
        if needle in elicitation_dump:
            errors.append(
                f"elicitation-input leaked evaluator material containing {needle}"
            )
    if "holdout" in elicitation or "visible" in elicitation:
        errors.append("elicitation-input contains evaluator-only keys")
    if "holdout" in job_site:
        errors.append("job-site-input contains holdout")
    if "project_description" in job_site:
        errors.append("job-site-input contains the elicitation project_description")
    for req in fixture["visible"]["requirements"]:
        if req["text"] in elicitation_dump:
            errors.append(f"elicitation-input leaked golden text of {req['id']}")
    for asset in fixture["holdout"]["assets"]:
        asset_path = fixture_dir / asset["path"]
        if not asset_path.is_file():
            continue
        holdout_text = asset_path.read_text(encoding="utf-8")
        if HOLDOUT_TOKEN in job_site_dump or HOLDOUT_TOKEN in elicitation_dump:
            errors.append(f"{asset['id']} holdout token leaked into a projection")
        if HOLDOUT_TOKEN not in holdout_text:
            errors.append(
                f"{asset['id']} is missing an {HOLDOUT_TOKEN} isolation token"
            )

    worker_files = {path.name for path in worker_visible_files(fixture_dir)}
    holdout_names = {Path(asset["path"]).name for asset in fixture["holdout"]["assets"]}
    leaked = sorted(worker_files & holdout_names)
    if leaked:
        errors.append(f"Worker-visible files include holdout assets: {leaked}")
    for path in fixture_dir.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(fixture_dir)
        if "holdout" in rel.parts and path in worker_visible_files(fixture_dir):
            errors.append(f"Worker projection mounted holdout path {rel}")
    return errors


def validate_fixture(path: Path) -> list[str]:
    """Validate one fixture document and its holdout assets."""
    fixture = _load_yaml(path)
    errors = _iter_errors(_validator("fixture.schema.json"), fixture)
    if errors:
        return errors
    if fixture["schema_version"] != SUPPORTED_SCHEMA_VERSION:
        return [f"unsupported schema_version {fixture['schema_version']}"]
    errors.extend(_check_refs(fixture, path.parent))
    errors.extend(_check_projections(fixture, path.parent))
    return errors


def _check_result_semantics(
    result: dict[str, Any], examples: dict[str, Path]
) -> list[str]:
    errors: list[str] = []
    env = result["reproducibility"]["environment"]
    inputs = result["reproducibility"]["inputs"]
    if env["mode"] == "local":
        if env["hosted_cluster"]:
            errors.append("local result records a hosted cluster")
        if env["external_service_credentials"]:
            errors.append("local result records external service credentials")
    expected_projection = {
        "requirements": "elicitation-input",
        "job-site": "job-site-input",
        "end-to-end": "end-to-end",
    }[result["stage"]]
    if inputs["projection"] != expected_projection:
        errors.append(f"stage {result['stage']} used projection {inputs['projection']}")
    scores = result["scores"]
    if result["stage"] == "requirements" and set(scores) != {"requirements"}:
        errors.append("requirements result must score only requirements")
    if result["stage"] == "job-site" and set(scores) != {"job_site"}:
        errors.append("Job Site result must score only job_site")
    if result["stage"] == "end-to-end" and set(scores) != {
        "requirements",
        "job_site",
        "end_to_end",
    }:
        errors.append("end-to-end result must score all three stages")
    if "end_to_end" in scores:
        attribution = scores["end_to_end"]["attribution"]
        if attribution["requirements"] != scores["requirements"]["pass"]:
            errors.append("end-to-end attribution.requirements disagrees")
        if attribution["job_site"] != scores["job_site"]["pass"]:
            errors.append("end-to-end attribution.job_site disagrees")
    fixture_path = examples.get(inputs["fixture_id"])
    if fixture_path is not None:
        digest = _sha256(fixture_path)
        if digest != inputs["fixture_digest"]:
            errors.append(
                "fixture_digest does not match "
                f"{fixture_path}: recorded {inputs['fixture_digest']}, "
                f"computed {digest}"
            )
        if inputs["corpus_revision"] != _load_yaml(fixture_path)["corpus_revision"]:
            errors.append("result corpus_revision does not match the fixture")
    return errors


def validate_result(path: Path, examples: dict[str, Path]) -> list[str]:
    """Validate one result document."""
    result = _load_yaml(path)
    errors = _iter_errors(_validator("result.schema.json"), result)
    if errors:
        return errors
    errors.extend(_check_result_semantics(result, examples))
    return errors


def discover_examples(root: Path) -> tuple[dict[str, Path], list[Path]]:
    fixtures: dict[str, Path] = {}
    for path in sorted((root / "examples").glob("*/fixture.yaml")):
        fixture_id = _load_yaml(path)["fixture_id"]
        fixtures[fixture_id] = path
    results = sorted((root / "examples" / "results").glob("*.yaml"))
    return fixtures, results


def _failing_copy(base: dict[str, Any], mutator: Any) -> dict[str, Any]:
    clone = copy.deepcopy(base)
    mutator(clone)
    return clone


def _negative_cases(valid: dict[str, Any]) -> Iterator[tuple[str, dict[str, Any], str]]:
    yield (
        "missing-vision",
        _failing_copy(valid, lambda doc: doc["visible"].pop("vision")),
        "vision",
    )
    yield (
        "missing-architecture",
        _failing_copy(valid, lambda doc: doc["visible"].pop("architecture")),
        "architecture",
    )
    yield (
        "missing-requirements",
        _failing_copy(valid, lambda doc: doc["visible"].pop("requirements")),
        "requirements",
    )
    yield (
        "empty-interfaces",
        _failing_copy(
            valid,
            lambda doc: doc["visible"]["architecture"].update(external_interfaces=[]),
        ),
        "external_interfaces",
    )
    yield (
        "unsupported-schema",
        _failing_copy(valid, lambda doc: doc.update(schema_version=2)),
        "schema_version",
    )
    yield (
        "holdout-escape",
        _failing_copy(
            valid,
            lambda doc: doc["holdout"]["assets"][0].update(path="../secret.yaml"),
        ),
        "holdout/",
    )


def _negative_results(
    valid: dict[str, Any],
) -> Iterator[tuple[str, dict[str, Any], str]]:
    yield (
        "requirements-with-job-site-scores",
        _failing_copy(
            valid,
            lambda doc: doc["scores"].update(
                job_site={
                    "pass": True,
                    "summary": "leaked",
                    "visible_checks": {"passed": 0, "failed": 0, "total": 0},
                    "holdout_checks": {"passed": 0, "failed": 0, "total": 0},
                }
            ),
        ),
        "job_site",
    )
    yield (
        "missing-environment",
        _failing_copy(valid, lambda doc: doc["reproducibility"].pop("environment")),
        "environment",
    )
    yield (
        "end-to-end-without-attribution",
        _failing_copy(
            valid,
            lambda doc: (
                doc.update(stage="end-to-end"),
                doc["reproducibility"]["inputs"].update(projection="end-to-end"),
            ),
        ),
        "end_to_end",
    )


def run_negative_checks() -> list[str]:
    """Return failures from expected-invalid documents."""
    errors: list[str] = []
    fixture = _load_yaml(EXAMPLES_DIR / "cli-sync" / "fixture.yaml")
    fixture_schema = _validator("fixture.schema.json")
    for name, doc, needle in _negative_cases(fixture):
        found = _iter_errors(fixture_schema, doc)
        if name == "holdout-escape":
            extra = _check_refs(doc, EXAMPLES_DIR / "cli-sync")
            found.extend(extra)
        if not any(needle in item for item in found):
            errors.append(f"negative fixture {name} did not fail on {needle}: {found}")

    result = _load_yaml(EXAMPLES_DIR / "results" / "requirements-cli-sync.yaml")
    result_schema = _validator("result.schema.json")
    for name, doc, needle in _negative_results(result):
        found = _iter_errors(result_schema, doc)
        if not any(needle in item for item in found):
            errors.append(f"negative result {name} did not fail on {needle}: {found}")
    return errors


def validate_tree(root: Path) -> list[str]:
    """Validate every example fixture and result under *root*."""
    errors: list[str] = []
    fixtures, results = discover_examples(root)
    if not fixtures:
        return ["no example fixtures found"]
    required = {"cli-sync", "api-registry", "web-status"}
    missing = sorted(required - set(fixtures))
    if missing:
        errors.append(f"missing archetype examples: {missing}")
    archetypes = {
        fixture_id: _load_yaml(path)["archetype"]
        for fixture_id, path in fixtures.items()
    }
    expected_archetypes = {
        "cli-sync": "cli",
        "api-registry": "network-service",
        "web-status": "web-gui",
    }
    for fixture_id, archetype in expected_archetypes.items():
        if archetypes.get(fixture_id) != archetype:
            errors.append(f"{fixture_id} archetype is {archetypes.get(fixture_id)}")
    for fixture_id, path in fixtures.items():
        for item in validate_fixture(path):
            errors.append(f"{path}: {item}")
    if not results:
        errors.append("no example results found")
    for path in results:
        for item in validate_result(path, fixtures):
            errors.append(f"{path}: {item}")
    errors.extend(run_negative_checks())
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        type=Path,
        default=CORPUS_ROOT,
        help="corpus root containing schema/ and examples/",
    )
    args = parser.parse_args(argv)
    errors = validate_tree(args.root)
    if errors:
        print("EVAL CORPUS VALIDATION FAILED")
        for item in errors:
            print(f"  - {item}")
        return 1
    print("EVAL CORPUS VALIDATION PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
