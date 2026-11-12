import { spawn } from "node:child_process";

const sessionRoles = new Map();

function sessionIDOf(value) {
  const sessionID = value?.sessionID ?? value?.session_id;
  return typeof sessionID === "string" && sessionID.length > 0
    ? sessionID
    : undefined;
}

function guardInput({ sessionID, cwd, toolName, toolInput }) {
  return {
    hook_event_name: "PreToolUse",
    session_id: sessionID,
    cwd,
    tool_name: toolName,
    tool_input: toolInput,
  };
}

function invokeGuard(payload, role, cwd) {
  return new Promise((resolve, reject) => {
    let stderr = "";
    let settled = false;
    let child;

    const fail = (message) => {
      if (settled) return;
      settled = true;
      reject(new Error(message));
    };

    try {
      child = spawn(
        "drafting-table-guard",
        ["--harness", "opencode", "--role", role],
        {
          cwd,
          shell: false,
          stdio: ["pipe", "ignore", "pipe"],
        },
      );
    } catch (error) {
      fail(`drafting-table-guard: could not start guard: ${error.message}`);
      return;
    }

    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", (error) => {
      fail(`drafting-table-guard: could not invoke guard: ${error.message}`);
    });
    child.on("close", (code, signal) => {
      if (settled) return;
      settled = true;
      if (code === 0) {
        resolve();
        return;
      }

      const reason = stderr.trim().split("\n")[0];
      reject(
        new Error(
          reason ||
            `drafting-table-guard: exited with ${signal ? `signal ${signal}` : `status ${code}`}`,
        ),
      );
    });

    child.stdin.on("error", (error) => {
      fail(`drafting-table-guard: could not send hook input: ${error.message}`);
    });
    child.stdin.end(JSON.stringify(payload));
  });
}

export const ProtoBotGuard = async ({ directory }) => {
  const projectDirectory = directory;

  return {
    "chat.params": async (input) => {
      const sessionID = sessionIDOf(input);
      if (!sessionID) return;

      sessionRoles.set(
        sessionID,
        input.agent === "drafting-table" ? "drafting-table" : "other",
      );
    },

    "tool.execute.before": async (input, output) => {
      const sessionID = sessionIDOf(input);
      if (!sessionID || typeof input.tool !== "string" || !output?.args) {
        throw new Error("drafting-table-guard: OpenCode hook context is incomplete");
      }
      if (typeof projectDirectory !== "string" || projectDirectory.length === 0) {
        throw new Error("drafting-table-guard: project directory is unavailable");
      }

      const role = sessionRoles.get(sessionID) ?? "other";
      const payload = guardInput({
        sessionID,
        cwd: projectDirectory,
        // Keep OpenCode's effective tool IDs, including `bash` and `apply_patch`.
        toolName: input.tool,
        toolInput: output.args,
      });

      await invokeGuard(payload, role, projectDirectory);
    },
  };
};
