#!/usr/bin/env python3
"""Fail if accepted ADR historical text is rewritten in place.

Used as the ``adr-amendments`` local pre-commit hook (and via
``scripts/lint.py``). Accepted architecture decision records under
``docs/decisions/`` are immutable historical records: git diffs may
insert dated amendment blocks, but must not delete or modify lines
from the ``HEAD`` copy of an accepted ADR.
"""

from __future__ import annotations

import argparse
import difflib
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

_DECISIONS_DIR = Path("docs") / "decisions"
_ACCEPTED_STATUS = re.compile(
    r"^> Status:\s*\*\*Accepted\*\*",
    re.MULTILINE,
)
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


def historical_mutations(
    old_lines: list[str],
    new_lines: list[str],
) -> list[tuple[int, str]]:
    """Return ``(HEAD line number, text)`` for deleted or replaced lines.

    Insertions are allowed so dated amendment blocks can be added.
    Any delete or replace opcode means accepted decision text was
    rewritten or replaced in place.
    """
    matcher = difflib.SequenceMatcher(
        a=old_lines,
        b=new_lines,
        autojunk=False,
    )
    mutations: list[tuple[int, str]] = []
    for tag, i1, i2, _j1, _j2 in matcher.get_opcodes():
        if tag in {"replace", "delete"}:
            for idx in range(i1, i2):
                mutations.append((idx + 1, old_lines[idx]))
    return mutations


def _git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo_root* and return the completed process."""
    return subprocess.run(
        ["git", *args],
        capture_output=True,
        text=True,
        cwd=str(repo_root),
        check=False,
    )


def _relative_to_repo(path: Path, repo_root: Path) -> Path | None:
    """Return *path* relative to *repo_root*, or None if it is outside."""
    try:
        resolved = path.resolve()
        root = repo_root.resolve()
    except OSError:
        return None
    if not resolved.is_relative_to(root):
        return None
    return resolved.relative_to(root)


def _head_file_text(
    repo_root: Path,
    rel_posix: str,
) -> tuple[str | None, str | None]:
    """Return ``(text, error)`` for ``HEAD:rel_posix``.

    *text* is None when the path is not in ``HEAD``. *error* is set
    when git itself cannot be queried.
    """
    try:
        exists = _git(repo_root, "cat-file", "-e", f"HEAD:{rel_posix}")
    except FileNotFoundError:
        return None, "git not found on PATH"
    if exists.returncode != 0:
        stderr = exists.stderr.lower()
        if "not a git repository" in stderr or "bad revision" in stderr:
            detail = exists.stderr.strip() or "git cat-file failed"
            return None, detail
        return None, None
    try:
        shown = _git(repo_root, "show", f"HEAD:{rel_posix}")
    except FileNotFoundError:
        return None, "git not found on PATH"
    if shown.returncode != 0:
        return None, shown.stderr.strip() or "git show failed"
    return shown.stdout, None


def _display_line(text: str, limit: int = 80) -> str:
    """Return *text* truncated for diagnostic output."""
    if len(text) <= limit:
        return text
    return text[: limit - 3] + "..."


def _report_mutations(rel_posix: str, mutations: list[tuple[int, str]]) -> None:
    """Print a diagnostic for rewritten or deleted historical lines."""
    print(f"{rel_posix}: accepted decision text was rewritten or replaced in place")
    for line_no, line in mutations[:_MAX_REPORTED_LINES]:
        print(f"  HEAD line {line_no}: {_display_line(line)}")
    remaining = len(mutations) - _MAX_REPORTED_LINES
    if remaining > 0:
        print(f"  … ({remaining} more line(s))")


def check(files: list[str], repo_root: Path = REPO_ROOT) -> int:
    """Return 0 when every given accepted ADR preserves HEAD lines."""
    failed = False
    for raw in files:
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
        head_text, git_error = _head_file_text(repo_root, rel_posix)
        if git_error is not None:
            print(f"ERROR: {rel_posix}: {git_error}", file=sys.stderr)
            failed = True
            continue
        if head_text is None:
            continue
        if not is_accepted(head_text):
            continue
        if not path.is_file():
            print(f"{rel_posix}: accepted architecture decision record was deleted")
            failed = True
            continue
        try:
            new_text = path.read_text(encoding="utf-8")
        except OSError as exc:
            print(
                f"ERROR: {rel_posix}: cannot read working tree ({type(exc).__name__})",
                file=sys.stderr,
            )
            failed = True
            continue
        mutations = historical_mutations(
            head_text.splitlines(),
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
        "files",
        nargs="*",
        help="ADR Markdown files (paths relative to the repository root)",
    )
    args = parser.parse_args(argv)
    return check(args.files)


if __name__ == "__main__":
    sys.exit(main())
