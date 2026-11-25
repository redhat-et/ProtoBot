#!/usr/bin/env python3
"""Reject ``.skillsaw-baseline.json`` ceiling increases against a base branch.

A ``baseline_mode: "ceiling"`` entry is a one-way ratchet: its ``value`` may
stay the same or decrease, but it must not increase relative to the base
branch. skillsaw itself only compares the working tree against the committed
baseline, so bumping the ceiling in the same change would otherwise pass.

A ceiling entry may not be dropped from ``.skillsaw-baseline.json`` while its
file still exists in the repository. If a tracked file shrinks below its
``.skillsaw.yaml`` warn threshold, skillsaw stops emitting a finding for it,
and regenerating the baseline then omits the ceiling entirely. Dropping a
ceiling is accepted only when the file itself was removed from the repository.

For the tracked ``AGENTS.md`` context-budget ceiling, the configured
``rules.context-budget.limits.agents-md`` warn limit in ``.skillsaw.yaml`` must
stay at or below the recorded baseline ceiling. Otherwise skillsaw can stop
emitting the finding before the file grows past the ceiling, creating the same
silent-ceiling-loss path.

Used as the ``baseline-ratchet`` local pre-commit hook (and via
``scripts/lint.py``). Base content is loaded from the first existing git ref
in this order: ``--base-ref``, ``BASELINE_RATCHET_BASE_REF``,
``GITHUB_BASE_REF`` (as ``origin/<name>`` then ``<name>``), ``upstream/main``,
``origin/main``, ``main``. When no candidate exists, the check fetches the
file from GitHub (``GITHUB_REPOSITORY`` or the ``origin`` remote) so shallow
CI checkouts still enforce the ratchet. In CI environments, unresolved refs or
failed HTTP fetches cause the check to fail closed (exit 1). Offline local
runs skip with a warning so the rest of ``pre-commit run -a`` can finish.

Ref safety (rejecting option-like and path-smuggling candidates) and the
core git primitives (``git_commit_sha``, ``git_merge_base``,
``is_shallow_repository``) are aliased from the shared
``scripts/git_base.py`` helper, aligning with
``scripts/check_adr_amendments.py`` and
``scripts/check_spec_validator.py``. A local ``git_ref_exists`` wrapper
is kept for patch compatibility.
"""

from __future__ import annotations

import argparse
import http.client
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

try:
    import git_base
except ImportError:  # imported as ``scripts.<module>``
    from scripts import git_base

REPO_ROOT = Path(__file__).resolve().parent.parent
BASELINE_RELATIVE_PATH = ".skillsaw-baseline.json"
SKILLSAW_CONFIG_RELATIVE_PATH = ".skillsaw.yaml"
AGENTS_MD_RELATIVE_PATH = "AGENTS.md"
AGENTS_MD_LIMIT_KEY = "agents-md"
CONTEXT_BUDGET_RULE_ID = "context-budget"
DEFAULT_BASE_REF_CANDIDATES = git_base.DEFAULT_BASE_REF_CANDIDATES
NETWORK_TIMEOUT_SECONDS = 10
MAX_RESPONSE_BYTES = 2 * 1024 * 1024
_GITHUB_REPOSITORY = re.compile(r"^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$")
_ORIGIN_GITHUB = re.compile(r"(?:github\.com[:/])(?P<repo>[^/]+/[^/]+?)(?:\.git)?$")
_CONTROL_BYTES = re.compile(r"[\x00-\x1f\x7f]")


def sanitize_log_string(value: str) -> str:
    """Strip CR/LF, ASCII control bytes, and ASCII '::' from log strings."""
    sanitized = _CONTROL_BYTES.sub("", value)
    while "::" in sanitized:
        sanitized = sanitized.replace("::", "")
    return sanitized


class BaselineRatchetError(ValueError):
    """The working-tree or base baseline cannot be compared."""


class _UnexpectedRedirectError(ValueError):
    """A redirect was rejected while fetching the base baseline."""


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Reject redirects before urllib can request their target URLs."""

    def redirect_request(
        self,
        request: urllib.request.Request,
        response: object,
        code: int,
        message: str,
        headers: object,
        new_url: str,
    ) -> urllib.request.Request | None:
        """Stop urllib from creating a request for a redirected URL."""
        raise _UnexpectedRedirectError("redirect rejected while fetching baseline URL")


# Shared with scripts/check_adr_amendments.py and
# scripts/check_spec_validator.py via scripts/git_base.py, so the three
# checks reject option-injecting and path-smuggling refs the same way.
is_safe_ref = git_base.is_safe_git_ref
git_commit_sha = git_base.git_commit_sha
git_merge_base = git_base.git_merge_base
is_shallow_repository = git_base.is_shallow_repository


def git_ref_exists(repo_root: Path, ref: str) -> bool:
    """Return True when *ref* names a commit in *repo_root*.

    Calls the module-level ``git_commit_sha`` (not ``git_base`` directly)
    so a test that patches ``git_commit_sha`` on this module also changes
    what this function sees.
    """
    return git_commit_sha(repo_root, ref) is not None


def _git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo_root* and return the completed process."""
    return subprocess.run(
        ["git", *args],
        capture_output=True,
        text=True,
        cwd=str(repo_root),
        check=False,
    )


def git_show_file(repo_root: Path, ref: str, path: str) -> str | None:
    """Return the contents of *path* at *ref*, or ``None`` if it is missing.

    *ref* must already have been validated with :func:`git_ref_exists`.
    """
    result = _git(repo_root, "show", f"{ref}:{path}")
    if result.returncode != 0:
        return None
    return result.stdout


def is_ci_environment() -> bool:
    """Return True when running in a CI or automation environment."""
    return any(
        os.environ.get(var, "").strip()
        for var in ("CI", "GITHUB_ACTIONS", "GITHUB_BASE_REF", "GITHUB_REPOSITORY")
    )


def is_shallow_push_checkout(repo_root: Path) -> bool:
    """Return True when running in CI on a shallow push checkout."""
    if not is_ci_environment():
        return False
    if not is_shallow_repository(repo_root):
        return False
    return os.environ.get("GITHUB_EVENT_NAME", "").strip() == "push"


def _candidate_base_refs(explicit: str | None = None) -> list[str]:
    """Return de-duplicated base-ref candidates in resolution order."""
    candidates: list[str] = []

    def add(ref: str | None) -> None:
        if ref and ref not in candidates:
            candidates.append(ref)

    add(explicit)
    add(os.environ.get("BASELINE_RATCHET_BASE_REF", "").strip() or None)
    github_base = os.environ.get("GITHUB_BASE_REF", "").strip()
    if github_base:
        add(f"origin/{github_base}")
        add(github_base)
    for ref in DEFAULT_BASE_REF_CANDIDATES:
        add(ref)
    return candidates


def resolve_base_ref(repo_root: Path, explicit: str | None = None) -> str | None:
    """Return the candidate ref to use as the base branch.

    If an explicit ref is given and exists, return it. Otherwise, walk candidate
    refs in priority order and return the first one whose
    ``.skillsaw-baseline.json`` exists. If none has the file, return the first
    candidate that names an existing commit (or ``None`` if none exist).
    """
    if explicit is not None:
        return explicit if git_ref_exists(repo_root, explicit) else None

    fallback: str | None = None
    for ref in _candidate_base_refs():
        if not git_ref_exists(repo_root, ref):
            continue
        if fallback is None:
            fallback = ref
        if git_show_file(repo_root, ref, BASELINE_RELATIVE_PATH) is not None:
            return ref
    return fallback


def github_repository(repo_root: Path) -> str | None:
    """Return ``owner/repo`` from ``GITHUB_REPOSITORY`` or the origin URL."""
    env_repo = os.environ.get("GITHUB_REPOSITORY", "").strip()
    if env_repo and _GITHUB_REPOSITORY.fullmatch(env_repo):
        return env_repo
    result = _git(repo_root, "remote", "get-url", "origin")
    if result.returncode != 0:
        return None
    match = _ORIGIN_GITHUB.search(result.stdout.strip())
    if match is None:
        return None
    repo = match.group("repo").removesuffix(".git")
    if not _GITHUB_REPOSITORY.fullmatch(repo):
        return None
    return repo


def http_base_branch() -> str:
    """Return the branch name used for the GitHub raw-file fallback."""
    github_base = os.environ.get("GITHUB_BASE_REF", "").strip()
    if github_base and is_safe_ref(github_base):
        return github_base
    return "main"


def raw_baseline_url(repository: str, branch: str) -> str:
    """Return the GitHub raw URL for the baseline file."""
    return (
        f"https://raw.githubusercontent.com/{repository}/"
        f"{branch}/{BASELINE_RELATIVE_PATH}"
    )


def fetch_baseline_http(repository: str, branch: str) -> str | None:
    """Fetch the baseline file from GitHub raw content.

    Returns ``None`` on network, HTTP, redirect, size, or encoding errors so
    the caller can skip or fail closed depending on the environment.
    """
    url = raw_baseline_url(repository, branch)
    request = urllib.request.Request(url, method="GET")
    try:
        opener = urllib.request.build_opener(_NoRedirectHandler())
        with opener.open(request, timeout=NETWORK_TIMEOUT_SECONDS) as response:
            content = response.read(MAX_RESPONSE_BYTES + 1)
    except (
        urllib.error.URLError,
        TimeoutError,
        OSError,
        http.client.HTTPException,
        _UnexpectedRedirectError,
    ):
        return None
    if len(content) > MAX_RESPONSE_BYTES:
        return None
    try:
        return content.decode("utf-8")
    except UnicodeError:
        return None


def parse_baseline(raw: str, source: str) -> dict[str, Any]:
    """Parse a skillsaw baseline document from *raw* JSON text."""
    clean_source = sanitize_log_string(source)
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as error:
        raise BaselineRatchetError(
            f"ERROR: invalid JSON in {clean_source}: {sanitize_log_string(error.msg)}"
        ) from error
    if not isinstance(data, dict):
        raise BaselineRatchetError(
            f"ERROR: {clean_source} must be a JSON object, not {type(data).__name__}"
        )
    violations = data.get("violations", [])
    if not isinstance(violations, list):
        raise BaselineRatchetError(
            f"ERROR: {clean_source} field 'violations' must be a list"
        )
    return data


def _ceiling_number(value: object, file_path: str, source: str) -> int:
    """Return *value* as an int, rejecting bools and non-integers."""
    if isinstance(value, bool) or not isinstance(value, int):
        clean_path = sanitize_log_string(file_path)
        clean_source = sanitize_log_string(source)
        raise BaselineRatchetError(
            f"ERROR: {clean_source} ceiling for {clean_path} has a non-integer value"
        )
    return value


def ceiling_values(
    data: dict[str, Any], source: str
) -> dict[tuple[str, str], list[int]]:
    """Index ceiling entries as ``(file_path, rule_id) -> [value]``.

    Each ``(file_path, rule_id)`` pair may appear at most once. A duplicate is
    rejected as ambiguous instead of being resolved by a tie-break rule, so
    every comparison sees one ceiling value per key.

    ``file_path`` must be a repo-relative path without parent directory (``..``)
    segments. It is normalized with ``os.path.normpath`` and converted to a
    POSIX-style string before it is used as part of the index key, so
    alternate representations of the same path (``./AGENTS.md``,
    ``docs//vision.md``, Windows backslashes) match the canonical form used
    elsewhere. Absolute paths and paths containing ``..`` segments are rejected.
    """
    index: dict[tuple[str, str], list[int]] = {}
    clean_source = sanitize_log_string(source)
    for entry in data.get("violations", []):
        if not isinstance(entry, dict):
            raise BaselineRatchetError(
                f"ERROR: {clean_source} violations must be JSON objects"
            )
        if entry.get("baseline_mode") != "ceiling":
            continue
        file_path = entry.get("file_path")
        rule_id = entry.get("rule_id")
        if not isinstance(file_path, str) or not file_path.strip():
            raise BaselineRatchetError(
                f"ERROR: {clean_source} ceiling entry is missing file_path"
            )
        clean_path = sanitize_log_string(file_path)
        if not isinstance(rule_id, str) or not rule_id:
            raise BaselineRatchetError(
                f"ERROR: {clean_source} ceiling for {clean_path} is missing rule_id"
            )
        if "value" not in entry:
            raise BaselineRatchetError(
                f"ERROR: {clean_source} ceiling for {clean_path} is missing a value"
            )
        number = _ceiling_number(entry["value"], file_path, source)
        raw_path = file_path.strip().replace("\\", "/")
        if (
            raw_path.startswith("/")
            or re.match(r"^[a-zA-Z]:", raw_path) is not None
            or ".." in raw_path.split("/")
        ):
            raise BaselineRatchetError(
                f"ERROR: {clean_source} ceiling for {clean_path} has an invalid file_path"
            )
        normalized_path = Path(os.path.normpath(raw_path)).as_posix()
        if normalized_path == "." or normalized_path.startswith(("/", "..")):
            raise BaselineRatchetError(
                f"ERROR: {clean_source} ceiling for {clean_path} has an invalid file_path"
            )
        key = (normalized_path, rule_id)
        if key in index:
            clean_rule = sanitize_log_string(rule_id)
            raise BaselineRatchetError(
                f"ERROR: {clean_source} has more than one ceiling for "
                f"{clean_path} ({clean_rule}); each (file_path, rule_id) pair "
                "must appear at most once"
            )
        index[key] = [number]
    return index


def compare_ceilings(
    head: dict[str, Any],
    base: dict[str, Any],
    repo_root: Path | None = None,
    head_index: dict[tuple[str, str], list[int]] | None = None,
) -> list[str]:
    """Return diagnostics for ceiling regressions between *head* and *base*.

    An existing key's head value must be ``<=`` its base value. New
    ``(file_path, rule_id)`` keys present only in *head* are allowed.

    *head_index* may be supplied by a caller that already indexed *head* with
    :func:`ceiling_values`; otherwise it is built here. Duplicate keys are
    rejected by :func:`ceiling_values`, so each key has exactly one value and
    the comparison needs no tie-break rule.

    When *repo_root* is given, a base ceiling missing from *head* is rejected
    if its file still exists. If the file was deleted, dropping the ceiling is
    allowed. When *repo_root* is ``None``, dropped-ceiling detection is skipped
    because file existence cannot be verified; :func:`check_repo` always
    supplies the repository root.
    """
    diagnostics: list[str] = []
    if head_index is None:
        head_index = ceiling_values(head, "working tree")
    base_index = ceiling_values(base, "base branch")
    for key in sorted(head_index):
        file_path, _rule_id = key
        base_numbers = base_index.get(key)
        if not base_numbers:
            continue
        base_val = base_numbers[0]
        head_val = head_index[key][0]
        if head_val > base_val:
            clean_path = sanitize_log_string(file_path)
            diagnostics.append(
                "ERROR: .skillsaw-baseline.json ceiling for "
                f"{clean_path} increased from {base_val} to {head_val}. "
                "Baseline ceilings may only ratchet downwards or remain "
                "unchanged."
            )
    if repo_root is not None:
        for key in sorted(base_index):
            if key in head_index:
                continue
            file_path, rule_id = key
            if not os.path.lexists(repo_root / file_path):
                continue
            clean_path = sanitize_log_string(file_path)
            clean_rule = sanitize_log_string(rule_id)
            diagnostics.append(
                f"ERROR: .skillsaw-baseline.json ceiling for {clean_path} "
                f"({clean_rule}) was removed while the file still exists. "
                "Baseline ceilings may only ratchet downwards or remain "
                "unchanged, not be deleted."
            )
    return diagnostics


def _agents_md_limit_entry(config: dict[str, Any]) -> tuple[bool, object]:
    """Return ``(present, value)`` for ``limits.agents-md`` in *config*.

    ``present`` distinguishes an absent key from a key whose value is not a
    valid limit, so callers can report the actual problem.
    """
    rules = config.get("rules")
    if not isinstance(rules, dict):
        return False, None
    context_budget = rules.get(CONTEXT_BUDGET_RULE_ID)
    if not isinstance(context_budget, dict):
        return False, None
    limits = context_budget.get("limits")
    if not isinstance(limits, dict):
        return False, None
    if AGENTS_MD_LIMIT_KEY not in limits:
        return False, None
    return True, limits[AGENTS_MD_LIMIT_KEY]


def agents_md_warn_limit(config: dict[str, Any]) -> int | None:
    """Return ``limits.agents-md``'s numeric warn limit from *config*.

    skillsaw accepts either an integer shorthand or a mapping with a ``warn``
    key for a context-budget category. Only the ``agents-md`` category is
    checked here; the mapping between other file paths and skillsaw categories
    is intentionally left to skillsaw itself.
    """
    _present, entry = _agents_md_limit_entry(config)
    if isinstance(entry, bool):
        return None
    if isinstance(entry, int):
        return entry
    if isinstance(entry, dict):
        warn = entry.get("warn")
        if isinstance(warn, int) and not isinstance(warn, bool):
            return warn
    return None


def load_skillsaw_config(repo_root: Path) -> dict[str, Any] | None:
    """Return the parsed ``.skillsaw.yaml`` config, or ``None`` if absent."""
    path = repo_root / SKILLSAW_CONFIG_RELATIVE_PATH
    if not path.is_file():
        return None
    try:
        import yaml
    except ImportError:
        raise BaselineRatchetError(
            "ERROR: PyYAML is required to read .skillsaw.yaml (pip install pyyaml)"
        ) from None
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as error:
        raise BaselineRatchetError(
            f"ERROR: cannot read {SKILLSAW_CONFIG_RELATIVE_PATH} "
            f"({type(error).__name__})"
        ) from None
    try:
        data = yaml.safe_load(raw)
    except yaml.YAMLError as error:
        clean_error = sanitize_log_string(str(error))
        raise BaselineRatchetError(
            f"ERROR: invalid YAML in {SKILLSAW_CONFIG_RELATIVE_PATH}: {clean_error}"
        ) from error
    if not isinstance(data, dict):
        raise BaselineRatchetError(
            f"ERROR: {SKILLSAW_CONFIG_RELATIVE_PATH} must be a YAML mapping, "
            f"not {type(data).__name__}"
        )
    return data


def compare_warn_limits(
    head_index: dict[tuple[str, str], list[int]],
    config: dict[str, Any] | None,
) -> list[str]:
    """Return AGENTS.md warn-threshold parity diagnostics.

    When the working-tree baseline tracks the ``AGENTS.md`` context-budget
    ceiling, ``.skillsaw.yaml`` must exist and its ``agents-md`` warn limit
    must be numeric and ``<=`` the recorded ceiling. A missing config fails
    closed: otherwise deleting ``.skillsaw.yaml`` would silently disable the
    parity invariant.
    """
    agents_key = (AGENTS_MD_RELATIVE_PATH, CONTEXT_BUDGET_RULE_ID)
    if agents_key not in head_index:
        return []
    clean_path = sanitize_log_string(AGENTS_MD_RELATIVE_PATH)
    ceiling = head_index[agents_key][0]
    if config is None:
        return [
            (
                f"ERROR: {SKILLSAW_CONFIG_RELATIVE_PATH} is missing while the "
                f"working-tree baseline tracks the {clean_path} context-budget "
                "ceiling; the agents-md warn limit cannot be verified against "
                f"baseline ceiling ({ceiling})."
            )
        ]
    present, entry = _agents_md_limit_entry(config)
    warn = agents_md_warn_limit(config)
    if warn is None:
        if present:
            clean_entry = sanitize_log_string(repr(entry))
            return [
                (
                    "ERROR: .skillsaw.yaml context-budget warn limit for "
                    f"{clean_path} in "
                    f"rules.context-budget.limits.{AGENTS_MD_LIMIT_KEY} is not "
                    f"a numeric warn limit (found {clean_entry}). Configure an "
                    f"integer warn limit at or below baseline ceiling ({ceiling})."
                )
            ]
        return [
            (
                "ERROR: .skillsaw.yaml context-budget warn limit for "
                f"{clean_path} is missing from "
                f"rules.context-budget.limits.{AGENTS_MD_LIMIT_KEY}. Configure "
                f"a numeric warn limit at or below baseline ceiling ({ceiling})."
            )
        ]
    if warn > ceiling:
        return [
            (
                "ERROR: .skillsaw.yaml context-budget warn limit for "
                f"{clean_path} ({warn}) exceeds baseline ceiling ({ceiling}). "
                "Lower the warn limit in .skillsaw.yaml to match or precede "
                "the ceiling."
            )
        ]
    return []


def load_working_tree_baseline(repo_root: Path) -> dict[str, Any] | None:
    """Return the working-tree baseline, or ``None`` if the file is absent."""
    path = repo_root / BASELINE_RELATIVE_PATH
    if not path.is_file():
        return None
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as error:
        raise BaselineRatchetError(
            f"ERROR: cannot read {BASELINE_RELATIVE_PATH} ({type(error).__name__})"
        ) from None
    return parse_baseline(raw, BASELINE_RELATIVE_PATH)


def load_base_baseline(
    repo_root: Path, explicit_ref: str | None = None
) -> tuple[str, dict[str, Any]] | None:
    """Return ``(source_label, baseline)`` for the resolved base, if any.

    An explicit ``--base-ref`` that is missing or unsafe is an error, not a
    skip. Auto-detection falling through git refs tries GitHub raw next.
    """
    if explicit_ref is not None:
        if not is_safe_ref(explicit_ref):
            raise BaselineRatchetError(
                f"ERROR: invalid --base-ref {sanitize_log_string(explicit_ref)!r}"
            )
        if not git_ref_exists(repo_root, explicit_ref):
            raise BaselineRatchetError(
                f"ERROR: --base-ref {sanitize_log_string(explicit_ref)} not found"
            )
        mb = git_merge_base(repo_root, explicit_ref, "HEAD")
        target_ref = mb if mb is not None else explicit_ref
        raw = git_show_file(repo_root, target_ref, BASELINE_RELATIVE_PATH)
        source = f"{target_ref}:{BASELINE_RELATIVE_PATH}"
        if raw is None:
            return source, {"violations": []}
        return source, parse_baseline(raw, source)

    resolved = resolve_base_ref(repo_root)
    if resolved is not None:
        mb = git_merge_base(repo_root, resolved, "HEAD")
        target_ref = mb if mb is not None else resolved
        raw = git_show_file(repo_root, target_ref, BASELINE_RELATIVE_PATH)
        source = f"{target_ref}:{BASELINE_RELATIVE_PATH}"
        if raw is None:
            return source, {"violations": []}
        return source, parse_baseline(raw, source)

    repository = github_repository(repo_root)
    if repository is None:
        return None
    branch = http_base_branch()
    raw = fetch_baseline_http(repository, branch)
    if raw is None:
        return None
    source = raw_baseline_url(repository, branch)
    return source, parse_baseline(raw, source)


def check_repo(repo_root: Path, base_ref: str | None = None) -> tuple[int, list[str]]:
    """Compare the working-tree baseline against the resolved base.

    Returns ``(exit_code, messages)``. Exit 0 with a skip warning when no
    base source can be resolved outside CI. In CI, exit 1 if no base
    source can be resolved. Exit 0 with a validation message when every
    existing ceiling is less than or equal to its base value, no ceiling for
    an existing file was dropped, and the tracked ``AGENTS.md``
    context-budget warn limit is at or below its baseline ceiling. Exit 1 on
    any of those regressions or on unreadable input.
    """
    try:
        head = load_working_tree_baseline(repo_root)
        if base_ref is None and is_shallow_push_checkout(repo_root):
            return 0, [
                "WARNING: baseline ratchet skipped: shallow push checkout lacks base history."
            ]
        loaded = load_base_baseline(repo_root, explicit_ref=base_ref)
    except BaselineRatchetError as error:
        return 1, [str(error)]

    if loaded is None:
        tried = ", ".join(_candidate_base_refs())
        if is_ci_environment():
            error = (
                "ERROR: baseline ratchet failed: could not resolve a "
                f"base-branch {BASELINE_RELATIVE_PATH} in CI (tried: {tried})"
            )
            return 1, [error]
        warning = (
            "WARNING: baseline ratchet skipped: could not resolve a "
            f"base-branch {BASELINE_RELATIVE_PATH} (tried: {tried})"
        )
        return 0, [warning]

    source, base = loaded
    head_missing = head is None
    if head is None:
        head = {"violations": []}
    try:
        head_index = ceiling_values(head, "working tree")
        diagnostics = compare_ceilings(
            head, base, repo_root=repo_root, head_index=head_index
        )
        diagnostics += compare_warn_limits(head_index, load_skillsaw_config(repo_root))
        n_ceilings = sum(len(values) for values in head_index.values())
    except BaselineRatchetError as error:
        return 1, [str(error)]

    if diagnostics:
        return 1, diagnostics
    if head_missing:
        return 0, [
            (
                f"No {BASELINE_RELATIVE_PATH} in the working tree; all base "
                "ceilings belong to deleted files."
            )
        ]
    clean_source = sanitize_log_string(source)
    return 0, [
        f"Validated {n_ceilings} ceiling(s) against {clean_source}; none increased."
    ]


def main(argv: list[str] | None = None) -> int:
    """Compare the working-tree baseline against the resolved base ref."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--base-ref",
        default=None,
        help="Git ref of the base branch (default: auto-detect)",
    )
    args = parser.parse_args(argv)
    exit_code, messages = check_repo(REPO_ROOT, base_ref=args.base_ref)
    stream = sys.stderr if exit_code else sys.stdout
    for message in messages:
        print(sanitize_log_string(message), file=stream)
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
