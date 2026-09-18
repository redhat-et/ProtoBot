# AGENTS

- Create worktrees in `.worktrees/`
- All pre-commit tests must pass before committing changes. In sandboxed
  environments without network access, use `python scripts/lint.py` as a
  network-independent alternative to `pre-commit run`.
  Key flags: `--all-files` (check every tracked file),
  `--files FILE [FILE ...]` (check specific files),
  `--check-parity` (verify registry covers all configured hooks).
  Tools must be pre-installed when running without network access.
- The upstream repository is `redhat-et/protobot`. Ensure that pull requests
  are made against this repository.
- Agent skills live in `.agents/skills/`. `.claude/skills` is a symlink to
  that directory so Claude Code finds the same skills; do not add skills
  under `.claude/` directly.

## Specification document hierarchy

Every Markdown file under `docs/` is a governed hierarchy member,
including files added later. Membership is the `docs/` prefix, not
the list. Omission from the list does not exclude a file or leave
membership undecided.

- `docs/vision.md` — project Vision (purpose, users, outcomes).
- `docs/architecture.md` — Architecture artifact (external
  interfaces, persistent state, environmental constraints).
- `docs/architecture/overview.md` — guiding principles, EARS
  format, workflow, and platform.
- `docs/architecture/components.md` — component architecture,
  interfaces, and cross-cutting concerns.
- [`docs/architecture/git-integration.md`][git-integration-doc] — governed Git
  and project-repository integration.
- [`docs/architecture/validation-rules.md`][validation-rules-doc] — lifecycle
  validation, authorization, transition, and rejection contract.
- `docs/architecture/user-interaction-flow.md` — phase details,
  sequence diagrams, and testing strategy.
- `docs/architecture/drafting-table-ux.md` — stable interaction
  contract for the first local Drafting Table.
- [`docs/architecture/agent-harness/`][agent-harness-doc] — the Agent
  Harness Adapter Contract (`adapter-contract.md`) and its harness
  bindings (`opencode.md`, `claude-code.md`, `codex.md`).
- `docs/architecture/related-work.md` — internal and external
  projects informing the design.
- `docs/architecture/open-questions.md` — unresolved design
  questions across all areas.
- `docs/decisions/` — architecture decision records (ADRs).

[git-integration-doc]: docs/architecture/git-integration.md
[validation-rules-doc]: docs/architecture/validation-rules.md
[agent-harness-doc]: docs/architecture/agent-harness/
[ears-and-review-doc]: .agents/skills/eliciting-requirements/references/ears-and-review.md

### Rules for creating or modifying specification documents

When creating or modifying any document under `docs/`, agents
must follow these rules:

1. **Read all sibling specification documents first.** Before
   writing or revising a specification document, read every
   other governed Markdown document under `docs/`. Cross-document
   consistency cannot be verified without knowing what the
   sibling documents say.

2. **Account for all components, interfaces, and constraints.**
   New or revised specification documents must account for
   every component, interface, and constraint enumerated in
   `docs/architecture/components.md` and
   `docs/architecture/overview.md`. If a component or
   interface from those documents is relevant to the new
   document's scope, it must be addressed — not silently
   omitted.

3. **Verify deployment topology, security posture, and
   persistent state coverage.** Cross-check the document
   against `docs/architecture/overview.md` and
   `docs/architecture.md` to confirm that deployment topology
   (single-player, multi-player, web), security posture
   (credential isolation, sandbox constraints), and persistent
   state (all stores enumerated in the Architecture) are
   covered where relevant.

4. **Review agents must check cross-document coverage.** When
   reviewing a PR that creates or modifies a specification
   document, verify that the document accounts for components,
   interfaces, and constraints from `components.md` and
   `overview.md`. Findings should include coverage gaps, not
   only formatting and cross-reference text matching. Do not raise
   a hierarchy-membership finding for an unlisted `docs/` file.
   Checks must go beyond link freshness and heading alignment:
   register new capabilities where `components.md` enumerates
   them, avoid conflicting with principles in `overview.md`
   or `components.md` (for example, the harness-agnostic
   Toolkit principle in `components.md`), and match defined
   ProtoBot keywords and relationship terms; an undeclared
   alias for a defined keyword is a defect.

## Agent skills

### Rules for creating or modifying skill files

When creating or modifying any skill file under `.agents/skills/`,
agents must follow these rules:

1. **Read all sibling skill files first.** Before writing or
   revising a skill file, read every other skill file under
   `.agents/skills/`. Cross-skill consistency cannot be verified
   without knowing what the sibling skills say.

2. **Follow the formatting conventions observed in sibling files.**
   Match backtick-quoting of refs, command syntax style, and
   structural patterns used by sibling skills. For example, quote
   git refs as `` `upstream/main` `` rather than leaving them
   unquoted. Inconsistent formatting with sibling skills is a
   defect.

3. **Match structural conventions, not behavioral fields.**
   Structural formatting conventions should be matched to siblings,
   but behavioral fields like dispatch parameters must be determined
   by the skill's own requirements, not copied from siblings.
   Blindly copying behavioral configuration from a sibling can
   produce incorrect dispatch or workflow behavior.

### Aligning skills with the specification hierarchy

A skill that implements or describes a ProtoBot or Specification
Toolkit capability must remain consistent with every governed
Markdown document under `docs/`. When creating, modifying, or
reviewing such a skill, agents must:

1. **Register new Toolkit capabilities.** Add the capability to
   `components.md`, and distinguish it from repository process
   skills.

2. **Keep Toolkit frontmatter harness-agnostic.** Use `name`
   and `description` only, as required by the
   [Agent Harness Adapter Contract][agent-harness-doc]. Do not
   add harness-specific fields or placeholders such as
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
   [Adapter Contract][agent-harness-doc] only for host-mapping the
   subset of those fields it names (`Affected interfaces`,
   `Observable at the named boundary alone`) onto ADR-0002
   persistence fields (`applies_to`, `verification.mode`). Do not
   demand persistence fields from host-independent skills, and
   emit required properties as fields, not only as prose.

5. **Review agents must check skill-to-spec alignment.** When
   reviewing a PR that creates or modifies a skill file, verify
   the skill against the specification hierarchy, not only
   against sibling skills. Findings should include
   capability-registration gaps, harness-specific frontmatter
   that violates Toolkit skill rules, and terminology that
   conflicts with `overview.md` or `components.md`.
