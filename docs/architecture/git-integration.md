# ProtoBot: Git and Project-Repository Integration

> Design document — September 2026
>
> Defines how the Drafting Table turns governed specification changes
> into reviewable Git history, in single-player and multi-player modes.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Project identification](#project-identification)
  - [Every change arrives by pull
    request](#every-change-arrives-by-pull-request)
- [Registered artifact paths](#registered-artifact-paths)
  - [One change set, one file](#one-change-set-one-file)
- [Change-set branches](#change-set-branches)
- [Commit behavior](#commit-behavior)
- [Pull-request preparation](#pull-request-preparation)
- [Approved specification state](#approved-specification-state)
- [Ceremony in each mode](#ceremony-in-each-mode)
- [Ungoverned-edit detection](#ungoverned-edit-detection)
- [Permitted Git operations](#permitted-git-operations)
- [IdeaBot material](#ideabot-material)
- [Failure behavior](#failure-behavior)
- [Repository fixture](#repository-fixture)
- [Out-of-scope decisions](#out-of-scope-decisions)
- [Related Documents](#related-documents)

---

## Purpose and scope

This document answers the question posed by issue #34: _How does
the Drafting Table turn governed specification changes into
reviewable Git history in single-player and multi-player modes?_

It defines:

- how a project is identified, and where that identity lives;
- how specification artifact paths are selected and registered;
- when change-set branches are created, how they are named, and
  when they are deleted;
- what a specification commit contains and when it is made;
- how a pull request is prepared and what its body must show;
- what approved specification state is, and how approval is
  registered in each mode;
- which writes must go through `ears-manager`;
- which Git operations the Drafting Table may perform, and which
  are forbidden;
- how IdeaBot material seeds a session without becoming a hard
  dependency; and
- how each failure is diagnosed and safely retried.

The contract is defined for the **single-player TUI Drafting
Table** first, because that is the on-ramp
([Overview — Single-player mode](overview.md#single-player-mode)).
Every rule below also holds in multi-player and Web deployments;
[Ceremony in each mode](#ceremony-in-each-mode) states the only
differences.

This document does not define the Job Site's Git behavior. The
Job Site owns `wi/` branches, integration branches, attestation
paths, and the merge of completed work items
([Content Storage Model](components.md#content-storage-model)).

### Relationship to sibling contracts

This document defines _how a governed change becomes reviewable
Git history_. Adjacent contracts define the surfaces around it:

- **#28** (Drafting Table MVP user experience — pull request 81,
  pending) defines what the user sees and decides, including when
  the user asks for a commit or a pull request. Its authoritative
  mutation-ownership table names #34 for commit and pull-request
  mechanics; this document supplies them.
- **#30** (`ears-manager` CLI integration) defines the governed
  command and result boundary for specification reads and writes.
  This document names `ears-manager` operations; #30 defines their
  request and result shapes in the
  [`ears-manager` CLI Integration Contract](ears-manager-cli.md). The
  EM-04 first release records a change-set manifest but defers branch
  creation; this document defines the target Git workflow for that
  follow-on behavior.
- **#33** ([Agent Harness Adapter Contract](agent-harness/adapter-contract.md),
  with the [OpenCode](agent-harness/opencode.md),
  [Claude Code](agent-harness/claude-code.md), and
  [Codex](agent-harness/codex.md) bindings) defines
  skill discovery and harness tool permissions, including the optional
  early enforcement layer that denies direct writes to registered
  paths.
- **#125** ([Source Control Manager](source-control-manager.md))
  defines the component that performs this contract's Git and Git host
  operations for the Drafting Table. The Drafting Table asks for an
  operation, and the SCM derives the branch, the files, the message,
  and the pull request from the change set and enforces the rules
  below.
- **#75** implements this contract and executes the
  [repository fixture](#repository-fixture) as its test plan.

---

## Project identification

A **project** is one prototype and its canonical Git repository,
specification history, WMS configuration, and policy
([Overview — Terminology](overview.md#terminology)). A deployment
may serve many projects, so the Drafting Table must identify the
project from the working tree before it reads or writes anything.

### The project root

`.protobot/project.yaml` identifies the project. The directory
that contains `.protobot/` is the project root, and it must also
be the root of the Git working tree.

The Drafting Table resolves the project by walking up from the
current directory to the first directory that contains
`.protobot/project.yaml`. Resolution succeeds only when that
directory is the root of a Git working tree. If the file is
missing, if it sits below the working-tree root, or if the
directory is not a Git working tree at all, resolution fails and
no Git or `ears-manager` operation runs. The one exception is a
working tree with nothing named `.protobot` at its root: there the
steps of [Project initialization](#project-initialization) run,
because they create the file. A `.protobot` that is a symbolic link or
not a directory is not that exception: resolution fails there too. The
SCM's `repo_state` reports a tree with nothing named `.protobot` as
not initialized, and its `branch_init` cuts the initialization branch
in it ([`repo_state`](source-control-manager.md#repo_state)).

**Caller-supplied project claims are not trusted.** A project
name, branch, or remote supplied in a prompt, a command-line flag,
or a tool call never selects the project or widens what may be
written. The committed file in the working tree is the only
source. This is the same rule the Gate applies at hosted mutation
boundaries
([Authentication and Credential Isolation][credential-isolation]).

A hosted deployment keeps its own registry of project
registrations and adapter configurations outside any project's
`.protobot/`
([Deployment-level registry](../architecture.md#persistent-state)).
That registry decides which projects a user may open. It never
supplies or overrides the identity in the working tree.

### Repository fields

`project.yaml` already carries project identity, configured
artifact paths, non-secret backend references, and schema versions
([Content Storage Model](components.md#content-storage-model)).
This document adds a `repository` block for the Git-facing fields:

| Field | Purpose |
| --- | --- |
| `project.id` | Stable project identifier. Used as the WMS project key and in materialization keys. |
| `project.name` | Human-readable project name. |
| `repository.canonical_remote` | URL of the canonical repository. The Drafting Table pushes to this remote only. |
| `repository.default_branch` | The branch that holds approved specification state. `main` by default. It must lie outside `repository.branch_prefix` and outside the reserved `wi/` namespace: a default branch inside the prefix would read as a change-set branch, which the Drafting Table may write. The Source Control Manager enforces this when it loads the project, and when it cuts the branch; `ears-manager` does not check it yet. |
| `repository.review_mode` | `single-player` or `multi-player`. Declares the review ceremony. |
| `repository.branch_prefix` | Prefix for change-set branches. `cs/` by default. It may not lie in the reserved `wi/` namespace, the only reserved one today: neither `wi/` itself nor a prefix below it, such as `wi/cs/`; `ears-manager project init` and `check` reject it, and the Source Control Manager refuses to load such a project. A further reserved prefix has to be recorded in the [Content Storage Model](components.md#content-storage-model) before it can be enforced. |
| `schema_versions` | One version per store, as decided by [ADR-0002][adr2-versioning]. |
| `stores` | Relative paths for the requirement, interface, and change-set stores, as decided by [ADR-0003](../decisions/0003-ears-manager-storage-layout.md). |
| `store_digests` | Canonical integrity digest for each configured structured store. The digest covers its sorted visible YAML file set. |
| `artifacts` | The artifact registry: `id`, `kind`, `path`, `digest`, `owner`, and optional `validator` per entry ([ADR-0002][adr2-registry]). |

`ears-manager` owns this file and is the only writer
([Persistent State](../architecture.md#project-configuration-protobot)).
The Drafting Table reads it and commits it; it never edits it.
The registry cannot record a digest for `project.yaml` itself, so
`ears-manager check` validates that file structurally instead.

**`project.yaml` never contains credentials.** Tokens, client
secrets, and private keys are supplied by the environment in
single-player mode and by an external broker in hosted modes
([Environmental Constraints](../architecture.md#environmental-constraints)).
A `.protobot/` file that carries one is a `check` failure.

### Every change arrives by pull request

**A change reaches the default branch through a pull request, in
every mode. The Drafting Table never pushes to the default
branch.** Branch protection on that branch is a mandatory
enforcement layer in every mode
([Governed tool integrations][governed-tools]), and it is what
rejects an unreviewed push.

The mode changes who may merge, and nothing else:

| Mode | Who merges |
| --- | --- |
| Single-player | The author merges their own pull request. No reviewer is required. |
| Multi-player and Web | A reviewer merges. CODEOWNERS and required reviews apply. |

`repository.review_mode` declares which of the two the project
intends, so the Drafting Table knows whether to offer a self-merge
without reading branch protection, which it cannot do without host
credentials it does not hold. The declaration is a convenience for
the agent. The host is the control. When the two disagree, the
host wins and the merge fails. The
[failure table](#failure-behavior) names that case.

A project therefore has a Git host in every mode. A repository
with no host cannot satisfy this contract, because it has no place
for a reviewable pull request.

### Project initialization

Initialization writes `.protobot/project.yaml` through
`ears-manager`. It is a change set on its own branch and merges
through a pull request, like every other specification change. The
first change set of a project is `CS-00001`, so its branch is
`cs/00001-project-init`.

The order on that branch is fixed, because each step needs the
one before it:

1. Cut `cs/00001-project-init` from the default branch.
2. Write `project.yaml`, which gives the project its identity and
   its registry.
3. Run `change-set create`, which writes the manifest and records
   the default-branch head as `base_commit`.
4. Commit `.protobot/project.yaml`, `.protobot/projection.yaml`, and the
   initial change-set manifest, with the `shared` class of each registered
   path, then open the pull request and merge.

The default branch must already have at least one commit, because
a branch needs a base and a manifest needs a `base_commit`. A Git
host creates that commit when the repository is created. An empty
repository is initialized by the user first, outside this
contract.

The `ears-manager project init` command writes `project.yaml`, seeds the
artifact registry, records the schema versions, and classifies the registered
paths. The command and result boundary are defined in the
[`ears-manager` CLI Integration Contract](ears-manager-cli.md). The Git
fixture's initialization step invokes that command before creating the
initial change-set manifest.

Adopting an existing repository never rewrites its history and
never moves existing files. It registers the paths that are
already there.

---

## Registered artifact paths

The Drafting Table stages three categories of files: opaque
specification files listed in the `artifacts` registry of
`project.yaml`; structured requirement, interface, and change-set
files resolving under a directory named in the `stores` block (with
layout defined by
[ADR-0003](../decisions/0003-ears-manager-storage-layout.md)); and the
governed control files named in [Commit behavior](#commit-behavior)
(`.protobot/project.yaml` and `.protobot/projection.yaml`
classification entries). Opaque Vision, Architecture,
interface-IDL, and interface-prose files occupy `artifacts`.
Structured requirement, interface, and change-set records occupy
`stores`. A path matching none of these is not staged.
(The section heading serves as an umbrella term covering both
registered `artifacts` entries and structured `stores` directories,
while governed control files are a distinct, non-"artifact-path"
staged category outside the heading's scope.)

### Selecting the paths

At initialization the Drafting Table proposes a default layout and
the user confirms or changes it before the commit. The write goes
through `ears-manager project init` as described by
[Project initialization](#project-initialization). The proposed default is:

| Managed path | Role | Proposed path |
| --- | --- | --- |
| `vision` | opaque artifact | `docs/vision.md` |
| `architecture` | opaque artifact | `docs/architecture.md` |
| `requirements` | structured requirement store | `.protobot/requirements/` |
| `interfaces` | structured interface store | `.protobot/interfaces/` |
| `change-sets` | structured change-set store | `.protobot/change-sets/` |

Interface IDLs and interface prose are registered as they are
created, one entry per artifact, with the path the user chooses.

The three structured store paths are recorded in the `stores` block
of `project.yaml`, with defaults and filename mapping defined by
[ADR-0003](../decisions/0003-ears-manager-storage-layout.md). A
project may register different relative paths that remain inside the
working tree. The `artifacts` list remains for opaque Vision,
Architecture, interface-IDL, and interface-prose files.

### One change set, one file

A change set is one manifest file, named `cs-<nnnnn>.yaml` after its
change-set ID, in a flat `.protobot/change-sets/` folder. It is a
record like any other, so it follows the one-file-per-record rule
([ADR-0001][adr1-history]).

The manifest holds references, not content. It names which
requirements, interfaces and registered artifacts the change set
touches, why each one is touched, and how each impact candidate
was dispositioned
([ADR-0002][adr2-changeset]). The content itself lives in the
files the manifest names: one file per requirement, the
Architecture as its own registered document, each interface as its
own artifact. A large change set therefore produces many small
record files and one manifest, not one large manifest.

The folder only grows. An approved manifest is immutable, and an
abandoned change set is deleted with its branch, so it never
reaches the default branch. Every file in the folder is a
permanent audit record of one approved specification transaction.

The minimum layout inside each structured store and the file-naming
convention are defined by
[ADR-0003](../decisions/0003-ears-manager-storage-layout.md). This
document constrains which paths may be committed and by whom.

### Path rules

Every registered path:

- is relative to the project root and resolves inside the working
  tree, after symlink resolution;
- if it is an `artifacts` entry, is owned by exactly one component
  in the registry `owner` field, and is staged only when that owner
  is `ears-manager` or `user`;
- must not fall under `.protobot/attestations/` or name
  `.protobot/test-catalog.jsonl`, which the Job Site owns; and
- must be classified `shared` in `.protobot/projection.yaml`, so
  that both Workers receive approved specifications in their role
  projections
  ([Worker repository projections][projections]).

An unclassified path is denied by default in the projection
manifest. Registering a specification artifact without classifying
it therefore breaks Building.

**`ears-manager` writes that classification.** When it registers a
specification artifact, it adds the matching `shared` entry to
`.protobot/projection.yaml` in the same operation, so the
registration and the classification land in one change set. It
writes nothing else in that file. Every other classification stays
reviewed project policy, edited by a human and reviewed in the
pull request, in the same way as `.protobot/policy.yaml`. This
ownership is not stated anywhere else in the hierarchy; the same
pull request records it in
[architecture.md](../architecture.md#project-configuration-protobot).

An artifact imported from a Kit is re-owned on the way in.
`ears-manager` writes the file, so `ears-manager` is its registry
`owner`, and the Kit stays visible as the record's provenance and
in `.protobot/kits.lock`. Nothing a Kit owns is therefore
unstageable.

---

## Change-set branches

### One branch per change set

The Drafting Table creates one branch per change set, named:

```text
cs/<nnnnn>-<slug>
```

- `<nnnnn>` is the five-digit zero-padded sequence number of the change-set ID.
  Change set `CS-00005` uses `cs/00005-…`
  ([ADR-0002 — Change-Set Manifests][adr2-changeset]).
- `<slug>` is derived from the manifest `intent`: lowercased,
  non-alphanumeric runs replaced by a single hyphen, then cut at
  the last hyphen before position 40, or at exactly 40 characters
  when no hyphen precedes it. When nothing alphanumeric survives,
  the slug is `change-set`, so `CS-00005` becomes
  `cs/00005-change-set`. Every intent therefore yields exactly one
  branch name.
- `cs/` is the default and comes from `repository.branch_prefix`.

The prefix is the machine-readable part. A tool that must decide
whether a branch carries a proposed specification delta reads the
prefix, not the slug.

The representative transcript in the pending #28 document shows a
branch name without the sequence number. This document's
convention is the authoritative one for branch names.

### When the branch is created

The branch is cut from `repository.default_branch` when
`ears-manager change-set create` runs, not at session start and
not on the first write. The commit it is cut from is recorded as
the manifest's `base_commit`, as a full 40-character hexadecimal
hash; a branch name or tag is never accepted there
([ADR-0002][adr2-changeset]).

[Initialization](#project-initialization) is the one exception to
that order. There the branch exists first, because `project.yaml`
must be written before a change set can be created at all. Either
way, `change-set create` records the default-branch head as
`base_commit`, whether or not it also cuts the branch.

That head is read from the local ref for
`repository.default_branch`, after a fetch from
`repository.canonical_remote`. A branch is never cut from a stale
ref, and the recorded `base_commit` is never the remote-tracking
ref, so the branch and the manifest always name the same commit.
A fetch alone moves only the remote-tracking ref, so the SCM's
`repo_state` fetches and then fast-forwards the local ref
([`repo_state`](source-control-manager.md#repo_state)).

The initial Sketch is a change set like any other. Its Vision and
Architecture artifacts are written through
`ears-manager artifact put`, its manifest records the merge commit
that landed `CS-00001` on the default branch as `base_commit`, and
it is reviewed and merged the same way. Sketch updates are regular work
([Content Storage Model](components.md#content-storage-model)).

### Branch lifecycle

| Event | Branch |
| --- | --- |
| Change set created | Branch cut from the default branch |
| Change set proposed and under review | Branch pushed, updated by further commits |
| Pull request merged | Branch deleted after the merge commit exists on the default branch |
| Change set abandoned before merge | Branch deleted on explicit user request; the manifest is discarded with it |

Deleting a merged change-set branch loses nothing. The merge
strategy is merge commits, so every intermediate Drafting Table
commit stays reachable from the default branch
([Merge strategy](components.md#merge-strategy-decided)) and
remains available as evaluation data.

This resolves the change-set half of the open question _"Branch
naming and lifecycle"_ in the
[Content Storage Model](components.md#content-storage-model). The
`wi/` half stays open and belongs to the Job Site.

### Branches this document does not own

The Drafting Table never creates, checks out, writes to, or
deletes a `wi/` branch, an integration branch, or any branch it
did not cut for a change set. The one exception is the
fast-forward of the local default branch in the
[Allowed](#allowed) table, which moves no remote ref. A fetch's prune
drops remote-tracking refs under `refs/remotes/<remote>/`, `wi/*`
included, which is no such write: a remote-tracking ref is not the
branch, and the branch it mirrored is already gone from the remote. The
other branches belong to the Job Site, which
creates them from the source commit recorded in the work-item
contract.

---

## Commit behavior

### What is committed

A specification commit contains only:

- registered `artifacts` entries whose `owner` is `ears-manager` or
  `user`, and only those the active change set actually touched;
- structured requirement and interface records touched by the active
  change set;
- the active change-set manifest file itself, always (under the
  configured `stores.change_sets` directory, `.protobot/change-sets/`
  by default);
- `.protobot/project.yaml`, when the registry, a store digest, or an
  artifact digest changed;
  and
- `.protobot/projection.yaml`, when the change set registers a new
  specification path and `ears-manager` classifies it.

Everything else is excluded. Paths are staged by explicit list.
`git add -A`, `git add .`, and `git commit -a` are forbidden,
because each of them can sweep in an unrelated file that no
component owns. The SCM's `commit` derives the list from the change
set and writes the message below
([`commit`](source-control-manager.md#commit)).

### When a commit happens

Only on explicit user request. The Drafting Table never
auto-commits, never commits on a timer, and never commits on
session exit. Uncommitted `ears-manager` output stays in the
working tree for the next session. This matches the session-exit
behavior in the pending #28 contract and keeps the user as the
approver of every specification delta
([Governed tool integrations][governed-tools]).

### Message format

```text
spec(CS-00005): add --help requirements for ears-manager subcommands

Adds 13 requirements covering per-subcommand help output and
the unrecognized-flag error path. Two existing requirements are
recorded as applicable.

Change-Set: CS-00005
```

- The subject is `spec(CS-<nnnnn>): <intent>`, where `<intent>` is
  the manifest `intent` reduced to one line.
- The body is optional prose. It never restates the diff.
- The `Change-Set:` trailer is mandatory and machine-readable. A
  merge hook, a reconciler, or an evaluation job finds the
  manifest from the commit without parsing the subject.

Initialization is a change set too, so its commit uses the same
subject and the same trailer. There is no special case.

### History rules

- **Never rewrite pushed history.** No amend, no rebase, no force
  push, no history filter on a branch that has been pushed. The
  iteration history is evaluation data and the merge strategy
  exists to preserve it
  ([Evaluability](components.md#evaluability)).
- Before the first push, amending the most recent commit is
  allowed only on explicit user request.
- Every commit is authored with the user's configured Git
  identity. Hosted, where no user has a Git configuration, the
  author is the authenticated user's name and email from the Gate's
  signed context, and the committer is the service actor
  ([SCM identity](source-control-manager.md#identity)).
  Requirement-level origin is recorded in the
  `provenance` field of each record, not in the commit author, so
  the Job Site's bot-account question stays a Job Site question.

### Refreshing from the default branch

When the default branch has moved and the change set must be
brought up to date:

1. Fetch and merge `repository.default_branch` into the
   change-set branch, producing a merge commit.
2. Run `ears-manager change-set update --base-commit <default head>`
   to record the new `base_commit`.
3. Re-run `ears-manager impact`, because the candidate set may
   have changed, review any new candidate, and record the
   dispositions with a reviewed `change-set update --impact-file -`.
   A changed base makes the prior assessment stale
   ([Impact review protocol](ears-manager-cli.md#impact-review-protocol)).
4. Run `ears-manager check`.
5. Commit the manifest.

The Source Control Manager's `refresh` performs step 1, and its
`commit` performs step 5
([`refresh`](source-control-manager.md#refresh)).

Never rebase, and never reset the branch onto the new head. The
`base_commit` field names an immutable object rather than a
mutable ref; while the change set is proposed, `change-set update`
may repoint it. After the pull request merges, the manifest is
immutable and the field is frozen
([ADR-0002][adr2-changeset]).

---

## Pull-request preparation

### Title and body

The pull-request title is the commit subject without the `spec()`
prefix: the manifest `intent`, reduced to one line.

The body is rendered from the structured output of
`ears-manager change-set compare` and `ears-manager impact`, and
contains:

1. The intent, the change-set ID, and the `base_commit`.
2. The changed set: every `add`, `revise`, and `retire` operation
   with the requirement ID and, for a revision, the before and
   after text.
3. Interface and artifact operations, when the change set has any.
4. The impact assessment: every candidate with its disposition,
   its rationale, and whether it was found mechanically or added
   by semantic review.
5. `implementation_required`, with the rationale when it is
   `false`.
6. The list of files the pull request changes.

[ADR-0001][adr1-pr] plans this rendered summary and left open
whether it is posted by CI or on demand. This document decides it:
**the Drafting Table renders it into the pull-request body when
the pull request is created or updated.** CI is not required to
post it. A reviewer therefore sees the summary and the file diffs
in one place, and the summary exists even when CI is unavailable.
The SCM's `publish` renders it with code, so no model writes the
body ([Title and body](source-control-manager.md#title-and-body)).
The same pull request records the answer in ADR-0001, so a reader
who starts from the decision record finds it.

The raw file diffs remain the normative record. The rendered
summary is a view of them, in the same way that the inspection
report is a view of the Finding Ledger.

### Gates and labels

- CI runs `ears-manager check` on every branch push and as the merge gate.
  A bare `check` resolves and validates every proposed change-set manifest in
  the branch, including its impact assessment; interactive callers may pass
  `--change-set CS-<NNNNN>` to narrow the check.
- Path ownership in CI rejects a change that edits files outside
  the owning component's paths.
- Branch protection on the default branch requires the pull
  request.

No ProtoBot-specific labels are created or required. Approval is
the merge, governed by branch protection, CODEOWNERS, and required
reviews ([Multi-player mode](overview.md#multi-player-mode)).

### Updating a pull request

An update is another commit pushed to the same branch, followed by
re-rendering the body. The branch is never force-pushed and the
pull request is never closed and reopened to hide history.

### Demonstration and evidence attachments

Pull requests that carry a specification change do not attach demo
artifacts. Demo manifests and evidence belong to the Job Site's
attestation namespace, and a pull-request attachment is a
convenience view, never the canonical record
([Demonstration artifacts][demo-artifacts]).

---

## Approved specification state

### The merge is the approval event

Approved specification state is the state of the default branch. A
change-set manifest becomes immutable when its pull request merges
([ADR-0002][adr2-changeset]); `ears-manager` refuses to modify an
approved manifest afterwards
([ADR-0001][adr1-history]).

Merges use merge commits. Squash and rebase are forbidden on this
path because they destroy the intermediate Drafting Table commits
([Merge strategy](components.md#merge-strategy-decided)).

Merging changes no requirement record. Requirements carry no
`implemented` marker, and none is added at merge
([Build Work Item Lifecycle](components.md#build-work-item-lifecycle)).

### Registration

The merge commit alone does not create work. Registration does:
an idempotent call to the Job Site materializer with the
change-set ID, the resulting merge commit, and the stable
materialization key
([The PR → merge → build model][pr-merge-build]).

- **Multi-player.** A merge hook on the default branch registers
  the change set.
- **Single-player.** The author merges their own pull request, and
  then the Drafting Table runs `register-approved-change-set`
  locally, with the change-set ID only. The command reads the merge
  commit through the SCM's
  [`approved_merge`](source-control-manager.md#approved-state-read-face),
  so no commit hash passes through the agent. The merge alone is not
  sufficient ([Single-player mode](components.md#single-player-mode)).

Registration is idempotent by materialization key and by a distinct
per-command idempotency key. The registration command deterministically
derives the latter from the operation name, project ID, change-set ID, and
full merge commit as
`sha256("register-approved-change-set" + NUL + project_id + NUL +
change_set_id + NUL + merge_commit)`; the same registration retry therefore
reuses both keys and the same request fingerprint. Repeating it with the
same merge commit
returns the prior result. If the merge succeeds and the registration write
fails, the retry is the same registration call — never a second merge. A
registration that arrives with a different merge commit for the same change
set is rejected for reconciliation.

The Drafting Table never transitions a work item itself. Its
registration command hands the materializer a merge commit that the
SCM derived; the WMS Adapter applies
Validation Rules at its own write boundary
([Validation Rules](components.md#validation-rules)).

### True bugs produce no specification commit

A true bug — code that contradicts an approved requirement —
changes no specification, so it produces no branch, no commit, and
no pull request from the Drafting Table. The report enters the
materializer directly through the Job Site intake interface, with
the violated requirement IDs and the affected scope
([Incremental Development][change-types]). The Drafting Table's
Git contract begins only when a change set exists.

If refinement shows that the requirements themselves are wrong,
the request is reclassified as _undefined_ or _changes_, a change
set is created, and everything in this document applies again.

---

## Ceremony in each mode

Artifact ownership is identical in every mode. `ears-manager` is
the exclusive write gate for registered specification artifacts,
the Job Site owns the test catalog and attestations, and the same
branch, commit, and pull-request rules apply. Only the review
ceremony and the credential path differ.

| Concern | Single-player | Multi-player and Web |
| --- | --- | --- |
| Who owns specification artifacts | `ears-manager` | `ears-manager` |
| Branch naming and creation | `cs/<nnnnn>-<slug>` from the default branch | Identical |
| Commit content, message, trailer | As above | Identical |
| Pull-request body | Rendered from `change-set compare` and `impact` | Identical |
| How a change reaches the default branch | Pull request | Pull request |
| Who approves | The author merges their own pull request. No reviewer is required. | A reviewer merges; CODEOWNERS and required reviews apply |
| Registration trigger | Local `register-approved-change-set` | Merge hook on the default branch |
| Where the [Source Control Manager](source-control-manager.md#deployment-topology) runs | On the user's machine, started by the harness binding | On each contributor's machine for a local harness; hosted behind the Gate for the Web Drafting Table |
| Git host credential | The user's own Git host token, used by the SCM | On a local harness, the user's own token through Git's credential helper and `gh`, used by the SCM and not readable by the role on guard-checked calls; a fail-open call can expose it ([#33 Credentials](agent-harness/adapter-contract.md#credentials)); hosted and Web, OAuth 2.1 through the Bridge/Gate pattern, and the agent runtime never sees the credential |
| Merge strategy | Merge commit | Merge commit |
| Where code lands | Job Site merges `wi/` branches | Identical |

The credential rows follow the deployment topology in the
[WMS Adapter API](../architecture.md#wms-adapter-api) and the
credential isolation rules in
[Authentication and Credential Isolation][credential-isolation].
In hosted modes, the Gate authenticates the caller and scopes the
credential to the project and the action. The branch restrictions
that the token format cannot express, the allowlist in
[Permitted Git operations](#permitted-git-operations), are enforced
by the SCM behind the Gate, which checks every ref it writes against
the Gate's context, and by the host's branch protection
([SCM ref policy](source-control-manager.md#ref-policy)).

The Web Drafting Table adds per-user session state and
authorization at the application boundary
([Web Drafting Table](../architecture.md#user-facing-interfaces)).
That changes where the working tree lives and who may act on a
project; it does not change any rule in this document.

---

## Ungoverned-edit detection

No component is meant to write a registered specification file
directly; for the Drafting Table the guard enforces this on the calls
it checks, and a call without a guard decision may still write one
([File-source arguments][fail-open]). A file edited outside
`ears-manager`, by any route, must be rejected or caught before it can
reach the default branch. Four layers do that, in order of how early they
fire.

| Layer | Where | Catches |
| --- | --- | --- |
| Harness tool permission rules (optional, [#33](agent-harness/adapter-contract.md#what-the-harness-layer-stops)) | The agent's own tool call | A write under a registered path before it happens |
| Pre-stage digest comparison | The Drafting Table, in the SCM's `commit`, before staging | A registered artifact or structured store whose content no longer matches its governed digest |
| `ears-manager check` | Branch push and merge gate in CI | Malformed records, artifact or store digest mismatches, dangling references, symmetry and cycle violations |
| Path ownership in CI | Merge gate | A change that edits files outside the owning component's paths |

### The pre-stage digest comparison

Before staging anything, the Drafting Table recomputes the content
digest of every registered artifact the change set touches using the
canonical text rules in [ADR-0002][adr2-digest], then compares it with the
`digest` recorded in the registry. It also recomputes the canonical file-set
digest for every structured store and compares it with `store_digests`.
`ears-manager` updates the affected digest on every governed write
([ADR-0002][adr2-registry]), so a mismatch means the file or record set
changed by some other route beyond an allowed line-ending representation.

Artifact registry entries name regular opaque files. The structured
requirement, interface, and change-set directories are configured through
the `stores` block, remain distinct from opaque artifact entries, and are
protected by their own canonical store digests.

On a mismatch the Drafting Table:

1. stages nothing and commits nothing;
2. names each path whose digest does not match; and
3. offers the two routes forward — discard the direct edit
   (`git checkout -- <path>`), or bring the content in through
   `ears-manager artifact put` or the matching `requirement` or
   `interface` subcommand, which validates it and recomputes the
   digest.

The discard restores the last committed content. When the path also
holds a governed write that is not committed yet, that write goes
with it, and it is repeated through `ears-manager`; the digest
matches again only then. For a structured store, the discard restores
its tracked records only. A record that a direct edit added is
untracked, so the Drafting Table names it too, and the user removes it
before the store digest matches again.

Unregistered files in the working tree are not an error. They are
simply never staged by the Drafting Table.

CI repeats the same comparison inside `ears-manager check`, so a
contributor who bypasses the Drafting Table entirely is still
caught before merge. Path ownership in CI is the last layer.

---

## Permitted Git operations

The Drafting Table performs only the operations below. Anything
not listed is forbidden. This is the concrete form of the
principle that _Git operations are explicit_
([Governed tool integrations][governed-tools]).

The Drafting Table performs these operations through the
[Source Control Manager](source-control-manager.md). Its Drafting
Table face offers a stricter subset of this list as tools. It takes
no ref, path, remote, or message from the agent, except the checked
prefix and default branch at initialization and an optional commit
body, and derives every target from the change set ([Mapping to #34's
permitted operations][scm-mapping]).
The role is designed to run no Git or Git host command itself: on calls
it checks, the guard refuses them in the role's shell, where only
`ears-manager`, the clock, and registration remain
([Shell operations](agent-harness/adapter-contract.md#shell-operations)).
The SCM enforces its operation rules when called through its tools;
branch protection and CI continue to gate merges even if the harness
layer is off. They do not intercept direct shell `git` or `gh` calls
that an absent guard and permissive native rules let through
([What the harness layer stops][layer-stops]). On a call without a
guard decision, OpenCode's and Claude Code's native rules still refuse
`git` and `gh`; Codex has no native command rules, so its sandbox
blocks Git writes and Git host calls but not Git reads
([File-source arguments][fail-open]).

### Allowed

| Operation | Constraint |
| --- | --- |
| Initialize the control namespace | `ears-manager project init` writes `.protobot/project.yaml` and `.protobot/projection.yaml` without committing; Git commits them with the initial manifest on the change-set branch |
| Read repository state | `status`, `log`, `diff`, `show`, `ls-files`, `rev-parse`, `merge-base`, and `remote` for listing only |
| Fetch | From `repository.canonical_remote` only, into remote-tracking refs only, never a local branch or a tag, pruning the ones whose branch the remote deleted. Before `project.yaml` exists, from the upstream remote of the local default branch only, to cut the initialization branch from a fresh head, and `git ls-remote` of that remote, to see whether the initialization branch exists there |
| Fast-forward the local default branch | Only to the head of `repository.default_branch` on the canonical remote, or, before `project.yaml` exists, on the upstream remote of the local default branch; only by fast-forward; and, when it is checked out, only with no uncommitted change to a tracked file; a fetch alone leaves the local ref stale, and a change-set branch is cut from it |
| Create a change-set branch | Named `cs/<nnnnn>-<slug>`, cut from `repository.default_branch` |
| Switch to an existing change-set branch | Only to the branch of a change set in the store, on resume |
| Stage | Registered `artifacts` entries owned by `ears-manager` or `user` and touched by the active change set, structured requirement and interface records touched by the active change set, the active change-set manifest file itself (always), `project.yaml`, and the `ears-manager` classification entries in `projection.yaml`, by explicit path, each a file, never a directory. A failed commit leaves the user's index as it was, content that was already staged included; after a successful commit, the index entries of exactly those paths are set to the new commit |
| Commit | On explicit user request, with the required message and trailer |
| Push a change-set branch | Non-force, to the canonical remote only |
| Open or update a pull request | Against `repository.default_branch`, body rendered from `change-set compare` and `impact` |
| Merge the default branch into the change-set branch | Merge commit; followed by `change-set update`. A conflicted merge is aborted with `git merge --abort` and resolved as the failure table says |
| Merge one's own pull request | Single-player only, merge commit, followed by registration |
| Delete a merged change-set branch | Only after the merge commit exists on the default branch |

### Forbidden

| Operation | Why |
| --- | --- |
| Force push, rebase, amend of pushed commits, history filters | Destroys the iteration history the merge strategy and evaluability depend on |
| Squash or rebase merges | The decided merge strategy is merge commits |
| `git add -A`, `git add .`, `git commit -a` | Stages files no component owns |
| Direct writes to registered specification files | `ears-manager` is the exclusive write gate |
| Any write to `wi/*` or an integration branch | The Job Site owns them |
| Any write under `.protobot/attestations/` or to `.protobot/test-catalog.jsonl` | The Job Site owns them |
| Push to the default branch, in any mode | Approval is the merge of a pull request |
| Creating tags, adding or changing remotes, submodule operations | Outside the contract; no ProtoBot behavior depends on them |
| Fetching or pushing any repository other than the canonical remote, apart from the fetch before initialization in the Allowed table | Project identity comes from the working tree, not from a caller-supplied remote |

Refusal is not advisory. The SCM refuses what this list forbids in
every mode, in hosted modes behind a Gate that authenticates the
caller and scopes the credential, and in every mode branch
protection and CI path ownership catch what reaches the host.

The Drafting Table's Git role is scoped to change-set branches and
nothing else, and it exercises that role only through the SCM.
Workers and implementation-aware test agents receive no Git
mutation role at all, and only the Materializer, the
Integration/Merge service, the WMS control plane, and the SCM
acting for the Drafting Table receive the narrow actions their
current contract requires
([Authentication and Credential Isolation][credential-isolation]).

---

## IdeaBot material

IdeaBot research artifacts seed the first Sketching session. The
handoff is manual: the user pastes or references IdeaBot output in
the conversation
([Q4](open-questions.md#q4-ideabot-handoff-format), and non-goal 3
in the [Vision](../vision.md#non-goals)).

For this contract, that means three things:

- **IdeaBot material is input content, never a registered
  artifact.** It is not registered in `project.yaml` and is not
  committed as itself.
- **It enters the repository only through `ears-manager`.** When
  IdeaBot text becomes part of the Vision or the Architecture, the
  user approves that text and `ears-manager artifact put` writes
  it to the registered artifact. What lands in Git is the approved
  artifact, with its own digest and provenance.
- **It is never a dependency.** A session starts, initializes a
  project, and produces a Sketch with no IdeaBot input at all.
  Nothing in this contract branches on whether IdeaBot material
  was supplied.

---

## Failure behavior

Every failure leaves the working tree and the repository
unchanged: no partial commit, no half-created branch, no pushed
branch without its commit. Each row states the deterministic
diagnostic and the safe retry. The SCM reports each row that it
detects with a stable code
([Failure behavior](source-control-manager.md#failure-behavior)).

| Condition | Detection | Diagnostic | Safe retry |
| --- | --- | --- | --- |
| No `.protobot/project.yaml` found | Project resolution walks to the filesystem root | Names the directory searched and the expected path | Run project initialization, or start the session inside the project |
| `project.yaml` is not at the working-tree root | Project resolution | Names both the file location and the working-tree root | Move the session to the correct checkout; the Drafting Table never relocates the file |
| Store schema version newer than the tool | `ears-manager` reads `schema_versions` | Names the store, the file version, and the supported version | Use the supported version; after v1 adoption, perform any upgrade through a reviewed migration change set |
| Registered artifact or structured-store digest mismatch | Pre-stage comparison | Names the safe configuration field and mismatch class | Discard the direct edit, or re-apply it through `ears-manager` |
| Registered path missing from the projection manifest | `ears-manager check` | Names the path and the required class `shared` | Re-run the registration; `ears-manager` writes the classification entry and the Drafting Table stages `projection.yaml` with it |
| Branch `cs/<nnnnn>-<slug>` already exists | Branch creation | Names the branch and whether it is local, remote, or both | Resume that change set, or create the change set under a new ID |
| Default branch has moved since `base_commit` | `merge-base` check before push or merge | Names the recorded base and the current head | Refresh: merge the default branch in, then `change-set update` |
| Push rejected, non-fast-forward | Push exit status | Names the branch and the remote head | The remote change-set branch has commits that this checkout lacks. The user reviews and integrates them, then pushes again; never force, and never merge them unreviewed |
| Push rejected by branch protection | Push exit status | Names the protected branch | Push the change-set branch instead and open a pull request. A push to the default branch is a bug in the caller, not a state to retry |
| Merge refused by branch protection | Host API response | Names the protected branch, the failing requirement, and the declared `review_mode` | Satisfy the requirement, such as a green check or a review. If `review_mode` says `single-player` and the host still demands a reviewer, the declaration and the host disagree and the project configuration must be corrected |
| Push rejected, missing or expired credential | Push exit status | Names the remote and the credential source for the mode | Refresh the credential outside the agent; the agent never receives one directly |
| Pull-request creation failed | Host API response | Names the host status and whether the branch was pushed | Retry creation when the host refused the request; the branch and its commits are already correct. When the host may have applied it, as after a 5xx or a timeout, read the pull request's state first, then retry |
| `ears-manager check` failed in CI | Non-zero exit in the merge gate | The check's complete deterministic diagnostic set | Fix through `ears-manager`, commit, push to the same branch |
| Merge conflict in a change-set manifest or index file | Merge of the default branch | Names the conflicting file | Resolve mechanically; sorted lists and fixed key order keep the resolution deterministic ([ADR-0001][adr1-diff]) |
| Merge conflict in a requirement record | Merge of the default branch | Names the record | Rare by design, since records are one file each; resolve through `ears-manager` and revalidate |
| Merge succeeded, registration failed | Registration call | Names the change set, merge commit, materialization key, and registration idempotency key | Repeat the same registration call with both derived keys; it is idempotent and never merges again |
| Registration rejected, different merge commit | Materializer response | Names both merge commits | Reconcile; a change set has exactly one approved merge commit |

---

## Repository fixture

The fixture is a local **bare repository** plus one working clone.
It needs no Git host, no network, and no WMS backend. Issue #75
executes it as its test plan. The same steps and negative checks
also run against the SCM, with no shell in the caller
([Repository fixture against the SCM][scm-fixture]).

**Setup.** Create a bare repository as `origin` with one commit on
the default branch, clone it, and configure a Git identity. Where
a step needs a Job Site or a WMS, the fixture substitutes a
recording stub: registration is asserted by the call the Drafting
Table makes, not by work-item state.

**Standing in for the host.** A bare repository has no
pull-request API and no branch protection. The fixture tests the
Git mechanics that a pull request wraps, not the host. Step 6
asserts the two things that make a pull request reviewable, which
are the pushed branch and the rendered body, and writes the body
to a file for comparison. Step 8 performs the merge locally, in
place of the host merge button. Branch protection and the
host API are covered by a separate integration test, and the
negative check on pushing to the default branch stands in for the
protection rule.

| # | Action | Expected result |
| --- | --- | --- |
| 1 | Initialize the project: cut `cs/00001-project-init`, write `project.yaml`, create `CS-00001`, commit | The branch exists and is checked out. `.protobot/project.yaml` carries identity, `canonical_remote`, `default_branch`, `review_mode`, version-1 schema keys, the three configured store paths and store digests, and two opaque artifact entries. `.protobot/projection.yaml` carries a `shared` class for each governed specification path. One commit of the control files and initial manifest, subject `spec(CS-00001): <intent>`, trailer `Change-Set: CS-00001`. The default branch is unchanged. `ears-manager check` exits zero. |
| 2 | Merge `CS-00001`, register, then create change set `CS-00002` for the initial Sketch | The default branch head is a merge commit. Branch `cs/00002-<slug>` exists and is checked out. Its tip equals the new default-branch head, and the manifest records that head's full 40-character hash as `base_commit`. No other branch was created. |
| 3 | Write Vision and Architecture through `ears-manager artifact put` | Both registered paths exist. Their registry digests match their content and the structured-store digests still match their record sets. `projection.yaml` is unchanged, because step 1 already classified both paths and `artifact put` classifies only a new path ([Artifacts](ears-manager-cli.md#artifacts)). `git status` lists only the two artifacts, the manifest, and `project.yaml`. |
| 4 | Make a substantive direct edit to a registered artifact, then request a commit | Nothing is staged and no commit is created. The diagnostic names the path and both digests. `ears-manager check` exits non-zero for the same path. |
| 5 | Discard the direct edit, which also drops the uncommitted step-3 write of that artifact; write it again through `ears-manager artifact put`; request a commit | Exactly one commit. It contains only the two artifacts, the manifest, and `project.yaml`. Subject is `spec(CS-00002): <intent>`; the body carries the `Change-Set: CS-00002` trailer. |
| 6 | Push the branch and prepare the pull request | `origin` has `cs/00002-<slug>` at the same commit; the default branch is unchanged. The rendered body contains the intent, the `base_commit`, every changed operation, every impact disposition with origin and rationale, `implementation_required`, and the file list. It matches the output of `change-set compare` and `impact`. |
| 7 | Commit an unrelated change on the default branch, then refresh the change set | The change-set branch gains a merge commit with two parents. The manifest's `base_commit` equals the new default-branch head. `git log --walk-reflogs` shows no rebase and the branch's first commit is unchanged. |
| 8 | Merge the branch into the default branch with a merge commit, then register | The default branch head is a merge commit with two parents. The registration stub recorded one call with the change-set ID, that merge commit, the materialization key, and the derived registration idempotency key. Running registration again records no new call and returns the first result. A write to the merged manifest is refused. |

### Negative checks

| Check | Expected result |
| --- | --- |
| Force push the change-set branch | Refused by the Drafting Table; the remote branch is unchanged |
| Amend a pushed commit | Refused; the pushed commit is unchanged |
| Stage an unregistered file | Refused; the file stays untracked or unstaged |
| Edit, add, delete, or rename a structured record outside `ears-manager` | Refused by the store digest comparison; no commit is created |
| Create or write a `wi/` branch | Refused; no such ref exists in the fixture |
| Write under `.protobot/attestations/` | Refused; the path stays absent |
| Push to the default branch, in either `review_mode` | Refused before the push runs |
| Register a second, different merge commit for `CS-00002` | Rejected for reconciliation; the first registration stands |
| Run every step in a clone with no IdeaBot material | Identical results; no step depends on IdeaBot input |

---

## Out-of-scope decisions

Listed so the decisions are traceable and do not silently
resurface.

| Decision | Rationale |
| --- | --- |
| `wi/` branch naming and lifecycle | The Job Site owns those branches. The open question in the [Content Storage Model](components.md#content-storage-model) stays open for that half. |
| Git host API binding for pull requests | This document names the operations. The concrete host client and its error mapping belong to the [SCM's host adapter](source-control-manager.md#host-adapter-boundary), and its authentication to the deployment. The first adapter drives `gh` on GitHub, for every harness. |
| Bot account model for the Job Site | The Drafting Table commits with the user's identity, so the open question in [Multi-player Workflow](components.md#multi-player-workflow) is unchanged by this contract. |
| Merge queue or batching | Concurrent change sets follow the standard refresh-before-merge model. A Bors-style queue is [related work](related-work.md#gas-town--beads-steve-yegge), not a decision here. |
| Commit signing | Whether commits and merges must be signed is a project policy and deployment decision, not a Drafting Table behavior. |
| Store directory layout and record filenames | Defined by [ADR-0003](../decisions/0003-ears-manager-storage-layout.md). This document constrains which paths may be staged and committed, not how stores organize their records. |
| `ears-manager` command and result shapes | Defined by the [`ears-manager` CLI Integration Contract](ears-manager-cli.md). |
| Harness tool permission rules | Defined by [#33](agent-harness/adapter-contract.md#what-the-harness-layer-stops). This document names the layer and its effect, not its configuration. |
| Kit import commits | Kit packaging is open ([Kits](components.md#kits)). The imported specification content arrives as a proposed change set and follows this contract. The lock file `.protobot/kits.lock` is a separate matter: no document names its writer, so this contract does not stage it. Whoever settles Kit packaging must name that owner. |
| Conflict-resolution UX | The failure table states the deterministic diagnostic and the safe retry. How the Drafting Table presents a conflict to the user is UX (#28). |
| Web Drafting Table working-tree hosting | Where a hosted session keeps its checkout and session state is a deployment concern ([Web Drafting Table](../architecture.md#user-facing-interfaces)). The Git rules are unchanged. |

---

## Related Documents

- [Vision](../vision.md) — Purpose, intended users, desired
  outcomes, prototype scope, and non-goals.
- [Architecture](../architecture.md) — External interface
  inventory, persistent state, environmental constraints, and the
  Project Repository contract.
- [Overview](overview.md) — Guiding principles, EARS format,
  single-player and multi-player modes, workflow, and platform.
- [System Components](components.md) — Component architecture,
  the content storage model, the multi-player workflow, and
  cross-cutting concerns.
- [`ears-manager` CLI Integration Contract](ears-manager-cli.md) —
  Command grammar, results, diagnostics, and impact review.
- [Source Control Manager](source-control-manager.md) — The component
  that performs this contract's Git and Git host operations.
- [User Interaction Flow](user-interaction-flow.md) — Phase
  details, sequence diagrams, and change types.
- [Agent Harness Adapter Contract](agent-harness/adapter-contract.md) —
  Harness-neutral adapter core, the guard, and harness obligations.
- [OpenCode Harness Binding](agent-harness/opencode.md) — The first
  harness binding.
- [Claude Code Harness Binding](agent-harness/claude-code.md) — The
  second harness binding.
- [Codex Harness Binding](agent-harness/codex.md) — The third harness
  binding.
- [Open Design Questions](open-questions.md) — Unresolved design
  questions across all areas.
- [Related Work](related-work.md) — Internal and external
  projects informing the design.
- [ADR-0001](../decisions/0001-requirements-storage-format.md) —
  One-file-per-record YAML storage, the PR reviewability plan, and
  change-set history representation.
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md)
  — Record schemas, `base_commit`, and the artifact registry.
- [ADR-0003](../decisions/0003-ears-manager-storage-layout.md) —
  Store paths, schema version keys, and the `stores` block.

[adr1-diff]: ../decisions/0001-requirements-storage-format.md#1-git-diffmerge-compatibility
[adr1-history]: ../decisions/0001-requirements-storage-format.md#change-set-history-representation
[adr1-pr]: ../decisions/0001-requirements-storage-format.md#4-pr-reviewability-plan
[adr2-changeset]: ../decisions/0002-ears-specification-record-schema.md#change-set-manifests
[adr2-digest]: ../decisions/0002-ears-specification-record-schema.md#digest-calculation
[adr2-registry]: ../decisions/0002-ears-specification-record-schema.md#artifact-registry-entries
[adr2-versioning]: ../decisions/0002-ears-specification-record-schema.md#schema-versioning
[change-types]: user-interaction-flow.md#incremental-development-and-change-types
[credential-isolation]: components.md#authentication-and-credential-isolation
[demo-artifacts]: user-interaction-flow.md#demonstration-artifacts
[governed-tools]: ../architecture.md#governed-tool-integrations
[pr-merge-build]: components.md#the-pr--merge--build-model
[projections]: components.md#worker-repository-projections-decided
[scm-fixture]: source-control-manager.md#repository-fixture-against-the-scm
[scm-mapping]: source-control-manager.md#mapping-to-34s-permitted-operations

[layer-stops]: agent-harness/adapter-contract.md#what-the-harness-layer-stops
[fail-open]: agent-harness/adapter-contract.md#file-source-arguments
