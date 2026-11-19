#!/usr/bin/env python3
"""Fail if a non-test Go source file hardcodes the Unix null device.

Used as the ``check-portable-null-device`` local pre-commit hook (and via
``scripts/lint.py``). The literal path ``"/dev/null"`` is not portable:
Windows exposes the null device as ``NUL``, not ``/dev/null``. Go's
standard library already provides ``os.DevNull`` for platform-independent
null-device redirection, so a non-test Go source file that uses the
literal string ``/dev/null`` as an actual OS path (an environment
variable value, a git config flag, or a command argument) should use
``os.DevNull`` instead.

Comparisons against the literal (``== "/dev/null"`` or ``!= "/dev/null"``)
and switch ``case`` clauses matching the literal are exempt: they match
``/dev/null`` as a cross-platform *text convention* rather than use it as
an OS path this code opens, redirects to, or forwards to a child process.
For example, ``drafting-table/writes.go`` recognizes ``/dev/null`` as the
unified diff/patch "no file" marker, and ``drafting-table/shell_analysis.go``
detects a ``bash --rcfile /dev/null`` invocation by comparing against
the literal argument text another program received.

Inline suppression is supported via ``// portability:allow-null-device``
on the violating line or the line immediately preceding it.

Text-matching calls such as ``strings.HasSuffix``, ``strings.HasPrefix``,
``strings.Contains``, ``strings.TrimPrefix``, and ``strings.TrimSuffix``
that pass the literal argument are not syntactically exempt and will trip
the check; the ``// portability:allow-null-device`` pragma is the escape
hatch when matching text conventions in such calls.

Comments (line comments ``//`` and block comments ``/* ... */``) and
test files (``*_test.go``) are exempt entirely: mentions of ``/dev/null``
in documentation or test fixtures do not affect production portability.
"""

from __future__ import annotations

import argparse
import re
import string
import sys

_PATH_CHARS_BEFORE = set(string.ascii_letters + string.digits + "_./-")
_PATH_CHARS_AFTER = set(string.ascii_letters + string.digits + "_-")

_LITERAL_PATTERN = (
    r"(?:"
    r'"(?:[^"\\]|\\.)*?/dev/null(?:[^"\\]|\\.)*"'
    r"|"
    r"`[^`]*?/dev/null[^`]*`"
    r")"
)
_NULL_DEVICE_LITERAL = re.compile(_LITERAL_PATTERN)
_COMPARISON_EXEMPTION = re.compile(
    rf"(?:(==|!=)\s*{_LITERAL_PATTERN}|{_LITERAL_PATTERN}\s*(==|!=))"
)
_CASE_EXEMPTION = re.compile(
    rf"\bcase\s+(?![^:]*<-)[^:]*?({_LITERAL_PATTERN})[^:]*:"
)
_PRAGMA_PATTERN = re.compile(r"(?://|/\*)\s*portability:allow-null-device")


def _null_device_at_boundary(text: str, idx: int) -> bool:
    """Return True when ``/dev/null`` at *idx* in *text* is at a path boundary."""
    if idx > 0 and text[idx - 1] in _PATH_CHARS_BEFORE:
        return False
    after_idx = idx + len("/dev/null")
    if after_idx < len(text) and text[after_idx] in _PATH_CHARS_AFTER:
        return False
    return True


def is_test_file(path: str) -> bool:
    """Return True when *path* is a Go test file (``*_test.go``)."""
    return path.endswith("_test.go")


def strip_comments(text: str) -> str:
    """Return *text* with Go line and block comments replaced with spaces.

    Newlines and non-comment character offsets are preserved so line
    numbers and column alignments remain identical to the original text.
    Comment characters occurring inside interpreted strings, raw
    strings, or rune literals are not modified.
    """
    result: list[str] = []
    i = 0
    n = len(text)
    state = "NORMAL"

    while i < n:
        c = text[i]
        c2 = text[i : i + 2]

        if state == "NORMAL":
            if c2 == "//":
                state = "LINE_COMMENT"
                result.append("  ")
                i += 2
            elif c2 == "/*":
                state = "BLOCK_COMMENT"
                result.append("  ")
                i += 2
            elif c == '"':
                state = "DOUBLE_QUOTE"
                result.append(c)
                i += 1
            elif c == "`":
                state = "RAW_STRING"
                result.append(c)
                i += 1
            elif c == "'":
                state = "RUNE"
                result.append(c)
                i += 1
            else:
                result.append(c)
                i += 1
        elif state == "DOUBLE_QUOTE":
            result.append(c)
            if c == "\\":
                if i + 1 < n:
                    result.append(text[i + 1])
                    i += 2
                else:
                    i += 1
            elif c == '"':
                state = "NORMAL"
                i += 1
            else:
                i += 1
        elif state == "RAW_STRING":
            result.append(c)
            if c == "`":
                state = "NORMAL"
                i += 1
            else:
                i += 1
        elif state == "RUNE":
            result.append(c)
            if c == "\\":
                if i + 1 < n:
                    result.append(text[i + 1])
                    i += 2
                else:
                    i += 1
            elif c == "'":
                state = "NORMAL"
                i += 1
            else:
                i += 1
        elif state == "LINE_COMMENT":
            if c == "\n":
                state = "NORMAL"
                result.append("\n")
            else:
                result.append(" ")
            i += 1
        elif state == "BLOCK_COMMENT":
            if c2 == "*/":
                state = "NORMAL"
                result.append("  ")
                i += 2
            elif c == "\n":
                result.append("\n")
                i += 1
            else:
                result.append(" ")
                i += 1

    return "".join(result)


def find_violations(text: str) -> list[tuple[int, str]]:
    """Return ``(line_number, line_text)`` for each non-portable use.

    *text* is the full contents of one Go source file. A line is a
    violation when it contains a quoted string literal (interpreted or
    raw) embedding ``/dev/null`` at a path boundary, unless every such
    occurrence is part of an ``==``/``!=`` comparison, a switch ``case``
    value, or suppressed by ``portability:allow-null-device``.
    """
    original_lines = text.splitlines()

    # Identify lines suppressed by the pragma on the same or preceding line.
    suppressed_lines: set[int] = set()
    for line_number, line in enumerate(original_lines, start=1):
        if _PRAGMA_PATTERN.search(line):
            suppressed_lines.add(line_number)
            suppressed_lines.add(line_number + 1)

    # Strip line and block comments while preserving character offsets.
    clean_text = strip_comments(text)

    # Blank out comparisons (== and !=), including those wrapped across lines.
    def repl_comparison(m: re.Match[str]) -> str:
        return re.sub(r"[^\n]", " ", m.group(0))

    prev: str | None = None
    while prev != clean_text:
        prev = clean_text
        clean_text = _COMPARISON_EXEMPTION.sub(repl_comparison, clean_text)

    # Blank out switch case expressions matching /dev/null.
    def repl_case(m: re.Match[str]) -> str:
        full = m.group(0)
        lit = m.group(1)
        lit_start = m.start(1) - m.start(0)
        lit_end = m.end(1) - m.start(0)
        return full[:lit_start] + re.sub(r"[^\n]", " ", lit) + full[lit_end:]

    prev = None
    while prev != clean_text:
        prev = clean_text
        clean_text = _CASE_EXEMPTION.sub(repl_case, clean_text)

    # Detect remaining violations.
    violations: list[tuple[int, str]] = []
    seen_lines: set[int] = set()
    for m in _NULL_DEVICE_LITERAL.finditer(clean_text):
        pos = m.start()
        while True:
            null_idx = clean_text.find("/dev/null", pos, m.end())
            if null_idx == -1:
                break
            if _null_device_at_boundary(clean_text, null_idx):
                line_number = clean_text[:null_idx].count("\n") + 1
                if (
                    line_number not in suppressed_lines
                    and line_number not in seen_lines
                ):
                    seen_lines.add(line_number)
                    line_text = (
                        original_lines[line_number - 1].strip()
                        if line_number <= len(original_lines)
                        else ""
                    )
                    violations.append((line_number, line_text))
            pos = null_idx + len("/dev/null")

    return sorted(violations, key=lambda v: v[0])


def check(files: list[str]) -> int:
    """Check each given Go file and return 0 only when none violate."""
    failed = False
    for path in files:
        if not path.endswith(".go") or is_test_file(path):
            continue
        try:
            with open(path, encoding="utf-8") as fh:
                text = fh.read()
        except (OSError, UnicodeError) as error:
            print(f"ERROR: cannot read {path}: {error}", file=sys.stderr)
            failed = True
            continue
        for line_number, line in find_violations(text):
            print(
                f'{path}:{line_number}: hardcoded "/dev/null" is not '
                'portable (Windows uses "NUL"); use os.DevNull instead: '
                f"{line}"
            )
            failed = True
    return 1 if failed else 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "files",
        nargs="*",
        help="Go source files (paths relative to the repository root)",
    )
    args = parser.parse_args(argv)
    return check(args.files)


if __name__ == "__main__":
    sys.exit(main())
