#!/usr/bin/env python3
"""Thin wrapper around ``pre-commit`` for offline and CI lint runs.

Two pre-commit configurations share one hook set (ADR-0005,
``docs/decisions/0005-commit-hook-framework.md``). The gate
configuration, ``.pre-commit-config.yaml``, holds only ``repo: local``
hooks and obeys the gate invariant: ``pre-commit run --all-files``
against it passes with a fresh ``PRE_COMMIT_HOME`` when VCS access is
denied. The CI configuration, ``.github/pre-commit-ci.yaml``, holds
every hook that needs a VCS clone, the Go toolchain, or a
default-branch ref. This script runs both, one ``pre-commit run
--config <path>`` invocation per existing configuration, and adds only
the behaviour needed for sandboxed coding agents that lack network
access:

* It pins ``PRE_COMMIT_HOME`` so a pre-seeded cache is used.
* It pre-flights every non-local hook repository/revision of every
  existing configuration against that cache and fails with an
  actionable message (naming the missing repository, revision, and
  bootstrap command) instead of letting ``pre-commit`` crash with an
  opaque traceback.
* When the cache is cold and a bootstrap tarball was copied into the
  sandbox (by the Fullsend pre-script, see ``.fullsend/README.md``),
  it restores the cache from that tarball first and re-runs the
  pre-flight. The restore covers the gate configuration's
  requirements, because the archive is built from
  ``.pre-commit-config.yaml``. The restore never runs with
  ``--online``, and the tarball is deleted only after a complete
  restore passes the pre-flight.
* It exports ``ZIZMOR_NO_ONLINE_AUDITS=true`` while offline, so the
  ``zizmor`` hook skips audits that require network access.
* It replicates the previous script's default file-selection
  behaviour (changed files vs ``HEAD``, including unstaged edits)
  instead of ``pre-commit run``'s own default (staged files only).

A missing ``.github/pre-commit-ci.yaml`` is merge skew — a branch that
predates the cutover — and counts as an empty configuration, so such a
branch still passes with the gate configuration alone. A missing gate
configuration is an error.

An offline run of this wrapper pre-flights both configurations. The
bootstrap archive is built from the gate configuration alone, so it
does not hold the CI configuration's cloned hooks. The sandbox
therefore runs the direct gate command (``pre-commit run --files
<changed>`` against the gate configuration) instead of this wrapper.

Usage::

    python scripts/lint.py --all-files                 # every tracked file
    python scripts/lint.py --files a.py b.yaml         # specific files
    python scripts/lint.py                             # changed files vs HEAD
    python scripts/lint.py --check-parity              # validate configs/cache
    python scripts/lint.py --online                    # allow network access
    python scripts/lint.py --online-if-ci              # --online when CI=true
    python scripts/lint.py --print-pre-commit-version  # pinned version

CI runs ``python3 scripts/lint.py --all-files --online-if-ci`` and
installs the pre-commit version that ``--print-pre-commit-version``
prints. Under ``CI=true``, that invocation reads no configuration and
needs no PyYAML; ``pre-commit`` validates the configurations itself.

Adding, removing, or reconfiguring a hook requires no change here —
edit the YAML files and (if network access is available) refresh the
cache with ``pre-commit install-hooks`` for the gate configuration
and ``pre-commit install-hooks --config .github/pre-commit-ci.yaml``
for the CI configuration.
"""

from __future__ import annotations

import argparse
import os
import shlex
import shutil
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import zlib
from collections.abc import Iterator
from pathlib import Path
from typing import Any, NamedTuple

REPO_ROOT = Path(__file__).resolve().parent.parent
CONFIG_PATH = REPO_ROOT / ".pre-commit-config.yaml"
CI_CONFIG_PATH = REPO_ROOT / ".github" / "pre-commit-ci.yaml"

# The pre-commit version this wrapper requires. CI installs exactly
# this version; it reads the value through --print-pre-commit-version.
# It deliberately differs from PRECOMMIT_VERSION in
# scripts/pre_commit_cache.py (4.5.1, the version Fullsend ships): CI
# and developers validate the full hook set on this newer release,
# while the gate-rehearsal job proves the gate configuration on the
# exact version Fullsend runs in its sandbox. Change either pin only
# with the other in mind.
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


def _existing_config_paths() -> list[Path]:
    """Return the existing configurations, gate first.

    The gate and CI configurations together hold the hook set
    (ADR-0005). A missing CI configuration is merge skew — a branch
    that predates the cutover — and counts as an empty configuration,
    so only the gate runs; ``main`` reports a missing gate
    configuration as an error.
    """
    return [path for path in (CONFIG_PATH, CI_CONFIG_PATH) if path.exists()]


def _load_config(path: Path) -> dict[str, Any]:
    """Parse one pre-commit configuration file.

    PyYAML is imported here, not at module import time, so paths that
    read no configuration (``--print-pre-commit-version``;
    ``--all-files`` and ``--files`` with network access assumed) run
    without PyYAML. An absent file is an empty configuration — merge
    skew for the CI configuration; any other read error propagates.
    """
    if not path.exists():
        return {}
    try:
        import yaml
    except ImportError:
        sys.exit(
            "ERROR: PyYAML is required but not installed.\n"
            "       Install with:  pip install pyyaml"
        )
    with open(path) as fh:
        return yaml.safe_load(fh) or {}


def _load_each_config() -> list[tuple[Path, dict[str, Any]]]:
    """Load every existing configuration once, gate first."""
    return [(path, _load_config(path)) for path in _existing_config_paths()]


def _relative(path: Path) -> str:
    """Return *path* relative to the repository root, or as given."""
    try:
        return str(path.relative_to(REPO_ROOT))
    except ValueError:
        return str(path)


def _configured_repos(config: dict[str, Any]) -> list[tuple[str, str]]:
    """Return ``[(repo_url, rev), ...]`` for configured hook repositories and environments.

    Includes cloned hook repositories and ``repo: local`` hooks that pin
    dependencies via ``additional_dependencies``. Skips ``repo: meta``
    and local hooks with ``pygrep`` or ``system`` languages, which do not
    require an installed environment in the pre-commit cache.
    """
    repos: list[tuple[str, str]] = []
    seen: set[tuple[str, str]] = set()
    for entry in config.get("repos", []):
        repo = entry.get("repo", "")
        if repo == "meta":
            continue
        if repo == "local":
            for hook in entry.get("hooks", []):
                language = hook.get("language", "")
                if language in ("pygrep", "system"):
                    continue
                deps = hook.get("additional_dependencies")
                if not deps:
                    continue
                key = f"local:{','.join(deps)}"
                ref = "1"
                pair = (key, ref)
                if pair not in seen:
                    seen.add(pair)
                    repos.append(pair)
            continue
        rev = entry.get("rev", "")
        pair = (repo, rev)
        if pair not in seen:
            seen.add(pair)
            repos.append(pair)
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
    """Return ``{(repo, ref), ...}`` already cloned or installed in *pre_commit_home*.

    Reads the ``repos`` table from pre-commit's own ``db.db`` SQLite
    store directly (see ``pre_commit.store.Store``), so this check
    needs no network access and no dependency on pre-commit's internal
    Python API. Cloned hook repositories are stored with their bare
    repository URL and revision, while ``repo: local`` hooks with
    ``additional_dependencies`` are stored with keys of the form
    ``local:<dep1,dep2,...>`` and ref ``1``.
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


def _bootstrap_command(pre_commit_home: Path, config_path: Path) -> str:
    # install-hooks defaults to .pre-commit-config.yaml, so the
    # remedy must name the configuration whose revisions are missing.
    return (
        f'PRE_COMMIT_HOME="{pre_commit_home}" pre-commit install-hooks '
        f"--config {_relative(config_path)}"
    )


def _report_missing_repos(
    missing: list[tuple[str, str]],
    pre_commit_home: Path,
    config_path: Path,
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
        f"  {_bootstrap_command(pre_commit_home, config_path)}\n"
        "then retry. Alternatively, re-run with --online (or "
        "--online-if-ci under CI=true) to let pre-commit fetch them "
        "directly.",
        file=sys.stderr,
    )


def _preflight(
    online: bool,
    pre_commit_home: Path,
    config: dict[str, Any],
    config_path: Path = CONFIG_PATH,
) -> int:
    """Verify the cache covers every configured hook repository revision.

    Returns ``0`` when the cache is sufficient or network access is
    assumed available, and non-zero when a hook repository/revision
    would require network access that is not available. *config_path*
    names the configuration in the remedy message.
    """
    if online:
        return 0
    missing = _missing_repos(_configured_repos(config), _cached_repos(pre_commit_home))
    if missing:
        _report_missing_repos(missing, pre_commit_home, config_path)
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


def _cached_repo_paths(cache_root: Path) -> dict[tuple[str, str], object]:
    """Return ``{(repo, ref): stored repository path}`` from a db.db.

    Reads the same ``repos`` table as ``_cached_repos``, keeping the
    ``path`` column that records where the repository directory lived
    when the cache was built. The value is returned as SQLite stored
    it, so a hostile row can hold a BLOB.
    """
    db_path = cache_root / "db.db"
    if not db_path.exists():
        return {}
    try:
        conn = sqlite3.connect(str(db_path))
        try:
            rows = conn.execute("SELECT repo, ref, path FROM repos").fetchall()
        finally:
            conn.close()
    except sqlite3.DatabaseError:
        return {}
    return {(repo, ref): path for repo, ref, path in rows}


def _contained_real_path_rejection(path: Path, root: Path) -> str | None:
    """Return why *path* cannot be trusted inside *root*, or None.

    The path must not be a symlink and, when it exists, must resolve
    inside the resolved *root*, so archive-controlled symlinks cannot
    redirect reads, writes, or execution outside the root. A missing
    path is not rejected here; callers report absence where it matters.
    """
    if path.is_symlink():
        return f"{path} is a symlink"
    if not path.exists():
        return None
    resolved = path.resolve()
    resolved_root = root.resolve()
    if resolved != resolved_root and resolved_root not in resolved.parents:
        return f"{path} resolves outside {root}"
    return None


def _home_rejection(home: str) -> str | None:
    """Return why a ``pyvenv.cfg`` ``home`` value is untrusted, or None.

    Python reads ``home`` during path initialization, before ``-S`` or
    ``-c`` take effect, so it must name ``/usr`` or a directory below
    it. Traversal components are rejected outright, and the resolved
    path must stay below ``/usr``, so a symlink cannot escape.
    """
    home_path = Path(home)
    if not home_path.is_absolute():
        return f"home {home} is not an absolute path"
    if ".." in home_path.parts:
        return f"home {home} contains '..'"
    resolved = home_path.resolve()
    if resolved == Path("/usr") or Path("/usr") in resolved.parents:
        return None
    detail = f" (resolves to {resolved})" if str(resolved) != home else ""
    return f"home {home} is not /usr or below /usr{detail}"


class _EnvCheckResult(NamedTuple):
    """Why an environment could not be normalized, and how severe it is.

    An unsafe environment is archive-controlled and must block the
    promotion; an unverified one only may need a reinstall.
    """

    reason: str
    unsafe: bool = False


# An environment's path relative to the cache root and the reason it
# could not be normalized.
_EnvFinding = tuple[Path, str]


def _staged_db_rejection(staging: Path) -> str | None:
    """Return why the staged ``db.db`` cannot be read, or None.

    A db.db that exists must be a regular file inside the staging
    directory; a missing one is left to the coverage check.
    """
    db_path = staging / "db.db"
    if not db_path.exists() and not db_path.is_symlink():
        return None
    if db_path.is_symlink():
        return f"the staged {db_path} is a symlink"
    if not db_path.is_file():
        return f"the staged {db_path} is not a regular file"
    resolved = db_path.resolve()
    resolved_staging = staging.resolve()
    if resolved != resolved_staging and resolved_staging not in resolved.parents:
        return f"the staged {db_path} resolves outside the staging directory"
    return None


def _repository_relative(stored: str, cache_root: Path) -> Path:
    """Return a validated stored path's position inside the cache root."""
    return Path(os.path.normpath(stored)).relative_to(
        Path(os.path.normpath(cache_root))
    )


def _repository_relative_or_none(stored: object, cache_root: Path) -> Path | None:
    """Return a stored path's position inside the cache root, or None.

    Used for live rows, whose stored path was validated when the row
    was staged; an unusable value simply carries no environments to
    compare.
    """
    if not isinstance(stored, str) or not stored or not os.path.isabs(stored):
        return None
    if ".." in Path(stored).parts:
        return None
    try:
        return _repository_relative(stored, cache_root)
    except ValueError:
        return None


# The environment directory names pre-commit 4.5.1 uses, matched
# exactly or with a version suffix (``py_env``, ``py_env-python3.12``).
# docker does create a docker environment directory; docker_image,
# fail, pygrep, and the unsupported languages set ENVIRONMENT_DIR to
# None.
_ENVIRONMENT_DIR_NAMES = frozenset(
    {
        "conda",
        "coursier",
        "dartenv",
        "docker",
        "dotnetenv",
        "golangenv",
        "hs_env",
        "juliaenv",
        "lua_env",
        "node_env",
        "perl_env",
        "py_env",
        "rbenv",
        "renv",
        "rustenv",
        "swift_env",
    }
)


def _is_environment_dir(name: str) -> bool:
    """Report whether *name* is a pre-commit environment directory name.

    pre-commit names environments exactly (``py_env``, ``rbenv``) or
    with a version suffix (``py_env-python3.12``); names such as
    ``build_env_scripts`` are not environments.
    """
    if name in _ENVIRONMENT_DIR_NAMES:
        return True
    head, separator, _ = name.partition("-")
    return separator == "-" and head in _ENVIRONMENT_DIR_NAMES


def _environment_dirs(repo_dir: Path) -> set[str]:
    """Return the environment directory names under *repo_dir*."""
    if not repo_dir.is_dir():
        return set()
    return {
        entry.name
        for entry in repo_dir.iterdir()
        if entry.is_dir() and _is_environment_dir(entry.name)
    }


def _staged_repository_rejection(
    stored: object,
    staging: Path,
    cache_root: Path,
    *,
    require_manifest: bool = True,
) -> str | None:
    """Return why a staged repository row cannot be used, or None.

    The stored value must be an absolute path string without ``..``
    components inside the cache root used at build time — ``cache_root``
    under the path contract — and the staged tree must hold that
    directory as a real directory: a symlink, or a directory whose
    resolved path leaves the staging tree, is rejected, because the
    archive's symlink members may point anywhere. Cloned repositories
    must also hold a ``.pre-commit-hooks.yaml``, or pre-commit later
    fails to load the repository's hook manifest; rows for ``local``,
    ``local:`` keys, and ``meta`` carry no manifest, so
    *require_manifest* is False for them. Both sides are compared
    lexically with ``os.path.normpath``: resolving would follow
    symlinks in the live cache and inspect the wrong staging directory.
    """
    if not isinstance(stored, str) or not stored or not os.path.isabs(stored):
        return f"the stored path {stored!r} is not an absolute path"
    if ".." in Path(stored).parts:
        # normpath would collapse ``..`` lexically, but the filesystem
        # resolves the original spelling through any symlink in the
        # path, so the two can name different directories.
        return f"the stored path {stored} contains '..'"
    normalized_stored = Path(os.path.normpath(stored))
    normalized_root = Path(os.path.normpath(cache_root))
    if (
        normalized_stored != normalized_root
        and normalized_root not in normalized_stored.parents
    ):
        return f"the stored path {stored} is outside the cache root {cache_root}"
    staged_dir = staging / _repository_relative(stored, cache_root)
    if staged_dir.is_symlink():
        return f"the repository directory {staged_dir} is a symlink"
    if not staged_dir.is_dir():
        return f"the repository directory {staged_dir} is missing from the archive"
    resolved_staging = staging.resolve()
    resolved_dir = staged_dir.resolve()
    if (
        resolved_dir != resolved_staging
        and resolved_staging not in resolved_dir.parents
    ):
        return (
            f"the repository directory {staged_dir} resolves outside the "
            f"staging directory"
        )
    if require_manifest:
        manifest = staged_dir / ".pre-commit-hooks.yaml"
        manifest_rejection = _contained_real_path_rejection(manifest, staging)
        if manifest_rejection:
            return f"the manifest {manifest_rejection}"
        if not manifest.is_file():
            return f"{staged_dir} has no .pre-commit-hooks.yaml"
    return None


def _python_env_dirs(cache_root: Path) -> list[Path]:
    """Return every ``py_env`` or ``py_env-*`` directory under a cache root."""
    if not cache_root.is_dir():
        return []
    directories: list[Path] = []
    for repo_dir in cache_root.iterdir():
        if not repo_dir.is_dir():
            continue
        for env_dir in repo_dir.iterdir():
            if not env_dir.is_dir():
                continue
            if env_dir.name == "py_env" or env_dir.name.startswith("py_env-"):
                directories.append(env_dir)
    return sorted(directories)


def _read_pyvenv_cfg(cfg: Path) -> dict[str, str]:
    """Parse the ``key = value`` lines of a ``pyvenv.cfg``.

    Raises ``OSError`` or ``UnicodeDecodeError`` when the file cannot be
    read; callers that only warn catch those themselves.
    """
    values: dict[str, str] = {}
    for line in cfg.read_text(encoding="utf-8").splitlines():
        key, separator, value = line.partition("=")
        if separator:
            values[key.strip()] = value.strip()
    return values


def _pyvenv_version_info(env_dir: Path) -> str | None:
    """Return the ``version_info`` recorded in *env_dir*'s ``pyvenv.cfg``."""
    try:
        values = _read_pyvenv_cfg(env_dir / "pyvenv.cfg")
    except (OSError, UnicodeDecodeError):
        return None
    return values.get("version_info")


def _pyvenv_major_minor(version_info: str) -> str | None:
    """Return the ``major.minor`` of a ``version_info`` string, or None."""
    parts = version_info.split(".")
    if len(parts) < 2:
        return None
    return f"{parts[0]}.{parts[1]}"


def _version_key(version: str) -> str:
    """Return the ``major.minor.micro`` part of a version string."""
    return ".".join(version.split(".")[:3])


def _warn_on_interpreter_mismatch(pre_commit_home: Path) -> None:
    """Warn when the restored cache was built for another interpreter.

    Every python environment is judged by its ``pyvenv.cfg``
    ``version_info``, not by its directory name: pre-commit names the
    directory after the hook's ``language_version``, which carries no
    interpreter information for names such as ``py_env-python3``,
    ``py_env-default``, or plain ``py_env``.
    """
    current = f"{sys.version_info.major}.{sys.version_info.minor}"
    mismatched = []
    unreadable = []
    for env_dir in _python_env_dirs(pre_commit_home):
        version_info = _pyvenv_version_info(env_dir)
        if version_info is None:
            unreadable.append(env_dir)
        elif _pyvenv_major_minor(version_info) != current:
            mismatched.append(env_dir)
    if not mismatched and not unreadable:
        return
    details = []
    if mismatched:
        listing = ", ".join(str(directory) for directory in mismatched)
        details.append(f"built for another interpreter: {listing}")
    if unreadable:
        listing = ", ".join(str(directory) for directory in unreadable)
        details.append(f"pyvenv.cfg missing or unreadable: {listing}")
    print(
        f"WARNING: the restored pre-commit cache has python hook "
        f"environments {' and '.join(details)}; this interpreter is Python "
        f"{current}. Hooks that use the python language will fail until the "
        f"cache is rebuilt on this interpreter.",
        file=sys.stderr,
    )


def _rewrite_version_info(env_dir: Path, version_info: str, cache_root: Path) -> bool:
    """Replace the ``version_info`` line, preserving every other line.

    The rewrite is refused, without raising, when the resolved
    environment directory or ``pyvenv.cfg`` lies outside *cache_root*,
    so a symlink cannot direct the write outside the cache. The new
    content lands in a temporary file that ``tempfile.mkstemp`` creates
    exclusively in the validated directory, and replaces ``pyvenv.cfg``
    with one rename.
    """
    resolved_root = cache_root.resolve()
    resolved_env = env_dir.resolve()
    if resolved_env != resolved_root and resolved_root not in resolved_env.parents:
        return False
    cfg = env_dir / "pyvenv.cfg"
    resolved_cfg = cfg.resolve()
    if resolved_cfg != resolved_root and resolved_root not in resolved_cfg.parents:
        return False
    lines = cfg.read_text(encoding="utf-8").splitlines()
    rewritten = []
    replaced = False
    for line in lines:
        key, separator, _ = line.partition("=")
        if separator and key.strip() == "version_info":
            rewritten.append(f"version_info = {version_info}")
            replaced = True
        else:
            rewritten.append(line)
    if not replaced:
        rewritten.append(f"version_info = {version_info}")
    # mkstemp creates the file exclusively with a random name, so a
    # symlink planted at a predictable temporary name cannot redirect
    # the write.
    descriptor, temporary_name = tempfile.mkstemp(dir=env_dir, prefix=".pyvenv.cfg.")
    temporary = Path(temporary_name)
    try:
        os.fchmod(descriptor, 0o644)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write("\n".join(rewritten) + "\n")
        os.replace(temporary, cfg)
    except OSError:
        _remove_quietly(temporary)
        raise
    finally:
        # On the success path the ``with`` block already closed the
        # descriptor; close it on every other path so a failing fchmod
        # or write cannot leak it. Closing an already closed descriptor
        # raises OSError, which is ignored here.
        try:
            os.close(descriptor)
        except OSError:
            pass
    return True


def _interpreter_link_rejection(
    interpreter: Path, current: str, recorded: str
) -> _EnvCheckResult | None:
    """Return why *interpreter* must not be executed, or None.

    The environment's ``bin/python`` comes from the archive, so it is
    only executed when it is a symlink to a platform interpreter: the
    running interpreter, ``/usr/bin/python3``, or ``/usr/bin/python<X.Y>``
    for the running interpreter or the version the environment
    records — a cache built for another interpreter still names a
    platform binary. A missing interpreter is only unverifiable; a
    link to anything else is unsafe.
    """
    if not os.path.lexists(interpreter):
        return _EnvCheckResult(f"bin/python {interpreter} is missing")
    if not interpreter.is_symlink():
        return _EnvCheckResult(
            f"bin/python {interpreter} is not a symlink", unsafe=True
        )
    target = interpreter.resolve()
    if target == Path(sys.executable).resolve():
        return None
    allowed_names = {"python3", f"python{current}", f"python{recorded}"}
    if target.parent != Path("/usr/bin") or target.name not in allowed_names:
        return _EnvCheckResult(
            f"bin/python resolves to {target}, not /usr/bin/python3, "
            f"/usr/bin/python{current}, /usr/bin/python{recorded}, or the "
            f"running interpreter",
            unsafe=True,
        )
    return None


def _probe_interpreter(executable: Path) -> tuple[str | None, str | None]:
    """Ask *executable* for its version; return (version, failure reason)."""
    try:
        result = subprocess.run(
            [
                str(executable),
                "-S",
                "-c",
                "import sys;print('.'.join(str(p) for p in sys.version_info))",
            ],
            capture_output=True,
            text=True,
            timeout=60,
            check=False,
        )
    except subprocess.TimeoutExpired:
        return None, f"the interpreter probe timed out: {executable}"
    except (OSError, subprocess.SubprocessError) as error:
        return None, f"the interpreter probe failed: {error}"
    if result.returncode != 0:
        return None, f"the interpreter probe exited {result.returncode}"
    reported = result.stdout.strip()
    if _pyvenv_major_minor(reported) is None:
        return None, f"the interpreter reported an unparseable version: {reported!r}"
    return reported, None


def _base_executable_path_rejection(
    base_executable: str, current: str, recorded: str
) -> _EnvCheckResult | None:
    """Static check: the base executable must name a platform interpreter.

    ``base-executable`` comes from the archive's ``pyvenv.cfg``, so a
    value that is empty, relative, or not ``/usr/bin/python3``,
    ``/usr/bin/python<current>``, or ``/usr/bin/python<recorded>`` is
    unsafe. The file need not exist for this check; the dynamic check
    handles existence and version agreement once the version gate has
    passed.
    """
    if not base_executable:
        return _EnvCheckResult("base-executable is empty", unsafe=True)
    base = Path(base_executable)
    allowed_names = {"python3", f"python{current}", f"python{recorded}"}
    if (
        not base.is_absolute()
        or base.parent != Path("/usr/bin")
        or base.name not in allowed_names
    ):
        return _EnvCheckResult(
            f"base-executable {base_executable} is not /usr/bin/python3, "
            f"/usr/bin/python{current}, or /usr/bin/python{recorded}",
            unsafe=True,
        )
    return None


def _base_executable_version_rejection(
    base_executable: str, reported: str
) -> _EnvCheckResult | None:
    """Dynamic check: the base executable must agree with the environment."""
    base = Path(base_executable)
    if not base.is_file():
        return _EnvCheckResult(f"base-executable {base_executable} does not exist")
    base_reported, failure = _probe_interpreter(base)
    if failure:
        return _EnvCheckResult(failure)
    if _version_key(base_reported) != _version_key(reported):
        return _EnvCheckResult(
            f"base-executable {base_executable} reports {base_reported}, but "
            f"the environment reports {reported}"
        )
    return None


def _verify_and_normalize_env(
    env_dir: Path, cache_root: Path, current: str
) -> _EnvCheckResult | None:
    """Normalize one environment; return a failure result or None.

    Every static safety check runs before the version gate:
    pre-commit's own health check executes ``bin/python`` before it
    compares versions, so a mismatched ``version_info`` must not skip
    them. After the gate, the dynamic checks probe the interpreters
    and rewrite the metadata; their failures are unverified, not
    unsafe. Returns None without changes when the recorded
    ``major.minor`` differs from this interpreter; the restore's
    interpreter-mismatch warning reports that case.
    """
    # 1. The environment directory must be a real path inside the
    #    cache; only bin/python may be a symlink, to a platform
    #    interpreter.
    env_rejection = _contained_real_path_rejection(env_dir, cache_root)
    if env_rejection:
        return _EnvCheckResult(
            f"the environment directory {env_rejection}", unsafe=True
        )
    # 2. pyvenv.cfg must be a real path inside the cache.
    cfg = env_dir / "pyvenv.cfg"
    cfg_rejection = _contained_real_path_rejection(cfg, cache_root)
    if cfg_rejection:
        return _EnvCheckResult(f"pyvenv.cfg {cfg_rejection}", unsafe=True)
    # 3. Read pyvenv.cfg; a read error is unverified.
    values = _read_pyvenv_cfg(cfg)
    version_info = values.get("version_info", "")
    # The recorded interpreter names a platform binary too: a cache
    # built for another interpreter must reach the version gate, not
    # fail the static checks.
    recorded = _pyvenv_major_minor(version_info) or current
    # 4. home must name /usr or a directory below it; Python reads it
    #    during path initialization, before -S or -c take effect.
    if values.get("home"):
        home_rejection = _home_rejection(values["home"])
        if home_rejection:
            return _EnvCheckResult(home_rejection, unsafe=True)
    # 5. bin/python may only be a symlink to a platform interpreter.
    interpreter = env_dir / "bin" / "python"
    gate = _interpreter_link_rejection(interpreter, current, recorded)
    if gate:
        return gate
    # 6. base-executable must statically name a platform interpreter.
    if "base-executable" in values:
        base_path_result = _base_executable_path_rejection(
            values["base-executable"], current, recorded
        )
        if base_path_result:
            return base_path_result
    # 7. Version gate: a cache built for another interpreter is only
    #    reported by the interpreter-mismatch warning.
    if not version_info or _pyvenv_major_minor(version_info) != current:
        return None
    # 8. Dynamic checks.
    reported, probe_failure = _probe_interpreter(interpreter)
    if probe_failure:
        return _EnvCheckResult(probe_failure)
    if _pyvenv_major_minor(reported) != current:
        return _EnvCheckResult(f"bin/python reports {reported}, not Python {current}")
    if "base-executable" in values:
        base_result = _base_executable_version_rejection(
            values["base-executable"], reported
        )
        if base_result:
            return base_result
    # A real interpreter reports five components, for example
    # ``3.12.4.final.0``; keep major.minor.micro and normalize the tail
    # to the form virtualenv writes.
    rewritten = f"{_version_key(reported)}.final.0"
    if rewritten != version_info and not _rewrite_version_info(
        env_dir, rewritten, cache_root
    ):
        return _EnvCheckResult(
            f"the environment or its pyvenv.cfg resolves outside {cache_root}",
            unsafe=True,
        )
    return None


def _normalize_pyvenv_patch_versions(
    cache_root: Path,
) -> tuple[list[_EnvFinding], list[_EnvFinding]]:
    """Verify and rewrite each python environment's recorded patch version.

    The runner cannot know the sandbox image's Python patch version, so
    a ``pyvenv.cfg`` built with, for example, 3.12.4 fails pre-commit's
    own health check against a sandbox that has 3.12.3, and pre-commit
    then tries an offline reinstall. For every environment whose
    recorded ``major.minor`` matches this interpreter, the
    environment's ``bin/python`` is asked for its version — the same
    interpreter that pre-commit will run — and the ``version_info``
    line is rewritten to the reported ``major.minor.micro`` followed by
    ``.final.0``, the form virtualenv writes. Running this before the
    promotion means a concurrent ``lint.py`` never sees unrepaired
    metadata. Returns ``(unsafe, unverified)`` as ``(path relative to
    *cache_root*, reason)`` pairs. Unsafe environments are
    archive-controlled escapes and must block the promotion; unverified
    ones may need a reinstall, which pre-commit attempts itself. An
    environment whose ``pyvenv.cfg`` cannot be read or rewritten for a
    non-containment reason counts as unverified. No other metadata is
    changed.
    """
    current = f"{sys.version_info.major}.{sys.version_info.minor}"
    unsafe: list[_EnvFinding] = []
    unverified: list[_EnvFinding] = []
    for env_dir in _python_env_dirs(cache_root):
        try:
            result = _verify_and_normalize_env(env_dir, cache_root, current)
        except (OSError, UnicodeDecodeError) as error:
            result = _EnvCheckResult(
                f"pyvenv.cfg could not be read or rewritten: {error}"
            )
        if result is None:
            continue
        entry = (env_dir.relative_to(cache_root), result.reason)
        if result.unsafe:
            unsafe.append(entry)
        else:
            unverified.append(entry)
    return unsafe, unverified


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
    staged cache is in place. When the rollback itself fails, the
    backup is left in place and the error names it and the recovery
    command.
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
    except OSError as promotion_error:
        if not moved_aside:
            raise
        try:
            os.replace(backup, pre_commit_home)
        except OSError as rollback_error:
            # Keep the promotion error as the primary cause and tell the
            # operator where the live cache is and how to recover it.
            recovery = (
                f"mv -- {shlex.quote(str(backup))} {shlex.quote(str(pre_commit_home))}"
            )
            raise OSError(
                f"{promotion_error}; the live cache was moved to {backup} "
                f"and could not be moved back ({rollback_error}); recover "
                f"it with: {recovery}"
            ) from promotion_error
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
        db_rejection = _staged_db_rejection(staging)
        if db_rejection:
            print(
                f"WARNING: cannot restore the pre-commit cache from "
                f"{tarball}: {db_rejection}; keeping the tarball and the "
                f"existing cache.",
                file=sys.stderr,
            )
            return False
        staged_paths = _cached_repo_paths(staging)
        live = _cached_repos(pre_commit_home)
        live_paths = _cached_repo_paths(pre_commit_home)
        # A row whose repo or ref is not text cannot be sorted or
        # compared; reject the archive instead of crashing.
        malformed = sorted(
            (
                row
                for row in set(staged_paths) | live
                if not all(isinstance(part, str) for part in row)
            ),
            key=repr,
        )
        if malformed:
            print(
                f"WARNING: the archive {tarball} or the live cache holds a "
                f"malformed repository row {malformed!r}; repo and ref must "
                f"be text. Keeping the tarball and the existing cache.",
                file=sys.stderr,
            )
            return False
        if not required <= set(staged_paths):
            print(
                f"WARNING: the archive {tarball} does not cover every "
                f"configured hook repository revision "
                f"({_format_revisions(required - set(staged_paths))}); keeping "
                f"the tarball and the existing cache.",
                file=sys.stderr,
            )
            return False
        if not live <= set(staged_paths):
            print(
                f"WARNING: promoting the archive {tarball} would drop cached "
                f"revisions that the live cache {pre_commit_home} already "
                f"holds ({_format_revisions(live - set(staged_paths))}); "
                f"keeping the tarball and the live cache.",
                file=sys.stderr,
            )
            return False
        # Every required and retained revision needs its payload, so a
        # retained row without a repository directory cannot be promoted.
        # pre-commit stores local environments with additional
        # dependencies under ``local:`` keys without a hook manifest,
        # and never clones local or meta repositories, so those rows
        # need a valid stored path and directory but no manifest.
        for repo, rev in sorted(required | live):
            stored = staged_paths.get((repo, rev), "")
            require_manifest = repo not in _NON_CLONED_REPOS and not repo.startswith(
                "local:"
            )
            rejection = _staged_repository_rejection(
                stored,
                staging,
                pre_commit_home,
                require_manifest=require_manifest,
            )
            if rejection:
                print(
                    f"WARNING: the archive {tarball} cannot provide "
                    f"{repo} @ {rev}: {rejection}; keeping the tarball and "
                    f"the existing cache.",
                    file=sys.stderr,
                )
                return False
            if (repo, rev) in live:
                # A retained revision must keep its installed hook
                # environments: a matching row and repository directory
                # are not enough when the archive omits them. The live
                # side is looked up through the live row's stored path,
                # which can differ from the staged one.
                staged_relative = _repository_relative(stored, pre_commit_home)
                live_relative = _repository_relative_or_none(
                    live_paths.get((repo, rev), ""), pre_commit_home
                )
                if live_relative is not None:
                    staged_envs = _environment_dirs(staging / staged_relative)
                    missing = (
                        _environment_dirs(pre_commit_home / live_relative) - staged_envs
                    )
                    if missing:
                        listing = ", ".join(sorted(missing))
                        print(
                            f"WARNING: the archive {tarball} omits the installed "
                            f"environment(s) {listing} that the live cache holds "
                            f"for {repo} @ {rev}; keeping the tarball and the "
                            f"live cache.",
                            file=sys.stderr,
                        )
                        return False
        # Normalize before the promotion, so a concurrent lint.py never
        # sees unrepaired metadata, and so archive-controlled escapes
        # can block the promotion.
        unsafe, unverified = _normalize_pyvenv_patch_versions(staging)
        if unsafe:
            listing = "; ".join(
                f"{pre_commit_home / env} ({reason})" for env, reason in unsafe
            )
            print(
                f"WARNING: the archive {tarball} holds unsafe python hook "
                f"environments: {listing}; keeping the tarball and the "
                f"existing cache.",
                file=sys.stderr,
            )
            return False
        _swap_cache_home(staging, pre_commit_home)
    except (tarfile.TarError, EOFError, OSError, zlib.error) as error:
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
    if unverified:
        listing = "; ".join(
            f"{pre_commit_home / env} ({reason})" for env, reason in unverified
        )
        print(
            f"WARNING: could not normalize the version_info of {listing}; "
            f"pre-commit may try to reinstall these environments.",
            file=sys.stderr,
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
    config_path: Path = CONFIG_PATH,
) -> int:
    """Run the cache pre-flight, restoring from the tarball once when cold.

    Offline only: with ``--online`` the pre-flight passes without the
    cache and the tarball is never unpacked. The missing-revision error
    is reported only when no restore fixed the cache, so a successful
    restore prints no spurious error. The tarball is deleted only after
    a restored cache passes the pre-flight. *config_path* names the
    configuration in the remedy message.
    """
    if not online:
        configured = _configured_repos(config)
        missing = _missing_repos(configured, _cached_repos(pre_commit_home))
        if missing and _restore_cache(pre_commit_home, set(configured)):
            rc = _preflight(online, pre_commit_home, config, config_path)
            if rc == 0:
                _delete_tarball()
            return rc
    return _preflight(online, pre_commit_home, config, config_path)


def _report_config_preflight_failure(path: Path) -> None:
    """Name the configuration whose pre-flight failed."""
    print(
        f"ERROR: the configuration {_relative(path)} failed the offline "
        "cache pre-flight; the missing revision(s) reported above "
        "belong to it.",
        file=sys.stderr,
    )


def _preflight_configs(
    online: bool,
    pre_commit_home: Path,
    configs: list[tuple[Path, dict[str, Any]]],
) -> int:
    """Run the offline cache pre-flight for every configuration once.

    The bootstrap archive is built from the gate configuration alone,
    so the gate runs through ``_preflight_with_restore`` and that one
    call is also the gate's pre-flight: its return code is the gate's
    result. Every other configuration runs through ``_preflight``. No
    configuration is pre-flighted twice, so a cold cache reports each
    missing-revision block exactly once. Each failure names its
    configuration. Returns non-zero when any configuration fails,
    before hooks run or configurations are validated. A missing CI
    configuration never reaches this function
    (``_existing_config_paths`` filtered it out), so merge skew
    produces no pre-flight error.

    The gate comes first (``_existing_config_paths`` orders it so) on
    purpose: its restore can change the cache that the other
    configurations are pre-flighted against. Each configuration's path
    reaches the remedy message, so ``install-hooks`` fetches the
    revisions of the configuration that reported them.
    """
    if online:
        return 0
    failed = False
    for path, config in configs:
        if path == CONFIG_PATH:
            rc = _preflight_with_restore(online, pre_commit_home, config, path)
        else:
            rc = _preflight(online, pre_commit_home, config, path)
        if rc != 0:
            _report_config_preflight_failure(path)
            failed = True
    return 1 if failed else 0


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


def _pre_commit_run_command(path: Path, scope_args: list[str]) -> list[str]:
    """Return the ``pre-commit run`` argv that runs *path*."""
    return ["pre-commit", "run", "--config", str(path), *scope_args]


def _run_pre_commit(argv: list[str]) -> int | None:
    """Run one ``pre-commit`` command and return its exit code.

    Returns None when the ``pre-commit`` binary cannot run — for
    example it is not installed (``OSError``). The caller reports
    that failure once per wrapper run, not once per configuration.
    """
    try:
        return subprocess.run(argv, cwd=str(REPO_ROOT), check=False).returncode
    except OSError:
        return None


def _report_missing_pre_commit() -> None:
    """Report, once, that the ``pre-commit`` binary could not run."""
    print(
        "ERROR: cannot run pre-commit. Install the pinned version "
        f"{PRE_COMMIT_VERSION}; `scripts/lint.py --print-pre-commit-version` "
        "prints it.",
        file=sys.stderr,
    )


def _run_each_config(paths: list[Path], scope_args: list[str]) -> int:
    """Run ``pre-commit run`` once per configuration and wait for it.

    Every configuration runs even when an earlier one fails or could
    not launch, so one failing configuration cannot hide another's
    findings. When any configuration fails, one stderr line names
    every failing configuration — launch failures included — because
    the exit code alone cannot say which one failed. Returns 1 when
    any configuration could not launch, the first non-zero exit code
    when hooks failed, or 0 when every configuration passes.
    """
    # Flush so lines this wrapper already printed (for example the
    # cache restore notice) appear before the child's own output.
    sys.stdout.flush()
    sys.stderr.flush()
    first_rc = 0
    launch_failed = False
    launch_error_reported = False
    failed: list[Path] = []
    for path in paths:
        rc = _run_pre_commit(_pre_commit_run_command(path, scope_args))
        if rc is None:
            # The binary could not launch. Report that once, record
            # the configuration as failed, and keep going so the
            # remaining configurations still run.
            if not launch_error_reported:
                _report_missing_pre_commit()
                launch_error_reported = True
            launch_failed = True
            failed.append(path)
            continue
        if rc != 0:
            if first_rc == 0:
                first_rc = rc
            failed.append(path)
    if failed:
        listing = ", ".join(_relative(path) for path in failed)
        print(f"ERROR: these configurations failed: {listing}", file=sys.stderr)
    if launch_failed:
        return 1
    return first_rc


def _check_parity(pre_commit_home: Path, online: bool) -> int:
    """Validate every existing configuration without running hooks.

    ``pre-commit validate-config`` runs once per existing
    configuration; an absent CI configuration is merge skew and is
    skipped. On success one line names every configuration and states
    that the offline cache covers the configured hook repositories —
    but only when the offline pre-flight verified the cache; the
    online path skips the pre-flight, so it claims validity alone.
    """
    paths = _existing_config_paths()
    first_rc = 0
    for path in paths:
        rc = _run_pre_commit(["pre-commit", "validate-config", str(path)])
        if rc is None:
            _report_missing_pre_commit()
            return 1
        if rc != 0 and first_rc == 0:
            first_rc = rc
    if first_rc == 0:
        listing = ", ".join(_relative(path) for path in paths)
        if online:
            print(f"PARITY CHECK PASSED: {listing} valid.")
        else:
            print(
                f"PARITY CHECK PASSED: {listing} valid, and the offline "
                f"cache ({pre_commit_home}) covers every configured hook "
                "repository."
            )
    return first_rc


def _bare_scope_args(
    configs: list[tuple[Path, dict[str, Any]]] | None,
) -> list[str] | None:
    """Return bare mode's scope arguments, or None when nothing runs.

    ``["--files", *changed]`` normally; when no file changed, the
    configurations decide: with no ``always_run`` hook in any of them
    there is nothing to check, and any ``always_run`` hook keeps every
    configuration running with ``--files`` and no file arguments
    (``pre-commit`` honours ``always_run`` itself). Reading the
    configurations for that decision is the only config read online
    bare mode performs.
    """
    changed = _changed_files()
    if not changed:
        if configs is None:
            configs = _load_each_config()
        if not any(_any_always_run(config) for _, config in configs):
            print("No files to check.")
            return None
    return ["--files", *changed]


def _scope_args(
    args: argparse.Namespace,
    configs: list[tuple[Path, dict[str, Any]]] | None,
) -> list[str] | None:
    """Return the scope arguments shared by every ``run`` invocation.

    None means there is nothing to run (bare mode, no changed files,
    no ``always_run`` hook anywhere).
    """
    if args.all_files:
        return ["--all-files"]
    if args.files:
        return ["--files", *args.files]
    return _bare_scope_args(configs)


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
            "validate both pre-commit configurations (.pre-commit-config.yaml "
            "and .github/pre-commit-ci.yaml; a missing CI configuration is "
            "merge skew and is skipped) and confirm every configured hook "
            "repository revision is present in the offline cache, without "
            "running any hooks"
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

    if not CONFIG_PATH.exists():
        print(
            f"ERROR: the gate configuration {CONFIG_PATH} is missing; it is required.",
            file=sys.stderr,
        )
        return 1

    # Read each existing configuration at most once, and only where a
    # code path needs it: the offline cache pre-flight below, and bare
    # mode's always_run check. The CI invocation (--all-files
    # --online-if-ci under CI=true) needs neither, so it runs without
    # PyYAML; pre-commit validates the configurations itself.
    configs: list[tuple[Path, dict[str, Any]]] | None = None
    if not online:
        configs = _load_each_config()
        preflight_rc = _preflight_configs(online, pre_commit_home, configs)
        if preflight_rc != 0:
            return preflight_rc

    if args.check_parity:
        return _check_parity(pre_commit_home, online)

    scope_args = _scope_args(args, configs)
    if scope_args is None:
        return 0
    if configs is not None:
        paths = [path for path, _ in configs]
    else:
        # Online --all-files/--files (and online bare mode after its
        # always_run decision) reads no configuration: the path list
        # alone selects what runs.
        paths = _existing_config_paths()
    return _run_each_config(paths, scope_args)


if __name__ == "__main__":
    sys.exit(main())
