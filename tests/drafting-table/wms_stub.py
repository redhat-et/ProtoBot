"""Recording, backend-free WMS MCP server used only by the OpenCode fixture.

With `--state`, work items materialized by the Materializer stand-in
(`materialize`) persist in that file and are served by the read
operations alongside the fixed blocked item.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

ITEM = {
    "id": "WI-FIXTURE-001",
    "state": "blocked",
    "contract_version": 7,
    "reason_kind": "undefined-behavior",
    "resolution_options": [
        "add-requirement",
        "out-of-scope",
        "impact-amendment",
        "defer",
        "acknowledge",
    ],
}

TOOLS = [
    "request_create",
    "request_refine",
    "request_link_change_set",
    "request_link_build_work_item",
    "request_get",
    "request_query",
    "work_item_get",
    "work_item_query",
    "blocked_work_query",
    "lifecycle_preflight",
    "blocked_work_submit_resolution",
    "blocked_work_acknowledge",
    # Job Site lifecycle operations a real adapter serves to other roles;
    # the drafting-table guard refuses them before they reach the stub.
    "claim",
    "record_merge",
]


def record(path: Path, value: dict[str, Any]) -> None:
    with path.open("a", encoding="utf-8") as output:
        output.write(json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n")


def read_state(state: Path | None) -> dict[str, Any]:
    if state is None or not state.exists():
        return {"materializations": []}
    return json.loads(state.read_text())


def work_items(state: Path | None) -> list[dict[str, Any]]:
    return [ITEM] + [
        entry["work_item"] for entry in read_state(state)["materializations"]
    ]


def materialize(
    state: Path,
    log: Path,
    *,
    change_set: str,
    merge_commit: str,
    materialization_key: str,
    idempotency_key: str,
) -> dict[str, Any]:
    """Materializer-role write: create one ready-for-building work item per
    materialization key; a repeat with the same keys replays the result."""
    data = read_state(state)
    prior = next(
        (
            entry
            for entry in data["materializations"]
            if entry["materialization_key"] == materialization_key
        ),
        None,
    )
    result: dict[str, Any] = {
        "schema_version": 1,
        "operation": "materialize",
        "diagnostics": [],
    }
    if prior is not None and prior["idempotency_key"] != idempotency_key:
        result.update(
            ok=False,
            outcome="rejected",
            mutation="none",
            error={"code": "IDEMPOTENCY_CONFLICT"},
        )
        return result
    if prior is None:
        item = {
            "id": f"WI-{change_set}",
            "state": "ready-for-building",
            "dependencies": [],
            "contract_version": 1,
            "change_set_id": change_set,
        }
        data["materializations"].append(
            {
                "materialization_key": materialization_key,
                "idempotency_key": idempotency_key,
                "merge_commit": merge_commit,
                "work_item": item,
            }
        )
        temporary = state.with_name(state.name + ".tmp")
        temporary.write_text(json.dumps(data, sort_keys=True, indent=2) + "\n")
        os.replace(temporary, state)
        before = "initial"
        result.update(outcome="applied", mutation="applied", idempotency="new")
    else:
        item = prior["work_item"]
        before = item["state"]
        result.update(outcome="replayed", mutation="none", idempotency="replayed")
    result.update(
        ok=True,
        work_item_id=item["id"],
        work_item_state=item["state"],
        contract_version=item["contract_version"],
    )
    record(
        log,
        {
            "event": "materialize",
            "role": "materializer",
            "work_item_id": item["id"],
            "change_set": change_set,
            "materialization_key": materialization_key,
            "before": before,
            "after": item["state"],
            "mutation": result["mutation"],
            "idempotency": result["idempotency"],
        },
    )
    return result


def tool_result(
    name: str, arguments: dict[str, Any], state: Path | None = None
) -> dict[str, Any]:
    operation = name.replace("_", "-")
    result: dict[str, Any] = {
        "schema_version": 1,
        "ok": True,
        "operation": operation,
        "outcome": "read",
        "mutation": "none",
        "diagnostics": [],
    }
    items = work_items(state)
    if name == "blocked_work_query":
        result["items"] = [item for item in items if item["state"] == "blocked"]
    elif name == "work_item_query":
        result["items"] = items
    elif name in (
        "work_item_get",
        "blocked_work_submit_resolution",
        "blocked_work_acknowledge",
    ):
        result["item"] = ITEM
    elif name == "lifecycle_preflight":
        result["decision"] = {
            "outcome": "rejected",
            "authority": "preflight",
            "operation": arguments.get("operation", "resolve-block"),
            "before": {"state": "blocked", "contract_version": 7},
            "after": None,
            "rejection": {
                "code": "PRECONDITION_FAILED",
                "failed_precondition": "Fixture dependency remains incomplete",
            },
        }
    else:
        result["items"] = []
    return result


def send(value: dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(value, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def serve(log: Path, state: Path | None) -> None:
    record(log, {"event": "started", "state": ITEM["state"]})
    try:
        for raw in sys.stdin:
            try:
                request = json.loads(raw)
            except json.JSONDecodeError:
                continue
            method = request.get("method")
            request_id = request.get("id")
            if method == "initialize":
                requested = request.get("params", {}).get(
                    "protocolVersion", "2025-11-25"
                )
                send(
                    {
                        "jsonrpc": "2.0",
                        "id": request_id,
                        "result": {
                            "protocolVersion": requested,
                            "capabilities": {"tools": {}},
                            "serverInfo": {
                                "name": "recording-wms-fixture",
                                "version": "1",
                            },
                        },
                    }
                )
            elif method == "notifications/initialized":
                continue
            elif method == "tools/list":
                send(
                    {
                        "jsonrpc": "2.0",
                        "id": request_id,
                        "result": {
                            "tools": [
                                {
                                    "name": name,
                                    "description": "Recording fixture WMS operation; no backend is contacted.",
                                    "inputSchema": {
                                        "type": "object",
                                        "properties": {},
                                        "additionalProperties": True,
                                    },
                                }
                                for name in TOOLS
                            ]
                        },
                    }
                )
            elif method == "tools/call":
                params = request.get("params", {})
                name = params.get("name", "")
                arguments = params.get("arguments", {})
                result = tool_result(name, arguments, state)
                record(
                    log,
                    {
                        "event": "call",
                        "tool": name,
                        "arguments": arguments,
                        "before": ITEM["state"],
                        "after": ITEM["state"],
                        "mutation": "none",
                    },
                )
                send(
                    {
                        "jsonrpc": "2.0",
                        "id": request_id,
                        "result": {
                            "content": [
                                {
                                    "type": "text",
                                    "text": json.dumps(result, separators=(",", ":")),
                                }
                            ],
                            "isError": False,
                        },
                    }
                )
            elif request_id is not None:
                send(
                    {
                        "jsonrpc": "2.0",
                        "id": request_id,
                        "error": {"code": -32601, "message": "Method not found"},
                    }
                )
    finally:
        record(log, {"event": "stopped", "state": ITEM["state"]})


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--log", required=True, type=Path)
    parser.add_argument("--state", type=Path)
    parser.add_argument(
        "--fail-start",
        action="store_true",
        help="record a failed start and exit without serving (WMS unavailable)",
    )
    args = parser.parse_args()
    if args.fail_start:
        record(args.log, {"event": "failed-start", "state": ITEM["state"]})
        return 1
    serve(args.log, args.state)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
