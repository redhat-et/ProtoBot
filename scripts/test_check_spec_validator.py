from __future__ import annotations

import contextlib
import io
import shutil
import subprocess
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
class CheckEndToEndTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            ok, message = checker.build_validator(Path(tmp) / "validator", REAL_REPO)
        if not ok:
            raise unittest.SkipTest(f"validator cannot be built offline: {message}")

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = _make_repo(Path(tmp.name))
        self.req = self.repo / "spec" / "requirements" / "REQ-CLI-00001.yaml"

    def test_valid_spec_passes_quietly(self) -> None:
        """Pass with no output when the specification is valid."""
        rc, err = _run_check(self.repo)
        self.assertEqual(0, rc, err)
        self.assertEqual("", err)

    def test_invalid_spec_fails_with_diagnostic_and_path(self) -> None:
        """Fail with the diagnostic code and record path for an invalid record."""
        self.req.write_text(_NO_SHALL)
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("spec validator failed", err)
        self.assertIn("ears.shall_count", err)
        self.assertIn("REQ-CLI-00001.yaml", err)

    def test_reworded_text_fails_against_default_base(self) -> None:
        """Fail when requirement text changes relative to the default base."""
        self.req.write_text(_REWORDED)
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("requirement.text_immutable", err)

    def test_deleted_record_fails(self) -> None:
        """Fail when a requirement record is deleted."""
        self.req.unlink()
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("requirement.deleted", err)

    def test_feature_branch_compares_against_merge_base(self) -> None:
        """Compare a feature branch against its merge base with the default branch."""
        _git(self.repo, "checkout", "-q", "-b", "feature")
        self.req.write_text(_REWORDED)
        _git(self.repo, "commit", "-q", "-am", "reword")
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("requirement.text_immutable", err)

    def test_base_override_is_honoured(self) -> None:
        """Use the revision named by the base override variable."""
        self.req.write_text(_REWORDED)
        _git(self.repo, "commit", "-q", "-am", "reword")
        rc, _ = _run_check(self.repo, {checker.BASE_ENV: "HEAD"})
        self.assertEqual(0, rc)
        rc, err = _run_check(self.repo, {checker.BASE_ENV: "HEAD~1"})
        self.assertEqual(1, rc)
        self.assertIn("requirement.text_immutable", err)

    def test_option_like_override_is_rejected(self) -> None:
        """Reject an override such as ``-h`` instead of passing it to the validator."""
        self.req.write_text(_NO_SHALL)
        for value in ("-h", "--help"):
            rc, err = _run_check(self.repo, {checker.BASE_ENV: value})
            self.assertEqual(1, rc, value)
            self.assertIn("must not start with", err)

    def test_fallback_to_head_prints_note(self) -> None:
        """Print a note when no default-branch ref resolves."""
        _git(self.repo, "branch", "-m", "trunk")
        rc, err = _run_check(self.repo)
        self.assertEqual(0, rc, err)
        self.assertIn(checker.FALLBACK_NOTE, err)

    def test_self_comparison_flag_only_when_base_is_head(self) -> None:
        """Pass ``--allow-self-comparison`` only when the base equals HEAD."""
        calls = []

        def fake_run(cmd, **kwargs):
            calls.append(cmd)
            return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

        with (
            patch.object(checker, "build_validator", return_value=(True, "")),
            patch.object(checker.subprocess, "run", side_effect=fake_run),
        ):
            with patch.object(checker, "resolve_base", return_value=("abc", False)):
                _run_check(self.repo)
            with patch.object(checker, "resolve_base", return_value=("abc", True)):
                _run_check(self.repo)
        self.assertNotIn("--allow-self-comparison", calls[0])
        self.assertIn("--allow-self-comparison", calls[1])
        self.assertIn("--base=abc", calls[0])

    def test_failure_message_includes_status(self) -> None:
        """Include the validator exit status in the failure message."""
        self.req.write_text(_NO_SHALL)
        _, err = _run_check(self.repo)
        self.assertIn("status 1", err)

    def test_staged_deletion_with_file_on_disk_is_not_detected(self) -> None:
        """Document that the working tree, not the index, is validated."""
        _git(self.repo, "rm", "-q", "--cached", "spec/requirements/REQ-CLI-00001.yaml")
        rc, err = _run_check(self.repo)
        self.assertEqual(0, rc, err)

    def test_untracked_invalid_draft_fails(self) -> None:
        """Document that an untracked invalid record fails the check."""
        draft = self.repo / "spec" / "requirements" / "REQ-CLI-00002.yaml"
        draft.write_text(_NO_SHALL.replace("00001", "00002"))
        rc, err = _run_check(self.repo)
        self.assertEqual(1, rc)
        self.assertIn("REQ-CLI-00002.yaml", err)


@unittest.skipUnless(
    shutil.which("go") and shutil.which("git") and shutil.which("pre-commit"),
    "go, git and pre-commit required",
)
class PreCommitHookTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            ok, message = checker.build_validator(Path(tmp) / "validator", REAL_REPO)
        if not ok:
            raise unittest.SkipTest(f"validator cannot be built offline: {message}")

    def test_hook_blocks_invalid_staged_change(self) -> None:
        """Run the real hook through pre-commit against a scratch clone."""
        with tempfile.TemporaryDirectory() as tmp:
            repo = _make_repo(Path(tmp))
            shutil.copytree(
                REAL_REPO / "scripts",
                repo / "scripts",
                ignore=shutil.ignore_patterns("__pycache__"),
            )
            (repo / ".pre-commit-config.yaml").write_text(
                "repos:\n  - repo: local\n    hooks:\n"
                "      - id: spec-validator\n        name: spec-validator\n"
                "        entry: python3 scripts/check_spec_validator.py\n        language: system\n"
                "        pass_filenames: false\n        always_run: true\n"
            )
            _git(repo, "add", "-A")
            _git(repo, "commit", "-q", "-m", "hook")
            req = repo / "spec" / "requirements" / "REQ-CLI-00001.yaml"
            req.write_text(_NO_SHALL)
            _git(repo, "add", "-A")
            result = subprocess.run(
                ["pre-commit", "run", "spec-validator", "--all-files"],
                cwd=repo,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(0, result.returncode, result.stdout)
            self.assertIn("ears.shall_count", result.stdout + result.stderr)


@unittest.skipUnless(shutil.which("git"), "git required")
class ResolveBaseTests(unittest.TestCase):
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
        """Use HEAD as the base when no default branch resolves."""
        self.assertEqual(("HEAD", True), checker.resolve_base(self.repo, {}))

    def test_default_branch_at_head_uses_self_comparison(self) -> None:
        """Allow self-comparison when the default branch is at HEAD."""
        _git(self.repo, "branch", "main")
        self.assertEqual((self.head, True), checker.resolve_base(self.repo, {}))

    def test_diverged_branch_uses_merge_base(self) -> None:
        """Use the merge base when the branch has diverged from the default branch."""
        _git(self.repo, "branch", "main")
        _git(self.repo, "checkout", "-q", "-b", "feature")
        (self.repo / "b.txt").write_text("b")
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-q", "-m", "two")
        self.assertEqual((self.head, False), checker.resolve_base(self.repo, {}))

    def test_upstream_main_preferred_over_main(self) -> None:
        """Prefer upstream/main over a local main branch."""
        _git(self.repo, "branch", "main")
        (self.repo / "b.txt").write_text("b")
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-q", "-m", "two")
        new_head = _git(self.repo, "rev-parse", "HEAD")
        _git(self.repo, "update-ref", "refs/remotes/upstream/main", new_head)
        self.assertEqual((new_head, True), checker.resolve_base(self.repo, {}))

    def test_override_wins(self) -> None:
        """Prefer the base override over any default branch."""
        _git(self.repo, "branch", "main")
        self.assertEqual(
            ("HEAD", True),
            checker.resolve_base(self.repo, {checker.BASE_ENV: "HEAD"}),
        )

    def test_github_base_ref_is_honoured(self) -> None:
        """Use origin/$GITHUB_BASE_REF when set and resolvable."""
        _git(self.repo, "update-ref", "refs/remotes/origin/release", self.head)
        self.assertEqual(
            (self.head, True),
            checker.resolve_base(self.repo, {"GITHUB_BASE_REF": "release"}),
        )

    def test_github_base_sha_is_honoured(self) -> None:
        """Use GITHUB_BASE_SHA when it resolves."""
        self.assertEqual(
            (self.head, True),
            checker.resolve_base(self.repo, {"GITHUB_BASE_SHA": self.head}),
        )

    def test_unresolvable_override_is_passed_through(self) -> None:
        """Pass an unresolvable override through so the validator reports it."""
        self.assertEqual(
            ("no-such-ref", False),
            checker.resolve_base(self.repo, {checker.BASE_ENV: "no-such-ref"}),
        )


class BuildValidatorTests(unittest.TestCase):
    def _completed(self, rc: int, stderr: str = "") -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess([], rc, stdout="", stderr=stderr)

    def test_build_is_offline(self) -> None:
        """Build with module downloads, workspaces and network lookups disabled."""
        with patch.object(
            checker.subprocess, "run", return_value=self._completed(0)
        ) as run:
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertTrue(ok)
        self.assertEqual("", message)
        env = run.call_args.kwargs["env"]
        self.assertEqual("off", env["GOPROXY"])
        self.assertEqual("off", env["GOWORK"])
        # validator/vendor/ holds the vendored module dependencies, so the
        # real repository builds with -mod=vendor rather than -mod=readonly.
        self.assertEqual("-mod=vendor", env["GOFLAGS"])

    def test_build_goflags_reflects_vendor_directory(self) -> None:
        """Use -mod=vendor when validator/vendor/ exists and -mod=readonly when absent."""
        with tempfile.TemporaryDirectory(prefix="test-validator-build-") as tmp:
            synthetic_repo = Path(tmp)
            validator_dir = synthetic_repo / "validator"
            validator_dir.mkdir()

            # Without vendor/ directory
            with patch.object(
                checker.subprocess, "run", return_value=self._completed(0)
            ) as run:
                ok, _ = checker.build_validator(Path("/tmp/out"), synthetic_repo)
            self.assertTrue(ok)
            self.assertEqual("-mod=readonly", run.call_args.kwargs["env"]["GOFLAGS"])

            # With vendor/ directory
            vendor_dir = validator_dir / "vendor"
            vendor_dir.mkdir()
            with patch.object(
                checker.subprocess, "run", return_value=self._completed(0)
            ) as run:
                ok, _ = checker.build_validator(Path("/tmp/out"), synthetic_repo)
            self.assertTrue(ok)
            self.assertEqual("-mod=vendor", run.call_args.kwargs["env"]["GOFLAGS"])

    def test_dependency_failure_is_reported_as_offline_problem(self) -> None:
        """Report unresolved module dependencies as an offline problem."""
        failure = self._completed(1, "go: module lookup disabled by GOPROXY=off")
        with patch.object(checker.subprocess, "run", return_value=failure):
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertFalse(ok)
        self.assertIn("could not be resolved offline", message)

    def test_compile_failure_is_reported_verbatim(self) -> None:
        """Report a compile failure without the offline explanation."""
        failure = self._completed(1, "main.go:3: syntax error")
        with patch.object(checker.subprocess, "run", return_value=failure):
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertFalse(ok)
        self.assertIn("syntax error", message)
        self.assertNotIn("offline", message)

    def test_missing_go_fails_with_message(self) -> None:
        """Fail with a clear message when go is not on PATH."""
        with patch.object(checker.subprocess, "run", side_effect=FileNotFoundError):
            ok, message = checker.build_validator(Path("/tmp/out"), REAL_REPO)
        self.assertFalse(ok)
        self.assertIn("go not found", message)

    def test_check_returns_failure_when_build_fails(self) -> None:
        """Return failure and print the message when the build fails."""
        with (
            patch.object(checker, "resolve_base", return_value=("HEAD", True)),
            patch.object(checker, "build_validator", return_value=(False, "boom")),
        ):
            rc, err = _run_check(REAL_REPO)
        self.assertEqual(1, rc)
        self.assertIn("boom", err)


class MainTests(unittest.TestCase):
    def test_main_returns_check_result(self) -> None:
        """Return the exit code produced by check."""
        with patch.object(checker, "check", return_value=1) as check:
            self.assertEqual(1, checker.main([]))
        check.assert_called_once_with()


if __name__ == "__main__":
    unittest.main()
