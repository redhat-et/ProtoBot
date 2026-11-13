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
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
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
}

func TestRunLayoutDiagnostics(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "notes.txt") {
		t.Fatalf("stderr %q omitted offending path", stderr)
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
}

func TestRunLayoutDiagnosticsNullDocument(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", "null\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
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
