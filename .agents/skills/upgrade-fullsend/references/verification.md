# Fullsend upgrade verification

## Validate selected-release config

When either `.fullsend/config.yaml` or `.fullsend/config.base.yaml` exists,
validate each present file against the exact selected release. Prefer a
schema or validator from that release. Otherwise, inspect `internal/config`
at the selected tag: YAML tags on config structs and nested structs, custom
`UnmarshalYAML` handlers, and validation rules for dynamic maps. If neither
the selected release's schema/validator nor its `internal/config` source can
be inspected, report a blocker; do not treat the audit as satisfied. Compare
every configured key, type, and value at every level in each present file
with that release's supported config contract; reject unsupported or
misspelled keys. This audit must include base-only values that the effective
layered lint does not validate. `fullsend lint` is not necessarily a strict
key validator: `v0.56.1` uses non-strict YAML unmarshalling, so a typo such as
`runtim: pi` is ignored.

For `v0.56.1`, migrate `create_issues.allow_targets` to
`tracker.<provider>.allowed_targets`. For GitHub, repository destinations
are listed at `tracker.github.allowed_targets.repos`. The `repos` list holds
destinations beyond the source repository; preserve all prior external
entries and do not add the source repository, which remains implicitly
allowed.

The `tracker` mapping replaces the parent object as a whole. Each layer that
sets `tracker` must provide the complete effective provider configuration
and `allowed_targets` it needs. An overlay must repeat required external
targets rather than relying on them to merge from the base layer.

## Lint effective configuration

Run the selected release's `fullsend lint` for the semantic and harness
checks it supports. For `v0.56.1`, lint validates merged agents and model
aliases but does not validate some inherited values, such as `runtime` or
`version`, from `config.base.yaml` when the overlay omits them. The config
audit above must validate base-only values that lint misses.

When `config.yaml` exists, lint the effective layered config. Do not lint a
standalone copy of `config.base.yaml` when an overlay exists: that resolves
base harnesses even when the overlay disables an agent or replaces its
source.

When only `config.base.yaml` exists, its layered fields are effective,
alongside code defaults. For `v0.56.1`, `fullsend lint` requires
`config.yaml`, so lint a temporary copy that retains `config.base.yaml` and
adds an empty `config.yaml` overlay. Do not promote the base file by copying
it to `config.yaml`: `authorization` and `trace_context` are overlay-only,
so promotion would validate settings the target ignores. The config audit
must identify those settings as ineffective when they occur in the base.
Copy `.fullsend-cache` into the temporary directory for offline resolver use
during this lint invocation; the `EXIT` trap removes that temporary cache
after lint exits.

```bash
(
  set -euo pipefail
  cd "$FULLSEND_TARGET_ROOT"
  if [[ -f .fullsend/config.yaml ]]; then
    fullsend lint --fullsend-dir .fullsend --forge github
  elif [[ -f .fullsend/config.base.yaml ]]; then
    BASE_AUDIT_DIR=$(mktemp -d)
    trap 'rm -rf "$BASE_AUDIT_DIR"' EXIT
    cp -R .fullsend/. "$BASE_AUDIT_DIR/"
    printf '%s\n' '{}' > "$BASE_AUDIT_DIR/config.yaml"
    fullsend lint --fullsend-dir "$BASE_AUDIT_DIR" --forge github
  else
    printf '%s\n' 'No .fullsend config files; no per-repo config to validate.'
  fi
)
```

`fullsend lint` may fetch remote harness resources and write
`.fullsend/.fullsend-cache`; use `--offline` only when all required resources
are cached. Use normal lint mode, not `--strict`: warnings remain visible
without blocking on unrelated existing deprecations. If the selected CLI
lacks `lint`, use a documented non-mutating config validator from that
release; if no suitable validator exists or the check cannot run, report a
blocker.

## Repository checks

Inspect the target repository's documentation and CI/build configuration to
identify its actual commands, then run them from `FULLSEND_TARGET_ROOT`. For
a non-ProtoBot target, use that repository's checks. For ProtoBot, run:

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

If a category has no applicable command or a check cannot run, record that
limitation; never report it as passed. Record each command's actual result.
If any check fails or cannot run, report the failure or blocker; do not claim
the upgrade is verified.

## PR description

Prepare a structured PR description that includes:

- **Release and migration notes:** the previous and selected release tags,
  release-note links for both `fullsend-ai/fullsend` and
  `fullsend-ai/agents`, breaking changes, and how each migration requirement
  was handled.
- **Harness compatibility:** every affected overlay and the evidence for its
  compatibility with the selected release. A matching checksum proves file
  identity, not behavioral compatibility.
- **Workflow synchronization:** the generated workflow changes and each
  reviewed downstream addition retained.
- **Repository settings:** approved setting names and actions, without
  plaintext secret values.
- **Verification:** the selected-release config audit and lint when either
  config layer exists, plus the target's applicable integrity, lint, and test
  commands with their observed results.
