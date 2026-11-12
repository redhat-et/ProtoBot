from __future__ import annotations

import contextlib
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
        self.assertIn("Validated 1 ceiling(s) against main:", messages[0])

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


if __name__ == "__main__":
    unittest.main()
