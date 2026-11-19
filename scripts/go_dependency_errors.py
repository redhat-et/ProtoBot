"""Shared detection of Go module-dependency failures for offline hooks."""

from __future__ import annotations

DEPENDENCY_ERROR_INDICATORS = (
    "module lookup disabled",
    "cannot find module providing package",
    "no required module provides package",
    "missing go.sum entry",
    "dial tcp",
    "connection refused",
    "no such host",
    "i/o timeout",
    "tls handshake timeout",
    "server response:",
)


def is_dependency_error(output: str) -> bool:
    """Return True if *output* indicates dependency resolution / cache miss failure."""
    lower = output.lower()
    if any(indicator in lower for indicator in DEPENDENCY_ERROR_INDICATORS):
        return True
    return "go: downloading" in output and any(
        kw in lower for kw in ("error", "timeout", "failed")
    )
