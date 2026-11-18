#!/usr/bin/env python3
"""Fail if validator diagnostic codes are missing from documentation.

Used as the ``validator-spec-parity`` local pre-commit hook (and via
``scripts/lint.py``). Every exported ``Code*`` diagnostic constant under
``validator/internal/`` must appear in ``validator/README.md`` and be
listed in ``docs/architecture/ProtoBotV2.md``. Raw string literals in
``validator/internal/specvalidation/`` that duplicate an exported
``Code*`` value are also rejected.
"""

from __future__ import annotations

import argparse
import re
import sys
from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
VALIDATOR_INTERNAL = Path("validator") / "internal"
README_RELATIVE = Path("validator") / "README.md"
SPEC_RELATIVE = Path("docs") / "architecture" / "ProtoBotV2.md"
SPECVALIDATION_DIR = VALIDATOR_INTERNAL / "specvalidation"

_CODE_VALUE = r"[a-z]+(?:_[a-z]+)*\.[a-z]+(?:_[a-z]+)*"
_CONST_ASSIGN = re.compile(
    r"^\s*(?:const\s+)?(?P<name>Code[A-Za-z0-9]+)\s*=\s*"
    r"(?P<quote>[\"`])(?P<value>" + _CODE_VALUE + r")(?P=quote)"
)
_LINE_COMMENT = re.compile(r"^\s*//")
_STRING_LIT = re.compile(r"[\"`](" + _CODE_VALUE + r")[\"`]")
_BACKTICK_CODE = re.compile(r"`(" + _CODE_VALUE + r")`")
_VALIDATOR_HEADING = re.compile(r"(?m)^#{2,3}[ \t]+Validator[ \t]*$")
_NEXT_HEADING = re.compile(r"(?m)^#{1,3}[ \t]+")


class ValidatorSpecParityError(ValueError):
    """A diagnostic-code documentation input cannot be read or parsed."""


@dataclass(frozen=True)
class CodeConstant:
    """One exported ``Code*`` diagnostic constant."""

    name: str
    value: str
    rel_path: str
    line: int


@dataclass(frozen=True)
class RawStringDuplicate:
    """A raw diagnostic string that duplicates an exported constant."""

    rel_path: str
    line: int
    value: str
    constant_name: str


@dataclass(frozen=True)
class ParityReport:
    """Parity result for one repository tree."""

    constants: tuple[CodeConstant, ...]
    missing_in_readme: tuple[str, ...]
    missing_in_protobot_v2: tuple[str, ...]
    raw_string_duplicates: tuple[RawStringDuplicate, ...]
    errors: tuple[str, ...]

    @property
    def ok(self) -> bool:
        """Return True when every constant is documented and no errors exist."""
        return not (
            self.missing_in_readme
            or self.missing_in_protobot_v2
            or self.raw_string_duplicates
            or self.errors
        )


def humanize_code(code: str) -> str:
    """Return a prose form of *code* by replacing dots and underscores."""
    return code.replace(".", " ").replace("_", " ")


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


def parse_code_constants(text: str, rel_path: str) -> list[CodeConstant]:
    """Return exported diagnostic constants declared in Go source *text*."""
    source = strip_block_comments(text)
    constants: list[CodeConstant] = []
    for line_number, raw_line in enumerate(source.splitlines(), start=1):
        if _LINE_COMMENT.match(raw_line):
            continue
        code_part, _, _trailing = raw_line.partition("//")
        match = _CONST_ASSIGN.match(code_part)
        if match is None:
            continue
        constants.append(
            CodeConstant(
                name=match.group("name"),
                value=match.group("value"),
                rel_path=rel_path,
                line=line_number,
            )
        )
    return constants


def extract_documented_codes(text: str) -> set[str]:
    """Return diagnostic-code strings quoted in Markdown backticks."""
    return set(_BACKTICK_CODE.findall(text))


def extract_validator_section(text: str) -> str:
    """Return the Validator component section, or an empty string."""
    match = _VALIDATOR_HEADING.search(text)
    if match is None:
        return ""
    start = match.end()
    nxt = _NEXT_HEADING.search(text, start)
    if nxt is None:
        return text[start:]
    return text[start : nxt.start()]


def spec_listing_text(text: str) -> str:
    """Return Validator-section text, falling back to the whole document."""
    section = extract_validator_section(text)
    if section.strip():
        return section
    return text


def is_listed_in_spec(code: str, spec_text: str) -> bool:
    """Return True when *code* or its prose form appears in *spec_text*."""
    if not code or not spec_text.strip():
        return False
    pattern = re.compile(rf"(?<![A-Za-z0-9_]){re.escape(code)}(?![A-Za-z0-9_])")
    if pattern.search(spec_text):
        return True
    humanized = humanize_code(code)
    prose_suffixes = r"(?:\s+(?:checks?|checking|rules?|codes?|diagnostics?))"
    humanized_pattern = re.compile(
        rf"(?<![A-Za-z0-9_]){re.escape(humanized)}"
        rf"(?:{prose_suffixes}(?![A-Za-z0-9_])|(?!\s*[A-Za-z0-9_]))",
        re.IGNORECASE,
    )
    return bool(humanized_pattern.search(spec_text))


def find_raw_string_duplicates(
    text: str,
    rel_path: str,
    constants_by_value: dict[str, str],
) -> list[RawStringDuplicate]:
    """Return raw diagnostic strings that duplicate exported constants."""
    source = strip_block_comments(text)
    duplicates: list[RawStringDuplicate] = []
    for line_number, raw_line in enumerate(source.splitlines(), start=1):
        code_part, _, _trailing = raw_line.partition("//")
        if _CONST_ASSIGN.match(code_part):
            continue
        for match in _STRING_LIT.finditer(code_part):
            value = match.group(1)
            constant_name = constants_by_value.get(value)
            if constant_name is None:
                continue
            duplicates.append(
                RawStringDuplicate(
                    rel_path=rel_path,
                    line=line_number,
                    value=value,
                    constant_name=constant_name,
                )
            )
    return duplicates


def _relative_posix(path: Path, repo_root: Path) -> str:
    """Return *path* as a posix path relative to *repo_root* when possible."""
    try:
        return path.resolve().relative_to(repo_root.resolve()).as_posix()
    except (ValueError, OSError):
        return path.as_posix()


def _read_text(path: Path) -> str:
    """Return UTF-8 text from *path*, or raise ValidatorSpecParityError."""
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise ValidatorSpecParityError(
            f"{path.as_posix()}: cannot read file ({type(error).__name__})"
        ) from None


def _iter_go_sources(internal_root: Path) -> list[Path]:
    """Return non-test Go files under *internal_root*."""
    sources: list[Path] = []
    for path in sorted(internal_root.rglob("*.go")):
        try:
            is_file = path.is_file()
        except OSError:
            continue
        if not is_file:
            continue
        try:
            rel = path.relative_to(internal_root)
        except ValueError:
            continue
        if rel.name.endswith("_test.go"):
            continue
        if "testdata" in rel.parts:
            continue
        sources.append(path)
    return sources


def collect_constants(repo_root: Path) -> tuple[list[CodeConstant], list[str]]:
    """Parse diagnostic constants from ``validator/internal/``."""
    errors: list[str] = []
    internal = repo_root / VALIDATOR_INTERNAL
    try:
        is_dir = internal.is_dir()
    except OSError as error:
        return [], [f"{VALIDATOR_INTERNAL.as_posix()}: {type(error).__name__}"]
    if not is_dir:
        return [], [f"{VALIDATOR_INTERNAL.as_posix()} is missing or not a directory"]
    constants: list[CodeConstant] = []
    for path in _iter_go_sources(internal):
        rel_path = _relative_posix(path, repo_root)
        try:
            text = _read_text(path)
        except ValidatorSpecParityError as error:
            errors.append(str(error))
            continue
        constants.extend(parse_code_constants(text, rel_path))
    constants.sort(key=lambda item: (item.value, item.rel_path, item.line))
    return constants, errors


def collect_raw_string_duplicates(
    repo_root: Path,
    constants: Iterable[CodeConstant],
) -> tuple[list[RawStringDuplicate], list[str]]:
    """Find raw diagnostic strings in ``specvalidation`` that duplicate constants."""
    constants_by_value = {item.value: item.name for item in constants}
    if not constants_by_value:
        return [], []
    errors: list[str] = []
    duplicates: list[RawStringDuplicate] = []
    specvalidation = repo_root / SPECVALIDATION_DIR
    try:
        is_dir = specvalidation.is_dir()
    except OSError as error:
        return [], [f"{SPECVALIDATION_DIR.as_posix()}: {type(error).__name__}"]
    if not is_dir:
        return [], []
    for path in _iter_go_sources(specvalidation):
        rel_path = _relative_posix(path, repo_root)
        try:
            text = _read_text(path)
        except ValidatorSpecParityError as error:
            errors.append(str(error))
            continue
        duplicates.extend(
            find_raw_string_duplicates(text, rel_path, constants_by_value)
        )
    duplicates.sort(key=lambda item: (item.rel_path, item.line, item.value))
    return duplicates, errors


def evaluate(repo_root: Path) -> ParityReport:
    """Compare declared diagnostic constants against README and ProtoBotV2.md."""
    constants, errors = collect_constants(repo_root)
    duplicates, duplicate_errors = collect_raw_string_duplicates(repo_root, constants)
    errors.extend(duplicate_errors)

    readme_text = ""
    readme_path = repo_root / README_RELATIVE
    try:
        readme_text = _read_text(readme_path)
    except ValidatorSpecParityError as error:
        errors.append(str(error))

    spec_text = ""
    spec_path = repo_root / SPEC_RELATIVE
    try:
        spec_text = _read_text(spec_path)
    except ValidatorSpecParityError as error:
        errors.append(str(error))

    documented = extract_documented_codes(readme_text)
    listing = spec_listing_text(spec_text)
    missing_readme: list[str] = []
    missing_spec: list[str] = []
    seen: set[str] = set()
    for constant in constants:
        if constant.value in seen:
            continue
        seen.add(constant.value)
        if constant.value not in documented:
            missing_readme.append(constant.value)
        if not is_listed_in_spec(constant.value, listing):
            missing_spec.append(constant.value)
    return ParityReport(
        constants=tuple(constants),
        missing_in_readme=tuple(missing_readme),
        missing_in_protobot_v2=tuple(missing_spec),
        raw_string_duplicates=tuple(duplicates),
        errors=tuple(errors),
    )


def format_report(report: ParityReport) -> list[str]:
    """Return stderr diagnostic lines for a failing *report*."""
    lines: list[str] = []
    for error in report.errors:
        lines.append(f"ERROR: {error}")
    locations: dict[str, list[str]] = {}
    for constant in report.constants:
        locations.setdefault(constant.value, []).append(
            f"{constant.name} at {constant.rel_path}:{constant.line}"
        )
    for code in report.missing_in_readme:
        where = ", ".join(locations.get(code, []))
        suffix = f" ({where})" if where else ""
        lines.append(
            "ERROR: diagnostic code "
            f"{code}{suffix} is not documented in {README_RELATIVE.as_posix()}"
        )
    for code in report.missing_in_protobot_v2:
        where = ", ".join(locations.get(code, []))
        suffix = f" ({where})" if where else ""
        lines.append(
            "ERROR: diagnostic code "
            f"{code}{suffix} is not listed in {SPEC_RELATIVE.as_posix()}"
        )
    for duplicate in report.raw_string_duplicates:
        lines.append(
            "ERROR: raw diagnostic string "
            f"{duplicate.value} in {duplicate.rel_path}:{duplicate.line} "
            f"duplicates exported constant {duplicate.constant_name}"
        )
    return lines


def check(repo_root: Path = REPO_ROOT) -> int:
    """Return 0 when diagnostic constants match README and ProtoBotV2.md."""
    report = evaluate(repo_root)
    if report.ok:
        print(
            f"Validated {len(report.constants)} diagnostic code(s) against "
            f"{README_RELATIVE.as_posix()} and {SPEC_RELATIVE.as_posix()}."
        )
        return 0
    for line in format_report(report):
        print(line, file=sys.stderr)
    return 1


def main(argv: list[str] | None = None) -> int:
    """Parse CLI arguments and check diagnostic-code documentation parity."""
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
