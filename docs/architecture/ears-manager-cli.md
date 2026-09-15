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

## Boundary and authority

### Callers

The same binary serves these callers:

| Caller | Reads | Writes |
| --- | --- | --- |
| Drafting Table through the Specification Toolkit | Proposed and approved specification state, comparison, impact candidates, validation results | Proposed specification records and change-set manifests on the active change-set branch |
| CI | Registered records and artifacts at the checked-out revision | None |
| Job Site Materializer | The approved manifest and requirements at an immutable specification commit | None; it passes lifecycle state to the WMS Adapter |
| Human maintainer | All data exposed by the read commands | The same proposed changes as the Toolkit, subject to the same validation |

The CLI is a local process over the caller's working tree. A hosted Drafting
Table may invoke it inside a controlled project workspace, but hosting does
not change the command or JSON contract.

### Read authority

`ears-manager` is the read authority for registered specification artifacts
and records. It resolves the project from the Git working tree containing
`.protobot/project.yaml`; a caller-supplied project name, branch, remote, or
path does not override that identity. The initialization command is the one
exception: it resolves the Git working-tree root before
`.protobot/project.yaml` exists.

Reads are root-relative and deterministic:

- `artifact get` reads registered content through the registry;
- `requirement`, `interface`, and `change-set` reads resolve IDs through the
  store; and
- `check`, `change-set compare`, and `impact` read the complete relevant
  store rather than trusting a caller-provided subset.

The optional `--at <full-commit-sha>` selector is read-only and is accepted
only by read and analysis commands. It must name a full 40-character commit
present in the local repository. The default is the current working tree.

### Write authority

Only `ears-manager` writes these paths:

- `.protobot/project.yaml`;
- `.protobot/projection.yaml` classification entries for registered
  specification paths;
- registered Vision, Architecture, interface-IDL, and interface-prose
  artifacts;
- requirement and interface records; and
- proposed change-set manifests.

The CLI never writes `.protobot/test-catalog.jsonl`,
`.protobot/attestations/`, WMS state, credentials, or Worker projections. It
does not commit, push, open, or merge a pull request. The Git integration
contract owns those operations. `change-set create` is the governed seam at
which the Drafting Table requests a change-set branch; branch naming and
branch lifecycle still follow [Git and Project-Repository
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
immutable; attempting to update it is a conflict, not a new draft.

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
  `--change-set CS-<NNN>`. The command never silently selects an unrelated
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
namespace, seeds schema versions and the default artifact registry, and
classifies registered specification paths as `shared` in
`.protobot/projection.yaml`. It does not create content, commit, push, or
merge anything. The caller follows the project-initialization sequence in
[Git and Project-Repository Integration][git-init].

`--canonical-remote` must use a credential-free `https://` or `ssh://` URL,
or the standard `git@host:path` SSH form. `project init` and `check` reject
URL passwords, tokens, and other embedded secret userinfo with
`project.remote_credentials`, status `4`, and no echoed remote value.

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
comparison baseline.

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
    "message": "The requirement is not valid.",
    "exit_code": 4,
    "diagnostics": [],
    "mutation": "none",
    "retry": "revise-request"
  }
}
```

`data` is operation-specific. `diagnostics` on a successful response contains
warnings that did not prevent the operation. A failure's `error.diagnostics`
contains structured errors in stable path/code order. The process exit status
and `error.exit_code` are the same value.

### Diagnostic entries

Each structured diagnostic has this shape:

```json
{
  "code": "requirement.ears_pattern_mismatch",
  "severity": "error",
  "path": ".protobot/requirements/REQ-CLI-001.yaml",
  "record_id": "REQ-CLI-001",
  "field": "text",
  "message": "Text does not match the declared event-driven pattern.",
  "hint": "Start the statement with 'When ...'."
}
```

`path`, `record_id`, and `field` are optional. Codes are stable identifiers,
not localized prose. Implementations may add diagnostic codes, but they must
not change the meaning of an existing code within schema version `1`.

## Exit statuses and diagnostics

The process status is part of the contract:

| Status | Class | Examples | Safe retry |
| ---: | --- | --- | --- |
| `0` | Success | Read succeeded, validation passed, or a write was applied | No retry needed |
| `2` | Usage | Unknown command/option, missing option, malformed option value | Correct the request; no mutation occurred |
| `3` | Project | Project not initialized, project outside Git root, unsupported store version, invalid project configuration | Select or upgrade the project; no mutation occurred |
| `4` | Validation | Invalid EARS text, missing required metadata, dangling reference, invalid relationship, invalid artifact content | Revise the proposed request; no mutation occurred |
| `5` | Conflict or stale state | Approved manifest, duplicate ID, branch conflict, changed base, impact assessment no longer matches candidates | Refresh and review; no mutation occurred |
| `6` | I/O or external boundary | Permission failure, unreadable input, Git read failure, atomic write failure with known rollback | Fix the environment, then retry after checking state |
| `70` | Internal failure | Unexpected invariant or serialization failure | Do not blindly retry; retain the diagnostic for implementation triage |

An operation that cannot establish whether a write happened returns status `6`
with `mutation: "unknown"` and a reconciliation instruction. It never claims
that the requested state was applied.

Warnings do not change a successful status. A command that reports candidates,
duplicates, or conflicts as analysis data still exits `0`; malformed input or
an invalid store exits non-zero.

## Operation contracts

The tables below define the minimum request and result for every Toolkit-used
operation. Fields inherited from ADR-0002 are not repeated in full.

### `project init`

| Request | Success result | Diagnostic result |
| --- | --- | --- |
| Project ID, name, canonical remote, review mode, and optional path/branch defaults | Project identity, repository settings, schema versions, registered artifact IDs/paths, and changed paths | `project.already_initialized`, `project.not_git_root`, `project.invalid_path`, `project.remote_credentials`, or `project.invalid_configuration` |

The operation is the only write that does not require an existing project
configuration or `--change-set`. It is atomic across `project.yaml` and
`projection.yaml`. The initial change set and Git branch follow the #34
sequence; initialization itself does not approve or commit the project.

### Artifacts

| Command | Request | Success result | Diagnostic result |
| --- | --- | --- | --- |
| `artifact put` | Change set, artifact ID/kind/path/owner, optional validator, and UTF-8 content | The complete registry entry, content digest, changed path, and change-set artifact operation | `artifact.unknown_kind`, `artifact.invalid_path`, `artifact.validator_not_allowed`, `artifact.write_not_allowed`, or a validator diagnostic |
| `artifact get` | Exactly one of artifact ID or kind, where kind must match one entry; optional `--at` | Registry entry and UTF-8 content | `artifact.not_found`, `artifact.ambiguous`, or `artifact.read_failed` |
| `artifact list` | Optional kind/owner filter and `--at` | Registry entries sorted by artifact ID; content is not included | `project.invalid_configuration` or `artifact.read_failed` |

`artifact put` is the only route for Vision, Architecture, interface prose,
and external interface-IDL content. It updates the registry digest, records
the artifact operation in the change-set manifest, and, for a new registered
path, adds the `shared` projection classification in the same transaction. It
never executes a validator name supplied by the caller; validators are
selected from the built-in allowlist.

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
| `requirement update` | Change set, stable ID, and replacement fields | Before/after record summary and the `revise` operation | `requirement.not_found`, `requirement.immutable_field`, or validation diagnostics |
| `requirement retire` | Change set and stable ID | Before/after status and the `retire` operation | `requirement.not_found`, `requirement.already_retired`, or impact/reference diagnostics |

All requirement mutations validate the complete affected relationship graph
before writing. Retirement preserves the record and its historical ID.
Retirement does not imply that implementation code must be removed; the
change set's `implementation_required` and rationale make that decision
explicit.

### Change sets

| Command | Request | Success result | Diagnostic result |
| --- | --- | --- | --- |
| `change-set create` | Intent, affected interfaces/scopes, implementation decision, and `--created` | Allocated `CS-<NNN>` ID, full base commit, branch name, manifest path, and empty proposed manifest | `change_set.branch_exists`, `change_set.no_base`, `change_set.invalid_scope`, or project diagnostics |
| `change-set list` | Optional status, interface, scope, and `--at` filters | Proposed/approved manifests sorted by ID | `change_set.read_failed` |
| `change-set show` | `--change-set CS-ID` and optional `--at` | Complete manifest, derived status, changed/applicable counts, and exact paths | `change_set.not_found` |
| `change-set update` | `--change-set CS-ID` plus metadata, base refresh, or complete impact assessment | `before`, `after`, `assessment_status`, and `changed_paths` in the result | `change_set.not_proposed`, `change_set.base_mismatch`, `change_set.invalid_impact`, or validation diagnostics |
| `change-set compare` | `--change-set CS-ID` and optional `--against` full commit | Deterministic comparison report described below | `change_set.not_found`, `change_set.invalid_base`, or read/validation diagnostics |

`change-set create` allocates the next unused sequence number and records a
full 40-character `base_commit`. Normal creation cuts the branch named by
`repository.branch_prefix` and the slug rules in #34. Project initialization
is the documented exception: when the working tree is already on the
pre-cut `cs/<nnn>-project-init` branch, the project is not yet approved, and
that branch has no manifest, `change-set create` records the existing branch
and does not return `change_set.branch_exists`. This is the only branch
reuse case and corresponds to [Git and Project-Repository
Integration](git-integration.md#project-initialization). A failed creation
leaves neither a manifest nor a new branch.

Every successful `change-set update` returns a `before` and `after` manifest
summary, the resulting `assessment_status`, and sorted `changed_paths`.
`change-set update --impact-file` replaces the complete impact assessment in
one operation. The file must contain a final `applicable` or
`not-applicable` disposition, a rationale, and an origin of `mechanical` or
`semantic` for every entry. A semantic entry must name an unchanged active
requirement not already in the changed operations.

### `check`

```text
ears-manager check [--at FULL-SHA] [--change-set CS-ID]
```

`check` is read-only. Without `--change-set`, it validates the complete
project store, registry, projection classification, all records, and all
referential, relationship, EARS, digest, and change-set rules. With a change
set it additionally verifies that the proposed manifest is complete and its
impact assessment matches the current deterministic candidate set. "Matches"
means that every current mechanical candidate has exactly one final recorded
disposition, every recorded `mechanical` entry is still a current mechanical
candidate, and every `semantic` entry names an unchanged active requirement
that is not in the change-set operations. Semantic entries are permitted
extras; unreviewed or duplicate entries are not.

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
diagnostics and status `4`; project discovery or schema-version failures use
status `3`. When `--change-set` finds an incomplete, stale, or mismatched
impact assessment, `check` returns status `5` so the caller refreshes and
re-reviews state rather than revising record content. `check` never repairs
files.

### `change-set compare`

`change-set compare` is deterministic and read-only. It compares the
proposed change set with its `base_commit` by default; `--against` is used for
an explicit immutable comparison revision. Its result contains:

```json
{
  "change_set_id": "CS-005",
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
The result shape is:

```json
{
  "change_set_id": "CS-005",
  "against_commit": "<full-sha>",
  "candidates": [
    {
      "requirement_id": "REQ-CLI-001",
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
the change set has no unreviewed recorded entries. A prior complete assessment
becomes `stale` whenever any input that can alter the mechanical candidate set
changes: the base commit, affected interfaces, affected scopes, changed
requirement/interface/artifact operations, or relevant requirement
relationships and applicability records. `assessment_status` is `incomplete`
when current candidates lack final dispositions, `stale` when the recorded
assessment was computed from different inputs, and `complete` only when the
matching rule above passes. Approval is blocked for either incomplete or
stale status.

## Atomicity and failure behavior

Every mutating command follows this sequence:

1. Resolve the project and verify schema versions.
2. Load the complete affected store using safe parsing.
3. Validate the request and all affected records, references, relationships,
   registered paths, and projection classifications.
4. For an existing-change-set write, verify that the change set is proposed
   and its base/revision is current. `project init` instead verifies that the
   control namespace is absent; `change-set create` verifies project
   configuration, base availability, branch state, and the initialization
   branch-reuse rule.
5. Write a complete replacement set through a temporary file or directory.
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
| `6` with `mutation: unknown` | Reconcile first; retry the same request only when the result proves it was not applied |
| `70` | Preserve the input and diagnostics for implementation triage; do not repeat blindly |

## Golden fixture

[`fixtures/ears-manager-cli-golden.jsonl`](fixtures/ears-manager-cli-golden.jsonl)
is the harness-neutral fixture for the implementation issues. It covers:

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

## Acceptance evidence

An implementation satisfies this contract when its offline fixture suite
demonstrates:

- every command in the command surface has a request, success, diagnostic,
  and exit-status assertion;
- human diagnostics go to stderr and JSON results contain no progress output;
- every failed mutation leaves the governed store unchanged;
- direct edits or unregistered paths are rejected by `check` and by the
  Drafting Table's pre-stage digest check;
- `change-set compare` and `impact` are deterministic for the same base and
  working tree;
- semantic impact additions are visible, carry rationale, and cannot bypass
  user disposition;
- an incomplete or stale impact assessment prevents approval; and
- an approved change set is immutable while an unapproved change set remains
  revisable.

The fixture is local-only. It requires no WMS, Git host, network service,
OAuth token, or hosted agent runtime.

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
- [User Interaction Flow](user-interaction-flow.md) — Sketching,
  Dimensioning, and impact review
- [Drafting Table UX](drafting-table-ux.md) — User-visible checkpoints and
  approval behavior
- [Git and Project-Repository Integration](git-integration.md) — Branches,
  commits, pull requests, and approved state
- [Open Design Questions](open-questions.md) — Remaining unresolved design
  questions
- [ADR-0001](../decisions/0001-requirements-storage-format.md) — Physical
  storage format
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md) —
  Logical record schema

[git-init]: git-integration.md#project-initialization
