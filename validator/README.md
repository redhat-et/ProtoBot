# Validator

`validator` is the ProtoBot V2 specification validator. It is a
deterministic, network-free Go command. It reads a checkout and a
comparison base; it does not contact a tracker, a network service, or
an external daemon.

This module is the foundation for later check groups. The command line
accepts the checkout path and comparison base. It reads requirement and
interface YAML records from `spec/requirements/` and `spec/interfaces/`
in the target checkout. Missing directories are treated as empty.
Unreadable files, non-YAML entries, and malformed YAML produce
diagnostics. It also scans Gherkin `.feature` files under `tests/` for
`@req:<ID>` tags and fails when a tag does not resolve to a known
requirement record. Active and retired IDs both count as known; unknown
or misspelled IDs fail with line-level diagnostics. Schema, EARS,
identifier, and remaining reference checks land in follow-on work.

```text
validator --target PATH --base REF
validator --help
```

Build and test it from this directory:

```sh
go build -o validator ./cmd/validator
./validator --target /path/to/checkout --base main
go test ./...
go vet ./...
```
