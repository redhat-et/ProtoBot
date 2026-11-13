# Fullsend GitHub setup diff behavior

## Dry-run and reconstruction contract

`fullsend github setup <owner/repo> --dry-run` is used for discovery under the
[upstream data trust boundary](../SKILL.md#upstream-data-trust-boundary).
The command returns before GitHub writes, and its output lists the files it
would commit plus their byte sizes. It does not print the file contents or a
unified diff.

The helper reconstructs repository-managed workflow files from the embedded
scaffold at the exact release tag reported by the active CLI. It currently
maps:

- `internal/scaffold/fullsend-repo/templates/shim-per-repo.yaml` becomes
  `.github/workflows/fullsend.yaml`.
- `internal/scaffold/fullsend-repo/.github/workflows/prioritize.yml` becomes
  `.github/workflows/prioritize.yml`.

The workflow templates receive the release commit SHA, release tag comment,
the `per-repo` install mode, and the compiled-in runner image. The helper reads
configuration and reference-comment formatting from the same release's
source, rather than assigning formatting rules to version ranges. It verifies
the reconstructed byte count against the CLI dry-run output before printing
a diff.

The `upgrade-fullsend` skill first checks the installed CLI against the
selected release and upgrades or isolates it when necessary. The CLI,
reconstructed templates, harness hashes, and workflow pins use the same
selected release rather than a fixed version in this skill.

Unsupported generated paths, rendering rules, or dry-run output contracts stop
the upgrade, as do reconstructed byte-count mismatches. Inspect the tagged setup
implementation, then make and review any needed renderer changes as repository
changes with regression coverage before resuming. Never fall back to an older
template, ignore a mismatch, or skip the report.

Before reading local workflow content, the helper resolves each managed path
under `--root` and rejects file or ancestor symlinks that escape that root.
Missing local workflows and symlinks that remain inside the root can still be
previewed.

## Generated workflow synchronization

The helper renders upstream templates only; it does not reconstruct
downstream/local workflow additions. Inspect every complete current-versus-
generated hunk for each managed workflow. Inventory the target repository's
local additions and customizations, identify which behavior remains intended,
and preserve each reviewed addition unless its removal is explicitly
approved. Never silently delete reviewed local behavior just because it is
absent from the generated content. Synchronize the upstream-generated content
to the selected release while retaining intended, reviewed downstream
additions.

For ProtoBot, preserve the following three repository-local `dispatch` `if`
customizations in `.github/workflows/fullsend.yaml`:

- Permit non-bot `issue_comment` events without the upstream `/fs-` prefix
  requirement.
- Exclude Renovate `pull_request_target` events only for review-starting
  actions: `opened`, `synchronize`, `ready_for_review`, or `labeled` with the
  `ready-for-review` label. Preserve other actions, including `closed`.
- Do not add a Renovate `pull_request_review` exclusion; the ProtoBot local
  workflow has none. The upstream `fullsend/scaffold-install` exclusion for
  both `pull_request_target` and `pull_request_review` remains generated
  content, not a downstream override.

Also preserve ProtoBot's local `stop-fix` job, which authorizes `/fs-fix-stop`
and applies `fullsend-no-fix`. These dispatch rules and additions are
ProtoBot-specific; do not apply the dispatch rules verbatim to other
repositories. For any other target, inventory and preserve its own intended
downstream behavior instead.

For ProtoBot, `.github/workflows/prioritize.yml` has no downstream override.
Inspect that file independently for every other target; do not assume the
ProtoBot state applies elsewhere.

## Settings report

The helper is report-only. It compares proposed repository variables with
`gh variable list`, scans generated GitHub `${{ ... }}` expressions for
`vars.NAME` and `secrets.NAME` references—including compound expressions—and
reports missing variable values and secret presence. It also includes
secret names proposed by setup. It never reads or changes secret values.
The [settings workflow](../SKILL.md#step-4-apply-confirmed-repository-settings)
shows concrete variable values and secret requirements, then lets the agent
apply only individually approved changes; the preview helper stays read-only.
Malformed variable or secret rows, unsupported settings headings, or missing
required settings sections cause a controlled failure (status `2`), not an
empty settings plan.

The setup output may include the non-setting footer
`Would remove stale vendored assets at .fullsend/bin/fullsend (if present)`
after the secrets list. The helper accepts only that exact footer in this
position and continues to print the raw output for review.

When the helper exits with status `3`, it reports a workflow difference or
unresolved repository settings. This is a report status: apply the mandatory
workflow and pin updates, and request approval only before changing repository
variables or secrets.

## Presenting repository settings decisions

Before any settings write, show a concise review table containing the explicit
target repository, each variable or secret name, current value or presence,
proposed value or credential source, reason, and action (`ADD`, `UPDATE`, or
no change).

For variables, show concrete current and proposed values when known. Resolve
values from the selected release, setup proposal, and repository configuration
first. If a required value is still unknown, explain its purpose and ask for
it; do not invent a default or request approval for an unresolved value.

For GitHub Actions secrets, show the required credential type, format, and
source, not plaintext. Existing secret values cannot be retrieved or compared;
do not assume a present secret needs rotation. Obtain a new value through an
approved secure file or an interactive local prompt, never by asking for
credentials in chat. Warn before replacing a secret because GitHub cannot
return its old value for rollback. Include any timing constraint needed to
keep active workflows working during the upgrade.

Show the proposed commands and ask the user to approve the named rows,
variable values, and secret sources. A confirmation covers only that plan;
changed values, targets, or credential sources require renewed approval.
When the helper exits with status `3`, review its workflow differences or
unresolved repository state. Status `3` does not permit skipping required
workflow or pin updates; ask for approval only for repository-setting changes.
Do not commit or merge generated workflow changes until required settings are
approved and applied, except for an explicitly approved staged-compatibility
plan with its dependency and timing documented.

## Applying approved settings

Use the same explicit `FULLSEND_TARGET_REPO` used for the setup preview. Set
`NAME`, `VALUE`, and `SECRET_FILE` as data from each approved row; do not build
shell source from proposed values or use `eval`. Skip unchanged, unresolved, or
unapproved rows.

For each approved repository variable, validate the name and target with the
same checks as the preview helper, then pass the value through standard input
so the shell never interprets it as command text:

```bash
set -euo pipefail
[[ "$NAME" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || {
  printf 'invalid repository setting name\n' >&2
  exit 1
}
[[ "$FULLSEND_TARGET_REPO" =~ ^[A-Za-z0-9_.][A-Za-z0-9_.-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$ ]] || {
  printf 'invalid GitHub owner/repository target\n' >&2
  exit 1
}
printf '%s' "$VALUE" | gh variable set "$NAME" --repo "$FULLSEND_TARGET_REPO"
```

For an approved GitHub Actions secret, apply the same name and target checks.
Use the approved private file outside the worktree, or run the same command
without redirection for a local interactive prompt:

```bash
set -euo pipefail
[[ "$NAME" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || {
  printf 'invalid repository setting name\n' >&2
  exit 1
}
[[ "$FULLSEND_TARGET_REPO" =~ ^[A-Za-z0-9_.][A-Za-z0-9_.-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$ ]] || {
  printf 'invalid GitHub owner/repository target\n' >&2
  exit 1
}
gh secret set "$NAME" --repo "$FULLSEND_TARGET_REPO" --app actions \
  < "$SECRET_FILE"
```

Keep secret contents out of command arguments, logs, chat, committed files, and
the review report. Never bulk-import an environment file or delete repository
settings merely because a workflow reference was removed.

After applying changes, re-read the target repository's settings:

```bash
gh variable list --repo "$FULLSEND_TARGET_REPO" --json name,value
gh secret list --repo "$FULLSEND_TARGET_REPO" --app actions \
  --json name,updatedAt
```

Verify variable values against the approved plan. For secrets, report the
write result and presence/update metadata; do not claim plaintext read-back
verification. Report applied, failed, and unapplied rows accurately, and stop
on a failed write rather than executing an unapproved fallback. Fullsend
setup still runs only with `--dry-run`; approval is not permission to replace
the repository's setup wholesale.
