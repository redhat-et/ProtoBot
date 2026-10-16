# ProtoBot: OpenCode Harness Binding

> Design document — September 2026
>
> The first harness binding of the
> [Agent Harness Adapter Contract](adapter-contract.md): the OpenCode
> files, how they meet each obligation, and the OpenCode behaviors they
> rely on.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Binding files](#binding-files)
- [Skill discovery and invocation](#skill-discovery-and-invocation)
- [Obligation status](#obligation-status)
- [Observed OpenCode behaviors](#observed-opencode-behaviors)
- [Running the fixture on OpenCode](#running-the-fixture-on-opencode)
- [Related Documents](#related-documents)

---

## Purpose and scope

Issue #33 asks for the smallest useful Drafting Table adapter when the
user invokes ProtoBot inside an existing OpenCode session. The
harness-neutral half of the answer — the three layers, the manifest,
the Drafting Table role, the shell operations, the guard, session
behavior, traces, resumable state, exit conditions, and the fixture —
is in [Agent Harness Adapter Contract](adapter-contract.md).
This document is the OpenCode half.

It adds no rule of its own. Where OpenCode forces a choice, this
document states the choice and the obligation it serves. Every
OpenCode behavior named here was observed on OpenCode 1.18.30 with a
replayed model ([Observed OpenCode behaviors](#observed-opencode-behaviors)).
The fixture ran in #77 against the pinned OpenCode 1.18.30 binary, with
real skills, permissions, plugin, MCP clients, shell, `ears-manager`, the
Source Control Manager, and the guard, plus recording WMS and `gh` stubs.
The recorded fixture covers the baseline guard vectors. Guard unit tests
cover expanded Rule 4 and Rule 7 cases, but not all are represented in the
fixture; the statuses below record the fixture evidence and the gaps it left
open. See
[Running the fixture on OpenCode](#running-the-fixture-on-opencode).

---

## Binding files

### Installed files

The binding is four files. At project scope they sit beside the shared
layer:

```text
<project root>/
├── opencode.json                        rules for every OpenCode agent
├── .opencode/
│   ├── agents/drafting-table.md         the Drafting Table role
│   ├── commands/drafting-table.md       the drafting-table entry point
│   └── plugins/protobot-guard.js        the shim that calls the guard
└── .agents/                             shared layer (harness-neutral)
    ├── drafting-table.yaml
    └── skills/
```

At user scope the same four files go under `~/.config/opencode/`.
Project config is merged after user config, so a project that carries
the binding overrides a user-scope install. A user-scope install also
needs the `external_directory` rule in
[How the rules combine](#how-the-rules-combine).

### `opencode.json`

```json
{
  "$schema": "https://opencode.ai/config.json",
  "share": "disabled",
  "mcp": {
    "wms": {
      "type": "local",
      "command": ["<WMS Adapter MCP server, named by #31>"],
      "enabled": false
    },
    "scm": {
      "type": "local",
      "command": ["source-control-manager", "serve", "--face", "drafting-table"]
    }
  },
  "permission": {
    "apply_patch": "deny",
    "edit": {
      ".protobot/**": "deny"
    },
    "wms_*": "deny",
    "scm_*": "deny"
  }
}
```

- **`share` is `disabled`**, because OpenCode's share feature uploads a
  session (H11).
- **The `wms` and `scm` servers** are the manifest's MCP servers (H2).
  OpenCode names their tools `wms_<normalized-operation>` and
  `scm_<operation>`, where separators in the canonical operation name
  become underscores. The `wms` command value is a placeholder until #31
  names the server. The `scm` server is the Drafting Table face of the
  [Source Control Manager](../source-control-manager.md), which runs
  every Git and Git host operation of the role. Neither entry holds a
  credential. The placeholder `wms` command stays disabled until #31
  provides an executable server. `ears-manager` needs no entry: it is a
  shell operation.
- **`edit` is denied under `.protobot/` for every agent.** The `edit`
  rule covers every OpenCode file tool: `edit`, `write`, and the patch
  tool. It is the native `.protobot/` slice of guard rule 3 and holds
  even when plugins do not load; the other protected-path checks remain
  the guard's responsibility.
- **`apply_patch` is denied globally**, so file patches reach the guard
  only through direct hook fixtures; the native tool is not offered in
  the resolved OpenCode tool set.
- **The `wms` and `scm` tools are denied here and allowed only in the
  `drafting-table` agent**, a native copy of the MCP half of guard rule
  4. The `ears-manager` half has no native copy: the agent's `bash`
  rules allow it in the role, and the guard refuses it elsewhere. The
  agent's named allows match the normalized names in the manifest; the
  guard independently enforces the same list.

This file disables the unimplemented WMS placeholder and adds permission
denials, but no allow rules;
[How the rules combine](#how-the-rules-combine) explains why.

### The `drafting-table` agent

`.opencode/agents/drafting-table.md` gives the Drafting Table role to
one primary agent (H3):

```markdown
---
description: ProtoBot Drafting Table — Sketching and Dimensioning through governed tools
mode: primary
permission:
  "*": deny
  apply_patch: deny
  read:
    "*": allow
    "*.env": deny
    "*.env.*": deny
    ".git/**": deny
    ".protobot/**": deny
    ".protobot/project.yaml": allow
  glob: allow
  grep: allow
  question: allow
  todowrite: allow
  skill:
    "*": deny
    drafting-specifications: allow
    eliciting-requirements: allow
  wms_request_create: allow
  wms_request_refine: allow
  wms_request_link_change_set: allow
  wms_request_link_build_work_item: allow
  wms_request_get: allow
  wms_request_query: allow
  wms_work_item_get: allow
  wms_work_item_query: allow
  wms_blocked_work_query: allow
  wms_lifecycle_preflight: allow
  wms_blocked_work_submit_resolution: allow
  wms_blocked_work_acknowledge: allow
  scm_repo_state: allow
  scm_branch_init: allow
  scm_branch_resume: allow
  scm_commit: allow
  scm_publish: allow
  scm_refresh: allow
  bash:
    "*": deny
    # the native copy of the shell operations, below
---

You are the ProtoBot Drafting Table, running in OpenCode with the
OpenCode binding of adapter layout 1.

Load the drafting-specifications skill before anything else, and
follow it.

Toolkit skills name operations. In OpenCode:
- an `ears-manager` operation is one shell command,
  `ears-manager --output json <command> ...`; artifact content and
  the impact file go on standard input;
- a WMS operation is the tool `wms_<normalized-operation>`; and
- a Git or Git host operation is the SCM tool `scm_<operation>`.
```

- **`"*": deny` hides tools (H9).** OpenCode does not offer the model a
  built-in tool that has no allow rule, so the agent never sees `edit`,
  `write`, the patch tool, `task`, `webfetch`, or `websearch`. The same
  catch-all denies `doom_loop`, so an identical call repeated three
  times is refused rather than asked about.
- **The `read` denies are the native copy of the role's read denies.**
  They cover `.env` files, `.git/`, and `.protobot/`; the
  `project.yaml` allow follows the `.protobot/**` deny, so it wins for
  that one file, which the role may read. The guard refuses the other
  credential files that the contract names
  ([The Drafting Table role](adapter-contract.md#the-drafting-table-role)).
- **The `skill` rule is the manifest's `toolkit_skills` (H10).** Its
  `"*": deny` comes first, so every other skill is left out of the
  model's list and refused when called
  ([Skill visibility](#skill-visibility)). A new Toolkit skill adds one
  line here and one in the manifest; the fixture checks that the two
  lists match. OpenCode adds a read allow for each discovered skill's
  directory, so a `read` of another skill's `SKILL.md` passes the
  native rules; the guard refuses it as a load (guard rule 5). `glob`
  and `grep` are allowed natively; the guard treats them as reads of
  everything below their path.
- **No rule is `ask`.** In `opencode run`, a rule that resolves to `ask`
  is rejected automatically and the run ends, so an agent with only
  allow and deny rules behaves the same headless and in the TUI (H7).
- **The prompt holds OpenCode facts only:** the binding and layout, the
  skill to load, and the tool-name mapping. The refusal rule, the
  resume triggers, and the recording notice are in the session skill.

#### Native copy of the shell operations

The `bash` block copies the harness-neutral
[shell operations](adapter-contract.md#shell-operations) into OpenCode
patterns, with a wildcard where a form takes a value. Git and `gh` have
no rule, so `"*": deny` refuses every `git` and `gh` command; the role
reaches them only through the `scm` tools. The guard enforces the same
operations with the project's real values and the current branch. The
native copy refuses early for constraints its patterns can express;
the guard supplies the state and argument-value checks. OpenCode's
native rules do not implement a value-level restriction for
`--content-file` and `--impact-file`, so the shared guard must refuse
non-`-` values for these options.

```yaml
bash:
  "*": deny
  # Specification reads and writes
  "ears-manager *": allow
  # The clock, for --created
  "date -u +%Y-%m-%dT%H:%M:%SZ": allow
  # Registration, after the user merged
  "register-approved-change-set --change-set *": allow
```

A pattern without `*` matches only that exact command. A pattern with
`*` still matches a longer command, so the guard refuses every option
that #30's grammar does not show, and a registration whose change set
is not the current branch's. In particular, the native
`"ears-manager *": allow` rule must not be treated as permission to pass
non-`-` values to `--content-file` or `--impact-file`; the binding does
not implement a native value-level restriction for these options. The
binding also has no native fail-closed check for the plugin's presence.
If the plugin is absent or its hook is not registered for a call, the
`"ears-manager *": allow` rule remains effective: OpenCode may run
non-`-` file-source values, `--text "$GH_TOKEN"`, output redirection,
input redirection, and other shell forms that only the guard rejects.
The binding does not claim the rest of the shell path is protected in
that state; these are
recorded H8 gaps, not behavior supplied by native permissions or later
layers. See the shared
[file-source exception](adapter-contract.md#file-source-arguments).

OpenCode matches these patterns against the whole command text,
here-document bodies included, but does not look inside an output
redirection. `ears-manager --output json check > docs/vision.md`
matches `ears-manager *`, so the native copy alone would let that command
empty the file. The active guard refuses it; a call that reaches the
allowed command without a guard decision is not refused by the native
rule.

### The `drafting-table` command

`.opencode/commands/drafting-table.md` is the entry point (H3, H4):

```markdown
---
description: Start or resume a ProtoBot Drafting Table session
agent: drafting-table
---

Start or resume a Drafting Table session with the
drafting-specifications skill. The user's intent, if given: $ARGUMENTS

Adapter: ProtoBot adapter layout 1, OpenCode binding.
```

The last line puts the adapter layout and the binding into the session
record ([Traces](adapter-contract.md#traces)).

### The guard shim

`.opencode/plugins/protobot-guard.js` connects OpenCode to the shared
[guard](adapter-contract.md#the-guard) (H8). OpenCode has no
`PreToolUse` command hook, so the shim does the translation:

1. **Tracks the role.** OpenCode's `tool.execute.before` hook does not
   name the agent, but `chat.params` does. The shim records the agent of
   each session: `drafting-table` is the Drafting Table role, and every
   other agent is `other`. A tool call from a session the shim has not
   seen yet is also treated as `other`, which fails closed for
   `ears-manager` writes and governed tools but leaves rule 5's
   credential-read refusals to the native deny list; that unbound-session
   window is a recorded gap, because refusing every unbound call would
   also refuse tool calls from sessions the shim cannot attribute.
2. **Builds the guard input.** For each tool call it writes the
   `PreToolUse` JSON — `hook_event_name`, `session_id`, `cwd`,
   `tool_name`, and `tool_input` from the call's arguments — and runs
   `drafting-table-guard --harness opencode --role <role>`.
3. **Blocks on refusal.** On any non-zero status it throws an error
   whose message is the guard's line. OpenCode returns that message to
   the model as the tool result and keeps it in the session record.

The shim holds no rule, reads no project file, and does nothing when a
session is idle or ends (H5).

### How the rules combine

OpenCode resolves each agent's permissions into one ordered list, and
the last matching rule wins. The order is: OpenCode's defaults, the
built-in agent's own rules, the `permission` block of the config, then
the agent file's `permission` block. Three consequences shape the
binding:

| Trap | What happens | Rule in this binding |
| --- | --- | --- |
| A catch-all allow in `opencode.json` | It follows the built-in `plan` agent's `edit` deny, and gives `plan` write access | `opencode.json` adds denies only |
| A catch-all allow in an agent file | It follows OpenCode's default `ask` for `.env` files, and lets the agent read them | The agent denies `*.env` and `*.env.*` again |
| A catch-all deny in an agent file | It follows the allow that OpenCode adds for each discovered skill's directory, so a skill outside the project cannot read its own `references/` | A user-scope install adds `external_directory` allow for `~/.agents/skills/*` to the agent |

A user's own agent file can still override the project rules, because
an agent's rules come last. That agent is not the Drafting Table, and
the guard still applies to it.

---

## Skill discovery and invocation

### Discovery

OpenCode discovers skills without configuration (H1). It walks up from
the working directory to the root of the Git working tree and reads
`<name>/SKILL.md` under `.opencode/skills/`, `.claude/skills/`, and
`.agents/skills/`. It also reads `~/.config/opencode/skills/`,
`~/.claude/skills/`, and `~/.agents/skills/`. The Toolkit's
`.agents/skills/` is therefore read with no link. In the ProtoBot
repository, `.claude/skills` also links to `.agents/skills`; OpenCode
finds each skill twice and lists it once.

### Skill visibility

Discovery is not permission. The ProtoBot repository also holds
maintenance skills such as `pull-request` and `review-pr`, OpenCode
ships a built-in skill, and a user can have skills of their own.

OpenCode lists skills for the model in the system prompt, in an
`<available_skills>` block, and the `skill` tool loads one by name. The
agent's `skill` rule decides both. Headless runs on 1.18.30, on
2026-09-16, with a stub model that recorded every request, showed:

| Agent's `skill` rule | Skill in `<available_skills>` | The model calls the skill |
| --- | --- | --- |
| `"*": deny`, then one allowed name | Only the allowed name; every other skill, from the project and from the global config directory, is left out | The allowed skill loads. Any other is refused with the matching rules. |
| One allowed and one denied name, no `"*"` | Every skill except the denied one | The denied skill is refused. |

The binding's `skill` rule starts with `"*": deny`, so the model sees
and loads only the manifest's Toolkit skills. Unlike the Claude Code
binding, it needs no list of other skill names, and a skill that the
binding cannot know in advance is hidden too.

### Invocation

| Entry point | Effect |
| --- | --- |
| `/drafting-table [intent]` in a running session | The normal route. The command prompt runs on the `drafting-table` agent. |
| Selecting `drafting-table` with Tab | The same agent without the start prompt; its first turn loads the session skill |
| `opencode --agent drafting-table` | A new TUI session on the agent |
| `opencode run --agent drafting-table --command drafting-table` | A headless session; the fixture uses it |

Later turns must also run on `drafting-table`. A turn on another agent
has no governed tools, and the guard refuses them.

### The first consumer in OpenCode

`eliciting-requirements` loads through the `skill` tool and reads its
`references/` through `read`. Its evaluation wrapper under
`eval/eliciting-requirements/` runs OpenCode in a per-case workspace
with its own `opencode.json` and invokes the skill as
`/eliciting-requirements`, so the skill needs no binding file, and the
entry point's name must differ from every skill name.

---

## Obligation status

| # | Obligation | OpenCode binding | Status |
| --- | --- | --- | --- |
| H1 | Discover Toolkit skills from `.agents/skills/` | Native discovery | Met; the fixture listed `drafting-specifications`, `eliciting-requirements`, and `review-pr` from a nested directory |
| H2 | The `ears-manager` CLI and the `wms` and `scm` tools for the role | `ears-manager *` in the bash rules; the `wms` and `scm` entries and named normalized tool rules | Met; the fixture ran real `ears-manager` shell operations and `wms`/`scm` MCP tools through OpenCode's MCP clients. OpenCode 1.18.30 negotiated the legacy MCP revision 2025-11-25 with both servers |
| H3 | `drafting-table` entry point | The command and the agent | Met; the fixture entered the role headlessly with `--agent drafting-table --command drafting-table` |
| H4 | Resume on every entry, continued session, and compaction | Command prompt and session skill; `--continue` and `--session` continue a session | Met; fresh and continued sessions repeated the resume reads before any governed write |
| H5 | Nothing on idle or exit | The shim registers no idle or exit hook | Met; no commit, push, or registration ran without request, and the recording WMS process stopped cleanly. A dedicated SCM-process idle/exit assertion is deferred |
| H6 | Replayable session record | OpenCode's session record and `opencode export`; the resolved rules come from `opencode debug agent` | Met; the fixture exported every session with the agent, model, harness version, tool calls, and guard refusals |
| H7 | Headless replay with no permission prompt | `opencode run --format json`, a replay provider, no `ask` rules | Met; the whole scenario replayed twice with byte-stable summaries and no permission prompt |
| H8 | Invoke the guard on each tool call and enforce its decision | The shim | Met on the active-hook path; guard refusals reached the model and the session record. If the plugin is absent or no hook is registered, allowed `ears-manager` commands bypass guard-only shell restrictions, so a credential file or an expanded variable such as `$GH_TOKEN` can reach governed state (recorded gap); `"*": deny` still refuses `git` and `gh` |
| H9 | Hide file-writing, subagent, and web tools | `"*": deny` | Met; the resolved tool set offers neither `edit`, `write`, `task`, `webfetch`, nor `websearch`. OpenCode 1.18.30 does not register a native `apply_patch` tool for this agent; the guard vocabulary still covers it, and the fixture exercised the documented hook shape directly |
| H10 | Toolkit skills only | `skill` rule with `"*": deny` first; other skills are hidden from the model's list and refused | Met; the `<available_skills>` block the model received named exactly the Toolkit skills, and both `review-pr` load routes were refused |
| H11 | No credential in binding files; no session upload | Placeholders; `share: disabled` | Met; the resolved config shows `share` as `disabled`, and a planted token-shaped string reached no export, model request, or recording stub |
| H12 | Publish this status | This table | Met |
| H13 | Remote `wms` server with OAuth 2.1 | Not checked; the fixture is single-player | Open |

"Met" means the #77 fixture demonstrated the obligation against the pinned
OpenCode 1.18.30 binary with real governed tools; where an obligation holds
only on a checked path, the gap stays in the row. Deferred fixture steps are
recorded in the golden fixture's expectation record
([Running the fixture on OpenCode](#running-the-fixture-on-opencode)).

What the OpenCode layer stops on its normal native and active-guard
paths, by route. The plugin-unavailable exception above applies to any
row that depends on the guard:

| Write route to a guarded path | `drafting-table` agent | Every other agent |
| --- | --- | --- |
| File tool under `.protobot/` project controls | Tool not offered | Refused by the project rule and the guard |
| File tool under Git metadata or reserved harness binding paths/files (`.agents/`, `.claude/`, `.codex/`, `.opencode/`, `.github/`, `opencode.json`, `AGENTS.md`, or `CLAUDE.md`) | Tool not offered | Refused by the active guard |
| File tool under the selected adapter manifest path and its containing directory/`skills/` subtree | Tool not offered | Refused by the active guard, whether or not the selected manifest exists |
| File tool under configured store paths read from `project.yaml` and resolved safely | Tool not offered | Refused by the active guard |
| File tool on a registered path elsewhere, when known from successful project inspection | Tool not offered | Refused by the guard |
| Direct shell writer to an ordinary registered artifact from the project root or by absolute path, such as `sed -i`, `cp`, `tee`, `truncate`, `sort -o`/`--output`, `uniq input output`, or Git `diff`/`diff-tree`/`diff-index`/`diff-files`/`range-diff`/`show`/`log --output` | Refused by the native copy and the role's exact shell grammar | Not generally stopped solely because the target is an ordinary registered file; a relative target after a working-directory change is refused as ambiguous |
| Recognized direct shell writers to the `.protobot/` project-control namespace, Git metadata, reserved harness binding paths/files (`.agents/`, `.claude/`, `.codex/`, `.opencode/`, `.github/`, `opencode.json`, `AGENTS.md`, or `CLAUDE.md`), configured store paths read from `project.yaml` and resolved safely, or the selected adapter manifest path and its containing directory/`skills/` subtree, as detailed in [Rule 7](adapter-contract.md#guard-rules) | Refused by the role's exact shell grammar | Refused by the active guard when it returns a decision. Rule 7 also checks move/removal and link-mode source operands, including existing symlink chains and destination-relative symbolic-link targets; a recognized directory-content `cp` (source `.` or ending in `/.`) is refused when its resolved destination directory is an ancestor of or equal to Git metadata, `.protobot/`, the selected manifest/`skills/`, `.agents/drafting-table.yaml`, `.agents/skills`, or safely resolved configured store paths; a plain directory copy such as `cp -r dir .` checks its mapped `destination/basename(source)` target instead. For `cp`, `mv`, and `install`, existing or trailing-slash directory destinations and `-t`/`--target-directory` check each source's mapped `destination/basename(source)` target. `cp --parents`, `rsync --relative`/`-R`, and `rsync --files-from` fail closed because they change destination mapping (`-R` is recursive for `cp` and relative for `rsync`; `--files-from` uses uninspected file-list entries to map output beneath the destination). `rsync --backup-dir`, `--partial-dir`, `--temp-dir`, and `--write-batch` fail closed because they can name write locations outside modeled destinations. `rsync --rsh`/`-e` and `--rsync-path` fail closed because they carry remote-side commands. `rsync --link-dest` fails closed because it can create a hard link from an alternate base into a mapped destination, allowing a later in-place update to change the base. `rsync --remove-source-files` fails closed because it deletes source operands, making the sources write locations. `ln` checks directory destinations, including trailing slash, and the implicit `basename(target)` destination for single-operand `ln target` and `ln -s target`. The writer inventory includes `mkdir` and `touch`. Recognized file writers fail closed when required operands are missing. Optional long values for `cp --context[=CTX]`, `cp --reflink[=WHEN]`, `mv --context[=CTX]`, and `install`/`mkdir --context[=CTX]` do not consume the next argument. The short `-Z` flag for `cp`, `install`, and `mkdir` also leaves the next argument as an operand; `install --preserve-context` and `rsync --delete-missing-args` are flags. Symlink-preserving analysis covers tree members for recursive `cp -r`/`-R`/`-a`/`--recursive` unless `-L`/`--dereference` is present without `-H` or an explicit symlink-preserving flag; `-H` dereferences command-line links but tree-member links remain covered; `mv` directory trees; `rsync -a`/`--archive`; or `rsync -r`/`--recursive` with `-l`/`--links`. Top-level source links are preserved for `cp -a`/`--archive`/`-d`/`-P`/`--no-dereference` and for recursive `cp` without `-H`/`-L`/`--dereference`; `mv` and `rsync -a`/`--archive`/`-l`/`--links` also preserve top-level links. `cp --preserve=links` preserves hard links only, not symlink operands. Tree walks do not follow symlinks, re-anchor relative symbolic-link text at the mapped destination, check absolute link text unchanged, and fail closed on inspection errors or more than 10,000 entries. For recursive `cp`, `-H` combined with `-L`/`--dereference` makes the guard scan tree-member links conservatively; if `-H`, `-L`, or `--dereference` is combined with an explicit symlink-preserving flag (`-a`, `-d`, `-P`, or `--no-dereference`), it also checks top-level source links. The targeted inventory is not exhaustive; on a fail-open direct write, intact artifact/store digests can catch changed registered content but not coordinated digest edits or provide general writer provenance |
| Output redirection in a shell command | Refused by the active guard; not checked inside an allowed native command pattern | Refused for the `.protobot/` project-control namespace, Git metadata, reserved harness binding paths/files, the selected adapter manifest path and its containing directory/`skills/` subtree, configured store paths read from `project.yaml` and resolved safely, and registered artifact paths known from successful project inspection; any shell command that contains both an output redirection and any working-directory change is refused, regardless of order; an uninspectable target is refused too |
| Recognized opaque execution forms that could hide an indirect governed write, including inline or standard-input interpreters, command-string wrappers, Git aliases and metadata-writing Git commands, transport plumbing commands `git fetch-pack` and `git send-pack`, recipe/build/test runners, execution-changing `find` placeholders, `rg` preprocessor/pager forms except data-only `--paging=never` and `--paging never`, and Git `grep -O`. Rule 4 defines the exact targeted environment keys (including Git SSH/askpass and repository/index/object/template/ceiling overrides, shell-startup/loader assignments, ripgrep configuration, and imported `BASH_FUNC_*%%` assignments), the `xargs --help`/`--version` informational forms, and the `xargs` append/replacement rules. Git effect examples include unconditional `archive` (including `-o`, `--output`, and stdout forms), `bundle create`, unconditional `format-patch` (because `format.outputDirectory` can redirect output), `mailsplit`, `checkout-index`, `fast-import`, and `instaweb`; these are examples, not a complete Git command inventory. The guard does not refuse every environment assignment or every `xargs` invocation. | Refused by the role's exact shell grammar | Refused by the active guard; no native rule covers these forms if the plugin is absent |
| Some interpreter source-file invocations, such as `python helper.py`, renamed executables, or unrecognized shell writers/executors | Refused by the role's exact shell grammar | Not generally stopped; an indirect `ears-manager` write has no later control |
| Tool of another MCP server | Not offered | The user's own configuration |
| Subagent | `task` not offered | Not applicable |

Later integrity and CI layers catch unauthorized persistent edits to
registered paths, and configured-store changes only while their recorded
digests remain intact; they do not establish writer provenance or generally
check `.protobot/` control files, reserved harness controls, or Git metadata.
They also do not enforce shell grammar, argument values, variable expansion,
or file-source provenance. In particular, they do not make an allowed shell
command safe when the plugin is unavailable; see [What the harness layer
stops][layer-stops].

---

## Observed OpenCode behaviors

The binding relies on these behaviors, each observed on OpenCode
1.18.30:

1. Skills are discovered from the locations in [Discovery](#discovery),
   and a name found twice is listed once.
2. A skill denied by the `skill` rule is left out of the
   `<available_skills>` block of the system prompt and cannot be
   loaded. A `"*": deny` rule hides project skills and skills in the
   global config directory alike.
3. A built-in tool with no allow rule for the agent is not offered to
   the model.
4. Rules resolve in the order and with the effects in
   [How the rules combine](#how-the-rules-combine).
5. Shell patterns match the whole command text, here-document bodies
   included. Commands joined with `;` are checked one by one. An output
   redirection inside an allowed command is not checked.
6. In `opencode run`, a rule that resolves to `ask` is rejected and the
   run ends.
7. An error thrown in the `tool.execute.before` plugin hook blocks the
   call, and its text reaches the model and the session record. The
   `chat.params` hook carries the agent name.
8. `opencode export` writes the session record as JSON with the agent,
   the model and provider, the OpenCode version, and every tool call.
   `opencode export --sanitize` redacts message text, tool arguments,
   and tool results, and keeps tool names, statuses, and error texts.
9. A custom OpenAI-compatible provider can serve recorded model turns
   to `opencode run`.

Issue #77 pins the OpenCode version it tests. An upgrade runs the
fixture before it is used. A behavior that changes is fixed in this
binding, never in a Toolkit or adapter-core file.

---

## Running the fixture on OpenCode

The [fixture session](adapter-contract.md#fixture-session) needs these
harness commands. For OpenCode:

| Fixture need | OpenCode |
| --- | --- |
| List discovered skills (step 1) | `opencode debug skill` |
| Headless turn in the role (steps 2 to 14) | `opencode run --agent drafting-table --command drafting-table --format json`; add `--continue` or `--session <id>` to continue |
| Headless turn outside the role (steps 2, 7, 9) | `opencode run --agent build --format json` |
| Resolved native rules | `opencode debug agent drafting-table` and `opencode debug config` |
| Session export (step 15) | `opencode export <session>`, with and without `--sanitize` |

**Replayed model.** A local OpenAI-compatible endpoint returns the
recorded model turns in order. It is registered as a custom provider:

```json
{
  "provider": {
    "replay": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Fixture replay",
      "options": { "baseURL": "http://127.0.0.1:<port>/v1" },
      "models": { "turns": { "name": "turns" } }
    }
  },
  "model": "replay/turns",
  "small_model": "replay/turns"
}
```

The endpoint answers a request that offers tools with the next recorded
turn, and a request without tools, such as title generation, with a
short text. OpenCode then runs its real skill tool, rules, plugin, MCP
clients, and shell.

**Binding checks** added to the harness-neutral ones:

- `opencode debug config` shows `share` as `disabled`.
- `opencode debug agent drafting-table` shows no `ask` rule from the
  agent file, and its `skill` patterns equal the manifest's
  `toolkit_skills`.
- The replay endpoint records each request. In a turn in the role, the
  `<available_skills>` block of the system prompt names exactly the
  manifest's `toolkit_skills`.
- No headless run prints an automatic permission rejection.

**Running the #77 fixture.** The implemented driver replays the recorded
scenario twice and compares the semantic summaries and source-input hashes:

```sh
PYTHONDONTWRITEBYTECODE=1 OPENCODE_BIN=/path/to/opencode-1.18.30/bin/opencode \
  python -m unittest discover -s tests/drafting-table -p 'test_*.py' -v
```

The driver builds the real `ears-manager`, `source-control-manager`, and
`drafting-table-guard` binaries from the repository modules, provisions an
isolated temporary Git project, and uses a loopback-only recorded model
endpoint plus recording WMS and `gh` stubs. It never edits the checkout or
registered specification artifacts. The inputs, expected state, and deferred
steps are recorded in
[`docs/architecture/fixtures/drafting-table-opencode-golden.jsonl`](../fixtures/drafting-table-opencode-golden.jsonl).
OpenCode 1.18.30 does not register a native `apply_patch` tool in the
resolved agent tool set; the fixture records that fact and exercises the
documented `apply_patch` hook shape against the built guard directly.

---

## Related Documents

- [Agent Harness Adapter Contract](adapter-contract.md) — The
  harness-neutral contract this binding implements.
- [Claude Code Harness Binding](claude-code.md) — The sibling binding
  for Claude Code.
- [Codex Harness Binding](codex.md) — The sibling binding for Codex.
- [Vision](../../vision.md) — Purpose, intended users, desired
  outcomes, prototype scope, and non-goals.
- [Architecture](../../architecture.md) — The Drafting Table Boundary and
  the OpenCode-plus-skill strawman.
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
- [Evaluation Corpus Schema and Result Contract](../evaluation-corpus.md)
  — Versioned fixtures, holdout isolation, and stage-attributed
  results.
- [Open Design Questions](../open-questions.md) — Unresolved design
  questions across all areas.
- [Related Work](../related-work.md) — Internal and external
  projects informing the design.

[layer-stops]: adapter-contract.md#what-the-harness-layer-stops
