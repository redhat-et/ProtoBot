# Issue Audit Read Templates

Use `set -euo pipefail` for every block. Replace `<OWNER>`, `<REPO>`, and
other placeholders with values from the repository and audit scope.

## Milestones

```bash
set -euo pipefail
MILESTONES="$(gh api --method GET --paginate --slurp \
  "repos/<OWNER>/<REPO>/milestones?state=all&per_page=100")"
jq -e 'if type != "array" or any(.[]; type != "array")
  then error("milestone response is not a paginated array")
  else (add) as $milestones
    | if any($milestones[];
        (.number | type) != "number"
        or (.title | type) != "string"
        or (.state as $state | ($state != "open" and $state != "closed"))
        or (.due_on != null and (.due_on | type) != "string"))
      then error("milestone response contains malformed records")
      else $milestones | map({number, title, state, due_on})
      end
  end' <<<"$MILESTONES"
```

## Open Issues

```bash
set -euo pipefail
ISSUES="$(gh api --method GET --paginate --slurp \
  "repos/<OWNER>/<REPO>/issues?state=open&per_page=100")"
jq -e 'if type != "array" or any(.[]; type != "array")
  then error("issue response is not a paginated array")
  else (add) as $issues
    | if any($issues[];
        (.number | type) != "number"
        or (.title | type) != "string"
        or (.body != null and (.body | type) != "string")
        or (.pull_request != null and (.pull_request | type) != "object")
        or ((.labels | type) != "array"
          or any(.labels[]; (.name | type) != "string"))
        or (.milestone != null
          and ((.milestone.number | type) != "number"
            or (.milestone.title | type) != "string")))
      then error("issue response contains malformed records")
      else [$issues[] | select(.pull_request == null) | {
        number,
        title,
        labels: [(.labels // [])[] | .name],
        body,
        milestone: (.milestone // {} | {number, title})
      }]
      end
  end' <<<"$ISSUES"
```

## Open Pull Requests

List every open PR:

```bash
set -euo pipefail
PRS="$(gh api graphql --paginate --slurp \
  -F owner="<OWNER>" \
  -F repo="<REPO>" \
  -f query='
query($owner: String!, $repo: String!, $endCursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(first: 100, states: OPEN, after: $endCursor) {
      nodes { number title body headRefName }
      pageInfo { hasNextPage endCursor }
    }
  }
}')"
jq -e 'if type != "array"
  or any(.[]; ((.errors // []) | length) > 0
    or .data.repository.pullRequests == null
    or (.data.repository.pullRequests.nodes | type) != "array"
    or (.data.repository.pullRequests.pageInfo.hasNextPage | type)
      != "boolean"
    or (.data.repository.pullRequests.pageInfo.hasNextPage
      and (.data.repository.pullRequests.pageInfo.endCursor | type)
        != "string")
    or any(.data.repository.pullRequests.nodes[];
      (.number | type) != "number"
      or (.title | type) != "string"
      or (.body != null and (.body | type) != "string")))
  or length == 0
  or .[-1].data.repository.pullRequests.pageInfo.hasNextPage != false
  then error("pull request response is incomplete or has GraphQL errors")
  else [.[].data.repository.pullRequests.nodes[]]
  end' <<<"$PRS"
```

Read every PR's description and files:

```bash
set -euo pipefail
PR_FILES="$(gh api graphql --paginate --slurp \
  -F owner="<OWNER>" \
  -F repo="<REPO>" \
  -F number="<PR_NUMBER>" \
  -f query='
query(
  $owner: String!,
  $repo: String!,
  $number: Int!,
  $endCursor: String
) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      title
      body
      files(first: 100, after: $endCursor) {
        nodes { path additions deletions changeType }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}')"
jq -e 'if type != "array"
  or any(.[]; ((.errors // []) | length) > 0
    or .data.repository.pullRequest.files == null
    or (.data.repository.pullRequest.title | type) != "string"
    or (.data.repository.pullRequest.body != null
      and (.data.repository.pullRequest.body | type) != "string")
    or (.data.repository.pullRequest.files.nodes | type) != "array"
    or (.data.repository.pullRequest.files.pageInfo.hasNextPage | type)
      != "boolean"
    or (.data.repository.pullRequest.files.pageInfo.hasNextPage
      and (.data.repository.pullRequest.files.pageInfo.endCursor | type)
        != "string")
    or any(.data.repository.pullRequest.files.nodes[];
      (.path | type) != "string"
      or (.additions | type) != "number"
      or (.deletions | type) != "number"
      or (.changeType | type) != "string"))
  or length == 0
  or .[-1].data.repository.pullRequest.files.pageInfo.hasNextPage != false
  then error("pull request file response is incomplete or has GraphQL errors")
  else {
    title: .[-1].data.repository.pullRequest.title,
    body: .[-1].data.repository.pullRequest.body,
    files: [.[].data.repository.pullRequest.files.nodes[]]
  }
  end' <<<"$PR_FILES"
```

## Closed Issues

```bash
set -euo pipefail
CLOSED_ISSUES="$(gh api --method GET --paginate --slurp \
  "repos/<OWNER>/<REPO>/issues?state=closed&per_page=100")"
jq -e --argjson milestoneNumber "<MILESTONE_NUMBER>" \
  'if type != "array" or any(.[]; type != "array")
    then error("closed-issue response is not a paginated array")
    else (add) as $issues
      | if any($issues[];
          (.number | type) != "number"
          or (.title | type) != "string"
          or (.state != null and (.state | type) != "string")
          or (.pull_request != null
            and (.pull_request | type) != "object")
          or (.milestone != null
            and ((.milestone.number | type) != "number"
              or (.milestone.title | type) != "string")))
        then error("closed-issue response contains malformed records")
        else [$issues[]
          | select(.pull_request == null)
          | select((.milestone // {}).number == $milestoneNumber)
          | {number, title}]
        end
    end' <<<"$CLOSED_ISSUES"
```
