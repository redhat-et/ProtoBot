# Drafting Table OpenCode fixture

Run the deterministic headless replay against the pinned OpenCode 1.18.30
binary:

```sh
PYTHONDONTWRITEBYTECODE=1 OPENCODE_BIN=/path/to/opencode-1.18.30/bin/opencode \
  python -m unittest discover -s tests/drafting-table -p 'test_*.py' -v
```

The driver builds the real `ears-manager`, `source-control-manager`, and
`drafting-table-guard` binaries from the existing Go modules, provisions an
isolated temporary Git project, and uses a loopback-only recorded model endpoint
plus recording WMS and `gh` stubs. It never edits the checkout or registered
specification artifacts. The contract fixture inputs, expected state, and
deferred steps are in
`docs/architecture/fixtures/drafting-table-opencode-golden.jsonl`.

The actual `ears-manager artifact list` implementation is used by the guard
and verified directly; there is no CLI compatibility wrapper in this fixture.
The temporary `build` profile allows Bash so non-role redirection vectors reach
the guard. OpenCode 1.18.30 does not register a native `apply_patch` tool in the
resolved agent tool set. The fixture starts Drafting Table from a subdirectory,
then sends the documented `apply_patch` hook shape directly to the built guard
in both roles. It asserts root-relative `docs/vision.md` refusal and byte
preservation. A native OpenCode `apply_patch` invocation remains deferred; no
checked-out binding permission is changed.
