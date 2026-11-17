package testtags

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckResolvesActiveAndRetiredIDs(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `@req:REQ-API-00001
Feature: Health endpoint
`)
	writeFile(t, target, "tests/legacy.feature", `@req:REQ-API-00002
Feature: Historical health body
`)

	diagnostics := Check(target, known("REQ-API-00001", "REQ-API-00002"))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestCheckAllowsRetiredIDAsOnlyAssociation(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/retired.feature", `@req:REQ-API-00002
Feature: Retired obligation still runnable
`)

	diagnostics := Check(target, known("REQ-API-00002"))
	if len(diagnostics) != 0 {
		t.Fatalf("retired-only tags must pass: %v", diagnostics)
	}
}

func TestCheckUnresolvedAndMisspelledIDs(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `@req:REQ-API-00001 @req:REQ-UNKNOWN
Feature: Health endpoint

  @req:REQ-API-0000l
  Scenario: Misspelled identifier
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasUnresolved(diagnostics, "tests/health.feature", 1, "REQ-UNKNOWN") {
		t.Fatalf("missing unknown-ID diagnostic: %v", diagnostics)
	}
	if !hasUnresolved(diagnostics, "tests/health.feature", 4, "REQ-API-0000l") {
		t.Fatalf("missing misspelled-ID diagnostic: %v", diagnostics)
	}
	if hasUnresolved(diagnostics, "tests/health.feature", 1, "REQ-API-00001") {
		t.Fatalf("known ID was reported unresolved: %v", diagnostics)
	}
}

func TestCheckBOMPrefixedFeature(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/bom_unknown.feature", "\ufeff@req:REQ-UNKNOWN\nFeature: BOM prefixed unknown\n")
	writeFile(t, target, "tests/bom_known.feature", "\ufeff@req:REQ-API-00001\nFeature: BOM prefixed known\n")

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasUnresolved(diagnostics, "tests/bom_unknown.feature", 1, "REQ-UNKNOWN") {
		t.Fatalf("missing unknown-ID diagnostic for BOM-prefixed feature: %v", diagnostics)
	}
	if hasUnresolved(diagnostics, "tests/bom_known.feature", 1, "REQ-API-00001") {
		t.Fatalf("known ID in BOM-prefixed feature was reported unresolved: %v", diagnostics)
	}
}

func TestCheckReportsLineLevelPathAndID(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/nested/health.feature", `Feature: Health endpoint
  @req:REQ-MISSING
  Scenario: Nested tag
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeUnresolved {
		t.Fatalf("code=%q, want %q", got.Code, CodeUnresolved)
	}
	if got.Path != "tests/nested/health.feature" {
		t.Fatalf("path=%q", got.Path)
	}
	if got.Line != 2 {
		t.Fatalf("line=%d, want 2", got.Line)
	}
	if !strings.Contains(got.Message, "REQ-MISSING") {
		t.Fatalf("message %q omitted unresolved ID", got.Message)
	}
}

func TestCheckMixedGherkinTags(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `@smoke @req:REQ-API-00001 @wip
Feature: Health endpoint
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if len(diagnostics) != 0 {
		t.Fatalf("mixed tags with a known ID failed: %v", diagnostics)
	}
}

func TestCheckDocstringsIgnored(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/docstring.feature", `Feature: Docstrings
  Scenario: Examples inside docstrings
    Given sample documentation:
      """
      @req:REQ-INVALID-00001
      Content with tag prefix
      """
    When sending raw snippet:
      `+"```"+`
      @req:REQ-INVALID-00002
      `+"```"+`
    When sending json with media-type opener:
      """json
      @req:REQ-INVALID-00003
      """
    When sending block with escaped delimiter:
      """
      \"""
      @req:REQ-INVALID-00004
      """
  @req:REQ-AFTER
  Scenario: After docstring
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if hasUnresolved(diagnostics, "tests/docstring.feature", 5, "REQ-INVALID-00001") {
		t.Fatalf("triple-quote docstring emitted unresolved tag: %v", diagnostics)
	}
	if hasUnresolved(diagnostics, "tests/docstring.feature", 10, "REQ-INVALID-00002") {
		t.Fatalf("backtick docstring emitted unresolved tag: %v", diagnostics)
	}
	if hasUnresolved(diagnostics, "tests/docstring.feature", 14, "REQ-INVALID-00003") {
		t.Fatalf("media-type docstring emitted unresolved tag: %v", diagnostics)
	}
	if hasUnresolved(diagnostics, "tests/docstring.feature", 19, "REQ-INVALID-00004") {
		t.Fatalf("escaped delimiter docstring emitted unresolved tag: %v", diagnostics)
	}
	if !hasUnresolved(diagnostics, "tests/docstring.feature", 21, "REQ-AFTER") {
		t.Fatalf("tag after docstring was not scanned: %v", diagnostics)
	}
}

func TestCheckUnterminatedDocstring(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/unterminated.feature", `Feature: Unterminated docstring
  Scenario: Unclosed block
    """
    @req:REQ-INSIDE-00001
    content that never closes
@req:REQ-AFTER-00001
Feature: Another feature
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasCodePath(diagnostics, CodeReadFailed, "tests/unterminated.feature") {
		t.Fatalf("missing read_failed diagnostic for unterminated docstring: %v", diagnostics)
	}
	var readFailed *Diagnostic
	for i := range diagnostics {
		if diagnostics[i].Code == CodeReadFailed && diagnostics[i].Path == "tests/unterminated.feature" {
			readFailed = &diagnostics[i]
			break
		}
	}
	if readFailed == nil {
		t.Fatalf("expected CodeReadFailed diagnostic on tests/unterminated.feature")
	}
	if readFailed.Line != 3 {
		t.Fatalf("read_failed line=%d, want 3", readFailed.Line)
	}
	wantMsg := "unterminated Gherkin docstring; later @req tags were not scanned"
	if readFailed.Message != wantMsg {
		t.Fatalf("read_failed message=%q, want %q", readFailed.Message, wantMsg)
	}
	if hasUnresolved(diagnostics, "tests/unterminated.feature", 4, "REQ-INSIDE-00001") {
		t.Fatalf("unterminated docstring emitted tag from inside block: %v", diagnostics)
	}
	if hasUnresolved(diagnostics, "tests/unterminated.feature", 6, "REQ-AFTER-00001") {
		t.Fatalf("tag after unterminated docstring should not be scanned: %v", diagnostics)
	}
}

func TestCheckIgnoresNonTagLinesAndComments(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `# @req:REQ-UNKNOWN
Feature: Health endpoint
  Scenario: Mention a tag in a step
    Then the body contains "@req:REQ-UNKNOWN"
`)
	writeFile(t, target, "tests/notes.md", "@req:REQ-UNKNOWN\n")
	writeFile(t, target, "tests/helper.go", "// @req:REQ-UNKNOWN\n")
	writeFile(t, target, "docs/example.feature", `@req:REQ-UNKNOWN
Feature: Outside tests
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if len(diagnostics) != 0 {
		t.Fatalf("non-tag occurrences were extracted: %v", diagnostics)
	}
}

func TestCheckMissingTestsDirectory(t *testing.T) {
	target := t.TempDir()
	diagnostics := Check(target, known("REQ-API-00001"))
	if len(diagnostics) != 0 {
		t.Fatalf("missing tests/ produced diagnostics: %v", diagnostics)
	}
}

func TestCheckEmptyTestsDirectory(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, TestsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	diagnostics := Check(target, known("REQ-API-00001"))
	if len(diagnostics) != 0 {
		t.Fatalf("empty tests/ produced diagnostics: %v", diagnostics)
	}
}

func TestCheckTestsPathIsFile(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, TestsDir, "not a directory\n")
	diagnostics := Check(target, nil)
	if !hasCodePath(diagnostics, CodeUnexpectedFile, TestsDir) {
		t.Fatalf("missing file-as-tests diagnostic: %v", diagnostics)
	}
}

func TestCheckEmptyReqTag(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `@req:
Feature: Empty identifier
`)
	diagnostics := Check(target, map[string]struct{}{"REQ-API-00001": {}, "": {}})
	if !hasUnresolved(diagnostics, "tests/health.feature", 1, "") {
		t.Fatalf("missing empty-ID diagnostic: %v", diagnostics)
	}
}

func TestCheckInlineCommentDoesNotHideTag(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `@req:REQ-MISSING # historical
Feature: Health endpoint
`)
	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasUnresolved(diagnostics, "tests/health.feature", 1, "REQ-MISSING") {
		t.Fatalf("missing diagnostic before inline comment: %v", diagnostics)
	}
}

func TestCheckReportsEveryOccurrence(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/a.feature", `@req:REQ-MISSING
Feature: First
`)
	writeFile(t, target, "tests/b.feature", `@req:REQ-MISSING @req:REQ-ALSO-MISSING
Feature: Second
`)

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasUnresolved(diagnostics, "tests/a.feature", 1, "REQ-MISSING") {
		t.Fatalf("missing a.feature diagnostic: %v", diagnostics)
	}
	if !hasUnresolved(diagnostics, "tests/b.feature", 1, "REQ-MISSING") {
		t.Fatalf("missing b.feature REQ-MISSING diagnostic: %v", diagnostics)
	}
	if !hasUnresolved(diagnostics, "tests/b.feature", 1, "REQ-ALSO-MISSING") {
		t.Fatalf("missing b.feature REQ-ALSO-MISSING diagnostic: %v", diagnostics)
	}
}

func TestCheckDeterministicOrder(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "tests/z.feature", `@req:REQ-Z
Feature: Z
`)
	writeFile(t, target, "tests/a.feature", `@req:REQ-A
Feature: A
`)

	first := Check(target, nil)
	second := Check(target, nil)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("first=%v second=%v", first, second)
	}
	if first[0].Path != "tests/a.feature" || first[1].Path != "tests/z.feature" {
		t.Fatalf("order=%v", []string{first[0].Path, first[1].Path})
	}
	if first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("repeated check changed diagnostics: %v vs %v", first, second)
	}
}

func TestCheckDoesNotFollowFeatureSymlink(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.feature")
	if err := os.WriteFile(outside, []byte("@req:REQ-SECRET-00001\nFeature: Leaked\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	writeFile(t, target, "tests/health.feature", `@req:REQ-API-00001
Feature: Health endpoint
`)
	link := filepath.Join(target, "tests", "escape.feature")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasCodePath(diagnostics, CodeUnexpectedFile, "tests/escape.feature") {
		t.Fatalf("missing symlink diagnostic: %v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, "REQ-SECRET-00001") {
			t.Fatalf("scanner followed a symlink out of the checkout: %v", diagnostics)
		}
	}
}

func TestCheckDirectorySymlinkUnexpectedFile(t *testing.T) {
	outsideDir := t.TempDir()
	writeFile(t, outsideDir, "test.feature", "@req:REQ-OUTSIDE-00001\nFeature: Outside\n")

	target := t.TempDir()
	testsSub := filepath.Join(target, TestsDir, "sub")
	if err := os.MkdirAll(filepath.Dir(testsSub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, testsSub); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasCodePath(diagnostics, CodeUnexpectedFile, "tests/sub") {
		t.Fatalf("missing unexpected_file diagnostic for directory symlink: %v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == CodeUnexpectedFile && diagnostic.Path == "tests/sub" && diagnostic.Message != "is not a regular file" {
			t.Fatalf("directory symlink message=%q, want %q", diagnostic.Message, "is not a regular file")
		}
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == "tests/sub/test.feature" || strings.Contains(diagnostic.Message, "REQ-OUTSIDE-00001") {
			t.Fatalf("scanner followed a directory symlink: %v", diagnostics)
		}
	}
}

func TestCheckDoesNotFollowTestsSymlink(t *testing.T) {
	outsideDir := t.TempDir()
	writeFile(t, outsideDir, "secret.feature", "@req:REQ-SECRET-00001\nFeature: Leaked\n")

	target := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(target, TestsDir)); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasCodePath(diagnostics, CodeUnexpectedFile, TestsDir) {
		t.Fatalf("missing tests-symlink diagnostic: %v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, "REQ-SECRET-00001") {
			t.Fatalf("scanner followed a tests/ symlink: %v", diagnostics)
		}
	}
}

func TestCheckLongLineWithinBufferLimit(t *testing.T) {
	target := t.TempDir()
	longLine := strings.Repeat("x", 70*1024)
	content := "@req:REQ-UNKNOWN\nFeature: Long but admitted line\n" + longLine + "\n"
	writeFile(t, target, "tests/long.feature", content)

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasUnresolved(diagnostics, "tests/long.feature", 1, "REQ-UNKNOWN") {
		t.Fatalf("expected line 1 unresolved diagnostic: %v", diagnostics)
	}
	if hasCodePath(diagnostics, CodeReadFailed, "tests/long.feature") {
		t.Fatalf("70 KiB line should be within the scanner limit: %v", diagnostics)
	}
}

func TestCheckBufferResiliencePreservesEarlierDiagnostics(t *testing.T) {
	target := t.TempDir()
	oversizedLine := strings.Repeat("x", maxFeatureLineBytes+1)
	content := "@req:REQ-UNKNOWN\nFeature: Oversized line\n" + oversizedLine + "\n@req:REQ-AFTER\n"
	writeFile(t, target, "tests/oversized.feature", content)

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasUnresolved(diagnostics, "tests/oversized.feature", 1, "REQ-UNKNOWN") {
		t.Fatalf("expected line 1 unresolved diagnostic to be preserved: %v", diagnostics)
	}
	if !hasCodePath(diagnostics, CodeReadFailed, "tests/oversized.feature") {
		t.Fatalf("expected read_failed diagnostic for oversized line: %v", diagnostics)
	}
	var readFailed *Diagnostic
	for i := range diagnostics {
		if diagnostics[i].Code == CodeReadFailed && diagnostics[i].Path == "tests/oversized.feature" {
			readFailed = &diagnostics[i]
			break
		}
	}
	if readFailed == nil {
		t.Fatalf("expected CodeReadFailed diagnostic on tests/oversized.feature")
	}
	if readFailed.Line != 3 {
		t.Fatalf("read_failed line=%d, want 3", readFailed.Line)
	}
	wantMsg := "line exceeds 1 MiB limit; later @req tags were not scanned"
	if readFailed.Message != wantMsg {
		t.Fatalf("read_failed message=%q, want %q", readFailed.Message, wantMsg)
	}
	if hasUnresolved(diagnostics, "tests/oversized.feature", 4, "REQ-AFTER") {
		t.Fatalf("tag after oversized line should not be scanned: %v", diagnostics)
	}
}

func TestCheckUnreadableFeature(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not portable")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read mode 000 files")
	}
	target := t.TempDir()
	rel := "tests/health.feature"
	writeFile(t, target, rel, "@req:REQ-API-00001\nFeature: Health\n")
	path := filepath.Join(target, filepath.FromSlash(rel))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("filesystem allows reading mode 000 files")
	}

	diagnostics := Check(target, known("REQ-API-00001"))
	if !hasCodePath(diagnostics, CodeReadFailed, rel) {
		t.Fatalf("missing unreadable-file diagnostic: %v", diagnostics)
	}
}

func known(ids ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasUnresolved(diagnostics []Diagnostic, path string, line int, id string) bool {
	want := "@req tag references unknown requirement ID \"" + id + "\""
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == CodeUnresolved && diagnostic.Path == path && diagnostic.Line == line && strings.Contains(diagnostic.Message, want) {
			return true
		}
	}
	return false
}

func hasCodePath(diagnostics []Diagnostic, code, path string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Path == path {
			return true
		}
	}
	return false
}
