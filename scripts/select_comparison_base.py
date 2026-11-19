#!/usr/bin/env python3
"""Select the comparison base for the V2 validator CI job.

GitHub Actions supplies event fields that identify the approved base for a
change. This script maps them to the two inputs that
``scripts/check_spec_validator.py`` consumes:

``merge_base``
    A revision whose merge base with ``HEAD`` is the comparison base. Used for
    pull requests, merge groups, and any run that has no default-branch push
    base.
``exact_base``
    The exact comparison revision. Used for a push to the default branch, where
    the previous tip is compared directly, so a history rewrite cannot hide
    approved text behind an earlier merge base.

A pull request or merge group run uses its base branch SHA. A push to the
default branch uses the exact previous tip, and fails when git cannot provide
that commit. Every other run uses ``origin/<default branch>`` so the check
compares against the approved default branch rather than branch-local commits.
A missing required base is an error, never a silent self-comparison.

Commit SHA inputs are validated against the object-ID format before they reach
git, and error text is escaped before it is emitted as a GitHub Actions
annotation.

Inputs come from the environment:

``DEFAULT_BRANCH``
    Default branch name from the event. Falls back to ``main``.
``PULL_REQUEST_BASE_SHA``, ``MERGE_GROUP_BASE_SHA``, ``PUSH_BEFORE_SHA``
    Event base fields. Unset or empty when the event does not supply them.
``GITHUB_REF``
    Checked-out ref, used to tell a default-branch push from another push.
``GITHUB_OUTPUT``
    When set, the step outputs are appended to that file. Otherwise they are
    written to standard output.

The checkout must be the working directory. Exit status is 0 on success and 1
when a required base is unavailable.
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

ZERO_SHA = "0" * 40
DEFAULT_BRANCH_FALLBACK = "main"
OUTPUT_KEYS = ("merge_base", "exact_base")
# Git object IDs on GitHub are SHA-1 today; accept SHA-256 lengths too.
SHA_PATTERN = re.compile(r"[0-9a-fA-F]{40}|[0-9a-fA-F]{64}")
# Conservative branch-name check before a name becomes a git revision.
BRANCH_PATTERN = re.compile(r"[A-Za-z0-9][A-Za-z0-9._/-]*")


@dataclass(frozen=True)
class Selection:
    """The comparison bases the check script should use."""

    merge_base: str = ""
    exact_base: str = ""

    def as_output(self) -> str:
        """Return the value in ``$GITHUB_OUTPUT`` format."""
        return "".join(f"{key}={getattr(self, key)}\n" for key in OUTPUT_KEYS)


class SelectionError(Exception):
    """A required comparison base is unavailable."""


def _git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=str(repo),
        capture_output=True,
        text=True,
        check=False,
    )


def _resolves(repo: Path, revision: str) -> bool:
    return _git(repo, "cat-file", "-e", f"{revision}^{{commit}}").returncode == 0


def _require_sha(label: str, value: str) -> str:
    """Return *value* when it is a commit object ID, else raise."""
    if not SHA_PATTERN.fullmatch(value):
        raise SelectionError(f"{label} must be a commit SHA: {value!r}")
    return value


def _require_branch(value: str) -> str:
    """Return *value* when it is a safe branch name, else raise."""
    if not BRANCH_PATTERN.fullmatch(value) or ".." in value:
        raise SelectionError(f"DEFAULT_BRANCH is not a valid branch name: {value!r}")
    return value


def escape_command_text(text: str) -> str:
    """Escape annotation text per the GitHub Actions workflow-command rules."""
    return text.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")


def _exact_previous_tip(repo: Path, before: str) -> Selection:
    """Return the previous default-branch tip, fetching it when possible."""
    before = _require_sha("PUSH_BEFORE_SHA", before)
    if not _resolves(repo, before):
        _git(repo, "fetch", "--no-tags", "--depth=1", "origin", before)
    if not _resolves(repo, before):
        raise SelectionError(
            f"previous default-branch commit {before} is unavailable; "
            "approved text cannot be verified"
        )
    return Selection(exact_base=before)


def select(environ: dict[str, str], repo: Path) -> Selection:
    """Map event fields to a comparison-base selection.

    Raises ``SelectionError`` when a base that the event requires cannot be
    resolved, so the caller fails instead of comparing a tree with itself.
    """
    default_branch = _require_branch(
        environ.get("DEFAULT_BRANCH", "").strip() or DEFAULT_BRANCH_FALLBACK
    )

    for key in ("PULL_REQUEST_BASE_SHA", "MERGE_GROUP_BASE_SHA"):
        base = environ.get(key, "").strip()
        if not base:
            continue
        _require_sha(key, base)
        if not _resolves(repo, base):
            raise SelectionError(f"{key} {base} is not present in this checkout")
        return Selection(merge_base=base)

    ref = environ.get("GITHUB_REF", "").strip()
    before = environ.get("PUSH_BEFORE_SHA", "").strip()
    if ref == f"refs/heads/{default_branch}" and before and before != ZERO_SHA:
        return _exact_previous_tip(repo, before)

    default_base = f"origin/{default_branch}"
    if not _resolves(repo, default_base):
        raise SelectionError(
            f"default-branch ref {default_base} is unavailable; "
            "the comparison base cannot be resolved"
        )
    return Selection(merge_base=default_base)


def main(argv: Sequence[str] | None = None) -> int:
    """Write the selected bases and return an exit status."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args(argv)
    try:
        selection = select(dict(os.environ), Path.cwd())
    except SelectionError as error:
        print(f"::error::{escape_command_text(str(error))}", file=sys.stderr)
        return 1
    output = selection.as_output()
    destination = os.environ.get("GITHUB_OUTPUT", "").strip()
    if destination:
        with open(destination, "a", encoding="utf-8") as handle:
            handle.write(output)
    else:
        sys.stdout.write(output)
    return 0


if __name__ == "__main__":
    sys.exit(main())
