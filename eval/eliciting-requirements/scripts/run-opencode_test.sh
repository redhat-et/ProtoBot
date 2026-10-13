#!/usr/bin/env bash
# Reproducible tests for run-opencode.sh OpenCode v2 gating and CLI flags.
#
# Usage:
#     bash eval/eliciting-requirements/scripts/run-opencode_test.sh
#
# Exit 0  = all assertions passed
# Exit 1  = at least one assertion failed
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
RUNNER="${REPO_ROOT}/eval/eliciting-requirements/scripts/run-opencode.sh"
PASS=0
FAIL=0
WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT INT TERM HUP

# ── Helpers ────────────────────────────────────────────────────

write_exec() {
  local path="$1"
  cat > "${path}"
  chmod +x "${path}"
}

indent() {
  local line
  while IFS= read -r line || [[ -n "${line}" ]]; do
    printf '    %s\n' "${line}"
  done <<< "$1"
}

fail() {
  echo "FAIL  $1"
  FAIL=$((FAIL + 1))
}

pass() {
  echo "PASS  $1"
  PASS=$((PASS + 1))
}

assert_contains() {
  local label="$1"
  local haystack="$2"
  local needle="$3"
  if printf '%s' "${haystack}" | grep -Fq -- "${needle}"; then
    pass "${label}"
  else
    fail "${label}: expected to contain '${needle}'"
    echo "  actual:"
    indent "${haystack}"
  fi
}

assert_not_contains() {
  local label="$1"
  local haystack="$2"
  local needle="$3"
  if printf '%s' "${haystack}" | grep -Fq -- "${needle}"; then
    fail "${label}: did not expect '${needle}'"
    echo "  actual:"
    indent "${haystack}"
  else
    pass "${label}"
  fi
}

run_with_path() {
  local bindir="$1"
  shift
  env PATH="${bindir}:/usr/bin:/bin" \
    HOME="${WORKDIR}/home" \
    bash "${RUNNER}" "$@"
}

make_workspace() {
  local dir="$1"
  mkdir -p "${dir}"
  printf '%s\n' "hidden reference" > "${dir}/reference.md"
  printf '%s\n' "hidden annotations" > "${dir}/annotations.yaml"
}

# ── Fixtures ───────────────────────────────────────────────────

BIN_V1="${WORKDIR}/bin-v1"
BIN_V2="${WORKDIR}/bin-v2"
BIN_BAD="${WORKDIR}/bin-bad"
BIN_NONE="${WORKDIR}/bin-none"
WS="${WORKDIR}/workspace"
mkdir -p "${BIN_V1}" "${BIN_V2}" "${BIN_BAD}" "${BIN_NONE}" "${WS}" \
  "${WORKDIR}/home"
make_workspace "${WS}"

write_exec "${BIN_V1}/opencode" <<'MOCK'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "1.18.30"
  exit 0
fi
echo "v1 binary should not be invoked beyond --version" >&2
exit 90
MOCK

write_exec "${BIN_V2}/opencode" <<'MOCK'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "2.0.28"
  exit 0
fi
printf "%s\n" "PWD=${PWD}" > "${OPENCODE_MOCK_LOG}"
printf "%s\n" "$@" >> "${OPENCODE_MOCK_LOG}"
exit 0
MOCK

write_exec "${BIN_BAD}/opencode" <<'MOCK'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "not-a-version"
  exit 0
fi
exit 91
MOCK

# ── Tests ──────────────────────────────────────────────────────

echo "=== missing binary ==="
set +e
out="$(run_with_path "${BIN_NONE}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  pass "missing binary exits non-zero"
else
  fail "missing binary exited 0"
fi
assert_contains "missing binary names v2" "${out}" "OpenCode v2"
assert_contains "missing binary rejects v1.x" "${out}" "v1.x"

echo "=== v1.x binary ==="
set +e
out="$(run_with_path "${BIN_V1}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  pass "v1 binary exits non-zero"
else
  fail "v1 binary exited 0"
fi
assert_contains "v1 message names 1.18.30" "${out}" "1.18.30"
assert_contains "v1 message requires v2" "${out}" "rejects v1.x"

echo "=== unparseable version ==="
set +e
out="$(run_with_path "${BIN_BAD}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  pass "unparseable version exits non-zero"
else
  fail "unparseable version exited 0"
fi
assert_contains "unparseable names v2" "${out}" "OpenCode v2"

echo "=== v2 binary CLI flags and cwd ==="
LOG="${WORKDIR}/v2-args.log"
export OPENCODE_MOCK_LOG="${LOG}"
set +e
out="$(run_with_path "${BIN_V2}" "${WS}" "google-vertex/claude-sonnet-4-6@default" "eliciting-requirements" "the prompt" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -eq 0 ]]; then
  pass "v2 binary exits 0"
else
  fail "v2 binary exited ${rc}"
  echo "  output:"
  indent "${out}"
fi

if [[ -f "${LOG}" ]]; then
  log="$(cat "${LOG}")"
  assert_contains "v2 cwd is workspace" "${log}" "PWD=${WS}"
  assert_contains "v2 uses run" "${log}" "run"
  assert_contains "v2 uses --standalone" "${log}" "--standalone"
  assert_contains "v2 uses --format json" "${log}" "--format"
  assert_contains "v2 uses --auto" "${log}" "--auto"
  assert_contains "v2 uses --model" "${log}" "--model"
  assert_contains "v2 invokes skill as slash command" "${log}" "/eliciting-requirements the prompt"
  assert_not_contains "v2 does not pass --dir" "${log}" "--dir"
else
  fail "v2 mock did not write argv log"
fi

if [[ -f "${WS}/reference.md" ]] && [[ -f "${WS}/annotations.yaml" ]]; then
  pass "answer keys restored"
else
  fail "answer keys were not restored"
fi

echo
echo "Passed: ${PASS}  Failed: ${FAIL}"
if [[ "${FAIL}" -ne 0 ]]; then
  exit 1
fi
exit 0
