package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunValidTargetAndBase(t *testing.T) {
	target := t.TempDir()
	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
	if !strings.Contains(stdout, "target: "+target) {
		t.Fatalf("stdout %q does not include target", stdout)
	}
	if !strings.Contains(stdout, "base: main") {
		t.Fatalf("stdout %q does not include base", stdout)
	}
}

func TestRunEqualsForm(t *testing.T) {
	target := t.TempDir()
	code, stdout, stderr := runCLI("--target="+target, "--base=origin/main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "base: origin/main") {
		t.Fatalf("stdout %q does not include equals-form base", stdout)
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--help", "--bogus"}} {
		code, stdout, stderr := runCLI(args...)
		if code != exitOK || stderr != "" {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "--target") || !strings.Contains(stdout, "--base") {
			t.Fatalf("help stdout %q omitted flag names", stdout)
		}
		if !strings.Contains(stdout, "validator -h") {
			t.Fatalf("help stdout %q omitted -h alias", stdout)
		}
	}
}

func TestRunNoArgumentsShowsUsage(t *testing.T) {
	code, stdout, stderr := runCLI()
	if code != exitUsage || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr %q omitted usage", stderr)
	}
	if !strings.Contains(stderr, "--target") || !strings.Contains(stderr, "--base") {
		t.Fatalf("stderr %q omitted flag names", stderr)
	}
	if !strings.Contains(stderr, "--target is required") {
		t.Fatalf("stderr %q omitted --target requirement", stderr)
	}
}

func TestCommandBuilds(t *testing.T) {
	out := filepath.Join(t.TempDir(), "validator")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/validator")
	cmd.Dir = moduleRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/validator failed: %v\n%s", err, output)
	}
}

func TestRunMissingBase(t *testing.T) {
	code, _, stderr := runCLI("--target", t.TempDir())
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "--base is required") {
		t.Fatalf("stderr %q omitted --base requirement", stderr)
	}
}

func TestRunMalformedFlags(t *testing.T) {
	target := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown option", args: []string{"--unknown"}, want: "unexpected argument"},
		{name: "positional", args: []string{target, "main"}, want: "unexpected argument"},
		{name: "target without value", args: []string{"--target"}, want: "--target takes a directory path"},
		{name: "base without value", args: []string{"--target", target, "--base"}, want: "--base takes a comparison-base argument"},
		{name: "empty target", args: []string{"--target=", "--base", "main"}, want: "--target takes a directory path"},
		{name: "empty base", args: []string{"--target", target, "--base="}, want: "--base takes a comparison-base argument"},
		{name: "duplicate target", args: []string{"--target", target, "--target", target, "--base", "main"}, want: "--target may be specified once"},
		{name: "duplicate base", args: []string{"--target", target, "--base", "main", "--base", "other"}, want: "--base may be specified once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)
			if code != exitUsage || stdout != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr %q does not contain %q", stderr, tc.want)
			}
		})
	}
}

func TestRunFlagLikeValuesRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "flag consumed as target value",
			args: []string{"--target", "--base", "main"},
			want: "--target takes a directory path",
		},
		{
			name: "double-dash consumed as target value",
			args: []string{"--target", "--", "--base", "main"},
			want: "--target takes a directory path",
		},
		{
			name: "flag consumed as base value",
			args: []string{"--target", t.TempDir(), "--base", "--target"},
			want: "--base takes a comparison-base argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)
			if code != exitUsage || stdout != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q; want exitUsage", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr %q does not contain %q", stderr, tc.want)
			}
		})
	}
}

func TestRunAcceptedTargetForms(t *testing.T) {
	dir := t.TempDir()
	slash := dir + string(os.PathSeparator)
	link := filepath.Join(t.TempDir(), "checkout")
	symlinkErr := os.Symlink(dir, link)
	relParent := t.TempDir()
	relName := "rel-target"
	if err := os.Mkdir(filepath.Join(relParent, relName), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		chdir  string
		args   []string
		target string
	}{
		{
			name:   "base before target",
			args:   []string{"--base", "main", "--target", dir},
			target: dir,
		},
		{
			name:   "trailing slash",
			args:   []string{"--target", slash, "--base", "main"},
			target: slash,
		},
		{
			name:   "directory symlink",
			args:   []string{"--target", link, "--base", "main"},
			target: link,
		},
		{
			name:   "relative path",
			chdir:  relParent,
			args:   []string{"--target", relName, "--base", "main"},
			target: relName,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "directory symlink" && symlinkErr != nil {
				t.Skipf("Symlink is unavailable: %v", symlinkErr)
			}
			if tc.chdir != "" {
				t.Chdir(tc.chdir)
			}
			code, stdout, stderr := runCLI(tc.args...)
			if code != exitOK || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stdout, "target: "+tc.target) {
				t.Fatalf("stdout %q does not include target %q", stdout, tc.target)
			}
			if !strings.Contains(stdout, "base: main") {
				t.Fatalf("stdout %q does not include base", stdout)
			}
		})
	}
}

func TestRunStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not portable")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can stat mode 000 directories")
	}
	parent := t.TempDir()
	blocked := filepath.Join(parent, "blocked")
	target := filepath.Join(blocked, "checkout")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := os.Stat(target); err == nil {
		t.Skip("filesystem allows stating through mode 000 directories")
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "cannot stat target path") {
		t.Fatalf("stderr %q omitted stat-error message", stderr)
	}
}

func TestRunMissingTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	code, stdout, stderr := runCLI("--target", missing, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "does not exist") {
		t.Fatalf("stderr %q omitted missing-path message", stderr)
	}
}

func TestRunTargetIsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI("--target", path, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "is not a directory") {
		t.Fatalf("stderr %q omitted directory message", stderr)
	}
}

func TestRunValidSpecRecords(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
status: active
`)
	writeSpecFile(t, target, "spec/interfaces/cli.yaml", "name: cli\nkind: cli\ndescription: Validator command line.\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunAcceptsAllowedRelationshipTypes(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00001
  - type: supersedes
    target: REQ-API-00000
  - type: related-to
    target: REQ-API-00003
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunRejectsUnsupportedRelationshipTypes(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00001
  - type: conflicts-with
    target: REQ-API-00004
  - type: related-to
    target: REQ-API-00003
  - type: blocks
    target: REQ-API-00005
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "conflicts-with") {
		t.Fatalf("stderr %q omitted legacy type", stderr)
	}
	if !strings.Contains(stderr, "blocks") {
		t.Fatalf("stderr %q omitted undeclared type", stderr)
	}
	wantPrefix := "validator: relationship.invalid_type: spec/requirements/REQ-API-00002.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
}

func TestRunTestTagReferenceResolution(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001", "active"))
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", requirementYAML("REQ-API-00002", "retired"))
	writeSpecFile(t, target, "tests/active.feature", "@req:REQ-API-00001\nFeature: Active obligation\n")
	writeSpecFile(t, target, "tests/retired.feature", "@req:REQ-API-00002\nFeature: Historical obligation\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("valid tags code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}

	writeSpecFile(t, target, "tests/unknown.feature", "@req:REQ-UNKNOWN\nFeature: Unknown identifier\n")
	writeSpecFile(t, target, "tests/misspelled.feature", "@req:REQ-API-0000l\nFeature: Misspelled identifier\n")

	code, stdout, stderr = runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("unresolved tags code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "validator: testtags.unresolved: tests/unknown.feature:1:") {
		t.Fatalf("stderr %q omitted testtags.unresolved code for unknown tag", stderr)
	}
	if !strings.Contains(stderr, "REQ-UNKNOWN") {
		t.Fatalf("stderr %q omitted unknown requirement ID", stderr)
	}
	if !strings.Contains(stderr, "validator: testtags.unresolved: tests/misspelled.feature:1:") {
		t.Fatalf("stderr %q omitted testtags.unresolved code for misspelled tag", stderr)
	}
	if !strings.Contains(stderr, "REQ-API-0000l") {
		t.Fatalf("stderr %q omitted misspelled requirement ID", stderr)
	}
}

func TestRunRejectsRelationshipCycles(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00002
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /ready, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00001
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001") || !strings.Contains(stderr, "REQ-API-00002") {
		t.Fatalf("stderr %q omitted participating requirement IDs", stderr)
	}
	if !strings.Contains(stderr, "depends-on") {
		t.Fatalf("stderr %q omitted depends-on", stderr)
	}
}

func TestRunLayoutDiagnostics(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "layout.unexpected_file") {
		t.Fatalf("stderr %q omitted diagnostic code", stderr)
	}
	if !strings.Contains(stderr, "notes.txt") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	wantPrefix := "validator: layout.unexpected_file: spec/requirements/notes.txt:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
}

func TestRunReportsBothLayoutAndRelationshipDiagnostics(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: conflicts-with
    target: REQ-API-00004
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "notes.txt") {
		t.Fatalf("stderr %q omitted layout diagnostic path", stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted relationship diagnostic path", stderr)
	}
	if !strings.Contains(stderr, "conflicts-with") {
		t.Fatalf("stderr %q omitted legacy type", stderr)
	}
	wantLayoutPrefix := "validator: layout.unexpected_file: spec/requirements/notes.txt:"
	if !strings.Contains(stderr, wantLayoutPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantLayoutPrefix)
	}
	wantRelPrefix := "validator: relationship.invalid_type: spec/requirements/REQ-API-00002.yaml:"
	if !strings.Contains(stderr, wantRelPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantRelPrefix)
	}
}

func TestRunLayoutDiagnosticsNullDocument(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", "null\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	wantPrefix := "validator: layout.parse_failed: spec/requirements/REQ-API-00002.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
}

func TestRunLayoutDiagnosticsMultilineYAMLFlattened(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", "id: [1]\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr had %d lines, want 1: %q", len(lines), stderr)
	}
	wantPrefix := "validator: layout.parse_failed: spec/requirements/REQ-API-00001.yaml:"
	if !strings.HasPrefix(lines[0], wantPrefix) {
		t.Fatalf("stderr line %q omitted %q", lines[0], wantPrefix)
	}
}

func TestRunLayoutDiagnosticsFilenameWithControlChar(t *testing.T) {
	target := t.TempDir()
	reqDir := filepath.Join(target, "spec", "requirements")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	injectedName := "a\n::error::injected.yaml"
	filePath := filepath.Join(reqDir, injectedName)
	if err := os.WriteFile(filePath, []byte("status: active\n"), 0o600); err != nil {
		t.Skipf("filesystem does not support newlines in filenames: %v", err)
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr had %d lines, want 1: %q", len(lines), stderr)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "::") {
			t.Fatalf("stderr line starts with workflow command: %q", line)
		}
		if !strings.HasPrefix(line, "validator: layout.unexpected_file:") {
			t.Fatalf("stderr line has unexpected prefix: %q", line)
		}
	}
}

func TestRunTestTagDiagnosticsFilenameWithControlChar(t *testing.T) {
	target := t.TempDir()
	testsDir := filepath.Join(target, "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	injectedName := "a\n::error::injected.feature"
	filePath := filepath.Join(testsDir, injectedName)
	if err := os.WriteFile(filePath, []byte("@req:REQ-UNKNOWN\nFeature: Injected\n"), 0o600); err != nil {
		t.Skipf("filesystem does not support newlines in filenames: %v", err)
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr had %d lines, want 1: %q", len(lines), stderr)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "::") {
			t.Fatalf("stderr line starts with workflow command: %q", line)
		}
		if !strings.HasPrefix(line, "validator: testtags.unresolved:") {
			t.Fatalf("stderr line has unexpected prefix: %q", line)
		}
	}
}

func TestPrintDiagnosticSanitizesLineBreaks(t *testing.T) {
	var buf bytes.Buffer
	printDiagnostic(&buf, "relationship.invalid_type", "spec/requirements/a\nb.yaml", "unsupported relationship type\nmulti line\r\nmessage")
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), got)
	}
	want := "validator: relationship.invalid_type: spec/requirements/a b.yaml: unsupported relationship type multi line message\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRunIsDeterministic pins in-process repeatability of Run: the
// command reads no clock, environment, random source, or shared
// mutable state. It is not a cross-process isolation check.
func TestRunIsDeterministic(t *testing.T) {
	target := t.TempDir()
	code1, stdout1, stderr1 := runCLI("--target", target, "--base", "main")
	code2, stdout2, stderr2 := runCLI("--target", target, "--base", "main")
	if code1 != code2 || stdout1 != stdout2 || stderr1 != stderr2 {
		t.Fatalf("repeated invocation differed: (%d, %q, %q) vs (%d, %q, %q)", code1, stdout1, stderr1, code2, stdout2, stderr2)
	}
}

func TestModuleDoesNotDependOnNetworkPackages(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./...")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list -deps failed: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("go list -deps failed: %v", err)
	}
	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg = strings.TrimSpace(pkg)
		if pkg == "net" || strings.HasPrefix(pkg, "net/") {
			t.Errorf("validator depends on network package %s", pkg)
		}
	}
}

func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func writeSpecFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requirementYAML(id, status string) string {
	return "id: " + id + "\ntype: ubiquitous\ntext: The service shall exist.\nstatus: " + status + "\n"
}
