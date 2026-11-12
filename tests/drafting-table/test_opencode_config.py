"""Checks that V1 OpenCode root config no longer blocks ordinary sessions."""

from __future__ import annotations

import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class OpenCodeConfigTest(unittest.TestCase):
    def test_guard_plugin_is_removed(self) -> None:
        self.assertFalse((ROOT / ".opencode/plugins/protobot-guard.js").exists())

    def test_drafting_table_binding_files_remain(self) -> None:
        self.assertTrue((ROOT / ".opencode/agents/drafting-table.md").is_file())
        self.assertTrue((ROOT / ".opencode/commands/drafting-table.md").is_file())

    def test_root_opencode_json_only_disables_sharing(self) -> None:
        path = ROOT / "opencode.json"
        self.assertTrue(path.is_file())
        self.assertEqual(json.loads(path.read_text()), {"share": "disabled"})

    def test_root_opencode_json_has_no_legacy_references(self) -> None:
        text = (ROOT / "opencode.json").read_text()
        for token in ("source-control-manager", "wms", "ears-manager", ".protobot/"):
            with self.subTest(token=token):
                self.assertNotIn(token, text)


if __name__ == "__main__":
    unittest.main()
