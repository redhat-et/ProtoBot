"""Repeat-run checks for the deterministic OpenCode fixture driver."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import unittest
from pathlib import Path

sys.dont_write_bytecode = True

DRIVER = Path(__file__).with_name("replay.py")
sys.path.insert(0, str(DRIVER.parent))

import replay


def recorded_output(actions: list[dict], failed: frozenset[int] = frozenset()) -> str:
    """Render golden actions as the OpenCode JSON tool_use event stream."""
    return "\n".join(
        json.dumps(
            {
                "type": "tool_use",
                "part": {
                    "tool": action["tool"],
                    "state": {
                        "input": action["arguments"],
                        "status": "error" if index in failed else "completed",
                    },
                },
            }
        )
        for index, action in enumerate(actions)
    )


class ResumeReadOrderTest(unittest.TestCase):
    """Negative vectors for the resume read-before-write assertion."""

    def setUp(self) -> None:
        _, scenarios = replay.read_golden()
        stale = scenarios["stale-state"]
        self.turns = stale["turns"]
        self.required = stale["expectation"]["start_before_first_write"]

    def assert_order(self, actions: list[dict], failed: frozenset[int] = frozenset()):
        replay.assert_resume_reads_before_write(
            recorded_output(actions, failed), "vector", self.required
        )

    def write_first(self, turn: str, marker: str) -> list[dict]:
        actions = list(self.turns[turn]["actions"])
        index = next(
            i
            for i, action in enumerate(actions)
            if action["tool"] == marker
            or marker in action["arguments"].get("command", "")
        )
        write = actions.pop(index)
        return [actions[0], write, *actions[1:]]

    def test_recorded_turns_pass(self) -> None:
        for turn in ("stale-refresh-and-update", "stale-register"):
            with self.subTest(turn=turn):
                self.assert_order(self.turns[turn]["actions"])

    def test_each_write_before_reads_fails(self) -> None:
        for turn, marker in (
            ("stale-refresh-and-update", "scm_refresh"),
            ("stale-refresh-and-update", "--base-commit"),
            ("stale-register", "register-approved-change-set"),
            ("stale-approval-default-moved", "scm_commit"),
        ):
            with self.subTest(write=marker), self.assertRaises(AssertionError):
                self.assert_order(self.write_first(turn, marker))

    def test_failed_read_does_not_count(self) -> None:
        actions = self.turns["stale-register"]["actions"]
        index = next(
            i
            for i, action in enumerate(actions)
            if "artifact get" in action["arguments"].get("command", "")
        )
        with self.assertRaises(AssertionError):
            self.assert_order(actions, failed=frozenset({index}))


class OpenCodeReplayTest(unittest.TestCase):
    def test_headless_replay_is_deterministic(self) -> None:
        command = [sys.executable, str(DRIVER)]
        if os.environ.get("OPENCODE_BIN"):
            command.extend(["--opencode-bin", os.environ["OPENCODE_BIN"]])
        completed = subprocess.run(
            command,
            cwd=DRIVER.parents[2],
            text=True,
            capture_output=True,
            timeout=900,
            check=False,
        )
        self.assertEqual(
            completed.returncode,
            0,
            msg=f"fixture driver failed\nstdout:\n{completed.stdout}\nstderr:\n{completed.stderr}",
        )
        result = json.loads(completed.stdout)
        self.assertEqual(result["result"], "passed")
        self.assertEqual(result["repeat_runs"], 2)
        self.assertEqual(
            list(result["scenarios"]),
            [
                "approved-handoff",
                "rejection-revision",
                "stale-state",
                "wms-unavailable",
            ],
        )
        approved = result["scenarios"]["approved-handoff"]
        self.assertTrue(approved["chat"]["session_exported"])
        self.assertEqual(approved["chat"]["session_export_count"], 9)
        self.assertTrue(approved["chat"]["jobsite_lifecycle_refused"])
        self.assertEqual(approved["wms"]["state_before"], "blocked")
        self.assertEqual(approved["wms"]["state_after"], "blocked")
        rejection = result["scenarios"]["rejection-revision"]
        self.assertEqual(rejection["chat"]["explicit_rejection"]["tool_calls"], 0)
        self.assertTrue(rejection["chat"]["revision"]["vision_revised"])
        self.assertFalse(rejection["chat"]["revision"]["committed"])
        self.assertEqual(rejection["wms"]["mutations"], 0)
        stale = result["scenarios"]["stale-state"]
        self.assertTrue(stale["chat"]["default_moved_detected"])
        self.assertTrue(stale["chat"]["publish_failed_default_moved"])
        self.assertTrue(stale["chat"]["base_commit_updated"])
        self.assertEqual(stale["git"]["pr_create_count"], 1)
        self.assertEqual(stale["git"]["pr_merge_count"], 1)
        self.assertTrue(stale["chat"]["refresh_withheld_commit_and_publish"])
        self.assertTrue(stale["chat"]["renewed_approval_bound_to_new_base"])
        self.assertTrue(stale["chat"]["registered"])
        self.assertTrue(stale["chat"]["registration_idempotent"])
        self.assertEqual(
            stale["wms"]["materialized_work_item"],
            {"id": "WI-CS-00003", "state": "ready-for-building"},
        )
        self.assertTrue(stale["wms"]["persisted_readback"])
        self.assertEqual(stale["wms"]["role_mutations"], 0)
        self.assertEqual(stale["wms"]["state_after"], "blocked")
        wms_unavailable = result["scenarios"]["wms-unavailable"]
        self.assertFalse(wms_unavailable["wms"]["wms_available"])
        self.assertTrue(wms_unavailable["chat"]["drafting_continued"])
        self.assertEqual(
            wms_unavailable["git"]["working_tree_status_paths"],
            [".protobot/change-sets/cs-00002.yaml", ".protobot/project.yaml"],
        )


if __name__ == "__main__":
    unittest.main()
