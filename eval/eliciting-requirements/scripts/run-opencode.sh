#!/usr/bin/env bash
set -euo pipefail

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

opencode_args=(
  run
  --dir "$workspace"
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
    --chdir "$workspace" \
    -- opencode "${opencode_args[@]}"
else
  opencode "${opencode_args[@]}"
fi
