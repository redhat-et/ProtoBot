from __future__ import annotations

import contextlib
import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

if __package__:
    from scripts import check_adr_amendments as checker
else:
    import check_adr_amendments as checker

_ACCEPTED_ADR = """# ADR-0001: Example

> Status: **Accepted** — September 2026

## Decision

Use one file per record.

## Consequences

Keep diffs small.
"""

_PROPOSED_ADR = """# ADR-0009: Draft

> Status: **Proposed** — November 2026

## Decision

A draft decision that may still change.
"""


def _git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run a git command in *repo* and fail the test on a non-zero exit."""
    env = os.environ.copy()
    env.setdefault("GIT_AUTHOR_NAME", "ADR Test")
    env.setdefault("GIT_AUTHOR_EMAIL", "adr-test@example.com")
    env.setdefault("GIT_COMMITTER_NAME", "ADR Test")
    env.setdefault("GIT_COMMITTER_EMAIL", "adr-test@example.com")
    result = subprocess.run(
        ["git", *args],
        cwd=str(repo),
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise AssertionError(
            f"git {' '.join(args)} failed ({result.returncode}): "
            f"{result.stderr.strip()}"
        )
    return result


class HistoricalMutationTests(unittest.TestCase):
    def test_insertions_are_allowed(self) -> None:
        """Appending or inserting lines is not a historical mutation."""
        old = ["alpha", "bravo", "charlie"]
        new = [
            "alpha",
            "**[Amended October 2026]** Clarified bravo.",
            "bravo",
            "charlie",
            "**[Amended November 2026]** Added a trailing note.",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_replaced_line_is_reported(self) -> None:
        """Rewriting a historical line is a mutation at that HEAD line."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "bravo changed", "charlie"]
        self.assertEqual([(2, "bravo")], checker.historical_mutations(old, new))

    def test_deleted_line_is_reported(self) -> None:
        """Removing a historical line is a mutation at that HEAD line."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "charlie"]
        self.assertEqual([(2, "bravo")], checker.historical_mutations(old, new))

    def test_identical_files_have_no_mutations(self) -> None:
        """Unchanged accepted text is not a mutation."""
        lines = ["alpha", "bravo"]
        self.assertEqual([], checker.historical_mutations(lines, list(lines)))


class AcceptedStatusTests(unittest.TestCase):
    def test_accepted_header_is_detected(self) -> None:
        """Detect the Accepted status line used by existing ADRs."""
        self.assertTrue(checker.is_accepted(_ACCEPTED_ADR))

    def test_proposed_header_is_not_accepted(self) -> None:
        """Proposed ADRs are not locked by the immutability check."""
        self.assertFalse(checker.is_accepted(_PROPOSED_ADR))

    def test_status_outside_header_is_ignored(self) -> None:
        """A quoted Accepted status below the header does not lock the file."""
        body = "\n".join(
            [
                "# ADR-0010: Draft",
                "",
                "> Status: **Proposed** — November 2026",
            ]
            + [""] * 40
            + ["> Status: **Accepted** — quoted example"]
        )
        self.assertFalse(checker.is_accepted(body))


class PathFilterTests(unittest.TestCase):
    def test_decisions_markdown_is_included(self) -> None:
        """Markdown files under docs/decisions/ are in scope."""
        self.assertTrue(
            checker.is_adr_markdown(Path("docs") / "decisions" / "0001-example.md")
        )

    def test_non_decision_markdown_is_excluded(self) -> None:
        """Specification documents outside docs/decisions/ are ignored."""
        self.assertFalse(checker.is_adr_markdown(Path("docs") / "architecture.md"))


class GitRepoCheckTests(unittest.TestCase):
    def setUp(self) -> None:
        """Create a temporary git repository with one accepted ADR."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        _git(self.root, "init", "-b", "main")
        _git(self.root, "config", "user.name", "ADR Test")
        _git(self.root, "config", "user.email", "adr-test@example.com")
        _git(self.root, "config", "commit.gpgsign", "false")
        self.adr_rel = Path("docs") / "decisions" / "0001-example.md"
        adr_path = self.root / self.adr_rel
        adr_path.parent.mkdir(parents=True)
        adr_path.write_text(_ACCEPTED_ADR, encoding="utf-8")
        proposed = self.root / "docs" / "decisions" / "0009-draft.md"
        proposed.write_text(_PROPOSED_ADR, encoding="utf-8")
        _git(self.root, "add", "docs/decisions")
        _git(self.root, "commit", "--no-gpg-sign", "-m", "add ADRs")

    def _check(
        self,
        *rel_paths: str,
        baseline_ref: str | None = None,
    ) -> tuple[int, str]:
        """Run the checker in the temporary repository and capture stdout."""
        stdout = io.StringIO()
        paths = list(rel_paths) if rel_paths else None
        with contextlib.redirect_stdout(stdout):
            rc = checker.check(
                paths,
                repo_root=self.root,
                baseline_ref=baseline_ref,
            )
        return rc, stdout.getvalue()

    def test_unmodified_accepted_adr_passes(self) -> None:
        """Existing accepted ADRs match HEAD and pass."""
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_appended_amendment_passes(self) -> None:
        """Appending a dated amendment block without editing HEAD lines passes."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR + "\n**[Amended November 2026]** Clarified consequences.\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_rewritten_decision_text_fails(self) -> None:
        """In-place rewriting of accepted decision prose fails."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_proposed_adr_may_be_rewritten(self) -> None:
        """Proposed ADRs are still mutable, including becoming Accepted."""
        rel = "docs/decisions/0009-draft.md"
        path = self.root / rel
        path.write_text(
            _PROPOSED_ADR.replace(
                "A draft decision that may still change.",
                "Final.",
            ).replace("**Proposed**", "**Accepted**"),
            encoding="utf-8",
        )
        rc, output = self._check(rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_new_adr_is_skipped(self) -> None:
        """A path not in HEAD has no historical accepted text to preserve."""
        rel = "docs/decisions/0010-new.md"
        path = self.root / rel
        path.write_text(_ACCEPTED_ADR, encoding="utf-8")
        rc, output = self._check(rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_deleted_accepted_adr_fails(self) -> None:
        """Deleting an accepted ADR deletes its historical lines."""
        path = self.root / self.adr_rel
        path.unlink()
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_committed_mutation_on_feature_branch_fails_against_baseline(self) -> None:
        """A mutation committed to a feature branch fails on clean tree against baseline."""
        _git(self.root, "checkout", "-b", "feature")
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "rewrite ADR in place")
        # Clean working tree: working tree == HEAD
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_pre_commit_from_ref_environment_variable(self) -> None:
        """PRE_COMMIT_FROM_REF overrides default branch discovery."""
        _git(self.root, "checkout", "-b", "feature")
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "rewrite ADR in place")
        os.environ["PRE_COMMIT_FROM_REF"] = "main"
        try:
            rc, output = self._check()
            self.assertEqual(1, rc)
            self.assertIn("rewritten or replaced in place", output)
        finally:
            os.environ.pop("PRE_COMMIT_FROM_REF", None)

    def test_github_base_ref_environment_variable(self) -> None:
        """GITHUB_BASE_REF resolves the baseline branch when main is renamed or custom."""
        _git(self.root, "checkout", "-b", "feature")
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "rewrite ADR in place")
        _git(self.root, "branch", "-m", "main", "custom-base")
        os.environ["GITHUB_BASE_REF"] = "custom-base"
        try:
            rc, output = self._check()
            self.assertEqual(1, rc)
            self.assertIn("rewritten or replaced in place", output)
        finally:
            os.environ.pop("GITHUB_BASE_REF", None)

    def test_github_base_sha_environment_variable(self) -> None:
        """GITHUB_BASE_SHA resolves the baseline commit SHA."""
        base_sha = _git(self.root, "rev-parse", "main").stdout.strip()
        _git(self.root, "checkout", "-b", "feature")
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "rewrite ADR in place")
        _git(self.root, "branch", "-D", "main")
        os.environ["GITHUB_BASE_SHA"] = base_sha
        try:
            rc, output = self._check()
            self.assertEqual(1, rc)
            self.assertIn("rewritten or replaced in place", output)
        finally:
            os.environ.pop("GITHUB_BASE_SHA", None)

    def test_missing_baseline_ref_fails_closed(self) -> None:
        """When no default-branch baseline can be resolved, check fails closed."""
        _git(self.root, "checkout", "-b", "feature")
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "rewrite ADR in place")
        _git(self.root, "branch", "-D", "main")
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc, _output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("cannot resolve default-branch baseline", stderr.getvalue())

    def test_invalid_explicit_baseline_ref_fails_closed(self) -> None:
        """An invalid explicit baseline ref fails closed."""
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc, _output = self._check(baseline_ref="nonexistent-ref")
        self.assertEqual(1, rc)
        self.assertIn("cannot resolve default-branch baseline", stderr.getvalue())

    def test_git_show_blob_missing_path_returns_none(self) -> None:
        """A path not in the tree returns (None, None)."""
        text, err = checker._git_show_blob(
            self.root,
            "HEAD:docs/decisions/nonexistent.md",
        )
        self.assertIsNone(text)
        self.assertIsNone(err)

    def test_git_show_blob_invalid_revision_returns_error(self) -> None:
        """An invalid revision returns an error rather than swallowing it."""
        text, err = checker._git_show_blob(
            self.root,
            "invalid-revision:docs/decisions/0001-example.md",
        )
        self.assertIsNone(text)
        self.assertIsNotNone(err)

    def test_committed_amendment_on_feature_branch_passes(self) -> None:
        """Appending and committing an amendment on a feature branch passes."""
        _git(self.root, "checkout", "-b", "feature")
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR + "\n**[Amended November 2026]** Clarified consequences.\n",
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "amend ADR")
        rc, output = self._check()
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_git_rm_of_accepted_adr_fails(self) -> None:
        """Removing an accepted ADR with git rm fails when enumerated from baseline."""
        _git(self.root, "rm", self.adr_rel.as_posix())
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_git_mv_and_rewrite_of_accepted_adr_fails(self) -> None:
        """Renaming and rewriting an accepted ADR fails."""
        renamed_rel = "docs/decisions/0001-renamed.md"
        _git(self.root, "mv", self.adr_rel.as_posix(), renamed_rel)
        renamed_path = self.root / renamed_rel
        renamed_path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted (renamed to docs/decisions/0001-renamed.md)", output)
        self.assertIn("rewritten or replaced in place", output)

    def test_git_mv_without_rewrite_of_accepted_adr_fails(self) -> None:
        """Renaming an accepted ADR without rewriting still fails because it was moved."""
        renamed_rel = "docs/decisions/0001-renamed.md"
        _git(self.root, "mv", self.adr_rel.as_posix(), renamed_rel)
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted (renamed to docs/decisions/0001-renamed.md)", output)

    def test_uncommitted_mutation_on_branch_new_accepted_adr_fails(self) -> None:
        """A new accepted ADR on a branch falls back to HEAD for local uncommitted checks."""
        _git(self.root, "checkout", "-b", "feature")
        new_rel = "docs/decisions/0015-branch-accepted.md"
        new_path = self.root / new_rel
        new_path.write_text(_ACCEPTED_ADR, encoding="utf-8")
        _git(self.root, "add", new_rel)
        _git(self.root, "commit", "--no-gpg-sign", "-m", "add branch ADR")
        new_path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)

    def test_non_decision_path_is_ignored(self) -> None:
        """Files outside docs/decisions/ are not checked."""
        other = self.root / "README.md"
        other.write_text("not an ADR\n", encoding="utf-8")
        rc, output = self._check("README.md")
        self.assertEqual(0, rc)
        self.assertEqual("", output)


class LiveRepoTests(unittest.TestCase):
    def test_current_accepted_adrs_pass(self) -> None:
        """Every committed accepted ADR in this repository currently passes."""
        decisions = sorted((checker.REPO_ROOT / "docs" / "decisions").glob("*.md"))
        self.assertTrue(decisions)
        rels = [str(path.relative_to(checker.REPO_ROOT)) for path in decisions]
        self.assertEqual(0, checker.check(rels))


if __name__ == "__main__":
    unittest.main()
