#!/usr/bin/env python3
"""Build and run the V2 specification validator against the local checkout.

Used as the ``spec-validator`` local pre-commit hook (and via
``scripts/lint.py``). The validator is built from ``validator/`` with
module downloads disabled, so the check needs no network access and no
credentials; the Go module cache (or a vendor directory) must already
hold the dependencies. The working tree is compared against the default
base: the merge base of ``HEAD`` with the first of ``upstream/main``,
``origin/main``, or ``main`` that resolves, or ``HEAD`` when none does.
Set ``PROTOBOT_VALIDATOR_BASE`` to compare against another revision.
Validator diagnostics are printed unchanged and fail the hook.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
VALIDATOR_MODULE = Path("validator")
VALIDATOR_PACKAGE = "./cmd/validator"
BASE_ENV = "PROTOBOT_VALIDATOR_BASE"
DEFAULT_BASE_CANDIDATES = ("upstream/main", "origin/main", "main")

_DEPENDENCY_ERROR_INDICATORS = (
    "module lookup disabled",
    "cannot find module providing package",
    "no required module provides package",
    "missing go.sum entry",
    "dial tcp",
    "connection refused",
    "no such host",
    "i/o timeout",
    "tls handshake timeout",
    "server response:",
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
    for candidate in DEFAULT_BASE_CANDIDATES:
        if _git(repo_root, "rev-parse", "--verify", f"{candidate}^{{commit}}") is None:
            continue
        merge_base = _git(repo_root, "merge-base", "HEAD", candidate)
        if merge_base:
            return merge_base, merge_base == head
    return "HEAD", True


def _is_dependency_error(output: str) -> bool:
    lower = output.lower()
    if any(indicator in lower for indicator in _DEPENDENCY_ERROR_INDICATORS):
        return True
    return "go: downloading" in output and any(
        kw in lower for kw in ("error", "timeout", "failed")
    )


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
    base, base_is_head = resolve_base(repo_root, env)
    with tempfile.TemporaryDirectory(prefix="protobot-validator-") as tmp:
        binary = Path(tmp) / "validator"
        ok, message = build_validator(binary, repo_root)
        if not ok:
            print(message, file=sys.stderr)
            return 1
        cmd = [str(binary), "--target", str(repo_root), "--base", base]
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
        print(f"spec validator failed (base {base}):", file=sys.stderr)
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
