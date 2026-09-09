#!/bin/bash
# WorktreeRemove hook — the pair of worktree-create.sh.
#
# Claude Code calls it when the user picks "remove" at the end of a `claude -w`
# session. Because worktree-create.sh moved the directory, the built-in remove
# would look in the wrong place, so this hook removes it instead.
#
# It removes the worktree directory and nothing else. The branch stays, because
# the branch is the pull request. Deleting a branch is the user's call, in the
# main checkout, after the merge.
#
# Two refusals, on purpose:
#   - a path that is not under <repo>/.worktrees/ — never remove anything else
#   - a worktree git reports as locked — another session is still working in
#     it, so removing it would pull the floor out from under that session
#
# Not covered (checked 2026-09-09, Claude Code 2.1.263): a subagent worktree
# (`.worktrees/agent-<hex>`) is created through the hook but no remove call
# follows when the subagent finishes, and the periodic sweep skips worktrees it
# did not create itself. Prune those by hand: `git worktree list`, then
# `git worktree remove .worktrees/agent-...`.
#
# stdin: JSON with the worktree path (`path`; `cwd` as a fallback)
#
# Kept compatible with bash 3.2, which is what macOS ships.

set -eu

input=$(cat)
path=$(printf '%s' "$input" | python3 -c '
import json, sys
d = json.load(sys.stdin)
print(d.get("path") or d.get("worktree_path") or d.get("cwd") or "")')
[ -n "$path" ] || { echo "worktree-remove: no worktree path in the hook input" >&2; exit 1; }

root=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
case "$path" in
  "$root"/.worktrees/?*) ;;
  *) echo "worktree-remove: refusing — $path is not under $root/.worktrees/" >&2; exit 1 ;;
esac

if git -C "$root" worktree list --porcelain | grep -A3 -F "worktree $path" | grep -q '^locked'; then
  echo "worktree-remove: $path is locked — another session is still in it. Left in place." >&2
  exit 1
fi

git -C "$root" worktree remove --force "$path" >&2
git -C "$root" worktree prune
# A nested name (agent/58-foo) leaves an empty parent folder behind.
rmdir "$(dirname "$path")" 2>/dev/null || true
echo "worktree-remove: removed $path (branch kept)" >&2
