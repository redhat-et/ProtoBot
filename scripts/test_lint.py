from __future__ import annotations

import contextlib
import io
import os
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

if __package__:
    from scripts import lint as checker
else:
    import lint as checker


def _git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo* and fail the test on a non-zero exit."""
    return subprocess.run(
        ["git", *args],
        cwd=str(repo),
        capture_output=True,
        text=True,
        check=True,
    )


def _init_repo(repo: Path) -> None:
    _git(repo, "init", "-b", "main")
    _git(repo, "config", "user.email", "lint@example.com")
    _git(repo, "config", "user.name", "Lint Test")
    _git(repo, "config", "commit.gpgsign", "false")


def _make_db(db_path: Path, rows: list[tuple[str, str]]) -> None:
    """Create a pre-commit-style ``db.db`` with *rows* of ``(repo, ref)``."""
    db_path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(str(db_path))
    try:
        conn.executescript(
            "CREATE TABLE repos ("
            "    repo TEXT NOT NULL,"
            "    ref TEXT NOT NULL,"
            "    path TEXT NOT NULL,"
            "    PRIMARY KEY (repo, ref)"
            ");",
        )
        conn.executemany(
            "INSERT INTO repos (repo, ref, path) VALUES (?, ?, 'unused')",
            rows,
        )
        conn.commit()
    finally:
        conn.close()


_CONFIG_TWO_REMOTE_ONE_LOCAL: dict[str, object] = {
    "repos": [
        {
            "repo": "https://example.invalid/foo",
            "rev": "aaaa",
            "hooks": [{"id": "foo"}],
        },
        {
            "repo": "https://example.invalid/bar",
            "rev": "bbbb",
            "hooks": [{"id": "bar"}],
        },
        {
            "repo": "local",
            "hooks": [{"id": "always", "always_run": True}],
        },
        {
            "repo": "meta",
            "hooks": [{"id": "check-hooks-apply"}],
        },
    ],
}


class CiIsEnabledTests(unittest.TestCase):
    def test_true_when_ci_true(self) -> None:
        with patch.dict(os.environ, {"CI": "true"}, clear=False):
            self.assertTrue(checker._ci_is_enabled())

    def test_case_insensitive(self) -> None:
        with patch.dict(os.environ, {"CI": "True"}, clear=False):
            self.assertTrue(checker._ci_is_enabled())

    def test_false_when_unset(self) -> None:
        env = dict(os.environ)
        env.pop("CI", None)
        with patch.dict(os.environ, env, clear=True):
            self.assertFalse(checker._ci_is_enabled())

    def test_false_for_other_values(self) -> None:
        with patch.dict(os.environ, {"CI": "1"}, clear=False):
            self.assertFalse(checker._ci_is_enabled())


class PreCommitHomeTests(unittest.TestCase):
    def test_uses_pre_commit_home_when_set(self) -> None:
        with patch.dict(os.environ, {"PRE_COMMIT_HOME": "/tmp/explicit-home"}):
            self.assertEqual(Path("/tmp/explicit-home"), checker._pre_commit_home())

    def test_falls_back_to_xdg_cache_home(self) -> None:
        env = dict(os.environ)
        env.pop("PRE_COMMIT_HOME", None)
        env["XDG_CACHE_HOME"] = "/tmp/xdg-cache"
        with patch.dict(os.environ, env, clear=True):
            self.assertEqual(
                Path("/tmp/xdg-cache/pre-commit"), checker._pre_commit_home()
            )

    def test_falls_back_to_home_cache(self) -> None:
        env = dict(os.environ)
        env.pop("PRE_COMMIT_HOME", None)
        env.pop("XDG_CACHE_HOME", None)
        env["HOME"] = "/tmp/home"
        with patch.dict(os.environ, env, clear=True):
            self.assertEqual(
                Path("/tmp/home/.cache/pre-commit"), checker._pre_commit_home()
            )


class ConfiguredReposTests(unittest.TestCase):
    def test_skips_local_and_meta(self) -> None:
        repos = checker._configured_repos(_CONFIG_TWO_REMOTE_ONE_LOCAL)
        self.assertEqual(
            [
                ("https://example.invalid/foo", "aaaa"),
                ("https://example.invalid/bar", "bbbb"),
            ],
            repos,
        )

    def test_empty_config(self) -> None:
        self.assertEqual([], checker._configured_repos({}))


class AnyAlwaysRunTests(unittest.TestCase):
    def test_true_when_a_hook_declares_always_run(self) -> None:
        self.assertTrue(checker._any_always_run(_CONFIG_TWO_REMOTE_ONE_LOCAL))

    def test_false_when_no_hook_declares_it(self) -> None:
        config = {"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]}
        self.assertFalse(checker._any_always_run(config))


class CachedReposTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.home = Path(temporary_directory.name)

    def test_missing_db_returns_empty_set(self) -> None:
        self.assertEqual(set(), checker._cached_repos(self.home))

    def test_reads_rows_from_real_schema(self) -> None:
        _make_db(
            self.home / "db.db",
            [("https://example.invalid/foo", "aaaa")],
        )
        self.assertEqual(
            {("https://example.invalid/foo", "aaaa")},
            checker._cached_repos(self.home),
        )

    def test_malformed_db_returns_empty_set(self) -> None:
        db_path = self.home / "db.db"
        db_path.parent.mkdir(parents=True, exist_ok=True)
        db_path.write_text("not a sqlite database", encoding="utf-8")
        self.assertEqual(set(), checker._cached_repos(self.home))


class MissingReposTests(unittest.TestCase):
    def test_returns_only_uncached_pairs(self) -> None:
        configured = [("a", "1"), ("b", "2"), ("c", "3")]
        cached = {("a", "1"), ("c", "3")}
        self.assertEqual([("b", "2")], checker._missing_repos(configured, cached))

    def test_empty_when_fully_cached(self) -> None:
        configured = [("a", "1")]
        cached = {("a", "1")}
        self.assertEqual([], checker._missing_repos(configured, cached))


class PreflightTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.home = Path(temporary_directory.name)

    def test_online_skips_the_cache_check_entirely(self) -> None:
        # No db.db exists at all; --online must still report success.
        rc = checker._preflight(True, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL)
        self.assertEqual(0, rc)

    def test_offline_fails_closed_on_empty_cache(self) -> None:
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL)
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertIn("https://example.invalid/bar @ bbbb", message)
        self.assertIn("pre-commit install-hooks", message)
        self.assertIn(str(self.home), message)

    def test_offline_passes_when_cache_is_warm(self) -> None:
        _make_db(
            self.home / "db.db",
            [
                ("https://example.invalid/foo", "aaaa"),
                ("https://example.invalid/bar", "bbbb"),
            ],
        )
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL)
        self.assertEqual(0, rc)
        self.assertEqual("", buf.getvalue())

    def test_offline_reports_only_the_missing_repo(self) -> None:
        _make_db(self.home / "db.db", [("https://example.invalid/foo", "aaaa")])
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL)
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertNotIn("example.invalid/foo @ aaaa", message)
        self.assertIn("https://example.invalid/bar @ bbbb", message)


class ChangedFilesTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        _init_repo(self.root)
        (self.root / "committed.txt").write_text("one\n", encoding="utf-8")
        _git(self.root, "add", "committed.txt")
        _git(self.root, "commit", "-m", "initial")

    def test_includes_unstaged_edits(self) -> None:
        (self.root / "committed.txt").write_text("two\n", encoding="utf-8")
        with patch.object(checker, "REPO_ROOT", self.root):
            self.assertEqual(["committed.txt"], checker._changed_files())

    def test_includes_staged_additions(self) -> None:
        (self.root / "new.txt").write_text("new\n", encoding="utf-8")
        _git(self.root, "add", "new.txt")
        with patch.object(checker, "REPO_ROOT", self.root):
            self.assertEqual(["new.txt"], checker._changed_files())

    def test_excludes_deleted_paths(self) -> None:
        (self.root / "committed.txt").unlink()
        with patch.object(checker, "REPO_ROOT", self.root):
            self.assertEqual([], checker._changed_files())

    def test_no_changes_returns_empty_list(self) -> None:
        with patch.object(checker, "REPO_ROOT", self.root):
            self.assertEqual([], checker._changed_files())


class MainCliMappingTests(unittest.TestCase):
    """``main()`` translates flags into a ``pre-commit run`` invocation.

    Every case below is already cached (satisfied pre-flight) so these
    tests exercise CLI translation, not the cache gate covered by
    ``PreflightTests``.
    """

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.home = Path(temporary_directory.name) / "cache"
        _make_db(self.home / "db.db", [])
        env_patcher = patch.dict(
            os.environ,
            {"PRE_COMMIT_HOME": str(self.home)},
            clear=False,
        )
        env_patcher.start()
        self.addCleanup(env_patcher.stop)
        # Config has no cloned repos, so the cache is trivially satisfied.
        config_patcher = patch.object(
            checker,
            "_load_config",
            return_value={"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]},
        )
        config_patcher.start()
        self.addCleanup(config_patcher.stop)

    def test_all_files_maps_to_pre_commit_all_files(self) -> None:
        with patch("os.execvp") as mock_execvp:
            checker.main(["--all-files"])
        mock_execvp.assert_called_once_with(
            "pre-commit", ["pre-commit", "run", "--all-files"]
        )

    def test_files_maps_to_pre_commit_files(self) -> None:
        with patch("os.execvp") as mock_execvp:
            checker.main(["--files", "a.py", "b.yaml"])
        mock_execvp.assert_called_once_with(
            "pre-commit", ["pre-commit", "run", "--files", "a.py", "b.yaml"]
        )

    def test_bare_mode_maps_to_changed_files(self) -> None:
        with (
            patch.object(checker, "_changed_files", return_value=["x.py"]),
            patch("os.execvp") as mock_execvp,
        ):
            checker.main([])
        mock_execvp.assert_called_once_with(
            "pre-commit", ["pre-commit", "run", "--files", "x.py"]
        )

    def test_bare_mode_with_no_changes_and_no_always_run_skips_pre_commit(
        self,
    ) -> None:
        with (
            patch.object(checker, "_changed_files", return_value=[]),
            patch("os.execvp") as mock_execvp,
        ):
            rc = checker.main([])
        mock_execvp.assert_not_called()
        self.assertEqual(0, rc)

    def test_bare_mode_with_no_changes_but_always_run_hook_still_runs(self) -> None:
        always_run_config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [{"id": "x", "always_run": True}],
                }
            ],
        }
        with (
            patch.object(checker, "_load_config", return_value=always_run_config),
            patch.object(checker, "_changed_files", return_value=[]),
            patch("os.execvp") as mock_execvp,
        ):
            checker.main([])
        mock_execvp.assert_called_once_with(
            "pre-commit", ["pre-commit", "run", "--files"]
        )


class MainEnvironmentTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.home = Path(temporary_directory.name) / "cache"
        config_patcher = patch.object(
            checker,
            "_load_config",
            return_value={"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]},
        )
        config_patcher.start()
        self.addCleanup(config_patcher.stop)

    def _clean_env(self) -> dict[str, str]:
        env = dict(os.environ)
        env.pop("PRE_COMMIT_HOME", None)
        env.pop("ZIZMOR_NO_ONLINE_AUDITS", None)
        env.pop("CI", None)
        return env

    def test_offline_sets_pre_commit_home_and_zizmor_flag(self) -> None:
        with (
            patch.dict(os.environ, self._clean_env(), clear=True),
            patch("os.execvp"),
        ):
            os.environ["PRE_COMMIT_HOME"] = str(self.home)
            checker.main(["--all-files"])
            self.assertEqual("true", os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))

    def test_online_does_not_force_zizmor_offline(self) -> None:
        with (
            patch.dict(os.environ, self._clean_env(), clear=True),
            patch("os.execvp"),
        ):
            os.environ["PRE_COMMIT_HOME"] = str(self.home)
            checker.main(["--all-files", "--online"])
            self.assertNotIn("ZIZMOR_NO_ONLINE_AUDITS", os.environ)

    def test_zizmor_flag_is_not_overridden_when_caller_set_it(self) -> None:
        with (
            patch.dict(os.environ, self._clean_env(), clear=True),
            patch("os.execvp"),
        ):
            os.environ["PRE_COMMIT_HOME"] = str(self.home)
            os.environ["ZIZMOR_NO_ONLINE_AUDITS"] = "false"
            checker.main(["--all-files"])
            self.assertEqual("false", os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))


class MainCheckParityTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.home = Path(temporary_directory.name) / "cache"
        env_patcher = patch.dict(
            os.environ,
            {"PRE_COMMIT_HOME": str(self.home)},
            clear=False,
        )
        env_patcher.start()
        self.addCleanup(env_patcher.stop)

    def test_cache_miss_reports_before_validating_config(self) -> None:
        with (
            patch.object(
                checker,
                "_load_config",
                return_value=_CONFIG_TWO_REMOTE_ONE_LOCAL,
            ),
            patch("subprocess.run") as mock_run,
        ):
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--check-parity"])
        mock_run.assert_not_called()
        self.assertEqual(1, rc)
        self.assertIn("pre-commit install-hooks", buf.getvalue())

    def test_cache_hit_runs_validate_config(self) -> None:
        _make_db(self.home / "db.db", [])
        with (
            patch.object(
                checker,
                "_load_config",
                return_value={"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]},
            ),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(0, rc)
        mock_run.assert_called_once_with(
            ["pre-commit", "validate-config", str(checker.CONFIG_PATH)],
            cwd=str(checker.REPO_ROOT),
            check=False,
        )
        self.assertIn("PARITY CHECK PASSED", buf.getvalue())

    def test_validate_config_failure_propagates_exit_code(self) -> None:
        _make_db(self.home / "db.db", [])
        with (
            patch.object(
                checker,
                "_load_config",
                return_value={"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]},
            ),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=1)
            rc = checker.main(["--check-parity"])
        self.assertEqual(1, rc)

    def test_online_check_parity_skips_cache_but_still_validates(self) -> None:
        # No db.db at all; --online must bypass the cache gate.
        with (
            patch.object(
                checker,
                "_load_config",
                return_value=_CONFIG_TWO_REMOTE_ONE_LOCAL,
            ),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main(["--check-parity", "--online"])
        self.assertEqual(0, rc)
        mock_run.assert_called_once()


if __name__ == "__main__":
    unittest.main()
