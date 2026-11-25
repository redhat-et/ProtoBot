# AGENTS

- Pre-commit tests must pass before committing. Offline, use
  `python scripts/lint.py` for `pre-commit run`; pre-install tools and
  populate the hook cache with `pre-commit install-hooks`.
  Key flags: `--all-files` (tracked files), `--files FILE [FILE ...]`
  (specific files), `--check-parity` (validate config and offline hook cache),
  `--online-if-ci` (allow network when CI=true).
  Go changes must pass `gofmt` and `go vet` across affected Go modules
  before committing.
- Pull requests target the `redhat-et/protobot` upstream repository.
- Agent skills live in `.agents/skills/`; `.claude/skills` symlinks
  there. Do not add them under `.claude/` directly.

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

`AGENTS.md` enters every dispatched agent's prompt. The `context-budget`
rule's `agents-md` warn limit in `.skillsaw.yaml` triggers the finding;
[`scripts/check_baseline_ratchet.py`](scripts/check_baseline_ratchet.py)
rejects a `.skillsaw-baseline.json` ceiling increase.

1. **Respect the prompt context budget.** Never raise or delete a
   `.skillsaw-baseline.json` ceiling row for an existing file. After a
   genuine prose shrink, lower the `agents-md` warn limit, then run
   `skillsaw baseline` to ratchet the ceiling
   down.
2. **Consolidate or extract when adding prose.** Offset new rules or
   explanations by consolidating prose or extracting detail into an
   on-demand `.agents/skills/` skill.
3. **Review agents must recognize budget consolidation.** Treat prose
   consolidation offsetting a new `AGENTS.md` addition as budget compliance,
   not scope creep, if it omits no governance requirement, defined
   keyword, or diagnostic name and introduces no undeclared alias or
   terminology drift. Consolidation does not exempt entries from
   sibling-entry or intra-entry terminology rules.

## Rules for creating or modifying sibling entries

A governed scope defines a domain of applicability; its list or
governed collection defines the sibling set. A **module README** is
the top-level `README.md` of a directory with a `go.mod`.

When creating or modifying a numbered rule in `AGENTS.md`, specification
document under `docs/`, skill file under `.agents/skills/`, module
README, or an entry in a future governed scope, agents must follow these
rules. Review agents must enforce these rules reflexively on PRs that
create, modify, or consolidate entries within `AGENTS.md` itself.

1. **Read all sibling entries first.** Read every other entry in the
   list or governed collection before drafting or revising one.

2. **Reuse established terminology.** Match sibling entries' terms for
   the same concepts. An undeclared alias for a term used in the
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

- `docs/vision.md` — Vision artifact (purpose, users, outcomes).
- `docs/architecture.md` — Architecture artifact (external interfaces,
  persistent state, environmental constraints).
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
  sequence diagrams, testing strategy.
- `docs/architecture/drafting-table-ux.md` — stable interaction
  contract for the first local Drafting Table.
- [`docs/architecture/agent-harness/`][agent-harness-doc] — Agent
  Harness Adapter Contract (`adapter-contract.md`) and bindings
  (`opencode.md`, `claude-code.md`, `codex.md`).
- [`docs/architecture/related-work.md`][related-work-doc] —
  internal and external projects informing the design.
- [`docs/architecture/evaluation-corpus.md`][evaluation-corpus-doc] —
  versioned evaluation fixture schema and result contract.
- `docs/architecture/open-questions.md` — unresolved design questions
  across all areas.
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
   Specification documents must account for every
   component, interface, and constraint in
   `docs/architecture/components.md` and
   `docs/architecture/overview.md`, and address, not silently
   omit, those relevant to their scope.

2. **Verify deployment topology, security posture, and
   persistent state coverage.** Cross-check
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
   hierarchy-membership findings for unlisted `docs/` files.
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
   prose describing the same behavior and flag any
   not updated to match.

5. **Preserve accepted and superseded architecture decision records.**
   Accepted and superseded records under `docs/decisions/` are
   immutable: never rewritten or replaced in place, never deleted or
   renamed, even after supersession or component retirement. To amend a
   record, append a dated amendment block citing the change, its
   rationale, and updated architecture documents. The only authorized
   transition is one dated accepted-to-superseded status-line change per
   the [supersession
   skill](.agents/skills/adr-supersession/SKILL.md). Review agents must
   verify no in-place rewrite, deletion, or rename, and amendments and
   transitions match it.

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
   requirements, not copied sibling configuration, which can cause
   incorrect dispatch or workflow behavior.

3. **Link a GitHub issue or justify why none is needed.**
   When reversing or correcting a business rule, link an issue describing
   incorrect prior behavior and impact, or justify why none was filed
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
Toolkit capability must be consistent with every governed `docs/`
Markdown document. When creating, modifying, or reviewing one, agents
must:

1. **Register new Toolkit capabilities.** Add the capability to
   `components.md` and distinguish it from repository process
   skills.

2. **Keep Toolkit frontmatter harness-agnostic.** Use `name`
   and `description` only, as required by the
   [Agent Harness Adapter Contract][agent-harness-doc]. Do not
   add harness-specific fields or placeholders like
   `user-invocable`, `allowed-tools`, or `$ARGUMENTS`.

3. **Use ProtoBot's defined vocabulary.** State-driven EARS
   uses `While`, not undeclared aliases. Skills persisting
   records use ADR-0002 relationships (`depends-on`,
   `conflicts-with`, `supersedes`, `related-to`); host-independent
   skills use non-persisted companion-role labels for suggested
   companions.

4. **Emit machine-separable fields.** Distinguish vocabulary
   by layer: host-independent elicitation skills emit portable-response
   fields defined in [`ears-and-review.md`][ears-and-review-doc]
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
   that violates Toolkit skill rules, and terminology
   conflicting with `overview.md` or `components.md`.
