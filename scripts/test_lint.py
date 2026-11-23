from __future__ import annotations

import contextlib
import io
import json
import os
import re
import shlex
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zlib
from pathlib import Path
from unittest.mock import patch

if __package__:
    from scripts import lint as checker
else:
    import lint as checker


# env_specs sentinel: link an environment's bin/python to the running
# interpreter, as a real virtualenv links to its base interpreter.
_RUNTIME = "runtime"


def _expected_python_env_name() -> str:
    """Return pre-commit's python environment suffix, computed independently."""
    try:
        from pre_commit.languages.python import get_default_version
    except ImportError:
        return f"python{sys.version_info.major}.{sys.version_info.minor}"
    return get_default_version()


def _add_environment_health(environment_path: Path) -> None:
    """Add the pyvenv.cfg and bin/python pre-commit's health check requires."""
    environment_path.mkdir(parents=True, exist_ok=True)
    (environment_path / "pyvenv.cfg").write_text(
        f"version_info = {sys.version_info.major}.{sys.version_info.minor}."
        f"{sys.version_info.micro}.final.0\n",
        encoding="utf-8",
    )
    bin_dir = environment_path / "bin"
    bin_dir.mkdir(exist_ok=True)
    os.symlink(sys.executable, bin_dir / "python")


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


def _make_db(
    db_path: Path,
    rows: list[tuple[str, str] | tuple[str, str, str]],
    paths: dict[tuple[str, str], str | bytes] | None = None,
) -> None:
    """Create a pre-commit-style ``db.db`` with *rows* of ``(repo, ref)``.

    *paths* maps a row to its build-time repository directory; rows
    without an entry use a three-tuple's path, or ``unused``. A ``bytes``
    value is stored as a BLOB.
    """
    db_path.parent.mkdir(parents=True, exist_ok=True)
    normalized_rows = []
    for row in rows:
        repo, ref = row[:2]
        path = row[2] if len(row) == 3 else (paths or {}).get((repo, ref), "unused")
        normalized_rows.append((repo, ref, path))
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
            "INSERT INTO repos (repo, ref, path) VALUES (?, ?, ?)",
            normalized_rows,
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

_CONFIG_LOCAL_DEPENDENCIES: dict[str, object] = {
    "repos": [
        {
            "repo": "local",
            "hooks": [
                {
                    "id": "dependency-hook",
                    "language": "python",
                    "additional_dependencies": ["pyyaml==6.0.3"],
                }
            ],
        }
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

    def test_includes_local_hooks_with_additional_dependencies(self) -> None:
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "pkg-a",
                            "language": "python",
                            "additional_dependencies": ["tool-a==1.0.0"],
                        },
                        {
                            "id": "pkg-b",
                            "language": "python",
                            "additional_dependencies": [
                                "tool-b==2.0.0",
                                "dep-c==3.0.0",
                            ],
                        },
                    ],
                }
            ]
        }
        repos = checker._configured_repos(config)
        self.assertEqual(
            [
                ("local:tool-a==1.0.0", "1"),
                ("local:tool-b==2.0.0,dep-c==3.0.0", "1"),
            ],
            repos,
        )

    def test_skips_local_hooks_with_pygrep_or_system_language(self) -> None:
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "pygrep-hook",
                            "language": "pygrep",
                            "additional_dependencies": ["ignored==1.0.0"],
                        },
                        {
                            "id": "system-hook",
                            "language": "system",
                            "additional_dependencies": ["ignored==2.0.0"],
                        },
                        {
                            "id": "no-deps",
                            "language": "python",
                        },
                    ],
                }
            ]
        }
        self.assertEqual([], checker._configured_repos(config))

    def test_deduplicates_identical_local_dependencies(self) -> None:
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "hook-1",
                            "language": "python",
                            "additional_dependencies": ["shared==1.0.0"],
                        },
                        {
                            "id": "hook-2",
                            "language": "python",
                            "additional_dependencies": ["shared==1.0.0"],
                        },
                    ],
                }
            ]
        }
        self.assertEqual(
            [("local:shared==1.0.0", "1")], checker._configured_repos(config)
        )

    def test_empty_config(self) -> None:
        self.assertEqual([], checker._configured_repos({}))


class ConfiguredLocalDependenciesTests(unittest.TestCase):
    def test_returns_local_hooks_with_dependencies(self) -> None:
        self.assertEqual(
            [("dependency-hook", ("pyyaml==6.0.3",))],
            checker._configured_local_dependencies(_CONFIG_LOCAL_DEPENDENCIES),
        )

    def test_ignores_remote_hooks_and_local_hooks_without_dependencies(self) -> None:
        config = {
            "repos": [
                {
                    "repo": "https://example.invalid/foo",
                    "rev": "aaaa",
                    "hooks": [
                        {
                            "id": "remote",
                            "additional_dependencies": ["remote-dependency"],
                        }
                    ],
                },
                {
                    "repo": "local",
                    "hooks": [{"id": "local-without-dependencies"}],
                },
            ]
        }
        self.assertEqual([], checker._configured_local_dependencies(config))

    def test_skips_languages_without_an_environment(self) -> None:
        """A system hook never installs a dependency environment."""
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "system-hook",
                            "language": "system",
                            "additional_dependencies": ["pyyaml==6.0.3"],
                        }
                    ],
                }
            ]
        }
        self.assertEqual([], checker._configured_local_dependencies(config))


class PythonLanguageVersionTests(unittest.TestCase):
    def test_matches_pre_commit_default_version(self) -> None:
        """Pin the copied logic to pre-commit's own default-version contract."""
        try:
            from pre_commit.languages.python import get_default_version
        except ImportError:
            self.skipTest("pre-commit is not installed")

        self.assertEqual(
            get_default_version(), checker._default_python_language_version()
        )

    def test_uses_versioned_interpreter_found_on_path(self) -> None:
        """Select the versioned executable when pre-commit would find it."""
        expected = f"python{sys.version_info.major}.{sys.version_info.minor}"

        def which(name: str) -> str | None:
            return f"/usr/bin/{name}" if name == expected else None

        with patch("shutil.which", side_effect=which):
            self.assertEqual(expected, checker._default_python_language_version())

    def test_explicit_language_version_names_environment(self) -> None:
        """An explicit language_version selects that environment directory."""
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "explicit",
                            "language": "python",
                            "language_version": "python3.11",
                            "additional_dependencies": ["pyyaml==6.0.3"],
                        }
                    ],
                }
            ]
        }

        self.assertEqual(
            ("py_env-python3.11",),
            checker._configured_local_python_environments(config, ("pyyaml==6.0.3",)),
        )


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

    def test_offline_reports_missing_local_dependency_environment(self) -> None:
        repo_path = self.home / "repo-local"
        repo_path.mkdir()
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn("local hook dependency environment", message)
        self.assertIn("dependency-hook: pyyaml==6.0.3", message)
        self.assertIn("pre-commit install-hooks", message)

    def test_offline_skips_hooks_whose_language_has_no_environment(self) -> None:
        """A system hook's additional_dependencies need no environment."""
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "system-hook",
                            "language": "system",
                            "additional_dependencies": ["pyyaml==6.0.3"],
                        }
                    ],
                }
            ]
        }
        _make_db(self.home / "db.db", [])

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(False, self.home, config)

        self.assertEqual(0, rc)
        self.assertNotIn("local hook dependency environment", buf.getvalue())

    def test_offline_passes_with_installed_local_dependency_environment(
        self,
    ) -> None:
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        _add_environment_health(environment_path)
        (environment_path / ".install_state_v1").write_text(
            json.dumps({"additional_dependencies": ["pyyaml==6.0.3"]}),
            encoding="utf-8",
        )
        (environment_path / ".install_state_v2").touch()
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(0, rc)
        self.assertEqual("", buf.getvalue())

    def test_matching_v1_state_without_v2_is_installed(self) -> None:
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        _add_environment_health(environment_path)
        (environment_path / ".install_state_v1").write_text(
            json.dumps({"additional_dependencies": ["pyyaml==6.0.3"]}),
            encoding="utf-8",
        )
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(0, rc)
        self.assertEqual("", buf.getvalue())

    def test_v2_marker_without_v1_is_installed(self) -> None:
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        _add_environment_health(environment_path)
        (environment_path / ".install_state_v2").touch()
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(0, rc)
        self.assertEqual("", buf.getvalue())

    def test_v2_marker_without_health_payload_is_missing(self) -> None:
        """A marker alone must not pass: pre-commit would rebuild the env."""
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        environment_path.mkdir(parents=True)
        (environment_path / ".install_state_v2").touch()
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(1, rc)
        self.assertIn("dependency-hook: pyyaml==6.0.3", buf.getvalue())

    def test_environment_for_another_python_version_is_missing(self) -> None:
        repo_path = self.home / "repo-local"
        environment_path = repo_path / "py_env-python0.0"
        _add_environment_health(environment_path)
        (environment_path / ".install_state_v1").write_text(
            json.dumps({"additional_dependencies": ["pyyaml==6.0.3"]}),
            encoding="utf-8",
        )
        (environment_path / ".install_state_v2").touch()
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(1, rc)
        self.assertIn("dependency-hook: pyyaml==6.0.3", buf.getvalue())

    def test_undecodable_install_state_is_missing(self) -> None:
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        _add_environment_health(environment_path)
        (environment_path / ".install_state_v1").write_bytes(b"\xff")
        _make_db(
            self.home / "db.db",
            [("local:pyyaml==6.0.3", "1", str(repo_path))],
        )

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(
                False,
                self.home,
                _CONFIG_LOCAL_DEPENDENCIES,
            )
        self.assertEqual(1, rc)
        self.assertIn("dependency-hook: pyyaml==6.0.3", buf.getvalue())

    def test_offline_fails_closed_on_empty_cache_with_local_python_hook(self) -> None:
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "yamllint",
                            "language": "python",
                            "additional_dependencies": ["yamllint==1.38.0"],
                        }
                    ],
                }
            ]
        }
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(False, self.home, config)
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn("local:yamllint==1.38.0 @ 1", message)
        self.assertIn("pre-commit install-hooks", message)

    def test_offline_passes_when_local_python_hook_is_cached(self) -> None:
        config = {
            "repos": [
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "yamllint",
                            "language": "python",
                            "additional_dependencies": ["yamllint==1.38.0"],
                        }
                    ],
                }
            ]
        }
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        _add_environment_health(environment_path)
        (environment_path / ".install_state_v2").touch()
        _make_db(
            self.home / "db.db",
            [("local:yamllint==1.38.0", "1", str(repo_path))],
        )
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            rc = checker._preflight(False, self.home, config)
        self.assertEqual(0, rc)
        self.assertEqual("", buf.getvalue())


class PreflightRestoreTests(unittest.TestCase):
    """The pre-flight restores a cold cache from the bootstrap tarball."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name).resolve()
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

    def _current_python(self) -> str:
        """The five-part form a real interpreter prints for sys.version_info."""
        return (
            f"{sys.version_info.major}.{sys.version_info.minor}."
            f"{sys.version_info.micro}.final.0"
        )

    def _stale_patch_version(self) -> str:
        """A patch version that never matches the running interpreter."""
        return (
            f"{sys.version_info.major}.{sys.version_info.minor}."
            f"{sys.version_info.micro + 1}.final.0"
        )

    def _other_interpreter(self) -> str:
        """A major.minor that differs from the running interpreter."""
        return f"{sys.version_info.major + 1}.{sys.version_info.minor}"

    def _build_tarball(
        self,
        rows: list[tuple[str, str]],
        *,
        other_interpreters: tuple[str, ...] = (),
        env_specs: tuple[tuple[str, str | None, str | None], ...] = (),
        include_repo_dirs: bool = True,
        stored_paths: dict[tuple[str, str], str | bytes] | None = None,
    ) -> None:
        """Write a bootstrap tarball holding a cache with *rows* cached.

        The fixture mirrors the real layout: a ``db.db`` whose rows store
        the build-time repository paths, one repository directory per
        row with a ``.pre-commit-hooks.yaml``, and python environments
        described by ``pyvenv.cfg`` metadata. ``env_specs`` adds
        environments as ``(name, version_info, interpreter)``:
        ``_RUNTIME`` links ``bin/python`` to the running interpreter, a
        version string installs a regular-file stub script that prints
        it, and ``None`` leaves ``bin/python`` out. ``stored_paths``
        overrides the row paths.
        """
        packed = self.root / "packed"
        paths: dict[tuple[str, str], str | bytes] = {}
        if include_repo_dirs:
            for index, row in enumerate(rows):
                repo_dir = packed / f"repo{index}"
                repo_dir.mkdir(parents=True, exist_ok=True)
                (repo_dir / ".pre-commit-hooks.yaml").write_text(
                    "hooks: []\n", encoding="utf-8"
                )
                paths[row] = str(self.home / f"repo{index}")
        paths.update(stored_paths or {})
        _make_db(packed / "db.db", rows, paths)
        default_env = packed / "repodir" / f"py_env-python{self._current_interpreter()}"
        (default_env / "bin").mkdir(parents=True, exist_ok=True)
        (default_env / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\n",
            encoding="utf-8",
        )
        # A real virtualenv's bin/python is an absolute link to the base
        # interpreter; the restore must keep it.
        os.symlink(sys.executable, default_env / "bin" / "python")
        for version in other_interpreters:
            env_dir = packed / "repodir" / f"py_env-python{version}"
            env_dir.mkdir(parents=True, exist_ok=True)
            (env_dir / "pyvenv.cfg").write_text(
                f"version_info = {version}.0.final.0\n", encoding="utf-8"
            )
        for name, version_info, interpreter in env_specs:
            env_dir = packed / "repodir" / name
            (env_dir / "bin").mkdir(parents=True, exist_ok=True)
            if version_info is not None:
                (env_dir / "pyvenv.cfg").write_text(
                    f"version_info = {version_info}\n", encoding="utf-8"
                )
            if interpreter == _RUNTIME:
                os.symlink(sys.executable, env_dir / "bin" / "python")
            elif interpreter is not None:
                stub = env_dir / "bin" / "python"
                stub.write_text(
                    f"#!/bin/sh\nprintf '%s\\n' '{interpreter}'\n",
                    encoding="utf-8",
                )
                stub.chmod(0o755)
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
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
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
        self.assertEqual(sys.executable, os.readlink(link))
        # The promoted pyvenv.cfg is already normalized: the fixture
        # records a stale patch version and bin/python reports the
        # running interpreter.
        cfg = (
            self.home
            / "repodir"
            / f"py_env-python{self._current_interpreter()}"
            / "pyvenv.cfg"
        ).read_text(encoding="utf-8")
        self.assertIn(f"version_info = {self._current_python()}", cfg)
        # No interpreter warning for a matching cache.
        self.assertEqual("", stderr.getvalue())

    def test_missing_local_environment_restores_with_cached_revisions(
        self,
    ) -> None:
        local_dep = ("local:pyyaml==6.0.3", "1")
        _make_db(self.home / "db.db", self._config_rows())
        self._build_tarball(
            [*self._config_rows(), local_dep],
            stored_paths={local_dep: str(self.home / "repo-local")},
        )
        packed = self.root / "packed"
        environment_path = (
            packed / "repo-local" / f"py_env-python{self._current_interpreter()}"
        )
        (environment_path / "bin").mkdir(parents=True)
        (environment_path / "pyvenv.cfg").write_text(
            f"version_info = {self._current_python()}\n",
            encoding="utf-8",
        )
        os.symlink(sys.executable, environment_path / "bin" / "python")
        (environment_path / ".install_state_v1").write_text(
            json.dumps({"additional_dependencies": ["pyyaml==6.0.3"]}),
            encoding="utf-8",
        )
        (environment_path / ".install_state_v2").touch()
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        config = {
            "repos": [
                *_CONFIG_TWO_REMOTE_ONE_LOCAL["repos"],
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "dependency-hook",
                            "language": "python",
                            "additional_dependencies": ["pyyaml==6.0.3"],
                        }
                    ],
                },
            ]
        }

        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(False, self.home, config)
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertFalse(self.tarball.exists())
        self.assertTrue(
            (
                self.home
                / "repo-local"
                / f"py_env-python{self._current_interpreter()}"
                / ".install_state_v1"
            ).is_file()
        )
        self.assertEqual("", stderr.getvalue())

    def test_incomplete_local_environment_does_not_replace_live_cache(
        self,
    ) -> None:
        local_dep = ("local:pyyaml==6.0.3", "1")
        _make_db(self.home / "db.db", self._config_rows())
        self._build_tarball(
            [*self._config_rows(), local_dep],
            stored_paths={local_dep: str(self.home / "repo-local")},
        )
        packed = self.root / "packed"
        (packed / "repo-local").mkdir()
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        config = {
            "repos": [
                *_CONFIG_TWO_REMOTE_ONE_LOCAL["repos"],
                {
                    "repo": "local",
                    "hooks": [
                        {
                            "id": "dependency-hook",
                            "language": "python",
                            "additional_dependencies": ["pyyaml==6.0.3"],
                        }
                    ],
                },
            ]
        }

        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(False, self.home, config)
        self.assertEqual(1, rc)
        message = stderr.getvalue()
        self.assertIn("cannot provide the configured local hook dependency", message)
        self.assertIn("dependency-hook: pyyaml==6.0.3", message)
        self.assertNotIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertEqual(set(self._config_rows()), checker._cached_repos(self.home))

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
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
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

    def test_unrecognized_env_name_with_mismatched_metadata_warns(self) -> None:
        # Directory names carry no interpreter information; the warning
        # must come from the pyvenv.cfg metadata.
        self._build_tarball(
            self._config_rows(),
            env_specs=(
                ("py_env-default", "9.9.1.final.0", None),
                ("py_env-nocfg", None, None),
            ),
        )
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("built for another interpreter", message)
        self.assertIn("py_env-default", message)
        self.assertIn("pyvenv.cfg missing or unreadable", message)
        self.assertIn("py_env-nocfg", message)

    def test_stale_patch_version_is_normalized_from_the_env_interpreter(self) -> None:
        # The runner cannot know the sandbox's patch version; the
        # environment's own bin/python supplies it and the restore
        # rewrites version_info so pre-commit's health check passes.
        self._build_tarball(
            self._config_rows(),
            env_specs=(("py_env-stale", self._stale_patch_version(), _RUNTIME),),
        )
        stdout = io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        cfg = (self.home / "repodir" / "py_env-stale" / "pyvenv.cfg").read_text(
            encoding="utf-8"
        )
        self.assertIn(f"version_info = {self._current_python()}", cfg)

    def test_unwritable_pyvenv_cfg_does_not_fail_the_restore(self) -> None:
        # A pyvenv.cfg that cannot be rewritten must not crash the
        # restore; the environment is skipped with a warning.
        self._build_tarball(
            self._config_rows(),
            env_specs=(("py_env-stale", self._stale_patch_version(), _RUNTIME),),
        )
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            patch.object(
                checker, "_rewrite_version_info", side_effect=OSError("read-only")
            ),
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        # The restore succeeded and no traceback escaped.
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        message = stderr.getvalue()
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("py_env-stale", message)
        self.assertIn("reinstall", message)

    def test_mismatched_version_with_regular_file_bin_python_is_rejected(self) -> None:
        # pre-commit's health check executes bin/python before it
        # compares versions, so a mismatched version_info must not skip
        # the static safety checks.
        other = self._other_interpreter()
        self._build_tarball(
            self._config_rows(),
            env_specs=((f"py_env-python{other}", f"{other}.9.final.0", "9.9.1"),),
        )
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
        self.assertIn("unsafe python hook environments", message)
        self.assertIn(f"py_env-python{other}", message)
        self.assertIn("is not a symlink", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_mismatched_version_with_missing_bin_python_warns_and_promotes(
        self,
    ) -> None:
        other = self._other_interpreter()
        self._build_tarball(
            self._config_rows(),
            env_specs=((f"py_env-python{other}", f"{other}.9.final.0", None),),
        )
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        message = stderr.getvalue()
        self.assertIn("built for another interpreter", message)
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("is missing", message)

    def test_other_version_platform_interpreters_warn_and_promote(self) -> None:
        # A cache built for another interpreter names platform binaries
        # for that version; the static checks must accept them so the
        # version gate can warn instead of rejecting the archive.
        other = self._other_interpreter()
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / f"py_env-python{other}"
        (env_dir / "bin").mkdir(parents=True)
        cfg_content = (
            f"version_info = {other}.9.final.0\n"
            f"base-executable = /usr/bin/python{other}\n"
        )
        (env_dir / "pyvenv.cfg").write_text(cfg_content, encoding="utf-8")
        # The symlink target need not exist: os.path.lexists treats the
        # link as present and Path.resolve returns the lexical target,
        # so the static check still sees the platform name.
        os.symlink(f"/usr/bin/python{other}", env_dir / "bin" / "python")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        message = stderr.getvalue()
        self.assertIn("built for another interpreter", message)
        self.assertNotIn("unsafe python hook environments", message)
        self.assertNotIn("could not normalize the version_info", message)
        # Nothing was rewritten: pyvenv.cfg is byte-identical.
        self.assertEqual(
            cfg_content,
            (self.home / "repodir" / f"py_env-python{other}" / "pyvenv.cfg").read_text(
                encoding="utf-8"
            ),
        )

    def test_mismatched_version_with_untrusted_home_is_rejected(self) -> None:
        other = self._other_interpreter()
        self._build_env_cfg_tarball(
            [f"version_info = {other}.9.final.0", "home = /tmp/evil"]
        )
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
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("home /tmp/evil is not /usr or below /usr", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_mismatched_version_with_foreign_base_executable_is_rejected(self) -> None:
        other = self._other_interpreter()
        self._build_env_cfg_tarball(
            [f"version_info = {other}.9.final.0", "base-executable = /opt/python3"]
        )
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
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("base-executable /opt/python3", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_env_reporting_another_major_minor_is_left_unchanged(self) -> None:
        other = self._other_interpreter()
        self._build_tarball(
            self._config_rows(),
            env_specs=((f"py_env-python{other}", f"{other}.9.final.0", None),),
        )
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        cfg = (
            self.home / "repodir" / f"py_env-python{other}" / "pyvenv.cfg"
        ).read_text(encoding="utf-8")
        self.assertIn(f"version_info = {other}.9.final.0", cfg)
        self.assertIn("built for another interpreter", stderr.getvalue())

    def test_probe_reporting_another_major_minor_is_reported(self) -> None:
        self._build_tarball(
            self._config_rows(),
            env_specs=(("py_env-drifted", self._stale_patch_version(), _RUNTIME),),
        )
        with patch.object(
            checker,
            "_probe_interpreter",
            return_value=("9.9.1.final.0", None),
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("py_env-drifted", message)
        self.assertIn("reports 9.9.1.final.0", message)
        # The environment's file is left unchanged.
        cfg = (self.home / "repodir" / "py_env-drifted" / "pyvenv.cfg").read_text(
            encoding="utf-8"
        )
        self.assertIn(f"version_info = {self._stale_patch_version()}", cfg)

    def test_regular_file_bin_python_is_not_executed(self) -> None:
        self._build_tarball(
            self._config_rows(),
            env_specs=(("py_env-regular", self._stale_patch_version(), "9.9.1"),),
        )
        original_probe = checker._probe_interpreter

        def probe_refusing_regular_files(executable):
            if "py_env-regular" in str(executable):
                raise AssertionError("the regular file was executed")
            return original_probe(executable)

        with patch.object(
            checker, "_probe_interpreter", side_effect=probe_refusing_regular_files
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(1, rc)
        message = stderr.getvalue()
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("py_env-regular", message)
        self.assertIn("is not a symlink", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_missing_bin_python_is_reported(self) -> None:
        self._build_tarball(
            self._config_rows(),
            env_specs=(("py_env-nolink", self._stale_patch_version(), None),),
        )
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("py_env-nolink", message)
        self.assertIn("is missing", message)

    def test_non_zero_probe_exit_is_reported(self) -> None:
        self._build_tarball(
            self._config_rows(),
            env_specs=(("py_env-failing", self._stale_patch_version(), _RUNTIME),),
        )
        with patch.object(
            checker,
            "_probe_interpreter",
            return_value=(None, "the interpreter probe exited 1"),
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("py_env-failing", message)
        self.assertIn("the interpreter probe exited 1", message)

    def test_base_executable_disagreement_is_reported(self) -> None:
        # bin/python is a real interpreter; the patched probe makes the
        # base executable report a different version.
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / "py_env-based"
        (env_dir / "bin").mkdir(parents=True)
        (env_dir / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\n"
            "base-executable = /usr/bin/python3\n",
            encoding="utf-8",
        )
        os.symlink(sys.executable, env_dir / "bin" / "python")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        original_is_file = Path.is_file

        def is_file_with_python3(path):
            if str(path) == "/usr/bin/python3":
                return True
            return original_is_file(path)

        with (
            patch.object(
                Path, "is_file", side_effect=is_file_with_python3, autospec=True
            ),
            patch.object(
                checker,
                "_probe_interpreter",
                side_effect=[(self._current_python(), None), ("3.9.1.final.0", None)],
            ),
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("py_env-based", message)
        self.assertIn("base-executable /usr/bin/python3 reports 3.9.1.final.0", message)

    def test_pyvenv_cfg_symlink_outside_the_cache_is_not_written(self) -> None:
        outside = self.root / "outside.cfg"
        outside.write_text(
            f"version_info = {self._stale_patch_version()}\n", encoding="utf-8"
        )
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / "py_env-linked"
        (env_dir / "bin").mkdir(parents=True)
        os.symlink(sys.executable, env_dir / "bin" / "python")
        os.symlink(outside, env_dir / "pyvenv.cfg")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        # A symlinked pyvenv.cfg is unsafe: the archive is rejected, the
        # external file is unchanged, and nothing is promoted.
        self.assertEqual(1, rc)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertEqual(
            f"version_info = {self._stale_patch_version()}\n",
            outside.read_text(encoding="utf-8"),
        )
        message = stderr.getvalue()
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("py_env-linked", message)
        self.assertIn("is a symlink", message)
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_rewrite_version_info_preserves_other_lines(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        env_dir = Path(temporary_directory.name)
        (env_dir / "pyvenv.cfg").write_text(
            "home = /usr/bin\nversion_info = 3.12.0.final.0\nbinary = x\n",
            encoding="utf-8",
        )
        replaced = checker._rewrite_version_info(
            env_dir, "3.12.9.final.0", env_dir.parent
        )
        self.assertTrue(replaced)
        self.assertEqual(
            "home = /usr/bin\nversion_info = 3.12.9.final.0\nbinary = x\n",
            (env_dir / "pyvenv.cfg").read_text(encoding="utf-8"),
        )

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

    def test_archive_missing_a_configured_revision_is_not_promoted(self) -> None:
        # The archive holds a row the config does not need and lacks the
        # rows it does need; nothing may be promoted.
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
        message = stderr.getvalue()
        self.assertIn("does not cover every configured", message)
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        # No partial cache was published and no staging directory leaked.
        self.assertFalse((self.home / "db.db").exists())
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.restore-*")))

    def test_archive_dropping_a_live_revision_is_not_promoted(self) -> None:
        # The live cache holds a revision the archive lacks; promotion
        # would drop it, so the live cache must stay.
        _make_db(
            self.home / "db.db",
            [
                ("https://example.invalid/foo", "aaaa"),
                ("https://example.invalid/extra", "eeee"),
            ],
        )
        self._build_tarball(self._config_rows())
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
        self.assertIn("would drop cached revisions", message)
        self.assertIn("https://example.invalid/extra @ eeee", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        # The live cache still holds both of its revisions.
        self.assertEqual(
            {
                ("https://example.invalid/foo", "aaaa"),
                ("https://example.invalid/extra", "eeee"),
            },
            checker._cached_repos(self.home),
        )
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.restore-*")))

    def test_stored_path_with_dotdot_components_is_rejected(self) -> None:
        # normpath collapses ".." lexically, but the filesystem resolves
        # the original spelling through symlinks, so the two can name
        # different directories.
        stored = str(self.home / "alias" / ".." / "repo0")
        self._build_tarball(
            self._config_rows(),
            stored_paths={
                ("https://example.invalid/foo", "aaaa"): stored,
            },
        )
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
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertIn("contains '..'", message)
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_canonical_stored_paths_still_pass(self) -> None:
        # A canonical path without ".." components is validated normally.
        self._build_tarball(self._config_rows())
        stdout = io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())

    def test_rewrite_uses_an_exclusive_temporary_file(self) -> None:
        self._build_tarball(self._config_rows())
        with patch("tempfile.mkstemp", wraps=tempfile.mkstemp) as mkstemp:
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(0, rc)
        self.assertEqual("", stderr.getvalue())
        # mkstemp created the replacement file exclusively, and no
        # predictable-name temporary file is left behind.
        mkstemp.assert_called()
        default_env = (
            self.home / "repodir" / f"py_env-python{self._current_interpreter()}"
        )
        self.assertEqual([], list(default_env.glob(".pyvenv.cfg.*")))

    def test_env_directory_outside_the_cache_is_not_normalized(self) -> None:
        # An environment directory that is a symlink to outside the
        # cache must not be rewritten through.
        outside_env = self.root / "outside-env"
        (outside_env / "bin").mkdir(parents=True)
        (outside_env / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\n", encoding="utf-8"
        )
        os.symlink(sys.executable, outside_env / "bin" / "python")
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_link = packed / "repodir" / "py_env-escaped"
        env_link.parent.mkdir(parents=True, exist_ok=True)
        os.symlink(outside_env, env_link)
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)

        original_probe = checker._probe_interpreter

        def probe_refusing_escaped_env(executable):
            if "py_env-escaped" in str(executable):
                raise AssertionError("the escaped environment was executed")
            return original_probe(executable)

        with patch.object(
            checker, "_probe_interpreter", side_effect=probe_refusing_escaped_env
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(1, rc)
        message = stderr.getvalue()
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("py_env-escaped", message)
        self.assertIn("is a symlink", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())
        # The external pyvenv.cfg is unchanged.
        self.assertEqual(
            f"version_info = {self._stale_patch_version()}\n",
            (outside_env / "pyvenv.cfg").read_text(encoding="utf-8"),
        )

    def test_empty_base_executable_is_reported(self) -> None:
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / "py_env-emptybase"
        (env_dir / "bin").mkdir(parents=True)
        (env_dir / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\nbase-executable =\n",
            encoding="utf-8",
        )
        os.symlink(sys.executable, env_dir / "bin" / "python")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(1, rc)
        message = stderr.getvalue()
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("py_env-emptybase", message)
        self.assertIn("base-executable is empty", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_local_row_without_a_staged_directory_is_rejected(self) -> None:
        # A retained local: row still needs its staged directory; an
        # archive with only the row would discard a usable live
        # environment.
        local_dep = ("local:dep", "rev1")
        _make_db(
            self.home / "db.db",
            [("https://example.invalid/foo", "aaaa"), local_dep],
        )
        self._build_tarball(
            [*self._config_rows(), local_dep],
            stored_paths={local_dep: str(self.home / "repo-absent")},
        )
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
        self.assertIn("local:dep @ rev1", message)
        self.assertIn("is missing from the archive", message)
        self.assertTrue(self.tarball.exists())
        # The live cache keeps its rows.
        self.assertIn(local_dep, checker._cached_repos(self.home))

    def test_local_row_without_a_manifest_is_promoted(self) -> None:
        # A retained local: row whose staged directory exists but holds
        # no hook manifest is promoted; pre-commit stores those
        # environments without a manifest.
        local_dep = ("local:dep", "rev1")
        _make_db(
            self.home / "db.db",
            [("https://example.invalid/foo", "aaaa"), local_dep],
        )
        # repodir exists in every fixture archive and holds no manifest.
        self._build_tarball(
            [*self._config_rows(), local_dep],
            stored_paths={local_dep: str(self.home / "repodir")},
        )
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        # The local: row survived the promotion.
        self.assertIn(local_dep, checker._cached_repos(self.home))

    def test_planted_predictable_temporary_name_is_ignored(self) -> None:
        # The rewrite creates its temporary file with mkstemp; a symlink
        # planted at the old predictable name must not redirect it.
        outside = self.root / "outside-tmp.cfg"
        outside.write_text("do not touch\n", encoding="utf-8")
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / f"py_env-python{self._current_interpreter()}"
        (env_dir / "bin").mkdir(parents=True)
        (env_dir / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\n", encoding="utf-8"
        )
        os.symlink(sys.executable, env_dir / "bin" / "python")
        os.symlink(outside, env_dir / f".pyvenv.cfg.{os.getpid()}.tmp")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        # The restore succeeded and rewrote version_info through mkstemp.
        self.assertEqual(0, rc)
        self.assertEqual("", stderr.getvalue())
        promoted_cfg = (
            self.home
            / "repodir"
            / f"py_env-python{self._current_interpreter()}"
            / "pyvenv.cfg"
        ).read_text(encoding="utf-8")
        self.assertIn(f"version_info = {self._current_python()}", promoted_cfg)
        # The external file is unchanged.
        self.assertEqual("do not touch\n", outside.read_text(encoding="utf-8"))

    def test_fchmod_failure_closes_the_descriptor_and_is_reported(self) -> None:
        # A failing fchmod must not leak the mkstemp descriptor; the
        # failure is reported through the restore warning.
        self._build_tarball(self._config_rows())
        captured: dict[str, int] = {}
        original_mkstemp = tempfile.mkstemp

        def capturing_mkstemp(*args, **kwargs):
            descriptor, name = original_mkstemp(*args, **kwargs)
            captured["fd"] = descriptor
            return descriptor, name

        with (
            patch("tempfile.mkstemp", side_effect=capturing_mkstemp),
            patch("os.fchmod", side_effect=OSError("fchmod refused")),
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        # The restore succeeded and the failure was reported.
        self.assertEqual(0, rc)
        message = stderr.getvalue()
        self.assertIn("could not normalize the version_info", message)
        self.assertIn("fchmod refused", message)
        # The descriptor was closed on the failure path.
        with self.assertRaises(OSError):
            os.fstat(captured["fd"])

    def test_blob_repo_key_is_rejected(self) -> None:
        # A BLOB repo key must produce a normal rejection warning, not
        # a TypeError from sorting or startswith.
        self._build_tarball([*self._config_rows(), (b"blob-repo", "rev1")])
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
        self.assertIn("malformed repository row", message)
        self.assertIn("must be text", message)
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def _build_retained_local_env_tarball(
        self,
        *,
        with_environment: bool,
        environment_name: str = "py_env-python3.12",
    ) -> None:
        """Archive with a retained local: row and an optional environment.

        The live row stores the revision under ``repo-local`` and holds
        its environment there; the staged row stores it under
        ``staged-local`` and the archive creates the environment there.
        The live and staged stored paths differ on purpose, so the
        retained-environment check must read the live row for the live
        side: reading the staged path would find no live environment
        and let an omitting archive through.
        """
        local_dep = ("local:dep", "rev1")
        _make_db(
            self.home / "db.db",
            [("https://example.invalid/foo", "aaaa"), local_dep],
            {
                ("https://example.invalid/foo", "aaaa"): str(self.home / "repo0"),
                local_dep: str(self.home / "repo-local"),
            },
        )
        # The live cache holds the installed environment.
        (self.home / "repo-local" / environment_name).mkdir(parents=True)
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(self._config_rows()):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        staged_local = packed / "staged-local"
        staged_local.mkdir()
        if with_environment:
            (staged_local / environment_name).mkdir()
        paths[local_dep] = str(self.home / "staged-local")
        _make_db(packed / "db.db", [*self._config_rows(), local_dep], paths)
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)

    def test_retained_local_row_missing_its_environment_is_rejected(self) -> None:
        self._build_retained_local_env_tarball(with_environment=False)
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
        self.assertIn("local:dep @ rev1", message)
        self.assertIn("py_env-python3.12", message)
        self.assertIn("omits the installed environment", message)
        self.assertTrue(self.tarball.exists())
        # The live cache, including its environment, is untouched.
        self.assertTrue((self.home / "repo-local" / "py_env-python3.12").is_dir())

    def test_retained_docker_environment_omitted_is_rejected(self) -> None:
        # The docker language creates a docker environment directory;
        # an archive omitting it cannot replace the live cache.
        self._build_retained_local_env_tarball(
            with_environment=False, environment_name="docker"
        )
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
        self.assertIn("local:dep @ rev1", message)
        self.assertIn("docker", message)
        self.assertIn("omits the installed environment", message)
        self.assertTrue(self.tarball.exists())
        # The live cache, including its docker environment, is untouched.
        self.assertTrue((self.home / "repo-local" / "docker").is_dir())

    def test_retained_local_row_with_its_environment_is_promoted(self) -> None:
        self._build_retained_local_env_tarball(with_environment=True)
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        # The promoted cache holds the retained environment under the
        # staged row's directory.
        self.assertTrue((self.home / "staged-local" / "py_env-python3.12").is_dir())

    def test_staged_repository_symlink_is_rejected(self) -> None:
        # A staged repository directory that is a symlink to an outside
        # directory with a valid manifest must be rejected; is_dir()
        # would follow it.
        outside_repo = self.root / "outside-repo"
        outside_repo.mkdir()
        (outside_repo / ".pre-commit-hooks.yaml").write_text(
            "hooks: []\n", encoding="utf-8"
        )
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows[1:], start=1):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        # foo's staged repository directory is a symlink outside.
        os.symlink(outside_repo, packed / "repo0")
        paths[rows[0]] = str(self.home / "repo0")
        _make_db(packed / "db.db", rows, paths)
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
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
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertIn("repository directory", message)
        self.assertIn("is a symlink", message)
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_staged_db_symlink_is_rejected(self) -> None:
        # A db.db that is a symlink must not be read.
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        outside_db = self.root / "outside-db.db"
        _make_db(outside_db, rows, paths)
        _make_db(packed / "db.real", rows, paths)
        os.symlink(outside_db, packed / "db.db")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
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
        self.assertIn("db.db", message)
        self.assertIn("is a symlink", message)
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def _build_env_cfg_tarball(self, cfg_lines: list[str]) -> None:
        """Archive whose default environment's pyvenv.cfg holds *cfg_lines*."""
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / f"py_env-python{self._current_interpreter()}"
        (env_dir / "bin").mkdir(parents=True)
        (env_dir / "pyvenv.cfg").write_text(
            "\n".join(cfg_lines) + "\n", encoding="utf-8"
        )
        os.symlink(sys.executable, env_dir / "bin" / "python")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)

    def test_home_outside_usr_is_reported_and_not_probed(self) -> None:
        # Python reads pyvenv.cfg's home during path initialization,
        # before -S takes effect, so an untrusted home must stop the
        # probe.
        self._build_env_cfg_tarball(
            [f"version_info = {self._stale_patch_version()}", "home = /tmp/evil"]
        )

        def probe_refusing_evil_home(executable):
            raise AssertionError("the environment with an untrusted home was probed")

        with patch.object(
            checker, "_probe_interpreter", side_effect=probe_refusing_evil_home
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(1, rc)
        message = stderr.getvalue()
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("home /tmp/evil is not /usr or below /usr", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_home_traversal_is_not_promoted(self) -> None:
        # "/usr/../tmp/evil" lexically sits under /usr only through the
        # ".." component; it must be rejected before resolution.
        self._build_env_cfg_tarball(
            [
                f"version_info = {self._stale_patch_version()}",
                "home = /usr/../tmp/evil",
            ]
        )
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
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("home /usr/../tmp/evil contains '..'", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_home_resolving_outside_usr_is_rejected(self) -> None:
        original_resolve = Path.resolve

        def resolve_escaping_usr(path):
            if str(path) == "/usr/linked":
                return Path("/tmp/evil")
            return original_resolve(path)

        with patch.object(
            Path, "resolve", side_effect=resolve_escaping_usr, autospec=True
        ):
            rejection = checker._home_rejection("/usr/linked")
        self.assertIsNotNone(rejection)
        self.assertIn("home /usr/linked is not /usr or below /usr", rejection)
        self.assertIn("resolves to /tmp/evil", rejection)

    def test_home_at_usr_bin_is_accepted(self) -> None:
        self.assertIsNone(checker._home_rejection("/usr/bin"))

    def test_bin_python_to_a_non_platform_path_is_not_promoted(self) -> None:
        # bin/python may only be a symlink to a platform interpreter;
        # /bin/sh is not one.
        rows = self._config_rows()
        packed = self.root / "packed"
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
        env_dir = packed / "repodir" / "py_env-sh"
        (env_dir / "bin").mkdir(parents=True)
        (env_dir / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\n", encoding="utf-8"
        )
        os.symlink("/bin/sh", env_dir / "bin" / "python")
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
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
        self.assertIn("unsafe python hook environments", message)
        self.assertIn("py_env-sh", message)
        self.assertIn("not /usr/bin/python3", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_home_at_usr_bin_passes(self) -> None:
        self._build_env_cfg_tarball(
            [f"version_info = {self._stale_patch_version()}", "home = /usr/bin"]
        )
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertEqual("", stderr.getvalue())

    def test_blob_stored_path_is_rejected(self) -> None:
        # A BLOB in the path column must produce a normal rejection,
        # not a TypeError traceback.
        self._build_tarball(
            self._config_rows(),
            stored_paths={
                ("https://example.invalid/foo", "aaaa"): b"\x00\x01",
            },
        )
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
        self.assertIn("cannot provide", message)
        self.assertIn("is not an absolute path", message)
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_stored_path_through_a_live_symlink_is_rejected(self) -> None:
        # The live cache holds repo0 as a symlink to repo1; the archive
        # has only repo1. The check must compare lexically, so the
        # missing repo0 is caught instead of silently mapping to repo1.
        live_repo1 = self.home / "repo1"
        live_repo1.mkdir(parents=True)
        os.symlink("repo1", self.home / "repo0")
        packed = self.root / "packed"
        repo1 = packed / "repo1"
        repo1.mkdir(parents=True)
        (repo1 / ".pre-commit-hooks.yaml").write_text("hooks: []\n", encoding="utf-8")
        _make_db(
            packed / "db.db",
            self._config_rows(),
            {
                ("https://example.invalid/foo", "aaaa"): str(self.home / "repo0"),
                ("https://example.invalid/bar", "bbbb"): str(self.home / "repo1"),
            },
        )
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)
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
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertIn("is missing from the archive", message)
        self.assertTrue(self.tarball.exists())
        # The live cache, including its symlink, is untouched.
        self.assertTrue((self.home / "repo0").is_symlink())

    def test_retained_revision_without_a_payload_is_rejected(self) -> None:
        # The live cache holds an extra revision; the archive has its
        # row but no repository directory for it.
        extra = ("https://example.invalid/extra", "eeee")
        _make_db(
            self.home / "db.db",
            [("https://example.invalid/foo", "aaaa"), extra],
        )
        self._build_tarball(
            [*self._config_rows(), extra],
            stored_paths={extra: str(self.home / "repo-absent")},
        )
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
        self.assertIn("https://example.invalid/extra @ eeee", message)
        self.assertIn("is missing from the archive", message)
        self.assertTrue(self.tarball.exists())
        # The live cache keeps both of its revisions.
        self.assertEqual(
            {
                ("https://example.invalid/foo", "aaaa"),
                ("https://example.invalid/extra", "eeee"),
            },
            checker._cached_repos(self.home),
        )

    def test_row_without_a_repository_directory_is_rejected(self) -> None:
        # db.db covers the configured revisions, but the archive does
        # not carry the repository directory for one of them.
        inside = {
            ("https://example.invalid/foo", "aaaa"): str(self.home / "repo0"),
            ("https://example.invalid/bar", "bbbb"): str(self.home / "repo1"),
        }
        self._build_tarball(
            self._config_rows(), include_repo_dirs=False, stored_paths=inside
        )
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
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertIn("is missing from the archive", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_row_with_a_path_outside_the_cache_root_is_rejected(self) -> None:
        self._build_tarball(
            self._config_rows(),
            stored_paths={("https://example.invalid/foo", "aaaa"): "/elsewhere/repo0"},
        )
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
        self.assertIn("https://example.invalid/foo @ aaaa", message)
        self.assertIn("the stored path /elsewhere/repo0 is outside", message)
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

    def test_corrupt_deflate_payload_fails_gracefully(self) -> None:
        self._build_tarball(self._config_rows())
        raw = bytearray(self.tarball.read_bytes())
        # A valid gzip header with a corrupted DEFLATE payload; the
        # exception type differs by Python version, but the restore must
        # never traceback.
        raw[20:28] = b"\x00\xff\x00\xff\x00\xff\x00\xff"
        self.tarball.write_bytes(bytes(raw))
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(1, rc)
        self.assertIn("cannot restore the pre-commit cache", stderr.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_zlib_error_is_caught(self) -> None:
        self._build_tarball(self._config_rows())
        with patch.object(
            tarfile, "open", side_effect=zlib.error("Error -3 while decompressing")
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(1, rc)
        self.assertIn("cannot restore the pre-commit cache", stderr.getvalue())
        self.assertIn("Error -3", stderr.getvalue())
        self.assertTrue(self.tarball.exists())

    def test_complete_archive_is_promoted_over_a_partial_live_cache(self) -> None:
        _make_db(self.home / "db.db", [("https://example.invalid/foo", "aaaa")])
        self._build_tarball(self._config_rows())
        stdout = io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
            )
        self.assertEqual(0, rc)
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())
        self.assertEqual(set(self._config_rows()), checker._cached_repos(self.home))
        self.assertFalse(self.tarball.exists())

    def test_symlink_cycle_does_not_crash_or_escape(self) -> None:
        # Python 3.12 raises RuntimeError from Path.resolve on a symlink
        # cycle; the filter must reject such members instead of letting
        # the error escape _restore_cache. On newer Pythons the cycle
        # resolves inside the destination and is harmless.
        with tarfile.open(self.tarball, "w:gz") as tar:
            first = tarfile.TarInfo("dir/loop-a")
            first.type = tarfile.SYMTYPE
            first.linkname = "loop-b"
            tar.addfile(first)
            second = tarfile.TarInfo("dir/loop-b")
            second.type = tarfile.SYMTYPE
            second.linkname = "loop-a"
            tar.addfile(second)
        stdout, stderr = io.StringIO(), io.StringIO()
        try:
            with (
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        except (RuntimeError, OSError) as error:
            self.fail(f"the symlink cycle escaped the restore: {error!r}")
        # Nothing landed outside the cache home.
        self.assertFalse((self.root / "loop-a").exists())
        self.assertFalse((self.root / "loop-b").exists())

    def test_unresolvable_member_path_is_rejected(self) -> None:
        # Simulates Python 3.12, where Path.resolve raises RuntimeError
        # for a symlink cycle: the member must count as rejected.
        self._build_tarball(self._config_rows())
        original_resolve = Path.resolve

        def resolve_raising_on_cycle(path):
            if path.name == "db.db":
                raise RuntimeError("Symlink loop")
            return original_resolve(path)

        with patch.object(
            Path, "resolve", side_effect=resolve_raising_on_cycle, autospec=True
        ):
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
            ):
                rc = checker._preflight_with_restore(
                    False, self.home, _CONFIG_TWO_REMOTE_ONE_LOCAL
                )
        self.assertEqual(1, rc)
        self.assertIn("skipped 1 tarball member(s)", stderr.getvalue())
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())
        self.assertFalse((self.home / "db.db").exists())

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
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
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


class StoredRepositoryPathTests(unittest.TestCase):
    """Stored repository paths must be direct children of the cache root."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name).resolve()
        self.cache_root = self.root / "cache"
        self.staging = self.root / "staging"
        self.staging.mkdir()

    def test_stored_path_at_cache_root_is_rejected(self) -> None:
        rejection = checker._staged_repository_rejection(
            str(self.cache_root), self.staging, self.cache_root, require_manifest=False
        )

        self.assertIsNotNone(rejection)
        self.assertIn("not a direct child", rejection)

    def test_nested_stored_path_is_rejected(self) -> None:
        stored = str(self.cache_root / "a" / "b")
        rejection = checker._staged_repository_rejection(
            stored, self.staging, self.cache_root, require_manifest=False
        )

        self.assertIsNotNone(rejection)
        self.assertIn("not a direct child", rejection)

    def test_direct_child_is_accepted(self) -> None:
        (self.staging / "repo1").mkdir()

        rejection = checker._staged_repository_rejection(
            str(self.cache_root / "repo1"),
            self.staging,
            self.cache_root,
            require_manifest=False,
        )

        self.assertIsNone(rejection)

    def test_live_path_at_cache_root_has_no_relative_path(self) -> None:
        relative = checker._repository_relative_or_none(
            str(self.cache_root), self.cache_root
        )

        self.assertIsNone(relative)

    def test_live_nested_path_has_no_relative_path(self) -> None:
        relative = checker._repository_relative_or_none(
            str(self.cache_root / "a" / "b"), self.cache_root
        )

        self.assertIsNone(relative)

    def test_live_direct_child_is_relative(self) -> None:
        relative = checker._repository_relative_or_none(
            str(self.cache_root / "repo1"), self.cache_root
        )

        self.assertEqual(Path("repo1"), relative)


class EnvironmentDirTests(unittest.TestCase):
    """Environment directories match pre-commit's real names."""

    def test_environment_dir_names(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        repo_dir = Path(temporary_directory.name)
        for name in (
            "build_env_scripts",
            "docker",
            "golang_env",
            "golangenv-default",
            "py_env-python3.12",
            "rbenv",
            "renv",
        ):
            (repo_dir / name).mkdir()
        (repo_dir / "manifest.yaml").write_text("hooks: []\n", encoding="utf-8")
        self.assertEqual(
            {"docker", "golangenv-default", "py_env-python3.12", "rbenv", "renv"},
            checker._environment_dirs(repo_dir),
        )


class SwapCacheHomeTests(unittest.TestCase):
    """A failed promotion keeps the live cache."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name).resolve()
        self.home = self.root / "cache"

    def _seed_live_cache(self) -> None:
        _make_db(self.home / "db.db", [("https://example.invalid/foo", "aaaa")])

    def test_missing_staging_refuses_and_keeps_the_live_cache(self) -> None:
        # An empty archive never creates the staging directory; the swap
        # must refuse before touching the live cache.
        self._seed_live_cache()
        staging = self.root / ".cache.restore-test"
        with self.assertRaises(OSError):
            checker._swap_cache_home(staging, self.home)
        self.assertEqual(
            {("https://example.invalid/foo", "aaaa")},
            checker._cached_repos(self.home),
        )
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.backup-*")))

    def test_failed_replace_restores_the_live_cache(self) -> None:
        self._seed_live_cache()
        staging = self.root / "staging"
        staging.mkdir()
        (staging / "marker").write_text("staged", encoding="utf-8")
        original_replace = os.replace
        calls = {"count": 0}

        def replace_failing_on_promotion(source, destination):
            calls["count"] += 1
            if calls["count"] == 2:
                raise OSError("promotion failed")
            return original_replace(source, destination)

        with (
            patch("os.replace", side_effect=replace_failing_on_promotion),
            self.assertRaises(OSError),
        ):
            checker._swap_cache_home(staging, self.home)
        # The live cache is back in place with its revision.
        self.assertEqual(
            {("https://example.invalid/foo", "aaaa")},
            checker._cached_repos(self.home),
        )
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.backup-*")))

    def test_double_failure_reports_the_backup_and_recovery_command(self) -> None:
        self._seed_live_cache()
        staging = self.root / "staging"
        staging.mkdir()
        original_replace = os.replace
        calls = {"count": 0}

        def replace_failing_on_promotion_and_rollback(source, destination):
            calls["count"] += 1
            if calls["count"] in (2, 3):
                raise OSError("replace refused")
            return original_replace(source, destination)

        with (
            patch("os.replace", side_effect=replace_failing_on_promotion_and_rollback),
            self.assertRaises(OSError) as ctx,
        ):
            checker._swap_cache_home(staging, self.home)
        backup = self.root / f".{self.home.name}.backup-{os.getpid()}"
        message = str(ctx.exception)
        # The promotion error stays the primary cause.
        self.assertIn("replace refused", message)
        # The backup location and the quoted recovery command are named.
        self.assertIn(str(backup), message)
        recovery = f"mv -- {shlex.quote(str(backup))} {shlex.quote(str(self.home))}"
        self.assertIn(recovery, message)
        # The backup with the live cache is still there.
        self.assertTrue(backup.is_dir())
        self.assertEqual(
            {("https://example.invalid/foo", "aaaa")},
            checker._cached_repos(backup),
        )

    def test_recovery_command_is_quoted_for_paths_with_spaces(self) -> None:
        home = self.root / "cache with space"
        _make_db(home / "db.db", [("https://example.invalid/foo", "aaaa")])
        staging = self.root / "staging"
        staging.mkdir()
        original_replace = os.replace
        calls = {"count": 0}

        def replace_failing_on_promotion_and_rollback(source, destination):
            calls["count"] += 1
            if calls["count"] in (2, 3):
                raise OSError("replace refused")
            return original_replace(source, destination)

        with (
            patch("os.replace", side_effect=replace_failing_on_promotion_and_rollback),
            self.assertRaises(OSError) as ctx,
        ):
            checker._swap_cache_home(staging, home)
        backup = self.root / f".{home.name}.backup-{os.getpid()}"
        recovery = f"mv -- {shlex.quote(str(backup))} {shlex.quote(str(home))}"
        self.assertIn(recovery, str(ctx.exception))
        self.assertIn(str(backup), str(ctx.exception))

    def test_successful_promotion_removes_the_backup(self) -> None:
        self._seed_live_cache()
        staging = self.root / "staging"
        _make_db(staging / "db.db", [("https://example.invalid/bar", "bbbb")])
        checker._swap_cache_home(staging, self.home)
        self.assertEqual(
            {("https://example.invalid/bar", "bbbb")},
            checker._cached_repos(self.home),
        )
        self.assertEqual([], list(self.root.glob(f".{self.home.name}.backup-*")))


class ChangedFilesTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name).resolve()
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
        self.root = Path(temporary_directory.name).resolve()
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
        paths = {}
        for index, row in enumerate(rows):
            repo_dir = packed / f"repo{index}"
            repo_dir.mkdir(parents=True)
            (repo_dir / ".pre-commit-hooks.yaml").write_text(
                "hooks: []\n", encoding="utf-8"
            )
            paths[row] = str(self.home / f"repo{index}")
        _make_db(packed / "db.db", rows, paths)
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
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
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

    def test_missing_local_dependency_environment_fails_before_config_validation(
        self,
    ) -> None:
        _make_db(self.home / "db.db", [])
        with (
            patch.object(
                checker,
                "_load_config",
                return_value=_CONFIG_LOCAL_DEPENDENCIES,
            ),
            patch("subprocess.run") as mock_run,
        ):
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(1, rc)
        mock_run.assert_not_called()
        self.assertIn("local:pyyaml==6.0.3 @ 1", buf.getvalue())

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
