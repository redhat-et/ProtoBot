from __future__ import annotations

import contextlib
import io
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

if __package__:
    from scripts import check_spec_validator as checker
else:
    import check_spec_validator as checker

REAL_REPO = Path(__file__).resolve().parent.parent

_REQUIREMENT = """---
id: REQ-CLI-00001
type: event-driven
text: >-
  When a contributor requests a specification check, the CLI shall
  validate the registered artifacts and structured records.
applies_to:
  interfaces:
    - protobot-cli
relationships: []
status: active
"""

_INTERFACE = """---
name: protobot-cli
kind: cli
description: >-
  Command-line interface for specification validation.
"""

_NO_SHALL = _REQUIREMENT.replace(
    "the CLI shall\n  validate", "the CLI will\n  validate"
)
_REWORDED = _REQUIREMENT.replace("validate the registered", "check the registered")


def _git(repo: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=repo,
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout.strip()


def _make_repo(root: Path) -> Path:
    """Create a git repo on ``main`` holding a valid spec and the validator."""
    repo = root / "repo"
    repo.mkdir()
    _git(repo, "init", "-q", "-b", "main")
    _git(repo, "config", "user.email", "test@example.com")
    _git(repo, "config", "user.name", "Test")
    _git(repo, "config", "commit.gpgsign", "false")
    (repo / "spec" / "requirements").mkdir(parents=True)
    (repo / "spec" / "interfaces").mkdir(parents=True)
    (repo / "spec" / "requirements" / "REQ-CLI-00001.yaml").write_text(_REQUIREMENT)
    (repo / "spec" / "interfaces" / "protobot-cli.yaml").write_text(_INTERFACE)
    shutil.copytree(REAL_REPO / "validator", repo / "validator")
    _git(repo, "add", "-A")
    _git(repo, "commit", "-q", "-m", "initial")
    return repo


def _run_check(repo: Path, env: dict[str, str] | None = None) -> tuple[int, str]:
    err = io.StringIO()
    with contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
        rc = checker.check(repo, env if env is not None else {})
    return rc, err.getvalue()


@unittest.skipUnless(shutil.which("go") and shutil.which("git"), "go and git required")
class CheckEndToEndTest(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = _make_repo(Path(tmp.name))
        self.req = self.repo / "spec" / "requirements" / "REQ-CLI-00001.yaml"

    def test_valid_spec_passes_quietly(self) -> None:
        rc, err = _run_check(self.repo)
        self.assertEqual(0, rc, err)
        self.assertEqual("", err)

    def test_invalid_spec_fails_with_diagnostic_and_path(self) -> None:
        self.req.write_text(_NO_SHALL)
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("spec validator failed", err)
        self.assertIn("ears.shall_count", err)
        self.assertIn("REQ-CLI-00001.yaml", err)

    def test_reworded_text_fails_against_default_base(self) -> None:
        self.req.write_text(_REWORDED)
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("requirement.text_immutable", err)

    def test_deleted_record_fails(self) -> None:
        self.req.unlink()
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("requirement.deleted", err)

    def test_feature_branch_compares_against_merge_base(self) -> None:
        _git(self.repo, "checkout", "-q", "-b", "feature")
        self.req.write_text(_REWORDED)
        _git(self.repo, "commit", "-q", "-am", "reword")
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("requirement.text_immutable", err)

    def test_base_override_is_honoured(self) -> None:
        self.req.write_text(_REWORDED)
        _git(self.repo, "commit", "-q", "-am", "reword")
        rc, _ = _run_check(self.repo, {checker.BASE_ENV: "HEAD"})
        self.assertEqual(0, rc)
        rc, err = _run_check(self.repo, {checker.BASE_ENV: "HEAD~1"})
        self.assertEqual(1, rc)
        self.assertIn("requirement.text_immutable", err)


@unittest.skipUnless(shutil.which("git"), "git required")
class ResolveBaseTest(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = Path(tmp.name) / "repo"
        self.repo.mkdir()
        _git(self.repo, "init", "-q", "-b", "trunk")
        _git(self.repo, "config", "user.email", "test@example.com")
        _git(self.repo, "config", "user.name", "Test")
        _git(self.repo, "config", "commit.gpgsign", "false")
        (self.repo / "a.txt").write_text("a")
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-q", "-m", "one")
        self.head = _git(self.repo, "rev-parse", "HEAD")

    def test_falls_back_to_head_without_default_branch(self) -> None:
        self.assertEqual(("HEAD", True), checker.resolve_base(self.repo, {}))

    def test_default_branch_at_head_uses_self_comparison(self) -> None:
        _git(self.repo, "branch", "main")
        self.assertEqual((self.head, True), checker.resolve_base(self.repo, {}))

    def test_diverged_branch_uses_merge_base(self) -> None:
        _git(self.repo, "branch", "main")
        _git(self.repo, "checkout", "-q", "-b", "feature")
        (self.repo / "b.txt").write_text("b")
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-q", "-m", "two")
        self.assertEqual((self.head, False), checker.resolve_base(self.repo, {}))

    def test_upstream_main_preferred_over_main(self) -> None:
        _git(self.repo, "branch", "main")
        (self.repo / "b.txt").write_text("b")
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-q", "-m", "two")
        new_head = _git(self.repo, "rev-parse", "HEAD")
        _git(self.repo, "update-ref", "refs/remotes/upstream/main", new_head)
        self.assertEqual((new_head, True), checker.resolve_base(self.repo, {}))

    def test_override_wins(self) -> None:
        _git(self.repo, "branch", "main")
        self.assertEqual(
            ("HEAD", True),
            checker.resolve_base(self.repo, {checker.BASE_ENV: "HEAD"}),
        )

    def test_unresolvable_override_is_passed_through(self) -> None:
        self.assertEqual(
            ("no-such-ref", False),
            checker.resolve_base(self.repo, {checker.BASE_ENV: "no-such-ref"}),
        )


class BuildValidatorTest(unittest.TestCase):
    def _completed(self, rc: int, stderr: str = "") -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess([], rc, stdout="", stderr=stderr)

    def test_build_is_offline(self) -> None:
        with patch.object(
            checker.subprocess, "run", return_value=self._completed(0)
        ) as run:
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertTrue(ok)
        self.assertEqual("", message)
        env = run.call_args.kwargs["env"]
        self.assertEqual("off", env["GOPROXY"])
        self.assertEqual("off", env["GOWORK"])
        self.assertEqual("-mod=readonly", env["GOFLAGS"])

    def test_dependency_failure_is_reported_as_offline_problem(self) -> None:
        failure = self._completed(1, "go: module lookup disabled by GOPROXY=off")
        with patch.object(checker.subprocess, "run", return_value=failure):
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertFalse(ok)
        self.assertIn("could not be resolved offline", message)

    def test_compile_failure_is_reported_verbatim(self) -> None:
        failure = self._completed(1, "main.go:3: syntax error")
        with patch.object(checker.subprocess, "run", return_value=failure):
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertFalse(ok)
        self.assertIn("syntax error", message)
        self.assertNotIn("offline", message)

    def test_missing_go_fails_with_message(self) -> None:
        with patch.object(checker.subprocess, "run", side_effect=FileNotFoundError):
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertFalse(ok)
        self.assertIn("go not found", message)

    def test_check_returns_failure_when_build_fails(self) -> None:
        with (
            patch.object(checker, "resolve_base", return_value=("HEAD", True)),
            patch.object(checker, "build_validator", return_value=(False, "boom")),
        ):
            rc, err = _run_check(REAL_REPO)
        self.assertEqual(1, rc)
        self.assertIn("boom", err)


class MainTest(unittest.TestCase):
    def test_main_returns_check_result(self) -> None:
        with patch.object(checker, "check", return_value=1) as check:
            self.assertEqual(1, checker.main([]))
        check.assert_called_once_with()


if __name__ == "__main__":
    sys.exit(unittest.main())
