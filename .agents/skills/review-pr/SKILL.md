---
name: "review-pr"
description: >
  Provides a structured process and a comment format for reviewing pull
  requests in a repository where autonomous agents also review and fix.
  Runs in six phases, gated by the user: quiesce the bot loop, draft a local
  review, post it, apply it in one batch, close it out, release the PR. Use
  when asked to "review a PR", "review PR #N", or "give me a review".
---

# Reviewing a Pull Request

In a repository that runs fullsend, two review loops can share a PR: yours
and the bot's. They are not coordinated. A fix commit pushes, the push wakes
the review agent, the review agent requests changes, and that starts another
fix. The loop is bounded — the bot is refused once the PR carries five fix
commits, whoever triggered them — but while it runs it moves the head under
your line numbers and cancels the fix run you started, because two fix runs
cannot share a PR and the newer one wins.

So the first thing a review does is stop that loop. Everything else follows.

| Phase | Does | Then |
|-------|------|------|
| 0. Quiesce | Stops the bot fix loop, freezes the head | Continue |
| 1. Draft | Writes `review-<PR number>.md` in the repository root | Ask the user to read it |
| 2. Post | One GitHub review: a summary plus the inline comments | Ask if the fix agent should run |
| 3. Apply | One PR comment that starts with `/fs-fix` | Ask the user to read the commit |
| 4. Close out | Resolves threads, replaces the review | Continue |
| 5. Release | Removes the labels phase 0 added | Report |

Phases 0 and 1 run without asking. Every later phase needs the user to
agree first.

"Review a PR" means phases 0 and 1. It does not mean all six. A user who
wants everything in one go says so.

## Before you start — what this PR allows

Three facts about the PR decide which phases can run. Check them first.

| Fact | How to check | Decides |
|------|--------------|---------|
| The repository runs fullsend | `.fullsend/config.yaml` exists, with `fix` in `roles` | Phases 0, 3 and 5 exist at all |
| The bot can fix on its own | PR author is the coder bot, or the PR carries `fullsend-fix` | Phase 0 is needed. On any other PR there is no loop |
| The PR is same-repo | `gh pr view $PR --json isCrossRepository` is `false` | Phase 3 works. The fix job refuses fork PRs outright |

Your own access matters too. `/fs-fix` and `/fs-fix-stop` need write access
on the repository (the PR author may also stop). `/fs-review` needs triage.
A command you are not allowed to send fails **silently** — the job exits
with a notice, nothing is posted. That is why phase 0 checks its own result.

## Phase 0 — quiesce

Skip this phase when the PR cannot loop: a human-authored PR with no
`fullsend-fix` label, or a repository without fullsend. Go to phase 1.

1. **Stop first, then wait.** Post a comment whose **entire body** is:

   ```text
   /fs-fix-stop
   ```

   Nothing else. The workflow matches on `==`, not on a prefix, so one extra
   word and the job does not run. Post it before you wait for anything: a
   review that finishes while you wait would start a fix, and the label has
   to be there before that verdict lands.

2. **Check the label landed.** Within a minute, `fullsend-no-fix` must show
   on the PR:

   ```bash
   gh pr view "$PR" --repo "$REPO" --json labels --jq '[.labels[].name]'
   ```

   If it does not, you lack write access and the job exited silently. Stop
   and say so.

   The label blocks **bot-triggered** fixes only. Your own `/fs-fix` still
   runs: the eligibility script exits before it reads the label when the
   trigger is human. Nothing removes the label on its own — not a fix run,
   not a review, not a merge. Phase 5 removes it.

3. **Now wait for anything in flight.** Runs are listed by PR title, not by
   PR number:

   ```bash
   gh run list --repo "$REPO" --limit 20 --json status,event,displayTitle \
     --jq '.[] | select(.displayTitle == "<PR title>")
                | select(.status != "completed")'
   ```

   A fix run that is still going will commit and wake a review. The label
   stops that review from starting another fix. When the list is empty, the
   head is frozen.

4. **Check for other reviewers.** An open `CHANGES_REQUESTED` from another
   human is a second loop with the same problems:

   ```bash
   gh api "repos/$REPO/pulls/$PR/reviews" --paginate \
     --jq '.[] | select(.state == "CHANGES_REQUESTED") | .user.login' | sort -u
   ```

   Their `/fs-fix` moves the head under your comments, and yours under
   theirs, and the newer run cancels the older. Tell the user who else is
   reviewing. Whoever posted `/fs-fix-stop` owns the apply: phase 3 then
   sends **one merged batch** for every reviewer, not one batch each. See
   "More than one reviewer" in phase 3.

The review agent cannot be suppressed. It will keep posting
`CHANGES_REQUESTED` on every push. With the label on, it cannot act on its
own verdict, so it is noise and not a race. Its verdict does not set the
PR's review decision either — only human reviews do.

## Phase 1 — the local review

- Fetch the PR with `gh pr view` and `gh pr diff`. Record the head SHA, the
  base branch, and the merge base. Every line number in the review refers to
  the head SHA, which phase 0 has made stable.
- Read the full diff. Then read every file the diff cites or depends on. For
  design documents, read the sibling documents end to end. Contradictions
  live in the parts that nobody cites.
- Check every claim against its source and every link against its target.
  Record what you checked and what you did not.
- Write the review to `review-<PR number>.md` in the repository root. Do not
  commit it, do not post it, do not push.

Then stop. Name the file and ask the user to read it.

The user agrees in a plain sentence: "I agree", "looks good", "go ahead".
Anything else is a change request. Edit the document, then stop again.
Silence is not agreement.

### Review document

1. Header: PR link, head and base SHAs, author, review date, files touched.
2. Verdict: approve or request changes, then the blocking comments as a
   list, one line each: ID and title. Then any `chore`, then praise, then
   any `thought`. Nothing else.
3. Comments, grouped in this order: blocking; non-blocking issues and
   suggestions; todos, nitpicks, and questions; other files. Inside a group,
   keep file order.
4. What was checked, and what was not.

### Comment format

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

- **Heading:** an ID and a title. The ID is a group letter plus a number.
  The title is how a reader remembers the comment: 3 to 7 words that name
  the problem, not the location and not the fix. Titles are unique inside
  one review.
- **Location:** `path:line` or `path:start-end` at the head SHA, on its own
  line. Separate several ranges with commas. The first range is the anchor;
  the rest are repeated in the posted body as `Also lines X-Y.` The anchor
  must be a line the PR changed — see phase 2.
- **Subject line:** `**label (decoration):** subject`. The subject is one
  sentence that states the claim.
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

### Rules for the content

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

## Phase 2 — post to GitHub

Post the whole review as **one** GitHub review, never as a stream of
separate comments. One review is one notification and one review state.

Build a JSON payload and send it in a single call:

```bash
gh api --method POST "repos/$REPO/pulls/$PR/reviews" --input review.json
```

```json
{
  "commit_id": "<head SHA from phase 1>",
  "event": "REQUEST_CHANGES",
  "body": "<the verdict section>",
  "comments": [
    {
      "path": "docs/architecture/architecture.md",
      "start_line": 505,
      "line": 519,
      "side": "RIGHT",
      "body": "**B5 · Request tools missing**\n\n**issue (non-blocking):** ..."
    }
  ]
}
```

- `event` is `REQUEST_CHANGES` when the review has a blocking comment,
  `APPROVE` when it has none and the user approves, `COMMENT` otherwise.
- **Every anchor must be a line inside the PR's diff.** GitHub rejects the
  whole review with `422` if one comment points at an unchanged line. For a
  finding on an unchanged line, anchor to the nearest changed line and name
  the real range as `Also lines X-Y.`, or make it a `chore` in the body.
- Drop `start_line` for a single-line anchor. Keep `side` as `RIGHT` unless
  the comment is about a deleted line.
- Each inline body starts with the **bold heading**, then any extra ranges
  as `Also lines X-Y.`, then the subject line and the discussion. The
  heading is posted. Phase 3 refers to comments by their ID, so the ID has
  to survive into GitHub.
- The `body` ends with the head SHA the line numbers refer to, and a link
  to Conventional Comments.
- Record the login that posted the review — `gh api user --jq .login`.
  Phase 3 filters on it.

Then stop. Report the review URL and ask whether the fix agent should run.

## Phase 3 — apply, in one batch

Only on a same-repo PR. The fix job's first step fails a fork PR with
"Fork PRs are not allowed to trigger the fix agent". On a fork PR, hand the
review to the author instead and skip to phase 4.

**Send one batch per PR.** Not one per group letter. Every batch costs an
iteration and rewrites the files, which makes the line numbers in every
comment you have not sent yet wrong. One batch is the difference between a
review that lands and a review that rots.

Select by **label**, never by group letter:

| Label | Send it? |
|-------|----------|
| `issue (blocking)`, `issue (non-blocking)`, `todo`, `chore` | yes |
| `suggestion` | only the IDs the user names |
| `question`, `nitpick`, `praise`, `thought`, `note` | never |

A `question` needs the user's answer, not a patch. Sending one makes the
agent guess.

Check the budget before sending. A human-triggered run is refused once the
PR carries ten fix commits, whoever triggered them:

```bash
gh api "repos/$REPO/pulls/$PR/commits" --paginate \
  | jq -s 'add | [.[] | select(.commit.author.name == "fullsend-fix")] | length'
```

The trigger is a **new top-level PR comment** whose **first word** is
`/fs-fix`. It cannot go anywhere else:

| Where | Fires? |
|-------|--------|
| New PR comment, `/fs-fix` first word | yes |
| Appended to the review body | no — a human review is not routed |
| Inside an inline comment | no — that event is not subscribed to |
| Added by editing an existing comment | no — only `created` is routed |

The fix agent does not read inline comments. It reads the review body of a
**bot** review and the text of the `/fs-fix` comment. So the comment has to
tell it how to fetch the review:

````markdown
/fs-fix Address N review comments on this PR.

They are inline review comments, so they are not in your `review-body.txt`.
Fetch them:

```
gh api "repos/${REPO_FULL_NAME}/pulls/${PR_NUMBER}/comments" --paginate \
  --jq '.[] | select(.user.login == "<reviewer login>") |
        "=== \(.path) lines \(.start_line // .line)-\(.line) ===\n\(.body)\n"'
```

Handle exactly these, matched by the ID in the bold heading of each comment:

- `issue`: B1, B2, B5
- `todo`: C1, C2

Skip everything else. Leave <the excluded IDs> alone; those are suggestions,
nitpicks, questions and praise that I am handling myself.

`issue` means change it, `todo` means a small required change. Where you
disagree with a finding, record the disagreement instead of forcing a change.
````

- `REPO_FULL_NAME` and `PR_NUMBER` are set inside the agent sandbox. Leave
  them as written; do not expand them.
- The `select` on the reviewer login keeps other reviewers' and other bots'
  inline comments out. That is deliberate: this batch is your review only.
- List the IDs one by one, and name what to leave alone. "Comments starting
  with B" is not enough when the labels are mixed inside a group.
- **Never send a second `/fs-fix` while one is running.** It cancels the
  first. Wait for the status comment that says Finished.
- A run has a 25 minute timeout. If a batch is very large, say so to the
  user and let them decide to split rather than splitting silently.
- If a second batch is unavoidable, tell the agent the earlier fix moved the
  files and to match on the quoted text, not on the line number.
- If the run ends Cancelled or Failed, read the status comment. Cancelled
  with no `/fs-fix` of yours after it means another fix run took the slot:
  phase 0 was skipped or the label is gone. Fix that, then re-send.

### More than one reviewer

Reviews run in parallel. Applying them does not: there is one fix slot per
PR, and every fix commit moves the head under every unapplied comment. So
when phase 0 found other open reviews, the owner sends **one batch for all
of them**.

- Read the other reviewers' comments and sort them by the same label table.
  If they did not use Conventional Comments, decide what is actionable and
  name it. The owner is accountable for what the batch contains.
- Fetch every reviewer in one `select`, and print the login in the header
  so the agent can tell whose finding is whose — an ID like `A1` can repeat
  across reviewers:

  ```text
  --jq '.[] | select(.user.login == "alice" or .user.login == "bob") |
        "=== \(.user.login) · \(.path) lines \(.start_line // .line)-\(.line) ===\n\(.body)\n"'
  ```

- List the IDs per reviewer: `alice: B1, B2, B5 · bob: A1, A3`.
- Tell the other reviewers the batch is in. Everyone then reads the same
  commit and approves the same SHA.

What this does not solve: a reviewer who posts after the batch, or who
sends their own `/fs-fix` anyway. Nothing in fullsend queues or locks a
fix. The label stops the bot; only the owner rule stops the humans.

Then stop. Report the commit and ask the user to read it.

## Phase 4 — close out

1. The user reads the commit and gives a verdict. Read the agent's summary
   comment too: findings it **disagreed** with are not done. Those threads
   need a human answer, not a resolve.

2. Resolve the threads the user accepts. Yours only — the query below
   filters on the login from phase 2, so other reviewers' and bots' threads
   are untouched:

   ```bash
   gh api graphql -f query='
     query($owner:String!,$repo:String!,$pr:Int!){
       repository(owner:$owner,name:$repo){
         pullRequest(number:$pr){
           reviewThreads(first:100){
             nodes{id isResolved comments(first:1){nodes{author{login}}}}}}}}' \
     -F owner="$OWNER" -F repo="$REPO_NAME" -F pr="$PR" \
     --jq '.data.repository.pullRequest.reviewThreads.nodes[]
           | select(.isResolved | not)
           | select(.comments.nodes[0].author.login == "<reviewer login>")
           | .id'
   ```

   Then one mutation per thread ID:

   ```bash
   gh api graphql -f query='
     mutation($id:ID!){
       resolveReviewThread(input:{threadId:$id}){thread{isResolved}}}' \
     -F id="$THREAD_ID"
   ```

   `first:100` covers a normal PR. Past that, page with `after:`.

3. Replace the review. A new commit never clears a `CHANGES_REQUESTED` —
   only a new review from the same reviewer does:

   ```bash
   gh api --method POST "repos/$REPO/pulls/$PR/reviews" \
     -f event=APPROVE -f body="<what changed since the review>"
   ```

   An approval is tied to the head SHA at the moment it is given. Any push
   after it — another reviewer's `/fs-fix`, the author, anyone — leaves it
   standing on an older commit. Say which SHA the approval covers.

Never submit an `APPROVE` the user has not asked for by name. The approval
carries their identity, not yours.

## Phase 5 — release

The label is shared by everyone reviewing this PR. Before you remove it,
run the phase 0 reviewer check again. If another human still has an open
`CHANGES_REQUESTED`, leave the label on, tell the user who, and stop here:
their review is still in progress and the label is protecting it.

Otherwise remove what phase 0 added, and any stale state:

```bash
gh pr edit "$PR" --repo "$REPO" --remove-label fullsend-no-fix
```

- Remove `needs-human` if it is set and the PR no longer needs a human.
- Remove `ready-for-merge` if the PR still has an open request for changes
  from anyone. It means "all reviewers approved" and it is wrong there.
- Never leave `fullsend-no-fix` on a PR you walk away from. It silently
  disables the bot's own fixes for everyone, and nothing else removes it.

Then report: the commits, the review state and which SHA it covers, the
threads resolved, and any comments left unsent.

## Reference — how fullsend routes

The rules above are not preferences. They follow from what the dispatch
workflow does:

| Into | From |
|------|------|
| `fix` | a human `/fs-fix` comment from a write-access user; or a `CHANGES_REQUESTED` review **by the review bot**, when the PR author is a bot or the PR carries `fullsend-fix`, the PR is same-repo, and `fullsend-no-fix` is absent |
| `review` | PR `opened`, `synchronize`, `ready_for_review`; the `ready-for-review` label; a `/fs-review` comment |

The cycle is: a fix commits, the push is a `synchronize`, the review agent
runs, it requests changes, that starts another fix. Phase 0 cuts the last
arrow and leaves your own entry point open.

Caps, from the fix harness: the run counts the PR's commits authored by
`fullsend-fix`. A bot-triggered run is refused at five, a human-triggered
run at ten — both against the same total, whoever made the commits. Past
five the loop stops itself; below five, it will keep taking the slot.
