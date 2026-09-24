# Board Fixture and Dry-Run

Use this fixture when changing Step 3 eligibility, Step 4 stale-status
rules, or the Step 2 `items` shape those rules consume. Lint checks do
not catch a wrong assumption about sub-issues, blockers, or parent
status.

The dry-run jq encodes Steps 3 and 4. If it disagrees with those
steps, that is a defect. A change to those steps must update the jq
and the recorded recommendations together.

## Fixture files

- [board-tidy-items.json](board-tidy-items.json) - a representative
  Step 2 `items` array plus `excluded_cross_repository` and
  `excluded_non_issue`.
- [board-tidy-recommendations.jq](board-tidy-recommendations.jq) -
  Step 3 eligibility and Step 4 stale-status rules as a Step 5
  proposal.
- [board-tidy-recommendations.json](board-tidy-recommendations.json) -
  recorded dry-run output for `active` and `none` milestone modes.

## Coverage

The `items` array includes:

- Unblocked Backlog items with an open milestone, including a
  sub-issue whose parent is In progress.
- Unblocked Backlog items with no milestone, including a sub-issue.
- A blocked Backlog item, a Backlog item whose blockers are all
  closed, `renovate[bot]`, the exact title
  **Renovate Dependency Dashboard**, and a null `author_login`.
- Closed items whose status is not Done, including a sub-issue.
- Ready with open blockers, In progress with every returned blocker
  open, and In progress with mixed open and closed blockers.
- Open items with `blockers_complete == false` in Backlog, Ready, and
  In progress, and a closed item with incomplete blockers.
- Closed Done items, including a sub-issue whose parent is In
  progress.
- In review, unblocked Ready, unblocked In progress, and Backlog
  whose milestone is closed.
- A cross-repository item and a non-issue project item.

If a change introduces a case the fixture does not cover, add a
representative item first.

## Dry-run

From the repository root, with `MILESTONE_MODE` set to `active` or
`none`. For `active`, pass the fixture's open-milestone array; for
`none`, pass `[]`. Pipe live Step 2 JSON to the same jq instead of
the fixture file when running a live dry-run:

```bash
set -euo pipefail
: "${MILESTONE_MODE:?}"
: "${ACTIVE_MILESTONES_JSON:?}"
jq -e --arg milestoneMode "$MILESTONE_MODE" \
  --argjson activeMilestones "$ACTIVE_MILESTONES_JSON" \
  -f .agents/skills/board-tidy/references/board-tidy-recommendations.jq \
  .agents/skills/board-tidy/references/board-tidy-items.json
```

Fixture open-milestone array for `active` mode:

```json
[{"number": 1, "title": "Sprint 1", "state": "open"}]
```

Compare each mode to the recorded output:

```bash
set -euo pipefail
: "${MILESTONE_MODE:?}"
: "${ACTIVE_MILESTONES_JSON:?}"
diff -u \
  <(jq --arg mode "$MILESTONE_MODE" '.[$mode]' \
    .agents/skills/board-tidy/references/board-tidy-recommendations.json) \
  <(jq -e --arg milestoneMode "$MILESTONE_MODE" \
    --argjson activeMilestones "$ACTIVE_MILESTONES_JSON" \
    -f .agents/skills/board-tidy/references/board-tidy-recommendations.jq \
    .agents/skills/board-tidy/references/board-tidy-items.json)
```

A live dry-run of Steps 1-5 with no Step 6 mutations is an accepted
substitute for the fixture comparison.

## Recorded recommendations

`active` Move to Ready: 101, 102, 106. `none` Move to Ready: 103,
104. Item 102 is a sub-issue whose parent 117 is In progress; it is
eligible on its own merits.

Stale statuses are the same in both modes:

| Issue | Status | Suggested status | Reason |
|-------|--------|------------------|--------|
| 110 | In progress | Done | closed item is not Done |
| 111 | Backlog | Done | closed item is not Done |
| 112 | Ready | Backlog | Ready with open blockers |
| 113 | In progress | | In progress with open blockers |
| 115 | Ready | | blocker data is incomplete |
| 123 | Backlog | | blocker data is incomplete |
| 124 | In progress | Done | closed item is not Done |
| 125 | In progress | | blocker data is incomplete |

114 (mixed blockers), 116, 117, 118, 119, 120, and 121 produce no
stale-status row. 201 is `excluded_cross_repository`. 202 is
`excluded_non_issue`.

## Evidence before merge

Before merging a change to Step 3 eligibility, Step 4 stale-status
rules, or the Step 2 `items` shape those rules consume, include the
dry-run before/after for `move_to_ready` and `stale_statuses` against
this fixture, or the live Step 5 proposal, in the description of the
change. Lint checks alone do not satisfy this requirement.

Review agents must treat a missing dry-run or fixture before/after as
a finding when the diff changes Step 3 eligibility, Step 4
stale-status rules, or the Step 2 `items` shape those rules consume.
