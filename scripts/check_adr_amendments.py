#!/usr/bin/env python3
"""Fail if accepted ADR historical text is rewritten in place.

Used as the ``adr-amendments`` local pre-commit hook (and via
``scripts/lint.py``). Accepted architecture decision records under
``docs/decisions/`` are immutable historical records: git diffs may
insert dated amendment blocks, but must not delete or modify lines
from the baseline copy of an accepted ADR.

The baseline is resolved from the merge-base with the default branch
(or ``PRE_COMMIT_FROM_REF`` when pre-commit sets it), falling back to
``HEAD`` for local uncommitted checks when the file is not on the baseline.
"""

from __future__ import annotations

import argparse
import difflib
import os
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

    Returns ``None`` when no baseline ref can be resolved (fail closed).
    """

    def _resolve_commit(target: str) -> str | None:
        rev = _git(repo_root, "rev-parse", "--verify", f"{target}^{{commit}}")
        if rev.returncode == 0 and rev.stdout.strip():
            mb = _git(repo_root, "merge-base", "HEAD", target)
            if mb.returncode == 0 and mb.stdout.strip():
                return mb.stdout.strip()
            return rev.stdout.strip()
        return None

    if explicit_ref:
        return _resolve_commit(explicit_ref)

    from_ref = os.environ.get("PRE_COMMIT_FROM_REF")
    if from_ref:
        resolved = _resolve_commit(from_ref)
        if resolved:
            return resolved

    base_sha = os.environ.get("GITHUB_BASE_SHA")
    if base_sha:
        resolved = _resolve_commit(base_sha)
        if resolved:
            return resolved

    base_ref = os.environ.get("GITHUB_BASE_REF")
    if base_ref:
        for candidate in [
            f"origin/{base_ref}",
            base_ref,
            f"refs/remotes/origin/{base_ref}",
        ]:
            resolved = _resolve_commit(candidate)
            if resolved:
                return resolved

    candidates = [
        "origin/main",
        "main",
        "origin/master",
        "master",
        "refs/remotes/origin/HEAD",
    ]
    for target in candidates:
        resolved = _resolve_commit(target)
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


def _report_mutations(rel_posix: str, mutations: list[tuple[int, str]]) -> None:
    """Print a diagnostic for rewritten or deleted historical lines."""
    print(f"{rel_posix}: accepted decision text was rewritten or replaced in place")
    for line_no, line in mutations[:_MAX_REPORTED_LINES]:
        print(f"  HEAD line {line_no}: {_display_line(line)}")
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
        if not is_accepted(hist_text):
            continue

        if not path.is_file():
            new_path_posix = renames.get(rel_posix)
            if new_path_posix:
                print(
                    f"{rel_posix}: accepted architecture decision record "
                    f"was deleted (renamed to {new_path_posix})"
                )
                failed = True
                handled_renames.add(new_path_posix)
                new_path = repo_root / new_path_posix
                if new_path.is_file():
                    try:
                        new_text = new_path.read_text(encoding="utf-8")
                    except OSError as exc:
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
