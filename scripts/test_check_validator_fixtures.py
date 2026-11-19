from __future__ import annotations

import contextlib
import io
import os
import runpy
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

if __package__:
    from scripts import check_validator_fixtures as checker
else:
    import check_validator_fixtures as checker


def find_violations(rel_path: str, code: str) -> list[checker.Violation]:
    """Call checker.find_violations with a conceptual positional signature."""
    return checker.find_violations(rel_path, code)


def is_exempt_path(rel_path: str) -> bool:
    """Call checker.is_exempt_path with a conceptual positional signature."""
    return checker.is_exempt_path(rel_path)


class StripBlockCommentsTests(unittest.TestCase):
    def test_empty_source_returns_empty(self) -> None:
        """Return an empty string when there is no source to scan."""
        self.assertEqual("", checker.strip_block_comments(""))

    def test_block_comment_is_blanked(self) -> None:
        """Replace a closed block comment with spaces and keep newlines."""
        source = "package p\n/* records.Requirement{} */\nvar x = 1\n"
        stripped = checker.strip_block_comments(source)
        self.assertNotIn("records.Requirement{}", stripped)
        self.assertEqual(source.count("\n"), stripped.count("\n"))


class IsExemptPathTests(unittest.TestCase):
    def test_exempts_testutil_package_files(self) -> None:
        """Treat every file under validator/internal/testutil/ as exempt."""
        self.assertTrue(is_exempt_path("validator/internal/testutil/fixtures.go"))
        self.assertTrue(is_exempt_path("validator/internal/testutil/fixtures_test.go"))

    def test_exempts_records_package_files(self) -> None:
        """Treat files under validator/internal/records/ as exempt."""
        self.assertTrue(is_exempt_path("validator/internal/records/records_test.go"))

    def test_exempts_layout_package_files(self) -> None:
        """Treat files under validator/internal/layout/ as exempt."""
        self.assertTrue(is_exempt_path("validator/internal/layout/reader_test.go"))

    def test_does_not_exempt_sibling_packages(self) -> None:
        """Leave non-testutil packages subject to the check."""
        self.assertFalse(
            is_exempt_path("validator/internal/specvalidation/schema_test.go")
        )

    def test_windows_style_separators_are_normalized(self) -> None:
        """Normalize backslash separators before matching the prefix."""
        self.assertTrue(is_exempt_path("validator\\internal\\testutil\\fixtures.go"))
        self.assertTrue(is_exempt_path("validator\\internal\\records\\records_test.go"))
        self.assertTrue(is_exempt_path("validator\\internal\\layout\\reader_test.go"))


class FindViolationsTests(unittest.TestCase):
    def test_detects_prohibited_struct_literals(self) -> None:
        """Verify scanner detects direct struct literal construction in test files."""
        code = """
    package specvalidation_test
    func TestExample(t *testing.T) {
        req := records.Requirement{ID: "REQ-TEST-00001"}
        iface := records.InterfaceRecord{Name: "test-api"}
    }
    """
        violations = find_violations(
            "validator/internal/specvalidation/example_test.go", code
        )
        self.assertEqual(2, len(violations))
        self.assertIn("validator/internal/testutil", violations[0].remediation)
        self.assertEqual("Requirement", violations[0].type_name)
        self.assertEqual("InterfaceRecord", violations[1].type_name)

    def test_detects_synthetic_document_literals(self) -> None:
        """Verify synthetic layout.Document literals not using testutil builders are flagged."""
        code = """
    doc := layout.Document[records.Requirement]{
        Path: "spec/requirements/REQ-00001.yaml",
        Record: req,
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)

    def test_slice_of_builder_calls_is_not_flagged(self) -> None:
        """A slice-typed Document literal wrapping builder calls is not synthetic."""
        code = """
    reqs := []layout.Document[records.Requirement]{testutil.ValidRequirementDocument()}
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual([], violations)

    def test_slice_element_literal_is_flagged_single_line(self) -> None:
        """A slice-typed Document literal containing bare element literals is flagged."""
        code = """
    reqs := []layout.Document[records.Requirement]{{
        Path: "spec/requirements/REQ-00001.yaml",
        Record: req,
    }}
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)
        self.assertEqual("Requirement", violations[0].type_name)

    def test_slice_element_literal_is_flagged_multiline(self) -> None:
        """A multiline slice-typed Document literal with bare element literals is flagged."""
        code = """
    reqs := []layout.Document[records.Requirement]{
        {
            Path: "spec/requirements/REQ-00001.yaml",
            Record: req,
        },
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)
        self.assertEqual("Requirement", violations[0].type_name)

    def test_slice_element_literal_interface_is_flagged(self) -> None:
        """A slice-typed Document literal for InterfaceRecord with bare element literals is flagged."""
        code = """
    ifaces := []layout.Document[records.InterfaceRecord]{{
        Path: "spec/interfaces/api.yaml",
        Record: iface,
    }}
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)
        self.assertEqual("InterfaceRecord", violations[0].type_name)

    def test_slice_element_annotated_with_nolint_is_exempt(self) -> None:
        """A slice-typed Document literal with nolint annotation on the slice is exempt."""
        code = """
    reqs := []layout.Document[records.Requirement]{ // nolint:fixtures
        {
            Path: "spec/requirements/REQ-00001.yaml",
            Record: req,
        },
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual([], violations)

    def test_slice_element_literal_after_builder_is_flagged(self) -> None:
        """A bare literal appearing after a builder call in the same slice is flagged."""
        code = """
    reqs := []layout.Document[records.Requirement]{
        testutil.ValidRequirementDocument(),
        {
            Path: "spec/requirements/REQ-00001.yaml",
            Record: req,
        },
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)
        self.assertEqual("Requirement", violations[0].type_name)

    def test_slice_element_literal_after_builder_single_line_is_flagged(self) -> None:
        """A bare literal appearing after a builder call on the same line is flagged."""
        code = """
    reqs := []layout.Document[records.Requirement]{ testutil.ValidRequirementDocument(), {Path: "spec/requirements/REQ-00001.yaml", Record: req} }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)
        self.assertEqual("Requirement", violations[0].type_name)

    def test_slice_element_annotated_on_inner_line_is_exempt(self) -> None:
        """A slice element with nolint on its own opening line is exempt."""
        code = """
    reqs := []layout.Document[records.Requirement]{
        { // nolint:fixtures
            Path: "spec/requirements/REQ-00001.yaml",
            Record: req,
        },
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual([], violations)

    def test_slice_element_annotated_above_inner_line_is_exempt(self) -> None:
        """A slice element with nolint directly above its opening line is exempt."""
        code = """
    reqs := []layout.Document[records.Requirement]{
        // nolint:fixtures
        {
            Path: "spec/requirements/REQ-00001.yaml",
            Record: req,
        },
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual([], violations)

    def test_slice_mixed_elements_one_exempt_one_flagged(self) -> None:
        """A slice with one exempt element and one unexempt bare literal is flagged."""
        code = """
    reqs := []layout.Document[records.Requirement]{
        { // nolint:fixtures
            Path: "spec/requirements/REQ-00001.yaml",
            Record: req1,
        },
        {
            Path: "spec/requirements/REQ-00002.yaml",
            Record: req2,
        },
    }
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))
        self.assertEqual("document_literal", violations[0].kind)
        self.assertEqual("Requirement", violations[0].type_name)

    def test_exempts_testutil_package_and_nolint_comments(self) -> None:
        """Verify builder package and explicit nolint annotations are exempted."""
        annotated = """
    // nolint:fixtures
    req := records.Requirement{}
    """
        self.assertEqual(
            [],
            find_violations(
                "validator/internal/specvalidation/edge_test.go", annotated
            ),
        )
        self.assertTrue(is_exempt_path("validator/internal/testutil/fixtures.go"))

    def test_prose_containing_nolint_on_previous_line_does_not_exempt(self) -> None:
        """Prose mentioning nolint:fixtures on the preceding line does not exempt the literal."""
        code = """
    // do not write nolint:fixtures here
    req := records.Requirement{}
    """
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))

    def test_trailing_nolint_comment_exempts_the_line(self) -> None:
        """A trailing nolint marker on the violating line is also honored."""
        code = "req := records.Requirement{} // nolint:fixtures\n"
        violations = find_violations(
            "validator/internal/specvalidation/edge_test.go", code
        )
        self.assertEqual([], violations)

    def test_inline_block_comment_nolint_exempts_the_line(self) -> None:
        """An inline block-comment nolint marker on the violating line is honored."""
        code = "req := records.Requirement{} /* nolint:fixtures */\n"
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual([], violations)

    def test_trailing_prose_comment_does_not_exempt(self) -> None:
        """Trailing prose mentioning nolint:fixtures does not exempt the literal."""
        code = "req := records.Requirement{} // do not write nolint:fixtures here\n"
        violations = find_violations("validator/internal/example_test.go", code)
        self.assertEqual(1, len(violations))

    def test_exempts_records_and_layout_packages(self) -> None:
        """Package records and package layout tests are exempt from the check."""
        code = "req := records.Requirement{}\n"
        self.assertEqual(
            [],
            find_violations("validator/internal/records/records_test.go", code),
        )
        self.assertEqual(
            [],
            find_violations("validator/internal/layout/reader_test.go", code),
        )

    def test_passes_conforming_builder_usage(self) -> None:
        """Verify calls to testutil.ValidRequirementDocument pass without violations."""
        conforming = """
    doc := testutil.ValidRequirementDocument(testutil.WithID("REQ-CORE-00001"))
    """
        violations = find_violations(
            "validator/internal/specvalidation/valid_test.go", conforming
        )
        self.assertEqual([], violations)

    def test_non_test_file_is_not_scanned(self) -> None:
        """A production (non ``_test.go``) file is out of scope for this check."""
        code = 'req := records.Requirement{ID: "REQ-TEST-00001"}\n'
        violations = find_violations(
            "validator/internal/specvalidation/helper.go", code
        )
        self.assertEqual([], violations)

    def test_commented_out_literal_is_ignored(self) -> None:
        """A literal inside a line comment is not a live construction."""
        code = '// req := records.Requirement{ID: "REQ-TEST-00001"}\n'
        violations = find_violations(
            "validator/internal/specvalidation/edge_test.go", code
        )
        self.assertEqual([], violations)

    def test_block_commented_literal_is_ignored(self) -> None:
        """A literal inside a block comment is not a live construction."""
        code = '/* req := records.Requirement{ID: "REQ-TEST-00001"} */\n'
        violations = find_violations(
            "validator/internal/specvalidation/edge_test.go", code
        )
        self.assertEqual([], violations)

    def test_line_number_and_snippet_are_reported(self) -> None:
        """Report the 1-indexed line and a trimmed snippet for each violation."""
        code = '\n\nreq := records.Requirement{ID: "REQ-TEST-00001"}\n'
        violations = find_violations(
            "validator/internal/specvalidation/edge_test.go", code
        )
        self.assertEqual(1, len(violations))
        self.assertEqual(3, violations[0].line)
        self.assertIn("records.Requirement{", violations[0].snippet)


class RelativePosixTests(unittest.TestCase):
    def test_relative_path_uses_posix(self) -> None:
        """Return a posix-style path relative to the repository root."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "validator" / "internal" / "x_test.go"
            path.parent.mkdir(parents=True)
            path.write_text("package x\n", encoding="utf-8")
            self.assertEqual(
                "validator/internal/x_test.go",
                checker._relative_posix(path, root),
            )

    def test_outside_path_falls_back_to_as_posix(self) -> None:
        """Fall back to the path's own posix form outside the repo root."""
        with (
            tempfile.TemporaryDirectory() as tmp_a,
            tempfile.TemporaryDirectory() as tmp_b,
        ):
            outside = Path(tmp_a) / "x_test.go"
            outside.write_text("package x\n", encoding="utf-8")
            got = checker._relative_posix(outside, Path(tmp_b))
            self.assertEqual(outside.as_posix(), got)


class ReadTextTests(unittest.TestCase):
    def test_oserror_is_wrapped(self) -> None:
        """Wrap an unreadable path in ValidatorFixturesError."""
        with self.assertRaises(checker.ValidatorFixturesError) as raised:
            checker._read_text(Path("/nonexistent/x_test.go"))
        self.assertIn("cannot read file", str(raised.exception))


class IterTestSourcesTests(unittest.TestCase):
    def test_finds_only_test_files(self) -> None:
        """Return only ``*_test.go`` files, skipping production sources."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            keep = root / "pkg" / "a_test.go"
            keep.parent.mkdir(parents=True)
            keep.write_text("package pkg\n", encoding="utf-8")
            (root / "pkg" / "a.go").write_text("package pkg\n", encoding="utf-8")
            got = checker._iter_test_sources(root)
            self.assertEqual([keep], got)

    def test_is_file_oserror_is_skipped(self) -> None:
        """Skip a path that raises OSError from is_file."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "a_test.go"
            path.write_text("package pkg\n", encoding="utf-8")
            real_is_file = Path.is_file

            def is_file(self: Path) -> bool:
                if self.name == "a_test.go":
                    raise OSError("boom")
                return real_is_file(self)

            with patch.object(Path, "is_file", is_file):
                self.assertEqual([], checker._iter_test_sources(root))


class CollectViolationsTests(unittest.TestCase):
    def test_missing_internal_directory_is_an_error(self) -> None:
        """Fail closed when validator/internal does not exist."""
        with tempfile.TemporaryDirectory() as tmp:
            violations, errors = checker.collect_violations(Path(tmp))
        self.assertEqual([], violations)
        self.assertTrue(errors)
        self.assertIn("missing or not a directory", errors[0])

    def test_is_dir_oserror_is_an_error(self) -> None:
        """Fail closed when stat-ing validator/internal raises OSError."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            real_is_dir = Path.is_dir

            def is_dir(self: Path) -> bool:
                if self.as_posix().endswith("validator/internal"):
                    raise PermissionError("denied")
                return real_is_dir(self)

            with patch.object(Path, "is_dir", is_dir):
                violations, errors = checker.collect_violations(root)
        self.assertEqual([], violations)
        self.assertIn("PermissionError", errors[0])

    def test_skips_testutil_directory(self) -> None:
        """Never scan files under validator/internal/testutil/."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            testutil_dir = root / "validator" / "internal" / "testutil"
            testutil_dir.mkdir(parents=True)
            (testutil_dir / "fixtures_test.go").write_text(
                'package testutil\nvar _ = records.Requirement{ID: "x"}\n',
                encoding="utf-8",
            )
            violations, errors = checker.collect_violations(root)
        self.assertEqual([], violations)
        self.assertEqual([], errors)

    def test_skips_records_and_layout_directories(self) -> None:
        """Never scan files under validator/internal/records/ or layout/."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for pkg_name in ("records", "layout"):
                pkg_dir = root / "validator" / "internal" / pkg_name
                pkg_dir.mkdir(parents=True)
                (pkg_dir / "pkg_test.go").write_text(
                    'package pkg\nvar _ = records.Requirement{ID: "x"}\n',
                    encoding="utf-8",
                )
            violations, errors = checker.collect_violations(root)
        self.assertEqual([], violations)
        self.assertEqual([], errors)

    def test_finds_violation_in_scanned_tree(self) -> None:
        """Detect a bare literal in a non-exempt test file under the tree."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pkg = root / "validator" / "internal" / "specvalidation"
            pkg.mkdir(parents=True)
            (pkg / "example_test.go").write_text(
                'package specvalidation_test\nvar _ = records.Requirement{ID: "x"}\n',
                encoding="utf-8",
            )
            violations, errors = checker.collect_violations(root)
        self.assertEqual([], errors)
        self.assertEqual(1, len(violations))
        self.assertEqual(
            "validator/internal/specvalidation/example_test.go",
            violations[0].rel_path,
        )

    def test_unreadable_test_file_is_recorded(self) -> None:
        """Record a read failure and continue with other files."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pkg = root / "validator" / "internal" / "specvalidation"
            pkg.mkdir(parents=True)
            (pkg / "example_test.go").write_text(
                "package specvalidation_test\n", encoding="utf-8"
            )
            with patch.object(
                checker,
                "_read_text",
                side_effect=checker.ValidatorFixturesError("read failed"),
            ):
                violations, errors = checker.collect_violations(root)
        self.assertEqual([], violations)
        self.assertEqual(["read failed"], errors)


class FormatViolationsTests(unittest.TestCase):
    def test_includes_remediation_and_location(self) -> None:
        """Format a violation with its path, line, and remediation."""
        violation = checker.Violation(
            rel_path="validator/internal/specvalidation/x_test.go",
            line=5,
            kind="record_literal",
            type_name="Requirement",
            snippet='req := records.Requirement{ID: "x"}',
            remediation="use validator/internal/testutil.ValidRequirementDocument",
        )
        lines = checker.format_violations([violation], ["boom"])
        self.assertEqual(2, len(lines))
        self.assertIn("ERROR: boom", lines[0])
        self.assertIn("x_test.go:5", lines[1])
        self.assertIn("validator/internal/testutil.ValidRequirementDocument", lines[1])


class CheckAndMainTests(unittest.TestCase):
    def test_passes_on_current_repository(self) -> None:
        """Verify that the live repository has no prohibited literals."""
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            rc = checker.check(checker.REPO_ROOT)
        self.assertEqual(0, rc)
        self.assertIn("No prohibited", stdout.getvalue())

    def test_check_fails_when_literal_is_introduced(self) -> None:
        """Detect a newly introduced literal under a temporary repo root."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pkg = root / "validator" / "internal" / "specvalidation"
            pkg.mkdir(parents=True)
            (pkg / "example_test.go").write_text(
                'package specvalidation_test\nvar _ = records.Requirement{ID: "x"}\n',
                encoding="utf-8",
            )
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc = checker.check(root)
        self.assertEqual(1, rc)
        self.assertIn("prohibited", stderr.getvalue())

    def test_cli_repo_root_success_and_failure(self) -> None:
        """Honor --repo-root for both a clean tree and a violating tree."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pkg = root / "validator" / "internal" / "specvalidation"
            pkg.mkdir(parents=True)
            (pkg / "clean_test.go").write_text(
                "package specvalidation_test\n", encoding="utf-8"
            )
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                self.assertEqual(0, checker.main(["--repo-root", str(root)]))

            (pkg / "dirty_test.go").write_text(
                'package specvalidation_test\nvar _ = records.Requirement{ID: "x"}\n',
                encoding="utf-8",
            )
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                self.assertEqual(1, checker.main(["--repo-root", str(root)]))

    def test_main_defaults_to_repository_root(self) -> None:
        """Check the live repository when --repo-root is omitted."""
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            self.assertEqual(0, checker.main([]))

    def test_main_accepts_and_ignores_positional_files(self) -> None:
        """Positional file arguments are accepted and ignored for CLI compatibility."""
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            rc = checker.main(["validator/internal/specvalidation/references_test.go"])
        self.assertEqual(0, rc)
        self.assertIn("No prohibited", stdout.getvalue())

    def test_help_describes_positional_files(self) -> None:
        """Check that --help output mentions accepted positional files."""
        stdout = io.StringIO()
        with (
            contextlib.redirect_stdout(stdout),
            self.assertRaises(SystemExit) as raised,
        ):
            checker.main(["--help"])
        self.assertEqual(0, raised.exception.code)
        self.assertIn("positional file arguments", stdout.getvalue())

    def test_module_entrypoint_invokes_main(self) -> None:
        """Running the script as a program exits 0 on the live repository."""
        script = Path(checker.__file__).resolve()
        result = subprocess.run(
            [sys.executable, str(script)],
            capture_output=True,
            text=True,
            cwd=str(checker.REPO_ROOT),
            check=False,
        )
        self.assertEqual(0, result.returncode)
        self.assertIn("No prohibited", result.stdout)

    def test_name_main_guard_calls_main(self) -> None:
        """Execute the module as __main__ in-process so the guard is covered."""
        script = Path(checker.__file__).resolve()
        stdout = io.StringIO()
        with (
            patch.object(sys, "argv", [str(script)]),
            contextlib.redirect_stdout(stdout),
            self.assertRaises(SystemExit) as raised,
        ):
            runpy.run_path(str(script), run_name="__main__")
        self.assertEqual(0, raised.exception.code)
        self.assertIn("No prohibited", stdout.getvalue())


class UnreadableFileTests(unittest.TestCase):
    @unittest.skipIf(
        hasattr(os, "geteuid") and os.geteuid() == 0,
        "root ignores file mode 000 permissions",
    )
    def test_chmod_zero_test_file_is_a_read_error(self) -> None:
        """Treat a permission-denied test file as a documented read error."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pkg = root / "validator" / "internal" / "specvalidation"
            pkg.mkdir(parents=True)
            path = pkg / "example_test.go"
            path.write_text("package specvalidation_test\n", encoding="utf-8")
            mode = path.stat().st_mode
            os.chmod(path, 0)
            try:
                violations, errors = checker.collect_violations(root)
            finally:
                os.chmod(path, mode | stat.S_IRUSR)
            self.assertTrue(errors)
            self.assertEqual([], violations)


if __name__ == "__main__":
    unittest.main()
