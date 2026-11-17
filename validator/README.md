# Validator

`validator` is the ProtoBot V2 specification validator. It is a
deterministic, network-free Go command. It reads the checkout at
`--target` and compares requirement identifiers and parsed requirement
text against `--base`; it does not contact a tracker, a network service, or
an external daemon.

## Prerequisites

`--target` must be the root of a Git working tree, `git` must be
available on `PATH`, and `--base` must resolve to a commit already
present in that checkout. The command operates locally and does not fetch.
Git 2.45 or later is required for GIT_NO_LAZY_FETCH. Otherwise a partial clone
can fetch a missing base object over the network.

The design is the [ProtoBot V2 validator][design]. This module implements the
command line, spec directory reader, record schema checks, relationship-type
allowlist, relationship acyclicity, replacement and retirement rules,
reference resolution, EARS shape checks, requirement identifier checks,
basename-to-identifier matching, and parsed-text immutability against
comparison base (issues #381, #383, #385, #387, #388, #390, #392, #395, #397,
#399, #402, and #463). The command line accepts the checkout path and
comparison base. It reads requirement and interface YAML records from
`spec/requirements/` and `spec/interfaces/` in the target checkout. Missing
directories are treated as empty. Unreadable files, non-YAML entries, `.yaml`
records larger than 1 MiB, and malformed YAML produce diagnostics. A mapping
key that contains `.`, `[`, or `]` fails with `layout.parse_failed`.
Requirement relationship types are restricted to `depends-on`, `supersedes`,
and `related-to`; unsupported types, including legacy `conflicts-with`, fail
validation. Directed `depends-on` and `supersedes` graphs must be acyclic;
a cycle fails with `relationship.cycle` and names the participating requirement
IDs. `related-to` associations are exempt. A `supersedes` target must have
`status: retired`. A newly introduced `supersedes` edge must retire that
predecessor in the same specification change relative to `--base`. Retirement
without a successor is allowed. Retired records must not be deleted. Each
`applies_to.interfaces` entry must equal an interface record's `name`
(matching is case-sensitive), and requirement IDs in `relationships[].target`
must resolve to declared records; retired requirement targets satisfy
resolution. Requirement IDs must match `REQ-<SCOPE>-<NNNNN>`, be unique in the
working tree, and must not be deleted or reused relative to the comparison base.
Requirement file basenames must use that same identifier pattern and equal the
in-record `id`. Interface file basenames must equal the declared `name`. It
also scans Gherkin `.feature` files under `tests/` for `@req:<ID>` tags and fails
when a tag does not resolve to a known requirement record. Active and
retired IDs both count as known; unknown or misspelled IDs fail with
line-level diagnostics. Requirement records must include `id`, `type`,
`text`, `applies_to`, `relationships`, and `status`. `applies_to` must
name at least one interface or scope. Blank selectors count as absent.
Interface records must include `name`, `kind`, and `description`. Status
values are `active` or `retired`. Legacy metadata (`verification`,
`provenance`, `created`) and other unparsed fields are rejected.
Requirement text is checked for exactly one `shall` and for a match
against the declared EARS type (`ubiquitous`, `event-driven`,
`state-driven`, `unwanted-behavior`, `optional-feature`, or `complex`).
State-driven statements require the keyword `While`. Parsed requirement
`text` is compared against `--base` for every existing ID; YAML scalar
reformatting that preserves the parsed string passes, and metadata-only
edits such as status transitions pass. A wording change under an
existing ID fails.

`--target` is used as given; the command does not apply
`filepath.Clean`, so relative paths and trailing slashes stay in the
reported output. The command reports diagnostics in the format
`validator: <code>: <path>: <message>` (or
`validator: <code>: <message>` when no path applies) on standard error.
On success, it prints `validator: ok` along with the target and base
arguments on standard output and exits with status 0. Validation failures
exit with status 1; command-line usage errors exit with status 2.

`layout.Read` discovers `*.yaml` files in those directories. After a record
parses, specification checks require a requirement basename to match
`REQ-<SCOPE>-<NNNNN>.yaml` using the same identifier pattern as in-record
`id` values, and to equal that `id`. A malformed requirement basename fails
with `layout.invalid_basename`. A valid basename that does not equal `id`
fails with `spec.id_basename_mismatch`. An interface basename that does not
equal the declared `name` fails with `layout.invalid_basename`. Parsed
records remain in the snapshot so other checks still run. Requirement
identity is path-keyed: moving a record to a new file path counts as a
deletion at the old path and a reuse at the new path.

```text
validator --target PATH --base REF
validator --help
validator -h
```

## Exit status and diagnostics

The command exits with:

- `0` when validation succeeds
- `1` when it reports diagnostics or target errors
- `2` on usage or command-line syntax errors

Usage errors (exit status 2) print an uncoded error message followed by
the command usage:

```text
validator: <message>

usage:
  validator --target PATH --base REF
  validator --help
  validator -h

  --target PATH   checkout directory to validate
  --base REF      comparison base for identity and immutability checks
```

Target errors (exit status 1, such as a missing, unstattable, or
non-directory `--target`) print an uncoded message to stderr:

```text
validator: <message>
```

Revision-level diagnostics (exit status 1, such as an unresolvable
comparison base or failed Git tree read) omit the path:

```text
validator: <code>: <message>
```

Layout, specification, and test-tag diagnostics (exit status 1) are
written to stderr, one per line:

```text
validator: <code>: <path>: <message>
```

`<path>` identifies the affected file, including `:line` when the
diagnostic applies to a specific line. Layout diagnostics quote entry
names containing control characters (e.g.,
`spec/requirements/"a\n::error::injected.yaml"`). At the CLI print
boundary, line breaks in diagnostic paths and messages are replaced with
spaces to ensure single-line output. Informational notes regarding historical
comparison base parse warnings (which do not cause a non-zero exit when the
working tree repairs the file) are prefixed with
`validator: note: comparison base <ref>: ...` and are not diagnostics.
Check the exit status first. A line without a `<code>` field is not a
diagnostic.

This line format is stable for external tool integration. Current codes
include layout codes (`layout.read_failed`, `layout.unexpected_file`,
`layout.parse_failed`, `layout.invalid_basename`), revision check codes
(`revision.not_found`, `revision.read_failed`, `revision.unsupported_entry`,
`revision.unexpected_file`, `revision.parse_failed`), requirement check codes
(`requirement.missing_field`, `requirement.invalid_status`,
`requirement.unexpected_field`, `requirement.invalid_id`,
`requirement.duplicate_id`, `requirement.deleted`,
`requirement.reused_id`, `requirement.text_immutable`), interface check codes
(`interface.missing_field`, `interface.unexpected_field`), reference
check codes (`reference.not_found`, `reference.ambiguous`),
specification check codes (`spec.id_basename_mismatch`,
`relationship.invalid_type`, `relationship.cycle`,
`relationship.superseded_requirement_active`,
`relationship.superseded_requirement_not_in_change`), EARS check codes
(`ears.shall_count`,
`ears.pattern_mismatch`, `ears.invalid_type`), and test-tag codes
(`testtags.read_failed`, `testtags.unexpected_file`,
`testtags.unresolved`). A `.yaml` record larger than 1 MiB produces
`layout.read_failed`.

Build and test it from this directory:

```sh
go build -o validator ./cmd/validator
./validator --target /path/to/checkout --base main
go test ./...
go vet ./...
```

[design]: ../docs/architecture/ProtoBotV2.md
