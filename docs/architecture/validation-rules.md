# ProtoBot: Validation Rules Contract

> Interface contract — issue #32 — September 2026
>
> Defines the shared lifecycle rules used before and at the WMS write
> boundary.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Boundary and ownership](#boundary-and-ownership)
- [MVP packaging decision](#mvp-packaging-decision)
- [Vocabulary and record context](#vocabulary-and-record-context)
- [Authorization context](#authorization-context)
- [Evaluation contract](#evaluation-contract)
- [MVP lifecycle](#mvp-lifecycle)
- [Early and authoritative checks](#early-and-authoritative-checks)
- [Rejection contract](#rejection-contract)
- [Deployment, security, and state](#deployment-security-and-state)
- [Conformance test matrix](#conformance-test-matrix)
- [Out-of-scope decisions](#out-of-scope-decisions)
- [Related Documents](#related-documents)

---

## Purpose and scope

Validation Rules answer whether a proposed work-item lifecycle command is
permitted. They are shared domain logic, not a WMS backend and not an
agent prompt. The same rules provide early diagnostics to the Drafting
Table and Job Site, then enforce the decision authoritatively at the WMS
write boundary.

This contract defines:

- the MVP work-item states and valid transitions;
- pipeline entry-point and readiness checks;
- the trusted authorization context for each mutation;
- expected state, contract version, and fencing-token checks;
- the difference between advisory preflight and an authoritative write;
- stable rule versions, rejection codes, and retry guidance; and
- deterministic acceptance evidence for concurrency and authorization.

Validation Rules do not define the WMS request/backlog API, backend-specific
translation, scheduling policy, or Finding Ledger event schema. Those
boundaries remain with the WMS Adapter, Job Site, and their related
contracts.

### Scope boundary with `ears-manager`

`ears-manager` validates specification content:

- EARS formatting and required metadata;
- registered artifact and interface references;
- explicit requirement relationships and change-set integrity;
- impact candidates and their recorded dispositions; and
- file format and schema-version consistency.

Validation Rules validate workflow around that content:

- whether a work item can enter a pipeline state;
- whether a caller may request a lifecycle command;
- whether the caller's expected state/version/fence is current; and
- whether an atomic transition is valid for the current record.

The WMS boundary may require a successful `ears-manager check` and a
complete impact assessment as transition preconditions. It does not
reimplement those specification checks.

---

## Boundary and ownership

Validation Rules are used by several components, but lifecycle state has
one authoritative owner: the WMS Adapter write boundary.

| Component | Responsibility | May it authoritatively mutate lifecycle state? |
| --- | --- | --- |
| Drafting Table | Run preflight checks and present diagnostics; submit a reviewed blocked-work resolution under delegated human approval. | No direct lifecycle mutation; the Materializer performs the authoritative transition after approval is verified. |
| Job Site | Request claims, leases, execution progress, inspection, and merge completion. | No direct store write; its commands go through the WMS boundary. |
| Materializer | Assemble and refresh complete work-item contracts after approved specification changes or true-bug intake. | Only through the WMS materialization/transition operation. |
| Reconciler role (Job Site Materializer/Dispatcher recovery) | Supply Git/WMS reconciliation evidence for lease recovery, merge recovery, and lost completion writes. | No direct store write; it uses the WMS boundary and cannot issue execution leases or mutate implementation branches. |
| WMS Adapter | Fetch current state, invoke the authoritative evaluator, and commit an allowed mutation atomically. | Yes. |
| Backend translator | Map the validated ProtoBot operation to GitHub, GitLab, Jira, Beads, or Trello. | No. It must not reimplement lifecycle rules. |
| `ears-manager` | Validate and resolve specification artifacts referenced by the work item. | No. Specification state and lifecycle state are separate. |
| Gate / authorization boundary | Validate identity and create the trusted authorization context. | No. It does not decide lifecycle validity. |
| Finding Router / Inspectors | Emit findings and proposed dispositions to the Job Site control plane. | No. Lifecycle-changing block or rework commands are mediated by the Job Site and checked at the WMS boundary. |

The evaluator may be called directly by a local single-player adapter or
inside a hosted WMS service. In every topology, a successful preflight is
not a successful mutation.

---

## MVP packaging decision

The MVP packages the rules as a versioned declarative state-machine
ruleset plus a deterministic evaluator with a stable input/output
contract. The initial ruleset identifier is:

```text
validation-rules/v1
```

The ruleset defines states, commands, allowed principals, preconditions,
transition effects, and stable rejection codes. The evaluator applies the
ruleset without model inference, network calls, or backend-specific logic.

An implementation may compile or embed the ruleset in a linkable library,
but callers use the same evaluator semantics. The MVP does not require a
networked Validation Rules service, a WIT binding, or rules expressed as
agent skills. Those can be added behind this contract after the first
local WMS fixture exists.

The ruleset version is selected by deployment or reviewed project policy;
the caller cannot select a weaker rule version in an individual request.
Every decision and authoritative WMS event records the applied
`rule_version`.

---

## Vocabulary and record context

### Work-item states

The following states are the complete MVP lifecycle vocabulary. `initial`
is an input condition, not a persisted state.

| State | Meaning |
| --- | --- |
| `waiting` | A durable contract exists, but one or more build-work dependencies remain. |
| `ready-for-building` | The contract is complete, impact is dispositioned, dependencies are resolved, and the item may be claimed. |
| `building` | One Job Site owns an active execution lease and is generating or integrating implementation artifacts. |
| `inspecting` | The candidate passed the Building test gate and independent inspection is in progress. |
| `blocked` | Progress requires specification, impact, policy, or reconciliation resolution. The execution lease is released. |
| `merging` | Inspection and final test gates passed; the tested candidate and merge operation are being reconciled. |
| `completed` | The resulting merge commit and immutable completion evidence are recorded. |
| `abandoned` | Terminal work that is intentionally canceled and confirmed not to have integrated Git mutations. |

Requirements do not have these states. They remain specification records;
the WMS owns mutable delivery state.

### Version and concurrency fields

| Field | Meaning | Required for |
| --- | --- | --- |
| `contract_version` | Monotonically increasing version of the complete work-item contract. Every accepted authoritative mutation increments it exactly once. | Every authoritative mutation as `expected_contract_version`. |
| `fencing_token` | Opaque token issued by a successful claim. It identifies the current execution lease and is never caller-generated. | Job Site mutations after `claim`, until the lease is released or reaches a terminal state. |
| `idempotency_key` | Stable key for one logical mutating command. The same key and request fingerprint return the original result. | Every authoritative mutation, including materialization and retries. |
| `materialization_key` | Stable identity of the logical work item, derived from its approved change set/merge or true-bug intake. It is distinct from a per-command retry key. | `materialize` create-or-return operations. |
| `rule_version` | Immutable identifier of the ruleset used for a decision. | Every decision and authoritative event. |
| `policy_version` | Version of the project/deployment authorization and readiness policy used with the ruleset. | Authoritative decisions when policy participates in a precondition. |

The WMS compares expected fields against the current record in the same
transaction that applies a successful transition. A caller-supplied
snapshot is never an authority for the current state. Materialization is
the one create-if-absent form of this operation: it uses
`expected_state: initial`, `expected_contract_version: 0`, and no current
record. A successful create starts at contract version 1.

### Pipeline entry point

The Materializer classifies an incoming request before it creates a work
item. The classification is part of the trusted command payload.

| Change type | Required input | Pipeline result |
| --- | --- | --- |
| `undefined` | An approved change set with added requirements and a reviewed impact assessment. | Dimensioning has completed; materialize to `waiting`, `ready-for-building`, or `blocked`. |
| `changes` | An approved change set with revised/retired requirements and a reviewed impact assessment. | Dimensioning has completed; materialize to `waiting`, `ready-for-building`, or `blocked`. |
| `contradicts` | No specification change; violated approved requirement IDs and affected scope are supplied. | Bypass Dimensioning and enter the Building pipeline directly; WMS materialization still produces `waiting`, `ready-for-building`, or `blocked` and requires a normal claim. |
| Any other value | No valid MVP classification. | Reject with `INVALID_CHANGE_TYPE`; do not create a work item. |

If an approved change set declares `implementation_required: false`,
materialization returns `omitted` rather than creating a build work item.
The WMS still stores a materialization reservation containing the source
fingerprint and `omitted` result. The `materialization_key` prevents
duplicate logical registration and the per-command `idempotency_key`
prevents duplicate command results.

---

## Authorization context

The Gate or local trusted adapter constructs the authorization context.
The evaluator does not trust caller-supplied role, project, work-item,
branch, or ref claims.

| Field | Required semantics |
| --- | --- |
| `subject` | Authenticated human, service, or local principal identity. |
| `role` | One of the roles permitted for the command: `human-maintainer`, `drafting-table`, `materializer`, `job-site`, or `reconciler`. |
| `project_id` | Project selected from trusted project configuration, not from an arbitrary command argument. |
| `work_item_id` | Target work item, when the command is item-scoped. The boundary checks that the principal is authorized for this project and item. |
| `change_set_id` | Related approved change set for materialization or reviewed resolution, when applicable. |
| `allowed_actions` | Actions granted to this principal for this request. The requested command must be in this set. |
| `allowed_refs` | Git refs or resource scopes the principal may affect. A caller cannot widen this list. |
| `expires_at` | Expiry of the authorization context. Expired contexts are rejected. |
| `policy_version` | Version of the authorization policy that produced the context. Required for every authoritative request. |

Blocked-work operations carry `human_approval_id` and
`approval_resolution_digest` as request references, not as fields of the
base `AuthorizationContext`. The WMS resolves the ID against Gate-owned
single-use approval state and compares the request digest with that record;
the caller cannot create or widen the approval binding.

The command authority is an exclusive mapping, not a default permission
floor:

| Role | Authoritative command families |
| --- | --- |
| `human-maintainer` | Approve blocked-work resolution and perform safe abandonment. |
| `materializer` | Materialization, dependency refresh, `resolve-block`, and contract revalidation. |
| `job-site` | `claim`, `renew-lease`, `tests-pass`, `raise-spec-question`, `refresh-active`, `return-to-building`, `begin-merge`, Job Site `merge-conflict`, and Job Site `record-merge`. |
| `reconciler` | `recover-lease`, `merge-conflict`, `merge-not-applied`, and reconciled `record-merge`. |
| `drafting-table` | Preflight and submission of a reviewed resolution under a trusted `human_approval_id`; it cannot claim, build, inspect, complete, or directly unblock a work item. |

For blocked-work resolution, the Drafting Table submits the user's
reviewed resolution and `human_approval_id` without changing lifecycle
state. The Materializer names the currently-active
`resolution_submission_id`, verifies that submission's approval, refreshes
the authoritative record (including any planned dependency recorded on
that submission), and performs `resolve-block` through the WMS boundary.
This keeps the conversational surface useful without granting it a direct
unblock authority. There is no same-state `blocked` mutation.

The approval binding is checked and consumed in the same transaction as
`resolve-block`. A mismatched, expired, revoked, or already-consumed
approval cannot unblock any item. The Materializer's trusted subject must
match the approval's delegated principal, and the approval's authorized
human subject must be preserved in the audit event before consumption.

Every authoritative evaluation fails closed unless the Gate-issued context
contains a non-empty authenticated `subject`, known `role`, trusted
`project_id`, unexpired `expires_at`, `policy_version`, non-empty
`allowed_actions`, and non-empty `allowed_refs`. Unknown roles, missing or
malformed fields, empty allowlists, and `*`/`all` wildcard entries are
rejected with `UNAUTHORIZED_ACTION`; the MVP has no wildcard exception.
The requested operation must be in both the role's exclusive command
family and Gate-issued `allowed_actions`. Payload refs must be a subset of
`allowed_refs`. A caller cannot select or downgrade `policy_version`.
Every authoritative `resolve-block` request must also carry
`human_approval_id`, `approval_resolution_digest`, and the currently-active
`resolution_submission_id`. The named submission must be the work item's
active nonterminal lifecycle resolution, and the approval must belong to
that submission. Their binding and single-use consumption occur after an
idempotency replay check, so an exact lost-response retry can return the
original result without consuming the approval twice. A superseded
submission's revoked approval cannot satisfy this binding.

The authorization context contains no downstream credential. In hosted
deployments, the Bridge/Gate obtains a scoped credential only after the
decision is authorized. In single-player mode, the local adapter uses the
user's own credential without exposing it to the evaluator.

---

## Evaluation contract

The evaluator is a pure decision function for a supplied record, trusted
context, and trusted evaluation time:

```text
evaluate(request, current_record, ruleset, evaluation_context) -> decision
```

The WMS boundary supplies `current_record` from its transaction. A
preflight caller supplies an observed snapshot and must treat the result
as advisory. `evaluation_context` contains the Gate/WMS-normalized
`evaluation_time`; the evaluator compares authorization and lease expiry
against that value rather than a caller clock.

The WMS transaction owns replay and materialization lookup. It atomically
loads the idempotency result and materialization reservation, returns a
stored decision for a replay or matching create-or-return, and invokes
`evaluate` only for a new command with the fresh current record and
evaluation context. The pure evaluator never queries a store.

### Request

Every authoritative request contains:

| Field | Description |
| --- | --- |
| `operation` | Canonical command from the MVP lifecycle table. Arbitrary state assignment is not an operation. |
| `target` | Project and work-item identity resolved by the trusted boundary. |
| `payload` | Command-specific data, such as change-set ID, merge commit, or resolution reference. |
| `authorization` | Trusted authorization context described above. |
| `materialization_key` | Stable logical work-item key for `materialize`; absent for later lifecycle commands. |
| `expected_state` | Exact current state the caller read. Required for state mutation. |
| `expected_contract_version` | Exact current contract version the caller read. Required for state mutation. |
| `fencing_token` | Current lease token for a Job Site mutation. Required after claim; absent for claim itself. |
| `idempotency_key` | Stable key for the logical operation. Required for authoritative mutation. |
| `observed_snapshot` | Optional caller snapshot used by preflight. It is ignored as authority by the WMS transaction. |

### Reconciliation evidence

Reconciliation-sensitive operations use evidence observed by the WMS and
trusted integration boundary, not a proof blob supplied by the caller. The
authoritative `current_record` carries:

| Field | Required meaning |
| --- | --- |
| `reconciliation.status` | One of `conflict`, `not-applied`, `merge-recorded`, `lease-recovered`, or `not-integrated`. |
| `reconciliation.git_mutation` | Observed Git result: `none`, `conflict`, or `merged`. |
| `reconciliation.merge_envelope` | For `merge-recorded`, the tested candidate digest, sealed Inspection Run, post-attestation integration head, target, contract version, and resulting merge commit. It may be absent for `conflict`, `not-applied`, `lease-recovered`, and `not-integrated`. |

The WMS compares this evidence with the current work-item contract. A
reconciler `merge-conflict` to `ready-for-building` requires a matching
`conflict`/non-merged result; `merge-not-applied` requires a matching
`not-applied`/`none` result; and reconciled `record-merge` requires a
matching `merge-recorded`/`merged` envelope. `recover-lease` requires an
expired lease plus `lease-recovered`/`none` evidence, and `abandon`
requires `not-integrated`/`none` evidence. Step 11 evaluates these same
current-record pairs. Missing, malformed, or mismatched evidence returns
`PRECONDITION_FAILED` with reconciliation details and cannot mutate state.
Callers cannot replace these fields with payload claims.

### Decision

The evaluator returns a deterministic decision with these fields:

| Field | Values or meaning |
| --- | --- |
| `outcome` | `allowed`, `rejected`, or `omitted`. |
| `replayed` | `true` when an exact idempotent retry returns a prior result; otherwise `false`. |
| `authority` | `preflight` or `authoritative`. |
| `rule_version` | For the MVP, `validation-rules/v1`. |
| `policy_version` | Policy version used for authorization/readiness checks. |
| `operation` | The evaluated command. |
| `before` | State and contract version observed for the decision. Materialization uses `initial` and version 0. |
| `after` | State and contract version that would result or did result. No state is changed by preflight. For `outcome: omitted`, this is `null` because no work-item lifecycle record exists. |
| `materialization_reservation` | For `materialize`, the source-fingerprinted reservation and result (`waiting`, `ready-for-building`, `blocked`, or `omitted`); omitted outcomes retain this reservation without an `after` lifecycle state/version. |
| `fencing_token_issued` | Present when `claim`, `refresh-active` to `building`, `return-to-building`, or Job Site `merge-conflict` to `building` succeeds; the token is returned only to the authorized owner channel. |
| `rejection` | Structured rejection data when `outcome` is `rejected`. |

The authoritative WMS operation commits the state mutation, contract
version increment, idempotency result, and required lifecycle/audit event
as one atomic operation. A lost response is therefore safe to retry with
the same idempotency key.

### Idempotency rules

1. The first request for an idempotency key evaluates and records its
    request fingerprint and result. The key is scoped to the project; the
    fingerprint includes target, operation, canonical payload,
    `materialization_key` when present, expected state/version/fence,
    subject, role, allowed actions and refs,
    `human_approval_id` and `approval_resolution_digest` when present,
    policy version, and rule version.
2. A retry with the same key and identical fingerprint returns the original
   result with `replayed: true` and performs no second mutation.
3. Reusing a key with a different operation, target, materialization key,
   expected state or version, fence, principal, authorization scope,
   approval, or payload is rejected with `IDEMPOTENCY_CONFLICT`.
4. A different key does not bypass current state, version, authorization,
   or fencing checks. A second claimant therefore cannot acquire an item
   already claimed by another owner.
5. `materialization_key` is the create-or-return identity for a logical
   work item or an `omitted` materialization reservation. Reusing it with
   a different source contract is an `IDEMPOTENCY_CONFLICT`, even when the
   per-command key is new.
6. A rejected request is also idempotent. The caller must use a refreshed
   expected version and a new key after correcting the cause.

Authorization expiry is checked before replay and is not part of the
fingerprint. A lost-response retry therefore obtains a fresh context with
the same subject, role, scopes, policy version, and approval, then retries
the same key. An expired or changed authorization cannot be used to replay
or mutate a result.

---

## MVP lifecycle

### Valid transitions

All transitions not listed here are invalid. Same-state operations such as
lease renewal are included explicitly so that a caller cannot turn them
into an arbitrary update.

| Operation | From | To | Required conditions |
| --- | --- | --- | --- |
| `materialize` | `initial` at version 0 | `waiting`, `ready-for-building`, `blocked`, or `omitted` | A unique `materialization_key` and complete contract are supplied; the result follows dependency, impact, readiness, and implementation-effect checks. |
| `refresh-dependencies` | `waiting` | `ready-for-building` | All dependencies are completed and the full pre-claim refresh passes. |
| `revalidate` | `waiting` or `ready-for-building` | `blocked` | Refresh finds unresolved impact, specification, policy, or reconciliation work. |
| `resolve-block` | `blocked` | `ready-for-building` | Every authoritative request includes a Gate-bound `human_approval_id`, matching `approval_resolution_digest`, and the currently-active `resolution_submission_id`; the approval is consumed only after a full refresh passes, including any planned dependency recorded on that submission. Conversation alone cannot perform this transition. There is no same-state `blocked` mutation. |
| `claim` | `ready-for-building` | `building` | Atomic expected-state/version check passes; a new owner, lease, and fencing token are recorded. |
| `renew-lease` | `building` or `inspecting` | Same state | Current owner presents the current fencing token and an unexpired authorization context. |
| `tests-pass` | `building` | `inspecting` | Current owner presents the expected state/version/fence and the Building gate has passed. |
| `raise-spec-question` | `building` or `inspecting` | `blocked` | An undefined behavior or omitted obligation is recorded; the execution lease is released. |
| `refresh-active` | `building` or `inspecting` | `building` | The current owner presents its fence; the latest main/policy state is compatible, the contract version is incremented, and the old fence is atomically replaced with a new lease fence. |
| `refresh-active` | `building` or `inspecting` | `blocked` | Active refresh finds unresolved impact, specification, policy, or reconciliation work; the old lease is released. |
| `return-to-building` | `inspecting` | `building` | In-contract defect or failed final test requires rework. The current fence is checked and atomically replaced with a new lease fence. |
| `begin-merge` | `inspecting` | `merging` | Inspection Run is sealed, all findings are terminal, and the final test gate passed. |
| `merge-conflict` | `merging` | `building` | A Job Site owner presents the current merge fence; the WMS atomically replaces it with a new Job Site execution lease fence after conflict reconciliation. |
| `merge-conflict` | `merging` | `ready-for-building` | A trusted `reconciler` operation evaluates matching WMS-observed `conflict`/non-merged evidence; no live fence is required and the next Job Site must claim the item normally. |
| `merge-not-applied` | `merging` | `ready-for-building` | A trusted `reconciler` operation evaluates matching WMS-observed `not-applied`/`none` evidence; no live fence is required and all gates must run again before a new claim. |
| `record-merge` | `merging` | `completed` | A Job Site supplies the current fence, or a trusted `reconciler` operation evaluates matching WMS-observed `merge-recorded`/`merged` evidence; the merge envelope matches. |
| `recover-lease` | `building` or `inspecting` | `ready-for-building` | The lease is expired, Git/WMS reconciliation is complete, and no unrecorded mutation remains. |
| `abandon` | `waiting`, `ready-for-building`, `building`, `inspecting`, or `blocked` | `abandoned` | An authorized maintainer confirms cancellation and reconciliation proves that integration cannot have occurred. |

`merging` cannot be abandoned because Git may already have been mutated.
`completed` and `abandoned` are terminal. A new logical attempt after
abandonment receives a new materialization key and work-item identity.

Any transition that returns to `building` issues a fresh fencing token in
the same atomic operation that changes the state. The prior token is
invalidated before the decision is returned. The new owner is the subject
in the trusted authorization context; a caller cannot transfer ownership
by putting another subject in the command payload.

### Readiness and materialization outcomes

The `materialize` and refresh operations require all of the following
before returning `ready-for-building`:

- the source specification commit is immutable and resolvable;
- `ears-manager` has validated the registered specification artifacts;
- changed and applicable requirement IDs resolve at that commit;
- every impact candidate has an `applicable` or `not-applicable`
  disposition with rationale;
- the change type is valid and its pipeline entry point is satisfied;
- no unresolved dependency remains; and
- the work-item contract contains its source, scope, provenance,
  materialization key, and required policy versions.

`materialize` returns `waiting` when the contract is complete but a
dependency remains. `refresh-dependencies` is accepted only after those
dependencies complete and then returns `ready-for-building`; it does not
silently preserve an unresolved dependency. If review, policy, impact, or
reconciliation is unresolved, `revalidate` returns `blocked`. A complete
contract with no implementation effect returns `omitted` and creates no
executable work item.

`resolve-block` uses the same full refresh. For an `add-requirement`
submission, that refresh also requires the planned dependency recorded on
the named `resolution_submission_id` to be completed. The planned
dependency is not copied onto the work item before the transition; an
incomplete planned dependency returns `PRECONDITION_FAILED` and leaves
the item `blocked`.

`refresh-active` is the pre-merge revalidation path for a Job Site that
still holds an active lease. A passing refresh records a new contract
version, incorporates the compatible source/policy state, invalidates the
old fence, and keeps the item in `building` with a new fence. A failing
refresh releases the lease and moves the item to `blocked`; the caller
cannot continue execution until a reviewed resolution and later refresh
pass.

### Invalid transitions

The following are representative invalid requests. The rule is closed:
any state/operation pair absent from the valid-transition table is also
invalid.

| Invalid request | Rejection |
| --- | --- |
| Claim `waiting` or `blocked` with no active owner while `expected_state` differs from the current state | `STALE_STATE`; refresh before choosing the next command. |
| Claim `waiting` or `blocked` with no active owner while `expected_state` matches the current state | `INVALID_TRANSITION`; resolve dependencies or the block before claiming. |
| Claim `building` or `inspecting` with an active owner/lease | `DUPLICATE_CLAIM`; query the current owner/status rather than retrying the claim. |
| Move `blocked` directly to `building` | `INVALID_TRANSITION`; a reviewed resolution and full refresh must produce `ready-for-building` first. |
| Move `ready-for-building` directly to `inspecting` or `completed` | `INVALID_TRANSITION`; the item must be claimed and pass the intervening gates. |
| Move `building` directly to `merging` | `INVALID_TRANSITION`; `tests-pass`, inspection, and `begin-merge` are required. |
| Move `inspecting` directly to `completed` | `INVALID_TRANSITION`; `begin-merge` and a recorded merge envelope are required. |
| Abandon `merging` | `INVALID_TRANSITION`; reconcile the Git mutation and complete or recover it. |
| Mutate `completed` or `abandoned` | `ALREADY_TERMINAL`; create or locate the replacement logical work item. |
| Renew a lease without the current fencing token | `STALE_FENCING_TOKEN`; refresh/reconcile before retrying. |

---

## Early and authoritative checks

Both paths use the same ruleset and decision shape, but they have different
authority.

| Concern | Early preflight | Authoritative WMS check |
| --- | --- | --- |
| Caller | Drafting Table, Job Site, or Materializer before a write. | WMS Adapter/Gate in the write transaction. |
| Record | Caller-observed snapshot. | Fresh current record loaded by the WMS boundary. |
| Purpose | Fast diagnostics and better UX before a network or backend call. | Final authorization, concurrency check, rule evaluation, and mutation gate. |
| Mutation | None. | State, version, lease/fence, idempotency result, and required event commit atomically. |
| Stale data | May report a possible success; caller must still submit the command. | Rejects mismatched expected state/version/fence without mutation. |
| Result | `authority: preflight`; never proof that the operation succeeded. | `authority: authoritative`; the only source of lifecycle success. |

The WMS boundary must not implement "validate, then write" as two
independent operations. It evaluates the fresh record and applies the
conditional mutation in one transaction or through the same external
coordinator used for atomic claims.

The Drafting Table may use preflight to show why a blocked resolution,
claim, or other command would fail. It must display an authoritative
rejection if the record changed after preflight and must not convert the
preflight result into conversational success.

---

## Rejection contract

Every rejection includes the following machine-readable fields:

```yaml
rejection:
  code: STALE_CONTRACT_VERSION
  message: >-
    The work item changed after this command was prepared.
  details:
    expected_contract_version: 4
    current_contract_version: 5
  retry: refresh
```

`message` is actionable presentation text. Clients branch on `code` and
use `details`; they must not parse the message. The `rule_version` and
`policy_version` remain present at the decision level for replay.

`retry` is one of the stable values `never`, `authorize`, `refresh`,
`reconcile`, `query`, or `new-key`. Every rejection includes the
non-secret detail fields listed for its code below.

The MVP rejection codes are:

| Code | Meaning | Retry | Required non-secret details |
| --- | --- | --- | --- |
| `UNAUTHORIZED_ACTION` | Trusted context does not permit the requested command or target. | `authorize` | `required_action`, `target_type`, `policy_version` |
| `INVALID_CHANGE_TYPE` | Materialization supplied a change classification outside the MVP vocabulary. | `new-key` | `provided_type`, `allowed_types` |
| `INVALID_TRANSITION` | The command is not valid from the current state. | `refresh` | `operation`, `current_state`, `allowed_operations` |
| `PRECONDITION_FAILED` | A state-specific readiness, inspection, evidence, dependency, or reconciliation condition is not satisfied. | `refresh` | `failed_precondition`, `required_evidence` or `blocking_dependency` |
| `STALE_STATE` | `expected_state` does not match the current state. | `refresh` | `expected_state`, `current_state` |
| `STALE_CONTRACT_VERSION` | `expected_contract_version` does not match the current version. | `refresh` | `expected_contract_version`, `current_contract_version` |
| `STALE_FENCING_TOKEN` | The caller's lease token is missing, expired, or no longer current. | `reconcile` | `fence_status`, `current_contract_version` |
| `DUPLICATE_CLAIM` | Another owner successfully claimed the item or the claim is otherwise no longer available. | `query` | `current_state`, `current_contract_version`, `claim_status` |
| `IDEMPOTENCY_CONFLICT` | An idempotency key was reused with a different request fingerprint or source contract. | `new-key` for a request-fingerprint conflict; `reconcile` for a materialization-source conflict. | `conflict_kind`, `key_scope`, `request_fingerprint_digest`, `recorded_fingerprint_digest`, or `materialization_key`, `recorded_source_digest` |
| `ALREADY_TERMINAL` | The work item is `completed` or `abandoned`. | `never` | `terminal_state`, `current_contract_version` |
| `NOT_FOUND` | The target project or work item is not visible in the trusted context. | `refresh` | `target_type`, `visibility_scope` |

`STALE_STATE`, `STALE_CONTRACT_VERSION`, and `STALE_FENCING_TOKEN` are
separate codes even when they occur in one failed request. The boundary
reports the first failed check using a deterministic check order:

1. target visibility and complete authorization-context validation;
2. role-family, `allowed_actions`, and payload-ref subset checks;
3. idempotency-key replay or conflict;
4. `materialize` create-or-return by `materialization_key`;
5. `resolve-block` active `resolution_submission_id` and approval binding,
   when that operation is requested; single-use consumption is recorded
   only if the command is later allowed;
6. terminal-state check;
7. active-owner contention for `claim` (`DUPLICATE_CLAIM`);
8. expected state;
9. expected contract version;
10. live fencing token and lease, only for live-owner operations that
    require one: `renew-lease`, `tests-pass`, `refresh-active`,
    `return-to-building`, `begin-merge`, `raise-spec-question`, Job Site
    `merge-conflict`, and Job Site `record-merge`;
11. transition and command preconditions.

The claim-specific contention check applies only when the current record
has an active owner or lease. A claim against another non-claimable state
without an active owner uses `STALE_STATE` or `INVALID_TRANSITION` as
appropriate. This keeps duplicate claims distinguishable from ordinary
stale reads.

For `materialize`, the create-or-return check treats a missing work-item
record as an expected create when the project and `materialization_key`
are authorized. If an existing record has the same source-contract
fingerprint, the evaluator returns it with `replayed: true` and performs
no mutation. An existing `omitted` reservation with the same fingerprint
returns the prior `omitted` result with `replayed: true`. A different
source fingerprint returns `IDEMPOTENCY_CONFLICT` before expected
state/version checks.

`recover-lease` intentionally requires an expired or missing live lease
after Git/WMS reconciliation, so it is exempt from step 10. `abandon` is a
maintainer operation and is also exempt; its cancellation and
reconciliation preconditions authorize it without a Job Site fence.

`merge-conflict`, `merge-not-applied`, and reconciled `record-merge` are
also reconciliation operations. A trusted `reconciler` role for one of
these operations skips the live-owner fence check; step 11 then evaluates
the required WMS-observed evidence on `current_record`. A reconciler
conflict returns the item to `ready-for-building` for a fresh Job Site
claim; it never issues a Job Site fence to the reconciler. Caller payload
claims cannot satisfy or replace the evidence check.

The response never returns a credential or an untrusted caller claim.

---

## Deployment, security, and state

### Deployment topology

The rules and rejection semantics are identical in all supported modes.

| Mode | Evaluator and WMS boundary | Authorization |
| --- | --- | --- |
| Single-player | Local WMS process over MCP stdio, with a local or in-memory claim coordinator. | User's local Git/WMS identity; no hosted OAuth service is required. |
| Multi-player | Network WMS service and shared claim coordinator. | OAuth 2.1 through the Bridge/Gate pattern. |
| Web | Shared network WMS service and project-aware claim coordinator. | OAuth 2.1 through the Bridge/Gate pattern plus application authorization. |

The local path is not allowed to bypass expected-version, fencing, or
idempotency checks merely because it has one expected user. Multi-player
and web deployments must not weaken the rules to match a backend's native
issue/card semantics.

### Security posture

- The Gate validates token signature, issuer, audience, expiry, and
  subject before constructing the context.
- Project, work-item, change-set, branch, and action scope come from
  trusted configuration and authorization, not caller-provided claims.
- Workers and Inspectors do not receive WMS mutation credentials or a
  lifecycle mutation role.
- A downstream credential is obtained only after the decision is allowed
  and is not returned in the decision or exposed to the evaluator.
- Rule and policy versions, principal, action, target, expected version,
  idempotency key, outcome, and resulting version are recorded for audit.

### Persistent state

Validation Rules are stateless. The following existing stores remain the
authorities:

| State | Owner | Validation Rules responsibility |
| --- | --- | --- |
| Request backlog, request revisions, and blocked-resolution submissions | WMS Adapter/backend | Validate request-namespace preconditions and supply durable submission records; consume only the currently-active submission's approval and lifecycle fields during authoritative `resolve-block`. |
| Work-item state, contract versions, leases, and fencing tokens | WMS Adapter and its claim coordinator | Validate all reads used for a mutation and require atomic compare-and-swap semantics. |
| Idempotency results, materialization reservations, and lifecycle audit events | WMS Adapter / external coordinator | Ensure retries return the original result and never duplicate a mutation, including `omitted` outcomes. |
| Specification records and impact dispositions | Git through `ears-manager` | Consume successful validation/check evidence; do not parse or mutate records. |
| Project/deployment policy | `.protobot/policy.yaml` or deployment configuration | Select the compatible rule/policy version; changes are reviewed. |
| Web session state | Web Drafting Table deployment | Supply authenticated project/session context only; never become lifecycle state or an alternate write authority. |
| Deployment-level project registry | Deployment operator | Supply project registration and adapter configuration only; never own work-item state or override a trusted working-tree identity. |
| Evidence and attestation artifacts | Job Site artifact store | Require the appropriate evidence references before merge completion; do not own their storage. |

Changing `validation-rules/v1` semantics requires a reviewed ruleset
version and a migration/compatibility plan. A caller cannot request a
different rule version to make a currently invalid transition pass.

---

## Conformance test matrix

The MVP fixture suite uses a fake WMS store with atomic compare-and-swap,
a fake Gate that produces trusted contexts, and the same evaluator used by
preflight and authoritative checks. Each case asserts `rule_version`,
`policy_version`, `authority`, and the returned decision. Each
authoritative case also asserts the absence of an extra mutation on
rejection or replay, plus one audit event for each accepted mutation.

| ID | Setup and command | Expected result |
| --- | --- | --- |
| `VR-001` | `ready-for-building`, version 4; authorized `job-site` claims with expected state/version. | Allowed; state becomes `building`, version becomes 5, and exactly one new fencing token is issued. |
| `VR-002` | Drafting Table preflights a claim against an observed `ready-for-building` version 4 snapshot; another owner claims it before the original request is submitted. | Preflight is advisory and does not mutate; the authoritative retry is rejected with `DUPLICATE_CLAIM`, with matching rule/policy metadata and no mutation. |
| `VR-003` | Claim expects `ready-for-building`, but the current state is `blocked` with no active owner. | Rejected with `STALE_STATE`; no mutation; caller must refresh. |
| `VR-004` | Claim expects version 4, but the current `ready-for-building` record is version 5. | Rejected with `STALE_CONTRACT_VERSION`; no mutation. |
| `VR-005` | Two authorized Job Sites claim the same version-4 ready item with different keys. | First claim succeeds; second is rejected with `DUPLICATE_CLAIM` and cannot replace the owner or token. |
| `VR-006` | Drafting Table attempts `claim` or `record-merge` using its trusted `drafting-table` role. | Rejected with `UNAUTHORIZED_ACTION`; no mutation. |
| `VR-007` | Current item is `building` with fence `F1`; a later request presents stale fence `F0`. | Rejected with `STALE_FENCING_TOKEN`; no mutation. |
| `VR-008` | A successful claim response is lost; the owner retries the exact claim with the same key and fingerprint. | Original allowed result is returned with `replayed: true`; version and lease are not incremented again. |
| `VR-009` | A caller reuses the successful claim's key with a different target or payload. | Rejected with `IDEMPOTENCY_CONFLICT`; no mutation. |
| `VR-010` | A different authorized subject reuses the successful claim's key. | Rejected with `IDEMPOTENCY_CONFLICT`; the prior result is not replayed across principals. |
| `VR-011` | A blocked item is sent directly to `building` without a reviewed resolution and refresh. | Rejected with `INVALID_TRANSITION`; the item remains `blocked`. |
| `VR-012` | A rejected claim response is lost; the owner retries the exact request with the same key, expected values, principal, and fingerprint. | The recorded rejection is replayed; no mutation occurs. A refreshed command must use a new key. |
| `VR-013` | Materialize a complete approved change-set contract with unresolved impact candidates. | Item is created at version 1 as `blocked`; it cannot be `ready-for-building`. |
| `VR-013B` | Materialize an incomplete contract with missing required source or impact data. | Rejected with `PRECONDITION_FAILED`; no work item is created. |
| `VR-014` | Materialize a complete contract with an unfinished dependency, complete that dependency through an authoritative WMS operation, then refresh. | Initial state is `waiting`; refresh observes the live dependency state and moves it to `ready-for-building` only after all checks pass. |
| `VR-015` | `building` owner reports passing tests with the current state/version/fence, then begins merge after sealed inspection. | `building -> inspecting -> merging` succeeds only in order and with all evidence gates. |
| `VR-016` | A `job-site` owner presents the current merge fence for a Git conflict. | `merging -> building` succeeds with the old fence invalidated and a new Job Site fence issued. |
| `VR-017` | A `reconciler` proves a `merging` item was not mutated in Git. | `merging -> ready-for-building` succeeds directly without a live fence; the next Job Site must claim it. |
| `VR-018` | Exact `record-merge` retry repeats after the WMS response is lost. | Original completion is returned with `replayed: true`; no second merge event or completion mutation is created. |
| `VR-019` | A `completed` or `abandoned` item receives any lifecycle command. | Rejected with `ALREADY_TERMINAL`; no mutation. |
| `VR-020` | `building` or `inspecting` owner runs `refresh-active` with the current fence and compatible latest main. | Contract version increments, the old fence is rejected, and a new fence is issued for continued `building`. |
| `VR-021` | An `inspecting` owner returns to work after an in-contract defect. | `return-to-building` issues a new fence; the old fence cannot mutate the new attempt. |
| `VR-022` | Drafting Table submits a blocked resolution with missing or forged `human_approval_id`. | Rejected with `UNAUTHORIZED_ACTION`; the item remains `blocked`. |
| `VR-023` | Drafting Table submits a valid reviewed resolution; Materializer performs `resolve-block` naming the active submission whose refresh preconditions pass. | Approval is verified and consumed, `blocked -> ready-for-building` succeeds, and the Drafting Table itself performs no lifecycle mutation. |
| `VR-024` | Materialize at `initial` version 0 with a new `materialization_key`, then repeat with the same source contract and a new command key. | The first result creates version 1; the second returns the existing item without a duplicate. |
| `VR-025` | Reuse a `materialization_key` with a different source commit or contract payload. | Rejected with `IDEMPOTENCY_CONFLICT`; no second item is created. |
| `VR-026` | A reconciler runs `recover-lease` for an expired/missing-fence `building` item whose `current_record` has matching `lease-recovered`/`none` evidence. | Allowed; the item returns to `ready-for-building` without a fencing-token rejection, and the old lease cannot write. |
| `VR-027` | An authorized maintainer runs `abandon` for a `building` item whose `current_record` has matching `not-integrated`/`none` evidence. | Allowed; the item becomes `abandoned` without a Job Site fence. |
| `VR-028` | An authoritative request has a missing/unknown role, expired context, or malformed/missing required authorization field. | Rejected with `UNAUTHORIZED_ACTION`; no mutation. |
| `VR-029` | A request has empty or wildcard allowlists, or attempts to select a weaker `policy_version`. | Rejected with `UNAUTHORIZED_ACTION`; no mutation. |
| `VR-030` | A valid `human_approval_id` bound to another work item or resolution is used for `resolve-block`. | Rejected with `UNAUTHORIZED_ACTION`; the approval is not consumed and the item remains `blocked`. |
| `VR-031` | A new `resolve-block` request uses an approval that is expired, revoked, already consumed, or has a mismatched resolution digest. | Rejected with `UNAUTHORIZED_ACTION`; no lifecycle mutation occurs. |
| `VR-032` | A role-valid Materializer requests `resolve-block` without `human_approval_id`, `approval_resolution_digest`, or the currently-active `resolution_submission_id`. | Rejected with `UNAUTHORIZED_ACTION`; the item remains `blocked`. |
| `VR-033` | A successful `resolve-block` response is lost; the Materializer retries the exact request with the same key after the approval was consumed. | The original allowed result is replayed before approval consumption is checked again; no second transition occurs. |
| `VR-034` | A trusted `reconciler` handles a merge conflict without a Job Site fence while `current_record` contains matching `conflict`/non-merged evidence. | `merging -> ready-for-building` succeeds without issuing a fence to the reconciler; caller proof fields are ignored. |
| `VR-035` | Git merge is recorded, but the WMS record remains `merging` because the completion write did not commit; `current_record` contains valid merge evidence and a `reconciler` calls `record-merge` with an empty or forged payload. | Completion follows the current WMS evidence and work-item contract, preserves that evidence, and an identical retry returns `replayed: true`. |
| `VR-036` | A `job-site` presents a stale or missing fence for `merge-conflict` or `record-merge`. | Rejected with `STALE_FENCING_TOKEN`; no mutation and no new lease are issued. |
| `VR-037` | A `reconciler` invokes `merge-conflict`, `merge-not-applied`, or `record-merge` with missing, malformed, or mismatched WMS-observed evidence on `current_record`. | Rejected with `PRECONDITION_FAILED`; no mutation and no execution lease are issued. |
| `VR-038` | A reconciler includes forged proof in the request payload while `current_record` contains valid matching evidence. | The payload claim is ignored; the decision follows `current_record` and no caller-supplied proof is trusted. |
| `VR-039` | An `implementation_required: false` materialization returns `omitted`, then repeats with the same source or a different source under the same `materialization_key`. | Same source replays the stored `omitted` result; a different source is rejected with `IDEMPOTENCY_CONFLICT`. |
| `VR-040` | `recover-lease` or `abandon` is requested with missing, malformed, or mismatched reconciliation evidence on `current_record`. | Rejected with `PRECONDITION_FAILED`; no mutation occurs even when the caller has the correct role. |
| `VR-041` | A claim uses `expected_state: blocked` against a current blocked item with no active owner. | Rejected with `INVALID_TRANSITION`; the expected state is current but the item must be resolved before claiming. |
| `VR-042` | A Materializer uses a valid approval whose delegated principal or approved human subject does not match the trusted authorization context. | Rejected with `UNAUTHORIZED_ACTION`; the approval is not consumed and the item remains `blocked`. |
| `VR-043` | A role-valid Materializer requests `resolve-block` with a `human_approval_id` belonging to a superseded resolution submission, or names a `resolution_submission_id` that is not currently active. | Rejected with `UNAUTHORIZED_ACTION`; no lifecycle mutation occurs. |
| `VR-044` | A role-valid Materializer requests `resolve-block` naming the active `add-requirement` submission whose planned dependency is not completed. | Rejected with `PRECONDITION_FAILED`; the item remains `blocked` and the approval is not consumed. |
| `VR-045` | A Job Site preflights `tests-pass` and `renew-lease` with the current live fencing token and the same command payloads used by authoritative evaluation. | Each preflight decision agrees with the evaluator for the supplied snapshot, including its after-state; neither call mutates WMS state. |
| `VR-046` | Submit an `add-requirement` resolution while its planned dependency is incomplete, complete that dependency through an authoritative `record-merge`, then retry `resolve-block` with a fresh idempotency key. | The first resolve is rejected with `PRECONDITION_FAILED` and leaves approval unused; after completion, the retry observes the current dependency state and succeeds. |
| `VR-047` | Materialize with `change_type` supplied only at payload level, then retry the same materialization key with a new idempotency key. | The canonical source fingerprint matches and the existing materialization is returned as replayed, not rejected with `IDEMPOTENCY_CONFLICT`. |
| `VR-048` | Refine a request with an approval missing or mismatching `project_id`. | Rejected with `UNAUTHORIZED_ACTION`; the request and approval remain unchanged. |
| `VR-049` | A lifecycle payload names a merge target, integration head, merge commit, or inspection run outside Gate `allowed_refs`; exercise authoritative `record-merge`, its preflight, and a `materialize` source contract. | Out-of-scope references are rejected with `UNAUTHORIZED_ACTION` before mutation; matching allowed references proceed to lifecycle evaluation. |

The matrix covers the required stale-write, duplicate-claim,
unauthorized-mutation, and idempotent-retry cases. Backend adapter tests
must add translator-specific failures without changing these expected
decisions.

---

## Out-of-scope decisions

| Topic | Owner or reason |
| --- | --- |
| WMS request/backlog operation shapes | #31, [Drafting Table WMS Integration](drafting-table-wms.md). |
| Backend-specific issue/card/API mapping | WMS Adapter implementation behind this contract. |
| EARS, artifact, relationship, or impact-record validation | `ears-manager`, #30 and its implementation issues. |
| Job Site scheduling, worker isolation, sandboxing, and Finding Ledger schema | Job Site contracts and project policy. |
| Exact OAuth, Bridge, Gate, or downstream credential implementation | Deployment and security infrastructure; this contract only consumes trusted context. |
| Hosted service topology and session management | Web Drafting Table and deployment contracts. |
| WIT bindings or a separate Validation Rules network service | Future packaging after the MVP evaluator is proven. |

---

## Related Documents

- [Vision](../vision.md) — Purpose, users, outcomes, and prototype scope.
- [Architecture](../architecture.md) — External interfaces, persistent
  state, deployment modes, and environmental constraints.
- [Overview](overview.md) — Guiding principles, workflow, and platform.
- [System Components](components.md) — WMS lifecycle, component ownership,
  Job Site, and security boundaries.
- [Drafting Table WMS Integration](drafting-table-wms.md) — Backend-neutral
  request, query, linking, and blocked-resolution operations.
- [User Interaction Flow](user-interaction-flow.md) — Change types, work-item
  lifecycle, and testing strategy.
- [Drafting Table UX](drafting-table-ux.md) — Preflight, blocked-work,
  stale-state, and mutation ownership behavior.
- [Git and Project-Repository Integration](git-integration.md) — Git/WMS
  registration and merge boundaries.
- [Source Control Manager](source-control-manager.md) — Reuses this
  contract's authorization context for Git, with a proposed
  `change_set_id`.
- [Open Design Questions](open-questions.md) — Cross-cutting questions about
  interactive, autonomous, compliance, and interface concerns.
- [ADR-0001](../decisions/0001-requirements-storage-format.md) — Physical
  specification storage and reviewability.
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md) —
  Specification record schemas and impact metadata.
