# Validator

`validator` is the ProtoBot V2 specification validator. It is a
deterministic, network-free Go command. It reads the checkout at
`--target` and compares requirement identifiers and parsed requirement
text against `--base`; it does not contact a tracker, a network service, or
an external daemon.

## Prerequisites

`--target` must be the root of a Git working tree, `git` must be
available on `PATH`, and `--base` must resolve to a commit already
present in that checkout. `--base` must resolve to a distinct commit from
the target checkout `HEAD`; pass `--allow-self-comparison` only when a
harness needs to compare a working tree against `HEAD`. The command
operates locally and does not fetch.
Git 2.45 or later is required for GIT_NO_LAZY_FETCH. Otherwise a partial clone
can fetch a missing base object over the network.

The design is the [ProtoBot V2 validator][design]. This module implements the
command line, spec directory reader, record schema checks, relationship-type
allowlist, relationship acyclicity, replacement and retirement rules,
reference resolution, EARS shape checks, requirement identifier checks,
basename-to-id and basename-to-name matching, and parsed-text
immutability against comparison base
(issues #381, #383, #385, #387, #388, #390, #392, #395, #397, #399, #402,
\#459, #462, #463, #468, #471, #479, #497, and #475). The command line accepts
the checkout path and comparison base.
It reads requirement and interface YAML records from `spec/requirements/` and
`spec/interfaces/` in the target checkout. Missing directories are treated as
empty. Unreadable files, non-YAML entries, `.yaml` records larger than 1 MiB,
and malformed YAML produce diagnostics. A mapping key that contains `.`, `[`,
or `]` fails with `layout.parse_failed`. Requirement relationship types are
restricted to `depends-on`, `supersedes`, and `related-to`; unsupported
types, including legacy `conflicts-with`, fail validation. Directed
`depends-on` and `supersedes` graphs must be acyclic; a cycle fails with
`relationship.cycle` and names the participating requirement IDs. A
multi-node cycle names its IDs as a comma-separated list; a singleton
cyclic strongly connected component (a self-loop) is instead named as a
directed edge (`REQ-...->REQ-...`) so the diagnostic is not mistaken for
a truncated one-ID list. `related-to` associations are exempt.
A `supersedes` target must have
`status: retired`. A newly introduced `supersedes` edge must retire that
predecessor in the same specification change relative to `--base`. An edge
is newly introduced when it is present in the working tree and absent from
`--base`; historical `supersedes` edges already present in `--base` are
exempt from same-specification-change retirement enforcement. Retirement
without a successor is allowed. Retired records must not be deleted. Each
non-blank `applies_to.interfaces` selector must equal an interface
record's `name` (matching is case-sensitive), and requirement IDs in
`relationships[].target` must resolve to declared records; retired
requirement targets satisfy resolution. A missing interface name or
requirement target fails with `reference.not_found`. An interface name
declared by more than one interface record fails with
`reference.ambiguous` when an `applies_to.interfaces` selector names it.
Duplicate requirement IDs fail with `requirement.duplicate_id` on the
declaring records, not with `reference.ambiguous`. Requirement IDs must
match `REQ-<SCOPE>-<NNNNN>`, where `<SCOPE>` begins with an uppercase
letter and consists of one or more uppercase alphanumeric segments
separated by single hyphens (consecutive, leading, or trailing
hyphens are rejected), be unique in the working tree, and must not be
deleted or reused relative to the comparison base. Requirement file
basenames must use that same identifier pattern and equal the
in-record `id`. Interface file basenames must equal the declared
`name`. An interface `name` (and its file basename) must be a well-formed
single path segment: non-empty, free of path separators (`/` or `\`), and
without a leading or trailing dot. This rejects the degenerate segments `.`
and `..`, hidden-file-style names such as `.hidden`, and trailing-dot names
such as `service.`, while still permitting interior dots such as
`service..api` or `api.v1`. A non-empty `name` that is not a single path
segment fails with `interface.invalid_name`; an empty or whitespace-only
`name` fails with `interface.missing_field`. An invalid file basename
fails with `interface.invalid_basename` even when the basename equals an
equally degenerate `name`. It also scans Gherkin `.feature` files under `tests/`
for `@req:<ID>`
tags and fails when a tag does not resolve to a known requirement record.
Active and retired IDs both count as known; unknown or misspelled IDs fail
with line-level diagnostics. Requirement records must include `id`, `type`,
`text`, `applies_to`, `relationships`, and `status`. `applies_to` must
name at least one interface or scope. A blank or whitespace-only
selector in `applies_to.interfaces` or `applies_to.scopes` is
`requirement.missing_field` on the indexed field and does not count as a
named selector. A blank or whitespace-only `relationships[].target` is
`requirement.missing_field` on the indexed field. Interface records must
include `name`, `kind`, and `description`. Status values are `active` or
`retired`. Legacy metadata (`verification`, `provenance`, `created`) and
other unparsed fields are rejected.
Requirement text is checked for exactly one `shall` and for a match
against the declared EARS `type` (`ubiquitous`, `event-driven`,
`state-driven`, `unwanted-behavior`, `optional-feature`, or `complex`).
The declared `type` value is matched exactly (case-sensitive) against
those constants; a variation such as `type: State-Driven` fails with
`ears.invalid_type`. Statement pattern matching and the `shall` count
use case-insensitive regex matching per [ADR-0002][adr-0002]: the
five simple EARS patterns and the `If`...`then` complex trigger use
`(?is)` (case-insensitive, dot matches newline), the individual
complex trigger keywords `When`, `While`, and `Where` use `(?i)`
(case-insensitive only, no dot-matches-newline), and the obligation
keyword uses `(?i)\bshall\b`. State-driven statements require the
keyword `While` rather than an undeclared alias such as `During`;
case variations such as `while` and `While` match. Complex statements
require at least two distinct trigger keywords from `When`, `While`,
`Where`, and the pair `If`...`then`; each keyword counts once. Parsed
requirement `text` is compared against `--base` for every existing ID;
YAML scalar reformatting that preserves the parsed string passes, and
metadata-only edits such as status transitions pass. A wording change
under an existing ID fails.

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
with `requirement.invalid_basename`. When `id` is non-empty, a valid basename
that does not equal `id` fails with `requirement.id_basename_mismatch`.
When `name` is non-empty and the basename itself is valid, an interface
basename that does not equal the declared `name` fails with
`interface.name_basename_mismatch`. Parsed records remain in
the snapshot so other checks still run. When a target record fails to load,
referring records do not receive a cascading `reference.not_found`; the layout
diagnostic remains the actionable error. Parse-failure suppression looks up
`<store>/<target>.yaml` in the same store directory, so interface suppression
assumes the file stem matches the declared `name`. A target that contains `/`
or `\` is not eligible for suppression. Requirement identity is
path-keyed: moving a record to a new file path with the same ID emits
`requirement.reused_id` at the new path and no `requirement.deleted`. Two
exceptions apply: moving a record from a non-canonical path to its canonical
`spec/requirements/{id}.yaml` path to correct its basename suppresses
`requirement.reused_id`, and replacing exactly one non-canonical unparsed or
invalid-ID requirement file from the comparison base with exactly one canonical
`spec/requirements/{valid-id}.yaml` record suppresses `requirement.deleted`.
The exception applies only when the repair is the only new requirement record
in the change set; `REQ-FIX-00001.yaml` plus `REQ-NEW-00001.yaml` still emits
`requirement.deleted`. Each legacy file needs its own change set; two legacy
files cannot be repaired together, and two sequential repairs work. The
validator cannot read the id of an unreadable base record, so one removed file
and one unrelated added record also pass.

```text
validator --target PATH --base REF [--allow-self-comparison]
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
  validator --target PATH --base REF [--allow-self-comparison]
  validator --help
  validator -h

  --target PATH              checkout directory to validate
  --base REF                 comparison base for identity and immutability checks
  --allow-self-comparison    allow --base to resolve to the target checkout HEAD
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
working tree repairs the file) and a comparison base that contains no
requirement records are prefixed with
`validator: note: comparison base <ref>: ...` and are not diagnostics.
Check the exit status first. A line without a `<code>` field is not a
diagnostic.

This line format is stable for external tool integration. Current codes
include layout codes (`layout.read_failed`, `layout.unexpected_file`,
`layout.parse_failed`), revision check codes
(`revision.not_found`, `revision.read_failed`, `revision.unsupported_entry`,
`revision.unexpected_file`, `revision.parse_failed`,
`revision.self_comparison`, `revision.empty_base`), requirement check codes
(`requirement.missing_field`, `requirement.invalid_status`,
`requirement.unexpected_field`, `requirement.invalid_id`,
`requirement.duplicate_id`, `requirement.deleted`,
`requirement.reused_id`, `requirement.text_immutable`,
`requirement.invalid_basename`, `requirement.id_basename_mismatch`),
interface check codes (`interface.missing_field`,
`interface.unexpected_field`, `interface.invalid_name`,
`interface.invalid_basename`, `interface.name_basename_mismatch`), reference
check codes (`reference.not_found`, `reference.ambiguous`),
specification check codes (`relationship.invalid_type`,
`relationship.cycle`, `relationship.superseded_requirement_active`,
`relationship.superseded_requirement_not_retired_in_change`), EARS check codes
(`ears.shall_count`,
`ears.pattern_mismatch`, `ears.invalid_type`), and test-tag codes
(`testtags.read_failed`, `testtags.unexpected_file`,
`testtags.unresolved`). A `.yaml` record larger than 1 MiB produces
`layout.read_failed`.

An `ears.pattern_mismatch` message appends the expected template
syntax for author remediation. A state-driven mismatch, including an
undeclared alias such as `During`, reports:

```text
requirement text does not match the declared state-driven pattern; expected "While <state>, the <system> shall <response>"
```

A `complex` mismatch reports:

```text
requirement text does not match the declared complex pattern; expected at least two distinct trigger keywords
```

Build and test it from this directory:

```sh
go build -o validator ./cmd/validator
./validator --target /path/to/checkout --base origin/main
go test ./...
go vet ./...
```

Continuous integration selects the comparison base with
`scripts/select_comparison_base.py` and then runs the same check through
`scripts/check_spec_validator.py`. The candidate is compared against the
approved base: the base branch for a pull request or merge group, the exact
previous commit for a push to the default branch, and the default branch for a
push to any other branch or for a manual run. A default-branch push fails when
the previous commit is unavailable. When the comparison base resolves to the
checked-out commit, the base-relative checks are skipped. Any diagnostic fails
the `validator` job.

[design]: ../docs/architecture/ProtoBotV2.md
[adr-0002]: ../docs/decisions/0002-ears-specification-record-schema.md#ears-template-validation
