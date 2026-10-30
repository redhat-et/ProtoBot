#!/usr/bin/env python3
"""Recording local `gh` surface for the Source Control Manager fixture.

Records every invocation to the log named by `DT_GH_LOG`. With
`DT_GH_STATE` and `DT_GH_ORIGIN` set, `pr create` records the pull
request and `pr merge` performs the host-side merge into the bare
origin, so the merged pull request is visible to later `pr list` calls.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


def flag(args: list[str], name: str) -> str | None:
    if name in args:
        index = args.index(name)
        if index + 1 < len(args):
            return args[index + 1]
    return None


def read_state() -> dict[str, Any]:
    path = os.environ.get("DT_GH_STATE")
    if not path or not Path(path).exists():
        return {"pulls": []}
    return json.loads(Path(path).read_text())


def write_state(state: dict[str, Any]) -> None:
    path = os.environ.get("DT_GH_STATE")
    if path:
        Path(path).write_text(json.dumps(state, sort_keys=True, separators=(",", ":")))


def pull_json(pull: dict[str, Any]) -> dict[str, Any]:
    entry: dict[str, Any] = {
        "number": pull["number"],
        "state": "MERGED" if pull.get("merged") else "OPEN",
        "url": f"https://github.com/protobot-fixture/fixture/pull/{pull['number']}",
        "title": "Fixture pull request",
        "body": "",
        "headRefName": pull["head"],
        "baseRefName": pull["base"],
        "isCrossRepository": False,
        "headRepository": {"name": "fixture"},
        "headRepositoryOwner": {"login": "protobot-fixture"},
    }
    if pull.get("merge_commit"):
        entry["mergeCommit"] = {"oid": pull["merge_commit"]}
    return entry


def pr_list(args: list[str]) -> list[dict[str, Any]]:
    state = read_state()
    wanted = flag(args, "--state") or "open"
    if wanted == "all":
        return [pull_json(pull) for pull in state["pulls"]]
    merged = wanted == "merged"
    return [
        pull_json(pull) for pull in state["pulls"] if bool(pull.get("merged")) == merged
    ]


def host_merge() -> str | None:
    """Merge the open pull request into the bare origin, as the host would."""
    origin = os.environ.get("DT_GH_ORIGIN")
    if not origin:
        return None
    state = read_state()
    pull = next((pull for pull in state["pulls"] if not pull.get("merged")), None)
    if pull is None:
        return None
    env = dict(os.environ)
    with tempfile.TemporaryDirectory(prefix="gh-stub-merge-") as work:
        clone = Path(work) / "clone"
        subprocess.run(
            ["git", "clone", "--quiet", origin, str(clone)],
            check=True,
            env=env,
            capture_output=True,
        )
        subprocess.run(
            [
                "git",
                "merge",
                "--quiet",
                "--no-ff",
                "--no-edit",
                "origin/" + pull["head"],
            ],
            cwd=clone,
            check=True,
            env=env,
            capture_output=True,
        )
        subprocess.run(
            ["git", "push", "--quiet", "origin", pull["base"]],
            cwd=clone,
            check=True,
            env=env,
            capture_output=True,
        )
        head = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=clone,
            check=True,
            env=env,
            capture_output=True,
            text=True,
        ).stdout.strip()
    pull["merged"] = True
    pull["merge_commit"] = head
    write_state(state)
    return head


def main() -> int:
    log = Path(os.environ["DT_GH_LOG"])
    args = sys.argv[1:]
    body = sys.stdin.read()
    if args[:2] == ["pr", "list"]:
        event = {"event": "list", "argv": args, "stdin": body}
        output = json.dumps(pr_list(args)) + "\n"
    elif args[:2] == ["pr", "create"]:
        event = {"event": "create", "argv": args, "stdin": body}
        state = read_state()
        state["pulls"].append(
            {
                "number": len(state["pulls"]) + 1,
                "head": flag(args, "--head") or "",
                "base": flag(args, "--base") or "main",
            }
        )
        write_state(state)
        output = (
            f"https://github.com/protobot-fixture/fixture/pull/{len(state['pulls'])}\n"
        )
    elif args[:2] == ["pr", "edit"]:
        event = {"event": "edit", "argv": args, "stdin": body}
        output = ""
    elif args[:2] == ["pr", "merge"]:
        event = {"event": "merge", "argv": args, "stdin": body}
        host_merge()
        output = ""
    else:
        event = {"event": "refused", "argv": args, "stdin": body}
        with log.open("a", encoding="utf-8") as output_file:
            output_file.write(
                json.dumps(event, sort_keys=True, separators=(",", ":")) + "\n"
            )
        print("fixture gh stub refuses unsupported operation", file=sys.stderr)
        return 2
    with log.open("a", encoding="utf-8") as output_file:
        output_file.write(
            json.dumps(event, sort_keys=True, separators=(",", ":")) + "\n"
        )
    sys.stdout.write(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
