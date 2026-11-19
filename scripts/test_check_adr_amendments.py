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

_SUPERSEDED_ADR = """# ADR-0001: Example

> Status: **Superseded by ADR-0004** — November 2026

## Decision

Use one file per record.

## Consequences

Keep diffs small.

**[Amended November 2026]** Superseded by ADR-0004.
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
        """Rewriting a historical line is a mutation at its baseline/HEAD lines."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "bravo changed", "charlie"]
        self.assertEqual([(2, 2, "bravo")], checker.historical_mutations(old, new))

    def test_deleted_line_is_reported(self) -> None:
        """Removing a historical line is a mutation with no HEAD line."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "charlie"]
        self.assertEqual([(2, None, "bravo")], checker.historical_mutations(old, new))

    def test_identical_files_have_no_mutations(self) -> None:
        """Unchanged accepted text is not a mutation."""
        lines = ["alpha", "bravo"]
        self.assertEqual([], checker.historical_mutations(lines, list(lines)))

    def test_mutation_reports_accurate_baseline_and_head_lines(self) -> None:
        """Baseline and HEAD line numbers diverge once insertions precede a mutation."""
        old = [f"line {n}" for n in range(1, 11)]
        new = (
            old[:4]
            + [
                "**[Amended November 2026]** Note one.",
                "**[Amended November 2026]** Note two.",
                "**[Amended November 2026]** Note three.",
            ]
            + old[4:9]
            + ["line 10 changed"]
        )
        self.assertEqual(
            [(10, 13, "line 10")],
            checker.historical_mutations(old, new),
        )

    def test_authorized_status_supersession_is_allowed(self) -> None:
        """An authorized status-line supersession transition is not a mutation."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_authorized_status_supersession_without_date_is_allowed(self) -> None:
        """An authorized status-line supersession without date suffix is allowed."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted**",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004**",
            "",
            "Body.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_status_flip_with_trailing_text_fails(self) -> None:
        """Trailing text after the status line is rejected as a mutation."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            (
                "> Status: **Superseded by ADR-0004** — November 2026 and"
                " arbitrary prose"
            ),
            "",
            "Body.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Accepted** — September 2026")],
            mutations,
        )

    def test_status_flip_from_annotated_accepted_status_fails(self) -> None:
        """An accepted status line with trailing annotation cannot be superseded."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** (supersedes ADR-0000)",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Accepted** (supersedes ADR-0000)")],
            mutations,
        )

    def test_status_supersession_outside_header_is_not_authorized(self) -> None:
        """A status-line-shaped replace outside the header window still fails."""
        old = [""] * 35 + ["> Status: **Accepted** \u2014 September 2026"]
        new = [""] * 35 + ["> Status: **Superseded by ADR-0004** \u2014 November 2026"]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(1, len(mutations))

    def test_superseded_status_combined_with_other_edit_is_still_a_mutation(
        self,
    ) -> None:
        """The status-line exception does not license other accepted-text edits."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Use one file per record.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Use JSONL storage instead.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual([(5, 5, "Use one file per record.")], mutations)

    def test_authorized_status_supersession_with_trailing_whitespace_is_allowed(
        self,
    ) -> None:
        """An authorized status supersession with trailing whitespace or CRLF is allowed."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted**   \r",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026  \r",
            "",
            "Body.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_superseded_to_superseded_transition_is_not_authorized(self) -> None:
        """Re-pointing an already-superseded ADR status line is not authorized."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0002** \u2014 November 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0003** \u2014 December 2026",
            "",
            "Body.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Superseded by ADR-0002** \u2014 November 2026")],
            mutations,
        )

    def test_status_flip_with_no_amendment_block_fails(self) -> None:
        """A status flip with no amendment block must fail."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Accepted** \u2014 September 2026")],
            mutations,
        )

    def test_status_flip_with_appended_amendment_block_passes(self) -> None:
        """A status flip with an appended amendment block must pass."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** — November 2026",
            "",
            "Body.",
            "",
            (
                "**[Amended November 2026]** Superseded by ADR-0004: rationale"
                " for transition; see docs/decisions/0004-adopt-protobot-v2.md."
            ),
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_status_flip_with_undated_amendment_block_fails(self) -> None:
        """An undated amendment block marker fails."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
            "",
            "**[Amended ]**",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Accepted** \u2014 September 2026")],
            mutations,
        )

    def test_status_flip_with_amendment_block_in_code_fence_fails(self) -> None:
        """An amendment marker inside a fenced code block is ignored and fails."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
            "",
            "```markdown",
            "**[Amended November 2026]** Superseded by ADR-0004.",
            "```",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Accepted** \u2014 September 2026")],
            mutations,
        )

    def test_status_flip_with_amendment_block_before_title_fails(self) -> None:
        """An amendment marker placed before the title is rejected."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "**[Amended November 2026]** Superseded by ADR-0004.",
            "",
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 5, "> Status: **Accepted** \u2014 September 2026")],
            mutations,
        )

    def test_status_flip_with_amendment_block_before_last_baseline_line_fails(
        self,
    ) -> None:
        """An amendment block inserted before the last baseline line fails."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "**[Amended November 2026]** Superseded by ADR-0004.",
            "",
            "Body.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, "> Status: **Accepted** \u2014 September 2026")],
            mutations,
        )

    def test_status_flip_with_second_status_line_fails(self) -> None:
        """Inserting a second status line in the header is rejected."""
        old = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "",
            "Body.",
        ]
        new = [
            "# ADR-0001: Example",
            "",
            "> Status: **Accepted** \u2014 September 2026",
            "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            "",
            "Body.",
            "",
            "**[Amended November 2026]** Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertTrue(len(mutations) > 0)


class AcceptedStatusTests(unittest.TestCase):
    def test_accepted_header_is_detected(self) -> None:
        """Detect the Accepted status line used by existing ADRs."""
        self.assertTrue(checker.is_accepted(_ACCEPTED_ADR))

    def test_proposed_header_is_not_accepted(self) -> None:
        """Proposed ADRs are not locked by the immutability check."""
        self.assertFalse(checker.is_accepted(_PROPOSED_ADR))

    def test_superseded_header_is_detected(self) -> None:
        """Detect the Superseded status line used by superseded ADRs."""
        self.assertTrue(checker.is_superseded(_SUPERSEDED_ADR))
        self.assertFalse(checker.is_accepted(_SUPERSEDED_ADR))

    def test_accepted_header_is_not_superseded(self) -> None:
        """Accepted ADRs are not detected as superseded."""
        self.assertFalse(checker.is_superseded(_ACCEPTED_ADR))

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

    def test_accepted_header_with_trailing_whitespace_is_detected(self) -> None:
        """Trailing whitespace on the status line does not prevent detection."""
        adr = "# ADR-0001: Example\n\n> Status: **Accepted**   \n\n## Decision\n"
        self.assertTrue(checker.is_accepted(adr))

    def test_accepted_header_with_crlf_is_detected(self) -> None:
        """CRLF line endings on the status line do not prevent detection."""
        adr = "# ADR-0001: Example\r\n\r\n> Status: **Accepted**\r\n\r\n## Decision\r\n"
        self.assertTrue(checker.is_accepted(adr))

    def test_accepted_header_with_additional_annotation_is_detected(self) -> None:
        """Additional annotations on the status line do not prevent detection."""
        adr = (
            "# ADR-0001: Example\n\n"
            "> Status: **Accepted** (supersedes ADR-0000)\n\n"
            "## Decision\n"
        )
        self.assertTrue(checker.is_accepted(adr))

    def test_superseded_header_with_trailing_whitespace_is_detected(self) -> None:
        """Trailing whitespace on superseded status line does not prevent detection."""
        adr = (
            "# ADR-0001: Example\n\n"
            "> Status: **Superseded by ADR-0004**   \n\n"
            "## Decision\n"
        )
        self.assertTrue(checker.is_superseded(adr))
        self.assertFalse(checker.is_accepted(adr))

    def test_superseded_header_with_crlf_is_detected(self) -> None:
        """CRLF line endings on superseded status line do not prevent detection."""
        adr = (
            "# ADR-0001: Example\r\n\r\n"
            "> Status: **Superseded by ADR-0004**\r\n\r\n"
            "## Decision\r\n"
        )
        self.assertTrue(checker.is_superseded(adr))

    def test_superseded_header_with_additional_annotation_is_detected(self) -> None:
        """Additional annotations on superseded status line do not prevent detection."""
        adr = (
            "# ADR-0001: Example\n\n"
            "> Status: **Superseded by ADR-0004** (see ADR-0004)\n\n"
            "## Decision\n"
        )
        self.assertTrue(checker.is_superseded(adr))


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
        saved_env = {}
        for var in ("PRE_COMMIT_FROM_REF", "GITHUB_BASE_SHA", "GITHUB_BASE_REF"):
            if var in os.environ:
                saved_env[var] = os.environ.pop(var)
        if saved_env:
            self.addCleanup(os.environ.update, saved_env)
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

    def test_superseded_status_transition_passes(self) -> None:
        """Transitioning an accepted ADR's status line to Superseded passes."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            )
            + (
                "\n**[Amended November 2026]** Superseded by ADR-0004; see "
                "docs/decisions/0004-adopt-protobot-v2.md.\n"
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_superseded_status_combined_with_rewrite_fails(self) -> None:
        """The status-line exception does not license other accepted-text edits."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            ).replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_superseded_status_transition_with_trailing_text_fails(self) -> None:
        """A superseded status line with trailing text fails check()."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** — September 2026",
                "> Status: **Superseded by ADR-0004** (see issue #406)",
            )
            + (
                "\n**[Amended November 2026]** Superseded by ADR-0004; see "
                "docs/decisions/0004-adopt-protobot-v2.md.\n"
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "accepted decision text was rewritten or replaced in place",
            output,
        )

    def test_superseded_status_transition_with_adjacent_amendment_before_body_fails(
        self,
    ) -> None:
        """An amendment block placed directly after the status line fails check()."""
        amendment = (
            "\n**[Amended November 2026]** Superseded by ADR-0004; see "
            "docs/decisions/0004-adopt-protobot-v2.md."
        )
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** — September 2026",
                "> Status: **Superseded by ADR-0004** — November 2026"
                + amendment,
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "supersession requires an appended dated amendment block",
            output,
        )

    def test_status_only_supersession_without_amendment_reports_distinct_diagnostic(
        self,
    ) -> None:
        """A status-only supersession without amendment reports a distinct diagnostic."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** — September 2026",
                "> Status: **Superseded by ADR-0004** — November 2026",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "supersession requires an appended dated amendment block",
            output,
        )
        self.assertNotIn(
            "accepted decision text was rewritten or replaced in place",
            output,
        )

    def test_mutation_diagnostic_rendering(self) -> None:
        """Assert rendered baseline line, HEAD line, and truncation strings."""
        path = self.root / self.adr_rel
        lines = (
            [
                "# ADR-0001: Example",
                "",
                "> Status: **Accepted** — September 2026",
                "",
                "Replaced.",
                "Del1.",
                "Anchor.",
            ]
            + [f"Line {i}." for i in range(15)]
        )
        base_text = "\n".join(lines) + "\n"
        path.write_text(base_text, encoding="utf-8")
        _git(
            self.root,
            "commit",
            "-a",
            "--no-gpg-sign",
            "-m",
            "update base adr",
        )

        mutated_lines = (
            [
                "# ADR-0001: Example",
                "",
                "> Status: **Accepted** — September 2026",
                "",
                "Rewritten.",
                "Anchor.",
            ]
            + [f"New {i}." for i in range(15)]
        )

        path.write_text("\n".join(mutated_lines) + "\n", encoding="utf-8")
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "accepted decision text was rewritten or replaced in place",
            output,
        )
        self.assertIn("baseline line 5 (HEAD line 5)", output)
        self.assertIn("baseline line 6 (deleted)", output)
        self.assertRegex(output, r"… \(\d+ more line\(s\)\)")

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

    def test_superseded_adr_on_baseline_cannot_be_deleted(self) -> None:
        """A superseded ADR on baseline cannot be deleted."""
        path = self.root / self.adr_rel
        path.write_text(_SUPERSEDED_ADR, encoding="utf-8")
        _git(self.root, "commit", "-a", "--no-gpg-sign", "-m", "supersede ADR-0001")
        path.unlink()
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_superseded_adr_on_baseline_cannot_be_renamed(self) -> None:
        """A superseded ADR on baseline cannot be renamed."""
        path = self.root / self.adr_rel
        path.write_text(_SUPERSEDED_ADR, encoding="utf-8")
        _git(self.root, "commit", "-a", "--no-gpg-sign", "-m", "supersede ADR-0001")
        new_rel = Path("docs") / "decisions" / "0001-renamed.md"
        _git(self.root, "mv", str(self.adr_rel), str(new_rel))
        rc, output = self._check(self.adr_rel.as_posix(), new_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(f"was deleted (renamed to {new_rel.as_posix()})", output)

    def test_superseded_adr_on_baseline_cannot_be_rewritten(self) -> None:
        """In-place rewriting of superseded ADR decision prose fails."""
        path = self.root / self.adr_rel
        path.write_text(_SUPERSEDED_ADR, encoding="utf-8")
        _git(self.root, "commit", "-a", "--no-gpg-sign", "-m", "supersede ADR-0001")
        path.write_text(
            _SUPERSEDED_ADR.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_superseded_adr_on_baseline_amendment_passes(self) -> None:
        """Appending a dated amendment block to a superseded ADR passes."""
        path = self.root / self.adr_rel
        path.write_text(_SUPERSEDED_ADR, encoding="utf-8")
        _git(self.root, "commit", "-a", "--no-gpg-sign", "-m", "supersede ADR-0001")
        path.write_text(
            _SUPERSEDED_ADR + "\n**[Amended December 2026]** Follow-up amendment.\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_accepted_adr_with_format_drift_on_baseline_is_protected(self) -> None:
        """An accepted ADR with format drift on baseline is not skipped by check()."""
        path = self.root / self.adr_rel
        drifted_adr = _ACCEPTED_ADR.replace(
            "> Status: **Accepted** \u2014 September 2026",
            "> Status: **Accepted** (amended)   ",
        )
        path.write_text(drifted_adr, encoding="utf-8")
        _git(
            self.root,
            "commit",
            "-a",
            "--no-gpg-sign",
            "-m",
            "add drifted accepted ADR",
        )
        path.write_text(
            drifted_adr.replace(
                "Use one file per record.",
                "Use JSONL storage instead.",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_superseded_to_superseded_transition_fails_check(self) -> None:
        """Attempting to re-point an already superseded ADR fails check()."""
        path = self.root / self.adr_rel
        path.write_text(_SUPERSEDED_ADR, encoding="utf-8")
        _git(self.root, "commit", "-a", "--no-gpg-sign", "-m", "supersede ADR-0001")
        path.write_text(
            _SUPERSEDED_ADR.replace(
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
                "> Status: **Superseded by ADR-0005** \u2014 December 2026",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn(
            "> Status: **Superseded by ADR-0004** \u2014 November 2026", output
        )

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

    def test_github_base_sha_unreachable_fails_closed(self) -> None:
        """When GITHUB_BASE_SHA points to an unreachable SHA, check fails closed."""
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
        os.environ["GITHUB_BASE_SHA"] = "0" * 40
        try:
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
            self.assertEqual(1, rc)
            self.assertIn("cannot resolve default-branch baseline", stderr.getvalue())
        finally:
            os.environ.pop("GITHUB_BASE_SHA", None)

    def test_github_base_ref_fetches_from_origin_when_missing_locally(self) -> None:
        """GITHUB_BASE_REF fetches the baseline branch from origin when missing locally."""
        with tempfile.TemporaryDirectory() as tmp_remote_dir:
            remote_path = Path(tmp_remote_dir) / "remote"
            _git(Path(tmp_remote_dir), "init", "--bare", "-b", "main", str(remote_path))
            _git(self.root, "remote", "add", "origin", str(remote_path))
            _git(self.root, "push", "origin", "main")
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
            _git(self.root, "update-ref", "-d", "refs/remotes/origin/main")
            verify_missing = subprocess.run(
                ["git", "rev-parse", "--verify", "origin/main"],
                cwd=str(self.root),
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(0, verify_missing.returncode)
            os.environ["GITHUB_BASE_REF"] = "main"
            try:
                rc, output = self._check()
                self.assertEqual(1, rc)
                self.assertIn("rewritten or replaced in place", output)
            finally:
                os.environ.pop("GITHUB_BASE_REF", None)
                _git(self.root, "remote", "remove", "origin")

    def test_github_base_ref_stale_fetch_head_on_fetch_failure_fails_closed(
        self,
    ) -> None:
        """A stale FETCH_HEAD at HEAD is not consulted when fetching GITHUB_BASE_REF fails."""
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
        _git(self.root, "update-ref", "-d", "refs/remotes/origin/main")
        broken_remote = str(self.root / "nonexistent-remote")
        _git(self.root, "remote", "add", "origin", broken_remote)

        head_sha = _git(self.root, "rev-parse", "HEAD").stdout.strip()
        (self.root / ".git" / "FETCH_HEAD").write_text(
            f"{head_sha}\t\tbranch 'feature' of {broken_remote}\n",
            encoding="utf-8",
        )
        self.assertEqual(
            head_sha,
            _git(self.root, "rev-parse", "FETCH_HEAD").stdout.strip(),
        )

        os.environ["GITHUB_BASE_REF"] = "missing-branch"
        try:
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
            self.assertEqual(1, rc)
            self.assertIn("cannot resolve default-branch baseline", stderr.getvalue())
        finally:
            os.environ.pop("GITHUB_BASE_REF", None)
            _git(self.root, "remote", "remove", "origin")

    def test_default_branch_stale_fetch_head_on_fetch_failure_fails_closed(
        self,
    ) -> None:
        """A stale FETCH_HEAD at HEAD is not consulted when default branch fetch fails."""
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
        _git(self.root, "update-ref", "-d", "refs/remotes/origin/main")
        broken_remote = str(self.root / "nonexistent-remote")
        _git(self.root, "remote", "add", "origin", broken_remote)

        head_sha = _git(self.root, "rev-parse", "HEAD").stdout.strip()
        (self.root / ".git" / "FETCH_HEAD").write_text(
            f"{head_sha}\t\tbranch 'feature' of {broken_remote}\n",
            encoding="utf-8",
        )
        self.assertEqual(
            head_sha,
            _git(self.root, "rev-parse", "FETCH_HEAD").stdout.strip(),
        )

        try:
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
            self.assertEqual(1, rc)
            self.assertIn("cannot resolve default-branch baseline", stderr.getvalue())
        finally:
            _git(self.root, "remote", "remove", "origin")

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

    def test_github_base_ref_option_injection_fails_closed_and_does_not_execute(
        self,
    ) -> None:
        """Environment-derived refs starting with options fail closed and are not executed."""
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
        marker = self.root / "injection_marker"
        os.environ["GITHUB_BASE_REF"] = f"--upload-pack=touch {marker}"
        try:
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
            self.assertEqual(1, rc)
            self.assertFalse(marker.exists())
        finally:
            os.environ.pop("GITHUB_BASE_REF", None)

    def test_symlinked_accepted_adr_fails(self) -> None:
        """Replacing an accepted ADR with a symlink fails the check."""
        target = self.root / "docs" / "notes.md"
        target.write_text("Not the accepted ADR text.\n", encoding="utf-8")
        path = self.root / self.adr_rel
        path.unlink()
        path.symlink_to("../notes.md")
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc, _output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("not a regular file", stderr.getvalue())

    def test_shallow_clone_deepens_to_find_merge_base(self) -> None:
        """A shallow depth-1 clone deepens fetch from origin to resolve merge-base."""
        with tempfile.TemporaryDirectory() as tmp_remote_dir:
            remote_path = Path(tmp_remote_dir) / "remote"
            _git(Path(tmp_remote_dir), "init", "--bare", "-b", "main", str(remote_path))
            _git(self.root, "remote", "add", "origin", str(remote_path))
            _git(self.root, "push", "origin", "main")
            _git(self.root, "checkout", "-b", "feature")
            feature_path = self.root / self.adr_rel
            feature_path.write_text(
                _ACCEPTED_ADR + "\n**[Amended October 2026]** Feature amendment.\n",
                encoding="utf-8",
            )
            _git(self.root, "commit", "--no-gpg-sign", "-am", "amend on feature")
            _git(self.root, "push", "origin", "feature")

            # Push two newer amendments to main on remote (>=2 commits ahead of branch point)
            _git(self.root, "checkout", "main")
            main_path = self.root / self.adr_rel
            main_path.write_text(
                _ACCEPTED_ADR + "\n**[Amended November 2026]** Main amendment 1.\n",
                encoding="utf-8",
            )
            _git(self.root, "commit", "--no-gpg-sign", "-am", "amend on main 1")
            main_path.write_text(
                _ACCEPTED_ADR
                + "\n**[Amended November 2026]** Main amendment 1.\n\n"
                + "**[Amended December 2026]** Main amendment 2.\n",
                encoding="utf-8",
            )
            _git(self.root, "commit", "--no-gpg-sign", "-am", "amend on main 2")
            _git(self.root, "push", "origin", "main")

            # Create a depth-1 clone of feature
            with tempfile.TemporaryDirectory() as tmp_clone_dir:
                clone_path = Path(tmp_clone_dir) / "clone"
                subprocess.run(
                    [
                        "git",
                        "clone",
                        "--depth=1",
                        "--branch=feature",
                        str(remote_path),
                        str(clone_path),
                    ],
                    capture_output=True,
                    check=True,
                )
                stdout = io.StringIO()
                with contextlib.redirect_stdout(stdout):
                    rc = checker.check(
                        None,
                        repo_root=clone_path,
                    )
                self.assertEqual(0, rc)
                self.assertEqual("", stdout.getvalue())
            _git(self.root, "remote", "remove", "origin")

    def test_shallow_clone_unresolvable_merge_base_bounds_and_fails_closed(
        self,
    ) -> None:
        """Deepen loop bounds and fails closed when merge-base cannot resolve."""
        with tempfile.TemporaryDirectory() as tmp_remote_dir:
            remote_path = Path(tmp_remote_dir) / "remote"
            _git(Path(tmp_remote_dir), "init", "--bare", "-b", "main", str(remote_path))
            _git(self.root, "remote", "add", "origin", str(remote_path))
            _git(self.root, "push", "origin", "main")

            # Create orphan branch on remote with completely disjoint history
            _git(self.root, "checkout", "--orphan", "disjoint")
            _git(self.root, "rm", "-rf", ".")
            (self.root / "README.md").write_text("Disjoint branch\n", encoding="utf-8")
            _git(self.root, "add", "README.md")
            _git(self.root, "commit", "--no-gpg-sign", "-m", "disjoint root")
            _git(self.root, "push", "origin", "disjoint")

            with tempfile.TemporaryDirectory() as tmp_clone_dir:
                clone_path = Path(tmp_clone_dir) / "clone"
                subprocess.run(
                    [
                        "git",
                        "clone",
                        "--depth=1",
                        "--branch=disjoint",
                        str(remote_path),
                        str(clone_path),
                    ],
                    capture_output=True,
                    check=True,
                )
                # Fetch main into clone
                _git(
                    clone_path,
                    "fetch",
                    "--depth=1",
                    "origin",
                    "+refs/heads/main:refs/remotes/origin/main",
                )
                # Attempt to resolve baseline against origin/main; histories are disjoint
                resolved = checker.resolve_baseline_ref(
                    clone_path, explicit_ref="origin/main"
                )
                self.assertIsNone(resolved)
            _git(self.root, "checkout", "main")
            _git(self.root, "branch", "-D", "disjoint")
            _git(self.root, "remote", "remove", "origin")


class LiveRepoTests(unittest.TestCase):
    def test_current_accepted_adrs_pass(self) -> None:
        """Every committed accepted ADR in this repository currently passes."""
        decisions = sorted((checker.REPO_ROOT / "docs" / "decisions").glob("*.md"))
        self.assertTrue(decisions)
        rels = [str(path.relative_to(checker.REPO_ROOT)) for path in decisions]
        self.assertEqual(0, checker.check(rels))


if __name__ == "__main__":
    unittest.main()
