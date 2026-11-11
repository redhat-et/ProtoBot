# Fullsend CLI installation

## Classify the current executable

Record the resolved executable path from `command -v fullsend`, its version, and
its installation ownership. Compare semantic versions with the exact target
selected by the [Fullsend upgrade skill](../SKILL.md#step-1-select-the-release-and-check-the-cli).

- If the installed release equals the target, reuse it without reinstalling.
- If the CLI is missing or older, install or upgrade to the target before
  preparing the repository changes. Report the command and obtain approval for
  a local tool change unless the user's request already authorizes it.
- If the CLI is newer, a development build, or has unknown provenance, do not
  silently downgrade it or overwrite a custom wrapper. Prefer an isolated
  target-release binary for this invocation.

Preserve the existing installation method when it is positively identified.
For a package-manager-owned executable, use that manager's documented upgrade
command only when it can supply the exact target release; do not replace a
manager-owned binary with an extracted archive.

For a manually installed CLI, use `fullsend self-update` only when the active
CLI's help documents support for the exact target and `--dry-run`. The command
was introduced in v0.55.0 and verifies the release's Sigstore signature.
Review `fullsend self-update --version "$FULLSEND_TARGET_VERSION" --dry-run`,
then run the same command without `--dry-run` only after the required local
tool-change approval. Older CLI versions may not support this command; use the
verified archive procedure below instead. Never guess an installer URL or
self-update syntax.

## Verify a release archive before installation

The official distribution is the GitHub release archive. If the currently
installed CLI does not document a signature-verified self-update for the exact
target release, select `FULLSEND_ASSET` and `FULLSEND_CHECKSUM_ASSET` from the
release metadata for the host operating system and architecture. Do not
hard-code a version or assume an archive layout. Download the selected archive
and checksum manifest into a temporary staging directory:

```bash
FULLSEND_STAGE=$(mktemp -d)
gh release download "$FULLSEND_TARGET_VERSION" \
  --repo fullsend-ai/fullsend \
  --pattern "$FULLSEND_ASSET" --pattern "$FULLSEND_CHECKSUM_ASSET" \
  --dir "$FULLSEND_STAGE"
```

Before extracting or executing the archive, compare its locally calculated
SHA-256 with the exact archive entry in the downloaded checksum manifest. Do
not accept a checksum from another release, asset, or source. Inspect archive
members and reject absolute paths or any member whose normalized path escapes
the staging directory; extract only beneath that directory. Install the
verified binary at the existing user-writable location for a manual
installation, or use an isolated directory on `PATH`. Never overwrite a
package-manager-owned executable with an extracted binary or change unrelated
tools such as OpenShell.

Refresh the shell's command lookup after installation. Re-run
`command -v fullsend`, `fullsend --version`, and
`fullsend github setup --help`. Continue only when the executable selected by
`PATH` reports the exact target release and supports the required `--dry-run`
setup command. Resolve installation or `PATH` mismatches rather than proceeding
with a different CLI.
