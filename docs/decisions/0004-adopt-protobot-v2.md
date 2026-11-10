# ADR-0004: Adopt ProtoBot V2

> Status: **Accepted** — November 2026

**Contents:**

- [Context](#context)
- [Decision](#decision)
- [Consequences](#consequences)
  - [Hard ordering constraint](#hard-ordering-constraint)
  - [Removal sequence](#removal-sequence)
  - [Artifact supersession](#artifact-supersession)
  - [Risks](#risks)
- [Related Documents](#related-documents)

## Context

Pull request #331 merged `ProtoBotV2.md` into `docs/architecture/`. That
document states of itself that merging it does not adopt V2, supersede V1,
or authorize changes to current tooling, permissions, or workflow.
GitHub issue #346 tracks the discussion.

The maintainer decided to build and evaluate ProtoBot V2. That decision
needs a durable record before any restructuring work starts.

## Decision

ProtoBot adopts V2 as the target design and builds it across four milestones:

- M1: Adoption and validator
- M2: Repository template
- M3: Coordinator composition
- M4: Pilot and evaluation

These four milestones replace the existing `Drafting Table MVP` and `Fullsend
Job Site MVP` milestones, which close when the plan is applied.

The V2 validator is a new Go module. The project deletes `ears-manager` rather
than reworking it.

Removal of V1 components ships as a staged sequence of small pull requests (R1
through R7). The Consequences section lists these pull requests.

The project keeps these V1 components:

- The Drafting Table and its tool-call guard
- `eval/eliciting-requirements/`
- `docs/decisions/`
- `docs/related-work.md`

The project reduces `docs/architecture/agent-harness/` to the OpenCode binding
only.

`ProtoBotV2.md` will become an appendix in the root `Architecture.md`.
The project deletes the design document from `docs/architecture/` only after
that appendix lands.

This decision extends the path taxonomy in Appendix A of `ProtoBotV2.md`.
`docs/**` is Process-class, not Implementation-class. Background documents,
such as `docs/related-work.md` and the architecture decision records, remain
human-reviewed and may accompany either class.

V1 specifications and accepted ADRs remain the current contracts until the
removal pull request that supersedes each one merges.

## Consequences

### Hard ordering constraint

The new validator must pass on `spec/` and run in CI before developers delete
`ears-manager` or `.protobot/`.

Without `.protobot/project.yaml`, `ears-manager check` fails with
`project.not_initialized`. In addition, `artifact.unregistered` prevents the
removal of a registered artifact through any registry edit. CI requires the new
validator to gate pull requests before developers delete V1 verification
tooling.

R2 follows R1. The registered artifacts cannot move while the artifact
registry exists. R1 also precedes R3, so no surviving instruction points
at deleted tooling.

### Removal sequence

| Pull Request | Target Components | Description |
| --- | --- | --- |
| R1 | `ears-manager` and `.protobot/` | Delete `ears-manager`, `.protobot/`, and V1 stores |
| R2 | `Vision.md` and `Architecture.md` | Move `docs/vision.md` and `docs/architecture.md` to root |
| R3 | `AGENTS.md` | Rewrite `AGENTS.md` for the V2 workflow and taxonomy |
| R4 | `wms/` | Delete the Work Management Store subsystem |
| R5 | `source-control-manager/` | Delete the Source Control Manager subsystem |
| R6 | `docs/architecture/` | Retire superseded V1 architecture specification files |
| R7 | `eval/corpus/` | Delete the V1 evaluation corpus |

### Artifact supersession

| V1 Artifact | Removal PR | Replacement |
| --- | --- | --- |
| ADR-0003 (`.protobot/` store layout) | R1 | Root `spec/` directory layout |
| Artifact registry (`.protobot/project.yaml`) | R1 | Plain repository paths and Git branch protection |
| Change-set stores (`.protobot/change-sets/`) | R1 | Git branches and specification pull requests |
| Requirements store (`.protobot/requirements/`) | R1 | `spec/requirements/` |
| Interfaces store (`.protobot/interfaces/`) | R1 | `spec/interfaces/` |
| `docs/vision.md` | R2 | Root `Vision.md` |
| `docs/architecture.md` | R2 | Root `Architecture.md` |
| `wms/` module | R4 | GitHub Issues and repository-owned coordinator |
| `source-control-manager/` module | R5 | Native Git and GitHub Actions |
| V1 specification files in `docs/architecture/` | R6 | Root `Architecture.md` with V2 appendix |
| `eval/corpus/` | R7 | Deferred beyond the V2 MVP; see issue #346. |

### Risks

The planned work is large. The four milestones span an estimated 13 to 20
weeks. Several deliverables depend on unresolved design details from
`ProtoBotV2.md`:

- Open Question 1 (Test runner and evidence reporting): The team must resolve
  how the evidence report represents incomplete or failing evidence. The team
  must also standardize tag extraction across test runners.
- Open Question 6 (Planning model): The coordinator planning model must balance
  per-source-issue planning against whole-queue reconciliation.
- Open Question 7 (Scheduler shape): The project must evaluate whether
  scheduled coordinator polling justifies its inference cost over event-driven
  execution.

## Related Documents

- [ProtoBot V2 Design Proposal](../architecture/ProtoBotV2.md)
- [ADR-0001: One-File-Per-Record YAML Storage Format](0001-requirements-storage-format.md)
- [ADR-0002: EARS Specification Record Schema](0002-ears-specification-record-schema.md)
- [ADR-0003: EARS Manager Storage Layout and Schema Version Keys](0003-ears-manager-storage-layout.md)
- [GitHub Issue #346: Propose clean-slate ProtoBot V2 design](https://github.com/redhat-et/ProtoBot/issues/346)
