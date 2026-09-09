#!/usr/bin/env bash
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
# Four refusals, on purpose:
#   - a path that is not under <repo>/.worktrees/ — never remove anything else
#   - a path git does not report as a working tree — a stray directory is not
#     this hook's to delete
#   - a worktree git reports as locked — another session is still working in
#     it, so removing it would pull the floor out from under that session
#   - a worktree with uncommitted changes to tracked files — those exist only
#     there, and removal would destroy them with no way back
#
# Untracked files do not block removal. A worktree almost always collects build
# output and virtual environments, and refusing on those would mean the hook
# never removes anything.
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

set -euo pipefail

# Resolve symlinks and `..` so the containment check below compares real paths.
canon() {
  python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$1"
}

input=$(cat)
path=$(printf '%s' "$input" | python3 -c '
import json, sys
d = json.load(sys.stdin)
print(d.get("path") or d.get("worktree_path") or d.get("cwd") or "")') || {
  echo "worktree-remove: cannot read the worktree path from the hook input" >&2
  exit 1
}
[ -n "$path" ] || { echo "worktree-remove: no worktree path in the hook input" >&2; exit 1; }

root=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")

# Compare canonical forms, so a path such as .worktrees/../../elsewhere cannot
# satisfy the prefix. git keeps working with the path it was given.
root_real=$(canon "$root")
path_real=$(canon "$path")
case "$path_real" in
  "$root_real"/.worktrees/?*) ;;
  *)
    echo "worktree-remove: refusing — $path is not under $root/.worktrees/" >&2
    exit 1
    ;;
esac

# Ask git for this worktree's own admin directory and look for the lock file
# there. `git worktree lock` writes <common>/worktrees/<id>/locked, and that is
# what `git worktree list --porcelain` reports as `locked`. Reading it directly
# avoids matching paths in that output, which is wrong twice over: a substring
# match also hits a worktree whose name merely starts the same, and the path
# git stored need not be spelled the way this hook receives it.
admin=$(git -C "$path" rev-parse --path-format=absolute --git-dir 2>/dev/null || true)
if [ -z "$admin" ]; then
  echo "worktree-remove: refusing — git does not report $path as a working tree" >&2
  exit 1
fi
if [ -f "$admin/locked" ]; then
  echo "worktree-remove: $path is locked — another session is still in it. Left in place." >&2
  exit 1
fi

if [ -n "$(git -C "$path" status --porcelain --untracked-files=no 2>/dev/null)" ]; then
  echo "worktree-remove: $path has uncommitted changes to tracked files. Left in" \
       "place. Commit them, or remove it yourself with:" \
       "git -C $root worktree remove --force $path" >&2
  exit 1
fi

git -C "$root" worktree remove --force "$path" >&2
git -C "$root" worktree prune
# A nested name (agent/58-foo) leaves an empty parent folder behind. Never the
# .worktrees/ directory itself, which is the convention this hook exists for.
parent=$(dirname "$path_real")
if [ "$parent" != "$root_real/.worktrees" ]; then
  rmdir "$parent" 2>/dev/null || true
fi
echo "worktree-remove: removed $path (branch kept)" >&2
