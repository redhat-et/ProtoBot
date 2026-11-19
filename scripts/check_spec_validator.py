#!/usr/bin/env python3
"""Build and run the V2 specification validator against the local checkout.

Used as the ``spec-validator`` local pre-commit hook (and via
``scripts/lint.py``). The validator is built from ``validator/`` with
module downloads disabled, so the check needs no network access and no
credentials; the Go module cache (or a vendor directory) must already
hold the dependencies. The working tree is compared against the default
base: the merge base of ``HEAD`` with the first of ``GITHUB_BASE_SHA``,
``origin/$GITHUB_BASE_REF``, ``upstream/main``, ``origin/main``, or
``main`` that resolves. When none resolves the tree is compared with
``HEAD`` itself, the base-relative checks (text immutability, deletion,
replacement) cannot fire, and a note says so on stderr.
Set ``PROTOBOT_VALIDATOR_BASE`` to compare against another revision; a
value starting with ``-`` is rejected.
Validator diagnostics are printed unchanged and fail the hook.

Limitations: the validator reads the working tree, not the index. A
staged deletion (``git rm --cached``) whose file stays on disk is not seen
as a deletion, and an untracked draft under ``spec/`` is validated and can
fail a commit that does not touch it.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

try:
    from go_dependency_errors import is_dependency_error as _is_dependency_error
except ImportError:  # imported as ``scripts.<module>``
    from scripts.go_dependency_errors import (
        is_dependency_error as _is_dependency_error,
    )

REPO_ROOT = Path(__file__).resolve().parent.parent
VALIDATOR_MODULE = Path("validator")
VALIDATOR_PACKAGE = "./cmd/validator"
BASE_ENV = "PROTOBOT_VALIDATOR_BASE"
DEFAULT_BASE_CANDIDATES = ("upstream/main", "origin/main", "main")
FALLBACK_NOTE = (
    "note: no default-branch ref resolved; comparing the working tree with "
    "HEAD, so base-relative checks are skipped"
)


def _git(repo_root: Path, *args: str) -> str | None:
    """Return stripped stdout of a git command, or ``None`` on failure."""
    try:
        result = subprocess.run(
            ["git", *args],
            capture_output=True,
            text=True,
            cwd=str(repo_root),
            check=False,
        )
    except FileNotFoundError:
        return None
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def resolve_base(
    repo_root: Path = REPO_ROOT,
    env: dict[str, str] | None = None,
) -> tuple[str, bool]:
    """Return ``(base_revision, base_is_head)`` for the validator run.

    ``PROTOBOT_VALIDATOR_BASE`` wins when set. Otherwise the merge base of
    ``HEAD`` and the first resolvable default-branch candidate is used, so
    a feature branch is compared against where it diverged. When no
    candidate resolves, the base is ``HEAD`` itself.
    """
    environ = os.environ if env is None else env
    head = _git(repo_root, "rev-parse", "--verify", "HEAD^{commit}")
    override = environ.get(BASE_ENV, "").strip()
    if override:
        resolved = _git(repo_root, "rev-parse", "--verify", f"{override}^{{commit}}")
        return override, resolved is not None and resolved == head
    candidates = []
    if environ.get("GITHUB_BASE_SHA"):
        candidates.append(environ["GITHUB_BASE_SHA"])
    if environ.get("GITHUB_BASE_REF"):
        candidates.append(f"origin/{environ['GITHUB_BASE_REF']}")
    candidates.extend(DEFAULT_BASE_CANDIDATES)
    for candidate in candidates:
        if candidate.startswith("-"):
            continue
        if _git(repo_root, "rev-parse", "--verify", f"{candidate}^{{commit}}") is None:
            continue
        merge_base = _git(repo_root, "merge-base", "HEAD", candidate)
        if merge_base:
            return merge_base, merge_base == head
    return "HEAD", True


def build_validator(
    output: Path,
    repo_root: Path = REPO_ROOT,
) -> tuple[bool, str]:
    """Build the validator binary offline. Returns ``(ok, message)``."""
    module = repo_root / VALIDATOR_MODULE
    cmd = ["go", "build", "-o", str(output), VALIDATOR_PACKAGE]
    env = os.environ.copy()
    env["GOWORK"] = "off"
    env["GOPROXY"] = "off"
    env["GOFLAGS"] = "-mod=vendor" if (module / "vendor").is_dir() else "-mod=readonly"
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            cwd=str(module),
            env=env,
            check=False,
        )
    except FileNotFoundError:
        return False, "go not found on PATH"
    if result.returncode == 0:
        return True, ""
    detail = (result.stdout + result.stderr).strip()
    if _is_dependency_error(detail):
        return False, (
            "validator build failed: module dependencies could not be "
            "resolved offline. Ensure GOMODCACHE is populated or vendor/ "
            f"is present.\n{detail}"
        )
    return False, f"validator build failed:\n{detail}"


def check(repo_root: Path = REPO_ROOT, env: dict[str, str] | None = None) -> int:
    """Build the validator and run it on *repo_root*. Returns an exit code."""
    environ = os.environ if env is None else env
    override = environ.get(BASE_ENV, "").strip()
    if override.startswith("-"):
        print(f"{BASE_ENV} must not start with '-': {override!r}", file=sys.stderr)
        return 1
    base, base_is_head = resolve_base(repo_root, env)
    if base == "HEAD" and base_is_head and not override:
        print(FALLBACK_NOTE, file=sys.stderr)
    with tempfile.TemporaryDirectory(prefix="protobot-validator-") as tmp:
        binary = Path(tmp) / "validator"
        ok, message = build_validator(binary, repo_root)
        if not ok:
            print(message, file=sys.stderr)
            return 1
        cmd = [str(binary), f"--target={repo_root}", f"--base={base}"]
        if base_is_head:
            cmd.append("--allow-self-comparison")
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            cwd=str(repo_root),
            check=False,
        )
    output = (result.stdout + result.stderr).strip()
    if result.returncode != 0:
        print(
            f"spec validator failed (base {base}, status {result.returncode}):",
            file=sys.stderr,
        )
        if output:
            print(output, file=sys.stderr)
        return 1
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args(argv)
    return check()


if __name__ == "__main__":
    sys.exit(main())
