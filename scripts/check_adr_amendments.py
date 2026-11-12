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
import stat
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


def _git(
    repo_root: Path,
    *args: str,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo_root* and return the completed process."""
    run_env = dict(os.environ)
    if env:
        run_env.update(env)
    return subprocess.run(
        ["git", *args],
        capture_output=True,
        text=True,
        cwd=str(repo_root),
        check=False,
        env=run_env,
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
                    f"{rel_posix}: accepted architecture decision record "
                    f"was deleted (renamed to {new_path_posix})"
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

        if not stat.S_ISREG(st.st_mode):
            print(
                f"ERROR: {rel_posix}: not a regular file",
                file=sys.stderr,
            )
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
