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
entries, files larger than 1 MiB, and malformed YAML produce
diagnostics. Requirement relationship types are restricted to
`depends-on`, `supersedes`, and `related-to`; unsupported types,
including legacy `conflicts-with`, fail validation. Directed
`depends-on` and `supersedes` graphs must be acyclic; a cycle fails with
`relationship.cycle` and names the participating requirement IDs.
`related-to` associations are exempt. It also scans Gherkin `.feature`
files under `tests/` for `@req:<ID>` tags and fails when a tag does not
resolve to a known requirement record. Active and retired IDs both count
as known; unknown or misspelled IDs fail with line-level diagnostics.
Schema, EARS, identifier, and remaining reference checks land in
follow-on work.

`--target` is used as given. The command does not apply
`filepath.Clean`, so relative paths and trailing slashes stay in the
reported output.

`layout.Read` discovers `*.yaml` files in those directories. It does
not require requirement basenames to match `REQ-<SCOPE>-<NNNNN>.yaml`
or interface basenames to match the in-record name. No current check
covers basename-to-record-ID or basename-to-name matching (tracked in
[#443](https://github.com/redhat-et/ProtoBot/issues/443)).
Requirement ID format, uniqueness, and related in-record identifier
checks are enforced by follow-on check groups
[#385](https://github.com/redhat-et/ProtoBot/issues/385) and
[#395](https://github.com/redhat-et/ProtoBot/issues/395).

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

Target errors (such as a missing, unstattable, or non-directory
`--target`) print an uncoded message to stderr:

```text
validator: <message>
```

Layout diagnostics are written to stderr, one per line:

```text
validator: <code>: <path>: <message>
```

This line format is stable for external tool integration. Current
layout codes are `layout.read_failed`, `layout.unexpected_file`, and
`layout.parse_failed`. A file larger than 1 MiB produces
`layout.read_failed`.

Build and test it from this directory:

```sh
go build -o validator ./cmd/validator
./validator --target /path/to/checkout --base main
go test ./...
go vet ./...
```

[design]: ../docs/architecture/ProtoBotV2.md
