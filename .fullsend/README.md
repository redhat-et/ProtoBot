# Fullsend configuration

This directory holds the per-repo Fullsend configuration: `config.yaml`,
the local harness overlays in `harness/`, and the pre-script wrappers in
`scripts/`. See ADR 0033 for the per-repo installation mode.

## Offline pre-commit cache bootstrap

Agent sandboxes have no network egress, so `scripts/lint.py` needs a
pre-seeded `PRE_COMMIT_HOME`. The `code` and `fix` harnesses seed it with
a `pre_script` wrapper around `scripts/pre_commit_cache.py`
(`scripts/pre-code-cache.sh` and `scripts/pre-fix-cache.sh`). The wrapper
first runs the pinned upstream pre-script unchanged, so its validation
and exit-78 skip semantics survive, and then warms the hook cache on the
trusted runner, which has network access.

Why a wrapper is required: Fullsend allows one `pre_script` per harness,
and every pinned base harness already sets one. A local override replaces
the base value instead of chaining, so the wrapper must delegate itself.

The wrapper writes the warm cache to
`/tmp/workspace/pre-commit-cache.tar.gz`. A `host_files` entry copies that
tarball into the sandbox at `/tmp/protobot-pre-commit-cache.tar.gz`, and
`scripts/lint.py` restores it into `PRE_COMMIT_HOME` on the first offline
run, deleting it after the restored cache passes its pre-flight.

## Path contract

Hook virtualenvs embed absolute paths, so the runner must build the cache
at the exact path the sandbox unpacks it to: `/sandbox/.cache/pre-commit`,
set as `PRE_COMMIT_HOME` in `env.sandbox` of `harness/code.yaml` and
`harness/fix.yaml`. The cache must also be built with the same Python as
the sandbox image (Ubuntu Noble, `/usr/bin/python3` 3.12), or the hook
environments will not run there.

Before the archive is written, the bootstrap reads each python hook
environment's `pyvenv.cfg` (`version_info` and `base-executable`) and
refuses to publish an archive that was built for another interpreter;
directory names are not trusted, because pre-commit names them after the
hook's `language_version`. It names the found versions and writes no
archive. Use `--sandbox-python <X.Y>` (or
`$PROTOBOT_SANDBOX_PYTHON_VERSION`) to override the expected version for
local runs on another interpreter. A wrong-interpreter cache therefore
fails loudly when a person runs `scripts/pre_commit_cache.py` or the
bootstrap by hand on a host with a different Python, instead of failing
later inside the sandbox. The upgrade-fullsend skill's Step 6 does not
run this script.

The cache is about 250-400 MB as a `.tar.gz` (1.1-1.6 GB on disk),
depending on the platform. The tarball is copied per run and deleted
after the restore, so the sandbox never carries both copies.

## Trust boundary

The runner already executes this repository's hooks itself: Fullsend's
upstream `post_script` runs `pre-commit` on the runner before it pushes.
Warming the same hook cache on the runner therefore adds no new class of
trust. The upstream pre-script content is hash-pinned
(`UPSTREAM_SCRIPT_SHA256` in `scripts/pre_commit_cache.py`), so a moved
tag or a changed file fails the run instead of running unverified code.

Only the `code` and `fix` harnesses seed the cache. They are the agents
that commit and run `scripts/lint.py`. The `review`, `triage`, `retro`,
and `prioritize` harnesses do not seed it.

## Updating the pins

When Fullsend is upgraded:

1. Update the `base:` URL and its `#sha256=` pin in every
   `harness/*.yaml`. Verify with `scripts/check_harness_integrity.py
   --online`.
2. Update `AGENTS_REF` in `scripts/pre_commit_cache.py` to the same
   `vX.Y.Z`, and `UPSTREAM_SCRIPT_SHA256` to the SHA-256 of
   `scripts/pre-code.sh` and `scripts/pre-fix.sh` at that ref. A
   mismatch fails the run before the script executes.
   `scripts/test_pre_commit_cache.py` fails when the harness pins and
   `AGENTS_REF` differ.
3. Update `PRECOMMIT_VERSION` in `scripts/pre_commit_cache.py` to the
   `PRECOMMIT_VERSION` of fullsend's `images/sandbox/Containerfile`.
4. Update `SANDBOX_PYTHON_VERSION` in `scripts/pre_commit_cache.py` when
   the sandbox image's interpreter changes (Ubuntu Noble `/usr/bin/python3`
   3.12 today).

## Running the bootstrap by hand

```bash
python3 scripts/pre_commit_cache.py --upstream code --skip-upstream \
    --cache-home /sandbox/.cache/pre-commit \
    --tarball /tmp/workspace/pre-commit-cache.tar.gz
```

`--skip-upstream` omits the upstream pre-script. Use it for manual runs
outside Fullsend; inside a Fullsend run the upstream pre-script must run.
Building the cache needs network access and takes about five minutes.
Building it at `/sandbox/.cache/pre-commit` needs `sudo` on the runner;
the script does that itself.

## Failure mode

The warm phase is fail-soft: on any failure the wrapper prints a GitHub
Actions `::warning::` line and exits 0, so the agent run is never blocked.
`scripts/lint.py` then reports its usual missing-revision error, which
names the manual `pre-commit install-hooks` command.
