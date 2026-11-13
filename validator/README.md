# Validator

`validator` is the ProtoBot V2 specification validator. It is a
deterministic, network-free Go command. It reads the checkout at
`--target` and records `--base` as the comparison argument; it does
not contact a tracker, a network service, or an external daemon.

The design is the [ProtoBot V2 validator][design]. This module
implements the command line and the spec directory reader (issues #381
and #383). The command line accepts the checkout path and comparison
base. It reads requirement and interface YAML records from
`spec/requirements/` and `spec/interfaces/` in the target checkout.
Missing directories are treated as empty. Unreadable files, non-YAML
entries, and malformed YAML produce diagnostics. Requirement
relationship types are restricted to `depends-on`, `supersedes`, and
`related-to`; unsupported types, including legacy `conflicts-with`, fail
validation. Directed `depends-on` and `supersedes` graphs must be
acyclic; a cycle fails with `relationship.cycle` and names the
participating requirement IDs. `related-to` associations are exempt.
Interface identifiers in `applies_to.interfaces` and
requirement IDs in `relationships[].target` must resolve to declared
records; retired requirement targets satisfy resolution. It also scans
Gherkin `.feature` files under `tests/` for `@req:<ID>` tags and fails
when a tag does not resolve to a known requirement record. Active and
retired IDs both count as known; unknown or misspelled IDs fail with
line-level diagnostics. Schema, EARS, and identifier checks land in
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
