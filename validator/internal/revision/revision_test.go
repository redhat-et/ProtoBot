package revision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/gitcmd"
	"github.com/redhat-et/protobot/validator/internal/layout"
)

func TestReadLoadsCommittedRequirements(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	snapshot, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if snapshot.Root != root {
		t.Fatalf("snapshot.Root=%q, want %q", snapshot.Root, root)
	}
	if len(snapshot.Requirements) != 1 || snapshot.Requirements[0].Record.ID != "REQ-API-00001" {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
}

func TestReadIgnoresWorkingTreeEdits(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00002"))

	snapshot, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
	if snapshot.Requirements[0].Record.ID != "REQ-API-00001" {
		t.Fatalf("working-tree edit leaked into revision read: %+v", snapshot.Requirements[0].Record)
	}
}

func TestReadMissingRequirementsIsEmpty(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "README.md", "empty spec\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	snapshot, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(diagnostics, CodeEmptyBase) {
		t.Fatalf("diagnostics=%v, want %s", diagnostics, CodeEmptyBase)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
}

func TestReadUnknownRef(t *testing.T) {
	root := gitRepo(t)
	_, _, err := Read(root, "not-a-ref", Options{})
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
	revErr, ok := err.(*Error)
	if !ok || revErr.Code != CodeNotFound {
		t.Fatalf("err=%v, want CodeNotFound", err)
	}
}

func TestReadNonGitTarget(t *testing.T) {
	_, _, err := Read(t.TempDir(), "HEAD", Options{})
	if err == nil {
		t.Fatal("expected error for non-git target")
	}
	revErr, ok := err.(*Error)
	if !ok || revErr.Code != CodeReadFailed {
		t.Fatalf("err=%v, want CodeReadFailed", err)
	}
}

func TestReadSkipsNonYAMLEntriesInBase(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	writeFile(t, root, "spec/requirements/notes.txt", "not yaml\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	snapshot, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 || snapshot.Requirements[0].Record.ID != "REQ-API-00001" {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
}

func TestReadLayoutDiagnosticsForMalformedBase(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/bad.yaml", "id: [\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	_, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasPath(diagnostics, "spec/requirements/bad.yaml") {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if !hasCode(diagnostics, CodeParseFailed) {
		t.Fatalf("expected revision.parse_failed, got %v", diagnostics)
	}
	if len(diagnostics) > 0 && !strings.Contains(diagnostics[0].Message, `comparison base "HEAD"`) {
		t.Fatalf("diagnostic message %q does not contain comparison base ref", diagnostics[0].Message)
	}
}

func TestReadRejectsUnsupportedEntryInBase(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/target.yaml", requirementYAML("REQ-API-00001"))
	linkPath := filepath.Join(root, "spec", "requirements", "LINK-00001.yaml")
	if err := os.Symlink("target.yaml", linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base with symlink")

	_, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(diagnostics, CodeUnsupportedEntry) {
		t.Fatalf("expected %s, got diagnostics=%v", CodeUnsupportedEntry, diagnostics)
	}
	if !hasPath(diagnostics, "spec/requirements/LINK-00001.yaml") {
		t.Fatalf("expected path spec/requirements/LINK-00001.yaml, got %v", diagnostics)
	}
}

func TestReadRejectsNestedDirectoryInBase(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/sub/nested.yaml", requirementYAML("REQ-API-00001"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base with nested directory")

	_, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasPath(diagnostics, "spec/requirements/sub") {
		t.Fatalf("expected path spec/requirements/sub, got %v", diagnostics)
	}
	if !hasCode(diagnostics, CodeUnexpectedFile) {
		t.Fatalf("expected %s, got %v", CodeUnexpectedFile, diagnostics)
	}
}

func TestReadBaseSpecSymlinkFailsUnsupportedEntry(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "other/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	if err := os.Symlink("other", filepath.Join(root, "spec")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base with spec symlink")

	_, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(diagnostics, CodeUnsupportedEntry) {
		t.Fatalf("diagnostics=%v, want %s", diagnostics, CodeUnsupportedEntry)
	}
	if !hasPath(diagnostics, "spec") {
		t.Fatalf("diagnostics=%v, want path 'spec'", diagnostics)
	}
}

func TestReadBaseSpecRequirementsSymlinkFailsUnsupportedEntry(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "other/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other", filepath.Join(root, "spec", "requirements")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base with spec/requirements symlink")

	_, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(diagnostics, CodeUnsupportedEntry) {
		t.Fatalf("diagnostics=%v, want %s", diagnostics, CodeUnsupportedEntry)
	}
	if !hasPath(diagnostics, "spec/requirements") {
		t.Fatalf("diagnostics=%v, want path 'spec/requirements'", diagnostics)
	}
}

func TestSafeRepoPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{path: "spec/requirements/REQ-API-00001.yaml", want: true},
		{path: "spec/requirements", want: true},
		{path: "spec/interfaces/cli.yaml", want: false},
		{path: "../spec/requirements/escape.yaml", want: false},
		{path: "spec/requirements/../secrets.yaml", want: false},
		{path: "", want: false},
	}
	for _, tc := range cases {
		if got := safeRepoPath(tc.path); got != tc.want {
			t.Errorf("safeRepoPath(%q)=%v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestGitOutputRejectsUnknownRef(t *testing.T) {
	root := gitRepo(t)
	if _, err := resolveCommit(root, strings.Repeat("a", 40)); err == nil {
		t.Fatal("expected unknown SHA to fail")
	}
}

func TestReadOversizedBaseFile(t *testing.T) {
	root := gitRepo(t)
	oversized := make([]byte, layout.MaxYAMLBytes+10)
	copy(oversized, "id: REQ-BIG-00001\ntext: ")
	for i := len("id: REQ-BIG-00001\ntext: "); i < len(oversized)-1; i++ {
		oversized[i] = 'a'
	}
	oversized[len(oversized)-1] = '\n'
	writeFile(t, root, "spec/requirements/REQ-BIG-00001.yaml", string(oversized))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "oversized")

	_, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCode(diagnostics, CodeReadFailed) || !hasPath(diagnostics, "spec/requirements/REQ-BIG-00001.yaml") {
		t.Fatalf("diagnostics=%v, want CodeReadFailed for oversized file", diagnostics)
	}
}

func TestReadRejectsTargetHeadSelfComparison(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	_, _, err := Read(root, "HEAD", Options{})
	if err == nil {
		t.Fatal("expected error comparing checkout against HEAD, got nil")
	}
	revErr, ok := err.(*Error)
	if !ok || revErr.Code != CodeSelfComparison {
		t.Fatalf("err=%v, want CodeSelfComparison", err)
	}
	if !strings.Contains(revErr.Message, "distinct revision required") {
		t.Fatalf("message %q omitted distinct-revision guidance", revErr.Message)
	}
}

func TestReadAllowsSelfComparisonWhenOptedIn(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	snapshot, diagnostics, err := Read(root, "HEAD", Options{AllowSelfComparison: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 || snapshot.Requirements[0].Record.ID != "REQ-API-00001" {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
}

func TestReadEmptyBaseEmitsNote(t *testing.T) {
	root := gitRepo(t)
	empty := gitOutputSHA(t, root, "HEAD")
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "with spec")

	snapshot, diagnostics, err := Read(root, empty, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
	if !hasCode(diagnostics, CodeEmptyBase) {
		t.Fatalf("diagnostics=%v, want %s", diagnostics, CodeEmptyBase)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want exactly one empty-base note", diagnostics)
	}
	if !strings.Contains(diagnostics[0].Message, "immutability checks skipped") {
		t.Fatalf("message %q omitted skip notice", diagnostics[0].Message)
	}
}

func gitOutputSHA(t *testing.T, root string, ref string) string {
	t.Helper()
	command := gitcmd.Command("-C", root, "rev-parse", ref)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("rev-parse %s failed: %v", ref, err)
	}
	return strings.TrimSpace(string(output))
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Test User")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "commit", "--allow-empty", "-m", "init")
	return root
}

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	command := gitcmd.Command(append([]string{"-C", root}, args...)...)
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
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

func requirementYAML(id string) string {
	return "id: " + id + "\ntype: ubiquitous\ntext: The service shall exist.\nstatus: active\n"
}

func hasCode(diagnostics []layout.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func hasPath(diagnostics []layout.Diagnostic, path string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path {
			return true
		}
	}
	return false
}
