#!/usr/bin/env python3
"""Fail if accepted or superseded ADR historical text is rewritten in place.

Used as the ``adr-amendments`` local pre-commit hook (and via
``scripts/lint.py``). Accepted and superseded architecture decision records
under ``docs/decisions/`` are immutable historical records: they are never
deleted or renamed, and git diffs may insert dated amendment blocks, but
must not delete or modify lines from the baseline copy. The sole exception
is the authorized accepted-to-superseded status-line transition (see
``historical_mutations`` and ``_authorized_transition``), which requires a
dated ``Superseded by ADR-NNNN`` status line plus an appended dated
amendment block.

The baseline is resolved from the merge-base with the default branch
(or ``PRE_COMMIT_FROM_REF`` when pre-commit sets it), falling back to
``HEAD`` for local uncommitted checks when the file is not on the baseline.
"""

from __future__ import annotations

import argparse
import difflib
import os
import re
import stat
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

_DECISIONS_DIR = Path("docs") / "decisions"
_MONTH_NAMES = (
    "January|February|March|April|May|June|July|August|September|"
    "October|November|December"
)
# Prefix match: used for header detection (is_accepted, is_superseded)
# and for scanning baseline files. Any line starting with the prefix is
# treated as accepted/superseded (and therefore protected), failing closed
# even if trailing text or an unrecognized status-line format is present.
_ACCEPTED_STATUS = re.compile(
    r"^> Status:[ \t]*\*\*Accepted\*\*",
    re.MULTILINE,
)
_SUPERSEDED_STATUS = re.compile(
    r"^> Status:[ \t]*\*\*Superseded by ADR-\d+\*\*",
    re.MULTILINE,
)
# Strict (date required): the only status line an authorized transition may
# write. Trailing text after the date is never allowed.
_SUPERSEDED_STATUS_DATED = re.compile(
    rf"^> Status:[ \t]*\*\*Superseded by ADR-\d+\*\* — (?:{_MONTH_NAMES}) \d{{4}}$",
    re.MULTILINE,
)
_AMBIGUOUS_STATUS = re.compile(
    r"^[ \t]*>[ \t]*status:[ \t]*\*{0,2}(?:Accepted|Superseded\b)",
    re.IGNORECASE | re.MULTILINE,
)
_AMENDMENT_MARKER = re.compile(
    rf"^\*\*\[Amended (?:{_MONTH_NAMES}) \d{{4}}\]\*\*$",
    re.MULTILINE,
)
_FENCE_DELIMITER = re.compile(r"^(`{3,}|~{3,})")
_HEADER_LINE_LIMIT = 30
_MAX_REPORTED_LINES = 10


def is_adr_markdown(rel_path: Path) -> bool:
    """Return True when *rel_path* is a Markdown file under ``docs/decisions/``."""
    parts = rel_path.parts
    return (
        len(parts) >= 3
        and Path(*parts[:2]) == _DECISIONS_DIR
        and rel_path.suffix == ".md"
    )


def is_accepted(text: str) -> bool:
    """Return True when *text* declares ADR status Accepted in its header."""
    header = "\n".join(text.splitlines()[:_HEADER_LINE_LIMIT])
    return _ACCEPTED_STATUS.search(header) is not None


def is_superseded(text: str) -> bool:
    """Return True when *text* declares ADR status Superseded in its header."""
    header = "\n".join(text.splitlines()[:_HEADER_LINE_LIMIT])
    return _SUPERSEDED_STATUS.search(header) is not None


def is_protected(text: str) -> bool:
    """Return True when *text* is an Accepted or Superseded ADR.

    Protection from in-place rewriting, deletion, and renaming continues
    after an ADR is superseded; only the dated status-line transition
    performed by an authorized supersession is permitted (see
    ``historical_mutations``). Non-canonical or ambiguous status headers
    are also treated as protected (fail closed) and reported as errors.
    """
    if is_accepted(text) or is_superseded(text):
        return True
    header_lines = text.splitlines()[:_HEADER_LINE_LIMIT]
    fenced = _fenced_code_mask(header_lines)
    for idx, line in enumerate(header_lines):
        if not fenced[idx] and _AMBIGUOUS_STATUS.search(line):
            print(
                f"ERROR: ambiguous status header: {line.strip()}",
                file=sys.stderr,
            )
            return True
    return False


def _fenced_code_mask(lines: list[str]) -> list[bool]:
    """Return a per-line mask that is True while *lines* is inside a fence.

    A fence delimiter line itself is marked True: text may not be hidden in
    the delimiter line, and a marker immediately following the delimiter is
    still inside the fenced block.
    """
    mask: list[bool] = []
    in_fence = False
    for line in lines:
        if _FENCE_DELIMITER.match(line):
            mask.append(True)
            in_fence = not in_fence
        else:
            mask.append(in_fence)
    return mask


def _accepted_status_index(lines: list[str]) -> int | None:
    """Return the index of the sole line matching Accepted status in the header, or None."""
    header_lines = lines[:_HEADER_LINE_LIMIT]
    fenced = _fenced_code_mask(header_lines)
    matches = [
        idx
        for idx, line in enumerate(header_lines)
        if not fenced[idx] and _ACCEPTED_STATUS.match(line)
    ]
    return matches[0] if len(matches) == 1 else None


def _dated_superseded_status_index(lines: list[str]) -> int | None:
    """Return the index of the sole dated Superseded status line in the header, or None."""
    header_lines = lines[:_HEADER_LINE_LIMIT]
    fenced = _fenced_code_mask(header_lines)
    matches = [
        idx
        for idx, line in enumerate(header_lines)
        if not fenced[idx] and _SUPERSEDED_STATUS_DATED.fullmatch(line)
    ]
    return matches[0] if len(matches) == 1 else None


def _has_appended_amendment_block(
    baseline_lines: list[str],
    new_lines: list[str],
    opcodes: list[tuple[str, int, int, int, int]],
) -> bool:
    """Return True when a dated amendment marker is inserted after the last
    *baseline_lines* line and outside any fenced code block in *new_lines*.
    """
    fenced = _fenced_code_mask(new_lines)
    tail_start = len(baseline_lines)
    for tag, i1, _i2, j1, j2 in opcodes:
        if tag != "insert" or i1 != tail_start:
            continue
        for idx in range(j1, j2):
            if _AMENDMENT_MARKER.fullmatch(new_lines[idx]) and not fenced[idx]:
                return True
    return False


def _authorized_transition(
    old_lines: list[str],
    new_lines: list[str],
) -> tuple[int, int] | None:
    """Return ``(old_index, new_index)`` for an authorized status transition.

    An accepted-to-superseded transition is authorized when:

    1. *old_lines* has exactly one line matching the Accepted status.
    2. *new_lines* has exactly one line matching the dated Superseded status.
    3. Substituting the new status line for the old one in *old_lines*
       (a "synthetic baseline") and diffing it against *new_lines* produces
       only insertions. Any other historical line rewritten or deleted
       invalidates the transition, however the real diff happens to group
       opcodes (e.g. an amendment block diffed adjacent to the status line).
    4. One of those insertions is a dated amendment marker appended after
       the synthetic baseline's last line and outside a fenced code block.

    This is the single implementation of the supersession authorization
    rule; both the per-line mutation check and the extra-status-line check
    in ``historical_mutations`` consult it rather than re-deriving it.
    """
    old_status_idx = _accepted_status_index(old_lines)
    if old_status_idx is None:
        return None
    new_status_idx = _dated_superseded_status_index(new_lines)
    if new_status_idx is None:
        return None

    synthetic_old = list(old_lines)
    synthetic_old[old_status_idx] = new_lines[new_status_idx]

    opcodes = difflib.SequenceMatcher(
        a=synthetic_old,
        b=new_lines,
        autojunk=False,
    ).get_opcodes()
    if any(tag in {"replace", "delete"} for tag, *_rest in opcodes):
        return None
    if not _has_appended_amendment_block(synthetic_old, new_lines, opcodes):
        return None
    return old_status_idx, new_status_idx


def _status_line_count(lines: list[str]) -> int:
    """Return the number of lines matching the Accepted or Superseded status outside fences."""
    fenced = _fenced_code_mask(lines)
    return sum(
        1
        for idx, line in enumerate(lines)
        if not fenced[idx]
        and (_ACCEPTED_STATUS.match(line) or _SUPERSEDED_STATUS.match(line))
    )


def historical_mutations(
    old_lines: list[str],
    new_lines: list[str],
) -> list[tuple[int | None, int | None, str]]:
    """Return ``(baseline line number, HEAD line number, text)`` mutations.

    Insertions are allowed so dated amendment blocks can be added. Any
    delete or replace opcode means accepted or superseded decision text was
    rewritten or replaced in place, except for the single authorized
    accepted-to-superseded status transition (see ``_authorized_transition``).
    An inserted line that adds an extra status line beyond the one
    authorized transition is also reported, unless the baseline already had
    the same number of status lines (an unrelated, pre-existing condition).

    Either line number may be ``None``: a deleted line has no HEAD line, and
    an unauthorized extra status line insertion has no baseline line.
    """
    opcodes = difflib.SequenceMatcher(
        a=old_lines,
        b=new_lines,
        autojunk=False,
    ).get_opcodes()
    authorized = _authorized_transition(old_lines, new_lines)
    check_extra_status_lines = _status_line_count(old_lines) != _status_line_count(
        new_lines
    )

    mutations: list[tuple[int | None, int | None, str]] = []
    for tag, i1, i2, j1, j2 in opcodes:
        if tag in {"replace", "delete"}:
            for k in range(i2 - i1):
                old_idx = i1 + k
                if authorized is not None and old_idx == authorized[0]:
                    continue
                head_idx = j1 + k
                head_line = head_idx + 1 if head_idx < j2 else None
                mutations.append((old_idx + 1, head_line, old_lines[old_idx]))
        elif tag == "insert" and check_extra_status_lines:
            fenced = _fenced_code_mask(new_lines)
            for idx in range(j1, j2):
                if authorized is not None and idx == authorized[1]:
                    continue
                if fenced[idx]:
                    continue
                line = new_lines[idx]
                if _ACCEPTED_STATUS.match(line) or _SUPERSEDED_STATUS.match(line):
                    mutations.append((None, idx + 1, line))
    return mutations


def _git(
    repo_root: Path,
    *args: str,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo_root* and return the completed process."""
    run_env = dict(os.environ)
    if env:
        run_env.update(env)
    try:
        return subprocess.run(
            ["git", *args],
            capture_output=True,
            text=True,
            encoding="utf-8",
            cwd=str(repo_root),
            check=False,
            env=run_env,
        )
    except UnicodeDecodeError as exc:
        return subprocess.CompletedProcess(
            args=["git", *args],
            returncode=1,
            stdout="",
            stderr=f"git decode error: {exc}",
        )


def _relative_to_repo(path: Path, repo_root: Path) -> Path | None:
    """Return *path* relative to *repo_root*, or None if it is outside."""
    try:
        norm_root = Path(os.path.normpath(repo_root))
        if path.is_absolute():
            norm_path = Path(os.path.normpath(path))
        else:
            norm_path = Path(os.path.normpath(norm_root / path))
        if norm_path.is_relative_to(norm_root):
            return norm_path.relative_to(norm_root)
        resolved_root = repo_root.resolve()
        if norm_path.is_relative_to(resolved_root):
            return norm_path.relative_to(resolved_root)
        return None
    except (ValueError, OSError):
        return None


def _fetch_target(repo_root: Path, target: str) -> bool:
    """Attempt to fetch *target* (SHA or ref name) from origin.

    Returns True if a named remote ref or commit object was successfully
    fetched from origin, False otherwise.
    """
    env = {"GIT_TERMINAL_PROMPT": "0"}
    if re.fullmatch(r"[0-9a-fA-F]{7,64}", target):
        res = _git(repo_root, "fetch", "--depth=1", "origin", target, env=env)
        return res.returncode == 0
    clean_ref = target
    for prefix in ("refs/remotes/origin/", "refs/heads/", "origin/"):
        if clean_ref.startswith(prefix):
            clean_ref = clean_ref[len(prefix) :]
            break
    res = _git(
        repo_root,
        "fetch",
        "--depth=1",
        "origin",
        f"+refs/heads/{clean_ref}:refs/remotes/origin/{clean_ref}",
        env=env,
    )
    return res.returncode == 0


def resolve_baseline_ref(
    repo_root: Path,
    explicit_ref: str | None = None,
) -> str | None:
    """Resolve the git ref or commit SHA to baseline historical text against.

    Priority:
    1. *explicit_ref* if provided.
    2. ``PRE_COMMIT_FROM_REF`` environment variable if set.
    3. ``GITHUB_BASE_SHA`` environment variable if set.
    4. ``GITHUB_BASE_REF`` environment variable if set (probing ``origin/<ref>``,
       ``<ref>``, and ``refs/remotes/origin/<ref>``).
    5. Merge-base with the default branch (probing candidate refs:
       ``origin/main``, ``main``, ``origin/master``, ``master``,
       ``refs/remotes/origin/HEAD``).

    When a ref or commit SHA is not present locally, an attempt is made
    to fetch it from ``origin`` with ``--depth=1``.

    Returns ``None`` when no baseline ref can be resolved (fail closed).
    """

    def _resolve_commit(target: str) -> str | None:
        rev = _git(repo_root, "rev-parse", "--verify", f"{target}^{{commit}}")
        if rev.returncode != 0 or not rev.stdout.strip():
            return None
        target_sha = rev.stdout.strip()
        mb = _git(repo_root, "merge-base", "HEAD", target)
        if mb.returncode == 0 and mb.stdout.strip():
            return mb.stdout.strip()
        is_shallow = (
            _git(repo_root, "rev-parse", "--is-shallow-repository").stdout.strip()
            == "true"
        )
        if is_shallow:
            env = {"GIT_TERMINAL_PROMPT": "0"}
            depth = 50
            clean_ref = target
            for prefix in ("refs/remotes/origin/", "refs/heads/", "origin/"):
                if clean_ref.startswith(prefix):
                    clean_ref = clean_ref[len(prefix) :]
                    break
            is_sha = bool(re.fullmatch(r"[0-9a-fA-F]{7,64}", clean_ref))
            max_attempts = 10
            for _ in range(max_attempts):
                fetched_anything = False
                res_sha = _git(
                    repo_root,
                    "fetch",
                    f"--deepen={depth}",
                    "origin",
                    target_sha,
                    env=env,
                )
                if res_sha.returncode == 0:
                    fetched_anything = True
                if not is_sha:
                    res_ref = _git(
                        repo_root,
                        "fetch",
                        f"--deepen={depth}",
                        "origin",
                        f"+refs/heads/{clean_ref}:refs/remotes/origin/{clean_ref}",
                        env=env,
                    )
                    if res_ref.returncode == 0:
                        fetched_anything = True
                res_origin = _git(
                    repo_root,
                    "fetch",
                    f"--deepen={depth}",
                    "origin",
                    env=env,
                )
                if res_origin.returncode == 0:
                    fetched_anything = True

                mb = _git(repo_root, "merge-base", "HEAD", target)
                if mb.returncode == 0 and mb.stdout.strip():
                    return mb.stdout.strip()
                if not fetched_anything:
                    break
                if (
                    _git(
                        repo_root, "rev-parse", "--is-shallow-repository"
                    ).stdout.strip()
                    != "true"
                ):
                    break
                depth = min(depth * 2, 500)
        return None

    if explicit_ref:
        resolved = _resolve_commit(explicit_ref)
        if not resolved and _fetch_target(repo_root, explicit_ref):
            clean_ref = explicit_ref
            for prefix in ("refs/remotes/origin/", "refs/heads/", "origin/"):
                if clean_ref.startswith(prefix):
                    clean_ref = clean_ref[len(prefix) :]
                    break
            for candidate in [
                explicit_ref,
                f"origin/{clean_ref}",
                f"refs/remotes/origin/{clean_ref}",
            ]:
                resolved = _resolve_commit(candidate)
                if resolved:
                    break
        return resolved

    from_ref = os.environ.get("PRE_COMMIT_FROM_REF")
    if from_ref:
        resolved = _resolve_commit(from_ref)
        if not resolved and _fetch_target(repo_root, from_ref):
            clean_ref = from_ref
            for prefix in ("refs/remotes/origin/", "refs/heads/", "origin/"):
                if clean_ref.startswith(prefix):
                    clean_ref = clean_ref[len(prefix) :]
                    break
            for candidate in [
                from_ref,
                f"origin/{clean_ref}",
                f"refs/remotes/origin/{clean_ref}",
            ]:
                resolved = _resolve_commit(candidate)
                if resolved:
                    break
        if resolved:
            return resolved

    base_sha = os.environ.get("GITHUB_BASE_SHA")
    if base_sha:
        resolved = _resolve_commit(base_sha)
        if not resolved and _fetch_target(repo_root, base_sha):
            resolved = _resolve_commit(base_sha)
        if resolved:
            return resolved
        return None

    base_ref = os.environ.get("GITHUB_BASE_REF")
    if base_ref:
        clean_base = base_ref
        for prefix in ("refs/remotes/origin/", "refs/heads/", "origin/"):
            if clean_base.startswith(prefix):
                clean_base = clean_base[len(prefix) :]
                break
        for candidate in [
            f"origin/{clean_base}",
            clean_base,
            f"refs/remotes/origin/{clean_base}",
        ]:
            resolved = _resolve_commit(candidate)
            if resolved:
                return resolved
        if _fetch_target(repo_root, base_ref):
            for candidate in [
                f"origin/{clean_base}",
                f"refs/remotes/origin/{clean_base}",
            ]:
                resolved = _resolve_commit(candidate)
                if resolved:
                    return resolved
        return None

    candidates = [
        "origin/main",
        "main",
        "origin/master",
        "master",
    ]
    for target in candidates:
        resolved = _resolve_commit(target)
        if resolved:
            return resolved

    for default_branch in ["main", "master"]:
        if _fetch_target(repo_root, default_branch):
            for target in [
                f"origin/{default_branch}",
                f"refs/remotes/origin/{default_branch}",
            ]:
                resolved = _resolve_commit(target)
                if resolved:
                    return resolved

    resolved = _resolve_commit("refs/remotes/origin/HEAD")
    if resolved:
        return resolved

    return None


def _git_show_blob(
    repo_root: Path,
    ref_and_path: str,
) -> tuple[str | None, str | None]:
    """Return ``(text, error)`` for git blob ``ref:path``."""
    try:
        exists = _git(repo_root, "cat-file", "-e", ref_and_path)
    except FileNotFoundError:
        return None, "git not found on PATH"
    except UnicodeDecodeError as exc:
        return None, f"git decode error: {exc}"
    if exists.returncode != 0:
        stderr = exists.stderr.lower()
        if "does not exist in" in stderr or "but not in" in stderr:
            return None, None
        detail = exists.stderr.strip() or "git cat-file failed"
        return None, detail
    try:
        shown = _git(repo_root, "show", ref_and_path)
    except FileNotFoundError:
        return None, "git not found on PATH"
    except UnicodeDecodeError as exc:
        return None, f"git decode error: {exc}"
    if shown.returncode != 0:
        return None, shown.stderr.strip() or f"git show {ref_and_path} failed"
    return shown.stdout, None


def _head_file_text(
    repo_root: Path,
    rel_posix: str,
) -> tuple[str | None, str | None]:
    """Return ``(text, error)`` for ``HEAD:rel_posix``."""
    return _git_show_blob(repo_root, f"HEAD:{rel_posix}")


def get_historical_text(
    repo_root: Path,
    rel_posix: str,
    baseline_ref: str,
) -> tuple[str | None, str | None]:
    """Return ``(text, error)`` for *rel_posix* from *baseline_ref* or fallback ``HEAD``."""
    text, err = _git_show_blob(repo_root, f"{baseline_ref}:{rel_posix}")
    if err is not None or text is not None:
        return text, err

    if baseline_ref != "HEAD":
        return _head_file_text(repo_root, rel_posix)

    return None, None


def enumerate_adrs(repo_root: Path, baseline_ref: str) -> set[str]:
    """Enumerate all ADR paths to check from baseline, working tree, and diff."""
    paths: set[str] = set()

    ls_proc = _git(
        repo_root,
        "ls-tree",
        "-r",
        "--name-only",
        baseline_ref,
        "--",
        str(_DECISIONS_DIR),
    )
    if ls_proc.returncode == 0:
        for line in ls_proc.stdout.splitlines():
            line = line.strip()
            if line and is_adr_markdown(Path(line)):
                paths.add(line)

    dec_dir = repo_root / _DECISIONS_DIR
    if dec_dir.is_dir():
        for p in dec_dir.glob("*.md"):
            rel = _relative_to_repo(p, repo_root)
            if rel is not None and is_adr_markdown(rel):
                paths.add(rel.as_posix())

    diff_proc = _git(
        repo_root,
        "diff",
        "--name-only",
        "--diff-filter=ACDMR",
        baseline_ref,
        "--",
        str(_DECISIONS_DIR),
    )
    if diff_proc.returncode == 0:
        for line in diff_proc.stdout.splitlines():
            line = line.strip()
            if line and is_adr_markdown(Path(line)):
                paths.add(line)

    return paths


def get_renames(repo_root: Path, baseline_ref: str) -> dict[str, str]:
    """Return a mapping of ``{old_rel_posix: new_rel_posix}`` for detected renames."""
    renames: dict[str, str] = {}
    proc = _git(
        repo_root,
        "diff",
        "-M",
        "--name-status",
        "--diff-filter=R",
        baseline_ref,
        "--",
        str(_DECISIONS_DIR),
    )
    if proc.returncode == 0:
        for line in proc.stdout.splitlines():
            parts = line.strip().split("\t")
            if len(parts) >= 3 and parts[0].startswith("R"):
                old_path, new_path = parts[1], parts[2]
                renames[old_path] = new_path
    return renames


def _display_line(text: str, limit: int = 80) -> str:
    """Return *text* truncated for diagnostic output."""
    if len(text) <= limit:
        return text
    return text[: limit - 3] + "..."


def _report_mutations(
    rel_posix: str,
    mutations: list[tuple[int | None, int | None, str]],
) -> None:
    """Print a diagnostic for rewritten, deleted, or unauthorized historical text."""
    print(
        f"{rel_posix}: accepted or superseded decision text was rewritten "
        "or replaced in place"
    )
    for baseline_line, head_line, line in mutations[:_MAX_REPORTED_LINES]:
        if baseline_line is not None and head_line is not None:
            print(
                f"  baseline line {baseline_line} / HEAD line {head_line}: "
                f"{_display_line(line)}"
            )
        elif baseline_line is not None:
            print(
                f"  baseline line {baseline_line} (deleted, no corresponding "
                f"HEAD line): {_display_line(line)}"
            )
        else:
            print(
                f"  HEAD line {head_line} (no corresponding baseline line): "
                f"{_display_line(line)}"
            )
    remaining = len(mutations) - _MAX_REPORTED_LINES
    if remaining > 0:
        print(f"  … ({remaining} more line(s))")


def check(
    files: list[str] | None = None,
    repo_root: Path = REPO_ROOT,
    baseline_ref: str | None = None,
) -> int:
    """Return 0 when every given or enumerated accepted ADR preserves historical lines."""
    failed = False
    resolved_baseline = resolve_baseline_ref(repo_root, baseline_ref)
    if resolved_baseline is None:
        print(
            "ERROR: cannot resolve default-branch baseline ref to check against",
            file=sys.stderr,
        )
        return 1

    renames = get_renames(repo_root, resolved_baseline)
    handled_renames: set[str] = set()

    if files:
        raw_paths = list(files)
    else:
        raw_paths = sorted(enumerate_adrs(repo_root, resolved_baseline))

    for raw in raw_paths:
        path = Path(raw)
        if not path.is_absolute():
            path = repo_root / path
        rel = _relative_to_repo(path, repo_root)
        if rel is None:
            print(
                f"ERROR: path is outside the repository: {raw}",
                file=sys.stderr,
            )
            failed = True
            continue
        if not is_adr_markdown(rel):
            continue
        rel_posix = rel.as_posix()
        if rel_posix in handled_renames:
            continue

        hist_text, git_error = get_historical_text(
            repo_root,
            rel_posix,
            resolved_baseline,
        )
        if git_error is not None:
            print(f"ERROR: {rel_posix}: {git_error}", file=sys.stderr)
            failed = True
            continue
        if hist_text is None:
            continue
        if not is_protected(hist_text):
            continue

        try:
            st = os.lstat(path)
        except FileNotFoundError:
            st = None
        except OSError as exc:
            print(
                f"ERROR: {rel_posix}: cannot inspect path ({type(exc).__name__})",
                file=sys.stderr,
            )
            failed = True
            continue

        if st is None:
            new_path_posix = renames.get(rel_posix)
            if new_path_posix:
                print(
                    f"{rel_posix}: accepted or superseded architecture decision "
                    f"record was deleted (renamed to {new_path_posix})"
                )
                failed = True
                handled_renames.add(new_path_posix)
                new_path = repo_root / new_path_posix
                try:
                    nst = os.lstat(new_path)
                except OSError:
                    nst = None
                if nst and stat.S_ISREG(nst.st_mode):
                    try:
                        new_text = new_path.read_text(encoding="utf-8")
                    except (OSError, UnicodeDecodeError) as exc:
                        print(
                            f"ERROR: {new_path_posix}: cannot read working tree ({type(exc).__name__})",
                            file=sys.stderr,
                        )
                    else:
                        mutations = historical_mutations(
                            hist_text.splitlines(),
                            new_text.splitlines(),
                        )
                        if mutations:
                            _report_mutations(new_path_posix, mutations)
            else:
                print(
                    f"{rel_posix}: accepted or superseded architecture decision "
                    "record was deleted"
                )
                failed = True
            continue

        if not stat.S_ISREG(st.st_mode):
            print(
                f"ERROR: {rel_posix}: not a regular file",
                file=sys.stderr,
            )
            failed = True
            continue

        try:
            new_text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as exc:
            print(
                f"ERROR: {rel_posix}: cannot read working tree ({type(exc).__name__})",
                file=sys.stderr,
            )
            failed = True
            continue

        mutations = historical_mutations(
            hist_text.splitlines(),
            new_text.splitlines(),
        )
        if mutations:
            _report_mutations(rel_posix, mutations)
            failed = True

    return 1 if failed else 0


def main(argv: list[str] | None = None) -> int:
    """Parse filenames and check accepted ADR historical text."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--baseline",
        "--from-ref",
        dest="from_ref",
        default=None,
        help=(
            "Baseline git ref or commit SHA (default: merge-base with default branch, "
            "or PRE_COMMIT_FROM_REF / GITHUB_BASE_SHA / GITHUB_BASE_REF env vars)"
        ),
    )
    parser.add_argument(
        "files",
        nargs="*",
        help="ADR Markdown files (paths relative to the repository root)",
    )
    args = parser.parse_args(argv)
    return check(args.files if args.files else None, baseline_ref=args.from_ref)


if __name__ == "__main__":
    sys.exit(main())
