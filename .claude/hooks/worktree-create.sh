#!/bin/bash
# WorktreeCreate hook — put Claude Code worktrees where AGENTS.md says.
#
# AGENTS.md: "Create worktrees in `.worktrees/`". Claude Code's built-in logic
# does not do that. Left alone it creates <repo>/.claude/worktrees/<name> on a
# branch named worktree-<name>, and `claude -w` takes a name only — there is no
# setting for the location (checked 2026-09-09, Claude Code 2.1.263).
#
# A WorktreeCreate hook replaces that built-in logic entirely, which is the
# documented way to choose the directory. This one applies the repository
# convention:
#
#   - the worktree lives at <repo>/.worktrees/<name>, which .gitignore covers
#   - the branch is named <name>            (`agent/58-foo` is a valid name)
#   - an existing worktree at that path is adopted, never recreated — that is
#     how two sessions share one, and how a hand-made worktree is used
#   - a new worktree branches from origin/<default branch>, after a fetch
#   - an existing branch named <name> is checked out instead of created
#
# stdin:  JSON — session_id, cwd, hook_event_name, name (the -w argument, or
#         agent-<hex> for a subagent that asked for worktree isolation)
# stdout: the worktree directory, one line; Claude Code starts the session there
# stderr: shown to the user
# exit:   non-zero aborts the session start
#
# Kept compatible with bash 3.2, which is what macOS ships.

set -eu

input=$(cat)
name=$(printf '%s' "$input" | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')
[ -n "$name" ] || { echo "worktree-create: empty worktree name" >&2; exit 1; }

# The main checkout. Even when launched from inside a linked worktree, the
# common git dir is always <main>/.git.
root=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
dir="$root/.worktrees/$name"

if [ -d "$dir" ]; then
  echo "worktree-create: adopting $dir" >&2
  printf '%s\n' "$dir"
  exit 0
fi

# Base: the remote default branch, fetched first so "fresh" means fresh.
# No remote, or origin/HEAD unknown: fall back to the main checkout's HEAD.
base=$(git -C "$root" symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null || true)
if [ -z "$base" ] && git -C "$root" rev-parse -q --verify refs/remotes/origin/main >/dev/null 2>&1; then
  base=origin/main
fi
if [ -n "$base" ]; then
  git -C "$root" fetch -q origin "${base#origin/}" 2>/dev/null \
    || echo "worktree-create: fetch failed, branching from the cached $base" >&2
else
  base=HEAD
fi

if git -C "$root" show-ref -q --verify "refs/heads/$name"; then
  git -C "$root" worktree add "$dir" "$name" >&2
  echo "worktree-create: created $dir on the existing branch $name" >&2
else
  git -C "$root" worktree add -b "$name" "$dir" "$base" >&2
  echo "worktree-create: created $dir on new branch $name from $base" >&2
fi

printf '%s\n' "$dir"
