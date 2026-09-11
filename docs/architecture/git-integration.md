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
  request and result shapes.
- **#33** (OpenCode Specification Toolkit adapter) defines skill
  discovery and harness tool permissions, including the optional
  early enforcement layer that denies direct writes to registered
  paths.
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
no Git or `ears-manager` operation runs.

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
| `repository.default_branch` | The branch that holds approved specification state. `main` by default. |
| `repository.review_mode` | `single-player` or `multi-player`. Declares the review ceremony. |
| `repository.branch_prefix` | Prefix for change-set branches. `cs/` by default. It may not be `wi/`, which is the only reserved prefix today; `ears-manager check` rejects it. A further reserved prefix has to be recorded in the [Content Storage Model](components.md#content-storage-model) before it can be enforced. |
| `schema_versions` | One version per store, as decided by [ADR-0002][adr2-versioning]. |
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
first change set of a project is `CS-001`, so its branch is
`cs/001-project-init`.

The order on that branch is fixed, because each step needs the
one before it:

1. Cut `cs/001-project-init` from the default branch.
2. Write `project.yaml`, which gives the project its identity and
   its registry.
3. Run `change-set create`, which writes the manifest and records
   the default-branch head as `base_commit`.
4. Commit three files — `project.yaml`, the manifest, and
   `projection.yaml` with the `shared` class of each registered
   path — then open the pull request and merge.

The default branch must already have at least one commit, because
a branch needs a base and a manifest needs a `base_commit`. A Git
host creates that commit when the repository is created. An empty
repository is initialized by the user first, outside this
contract.

No `ears-manager` subcommand writes `project.yaml` today. Neither
the CLI contract in
[architecture.md](../architecture.md#ears-manager-cli) nor the
subcommand table in
[components.md](components.md#subcommands) lists one. This
contract therefore depends on #30 defining a project
initialization operation that writes `project.yaml`, seeds the
artifact registry, and records the schema versions. Until that
operation exists, fixture step 1 has no command to run.

Adopting an existing repository never rewrites its history and
never moves existing files. It registers the paths that are
already there.

---

## Registered artifact paths

Every specification artifact the Drafting Table may commit is
registered in the `artifacts` list of `project.yaml`. An
unregistered path is not a specification artifact, and the
Drafting Table never stages it.

### Selecting the paths

At initialization the Drafting Table proposes a default layout and
the user confirms or changes it before the commit. The write goes
through the `ears-manager` initialization operation that
[Project initialization](#project-initialization) records as a
dependency on #30. The proposed default is:

| Registry entry | `kind` | Proposed path |
| --- | --- | --- |
| `vision` | `vision` | `docs/vision.md` |
| `architecture` | `architecture` | `docs/architecture.md` |
| `requirements` | `requirement-store` | `.protobot/requirements/` |
| `change-sets` | `change-set` | `.protobot/change-sets/` |

Interface IDLs and interface prose are registered as they are
created, one entry per artifact, with the path the user chooses.

The requirement store sits inside `.protobot/` by default, because
`ears-manager` manages every record in it and keeping those files
together leaves the rest of the tree to the project. The path is
still a registry entry, so a project may point it elsewhere.

`.protobot/change-sets/` is fixed by the
[Content Storage Model](components.md#content-storage-model) and
is not a user choice. The other paths are defaults, not mandates:
an existing project points its entries at the files it already
has.

Two of these entries name a directory rather than a file. ADR-0002
defines `requirement-store` as a directory and `change-set` as a
single manifest file
([ADR-0002][adr2-registry]), so the folder entry above uses a kind
that describes one of its members. The registry needs either a
directory kind for the folder or a rule that a `change-set` entry
may name the folder. Whichever way ADR-0002 and #30 settle it, the
digest rule below already covers both.

### One change set, one file

A change set is one manifest file, named `cs-<nnn>.yaml` after its
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

The layout _inside_ the requirement store — flat or mirroring the
specification levels, and the file-naming convention — stays with
`ears-manager` and remains an open design question
([`ears-manager` — Open design questions](components.md#ears-manager)).
This document constrains only which paths may be committed and by
whom.

### Path rules

Every registered path:

- is relative to the project root and resolves inside the working
  tree, after symlink resolution;
- is owned by exactly one component in the registry `owner` field,
  and the Drafting Table stages only entries owned by
  `ears-manager` or `user`;
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
cs/<nnn>-<slug>
```

- `<nnn>` is the zero-padded sequence number of the change-set ID.
  Change set `CS-005` uses `cs/005-…`
  ([ADR-0002 — Change-Set Manifests][adr2-changeset]).
- `<slug>` is derived from the manifest `intent`: lowercased,
  non-alphanumeric runs replaced by a single hyphen, then cut at
  the last hyphen before position 40, or at exactly 40 characters
  when no hyphen precedes it. When nothing alphanumeric survives,
  the slug is `change-set`, so `CS-005` becomes
  `cs/005-change-set`. Every intent therefore yields exactly one
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

The initial Sketch is a change set like any other. Its Vision and
Architecture artifacts are written through
`ears-manager artifact put`, its manifest records the merge commit
that landed `CS-001` on the default branch as `base_commit`, and
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
did not cut for a change set. Those belong to the Job Site, which
creates them from the source commit recorded in the work-item
contract.

---

## Commit behavior

### What is committed

A specification commit contains only:

- registered artifact paths whose `owner` is `ears-manager` or
  `user`, and only those the active change set actually touched;
- the change-set manifest under `.protobot/change-sets/`;
- `.protobot/project.yaml`, when the registry or a digest changed;
  and
- `.protobot/projection.yaml`, when the change set registers a new
  specification path and `ears-manager` classifies it.

Everything else is excluded. Paths are staged by explicit list.
`git add -A`, `git add .`, and `git commit -a` are forbidden,
because each of them can sweep in an unrelated file that no
component owns.

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
spec(CS-005): add --help requirements for ears-manager subcommands

Adds 13 requirements covering per-subcommand help output and
the unrecognized-flag error path. Two existing requirements are
recorded as applicable.

Change-Set: CS-005
```

- The subject is `spec(CS-<nnn>): <intent>`, where `<intent>` is
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
  identity. Requirement-level origin is recorded in the
  `provenance` field of each record, not in the commit author, so
  the Job Site's bot-account question stays a Job Site question.

### Refreshing from the default branch

When the default branch has moved and the change set must be
brought up to date:

1. Fetch and merge `repository.default_branch` into the
   change-set branch, producing a merge commit.
2. Run `ears-manager change-set update` to record the new
   `base_commit`.
3. Re-run `ears-manager impact`, because the candidate set may
   have changed, and review any new candidate before continuing.

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
The same pull request records the answer in ADR-0001, so a reader
who starts from the decision record finds it.

The raw file diffs remain the normative record. The rendered
summary is a view of them, in the same way that the inspection
report is a view of the Finding Ledger.

### Gates and labels

- CI runs `ears-manager check` on every branch push and as the
  merge gate.
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
  locally. The merge alone is not sufficient
  ([Single-player mode](components.md#single-player-mode)).

Registration is idempotent by materialization key. Repeating it
with the same merge commit returns the prior result. If the merge
succeeds and the registration write fails, the retry is the same
registration call — never a second merge. A registration that
arrives with a different merge commit for the same change set is
rejected for reconciliation.

The Drafting Table never transitions a work item itself. It hands
the materializer a merge commit; the WMS Adapter applies
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
| Branch naming and creation | `cs/<nnn>-<slug>` from the default branch | Identical |
| Commit content, message, trailer | As above | Identical |
| Pull-request body | Rendered from `compare` and `impact` | Identical |
| How a change reaches the default branch | Pull request | Pull request |
| Who approves | The author merges their own pull request. No reviewer is required. | A reviewer merges; CODEOWNERS and required reviews apply |
| Registration trigger | Local `register-approved-change-set` | Merge hook on the default branch |
| Git host credential | The user's own Git host token | OAuth 2.1 through the Bridge/Gate pattern; the agent runtime never sees the credential |
| Merge strategy | Merge commit | Merge commit |
| Where code lands | Job Site merges `wi/` branches | Identical |

The credential rows follow the deployment topology in the
[WMS Adapter API](../architecture.md#wms-adapter-api) and the
credential isolation rules in
[Authentication and Credential Isolation][credential-isolation].
In hosted modes, the Gate enforces the branch restrictions that
the token format cannot express, so the allowlist in
[Permitted Git operations](#permitted-git-operations) is enforced
at the network boundary as well as in the Drafting Table.

The Web Drafting Table adds per-user session state and
authorization at the application boundary
([Web Drafting Table](../architecture.md#user-facing-interfaces)).
That changes where the working tree lives and who may act on a
project; it does not change any rule in this document.

---

## Ungoverned-edit detection

The Drafting Table never writes a registered specification file
directly, and neither does anything else. A file edited outside
`ears-manager` must be rejected or caught before it can reach the
default branch. Four layers do that, in order of how early they
fire.

| Layer | Where | Catches |
| --- | --- | --- |
| Harness tool permission rules (optional, #33) | The agent's own tool call | A write under a registered path before it happens |
| Pre-stage digest comparison | The Drafting Table, before staging | A registered path whose content no longer matches its registry digest |
| `ears-manager check` | Branch push and merge gate in CI | Malformed records, digest mismatches, dangling references, symmetry and cycle violations |
| Path ownership in CI | Merge gate | A change that edits files outside the owning component's paths |

### The pre-stage digest comparison

Before staging anything, the Drafting Table recomputes the content
digest of every registered artifact the change set touches and
compares it with the `digest` recorded in the registry.
`ears-manager` updates that digest on every governed write
([ADR-0002][adr2-registry]), so a mismatch means the file changed
by some other route.

When a registry entry names a directory rather than a file, the
digest covers that directory's canonical file set, so an added or
deleted record is a mismatch too. This holds for the requirement
store and for the change-set folder alike.

On a mismatch the Drafting Table:

1. stages nothing and commits nothing;
2. names each path whose digest does not match; and
3. offers the two routes forward — discard the direct edit
   (`git checkout -- <path>`), or bring the content in through
   `ears-manager artifact put` or the matching `requirement` or
   `interface` subcommand, which validates it and recomputes the
   digest.

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

The Specification Toolkit supplies tool definitions for
`ears-manager` and the WMS Adapter, but Git runs through the
harness's own file and shell tools
([Drafting Table Boundary](../architecture.md#drafting-table-boundary)).
There is no Git tool schema to constrain, so this allowlist is
what bounds the agent, and #33 may additionally deny the same
operations at the harness permission layer.

### Allowed

| Operation | Constraint |
| --- | --- |
| Initialize the control namespace | Through the `ears-manager` operation #30 must define; commits `.protobot/project.yaml` on a change-set branch |
| Read repository state | `status`, `log`, `diff`, `show`, `ls-files`, `rev-parse`, `merge-base` |
| Fetch | From `repository.canonical_remote` only |
| Create a change-set branch | Named `cs/<nnn>-<slug>`, cut from `repository.default_branch` |
| Stage | Registered artifact paths, the change-set manifest, `project.yaml`, and the `ears-manager` classification entries in `projection.yaml`, by explicit path |
| Commit | On explicit user request, with the required message and trailer |
| Push a change-set branch | Non-force, to the canonical remote only |
| Open or update a pull request | Against `repository.default_branch`, body rendered from `compare` and `impact` |
| Merge the default branch into the change-set branch | Merge commit; followed by `change-set update` |
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
| Fetching or pushing any repository other than the canonical remote | Project identity comes from the working tree, not from a caller-supplied remote |

Refusal is not advisory. In hosted modes, the same restrictions
are enforced by the Gate, and in every mode branch protection and
CI path ownership catch what reaches the host.

The Drafting Table's Git role is scoped to change-set branches and
nothing else. Workers and implementation-aware test agents receive
no Git mutation role at all, and only the Materializer, the
Integration/Merge service, and the WMS control plane receive the
narrow actions their current contract requires
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
diagnostic and the safe retry.

| Condition | Detection | Diagnostic | Safe retry |
| --- | --- | --- | --- |
| No `.protobot/project.yaml` found | Project resolution walks to the filesystem root | Names the directory searched and the expected path | Run project initialization, or start the session inside the project |
| `project.yaml` is not at the working-tree root | Project resolution | Names both the file location and the working-tree root | Move the session to the correct checkout; the Drafting Table never relocates the file |
| Store schema version newer than the tool | `ears-manager` reads `schema_versions` | Names the store, the file version, and the supported version | Upgrade `ears-manager`; migration is a reviewed change set, never automatic |
| Registered path digest mismatch | Pre-stage comparison | Names each path and both digests | Discard the direct edit, or re-apply it through `ears-manager` |
| Registered path missing from the projection manifest | `ears-manager check` | Names the path and the required class `shared` | Re-run the registration; `ears-manager` writes the classification entry and the Drafting Table stages `projection.yaml` with it |
| Branch `cs/<nnn>-<slug>` already exists | Branch creation | Names the branch and whether it is local, remote, or both | Resume that change set, or create the change set under a new ID |
| Default branch has moved since `base_commit` | `merge-base` check before push or merge | Names the recorded base and the current head | Refresh: merge the default branch in, then `change-set update` |
| Push rejected, non-fast-forward | Push exit status | Names the branch and the remote head | Refresh and push again; never force |
| Push rejected by branch protection | Push exit status | Names the protected branch | Push the change-set branch instead and open a pull request. A push to the default branch is a bug in the caller, not a state to retry |
| Merge refused by branch protection | Host API response | Names the protected branch, the failing requirement, and the declared `review_mode` | Satisfy the requirement, such as a green check or a review. If `review_mode` says `single-player` and the host still demands a reviewer, the declaration and the host disagree and the project configuration must be corrected |
| Push rejected, missing or expired credential | Push exit status | Names the remote and the credential source for the mode | Refresh the credential outside the agent; the agent never receives one directly |
| Pull-request creation failed | Host API response | Names the host status and whether the branch was pushed | Retry creation; the branch and its commits are already correct |
| `ears-manager check` failed in CI | Non-zero exit in the merge gate | The check's own diagnostics, by record | Fix through `ears-manager`, commit, push to the same branch |
| Merge conflict in a change-set manifest or index file | Merge of the default branch | Names the conflicting file | Resolve mechanically; sorted lists and fixed key order keep the resolution deterministic ([ADR-0001][adr1-diff]) |
| Merge conflict in a requirement record | Merge of the default branch | Names the record | Rare by design, since records are one file each; resolve through `ears-manager` and revalidate |
| Merge succeeded, registration failed | Registration call | Names the change set and the merge commit | Repeat the same registration call; it is idempotent by materialization key. Never merge again |
| Registration rejected, different merge commit | Materializer response | Names both merge commits | Reconcile; a change set has exactly one approved merge commit |

---

## Repository fixture

The fixture is a local **bare repository** plus one working clone.
It needs no Git host, no network, and no WMS backend. Issue #75
executes it as its test plan.

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
| 1 | Initialize the project: cut `cs/001-project-init`, write `project.yaml`, create `CS-001`, commit | The branch exists and is checked out. `.protobot/project.yaml` carries identity, `canonical_remote`, `default_branch`, `review_mode`, schema versions, and the four default registry entries. `.protobot/projection.yaml` carries a `shared` class for each of those four paths. One commit of three files, subject `spec(CS-001): <intent>`, trailer `Change-Set: CS-001`. The default branch is unchanged. `ears-manager check` exits zero. |
| 2 | Merge `CS-001`, register, then create change set `CS-002` for the initial Sketch | The default branch head is a merge commit. Branch `cs/002-<slug>` exists and is checked out. Its tip equals the new default-branch head, and the manifest records that head's full 40-character hash as `base_commit`. No other branch was created. |
| 3 | Write Vision and Architecture through `ears-manager artifact put` | Both registered paths exist. Their registry digests match their content. `ears-manager` has added a `shared` class for each new path. `git status` lists only the two artifacts, the manifest, `project.yaml`, and `projection.yaml`. |
| 4 | Edit a registered artifact directly with a text editor, then request a commit | Nothing is staged and no commit is created. The diagnostic names the path and both digests. `ears-manager check` exits non-zero for the same path. |
| 5 | Discard the direct edit and request a commit | Exactly one commit. It contains only the two artifacts, the manifest, `project.yaml`, and `projection.yaml`. Subject is `spec(CS-002): <intent>`; the body carries the `Change-Set: CS-002` trailer. |
| 6 | Push the branch and prepare the pull request | `origin` has `cs/002-<slug>` at the same commit; the default branch is unchanged. The rendered body contains the intent, the `base_commit`, every changed operation, every impact disposition with origin and rationale, `implementation_required`, and the file list. It matches the output of `change-set compare` and `impact`. |
| 7 | Commit an unrelated change on the default branch, then refresh the change set | The change-set branch gains a merge commit with two parents. The manifest's `base_commit` equals the new default-branch head. `git log --walk-reflogs` shows no rebase and the branch's first commit is unchanged. |
| 8 | Merge the branch into the default branch with a merge commit, then register | The default branch head is a merge commit with two parents. The registration stub recorded one call with the change-set ID, that merge commit, and the materialization key. Running registration again records no new call and returns the first result. A write to the merged manifest is refused. |

### Negative checks

| Check | Expected result |
| --- | --- |
| Force push the change-set branch | Refused by the Drafting Table; the remote branch is unchanged |
| Amend a pushed commit | Refused; the pushed commit is unchanged |
| Stage an unregistered file | Refused; the file stays untracked or unstaged |
| Create or write a `wi/` branch | Refused; no such ref exists in the fixture |
| Write under `.protobot/attestations/` | Refused; the path stays absent |
| Push to the default branch, in either `review_mode` | Refused before the push runs |
| Register a second, different merge commit for `CS-002` | Rejected for reconciliation; the first registration stands |
| Run every step in a clone with no IdeaBot material | Identical results; no step depends on IdeaBot input |

---

## Out-of-scope decisions

Listed so the decisions are traceable and do not silently
resurface.

| Decision | Rationale |
| --- | --- |
| `wi/` branch naming and lifecycle | The Job Site owns those branches. The open question in the [Content Storage Model](components.md#content-storage-model) stays open for that half. |
| Git host API binding for pull requests | This document names the operations. The concrete host client, its authentication, and its error mapping belong to the harness adapter (#33) and the deployment. |
| Bot account model for the Job Site | The Drafting Table commits with the user's identity, so the open question in [Multi-player Workflow](components.md#multi-player-workflow) is unchanged by this contract. |
| Merge queue or batching | Concurrent change sets follow the standard refresh-before-merge model. A Bors-style queue is [related work](related-work.md#gas-town--beads-steve-yegge), not a decision here. |
| Commit signing | Whether commits and merges must be signed is a project policy and deployment decision, not a Drafting Table behavior. |
| Directory layout inside the requirement store | Open with `ears-manager` ([`ears-manager`](components.md#ears-manager)). This document constrains which paths may be committed, not how the store organizes them. |
| `ears-manager` command and result shapes | Defined by #30. |
| Harness tool permission rules | Defined by #33. This document names the layer and its effect, not its configuration. |
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
- [User Interaction Flow](user-interaction-flow.md) — Phase
  details, sequence diagrams, and change types.
- [Open Design Questions](open-questions.md) — Unresolved design
  questions across all areas.
- [Related Work](related-work.md) — Internal and external
  projects informing the design.
- [ADR-0001](../decisions/0001-requirements-storage-format.md) —
  One-file-per-record YAML storage, the PR reviewability plan, and
  change-set history representation.
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md)
  — Record schemas, `base_commit`, and the artifact registry.

[adr1-diff]: ../decisions/0001-requirements-storage-format.md#1-git-diffmerge-compatibility
[adr1-history]: ../decisions/0001-requirements-storage-format.md#change-set-history-representation
[adr1-pr]: ../decisions/0001-requirements-storage-format.md#4-pr-reviewability-plan
[adr2-changeset]: ../decisions/0002-ears-specification-record-schema.md#change-set-manifests
[adr2-registry]: ../decisions/0002-ears-specification-record-schema.md#artifact-registry-entries
[adr2-versioning]: ../decisions/0002-ears-specification-record-schema.md#schema-versioning
[change-types]: user-interaction-flow.md#incremental-development-and-change-types
[credential-isolation]: components.md#authentication-and-credential-isolation
[demo-artifacts]: user-interaction-flow.md#demonstration-artifacts
[governed-tools]: ../architecture.md#governed-tool-integrations
[pr-merge-build]: components.md#the-pr--merge--build-model
[projections]: components.md#worker-repository-projections-decided
