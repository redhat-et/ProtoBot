#!/usr/bin/env python3
"""Run ``go vet ./...`` in each Go module that contains given files.

Used as the ``go-vet`` local pre-commit hook (and via ``scripts/lint.py``)
so vet findings fail in the same change. Filenames select the affected
modules; vet itself always runs as ``go vet ./...`` in the module
directory, matching CI. Uses ``-mod=vendor`` when a vendor directory is
present, and distinguishes offline module dependency resolution errors
from vet diagnostics.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path

try:
    from go_dependency_errors import is_dependency_error as _is_dependency_error
except ImportError:  # imported as ``scripts.<module>``
    from scripts.go_dependency_errors import (
        is_dependency_error as _is_dependency_error,
    )

REPO_ROOT = Path(__file__).resolve().parent.parent


def find_go_module(repo_root: Path, file_path: Path) -> Path | None:
    """Return the directory containing ``go.mod`` for *file_path*, if any.

    Walks from the file's directory toward *repo_root* and returns the
    nearest ancestor (or the directory itself) that contains ``go.mod``.
    Paths outside *repo_root* yield ``None``.
    """
    try:
        resolved = file_path.resolve()
        root = repo_root.resolve()
    except OSError:
        return None
    if not resolved.is_relative_to(root):
        return None
    start = resolved if resolved.is_dir() else resolved.parent
    for candidate in [start, *start.parents]:
        if (candidate / "go.mod").is_file():
            return candidate
        if candidate == root:
            break
    return None


def _display_module(module: Path, repo_root: Path) -> str:
    try:
        return str(module.resolve().relative_to(repo_root.resolve()))
    except ValueError:
        return str(module)


def _build_vet_command(module: Path) -> list[str]:
    """Construct the ``go vet`` command for *module*."""
    cmd = ["go", "vet"]
    if (module / "vendor").is_dir():
        cmd.append("-mod=vendor")
    cmd.append("./...")
    return cmd


def check(files: list[str], repo_root: Path = REPO_ROOT) -> int:
    """Vet each unique Go module that contains a given ``.go`` file."""
    modules: set[Path] = set()
    for raw in files:
        if not raw.endswith(".go"):
            continue
        path = Path(raw)
        if not path.is_absolute():
            path = repo_root / path
        module = find_go_module(repo_root, path)
        if module is not None:
            modules.add(module)
    if not modules:
        return 0

    failed = False
    for module in sorted(modules):
        cmd = _build_vet_command(module)
        env = os.environ.copy()
        env["GOWORK"] = "off"
        try:
            result = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                cwd=str(module),
                env=env,
                check=False,
            )
        except FileNotFoundError:
            print("ERROR: go not found on PATH", file=sys.stderr)
            return 1
        if result.returncode != 0:
            display = _display_module(module, repo_root)
            output = (result.stdout + result.stderr).strip()
            if _is_dependency_error(output):
                print(
                    f"go vet dependency resolution failed in {display} "
                    "(module download/cache miss):"
                )
                print(
                    "  Module dependencies could not be resolved offline. "
                    "Ensure GOMODCACHE is populated or vendor/ is present."
                )
            else:
                print(f"go vet failed in {display}:")
            if output:
                print(output)
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
