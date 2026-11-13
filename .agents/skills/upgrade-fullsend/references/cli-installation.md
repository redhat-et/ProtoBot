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
was introduced in `v0.55.0` and verifies the release's Sigstore signature.
Review `fullsend self-update --version "$FULLSEND_TARGET_VERSION" --dry-run`,
then run the same command without `--dry-run` only after the required local
tool-change approval. Older CLI versions may not support this command; use the
verified archive procedure below instead. Never guess an installer URL or
self-update syntax.

## Verify a release archive before installation

Use this fallback only when the installed CLI lacks a signature-verified
self-update for the exact release. Require its Sigstore checksum bundle and stop
if verification fails; a same-origin checksum alone is insufficient.

Derive the archive basename from the validated tag and host `GOOS`/`GOARCH`:
`fullsend_<version-without-v>_<goos>_<goarch>.tar.gz`. Use the fixed
`checksums.txt` and `checksums.txt.sigstore.json` names, and confirm all three
occur in Step 1's asset list. Never pass arbitrary metadata names to shell
commands. Stop for unsupported platforms or missing assets.

```bash
set -euo pipefail
case "$(uname -s):$(uname -m)" in
  Darwin:arm64) FULLSEND_GOOS=darwin; FULLSEND_GOARCH=arm64 ;;
  Darwin:x86_64) FULLSEND_GOOS=darwin; FULLSEND_GOARCH=amd64 ;;
  Linux:aarch64|Linux:arm64) FULLSEND_GOOS=linux; FULLSEND_GOARCH=arm64 ;;
  Linux:x86_64) FULLSEND_GOOS=linux; FULLSEND_GOARCH=amd64 ;;
  *) printf 'unsupported Fullsend release platform\n' >&2; exit 1 ;;
esac
FULLSEND_STAGE=$(mktemp -d)
FULLSEND_ASSET="fullsend_${FULLSEND_TARGET_VERSION#v}_${FULLSEND_GOOS}_${FULLSEND_GOARCH}.tar.gz"
FULLSEND_CHECKSUM_ASSET=checksums.txt
FULLSEND_SIGNATURE_ASSET=checksums.txt.sigstore.json
command -v cosign >/dev/null || {
  printf 'cosign is required to verify the release checksum signature\n' >&2
  exit 1
}
gh release download "$FULLSEND_TARGET_VERSION" \
  --repo fullsend-ai/fullsend \
  --pattern "$FULLSEND_ASSET" \
  --pattern "$FULLSEND_CHECKSUM_ASSET" \
  --pattern "$FULLSEND_SIGNATURE_ASSET" \
  --dir "$FULLSEND_STAGE"
```

Verify the bundle against the Fullsend release workflow identity and GitHub
Actions OIDC issuer before trusting the checksum manifest; stop on failure:

```bash
cosign verify-blob \
  --bundle "$FULLSEND_STAGE/$FULLSEND_SIGNATURE_ASSET" \
  --certificate-identity \
  "https://github.com/fullsend-ai/fullsend/.github/workflows/release.yml@refs/tags/$FULLSEND_TARGET_VERSION" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$FULLSEND_STAGE/$FULLSEND_CHECKSUM_ASSET"
```

Only after verification, compare the archive SHA-256 with the exact entry for
`FULLSEND_ASSET` in the signed manifest. Do not accept a checksum from another
release, asset, or source.
Inspect archive members and reject absolute paths or any member whose normalized
path escapes the staging directory; extract only beneath that directory. Install
the verified binary at the existing user-writable location for a manual
installation, or use an isolated directory on `PATH`. Never overwrite a
package-manager-owned executable with an extracted binary or change unrelated
tools such as OpenShell.

Refresh the shell's command lookup after installation. Re-run
`command -v fullsend`, `fullsend --version`, and
`fullsend github setup --help`. Continue only when the executable selected by
`PATH` reports the exact target release and supports the required `--dry-run`
setup command. Resolve installation or `PATH` mismatches rather than proceeding
with a different CLI.
