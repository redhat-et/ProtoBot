#!/usr/bin/env python3
"""Preview the files seeded by ``fullsend github setup`` without mutating GitHub."""

from __future__ import annotations

import argparse
import difflib
import json
import re
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

from update_fullsend_pins import (
    UpdateError,
    fetch_bytes,
    normalize_version,
    resolve_workflow_sha,
)

FULLSEND_RAW_BASE = "https://raw.githubusercontent.com/fullsend-ai/fullsend"
FULLSEND_REPOSITORY = "fullsend-ai/fullsend"
MANAGED_HEADER_BASE = (
    "https://github.com/fullsend-ai/fullsend/blob/main/internal/scaffold/fullsend-repo/"
)
ANSI_ESCAPE_PATTERN = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
VERSION_PATTERN = re.compile(
    r"(?<![0-9A-Za-z])v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?"
)
SETUP_FILE_PATTERN = re.compile(
    r"Would commit:\s+(?P<path>\S+)\s+\((?P<size>\d+) bytes\)"
)
GITHUB_EXPRESSION_PATTERN = re.compile(r"\$\{\{(?P<expression>.*?)\}\}", re.DOTALL)
VARIABLE_REFERENCE_PATTERN = re.compile(
    r"\bvars(?:\.(?P<name>[A-Za-z_][A-Za-z0-9_]*)|"
    r"\[(?P<quote>['\"])(?P<indexed_name>[A-Za-z_][A-Za-z0-9_]*)(?P=quote)\])"
)
SECRET_REFERENCE_PATTERN = re.compile(
    r"\bsecrets(?:\.(?P<name>[A-Za-z_][A-Za-z0-9_]*)|"
    r"\[(?P<quote>['\"])(?P<indexed_name>[A-Za-z_][A-Za-z0-9_]*)(?P=quote)\])"
)


@dataclass(frozen=True)
class PlannedFile:
    path: str
    expected_size: int


@dataclass(frozen=True)
class SetupState:
    variables: dict[str, str]
    secrets: tuple[str, ...]


@dataclass(frozen=True)
class VariableChange:
    name: str
    desired: str | None
    current: str | None

    @property
    def changed(self) -> bool:
        return self.desired is not None and self.current != self.desired


TEMPLATE_BY_TARGET: dict[str, tuple[str, str]] = {
    ".github/workflows/fullsend.yaml": (
        "internal/scaffold/fullsend-repo/templates/shim-per-repo.yaml",
        "dispatch",
    ),
    ".github/workflows/prioritize.yml": (
        "internal/scaffold/fullsend-repo/.github/workflows/prioritize.yml",
        "prioritize",
    ),
}


def run_command(
    command: Sequence[str],
    root: Path,
    timeout: float,
    *,
    stdout_only: bool = False,
) -> str:
    try:
        result = subprocess.run(
            list(command),
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except FileNotFoundError as exc:
        raise UpdateError(f"required command not found: {command[0]}") from exc
    except subprocess.TimeoutExpired as exc:
        raise UpdateError(f"command timed out: {' '.join(command)}") from exc

    stdout = result.stdout or ""
    stderr = result.stderr or ""
    if result.returncode != 0:
        detail = (stdout + stderr).strip() or "no output"
        raise UpdateError(
            f"command failed with exit code {result.returncode}: "
            f"{' '.join(command)}\n{detail}"
        )
    return stdout if stdout_only else stdout + stderr


def resolve_cli_version(root: Path, timeout: float) -> str:
    output = run_command(["fullsend", "--version"], root, timeout)
    match = VERSION_PATTERN.search(output)
    if match is None:
        raise UpdateError(
            "could not determine a release version from `fullsend --version`; "
            "a release build is required to reconstruct setup files"
        )
    return normalize_version(match.group(0))


def parse_setup_state(output: str) -> SetupState:
    variables: dict[str, str] = {}
    secrets: list[str] = []
    expected_secret_count: int | None = None
    section: str | None = None
    seen_sections: set[str] = set()
    for line in output.splitlines():
        stripped = line.strip()
        if stripped == "Would set repository variables:":
            section = "variables"
            seen_sections.add("variables")
            continue
        secret_heading = re.fullmatch(
            r"Would set (?P<count>\d+) repository secrets:", stripped
        )
        if secret_heading is not None:
            expected_secret_count = int(secret_heading.group("count"))
            section = "secrets"
            seen_sections.add("secrets")
            continue
        heading = re.match(
            r"Would set\b.*\brepository (?P<section>variables|secrets)\b", stripped
        )
        if heading is not None:
            raise UpdateError(
                "the dry-run reported an unsupported repository "
                f"{heading.group('section')} heading"
            )
        if not stripped:
            continue

        if (
            section == "secrets"
            and stripped
            == "Would remove stale vendored assets at .fullsend/bin/fullsend (if present)"
        ):
            section = None
            continue
        if section == "variables":
            match = re.match(
                r"\s+(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?P<value>.*)$",
                line,
            )
            if match is None:
                raise UpdateError(
                    "the dry-run reported a malformed repository variables row"
                )
            else:
                variables[match.group("name")] = match.group("value")
        elif section == "secrets":
            match = re.fullmatch(r"\s+(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*", line)
            if match is None:
                raise UpdateError(
                    "the dry-run reported a malformed repository secrets row"
                )
            else:
                secrets.append(match.group("name"))
    missing_sections = {"variables", "secrets"} - seen_sections
    if missing_sections:
        raise UpdateError(
            "the dry-run omitted required repository settings sections: "
            + ", ".join(sorted(missing_sections))
        )
    if expected_secret_count != len(secrets):
        raise UpdateError(
            "the dry-run declared "
            f"{expected_secret_count} repository secrets but listed {len(secrets)}"
        )
    return SetupState(variables, tuple(secrets))


def run_setup_dry_run(
    target: str, root: Path, timeout: float
) -> tuple[str, list[PlannedFile], SetupState]:
    # Keep --dry-run in this literal command construction. Do not add a mode
    # that can call the mutating setup command.
    output = run_command(
        ["fullsend", "github", "setup", target, "--dry-run"], root, timeout
    )
    clean_output = ANSI_ESCAPE_PATTERN.sub("", output)
    planned_files = [
        PlannedFile(match.group("path"), int(match.group("size")))
        for match in SETUP_FILE_PATTERN.finditer(clean_output)
    ]
    if not planned_files:
        raise UpdateError(
            "the dry-run did not report any generated files; inspect the setup output"
        )
    duplicate_paths = {
        item.path
        for item in planned_files
        if sum(f.path == item.path for f in planned_files) > 1
    }
    if duplicate_paths:
        raise UpdateError(
            "the dry-run reported duplicate generated paths: "
            + ", ".join(sorted(duplicate_paths))
        )
    missing_paths = set(TEMPLATE_BY_TARGET) - {item.path for item in planned_files}
    if missing_paths:
        raise UpdateError(
            "the dry-run omitted required generated paths: "
            + ", ".join(sorted(missing_paths))
        )
    return clean_output, planned_files, parse_setup_state(clean_output)


def list_current_variables(target: str, root: Path, timeout: float) -> dict[str, str]:
    raw = run_command(
        ["gh", "variable", "list", "--repo", target, "--json", "name,value"],
        root,
        timeout,
        stdout_only=True,
    )
    try:
        entries = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise UpdateError("gh variable list returned invalid JSON") from exc
    if not isinstance(entries, list):
        raise UpdateError("gh variable list returned an unexpected JSON shape")

    current: dict[str, str] = {}
    for entry in entries:
        if not isinstance(entry, dict) or not isinstance(entry.get("name"), str):
            raise UpdateError("gh variable list returned an invalid variable entry")
        value = entry.get("value")
        if not isinstance(value, str):
            raise UpdateError(
                f"gh variable list returned a non-string value for {entry['name']}"
            )
        current[entry["name"]] = value
    return current


def list_current_secrets(target: str, root: Path, timeout: float) -> set[str]:
    raw = run_command(
        ["gh", "secret", "list", "--repo", target, "--json", "name"],
        root,
        timeout,
        stdout_only=True,
    )
    try:
        entries = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise UpdateError("gh secret list returned invalid JSON") from exc
    if not isinstance(entries, list):
        raise UpdateError("gh secret list returned an unexpected JSON shape")

    current: set[str] = set()
    for entry in entries:
        if not isinstance(entry, dict) or not isinstance(entry.get("name"), str):
            raise UpdateError("gh secret list returned an invalid secret entry")
        current.add(entry["name"])
    return current


def compare_variables(
    desired: dict[str, str],
    current: dict[str, str],
    workflow_references: set[str],
) -> tuple[VariableChange, ...]:
    return tuple(
        VariableChange(name, desired.get(name), current.get(name))
        for name in sorted(set(desired) | workflow_references)
    )


def extract_workflow_references(
    rendered_files: Sequence[tuple[PlannedFile, bytes, bytes]],
    generated: bool,
) -> tuple[set[str], set[str]]:
    variables: set[str] = set()
    secrets: set[str] = set()
    for _, current, generated_content in rendered_files:
        content = generated_content if generated else current
        text = content.decode("utf-8")
        for expression in GITHUB_EXPRESSION_PATTERN.finditer(text):
            body = expression.group("expression")
            variables.update(
                match.group("name") or match.group("indexed_name")
                for match in VARIABLE_REFERENCE_PATTERN.finditer(body)
            )
            secrets.update(
                match.group("name") or match.group("indexed_name")
                for match in SECRET_REFERENCE_PATTERN.finditer(body)
            )
    return variables, secrets


def print_reference_delta(label: str, generated: set[str], current: set[str]) -> None:
    added = sorted(generated - current)
    removed = sorted(current - generated)
    if not added and not removed:
        print(f"  No {label} changes in generated files.", file=sys.stderr)
        return

    print(f"  {label} changes in generated files:", file=sys.stderr)
    for name in added:
        print(f"    ADD {name}", file=sys.stderr)
    for name in removed:
        print(f"    REMOVE {name}", file=sys.stderr)


def print_setup_state(
    state: SetupState,
    variable_changes: tuple[VariableChange, ...],
    generated_variable_references: set[str],
    current_variable_references: set[str],
    generated_secret_references: set[str],
    current_secret_references: set[str],
    current_secrets: set[str],
) -> None:
    print(
        "Repository state proposed by setup dry-run (report only; no changes applied):",
        file=sys.stderr,
    )
    print_reference_delta(
        "Workflow variable reference",
        generated_variable_references,
        current_variable_references,
    )
    print_reference_delta(
        "Workflow secret reference",
        generated_secret_references,
        current_secret_references,
    )

    if variable_changes:
        print("  Repository variable review:", file=sys.stderr)
        changed_variables = False
        for change in variable_changes:
            if change.desired is None:
                if change.current is None:
                    print(
                        f"    REVIEW {change.name}: generated workflow references it, "
                        "but setup dry-run supplied no value",
                        file=sys.stderr,
                    )
                else:
                    print(
                        f"    PRESENT {change.name}: {change.current} "
                        "(workflow reference; setup supplied no value)",
                        file=sys.stderr,
                    )
            elif change.current is None:
                print(
                    f"    ADD {change.name}: <missing> -> {change.desired}",
                    file=sys.stderr,
                )
                changed_variables = True
            elif change.changed:
                print(
                    f"    UPDATE {change.name}: {change.current} -> {change.desired}",
                    file=sys.stderr,
                )
                changed_variables = True
            else:
                print(
                    f"    UNCHANGED {change.name}: {change.desired}",
                    file=sys.stderr,
                )
        if not changed_variables and all(
            change.desired is not None for change in variable_changes
        ):
            print("    No variable changes are necessary.", file=sys.stderr)
    else:
        print("  No repository variable changes suggested.", file=sys.stderr)

    secret_names = sorted(set(state.secrets) | generated_secret_references)
    if secret_names:
        print(
            "  Repository secret review (names only; nothing applied):", file=sys.stderr
        )
        for name in secret_names:
            source = []
            if name in state.secrets:
                source.append("setup dry-run")
            if name in generated_secret_references:
                source.append("generated workflow")
            presence = "PRESENT" if name in current_secrets else "MISSING"
            print(
                f"    {presence} {name} [{', '.join(source)}]",
                file=sys.stderr,
            )
    else:
        print("  No repository secrets referenced or listed by setup.", file=sys.stderr)


def resolve_runner(version: str, timeout: float) -> str:
    source = fetch_bytes(
        f"{FULLSEND_RAW_BASE}/{version}/internal/config/config.go", timeout
    ).decode("utf-8")
    match = re.search(r'DefaultGHRunner\s*=\s*"([^"]+)"', source)
    if match is None:
        raise UpdateError(f"could not find DefaultGHRunner in Fullsend {version}")
    return match.group(1)


def resolve_ref_comment_separator(version: str, timeout: float) -> str:
    source = fetch_bytes(
        f"{FULLSEND_RAW_BASE}/{version}/internal/scaffold/render.go", timeout
    ).decode("utf-8")
    literals = re.findall(
        r"(?:return\s+ref\s*\+|uses\s*\+=)\s*"
        r'("(?:[^"\\]|\\.)*")\s*\+\s*opts\.UpstreamTag',
        source,
    )
    try:
        separators = {json.loads(literal) for literal in literals}
    except json.JSONDecodeError as exc:
        raise UpdateError(
            f"could not decode reference comments in Fullsend {version}"
        ) from exc
    if len(separators) != 1:
        raise UpdateError(
            "could not determine a consistent reference comment separator "
            f"in Fullsend {version}"
        )
    return separators.pop()


def managed_header(target_path: str) -> str:
    return (
        "# This file is managed by fullsend. Do not edit it directly.\n"
        f"# Upstream: {MANAGED_HEADER_BASE}{target_path}\n"
    )


def render_generated_file(
    target_path: str,
    version: str,
    workflow_sha: str,
    runner: str,
    ref_comment_separator: str,
    timeout: float,
) -> bytes:
    try:
        source_path, stage = TEMPLATE_BY_TARGET[target_path]
    except KeyError as exc:
        supported = ", ".join(sorted(TEMPLATE_BY_TARGET))
        raise UpdateError(
            f"cannot reconstruct setup file {target_path}; supported files: {supported}"
        ) from exc

    raw = fetch_bytes(f"{FULLSEND_RAW_BASE}/{version}/{source_path}", timeout)
    rendered = raw.decode("utf-8")
    ref = f"{workflow_sha}{ref_comment_separator}{version}"
    if stage == "dispatch":
        rendered = rendered.replace(
            "__REUSABLE_DISPATCH__",
            f"{FULLSEND_REPOSITORY}/.github/workflows/reusable-dispatch.yml@{ref}",
        )
    else:
        rendered = rendered.replace(
            "__REUSABLE_WORKFLOW__",
            f"{FULLSEND_REPOSITORY}/.github/workflows/reusable-{stage}.yml@{ref}",
        )
        rendered = rendered.replace("install_mode: per-org", "install_mode: per-repo")
    rendered = rendered.replace("__FULLSEND_AI_REF__", ref)
    rendered = rendered.replace("__GH_RUNNER__", runner)
    return (managed_header(target_path) + rendered).encode("utf-8")


def render_diff(target_path: str, current: bytes, generated: bytes) -> str:
    current_lines = current.decode("utf-8").splitlines(keepends=True)
    generated_lines = generated.decode("utf-8").splitlines(keepends=True)
    from_name = f"a/{target_path}" if current else "/dev/null"
    diff = difflib.unified_diff(
        current_lines,
        generated_lines,
        fromfile=from_name,
        tofile=f"b/{target_path}",
    )
    return "".join(diff)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Run Fullsend GitHub setup in dry-run mode and print the "
            "reconstructed managed-file diff."
        )
    )
    parser.add_argument(
        "target", help="GitHub owner/repository, for example example-org/example-repo"
    )
    parser.add_argument(
        "--version",
        required=True,
        help="selected Fullsend release tag; must match `fullsend --version`",
    )
    parser.add_argument(
        "--root",
        type=Path,
        default=Path.cwd(),
        help="repository root used as the setup command cwd (default: current directory)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=60.0,
        help="command and network timeout in seconds (default: 60)",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.target):
        parser.error("target must be a GitHub owner/repository")

    root = args.root.resolve()
    if not root.is_dir():
        parser.error(f"repository root is not a directory: {root}")

    try:
        selected_version = normalize_version(args.version)
        version = resolve_cli_version(root, args.timeout)
        if version != selected_version:
            raise UpdateError(
                f"Fullsend CLI version {version} does not match selected target "
                f"{selected_version}"
            )
        dry_run_output, planned_files, state = run_setup_dry_run(
            args.target, root, args.timeout
        )
        workflow_sha = resolve_workflow_sha(version, args.timeout)
        runner = resolve_runner(version, args.timeout)
        ref_comment_separator = resolve_ref_comment_separator(version, args.timeout)

        rendered_files: list[tuple[PlannedFile, bytes, bytes]] = []
        for planned in planned_files:
            generated = render_generated_file(
                planned.path,
                version,
                workflow_sha,
                runner,
                ref_comment_separator,
                args.timeout,
            )
            if len(generated) != planned.expected_size:
                raise UpdateError(
                    f"reconstructed {planned.path} is {len(generated)} bytes, "
                    f"but `fullsend github setup --dry-run` reported "
                    f"{planned.expected_size} bytes"
                )
            current_path = (root / planned.path).resolve()
            try:
                current_path.relative_to(root)
            except ValueError as exc:
                raise UpdateError(
                    f"managed workflow path escapes repository root: {planned.path}"
                ) from exc
            current = current_path.read_bytes() if current_path.is_file() else b""
            rendered_files.append((planned, current, generated))
        generated_variable_references, generated_secret_references = (
            extract_workflow_references(rendered_files, generated=True)
        )
        current_variable_references, current_secret_references = (
            extract_workflow_references(rendered_files, generated=False)
        )
    except (OSError, UnicodeDecodeError, UpdateError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2

    print(
        f"# Safe command executed: fullsend github setup {args.target} --dry-run",
        file=sys.stderr,
    )
    print(dry_run_output.rstrip(), file=sys.stderr)
    print(
        f"# Reconstructed Fullsend release: {version} ({workflow_sha})", file=sys.stderr
    )

    diffs = [
        (planned.path, render_diff(planned.path, current, generated))
        for planned, current, generated in rendered_files
    ]
    changed = False
    for _, diff in diffs:
        if diff:
            changed = True
            sys.stdout.write(diff)

    try:
        current_variables = list_current_variables(args.target, root, args.timeout)
        current_secrets = list_current_secrets(args.target, root, args.timeout)
        variable_changes = compare_variables(
            state.variables,
            current_variables,
            generated_variable_references,
        )
    except (OSError, UnicodeDecodeError, UpdateError) as exc:
        print(f"ERROR: repository settings lookup failed: {exc}", file=sys.stderr)
        return 2

    variable_review_required = any(
        change.changed or (change.desired is None and change.current is None)
        for change in variable_changes
    )
    reference_review_required = bool(
        generated_variable_references ^ current_variable_references
        or generated_secret_references ^ current_secret_references
    )
    secret_review_required = any(
        name not in current_secrets
        for name in set(state.secrets) | generated_secret_references
    )
    review_required = (
        any(diff for _, diff in diffs)
        or variable_review_required
        or reference_review_required
        or secret_review_required
    )
    if review_required:
        print(
            "# REVIEW REQUIRED: setup proposes changes or unresolved repository state",
            file=sys.stderr,
        )
    print_setup_state(
        state,
        variable_changes,
        generated_variable_references,
        current_variable_references,
        generated_secret_references,
        current_secret_references,
        current_secrets,
    )
    if not changed:
        print("No generated setup file changes.", file=sys.stderr)
    return 3 if review_required else 0


if __name__ == "__main__":
    raise SystemExit(main())
