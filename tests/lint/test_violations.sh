#!/usr/bin/env bash
# tests/lint/test_violations.sh
#
# Reproducible fixture that verifies scripts/lint.py catches
# representative violations for each supported check category.
#
# Usage:
#     bash tests/lint/test_violations.sh
#
# Exit 0  = all assertions passed
# Exit 1  = at least one assertion failed

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LINT="${REPO_ROOT}/scripts/lint.py"
PASS=0
FAIL=0
SKIPPED=0
# Track test fixture files created in the repo so they can be
# cleaned up even if the script is interrupted.
CREATED_FILES=()
STAGED_FILES=()

cleanup() {
    for f in "${STAGED_FILES[@]}"; do
        git -C "${REPO_ROOT}" restore --staged -- "${f}" 2>/dev/null || true
    done
    for f in "${CREATED_FILES[@]}"; do
        rm -f "${f}"
    done
}
trap cleanup EXIT INT TERM HUP

# ── Helpers ────────────────────────────────────────────────────

# Create a temp file inside the repo (tools need repo-relative
# paths), run lint.py --files on it, and check for the expected
# exit code and output substring.
assert_lint() {
    local label="$1"
    local filename="$2"   # relative to repo root
    local content="$3"    # file content
    local expect_rc="$4"  # 0 or nonzero
    local expect_str="${5:-}"  # substring to find in output
    local expect_diag="${6:-}" # diagnostic substring to find in output

    local filepath="${REPO_ROOT}/${filename}"
    mkdir -p "$(dirname "${filepath}")"
    printf '%s' "${content}" > "${filepath}"
    CREATED_FILES+=("${filepath}")

    local rc=0
    local output
    output="$(python3 "${LINT}" --files "${filename}" 2>&1)" || rc=$?

    rm -f "${filepath}"

    local ok=true

    if [[ "${expect_rc}" == "0" ]] && [[ "${rc}" -ne 0 ]]; then
        echo "FAIL  ${label}: expected exit 0, got ${rc}"
        ok=false
    elif [[ "${expect_rc}" != "0" ]] && [[ "${rc}" -eq 0 ]]; then
        echo "FAIL  ${label}: expected non-zero exit, got 0"
        ok=false
    fi

    # Strip ANSI colour codes for reliable substring matching.
    local clean_output
    # shellcheck disable=SC2001  # regex substitution requires sed
    clean_output="$(echo "${output}" | sed 's/\x1b\[[0-9;]*m//g')"

    if [[ -n "${expect_str}" ]] && ! echo "${clean_output}" | grep -qE "(✓|✗|○|\?|!) ${expect_str}( |$)"; then
        echo "FAIL  ${label}: expected output to contain '${expect_str}'"
        echo "  actual output (last 5 lines):"
        echo "${output}" | tail -5 | sed 's/^/    /'
        ok=false
    fi

    if [[ -n "${expect_diag}" ]] && ! echo "${clean_output}" | grep -qF "${expect_diag}"; then
        echo "FAIL  ${label}: expected output to contain diagnostic '${expect_diag}'"
        echo "  actual output (last 5 lines):"
        echo "${output}" | tail -5 | sed 's/^/    /'
        ok=false
    fi

    # When expecting a failure, verify the expected hook was the one
    # that actually failed (not just that the name appears somewhere).
    # If the hook is unavailable (tool not installed), treat the test
    # as skipped — violation detection can only be verified when the
    # tool is present.
    if [[ "${expect_rc}" != "0" ]] && [[ -n "${expect_str}" ]]; then
        if echo "${clean_output}" | grep -qE "! ${expect_str}( |$)"; then
            echo "SKIP  ${label}: ${expect_str} tool not available"
            SKIPPED=$((SKIPPED + 1))
            return
        fi
        if ! echo "${clean_output}" | grep -qE "✗ ${expect_str}( |$)"; then
            echo "FAIL  ${label}: expected '${expect_str}' to be the failing hook"
            echo "  actual output (last 5 lines):"
            echo "${output}" | tail -5 | sed 's/^/    /'
            ok=false
        fi
    fi

    if ${ok}; then
        echo "PASS  ${label}"
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
    fi
}

cd "${REPO_ROOT}"

# ── Parity check ──────────────────────────────────────────────

echo "── Parity ──────────────────────────────────────────────"
parity_rc=0
parity_output="$(python3 "${LINT}" --check-parity 2>&1)" || parity_rc=$?
if [[ "${parity_rc}" -eq 0 ]] && echo "${parity_output}" | grep -q "PARITY CHECK PASSED"; then
    echo "PASS  parity: all hooks have registered handlers"
    PASS=$((PASS + 1))
else
    echo "FAIL  parity: some hooks lack registered handlers (exit code: ${parity_rc})"
    echo "${parity_output}" | tail -5 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
fi

# ── YAML violations ──────────────────────────────────────────

echo ""
echo "── YAML ────────────────────────────────────────────────"

assert_lint \
    "yaml-valid" \
    "_test_lint_valid.yaml" \
    $'---\nkey: value\n' \
    0

assert_lint \
    "yaml-bad-indent" \
    "_test_lint_bad.yaml" \
    $'---\nbad:\n yaml:\n   wrong: true\n' \
    nonzero \
    "yamllint"

# ── JSON violations ──────────────────────────────────────────

echo ""
echo "── JSON ────────────────────────────────────────────────"

assert_lint \
    "json-valid" \
    "_test_lint_valid.json" \
    $'{"key": "value"}\n' \
    0

assert_lint \
    "json-invalid" \
    "_test_lint_bad.json" \
    '{ invalid json' \
    nonzero \
    "check-json"

# ── Markdown violations ──────────────────────────────────────

echo ""
echo "── Markdown ──────────────────────────────────────────────"

assert_lint \
    "md-valid" \
    "_test_lint_valid.md" \
    $'# Valid heading\n\nSome text.\n' \
    0

assert_lint \
    "md-trailing-ws" \
    "_test_lint_ws.md" \
    $'# Heading\n\nTrailing spaces   \n' \
    nonzero \
    "trailing-whitespace"

# ── Shell violations ─────────────────────────────────────────

echo ""
echo "── Shell ─────────────────────────────────────────────────"

assert_lint \
    "sh-valid" \
    "_test_lint_valid.sh" \
    $'#!/usr/bin/env bash\nset -euo pipefail\necho "hello"\n' \
    0

assert_lint \
    "sh-unquoted-var" \
    "_test_lint_bad.sh" \
    $'#!/bin/bash\necho $unquoted_var\n' \
    nonzero \
    "shellcheck"

# ── JSON5 violations ─────────────────────────────────────────

echo ""
echo "── JSON5 ─────────────────────────────────────────────────"

assert_lint \
    "json5-valid" \
    "_test_lint_valid.json5" \
    $'{key: "value", /* comment */ trailing: 1,}\n' \
    0

assert_lint \
    "json5-invalid" \
    "_test_lint_bad.json5" \
    $'{key: value: invalid}\n' \
    nonzero \
    "check-json5"

# ── End-of-file fixer ────────────────────────────────────────

echo ""
echo "── End-of-file ────────────────────────────────────────────"

assert_lint \
    "eof-correct" \
    "_test_lint_eof_ok.txt" \
    $'has newline\n' \
    0

assert_lint \
    "eof-missing-newline" \
    "_test_lint_eof_bad.txt" \
    "no final newline" \
    nonzero \
    "end-of-file-fixer"

# ── Secrets violations ───────────────────────────────────

echo ""
echo "── Secrets ─────────────────────────────────────────────"

# detect-secrets should flag a private key header regardless of
# baseline contents — PrivateKeyDetector matches the PEM marker.
# Content stored in a variable so the pragma can sit on the same line.
_secret_fixture=$'# Test file\n-----BEGIN RSA PRIVATE KEY-----\nMIIBog\n-----END RSA PRIVATE KEY-----\n'  # pragma: allowlist secret
assert_lint \
    "secrets-private-key" \
    "_test_lint_secret.py" \
    "${_secret_fixture}" \
    nonzero \
    "detect-secrets"

# ── Workflow violations ──────────────────────────────────

echo ""
echo "── Workflow (zizmor) ─────────────────────────────────────"

# zizmor should flag template injection: an untrusted input from
# pull_request_target used directly in a run: step.
assert_lint \
    "workflow-template-injection" \
    ".github/workflows/_test_lint_bad.yml" \
    $'name: Test\non:\n  pull_request_target:\n    types: [opened]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo "${{ github.event.pull_request.title }}"\n' \
    nonzero \
    "zizmor"

# ── Spec document independence ──────────────────────────────

echo ""
echo "── Spec document independence ──────────────────────────"

# Adding a new docs/ specification (and touching AGENTS.md, as an
# authoring agent would) must not invoke the retired
# spec-hierarchy-sync hook or demand a REVIEW_SPEC_HIERARCHY
# update in the protected review harness.
dummy_spec="docs/architecture/_test_lint_new_spec.md"
dummy_path="${REPO_ROOT}/${dummy_spec}"
printf '%s\n' '# Test specification' '' \
    'Temporary specification document used by the lint independence test.' \
    > "${dummy_path}"
CREATED_FILES+=("${dummy_path}")

lint_spec_rc=0
lint_spec_output="$(python3 "${LINT}" --files "${dummy_spec}" AGENTS.md 2>&1)" || lint_spec_rc=$?
# shellcheck disable=SC2001  # regex substitution requires sed
lint_spec_clean="$(echo "${lint_spec_output}" | sed 's/\x1b\[[0-9;]*m//g')"
rm -f "${dummy_path}"

if echo "${lint_spec_clean}" | grep -qE "spec-hierarchy-sync|REVIEW_SPEC_HIERARCHY"; then
    echo "FAIL  spec-doc-independence: retired hook or REVIEW_SPEC_HIERARCHY mentioned"
    echo "${lint_spec_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
elif echo "${lint_spec_clean}" | grep -qF "check_spec_hierarchy.py"; then
    echo "FAIL  spec-doc-independence: retired checker still invoked"
    echo "${lint_spec_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
elif [[ "${lint_spec_rc}" -ne 0 ]]; then
    echo "FAIL  spec-doc-independence: lint failed on new spec without review.yaml (exit ${lint_spec_rc})"
    echo "${lint_spec_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
else
    echo "PASS  spec-doc-independence: new spec does not demand review.yaml update"
    PASS=$((PASS + 1))
fi

# ── Go formatting and vet ───────────────────────────────────

echo ""
echo "── Go ──────────────────────────────────────────────────"

assert_lint \
    "go-fmt-valid" \
    "tests/lint/fixtures/linttest_valid.go" \
    $'package linttest\n\nfunc Hello() {\n}\n' \
    0 \
    "gofmt"

assert_lint \
    "go-fmt-unformatted" \
    "tests/lint/fixtures/linttest_unformatted.go" \
    $'package linttest\n\nfunc Hello() {\n\tvar x int=1\n\t_ = x\n}\n' \
    nonzero \
    "gofmt"

assert_lint \
    "go-vet-printf" \
    "tests/lint/fixtures/linttest_vet.go" \
    $'package linttest\n\nimport "fmt"\n\nfunc Hello() {\n\tfmt.Printf("%d", "not an int")\n}\n' \
    nonzero \
    "go-vet" \
    "wrong type"

# lint.py must wire the local Go hooks when a real Go file is in
# the file set (not only the temp fixtures above).
lint_go_rc=0
lint_go_output="$(python3 "${LINT}" --files wms/memory/lifecycle.go 2>&1)" || lint_go_rc=$?
# shellcheck disable=SC2001  # regex substitution requires sed
lint_go_clean="$(echo "${lint_go_output}" | sed 's/\x1b\[[0-9;]*m//g')"
if [[ "${lint_go_rc}" -eq 0 ]] \
    && echo "${lint_go_clean}" | grep -qE "✓ gofmt( |$)" \
    && echo "${lint_go_clean}" | grep -qE "✓ go-vet( |$)"; then
    echo "PASS  go-hooks-wiring: lint.py runs gofmt and go-vet"
    PASS=$((PASS + 1))
else
    echo "FAIL  go-hooks-wiring: gofmt/go-vet did not pass (exit ${lint_go_rc})"
    echo "${lint_go_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
fi

# ears-manager check (module with third-party dependencies):
# verify go-vet either succeeds (with populated cache/network) or
# fails closed with the dependency resolution diagnostic banner.
lint_ears_rc=0
lint_ears_output="$(python3 "${LINT}" --files "ears-manager/cmd/ears-manager/main.go" 2>&1)" || lint_ears_rc=$?

# shellcheck disable=SC2001  # regex substitution requires sed
lint_ears_clean="$(echo "${lint_ears_output}" | sed 's/\x1b\[[0-9;]*m//g')"
if [[ "${lint_ears_rc}" -eq 0 ]] && echo "${lint_ears_clean}" | grep -qE "✓ go-vet( |$)"; then
    echo "PASS  go-vet-ears-manager: go-vet passed with populated cache/network"
    PASS=$((PASS + 1))
elif [[ "${lint_ears_rc}" -ne 0 ]] && echo "${lint_ears_clean}" | grep -qF "dependency resolution failed in ears-manager"; then
    echo "PASS  go-vet-ears-manager: go-vet failed closed with dependency resolution diagnostic"
    PASS=$((PASS + 1))
else
    echo "FAIL  go-vet-ears-manager: expected go-vet success or dependency-resolution diagnostic (exit ${lint_ears_rc})"
    echo "${lint_ears_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
fi

# ── Unstaged deletions ─────────────────────────────────────

echo ""
echo "── Unstaged deletions ────────────────────────────────"

# --all-files previously passed tracked-but-missing paths to
# file-oriented hooks, which crashed with FileNotFoundError.
# Incremental mode shares the same existence filter in _get_files.
# A staged add with an unstaged working-tree deletion is the
# scenario that crashed check-docstring-first and debug-statements.
dummy_del="tests/lint/fixtures/_test_unstaged_deletion.py"
dummy_del_path="${REPO_ROOT}/${dummy_del}"
printf '%s\n' '"""Temporary fixture for unstaged-deletion lint coverage."""' \
    > "${dummy_del_path}"
CREATED_FILES+=("${dummy_del_path}")
git -C "${REPO_ROOT}" add -- "${dummy_del}"
STAGED_FILES+=("${dummy_del}")
rm -f "${dummy_del_path}"

get_files_rc=0
get_files_output="$(python3 - "${LINT}" "${dummy_del}" <<'PY'
import argparse
import importlib.util
import sys

lint_path, dummy = sys.argv[1], sys.argv[2]
spec = importlib.util.spec_from_file_location("lintmod", lint_path)
mod = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(mod)
all_files = mod._get_files(argparse.Namespace(files=None, all_files=True))
incremental = mod._get_files(argparse.Namespace(files=None, all_files=False))
sentinel = "scripts/lint.py"
if sentinel not in all_files:
    print(f"expected tracked file missing from --all-files: {sentinel}")
    sys.exit(1)
if dummy in all_files:
    print(f"deleted path still collected by --all-files: {dummy}")
    sys.exit(1)
if dummy in incremental:
    print(f"deleted path still collected incrementally: {dummy}")
    sys.exit(1)
print("omitted")
PY
)" || get_files_rc=$?

# Incremental mode shares _get_files with --all-files. A staged add
# with an unstaged working-tree deletion still appears in
# ``git diff --cached --diff-filter=ACMR``, so this run would crash
# python hooks if the deleted .py path were still passed through.
# Full-tree --all-files is not invoked here because in-place fixers
# would rewrite the working tree; collection coverage is above.
lint_del_output="$(python3 "${LINT}" 2>&1)" || true
# shellcheck disable=SC2001  # regex substitution requires sed
lint_del_clean="$(echo "${lint_del_output}" | sed 's/\x1b\[[0-9;]*m//g')"

git -C "${REPO_ROOT}" restore --staged -- "${dummy_del}" 2>/dev/null || true
rm -f "${dummy_del_path}"

unstaged_ok=true
if [[ "${get_files_rc}" -ne 0 ]] || ! echo "${get_files_output}" | grep -qF "omitted"; then
    echo "FAIL  unstaged-deletion-filter: _get_files still collected the deleted path"
    echo "${get_files_output}" | tail -8 | sed 's/^/    /'
    unstaged_ok=false
elif echo "${lint_del_clean}" | grep -qF "FileNotFoundError"; then
    echo "FAIL  unstaged-deletion-filter: lint.py raised FileNotFoundError"
    echo "${lint_del_output}" | tail -8 | sed 's/^/    /'
    unstaged_ok=false
elif echo "${lint_del_clean}" | grep -qF "${dummy_del}"; then
    echo "FAIL  unstaged-deletion-filter: deleted path still reached a hook"
    echo "${lint_del_output}" | tail -8 | sed 's/^/    /'
    unstaged_ok=false
fi

if ${unstaged_ok}; then
    echo "PASS  unstaged-deletion-filter: missing tracked files are ignored"
    PASS=$((PASS + 1))
else
    FAIL=$((FAIL + 1))
fi

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "════════════════════════════════════════════════════════"
echo "Results: ${PASS} passed, ${FAIL} failed, ${SKIPPED} skipped"

if [[ "${FAIL}" -gt 0 ]]; then
    exit 1
fi

# Guard against vacuous success: if no tests passed and some were
# skipped (all tools unavailable), the suite provides no regression
# value — fail so the environment issue is surfaced.
if [[ "${PASS}" -eq 0 ]] && [[ "${SKIPPED}" -gt 0 ]]; then
    echo "ERROR: no tests passed (${SKIPPED} skipped) — tools may be missing"
    exit 1
fi
