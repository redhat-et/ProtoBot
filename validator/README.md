# Validator

`validator` is the ProtoBot V2 specification validator. It is a
deterministic, network-free Go command. It reads the checkout at
`--target` and records `--base` as the comparison argument; it does
not contact a tracker, a network service, or an external daemon.

The design is the [ProtoBot V2 validator][design]. This module
implements the command line, spec directory reader, record schema
checks, relationship-type allowlist, relationship acyclicity, and
reference resolution (issues #381, #383, #385, #387, #388, #390,
and #402). The command line accepts the checkout path and comparison
base. It reads requirement and interface YAML records from
`spec/requirements/` and `spec/interfaces/` in the target checkout.
Missing directories are treated as empty. Unreadable files, non-YAML
entries, `.yaml` records larger than 1 MiB, and malformed YAML produce
diagnostics. A mapping key that contains `.`, `[`, or `]` fails with
`layout.parse_failed`. Requirement relationship types are restricted to
`depends-on`, `supersedes`, and `related-to`; unsupported types,
including legacy `conflicts-with`, fail validation. Directed
`depends-on` and `supersedes` graphs must be acyclic; a cycle fails with
`relationship.cycle` and names the participating requirement IDs.
`related-to` associations are exempt. Each `applies_to.interfaces` entry
must equal an interface record's `name` (matching is case-sensitive),
and requirement IDs in `relationships[].target` must resolve to declared
records; retired requirement targets satisfy resolution. It also scans
Gherkin `.feature` files under `tests/` for `@req:<ID>` tags and fails
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
State-driven statements require the keyword `While`. Identifier checks
land in follow-on work.

`--target` is used as given. The command does not apply
`filepath.Clean`, so relative paths and trailing slashes stay in the
reported output.

`layout.Read` discovers `*.yaml` files in those directories. It does
not require requirement basenames to match `REQ-<SCOPE>-<NNNNN>.yaml`
or interface basenames to match the in-record name. No current check
covers basename-to-record-ID or basename-to-name matching; that matching
is not implemented yet.

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
spaces to ensure single-line output. Check the exit status first.
A line without a `<code>` field is not a diagnostic.

This line format is stable for external tool integration. Current codes
include layout codes (`layout.read_failed`, `layout.unexpected_file`,
`layout.parse_failed`), specification check codes (such as
`relationship.invalid_type`, `relationship.cycle`), and test-tag codes
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
