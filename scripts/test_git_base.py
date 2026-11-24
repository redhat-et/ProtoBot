#!/usr/bin/env python3
"""Unit tests for scripts/git_base.py."""

from __future__ import annotations

import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

if __package__:
    from scripts import git_base
else:
    import git_base


class IsSafeGitRefTests(unittest.TestCase):
    def test_typical_refs_are_accepted(self) -> None:
        """Accept ordinary git ref names and commit SHAs."""
        for ref in (
            "main",
            "origin/main",
            "upstream/main",
            "refs/heads/main",
            "HEAD~1",
            "origin/main~2",
            "abc1234",
        ):
            with self.subTest(ref=ref):
                self.assertTrue(git_base.is_safe_git_ref(ref))

    def test_option_like_and_path_smuggling_refs_are_rejected(self) -> None:
        """Reject refs that git could parse as an option or a ref:path pair."""
        for ref in (
            "-h",
            "--upload-pack=touch /tmp/x",
            "-main",
            "main:.git/config",
            "",
        ):
            with self.subTest(ref=ref):
                self.assertFalse(git_base.is_safe_git_ref(ref))


@unittest.skipUnless(shutil.which("git"), "git required")
class GitPrimitiveTests(unittest.TestCase):
    """Exercise the low-level primitives against a real throwaway repo."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self._git("init", "-q", "-b", "trunk")
        self._git("config", "user.email", "test@example.com")
        self._git("config", "user.name", "Test")
        self._git("config", "commit.gpgsign", "false")
        (self.root / "a.txt").write_text("a")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "one")
        self.head = self._git("rev-parse", "HEAD").stdout.strip()

    def _git(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["git", *args],
            cwd=str(self.root),
            capture_output=True,
            text=True,
            check=True,
        )

    def test_git_commit_sha_resolves_head(self) -> None:
        """Resolve HEAD to its commit SHA."""
        self.assertEqual(self.head, git_base.git_commit_sha(self.root, "HEAD"))

    def test_git_commit_sha_rejects_unsafe_ref(self) -> None:
        """Refuse to resolve an option-like ref instead of passing it to git."""
        self.assertIsNone(git_base.git_commit_sha(self.root, "-h"))

    def test_git_commit_sha_returns_none_for_missing_ref(self) -> None:
        """Return None for a ref that does not exist."""
        self.assertIsNone(git_base.git_commit_sha(self.root, "no-such-branch"))

    def test_git_ref_exists_matches_commit_sha(self) -> None:
        """Report existence consistently with git_commit_sha."""
        self.assertTrue(git_base.git_ref_exists(self.root, "HEAD"))
        self.assertFalse(git_base.git_ref_exists(self.root, "no-such-branch"))
        self.assertFalse(git_base.git_ref_exists(self.root, "-h"))

    def test_get_merge_base_common_ancestor(self) -> None:
        """Compute the merge base of two diverged branches."""
        self._git("branch", "main")
        self._git("checkout", "-q", "-b", "feature")
        (self.root / "b.txt").write_text("b")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "two")
        self.assertEqual(self.head, git_base.get_merge_base(self.root, "main"))

    def test_get_merge_base_returns_none_for_unsafe_ref(self) -> None:
        """Refuse to compute a merge base against an option-like ref."""
        self.assertIsNone(git_base.get_merge_base(self.root, "--upload-pack=x"))

    def test_get_merge_base_returns_none_when_unresolvable(self) -> None:
        """Return None when the candidate ref does not exist."""
        self.assertIsNone(git_base.get_merge_base(self.root, "no-such-branch"))

    def test_is_shallow_repository_false_for_full_clone(self) -> None:
        """A normal local repository is not shallow."""
        self.assertFalse(git_base.is_shallow_repository(self.root))

    def test_is_shallow_repository_true_for_shallow_clone(self) -> None:
        """A --depth=1 clone is reported as shallow."""
        (self.root / "b.txt").write_text("b")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "two")
        with tempfile.TemporaryDirectory() as clone_dir:
            clone_path = Path(clone_dir) / "clone"
            # --no-local forces the transport that honors --depth; a
            # same-machine "local" clone ignores it and hardlinks history.
            subprocess.run(
                [
                    "git",
                    "clone",
                    "-q",
                    "--no-local",
                    "--depth=1",
                    str(self.root),
                    str(clone_path),
                ],
                capture_output=True,
                text=True,
                check=True,
            )
            self.assertTrue(git_base.is_shallow_repository(clone_path))


@unittest.skipUnless(shutil.which("git"), "git required")
class ResolveComparisonBaseTests(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self._git("init", "-q", "-b", "trunk")
        self._git("config", "user.email", "test@example.com")
        self._git("config", "user.name", "Test")
        self._git("config", "commit.gpgsign", "false")
        (self.root / "a.txt").write_text("a")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "one")
        self.head = self._git("rev-parse", "HEAD").stdout.strip()

    def _git(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["git", *args],
            cwd=str(self.root),
            capture_output=True,
            text=True,
            check=True,
        )

    def test_explicit_ref_is_returned_directly(self) -> None:
        """An explicit override is returned as given, not as a merge base."""
        self._git("branch", "feature-base")
        base, is_head = git_base.resolve_comparison_base(
            self.root, explicit_ref="feature-base", env={}
        )
        self.assertEqual("feature-base", base)
        self.assertTrue(is_head)

    def test_unresolvable_explicit_ref_fails_closed(self) -> None:
        """An explicit override that cannot be resolved fails closed."""
        base, is_head = git_base.resolve_comparison_base(
            self.root, explicit_ref="no-such-ref", env={}
        )
        self.assertIsNone(base)
        self.assertFalse(is_head)

    def test_unsafe_explicit_ref_fails_closed_without_running_git(self) -> None:
        """An option-like explicit override fails closed and is never executed."""
        with patch.object(git_base, "_run_git") as run_git:
            base, is_head = git_base.resolve_comparison_base(
                self.root, explicit_ref="-h", env={}
            )
        self.assertIsNone(base)
        self.assertFalse(is_head)
        run_git.assert_not_called()

    def test_pre_commit_from_ref_takes_priority_over_default_candidates(self) -> None:
        """PRE_COMMIT_FROM_REF wins over GITHUB_BASE_SHA/REF and defaults."""
        self._git("branch", "main")
        (self.root / "b.txt").write_text("b")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "two")
        env = {
            "PRE_COMMIT_FROM_REF": "main",
            "GITHUB_BASE_SHA": "0" * 40,
            "GITHUB_BASE_REF": "nonexistent",
        }
        base, is_head = git_base.resolve_comparison_base(self.root, env=env)
        self.assertEqual(self.head, base)
        self.assertFalse(is_head)

    def test_github_base_sha_is_honoured(self) -> None:
        """Use GITHUB_BASE_SHA when it resolves to a commit."""
        base, is_head = git_base.resolve_comparison_base(
            self.root, env={"GITHUB_BASE_SHA": self.head}
        )
        self.assertEqual(self.head, base)
        self.assertTrue(is_head)

    def test_github_base_ref_probes_origin_prefix_first(self) -> None:
        """Probe origin/<ref> before the bare ref name."""
        self._git("update-ref", "refs/remotes/origin/release", self.head)
        base, is_head = git_base.resolve_comparison_base(
            self.root, env={"GITHUB_BASE_REF": "release"}
        )
        self.assertEqual(self.head, base)
        self.assertTrue(is_head)

    def test_github_base_ref_injection_fails_closed(self) -> None:
        """An option-like GITHUB_BASE_REF is skipped, not passed to git."""
        with patch.object(git_base, "_run_git", wraps=git_base._run_git) as run_git:
            base, is_head = git_base.resolve_comparison_base(
                self.root, env={"GITHUB_BASE_REF": "--upload-pack=true"}
            )
        self.assertIsNone(base)
        self.assertFalse(is_head)
        for call in run_git.call_args_list:
            self.assertNotIn("--upload-pack=true", call.args)

    def test_event_base_selected_by_event_name(self) -> None:
        """Select the event base field that matches GITHUB_EVENT_NAME."""
        cases = {
            "pull_request": "PULL_REQUEST_BASE_SHA",
            "merge_group": "MERGE_GROUP_BASE_SHA",
            "push": "PUSH_BEFORE_SHA",
        }
        for event_name, env_var in cases.items():
            with self.subTest(event=event_name):
                env = {"GITHUB_EVENT_NAME": event_name, env_var: self.head}
                base, is_head = git_base.resolve_comparison_base(self.root, env=env)
                self.assertEqual(self.head, base)
                self.assertTrue(is_head)

    def test_event_base_ignored_for_unmapped_event(self) -> None:
        """An event name without a mapped base field falls through to defaults."""
        self._git("branch", "main")
        env = {
            "GITHUB_EVENT_NAME": "workflow_dispatch",
            "PUSH_BEFORE_SHA": self.head,
        }
        base, is_head = git_base.resolve_comparison_base(self.root, env=env)
        self.assertEqual(self.head, base)
        self.assertTrue(is_head)

    def test_default_candidates_tried_in_order(self) -> None:
        """Prefer upstream/main over origin/main and main."""
        self._git("branch", "main")
        (self.root / "b.txt").write_text("b")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "two")
        new_head = self._git("rev-parse", "HEAD").stdout.strip()
        self._git("update-ref", "refs/remotes/upstream/main", new_head)
        base, is_head = git_base.resolve_comparison_base(self.root, env={})
        self.assertEqual(new_head, base)
        self.assertTrue(is_head)

    def test_diverged_branch_uses_merge_base_not_tip(self) -> None:
        """Reduce an automatically discovered candidate to its merge base."""
        self._git("branch", "main")
        self._git("checkout", "-q", "-b", "feature")
        (self.root / "b.txt").write_text("b")
        self._git("add", "-A")
        self._git("commit", "-q", "-m", "two")
        base, is_head = git_base.resolve_comparison_base(self.root, env={})
        self.assertEqual(self.head, base)
        self.assertFalse(is_head)

    def test_deterministic_fail_closed_when_nothing_resolves(self) -> None:
        """Return (None, False) when no candidate resolves, never HEAD."""
        base, is_head = git_base.resolve_comparison_base(self.root, env={})
        self.assertIsNone(base)
        self.assertFalse(is_head)

    def test_fail_closed_does_not_run_git_with_unsafe_candidates(self) -> None:
        """Every unsafe candidate in a mixed set is skipped before touching git."""
        env = {
            "GITHUB_BASE_SHA": "-h",
            "GITHUB_BASE_REF": "--upload-pack=true",
        }
        base, is_head = git_base.resolve_comparison_base(self.root, env=env)
        self.assertIsNone(base)
        self.assertFalse(is_head)


if __name__ == "__main__":
    unittest.main()
