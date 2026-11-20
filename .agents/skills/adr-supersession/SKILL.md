---
name: "adr-supersession"
description: >
  Exact format for the authorized accepted-to-superseded status
  transition on architecture decision records under `docs/decisions/`.
  Use when superseding an accepted ADR or when the `adr-amendments`
  pre-commit hook rejects a status change.
---

# ADR Supersession Format

`AGENTS.md` Rule 7 makes accepted and superseded architecture decision
records immutable, with one authorized exception: a dated
accepted-to-superseded status-line transition paired with an appended
dated amendment block. The `adr-amendments` local pre-commit hook
(`scripts/check_adr_amendments.py`) enforces this exact format; a
transition that does not match every part below is rejected as an
unauthorized rewrite. The hook enforces only the status line, the marker
line, and the marker placement; a reviewer must confirm the prose citation.

## Status-line transition

Change the ADR's header status line from:

```text
> Status: **Accepted** — Month Year
```

to exactly:

```text
> Status: **Superseded by ADR-NNNN** — Month Year
```

`ADR-NNNN` is the superseding ADR's number, and the month/year date is
required — an undated `> Status: **Superseded by ADR-NNNN**` line is
rejected. Month names must be full English names. No other text may
follow the status line on that line.

## Appended amendment block

Immediately after the status-line edit, append a block after the
ADR's last pre-amendment line (not inserted earlier in the file, and
not merged into an existing paragraph):

```text
**[Amended Month Year]**
<Prose citing the change, its rationale, and references to updated
architecture documents.>
```

The `**[Amended Month Year]**` marker must occupy its own line, with
nothing else on it. Month names must be full English names. The marker
line must not sit inside a fenced (` ``` `) code block.

## Worked example

`docs/decisions/0003-ears-manager-storage-layout.md` demonstrates this
transition for ADR-0004's adoption of ProtoBot V2. Its header status
line was updated to:

```text
> Status: **Superseded by ADR-0004** — November 2026
```

and the following block was appended after the file's last line:

```text
**[Amended November 2026]**
ADR-0004 adopts ProtoBot V2 and deletes `ears-manager` and `.protobot/`
rather than reworking this storage layout (removal PR R1, tracked in
issue #406). This ADR's file stays in place per the supersession,
rename, and deletion lifecycle in `AGENTS.md` Rule 7; only its status
line changes. See [ADR-0004: Adopt ProtoBot
V2](0004-adopt-protobot-v2.md) and updated architecture documents
under `docs/architecture/` for the replacement decision.
```

## What stays unauthorized

- Deleting or renaming an accepted or superseded ADR, including when a
  removal change retires the component it describes.
- Rewriting any other historical line, even alongside an otherwise
  valid status transition.
- A second, unrelated status-pattern line inserted anywhere else in
  the file.

Ordinary free-text amendments to an ADR that is not being superseded
(for example, the existing amendments in `docs/decisions/0001-*.md`)
are unaffected: any inserted text is allowed as long as no baseline
line is deleted or rewritten. The marker-alone-on-its-own-line format
above is required only for the authorized supersession transition.
