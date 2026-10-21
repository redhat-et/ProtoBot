#!/usr/bin/env python3
"""Render the recorded demonstration for the OpenCode Drafting Table fixture.

Runs the approved-handoff and rejection-revision scenarios once against the
pinned OpenCode binary and renders one approved path and one rejected path
as a committed Markdown transcript, from the golden turns and the replay's
deterministic evidence. The document is not CI-diffed; regenerate it with:

    PYTHONDONTWRITEBYTECODE=1 OPENCODE_BIN=<opencode-1.18.30> \
      python3 tests/drafting-table/render_demo.py
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sys
import tempfile
import textwrap
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import replay

DEMO_SCENARIOS = ("approved-handoff", "rejection-revision")
OUTPUT = replay.ROOT / "docs/architecture/fixtures/drafting-table-opencode-demo.md"


def render_turn(turn: dict[str, Any], entry: dict[str, Any] | None) -> list[str]:
    lines = [f"### {turn['name']}", ""]
    lines.append("**User:**")
    lines.append("")
    lines.extend(textwrap.wrap(turn["prompt"], width=80) or [""])
    lines.append("")
    if entry is not None and entry["tools"]:
        lines.append("**Tool sequence:**")
        lines.append("")
        lines.extend(
            f"- `{tool['tool']}` ({tool['status']})" for tool in entry["tools"]
        )
        lines.append("")
    elif entry is not None:
        lines.append("**Tool sequence:** none — the rejection makes no tool call.")
        lines.append("")
    lines.append("**Reply:**")
    lines.append("")
    lines.extend(
        textwrap.wrap(turn.get("final", "Fixture turn complete."), width=80) or [""]
    )
    lines.append("")
    return lines


def render_state(summary: dict[str, Any]) -> list[str]:
    lines = ["**Authoritative state after the scenario:**", ""]
    lines.append("| Check | Evidence |")
    lines.append("| --- | --- |")
    for section in ("chat", "git", "wms"):
        for key, value in summary[section].items():
            # Content digests are replay evidence, not narrative; a hex
            # digest would trip secret scanning in the committed document.
            if key.endswith("_sha256"):
                continue
            lines.append(f"| {section}.{key} | `{json.dumps(value)}` |")
    lines.append("")
    return lines


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--opencode-bin", default=os.environ.get("OPENCODE_BIN", "opencode")
    )
    parser.add_argument("--output", type=Path, default=OUTPUT)
    args = parser.parse_args()
    binary = shutil.which(args.opencode_bin) or args.opencode_bin
    if not Path(binary).exists() and shutil.which(binary) is None:
        parser.error(f"OpenCode binary not found: {args.opencode_bin}")
    replay.verify_binary(binary)
    fixture, scenarios = replay.read_golden()
    runners = {
        "approved-handoff": replay.run_approved,
        "rejection-revision": replay.run_rejection,
    }
    lines = [
        "# Drafting Table OpenCode fixture — recorded demonstration",
        "",
        "Generated transcript of the deterministic headless replay in",
        "`tests/drafting-table/`, run against the pinned OpenCode",
        f"{replay.PINNED_OPENCODE} binary. It shows one approved path and one",
        "rejected path through the Drafting Table flow. Each turn lists the",
        "user message, the role's tool sequence, and its closing reply;",
        "`<BASE_COMMIT>` stands for the exact base commit that explicit",
        "approval binds to. This document is evidence for #173, regenerated",
        "from the golden fixture; it is not executed live in CI.",
        "",
    ]
    with tempfile.TemporaryDirectory(prefix="opencode-drafting-table-bin-") as work:
        bindir = replay.build_binaries(Path(work), os.environ)
        for name in DEMO_SCENARIOS:
            scenario = scenarios[name]
            result = runners[name](binary, fixture, scenario, bindir)
            narrative = {entry["turn"]: entry for entry in result["narrative"]}
            lines.append(f"## Scenario `{name}`")
            lines.append("")
            for turn in scenario["turns"].values():
                lines.extend(render_turn(turn, narrative.get(turn["name"])))
            lines.extend(render_state(result["summary"]))
    lines.append("## Regeneration")
    lines.append("")
    lines.append("```sh")
    lines.append("PYTHONDONTWRITEBYTECODE=1 OPENCODE_BIN=<opencode-1.18.30> \\")
    lines.append("  python3 tests/drafting-table/render_demo.py")
    lines.append("```")
    lines.append("")
    args.output.write_text("\n".join(lines), encoding="utf-8")
    print(f"wrote {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
