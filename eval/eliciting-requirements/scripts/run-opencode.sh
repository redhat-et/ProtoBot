#!/usr/bin/env bash
# Run the eliciting-requirements skill through OpenCode v2.
#
# ProtoBot requires OpenCode v2 (@opencode/cli). This wrapper rejects a
# missing binary and any 1.x (or older) version before it writes config
# or invokes the CLI. v2 `run` has no `--dir` flag and uses cwd/PWD as
# the project directory; `--standalone` keeps each case off the v2
# background service.
set -euo pipefail

require_opencode_v2() {
  if ! command -v opencode >/dev/null 2>&1; then
    printf '%s\n' \
      "error: opencode is not on PATH." \
      "ProtoBot requires OpenCode v2 (npm i -g @opencode/cli)." \
      "v1.x is not supported." >&2
    exit 1
  fi

  local version_text version_string major
  version_text="$(opencode --version 2>&1)" || {
    printf '%s\n' \
      "error: opencode --version failed." \
      "ProtoBot requires OpenCode v2; v1.x is not supported." \
      "output: ${version_text}" >&2
    exit 1
  }

  if [[ "${version_text}" =~ ([0-9]+)\.[0-9]+\.[0-9]+ ]]; then
    version_string="${BASH_REMATCH[0]}"
    major="${BASH_REMATCH[1]}"
  else
    printf '%s\n' \
      "error: could not parse an OpenCode version from:" \
      "${version_text}" \
      "ProtoBot requires OpenCode v2; v1.x is not supported." >&2
    exit 1
  fi

  if (( major < 2 )); then
    printf '%s\n' \
      "error: OpenCode ${version_string} is not v2." \
      "ProtoBot requires OpenCode v2 and rejects v1.x." >&2
    exit 1
  fi
}

require_opencode_v2

workspace=${1:?workspace is required}
model=${2:?model is required}
agent=${3:?skill name is required}
args=${4-}
backup=$(mktemp -d)
data_dir="$workspace/.opencode-data"
mkdir -p "$data_dir"

restore_answer_keys() {
  rm -f "$workspace/opencode.json"
  for name in reference.md annotations.yaml; do
    if [[ -f "$backup/$name" ]]; then
      mv "$backup/$name" "$workspace/$name"
    fi
  done
  if [[ -f "$backup/opencode.json" ]]; then
    mv "$backup/opencode.json" "$workspace/opencode.json"
  fi
  rm -rf "$data_dir" "$backup"
}

trap restore_answer_keys EXIT

for name in reference.md annotations.yaml; do
  if [[ -f "$workspace/$name" ]]; then
    mv "$workspace/$name" "$backup/$name"
  fi
done

if [[ -f "$workspace/opencode.json" ]]; then
  mv "$workspace/opencode.json" "$backup/opencode.json"
fi

printf '%s\n' \
  '{' \
  '  "permission": {' \
  '    "read": {"*": "allow", "reference.md": "deny", "annotations.yaml": "deny"},' \
  '    "grep": {"*": "allow", "reference.md": "deny", "annotations.yaml": "deny"},' \
  '    "glob": {"*": "allow", "reference.md": "deny", "annotations.yaml": "deny"}' \
  '  }' \
  '}' > "$workspace/opencode.json"

# v2 `run` has no `--dir`; it takes the project directory from cwd/PWD.
# `--standalone` uses a private server instead of the v2 background service.
opencode_args=(
  run
  --standalone
  --model "$model"
  --format json
  --auto
  "/$agent $args"
)

if command -v bwrap >/dev/null 2>&1; then
  bwrap \
    --ro-bind / / \
    --tmpfs /tmp \
    --bind "$workspace" "$workspace" \
    --dev /dev \
    --proc /proc \
    --share-net \
    --setenv XDG_DATA_HOME "$data_dir" \
    --setenv PWD "$workspace" \
    --chdir "$workspace" \
    -- opencode "${opencode_args[@]}"
else
  (
    cd "$workspace"
    opencode "${opencode_args[@]}"
  )
fi
