#!/usr/bin/env python3
"""Validate a pre-commit gate configuration against the ADR-0005 invariant.

Used by the ``gate-rehearsal`` CI job. ``.pre-commit-config.yaml`` is the
gate configuration: it must run in the sandbox without VCS access. Each
violation prints one ``ERROR:`` diagnostic to stderr, naming the
offending hook id (or the repository entry) and the fix. A passing run
prints one ``GATE INVARIANT PASSED`` line to stdout naming the number of
hooks checked.

Rules (ADR-0005):

* The file exists, is a YAML mapping with a ``repos`` list, and
  defines at least one hook: an empty gate protects nothing.
* Every ``repos`` entry is ``repo: local``. A hook that needs a VCS
  clone, the Go toolchain, or a default-branch ref belongs in
  ``.github/pre-commit-ci.yaml`` instead.
* Every hook sets an ``id`` and a ``language``.
* A hook ``stages`` list and the top-level ``default_stages`` list,
  when present, contain the ``pre-commit`` stage: pre-commit silently
  omits a hook outside the run stage, so the rehearsal would pass
  while the hook never runs.
* ``language: python`` hooks set ``additional_dependencies`` to a
  non-empty list of exact ``name==version`` pins.
* ``language: system`` hooks run ``python3`` with one script under
  ``scripts/`` and plain arguments only: no shell control operators,
  and the resolved script path stays inside ``scripts/``. Every import
  in the entry script, at any nesting depth, must be a standard-library
  module, a relative import, or a repository module (the ``scripts``
  package name, or a file ``scripts/<root>.py`` or
  ``scripts/<root>/__init__.py`` under the repository root).
  Repository modules are resolved along their full dotted path (a
  package ``__init__.py`` plus each named submodule) and inspected
  transitively for the imports that execute at module import time.
  Class bodies execute at import time, so their imports count; function
  bodies run only when called, so their imports do not count
  (``scripts/lint.py`` imports PyYAML inside ``_load_config``).
* Every ``scripts/check_NAME.py`` gate checker has a companion hook
  that runs ``scripts/test_check_NAME.py``, so its negative fixtures
  run in the rehearsal.

PyYAML is used because this script runs only in CI, where the rehearsal
environment installs it; the script is not itself a gate hook.
"""

from __future__ import annotations

import argparse
import ast
import os
import re
import shlex
import sys
from dataclasses import dataclass
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_CONFIG_NAME = ".pre-commit-config.yaml"
ALLOWED_LANGUAGES = ("python", "pygrep", "system")
ENTRY_EXAMPLE = 'for example "python3 scripts/check_foo.py --flag"'
MAX_SUBJECT_CHARS = 80
GATE_STAGE = "pre-commit"
EXACT_PIN_PATTERN = re.compile(r"^[A-Za-z0-9_.-]+==[A-Za-z0-9_.+-]+$")
# shortcut: one character per operator class covers chaining, piping,
# redirection, substitution, and expansion; upgrade to a shell tokenizer
# if an entry ever needs a quoted operator character.
SHELL_METACHARACTERS = frozenset("&|;><`$\n\r")


@dataclass
class GateReport:
    """Result of checking one gate configuration file."""

    config_display: str
    hooks_checked: int
    diagnostics: list[str]


@dataclass(frozen=True)
class ImportViolation:
    """One third-party import reached from a gate entry script.

    ``chain`` lists the repository modules from the gate entry script
    to the module that imports ``module``; the entry script is the
    first entry and the importing module is the last entry.
    """

    chain: tuple[str, ...]
    module: str


def _violation(display: str, subject: str, message: str) -> str:
    """Return one formatted gate-invariant diagnostic line."""
    return f"ERROR: {display}: {subject}: {message}"


def _short(value: object) -> str:
    """Return a bounded string form of *value* for diagnostic output."""
    text = str(value)
    if len(text) <= MAX_SUBJECT_CHARS:
        return text
    return text[: MAX_SUBJECT_CHARS - 3] + "..."


def _display_path(path: Path, repo_root: Path) -> str:
    """Return *path* relative to *repo_root*, or absolute when outside."""
    absolute = Path(os.path.abspath(path))
    try:
        return absolute.resolve().relative_to(repo_root.resolve()).as_posix()
    except ValueError:
        return absolute.as_posix()


def _load_config(path: Path) -> tuple[dict[str, object] | None, str | None]:
    """Return the parsed gate configuration mapping, or an error."""
    try:
        with open(path, encoding="utf-8") as stream:
            data = yaml.safe_load(stream)
    except (OSError, UnicodeError) as error:
        return None, f"cannot read gate configuration ({type(error).__name__})"
    except yaml.YAMLError:
        return None, "gate configuration is not valid YAML"
    if not isinstance(data, dict):
        return None, "gate configuration must be a YAML mapping"
    return data, None


def _check_default_stages(
    data: dict[str, object],
    display: str,
) -> list[str]:
    """Reject a ``default_stages`` list without the gate stage.

    pre-commit silently omits a hook whose stages do not contain the
    run stage, so a configuration-wide default outside the gate stage
    would silence every hook without printing a ``Skipped`` line.
    """
    if "default_stages" not in data:
        return []
    stages = data["default_stages"]
    if isinstance(stages, list) and GATE_STAGE in stages:
        return []
    message = (
        f"default_stages must contain the {GATE_STAGE} stage; the gate "
        f"runs the {GATE_STAGE} stage, and a hook outside it cannot be "
        "rehearsed"
    )
    return [_violation(display, "default_stages", message)]


def _repo_subject(entry: dict[str, object]) -> str:
    """Return the repository value that names a repository-level violation."""
    if "repo" not in entry:
        return "<missing repo>"
    return _short(entry["repo"])


def _hook_subject(hook: object, repo_value: str) -> str:
    """Return the hook id that names a hook-level violation.

    A hook without a usable id is named by its repository entry value.
    """
    if isinstance(hook, dict):
        hook_id = hook.get("id")
        if isinstance(hook_id, str) and hook_id:
            return hook_id
    return repo_value


def _check_repo_entry(
    entry: object,
    display: str,
) -> tuple[list[str], list[tuple[object, str]]]:
    """Return the diagnostics and local hooks for one ``repos`` entry.

    Each local hook is one ``(hook, repo value)`` pair. Entries that are
    not ``repo: local`` contribute no hooks: their hooks come from a VCS
    clone and are not part of the gate.
    """
    if not isinstance(entry, dict):
        diagnostic = _violation(display, _short(entry), "repo entry must be a mapping")
        return [diagnostic], []
    if entry.get("repo") != "local":
        message = (
            'repo must be "local"; convert the hook to a local hook with '
            "pinned additional_dependencies, or move it to "
            ".github/pre-commit-ci.yaml (ADR-0005)"
        )
        return [_violation(display, _repo_subject(entry), message)], []
    hooks = entry.get("hooks")
    if not isinstance(hooks, list):
        diagnostic = _violation(display, "local", "repo entry must define a hooks list")
        return [diagnostic], []
    return [], [(hook, "local") for hook in hooks]


def _check_hook(
    hook: object,
    subject: str,
    display: str,
    repo_root: Path,
) -> list[str]:
    """Return every diagnostic for one hook entry."""
    if not isinstance(hook, dict):
        return [_violation(display, subject, "hook entry must be a mapping")]
    diagnostics: list[str] = []
    hook_id = hook.get("id")
    if not (isinstance(hook_id, str) and hook_id):
        diagnostics.append(_violation(display, subject, 'hook must set an "id"'))
    diagnostics.extend(_check_hook_stages(hook, subject, display))
    language = hook.get("language")
    if not (isinstance(language, str) and language):
        allowed = ", ".join(ALLOWED_LANGUAGES)
        diagnostics.append(
            _violation(
                display, subject, f"hook must set a language (one of: {allowed})"
            )
        )
        return diagnostics
    if language == "python":
        diagnostics.extend(_check_python_hook(hook, subject, display))
    elif language == "system":
        diagnostics.extend(_check_system_hook(hook, subject, display, repo_root))
    elif language != "pygrep":
        allowed = ", ".join(ALLOWED_LANGUAGES)
        diagnostics.append(
            _violation(
                display,
                subject,
                f'language "{language}" is not allowed in the gate '
                f"configuration; allowed languages are: {allowed} (ADR-0005)",
            )
        )
    return diagnostics


def _check_hook_stages(
    hook: dict[str, object],
    subject: str,
    display: str,
) -> list[str]:
    """Reject a ``stages`` list without the gate stage.

    A missing ``stages`` key is allowed: pre-commit then runs the hook
    in the ``pre-commit`` stage by default.
    """
    if "stages" not in hook:
        return []
    stages = hook["stages"]
    if isinstance(stages, list) and GATE_STAGE in stages:
        return []
    message = (
        f"stages must contain the {GATE_STAGE} stage; the gate runs the "
        f"{GATE_STAGE} stage, and a hook outside it cannot be rehearsed"
    )
    return [_violation(display, subject, message)]


def _check_python_hook(
    hook: dict[str, object],
    subject: str,
    display: str,
) -> list[str]:
    """Return diagnostics for one ``language: python`` hook."""
    dependencies = hook.get("additional_dependencies")
    if dependencies is None:
        message = (
            'language "python" hooks must set additional_dependencies to a '
            'non-empty list of "name==version" pins'
        )
        return [_violation(display, subject, message)]
    if not isinstance(dependencies, list) or not dependencies:
        message = (
            'additional_dependencies must be a non-empty list of "name==version" pins'
        )
        return [_violation(display, subject, message)]
    diagnostics: list[str] = []
    for dependency in dependencies:
        if not (isinstance(dependency, str) and _is_exact_pin(dependency)):
            message = (
                f'dependency "{_short(dependency)}" must be an exact '
                f'"name==version" pin (for example "ruff==0.17.5")'
            )
            diagnostics.append(_violation(display, subject, message))
    return diagnostics


def _is_exact_pin(dependency: str) -> bool:
    """Return True when *dependency* has the exact ``name==version`` form.

    Only a plain name and an exact version pin give the sandbox a
    deterministic install; URLs, ``git+`` references, ``@``, ``#``,
    extras, and environment markers are rejected.
    """
    return EXACT_PIN_PATTERN.fullmatch(dependency) is not None


def _contains_shell_metacharacters(tokens: list[str]) -> bool:
    """Return True when any entry token holds a shell control operator.

    ``&``, ``|``, ``;``, ``>``, ``<``, the backtick, and ``$`` cover
    chained commands, pipelines, redirections, and substitutions.
    Newline and carriage return separate commands in a shell.
    """
    return any(SHELL_METACHARACTERS.intersection(token) for token in tokens)


def _parse_entry_script(entry: str) -> str | None:
    """Return the ``scripts/`` path that one system entry runs, or None."""
    try:
        tokens = shlex.split(entry)
    except ValueError:
        return None
    if len(tokens) < 2 or tokens[0] != "python3":
        return None
    if _contains_shell_metacharacters(tokens):
        return None
    script = Path(tokens[1]).as_posix()
    if not (script.startswith("scripts/") and script.endswith(".py")):
        return None
    return script


def _check_system_hook(
    hook: dict[str, object],
    subject: str,
    display: str,
    repo_root: Path,
) -> list[str]:
    """Return diagnostics for one ``language: system`` hook."""
    entry = hook.get("entry")
    if not isinstance(entry, str) or not entry:
        message = (
            "system hooks must run a python3 script under scripts/ with "
            f"plain arguments only ({ENTRY_EXAMPLE})"
        )
        return [_violation(display, subject, message)]
    script = _parse_entry_script(entry)
    if script is None:
        message = (
            f'system entry "{_short(entry)}" must run a python3 script '
            f"under scripts/ with plain arguments only ({ENTRY_EXAMPLE})"
        )
        return [_violation(display, subject, message)]
    return _check_entry_script(script, subject, display, repo_root)


def _is_inside_scripts(path: Path, repo_root: Path) -> bool:
    """Return True when *path* resolves inside the ``scripts/`` directory."""
    scripts_directory = (repo_root / "scripts").resolve()
    try:
        path.resolve().relative_to(scripts_directory)
    except (OSError, ValueError):
        return False
    return True


def _check_entry_script(
    script: str,
    subject: str,
    display: str,
    repo_root: Path,
) -> list[str]:
    """Return diagnostics for the script that one system entry runs."""
    path = repo_root / script
    if not _is_inside_scripts(path, repo_root):
        message = f'entry script "{script}" escapes the scripts/ directory'
        return [_violation(display, subject, message)]
    try:
        source = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        message = f'cannot read entry script "{script}" ({type(error).__name__})'
        return [_violation(display, subject, message)]
    try:
        tree = ast.parse(source)
    except SyntaxError:
        message = f'entry script "{script}" does not parse as Python'
        return [_violation(display, subject, message)]
    return [
        _violation(display, subject, _import_violation_message(script, found))
        for found in _third_party_imports(script, tree, repo_root)
    ]


def _third_party_imports(
    script: str,
    tree: ast.Module,
    repo_root: Path,
) -> list[ImportViolation]:
    """Return every third-party import reachable from *script*.

    The entry script is inspected with ``ast.walk``, so imports nested
    in functions count. Repository modules that any import names are
    parsed and inspected for their module-scope imports with the same
    allowed-root rules. The visited set holds repository-module paths,
    so each module is inspected once and the recursion stays bounded.
    """
    violations: list[ImportViolation] = []
    _scan_imports(_all_imports(tree), (script,), repo_root, {script}, violations)
    return violations


def _scan_imports(
    imports: list[ast.Import | ast.ImportFrom],
    chain: tuple[str, ...],
    repo_root: Path,
    visited: set[str],
    violations: list[ImportViolation],
) -> None:
    """Inspect *imports* and every repository module they name."""
    for node in imports:
        if isinstance(node, ast.Import):
            for alias in node.names:
                _scan_dotted_import(alias.name, chain, repo_root, visited, violations)
        elif isinstance(node, ast.ImportFrom):
            _scan_from_import(node, chain, repo_root, visited, violations)


def _scan_dotted_import(
    dotted: str,
    chain: tuple[str, ...],
    repo_root: Path,
    visited: set[str],
    violations: list[ImportViolation],
) -> None:
    """Inspect one dotted module name of an ``import`` statement."""
    parts = dotted.split(".")
    if not _is_allowed_root(parts[0], repo_root):
        violations.append(ImportViolation(chain, parts[0]))
        return
    _scan_repository_files([dotted], chain, repo_root, visited, violations)


def _scan_from_import(
    node: ast.ImportFrom,
    chain: tuple[str, ...],
    repo_root: Path,
    visited: set[str],
    violations: list[ImportViolation],
) -> None:
    """Inspect one ``from`` import statement.

    The module itself and every imported name resolve to repository
    files; a name that is an attribute, not a submodule, resolves to
    no file.
    """
    if node.level > 0:
        return
    module = node.module or ""
    parts = module.split(".")
    if not _is_allowed_root(parts[0], repo_root):
        violations.append(ImportViolation(chain, parts[0]))
        return
    paths = [module] + [f"{module}.{alias.name}" for alias in node.names]
    _scan_repository_files(paths, chain, repo_root, visited, violations)


def _scan_repository_files(
    paths: list[str],
    chain: tuple[str, ...],
    repo_root: Path,
    visited: set[str],
    violations: list[ImportViolation],
) -> None:
    """Inspect every repository file that the dotted *paths* resolve to."""
    for path in paths:
        current = chain
        for module_file in _repository_module_files(path, repo_root):
            current = _scan_repository_file(
                module_file, current, repo_root, visited, violations
            )


def _scan_repository_file(
    module_file: str,
    chain: tuple[str, ...],
    repo_root: Path,
    visited: set[str],
    violations: list[ImportViolation],
) -> tuple[str, ...]:
    """Parse one repository file and return the chain extended with it.

    An already-visited file is not parsed again, but it still extends
    the chain, so a submodule diagnostic names the package files that
    import it.
    """
    extended = (*chain, module_file)
    if module_file in visited:
        return extended
    visited.add(module_file)
    tree = _parse_repository_module(module_file, repo_root)
    if tree is not None:
        _scan_imports(
            _module_scope_imports(tree),
            extended,
            repo_root,
            visited,
            violations,
        )
    return extended


def _parse_repository_module(module_path: str, repo_root: Path) -> ast.Module | None:
    """Return the parsed repository module, or None when unreadable."""
    try:
        source = (repo_root / module_path).read_text(encoding="utf-8")
    except (OSError, UnicodeError):
        return None
    try:
        return ast.parse(source)
    except SyntaxError:
        return None


def _repository_module_files(dotted: str, repo_root: Path) -> list[str]:
    """Return the repository files that one dotted import path resolves to.

    A path with a ``scripts.`` prefix resolves from the repository
    root, so ``scripts/__init__.py`` counts as an ancestor package. A
    bare path resolves inside ``scripts/`` (the ``sys.path`` style the
    gate scripts use), so the ``scripts`` package itself does not run.
    The ``__init__.py`` of every ancestor package comes first, then the
    leaf module; each file is included only when it exists, in the
    order in which Python executes it at import time.
    """
    parts = dotted.split(".")
    if parts[0] == "scripts":
        first = 1
        full = parts
    else:
        first = 2
        full = ["scripts", *parts]
    files: list[str] = []
    for depth in range(first, len(full)):
        candidate = f"{'/'.join(full[:depth])}/__init__.py"
        if (repo_root / candidate).is_file():
            files.append(candidate)
    files.extend(_leaf_module_files(full, repo_root))
    return files


def _leaf_module_files(full: list[str], repo_root: Path) -> list[str]:
    """Return the leaf module or package file for the full component path."""
    stem = "/".join(full)
    module = f"{stem}.py"
    if (repo_root / module).is_file():
        return [module]
    package = f"{stem}/__init__.py"
    if (repo_root / package).is_file():
        return [package]
    return []


def _is_allowed_root(root: str, repo_root: Path) -> bool:
    """Return True when *root* is standard library or repository-local."""
    return (
        root in sys.stdlib_module_names
        or root == "scripts"
        or bool(_repository_module_files(root, repo_root))
    )


def _all_imports(tree: ast.Module) -> list[ast.Import | ast.ImportFrom]:
    """Return every import node in *tree*, at any nesting depth."""
    return [
        node
        for node in ast.walk(tree)
        if isinstance(node, (ast.Import, ast.ImportFrom))
    ]


def _module_scope_imports(tree: ast.Module) -> list[ast.Import | ast.ImportFrom]:
    """Return the imports that execute when the module is imported.

    Module-level ``if``, ``try``, and loop blocks run at import time,
    and so do class bodies, so their imports count. Function and async
    function bodies (including methods) run only when called, so their
    imports do not: the repository treats a function-nested import as
    an optional dependency that the module does not require at import
    time (``scripts/lint.py`` imports PyYAML inside ``_load_config``).
    """
    imports: list[ast.Import | ast.ImportFrom] = []
    for statement in tree.body:
        imports.extend(_statement_imports(statement))
    return imports


def _statement_imports(node: ast.AST) -> list[ast.Import | ast.ImportFrom]:
    """Return imports in *node*'s subtree, outside function bodies.

    A class body executes when its class statement runs, so imports
    inside it count; methods are function bodies and do not count.
    """
    if isinstance(node, (ast.Import, ast.ImportFrom)):
        return [node]
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        return []
    imports: list[ast.Import | ast.ImportFrom] = []
    for child in ast.iter_child_nodes(node):
        imports.extend(_statement_imports(child))
    return imports


def _import_violation_message(script: str, found: ImportViolation) -> str:
    """Return the diagnostic message for one third-party import."""
    through = ""
    if len(found.chain) > 1:
        chain = " -> ".join(f'"{path}"' for path in found.chain[1:])
        through = f" through {chain}"
    return (
        f'entry script "{script}" imports "{found.module}" outside the '
        f"standard library and the repository{through}; convert the hook "
        'to language "python" with pinned additional_dependencies, or '
        "move it to .github/pre-commit-ci.yaml"
    )


def _system_script(hook: object) -> str | None:
    """Return the ``scripts/`` path a system hook entry runs, or None."""
    if not isinstance(hook, dict) or hook.get("language") != "system":
        return None
    entry = hook.get("entry")
    if not isinstance(entry, str):
        return None
    return _parse_entry_script(entry)


def _is_checker_script(script: str) -> bool:
    """Return True when *script* names a ``scripts/check_NAME.py`` checker.

    Companion detection only runs on scripts that already parse as
    ``scripts/*.py`` system entries.
    """
    return script.rsplit("/", 1)[-1].startswith("check_")


def _companion_test_script(script: str) -> str:
    """Return the test script required beside one gate checker script."""
    directory, _, name = script.rpartition("/")
    return f"{directory}/test_{name}"


def _check_companions(
    checkers: list[tuple[str, str]],
    entry_scripts: set[str],
    display: str,
) -> list[str]:
    """Require a companion test hook for every gate checker script."""
    diagnostics: list[str] = []
    for subject, script in checkers:
        test_script = _companion_test_script(script)
        if test_script not in entry_scripts:
            message = (
                f'gate checker "{script}" has no companion test hook; add a '
                f'system hook running "{test_script}" so its negative '
                "fixtures run in the rehearsal (ADR-0005)"
            )
            diagnostics.append(_violation(display, subject, message))
    return diagnostics


def check_config(config_path: Path, repo_root: Path = REPO_ROOT) -> GateReport:
    """Check one gate configuration file against the gate invariant.

    *config_path* is the gate configuration to check; the ``scripts/``
    paths that its system entries name resolve against *repo_root*.
    """
    display = _display_path(config_path, repo_root)
    data, error = _load_config(config_path)
    if error is not None:
        return GateReport(display, 0, [f"ERROR: {display}: {error}"])

    repos = data.get("repos")
    if not isinstance(repos, list):
        message = "gate configuration must define a 'repos' list"
        return GateReport(display, 0, [f"ERROR: {display}: {message}"])

    diagnostics = _check_default_stages(data, display)
    local_hooks: list[tuple[object, str]] = []
    for entry in repos:
        entry_diagnostics, hooks = _check_repo_entry(entry, display)
        diagnostics.extend(entry_diagnostics)
        local_hooks.extend(hooks)

    entry_scripts: set[str] = set()
    checkers: list[tuple[str, str]] = []
    hooks_checked = 0
    for hook, repo_value in local_hooks:
        hooks_checked += 1
        subject = _hook_subject(hook, repo_value)
        diagnostics.extend(_check_hook(hook, subject, display, repo_root))
        script = _system_script(hook)
        if script is not None:
            entry_scripts.add(script)
            if _is_checker_script(script):
                checkers.append((subject, script))
    diagnostics.extend(_check_companions(checkers, entry_scripts, display))
    if hooks_checked == 0:
        diagnostics.append(
            f"ERROR: {display}: gate configuration must define at least "
            "one hook; an empty gate protects nothing"
        )
    return GateReport(display, hooks_checked, diagnostics)


def main(argv: list[str] | None = None) -> int:
    """Check the gate configuration and print one diagnostic per violation."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        type=Path,
        default=REPO_ROOT / DEFAULT_CONFIG_NAME,
        help="gate configuration file to check (default: %(default)s)",
    )
    args = parser.parse_args(argv)

    report = check_config(args.config)
    for diagnostic in report.diagnostics:
        print(diagnostic, file=sys.stderr)
    if report.diagnostics:
        return 1
    print(
        f"GATE INVARIANT PASSED: {report.hooks_checked} hook(s) checked "
        f"in {report.config_display}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
