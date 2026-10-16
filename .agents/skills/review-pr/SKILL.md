---
name: "review-pr"
description: >
  Provides a structured process and a comment format for reviewing pull
  requests. Use when asked to "review a PR", "review PR #N", or "give me a
  review".
---

# Reviewing a Pull Request

When asked to "review a PR", follow this process. The output is a local
review document, not comments on GitHub. Post to GitHub only when asked to.

## Process

- Fetch the PR with `gh pr view` and `gh pr diff`. Record the head SHA, the
  base branch, and the merge base. Every line number in the review refers to
  the head SHA.
- Read the full diff. Then read every file the diff cites or depends on. For
  design documents, read the sibling documents end to end. Contradictions
  live in the parts that nobody cites.
- Check every claim against its source and every link against its target.
  Record what you checked and what you did not.
- **Review agents must check registered specification artifact and
  project configuration updates.** When a PR creates or modifies a
  registered specification artifact, or when Rule 4 identifies staleness
  in a registered specification artifact, inspect each such registered
  specification artifact for hand edits under Rule 5 and run
  `ears-manager check` on a checkout or worktree of the recorded PR head; a
  check on the base branch is not evidence. Require it to pass without
  `artifact.digest_mismatch`, `artifact.operation_missing`, or
  `artifact.unregistered`. The check requires each registered artifact path
  or canonical-content delta to have a matching `artifact_id` in an
  `artifact_operations[]` entry in any proposed change set, and rejects
  unregistration of an artifact-registry entry present on the default
  branch. This confirms manifest association only; it does not prove
  `ears-manager artifact put` was invoked. Reviewers must flag hand edits
  they can identify, not claim CLI provenance from an operation record.
  Separately, whenever a PR modifies
  [.protobot/project.yaml](../../../.protobot/project.yaml), inspect the
  project configuration under Rule 5 of the specification-document rules in
  `AGENTS.md` and flag any hand edit, even if no registered specification
  artifact is changed. Metadata-only changes to an artifact-registry entry
  do not by themselves require an artifact operation. Findings should
  include each listed diagnostic; any hand edit identified while inspecting
  a registered specification artifact when a PR creates or modifies a
  registered specification artifact, or when Rule 4 identifies staleness in
  a registered specification artifact; any hand edit identified in
  [.protobot/project.yaml](../../../.protobot/project.yaml) whenever a PR
  modifies [.protobot/project.yaml](../../../.protobot/project.yaml), even if
  no registered specification artifact is changed; and, when Rule 4
  identifies staleness in a registered specification artifact, a staleness
  finding that prescribes `ears-manager artifact put`, not a hand edit.
- Write the review to `review-<PR number>.md` in the repository root. Do not
  commit it, do not post it, do not push.

## Review document

1. Header: PR link, head and base SHAs, author, review date, files touched.
2. Verdict: approve or request changes, then the blocking comments as a
   list, one line each: ID and title. Then any `chore`, then praise, then
   any `thought`. Nothing else.
3. Comments, grouped in this order: blocking; non-blocking issues and
   suggestions; todos, nitpicks, and questions; other files. Inside a group,
   keep file order.
4. What was checked, and what was not.

## Comment format

Each comment is a heading, a location, and a
[Conventional Comment](https://conventionalcomments.org):

```markdown
### B5 · Request tools missing

`docs/architecture/architecture.md:505-519`

**issue (non-blocking):** The tool inventory has no request tools.

The Drafting Table agent creates and refines backlog requests
(`docs/architecture/user-interaction-flow.md:842-866`). The table has only
"WMS query" and "WMS resolve".

**suggestion:** Add `WMS request create/refine/link`.
```

- **Heading:** an ID and a title. The ID (group letter + number) is for
  cross-reference only. The title is how a reader remembers the comment:
  3 to 7 words that name the problem, not the location and not the fix.
  Titles are unique inside one review.
- **Location:** `path:line` or `path:start-end` at the head SHA, on its own
  line. Separate several ranges with commas.
- **Subject line:** `**label (decoration):** subject`. The subject is one
  sentence that states the claim. This line and the discussion are what
  gets pasted into GitHub. The heading and the location are not.
- **Discussion:** evidence with `path:line` citations, two to five lines,
  then the fix. An `issue` is always paired with a `**suggestion:**` line.
- **Labels:** `issue` for a concrete defect, `suggestion` for an improvement
  with its reason, `todo` for a small required change, `question` for a
  concern you cannot settle, `nitpick` for a preference, `praise` for what
  is right, `chore` for a task that must happen before merge and has no
  line to anchor to (it blocks, like an `issue (blocking)`), `thought` and
  `note` for non-blocking context.
- **Decorations:** always decorate `issue` with `(blocking)` or
  `(non-blocking)`. `(blocking)` means the PR must not merge until the
  comment is resolved. A `suggestion` without a decoration is non-blocking.

## Rules for the content

- Post only what the author can act on. A check that found nothing (the
  branch is behind `main`, the merge is clean, no stale wording is left)
  goes in the "What was checked" section of the local document, never in
  the posted review or its comments.
- Problem first, then evidence, then fix. Say what is wrong before why.
- Every fix must make the design or the code simpler or more complete. Drop
  a proposal that adds more than it removes.
- Cite sources, not memory. A claim about another file carries its
  `path:line`.
- Short sentences, one idea each. If a comment needs more than about ten
  lines, split it or cut it.
- Mark uncertainty. "As far as I know" is allowed. A guess presented as a
  fact is not.
