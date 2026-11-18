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

    def test_detects_raw_string_literal(self) -> None:
        """Flag a key=value environment literal in backtick raw string."""
        text = "env = append(env, `GIT_CONFIG_GLOBAL=/dev/null`)\n"
        violations = checker.find_violations(text)
        self.assertEqual(1, len(violations))
        self.assertEqual(1, violations[0][0])
        self.assertIn("/dev/null", violations[0][1])

    def test_detects_bare_raw_string_literal(self) -> None:
        """Flag a bare `/dev/null` literal in backticks used as argument."""
        text = "probes := []string{`/dev/null`}\n"
        violations = checker.find_violations(text)
        self.assertEqual(1, len(violations))
        self.assertEqual(1, violations[0][0])

    def test_exempts_raw_string_comparison(self) -> None:
        """Do not flag a comparison using a raw string literal."""
        text = "if path == `/dev/null` {\n"
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_multiline_equality_comparison(self) -> None:
        """Do not flag an == comparison split across lines."""
        text = 'if path ==\n\t"/dev/null" {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_multiline_lhs_equality_comparison(self) -> None:
        """Do not flag an == comparison with LHS literal split across lines."""
        text = 'if "/dev/null" ==\n\tpath {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_multiline_inequality_comparison(self) -> None:
        """Do not flag a != comparison split across lines."""
        text = 'if optionValue !=\n\t"/dev/null" {\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_line_comment(self) -> None:
        """Do not flag a line comment mentioning /dev/null."""
        text = '// e.g. use "/dev/null" as the null device\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_block_comment(self) -> None:
        """Do not flag a block comment mentioning /dev/null."""
        text = '/*\n * Hardcoding "/dev/null" is non-portable.\n */\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_switch_case(self) -> None:
        """Do not flag a switch case clause matching the literal."""
        text = 'switch path {\ncase "/dev/null":\n\treturn nil\n}\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_switch_case_multiple_values(self) -> None:
        """Do not flag a switch case with multiple expressions."""
        text = 'switch path {\ncase "", "/dev/null":\n\treturn nil\n}\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_switch_case_multiline(self) -> None:
        """Do not flag a switch case with literal on a continuation line."""
        text = 'switch path {\ncase\n\t"/dev/null":\n\treturn nil\n}\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_path_boundary_substring(self) -> None:
        """Do not flag paths where /dev/null is part of a larger path."""
        text = 'path := "/tmp/dev/null"\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_path_boundary_raw_string_substring(self) -> None:
        """Do not flag raw strings where /dev/null is part of a larger path."""
        text = "path := `/tmp/dev/null`\n"
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_pragma_same_line(self) -> None:
        """Do not flag code with portability:allow-null-device on same line."""
        text = 'env = append(env, "GIT_CONFIG_GLOBAL=/dev/null") // portability:allow-null-device\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_pragma_preceding_line(self) -> None:
        """Do not flag code preceded by portability:allow-null-device."""
        text = '// portability:allow-null-device\nenv = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n'
        self.assertEqual([], checker.find_violations(text))

    def test_combined_exempted_comparison_and_real_violation(self) -> None:
        """Report only real violation in file containing exempted comparison."""
        text = (
            'if path == "/dev/null" {\n'
            "\treturn\n"
            "}\n"
            'env = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n'
        )
        violations = checker.find_violations(text)
        self.assertEqual(1, len(violations))
        self.assertEqual(4, violations[0][0])
        self.assertIn("GIT_CONFIG_GLOBAL", violations[0][1])

    def test_exempts_writes_go_diff_sentinel(self) -> None:
        """Reproduce drafting-table/writes.go diff sentinel comparison."""
        text = 'if path == "" || path == "/dev/null" {\n\tcontinue\n}\n'
        self.assertEqual([], checker.find_violations(text))

    def test_exempts_shell_analysis_go_rcfile_probe(self) -> None:
        """Reproduce drafting-table/shell_analysis.go rcfile probe check."""
        text = 'if optionValue != "/dev/null" {\n\tbreak\n}\n'
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

    def test_suppression_pragma_file_passes(self) -> None:
        """Pass a file using portability:allow-null-device suppression."""
        path = self.write_go(
            "suppressed.go",
            '// portability:allow-null-device\n'
            'env = append(env, "GIT_CONFIG_GLOBAL=/dev/null")\n',
        )
        self.assertEqual(0, checker.check([path]))

    def test_raw_string_file_fails(self) -> None:
        """Fail and report diagnostic for raw string literal violation."""
        path = self.write_go(
            "raw.go",
            "env = append(env, `GIT_CONFIG_GLOBAL=/dev/null`)\n",
        )
        buffer = io.StringIO()
        with contextlib.redirect_stdout(buffer):
            result = checker.check([path])
        self.assertEqual(1, result)
        self.assertIn("os.DevNull", buffer.getvalue())

    def test_comment_only_file_passes(self) -> None:
        """Pass a file that only mentions /dev/null in comments."""
        path = self.write_go(
            "comments.go",
            '// Note: /dev/null is used on Unix\n'
            '/* e.g. "/dev/null" */\n'
            'var x = 1\n',
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
