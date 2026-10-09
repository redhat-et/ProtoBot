# ProtoBot: Evaluation Corpus Schema and Result Contract

> Interface contract — issue #215 — September 2026
> Document revision: `eval-corpus-doc/v1` (document revision,
> distinct from fixture `schema_version` and `corpus_revision`)

Defines the versioned fixture and result records shared by ProtoBot
requirements evaluation and Job Site evaluation.

**Contents:**

- [Purpose and scope](#purpose-and-scope)
- [Relationship to sibling contracts](#relationship-to-sibling-contracts)
- [Vocabulary](#vocabulary)
- [Fixture contract](#fixture-contract)
- [Generator-visible and evaluator-only boundary](#generator-visible-and-evaluator-only-boundary)
- [Result contract](#result-contract)
- [Schema versioning](#schema-versioning)
- [Local execution](#local-execution)
- [Deployment, security, and state](#deployment-security-and-state)
- [Components and interfaces](#components-and-interfaces)
- [Examples](#examples)
- [Validation](#validation)
- [Out-of-scope decisions](#out-of-scope-decisions)
- [Related Documents](#related-documents)

---

## Purpose and scope

This contract answers issue #215: what fields does a ProtoBot
evaluation fixture carry, which of those fields a Worker or
elicitation skill may see, and what a result record must retain so
runs can be reproduced and compared.

The corpus is meant to answer two questions separately:

1. Did the Specification Toolkit turn a free-form project
   description into a complete, correct, implementable Schematic?
2. Did the Job Site turn a known-good Schematic into a behaviorally
   conformant prototype?

An end-to-end pipeline may run both stages. Its scores remain
attributable to requirements quality and Job Site conformance
separately. An aggregate end-to-end pass is not a substitute for
either stage.

This contract specifies:

- fixture fields for a project description, golden Vision and
  Architecture artifacts, golden EARS requirements,
  interface/runtime metadata, reference behavior checks,
  holdout-asset references, and provenance;
- the boundary between generator-visible inputs and evaluator-only
  holdout assets;
- result records for reproducible inputs, model/settings, component
  versions, traces, scores, failures, and corpus revisions;
- schema evolution;
- local validation with no hosted cluster or external service
  credentials.

Live evaluation runners, reviewed golden fixtures, and executable
Job Site holdout tests are follow-on work (#216, #217, and the
parent corpus issue #135). This revision ships illustrative
examples that exercise the schemas.

This contract does not replace the eliciting-requirements skill
corpus under `eval/eliciting-requirements/` (#63). That corpus
judges the host-independent elicitation skill. This corpus judges
project-level Sketch, Schematic, and Job Site outcomes.

## Relationship to sibling contracts

- [System Components — Evaluability](components.md#evaluability)
  requires defined inputs, measurable outputs, and isolation
  boundaries. This document is that fixture and result contract.
- [Overview](overview.md) supplies the Sketch, Schematic, Job Site,
  Worker, and interface-type vocabulary used here.
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md)
  supplies EARS pattern types, interface types, applicability,
  verification modes, provenance values, and relationship types.
- [`ears-manager` CLI](ears-manager-cli.md) remains the write gate
  for project specification stores. Corpus fixtures are evaluation
  records, not `.protobot/` specification files.
- [Worker repository projections](components.md#worker-repository-projections-decided)
  keep canonical tests and implementation apart. Holdout assets are
  a further evaluator-only class: they are never mounted into a
  Worker projection.
- [Validation Rules](validation-rules.md), [Drafting Table
  WMS](drafting-table-wms.md), [Git
  integration](git-integration.md), and the [Source Control
  Manager](source-control-manager.md) are optional in a local
  schema-validation run. A later Job Site runner may use the
  in-memory WMS adapter, the repository fixture, and the
  deterministic evaluator without a Git host or OAuth token.
- [Related Work](related-work.md#eval-hub-and-agent-eval-harness)
  records Eval Hub and Agent Eval Harness. This contract runs
  locally without those hosted services.

### Non-goals

This contract does not:

- author reviewed CLI, API, or web golden specifications (#216);
- author executable Job Site holdout tests (#217);
- run a live elicitation skill, Worker, or Inspector;
- replace `eval/eliciting-requirements/`;
- require Eval Hub, Agent Eval Harness, a hosted cluster, or
  external service credentials;
- treat exact generated source comparison as the success
  criterion;
- store evaluation results in a project WMS, specification store,
  or evidence store.

## Vocabulary

Terms match the Overview and ADR-0002. An undeclared alias is a
defect.

| Term | Meaning in this contract |
| --- | --- |
| **Sketch** | Golden Vision plus Architecture in the fixture. |
| **Schematic** | Golden EARS requirements plus registered interfaces. |
| **elicitation-input** | Generator-visible projection for requirements evaluation: the free-form project description and case identity. |
| **job-site-input** | Worker-visible projection for Job Site evaluation: the golden Sketch, Schematic, interface/runtime metadata, and worker-visible reference behavior checks. |
| **holdout** | Evaluator-only behavior-check assets under `holdout/`. A Worker projection must not mount them. |
| **Worker** | Job Site test generator (Worker A) or implementation generator (Worker B). |
| **Inspector** | Job Site review agent. Inspectors are under test; they are not the holdout oracle. |
| **corpus_revision** | Identifier for a reviewed content revision of the corpus. Independent of `schema_version`. |
| **schema_version** | Integer version of this fixture or result schema. Version 1 is this revision. |

Interface types, EARS pattern types, verification modes, and
relationship types are the ADR-0002 enums. First-revision example
archetypes are `cli`, `network-service`, and `web-gui`.

## Fixture contract

Executable schemas live under
[`eval/corpus/schema/`](../../eval/corpus/schema/). A fixture is a
YAML document that validates against `fixture.schema.json`.

Required fixture fields:

| Field | Role |
| --- | --- |
| `schema_version` | Integer. Version 1 only in this revision. |
| `fixture_id` | Stable lowercase-hyphenated case id. |
| `corpus_revision` | Content revision of the corpus. |
| `archetype` | Primary interface type: `cli`, `network-service`, or `web-gui`. |
| `title` | Short case title. |
| `project_description` | Free-form elicitation-input. |
| `visible` | Golden Sketch, Schematic, interface/runtime metadata, and worker-visible reference behavior checks. |
| `holdout` | Holdout-asset references. The assets themselves live under `holdout/`. |
| `provenance` | Authors, reviewers, rationale, and known ambiguities. |

`visible.vision` records purpose, intended users, and desired
outcomes. `visible.architecture` records external interfaces,
persistent state, and environmental constraints. Those three
Architecture headings are the ones
[architecture.md](../architecture.md) uses for ProtoBot itself.

`visible.requirements` use ADR-0002 requirement fields: `id`,
`type`, `text`, `applies_to`, `verification`, `provenance`, and
`created`. Optional `relationships` use `depends-on`,
`conflicts-with`, `supersedes`, and `related-to`. Optional
`status` is `active` or `retired`.

`visible.interface_runtime` binds each registered interface to a
local runtime (`local-process`, `local-http`, or `local-web`),
`credentials: none`, and `hosted_cluster: false`. Runtime metadata
does not name an implementation language or framework. That
omission is deliberate: requirements evaluation must not reward
implementation leakage.

`visible.reference_behavior_checks` are worker-visible. Each check
names requirement IDs, an interface ID, setup, and expected
evidence. These checks may later become Worker A canonical tests.
They are not holdouts.

`holdout.assets[]` are references, not inline expected values. Each
reference has a `holdout/` relative path, a `sha256:` digest, and
the requirement and interface IDs it covers. The referenced file
validates against `holdout-asset.schema.json` and sets
`visibility: evaluator-only`.

A fixture may have an empty `holdout.assets` list so requirements
fixtures (#216) can omit Job Site holdouts. A fixture may also omit
worker-visible checks. Missing Vision, Architecture, requirements,
or interface/runtime metadata is invalid.

## Generator-visible and evaluator-only boundary

The full fixture is evaluator-owned. Runners must project it before
any skill or Worker sees the case.

| Projection | Schema | Included | Forbidden |
| --- | --- | --- | --- |
| elicitation-input | `elicitation-input.schema.json` | `project_description` and case identity | `visible`, `holdout`, `provenance` |
| job-site-input | `job-site-input.schema.json` | golden Sketch, Schematic, interface/runtime, worker-visible checks | `holdout`, `project_description` |
| evaluator | `fixture.schema.json` plus holdout files | everything | — |

Job Site evaluation uses the golden Schematic as the Worker-visible
baseline, not a model-generated Schematic. End-to-end evaluation
starts from elicitation-input and still scores the Job Site stage
against holdouts the Workers never saw.

Holdout isolation is structural:

1. Holdout files live only under `holdout/` in the fixture
   directory.
2. A Worker file mount excludes every path whose parts include
   `holdout`.
3. `job-site-input` documents have no `holdout` key.
4. `elicitation-input` documents have no golden artifacts, no
   holdouts, and no provenance.

A later Job Site sandbox must enforce the same path isolation the
validator checks. Prompting a Worker not to read holdouts is not
sufficient ([Overview — enforce constraints
structurally](overview.md#enforce-constraints-structurally-not-through-trust)).

## Result contract

A result is a YAML document that validates against
`result.schema.json`.

Required result fields:

| Field | Role |
| --- | --- |
| `schema_version` | Integer. Version 1 only in this revision. |
| `result_id` | Stable id for this record. |
| `fixture_id` / `corpus_revision` | Case and corpus content revision. |
| `run_id` / `recorded_at` | Run identity and timestamp. |
| `stage` | `requirements`, `job-site`, or `end-to-end`. |
| `reproducibility.inputs` | Fixture id, corpus revision, fixture digest, and projection. |
| `reproducibility.environment` | Mode, cluster/credential flags, models, component versions. |
| `scores` | Stage-scoped score objects. |
| `failures` / `critical_failures` | Per-case evidence. Aggregate scores must not hide a critical failure. |
| `traces` | Optional URIs for elicitation, Job Site, end-to-end, or judge traces. |

`stage` controls which score objects may appear:

- `requirements` — only `scores.requirements`.
- `job-site` — only `scores.job_site`.
- `end-to-end` — `scores.requirements`, `scores.job_site`, and
  `scores.end_to_end`. `end_to_end.attribution` restates the two
  stage pass flags and must match them.

Requirements scores record coverage, applicability, and
implementability in \[0, 1\], plus a per-requirement outcome:
`covered`, `missing`, `unsupported-addition`, `false-readiness`, or
`implementation-leakage`. Comparison is by behavior and coverage,
not exact wording.

Job Site scores count worker-visible checks and holdout checks
separately. A visible-check pass cannot conceal a holdout failure.

Environment `components[]` name ProtoBot components with a
revision: Drafting Table, Specification Toolkit, Kits,
`ears-manager`, WMS Adapter, Source Control Manager, Validation
Rules, Job Site, and the eval runner. Models record role
(`elicitation`, `worker-a`, `worker-b`, `inspector`, `judge`),
name, and settings.

Local example results set `mode: local`, `hosted_cluster: false`,
and `external_service_credentials: false`. Hosted runs may set
those flags true; the schema records them so comparisons stay
honest.

## Schema versioning

`schema_version` is a monotonically increasing integer. Version 1
is this revision. Additive fields and breaking changes both
increment the version. The validator refuses a document newer than
the version it implements.

`corpus_revision` versions content, not the schema. Changing an
example or a later reviewed fixture without changing field names
keeps `schema_version: 1` and records a new `corpus_revision`.

Illustrative examples in this revision are not a calibrated
baseline. Promoting a corpus revision as a trusted baseline remains
blocked by #63, #69, and #79.

## Local execution

Schema validation is a local, deterministic program:

```text
python3 eval/corpus/validate.py
python3 eval/corpus/test_contract.py
```

The validator loads JSON Schema from disk, hashes holdout bytes,
projects elicitation-input and job-site-input, and rejects invalid
documents. It does not call a model, a Git host, a WMS backend, or
a cloud API.

Example fixtures declare local runtimes, `credentials: none`, and
`hosted_cluster: false`. They are sufficient to prove the contract
without a hosted cluster or external service credentials.

A later live runner must keep that local path: single-player mode
with the in-memory WMS adapter, the repository fixture, and the
Validation Rules evaluator. OAuth, Bridge/Gate tokens, and a
hosted Drafting Table session are not required to score a fixture.

## Deployment, security, and state

The fixture and result schemas are the same in every deployment
mode. Only the runner's environment block changes.

| Mode | Schema validation | Live run (future) |
| --- | --- | --- |
| Single-player | Local files, no network. | Local process, in-memory WMS, no cluster. |
| Multi-player | Same local validator. | May record Git-host and WMS backend revisions; still must isolate holdouts. |
| Web | Same local validator. | Must not put holdouts in Web session state or Worker mounts. |

Security posture:

- Holdout isolation is a path and projection rule, not a prompt.
- Fixtures and example results contain no tokens, passwords, or
  private keys.
- Credential isolation (Bridge/Gate) applies to a hosted live
  runner; schema validation never handles credentials.
- Worker A still must not see implementation source, and Worker B
  still must not see canonical tests. Holdouts are excluded from
  both.

Persistent state:

The Architecture enumerates eight project stores. The evaluation
corpus is not one of them. Fixtures and results live in the
ProtoBot repository under `eval/corpus/`. They are not written
through `ears-manager`, the WMS Adapter, the claim coordinator, the
Gate, the evidence store, or the deployment-level registry.

A live Job Site run of a fixture may still use those stores inside
the sandbox under test. The evaluation record of that run remains a
corpus result, not project conformance evidence.

## Components and interfaces

Every component in [components.md](components.md) has a role in
this contract or an explicit non-role:

| Component | Role |
| --- | --- |
| Specification Toolkit | System under test for requirements evaluation. Receives elicitation-input only. |
| Job Site | System under test for conformance evaluation. Workers receive job-site-input only. |
| Worker A / Worker B | Consume job-site-input. Must not see `holdout/`. |
| Inspector | Job Site sub-component under test. Not the holdout oracle. |
| Drafting Table | Not required for schema validation or headless eval. A live end-to-end runner may host elicitation here. |
| `ears-manager` | Golden requirements reuse ADR-0002 fields. A live runner may call `ears-manager check` on materialized records. Corpus YAML is not a specification store. |
| WMS Adapter | Not required for schema validation. A live Job Site runner may use the in-memory adapter. |
| Source Control Manager | Not required for schema validation. A live runner may use the repository fixture without Git-host credentials. |
| Validation Rules | Not required for schema validation. A live runner may use the deterministic evaluator. |
| Kits | Out of scope for corpus revision `v1`. |

External interfaces from the Architecture inventory are not
invoked by schema validation. Example fixtures use the
interface-type taxonomy (`cli`, `network-service`, `web-gui`) as
case archetypes, not as live ProtoBot interfaces.

Environmental constraints that apply:

- Evaluability from day one: known inputs, measurable outputs,
  traces.
- Single-player mode must work without an OpenShift cluster.
- Credential isolation: evaluation records must not require real
  downstream secrets.
- Observable behavior is all that matters: holdout and visible
  checks assert interface behavior, not implementation structure.

## Examples

Illustrative fixtures:

| Directory | Archetype | Interface |
| --- | --- | --- |
| `eval/corpus/examples/cli-sync/` | `cli` | `cli-sync` command-line tool |
| `eval/corpus/examples/api-registry/` | `network-service` | local HTTP record registry |
| `eval/corpus/examples/web-status/` | `web-gui` | local status page |

Each fixture directory contains `fixture.yaml` and
`holdout/checks.yaml`. Sample results under
`eval/corpus/examples/results/` cover a requirements run, a Job
Site run, and an end-to-end run of `cli-sync`.

These examples exist to validate the schema. They are not reviewed
golden specifications and not a calibrated baseline.

## Validation

`eval/corpus/validate.py` is the deterministic checker. It:

- validates fixtures, holdout assets, projections, and results
  against the version-1 schemas;
- verifies holdout digests and relative `holdout/` paths;
- rejects unknown interface or requirement references;
- rejects elicitation-input that contains golden artifacts or
  holdouts;
- rejects a Worker file set that includes `holdout/`;
- rejects a requirements result that carries Job Site scores, a
  Job Site result that carries requirements scores, and an
  end-to-end result that omits either stage;
- rejects a local result that claims a hosted cluster or external
  service credentials;
- fails deterministically on missing Vision, Architecture,
  requirements, or interface metadata.

`eval/corpus/test_contract.py` covers those cases.

## Out-of-scope decisions

| Topic | Owner or reason |
| --- | --- |
| Reviewed CLI, API, and web golden fixtures | #216 |
| Executable Job Site holdout tests | #217 |
| Live elicitation, Job Site, and end-to-end runners | #135, blocked by #63, #69, #79 |
| Calibrated semantic judges and trusted baselines | #63 |
| Deterministic Worker/Triage fixture execution | #69 |
| Executable Job Site sandbox contract | #79 |
| Eval Hub / Agent Eval Harness hosting | Optional later; not required locally |
| Kit evaluation | First corpus revision does not cover Kits |
| Trace storage format beyond URI + digest | Open under Evaluability |

## Related Documents

- [Vision](../vision.md) — Purpose, users, outcomes, and prototype
  scope.
- [Architecture](../architecture.md) — External interfaces,
  persistent state, deployment modes, and environmental
  constraints.
- [Overview](overview.md) — Guiding principles, Sketch/Schematic
  vocabulary, and single-player mode.
- [System Components](components.md) — Component roles, Worker
  projections, and Evaluability.
- [`ears-manager` CLI Integration Contract](ears-manager-cli.md) —
  Specification-store writes this corpus does not perform.
- [Validation Rules](validation-rules.md) — Lifecycle evaluator a
  live Job Site runner may use locally.
- [Drafting Table WMS Integration](drafting-table-wms.md) —
  In-memory adapter path for local Job Site evaluation.
- [Git and Project-Repository Integration](git-integration.md) —
  Repository fixture without Git-host credentials.
- [Source Control Manager](source-control-manager.md) — Git and Git
  host boundary a live runner may omit for schema validation.
- [User Interaction Flow](user-interaction-flow.md) — Sketching,
  Dimensioning, Building, and Inspecting stages this corpus
  scores.
- [Drafting Table UX](drafting-table-ux.md) — Interactive
  elicitation surface not required for headless eval.
- [Agent Harness Adapter Contract](agent-harness/adapter-contract.md)
  — Harness traces a live elicitation runner may record.
- [Open Design Questions](open-questions.md) — Remaining runner
  and baseline questions.
- [Related Work](related-work.md) — Eval Hub, Agent Eval Harness,
  and holdout-oracle lessons.
- [ADR-0002](../decisions/0002-ears-specification-record-schema.md)
  — Requirement, interface, verification, and relationship enums.
