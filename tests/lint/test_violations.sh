#!/usr/bin/env bash
# tests/lint/test_violations.sh
#
# Contract tests for the scripts/lint.py wrapper around `pre-commit`.
#
# This suite intentionally does NOT verify that individual third-party
# tools (ruff, yamllint, shellcheck, markdownlint, detect-secrets,
# zizmor, skillsaw, ...) catch specific violations. scripts/lint.py no
# longer re-implements those tools or their dispatch; it is a thin
# wrapper that runs the real `pre-commit` once per configuration, so
# tool-level correctness is CI's responsibility via `pre-commit run
# -a` against the genuine hook repositories. This suite instead
# verifies the wrapper's own contract:
#
#   * the offline cache pre-flight fails closed with an actionable
#     message when a hook repository/revision is missing, and is
#     skipped entirely with --online;
#   * --check-parity validates both configurations
#     (.pre-commit-config.yaml and .github/pre-commit-ci.yaml; a
#     missing CI configuration is merge skew and is skipped) and the
#     cache without running any hooks;
#   * --all-files / --files / bare invocation translate to one
#     `pre-commit run --config <path> <scope>` invocation per existing
#     configuration, including the changed-vs-HEAD (not staged-only)
#     default scope;
#   * ZIZMOR_NO_ONLINE_AUDITS and PRE_COMMIT_HOME are exported
#     correctly for offline and --online runs;
#   * this repository's own `repo: local` `language: system` hooks
#     still run correctly through the full wrapper -> pre-commit ->
#     local-hook chain once the cache is warm. gofmt and go-vet moved
#     to .github/pre-commit-ci.yaml in the ADR-0005 cutover, so their
#     wiring is asserted through the CI configuration invocation;
#     check-portable-null-device stays in the gate.
#
# The last point uses tests/lint/build_fake_pre_commit_cache.py to
# fabricate a structurally-valid (but non-functional) cache for every
# hook repository and local environment in both configurations, so
# pre-commit can load every repo's manifest (which it always does,
# regardless of which files are given) without network access, while
# the real local system hooks run unmodified.
#
# Usage:
#     bash tests/lint/test_violations.sh
#
# Exit 0  = all assertions passed
# Exit 1  = at least one assertion failed

set -euo pipefail

# -P matches scripts/lint.py's Path(__file__).resolve(), so the argv
# assertions can compare the absolute --config paths it passes.
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"
LINT="${REPO_ROOT}/scripts/lint.py"
FAKE_CACHE_BUILDER="${REPO_ROOT}/tests/lint/build_fake_pre_commit_cache.py"
PASS=0
FAIL=0

# The wrapper runs one pre-commit invocation per existing
# configuration, gate first. The stub capture holds the LAST
# invocation, which is the CI configuration once the cutover lands;
# on merge-skew trees (no .github/pre-commit-ci.yaml) it is the gate.
LAST_CONFIG=".github/pre-commit-ci.yaml"
if [[ ! -f "${REPO_ROOT}/${LAST_CONFIG}" ]]; then
    LAST_CONFIG=".pre-commit-config.yaml"
fi
LAST_CONFIG_PATH="${REPO_ROOT}/${LAST_CONFIG}"

WORKDIR="$(mktemp -d)"
EMPTY_CACHE="${WORKDIR}/empty-cache"
WARM_CACHE="${WORKDIR}/warm-cache"
STUB_BIN="${WORKDIR}/stub-bin"
STUB_CAPTURE="${WORKDIR}/stub-capture"

# Track fixture files created in the repo so they are cleaned up even
# if the script is interrupted.
CREATED_FILES=()
STAGED_FILES=()
CLEANUP_DONE=false

cleanup() {
    # A signal trap runs cleanup and exits; the EXIT trap then fires
    # again. Run the body exactly once so fixtures and the git index
    # are restored a single time on every path.
    if [[ "${CLEANUP_DONE}" == true ]]; then
        return 0
    fi
    CLEANUP_DONE=true
    for f in "${STAGED_FILES[@]}"; do
        git -C "${REPO_ROOT}" restore --staged -- "${f}" 2>/dev/null || true
    done
    for f in "${CREATED_FILES[@]}"; do
        rm -f "${f}"
    done
    rm -rf "${WORKDIR}"
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 129' HUP
trap 'cleanup; exit 143' TERM

cd "${REPO_ROOT}"

if ! command -v pre-commit >/dev/null 2>&1; then
    echo "ERROR: pre-commit is not installed; cannot exercise the wrapper contract"
    exit 1
fi

python3 "${FAKE_CACHE_BUILDER}" "${REPO_ROOT}" "${WARM_CACHE}"

# A stub `pre-commit` placed ahead of the real one on PATH. Records the
# argv it was called with (one per line) and the environment variables
# the wrapper is responsible for, then exits with $STUB_EXIT_CODE.
mkdir -p "${STUB_BIN}"
cat > "${STUB_BIN}/pre-commit" <<'STUB'
#!/usr/bin/env python3
import os
import sys

capture_dir = os.environ["STUB_CAPTURE_DIR"]
with open(os.path.join(capture_dir, "argv"), "w") as fh:
    fh.write("\n".join(sys.argv) + "\n")
with open(os.path.join(capture_dir, "env"), "w") as fh:
    fh.write("ZIZMOR_NO_ONLINE_AUDITS=" + os.environ.get("ZIZMOR_NO_ONLINE_AUDITS", "<unset>") + "\n")
    fh.write("PRE_COMMIT_HOME=" + os.environ.get("PRE_COMMIT_HOME", "<unset>") + "\n")
sys.exit(int(os.environ.get("STUB_EXIT_CODE", "0")))
STUB
chmod +x "${STUB_BIN}/pre-commit"

# ── Helpers ────────────────────────────────────────────────────

pass_test() {
    echo "PASS  $1"
    PASS=$((PASS + 1))
}

fail_test() {
    echo "FAIL  $1: $2"
    FAIL=$((FAIL + 1))
}

# Run lint.py against the *real* configurations with an empty cache
# directory (no db.db at all), network assumed unavailable.
run_with_empty_cache() {
    PRE_COMMIT_HOME="${EMPTY_CACHE}" env -u CI python3 "${LINT}" "$@" 2>&1
}

# Run lint.py against the real configurations through the stub
# `pre-commit`, with the fake warm cache so the pre-flight passes.
run_with_stub() {
    rm -rf "${STUB_CAPTURE}"
    mkdir -p "${STUB_CAPTURE}"
    local rc=0
    PATH="${STUB_BIN}:${PATH}" \
        PRE_COMMIT_HOME="${WARM_CACHE}" \
        STUB_CAPTURE_DIR="${STUB_CAPTURE}" \
        env -u CI python3 "${LINT}" "$@" >/dev/null 2>&1 || rc=$?
    return "${rc}"
}

captured_argv() {
    cat "${STUB_CAPTURE}/argv" 2>/dev/null
}

captured_env() {
    cat "${STUB_CAPTURE}/env" 2>/dev/null
}

# ── Cache pre-flight ─────────────────────────────────────────────

echo "── Cache pre-flight (offline) ────────────────────────────"

preflight_rc=0
preflight_output="$(run_with_empty_cache --check-parity)" || preflight_rc=$?
missing_repos_ok=true
while IFS= read -r repo_url; do
    [[ -z "${repo_url}" ]] && continue
    if ! echo "${preflight_output}" | grep -qF "${repo_url}"; then
        missing_repos_ok=false
    fi
# The missing-revision report must name every non-local repository
# from BOTH configuration files; a file that does not exist is merge
# skew and is skipped.
done < <(python3 - "${REPO_ROOT}" <<'PY'
import sys
from pathlib import Path

import yaml

root = Path(sys.argv[1])
for name in (".pre-commit-config.yaml", ".github/pre-commit-ci.yaml"):
    config_path = root / name
    if not config_path.exists():
        continue
    with open(config_path) as fh:
        config = yaml.safe_load(fh)
    for entry in config.get("repos", []):
        repo = entry.get("repo", "")
        if repo not in ("local", "meta"):
            print(repo)
PY
)

if [[ "${preflight_rc}" -ne 0 ]] \
    && echo "${preflight_output}" | grep -qF "pre-commit install-hooks" \
    && ${missing_repos_ok}; then
    pass_test "cache-miss: --check-parity fails closed and names every missing repo + bootstrap command"
else
    fail_test "cache-miss" "expected non-zero exit naming all missing repos and the bootstrap command (exit ${preflight_rc})"
    echo "${preflight_output}" | tail -10 | sed 's/^/    /'
fi

online_parity_rc=0
online_parity_output="$(run_with_empty_cache --check-parity --online)" || online_parity_rc=$?
if [[ "${online_parity_rc}" -eq 0 ]] \
    && echo "${online_parity_output}" | grep -qF "PARITY CHECK PASSED"; then
    pass_test "--check-parity --online skips the cache gate and validates the config"
else
    fail_test "check-parity-online" "expected exit 0 with PARITY CHECK PASSED (exit ${online_parity_rc})"
    echo "${online_parity_output}" | tail -10 | sed 's/^/    /'
fi

warm_parity_rc=0
warm_parity_output="$(PRE_COMMIT_HOME="${WARM_CACHE}" env -u CI python3 "${LINT}" --check-parity)" || warm_parity_rc=$?
if [[ "${warm_parity_rc}" -eq 0 ]] \
    && echo "${warm_parity_output}" | grep -qF "PARITY CHECK PASSED"; then
    pass_test "--check-parity passes offline once every configured repo/revision is cached"
else
    fail_test "check-parity-warm" "expected exit 0 with PARITY CHECK PASSED (exit ${warm_parity_rc})"
    echo "${warm_parity_output}" | tail -10 | sed 's/^/    /'
fi

# ── CLI argument mapping ─────────────────────────────────────────

echo ""
echo "── CLI argument mapping ──────────────────────────────────"

# argv[0] is rewritten by the kernel to the stub's own resolved path
# when it is invoked through its `#!/usr/bin/env python3` shebang, so
# these assertions check argv[1:] (the arguments lint.py chose), not
# the program name lint.py originally passed. The stub capture holds
# the LAST invocation, so these assert the CI configuration's argv
# (the gate configuration runs first with the same scope arguments).
trailing_argv() {
    captured_argv | tail -n +2
}

run_with_stub --all-files
if [[ "$(trailing_argv)" == $'run\n--config\n'"${LAST_CONFIG_PATH}"$'\n--all-files' ]]; then
    pass_test "--all-files maps to \`pre-commit run --config <config> --all-files\` per configuration"
else
    fail_test "all-files-mapping" "unexpected argv: $(trailing_argv | tr '\n' ' ')"
fi

run_with_stub --files a.py b.yaml
if [[ "$(trailing_argv)" == $'run\n--config\n'"${LAST_CONFIG_PATH}"$'\n--files\na.py\nb.yaml' ]]; then
    pass_test "--files a.py b.yaml maps to \`pre-commit run --config <config> --files a.py b.yaml\` per configuration"
else
    fail_test "files-mapping" "unexpected argv: $(trailing_argv | tr '\n' ' ')"
fi

# Bare mode: changed-vs-HEAD (including unstaged edits), not staged-only.
dummy_bare="tests/lint/fixtures/lint_wrapper_bare_mode.txt"
dummy_bare_path="${REPO_ROOT}/${dummy_bare}"
printf 'unstaged change for bare-mode CLI mapping test\n' > "${dummy_bare_path}"
git -C "${REPO_ROOT}" add -f -- "${dummy_bare}"
STAGED_FILES+=("${dummy_bare}")
CREATED_FILES+=("${dummy_bare_path}")

run_with_stub
bare_argv="$(trailing_argv)"
git -C "${REPO_ROOT}" restore --staged -- "${dummy_bare}" 2>/dev/null || true
rm -f "${dummy_bare_path}"

if echo "${bare_argv}" | grep -qF "${dummy_bare}" \
    && [[ "$(echo "${bare_argv}" | head -4 | tr '\n' ' ')" == "run --config ${LAST_CONFIG_PATH} --files " ]]; then
    pass_test "bare invocation maps to \`pre-commit run --config <config> --files <changed-vs-HEAD>\` per configuration"
else
    fail_test "bare-mapping" "unexpected argv: $(echo "${bare_argv}" | tr '\n' ' ')"
fi

# ── Environment propagation ───────────────────────────────────────

echo ""
echo "── Environment propagation ───────────────────────────────"

run_with_stub --all-files
if captured_env | grep -qF "ZIZMOR_NO_ONLINE_AUDITS=true" \
    && captured_env | grep -qF "PRE_COMMIT_HOME=${WARM_CACHE}"; then
    pass_test "offline runs export ZIZMOR_NO_ONLINE_AUDITS=true and PRE_COMMIT_HOME"
else
    fail_test "env-offline" "unexpected env: $(captured_env | tr '\n' ' ')"
fi

run_with_stub --all-files --online
if captured_env | grep -qF "ZIZMOR_NO_ONLINE_AUDITS=<unset>"; then
    pass_test "--online does not force ZIZMOR_NO_ONLINE_AUDITS"
else
    fail_test "env-online" "unexpected env: $(captured_env | tr '\n' ' ')"
fi

# ── Local hook wiring (offline execution with a warm cache) ──────

echo ""
echo "── Local hook wiring (offline, warm cache) ───────────────"

# The wiring cases run the REAL pre-commit through both
# configurations. gofmt and go-vet moved to .github/pre-commit-ci.yaml
# in the ADR-0005 cutover, so their wiring is proven by the CI
# configuration invocation; check-portable-null-device stays in the
# gate configuration, so its case proves the gate invocation runs real
# hooks too. The fake cache neutralizes the third-party tools either
# way.
assert_wiring() {
    local label="$1"
    local relative_path="$2"
    local content="$3"
    local expect_rc="$4"
    local expect_str="$5"

    local full_path="${REPO_ROOT}/${relative_path}"
    mkdir -p "$(dirname "${full_path}")"
    printf '%s' "${content}" > "${full_path}"
    CREATED_FILES+=("${full_path}")

    local rc=0
    local output
    output="$(PRE_COMMIT_HOME="${WARM_CACHE}" env -u CI python3 "${LINT}" --files "${relative_path}" 2>&1)" || rc=$?
    rm -f "${full_path}"

    local ok=true
    if [[ "${expect_rc}" == "0" ]] && [[ "${rc}" -ne 0 ]]; then
        ok=false
    elif [[ "${expect_rc}" != "0" ]] && [[ "${rc}" -eq 0 ]]; then
        ok=false
    fi
    if [[ -n "${expect_str}" ]] && ! echo "${output}" | grep -qF "${expect_str}"; then
        ok=false
    fi

    if ${ok}; then
        pass_test "${label}"
    else
        fail_test "${label}" "exit ${rc}, expected ${expect_rc} containing '${expect_str}'"
        echo "${output}" | tail -10 | sed 's/^/    /'
    fi
}

assert_wiring \
    "gofmt passes on correctly formatted Go" \
    "tests/lint/fixtures/linttest_wiring_valid.go" \
    $'package linttest\n\nfunc Hello() {\n}\n' \
    0 \
    "gofmt"

assert_wiring \
    "gofmt fails on unformatted Go" \
    "tests/lint/fixtures/linttest_wiring_unformatted.go" \
    $'package linttest\n\nfunc Hello() {\n\tvar x int=1\n\t_ = x\n}\n' \
    nonzero \
    "Go files need gofmt"

assert_wiring \
    "go-vet fails on a wrong-type Printf argument" \
    "tests/lint/fixtures/linttest_wiring_vet.go" \
    $'package linttest\n\nimport "fmt"\n\nfunc Hello() {\n\tfmt.Printf("%d", "not an int")\n}\n' \
    nonzero \
    "wrong type"

assert_wiring \
    "check-portable-null-device fails on a hardcoded null-device path (gate)" \
    "tests/lint/fixtures/linttest_wiring_null_device.go" \
    $'package linttest\n\nfunc Null() string {\n\treturn "/dev/null"\n}\n' \
    nonzero \
    "use os.DevNull instead"

# ── Deletions do not crash default (bare) mode ────────────────────

echo ""
echo "── Deletions in bare mode ─────────────────────────────────"

dummy_del="tests/lint/fixtures/linttest_wiring_deleted.go"
dummy_del_path="${REPO_ROOT}/${dummy_del}"
printf 'package linttest\n' > "${dummy_del_path}"
CREATED_FILES+=("${dummy_del_path}")
git -C "${REPO_ROOT}" add -f -- "${dummy_del}"
STAGED_FILES+=("${dummy_del}")
rm -f "${dummy_del_path}"

del_rc=0
del_output="$(PRE_COMMIT_HOME="${WARM_CACHE}" env -u CI python3 "${LINT}" 2>&1)" || del_rc=$?

git -C "${REPO_ROOT}" restore --staged -- "${dummy_del}" 2>/dev/null || true

if ! echo "${del_output}" | grep -qF "Traceback"; then
    pass_test "bare mode tolerates a staged deletion without crashing"
else
    fail_test "bare-mode-deletion" "unexpected traceback (exit ${del_rc})"
    echo "${del_output}" | tail -10 | sed 's/^/    /'
fi

# ── Summary ──────────────────────────────────────────────────────

echo ""
echo "════════════════════════════════════════════════════════"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [[ "${FAIL}" -gt 0 ]]; then
    exit 1
fi
