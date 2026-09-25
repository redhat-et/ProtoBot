# ProtoBot: Codex Harness Binding

> Design document — September 2026
>
> The third harness binding of the
> [Agent Harness Adapter Contract](adapter-contract.md): the Codex files,
> how they are meant to meet each obligation, and the Codex behaviors
> they rely on. Designed against Codex CLI 0.154.0; the fixture has not
> run on it.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Binding files](#binding-files)
- [Skill discovery and invocation](#skill-discovery-and-invocation)
- [Obligation status](#obligation-status)
- [Codex behaviors](#codex-behaviors)
- [Running the fixture on Codex](#running-the-fixture-on-codex)
- [Open points](#open-points)
- [Related Documents](#related-documents)

---

## Purpose and scope

This document binds the
[Agent Harness Adapter Contract](adapter-contract.md) to the Codex CLI,
so the Drafting Table runs there with the same Toolkit, the same guard,
and the same fixture as in OpenCode and Claude Code. It adds no rule of
its own. Where Codex forces a choice, this document states the choice
and the obligation it serves.

The fixture has not run here yet. The behaviors marked "observed" in
[Codex behaviors](#codex-behaviors) were checked on 2026-09-16 with
`codex debug prompt-input`, which renders the model's prompt without a
model, and with headless runs against a local stub model that recorded
every request. The behaviors marked "documented" come from the Codex
documentation, the CLI help, and the source of version 0.154.0. The
[fixture](#running-the-fixture-on-codex) turns each "designed" status
into "met" or into a recorded gap, and [Open points](#open-points) lists
what it must confirm first.

Codex differs from the other two harnesses in four ways that shape
this binding:

- **It has no agent file for the main session.** Codex agent roles
  apply to spawned subagents only. The role is a configuration profile,
  and Codex reads a profile only from `$CODEX_HOME`.
- **It has no file-reading tool and no skill tool.** The model reads a
  file, a `SKILL.md` included, through its shell tool. The guard
  therefore counts a small set of read-only command forms as reads
  ([Read forms](#read-forms)).
- **Its hooks fail open.** A hook that is not trusted does not run, and
  a hook that exits with any status other than 2, prints nothing, or
  times out lets the call through. A launcher makes the hook run, the
  sandbox stays on, and the rest is recorded.
- **Its sandbox keeps `.git/` read-only and has no network.** The role
  cannot write Git or reach the Git host from its shell. The `scm`
  server of the [Source Control Manager](../source-control-manager.md)
  does those steps. Codex documents that it runs an MCP server as its
  own process, outside the tool sandbox; the fixture must still confirm
  it ([Open points](#open-points), point 8). The user runs the two shell
  steps that remain
  ([What the user runs in Codex](#what-the-user-runs-in-codex)). The
  user therefore no longer types the push and the pull request by hand.
  That is a deliberate trade, with a recorded risk: the session skill
  tells the role to publish only on the user's explicit request, but
  no mechanism enforces it, because the role profile approves the
  `scm` tools without a prompt. A model steered by injected text can
  publish a proposed change set. It can publish only what the SCM's
  rules allow, and a reviewer sees it as a pull request before
  anything merges.

---

## Binding files

### Installed files

The binding is three files. At project scope they sit beside the shared
layer:

```text
<project root>/
├── .codex/
│   ├── hooks.json                       the hook that calls the guard, every session
│   ├── drafting-table.config.toml       the role profile, linked into $CODEX_HOME
│   └── drafting-table                   the launcher: the drafting-table entry point
└── .agents/                             shared layer (harness-neutral)
    ├── drafting-table.yaml
    └── skills/
```

Codex reads `.agents/skills/` natively, so no skill link exists.

Codex loads a project's `.codex/` files only when the user trusts the
project. `--profile <name>` reads `$CODEX_HOME/<name>.config.toml` and
nothing else: a profile file inside the project is ignored, and a
missing profile raises no error (observed). The install step therefore
links `$CODEX_HOME/drafting-table.config.toml`, by default
`~/.codex/drafting-table.config.toml`, to the project's file. The
profile names no project path, so one link serves every project that
uses adapter layout 1, and the fixture checks that the linked file
equals the project's.

At user scope, `hooks.json` goes to `$CODEX_HOME/hooks.json`, the
profile to `$CODEX_HOME/drafting-table.config.toml`, and the launcher
onto `PATH`.

The role is started by the launcher, not selected inside a session:

```text
.codex/drafting-table "<intent>"
```

Codex has no documented way to switch a running session to another
profile. A user who is already in a session exits and resumes it
through the launcher, as [Invocation](#invocation) shows.

### `.codex/hooks.json`

Project hooks apply to every Codex session in the project, whatever its
profile:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "sh -c 'command -v drafting-table-guard >/dev/null 2>&1 || { echo \"drafting-table-guard: not on PATH\" >&2; exit 2; }; exec drafting-table-guard --harness codex --role \"${PROTOBOT_ROLE:-other}\"'"
          }
        ]
      }
    ]
  }
}
```

- **The hook attempts to call the guard before every tool call (H8).**
  Codex sends
  the `PreToolUse` input on standard input, in the shape the guard
  expects: `hook_event_name`, `session_id`, `cwd`, `tool_name`, and
  `tool_input`, plus `model`, `permission_mode`, `tool_use_id`,
  `transcript_path`, and `turn_id`. For a shell command, `tool_name` is
  `Bash` and the command is `tool_input.command` (observed). A file
  patch arrives as `apply_patch` and an MCP call as
  `mcp__<server>__<tool>` (documented).
- **Exit status 2 with a line on standard error blocks the call**, and
  the model gets "Command blocked by PreToolUse hook:" followed by that
  line (observed). The guard always writes the line, and the command
  exits 2 itself when the guard is missing, so a missing executable does
  not let calls through.
- **`PROTOBOT_ROLE` is the role signal.** The hook input names no
  profile. A hook command inherits the environment Codex was started
  with (observed), so the launcher sets the variable and the command
  passes it to the guard. A launch without it runs as `other`, where
  the guard refuses every governed operation. The converse holds too:
  a variable exported in the user's own shell makes a plain `codex`
  session the role for the guard, without the profile's tool and
  skill rules. That is the user's own configuration, and the fixture
  checks the `other` case only.
- **An untrusted hook does not run.** Project trust is not enough:
  Codex keeps a trust hash for each hook in the user configuration,
  under `hooks.state`, and a changed hook needs trust again. The user
  reviews and trusts the hook once with `/hooks`. In a trusted project
  with an untrusted `hooks.json`, a command that the hook refuses ran
  without any diagnostic (observed). The [launcher](#the-launcher)
  does not depend on that step: it checks the file and starts Codex
  with `--dangerously-bypass-hook-trust`, which runs the hook without
  the trust store (observed). Other sessions in the project still need
  the `/hooks` step, and there the hook is the optional early layer.
- **A crash, an exit other than 2, or a timeout lets the call through**
  (documented). The guard exits 2 on any internal error it catches, as
  the contract requires. A guard process that is killed, or that hangs
  past the hook timeout, is a gap the native layer cannot close. The
  [sandbox](#the-role-profile) bounds what that one call can write,
  not what it can read. In particular, a passed `ears-manager` call can
  consume a non-`-` `--content-file` or `--impact-file` value: the sandbox
  does not establish the provenance of those bytes, and neither do the
  later integrity and CI checks
  ([File-source arguments](adapter-contract.md#file-source-arguments)).
- Every matching hook of every configuration layer runs, so a user's
  own hooks run beside this one and cannot replace it.

### The launcher

`.codex/drafting-table` is the `drafting-table` entry point (H3). It
is the check that the hook runs, made outside the model loop:

```sh
#!/bin/sh
# ProtoBot Drafting Table entry point for Codex.
set -eu
root=$(git rev-parse --show-toplevel)
hook="$root/.codex/hooks.json"
[ -f "$hook" ] || hook="${CODEX_HOME:-$HOME/.codex}/hooks.json"
profile="${CODEX_HOME:-$HOME/.codex}/drafting-table.config.toml"
expected_hook=$(cat <<'HOOK'
<the hooks.json above, byte for byte>
HOOK
)
expected_profile=$(cat <<'PROFILE'
<the role profile below, byte for byte>
PROFILE
)
command -v drafting-table-guard >/dev/null 2>&1 ||
  { echo "drafting-table-guard: not on PATH" >&2; exit 2; }
[ "$(cat "$hook")" = "$expected_hook" ] ||
  { echo "$hook is not the ProtoBot guard hook" >&2; exit 2; }
[ "$(cat "$profile")" = "$expected_profile" ] ||
  { echo "$profile is not the ProtoBot role profile" >&2; exit 2; }
for arg in "$@"; do
  case "$arg" in
    exec|resume|--last|--json|--strict-config) ;;
    -*) echo "$arg: not an option the launcher passes" >&2; exit 2 ;;
  esac
done
PROTOBOT_ROLE=drafting-table exec codex --profile drafting-table \
  --dangerously-bypass-hook-trust "$@"
```

- **It vets the hook and the profile, then bypasses the trust store.**
  Codex documents `--dangerously-bypass-hook-trust` for automation
  that has vetted its hook sources, and the checks are that vetting.
  With the flag, the hook runs whether or not the user trusted it
  (observed behavior 7), so the untrusted-hook and changed-hook cases
  are closed before Codex starts. A hook file or a profile that
  differs from the binding's by one byte stops the launcher, so
  `sandbox_mode`, the network setting, and the `wms` command cannot
  change unnoticed.
- **Only these arguments pass through.** `.codex/drafting-table
  "<intent>"` starts a session, `.codex/drafting-table resume --last`
  continues one, and `.codex/drafting-table exec --json "<intent>"` is
  the headless form the fixture uses. Any other option is refused, so
  a `-c sandbox_mode=...`, `--sandbox`, or
  `--dangerously-bypass-approvals-and-sandbox` cannot ride through and
  outrank the profile. The profile pins `network_access = false`
  itself, so a user configuration that turns it on does not reach the
  role.
- **A session started by hand has only the probe.** `codex --profile
  drafting-table` without the launcher runs with the trust store, and
  the probe in the profile is then the only check that the hook fired
  ([The role profile](#the-role-profile)).
- The launcher holds no rule and reads no project file besides the
  hook and the linked profile it compares.

### The role profile

`.codex/drafting-table.config.toml` holds the role's native
configuration:

```toml
developer_instructions = """
You are the ProtoBot Drafting Table, running in Codex with the Codex
binding of adapter layout 1. Begin every start summary with the line
"Adapter: ProtoBot adapter layout 1, Codex binding."

Before anything else, run the shell command `true`. The ProtoBot guard
refuses it, in an initialized project and before initialization alike,
and that refusal shows that the guard hook is running. If `true` runs
instead, stop: the guard hook is not running. Tell the user to start
the session through the launcher, .codex/drafting-table, and do
nothing else in this session.

In the EM-04 first release, `ears-manager change-set create` writes the
manifest but does not create or check out a branch. Do not report its
success as branch creation or treat it as the target branch-start step;
branch creation is deferred to follow-on Git integration (see the
[`ears-manager` CLI first-release
scope](../ears-manager-cli.md#em-04-first-release-scope)). Run it only
when an appropriate change-set branch already exists; otherwise stop and
explain that branch-start integration is deferred. Registration needs the
network and is refused in this sandbox. When the session
protocol reaches registration, print its exact command as the shell
operations show it, ask the user to run it in their own shell, and read
the state again through `repo_state` and `ears-manager` before continuing.

Your skills are these Toolkit skills and no others. Open a skill by
reading its SKILL.md under .agents/skills/, and read its references the
same way:
- drafting-specifications: the session protocol. Open it first and
  follow it.
- eliciting-requirements: EARS elicitation, when the session protocol
  asks for it.

Toolkit skills name operations. In Codex:
- an `ears-manager` operation is one shell command,
  `ears-manager --output json <command> ...`; artifact content and
  the impact file go on standard input;
- a WMS operation is the tool `mcp__wms__<normalized-operation>`;
- a Git or Git host operation is the SCM tool `mcp__scm__<operation>`;
  and
- a file is read with one of the read forms of the Codex binding.
"""
approval_policy = "never"
sandbox_mode = "workspace-write"
web_search = "disabled"

[sandbox_workspace_write]
network_access = false

[features]
multi_agent = false

[skills]
include_instructions = false

[mcp_servers.wms]
command = "<WMS Adapter MCP server, named by #31>"
default_tools_approval_mode = "approve"

[mcp_servers.scm]
command = "source-control-manager"
args = ["serve", "--face", "drafting-table"]
default_tools_approval_mode = "approve"

[analytics]
enabled = false

[feedback]
enabled = false
```

`--strict-config` accepted every key of this profile on 2026-09-16
(observed), before `[sandbox_workspace_write]` was added; the fixture
checks the profile as it stands.

- **`developer_instructions` is the entry point's prompt (H3).** It
  holds Codex facts only: the binding and layout line, the Toolkit
  skills and where to open them, and the tool-name mapping. The adapter
  line is spoken in the first turn, so the session record holds it
  ([Traces](adapter-contract.md#traces)). The two skill names are the
  manifest's `toolkit_skills`, and the fixture checks that they match.
- **The probe is the second layer (H8).** `true` is not a shell
  operation, so a running guard refuses it, and the refusal reaches
  the model. If the hook is not running, `true` runs, changes nothing,
  and the role stops before any governed step. The
  [launcher](#the-launcher) is the check outside the model loop; the
  probe is what a session started by hand still has. Neither sees a
  per-call crash or timeout, which H8 records.
- **`[skills] include_instructions = false` hides every skill (H10).**
  See [Skill visibility](#skill-visibility).
- **`approval_policy = "never"` (H7).** A command that needs approval is
  rejected and the failure returns to the model, so the role behaves the
  same headless and interactive. It refuses; it never approves. A
  command that would need to leave the sandbox is refused, not
  escalated.
- **`sandbox_mode = "workspace-write"`, network off.** The role's
  shell can write inside the working tree and `$TMPDIR`, which is what
  `ears-manager` needs, and nothing else. Codex keeps `.git/`,
  `.agents/`, and `.codex/` read-only in that mode and, with
  `network_access` off, gives the shell no network (documented). The
  EM-04 `change-set create` command writes only the manifest, so it is not
  refused on `.git/` write grounds and does not create or check out a
  branch. The target branch-cutting behavior would write `.git/` and be
  refused if run as a shell command in this sandbox; it remains follow-on
  scope (see the [`ears-manager` CLI first-release
  scope](../ears-manager-cli.md#em-04-first-release-scope)).
  `register-approved-change-set` still needs network, so the user runs it
  ([What the user runs in Codex](#what-the-user-runs-in-codex)). The
  supported EM-04 `ears-manager` commands and the `wms` and `scm` tools
  otherwise work.
- **`web_search = "disabled"` and `multi_agent = false` hide the web and
  subagent tools (H9).** With them, a model without an `apply_patch`
  tool type gets `exec_command`, `write_stdin`, `request_user_input`,
  and `view_image` (observed), plus the `wms` tools. Codex's hosted web
  search is not seen by hooks, so hiding it is the only control.
- **`apply_patch` cannot be hidden.** Codex offers it when the model's
  metadata names an `apply_patch` tool type, which OpenAI models do, and
  no setting removes it (documented). The guard refuses it in the role
  (guard rule 5), and H9 records the gap.
- **`[mcp_servers.wms]` and `[mcp_servers.scm]` exist only in the
  profile (H2).** Other sessions never load the servers.
  `default_tools_approval_mode` lets their tools run under
  `approval_policy = "never"`; it approves every tool of a server, and
  the guard allows only the tools the manifest lists. An MCP server
  from the user's base configuration still loads in the role, and guard
  rule 4 refuses its tools. Codex starts each server as its own
  process, not as a tool call, so the tool sandbox does not apply to
  it; the fixture must confirm that `wms` reaches the WMS backend, and
  that `scm` writes `.git/` and reaches the Git host, with
  `network_access` off ([Open points](#open-points)). The `scm` server
  is the Drafting Table face of the
  [Source Control Manager](../source-control-manager.md):
  it is where the role's Git and Git host operations run, bounded by
  that face instead of by the sandbox. In multi-player mode the `wms`
  entry becomes a streamable HTTP server with `url`, and the user
  authenticates once with `codex mcp login wms`, which keeps the token
  in Codex's own store outside the project (H13, unverified). The `scm`
  server stays a local process in every mode. The Web Drafting Table
  uses no harness binding
  ([Deployment modes](adapter-contract.md#deployment-modes)).
- **`[analytics]` and `[feedback]` are off (H11).** Analytics is on by
  default in `codex exec`, and feedback can upload a session. Codex
  Cloud and remote control run only when the user starts them.
- The profile holds no credential and no project path.

### What the user runs in Codex

Registration is the user-run shell operation in the EM-04 first release
because it needs network access. Branch creation is not implemented by
that release's CLI, so there is no branch-start command for the role to
delegate. When the user runs an available command, the role prints it
exactly as the [shell operations](adapter-contract.md#shell-operations)
show it, with placeholders filled, and reads the state again before it
continues, as it does for a discard and a merge:

| Step | Commands the user runs |
| --- | --- |
| Start a change set | Not available as an end-to-end EM-04 step: `change-set create` writes the manifest only and does not create or check out the branch; see the [`ears-manager` CLI first-release scope](../ears-manager-cli.md#em-04-first-release-scope) |
| Register after the merge | `register-approved-change-set --change-set CS-<nnnnn>` |

The role runs the supported EM-04 `ears-manager` commands, including
metadata-only `change-set create` when an appropriate branch is already
established, and every `wms` and `scm` tool available to the binding.
Neither the CLI call nor the Codex sandbox starts a new change-set
branch. A session started in OpenCode or Claude Code and resumed in
Codex, or the other way round, finds the same state; only the
network-dependent registration step is user-run in this release.

### Read forms

The contract's role may read project files. Codex offers no read tool
besides `view_image`, so the guard's Codex vocabulary row lists the
read-only shell forms that count as reads. Each is one simple command:

| Read | Command forms |
| --- | --- |
| Show a file or a part of it | `cat <path> ...`, `sed -n '<a>,<b>p' <path>`, `head -n <n> <path>`, `tail -n <n> <path>`, `nl -ba <path>` |
| Count lines | `wc -l <path> ...` |
| Read the clock | `date -u +%Y-%m-%dT%H:%M:%SZ`, the value of `--created` |
| List a directory | `ls <path> ...`, `ls -la <path> ...`, `rg --files <path> ...` |
| Search | `rg -n -e <pattern> -- <path> ...` |

Every `<path>` is named explicitly, does not start with `-`, and is
checked after symlink resolution. `<a>`, `<b>`, and `<n>` are unsigned
integers. `<pattern>` follows `-e` and is one shell word, so it cannot
be parsed as an option; a pattern in any other position is refused. The
guard refuses a read form under `.protobot/` other than
`project.yaml`, under `.git/`, of a credential file, or outside the
project except a user-scope Toolkit skill root, and
refuses a read of `<skill root>/<name>/SKILL.md` or of a file below it
when `<name>` is not in `toolkit_skills` (guard rule 5). A search or
listing must name a path, and a search of the whole project root, or
of a directory that holds a denied path anywhere below it, is refused,
as for every harness's search tool.

### No execpolicy rules

Codex prefix rules in `.codex/rules/` load for every session in a
trusted project (observed), and the strictest matching decision wins.
A rule matches a command from its first word only, a command that no
rule matches is allowed, and an `allow` rule runs its command outside
the sandbox (documented). A native copy of the shell operations in
those rules would either be too weak to refuse an option after the
prefix, or would widen every other session. The binding adds no rules
file. The guard carries the shell operations (H8), as it does for
options inside a command in the Claude Code binding.

---

## Skill discovery and invocation

### Discovery

Codex discovers skills from `.agents/skills/` in the working directory
and every parent up to the project root, the first directory with
`.git`; from `.codex/skills/` in a trusted project; from
`~/.agents/skills/` and `$CODEX_HOME/skills/` for the user; from its
bundled system skills in `$CODEX_HOME/skills/.system`; from
`/etc/codex/skills/`; and from enabled plugins (H1, documented). A run
from the ProtoBot worktree listed the five bundled skills and every
skill in `.agents/skills/` (observed). Codex lists both entries when
one name exists twice, which is one reason skill rule 3 of the contract
demands unique names.

### Skill visibility

Discovery is not permission. Codex lists skills for the model in a
developer message, `<skills_instructions>`, with a name, a description,
and a path for each, and the model opens the `SKILL.md` itself.
`codex debug prompt-input` on 0.154.0, on 2026-09-16, showed:

| Setting | Skills in the model's prompt |
| --- | --- |
| None | Every discovered skill, bundled ones included |
| `[[skills.config]]` with `name` and `enabled = false` | That skill left out |
| `[[skills.config]]` with the path of its `SKILL.md` and `enabled = false` | That skill left out |
| `[[skills.config]]` with the path of the skill directory, or `name = "*"` | No effect |
| `[skills.bundled]` with `enabled = false` | Every bundled skill left out |
| `[skills] include_instructions = false` | No `<skills_instructions>` message at all |

`[[skills.config]]` works only by exact name or file path, like
`skillOverrides` in Claude Code, and Codex reads it only from user
layers: the user configuration, a profile, and `-c`, never a project's
`.codex/config.toml` (documented). A skill that the binding cannot name
in advance, in the user's own skill directories or in a plugin, would
stay listed.

The profile therefore turns the catalog off with
`include_instructions = false`, and its `developer_instructions` name
the two Toolkit skills and where to open them. The model's prompt then
names exactly the manifest's `toolkit_skills`, whatever else Codex
discovers. Unlike the Claude Code binding, no skill stays visible.

A hidden skill's files are still on disk, and Codex has no skill tool
to refuse a load. The guard refuses a read form on a `SKILL.md` outside
`toolkit_skills` ([Read forms](#read-forms)), so the model cannot load
another skill in the role. Codex also inserts a skill into the prompt
when the prompt text mentions `$<name>` (documented), and the guard
does not see that route, because it is not a tool call. Whether pasted
text, such as IdeaBot material, triggers it too is not observed. The
route changes what the model reads, not what it can do: the guard and
the profile bound every effect of a guard-checked call
([Untrusted input](adapter-contract.md#untrusted-input)). H10 records
the gap, and the fixture confirms the reach of `$<name>` before H10 is
marked met ([Open points](#open-points)).

### Invocation

| Entry point | Effect |
| --- | --- |
| `.codex/drafting-table "<intent>"` | A new session in the role |
| `.codex/drafting-table resume --last` | The most recent session continues in the role; the resume steps run again |
| `.codex/drafting-table resume <id>` | The named session continues in the role |
| `.codex/drafting-table exec --json "<intent>"` | A headless session; the fixture uses it |

The launcher sets `PROTOBOT_ROLE` and the profile. A session without
the profile has no `wms` tools, and one without `PROTOBOT_ROLE` is
`other` to the guard, which refuses every governed operation. A
session started with `codex --profile drafting-table` by hand runs
without the launcher's hook check ([The launcher](#the-launcher)).

### The first consumer in Codex

The profile names `eliciting-requirements`, and the model opens its
`SKILL.md` and `references/` with read forms. The skill needs no binding
file.

---

## Obligation status

| # | Obligation | Codex binding | Status |
| --- | --- | --- | --- |
| H1 | Discover Toolkit skills from `.agents/skills/` | Native discovery | Observed from the project root; the fixture has not run |
| H2 | The `ears-manager` CLI and the `wms` and `scm` tools for the role | `exec_command`; `[mcp_servers.wms]` and `[mcp_servers.scm]` in the profile | Designed |
| H3 | `drafting-table` entry point | The launcher, which starts the profile with `--profile` and `PROTOBOT_ROLE` | Observed that the profile loads and its instructions reach the model; a missing profile raises no error; the launcher is designed |
| H4 | Resume on every entry, continued session, and compaction | `codex resume` and `codex exec resume` with the profile; the session skill runs the resume steps | Designed |
| H5 | Nothing on idle or exit | No `Stop` or `SessionEnd` hook | Designed |
| H6 | Replayable session record | The session file under `$CODEX_HOME/sessions/` and the `codex exec --json` event stream | Designed; hook events are not recorded, and a refusal is recorded as the tool output |
| H7 | Headless replay with no permission prompt | `codex exec --json`, `approval_policy = "never"`, and a custom model provider pointed at a replay endpoint | Observed with a stub endpoint; the fixture has not run |
| H8 | Invoke the guard on each tool call and enforce its decision | The project hook; the launcher, which checks the hook file and the profile and starts Codex with `--dangerously-bypass-hook-trust`; the probe as the second layer | Observed for shell commands. The launcher closes the untrusted-hook and changed-hook cases and pins the profile; outside it, an untrusted hook does not run. A killed guard, non-2 exit, or hook timeout lets that call through (documented gaps). The sandbox bounds what a call can write, not what it can read: a host credential file can reach the model, and the `wms` and `scm` servers have network. Later checks catch unauthorized persistent edits to registered paths, but cannot establish the provenance of a passed file-source read ([File-source arguments](adapter-contract.md#file-source-arguments)) |
| H9 | Hide file-writing, subagent, and web tools | `web_search = "disabled"`, `multi_agent = false` | Observed for web and subagent tools; `apply_patch` cannot be hidden and is refused by the guard (gap) |
| H10 | Toolkit skills only | `include_instructions = false`; the profile names the Toolkit skills; the guard refuses any other `SKILL.md` read | Observed that the catalog is gone; `$<name>` in the prompt text can still insert another skill's text, and the fixture has not run (gap) |
| H11 | No credential in binding files; no session upload | Placeholders; `[analytics]` and `[feedback]` off; no `codex cloud` or `remote-control` | Designed |
| H12 | Publish this status | This table | Met |
| H13 | Remote `wms` server with OAuth 2.1 | `[mcp_servers.wms]` with `url` and `oauth`, and `codex mcp login wms` | Candidate; not checked |

"Designed" means the mechanism is documented and the fixture has not
run. "Observed" means a stub run or `codex debug prompt-input` showed
it. Nothing else is marked met.

One limitation sits outside the obligations: EM-04 `change-set create`
does not cut a branch; that behavior is deferred. The sandbox keeps
`.git/` read-only, so a future shell-based branch cut would be refused,
and the role's shell has no network, so registration is the user's
([What the user runs in Codex](#what-the-user-runs-in-codex)). Git and
the Git host reach the role through the `scm` server, outside the
sandbox.

What the Codex layer stops on the normal active-hook path, by route.
Sandbox restrictions still apply if the hook fails open, but guard-only
refusals below do not:

| Write route to a guarded path | Role profile | Every other session |
| --- | --- | --- |
| Any write under `.git/`, and any network from the shell | Refused by the sandbox; the `scm` server writes `.git/` from its own process, bounded by its Drafting Table face | Refused by the sandbox in a `workspace-write` session |
| `apply_patch` under `.protobot/` or on a registered path | Offered to OpenAI models; refused by the active guard | Refused by the active guard |
| Shell writer, such as `sed -i` | Refused by the active guard | Not stopped |
| Output redirection in a shell command | Refused by the active guard | Refused by the active guard when the redirection target is written from the project root |
| Tool of another MCP server | Refused by the active guard | The user's own configuration |
| Subagent | Not offered | Not applicable |

The native layer stops less than in the other two bindings: no Codex
rule refuses a command for the role alone, and no setting hides
`apply_patch`. An active guard carries the difference; on a fail-open
call, native rules do not replace its shell and argument checks. The
later layers catch unauthorized persistent edits to registered paths
when those edits reach the SCM or CI checks, not reads or file-source
provenance ([What the harness layer stops][layer-stops]). The sandbox
stops more: no command in the role writes `.git/` or reaches the
network.

---

## Codex behaviors

The binding relies on these behaviors of Codex CLI 0.154.0.

**Observed on 2026-09-16:**

1. `codex debug prompt-input` renders the developer message
   `<skills_instructions>` without a model, with the bundled skills
   from `$CODEX_HOME/skills/.system` and the skills in the project's
   `.agents/skills/`.
2. `[[skills.config]]` with `enabled = false` hides a skill named by
   `name` or by the path of its `SKILL.md`; a directory path and
   `name = "*"` have no effect; `[skills.bundled]` with
   `enabled = false` hides every bundled skill; `[skills]` with
   `include_instructions = false` removes the whole message.
3. A request to a custom model provider with `wire_api = "responses"`
   carries the tools `exec_command`, `write_stdin`,
   `request_user_input`, `view_image`, `multi_agent_v1`, and
   `web_search`, and no `apply_patch` for a model slug Codex does not
   know. `web_search = "disabled"` removes `web_search`, and
   `features.multi_agent = false` removes `multi_agent_v1`.
4. `--profile <name>` layers `$CODEX_HOME/<name>.config.toml`, and its
   `developer_instructions` reach the model. A profile file in the
   project's `.codex/` is ignored, and a missing profile raises no
   error. `--strict-config` rejects an unknown key, such as
   `tools.view_image`, and accepted the role profile as it stood then,
   without `[sandbox_workspace_write]`; the binding check covers the
   profile as it stands.
5. A trusted project's `.codex/config.toml` and `.codex/rules/*.rules`
   load, and a `forbidden` prefix rule rejects its command.
6. A `PreToolUse` command hook receives a shell call as `tool_name`
   `Bash` with `tool_input.command`. Exit status 2 blocks the call and
   returns the hook's standard error to the model. The hook runs before
   the prefix rules. A variable set at launch reaches the hook command.
7. A project `.codex/hooks.json` that is not trusted does not run, even
   in a trusted project; `--dangerously-bypass-hook-trust` runs it.

**Documented:**

1. Skill discovery locations are the ones in [Discovery](#discovery);
   `[[skills.config]]` is read from user layers only; a `$<name>`
   mention inserts a skill into the prompt.
2. Hook trust is a hash per hook under `hooks.state` in the user
   configuration, reviewed with `/hooks`; a changed hook needs trust
   again. A hook that crashes, exits with a status other than 2, writes
   nothing to standard error, or times out lets the call through. Hook
   events are not written to the `--json` stream or the session file.
3. `apply_patch` reaches hooks as `apply_patch`, and an MCP tool as
   `mcp__<server>__<tool>`; hosted web search does not reach hooks.
4. `workspace-write` keeps `.git`, `.agents`, and `.codex` read-only
   and, with `network_access` off, gives a command no network;
   `approval_policy = "never"` rejects a command that needs approval.
5. A prefix rule matches from the first word; the strictest decision
   wins; an unmatched command is allowed; an `allow` rule runs the
   command outside the sandbox.
6. `codex resume` and `codex exec resume` continue a session. The
   session file is `$CODEX_HOME/sessions/<year>/<month>/<day>/rollout-*.jsonl`
   and records the developer instructions, every tool call, and every
   tool output.
7. `[mcp_servers.<name>]` registers a stdio server with `command` or a
   streamable HTTP server with `url`, and `codex mcp login <name>` runs
   its OAuth flow. `wire_api = "responses"` is the only model provider
   wire protocol.
8. Analytics is on by default in `codex exec`, and `/feedback` can
   upload a session; `[analytics]` and `[feedback]` with
   `enabled = false` turn them off.

An upgrade of Codex runs the fixture before it is used. A behavior
that changes is fixed in this binding, never in a Toolkit or
adapter-core file.

---

## Running the fixture on Codex

The [fixture session](adapter-contract.md#fixture-session) needs these
harness commands. For Codex:

| Fixture need | Codex |
| --- | --- |
| List discovered skills (step 1) | `codex debug prompt-input` from a subdirectory of the clone, without the profile; the names in `<skills_instructions>` |
| Headless turn in the role (steps 2 to 14) | `.codex/drafting-table exec --strict-config --json "<intent>"`; `.codex/drafting-table exec resume --last` or `resume <id>` to continue |
| Headless turn outside the role (steps 2, 7, 9) | `codex exec --json "<prompt>"` |
| Out-of-role setup and refused commands | Setup prepares the EM-04 `change-set create` manifest; that command does not write `.git/` and is not refused by the sandbox. Step 9 runs `git checkout -- docs/vision.md` outside the session after the role or out-of-role turn names it. Step 14 runs through the `scm` tools, and the `gh` stub records the call. The out-of-role run uses `workspace-write` too, so `echo x >> vision.md` succeeds inside the workspace |
| Resolved native rules | No command prints them; the fixture keeps the profile and `hooks.json` next to the export |
| Session export (step 15) | The session file under the fixture's `$CODEX_HOME/sessions/` and the `--json` event stream |

**Replayed model.** The fixture's own `$CODEX_HOME/config.toml` selects
a custom model provider with `model_provider`, defined as
`[model_providers.replay]` with `base_url` at a local endpoint and
`wire_api = "responses"`. Provider settings are not read from a
project's configuration. The endpoint speaks the Responses API and
answers with the next recorded turn. For a model slug Codex does not
know, Codex uses fallback metadata without `apply_patch`, so the
fixture also supplies model metadata that offers `apply_patch`, and
step 6 includes an `apply_patch` call. The same `config.toml` marks the
clone as trusted and holds the hook's trust entry.

**Binding checks** added to the harness-neutral ones:

- `--strict-config` accepts the profile. The launcher refuses to
  start when the hook file or the linked profile differs from the
  binding's by one byte.
- The first recorded request in the role carries the adapter line in
  its developer instructions, which shows that the profile loaded.
- That request has no `<skills_instructions>` message, and its
  developer instructions name exactly the manifest's `toolkit_skills`.
- Its tools are `exec_command`, `write_stdin`, `request_user_input`,
  `view_image`, `apply_patch`, and the `wms` and `scm` tools, and no
  other.
- The launcher refuses to start when `hooks.json` differs from the
  binding's by one byte, and the first tool call of every run in the
  role is the probe `true`, refused, which shows that the hook runs.
- In step 14, the `scm` tools commit, refresh, and publish from outside
  the sandbox with no prompt, and the `gh` stub records `pr create`.
- The `wms` and `scm` tools answer in step 2 with `network_access` off.

---

## Open points

The fixture must confirm these before any other obligation is marked
met:

1. Whether `--dangerously-bypass-hook-trust` runs a never-trusted
   project hook in the TUI as it does in `codex exec` (observed
   behavior 7).
2. Whether `PreToolUse` fires for `write_stdin`, `view_image`, and
   `request_user_input`, and which `tool_name` each carries.
3. Whether the hook command runs through a shell, so that the command
   in `hooks.json` works as written.
4. Whether `default_tools_approval_mode = "approve"` lets the `wms`
   and `scm` tools run headless under `approval_policy = "never"`.
5. Whether a resumed session keeps the profile's tools, instructions,
   and hidden skill catalog.
6. Whether the model, without a skill catalog, opens the Toolkit skills
   reliably from the profile's instructions. The fixture's replayed
   turns cannot show this; the skill evaluation (#63) can.
7. Whether `$<name>` is matched in pasted prompt text as well as in
   text the user types, and whether it still inserts a skill when
   `include_instructions = false`.
8. Whether the `wms` and `scm` MCP servers, which Codex starts as their
   own processes, run outside the tool sandbox, so that `wms` reaches
   the WMS backend and `scm` writes `.git/` and reaches the Git host,
   with `network_access` off. If `scm` does not, every Git write and
   host step is the user's again, through the
   [SCM's CLI](../source-control-manager.md#packaging) in their own
   shell.
9. Whether the guard's own Git read, `git rev-parse --abbrev-ref HEAD`,
   runs cleanly from the hook while the role's sandbox keeps `.git/`
   read-only; the fixture shows it.
10. Which MCP revision Codex 0.154.0 negotiates with the `scm` server:
    2026-07-28, or the legacy 2025-11-25. The server serves both
    ([MCP protocol](../source-control-manager.md#mcp-protocol)), so the
    answer changes nothing in the role; the fixture records it.

---

## Related Documents

- [Agent Harness Adapter Contract](adapter-contract.md) — The
  harness-neutral contract this binding implements.
- [OpenCode Harness Binding](opencode.md) — The first binding.
- [Claude Code Harness Binding](claude-code.md) — The second binding.
- [Vision](../../vision.md) — Purpose, intended users, desired
  outcomes, prototype scope, and non-goals.
- [Architecture](../../architecture.md) — The Drafting Table Boundary
  and the OpenCode-plus-skill strawman.
- [Overview](../overview.md) — Single-player and multi-player modes, and
  platform.
- [System Components](../components.md) — The Drafting Table and the
  Specification Toolkit.
- [`ears-manager` CLI Integration Contract](../ears-manager-cli.md) —
  The command grammar the role's shell commands follow.
- [Git and Project-Repository Integration](../git-integration.md) —
  Permitted Git operations and ungoverned-edit detection.
- [Source Control Manager](../source-control-manager.md) — The Git and
  Git host operations exposed through the `scm` MCP server.
- [Validation Rules](../validation-rules.md) — The WMS boundary that
  rejects a lifecycle transition from the Drafting Table.
- [Drafting Table WMS Integration](../drafting-table-wms.md) — The WMS
  operations exposed through the `wms` MCP server.
- [User Interaction Flow](../user-interaction-flow.md) — Phase
  details, sequence diagrams, and change types.
- [Drafting Table UX](../drafting-table-ux.md) — Stable interaction
  contract for the first local Drafting Table.
- [Open Design Questions](../open-questions.md) — Unresolved design
  questions across all areas.
- [Related Work](../related-work.md) — Internal and external
  projects informing the design.

[layer-stops]: adapter-contract.md#what-the-harness-layer-stops
