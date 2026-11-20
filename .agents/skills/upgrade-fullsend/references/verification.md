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

## Migrate tracker policy

### Migrate removed keys and presets

For `v0.56.1`, migrate every `create_issues` setting in each layer to
`tracker.<provider>.allowed_targets`. Runtime loading rejects the removed key
in either `.fullsend/config.yaml` or `.fullsend/config.base.yaml`. For GitHub,
preserve external repository targets at
`tracker.github.allowed_targets.repos`; the source repository is implicitly
allowed.

A `config_base` preset can still contain `create_issues` after the overlay is
updated. Migrate the preset at its source, update its pinned `sha256` if set,
and rerun `fullsend repos install` to deliver the updated base. In `v0.56.1`,
`fullsend repos install` migrates managed `config:` entries but does not
convert presets or markerless local config files.

### Keep post-script targets in the overlay

For `v0.56.1` retro or triage post-scripts that need external targets, put the
complete tracker policy in `.fullsend/config.yaml`: those scripts read the
overlay, not `config.base.yaml`. For `fullsend repos`-managed repositories,
declare the policy in the manifest's applicable `config` section and rerun
`fullsend repos install`. For unmanaged installations, create or update
`.fullsend/config.yaml`; updating only the preset does not enable these
operations.

### Locate managed configuration

A repository is fleet-managed when `fullsend repos` installs it from a
`repos.yaml` manifest. Locate that manifest from a `Source manifest:` marker
in `.fullsend/config.yaml` when present, or from the repository's deployment
documentation or workflow. Resolve the effective `config_base.source` from
that manifest. If the manifest or preset source cannot be located, report a
blocker; do not guess or edit generated `.fullsend` files. See the [v0.56.1 Config
Reference](https://github.com/fullsend-ai/fullsend/blob/v0.56.1/docs/reference/config-reference.md).

### Check whole-object replacements

`tracker` replaces its parent object as a whole. Each layer that sets it must
provide the complete effective provider configuration and `allowed_targets`.
When an overlay replaces a whole-object field, identify base-only children
shadowed by the overlay as ineffective, even when schema-valid.

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
alongside code defaults. Before linting, verify from the selected release's CLI
docs/source whether `fullsend lint` consumes `config.base.yaml`, requires
`config.yaml`, and validates inherited values. For `v0.56.1`, `fullsend lint`
requires `config.yaml`; lint a temporary copy retaining `config.base.yaml` and
adding an empty `config.yaml`. Do not promote the base to `config.yaml`: in
`v0.56.1`, `authorization` and `trace_context` are overlay-only. Audit these
as ineffective when they occur in the base. For another release, use its
documented base-only lint procedure; if none can be established, report a
blocker.
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
    if [[ "$FULLSEND_TARGET_VERSION" != "v0.56.1" ]]; then
      printf '%s\n' \
        'The v0.56.1 workaround does not apply; use the selected release’s documented base-only lint procedure.' >&2
      exit 1
    fi
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
