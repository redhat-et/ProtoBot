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
        self.assertTrue(result["summary"]["chat"]["session_exported"])
        self.assertEqual(result["summary"]["chat"]["session_export_count"], 8)
        self.assertEqual(result["summary"]["wms"]["state_before"], "blocked")
        self.assertEqual(result["summary"]["wms"]["state_after"], "blocked")


if __name__ == "__main__":
    unittest.main()
