---
name: "rebase-pr"
description: >
  Provides a structured process for rebasing pull requests use when asked to
  "rebase a PR".
---

# Rebasing a Pull Request

When asked to "rebase a PR", follow this structured process:

- Check out the branch associated with the pull request into a local worktree
- Rebase the branch on the latest `upstream/main`
- Resolve any merge conflicts that arise during the rebase process
- Ensure all pre-commit checks pass before creating any commit for conflict
  resolution, and ensure all tests pass locally after the rebase
- Before pushing, verify that the expected remote branch state still points to
  the commit recorded before the rebase; stop if the remote branch has changed
- Push the rebased branch back to the remote repository with
  `git push --force-with-lease`, which will update the pull request; stop if the
  lease check fails and do not fall back to an unqualified force push
- Monitor CI checks and address any issues that arise after pushing the
  rebased branch
- Clean up the local worktree after the rebase process is complete
