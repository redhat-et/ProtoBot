# ADR-0001: One-File-Per-Record YAML Storage Format

> Status: **Accepted** — September 2026

**Contents:**

- [Context](#context)
- [Decision](#decision)
- [Rationale](#rationale)
- [Alternatives Considered](#alternatives-considered)
- [Cross-Reference Representation](#cross-reference-representation)
- [Change-Set History Representation](#change-set-history-representation)
- [Consequences](#consequences)
- [Related Documents](#related-documents)

## Context

`ears-manager` is ProtoBot's exclusive read/write gate for
specification content. It abstracts the underlying file format
so that agents, CI, and humans interact through CLI subcommands,
never by editing files directly
([components.md](../architecture/components.md#ears-manager)).

The architecture docs tentatively proposed JSONL for
git-friendliness but flagged it as unresolved
([Q7][q7],
[Phase 2 follow-up note][phase2]).
The follow-up note specifically warned that JSONL works only
if requirements are independent, self-contained records, and
questioned whether cross-references and change-set history make
a different format necessary.

This ADR resolves that question for the initial implementation.
Because `ears-manager` abstracts the format, the choice can be
revised later without breaking callers.

### Evaluation criteria (from issue)

1. Git diff/merge compatibility
2. Cross-reference representation
3. Change-set/immutable history support
4. PR reviewability
5. Normalization and formatting stability

---

## Decision

Use **one file per record in YAML format** for all specification
records managed by `ears-manager`: requirements, interfaces,
and change-set manifests. Artifact registration metadata
(kind, path, digest, owner, validator) remains in
`.protobot/project.yaml`
([components.md](../architecture/components.md#content-storage-model))
and is not covered by this format decision.

Each record is a single YAML file whose filename is derived from
its stable ID. `ears-manager` owns the mapping from ID to file
path and enforces canonical serialization on every write.

The exact directory hierarchy (how files are organized within the
spec store) and the logical field-level schema (which fields each
record type contains) are out of scope for this ADR and are
addressed by follow-up work (repository layout and
[ADR-0002](0002-ears-specification-record-schema.md)
respectively).

---

## Rationale

### 1. Git diff/merge compatibility

One-file-per-record **eliminates merge conflicts for
independent requirement edits**. Two contributors working on
different requirements modify different files; `git merge`
applies both changes without conflict — no format tuning or
merge drivers required.

Conflicts still occur in shared files: two contributors who
add requirements to the same change set both edit that
change-set manifest, and any index file `ears-manager`
maintains is a similar hot spot. `ears-manager` keeps these
conflicts mechanical by using sorted lists and deterministic
key ordering, so the merge resolution is straightforward even
when it is not automatic.

By contrast, any single-file format (JSONL, a monolithic YAML
document, or a JSON array) places all records in one file,
creating merge conflicts whenever two branches touch the same
file, even if they edit unrelated records on different lines.
JSONL limits this to adjacent-line conflicts, but does not
eliminate them.

### 2. Cross-references

Requirements reference other requirements by stable ID using
explicit relationship fields (`depends-on`, `conflicts-with`,
`supersedes`, `related-to`). Each reference is a string value
naming the target record's ID.

One-file-per-record makes cross-references simple string
pointers — no line numbers, byte offsets, or format-specific
anchors. `ears-manager check` resolves these IDs against the
full set of files and validates referential integrity, including
acyclicity for `depends-on`. Adding, removing, or changing a
relationship modifies only the referencing file(s); the
referenced file is untouched.

This is materially better than JSONL for cross-references: in
JSONL, reorganizing or reordering lines can silently break
line-based tooling assumptions, and a relationship change still
touches the same monolithic file.

### 3. Change-set and immutable history

Change-set manifests are already stored as individual files in
`.protobot/change-sets/`
([content storage model](../architecture/components.md#content-storage-model)).
Using one-file-per-record for requirements and interfaces
makes the entire specification store consistent: every record
is a file, every file is individually versioned by Git.

Immutability of approved change sets is enforced by
`ears-manager`, not by the file format. An approved manifest
file on `main` is immutable by convention and CI gate, the same
way any committed file is. The storage format does not need to
encode immutability itself.

### 4. PR reviewability plan

One-file-per-record produces **the smallest possible diffs** for
requirement changes:

- **Adding** a requirement creates one new file. The PR diff
  shows the full content of the new requirement and nothing
  else.
- **Revising** a requirement modifies one file. The PR diff
  shows a before/after comparison of that requirement only.
- **Retiring** a requirement modifies one file (status field
  change). The file is never deleted; retired records remain
  as permanent targets of historical cross-references and
  approved change-set manifests.
- **Cross-reference changes** modify only the files that add
  or remove relationships.

A reviewer sees exactly which requirements changed, with no
noise from unrelated records.

**Supplementary rendered summary.** For change sets that touch
many requirements, `ears-manager compare` and
`ears-manager impact` produce structured output suitable for
posting as a PR comment. This rendered summary shows the
before/after for each changed requirement and the applicable
requirement set in a human-readable table, supplementing the
raw file diffs. Whether this summary is posted automatically
(via CI) or on demand (via a Drafting Table action) is a
UX decision outside this ADR's scope.

**[Amended September 2026]** That UX decision is now made: the
Drafting Table renders the summary into the pull-request body when
the pull request is created or updated, and CI is not required to
post it ([Git and Project-Repository
Integration](../architecture/git-integration.md#title-and-body)).

**Normalization rules.** `ears-manager` enforces canonical
serialization on every write to keep diffs stable across
editors and tooling:

- Stable key ordering (fixed order defined by the schema,
  not alphabetical)
- Consistent YAML scalar style (block scalars for multi-line
  EARS text; plain scalars for short values that are
  unambiguously strings; quoted scalars for values that
  could be parsed as non-string types — timestamps,
  bare `yes`/`no`/`on`/`off`, numeric-looking IDs, and
  values with leading zeros)
- One sentence per line in block scalars (no column-based
  rewrapping), so a single changed word does not re-flow
  subsequent lines
- Two-space indentation with sequences indented consistently,
  matching the repository's yamllint configuration
- LF line endings only (no CRLF), regardless of contributor
  platform
- No trailing whitespace; single newline at end of file
- UTF-8 encoding without BOM
- Relationship lists sorted by target ID
- No anchors, aliases, merge keys (`<<`), custom tags, or
  duplicate keys; `ears-manager` rejects them and parses
  canonical files with a safe loader only

These rules ensure that two writes of the same logical content
produce byte-identical files, so diffs reflect only real
changes.

### 5. Why YAML over JSON

Both YAML and JSON are viable serialization formats for
individual record files. YAML is preferred for this use case
because:

- **Multi-line EARS text.** EARS requirement statements can be
  long. YAML block scalars (`>-`, `|`) render naturally in
  diffs and editors. JSON requires escaped newlines or a single
  long line, both of which are harder to review.
- **Human readability.** Requirements are reviewed by humans
  (architects, stakeholders) who may not use `ears-manager`
  for every read. YAML is more accessible without tooling.
- **Comments.** YAML supports inline comments, which improves
  readability over JSON. `ears-manager` writes a single
  generated header comment (e.g.,
  `# managed by ears-manager — do not edit by hand`) and
  preserves it across rewrites. Comments carry no semantics;
  `ears-manager check` and the
  [ADR-0002](0002-ears-specification-record-schema.md) schema
  ignore them.
- **Ecosystem.** Go is the implementation language for
  `ears-manager`. The YAML library must support deterministic
  emit: explicit control over key order, indentation, scalar
  style, and line-break placement. Round-trip preservation of
  input formatting is not required, since `ears-manager`
  always writes canonical output.

JSON remains the format for `ears-manager` structured CLI
output (commands that produce machine-readable results), keeping
a clean separation: YAML on disk, JSON on stdout.

---

## Alternatives Considered

### JSONL (one requirement per line)

JSONL places one JSON object per line in a single file (or one
file per interface).

**Advantages:**

- Append-only writes are simple (add a line).
- Each line is independently parseable.
- Tentatively proposed in the architecture docs for
  git-friendliness.

**Why rejected:**

- **Merge conflicts.** Two branches adding requirements to the
  same file produce merge conflicts even when the requirements
  are unrelated. This is the most common concurrent-edit
  scenario in a multi-contributor workflow.
- **PR reviewability.** A diff of a JSONL file shows added or
  changed lines, but each line is a full JSON object (often
  100+ characters). Reviewers must parse dense single-line
  JSON to understand changes. There is no structural
  before/after comparison.
- **Cross-references.** Relationship fields referencing other
  records by ID work the same as in one-file-per-record, but
  referential integrity validation requires parsing the
  entire file rather than individual files. File
  reorganization (reordering lines, splitting into multiple
  files) is more disruptive.
- **Canonical formatting.** Keeping JSON objects on single
  lines conflicts with readability; pretty-printing defeats
  the one-line-per-record property. There is no good
  middle ground.

JSONL is still well-suited for append-only logs and event
streams (e.g., the Finding Ledger JSONL snapshot). It is not
the best fit for records that are frequently revised, cross-
referenced, and reviewed in pull requests.

### Single structured document (JSON/YAML array or object)

A single file containing all requirements as an array or keyed
object.

**Advantages:**

- All data in one place; simple to load.
- Relationships can use internal anchors (YAML) or path
  references (JSON).

**Why rejected:**

- **Merge conflicts.** Every edit touches the same file. Two
  concurrent edits always conflict, even on unrelated
  requirements. This is the worst-case scenario for a
  multi-contributor workflow.
- **PR reviewability.** Diffs show structural changes mixed
  with unrelated context. A single added requirement may
  appear in a diff alongside hundreds of unchanged lines.
- **Scalability.** As the specification grows, the file
  becomes large and unwieldy for both humans and tooling.

### SQLite or lightweight relational store

A SQLite database file committed to Git.

**Advantages:**

- Rich queries without custom tooling.
- Referential integrity via foreign keys.
- Atomic transactions.

**Why rejected:**

- **Binary format.** Git cannot diff or merge SQLite files
  meaningfully. Every change produces a binary diff that
  reviewers cannot read.
- **PR reviewability.** Impossible without an external
  rendering step. The raw diff is opaque.
- **Tooling dependency.** Requires SQLite CLI or library to
  read; violates the principle that spec files should be
  human-readable with standard tools.
- **Conflict resolution.** No meaningful merge strategy
  exists for binary database files.

---

## Cross-Reference Representation

Cross-references between requirements use the decided minimum
relationship vocabulary from the architecture docs:

- `depends-on` — must be acyclic; `ears-manager` validates
  this on every write
- `conflicts-with` — stored in both files;
  `ears-manager check` validates symmetry
  ([ADR-0002](0002-ears-specification-record-schema.md)
  decided bidirectional storage)
- `supersedes` — directional; the superseding requirement
  references the superseded one
- `related-to` — stored in both files;
  `ears-manager check` validates symmetry.
  Informational; no validation constraints beyond target
  existence and symmetry
  ([ADR-0002](0002-ears-specification-record-schema.md)
  decided bidirectional storage)

Each relationship is stored as a list entry in the source
requirement's file, containing the relationship type and the
target requirement's stable ID. The exact field structure is
defined by [ADR-0002](0002-ears-specification-record-schema.md);
the storage format treats it as
a list of typed string references.

`ears-manager check` validates all cross-references:

- Every target ID resolves to an existing record
- `depends-on` relationships form a DAG (no cycles)
- `supersedes` relationships form a DAG (no cycles)
- `conflicts-with` and `related-to` symmetry is validated
  ([ADR-0002](0002-ears-specification-record-schema.md))

---

## Change-Set History Representation

Change-set manifests are individual YAML files in
`.protobot/change-sets/`. Each manifest records:

- Base specification commit (immutable reference point)
- Intent and rationale
- Requirement operations (`add`, `revise`, `retire`) with
  target IDs
- Affected interfaces and scopes
- Impact assessment with dispositions

Approved change sets are immutable. The file on `main` after
PR merge is the permanent audit record. `ears-manager` refuses
to modify an approved manifest.

The one-file-per-record format is consistent with this existing
design. Change-set manifests, requirements, and interfaces all
follow the same pattern: one YAML file per record, individually
versioned by Git, canonically serialized by `ears-manager`.

---

## Consequences

### Benefits

- Merge conflicts between independent requirement record edits
  are eliminated (shared files like change-set manifests may
  still conflict but are kept mechanical by sorted lists).
- PR diffs are minimal and focused on the changed records.
- Human reviewers can read spec files without tooling.
- `ears-manager` can enforce canonical formatting on individual
  files without affecting unrelated records.
- The format is consistent across all spec record types.

### Costs

- **File count.** A large specification produces many small
  files. This is manageable with standard Git tooling and is
  the accepted trade-off for conflict-free merges.
- **Cross-reference resolution.** `ears-manager` must read
  multiple files to validate referential integrity. This is
  a one-time cost at validation time, not a per-read cost for
  callers (who use `ears-manager` subcommands, not direct file
  access).
- **Canonical serialization.** `ears-manager` must enforce
  formatting rules on every write. This is additional
  implementation work but is required regardless of format to
  keep diffs stable.

### Compatibility with `ears-manager` abstraction

This decision does not change the `ears-manager` interface.
Callers continue to use subcommands (`add`, `list`, `show`,
`check`, etc.) and never read or write spec files directly.
The format can be changed in a future ADR without breaking
any caller.

### Relationship to ADR-0002 (schema)

This ADR decides the physical format (one-file-per-record
YAML). [ADR-0002](0002-ears-specification-record-schema.md)
defines the logical schema (field names, types, enums,
validation rules). The two are independent: the schema defines
what goes in each file; this ADR defines how those files are
serialized and organized on disk.

---

## Related Documents

- [Q7: Requirements storage format][q7] — the open
  question this ADR resolves
- [Phase 2 follow-up note][phase2] — the JSONL concern
  this ADR addresses
- [ears-manager][em] — the component that abstracts this
  format
- [Content Storage Model][csm] — the repository layout
  this format fits into
- [ADR-0002](0002-ears-specification-record-schema.md) —
  logical schema for specification records (resolved)
- #30 — `ears-manager` CLI integration (unchanged by
  this decision)
- #34 — Git and project-repository integration (the PR
  reviewability plan here should stay consistent)

[q7]: ../architecture/open-questions.md#q7-requirements-storage-format
[phase2]: ../architecture/user-interaction-flow.md#phase-2-dimensioning
[em]: ../architecture/components.md#ears-manager
[csm]: ../architecture/components.md#content-storage-model
