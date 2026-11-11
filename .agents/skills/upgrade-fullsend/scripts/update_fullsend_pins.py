#!/usr/bin/env python3
"""Update Fullsend harness hashes and reusable workflow commit pins."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import stat
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

AGENTS_RAW_BASE = "https://raw.githubusercontent.com/fullsend-ai/agents"
FULLSEND_REPOSITORY = "fullsend-ai/fullsend"
FULLSEND_GIT_URL = "https://github.com/fullsend-ai/fullsend.git"
VERSION_PATTERN = re.compile(
    r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?"
)
HARNESS_PIN_PATTERN = re.compile(
    r"https://raw\.githubusercontent\.com/fullsend-ai/agents/"
    r"v[^/\s\"]+/(?P<path>harness/[^#\s\"]+\.ya?ml)"
    r"#sha256=(?P<sha>[0-9A-Fa-f]{64})"
)
WORKFLOW_PIN_PATTERN = re.compile(
    r"(?P<quote>['\"]?)"
    r"(?P<prefix>fullsend-ai/fullsend/\.github/workflows/"
    r"reusable-[A-Za-z0-9_.-]+\.ya?ml@)"
    r"(?P<sha>[0-9A-Fa-f]{40})"
    r"(?P=quote)"
    r"(?:(?P<sep>[ \t]+#[ \t]*)(?P<tag>v[^\s]+))?"
)


class UpdateError(RuntimeError):
    """A user-actionable failure while resolving or preparing pins."""


@dataclass(frozen=True)
class Replacement:
    path: Path
    old: str
    new: str


@dataclass(frozen=True)
class FilePlan:
    path: Path
    original: bytes
    updated: bytes
    replacements: tuple[Replacement, ...]


def normalize_version(value: str) -> str:
    version = value if value.startswith("v") else f"v{value}"
    if not VERSION_PATTERN.fullmatch(version):
        raise UpdateError(
            f"invalid version {value!r}; expected a release such as v0.44.0"
        )
    return version


def fetch_bytes(url: str, timeout: float) -> bytes:
    request = Request(url, headers={"User-Agent": "upgrade-fullsend"})
    try:
        with urlopen(request, timeout=timeout) as response:
            return response.read()
    except HTTPError as exc:
        raise UpdateError(f"failed to fetch {url}: HTTP {exc.code}") from exc
    except URLError as exc:
        raise UpdateError(f"failed to fetch {url}: {exc.reason}") from exc
    except OSError as exc:
        raise UpdateError(f"failed to fetch {url}: {exc}") from exc


def resolve_workflow_sha(version: str, timeout: float) -> str:
    tag_ref = f"refs/tags/{version}"
    peeled_ref = f"{tag_ref}^{{}}"
    try:
        result = subprocess.run(
            ["git", "ls-remote", FULLSEND_GIT_URL, tag_ref, peeled_ref],
            check=True,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except FileNotFoundError as exc:
        raise UpdateError(
            "git is required to resolve the Fullsend release tag"
        ) from exc
    except subprocess.CalledProcessError as exc:
        detail = exc.stderr.strip() or "unknown git error"
        raise UpdateError(
            f"failed to resolve {version} from {FULLSEND_GIT_URL}: {detail}"
        ) from exc
    except subprocess.TimeoutExpired as exc:
        raise UpdateError(
            f"timed out resolving {version} from {FULLSEND_GIT_URL}"
        ) from exc

    tag_sha: str | None = None
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) != 2:
            continue
        sha, ref = fields
        if not re.fullmatch(r"[0-9a-fA-F]{40}", sha):
            continue
        if ref == peeled_ref:
            return sha.lower()
        if ref == tag_ref:
            tag_sha = sha.lower()

    if tag_sha is None:
        raise UpdateError(
            f"Fullsend release tag {version} was not found at {FULLSEND_GIT_URL}"
        )
    return tag_sha


def resolve_target(root: Path, raw_path: str) -> Path:
    path = Path(raw_path)
    if not path.is_absolute():
        path = root / path
    path = path.resolve()
    try:
        path.relative_to(root)
    except ValueError as exc:
        raise UpdateError(f"target path escapes repository root: {raw_path}") from exc
    if not path.is_file():
        raise UpdateError(f"target file does not exist: {path}")
    return path


def discover_targets(
    root: Path, explicit: Sequence[str] | None, kind: str
) -> list[Path]:
    if explicit:
        return sorted({resolve_target(root, value) for value in explicit})

    if kind == "harness":
        directory = root / ".fullsend" / "harness"
        pattern = "*.y*ml"
    else:
        directory = root / ".github" / "workflows"
        pattern = "*.y*ml"

    if not directory.is_dir():
        raise UpdateError(f"could not find Fullsend {kind} directory: {directory}")
    resolved_directory = directory.resolve()
    try:
        resolved_directory.relative_to(root)
    except ValueError as exc:
        raise UpdateError(
            f"target directory escapes repository root: {directory}"
        ) from exc
    return sorted(
        {
            resolve_target(root, str(path))
            for path in resolved_directory.glob(pattern)
            if path.is_file()
        }
    )


def read_utf8(path: Path) -> tuple[bytes, str]:
    original = path.read_bytes()
    try:
        return original, original.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise UpdateError(f"target is not valid UTF-8: {path}") from exc


def update_harness_file(
    path: Path,
    version: str,
    digest_cache: dict[str, str],
    timeout: float,
) -> FilePlan | None:
    original, text = read_utf8(path)
    matches = list(HARNESS_PIN_PATTERN.finditer(text))
    if not matches:
        return None

    updated_text = text
    replacements: list[Replacement] = []
    for match in reversed(matches):
        relative_path = match.group("path")
        if not re.fullmatch(r"harness/[A-Za-z0-9_.-]+\.ya?ml", relative_path):
            raise UpdateError(
                f"unsafe harness path {relative_path!r}; "
                "expected a YAML filename directly below harness/"
            )
        url = f"{AGENTS_RAW_BASE}/{version}/{relative_path}"
        if relative_path not in digest_cache:
            digest_cache[relative_path] = hashlib.sha256(
                fetch_bytes(url, timeout)
            ).hexdigest()
        new_pin = f"{url}#sha256={digest_cache[relative_path]}"
        old_pin = match.group(0)
        if old_pin == new_pin:
            continue
        updated_text = (
            updated_text[: match.start()] + new_pin + updated_text[match.end() :]
        )
        replacements.append(Replacement(path, old_pin, new_pin))

    if not replacements:
        return None
    return FilePlan(path, original, updated_text.encode("utf-8"), tuple(replacements))


def update_workflow_file(
    path: Path,
    version: str,
    workflow_sha: str,
) -> FilePlan | None:
    original, text = read_utf8(path)
    matches = list(WORKFLOW_PIN_PATTERN.finditer(text))
    if not matches:
        return None

    updated_text = text
    replacements: list[Replacement] = []
    for match in reversed(matches):
        old_pin = match.group(0)
        if (
            match.group("sha").lower() == workflow_sha.lower()
            and match.group("tag") == version
        ):
            continue
        separator = match.group("sep") or " # "
        quote = match.group("quote")
        new_pin = (
            f"{quote}{match.group('prefix')}{workflow_sha}{quote}{separator}{version}"
        )
        updated_text = (
            updated_text[: match.start()] + new_pin + updated_text[match.end() :]
        )
        replacements.append(Replacement(path, old_pin, new_pin))

    if not replacements:
        return None
    return FilePlan(path, original, updated_text.encode("utf-8"), tuple(replacements))


def atomic_write(path: Path, content: bytes) -> None:
    mode = stat.S_IMODE(path.stat().st_mode)
    file_descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", dir=str(path.parent)
    )
    try:
        with os.fdopen(file_descriptor, "wb") as temporary_file:
            temporary_file.write(content)
            temporary_file.flush()
            os.fsync(temporary_file.fileno())
        os.chmod(temporary_name, mode)
        os.replace(temporary_name, path)
    except BaseException:
        try:
            os.unlink(temporary_name)
        except FileNotFoundError:
            pass
        raise


def relative_path(path: Path, root: Path) -> str:
    return path.relative_to(root).as_posix()


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Update Fullsend harness hashes and reusable workflow commit pins."
    )
    parser.add_argument(
        "--version",
        required=True,
        help="Fullsend release version, for example v0.44.0",
    )
    parser.add_argument(
        "--root",
        type=Path,
        default=Path.cwd(),
        help="repository root (default: current directory)",
    )
    parser.add_argument(
        "--write",
        action="store_true",
        help="write the resolved pins; otherwise print a dry-run plan",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail if any pin is stale without modifying files",
    )
    parser.add_argument(
        "--harness-file",
        action="append",
        metavar="PATH",
        help="explicit harness file; may be repeated",
    )
    parser.add_argument(
        "--workflow-file",
        action="append",
        metavar="PATH",
        help="explicit workflow file; may be repeated",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=30.0,
        help="network and git timeout in seconds (default: 30)",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.write and args.check:
        parser.error("--write and --check cannot be used together")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")

    root = args.root.resolve()
    if not root.is_dir():
        parser.error(f"repository root is not a directory: {root}")

    try:
        version = normalize_version(args.version)
        harness_files = discover_targets(root, args.harness_file, "harness")
        workflow_files = discover_targets(root, args.workflow_file, "workflow")
        digest_cache: dict[str, str] = {}
        plans: list[FilePlan] = []
        harness_match_count = 0
        workflow_match_count = 0

        for path in harness_files:
            _, text = read_utf8(path)
            harness_match_count += len(list(HARNESS_PIN_PATTERN.finditer(text)))
            plan = update_harness_file(path, version, digest_cache, args.timeout)
            if plan is not None:
                plans.append(plan)

        for path in workflow_files:
            _, text = read_utf8(path)
            workflow_match_count += len(list(WORKFLOW_PIN_PATTERN.finditer(text)))

        if harness_match_count == 0:
            raise UpdateError(
                "no Fullsend agents harness pins found; pass --harness-file for a non-standard layout"
            )
        if workflow_match_count == 0:
            raise UpdateError(
                "no Fullsend reusable workflow pins found; pass --workflow-file for a non-standard layout"
            )

        workflow_sha = resolve_workflow_sha(version, args.timeout)
        for path in workflow_files:
            plan = update_workflow_file(path, version, workflow_sha)
            if plan is not None:
                plans.append(plan)
    except (OSError, UpdateError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    print(f"Fullsend target: {version}")
    print(f"Reusable workflow commit: {workflow_sha}")
    if plans:
        for plan in plans:
            for replacement in plan.replacements:
                print(
                    f"{relative_path(plan.path, root)}: "
                    f"{replacement.old} -> {replacement.new}"
                )
    else:
        print("All discovered Fullsend pins already match the target release.")
        return 0

    if args.check:
        print("Fullsend pins are stale.", file=sys.stderr)
        return 1
    if not args.write:
        print("Dry run only; rerun with --write to apply these updates.")
        return 0

    for plan in plans:
        atomic_write(plan.path, plan.updated)
    print(f"Updated {len(plans)} file(s).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
