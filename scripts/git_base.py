#!/usr/bin/env python3
"""Shared Git comparison-base resolution for repository check scripts.

``scripts/check_adr_amendments.py``, ``scripts/check_spec_validator.py``,
and ``scripts/check_baseline_ratchet.py`` each grew their own ad-hoc
logic for picking the git revision a working tree is compared against.
Independently, each one missed something: a CI environment variable, a
shallow-clone edge case, or rejection of an option-like ref (``-h``,
``--upload-pack=...``) that git would otherwise interpret as a flag
instead of a revision. This module centralizes the primitives so new
and existing checks share one tested implementation instead of
re-deriving it.

Only Python standard library modules are used (``os``, ``re``,
``subprocess``, ``pathlib``), matching ``scripts/go_dependency_errors.py``.

Public surface:

``is_safe_git_ref``
    Reject a candidate ref/SHA that could be parsed as a git option
    (leading ``-``) or smuggle a path (``:``) instead of naming a
    revision.
``git_ref_exists`` / ``git_commit_sha``
    Check whether a ref resolves to a commit, or resolve it to a SHA.
``git_merge_base`` / ``get_merge_base``
    Compute ``git merge-base <ref> <head_ref>``.
``is_shallow_repository``
    Report whether the checkout is a shallow clone, so a caller can
    decide whether an unresolved merge base might be fixed by deepening
    the clone (fetching more history is a CI-environment concern left
    to each caller; this module only reports the condition).
``resolve_comparison_base``
    Resolve the comparison base for a check, trying in order: an
    explicit override, ``PRE_COMMIT_FROM_REF``, ``GITHUB_BASE_SHA``,
    ``GITHUB_BASE_REF``, CI event base fields (``PULL_REQUEST_BASE_SHA``
    for a ``pull_request`` event, ``MERGE_GROUP_BASE_SHA`` for a
    ``merge_group`` event, ``PUSH_BEFORE_SHA`` for a ``push`` event),
    and finally default-branch candidates. Returns
    ``(merge_base, is_head)`` where ``is_head`` is True only when the
    resolved base equals ``HEAD`` (a legitimate self-comparison, not a
    failure). Returns ``(None, False)`` when nothing resolves
    (deterministic fail-closed): callers decide how to treat a missing
    base rather than silently comparing a tree with itself.

An explicit override is trusted directly once it is confirmed to exist:
it is returned as given, not reduced to a merge base, so a caller that
deliberately named a revision gets exactly that revision back. Every
other candidate is reduced to its merge base with ``HEAD``, so a
diverged feature branch is compared against where it branched rather
than the base branch's current tip.
"""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

DEFAULT_BASE_REF_CANDIDATES: tuple[str, ...] = (
    "upstream/main",
    "origin/main",
    "main",
)

# GitHub Actions event name -> the environment variable a workflow step
# populates with that event's base SHA (``pull_request.base.sha``,
# ``merge_group.base_sha``, ``push.before``). Wiring a workflow to set
# these is left to the caller; this module only consumes them.
_EVENT_BASE_ENV_VARS: dict[str, str] = {
    "pull_request": "PULL_REQUEST_BASE_SHA",
    "merge_group": "MERGE_GROUP_BASE_SHA",
    "push": "PUSH_BEFORE_SHA",
}

# A ref/SHA safe to pass as a bare git revision argument: non-empty, no
# leading ``-`` (option injection, e.g. ``-h`` or ``--upload-pack=...``
# being parsed as a flag instead of a revision), and no ``:`` (path
# smuggling via ``ref:path`` where an attacker-controlled "ref" reads an
# unintended file).
_SAFE_REF = re.compile(r"^(?!-)[A-Za-z0-9._/@+~-]+$")


def is_safe_git_ref(ref: str) -> bool:
    """Return True when *ref* is safe to pass as a bare git revision argument."""
    return bool(ref) and _SAFE_REF.fullmatch(ref) is not None


def _run_git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[str] | None:
    """Run ``git`` in *repo_root*, or return None if git cannot be run at all."""
    try:
        return subprocess.run(
            ["git", *args],
            capture_output=True,
            text=True,
            cwd=str(repo_root),
            check=False,
        )
    except FileNotFoundError:
        return None


def git_commit_sha(repo_root: Path, ref: str) -> str | None:
    """Return the commit SHA *ref* resolves to in *repo_root*, or None."""
    if not is_safe_git_ref(ref):
        return None
    result = _run_git(
        repo_root, "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}"
    )
    if result is None or result.returncode != 0:
        return None
    sha = result.stdout.strip()
    return sha or None


def git_ref_exists(repo_root: Path, ref: str) -> bool:
    """Return True when *ref* names a commit in *repo_root*."""
    return git_commit_sha(repo_root, ref) is not None


def git_merge_base(repo_root: Path, ref: str, head_ref: str = "HEAD") -> str | None:
    """Return the merge-base commit of *ref* and *head_ref*, or None.

    Runs ``git merge-base <ref> <head_ref>``. For two commits git merge-base
    is symmetric; passing *ref* followed by *head_ref* matches the parameter
    order.
    """
    if not is_safe_git_ref(ref) or not is_safe_git_ref(head_ref):
        return None
    result = _run_git(repo_root, "merge-base", ref, head_ref)
    if result is None or result.returncode != 0:
        return None
    sha = result.stdout.strip()
    return sha or None


get_merge_base = git_merge_base


def is_shallow_repository(repo_root: Path) -> bool:
    """Return True when *repo_root* is a shallow git clone."""
    result = _run_git(repo_root, "rev-parse", "--is-shallow-repository")
    return (
        result is not None
        and result.returncode == 0
        and result.stdout.strip() == "true"
    )


def _event_base_candidate(environ: dict[str, str]) -> str | None:
    """Return the CI event base SHA selected by ``GITHUB_EVENT_NAME``, or None."""
    event = environ.get("GITHUB_EVENT_NAME", "").strip()
    env_var = _EVENT_BASE_ENV_VARS.get(event)
    if env_var is None:
        return None
    value = environ.get(env_var, "").strip()
    return value or None


def resolve_comparison_base(
    repo_root: Path,
    explicit_ref: str | None = None,
    env: dict[str, str] | None = None,
    default_candidates: tuple[str, ...] = DEFAULT_BASE_REF_CANDIDATES,
) -> tuple[str | None, bool]:
    """Resolve the comparison base ref and whether it equals HEAD.

    Priority order:

    1. *explicit_ref*, when provided. Returned as given once confirmed to
       exist (not reduced to a merge base); unresolvable or unsafe means
       fail closed (``(None, False)``) rather than falling through, since
       a caller-named override that cannot be honored should not be
       silently replaced by an automatically discovered base.
    2. ``PRE_COMMIT_FROM_REF``.
    3. ``GITHUB_BASE_SHA``.
    4. ``GITHUB_BASE_REF`` (probing ``origin/<ref>`` then ``<ref>``).
    5. The CI event base field selected by ``GITHUB_EVENT_NAME``
       (``PULL_REQUEST_BASE_SHA``, ``MERGE_GROUP_BASE_SHA``, or
       ``PUSH_BEFORE_SHA``).
    6. *default_candidates* (``upstream/main``, ``origin/main``, ``main``
       by default).

    Every candidate from tiers 2-6 is skipped when unsafe (see
    ``is_safe_git_ref``) or when it does not resolve to a commit, and is
    then reduced to its merge base with HEAD. Returns
    ``(merge_base, is_head)``; ``is_head`` is True only when the
    resolved base equals HEAD. Returns ``(None, False)`` when nothing
    resolves (deterministic fail-closed).
    """
    environ = os.environ if env is None else env

    if explicit_ref is not None:
        ref = explicit_ref.strip()
        if not ref or not is_safe_git_ref(ref) or not git_ref_exists(repo_root, ref):
            return None, False
        head = git_commit_sha(repo_root, "HEAD")
        sha = git_commit_sha(repo_root, ref)
        return ref, sha is not None and sha == head

    head = git_commit_sha(repo_root, "HEAD")

    candidates: list[str] = []
    from_ref = environ.get("PRE_COMMIT_FROM_REF", "").strip()
    if from_ref:
        candidates.append(from_ref)
    base_sha = environ.get("GITHUB_BASE_SHA", "").strip()
    if base_sha:
        candidates.append(base_sha)
    base_ref = environ.get("GITHUB_BASE_REF", "").strip()
    if base_ref:
        candidates.append(f"origin/{base_ref}")
        candidates.append(base_ref)
    event_ref = _event_base_candidate(environ)
    if event_ref:
        candidates.append(event_ref)
    candidates.extend(default_candidates)

    for candidate in candidates:
        if not is_safe_git_ref(candidate):
            continue
        if not git_ref_exists(repo_root, candidate):
            continue
        merge_base = git_merge_base(repo_root, candidate)
        if merge_base:
            return merge_base, merge_base == head
    return None, False
