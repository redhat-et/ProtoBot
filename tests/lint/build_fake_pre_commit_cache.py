"""Build a structurally-valid, offline-only fake ``pre-commit`` cache.

``tests/lint/test_violations.sh`` uses this to exercise
``scripts/lint.py`` end-to-end — including the real ``repo: local``
hooks (``gofmt``, ``go-vet``, ...) — without any network access.

For every non-local, non-meta repository in ``.pre-commit-config.yaml``
this writes a minimal manifest (``.pre-commit-hooks.yaml``) declaring
each configured hook id as a ``language: system`` / ``entry: true``
no-op, and registers it in a ``db.db`` matching pre-commit's own
``Store`` schema. For each ``repo: local`` hook that declares
``additional_dependencies``, it also registers the ``local:<deps> @ 1``
row and a healthy ``py_env-<version>`` directory, so the offline
pre-flight's dependency-environment check passes. ``pre-commit`` then
loads every repo's manifest (it always does this up front, regardless
of which files are given) without attempting to clone anything, and the
real ``repo: local`` hooks run unmodified against whatever files are
passed in.

This intentionally does not exercise third-party tool behaviour
(ruff, yamllint, shellcheck, ...) — that is CI's job, via a real
``pre-commit run -a`` against the genuine hook repositories. It only
proves that the wrapper's cache bookkeeping and this repository's own
local hooks keep working together offline.

Usage::

    python3 tests/lint/build_fake_pre_commit_cache.py <repo_root> <cache_dir>
"""

from __future__ import annotations

import os
import sqlite3
import sys
from pathlib import Path

import yaml


def _python_environment_version() -> str:
    """Return the python environment suffix pre-commit would select."""
    try:
        from pre_commit.languages.python import get_default_version
    except ImportError:
        return f"python{sys.version_info.major}.{sys.version_info.minor}"
    return get_default_version()


def _write_local_environment(repo_dir: Path) -> None:
    """Write a Python environment that passes the pre-flight health check."""
    environment = repo_dir / f"py_env-{_python_environment_version()}"
    (environment / "bin").mkdir(parents=True)
    (environment / "pyvenv.cfg").write_text(
        f"version_info = {sys.version_info.major}.{sys.version_info.minor}."
        f"{sys.version_info.micro}.final.0\n",
        encoding="utf-8",
    )
    os.symlink(sys.executable, environment / "bin" / "python")
    (environment / ".install_state_v2").touch()


def build(repo_root: Path, cache_dir: Path) -> None:
    # pre-commit resolves PRE_COMMIT_HOME with os.path.realpath before it
    # stores repository paths, so the rows must use the resolved root too.
    cache_dir = cache_dir.resolve()
    cache_dir.mkdir(parents=True, exist_ok=True)
    db_path = cache_dir / "db.db"
    if db_path.exists():
        db_path.unlink()

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

        with open(repo_root / ".pre-commit-config.yaml") as fh:
            config = yaml.safe_load(fh)

        local_rows: dict[str, Path] = {}
        for index, entry in enumerate(config.get("repos", [])):
            repo = entry.get("repo", "")
            if repo == "meta":
                continue
            if repo == "local":
                for hook in entry.get("hooks", []):
                    dependencies = hook.get("additional_dependencies") or []
                    if hook.get("language") != "python" or not dependencies:
                        continue
                    repo_name = "local:" + ",".join(str(dep) for dep in dependencies)
                    if repo_name not in local_rows:
                        local_dir = cache_dir / f"fake_local_{len(local_rows)}"
                        local_dir.mkdir()
                        _write_local_environment(local_dir)
                        local_rows[repo_name] = local_dir
                        conn.execute(
                            "INSERT INTO repos (repo, ref, path) VALUES (?, ?, ?)",
                            (repo_name, "1", str(local_dir)),
                        )
                continue
            rev = entry.get("rev", "")
            fake_repo_dir = cache_dir / f"fake_repo_{index}"
            fake_repo_dir.mkdir(exist_ok=True)
            manifest = [
                {
                    "id": hook["id"],
                    "name": hook["id"],
                    "entry": "true",
                    "language": "system",
                }
                for hook in entry.get("hooks", [])
            ]
            with open(fake_repo_dir / ".pre-commit-hooks.yaml", "w") as manifest_fh:
                yaml.safe_dump(manifest, manifest_fh)
            conn.execute(
                "INSERT INTO repos (repo, ref, path) VALUES (?, ?, ?)",
                (repo, rev, str(fake_repo_dir)),
            )
        conn.commit()
    finally:
        conn.close()


if __name__ == "__main__":
    build(Path(sys.argv[1]), Path(sys.argv[2]))
