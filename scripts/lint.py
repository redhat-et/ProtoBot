#!/usr/bin/env python3
"""Thin wrapper around ``pre-commit`` for offline and CI lint runs.

``.pre-commit-config.yaml`` is the single source of truth for which
hooks run and how. This script adds only the behaviour needed for
sandboxed coding agents that lack network access:

* It pins ``PRE_COMMIT_HOME`` so a pre-seeded cache is used.
* It pre-flights every non-local hook repository/revision against that
  cache and fails with an actionable message (naming the missing
  repository, revision, and bootstrap command) instead of letting
  ``pre-commit`` crash with an opaque traceback.
* It exports ``ZIZMOR_NO_ONLINE_AUDITS=true`` while offline, so the
  ``zizmor`` hook skips audits that require network access.
* It replicates the previous script's default file-selection
  behaviour (changed files vs ``HEAD``, including unstaged edits)
  instead of ``pre-commit run``'s own default (staged files only).

Usage::

    python scripts/lint.py --all-files                 # every tracked file
    python scripts/lint.py --files a.py b.yaml         # specific files
    python scripts/lint.py                             # changed files vs HEAD
    python scripts/lint.py --check-parity              # validate config/cache
    python scripts/lint.py --online                    # allow network access
    python scripts/lint.py --online-if-ci              # --online when CI=true
    python scripts/lint.py --print-pre-commit-version  # pinned version

CI runs ``python3 scripts/lint.py --all-files --online-if-ci`` and
installs the pre-commit version that ``--print-pre-commit-version``
prints. Under ``CI=true``, that invocation reads no configuration and
needs no PyYAML; ``pre-commit`` validates the config itself.

Because ``.pre-commit-config.yaml`` is the only configuration this
script reads, adding, removing, or reconfiguring a hook requires no
change here — edit the YAML file and (if network access is available)
run ``pre-commit install-hooks`` to refresh the cache.
"""

from __future__ import annotations

import argparse
import os
import sqlite3
import subprocess
import sys
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parent.parent
CONFIG_PATH = REPO_ROOT / ".pre-commit-config.yaml"

# The pre-commit version this wrapper requires. CI installs exactly
# this version; it reads the value through --print-pre-commit-version.
PRE_COMMIT_VERSION = "4.6.2"

# ``repo:`` values pre-commit never clones over the network.
_NON_CLONED_REPOS = {"local", "meta"}


# ── Environment helpers ─────────────────────────────────────────


def _ci_is_enabled() -> bool:
    """Return whether the environment explicitly enables online checks.

    Mirrors ``scripts/check_harness_integrity.py``'s identically named
    helper, which establishes this repository's ``CI=true`` convention
    for opting ``--online-if-ci`` flags into network access.
    """
    return os.environ.get("CI", "").strip().casefold() == "true"


def _pre_commit_home() -> Path:
    """Return the effective ``PRE_COMMIT_HOME``.

    Matches ``pre_commit.store.Store.get_default_directory``'s own
    resolution order so the pre-flight check inspects the same cache
    directory that ``pre-commit`` itself will use.
    """
    configured = os.environ.get("PRE_COMMIT_HOME")
    if configured:
        return Path(configured).expanduser().resolve()
    xdg = os.environ.get("XDG_CACHE_HOME")
    base = Path(xdg).expanduser() if xdg else Path.home() / ".cache"
    return (base / "pre-commit").resolve()


# ── Config helpers ──────────────────────────────────────────────


def _load_config() -> dict[str, Any]:
    """Parse ``.pre-commit-config.yaml``.

    PyYAML is imported here, not at module import time, so paths that
    read no config (``--print-pre-commit-version``; ``--all-files`` and
    ``--files`` with network access assumed) run without PyYAML.
    """
    try:
        import yaml
    except ImportError:
        sys.exit(
            "ERROR: PyYAML is required but not installed.\n"
            "       Install with:  pip install pyyaml"
        )
    with open(CONFIG_PATH) as fh:
        return yaml.safe_load(fh)


def _configured_repos(config: dict[str, Any]) -> list[tuple[str, str]]:
    """Return ``[(repo_url, rev), ...]`` for cloned hook repositories.

    Skips ``repo: local`` and ``repo: meta`` entries, which pre-commit
    never fetches over the network.
    """
    repos = []
    for entry in config.get("repos", []):
        repo = entry.get("repo", "")
        if repo in _NON_CLONED_REPOS:
            continue
        repos.append((repo, entry.get("rev", "")))
    return repos


def _any_always_run(config: dict[str, Any]) -> bool:
    """Return True when any configured hook is marked ``always_run``."""
    for repo in config.get("repos", []):
        for hook in repo.get("hooks", []):
            if hook.get("always_run"):
                return True
    return False


# ── Cache pre-flight ────────────────────────────────────────────


def _cached_repos(pre_commit_home: Path) -> set[tuple[str, str]]:
    """Return ``{(repo, ref), ...}`` already cloned into *pre_commit_home*.

    Reads the ``repos`` table from pre-commit's own ``db.db`` SQLite
    store directly (see ``pre_commit.store.Store``), so this check
    needs no network access and no dependency on pre-commit's internal
    Python API. None of this repository's hooks declare
    ``additional_dependencies``, so pre-commit's stored ``repo`` key
    always equals the bare repo URL from ``.pre-commit-config.yaml``
    (a ``:dep1,dep2`` suffix is only appended when dependencies are
    declared).
    """
    db_path = pre_commit_home / "db.db"
    if not db_path.exists():
        return set()
    try:
        conn = sqlite3.connect(str(db_path))
        try:
            rows = conn.execute("SELECT repo, ref FROM repos").fetchall()
        finally:
            conn.close()
    except sqlite3.DatabaseError:
        return set()
    return {(repo, ref) for repo, ref in rows}


def _missing_repos(
    configured: list[tuple[str, str]],
    cached: set[tuple[str, str]],
) -> list[tuple[str, str]]:
    return [pair for pair in configured if pair not in cached]


def _bootstrap_command(pre_commit_home: Path) -> str:
    return f'PRE_COMMIT_HOME="{pre_commit_home}" pre-commit install-hooks'


def _report_missing_repos(
    missing: list[tuple[str, str]],
    pre_commit_home: Path,
) -> None:
    plural = "" if len(missing) == 1 else "s"
    print(
        f"ERROR: the offline pre-commit cache ({pre_commit_home}) is "
        f"missing {len(missing)} hook repository revision{plural}:",
        file=sys.stderr,
    )
    for repo, rev in missing:
        print(f"  - {repo} @ {rev}", file=sys.stderr)
    print(
        "\nNo network access is assumed for this run, so the missing "
        "revision(s) cannot be fetched. With network access, run:\n"
        f"  {_bootstrap_command(pre_commit_home)}\n"
        "then retry. Alternatively, re-run with --online (or "
        "--online-if-ci under CI=true) to let pre-commit fetch them "
        "directly.",
        file=sys.stderr,
    )


def _preflight(online: bool, pre_commit_home: Path, config: dict[str, Any]) -> int:
    """Verify the cache covers every configured hook repository revision.

    Returns ``0`` when the cache is sufficient or network access is
    assumed available, and non-zero when a hook repository/revision
    would require network access that is not available.
    """
    if online:
        return 0
    missing = _missing_repos(_configured_repos(config), _cached_repos(pre_commit_home))
    if missing:
        _report_missing_repos(missing, pre_commit_home)
        return 1
    return 0


# ── File selection ──────────────────────────────────────────────


def _changed_files() -> list[str]:
    """Return paths changed in the working tree relative to ``HEAD``.

    This differs from ``pre-commit run``'s own default scope (staged
    files only, via ``git diff --staged``): it also covers unstaged
    edits, matching the previous ``scripts/lint.py`` offline engine's
    default so routine incremental runs keep seeing in-progress edits.
    Deleted paths are excluded (``--diff-filter=ACMR``) because hooks
    that take filenames cannot act on a path that no longer exists;
    hooks that must still react to deletions declare ``always_run``
    in ``.pre-commit-config.yaml``, which ``pre-commit`` honours
    independently of the file list passed here.
    """
    result = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=ACMR", "HEAD"],
        capture_output=True,
        text=True,
        cwd=str(REPO_ROOT),
        check=False,
    )
    if result.returncode != 0:
        stderr = result.stderr.strip()
        sys.exit(
            f"ERROR: git diff --name-only HEAD exited {result.returncode}"
            + (f": {stderr}" if stderr else "")
        )
    return [
        line.strip()
        for line in result.stdout.splitlines()
        if line.strip() and (REPO_ROOT / line.strip()).is_file()
    ]


# ── Entrypoint ───────────────────────────────────────────────────


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    scope = parser.add_mutually_exclusive_group()
    scope.add_argument(
        "--all-files",
        action="store_true",
        help="check all tracked files (passed through to `pre-commit run --all-files`)",
    )
    scope.add_argument(
        "--files",
        nargs="+",
        metavar="FILE",
        help="check only these files (passed through to `pre-commit run --files`)",
    )
    parser.add_argument(
        "--check-parity",
        action="store_true",
        help=(
            "validate .pre-commit-config.yaml and confirm every configured "
            "hook repository revision is present in the offline cache, "
            "without running any hooks"
        ),
    )
    online_group = parser.add_mutually_exclusive_group()
    online_group.add_argument(
        "--online",
        action="store_true",
        help=(
            "assume network access: skip the cache pre-flight (letting "
            "pre-commit fetch missing hook repositories itself) and allow "
            "zizmor's online audits"
        ),
    )
    online_group.add_argument(
        "--online-if-ci",
        action="store_true",
        help="behave as --online only when CI=true",
    )
    parser.add_argument(
        "--print-pre-commit-version",
        action="store_true",
        help="print the pinned pre-commit version and exit; CI installs it",
    )
    args = parser.parse_args(argv)

    if args.print_pre_commit_version:
        if args.check_parity or args.all_files or args.files:
            parser.error(
                "--print-pre-commit-version cannot be combined with "
                "--check-parity, --all-files, or --files"
            )
        print(PRE_COMMIT_VERSION)
        return 0

    online = args.online or (args.online_if_ci and _ci_is_enabled())

    pre_commit_home = _pre_commit_home()
    os.environ.setdefault("PRE_COMMIT_HOME", str(pre_commit_home))
    if not online:
        os.environ.setdefault("ZIZMOR_NO_ONLINE_AUDITS", "true")

    # Read the config at most once, and only where a code path needs
    # it: the offline cache pre-flight below, and bare mode's
    # always_run check. The CI invocation (--all-files --online-if-ci
    # under CI=true) needs neither, so it runs without PyYAML;
    # pre-commit validates the config itself.
    config: dict[str, Any] | None = None
    if not online:
        config = _load_config()
        rc = _preflight(online, pre_commit_home, config)
        if rc != 0:
            return rc

    if args.check_parity:
        validate_rc = subprocess.run(
            ["pre-commit", "validate-config", str(CONFIG_PATH)],
            cwd=str(REPO_ROOT),
            check=False,
        ).returncode
        if validate_rc == 0:
            print(
                "PARITY CHECK PASSED: .pre-commit-config.yaml is valid and "
                "every configured hook repository revision is present in "
                f"the offline cache ({pre_commit_home})."
            )
        return validate_rc

    cmd = ["pre-commit", "run"]
    if args.all_files:
        cmd.append("--all-files")
    elif args.files:
        cmd.append("--files")
        cmd.extend(args.files)
    else:
        changed = _changed_files()
        if not changed:
            if config is None:
                config = _load_config()
            if not _any_always_run(config):
                print("No files to check.")
                return 0
        cmd.append("--files")
        cmd.extend(changed)

    os.execvp(cmd[0], cmd)  # intentional process replacement


if __name__ == "__main__":
    sys.exit(main())
