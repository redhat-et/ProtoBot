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
    from scripts import check_validator_spec_parity as checker
else:
    import check_validator_spec_parity as checker

_SAMPLE_GO = """package sample

// Diagnostic codes for sample checks.
const (
	CodeSampleError = "sample.error"
)

const ignored = "not.a.code"
"""

_SAMPLE_README = "`sample.error` is documented.\n"
_SAMPLE_SPEC = """# Spec

### Validator

The validator checks:

- `sample.error` check
"""


_SAMPLE_ARCH = """# Architecture

#### Validator

Codes: `sample.error`.

#### Specification pull request check

Unrelated.
"""


def _arch_path(root: Path) -> Path:
    """Return the registered architecture document path under *root*."""
    return root / "docs" / "architecture.md"


def _write(path: Path, content: str) -> None:
    """Create parent directories and write UTF-8 *content* to *path*."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def _layout(root: Path) -> tuple[Path, Path, Path]:
    """Return Go, README, and spec paths under *root* after creating dirs.

    A documented ``docs/architecture.md`` is also written; tests that exercise
    its absence overwrite it via ``_arch_path``.
    """
    go_path = root / "validator" / "internal" / "sample" / "check.go"
    readme = root / "validator" / "README.md"
    spec = root / "docs" / "architecture" / "ProtoBotV2.md"
    go_path.parent.mkdir(parents=True, exist_ok=True)
    readme.parent.mkdir(parents=True, exist_ok=True)
    spec.parent.mkdir(parents=True, exist_ok=True)
    _write(_arch_path(root), _SAMPLE_ARCH)
    return go_path, readme, spec


class HumanizeCodeTests(unittest.TestCase):
    def test_replaces_dot_and_underscore(self) -> None:
        """Turn a diagnostic code into a prose phrase."""
        self.assertEqual(
            "sample error",
            checker.humanize_code("sample.error"),
        )
        self.assertEqual(
            "requirement invalid basename",
            checker.humanize_code("requirement.invalid_basename"),
        )


class StripBlockCommentsTests(unittest.TestCase):
    def test_empty_source_returns_empty(self) -> None:
        """Return an empty string when there is no source to scan."""
        self.assertEqual("", checker.strip_block_comments(""))

    def test_text_without_comments_is_unchanged(self) -> None:
        """Leave source with no block comments untouched."""
        source = 'const CodeFoo = "foo.bar"\n'
        self.assertEqual(source, checker.strip_block_comments(source))

    def test_closed_comment_at_eof_leaves_no_remainder(self) -> None:
        """Handle a closed block comment that consumes the rest of the file."""
        stripped = checker.strip_block_comments("/*x*/")
        self.assertEqual(len("/*x*/"), len(stripped))
        self.assertTrue(stripped.isspace())

    def test_closed_comment_is_blanked(self) -> None:
        """Replace a closed block comment with spaces and keep newlines."""
        source = 'package p\n/* const CodeHidden = "hide.me" */\nconst x = 1\n'
        stripped = checker.strip_block_comments(source)
        self.assertNotIn("hide.me", stripped)
        self.assertIn("package p", stripped)
        self.assertEqual(source.count("\n"), stripped.count("\n"))

    def test_unclosed_comment_is_blanked_to_eof(self) -> None:
        """Blank an unclosed block comment through the end of the file."""
        source = 'const CodeFoo = "foo.bar"\n/* unclosed\nCodeBar = "bar.baz"\n'
        stripped = checker.strip_block_comments(source)
        self.assertIn('CodeFoo = "foo.bar"', stripped)
        self.assertNotIn("bar.baz", stripped)
        self.assertEqual(source.count("\n"), stripped.count("\n"))


class ParseCodeConstantsTests(unittest.TestCase):
    def test_grouped_and_single_const_declarations(self) -> None:
        """Parse grouped quoted values and a single const declaration."""
        source = """package p
const (
	CodeAlpha = "alpha.one"
	CodeBeta  = `beta.two`
)
const CodeGamma = "gamma.three"
"""
        constants = checker.parse_code_constants(source, "p.go")
        self.assertEqual(
            ["alpha.one", "beta.two", "gamma.three"],
            [item.value for item in constants],
        )
        self.assertEqual(
            ["CodeAlpha", "CodeBeta", "CodeGamma"], [c.name for c in constants]
        )
        self.assertEqual([3, 4, 6], [c.line for c in constants])

    def test_comments_and_non_code_assignments_are_ignored(self) -> None:
        """Skip line comments, commented-out constants, and non-Code names."""
        source = """package p
// const CodeHidden = "hide.me"
const NotACode = "also.skip"
const CodeVisible = "see.me" // trailing
iotaValue := "iota.skip"
"""
        constants = checker.parse_code_constants(source, "p.go")
        self.assertEqual(["see.me"], [item.value for item in constants])

    def test_block_comment_hides_constant(self) -> None:
        """Do not parse constants that live only inside a block comment."""
        source = """package p
/*
const CodeHidden = "hide.me"
*/
const CodeVisible = "see.me"
"""
        constants = checker.parse_code_constants(source, "p.go")
        self.assertEqual(["see.me"], [item.value for item in constants])

    def test_typed_and_digit_constants_are_parsed(self) -> None:
        """Parse constants with explicit types and codes containing digits."""
        source = """package p
const CodeProbeTyped string = "layout.probe"
CodeV2Check = "v2.check_1"
"""
        constants = checker.parse_code_constants(source, "p.go")
        self.assertEqual(["layout.probe", "v2.check_1"], [c.value for c in constants])
        self.assertEqual(["CodeProbeTyped", "CodeV2Check"], [c.name for c in constants])

    def test_unmatched_code_assignment_is_an_error(self) -> None:
        """Report lines with Code* assignments that do not match _CONST_ASSIGN."""
        source = """package p
const CodeBroken = 123
CodeInvalid string = "not a valid code"
const CodeValid = "valid.code"
"""
        errors = checker.find_unmatched_code_constants(source, "p.go")
        self.assertEqual(2, len(errors))
        self.assertIn("CodeBroken", errors[0])
        self.assertIn("CodeInvalid", errors[1])


class ExtractDocumentedCodesTests(unittest.TestCase):
    def test_backtick_codes_are_extracted(self) -> None:
        """Collect diagnostic codes from Markdown backticks."""
        text = "See `sample.error` and `other.code` plus bare not.a.match."
        self.assertEqual(
            {"sample.error", "other.code"},
            checker.extract_documented_codes(text),
        )

    def test_empty_document_has_no_codes(self) -> None:
        """Return an empty set when no backtick codes exist."""
        self.assertEqual(set(), checker.extract_documented_codes("no codes here"))


class ValidatorSectionTests(unittest.TestCase):
    def test_missing_heading_returns_empty(self) -> None:
        """Return empty when the Validator heading is absent."""
        self.assertEqual("", checker.extract_validator_section("# Other\n\nbody\n"))

    def test_section_runs_until_next_heading(self) -> None:
        """Stop the Validator section at the next heading."""
        text = "# Title\n\n### Validator\n\nbody\n\n### Next\n\nlater\n"
        section = checker.extract_validator_section(text)
        self.assertIn("body", section)
        self.assertNotIn("later", section)

    def test_section_runs_to_eof_without_next_heading(self) -> None:
        """Keep the rest of the file when no later heading exists."""
        text = "## Validator\n\nonly this\n"
        self.assertIn("only this", checker.extract_validator_section(text))

    def test_listing_falls_back_to_whole_document(self) -> None:
        """Use the whole document when the Validator section is empty."""
        text = "### Validator\n\n### Next\n`sample.error`\n"
        self.assertIn("sample.error", checker.spec_listing_text(text))

    def test_listing_uses_nonempty_section(self) -> None:
        """Prefer a non-empty Validator section over the rest of the file."""
        text = "### Validator\n\n`sample.error`\n\n### Next\n`other.code`\n"
        listing = checker.spec_listing_text(text)
        self.assertIn("sample.error", listing)
        self.assertNotIn("other.code", listing)


class IsListedInSpecTests(unittest.TestCase):
    def test_empty_inputs_are_not_listed(self) -> None:
        """Reject an empty code or empty specification text."""
        self.assertFalse(checker.is_listed_in_spec("", "sample error check"))
        self.assertFalse(checker.is_listed_in_spec("sample.error", "   \n"))

    def test_exact_code_is_listed(self) -> None:
        """Accept the diagnostic code string itself."""
        self.assertTrue(checker.is_listed_in_spec("sample.error", "`sample.error`"))

    def test_prose_without_code_is_not_listed(self) -> None:
        """Reject prose check-list phrases that omit the exact code."""
        self.assertFalse(
            checker.is_listed_in_spec("sample.error", "- sample error check")
        )

    def test_unrelated_prose_is_not_listed(self) -> None:
        """Reject specification text that names neither the code nor its prose."""
        self.assertFalse(checker.is_listed_in_spec("sample.error", "- other check"))

    def test_prefix_of_longer_code_is_not_listed(self) -> None:
        """A diagnostic code that is a prefix of a listed code is not listed."""
        self.assertFalse(
            checker.is_listed_in_spec("layout.read", "`layout.read_failed`")
        )
        self.assertFalse(checker.is_listed_in_spec("layout.read", "layout read failed"))
        self.assertFalse(
            checker.is_listed_in_spec("layout.read", "layout read failed check")
        )
        self.assertFalse(
            checker.is_listed_in_spec("layout.read", "layout read check failed")
        )
        self.assertFalse(
            checker.is_listed_in_spec("requirement.invalid", "`requirement.invalid_id`")
        )
        self.assertFalse(
            checker.is_listed_in_spec("requirement.invalid", "requirement invalid id")
        )
        self.assertTrue(
            checker.is_listed_in_spec(
                "layout.read", "(`layout.read_failed`, `layout.read`)"
            )
        )

    def test_spec_heading_collisions_and_prose_are_not_listed(self) -> None:
        """Headings and prose phrases are not treated as diagnostic listings."""
        self.assertFalse(
            checker.is_listed_in_spec(
                "requirement.check",
                "requirement check codes (`requirement.missing_field`)",
            )
        )
        self.assertFalse(checker.is_listed_in_spec("ears.shape", "EARS shape:"))
        self.assertFalse(
            checker.is_listed_in_spec("revision.check", "revision check codes")
        )
        self.assertFalse(checker.is_listed_in_spec("layout.codes", "layout codes"))


class RawStringDuplicateTests(unittest.TestCase):
    def test_declaration_is_not_a_duplicate(self) -> None:
        """Ignore the constant declaration itself."""
        source = 'const CodeSampleError = "sample.error"\n'
        self.assertEqual(
            [],
            checker.find_raw_string_duplicates(
                source, "check.go", {"sample.error": "CodeSampleError"}
            ),
        )

    def test_duplicate_literal_is_reported(self) -> None:
        """Flag a raw string that copies an exported diagnostic code."""
        source = """package p
const CodeSampleError = "sample.error"
func f() { code := "sample.error" }
func g() { other := "unrelated.token" }
"""
        duplicates = checker.find_raw_string_duplicates(
            source, "check.go", {"sample.error": "CodeSampleError"}
        )
        self.assertEqual(1, len(duplicates))
        self.assertEqual("sample.error", duplicates[0].value)
        self.assertEqual("CodeSampleError", duplicates[0].constant_name)
        self.assertEqual(3, duplicates[0].line)

    def test_commented_literal_is_ignored(self) -> None:
        """Do not flag a duplicated code that appears only in a comment."""
        source = 'func f() { // "sample.error"\n}\n'
        self.assertEqual(
            [],
            checker.find_raw_string_duplicates(
                source, "check.go", {"sample.error": "CodeSampleError"}
            ),
        )

    def test_fixture_and_escaped_strings_are_not_duplicates(self) -> None:
        """A code mentioned inside a JSON fixture or escaped string is not a duplicate."""
        source = """package p
var fixture = `{"code": "sample.error"}`
var escaped = "{\\"code\\": \\"sample.error\\"}"
var prose = "prefix sample.error suffix"
"""
        duplicates = checker.find_raw_string_duplicates(
            source, "check.go", {"sample.error": "CodeSampleError"}
        )
        self.assertEqual([], duplicates)

    def test_duplicate_backtick_literal_is_reported(self) -> None:
        """Flag a raw backtick string that copies an exported diagnostic code."""
        source = "func f() { code := `sample.error` }\n"
        duplicates = checker.find_raw_string_duplicates(
            source, "check.go", {"sample.error": "CodeSampleError"}
        )
        self.assertEqual(1, len(duplicates))
        self.assertEqual("sample.error", duplicates[0].value)
        self.assertEqual(1, duplicates[0].line)


class RelativePosixTests(unittest.TestCase):
    def test_relative_path_uses_posix(self) -> None:
        """Return a repo-relative posix path for files inside the root."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "validator" / "internal" / "x.go"
            path.parent.mkdir(parents=True)
            path.write_text("package x\n", encoding="utf-8")
            self.assertEqual(
                "validator/internal/x.go",
                checker._relative_posix(path, root),
            )

    def test_outside_path_falls_back_to_as_posix(self) -> None:
        """Fall back to as_posix when the path is outside the repo root."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            root.mkdir()
            outside = Path(tmp) / "other.go"
            outside.write_text("package o\n", encoding="utf-8")
            self.assertEqual(
                outside.as_posix(),
                checker._relative_posix(outside, root),
            )

    def test_resolve_oserror_falls_back(self) -> None:
        """Fall back to as_posix when resolve raises OSError."""
        path = Path("missing.go")
        with patch.object(Path, "resolve", side_effect=OSError("boom")):
            self.assertEqual("missing.go", checker._relative_posix(path, Path(".")))


class ReadTextTests(unittest.TestCase):
    def test_oserror_and_unicodeerror_are_wrapped(self) -> None:
        """Report the exception type without leaking details."""
        path = Path("missing.md")
        with (
            patch.object(Path, "read_text", side_effect=FileNotFoundError("x")),
            self.assertRaises(checker.ValidatorSpecParityError) as raised,
        ):
            checker._read_text(path)
        self.assertIn("FileNotFoundError", str(raised.exception))
        self.assertNotIn("x", str(raised.exception))

        decode = UnicodeDecodeError("utf-8", b"\xff", 0, 1, "bad")
        with (
            patch.object(Path, "read_text", side_effect=decode),
            self.assertRaises(checker.ValidatorSpecParityError) as raised,
        ):
            checker._read_text(path)
        self.assertIn("UnicodeDecodeError", str(raised.exception))


class IterGoSourcesTests(unittest.TestCase):
    def test_skips_tests_testdata_and_directories(self) -> None:
        """Keep production Go files and skip tests, testdata, and dirs."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            keep = root / "keep.go"
            keep.write_text("package keep\n", encoding="utf-8")
            (root / "keep_test.go").write_text("package keep\n", encoding="utf-8")
            testdata = root / "testdata" / "skip.go"
            testdata.parent.mkdir()
            testdata.write_text("package skip\n", encoding="utf-8")
            (root / "not_a_file.go").mkdir()
            got = checker._iter_go_sources(root)
            self.assertEqual([keep], got)

    def test_is_file_oserror_is_skipped(self) -> None:
        """Skip a path that raises OSError from is_file."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "keep.go"
            path.write_text("package keep\n", encoding="utf-8")
            real_is_file = Path.is_file

            def is_file(self: Path) -> bool:
                if self.name == "keep.go":
                    raise OSError("boom")
                return real_is_file(self)

            with patch.object(Path, "is_file", is_file):
                self.assertEqual([], checker._iter_go_sources(root))

    def test_relative_to_valueerror_is_skipped(self) -> None:
        """Skip a glob result that is not relative to the internal root."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "keep.go"
            path.write_text("package keep\n", encoding="utf-8")
            with patch.object(Path, "relative_to", side_effect=ValueError("nope")):
                self.assertEqual([], checker._iter_go_sources(root))


class CollectConstantsTests(unittest.TestCase):
    def test_missing_internal_directory_is_an_error(self) -> None:
        """Fail closed when validator/internal does not exist."""
        with tempfile.TemporaryDirectory() as tmp:
            constants, errors = checker.collect_constants(Path(tmp))
        self.assertEqual([], constants)
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
                constants, errors = checker.collect_constants(root)
        self.assertEqual([], constants)
        self.assertIn("PermissionError", errors[0])

    def test_unreadable_go_file_is_recorded(self) -> None:
        """Record a read failure and continue with other files."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            with patch.object(
                checker,
                "_read_text",
                side_effect=checker.ValidatorSpecParityError("go read failed"),
            ):
                constants, errors = checker.collect_constants(root)
        self.assertEqual([], constants)
        self.assertEqual(["go read failed"], errors)


class CollectRawStringDuplicatesTests(unittest.TestCase):
    def test_empty_constants_return_no_duplicates(self) -> None:
        """Skip the specvalidation scan when no constants were declared."""
        duplicates, errors = checker.collect_raw_string_duplicates(Path("/tmp"), [])
        self.assertEqual([], duplicates)
        self.assertEqual([], errors)

    def test_missing_specvalidation_dir_is_ok(self) -> None:
        """Treat a missing specvalidation directory as having no duplicates."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, _readme, _spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            constants, _errors = checker.collect_constants(root)
            duplicates, errors = checker.collect_raw_string_duplicates(root, constants)
        self.assertEqual([], duplicates)
        self.assertEqual([], errors)

    def test_is_dir_oserror_is_an_error(self) -> None:
        """Fail closed when stat-ing specvalidation raises OSError."""
        constant = checker.CodeConstant("CodeX", "sample.error", "x.go", 1)
        real_is_dir = Path.is_dir

        def is_dir(self: Path) -> bool:
            if self.as_posix().endswith("specvalidation"):
                raise OSError("denied")
            return real_is_dir(self)

        with tempfile.TemporaryDirectory() as tmp, patch.object(Path, "is_dir", is_dir):
            duplicates, errors = checker.collect_raw_string_duplicates(
                Path(tmp), [constant]
            )
        self.assertEqual([], duplicates)
        self.assertIn("OSError", errors[0])

    def test_unreadable_specvalidation_file_is_recorded(self) -> None:
        """Record a read failure for a specvalidation Go file."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, _readme, _spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            sv = root / "validator" / "internal" / "specvalidation" / "x.go"
            _write(sv, 'package specvalidation\ncode := "sample.error"\n')
            constants, _errors = checker.collect_constants(root)
            with patch.object(
                checker,
                "_read_text",
                side_effect=checker.ValidatorSpecParityError("sv read failed"),
            ):
                duplicates, errors = checker.collect_raw_string_duplicates(
                    root, constants
                )
        self.assertEqual([], duplicates)
        self.assertEqual(["sv read failed"], errors)

    def test_duplicate_literal_in_specvalidation_is_found(self) -> None:
        """Report a raw diagnostic string in specvalidation production code."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, _readme, _spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            sv = root / "validator" / "internal" / "specvalidation" / "x.go"
            _write(sv, 'package specvalidation\ncode := "sample.error"\n')
            constants, _errors = checker.collect_constants(root)
            duplicates, errors = checker.collect_raw_string_duplicates(root, constants)
        self.assertEqual([], errors)
        self.assertEqual(1, len(duplicates))
        self.assertEqual("sample.error", duplicates[0].value)


class EvaluateAndCheckTests(unittest.TestCase):
    def test_passes_when_fully_documented(self) -> None:
        """Diagnostic code documented in both targets passes."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            report = checker.evaluate(root)
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                rc = checker.check(root)
        self.assertTrue(report.ok)
        self.assertEqual(0, rc)
        self.assertIn("Validated 1 diagnostic code", stdout.getvalue())
        self.assertEqual([], list(report.missing_in_readme))
        self.assertEqual([], list(report.missing_in_protobot_v2))

    def test_detects_undocumented_diagnostic_constant(self) -> None:
        """Introducing a dummy Code* constant without docs exits non-zero."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, "")
            _write(spec, "")
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc = checker.check(root)
            output = stderr.getvalue()
        self.assertNotEqual(0, rc)
        self.assertIn("sample.error", output)
        self.assertIn("validator/README.md", output)
        self.assertIn("ProtoBotV2.md", output)

    def test_detects_partial_documentation(self) -> None:
        """Diagnostic code documented in README but omitted in spec fails."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, "")
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc = checker.check(root)
            output = stderr.getvalue()
        self.assertNotEqual(0, rc)
        self.assertIn("ProtoBotV2.md", output)
        self.assertNotIn("validator/README.md", output)

    def test_prefix_constant_fails_when_only_longer_sibling_is_listed(
        self,
    ) -> None:
        """A prefix constant fails evaluate when only its longer sibling is in spec."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(
                go_path,
                'package sample\nconst CodeLayoutRead = "layout.read"\n',
            )
            _write(readme, "`layout.read` is documented.\n")
            _write(spec, "# Spec\n\n### Validator\n\n`layout.read_failed`\n")
            report = checker.evaluate(root)
        self.assertFalse(report.ok)
        self.assertIn("layout.read", report.missing_in_protobot_v2)
        self.assertEqual([], list(report.missing_in_readme))

    def test_prefix_constant_fails_when_only_longer_sibling_is_in_prose(
        self,
    ) -> None:
        """A prefix constant fails evaluate when only its longer sibling is in prose."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(
                go_path,
                'package sample\nconst CodeLayoutRead = "layout.read"\n',
            )
            _write(readme, "`layout.read` is documented.\n")
            _write(spec, "# Spec\n\n### Validator\n\n- layout read failed check\n")
            report = checker.evaluate(root)
        self.assertFalse(report.ok)
        self.assertIn("layout.read", report.missing_in_protobot_v2)
        self.assertEqual([], list(report.missing_in_readme))

    def test_duplicate_constant_values_are_reported_once(self) -> None:
        """Deduplicate the same diagnostic string declared twice."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(
                go_path,
                'package sample\nconst CodeA = "sample.error"\n'
                'const CodeB = "sample.error"\n',
            )
            _write(readme, "")
            _write(spec, "")
            report = checker.evaluate(root)
        self.assertEqual(("sample.error",), report.missing_in_readme)
        self.assertEqual(2, len(report.constants))

    def test_missing_readme_and_spec_are_errors(self) -> None:
        """Record read errors when README or ProtoBotV2.md cannot be read."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, _readme, _spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            report = checker.evaluate(root)
        self.assertTrue(report.errors)
        self.assertTrue(any("README.md" in item for item in report.errors))
        self.assertTrue(any("ProtoBotV2.md" in item for item in report.errors))

    def test_zero_constants_fails_evaluate(self) -> None:
        """Fail when validator/internal contains no diagnostic constants."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, "package sample\nconst Other = 123\n")
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            report = checker.evaluate(root)
        self.assertFalse(report.ok)
        self.assertTrue(
            any("no diagnostic constants found" in err for err in report.errors)
        )

    def test_raw_string_duplicate_fails_check(self) -> None:
        """Fail when specvalidation uses a raw string of an exported code."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            sv = root / "validator" / "internal" / "specvalidation" / "x.go"
            _write(sv, 'package specvalidation\ncode := "sample.error"\n')
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc = checker.check(root)
        self.assertEqual(1, rc)
        self.assertIn("duplicates exported constant", stderr.getvalue())

    def test_passes_on_current_repository(self) -> None:
        """Verify that current repository Go constants and documentation match."""
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            rc = checker.check(checker.REPO_ROOT)
        self.assertEqual(0, rc)
        self.assertIn("Validated", stdout.getvalue())


class ArchitectureParityTests(unittest.TestCase):
    def test_missing_code_in_architecture_md_fails_parity(self) -> None:
        """Fail parity when an exported constant is absent from architecture.md."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            _write(
                _arch_path(root),
                "#### Validator\nNo codes listed.\n"
                "#### Specification pull request check\n`sample.error`\n",
            )
            report = checker.evaluate(root)
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rc = checker.check(root)
        self.assertFalse(report.ok)
        self.assertEqual(1, rc)
        self.assertEqual(("sample.error",), report.missing_in_architecture_md)
        self.assertEqual([], list(report.missing_in_protobot_v2))
        lines = checker.format_report(report)
        self.assertTrue(any("docs/architecture.md" in line for line in lines))
        self.assertIn("ears-manager artifact put", stderr.getvalue())

    def test_missing_architecture_md_file_is_an_error(self) -> None:
        """Report a read error when docs/architecture.md does not exist."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            _arch_path(root).unlink()
            report = checker.evaluate(root)
        self.assertFalse(report.ok)
        self.assertTrue(any("architecture.md" in e for e in report.errors))

    def test_level_four_validator_section_is_bounded(self) -> None:
        """A #### Validator section ends at the next heading of level <= 4."""
        text = "#### Validator\n\n`a.b`\n\n#### Next\n\n`c.d`\n"
        section = checker.extract_validator_section(text)
        self.assertIn("a.b", section)
        self.assertNotIn("c.d", section)

    def test_deeper_subheadings_stay_inside_section(self) -> None:
        """Subheadings below the Validator level do not end the section."""
        text = "### Validator\n\nx\n\n#### Sub\n\n`a.b`\n\n### Next\n`c.d`\n"
        section = checker.extract_validator_section(text)
        self.assertIn("a.b", section)
        self.assertNotIn("c.d", section)


class FormatReportTests(unittest.TestCase):
    def test_includes_locations_when_present(self) -> None:
        """Attach constant locations when the code was parsed."""
        report = checker.ParityReport(
            constants=(checker.CodeConstant("CodeX", "sample.error", "x.go", 4),),
            missing_in_readme=("sample.error",),
            missing_in_protobot_v2=("sample.error",),
            missing_in_architecture_md=(),
            raw_string_duplicates=(
                checker.RawStringDuplicate("y.go", 8, "sample.error", "CodeX"),
            ),
            errors=("boom",),
        )
        lines = checker.format_report(report)
        self.assertTrue(report.ok is False)
        joined = "\n".join(lines)
        self.assertIn("ERROR: boom", joined)
        self.assertIn("CodeX at x.go:4", joined)
        self.assertIn("duplicates exported constant CodeX", joined)

    def test_omits_locations_when_absent(self) -> None:
        """Leave the location suffix off when no constant recorded the code."""
        report = checker.ParityReport(
            constants=(),
            missing_in_readme=("sample.error",),
            missing_in_protobot_v2=("sample.error",),
            missing_in_architecture_md=(),
            raw_string_duplicates=(),
            errors=(),
        )
        lines = checker.format_report(report)
        self.assertEqual(2, len(lines))
        self.assertNotIn("(", lines[0].split("code")[-1][:3])
        self.assertIn("sample.error is not documented", lines[0])
        self.assertIn("sample.error is not listed", lines[1])

    def test_ok_is_false_for_each_failure_kind(self) -> None:
        """Each populated failure field makes the report not ok."""
        empty = checker.ParityReport((), (), (), (), (), ())
        self.assertTrue(empty.ok)
        self.assertFalse(checker.ParityReport((), ("x",), (), (), (), ()).ok)
        self.assertFalse(checker.ParityReport((), (), ("x",), (), (), ()).ok)
        self.assertFalse(checker.ParityReport((), (), (), ("x",), (), ()).ok)
        self.assertFalse(
            checker.ParityReport(
                (),
                (),
                (),
                (),
                (checker.RawStringDuplicate("a.go", 1, "x.y", "CodeX"),),
                (),
            ).ok
        )
        self.assertFalse(checker.ParityReport((), (), (), (), (), ("e",)).ok)


class MainTests(unittest.TestCase):
    def test_cli_repo_root_success_and_failure(self) -> None:
        """Honor --repo-root for both a valid tree and an empty tree."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                self.assertEqual(0, checker.main(["--repo-root", str(root)]))
            empty = Path(tmp) / "empty"
            empty.mkdir()
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                self.assertEqual(1, checker.main(["--repo-root", str(empty)]))

    def test_main_defaults_to_repository_root(self) -> None:
        """Check the live repository when --repo-root is omitted."""
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            self.assertEqual(0, checker.main([]))

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
        self.assertIn("Validated", result.stdout)

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
        self.assertIn("Validated", stdout.getvalue())


class UnreadableFileTests(unittest.TestCase):
    @unittest.skipIf(
        hasattr(os, "geteuid") and os.geteuid() == 0,
        "root ignores file mode 000 permissions",
    )
    def test_chmod_zero_go_file_is_a_read_error(self) -> None:
        """Treat a permission-denied Go file as a documented read error."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            go_path, readme, spec = _layout(root)
            _write(go_path, _SAMPLE_GO)
            _write(readme, _SAMPLE_README)
            _write(spec, _SAMPLE_SPEC)
            mode = go_path.stat().st_mode
            os.chmod(go_path, 0)
            try:
                constants, errors = checker.collect_constants(root)
            finally:
                os.chmod(go_path, mode | stat.S_IRUSR)
            self.assertTrue(errors)
            self.assertEqual([], constants)


if __name__ == "__main__":
    unittest.main()
