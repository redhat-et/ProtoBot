#!/usr/bin/env python3
"""Seed the offline ``pre-commit`` hook cache for Fullsend agent sandboxes.

Fullsend agent sandboxes have no network egress, so ``scripts/lint.py``
(the validation entrypoint ``AGENTS.md`` prescribes) needs a pre-seeded
``PRE_COMMIT_HOME``. This script runs on the trusted runner, which has
network access, in two steps:

1. Run the pinned upstream Fullsend pre-script unchanged, so its input
   validation and exit-78 skip semantics survive. A local harness
   ``pre_script`` replaces the base harness value instead of chaining,
   so the wrapper must delegate itself.
2. Warm the hook cache and write it to a tarball. ``host_files`` copies
   the tarball into the sandbox, where ``scripts/lint.py`` restores it
   on the first offline run.

Path contract: hook virtualenvs embed absolute paths, so the cache must
be built at the exact path the sandbox will unpack it to. The default
cache home ``/sandbox/.cache/pre-commit`` matches ``PRE_COMMIT_HOME`` in
``.fullsend/harness/code.yaml`` and ``fix.yaml``, and the default
tarball path ``/tmp/workspace/pre-commit-cache.tar.gz`` matches the
``host_files`` entry that copies it to
``/tmp/protobot-pre-commit-cache.tar.gz`` in the sandbox.

Version pins: ``AGENTS_REF`` must equal the ``vX.Y.Z`` in every
``.fullsend/harness/*.yaml`` ``base:`` URL (checked offline by
``scripts/test_pre_commit_cache.py``), and ``PRECOMMIT_VERSION`` must
equal ``PRECOMMIT_VERSION`` in fullsend's ``images/sandbox/Containerfile``
— the version the sandbox installs. A mismatch on either pin breaks the
cache, because hook environments and ``db.db`` record exact versions.
``UPSTREAM_SCRIPT_SHA256`` pins the exact bytes of each upstream
pre-script at ``AGENTS_REF``; a moved tag or a changed file is a hard
failure, never unverified script content. ``SANDBOX_PYTHON_VERSION`` is
the sandbox image's interpreter; the bootstrap refuses to publish an
archive whose python hook environments were built for another
interpreter.

Step 2 is fail-soft: a bootstrap failure prints a GitHub Actions warning
and exits 0, so it never blocks the agent run. ``scripts/lint.py``
still reports its actionable cache-miss error.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
import tempfile
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

# Pin of the fullsend-ai/agents repository. Keep in sync with the
# ``base:`` URLs in .fullsend/harness/*.yaml.
AGENTS_REF = "v0.56.1"
AGENTS_RAW_BASE = f"https://raw.githubusercontent.com/fullsend-ai/agents/{AGENTS_REF}"
UPSTREAM_SCRIPTS = {
    "code": "scripts/pre-code.sh",
    "fix": "scripts/pre-fix.sh",
}

# SHA-256 of each pinned upstream pre-script at AGENTS_REF. When a value
# stops matching, the tag moved or the file changed; re-verify the new
# content before updating the pin.
UPSTREAM_SCRIPT_SHA256 = {
    "code": "cb9fcec5ca9b3c7e2004aaeb24b4453e2435e094da4c801c851ef29604495278",  # pragma: allowlist secret
    "fix": "8457b72882bad5b79392d16d72e67e2215e8a040d7e6e875e441272d858a931f",  # pragma: allowlist secret
}

# Must equal PRECOMMIT_VERSION in fullsend's images/sandbox/Containerfile.
PRECOMMIT_VERSION = "4.5.1"

# The Ubuntu Noble interpreter in the Fullsend sandbox image. Python
# hook environments embed the interpreter's path, so the cache must be
# built for this version or the sandbox cannot run them.
SANDBOX_PYTHON_VERSION = "3.12"

DEFAULT_CACHE_HOME = "/sandbox/.cache/pre-commit"
DEFAULT_TARBALL = "/tmp/workspace/pre-commit-cache.tar.gz"

DOWNLOAD_TIMEOUT_SECONDS = 60
VERSION_TIMEOUT_SECONDS = 60
PIP_TIMEOUT_SECONDS = 10 * 60
INSTALL_HOOKS_TIMEOUT_SECONDS = 15 * 60
SUDO_TIMEOUT_SECONDS = 60
MAX_SCRIPT_BYTES = 2 * 1024 * 1024

# A GitHub Actions workflow command must stay on one line, cannot carry
# control characters, and must not expand ``%``-encoded data.
_CONTROL_CHARACTERS = re.compile(r"[\x00-\x1f\x7f]")
_WHITESPACE_RUNS = re.compile(r"\s+")
_WORKFLOW_COMMAND_CAP = 500


def _sanitize_workflow_command(text: str) -> str:
    """Make *text* safe for one GitHub Actions workflow command line.

    Removes control characters (including CR, LF, and ESC), collapses
    whitespace runs so nothing can start a new command, escapes ``%`` as
    ``%25`` so GitHub cannot expand encoded data, breaks the legacy
    ``##[`` command marker with a zero-width space, and caps the length.
    """
    without_controls = _CONTROL_CHARACTERS.sub(" ", text)
    collapsed = _WHITESPACE_RUNS.sub(" ", without_controls).strip()
    escaped = collapsed.replace("%", "%25")
    return escaped.replace("##[", "##\u200b[")[:_WORKFLOW_COMMAND_CAP]


class UpstreamScriptError(OSError):
    """The pinned upstream pre-script could not be fetched or verified."""


class _UnexpectedRedirectError(ValueError):
    """A redirect was rejected while fetching a pinned URL."""


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Reject redirects so a moved pin cannot change the fetch target."""

    def redirect_request(
        self,
        request: object,
        response: object,
        code: int,
        message: str,
        headers: object,
        new_url: str,
    ) -> urllib.request.Request | None:
        raise _UnexpectedRedirectError("redirect rejected while fetching a pinned URL")


def _run(
    argv: list[str],
    *,
    env: dict[str, str] | None = None,
    timeout: int | None = None,
    capture: bool = False,
    cwd: str | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run one external command. Module-level so tests can intercept it."""
    return subprocess.run(
        argv,
        env=env,
        timeout=timeout,
        capture_output=capture,
        text=capture,
        cwd=cwd,
        check=False,
    )


def _tail(text: str, lines: int = 10) -> str:
    """Return the last non-empty *lines* of *text*, for error messages."""
    return "\n".join(text.strip().splitlines()[-lines:])


def _run_or_raise(
    argv: list[str],
    *,
    description: str,
    env: dict[str, str] | None = None,
    timeout: int | None = None,
    cwd: str | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run one external command and raise ``RuntimeError`` on failure.

    Output is captured so the raised message can quote the command's own
    stderr, which is usually the real reason for the failure.
    """
    result = _run(argv, env=env, timeout=timeout, cwd=cwd, capture=True)
    if result.returncode != 0:
        message = f"{description} (exit {result.returncode})"
        detail = _tail(result.stderr)
        if detail:
            message = f"{message}\n{detail}"
        raise RuntimeError(message)
    return result


# ── Step 1: upstream delegation ─────────────────────────────────


def _upstream_url(upstream: str) -> str:
    return f"{AGENTS_RAW_BASE}/{UPSTREAM_SCRIPTS[upstream]}"


def _response_url(response: object) -> str | None:
    """Return the final URL when *response* exposes one."""
    geturl = getattr(response, "geturl", None)
    if callable(geturl):
        url = geturl()
        return url if isinstance(url, str) else None
    return None


def _download_upstream_script(
    url: str, destination: Path, expected_sha256: str
) -> None:
    """Download the pinned upstream pre-script and verify its bytes.

    Requires https, rejects redirects, and caps the response size, so a
    moved tag or a hostile response cannot smuggle script content past
    the SHA-256 pin.
    """
    if not url.startswith("https://"):
        raise UpstreamScriptError(f"refusing to fetch a non-https URL: {url}")
    try:
        opener = urllib.request.build_opener(_NoRedirectHandler())
        response = opener.open(url, timeout=DOWNLOAD_TIMEOUT_SECONDS)
    except _UnexpectedRedirectError:
        raise UpstreamScriptError(f"redirect rejected while fetching {url}") from None
    with response:
        final_url = _response_url(response)
        if final_url is not None and final_url != url:
            raise UpstreamScriptError(
                f"redirect rejected: {url} resolved to {final_url}"
            )
        data = response.read(MAX_SCRIPT_BYTES + 1)
    if len(data) > MAX_SCRIPT_BYTES:
        raise UpstreamScriptError(
            f"the upstream pre-script exceeds {MAX_SCRIPT_BYTES} bytes: {url}"
        )
    digest = hashlib.sha256(data).hexdigest()
    if digest != expected_sha256:
        raise UpstreamScriptError(
            f"sha256 mismatch for {url}: expected {expected_sha256}, got {digest}"
        )
    destination.write_bytes(data)


def _run_upstream_script(upstream: str) -> int:
    """Download, verify, and run the pinned upstream pre-script.

    Returns its exit code verbatim (0, 78 for a neutral skip, or a hard
    failure), so fullsend sees exactly the decision the upstream script
    made. A script that cannot be fetched or verified is a hard failure:
    it must not silently disable the agent's input validation.
    """
    url = _upstream_url(upstream)
    with tempfile.TemporaryDirectory() as temporary:
        script = Path(temporary) / "upstream-pre-script.sh"
        try:
            _download_upstream_script(url, script, UPSTREAM_SCRIPT_SHA256[upstream])
        except OSError as error:
            print(
                f"ERROR: cannot run the pinned upstream pre-script {url}: {error}\n"
                "       The agent's input validation needs it. Check network\n"
                "       access and the AGENTS_REF and UPSTREAM_SCRIPT_SHA256 pins\n"
                "       in scripts/pre_commit_cache.py (the tag may have moved\n"
                "       or the file changed).",
                file=sys.stderr,
            )
            return 1
        try:
            # bash <path>, not exec: the download is not executable.
            # The inherited environment and working directory carry
            # FULLSEND_PRESCRIPT_OUTPUT and the env.runner variables to
            # the upstream script unchanged.
            return _run(["bash", str(script)]).returncode
        except OSError as error:
            print(
                f"ERROR: cannot run the upstream pre-script: {error}", file=sys.stderr
            )
            return 1


def _upstream_requested_skip() -> bool:
    """Report whether the upstream pre-script wrote ``skipped=true``.

    Fullsend's pre-script output protocol lets a script that exits 0
    request a skip by writing ``skipped=true`` to the file named by
    ``FULLSEND_PRESCRIPT_OUTPUT``. When the variable is unset (an older
    CLI), there is nothing to read and the run proceeds.
    """
    output_path = os.environ.get("FULLSEND_PRESCRIPT_OUTPUT", "")
    if not output_path:
        return False
    try:
        content = Path(output_path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    return any(line.strip() == "skipped=true" for line in content.splitlines())


# ── Step 2: warm the cache (fail-soft) ───────────────────────────


def _target_repo() -> Path:
    """Return the repository whose hook cache should be warmed.

    Fullsend exports ``TARGET_REPO_DIR`` for the repository the agent
    will edit. Fall back to this checkout for local runs.
    """
    candidate = os.environ.get("TARGET_REPO_DIR", "")
    if candidate and (Path(candidate) / ".pre-commit-config.yaml").is_file():
        return Path(candidate)
    return REPO_ROOT


def _pre_commit_version(binary: str) -> str | None:
    """Return the version *binary* reports, or None when it cannot run."""
    try:
        result = _run(
            [binary, "--version"], timeout=VERSION_TIMEOUT_SECONDS, capture=True
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if result.returncode != 0:
        return None
    words = result.stdout.split()
    return words[-1] if words else None


def _install_pre_commit() -> None:
    """Install ``pre-commit==PRECOMMIT_VERSION`` into the user site.

    Ubuntu marks the system Python as externally managed, so the install
    needs ``--break-system-packages``. Older pip versions reject that
    flag, so retry without it.
    """
    pinned = f"pre-commit=={PRECOMMIT_VERSION}"
    attempts = (
        [
            sys.executable,
            "-m",
            "pip",
            "install",
            "--user",
            "--break-system-packages",
            pinned,
        ],
        [sys.executable, "-m", "pip", "install", "--user", pinned],
    )
    for attempt in attempts:
        result = _run(attempt, timeout=PIP_TIMEOUT_SECONDS, capture=True)
        if result.returncode == 0:
            return
    message = f"could not install {pinned} with pip"
    detail = _tail(result.stderr)
    if detail:
        message = f"{message}: {detail}"
    raise RuntimeError(
        f"{message}; run manually: "
        f"python3 -m pip install --user --break-system-packages {pinned}"
    )


def _pre_commit_candidates() -> list[str]:
    """Return pre-commit binaries to try, most specific first."""
    candidates = []
    override = os.environ.get("PRE_COMMIT_BIN")
    if override:
        candidates.append(override)
    candidates.append("pre-commit")
    # pip --user installs into ~/.local/bin, which may not be on PATH.
    candidates.append(str(Path.home() / ".local" / "bin" / "pre-commit"))
    return candidates


def _resolve_pre_commit() -> str:
    """Return a pre-commit binary whose version matches the pin.

    Uses an existing binary when its ``--version`` matches. Otherwise
    installs the pinned version and looks again.
    """
    for binary in _pre_commit_candidates():
        if _pre_commit_version(binary) == PRECOMMIT_VERSION:
            return binary
    _install_pre_commit()
    for binary in _pre_commit_candidates():
        if _pre_commit_version(binary) == PRECOMMIT_VERSION:
            return binary
    raise RuntimeError(
        f"no pre-commit {PRECOMMIT_VERSION} binary is available; run manually: "
        f"python3 -m pip install --user pre-commit=={PRECOMMIT_VERSION}"
    )


def _ensure_cache_home(cache_home: Path) -> None:
    """Ensure the cache home exists and is writable by the current user.

    The cache must live at the sandbox's exact path, which on the runner
    may not exist yet or sit under a root-owned directory. One
    ``sudo -n -- install -d`` then creates it owned by the runner user,
    so pre-commit itself never runs under ``sudo``. The writability
    check makes a failed ``sudo`` (for example no passwordless sudo)
    surface as an actionable error instead of a later crash.
    """
    try:
        cache_home.mkdir(parents=True, exist_ok=True)
    except PermissionError:
        sudo = os.environ.get("SUDO", "sudo")
        _run_or_raise(
            [
                sudo,
                "-n",
                "--",
                "install",
                "-d",
                "-o",
                str(os.getuid()),
                "-g",
                str(os.getgid()),
                str(cache_home),
            ],
            description=f"cannot create the cache home {cache_home} with sudo",
            timeout=SUDO_TIMEOUT_SECONDS,
        )
    if not os.access(cache_home, os.W_OK):
        raise RuntimeError(
            f"the cache home {cache_home} is not writable by uid {os.getuid()}; "
            f"create it with: sudo install -d -o {os.getuid()} -g {os.getgid()} "
            f"{cache_home}"
        )


def _read_pyvenv_cfg(env_dir: Path) -> dict[str, str]:
    """Parse the ``key = value`` lines of a virtualenv's ``pyvenv.cfg``."""
    content = env_dir.joinpath("pyvenv.cfg").read_text(encoding="utf-8")
    values: dict[str, str] = {}
    for line in content.splitlines():
        key, separator, value = line.partition("=")
        if separator:
            values[key.strip()] = value.strip()
    return values


def _python_env_rejection(env_dir: Path, sandbox_python: str) -> str | None:
    """Return why *env_dir* cannot run in the sandbox, or None when it can.

    pre-commit's own health check reads the same ``pyvenv.cfg`` fields:
    ``version_info`` (for example ``3.12.3.final.0``) names the
    interpreter the environment was built for, and ``base-executable``
    names the interpreter it embeds.
    """
    try:
        values = _read_pyvenv_cfg(env_dir)
    except (OSError, UnicodeDecodeError):
        return "pyvenv.cfg is missing or unreadable"
    version_info = values.get("version_info")
    if not version_info:
        return "pyvenv.cfg has no version_info"
    parts = version_info.split(".")
    if len(parts) < 2 or f"{parts[0]}.{parts[1]}" != sandbox_python:
        return f"version_info {version_info} is not Python {sandbox_python}"
    if "base-executable" in values:
        base_executable = values["base-executable"]
        if not base_executable:
            # pre-commit would try to execute the empty value.
            return "pyvenv.cfg has an empty base-executable"
        executable = Path(base_executable)
        allowed_names = {"python3", f"python{sandbox_python}"}
        if (
            not executable.is_absolute()
            or executable.parent != Path("/usr/bin")
            or executable.name not in allowed_names
        ):
            return (
                f"base-executable {base_executable} is not /usr/bin/python3 "
                f"or /usr/bin/python{sandbox_python}"
            )
    return None


def _verify_python_envs(cache_home: Path, sandbox_python: str) -> None:
    """Refuse a cache whose python hook environments use another interpreter.

    Every ``py_env`` or ``py_env-*`` directory under a repository
    directory is checked by its ``pyvenv.cfg`` metadata, not by its
    name: pre-commit names the directory after the hook's effective
    ``language_version``, so names such as ``py_env-python3``,
    ``py_env-default``, or plain ``py_env`` carry no interpreter
    information. Node environments are not checked: they bundle their
    own node binary or use the sandbox PATH, so they are portable.
    """
    rejected: list[str] = []
    for repo_dir in sorted(cache_home.iterdir()):
        if not repo_dir.is_dir():
            continue
        for env_dir in sorted(repo_dir.iterdir()):
            if not env_dir.is_dir():
                continue
            if env_dir.name != "py_env" and not env_dir.name.startswith("py_env-"):
                continue
            reason = _python_env_rejection(env_dir, sandbox_python)
            if reason:
                rejected.append(f"{env_dir.relative_to(cache_home)}: {reason}")
    if rejected:
        listing = "; ".join(rejected)
        raise RuntimeError(
            f"python hook environments do not match the sandbox interpreter "
            f"Python {sandbox_python} ({listing}); no archive was written. "
            f"Pass --sandbox-python <X.Y> to override when building locally."
        )


def _remove_file(path: Path) -> None:
    """Unlink *path* when it exists; ignore errors."""
    try:
        path.unlink()
    except OSError:
        pass


def _write_tarball_atomically(cache_home: Path, tarball: Path) -> None:
    """Archive the cache to *tarball* through a staging file and one rename.

    A reader of *tarball* therefore sees either no archive or a complete
    one, never a partially written file.
    """
    tarball.parent.mkdir(parents=True, exist_ok=True)
    staging = tarball.parent / f".{tarball.name}.{os.getpid()}.tmp"
    tar = os.environ.get("TAR", "tar")
    try:
        _run_or_raise(
            [tar, "-czf", str(staging), "-C", str(cache_home), "."],
            description=f"cannot write the cache tarball {tarball}",
        )
        _run_or_raise(
            ["chmod", "0644", str(staging)],
            description=f"cannot make {staging} readable",
        )
        os.replace(staging, tarball)
    except (RuntimeError, OSError):
        _remove_file(staging)
        raise


def _warm_cache(cache_home: Path, tarball: Path, sandbox_python: str) -> None:
    """Warm the hook cache and write the transport tarball.

    Raises on any failure; ``main`` makes this phase fail-soft.
    """
    repo = _target_repo()
    config = repo / ".pre-commit-config.yaml"
    if not config.is_file():
        raise RuntimeError(f"no .pre-commit-config.yaml in {repo}")
    # A failed run must never leave a stale archive at the advertised
    # path: the sandbox would restore it and trust its contents.
    _remove_file(tarball)
    binary = _resolve_pre_commit()
    _ensure_cache_home(cache_home)
    env = dict(os.environ)
    env["PRE_COMMIT_HOME"] = str(cache_home)
    _run_or_raise(
        [binary, "install-hooks", "--config", str(config)],
        description="pre-commit install-hooks failed",
        env=env,
        timeout=INSTALL_HOOKS_TIMEOUT_SECONDS,
        cwd=str(repo),
    )
    _verify_python_envs(cache_home, sandbox_python)
    _run_or_raise(
        ["chmod", "-R", "a+rX", str(cache_home)],
        description=f"cannot make {cache_home} world-readable",
    )
    _write_tarball_atomically(cache_home, tarball)


# ── Entrypoint ───────────────────────────────────────────────────


def _parse_args(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--upstream",
        required=True,
        choices=("code", "fix"),
        help="pinned upstream pre-script to delegate to",
    )
    parser.add_argument(
        "--skip-upstream",
        action="store_true",
        help="skip the upstream pre-script (for local use and tests)",
    )
    parser.add_argument(
        "--cache-home",
        default=os.environ.get("PROTOBOT_CACHE_HOME", DEFAULT_CACHE_HOME),
        help=f"cache home to warm (default: {DEFAULT_CACHE_HOME})",
    )
    parser.add_argument(
        "--tarball",
        default=os.environ.get("PROTOBOT_CACHE_TARBALL", DEFAULT_TARBALL),
        help=f"tarball to write (default: {DEFAULT_TARBALL})",
    )
    parser.add_argument(
        "--sandbox-python",
        default=os.environ.get(
            "PROTOBOT_SANDBOX_PYTHON_VERSION", SANDBOX_PYTHON_VERSION
        ),
        help=(
            f"Python X.Y version the sandbox uses (default: {SANDBOX_PYTHON_VERSION})"
        ),
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    if not args.skip_upstream:
        upstream_rc = _run_upstream_script(args.upstream)
        if upstream_rc != 0:
            # The upstream script skipped (78) or failed the run. No
            # sandbox is created, so warming the cache would be wasted.
            return upstream_rc
        if _upstream_requested_skip():
            # The upstream script asked fullsend to skip the run. No
            # sandbox is created, so warming the cache would be wasted.
            return 0
    try:
        _warm_cache(Path(args.cache_home), Path(args.tarball), args.sandbox_python)
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        # Fail-soft: a bootstrap failure must not block the agent run.
        # scripts/lint.py still reports its actionable cache-miss error.
        # A failed run must leave no archive at the advertised path: the
        # sandbox would restore a stale one and trust its contents.
        _remove_file(Path(args.tarball))
        message = (
            f"pre-commit cache bootstrap failed: {error}. "
            f"Warm it manually: PRE_COMMIT_HOME={args.cache_home} "
            f"pre-commit install-hooks --config "
            f"{_target_repo() / '.pre-commit-config.yaml'}"
        )
        if Path(args.tarball).exists():
            print(
                "::error::"
                + _sanitize_workflow_command(
                    f"the stale archive {args.tarball} could not be removed; "
                    f"stopping the run so the sandbox cannot restore it"
                )
            )
            for line in message.splitlines():
                print(f"| {_sanitize_workflow_command(line)}")
            return 1
        # One sanitized workflow command, then the detail lines. The
        # ``| `` prefix starts each line with a non-whitespace character,
        # so a runner cannot parse the detail as a workflow command.
        print(f"::warning::{_sanitize_workflow_command(message)}")
        for line in message.splitlines():
            print(f"| {_sanitize_workflow_command(line)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
