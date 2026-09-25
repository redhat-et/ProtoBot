# ProtoBot: Agent Harness Adapter Contract

> Design document — September 2026
>
> Defines how the Specification Toolkit becomes the TUI Drafting Table
> inside any coding-agent harness, what is written once for every
> harness, and what each harness binding adds.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [The three layers](#the-three-layers)
- [Adapter core](#adapter-core)
- [Session entry and resume](#session-entry-and-resume)
- [What the harness layer stops](#what-the-harness-layer-stops)
- [Repository and credential capabilities](#repository-and-credential-capabilities)
- [Deployment modes](#deployment-modes)
- [Artifacts and traces](#artifacts-and-traces)
- [Resumable state](#resumable-state)
- [Exit conditions](#exit-conditions)
- [Harness obligations](#harness-obligations)
- [Adding a harness](#adding-a-harness)
- [Toolkit skill rules](#toolkit-skill-rules)
- [Fixture session](#fixture-session)
- [Out-of-scope decisions](#out-of-scope-decisions)
- [Related Documents](#related-documents)

---

## Purpose and scope

This document answers the question posed by issue #33: _What is the
smallest useful Drafting Table adapter when the user invokes ProtoBot
inside an existing OpenCode session?_

OpenCode is the first harness, not the only one. The Toolkit must work
in any compatible agent harness
([Specification Toolkit](../../architecture.md#specification-toolkit)),
and ProtoBot is expected to run in Claude Code, Codex, OpenCode, and
harnesses that do not exist yet. The answer is one contract and one
binding per harness:

- **This document is the harness-neutral adapter contract.** It defines
  what is written once and shared by every harness, and what every
  harness binding must provide.
- **[OpenCode Harness Binding](opencode.md) is the first binding.** It
  defines the OpenCode files that meet this contract, designed against
  OpenCode 1.18.30 and partly observed with a stub model. Its fixture
  runs in #77.
- **[Claude Code Harness Binding](claude-code.md) is the second.** It
  defines the Claude Code files, designed against the Claude Code
  2.1.273 documentation and CLI help. Its fixture has not run yet.
- **[Codex Harness Binding](codex.md) is the third.** It defines the
  Codex files, designed against Codex CLI 0.154.0 and partly observed
  with a stub model. Its fixture has not run yet.

A binding for another harness is one more document beside those three
and one more row in [Binding status](#binding-status). It changes no
Toolkit file and no rule in this document.

There is no `protobot` program. In every harness the user invokes
ProtoBot through an entry point named `drafting-table`, which loads the
Toolkit skills. The adapter core adds one executable, the
[guard](#the-guard).

The first Toolkit consumer is the `eliciting-requirements` skill in
`.agents/skills/` (#56). The contract is defined for the
**single-player TUI Drafting Table** first
([Overview — Single-player mode](../overview.md#single-player-mode)).
The Web Drafting Table and a standalone session manager are out of
scope: the Web Drafting Table replaces the local harness with a hosted
runtime
([Drafting Table Boundary](../../architecture.md#drafting-table-boundary)),
and every harness already owns its sessions.

### Relationship to sibling contracts

- **#28** ([Drafting Table UX][ux]) defines what the user sees and
  decides. It defers skill packaging, adapter hooks, the
  session-recording notice, and chat persistence to #33. Where a
  harness sandbox refuses a governed step, the user runs it; that is
  a recorded deviation from #28's agent-performed steps
  ([Shell operations](#shell-operations)).
- **#30** ([`ears-manager` CLI Integration Contract](../ears-manager-cli.md))
  defines the commands and their request and result shapes. This
  document decides that the role runs that CLI through the harness's
  shell tool, and takes the command grammar, the JSON envelope, and
  the exit statuses from that contract.
- **#31** ([Drafting Table WMS integration](../drafting-table-wms.md))
  defines the WMS operations. This document registers them as tools and
  does not name them.
- **#32** ([Validation Rules](../validation-rules.md)) defines lifecycle
  validation. #32 places preflight with the caller; in a harness the
  caller is the `wms` server acting for the role, which offers
  preflight as a tool that #31 names and answers with
  `authority: preflight`, so no harness loads a rule library. That
  placement is a decision of this document
  ([Out-of-scope decisions](#out-of-scope-decisions)). The WMS write
  boundary stays authoritative: it rejects a lifecycle transition from
  the `drafting-table` role with `UNAUTHORIZED_ACTION` (case VR-006).
- **#34** ([Git and Project-Repository Integration](../git-integration.md))
  defines the Git rules. It leaves the harness permission layer to #33,
  which decides it in [Shell operations](#shell-operations).
- **#125** ([Source Control Manager][scm]) defines the component that
  performs the Git and Git host operations of #34 for the role, and its
  host client binding. This document registers the SCM's `scm` tools
  in the manifest, and every binding starts the `scm` server.
- **#77** implements the OpenCode Drafting Table MVP: the adapter
  core, the OpenCode binding, the `drafting-specifications` skill, and
  the [fixture session](#fixture-session) as its test plan.

---

## The three layers

| Layer | Contains | Written | Changes when |
| --- | --- | --- | --- |
| Specification Toolkit | Skills: the session protocol, elicitation, Sketching and Dimensioning guidance, the `ears-manager` CLI guidance, and the [host mapping](#host-mapping). MCP tool definitions for the WMS Adapter and the Source Control Manager. Reference material. | Once, for every harness | The specification method changes |
| Adapter core | The [manifest](#the-adapter-manifest), the [Drafting Table role](#the-drafting-table-role), the [shell operations](#shell-operations), the [guard](#the-guard) and its test vectors, and the [fixture](#fixture-session) | Once, for every harness | This contract changes |
| Harness binding | The harness's own files: MCP registration, skill discovery, the role's native rules, the entry point, the hook that calls the guard, and access to the session record | Once per harness | That harness changes |
| Governed systems | `ears-manager`, the WMS Adapter, and the Source Control Manager in front of Git and the Git host | Their own contracts (#30, #31, #34, #125) | Their contracts change |

Three rules keep the layers apart:

1. **No Toolkit file names a harness.** A Toolkit skill names
   operations — `ears-manager requirement add`, a WMS operation from
   issue #31, an SCM operation from issue #125 — and never a harness
   tool, a permission key, or a harness config file.
2. **The adapter core names a harness in one place only:** the guard's
   [tool vocabulary](#tool-vocabulary), which has one row per harness.
3. **A binding holds no domain logic and no policy of its own.** It
   connects the harness to the Toolkit and to the guard. Its native
   rules may copy the core's rules as an early layer and add no
   permission of their own. A native pattern can still be coarser than
   the core, so on a call that receives no guard decision it can admit
   a command the core refuses; each binding records those cases as
   gaps ([File-source arguments](#file-source-arguments)).

### The session protocol is a skill

The start, resume, checkpoint, and approval protocol of the UX contract
is domain logic. If it lived in a harness's agent prompt, every harness would
have to copy it, and the copies would drift. It therefore lives in a
Toolkit skill, `drafting-specifications`, which issue #77 writes from
the [Drafting Table UX][ux] contract. The skill also holds the protocol
rules that do not depend on a harness:

- a refused tool call is a result, reported and not retried in another
  form;
- the resume steps run again after a continued or compacted
  conversation, before the next governed write; and
- the start summary tells the user once that the harness records the
  session on this machine.

A binding's prompt only loads that skill and states how operation names
map to the harness's tool names.

`eliciting-requirements` stays a general-purpose capability, as its
`SKILL.md` states, and knows nothing about ProtoBot or any harness.
`drafting-specifications` is its host.

### The swap test

The layers hold when one harness binding can replace another while
every Toolkit file and every adapter-core file stays byte-identical.
[Adding a harness](#adding-a-harness) turns that test into a procedure.

---

## Adapter core

### Installed layout

```text
<project root>/
├── .agents/
│   ├── drafting-table.yaml        adapter manifest
│   └── skills/                    Toolkit skills
│       ├── drafting-specifications/
│       └── eliciting-requirements/
└── <binding files>                one set per harness, in its own directories

On PATH: ears-manager, drafting-table-guard, source-control-manager,
         the WMS Adapter MCP server,
         register-approved-change-set (single-player, from the Job Site)
```

`.agents/` is the directory that several harnesses already read for
skills, so the shared layer lives there. Each binding keeps its files in
its harness's own directories, so bindings for several harnesses sit in
one project without touching each other.

These are ordinary project files. They are not registered specification
artifacts, the Drafting Table never stages them, and the deny-by-default
projection manifest keeps them out of Worker projections
([Worker repository projections][projections]). A change to them
arrives as a normal pull request and is reviewed as code, because a
binding runs its hook on the machine of everyone who opens the project.

A project that does not carry the adapter can install the same shared
files under `~/.agents/` and each binding under its harness's user
config directory. Outside the Drafting Table role, the guard does
nothing in a directory that is not a ProtoBot project, so a user-scope
install leaves other work unchanged.

None of these files is a specification artifact, and a governed write
must not reach them either: the guard refuses an `artifact put` or
`project init` whose path lies under `.agents/`, `.claude/`, `.codex/`,
`.opencode/`, `.github/`, or `.git/`, or names `opencode.json`,
`AGENTS.md`, or `CLAUDE.md` ([Shell operations](#shell-operations)).
Otherwise the role could register its own hook, manifest, or project
instructions as an artifact and rewrite them for the next session.

### The adapter manifest

`.agents/drafting-table.yaml` is the single list that every binding and
the guard read:

```yaml
adapter_layout: 1
entry_point: drafting-table
session_skill: drafting-specifications
toolkit_skills:
  - drafting-specifications
  - eliciting-requirements
governed_commands:
  - ears-manager
governed_mcp_servers:
  wms:
    tools:
      - request_create
      - request_refine
      - request_link_change_set
      - request_link_build_work_item
      - request_get
      - request_query
      - work_item_get
      - work_item_query
      - blocked_work_query
      - lifecycle_preflight
      - blocked_work_submit_resolution
      - blocked_work_acknowledge
  scm:
    tools:
      - repo_state
      - branch_init
      - branch_resume
      - commit
      - publish
      - refresh
```

- A binding takes its entry-point name, its skill allowlist, its
  governed command, and its MCP servers with the tools the role may
  call from the manifest. Where a harness needs the values written
  into its own config, the binding copies them, and the fixture checks
  that the copy matches.
- The tool list of a governed server is the role's allowlist for it.
  The names are the canonical operation names with `.` and `-` replaced by
  `_`; a harness adds its server prefix (`<server>_` in OpenCode,
  `mcp__<server>__` in Claude Code and Codex). A lifecycle transition is
  never on the `wms` list. The fixture's manifest lists the names its
  `wms` stub serves.
- The `scm` list is the Drafting Table face of the
  [Source Control Manager][scm], which a binding starts with
  `source-control-manager serve --face drafting-table`. No Git or Git
  host operation reaches the role any other way.
- A new Toolkit skill is one manifest line, plus the same line in each
  binding's native copy.
- The manifest holds no credential and no path rule.

### The Drafting Table role

Each binding gives exactly one agent, profile, or session mode the
Drafting Table role. Only that role performs governed mutations.

| Capability | Drafting Table role | Through |
| --- | --- | --- |
| Read project files | Yes, except `.protobot/` other than `project.yaml`, `.git/`, and credential files | The harness's read and search tools, or the read forms of its [tool vocabulary](#tool-vocabulary) row when it has none |
| Read files outside the project | No, except a user-scope Toolkit skill root | — |
| Read and write registered specifications | Yes, validated | The `ears-manager` CLI, through the harness's shell tool |
| Write a file directly | No | — |
| Read requests and work items, create and refine requests, submit reviewed resolutions | Yes | The `wms` tools the manifest lists |
| Transition a work item | No. The manifest lists no such tool, and the WMS boundary rejects it (#32, case VR-006) | — |
| Git and pull-request operations | Only the `scm` tools the manifest lists; the SCM binds every ref, path, message, and pull request to the current change set and the canonical repository | The `scm` MCP server |
| Merge a pull request, delete a branch, or discard an edit | No; the user does these ([Stricter than #34](#stricter-than-34)) | — |
| Register an approved change set | Yes, single-player, after the user merged | `register-approved-change-set`, which reads the merge commit through the SCM |
| See and load skills | Toolkit skills only, in the model's skill list and in a load | The harness's skill mechanism |
| Subagents, web fetch, web search, other MCP servers | No | — |
| Credentials | None held. Credential files are not readable, and a remote URL that carries one is not printed ([Credentials](#credentials)). A local harness does not isolate the user's own credentials from the user's own machine. | — |

In an existing session, the conversation so far is context, not state:
the resume steps read authoritative state whatever it says
([resuming a session][ux-resume]). A turn outside the role has no
governed tools, so choosing the wrong agent cannot mutate governed
state.

The in-project read denies have different reasons; reads outside the
project are refused by guard rule 5. Credential files hold
credentials: `.env` files; `.netrc`, `.npmrc`, `.pypirc`, and
`.git-credentials`; the directories `.ssh/`, `.gnupg/`, `.aws/`,
`.kube/`, and `.docker/`; private keys and keystores, that is `*.pem`,
`*.key`, `*.p12`, `*.pfx`, `*.jks`, and `id_*` files; and any file
whose name contains `credential` or `secret`. Guard rule 5 refuses a
read of any of them. That list is the guard's floor, not a promise
that a project holds no token elsewhere; a binding's native copy may
deny fewer, and the guard carries the rest. `.git/` is denied because
the role reads Git state through the SCM's `repo_state`, never from
files, and `.git/config` can hold a remote URL with a credential.
`.protobot/` is denied, except `project.yaml`, because callers never
parse the store
([`ears-manager` CLI](../../architecture.md#ears-manager-cli));
`project.yaml` is configuration that #34 lets the Drafting Table read,
and #30 keeps it free of credentials. An `ears-manager` read can still
return a store record's content, so that deny is a contract rule, not a
secrecy boundary.

### Governed operations

The role reaches governed state through three routes, all named by the
manifest:

- **`ears-manager` is a CLI**
  ([`ears-manager` CLI Integration Contract](../ears-manager-cli.md)),
  and the role runs it through the harness's shell tool. Every call
  carries `--output json` before the command, so the
  result is one JSON envelope that the shell tool returns unchanged,
  and a non-zero exit carries the CLI's own diagnostic envelope with
  its status, 2 to 70. Field values such as a requirement text travel
  as quoted option values, which the guard parses as data. Long input
  travels on standard input in a quoted here-document where #30
  provides for it: the artifact content of
  `artifact put --content-stdin` and the impact file of
  `change-set update --impact-file -`. The Toolkit skill
  `drafting-specifications` teaches the grammar and points at
  `--help`. No wrapper, tool schema, or harness-specific code exists
  for it.
- **`wms` is an MCP server**, because the WMS Adapter is a network
  service, and in single-player mode a local process over MCP stdio
  ([WMS Adapter API](../../architecture.md#wms-adapter-api)). Its tools
  carry the operations and result shapes of #31, one tool per
  operation. Each harness adds its own prefix, for example
  `wms_<normalized-operation>` in OpenCode and
  `mcp__wms__<normalized-operation>` in Claude Code, and the binding's prompt
  states that mapping. MCP tool schemas
  are the specification approach the Architecture names for the Toolkit
  ([Interface Specification Approach][interface-approach]), and
  OpenCode, Claude Code, and Codex all load MCP servers. The `wms`
  server also runs the caller-side preflight of #32, so an early
  diagnostic reaches the agent as a tool result and no harness loads a
  rule library; the WMS write boundary stays authoritative. The
  Drafting Table never transitions a work item itself
  ([Registration](../git-integration.md#registration)).
- **`scm` is an MCP server**, the Drafting Table face of the
  [Source Control Manager][scm]. It is a local process over MCP stdio in
  every mode that uses a binding, because the working tree is local, so
  it needs no MCP authentication. It speaks the stateless MCP revision
  2026-07-28, and still serves a harness client that speaks only the
  legacy revision 2025-11-25 ([MCP protocol][scm-mcp]), so every
  bound harness can load it. Its tools carry the SCM's operations and
  result envelope: `repo_state`, `branch_init`, `branch_resume`,
  `commit`, `publish`, and `refresh`. Each takes the change set of the
  current branch, or a change-set ID, and never a ref, a path, a
  remote, or a message, except the checked prefix and default branch
  of `branch_init` and an optional commit body. The SCM derives the
  rest and renders the commit message and the pull-request body
  itself. The binding's prompt maps the tools to `scm_<operation>` or
  `mcp__scm__<operation>`, as for `wms`.

This contract takes three things from #30, and settles a fourth that
the CLI contract leaves open:

- **Long input on standard input.** `artifact put --content-stdin` and
  `change-set update --impact-file -` read from standard input, so the
  role needs no file-writing tool for a Vision document or an impact
  file. `--content-file` and `--impact-file` also accept a path, which
  may lie outside the project; the guard allows only `-`, because a
  path there would let the role copy any readable file, a credential
  file included, into a registered artifact.
- **One JSON envelope per call.** With `--output json`, success and
  failure alike print one document and nothing else on standard
  output, so a result can be replayed byte for byte. The recording
  stub in the [fixture](#fixture-session) answers with the envelopes
  of #30's golden fixture.
- **The branch cut.** In the target #30 contract, `change-set create`
  cuts and checks out the change-set branch, and a failed creation leaves
  neither a manifest nor a branch, so the role never creates a branch
  itself. EM-04 first release writes the manifest but does not create or
  check out a branch; branch creation and reuse are follow-on Git
  integration ([`ears-manager` CLI first-release
  scope](../ears-manager-cli.md#em-04-first-release-scope)). The target
  initialization branch that #34 needs before `project init` is cut by
  the SCM's `branch_init`.
- **A read of the project fields.** No #30 read command returns the
  Git-facing fields of `project.yaml`, such as
  `repository.canonical_remote`, `repository.default_branch`, and
  `repository.branch_prefix`
  ([Repository fields](../git-integration.md#repository-fields)), or
  its `stores` block
  ([ADR-0003](../../decisions/0003-ears-manager-storage-layout.md)).
  `project init` returns them once, at initialization. #34 lets the
  Drafting Table read that file and never edit it, and #30 rejects a
  credential in it. So the role and the guard read
  `.protobot/project.yaml` directly, the role reads nothing else under
  `.protobot/`, and the registered artifact paths come from
  `artifact list`. The SCM's `repo_state` also returns the Git-facing
  fields.

#### The first consumer: `eliciting-requirements`

| Need of the skill | Met by |
| --- | --- |
| Loaded by name | The manifest's `toolkit_skills` |
| Reads `references/ears-and-review.md` and `references/quality-guidance.md` on demand | The role's read access to the skill directory |
| Host metadata to preserve: IDs, tags, trace links | The session skill passes records it read through `ears-manager` |
| No file output in normal use | Nothing; the package stays in the conversation |
| Usable without ProtoBot | No adapter file is needed to load the skill in any harness |

#### Host mapping

The skill leaves approval, persistence, identifiers, and lifecycle to
its host. In ProtoBot the host is `drafting-specifications`, and the
full mapping is Toolkit content of that skill, written by #77. The rows
below are the ones the fixture asserts:

| Elicitation package content | ProtoBot decision | Governed write |
| --- | --- | --- |
| A candidate with status `ready for review` that the user accepts | A proposed requirement in the change set: `Template:` is the record `type` ([ADR-0002][adr2-pattern]), `Requirement:` is the `text`, `Affected interfaces` is `applies_to.interfaces` | `ears-manager requirement add`, or `requirement update` for a revision |
| A candidate with status `needs clarification` or `candidate`, or `Template: unresolved` | Never written. Its open question stays a gap ([gap surfacing][ux-gaps]). | None |
| `Observable at the named boundary alone: yes` or `no` with a rationale | `verification.mode: isolated-interface`, or `implementation-aware` with that rationale ([ADR-0002][adr2-verification]) | Same call |
| A suggested supporting requirement that the user accepts | `provenance: agent-suggested`; its label, such as Required companion, is not a `relationships` entry ([ADR-0002][adr2-relationships]) | Same call |

### Shell operations

`ears-manager` runs through the harness's own shell tool, as the
strawman states ([OpenCode-plus-skill strawman][strawman]). Git and the
Git host do not. The role reaches them only through the `scm` tools of
the [Source Control Manager][scm], which derive every ref, path,
message, and pull request from the change set. The Drafting Table role
may run only the shell operations below, and only in the forms shown.
They are the `ears-manager` commands of #30, the clock, and
registration.

The guard fills every placeholder from the working tree and the project
fields, never from the command, the prompt, or the conversation:

| Placeholder | Value |
| --- | --- |
| `<default>` | `repository.default_branch` |
| `<prefix>` | `repository.branch_prefix` |
| `<branch>` | The current branch, read with `git rev-parse --abbrev-ref HEAD`. It must have the form `<prefix><nnnnn>-<slug>`, where `<nnnnn>` is a change set in the store. |
| `<nnnnn>` | The five-digit number of a change set that `ears-manager --output json change-set list` returns for the current checkout. `<slug>` is not bound: #34 derives it from the intent when the branch is cut, the intent can change afterwards through `change-set update`, and a tool reads the prefix, not the slug ([One branch per change set](../git-integration.md#one-branch-per-change-set)) |

| Operation | Command forms | Constraint |
| --- | --- | --- |
| Read specifications | `ears-manager --output json <read>`, where `<read>` is `<group> list`, `<group> show`, `artifact get`, `change-set compare`, `check`, or `impact` with #30's options; and `ears-manager <any> --help` and `ears-manager --version` | Read-only |
| Write specifications | `ears-manager --output json <write>`, where `<write>` is `artifact put`, `interface add` or `update`, `requirement add`, `update`, or `retire`, or `change-set create` or `update` with #30's options; `artifact put --content-stdin` and `change-set update --impact-file -` take their input on standard input in a quoted here-document | #30's grammar; `--content-file` and `--impact-file` only with `-` |
| Initialize a project | `ears-manager --output json project init` with #30's options, after the `scm` tool `branch_init` cut `<prefix>00001-project-init` | Only while `.protobot/` does not exist ([guard rule 1](#guard-rules)); #34's initialization order needs the branch before `project init` |
| Read the clock | `date -u +%Y-%m-%dT%H:%M:%SZ` | Read-only; the value of every `--created` option that #30 requires |
| Register an approved change set | `register-approved-change-set --change-set CS-<nnnnn>`, where `<nnnnn>` is the change set of `<branch>`. The command reads the merge commit through the SCM's [`approved_merge`][scm-read], and derives the materialization key and the distinct registration idempotency key from `project.id` and those two values using #34's canonical tuple | Single-player, after the user merged the pull request |

The guard parses the command as shell words into an argument list and
a standard-input text, and matches the list against the forms; it
never matches the command string. Every command is one simple command
in one of these forms, with the options in the order shown and nothing
more. For `ears-manager`, the form is `--output json`, then the
command, its subcommand, and #30's options for it, except
`<any> --help` and `--version`, which take no other option; the guard
takes the option list of each command from #30, refuses
`--content-file` and `--impact-file` with any value but `-`, and
refuses a `--path`, `--vision`, or `--architecture` value under
`.agents/`, `.claude/`, `.codex/`, `.opencode/`, `.github/`, or `.git/`,
or equal to `opencode.json`, `AGENTS.md`, or `CLAUDE.md`, because those
are adapter, binding, and instruction files, never artifacts. The guard
refuses:

- an option or argument that the form does not show;
- an environment assignment before the command;
- output redirection, command substitution, and variable expansion
  outside a quoted here-document body;
- a command that does not parse to exactly one simple command, so a
  quoted here-document whose body holds its own delimiter line ends
  early, and the second command that follows is refused; and
- any other command. Every `git` and `gh` command is among them, and
  so is `source-control-manager`, so a push to the default branch
  cannot even be asked for in the shell, as the
  [repository fixture](../git-integration.md#repository-fixture)
  requires. The `scm` tools refuse it too.

Three properties follow:

- **Targets come from state, not from the caller.** The SCM derives
  every Git target from the working tree, the project fields, and the
  change set, and the guard binds registration's `<nnnnn>` to the
  current branch, in the same way that #34 takes the project identity
  from the working tree
  ([The project root](../git-integration.md#the-project-root)) and the
  Gate binds allowed refs in hosted modes
  ([Authentication and Credential Isolation][credential-isolation]).
  The role therefore cannot push another change set's branch, switch
  to a branch that no change set owns, edit another pull request, open
  a pull request in another repository, or stage a file outside the
  current change set.
- **Messages, bodies, and specification text are data.** An artifact's
  content and an impact file travel in a quoted here-document, and a
  requirement text travels as a quoted option value; the guard parses
  both as data, so no text becomes shell syntax. The commit message
  and the pull-request body are rendered by the SCM from `ears-manager`
  output. The role writes at most the optional commit body, as a tool
  field. A harness whose native patterns match here-document text may
  refuse a text that contains a forbidden option; reword it.
- **The Git host client belongs to the SCM.** The SCM's
  [host adapter][scm-host] drives `gh` on GitHub, for every harness.
  The role runs no `gh` command: `gh pr merge`, `gh api`, `gh auth`,
  and every other `gh` command are refused in its shell, as every Git
  command is. A failed host request returns a stable SCM code, which
  matches the row "Pull-request creation failed" of #34's
  [failure table](../git-integration.md#failure-behavior).

A binding may copy these operations into its harness's native command
rules as an early layer, with a wildcard where a form takes a value.
The guard always binds the real values. The binding also allows the
`scm` tools that the manifest lists, in the role only.

A harness sandbox may refuse a shell operation before the guard's
answer matters. In the target #30 contract, the branch-cutting form of
`ears-manager change-set create` writes `.git/` and a sandbox that keeps
it read-only may refuse it. The EM-04 first-release command writes only
the manifest, so `.git/` read-only does not itself refuse that command;
it also does not establish the branch required by the target workflow
(see the [`ears-manager` CLI first-release
scope](../ears-manager-cli.md#em-04-first-release-scope)). Registration
still needs the network and is refused in Codex. The role reports any
refusal and names the command for the user to run, as for a discard and
a merge ([Stricter than #34](#stricter-than-34)). The binding document
says which operations that covers. The `scm` server runs as its own
process, outside a tool sandbox, so its tools are not refused there.

#### Stricter than #34

The [Permitted Git operations](../git-integration.md#permitted-git-operations)
of #34 are the most that the Drafting Table may do. Three entries of
that list exist for this contract: `remote`, for listing only, among the
reads, switching to an existing change-set branch on resume, and the
abort of a conflicted merge. The SCM's Drafting Table face covers them:
`repo_state` resolves the remote itself, `branch_resume` switches, and
`refresh` aborts a conflicted merge. The face leaves out five of #34's
operations, and one route that #34 offers, because no Drafting Table
step needs them, each one reaches past the current change set, or #30
performs them:

| #34 names | Drafting Table role | Why |
| --- | --- | --- |
| Reading state with `log`, `diff`, `show`, and `ls-files` | Not an operation; `repo_state` reports what a step needs | No step needs them. `ears-manager change-set compare` shows the change, and `show` and `diff` print store files under `.protobot/`, which the role does not read. |
| Creating a change-set branch | Not an operation, except `cs/00001-project-init`, which the SCM's `branch_init` cuts | Target #30 behavior: `ears-manager change-set create` cuts and checks out the branch. EM-04 first release only writes the manifest and defers branch creation and reuse (see the [`ears-manager` CLI first-release scope](../ears-manager-cli.md#em-04-first-release-scope)). Initialization is the target case where the branch must exist first, and guard rule 1 lets the role run `project init` on it before `.protobot/` exists. |
| Amending an unpushed commit on explicit request | Refused. `commit` has no amend | History stays append-only, the rule for every pushed commit, so no second rule is needed for an unpushed one. |
| Merging one's own pull request, in single-player mode | Refused. The user merges on the Git host, then asks the role to register. | The merge is the approval event ([Registration](../git-integration.md#registration)), so a person makes it, never an agent tool call ([Compliance](../components.md#compliance-ess--aia)). |
| Deleting a merged change-set branch | Refused. The host deletes merged head branches, or the user does. | Drafting needs no deleted ref, and a wrong delete can remove a colleague's branch. |
| Discarding a direct edit, the route #34 offers after a digest mismatch | Refused. The SCM's diagnostic names `git checkout -- <path>`, and the user runs it. | A discard destroys text that the user wrote. |

### The guard

`drafting-table-guard` is the only adapter code shared by every harness.
It is one executable with no runtime dependency, like `ears-manager`.
Each binding wires it into the tool-call path. Guard availability is a
per-call fact: a hook or plugin being installed, trusted, or passing a
startup probe does not prove that the guard returned a decision for a
particular call. Each binding documents whether its harness blocks or
passes a call when that invocation fails or does not run.

#### Guard input and output

- **Input.** JSON on standard input, in the `PreToolUse` hook shape that
  Claude Code and Codex already send: `hook_event_name`, `session_id`,
  `cwd`, `tool_name`, and `tool_input`. The binding adds two arguments:
  `--harness <name>` and `--role drafting-table` or `--role other`. A
  harness without that hook shape, such as OpenCode, gets a binding
  shim that builds the same JSON. The role argument is a claim the
  binding makes from its own launch; a `PROTOBOT_ROLE` exported in the
  user's own shell turns any session into the role for the guard,
  which then applies the role's refusals without the role's native
  layer. That is the user's own machine and configuration; each
  binding names its role signal and its gap.
- **Allow.** Exit status 0 with no output. The harness's own rules then
  decide.
- **Refuse.** Exit status 2 and one line on standard error that names
  the rule and the governed route, for example
  `ears-manager artifact put`. Claude Code and Codex both block a tool
  call on status 2 and show the line to the model, so their bindings
  need only a shim that adds the two arguments and exits 2 when the
  guard is not on `PATH`.
- **Fail closed.** The guard exits with status 2 on any internal error,
  and always writes its line, because some harnesses treat other
  non-zero statuses, or a refusal without a reason, as a pass. It
  bounds its own `ears-manager` and Git subprocesses with a timeout
  shorter than any harness's hook timeout and exits 2 when one
  expires. A guard process that the harness kills or times out yields
  no status 2, and each binding records what its harness does then.

#### Tool vocabulary

The guard carries one vocabulary row per harness. A row lists which
tool names write files and where their target paths are, which tool runs
shell commands and where the command is, which tools read or search
files and where their path is, which tool loads a skill, and how MCP
tool names are formed. It is data, not logic. Adding a harness adds a
row and its test vectors. Under the Drafting Table role, a tool name
that the row does not list is refused. A search tool is a read of
every file below its path, so rule 5 applies to that path: a search
whose path is the project root, or a directory that holds a denied
path anywhere below it, is refused, because the guard cannot exclude
one file from a harness's search, and a search below a directory that
holds none passes. The Codex read forms follow the same rule.

A harness whose model reads files only through its shell, such as
Codex, has no read tool to list. Its row lists read forms instead: a
few read-only shell command forms, such as `cat <path>`, that count as
reads, and the path form that counts as a skill load. The binding
document names them, and guard rules 5 and 6 treat them as reads.

#### Guard rules

1. **Find the project.** The guard looks for `.protobot/project.yaml`
   at the root of the Git working tree that contains `cwd`, by the rule
   in [The project root](../git-integration.md#the-project-root). Without
   a `.protobot/` directory there, the directory is not a ProtoBot
   project: outside the Drafting Table role the guard allows
   everything, and under the role it still applies rules 5 and 6, with
   `ears-manager project init`, `ears-manager <any> --help`,
   `ears-manager --version`, and the clock as the only shell
   operations. The `scm` tools work there too (rule 4), so the role can
   read state and cut the initialization branch. `project init` is the
   one case where `<prefix>` and `<default>` come from the command,
   because no project records them yet; the guard checks that the
   prefix names the branch it is on, `<prefix>00001-project-init`,
   which `branch_init` cut. A different default branch fails at the
   first `publish`. With the directory but no readable
   `project.yaml`, the project counts as found and the read in rule 2
   as failed, so a malformed project fails closed.
2. **Ask `ears-manager`, and read `project.yaml`.** For each decision
   it reads the registered artifact paths through `artifact list`, and
   the `stores` paths and the Git-facing fields from
   `.protobot/project.yaml` itself, which #34 lets the Drafting Table
   read; it parses no other file under `.protobot/`
   ([`ears-manager` CLI](../../architecture.md#ears-manager-cli)). If
   either read fails, it refuses every file write and every shell command in
   the Drafting Table role, refuses a write under `.protobot/` in every
   role, and names the failure. Other writes outside the role are
   allowed, because the harness layer is optional. Unauthorized
   persistent edits to registered paths remain subject to downstream
   checks ([What the harness layer stops][layer-stops]); those checks
   cannot establish the source of an `ears-manager` file-source input.
3. **Guarded paths, every role.** A file write under `.protobot/`, to
   a registered artifact path, or below a store is refused. Paths are
   compared after symlink resolution, for reads and writes alike, a
   registered directory or a store guards everything below it, and a
   write whose target path cannot be read is refused.
4. **Governed operations.** A write command of a program in
   `governed_commands`, such as `ears-manager requirement add`, and a
   tool of a server in `governed_mcp_servers` are refused outside the
   Drafting Table role. A read command, such as
   `ears-manager requirement list`, is allowed in every role. Inside
   the role, only the tools that the manifest lists for a governed
   server are allowed; every other tool of that server, a lifecycle
   transition among them, and every tool of any other server is
   refused. The WMS boundary rejects such a transition in any case
   ([Validation Rules](../validation-rules.md), case VR-006), so this
   rule is the early copy of that refusal. `scm branch_init` is the only
   route for cutting the initialization branch, and its contract permits
   it only while `.protobot/` is absent at the working-tree root
   ([SCM `branch_init` precondition](../source-control-manager.md#branch_init)).
5. **The role's tool set.** Under the Drafting Table role, the guard
   refuses every file write, subagent launch, web fetch, and web search,
   every read under `.protobot/` other than `project.yaml`, every read
   under `.git/` or of a credential file, every read or search whose
   resolved path is outside the project root except below a user-scope
   Toolkit skill root, and every skill load not listed in
   `toolkit_skills`. A read of `<skill root>/<name>/SKILL.md`, or of a
   file below it, counts as a load of `<name>` in every harness. The
   outside-the-project refusal is what keeps the `gh` store and the
   harness's own credential file out of reach.
6. **The role's shell commands.** Under the Drafting Table role, the
   guard first recognizes the initialization-branch shape for its
   lifetime check:
   `git switch -c <prefix>00001-project-init <default>`. If
   `.protobot/` already exists, it refuses that form with the reason
   `Project already initialized` before applying the generic
   shell-operation check. If `.protobot/` is absent, it falls through to
   that check: raw Git remains outside the allowed shell operations. A
   command that is neither one of the [shell operations](#shell-operations)
   in its exact form nor a read form of the harness's
   [vocabulary row](#tool-vocabulary) is refused. The guard reads the
   current branch with `git rev-parse --abbrev-ref HEAD`, the project
   fields from `project.yaml`, and the change sets through `ears-manager`,
   and refuses a registration whose `<nnnnn>` is not the change set of
   `<branch>`.
7. **Other roles' shell commands.** A command whose output redirection
   targets a guarded path written from the project root, such as
   `> docs/vision.md`, is refused. A path in any other position is not
   a write, so `echo "see docs/vision.md" > notes.txt` is allowed.

The guard runs no Git command that writes, opens no network connection,
writes no file, and does nothing when a session is idle or ends.

#### Guard test vectors

The adapter core ships test vectors: a JSON input, a harness, a role,
the expected exit status, and a fragment of the expected reason. The
vectors run against the guard directly, and again through each
binding's hook, in [the fixture](#guard-vectors). Two vectors expect
an allow in a clone without `.protobot/`: the `scm` tool `branch_init`
under the role, and `sed -i` on a file outside the role, because the
directory is not yet a ProtoBot project. A third expects a refusal
there: `true` under the role, so the Codex probe holds before
initialization too.

---

## Session entry and resume

Every binding provides the same session behavior:

- **One entry point named `drafting-table`.** Depending on the harness
  it is a command, an agent, a profile, or a launcher. It gives the turn the
  Drafting Table role and loads the `drafting-specifications` skill.
  Its name differs from every Toolkit skill name, because harnesses can
  also invoke a skill by name as a command.
- **Resume on every start.** The resume steps of the session skill run
  on every entry, on a continued session, and after compaction. They
  read the project, the change-set branch, and blocked work through
  governed tools and shell operations only
  ([resuming a session][ux-resume]).
- **One recording notice.** The start summary tells the user once that
  the harness records the session on this machine. The UX contract left
  that decision to #33 ([draft conversation state][ux-draft]).
- **No action on idle or exit.** No binding commits, pushes, registers,
  or cleans up when a session is idle or ends.
- **No session upload.** A binding turns off any harness feature that
  uploads or shares a session, because a session carries unapproved
  specification text and pasted IdeaBot material.
- **No permission prompt inside the governed path.** A binding's rules
  allow or refuse. A headless run then behaves like an interactive one,
  and the fixture can replay it.

```mermaid
sequenceDiagram
    actor User
    participant H as Harness session
    participant DT as Drafting Table role
    participant G as Guard
    participant EM as ears-manager (shell)
    participant WMS as wms tools
    participant SCM as scm tools (Git, gh)

    User->>H: drafting-table entry point, with intent
    H->>DT: Turn in the Drafting Table role
    DT->>H: Load drafting-specifications
    DT->>SCM: repo_state
    DT->>EM: Read the active change set
    DT->>WMS: Query blocked work items
    DT-->>User: Start summary and recording notice
    DT->>H: Load eliciting-requirements
    DT-->>User: Elicitation package (draft)
    User->>DT: Accept a candidate
    DT->>EM: requirement add
    User->>DT: Commit and open a pull request
    DT->>SCM: commit, publish
    Note over H,G: The binding invokes the guard on its hook path; failure behavior is harness-specific
```

---

## What the harness layer stops

The harness layer is the optional early layer of the
[Governed tool integrations](../../architecture.md#governed-tool-integrations).
The mandatory layers stay where #34 puts them
([Ungoverned-edit detection](../git-integration.md#ungoverned-edit-detection)),
so a harness whose binding is weaker normally changes how early an
unauthorized persistent edit to a registered path is caught, not whether
that edit is caught when it reaches the SCM or CI checks. Those later
layers do not replace the guard's command parser, option/value checks,
read restrictions, or shell-syntax restrictions. A call that reaches an
allowed shell tool without a guard decision can therefore bypass any
guard-only check, including variable-expansion and redirection checks;
the later layers check persistent state, not the command or its inputs.
The file-source provenance gap is described below.

The guard entries below describe calls on which the guard runs. A
binding's hook or plugin being installed does not by itself prove that it
made a decision for a particular call; the failure cases are binding-
specific and are not refusals.

| Route to guarded state | Drafting Table role | Every other role | Caught later by |
| --- | --- | --- | --- |
| File-writing tool | Refused by the guard; hidden by native rules where the harness can hide tools | Refused by the guard | Pre-stage digest comparison, `ears-manager check`, CI path ownership, for persistent edits to registered paths |
| Shell writer, such as `sed -i`, `cp`, or `tee` | Refused by the guard | Not stopped | Same, for persistent edits to registered paths |
| Output redirection in a shell command | Refused by the guard | Refused by the guard when the redirection target is written from the project root | Same, for persistent edits to registered paths |
| `ears-manager --content-file` or `--impact-file` with a value other than `-` | Refused when the guard runs; binding-specific hook failures may pass the call through | Same | None: integrity and CI cannot establish whether the bytes came from standard input or an external file |
| Tool of a non-governed MCP server | Refused by the guard | The user's own configuration | No tool-call check; only unauthorized persistent edits to registered paths are caught if they reach SCM or CI |
| Subagent | Refused by the guard | Not applicable | No launch check; only unauthorized persistent edits to registered paths are caught if they reach SCM or CI |

A route marked "not stopped" is real. An agent outside the role can
still change a registered file through its shell, for example after
`cd docs`. The pre-stage digest comparison refuses to stage the change,
`ears-manager check` fails in CI, and the Drafting Table offers the two
routes forward from [The pre-stage digest comparison][pre-stage].

A user can also switch the harness layer off, by editing a binding or
starting the harness without hooks. Unauthorized persistent edits to
registered paths remain subject to the SCM's pre-stage checks and the
repository's `ears-manager check` and CI path-ownership checks. This does
not make the rest of the shell path safe without the guard: those later
checks do not enforce its command grammar, shell-syntax, read, or
argument-value rules. File-source values are one concrete example: their
restriction is only enforced when the guard runs or a binding has an
equivalent native value-level rule. A hook that fails open can let the
CLI read a non-`-` source, and no later layer detects that provenance
loss.

### File-source arguments

`--content-file` and `--impact-file` are caller-owned read sources, not
project destinations. #30 permits regular-file sources outside the project
root as well as `-` for standard input
([CLI file inputs](../ears-manager-cli.md)). Their contents can therefore
be copied into authoritative specification state without changing the
destination path: an artifact for `--content-file`, or the change-set
impact record for `--impact-file`. The later integrity and CI checks do
not establish that the bytes came from standard input rather than an
external source.

On every call for which it returns a decision, the shared guard rejects
non-`-` values for these options. A binding may provide the same check as a
native value-level restriction. The check is per call, not per process or
session: an installed hook, a passing startup probe, or an available guard
binary is not sufficient evidence that a particular invocation was
checked.

The shared refusal guarantee has an explicit fail-open exception where a
harness may run the tool without a guard decision. In that case the native
rules may still admit the shell command, `ears-manager` may read the
external source, and the later integrity and CI layers do not catch it.
These are binding gaps, not protected behavior or successful H8
enforcement:

| Binding | Guard-unavailable case | Admitted without a guard decision | Still blocked by native rules or the sandbox |
| --- | --- | --- | --- |
| [OpenCode](opencode.md) | The plugin is absent or no hook is registered; native Bash permissions have no plugin-presence check | The `ears-manager *` allow rule still admits the command, including non-`-` file-source values, `--text "$GH_TOKEN"`, output redirection, and other shell syntax that only the guard rejects; a `read` of an in-project credential file other than `.env` | Every other shell command, including `git` and `gh` (`"*": deny`); reads of `.env`, `.git/`, and `.protobot/` stores; hidden file-writing, subagent, and web tools |
| [Claude Code](claude-code.md) | The hook times out, is killed, or its shim cannot run | No status 2 is returned, so an `ears-manager` call may proceed with a non-`-` value, such as a credential file, or with variable expansion such as `--text "$GH_TOKEN"`; a `Read` of a credential file outside the native denies, including one outside the project | Every shell command without an allow rule, including `git` and `gh` (`dontAsk`); reads of `.env`, `.git/`, and `.protobot/` stores; hidden file-writing, subagent, and web tools |
| [Codex](codex.md) | The hook is untrusted outside the launcher, crashes, exits other than 2, or times out | The call may proceed with a non-`-` value or variable expansion; any shell command, including Git reads; an `apply_patch` write in the working tree, including `.protobot/` and registered paths; the sandbox bounds writes, not reads, so a host credential file can reach the model or governed state, and the sandbox does not establish the source of bytes read into governed state | Writes outside the working tree and `$TMPDIR`, writes under `.git/`, and shell network access, so Git writes and Git host calls fail; hidden web and subagent tools |

The binding status and fixture must keep these failure modes visible. A
call with no guard decision is not a refusal, and a fixture that exercises
only the successful hook path does not establish fail-closed behavior.

---

## Repository and credential capabilities

### Credentials

- **No binding file holds a credential.** Binding config names servers
  and commands, not tokens, in the same way that `project.yaml` never
  holds one ([Repository fields](../git-integration.md#repository-fields)).
- **A local harness uses the user's own credentials, in every mode.**
  The SCM runs Git and `gh`, which use the user's credential helper and
  `gh`'s own store, whether the project's `review_mode` is
  single-player or multi-player. On every call that receives a guard
  decision, the Drafting Table role runs neither and cannot print a
  credential: environment and file-printing commands are not shell
  operations, credential-file reads, reads under `.git/`, and reads
  outside the project are refused, and variable expansion is refused.
  Independently of the guard, the SCM prints no remote URL and refuses
  a canonical remote whose URL carries userinfo other than the fixed
  `git@` of the SCP form, as `ears-manager` accepts it. A call that a
  binding passes through without a guard decision loses the guard's
  refusals; each binding's resulting credential exposure is listed
  under [File-source arguments](#file-source-arguments).
- **The limit of a local harness.** The agent runs as the user, on the
  user's machine. The adapter narrows what the Drafting Table role can
  reach; it does not isolate a token from the user's own shell or from
  another agent. Credential isolation by the Bridge/Gate pattern is a
  property of hosted runtimes
  ([Environmental Constraints][env-constraints]). A laptop in a
  multi-player project is therefore a recorded deviation from that
  constraint. Git's credential helper and `gh`'s store supply the token
  to those programs, so on guard-checked calls the model never sees it
  and the role cannot read or print it; a fail-open call can expose it
  as that binding's row under
  [File-source arguments](#file-source-arguments) states. In either
  case no Bridge or Gate stands between the harness process and the
  token. #34's
  [ceremony table](../git-integration.md#ceremony-in-each-mode) records
  both cases.
- **The `wms` server.** In single-player mode it is a local process
  that obtains the user's own Git host token itself, as the
  [WMS Adapter API](../../architecture.md#wms-adapter-api) topology states.
  In multi-player mode it is remote: the harness's MCP client
  authenticates the user with OAuth 2.1, Red Hat SSO as the issuer, and
  the hosted WMS Adapter terminates the token and uses its own
  credentials downstream
  ([Authentication and Credential Isolation][credential-isolation]).
  The harness keeps that token in its own store outside the project,
  where the role's guard-checked reads cannot reach it (H13); a
  fail-open read outside the project can, in the bindings whose row
  under [File-source arguments](#file-source-arguments) admits one.
- **The harness's own model credentials** belong to the harness and its
  user. The adapter neither reads nor configures them.

### Untrusted input

IdeaBot material, repository files, and project instructions such as
`AGENTS.md`, which harnesses load for every agent, all enter the
agent's context. They can change what the agent says. On calls that
receive a guard decision, they cannot change what the agent can do,
because the guard and the native rules bound every effect
([Enforce constraints structurally][structural]). A call that a
binding passes through without a guard decision is bounded only by the
native rules and, in Codex, the sandbox; those cases are binding gaps
([File-source arguments](#file-source-arguments)).
The guard and the resume steps take the project identity from the
working tree only, never from a caller
([The project root](../git-integration.md#the-project-root)).

IdeaBot material enters as pasted text or as a file attached to the
user's prompt. On guard-checked calls the role does not read
IdeaBot files outside the project; a fail-open call is limited as
the paragraph above states. Nothing in the adapter depends on IdeaBot
input
([IdeaBot material](../git-integration.md#ideabot-material)).

---

## Deployment modes

| Mode | Harness bindings | `wms` server | `scm` server | Git host credential | Who merges |
| --- | --- | --- | --- | --- | --- |
| Single-player | Used, in any bound harness | Local, MCP over stdio | Local, MCP over stdio | The user's own token, held by Git and `gh`, used by the SCM | The author, on the Git host; the role then registers |
| Multi-player | Used by each contributor, each in the harness they choose | Remote, OAuth 2.1 (H13) | Local, MCP over stdio, because the working tree is local | The user's own token, held by Git and `gh`, used by the SCM, as for every local harness | A reviewer, on the Git host |
| Web | Not used. A hosted runtime loads the same Toolkit | Hosted | Hosted, behind the Gate ([SCM deployment topology][scm-deploy]) | Bridge/Gate | As #34 states |

Contributors to one project may use different harnesses at the same
time. The Toolkit, the manifest, the guard, and the Git rules are the
same for all of them, so the specification history they produce is the
same.

A binding runs on the user's machine and needs no cluster
([Vision — Intended users](../../vision.md#intended-users)). The
deployment-level registry of hosted modes decides which projects a user
may open and never supplies the project identity
([Persistent State](../../architecture.md#persistent-state)).

---

## Artifacts and traces

### Artifacts

| Artifact | Produced by | Where | Status |
| --- | --- | --- | --- |
| Registered specification artifacts and the change-set manifest | `ears-manager`, through the shell operations | Working tree on the change-set branch | Proposed; approved on merge |
| Commits, the pushed branch, the pull request | The SCM, through the `scm` tools | Project repository and Git host | #34 |
| Registration call | `register-approved-change-set`, with the merge commit from the SCM | Job Site intake | #34 |
| Resolutions of blocked work | `wms` tools | WMS backend | WMS Adapter |
| Elicitation packages | `eliciting-requirements` | The conversation only | Draft; never a file |
| Session record | The harness | The harness's own store on the user's machine | Non-authoritative trace source |
| Exported session | The binding's export route | Where the user writes it | Evaluation input |

The adapter produces no demonstration artifact and writes nothing under
`.protobot/attestations/`; those belong to the Job Site
([Job Site Handoff Boundary](../../architecture.md#job-site-handoff-boundary)).

### Traces

The Architecture requires replayable inputs, outputs, and decision
records for every agentic operation, and leaves the trace format open
([Evaluability](../components.md#evaluability)). In every harness the trace
source is the harness's own session record. The adapter adds no trace
store.

A usable session record holds the role, the model and provider, the
harness and its version, every message, and every tool call with its
arguments, status, and result or error text, including guard refusals.
The adapter places three more facts in it:

| Fact | How it enters the record |
| --- | --- |
| Adapter layout and binding | The entry point's prompt states `adapter_layout` and the binding name |
| Toolkit skill content | The skill-load result holds the loaded `SKILL.md`; read results hold its references |
| Project, branch, and base commit | The results of the resume reads |

- Each binding names its record and its export route, and states what
  the record lacks. The fixture captures anything missing, such as the
  resolved native rules, next to the export.
- The adapter cannot redact a harness's record, so it keeps
  credentials out of the role's own turns: every credential-file read,
  every read outside the project, and every read under `.git/` are
  refused, and no `scm` result holds a remote URL; the fixture
  plants a token-shaped string in the in-project places among those
  and asserts that no export holds it, and vectors cover the rest
  ([harness checks](#guard-vectors)). A turn outside
  the role in a continued conversation is bounded by the harness's own
  rules only, and its results sit in the same record. A full
  export still holds unapproved specification text, pasted material,
  and whatever the role read, and is handled as confidential to the
  project. A redacted export, where the harness offers one, shows the
  shape of a session without its content.
- The session record is harness state. It is not one of the six stores
  in [Persistent State](../../architecture.md#persistent-state), no
  component reads it to resume or decide, and a harness-neutral trace
  format remains an open question.

---

## Resumable state

A session resumes from authoritative state, never from the
conversation ([authoritative state][ux-auth]). The adapter keeps no
state of its own, so a session started in one harness can resume in
another. The conversation itself does not move between harnesses;
whether a Web implementation transfers it stays open
([Q6](../open-questions.md#q6-drafting-table-session-continuity)).

| State | Where it lives | Survives the end of a session | Read on resume through |
| --- | --- | --- | --- |
| Registered artifacts written by `ears-manager`, not yet committed | Working tree on the change-set branch | Yes | `ears-manager` reads |
| Commits on the change-set branch | Git | Yes | `repo_state` and `ears-manager` reads |
| Work-item and request state | WMS backend | Yes | `wms` tools |
| Proposals, open questions, elicitation packages | The conversation | Only as part of the session record | Not read; asked again |
| The harness conversation | The harness's session store | Yes, in that harness only | Only when the user continues that session, and never as authority |

Every entry resumes, a compaction included, as
[Session entry and resume](#session-entry-and-resume) states. When a
continued conversation and the working tree, Git, or WMS disagree, the
role presents the authoritative state and asks for review again
([stale state][ux-stale]). Nothing commits on exit, so uncommitted
`ears-manager` output is the next session's draft
([When a commit happens](../git-integration.md#when-a-commit-happens)).

A draft item that must survive a new session must be written through
`ears-manager`. The UX contract expects unresolved gaps to survive a
resume ([anti-leakage rule][ux-leak]), but the change-set manifest of
[ADR-0002][adr2-changeset] has no field for an unresolved gap. The
adapter keeps no store for one, so this contract depends on issues #28
and #30 giving unresolved gaps a governed home. Until they do, a new
session finds its gaps again by running `eliciting-requirements` on the
written records.

---

## Exit conditions

| Exit | Harness shows | Adapter behavior | State left behind |
| --- | --- | --- | --- |
| The user ends the session | The session closes | Nothing runs on exit: no commit, no push, no registration. The exit warning of #28 is the session skill's: it names uncommitted changes at every checkpoint and in the start summary, so a hard exit without a turn gets no warning | Uncommitted output stays in the working tree for the next session |
| A tool call is refused | A tool error with the guard's or the native rule's text | The role reports the refusal and stops that step | Unchanged |
| `ears-manager` is not on `PATH`, or the `wms` or `scm` server is not running | A failed shell command, or missing tools | Without `ears-manager`, no governed write is possible and drafting stops. Without `wms`, blocked work is marked unavailable and drafting continues ([unavailable WMS][ux-wms]). Without `scm`, drafting continues on the current branch; the start summary says that Git state is unavailable, and commit, publish, refresh, and resume to another branch wait until it runs. | Unchanged |
| The model or provider fails, or the context overflows | A session error | Handled as a failed governed call ([failure behavior][ux-failure]). No governed write is replayed automatically. | As the last diagnostic says |
| The user interrupts a governed call | The call is aborted | Its result is unknown | Read again before the next write |
| A permission prompt appears in a headless run | The harness rejects or stops | A defect in the binding, which has no prompt rules | Unchanged |
| The work is complete | The [approval handoff][ux-approval] is reached | Commit, push, and the pull request run on explicit request (#34), through the `scm` tools. Before `publish`, the role compares the `parent` and the `paths` that `commit` returned with the branch tip and the change-set paths that the final review showed, from `repo_state`, or, after a `refresh` in the same handoff, with the merge commit that `refresh` returned and the paths that the reviewed refresh sequence wrote; when they differ, it stops and asks for review again ([stale state][ux-stale], [SCM security posture](../source-control-manager.md#security-posture)). The user merges on the Git host and, in single-player mode, asks the role to register. | A committed branch and a pull request. Another change set starts another session. |

---

## Harness obligations

A harness can host the Drafting Table when its binding meets these
obligations. **Required** obligations make the binding usable at all.
**Enforcement** obligations form the early layer: a binding that cannot
meet one records the gap. Later layers check unauthorized persistent
edits to registered paths when they reach the SCM or CI, but cannot
establish file-source provenance when a hook fails open, as described
under [File-source arguments](#file-source-arguments).

| # | Obligation | Kind | Shared by the core | Added by the binding | Fixture |
| --- | --- | --- | --- | --- | --- |
| H1 | Discover Toolkit skills from `.agents/skills/` without changing them | Required | Skill location | Native discovery, or a link — never a copy | 1, 3 |
| H2 | Give the role the `ears-manager` CLI through its shell tool, and the `wms` and `scm` MCP tools | Required | `governed_commands`, `governed_mcp_servers` | Shell rules and MCP registration | 2, 5, 14 |
| H3 | Provide the `drafting-table` entry point, which gives the role and loads the session skill | Required | `entry_point`, `session_skill` | Command, agent, profile, or launcher | 2 |
| H4 | Run the resume steps on every entry, continued session, and compaction | Required | The session skill | The entry point's prompt | 2, 11, 12 |
| H5 | Do nothing when a session is idle or ends | Required | The guard has no exit action | No exit hook | 10 |
| H6 | Keep a replayable session record with the facts in [Traces](#traces) | Required | The facts | The record and its export route | 15 |
| H7 | Run headless with replayed model turns and no permission prompt | Required | The fixture steps | A replay mechanism and a headless command | All |
| H8 | Invoke the guard on each tool call and enforce its decision; document any per-call fail-open path | Enforcement | The guard | A hook, a plugin, or a shim, with harness-specific failure behavior | 6, 7, 14, vectors; fail-open cases as recorded gaps ([File-source arguments](#file-source-arguments)) |
| H9 | Hide file-writing, subagent, and web tools from the role | Enforcement | Guard rule 5 refuses them anyway | Native tool rules | 6 |
| H10 | Offer the role's model only Toolkit skills in its skill list, and let the role load only those | Enforcement | `toolkit_skills`, guard rule 5 refuses a load | Native rules that hide and refuse every other skill | 3, 4 |
| H11 | Hold no credential in binding files, and turn off session upload | Enforcement | — | Binding config | Vectors, harness checks |
| H12 | Publish the binding's status for each obligation | Required | [Binding status](#binding-status) | The binding document | — |
| H13 | Authenticate the role to a remote `wms` server with OAuth 2.1 | Required in multi-player | — | The harness's MCP client and its token store | None; the fixture is single-player |

### Binding status

| Harness | Binding | Status |
| --- | --- | --- |
| OpenCode | [OpenCode Harness Binding](opencode.md) | Designed against OpenCode 1.18.30, with skill discovery and visibility, tool hiding, rule order, pattern matching, the plugin hook, export, and the replay provider observed against a stub model; the fixture runs in #77, so no H1 to H11 obligation is marked met; H12 is met; H13 not checked |
| Claude Code | [Claude Code Harness Binding](claude-code.md) | Designed against Claude Code 2.1.273; the fixture has not run, so no H1 to H11 obligation is marked met; H12 is met |
| Codex | [Codex Harness Binding](codex.md) | Designed against Codex CLI 0.154.0, with skill visibility, tools, the profile, and the hook observed against a stub model; the fixture has not run, so no H1 to H11 obligation is marked met; H12 is met. The Codex sandbox stays on. EM-04 `change-set create` only writes a manifest and does not cut a branch; registration requires network, and target branch creation remains follow-on scope. Git and the Git host go through the `scm` server, which runs outside the sandbox |

---

## Adding a harness

1. Add the harness's row to the guard's
   [tool vocabulary](#tool-vocabulary), with test vectors for its tool
   names.
2. Write a binding document beside the [OpenCode](opencode.md),
   [Claude Code](claude-code.md), and [Codex](codex.md) bindings, and
   the binding files, using the manifest's values.
3. Wire the guard: a pre-tool hook, a plugin, or a shim that passes the
   harness name and the role.
4. Provide a headless run and a way to replay recorded model turns.
5. Run the [fixture session](#fixture-session), and check that every
   Toolkit and adapter-core file is byte-identical before and after.
6. Add a row to [Binding status](#binding-status), and add the binding
   document to the Related Documents lists.

A binding that can meet a required obligation only by editing a Toolkit
or adapter-core file fails the boundary for that obligation, and its
status names it.

### Known extension points

The table records what each harness offers for each obligation. The
OpenCode column is bound in [OpenCode Harness Binding](opencode.md)
and partly observed against a stub model on 2026-09-16; its fixture
runs in #77. The Claude Code column is bound
in [Claude Code Harness Binding](claude-code.md) from the published
documentation and CLI help of Claude Code 2.1.273, read on 2026-09-16,
and not yet checked by the fixture. The Codex column is bound in
[Codex Harness Binding](codex.md) from the documentation, CLI help, and
source of Codex CLI 0.154.0, partly observed against a stub model on
2026-09-16, and not yet checked by the fixture. "Open" marks what is not
yet known. Each binding verifies its column against the version it pins.

| Extension point | OpenCode | Claude Code | Codex |
| --- | --- | --- | --- |
| Skills in `.agents/skills/` (H1) | Read natively | Reads `.claude/skills/` only; ProtoBot links it to `.agents/skills/` | Read natively, walking up to the repository root; `~/.agents/skills/` at user scope |
| The same skill name found twice | Listed once | User scope hides project scope | Both entries listed |
| The `ears-manager` CLI (H2) | The `bash` tool | The `Bash` tool | The shell tool |
| The `wms` MCP server (H2) | `mcp` in `opencode.json`; tools named `<server>_<tool>` | `--mcp-config` with `--strict-mcp-config` at launch; tools named `mcp__<server>__<tool>` | `[mcp_servers.wms]` in the role profile; tools named `mcp__<server>__<tool>` |
| Drafting Table role (H3) | A primary agent file | An agent file, run as the main session with `--agent` | A profile, `$CODEX_HOME/<name>.config.toml`, started by the `drafting-table` launcher with `--profile`; agent roles are for subagents only |
| Enter the role from a running session (H3) | The `/drafting-table` command runs the turn on the agent | Not in-session; relaunch with `--continue --agent drafting-table`, which keeps the conversation | Not in-session; the launcher with `resume` continues the session |
| The `scm` MCP server, for Git and the Git host (H2) | `mcp` in `opencode.json`; tools named `scm_<tool>` | `--mcp-config` with `--strict-mcp-config` at launch; tools named `mcp__scm__<tool>` | `[mcp_servers.scm]` in the role profile; tools named `mcp__scm__<tool>`; the server runs as its own process, outside the tool sandbox, as Codex documents; the fixture has not confirmed it yet ([Codex open point 8](codex.md#open-points)). If it holds, Git writes and the host reach the role through it |
| Shell operations the sandbox refuses | None | None | Registration, which needs the network; the target branch-cutting form of `ears-manager change-set create` would also be refused because `.git/` is read-only. EM-04's manifest-only command is not refused for that reason and does not create a branch |
| Remote `wms` server with OAuth 2.1 (H13) | Open | An HTTP MCP server with OAuth through `/mcp`; candidate | A streamable HTTP MCP server with `codex mcp login`; candidate |
| Hide tools from the role (H9) | `"*": deny` in the agent | The agent's `tools` list and deny rules | `web_search = "disabled"` and `multi_agent = false`, observed; no setting hides `apply_patch` |
| Restrict skills (H10) | `permission.skill` with `"*": deny` first hides every other skill from the model's list and refuses it; observed | `skillOverrides` with `off` hides and refuses a named skill; `Skill(<name>)` rules never change the list; a skill in another user's scope cannot be named in advance; observed | `include_instructions = false` removes the skill catalog, and the profile names the Toolkit skills; `[[skills.config]]` hides a named skill; observed. No skill tool: the guard refuses a read of another `SKILL.md` |
| Invoke the guard (H8) | A plugin's `tool.execute.before` and a shim | A `PreToolUse` command hook; status 2 blocks and the reason reaches the model | A `PreToolUse` hook in `.codex/hooks.json`; status 2 blocks, observed; the hook needs trust, and an untrusted, crashing, or silent hook lets the call through; the launcher checks the hook file and the profile and starts Codex with `--dangerously-bypass-hook-trust`, so the hook runs, and the sandbox stays on |
| Role signal for the guard | The agent name from `chat.params` | `PROTOBOT_ROLE` from the role's settings `env`; `agent_type` in the hook input is undocumented | `PROTOBOT_ROLE` set at launch reaches the hook, observed; the input names no profile |
| Headless run (H7) | `opencode run --format json` | `claude -p --output-format stream-json --permission-prompts none` | `codex exec --json` |
| Continue a session (H4) | `--continue`, `--session` | `--continue`, `--resume` | `codex resume`, `codex exec resume` |
| Session record (H6) | `opencode export`, with `--sanitize` | The transcript JSONL under `~/.claude/projects/`, plus `stream-json` with `--include-hook-events` | The session file under `$CODEX_HOME/sessions/`, plus `--json`; hook events are not recorded |
| Model replay (H7) | A custom OpenAI-compatible provider | A base-URL override to a replay endpoint; candidate, unverified | A custom model provider with `wire_api = "responses"`; observed with a stub |

Four differences already shape the core. Claude Code and Codex share
the `PreToolUse` input shape and the status-2 convention, so the guard
adopts them and OpenCode gets a shim. Codex lists duplicate skill names
and Claude Code reads only `.claude/skills/`, so the Toolkit keeps
unique names in one place and bindings link, never copy. Codex has no
read tool and no skill tool, so a vocabulary row can list read forms
([Tool vocabulary](#tool-vocabulary)). Codex's sandbox keeps `.git/`
read-only, so the contract states what a role does when a sandbox
refuses a shell operation ([Shell operations](#shell-operations)).

---

## Toolkit skill rules

Every skill the Drafting Table loads:

1. Is a directory `<name>/SKILL.md` under `.agents/skills/`. A harness
   that reads another directory gets a link to it, never a copy.
2. Has a `name` that matches the directory and
   `^[a-z0-9]+(-[a-z0-9]+)*$`, is at most 64 characters long, and has a
   `description` of 1 to 1024 characters. These are the strictest limits
   among the known harnesses.
3. Has a name used by no other skill in any discovery location of any
   bound harness.
4. Relies on no frontmatter field beyond `name` and `description`.
5. Links to its references by relative path inside its own directory.
6. Names operations, never harness tools, as
   [The three layers](#the-three-layers) requires.
7. Assumes no file write, subagent, web access, or permission prompt.
8. Works without ProtoBot, as `eliciting-requirements` does, or names
   the host it needs, as `drafting-specifications` names `ears-manager`
   and the WMS Adapter.

---

## Fixture session

The fixture proves skill discovery, governed calls, exit conditions,
and resumable draft state in any bound harness. It needs no live model,
no Git host, and no WMS backend. Issue #77 runs it for the OpenCode
binding; every later binding runs the same steps, and no issue runs it
for the Claude Code or Codex binding yet. Where a step needs a harness command,
the binding document supplies it. Where the harness's sandbox refuses
a shell operation, the role reports the refusal, the fixture runs the
command outside the session, and the binding document says which
steps that covers.

Two sibling fixtures cover what this one does not. The UX contract's
[acceptance evidence][ux-evidence] replaces the harness with a
deterministic driver and asserts interaction semantics; this fixture
keeps the real harness and asserts the binding and the guard. Merge and
registration are covered by step 8 of #34's
[repository fixture](../git-integration.md#repository-fixture), and the
SCM's operations and refusals by the same fixture run against the SCM
([Repository fixture against the SCM][scm-fixture]).

### Setup

This fixture exercises the target combined Git/CLI workflow after branch
creation is integrated. Its recording stub models the target branch cut;
the EM-04 first-release CLI itself only writes the manifest (see the
[`ears-manager` CLI first-release
scope](../ears-manager-cli.md#em-04-first-release-scope)).

- A bare repository as `origin`, and one clone in the state after step
  8 of the [repository fixture](../git-integration.md#repository-fixture):
  `CS-00001` and `CS-00002` merged, with `docs/vision.md` and
  `docs/architecture.md` registered and committed. Then
  `ears-manager change-set create` creates `CS-00003`, cuts its branch
  `cs/00003-<slug>` from the default branch, checks it out, and records
  the default-branch head as `base_commit`, as step 2 of that fixture
  did for `CS-00002`. `repository.canonical_remote`
  is a GitHub-shaped URL that an `insteadOf` rule in the clone's Git
  config rewrites to the bare repository, so the repository has an
  owner and a name.
- The manifest, the Toolkit skills at a pinned commit in
  `.agents/skills/`, one maintenance skill `review-pr`, the guard, and
  the binding under test.
- `ears-manager` on `PATH`. Until its implementation exists, a
  recording stub with #30's command grammar answers with the envelopes
  of #30's [golden fixture](../ears-manager-cli.md#golden-fixture) and
  writes the files the real CLI would write.
- `source-control-manager` on `PATH`, which the binding starts as the
  `scm` server.
- A recording `wms` stub that reports one blocked work item. The
  fixture's manifest lists the tool names the stub serves.
- A recording `gh` stub on `PATH`, which the SCM's host adapter calls,
  because a bare repository has no pull-request API. #34's fixture
  stands in for the host in the same way.
- A token-shaped string, `PROTOBOT-FIXTURE-TOKEN`, planted where a
  credential could sit: in `.git/config` under `http.extraHeader`, in
  a `.netrc` at the project root, and in `docs/.env`, below a
  registered directory. No step reads it, and the harness checks
  assert that no export holds it. A remote URL with userinfo is a
  negative check of the SCM's own fixture
  ([Repository fixture against the SCM][scm-fixture]), because no
  shell operation and no `scm` result prints a remote URL.
- The binding's replay mechanism, which serves recorded model turns in
  order. The harness then runs its real skill loader, rules, hooks, MCP
  clients, and shell, so the fixture tests the binding and the guard,
  not model quality. The quality of `eliciting-requirements` is
  measured by its own evaluation (#63).

Each run keeps the harness's event stream, the session export, the
resolved native rules where the harness can print them, and the
repository state.

### Steps

| # | Action | Expected result |
| --- | --- | --- |
| 1 | List the skills the harness discovers, from a subdirectory of the clone | `drafting-specifications`, `eliciting-requirements`, and `review-pr` are listed from `.agents/skills/`. Discovery is not permission. |
| 2 | Start a session outside the role with any prompt, then continue it through the `drafting-table` entry point with an intent | The second turn runs in the role. It loads `drafting-specifications`, and its resume reads — `repo_state`, the change-set reads, and the blocked-work query — all complete before any governed write. The start summary names the blocked item and carries the recording notice. |
| 3 | A replayed turn loads `eliciting-requirements` and reads `references/ears-and-review.md` | Both calls complete. The skill text in the session record equals the pinned files. |
| 4 | A replayed turn loads `review-pr`, and another reads `.agents/skills/review-pr/SKILL.md` with the file tool or a read form | Both refused. The refusal text is in the record. The skill list that the harness sent to the model in the role names only the Toolkit skills; the replay endpoint or the binding's event stream shows it. |
| 5 | The user accepts a `ready for review` candidate, and a replayed turn runs `ears-manager --output json requirement add` with #30's options; another replayed turn writes a revised Vision through `ears-manager --output json artifact put` with the content on standard input, so `docs/vision.md` is a path of `CS-00003` | The stub records both calls. The options of the first follow the [host mapping](#host-mapping) and #30's record-mutation grammar. The record file exists, and `docs/vision.md` holds the revised content. `git status` lists only registered paths and the manifest. No commit exists. |
| 6 | In the role, replayed turns write a record under `.protobot/requirements/` with a file tool, run `sed -i` on `docs/vision.md`, and run `ears-manager --output json check > docs/vision.md` | All three are refused, by the guard or earlier by native rules. Every file is byte-identical. |
| 7 | Outside the role, replayed turns write `docs/vision.md` and edit `.protobot/change-sets/cs-00002.yaml` with file tools | Both are refused. Both files are unchanged. |
| 8 | The `ears-manager` stub fails the next `requirement add` with #30's failure envelope and status 4 | The shell result carries the envelope unchanged. No second write follows. The working tree is as it was after step 5. |
| 9 | Outside the role, a replayed turn runs `cd docs && echo x >> vision.md`; then the user asks the role for a commit | The shell write succeeds, because the redirection target is not written from the project root. The SCM's `commit` stages nothing and fails with `SPEC_DIGEST_MISMATCH`, #34's pre-stage digest comparison, or with `SPEC_CHECK_FAILED` when the `ears-manager` stub reports no `artifact.digest_mismatch` code. Its diagnostic names `docs/vision.md` with `ears-manager`'s diagnostics, and `git checkout -- docs/vision.md` as the discard route, which the role does not run. Outside the role, a replayed turn runs it, and the file is back at its committed content, so the step-5 Vision write is gone too and the digest still differs ([`commit`][scm-commit]). In the role, a replayed turn repeats the step-5 `artifact put`, and `check --change-set CS-00003` no longer reports `artifact.digest_mismatch` for that record. |
| 10 | The session ends | No commit and no push since setup. The step-5 record and the Vision write are still in the working tree. No harness or MCP stub process remains. |
| 11 | Start a new session, without continuing, through the entry point | A new session ID. The resume reads present `CS-00003`, its branch, and the step-5 requirement and Vision as uncommitted drafts. No call reads an earlier session. |
| 12 | Push a commit to the default branch of `origin` from outside the session, then continue the session through the entry point | The resume reads run again and present `CS-00003`, its branch, and `base_commit` from governed reads and `repo_state`, not from the conversation. The summary makes no claim about the default branch; step 14 detects the move. |
| 13 | Start a session with the `wms` stub stopped | The summary marks blocked work as unavailable. Drafting continues. No `wms` call succeeds. |
| 14 | The user approves and asks for a commit and a pull request | Only `scm` tools and `ears-manager` shell operations run. `commit` makes one commit in #34's message format. `publish` fetches and fails with `DEFAULT_MOVED`, because the default branch moved since `base_commit` (step 12), as #34's [failure table](../git-integration.md#failure-behavior) states. The role runs `refresh`, which adds a merge commit, then, in #34's [refresh sequence](../git-integration.md#refreshing-from-the-default-branch), `change-set update --base-commit` with the head that `refresh` returned, `impact`, a reviewed `change-set update --impact-file -` that records any new disposition, and `check --change-set CS-00003`, and runs `commit` again, so the manifest's `base_commit` equals the new default-branch head and the assessment is complete. `publish` pushes the branch to `origin`. The `gh` stub records one `pr create` whose `--repo` is the canonical repository, `--base` is `main`, and `--head` is `cs/00003-<slug>`, and whose body on standard input is the SCM's rendering. No merge and no other call follows the handoff. |
| 15 | Export every session | Each export names the role, the model, and the harness version, and holds the adapter line and every tool call with its status and error text. |

A session started in one bound harness and resumed in another, at step
11, gives the same result. That is the swap test run end to end.

### Guard vectors

Each vector runs against the guard directly and through the binding's
normal, active hook path, and must be refused before it runs. Hook-
unavailable cases are recorded separately as explicit gaps in
[File-source arguments](#file-source-arguments); a call
that passes through without a guard decision is not a successful refusal.
Unless a row says otherwise,
`cs/00003-<slug>` is checked out. The SCM's own refusals, such as a
force push, another repository, or a push to the default branch, are
negative checks of the [SCM's fixture][scm-fixture]:

| Role | Tool call | Expected refusal |
| --- | --- | --- |
| Drafting Table | `git push --force origin cs/00003-<slug>`, `git push origin main`, `git commit -F -`, `git add -A`, `git fetch origin`, `git switch cs/00004-<slug>`, and `git -c core.hooksPath=<dir> commit -F -` | Not a shell operation: Git runs through the `scm` tools |
| Drafting Table | `gh pr create --repo <other-owner>/<name> --base main --head cs/00003-<slug> --title 'x' --body-file -`, `gh pr edit 42 --repo <owner>/<name> --body-file -`, `gh api repos/<owner>/<name>`, and `gh auth token` | Not a shell operation: the Git host runs through the `scm` tools |
| Drafting Table | `git show HEAD:.protobot/project.yaml`, `git diff`, and `git remote -v` | Not a shell operation |
| Drafting Table | `git checkout -- docs/vision.md` | Not a shell operation; the user discards |
| Drafting Table | `gh pr merge 1 --merge` | Not a shell operation; the user merges |
| Drafting Table | `source-control-manager publish` | Not a shell operation: the role reaches the SCM only through the `scm` tools |
| Drafting Table | An `scm` tool that the manifest does not list | Not a Drafting Table operation |
| Drafting Table | `register-approved-change-set --change-set CS-00004` | Not the change set of the current branch |
| Drafting Table | `git switch -c cs/00001-project-init main`, in an initialized fixture clone | `Project already initialized` |
| Drafting Table | `ears-manager --output json artifact put --change-set CS-00003 --id vision --kind vision --path docs/vision.md --owner <owner> --content-file ~/.netrc` | `--content-file` with a path |
| Drafting Table | A `wms` tool that the manifest does not list, such as a lifecycle transition | Not a Drafting Table operation |
| Drafting Table | `ears-manager --output json requirement add --change-set CS-00003 ... --text "$GH_TOKEN" ...` | Variable expansion |
| Drafting Table | `date +%s` | Not a shell operation: only the ISO 8601 form |
| Drafting Table | `ears-manager --output json artifact put ... --content-stdin <<'EOF'` whose body holds a line `EOF` before the end | Here-document ends early; a second command follows |
| Drafting Table | `rg -n --pre=<program> docs/`, in Codex | Not a read form: the pattern follows `-e` |
| Drafting Table | A subagent launch, a web fetch, or a tool of a non-governed MCP server | Outside the role's tool set |
| Drafting Table | A read of `.env`, `.netrc`, `id_ed25519`, or `.protobot/change-sets/cs-00002.yaml` | Outside the role's tool set |
| Drafting Table | A read of `.git/config` or `.aws/credentials`, with a file tool or a Codex read form | Outside the role's tool set |
| Drafting Table | A read of `~/.config/gh/hosts.yml`, or of an in-project symlink that resolves outside the project | Outside the project |
| Drafting Table | A search with the harness's search tool whose path is `.git/config`, `.netrc`, or the project root | A search is a read |
| Drafting Table | `ears-manager --output json artifact put --change-set CS-00003 --id hook --kind architecture --path .claude/hooks/drafting-table-guard.sh --owner user --content-stdin` | Binding file, never an artifact |
| Drafting Table | `ears-manager check > out.json` | Output redirection |
| Other | `ears-manager requirement add` with any arguments | Governed write outside the role |
| Other | A file write to `.protobot/change-sets/cs-00002.yaml` | Guarded path |
| Drafting Table | A file write while `ears-manager` cannot list the registry | The failed read, named |
| Other | A file write under `.protobot/` while `ears-manager` cannot list the registry | Guarded path |
| Drafting Table | A file write or a shell command while `.protobot/` exists without a readable `project.yaml` | The failed read, named |

Harness checks: no headless run shows a permission prompt, session
upload is off, no export holds the planted `PROTOBOT-FIXTURE-TOKEN`,
and the whole fixture gives identical results with no IdeaBot
material.

---

## Out-of-scope decisions

| Decision | Rationale |
| --- | --- |
| Web Drafting Table and its hosted runtime | Excluded by #33. The Web Drafting Table loads the same Toolkit without a harness binding. |
| A standalone session manager | Excluded by #33. Each harness owns its sessions. |
| Bindings for other harnesses | Not written here. [Adding a harness](#adding-a-harness) gives the procedure and the table to extend. |
| `ears-manager` command grammar, request and result shapes | Defined by #30 ([`ears-manager` CLI Integration Contract](../ears-manager-cli.md)). |
| WMS operations and result shapes | Defined by #31. |
| The `wms` preflight tool and the authorization context of a resolution | #31 names the preflight tool and the operations; #32 defines the `human_approval_id` that a resolution carries and the trusted `drafting-table` role that the `wms` server establishes. This document decides only that preflight runs in the `wms` server for the role, and that the approval is the user's action at the WMS boundary, never a model claim. |
| Kit import | No route in the role: #30 has no import command, and no document names the writer of `.protobot/kits.lock` ([Kits](../components.md#kits), [Q5](../open-questions.md#q5-kit-package-and-future-capabilities)). A Kit's content arrives as an ordinary change set once #30 gains the operation. |
| Validation Rules packaging | Defined by #32 ([Validation Rules](../components.md#validation-rules)); the TUI path needs no library in the harness. |
| Interaction semantics and presentation | Defined by the [Drafting Table UX][ux]. |
| Branch, commit, pull-request, and registration rules | Defined by #34 ([Git and Project-Repository Integration](../git-integration.md)). The SCM performs the Git and Git host operations, and defines their tools, results, and failures (#125, [Source Control Manager][scm]). |
| The content of `drafting-specifications` | Written by #77 from the UX contract. This document reserves the name and requires the host mapping. |
| The guard's implementation language and packaging | Chosen by #77, within the limits in [The guard](#the-guard). |
| How the Toolkit and the adapter core reach a project other than ProtoBot, and how they are versioned | Open: the Toolkit packaging question in [Specification Toolkit](../components.md#specification-toolkit) and [Q5](../open-questions.md#q5-kit-package-and-future-capabilities). |
| A harness-neutral trace format | Open: [Evaluability](../components.md#evaluability). |
| Model and provider choice | The harness's choice, recorded in the trace. Skill quality per model is #63. |

---

## Related Documents

- [OpenCode Harness Binding](opencode.md) — The first harness binding:
  OpenCode files, native rules, and observed behaviors.
- [Claude Code Harness Binding](claude-code.md) — The second harness
  binding: Claude Code files, native rules, and documented behaviors.
- [Codex Harness Binding](codex.md) — The third harness binding: Codex
  files, the role profile, read forms, and observed behaviors.
- [Vision](../../vision.md) — Purpose, intended users, desired
  outcomes, prototype scope, and non-goals.
- [Architecture](../../architecture.md) — External interface
  inventory, persistent state, environmental constraints, and the
  Drafting Table Boundary.
- [Overview](../overview.md) — Guiding principles, EARS format,
  single-player and multi-player modes, workflow, and platform.
- [System Components](../components.md) — Component architecture,
  the Specification Toolkit, and cross-cutting concerns.
- [`ears-manager` CLI Integration Contract](../ears-manager-cli.md) —
  Command grammar, result envelopes, exit statuses, and the golden
  fixture.
- [Git and Project-Repository Integration](../git-integration.md) —
  Branches, commits, pull requests, permitted Git operations, and
  ungoverned-edit detection.
- [Source Control Manager](../source-control-manager.md) — The `scm`
  tools, their results and failures, and the repository fixture run
  against them.
- [Validation Rules](../validation-rules.md) — Lifecycle
  authorization, preflight, and the rejection of a Drafting Table
  transition.
- [Drafting Table WMS Integration](../drafting-table-wms.md) — Backend-neutral
  WMS operations, result shapes, and blocked-work resolution.
- [User Interaction Flow](../user-interaction-flow.md) — Phase
  details, sequence diagrams, and change types.
- [Drafting Table UX](../drafting-table-ux.md) — Stable interaction
  contract for the first local Drafting Table: checkpoints, resume,
  gaps, approval, and failure behavior.
- [Open Design Questions](../open-questions.md) — Unresolved design
  questions across all areas.
- [Related Work](../related-work.md) — Internal and external
  projects informing the design.
- [ADR-0002](../../decisions/0002-ears-specification-record-schema.md)
  — Record schemas, provenance, verification modes, and relationships.

[adr2-changeset]: ../../decisions/0002-ears-specification-record-schema.md#change-set-manifests
[adr2-pattern]: ../../decisions/0002-ears-specification-record-schema.md#ears-pattern-enum
[adr2-relationships]: ../../decisions/0002-ears-specification-record-schema.md#relationship-structure
[adr2-verification]: ../../decisions/0002-ears-specification-record-schema.md#verification
[credential-isolation]: ../components.md#authentication-and-credential-isolation
[env-constraints]: ../../architecture.md#environmental-constraints
[interface-approach]: ../../architecture.md#interface-specification-approach
[layer-stops]: #what-the-harness-layer-stops
[pre-stage]: ../git-integration.md#the-pre-stage-digest-comparison
[projections]: ../components.md#worker-repository-projections-decided
[scm]: ../source-control-manager.md
[scm-commit]: ../source-control-manager.md#commit
[scm-deploy]: ../source-control-manager.md#deployment-topology
[scm-fixture]: ../source-control-manager.md#repository-fixture-against-the-scm
[scm-host]: ../source-control-manager.md#host-adapter-boundary
[scm-mcp]: ../source-control-manager.md#mcp-protocol
[scm-read]: ../source-control-manager.md#approved-state-read-face
[strawman]: ../../architecture.md#opencode-plus-skill-strawman
[structural]: ../overview.md#enforce-constraints-structurally-not-through-trust
[ux]: ../drafting-table-ux.md
[ux-approval]: ../drafting-table-ux.md#approval-and-post-merge-materialization
[ux-auth]: ../drafting-table-ux.md#authoritative-state-over-conversation-memory
[ux-draft]: ../drafting-table-ux.md#draft-conversation-state
[ux-evidence]: ../drafting-table-ux.md#acceptance-evidence
[ux-failure]: ../drafting-table-ux.md#failure-behavior
[ux-gaps]: ../drafting-table-ux.md#hybrid-gap-surfacing-ux
[ux-leak]: ../drafting-table-ux.md#anti-leakage-rule
[ux-resume]: ../drafting-table-ux.md#resuming-an-existing-session
[ux-stale]: ../drafting-table-ux.md#stale-git-or-wms-state
[ux-wms]: ../drafting-table-ux.md#unavailable-wms
