# Validator

`validator` is the ProtoBot V2 specification validator. It is a
deterministic, network-free Go command. It reads the checkout at
`--target` and records `--base` as the comparison argument; it does
not contact a tracker, a network service, or an external daemon.

The design is the [ProtoBot V2 validator][design]. This module
implements the command line, spec directory reader, record schema
checks, and relationship-type allowlist (issues #381, #383, #385, and #387).
The command line accepts the checkout path and comparison base. It reads
requirement and interface YAML records from `spec/requirements/` and
`spec/interfaces/` in the target checkout. Missing directories are treated
as empty. Unreadable files, non-YAML entries, and malformed YAML produce
diagnostics. Requirement relationship types are restricted to `depends-on`,
`supersedes`, and `related-to`; unsupported types, including legacy
`conflicts-with`, fail validation. It also scans Gherkin `.feature` files
under `tests/` for `@req:<ID>` tags and fails when a tag does not resolve to
a known requirement record. Active and retired IDs both count as known;
unknown or misspelled IDs fail with line-level diagnostics. Requirement
records must include `id`, `type`, `text`, `applies_to`, `relationships`,
and `status`. `applies_to` must name at least one interface or scope. Blank
selectors count as absent. Interface records must include `name`, `kind`,
and `description`. Status values are `active` or `retired`. Legacy metadata
(`verification`, `provenance`, `created`) and other unparsed fields are
rejected. EARS, identifier, and remaining reference checks (#388) land in
follow-on work.

`--target` is used as given. The command does not apply
`filepath.Clean`, so relative paths and trailing slashes stay in the
reported output.

```text
validator --target PATH --base REF
validator --help
validator -h
```

Build and test it from this directory:

```sh
go build -o validator ./cmd/validator
./validator --target /path/to/checkout --base main
go test ./...
go vet ./...
```

[design]: ../docs/architecture/ProtoBotV2.md
