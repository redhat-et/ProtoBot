from __future__ import annotations

import contextlib
import hashlib
import io
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
from urllib.error import URLError

if __package__:
    from scripts import check_harness_integrity as checker
else:
    import check_harness_integrity as checker


class _TestResponse(io.BytesIO):
    """A byte response with a configurable final URL for isolated tests."""

    def __init__(self, content: bytes, final_url: object) -> None:
        """Initialize response bytes and the final URL reported to callers."""
        super().__init__(content)
        self.final_url = final_url

    def geturl(self) -> object:
        """Return the test response's configured final URL value."""
        return self.final_url


class HarnessIntegrityTests(unittest.TestCase):
    def setUp(self) -> None:
        """Create an isolated harness directory for each test."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)
        self.harness_dir = self.root / ".fullsend" / "harness"
        self.harness_dir.mkdir(parents=True)
        self.path = self.harness_dir / "review.yaml"

    def write_harness(self, content: str) -> Path:
        """Write one harness entry and return its path."""
        self.path.write_text(content, encoding="utf-8")
        return self.path

    def test_valid_base_entry_is_accepted_offline(self) -> None:
        """Accept a pinned base without making a network request."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'---\nbase: "{base}" \t\n\ntimeout_minutes: 5\n')

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            self.assertEqual([], checker.check_harness_file(path))

        build_opener.assert_not_called()

    def test_prerelease_and_build_pin_is_accepted_by_parser(self) -> None:
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/"
            "v1.2.3-rc.1+build.2/harness/review.yaml#sha256=" + "a" * 64
        )

        pinned = checker.parse_pinned_base(base)

        self.assertEqual(base.rsplit("#", maxsplit=1)[0], pinned.fetch_url)
        self.assertEqual("a" * 64, pinned.expected_sha256)

    def test_harness_read_failure_reports_exception_type_only(self) -> None:
        """Distinguish local read failures without exposing their details."""
        errors = (
            FileNotFoundError("private missing path"),
            PermissionError("private permission detail"),
            UnicodeDecodeError("utf-8", b"\xff", 0, 1, "private decode detail"),
        )

        for error in errors:
            with self.subTest(error_type=type(error).__name__):
                with patch.object(Path, "read_text", side_effect=error):
                    diagnostic = checker.check_harness_file(self.path)[0]

                self.assertTrue(
                    diagnostic.endswith(
                        f"cannot read harness YAML ({type(error).__name__})"
                    )
                )
                self.assertNotIn(str(error), diagnostic)

    def test_invalid_url_and_unsafe_path_are_rejected(self) -> None:
        """Reject non-pinned URLs and unsafe repository paths."""
        invalid_urls = (
            "http://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64,
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/../review.yaml#sha256=" + "a" * 64,
            "https://example.com/fullsend-ai/agents/v1.2.3/review.yaml#sha256="
            + "a" * 64,
            (
                "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
                "harness/review.yaml#sha256=bad-hash"
            ),
        )
        for url in invalid_urls:
            with self.subTest(url=url):
                path = self.write_harness(f'base: "{url}"\n')
                diagnostic = checker.check_harness_file(path)[0]
                self.assertIn("review.yaml", diagnostic)
                self.assertIn("base", diagnostic)

    def test_noncanonical_base_key_and_value_quoting_are_rejected(self) -> None:
        """Reject base entries that do not use canonical YAML quoting."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        entries = (
            f"base: {base}\n",
            f"base: '{base}'\n",
            f'"base": "{base}"\n',
            f"'base': \"{base}\"\n",
        )
        for entry in entries:
            with self.subTest(entry=entry):
                path = self.write_harness(entry)
                self.assertTrue(checker.check_harness_file(path))

    def test_trailing_comment_after_base_scalar_is_rejected(self) -> None:
        """Reject comments after the quoted base scalar."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}" # pinned upstream file\n')

        diagnostic = checker.check_harness_file(path)[0]

        self.assertIn("base value must be one YAML scalar", diagnostic)

    def test_uppercase_sha256_is_rejected(self) -> None:
        """Reject uppercase characters in the SHA-256 fragment."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "A" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')

        diagnostic = checker.check_harness_file(path)[0]

        self.assertIn("base must be", diagnostic)

    def test_missing_base_is_rejected(self) -> None:
        """Require an explicit top-level base entry."""
        path = self.write_harness(
            "# base: this comment does not count\n"
            "env:\n"
            "  base: this nested value does not count\n"
        )

        diagnostic = checker.check_harness_file(path)[0]

        self.assertIn("review.yaml", diagnostic)
        self.assertIn("missing top-level base entry", diagnostic)

    def test_duplicate_base_entries_are_rejected(self) -> None:
        """Reject duplicate top-level base entries with line numbers."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\nbase: "{base}"\n')

        diagnostic = checker.check_harness_file(path)[0]

        self.assertIn("duplicate top-level base entries", diagnostic)
        self.assertIn("lines 1, 2", diagnostic)

    def test_malformed_base_scalar_is_rejected(self) -> None:
        """Reject trailing text after the base scalar."""
        path = self.write_harness('base: "not a URL" trailing text\n')

        diagnostic = checker.check_harness_file(path)[0]

        self.assertIn("review.yaml", diagnostic)
        self.assertIn("base value must be one YAML scalar", diagnostic)

    def test_online_hash_matches_exact_response_bytes(self) -> None:
        """Verify exact bytes from the pinned final response URL."""
        response_bytes = b"line one\r\n\x00line two"
        digest = hashlib.sha256(response_bytes).hexdigest()
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + digest
        )
        path = self.write_harness(f'base: "{base}"\n')
        fetch_url = base.rsplit("#", maxsplit=1)[0]

        with patch.object(
            checker.urllib.request,
            "build_opener",
        ) as build_opener:
            opener = build_opener.return_value
            opener.open.return_value = _TestResponse(response_bytes, fetch_url)
            diagnostics = checker.check_harness_file(path, online=True)

        self.assertEqual([], diagnostics)
        build_opener.assert_called_once()
        self.assertIsInstance(
            build_opener.call_args.args[0], checker._NoRedirectHandler
        )
        opener.open.assert_called_once_with(
            fetch_url,
            timeout=checker.NETWORK_TIMEOUT_SECONDS,
        )

    def test_online_hash_mismatch_fails(self) -> None:
        """Report when fetched bytes do not match the pinned digest."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "0" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')
        fetch_url = base.rsplit("#", maxsplit=1)[0]

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            build_opener.return_value.open.return_value = _TestResponse(
                b"different content",
                fetch_url,
            )
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("review.yaml", diagnostic)
        self.assertIn("SHA-256 mismatch", diagnostic)

    def test_online_fetch_failure_fails(self) -> None:
        """Report fetch failures without exposing underlying URLs."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')
        raw_error = "network unavailable at https://example.com/private-target"

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            build_opener.return_value.open.side_effect = URLError(raw_error)
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("review.yaml", diagnostic)
        self.assertIn("failed to fetch", diagnostic)
        self.assertNotIn(raw_error, diagnostic)
        self.assertNotIn("https://example.com", diagnostic)

    def test_online_response_size_limit_fails(self) -> None:
        """Reject response bodies larger than the configured size limit."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')

        with (
            patch.object(checker, "MAX_RESPONSE_BYTES", 8),
            patch.object(checker, "RESPONSE_READ_CHUNK_BYTES", 4),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
        ):
            build_opener.return_value.open.return_value = _TestResponse(
                b"x" * 9,
                base.rsplit("#", maxsplit=1)[0],
            )
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("exceeds the 8-byte limit", diagnostic)

    def test_redirect_handler_rejects_before_following_target(self) -> None:
        """Reject redirects before urllib can issue a request to their target."""
        source_url = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml"
        )
        target_url = "https://example.com/private-redirect-target"
        handler = checker._NoRedirectHandler()
        parent = checker.urllib.request.OpenerDirector()
        handler.parent = parent
        request = checker.urllib.request.Request(source_url)

        with (
            patch.object(parent, "open") as target_request,
            self.assertRaises(checker._UnexpectedRedirectError) as error,
        ):
            handler.http_error_302(
                request,
                io.BytesIO(),
                302,
                "Found",
                {"location": target_url},
            )

        target_request.assert_not_called()
        self.assertNotIn(target_url, str(error.exception))
        self.assertEqual(
            "redirect rejected while fetching pinned base URL",
            str(error.exception),
        )

    def test_online_redirect_failure_uses_static_diagnostic(self) -> None:
        """Report redirect failures with their static sanitized message."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            build_opener.return_value.open.side_effect = (
                checker._UnexpectedRedirectError(
                    "redirect rejected while fetching pinned base URL"
                )
            )
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("redirect rejected while fetching pinned base URL", diagnostic)

    def test_online_final_response_url_mismatch_fails(self) -> None:
        """Reject and safely report a response from a different URL."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')
        redirected_url = "https://example.com/private-redirect-target"

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            build_opener.return_value.open.return_value = _TestResponse(
                b"pinned bytes",
                redirected_url,
            )
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn(
            "final response URL is missing or differs from pinned base URL",
            diagnostic,
        )
        self.assertIn("review.yaml", diagnostic)
        self.assertNotIn(redirected_url, diagnostic)

    def test_online_missing_final_response_url_fails(self) -> None:
        """Reject responses that do not expose a final URL."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            build_opener.return_value.open.return_value = io.BytesIO(b"pinned bytes")
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("final response URL", diagnostic)
        self.assertIn("review.yaml", diagnostic)

    def test_online_non_string_final_response_url_fails(self) -> None:
        """Reject non-string final URLs even when a URL attribute is present."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        fetch_url = base.rsplit("#", maxsplit=1)[0]
        path = self.write_harness(f'base: "{base}"\n')
        response = _TestResponse(b"pinned bytes", object())
        response.url = fetch_url

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            build_opener.return_value.open.return_value = response
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("final response URL", diagnostic)
        self.assertIn("review.yaml", diagnostic)

    def test_online_response_read_deadline_fails(self) -> None:
        """Fail when reading the response exceeds the total deadline."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        path = self.write_harness(f'base: "{base}"\n')
        current_time = [0.0]

        class SlowResponse(_TestResponse):
            def read1(self, size: int = -1) -> bytes:
                """Advance simulated time so the total-read deadline expires."""
                current_time[0] += checker.NETWORK_READ_DEADLINE_SECONDS + 1
                return super().read1(size)

        with (
            patch.object(checker, "monotonic", side_effect=lambda: current_time[0]),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
        ):
            build_opener.return_value.open.return_value = SlowResponse(
                b"pinned bytes",
                base.rsplit("#", maxsplit=1)[0],
            )
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("response read exceeded", diagnostic)

    def test_malformed_url_is_rejected_before_online_fetch(self) -> None:
        """Reject malformed base URLs before opening a network connection."""
        path = self.write_harness(
            'base: "https://example.com/not-a-pinned-harness.yaml"\n'
        )

        with patch.object(checker.urllib.request, "build_opener") as build_opener:
            diagnostic = checker.check_harness_file(path, online=True)[0]

        self.assertIn("base must be", diagnostic)
        build_opener.assert_not_called()

    def test_cli_scans_yaml_and_yml_files_and_defaults_to_offline(self) -> None:
        """Validate discovered YAML and YML files offline by default."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        self.write_harness(f'base: "{base}"\n')
        (self.harness_dir / "included.yml").write_text(
            f'base: "{base}"\n', encoding="utf-8"
        )

        stdout = io.StringIO()
        with (
            patch.object(checker, "REPO_ROOT", self.root),
            patch.dict(os.environ, {"CI": "true"}),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
            contextlib.redirect_stdout(stdout),
        ):
            result = checker.main([])

        self.assertEqual(0, result)
        self.assertIn("Validated 2 harness file(s)", stdout.getvalue())
        self.assertIn("offline structure", stdout.getvalue())
        build_opener.assert_not_called()

    def test_cli_online_if_ci_stays_offline_when_ci_is_false(self) -> None:
        """Keep CI-only verification offline unless CI is exactly true."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        self.write_harness(f'base: "{base}"\n')
        stdout = io.StringIO()

        with (
            patch.object(checker, "REPO_ROOT", self.root),
            patch.dict(os.environ, {"CI": "false"}),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
            contextlib.redirect_stdout(stdout),
        ):
            result = checker.main(["--online-if-ci"])

        self.assertEqual(0, result)
        self.assertIn("offline structure", stdout.getvalue())
        build_opener.assert_not_called()

    def test_cli_online_if_ci_stays_offline_when_ci_is_unset(self) -> None:
        """Keep CI-only verification offline when CI is not defined."""
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + "a" * 64
        )
        self.write_harness(f'base: "{base}"\n')
        stdout = io.StringIO()

        with (
            patch.object(checker, "REPO_ROOT", self.root),
            patch.dict(os.environ),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
            contextlib.redirect_stdout(stdout),
        ):
            os.environ.pop("CI", None)
            self.assertNotIn("CI", os.environ)
            result = checker.main(["--online-if-ci"])

        self.assertEqual(0, result)
        self.assertIn("offline structure", stdout.getvalue())
        build_opener.assert_not_called()

    def test_cli_online_if_ci_verifies_hash_when_ci_is_true(self) -> None:
        """Verify pinned bytes for a case-insensitive CI=true."""
        response_bytes = b"pinned bytes"
        digest = hashlib.sha256(response_bytes).hexdigest()
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + digest
        )
        self.write_harness(f'base: "{base}"\n')
        fetch_url = base.rsplit("#", maxsplit=1)[0]
        stdout = io.StringIO()

        with (
            patch.object(checker, "REPO_ROOT", self.root),
            patch.dict(os.environ, {"CI": "TrUe"}),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
            contextlib.redirect_stdout(stdout),
        ):
            opener = build_opener.return_value
            opener.open.return_value = _TestResponse(response_bytes, fetch_url)
            result = checker.main(["--online-if-ci"])

        self.assertEqual(0, result)
        self.assertIn("online integrity", stdout.getvalue())
        build_opener.assert_called_once()
        self.assertIsInstance(
            build_opener.call_args.args[0], checker._NoRedirectHandler
        )
        opener.open.assert_called_once_with(
            fetch_url,
            timeout=checker.NETWORK_TIMEOUT_SECONDS,
        )

    def test_cli_online_fetches_discovered_harness_files(self) -> None:
        """Fetch discovered harness files when online mode is requested."""
        response_bytes = b"pinned bytes"
        digest = hashlib.sha256(response_bytes).hexdigest()
        base = (
            "https://raw.githubusercontent.com/fullsend-ai/agents/v1.2.3/"
            "harness/review.yaml#sha256=" + digest
        )
        self.write_harness(f'base: "{base}"\n')
        fetch_url = base.rsplit("#", maxsplit=1)[0]
        stdout = io.StringIO()

        with (
            patch.object(checker, "REPO_ROOT", self.root),
            patch.dict(os.environ, {"CI": "false"}),
            patch.object(checker.urllib.request, "build_opener") as build_opener,
            contextlib.redirect_stdout(stdout),
        ):
            opener = build_opener.return_value
            opener.open.return_value = _TestResponse(response_bytes, fetch_url)
            result = checker.main(["--online"])

        self.assertEqual(0, result)
        self.assertIn("online integrity", stdout.getvalue())
        opener.open.assert_called_once_with(
            fetch_url,
            timeout=checker.NETWORK_TIMEOUT_SECONDS,
        )


if __name__ == "__main__":
    unittest.main()
