"""Build a structurally-valid, offline-only fake ``pre-commit`` cache.

``tests/lint/test_violations.sh`` uses this to exercise
``scripts/lint.py`` end-to-end — including the real ``repo: local``
hooks (``gofmt``, ``go-vet``, ...) — without any network access.

For every non-local, non-meta repository in ``.pre-commit-config.yaml``
this writes a minimal manifest (``.pre-commit-hooks.yaml``) declaring
each configured hook id as a ``language: system`` / ``entry: true``
no-op, and registers it in a ``db.db`` matching pre-commit's own
``Store`` schema. ``pre-commit`` then loads every repo's manifest
(it always does this up front, regardless of which files are given)
without attempting to clone anything, and the real ``repo: local``
hooks run unmodified against whatever files are passed in.

This intentionally does not exercise third-party tool behaviour
(ruff, yamllint, shellcheck, ...) — that is CI's job, via a real
``pre-commit run -a`` against the genuine hook repositories. It only
proves that the wrapper's cache bookkeeping and this repository's own
local hooks keep working together offline.

Usage::

    python3 tests/lint/build_fake_pre_commit_cache.py <repo_root> <cache_dir>
"""

from __future__ import annotations

import sqlite3
import sys
from pathlib import Path

import yaml

_NON_CLONED_REPOS = {"local", "meta"}


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

        with open(repo_root / ".pre-commit-config.yaml") as fh:
            config = yaml.safe_load(fh)

        for index, entry in enumerate(config.get("repos", [])):
            repo = entry.get("repo", "")
            if repo in _NON_CLONED_REPOS:
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
