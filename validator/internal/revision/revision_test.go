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

	snapshot, diagnostics := Read(root, "HEAD")
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
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

	snapshot, diagnostics := Read(root, "HEAD")
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

	snapshot, diagnostics := Read(root, "HEAD")
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
}

func TestReadUnknownRef(t *testing.T) {
	root := gitRepo(t)
	_, diagnostics := Read(root, "not-a-ref")
	if !hasCode(diagnostics, CodeNotFound) {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestReadNonGitTarget(t *testing.T) {
	_, diagnostics := Read(t.TempDir(), "HEAD")
	if !hasCode(diagnostics, CodeReadFailed) {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestReadSkipsNonYAMLEntriesInBase(t *testing.T) {
	root := gitRepo(t)
	writeFile(t, root, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	writeFile(t, root, "spec/requirements/notes.txt", "not yaml\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")

	snapshot, diagnostics := Read(root, "HEAD")
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

	_, diagnostics := Read(root, "HEAD")
	if !hasPath(diagnostics, "spec/requirements/bad.yaml") {
		t.Fatalf("diagnostics=%v", diagnostics)
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
