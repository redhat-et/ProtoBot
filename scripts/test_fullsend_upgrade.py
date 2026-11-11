from __future__ import annotations

import contextlib
import hashlib
import importlib
import io
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

_HELPER_SCRIPTS = (
    Path(__file__).resolve().parent.parent
    / ".agents"
    / "skills"
    / "upgrade-fullsend"
    / "scripts"
)
sys.path.insert(0, str(_HELPER_SCRIPTS))
try:
    updater = importlib.import_module("update_fullsend_pins")
    setup_preview = importlib.import_module("show_fullsend_setup_diff")
finally:
    sys.path.remove(str(_HELPER_SCRIPTS))


class FullsendPinUpdaterTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        self.harness = self.root / ".fullsend" / "harness" / "review.yaml"
        self.workflow = self.root / ".github" / "workflows" / "review.yml"
        self.harness.parent.mkdir(parents=True)
        self.workflow.parent.mkdir(parents=True)

    def test_dry_run_and_check_report_transformed_pins_without_writing(self) -> None:
        harness_original = (
            'base: "https://raw.githubusercontent.com/fullsend-ai/agents/'
            f'v0.9.0/harness/review.yaml#sha256={"a" * 64}"\n'
            "timeout_minutes: 15\n"
        )
        workflow_original = (
            "jobs:\n  check:\n    uses: "
            "fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
            f"{'b' * 40} # v0.9.0\n"
        )
        self.harness.write_text(harness_original, encoding="utf-8")
        self.workflow.write_text(workflow_original, encoding="utf-8")
        harness_body = b"updated harness content\n"
        harness_digest = hashlib.sha256(harness_body).hexdigest()
        workflow_sha = "c" * 40
        expected_harness_pin = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            f"harness/review.yaml#sha256={harness_digest}"
        )
        expected_workflow_pin = (
            "fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
            f"{workflow_sha} # v1.2.3"
        )

        with (
            patch.object(updater, "fetch_bytes", return_value=harness_body),
            patch.object(updater, "resolve_workflow_sha", return_value=workflow_sha),
            contextlib.redirect_stdout(io.StringIO()) as dry_run_output,
        ):
            dry_run_result = updater.main(
                ["--version", "1.2.3", "--root", str(self.root)]
            )

        self.assertEqual(0, dry_run_result)
        self.assertIn(expected_harness_pin, dry_run_output.getvalue())
        self.assertIn(expected_workflow_pin, dry_run_output.getvalue())
        self.assertEqual(harness_original, self.harness.read_text(encoding="utf-8"))
        self.assertEqual(workflow_original, self.workflow.read_text(encoding="utf-8"))

        with (
            patch.object(updater, "fetch_bytes", return_value=harness_body),
            patch.object(updater, "resolve_workflow_sha", return_value=workflow_sha),
            contextlib.redirect_stdout(io.StringIO()),
            contextlib.redirect_stderr(io.StringIO()) as check_error,
        ):
            check_result = updater.main(
                ["--version", "1.2.3", "--root", str(self.root), "--check"]
            )

        self.assertEqual(1, check_result)
        self.assertIn("Fullsend pins are stale.", check_error.getvalue())
        self.assertEqual(harness_original, self.harness.read_text(encoding="utf-8"))
        self.assertEqual(workflow_original, self.workflow.read_text(encoding="utf-8"))

    def test_write_applies_all_resolved_pins(self) -> None:
        harness_original = (
            'base: "https://raw.githubusercontent.com/fullsend-ai/agents/'
            f'v0.9.0/harness/review.yaml#sha256={"a" * 64}"\n'
        )
        workflow_original = (
            "uses: fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
            f"{'b' * 40} # v0.9.0\n"
        )
        self.harness.write_text(harness_original, encoding="utf-8")
        self.workflow.write_text(workflow_original, encoding="utf-8")
        harness_body = b"new harness content\n"
        digest = hashlib.sha256(harness_body).hexdigest()
        workflow_sha = "c" * 40

        with (
            patch.object(updater, "fetch_bytes", return_value=harness_body),
            patch.object(updater, "resolve_workflow_sha", return_value=workflow_sha),
            contextlib.redirect_stdout(io.StringIO()),
        ):
            result = updater.main(
                ["--version", "v1.2.3", "--root", str(self.root), "--write"]
            )

        self.assertEqual(0, result)
        self.assertIn(
            f"v1.2.3/harness/review.yaml#sha256={digest}",
            self.harness.read_text(encoding="utf-8"),
        )
        self.assertIn(
            f"@{workflow_sha} # v1.2.3",
            self.workflow.read_text(encoding="utf-8"),
        )

    def test_explicit_target_cannot_escape_repository_root(self) -> None:
        outside = self.root.parent / f"{self.root.name}-outside.yaml"
        outside.write_text("base: unused\n", encoding="utf-8")
        self.addCleanup(outside.unlink)

        with self.assertRaisesRegex(updater.UpdateError, "escapes repository root"):
            updater.resolve_target(self.root, str(outside))

    def test_explicit_symlink_cannot_escape_repository_root(self) -> None:
        with tempfile.TemporaryDirectory(dir=self.root.parent) as outside_directory:
            outside_file = Path(outside_directory) / "secret.yaml"
            outside_file.write_text("outside data\n", encoding="utf-8")
            self.harness.symlink_to(outside_file)

            with self.assertRaisesRegex(
                updater.UpdateError, "target path escapes repository root"
            ):
                updater.resolve_target(self.root, str(self.harness))

    def test_harness_directory_symlink_cannot_escape_repository_root(self) -> None:
        with tempfile.TemporaryDirectory(dir=self.root.parent) as outside_directory:
            outside_file = Path(outside_directory) / "review.yaml"
            outside_file.write_text("outside data\n", encoding="utf-8")
            self.harness.parent.rmdir()
            self.harness.parent.symlink_to(
                outside_file.parent, target_is_directory=True
            )

            with self.assertRaisesRegex(
                updater.UpdateError, "target directory escapes repository root"
            ):
                updater.discover_targets(self.root, explicit=None, kind="harness")


class FullsendSetupPreviewTests(unittest.TestCase):
    def test_managed_workflow_symlink_cannot_escape_repository_root(self) -> None:
        generated = b"generated"
        with (
            tempfile.TemporaryDirectory() as root_directory,
            tempfile.TemporaryDirectory() as outside_directory,
        ):
            root = Path(root_directory)
            workflow = root / ".github" / "workflows" / "fullsend.yaml"
            workflow.parent.mkdir(parents=True)
            outside_file = Path(outside_directory) / "secret.yaml"
            outside_file.write_bytes(b"private outside content\n")
            workflow.symlink_to(outside_file)
            planned = setup_preview.PlannedFile(
                ".github/workflows/fullsend.yaml", len(generated)
            )
            setup_state = setup_preview.SetupState({}, ())
            error = io.StringIO()
            output = io.StringIO()

            with (
                patch.object(
                    setup_preview, "resolve_cli_version", return_value="v1.2.3"
                ),
                patch.object(
                    setup_preview,
                    "run_setup_dry_run",
                    return_value=("", [planned], setup_state),
                ),
                patch.object(
                    setup_preview, "resolve_workflow_sha", return_value="c" * 40
                ),
                patch.object(
                    setup_preview, "resolve_runner", return_value="ubuntu-latest"
                ),
                patch.object(
                    setup_preview,
                    "resolve_ref_comment_separator",
                    return_value=" # ",
                ),
                patch.object(
                    setup_preview, "render_generated_file", return_value=generated
                ),
                contextlib.redirect_stderr(error),
                contextlib.redirect_stdout(output),
            ):
                result = setup_preview.main(
                    [
                        "example-org/example-repo",
                        "--version",
                        "v1.2.3",
                        "--root",
                        str(root),
                    ]
                )

        self.assertEqual(2, result)
        self.assertIn("managed workflow path escapes repository root", error.getvalue())
        self.assertNotIn(
            "private outside content", output.getvalue() + error.getvalue()
        )

    def test_setup_dry_run_parses_generated_files_variables_and_secrets(self) -> None:
        output = (
            "Would commit: .github/workflows/fullsend.yaml (120 bytes)\n"
            "Would commit: .github/workflows/prioritize.yml (240 bytes)\n"
            "Would set repository variables:\n"
            "  SERVICE_URL = https://example.invalid/api\n\n"
            "Would set 2 repository secrets:\n"
            "  DEPLOY_TOKEN\n"
            "  CLOUD_KEY"
        )
        root = Path("/offline/repository")
        target = "example-org/example-repo"

        with patch.object(setup_preview, "run_command", return_value=output) as run:
            clean_output, files, state = setup_preview.run_setup_dry_run(
                target, root, timeout=5.0
            )

        run.assert_called_once_with(
            ["fullsend", "github", "setup", target, "--dry-run"], root, 5.0
        )
        self.assertEqual(output, clean_output)
        self.assertEqual(
            [
                setup_preview.PlannedFile(".github/workflows/fullsend.yaml", 120),
                setup_preview.PlannedFile(".github/workflows/prioritize.yml", 240),
            ],
            files,
        )
        self.assertEqual(
            setup_preview.SetupState(
                {"SERVICE_URL": "https://example.invalid/api"},
                ("DEPLOY_TOKEN", "CLOUD_KEY"),
            ),
            state,
        )

    def test_setup_state_rejects_malformed_rows_and_missing_sections(self) -> None:
        invalid_outputs = (
            (
                "malformed variable row",
                (
                    "Would set repository variables:\n"
                    "  missing delimiter\n\n"
                    "Would set 0 repository secrets:\n"
                ),
            ),
            (
                "malformed secret row",
                (
                    "Would set repository variables:\n\n"
                    "Would set 1 repository secrets:\n  BAD-NAME\n"
                ),
            ),
            (
                "missing secrets section",
                "Would set repository variables:\n",
            ),
            (
                "missing variables section",
                "Would set 0 repository secrets:\n",
            ),
        )
        for label, output in invalid_outputs:
            with (
                self.subTest(label=label),
                self.assertRaises(setup_preview.UpdateError),
            ):
                setup_preview.parse_setup_state(output)

    def test_workflow_references_are_extracted_from_github_expressions(self) -> None:
        planned = setup_preview.PlannedFile(".github/workflows/review.yml", 0)
        current = (
            b'run: echo "${{ vars.OLD_URL }} ${{ secrets.OLD_TOKEN }}"\n'
            b"# vars.IGNORED is not an expression\n"
        )
        generated = (
            b'run: echo "${{ vars.SERVICE_URL }} ${{ secrets.DEPLOY_TOKEN }}"\n'
            b"# vars.IGNORED is not an expression\n"
        )
        rendered = [(planned, current, generated)]

        self.assertEqual(
            ({"SERVICE_URL"}, {"DEPLOY_TOKEN"}),
            setup_preview.extract_workflow_references(rendered, generated=True),
        )
        self.assertEqual(
            ({"OLD_URL"}, {"OLD_TOKEN"}),
            setup_preview.extract_workflow_references(rendered, generated=False),
        )


if __name__ == "__main__":
    unittest.main()
