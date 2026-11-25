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

The check fails closed: a baseline with an ambiguous (non-canonical)
status header fails the run, a path that traverses a symbolic link is
rejected, and a failing git enumeration command is an error instead of
a silent skip.

The baseline is resolved from the merge-base with the default branch
(or ``PRE_COMMIT_FROM_REF`` when pre-commit sets it), falling back to
``HEAD`` for local uncommitted checks when the file is not on the baseline.
An explicit ref starting with ``-`` or containing ``:`` (smuggled
path or option injection like ``-h`` or ``--upload-pack=...``),
or an unsafe ``PRE_COMMIT_FROM_REF``, ``GITHUB_BASE_SHA``, or
``GITHUB_BASE_REF`` (validated via ``is_safe_git_ref`` from the shared
``scripts/git_base.py`` helper), is rejected before use.
Unsafe environment variables are skipped in favor of subsequent fallback
tiers, while an unsafe explicit ref fails closed immediately.
"""

from __future__ import annotations

import argparse
import difflib
import os
import re
import stat
import subprocess
import sys
from collections import Counter
from pathlib import Path

try:
    from git_base import is_safe_git_ref
except ImportError:  # imported as ``scripts.<module>``
    from scripts.git_base import is_safe_git_ref

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
# Any ``> Status:`` field, regardless of the value it declares. Used
# for the extra-status-line allowance: a supersession must not add a
# second status field, even one that declares a draft.
_STATUS_FIELD = re.compile(
    r"^[ \t]*>[ \t]*status:[ \t]*",
    re.IGNORECASE | re.MULTILINE,
)
# Ambiguous (fail-closed) match: any line that starts with an
# optional blockquote marker and ``status:`` and states accepted or
# superseded with any markup. The lookarounds treat ``_`` as markup,
# not as a word character, so ``__Accepted__`` matches; a longer word
# such as ``unaccepted`` does not. Used for whole-file protection; a
# status-like line that is not canonical makes the run fail.
_AMBIGUOUS_STATUS = re.compile(
    r"^[ \t]*>[ \t]*status:.*(?<![A-Za-z0-9])(?:accepted|superseded)(?![A-Za-z0-9])",
    re.IGNORECASE | re.MULTILINE,
)
# A dated amendment marker at the start of a line. Unlike
# _AMENDMENT_MARKER, an issue annotation inside the brackets is
# allowed and the marker may share its line with prose, because
# committed records use forms such as ``**[Amended November 2026,
# #547]** `ears-manager check` accepts ...``.
_DATED_AMENDMENT_LINE = re.compile(
    rf"^\*\*\[Amended (?:{_MONTH_NAMES}) \d{{4}}(?:, #[0-9]+)?\]\*\*",
)
_AMENDMENT_MARKER = re.compile(
    rf"^\*\*\[Amended (?:{_MONTH_NAMES}) \d{{4}}\]\*\*$",
    re.MULTILINE,
)
# CommonMark fence opening: three or more backticks or tildes after up
# to three leading spaces.
_FENCE_OPEN = re.compile(r"^ {0,3}(`{3,}|~{3,})")
_HEADER_LINE_LIMIT = 30
_MAX_REPORTED_LINES = 10

# A reported mutation: baseline line number, HEAD line range start and
# end (1-based; equal for a single line), and the baseline line text.
_Mutation = tuple[int | None, int | None, int | None, str]


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


def _is_status_field(line: str) -> bool:
    """Return True when *line* states an ADR status field, whatever the value."""
    return _STATUS_FIELD.match(line) is not None


def _is_status_line(line: str) -> bool:
    """Return True when *line* states an ADR status in any emphasis form."""
    return _AMBIGUOUS_STATUS.match(line) is not None


def _is_canonical_status(line: str) -> bool:
    """Return True when *line* uses the canonical Accepted or Superseded format."""
    return bool(_ACCEPTED_STATUS.match(line) or _SUPERSEDED_STATUS.match(line))


def is_protected(text: str) -> bool:
    """Return True when *text* is an Accepted or Superseded ADR.

    Protection from in-place rewriting, deletion, and renaming continues
    after an ADR is superseded; only the dated status-line transition
    performed by an authorized supersession is permitted (see
    ``historical_mutations``). The canonical header predicates
    ``is_accepted`` and ``is_superseded`` are the first check, so they
    stay the one source of truth for header status. Header protection
    is fence-unaware: a status-like line in the header protects the
    file even inside a fenced code block, so a baseline cannot drop the
    immutability guarantee by fencing its status line. A canonical
    Accepted or Superseded status line anywhere in the file also
    protects it, regardless of fences (fail-closed over-protection: a
    fenced status example in the body protects the file). Below the
    header, any other status-like line outside a fenced code block also
    protects the file. ``check`` reports status-like lines that are not
    canonical as errors.
    """
    if is_accepted(text) or is_superseded(text):
        return True
    lines = text.splitlines()
    if any(_is_status_line(line) for line in lines[:_HEADER_LINE_LIMIT]):
        return True
    if _ACCEPTED_STATUS.search(text) or _SUPERSEDED_STATUS.search(text):
        return True
    fenced = _fenced_code_mask(lines)
    return any(
        not fenced[idx] and _is_status_line(line) for idx, line in enumerate(lines)
    )


def _ambiguous_status_lines(lines: list[str]) -> list[str]:
    """Return status-like lines that do not use the canonical status format."""
    fenced = _fenced_code_mask(lines)
    return [
        line
        for idx, line in enumerate(lines)
        if not fenced[idx] and _is_status_line(line) and not _is_canonical_status(line)
    ]


def _fence_opener(line: str) -> tuple[str, int] | None:
    """Return ``(character, length)`` when *line* opens a fenced code block.

    A backtick fence info string may not contain backticks
    (CommonMark), so such a line does not open a fence.
    """
    matched = _FENCE_OPEN.match(line)
    if matched is None:
        return None
    run = matched.group(1)
    if run[0] == "`" and "`" in line[matched.end() :]:
        return None
    return run[0], len(run)


def _closes_fence(line: str, fence_char: str, fence_len: int) -> bool:
    """Return True when *line* closes the fence opened by *fence_char*/*fence_len*.

    A closing fence uses the same character as the opening fence, is at
    least as long, and carries no trailing info text (CommonMark). Only
    spaces and tabs count as no info text; other whitespace, such as a
    non-breaking space, is info text.
    """
    matched = _FENCE_OPEN.match(line)
    if matched is None or matched.group(1)[0] != fence_char:
        return False
    if len(matched.group(1)) < fence_len:
        return False
    return line[matched.end() :].strip(" \t") == ""


def _fenced_code_mask(lines: list[str]) -> list[bool]:
    """Return a per-line mask that is True while *lines* is inside a fence.

    A fence delimiter line itself is marked True: text may not be hidden in
    the delimiter line, and a marker immediately following the delimiter is
    still inside the fenced block. Fence delimiters follow CommonMark: an
    opening fence is three or more backticks or tildes after up to three
    leading spaces, and only a same-character run of at least the opening
    length with no trailing info text closes it.
    """
    mask: list[bool] = []
    fence: tuple[str, int] | None = None
    for line in lines:
        if fence is None:
            opened = _fence_opener(line)
            if opened is not None:
                fence = opened
                mask.append(True)
            else:
                mask.append(False)
        else:
            mask.append(True)
            if _closes_fence(line, fence[0], fence[1]):
                fence = None
    return mask


def _html_comment_mask(lines: list[str]) -> list[bool]:
    """Return a per-line mask that is True while *lines* is inside an HTML comment.

    A line is masked when it starts inside a comment or ends inside
    one, so a marker wrapped in ``<!--`` ... ``-->`` never counts as
    visible amendment text. Comment open and close markers are tracked
    across lines, and several comments on one line are handled.
    """
    mask: list[bool] = []
    in_comment = False
    for line in lines:
        starts_inside = in_comment
        idx = 0
        while idx < len(line):
            if in_comment:
                close = line.find("-->", idx)
                if close < 0:
                    break
                in_comment = False
                idx = close + 3
            else:
                open_at = line.find("<!--", idx)
                if open_at < 0:
                    break
                in_comment = True
                idx = open_at + 4
        mask.append(starts_inside or in_comment)
    return mask


_PRE_OPEN = re.compile(r"<pre\b", re.IGNORECASE)
_PRE_CLOSE = re.compile(r"</pre\b", re.IGNORECASE)
_INLINE_CODE_SPAN = re.compile(r"(`+).*?\1")


def _pre_block_mask(lines: list[str]) -> list[bool]:
    """Return a per-line mask that is True while *lines* is inside a <pre> block.

    A line is masked when it starts inside a block or ends inside one.
    Open and close tags are tracked across lines, case-insensitively,
    and several blocks on one line are handled. Tags appearing inside
    inline code spans (such as `` `<pre>` ``) are ignored and do not
    open or close a block.
    """
    mask: list[bool] = []
    in_pre = False
    for line in lines:
        starts_inside = in_pre
        cleaned = _INLINE_CODE_SPAN.sub("", line)
        idx = 0
        while idx < len(cleaned):
            if in_pre:
                close = _PRE_CLOSE.search(cleaned, idx)
                if close is None:
                    break
                in_pre = False
                idx = close.end()
            else:
                open_at = _PRE_OPEN.search(cleaned, idx)
                if open_at is None:
                    break
                in_pre = True
                idx = open_at.end()
        mask.append(starts_inside or in_pre)
    return mask


def _hidden_line_mask(lines: list[str]) -> list[bool]:
    """Return a per-line mask for lines hidden from the rendered document.

    A line inside a fenced code block, an HTML comment, or a raw HTML
    preformatted block is hidden.
    """
    fenced = _fenced_code_mask(lines)
    commented = _html_comment_mask(lines)
    preformatted = _pre_block_mask(lines)
    return [
        in_fence or in_comment or in_pre
        for in_fence, in_comment, in_pre in zip(fenced, commented, preformatted)
    ]


def _accepted_status_index(lines: list[str]) -> int | None:
    """Return the index of the sole line matching Accepted status in the header, or None.

    Lines inside a fenced code block or an HTML comment do not count,
    so a status hidden from the rendered document cannot authorize a
    transition.
    """
    header_lines = lines[:_HEADER_LINE_LIMIT]
    hidden = _hidden_line_mask(header_lines)
    matches = [
        idx
        for idx, line in enumerate(header_lines)
        if not hidden[idx] and _ACCEPTED_STATUS.match(line)
    ]
    return matches[0] if len(matches) == 1 else None


def _dated_superseded_status_index(lines: list[str]) -> int | None:
    """Return the index of the sole dated Superseded status line in the header, or None.

    Lines inside a fenced code block or an HTML comment do not count,
    so a status hidden from the rendered document cannot authorize a
    transition.
    """
    header_lines = lines[:_HEADER_LINE_LIMIT]
    hidden = _hidden_line_mask(header_lines)
    matches = [
        idx
        for idx, line in enumerate(header_lines)
        if not hidden[idx] and _SUPERSEDED_STATUS_DATED.fullmatch(line)
    ]
    return matches[0] if len(matches) == 1 else None


def _has_visible_marker(
    new_lines: list[str],
    start: int,
    end: int,
    hidden: list[bool],
    marker: re.Pattern[str],
) -> bool:
    """Return True when a marker line matching *marker* sits in *new_lines* [start:end].

    A line matches when *marker* matches from the start of the line;
    ``_AMENDMENT_MARKER`` ends with ``$``, so for the authorized
    transition the marker must occupy the whole line. The line must
    sit outside every hidden region.
    """
    return any(
        marker.match(new_lines[idx]) and not hidden[idx] for idx in range(start, end)
    )


def _has_appended_amendment_block(
    baseline_lines: list[str],
    new_lines: list[str],
    opcodes: list[tuple[str, int, int, int, int]],
) -> bool:
    """Return True when a dated amendment marker is inserted after the last
    *baseline_lines* line and outside any hidden region in *new_lines*.
    The marker line must match ``**[Amended Month Year]**`` exactly, so
    a marker wrapped in a single-line comment already fails.
    """
    hidden = _hidden_line_mask(new_lines)
    tail_start = len(baseline_lines)
    return any(
        tag == "insert"
        and i1 == tail_start
        and _has_visible_marker(new_lines, j1, j2, hidden, _AMENDMENT_MARKER)
        for tag, i1, _i2, j1, j2 in opcodes
    )


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


def _replace_or_delete_mutations(
    old_lines: list[str],
    opcodes: list[tuple[str, int, int, int, int]],
    authorized: tuple[int, int] | None,
) -> list[_Mutation]:
    """Return mutations for replace and delete opcodes.

    An authorized transition exempts the one replaced status line. A
    replace opcode always reports the whole HEAD block, because the
    pairing of old and new lines is ambiguous.
    """
    mutations: list[_Mutation] = []
    for tag, i1, i2, j1, j2 in opcodes:
        if tag not in {"replace", "delete"}:
            continue
        for k in range(i2 - i1):
            old_idx = i1 + k
            if authorized is not None and old_idx == authorized[0]:
                continue
            if tag == "delete":
                mutations.append((old_idx + 1, None, None, old_lines[old_idx]))
            else:
                mutations.append((old_idx + 1, j1 + 1, j2, old_lines[old_idx]))
    return mutations


def _unexpected_status_mutations(
    old_lines: list[str],
    new_lines: list[str],
    old_fenced: list[bool],
    new_fenced: list[bool],
    authorized: tuple[int, int] | None,
) -> list[_Mutation]:
    """Return status fields in *new_lines* with no baseline allowance.

    The allowance is a multiset of the baseline status-field texts; any
    ``> Status:`` line counts, whatever value it declares, so a
    supersession cannot add a second status field that declares a
    draft. An authorized transition consumes the old Accepted status
    line and grants the new dated Superseded status line. Every
    remaining status field in *new_lines* outside a fence is an extra
    status line; it is reported with no baseline line.
    """
    allowance = Counter(
        line
        for idx, line in enumerate(old_lines)
        if not old_fenced[idx] and _is_status_field(line)
    )
    if authorized is not None:
        old_idx, new_idx = authorized
        allowance[old_lines[old_idx]] -= 1
        allowance[new_lines[new_idx]] += 1
    mutations: list[_Mutation] = []
    for idx, line in enumerate(new_lines):
        if new_fenced[idx] or not _is_status_field(line):
            continue
        if allowance.get(line, 0) > 0:
            allowance[line] -= 1
        else:
            mutations.append((None, idx + 1, idx + 1, line))
    return mutations


def _map_old_index(
    opcodes: list[tuple[str, int, int, int, int]],
    old_idx: int,
) -> int | None:
    """Return the working-tree index that *old_idx* maps to, or None.

    Only an equal opcode maps baseline index ``i`` to ``j1 + (i - i1)``.
    Replace and delete opcodes give no mapping, because the replace
    path already reports those lines; a second report would only waste
    reported-line slots.
    """
    for tag, i1, i2, j1, _j2 in opcodes:
        if i1 <= old_idx < i2:
            if tag == "equal":
                return j1 + (old_idx - i1)
            return None
    return None


def _fenced_status_mutations(
    old_lines: list[str],
    opcodes: list[tuple[str, int, int, int, int]],
    old_fenced: list[bool],
    new_fenced: list[bool],
    authorized: tuple[int, int] | None,
) -> list[_Mutation]:
    """Return baseline status fields that the diff moved inside a fence.

    An insertion can fence a baseline status field and push it below
    the header window, so a later pull request could delete the record.
    Every baseline status field outside a fence, except the authorized
    transition's old status line, must stay outside a fence in the
    working tree. Only equal-opcode mappings are checked: a status
    field moved by a replace opcode is already reported by the replace
    path.
    """
    mutations: list[_Mutation] = []
    for idx, line in enumerate(old_lines):
        if old_fenced[idx] or not _is_status_field(line):
            continue
        if authorized is not None and idx == authorized[0]:
            continue
        head_idx = _map_old_index(opcodes, idx)
        if head_idx is not None and new_fenced[head_idx]:
            mutations.append((idx + 1, head_idx + 1, head_idx + 1, line))
    return mutations


def _unexpected_insertion_mutations(
    new_lines: list[str],
    opcodes: list[tuple[str, int, int, int, int]],
) -> list[_Mutation]:
    """Return inserted lines that are not part of a dated amendment block.

    Every inserted range must carry at least one dated amendment marker
    outside every hidden region; otherwise every inserted line in the
    range is reported. The marker may carry an issue annotation,
    because committed records use forms such as ``**[Amended November
    2026, #547]**``, and a block may sit before trailing sections, so a
    tail-only rule would reject the repository's own convention.
    Inserted status fields are left to the extra-status check, so one
    line is never reported twice.
    """
    hidden = _hidden_line_mask(new_lines)
    mutations: list[_Mutation] = []
    for tag, _i1, _i2, j1, j2 in opcodes:
        if tag != "insert":
            continue
        if _has_visible_marker(new_lines, j1, j2, hidden, _DATED_AMENDMENT_LINE):
            continue
        for idx in range(j1, j2):
            if _is_status_field(new_lines[idx]):
                continue
            mutations.append((None, idx + 1, idx + 1, new_lines[idx]))
    return mutations


def _mutation_opcodes(
    old_lines: list[str],
    new_lines: list[str],
    authorized: tuple[int, int] | None,
) -> list[tuple[str, int, int, int, int]]:
    """Return the opcodes that pair baseline lines with working-tree lines.

    When a transition is authorized, the baseline status line is first
    replaced with the new status line (the synthetic baseline), so the
    diff aligns against the real lines instead of a quotation in an
    appended amendment block. The raw opcodes are kept when
    authorization fails, so unauthorized edits still get diagnostics.
    """
    if authorized is None:
        return difflib.SequenceMatcher(
            a=old_lines, b=new_lines, autojunk=False
        ).get_opcodes()
    synthetic_old = list(old_lines)
    old_idx, new_idx = authorized
    synthetic_old[old_idx] = new_lines[new_idx]
    return difflib.SequenceMatcher(
        a=synthetic_old, b=new_lines, autojunk=False
    ).get_opcodes()


def historical_mutations(
    old_lines: list[str],
    new_lines: list[str],
) -> list[_Mutation]:
    """Return ``(baseline line, HEAD line start, HEAD line end, text)`` mutations.

    Insertions are allowed only inside an inserted range that carries
    a dated amendment marker. Any delete or replace opcode means
    accepted or superseded decision text was rewritten or replaced in
    place, except for the single authorized accepted-to-superseded
    status transition (see ``_authorized_transition``). A status field
    in *new_lines* that no baseline status field and no authorized
    transition accounts for is also reported, with a baseline line of
    ``None``. A baseline status field that the diff moves inside a
    fenced code block is reported with its baseline and HEAD lines.

    Line numbers may be ``None``: a deleted line has no HEAD line, and
    an extra status line has no baseline line.
    """
    authorized = _authorized_transition(old_lines, new_lines)
    opcodes = _mutation_opcodes(old_lines, new_lines, authorized)
    old_fenced = _fenced_code_mask(old_lines)
    new_fenced = _fenced_code_mask(new_lines)
    return (
        _replace_or_delete_mutations(old_lines, opcodes, authorized)
        + _unexpected_status_mutations(
            old_lines, new_lines, old_fenced, new_fenced, authorized
        )
        + _fenced_status_mutations(
            old_lines, opcodes, old_fenced, new_fenced, authorized
        )
        + _unexpected_insertion_mutations(new_lines, opcodes)
    )


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

    Candidate refs from ``PRE_COMMIT_FROM_REF``, ``GITHUB_BASE_SHA``, and
    ``GITHUB_BASE_REF`` are validated with ``is_safe_git_ref`` before use.
    An unsafe candidate is skipped so resolution falls through to
    subsequent tiers or default-branch candidates, matching
    ``scripts/git_base.py``. An explicit ref starting with ``-`` or
    containing ``:`` fails closed immediately, while valid git revision
    expressions like ``HEAD^`` are accepted.

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
        ref = explicit_ref.strip()
        if not ref or ref.startswith("-") or ":" in ref:
            return None
        resolved = _resolve_commit(ref)
        if not resolved and _fetch_target(repo_root, ref):
            clean_ref = ref
            for prefix in ("refs/remotes/origin/", "refs/heads/", "origin/"):
                if clean_ref.startswith(prefix):
                    clean_ref = clean_ref[len(prefix) :]
                    break
            for candidate in [
                ref,
                f"origin/{clean_ref}",
                f"refs/remotes/origin/{clean_ref}",
            ]:
                resolved = _resolve_commit(candidate)
                if resolved:
                    break
        return resolved

    from_ref = os.environ.get("PRE_COMMIT_FROM_REF")
    if from_ref and is_safe_git_ref(from_ref):
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
    if base_sha and is_safe_git_ref(base_sha):
        resolved = _resolve_commit(base_sha)
        if not resolved and _fetch_target(repo_root, base_sha):
            resolved = _resolve_commit(base_sha)
        if resolved:
            return resolved
        return None

    base_ref = os.environ.get("GITHUB_BASE_REF")
    if base_ref and is_safe_git_ref(base_ref):
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
    """Return ``(text, error)`` for git blob ``ref:path``.

    ``_git`` converts a decode error into a failed ``CompletedProcess``,
    so decode errors flow through the same return-code error path as
    other git failures.
    """
    try:
        exists = _git(repo_root, "cat-file", "-e", ref_and_path)
    except FileNotFoundError:
        return None, "git not found on PATH"
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


def _git_nul(repo_root: Path, *args: str) -> tuple[list[str], str | None]:
    """Run a git command that prints NUL-delimited output.

    The output is captured as bytes and decoded with ``surrogateescape``,
    so a carriage return or any other byte in a pathname survives
    universal-newline translation. Returns ``(parts, error)``: *parts*
    is the NUL-split output; *error* is a formatted git failure, with
    stderr decoded for the message.
    """
    try:
        proc = subprocess.run(
            ["git", *args],
            capture_output=True,
            cwd=str(repo_root),
            check=False,
        )
    except FileNotFoundError:
        return [], "git not found on PATH"
    if proc.returncode != 0:
        stderr = proc.stderr.decode("utf-8", "surrogateescape")
        detail = stderr.strip() or f"exit code {proc.returncode}"
        return [], f"git {args[0]} failed: {detail}"
    stdout = proc.stdout.decode("utf-8", "surrogateescape")
    return stdout.split("\0"), None


def _add_adr_paths(paths: set[str], parts: list[str]) -> None:
    """Add every ADR Markdown path in NUL-split *parts* to *paths*.

    ``-z`` output is unquoted, and a path may begin or end with a
    space, so paths are tested for truthiness only and are never
    stripped.
    """
    for field in parts:
        if field and is_adr_markdown(Path(field)):
            paths.add(field)


def enumerate_adrs(repo_root: Path, baseline_ref: str) -> tuple[set[str], str | None]:
    """Enumerate all ADR paths to check from baseline, working tree, and diff.

    Git output is NUL-delimited (``-z``) and byte-exact, so paths with
    non-ASCII characters or a carriage return arrive intact. Returns
    ``(paths, error)``. *error* is set when a git command fails, so the
    caller fails closed instead of silently checking nothing.
    """
    paths: set[str] = set()

    ls_parts, ls_error = _git_nul(
        repo_root,
        "ls-tree",
        "-r",
        "-z",
        "--name-only",
        baseline_ref,
        "--",
        str(_DECISIONS_DIR),
    )
    if ls_error is not None:
        return paths, ls_error
    _add_adr_paths(paths, ls_parts)

    dec_dir = repo_root / _DECISIONS_DIR
    if dec_dir.is_dir():
        for p in dec_dir.glob("*.md"):
            rel = _relative_to_repo(p, repo_root)
            if rel is not None and is_adr_markdown(rel):
                paths.add(rel.as_posix())

    diff_parts, diff_error = _git_nul(
        repo_root,
        "diff",
        "--name-only",
        "-z",
        "--diff-filter=ACDMR",
        baseline_ref,
        "--",
        str(_DECISIONS_DIR),
    )
    if diff_error is not None:
        return paths, diff_error
    _add_adr_paths(paths, diff_parts)

    return paths, None


def get_renames(
    repo_root: Path,
    baseline_ref: str,
) -> tuple[dict[str, str], str | None]:
    """Return ``(renames, error)`` for detected renames.

    Git output is NUL-delimited (``-z``) and byte-exact, so paths with
    non-ASCII characters or a carriage return arrive intact. Records
    are ``status\\0old\\0new\\0`` for a rename or copy status and
    ``status\\0path\\0`` otherwise. *renames* maps
    ``{old_rel_posix: new_rel_posix}``. *error* is set when the git
    diff fails, so the caller fails closed instead of silently skipping
    rename detection. Fields are never stripped: a path may begin or
    end with a space.
    """
    renames: dict[str, str] = {}
    fields, error = _git_nul(
        repo_root,
        "diff",
        "-M",
        "--name-status",
        "-z",
        "--diff-filter=R",
        baseline_ref,
        "--",
        str(_DECISIONS_DIR),
    )
    if error is not None:
        return renames, error
    idx = 0
    while idx + 1 < len(fields):
        status = fields[idx]
        if status.startswith(("R", "C")):
            if idx + 2 < len(fields):
                renames[fields[idx + 1]] = fields[idx + 2]
            idx += 3
        elif status:
            idx += 2
        else:
            idx += 1
    return renames, None


def _resolves_inside(path: Path, repo_root: Path) -> bool:
    """Return True when *path* resolves to a location under *repo_root*.

    ``_relative_to_repo`` normalizes the path lexically, so a symlink
    plus ``..`` can hide a location outside the repository. This check
    resolves the full path.
    """
    try:
        return path.resolve().is_relative_to(repo_root.resolve())
    except OSError:
        return False


def _has_symlinked_component(repo_root: Path, rel: Path) -> bool:
    """Return True when a *rel* path component below *repo_root* is a symlink.

    Every component, including intermediate directories such as
    ``docs/decisions``, is inspected with ``os.lstat`` so a symlinked
    directory cannot hide a rename or an in-place rewrite. An absent
    component is safe to continue past, because a deletion is reported
    later. Any other inspection error fails closed.
    """
    current = repo_root
    for part in rel.parts:
        current = current / part
        try:
            st = os.lstat(current)
        except FileNotFoundError:
            return False
        except OSError:
            return True
        if stat.S_ISLNK(st.st_mode):
            return True
    return False


def _display_line(text: str, limit: int = 80) -> str:
    """Return *text* truncated for diagnostic output."""
    if len(text) <= limit:
        return text
    return text[: limit - 3] + "..."


def _report_mutations(
    rel_posix: str,
    mutations: list[_Mutation],
) -> None:
    """Print a diagnostic for rewritten, deleted, or unauthorized historical text."""
    print(
        f"{rel_posix}: accepted or superseded decision text was rewritten "
        "or replaced in place"
    )
    for baseline_line, head_start, head_end, line in mutations[:_MAX_REPORTED_LINES]:
        if baseline_line is None:
            print(
                f"  HEAD line {head_start} (no corresponding baseline line): "
                f"{_display_line(line)}"
            )
        elif head_start is None:
            print(
                f"  baseline line {baseline_line} (deleted, no corresponding "
                f"HEAD line): {_display_line(line)}"
            )
        elif head_start == head_end:
            print(
                f"  baseline line {baseline_line} / HEAD line {head_start}: "
                f"{_display_line(line)}"
            )
        else:
            print(
                f"  baseline line {baseline_line} / HEAD lines "
                f"{head_start}-{head_end}: {_display_line(line)}"
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
        if baseline_ref:
            print(
                f"ERROR: cannot resolve explicit baseline ref {baseline_ref!r} to check against",
                file=sys.stderr,
            )
        else:
            print(
                "ERROR: cannot resolve default-branch baseline ref to check against",
                file=sys.stderr,
            )
        return 1

    renames, renames_error = get_renames(repo_root, resolved_baseline)
    if renames_error is not None:
        print(f"ERROR: {renames_error}", file=sys.stderr)
        failed = True
    handled_renames: set[str] = set()

    if files:
        raw_paths = list(files)
    else:
        enumerated, enumerate_error = enumerate_adrs(repo_root, resolved_baseline)
        if enumerate_error is not None:
            print(f"ERROR: {enumerate_error}", file=sys.stderr)
            failed = True
        raw_paths = sorted(enumerated)

    for raw in raw_paths:
        path = Path(raw)
        if not path.is_absolute():
            path = repo_root / path
        rel = _relative_to_repo(path, repo_root)
        if rel is None or not _resolves_inside(path, repo_root):
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
        if _has_symlinked_component(repo_root, rel):
            print(
                f"{rel_posix}: path traverses a symbolic link "
                "or cannot be inspected safely",
                file=sys.stderr,
            )
            failed = True
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
        for line in _ambiguous_status_lines(hist_text.splitlines()):
            print(f"{rel_posix}: ambiguous status header: {line}", file=sys.stderr)
            failed = True

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
                if _has_symlinked_component(repo_root, Path(new_path_posix)):
                    print(
                        f"{new_path_posix}: path traverses a symbolic link "
                        "or cannot be inspected safely",
                        file=sys.stderr,
                    )
                    failed = True
                    continue
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
