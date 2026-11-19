#!/usr/bin/env python3
"""Fail if validator tests construct bare record literals directly.

Used as the ``validator-fixtures`` local pre-commit hook (and via
``scripts/lint.py``). ``validator/internal/testutil`` (see
``fixtures.go``) provides canonical builders
(``ValidRequirementDocument``, ``ValidInterfaceDocument``,
``ValidRequirementYAML``, ``ValidInterfaceYAML``) that satisfy every
check registered in ``specvalidation.Validate``. Test files under
``validator/internal/`` that instead construct ``records.Requirement{``
or ``records.InterfaceRecord{`` directly -- or build a synthetic
``layout.Document[records.Requirement]{`` /
``layout.Document[records.InterfaceRecord]{`` (either as a single
literal or as a slice element literal) outside a slice of builder calls
-- regress to one-off fixtures that break the first time a new check is
registered.

A line is exempt when it (or the standalone comment line immediately
above it) carries a ``// nolint:fixtures`` or ``/* nolint:fixtures */``
marker, which is reserved for tests that intentionally exercise a
synthetic or malformed document. ``validator/internal/testutil/**``,
``validator/internal/records/**``, and
``validator/internal/layout/**`` are exempt (testutil is where the
canonical fixtures live; records and layout cannot import testutil
without creating an import cycle). Positional file arguments are accepted
and ignored for CLI compatibility with sibling check scripts.
"""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
VALIDATOR_INTERNAL = Path("validator") / "internal"
EXEMPT_DIRS_POSIX = (
    "validator/internal/testutil",
    "validator/internal/records",
    "validator/internal/layout",
)
EXEMPT_PREFIXES = tuple(f"{d}/" for d in EXEMPT_DIRS_POSIX)

_NOLINT_STANDALONE = re.compile(r"^\s*(?://|/\*)\s*nolint:fixtures\b")
_NOLINT_CURRENT = re.compile(r"(?://|/\*)\s*nolint:fixtures\b")
_DOCUMENT_LITERAL = re.compile(
    r"(?<!\[\])\blayout\.Document\[records\.(Requirement|InterfaceRecord)\]\{"
)
_MAP_DOCUMENT_HEADER = re.compile(r"\bmap\[[^\]]*\]\s*$")
_DOCUMENT_SLICE_START = re.compile(
    r"\[\]layout\.Document\[records\.(Requirement|InterfaceRecord)\]\{"
)
_RECORD_LITERAL = re.compile(r"\brecords\.(Requirement|InterfaceRecord)\{")

_BUILDERS = {
    "Requirement": ("ValidRequirementDocument", "ValidRequirementYAML"),
    "InterfaceRecord": ("ValidInterfaceDocument", "ValidInterfaceYAML"),
}


class ValidatorFixturesError(ValueError):
    """A test source file cannot be read."""


@dataclass(frozen=True)
class Violation:
    """One prohibited literal found in a validator test file."""

    rel_path: str
    line: int
    kind: str  # "record_literal" or "document_literal"
    type_name: str  # "Requirement" or "InterfaceRecord"
    snippet: str
    remediation: str


def strip_block_comments(text: str) -> str:
    """Replace ``/* ... */`` comments with spaces, preserving newlines."""
    pieces: list[str] = []
    index = 0
    length = len(text)
    while index < length:
        start = text.find("/*", index)
        if start < 0:
            pieces.append(text[index:])
            break
        pieces.append(text[index:start])
        end = text.find("*/", start + 2)
        if end < 0:
            comment = text[start:]
            pieces.append(re.sub(r"[^\n]", " ", comment))
            break
        comment = text[start : end + 2]
        pieces.append(re.sub(r"[^\n]", " ", comment))
        index = end + 2
    return "".join(pieces)


def is_exempt_path(rel_path: str) -> bool:
    """Return True when *rel_path* is exempt from fixture builder enforcement.

    Exempt packages are:
    - ``validator/internal/testutil/``: where the canonical fixtures live.
    - ``validator/internal/records/``: low-level types; cannot import testutil.
    - ``validator/internal/layout/``: low-level layout types; cannot import testutil.
    """
    posix = rel_path.replace("\\", "/")
    return posix in EXEMPT_DIRS_POSIX or any(
        posix.startswith(prefix) for prefix in EXEMPT_PREFIXES
    )


def _remediation(type_name: str) -> str:
    """Return a remediation message pointing to the matching testutil builders."""
    document_builder, yaml_builder = _BUILDERS[type_name]
    return (
        f"use validator/internal/testutil.{document_builder} (or "
        f"{yaml_builder}) instead of constructing records.{type_name}{{}} "
        "directly in test code; annotate the line with `// nolint:fixtures` "
        "only when the test intentionally exercises a synthetic or "
        "malformed document (or where importing testutil would create an "
        "import cycle)."
    )


def _line_is_exempt(raw_lines: list[str], index: int) -> bool:
    """Return True when the match at *index* carries a nolint marker.

    The marker may appear on the matched line itself (as a ``//`` or
    ``/* ... */`` comment), or stand alone on the line immediately above it.
    """
    if 0 <= index < len(raw_lines):
        current = raw_lines[index].strip()
        if _NOLINT_CURRENT.search(current):
            return True
    if index > 0:
        previous = raw_lines[index - 1]
        if _NOLINT_STANDALONE.search(previous):
            return True
    return False


def _slice_has_unexempted_bare_element(
    stripped_lines: list[str],
    raw_lines: list[str],
    start_line_idx: int,
    start_col: int,
) -> bool:
    """Return True when the slice starting at *start_line_idx*:*start_col*
    contains at least one unexempted bare '{'-opening element literal.
    """
    depth = 1
    paren_depth = 0
    bracket_depth = 0
    in_string: str | None = None
    expecting_element = True

    line_idx = start_line_idx
    col = start_col

    while line_idx < len(stripped_lines):
        line = stripped_lines[line_idx]
        line_len = len(line)

        while col < line_len:
            if in_string is None and col + 1 < line_len and line[col : col + 2] == "//":
                break

            ch = line[col]

            if in_string is not None:
                if in_string == '"':
                    if ch == "\\":
                        col += 2
                        continue
                    elif ch == '"':
                        in_string = None
                elif in_string == "`":
                    if ch == "`":
                        in_string = None
                elif in_string == "'":
                    if ch == "\\":
                        col += 2
                        continue
                    elif ch == "'":
                        in_string = None
                col += 1
                continue

            if ch == '"':
                in_string = '"'
                expecting_element = False
                col += 1
                continue
            elif ch == "`":
                in_string = "`"
                expecting_element = False
                col += 1
                continue
            elif ch == "'":
                in_string = "'"
                expecting_element = False
                col += 1
                continue

            if ch in " \t\r\n":
                col += 1
                continue

            if (
                expecting_element
                and depth == 1
                and paren_depth == 0
                and bracket_depth == 0
            ):
                expecting_element = False
                if ch == "}":
                    depth -= 1
                    col += 1
                    if depth == 0:
                        return False
                    continue
                if ch == "{":
                    if not _line_is_exempt(raw_lines, line_idx):
                        return True
                    depth += 1
                    col += 1
                    continue

            if ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    return False
            elif ch == "(":
                paren_depth += 1
            elif ch == ")":
                if paren_depth > 0:
                    paren_depth -= 1
            elif ch == "[":
                bracket_depth += 1
            elif ch == "]":
                if bracket_depth > 0:
                    bracket_depth -= 1
            elif ch == "," and depth == 1 and paren_depth == 0 and bracket_depth == 0:
                expecting_element = True

            col += 1

        line_idx += 1
        col = 0

    return False


def find_violations(rel_path: str, text: str) -> list[Violation]:
    """Return prohibited literal violations in *text* at *rel_path*.

    *rel_path* is used only to decide exemption (builder and low-level
    packages) and file selection (``*_test.go``); it need not exist on disk.
    """
    if is_exempt_path(rel_path) or not rel_path.endswith("_test.go"):
        return []
    source = strip_block_comments(text)
    raw_lines = text.splitlines()
    stripped_lines = source.splitlines()
    violations: list[Violation] = []
    for index, line in enumerate(stripped_lines):
        code_part, _, _ = line.partition("//")
        if not code_part.strip():
            continue
        matches: list[tuple[str, str]] = []
        for match in _DOCUMENT_LITERAL.finditer(code_part):
            prefix = code_part[: match.start()]
            if _MAP_DOCUMENT_HEADER.search(prefix):
                continue
            matches.append(("document_literal", match.group(1)))
        for match in _DOCUMENT_SLICE_START.finditer(code_part):
            if _slice_has_unexempted_bare_element(
                stripped_lines, raw_lines, index, match.end()
            ):
                matches.append(("document_literal", match.group(1)))
        for match in _RECORD_LITERAL.finditer(code_part):
            matches.append(("record_literal", match.group(1)))
        if not matches:
            continue
        if _line_is_exempt(raw_lines, index):
            continue
        unique_matches: list[tuple[str, str]] = []
        for m in matches:
            if m not in unique_matches:
                unique_matches.append(m)
        for kind, type_name in unique_matches:
            violations.append(
                Violation(
                    rel_path=rel_path,
                    line=index + 1,
                    kind=kind,
                    type_name=type_name,
                    snippet=code_part.strip(),
                    remediation=_remediation(type_name),
                )
            )
    return violations


def _relative_posix(path: Path, repo_root: Path) -> str:
    """Return *path* as a posix path relative to *repo_root* when possible."""
    try:
        return path.resolve().relative_to(repo_root.resolve()).as_posix()
    except (ValueError, OSError):
        return path.as_posix()


def _read_text(path: Path) -> str:
    """Return UTF-8 text from *path*, or raise ValidatorFixturesError."""
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise ValidatorFixturesError(
            f"{path.as_posix()}: cannot read file ({type(error).__name__})"
        ) from None


def _iter_test_sources(internal_root: Path) -> list[Path]:
    """Return ``*_test.go`` files under *internal_root*."""
    sources: list[Path] = []
    for path in sorted(internal_root.rglob("*_test.go")):
        try:
            is_file = path.is_file()
        except OSError:
            continue
        if is_file:
            sources.append(path)
    return sources


def collect_violations(repo_root: Path) -> tuple[list[Violation], list[str]]:
    """Scan ``validator/internal/`` test files for prohibited literals."""
    errors: list[str] = []
    internal = repo_root / VALIDATOR_INTERNAL
    try:
        is_dir = internal.is_dir()
    except OSError as error:
        return [], [f"{VALIDATOR_INTERNAL.as_posix()}: {type(error).__name__}"]
    if not is_dir:
        return [], [f"{VALIDATOR_INTERNAL.as_posix()} is missing or not a directory"]
    violations: list[Violation] = []
    for path in _iter_test_sources(internal):
        rel_path = _relative_posix(path, repo_root)
        if is_exempt_path(rel_path):
            continue
        try:
            text = _read_text(path)
        except ValidatorFixturesError as error:
            errors.append(str(error))
            continue
        violations.extend(find_violations(rel_path, text))
    violations.sort(key=lambda item: (item.rel_path, item.line))
    return violations, errors


def format_violations(violations: list[Violation], errors: list[str]) -> list[str]:
    """Return stderr diagnostic lines for *violations* and *errors*."""
    lines: list[str] = []
    for error in errors:
        lines.append(f"ERROR: {error}")
    for violation in violations:
        if violation.kind == "document_literal":
            literal_type = f"layout.Document[records.{violation.type_name}]{{}}"
        else:
            literal_type = f"records.{violation.type_name}{{}}"
        lines.append(
            f"ERROR: {violation.rel_path}:{violation.line}: prohibited "
            f"{literal_type} literal "
            f"({violation.snippet!r}); {violation.remediation}"
        )
    return lines


def check(repo_root: Path = REPO_ROOT) -> int:
    """Return 0 when no validator test constructs a bare record literal."""
    violations, errors = collect_violations(repo_root)
    if not violations and not errors:
        print(
            "No prohibited records.Requirement{} or records.InterfaceRecord{} "
            f"literals found under {VALIDATOR_INTERNAL.as_posix()}."
        )
        return 0
    for line in format_violations(violations, errors):
        print(line, file=sys.stderr)
    return 1


def main(argv: list[str] | None = None) -> int:
    """Parse CLI arguments and check validator test fixture usage."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "files",
        nargs="*",
        help="positional file arguments are accepted and ignored for CLI compatibility with sibling check scripts",
    )
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=REPO_ROOT,
        help="repository root (default: parent of scripts/)",
    )
    args = parser.parse_args(argv)
    return check(repo_root=args.repo_root)


if __name__ == "__main__":
    sys.exit(main())
