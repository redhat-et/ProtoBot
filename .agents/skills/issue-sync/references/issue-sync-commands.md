# Issue Sync Command Templates

Run the lookup and mutation blocks in the same shell with
`set -euo pipefail`.

## Dependency Lookup

Provide the approved relationships once as issue-number pairs. The lookup
derives a deduplicated endpoint list, fails closed, and preserves the
issue-number-to-node-ID map:

```bash
set -euo pipefail

RELATIONSHIPS_FILE="$(mktemp)"
cat >"$RELATIONSHIPS_FILE" <<'RELATIONSHIPS'
<BLOCKED_ISSUE_NUMBER> <BLOCKING_ISSUE_NUMBER>
RELATIONSHIPS

ISSUE_NUMBERS=()
declare -A SEEN_ISSUES=()
while read -r BLOCKED_ISSUE_NUMBER BLOCKING_ISSUE_NUMBER; do
  [ -n "$BLOCKED_ISSUE_NUMBER" ] || continue
  for ISSUE in "$BLOCKED_ISSUE_NUMBER" "$BLOCKING_ISSUE_NUMBER"; do
    if [[ -z "${SEEN_ISSUES[$ISSUE]+x}" ]]; then
      ISSUE_NUMBERS+=("$ISSUE")
      SEEN_ISSUES[$ISSUE]=1
    fi
  done
done <"$RELATIONSHIPS_FILE"
if ((${#ISSUE_NUMBERS[@]} == 0)); then
  printf 'provide at least one relationship\n' >&2
  exit 1
fi

ISSUE_LOOKUPS='[]'
for ISSUE in "${ISSUE_NUMBERS[@]}"; do
  RESULT="$(
    gh api graphql \
      -F owner="<REPO_OWNER>" \
      -F repo="<REPO_NAME>" \
      -F number="$ISSUE" \
      -f query='
    query($owner: String!, $repo: String!, $number: Int!) {
      repository(owner: $owner, name: $repo) {
        issue(number: $number) { id number title state }
      }
    }'
  )"
  LOOKUP="$(jq -e '
    if ((.errors // []) | length) > 0 then
      error("GraphQL lookup failed")
    elif .data.repository.issue == null then
      error("Issue was not found")
    else
      .data.repository.issue | {
        node_id: .id,
        number,
        title,
        state
      }
    end
  ' <<<"$RESULT")"
  ISSUE_LOOKUPS="$(jq -n -e \
    --argjson entries "$ISSUE_LOOKUPS" \
    --argjson lookup "$LOOKUP" \
    '$entries + [$lookup]')"
done

ISSUE_NODE_IDS="$(jq -e '
  map({key: (.number | tostring),
    value: {node_id, number, title, state}}) | from_entries
' <<<"$ISSUE_LOOKUPS")"
```

## Dependency Mutation

Provide relationships as issue numbers. The loop resolves both endpoints from
the map, rejects self-blocking relationships, skips only duplicate/cycle
errors, and aborts on every other failure:

```bash
set -euo pipefail

RESULT_FILE="$(mktemp)"
ERROR_FILE="$(mktemp)"
trap 'rm -f "$RELATIONSHIPS_FILE" "$RESULT_FILE" "$ERROR_FILE"' EXIT

while read -r BLOCKED_ISSUE_NUMBER BLOCKING_ISSUE_NUMBER; do
  if [ "$BLOCKED_ISSUE_NUMBER" = "$BLOCKING_ISSUE_NUMBER" ]; then
    printf 'refusing self-blocking relationship: #%s\n' \
      "$BLOCKED_ISSUE_NUMBER" >&2
    exit 1
  fi

  BLOCKED_ISSUE_NODE_ID="$(jq -er \
    --arg number "$BLOCKED_ISSUE_NUMBER" \
    '.[$number].node_id // error("blocked issue is not in the lookup map")' \
    <<<"$ISSUE_NODE_IDS")"
  BLOCKING_ISSUE_NODE_ID="$(jq -er \
    --arg number "$BLOCKING_ISSUE_NUMBER" \
    '.[$number].node_id // error("blocking issue is not in the lookup map")' \
    <<<"$ISSUE_NODE_IDS")"

  set +e
  gh api graphql \
      -F blockedIssueId="$BLOCKED_ISSUE_NODE_ID" \
      -F blockingIssueId="$BLOCKING_ISSUE_NODE_ID" \
      -f query='
    mutation($blockedIssueId: ID!, $blockingIssueId: ID!) {
      addBlockedBy(input: {
        issueId: $blockedIssueId
        blockingIssueId: $blockingIssueId
      }) {
        clientMutationId
      }
    }' >"$RESULT_FILE" 2>"$ERROR_FILE"
  STATUS=$?
  set -e
  RESULT="$(<"$RESULT_FILE")"
  STDERR="$(<"$ERROR_FILE")"

  if [ "$STATUS" -ne 0 ] && ! jq -e \
    'type == "object" and ((.errors // []) | length) > 0' \
    <<<"$RESULT" >/dev/null 2>&1; then
    printf '%s\n%s\n' "$RESULT" "$STDERR" >&2
    exit "$STATUS"
  fi

  if jq -e '
    ((.errors // []) | length) == 0
    and .data.addBlockedBy != null
  ' <<<"$RESULT" >/dev/null; then
    printf 'applied: #%s -> #%s\n' \
      "$BLOCKED_ISSUE_NUMBER" "$BLOCKING_ISSUE_NUMBER"
    continue
  fi

  ERROR_KIND="$(jq -er '
    if ((.errors // []) | length) == 0 then
      "unexpected"
    elif all(.errors[]; .message == "Target issue has already been taken") then
      "duplicate"
    elif all(.errors[]; .message == "this dependency would create a cycle") then
      "cycle"
    else
      "unexpected"
    end
  ' <<<"$RESULT")" || ERROR_KIND="unexpected"
  case "$ERROR_KIND" in
    duplicate)
      printf 'skipped (already exists): #%s -> #%s\n' \
        "$BLOCKED_ISSUE_NUMBER" "$BLOCKING_ISSUE_NUMBER"
      ;;
    cycle)
      printf 'skipped (cycle): #%s -> #%s\n' \
        "$BLOCKED_ISSUE_NUMBER" "$BLOCKING_ISSUE_NUMBER"
      ;;
    *)
      printf '%s\n%s\n' "$RESULT" "$STDERR" >&2
      exit 1
      ;;
  esac
done <"$RELATIONSHIPS_FILE"
```

There is no standard MCP equivalent for `addBlockedBy`.
