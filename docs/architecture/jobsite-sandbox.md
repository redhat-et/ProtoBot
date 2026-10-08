# ProtoBot: Job Site Sandbox Contract

> Interface contract — issue #79 — October 2026
> Document revision: `jobsite-sandbox/v1`
>
> Defines the backend-neutral execution and sandbox contract that a
> local adapter, Fullsend/OpenShell adapter, or future backend must
> pass before the Job Site may run autonomous work.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Boundary and ownership](#boundary-and-ownership)
- [Contract version and capabilities](#contract-version-and-capabilities)
- [Adapter seam](#adapter-seam)
- [Filesystem and Git isolation](#filesystem-and-git-isolation)
- [Network policy](#network-policy)
- [Credential isolation](#credential-isolation)
- [Lifecycle, cancellation, and cleanup](#lifecycle-cancellation-and-cleanup)
- [Resource limits](#resource-limits)
- [Audit evidence](#audit-evidence)
- [Fail-closed behavior](#fail-closed-behavior)
- [Deployment, security, and state](#deployment-security-and-state)
- [Local test adapter](#local-test-adapter)
- [Conformance test matrix](#conformance-test-matrix)
- [Fullsend and OpenShell mapping](#fullsend-and-openshell-mapping)
- [Out-of-scope decisions](#out-of-scope-decisions)
- [Related Documents](#related-documents)

---

## Purpose and scope

This contract turns the Job Site execution and sandbox requirements
into an acceptance suite that every backend must pass. Prompts are
not a security boundary. A product-name claim is not sufficient.

The contract covers:

- filesystem and Git-object isolation for Worker role projections;
- per-executable and per-destination L7 network policy;
- credential non-exposure, with injection only at a boundary broker;
- cancellation, lease expiry, and ephemeral cleanup;
- wall-clock, CPU, memory, process-count, and writable disk/file
  limits; and
- tamper-evident private audit evidence tied to the work-item trace.

The local test adapter in
`source-control-manager/internal/jobsite` passes this suite without a
cluster, external network access, or real credentials. It builds on
the Worker projection isolation fixtures from issue #78.

### Relationship to sibling contracts

- [System Components — Job Site](components.md#job-site) owns
  autonomous Building and Inspecting and names this sandbox as the
  primary enforcement mechanism.
- [Worker repository projections](components.md#worker-repository-projections-decided)
  define the role snapshots this contract isolates.
- [Authentication and Credential Isolation][auth-isolation]
  defines the Bridge/Gate pattern the boundary broker follows.
- [Source Control Manager](source-control-manager.md) remains the Git
  and Git host boundary for the Drafting Table. This contract does
  not add a Job Site SCM face
  ([Q21](open-questions.md#q21-source-control-manager-job-site-face)).
- [Validation Rules](validation-rules.md) and the
  [Drafting Table WMS Integration Contract](drafting-table-wms.md)
  own work-item lifecycle. The sandbox records a work-item trace; it
  does not mutate WMS state.
- [Related Work](related-work.md) records Fullsend and OpenShell as
  execution backends behind this contract, not as ProtoBot control-plane
  dependencies.

### Non-goals

This contract does not:

- run the deterministic Worker/Triage control-plane loop (#69);
- implement the concrete Fullsend/OpenShell adapter, runtime pinning,
  or live Fullsend execution (#172);
- own corpus fixtures or the Job Site conformance runner (#217,
  #219); or
- replace Projection export, PatchBundle v1 validation, or private
  Integration Git writes from #78.

---

## Boundary and ownership

| Actor or component | Sandbox responsibility | May it weaken policy? |
| --- | --- | --- |
| Job Site control plane | Select a backend, open sessions against role projections, and refuse autonomous execution when the suite fails. | No. |
| Sandbox adapter | Enforce isolation, network policy, credential brokering, limits, cleanup, and audit for one Worker session. | No. |
| Local test adapter | Deterministic in-process backend used by this suite. | No. |
| Fullsend / OpenShell adapter | Map this seam onto OpenShell (or Fullsend's OpenShell path) without becoming a ProtoBot control-plane dependency. | No. |
| Portable rootless-OCI/microVM profile | Independent fallback when OpenShell cannot pass the suite on a platform. | No. A plain container is not sufficient. |
| Worker / Inspector agent | Run only inside a session that already passed Open. | No. Prompts are not a security boundary. |
| Patch/Ownership Validator | Treat returned PatchBundle v1 payloads as untrusted and re-check role paths. | No. |
| WMS Adapter | Own work-item lifecycle. Consume only the work-item identifier on audit records. | No sandbox mutation of WMS state. |

The adapter is a Job Site execution seam. ProtoBot continues to own
materialization, projection, PatchBundle validation, private
Integration, Triage sanitization, and conformance evidence.

---

## Contract version and capabilities

The immutable v1 identifier is `jobsite-sandbox/v1`. Changing denial
semantics, required capabilities, or audit hashing requires a new
contract version and a reviewed compatibility plan.

Every v1 backend must report these capabilities, each either
supported or not, with no silent skip:

| Capability | v1 requirement |
| --- | --- |
| `filesystem-isolation` | Reads and writes stay inside the role projection. |
| `git-isolation` | Forbidden refs, objects, remotes, reflogs, and alternates stay unavailable. |
| `network-policy` | Deny by default; allow only an approved executable to an approved destination, protocol, method, and L7 path. |
| `credential-broker` | Downstream credentials never enter the sandbox; the broker injects them after authorization and strips them before responses return. |
| `lifecycle-cleanup` | Cancel, lease expiry, and Close terminate the process tree and remove ephemeral state. |
| `resource-limits` | Enforce wall-clock, CPU, memory, process-count, and writable disk/file limits. |
| `audit-chain` | Record a SHA-256 hash chain of sandbox events tied to the work-item trace. |

A missing capability, unsupported platform, or failed check disables
autonomous execution. The backend must not fall back to a weaker
profile.

---

## Adapter seam

Backends implement one adapter with two operations plus session
methods. The Go types live in
`source-control-manager/internal/jobsite` so #172 and #219 can invoke
the same suite.

| Operation | Input | Result |
| --- | --- | --- |
| `Identity` | — | Backend name and version. |
| `Capabilities` | — | The v1 capability set and support flags. |
| `Open` | Role, projection, projection policy, limits, network allowlist, work-item trace, optional lease | A session, or a fail-closed error. |
| Session `ReadPath` / `WritePath` | Project-relative path | Bytes, or a denial. |
| Session `Git` | Argument list | Git status and output, or a denial. |
| Session `Execute` | Executable, digest, optional network request | Status and body, or a denial. |
| Session `CollectPatch` | — | PatchBundle v1 of accepted writes. |
| Session `Cancel` / `Close` | — | Process-tree termination and cleanup. |
| Session `Audit` | — | The session's hash-chained events. |

`Open` copies or mounts the role projection into an ephemeral
workspace. The session must not reuse the control-plane export
directory as its writable root. `Close` removes that workspace.

Stable failure codes are `sandbox.denied`, `sandbox.capability`,
`sandbox.limit`, `sandbox.cancelled`, and `sandbox.fail_closed`.

`RunSandboxConformance` is the reusable suite. It returns a
`ConformanceReport` covering backend identity and version, policy
version and digest, executable digests, authorization decisions,
network rules, resource limits, cleanup results, audit head, and
whether autonomous execution remains enabled.

---

## Filesystem and Git isolation

The session root is one Worker role projection from #78. Worker A
may write `test` paths. Worker B may write `implementation` paths.
`shared` paths are visible and read-only. `unclassified`,
`integration-only`, and `attestation-only` paths are denied to
Workers. Host paths, peer Worker directories, the canonical
repository, and private Integration state are denied.

Git isolation reuses the projection properties: parentless synthetic
SHA-1 roots, independent object databases, no remotes, no Git
alternates, and no reflogs. `git cat-file` of a forbidden object
must fail. `fetch`, `pull`, `clone`, `ls-remote`, and `remote add`
are denied. Returned patches continue to satisfy PatchBundle v1 path
and integrity rules; Integration applies them only after the
Patch/Ownership Validator accepts the bundle.

---

## Network policy

The default policy is deny. A request is allowed only when the
executable name, executable digest, destination, protocol, method,
and L7 path all match one allowlisted rule.

The suite rejects:

- the same destination from another binary;
- an unapproved destination;
- a direct IP address;
- DNS tunneling;
- a redirect off the allowlisted path;
- a raw socket;
- an alternate port; and
- a TLS-bypass attempt.

The local adapter implements this with loopback in-process servers.
A hosted backend must produce the same denials under kernel
enforcement. Loopback stubs do not waive the policy.

---

## Credential isolation

No downstream credential appears in a sandbox file, environment
variable, process argument, log, response body, or agent-readable
proxy state. The #78 source-fixture token and the sandbox broker
token are both sentinels.

The boundary broker injects credentials only after authorization
and strips them before responses return. Workers receive no Git or
WMS mutation credentials. This is the same Bridge/Gate posture as
[Authentication and Credential Isolation][auth-isolation], applied at
the sandbox egress rather than at the WMS or Source Control Manager
write boundary.

---

## Lifecycle, cancellation, and cleanup

Cancellation, lease expiry, and Close terminate the full process
tree. Ephemeral repositories, processes, mounts, network policy, and
broker sessions are removed. The next sandbox cannot recover prior
state: it receives a fresh copy of the role projection, not the
previous workspace.

v1 does not require a numeric process-kill deadline beyond "prompt
termination observed by the suite." Hosted backends may document a
stricter deadline; they must still pass the suite's cancellation
check.

---

## Resource limits

v1 requires these limits, each recorded with the configured value,
enforcement result, and audit evidence:

| Limit | Enforcement |
| --- | --- |
| Wall-clock | Session deadline; further operations fail closed. |
| CPU | Accumulated execution time; further operations fail closed. |
| Memory | Oversize writes and allocations are denied. |
| Process-count | Additional processes are denied. |
| Writable disk | Additional bytes are denied. |
| Writable files | Additional creates are denied. |

A backend that cannot enforce a required limit reports the
corresponding capability as unsupported and disables autonomous
execution.

---

## Audit evidence

v1 requires tamper evidence. Each session records a SHA-256 hash
chain of private audit events. `EventDigest` covers the canonical
JSON payload with `EventDigest` itself empty.
`PreviousDigest` is the prior event's digest, or the SHA-256 of
empty input for the first event. Reordering or altering a field
invalidates the chain.

Every event carries contract version, backend name and version,
projection policy version and digest, work-item identifier, role,
trace identifier, decision, and optional executable digest, limit
name, configured limit, and cleanup result. Events cover open,
authorization decision, allowed request, denial, cancellation, and
cleanup. Credentials, remote URLs, peer source, raw Worker logs, and
raw Inspector findings stay out of the record.

Audit files are private Job Site evidence. They are not project
repository content and are not a WMS Adapter resource.

---

## Fail-closed behavior

If `Capabilities` omits a required name, marks one unsupported, or
`Open` cannot enforce the contract on the current platform, the
adapter returns `sandbox.fail_closed` and the Job Site disables
autonomous execution. The suite records that outcome; it does not
skip the failed control or retry with a weaker profile.

A later failed check in an already-open session also disables
autonomous execution for that report. Remaining denials still run;
the backend must not stop enforcing policy after the first denial.

---

## Deployment, security, and state

The contract is the same in every deployment mode. Only the backend
that implements it changes.

| Mode | Backend | Credential posture |
| --- | --- | --- |
| Single-player | Local process. The local test adapter proves the seam. A portable rootless-OCI/microVM profile is the production fallback when OpenShell is unavailable. | User-owned local credential; the sandbox receives no reusable downstream secret. |
| Multi-player | Hosted Job Site; Fullsend/OpenShell is the first backend. | OAuth 2.1 through the Bridge/Gate pattern. The sandbox still sees no downstream credential. |
| Web | Same hosted Job Site and sandbox backends as multi-player. | OAuth 2.1 through Bridge/Gate plus application authorization. |

Relevant persistent state remains owned by existing components:

| State | Owner | Sandbox contract |
| --- | --- | --- |
| Role projections and PatchBundle v1 | Job Site projection fixture (#78) | Open copies a projection; CollectPatch returns a bundle. |
| Work-item lifecycle | WMS Adapter | Trace identifier only; no lifecycle mutation. |
| Specification records | Git through `ears-manager` | Not parsed or edited through sandbox operations. |
| Canonical and Integration Git | Job Site private Integration / future Job Site face ([Q21](open-questions.md#q21-source-control-manager-job-site-face)) | Denied to Workers. |
| Sandbox audit chain | Job Site ephemeral store | Private, hash-chained, destroyed or archived with the session. |
| Web session state | Web Drafting Table deployment | Never a sandbox workspace or write authority. |
| Deployment-level project registry | Deployment operator | Selects the backend; cannot waive this contract. |

Security posture:

- Deny by default for filesystem, Git, and network.
- Credentials exist only in the broker after authorization.
- Isolation is structural. Prompts, harness sandboxes, and product
  names are not substitutes.
- Single-player mode is not allowed to skip limits, audit, or
  fail-closed behavior because it has one expected user.

---

## Local test adapter

The local test adapter is the deterministic fixture for this
contract. It:

- uses `BuildSourceFixture` and `Export` from #78;
- copies Worker A or Worker B into an ephemeral workspace;
- enforces projection `VisibleTo` / `WritableBy` on every path;
- runs Git only inside that workspace, with fetch and remote
  mutation denied;
- serves an in-process allowlisted registry and a credential broker
  on loopback;
- injects the sentinel `PROTOBOT-SANDBOX-BROKER-TOKEN` only after
  authorization and strips it from responses;
- enforces resource limits in-process;
- records the v1 hash chain; and
- deletes the workspace on Close.

It does not require a cluster, external network access, or real
downstream credentials. Its denials are the contract's observable
behavior. Kernel enforcement is a hosted-backend concern and is
validated when #172 maps this seam onto Fullsend/OpenShell.

`go test ./internal/jobsite` in `source-control-manager` runs the
suite against this adapter.

---

## Conformance test matrix

The suite is `RunSandboxConformance`. Downstream adapters call it
with their `Adapter` and a `ConformanceEnv`. Each check records an
identifier, name, pass/fail, and detail. A failed check sets
`autonomous_execution` to false.

| ID | Setup and command | Expected result |
| --- | --- | --- |
| `SB-FS-001` | Worker A writes `tests/canonical/app_test.go`. | Allowed. |
| `SB-FS-002` | Worker B writes `src/app.go`. | Allowed. |
| `SB-FS-003` | Worker A writes `src/app.go`. | Denied. |
| `SB-FS-004` | Worker B writes `tests/canonical/app_test.go`. | Denied. |
| `SB-FS-005` | Write `/etc/passwd`. | Denied. |
| `SB-FS-006` | Write a peer Worker absolute path. | Denied. |
| `SB-FS-007` | Write the canonical source root. | Denied. |
| `SB-FS-008` | Write the private Integration path. | Denied. |
| `SB-FS-009` | Read a `shared` path, then write it. | Read allowed; write denied. |
| `SB-FS-010` | Write an unclassified path. | Denied. |
| `SB-FS-011` | CollectPatch after an allowed Worker A write; apply to Integration. | PatchBundle v1 accepted. |
| `SB-GIT-001` | Worker A `git cat-file -e` of the implementation blob. | Non-zero or denied. |
| `SB-GIT-002` | Worker B `git cat-file -e` of the canonical test blob. | Non-zero or denied. |
| `SB-GIT-003` | `git remote` in a Worker session. | Empty. |
| `SB-GIT-004` | Alternates file under the Worker git dir. | Absent. |
| `SB-GIT-005` | Reflogs under the Worker git dir. | Absent. |
| `SB-GIT-006` | `git fetch` of Integration. | Denied or non-zero. |
| `SB-GIT-007` | `git cat-file -e` of a historical implementation blob. | Non-zero or denied. |
| `SB-NET-001` | `approved-fetch` to the allowlisted destination, method, and path. | HTTP 200; body has no credential. |
| `SB-NET-002` | Same destination from `curl`. | Denied. |
| `SB-NET-003` | Unapproved destination. | Denied. |
| `SB-NET-004` | Direct IP. | Denied. |
| `SB-NET-005` | DNS tunneling. | Denied. |
| `SB-NET-006` | Allowlisted path that redirects off the allowlist. | Denied. |
| `SB-NET-007` | Raw socket protocol. | Denied. |
| `SB-NET-008` | Alternate port. | Denied. |
| `SB-NET-009` | TLS bypass (`http` to an `https` rule). | Denied. |
| `SB-NET-010` | Unlisted method to the allowlisted destination. | Denied. |
| `SB-CRED-001` | Inspect sandbox environment. | No broker or fixture token. |
| `SB-CRED-002` | Walk sandbox files. | No broker token. |
| `SB-CRED-003` | Execute with a sentinel argument. | Sentinel absent from audit. |
| `SB-CRED-004` | Approved fetch response body. | Token stripped. |
| `SB-CRED-005` | Session audit records. | No sentinel. |
| `SB-CRED-006` | Unapproved binary, then approved fetch. | Deny, then inject-after-auth success. |
| `SB-CRED-007` | Walk sandbox files for the #78 fixture token. | Absent. |
| `SB-LIFE-001` | Start `sleep 30`; cancel the call context. | Process exits; `sandbox.cancelled`. |
| `SB-LIFE-002` | Open with a short lease; operate after expiry. | `sandbox.limit` or `sandbox.cancelled`. |
| `SB-LIFE-003` | Write, Close, `lstat` the workspace. | Workspace removed. |
| `SB-LIFE-004` | Write a scratch file, Close, Open a new session. | Prior file absent; roots differ. |
| `SB-LIM-001` | Short wall-clock; operate after expiry. | Limit recorded and enforced. |
| `SB-LIM-002` | Tiny CPU limit; execute twice. | Limit recorded and enforced. |
| `SB-LIM-003` | Tiny memory limit; oversize write. | Limit recorded and enforced. |
| `SB-LIM-004` | Process-count 0; `sleep`. | Limit recorded and enforced. |
| `SB-LIM-005` | Tiny writable-disk limit; oversize write. | Limit recorded and enforced. |
| `SB-LIM-006` | Writable-files 0; create a new file. | Limit recorded and enforced. |
| `SB-AUD-001` | After executions and denials. | Audit events exist. |
| `SB-AUD-002` | `VerifyAuditChain`. | Chain valid. |
| `SB-AUD-003` | Alter `Decision` on a copied event. | Chain verification fails. |
| `SB-AUD-004` | Inspect work-item and trace fields. | Match the Open request. |
| `SB-AUD-005` | Inspect backend, policy, and executable digest. | Present on accepted network events. |
| `SB-FC-001` | Wrap the adapter with a missing capability. | `sandbox.fail_closed`; autonomous execution disabled. |
| `SB-FC-002` | Report the platform unsupported. | `sandbox.fail_closed`; autonomous execution disabled. |
| `SB-FC-003` | After one denial, attempt another forbidden write. | Still denied. |

---

## Fullsend and OpenShell mapping

Fullsend and OpenShell are execution backends behind this seam.
They are not ProtoBot control-plane dependencies.

| Contract element | Fullsend / OpenShell mapping | Local test adapter |
| --- | --- | --- |
| Adapter `Open` | Fullsend pre-script / OpenShell session create, given only the role projection bundle. | Copy the #78 projection into a temp workspace. |
| Filesystem isolation | Landlock (or equivalent) confined to the projection. | `VisibleTo` / `WritableBy` on every path. |
| Git isolation | Same projection properties, enforced inside the agent process. | Git runs only in the copied workspace; fetch/remote denied. |
| Network policy | Per-binary OPA/Rego and L7 inspection via TLS interception. | In-process broker with an allowlisted loopback registry. |
| Credential broker | Inference/routing proxy injects tokens at the network boundary. | Loopback broker injects and strips a sentinel token. |
| Lifecycle | OpenShell session teardown; Fullsend post-script outside the sandbox. | Cancel/Close kill processes and `RemoveAll` the workspace. |
| Resource limits | cgroup / microVM limits. | In-process counters and deadlines. |
| Audit chain | Backend-specific event log mapped onto the v1 schema and hash chain. | JSON files plus in-memory hash chain. |
| Fail-closed | Failed OpenShell acceptance disables autonomous execution. | `FailClosedAdapter` and capability checks. |

Issue #172 owns runtime pinning, the concrete mapping, and a
conformance run against that mapping. This issue only names the
seam. A Fullsend or OpenShell product-name claim without a passing
`ConformanceReport` is not acceptance.

The portable rootless-OCI/microVM profile is the independent
fallback when OpenShell cannot satisfy a platform. It must pair
filesystem/process isolation with an external egress policy proxy
and credential broker, then pass this same suite.

---

## Out-of-scope decisions

| Topic | Owner or reason |
| --- | --- |
| Worker/Triage fixture loop and sanitized feedback routing | #69. |
| Concrete Fullsend/OpenShell adapter and runtime pinning | #172. |
| Corpus fixtures and conformance-runner integration | #217 and #219. |
| Job Site SCM face for Integration Git | [Q21](open-questions.md#q21-source-control-manager-job-site-face). |
| WMS lifecycle, fencing, and claims | Validation Rules and WMS contracts. |
| Drafting Table harness sandbox (OpenCode, Claude Code, Codex) | Agent Harness Adapter Contract. That sandbox is not this Job Site contract. |
| Numeric kill deadline tighter than suite observation | Hosted-backend documentation; v1 requires prompt termination. |
| Long-term audit archive and retention | Project policy in `.protobot/policy.yaml`; v1 stores a private chain per session. |

---

## Related Documents

- [Vision](../vision.md) — Purpose, users, outcomes, and prototype
  scope.
- [Architecture](../architecture.md) — External interfaces,
  persistent state, deployment modes, and environmental constraints.
- [Overview](overview.md) — Guiding principles, workflow, and
  platform, including the backend-neutral sandbox requirement.
- [System Components](components.md) — Job Site, Worker projections,
  sandbox summary, and credential isolation.
- [Validation Rules](validation-rules.md) — Lifecycle authorization
  the sandbox must not bypass.
- [Drafting Table WMS Integration](drafting-table-wms.md) —
  Backend-neutral WMS operations, disjoint from this execution seam.
- [Git and Project-Repository Integration](git-integration.md) —
  Canonical repository rules; Worker Git stays on role projections.
- [Source Control Manager](source-control-manager.md) — Drafting
  Table Git face; this contract does not extend it.
- [Agent Harness Adapter Contract](agent-harness/adapter-contract.md)
  — Drafting Table harness sandbox, a different boundary.
- [User Interaction Flow](user-interaction-flow.md) — Building and
  Inspecting phases that this sandbox hosts.
- [Drafting Table UX](drafting-table-ux.md) — Interactive surface;
  it does not open Job Site sandboxes.
- [`ears-manager` CLI Integration Contract](ears-manager-cli.md) —
  Specification reads the Job Site consumes outside the sandbox.
- [Open Design Questions](open-questions.md) — Q21, the Job Site
  face.
- [Related Work](related-work.md) — Fullsend, OpenShell, and the
  portable fallback behind this contract.

[auth-isolation]: components.md#authentication-and-credential-isolation
