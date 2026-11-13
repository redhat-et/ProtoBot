from __future__ import annotations

import contextlib
import hashlib
import importlib
import io
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

_REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
_HELPER_SCRIPTS = (
    _REPOSITORY_ROOT / ".agents" / "skills" / "upgrade-fullsend" / "scripts"
)
sys.path.insert(0, str(_REPOSITORY_ROOT))
sys.path.insert(0, str(_HELPER_SCRIPTS))
try:
    updater = importlib.import_module("update_fullsend_pins")
    setup_preview = importlib.import_module("show_fullsend_setup_diff")
    checker = importlib.import_module("scripts.check_harness_integrity")
finally:
    sys.path.remove(str(_HELPER_SCRIPTS))
    sys.path.remove(str(_REPOSITORY_ROOT))


_TEST_HARNESS_URL = (
    "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/harness/review.yaml"
)


class _FakeResponse(io.BytesIO):
    def __init__(self, content: bytes, response_url: str) -> None:
        super().__init__(content)
        self._response_url = response_url
        self.read_sizes: list[int] = []
        self.bytes_read = 0
        self.socket_timeouts: list[float] = []
        self.fp = SimpleNamespace(
            raw=SimpleNamespace(
                _sock=SimpleNamespace(settimeout=self.socket_timeouts.append)
            )
        )

    def geturl(self) -> str:
        return self._response_url

    def read1(self, size: int = -1) -> bytes:
        self.read_sizes.append(size)
        content = self.read(size)
        self.bytes_read += len(content)
        return content


class FullsendPinUpdaterTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name).resolve()
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

    def test_workflow_update_preserves_quoted_scalars_and_comment_formatting(
        self,
    ) -> None:
        prefix = "fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
        single_sha = "a" * 40
        double_sha = "b" * 40
        unquoted_sha = "d" * 40
        workflow_original = (
            "jobs:\n"
            f"  single:\n    uses: '{prefix}{single_sha}' # v0.9.0\n"
            f'  double:\n    uses: "{prefix}{double_sha}"  #  v0.9.1\n'
            f"  unquoted:\n    uses: {prefix}{unquoted_sha}\n"
        )
        self.workflow.write_text(workflow_original, encoding="utf-8")
        workflow_sha = "c" * 40

        plan = updater.update_workflow_file(self.workflow, "v1.2.3", workflow_sha)

        expected = (
            "jobs:\n"
            f"  single:\n    uses: '{prefix}{workflow_sha}' # v1.2.3\n"
            f'  double:\n    uses: "{prefix}{workflow_sha}"  #  v1.2.3\n'
            f"  unquoted:\n    uses: {prefix}{workflow_sha} # v1.2.3\n"
        )
        self.assertIsNotNone(plan)
        assert plan is not None
        self.assertEqual(expected, plan.updated.decode("utf-8"))

    def test_comment_only_workflow_pin_is_not_counted_or_rewritten(self) -> None:
        harness_original = (
            'base: "https://raw.githubusercontent.com/fullsend-ai/agents/'
            f'v0.9.0/harness/review.yaml#sha256={"a" * 64}"\n'
        )
        workflow_original = (
            "# uses: fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
            f"{'b' * 40} # v0.9.0\n"
        )
        self.harness.write_text(harness_original, encoding="utf-8")
        self.workflow.write_text(workflow_original, encoding="utf-8")

        with (
            patch.object(updater, "fetch_bytes", return_value=b"harness\n"),
            patch.object(updater, "resolve_workflow_sha", return_value="c" * 40),
            contextlib.redirect_stdout(io.StringIO()),
            contextlib.redirect_stderr(io.StringIO()) as error,
        ):
            result = updater.main(
                ["--version", "v1.2.3", "--root", str(self.root), "--write"]
            )

        self.assertEqual(2, result)
        self.assertIn(
            "ERROR: no Fullsend reusable workflow pins found", error.getvalue()
        )
        self.assertEqual(workflow_original, self.workflow.read_text(encoding="utf-8"))

    def test_discover_targets_only_includes_yaml_extensions(self) -> None:
        for kind, directory in (
            ("harness", self.harness.parent),
            ("workflow", self.workflow.parent),
        ):
            filenames = (
                "accepted.yaml",
                "accepted.yml",
                "ignored.yxml",
                "ignored.yaaml",
            )
            for filename in filenames:
                (directory / filename).write_text("", encoding="utf-8")
            expected = sorted(directory / filename for filename in filenames[:2])

            self.assertEqual(
                expected,
                updater.discover_targets(self.root, explicit=None, kind=kind),
            )

    def test_fetch_bytes_accepts_matching_https_response(self) -> None:
        requested_url = _TEST_HARNESS_URL
        response = _FakeResponse(b"release content", requested_url)
        with (
            patch.object(updater, "build_opener") as build_opener,
            patch.object(updater, "monotonic", return_value=0.0),
        ):
            build_opener.return_value.open.return_value = response
            content = updater.fetch_bytes(requested_url, timeout=5.0)

        self.assertEqual(b"release content", content)
        self.assertIsInstance(
            build_opener.call_args.args[0], updater._NoRedirectHandler
        )
        open_call = build_opener.return_value.open.call_args
        self.assertEqual(requested_url, open_call.args[0].full_url)
        self.assertEqual(5.0, open_call.kwargs["timeout"])
        self.assertEqual([5.0, 5.0], response.socket_timeouts)

    def test_fetch_bytes_rejects_redirects_before_following(self) -> None:
        requested_url = _TEST_HARNESS_URL
        redirected_url = "https://example.invalid/redirected.yaml"
        requested_urls: list[str] = []

        def build_redirecting_opener(
            handler: updater._NoRedirectHandler,
        ) -> object:
            class RedirectingOpener:
                def open(self, request: updater.Request, timeout: float) -> None:
                    requested_urls.append(request.full_url)
                    handler.redirect_request(
                        request, None, 302, "Found", {}, redirected_url
                    )

            return RedirectingOpener()

        with (
            patch.object(
                updater, "build_opener", side_effect=build_redirecting_opener
            ) as build_opener,
            self.assertRaisesRegex(updater.UpdateError, "redirect rejected"),
        ):
            updater.fetch_bytes(requested_url, timeout=5.0)

        self.assertEqual([requested_url], requested_urls)
        self.assertIsInstance(
            build_opener.call_args.args[0], updater._NoRedirectHandler
        )

    def test_fetch_bytes_rejects_non_https_requests(self) -> None:
        with (
            patch.object(updater, "build_opener") as build_opener,
            self.assertRaisesRegex(updater.UpdateError, "non-HTTPS"),
        ):
            updater.fetch_bytes("http://example.invalid/review.yaml", timeout=5.0)

        build_opener.assert_not_called()

    def test_fetch_bytes_rejects_an_unexpected_final_url(self) -> None:
        requested_url = _TEST_HARNESS_URL
        response = _FakeResponse(
            b"must not be read", "https://example.invalid/redirected.yaml"
        )
        with patch.object(updater, "build_opener") as build_opener:
            build_opener.return_value.open.return_value = response
            with self.assertRaisesRegex(updater.UpdateError, "final response URL"):
                updater.fetch_bytes(requested_url, timeout=5.0)

        self.assertEqual(0, response.bytes_read)

    def test_fetch_bytes_rejects_oversized_responses(self) -> None:
        requested_url = _TEST_HARNESS_URL
        response = _FakeResponse(b"x" * (updater.MAX_RESPONSE_BYTES + 1), requested_url)
        with (
            patch.object(updater, "build_opener") as build_opener,
            patch.object(updater, "monotonic", return_value=0.0),
        ):
            build_opener.return_value.open.return_value = response
            with self.assertRaisesRegex(
                updater.UpdateError,
                f"{updater.MAX_RESPONSE_BYTES}-byte limit",
            ):
                updater.fetch_bytes(requested_url, timeout=5.0)

        self.assertEqual(updater.MAX_RESPONSE_BYTES + 1, response.bytes_read)
        self.assertEqual(1, response.read_sizes[-1])

    def test_fetch_bytes_enforces_total_read_deadline(self) -> None:
        requested_url = _TEST_HARNESS_URL
        response = _FakeResponse(b"release content", requested_url)
        with (
            patch.object(updater, "build_opener") as build_opener,
            patch.object(
                updater,
                "monotonic",
                side_effect=(
                    0.0,
                    0.0,
                    updater.NETWORK_READ_DEADLINE_SECONDS + 0.1,
                ),
            ),
        ):
            build_opener.return_value.open.return_value = response
            with self.assertRaisesRegex(updater.UpdateError, "deadline"):
                updater.fetch_bytes(requested_url, timeout=5.0)

        self.assertEqual([5.0], response.socket_timeouts)

    def test_updater_errors_use_uppercase_prefix(self) -> None:
        with (
            contextlib.redirect_stdout(io.StringIO()),
            contextlib.redirect_stderr(io.StringIO()) as error,
        ):
            result = updater.main(
                ["--version", "not-a-version", "--root", str(self.root)]
            )

        self.assertEqual(2, result)
        self.assertTrue(error.getvalue().startswith("ERROR: invalid version"))

    def test_write_error_returns_status_two_without_multi_file_rollback(
        self,
    ) -> None:
        old_harness_digest = "a" * 64
        old_workflow_sha = "b" * 40
        harness_original = (
            'base: "https://raw.githubusercontent.com/fullsend-ai/agents/'
            f'v0.9.0/harness/review.yaml#sha256={old_harness_digest}"\n'
        )
        workflow_original = (
            "uses: fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
            f"{old_workflow_sha} # v0.9.0\n"
        )
        self.harness.write_text(harness_original, encoding="utf-8")
        self.workflow.write_text(workflow_original, encoding="utf-8")
        harness_body = b"updated harness content\n"
        harness_digest = hashlib.sha256(harness_body).hexdigest()
        workflow_sha = "c" * 40
        real_replace = updater.os.replace

        def fail_workflow_replace(source: str, destination: str | Path) -> None:
            if Path(destination) == self.workflow:
                raise OSError("simulated replace failure")
            real_replace(source, destination)

        with (
            patch.object(updater, "fetch_bytes", return_value=harness_body),
            patch.object(updater, "resolve_workflow_sha", return_value=workflow_sha),
            patch.object(updater.os, "replace", side_effect=fail_workflow_replace),
            contextlib.redirect_stdout(io.StringIO()),
            contextlib.redirect_stderr(io.StringIO()) as error,
        ):
            result = updater.main(
                ["--version", "v1.2.3", "--root", str(self.root), "--write"]
            )

        self.assertEqual(2, result)
        self.assertIn(
            "ERROR: failed to write .github/workflows/review.yml: "
            "simulated replace failure",
            error.getvalue(),
        )
        self.assertIn(
            f"v1.2.3/harness/review.yaml#sha256={harness_digest}",
            self.harness.read_text(encoding="utf-8"),
        )
        self.assertEqual(workflow_original, self.workflow.read_text(encoding="utf-8"))

    def test_write_preserves_file_changed_during_network_planning(self) -> None:
        harness_original = (
            'base: "https://raw.githubusercontent.com/fullsend-ai/agents/'
            f'v0.9.0/harness/review.yaml#sha256={"a" * 64}"\n'
        )
        newer_harness = harness_original + "# changed during planning\n"
        workflow_original = (
            "uses: fullsend-ai/fullsend/.github/workflows/reusable-ci.yml@"
            f"{'b' * 40} # v0.9.0\n"
        )
        self.harness.write_text(harness_original, encoding="utf-8")
        self.workflow.write_text(workflow_original, encoding="utf-8")

        def mutate_harness_during_fetch(url: str, timeout: float) -> bytes:
            self.harness.write_text(newer_harness, encoding="utf-8")
            return b"updated harness content\n"

        with (
            patch.object(
                updater, "fetch_bytes", side_effect=mutate_harness_during_fetch
            ),
            patch.object(updater, "resolve_workflow_sha", return_value="c" * 40),
            contextlib.redirect_stdout(io.StringIO()),
            contextlib.redirect_stderr(io.StringIO()) as error,
        ):
            result = updater.main(
                ["--version", "v1.2.3", "--root", str(self.root), "--write"]
            )

        self.assertEqual(2, result)
        self.assertIn(
            ".fullsend/harness/review.yaml changed since planning", error.getvalue()
        )
        self.assertIn("re-run the updater to plan again", error.getvalue())
        self.assertEqual(newer_harness, self.harness.read_text(encoding="utf-8"))
        self.assertEqual(workflow_original, self.workflow.read_text(encoding="utf-8"))

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

    def test_resolve_workflow_sha_uses_lightweight_tag_sha(self) -> None:
        expected_sha = "a" * 40
        result = subprocess.CompletedProcess(
            args=["git", "ls-remote"],
            returncode=0,
            stdout=f"{expected_sha}\trefs/tags/v1.2.3\n",
            stderr="",
        )

        with patch.object(updater.subprocess, "run", return_value=result):
            actual_sha = updater.resolve_workflow_sha("v1.2.3", timeout=5.0)

        self.assertEqual(expected_sha, actual_sha)

    def test_resolve_workflow_sha_prefers_peeled_annotated_tag_sha(self) -> None:
        tag_sha = "a" * 40
        commit_sha = "b" * 40
        result = subprocess.CompletedProcess(
            args=["git", "ls-remote"],
            returncode=0,
            stdout=(
                f"{tag_sha}\trefs/tags/v1.2.3\n{commit_sha}\trefs/tags/v1.2.3^{{}}\n"
            ),
            stderr="",
        )

        with patch.object(updater.subprocess, "run", return_value=result):
            actual_sha = updater.resolve_workflow_sha("v1.2.3", timeout=5.0)

        self.assertEqual(commit_sha, actual_sha)

    def test_resolve_workflow_sha_rejects_missing_tag(self) -> None:
        result = subprocess.CompletedProcess(
            args=["git", "ls-remote"], returncode=0, stdout="", stderr=""
        )

        with (
            patch.object(updater.subprocess, "run", return_value=result),
            self.assertRaisesRegex(
                updater.UpdateError, "release tag v1.2.3 was not found"
            ),
        ):
            updater.resolve_workflow_sha("v1.2.3", timeout=5.0)

    def test_written_prerelease_pin_is_accepted_by_integrity_parser(self) -> None:
        version = "v1.2.3-rc.1+build.2"
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
        harness_body = b"updated harness content\n"
        expected_digest = hashlib.sha256(harness_body).hexdigest()
        workflow_sha = "c" * 40

        with (
            patch.object(updater, "fetch_bytes", return_value=harness_body),
            patch.object(updater, "resolve_workflow_sha", return_value=workflow_sha),
            contextlib.redirect_stdout(io.StringIO()),
        ):
            result = updater.main(
                ["--version", version, "--root", str(self.root), "--write"]
            )

        self.assertEqual(0, result)
        base_value = (
            self.harness.read_text(encoding="utf-8").splitlines()[0].split('"')[1]
        )
        parsed = checker.parse_pinned_base(base_value)
        self.assertEqual(
            "https://raw.githubusercontent.com/fullsend-ai/agents/"
            f"{version}/harness/review.yaml",
            parsed.fetch_url,
        )
        self.assertEqual(expected_digest, parsed.expected_sha256)


class FullsendSetupPreviewTests(unittest.TestCase):
    def run_preview(
        self,
        current_workflow: bytes,
        generated_workflow: bytes,
        setup_state: setup_preview.SetupState,
    ) -> tuple[int, str, str]:
        with tempfile.TemporaryDirectory() as root_directory:
            root = Path(root_directory)
            workflow_path = root / ".github" / "workflows" / "fullsend.yaml"
            workflow_path.parent.mkdir(parents=True)
            workflow_path.write_bytes(current_workflow)
            planned_file = setup_preview.PlannedFile(
                ".github/workflows/fullsend.yaml", len(generated_workflow)
            )
            stdout = io.StringIO()
            stderr = io.StringIO()

            with (
                patch.object(
                    setup_preview, "resolve_cli_version", return_value="v1.2.3"
                ),
                patch.object(
                    setup_preview,
                    "run_setup_dry_run",
                    return_value=("", [planned_file], setup_state),
                ),
                patch.object(
                    setup_preview, "resolve_workflow_sha", return_value="a" * 40
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
                    setup_preview,
                    "render_generated_file",
                    return_value=generated_workflow,
                ),
                patch.object(setup_preview, "list_current_variables", return_value={}),
                patch.object(setup_preview, "list_current_secrets", return_value=set()),
                contextlib.redirect_stdout(stdout),
                contextlib.redirect_stderr(stderr),
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

        return result, stdout.getvalue(), stderr.getvalue()

    def test_preview_returns_zero_when_workflows_and_settings_are_in_sync(self) -> None:
        workflow = b"jobs: {}\n"

        result, _, error = self.run_preview(
            workflow,
            workflow,
            setup_preview.SetupState({}, ()),
        )

        self.assertEqual(0, result)
        self.assertNotIn("# REVIEW REQUIRED", error)

    def test_preview_rejects_target_segments_starting_with_hyphen(self) -> None:
        for target in ("--force/example-org", "example-org/--force"):
            with self.subTest(target=target):
                error = io.StringIO()
                with (
                    self.assertRaises(SystemExit) as raised,
                    contextlib.redirect_stderr(error),
                ):
                    setup_preview.main(["--version", "v1.2.3", "--", target])

                self.assertEqual(2, raised.exception.code)
                self.assertIn(
                    "target must be a GitHub owner/repository", error.getvalue()
                )

    def test_preview_returns_three_when_generated_workflow_differs(self) -> None:
        result, output, error = self.run_preview(
            b"jobs: {}\n",
            b"jobs:\n  check: {}\n",
            setup_preview.SetupState({}, ()),
        )

        self.assertEqual(3, result)
        self.assertIn("+jobs:\n", output)
        self.assertIn("# REVIEW REQUIRED", error)

    def test_preview_returns_three_when_generated_secret_is_missing(self) -> None:
        workflow = (
            b"jobs:\n  check:\n    env:\n      TOKEN: ${{ secrets.DEPLOY_TOKEN }}\n"
        )

        result, _, error = self.run_preview(
            workflow,
            workflow,
            setup_preview.SetupState({}, ("DEPLOY_TOKEN",)),
        )

        self.assertEqual(3, result)
        self.assertIn("MISSING DEPLOY_TOKEN", error)
        self.assertIn("# REVIEW REQUIRED", error)

    def test_setup_state_rejects_secret_count_mismatches_with_status_2(self) -> None:
        output = (
            "Would commit: .github/workflows/fullsend.yaml (1 bytes)\n"
            "Would commit: .github/workflows/prioritize.yml (1 bytes)\n"
            "Would set repository variables:\n\n"
            "Would set 2 repository secrets:\n"
            "  DEPLOY_TOKEN"
        )
        error = io.StringIO()

        with (
            tempfile.TemporaryDirectory() as root_directory,
            patch.object(setup_preview, "resolve_cli_version", return_value="v1.2.3"),
            patch.object(setup_preview, "run_command", return_value=output),
            contextlib.redirect_stderr(error),
        ):
            result = setup_preview.main(
                [
                    "example-org/example-repo",
                    "--version",
                    "v1.2.3",
                    "--root",
                    root_directory,
                ]
            )

        self.assertEqual(2, result)
        self.assertIn("declared 2 repository secrets but listed 1", error.getvalue())

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
            "  TIMEOUT_SECONDS = 30\n\n"
            "Would set 2 repository secrets:\n"
            "  DEPLOY_TOKEN\n"
            "  CLOUD_KEY\n"
            "\n"
            "Would remove stale vendored assets at .fullsend/bin/fullsend (if present)"
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
                {
                    "SERVICE_URL": "https://example.invalid/api",
                    "TIMEOUT_SECONDS": "30",
                },
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

    def test_setup_state_rejects_trailing_text_after_vendored_assets_footer(
        self,
    ) -> None:
        output = (
            "Would set repository variables:\n\n"
            "Would set 0 repository secrets:\n"
            "Would remove stale vendored assets at .fullsend/bin/fullsend (if present)\n"
            "unexpected trailing line\n"
        )

        with self.assertRaises(setup_preview.UpdateError):
            setup_preview.parse_setup_state(output)

    def test_workflow_references_are_extracted_from_github_expressions(self) -> None:
        planned = setup_preview.PlannedFile(".github/workflows/review.yml", 0)
        current = (
            b'run: echo "${{ vars.OLD_URL }} ${{ secrets.OLD_TOKEN }} '
            b'${{ vars["OLD_INDEXED_URL"] }} ${{ secrets[\'OLD_INDEXED_TOKEN\'] }}"\n'
            b"# vars.IGNORED is not an expression\n"
        )
        generated = (
            b'run: echo "${{ vars.SERVICE_URL }} ${{ secrets.DEPLOY_TOKEN }} '
            b"${{ vars['INDEXED_SERVICE_URL'] }} "
            b'${{ secrets["INDEXED_DEPLOY_TOKEN"] }}"\n'
            b"# vars.IGNORED is not an expression\n"
        )
        rendered = [(planned, current, generated)]

        self.assertEqual(
            (
                {"SERVICE_URL", "INDEXED_SERVICE_URL"},
                {"DEPLOY_TOKEN", "INDEXED_DEPLOY_TOKEN"},
            ),
            setup_preview.extract_workflow_references(rendered, generated=True),
        )
        self.assertEqual(
            (
                {"OLD_URL", "OLD_INDEXED_URL"},
                {"OLD_TOKEN", "OLD_INDEXED_TOKEN"},
            ),
            setup_preview.extract_workflow_references(rendered, generated=False),
        )

    def test_indexed_workflow_references_are_reported_as_missing(self) -> None:
        planned = setup_preview.PlannedFile(".github/workflows/review.yml", 0)
        generated = (
            b'run: echo "${{ vars[\'SERVICE_URL\'] }} ${{ secrets["DEPLOY_TOKEN"] }}"\n'
        )
        rendered = [(planned, b"", generated)]
        variables, secrets = setup_preview.extract_workflow_references(
            rendered, generated=True
        )
        changes = setup_preview.compare_variables({}, {}, variables)
        output = io.StringIO()

        with contextlib.redirect_stderr(output):
            setup_preview.print_setup_state(
                setup_preview.SetupState({}, ()),
                changes,
                variables,
                set(),
                secrets,
                set(),
                set(),
            )

        self.assertIn(
            "REVIEW SERVICE_URL: generated workflow references it",
            output.getvalue(),
        )
        self.assertIn("MISSING DEPLOY_TOKEN [generated workflow]", output.getvalue())

    def test_managed_header_names_the_upstream_file(self) -> None:
        self.assertEqual(
            "# This file is managed by fullsend. Do not edit it directly.\n"
            "# Upstream: https://github.com/fullsend-ai/fullsend/blob/main/"
            "internal/scaffold/fullsend-repo/.github/workflows/fullsend.yaml\n",
            setup_preview.managed_header(".github/workflows/fullsend.yaml"),
        )

    def test_resolve_ref_comment_separator_reads_supported_literal(self) -> None:
        with patch.object(
            setup_preview,
            "fetch_bytes",
            return_value=b'uses += " # " + opts.UpstreamTag',
        ):
            separator = setup_preview.resolve_ref_comment_separator(
                "v1.2.3", timeout=5.0
            )

        self.assertEqual(" # ", separator)

    def test_render_generated_files_for_both_templates(self) -> None:
        dispatch_target = ".github/workflows/fullsend.yaml"
        prioritize_target = ".github/workflows/prioritize.yml"
        dispatch_template = (
            b"uses: __REUSABLE_DISPATCH__\n"
            b"ref: __FULLSEND_AI_REF__\n"
            b"runner: __GH_RUNNER__\n"
        )
        prioritize_template = (
            b"uses: __REUSABLE_WORKFLOW__\n"
            b"install_mode: per-org\n"
            b"ref: __FULLSEND_AI_REF__\n"
            b"runner: __GH_RUNNER__\n"
        )
        version = "v1.2.3"
        workflow_sha = "a" * 40
        ref = f"{workflow_sha} # {version}"

        with patch.object(
            setup_preview,
            "fetch_bytes",
            side_effect=[dispatch_template, prioritize_template],
        ):
            dispatch = setup_preview.render_generated_file(
                dispatch_target,
                version,
                workflow_sha,
                "ubuntu-latest",
                " # ",
                timeout=5.0,
            )
            prioritize = setup_preview.render_generated_file(
                prioritize_target,
                version,
                workflow_sha,
                "ubuntu-latest",
                " # ",
                timeout=5.0,
            )

        expected_dispatch = (
            setup_preview.managed_header(dispatch_target)
            + "uses: fullsend-ai/fullsend/.github/workflows/"
            f"reusable-dispatch.yml@{ref}\n"
            f"ref: {ref}\n"
            "runner: ubuntu-latest\n"
        ).encode("utf-8")
        expected_prioritize = (
            setup_preview.managed_header(prioritize_target)
            + "uses: fullsend-ai/fullsend/.github/workflows/"
            f"reusable-prioritize.yml@{ref}\n"
            "install_mode: per-repo\n"
            f"ref: {ref}\n"
            "runner: ubuntu-latest\n"
        ).encode("utf-8")
        self.assertEqual(expected_dispatch, dispatch)
        self.assertEqual(expected_prioritize, prioritize)

    def test_preview_rejects_reconstructed_byte_count_mismatch(self) -> None:
        generated = b"generated"
        planned = setup_preview.PlannedFile(
            ".github/workflows/fullsend.yaml", len(generated) + 1
        )
        error = io.StringIO()

        with (
            tempfile.TemporaryDirectory() as root_directory,
            patch.object(setup_preview, "resolve_cli_version", return_value="v1.2.3"),
            patch.object(
                setup_preview,
                "run_setup_dry_run",
                return_value=("", [planned], setup_preview.SetupState({}, ())),
            ),
            patch.object(setup_preview, "resolve_workflow_sha", return_value="a" * 40),
            patch.object(setup_preview, "resolve_runner", return_value="ubuntu-latest"),
            patch.object(
                setup_preview, "resolve_ref_comment_separator", return_value=" # "
            ),
            patch.object(
                setup_preview, "render_generated_file", return_value=generated
            ),
            contextlib.redirect_stderr(error),
        ):
            result = setup_preview.main(
                [
                    "example-org/example-repo",
                    "--version",
                    "v1.2.3",
                    "--root",
                    root_directory,
                ]
            )

        self.assertEqual(2, result)
        self.assertIn(
            "reconstructed .github/workflows/fullsend.yaml is 9 bytes",
            error.getvalue(),
        )
        self.assertIn("reported 10 bytes", error.getvalue())


if __name__ == "__main__":
    unittest.main()
