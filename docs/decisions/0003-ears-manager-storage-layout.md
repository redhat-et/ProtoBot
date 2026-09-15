# ADR-0003: EARS Manager Storage Layout and Schema Version Keys

> Status: **Accepted** — September 2026

## Context

ADR-0001 selected one-file-per-record YAML, and ADR-0002 defined the
logical record schemas. Neither decision selected the directories for
the structured requirement, interface, and change-set records or the
names of the per-store version keys in `project.yaml`.

The Go foundation needs those choices to be stable before later issues
implement validation and CLI operations.

## Decision

The project configuration contains a `stores` block and one schema
version for each configuration store:

```yaml
schema_versions:
  project: 1
  specification: 1
stores:
  requirements: .protobot/requirements
  interfaces: .protobot/interfaces
  change_sets: .protobot/change-sets
```

The default store paths are:

| Store | Default path | Record filename |
| --- | --- | --- |
| Requirements | `.protobot/requirements/` | `REQ-<SCOPE>-<NNNNN>.yaml` |
| Interfaces | `.protobot/interfaces/` | `<lower-kebab-id>.yaml` |
| Change sets | `.protobot/change-sets/` | `cs-<nnnnn>.yaml` for `CS-<NNNNN>` |

Projects may register different relative paths in `stores`. Every path
must remain inside the project root after symlink resolution. Absolute
paths, traversal paths, and symlink escapes are invalid.

`project.yaml` remains the home of artifact-registry entries. The
structured interface records in the interface store are distinct from
opaque interface IDL or prose artifacts registered in that list.

`ears-manager` supports schema version `1` for both `project` and
`specification`. It refuses missing, invalid, or newer versions. Older
versions require an explicitly implemented reviewed migration and are
not silently interpreted as version `1`.

The stable filename mapping is owned by `ears-manager`:

- Requirement IDs match `REQ-[A-Z][A-Z0-9-]*-[0-9]{5}`.
- Interface IDs use lowercase kebab-case.
- Change-set IDs match `CS-[0-9]{5}` and use lowercase `cs-` in filenames.

Canonical serialization uses schema field order, stable scalar styles,
sorted set-like lists, LF endings, no trailing whitespace, and one final
newline. Empty optional fields use one consistent representation. The
storage layer rejects malformed YAML, duplicate keys, aliases, merge
keys, and custom tags before typed decoding.

## Consequences

- Requirement, interface, and change-set records have predictable,
  conflict-minimizing locations.
- Project discovery can resolve all managed paths without guessing from
  the caller's current directory.
- Schema-version failures are deterministic and can be surfaced by the
  later CLI contract.
- Semantic validation of EARS text, references, relationships, and
  impact remains separate from storage parsing.

## Related Documents

- [ADR-0001: One-File-Per-Record YAML Storage Format](0001-requirements-storage-format.md)
- [ADR-0002: EARS Specification Record Schema](0002-ears-specification-record-schema.md)
- [System Components](../architecture/components.md#content-storage-model)
- [Git and Project-Repository Integration](../architecture/git-integration.md#registered-artifact-paths)
