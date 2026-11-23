from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

if __package__:
    from scripts import check_gate_invariant as checker
else:
    import check_gate_invariant as checker


REPO_ROOT = Path(__file__).resolve().parent.parent

PINNED_PYTHON_HOOKS = (
    "      - id: check-json\n"
    "        entry: check-json\n"
    "        language: python\n"
    '        additional_dependencies: ["pre-commit-hooks==6.0.0"]\n'
    "      - id: check-json5\n"
    "        entry: check-json5\n"
    "        language: python\n"
    '        additional_dependencies: ["json5==0.17.3", "check-json5==3.4.0"]\n'
)
PYGREP_HOOK = (
    "      - id: text-unicode-replacement-char\n"
    '        entry: "\\uFFFD"\n'
    "        language: pygrep\n"
)
WIDGET_CHECKER_HOOK = (
    "      - id: check-widget\n"
    "        entry: python3 scripts/check_widget.py\n"
    "        language: system\n"
)
WIDGET_CHECKER_HOOK_WITH_FLAG = (
    "      - id: check-widget\n"
    "        entry: python3 scripts/check_widget.py --online-if-ci\n"
    "        language: system\n"
)
WIDGET_COMPANION_HOOK = (
    "      - id: test-check-widget\n"
    "        entry: python3 scripts/test_check_widget.py\n"
    "        language: system\n"
)
TOOLS_CHECKER_HOOK = (
    "      - id: check-widget\n"
    "        entry: python3 scripts/tools/check_widget.py\n"
    "        language: system\n"
)
TOOLS_COMPANION_HOOK = (
    "      - id: test-check-widget\n"
    "        entry: python3 scripts/tools/test_check_widget.py\n"
    "        language: system\n"
)
REMOTE_REPO_CONFIG = (
    "repos:\n"
    "  - repo: https://github.com/pre-commit/pre-commit-hooks\n"
    "    rev: v6.0.0\n"
    "    hooks:\n"
    "      - id: check-json\n"
)
PINNED_DEPENDENCIES_LINE = (
    '        additional_dependencies: ["pre-commit-hooks==6.0.0"]\n'
)
STAGES_MANUAL_LINE = "        stages: [manual]\n"
STAGES_WITH_GATE_LINE = "        stages: [pre-commit, manual]\n"
STDLIB_SCRIPT_SOURCE = (
    "from __future__ import annotations\nimport argparse\nfrom pathlib import Path\n"
)
YAML_IMPORT_SCRIPT_SOURCE = (
    "from __future__ import annotations\nimport os\nimport yaml\n"
)
JSONSCHEMA_FROM_IMPORT_SCRIPT_SOURCE = (
    "from __future__ import annotations\nfrom jsonschema import validate\n"
)
SIBLING_IMPORT_SCRIPT_SOURCE = (
    "from __future__ import annotations\n"
    "import unittest\n"
    "if __package__:\n"
    "    from scripts import helper as checker\n"
    "else:\n"
    "    import helper as checker\n"
)
TRY_REPOSITORY_IMPORT_SCRIPT_SOURCE = (
    "from __future__ import annotations\n"
    "try:\n"
    "    from go_dependency_errors import is_dependency_error\n"
    "except ImportError:\n"
    "    from scripts.go_dependency_errors import is_dependency_error\n"
)
FUNCTION_NESTED_IMPORT_SCRIPT_SOURCE = (
    "from __future__ import annotations\n"
    "import os\n"
    "def run() -> int:\n"
    "    import yaml\n"
    "    return 0\n"
)
TRY_NESTED_IMPORT_SCRIPT_SOURCE = (
    "from __future__ import annotations\n"
    "try:\n"
    "    import yaml\n"
    "except ImportError:\n"
    "    yaml = None\n"
)


def gate_config(*hook_blocks: str) -> str:
    """Return one gate configuration whose single repo entry is local."""
    return "repos:\n  - repo: local\n    hooks:\n" + "".join(hook_blocks)


def python_hook(additional_dependencies: str = "") -> str:
    """Return one ``language: python`` hook block with the given fields."""
    block = (
        "      - id: check-json\n        entry: check-json\n        language: python\n"
    )
    if additional_dependencies:
        return block + additional_dependencies
    return block


def system_hook(entry: str) -> str:
    """Return one ``language: system`` hook block with the given entry."""
    return (
        "      - id: check-widget\n"
        f'        entry: "{entry}"\n'
        "        language: system\n"
    )


class CheckConfigTests(unittest.TestCase):
    """Tests for ``check_config`` against temporary gate configurations."""

    def setUp(self) -> None:
        """Create an isolated repository root for each test."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        self.config_path = self.root / ".pre-commit-config.yaml"

    def write_config(self, yaml_text: str) -> Path:
        """Write the temporary gate configuration and return its path."""
        self.config_path.write_text(yaml_text, encoding="utf-8")
        return self.config_path

    def write_script(self, relative_posix: str, source: str) -> None:
        """Write one script under the temporary repository root."""
        script = self.root / relative_posix
        script.parent.mkdir(parents=True, exist_ok=True)
        script.write_text(source, encoding="utf-8")

    def write_widget_scripts(self, checker_source: str) -> None:
        """Write the widget checker and its companion test script."""
        self.write_script("scripts/check_widget.py", checker_source)
        self.write_script("scripts/test_check_widget.py", STDLIB_SCRIPT_SOURCE)

    def write_tools_scripts(self, checker_source: str) -> None:
        """Write the tools checker and its companion test script."""
        self.write_script("scripts/tools/check_widget.py", checker_source)
        self.write_script("scripts/tools/test_check_widget.py", STDLIB_SCRIPT_SOURCE)

    def run_checker(self) -> checker.GateReport:
        """Check the temporary gate configuration."""
        return checker.check_config(self.config_path, repo_root=self.root)

    def test_valid_configuration_passes(self) -> None:
        """Accept pinned python hooks, a pygrep hook, and a system pair."""
        self.write_config(
            gate_config(
                PINNED_PYTHON_HOOKS,
                PYGREP_HOOK,
                WIDGET_CHECKER_HOOK,
                WIDGET_COMPANION_HOOK,
            )
        )
        self.write_widget_scripts(STDLIB_SCRIPT_SOURCE)

        report = self.run_checker()

        self.assertEqual([], report.diagnostics)
        self.assertEqual(5, report.hooks_checked)

    def test_remote_repository_fails_and_names_the_repo(self) -> None:
        """Reject a remote repository entry and name its URL."""
        self.write_config(REMOTE_REPO_CONFIG)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(2, len(diagnostics))
        remote = [
            diagnostic
            for diagnostic in diagnostics
            if "https://github.com/pre-commit/pre-commit-hooks" in diagnostic
        ]
        self.assertEqual(1, len(remote))
        self.assertTrue(remote[0].startswith("ERROR: "))

    def test_python_hook_without_additional_dependencies_fails(self) -> None:
        """Reject a python hook with missing or empty dependencies."""
        cases = {
            "missing": "",
            "empty list": "        additional_dependencies: []\n",
        }
        for label, additional in cases.items():
            with self.subTest(case=label):
                self.write_config(gate_config(python_hook(additional)))

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("additional_dependencies", diagnostics[0])

    def test_unpinned_dependency_fails(self) -> None:
        """Reject a python dependency that pins no exact version."""
        self.write_config(
            gate_config(python_hook('        additional_dependencies: ["ruff"]\n'))
        )

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("ruff", diagnostics[0])
        self.assertIn("==", diagnostics[0])

    def test_dependency_url_and_git_reference_fail(self) -> None:
        """Reject direct URL and Git reference dependencies."""
        cases = {
            "URL": "https://github.com/psf/requests/archive/v2.31.0.tar.gz",
            "Git reference": (
                "git+https://github.com/psf/requests@v2.31.0#egg=requests==2.31.0"
            ),
        }
        for label, dependency in cases.items():
            with self.subTest(case=label):
                line = f'        additional_dependencies: ["{dependency}"]\n'
                self.write_config(gate_config(python_hook(line)))

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("requests", diagnostics[0])
                self.assertIn("name==version", diagnostics[0])

    def test_system_script_importing_yaml_fails_and_names_module(self) -> None:
        """Reject a gate script that imports a third-party module."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts(YAML_IMPORT_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/check_widget.py", diagnostics[0])

    def test_system_script_from_importing_jsonschema_fails(self) -> None:
        """Reject a third-party module imported with ``from``."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts(JSONSCHEMA_FROM_IMPORT_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("jsonschema", diagnostics[0])
        self.assertIn("scripts/check_widget.py", diagnostics[0])

    def test_system_script_conditional_sibling_import_passes(self) -> None:
        """Allow the repository sibling-import pattern under ``if __package__:``."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts(SIBLING_IMPORT_SCRIPT_SOURCE)
        self.write_script("scripts/helper.py", STDLIB_SCRIPT_SOURCE)

        report = self.run_checker()

        self.assertEqual([], report.diagnostics)

    def test_system_script_try_import_of_repository_module_passes(self) -> None:
        """Allow a ``try:`` import of a module under ``scripts/``."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts(TRY_REPOSITORY_IMPORT_SCRIPT_SOURCE)
        self.write_script("scripts/go_dependency_errors.py", STDLIB_SCRIPT_SOURCE)

        report = self.run_checker()

        self.assertEqual([], report.diagnostics)

    def test_system_script_function_nested_import_fails(self) -> None:
        """Reject a third-party module imported inside a function."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts(FUNCTION_NESTED_IMPORT_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/check_widget.py", diagnostics[0])

    def test_system_script_try_nested_import_fails(self) -> None:
        """Reject a third-party module imported inside a ``try:`` block."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts(TRY_NESTED_IMPORT_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/check_widget.py", diagnostics[0])

    def test_transitive_third_party_import_fails(self) -> None:
        """Reject a third-party import inside an imported repository module."""
        sources = {
            "bare import": "import helper\n",
            "scripts package import": "from scripts import helper\n",
        }
        for label, checker_source in sources.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts(checker_source)
                self.write_script("scripts/helper.py", "import yaml\n")

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("yaml", diagnostics[0])
                self.assertIn("scripts/helper.py", diagnostics[0])
                self.assertIn("scripts/check_widget.py", diagnostics[0])

    def test_circular_repository_import_stays_bounded(self) -> None:
        """Stay bounded when repository modules import each other."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("import helper\n")
        self.write_script("scripts/helper.py", "import check_widget\n")

        report = self.run_checker()

        self.assertEqual([], report.diagnostics)

    def test_transitive_package_child_import_fails(self) -> None:
        """Reject a third-party import in a package submodule."""
        sources = {
            "dotted import": "import pkg.child\n",
            "from import": "from pkg import child\n",
        }
        for label, checker_source in sources.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts(checker_source)
                self.write_script("scripts/pkg/__init__.py", "")
                self.write_script("scripts/pkg/child.py", "import yaml\n")

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("yaml", diagnostics[0])
                self.assertIn("scripts/pkg/__init__.py", diagnostics[0])
                self.assertIn("scripts/pkg/child.py", diagnostics[0])

    def test_repository_module_class_body_import_fails(self) -> None:
        """Reject a third-party import in a repository module class body."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("import helper\n")
        self.write_script("scripts/helper.py", "class Settings:\n    import yaml\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/helper.py", diagnostics[0])

    def test_repository_module_function_nested_import_passes(self) -> None:
        """Allow a function-nested import in a repository module."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("import helper\n")
        self.write_script("scripts/helper.py", "def load():\n    import yaml\n")

        self.assertEqual([], self.run_checker().diagnostics)

    def test_relative_import_resolves_and_scans_helper(self) -> None:
        """Resolve a relative import against the importing package."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("import pkg.child\n")
        self.write_script("scripts/pkg/__init__.py", "")
        self.write_script("scripts/pkg/child.py", "from . import helper\n")
        self.write_script("scripts/pkg/helper.py", "import yaml\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/pkg/helper.py", diagnostics[0])

    def test_relative_import_forms_resolve(self) -> None:
        """Resolve each relative import form against the right package."""
        sources = {
            "from . import helper": "from . import helper\n",
            "from .helper import value": "from .helper import value\n",
        }
        for label, child_source in sources.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts("import pkg.child\n")
                self.write_script("scripts/pkg/__init__.py", "")
                self.write_script("scripts/pkg/child.py", child_source)
                self.write_script("scripts/pkg/helper.py", "import os\n")

                self.assertEqual([], self.run_checker().diagnostics)

    def test_relative_import_parent_package_sibling_resolves(self) -> None:
        """Resolve ``from ..sibling`` inside ``pkg.sub.child`` to ``pkg.sibling``."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("import pkg.sub.child\n")
        self.write_script("scripts/pkg/__init__.py", "")
        self.write_script("scripts/pkg/sub/__init__.py", "")
        self.write_script("scripts/pkg/sub/child.py", "from ..sibling import value\n")
        self.write_script("scripts/pkg/sibling.py", "import yaml\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/pkg/sibling.py", diagnostics[0])

    def test_relative_import_above_top_level_fails(self) -> None:
        """Reject a relative import that goes above the top-level package."""
        sources = {
            "two dots in pkg.child": "from ..other import value\n",
            "three dots in pkg.child": "from ... import value\n",
        }
        for label, child_source in sources.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts("import pkg.child\n")
                self.write_script("scripts/pkg/__init__.py", "")
                self.write_script("scripts/pkg/child.py", child_source)
                self.write_script("scripts/other.py", "import os\n")

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("above the top-level package", diagnostics[0])

    def test_relative_import_missing_target_fails(self) -> None:
        """Reject a relative import that resolves to no repository file."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("import pkg.child\n")
        self.write_script("scripts/pkg/__init__.py", "")
        self.write_script("scripts/pkg/child.py", "from . import missing_helper\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("missing_helper", diagnostics[0])
        self.assertIn("no repository file", diagnostics[0])

    def test_from_import_of_bound_name_skips_submodule(self) -> None:
        """Treat a name the package initializer binds as an attribute."""
        initializers = {
            "assignment": "child = None\n",
            "function": "def child():\n    return 1\n",
            "class": "class child:\n    pass\n",
            "import alias": "import os as child\n",
        }
        for label, initializer in initializers.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts("from pkg import child\n")
                self.write_script("scripts/pkg/__init__.py", initializer)
                self.write_script("scripts/pkg/child.py", "import yaml\n")

                self.assertEqual([], self.run_checker().diagnostics)

    def test_from_import_of_unbound_name_loads_submodule(self) -> None:
        """Scan the submodule that a package initializer does not bind."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_widget_scripts("from pkg import child\n")
        self.write_script("scripts/pkg/__init__.py", "other = None\n")
        self.write_script("scripts/pkg/child.py", "import yaml\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/pkg/child.py", diagnostics[0])

    def test_package_initializer_wins_over_module_file(self) -> None:
        """Load the package initializer before a same-named module file."""
        cases = {
            "initializer is loaded": ("import os\n", "import yaml\n", False),
            "module file is not loaded": ("import yaml\n", "import os\n", True),
        }
        for label, (initializer, module_source, fails) in cases.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts("import pkg\n")
                self.write_script("scripts/pkg/__init__.py", initializer)
                self.write_script("scripts/pkg.py", module_source)

                diagnostics = self.run_checker().diagnostics

                if fails:
                    self.assertEqual(1, len(diagnostics))
                    self.assertIn("yaml", diagnostics[0])
                    self.assertIn("scripts/pkg/__init__.py", diagnostics[0])
                else:
                    self.assertEqual([], diagnostics)

    def test_namespace_package_child_resolves(self) -> None:
        """Resolve a submodule through a package without an initializer."""
        sources = {
            "dotted import": "import pkg.child\n",
            "from import": "from pkg import child\n",
        }
        for label, checker_source in sources.items():
            with self.subTest(case=label):
                self.write_config(
                    gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK)
                )
                self.write_widget_scripts(checker_source)
                self.write_script("scripts/pkg/child.py", "import yaml\n")

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("yaml", diagnostics[0])
                self.assertIn("scripts/pkg/child.py", diagnostics[0])

    def test_entry_directory_resolves_bare_import_first(self) -> None:
        """Resolve a bare import from the entry script's directory first."""
        self.write_config(gate_config(TOOLS_CHECKER_HOOK, TOOLS_COMPANION_HOOK))
        self.write_tools_scripts("import helper\n")
        self.write_script("scripts/tools/helper.py", "import yaml\n")
        self.write_script("scripts/helper.py", "import os\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("yaml", diagnostics[0])
        self.assertIn("scripts/tools/helper.py", diagnostics[0])
        self.assertNotIn("scripts/helper.py", diagnostics[0])

    def test_bare_import_outside_entry_directory_fails(self) -> None:
        """Reject a bare import that only scripts/ would satisfy."""
        self.write_config(gate_config(TOOLS_CHECKER_HOOK, TOOLS_COMPANION_HOOK))
        self.write_tools_scripts("import helper\n")
        self.write_script("scripts/helper.py", "import os\n")

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("helper", diagnostics[0])
        self.assertIn("outside the standard library and the repository", diagnostics[0])

    def test_system_entry_with_shell_operator_fails(self) -> None:
        """Reject chained commands, pipelines, and command substitution."""
        entries = {
            "chained command": "python3 scripts/check_widget.py && node evil.js",
            "pipeline": "python3 scripts/check_widget.py | tee out.txt",
            "command substitution": "python3 scripts/check_widget.py $(node evil.js)",
        }
        for label, entry in entries.items():
            with self.subTest(case=label):
                self.write_config(gate_config(system_hook(entry)))

                diagnostics = self.run_checker().diagnostics

                self.assertEqual(1, len(diagnostics))
                self.assertIn("check-widget", diagnostics[0])
                self.assertIn("python3", diagnostics[0])
                self.assertIn("plain arguments", diagnostics[0])

    def test_system_entry_with_plain_flag_passes(self) -> None:
        """Accept a plain flag argument after the script path."""
        self.write_config(
            gate_config(WIDGET_CHECKER_HOOK_WITH_FLAG, WIDGET_COMPANION_HOOK)
        )
        self.write_widget_scripts(STDLIB_SCRIPT_SOURCE)

        report = self.run_checker()

        self.assertEqual([], report.diagnostics)

    def test_system_entry_without_python3_script_fails(self) -> None:
        """Reject a system entry that does not run a python3 script."""
        self.write_config(gate_config(system_hook("node scripts/check_widget.js")))

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("check-widget", diagnostics[0])
        self.assertIn("python3", diagnostics[0])

    def test_system_entry_with_missing_script_fails(self) -> None:
        """Reject a system entry whose script does not exist."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK, WIDGET_COMPANION_HOOK))
        self.write_script("scripts/test_check_widget.py", STDLIB_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("scripts/check_widget.py", diagnostics[0])

    def test_system_entry_with_parent_directory_escape_fails(self) -> None:
        """Reject an entry script that escapes scripts/ with ``..``."""
        self.write_config(gate_config(system_hook("python3 scripts/../outside.py")))
        self.write_script("outside.py", STDLIB_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("scripts/../outside.py", diagnostics[0])
        self.assertIn("escapes the scripts/ directory", diagnostics[0])

    def test_system_entry_with_symlink_escape_fails(self) -> None:
        """Reject a symlink under scripts/ that points outside the tree."""
        outside = tempfile.TemporaryDirectory()
        self.addCleanup(outside.cleanup)
        target = Path(outside.name) / "outside.py"
        target.write_text(STDLIB_SCRIPT_SOURCE, encoding="utf-8")
        self.write_config(gate_config(system_hook("python3 scripts/escape.py")))
        link = self.root / "scripts" / "escape.py"
        link.parent.mkdir(parents=True, exist_ok=True)
        link.symlink_to(target)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("scripts/escape.py", diagnostics[0])
        self.assertIn("escapes the scripts/ directory", diagnostics[0])

    def test_node_language_hook_fails(self) -> None:
        """Reject a hook whose language is not allowed in the gate."""
        hook = (
            "      - id: prettier\n"
            "        entry: prettier --write\n"
            "        language: node\n"
        )
        self.write_config(gate_config(hook))

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn('"node"', diagnostics[0])
        self.assertIn("python", diagnostics[0])

    def test_hook_without_id_fails(self) -> None:
        """Reject a hook that sets no id."""
        hook = "      - entry: check-json\n        language: pygrep\n"
        self.write_config(gate_config(hook))

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn('must set an "id"', diagnostics[0])

    def test_hook_without_language_fails(self) -> None:
        """Reject a hook that sets no language."""
        hook = "      - id: check-json\n        entry: check-json\n"
        self.write_config(gate_config(hook))

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("must set a language", diagnostics[0])

    def test_hook_stages_without_pre_commit_fails(self) -> None:
        """Reject a hook whose stages list omits the pre-commit stage."""
        self.write_config(
            gate_config(python_hook(PINNED_DEPENDENCIES_LINE) + STAGES_MANUAL_LINE)
        )

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("check-json", diagnostics[0])
        self.assertIn("pre-commit", diagnostics[0])
        self.assertIn("cannot be rehearsed", diagnostics[0])

    def test_hook_stages_with_pre_commit_passes(self) -> None:
        """Accept a hook whose stages list contains the pre-commit stage."""
        self.write_config(
            gate_config(python_hook(PINNED_DEPENDENCIES_LINE) + STAGES_WITH_GATE_LINE)
        )

        self.assertEqual([], self.run_checker().diagnostics)

    def test_hook_without_stages_passes(self) -> None:
        """Accept a hook that sets no stages list."""
        self.write_config(gate_config(python_hook(PINNED_DEPENDENCIES_LINE)))

        self.assertEqual([], self.run_checker().diagnostics)

    def test_default_stages_without_pre_commit_fails(self) -> None:
        """Reject a default_stages list without the pre-commit stage."""
        self.write_config(
            "default_stages: [manual]\n"
            + gate_config(python_hook(PINNED_DEPENDENCIES_LINE))
        )

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("default_stages", diagnostics[0])
        self.assertIn("pre-commit", diagnostics[0])

    def test_default_stages_with_pre_commit_passes(self) -> None:
        """Accept a default_stages list with the pre-commit stage."""
        self.write_config(
            "default_stages: [pre-commit]\n"
            + gate_config(python_hook(PINNED_DEPENDENCIES_LINE))
        )

        self.assertEqual([], self.run_checker().diagnostics)

    def test_checker_without_companion_test_hook_fails(self) -> None:
        """Reject a gate checker that has no companion test hook."""
        self.write_config(gate_config(WIDGET_CHECKER_HOOK))
        self.write_script("scripts/check_widget.py", STDLIB_SCRIPT_SOURCE)

        diagnostics = self.run_checker().diagnostics

        self.assertEqual(1, len(diagnostics))
        self.assertIn("check-widget", diagnostics[0])
        self.assertIn("scripts/test_check_widget.py", diagnostics[0])

    def test_missing_config_file_fails(self) -> None:
        """Reject a gate configuration file that does not exist."""
        report = self.run_checker()

        self.assertEqual(1, len(report.diagnostics))
        self.assertTrue(report.diagnostics[0].startswith("ERROR: "))
        self.assertIn(".pre-commit-config.yaml", report.diagnostics[0])

    def test_empty_repos_list_fails(self) -> None:
        """Reject a gate configuration that defines no hooks."""
        self.write_config("repos: []\n")

        report = self.run_checker()

        self.assertEqual(1, len(report.diagnostics))
        self.assertIn("at least one hook", report.diagnostics[0])
        self.assertEqual(0, report.hooks_checked)


class CheckCliTests(unittest.TestCase):
    """Tests for the command-line entry point."""

    def setUp(self) -> None:
        """Create an isolated repository root for each test."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        self.config_path = self.root / ".pre-commit-config.yaml"

    def run_cli(self) -> subprocess.CompletedProcess[str]:
        """Run the checker CLI against the temporary gate configuration."""
        return subprocess.run(
            [
                sys.executable,
                str(REPO_ROOT / "scripts" / "check_gate_invariant.py"),
                "--config",
                str(self.config_path),
            ],
            capture_output=True,
            text=True,
            check=False,
        )

    def test_cli_accepts_valid_configuration(self) -> None:
        """Exit 0 and print the passed line for a valid configuration."""
        self.config_path.write_text(
            gate_config(PINNED_PYTHON_HOOKS, PYGREP_HOOK), encoding="utf-8"
        )

        result = self.run_cli()

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("GATE INVARIANT PASSED", result.stdout)
        self.assertIn("3 hook(s)", result.stdout)
        self.assertEqual("", result.stderr)

    def test_cli_rejects_remote_repository(self) -> None:
        """Exit 1 and print one ERROR diagnostic per violation to stderr."""
        self.config_path.write_text(REMOTE_REPO_CONFIG, encoding="utf-8")

        result = self.run_cli()

        self.assertEqual(1, result.returncode)
        self.assertIn("ERROR: ", result.stderr)
        self.assertIn("https://github.com/pre-commit/pre-commit-hooks", result.stderr)
        self.assertEqual("", result.stdout)


class RepositoryConfigTests(unittest.TestCase):
    """Tests for the repository's own gate configuration."""

    def test_repository_gate_configuration_satisfies_the_invariant(self) -> None:
        """The repository gate configuration must pass the invariant."""
        report = checker.check_config(checker.REPO_ROOT / checker.DEFAULT_CONFIG_NAME)

        self.assertEqual([], report.diagnostics)


if __name__ == "__main__":
    unittest.main()
