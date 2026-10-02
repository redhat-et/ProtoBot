# ProtoBot: `ears-manager` CLI Integration Contract

> Architecture interface contract — September 2026
>
> Defines the command and result boundary used by the Specification Toolkit,
> Drafting Table, CI, and Job Site when they access governed specification
> state.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Boundary and authority](#boundary-and-authority)
- [Command grammar](#command-grammar)
- [Request and result protocol](#request-and-result-protocol)
- [Exit statuses and diagnostics](#exit-statuses-and-diagnostics)
- [Operation contracts](#operation-contracts)
- [Impact review protocol](#impact-review-protocol)
- [Atomicity and failure behavior](#atomicity-and-failure-behavior)
- [Golden fixture](#golden-fixture)
- [Acceptance evidence](#acceptance-evidence)
- [Decision for Q18](#decision-for-q18)
- [Related Documents](#related-documents)

---

## Purpose and scope

This document answers issue #30: _How does the Drafting Table use
`ears-manager` without editing governed specification artifacts directly?_

The contract covers the stable CLI boundary for:

- project initialization and project-root discovery;
- Vision, Architecture, and other registered artifacts;
- interface records;
- EARS requirement records;
- proposed change-set manifests;
- specification validation;
- deterministic change-set comparison; and
- deterministic impact analysis.

It defines the request grammar, success result, diagnostic result, output
formats, exit statuses, mutation authority, and retry behavior. It also
defines how deterministic impact candidates and agent-supplied semantic
candidates become reviewed dispositions.

This contract does not redefine:

- the one-file-per-record YAML storage format in
  [ADR-0001](../decisions/0001-requirements-storage-format.md);
- the logical record schema in
  [ADR-0002](../decisions/0002-ears-specification-record-schema.md);
- Git branch, commit, pull-request, or merge ceremony in
  [Git and Project-Repository Integration](git-integration.md); or
- WMS lifecycle validation and work-item mutation.

Callers use this CLI even when the underlying storage format, directory
layout, or validator implementation changes.

---

## Boundary and authority

### Callers

The same binary serves these callers:

| Caller | Reads | Writes |
| --- | --- | --- |
| Drafting Table through the Specification Toolkit | Proposed and approved specification state, comparison, impact candidates, validation results | Proposed specification records and change-set manifests on the active change-set branch, which `change-set create` cuts |
| CI | Registered records and artifacts at the checked-out revision | None |
| Job Site Materializer | The approved manifest and requirements at an immutable specification commit | None; it passes lifecycle state to the WMS Adapter |
| [Source Control Manager](source-control-manager.md) | A change set's paths, comparison, impact candidates, and validation result, and a change set's manifest, status, and manifest path at a commit | None; it stages, commits, and publishes what `ears-manager` wrote |
| Human maintainer | All data exposed by the read commands | The same proposed changes as the Toolkit, subject to the same validation |

The CLI is a local process over the caller's working tree. A hosted Drafting
Table may invoke it inside a controlled project workspace, but hosting does
not change the command or JSON contract.

### Read authority

`ears-manager` is the read authority for registered specification artifacts
and records. It resolves the project from the Git working tree containing
`.protobot/project.yaml`; a caller-supplied project name, branch, remote, or
path does not override that identity. Nor does Git's environment: the CLI
runs every Git command without the variables that name another repository,
index, or configuration, such as `GIT_DIR`, `GIT_WORK_TREE`, and
`GIT_CONFIG_PARAMETERS`. There are two exceptions. The
initialization command resolves the Git working-tree root before
`.protobot/project.yaml` exists, and a read at a commit with `--at` takes
`.protobot/project.yaml` from that commit, as described below.

Reads are root-relative and deterministic:

- `artifact get` reads registered content through the registry;
- `requirement`, `interface`, and `change-set` reads resolve IDs through the
  store; and
- `check`, `change-set compare`, and `impact` read the complete relevant
  store rather than trusting a caller-provided subset.

In the target contract, the optional `--at <full-commit-sha>` selector provides
read-revision semantics accepted only by read and analysis commands.
`change-set show` implements it; the other read and analysis commands defer it
to follow-on scope. It must name a full 40-character commit present in the
local repository. The default is the current working tree. A read at a commit
still resolves the Git repository from the working tree, but it takes
`.protobot/project.yaml`, `.protobot/projection.yaml`, the structured stores,
and the registered artifacts from the tree of that commit. The working tree
need not contain `.protobot/project.yaml`, and an uncommitted, untracked, or
deleted file there does not change the result. The CLI reads the commit through
the local `git` executable, ignores Git replace refs, and writes nothing under
`.git/`. It may stage that tree in a private temporary directory outside the
project root and removes it before it exits; that copy is not a governed write.

### Write authority

Only `ears-manager` writes these paths:

- `.protobot/project.yaml`;
- `.protobot/projection.yaml` classification entries for registered
  specification paths and configured store directories, except that a
  reviewed policy edit restores a missing entry or corrects a class (see
  [Projection classification](#projection-classification));
- registered Vision, Architecture, interface-IDL, and interface-prose
  artifacts;
- requirement and interface records; and
- proposed change-set manifests.

The CLI never writes `.protobot/test-catalog.jsonl`,
`.protobot/attestations/`, WMS state, credentials, or Worker projections. It
does not commit, push, open, or merge a pull request. The Git integration
contract owns those operations. The
[Source Control Manager](source-control-manager.md) performs the commit,
the push, and the pull-request operations; a person merges.
`change-set create` is the governed seam at which the Drafting Table gets a
change-set branch: it cuts the branch and checks it out before it writes the
manifest ([`change-set create`](#change-set-create)). Branch naming and
branch lifecycle follow [Git and Project-Repository
Integration](git-integration.md#change-set-branches).

The write destination allowlist is independent of Git staging. A write may
target a registered specification artifact, the registered requirement or
change-set stores, `.protobot/project.yaml`, or the classification entries in
`.protobot/projection.yaml`. New artifact paths may be project-owned
specification paths outside reserved control directories. The CLI rejects
`.git/`, `.github/`, `.protobot/attestations/`,
`.protobot/test-catalog.jsonl`, and other workflow or evidence paths even
when they resolve inside the project root. Git's deny-by-default projection
classification remains a second, independent enforcement layer; see [Git and
Project-Repository Integration](git-integration.md#path-rules).

Except for `project init`, every record or artifact write targets an
unapproved proposed change set. An approved manifest on the default branch is
immutable; attempting to update it is a conflict, not a new draft. The
[approval rule](#approved-and-proposed-change-sets) says which refs decide
that a manifest is on the default branch.

### Human approval checkpoint

The CLI records proposals and reviewed dispositions; it never grants
approval. The human approval checkpoint is the explicit approval of the exact
proposed change-set revision, followed by the Git pull-request merge defined
by #34. In single-player mode the author may merge their own pull request; in
multi-player and Web modes a reviewer merges it.

The Toolkit must therefore show the user, before requesting approval:

- the change-set ID and base commit;
- the complete `change-set compare` result;
- every changed operation;
- every deterministic and semantic impact candidate;
- each candidate's disposition and rationale;
- validation results from `check`; and
- the exact files and proposed branch to be handed to Git.

An individual accepted proposal, a successful CLI write, or a successful
validation command is not approval.

### Other component boundaries

The CLI's boundary with the other ProtoBot components is deliberately
narrow:

- The Drafting Table and Toolkit decide intent, semantic additions, and what
  to show the user.
- `ears-manager` validates specification content and persists proposed
  specification state.
- Git records the reviewed approval and repository history.
- Validation Rules and the WMS Adapter own work-item lifecycle transitions;
  `ears-manager` does not move WMS items.
- The Job Site may read approved specification state at an immutable commit,
  but it does not edit it.
- A Kit can propose content, but importing that content is an ordinary
  change-set write and requires the same review.

The binary uses the same boundary in single-player, multi-player, and Web
deployments. Hosted credential isolation remains the Bridge/Gate concern;
credentials never appear in CLI arguments, project configuration, JSON
results, or diagnostics.

---

## Command grammar

The canonical invocation is:

```text
ears-manager [--output human|json] <command> [<subcommand>] [options]
```

The default output is `human`. `--output` may occur before the command or
immediately after it. There are no command aliases in this contract. In
particular, comparison is spelled `change-set compare`, not a top-level
`compare` command.

The complete command surface is:

```text
ears-manager project init

ears-manager artifact put
ears-manager artifact get
ears-manager artifact list

ears-manager interface add
ears-manager interface list
ears-manager interface show
ears-manager interface update

ears-manager requirement add
ears-manager requirement list
ears-manager requirement show
ears-manager requirement update
ears-manager requirement retire

ears-manager change-set create
ears-manager change-set list
ears-manager change-set show
ears-manager change-set update
ears-manager change-set compare

ears-manager check
ears-manager impact
```

### EM-04 first-release scope

The command surface above is the target caller contract. The EM-04 first
release implements `check`, requirement add/list/show/update/retire,
interface add/list/show, artifact get/put, and minimal proposed change-set
creation. EM-05 adds change-set list/show/update/compare and `impact`,
including proposed-change-set impact-completeness checks. Issue #206 adds the
immutable `--at` read to `change-set show`. EM-06 adds `project init` and the
`shared` projection classification that `project init` and `artifact put`
write and `check` validates. EM-07 (#114) adds the change-set branch cut to
`change-set create`. Artifact listing, interface updates, `--at` reads on the
other read and analysis commands, and explicit `--against` comparison
revisions remain follow-on work. The dispatcher must not claim those remaining
operations are available. The CLI never commits, pushes, or opens a pull
request; the Source Control Manager does ([Write authority](#write-authority)).

`change-set create` allocates the ID, records the default-branch head as the
base commit, cuts and checks out the change-set branch, and writes the
manifest on it, so its success data contains `id`, `base_commit`, `branch`,
and `manifest_path` ([`change-set create`](#change-set-create)).

Every command accepts `--help`. Help is a read-only successful operation and
exits with status `0`. `--version` is accepted at the top level and prints
the binary version without reading the project.

### Common option rules

- IDs, enum values, and paths use the names and constraints in ADR-0002.
- A repeated option is written once per value; list ordering in output is
  canonical and does not depend on input order.
- Paths are relative to the resolved project root, use `/` separators, and
  must remain inside that root after symlink resolution.
- Full Git commit selectors use lowercase or uppercase hexadecimal, but JSON
  output uses lowercase hexadecimal.
- A write command that needs a proposed change set requires
  `--change-set CS-<NNNNN>`. The command never silently selects an unrelated
  active change set.
- Path containment is validated before any read or write. Absolute paths,
  `..` escapes, symlink-resolution failures, and reserved control/workflow
  paths in artifact operations return `artifact.invalid_path` or
  `artifact.write_not_allowed`, status `4`, and `mutation: "none"`.
  `project init --vision` and `--architecture` use `project.invalid_path` for
  the same containment failures. Diagnostics never echo an unsafe or
  credential-bearing path.
- `project init` accepts credential-free `https://` and `ssh://` remotes and
  the standard `git@host:path` SSH form. URL passwords, tokens, and other
  embedded secret userinfo are rejected with
  `project.remote_credentials`, status `4`, and no echoed remote value.
- Record-creation commands require a caller-supplied `--created` timestamp
  in the ADR-0002 ISO 8601 format. The CLI does not add an invocation
  timestamp to a result envelope, which keeps replayed JSON deterministic.
- Text values may be supplied as ordinary option values. `artifact put` also
  accepts `--content-file PATH` or `--content-stdin`; the input source is
  never itself a registered destination.
- `--content-file PATH` and `--impact-file PATH` are caller-owned read
  sources, not project destinations. They may be outside the project root or
  use `-` for stdin, are opened read-only as regular files, and are never
  written or staged. A source that is missing, a directory, or a symlink is
  rejected with `input.invalid_source`, status `4`, and `mutation: "none"`.

### Project initialization grammar

```text
ears-manager project init \
  --id PROJECT-ID \
  --name PROJECT-NAME \
  --canonical-remote URL \
  --review-mode single-player|multi-player \
  [--default-branch BRANCH] \
  [--branch-prefix PREFIX] \
  [--vision PATH] \
  [--architecture PATH]
```

The defaults are `main`, `cs/`, `docs/vision.md`, and
`docs/architecture.md`. Initialization creates the `.protobot/` control
namespace, seeds the version-1 schema keys, the default `stores` block, and
the initial `store_digests` values, registers the opaque Vision and
Architecture artifacts, and classifies the registered specification paths
and the configured store directories as `shared` in
`.protobot/projection.yaml`. It does not create content, commit, push, or
merge anything. The caller follows the project-initialization sequence in
[Git and Project-Repository Integration][git-init].

The selected Vision and Architecture paths must already exist so initialization
can compute their registry digests without writing content. They must also be
committed at `HEAD` with no uncommitted change, because the initialization
commit holds only the control namespace; an untracked, only staged, or
modified file would be missing or stale there and fail `check` in CI. The
fixture seeds those files as committed project content. A missing or
uncommitted selected path returns `project.invalid_path` before the control
namespace is created.

`--canonical-remote` must use a credential-free `https://` or `ssh://` URL,
or the standard `git@host:path` SSH form. Any URL userinfo, including a
username without a password, is rejected for `https://` and `ssh://` URLs.
For SCP-style remotes, only the fixed `git@host:path` form is accepted.
`project init` and `check` reject other userinfo with
`project.remote_credentials`, status `4`, and no echoed remote value.

`--branch-prefix` may not be the reserved `wi/` prefix or another prefix
reserved by the project repository contract. `project init` rejects a
reserved prefix with `project.invalid_configuration` before writing the
control namespace; `check` applies the same rule to existing configuration.

### Record mutation grammar

The following options are the canonical request fields. The command may
reject a field that is not applicable to its record type.

```text
requirement add --change-set CS-ID --id REQ-ID --type TYPE --text TEXT \
  [--interface INTERFACE-ID]... [--scope SCOPE]... \
  --verification-mode isolated-interface|implementation-aware \
  [--verification-rationale TEXT] --provenance PROVENANCE --created ISO8601 \
  [--relationship TYPE=REQ-ID]...

requirement update --change-set CS-ID --id REQ-ID [record fields...]
requirement retire --change-set CS-ID --id REQ-ID

interface add --change-set CS-ID --id INTERFACE-ID --name NAME --type TYPE \
  --created ISO8601 [--spec-approach TEXT] [--description TEXT]
interface update --change-set CS-ID --id INTERFACE-ID [record fields...]

artifact put --change-set CS-ID --id ARTIFACT-ID --kind KIND --path PATH \
  --owner OWNER [--validator VALIDATOR] \
  (--content-file PATH | --content-stdin)

change-set create --intent TEXT [--affected-interface ID]... \
  [--affected-scope SCOPE]... --implementation-required true|false \
  [--implementation-rationale TEXT] --created ISO8601
change-set update --change-set CS-ID [--intent TEXT] [--affected-interface ID]... \
  [--affected-scope SCOPE]... [--base-commit FULL-SHA] \
  [--implementation-required true|false] \
  [--implementation-rationale TEXT] [--impact-file PATH|-]
```

`requirement update` preserves the record's stable ID and creation metadata.
`interface update` preserves its stable ID and creation metadata; setting
`status: retired` is the interface-retirement operation. A relationship
write validates both sides when ADR-0002 requires symmetric storage.

The `--impact-file` value is a temporary, caller-owned JSON document whose
top-level value is a list of complete `ImpactAssessment` objects. It is not
stored as a separate artifact. Supplying it replaces the draft assessment
atomically, after `impact` has been rerun and all entries have a final
disposition.

### Read and analysis grammar

```text
artifact get (--id ARTIFACT-ID | --kind KIND) [--at FULL-SHA]
artifact list [--kind KIND] [--owner OWNER] [--at FULL-SHA]

interface list [--type TYPE] [--status STATUS] [--at FULL-SHA]
interface show --id INTERFACE-ID [--at FULL-SHA]

requirement list [--interface ID] [--scope SCOPE] [--type TYPE] \
  [--status STATUS] [--relationship RELATIONSHIP] [--at FULL-SHA]
requirement show --id REQUIREMENT-ID [--at FULL-SHA]

change-set list [--status STATUS] [--interface ID] [--scope SCOPE] \
  [--at FULL-SHA]
change-set show --change-set CS-ID [--at FULL-SHA]
change-set compare --change-set CS-ID [--against FULL-SHA]

check [--at FULL-SHA] [--change-set CS-ID]
impact --change-set CS-ID [--at FULL-SHA]
```

`--id` identifies a requirement, interface, or artifact. `--change-set`
identifies a change set in every command that operates on an existing change
set. `--at` selects the immutable read revision; `--against` selects the
comparison baseline. `change-set show` accepts `--at`; `--at` on the other
commands and `--against` remain deferred to follow-on scope.

---

## Request and result protocol

### Human output

Human mode is intended for a maintainer at a terminal. Successful commands
print a stable summary to stdout. Failed commands print no success summary
and write this diagnostic form to stderr:

```text
error[<code>]: <message>
at <root-relative-path>[:<field>]: <detail>
hint: <safe next action>
mutation: none|unknown
retry: <retry policy>
```

The `at`, `hint`, `mutation`, and `retry` lines are omitted when they do not
apply. Diagnostics never include absolute paths, credentials, raw YAML from
another record, or a caller-supplied command line.

### JSON output

With `--output json`, stdout contains exactly one JSON document and a final
newline for both success and failure. The CLI writes no progress or human
diagnostics to stdout. JSON object keys use the order shown below, arrays are
sorted by the ordering specified by the operation, and no map iteration or
invocation timestamp is exposed.

Successful result envelope:

```json
{
  "schema_version": 1,
  "ok": true,
  "command": "requirement list",
  "data": {},
  "diagnostics": [],
  "mutation": {
    "applied": false,
    "paths": []
  }
}
```

Failed result envelope:

```json
{
  "schema_version": 1,
  "ok": false,
  "command": "requirement add",
  "error": {
    "code": "validation.failed",
    "message": "The specification is not valid.",
    "exit_code": 4,
    "diagnostics": [],
    "mutation": "none",
    "retry": "revise-request"
  }
}
```

`data` is operation-specific. `diagnostics` on a successful response contains
warnings that did not prevent the operation. A failure's `error.diagnostics`
contains structured errors sorted lexicographically by path, then code, then
record ID, field, severity, message, and hint. The process exit status
and `error.exit_code` are the same value.

### Diagnostic entries

Each structured diagnostic has this shape:

```json
{
  "code": "requirement.ears_pattern_mismatch",
  "severity": "error",
  "path": ".protobot/requirements/REQ-CLI-00001.yaml",
  "record_id": "REQ-CLI-00001",
  "field": "text",
  "message": "Text does not match the declared event-driven pattern.",
  "hint": "Start the statement with 'When ...'."
}
```

`path`, `record_id`, and `field` are optional. Codes are stable identifiers,
not localized prose. Implementations may add diagnostic codes, but they must
not change the meaning of an existing code within schema version `1`.

---

## Exit statuses and diagnostics

The process status is part of the contract:

| Status | Class | Examples | Safe retry |
| ---: | --- | --- | --- |
| `0` | Success | Read succeeded, validation passed, or a write was applied | No retry needed |
| `2` | Usage | Unknown command/option, missing option, malformed option value | Correct the request; no mutation occurred |
| `3` | Project | Project not initialized, project outside Git root, unsupported store version, invalid project configuration or repository state, unresolved default branch | Select or upgrade the project; no mutation occurred |
| `4` | Validation | Invalid EARS text, missing required metadata, dangling reference, invalid relationship, invalid artifact content, invalid base commit, a structured store or registered artifact that does not match its digest, an `--at` value that is not a full hash or names no local commit | Revise the proposed request; no mutation occurred |
| `5` | Conflict or stale state | Approved manifest, duplicate ID, branch conflict, changed base, impact assessment no longer matches candidates | Refresh and review; no mutation occurred |
| `6` | I/O or external boundary | Permission failure, unreadable input, Git read failure, atomic write failure with known rollback | Fix the environment, then retry after checking state |
| `70` | Internal failure | Unexpected invariant or serialization failure | Do not blindly retry; retain the diagnostic for implementation triage |

An operation that cannot establish whether a write happened returns status `6`
with `mutation: "unknown"` and a reconciliation instruction. It never claims
that the requested state was applied.

Warnings do not change a successful status. A command that reports candidates,
duplicates, or conflicts as analysis data still exits `0`; malformed input or
an invalid store exits non-zero.

---

## Operation contracts

The tables below define the minimum request and result for every Toolkit-used
operation. Fields inherited from ADR-0002 are not repeated in full. Every
write to an existing change set can also return the diagnostics of the
[approval rule](#approved-and-proposed-change-sets) and of the
[ancestry check](#ancestry-check).

### `project init`

| Request | Success result | Diagnostic result |
| --- | --- | --- |
| Project ID, name, canonical remote, review mode, and optional path/branch defaults | Project identity, repository settings, version-1 schema keys, store paths and integrity digests, registered artifact IDs/paths, and changed paths | `project.already_initialized`, `project.not_git_root`, `project.invalid_path`, `project.remote_credentials`, or `project.invalid_configuration` |

The operation is the only write that does not require an existing project
configuration or `--change-set`. It is atomic across `project.yaml` and
`projection.yaml`. The initial change set and Git branch follow the #34
sequence; initialization itself does not approve or commit the project.

Initialization adopts an existing repository. It registers the files already
at the selected Vision and Architecture paths without moving or rewriting
them, computes the store digests of the configured stores as they are, and
creates no store directory. It leaves Git history, the index, and every
other working-tree path untouched.

Diagnostic results use these statuses:

| Code | Status | Condition |
| --- | ---: | --- |
| `project.not_git_root` | `3` | The current directory is not in a Git working tree, or a `.protobot/project.yaml` exists in a directory between the current directory and the working-tree root. The command walks up from the current directory only, as [project resolution](git-integration.md#the-project-root) does; a `.protobot/project.yaml` elsewhere below the root, such as in a nested clone or a test fixture, does not block it. The diagnostic names the misplaced file by its root-relative path; the command never relocates it. |
| `project.invalid_configuration` | `3` | The repository has no commit, or the selected default branch does not resolve to a commit. An empty repository is initialized outside this contract. |
| `project.already_initialized` | `5` | Anything named `.protobot` exists at the working-tree root, including a symbolic link or a file. |
| `project.invalid_path` | `4` | A selected path is unsafe, reserved, missing, not a regular file, selected twice, not a file in the `HEAD` commit, or different in the working tree from `HEAD`. |
| `project.remote_credentials` | `4` | The canonical remote carries userinfo other than the fixed `git@host:path` form. |
| `project.invalid_configuration` | `4` | Another request value is invalid, such as a reserved branch prefix, an unknown review mode, an unsupported remote, or selected content that is not UTF-8 text without a BOM. |

`project.invalid_configuration` carries two statuses. Status `3` means the
repository state cannot hold a project yet: correct the repository, then
retry. Status `4` means a request value is invalid: revise the request. The
Source Control Manager's `ears-manager` test stub follows the same split.

#### Projection classification

`projection.yaml` is a YAML mapping. Its integer `version` key names the
manifest format; this contract defines version `1`. The version belongs to
the manifest, not to `schema_versions` in `project.yaml`. A manifest without
`version: 1` is refused as `projection.invalid`. Its `paths` key lists
entries with exactly the keys `path` and `class`, and each `class` is one of
the projection classes defined by [Worker repository
projections](components.md#worker-repository-projections-decided). Other
top-level keys are reviewed project policy.

A trailing `/` on a `path` names a directory, and a directory entry
classifies every path below it. A path and the same path with a trailing
`/` are one entry, so listing both is `projection.invalid`. A path takes the
class of its most specific entry: its own entry, else the entry of its
nearest ancestor directory. A file entry therefore overrides the entry of the
directory that holds it.

`project init` writes a `shared` entry for each registered specification
path and a directory entry for each configured store directory: requirement,
interface, and change-set. `artifact put` adds a `shared` entry only for the
path it registers, when no entry already covers that path as `shared`, in
the same transaction as the registry write. `ears-manager` never adds an
entry for another path, and never changes or removes an entry or key.
`check` and every write that validates the full project report a registered
path or a configured store directory that is not covered as `shared` as
`projection.unclassified`, naming the path and the required class, and a
manifest outside this format as `projection.invalid`.

A missing entry, for example after a merge conflict in `paths` or after a
person deletes the manifest, is not repaired by `ears-manager`. A reviewed
policy edit of `projection.yaml` restores it, in the same way as a class
correction; the diagnostic hint names that route. The Source Control
Manager keeps such a policy edit out of a change-set commit
([`commit`](source-control-manager.md#commit)), so the person commits it
apart and it merges through its own review.

### Artifacts

| Command | Request | Success result | Diagnostic result |
| --- | --- | --- | --- |
| `artifact put` | Change set, artifact ID/kind/path/owner, optional validator registry name, and UTF-8 content | The complete registry entry, content digest, changed paths, and change-set artifact operation | `artifact.unknown_kind`, `artifact.invalid_id`, `artifact.invalid_path`, `artifact.invalid_content`, `artifact.owner_immutable`, `artifact.validator_not_allowed`, `artifact.validator_incompatible`, `artifact.write_not_allowed`, `projection.invalid`, `projection.unclassified`, or a validator diagnostic |
| `artifact get` | Exactly one of artifact ID or kind, where kind must match one opaque artifact entry; optional `--at` | Registry entry and UTF-8 content | `artifact.not_found`, `artifact.ambiguous`, or `artifact.read_failed` |
| `artifact list` | Optional kind/owner filter and `--at` | Registry entries sorted by artifact ID; content is not included | `project.invalid_configuration` or `artifact.read_failed` |

`artifact put` is the only route for Vision, Architecture, interface prose,
and external interface-IDL content. It updates the registry digest, records
the artifact operation in the change-set manifest, and, for a new registered
path, adds the `shared` projection classification of that path only in the
same transaction. It refuses to register a path while `projection.yaml` is
malformed, reporting `projection.invalid`. It
accepts only a stable validator registry name; the selected code-controlled
adapter may invoke an approved external tool with fixed arguments, but a
caller-supplied executable or command line is never executed.

### Interfaces

| Command | Request | Success result | Diagnostic result |
| --- | --- | --- | --- |
| `interface add` | Change set plus an ADR-0002 interface record | The complete interface record and `interface_operations` entry | `interface.duplicate_id`, `interface.invalid_id`, `interface.invalid_type`, or `change_set.not_proposed` |
| `interface list` | Optional type/status filter and `--at` | Active or requested interface records sorted by ID | `interface.read_failed` |
| `interface show` | Interface ID and optional `--at` | The complete interface record | `interface.not_found` |
| `interface update` | Change set, stable ID, and changed record fields | Before/after record summary and the manifest operation | `interface.not_found`, `interface.immutable_field`, or validation diagnostics |

An interface record is not a lifecycle work item. Setting its record status to
`retired` is a specification operation and does not change WMS state.

### Requirements

| Command | Request | Success result | Diagnostic result |
| --- | --- | --- | --- |
| `requirement add` | Change set plus ID, EARS type/text, applicability selectors, verification, provenance, timestamp, and optional relationships | The complete new record and the `add` operation | `requirement.duplicate_id`, `requirement.invalid_id`, `requirement.invalid_applicability`, EARS diagnostics, or relationship diagnostics |
| `requirement list` | Optional interface, scope, type, status, relationship, and `--at` filters | Matching records sorted by stable requirement ID | `requirement.read_failed` |
| `requirement show` | Requirement ID and optional `--at` | The complete requirement record | `requirement.not_found` |
| `requirement update` | Change set, stable ID, and replacement fields | Complete before/after records and the `revise` operation | `requirement.not_found`, `requirement.immutable_field`, or validation diagnostics |
| `requirement retire` | Change set and stable ID | Complete before/after records and the `retire` operation | `requirement.not_found`, `requirement.already_retired`, or impact/reference diagnostics |

All requirement mutations validate the complete affected relationship graph
before writing. Retirement preserves the record and its historical ID.
Retirement does not imply that implementation code must be removed; the
change set's `implementation_required` and rationale make that decision
explicit.

### Change sets

| Command | Request | Success result | Diagnostic result |
| --- | --- | --- | --- |
| `change-set create` | Intent, affected interfaces/scopes, implementation decision, and `--created` | Change-set ID, full base commit, branch, and manifest path; not the manifest body | `change_set.no_base`, `project.default_branch_unresolved`, `change_set.invalid_scope`, `change_set.base_mismatch`, `change_set.branch_exists`, `change_set.unexpected_branch`, `change_set.uncommitted_changes`, `change_set.sequence_exhausted`, `git.read_failed`, `git.write_failed`, `git.write_unknown`, or project diagnostics |
| `change-set list` | Optional status, interface, and scope filters, and optional --at (deferred to follow-on scope) | Proposed/approved manifests sorted by ID | `change_set.read_failed`, `project.default_branch_unresolved`, or `git.read_failed` |
| `change-set show` | `--change-set CS-ID` and optional `--at` full commit | Complete manifest, derived status, changed/applicable counts, and exact paths, each a file: every registered artifact and every structured requirement and interface record that the change set touches, and its manifest | `change_set.not_found`, `git.read_failed`, `project.default_branch_unresolved`, `project.invalid_configuration`, `revision.invalid`, `revision.not_found`, or `revision.read_failed` |
| `change-set update` | `--change-set CS-ID` plus metadata, `--base-commit`, or complete impact assessment | `before`, `after`, `assessment_status`, and `changed_paths` in the result | `change_set.not_proposed`, `change_set.base_mismatch`, `change_set.invalid_base`, `change_set.invalid_impact`, `git.read_failed`, or validation diagnostics |
| `change-set compare` | `--change-set CS-ID` and optional `--against` full commit (deferred to follow-on scope) | Deterministic comparison report described below | `change_set.not_found`, `change_set.invalid_base`, or read/validation diagnostics |

`change-set create` allocates the next sequence number, cuts and checks out
the change-set branch, and writes the manifest on it, with the default-branch
head as `base_commit` and as `impact_assessment_base_commit`. A creation that
fails with `mutation: "none"` leaves no manifest and no branch. [`change-set
create`](#change-set-create) gives the rules.

Every successful `change-set update` returns a `before` and `after` manifest
summary, the resulting `assessment_status`, and sorted `changed_paths`.
`change-set update --impact-file` replaces the complete impact assessment in
one operation and sets `impact_assessment_base_commit` to the `base_commit`
that the same update leaves in the manifest. The file must contain a final
`applicable` or
`not-applicable` disposition, a rationale, and an origin of `mechanical` or
`semantic` for every entry. A semantic entry must name an unchanged active
requirement not already in the changed operations. An empty list is the
reviewed assessment of a change set with no candidates.

#### Approved and proposed change sets

A change set is approved when its manifest path is in the tree of one of
these refs, each when it resolves to a commit:

- the local default branch, `refs/heads/<default_branch>`; or
- the canonical remote's default branch,
  `refs/remotes/<remote>/<default_branch>`, for each `<remote>` whose
  configured fetch URL, with any userinfo other than the fixed `git@` of the
  SCP form removed, equals `repository.canonical_remote`.

Any other change set is proposed. The URL match is the one the [Source
Control Manager](source-control-manager.md#operation-matrix) uses for its
`<remote>`. The comparison is exact text: a `.git` suffix, letter case, and a
trailing `/` are not normalized. Unlike the Source Control Manager,
`ears-manager` reads every remote that matches, takes the first configured
URL of each, and refuses none, because it only reads. A remote with another
URL, such as a fork named `origin`, never decides approval.

The merge is the approval event; the refs only let `ears-manager` see it.
`ears-manager` never fetches, so the remote-tracking ref shows the canonical
remote as of the last fetch. So after a fetch, `ears-manager` reports a
change set whose pull request merged on the host as approved, even while the
local default branch is behind.

Every command that tells approved from proposed change sets uses this rule:
`change-set list` and `change-set show` report the status (`change-set show
--at` asks the same refs about a commit, as [`change-set
show`](#change-set-show) describes), `check` requires
impact completeness only for the proposed change sets, and every write to an
existing change set refuses an approved one with `change_set.not_proposed`,
status `5`, and `mutation: "none"`. When no ref resolves to a commit, such a
command fails with `project.default_branch_unresolved`, status `3`, and does
not treat every change set as proposed. A failure to read the remotes is
`git.read_failed`, status `6`. A caller that runs such a command in a clone
without the local default branch, such as CI, first fetches the default
branch from a remote whose configured URL equals
`repository.canonical_remote`.

#### Ancestry check

Every write to an existing proposed change set runs an ancestry check of
`HEAD` against the manifest's `base_commit`. A change-set branch can hold more
than one commit: it is [updated by further
commits](git-integration.md#branch-lifecycle) under review, and it gains a
merge commit when it is [refreshed from the default
branch](git-integration.md#refreshing-from-the-default-branch). So the check
asks for ancestry, not equality. An ancestor of a commit is a commit reachable
from it, and a commit is its own ancestor, as in
`git merge-base --is-ancestor`. The check ignores Git replace refs.

The check reads the recorded `base_commit` first:

- A manifest without a `base_commit` is refused with
  `change_set.not_proposed`, status `4`.
- Outside a shallow clone, a `base_commit` that is not the full 40-character
  ID of a commit in the local repository is refused with
  `change_set.invalid_base`, status `4`. The
  diagnostic names the value only when it is a full 40-character hexadecimal
  ID, because the value comes from a file.

Then it applies the ancestry rule:

- A write without `--base-commit` is accepted when `base_commit` is an
  ancestor of `HEAD`. This covers the first write after `change-set create`,
  a write after further commits on the change-set branch, and a write after
  the refresh merge but before `change-set update --base-commit`.
- `change-set update --base-commit X` is accepted when `X` is an ancestor of
  `HEAD` and the recorded `base_commit` is an ancestor of `X`. After the
  refresh merge, `X` is the default-branch head that the merge brought in.
  So `base_commit` moves forward along the change-set branch, never back or
  sideways. A new `base_commit` makes the impact assessment `stale` until a
  reviewed `change-set update --impact-file` records the assessment against
  the new base ([Impact review protocol](#impact-review-protocol)).

`change_set.base_mismatch`, status `5`, and `mutation: "none"` refuse a write
in each of these cases:

- `base_commit` is not an ancestor of `HEAD`, for a write without
  `--base-commit`. For example, `HEAD` is on a branch that forked before
  `base_commit`.
- `X` is not an ancestor of `HEAD`. For a refresh, the default branch is then
  not merged in yet.
- The recorded `base_commit` is not an ancestor of `X`. `base_commit` would
  then move back or sideways.
- `HEAD` moved while the command prepared its write. This case also applies
  to `change-set create`.

The third case means that `change-set update --base-commit` cannot repair a
recorded `base_commit` that is not an ancestor of the new base, such as a
commit that the default branch does not contain. No command in this contract
repairs such a base.

A value of `X` that is not the full 40-character ID of a commit in the local
repository, such as the ID of an annotated tag, is refused with
`change_set.invalid_base`, status `4`.

In a shallow clone, a recorded `base_commit` that is not a local commit, and
any answer that a commit is not an ancestor, is `git.read_failed`, status
`6`, with `mutation: "none"`, because the history is incomplete. Fetching the
full history lets the check answer. A failure of `git merge-base` itself is
`git.read_failed` too.

`ears-manager` does not check that `base_commit` is on the default branch.
The [Source Control Manager's `publish`](source-control-manager.md#publish)
refuses a `base_commit` that is not reachable from the default branch of the
canonical remote (`BASE_NOT_ON_DEFAULT`), and a `base_commit` that differs
from the default-branch head reachable from `HEAD` (`BASE_COMMIT_STALE`).

### `change-set create`

```text
ears-manager change-set create --intent TEXT [--affected-interface ID]... \
  [--affected-scope SCOPE]... --implementation-required true|false \
  [--implementation-rationale TEXT] --created ISO8601
```

`change-set create` starts a change set on its own branch
([One branch per change set](git-integration.md#one-branch-per-change-set)).
It reads the head of the local default branch,
`refs/heads/<default_branch>`, and records that commit as `base_commit` and
as `impact_assessment_base_commit`. It never takes the base from `HEAD`, and
it never fetches: the Source Control Manager's
[`repo_state`](source-control-manager.md#repo_state) fetches and
fast-forwards the local default branch first
([When the branch is created](git-integration.md#when-the-branch-is-created)).

There are two cases:

- **Initialization.** `HEAD` is on `<branch_prefix>00001-project-init`, and
  the change-set store holds no manifest. The Source Control Manager's
  `branch_init` cut that branch before `project init` wrote the project
  ([Project initialization](git-integration.md#project-initialization)), so
  `change-set create` records it as the branch of `CS-00001` and cuts
  nothing. The branch must descend from the default-branch head.
- **Every other change set.** The sequence number is one more than the
  highest in use: in the manifests of the working tree, in the change-set
  store at every default-branch ref of the
  [approval rule](#approved-and-proposed-change-sets), and in the name of
  every change-set branch, local or a remote-tracking ref of the
  canonical remote. So the new ID names no manifest and no branch that the
  checkout knows, and a change set under review on another branch keeps its
  number. Two clones that have not fetched each other's change-set branches
  can still allocate the same number. Their manifests then share one path,
  so Git refuses the second merge with a conflict. The branch is
  `<branch_prefix><nnnnn>-<slug>`. The command creates it at the
  default-branch head, checks it out, reads the project there, and writes the
  manifest on it.

The command runs its Git writes with no hook and no `fsmonitor` program, the
rule of the Source Control Manager
([Design principles](source-control-manager.md#design-principles)), so a
repository cannot ship a program that runs when the branch is cut. It checks
the branch out rather than only writing the ref, so the index and the working
tree move to the default-branch head with it. It ignores Git replace refs,
as a read at a commit does, so the new branch starts from the tree of the
recorded base commit.

A refusal changes nothing and reports `mutation: "none"`:

| Code | Status | Condition |
| --- | ---: | --- |
| `project.default_branch_unresolved` | `3` | `refs/heads/<default_branch>` does not resolve to a commit |
| `change_set.no_base` | `5` | `HEAD` names no commit, as in a repository without one |
| `change_set.unexpected_branch` | `5` | `HEAD` is detached, as during a rebase or a bisect. Or the default branch holds no `.protobot/project.yaml` yet, and the initialization case does not apply: `HEAD` is on another branch, or `CS-00001` exists but is not merged |
| `change_set.base_mismatch` | `5` | In the initialization case, the initialization branch does not descend from the default-branch head, because the default branch moved after the cut. In either case, `HEAD` moved while the command prepared its write |
| `change_set.uncommitted_changes` | `5` | Every other change set only: a tracked file has an uncommitted change, staged or not, which would move one change set's draft to the branch of another; or the checkout would overwrite an untracked file. Other untracked files stay in the working tree |
| `change_set.branch_exists` | `5` | The branch exists locally or on the canonical remote, as of the last fetch; the diagnostic says which. The allocation skips every number that a known branch uses, so another writer created the branch during the command |
| `change_set.sequence_exhausted` | `5` | No sequence number up to `99999` is free |
| `git.read_failed` | `6` | Git could not read the status, the refs, the remotes, or the change-set store at a default-branch ref |
| `git.write_failed` | `6` | Git could not cut or check out the branch, and the original branch is checked out again |

A failure after the cut, such as a project at the default-branch head that
does not validate, checks the original branch out again and deletes the new
branch. The result is that failure, with `mutation: "none"`. When the command
cannot establish that state, the result is `git.write_unknown`, status `6`,
with `mutation: "unknown"`; the caller checks `git status` and the branch list
before it retries. A manifest write whose own rollback fails returns
`storage.write_unknown`, as every write does, and leaves the new branch checked
out, so the caller finds the files where they were written.

### `change-set show`

```text
ears-manager change-set show --change-set CS-ID [--at FULL-SHA]
```

`change-set show` is read-only. Without `--at`, it reads the working tree.
With `--at`, it reads the manifest, `.protobot/project.yaml`, and the other
records from the tree of that commit, as
[Read authority](#read-authority) describes. Both reads return the same
fields, and the same load rules apply: a symbolic link, a directory, or a
submodule in a structured store at that commit is refused as it is in the
working tree.

`status` is derived from Git, because approved specification state is the
state of the default branch and the merge is the approval event
([ADR-0002](../decisions/0002-ears-specification-record-schema.md#change-set-manifests);
[Git and Project-Repository
Integration](git-integration.md#the-merge-is-the-approval-event)). Both rules
read the default-branch refs of the
[approval rule](#approved-and-proposed-change-sets):
`refs/heads/<default_branch>`, and `refs/remotes/<remote>/<default_branch>`
for each remote whose configured fetch URL is `repository.canonical_remote`.

- Without `--at`, the manifest is `approved` when any of those refs holds its
  path. Otherwise it is `proposed`.
- With `--at`, the manifest is `approved` when the commit is on the default
  branch: any of those refs is that commit or a descendant of it. Otherwise
  it is `proposed`. `default_branch` and `canonical_remote` are read from that
  commit, as a working-tree read takes them from the working tree. The remotes
  are always the ones the local repository configures.

So a manifest is `proposed` at a change-set branch tip before the merge, and
`approved` at the merge commit and at every later default-branch commit.
Every ref counts in both rules, so a local default branch that is behind the
canonical remote's branch does not hide a merge. A remote with another URL,
such as a fork named `origin`, counts in neither rule. When no ref resolves,
the read fails with `project.default_branch_unresolved` and status `3`. Both
rules check `default_branch` against the Git branch-name rule that `check`
applies before any Git command runs, because Git would read a name such as
`main~1` or `main@{1}` as another commit. A name that fails the rule is
`project.invalid_configuration` with status `3`. With `--at`, "descendant"
uses the ancestry test of the [ancestry check](#ancestry-check): it ignores Git
replace refs. One ref that proves the commit is on the default branch is
enough. In a shallow clone, when no ref proves it, the answer is
`git.read_failed`, status `6`, because the history is incomplete.
`approved` states only that the commit is reachable from a default-branch
ref. It does not prove that a reviewed pull request merged it; the
[Source Control Manager](source-control-manager.md#approved-state-read-face)
confirms that with the Git host.

`--at` refuses a value that is not a full 40-character hexadecimal hash,
such as a short hash, a branch or tag name, or `HEAD`, with
`revision.invalid`. It refuses a full hash that names no commit in the local
repository, such as an unknown object, a tree, or an annotated tag, with
`revision.not_found`.
Both use status `4` and `mutation: "none"`, as a malformed or unknown
`--base-commit` does. `revision.read_failed`, with status `6`, reports a Git
failure while the tree is read, or a private copy that cannot be created,
for example in a sandbox without a writable temporary directory. A commit
whose tree has no manifest of the change set, or no
`.protobot/project.yaml`, gives `change_set.not_found` with status `4`, the
same result as a working tree without the manifest.

### `check`

```text
ears-manager check [--at FULL-SHA] [--change-set CS-ID]
```

`check` is read-only. Without `--change-set`, it validates the complete
project store, registry, projection classification, all records, and all
referential, relationship, EARS, artifact-digest, structured-store-integrity,
and change-set rules, including impact completeness for every proposed
change set. Approved manifests' stored historical assessments remain
preserved. Independent load failures are aggregated with semantic diagnostics
from records that could still be read.
`--change-set` narrows that impact check to the named proposed manifest.
"Matches" means that every current mechanical candidate has exactly one final
recorded disposition, every recorded `mechanical` entry is still a current
mechanical candidate, every `semantic` entry names an unchanged active
requirement that is not in the change-set operations, and
`impact_assessment_base_commit` equals `base_commit`
([Impact review protocol](#impact-review-protocol)). Semantic entries are
permitted extras; unreviewed or duplicate entries are not.

Success data contains:

```json
{
  "valid": true,
  "checked_paths": [],
  "record_counts": {
    "requirements": 0,
    "interfaces": 0,
    "change_sets": 0,
    "artifacts": 0
  }
}
```

An invalid specification returns the failure envelope with one or more stable
diagnostics and status `4`; project discovery, schema-version, and
project-configuration failures use status `3`. An incomplete, stale, or
mismatched proposed impact assessment returns status `5` so the caller
refreshes and re-reviews state rather than revising record content. Approved
manifests are checked against their stored historical assessment. `check`
never repairs files.

The class of a `project.` or `schema.` code, not its prefix, decides its
status:

- **Project, status `3`.** The project cannot be found, or cannot be used as
  configured: it is not initialized, it is outside a Git working tree, a
  schema version is unsupported, or a `project.yaml` setting is invalid,
  apart from the two cases below.
- **Validation, status `4`.** Structured-store integrity is a specification
  rule. A `project.` diagnostic on a `store_digests.<store>` field is in the
  same class as `artifact.digest_mismatch`. A credential-bearing canonical
  remote is also status `4`, as in `project init`.

`project.configuration_unreadable` is an I/O failure with status `6`. A
`project.yaml` that does not decode, such as one with malformed YAML or an
unknown key, reports `storage.decode_failed` with status `4`, as a record
that does not decode does.

When one result holds diagnostics of both classes, status `3` wins, because
an invalid configuration can make the other results wrong. A status `3`
result that carries diagnostics has the error code
`project.invalid_configuration` and the retry `select-or-upgrade-project`. A
status `4` result that carries these diagnostics has the error code
`validation.failed` and the retry `revise-request`. Every command that loads
an existing project uses the same statuses. `project init` validates its
request as the [project initialization grammar](#project-initialization-grammar)
describes.

In the table, an `error.code` is the failure's own code, and that failure
carries no diagnostics. A diagnostic arrives in `error.diagnostics`.

| Code | Reported as | `check` reports it when | Status |
| --- | --- | --- | --- |
| `project.not_git_root` | `error.code` | The working directory is not inside a Git working tree | `3` |
| `project.not_initialized` | `error.code` | The Git working tree has no `.protobot/project.yaml` | `3` |
| `project.configuration_unreadable` | `error.code` | `.protobot/project.yaml` cannot be inspected for a reason other than its absence, for example because `.protobot` is a regular file | `6` |
| `project.load_failed` | `error.code` | With `--change-set` only: the project does not load, and validation names no diagnostic | `3` |
| `project.default_branch_unresolved` | `error.code` | A change set exists, and the default branch does not resolve to a commit | `3` |
| `project.invalid_configuration` | Both | The review mode, default branch, or branch prefix is invalid, or the canonical remote is not a supported form. It is also the `error.code` of every status `3` result that carries diagnostics | `3` |
| `schema.unsupported_version` | Diagnostic | A schema version is missing or is not the supported version | `3` |
| `project.invalid_path` | Diagnostic | `.protobot/` is a symlink, or a configured store path is not a project-relative directory | `3` |
| `project.missing_field` on `project.id`, `project.name`, `repository.canonical_remote`, or `repository.review_mode` | Diagnostic | A required configuration field is missing | `3` |
| `project.remote_credentials` | Diagnostic | A supported `https://` or `ssh://` remote carries userinfo, or an SCP-style remote carries userinfo other than the fixed `git@` | `4` |
| `project.missing_field` on `store_digests.<store>` | Diagnostic | A structured store has no recorded digest | `4` |
| `project.invalid_digest` | Diagnostic | A recorded store digest does not match `sha256:<64 lowercase hexadecimal characters>` | `4` |
| `project.store_digest_unreadable` | Diagnostic | A visible entry of a structured store is a symlink, a subdirectory, or not a `.yaml` file, or the store cannot be read | `4` |
| `project.store_digest_mismatch` | Diagnostic | The visible YAML file set of a structured store does not match its recorded digest | `4` |

### `change-set compare`

`change-set compare` is deterministic and read-only. It compares the
proposed change set with its `base_commit` by default; `--against` is used for
an explicit immutable comparison revision (deferred to follow-on scope; passing
`--against` is rejected with `change_set.invalid_base`). Its result contains:

```json
{
  "change_set_id": "CS-00005",
  "against_commit": "<full-sha>",
  "changed": [],
  "exact_duplicates": [],
  "declared_conflicts": [],
  "supersession": [],
  "dependency_cycles": [],
  "implementation_required": true
}
```

The arrays contain stable IDs and before/after values where applicable. The
report distinguishes a finding from a command failure: a duplicate,
conflict, or dependency cycle is returned as analysis data so the agent and
user can decide how to revise the proposal. A malformed record or unreadable
base is a non-zero failure. The PR body renderer consumes this result without
parsing YAML.

### `impact`

```text
ears-manager impact --change-set CS-ID [--at FULL-SHA]
```

`impact` is deterministic and read-only. It compares the proposed change set
with the selected Schematic and returns unchanged active requirements that
intersect:

- an affected interface;
- an affected scope;
- an explicit requirement relationship relevant to the changed set; or
- a project-wide selector where the change affects the project boundary.

Changed and retired requirements are not returned as unchanged candidates.
Every current mechanical candidate is returned, including its recorded
disposition and rationale when an assessment already contains it. Candidates
are sorted by requirement ID.

Impact candidate completeness applies only to proposed change sets. `check`
tells proposed from approved change sets by the
[approval rule](#approved-and-proposed-change-sets) and recomputes the
candidate set for the proposed ones. Approved manifests are immutable historical
records: validation checks their stored assessment shape and references but
does not mark them stale or invalid because later requirements changed.
The result shape is:

```json
{
  "change_set_id": "CS-00005",
  "against_commit": "<full-sha>",
  "candidates": [
    {
      "requirement_id": "REQ-CLI-00001",
      "origin": "mechanical",
      "matched_by": ["interface:cli-main", "scope:help-output"],
      "recommended_disposition": "applicable",
      "recorded_disposition": null,
      "rationale": null
    }
  ],
  "assessment_status": "incomplete"
}
```

`matched_by` is explanatory output from the deterministic rule; it is not a
new schema field in the manifest. `recommended_disposition` is a conservative
review recommendation, not an approval. `assessment_status` is one of
`complete`, `incomplete`, or `stale`.

---

## Impact review protocol

Impact review is a four-step protocol. The CLI makes each boundary visible:

1. **Generate deterministic candidates.** The Toolkit invokes `impact` and
   presents every mechanical candidate, its matched selectors, and the
   conservative recommendation. This call writes nothing.
2. **Add semantic candidates.** The agent examines the proposed change
   semantically. If metadata does not expose an unchanged obligation, it
   presents that requirement to the user as a semantic candidate with a
   rationale. The agent does not silently add it.
3. **Record disposition.** After the user decides, the Toolkit supplies a
   complete `--impact-file` to `change-set update`. Each candidate has
   `applicable` or `not-applicable`, a rationale, and `mechanical` or
   `semantic` origin. This is a draft mutation, not approval.
4. **Approve the exact reviewed set.** The Toolkit reruns `impact`,
   `change-set compare`, and `check`, then presents the complete result. Any
   changed or new candidate invalidates the presentation. The user explicitly
   approves that exact revision; Git merge is the approval event.

An applicable candidate becomes part of the delivery-obligation set. A
`not-applicable` candidate remains in the manifest with its rationale so the
same conservative result is not repeatedly reconsidered without context.
Retired requirements are retained in the change-set delta but are never
delivery obligations.

If `impact` finds no candidates, `assessment_status` is `complete` only when
the change set has no unreviewed recorded entries and its assessment was
recorded against the current base commit. A prior complete assessment
becomes `stale` whenever any input that can alter the mechanical candidate set
changes: the base commit, affected interfaces, affected scopes, changed
requirement/interface/artifact operations, or relevant requirement
relationships and applicability records. `assessment_status` is `incomplete`
when current candidates lack final dispositions, `stale` when the recorded
assessment was computed from different inputs, and `complete` only when the
matching rule above passes. Approval is blocked for either incomplete or
stale status.

`impact` recomputes the candidate set from every input except the base
commit, which it does not read. For the base commit, the manifest records
`impact_assessment_base_commit`
([ADR-0002](../decisions/0002-ears-specification-record-schema.md#change-set-manifests)):

- `change-set create` sets it to `base_commit`;
- a reviewed `change-set update --impact-file` sets it to the `base_commit`
  that the same update leaves in the manifest; and
- `change-set update --base-commit` leaves it unchanged.

A proposed change set whose `impact_assessment_base_commit` is missing or
differs from `base_commit`, ignoring letter case, has a `stale` assessment,
even when it has no candidates and no entries. So after a base update,
`impact` and
`change-set update` report `stale`, and `check` returns
`change_set.assessment_incomplete`, status `5`, with a
`change_set.stale_impact` diagnostic on `impact_assessment_base_commit`,
until a reviewed `change-set update --impact-file` records the assessment
against the new base.

---

## Atomicity and failure behavior

Every mutating command follows this sequence:

1. Resolve the project and read schema-version metadata before strict field
   decoding, so a newer schema produces `schema.unsupported_version` rather
   than an unknown-field decode failure.
2. Load the complete affected store using safe parsing, retaining all valid
   records and aggregating independent load failures.
3. Validate the request and all affected records, references, relationships,
   registered paths, projection classifications, and structured-store
   integrity digests.
4. For an existing-change-set write, verify that the change set is
   [proposed](#approved-and-proposed-change-sets) and that `HEAD` passes the
   [ancestry check](#ancestry-check). `project init` instead verifies that the
   control namespace is absent. `change-set create` instead checks the branch
   state, then reuses the initialization branch or cuts and checks out the new
   branch, and loads the project again from there
   ([`change-set create`](#change-set-create)).
5. Except for `project init`, verify that `HEAD` has not moved since the
   command last read it: in step 2, or, for `change-set create`, after the cut.
   Then write a complete replacement set through a temporary file or
   directory.
6. Re-read and validate the replacement set.
7. Atomically replace the governed paths and return the result.

No failed validation writes a partial record, registry entry, digest, or
manifest operation. A failure after an external filesystem or Git operation
cannot be proven rolled back returns status `6` and `mutation: "unknown"`;
the caller must reconcile with the applicable resource-specific `show`
command and `check` before retrying.

The caller must preserve the current checkpoint on every non-zero result. It
must not turn a failed write into conversational success or edit a governed
path as a workaround. Safe retries are:

| Result | Caller action |
| --- | --- |
| `2` or `4` with `mutation: none` | Revise the request and retry with a new request |
| `5` with `mutation: none` | Refresh the project/change-set state, rerun comparison and impact, and obtain renewed user review |
| `6` with `mutation: none` | Fix the environment, then retry after checking state |
| `6` with `mutation: unknown` | Reconcile first; retry the same request only when the result proves it was not applied |
| `70` | Preserve the input and diagnostics for implementation triage; do not repeat blindly |

---

## Golden fixture

[`fixtures/ears-manager-cli-golden.jsonl`](fixtures/ears-manager-cli-golden.jsonl)
is the harness-neutral follow-on fixture for the complete target contract. Its
`fixture-scope` record identifies the subset implemented by EM-04, EM-05,
EM-06, and EM-07 and the commands deferred to later increments. Implementation
tests exercise the implemented subset directly; the deferred fixture steps
remain acceptance data for their owning follow-on issues. The fixture covers:

- project initialization;
- change-set creation;
- Vision/Architecture artifact writes;
- interface and requirement writes;
- validation success and validation failure;
- deterministic comparison;
- mechanical impact candidates;
- a semantic candidate and both dispositions; and
- an attempted write that proves the governed failure path leaves the store
  unchanged.

The fixture supplies all creation timestamps and uses a fixed base commit so
that JSON output is replayable. `<computed>` values identify fields whose
contents are derived from the fixture's canonical files; the implementation
test compares them after applying the same canonicalization algorithm. The
final `same_as` assertion compares the post-failure `change-set compare`
response byte-for-byte with the earlier comparison step, while the preceding
read proves that the rejected requirement was not created.

---

## Acceptance evidence

An implementation satisfies this contract when its offline fixture suite
demonstrates:

- every command in the command surface has a request, success, diagnostic,
  and exit-status assertion;
- human diagnostics go to stderr and JSON results contain no progress output;
- every failed mutation leaves the governed store unchanged;
- direct edits, additions, deletions, renames, symlinked entries, or
  unregistered paths are rejected by `check` and by the Drafting Table's
  pre-stage digest check;
- independent load failures are aggregated with diagnostics from valid
  records that remain readable, in path/code order;
- validator names select code-controlled adapters and never caller-supplied
  commands;
- `change-set compare` and `impact` are deterministic for the same base and
  working tree;
- `change-set show --at` returns the manifest and status of the selected
  commit, and a change in the working tree does not change that result;
- semantic impact additions are visible, carry rationale, and cannot bypass
  user disposition;
- an incomplete or stale proposed impact assessment prevents approval; and
- an approved change set is immutable while an unapproved change set remains
  revisable.

The fixture is local-only. It requires no WMS, Git host, network service,
OAuth token, or hosted agent runtime.

---

## Decision for Q18

The CLI interface is specified by this command grammar, typed request fields,
JSON result envelope, human diagnostic form, and exit-status table. No
third-party CLI IDL is a runtime dependency for the first implementation.
`usage`, docopt, and `wasi:cli` remain possible implementation or
documentation aids, but selecting one is not a prerequisite for the stable
caller contract.

This closes the implementation-blocking part of
[Q18](open-questions.md#q18-cli-interface-spec-evaluation) without adding a
new design track.

---

## Related Documents

- [Vision](../vision.md) — Purpose, users, outcomes, and prototype boundary
- [Architecture](../architecture.md) — External interfaces, persistent
  state, and environmental constraints
- [Overview](overview.md) — Specification hierarchy, modes, and workflow
- [System Components](components.md) — Component responsibilities and
  `ears-manager` behavior
- [Drafting Table WMS Integration](drafting-table-wms.md) — Backend-neutral
  WMS operations and blocked-work resolution
- [User Interaction Flow](user-interaction-flow.md) — Sketching,
  Dimensioning, and impact review
- [Drafting Table UX](drafting-table-ux.md) — User-visible checkpoints and
  approval behavior
- [Git and Project-Repository Integration](git-integration.md) — Branches,
  commits, pull requests, and approved state
- [Source Control Manager](source-control-manager.md) — The caller that
  stages, commits, and publishes what `ears-manager` writes
- [Evaluation Corpus Schema and Result Contract](evaluation-corpus.md)
  — Versioned fixtures, holdout isolation, and stage-attributed results
- [Open Design Questions](open-questions.md) — Remaining unresolved design
  questions
- [ADR-0001](../decisions/0001-requirements-storage-format.md) — Physical
  storage format
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md) —
  Logical record schema

[git-init]: git-integration.md#project-initialization
