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
AGENTS_FILE="${REPO_ROOT}/AGENTS.md"
AGENTS_BACKUP=""
BASELINE_FILE="${REPO_ROOT}/.skillsaw-baseline.json"
BASELINE_BACKUP=""
RATCHET="${REPO_ROOT}/scripts/check_baseline_ratchet.py"

restore_agents_md() {
    if [[ -n "${AGENTS_BACKUP}" ]] && [[ -f "${AGENTS_BACKUP}" ]]; then
        cp "${AGENTS_BACKUP}" "${AGENTS_FILE}"
        rm -f "${AGENTS_BACKUP}"
        AGENTS_BACKUP=""
    fi
}

restore_baseline() {
    if [[ -n "${BASELINE_BACKUP}" ]] && [[ -f "${BASELINE_BACKUP}" ]]; then
        cp "${BASELINE_BACKUP}" "${BASELINE_FILE}"
        rm -f "${BASELINE_BACKUP}"
        BASELINE_BACKUP=""
    fi
}

cleanup() {
    restore_agents_md
    restore_baseline
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

# ── AGENTS.md context-budget ratchet ───────────────────────

echo ""
echo "── AGENTS.md context-budget ────────────────────────────"

# skillsaw is already the pre-commit / lint.py hook for instruction
# files. context-budget + .skillsaw-baseline.json (ceiling) reject
# token growth in AGENTS.md without a special-purpose line-count
# script. Assert that the committed ceiling equals len(AGENTS.md)//4
# (no unratcheted headroom) and that the configured agents-md warn
# limit is <= that value so context-budget always emits a finding.
ceiling_check_rc=0
ceiling_check_output="$(python3 - "${REPO_ROOT}" <<'PY'
import json, pathlib, sys, yaml

repo_root = pathlib.Path(sys.argv[1])
agents_path = repo_root / "AGENTS.md"
baseline_path = repo_root / ".skillsaw-baseline.json"
skillsaw_yaml = repo_root / ".skillsaw.yaml"

agents_text = agents_path.read_text(encoding="utf-8")
agents_tokens = len(agents_text) // 4

with open(baseline_path, encoding="utf-8") as f:
    baseline_data = json.load(f)

ceiling_entry = None
for v in baseline_data.get("violations", []):
    if v.get("rule_id") == "context-budget" and v.get("file_path") == "AGENTS.md":
        ceiling_entry = v
        break

if not ceiling_entry:
    print("missing AGENTS.md context-budget entry in .skillsaw-baseline.json")
    sys.exit(1)

committed_ceiling = ceiling_entry.get("value")
if committed_ceiling is None:
    print("missing value in AGENTS.md context-budget baseline entry")
    sys.exit(1)

if int(committed_ceiling) != agents_tokens:
    print(f"committed ceiling ({committed_ceiling}) != len(AGENTS.md)//4 ({agents_tokens})")
    sys.exit(1)

baseline_mode = ceiling_entry.get("baseline_mode")
if baseline_mode != "ceiling":
    print(f"expected baseline_mode 'ceiling', got '{baseline_mode}'")
    sys.exit(1)

with open(skillsaw_yaml, encoding="utf-8") as f:
    skillsaw_config = yaml.safe_load(f)

limits = skillsaw_config.get("rules", {}).get("context-budget", {}).get("limits", {}).get("agents-md", {})
if isinstance(limits, dict):
    warn_limit = limits.get("warn")
else:
    warn_limit = limits

if warn_limit is None:
    print("missing agents-md warn limit in .skillsaw.yaml")
    sys.exit(1)

if int(warn_limit) > int(committed_ceiling):
    print(f"configured warn limit ({warn_limit}) > committed ceiling ({committed_ceiling})")
    sys.exit(1)

PY
)" || ceiling_check_rc=$?

if [[ "${ceiling_check_rc}" -ne 0 ]]; then
    echo "FAIL  agents-md-budget-ceiling: ${ceiling_check_output}"
    FAIL=$((FAIL + 1))
else
    echo "PASS  agents-md-budget-ceiling: committed ceiling equals len(AGENTS.md)//4 and warn limit is <= ceiling"
    PASS=$((PASS + 1))
fi

lint_agents_rc=0
lint_agents_output="$(python3 "${LINT}" --files AGENTS.md 2>&1)" || lint_agents_rc=$?
# shellcheck disable=SC2001  # regex substitution requires sed
lint_agents_clean="$(echo "${lint_agents_output}" | sed 's/\x1b\[[0-9;]*m//g')"

if echo "${lint_agents_clean}" | grep -qE "! skillsaw( |$)"; then
    echo "SKIP  agents-md-budget-current: skillsaw tool not available"
    SKIPPED=$((SKIPPED + 1))
elif [[ "${lint_agents_rc}" -eq 0 ]] \
    && echo "${lint_agents_clean}" | grep -qE "✓ skillsaw( |$)"; then
    echo "PASS  agents-md-budget-current: current AGENTS.md is within the ceiling"
    PASS=$((PASS + 1))
else
    echo "FAIL  agents-md-budget-current: skillsaw did not pass on AGENTS.md (exit ${lint_agents_rc})"
    echo "${lint_agents_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
fi

# Overflow by a 1-token delta instead of large fixture blocks.
# Adding 4 - (len(text) % 4) characters increases len(text)//4 by exactly
# 1 token. Ending with a newline ensures pre-commit end-of-file-fixer passes
# without modifying the file before skillsaw runs.
AGENTS_BACKUP="$(mktemp)"
cp "${AGENTS_FILE}" "${AGENTS_BACKUP}"
python3 - "${AGENTS_FILE}" <<'PY'
import sys

agents_path = sys.argv[1]
with open(agents_path, "r", encoding="utf-8") as f:
    text = f.read()

cur_tokens = len(text) // 4
needed_chars = 4 - (len(text) % 4)
pad = ("x" * (needed_chars - 1)) + "\n"
assert len(pad) == needed_chars
assert (len(text) + len(pad)) // 4 == cur_tokens + 1

with open(agents_path, "a", encoding="utf-8") as f:
    f.write(pad)
PY

lint_bloat_rc=0
lint_bloat_output="$(python3 "${LINT}" --files AGENTS.md 2>&1)" || lint_bloat_rc=$?
# shellcheck disable=SC2001  # regex substitution requires sed
lint_bloat_clean="$(echo "${lint_bloat_output}" | sed 's/\x1b\[[0-9;]*m//g')"
restore_agents_md

if [[ "${ceiling_check_rc}" -ne 0 ]]; then
    echo "FAIL  agents-md-budget-overflow: prerequisite ceiling check failed (${ceiling_check_output})"
    FAIL=$((FAIL + 1))
elif echo "${lint_bloat_clean}" | grep -qE "! skillsaw( |$)"; then
    echo "SKIP  agents-md-budget-overflow: skillsaw tool not available"
    SKIPPED=$((SKIPPED + 1))
elif [[ "${lint_bloat_rc}" -ne 0 ]] \
    && echo "${lint_bloat_clean}" | grep -qE "✗ skillsaw( |$)" \
    && echo "${lint_bloat_clean}" | grep -qF "context-budget"; then
    echo "PASS  agents-md-budget-overflow: token growth fails context-budget"
    PASS=$((PASS + 1))
else
    echo "FAIL  agents-md-budget-overflow: expected skillsaw context-budget failure (exit ${lint_bloat_rc})"
    echo "${lint_bloat_output}" | tail -12 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
fi

# ── Baseline ceiling ratchet vs base branch ────────────────

echo ""
echo "── Baseline ceiling ratchet ────────────────────────────"

# Resolve a local base ref distinct from HEAD. Prefer origin/main,
# then main, then HEAD~1, ensuring the comparison base is not HEAD.
ratchet_base_ref=""
head_commit="$(git -C "${REPO_ROOT}" rev-parse --verify --quiet 'HEAD^{commit}' 2>/dev/null || true)"
for ref_candidate in "origin/main" "main" "HEAD~1"; do
    cand_commit="$(git -C "${REPO_ROOT}" rev-parse --verify --quiet "${ref_candidate}^{commit}" 2>/dev/null || true)"
    if [[ -n "${cand_commit}" ]] && [[ "${cand_commit}" != "${head_commit}" ]]; then
        ratchet_base_ref="${ref_candidate}"
        break
    fi
done

baseline_adjust_ceiling() {
    local new_value="$1"
    python3 - "${BASELINE_FILE}" "${new_value}" <<'PY'
import json, sys

path, new_value = sys.argv[1], int(sys.argv[2])
with open(path, encoding="utf-8") as handle:
    data = json.load(handle)
for entry in data.get("violations", []):
    if (
        entry.get("rule_id") == "context-budget"
        and entry.get("file_path") == "AGENTS.md"
        and entry.get("baseline_mode") == "ceiling"
    ):
        print(entry["value"])
        entry["value"] = new_value
        break
else:
    sys.exit("missing AGENTS.md context-budget ceiling")
with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2, ensure_ascii=False)
    handle.write("\n")
PY
}

if [[ -z "${ratchet_base_ref}" ]]; then
    echo "SKIP  baseline-ratchet-increase: no origin/main or main ref"
    SKIPPED=$((SKIPPED + 1))
    echo "SKIP  baseline-ratchet-decrease: no origin/main or main ref"
    SKIPPED=$((SKIPPED + 1))
    echo "SKIP  baseline-ratchet-unchanged: no origin/main or main ref"
    SKIPPED=$((SKIPPED + 1))
else
    BASELINE_BACKUP="$(mktemp)"
    cp "${BASELINE_FILE}" "${BASELINE_BACKUP}"
    original_ceiling="$(python3 - "${BASELINE_FILE}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    data = json.load(handle)
for entry in data.get("violations", []):
    if (
        entry.get("rule_id") == "context-budget"
        and entry.get("file_path") == "AGENTS.md"
        and entry.get("baseline_mode") == "ceiling"
    ):
        print(entry["value"])
        break
else:
    sys.exit("missing AGENTS.md context-budget ceiling")
PY
)"

    bumped_ceiling=$((original_ceiling + 1))
    baseline_adjust_ceiling "${bumped_ceiling}" >/dev/null
    bump_rc=0
    bump_output="$(python3 "${RATCHET}" --base-ref "${ratchet_base_ref}" 2>&1)" || bump_rc=$?
    restore_baseline

    if [[ "${bump_rc}" -ne 0 ]] \
        && echo "${bump_output}" | grep -qF "ceiling for AGENTS.md increased from ${original_ceiling} to ${bumped_ceiling}"; then
        echo "PASS  baseline-ratchet-increase: ceiling bump fails vs ${ratchet_base_ref}"
        PASS=$((PASS + 1))
    else
        echo "FAIL  baseline-ratchet-increase: expected baseline-ratchet failure (exit ${bump_rc})"
        echo "${bump_output}" | tail -12 | sed 's/^/    /'
        FAIL=$((FAIL + 1))
    fi

    BASELINE_BACKUP="$(mktemp)"
    cp "${BASELINE_FILE}" "${BASELINE_BACKUP}"
    lowered_ceiling=$((original_ceiling - 1))
    baseline_adjust_ceiling "${lowered_ceiling}" >/dev/null
    decrease_rc=0
    decrease_output="$(python3 "${RATCHET}" --base-ref "${ratchet_base_ref}" 2>&1)" || decrease_rc=$?
    restore_baseline

    if [[ "${decrease_rc}" -eq 0 ]] && echo "${decrease_output}" | grep -qF "none increased"; then
        echo "PASS  baseline-ratchet-decrease: lowered ceiling passes vs ${ratchet_base_ref}"
        PASS=$((PASS + 1))
    else
        echo "FAIL  baseline-ratchet-decrease: expected exit 0 (exit ${decrease_rc})"
        echo "${decrease_output}" | tail -8 | sed 's/^/    /'
        FAIL=$((FAIL + 1))
    fi

    unchanged_rc=0
    unchanged_output="$(python3 "${RATCHET}" --base-ref "${ratchet_base_ref}" 2>&1)" || unchanged_rc=$?
    if [[ "${unchanged_rc}" -eq 0 ]] && echo "${unchanged_output}" | grep -qF "none increased"; then
        echo "PASS  baseline-ratchet-unchanged: matching ceiling passes vs ${ratchet_base_ref}"
        PASS=$((PASS + 1))
    else
        echo "FAIL  baseline-ratchet-unchanged: expected exit 0 (exit ${unchanged_rc})"
        echo "${unchanged_output}" | tail -8 | sed 's/^/    /'
        FAIL=$((FAIL + 1))
    fi
fi

lint_ratchet_rc=0
lint_ratchet_output="$(BASELINE_RATCHET_BASE_REF="${ratchet_base_ref}" python3 "${LINT}" --files .skillsaw-baseline.json 2>&1)" || lint_ratchet_rc=$?
# shellcheck disable=SC2001  # regex substitution requires sed
lint_ratchet_clean="$(echo "${lint_ratchet_output}" | sed 's/\x1b\[[0-9;]*m//g')"
if [[ "${lint_ratchet_rc}" -eq 0 ]] \
    && echo "${lint_ratchet_clean}" | grep -qE "✓ baseline-ratchet( |$)"; then
    echo "PASS  baseline-ratchet-wiring: lint.py runs baseline-ratchet"
    PASS=$((PASS + 1))
else
    echo "FAIL  baseline-ratchet-wiring: baseline-ratchet did not pass (exit ${lint_ratchet_rc})"
    echo "${lint_ratchet_output}" | tail -8 | sed 's/^/    /'
    FAIL=$((FAIL + 1))
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
