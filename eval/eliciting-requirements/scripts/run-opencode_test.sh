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

make_workspace() {
  local dir="$1"
  mkdir -p "${dir}"
  printf '%s\n' "hidden reference" > "${dir}/reference.md"
  printf '%s\n' "hidden annotations" > "${dir}/annotations.yaml"
}

# ── System PATH without host bwrap ─────────────────────────────
# Prepare a PATH directory that mirrors system binaries but excludes bwrap.
# This ensures tests for the non-bwrap fallback never pick up host bwrap.
NO_BWRAP_DIR="${WORKDIR}/system-bin-no-bwrap"
mkdir -p "${NO_BWRAP_DIR}"
for d in /usr/bin /bin; do
  if [[ -d "${d}" ]]; then
    for p in "${d}"/*; do
      name="${p##*/}"
      if [[ "${name}" != "bwrap" && ! -e "${NO_BWRAP_DIR}/${name}" ]]; then
        ln -s "${p}" "${NO_BWRAP_DIR}/${name}" 2>/dev/null || true
      fi
    done
  fi
done

run_without_bwrap() {
  local bindir="$1"
  shift
  env PATH="${bindir}:${NO_BWRAP_DIR}" \
    HOME="${WORKDIR}/home" \
    bash "${RUNNER}" "$@"
}

run_with_bwrap_stub() {
  local bindir="$1"
  shift
  env PATH="${BIN_BWRAP}:${bindir}:${NO_BWRAP_DIR}" \
    HOME="${WORKDIR}/home" \
    bash "${RUNNER}" "$@"
}

# ── Fixtures ───────────────────────────────────────────────────
# Place mock binaries, log targets, and workspace under WS so all paths
# survive bubblewrap bind mounts (--bind "$workspace" "$workspace").

WS="${WORKDIR}/workspace"
BIN_V1="${WS}/bin-v1"
BIN_V2="${WS}/bin-v2"
BIN_V3="${WS}/bin-v3"
BIN_NOISY="${WS}/bin-noisy"
BIN_BAD="${WS}/bin-bad"
BIN_NONE="${WS}/bin-none"
BIN_BWRAP="${WS}/bin-bwrap"
LOG="${WS}/v2-args.log"
BWRAP_LOG="${WS}/bwrap.log"

mkdir -p "${BIN_V1}" "${BIN_V2}" "${BIN_V3}" "${BIN_NOISY}" "${BIN_BAD}" \
  "${BIN_NONE}" "${BIN_BWRAP}" "${WS}" "${WORKDIR}/home"
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

write_exec "${BIN_V3}/opencode" <<'MOCK'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "3.0.0"
  exit 0
fi
echo "v3 binary should not be invoked beyond --version" >&2
exit 92
MOCK

write_exec "${BIN_NOISY}/opencode" <<'MOCK'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  echo "2026-10-13 12:00:00 [info] loaded dependency 0.9.1" >&2
  echo "opencode 2.0.28"
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

write_exec "${BIN_BWRAP}/bwrap" <<'MOCK'
#!/bin/sh
if [ -n "${BWRAP_MOCK_LOG:-}" ]; then
  printf "%s\n" "$@" > "${BWRAP_MOCK_LOG}"
fi
while [ "$#" -gt 0 ]; do
  case "$1" in
    --chdir)
      shift
      cd "$1" || exit 1
      ;;
    --setenv)
      shift
      var="$1"
      shift
      val="$1"
      export "$var=$val"
      ;;
    --)
      shift
      exec "$@"
      ;;
  esac
  shift
done
echo "bwrap mock: no command found after --" >&2
exit 1
MOCK

# ── Tests ──────────────────────────────────────────────────────

echo "=== missing binary ==="
set +e
out="$(run_without_bwrap "${BIN_NONE}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
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
out="$(run_without_bwrap "${BIN_V1}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
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
out="$(run_without_bwrap "${BIN_BAD}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  pass "unparseable version exits non-zero"
else
  fail "unparseable version exited 0"
fi
assert_contains "unparseable names v2" "${out}" "OpenCode v2"

echo "=== v3.x binary (major != 2) ==="
set +e
out="$(run_without_bwrap "${BIN_V3}" "${WS}" "provider/model" "eliciting-requirements" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -ne 0 ]]; then
  pass "v3 binary exits non-zero"
else
  fail "v3 binary exited 0"
fi
assert_contains "v3 message names 3.0.0" "${out}" "3.0.0"
assert_contains "v3 message requires v2" "${out}" "rejects v1.x"

echo "=== noisy version stream ==="
rm -f "${LOG}"
make_workspace "${WS}"
export OPENCODE_MOCK_LOG="${LOG}"
set +e
out="$(run_without_bwrap "${BIN_NOISY}" "${WS}" "provider/model" "eliciting-requirements" "the prompt" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -eq 0 ]]; then
  pass "noisy version stream exits 0"
else
  fail "noisy version stream exited ${rc}"
  echo "  output:"
  indent "${out}"
fi
if [[ -f "${LOG}" ]]; then
  log="$(cat "${LOG}")"
  assert_contains "noisy version stream runs v2" "${log}" "PWD=${WS}"
else
  fail "noisy version mock did not write argv log"
fi

echo "=== v2 binary CLI flags and cwd (fallback without bwrap) ==="
rm -f "${LOG}"
make_workspace "${WS}"
export OPENCODE_MOCK_LOG="${LOG}"
set +e
out="$(run_without_bwrap "${BIN_V2}" "${WS}" "google-vertex/claude-sonnet-4-6@default" "eliciting-requirements" "the prompt" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -eq 0 ]]; then
  pass "v2 fallback exits 0"
else
  fail "v2 fallback exited ${rc}"
  echo "  output:"
  indent "${out}"
fi

if [[ -f "${LOG}" ]]; then
  log="$(cat "${LOG}")"
  assert_contains "v2 fallback cwd is workspace" "${log}" "PWD=${WS}"
  assert_contains "v2 fallback uses run" "${log}" "run"
  assert_contains "v2 fallback uses --standalone" "${log}" "--standalone"
  assert_contains "v2 fallback uses --format json" "${log}" "--format"
  assert_contains "v2 fallback uses --auto" "${log}" "--auto"
  assert_contains "v2 fallback uses --model" "${log}" "--model"
  assert_contains "v2 fallback invokes skill as slash command" "${log}" "/eliciting-requirements the prompt"
  assert_not_contains "v2 fallback does not pass --dir" "${log}" "--dir"
else
  fail "v2 fallback mock did not write argv log"
fi

if [[ -f "${WS}/reference.md" ]] && [[ -f "${WS}/annotations.yaml" ]]; then
  pass "v2 fallback answer keys restored"
else
  fail "v2 fallback answer keys were not restored"
fi

echo "=== v2 binary sandbox invocation (with stub bwrap) ==="
rm -f "${LOG}" "${BWRAP_LOG}"
make_workspace "${WS}"
export OPENCODE_MOCK_LOG="${LOG}"
export BWRAP_MOCK_LOG="${BWRAP_LOG}"
set +e
out="$(run_with_bwrap_stub "${BIN_V2}" "${WS}" "google-vertex/claude-sonnet-4-6@default" "eliciting-requirements" "the prompt" 2>&1)"
rc=$?
set -e
if [[ "${rc}" -eq 0 ]]; then
  pass "v2 bwrap stub exits 0"
else
  fail "v2 bwrap stub exited ${rc}"
  echo "  output:"
  indent "${out}"
fi

if [[ -f "${BWRAP_LOG}" ]]; then
  blog="$(cat "${BWRAP_LOG}")"
  assert_contains "bwrap uses --ro-bind / /" "${blog}" "--ro-bind"
  assert_contains "bwrap uses --tmpfs /tmp" "${blog}" "--tmpfs"
  assert_contains "bwrap binds workspace" "${blog}" "${WS}"
  assert_contains "bwrap uses --setenv PWD" "${blog}" "PWD"
  assert_contains "bwrap uses --chdir workspace" "${blog}" "--chdir"
  assert_contains "bwrap runs opencode" "${blog}" "opencode"
  assert_contains "bwrap runs standalone" "${blog}" "--standalone"
  assert_not_contains "bwrap does not pass --dir" "${blog}" "--dir"
else
  fail "bwrap mock did not write argv log"
fi

if [[ -f "${LOG}" ]]; then
  log="$(cat "${LOG}")"
  assert_contains "v2 bwrap cwd is workspace" "${log}" "PWD=${WS}"
  assert_contains "v2 bwrap uses run" "${log}" "run"
  assert_contains "v2 bwrap uses --standalone" "${log}" "--standalone"
  assert_contains "v2 bwrap invokes skill as slash command" "${log}" "/eliciting-requirements the prompt"
else
  fail "v2 bwrap mock did not write argv log"
fi

if [[ -f "${WS}/reference.md" ]] && [[ -f "${WS}/annotations.yaml" ]]; then
  pass "v2 bwrap answer keys restored"
else
  fail "v2 bwrap answer keys were not restored"
fi

echo
echo "Passed: ${PASS}  Failed: ${FAIL}"
if [[ "${FAIL}" -ne 0 ]]; then
  exit 1
fi
exit 0
