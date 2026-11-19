from __future__ import annotations

import contextlib
import http.client
import io
import json
import os
import subprocess
import tempfile
import unittest
import urllib.request
from pathlib import Path
from unittest.mock import patch

if __package__:
    from scripts import check_baseline_ratchet as checker
else:
    import check_baseline_ratchet as checker


def _ceiling(
    file_path: str = "AGENTS.md",
    value: int = 4000,
    rule_id: str = "context-budget",
) -> dict[str, object]:
    """Return one skillsaw ceiling baseline entry."""
    return {
        "fingerprint": "test",
        "rule_id": rule_id,
        "file_path": file_path,
        "message": "test",
        "severity": "warning",
        "value": value,
        "baseline_mode": "ceiling",
    }


def _baseline(*entries: dict[str, object]) -> dict[str, object]:
    """Return a skillsaw baseline document containing *entries*."""
    return {"version": "1", "violations": list(entries)}


class CompareCeilingsTests(unittest.TestCase):
    def test_increase_is_rejected(self) -> None:
        """Fail when a ceiling value rises relative to the base."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(value=4100)),
            _baseline(_ceiling(value=4000)),
        )

        expected = (
            "ERROR: .skillsaw-baseline.json ceiling for AGENTS.md "
            "increased from 4000 to 4100. Baseline ceilings may only "
            "ratchet downwards or remain unchanged."
        )
        self.assertEqual([expected], diagnostics)

    def test_increase_sanitizes_file_path(self) -> None:
        """Strip control bytes and :: from file_path in increase diagnostics."""
        malicious_path = "AGENTS.md\n::warning::bump"
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path=malicious_path, value=4100)),
            _baseline(_ceiling(file_path=malicious_path, value=4000)),
        )
        self.assertEqual(1, len(diagnostics))
        diag = diagnostics[0]
        self.assertNotIn("\n", diag)
        self.assertNotIn("::", diag)
        self.assertIn("AGENTS.mdwarningbump", diag)

    def test_same_value_is_accepted(self) -> None:
        """Allow a ceiling that matches the base value."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(value=4000)),
            _baseline(_ceiling(value=4000)),
        )

        self.assertEqual([], diagnostics)

    def test_decrease_is_accepted(self) -> None:
        """Allow a ceiling that ratchets downward."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(value=3900)),
            _baseline(_ceiling(value=4000)),
        )

        self.assertEqual([], diagnostics)

    def test_new_ceiling_key_is_accepted(self) -> None:
        """Allow a newly introduced (file_path, rule_id) ceiling."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path="NEW.md", value=100)),
            _baseline(_ceiling(value=4000)),
        )

        self.assertEqual([], diagnostics)

    def test_dropped_ceiling_for_existing_file_is_rejected(self) -> None:
        """Reject dropping a baseline ceiling entry if the file still exists on disk."""
        with tempfile.TemporaryDirectory() as tmp_dir:
            repo_root = Path(tmp_dir)
            (repo_root / "AGENTS.md").write_text("dummy", encoding="utf-8")
            diagnostics = checker.compare_ceilings(
                _baseline(),
                _baseline(_ceiling(file_path="AGENTS.md", value=4000)),
                repo_root=repo_root,
            )

        expected = (
            "ERROR: .skillsaw-baseline.json ceiling for AGENTS.md "
            "(context-budget) was removed while the file still exists. "
            "Baseline ceilings may only ratchet downwards or remain "
            "unchanged, not be deleted."
        )
        self.assertEqual([expected], diagnostics)

    def test_dropped_ceiling_for_deleted_file_is_accepted(self) -> None:
        """Permit dropping a baseline ceiling entry if the file was removed from the repo."""
        with tempfile.TemporaryDirectory() as tmp_dir:
            repo_root = Path(tmp_dir)
            diagnostics = checker.compare_ceilings(
                _baseline(),
                _baseline(
                    _ceiling(file_path="docs/nonexistent_deleted_file.md", value=100)
                ),
                repo_root=repo_root,
            )

        self.assertEqual([], diagnostics)

    def test_dropped_ceiling_without_repo_root_is_not_checked(self) -> None:
        """Skip dropped-ceiling detection entirely when repo_root is not given."""
        diagnostics = checker.compare_ceilings(
            _baseline(),
            _baseline(_ceiling(file_path="AGENTS.md", value=4000)),
        )

        self.assertEqual([], diagnostics)

    def test_dropped_ceiling_for_non_context_budget_rule_is_rejected(self) -> None:
        """Reject dropping a ceiling for any rule, not just context-budget."""
        with tempfile.TemporaryDirectory() as tmp_dir:
            repo_root = Path(tmp_dir)
            (repo_root / "docs/vision.md").parent.mkdir(parents=True, exist_ok=True)
            (repo_root / "docs/vision.md").write_text("dummy", encoding="utf-8")
            diagnostics = checker.compare_ceilings(
                _baseline(),
                _baseline(
                    _ceiling(
                        file_path="docs/vision.md",
                        rule_id="content-section-length",
                        value=500,
                    )
                ),
                repo_root=repo_root,
            )

        expected = (
            "ERROR: .skillsaw-baseline.json ceiling for docs/vision.md "
            "(content-section-length) was removed while the file still exists. "
            "Baseline ceilings may only ratchet downwards or remain "
            "unchanged, not be deleted."
        )
        self.assertEqual([expected], diagnostics)

    def test_non_ceiling_entries_are_ignored(self) -> None:
        """Ignore baseline rows that are not ceiling mode."""
        head = _baseline(
            {
                "rule_id": "content-section-length",
                "file_path": "AGENTS.md",
                "value": 9999,
            }
        )
        base = _baseline(
            {
                "rule_id": "content-section-length",
                "file_path": "AGENTS.md",
                "value": 1,
            }
        )

        self.assertEqual([], checker.compare_ceilings(head, base))

    def test_duplicate_head_entries_use_the_maximum(self) -> None:
        """Reject when any duplicate head value exceeds the base."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(value=4000), _ceiling(value=4100)),
            _baseline(_ceiling(value=4000)),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("increased from 4000 to 4100", diagnostics[0])

    def test_distinct_rule_ids_are_independent(self) -> None:
        """Ratchet each (file_path, rule_id) pair separately."""
        diagnostics = checker.compare_ceilings(
            _baseline(
                _ceiling(value=4000),
                _ceiling(rule_id="other-rule", value=20),
            ),
            _baseline(
                _ceiling(value=4000),
                _ceiling(rule_id="other-rule", value=10),
            ),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("increased from 10 to 20", diagnostics[0])

    def test_dot_slash_prefix_matches_canonical_base_path(self) -> None:
        """A ``./`` prefix on the head path must still ratchet against base."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path="./AGENTS.md", value=4100)),
            _baseline(_ceiling(file_path="AGENTS.md", value=4000)),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("AGENTS.md", diagnostics[0])
        self.assertIn("increased from 4000 to 4100", diagnostics[0])

    def test_redundant_separators_match_canonical_base_path(self) -> None:
        """Redundant path separators on the head path must still ratchet."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path="docs//vision.md", value=2050)),
            _baseline(_ceiling(file_path="docs/vision.md", value=2000)),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("docs/vision.md", diagnostics[0])
        self.assertIn("increased from 2000 to 2050", diagnostics[0])

    def test_backslash_separators_match_canonical_base_path(self) -> None:
        """Windows backslashes on the head path must still ratchet against base."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path=r"docs\vision.md", value=2050)),
            _baseline(_ceiling(file_path="docs/vision.md", value=2000)),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("docs/vision.md", diagnostics[0])
        self.assertIn("increased from 2000 to 2050", diagnostics[0])

    def test_trailing_separator_matches_canonical_base_path(self) -> None:
        """A trailing path separator on the head path must still ratchet."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path="AGENTS.md/", value=4100)),
            _baseline(_ceiling(file_path="AGENTS.md", value=4000)),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("AGENTS.md", diagnostics[0])
        self.assertIn("increased from 4000 to 4100", diagnostics[0])

    def test_equivalent_paths_ratchet_against_canonical_base_path(self) -> None:
        """Normalization is applied to both head and base before matching."""
        diagnostics = checker.compare_ceilings(
            _baseline(_ceiling(file_path="./docs//vision.md", value=2100)),
            _baseline(_ceiling(file_path="docs/vision.md", value=2050)),
        )

        self.assertEqual(1, len(diagnostics))
        self.assertIn("docs/vision.md", diagnostics[0])
        self.assertIn("increased from 2050 to 2100", diagnostics[0])


class ParseBaselineTests(unittest.TestCase):
    def test_invalid_json_is_rejected(self) -> None:
        """Reject a baseline file that is not JSON."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.parse_baseline("{", "working tree")

        self.assertIn("invalid JSON in working tree", str(raised.exception))

    def test_non_object_root_is_rejected(self) -> None:
        """Reject a JSON array at the document root."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.parse_baseline("[]", "working tree")

        self.assertIn("must be a JSON object", str(raised.exception))

    def test_non_list_violations_are_rejected(self) -> None:
        """Reject a violations field that is not a list."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.parse_baseline('{"violations": {}}', "working tree")

        self.assertIn("must be a list", str(raised.exception))

    def test_non_integer_ceiling_value_is_rejected(self) -> None:
        """Reject a ceiling whose value is not an integer."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(
                _baseline(_ceiling(value="4000")),  # type: ignore[arg-type]
                "working tree",
            )

        self.assertIn("non-integer value", str(raised.exception))

    def test_boolean_ceiling_value_is_rejected(self) -> None:
        """Reject a ceiling whose value is a boolean."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(
                _baseline(_ceiling(value=True)),  # type: ignore[arg-type]
                "working tree",
            )

        self.assertIn("non-integer value", str(raised.exception))

    def test_missing_file_path_is_rejected(self) -> None:
        """Reject a ceiling entry without file_path."""
        entry = _ceiling()
        del entry["file_path"]
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(_baseline(entry), "working tree")

        self.assertIn("missing file_path", str(raised.exception))

    def test_whitespace_file_path_is_rejected(self) -> None:
        """Reject a ceiling entry whose file_path is only whitespace."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(_baseline(_ceiling(file_path="   ")), "working tree")

        self.assertIn("missing file_path", str(raised.exception))

    def test_absolute_file_path_is_rejected(self) -> None:
        """Reject a ceiling entry whose file_path is absolute."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(
                _baseline(_ceiling(file_path="/AGENTS.md")), "working tree"
            )

        self.assertIn("invalid file_path", str(raised.exception))

    def test_parent_directory_file_path_is_rejected(self) -> None:
        """Reject a ceiling entry whose file_path contains parent directory segments."""
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(
                _baseline(_ceiling(file_path="../AGENTS.md")), "working tree"
            )

        self.assertIn("invalid file_path", str(raised.exception))

    def test_invalid_file_path_sanitizes_file_path(self) -> None:
        """Strip control bytes and :: from file_path in invalid file_path error messages."""
        malicious_path = "/AGENTS.md\n::warning::payload"
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(
                _baseline(_ceiling(file_path=malicious_path)), "working tree"
            )
        msg = str(raised.exception)
        self.assertNotIn("\n", msg)
        self.assertNotIn("::", msg)
        self.assertIn("invalid file_path", msg)

    def test_non_integer_ceiling_sanitizes_file_path(self) -> None:
        """Strip control bytes and :: from file_path in non-integer error messages."""
        malicious_path = "AGENTS.md\n::warning::injected\r\n::add-mask::token"
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(
                _baseline(_ceiling(file_path=malicious_path, value="bad")),  # type: ignore[arg-type]
                "working tree",
            )
        msg = str(raised.exception)
        self.assertNotIn("\n", msg)
        self.assertNotIn("\r", msg)
        self.assertNotIn("::", msg)
        self.assertIn("AGENTS.mdwarninginjectedadd-masktoken", msg)

    def test_missing_rule_id_sanitizes_file_path(self) -> None:
        """Strip control bytes and :: from file_path when rule_id is missing."""
        malicious_path = "AGENTS.md\n::warning::payload"
        entry = _ceiling(file_path=malicious_path)
        del entry["rule_id"]
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(_baseline(entry), "working tree")
        msg = str(raised.exception)
        self.assertNotIn("\n", msg)
        self.assertNotIn("::", msg)
        self.assertIn("AGENTS.mdwarningpayload", msg)

    def test_missing_value_sanitizes_file_path(self) -> None:
        """Strip control bytes and :: from file_path when value is missing."""
        malicious_path = "AGENTS.md\r\n::warning::payload"
        entry = _ceiling(file_path=malicious_path)
        del entry["value"]
        with self.assertRaises(checker.BaselineRatchetError) as raised:
            checker.ceiling_values(_baseline(entry), "working tree")
        msg = str(raised.exception)
        self.assertNotIn("\r", msg)
        self.assertNotIn("\n", msg)
        self.assertNotIn("::", msg)
        self.assertIn("AGENTS.mdwarningpayload", msg)


class SanitizeLogStringTests(unittest.TestCase):
    def test_clean_string_is_unchanged(self) -> None:
        """Leave safe strings without newlines, control bytes, or :: untouched."""
        self.assertEqual("AGENTS.md", checker.sanitize_log_string("AGENTS.md"))
        self.assertEqual(
            "docs/vision.md", checker.sanitize_log_string("docs/vision.md")
        )
        self.assertEqual(
            "path with spaces.txt", checker.sanitize_log_string("path with spaces.txt")
        )

    def test_strip_crlf_and_control_bytes(self) -> None:
        """Strip CR, LF, null bytes, escape characters, and DEL."""
        self.assertEqual("foobar", checker.sanitize_log_string("foo\r\nbar"))
        self.assertEqual(
            "foobar", checker.sanitize_log_string("foo\x00\x07\x1b\x7fbar")
        )
        self.assertEqual("foobar", checker.sanitize_log_string("\n\rfoo\tbar\n"))

    def test_strip_ascii_colons(self) -> None:
        """Strip ASCII '::' workflow command delimiters, including nested or repeated."""
        self.assertEqual("warning", checker.sanitize_log_string("::warning::"))
        self.assertEqual("", checker.sanitize_log_string("::::"))
        self.assertEqual(":", checker.sanitize_log_string(":::"))
        self.assertEqual(
            "set-output name=xval",
            checker.sanitize_log_string("::set-output name=x::val"),
        )

    def test_strip_crlf_and_workflow_commands_combined(self) -> None:
        """Neutralize workflow command injection payloads in interpolated values."""
        payload = (
            "AGENTS.md\r\n::warning::malicious workflow command\n::add-mask::secret"
        )
        sanitized = checker.sanitize_log_string(payload)
        self.assertNotIn("\r", sanitized)
        self.assertNotIn("\n", sanitized)
        self.assertNotIn("::", sanitized)
        self.assertEqual(
            "AGENTS.mdwarningmalicious workflow commandadd-masksecret",
            sanitized,
        )


class GitHubSourceTests(unittest.TestCase):
    def test_repository_from_env(self) -> None:
        """Prefer GITHUB_REPOSITORY when it is a valid owner/repo."""
        with patch.dict(
            os.environ, {"GITHUB_REPOSITORY": "redhat-et/ProtoBot"}, clear=False
        ):
            self.assertEqual(
                "redhat-et/ProtoBot",
                checker.github_repository(Path("/tmp")),
            )

    def test_repository_from_origin_url(self) -> None:
        """Parse owner/repo from an HTTPS origin remote."""
        with (
            patch.dict(os.environ, {"GITHUB_REPOSITORY": ""}, clear=False),
            patch.object(
                checker,
                "_git",
                return_value=subprocess.CompletedProcess(
                    args=["git"],
                    returncode=0,
                    stdout="https://github.com/redhat-et/ProtoBot.git\n",
                    stderr="",
                ),
            ),
        ):
            self.assertEqual(
                "redhat-et/ProtoBot",
                checker.github_repository(Path("/tmp")),
            )

    def test_http_base_branch_uses_github_base_ref(self) -> None:
        """Use GITHUB_BASE_REF as the raw-file branch when it is a safe ref."""
        with patch.dict(os.environ, {"GITHUB_BASE_REF": "release-1"}, clear=False):
            self.assertEqual("release-1", checker.http_base_branch())

    def test_fetch_baseline_http_returns_none_on_url_error(self) -> None:
        """Treat network failures as a skip rather than a hard error."""
        with patch.object(
            checker.urllib.request,
            "build_opener",
        ) as mock_build_opener:
            mock_opener = mock_build_opener.return_value
            mock_opener.open.side_effect = checker.urllib.error.URLError("offline")
            self.assertIsNone(checker.fetch_baseline_http("redhat-et/ProtoBot", "main"))

    def test_fetch_baseline_http_uses_no_redirect_handler(self) -> None:
        """Use _NoRedirectHandler in fetch_baseline_http to prevent redirects."""
        with patch.object(
            checker.urllib.request,
            "build_opener",
        ) as mock_build_opener:
            mock_opener = mock_build_opener.return_value
            mock_opener.open.side_effect = checker.urllib.error.URLError("offline")
            checker.fetch_baseline_http("redhat-et/ProtoBot", "main")
            self.assertTrue(mock_build_opener.called)
            self.assertIsInstance(
                mock_build_opener.call_args[0][0],
                checker._NoRedirectHandler,
            )

    def test_fetch_baseline_http_returns_none_on_redirect(self) -> None:
        """Return None when HTTP request encounters a redirect."""
        with patch.object(
            checker.urllib.request,
            "build_opener",
        ) as mock_build_opener:
            mock_opener = mock_build_opener.return_value
            mock_opener.open.side_effect = checker._UnexpectedRedirectError("redirect")
            self.assertIsNone(checker.fetch_baseline_http("redhat-et/ProtoBot", "main"))

    def test_fetch_baseline_http_returns_none_on_http_exception(self) -> None:
        """Treat http.client.HTTPException as a fetch failure rather than a traceback."""
        with patch.object(
            checker.urllib.request,
            "build_opener",
        ) as mock_build_opener:
            mock_opener = mock_build_opener.return_value
            mock_opener.open.side_effect = http.client.BadStatusLine("corrupt")
            self.assertIsNone(checker.fetch_baseline_http("redhat-et/ProtoBot", "main"))

    def test_fetch_baseline_http_returns_none_on_incomplete_read(self) -> None:
        """Treat http.client.IncompleteRead as a fetch failure rather than a traceback."""
        with patch.object(
            checker.urllib.request,
            "build_opener",
        ) as mock_build_opener:
            mock_opener = mock_build_opener.return_value
            mock_response = mock_opener.open.return_value.__enter__.return_value
            mock_response.read.side_effect = http.client.IncompleteRead(b"partial")
            self.assertIsNone(checker.fetch_baseline_http("redhat-et/ProtoBot", "main"))


class NoRedirectHandlerTests(unittest.TestCase):
    def test_redirect_request_raises_unexpected_redirect(self) -> None:
        """Reject HTTP redirects with _UnexpectedRedirectError."""
        handler = checker._NoRedirectHandler()
        request = urllib.request.Request("https://raw.githubusercontent.com/test")
        with self.assertRaises(checker._UnexpectedRedirectError):
            handler.redirect_request(
                request,
                response=object(),
                code=302,
                message="Found",
                headers=object(),
                new_url="https://evil.example.com",
            )


class SafeRefTests(unittest.TestCase):
    def test_typical_refs_are_accepted(self) -> None:
        """Accept ordinary git ref names used as base-branch candidates."""
        for ref in (
            "main",
            "origin/main",
            "upstream/main",
            "refs/heads/main",
            "HEAD~1",
            "origin/main~2",
        ):
            with self.subTest(ref=ref):
                self.assertTrue(checker.is_safe_ref(ref))

    def test_path_smuggling_and_flags_are_rejected(self) -> None:
        """Reject refs that could be parsed as a path or git option."""
        for ref in ("main:.git/config", "--output=/tmp/x", "", "-main"):
            with self.subTest(ref=ref):
                self.assertFalse(checker.is_safe_ref(ref))
                self.assertFalse(checker.git_ref_exists(Path("/tmp"), ref))


class CandidateRefTests(unittest.TestCase):
    def setUp(self) -> None:
        patcher = patch.dict(
            os.environ,
            {"GITHUB_BASE_REF": "", "BASELINE_RATCHET_BASE_REF": ""},
        )
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_resolution_order_prefers_explicit_then_env(self) -> None:
        """Prefer --base-ref, then the env override, then GITHUB_BASE_REF, then defaults."""
        environ = {
            "BASELINE_RATCHET_BASE_REF": "env-ref",
            "GITHUB_BASE_REF": "gh-base",
        }
        with patch.dict(os.environ, environ, clear=False):
            self.assertEqual(
                [
                    "explicit",
                    "env-ref",
                    "origin/gh-base",
                    "gh-base",
                    "upstream/main",
                    "origin/main",
                    "main",
                ],
                checker._candidate_base_refs("explicit"),
            )

    def test_blank_env_values_are_ignored(self) -> None:
        """Skip empty environment overrides instead of inserting blanks."""
        environ = {
            "BASELINE_RATCHET_BASE_REF": "  ",
            "GITHUB_BASE_REF": "",
        }
        with patch.dict(os.environ, environ, clear=False):
            self.assertEqual(
                ["upstream/main", "origin/main", "main"],
                checker._candidate_base_refs(),
            )

    def test_candidate_ref_resolving_to_head_is_not_skipped(self) -> None:
        """Do not skip candidate refs whose commit SHA matches HEAD."""
        head_sha = "abc1234"

        def fake_commit_sha(repo_root: Path, ref: str) -> str | None:
            if ref in ("HEAD", "main"):
                return head_sha
            return None

        with patch.object(checker, "git_commit_sha", side_effect=fake_commit_sha):
            resolved = checker.resolve_base_ref(Path("/tmp"))
            self.assertEqual("main", resolved)

    def test_stale_first_candidate_without_baseline_is_skipped(self) -> None:
        """Skip candidate refs that lack a baseline file when a later candidate has it."""

        def fake_git_ref_exists(repo_root: Path, ref: str) -> bool:
            return ref in ("upstream/main", "origin/main")

        def fake_git_show_file(repo_root: Path, ref: str, path: str) -> str | None:
            if ref == "upstream/main":
                return None
            if ref == "origin/main":
                return '{"violations": []}'
            return None

        with (
            patch.object(checker, "git_ref_exists", side_effect=fake_git_ref_exists),
            patch.object(checker, "git_show_file", side_effect=fake_git_show_file),
        ):
            resolved = checker.resolve_base_ref(Path("/tmp"))
            self.assertEqual("origin/main", resolved)

    def test_all_candidates_lacking_baseline_falls_back_to_first_existing(self) -> None:
        """Fall back to the first existing candidate when none have the baseline file."""

        def fake_git_ref_exists(repo_root: Path, ref: str) -> bool:
            return ref in ("upstream/main", "origin/main")

        def fake_git_show_file(repo_root: Path, ref: str, path: str) -> str | None:
            return None

        with (
            patch.object(checker, "git_ref_exists", side_effect=fake_git_ref_exists),
            patch.object(checker, "git_show_file", side_effect=fake_git_show_file),
        ):
            resolved = checker.resolve_base_ref(Path("/tmp"))
            self.assertEqual("upstream/main", resolved)

    def test_all_candidates_lacking_baseline_with_github_base_ref(self) -> None:
        """With GITHUB_BASE_REF set, fall back to the first existing candidate (origin/<base>)."""

        def fake_git_ref_exists(repo_root: Path, ref: str) -> bool:
            return ref in ("upstream/main", "origin/main")

        def fake_git_show_file(repo_root: Path, ref: str, path: str) -> str | None:
            return None

        with (
            patch.dict(os.environ, {"GITHUB_BASE_REF": "main"}),
            patch.object(checker, "git_ref_exists", side_effect=fake_git_ref_exists),
            patch.object(checker, "git_show_file", side_effect=fake_git_show_file),
        ):
            resolved = checker.resolve_base_ref(Path("/tmp"))
            self.assertEqual("origin/main", resolved)


class ContextBudgetCategoryTests(unittest.TestCase):
    def test_agents_md_categorization(self) -> None:
        """Categorize AGENTS.md as agents-md."""
        self.assertEqual("agents-md", checker.context_budget_category("AGENTS.md"))
        self.assertEqual("agents-md", checker.context_budget_category("subdir/AGENTS.md"))

    def test_skill_categorization(self) -> None:
        """Categorize SKILL.md and paths under skills/ as skill."""
        self.assertEqual("skill", checker.context_budget_category("SKILL.md"))
        self.assertEqual(
            "skill",
            checker.context_budget_category(".agents/skills/my-skill/SKILL.md"),
        )
        self.assertEqual(
            "skill",
            checker.context_budget_category(".agents/skills/helper/reference.md"),
        )

    def test_instruction_categorization(self) -> None:
        """Categorize instruction files including CLAUDE.md and COPILOT.md."""
        for filename in (
            "CLAUDE.md",
            "COPILOT.md",
            "INSTRUCTIONS.md",
            ".cursorrules",
            ".windsurfrules",
            "copilot-instructions.md",
        ):
            with self.subTest(file=filename):
                self.assertEqual(
                    "instruction", checker.context_budget_category(filename)
                )
                self.assertEqual(
                    "instruction",
                    checker.context_budget_category(f"nested/dir/{filename}"),
                )

    def test_memory_categorization(self) -> None:
        """Categorize MEMORY.md as memory."""
        self.assertEqual("memory", checker.context_budget_category("MEMORY.md"))
        self.assertEqual(
            "memory", checker.context_budget_category(".claude/memory/MEMORY.md")
        )

    def test_extra_categorization_with_exact_path(self) -> None:
        """Categorize files listed in .skillsaw.yaml content-paths as extra."""
        config = {"content-paths": ["docs/vision.md", "docs/architecture.md"]}
        self.assertEqual(
            "extra", checker.context_budget_category("docs/vision.md", config)
        )
        self.assertEqual(
            "extra", checker.context_budget_category("docs/architecture.md", config)
        )
        self.assertIsNone(checker.context_budget_category("docs/other.md", config))

    def test_extra_categorization_with_glob_pattern(self) -> None:
        """Categorize files matching .skillsaw.yaml content-paths glob patterns as extra."""
        config = {"content-paths": ["docs/*.md", "specs/*.txt"]}
        self.assertEqual(
            "extra", checker.context_budget_category("docs/vision.md", config)
        )
        self.assertEqual(
            "extra",
            checker.context_budget_category("specs/spec.txt", config),
        )
        self.assertIsNone(checker.context_budget_category("other/file.md", config))

    def test_canonical_category_names_pass_through(self) -> None:
        """Pass through canonical category names unchanged."""
        for category in ("agents-md", "skill", "instruction", "memory", "extra"):
            with self.subTest(category=category):
                self.assertEqual(category, checker.context_budget_category(category))

    def test_unrecognized_file_returns_none(self) -> None:
        """Return None for files not matching any recognized category."""
        self.assertIsNone(checker.context_budget_category("random_file.py"))
        self.assertIsNone(checker.context_budget_category("README.md"))
        self.assertIsNone(checker.context_budget_category("docs/vision.md", config=None))

    def test_config_with_invalid_or_missing_content_paths(self) -> None:
        """Handle config missing content-paths or having non-string elements safely."""
        self.assertIsNone(checker.context_budget_category("docs/vision.md", config={}))
        self.assertIsNone(
            checker.context_budget_category("docs/vision.md", config={"content-paths": None})
        )
        self.assertIsNone(
            checker.context_budget_category(
                "docs/vision.md", config={"content-paths": [123, None]}
            )
        )


class CompareWarnLimitsTests(unittest.TestCase):
    def test_missing_limits_entry_is_rejected(self) -> None:
        """Reject context-budget ceiling with no corresponding .skillsaw.yaml limits entry."""
        head_index = {("AGENTS.md", "context-budget"): [4000]}
        config = {"rules": {"context-budget": {"limits": {}}}}
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            "is missing a rules.context-budget.limits.agents-md entry", diagnostics[0]
        )

    def test_miskeyed_or_renamed_limits_entry_is_rejected(self) -> None:
        """Reject mis-keyed or renamed .skillsaw.yaml limits entry for context-budget ceiling."""
        head_index = {("AGENTS.md", "context-budget"): [4000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "agents_md": {"warn": 4000},
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            "is missing a rules.context-budget.limits.agents-md entry", diagnostics[0]
        )

    def test_missing_or_non_numeric_warn_field_is_rejected(self) -> None:
        """Reject .skillsaw.yaml limits entry with missing or non-numeric warn value."""
        head_index = {("AGENTS.md", "context-budget"): [4000]}
        for invalid_warn in (
            {},
            {"warn": None},
            {"warn": "4000"},
            {"warn": True},
            None,
            "4000",
            True,
        ):
            with self.subTest(entry=invalid_warn):
                config = {
                    "rules": {
                        "context-budget": {
                            "limits": {
                                "agents-md": invalid_warn,
                            }
                        }
                    }
                }
                diagnostics = checker.compare_warn_limits(head_index, config)
                self.assertEqual(1, len(diagnostics))
                self.assertIn("is missing a numeric 'warn' limit", diagnostics[0])

    def test_integer_shorthand_warn_limit_is_accepted(self) -> None:
        """Accept integer shorthand for context-budget warn limits."""
        head_index = {("AGENTS.md", "context-budget"): [4000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "agents-md": 4000,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_integer_shorthand_warn_limit_exceeding_ceiling_is_rejected(self) -> None:
        """Reject integer shorthand warn limit when it exceeds the baseline ceiling."""
        head_index = {("AGENTS.md", "context-budget"): [4000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "agents-md": 4100,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            "context-budget warn limit for AGENTS.md (4100) exceeds baseline ceiling (4000)",
            diagnostics[0],
        )

    def test_skill_category_ceiling_with_mapping_is_accepted(self) -> None:
        """Accept a skill category ceiling validated against limits.skill mapping."""
        head_index = {(".agents/skills/helper/SKILL.md", "context-budget"): [3000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "skill": {"warn": 3000},
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_skill_category_ceiling_with_shorthand_is_accepted(self) -> None:
        """Accept a skill category ceiling validated against limits.skill integer shorthand."""
        head_index = {(".agents/skills/helper/SKILL.md", "context-budget"): [3000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "skill": 3000,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_skill_category_ceiling_exceeding_ceiling_is_rejected(self) -> None:
        """Reject a skill category ceiling when limits.skill exceeds the ceiling."""
        head_index = {(".agents/skills/helper/SKILL.md", "context-budget"): [2800]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "skill": 3000,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            "context-budget warn limit for .agents/skills/helper/SKILL.md (3000) exceeds baseline ceiling (2800)",
            diagnostics[0],
        )

    def test_multiple_files_in_same_category_are_validated(self) -> None:
        """Validate multiple files sharing a category against the shared category limit."""
        head_index = {
            (".agents/skills/a/SKILL.md", "context-budget"): [3000],
            (".agents/skills/b/SKILL.md", "context-budget"): [3500],
        }
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "skill": 2800,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_skill_category_whole_file_limit_mapping_is_accepted(self) -> None:
        """Accept a skill category limit using whole-file nested mapping."""
        head_index = {(".agents/skills/helper/SKILL.md", "context-budget"): [3000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "skill": {
                            "file": {"warn": 3000},
                            "description": {"warn": 100},
                        },
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_unknown_category_file_is_reported(self) -> None:
        """Report baseline ceiling whose file path does not belong to a recognized category."""
        head_index = {("unrecognized_file.xyz", "context-budget"): [500]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {},
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            "cannot determine .skillsaw.yaml context-budget category for baseline ceiling unrecognized_file.xyz",
            diagnostics[0],
        )

    def test_none_config_with_context_budget_ceilings_is_rejected(self) -> None:
        """Reject absent .skillsaw.yaml when context-budget ceilings are tracked."""
        head_index = {("AGENTS.md", "context-budget"): [4000]}
        diagnostics = checker.compare_warn_limits(head_index, None)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            ".skillsaw.yaml is missing but context-budget ceilings are tracked",
            diagnostics[0],
        )

    def test_none_config_without_context_budget_ceilings_skips(self) -> None:
        """Skip checking when config is None and no context-budget ceilings exist."""
        head_index = {("docs/architecture.md", "content-section-length"): [500]}
        diagnostics = checker.compare_warn_limits(head_index, None)
        self.assertEqual([], diagnostics)

    def test_non_context_budget_ceilings_are_ignored(self) -> None:
        """Ignore ceiling entries that do not belong to context-budget."""
        head_index = {("docs/architecture.md", "content-section-length"): [500]}
        config = {"rules": {"context-budget": {"limits": {}}}}
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_instruction_category_ceiling_with_mapping_is_accepted(self) -> None:
        """Accept an instruction category ceiling validated against limits.instruction mapping."""
        head_index = {("CLAUDE.md", "context-budget"): [5000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "instruction": {"warn": 5000},
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_instruction_category_ceiling_exceeding_ceiling_is_rejected(self) -> None:
        """Reject an instruction category ceiling when limits.instruction exceeds ceiling."""
        head_index = {("CLAUDE.md", "context-budget"): [4500]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "instruction": 5000,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual(1, len(diagnostics))
        self.assertIn(
            "context-budget warn limit for CLAUDE.md (5000) exceeds baseline ceiling (4500)",
            diagnostics[0],
        )

    def test_memory_category_ceiling_with_mapping_is_accepted(self) -> None:
        """Accept a memory category ceiling validated against limits.memory."""
        head_index = {("MEMORY.md", "context-budget"): [2000]}
        config = {
            "rules": {
                "context-budget": {
                    "limits": {
                        "memory": {"warn": 2000},
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_extra_category_ceiling_with_content_paths_is_accepted(self) -> None:
        """Accept an extra category ceiling matched via .skillsaw.yaml content-paths."""
        head_index = {("docs/vision.md", "context-budget"): [3000]}
        config = {
            "content-paths": ["docs/vision.md"],
            "rules": {
                "context-budget": {
                    "limits": {
                        "extra": {"warn": 3000},
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)

    def test_extra_category_ceiling_with_glob_content_paths_is_accepted(self) -> None:
        """Accept an extra category ceiling matched via .skillsaw.yaml content-paths glob."""
        head_index = {("docs/rfcs/0001.md", "context-budget"): [3000]}
        config = {
            "content-paths": ["docs/rfcs/*.md"],
            "rules": {
                "context-budget": {
                    "limits": {
                        "extra": 3000,
                    }
                }
            }
        }
        diagnostics = checker.compare_warn_limits(head_index, config)
        self.assertEqual([], diagnostics)


class CheckRepoTests(unittest.TestCase):
    def setUp(self) -> None:
        """Create an isolated git repository with a committed baseline."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        self._git("init", "-b", "main")
        self._git("config", "user.email", "ratchet@example.com")
        self._git("config", "user.name", "Ratchet Test")
        self._git("config", "commit.gpgsign", "false")
        self._write_baseline(_baseline(_ceiling(value=4000)))
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            "rules:\n"
            "  context-budget:\n"
            "    limits:\n"
            "      agents-md:\n"
            "        warn: 1000\n",
            encoding="utf-8",
        )
        self._git("add", checker.BASELINE_RELATIVE_PATH)
        self._git("commit", "-m", "base ceiling")

    def _git(self, *args: str) -> subprocess.CompletedProcess[str]:
        """Run a git command in the isolated repository."""
        return subprocess.run(
            ["git", *args],
            cwd=str(self.root),
            capture_output=True,
            text=True,
            check=True,
        )

    def _write_baseline(self, data: dict[str, object]) -> None:
        """Write *data* as the working-tree skillsaw baseline."""
        path = self.root / checker.BASELINE_RELATIVE_PATH
        path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")

    def test_increase_against_base_ref_fails(self) -> None:
        """Fail when the working-tree ceiling exceeds the committed base."""
        self._write_baseline(_baseline(_ceiling(value=4100)))

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(1, exit_code)
        self.assertTrue(messages)
        self.assertIn("increased from 4000 to 4100", messages[0])
        self.assertIn("AGENTS.md", messages[0])

    def test_decrease_against_base_ref_passes(self) -> None:
        """Pass when the working-tree ceiling is lowered."""
        self._write_baseline(_baseline(_ceiling(value=3900)))

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertTrue(messages)
        self.assertIn("none increased", messages[0])

    def test_unchanged_ceiling_passes(self) -> None:
        """Pass when the working-tree ceiling matches the committed base."""
        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        head_sha = checker.git_commit_sha(self.root, "HEAD")
        self.assertIn(
            f"Validated 1 ceiling(s) against {head_sha}:{checker.BASELINE_RELATIVE_PATH}",
            messages[0],
        )

    def test_merge_base_missing_uses_named_ref_in_source_label(self) -> None:
        """Use the named ref as the source label when merge-base returns None."""
        with patch.object(checker, "git_merge_base", return_value=None):
            exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertIn(
            f"Validated 1 ceiling(s) against main:{checker.BASELINE_RELATIVE_PATH}",
            messages[0],
        )

    def test_missing_working_tree_baseline_skips(self) -> None:
        """Skip when the working tree has no baseline file."""
        (self.root / checker.BASELINE_RELATIVE_PATH).unlink()

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertIn("No .skillsaw-baseline.json", messages[0])

    def test_missing_explicit_base_ref_fails(self) -> None:
        """Fail closed when --base-ref does not exist."""
        exit_code, messages = checker.check_repo(self.root, base_ref="does-not-exist")

        self.assertEqual(1, exit_code)
        self.assertIn("not found", messages[0])

    def test_unsafe_explicit_base_ref_fails(self) -> None:
        """Fail closed when --base-ref is not a safe git ref."""
        exit_code, messages = checker.check_repo(self.root, base_ref="main:.git/config")

        self.assertEqual(1, exit_code)
        self.assertIn("invalid --base-ref", messages[0])

    def test_invalid_working_tree_json_fails(self) -> None:
        """Fail when the working-tree baseline is not valid JSON."""
        (self.root / checker.BASELINE_RELATIVE_PATH).write_text("{", encoding="utf-8")

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(1, exit_code)
        self.assertIn("invalid JSON", messages[0])

    def test_missing_base_file_treats_ceilings_as_new(self) -> None:
        """Treat a missing base baseline as empty, allowing new ceilings."""
        self._git("rm", "-f", checker.BASELINE_RELATIVE_PATH)
        self._git("commit", "-m", "remove baseline")
        self._write_baseline(_baseline(_ceiling(value=4100)))

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertIn("none increased", messages[0])

    def test_auto_detect_skips_when_no_candidate_ref_exists(self) -> None:
        """Skip auto-detection when none of the default refs exist."""
        other = self.root / "not-a-default-branch"
        other.mkdir()
        subprocess.run(
            ["git", "init", "-b", "feature"],
            cwd=str(other),
            capture_output=True,
            text=True,
            check=True,
        )
        (other / checker.BASELINE_RELATIVE_PATH).write_text(
            json.dumps(_baseline(_ceiling(value=4100))), encoding="utf-8"
        )

        env = {
            key: value
            for key, value in os.environ.items()
            if key
            not in {
                "BASELINE_RATCHET_BASE_REF",
                "GITHUB_BASE_REF",
                "CI",
                "GITHUB_ACTIONS",
                "GITHUB_REPOSITORY",
            }
        }
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(checker, "fetch_baseline_http", return_value=None),
        ):
            exit_code, messages = checker.check_repo(other)

        self.assertEqual(0, exit_code)
        self.assertIn("baseline ratchet skipped", messages[0])

    def test_auto_detect_rejects_increase_when_head_matches_main_without_http(
        self,
    ) -> None:
        """Reject working-tree ceiling increase when HEAD points to main, without HTTP fallback."""
        self._write_baseline(_baseline(_ceiling(value=4100)))

        with patch.object(
            checker,
            "fetch_baseline_http",
            side_effect=AssertionError("HTTP should not be called"),
        ):
            exit_code, messages = checker.check_repo(self.root)

        self.assertEqual(1, exit_code)
        self.assertTrue(messages)
        self.assertIn("increased from 4000 to 4100", messages[0])
        self.assertIn("AGENTS.md", messages[0])

    def test_http_fallback_rejects_an_increase(self) -> None:
        """Fetch the base baseline over HTTP when no git ref exists."""
        other = self.root / "shallow"
        other.mkdir()
        subprocess.run(
            ["git", "init", "-b", "feature"],
            cwd=str(other),
            capture_output=True,
            text=True,
            check=True,
        )
        (other / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            "rules:\n"
            "  context-budget:\n"
            "    limits:\n"
            "      agents-md:\n"
            "        warn: 1000\n",
            encoding="utf-8",
        )
        (other / checker.BASELINE_RELATIVE_PATH).write_text(
            json.dumps(_baseline(_ceiling(value=4100))) + "\n",
            encoding="utf-8",
        )
        base_raw = json.dumps(_baseline(_ceiling(value=4000)))

        env = {
            key: value
            for key, value in os.environ.items()
            if key not in {"BASELINE_RATCHET_BASE_REF", "GITHUB_BASE_REF"}
        }
        env["GITHUB_REPOSITORY"] = "redhat-et/ProtoBot"
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(checker, "fetch_baseline_http", return_value=base_raw),
        ):
            exit_code, messages = checker.check_repo(other)

        self.assertEqual(1, exit_code)
        self.assertIn("increased from 4000 to 4100", messages[0])

    def test_ci_shallow_checkout_fails_when_fetch_fails(self) -> None:
        """Fail closed in CI when git refs are missing and HTTP fetch fails."""
        other = self.root / "ci_offline"
        other.mkdir()
        subprocess.run(
            ["git", "init", "-b", "feature"],
            cwd=str(other),
            capture_output=True,
            text=True,
            check=True,
        )
        (other / checker.BASELINE_RELATIVE_PATH).write_text(
            json.dumps(_baseline(_ceiling(value=4100))) + "\n",
            encoding="utf-8",
        )

        env = {
            key: value
            for key, value in os.environ.items()
            if key not in {"BASELINE_RATCHET_BASE_REF", "GITHUB_BASE_REF"}
        }
        env["CI"] = "true"
        env["GITHUB_REPOSITORY"] = "redhat-et/ProtoBot"
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(checker, "fetch_baseline_http", return_value=None),
        ):
            exit_code, messages = checker.check_repo(other)

        self.assertEqual(1, exit_code)
        self.assertIn(
            "could not resolve a base-branch .skillsaw-baseline.json in CI",
            messages[0],
        )

    def test_offline_local_skips_when_no_base_source(self) -> None:
        """Skip with warning on offline local runs where no base source exists."""
        other = self.root / "local_offline"
        other.mkdir()
        subprocess.run(
            ["git", "init", "-b", "feature"],
            cwd=str(other),
            capture_output=True,
            text=True,
            check=True,
        )
        (other / checker.BASELINE_RELATIVE_PATH).write_text(
            json.dumps(_baseline(_ceiling(value=4100))) + "\n",
            encoding="utf-8",
        )

        env = {
            key: value
            for key, value in os.environ.items()
            if key
            not in {
                "BASELINE_RATCHET_BASE_REF",
                "GITHUB_BASE_REF",
                "CI",
                "GITHUB_ACTIONS",
                "GITHUB_REPOSITORY",
            }
        }
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(checker, "fetch_baseline_http", return_value=None),
        ):
            exit_code, messages = checker.check_repo(other)

        self.assertEqual(0, exit_code)
        self.assertIn("baseline ratchet skipped", messages[0])

    def test_cli_increase_writes_stderr_and_exits_one(self) -> None:
        """Print ratchet errors on stderr and return exit status 1."""
        self._write_baseline(_baseline(_ceiling(value=4100)))
        stderr = io.StringIO()

        with (
            patch.object(checker, "REPO_ROOT", self.root),
            contextlib.redirect_stderr(stderr),
        ):
            result = checker.main(["--base-ref", "main"])

        self.assertEqual(1, result)
        self.assertIn("increased from 4000 to 4100", stderr.getvalue())

    def test_cli_sanitizes_workflow_commands_in_diagnostics(self) -> None:
        """Verify that main() strips control bytes and :: before printing to stream."""
        malicious_path = "AGENTS.md\n::warning::injected\n"
        self._write_baseline(
            _baseline(_ceiling(file_path=malicious_path, value="invalid"))
        )  # type: ignore[arg-type]
        stderr = io.StringIO()

        with (
            patch.object(checker, "REPO_ROOT", self.root),
            contextlib.redirect_stderr(stderr),
        ):
            result = checker.main(["--base-ref", "main"])

        self.assertEqual(1, result)
        out = stderr.getvalue()
        self.assertEqual(1, out.count("\n"))
        self.assertNotIn("::", out)
        self.assertIn("AGENTS.mdwarninginjected", out)

    def test_merge_base_used_when_base_branch_lowered_ceiling(self) -> None:
        """Use merge-base so stale branches do not fail when base branch lowered a ceiling."""
        self._git("checkout", "-b", "feature")

        self._git("checkout", "main")
        self._write_baseline(_baseline(_ceiling(value=3800)))
        self._git("add", checker.BASELINE_RELATIVE_PATH)
        self._git("commit", "-m", "main lowers ceiling to 3800")

        self._git("checkout", "feature")

        exit_code, messages = checker.check_repo(self.root, base_ref="main")
        self.assertEqual(0, exit_code)
        self.assertTrue(messages)
        self.assertIn("none increased", messages[0])

        self._write_baseline(_baseline(_ceiling(value=4100)))
        exit_code, messages = checker.check_repo(self.root, base_ref="main")
        self.assertEqual(1, exit_code)
        self.assertIn("increased from 4000 to 4100", messages[0])

    def test_shallow_push_checkout_skips_in_ci(self) -> None:
        """Skip baseline ratchet in CI on shallow push checkouts lacking base history."""
        env = {
            "CI": "true",
            "GITHUB_EVENT_NAME": "push",
        }
        with (
            patch.dict(os.environ, env, clear=False),
            patch.object(checker, "is_shallow_repository", return_value=True),
        ):
            exit_code, messages = checker.check_repo(self.root)
            self.assertEqual(0, exit_code)
            self.assertIn("shallow push checkout lacks base history", messages[0])

    def test_warn_limit_exceeding_ceiling_is_rejected(self) -> None:
        """Reject .skillsaw.yaml warn limits that exceed .skillsaw-baseline.json ceilings."""
        skillsaw_config = (
            "rules:\n"
            "  context-budget:\n"
            "    limits:\n"
            "      agents-md:\n"
            "        warn: 4100\n"
        )
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            skillsaw_config, encoding="utf-8"
        )

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(1, exit_code)
        self.assertTrue(
            any("exceeds baseline ceiling" in message for message in messages)
        )

    def test_warn_limit_at_or_below_ceiling_is_accepted(self) -> None:
        """Allow a .skillsaw.yaml warn limit at or below the baseline ceiling."""
        skillsaw_config = (
            "rules:\n"
            "  context-budget:\n"
            "    limits:\n"
            "      agents-md:\n"
            "        warn: 4000\n"
        )
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            skillsaw_config, encoding="utf-8"
        )

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertIn("none increased", messages[0])

    def test_missing_skillsaw_config_with_context_budget_fails_closed(self) -> None:
        """Fail closed when .skillsaw.yaml is absent and context-budget ceilings exist."""
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).unlink()

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(1, exit_code)
        self.assertTrue(
            any(
                ".skillsaw.yaml is missing but context-budget ceilings are tracked"
                in msg
                for msg in messages
            )
        )

    def test_missing_skillsaw_config_without_context_budget_skips_warn_parity(
        self,
    ) -> None:
        """Skip the warn-parity check when .skillsaw.yaml is absent and no context-budget ceilings exist."""
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).unlink()
        self._write_baseline(
            _baseline(_ceiling(file_path="foo.md", rule_id="other-rule", value=100))
        )

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertIn("none increased", messages[0])

    def test_corrupted_skillsaw_config_fails_closed(self) -> None:
        """Reject unparseable or corrupted .skillsaw.yaml rather than silently skipping."""
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            "rules: [invalid: yaml: :\n", encoding="utf-8"
        )
        exit_code, messages = checker.check_repo(self.root, base_ref="main")
        self.assertEqual(1, exit_code)
        self.assertTrue(
            any("invalid YAML in .skillsaw.yaml" in msg for msg in messages)
        )

    def test_non_mapping_skillsaw_config_fails_closed(self) -> None:
        """Reject non-mapping .skillsaw.yaml (e.g. list, string, empty)."""
        for content in ("- list\n- items\n", "just a string\n", ""):
            with self.subTest(content=content):
                (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
                    content, encoding="utf-8"
                )
                exit_code, messages = checker.check_repo(self.root, base_ref="main")
                self.assertEqual(1, exit_code)
                self.assertTrue(
                    any("must be a YAML mapping" in msg for msg in messages)
                )

    def test_skillsaw_config_read_error_fails_closed(self) -> None:
        """Fail closed when .skillsaw.yaml cannot be read."""
        path = self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH
        path.write_text("rules:\n", encoding="utf-8")
        original_read_text = Path.read_text

        def mock_read_text(self_path: Path, *args: object, **kwargs: object) -> str:
            if self_path.name == checker.SKILLSAW_CONFIG_RELATIVE_PATH:
                raise OSError("Disk error")
            return original_read_text(self_path, *args, **kwargs)

        with patch.object(Path, "read_text", side_effect=mock_read_text, autospec=True):
            exit_code, messages = checker.check_repo(self.root, base_ref="main")
            self.assertEqual(1, exit_code)
            self.assertTrue(
                any("cannot read .skillsaw.yaml" in msg for msg in messages)
            )

    def test_skillsaw_config_missing_limit_entry_is_rejected(self) -> None:
        """Reject .skillsaw.yaml missing limits entry for tracked context-budget ceiling."""
        skillsaw_config = (
            "rules:\n"
            "  context-budget:\n"
            "    limits:\n"
            "      other-file-md:\n"
            "        warn: 100\n"
        )
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            skillsaw_config, encoding="utf-8"
        )
        exit_code, messages = checker.check_repo(self.root, base_ref="main")
        self.assertEqual(1, exit_code)
        self.assertTrue(
            any(
                "is missing a rules.context-budget.limits.agents-md entry" in msg
                for msg in messages
            )
        )

    def test_skillsaw_config_with_skill_category_and_integer_shorthand_accepted(
        self,
    ) -> None:
        """Accept .skillsaw.yaml with skill category ceiling and integer shorthand limits."""
        self._write_baseline(
            _baseline(
                _ceiling(file_path="AGENTS.md", value=4000),
                _ceiling(file_path=".agents/skills/helper/SKILL.md", value=3000),
            )
        )
        skillsaw_config = (
            "rules:\n"
            "  context-budget:\n"
            "    limits:\n"
            "      agents-md: 4000\n"
            "      skill: 3000\n"
        )
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            skillsaw_config, encoding="utf-8"
        )
        exit_code, messages = checker.check_repo(self.root, base_ref="main")
        self.assertEqual(0, exit_code)
        self.assertIn("none increased", messages[0])

    def test_skillsaw_config_with_skill_category_exceeding_ceiling_rejected(
        self,
    ) -> None:
        """Reject .skillsaw.yaml when skill category limit exceeds ceiling in check_repo."""
        self._write_baseline(
            _baseline(
                _ceiling(file_path=".agents/skills/helper/SKILL.md", value=2800),
            )
        )
        skillsaw_config = "rules:\n  context-budget:\n    limits:\n      skill: 3000\n"
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            skillsaw_config, encoding="utf-8"
        )
        exit_code, messages = checker.check_repo(self.root, base_ref="main")
        self.assertEqual(1, exit_code)
        self.assertTrue(
            any(
                "context-budget warn limit for .agents/skills/helper/SKILL.md (3000) exceeds baseline ceiling (2800)"
                in msg
                for msg in messages
            )
        )

    def test_skillsaw_config_with_none_intermediates_reports_missing_limit(
        self,
    ) -> None:
        """Report missing limit without error when intermediate config keys map to None."""
        for malformed in (
            "rules:\n",
            "rules:\n  context-budget:\n",
            "rules:\n  context-budget:\n    limits:\n",
        ):
            with self.subTest(config=malformed):
                (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
                    malformed, encoding="utf-8"
                )

                exit_code, messages = checker.check_repo(self.root, base_ref="main")

                self.assertEqual(1, exit_code)
                self.assertTrue(
                    any(
                        "is missing a rules.context-budget.limits.agents-md entry"
                        in msg
                        for msg in messages
                    )
                )

    def test_skillsaw_config_with_none_intermediates_skips_when_no_context_budget_ceilings(
        self,
    ) -> None:
        """Skip warn parity when intermediate config keys map to None and no context-budget ceiling exists."""
        self._write_baseline(
            _baseline(_ceiling(file_path="foo.md", rule_id="other-rule", value=100))
        )
        (self.root / checker.SKILLSAW_CONFIG_RELATIVE_PATH).write_text(
            "rules:\n", encoding="utf-8"
        )

        exit_code, messages = checker.check_repo(self.root, base_ref="main")

        self.assertEqual(0, exit_code)
        self.assertIn("none increased", messages[0])


if __name__ == "__main__":
    unittest.main()
