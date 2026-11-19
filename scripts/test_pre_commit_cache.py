from __future__ import annotations

import contextlib
import hashlib
import io
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Self
from unittest.mock import Mock, patch

import yaml

if __package__:
    from scripts import lint as lint_wrapper
    from scripts import pre_commit_cache as bootstrap
else:
    import lint as lint_wrapper
    import pre_commit_cache as bootstrap


REPO_ROOT = Path(__file__).resolve().parent.parent

# Extracts the vX.Y.Z ref from a harness file's pinned ``base:`` URL.
_BASE_PIN = re.compile(
    r"^base:[ \t]*\"https://raw\.githubusercontent\.com/fullsend-ai/agents/"
    r"v(?P<version>[0-9]+\.[0-9]+\.[0-9]+)/",
    re.MULTILINE,
)

_URL = "https://raw.githubusercontent.com/fullsend-ai/agents/v0.56.1/scripts/x.sh"


def _completed(
    returncode: int = 0, stdout: str = "", stderr: str = ""
) -> subprocess.CompletedProcess:
    return subprocess.CompletedProcess(
        args=[], returncode=returncode, stdout=stdout, stderr=stderr
    )


def _fake_run_succeeds(argv, *, env=None, timeout=None, capture=False, cwd=None):
    """Stand-in for ``_run``: version probes match the pin, all else is 0.

    A ``tar`` invocation writes a real file so the atomic publish
    (``os.replace``) has something to rename.
    """
    if "--version" in argv:
        return _completed(stdout=f"pre-commit {bootstrap.PRECOMMIT_VERSION}\n")
    if argv and argv[0] == "tar":
        Path(argv[2]).write_bytes(b"tarball")
    return _completed()


class _FakeResponse:
    """Minimal context-managed HTTP response for download tests."""

    def __init__(self, data: bytes, url: str) -> None:
        self._data = data
        self._url = url

    def read(self, limit: int = -1) -> bytes:
        return self._data[:limit] if limit > 0 else self._data

    def geturl(self) -> str:
        return self._url

    def __enter__(self) -> Self:
        return self

    def __exit__(self, *exc: object) -> None:
        return None


class SkipUpstreamTests(unittest.TestCase):
    """``--skip-upstream`` runs the warm phase without any download."""

    def test_skip_upstream_never_downloads(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        root = Path(temporary_directory.name)
        with (
            patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds) as run,
            patch("urllib.request.urlopen") as urlopen,
        ):
            rc = bootstrap.main(
                [
                    "--upstream",
                    "code",
                    "--skip-upstream",
                    "--cache-home",
                    str(root / "cache"),
                    "--tarball",
                    str(root / "pre-commit-cache.tar.gz"),
                ]
            )
        self.assertEqual(0, rc)
        urlopen.assert_not_called()
        # The warm phase ran: the tarball command was reached.
        tar_calls = [
            call.args[0] for call in run.call_args_list if call.args[0][0] == "tar"
        ]
        self.assertEqual(1, len(tar_calls))
        self.assertIn("-czf", tar_calls[0])


class DownloadVerificationTests(unittest.TestCase):
    """The pinned pre-script is verified before it runs."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.destination = Path(temporary_directory.name) / "script.sh"

    def _patch_opener(self, data: bytes, url: str = _URL) -> patch:
        opener = Mock()
        opener.open = Mock(return_value=_FakeResponse(data, url))
        return patch("urllib.request.build_opener", return_value=opener)

    def test_matching_hash_is_written(self) -> None:
        payload = b"#!/usr/bin/env bash\ntrue\n"
        digest = hashlib.sha256(payload).hexdigest()
        with self._patch_opener(payload):
            bootstrap._download_upstream_script(_URL, self.destination, digest)
        self.assertEqual(payload, self.destination.read_bytes())

    def test_hash_mismatch_is_rejected(self) -> None:
        with (
            self._patch_opener(b"malicious"),
            self.assertRaises(bootstrap.UpstreamScriptError) as ctx,
        ):
            bootstrap._download_upstream_script(_URL, self.destination, "0" * 64)
        self.assertIn("sha256 mismatch", str(ctx.exception))
        self.assertFalse(self.destination.exists())

    def test_non_https_url_is_rejected(self) -> None:
        with self.assertRaises(bootstrap.UpstreamScriptError) as ctx:
            bootstrap._download_upstream_script(
                "http://example.invalid/x.sh", self.destination, "0" * 64
            )
        self.assertIn("non-https", str(ctx.exception))

    def test_redirect_is_rejected(self) -> None:
        redirected = "https://elsewhere.example/moved.sh"
        with (
            self._patch_opener(b"x", url=redirected),
            self.assertRaises(bootstrap.UpstreamScriptError) as ctx,
        ):
            bootstrap._download_upstream_script(_URL, self.destination, "0" * 64)
        self.assertIn("redirect", str(ctx.exception))

    def test_response_over_the_size_cap_is_rejected(self) -> None:
        payload = b"x" * (bootstrap.MAX_SCRIPT_BYTES + 1)
        with (
            self._patch_opener(payload),
            self.assertRaises(bootstrap.UpstreamScriptError) as ctx,
        ):
            bootstrap._download_upstream_script(_URL, self.destination, "0" * 64)
        self.assertIn("exceeds", str(ctx.exception))


class UpstreamDelegationTests(unittest.TestCase):
    """The upstream pre-script's exit code is returned verbatim."""

    def test_upstream_script_exit_code_is_verbatim(self) -> None:
        for code in (0, 78, 1):
            with self.subTest(code=code):
                with (
                    patch.object(bootstrap, "_download_upstream_script"),
                    patch.object(bootstrap, "_run", return_value=_completed(code)),
                ):
                    rc = bootstrap._run_upstream_script("code")
                self.assertEqual(code, rc)

    def test_main_returns_upstream_code_and_skips_warm_when_nonzero(self) -> None:
        for code in (0, 78, 1):
            with self.subTest(code=code):
                with (
                    patch.object(bootstrap, "_run_upstream_script", return_value=code),
                    patch.object(bootstrap, "_warm_cache") as warm,
                ):
                    rc = bootstrap.main(["--upstream", "fix"])
                self.assertEqual(code, rc)
                # A skipped (78) or failed run creates no sandbox, so the
                # warm phase must not run.
                self.assertEqual(code == 0, warm.called)

    def test_unverifiable_upstream_script_is_a_hard_failure(self) -> None:
        for side_effect in (
            OSError("no route to host"),
            bootstrap.UpstreamScriptError("sha256 mismatch for " + _URL),
        ):
            with self.subTest(side_effect=side_effect):
                with (
                    patch.object(
                        bootstrap,
                        "_download_upstream_script",
                        side_effect=side_effect,
                    ),
                    patch.object(bootstrap, "_run") as run,
                ):
                    stderr, stdout = io.StringIO(), io.StringIO()
                    with (
                        contextlib.redirect_stderr(stderr),
                        contextlib.redirect_stdout(stdout),
                    ):
                        rc = bootstrap.main(["--upstream", "code"])
                self.assertEqual(1, rc)
                run.assert_not_called()
                self.assertIn(
                    "cannot run the pinned upstream pre-script", stderr.getvalue()
                )
                self.assertIn("UPSTREAM_SCRIPT_SHA256", stderr.getvalue())


class WarmPhaseTests(unittest.TestCase):
    """The warm phase is fail-soft and owns the tarball layout."""

    def test_warm_failure_warns_and_returns_zero(self) -> None:
        failure = RuntimeError("pre-commit install-hooks failed (exit 1)\nBuild failed")
        with (
            patch.object(bootstrap, "_run_upstream_script", return_value=0),
            patch.object(bootstrap, "_warm_cache", side_effect=failure),
        ):
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                rc = bootstrap.main(["--upstream", "code"])
        self.assertEqual(0, rc)
        message = stdout.getvalue()
        self.assertIn("::warning::", message)
        # The real reason, not only the exit code.
        self.assertIn("Build failed", message)
        self.assertIn("pre-commit install-hooks --config", message)

    def test_tarball_command_uses_cache_home_as_tar_root(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        root = Path(temporary_directory.name)
        cache_home = root / "cache"
        tarball = root / "out" / "pre-commit-cache.tar.gz"
        staging = tarball.parent / f".{tarball.name}.{os.getpid()}.tmp"
        # ``-C <cache home> .`` archives the cache home's contents, so the
        # restore unpacks them directly into PRE_COMMIT_HOME. The archive
        # is written to a staging file and renamed into place.
        with patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds) as run:
            bootstrap._warm_cache(cache_home, tarball, "3.12")
        tar_calls = [
            call.args[0] for call in run.call_args_list if call.args[0][0] == "tar"
        ]
        self.assertEqual(
            [["tar", "-czf", str(staging), "-C", str(cache_home), "."]],
            tar_calls,
        )
        self.assertTrue(tarball.is_file())
        self.assertEqual([], list(tarball.parent.glob(f".{tarball.name}.*")))

    def test_tar_failure_leaves_no_advertised_archive_and_no_staging(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        root = Path(temporary_directory.name)
        cache_home = root / "cache"
        tarball = root / "out" / "pre-commit-cache.tar.gz"
        tarball.parent.mkdir(parents=True)
        # A stale archive from an earlier run must not survive a failure.
        tarball.write_bytes(b"stale")

        def failing_tar(argv, *, env=None, timeout=None, capture=False, cwd=None):
            if argv[0] == "tar":
                return _completed(returncode=1, stderr="tar: disk full")
            return _fake_run_succeeds(
                argv, env=env, timeout=timeout, capture=capture, cwd=cwd
            )

        with (
            patch.object(bootstrap, "_run", side_effect=failing_tar),
            self.assertRaises(RuntimeError) as ctx,
        ):
            bootstrap._warm_cache(cache_home, tarball, "3.12")
        self.assertIn("disk full", str(ctx.exception))
        self.assertFalse(tarball.exists())
        self.assertEqual([], list(tarball.parent.glob(f".{tarball.name}.*")))

    def test_install_hooks_runs_with_cache_home_env_and_repo_cwd(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        root = Path(temporary_directory.name)
        cache_home = root / "cache"
        seen = {}

        def fake_run(argv, *, env=None, timeout=None, capture=False, cwd=None):
            if "install-hooks" in argv:
                seen["env"] = env
                seen["timeout"] = timeout
                seen["cwd"] = cwd
            return _fake_run_succeeds(
                argv, env=env, timeout=timeout, capture=capture, cwd=cwd
            )

        with patch.object(bootstrap, "_run", side_effect=fake_run):
            bootstrap._warm_cache(cache_home, root / "out.tar.gz", "3.12")
        self.assertEqual(str(cache_home), seen["env"]["PRE_COMMIT_HOME"])
        self.assertEqual(bootstrap.INSTALL_HOOKS_TIMEOUT_SECONDS, seen["timeout"])
        # pre-commit resolves relative hook metadata from the repo root.
        self.assertEqual(str(bootstrap._target_repo()), seen["cwd"])

    def test_run_or_raise_includes_the_stderr_tail(self) -> None:
        result = _completed(returncode=2, stderr="line1\nline2\nline3\n")
        with (
            patch.object(bootstrap, "_run", return_value=result),
            self.assertRaises(RuntimeError) as ctx,
        ):
            bootstrap._run_or_raise(["failing-cmd"], description="boom")
        message = str(ctx.exception)
        self.assertIn("boom (exit 2)", message)
        self.assertIn("line3", message)

    def test_sudo_install_used_when_mkdir_is_denied(self) -> None:
        calls = []

        def fake_run(argv, *, env=None, timeout=None, capture=False, cwd=None):
            calls.append((argv, timeout))
            return _completed()

        cache_home = Path("/sandbox/.cache/pre-commit")
        with (
            patch.object(bootstrap, "_run", side_effect=fake_run),
            patch.object(Path, "mkdir", side_effect=PermissionError("denied")),
            patch("os.access", return_value=True),
        ):
            bootstrap._ensure_cache_home(cache_home)
        self.assertEqual(1, len(calls))
        argv, timeout = calls[0]
        self.assertEqual(
            [
                "sudo",
                "-n",
                "--",
                "install",
                "-d",
                "-o",
                str(os.getuid()),
                "-g",
                str(os.getgid()),
                str(cache_home),
            ],
            argv,
        )
        self.assertEqual(bootstrap.SUDO_TIMEOUT_SECONDS, timeout)

    def test_unwritable_cache_home_raises_an_actionable_error(self) -> None:
        with (
            patch("os.access", return_value=False),
            self.assertRaises(RuntimeError) as ctx,
        ):
            bootstrap._ensure_cache_home(Path("/tmp"))
        self.assertIn("not writable", str(ctx.exception))
        self.assertIn("sudo install -d", str(ctx.exception))


class PythonEnvironmentTests(unittest.TestCase):
    """The cache must match the sandbox interpreter before it publishes."""

    def _temporary_root(self) -> Path:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        return Path(temporary_directory.name)

    def _make_env(self, cache_home: Path, name: str) -> None:
        (cache_home / "repodir" / name).mkdir(parents=True, exist_ok=True)

    def test_matching_version_publishes_the_archive(self) -> None:
        root = self._temporary_root()
        cache_home = root / "cache"
        tarball = root / "pc.tar.gz"
        self._make_env(cache_home, "py_env-python3.12")
        with patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds):
            bootstrap._warm_cache(cache_home, tarball, "3.12")
        self.assertTrue(tarball.is_file())

    def test_mismatched_version_raises_and_writes_no_archive(self) -> None:
        root = self._temporary_root()
        cache_home = root / "cache"
        tarball = root / "pc.tar.gz"
        self._make_env(cache_home, "py_env-python3.11")
        with (
            patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds) as run,
            self.assertRaises(RuntimeError) as ctx,
        ):
            bootstrap._warm_cache(cache_home, tarball, "3.12")
        message = str(ctx.exception)
        self.assertIn("py_env-python3.11", message)
        self.assertIn("Python 3.12", message)
        self.assertIn("--sandbox-python", message)
        self.assertFalse(tarball.exists())
        # The archive step was never reached.
        self.assertFalse(any(call.args[0][0] == "tar" for call in run.call_args_list))

    def test_scan_ignores_non_python_environments(self) -> None:
        root = self._temporary_root()
        cache_home = root / "cache"
        tarball = root / "pc.tar.gz"
        for name in (
            "py_env-python3.12",
            "node_env-default",
            "py_env-python",  # no version suffix: not a python env name
        ):
            self._make_env(cache_home, name)
        with patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds):
            bootstrap._warm_cache(cache_home, tarball, "3.12")
        self.assertTrue(tarball.is_file())

    def test_sandbox_python_argument_overrides_the_default(self) -> None:
        root = self._temporary_root()
        cache_home = root / "cache"
        tarball = root / "pc.tar.gz"
        self._make_env(cache_home, "py_env-python3.14")
        with patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds):
            rc = bootstrap.main(
                [
                    "--upstream",
                    "code",
                    "--skip-upstream",
                    "--sandbox-python",
                    "3.14",
                    "--cache-home",
                    str(cache_home),
                    "--tarball",
                    str(tarball),
                ]
            )
        self.assertEqual(0, rc)
        self.assertTrue(tarball.is_file())

    def test_default_refuses_a_mismatched_interpreter(self) -> None:
        root = self._temporary_root()
        cache_home = root / "cache"
        tarball = root / "pc.tar.gz"
        self._make_env(cache_home, "py_env-python3.14")
        with patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds):
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                rc = bootstrap.main(
                    [
                        "--upstream",
                        "code",
                        "--skip-upstream",
                        "--cache-home",
                        str(cache_home),
                        "--tarball",
                        str(tarball),
                    ]
                )
        self.assertEqual(0, rc)
        self.assertIn("::warning::", stdout.getvalue())
        self.assertIn("py_env-python3.14", stdout.getvalue())
        self.assertFalse(tarball.exists())

    def test_sandbox_python_defaults(self) -> None:
        self.assertEqual(
            bootstrap.SANDBOX_PYTHON_VERSION,
            bootstrap._parse_args(["--upstream", "code"]).sandbox_python,
        )
        with patch.dict(
            os.environ,
            {"PROTOBOT_SANDBOX_PYTHON_VERSION": "3.13"},
            clear=False,
        ):
            self.assertEqual(
                "3.13",
                bootstrap._parse_args(["--upstream", "code"]).sandbox_python,
            )


class UpstreamSkipOutputTests(unittest.TestCase):
    """A ``skipped=true`` output line skips the warm phase."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.output_file = Path(temporary_directory.name) / "prescript-output"

    def _run_main(self) -> tuple[int, object]:
        with (
            patch.object(bootstrap, "_run_upstream_script", return_value=0),
            patch.object(bootstrap, "_warm_cache") as warm,
        ):
            rc = bootstrap.main(["--upstream", "code"])
        return rc, warm

    def test_skipped_true_returns_without_warming(self) -> None:
        self.output_file.write_text(
            "reason=duplicate\nskipped=true\n", encoding="utf-8"
        )
        with patch.dict(
            os.environ, {"FULLSEND_PRESCRIPT_OUTPUT": str(self.output_file)}
        ):
            rc, warm = self._run_main()
        self.assertEqual(0, rc)
        warm.assert_not_called()

    def test_other_output_still_warms(self) -> None:
        self.output_file.write_text("skipped=false\nreason=nothing\n", encoding="utf-8")
        with patch.dict(
            os.environ, {"FULLSEND_PRESCRIPT_OUTPUT": str(self.output_file)}
        ):
            rc, warm = self._run_main()
        self.assertEqual(0, rc)
        warm.assert_called_once()

    def test_unset_variable_still_warms(self) -> None:
        env = dict(os.environ)
        env.pop("FULLSEND_PRESCRIPT_OUTPUT", None)
        with patch.dict(os.environ, env, clear=True):
            rc, warm = self._run_main()
        self.assertEqual(0, rc)
        warm.assert_called_once()


class WorkflowCommandTests(unittest.TestCase):
    """The fail-soft warning is a single sanitized workflow command."""

    def test_sanitize_workflow_command(self) -> None:
        dirty = "line1\r\nline2\ttab ::error:: injected\x1b[31m 100%"
        clean = bootstrap._sanitize_workflow_command(dirty)
        # One line, no control characters, whitespace collapsed.
        for control in ("\r", "\n", "\t", "\x1b"):
            self.assertNotIn(control, clean)
        # Percent is escaped so GitHub cannot expand encoded data.
        self.assertTrue(clean.endswith("100%25"))
        self.assertNotIn("%", clean.replace("%25", ""))
        # ``::`` cannot start a new command once the line is single-line.
        self.assertIn("::error::", clean)

    def test_sanitize_workflow_command_caps_the_length(self) -> None:
        clean = bootstrap._sanitize_workflow_command("x" * 600)
        self.assertLessEqual(len(clean), bootstrap._WORKFLOW_COMMAND_CAP)

    def test_fail_soft_warning_is_sanitized_with_plain_detail(self) -> None:
        failure = RuntimeError("boom\nsecond line ::error:: 100% done")
        with (
            patch.object(bootstrap, "_run_upstream_script", return_value=0),
            patch.object(bootstrap, "_warm_cache", side_effect=failure),
        ):
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                rc = bootstrap.main(["--upstream", "code"])
        self.assertEqual(0, rc)
        output = stdout.getvalue()
        warning_lines = [
            line for line in output.splitlines() if line.startswith("::warning::")
        ]
        self.assertEqual(1, len(warning_lines))
        self.assertIn("100%25", warning_lines[0])
        self.assertNotIn("%", warning_lines[0].replace("%25", ""))
        # The unsanitized detail survives as indented plain lines.
        self.assertIn("  pre-commit cache bootstrap failed: boom", output)
        self.assertIn("  second line ::error:: 100% done", output)


class ResolvePreCommitTests(unittest.TestCase):
    def test_reinstalls_when_the_found_binary_reports_a_different_version(self):
        installed = {"done": False}

        def fake_run(argv, *, env=None, timeout=None, capture=False, cwd=None):
            if "--version" in argv:
                version = bootstrap.PRECOMMIT_VERSION if installed["done"] else "0.0.0"
                return _completed(stdout=f"pre-commit {version}\n")
            if "pip" in argv:
                installed["done"] = True
            return _completed()

        with patch.object(bootstrap, "_run", side_effect=fake_run) as run:
            binary = bootstrap._resolve_pre_commit()
        self.assertEqual("pre-commit", binary)
        self.assertTrue(installed["done"])
        pip_calls = [
            call.args[0]
            for call in run.call_args_list
            if "pip" in call.args[0] and "install" in call.args[0]
        ]
        self.assertEqual(1, len(pip_calls))
        self.assertIn(f"pre-commit=={bootstrap.PRECOMMIT_VERSION}", pip_calls[0])


class TargetRepoTests(unittest.TestCase):
    def test_target_repo_dir_wins_when_it_has_a_config(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        target = Path(temporary_directory.name)
        (target / ".pre-commit-config.yaml").write_text("repos: []\n", encoding="utf-8")
        with patch.dict(os.environ, {"TARGET_REPO_DIR": str(target)}):
            self.assertEqual(target, bootstrap._target_repo())

    def test_falls_back_to_this_checkout(self) -> None:
        env = dict(os.environ)
        env.pop("TARGET_REPO_DIR", None)
        with patch.dict(os.environ, env, clear=True):
            self.assertEqual(bootstrap.REPO_ROOT, bootstrap._target_repo())


class WrapperScriptTests(unittest.TestCase):
    """The pre_script wrappers exist and delegate with the right upstream."""

    def test_wrappers_are_executable_and_pass_the_upstream_choice(self) -> None:
        for filename, upstream in (
            ("pre-code-cache.sh", "code"),
            ("pre-fix-cache.sh", "fix"),
        ):
            with self.subTest(wrapper=filename):
                path = REPO_ROOT / ".fullsend" / "scripts" / filename
                self.assertTrue(path.is_file(), f"{path} does not exist")
                self.assertTrue(os.access(path, os.X_OK), f"{path} is not executable")
                content = path.read_text(encoding="utf-8")
                self.assertIn("scripts/pre_commit_cache.py", content)
                self.assertIn(f"--upstream {upstream}", content)


class PinConsistencyTests(unittest.TestCase):
    """The agents ref in this module matches every harness ``base:`` pin."""

    def test_agents_ref_matches_every_harness_base_pin(self) -> None:
        harness_directory = REPO_ROOT / ".fullsend" / "harness"
        files = sorted(
            [*harness_directory.glob("*.yaml"), *harness_directory.glob("*.yml")],
        )
        self.assertTrue(files, "no harness files found")
        for path in files:
            with self.subTest(path=path.name):
                match = _BASE_PIN.search(path.read_text(encoding="utf-8"))
                self.assertIsNotNone(match, f"{path} has no pinned base URL")
                self.assertEqual(
                    f"v{match.group('version')}",
                    bootstrap.AGENTS_REF,
                    f"{path} pins a different fullsend-ai/agents ref",
                )


class PathContractTests(unittest.TestCase):
    """The runner and the sandbox must agree on the cache paths."""

    def test_harness_paths_match_the_script_constants(self) -> None:
        for name in ("code.yaml", "fix.yaml"):
            with self.subTest(harness=name):
                harness = yaml.safe_load(
                    (REPO_ROOT / ".fullsend" / "harness" / name).read_text(
                        encoding="utf-8"
                    )
                )
                entries = [
                    entry
                    for entry in harness["host_files"]
                    if entry["dest"].endswith("pre-commit-cache.tar.gz")
                ]
                self.assertEqual(1, len(entries), f"{name} needs the tarball entry")
                entry = entries[0]
                self.assertEqual(bootstrap.DEFAULT_TARBALL, entry["src"])
                self.assertEqual(
                    str(lint_wrapper._DEFAULT_CACHE_TARBALL), entry["dest"]
                )
                self.assertTrue(entry["optional"])
                self.assertEqual(
                    bootstrap.DEFAULT_CACHE_HOME,
                    harness["env"]["sandbox"]["PRE_COMMIT_HOME"],
                )


if __name__ == "__main__":
    unittest.main()
