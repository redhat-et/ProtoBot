#!/usr/bin/env python3
"""Recording local `gh` surface for the Source Control Manager fixture."""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    log = Path(os.environ["DT_GH_LOG"])
    args = sys.argv[1:]
    body = sys.stdin.read()
    if args[:2] == ["pr", "list"]:
        event = {"event": "list", "argv": args, "stdin": body}
        output = "[]\n"
    elif args[:2] == ["pr", "create"]:
        event = {"event": "create", "argv": args, "stdin": body}
        output = "https://github.com/protobot-fixture/fixture/pull/1\n"
    elif args[:2] == ["pr", "edit"]:
        event = {"event": "edit", "argv": args, "stdin": body}
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
