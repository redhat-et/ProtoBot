#!/usr/bin/env python3
"""Offline pre-commit hook runner for sandboxed environments.

Reads ``.pre-commit-config.yaml`` and runs each configured hook using
locally installed tools, without network access to pre-commit's hook
repository cache.

Usage::

    python scripts/lint.py --all-files          # every tracked file
    python scripts/lint.py --files a.py b.yaml  # specific files
    python scripts/lint.py                      # changed files vs HEAD
    python scripts/lint.py --check-parity       # verify registry coverage

Tool provenance
---------------
Tools are resolved from ``PATH`` first (with version verification for
pip-installed packages); when absent, they are installed via ``pip`` or
``npx``, pinned to the version recorded in the frozen comment of
``.pre-commit-config.yaml``.  Installation requires network access —
in a fully offline sandbox, all tools must be pre-installed.
The YAML config is the **single source of truth** for which hooks run
and with what arguments (except for hooks with ``fixed_args`` in the
registry); this script only provides the *how* — a registry mapping
each repo URL to an installation method and command template.

When a hook has no registered handler the script reports it as
**UNSUPPORTED** and exits non-zero, ensuring silent coverage reduction
cannot happen.
"""

from __future__ import annotations

import argparse
import re
import shutil
import subprocess
import sys
from collections.abc import Callable
from pathlib import Path
from typing import Any

try:
    import yaml
except ImportError:
    sys.exit(
        "ERROR: PyYAML is required but not installed.\n"
        "       Install with:  pip install pyyaml"
    )

# ── Paths ──────────────────────────────────────────────────────

REPO_ROOT = Path(__file__).resolve().parent.parent
CONFIG_PATH = REPO_ROOT / ".pre-commit-config.yaml"

# ── File-type detection ────────────────────────────────────────

_EXT_TO_TYPE: dict[str, str] = {
    ".py": "python",
    ".pyi": "python",
    ".ipynb": "jupyter",
    ".json": "json",
    ".json5": "json5",
    ".jsonc": "json5",
    ".yaml": "yaml",
    ".yml": "yaml",
    ".toml": "toml",
    ".xml": "xml",
    ".md": "markdown",
    ".markdown": "markdown",
    ".sh": "shell",
    ".bash": "shell",
    ".zsh": "shell",
    ".ksh": "shell",
    ".go": "go",
}

_NAME_PREFIX_TYPES: list[tuple[str, str]] = [
    ("Dockerfile", "dockerfile"),
    ("Containerfile", "dockerfile"),
]


def _is_binary(path: str) -> bool:
    """Return *True* if *path* looks like a binary file.

    Checks the first 8 KB for null bytes — the same heuristic used by
    ``git diff`` and the ``identify`` library that pre-commit relies on.
    """
    try:
        with open(REPO_ROOT / path, "rb") as fh:
            chunk = fh.read(8192)
    except OSError:
        return False
    return b"\x00" in chunk


def _file_type(path: str) -> str:
    """Return a type tag for *path* based on name / extension."""
    name = Path(path).name
    for prefix, ftype in _NAME_PREFIX_TYPES:
        if name == prefix or name.startswith(prefix + "."):
            return ftype
    return _EXT_TO_TYPE.get(Path(path).suffix.lower(), "text")


def _filter_by_types(
    files: list[str],
    types: list[str] | None,
) -> list[str]:
    """Filter *files* to those matching any type in *types* (OR semantics).

    This intentionally uses OR semantics — a file matches if its type
    is any of the listed types.  Pre-commit's ``types`` field uses AND
    semantics via the ``identify`` library (where files carry multiple
    tags), but this script assigns a single tag per file, making AND
    over multiple tags impractical.  Prefer single-element ``types``
    lists in the registry to avoid semantic surprises; combining
    ``"text"`` with specific types is rejected to prevent silent
    misfiltering.
    """
    if not types:
        return files
    ts = set(types)
    if "all" in ts:
        return files
    if "text" in ts:
        if len(ts) > 1:
            raise ValueError(
                f"Cannot combine 'text' with specific types in types list: {types}. "
                "Use single-element types or separate hooks."
            )
        # "text" matches every recognised extension *except* binary files.
        # Pre-commit uses the ``identify`` library to make this distinction;
        # we approximate it by checking for null bytes in the first 8 KB.
        return [f for f in files if not _is_binary(f)]
    return [f for f in files if _file_type(f) in ts]


def _filter_by_regex(
    files: list[str],
    include: str | None = None,
    exclude: str | None = None,
) -> list[str]:
    """Filter *files* using include and exclude regular expressions.

    Applies *include* first (retaining files matching via ``re.search``),
    then filters out files matching *exclude* (also via ``re.search``).
    """
    result = files
    if include:
        pat = re.compile(include)
        result = [f for f in result if pat.search(f)]
    if exclude:
        pat = re.compile(exclude)
        result = [f for f in result if not pat.search(f)]
    return result


# ── Config helpers ─────────────────────────────────────────────


def _load_config() -> dict[str, Any]:
    with open(CONFIG_PATH) as fh:
        return yaml.safe_load(fh)


def _extract_frozen_versions() -> dict[str, str]:
    """Return ``{rev_sha: version}`` from ``# frozen: vX.Y.Z`` comments."""
    mapping: dict[str, str] = {}
    with open(CONFIG_PATH) as fh:
        for line in fh:
            m = re.match(r"\s*rev:\s*(\S+)\s+#\s*frozen:\s*v?([\d.]+)", line)
            if m:
                mapping[m.group(1)] = m.group(2)
    return mapping


def _normalize_url(url: str) -> str:
    url = re.sub(r"^https?://", "", url)
    url = url.rstrip("/")
    url = url.removesuffix(".git")
    return url


# ── Tool installation ──────────────────────────────────────────


def _pip_install(package: str, version: str | None) -> bool:
    spec = f"{package}=={version}" if version else package
    r = subprocess.run(
        [sys.executable, "-m", "pip", "install", "--quiet", spec],
        capture_output=True,
        text=True,
        check=False,
    )
    if r.returncode != 0:
        print(f"  pip install {spec} failed:\n  {r.stderr.strip()}")
        return False
    return True


def _ensure_tool(
    cmd: str,
    installer: str,
    package: str | None,
    version: str | None,
) -> bool:
    """Return *True* when *cmd* is (or becomes) available on ``PATH``."""
    if shutil.which(cmd):
        # Verify the installed version matches the frozen pin for
        # pip-installed packages.  Other installers (system, npx)
        # either specify the version at invocation time or lack a
        # reliable version query.
        #
        # Fail-closed: a definite version mismatch rejects the tool
        # to prevent running an outdated or attacker-placed binary.
        if installer == "pip" and package and version:
            try:
                from importlib.metadata import (
                    PackageNotFoundError,
                )
                from importlib.metadata import (
                    version as pkg_version,
                )

                try:
                    installed = pkg_version(package)
                except PackageNotFoundError:
                    print(
                        f"  REJECTED: cannot verify version for {package} on PATH"
                        f" (package not found in pip metadata; expected {version})",
                        file=sys.stderr,
                    )
                    return False

                if installed != version:
                    print(
                        f"  REJECTED: {package} {installed} on PATH"
                        f" does not match frozen version {version}",
                        file=sys.stderr,
                    )
                    return False
            except ImportError:
                # importlib.metadata module itself unavailable
                print(
                    f"  REJECTED: cannot verify version for {package} on PATH"
                    f" (importlib.metadata unavailable; expected {version})",
                    file=sys.stderr,
                )
                return False
        return True
    if installer == "pip" and package:
        if version is None:
            print(f"  Refusing to install {package}: no frozen version available")
            return False
        print(f"  Installing {package} via pip …")
        if _pip_install(package, version):
            return shutil.which(cmd) is not None
        return False
    if installer == "system":
        return False
    return installer in ("npx", "builtin")


# ── Built-in hook implementations ─────────────────────────────

# Pinned versions for builtin dependencies that are installed at runtime.
# These are Python library packages used by builtin hook implementations,
# not the hook tools themselves (which are pinned via .pre-commit-config.yaml).
#
# Cross-reference with .pre-commit-config.yaml:
#   - json5: runtime dependency for check-json5
#     (repo: https://gitlab.com/bmares/check-json5)
#
# NOTE: _BUILTIN_DEP_VERSIONS is intentionally decoupled from the repo
# revision in .pre-commit-config.yaml. The YAML entry's frozen comment
# (e.g. v1.0.1) records the Git tag of the upstream check-json5 wrapper
# repository, whereas the 'json5' package installed here is an independent
# PyPI parser library with its own semantic versioning (0.15.0).
_BUILTIN_DEP_VERSIONS: dict[str, str] = {
    "json5": "0.15.0",
}


def _builtin_unicode_replacement(files: list[str]) -> int:
    """Detect the UTF-8 replacement character U+FFFD."""
    bad = False
    replacement = b"\xef\xbf\xbd"
    for fp in files:
        try:
            data = (REPO_ROOT / fp).read_bytes()
        except OSError:
            continue
        if replacement in data:
            print(f"  {fp}: contains Unicode replacement character (U+FFFD)")
            bad = True
    return 1 if bad else 0


def _builtin_check_json5(files: list[str]) -> int:
    """Validate JSON5 files using the ``json5`` Python package."""
    try:
        import json5  # type: ignore[import-untyped]
    except ImportError:
        if not _pip_install("json5", _BUILTIN_DEP_VERSIONS["json5"]):
            print("  Cannot install json5 package")
            return 1
        import json5  # type: ignore[import-untyped]

    bad = False
    for fp in files:
        try:
            with open(REPO_ROOT / fp) as fh:
                json5.load(fh)
        except (ValueError, OSError) as exc:
            print(f"  {fp}: {exc}")
            bad = True
    return 1 if bad else 0


_BUILTINS: dict[str, Callable[[list[str]], int]] = {
    "check_unicode_replacement": _builtin_unicode_replacement,
    "check_json5": _builtin_check_json5,
}

# ── Hook registry ─────────────────────────────────────────────
#
# Maps normalised repo URLs → tool metadata.  The YAML config is
# the single source of truth for WHICH hooks to run and with what
# arguments; this registry only says HOW to install and invoke
# each tool.
#
# If a hook is added to the YAML without a corresponding entry
# here, the script reports it as UNSUPPORTED and exits non-zero.
#
# Argument-passing mechanisms (in command-line order):
#
#   1. ``prepend_args`` — inserted immediately after the base
#      command.  Use for sub-commands and their flags that must
#      always appear (e.g. ``["check"]`` → ``ruff check …``).
#
#   2. ``fixed_args`` — appended after prepend_args.  When present
#      these REPLACE the YAML-configured ``args`` entirely, so the
#      hook ignores per-repo argument overrides.
#
#   3. YAML ``args`` — appended after prepend_args when
#      ``fixed_args`` is absent.  This is the normal per-repo
#      configuration from ``.pre-commit-config.yaml``.

_REGISTRY: dict[str, dict[str, Any]] = {
    # ── Python: pre-commit-hooks ───────────────────────────────
    "github.com/pre-commit/pre-commit-hooks": {
        "installer": "pip",
        "package": "pre-commit-hooks",
        "hooks": {
            "check-added-large-files": {"types": ["all"]},
            "check-docstring-first": {"types": ["python"]},
            "check-json": {"types": ["json"]},
            "check-merge-conflict": {"types": ["text"]},
            "check-symlinks": {"types": ["all"]},
            "check-toml": {"types": ["toml"]},
            "check-xml": {"types": ["xml"]},
            "debug-statements": {
                "cmd": "debug-statement-hook",
                "types": ["python"],
            },
            "end-of-file-fixer": {"types": ["text"]},
            "fix-byte-order-marker": {"types": ["text"]},
            "trailing-whitespace": {
                "cmd": "trailing-whitespace-fixer",
                "types": ["text"],
            },
        },
    },
    # ── Python: pygrep-hooks (builtin) ─────────────────────────
    "github.com/pre-commit/pygrep-hooks": {
        "installer": "builtin",
        "hooks": {
            "text-unicode-replacement-char": {
                "builtin_fn": "check_unicode_replacement",
                "types": ["text"],
            },
        },
    },
    # ── Python: yamllint ───────────────────────────────────────
    "github.com/adrienverge/yamllint": {
        "installer": "pip",
        "package": "yamllint",
        "hooks": {
            "yamllint": {"types": ["yaml"]},
        },
    },
    # ── Python/Rust: ruff ──────────────────────────────────────
    "github.com/astral-sh/ruff-pre-commit": {
        "installer": "pip",
        "package": "ruff",
        "hooks": {
            "ruff-check": {
                "cmd": "ruff",
                "prepend_args": ["check"],
                "types": ["python", "jupyter"],
            },
            "ruff-format": {
                "cmd": "ruff",
                "prepend_args": ["format", "--check", "--diff"],
                "types": ["python", "jupyter"],
            },
        },
    },
    # ── System: uv ─────────────────────────────────────────────
    "github.com/astral-sh/uv-pre-commit": {
        "installer": "system",
        "hooks": {
            "uv-lock": {
                "cmd": "uv",
                "fixed_args": ["lock", "--check"],
                "pass_filenames": False,
                "default_files": r"(^|/)pyproject\.toml$",
            },
        },
    },
    # ── Python: skillsaw ───────────────────────────────────────
    "github.com/stbenjam/skillsaw": {
        "installer": "pip",
        "package": "skillsaw",
        "hooks": {
            "skillsaw": {"pass_filenames": False},
        },
    },
    # ── Node: markdownlint-cli2 ────────────────────────────────
    "github.com/DavidAnson/markdownlint-cli2": {
        "installer": "npx",
        "package": "markdownlint-cli2",
        "hooks": {
            "markdownlint-cli2": {"types": ["markdown"]},
        },
    },
    # ── System binary: hadolint ────────────────────────────────
    "github.com/hadolint/hadolint": {
        "installer": "system",
        "hooks": {
            "hadolint": {"types": ["dockerfile"]},
        },
    },
    # ── Python (bundles binary): shellcheck ────────────────────
    "github.com/shellcheck-py/shellcheck-py": {
        "installer": "pip",
        "package": "shellcheck-py",
        "hooks": {
            "shellcheck": {"types": ["shell"]},
        },
    },
    # ── Python: detect-secrets ─────────────────────────────────
    "github.com/Yelp/detect-secrets": {
        "installer": "pip",
        "package": "detect-secrets",
        "hooks": {
            "detect-secrets": {
                "cmd": "detect-secrets-hook",
                "types": ["text"],
            },
        },
    },
    # ── Builtin (json5 package): check-json5 ───────────────────
    "gitlab.com/bmares/check-json5": {
        "installer": "builtin",
        "hooks": {
            "check-json5": {
                "builtin_fn": "check_json5",
                "types": ["json5"],
            },
        },
    },
    # ── Python/Rust: zizmor ────────────────────────────────────
    "github.com/zizmorcore/zizmor-pre-commit": {
        "installer": "pip",
        "package": "zizmor",
        "hooks": {
            "zizmor": {
                "types": ["yaml"],
                "default_files": r"^\.github/",
                # --offline avoids network-dependent audits that
                # cannot run in sandboxed environments.
                "prepend_args": ["--offline"],
            },
        },
    },
    # ── Node: renovate-config-validator ────────────────────────
    "github.com/renovatebot/pre-commit-hooks": {
        "installer": "npx",
        "package": "renovate",
        "hooks": {
            "renovate-config-validator": {
                "pass_filenames": False,
                "default_files": r"(^|/)\.?renovate(rc)?\.json5?$",
            },
        },
    },
    # ── Local: gofmt, go vet ──────────────────────────────
    "local": {
        "installer": "system",
        "hooks": {
            "gofmt": {
                "cmd": "python3",
                "fixed_args": ["scripts/check_gofmt.py"],
                "types": ["go"],
                "default_files": r"\.go$",
            },
            "go-vet": {
                "cmd": "python3",
                "fixed_args": ["scripts/check_govet.py"],
                "types": ["go"],
                "default_files": r"\.go$",
            },
        },
    },
}


# ── Hook execution ─────────────────────────────────────────────


def _run_hook(
    hook_id: str,
    hook_info: dict[str, Any],
    repo_info: dict[str, Any],
    hook_yaml: dict[str, Any],
    files: list[str],
    version: str | None,
) -> tuple[str, str]:
    """Run one hook.  Returns ``(status, detail)``."""

    # ── Common file filtering ─────────────────────────────────
    # The YAML config may override the upstream hook's ``files``
    # pattern.  When absent, fall back to the registry's
    # ``default_files`` (which mirrors the upstream definition).
    files_pattern = hook_yaml.get("files") or hook_info.get("default_files")
    exclude_pattern = hook_yaml.get("exclude")

    # ── Early file filtering (all hook types) ───────────────────
    # Determine matching files BEFORE installing or locating the
    # tool so that hooks with no applicable files are skipped
    # cheaply (e.g. hadolint in a repo with no Dockerfiles).
    pass_filenames = hook_info.get("pass_filenames", True)
    hook_files = _filter_by_types(files, hook_info.get("types"))
    hook_files = _filter_by_regex(
        hook_files,
        files_pattern,
        exclude_pattern,
    )
    if not hook_files:
        return "skip", "no matching files"

    # ── Builtin hooks ──────────────────────────────────────────
    if "builtin_fn" in hook_info:
        fn = _BUILTINS.get(hook_info["builtin_fn"])
        if fn is None:
            return "unsupported", f"Unknown builtin: {hook_info['builtin_fn']}"
        rc = fn(hook_files)
        return ("fail" if rc else "pass"), ""

    # ── External tools ─────────────────────────────────────────
    cmd = hook_info.get("cmd", hook_id)
    installer = repo_info["installer"]
    package = repo_info.get("package")

    # For npx tools, the command is run via npx
    if installer == "npx":
        pkg = package or cmd
        if version is None:
            return "unavailable", f"no frozen version for {pkg}"
        pkg_spec = f"{pkg}@{version}"
        if cmd != pkg:
            # When the hook command differs from the package name
            # (e.g. renovate-config-validator from renovate), use
            # --package to specify the package and then the command.
            base_cmd: list[str] = [
                "npx",
                "--yes",
                "--package",
                pkg_spec,
                cmd,
            ]
        else:
            base_cmd = ["npx", "--yes", pkg_spec]
    else:
        if not _ensure_tool(cmd, installer, package, version):
            return "unavailable", f"{cmd} is not installed"
        base_cmd = [cmd]

    full_cmd = list(base_cmd)

    # Sub-commands (e.g. "ruff check", "ruff format --check")
    if "prepend_args" in hook_info:
        full_cmd.extend(hook_info["prepend_args"])

    # Fixed args replace the YAML-configured args entirely
    if "fixed_args" in hook_info:
        if "args" in hook_yaml:
            print(
                f"WARNING: {hook_id}: YAML args {hook_yaml['args']} "
                f"discarded in favor of fixed_args {hook_info['fixed_args']}",
                file=sys.stderr,
            )
        full_cmd.extend(hook_info["fixed_args"])
    elif "args" in hook_yaml:
        full_cmd.extend(hook_yaml["args"])

    if pass_filenames:
        full_cmd.append("--")
        full_cmd.extend(hook_files)

    try:
        result = subprocess.run(
            full_cmd,
            capture_output=True,
            text=True,
            cwd=str(REPO_ROOT),
            timeout=300,
            check=False,
        )
    except FileNotFoundError:
        return "unavailable", f"command not found: {full_cmd[0]}"
    except PermissionError:
        return "unavailable", f"permission denied: {full_cmd[0]}"
    except subprocess.TimeoutExpired:
        return "fail", "timed out after 300 s"

    if result.returncode == 0:
        return "pass", ""

    detail = (result.stdout + result.stderr).strip()
    return "fail", detail


# ── File collection ────────────────────────────────────────────


def _git(*args: str, critical: bool = False) -> str:
    """Run a git command and return its stdout.

    When *critical* is ``True`` and the command fails, the script exits
    non-zero instead of silently returning empty output — preventing a
    broken git from masking lint violations.
    """
    r = subprocess.run(
        ["git", *args],
        capture_output=True,
        text=True,
        cwd=str(REPO_ROOT),
        check=False,
    )
    if r.returncode != 0:
        stderr = r.stderr.strip()
        msg = f"git {' '.join(args)} exited {r.returncode}" + (
            f": {stderr}" if stderr else ""
        )
        if critical:
            sys.exit(f"ERROR: {msg}")
        print(f"WARNING: {msg}", file=sys.stderr)
    return r.stdout.strip()


def _get_files(args: argparse.Namespace) -> list[str]:
    if args.files:
        validated: list[str] = []
        for f in args.files:
            resolved = (REPO_ROOT / f).resolve()
            if not resolved.is_relative_to(REPO_ROOT):
                print(f"WARNING: skipping path outside repo root: {f}")
                continue
            validated.append(str(f))
        return validated

    if args.all_files:
        raw = [f for f in _git("ls-files", critical=True).splitlines() if f]
    else:
        # Changed + staged files vs HEAD
        changed = _git(
            "diff",
            "--name-only",
            "--diff-filter=ACMR",
            "HEAD",
            critical=True,
        )
        staged = _git(
            "diff",
            "--name-only",
            "--cached",
            "--diff-filter=ACMR",
            critical=True,
        )
        combined: set[str] = set()
        for line in (changed + "\n" + staged).splitlines():
            if line.strip():
                combined.add(line.strip())
        raw = sorted(combined)

    # Exclude entries that resolve to directories (e.g. symlinks
    # pointing at directories — ``git ls-files`` lists them but
    # file-oriented tools choke on them).
    return [f for f in raw if not (REPO_ROOT / f).is_dir()]


# ── Parity check ───────────────────────────────────────────────


def _check_parity(config: dict[str, Any]) -> bool:
    """Verify bidirectional parity between config and registry."""
    ok = True
    config_hooks: set[tuple[str, str]] = set()
    for repo in config.get("repos", []):
        key = _normalize_url(repo["repo"])
        reg = _REGISTRY.get(key)
        if reg is None:
            print(f"MISSING REPO: {repo['repo']}")
            for h in repo.get("hooks", []):
                print(f"  - {h['id']}")
            ok = False
            continue
        for h in repo.get("hooks", []):
            config_hooks.add((key, h["id"]))
            if h["id"] not in reg.get("hooks", {}):
                print(f"MISSING HOOK: {h['id']} (from {repo['repo']})")
                ok = False

    # Reverse parity: detect registry entries not in config
    for repo_url, reg in _REGISTRY.items():
        for hid, hinfo in reg.get("hooks", {}).items():
            if (repo_url, hid) not in config_hooks:
                print(
                    f"ORPHANED REGISTRY ENTRY: {hid} "
                    f"(in {repo_url}, not configured in .pre-commit-config.yaml)"
                )
                ok = False
            # Also validate that 'text' is not mixed with other types
            htypes = hinfo.get("types", [])
            if "text" in htypes and len(htypes) > 1:
                print(
                    f"INVALID HOOK TYPES in {repo_url} / {hid}: "
                    f"'text' cannot be combined with other types"
                )
                ok = False

    if ok:
        print(
            "PARITY CHECK PASSED: all hooks in "
            ".pre-commit-config.yaml have registered handlers "
            "and no orphaned registry entries exist."
        )
    else:
        print(
            "\nPARITY CHECK FAILED: parity mismatch between config and registry — see above."
        )
    return ok


# ── Entrypoint ─────────────────────────────────────────────────

_STATUS_ICON: dict[str, str] = {
    "pass": "\033[32m✓\033[0m",
    "fail": "\033[31m✗\033[0m",
    "skip": "\033[33m○\033[0m",
    "unsupported": "\033[35m?\033[0m",
    "unavailable": "\033[35m!\033[0m",
}


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    grp = parser.add_mutually_exclusive_group()
    grp.add_argument(
        "--all-files",
        action="store_true",
        help="check all tracked files",
    )
    grp.add_argument(
        "--files",
        nargs="+",
        metavar="FILE",
        help="check only these files",
    )
    parser.add_argument(
        "--check-parity",
        action="store_true",
        help="verify every configured hook has a registered handler",
    )
    args = parser.parse_args()

    config = _load_config()

    if args.check_parity:
        return 0 if _check_parity(config) else 1

    frozen = _extract_frozen_versions()
    files = _get_files(args)

    if not files:
        print("No files to check.")
        return 0

    results: list[tuple[str, str, str]] = []

    for repo in config.get("repos", []):
        repo_url = _normalize_url(repo["repo"])
        rev = repo.get("rev", "")
        version = frozen.get(rev)

        reg = _REGISTRY.get(repo_url)
        if reg is None:
            for h in repo.get("hooks", []):
                hid = h["id"]
                msg = f"unknown repo: {repo['repo']}"
                print(f"{_STATUS_ICON['unsupported']} {hid}  ({msg})")
                results.append(("unsupported", hid, msg))
            continue

        for h in repo.get("hooks", []):
            hid = h["id"]
            hinfo = reg.get("hooks", {}).get(hid)

            if hinfo is None:
                msg = f"unknown hook in {repo['repo']}"
                print(f"{_STATUS_ICON['unsupported']} {hid}  ({msg})")
                results.append(("unsupported", hid, msg))
                continue

            # Progress indicator (overwritten by result line)
            print(f"  Running {hid} …", end="\r", flush=True)
            status, detail = _run_hook(hid, hinfo, reg, h, files, version)

            icon = _STATUS_ICON.get(status, "?")
            suffix = f"  ({detail})" if detail and status == "skip" else ""
            print(f"{icon} {hid}{suffix}")

            if detail and status == "fail":
                for line in detail.splitlines()[:30]:
                    print(f"  {line}")
                total = len(detail.splitlines())
                if total > 30:
                    print(f"  … ({total} lines total)")

            if detail and status in ("unsupported", "unavailable"):
                print(f"  {detail}")

            results.append((status, hid, detail))

    # ── Summary ────────────────────────────────────────────────
    print()
    print("─" * 60)
    passed = sum(1 for s, *_ in results if s == "pass")
    failed = sum(1 for s, *_ in results if s == "fail")
    skipped = sum(1 for s, *_ in results if s == "skip")
    unsup = sum(1 for s, *_ in results if s in ("unsupported", "unavailable"))
    total = len(results)
    print(
        f"Results: {passed} passed, {failed} failed, "
        f"{skipped} skipped, {unsup} unsupported  ({total} hooks)"
    )

    if failed:
        print("\nFailed hooks:")
        for s, h, _ in results:
            if s == "fail":
                print(f"  - {h}")

    if unsup:
        print("\nUnsupported / unavailable hooks:")
        for s, h, d in results:
            if s in ("unsupported", "unavailable"):
                print(f"  - {h}: {d}")

    return 1 if (failed or unsup) else 0


if __name__ == "__main__":
    sys.exit(main())
