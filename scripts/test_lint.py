from __future__ import annotations

import contextlib
import io
import json
import os
import re
import shlex
import shutil
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zlib
from pathlib import Path
from unittest.mock import ANY, call, patch

if __package__:
    from scripts import lint as checker
    from scripts import pre_commit_cache
else:
    import lint as checker
    import pre_commit_cache


# env_specs sentinel: link an environment's bin/python to the running
# interpreter, as a real virtualenv links to its base interpreter.
_RUNTIME = "runtime"


def _expected_python_env_name() -> str:
    """Return pre-commit's python environment suffix, computed independently.

    The mirror is independent of ``pre_commit``: the gate invariant
    rejects third-party imports in a gate entry script, so the earlier
    optional ``pre_commit`` import was removed. The body reproduces
    pre-commit's ``get_default_version`` with the standard library
    alone. It stays separate from ``checker._default_python_language_version``
    so the tests remain a cross-check: ``sys.version_info`` alone
    mispredicts the name where PATH holds no versioned interpreter,
    and pre-commit then selects ``python3`` even under Python 3.14.
    """
    major = str(sys.version_info[0])
    minor = f"{sys.version_info[0]}.{sys.version_info[1]}"
    implementation = (
        "python" if sys.implementation.name == "cpython" else sys.implementation.name
    )
    for potential in (minor, major):
        executable = f"{implementation}{potential}"
        if shutil.which(executable):
            return executable
    for executable_path in (sys.executable, os.path.realpath(sys.executable)):
        executable = _sys_executable_name(executable_path, implementation)
        if executable is not None and shutil.which(executable):
            return executable
    return "default"


def _sys_executable_name(executable_path: str, implementation: str) -> str | None:
    """Return the interpreter name pre-commit accepts for *executable_path*.

    Mirror pre-commit's ``_find_by_sys_executable`` with its
    ``_sys_executable_matches`` check: the lowercased basename, with
    any ``.exe`` suffix removed, must not be the bare ``python`` or
    ``pythonw`` name, must start with the running implementation, and
    its version part must match the running ``sys.version_info``.
    """
    name = Path(executable_path.lower()).name.partition(".exe")[0]
    if name in {"python", "pythonw"}:
        return None
    if not name.startswith(implementation):
        return None
    try:
        version = tuple(
            int(part) for part in name.removeprefix(implementation).split(".")
        )
    except ValueError:
        return None
    return name if sys.version_info[: len(version)] == version else None


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


def _pre_commit_interpreters() -> list[str]:
    """Return interpreters that may have pre-commit installed, in order."""
    candidates = []
    executable = shutil.which("pre-commit")
    if executable is not None:
        try:
            first_line = (
                Path(executable)
                .read_text(encoding="utf-8", errors="replace")
                .splitlines()[0]
            )
        except (OSError, IndexError):
            first_line = ""
        if first_line.startswith("#!"):
            parts = shlex.split(first_line[2:].strip())
            if parts:
                if Path(parts[0]).name == "env" and len(parts) > 1:
                    if Path(parts[1]).name.startswith("python"):
                        resolved = shutil.which(parts[1])
                        if resolved is not None:
                            candidates.append(resolved)
                elif Path(parts[0]).name.startswith("python"):
                    candidates.append(parts[0])
    candidates.append(sys.executable)
    return list(dict.fromkeys(candidates))


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


class PythonEnvironmentHealthTests(unittest.TestCase):
    """The pre-flight health check mirrors pre-commit's own requirements."""

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.environment = Path(temporary_directory.name) / "py_env-test"
        _add_environment_health(self.environment)

    def _current_python(self) -> str:
        return (
            f"{sys.version_info.major}.{sys.version_info.minor}."
            f"{sys.version_info.micro}.final.0"
        )

    def _append_cfg(self, line: str) -> None:
        cfg = self.environment / "pyvenv.cfg"
        cfg.write_text(cfg.read_text(encoding="utf-8") + line + "\n", encoding="utf-8")

    def test_missing_version_info_is_unhealthy(self) -> None:
        (self.environment / "pyvenv.cfg").write_text("home = /usr\n", encoding="utf-8")

        self.assertFalse(checker._python_environment_is_healthy(self.environment))
        self.assertFalse(
            checker._python_environment_is_healthy(
                self.environment, verify_version=True
            )
        )

    def test_relative_base_executable_is_unhealthy(self) -> None:
        self._append_cfg("base-executable = python3")

        self.assertFalse(checker._python_environment_is_healthy(self.environment))

    def test_absolute_base_executable_path_is_healthy(self) -> None:
        """A pyenv or uv-managed base interpreter is a valid live environment."""
        self._append_cfg("base-executable = /opt/python3")
        original_is_file = Path.is_file

        def is_file_with_python3(path):
            if str(path) == "/opt/python3":
                return True
            return original_is_file(path)

        with (
            patch.object(
                Path, "is_file", side_effect=is_file_with_python3, autospec=True
            ),
            patch.object(
                checker,
                "_probe_interpreter",
                side_effect=[
                    (self._current_python(), None),
                    (self._current_python(), None),
                ],
            ),
        ):
            self.assertTrue(
                checker._python_environment_is_healthy(
                    self.environment, verify_version=True
                )
            )

    def test_missing_base_executable_is_unhealthy(self) -> None:
        self._append_cfg("base-executable = /opt/missing-python")
        original_is_file = Path.is_file

        def is_file_without_missing(path):
            if str(path) == "/opt/missing-python":
                return False
            return original_is_file(path)

        with (
            patch.object(
                Path, "is_file", side_effect=is_file_without_missing, autospec=True
            ),
            patch.object(
                checker,
                "_probe_interpreter",
                return_value=(self._current_python(), None),
            ),
        ):
            self.assertFalse(
                checker._python_environment_is_healthy(
                    self.environment, verify_version=True
                )
            )

    def test_mismatched_base_executable_version_is_unhealthy(self) -> None:
        self._append_cfg("base-executable = /usr/bin/python3")
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
                side_effect=[(self._current_python(), None), ("9.9.9.final.0", None)],
            ),
        ):
            self.assertFalse(
                checker._python_environment_is_healthy(
                    self.environment, verify_version=True
                )
            )


class PythonLanguageVersionTests(unittest.TestCase):
    def test_matches_pre_commit_default_version(self) -> None:
        """Pin the copied logic to pre-commit's own default-version contract."""
        repo_root = Path(__file__).resolve().parent.parent
        script = (
            "import sys\n"
            f"sys.path.insert(0, {str(repo_root / 'scripts')!r})\n"
            "from pre_commit.languages.python import get_default_version\n"
            "import lint as checker\n"
            "assert get_default_version() == "
            "checker._default_python_language_version()\n"
        )
        for interpreter in _pre_commit_interpreters():
            result = subprocess.run(
                [interpreter, "-c", script],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode == 0:
                return
            if (
                "ModuleNotFoundError" not in result.stderr
                and "ImportError" not in result.stderr
            ):
                self.fail(
                    "pre-commit version comparison failed under "
                    f"{interpreter}: {result.stderr}"
                )
        self.skipTest("pre-commit is not installed")

    def test_uses_versioned_interpreter_found_on_path(self) -> None:
        """Select the versioned executable when pre-commit would find it."""
        expected = f"python{sys.version_info.major}.{sys.version_info.minor}"

        def which(name: str) -> str | None:
            return f"/usr/bin/{name}" if name == expected else None

        with patch("shutil.which", side_effect=which):
            self.assertEqual(expected, _expected_python_env_name())

    def test_uses_major_only_interpreter_found_on_path(self) -> None:
        """Probe the major-only name next, as pre-commit does."""
        expected = f"python{sys.version_info.major}"

        def which(name: str) -> str | None:
            return f"/usr/bin/{name}" if name == expected else None

        with patch("shutil.which", side_effect=which):
            self.assertEqual(expected, _expected_python_env_name())

    def test_falls_back_to_the_running_interpreter_name(self) -> None:
        """Accept the running interpreter's own name when no probe hits.

        The ``major.minor.micro`` name differs from both probed names,
        so only the ``sys.executable`` fallback can return it.
        """
        expected = (
            f"python{sys.version_info.major}.{sys.version_info.minor}."
            f"{sys.version_info.micro}"
        )

        def which(name: str) -> str | None:
            return f"/usr/bin/{name}" if name == expected else None

        with (
            patch("shutil.which", side_effect=which),
            patch("sys.executable", f"/opt/bin/{expected}"),
        ):
            self.assertEqual(expected, _expected_python_env_name())

    def _assert_default_for(self, executable: str) -> None:
        """Assert the mirror returns ``default`` for *executable* on PATH."""

        def which(name: str) -> str | None:
            return f"/usr/bin/{name}" if name == executable else None

        with (
            patch("shutil.which", side_effect=which),
            patch("sys.executable", f"/opt/bin/{executable}"),
        ):
            self.assertEqual("default", _expected_python_env_name())

    def test_rejects_names_that_do_not_name_the_running_version(self) -> None:
        """Reject a findable name for another version or implementation."""
        other = f"python{sys.version_info.major}.{sys.version_info.minor + 1}"
        for executable in (other, "python-custom", "pypy3"):
            with self.subTest(executable=executable):
                self._assert_default_for(executable)

    def test_returns_the_default_marker_when_nothing_resolves(self) -> None:
        """Return pre-commit's ``default`` marker after every name fails."""
        with (
            patch("shutil.which", return_value=None),
            patch("sys.executable", "/opt/bin/python"),
        ):
            self.assertEqual("default", _expected_python_env_name())

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


class ExistingConfigPathsTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name).resolve()

    def test_returns_the_existing_configurations_gate_first(self) -> None:
        gate = self.root / "gate.yaml"
        ci = self.root / "ci.yaml"
        absent_gate = self.root / "absent-gate.yaml"
        absent_ci = self.root / "absent-ci.yaml"
        gate.write_text("repos: []\n", encoding="utf-8")
        ci.write_text("repos: []\n", encoding="utf-8")
        for case, gate_path, ci_path, expected in (
            ("both present", gate, ci, [gate, ci]),
            ("merge skew: only the gate", gate, absent_ci, [gate]),
            ("none present", absent_gate, absent_ci, []),
        ):
            with (
                self.subTest(case=case),
                patch.object(checker, "CONFIG_PATH", gate_path),
                patch.object(checker, "CI_CONFIG_PATH", ci_path),
            ):
                self.assertEqual(expected, checker._existing_config_paths())


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
        self.assertNotIn("local:pyyaml==6.0.3 @ 1", message)
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

    def test_environment_with_stale_patch_version_is_missing(self) -> None:
        """A live env whose interpreter disagrees with pyvenv.cfg is rebuilt."""
        repo_path = self.home / "repo-local"
        environment_path = repo_path / f"py_env-{_expected_python_env_name()}"
        _add_environment_health(environment_path)
        (environment_path / "pyvenv.cfg").write_text(
            f"version_info = {sys.version_info.major}.{sys.version_info.minor}."
            f"{sys.version_info.micro + 1}.final.0\n",
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
        self.assertIn("local hook dependency environment", buf.getvalue())

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
        self.assertNotIn("local hook dependency environment", message)
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
        """The suffix pre-commit's default names the environment directory.

        pre-commit probes PATH for ``python<X.Y>`` and falls back to
        ``python<X>``, so the name comes from its default, not from
        ``sys.version_info`` directly: a minimal PATH yields
        ``py_env-python3`` even under Python 3.14.
        """
        return _expected_python_env_name().removeprefix("python")

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

    _LOCAL_ROW = ("local:pyyaml==6.0.3", "1")

    def _build_local_dependency_tarball(self, cfg_lines: list[str]) -> None:
        """Write a tarball with the configured local dependency environment."""
        packed = self.root / "packed"
        repo_dir = packed / "repo-local"
        environment = repo_dir / f"py_env-python{self._current_interpreter()}"
        (environment / "bin").mkdir(parents=True)
        (environment / "pyvenv.cfg").write_text(
            "\n".join(cfg_lines) + "\n", encoding="utf-8"
        )
        os.symlink(sys.executable, environment / "bin" / "python")
        (environment / ".install_state_v2").touch()
        _make_db(
            packed / "db.db",
            [self._LOCAL_ROW],
            {self._LOCAL_ROW: str(self.home / "repo-local")},
        )
        with tarfile.open(self.tarball, "w:gz") as tar:
            for path in sorted(packed.rglob("*")):
                tar.add(path, arcname=path.relative_to(packed), recursive=False)

    def test_stale_patch_live_environment_triggers_restore(self) -> None:
        """The restore trigger uses the same dynamic check as the gate."""
        repo_dir = self.home / "repo-local"
        environment = repo_dir / f"py_env-python{self._current_interpreter()}"
        _add_environment_health(environment)
        (environment / "pyvenv.cfg").write_text(
            f"version_info = {self._stale_patch_version()}\n", encoding="utf-8"
        )
        (environment / ".install_state_v2").touch()
        _make_db(
            self.home / "db.db",
            [self._LOCAL_ROW],
            {self._LOCAL_ROW: str(repo_dir)},
        )
        self._build_local_dependency_tarball(
            [f"version_info = {self._current_python()}"]
        )

        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_LOCAL_DEPENDENCIES
            )

        self.assertEqual(0, rc, stderr.getvalue())
        self.assertIn("Restored the offline pre-commit cache", stdout.getvalue())

    def test_archive_environment_without_version_info_is_not_promoted(self) -> None:
        """An archive that the next pre-flight would reject stays unpromoted."""
        self._build_local_dependency_tarball(["home = /usr"])

        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            rc = checker._preflight_with_restore(
                False, self.home, _CONFIG_LOCAL_DEPENDENCIES
            )

        self.assertEqual(1, rc)
        self.assertIn(
            "cannot provide the configured local hook dependency environment",
            stderr.getvalue(),
        )
        self.assertNotIn("Restored", stdout.getvalue())
        self.assertTrue(self.tarball.exists())

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

    def test_run_path_flushes_the_restore_line_before_the_child_runs(self) -> None:
        # The child inherits this process's stdout, so a block-buffered
        # stream, as under a shell redirect, must be flushed before the
        # child runs; otherwise the restore line appears after the
        # child's own output.
        self._build_tarball(self._config_rows())
        raw = io.BytesIO()
        buffered = io.TextIOWrapper(raw, encoding="utf-8")
        real_run = subprocess.run

        def run_stub(command, **kwargs):
            if command[:1] == ["pre-commit"]:
                return subprocess.CompletedProcess(args=list(command), returncode=0)
            return real_run(command, **kwargs)

        with (
            patch.dict(os.environ, {"PRE_COMMIT_HOME": str(self.home)}, clear=False),
            patch.object(
                checker,
                "_existing_config_paths",
                return_value=[checker.CONFIG_PATH],
            ),
            patch.object(
                checker,
                "_load_config",
                return_value=_CONFIG_TWO_REMOTE_ONE_LOCAL,
            ),
            patch("subprocess.run", side_effect=run_stub) as mock_run,
            patch("sys.stdout", buffered),
        ):
            rc = checker.main(["--files", "README.md"])
        mock_run.assert_any_call(
            [
                "pre-commit",
                "run",
                "--config",
                str(checker.CONFIG_PATH),
                "--files",
                "README.md",
            ],
            cwd=str(checker.REPO_ROOT),
            check=False,
        )
        self.assertEqual(0, rc)
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
    """``main()`` translates flags into ``pre-commit run`` invocations.

    Every case below is already cached (satisfied pre-flight) so these
    tests exercise CLI translation, not the cache gate covered by
    ``PreflightTests``. ``_existing_config_paths`` is pinned to both
    configurations so the cases stay deterministic on merge-skew trees
    where ``.github/pre-commit-ci.yaml`` does not exist yet.
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
        # Both configurations exist and hold no cloned repositories or
        # pinned local dependencies, so the cache is trivially
        # satisfied.
        self.gate_config = {"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]}
        self.ci_config = {"repos": [{"repo": "local", "hooks": [{"id": "y"}]}]}
        paths_patcher = patch.object(
            checker,
            "_existing_config_paths",
            return_value=[checker.CONFIG_PATH, checker.CI_CONFIG_PATH],
        )
        paths_patcher.start()
        self.addCleanup(paths_patcher.stop)
        config_patcher = patch.object(
            checker,
            "_load_config",
            side_effect=self._config_for_path,
        )
        self.load_config_mock = config_patcher.start()
        self.addCleanup(config_patcher.stop)

    def _config_for_path(self, path: Path) -> dict[str, object]:
        if path == checker.CONFIG_PATH:
            return self.gate_config
        return self.ci_config

    def _expected_run_calls(
        self, scope_args: list[str], paths: tuple[Path, ...] | None = None
    ) -> list:
        configurations = paths or (checker.CONFIG_PATH, checker.CI_CONFIG_PATH)
        return [
            call(
                ["pre-commit", "run", "--config", str(path), *scope_args],
                cwd=str(checker.REPO_ROOT),
                check=False,
            )
            for path in configurations
        ]

    def test_all_files_maps_to_pre_commit_all_files(self) -> None:
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main(["--all-files"])
        self.assertEqual(
            self._expected_run_calls(["--all-files"]), mock_run.call_args_list
        )
        self.assertEqual(0, rc)

    def test_files_maps_to_pre_commit_files(self) -> None:
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main(["--files", "a.py", "b.yaml"])
        self.assertEqual(
            self._expected_run_calls(["--files", "a.py", "b.yaml"]),
            mock_run.call_args_list,
        )
        self.assertEqual(0, rc)

    def test_bare_mode_maps_to_changed_files(self) -> None:
        with (
            patch.object(checker, "_changed_files", return_value=["x.py"]),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main([])
        self.assertEqual(
            self._expected_run_calls(["--files", "x.py"]), mock_run.call_args_list
        )
        self.assertEqual(0, rc)

    def test_bare_mode_with_no_changes_and_no_always_run_skips_pre_commit(
        self,
    ) -> None:
        with (
            patch.object(checker, "_changed_files", return_value=[]),
            patch("subprocess.run") as mock_run,
        ):
            rc = checker.main([])
        mock_run.assert_not_called()
        self.assertEqual(0, rc)

    def test_bare_mode_with_no_changes_but_always_run_hook_still_runs(self) -> None:
        # An always_run hook in the gate keeps every configuration
        # running with --files and no file arguments.
        self.gate_config = {
            "repos": [{"repo": "local", "hooks": [{"id": "a", "always_run": True}]}]
        }
        with (
            patch.object(checker, "_changed_files", return_value=[]),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main([])
        self.assertEqual(self._expected_run_calls(["--files"]), mock_run.call_args_list)
        self.assertEqual(0, rc)

    def test_bare_mode_with_no_changes_but_ci_always_run_hook_still_runs(self) -> None:
        # An always_run hook in ANY configuration is enough: bare mode
        # must not print "No files to check." while the CI
        # configuration still has work to do.
        self.ci_config = {
            "repos": [{"repo": "local", "hooks": [{"id": "a", "always_run": True}]}]
        }
        with (
            patch.object(checker, "_changed_files", return_value=[]),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main([])
        self.assertEqual(self._expected_run_calls(["--files"]), mock_run.call_args_list)
        self.assertEqual(0, rc)

    def test_offline_bare_mode_loads_each_config_once_and_preflights_each_once(
        self,
    ) -> None:
        # Regression test: the offline pre-flight and bare mode's
        # always_run check must share one read per configuration, not
        # two, and each configuration must be pre-flighted exactly
        # once: the gate through the restore wrapper (whose call is
        # the gate's pre-flight), the CI configuration through
        # _preflight. The stubs force offline mode and skip the hook
        # cache, so this runs the same way with and without CI=true.
        with (
            patch.object(checker, "_preflight", return_value=0) as mock_preflight,
            patch.object(
                checker, "_preflight_with_restore", return_value=0
            ) as mock_restore,
            patch.object(checker, "_changed_files", return_value=[]),
            patch("subprocess.run") as mock_run,
        ):
            rc = checker.main([])
        self.assertEqual(
            [
                call(checker.CONFIG_PATH),
                call(checker.CI_CONFIG_PATH),
            ],
            self.load_config_mock.call_args_list,
        )
        # Exactly one pre-flight per configuration, with no second
        # pre-flight of the gate; each call also names its
        # configuration so the remedy fetches the right revisions.
        mock_restore.assert_called_once_with(
            False, ANY, self.gate_config, checker.CONFIG_PATH
        )
        mock_preflight.assert_called_once_with(
            False, ANY, self.ci_config, checker.CI_CONFIG_PATH
        )
        mock_run.assert_not_called()
        self.assertEqual(0, rc)

    def test_missing_ci_config_runs_only_the_gate_configuration(self) -> None:
        # Merge skew: a branch that predates the cutover has no
        # .github/pre-commit-ci.yaml, so only the gate configuration
        # runs and is read.
        with (
            patch.object(
                checker, "_existing_config_paths", return_value=[checker.CONFIG_PATH]
            ),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            rc = checker.main(["--all-files"])
        self.assertEqual(
            self._expected_run_calls(["--all-files"], (checker.CONFIG_PATH,)),
            mock_run.call_args_list,
        )
        self.assertEqual(
            [call(checker.CONFIG_PATH)], self.load_config_mock.call_args_list
        )
        self.assertEqual(0, rc)

    def test_missing_gate_config_is_an_error(self) -> None:
        # The gate configuration is required; unlike a missing CI
        # configuration it is not merge skew.
        absent_gate = self.home.parent / "absent-gate.yaml"
        with (
            patch.object(checker, "CONFIG_PATH", absent_gate),
            patch("subprocess.run") as mock_run,
        ):
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--all-files"])
        self.assertEqual(1, rc)
        mock_run.assert_not_called()
        self.assertIn(str(absent_gate), buf.getvalue())

    def test_per_config_preflight_failure_names_the_failing_configuration(
        self,
    ) -> None:
        # The gate cache is satisfied, but the CI configuration names a
        # repository revision the cache does not hold: the failure must
        # name .github/pre-commit-ci.yaml and stop before any hook or
        # validation runs.
        (self.home / "db.db").unlink()
        _make_db(
            self.home / "db.db",
            [("https://example.invalid/foo", "aaaa")],
        )
        self.ci_config = {
            "repos": [
                {
                    "repo": "https://example.invalid/foo",
                    "rev": "bbbb",
                    "hooks": [{"id": "foo"}],
                }
            ]
        }
        with patch("subprocess.run") as mock_run:
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--all-files"])
        self.assertEqual(1, rc)
        mock_run.assert_not_called()
        self.assertIn(".github/pre-commit-ci.yaml", buf.getvalue())
        self.assertIn("https://example.invalid/foo @ bbbb", buf.getvalue())
        # The remedy must target the CI configuration: install-hooks
        # defaults to the gate file and cannot fetch these revisions.
        message = buf.getvalue()
        self.assertIn("--config .github/pre-commit-ci.yaml", message)
        self.assertNotIn("--config .pre-commit-config.yaml", message)

    def test_every_configuration_runs_when_the_first_fails(self) -> None:
        for return_codes, expected in (
            ([1, 2], 1),  # both fail: the FIRST non-zero code wins
            ([0, 3], 3),  # only the CI configuration fails: it propagates
        ):
            with self.subTest(return_codes=return_codes):
                with patch("subprocess.run") as mock_run:
                    mock_run.side_effect = [
                        subprocess.CompletedProcess(args=[], returncode=code)
                        for code in return_codes
                    ]
                    rc = checker.main(["--all-files"])
                self.assertEqual(2, mock_run.call_count)
                self.assertEqual(expected, rc)

    def test_failing_configurations_are_named(self) -> None:
        # The exit code alone cannot say which configuration failed;
        # one stderr line names every failing configuration, and a
        # fully passing run prints no such line.
        for return_codes, expected_line in (
            (
                [0, 3],
                "ERROR: these configurations failed: .github/pre-commit-ci.yaml",
            ),
            (
                [1, 2],
                (
                    "ERROR: these configurations failed: .pre-commit-config.yaml, "
                    ".github/pre-commit-ci.yaml"
                ),
            ),
        ):
            with self.subTest(return_codes=return_codes):
                with patch("subprocess.run") as mock_run:
                    mock_run.side_effect = [
                        subprocess.CompletedProcess(args=[], returncode=code)
                        for code in return_codes
                    ]
                    buf = io.StringIO()
                    with contextlib.redirect_stderr(buf):
                        checker.main(["--all-files"])
                self.assertIn(expected_line, buf.getvalue())
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--all-files"])
        self.assertEqual(0, rc)
        self.assertNotIn("these configurations failed", buf.getvalue())

    def test_missing_pre_commit_binary_fails_with_the_pinned_version(self) -> None:
        # A missing pre-commit binary must not raise a traceback. Both
        # configurations are attempted, one clear error names the
        # pinned version, and the summary names both configurations.
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = FileNotFoundError("pre-commit")
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--all-files"])
        self.assertEqual(1, rc)
        self.assertEqual(2, mock_run.call_count)
        message = buf.getvalue()
        self.assertIn("cannot run pre-commit", message)
        self.assertEqual(1, message.count("cannot run pre-commit"))
        self.assertIn(checker.PRE_COMMIT_VERSION, message)
        self.assertIn("--print-pre-commit-version", message)
        self.assertIn(
            "ERROR: these configurations failed: .pre-commit-config.yaml, "
            ".github/pre-commit-ci.yaml",
            message,
        )

    def test_launch_failure_still_runs_the_remaining_configurations(self) -> None:
        # The gate configuration cannot launch; the CI configuration
        # must still be attempted, and the summary must name only the
        # gate.
        with patch.object(
            checker, "_run_pre_commit", side_effect=[None, 0]
        ) as mock_run_pre_commit:
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--all-files"])
        self.assertEqual(
            [
                [
                    "pre-commit",
                    "run",
                    "--config",
                    str(checker.CONFIG_PATH),
                    "--all-files",
                ],
                [
                    "pre-commit",
                    "run",
                    "--config",
                    str(checker.CI_CONFIG_PATH),
                    "--all-files",
                ],
            ],
            [c.args[0] for c in mock_run_pre_commit.call_args_list],
        )
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn(
            "ERROR: these configurations failed: .pre-commit-config.yaml", message
        )
        self.assertNotIn(".github/pre-commit-ci.yaml", message)

    def test_launch_failure_of_every_configuration_names_both(self) -> None:
        # Both configurations cannot launch: one clear error (not one
        # per configuration), the summary names both, and the return
        # value is 1.
        with patch.object(
            checker, "_run_pre_commit", return_value=None
        ) as mock_run_pre_commit:
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--all-files"])
        self.assertEqual(2, mock_run_pre_commit.call_count)
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn(
            "ERROR: these configurations failed: .pre-commit-config.yaml, "
            ".github/pre-commit-ci.yaml",
            message,
        )
        self.assertEqual(1, message.count("cannot run pre-commit"))


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
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            os.environ["PRE_COMMIT_HOME"] = str(self.home)
            checker.main(["--all-files"])
            self.assertEqual("true", os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))

    def test_online_does_not_force_zizmor_offline(self) -> None:
        with (
            patch.dict(os.environ, self._clean_env(), clear=True),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            os.environ["PRE_COMMIT_HOME"] = str(self.home)
            checker.main(["--all-files", "--online"])
            self.assertNotIn("ZIZMOR_NO_ONLINE_AUDITS", os.environ)

    def test_zizmor_flag_is_not_overridden_when_caller_set_it(self) -> None:
        with (
            patch.dict(os.environ, self._clean_env(), clear=True),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
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
        # Both configurations exist (deterministic on merge-skew
        # trees) and hold no cloned repositories, so the cache is
        # trivially satisfied; tests that need remote repositories
        # override self.gate_config or self.ci_config.
        self.gate_config = {"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]}
        self.ci_config = {"repos": [{"repo": "local", "hooks": [{"id": "y"}]}]}
        paths_patcher = patch.object(
            checker,
            "_existing_config_paths",
            return_value=[checker.CONFIG_PATH, checker.CI_CONFIG_PATH],
        )
        paths_patcher.start()
        self.addCleanup(paths_patcher.stop)
        config_patcher = patch.object(
            checker,
            "_load_config",
            side_effect=self._config_for_path,
        )
        config_patcher.start()
        self.addCleanup(config_patcher.stop)

    def _config_for_path(self, path: Path) -> dict[str, object]:
        if path == checker.CONFIG_PATH:
            return self.gate_config
        return self.ci_config

    def _validate_call(self, path: Path) -> call:
        return call(
            ["pre-commit", "validate-config", str(path)],
            cwd=str(checker.REPO_ROOT),
            check=False,
        )

    def test_cache_miss_reports_before_validating_config(self) -> None:
        self.gate_config = _CONFIG_TWO_REMOTE_ONE_LOCAL
        with patch("subprocess.run") as mock_run:
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--check-parity"])
        mock_run.assert_not_called()
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn("pre-commit install-hooks", message)
        self.assertIn(".pre-commit-config.yaml", message)
        # The remedy fetches the revisions of the configuration that
        # reported them: install-hooks defaults to the gate file, so
        # the remedy must name it.
        self.assertIn("--config .pre-commit-config.yaml", message)
        # The gate's missing-revision block prints exactly once: the
        # restore wrapper's call is the gate's only pre-flight, so a
        # cold cache never duplicates the report.
        self.assertEqual(1, message.count("missing 2 hook repository revisions"))
        self.assertEqual(1, message.count("pre-commit install-hooks"))

    def test_cold_cache_with_tarball_restores_then_validates(self) -> None:
        self.gate_config = _CONFIG_TWO_REMOTE_ONE_LOCAL
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
            patch.object(checker, "_TARBALL_DELETE_ROOT", self.root),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(0, rc)
        self.assertEqual(
            [
                self._validate_call(checker.CONFIG_PATH),
                self._validate_call(checker.CI_CONFIG_PATH),
            ],
            mock_run.call_args_list,
        )
        self.assertIn("Restored the offline pre-commit cache", buf.getvalue())
        self.assertIn("PARITY CHECK PASSED", buf.getvalue())
        self.assertFalse(tarball.exists())

    def test_cache_hit_runs_validate_config(self) -> None:
        _make_db(self.home / "db.db", [])
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(0, rc)
        self.assertEqual(
            [
                self._validate_call(checker.CONFIG_PATH),
                self._validate_call(checker.CI_CONFIG_PATH),
            ],
            mock_run.call_args_list,
        )
        # The success line names both configuration paths and, because
        # the offline pre-flight verified the cache, keeps the cache
        # claim.
        message = buf.getvalue()
        self.assertIn("PARITY CHECK PASSED", message)
        self.assertIn(".pre-commit-config.yaml", message)
        self.assertIn(".github/pre-commit-ci.yaml", message)
        self.assertIn("offline cache", message)

    def test_missing_pre_commit_binary_fails_with_the_pinned_version(self) -> None:
        # A missing pre-commit binary must not raise a traceback; the
        # error names the pinned version to install.
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = FileNotFoundError("pre-commit")
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(1, rc)
        message = buf.getvalue()
        self.assertIn("cannot run pre-commit", message)
        self.assertIn(checker.PRE_COMMIT_VERSION, message)
        self.assertIn("--print-pre-commit-version", message)

    def test_validate_config_failure_propagates_exit_code(self) -> None:
        _make_db(self.home / "db.db", [])
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=1)
            rc = checker.main(["--check-parity"])
        self.assertEqual(1, rc)

    def test_ci_config_validate_failure_propagates(self) -> None:
        # The gate configuration validates, the CI configuration does
        # not: both run and the CI failure decides the exit code.
        _make_db(self.home / "db.db", [])
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0),
                subprocess.CompletedProcess(args=[], returncode=1),
            ]
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(1, rc)
        self.assertEqual(2, mock_run.call_count)
        self.assertNotIn("PARITY CHECK PASSED", buf.getvalue())

    def test_online_check_parity_skips_cache_but_still_validates(self) -> None:
        # No db.db at all; --online must bypass the cache gate and
        # read no configuration, yet still validate each one. The
        # pre-flight did not verify the cache, so the success line
        # claims validity only — no cache claim.

        def _fail_if_called(path: Path) -> None:
            self.fail("online --check-parity must not read a configuration")

        with (
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity", "--online"])
        self.assertEqual(0, rc)
        self.assertEqual(
            [
                self._validate_call(checker.CONFIG_PATH),
                self._validate_call(checker.CI_CONFIG_PATH),
            ],
            mock_run.call_args_list,
        )
        message = buf.getvalue()
        self.assertIn("PARITY CHECK PASSED", message)
        self.assertIn(".github/pre-commit-ci.yaml", message)
        self.assertNotIn("offline cache", message)

    def test_absent_ci_config_validates_only_the_gate_config(self) -> None:
        # Merge skew: without .github/pre-commit-ci.yaml only the gate
        # configuration is validated and named.
        with (
            patch.object(
                checker, "_existing_config_paths", return_value=[checker.CONFIG_PATH]
            ),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(0, rc)
        self.assertEqual(
            [self._validate_call(checker.CONFIG_PATH)], mock_run.call_args_list
        )
        message = buf.getvalue()
        self.assertIn("PARITY CHECK PASSED", message)
        self.assertIn(".pre-commit-config.yaml", message)
        self.assertNotIn(".github/pre-commit-ci.yaml", message)

    def test_missing_local_dependency_environment_fails_before_config_validation(
        self,
    ) -> None:
        # A local python hook with ``additional_dependencies`` needs its
        # ``local:`` row and a healthy environment in the cache; without
        # them the pre-flight fails before validate-config runs.
        self.gate_config = _CONFIG_LOCAL_DEPENDENCIES
        _make_db(self.home / "db.db", [])
        with patch("subprocess.run") as mock_run:
            buf = io.StringIO()
            with contextlib.redirect_stderr(buf):
                rc = checker.main(["--check-parity"])
        self.assertEqual(1, rc)
        mock_run.assert_not_called()
        self.assertIn("local:pyyaml==6.0.3 @ 1", buf.getvalue())


class PrintPreCommitVersionTests(unittest.TestCase):
    def test_prints_pinned_version_without_reading_config(self) -> None:
        def _fail_if_called(path: Path) -> None:
            self.fail("--print-pre-commit-version must not read the config")

        buf = io.StringIO()
        with (
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("subprocess.run") as mock_run,
            contextlib.redirect_stdout(buf),
        ):
            rc = checker.main(["--print-pre-commit-version"])
        self.assertEqual(0, rc)
        self.assertRegex(checker.PRE_COMMIT_VERSION, r"^\d+\.\d+\.\d+$")
        self.assertEqual(f"{checker.PRE_COMMIT_VERSION}\n", buf.getvalue())
        mock_run.assert_not_called()

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
                    patch("subprocess.run") as mock_run,
                    self.assertRaises(SystemExit) as raised,
                ):
                    checker.main(flags)
                self.assertEqual(2, raised.exception.code)
                mock_run.assert_not_called()
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
            patch("subprocess.run") as mock_run,
        ):
            rc = checker.main(["--print-pre-commit-version", "--online-if-ci"])
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        self.assertEqual(0, rc)
        self.assertEqual(f"{checker.PRE_COMMIT_VERSION}\n", buf.getvalue())
        mock_run.assert_not_called()


class PrintFullsendPreCommitVersionTests(unittest.TestCase):
    """``--print-fullsend-pre-commit-version`` mirrors the wrapper pin flag.

    The Fullsend pin is the version the sandbox image ships and the
    runner post-script installs when no executable resolves; the
    gate-rehearsal job installs it (ADR-0005).
    """

    def test_prints_fullsend_pin_without_reading_config(self) -> None:
        def _fail_if_called(path: Path) -> None:
            self.fail("--print-fullsend-pre-commit-version must not read the config")

        buf = io.StringIO()
        with (
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("subprocess.run") as mock_run,
            contextlib.redirect_stdout(buf),
        ):
            rc = checker.main(["--print-fullsend-pre-commit-version"])
        self.assertEqual(0, rc)
        self.assertRegex(checker.FULLSEND_PRE_COMMIT_VERSION, r"^\d+\.\d+\.\d+$")
        self.assertEqual(f"{checker.FULLSEND_PRE_COMMIT_VERSION}\n", buf.getvalue())
        mock_run.assert_not_called()

    def test_print_fullsend_version_rejects_mode_and_scope_flags(self) -> None:
        for flags in (
            ["--print-fullsend-pre-commit-version", "--check-parity"],
            ["--print-fullsend-pre-commit-version", "--all-files"],
            ["--print-fullsend-pre-commit-version", "--files", "a.py"],
        ):
            with self.subTest(flags=flags):
                buf = io.StringIO()
                with (
                    contextlib.redirect_stderr(buf),
                    patch("subprocess.run") as mock_run,
                    self.assertRaises(SystemExit) as raised,
                ):
                    checker.main(flags)
                self.assertEqual(2, raised.exception.code)
                mock_run.assert_not_called()
                self.assertIn("cannot be combined with", buf.getvalue())

    def test_fullsend_pin_equals_the_bootstrap_cache_pin(self) -> None:
        # ADR-0005: PRECOMMIT_VERSION in scripts/pre_commit_cache.py
        # must equal FULLSEND_PRE_COMMIT_VERSION until issue #581
        # retires the bootstrap.
        self.assertEqual(
            pre_commit_cache.PRECOMMIT_VERSION,
            checker.FULLSEND_PRE_COMMIT_VERSION,
        )


class MainCiInvocationTests(unittest.TestCase):
    """The CI invocation must work without reading any configuration.

    CI runs ``python3 scripts/lint.py --all-files --online-if-ci``
    under ``CI=true``. That path must read no configuration (so it
    needs no PyYAML), must not force zizmor's audits offline, and must
    still run one ``pre-commit run --config <path>`` invocation per
    existing configuration.
    """

    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.cache = Path(temporary_directory.name) / "cache"

    def _ci_env(self) -> dict[str, str]:
        env = dict(os.environ)
        env["CI"] = "true"
        env["PRE_COMMIT_HOME"] = str(self.cache)
        env.pop("ZIZMOR_NO_ONLINE_AUDITS", None)
        return env

    def test_ci_invocation_runs_pre_commit_without_config(self) -> None:
        def _fail_if_called(path: Path) -> None:
            self.fail("the CI invocation must not read the config")

        with (
            patch.dict(os.environ, self._ci_env(), clear=True),
            patch.object(
                checker,
                "_existing_config_paths",
                return_value=[checker.CONFIG_PATH, checker.CI_CONFIG_PATH],
            ),
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            checker.main(["--all-files", "--online-if-ci"])
            # Assert inside the patch: the wrapper must not set the
            # zizmor flag for this online run.
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        self.assertEqual(
            [
                call(
                    [
                        "pre-commit",
                        "run",
                        "--config",
                        str(checker.CONFIG_PATH),
                        "--all-files",
                    ],
                    cwd=str(checker.REPO_ROOT),
                    check=False,
                ),
                call(
                    [
                        "pre-commit",
                        "run",
                        "--config",
                        str(checker.CI_CONFIG_PATH),
                        "--all-files",
                    ],
                    cwd=str(checker.REPO_ROOT),
                    check=False,
                ),
            ],
            mock_run.call_args_list,
        )

    def test_ci_invocation_with_files_scope_runs_without_config(self) -> None:
        def _fail_if_called(path: Path) -> None:
            self.fail("the CI invocation must not read the config")

        with (
            patch.dict(os.environ, self._ci_env(), clear=True),
            patch.object(
                checker,
                "_existing_config_paths",
                return_value=[checker.CONFIG_PATH, checker.CI_CONFIG_PATH],
            ),
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            checker.main(["--online-if-ci", "--files", "a.py", "b.yaml"])
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        self.assertEqual(
            [
                call(
                    [
                        "pre-commit",
                        "run",
                        "--config",
                        str(checker.CONFIG_PATH),
                        "--files",
                        "a.py",
                        "b.yaml",
                    ],
                    cwd=str(checker.REPO_ROOT),
                    check=False,
                ),
                call(
                    [
                        "pre-commit",
                        "run",
                        "--config",
                        str(checker.CI_CONFIG_PATH),
                        "--files",
                        "a.py",
                        "b.yaml",
                    ],
                    cwd=str(checker.REPO_ROOT),
                    check=False,
                ),
            ],
            mock_run.call_args_list,
        )

    def test_ci_invocation_on_a_merge_skew_tree_runs_only_the_gate_config(self) -> None:
        # A branch that predates the cutover has no CI configuration:
        # the online CI invocation runs the gate configuration alone.
        def _fail_if_called(path: Path) -> None:
            self.fail("the CI invocation must not read the config")

        with (
            patch.dict(os.environ, self._ci_env(), clear=True),
            patch.object(
                checker, "_existing_config_paths", return_value=[checker.CONFIG_PATH]
            ),
            patch.object(checker, "_load_config", side_effect=_fail_if_called),
            patch("subprocess.run") as mock_run,
        ):
            mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
            checker.main(["--all-files", "--online-if-ci"])
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        mock_run.assert_called_once_with(
            [
                "pre-commit",
                "run",
                "--config",
                str(checker.CONFIG_PATH),
                "--all-files",
            ],
            cwd=str(checker.REPO_ROOT),
            check=False,
        )

    def test_online_bare_mode_reads_configs_only_for_the_always_run_decision(
        self,
    ) -> None:
        # Online bare mode with no changed files must decide whether
        # any configuration declares always_run; that decision may
        # read each configuration once, and nothing else may.
        gate_config = {"repos": [{"repo": "local", "hooks": [{"id": "x"}]}]}
        ci_config = {"repos": [{"repo": "local", "hooks": [{"id": "y"}]}]}

        def _config_for_path(path: Path) -> dict[str, object]:
            return gate_config if path == checker.CONFIG_PATH else ci_config

        with (
            patch.dict(os.environ, self._ci_env(), clear=True),
            patch.object(
                checker,
                "_existing_config_paths",
                return_value=[checker.CONFIG_PATH, checker.CI_CONFIG_PATH],
            ),
            patch.object(
                checker, "_load_config", side_effect=_config_for_path
            ) as mock_load_config,
            patch.object(checker, "_changed_files", return_value=[]),
            patch("subprocess.run") as mock_run,
        ):
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                rc = checker.main(["--online-if-ci"])
            self.assertIsNone(os.environ.get("ZIZMOR_NO_ONLINE_AUDITS"))
        self.assertEqual(
            [call(checker.CONFIG_PATH), call(checker.CI_CONFIG_PATH)],
            mock_load_config.call_args_list,
        )
        mock_run.assert_not_called()
        self.assertEqual(0, rc)
        self.assertIn("No files to check.", buf.getvalue())


# A `run:` key line. Group 1 is the indentation; group 2 is an
# optional quote pair around the key; group 3 is the inline value, if
# any. YAML also accepts `"run":`, `'run':`, and `run :`, so the key
# may carry a quote pair and spaces before the colon. A step may also
# be a sequence item with no name (`- run:` or a `- run: |` block), so
# an optional `- ` item prefix is accepted.
_RUN_KEY_LINE = re.compile(
    r"""^([ \t]*)(?:-[ \t]+)?(["']?)run\2[ \t]*:(?:[ \t]+(.*))?$"""
)

# A top-level job key under `jobs:` (exactly two spaces of indent).
# shortcut: keys under `on:` and `permissions:` also sit at two
# spaces, but they never match a job id, so they only close the
# current job; upgrade to a YAML parser if the workflow ever nests a
# two-space scalar block that starts like a job key.
_JOB_KEY_LINE = re.compile(r"^  (?P<job>[A-Za-z0-9_-]+):(?:[ \t]|$)")


def _split_job(text: str, job_id: str) -> tuple[str, str]:
    """Return ``(job text, remaining text)`` for top-level job *job_id*.

    The job text covers the job key line and every line until the next
    top-level key; the remaining text is everything else, so commands
    extracted from it cannot come from *job_id*. Returns ``("", text)``
    when the job is absent.
    """
    job: list[str] = []
    rest: list[str] = []
    into_job = False
    for line in text.splitlines():
        key = _JOB_KEY_LINE.match(line)
        if key is not None:
            into_job = key.group("job") == job_id
        (job if into_job else rest).append(line)
    return "\n".join(job), "\n".join(rest)


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


def _run_commands_by_step(text: str) -> list[list[str]]:
    """Return the ``run:`` command lines of *text*, grouped by step.

    Each ``run:`` key starts a new group holding that step's commands:
    the inline value, or every non-empty stripped line of the indented
    block that follows. The key may be quoted or spaced
    (``"run":``, ``'run':``, ``run :``) and the step may be a
    sequence item with no name (``- run:``). A block ends at the first
    non-blank line that is not indented deeper than the ``run`` key
    itself: for a ``- run: |`` item the key sits after the dash, so a
    sibling field such as ``name:`` at the key's indentation ends the
    block instead of becoming a command. Comment parts are removed
    first, so a comment that names a command does not count and a step
    name never becomes a command. The helper parses text only, so the
    tests need no PyYAML.
    """
    groups: list[list[str]] = []
    lines = _without_yaml_comments(text).splitlines()
    index = 0
    while index < len(lines):
        key = _RUN_KEY_LINE.match(lines[index])
        if key is None:
            index += 1
            continue
        value = key.group(3) or ""
        # The column where the run key (or its quote) starts; a block
        # must be indented deeper than this, not merely deeper than a
        # sequence dash.
        block_prefix = " " * (key.start(2) + 1)
        commands: list[str] = []
        if value[:1] in ("|", ">"):
            index += 1
            while index < len(lines):
                block_line = lines[index]
                if block_line.strip() and not block_line.startswith(block_prefix):
                    break
                if block_line.strip():
                    commands.append(block_line.strip())
                index += 1
        else:
            if value.strip():
                commands.append(value.strip())
            index += 1
        groups.append(commands)
    return groups


def _run_commands(text: str) -> list[str]:
    """Return the command lines of every ``run:`` value in *text*."""
    return [command for group in _run_commands_by_step(text) for command in group]


def _join_continuations(commands: list[str]) -> list[str]:
    """Join shell continuation lines into one command each.

    A line ending in a backslash continues on the next line, so
    ``env A=1 \\`` plus ``B=2 cmd`` is one ``env A=1 B=2 cmd``
    command. Comments are already removed by the extraction, so a
    comment between a line and its continuation cannot split it.
    """
    joined: list[str] = []
    pending = ""
    for command in commands:
        pending = f"{pending} {command}" if pending else command
        if pending.endswith("\\"):
            pending = pending[:-1].rstrip()
            continue
        joined.append(pending)
        pending = ""
    if pending:
        joined.append(pending)
    return joined


def _runs_under_env_i(command: str) -> bool:
    """Return whether *command* runs under ``env -i`` before ``pre-commit``."""
    head = command.split("pre-commit", 1)[0]
    return head.startswith("env -i") or " env -i " in f" {head}"


def _run_commands_joined(text: str) -> list[str]:
    """Return the ``run:`` commands of *text*, continuations joined.

    Joining happens inside each step's group, so a backslash at the
    end of one step's last line cannot merge it with the next step's
    first command.
    """
    return [
        command
        for group in _run_commands_by_step(text)
        for command in _join_continuations(group)
    ]


def _job_commands(text: str, job_id: str) -> list[str]:
    """Return the joined ``run:`` commands of one job."""
    return _run_commands_joined(_split_job(text, job_id)[0])


def _direct_runs_outside_job(text: str, job_id: str) -> list[str]:
    """Return joined commands outside job *job_id* that run ``pre-commit`` directly.

    Continuations are joined before the detection, so a direct run
    split across lines (``pre-commit \\`` then ``run -a``) cannot
    evade the contract.
    """
    _, remaining = _split_job(text, job_id)
    return [
        command
        for command in _run_commands_joined(remaining)
        if _has_direct_pre_commit_run(command)
    ]


def _direct_runs_in_job(text: str, job_id: str) -> list[str]:
    """Return every joined command in job *job_id* that runs ``pre-commit`` directly.

    Every step form counts, including ``- run:`` sequence items with
    no name, so a direct run cannot hide from the count.
    """
    job, _ = _split_job(text, job_id)
    return [
        command
        for command in _run_commands_joined(job)
        if _has_direct_pre_commit_run(command)
    ]


def _direct_run_invocations(commands: list[str]) -> list[str]:
    """Return every direct ``pre-commit run`` invocation in *commands*.

    One command line can hold several invocations — for example
    ``pre-commit run -a; pre-commit run -a`` — and each match counts,
    so the rehearsal contract counts invocations, not lines.
    """
    return [
        match
        for command in commands
        for match in _DIRECT_PRE_COMMIT_RUN.findall(command)
    ]


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

    def test_quoted_and_spaced_run_keys_are_extracted(self) -> None:
        # YAML accepts "run":, 'run':, and run : spellings of the key;
        # none of them may hide a command from the contract guards.
        for key in ('"run":', "'run':", "run :", '"run" :'):
            with self.subTest(key=key):
                snippet = f"        {key} pre-commit gc\n"
                self.assertEqual(["pre-commit gc"], _run_commands(snippet))

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


class RunCommandsByStepTests(unittest.TestCase):
    def test_groups_each_run_key_into_its_own_list(self) -> None:
        snippet = (
            "        run: python3 scripts/lint.py --all-files\n"
            "        run: |\n"
            "          echo one\n"
            "          echo two\n"
            "        run: pre-commit gc\n"
        )
        self.assertEqual(
            [
                ["python3 scripts/lint.py --all-files"],
                ["echo one", "echo two"],
                ["pre-commit gc"],
            ],
            _run_commands_by_step(snippet),
        )

    def test_sequence_item_run_steps_are_extracted(self) -> None:
        # GitHub allows a step as a sequence item with no name:
        # `- run: <command>` inline, and a `- run: |` block. Each is
        # its own group, and the direct-run matcher finds the command.
        snippet = (
            "      - run: pre-commit run --all-files\n"
            "      - run: |\n"
            "          env -i pre-commit run --all-files\n"
            "      - name: Named step\n"
            "        run: pre-commit gc\n"
        )
        self.assertEqual(
            [
                ["pre-commit run --all-files"],
                ["env -i pre-commit run --all-files"],
                ["pre-commit gc"],
            ],
            _run_commands_by_step(snippet),
        )
        for command in _run_commands(snippet):
            with self.subTest(command=command):
                if command != "pre-commit gc":
                    self.assertTrue(_has_direct_pre_commit_run(command))

    def test_unnamed_block_stops_at_sibling_keys(self) -> None:
        # A `- run: |` block ends at a sibling field at the run key's
        # indentation; the sibling value must not become a command,
        # even when its text names `pre-commit run`.
        snippet = (
            "      - run: |\n"
            "          echo only this\n"
            "        name: Explain pre-commit run\n"
            "      - name: Next step\n"
            "        run: pre-commit gc\n"
        )
        self.assertEqual(
            [
                ["echo only this"],
                ["pre-commit gc"],
            ],
            _run_commands_by_step(snippet),
        )
        # The name: value never reaches the commands, so the
        # outside-job direct-run check does not reject this workflow.
        commands = _run_commands(snippet)
        self.assertNotIn("name: Explain pre-commit run", commands)
        self.assertEqual(
            [], [command for command in commands if _has_direct_pre_commit_run(command)]
        )

    def test_join_continuations_merges_backslash_lines(self) -> None:
        self.assertEqual(
            ["env A=1 B=2 pre-commit run --all-files", "echo next"],
            _join_continuations(
                [
                    "env A=1 \\",
                    "B=2 pre-commit run --all-files",
                    "echo next",
                ]
            ),
        )

    def test_join_continuations_keeps_a_trailing_continuation(self) -> None:
        # A block that ends on a continuation still yields its command
        # instead of dropping it.
        self.assertEqual(
            ["env -i"],
            _join_continuations(["env -i \\"]),
        )

    def test_runs_under_env_i_matches_only_before_pre_commit(self) -> None:
        self.assertTrue(_runs_under_env_i("env -i PATH=x pre-commit run"))
        self.assertTrue(_runs_under_env_i("setup && env -i pre-commit run"))
        self.assertFalse(_runs_under_env_i("env PATH=x pre-commit run"))
        self.assertFalse(_runs_under_env_i("echo pre-commit run env -i"))


class SplitJobTests(unittest.TestCase):
    def test_separates_the_named_job_from_the_rest(self) -> None:
        text = (
            "on:\n"
            "  push:\n"
            "jobs:\n"
            "  pre-commit:\n"
            "    steps:\n"
            "      - run: python3 scripts/lint.py --all-files\n"
            "  gate-rehearsal:\n"
            "    steps:\n"
            "      - run: pre-commit run --all-files\n"
            "  ci-success:\n"
            "    steps:\n"
            "      - run: echo done\n"
        )
        job, rest = _split_job(text, "gate-rehearsal")
        self.assertIn("pre-commit run --all-files", job)
        self.assertNotIn("gate-rehearsal", rest)
        self.assertIn("python3 scripts/lint.py --all-files", rest)
        self.assertIn("echo done", rest)

    def test_absent_job_returns_nothing_removed(self) -> None:
        text = "jobs:\n  pre-commit:\n    steps:\n      - run: echo x\n"
        job, rest = _split_job(text, "gate-rehearsal")
        self.assertEqual("", job)
        self.assertEqual(text.rstrip("\n"), rest)


class CiWorkflowContractTests(unittest.TestCase):
    """The CI pre-commit job uses the wrapper as its lint entrypoint.

    Every assertion inspects the executed ``run:`` command lines, not
    the raw file text, so comments and step names cannot satisfy it.
    The ``gate-rehearsal`` job (ADR-0005 Decision item 3) is the one
    permitted place for a direct ``pre-commit run`` invocation — it
    proves the gate configuration on the exact version Fullsend runs —
    so its commands are excluded from the wrapper-entrypoint contract
    and pinned by their own, stricter tests.
    """

    _GATE_REHEARSAL_JOB = "gate-rehearsal"
    _PRE_COMMIT_JOB = "pre-commit"
    _CI_SUCCESS_JOB = "ci-success"
    _FULLSEND_PIN_COMMAND = (
        "python3 scripts/lint.py --print-fullsend-pre-commit-version"
    )

    # Guards the rehearsal step must carry (ADR-0005 Decision item 3):
    # VCS denial, no default-branch ref, no Go/Node/uvx toolchain, an
    # empty PRE_COMMIT_HOME, and a skip predicate on every log.
    _REHEARSAL_COMMAND_TOKENS = (
        "GIT_ALLOW_PROTOCOL=file",
        "GIT_TERMINAL_PROMPT=0",
        "git ls-remote",
        "refs/heads/main",
        "refs/remotes/origin/main",
        "go",
        "gofmt",
        "node",
        "npm",
        "uvx",
        "PRE_COMMIT_HOME",
        "ls -A",
        "Skipped",
    )

    # The base exports the pre-commit job must wire (ADR-0005
    # Base-aware validation). The select step's id is free; the
    # source of each export is not. The last two take exact_base
    # with a merge_base fallback.
    _BASE_EXPORT_PATTERNS = (
        r"GITHUB_BASE_SHA: \$\{\{ steps\.\w+\.outputs\.merge_base \}\}",
        r"PROTOBOT_VALIDATOR_BASE: \$\{\{ steps\.\w+\.outputs\.exact_base \}\}",
        (
            r"BASELINE_RATCHET_BASE_REF: \$\{\{ steps\.\w+\.outputs\.exact_base"
            r" \|\| steps\.\w+\.outputs\.merge_base \}\}"
        ),
        (
            r"PRE_COMMIT_FROM_REF: \$\{\{ steps\.\w+\.outputs\.exact_base"
            r" \|\| steps\.\w+\.outputs\.merge_base \}\}"
        ),
    )

    def setUp(self) -> None:
        workflow_path = checker.REPO_ROOT / ".github" / "workflows" / "ci-workflow.yaml"
        self.text = workflow_path.read_text(encoding="utf-8")
        rehearsal, remaining = _split_job(self.text, self._GATE_REHEARSAL_JOB)
        self.rehearsal_text = rehearsal
        # Joined commands: a direct run split across continuation
        # lines must not evade any contract assertion.
        self.rehearsal_commands = _run_commands_joined(rehearsal)
        self.commands = _run_commands_joined(remaining)

    def _rehearsal_direct_run_commands(self) -> list[str]:
        """Return the joined commands of the rehearsal's direct-run step."""
        groups = [
            _join_continuations(group)
            for group in _run_commands_by_step(self.rehearsal_text)
        ]
        direct_groups = [
            group
            for group in groups
            if any(_has_direct_pre_commit_run(command) for command in group)
        ]
        # Exactly one step in gate-rehearsal invokes pre-commit run
        # directly, as the Fullsend gate does.
        self.assertEqual(1, len(direct_groups))
        return direct_groups[0]

    def test_no_direct_pre_commit_run_invocation(self) -> None:
        self.assertEqual(
            [], _direct_runs_outside_job(self.text, self._GATE_REHEARSAL_JOB)
        )

    def test_line_continued_direct_run_outside_gate_rehearsal_fails(self) -> None:
        # Regression test: a direct run split across continuation
        # lines used to evade the per-line check. The contract check
        # joins continuations, so the sneaky step is reported.
        workflow = (
            "jobs:\n"
            "  pre-commit:\n"
            "    steps:\n"
            "      - name: Sneaky step\n"
            "        run: |\n"
            "          pre-commit \\\n"
            "          run -a\n"
            "  gate-rehearsal:\n"
            "    steps:\n"
            "      - run: pre-commit run --all-files\n"
        )
        self.assertEqual(
            ["pre-commit run -a"],
            _direct_runs_outside_job(workflow, self._GATE_REHEARSAL_JOB),
        )

    def test_gate_rehearsal_direct_run_step_runs_twice(self) -> None:
        # The one direct-run step holds exactly two runs: one in the
        # job environment, one under env -i to catch hooks that read
        # environment variables. The count covers every step form,
        # including `- run:` items with no name, and counts every
        # invocation, not just the lines that hold one.
        direct_runs = _direct_runs_in_job(self.text, self._GATE_REHEARSAL_JOB)
        self.assertEqual(2, len(_direct_run_invocations(direct_runs)))
        under_env_i = [command for command in direct_runs if _runs_under_env_i(command)]
        self.assertEqual(1, len(under_env_i))
        # The other direct run must not run under env -i.
        self.assertEqual(1, len(direct_runs) - len(under_env_i))
        # pre-commit prints a passing hook's output only with --verbose,
        # so the skip predicate can see an `OK (skipped=N)` unittest
        # summary; --color=never keeps the saved log free of ANSI
        # escapes. A future edit must not drop either flag.
        for command in direct_runs:
            with self.subTest(command=command):
                self.assertIn("--verbose", command)
                self.assertIn("--color=never", command)

    def test_a_third_direct_run_step_is_counted(self) -> None:
        # Regression test: GitHub also allows `- run:` steps with no
        # name. Before the parser accepted that form, a third direct
        # run added that way was not counted and the "exactly two"
        # contract still passed.
        workflow = (
            "jobs:\n"
            "  gate-rehearsal:\n"
            "    steps:\n"
            "      - name: Rehearse the gate\n"
            "        run: |\n"
            "          env PATH=x pre-commit run --all-files\n"
            "          env -i PATH=x pre-commit run --all-files\n"
            "      - run: pre-commit run --all-files\n"
        )
        self.assertEqual(
            3,
            len(
                _direct_run_invocations(
                    _direct_runs_in_job(workflow, self._GATE_REHEARSAL_JOB)
                )
            ),
        )

    def test_two_invocations_on_one_line_are_counted_separately(self) -> None:
        # Regression test: one line can hold several direct
        # invocations. Before the count used every match, the line
        # counted once and this workflow passed the "exactly two"
        # contract with three invocations.
        workflow = (
            "jobs:\n"
            "  gate-rehearsal:\n"
            "    steps:\n"
            "      - name: Rehearse the gate\n"
            "        run: |\n"
            "          pre-commit run -a; pre-commit run -a\n"
            "          env -i pre-commit run -a\n"
        )
        direct_runs = _direct_runs_in_job(workflow, self._GATE_REHEARSAL_JOB)
        # Two command lines hold the invocations...
        self.assertEqual(2, len(direct_runs))
        # ...but three invocations run, so the contract rejects it.
        self.assertEqual(3, len(_direct_run_invocations(direct_runs)))

    def test_gate_rehearsal_direct_run_step_guards_each_run(self) -> None:
        # Each direct run is bracketed: strict shell settings at the
        # top, the environment probes and an empty-home check before
        # it, and the skip predicate after it.
        commands = self._rehearsal_direct_run_commands()
        self.assertEqual("set -euo pipefail", commands[0])
        verify = [
            index
            for index, command in enumerate(commands)
            if command == "verify_gate_environment"
        ]
        empty_home = [
            index
            for index, command in enumerate(commands)
            if command == 'require_empty_home "$home"'
        ]
        runs = [
            index
            for index, command in enumerate(commands)
            if _has_direct_pre_commit_run(command)
        ]
        skip_checks = [
            index
            for index, command in enumerate(commands)
            if command.startswith('fail_on_skip "')
        ]
        self.assertEqual(2, len(verify))
        self.assertEqual(2, len(empty_home))
        self.assertEqual(2, len(runs))
        self.assertEqual(2, len(skip_checks))
        for verify_at, home_at, run_at, skip_at in zip(
            verify, empty_home, runs, skip_checks
        ):
            with self.subTest(run_index=run_at):
                self.assertLess(verify_at, run_at)
                self.assertLess(home_at, run_at)
                self.assertLess(run_at, skip_at)
                for token in (
                    "HOME=",
                    "LANG=C.UTF-8",
                    "GIT_ALLOW_PROTOCOL=file",
                    "GIT_TERMINAL_PROMPT=0",
                ):
                    self.assertIn(token, commands[run_at])

    def test_gate_rehearsal_command_text_includes_the_required_guards(self) -> None:
        step_text = "\n".join(self._rehearsal_direct_run_commands())
        for token in self._REHEARSAL_COMMAND_TOKENS:
            with self.subTest(token=token):
                self.assertIn(token, step_text)

    def test_gate_rehearsal_does_not_read_the_ci_configuration(self) -> None:
        # Merge skew: the rehearsal must pass when
        # .github/pre-commit-ci.yaml is absent, so it never names it.
        self.assertNotIn(".github/pre-commit-ci.yaml", self.rehearsal_text)

    def test_gate_rehearsal_runs_on_a_matrix_of_both_images(self) -> None:
        # ADR-0005 Decision item 3: the same rehearsal runs on
        # ubuntu-24.04 (the Fullsend runner image) and ubuntu-latest.
        # The job uses ${{ matrix.os }}, so the images are asserted
        # under the matrix section, not in a runs-on line.
        self.assertIn("matrix:", self.rehearsal_text)
        matrix_start = self.rehearsal_text.index("matrix:")
        for image in ("ubuntu-24.04", "ubuntu-latest"):
            with self.subTest(image=image):
                self.assertIn(image, self.rehearsal_text)
                self.assertGreater(self.rehearsal_text.index(image), matrix_start)

    def test_gate_rehearsal_installs_the_fullsend_pre_commit_version(self) -> None:
        # The Fullsend pin, not the wrapper pin: the rehearsal proves
        # the gate on the version the sandbox runs. scripts/lint.py
        # owns both pins; the rehearsal reads the Fullsend pin through
        # --print-fullsend-pre-commit-version.
        self.assertTrue(
            any(
                self._FULLSEND_PIN_COMMAND in command
                for command in self.rehearsal_commands
            )
        )

    def test_gate_rehearsal_probes_the_pre_commit_hooks_repository(self) -> None:
        # ADR-0005 amendment: the ls-remote probe names
        # pre-commit/pre-commit-hooks, the repository the gate once
        # cloned. The deny rule establishes the condition; the probe
        # only confirms that the deny rule is active.
        commands = self._rehearsal_direct_run_commands()
        self.assertTrue(
            any(
                "git ls-remote" in command
                and "https://github.com/pre-commit/pre-commit-hooks" in command
                for command in commands
            ),
            "the rehearsal must probe git ls-remote against "
            "https://github.com/pre-commit/pre-commit-hooks",
        )

    def test_gate_rehearsal_positive_controls_prove_local_git_and_pypi(self) -> None:
        # ADR-0005 amendment: denial covers network transports only,
        # so each run carries positive controls. A file:// clone
        # proves local Git operates; a --no-deps pip download of
        # iniconfig proves PyPI access operates.
        commands = self._rehearsal_direct_run_commands()
        self.assertTrue(
            any(
                all(token in command for token in ("git clone", "file://"))
                for command in commands
            ),
            "the rehearsal must clone a file:// repository as a positive control",
        )
        self.assertTrue(
            any(
                all(
                    token in command
                    for token in ("pip download", "--no-deps", "iniconfig")
                )
                for command in commands
            ),
            "the rehearsal must download iniconfig from PyPI as a positive control",
        )

    def test_gate_rehearsal_skip_check_records_and_limits_allowed_skips(self) -> None:
        # ADR-0005 amendment: the rehearsal records each skipped hook
        # with its skip reason, then fails unless every skipped hook
        # is check-toml or check-xml reporting no files to check. The
        # exception list is closed: a new hook needs a tracked
        # matching file or an amendment. The reason is asserted as
        # "no files to check" because pre-commit prints it inside the
        # "(...)Skipped" marker: a literal-marker grep and a parsed
        # reason check both name it, so either form satisfies the
        # contract.
        commands = self._rehearsal_direct_run_commands()
        self.assertTrue(
            any("grep" in command and "Skipped" in command for command in commands),
            "the skip check must grep the run log to record each skipped hook",
        )
        step_text = "\n".join(commands)
        for token in ("check-toml", "check-xml", "no files to check"):
            with self.subTest(token=token):
                self.assertIn(token, step_text)

    def test_pre_commit_job_sets_up_go_before_installing_pre_commit(self) -> None:
        # ADR-0005 Decision item 5: scripts/lint.py runs the Go hooks
        # through the CI configuration, so the pre-commit job needs
        # the Go toolchain before pre-commit itself is installed.
        job_text = _split_job(self.text, self._PRE_COMMIT_JOB)[0]
        self.assertIn("actions/setup-go@", job_text)
        self.assertIn("go-version-file: validator/go.mod", job_text)
        self.assertLess(
            job_text.index("actions/setup-go@"),
            job_text.index("Install pre-commit"),
        )

    def test_pre_commit_job_checkout_fetches_the_full_history(self) -> None:
        # ADR-0005 cutover step 8: the job resolves a comparison
        # base, so the checkout must hold enough history to contain
        # the base commit.
        job_text = _without_yaml_comments(
            _split_job(self.text, self._PRE_COMMIT_JOB)[0]
        )
        self.assertIn("fetch-depth: 0", job_text)
        self.assertLess(
            job_text.index("actions/checkout@"),
            job_text.index("fetch-depth: 0"),
        )

    def test_pre_commit_job_installs_pyyaml_and_jsonschema_before_lint(self) -> None:
        # ADR-0005 cutover step 7: the CI configuration's
        # language: system hooks import PyYAML and jsonschema without
        # declaring the dependency, so the job installs both pins for
        # python3 (venv or equivalent) before scripts/lint.py runs.
        commands = _job_commands(self.text, self._PRE_COMMIT_JOB)
        lint_at = next(
            index
            for index, command in enumerate(commands)
            if command == "python3 scripts/lint.py --all-files --online-if-ci"
        )
        for pin in ("pyyaml==6.0.3", "jsonschema==4.25.1"):
            with self.subTest(pin=pin):
                install_at = [
                    index for index, command in enumerate(commands) if pin in command
                ]
                self.assertTrue(install_at, f"no command installs {pin}")
                self.assertLess(min(install_at), lint_at)

    def test_pre_commit_job_exports_github_token_to_the_lint_step(self) -> None:
        # ADR-0005 Accepted risk: GH_TOKEN lets zizmor run its online
        # audits at merge time; without the token 5 audits skip even
        # in online mode. A job-level env block and the lint step's
        # own env both satisfy the export.
        job_text = _without_yaml_comments(
            _split_job(self.text, self._PRE_COMMIT_JOB)[0]
        )
        self.assertIn("GH_TOKEN: ${{ github.token }}", job_text)

    def test_pre_commit_job_selects_the_comparison_base(self) -> None:
        # ADR-0005 cutover step 9: the job binds the four event
        # inputs to scripts/select_comparison_base.py, which fails
        # closed when the event requires a base git cannot provide,
        # before the lint step consumes the exports.
        commands = _job_commands(self.text, self._PRE_COMMIT_JOB)
        self.assertIn("python3 scripts/select_comparison_base.py", commands)
        self.assertLess(
            commands.index("python3 scripts/select_comparison_base.py"),
            commands.index("python3 scripts/lint.py --all-files --online-if-ci"),
        )
        job_text = _without_yaml_comments(
            _split_job(self.text, self._PRE_COMMIT_JOB)[0]
        )
        for binding in (
            "DEFAULT_BRANCH:",
            "PULL_REQUEST_BASE_SHA:",
            "MERGE_GROUP_BASE_SHA:",
            "PUSH_BEFORE_SHA:",
        ):
            with self.subTest(binding=binding):
                self.assertIn(binding, job_text)

    def test_pre_commit_job_exports_the_comparison_base(self) -> None:
        # ADR-0005 Base-aware validation: GITHUB_BASE_SHA comes from
        # merge_base, PROTOBOT_VALIDATOR_BASE from exact_base, and
        # BASELINE_RATCHET_BASE_REF and PRE_COMMIT_FROM_REF from
        # exact_base with a merge_base fallback.
        job_text = _without_yaml_comments(
            _split_job(self.text, self._PRE_COMMIT_JOB)[0]
        )
        for pattern in self._BASE_EXPORT_PATTERNS:
            with self.subTest(pattern=pattern):
                self.assertRegex(job_text, pattern)

    def test_ci_success_needs_gate_rehearsal(self) -> None:
        job_text = _split_job(self.text, self._CI_SUCCESS_JOB)[0]
        self.assertIn("- gate-rehearsal", job_text)
        self.assertLess(
            job_text.index("needs:"),
            job_text.index("- gate-rehearsal"),
        )

    def test_run_step_invokes_the_wrapper(self) -> None:
        # Scoped to the pre-commit job: a wrapper command in another
        # job must not satisfy this contract.
        self.assertIn(
            "python3 scripts/lint.py --all-files --online-if-ci",
            _job_commands(self.text, self._PRE_COMMIT_JOB),
        )

    def test_wrapper_command_in_another_job_does_not_satisfy_the_contract(self) -> None:
        # Only the pre-commit job is checked, so the wrapper command
        # in a different job cannot satisfy the contract above.
        workflow = (
            "jobs:\n"
            "  other:\n"
            "    steps:\n"
            "      - name: Run the wrapper\n"
            "        run: python3 scripts/lint.py --all-files --online-if-ci\n"
            "  pre-commit:\n"
            "    steps:\n"
            "      - name: Not the wrapper\n"
            "        run: echo no wrapper here\n"
        )
        self.assertNotIn(
            "python3 scripts/lint.py --all-files --online-if-ci",
            _job_commands(workflow, self._PRE_COMMIT_JOB),
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
