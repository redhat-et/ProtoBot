"""Recording, backend-free WMS MCP server used only by the OpenCode fixture."""

from __future__ import annotations

import argparse
import json
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
]


def record(path: Path, value: dict[str, Any]) -> None:
    with path.open("a", encoding="utf-8") as output:
        output.write(json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n")


def tool_result(name: str, arguments: dict[str, Any]) -> dict[str, Any]:
    operation = name.replace("_", "-")
    result: dict[str, Any] = {
        "schema_version": 1,
        "ok": True,
        "operation": operation,
        "outcome": "read",
        "mutation": "none",
        "diagnostics": [],
    }
    if name in ("blocked_work_query", "work_item_query"):
        result["items"] = [ITEM]
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


def serve(log: Path) -> None:
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
                result = tool_result(name, arguments)
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
    args = parser.parse_args()
    serve(args.log)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
