"""Tests for scripts/select_comparison_base.py."""

from __future__ import annotations

import contextlib
import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import yaml

if __package__:
    from scripts import select_comparison_base as selector
else:
    import select_comparison_base as selector


def _git(repo: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=repo,
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout.strip()


def _make_repo(root: Path) -> tuple[Path, str, str]:
    """Create a repo on ``main`` with an ``origin/main`` remote-tracking ref.

    Returns the repo path, the previous tip, and ``HEAD``.
    """
    repo = root / "repo"
    repo.mkdir()
    _git(repo, "init", "-q", "-b", "main")
    _git(repo, "config", "user.email", "test@example.com")
    _git(repo, "config", "user.name", "Test")
    _git(repo, "config", "commit.gpgsign", "false")
    (repo / "README.md").write_text("first\n")
    _git(repo, "add", "-A")
    _git(repo, "commit", "-q", "-m", "first")
    previous = _git(repo, "rev-parse", "HEAD")
    _git(repo, "update-ref", "refs/remotes/origin/main", previous)
    _git(repo, "update-ref", "refs/remotes/origin/trunk", previous)
    (repo / "README.md").write_text("second\n")
    _git(repo, "commit", "-q", "-am", "second")
    return repo, previous, _git(repo, "rev-parse", "HEAD")


class SelectTests(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo, self.previous, self.head = _make_repo(Path(self._tmp.name))

    def test_pull_request_uses_base_sha(self) -> None:
        selection = selector.select(
            {"PULL_REQUEST_BASE_SHA": self.previous, "DEFAULT_BRANCH": "main"},
            self.repo,
        )
        self.assertEqual(selector.Selection(merge_base=self.previous), selection)

    def test_merge_group_uses_base_sha(self) -> None:
        selection = selector.select(
            {"MERGE_GROUP_BASE_SHA": self.previous, "DEFAULT_BRANCH": "main"},
            self.repo,
        )
        self.assertEqual(selector.Selection(merge_base=self.previous), selection)

    def test_pull_request_base_wins_over_merge_group(self) -> None:
        selection = selector.select(
            {
                "PULL_REQUEST_BASE_SHA": self.previous,
                "MERGE_GROUP_BASE_SHA": self.head,
                "DEFAULT_BRANCH": "main",
            },
            self.repo,
        )
        self.assertEqual(selector.Selection(merge_base=self.previous), selection)

    def test_default_branch_push_uses_exact_previous_tip(self) -> None:
        selection = selector.select(
            {
                "GITHUB_REF": "refs/heads/main",
                "PUSH_BEFORE_SHA": self.previous,
                "DEFAULT_BRANCH": "main",
            },
            self.repo,
        )
        self.assertEqual(selector.Selection(exact_base=self.previous), selection)

    def test_initial_default_branch_push_uses_default_ref(self) -> None:
        selection = selector.select(
            {
                "GITHUB_REF": "refs/heads/main",
                "PUSH_BEFORE_SHA": selector.ZERO_SHA,
                "DEFAULT_BRANCH": "main",
            },
            self.repo,
        )
        self.assertEqual(selector.Selection(merge_base="origin/main"), selection)

    def test_unavailable_previous_tip_fails(self) -> None:
        missing = "a" * 40
        with self.assertRaises(selector.SelectionError) as raised:
            selector.select(
                {
                    "GITHUB_REF": "refs/heads/main",
                    "PUSH_BEFORE_SHA": missing,
                    "DEFAULT_BRANCH": "main",
                },
                self.repo,
            )
        self.assertIn(missing, str(raised.exception))

    def test_feature_branch_push_uses_default_ref(self) -> None:
        selection = selector.select(
            {
                "GITHUB_REF": "refs/heads/issue-405",
                "PUSH_BEFORE_SHA": self.previous,
                "DEFAULT_BRANCH": "main",
            },
            self.repo,
        )
        self.assertEqual(selector.Selection(merge_base="origin/main"), selection)

    def test_tag_push_uses_default_ref(self) -> None:
        selection = selector.select(
            {"GITHUB_REF": "refs/tags/v1.0.0", "DEFAULT_BRANCH": "main"},
            self.repo,
        )
        self.assertEqual(selector.Selection(merge_base="origin/main"), selection)

    def test_workflow_dispatch_uses_default_ref(self) -> None:
        selection = selector.select({"DEFAULT_BRANCH": "main"}, self.repo)
        self.assertEqual(selector.Selection(merge_base="origin/main"), selection)

    def test_renamed_default_branch_uses_its_own_ref(self) -> None:
        selection = selector.select({"DEFAULT_BRANCH": "trunk"}, self.repo)
        self.assertEqual(selector.Selection(merge_base="origin/trunk"), selection)

    def test_renamed_default_branch_push_uses_exact_tip(self) -> None:
        selection = selector.select(
            {
                "GITHUB_REF": "refs/heads/trunk",
                "PUSH_BEFORE_SHA": self.previous,
                "DEFAULT_BRANCH": "trunk",
            },
            self.repo,
        )
        self.assertEqual(selector.Selection(exact_base=self.previous), selection)

    def test_missing_default_branch_falls_back_to_main(self) -> None:
        selection = selector.select({"DEFAULT_BRANCH": ""}, self.repo)
        self.assertEqual(selector.Selection(merge_base="origin/main"), selection)

    def test_missing_default_ref_fails(self) -> None:
        with self.assertRaises(selector.SelectionError):
            selector.select({"DEFAULT_BRANCH": "release"}, self.repo)

    def test_base_not_present_fails(self) -> None:
        missing = "b" * 40
        with self.assertRaises(selector.SelectionError):
            selector.select({"PULL_REQUEST_BASE_SHA": missing}, self.repo)

    def test_option_like_base_is_rejected(self) -> None:
        with self.assertRaises(selector.SelectionError):
            selector.select({"PULL_REQUEST_BASE_SHA": "--help"}, self.repo)

    def test_malformed_base_is_rejected(self) -> None:
        with self.assertRaises(selector.SelectionError):
            selector.select({"PULL_REQUEST_BASE_SHA": "main"}, self.repo)

    def test_malformed_previous_tip_is_rejected(self) -> None:
        with self.assertRaises(selector.SelectionError):
            selector.select(
                {
                    "GITHUB_REF": "refs/heads/main",
                    "PUSH_BEFORE_SHA": "not-a-sha",
                    "DEFAULT_BRANCH": "main",
                },
                self.repo,
            )

    def test_invalid_default_branch_is_rejected(self) -> None:
        for name in ("ma in", "--help", "main..x", "main:x"):
            with self.subTest(name=name), self.assertRaises(selector.SelectionError):
                selector.select({"DEFAULT_BRANCH": name}, self.repo)


class OutputTests(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo, self.previous, self.head = _make_repo(Path(self._tmp.name))
        self.output = Path(self._tmp.name) / "github_output"

    def _run(self, env: dict[str, str]) -> tuple[int, str, str]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        clean_env = {
            key: os.environ[key] for key in ("PATH", "HOME") if key in os.environ
        }
        clean_env.update(env)
        with (
            contextlib.chdir(self.repo),
            patch.dict(os.environ, clean_env, clear=True),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
        ):
            status = selector.main([])
        return status, stdout.getvalue(), stderr.getvalue()

    def test_appends_outputs_to_github_output(self) -> None:
        self.output.write_text("prior=value\n")
        status, stdout, stderr = self._run(
            {
                "PULL_REQUEST_BASE_SHA": self.previous,
                "GITHUB_OUTPUT": str(self.output),
            }
        )
        self.assertEqual(0, status, stderr)
        self.assertEqual("", stdout)
        self.assertEqual(
            f"prior=value\nmerge_base={self.previous}\nexact_base=\n",
            self.output.read_text(),
        )

    def test_writes_outputs_to_stdout_without_github_output(self) -> None:
        status, stdout, stderr = self._run(
            {"MERGE_GROUP_BASE_SHA": self.previous, "GITHUB_OUTPUT": ""}
        )
        self.assertEqual(0, status, stderr)
        self.assertEqual(
            f"merge_base={self.previous}\nexact_base=\n",
            stdout,
        )

    def test_failure_reports_an_error_annotation(self) -> None:
        status, stdout, stderr = self._run({"PULL_REQUEST_BASE_SHA": "c" * 40})
        self.assertEqual(1, status)
        self.assertEqual("", stdout)
        self.assertIn("::error::", stderr)

    def test_error_annotation_stays_single_line(self) -> None:
        status, _, stderr = self._run({"DEFAULT_BRANCH": "ma\nin"})
        self.assertEqual(1, status)
        self.assertIn("::error::", stderr)
        self.assertEqual(1, stderr.count("\n"))

    def test_annotation_text_escaping(self) -> None:
        self.assertEqual(
            "a%25b%0Ac%0D",
            selector.escape_command_text("a%b\nc\r"),
        )


class WorkflowBindingTests(unittest.TestCase):
    """The workflow must bind the events to the script's documented inputs."""

    WORKFLOW = (
        Path(__file__).resolve().parent.parent / ".github/workflows/ci-workflow.yaml"
    )
    ACTION = (
        Path(__file__).resolve().parent.parent
        / ".github/actions/check-go-module/action.yaml"
    )

    def setUp(self) -> None:
        self.workflow = yaml.safe_load(self.WORKFLOW.read_text(encoding="utf-8"))
        self.job = self.workflow["jobs"]["validator"]
        self.steps = self.job["steps"]
        self.action = yaml.safe_load(self.ACTION.read_text(encoding="utf-8"))
        self.action_steps = self.action["runs"]["steps"]

    def _step(self, name: str) -> dict:
        matches = [step for step in self.steps if step.get("name") == name]
        self.assertEqual(1, len(matches), f"expected one {name!r} step")
        return matches[0]

    def _action_step(self, name: str) -> dict:
        matches = [step for step in self.action_steps if step.get("name") == name]
        self.assertEqual(1, len(matches), f"expected one {name!r} step")
        return matches[0]

    def test_step_runs_the_selector(self) -> None:
        step = self._step("Select the comparison base")
        self.assertEqual(
            "python3 scripts/select_comparison_base.py", step["run"].strip()
        )

    def test_step_supplies_the_script_inputs(self) -> None:
        step = self._step("Select the comparison base")
        self.assertEqual(
            {
                "DEFAULT_BRANCH": "${{ github.event.repository.default_branch }}",
                "PULL_REQUEST_BASE_SHA": "${{ github.event.pull_request.base.sha }}",
                "MERGE_GROUP_BASE_SHA": "${{ github.event.merge_group.base_sha }}",
                "PUSH_BEFORE_SHA": "${{ github.event.before }}",
            },
            step["env"],
        )

    def test_check_step_receives_both_outputs(self) -> None:
        env = self._step("Run the V2 specification validator")["env"]
        self.assertEqual("${{ steps.base.outputs.merge_base }}", env["GITHUB_BASE_SHA"])
        self.assertEqual(
            "${{ steps.base.outputs.exact_base }}", env["PROTOBOT_VALIDATOR_BASE"]
        )

    def test_job_runs_the_shared_module_checks(self) -> None:
        step = self._step("Run shared Go module checks")
        self.assertEqual("./.github/actions/check-go-module", step["uses"])
        self.assertEqual("validator", step["with"]["module-path"])

    def test_action_checks_formatting(self) -> None:
        step = self._action_step("Check formatting")
        self.assertIn("gofmt -l", step["run"])
        self.assertEqual("${{ inputs.module-path }}", step["working-directory"])

    def test_action_lints_the_module(self) -> None:
        lint = self._action_step("Run golangci-lint")
        self.assertEqual("${{ inputs.module-path }}", lint["with"]["working-directory"])

    def test_action_runs_go_commands_with_gowork_off(self) -> None:
        for name, command in (
            ("Download module dependencies", "go mod download"),
            ("Run vet", "go vet ./..."),
            ("Run tests", "go test -v ./..."),
        ):
            with self.subTest(step=name):
                step = self._action_step(name)
                self.assertEqual(command, step["run"].strip())
                self.assertEqual("off", step["env"]["GOWORK"])
                self.assertEqual("${{ inputs.module-path }}", step["working-directory"])

    def test_action_wires_the_module_cache(self) -> None:
        self._action_step("Resolve module cache inputs")
        setup = self._action_step("Set up Go")
        self.assertEqual(
            "${{ steps.module.outputs.go-sum != '' }}", setup["with"]["cache"]
        )
        self.assertEqual(
            "${{ steps.module.outputs.go-sum }}",
            setup["with"]["cache-dependency-path"],
        )

    def _run_module_cache_step(self, root: Path, module_path: str) -> str:
        step = self._action_step("Resolve module cache inputs")
        output_path = root / "github-output"
        subprocess.run(
            ["bash", "-c", step["run"]],
            cwd=root,
            env={
                **os.environ,
                "MODULE_PATH": module_path,
                "GITHUB_OUTPUT": str(output_path),
            },
            check=True,
            text=True,
        )
        return output_path.read_text(encoding="utf-8")

    def test_module_cache_step_publishes_the_go_sum_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            module = root / "module-with-sum"
            module.mkdir()
            (module / "go.sum").write_text("", encoding="utf-8")
            self.assertEqual(
                "go-sum=module-with-sum/go.sum\n",
                self._run_module_cache_step(root, "module-with-sum"),
            )

    def test_module_cache_step_publishes_an_empty_value_without_go_sum(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "module-without-sum").mkdir()
            self.assertEqual(
                "go-sum=\n",
                self._run_module_cache_step(root, "module-without-sum"),
            )

    def test_steps_run_in_dependency_order(self) -> None:
        order = [
            "Run shared Go module checks",
            "Select the comparison base",
            "Run the V2 specification validator",
        ]
        names = [step.get("name") for step in self.steps]
        positions = [names.index(name) for name in order]
        self.assertEqual(sorted(positions), positions)

    def test_action_module_steps_run_in_order(self) -> None:
        order = [
            "Download module dependencies",
            "Check formatting",
            "Run golangci-lint",
            "Run vet",
            "Run tests",
        ]
        names = [step.get("name") for step in self.action_steps]
        positions = [names.index(name) for name in order]
        self.assertEqual(sorted(positions), positions)

    def test_job_has_no_condition(self) -> None:
        self.assertNotIn("if", self.job)

    def test_gate_requires_every_required_job(self) -> None:
        needs = self.workflow["jobs"]["ci-success"]["needs"]
        for job in (
            "pre-commit",
            "go",
            "spec",
            "validator",
            "scm",
            "wms",
            "drafting-table",
        ):
            with self.subTest(job=job):
                self.assertIn(job, needs)


if __name__ == "__main__":
    unittest.main()
