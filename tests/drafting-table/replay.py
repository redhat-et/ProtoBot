#!/usr/bin/env python3
"""Headless, deterministic OpenCode replay for the Drafting Table binding."""

from __future__ import annotations

import argparse
import hashlib
import http.server
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
GOLDEN = ROOT / "docs/architecture/fixtures/drafting-table-opencode-golden.jsonl"
WMS_STUB = ROOT / "tests/drafting-table/wms_stub.py"
GH_STUB = ROOT / "tests/drafting-table/gh_stub.py"
REGISTER_STUB = ROOT / "tests/drafting-table/register_stub.py"
PINNED_OPENCODE = "1.18.30"
PROJECT_ID = "fixture-project"
CHANGE_SET_ID = "CS-00002"
SENTINEL = "PROTOBOT-FIXTURE-TOKEN"
V2_ENV = (
    "OPENCODE_CONFIG_CONTENT",
    "OPENCODE_CONFIG",
    "OPENCODE_CONFIG_DIR",
    "OPENCODE_PERMISSION",
)


def read_golden() -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    records = [json.loads(line) for line in GOLDEN.read_text().splitlines() if line]
    fixture = next(record for record in records if record["kind"] == "fixture")
    scenarios = {
        record["name"]: {"expectation": record["expectation"], "turns": {}}
        for record in records
        if record["kind"] == "scenario"
    }
    for record in records:
        if record["kind"] != "turn":
            continue
        name = record.get("scenario")
        if name not in scenarios:
            raise AssertionError(
                f"golden turn {record.get('name')!r} references unknown scenario {name!r}"
            )
        scenarios[name]["turns"][record["name"]] = record
    for name in fixture["scenarios"]:
        if name not in scenarios:
            raise AssertionError(f"golden fixture scenario {name!r} has no records")
    return fixture, scenarios


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def clean_env(base: dict[str, str], *, project: Path, home: Path) -> dict[str, str]:
    env = dict(base)
    for name in (
        *V2_ENV,
        "OPENAI_API_KEY",
        "OPENAI_BASE_URL",
        "ANTHROPIC_API_KEY",
        "GITHUB_TOKEN",
        "GH_TOKEN",
        "GITLAB_TOKEN",
    ):
        env.pop(name, None)
    env.update(
        {
            "HOME": str(home),
            "PWD": str(project),
            "XDG_CONFIG_HOME": str(home / "xdg-config"),
            "XDG_DATA_HOME": str(home / "xdg-data"),
            "XDG_CACHE_HOME": str(home / "xdg-cache"),
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_TERMINAL_PROMPT": "0",
            "GIT_AUTHOR_NAME": "Fixture User",
            "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
            "GIT_COMMITTER_NAME": "Fixture User",
            "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
            "GIT_AUTHOR_DATE": "2026-10-12T10:00:00Z",
            "GIT_COMMITTER_DATE": "2026-10-12T10:00:00Z",
            "NO_COLOR": "1",
            "CI": "1",
        }
    )
    for name in ("XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"):
        Path(env[name]).mkdir(parents=True, exist_ok=True)
    return env


def run(
    args: list[str],
    *,
    cwd: Path,
    env: dict[str, str],
    check: bool = True,
    timeout: int = 120,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        args,
        cwd=cwd,
        env=env,
        text=True,
        input=input_text,
        capture_output=True,
        check=False,
        timeout=timeout,
    )
    if check and completed.returncode:
        raise AssertionError(
            f"command failed ({completed.returncode}): {args!r}\n"
            f"stdout:\n{completed.stdout}\nstderr:\n{completed.stderr}"
        )
    return completed


def json_output(completed: subprocess.CompletedProcess[str]) -> Any:
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError(
            f"expected JSON from command; stdout={completed.stdout!r}; "
            f"stderr={completed.stderr!r}"
        ) from error


class ReplayServer:
    """Local OpenAI-compatible endpoint returning only recorded tool calls."""

    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.actions: list[dict[str, Any]] = []
        self.final = "Fixture turn complete."
        self.replacements: dict[str, str] = {}
        self.cursor = 0
        self.requests: list[dict[str, Any]] = []
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_POST(self) -> None:
                length = int(self.headers.get("Content-Length", "0"))
                request = json.loads(self.rfile.read(length))
                with owner.lock:
                    owner.requests.append(request)
                    tools = request.get("tools") or []
                    action = None
                    if tools and owner.cursor < len(owner.actions):
                        action = owner.actions[owner.cursor]
                        owner.cursor += 1
                        offered = {
                            tool.get("function", {}).get("name") for tool in tools
                        }
                        if action["tool"] not in offered:
                            raise AssertionError(
                                f"recorded tool {action['tool']!r} was not offered; "
                                f"offered={sorted(str(name) for name in offered)}"
                            )
                    body = owner.response(request, action)
                encoded = json.dumps(body, separators=(",", ":")).encode()
                if request.get("stream"):
                    chunks = owner.stream_chunks(body)
                    payload = (
                        b"".join(
                            b"data: "
                            + json.dumps(chunk, separators=(",", ":")).encode()
                            + b"\n\n"
                            for chunk in chunks
                        )
                        + b"data: [DONE]\n\n"
                    )
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.send_header("Cache-Control", "no-cache")
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                else:
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(encoded)))
                    self.end_headers()
                    self.wfile.write(encoded)

            def log_message(self, _format: str, *_args: Any) -> None:
                return

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"http://127.0.0.1:{self.server.server_port}/v1"

    def set_turn(self, turn: dict[str, Any], project: Path) -> None:
        with self.lock:
            self.cursor = 0
            self.actions = []
            for action in turn.get("actions", []):
                args = json.dumps(action["arguments"], ensure_ascii=False)
                args = args.replace("<PROJECT>", str(project))
                for marker, value in self.replacements.items():
                    args = args.replace(marker, value)
                self.actions.append(
                    {"tool": action["tool"], "arguments": json.loads(args)}
                )
            self.final = turn.get("final", "Fixture turn complete.")
            for marker, value in self.replacements.items():
                self.final = self.final.replace(marker, value)

    def assert_consumed(self, turn: dict[str, Any]) -> None:
        expected = len(turn.get("actions", []))
        with self.lock:
            actual = self.cursor
        if actual != expected:
            raise AssertionError(
                f"replay phase {turn['name']!r} consumed {actual}/{expected} tool calls"
            )

    def response(
        self, request: dict[str, Any], action: dict[str, Any] | None
    ) -> dict[str, Any]:
        model = request.get("model", "turns")
        message: dict[str, Any] = {"role": "assistant"}
        if action is not None:
            message["content"] = None
            message["tool_calls"] = [
                {
                    "id": f"fixture-call-{self.cursor:04d}",
                    "type": "function",
                    "function": {
                        "name": action["tool"],
                        "arguments": json.dumps(
                            action["arguments"], ensure_ascii=False
                        ),
                    },
                }
            ]
            finish = "tool_calls"
        else:
            message["content"] = self.final
            finish = "stop"
        return {
            "id": "chatcmpl-protobot-fixture",
            "object": "chat.completion",
            "created": 1791800000,
            "model": model,
            "choices": [{"index": 0, "message": message, "finish_reason": finish}],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        }

    @staticmethod
    def stream_chunks(response: dict[str, Any]) -> list[dict[str, Any]]:
        choice = response["choices"][0]
        message = choice["message"]
        common = {
            "id": response["id"],
            "object": "chat.completion.chunk",
            "created": response["created"],
            "model": response["model"],
        }
        if message.get("tool_calls"):
            call = message["tool_calls"][0]
            delta = {
                "role": "assistant",
                "tool_calls": [
                    {
                        "index": 0,
                        "id": call["id"],
                        "type": "function",
                        "function": call["function"],
                    }
                ],
            }
        else:
            delta = {"role": "assistant", "content": message.get("content", "")}
        return [
            {
                **common,
                "choices": [{"index": 0, "delta": delta, "finish_reason": None}],
            },
            {
                **common,
                "choices": [
                    {"index": 0, "delta": {}, "finish_reason": choice["finish_reason"]}
                ],
            },
        ]

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)


def build_binaries(temp: Path, env: dict[str, str]) -> Path:
    bindir = temp / "bin"
    bindir.mkdir()
    modules = (
        (ROOT / "ears-manager", "./cmd/ears-manager", "ears-manager"),
        (
            ROOT / "source-control-manager",
            "./cmd/source-control-manager",
            "source-control-manager",
        ),
        (
            ROOT / "drafting-table",
            "./cmd/drafting-table-guard",
            "drafting-table-guard",
        ),
    )
    for module, package, name in modules:
        output = bindir / name
        run(["go", "build", "-o", str(output), package], cwd=module, env=env)
        output.chmod(0o755)
    gh = bindir / "gh"
    gh.write_text(
        f"#!{sys.executable}\nimport os, sys\nos.execv(sys.executable, [sys.executable, {str(GH_STUB)!r}, *sys.argv[1:]])\n"
    )
    gh.chmod(0o755)
    register = bindir / "register-approved-change-set"
    register.write_text(
        f"#!{sys.executable}\nimport os, sys\nos.execv(sys.executable, [sys.executable, {str(REGISTER_STUB)!r}, *sys.argv[1:]])\n"
    )
    register.chmod(0o755)
    return bindir


def copy_project_inputs(project: Path) -> None:
    (project / ".agents").mkdir(parents=True)
    shutil.copy2(
        ROOT / ".agents/drafting-table.yaml", project / ".agents/drafting-table.yaml"
    )
    for name in ("drafting-specifications", "eliciting-requirements", "review-pr"):
        shutil.copytree(
            ROOT / ".agents/skills" / name, project / ".agents/skills" / name
        )
    shutil.copytree(ROOT / ".opencode/agents", project / ".opencode/agents")
    shutil.copytree(ROOT / ".opencode/commands", project / ".opencode/commands")
    shutil.copytree(ROOT / ".opencode/plugins", project / ".opencode/plugins")
    (project / "docs").mkdir()
    (project / "docs/vision.md").write_text(
        "# Fixture Vision\n\nLocal deterministic fixture.\n"
    )
    (project / "docs/architecture.md").write_text(
        "# Fixture Architecture\n\nLocal deterministic fixture.\n"
    )
    (project / ".gitignore").write_text("/.netrc\n/docs/.env\n")


def prepare_project(
    project: Path,
    env: dict[str, str],
    ears: Path,
    scm: Path,
    provider_url: str,
    wms_log: Path,
    *,
    wms_mode: str = "serving",
    scenario_change_set: str = "CS-00002",
) -> dict[str, str]:
    bare = project.parent / "origin.git"
    run(
        ["git", "init", "--bare", "--initial-branch=main", str(bare)],
        cwd=project.parent,
        env=env,
    )
    project.mkdir()
    copy_project_inputs(project)
    (project / "opencode.json").write_text(
        json.dumps(
            open_config(project, provider_url, wms_log, wms_mode=wms_mode), indent=2
        )
        + "\n"
    )
    run(["git", "init", "--initial-branch=main"], cwd=project, env=env)
    run(["git", "config", "user.name", "Fixture User"], cwd=project, env=env)
    run(
        ["git", "config", "user.email", "fixture@example.invalid"], cwd=project, env=env
    )
    canonical = "https://github.com/protobot-fixture/fixture.git"
    run(["git", "remote", "add", "origin", canonical], cwd=project, env=env)
    run(
        ["git", "config", f"url.{bare.as_uri()}.insteadOf", canonical],
        cwd=project,
        env=env,
    )
    run(["git", "add", "-A"], cwd=project, env=env)
    run(["git", "commit", "--quiet", "-m", "Fixture source"], cwd=project, env=env)
    run(
        ["git", "push", "--quiet", "--set-upstream", "origin", "main"],
        cwd=project,
        env=env,
    )

    # Provision the same merged CS-00001 initialization baseline the adapter
    # fixture specifies. The session under test starts on proposed CS-00002.
    run(
        ["git", "switch", "--quiet", "--create", "cs/00001-project-init", "main"],
        cwd=project,
        env=env,
    )
    run(
        [
            str(ears),
            "--output",
            "json",
            "project",
            "init",
            "--id",
            PROJECT_ID,
            "--name",
            "OpenCode fixture",
            "--canonical-remote",
            canonical,
            "--review-mode",
            "single-player",
        ],
        cwd=project,
        env=env,
    )
    run(
        [
            str(ears),
            "--output",
            "json",
            "change-set",
            "create",
            "--intent",
            "Initialize the fixture project",
            "--implementation-required",
            "false",
            "--implementation-rationale",
            "Project initialization only.",
            "--created",
            "2026-10-12T10:00:00Z",
        ],
        cwd=project,
        env=env,
    )
    run(["git", "add", "-A"], cwd=project, env=env)
    run(
        ["git", "commit", "--quiet", "-m", "Fixture project initialization"],
        cwd=project,
        env=env,
    )
    run(["git", "switch", "--quiet", "main"], cwd=project, env=env)
    run(
        ["git", "merge", "--quiet", "--no-ff", "--no-edit", "cs/00001-project-init"],
        cwd=project,
        env=env,
    )
    run(["git", "push", "--quiet", "origin", "main"], cwd=project, env=env)
    created = run(
        [
            str(ears),
            "--output",
            "json",
            "change-set",
            "create",
            "--intent",
            "Exercise the OpenCode Drafting Table",
            "--implementation-required",
            "false",
            "--implementation-rationale",
            "Specification-only fixture change.",
            "--created",
            "2026-10-12T10:01:00Z",
            "--affected-scope",
            "drafting-table",
        ],
        cwd=project,
        env=env,
    )
    change_set = json_output(created)["data"]["change_set"]["id"]
    if change_set != "CS-00002":
        raise AssertionError(f"fixture setup made {change_set}, expected CS-00002")
    if scenario_change_set == "CS-00002":
        # The session under test starts on proposed CS-00002.
        pass
    else:
        # Merge CS-00002 into the default branch so the scenario change
        # set is cut from the merged baseline, as the adapter fixture
        # session table specifies.
        run(["git", "add", "-A"], cwd=project, env=env)
        run(
            ["git", "commit", "--quiet", "-m", "Fixture second change set"],
            cwd=project,
            env=env,
        )
        second_branch = run(
            ["git", "branch", "--show-current"], cwd=project, env=env
        ).stdout.strip()
        run(["git", "switch", "--quiet", "main"], cwd=project, env=env)
        run(
            ["git", "merge", "--quiet", "--no-ff", "--no-edit", second_branch],
            cwd=project,
            env=env,
        )
        run(["git", "push", "--quiet", "origin", "main"], cwd=project, env=env)
        third = run(
            [
                str(ears),
                "--output",
                "json",
                "change-set",
                "create",
                "--intent",
                "Exercise the stale-state refresh sequence",
                "--implementation-required",
                "false",
                "--implementation-rationale",
                "Specification-only fixture change.",
                "--created",
                "2026-10-12T10:02:00Z",
                "--affected-scope",
                "drafting-table",
            ],
            cwd=project,
            env=env,
        )
        third_id = json_output(third)["data"]["change_set"]["id"]
        if third_id != scenario_change_set:
            raise AssertionError(
                f"fixture setup made {third_id}, expected {scenario_change_set}"
            )

    (project / ".netrc").write_text(
        f"machine fixture.invalid login fixture password {SENTINEL}\n"
    )
    (project / "docs/.env").write_text(f"FIXTURE_CREDENTIAL={SENTINEL}\n")
    run(
        ["git", "config", "http.extraHeader", f"Authorization: Bearer {SENTINEL}"],
        cwd=project,
        env=env,
    )
    return {"canonical_remote": canonical, "bare": str(bare)}


def open_config(
    project: Path, provider_url: str, wms_log: Path, *, wms_mode: str = "serving"
) -> dict[str, Any]:
    wms_command = [sys.executable, str(WMS_STUB), "--log", str(wms_log)]
    if wms_mode == "fail-start":
        wms_command.append("--fail-start")
    elif wms_mode != "serving":
        raise AssertionError(f"unknown WMS fixture mode {wms_mode!r}")
    return {
        "$schema": "https://opencode.ai/config.json",
        "share": "disabled",
        "mcp": {
            "wms": {"type": "local", "command": wms_command},
            "scm": {
                "type": "local",
                "command": [
                    "source-control-manager",
                    "serve",
                    "--face",
                    "drafting-table",
                ],
            },
        },
        "permission": {
            "edit": {".protobot/**": "deny"},
            "wms_*": "deny",
            "scm_*": "deny",
        },
        "agent": {"build": {"permission": {"bash": {"*": "allow"}}}},
        "provider": {
            "replay": {
                "npm": "@ai-sdk/openai-compatible",
                "name": "Fixture replay",
                "options": {"baseURL": provider_url},
                "models": {"turns": {"name": "turns"}},
            }
        },
        "model": "replay/turns",
        "small_model": "replay/turns",
    }


def opencode_command(
    binary: str,
    args: list[str],
    *,
    project: Path,
    env: dict[str, str],
    check: bool = True,
    timeout: int = 180,
) -> subprocess.CompletedProcess[str]:
    command_env = dict(env)
    command_env["PWD"] = str(project)
    return run(
        [binary, *args],
        cwd=project,
        env=command_env,
        check=check,
        timeout=timeout,
    )


def session_id(output: str) -> str:
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        for key in ("sessionID", "session_id"):
            if isinstance(event.get(key), str):
                return event[key]
        props = event.get("properties")
        if isinstance(props, dict):
            for key in ("sessionID", "session_id"):
                if isinstance(props.get(key), str):
                    return props[key]
    raise AssertionError(
        f"OpenCode JSON output did not expose a session id: {output[:3000]}"
    )


def check_call_order(provider: ReplayServer, starts: int) -> list[str]:
    names: list[str] = []
    with provider.lock:
        requests = list(provider.requests[starts:])
    for request in requests:
        prompt = json.dumps(request.get("messages", []), ensure_ascii=False)
        for match in re.findall(
            r"<available_skills>(.*?)</available_skills>", prompt, re.DOTALL
        ):
            names.append(
                "skills:" + ",".join(sorted(re.findall(r"<name>(.*?)</name>", match)))
            )
    return names


def tool_events(output: str) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("type") != "tool_use" or not isinstance(event.get("part"), dict):
            continue
        part = event["part"]
        state = part.get("state", {})
        events.append(
            {
                "tool": part.get("tool", ""),
                "input": state.get("input", {}),
                "status": state.get("status", ""),
                "error": state.get("error", ""),
                "output": state.get("output", ""),
                "metadata": state.get("metadata", {}),
            }
        )
    return events


def canonical_tool_event(event: dict[str, Any]) -> str:
    tool = event["tool"]
    if tool.startswith(("scm_", "wms_")):
        return tool
    if tool != "bash":
        return tool
    command = event["input"].get("command", "")
    if "change-set update" in command and "--impact-file" in command:
        return "change-set update --impact-file"
    for name in (
        "requirement add",
        "change-set show",
        "change-set compare",
        "requirement list",
        "artifact get",
        "artifact put",
        "impact",
        "check",
    ):
        if name in command:
            return name
    return "bash"


def assert_resume_reads_before_write(
    output: str, phase: str, required: list[str]
) -> None:
    events = tool_events(output)
    names = [canonical_tool_event(event) for event in events]
    write_names = {
        "apply_patch",
        "requirement add",
        "artifact put",
        "change-set update --impact-file",
        "scm_commit",
        "scm_publish",
    }
    write_positions = [index for index, name in enumerate(names) if name in write_names]
    first_write = min(write_positions, default=len(names))
    for name in required:
        if name not in names[:first_write]:
            raise AssertionError(
                f"{phase} did not complete required resume read {name!r} before writes; "
                f"tool sequence={names!r}"
            )


def skill_files() -> list[Path]:
    paths = [
        ROOT / ".agents/drafting-table.yaml",
        ROOT / "opencode.json",
        ROOT / ".opencode/agents/drafting-table.md",
        ROOT / ".opencode/commands/drafting-table.md",
        ROOT / ".opencode/plugins/protobot-guard.js",
    ]
    for skill in ("drafting-specifications", "eliciting-requirements", "review-pr"):
        paths.extend(sorted((ROOT / ".agents/skills" / skill).rglob("*")))
    return [path for path in paths if path.is_file()]


EXPECTED_SKILLS = "skills:drafting-specifications,eliciting-requirements"


def source_hashes() -> dict[str, str]:
    hashes = {
        str(path.relative_to(ROOT)): sha256(path) for path in sorted(skill_files())
    }
    for path in (WMS_STUB, GH_STUB, REGISTER_STUB):
        hashes[str(path.relative_to(ROOT))] = sha256(path)
    driver = Path(__file__).resolve()
    hashes[str(driver.relative_to(ROOT))] = sha256(driver)
    hashes[str(GOLDEN.relative_to(ROOT))] = sha256(GOLDEN)
    return hashes


def wms_calls(wms_log: Path) -> list[dict[str, Any]]:
    return [event for event in read_jsonl(wms_log) if event.get("event") == "call"]


def assert_git_status(project: Path, env: dict[str, str], expected: list[str]) -> None:
    status = run(
        ["git", "status", "--porcelain", "--untracked-files=all"],
        cwd=project,
        env=env,
    ).stdout.splitlines()
    paths = sorted(line[3:] for line in status if line)
    if paths != sorted(expected):
        raise AssertionError(
            f"working tree status {paths!r} differs from golden output {sorted(expected)!r}"
        )


class ScenarioRun:
    """One replay run: temp project, provider, and binding environment checks."""

    def __init__(
        self,
        binary: str,
        fixture: dict[str, Any],
        bindir: Path,
        *,
        wms_mode: str = "serving",
        change_set_id: str = "CS-00002",
    ) -> None:
        self.binary = binary
        self.fixture = fixture
        self.bindir = bindir
        self.change_set_id = change_set_id
        self._work = tempfile.TemporaryDirectory(prefix="opencode-drafting-table-")
        self.temp = Path(self._work.name)
        self.home = self.temp / "home"
        self.project = self.temp / "project"
        self.home.mkdir()
        self.env = clean_env(os.environ, project=self.project, home=self.home)
        self.env["PATH"] = str(bindir) + os.pathsep + self.env.get("PATH", "")
        self.env["DT_GH_LOG"] = str(self.temp / "gh-events.jsonl")
        self.provider = ReplayServer()
        self.wms_log = self.temp / "wms-events.jsonl"
        self._disposed = False
        # Per-turn tool evidence for the recorded demonstration; never
        # part of the deterministic scenario summary.
        self.narrative: list[dict[str, Any]] = []
        try:
            self.project_context = prepare_project(
                self.project,
                self.env,
                bindir / "ears-manager",
                bindir / "source-control-manager",
                self.provider.url,
                self.wms_log,
                wms_mode=wms_mode,
                scenario_change_set=change_set_id,
            )
            self._verify_environment()
        except BaseException:
            self.provider.close()
            self._work.cleanup()
            raise

    def _verify_environment(self) -> None:
        project = self.project
        env = self.env
        effective_remote = run(
            ["git", "remote", "get-url", "origin"], cwd=project, env=env
        ).stdout.strip()
        if not effective_remote.startswith("file://"):
            raise AssertionError(
                "fixture Git origin is not rewritten to the local bare repository: "
                f"{effective_remote!r}"
            )
        self.effective_remote = effective_remote
        binary_version = run(
            [self.binary, "--version"], cwd=project, env=env, timeout=30
        ).stdout.strip()
        if binary_version != PINNED_OPENCODE:
            raise AssertionError(
                f"OpenCode must be pinned to {PINNED_OPENCODE}, got {binary_version!r}"
            )
        self.binary_version = binary_version
        # OpenCode skill discovery is checked from a nested directory.
        self.nested = project / "src/subdirectory"
        self.nested.mkdir(parents=True)
        discovered = opencode_command(
            self.binary, ["debug", "skill"], project=self.nested, env=env
        )
        discovered_skills = json.loads(discovered.stdout)
        discovered_names = {item.get("name") for item in discovered_skills}
        required_names = {
            "drafting-specifications",
            "eliciting-requirements",
            "review-pr",
        }
        missing_skills = sorted(required_names - discovered_names)
        if missing_skills:
            raise AssertionError(
                f"OpenCode did not discover {missing_skills!r}; "
                f"discovered={sorted(str(name) for name in discovered_names)}"
            )
        agent_debug = opencode_command(
            self.binary, ["debug", "agent", "drafting-table"], project=project, env=env
        ).stdout
        config_debug = opencode_command(
            self.binary, ["debug", "config"], project=project, env=env
        ).stdout
        resolved_agent = json.loads(agent_debug)
        resolved_rules = resolved_agent.get("permission", [])
        own_rules_start = max(
            (
                index
                for index, rule in enumerate(resolved_rules)
                if rule.get("permission") == "*" and rule.get("action") == "deny"
            ),
            default=len(resolved_rules),
        )
        own_rules = resolved_rules[own_rules_start:]
        if any(rule.get("action") == "ask" for rule in own_rules):
            raise AssertionError("Drafting Table agent rules introduce an ask permission")
        if '"disabled"' not in config_debug and "disabled" not in config_debug.lower():
            raise AssertionError("resolved OpenCode config does not show share=disabled")

    def base_commit(self, change_set: str) -> str:
        # Record the authoritative base revision for explicit approval
        # messages; never bind approval to conversational memory.
        show = run(
            [
                str(self.bindir / "ears-manager"),
                "--output",
                "json",
                "change-set",
                "show",
                "--change-set",
                change_set,
            ],
            cwd=self.project,
            env=self.env,
        )
        show_data = json_output(show)["data"]
        base_commit = show_data.get("change_set", {}).get("base_commit") or show_data.get(
            "base_commit"
        )
        if not isinstance(base_commit, str) or len(base_commit) != 40:
            raise AssertionError(
                f"could not read a full base_commit from change-set show: {show.stdout}"
            )
        return base_commit

    def git_head(self) -> str:
        return run(
            ["git", "rev-parse", "HEAD"], cwd=self.project, env=self.env
        ).stdout.strip()

    def turn_output(
        self, turn: dict[str, Any], args: list[str], cwd: Path | None = None
    ) -> str:
        output = invoke_turn(
            self.binary,
            self.provider,
            turn,
            turn["name"],
            args,
            self.project,
            self.env,
            cwd,
        )
        self.narrative.append(
            {
                "turn": turn["name"],
                "tools": [
                    {
                        "tool": canonical_tool_event(event),
                        "status": event["status"],
                    }
                    for event in tool_events(output)
                ],
            }
        )
        return output

    def new_role_session(
        self,
        turn: dict[str, Any],
        required_reads: list[str],
        cwd: Path | None = None,
    ) -> tuple[str, str]:
        """Start a session through the Drafting Table command entry point."""
        before = len(self.provider.requests)
        output = self.turn_output(
            turn,
            [
                "run",
                "--agent",
                "drafting-table",
                "--command",
                "drafting-table",
                "--format",
                "json",
                "--model",
                self.fixture["model"],
                turn["prompt"],
            ],
            cwd,
        )
        assert_resume_reads_before_write(output, turn["name"], required_reads)
        skill_list = next(
            (
                item
                for item in check_call_order(self.provider, before)
                if item.startswith("skills:")
            ),
            "",
        )
        if EXPECTED_SKILLS not in skill_list.replace(" ", ""):
            raise AssertionError(
                f"session {turn['name']!r} did not receive the Toolkit allowlist: {skill_list!r}"
            )
        return session_id(output), output

    def resume_session(self, turn: dict[str, Any], sid: str) -> str:
        return self.turn_output(
            turn,
            [
                "run",
                "--session",
                sid,
                "--agent",
                "drafting-table",
                "--format",
                "json",
                "--model",
                self.fixture["model"],
                turn["prompt"],
            ],
        )

    def export(
        self,
        session: str,
        expected_skill_markers: tuple[str, ...] = ("# Drafting Specifications",),
        expected_agent: str = "drafting-table",
    ) -> tuple[str, str]:
        return export_session(
            self.binary,
            session,
            self.project,
            self.env,
            expected_skill_markers=expected_skill_markers,
            expected_agent=expected_agent,
        )

    def dispose(self) -> None:
        if self._disposed:
            return
        self._disposed = True
        self.provider.close()
        self._work.cleanup()

    def __enter__(self) -> "ScenarioRun":
        return self

    def __exit__(self, *_exc: object) -> None:
        self.dispose()


def run_approved(
    binary: str,
    fixture: dict[str, Any],
    scenario: dict[str, Any],
    bindir: Path,
) -> dict[str, Any]:
    turns = scenario["turns"]
    expectation = scenario["expectation"]
    harness = ScenarioRun(binary, fixture, bindir)
    with harness:
        project = harness.project
        env = harness.env
        provider = harness.provider
        bindir = harness.bindir
        nested = harness.nested
        binary_version = harness.binary_version
        effective_remote = harness.effective_remote
        wms_log = harness.wms_log
        project_context = harness.project_context
        vision_path = project / "docs/vision.md"
        vision_before_patch = vision_path.read_bytes()

        # Record the authoritative base revision for the two explicit
        # approval messages; never bind approval to conversational memory.
        base_commit = harness.base_commit(CHANGE_SET_ID)
        provider.replacements["<BASE_COMMIT>"] = base_commit

        try:
            # Session A begins outside the role, then resumes through the
            # actual Drafting Table command before any governed write.
            outside = turns["outside-role-prelude"]
            provider.set_turn(outside, project)
            prelude = opencode_command(
                binary,
                [
                    "run",
                    "--print-logs",
                    "--log-level",
                    "DEBUG",
                    "--agent",
                    "build",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    outside["prompt"],
                ],
                project=project,
                env=env,
            )
            provider.assert_consumed(outside)
            sid_a = session_id(prelude.stdout)

            start = turns["start-and-resume-reads"]
            start_from = len(provider.requests)
            start_result = harness.turn_output(
                start,
                [
                    "run",
                    "--session",
                    sid_a,
                    "--agent",
                    "drafting-table",
                    "--command",
                    "drafting-table",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    start["prompt"],
                ],
            )
            provider.assert_consumed(start)
            assert_resume_reads_before_write(
                start_result, start["name"], expectation["start_before_first_write"]
            )
            if (
                fixture["blocked_work_item"]["id"] not in start_result
                or fixture["candidate_status"] not in start_result
                or "records the session on this machine" not in start_result
            ):
                raise AssertionError(
                    f"start summary omitted the blocked item, review status, or recording notice: {start_result}"
                )
            skill_prompts = check_call_order(provider, start_from)
            toolkit_prompt = next(
                (item for item in skill_prompts if item.startswith("skills:")), ""
            )

            if EXPECTED_SKILLS not in toolkit_prompt.replace(" ", ""):
                raise AssertionError(
                    f"model did not receive exactly the Toolkit skills: {toolkit_prompt!r}"
                )

            session_a_export, session_a_sanitized = harness.export(sid_a)

            skill_turn = turns["skill-discovery-refusals"]
            skill_from = len(provider.requests)
            skill_result = harness.turn_output(
                skill_turn,
                [
                    "run",
                    "--agent",
                    "drafting-table",
                    "--command",
                    "drafting-table",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    skill_turn["prompt"],
                ],
            )
            provider.assert_consumed(skill_turn)
            assert_resume_reads_before_write(
                skill_result,
                skill_turn["name"],
                expectation["start_before_first_write"],
            )
            sid_b = session_id(skill_result)
            skill_toolkit_prompt = next(
                (
                    item
                    for item in check_call_order(provider, skill_from)
                    if item.startswith("skills:")
                ),
                "",
            )
            if EXPECTED_SKILLS not in skill_toolkit_prompt.replace(" ", ""):
                raise AssertionError(
                    f"skill/refusal session did not receive the Toolkit allowlist: {skill_toolkit_prompt!r}"
                )
            if "drafting-table-guard" not in skill_result:
                raise AssertionError(
                    f"unapproved skill or review-pr read was not guard-refused: {skill_result}"
                )
            refusal_events = [
                event
                for event in tool_events(skill_result)
                if event["status"] == "error"
                and "drafting-table-guard" in event["error"]
            ]
            if not any(
                event["tool"] == "skill" and event["input"].get("name") == "review-pr"
                for event in refusal_events
            ) or not any(
                event["tool"] == "read"
                and str(event["input"].get("filePath", "")).endswith(
                    ".agents/skills/review-pr/SKILL.md"
                )
                for event in refusal_events
            ):
                raise AssertionError(
                    f"both unapproved review-pr routes were not refused: {refusal_events!r}"
                )
            session_b_export, session_b_sanitized = harness.export(
                sid_b,
                expected_skill_markers=(
                    "# Drafting Specifications",
                    "# Eliciting Requirements",
                ),
            )

            # A Drafting Table caller cannot invoke Job Site lifecycle
            # operations: OpenCode offers only the manifest-listed governed
            # tools to the role, and the guard refuses unlisted ones
            # outright, so the operation never reaches the WMS stub.
            jobsite = turns["jobsite-lifecycle-refusal"]
            jobsite_from = len(provider.requests)
            sid_jobsite, jobsite_result = harness.new_role_session(
                jobsite, expectation["start_before_first_write"]
            )
            jobsite_refusal = expectation["jobsite_lifecycle_refusal"]
            offered: set[str] = set()
            with provider.lock:
                for request in provider.requests[jobsite_from:]:
                    for tool in request.get("tools") or []:
                        offered.add(str(tool.get("function", {}).get("name")))
            if jobsite_refusal["tool"] in offered:
                raise AssertionError(
                    f"Job Site lifecycle operation {jobsite_refusal['tool']!r} was "
                    f"offered to the drafting-table role"
                )
            jobsite_input = {
                "hook_event_name": "PreToolUse",
                "session_id": sid_jobsite,
                "cwd": str(project),
                "tool_name": jobsite_refusal["tool"],
                "tool_input": {},
            }
            jobsite_guard = run(
                [
                    str(bindir / "drafting-table-guard"),
                    "--harness",
                    "opencode",
                    "--role",
                    "drafting-table",
                ],
                cwd=project,
                env=env,
                check=False,
                input_text=json.dumps(jobsite_input),
            )
            if (
                jobsite_guard.returncode != 2
                or jobsite_refusal["guard_reason"] not in jobsite_guard.stderr
            ):
                raise AssertionError(
                    f"Job Site lifecycle guard vector failed: "
                    f"exit={jobsite_guard.returncode}, stderr={jobsite_guard.stderr!r}"
                )
            if any(
                event.get("tool") == jobsite_refusal["tool"]
                for event in wms_calls(wms_log)
            ):
                raise AssertionError(
                    "Job Site lifecycle operation reached the recording WMS stub"
                )
            session_jobsite_export, session_jobsite_sanitized = harness.export(
                sid_jobsite
            )

            # A new session must repeat the resume reads before the candidate
            # write; it does not inherit authoritative state from chat.
            fresh = turns["fresh-session-resume"]
            fresh_from = len(provider.requests)
            fresh_result = harness.turn_output(
                fresh,
                [
                    "run",
                    "--agent",
                    "drafting-table",
                    "--command",
                    "drafting-table",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    fresh["prompt"],
                ],
            )
            provider.assert_consumed(fresh)
            assert_resume_reads_before_write(
                fresh_result, fresh["name"], expectation["start_before_first_write"]
            )
            sid_c = session_id(fresh_result)
            fresh_skills = check_call_order(provider, fresh_from)
            fresh_toolkit_prompt = next(
                (item for item in fresh_skills if item.startswith("skills:")), ""
            )
            if EXPECTED_SKILLS not in fresh_toolkit_prompt.replace(" ", ""):
                raise AssertionError(
                    f"fresh session did not receive exactly the Toolkit skills: {fresh_toolkit_prompt!r}"
                )

            failure = turns["failed-candidate-write"]
            failure_result = harness.turn_output(
                failure,
                [
                    "run",
                    "--session",
                    sid_c,
                    "--agent",
                    "drafting-table",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    failure["prompt"],
                ],
            )
            provider.assert_consumed(failure)
            if expectation["failure_diagnostic"] not in failure_result:
                raise AssertionError(
                    f"ears-manager validation failure was not returned unchanged: {failure_result}"
                )
            session_c_export, session_c_sanitized = harness.export(sid_c)

            candidate_resume = turns["candidate-present-resume"]
            sid_d, candidate_resume_result = harness.new_role_session(
                candidate_resume, expectation["start_before_first_write"]
            )
            accepted_write = turns["accepted-candidate-write"]
            accepted_write_result = harness.turn_output(
                accepted_write,
                [
                    "run",
                    "--session",
                    sid_d,
                    "--agent",
                    "drafting-table",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    accepted_write["prompt"],
                ],
            )
            provider.assert_consumed(accepted_write)
            session_d_export, session_d_sanitized = harness.export(sid_d)

            review = turns["review-proposal"]
            sid_e, review_result = harness.new_role_session(
                review, expectation["start_before_first_write"]
            )
            if (
                base_commit not in review_result
                or fixture["requirement_id"] not in review_result
            ):
                raise AssertionError(
                    "the final-review response did not identify the exact proposal and base"
                )
            if "drafting-table-guard" not in review_result:
                raise AssertionError(
                    "the unsafe shell write was not refused through the active guard"
                )
            if not any(
                event["tool"] == "bash"
                and event["input"].get("command") == expectation["guard_refusal"]
                and event["status"] == "error"
                and "drafting-table-guard" in event["error"]
                for event in tool_events(review_result)
            ):
                raise AssertionError(
                    "the golden unsafe shell command did not receive an active guard refusal: "
                    f"{tool_events(review_result)!r}"
                )
            session_e_export, session_e_sanitized = harness.export(sid_e)

            approved_resume = turns["approved-proposal-resume"]
            sid_f, approved_resume_result = harness.new_role_session(
                approved_resume, expectation["start_before_first_write"]
            )
            if (
                base_commit not in approved_resume_result
                or fixture["requirement_id"] not in approved_resume_result
            ):
                raise AssertionError(
                    "fresh approval session did not reconstruct the exact proposal and base"
                )

            approval = turns["explicit-approval-and-handoff"]
            approval_prompt = approval["prompt"].replace("<BASE_COMMIT>", base_commit)
            approval = {**approval, "prompt": approval_prompt}
            approval_result = harness.turn_output(
                approval,
                [
                    "run",
                    "--session",
                    sid_f,
                    "--agent",
                    "drafting-table",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    approval_prompt,
                ],
            )
            provider.assert_consumed(approval)
            if "pull request" not in approval_result.lower():
                raise AssertionError(
                    f"approval did not complete an SCM handoff: {approval_result}"
                )
            approval_tools = tool_events(approval_result)
            for tool_name in ("scm_commit", "scm_publish"):
                if not any(
                    event["tool"] == tool_name and event["status"] == "completed"
                    for event in approval_tools
                ):
                    raise AssertionError(
                        f"explicit approval did not complete {tool_name}: {approval_tools!r}"
                    )

            session_f_export, session_f_sanitized = harness.export(sid_f)

            patch_turn = turns["subdirectory-apply-patch"]
            sid_g, patch_result = harness.new_role_session(
                patch_turn, expectation["start_before_first_write"], cwd=nested
            )
            patch_vector = expectation["apply_patch_guard_vector"]
            if nested.relative_to(project).as_posix() != patch_vector["cwd"]:
                raise AssertionError(
                    "subdirectory apply_patch guard vector used an unexpected working directory"
                )
            patch_input = {
                "hook_event_name": "PreToolUse",
                "session_id": sid_g,
                "cwd": str(nested),
                "tool_name": "apply_patch",
                "tool_input": {"patchText": patch_vector["patch_text"]},
            }
            patch_vectors = []
            for role, expected_reason in (
                ("drafting-table", expectation["apply_patch_drafting_table_refusal"]),
                ("other", expectation["apply_patch_other_role_refusal"]),
            ):
                result = run(
                    [
                        str(bindir / "drafting-table-guard"),
                        "--harness",
                        "opencode",
                        "--role",
                        role,
                    ],
                    cwd=nested,
                    env=env,
                    check=False,
                    input_text=json.dumps(patch_input),
                )
                if result.returncode != 2 or expected_reason not in result.stderr:
                    raise AssertionError(
                        f"subdirectory apply_patch guard vector failed for {role}: "
                        f"exit={result.returncode}, stderr={result.stderr!r}"
                    )
                if (
                    patch_vector["project_root_relative_target"]
                    not in patch_vector["patch_text"]
                ):
                    raise AssertionError(
                        "apply_patch guard vector omitted the project-root-relative target"
                    )
                patch_vectors.append(
                    {
                        "role": role,
                        "exit": result.returncode,
                        "reason": result.stderr.strip(),
                    }
                )
                if vision_path.read_bytes() != vision_before_patch:
                    raise AssertionError(
                        f"apply_patch guard vector for {role} changed registered Vision bytes"
                    )
            native_tools = json.loads(
                opencode_command(
                    binary,
                    ["debug", "agent", "drafting-table"],
                    project=nested,
                    env=env,
                ).stdout
            ).get("tools", {})
            native_apply_patch_available = bool(native_tools.get("apply_patch", False))
            if (
                native_apply_patch_available
                != expectation["opencode_native_apply_patch_tool_available"]
            ):
                raise AssertionError(
                    "the pinned OpenCode apply_patch tool registry differs from golden output"
                )
            vision_unchanged_after_patch = (
                vision_path.read_bytes() == vision_before_patch
            )
            if not vision_unchanged_after_patch:
                raise AssertionError(
                    "subdirectory apply_patch changed the registered Vision bytes"
                )
            session_g_export, session_g_sanitized = harness.export(sid_g)

            non_role_turn = turns["non-role-redirection-vectors"]
            non_role_result = harness.turn_output(
                non_role_turn,
                [
                    "run",
                    "--agent",
                    "build",
                    "--format",
                    "json",
                    "--model",
                    fixture["model"],
                    non_role_turn["prompt"],
                ],
            )
            provider.assert_consumed(non_role_turn)
            non_role_events = tool_events(non_role_result)
            for expected_command in expectation["non_role_redirections"]:
                event = next(
                    (
                        event
                        for event in non_role_events
                        if event["tool"] == "bash"
                        and event["input"].get("command") == expected_command
                    ),
                    None,
                )
                if (
                    event is None
                    or event["status"] != "error"
                    or "drafting-table-guard" not in event["error"]
                ):
                    raise AssertionError(
                        f"non-role redirection was not refused by the active guard: {event!r}"
                    )
            vision_unchanged = vision_path.read_bytes() == vision_before_patch
            if not vision_unchanged:
                raise AssertionError(
                    "an apply_patch or non-role shell redirection changed registered Vision"
                )
            if (
                expectation["vision_unchanged_after_non_role_redirects"]
                != vision_unchanged
            ):
                raise AssertionError(
                    "Vision byte-preservation evidence differs from golden output"
                )
            sid_h = session_id(non_role_result)
            session_h_export, session_h_sanitized = harness.export(
                sid_h, expected_skill_markers=(), expected_agent="build"
            )
            exports = (
                session_a_export,
                session_b_export,
                session_c_export,
                session_d_export,
                session_e_export,
                session_f_export,
                session_g_export,
                session_jobsite_export,
                session_h_export,
            )
            if not any(
                "drafting-table-guard" in content
                or "requirement.ears_pattern_mismatch" in content
                for content in exports
            ):
                raise AssertionError(
                    "session exports do not preserve tool refusal/failure evidence"
                )

            # Verify real persistent state independently of the chat trace.
            req_list = run(
                [
                    str(bindir / "ears-manager"),
                    "--output",
                    "json",
                    "requirement",
                    "list",
                ],
                cwd=project,
                env=env,
            )
            reqs = json_output(req_list)["data"].get("requirements", [])
            req = next(
                (item for item in reqs if item.get("id") == fixture["requirement_id"]),
                None,
            )
            if (
                req is None
                or req.get("text") != fixture["requirement_text"]
                or req.get("verification", {}).get("rationale")
                != fixture["verification_rationale"]
            ):
                raise AssertionError(
                    f"approved candidate is not in authoritative state: {req_list.stdout}; "
                    f"candidate write event={accepted_write_result}"
                )
            artifact_list = run(
                [str(bindir / "ears-manager"), "--output", "json", "artifact", "list"],
                cwd=project,
                env=env,
            )
            artifacts = json_output(artifact_list)["data"].get("artifacts", [])
            if {item.get("id") for item in artifacts} != {"architecture", "vision"}:
                raise AssertionError(
                    f"real ears-manager artifact list returned unexpected entries: {artifact_list.stdout}"
                )
            git_status = run(
                ["git", "status", "--porcelain"], cwd=project, env=env
            ).stdout
            if git_status.strip():
                raise AssertionError(
                    f"SCM did not leave the change-set worktree clean: {git_status}"
                )
            paths = run(
                ["git", "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"],
                cwd=project,
                env=env,
            ).stdout.splitlines()
            gh_events = read_jsonl(Path(env["DT_GH_LOG"]))
            pr_creates = [
                event for event in gh_events if event.get("event") == "create"
            ]
            if len(pr_creates) != 1:
                raise AssertionError(
                    f"expected one recording gh PR create, got {gh_events!r}"
                )
            gh_args = pr_creates[0]["argv"]
            if (
                "--repo" not in gh_args
                or "--base" not in gh_args
                or "--head" not in gh_args
            ):
                raise AssertionError(
                    f"SCM omitted canonical PR binding fields: {gh_args!r}"
                )
            if (
                "--repo" in gh_args
                and gh_args[gh_args.index("--repo") + 1]
                != "github.com/protobot-fixture/fixture"
            ):
                raise AssertionError(f"PR used a non-canonical repository: {gh_args!r}")
            if gh_args[gh_args.index("--base") + 1] != "main":
                raise AssertionError(f"PR used an unexpected base branch: {gh_args!r}")
            if (
                gh_args[gh_args.index("--head") + 1]
                != "cs/00002-exercise-the-opencode-drafting-table"
            ):
                raise AssertionError(
                    f"PR used an unexpected change-set head: {gh_args!r}"
                )
            if len(pr_creates) != expectation["handoff"]["gh_pr_create"]:
                raise AssertionError(
                    "recorded Git host create count differs from golden output"
                )
            if any(event.get("event") not in ("list", "create") for event in gh_events):
                raise AssertionError(
                    f"unexpected Git host action after handoff: {gh_events!r}"
                )

            wms_events = read_jsonl(wms_log)
            calls = [event for event in wms_events if event.get("event") == "call"]
            wms_started = sum(event.get("event") == "started" for event in wms_events)
            wms_stopped = sum(event.get("event") == "stopped" for event in wms_events)
            if not any(
                event.get("tool") == "blocked_work_query" for event in calls
            ):
                raise AssertionError(f"WMS blocked work was not read: {wms_events!r}")
            if wms_started == 0 or wms_started != wms_stopped:
                raise AssertionError(
                    f"recording WMS MCP process did not stop cleanly: {wms_events!r}"
                )
            if any(event.get("before") != event.get("after") for event in calls):
                raise AssertionError(
                    f"WMS lifecycle state changed in the recording stub: {wms_events!r}"
                )
            for request in provider.requests:
                if SENTINEL in json.dumps(request, ensure_ascii=False):
                    raise AssertionError(
                        "a planted token-shaped secret reached the replay model"
                    )
            for record in (*gh_events, *wms_events):
                if SENTINEL in json.dumps(record, ensure_ascii=False):
                    raise AssertionError(
                        "a planted token-shaped secret reached a local recording stub"
                    )

            all_output = [
                prelude.stdout,
                start_result,
                failure_result,
                fresh_result,
                skill_result,
                candidate_resume_result,
                accepted_write_result,
                review_result,
                approved_resume_result,
                approval_result,
                jobsite_result,
                session_a_export,
                session_a_sanitized,
                session_b_export,
                session_b_sanitized,
                session_jobsite_export,
                session_jobsite_sanitized,
                session_c_export,
                session_c_sanitized,
                session_d_export,
                session_d_sanitized,
                session_e_export,
                session_e_sanitized,
                session_f_export,
                session_f_sanitized,
                patch_result,
                session_g_export,
                session_g_sanitized,
                non_role_result,
                session_h_export,
                session_h_sanitized,
            ]
            if any(
                re.search(
                    r"permission (?:prompt|rejection|request)|automatic permission",
                    output,
                    re.IGNORECASE,
                )
                for output in all_output
            ):
                raise AssertionError(
                    "headless replay emitted a permission prompt/rejection"
                )

            input_hashes = source_hashes()
            summary = {
                "fixture": fixture["name"],
                "opencode_version": binary_version,
                "recorded_model": fixture["model"],
                "chat": {
                    "candidate_reviewed": fixture["requirement_id"],
                    "candidate_status": fixture["candidate_status"],
                    "validation_failure_reported": "requirement.ears_pattern_mismatch"
                    in failure_result,
                    "active_guard_refusal_reported": "drafting-table-guard"
                    in review_result,
                    "subdirectory_apply_patch_refused": len(patch_vectors) == 2,
                    "subdirectory_apply_patch_target": expectation[
                        "apply_patch_guard_vector"
                    ]["project_root_relative_target"],
                    "opencode_native_apply_patch_tool_available": native_apply_patch_available,
                    "apply_patch_guard_roles": [
                        vector["role"] for vector in patch_vectors
                    ],
                    "non_role_redirections_refused": all(
                        event["status"] == "error"
                        and "drafting-table-guard" in event["error"]
                        for event in non_role_events
                        if event["tool"] == "bash"
                    ),
                    "explicit_approval_and_handoff": "pull request"
                    in approval_result.lower(),
                    "jobsite_lifecycle_refused": jobsite_guard.returncode == 2
                    and jobsite_refusal["tool"] not in offered,
                    "session_exported": True,
                    "session_export_count": len(exports),
                    "vision_unchanged_after_write_probes": vision_unchanged,
                    "vision_unchanged_after_apply_patch": vision_unchanged_after_patch,
                    "vision_sha256": hashlib.sha256(
                        vision_path.read_bytes()
                    ).hexdigest(),
                },
                "git": {
                    "branch": "cs/00002-exercise-the-opencode-drafting-table",
                    "local_bare_origin_only": effective_remote.startswith("file://"),
                    "change_set_committed": fixture["requirement_id"]
                    in req_list.stdout,
                    "pr_create_count": len(pr_creates),
                    "pr_repository": "github.com/protobot-fixture/fixture",
                    "pr_base": "main",
                    "merged": expectation["handoff"]["merged"],
                    "committed_paths": sorted(paths),
                    "working_tree_clean": True,
                },
                "wms": {
                    "blocked_item": fixture["blocked_work_item"]["id"],
                    "state_before": fixture["blocked_work_item"]["state"],
                    "state_after": fixture["blocked_work_item"]["state"],
                    "mutations": 0,
                },
                "source_input_sha256": input_hashes,
            }
            if summary["chat"]["session_export_count"] != expectation["exports"]:
                raise AssertionError(
                    f"golden expects {expectation['exports']} exports, got "
                    f"{summary['chat']['session_export_count']}"
                )
            stable = json.dumps(summary, sort_keys=True, separators=(",", ":"))
            return {
                "summary": summary,
                "stable": stable,
                "narrative": harness.narrative,
                "session_ids": [
                    sid_a,
                    sid_b,
                    sid_jobsite,
                    sid_c,
                    sid_d,
                    sid_e,
                    sid_f,
                    sid_g,
                    sid_h,
                ],
                "project_context": project_context,
            }
        finally:
            harness.dispose()


def run_rejection(
    binary: str,
    fixture: dict[str, Any],
    scenario: dict[str, Any],
    bindir: Path,
) -> dict[str, Any]:
    """Rejection returns to the prior checkpoint; revision goes through
    ears-manager only and stays uncommitted for renewed review."""
    turns = scenario["turns"]
    expectation = scenario["expectation"]
    harness = ScenarioRun(binary, fixture, bindir)
    with harness:
        provider = harness.provider
        project = harness.project
        env = harness.env
        wms_log = harness.wms_log
        gh_log = Path(env["DT_GH_LOG"])
        ears = str(harness.bindir / "ears-manager")

        base_commit = harness.base_commit(CHANGE_SET_ID)
        provider.replacements["<BASE_COMMIT>"] = base_commit

        presentation = turns["rejection-presentation"]
        sid, presentation_result = harness.new_role_session(
            presentation, expectation["start_before_first_write"]
        )
        if (
            base_commit not in presentation_result
            or fixture["requirement_id"] not in presentation_result
        ):
            raise AssertionError(
                "rejection presentation did not identify the exact proposal and base: "
                f"{presentation_result}"
            )

        # Rejection is rejection-as-revision: no tool call, no WMS record,
        # no host action, no commit, and the draft is preserved.
        rejection = turns["explicit-rejection"]
        calls_before = wms_calls(wms_log)
        gh_before = read_jsonl(gh_log)
        status_before = run(
            ["git", "status", "--porcelain"], cwd=project, env=env
        ).stdout
        head_before = harness.git_head()
        rejection_result = harness.resume_session(rejection, sid)
        if tool_events(rejection_result):
            raise AssertionError(
                f"explicit rejection made tool calls: {tool_events(rejection_result)!r}"
            )
        if wms_calls(wms_log) != calls_before:
            raise AssertionError("explicit rejection changed WMS records")
        if (
            run(["git", "status", "--porcelain"], cwd=project, env=env).stdout
            != status_before
        ):
            raise AssertionError("explicit rejection changed the working tree")
        if harness.git_head() != head_before:
            raise AssertionError("explicit rejection moved the checked-out branch")
        if read_jsonl(gh_log) != gh_before:
            raise AssertionError(
                f"explicit rejection reached the Git host: {read_jsonl(gh_log)!r}"
            )
        req_list = run(
            [ears, "--output", "json", "requirement", "list"], cwd=project, env=env
        )
        if fixture["requirement_id"] not in req_list.stdout:
            raise AssertionError(
                f"rejected proposal did not preserve the draft candidate: {req_list.stdout}"
            )

        # The requested revision is written through ears-manager only in a
        # fresh session that reconstructs state first, then validation and
        # impact rerun; no commit or publish follows.
        revision = turns["rejection-vision-revision"]
        sid_revision, revision_result = harness.new_role_session(
            revision, expectation["start_before_first_write"]
        )
        revision_events = tool_events(revision_result)
        if any(
            event["tool"] in ("scm_commit", "scm_publish") for event in revision_events
        ):
            raise AssertionError(
                f"revision attempted a Git operation: {revision_events!r}"
            )
        vision_revision = expectation["vision_revision"]
        vision_path = project / "docs/vision.md"
        if vision_path.read_text() != vision_revision["content"]:
            raise AssertionError(
                "revised Vision content differs from golden output: "
                f"{vision_path.read_text()!r}"
            )
        assert_git_status(project, env, vision_revision["git_status_paths"])
        if harness.git_head() != head_before:
            raise AssertionError("revision committed without explicit approval")
        if any(
            event.get("before") != event.get("after")
            or event.get("mutation") != "none"
            for event in wms_calls(wms_log)
        ):
            raise AssertionError("revision changed WMS records")
        if any(event.get("event") != "list" for event in read_jsonl(gh_log)):
            raise AssertionError(
                f"revision reached the Git host: {read_jsonl(gh_log)!r}"
            )
        check = run(
            [ears, "--output", "json", "check", "--change-set", CHANGE_SET_ID],
            cwd=project,
            env=env,
        )
        if "artifact.digest_mismatch" in check.stdout:
            raise AssertionError(
                f"revised Vision failed the registry digest check: {check.stdout}"
            )

        session_export, session_sanitized = harness.export(sid)
        if SENTINEL in session_export or SENTINEL in session_sanitized:
            raise AssertionError(
                "a planted token-shaped secret appeared in a session export"
            )
        revision_export, revision_sanitized = harness.export(sid_revision)
        if SENTINEL in revision_export or SENTINEL in revision_sanitized:
            raise AssertionError(
                "a planted token-shaped secret appeared in a session export"
            )

        summary = {
            "fixture": fixture["name"],
            "opencode_version": harness.binary_version,
            "recorded_model": fixture["model"],
            "chat": {
                "candidate_presented": fixture["requirement_id"],
                "explicit_rejection": {
                    "tool_calls": 0,
                    "returned_to_prior_checkpoint": True,
                    "baseline_commit_unchanged": True,
                },
                "revision": {
                    "vision_revised": True,
                    "vision_content": vision_revision["content"],
                    "committed": False,
                },
                "session_exported": True,
                "session_export_count": 2,
            },
            "git": {
                "pr_create_count": 0,
                "working_tree_status_paths": sorted(vision_revision["git_status_paths"]),
            },
            "wms": {
                "blocked_item": fixture["blocked_work_item"]["id"],
                "state_before": fixture["blocked_work_item"]["state"],
                "state_after": expectation["wms_state_after"],
                "mutations": 0,
            },
            "source_input_sha256": source_hashes(),
        }
        if summary["chat"]["session_export_count"] != expectation["exports"]:
            raise AssertionError(
                f"golden expects {expectation['exports']} exports, got "
                f"{summary['chat']['session_export_count']}"
            )
        stable = json.dumps(summary, sort_keys=True, separators=(",", ":"))
        return {
            "summary": summary,
            "stable": stable,
            "narrative": harness.narrative,
            "session_ids": [sid, sid_revision],
        }


def run_wms_unavailable(
    binary: str,
    fixture: dict[str, Any],
    scenario: dict[str, Any],
    bindir: Path,
) -> dict[str, Any]:
    """A session started with the WMS stub stopped marks blocked work
    unavailable and continues drafting without any WMS call."""
    turns = scenario["turns"]
    expectation = scenario["expectation"]
    harness = ScenarioRun(binary, fixture, bindir, wms_mode="fail-start")
    with harness:
        project = harness.project
        env = harness.env
        gh_log = Path(env["DT_GH_LOG"])

        turn = turns["wms-down-start"]
        sid, output = harness.new_role_session(
            turn, expectation["start_before_first_write"]
        )
        if "unavailable" not in output.lower():
            raise AssertionError(
                f"session did not mark blocked work unavailable: {output}"
            )
        events = tool_events(output)
        if any(event["tool"].startswith("wms_") for event in events):
            raise AssertionError(f"a WMS call was attempted: {events!r}")
        # The stub never served, so OpenCode could not offer WMS tools at all.
        for request in harness.provider.requests:
            offered = {
                tool.get("function", {}).get("name")
                for tool in request.get("tools") or []
            }
            wms_offered = sorted(
                str(name) for name in offered if str(name).startswith("wms_")
            )
            if wms_offered:
                raise AssertionError(
                    f"WMS tools were offered while the stub was down: {wms_offered!r}"
                )
        names = [canonical_tool_event(event) for event in events]
        for name in expectation["start_before_first_write"]:
            if name not in names:
                raise AssertionError(
                    f"governed read {name!r} did not complete while the WMS was down; "
                    f"tool sequence={names!r}"
                )
        wms_events = read_jsonl(harness.wms_log)
        if any(event.get("event") == "call" for event in wms_events):
            raise AssertionError(f"WMS calls were recorded: {wms_events!r}")
        if any(event.get("event") == "started" for event in wms_events):
            raise AssertionError(f"the WMS stub served while down: {wms_events!r}")
        if not any(event.get("event") == "failed-start" for event in wms_events):
            raise AssertionError(f"the WMS stub did not record its failed start: {wms_events!r}")
        gh_events = read_jsonl(gh_log)
        if any(event.get("event") != "list" for event in gh_events):
            raise AssertionError(
                f"WMS-unavailable session reached the Git host: {gh_events!r}"
            )
        assert_git_status(project, env, expectation["git_status_paths"])

        session_export, session_sanitized = harness.export(sid)
        if SENTINEL in session_export or SENTINEL in session_sanitized:
            raise AssertionError(
                "a planted token-shaped secret appeared in a session export"
            )

        summary = {
            "fixture": fixture["name"],
            "opencode_version": harness.binary_version,
            "recorded_model": fixture["model"],
            "chat": {
                "blocked_work_marked_unavailable": True,
                "drafting_continued": True,
                "wms_tools_offered": 0,
                "session_exported": True,
                "session_export_count": 1,
            },
            "git": {"working_tree_clean": True, "pr_create_count": 0},
            "wms": {
                "wms_available": False,
                "wms_calls_succeeded": 0,
                "mutations": 0,
            },
            "source_input_sha256": source_hashes(),
        }
        if summary["chat"]["session_export_count"] != expectation["exports"]:
            raise AssertionError(
                f"golden expects {expectation['exports']} exports, got "
                f"{summary['chat']['session_export_count']}"
            )
        stable = json.dumps(summary, sort_keys=True, separators=(",", ":"))
        return {
            "summary": summary,
            "stable": stable,
            "narrative": harness.narrative,
            "session_ids": [sid],
        }


def push_default_move(harness: ScenarioRun) -> str:
    """Push one commit to the default branch of the bare origin from
    outside any session; returns the new default-branch head."""
    env = harness.env
    mover = harness.temp / "mover"
    bare = Path(harness.project_context["bare"])
    run(["git", "clone", "--quiet", str(bare), str(mover)], cwd=harness.temp, env=env)
    (mover / "default-move.md").write_text("The default branch moved.\n")
    run(["git", "add", "default-move.md"], cwd=mover, env=env)
    run(["git", "commit", "--quiet", "-m", "Default branch move"], cwd=mover, env=env)
    run(["git", "push", "--quiet", "origin", "main"], cwd=mover, env=env)
    return run(["git", "rev-parse", "HEAD"], cwd=mover, env=env).stdout.strip()


def run_stale(
    binary: str,
    fixture: dict[str, Any],
    scenario: dict[str, Any],
    bindir: Path,
) -> dict[str, Any]:
    """A default-branch move between session pause and resume is detected
    on resume, publish fails DEFAULT_MOVED, and the refresh sequence ends
    in one pull request, one host merge, and one idempotent registration."""
    turns = scenario["turns"]
    expectation = scenario["expectation"]
    change_set = expectation["change_set"]
    harness = ScenarioRun(binary, fixture, bindir, change_set_id=change_set)
    with harness:
        provider = harness.provider
        project = harness.project
        env = harness.env
        wms_log = harness.wms_log
        gh_log = Path(env["DT_GH_LOG"])
        env["DT_GH_STATE"] = str(harness.temp / "gh-state.json")
        env["DT_GH_ORIGIN"] = harness.project_context["bare"]
        env["DT_REG_LOG"] = str(harness.temp / "register-events.jsonl")
        env["DT_REG_PROJECT"] = fixture["project_id"]

        base_commit = harness.base_commit(change_set)
        provider.replacements["<BASE_COMMIT>"] = base_commit

        candidate = turns["stale-candidate"]
        sid_candidate, candidate_result = harness.new_role_session(
            candidate, expectation["start_before_first_write"]
        )
        if expectation["requirement_id"] not in candidate_result:
            raise AssertionError(
                f"stale candidate was not written: {candidate_result}"
            )

        review = turns["stale-review"]
        sid_review, review_result = harness.new_role_session(
            review, expectation["start_before_first_write"]
        )
        if (
            base_commit not in review_result
            or expectation["requirement_id"] not in review_result
        ):
            raise AssertionError(
                "the stale review did not identify the exact proposal and base: "
                f"{review_result}"
            )

        # The default branch moves from outside the session.
        new_head = push_default_move(harness)
        provider.replacements["<NEW_BASE>"] = new_head

        # The resumed session re-presents CS-00003, its branch, and the
        # recorded base from governed reads, not from conversation.
        resume = turns["stale-resume-after-move"]
        resume_result = harness.resume_session(resume, sid_review)
        repo_event = next(
            (
                event
                for event in tool_events(resume_result)
                if event["tool"] == "scm_repo_state"
            ),
            None,
        )
        if (
            base_commit not in resume_result
            or repo_event is None
            or '"default_moved":true' not in repo_event["output"]
        ):
            raise AssertionError(
                f"resume did not detect the default-branch move: {resume_result}"
            )

        # Explicit approval bound to the reviewed base: the commit lands,
        # and publish refuses because the default branch moved.
        approval = turns["stale-approval-default-moved"]
        approval_prompt = approval["prompt"].replace("<BASE_COMMIT>", base_commit)
        approval = {**approval, "prompt": approval_prompt}
        sid_approval, approval_result = harness.new_role_session(
            approval, expectation["start_before_first_write"]
        )
        if base_commit not in approval_result:
            raise AssertionError(
                f"approval session lost the exact base: {approval_result}"
            )
        approval_events = tool_events(approval_result)
        if not any(
            event["tool"] == "scm_commit" and event["status"] == "completed"
            for event in approval_events
        ):
            raise AssertionError(
                f"explicit approval did not complete scm_commit: {approval_events!r}"
            )
        publish_event = next(
            (event for event in approval_events if event["tool"] == "scm_publish"),
            None,
        )
        if (
            publish_event is None
            or publish_event["status"] != "error"
            or expectation["publish_failure"]
            not in publish_event["error"] + str(publish_event["output"])
        ):
            raise AssertionError(
                f"publish did not fail {expectation['publish_failure']}: "
                f"{approval_events!r}"
            )
        if any(event.get("event") == "create" for event in read_jsonl(gh_log)):
            raise AssertionError("a failed publish still created a pull request")

        # The refresh sequence: merge the moved default, rebind the base
        # commit, rerun impact and validation, and commit again.
        refresh = turns["stale-refresh-and-update"]
        sid_refresh, refresh_result = harness.new_role_session(
            refresh, expectation["start_before_first_write"]
        )
        refresh_events = tool_events(refresh_result)
        for tool_name in ("scm_refresh", "scm_repo_state"):
            if not any(
                event["tool"] == tool_name and event["status"] == "completed"
                for event in refresh_events
            ):
                raise AssertionError(
                    f"refresh sequence did not complete {tool_name}: {refresh_events!r}"
                )
        parents = run(
            ["git", "rev-list", "--parents", "-n", "1", "HEAD"],
            cwd=project,
            env=env,
        ).stdout.split()
        if len(parents) < 3:
            raise AssertionError(
                f"refresh did not merge the moved default branch: {parents!r}"
            )
        if harness.base_commit(change_set) != new_head:
            raise AssertionError(
                "change-set update did not rebind the base commit to the new head"
            )

        republish = turns["stale-recommit-and-publish"]
        sid_republish, republish_result = harness.new_role_session(
            republish, expectation["start_before_first_write"]
        )
        republish_events = tool_events(republish_result)
        for tool_name in ("scm_commit", "scm_publish"):
            if not any(
                event["tool"] == tool_name and event["status"] == "completed"
                for event in republish_events
            ):
                raise AssertionError(
                    f"re-publish did not complete {tool_name}: {republish_events!r}"
                )
        if "pull request" not in republish_result.lower():
            raise AssertionError(
                f"re-publish did not hand off a pull request: {republish_result}"
            )

        # The user merges the pull request on the host.
        merged = run(
            [str(harness.bindir / "gh"), "pr", "merge", "1", "--merge"],
            cwd=project,
            env=env,
            check=False,
        )
        if merged.returncode:
            raise AssertionError(
                f"host merge failed: {merged.returncode}\n{merged.stdout}\n{merged.stderr}"
            )

        register = turns["stale-register"]
        sid_register, register_result = harness.new_role_session(
            register, expectation["start_before_first_write"]
        )
        register_events = tool_events(register_result)
        if not any(
            event["tool"] == "bash"
            and event["input"].get("command")
            == f"register-approved-change-set --change-set {change_set}"
            and event["status"] == "completed"
            for event in register_events
        ):
            raise AssertionError(
                f"registration command did not complete: {register_events!r}"
            )

        # A retry after registration is the same idempotent call.
        register_again = turns["stale-register-again"]
        register_again_result = harness.resume_session(register_again, sid_register)
        again_events = tool_events(register_again_result)
        if not any(
            event["tool"] == "bash"
            and event["input"].get("command")
            == f"register-approved-change-set --change-set {change_set}"
            and event["status"] == "completed"
            for event in again_events
        ):
            raise AssertionError(
                f"idempotent registration retry did not complete: {again_events!r}"
            )
        registrations = read_jsonl(Path(env["DT_REG_LOG"]))
        if len(registrations) != expectation["registration"]["calls"]:
            raise AssertionError(
                f"expected {expectation['registration']['calls']} registration "
                f"record(s), got {registrations!r}"
            )
        if registrations[0]["change_set"] != change_set:
            raise AssertionError(
                f"registration named the wrong change set: {registrations!r}"
            )

        gh_events = read_jsonl(gh_log)
        pr_creates = [event for event in gh_events if event.get("event") == "create"]
        pr_merges = [event for event in gh_events if event.get("event") == "merge"]
        if len(pr_creates) != 1 or len(pr_merges) != 1:
            raise AssertionError(
                f"expected one pull-request create and one merge, got {gh_events!r}"
            )
        gh_args = pr_creates[0]["argv"]
        if (
            gh_args[gh_args.index("--repo") + 1]
            != "github.com/protobot-fixture/fixture"
            or gh_args[gh_args.index("--base") + 1] != "main"
            or gh_args[gh_args.index("--head") + 1] != expectation["pr_head"]
        ):
            raise AssertionError(
                f"pull request used unexpected binding fields: {gh_args!r}"
            )
        if any(
            event.get("event") not in ("list", "create", "merge")
            for event in gh_events
        ):
            raise AssertionError(f"unexpected Git host action: {gh_events!r}")
        assert_git_status(project, env, [])
        if any(
            event.get("before") != event.get("after")
            or event.get("mutation") != "none"
            for event in wms_calls(wms_log)
        ):
            raise AssertionError("stale-state scenario changed WMS records")

        exports = []
        for sid in (
            sid_candidate,
            sid_review,
            sid_approval,
            sid_refresh,
            sid_republish,
            sid_register,
        ):
            raw, sanitized = harness.export(sid)
            if SENTINEL in raw or SENTINEL in sanitized:
                raise AssertionError(
                    "a planted token-shaped secret appeared in a session export"
                )
            exports.append(raw)

        summary = {
            "fixture": fixture["name"],
            "opencode_version": harness.binary_version,
            "recorded_model": fixture["model"],
            "chat": {
                "candidate_reviewed": expectation["requirement_id"],
                "default_moved_detected": True,
                "publish_failed_default_moved": True,
                "refresh_merge_parents": len(parents) - 1,
                "base_commit_updated": harness.base_commit(change_set) == new_head,
                "explicit_approval_and_handoff": True,
                "registered": len(registrations) == 1,
                "registration_idempotent": True,
                "session_exported": True,
                "session_export_count": len(exports),
            },
            "git": {
                "branch": expectation["pr_head"],
                "pr_create_count": len(pr_creates),
                "pr_merge_count": len(pr_merges),
                "pr_base": "main",
                "merged": True,
                "working_tree_clean": True,
            },
            "wms": {
                "blocked_item": fixture["blocked_work_item"]["id"],
                "state_before": fixture["blocked_work_item"]["state"],
                "state_after": expectation["wms_state_after"],
                "mutations": 0,
            },
            "source_input_sha256": source_hashes(),
        }
        if summary["chat"]["session_export_count"] != expectation["exports"]:
            raise AssertionError(
                f"golden expects {expectation['exports']} exports, got "
                f"{summary['chat']['session_export_count']}"
            )
        stable = json.dumps(summary, sort_keys=True, separators=(",", ":"))
        return {
            "summary": summary,
            "stable": stable,
            "narrative": harness.narrative,
            "session_ids": [
                sid_candidate,
                sid_review,
                sid_approval,
                sid_refresh,
                sid_republish,
                sid_register,
            ],
        }


def export_session(
    binary: str,
    session: str,
    project: Path,
    env: dict[str, str],
    expected_skill_markers: tuple[str, ...] = ("# Drafting Specifications",),
    expected_agent: str = "drafting-table",
) -> tuple[str, str]:
    raw = opencode_command(binary, ["export", session], project=project, env=env).stdout
    sanitized = opencode_command(
        binary, ["export", session, "--sanitize"], project=project, env=env
    ).stdout
    if SENTINEL in raw or SENTINEL in sanitized:
        raise AssertionError(
            "a planted token-shaped secret appeared in a session export"
        )
    data = json.loads(raw)
    info = data.get("info", {})
    model = info.get("model", {})
    if (
        info.get("agent") != expected_agent
        or model.get("providerID") != "replay"
        or model.get("id") != "turns"
        or info.get("version") != PINNED_OPENCODE
    ):
        raise AssertionError(
            "session export is missing the Drafting Table role, replay model, or pinned version"
        )
    serialized = json.dumps(data, ensure_ascii=False)
    for marker in expected_skill_markers:
        if marker not in serialized:
            raise AssertionError(
                f"session export omitted loaded skill content {marker!r}"
            )
    return raw, sanitized


def invoke_turn(
    binary: str,
    provider: ReplayServer,
    turn: dict[str, Any],
    name: str,
    args: list[str],
    project: Path,
    env: dict[str, str],
    cwd: Path | None = None,
) -> str:
    provider.set_turn(turn, project)
    before = len(provider.requests)
    working_directory = cwd or project
    completed = opencode_command(
        binary, args, project=working_directory, env=env, timeout=240
    )
    if "Executable not found in $PATH" in completed.stdout:
        guard_candidates = [
            Path(entry) / "drafting-table-guard"
            for entry in env.get("PATH", "").split(os.pathsep)
        ]
        guard_info = [
            (
                str(path),
                path.exists(),
                oct(path.stat().st_mode & 0o777) if path.exists() else None,
            )
            for path in guard_candidates
            if path.name == "drafting-table-guard"
        ]
        raise AssertionError(
            f"OpenCode could not resolve the built guard; PATH={env.get('PATH')!r}; "
            f"guard={guard_info!r}; stderr={completed.stderr!r}"
        )
    provider.assert_consumed(turn)
    # The response is returned unmodified apart from the session ID emitted
    # by OpenCode, which is not part of semantic assertions.
    if not completed.stdout.strip():
        raise AssertionError(f"OpenCode turn {name!r} produced no JSON event stream")
    if re.search(
        r"permission (?:prompt|rejection|request)|automatic permission",
        completed.stderr,
        re.IGNORECASE,
    ):
        raise AssertionError(
            f"OpenCode turn {name!r} emitted a permission prompt/rejection on stderr"
        )
    # Keep model-visible skill lists in the request evidence and ensure no
    # request used an unrecorded network/model provider.
    with provider.lock:
        requests = provider.requests[before:]
    if not requests:
        raise AssertionError(
            f"OpenCode turn {name!r} made no request to the replay provider"
        )
    for request in requests:
        if not str(request.get("model", "")).startswith("turns"):
            raise AssertionError(
                f"unexpected model/provider request: {request.get('model')!r}"
            )
    return completed.stdout


def read_jsonl(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def verify_binary(binary: str) -> None:
    with tempfile.TemporaryDirectory(prefix="opencode-version-check-") as temp:
        root = Path(temp)
        env = clean_env(os.environ, project=root, home=root / "home")
        completed = run([binary, "--version"], cwd=root, env=env, timeout=30)
        version = completed.stdout.strip()
        if version != PINNED_OPENCODE:
            raise AssertionError(
                f"OPENCODE_BIN must be OpenCode {PINNED_OPENCODE}; got {version!r}"
            )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--opencode-bin", default=os.environ.get("OPENCODE_BIN", "opencode")
    )
    args = parser.parse_args()
    binary = shutil.which(args.opencode_bin) or args.opencode_bin
    if not Path(binary).exists() and shutil.which(binary) is None:
        parser.error(f"OpenCode binary not found: {args.opencode_bin}")
    fixture, scenarios = read_golden()
    if fixture.get("opencode_version") != PINNED_OPENCODE:
        raise AssertionError(
            "golden fixture version does not match the pinned OpenCode version"
        )
    verify_binary(binary)
    runners = {
        "approved-handoff": run_approved,
        "rejection-revision": run_rejection,
        "wms-unavailable": run_wms_unavailable,
        "stale-state": run_stale,
    }
    results: dict[str, Any] = {}
    with tempfile.TemporaryDirectory(prefix="opencode-drafting-table-bin-") as work:
        bindir = build_binaries(Path(work), os.environ)
        for name in fixture["scenarios"]:
            runner = runners.get(name)
            if runner is None:
                raise AssertionError(f"no replay driver for golden scenario {name!r}")
            scenario = scenarios[name]
            first = runner(binary, fixture, scenario, bindir)
            second = runner(binary, fixture, scenario, bindir)
            if first["stable"] != second["stable"]:
                raise AssertionError(
                    f"repeat-run semantic/state summaries differ for scenario "
                    f"{name!r}\nfirst:  {first['stable']}\nsecond: {second['stable']}"
                )
            results[name] = first["summary"]
    print(
        json.dumps(
            {"result": "passed", "repeat_runs": 2, "scenarios": results},
            indent=2,
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
