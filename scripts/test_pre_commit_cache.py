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
    """Stand-in for ``_run``: version probes match the pin, all else is 0."""
    if "--version" in argv:
        return _completed(stdout=f"pre-commit {bootstrap.PRECOMMIT_VERSION}\n")
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

    def _patch_opener(self, data: bytes, url: str = _URL) -> None:
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
        # ``-C <cache home> .`` archives the cache home's contents, so the
        # restore unpacks them directly into PRE_COMMIT_HOME.
        with patch.object(bootstrap, "_run", side_effect=_fake_run_succeeds) as run:
            bootstrap._warm_cache(cache_home, tarball)
        tar_calls = [
            call.args[0] for call in run.call_args_list if call.args[0][0] == "tar"
        ]
        self.assertEqual(
            [["tar", "-czf", str(tarball), "-C", str(cache_home), "."]],
            tar_calls,
        )

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
            bootstrap._warm_cache(cache_home, root / "out.tar.gz")
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
