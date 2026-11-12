# ProtoBot: OpenCode Harness Binding

> Design document — September 2026
>
> The first harness binding of the
> [Agent Harness Adapter Contract](adapter-contract.md): the OpenCode
> files, how they meet each obligation, and the OpenCode behaviors they
> rely on.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [OpenCode v2 requirement](#opencode-v2-requirement)
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
document states the choice and the obligation it serves. ProtoBot
requires OpenCode v2. Issue #272 rechecked CLI flags, config keys,
plugin hooks, and debug commands against OpenCode 2.0.28
(`@opencode/cli`) source and documentation. Behaviors first observed
on OpenCode 1.18.30 with a replayed model remain in
[Observed OpenCode behaviors](#observed-opencode-behaviors) as
historical evidence until issue #77 re-runs the fixture on v2. v1.x is
not supported. The fixture has not run here yet. Issue #77 runs it, and
turns each "designed" and "observed" status into "met" or into a
recorded gap.

---

## OpenCode v2 requirement

Every repository-managed OpenCode invocation installs or selects v2 and
rejects v1.x, with the exception of the historical #77 Drafting Table
fixture driver (`tests/drafting-table/replay.py`) until issue #77 re-runs
and ports it to v2. Generic mentions of "OpenCode" as the first Drafting
Table harness are not version pins.

| Touch point | Kind | Disposition |
| --- | --- | --- |
| `eval/eliciting-requirements/scripts/run-opencode.sh` | Executable | Requires `opencode --version` 2.x; rejects a missing binary and 1.x; invokes `opencode run --standalone --model … --format json --auto` from the case workspace |
| `eval/eliciting-requirements/scripts/run-opencode_test.sh` | Test | Mocks 1.18.30, 2.0.28, 3.0.0, a noisy version stream, a missing binary, and an unparseable version; verifies CLI flags, bwrap sandbox argv, and fallback |
| `eval/eliciting-requirements/{eval,eval-regression}.yaml` | Config | Call the wrapper; they do not pin a binary |
| This binding and the adapter contract | Spec | Designed against OpenCode 2.0.28; 1.18.30 notes are historical |
| `docs/architecture.md` strawman, overview, components, UX | Spec | Name OpenCode as the first harness; not install paths |
| `eval/eliciting-requirements/baselines/v1/` and `/eval-run --run-id v2` | Eval | Baseline and run identifiers, not OpenCode versions |
| Binding files under `.opencode/` | Install (issue #77) | Project-scope v2 layout; the v1 plugin (`.opencode/plugins/protobot-guard.js`) was removed to unblock sessions, leaving H8 inactive in-repo until a v2 shim is restored (#77); no repository-managed installer yet |
| `tests/drafting-table/replay.py` | Executable | Historical #77 fixture driver; still pins 1.18.30 and v1 CLI flags until #77 ports it to v2; non-operative for v2 |
| `tests/drafting-table/README.md` | Doc | Historical #77 fixture instructions; exercises the 1.18.30 baseline driver until #77 re-runs on v2; non-operative for v2 |
| `docs/architecture/fixtures/drafting-table-opencode-golden.jsonl` | Fixture | Recorded #77 golden run (`opencode_version: 1.18.30`); historical baseline, non-operative for v2 |

There is no CI job that installs OpenCode. A setup or test that consumes
an external binary (outside the historical #77 replay test suite) must run
the wrapper (or the same version check) before use. Install v2 with
`npm i -g @opencode/cli` or another v2 installer from the
[OpenCode repository](https://github.com/anomalyco/opencode).

---

## Binding files

### Installed files

The binding is three checked-in files; the v1 plugin
(`.opencode/plugins/protobot-guard.js`) was removed pending the v2 plugin
implementation. At project scope they sit beside the shared layer:

```text
<project root>/
├── opencode.json                        rules for every OpenCode agent
├── .opencode/
│   ├── agents/drafting-table.md         the Drafting Table role
│   └── commands/drafting-table.md       the drafting-table entry point
└── .agents/                             shared layer (harness-neutral)
    ├── drafting-table.yaml
    └── skills/
```

At user scope the corresponding files go under `~/.config/opencode/`.
Project config is merged after user config, so a project that carries
the binding overrides a user-scope install. A user-scope install also
needs the `external_directory` rule in
[How the rules combine](#how-the-rules-combine).

### `opencode.json`

```json
{
  "share": "disabled"
}
```

- **`share` is `disabled`**, because OpenCode's share feature uploads a
  session (H11).
- **MCP server entries, permission denials, and global `apply_patch` denial were
  removed.** The missing-binary MCP spawn (`source-control-manager`), permission
  denials, and global `apply_patch: deny` were removed from root `opencode.json`
  to unblock ordinary agent sessions. WMS/SCM tool integration is deferred to
  milestone M2.

This file disables session sharing, but adds no allow or deny rules;
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

The v1 shim `.opencode/plugins/protobot-guard.js` was removed to unblock
ordinary sessions; H8 is not active in-repo until a v2 shim is restored.
OpenCode has no `PreToolUse` command hook, so the target shim will do the
translation to the shared [guard](adapter-contract.md#the-guard) (H8).

Issue #77 targets OpenCode v2's plugin contract: OpenCode v2 loads a
local plugin as a default export with an `id` and a `setup` (or `effect`)
function; a v1 module such as
`export const Plugin = async (ctx) => ({ "tool.execute.before": … })`
is not a valid v2 plugin. The historical v1 shim used named export
(`export const ProtoBotGuard = ...`) with `chat.params` and
`tool.execute.before`.

The v2 target design:

1. **Reads the role from the hook event.** OpenCode v2's
   `tool.execute.before` event names `agent`, `tool`, `sessionID`,
   `messageID`, `id`, and `input`. `drafting-table` is the Drafting
   Table role; every other agent is `other`. Historically, OpenCode
   1.18.30 did not put the agent on this hook, and the v1 shim used
   `chat.params` instead; that is not the v2 contract.
2. **Builds the guard input.** For each tool call it writes the
   `PreToolUse` JSON — `hook_event_name`, `session_id`, `cwd`,
   `tool_name`, and `tool_input` from the call's arguments — and runs
   `drafting-table-guard --harness opencode --role <role>`.
3. **Blocks on refusal.** On any non-zero status it fails the hook with
   a tool error whose message is the guard's line. OpenCode v2 rejects
   the call before the tool runs and returns that message to the model
   as the tool result, and keeps it in the session record.

The target shim holds no rule, reads no project file, and does nothing when a
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
an agent's rules come last. That agent is not the Drafting Table;
in-repo H8 is inactive until the v2 plugin is restored (#77), so such
an agent is limited to native OpenCode permission rules.

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
agent's `skill` rule decides both. Headless runs on 1.18.30 (historical),
on 2026-09-16, with a stub model that recorded every request, showed:

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
| `opencode run --standalone --agent drafting-table --format json "/drafting-table"` | A headless session; this is the v2 headless entry issue #77 should use. The message is the `/drafting-table` command; v2 `run` has no `--command` flag. `--standalone` uses a private server instead of the v2 background service; the checked-in historical driver still uses `--command` without `--standalone` until that port. |

Later turns must also run on `drafting-table`. A turn on another agent
has no governed tools; in-repo H8 is inactive pending #77, so other-agent
turns are limited to native OpenCode permission rules rather than guard
refusal.

### The first consumer in OpenCode

`eliciting-requirements` loads through the `skill` tool and reads its
`references/` through `read`. Its evaluation wrapper under
`eval/eliciting-requirements/` runs OpenCode v2 in a per-case workspace
with its own `opencode.json` and invokes the skill as
`/eliciting-requirements`, so the skill needs no binding file, and the
entry point's name must differ from every skill name. The wrapper
rejects OpenCode 1.x before it writes that config.

---

## Obligation status

| # | Obligation | OpenCode binding | Status |
| --- | --- | --- | --- |
| H1 | Discover Toolkit skills from `.agents/skills/` | Native discovery | Observed (behavior 1); the fixture has not run |
| H2 | The `ears-manager` CLI and the `wms` and `scm` tools for the role | `ears-manager *` in the bash rules; root `opencode.json` has no `wms`/`scm` MCP entries; named tool allows in the agent file are inert until M2 restores those servers | Designed; pattern matching observed on 1.18.30 (behavior 5), the `wms` and `scm` servers not yet. The `scm` server serves MCP revision 2026-07-28 and the legacy 2025-11-25 ([MCP protocol](../source-control-manager.md#mcp-protocol)); the fixture records which one OpenCode v2 negotiates |
| H3 | `drafting-table` entry point | The command and the agent | Designed; `--agent` observed in the stub runs, the command file not yet |
| H4 | Resume on every entry, continued session, and compaction | Command prompt and session skill; `--continue` and `--session` continue a session | Designed |
| H5 | Nothing on idle or exit | The target shim registers no idle or exit hook (shim not checked in pending #77) | Designed |
| H6 | Replayable session record | OpenCode's session record and `opencode session export`; the resolved rules come from `opencode debug agents` | Observed on 1.18.30 for `opencode export` (behavior 8); v2 command is `opencode session export` |
| H7 | Headless replay with no permission prompt | `opencode run --standalone --format json`, a replay provider, no `ask` rules | Observed for the replay provider and the `ask` rejection (behaviors 6, 9); the fixture has not run |
| H8 | Invoke the guard on each tool call and enforce its decision | The target shim (shim not checked in pending #77) | Historical 1.18.30 observation only (behavior 7); shim not checked in, leaving H8 inactive in-repo pending #77. If the plugin is absent or no hook is registered, allowed `ears-manager` commands bypass guard-only shell restrictions, so a credential file or an expanded variable such as `$GH_TOKEN` can reach governed state (recorded gap); `"*": deny` still refuses `git` and `gh` |
| H9 | Hide file-writing, subagent, and web tools | `"*": deny` | Observed (behavior 3) |
| H10 | Toolkit skills only | `skill` rule with `"*": deny` first; other skills are hidden from the model's list and refused | Observed (behavior 2) |
| H11 | No credential in binding files; no session upload | Placeholders; `share: disabled` | Designed |
| H12 | Publish this status | This table | Met |
| H13 | Remote `wms` server with OAuth 2.1 | Not checked; the fixture is single-player | Open |

"Designed" means the mechanism is documented and the fixture has not
run. "Observed" means a stub run on 1.18.30 showed it, and the number
points at [Observed OpenCode behaviors](#observed-opencode-behaviors).
Those observations are historical until #77 re-runs them on v2. Nothing
else is marked met; issue #77 runs the fixture on OpenCode v2.

What the OpenCode layer stops on its normal native and active-guard
paths, by route. The plugin-unavailable exception above applies to any
row that depends on the guard:

| Write route to a guarded path | `drafting-table` agent | Every other agent |
| --- | --- | --- |
| File tool under `.protobot/` project controls | Tool not offered | Refused by the active guard |
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

Later integrity and CI layers catch registered-artifact digest mismatches,
registered artifact path/canonical-content deltas without a matching proposed
operation, and unregistration. Artifact path/content deltas without an
operation are caught even if their digest is rewritten; configured-store
integrity checks catch changes only while their recorded digests remain intact.
These checks do not establish writer provenance or generally check
`.protobot/` control files, reserved harness controls, the selected manifest,
or Git metadata. Coordinated manual edits to artifact content and its registry
digest, plus a manifest containing a matching operation, can pass. Hand edits to
`repository.default_branch`, or to
`repository.canonical_remote` when the remote-tracking fallback is used, can
retarget the baseline selected from the working-tree project configuration.
The resolver uses the local default ref first, falls back to a canonical
remote-tracking ref, and does not fetch. Rule 5 in `AGENTS.md` prohibits
hand-editing `.protobot/project.yaml`; Rule 6 requires reviewers to inspect it
and flag hand edits. These layers also do not enforce shell grammar, argument
values, variable expansion, or file-source provenance. In particular, they do
not make an allowed shell command safe when the plugin is unavailable; see
[What the harness layer stops][layer-stops].

---

## Observed OpenCode behaviors

The binding relies on these behaviors. Items 1 to 9 were observed on
OpenCode 1.18.30 and are historical until the fixture re-runs them on
v2. Issue #272 confirmed the v2 CLI, config, and plugin surface below
against OpenCode 2.0.28 source; it did not re-run the stub model.

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
   call, and its text reaches the model and the session record. On
   1.18.30 the `chat.params` hook carried the agent name. OpenCode v2
   puts `agent` on the `tool.execute.before` event, so the target shim
   should not use `chat.params`; the v1 shim using `chat.params` was
   removed pending #77.
8. Session export writes the session record as JSON with the agent,
   the model and provider, the OpenCode version, and every tool call.
   `--sanitize` redacts message text, tool arguments, and tool results,
   and keeps tool names, statuses, and error texts. On 1.18.30 the
   command was `opencode export`; on v2 it is `opencode session export`.
9. A custom OpenAI-compatible provider can serve recorded model turns
   to `opencode run`.

**v2 CLI and plugin surface**, confirmed from OpenCode 2.0.28 source
and documentation (not from a stub-model run):

- `opencode run` accepts `--agent`, `--model`, `--format json`,
  `--auto`, `--continue`, `--session`, `--standalone`, and a message;
  it has no `--dir` and no `--command`.
- `opencode debug` exposes `agents`, `config`, and `paths`. It does not
  expose `skill` or a singular `agent` command.
- `opencode session export` accepts `--sanitize`.
- A local plugin is a default export with `id` and `setup` (or
  `effect`). `tool.execute.before` may fail and reject the call before
  the tool runs; the event includes `agent`.
- `share: "disabled"`, `permission` object syntax, local `mcp` servers,
  and skill discovery from `.agents/skills/` remain valid.

Issue #77 pins the OpenCode v2 version it tests. An upgrade runs the
fixture before it is used. A behavior that changes is fixed in this
binding, never in a Toolkit or adapter-core file.

---

## Running the fixture on OpenCode

The [fixture session](adapter-contract.md#fixture-session) needs these
harness commands. For OpenCode:

| Fixture need | OpenCode v2 |
| --- | --- |
| List discovered skills (step 1) | Open gap until #77; v2 dropped `opencode debug skill` and has no command to list discovered skills before a session. The `<available_skills>` system prompt block verified at the replay endpoint is a later binding check (steps 2 and 4), not a substitute for step 1 |
| Headless turn in the role (steps 2 to 14) | `opencode run --standalone --agent drafting-table --format json "/drafting-table"`; add `--continue` or `--session <id>` to continue |
| Headless turn outside the role (steps 2, 7, 9) | `opencode run --standalone --agent build --format json` with a message |
| Resolved native rules | `opencode debug agents` and `opencode debug config` |
| Session export (step 15) | `opencode session export <session>`, with and without `--sanitize` |

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
- `opencode debug agents` lists `drafting-table` with no `ask` rule from
  the agent file, and its `skill` patterns equal the manifest's
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
