from __future__ import annotations

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

if __package__:
    from scripts import check_portable_null_device as checker
else:
    import check_portable_null_device as checker


class IsTestFileTests(unittest.TestCase):
    def test_test_suffix_is_exempt(self) -> None:
        """Recognize a ``_test.go`` path as a test file."""
        self.assertTrue(checker.is_test_file("pkg/thing_test.go"))

    def test_non_test_suffix_is_not_exempt(self) -> None:
        """Do not treat a plain ``.go`` path as a test file."""
        self.assertFalse(checker.is_test_file("pkg/thing.go"))


class FindViolationsTests(unittest.TestCase):
    def test_detects_env_assignment_literal(self) -> None:
        """Flag a key=value environment literal embedding /dev/null."""
        text = 'env = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n'
        violations = checker.find_violations(text)
        self.assertEqual(1, len(violations))
        line_number, line = violations[0]
        self.assertEqual(1, line_number)
        self.assertIn("/dev/null", line)

    def test_detects_bare_argument_literal(self) -> None:
        """Flag a bare /dev/null literal used as a command argument."""
        text = (
            "probes := [][]string{\n"
            '\t{"diff", "--no-index", "--", "/etc/passwd", "/dev/null"},\n'
            "}\n"
        )
        violations = checker.find_violations(text)
        self.assertEqual([(2, violations[0][1])], violations)

    def test_detects_hooks_path_flag(self) -> None:
        """Flag a core.hooksPath=/dev/null git flag literal."""
        text = '"-c", "core.hooksPath=/dev/null",\n'
        violations = checker.find_violations(text)
        self.assertEqual(1, len(violations))

    def test_passes_os_devnull_concatenation(self) -> None:
        """Do not flag code already using os.DevNull."""
        text = '"GIT_CONFIG_GLOBAL=" + os.DevNull,\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_equality_comparison(self) -> None:
        """Do not flag a == comparison against the patch sentinel."""
        text = 'if path == "" || path == "/dev/null" {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_inequality_comparison(self) -> None:
        """Do not flag a != comparison against a shell argument value."""
        text = 'if optionValue != "/dev/null" {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_lhs_equality_comparison(self) -> None:
        """Do not flag a == comparison with the literal on the left."""
        text = 'if "/dev/null" == path {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_lhs_inequality_comparison(self) -> None:
        """Do not flag a != comparison with the literal on the left."""
        text = 'if "/dev/null" != optionValue {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_no_literal_present(self) -> None:
        """Return no violations when the line has no /dev/null literal."""
        text = 'fmt.Println("hello world")\n'
        self.assertEqual([], checker.find_violations(text))

    def test_reports_correct_line_number_among_many(self) -> None:
        """Report the 1-indexed line number of a later violating line."""
        text = 'package jobsite\n\nenv = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n'
        violations = checker.find_violations(text)
        self.assertEqual([3], [line_number for line_number, _ in violations])


class CheckTests(unittest.TestCase):
    def setUp(self) -> None:
        """Create an isolated directory for writing fixture Go files."""
        temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(temporary_directory.cleanup)
        self.root = Path(temporary_directory.name)

    def write_go(self, name: str, content: str) -> str:
        """Write *content* to *name* under the fixture root and return its path."""
        path = self.root / name
        path.write_text(content, encoding="utf-8")
        return str(path)

    def test_non_go_files_are_ignored(self) -> None:
        """Skip files that do not end in .go regardless of content."""
        path = self.write_go("notes.txt", 'env = "GIT_CONFIG_GLOBAL=/dev/null"\n')
        self.assertEqual(0, checker.check([path]))

    def test_test_files_are_exempted(self) -> None:
        """Skip *_test.go files even when they contain the literal."""
        path = self.write_go(
            "driver_test.go",
            'env = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n',
        )
        self.assertEqual(0, checker.check([path]))

    def test_violating_file_fails_and_prints_diagnostic(self) -> None:
        """Fail and print a diagnostic naming os.DevNull for a violation."""
        path = self.write_go(
            "local.go",
            'env = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n',
        )
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            result = checker.check([path])
        self.assertEqual(1, result)
        self.assertIn("os.DevNull", buffer.getvalue())
        self.assertIn(path, buffer.getvalue())

    def test_conforming_file_passes(self) -> None:
        """Pass a file that already uses os.DevNull."""
        path = self.write_go(
            "git.go",
            '"GIT_CONFIG_GLOBAL=" + os.DevNull,\n',
        )
        self.assertEqual(0, checker.check([path]))

    def test_comparison_fixture_file_passes(self) -> None:
        """Pass a file matching /dev/null as a diff-format text sentinel."""
        path = self.write_go(
            "writes.go",
            'if path == "" || path == "/dev/null" {\n\tcontinue\n}\n',
        )
        self.assertEqual(0, checker.check([path]))

    def test_missing_file_reports_error_and_fails(self) -> None:
        """Fail and report an error for a file that cannot be read."""
        missing = str(self.root / "missing.go")
        buffer = io.StringIO()
        with contextlib.redirect_stderr(buffer):
            result = checker.check([missing])
        self.assertEqual(1, result)
        self.assertIn("ERROR", buffer.getvalue())

    def test_no_files_passes(self) -> None:
        """Pass trivially when no files are given."""
        self.assertEqual(0, checker.check([]))


class MainTests(unittest.TestCase):
    def test_main_returns_check_result(self) -> None:
        """Exit 0 via main() when no files are given."""
        self.assertEqual(0, checker.main([]))


if __name__ == "__main__":
    unittest.main()
