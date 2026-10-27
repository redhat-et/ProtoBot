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
        self.assertTrue(stale["chat"]["registered"])
        self.assertTrue(stale["chat"]["registration_idempotent"])
        wms_unavailable = result["scenarios"]["wms-unavailable"]
        self.assertFalse(wms_unavailable["wms"]["wms_available"])
        self.assertTrue(wms_unavailable["chat"]["drafting_continued"])
        self.assertEqual(
            wms_unavailable["git"]["working_tree_status_paths"],
            [".protobot/change-sets/cs-00002.yaml", ".protobot/project.yaml"],
        )


if __name__ == "__main__":
    unittest.main()
