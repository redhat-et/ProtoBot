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
- Check CI status on the PR head with `gh pr checks`. A successful
  `V2 specification validator` check associated with that head is
  corroborating freshness evidence, not a substitute for the local check. If
  the check is pending or missing, wait for it or rerun it; do not report an
  author-actionable finding solely for that state. A completed failure
  belongs in the review verdict, with the local check used to identify the
  actual diagnostic.
- Before detailed specification analysis, run
  `python3 scripts/check_spec_validator.py` on a checkout or worktree of the
  recorded PR head
  ([#547](https://github.com/redhat-et/ProtoBot/issues/547)). A comparison
  base that the event requires but Git cannot resolve is a blocking freshness
  finding, and it stops detailed specification analysis.
- Read the full diff. Then read every file the diff cites or depends on. For
  design documents, read the sibling documents end to end. Contradictions
  live in the parts that nobody cites.
- Check every claim against its source and every link against its target.
  Record what you checked and what you did not.
- Write the review to `review-<PR number>.md` in the repository root. Do not
  commit it, do not post it, do not push.

## Review document

1. Header: PR link, head and base SHAs, author, review date, files touched.
2. Verdict: approve or request changes, then the blocking comments as a
   list, one line each: ID and title. Failing CI checks, including
   `V2 specification validator`, belong in the review verdict with the
   blocking comments or as `chore`. Then any `chore`, then praise, then
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
  sentence that states the claim. Inline comment text includes the full subject
  line and discussion, including any `**suggestion:**` line, but excludes the
  heading and location. For a finding posted inline, the main review body
  includes its full local comment entry.
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

- Post only what the author can act on. Keep praise, `thought`, `note`, and
  context-only comments in the local document, not in the GitHub review body
  or comments. A check that found nothing (the branch is up to date with
  `main`, the merge is clean, and no stale wording is left) goes in the "What
  was checked" section of the local document, never in the posted review or
  its comments. A comparison base that Git cannot resolve is a blocking
  freshness finding, not a check that found nothing. Do not create a GitHub review when
  no actionable comments are selected; preserve the required local verdict
  and "What was checked" sections.
- Duplicate each finding posted inline once in the main GitHub review body, in
  posted order, as its full existing local comment entry (heading, Location,
  subject line, discussion, and suggestion); retain its corresponding inline
  comments. That entry already carries the file and line/ranges, severity,
  problem, evidence, and fix, so there is no second field layout. Do not
  invent Critical/Major/Minor severity labels.
- For disjoint ranges, post one inline comment per contiguous range; one body
  entry carries all ranges. Keep the local verdict and other local-only material
  out of the GitHub review body and comments; the GitHub review event carries
  the verdict.
- Problem first, then evidence, then fix. Say what is wrong before why.
- Every fix must make the design or the code simpler or more complete. Drop
  a proposal that adds more than it removes.
- Cite sources, not memory. A claim about another file carries its
  `path:line`.
- Short sentences, one idea each. If a comment needs more than about ten
  lines, split it or cut it.
- Mark uncertainty. "As far as I know" is allowed. A guess presented as a
  fact is not.
