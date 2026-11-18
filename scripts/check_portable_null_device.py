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
are exempt: they match ``/dev/null`` as a cross-platform *text
convention* rather than use it as an OS path this code opens, redirects
to, or forwards to a child process. For example,
``drafting-table/writes.go`` recognizes ``/dev/null`` as the unified
diff/patch "no file" marker, and ``drafting-table/shell_analysis.go``
detects a ``bash --rcfile /dev/null`` invocation by comparing against
the literal argument text another program received.

Test files (``*_test.go``) are exempt entirely: null-device assumptions
in test fixtures do not affect production portability.
"""

from __future__ import annotations

import argparse
import re
import sys

_NULL_DEVICE_LITERAL = re.compile(r'"[^"]*/dev/null[^"]*"')
_COMPARISON_EXEMPTION = re.compile(r'(==|!=)\s*"[^"]*/dev/null[^"]*"')


def is_test_file(path: str) -> bool:
    """Return True when *path* is a Go test file (``*_test.go``)."""
    return path.endswith("_test.go")


def find_violations(text: str) -> list[tuple[int, str]]:
    """Return ``(line_number, line_text)`` for each non-portable use.

    *text* is the full contents of one Go source file. A line is a
    violation when it contains a quoted string literal that embeds
    ``/dev/null``, unless every such occurrence on the line is the
    right-hand side of an ``==``/``!=`` comparison (see module
    docstring for why comparisons are exempt).
    """
    violations: list[tuple[int, str]] = []
    for line_number, line in enumerate(text.splitlines(), start=1):
        if not _NULL_DEVICE_LITERAL.search(line):
            continue
        remaining = _COMPARISON_EXEMPTION.sub("", line)
        if _NULL_DEVICE_LITERAL.search(remaining):
            violations.append((line_number, line.strip()))
    return violations


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
