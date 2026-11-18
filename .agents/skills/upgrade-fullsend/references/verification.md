# Fullsend Upgrade Verification

## Validate selected-release config

When either `.fullsend/config.yaml` or `.fullsend/config.base.yaml` exists, validate the files against the exact selected release. Prefer a strict schema or validator from that release. Otherwise inspect `internal/config` at the selected tag: the YAML tags on config structs and nested structs, custom `UnmarshalYAML` handlers, and validation rules for dynamic maps. Compare every configured key at every level in each present file with that release's supported keys; reject unsupported or misspelled keys. `fullsend lint` is not necessarily a strict key validator: v0.56.1 uses non-strict YAML unmarshalling, so a typo such as `runtim: pi` is ignored.

Run the selected release's `fullsend lint` for the semantic and harness checks it supports. For v0.56.1, lint validates merged agents and model aliases but does not validate some inherited values, such as `runtime` or `version`, from `config.base.yaml` when the overlay omits them. Validate the base layer independently by linting a temporary copy as `config.yaml`, then lint the actual layered config:

```bash
(
  set -euo pipefail
  cd "$FULLSEND_TARGET_ROOT"
  if [[ -f .fullsend/config.base.yaml ]]; then
    BASE_AUDIT_DIR=$(mktemp -d)
    trap 'rm -rf "$BASE_AUDIT_DIR"' EXIT
    cp -R .fullsend/. "$BASE_AUDIT_DIR/"
    rm -f "$BASE_AUDIT_DIR/config.yaml"
    cp .fullsend/config.base.yaml "$BASE_AUDIT_DIR/config.yaml"
    rm -f "$BASE_AUDIT_DIR/config.base.yaml"
    rm -rf "$BASE_AUDIT_DIR/.fullsend-cache"
    fullsend lint --fullsend-dir "$BASE_AUDIT_DIR"
  fi
  if [[ -f .fullsend/config.yaml ]]; then
    fullsend lint --fullsend-dir .fullsend
  elif [[ -f .fullsend/config.base.yaml ]]; then
    printf '%s\n' 'Validated config.base.yaml as standalone config.'
  else
    printf '%s\n' 'No .fullsend config files; no per-repo config to validate.'
  fi
)
```

The temporary copy preserves local harness paths and is removed on success or failure. `fullsend lint` may fetch remote harness resources and write `.fullsend/.fullsend-cache`; use `--offline` only when all required resources are cached. Use normal lint mode, not `--strict`: warnings remain visible without blocking on unrelated existing deprecations. If the selected CLI lacks `lint`, use a documented non-mutating config validator from that release; if no suitable validator exists or the check cannot run, report a blocker.

## Repository checks

Inspect the target repository's documentation and CI/build configuration to identify its actual commands, then run them from `FULLSEND_TARGET_ROOT`. For a non-ProtoBot target, use that repository's checks. For ProtoBot, run:

```bash
(
  set -euo pipefail
  cd "$FULLSEND_TARGET_ROOT"
  python3 scripts/check_harness_integrity.py
  python scripts/lint.py --all-files
  for module in drafting-table ears-manager source-control-manager validator wms; do
    (cd "$module" && go test ./...)
  done
)
```

If a category has no applicable command or a check cannot run, record that limitation; never report it as passed. Record each command's actual result. If any check fails or cannot run, report the failure or blocker; do not claim the upgrade is verified.

## PR description

Prepare a structured PR description that includes:

- **Release and migration notes:** the previous and selected release tags, release-note links for both `fullsend-ai/fullsend` and `fullsend-ai/agents`, breaking changes, and how each migration requirement was handled.
- **Harness compatibility:** every affected overlay and the evidence for its compatibility with the selected release. A matching checksum proves file identity, not behavioral compatibility.
- **Workflow synchronization:** the generated workflow changes and each reviewed downstream addition retained.
- **Repository settings:** approved setting names and actions, without plaintext secret values.
- **Verification:** the selected-release config audit and lint when either config layer exists, plus the target's applicable integrity, lint, and test commands with their observed results.
