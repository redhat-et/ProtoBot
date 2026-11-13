#!/usr/bin/env python3
"""Validate pinned Fullsend harness URLs and optionally verify their hashes.

By default this checks local ``.fullsend/harness/*.yaml`` and
``.fullsend/harness/*.yml`` entries without network access. Pass ``--online``
to fetch each pinned raw file and compare its exact response bytes with the
recorded SHA-256 digest, or ``--online-if-ci`` to do so only when ``CI=true``.
"""

from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import os
import re
import sys
import urllib.request
from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path
from time import monotonic

REPO_ROOT = Path(__file__).resolve().parent.parent
HARNESS_DIRECTORY = Path(".fullsend") / "harness"
NETWORK_TIMEOUT_SECONDS = 10
NETWORK_READ_DEADLINE_SECONDS = 30
MAX_RESPONSE_BYTES = 2 * 1024 * 1024
RESPONSE_READ_CHUNK_BYTES = 64 * 1024

_BASE_KEY_CANDIDATE = re.compile(r"^(?P<key>base|\"base\"|'base')(?=[: \t]|$)")
_BASE_ENTRY = re.compile(r"base:[ \t]*(?P<value>.*)")
_PINNED_BASE_URL = re.compile(
    r"https://raw\.githubusercontent\.com/fullsend-ai/agents/"
    r"v[0-9]+\.[0-9]+\.[0-9]+"
    r"(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?/"
    r"(?P<path>[^?#]+)#sha256=(?P<sha256>[0-9a-f]{64})"
)
_SAFE_PATH_SEGMENT = re.compile(r"[A-Za-z0-9._-]+")


class HarnessIntegrityError(ValueError):
    """A harness file does not have one safe, pinned base URL."""


class _ResponseTooLargeError(ValueError):
    """A pinned base response exceeds the configured size limit."""


class _ResponseReadDeadlineError(TimeoutError):
    """Reading a pinned base response exceeded its total deadline."""


class _UnexpectedRedirectError(ValueError):
    """A redirect was rejected or the final response URL did not match."""


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Reject redirects before urllib can request their target URLs."""

    def redirect_request(
        self,
        request: urllib.request.Request,
        response: object,
        code: int,
        message: str,
        headers: object,
        new_url: str,
    ) -> urllib.request.Request | None:
        """Stop urllib from creating a request for a redirected URL."""
        raise _UnexpectedRedirectError(
            "redirect rejected while fetching pinned base URL"
        )


@dataclass(frozen=True)
class PinnedBase:
    fetch_url: str
    expected_sha256: str


def parse_pinned_base(value: str) -> PinnedBase:
    """Parse the strict Fullsend raw URL form and reject unsafe paths."""
    match = _PINNED_BASE_URL.fullmatch(value)
    if match is None:
        raise HarnessIntegrityError(
            "base must be an HTTPS raw.githubusercontent.com/fullsend-ai/agents/"
            "vX.Y.Z[-prerelease][+build]/<path> URL with "
            "#sha256=<64 lowercase hexadecimal characters>"
        )

    path = match.group("path")
    segments = path.split("/")
    if any(
        segment in {"", ".", ".."} or _SAFE_PATH_SEGMENT.fullmatch(segment) is None
        for segment in segments
    ):
        raise HarnessIntegrityError("base URL contains an unsafe repository path")

    return PinnedBase(
        fetch_url=value[: value.rfind("#")],
        expected_sha256=match.group("sha256"),
    )


def _check_quoted_suffix(suffix: str) -> None:
    """Require only whitespace after a quoted base scalar."""
    if suffix.strip():
        raise HarnessIntegrityError("base value must be one YAML scalar")


def _decode_base_scalar(raw_value: str) -> str:
    """Decode a one-line, double-quoted YAML string without a YAML dependency."""
    value = raw_value.strip()
    if not value:
        raise HarnessIntegrityError("base entry has no value")

    if not value.startswith('"'):
        raise HarnessIntegrityError("base value must be double-quoted")

    try:
        decoded, end = json.JSONDecoder().raw_decode(value)
    except json.JSONDecodeError as error:
        raise HarnessIntegrityError("malformed double-quoted base value") from error
    if not isinstance(decoded, str):
        raise HarnessIntegrityError("base value must be a string")
    if "\\" in value[:end]:
        raise HarnessIntegrityError("base URL must use literal characters")
    _check_quoted_suffix(value[end:])
    return decoded


def load_pinned_base(path: Path) -> PinnedBase:
    """Read one explicit top-level ``base`` scalar from a harness YAML file."""
    # shortcut: only simple one-line top-level scalars are parsed, upgrade if
    # the harness format permits flow-style or complex base nodes.
    try:
        content = path.read_text(encoding="utf-8-sig")
    except (OSError, UnicodeError) as error:
        raise HarnessIntegrityError(
            f"cannot read harness YAML ({type(error).__name__})"
        ) from None

    entries: list[tuple[int, str]] = []
    for line_number, line in enumerate(content.splitlines(), start=1):
        key_match = _BASE_KEY_CANDIDATE.match(line)
        if key_match is None:
            continue
        if key_match.group("key") != "base":
            raise HarnessIntegrityError(
                f"line {line_number}: top-level base key must be unquoted"
            )
        match = _BASE_ENTRY.fullmatch(line)
        if match is None:
            raise HarnessIntegrityError(
                f"line {line_number}: malformed top-level base entry"
            )
        entries.append((line_number, match.group("value")))

    if not entries:
        raise HarnessIntegrityError("missing top-level base entry")
    if len(entries) > 1:
        line_numbers = ", ".join(str(line) for line, _ in entries)
        raise HarnessIntegrityError(
            f"duplicate top-level base entries on lines {line_numbers}"
        )

    line_number, raw_value = entries[0]
    try:
        value = _decode_base_scalar(raw_value)
        return parse_pinned_base(value)
    except HarnessIntegrityError as error:
        raise HarnessIntegrityError(f"line {line_number}: {error}") from error


def _display_path(path: Path) -> str:
    try:
        return path.relative_to(REPO_ROOT).as_posix()
    except ValueError:
        return path.as_posix()


def _set_response_socket_timeout(response: object, timeout: float) -> None:
    """Keep each body read within the smaller per-operation/total timeout."""
    response_stream = getattr(response, "fp", None)
    raw_stream = getattr(response_stream, "raw", None)
    response_socket = getattr(raw_stream, "_sock", None)
    if response_socket is not None:
        response_socket.settimeout(timeout)


def _read_response_content(response: object) -> bytes:
    """Read a bounded response incrementally under a total time deadline."""
    deadline = monotonic() + NETWORK_READ_DEADLINE_SECONDS
    response_reader = getattr(response, "read1", None)
    if response_reader is None:
        response_reader = response.read

    chunks: list[bytes] = []
    size = 0
    while True:
        remaining = deadline - monotonic()
        if remaining <= 0:
            raise _ResponseReadDeadlineError(
                f"response read exceeded the {NETWORK_READ_DEADLINE_SECONDS}-second deadline"
            )
        _set_response_socket_timeout(
            response,
            min(NETWORK_TIMEOUT_SECONDS, remaining),
        )
        chunk = response_reader(
            min(RESPONSE_READ_CHUNK_BYTES, MAX_RESPONSE_BYTES + 1 - size)
        )
        if monotonic() > deadline:
            raise _ResponseReadDeadlineError(
                f"response read exceeded the {NETWORK_READ_DEADLINE_SECONDS}-second deadline"
            )
        if not chunk:
            break
        size += len(chunk)
        if size > MAX_RESPONSE_BYTES:
            raise _ResponseTooLargeError(
                f"pinned base response exceeds the {MAX_RESPONSE_BYTES}-byte limit"
            )
        chunks.append(chunk)
    return b"".join(chunks)


def _response_url(response: object) -> str | None:
    """Return the final URL when the response exposes it."""
    geturl = getattr(response, "geturl", None)
    if callable(geturl):
        url = geturl()
    else:
        url = getattr(response, "url", None)
    return url if isinstance(url, str) else None


def check_harness_file(path: Path, online: bool = False) -> list[str]:
    """Return file-specific diagnostics for one harness file."""
    display_path = _display_path(path)
    try:
        pinned_base = load_pinned_base(path)
    except HarnessIntegrityError as error:
        return [f"{display_path}: {error}"]

    if not online:
        return []

    try:
        opener = urllib.request.build_opener(_NoRedirectHandler())
        with opener.open(
            pinned_base.fetch_url,
            timeout=NETWORK_TIMEOUT_SECONDS,
        ) as response:
            response_url = _response_url(response)
            if response_url != pinned_base.fetch_url:
                raise _UnexpectedRedirectError(
                    "final response URL is missing or differs from pinned base URL"
                )
            content = _read_response_content(response)
    except _UnexpectedRedirectError as error:
        return [f"{display_path}: {error}"]
    except _ResponseTooLargeError as error:
        return [f"{display_path}: {error}"]
    except _ResponseReadDeadlineError as error:
        return [f"{display_path}: {error}"]
    except TimeoutError:
        return [f"{display_path}: timed out while fetching pinned base URL"]
    except (OSError, http.client.HTTPException):
        return [f"{display_path}: failed to fetch pinned base URL"]

    actual_sha256 = hashlib.sha256(content).hexdigest()
    if actual_sha256 != pinned_base.expected_sha256:
        diagnostic = (
            f"{display_path}: SHA-256 mismatch for pinned base URL "
            f"(expected {pinned_base.expected_sha256}, got {actual_sha256})"
        )
        return [diagnostic]
    return []


def discover_harness_files(repo_root: Path = REPO_ROOT) -> list[Path]:
    """Return YAML harness files directly inside the configured directory."""
    directory = repo_root / HARNESS_DIRECTORY
    return sorted((*directory.glob("*.yaml"), *directory.glob("*.yml")))


def check_harness_files(files: Iterable[Path], online: bool = False) -> list[str]:
    """Validate each harness file and collect all diagnostics."""
    diagnostics: list[str] = []
    for path in sorted(files):
        diagnostics.extend(check_harness_file(path, online=online))
    return diagnostics


def _ci_is_enabled() -> bool:
    """Return whether the environment explicitly enables online CI checks."""
    return os.environ.get("CI", "").strip().casefold() == "true"


def main(argv: list[str] | None = None) -> int:
    """Validate all configured harness files using the selected mode."""
    parser = argparse.ArgumentParser(description=__doc__)
    online_group = parser.add_mutually_exclusive_group()
    online_group.add_argument(
        "--online",
        action="store_true",
        help="fetch each pinned raw file and verify its exact SHA-256 digest",
    )
    online_group.add_argument(
        "--online-if-ci",
        action="store_true",
        help="verify hashes online only when CI is set to true",
    )
    args = parser.parse_args(argv)
    online = args.online or (args.online_if_ci and _ci_is_enabled())

    files = discover_harness_files(REPO_ROOT)
    if not files:
        print(
            f"ERROR: no YAML harness files found in {HARNESS_DIRECTORY}",
            file=sys.stderr,
        )
        return 1

    diagnostics = check_harness_files(files, online=online)
    if diagnostics:
        for diagnostic in diagnostics:
            print(f"ERROR: {diagnostic}", file=sys.stderr)
        return 1

    validation_mode = "online integrity" if online else "offline structure"
    print(f"Validated {len(files)} harness file(s) ({validation_mode}).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
