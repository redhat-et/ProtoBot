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
  module or resolve to a repository file. Imports resolve the way
  Python resolves them: bare imports search the entry script's
  directory first and ``scripts/`` second; ``scripts.``-prefixed
  imports resolve from the repository root; a package ``__init__.py``
  wins over a same-named module file; namespace directories are
  allowed along a path whose leaf module file exists; a ``from``
  import of a name that the package initializer binds is an attribute,
  not a submodule; and relative imports resolve against the importing
  file's package, failing closed when they escape ``scripts/`` or
  resolve to no file. Repository files are inspected transitively for
  the imports that execute at import time: class bodies count.
  Function and method bodies are not scanned, because they do not
  execute at import time (``scripts/lint.py`` imports PyYAML inside
  ``_load_config``).
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
THIRD_PARTY_FIX = (
    'convert the hook to language "python" with pinned '
    "additional_dependencies, or move it to .github/pre-commit-ci.yaml"
)
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


@dataclass
class ScanState:
    """State carried through one gate-script import scan.

    ``repo_root`` anchors every path. ``entry_directory`` is the
    directory of the gate entry script: Python puts it first on
    ``sys.path``, so bare imports resolve there before ``scripts/``.
    ``visited`` holds repository files already inspected, and
    ``diagnostics`` collects the rendered import violations.
    """

    repo_root: Path
    entry_directory: str
    visited: set[str]
    diagnostics: list[str]


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
        _violation(display, subject, diagnostic)
        for diagnostic in _import_diagnostics(script, tree, repo_root)
    ]


def _import_diagnostics(script: str, tree: ast.Module, repo_root: Path) -> list[str]:
    """Return every import violation reachable from *script*.

    The entry script is inspected with ``ast.walk``, so imports nested
    in functions count. Repository files that any import loads are
    parsed and inspected for their module-scope imports with the same
    rules. The visited set holds repository-file paths, so each file is
    inspected once and the recursion stays bounded.
    """
    state = ScanState(
        repo_root=repo_root,
        entry_directory=_parent_directory(script),
        visited={script},
        diagnostics=[],
    )
    _scan_imports(_all_imports(tree), (script,), state)
    return state.diagnostics


def _scan_imports(
    imports: list[ast.Import | ast.ImportFrom],
    chain: tuple[str, ...],
    state: ScanState,
) -> None:
    """Inspect *imports* and every repository file they load."""
    for node in imports:
        if isinstance(node, ast.Import):
            for alias in node.names:
                _scan_dotted_import(alias.name, chain, state)
        elif isinstance(node, ast.ImportFrom):
            _scan_from_import(node, chain, state)


def _scan_dotted_import(dotted: str, chain: tuple[str, ...], state: ScanState) -> None:
    """Inspect one dotted module name of an ``import`` statement.

    ``sys.path`` order applies: the entry script's directory first,
    then ``scripts/``. A name that resolves nowhere and whose root is
    not standard library is a third-party import.
    """
    if dotted == "scripts" or dotted.startswith("scripts."):
        files = _module_files_in_base("", dotted, state.repo_root)
        _scan_file_list(files, chain, state)
        return
    root = dotted.split(".")[0]
    for base in (state.entry_directory, "scripts"):
        files = _module_files_in_base(base, dotted, state.repo_root)
        if files:
            _scan_file_list(files, chain, state)
            return
    if root not in sys.stdlib_module_names:
        problem = _third_party_detail(root)
        state.diagnostics.append(_import_diagnostic(chain, problem, THIRD_PARTY_FIX))


def _scan_from_import(
    node: ast.ImportFrom,
    chain: tuple[str, ...],
    state: ScanState,
) -> None:
    """Inspect one ``from`` import statement."""
    if node.level > 0:
        _scan_relative_import(node, chain, state)
        return
    module = node.module or ""
    root = module.split(".")[0]
    bases = [""] if root == "scripts" else [state.entry_directory, "scripts"]
    for base in bases:
        files = _from_import_files(node, module, base, state.repo_root)
        if files:
            _scan_file_list(files, chain, state)
            return
    if root not in sys.stdlib_module_names:
        problem = _third_party_detail(root)
        state.diagnostics.append(_import_diagnostic(chain, problem, THIRD_PARTY_FIX))


def _scan_relative_import(
    node: ast.ImportFrom,
    chain: tuple[str, ...],
    state: ScanState,
) -> None:
    """Inspect one relative ``from`` import statement.

    The import resolves against the package of the file being
    inspected (the last entry of *chain*), one directory up per dot
    beyond the first. A base outside ``scripts/``, or a target that
    resolves to no repository file, fails closed.
    """
    base = _relative_base(node.level, chain[-1])
    if base is None:
        problem = (
            f'uses relative import "{_relative_render(node)}" that escapes '
            "the scripts/ directory"
        )
        state.diagnostics.append(_import_diagnostic(chain, problem, "fix the import"))
        return
    targets = [node.module] if node.module else [alias.name for alias in node.names]
    for target in targets:
        files = _module_files_in_base(base, target, state.repo_root)
        if not files:
            problem = (
                f'uses relative import "{_relative_render(node)}" that resolves '
                f'to no repository file for "{target}" under "{base}"'
            )
            state.diagnostics.append(
                _import_diagnostic(chain, problem, "fix the import or add the module")
            )
            continue
        _scan_file_list(files, chain, state)


def _relative_render(node: ast.ImportFrom) -> str:
    """Return the written form of one relative import, ``"..pkg"``."""
    return f"{'.' * node.level}{node.module or ''}"


def _scan_file_list(files: list[str], chain: tuple[str, ...], state: ScanState) -> None:
    """Inspect every repository file in *files*, extending the chain."""
    current = chain
    for module_file in files:
        current = _scan_repository_file(module_file, current, state)


def _scan_repository_file(
    module_file: str,
    chain: tuple[str, ...],
    state: ScanState,
) -> tuple[str, ...]:
    """Parse one repository file and return the chain extended with it.

    An already-visited file is not parsed again, but it still extends
    the chain, so a submodule diagnostic names the package files that
    import it.
    """
    extended = (*chain, module_file)
    if module_file in state.visited:
        return extended
    state.visited.add(module_file)
    tree = _parse_repository_module(module_file, state.repo_root)
    if tree is not None:
        _scan_imports(_module_scope_imports(tree), extended, state)
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


def _module_files_in_base(base: str, dotted: str, repo_root: Path) -> list[str]:
    """Return the files that *dotted* loads when resolved inside *base*.

    An empty *base* resolves from the repository root (the
    ``scripts.`` import form), so the base directory's own
    ``__init__.py`` counts as an ancestor package. A non-empty *base*
    is a ``sys.path`` entry, so resolution starts inside it. The
    ``__init__.py`` of every ancestor package comes first, then the
    leaf; a namespace directory (no ``__init__.py``) contributes no
    file but does not stop the resolution.
    """
    base_parts = base.split("/") if base else []
    components = base_parts + dotted.split(".")
    files: list[str] = []
    for depth in range(len(base_parts) + 1, len(components)):
        candidate = f"{'/'.join(components[:depth])}/__init__.py"
        if (repo_root / candidate).is_file():
            files.append(candidate)
    files.extend(_leaf_module_files(components, repo_root))
    return files


def _leaf_module_files(components: list[str], repo_root: Path) -> list[str]:
    """Return the leaf package or module file for the full components.

    A package ``__init__.py`` wins over a same-named module file, the
    way Python's finder prefers a package.
    """
    stem = "/".join(components)
    package = f"{stem}/__init__.py"
    if (repo_root / package).is_file():
        return [package]
    module = f"{stem}.py"
    if (repo_root / module).is_file():
        return [module]
    return []


def _from_import_files(
    node: ast.ImportFrom,
    module: str,
    base: str,
    repo_root: Path,
) -> list[str]:
    """Return the files that ``from module import names`` loads in *base*.

    The module's own files come first. Each imported name then adds its
    submodule file, unless the module is a plain file or its package
    initializer binds the name as an attribute. A module with no file
    of its own is a namespace package: its names resolve as submodules.
    """
    files = _module_files_in_base(base, module, repo_root)
    if not files:
        return _submodule_files(node, module, base, repo_root, None)
    leaf = files[-1]
    if not leaf.endswith("/__init__.py"):
        return files
    tree = _parse_repository_module(leaf, repo_root)
    return _dedupe(files + _submodule_files(node, module, base, repo_root, tree))


def _submodule_files(
    node: ast.ImportFrom,
    module: str,
    base: str,
    repo_root: Path,
    bound: ast.Module | None,
) -> list[str]:
    """Return the submodule files for the names of one ``from`` import.

    *bound* is the parsed package initializer: a name it binds is an
    attribute, not a submodule. ``None`` means no initializer exists,
    so every name resolves as a submodule.
    """
    files: list[str] = []
    for alias in node.names:
        if bound is not None and _binds_name(bound, alias.name):
            continue
        files.extend(_module_files_in_base(base, f"{module}.{alias.name}", repo_root))
    return files


def _binds_name(tree: ast.Module, name: str) -> bool:
    """Return True when the module body unconditionally binds *name*."""
    return any(_statement_binds(statement, name) for statement in tree.body)


def _statement_binds(statement: ast.stmt, name: str) -> bool:
    """Return True when one top-level statement binds *name*.

    Assignments, annotations, function and class definitions, and
    import aliases bind names. Statements inside conditional blocks do
    not count: they are not unconditional.
    """
    if isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
        return statement.name == name
    if isinstance(statement, ast.Assign):
        return _assign_binds(statement, name)
    if isinstance(statement, ast.AnnAssign):
        return _annotation_binds(statement, name)
    if isinstance(statement, ast.Import):
        return any(_import_binds(alias, name) for alias in statement.names)
    if isinstance(statement, ast.ImportFrom):
        return any((alias.asname or alias.name) == name for alias in statement.names)
    return False


def _assign_binds(statement: ast.Assign, name: str) -> bool:
    """Return True when one assignment binds *name*."""
    return any(
        isinstance(target, ast.Name) and target.id == name
        for target in statement.targets
    )


def _annotation_binds(statement: ast.AnnAssign, name: str) -> bool:
    """Return True when one annotated assignment binds *name*."""
    return isinstance(statement.target, ast.Name) and statement.target.id == name


def _import_binds(alias: ast.alias, name: str) -> bool:
    """Return True when one ``import`` alias binds *name*.

    ``import pkg.child`` binds ``pkg``, not ``child``; an ``as`` name
    always wins.
    """
    if alias.asname:
        return alias.asname == name
    return alias.name.split(".")[0] == name


def _relative_base(level: int, current_file: str) -> str | None:
    """Return the package directory a relative import resolves against.

    The base is the directory of *current_file*, one directory up per
    dot beyond the first. ``None`` means the base escapes the
    ``scripts/`` tree.
    """
    directory = _parent_directory(current_file)
    for _ in range(level - 1):
        if not directory.startswith("scripts/") or directory == "scripts":
            return None
        directory = _parent_directory(directory)
    if directory == "scripts" or directory.startswith("scripts/"):
        return directory
    return None


def _parent_directory(path: str) -> str:
    """Return the directory part of one repo-relative POSIX path."""
    return path.rsplit("/", 1)[0] if "/" in path else ""


def _dedupe(files: list[str]) -> list[str]:
    """Return *files* without duplicates, keeping the first order."""
    return list(dict.fromkeys(files))


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


def _import_diagnostic(
    chain: tuple[str, ...],
    problem: str,
    fix: str,
) -> str:
    """Return one rendered import diagnostic with its file chain."""
    through = ""
    if len(chain) > 1:
        links = " -> ".join(f'"{path}"' for path in chain[1:])
        through = f" through {links}"
    return f'entry script "{chain[0]}" {problem}{through}; {fix}'


def _third_party_detail(module: str) -> str:
    """Return the problem text for one third-party import."""
    return f'imports "{module}" outside the standard library and the repository'


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
