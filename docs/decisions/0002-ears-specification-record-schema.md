# ADR-0002: EARS Specification Record Schema

> Status: **Accepted** — September 2026

**Contents:**

- [Context](#context)
- [Decision](#decision)
  - [Requirement Records](#requirement-records)
  - [Interface Records](#interface-records)
  - [Change-Set Manifests](#change-set-manifests)
  - [Artifact-Registry Entries](#artifact-registry-entries)
- [Relationship Encoding](#relationship-encoding)
- [Design Decisions](#design-decisions)
- [Consequences](#consequences)
- [Related Documents](#related-documents)

## Context

`ears-manager` manages four kinds of specification records:
requirements, interfaces, change-set manifests, and artifact-registry
entries. The architecture documents
([components.md](../architecture/components.md#ears-manager),
[user-interaction-flow.md](../architecture/user-interaction-flow.md#phase-2-dimensioning))
describe the required fields narratively, but no formal schema
exists.

[ADR-0001](0001-requirements-storage-format.md) decided the
physical storage format (one-file-per-record YAML) independently of
the logical schema. This ADR defines the logical schema: field
names, types, optionality, enums, and validation rules for each
record type. The schema is format-agnostic — it defines what
`ears-manager` validates, not how records are serialized.

### Open questions resolved by this ADR

- **EARS template strictness**
  ([components.md](../architecture/components.md#ears-manager)):
  each requirement stores the full EARS text as a single free-form
  string tagged with a pattern `type` enum. `ears-manager` validates
  the text against the pattern's expected keyword structure using
  regex matching. This preserves human readability while enabling
  mechanical validation.
- **Q19 applicability-selector vocabulary**
  ([open-questions.md](../architecture/open-questions.md#q19-applicability-metadata-and-semantic-impact-coverage)):
  the `applies_to` selector uses `interfaces` (list of interface
  IDs) and `scopes` (list of free-form strings). The reserved
  value `project` identifies project-wide and environmental
  requirements; `ears-manager` validates its use. Other scope
  values are project-defined and not constrained by this schema
  beyond requiring non-empty strings. Projects may adopt a
  controlled vocabulary via project policy.

---

## Decision

The following schemas define the logical structure for each record
type managed by `ears-manager`. Field types use these conventions:

- **string** — UTF-8 text
- **string (enum)** — one of the listed values
- **string (ID)** — a stable identifier matching the pattern for
  its record type
- **string (ISO 8601)** — a timestamp in `YYYY-MM-DDTHH:MM:SSZ`
  format
- **list\[T\]** — an ordered list of values of type T
- **object** — a nested structure with its own fields
- **boolean** — `true` or `false`

### Schema Versioning

Each specification store carries a schema version in
`.protobot/project.yaml`, as established by the Architecture
([architecture.md](../architecture.md#persistent-state)). The
version applies per store, not per file: all records in a store
share the store's version. `ears-manager` refuses to operate on
data at a version newer than its own and forward-migrates older
versions through a reviewed change set.

This ADR defines four record types that live in two of the
Architecture's six stores: requirements, interfaces, and
change sets in the specification store, and artifact-registry
entries in `.protobot/project.yaml`. Each store carries one
version key; the initial version for both is `1`. The version
number is a monotonically increasing integer. Any change to a
store's field names, types, required constraints, or enum
values increments its version. This ADR establishes the
following increment policy: additive changes (new optional
fields, new enum values) and breaking changes both increment
the version; `ears-manager` uses the version to decide
whether migration is needed.

### Requirement Records

Requirement records are the primary specification artifact. Each
requirement contains a single EARS-formatted statement, metadata
for applicability and verification, and optional explicit
relationships to other requirements.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string (ID) | yes | Stable identifier. Format: `REQ-<SCOPE>-<NNN>` where `<SCOPE>` is a short uppercase tag derived from the interface or project area and `<NNN>` is a zero-padded sequence number. Must be unique across the specification store. |
| `type` | string (enum) | yes | EARS pattern type. See [EARS Pattern Enum](#ears-pattern-enum). |
| `text` | string | yes | The full EARS requirement statement. Free-form text validated against the keyword structure of the declared `type` via regex. See [EARS Template Validation](#ears-template-validation). |
| `applies_to` | object | yes | Applicability selector. See [Applicability Selector](#applicability-selector). |
| `verification` | object | yes | Verification metadata. See [Verification](#verification). |
| `provenance` | string (enum) | yes | Origin of the requirement. See [Provenance Enum](#provenance-enum). |
| `created` | string (ISO 8601) | yes | Timestamp when the requirement was first created. |
| `relationships` | list\[object\] | no | Explicit relationships to other requirements. See [Relationship Encoding](#relationship-encoding). Omit when no relationships exist. |
| `status` | string (enum) | no | Record lifecycle status. One of `active` (default if omitted) or `retired`. Retired records remain as permanent targets of historical cross-references. |

#### EARS Pattern Enum

The `type` field must be one of the six EARS patterns:

| Value | Keyword Structure | Description |
| --- | --- | --- |
| `ubiquitous` | "The \<system\> shall \<action\>" | Always-active invariant with no trigger or precondition. |
| `event-driven` | "When \<trigger\>, the \<system\> shall \<action\>" | Triggered by a specific event. |
| `state-driven` | "While \<state\>, the \<system\> shall \<action\>" | Active during a specific system state. |
| `unwanted-behavior` | "If \<condition\>, then the \<system\> shall \<action\>" | Response to an undesired or error condition. |
| `optional-feature` | "Where \<feature\>, the \<system\> shall \<action\>" | Behavior conditional on a feature or configuration. |
| `complex` | Combination of the above keywords | Combined pattern using multiple EARS keywords (e.g., state + event). |

#### EARS Template Validation

`ears-manager` validates the `text` field against the declared `type`
by checking for the expected keyword prefixes. Validation uses
case-insensitive regex matching:

- **`ubiquitous`**: text matches `^The .+ shall .+`
- **`event-driven`**: text matches `^When .+, the .+ shall .+`
- **`state-driven`**: text matches `^While .+, the .+ shall .+`
- **`unwanted-behavior`**: text matches `^If .+, then the .+ shall .+`
- **`optional-feature`**: text matches `^Where .+, the .+ shall .+`
- **`complex`**: text must satisfy both of the following conditions
  (case-insensitive):
  1. Contains the keyword `shall`.
  2. Contains at least two distinct EARS trigger keywords from
     this set:
     - `When`
     - `While`
     - `Where`
     - The pair `If` ... `then` (both words must appear, with
       `If` preceding `then`; the pair counts as one keyword)

  The exact validation rule for `complex` is intentionally looser
  than the single-pattern rules to accommodate the variety of
  combined patterns.

Validation is intentionally keyword-based rather than structurally
parsed. The full EARS text is stored as-is, preserving human
readability. `ears-manager` rejects text that does not match the
declared pattern's keyword structure but does not enforce word-level
grammar beyond the keywords.

#### Applicability Selector

The `applies_to` object identifies which parts of the system a
requirement applies to. At least one of `interfaces` or `scopes`
must be non-empty. A project-wide or environmental requirement
must include the reserved scope `project`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `interfaces` | list\[string (ID)\] | no | Interface IDs this requirement applies to. Each ID must resolve to a registered interface. |
| `scopes` | list\[string\] | no | Applicability scopes. The reserved value `project` identifies project-wide and environmental requirements; `ears-manager` validates its use and can find all project-wide requirements mechanically. Other values are project-defined strings (e.g., `authentication`, `data-export`) not validated against a controlled vocabulary by `ears-manager`; projects may enforce a vocabulary via project policy. |

**Constraint:** at least one of `interfaces` or `scopes` must be
present and non-empty. `ears-manager` validates that every interface
ID in `interfaces` resolves to a registered interface record.
Project-wide and environmental requirements must include the reserved
scope `project` so that `ears-manager impact` can find them
mechanically
([components.md](../architecture/components.md#ears-manager),
[user-interaction-flow.md](../architecture/user-interaction-flow.md#phase-2-dimensioning),
[open-questions.md](../architecture/open-questions.md#q19-applicability-metadata-and-semantic-impact-coverage)).

#### Verification

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `mode` | string (enum) | yes | One of `isolated-interface` (default) or `implementation-aware`. |
| `rationale` | string | conditional | Required when `mode` is `implementation-aware`. Explains why isolated-interface testing is insufficient. |

#### Provenance Enum

The `provenance` field records the origin of a requirement:

| Value | Description |
| --- | --- |
| `user-authored` | Written directly by a human user. |
| `agent-suggested` | Proposed by the Dimensioning agent and accepted (possibly with edits) by the user. |
| `kit-imported` | Imported from a Kit and accepted by the user. |

### Interface Records

Interface records register the interfaces identified in the
Architecture. Each interface has a stable ID, a type from the
interface-type taxonomy, and a reference to its specification
approach or IDL.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string (ID) | yes | Stable identifier. Format: a short lowercase-hyphenated name (e.g., `api-gateway`, `cli-main`, `config-store`). Must be unique across the interface registry. |
| `name` | string | yes | Human-readable display name for the interface. |
| `type` | string (enum) | yes | Interface type from the taxonomy. See [Interface Type Enum](#interface-type-enum). |
| `spec_approach` | string | no | The specification approach or IDL format used (e.g., `OpenAPI 3.1`, `protobuf`, `usage`, `prose`). Informational; `ears-manager` does not parse or validate the referenced IDL content. |
| `description` | string | no | Brief description of the interface's purpose and scope. |
| `created` | string (ISO 8601) | yes | Timestamp when the interface was first registered. |
| `status` | string (enum) | no | Record lifecycle status. One of `active` (default if omitted) or `retired`. |

#### Interface Type Enum

The `type` field classifies the interface using the
Architecture's interface-type taxonomy
([Specification Hierarchy][spec-hierarchy]).
Each type determines the specification approach and seeds the
Inspector roster. This list may be extended through a reviewed
Architecture change:

[spec-hierarchy]: ../architecture/user-interaction-flow.md#specification-hierarchy

| Value | Description |
| --- | --- |
| `network-service` | Network service (REST API, gRPC service). Specification approach: Smithy or OpenAPI. |
| `cli` | Command-line interface. Specification approach: command grammar and typed result contract defined by each CLI interface; `usage`, docopt, or `wasi:cli` may provide optional tooling. |
| `repl` | Read-eval-print loop. Specification approach: skills and prompts define the interaction protocol. |
| `linkable-library` | Linkable library or SDK. Specification approach: WIT (Wasm Interface Types). |
| `web-gui` | Web GUI (HTML/CSS). Specification approach: open gap — not yet established. |
| `native-gui` | Native GUI (desktop, mobile). Specification approach: open gap — not yet established. |
| `persistent-state` | Persistent state store (Git repository, config, database). Specification approach: Schema + CLI contract. |
| `package-source` | Package or Kit source. Specification approach: versioned import manifest. |

### Change-Set Manifests

Change-set manifests record a durable specification transaction.
A manifest is mutable on a feature branch and becomes immutable
once its PR merges to the main branch — the merge is the
approval event. The fields capture the base specification state,
the intent, the operations performed, and the impact assessment.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string (ID) | yes | Stable identifier. Format: `CS-<NNN>` where `<NNN>` is a zero-padded sequence number. |
| `base_commit` | string | yes | The immutable base specification commit this change set is based on. A full 40-character hexadecimal Git commit hash. Mutable refs (branch names, tags) are not accepted. Together with the WMS merge-commit reference, this forms the materialization idempotency input ([components.md](../architecture/components.md#change-set-impact-analysis)). |
| `intent` | string | yes | Human-readable description of what this change set accomplishes and why. |
| `operations` | list\[object\] | yes | Requirement operations in this change set. See [Change-Set Operations](#change-set-operations). |
| `interface_operations` | list\[object\] | no | Interface operations in this change set (registrations, updates, retirements). See [Interface Operations](#interface-operations). |
| `artifact_operations` | list\[object\] | no | Artifact operations in this change set (registrations, updates). See [Artifact Operations](#artifact-operations). |
| `affected_interfaces` | list\[string (ID)\] | yes | Interface IDs affected by this change set. Each must resolve to a registered interface. |
| `affected_scopes` | list\[string\] | no | Narrower scopes affected, using the same vocabulary as `applies_to.scopes` in requirements. |
| `implementation_required` | boolean | yes | Whether this change set requires implementation work (a build work item). |
| `implementation_rationale` | string | conditional | Required when `implementation_required` is `false`. Explains why no implementation work is needed (e.g., documentation-only change). |
| `impact_assessment` | list\[object\] | conditional | Impact assessment for unchanged requirements. See [Impact Assessment](#impact-assessment). Required before the change set can be merged (approved) and any candidate unchanged requirement exists (i.e., `ears-manager impact` identified at least one candidate). |
| `created` | string (ISO 8601) | yes | Timestamp when the change set was created. |

#### Change-Set Operations

Each operation in the `operations` list describes one requirement
change:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `action` | string (enum) | yes | One of `add`, `revise`, or `retire`. |
| `requirement_id` | string (ID) | yes | The requirement being operated on. For `add`, this is the new ID. For `revise` and `retire`, this must resolve to an existing requirement. |
| `rationale` | string | no | Why this operation is included. Particularly useful for `revise` and `retire`. |

#### Interface Operations

Each operation in the `interface_operations` list describes one
interface change within the change set. `ears-manager interface add`
and `ears-manager interface update` write these entries
([components.md](../architecture/components.md#subcommands)).
Interface retirement is recorded as a `revise` operation that
sets `status: retired` on the interface record.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `action` | string (enum) | yes | One of `add` or `revise`. |
| `interface_id` | string (ID) | yes | The interface being operated on. For `add`, this is the new ID. For `revise`, this must resolve to an existing interface. |
| `rationale` | string | no | Why this operation is included. |

#### Artifact Operations

Each operation in the `artifact_operations` list describes one
artifact-registry change within the change set.
`ears-manager artifact put` writes these entries
([components.md](../architecture/components.md#subcommands)).

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `action` | string (enum) | yes | One of `add` or `revise`. |
| `artifact_id` | string (ID) | yes | The artifact being operated on. For `add`, this is the new ID. For `revise`, this must resolve to an existing artifact-registry entry. |
| `rationale` | string | no | Why this operation is included. |

#### Impact Assessment

Each entry in `impact_assessment` records a candidate unchanged
requirement and its reviewed disposition:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `requirement_id` | string (ID) | yes | The candidate requirement ID. Must resolve to an existing requirement not in the `operations` list. |
| `disposition` | string (enum) | yes | One of `applicable` or `not-applicable`. |
| `rationale` | string | yes | Why this requirement is or is not affected by the change set. |
| `origin` | string (enum) | yes | How the candidate was identified. One of `mechanical` (found by `ears-manager impact` via scope intersection) or `semantic` (added by the Dimensioning agent or user during review). |

### Artifact-Registry Entries

Artifact-registry entries track registered specification artifacts
that `ears-manager` governs but does not parse. These include the
Vision document, Architecture document, interface IDLs, and other
opaque prose or specification files.

Artifact-registry entries are stored in `.protobot/project.yaml`
(per [components.md](../architecture/components.md#content-storage-model)),
not as individual record files. The schema below defines the
logical structure of each entry within that file.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string (ID) | yes | Stable identifier. Format: a short lowercase-hyphenated name describing the artifact (e.g., `vision`, `architecture`, `api-gateway-openapi`). |
| `kind` | string (enum) | yes | Artifact kind. See [Artifact Kind Enum](#artifact-kind-enum). |
| `path` | string | yes | Relative path from the repository root to the artifact file or registered store directory allowed by its `kind`. |
| `digest` | string | yes | Content digest for integrity verification. Format: `<algorithm>:<value>` (e.g., `sha256:...`). Updated by `ears-manager` on every write. |
| `owner` | string | yes | The component or role responsible for this artifact (e.g., `ears-manager`, `user`, `kit`). |
| `validator` | string (enum) | no | A name drawn from `ears-manager`'s built-in validator registry. `ears-manager` ships a fixed set of validator names (e.g., `markdownlint`, `openapi-lint`, `protoc`) and resolves each to a known, bundled validation routine. `ears-manager` never executes a caller-supplied command line; unrecognized names are rejected. When absent, no content validation is performed beyond path and digest tracking. |

#### Artifact Kind Enum

| Value | Description |
| --- | --- |
| `vision` | The project Vision document. |
| `architecture` | The project Architecture document. |
| `interface-idl` | An interface specification in a machine-readable IDL format (OpenAPI, protobuf, etc.). |
| `interface-prose` | An interface specification in prose form. |
| `requirement-store` | The structured requirement store directory managed by `ears-manager`. |
| `change-set` | A change-set manifest file, or the fixed change-set store directory registry aggregate. |

---

## Relationship Encoding

Requirements may declare explicit relationships to other
requirements using the `relationships` field. The minimum
relationship vocabulary is established in the architecture docs:
`depends-on`, `conflicts-with`, `supersedes`, and `related-to`.

### Relationship Structure

Each entry in the `relationships` list has the following fields:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `type` | string (enum) | yes | Relationship type. One of `depends-on`, `conflicts-with`, `supersedes`, `related-to`. |
| `target` | string (ID) | yes | The target requirement ID. Must resolve to an existing requirement. |

### Directionality and Storage

| Relationship | Directionality | Storage | Description |
| --- | --- | --- | --- |
| `depends-on` | Directional | Source file only | The declaring requirement depends on the target. A depends on B means A cannot be satisfied unless B is also satisfied. |
| `conflicts-with` | Bidirectional | Both files | Both requirements must declare the relationship. `ears-manager check` validates that if A declares `conflicts-with` B, then B also declares `conflicts-with` A. |
| `supersedes` | Directional | Source file only | The declaring requirement supersedes the target. A supersedes B means A replaces B. The superseded requirement should be `retired`. |
| `related-to` | Bidirectional | Both files | Both requirements must declare the relationship. `ears-manager check` validates symmetric storage, the same as `conflicts-with`. Informational only; no validation constraints beyond target existence and symmetry. |

### Cycle Rules

- **`depends-on`**: must be acyclic. `ears-manager check` validates
  that the `depends-on` graph forms a DAG. A cycle is rejected.
- **`conflicts-with`**: no cycle constraint (conflicts are pairwise
  declarations, not a directed graph).
- **`supersedes`**: must be acyclic. A chain of supersession (A
  supersedes B, B supersedes C) is valid but cycles are rejected.
- **`related-to`**: no cycle constraint (informational links with
  no ordering semantics).

---

## Design Decisions

### EARS text: free-form with pattern validation

**Decision:** the `text` field stores the full EARS requirement
statement as a single free-form string, tagged with a `type` enum
that names the EARS pattern. `ears-manager` validates the text
against the pattern using keyword-based regex.

**Rationale:** structured fields per pattern (trigger, condition,
subject, response, timing) would make the schema pattern-specific
and harder to read in YAML files. The free-form approach preserves
readability and allows `ears-manager` to validate formatting
without imposing a rigid grammar. Regex validation catches common
errors (wrong keywords, missing `shall`) while allowing natural
language variation.

**Trade-off:** regex validation is less strict than structured
parsing. An invalid requirement that happens to contain the right
keywords will pass validation. This is acceptable because
`ears-manager` is a formatting linter, not a semantic analyzer;
the Dimensioning agent and human reviewer catch semantic issues.

### Applicability scopes: free-form strings with a reserved value

**Decision:** the `applies_to.scopes` field uses free-form strings
rather than a controlled enum. The reserved value `project`
identifies project-wide and environmental requirements;
`ears-manager` validates its use.

**Rationale:** Q19 identified that the capability/resource scope
vocabulary is project-specific and cannot be fully enumerated in
advance. Interface selectors are validated against the interface
registry. Apart from the reserved `project` value, scope strings
are not validated against a controlled vocabulary by
`ears-manager` — projects may enforce their own vocabulary via
project policy or CI rules.

**Trade-off:** free-form scopes risk inconsistent naming across
requirements (e.g., `auth` vs. `authentication`). This is mitigated
by the Dimensioning agent, which can suggest consistent scope names,
and by project-level CI rules that enforce a vocabulary when one is
defined.

### Bidirectional relationship storage

**Decision:** `conflicts-with` and `related-to` are stored in both
files. `ears-manager check` validates symmetry.

This supersedes [ADR-0001's Cross-Reference
Representation][adr1-xref], which deferred the `conflicts-with`
storage rule to this ADR and described `related-to` without
symmetric storage. ADR-0001 has been amended to match.

[adr1-xref]: 0001-requirements-storage-format.md#cross-reference-representation

**Rationale:** bidirectional relationships must be discoverable from
either end. Storing in both files makes each requirement
self-describing: reading a single file shows all its relationships.
The alternative (storing in one file and deriving the inverse) saves
one write but makes individual files incomplete. Since `ears-manager`
owns all writes and enforces symmetry on every check, the storage
cost is minimal and the consistency guarantee is strong.

### No mutable workflow state in records

**Decision:** requirement and interface records contain no mutable
workflow state (e.g., `implemented`, `in-progress`). The `status`
field tracks only the record lifecycle (`active`/`retired`), not
delivery progress.

**Rationale:** this is an established architecture principle
([components.md](../architecture/components.md#build-work-item-lifecycle)).
Delivery progress belongs to build work items in the WMS.
Requirements are immutable at a given specification commit.
Conflating record lifecycle with delivery state would create
synchronization problems between the spec store and the WMS.

---

## Consequences

### Benefits

- `ears-manager` can validate every field mechanically: required
  fields, type constraints, enum membership, EARS keyword matching,
  referential integrity, and relationship rules.
- The schema is independent of the storage format
  ([ADR-0001](0001-requirements-storage-format.md)). It defines
  what `ears-manager` validates, not how records are serialized.
- EARS text readability is preserved. Human reviewers see natural
  language requirements in YAML files, not fragmented structured
  fields.
- Bidirectional relationships are explicitly stored and validated,
  eliminating the need for derived inverse lookups at read time.
- The schema covers all four record types referenced in the
  architecture docs, providing a complete specification for
  `ears-manager` implementation.

### Costs

- **Bidirectional storage overhead.** `conflicts-with` and
  `related-to` require writes to two files for a single logical
  relationship. This is acceptable because `ears-manager` owns all
  writes and can perform both atomically.
- **Regex validation limitations.** Keyword-based EARS validation
  is less strict than structured parsing. Some malformed
  requirements may pass validation. This is mitigated by human
  review during Dimensioning.
- **Free-form scopes.** Without a controlled vocabulary enforced by
  `ears-manager`, scope strings may drift. Projects must manage
  consistency through policy or CI rules.
- **Schema evolution.** Adding fields or enum values increments
  the per-store schema version in `.protobot/project.yaml` and
  requires updating `ears-manager` and potentially migrating
  existing records. This is manageable because `ears-manager`
  abstracts all reads and writes and refuses to operate on data
  at a version newer than its own.

### Example Requirement Record

```yaml
id: REQ-AUTH-001
type: event-driven
text: >-
  When a user submits valid credentials, the system shall
  return a JWT token within 500ms.
applies_to:
  interfaces:
    - api-gateway
  scopes:
    - authentication
verification:
  mode: isolated-interface
provenance: user-authored
created: "2026-08-01T14:30:00Z"
relationships:
  - type: depends-on
    target: REQ-AUTH-002
  - type: related-to
    target: REQ-SESSION-001
status: active
```

### Example Interface Record

```yaml
id: api-gateway
name: API Gateway
type: network-service
spec_approach: OpenAPI 3.1
description: >-
  The primary HTTP API surface for external clients.
created: "2026-07-15T10:00:00Z"
status: active
```

### Example Change-Set Manifest

```yaml
id: CS-001
base_commit: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
intent: >-
  Add authentication requirements for the API gateway
  interface, including token issuance, expiration, and
  error handling.
operations:
  - action: add
    requirement_id: REQ-AUTH-001
  - action: add
    requirement_id: REQ-AUTH-002
  - action: add
    requirement_id: REQ-AUTH-003
interface_operations: []
artifact_operations: []
affected_interfaces:
  - api-gateway
affected_scopes:
  - authentication
implementation_required: true
impact_assessment:
  - requirement_id: REQ-GW-010
    disposition: applicable
    rationale: >-
      Existing gateway routing requirement intersects with
      the authentication scope.
    origin: mechanical
  - requirement_id: REQ-LOG-005
    disposition: not-applicable
    rationale: >-
      Logging requirement shares the api-gateway interface
      but authentication changes do not affect logging
      behavior.
    origin: semantic
created: "2026-08-01T14:00:00Z"
```

### Example Artifact-Registry Entry

```yaml
# In .protobot/project.yaml
artifacts:
  - id: vision
    kind: vision
    path: docs/vision.md
    digest: "sha256:example"
    owner: user
  - id: architecture
    kind: architecture
    path: docs/architecture.md
    digest: "sha256:example"
    owner: user
  - id: api-gateway-openapi
    kind: interface-idl
    path: specs/api-gateway.yaml
    digest: "sha256:example"
    owner: ears-manager
    validator: openapi-lint
```

---

## Related Documents

- [ADR-0001: One-File-Per-Record YAML Storage Format](0001-requirements-storage-format.md)
  — the physical format this schema populates
- [ears-manager](../architecture/components.md#ears-manager)
  — the component that enforces this schema
- [Phase 2: Dimensioning](../architecture/user-interaction-flow.md#phase-2-dimensioning)
  — the workflow that produces requirement records
- [Q19: Applicability metadata](../architecture/open-questions.md#q19-applicability-metadata-and-semantic-impact-coverage)
  — the open question about scope vocabulary
- [Content Storage Model](../architecture/components.md#content-storage-model)
  — where artifact-registry entries are stored
- #46 — the issue this ADR resolves
- #45 — storage format (resolved by ADR-0001, independent of
  this schema)
- #30 — `ears-manager` CLI integration (unchanged by this
  schema)
- #34 — Git and project-repository integration
