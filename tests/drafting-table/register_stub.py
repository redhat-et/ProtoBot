#!/usr/bin/env python3
"""Local `register-approved-change-set` stand-in for the OpenCode fixture.

Reads the merge commit through the Source Control Manager's
`approved-merge` CLI face, hands the change set, merge commit, and both
derived keys to the Materializer stand-in, which persists the build work
item in the recording WMS state named by `DT_WMS_STATE`, then appends one
durable registration record to the log named by `DT_REG_LOG`. A repeat
call with the same change set and merge commit replays both the
materialization and the recorded result (idempotent retry). A different
merge commit for the same change set is refused.
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))

from wms_stub import materialize


def registration_result(
    change_set: str, merge_commit: str, work_item_id: str
) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "ok": True,
        "operation": "register-approved-change-set",
        "change_set": change_set,
        "merge_commit": merge_commit,
        "work_item_id": work_item_id,
        "registered": True,
    }


def main() -> int:
    args = sys.argv[1:]
    change_set = None
    if "--change-set" in args:
        index = args.index("--change-set")
        if index + 1 < len(args):
            change_set = args[index + 1]
    if not change_set or not change_set.startswith("CS-"):
        print(
            "usage: register-approved-change-set --change-set CS-NNNNN",
            file=sys.stderr,
        )
        return 2
    log = Path(os.environ["DT_REG_LOG"])
    completed = subprocess.run(
        [
            "source-control-manager",
            "--output",
            "json",
            "approved-merge",
            "--change-set",
            change_set,
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    if completed.returncode:
        sys.stdout.write(completed.stdout)
        sys.stderr.write(completed.stderr)
        return completed.returncode
    merge_commit = json.loads(completed.stdout)["data"]["merge_commit"]
    project_id = os.environ.get("DT_REG_PROJECT", "fixture-project")
    materialization_key = hashlib.sha256(
        f"{project_id}\0{change_set}\0{merge_commit}".encode()
    ).hexdigest()
    idempotency_key = hashlib.sha256(
        f"register-approved-change-set\0{project_id}\0{change_set}\0{merge_commit}".encode()
    ).hexdigest()
    entries = []
    if log.exists():
        entries = [json.loads(line) for line in log.read_text().splitlines() if line]
    prior = next(
        (entry for entry in entries if entry["change_set"] == change_set), None
    )
    if prior is not None and prior["merge_commit"] != merge_commit:
        print(
            "registration conflict: a different merge commit is registered",
            file=sys.stderr,
        )
        return 3
    materialized = materialize(
        Path(os.environ["DT_WMS_STATE"]),
        Path(os.environ["DT_WMS_LOG"]),
        change_set=change_set,
        merge_commit=merge_commit,
        materialization_key=materialization_key,
        idempotency_key=idempotency_key,
    )
    if not materialized["ok"]:
        sys.stdout.write(json.dumps(materialized, separators=(",", ":")) + "\n")
        return 3
    if prior is not None:
        result = prior["result"]
    else:
        result = registration_result(
            change_set, merge_commit, materialized["work_item_id"]
        )
        entry = {
            "event": "register",
            "change_set": change_set,
            "merge_commit": merge_commit,
            "materialization_key": materialization_key,
            "idempotency_key": idempotency_key,
            "result": result,
        }
        entries.append(entry)
        with log.open("a", encoding="utf-8") as output:
            output.write(
                json.dumps(entry, sort_keys=True, separators=(",", ":")) + "\n"
            )
    sys.stdout.write(json.dumps(result, separators=(",", ":")) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
