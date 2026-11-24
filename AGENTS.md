# AGENTS

- Pre-commit tests must pass before committing. Offline, use
  `python scripts/lint.py` for `pre-commit run`; pre-install tools and
  populate the hook cache with `pre-commit install-hooks`.
  Key flags: `--all-files` (tracked files), `--files FILE [FILE ...]`
  (specific files), `--check-parity` (validate config and offline hook
  cache), `--online-if-ci` (allow network when CI=true).
  Affected Go modules must pass `gofmt` and `go vet` before committing.
- The upstream repository is `redhat-et/protobot`. Ensure pull requests
  target this repository.
- Agent skills live in `.agents/skills/`; `.claude/skills` symlinks
  there. Do not add skills under `.claude/` directly.

## Workflow files and CI boundaries

Agents must follow these rules:

1. **Workflow file restrictions.** Fullsend agents running via
   GitHub workflows must not create or modify files under
   `.github/workflows/`. Local coding agents may create or modify
   them, except as restricted by Rule 2, when the change requires
   it and the user authorizes it.

2. **Defer CI integration for new modules.** When introducing a new
   Go module, package, or tool, implement code, local test suites,
   and module-local configs (`go.mod`, `.golangci.yml`), but do not
   create or modify [`.github/workflows/ci-workflow.yaml`][ci-workflow]
   — that is reserved for manual human pull requests.

[ci-workflow]: .github/workflows/ci-workflow.yaml

## Context budget and prompt efficiency

`AGENTS.md` enters every dispatched prompt. The `context-budget` rule's
`agents-md` warn limit in `.skillsaw.yaml` triggers the finding;
[`scripts/check_baseline_ratchet.py`](scripts/check_baseline_ratchet.py)
rejects a `.skillsaw-baseline.json` ceiling increase.

1. **Respect the prompt context budget.** Do not raise a
   `.skillsaw-baseline.json` ceiling to fit new prose. After a genuine
   shrink, lower the `agents-md` warn limit in `.skillsaw.yaml`, then run
   `skillsaw baseline` to ratchet the ceiling down
   (`.skillsaw.yaml:27`).
2. **Consolidate or extract when adding prose.** Offset new rules or
   explanations by consolidating prose or extracting detail into an
   on-demand `.agents/skills/` skill.
3. **Review agents must recognize budget consolidation.** Treat prose
   consolidation offsetting an `AGENTS.md` addition as budget compliance,
   not scope creep, if it omits no governance requirement, defined
   keyword, or diagnostic name and introduces no undeclared alias or
   terminology drift. It does not exempt entries from sibling-entry or
   intra-entry terminology rules.

## Rules for creating or modifying sibling entries

A governed scope defines applicability; its list or governed collection
defines the sibling set. A **module README** is the top-level
`README.md` of a directory with a `go.mod`.

When creating or modifying any numbered rule in `AGENTS.md`, any
specification document under `docs/`, any skill file under
`.agents/skills/`, any module README, or any entry in a future governed
scope, agents must follow these rules. Review agents must enforce these
rules reflexively on PRs that create, modify, or consolidate entries
within `AGENTS.md` itself.

1. **Read all sibling entries first.** Read every other entry in the
   list or governed collection before drafting or revising an entry.

2. **Reuse established terminology.** Match sibling entries' terms for
   the same concepts. An undeclared alias for a term already used in the
   same list or governed collection is a defect.

3. **Review agents must check sibling-entry terminology.** When reviewing
   a PR that creates or modifies an entry in a governed scope, verify
   that the new or changed entry reuses established sibling terminology
   rather than inventing an alias. In a module README, also check for
   drift between neighboring paragraphs and between a CLI flag and its
   description. Findings should include terminology drift within the
   same list or governed collection, not only specification-hierarchy
   keyword checks. When `AGENTS.md` itself is modified, verify that prose
   consolidations do not introduce undeclared aliases for established
   terms or omit grammatical referents in requirement clauses.

4. **Match intra-entry restatement terminology.** When an entry
   restates a requirement's cases in a separate enforcement or
   consequence clause, both statements must use identical terminology.
   An undeclared alias or omitted term is a defect. Review agents must
   check this restatement. Findings should include intra-entry
   terminology drift, not only sibling-entry terminology matching.

### Examples of sibling entries

Numbered rules in `AGENTS.md` (other rules in the list); specification
documents under `docs/` (other governed Markdown files); skill files under
`.agents/skills/` (other skill files); module READMEs (other module READMEs
and `docs/` specification documents describing the module).

## Specification document hierarchy

Every `docs/` Markdown file is a governed hierarchy member; the prefix,
not the list, defines membership.

- `docs/vision.md` — registered Vision artifact (purpose, users,
  outcomes).
- `docs/architecture.md` — registered Architecture artifact (external
  interfaces, persistent state, environmental constraints).
- `docs/architecture/overview.md` — guiding principles, EARS
  format, workflow, and platform.
- `docs/architecture/components.md` — component architecture,
  interfaces, and cross-cutting concerns.
- [`docs/architecture/git-integration.md`][git-integration-doc] — governed Git
  and project-repository integration.
- [`docs/architecture/validation-rules.md`][validation-rules-doc] — lifecycle
  validation, authorization, transition, and rejection contract.
- [`docs/architecture/drafting-table-wms.md`][drafting-table-wms-doc] —
  backend-neutral Drafting Table WMS operations and fixture.
- [`docs/architecture/jobsite-sandbox.md`][jobsite-sandbox-doc] —
  Job Site sandbox contract and local conformance suite.
- `docs/architecture/user-interaction-flow.md` — phase details,
  sequence diagrams, and testing strategy.
- `docs/architecture/drafting-table-ux.md` — stable interaction
  contract for the first local Drafting Table.
- [`docs/architecture/agent-harness/`][agent-harness-doc] — Agent
  Harness Adapter Contract (`adapter-contract.md`) and bindings
  (`opencode.md`, `claude-code.md`, `codex.md`).
- [`docs/architecture/related-work.md`][related-work-doc] —
  internal and external projects informing the design.
- [`docs/architecture/evaluation-corpus.md`][evaluation-corpus-doc] —
  versioned evaluation fixture schema and result contract.
- `docs/architecture/open-questions.md` — unresolved design
  questions across all areas.
- `docs/decisions/` — architecture decision records.

[git-integration-doc]: docs/architecture/git-integration.md
[validation-rules-doc]: docs/architecture/validation-rules.md
[agent-harness-doc]: docs/architecture/agent-harness/
[ears-and-review-doc]: .agents/skills/eliciting-requirements/references/ears-and-review.md
[drafting-table-wms-doc]: docs/architecture/drafting-table-wms.md
[related-work-doc]: docs/architecture/related-work.md
[evaluation-corpus-doc]: docs/architecture/evaluation-corpus.md
[jobsite-sandbox-doc]: docs/architecture/jobsite-sandbox.md

### Rules for creating or modifying specification documents

When creating or modifying documents under `docs/`, agents
must follow these rules:

1. **Account for all components, interfaces, and constraints.**
   New or revised specification documents must account for every
   component, interface, and constraint enumerated in
   `docs/architecture/components.md` and
   `docs/architecture/overview.md`, and must address, not
   silently omit, those relevant to the document's scope.

2. **Verify deployment topology, security posture, and
   persistent state coverage.** Cross-check against
   `docs/architecture/overview.md` and `docs/architecture.md`:
   deployment topology (single-player, multi-player, web),
   security posture (credential isolation, sandbox
   constraints), and persistent state (all stores in the
   Architecture) where relevant.

3. **Review agents must check cross-document coverage.** When
   reviewing a PR that creates or modifies a specification
   document, verify that the document accounts for components,
   interfaces, and constraints from `components.md` and
   `overview.md`. Findings should include coverage gaps, not
   only formatting and cross-reference matching. Do not raise
   a hierarchy-membership finding for an unlisted `docs/` file.
   Checks must go beyond link freshness and heading alignment:
   register new capabilities where `components.md` enumerates
   them, avoid conflicting with principles in `overview.md`
   or `components.md` (e.g., the harness-agnostic
   Toolkit principle), and match defined
   ProtoBot keywords and relationship terms; an undeclared
   alias for a defined keyword is a defect.

4. **Review agents must also check for staleness introduced
   elsewhere.** When a PR changes a contract, lifecycle, or
   behavior description in a specification document, search
   every other governed Markdown document under `docs/` for
   prose describing that behavior and flag any stale.

5. **Update registered specification artifacts through
   `ears-manager artifact put`.** Registered specification artifacts
   (`docs/vision.md`, `docs/architecture.md`, and any other artifact
   registered in `.protobot/project.yaml`) and
   `.protobot/project.yaml` must never be edited by hand.
   When a PR creates or modifies a registered specification artifact, or
   when Rule 4 identifies staleness in such an artifact, apply the update
   exclusively via
   `ears-manager artifact put` within an active change set. Run
   formatters and linters before `ears-manager artifact put`; verify
   with `ears-manager check` before committing.

6. **Review agents must check registered specification artifact and
   project configuration updates.** When a PR creates or modifies a
   registered specification artifact, when Rule 4 identifies staleness
   in such an artifact, or when a PR modifies
   [.protobot/project.yaml](.protobot/project.yaml), inspect the
   affected registered specification artifact or project configuration for
   hand edits under Rule 5.
   On the recorded PR head checkout or worktree, run
   `ears-manager check --base-ref` with the full SHA from
   `git merge-base <default-branch-ref> HEAD`; a base-branch check is not
   evidence. Require it to pass
   without `artifact.digest_mismatch`, `artifact.operation_missing`, or
   `artifact.unregistered`. Each registered artifact path or
   canonical-content delta must match the `artifact_id` of a proposed
   change set's `artifact_operations[]` entry. The check rejects
   unregistration of an artifact-registry entry present on the comparison
   base (the default-branch tree, or the commit named by `--base-ref`). It
   confirms manifest association only, not that `ears-manager artifact
   put` was invoked; metadata-only (`kind`, `owner`, `validator`) changes
   require no artifact operation. Reviewers must flag identifiable hand
   edits, not claim CLI
   provenance from an operation record. Findings should include each
   listed diagnostic and any hand edit in that registered specification
   artifact or
   [.protobot/project.yaml](.protobot/project.yaml); Rule 4 staleness
   findings must prescribe `ears-manager artifact put`, not a hand edit.

7. **Preserve accepted architecture decision records.**
   Accepted architecture decision records (`docs/decisions/`) are
   immutable historical records; accepted decision text is never
   rewritten or replaced in place. When design evolution amends an accepted
   architecture decision record, append a dated amendment block
   (`**[Amended Month Year]**`) with the change, its rationale, and
   references to updated architecture documents. Review agents must
   verify accepted decision text is never rewritten or replaced in
   place, and that amendments append the dated amendment block
   (`**[Amended Month Year]**`) with the change, its rationale, and
   references to updated architecture documents.

## Agent skills

### Rules for creating or modifying skill files

When creating or modifying skill files under `.agents/skills/`,
agents must follow these rules:

1. **Follow the formatting conventions observed in sibling files.**
   Match sibling skills' backtick-quoting of refs, command syntax, and
   structural patterns. Quote git refs as `` `upstream/main` `` rather
   than unquoted; inconsistent sibling formatting is a defect.

2. **Match structural conventions, not behavioral fields.**
   Behavioral fields like dispatch parameters must follow the skill's own
   requirements, not copied sibling configuration, which can produce
   incorrect dispatch or workflow behavior.

3. **Link a GitHub issue or justify why none is needed.**
   When reversing or correcting a business rule, link an issue describing
   the incorrect prior behavior and impact, or justify why none was filed
   in the PR description with that substance; comment acknowledgments do
   not qualify. Omitting both or a vacuous justification is a defect.
   Gating-condition or gated-action diffs (e.g., status moves) are in
   scope; other edits are not. Scope is determined by the diff, not author
   characterization (default in-scope if uncertain).

4. **Review agents must check for a linked issue or
   justification.** When reviewing a PR that creates or modifies a skill
   file, verify that the PR meets Rule 3's issue-or-justification
   requirement. Reject comments, vacuous justifications, and
   non-substantive issues (default in-scope if uncertain). Findings should
   include missing or inadequate issues and justifications, not only
   formatting and specification-hierarchy checks.

### Aligning skills with the specification hierarchy

A skill implementing or describing a ProtoBot or Specification
Toolkit capability must remain consistent with every governed `docs/`
Markdown document. When creating, modifying, or reviewing such a skill,
agents must:

1. **Register new Toolkit capabilities.** Add the capability to
   `components.md`, and distinguish it from repository process
   skills.

2. **Keep Toolkit frontmatter harness-agnostic.** Use `name`
   and `description` only, as required by the
   [Agent Harness Adapter Contract][agent-harness-doc]. Do not
   add harness-specific fields or placeholders such as
   `user-invocable`, `allowed-tools`, or `$ARGUMENTS`.

3. **Use ProtoBot's defined vocabulary.** State-driven EARS
   uses `While`, not undeclared aliases. Persisting skills use
   ADR-0002 relationships (`depends-on`,
   `conflicts-with`, `supersedes`, `related-to`); host-independent
   skills use non-persisted companion-role labels for suggested
   companions.

4. **Emit machine-separable fields.** Host-independent elicitation skills
   emit portable-response fields defined in
   [`ears-and-review.md`][ears-and-review-doc]
   ("Portable Response", including `Responsible system`). Cite the
   [Adapter Contract][agent-harness-doc] only for host-mapping named
   fields (`Affected interfaces`, `Observable at the named boundary alone`)
   onto ADR-0002 persistence fields (`applies_to`, `verification.mode`).
   Do not demand persistence fields from host-independent skills;
   emit required properties as fields, not only as prose.

5. **Review agents must check skill-to-spec alignment.** When
   reviewing a PR that creates or modifies a skill file, verify
   the skill against the specification hierarchy, not only
   sibling skills. Findings should include
   capability-registration gaps, harness-specific frontmatter
   that violates Toolkit skill rules, and terminology that
   conflicts with `overview.md` or `components.md`.
