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
* When the cache is cold and a bootstrap tarball was copied into the
  sandbox (by the Fullsend pre-script, see ``.fullsend/README.md``),
  it restores the cache from that tarball first and re-runs the
  pre-flight. The restore never runs with ``--online``, and the
  tarball is deleted only after a complete restore passes the
  pre-flight.
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
import re
import shutil
import sqlite3
import subprocess
import sys
import tarfile
from collections.abc import Iterator
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parent.parent
CONFIG_PATH = REPO_ROOT / ".pre-commit-config.yaml"

# The pre-commit version this wrapper requires. CI installs exactly
# this version; it reads the value through --print-pre-commit-version.
PRE_COMMIT_VERSION = "4.6.2"

# Bootstrap tarball copied into the sandbox by the Fullsend pre-script
# (see .fullsend/README.md).
_DEFAULT_CACHE_TARBALL = Path("/tmp/protobot-pre-commit-cache.tar.gz")

# Only tarballs under this directory are deleted after a restore; a
# configured path elsewhere may be a file the operator wants to keep.
_TARBALL_DELETE_ROOT = Path("/tmp")

# The warm cache is about 1 GB; 4 GiB bounds a corrupt or hostile
# tarball's extraction.
_MAX_EXTRACTED_BYTES = 4 * 1024 * 1024 * 1024

# ``repo*/py_env-python3.X`` directories hold per-hook virtualenvs for one
# interpreter; a cache built for another interpreter cannot run them.
_PY_ENV_NAME = re.compile(r"^py_env-python(?P<version>[0-9]+\.[0-9]+)$")

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


def _cache_tarball() -> Path:
    """Return the bootstrap tarball path copied in by the pre-script."""
    configured = os.environ.get("PROTOBOT_PRE_COMMIT_CACHE_TARBALL")
    if configured:
        return Path(configured).expanduser()
    return _DEFAULT_CACHE_TARBALL


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


# ── Cold-cache restore from the bootstrap tarball ────────────────


class _MemberFilter:
    """Tar member iterator that yields only members safe to extract.

    Skips members whose path would escape the destination and members
    that are not files, directories, symlinks, or hard links, counting
    every rejection in ``skipped_members``. A hard link member is also
    rejected when its target resolves outside the destination: the
    ``tar`` filter does not check link targets, and a hard link followed
    by a regular file of the same name would write through it. A path
    whose ``Path.resolve`` fails (``OSError``, or ``RuntimeError`` for a
    symlink cycle on Python 3.12) is rejected with the member that
    produced it. Stops yielding once the extracted bytes would exceed
    ``_MAX_EXTRACTED_BYTES`` and records that in ``size_capped``. The
    check runs lazily, one member at a time, so it also sees links that
    earlier members of the same archive created. It is the safety net
    for Python versions whose ``extractall`` takes no ``filter``
    argument; with ``filter="tar"`` it is defence in depth. Symlink
    targets are not restricted: the cache legitimately contains
    absolute links such as ``py_env-*/bin/python`` -> ``/usr/bin/python3``.
    """

    def __init__(self, tar: tarfile.TarFile, destination: Path) -> None:
        self._tar = tar
        self._destination = destination
        self.skipped_members = 0
        self.extracted_bytes = 0
        self.size_capped = False

    def _resolve_or_none(self, path: Path) -> Path | None:
        """Resolve *path*, returning None when the resolution fails.

        ``Path.resolve`` raises ``OSError`` and, on Python 3.12,
        ``RuntimeError`` for a symlink cycle. An unresolvable path cannot
        be verified, so the member that produced it is rejected.
        """
        try:
            return path.resolve()
        except (OSError, RuntimeError):
            return None

    def _inside_destination(self, candidate: Path, resolved_destination: Path) -> bool:
        return (
            candidate == resolved_destination
            or resolved_destination in candidate.parents
        )

    def __iter__(self) -> Iterator[tarfile.TarInfo]:
        resolved_destination = self._resolve_or_none(self._destination)
        if resolved_destination is None:
            # The destination itself cannot be resolved; nothing can be
            # verified, so every member is rejected.
            for member in self._tar:
                self.skipped_members += 1
            return
        for member in self._tar:
            if not (
                member.isfile() or member.isdir() or member.issym() or member.islnk()
            ):
                self.skipped_members += 1
                continue
            if member.islnk():
                # Hard link targets are archive-root relative and are not
                # checked by the ``tar`` filter.
                target = self._resolve_or_none(self._destination / member.linkname)
                if target is None or not self._inside_destination(
                    target, resolved_destination
                ):
                    self.skipped_members += 1
                    continue
            candidate = self._resolve_or_none(self._destination / member.name)
            if candidate is None or not self._inside_destination(
                candidate, resolved_destination
            ):
                self.skipped_members += 1
                continue
            if self.extracted_bytes + member.size > _MAX_EXTRACTED_BYTES:
                self.size_capped = True
                return
            self.extracted_bytes += member.size
            yield member


def _extract_tarball(tar: tarfile.TarFile, destination: Path) -> _MemberFilter:
    """Extract *tar* into *destination*; return the member filter used.

    Uses the ``tar`` filter, which keeps the cache's absolute symlinks
    (the ``data`` filter would reject them). Falls back for Python
    versions whose ``extractall`` takes no ``filter`` argument; there
    ``fully_trusted_filter`` is pinned where it exists, so the fallback
    keeps the same semantics instead of the newer default ``data``
    filter. ``_MemberFilter`` rejects escaping members on every version.
    """
    members = _MemberFilter(tar, destination)
    try:
        tar.extractall(destination, members=members, filter="tar")
    except TypeError:
        trusted = getattr(tarfile, "fully_trusted_filter", None)
        if trusted is not None:
            tar.extraction_filter = trusted
        tar.extractall(destination, members=members)
    return members


def _warn_on_interpreter_mismatch(pre_commit_home: Path) -> None:
    """Warn when the restored cache was built for another interpreter."""
    current = f"{sys.version_info.major}.{sys.version_info.minor}"
    others = sorted(
        {
            match.group("version")
            for entry in pre_commit_home.glob("*/py_env-python*")
            if (match := _PY_ENV_NAME.match(entry.name))
            and match.group("version") != current
        }
    )
    if others:
        found = ", ".join(f"py_env-python{version}" for version in others)
        print(
            f"WARNING: the restored pre-commit cache was built for another "
            f"interpreter: found {found}, this interpreter is Python "
            f"{current}. Hooks that use the python language will fail "
            f"until the cache is rebuilt on this interpreter.",
            file=sys.stderr,
        )


def _remove_quietly(path: Path) -> None:
    """Remove *path* (file, symlink, or directory tree), ignoring errors."""
    try:
        if path.is_symlink() or path.is_file():
            path.unlink()
        elif path.is_dir():
            shutil.rmtree(path)
    except OSError:
        pass


def _swap_cache_home(staging: Path, pre_commit_home: Path) -> None:
    """Promote the staged cache, keeping the live cache on failure.

    The live cache moves aside first, so a failed promotion can move it
    back instead of losing it. The backup is removed only after the
    staged cache is in place.
    """
    if not staging.is_dir():
        raise OSError(f"the staged cache {staging} is not a directory")
    backup = pre_commit_home.parent / f".{pre_commit_home.name}.backup-{os.getpid()}"
    _remove_quietly(backup)
    moved_aside = False
    if pre_commit_home.exists() or pre_commit_home.is_symlink():
        os.replace(pre_commit_home, backup)
        moved_aside = True
    try:
        os.replace(staging, pre_commit_home)
    except OSError:
        if moved_aside:
            os.replace(backup, pre_commit_home)
        raise
    if moved_aside:
        _remove_quietly(backup)


def _format_revisions(revisions: set[tuple[str, str]]) -> str:
    return ", ".join(f"{repo} @ {rev}" for repo, rev in sorted(revisions))


def _restore_cache(pre_commit_home: Path, required: set[tuple[str, str]]) -> bool:
    """Restore ``PRE_COMMIT_HOME`` from the bootstrap tarball, if present.

    Extraction goes to a staging directory beside ``pre_commit_home``,
    and only an extraction that covers every revision in *required* is
    promoted, and only when promotion would not drop revisions that the
    live cache already holds. A partial cache must never publish a
    ``db.db`` that lists revisions whose files are missing; a failed
    restore leaves ``pre_commit_home`` untouched so a later run
    retries. The tarball itself is left in place;
    ``_preflight_with_restore`` deletes it only after the restored
    cache passes the pre-flight. A corrupt or unreadable tarball only
    warns; the pre-flight then reports its usual missing-revision
    error.
    """
    tarball = _cache_tarball()
    if not tarball.is_file():
        return False
    staging = pre_commit_home.parent / f".{pre_commit_home.name}.restore-{os.getpid()}"
    _remove_quietly(staging)
    try:
        with tarfile.open(tarball) as tar:
            members = _extract_tarball(tar, staging)
        if members.skipped_members:
            print(
                f"WARNING: skipped {members.skipped_members} tarball member(s) "
                f"that would escape {pre_commit_home} or are not files, "
                f"directories, or links; keeping {tarball}.",
                file=sys.stderr,
            )
        if members.size_capped:
            print(
                f"WARNING: the tarball holds more than {_MAX_EXTRACTED_BYTES} "
                f"bytes; extraction stopped early and {tarball} was kept.",
                file=sys.stderr,
            )
        if members.skipped_members or members.size_capped:
            return False
        staged = _cached_repos(staging)
        if not required <= staged:
            print(
                f"WARNING: the archive {tarball} does not cover every "
                f"configured hook repository revision "
                f"({_format_revisions(required - staged)}); keeping the tarball "
                f"and the existing cache.",
                file=sys.stderr,
            )
            return False
        live = _cached_repos(pre_commit_home)
        if not live <= staged:
            print(
                f"WARNING: promoting the archive {tarball} would drop cached "
                f"revisions that the live cache {pre_commit_home} already "
                f"holds ({_format_revisions(live - staged)}); keeping the "
                f"tarball and the live cache.",
                file=sys.stderr,
            )
            return False
        _swap_cache_home(staging, pre_commit_home)
    except (tarfile.TarError, EOFError, OSError) as error:
        print(
            f"WARNING: cannot restore the pre-commit cache from {tarball}: {error}",
            file=sys.stderr,
        )
        return False
    finally:
        _remove_quietly(staging)
    print(
        f"Restored the offline pre-commit cache from {tarball} into {pre_commit_home}."
    )
    _warn_on_interpreter_mismatch(pre_commit_home)
    return True


def _delete_tarball() -> None:
    """Delete the bootstrap tarball after a complete, passing restore.

    Only paths under ``_TARBALL_DELETE_ROOT`` (``/tmp``) are deleted; a
    configured path elsewhere may be a file the operator wants to keep.
    """
    tarball = _cache_tarball()
    if not tarball.is_file():
        return
    if _TARBALL_DELETE_ROOT not in tarball.resolve().parents:
        print(
            f"WARNING: keeping {tarball}: only tarballs under "
            f"{_TARBALL_DELETE_ROOT} are deleted after a restore.",
            file=sys.stderr,
        )
        return
    try:
        # The sandbox should not carry both the cache and its tarball.
        tarball.unlink()
    except OSError as error:
        print(f"WARNING: cannot remove {tarball}: {error}", file=sys.stderr)


def _preflight_with_restore(
    online: bool,
    pre_commit_home: Path,
    config: dict[str, Any],
) -> int:
    """Run the cache pre-flight, restoring from the tarball once when cold.

    Offline only: with ``--online`` the pre-flight passes without the
    cache and the tarball is never unpacked. The missing-revision error
    is reported only when no restore fixed the cache, so a successful
    restore prints no spurious error. The tarball is deleted only after
    a restored cache passes the pre-flight.
    """
    if not online:
        configured = _configured_repos(config)
        missing = _missing_repos(configured, _cached_repos(pre_commit_home))
        if missing and _restore_cache(pre_commit_home, set(configured)):
            rc = _preflight(online, pre_commit_home, config)
            if rc == 0:
                _delete_tarball()
            return rc
    return _preflight(online, pre_commit_home, config)


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
        rc = _preflight_with_restore(online, pre_commit_home, config)
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

    # os.execvp replaces this process without flushing Python's buffers,
    # so the restore and warning lines must be flushed first.
    sys.stdout.flush()
    sys.stderr.flush()
    os.execvp(cmd[0], cmd)  # intentional process replacement


if __name__ == "__main__":
    sys.exit(main())
