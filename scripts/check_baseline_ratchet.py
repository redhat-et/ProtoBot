#!/usr/bin/env python3
"""Reject ``.skillsaw-baseline.json`` ceiling increases against a base branch.

A ``baseline_mode: "ceiling"`` entry is a one-way ratchet: its ``value`` may
stay the same or decrease, but it must not increase relative to the base
branch. skillsaw itself only compares the working tree against the committed
baseline, so bumping the ceiling in the same change would otherwise pass.

A ceiling entry may also not be dropped from ``.skillsaw-baseline.json``
while its file still exists in the repository. If a tracked file shrinks
below its ``.skillsaw.yaml`` warn threshold, skillsaw stops emitting a
finding for it, and regenerating the baseline then omits the ceiling
entirely — silently deleting the ratchet and allowing the file to grow back
up to the warn threshold unchecked. Dropping a ceiling is only accepted when
the file itself was removed from the repository.

Separately, for every ``context-budget`` ceiling tracked in the working-tree
baseline, its category (e.g. ``agents-md``, ``skill``) must have a corresponding
warn limit in ``.skillsaw.yaml`` under ``rules.context-budget.limits.<category>``
(as an integer or ``{warn, error}`` mapping) that stays ``<=`` the recorded
ceiling. A missing limit entry or a warn limit above the ceiling creates the same
blind spot described above as soon as the file grows back up to the warn threshold.

Used as the ``baseline-ratchet`` local pre-commit hook (and via
``scripts/lint.py``). Base content is loaded from the first existing git ref
in this order: ``--base-ref``, ``BASELINE_RATCHET_BASE_REF``,
``GITHUB_BASE_REF`` (as ``origin/<name>`` then ``<name>``), ``upstream/main``,
``origin/main``, ``main``. When no candidate exists, the check fetches the
file from GitHub (``GITHUB_REPOSITORY`` or the ``origin`` remote) so shallow
CI checkouts still enforce the ratchet. In CI environments, unresolved refs or
failed HTTP fetches cause the check to fail closed (exit 1). Offline local
runs skip with a warning so the rest of ``pre-commit run -a`` can finish.
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
    import yaml
except ImportError:
    sys.exit(
        "ERROR: PyYAML is required but not installed.\n"
        "       Install with:  pip install pyyaml"
    )

REPO_ROOT = Path(__file__).resolve().parent.parent
BASELINE_RELATIVE_PATH = ".skillsaw-baseline.json"
SKILLSAW_CONFIG_RELATIVE_PATH = ".skillsaw.yaml"
DEFAULT_BASE_REF_CANDIDATES = (
    "upstream/main",
    "origin/main",
    "main",
)
NETWORK_TIMEOUT_SECONDS = 10
MAX_RESPONSE_BYTES = 2 * 1024 * 1024
# Git refs used as ``git show <ref>:<path>``. Disallow ``:`` so a ref cannot
# smuggle a path, and reject option-like values that would be parsed as flags.
_SAFE_REF = re.compile(r"^(?!-)[A-Za-z0-9._/@+~-]+$")
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


def is_safe_ref(ref: str) -> bool:
    """Return True when *ref* is safe to pass to ``git show`` / ``rev-parse``."""
    return bool(ref) and _SAFE_REF.fullmatch(ref) is not None


def _git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo_root* and return the completed process."""
    return subprocess.run(
        ["git", *args],
        capture_output=True,
        text=True,
        cwd=str(repo_root),
        check=False,
    )


def git_commit_sha(repo_root: Path, ref: str) -> str | None:
    """Return the commit SHA for *ref*, or ``None`` if it cannot be resolved."""
    if not is_safe_ref(ref):
        return None
    result = _git(repo_root, "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}")
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def git_ref_exists(repo_root: Path, ref: str) -> bool:
    """Return True when *ref* names a commit in *repo_root*."""
    return git_commit_sha(repo_root, ref) is not None


def git_show_file(repo_root: Path, ref: str, path: str) -> str | None:
    """Return the contents of *path* at *ref*, or ``None`` if it is missing.

    *ref* must already have been validated with :func:`git_ref_exists`.
    """
    result = _git(repo_root, "show", f"{ref}:{path}")
    if result.returncode != 0:
        return None
    return result.stdout


def git_merge_base(repo_root: Path, ref: str, head_ref: str = "HEAD") -> str | None:
    """Return the merge base commit between *ref* and *head_ref*, or ``None``."""
    if not is_safe_ref(ref) or not is_safe_ref(head_ref):
        return None
    result = _git(repo_root, "merge-base", ref, head_ref)
    if result.returncode != 0:
        return None
    sha = result.stdout.strip()
    return sha if sha else None


def is_ci_environment() -> bool:
    """Return True when running in a CI or automation environment."""
    return any(
        os.environ.get(var, "").strip()
        for var in ("CI", "GITHUB_ACTIONS", "GITHUB_BASE_REF", "GITHUB_REPOSITORY")
    )


def is_shallow_repository(repo_root: Path) -> bool:
    """Return True when *repo_root* is a shallow git repository."""
    result = _git(repo_root, "rev-parse", "--is-shallow-repository")
    return result.returncode == 0 and result.stdout.strip() == "true"


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
    """Index ceiling entries as ``(file_path, rule_id) -> [values]``.

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
        index.setdefault((normalized_path, rule_id), []).append(number)
    return index


def compare_ceilings(
    head: dict[str, Any], base: dict[str, Any], repo_root: Path | None = None
) -> list[str]:
    """Return diagnostics for ceiling regressions between *head* and *base*.

    Two regressions are checked:

    * An existing ``(file_path, rule_id)`` ceiling whose head value exceeds
      its base value (the ratchet moving the wrong way). The highest head
      value must be ``<=`` the lowest base value for that key.
    * A ``(file_path, rule_id)`` ceiling present in *base* but absent from
      *head*. This is only rejected when *repo_root* is given and the file
      still exists on disk — dropping the ceiling for a file that was
      itself removed from the repository is allowed.

    New ``(file_path, rule_id)`` ceiling keys present only in *head* are
    allowed (a newly introduced skillsaw finding). When *repo_root* is
    ``None``, dropped-ceiling detection is skipped because file existence
    cannot be verified.
    """
    diagnostics: list[str] = []
    head_index = ceiling_values(head, "working tree")
    base_index = ceiling_values(base, "base branch")
    for key in sorted(head_index):
        file_path, _rule_id = key
        base_numbers = base_index.get(key)
        if not base_numbers:
            continue
        base_val = min(base_numbers)
        head_val = max(head_index[key])
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
            if not (repo_root / file_path).exists():
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


def context_budget_category(
    file_path: str, config: dict[str, Any] | None = None
) -> str | None:
    """Return the skillsaw context-budget category for *file_path*.

    These categorization rules match the specification and behavior of
    skillsaw v0.23.0 (https://github.com/stbenjam/skillsaw), as configured in
    ``.skillsaw.yaml`` and pinned in ``.pre-commit-config.yaml``.

    Skillsaw categorizes files into standard context-budget categories:
    * ``agents-md``: ``AGENTS.md`` files (root or subdirectories)
    * ``skill``: skill files (named ``SKILL.md`` or located under a ``skills`` directory)
    * ``instruction``: general agent instruction files (e.g. ``CLAUDE.md``,
      ``COPILOT.md``, ``INSTRUCTIONS.md``, ``.cursorrules``, ``.windsurfrules``,
      ``copilot-instructions.md``)
    * ``memory``: persistent memory files (e.g. ``MEMORY.md``)
    * ``extra``: files explicitly listed in ``.skillsaw.yaml`` ``content-paths``,
      matched either by exact relative path or via glob patterns

    Canonical category names passed directly as *file_path* pass through
    unchanged.

    Returns the category string, or ``None`` if *file_path* does not belong
    to a recognized category.
    """
    path = Path(file_path)
    name = path.name
    if name == "AGENTS.md":
        return "agents-md"
    if name == "SKILL.md" or "skills" in path.parts:
        return "skill"
    if name in (
        "CLAUDE.md",
        "COPILOT.md",
        "INSTRUCTIONS.md",
        ".cursorrules",
        ".windsurfrules",
        "copilot-instructions.md",
    ):
        return "instruction"
    if name == "MEMORY.md":
        return "memory"
    if config is not None:
        content_paths = config.get("content-paths")
        if isinstance(content_paths, list):
            for cp in content_paths:
                if isinstance(cp, str) and (cp == file_path or path.match(cp)):
                    return "extra"
    if file_path in ("agents-md", "skill", "instruction", "memory", "extra"):
        return file_path
    return None


def extract_warn_limit(entry: Any) -> int | float | None:
    """Extract a numeric warn limit from a skillsaw limit entry.

    The schema rules match skillsaw v0.23.0
    (https://github.com/stbenjam/skillsaw). A limit entry in ``.skillsaw.yaml``
    under ``rules.context-budget.limits.<category>`` may be:
    * An integer or float (shorthand for a warn-only limit), e.g. ``4000``
    * A mapping with a ``warn`` key, e.g. ``{"warn": 4000}`` or
      ``{"warn": 4000, "error": 5000}``
    * A mapping with a ``file`` key for whole-file limits, supporting either:
      - A bare numeric shorthand, e.g. ``{"file": 4000}``
      - A nested mapping with a ``warn`` key, e.g. ``{"file": {"warn": 4000}}``

    Returns the numeric warn limit, or ``None`` if no numeric warn limit is
    configured.
    """
    if isinstance(entry, bool):
        return None
    if isinstance(entry, (int, float)):
        return entry
    if isinstance(entry, dict):
        warn = entry.get("warn")
        if not isinstance(warn, bool) and isinstance(warn, (int, float)):
            return warn
        file_entry = entry.get("file")
        if not isinstance(file_entry, bool) and isinstance(file_entry, (int, float)):
            return file_entry
        if isinstance(file_entry, dict):
            file_warn = file_entry.get("warn")
            if not isinstance(file_warn, bool) and isinstance(file_warn, (int, float)):
                return file_warn
    return None


def load_skillsaw_config(repo_root: Path) -> dict[str, Any] | None:
    """Return the parsed ``.skillsaw.yaml`` config, or ``None`` if absent.

    If the config file does not exist, ``None`` is returned. Any read failure,
    parse error, or non-mapping document raises :class:`BaselineRatchetError`
    so invalid configuration fails closed rather than silently disabling
    warn-parity enforcement.
    """
    path = repo_root / SKILLSAW_CONFIG_RELATIVE_PATH
    if not path.is_file():
        return None
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as error:
        raise BaselineRatchetError(
            f"ERROR: cannot read {SKILLSAW_CONFIG_RELATIVE_PATH} ({type(error).__name__})"
        ) from None
    try:
        data = yaml.safe_load(raw)
    except yaml.YAMLError as error:
        raise BaselineRatchetError(
            f"ERROR: invalid YAML in {SKILLSAW_CONFIG_RELATIVE_PATH}: {sanitize_log_string(str(error))}"
        ) from error
    if not isinstance(data, dict):
        raise BaselineRatchetError(
            f"ERROR: {SKILLSAW_CONFIG_RELATIVE_PATH} must be a YAML mapping, not {type(data).__name__}"
        )
    return data


def compare_warn_limits(
    head_index: dict[tuple[str, str], list[int]], config: dict[str, Any] | None
) -> list[str]:
    """Return diagnostics for ``.skillsaw.yaml`` context-budget warn limits.

    For every ``context-budget`` ceiling key in *head_index*, derive its
    category (e.g. ``agents-md``, ``skill``) and look up the matching
    entry under ``rules.context-budget.limits.<category>`` in *config*
    (the parsed ``.skillsaw.yaml``). Limit entries may be an integer (shorthand
    for warn-only limit) or a mapping with a numeric ``warn`` limit. A warn
    limit greater than the recorded ceiling stops skillsaw from emitting a
    context-budget finding once the file grows back up to the warn limit,
    which drops the ceiling on the next baseline regeneration (see the
    module docstring).

    When *config* is ``None`` (the config file does not exist), checking is
    skipped only when *head_index* contains no ``context-budget`` ceilings.
    If *head_index* contains any ``context-budget`` ceiling entries, the
    absence of ``.skillsaw.yaml`` is an error (failing closed). When *config*
    is present, each ``context-budget`` ceiling must map to a recognized category
    with a configured numeric ``warn`` limit.
    """
    has_context_budget = any(rule_id == "context-budget" for _, rule_id in head_index)
    if config is None:
        if has_context_budget:
            return [
                "ERROR: .skillsaw.yaml is missing but context-budget ceilings are tracked."
            ]
        return []
    rules = config.get("rules")
    context_budget = rules.get("context-budget") if isinstance(rules, dict) else None
    limits = context_budget.get("limits") if isinstance(context_budget, dict) else None
    if not isinstance(limits, dict):
        limits = {}

    diagnostics: list[str] = []
    for file_path, rule_id in sorted(head_index):
        if rule_id != "context-budget":
            continue
        clean_path = sanitize_log_string(file_path)
        category = context_budget_category(file_path, config)
        if category is None:
            diagnostics.append(
                f"ERROR: cannot determine .skillsaw.yaml context-budget category "
                f"for baseline ceiling {clean_path}."
            )
            continue
        clean_category = sanitize_log_string(category)
        if category not in limits:
            diagnostics.append(
                "ERROR: .skillsaw.yaml is missing a "
                f"rules.context-budget.limits.{clean_category} entry for "
                f"baseline ceiling {clean_path}."
            )
            continue
        entry = limits[category]
        warn = extract_warn_limit(entry)
        if warn is None:
            diagnostics.append(
                "ERROR: .skillsaw.yaml rules.context-budget.limits."
                f"{clean_category} is missing a numeric 'warn' limit for "
                f"baseline ceiling {clean_path}."
            )
            continue
        ceiling = min(head_index[(file_path, "context-budget")])
        if warn > ceiling:
            diagnostics.append(
                "ERROR: .skillsaw.yaml context-budget warn limit for "
                f"{clean_path} ({warn}) exceeds baseline ceiling "
                f"({ceiling}). Lower the warn limit in .skillsaw.yaml to "
                "match or precede the ceiling."
            )
    return diagnostics


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
    an existing file was dropped, and every tracked ``.skillsaw.yaml``
    context-budget warn limit is at or below its baseline ceiling. Exit 1 on
    any of those regressions or on unreadable input.
    """
    try:
        head = load_working_tree_baseline(repo_root)
        if head is None:
            return 0, [f"No {BASELINE_RELATIVE_PATH} in the working tree."]
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
    try:
        head_index = ceiling_values(head, "working tree")
        diagnostics = compare_ceilings(head, base, repo_root=repo_root)
        diagnostics += compare_warn_limits(head_index, load_skillsaw_config(repo_root))
        n_ceilings = sum(len(values) for values in head_index.values())
    except BaselineRatchetError as error:
        return 1, [str(error)]

    if diagnostics:
        return 1, diagnostics
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
