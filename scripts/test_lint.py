from __future__ import annotations

import contextlib
import io
import os
import re
import sqlite3
import subprocess
import sys
import tarfile
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


class PreflightRestoreTests(unittest.TestCase):
    """The pre-flight restores a cold cache from the bootstrap tarball."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        self.home = self.root / "cache"
        self.tarball = self.root / "pre-commit-cache.tar.gz"
        env_patcher = patch.dict(
            os.environ,
            {"PROTOBOT_PRE_COMMIT_CACHE_TARBALL": str(self.tarball)},
            clear=False,
        )
        env_patcher.start()
        self.addCleanup(env_patcher.stop)

    def _current_interpreter(self) -> str:
        return f"{sys.version_info.major}.{sys.version_info.minor}"

    def _build_tarball(
        self,
        rows: list[tuple[str, str]],
        *,
        other_interpreters: tuple[str, ...] = (),
    ) -> None:
        """Write a bootstrap tarball holding a cache with *rows* cached.

        The fixture mirrors the real layout: a ``db.db`` at the archive
        root and per-repo ``py_env-python<X.Y>`` virtualenvs whose
        ``bin/python`` is an absolute symlink.
        """
        packed = self.root / "packed"
        _make_db(packed / "db.db", rows)
        versions = (self._current_interpreter(), *other_interpreters)
        for version in versions:
            bin_directory = packed / "repodir" / f"py_env-python{version}" / "bin"
            bin_directory.mkdir(parents=True, exist_ok=True)
            os.symlink("/usr/bin/python3", bin_directory / "python")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                # recursive=False: rglob already lists every entry, and
                # tar.add's own recursion would write duplicate members.
                tar.add(path, arcname=path.relative_to(packed), recursive=False)

    def _config_rows(self) -> list[tuple[str, str]]:
        return [
            ("https://example.invalid/foo", "aaaa"),
            ("https://example.invalid/bar", "bbbb"),
        ]

    def test_cold_cache_with_valid_tarball_restores_and_passes(self) -> None:
        self._build_tarball(self._config_rows())
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        message = stdout.getvalue()
        self.assertIn("Restored the offline pre-commit cache", message)
        self.assertIn(str(self.tarball), message)
        self.assertIn(str(self.home), message)
        # The tarball is deleted after a successful restore.
        self.assertFalse(self.tarball.exists())
        # No staging directory leaked beside the cache home.
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.restore-*")))
        # The restored db.db satisfies the pre-flight.
        self.assertTrue((self.home / "db.db").is_file())
        # The cache's absolute symlink survives the restore.
        link = (
            self.home
            / "repodir"
            / f"py_env-python{self._current_interpreter()}"
            / "bin"
            / "python"
        )
        self.assertTrue(link.is_symlink())
        self.assertEqual("/usr/bin/python3", os.readlink(link))
        # No interpreter warning for a matching cache.
        self.assertEqual("", stderr.getvalue())

    def test_no_tarball_keeps_the_existing_error(self) -> None:
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(1, rc)
        message = stderr.getvalue()
        self.assertIn("missing 2 hook repository revisions", message)
        self.assertIn("pre-commit install-hooks", message)

    def test_corrupt_tarball_warns_and_returns_missing_repositories(self) -> None:
        for content in (b"this is not a tarball", b"\x1f\x8btruncated-gzip"):
            with self.subTest(content=content):
                self.tarball.write_bytes(content)
                stdout, stderr = io.StringIO(), io.StringIO()
                with (
                    contextlib.redirect_stdout(stdout),
                    contextlib.redirect_stderr(stderr),
                ):
                    rc = checker._preflight_with_restore(
                        False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                    )
                self.assertEqual(1, rc)
                message = stderr.getvalue()
                self.assertIn("cannot restore the pre-commit cache", message)
                self.assertIn("missing 2 hook repository revisions", message)
                # A failed restore keeps the tarball and prints no restore line.
                self.assertTrue(self.tarball.exists())
                self.assertEqual("", stdout.getvalue())

    def test_online_never_restores(self) -> None:
        self._build_tarball(self._config_rows())
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            rc = checker._preflight_with_restore(
                True, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        # The tarball is untouched and no restore is reported.
        self.assertTrue(self.tarball.exists())
        self.assertNotIn("Restored", stdout.getvalue())

    def test_other_interpreter_py_env_warns(self) -> None:
        self._build_tarball(self._config_rows(), other_interpreters=("1.2",))
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("built for another interpreter", message)
        self.assertIn("py_env-python1.2", message)

    def test_members_that_escape_the_destination_are_skipped(self) -> None:
        payload = io.BytesIO(b"data")
        with tarfile.open(self.tarball, "w:gz") as tar:
            link = tarfile.TarInfo("repodir/escape")
            link.type = tarfile.SYMTYPE
            link.linkname = str(self.root / "outside")
            tar.addfile(link)
            member = tarfile.TarInfo("repodir/escape/payload.txt")
            member.size = len(payload.getbuffer())
            tar.addfile(member, payload)
            escape = tarfile.TarInfo("../escape.txt")
            escape.size = 0
            tar.addfile(escape)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        # No file landed outside the cache home, the skips are counted and
        # warned about, and the tarball is kept.
        self.assertFalse((self.root / "outside" / "payload.txt").exists())
        self.assertFalse((self.root / "escape.txt").exists())
        self.assertEqual(1, rc)
        self.assertIn("skipped 2 tarball member(s)", stderr.getvalue())
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_restore_that_does_not_fix_the_cache_keeps_the_tarball(self) -> None:
        # The tarball restores but holds none of the configured rows, so
        # the pre-flight still fails and the tarball must be kept.
        self._build_tarball([("https://example.invalid/other", "zzzz")])
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(1, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertIn("missing 2 hook repository revisions", stderr.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_hardlink_with_escaping_target_is_skipped(self) -> None:
        # A hard link whose target escapes the destination is not checked
        # by the tar filter. Extracted, a later regular file of the same
        # name would write through it and modify the outside file.
        outside = self.root / "outside-target"
        outside.write_bytes(b"original")
        payload = io.BytesIO(b"attacker data")
        with tarfile.open(self.tarball, "w:gz") as tar:
            link = tarfile.TarInfo("shared")
            link.type = tarfile.LNKTYPE
            link.linkname = "../outside-target"
            tar.addfile(link)
            member = tarfile.TarInfo("shared")
            member.size = len(payload.getbuffer())
            tar.addfile(member, payload)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(1, rc)
        # The outside file is unchanged and the skip is counted.
        self.assertEqual(b"original", outside.read_bytes())
        self.assertIn("skipped 1 tarball member(s)", stderr.getvalue())
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_size_cap_after_db_leaves_no_readable_db(self) -> None:
        # db.db must not survive a size-capped extraction: a later run
        # must retry the restore instead of trusting a partial cache.
        packed = self.root / "packed"
        _make_db(packed / "db.db", self._config_rows())
        (packed / "filler.bin").write_bytes(b"x" * 1024)
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                # recursive=False: rglob already lists every entry.
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        db_size = (packed / "db.db").stat().st_size
        with patch.object(checker, "_MAX_EXTRACTED_BYTES", db_size):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(1, rc)
        self.assertIn("extraction stopped early", stderr.getvalue())
        self.assertTrue(self.tarball.exists())
        # No partial cache was published and no staging directory leaked.
        self.assertFalse((self.home / "db.db").exists())
        self.assertEqual(set(), checker._cached_repos(self.home))
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.restore-*")))

    def test_tarball_outside_the_delete_root_is_never_deleted(self) -> None:
        self._build_tarball(self._config_rows())
        with patch.object(checker, "_TARBALL_DELETE_ROOT", self.root / "nowhere"):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertIn("only tarballs under", stderr.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_fallback_extraction_when_filter_kwarg_is_rejected(self) -> None:
        self._build_tarball(self._config_rows())
        original_extractall = tarfile.TarFile.extractall

        def extractall_without_filter(
            tar,
            path=".",
            members=None,
            *,
            numeric_owner=False,
            filter=None,
        ):
            if filter is not None:
                raise TypeError(
                    "extractall() got an unexpected keyword argument 'filter'"
                )
            return original_extractall(
                tar, path, members=members, numeric_owner=numeric_owner
            )

        stdout = io.StringIO()
        with (
            patch.object(tarfile.TarFile, "extractall", extractall_without_filter),
            contextlib.redirect_stdout(stdout),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertFalse(self.tarball.exists())

    def test_run_path_flushes_the_restore_line_before_exec(self) -> None:
        # os.execvp replaces the process without flushing Python's
        # buffers. A block-buffered stdout, as under a shell redirect,
        # would lose the restore line without an explicit flush.
        self._build_tarball(self._config_rows())
        raw = io.BytesIO()
        buffered = io.TextIOWrapper(raw, encoding="utf-8")
        with (
            patch.dict(os.environ, {"PRE_COMMIT_HOME": str(self.home)}, clear=False),
            patch.object(
                checker,
                "_load_config",
                return_value=_CONFIG_TWO_REMOTE_ONE_LOCAL,
            ),
            patch.object(checker, "_changed_files", return_value=["README.md"]),
            patch("os.execvp") as mock_execvp,
            patch("sys.stdout", buffered),
        ):
            checker.main(["--files", "README.md"])
        mock_execvp.assert_called_once_with(
            "pre-commit", ["pre-commit", "run", "--files", "README.md"]
        )
        self.assertIn(
            "Restored the offline pre-commit cache",
            raw.getvalue().decode("utf-8"),
        )


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
        # Force offline mode: these tests must not change their path
        # when GitHub Actions sets CI=true.
        ci_patcher = patch.object(checker, "_ci_is_enabled", return_value=False)
        ci_patcher.start()
        self.addCleanup(ci_patcher.stop)
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

    def test_offline_bare_mode_reads_config_once(self) -> None:
        # Regression test: the offline pre-flight and bare mode's
        # always_run check must share one config read, not two. The
        # test forces offline mode and stubs the pre-flight, so it
        # runs the same way with and without CI=true and never reads
        # the hook cache.
        config = {"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]}
        with (
            patch.object(checker, "_ci_is_enabled", return_value=False),
            patch.object(
                checker, "_load_config", return_value=config
            ) as mock_load_config,
            patch.object(checker, "_preflight", return_value=0) as mock_preflight,
            patch.object(checker, "_changed_files", return_value=[]),
            patch("os.execvp") as mock_execvp,
        ):
            rc = checker.main([])
        mock_load_config.assert_called_once()
        mock_preflight.assert_called_once()
        # The pre-flight ran offline and received the same config
        # object that _load_config returned: bare mode reused the
        # shared read instead of loading the config again.
        self.assertFalse(mock_preflight.call_args[0][0])
        self.assertIs(config, mock_preflight.call_args[0][2])
        mock_execvp.assert_not_called()
        self.assertEqual(0, rc)


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
        self.root = Path(temporary_directory.name)
        self.home = self.root / "cache"
        # Pin the bootstrap tarball to a path that does not exist, so a
        # tarball left on the host cannot change these cases.
        env_patcher = patch.dict(
            os.environ,
            {
                "PRE_COMMIT_HOME": str(self.home),
                "PROTOBOT_PRE_COMMIT_CACHE_TARBALL": str(self.root / "absent.tar.gz"),
            },
            clear=False,
        )
        env_patcher.start()
        self.addCleanup(env_patcher.stop)
        # Force offline mode: the cache-gate tests must not change
        # their path when GitHub Actions sets CI=true.
        ci_patcher = patch.object(checker, "_ci_is_enabled", return_value=False)
        ci_patcher.start()
        self.addCleanup(ci_patcher.stop)

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

    def test_cold_cache_with_tarball_restores_then_validates(self) -> None:
        rows = [
            ("https://example.invalid/foo", "aaaa"),
            ("https://example.invalid/bar", "bbbb"),
        ]
        tarball = self.root / "pre-commit-cache.tar.gz"
        packed = self.root / "packed"
        _make_db(packed / "db.db", rows)
        with tarfile.open(tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        with (
            patch.dict(
                os.environ,
                {"PROTOBOT_PRE_COMMIT_CACHE_TARBALL": str(tarball)},
                clear=False,
            ),
            patch.object(
                checker,
                "_load_config",
                return_value=_CONFIG_TWO_REMOTE_ONE_LOCAL,
            ),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(0, rc)
        mock_run.assert_called_once()
        self.assertIn("Restored the offline pre-commit cache", buf.getvalue())
        self.assertIn("PARITY CHECK PASSED", buf.getvalue())
        self.assertFalse(tarball.exists())

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


class PrintPreCommitVersionTests(unittest.TestCase):
    def test_prints_pinned_version_without_reading_config(self) -> None:
        def _fail_if_called() -> None:
            self.fail("--print-pre-commit-version must not read the config")

        buf = io.StringIO()
        with (
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("os.execvp") as mock_execvp,
            contextlib.redirect_stdout(buf),
        ):
            rc = checker.main(["--print-pre-commit-version"])
        self.assertEqual(0, rc)
        self.assertRegex(checker.PRE_COMMIT_VERSION, r"^\d+\.\d+\.\d+$")
        self.assertEqual(f"{checker.PRE_COMMIT_VERSION}\n", buf.getvalue())
        mock_execvp.assert_not_called()

    def test_print_version_rejects_mode_and_scope_flags(self) -> None:
        for flags in (
            ["--print-pre-commit-version", "--check-parity"],
            ["--print-pre-commit-version", "--all-files"],
            ["--print-pre-commit-version", "--files", "a.py"],
        ):
            with self.subTest(flags=flags):
                buf = io.StringIO()
                with (
                    contextlib.redirect_stderr(buf),
                    patch("os.execvp") as mock_execvp,
                    self.assertRaises(SystemExit) as raised,
                ):
                    checker.main(flags)
                self.assertEqual(2, raised.exception.code)
                mock_execvp.assert_not_called()
                self.assertIn("cannot be combined with", buf.getvalue())

    def test_print_version_ignores_online_flags(self) -> None:
        # The print branch returns before online mode is computed, so
        # --online-if-ci must not change its behavior. CI is unset
        # here: without the early return, --online-if-ci would resolve
        # offline and set the zizmor flag before printing.
        buf = io.StringIO()
        env = dict(os.environ)
        env.pop("CI", None)
        env.pop("ZIZMOR_NO_ONLINE_AUDITS", None)
        with (
            patch.dict(os.environ, env, clear=True),
            contextlib.redirect_stdout(buf),
            patch("os.execvp") as mock_execvp,
        ):
            rc = checker.main(["--print-pre-commit-version", "--online-if-ci"])
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        self.assertEqual(0, rc)
        self.assertEqual(f"{checker.PRE_COMMIT_VERSION}\n", buf.getvalue())
        mock_execvp.assert_not_called()


class MainCiInvocationTests(unittest.TestCase):
    """The CI invocation must work without reading the config.

    CI runs ``python3 scripts/lint.py --all-files --online-if-ci``
    under ``CI=true``. That path must not read
    ``.pre-commit-config.yaml`` (so it needs no PyYAML) and must not
    force zizmor's audits offline.
    """

    def test_ci_invocation_runs_pre_commit_without_config(self) -> None:
        def _fail_if_called() -> None:
            self.fail("the CI invocation must not read the config")

        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        env = dict(os.environ)
        env["CI"] = "true"
        env["PRE_COMMIT_HOME"] = str(Path(temporary_directory.name) / "cache")
        env.pop("ZIZMOR_NO_ONLINE_AUDITS", None)
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("os.execvp") as mock_execvp,
        ):
            checker.main(["--all-files", "--online-if-ci"])
            # Assert inside the patch: the wrapper must not set the
            # zizmor flag for this online run.
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        mock_execvp.assert_called_once_with(
            "pre-commit", ["pre-commit", "run", "--all-files"]
        )

    def test_ci_invocation_with_files_scope_runs_without_config(self) -> None:
        def _fail_if_called() -> None:
            self.fail("the CI invocation must not read the config")

        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        env = dict(os.environ)
        env["CI"] = "true"
        env["PRE_COMMIT_HOME"] = str(Path(temporary_directory.name) / "cache")
        env.pop("ZIZMOR_NO_ONLINE_AUDITS", None)
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("os.execvp") as mock_execvp,
        ):
            checker.main(["--online-if-ci", "--files", "a.py", "b.yaml"])
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        mock_execvp.assert_called_once_with(
            "pre-commit",
            ["pre-commit", "run", "--files", "a.py", "b.yaml"],
        )


# A `run:` key line. Group 1 is the indentation; group 2 is the
# inline value, if any.
_RUN_KEY_LINE = re.compile(r"^([ \t]*)run:(?:[ \t]+(.*))?$")

# A direct `pre-commit run` invocation: the `pre-commit` command or
# the `-m pre_commit` module form, each followed by a `run` token.
# The lookarounds match whole tokens only. `x-pre-commit` and
# `run-all` do not match.
_DIRECT_PRE_COMMIT_RUN = re.compile(
    r"(?<![\w.-])(?:pre-commit|-m[ \t]+pre_commit)[ \t]+run(?![\w-])"
)


def _strip_line_comment(line: str) -> str:
    """Return *line* cut at its first comment start.

    A ``#`` starts a comment when it begins the line or follows a
    space or tab outside quotes. A ``#`` inside a quoted string
    (``echo "a # b"``) is shell data and stays. A ``#`` glued to a
    scalar (``a#b``) is data and stays.
    """
    quote = ""
    for index, char in enumerate(line):
        if quote:
            if char == quote:
                quote = ""
        elif char in ("'", '"'):
            quote = char
        elif char == "#" and (index == 0 or line[index - 1] in " \t"):
            return line[:index]
    return line


def _without_yaml_comments(text: str) -> str:
    """Return *text* with the comment part of every line removed.

    Each line is cut at its first comment start, so a comment cannot
    hide a command from the contract guards: an unterminated quote
    keeps the rest of the line, which can only leave too much text,
    never too little.
    shortcut: escaped quotes toggle quote state, and a quote opened on
    one line does not carry to the next; upgrade to a real shell lexer
    if the workflow ever needs that precision.
    """
    return "\n".join(_strip_line_comment(line) for line in text.splitlines())


def _has_direct_pre_commit_run(text: str) -> bool:
    """Return whether *text* invokes ``pre-commit run`` directly.

    Comment parts are removed first, so an explanatory comment that
    names the old command does not count. The remaining text is
    searched for the ``pre-commit`` command or the ``-m pre_commit``
    module form followed by a ``run`` token, so prefixed and chained
    forms (``uv run pre-commit run -a``, ``python3 -m pre_commit run
    -a``, ``foo && pre-commit run``) are caught too.
    """
    return _DIRECT_PRE_COMMIT_RUN.search(_without_yaml_comments(text)) is not None


def _run_commands(text: str) -> list[str]:
    """Return the command lines of every ``run:`` value in *text*.

    Comment parts are removed first, so a comment that names a
    command does not count and a step name never becomes a command.
    The inline form (``run: <command>``) yields the command on the
    same line. The block form (``run: |`` or ``run: >``) yields every
    non-empty stripped line of the indented block that follows. The
    helper parses text only, so the tests need no PyYAML.
    """
    lines = _without_yaml_comments(text).splitlines()
    commands: list[str] = []
    index = 0
    while index < len(lines):
        key = _RUN_KEY_LINE.match(lines[index])
        if key is None:
            index += 1
            continue
        indent = key.group(1)
        value = key.group(2) or ""
        if value[:1] in ("|", ">"):
            index += 1
            while index < len(lines):
                block_line = lines[index]
                if block_line.strip() and not block_line.startswith(indent + " "):
                    break
                if block_line.strip():
                    commands.append(block_line.strip())
                index += 1
        else:
            if value.strip():
                commands.append(value.strip())
            index += 1
    return commands


class DirectPreCommitRunMatcherTests(unittest.TestCase):
    def test_detects_direct_invocations(self) -> None:
        for command in (
            "run: pre-commit run -a",
            "run: uv run pre-commit run -a",
            "run: python3 -m pre_commit run -a",
            "run: python -m pre_commit run -a",
            "echo x && python3 -m pre_commit run",
            "pre-commit run --all-files",
            "echo x && pre-commit run",
        ):
            with self.subTest(command=command):
                self.assertTrue(_has_direct_pre_commit_run(command))

    def test_ignores_the_wrapper_comments_and_other_subcommands(self) -> None:
        for line in (
            "run: python3 scripts/lint.py --all-files --online-if-ci",
            "# `pre-commit run -a` is no longer used",
            "run: pre-commit gc",
            "run: python3 -m pre_commit validate-config .pre-commit-config.yaml",
            "run: x-pre-commit run-all",
        ):
            with self.subTest(line=line):
                self.assertFalse(_has_direct_pre_commit_run(line))


class RunCommandsExtractionTests(unittest.TestCase):
    def test_extracts_inline_and_block_commands_and_drops_comments(self) -> None:
        snippet = (
            "      # run: echo not-a-command\n"
            "      - name: Run pre-commit checks\n"
            "        run: python3 scripts/lint.py --all-files --online-if-ci\n"
            "      - name: Install pre-commit\n"
            "        run: |\n"
            '          version="$(python3 scripts/lint.py'
            ' --print-pre-commit-version)"\n'
            '          # uv tool install "pre-commit==${version}"\n'
            '          uv tool install "pre-commit==${version}"\n'
            "      - name: Run pre-commit gc\n"
            "        run: pre-commit gc\n"
        )
        self.assertEqual(
            [
                "python3 scripts/lint.py --all-files --online-if-ci",
                'version="$(python3 scripts/lint.py --print-pre-commit-version)"',
                'uv tool install "pre-commit==${version}"',
                "pre-commit gc",
            ],
            _run_commands(snippet),
        )

    def test_step_name_is_not_extracted_as_a_command(self) -> None:
        snippet = "      - name: Run pre-commit gc\n        run: echo skipped\n"
        self.assertEqual(["echo skipped"], _run_commands(snippet))

    def test_quoted_hash_does_not_hide_a_direct_run(self) -> None:
        # A `#` inside quotes is shell data, not a comment. The
        # extracted command must stay whole so the matcher still
        # catches the direct run at its end.
        snippet = '        run: echo "a # b" && pre-commit run\n'
        commands = _run_commands(snippet)
        self.assertEqual(['echo "a # b" && pre-commit run'], commands)
        self.assertTrue(_has_direct_pre_commit_run(commands[0]))

    def test_comment_naming_the_wrapper_does_not_satisfy_the_contract(self) -> None:
        # The raw-text assertions passed on this snippet: the wrapper
        # command appears in a comment, and the executed command is a
        # module-form direct run.
        snippet = (
            "      # python3 scripts/lint.py --all-files --online-if-ci\n"
            "        run: python3 -m pre_commit run -a\n"
        )
        commands = _run_commands(snippet)
        self.assertNotIn("python3 scripts/lint.py --all-files --online-if-ci", commands)
        self.assertEqual(["python3 -m pre_commit run -a"], commands)
        self.assertTrue(_has_direct_pre_commit_run(commands[0]))


class CiWorkflowContractTests(unittest.TestCase):
    """The CI pre-commit job uses the wrapper as its lint entrypoint.

    Every assertion inspects the executed ``run:`` command lines, not
    the raw file text, so comments and step names cannot satisfy it.
    """

    def setUp(self) -> None:
        workflow_path = checker.REPO_ROOT / ".github" / "workflows" / "ci-workflow.yaml"
        self.commands = _run_commands(workflow_path.read_text(encoding="utf-8"))

    def test_no_direct_pre_commit_run_invocation(self) -> None:
        for command in self.commands:
            with self.subTest(command=command):
                self.assertFalse(_has_direct_pre_commit_run(command))

    def test_run_step_invokes_the_wrapper(self) -> None:
        self.assertIn(
            "python3 scripts/lint.py --all-files --online-if-ci",
            self.commands,
        )

    def test_install_step_pins_the_wrapper_version(self) -> None:
        self.assertIn(
            'version="$(python3 scripts/lint.py --print-pre-commit-version)"',
            self.commands,
        )
        self.assertIn('uv tool install "pre-commit==${version}"', self.commands)

    def test_pre_commit_gc_step_is_present(self) -> None:
        self.assertIn("pre-commit gc", self.commands)


if __name__ == "__main__":
    unittest.main()
