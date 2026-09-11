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

ProtoBot's specification documents live under `docs/` in the
following hierarchy:

- `docs/vision.md` — project Vision (purpose, users, outcomes).
- `docs/architecture.md` — Architecture artifact (external
  interfaces, persistent state, environmental constraints).
- `docs/architecture/overview.md` — guiding principles, EARS
  format, workflow, and platform.
- `docs/architecture/components.md` — component architecture,
  interfaces, and cross-cutting concerns.
- `docs/architecture/user-interaction-flow.md` — phase details,
  sequence diagrams, and testing strategy.
- `docs/architecture/drafting-table-ux.md` — stable interaction
  contract for the first local Drafting Table.
- `docs/architecture/related-work.md` — internal and external
  projects informing the design.
- `docs/architecture/open-questions.md` — unresolved design
  questions across all areas.
- `docs/decisions/` — architecture decision records (ADRs).

### Rules for creating or modifying specification documents

When creating or modifying any document under `docs/`, agents
must follow these rules:

1. **Read all sibling specification documents first.** Before
   writing or revising a specification document, read every
   other document in the hierarchy above. Cross-document
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
   only formatting and cross-reference text matching.

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
