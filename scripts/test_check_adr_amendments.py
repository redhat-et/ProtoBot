from __future__ import annotations

import contextlib
import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

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
        """Insertions that carry a dated amendment marker are allowed."""
        old = ["alpha", "bravo", "charlie"]
        new = [
            "alpha",
            "**[Amended October 2026]** Clarified bravo.",
            "bravo",
            "charlie",
            "**[Amended November 2026]** Added a trailing note.",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_undated_paragraph_inserted_in_middle_fails(self) -> None:
        """An undated paragraph inserted in the middle is a mutation."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "undated note", "bravo", "charlie"]
        self.assertEqual(
            [(None, 2, 2, "undated note")], checker.historical_mutations(old, new)
        )

    def test_undated_paragraph_appended_at_tail_fails(self) -> None:
        """An undated paragraph appended at the tail is a mutation."""
        old = ["alpha", "bravo", "charlie"]
        new = old + ["undated trailing note"]
        self.assertEqual(
            [(None, 4, 4, "undated trailing note")],
            checker.historical_mutations(old, new),
        )

    def test_replaced_line_is_reported(self) -> None:
        """Rewriting a historical line reports its baseline and HEAD line."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "bravo changed", "charlie"]
        self.assertEqual([(2, 2, 2, "bravo")], checker.historical_mutations(old, new))

    def test_deleted_line_is_reported(self) -> None:
        """Removing a historical line reports its baseline line and no HEAD line."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "charlie"]
        self.assertEqual(
            [(2, None, None, "bravo")], checker.historical_mutations(old, new)
        )

    def test_insertions_before_mutation_report_distinct_baseline_and_head_lines(
        self,
    ) -> None:
        """An insertion before a mutation shifts the HEAD line, not the baseline line."""
        old = [f"line {n}" for n in range(1, 13)]
        new = list(old)
        # Insert 3 lines before baseline line 6 (0-indexed 5), well clear of
        # the later mutation so the opcodes do not bundle the two edits.
        new[5:5] = ["extra 1", "extra 2", "extra 3"]
        # Mutate what was baseline line 10; it is now at HEAD line 13.
        new[12] = "line 10 changed"
        self.assertEqual(
            [
                (10, 13, 13, "line 10"),
                (None, 6, 6, "extra 1"),
                (None, 7, 7, "extra 2"),
                (None, 8, 8, "extra 3"),
            ],
            checker.historical_mutations(old, new),
        )

    def test_insertion_merged_with_rewrite_reports_head_line_range(self) -> None:
        """A rewrite merged with an adjacent insertion reports the whole HEAD block."""
        old = ["alpha", "bravo", "charlie"]
        new = ["alpha", "inserted", "bravo changed", "charlie"]
        self.assertEqual(
            [(2, 2, 3, "bravo")],
            checker.historical_mutations(old, new),
        )

    def test_equal_count_multiline_replace_reports_whole_head_block(self) -> None:
        """A multi-line replace with equal counts reports the whole HEAD block."""
        old = ["alpha", "bravo", "charlie", "delta"]
        new = ["alpha", "BRAVO", "CHARLIE", "delta"]
        self.assertEqual(
            [
                (2, 2, 3, "bravo"),
                (3, 2, 3, "charlie"),
            ],
            checker.historical_mutations(old, new),
        )

    def test_inserted_fence_around_header_status_line_fails(self) -> None:
        """An insertion that fences the header status line fails.

        The status line sits at line 30, the last line of the header
        window. Inserting fences around it moves it to line 31 inside a
        fence, so a later pull request could delete the record.
        """
        status = "> Status: **Accepted** — September 2026"
        old = ["# ADR-0001: Late status", ""]
        old += [f"Filler line {n}." for n in range(1, 28)]
        old.append(status)
        old += ["", "## Decision", "", "Use one file per record."]
        new = old[:29] + ["```text", status, "```"] + old[30:]
        self.assertEqual(
            [
                (30, 31, 31, status),
                (None, 30, 30, "```text"),
                (None, 32, 32, "```"),
            ],
            checker.historical_mutations(old, new),
        )

    def test_replaced_status_line_reports_single_mutation(self) -> None:
        """A status line replaced by a fenced line reports one mutation, not two.

        The replace path already reports the replaced line, so the
        fence check must not map through a replace opcode and report
        the same line again.
        """
        old = [
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "",
            "Use one file per record.",
        ]
        new = [
            "# ADR-0002: Example",
            "",
            "```text",
            "",
            "Use one file per record.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [(3, 3, 3, "> Status: **Accepted** — September 2026")],
            mutations,
        )

    def test_identical_files_have_no_mutations(self) -> None:
        """Unchanged accepted text is not a mutation."""
        lines = ["alpha", "bravo"]
        self.assertEqual([], checker.historical_mutations(lines, list(lines)))

    def test_inline_pre_code_span_does_not_mask_subsequent_lines(self) -> None:
        """An inline code span containing `<pre>` does not mask later lines."""
        old = ["alpha", "bravo", "Use `<pre>` for code."]
        new = old + [
            "",
            "**[Amended November 2026]**",
            "",
            "Valid amendment text.",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_genuine_pre_block_after_inline_code_span_still_masked(self) -> None:
        """A real <pre> block is still masked even after an inline code span."""
        old = ["alpha", "Use `<pre>` for code.", "bravo"]
        new = list(old)
        new += [
            "<pre>",
            "**[Amended November 2026]**",
            "</pre>",
        ]
        self.assertEqual(
            [
                (None, 4, 4, "<pre>"),
                (None, 5, 5, "**[Amended November 2026]**"),
                (None, 6, 6, "</pre>"),
            ],
            checker.historical_mutations(old, new),
        )


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

    def test_dated_accepted_status_outside_header_is_ignored(self) -> None:
        """A canonical dated Accepted status below the header does not lock the file."""
        body = "\n".join(
            [
                "# ADR-0010: Draft",
                "",
                "> Status: **Proposed** — November 2026",
            ]
            + [""] * 40
            + ["> Status: **Accepted** — September 2026"]
        )
        self.assertFalse(checker.is_accepted(body))

    def test_accepted_header_with_trailing_text_is_detected(self) -> None:
        """Fails closed: trailing text after Accepted status still marks file protected."""
        body = "# ADR-0010: Legacy\n\n> Status: **Accepted** (legacy note)\n"
        self.assertTrue(checker.is_accepted(body))
        self.assertTrue(checker.is_protected(body))

    def test_accepted_status_after_header_window_still_protects(self) -> None:
        """An Accepted status line below the header window still protects the file."""
        lines = ["# ADR-0010: Late status", ""]
        lines += [f"Filler line {n}." for n in range(1, 29)]
        lines.append("> Status: **Accepted** — September 2026")
        body = "\n".join(lines) + "\n"
        self.assertFalse(checker.is_accepted(body))
        self.assertTrue(checker.is_protected(body))

    def test_fenced_header_status_line_still_protects(self) -> None:
        """A status line fenced into the header still protects the file."""
        lines = [
            "# ADR-0002: Example",
            "",
            "```text",
            "> Status: **Accepted** — September 2026",
            "```",
            "",
            "## Decision",
            "",
            "Use one file per record.",
        ]
        self.assertTrue(checker.is_protected("\n".join(lines) + "\n"))

    def test_fenced_canonical_status_anywhere_protects(self) -> None:
        """A canonical status line inside a fence anywhere protects the file."""
        lines = ["# ADR-0010: Draft", "", "> Status: **Proposed** — November 2026"]
        lines += [""] * 30
        lines += ["```text", "> Status: **Accepted** — September 2026", "```"]
        text = "\n".join(lines) + "\n"
        self.assertTrue(checker.is_protected(text))

    def test_fenced_body_status_line_beyond_header_does_not_protect(self) -> None:
        """A fenced non-canonical status line below the header does not protect."""
        lines = [
            "# ADR-0010: Draft",
            "",
            "> Status: **Proposed** — November 2026",
        ]
        lines += [""] * 30
        lines += [
            "```text",
            "> status: accepted",
            "```",
        ]
        text = "\n".join(lines) + "\n"
        self.assertFalse(checker.is_protected(text))


class SupersededStatusTests(unittest.TestCase):
    _SUPERSEDED_ADR = """# ADR-0003: Example

> Status: **Superseded by ADR-0004** — November 2026

## Decision

Use one file per record.

## Consequences

Keep diffs small.

**[Amended November 2026]**
Superseded by ADR-0004; see docs/decisions/0004-adopt-protobot-v2.md.
"""

    def test_superseded_header_is_detected(self) -> None:
        """Detect the Superseded status line used by transitioned ADRs."""
        self.assertTrue(checker.is_superseded(self._SUPERSEDED_ADR))

    def test_accepted_header_is_not_superseded(self) -> None:
        """Accepted ADRs are not already in the superseded state."""
        self.assertFalse(checker.is_superseded(_ACCEPTED_ADR))

    def test_superseded_header_without_date_is_still_detected(self) -> None:
        """Lenient header matching tolerates an undated Superseded baseline."""
        text = self._SUPERSEDED_ADR.replace(
            "> Status: **Superseded by ADR-0004** — November 2026",
            "> Status: **Superseded by ADR-0004**",
        )
        self.assertTrue(checker.is_superseded(text))

    def test_dated_superseded_status_outside_header_is_ignored(self) -> None:
        """A dated Superseded status below the header does not lock the file."""
        body = "\n".join(
            [
                "# ADR-0010: Draft",
                "",
                "> Status: **Proposed** — November 2026",
            ]
            + [""] * 40
            + ["> Status: **Superseded by ADR-0004** — November 2026"]
        )
        self.assertFalse(checker.is_superseded(body))

    def test_superseded_header_with_trailing_text_is_detected(self) -> None:
        """Fails closed: trailing text after Superseded status still marks file protected."""
        body = (
            "# ADR-0003: Legacy\n\n> Status: **Superseded by ADR-0004** (deprecated)\n"
        )
        self.assertTrue(checker.is_superseded(body))
        self.assertTrue(checker.is_protected(body))

    def test_is_protected_covers_accepted_and_superseded(self) -> None:
        """Both Accepted and Superseded ADRs remain protected from rewriting."""
        self.assertTrue(checker.is_protected(_ACCEPTED_ADR))
        self.assertTrue(checker.is_protected(self._SUPERSEDED_ADR))
        self.assertFalse(checker.is_protected(_PROPOSED_ADR))

    def test_non_canonical_status_is_protected(self) -> None:
        """Non-canonical status lines are treated as protected (fail closed)."""
        cases = [
            "# ADR-0010: Legacy\n\n> Status: Accepted — September 2026\n",
            "# ADR-0010: Legacy\n\n> status: **Accepted** — September 2026\n",
            "# ADR-0010: Legacy\n\n> STATUS: accepted\n",
            "# ADR-0010: Legacy\n\n> Status: Superseded by ADR-0004\n",
            "# ADR-0010: Legacy\n\n> status: **Superseded by ADR-0004**\n",
        ]
        for text in cases:
            with self.subTest(text=text):
                self.assertTrue(checker.is_protected(text))

    def test_alternate_emphasis_status_lines_are_protected(self) -> None:
        """Status lines with underscore or triple-asterisk emphasis are protected."""
        cases = [
            "> Status: __Accepted__ — September 2026",
            "> Status: ***Accepted*** — September 2026",
            "> Status: __Superseded by ADR-0004__ — November 2026",
            "> Status: ***Superseded by ADR-0004*** — November 2026",
        ]
        for status_line in cases:
            with self.subTest(status_line=status_line):
                text = f"# ADR-0010: Legacy\n\n{status_line}\n"
                self.assertTrue(checker.is_protected(text))

    def test_non_standard_status_markup_is_protected(self) -> None:
        """Status lines with other markup than bold are protected (fail closed)."""
        cases = [
            "> Status: ****Accepted**** — September 2026",
            "> Status: <b>Accepted</b> — September 2026",
            "> Status: `Accepted` — September 2026",
            "> Status: record accepted September 2026",
        ]
        for status_line in cases:
            with self.subTest(status_line=status_line):
                text = f"# ADR-0010: Legacy\n\n{status_line}\n"
                self.assertTrue(checker.is_protected(text))
        self.assertFalse(checker.is_protected(_PROPOSED_ADR))

    def test_alternate_emphasis_accepted_cannot_transition(self) -> None:
        """An alternate-emphasis Accepted status line cannot authorize a transition."""
        for status_line in [
            "> Status: __Accepted__ — September 2026",
            "> Status: ***Accepted*** — September 2026",
        ]:
            with self.subTest(status_line=status_line):
                old = [
                    "# ADR-0002: Example",
                    "",
                    status_line,
                    "",
                    "## Decision",
                    "",
                    "Use one file per record.",
                ]
                new = list(old)
                new[2] = "> Status: **Superseded by ADR-0004** — November 2026"
                new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
                mutations = checker.historical_mutations(old, new)
                self.assertTrue(
                    mutations,
                    "an alternate-emphasis status line must not authorize "
                    "the transition",
                )
                self.assertIn(status_line, [mutation[3] for mutation in mutations])

    def test_alternate_emphasis_superseded_cannot_be_rewritten(self) -> None:
        """An alternate-emphasis Superseded status line cannot be rewritten."""
        for status_line in [
            "> Status: __Superseded by ADR-0004__ — November 2026",
            "> Status: ***Superseded by ADR-0004*** — November 2026",
        ]:
            with self.subTest(status_line=status_line):
                old = [
                    "# ADR-0002: Example",
                    "",
                    status_line,
                    "",
                    "## Decision",
                    "",
                    "Use one file per record.",
                ]
                new = list(old)
                new[2] = "> Status: **Superseded by ADR-0005** — November 2026"
                new += ["", "**[Amended November 2026]**", "Superseded by ADR-0005."]
                mutations = checker.historical_mutations(old, new)
                self.assertTrue(
                    mutations,
                    "an alternate-emphasis status line must not authorize a rewrite",
                )
                self.assertIn(status_line, [mutation[3] for mutation in mutations])

    def test_missing_status_is_not_protected(self) -> None:
        """An ADR with no status header is not protected."""
        body = "# ADR-0010: Note\n\n## Decision\n\nSome note.\n"
        self.assertFalse(checker.is_accepted(body))
        self.assertFalse(checker.is_superseded(body))
        self.assertFalse(checker.is_protected(body))


class SupersessionTransitionTests(unittest.TestCase):
    """Cover the authorized accepted-to-superseded status transition."""

    def setUp(self) -> None:
        self.old = [
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "",
            "## Decision",
            "",
            "Use one file per record.",
            "",
            "## Consequences",
            "",
            "Keep diffs small.",
        ]
        self.status_idx = 2  # 0-based index of the status line

    def _with_status(self, status_line: str) -> list[str]:
        new = list(self.old)
        new[self.status_idx] = status_line
        return new

    def _transition_with_tail(self, tail: list[str]) -> list[str]:
        """Return a transition attempt whose appended block ends with *tail*."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        return new + ["", *tail]

    def _assert_transition_rejected(self, new: list[str]) -> None:
        """Assert that the status transition in *new* is not authorized."""
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(mutations, "the transition must not be authorized")
        self.assertIn(
            (
                self.status_idx + 1,
                self.status_idx + 1,
                self.status_idx + 1,
                self.old[self.status_idx],
            ),
            mutations,
        )

    def _assert_status_rewrite_rejected(self, new: list[str]) -> None:
        """Assert an unauthorized status flip reports the baseline and HEAD lines."""
        mutations = checker.historical_mutations(self.old, new)
        self.assertEqual(
            [
                (
                    self.status_idx + 1,
                    self.status_idx + 1,
                    self.status_idx + 1,
                    self.old[self.status_idx],
                ),
                (
                    None,
                    self.status_idx + 1,
                    self.status_idx + 1,
                    new[self.status_idx],
                ),
            ],
            mutations,
        )

    def test_dated_transition_with_amendment_block_passes(self) -> None:
        """An authorized transition with a valid appended amendment block has no mutations."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new += [
            "",
            "**[Amended November 2026]**",
            "Superseded by ADR-0004; see docs/decisions/0004-adopt-protobot-v2.md.",
        ]
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_unfenced_transition_with_appended_block_still_passes(self) -> None:
        """A normal unfenced status line keeps the authorized transition available."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        self.assertTrue(checker.is_protected("\n".join(self.old) + "\n"))
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_status_change_without_amendment_block_fails(self) -> None:
        """A status transition with no appended amendment block is a mutation."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        self._assert_status_rewrite_rejected(new)

    def test_undated_amendment_block_fails(self) -> None:
        """An amendment marker with no year is not recognized, so the transition fails."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new += ["", "**[Amended]** Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertEqual(
            [
                (
                    self.status_idx + 1,
                    self.status_idx + 1,
                    self.status_idx + 1,
                    self.old[self.status_idx],
                ),
                (
                    None,
                    self.status_idx + 1,
                    self.status_idx + 1,
                    new[self.status_idx],
                ),
                (None, len(new) - 1, len(new) - 1, ""),
                (None, len(new), len(new), "**[Amended]** Superseded by ADR-0004."),
            ],
            mutations,
        )

    def test_amendment_marker_inside_fenced_code_block_fails(self) -> None:
        """A marker quoted inside a fenced code block does not authorize the transition."""
        new = self._transition_with_tail(
            ["```text", "**[Amended November 2026]**", "```"]
        )
        self._assert_transition_rejected(new)

    def test_single_line_html_comment_marker_does_not_authorize(self) -> None:
        """A marker wrapped in a single-line HTML comment does not authorize."""
        new = self._transition_with_tail(
            ["<!-- **[Amended November 2026]** -->", "Superseded by ADR-0004."]
        )
        self._assert_transition_rejected(new)

    def test_multi_line_html_comment_marker_does_not_authorize(self) -> None:
        """A marker inside a multi-line HTML comment does not authorize."""
        new = self._transition_with_tail(
            [
                "<!--",
                "**[Amended November 2026]**",
                "Superseded by ADR-0004.",
                "-->",
            ]
        )
        self._assert_transition_rejected(new)

    def test_marker_after_closed_html_comment_still_authorizes(self) -> None:
        """A normal marker appended after a closed HTML comment still authorizes."""
        new = self._transition_with_tail(
            [
                "<!-- note -->",
                "**[Amended November 2026]**",
                "Superseded by ADR-0004.",
            ]
        )
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_commented_superseded_status_does_not_authorize(self) -> None:
        """A Superseded status inside an HTML comment does not authorize.

        The rendered document has no visible status, so the transition
        must be rejected even though the appended marker is normal.
        """
        new = list(self.old)
        new[self.status_idx : self.status_idx + 1] = [
            "<!--",
            "> Status: **Superseded by ADR-0004** — November 2026",
            "-->",
        ]
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(mutations, "a commented status line must not authorize")
        self.assertIn(self.old[self.status_idx], [m[3] for m in mutations])

    def test_commented_accepted_status_cannot_be_transition_source(self) -> None:
        """A commented Accepted status line cannot be the transition source.

        Both status lines sit inside matching HTML comments, so only
        the commented source rule can reject the transition.
        """
        old = list(self.old)
        old[self.status_idx : self.status_idx + 1] = [
            "<!--",
            "> Status: **Accepted** — September 2026",
            "-->",
        ]
        new = [
            "# ADR-0002: Example",
            "",
            "<!--",
            "> Status: **Superseded by ADR-0004** — November 2026",
            "-->",
            "",
            "## Decision",
            "",
            "Use one file per record.",
            "",
            "## Consequences",
            "",
            "Keep diffs small.",
            "",
            "**[Amended November 2026]**",
            "Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertTrue(mutations)
        self.assertIn(
            "> Status: **Accepted** — September 2026", [m[3] for m in mutations]
        )

    def test_amendment_block_before_last_baseline_line_fails(self) -> None:
        """An amendment block inserted before the end of the baseline is not authorized."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new[self.status_idx + 1 : self.status_idx + 1] = [
            "",
            "**[Amended November 2026]**",
            "Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(
            mutations, "an early amendment block must not authorize the transition"
        )
        self.assertIn(self.old[self.status_idx], [m[3] for m in mutations])

    def test_amendment_block_directly_after_status_line_fails(self) -> None:
        """A marker on the line immediately after the status line is not appended."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new[self.status_idx + 1 : self.status_idx + 1] = [
            "**[Amended November 2026]**",
            "Superseded by ADR-0004.",
        ]
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(
            mutations,
            "an amendment block directly after the status line must not "
            "authorize the transition",
        )
        self.assertIn(self.old[self.status_idx], [m[3] for m in mutations])

    def test_amendment_block_before_title_fails(self) -> None:
        """An amendment block inserted before the title is not appended."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new[0:0] = ["**[Amended November 2026]**", "Superseded by ADR-0004.", ""]
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(
            mutations,
            "an amendment block before the title must not authorize the transition",
        )
        self.assertIn(self.old[self.status_idx], [m[3] for m in mutations])

    def test_trailing_text_after_status_line_fails(self) -> None:
        """Trailing text after the dated status line is not a recognized transition."""
        new = self._with_status(
            "> Status: **Superseded by ADR-0004** — November 2026 (draft)"
        )
        self._assert_status_rewrite_rejected(new)

    def test_undated_superseded_status_header_fails(self) -> None:
        """An undated Superseded status header is not an authorized transition."""
        new = self._with_status("> Status: **Superseded by ADR-0004**")
        self._assert_status_rewrite_rejected(new)

    def test_status_change_combined_with_another_rewrite_fails(self) -> None:
        """An authorized-looking transition still fails if other text is also rewritten."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new[6] = "Use JSONL storage instead."
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        reported_text = [m[3] for m in mutations]
        self.assertIn(self.old[self.status_idx], reported_text)
        self.assertIn(self.old[6], reported_text)

    def test_second_status_line_is_reported_with_no_baseline_line(self) -> None:
        """Inserting an unrelated extra status line fails with a HEAD-only location."""
        new = list(self.old)
        new += ["", "> Status: **Superseded by ADR-0005** — November 2026"]
        mutations = checker.historical_mutations(self.old, new)
        self.assertEqual(
            [
                (None, len(new), len(new), new[-1]),
                (None, len(new) - 1, len(new) - 1, ""),
            ],
            mutations,
        )

    def test_unmodified_adr_with_two_status_lines_passes(self) -> None:
        """A baseline that already has two status lines is not flagged when unchanged."""
        old = list(self.old) + ["", "> Status: **Accepted** — September 2026"]
        new = list(old)
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_third_status_line_on_two_status_baseline_is_reported(self) -> None:
        """A third status line added to a two-status baseline is reported."""
        old = list(self.old) + ["", "> Status: **Accepted** — September 2026"]
        new = list(old) + ["", "> Status: **Superseded by ADR-0005** — November 2026"]
        mutations = checker.historical_mutations(old, new)
        self.assertEqual(
            [
                (None, len(new), len(new), new[-1]),
                (None, len(new) - 1, len(new) - 1, ""),
            ],
            mutations,
        )

    def test_duplicate_accepted_line_before_superseded_line_fails(self) -> None:
        """A duplicate Accepted line kept beside the new Superseded line fails.

        The authorized transition consumes the baseline Accepted line,
        so the Accepted line that remains in the file has no allowance
        and is reported with no baseline line.
        """
        new = list(self.old)
        new.insert(
            self.status_idx + 1,
            "> Status: **Superseded by ADR-0004** — November 2026",
        )
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertEqual(
            [
                (
                    None,
                    self.status_idx + 1,
                    self.status_idx + 1,
                    self.old[self.status_idx],
                )
            ],
            mutations,
        )

    def test_duplicate_accepted_line_after_superseded_line_fails(self) -> None:
        """A duplicate Accepted line placed after the new Superseded line fails."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new.insert(self.status_idx + 1, self.old[self.status_idx])
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertEqual(
            [
                (
                    None,
                    self.status_idx + 2,
                    self.status_idx + 2,
                    self.old[self.status_idx],
                )
            ],
            mutations,
        )

    def test_exact_amendment_marker_line_in_baseline_body_does_not_suppress_detection(
        self,
    ) -> None:
        """An exact pre-existing amendment marker line does not mask a rewrite."""
        old = list(self.old) + ["", "**[Amended January 2000]**"]
        new = list(old)
        new[6] = "Use JSONL storage instead."
        mutations = checker.historical_mutations(old, new)
        self.assertEqual([(7, 7, 7, "Use one file per record.")], mutations)

    def test_quoted_status_line_in_fenced_code_block_passes(self) -> None:
        """An amendment quoting a status line in fenced code does not reject transition."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new += [
            "",
            "**[Amended November 2026]**",
            "Superseded by ADR-0004. Prior status was:",
            "```text",
            "> Status: **Accepted** — September 2026",
            "```",
        ]
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_quoted_superseded_status_in_amendment_fenced_block_passes(self) -> None:
        """An amendment quoting a superseded status line in fenced code does not reject transition."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new += [
            "",
            "**[Amended November 2026]**",
            "Superseded by ADR-0004. New header looks like:",
            "```text",
            "> Status: **Superseded by ADR-0004** — November 2026",
            "```",
        ]
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_authorized_supersession_with_quoted_old_lines_passes(self) -> None:
        """An amendment that quotes old lines in a fence does not misalign the diff.

        The raw diff would align its longest block against the fenced
        quotation and report unchanged lines as rewritten. The synthetic
        baseline keeps the alignment on the real lines.
        """
        old = [
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "",
            "Use one file per record.",
        ]
        new = [
            "# ADR-0002: Example",
            "",
            "> Status: **Superseded by ADR-0004** — November 2026",
            "",
            "Use one file per record.",
            "",
            "**[Amended November 2026]**",
            "Superseded by ADR-0004. Original opening:",
            "",
            "```text",
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "```",
        ]
        self.assertEqual([], checker.historical_mutations(old, new))

    def test_unauthorized_edit_with_quoted_block_reports_real_mutation(self) -> None:
        """An unauthorized edit with a quoted block still reports the real mutation."""
        old = [
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "",
            "Use one file per record.",
        ]
        new = [
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "",
            "Use JSONL storage instead.",
            "",
            "**[Amended November 2026]**",
            "Clarified the record. Original opening:",
            "",
            "```text",
            "# ADR-0002: Example",
            "",
            "> Status: **Accepted** — September 2026",
            "```",
        ]
        mutations = checker.historical_mutations(old, new)
        self.assertTrue(mutations)
        self.assertIn("Use one file per record.", [m[3] for m in mutations])

    def test_duplicate_dated_superseded_status_lines_fails(self) -> None:
        """Duplicate dated superseded lines in header invalidates the transition."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new.insert(
            self.status_idx + 1,
            "> Status: **Superseded by ADR-0004** — November 2026",
        )
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(mutations)

    def test_indented_opening_fence_does_not_authorize_transition(self) -> None:
        """A fence indented by one to three spaces still fences the marker."""
        new = self._transition_with_tail(
            [" ```", "**[Amended November 2026]**", " ```"]
        )
        self._assert_transition_rejected(new)

    def test_shorter_fence_inside_longer_fence_does_not_authorize_transition(
        self,
    ) -> None:
        """A three-backtick line inside a four-backtick fence does not close it."""
        new = self._transition_with_tail(
            ["````", "```", "**[Amended November 2026]**", "````"]
        )
        self._assert_transition_rejected(new)

    def test_info_string_closing_fence_does_not_authorize_transition(self) -> None:
        """A closing line with an info string does not close the fence."""
        new = self._transition_with_tail(
            ["```", "```text", "**[Amended November 2026]**"]
        )
        self._assert_transition_rejected(new)

    def test_mixed_character_fence_does_not_authorize_transition(self) -> None:
        """A tilde line does not close a backtick fence."""
        new = self._transition_with_tail(
            ["```", "~~~", "**[Amended November 2026]**", "```"]
        )
        self._assert_transition_rejected(new)

    def test_inserted_proposed_status_line_during_supersession_fails(self) -> None:
        """A supersession cannot add a second status field that declares a draft."""
        new = self._with_status("> Status: **Superseded by ADR-0004** — November 2026")
        new.insert(
            self.status_idx,
            "> Status: **Proposed** — November 2026",
        )
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertEqual(
            [
                (
                    None,
                    self.status_idx + 1,
                    self.status_idx + 1,
                    "> Status: **Proposed** — November 2026",
                )
            ],
            mutations,
        )

    def test_fenced_status_example_in_amendment_block_still_passes(self) -> None:
        """A fenced status example appended in an amendment block stays allowed."""
        new = list(self.old)
        new += [
            "",
            "**[Amended November 2026]**",
            "Clarified the record. Prior header:",
            "```text",
            "> Status: **Accepted** — September 2026",
            "```",
        ]
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_backtick_in_info_string_does_not_open_a_fence(self) -> None:
        """A backtick in a backtick fence info string does not open a fence."""
        new = self._transition_with_tail(
            ["```text `code`", "**[Amended November 2026]**", "```"]
        )
        self.assertEqual([], checker.historical_mutations(self.old, new))

    def test_non_breaking_space_does_not_close_a_fence(self) -> None:
        """A non-breaking space after a fence is info text, not a fence close."""
        new = self._transition_with_tail(
            ["```", "```\u00a0", "**[Amended November 2026]**"]
        )
        self._assert_transition_rejected(new)

    def test_pre_block_marker_does_not_authorize(self) -> None:
        """A marker inside a raw HTML preformatted block does not authorize."""
        new = self._transition_with_tail(
            ["<pre>", "**[Amended November 2026]**", "</pre>"]
        )
        self._assert_transition_rejected(new)

    def test_pre_block_status_cannot_authorize(self) -> None:
        """A status line inside a raw HTML preformatted block cannot authorize."""
        new = list(self.old)
        new[self.status_idx : self.status_idx + 1] = [
            "<pre>",
            "> Status: **Superseded by ADR-0004** — November 2026",
            "</pre>",
        ]
        new += ["", "**[Amended November 2026]**", "Superseded by ADR-0004."]
        mutations = checker.historical_mutations(self.old, new)
        self.assertTrue(mutations)
        self.assertIn(self.old[self.status_idx], [m[3] for m in mutations])


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

    def test_rename_destination_directory_symlink_fails(self) -> None:
        """A rename whose destination directory is a symlink is not read.

        Git never reports a rename destination beyond a symbolic link,
        because it refuses worktree paths under one. The guard protects
        against a malformed rename record, so the record is injected
        directly.
        """
        real = self.root / "docs" / "real-target"
        real.mkdir()
        (real / "0001-example.md").write_text(_ACCEPTED_ADR, encoding="utf-8")
        (self.root / "docs" / "renamed").symlink_to(real)
        dest = "docs/renamed/0001-example.md"
        (self.root / self.adr_rel).unlink()
        with mock.patch.object(
            checker,
            "get_renames",
            return_value=({self.adr_rel.as_posix(): dest}, None),
        ):
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
        self.assertEqual(1, rc)
        self.assertIn(
            f"{dest}: path traverses a symbolic link or cannot be inspected safely",
            stderr.getvalue(),
        )

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
        self.assertIn(
            "path traverses a symbolic link or cannot be inspected safely",
            stderr.getvalue(),
        )

    def test_decisions_directory_symlink_hides_rename_and_fails(self) -> None:
        """A docs/decisions directory symlink cannot hide a rename."""
        _git(self.root, "checkout", "-b", "feature")
        _git(self.root, "mv", "docs/decisions", "docs/archive")
        os.symlink("archive", self.root / "docs" / "decisions")
        _git(self.root, "add", "-A")
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-m",
            "hide rename behind a directory symlink",
        )
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc, _output = self._check()
        self.assertEqual(1, rc)
        self.assertIn(
            "path traverses a symbolic link or cannot be inspected safely",
            stderr.getvalue(),
        )

    def test_git_ls_tree_failure_fails_closed(self) -> None:
        """A failing git ls-tree enumeration fails the run."""
        real_nul = checker._git_nul

        def failing_ls_tree(repo_root: Path, *args: str):
            if "ls-tree" in args:
                return [], "git ls-tree failed: ls-tree boom"
            return real_nul(repo_root, *args)

        with mock.patch.object(checker, "_git_nul", failing_ls_tree):
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("git ls-tree failed", stderr.getvalue())

    def test_git_name_status_diff_failure_fails_closed(self) -> None:
        """A failing rename-detection git diff fails the run."""
        real_nul = checker._git_nul

        def failing_name_status(repo_root: Path, *args: str):
            if "diff" in args and "--name-status" in args:
                return [], "git diff failed: name-status boom"
            return real_nul(repo_root, *args)

        with mock.patch.object(checker, "_git_nul", failing_name_status):
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("git diff failed", stderr.getvalue())

    def test_git_name_only_diff_failure_fails_closed(self) -> None:
        """A failing path-enumeration git diff fails the run."""
        real_nul = checker._git_nul

        def failing_name_only(repo_root: Path, *args: str):
            if "diff" in args and "--name-only" in args:
                return [], "git diff failed: name-only boom"
            return real_nul(repo_root, *args)

        with mock.patch.object(checker, "_git_nul", failing_name_only):
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("git diff failed", stderr.getvalue())

    def test_accepted_status_after_line_30_rewrite_fails(self) -> None:
        """An accepted ADR with its status at line 31 is protected from rewriting."""
        lines = ["# ADR-0001: Late status", ""]
        lines += [f"Filler line {n}." for n in range(1, 29)]
        lines.append("> Status: **Accepted** — September 2026")
        lines += ["", "## Decision", "", "Use one file per record."]
        path = self.root / self.adr_rel
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "move the status below the header window",
        )
        path.write_text(
            "\n".join(lines).replace(
                "Use one file per record.", "Use JSONL storage instead."
            )
            + "\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_ambiguous_status_header_fails_the_run(self) -> None:
        """A baseline with a non-canonical status header fails the run."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace("**Accepted**", "__Accepted__"),
            encoding="utf-8",
        )
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "baseline with an ambiguous status header",
        )
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc, _output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "ambiguous status header: > Status: __Accepted__ — September 2026",
            stderr.getvalue(),
        )

    def test_four_star_status_header_fails_run_as_ambiguous(self) -> None:
        """An unmodified ADR with a four-star status header fails the run."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace("**Accepted**", "****Accepted****"),
            encoding="utf-8",
        )
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "baseline with a four-star status header",
        )
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rc, _output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "ambiguous status header: > Status: ****Accepted**** — September 2026",
            stderr.getvalue(),
        )

    def test_four_star_status_header_rewrite_fails(self) -> None:
        """An ADR with a four-star status header is protected from rewrites."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace("**Accepted**", "****Accepted****"),
            encoding="utf-8",
        )
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "baseline with a four-star status header",
        )
        path.write_text(
            _ACCEPTED_ADR.replace(
                "Use one file per record.", "Use JSONL storage instead."
            ).replace("**Accepted**", "****Accepted****"),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def _commit_fenced_header_baseline(self) -> list[str]:
        """Commit a baseline whose Accepted status line sits inside a fence."""
        lines = [
            "# ADR-0001: Example",
            "",
            "```text",
            "> Status: **Accepted** — September 2026",
            "```",
            "",
            "## Decision",
            "",
            "Use one file per record.",
            "",
            "## Consequences",
            "",
            "Keep diffs small.",
        ]
        path = self.root / self.adr_rel
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "fence the header status line",
        )
        return lines

    def test_fenced_header_baseline_rewrite_fails(self) -> None:
        """A baseline whose header status line is fenced stays protected from rewrites."""
        lines = self._commit_fenced_header_baseline()
        path = self.root / self.adr_rel
        path.write_text(
            "\n".join(lines).replace(
                "Use one file per record.", "Use JSONL storage instead."
            )
            + "\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_fenced_header_baseline_deletion_fails(self) -> None:
        """A baseline whose header status line is fenced stays protected from deletion."""
        self._commit_fenced_header_baseline()
        (self.root / self.adr_rel).unlink()
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def _commit_fenced_late_status_baseline(self) -> list[str]:
        """Commit a baseline whose Accepted status line is fenced below the header."""
        lines = ["# ADR-0001: Late status", ""]
        lines += [f"Filler line {n}." for n in range(1, 32)]
        lines += ["```text", "> Status: **Accepted** — September 2026", "```"]
        lines += ["", "## Decision", "", "Use one file per record."]
        path = self.root / self.adr_rel
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "fence the status line below the header window",
        )
        return lines

    def test_fenced_late_status_baseline_rewrite_fails(self) -> None:
        """A fenced canonical status below the header still protects from rewrites."""
        lines = self._commit_fenced_late_status_baseline()
        path = self.root / self.adr_rel
        path.write_text(
            "\n".join(lines).replace(
                "Use one file per record.", "Use JSONL storage instead."
            )
            + "\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)
        self.assertIn("Use one file per record.", output)

    def test_fenced_late_status_baseline_deletion_fails(self) -> None:
        """A fenced canonical status below the header still protects from deletion."""
        self._commit_fenced_late_status_baseline()
        (self.root / self.adr_rel).unlink()
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_proposed_adr_without_protected_baseline_stays_editable(self) -> None:
        """A Proposed ADR is not protected, and a rewrite passes."""
        self.assertFalse(checker.is_protected(_PROPOSED_ADR))
        rel = "docs/decisions/0009-draft.md"
        path = self.root / rel
        path.write_text(
            _PROPOSED_ADR.replace("A draft decision that may still change.", "Final."),
            encoding="utf-8",
        )
        rc, output = self._check(rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_deleted_non_ascii_adr_filename_fails(self) -> None:
        """A deleted ADR with a non-ASCII filename is reported."""
        rel = "docs/decisions/0003-résumé.md"
        path = self.root / rel
        path.write_text(_ACCEPTED_ADR, encoding="utf-8")
        _git(self.root, "add", rel)
        _git(self.root, "commit", "--no-gpg-sign", "-m", "add non-ASCII ADR")
        path.unlink()
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)
        self.assertIn(rel, output)

    def test_deleted_carriage_return_adr_filename_fails(self) -> None:
        """A deleted ADR whose filename contains a carriage return is reported."""
        rel = "docs/decisions/0002-a\rb.md"
        path = self.root / rel
        path.write_text(_ACCEPTED_ADR, encoding="utf-8")
        _git(self.root, "add", rel)
        _git(self.root, "commit", "--no-gpg-sign", "-m", "add carriage-return ADR")
        path.unlink()
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)
        self.assertIn(rel, output)

    def test_explicit_symlink_dotdot_path_is_rejected(self) -> None:
        """An explicit path that resolves outside the repository is rejected."""
        with tempfile.TemporaryDirectory() as outside_name:
            escape_target = Path(outside_name) / "sub"
            escape_target.mkdir()
            evil = Path(outside_name) / "0001-example.md"
            evil.write_text(_ACCEPTED_ADR, encoding="utf-8")
            link = self.root / "docs" / "decisions" / "escape"
            link.symlink_to(escape_target)
            raw = "docs/decisions/escape/../0001-example.md"
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc, _output = self._check(raw)
            self.assertEqual(1, rc)
            self.assertIn("path is outside the repository", stderr.getvalue())

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

    def test_authorized_supersession_transition_passes(self) -> None:
        """An accepted-to-superseded transition with an appended block passes end to end."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            )
            + "\n**[Amended November 2026]**\n"
            "Superseded by ADR-0004; see docs/decisions/0004-example.md.\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_superseded_status_change_without_amendment_fails(self) -> None:
        """A status flip with no appended amendment block still fails end to end."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            ),
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("rewritten or replaced in place", output)

    def test_deleted_superseded_adr_fails(self) -> None:
        """Protection continues after supersession: a superseded ADR cannot be deleted."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            )
            + "\n**[Amended November 2026]**\nSuperseded by ADR-0004.\n",
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "supersede ADR")
        path.unlink()
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_renamed_superseded_adr_fails(self) -> None:
        """Protection continues after supersession: a superseded ADR cannot be renamed."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            )
            + "\n**[Amended November 2026]**\nSuperseded by ADR-0004.\n",
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "supersede ADR")
        renamed_rel = "docs/decisions/0001-renamed.md"
        _git(self.root, "mv", self.adr_rel.as_posix(), renamed_rel)
        rc, output = self._check()
        self.assertEqual(1, rc)
        self.assertIn(f"was deleted (renamed to {renamed_rel})", output)

    def test_deleted_accepted_adr_with_trailing_text_fails(self) -> None:
        """Non-canonical Accepted status in baseline still protects file from deletion."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Accepted** (legacy note)",
            ),
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "baseline with legacy status")
        path.unlink()
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_superseded_adr_with_no_change_passes(self) -> None:
        """An unchanged superseded ADR in baseline passes end to end."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            )
            + "\n**[Amended November 2026]**\nSuperseded by ADR-0004.\n",
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "baseline superseded ADR")
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_superseded_adr_with_amendment_passes(self) -> None:
        """Appending an amendment to an already-superseded ADR passes end to end."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: **Superseded by ADR-0004** \u2014 November 2026",
            )
            + "\n**[Amended November 2026]**\nSuperseded by ADR-0004.\n",
            encoding="utf-8",
        )
        _git(self.root, "commit", "--no-gpg-sign", "-am", "baseline superseded ADR")
        path.write_text(
            path.read_text(encoding="utf-8")
            + "\n**[Amended December 2026]**\nFurther amendment note.\n",
            encoding="utf-8",
        )
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_proposed_adr_can_be_rewritten_and_deleted(self) -> None:
        """Proposed ADRs are not locked: rewriting or deleting them passes check."""
        prop_rel = "docs/decisions/0009-draft.md"
        path = self.root / prop_rel

        # Rewriting passes
        path.write_text(
            _PROPOSED_ADR.replace("A draft decision", "A rewritten draft decision"),
            encoding="utf-8",
        )
        rc, output = self._check(prop_rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

        # Deleting passes
        path.unlink()
        rc, output = self._check(prop_rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_missing_status_adr_can_be_rewritten_or_deleted(self) -> None:
        """An ADR with no status header is not protected and can be changed or removed."""
        unprotected_rel = "docs/decisions/0020-no-status.md"
        path = self.root / unprotected_rel
        path.write_text(
            "# ADR-0020: Draft\n\n## Decision\n\nDraft text.\n", encoding="utf-8"
        )
        _git(self.root, "add", unprotected_rel)
        _git(self.root, "commit", "--no-gpg-sign", "-m", "add unstatused ADR")

        # Rewrite
        path.write_text(
            "# ADR-0020: Draft\n\n## Decision\n\nModified draft text.\n",
            encoding="utf-8",
        )
        rc, output = self._check(unprotected_rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

        # Delete
        path.unlink()
        rc, output = self._check(unprotected_rel)
        self.assertEqual(0, rc)
        self.assertEqual("", output)

    def test_deleted_adr_with_non_canonical_status_fails(self) -> None:
        """Deleting an ADR with non-canonical status header fails closed."""
        path = self.root / self.adr_rel
        path.write_text(
            _ACCEPTED_ADR.replace(
                "> Status: **Accepted** \u2014 September 2026",
                "> Status: Accepted — September 2026",
            ),
            encoding="utf-8",
        )
        _git(
            self.root,
            "commit",
            "--no-gpg-sign",
            "-am",
            "baseline non-canonical status",
        )
        path.unlink()
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn("was deleted", output)

    def test_mutation_diagnostics_report_baseline_and_head_labels(self) -> None:
        """Verify report_mutations formatting for replaced, deleted, and inserted lines."""
        buf = io.StringIO()
        mutations = [
            (2, 4, 4, "replaced line text"),
            (3, 5, 8, "merged rewrite text"),
            (7, None, None, "deleted line text"),
            (None, 12, 12, "> Status: **Accepted** — September 2026"),
        ]
        with contextlib.redirect_stdout(buf):
            checker._report_mutations("docs/decisions/0001-example.md", mutations)
        output = buf.getvalue()
        self.assertIn("baseline line 2 / HEAD line 4: replaced line text", output)
        self.assertIn("baseline line 3 / HEAD lines 5-8: merged rewrite text", output)
        self.assertIn(
            "baseline line 7 (deleted, no corresponding HEAD line): deleted line text",
            output,
        )
        self.assertIn(
            "HEAD line 12 (no corresponding baseline line): > Status: **Accepted** — September 2026",
            output,
        )

    def test_check_reports_deleted_label_for_removed_line(self) -> None:
        """check() reports baseline line (deleted, no corresponding HEAD line) when line is removed."""
        path = self.root / self.adr_rel
        lines = _ACCEPTED_ADR.splitlines()
        lines.remove("Keep diffs small.")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "(deleted, no corresponding HEAD line): Keep diffs small.", output
        )

    def test_check_reports_inserted_label_for_extra_status_line(self) -> None:
        """check() reports HEAD line (no corresponding baseline line) when extra status line inserted."""
        path = self.root / self.adr_rel
        lines = _ACCEPTED_ADR.splitlines()
        lines.append("> Status: **Accepted** — October 2026")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "(no corresponding baseline line): > Status: **Accepted** — October 2026",
            output,
        )

    def test_check_reports_head_line_range_for_merged_insert_and_rewrite(self) -> None:
        """check() reports a HEAD line range when an insertion merges with a rewrite."""
        path = self.root / self.adr_rel
        lines = _ACCEPTED_ADR.splitlines()
        lines[6:6] = ["Inserted note."]
        lines[7] = "Use JSONL storage instead."
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        rc, output = self._check(self.adr_rel.as_posix())
        self.assertEqual(1, rc)
        self.assertIn(
            "baseline line 7 / HEAD lines 7-8: Use one file per record.", output
        )


class LiveRepoTests(unittest.TestCase):
    def test_current_accepted_adrs_pass(self) -> None:
        """Every committed accepted ADR in this repository currently passes."""
        decisions = sorted((checker.REPO_ROOT / "docs" / "decisions").glob("*.md"))
        self.assertTrue(decisions)
        rels = [str(path.relative_to(checker.REPO_ROOT)) for path in decisions]
        self.assertEqual(0, checker.check(rels))


if __name__ == "__main__":
    unittest.main()
