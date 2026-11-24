#!/usr/bin/env bash
# tests/lint/test_violations.sh
#
# Contract tests for the scripts/lint.py wrapper around `pre-commit`.
#
# This suite intentionally does NOT verify that individual third-party
# tools (ruff, yamllint, shellcheck, markdownlint, detect-secrets,
# zizmor, skillsaw, ...) catch specific violations. scripts/lint.py no
# longer re-implements those tools or their dispatch; it is a thin
# wrapper that execs the real `pre-commit`, so tool-level correctness
# is CI's responsibility via `pre-commit run -a` against the genuine
# hook repositories. This suite instead verifies the wrapper's own
# contract:
#
#   * the offline cache pre-flight fails closed with an actionable
#     message when a hook repository/revision is missing, and is
#     skipped entirely with --online;
#   * --check-parity validates .pre-commit-config.yaml and the cache
#     without running any hooks;
#   * --all-files / --files / bare invocation translate to the
#     equivalent `pre-commit run` invocation, including the
#     changed-vs-HEAD (not staged-only) default scope;
#   * ZIZMOR_NO_ONLINE_AUDITS and PRE_COMMIT_HOME are exported
#     correctly for offline and --online runs;
#   * this repository's own `repo: local` hooks (gofmt, go-vet, ...)
#     still run correctly through the full wrapper -> pre-commit ->
#     local-hook chain once the cache is warm.
#
# The last point uses tests/lint/build_fake_pre_commit_cache.py to
# fabricate a structurally-valid (but non-functional) cache for every
# *non-local* hook repository, so pre-commit can load every repo's
# manifest (which it always does, regardless of which files are given)
# without network access, while the real local hooks run unmodified.
#
# Usage:
#     bash tests/lint/test_violations.sh
#
# Exit 0  = all assertions passed
# Exit 1  = at least one assertion failed

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LINT="${REPO_ROOT}/scripts/lint.py"
FAKE_CACHE_BUILDER="${REPO_ROOT}/tests/lint/build_fake_pre_commit_cache.py"
# Use the real interpreter, not a version-manager shim: pyenv-style shims
# rewrite PATH before exec, which would let an installed pre-commit shadow
# the stub placed at the front of PATH in run_with_stub.
PYTHON="$(python3 -c 'import sys; print(sys.executable)')"
PASS=0
FAIL=0

WORKDIR="$(mktemp -d)"
EMPTY_CACHE="${WORKDIR}/empty-cache"
WARM_CACHE="${WORKDIR}/warm-cache"
STUB_BIN="${WORKDIR}/stub-bin"
STUB_CAPTURE="${WORKDIR}/stub-capture"

# Track fixture files created in the repo so they are cleaned up even
# if the script is interrupted.
CREATED_FILES=()
STAGED_FILES=()

cleanup() {
    if [[ "${#STAGED_FILES[@]}" -gt 0 ]]; then
        for f in "${STAGED_FILES[@]}"; do
            git -C "${REPO_ROOT}" restore --staged -- "${f}" 2>/dev/null || true
        done
    fi
    if [[ "${#CREATED_FILES[@]}" -gt 0 ]]; then
        for f in "${CREATED_FILES[@]}"; do
            rm -f "${f}"
        done
    fi
    rm -rf "${WORKDIR}"
}
trap cleanup EXIT INT TERM HUP

cd "${REPO_ROOT}"

if ! command -v pre-commit >/dev/null 2>&1; then
    echo "ERROR: pre-commit is not installed; cannot exercise the wrapper contract"
    exit 1
fi

"${PYTHON}" "${FAKE_CACHE_BUILDER}" "${REPO_ROOT}" "${WARM_CACHE}"

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
    PASS=$((PASS))
    FAIL=$((FAIL + 1))
}

# Run lint.py against the *real* .pre-commit-config.yaml with an empty
# cache directory (no db.db at all), network assumed unavailable.
run_with_empty_cache() {
    PRE_COMMIT_HOME="${EMPTY_CACHE}" env -u CI "${PYTHON}" "${LINT}" "$@" 2>&1
}

# Run lint.py against the real config through the stub `pre-commit`,
# with the fake warm cache so the pre-flight passes.
run_with_stub() {
    rm -rf "${STUB_CAPTURE}"
    mkdir -p "${STUB_CAPTURE}"
    local rc=0
    PATH="${STUB_BIN}:${PATH}" \
        PRE_COMMIT_HOME="${WARM_CACHE}" \
        STUB_CAPTURE_DIR="${STUB_CAPTURE}" \
        env -u CI "${PYTHON}" "${LINT}" "$@" >/dev/null 2>&1 || rc=$?
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
done < <("${PYTHON}" - "${REPO_ROOT}" <<'PY'
import sys
import yaml
from pathlib import Path
with open(Path(sys.argv[1]) / ".pre-commit-config.yaml") as fh:
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
warm_parity_output="$(PRE_COMMIT_HOME="${WARM_CACHE}" env -u CI "${PYTHON}" "${LINT}" --check-parity)" || warm_parity_rc=$?
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
# the program name lint.py originally passed.
trailing_argv() {
    captured_argv | tail -n +2
}

run_with_stub --all-files
if [[ "$(trailing_argv)" == $'run\n--all-files' ]]; then
    pass_test "--all-files maps to \`pre-commit run --all-files\`"
else
    fail_test "all-files-mapping" "unexpected argv: $(trailing_argv | tr '\n' ' ')"
fi

run_with_stub --files a.py b.yaml
if [[ "$(trailing_argv)" == $'run\n--files\na.py\nb.yaml' ]]; then
    pass_test "--files a.py b.yaml maps to \`pre-commit run --files a.py b.yaml\`"
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
    && [[ "$(echo "${bare_argv}" | head -2 | tr '\n' ' ')" == "run --files " ]]; then
    pass_test "bare invocation maps to \`pre-commit run --files <changed-vs-HEAD>\`"
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
    output="$(PRE_COMMIT_HOME="${WARM_CACHE}" env -u CI "${PYTHON}" "${LINT}" --files "${relative_path}" 2>&1)" || rc=$?
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
del_output="$(PRE_COMMIT_HOME="${WARM_CACHE}" env -u CI "${PYTHON}" "${LINT}" 2>&1)" || del_rc=$?

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
