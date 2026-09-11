# Board Mutation Templates

Before each mutation, rerun the Step 1 status/milestone queries and the Step 2
item query. Set `STATUS_OPTIONS_JSON` to the Step 1 `.statuses` object and
`ACTIVE_MILESTONES_JSON` to the current open-milestone array. Set
`MILESTONE_DEPENDENT=true` only for Backlog-to-Ready moves; use `false` for
closed-to-Done moves, which remain valid on closed milestones. Set
`BLOCKER_DEPENDENT=true` only for Backlog-to-Ready and Ready-to-Backlog moves;
use `false` for closed-to-Done moves.

## Freshness Check

Set the `EXPECTED_*` variables from structured proposal data, then pipe fresh
Step 2 JSON into this check. `EXPECTED_BLOCKERS_JSON` contains only
`number`/`state` objects:

```bash
set -euo pipefail

: "${PROJECT_NODE_ID:?}"
: "${STATUS_FIELD_ID:?}"
: "${TARGET_OPTION_ID:?}"
: "${TARGET_STATUS:?}"
: "${APPROVED_PROJECT_NODE_ID:?}"
: "${APPROVED_STATUS_FIELD_ID:?}"
: "${APPROVED_TARGET_OPTION_ID:?}"
: "${APPROVED_TARGET_STATUS:?}"
: "${STATUS_OPTIONS_JSON:?}"
: "${ACTIVE_MILESTONES_JSON:?}"
: "${MILESTONE_DEPENDENT:?}"
: "${BLOCKER_DEPENDENT:?}"
: "${EXPECTED_TITLE:?}"
: "${EXPECTED_AUTHOR_LOGIN_JSON:?}"
: "${EXPECTED_MILESTONE_NUMBER:?}"
: "${EXPECTED_MILESTONE_STATE_JSON:?}"
if [ "$PROJECT_NODE_ID" != "$APPROVED_PROJECT_NODE_ID" ] \
  || [ "$STATUS_FIELD_ID" != "$APPROVED_STATUS_FIELD_ID" ] \
  || [ "$TARGET_OPTION_ID" != "$APPROVED_TARGET_OPTION_ID" ] \
  || [ "$TARGET_STATUS" != "$APPROVED_TARGET_STATUS" ]; then
  printf 'project metadata changed; refresh the proposal\n' >&2
  exit 1
fi
if ! jq -e --arg name "$TARGET_STATUS" --arg id "$TARGET_OPTION_ID" \
  '.[$name] == $id' <<<"$STATUS_OPTIONS_JSON" >/dev/null; then
  printf 'target status option changed; refresh the proposal\n' >&2
  exit 1
fi
if [ "$MILESTONE_DEPENDENT" = "true" ] && ! jq -e \
  --argjson number "$EXPECTED_MILESTONE_NUMBER" \
  'any(.[]; .number == $number)' <<<"$ACTIVE_MILESTONES_JSON" >/dev/null; then
  printf 'milestone is no longer active; refresh the proposal\n' >&2
  exit 1
fi

: "${EXPECTED_ITEM_ID:?}"
: "${EXPECTED_ISSUE_NUMBER:?}"
: "${EXPECTED_STATUS_JSON:?}"
: "${EXPECTED_STATUS_OPTION_ID_JSON:?}"
: "${EXPECTED_STATE:?}"
: "${EXPECTED_MILESTONE_NUMBER:?}"
: "${EXPECTED_BLOCKERS_JSON:?}"
: "${EXPECTED_OPEN_BLOCKER_COUNT:?}"
: "${EXPECTED_BLOCKERS_COMPLETE:?}"

jq -e \
  --arg itemId "$EXPECTED_ITEM_ID" \
  --argjson expectedIssueNumber "$EXPECTED_ISSUE_NUMBER" \
  --argjson expectedStatus "$EXPECTED_STATUS_JSON" \
  --argjson expectedStatusOptionId "$EXPECTED_STATUS_OPTION_ID_JSON" \
  --arg expectedState "$EXPECTED_STATE" \
  --argjson expectedMilestoneNumber "$EXPECTED_MILESTONE_NUMBER" \
  --argjson expectedMilestoneState "$EXPECTED_MILESTONE_STATE_JSON" \
  --argjson expectedBlockers "$EXPECTED_BLOCKERS_JSON" \
  --argjson expectedOpenBlockerCount "$EXPECTED_OPEN_BLOCKER_COUNT" \
  --argjson expectedBlockersComplete "$EXPECTED_BLOCKERS_COMPLETE" \
  --argjson activeMilestones "$ACTIVE_MILESTONES_JSON" \
  --argjson milestoneDependent "$MILESTONE_DEPENDENT" \
  --argjson blockerDependent "$BLOCKER_DEPENDENT" \
  --arg targetStatus "$TARGET_STATUS" \
  --arg expectedTitle "$EXPECTED_TITLE" \
  --argjson expectedAuthorLogin "$EXPECTED_AUTHOR_LOGIN_JSON" \
  --arg repoOwner "$REPO_OWNER" \
  --arg repoName "$REPO_NAME" '
   .items[] | select(.item_id == $itemId) as $item
   | if $item.number != $expectedIssueNumber
       or $item.repository_owner != $repoOwner
       or $item.repository_name != $repoName
       or $item.parent != null
       or $item.status != $expectedStatus
       or $item.status_option_id != $expectedStatusOptionId
       or $item.state != $expectedState
       or $item.title != $expectedTitle
       or $item.author_login != $expectedAuthorLogin
       or $item.milestone_number != $expectedMilestoneNumber
       or $item.milestone_state != $expectedMilestoneState
       or ($item.milestone_state != "OPEN" and $milestoneDependent)
       or ($milestoneDependent
         and ($item.milestone_number == null
        or ($activeMilestones
          | any(.[]; .number == $item.milestone_number) | not)))
       or ($item.blocked_by | length) != ($expectedBlockers | length)
        or ($item.blocked_by | sort_by(.number))
          != ($expectedBlockers | sort_by(.number))
       or $item.open_blocker_count != $expectedOpenBlockerCount
       or $item.blockers_complete != $expectedBlockersComplete
       or ($blockerDependent and $item.blockers_complete != true)
       or ($targetStatus == "Ready"
         and ($item.author_login == null
           or $item.author_login == "renovate[bot]"
           or $item.title == "Renovate Dependency Dashboard"))
     then error("project item changed; refresh the proposal")
     else $item
    end'
```

## Mutation

```bash
set -euo pipefail

: "${PROJECT_NODE_ID:?}"
: "${EXPECTED_ITEM_ID:?}"
: "${STATUS_FIELD_ID:?}"
: "${TARGET_OPTION_ID:?}"

gh api graphql \
  -F projectId="$PROJECT_NODE_ID" \
  -F itemId="$EXPECTED_ITEM_ID" \
  -F fieldId="$STATUS_FIELD_ID" \
  -F targetOptionId="$TARGET_OPTION_ID" \
  -f query='
mutation(
  $projectId: ID!,
  $itemId: ID!,
  $fieldId: ID!,
  $targetOptionId: String!
) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId
    itemId: $itemId
    fieldId: $fieldId
    value: { singleSelectOptionId: $targetOptionId }
  }) {
    projectV2Item { id }
  }
}' | jq -e --arg itemId "$EXPECTED_ITEM_ID" '
  if ((.errors // []) | length) > 0 then
    error("Project status mutation failed")
  elif .data.updateProjectV2ItemFieldValue.projectV2Item.id != $itemId then
    error("Project status mutation returned an unexpected item")
  else
    .data.updateProjectV2ItemFieldValue.projectV2Item
  end'
```

After a successful mutation, pipe fresh Step 2 JSON into this target check:

```bash
set -euo pipefail

: "${EXPECTED_ITEM_ID:?}"
: "${TARGET_OPTION_ID:?}"

jq -e --arg itemId "$EXPECTED_ITEM_ID" --arg targetOptionId "$TARGET_OPTION_ID" '
  .items[] | select(.item_id == $itemId)
  | if .status_option_id != $targetOptionId
    then error("status mutation did not reach the target option")
    else .
    end'
```

Report applied, failed, skipped, and review-only items separately.
