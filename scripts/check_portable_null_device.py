#!/usr/bin/env python3
"""Run the Go AST null-device checker on the given Go files.

Used as the ``check-portable-null-device`` local pre-commit hook (and via
``scripts/lint.py``). The checker itself lives in ``scripts/nulldevice``
and uses only the Go standard library (``go/parser``, ``go/ast``), so it
runs offline. This wrapper passes absolute file paths, because the Go tool
runs from its own module directory, and keeps repository-relative paths
out of ``go run`` argument parsing.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
TOOL_DIR = REPO_ROOT / "scripts" / "nulldevice"


def main(argv: list[str] | None = None) -> int:
    files = [str(Path(f).resolve()) for f in (sys.argv[1:] if argv is None else argv)]
    if not files:
        return 0
    env = {**os.environ, "GOWORK": "off"}
    result = subprocess.run(
        ["go", "run", "-C", str(TOOL_DIR), ".", *files],
        env=env,
        check=False,
    )
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
