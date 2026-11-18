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
``layout.Document[records.InterfaceRecord]{`` outside a slice of
builder calls -- regress to one-off fixtures that break the first time
a new check is registered.

A line is exempt when it (or the standalone comment line immediately
above it) carries a ``// nolint:fixtures`` marker, which is reserved
for tests that intentionally exercise a synthetic or malformed
document. ``validator/internal/testutil/**`` itself is always exempt,
since that package is where the literals legitimately live.
"""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
VALIDATOR_INTERNAL = Path("validator") / "internal"
TESTUTIL_PREFIX = "validator/internal/testutil/"
TESTUTIL_DIR_POSIX = "validator/internal/testutil"

_NOLINT_MARKER = "nolint:fixtures"
_DOCUMENT_LITERAL = re.compile(
    r"(?<!\[\])\blayout\.Document\[records\.(Requirement|InterfaceRecord)\]\{"
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
    """Return True when *rel_path* is the builder package itself."""
    posix = rel_path.replace("\\", "/")
    return posix == TESTUTIL_DIR_POSIX or posix.startswith(TESTUTIL_PREFIX)


def _remediation(type_name: str) -> str:
    """Return a remediation message pointing to the matching testutil builders."""
    document_builder, yaml_builder = _BUILDERS[type_name]
    return (
        f"use validator/internal/testutil.{document_builder} (or "
        f"{yaml_builder}) instead of constructing records.{type_name}{{}} "
        "directly in test code; annotate the line with `// nolint:fixtures` "
        "only when the test intentionally exercises a synthetic or "
        "malformed document."
    )


def _line_is_exempt(raw_lines: list[str], index: int, comment_part: str) -> bool:
    """Return True when the match at *index* carries a nolint marker.

    The marker may trail the matched line itself, or stand alone on the
    line immediately above it.
    """
    if _NOLINT_MARKER in comment_part:
        return True
    if index > 0:
        previous = raw_lines[index - 1].strip()
        if previous.startswith("//") and _NOLINT_MARKER in previous:
            return True
    return False


def find_violations(rel_path: str, text: str) -> list[Violation]:
    """Return prohibited literal violations in *text* at *rel_path*.

    *rel_path* is used only to decide exemption (the builder package)
    and file selection (``*_test.go``); it need not exist on disk.
    """
    if is_exempt_path(rel_path) or not rel_path.endswith("_test.go"):
        return []
    source = strip_block_comments(text)
    raw_lines = text.splitlines()
    stripped_lines = source.splitlines()
    violations: list[Violation] = []
    for index, line in enumerate(stripped_lines):
        code_part, _, comment_part = line.partition("//")
        if not code_part.strip():
            continue
        matches: list[tuple[str, str]] = []
        for match in _DOCUMENT_LITERAL.finditer(code_part):
            matches.append(("document_literal", match.group(1)))
        for match in _RECORD_LITERAL.finditer(code_part):
            matches.append(("record_literal", match.group(1)))
        if not matches:
            continue
        if _line_is_exempt(raw_lines, index, comment_part):
            continue
        for kind, type_name in matches:
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
        lines.append(
            f"ERROR: {violation.rel_path}:{violation.line}: prohibited "
            f"records.{violation.type_name}{{}} literal "
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
        "--repo-root",
        type=Path,
        default=REPO_ROOT,
        help="repository root (default: parent of scripts/)",
    )
    args = parser.parse_args(argv)
    return check(repo_root=args.repo_root)


if __name__ == "__main__":
    sys.exit(main())
