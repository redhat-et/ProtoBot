"""Build a structurally-valid, offline-only fake ``pre-commit`` cache.

``tests/lint/test_violations.sh`` uses this to exercise
``scripts/lint.py`` end-to-end — including the real ``repo: local``
``language: system`` hooks (``gofmt``, ``go-vet``,
``check-portable-null-device``, ...) — without any network access.

Both pre-commit configurations (ADR-0005) are covered; a configuration
file that does not exist is merge skew and is skipped. For every
non-local, non-meta repository this writes a minimal manifest
(``.pre-commit-hooks.yaml``) declaring each configured hook id as a
``language: system`` / ``entry: true`` no-op. ``repo: local`` hooks
that install an environment (``language: python``, with or without
``additional_dependencies``) register under pre-commit's own
``local:<deps>`` keys, and their fake repository directory holds a
fake python environment: a ``pyvenv.cfg`` plus a ``bin/python`` symlink
to the running interpreter that satisfy pre-commit's health check, a
touched ``.install_state_v2``, and one no-op ``bin/<entry>`` stub per
hook so third-party tools (ruff, yamllint, ...) never install.
Everything registers in a ``db.db`` matching pre-commit's own ``Store``
schema. ``pre-commit`` then loads every repo's manifest (it always
does this up front, regardless of which files are given) without
attempting to clone anything, and the real ``language: system`` hooks
run unmodified against whatever files are passed in.

This intentionally does not exercise third-party tool behaviour
(ruff, yamllint, shellcheck, ...) — that is CI's job, via a real
``pre-commit run -a`` against the genuine hook repositories. It only
proves that the wrapper's cache bookkeeping and this repository's own
system hooks keep working together offline.

Usage::

    python3 tests/lint/build_fake_pre_commit_cache.py <repo_root> <cache_dir>
"""

from __future__ import annotations

import os
import sqlite3
import sys
from pathlib import Path

import yaml

_NON_CLONED_REPOS = {"local", "meta"}

# The gate and CI configurations (ADR-0005). A missing file is merge
# skew — a branch that predates the cutover — and is skipped.
_CONFIG_PATHS = (".pre-commit-config.yaml", ".github/pre-commit-ci.yaml")

# shortcut: pre-commit names a python environment directory after the
# interpreter it resolves as the default (``py_env-python<X.Y>``), and
# the fake below reuses this builder's interpreter. They agree only
# while pre-commit resolves the same <X.Y>; when they diverge,
# pre-commit rebuilds the environments and needs network access.
# Upgrade by asking pre-commit itself for its default version.


def _entry_names(hook: dict[str, object]) -> str | None:
    """Return the command name *hook* runs, or None without one."""
    entry = hook.get("entry")
    if not isinstance(entry, str) or not entry.strip():
        return None
    return entry.split()[0]


def _collect_env_hooks(
    repo_config: dict[str, object],
    env_rows: dict[str, set[str]],
) -> None:
    """Register ``repo: local`` hooks that install an environment.

    Mirrors ``scripts/lint.py``'s ``_configured_repos`` key format
    (``local:<dep1,dep2,...>``, or ``local`` without dependencies) and
    pre-commit's own ``Store.db_repo_name``. Only ``language: python``
    hooks install an environment in these configurations; system and
    pygrep hooks run from the working directory.
    """
    hooks = repo_config.get("hooks", [])
    if not isinstance(hooks, list):
        return
    for hook in hooks:
        if not isinstance(hook, dict) or hook.get("language") != "python":
            continue
        deps = hook.get("additional_dependencies") or []
        key = f"local:{','.join(deps)}" if deps else "local"
        entry_name = _entry_names(hook)
        if entry_name is not None:
            env_rows.setdefault(key, set()).add(entry_name)


def _write_fake_env(repo_dir: Path, entry_names: set[str]) -> None:
    """Write a python environment that pre-commit treats as installed.

    ``_hook_installed`` in pre-commit's repository module requires a
    ``pyvenv.cfg`` whose ``version_info`` matches what ``bin/python``
    reports and an existing ``.install_state_v2``; the health check
    also runs ``bin/python`` directly. The entry stubs let matching
    hooks run as no-ops instead of installing the real tools.
    """
    env_dir = repo_dir / (
        f"py_env-python{sys.version_info.major}.{sys.version_info.minor}"
    )
    bin_dir = env_dir / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    version = ".".join(str(part) for part in sys.version_info)
    (env_dir / "pyvenv.cfg").write_text(f"version_info = {version}\n", encoding="utf-8")
    python_link = bin_dir / "python"
    if not python_link.exists():
        os.symlink(sys.executable, python_link)
    (env_dir / ".install_state_v2").touch()
    for entry_name in entry_names:
        stub = bin_dir / entry_name
        stub.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        stub.chmod(0o755)


def build(repo_root: Path, cache_dir: Path) -> None:
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
        # ``local:<deps>`` key -> hook entry names sharing that
        # environment.
        env_rows: dict[str, set[str]] = {}
        fake_repo_count = 0
        for config_name in _CONFIG_PATHS:
            config_path = repo_root / config_name
            if not config_path.exists():
                continue
            with open(config_path) as fh:
                config = yaml.safe_load(fh)
            for repo_config in config.get("repos", []):
                repo = repo_config.get("repo", "")
                if repo == "local":
                    _collect_env_hooks(repo_config, env_rows)
                    continue
                if repo in _NON_CLONED_REPOS:
                    continue
                fake_repo_dir = cache_dir / f"fake_repo_{fake_repo_count}"
                fake_repo_count += 1
                fake_repo_dir.mkdir(exist_ok=True)
                manifest = [
                    {
                        "id": hook["id"],
                        "name": hook["id"],
                        "entry": "true",
                        "language": "system",
                    }
                    for hook in repo_config.get("hooks", [])
                ]
                with open(fake_repo_dir / ".pre-commit-hooks.yaml", "w") as fh:
                    yaml.safe_dump(manifest, fh)
                conn.execute(
                    "INSERT OR REPLACE INTO repos (repo, ref, path) VALUES (?, ?, ?)",
                    (repo, repo_config.get("rev", ""), str(fake_repo_dir)),
                )
        for key in sorted(env_rows):
            fake_repo_dir = cache_dir / f"fake_repo_{fake_repo_count}"
            fake_repo_count += 1
            fake_repo_dir.mkdir(exist_ok=True)
            _write_fake_env(fake_repo_dir, env_rows[key])
            conn.execute(
                "INSERT OR REPLACE INTO repos (repo, ref, path) VALUES (?, ?, ?)",
                (key, "1", str(fake_repo_dir)),
            )
        conn.commit()
    finally:
        conn.close()


if __name__ == "__main__":
    build(Path(sys.argv[1]), Path(sys.argv[2]))
