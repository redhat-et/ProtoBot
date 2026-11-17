package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/gitcmd"
	"github.com/redhat-et/protobot/validator/internal/immutability"
	"github.com/redhat-et/protobot/validator/internal/layout"
)

func TestRunValidTargetAndBase(t *testing.T) {
	target := gitRepo(t)
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
	target := gitRepo(t)
	code, stdout, stderr := runCLI("--target="+target, "--base=main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "base: main") {
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
	dir := gitRepo(t)
	slash := dir + string(os.PathSeparator)
	link := filepath.Join(t.TempDir(), "checkout")
	symlinkErr := os.Symlink(dir, link)
	relParent := t.TempDir()
	relName := "rel-target"
	relTarget := filepath.Join(relParent, relName)
	if err := os.Mkdir(relTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, filepath.Join(relParent, relName))

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
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", completeRequirementYAML())
	writeSpecFile(t, target, "spec/interfaces/cli.yaml", completeInterfaceYAML())
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "spec")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunAcceptsAllowedRelationshipTypes(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00000.yaml", `id: REQ-API-00000
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: retired
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall log requests.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00003.yaml", `id: REQ-API-00003
type: ubiquitous
text: The service shall expose metrics.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships:
  - type: depends-on
    target: REQ-API-00001
  - type: supersedes
    target: REQ-API-00000
  - type: related-to
    target: REQ-API-00003
status: active
`)
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "spec")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunAcceptsResolvedReferences(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/interfaces/health-api.yaml", "name: health-api\nkind: http\ndescription: Health check HTTP API.\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: retired
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  interfaces:
    - health-api
relationships:
  - type: supersedes
    target: REQ-API-00001
status: active
`)
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "spec")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunRejectsDanglingReferences(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  interfaces:
    - undeclared-api
relationships:
  - type: depends-on
    target: REQ-MISSING-00001
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "undeclared-api") {
		t.Fatalf("stderr %q omitted dangling interface", stderr)
	}
	if !strings.Contains(stderr, "REQ-MISSING-00001") {
		t.Fatalf("stderr %q omitted dangling relationship target", stderr)
	}
}

func TestRunSuppressesReferenceDiagnosticWhenTargetFailsToParse(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", "id: [\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00001
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001.yaml") || !strings.Contains(stderr, "parse YAML") {
		t.Fatalf("stderr %q omitted parse error for REQ-API-00001.yaml", stderr)
	}
	if strings.Contains(stderr, "is not declared") {
		t.Fatalf("stderr %q unexpectedly reported cascading 'is not declared' for malformed target", stderr)
	}
}

func TestRunCrossStoreParseFailureDoesNotSuppressReferenceDiagnostic(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/health-api.yaml", "id: [\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", "id: [\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  interfaces:
    - health-api
relationships:
  - type: depends-on
    target: REQ-API-00001
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "spec/requirements/health-api.yaml") || !strings.Contains(stderr, "parse YAML") {
		t.Fatalf("stderr %q omitted parse error for spec/requirements/health-api.yaml", stderr)
	}
	if !strings.Contains(stderr, "spec/requirements/REQ-API-00001.yaml") || !strings.Contains(stderr, "parse YAML") {
		t.Fatalf("stderr %q omitted parse error for spec/requirements/REQ-API-00001.yaml", stderr)
	}
	if !strings.Contains(stderr, "Interface \"health-api\" at applies_to.interfaces[0] is not declared.") {
		t.Fatalf("stderr %q omitted reference.not_found diagnostic for health-api", stderr)
	}
	if strings.Contains(stderr, "Requirement \"REQ-API-00001\" at relationships[0].target is not declared.") {
		t.Fatalf("stderr %q unexpectedly reported cascading 'is not declared' for malformed target", stderr)
	}
}

func TestRunRejectsAmbiguousInterfaceReference(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/interfaces/api-a.yaml", "name: health-api\nkind: http\ndescription: Health check.\n")
	writeSpecFile(t, target, "spec/interfaces/api-b.yaml", "name: health-api\nkind: http\ndescription: Duplicate health check.\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  interfaces:
    - health-api
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "health-api") || !strings.Contains(stderr, "ambiguous") {
		t.Fatalf("stderr %q omitted ambiguous interface diagnostic", stderr)
	}
}

func TestRunEARSShapeDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "zero shall",
			content: "id: REQ-API-00002\ntype: ubiquitous\ntext: The service must return HTTP 200.\nstatus: active\n",
			want:    "contains 0 occurrences",
		},
		{
			name:    "multiple shall",
			content: "id: REQ-API-00002\ntype: ubiquitous\ntext: The service shall start and shall stop.\nstatus: active\n",
			want:    "contains 2 occurrences",
		},
		{
			name:    "type mismatch",
			content: "id: REQ-API-00002\ntype: ubiquitous\ntext: When a client sends GET /health, the service shall return HTTP 200.\nstatus: active\n",
			want:    "ubiquitous",
		},
		{
			name:    "state-driven During alias",
			content: "id: REQ-API-00002\ntype: state-driven\ntext: During maintenance, the service shall reject writes.\nstatus: active\n",
			want:    "state-driven",
		},
		{
			name:    "unrecognized type",
			content: "id: REQ-API-00002\ntype: feature\ntext: The service shall return HTTP 200.\nstatus: active\n",
			want:    "feature",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := t.TempDir()
			writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", tc.content)
			code, stdout, stderr := runCLI("--target", target, "--base", "main")
			if code != exitFailure || stdout != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "REQ-API-00002.yaml") {
				t.Fatalf("stderr %q omitted offending path", stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr %q does not contain %q", stderr, tc.want)
			}
		})
	}
}

func TestRunValidEARSTypes(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-UBI-00001.yaml", `id: REQ-UBI-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-EVT-00001.yaml", `id: REQ-EVT-00001
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-STA-00001.yaml", `id: REQ-STA-00001
type: state-driven
text: While maintenance mode is active, the service shall reject writes.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-UNW-00001.yaml", `id: REQ-UNW-00001
type: unwanted-behavior
text: If a token expires, then the service shall reject the request.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-OPT-00001.yaml", `id: REQ-OPT-00001
type: optional-feature
text: Where caching is enabled, the service shall use a cache.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-CPX-00001.yaml", `id: REQ-CPX-00001
type: complex
text: While maintenance mode is active, when a user writes, the service shall reject the request.
applies_to:
  scopes:
    - api
relationships: []
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

func TestRunValidatesRetiredRequirementsEARSShape(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-RET-00001.yaml", `id: REQ-RET-00001
type: ubiquitous
text: Invalid text without any obligation.
applies_to:
  scopes:
    - api
relationships: []
status: retired
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-RET-00001.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "contains 0 occurrences") {
		t.Fatalf("stderr %q does not contain 0 occurrences", stderr)
	}
}

func TestRunRejectsUnsupportedRelationshipTypes(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall log requests.
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00003.yaml", `id: REQ-API-00003
type: ubiquitous
text: The service shall expose metrics.
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00004.yaml", `id: REQ-API-00004
type: ubiquitous
text: The service shall rate-limit clients.
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00005.yaml", `id: REQ-API-00005
type: ubiquitous
text: The service shall record audit events.
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
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

func TestRunLayoutAndEARSDiagnosticsTogether(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", "id: REQ-API-00002\ntype: ubiquitous\ntext: When a client sends GET /health, the service shall return HTTP 200.\nstatus: active\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "notes.txt") {
		t.Fatalf("stderr %q omitted layout path", stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted EARS path", stderr)
	}
}
func TestRunTestTagReferenceResolution(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001", "active"))
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", requirementYAML("REQ-API-00002", "retired"))
	writeSpecFile(t, target, "tests/active.feature", "@req:REQ-API-00001\nFeature: Active obligation\n")
	writeSpecFile(t, target, "tests/retired.feature", "@req:REQ-API-00002\nFeature: Historical obligation\n")
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "spec")

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
applies_to:
  scopes:
    - api
relationships:
  - type: depends-on
    target: REQ-API-00002
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /ready, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
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
	target := gitRepo(t)
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
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00004.yaml", `id: REQ-API-00004
type: ubiquitous
text: The service shall rate-limit clients.
status: active
`)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
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

func TestRunRejectsForgedNestedPathKey(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
relationships[0].type: depends-on
status: active
`)
	writeSpecFile(t, target, "spec/interfaces/health-api.yaml", "name: health-api\nkind: http\ndescription: Health endpoint\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want exitFailure", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
}

func TestRunLayoutDiagnosticsNullDocument(t *testing.T) {
	target := gitRepo(t)
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
	target := gitRepo(t)
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
	target := gitRepo(t)
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
	wantStderr := "validator: layout.unexpected_file: spec/requirements/\"a\\n::error::injected.yaml\": has an unsafe name\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestRunTestTagDiagnosticsFilenameWithControlChar(t *testing.T) {
	target := gitRepo(t)
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
	wantStderr := "validator: testtags.unresolved: tests/a ::error::injected.feature:1: @req tag references unknown requirement ID \"REQ-UNKNOWN\"\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestPrintDiagnosticSanitizesLineBreaks(t *testing.T) {
	var buf bytes.Buffer
	printDiagnostic(&buf, "relationship.\ninvalid_type", "spec/requirements/a\nb.yaml", "unsupported relationship type\nmulti line\r\nmessage")
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), got)
	}
	want := "validator: relationship. invalid_type: spec/requirements/a b.yaml: unsupported relationship type multi line message\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	buf.Reset()
	printDiagnostic(&buf, "revision.\nnot_found", "", "comparison base not found\nerror details")
	gotPathless := buf.String()
	pathlessLines := strings.Split(strings.TrimRight(gotPathless, "\n"), "\n")
	if len(pathlessLines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(pathlessLines), gotPathless)
	}
	wantPathless := "validator: revision. not_found: comparison base not found error details\n"
	if gotPathless != wantPathless {
		t.Fatalf("got %q, want %q", gotPathless, wantPathless)
	}
}

// TestRunIsDeterministic pins in-process repeatability of Run: the
// command reads no clock, environment, random source, or shared
// mutable state. It is not a cross-process isolation check.
func TestRunIsDeterministic(t *testing.T) {
	target := gitRepo(t)
	code1, stdout1, stderr1 := runCLI("--target", target, "--base", "main")
	code2, stdout2, stderr2 := runCLI("--target", target, "--base", "main")
	if code1 != code2 || stdout1 != stdout2 || stderr1 != stderr2 {
		t.Fatalf("repeated invocation differed: (%d, %q, %q) vs (%d, %q, %q)", code1, stdout1, stderr1, code2, stdout2, stderr2)
	}
}

func TestRunEARSDiagnosticsAreDeterministic(t *testing.T) {
	target := t.TempDir()
	writeSpecFile(t, target, "spec/requirements/REQ-Z-00001.yaml", "id: REQ-Z-00001\ntype: ubiquitous\ntext: When a client acts, the service shall respond.\nstatus: active\n")
	writeSpecFile(t, target, "spec/requirements/REQ-A-00001.yaml", "id: REQ-A-00001\ntype: feature\ntext: no obligation\nstatus: active\n")

	code1, stdout1, stderr1 := runCLI("--target", target, "--base", "main")
	code2, stdout2, stderr2 := runCLI("--target", target, "--base", "main")
	if code1 != exitFailure || code1 != code2 || stdout1 != stdout2 || stderr1 != stderr2 {
		t.Fatalf("repeated invocation differed: (%d, %q, %q) vs (%d, %q, %q)", code1, stdout1, stderr1, code2, stdout2, stderr2)
	}
	aIndex := strings.Index(stderr1, "REQ-A-00001.yaml")
	zIndex := strings.Index(stderr1, "REQ-Z-00001.yaml")
	if aIndex < 0 || zIndex < 0 || aIndex > zIndex {
		t.Fatalf("stderr %q is not sorted by path", stderr1)
	}
}

func TestRunUnchangedTextPasses(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAMLWithText("REQ-API-00001", "The service shall exist."))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunYAMLFormattingVariationPasses(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: event-driven
text: >-
  When a client sends GET /health, the service
  shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunModifiedRequirementTextFails(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAMLWithText("REQ-API-00001", "The service shall exist."))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAMLWithText("REQ-API-00001", "The service shall exist now."))

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, immutability.CodeTextImmutable) {
		t.Fatalf("stderr %q omitted %s", stderr, immutability.CodeTextImmutable)
	}
	if !strings.Contains(stderr, "REQ-API-00001") {
		t.Fatalf("stderr %q omitted requirement id", stderr)
	}
}

func TestRunReportsEarlierDiagnosticsAndTextImmutabilityTogether(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAMLWithText("REQ-API-00001", "The service shall exist."))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")

	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAMLWithText("REQ-API-00001", "The service shall exist now."))

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "notes.txt") {
		t.Fatalf("stderr %q omitted layout diagnostic", stderr)
	}
	if !strings.Contains(stderr, immutability.CodeTextImmutable) {
		t.Fatalf("stderr %q omitted text immutability diagnostic", stderr)
	}
}

func TestReportDiagnosticsDelegatesToPrintDiagnostic(t *testing.T) {
	var buf bytes.Buffer
	reportDiagnostics(&buf, []layout.Diagnostic{
		{Code: "test.code", Path: "spec/requirements/a\nb.yaml", Message: "multi\nline"},
		{Code: "test.pathless", Path: "", Message: "message\r\ntext"},
	})
	got := buf.String()
	want := "validator: test.code: spec/requirements/a b.yaml: multi line\nvalidator: test.pathless: message text\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunMetadataOnlyTransitionPasses(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: retired
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunNewlyAddedIDsPass(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAMLWithText("REQ-API-00001", "The service shall exist."))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", requirementYAMLWithText("REQ-API-00002", "The service shall log health checks."))

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
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

func TestRunRejectsMissingRequiredFields(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", "id: REQ-API-00002\n")
	writeSpecFile(t, target, "spec/interfaces/health-api.yaml", "name: health-api\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, field := range []string{"type", "text", "applies_to", "relationships", "status", "kind", "description"} {
		if !strings.Contains(stderr, field) {
			t.Errorf("stderr %q omitted missing field %q", stderr, field)
		}
	}
}

func TestRunRejectsInvalidStatus(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships: []
status: implemented
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "implemented") {
		t.Fatalf("stderr %q omitted invalid status", stderr)
	}
}

func TestRunRejectsLegacyMetadata(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", completeRequirementYAML()+`verification:
  mode: isolated-interface
provenance: user-authored
created: "2026-01-01T00:00:00Z"
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, field := range []string{"verification", "provenance", "created"} {
		if !strings.Contains(stderr, field) {
			t.Errorf("stderr %q omitted legacy field %q", stderr, field)
		}
	}
}

func TestRunReportsBothLayoutAndSchemaDiagnostics(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/notes.txt", "not yaml\n")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
status: draft
applies_to:
  scopes:
    - api
relationships: []
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "notes.txt") {
		t.Fatalf("stderr %q omitted layout diagnostic path", stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00002.yaml") {
		t.Fatalf("stderr %q omitted schema diagnostic path", stderr)
	}
	if !strings.Contains(stderr, "draft") {
		t.Fatalf("stderr %q omitted invalid status", stderr)
	}
}

func TestRunRejectsInvalidRequirementBasename(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/notes.yaml", requirementYAML("REQ-CORE-00001"))

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	wantPrefix := "validator: layout.invalid_basename: spec/requirements/notes.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
}

func TestRunRejectsRequirementIDBasenameMismatch(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-CORE-00001.yaml", requirementYAML("REQ-CORE-00002"))

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	wantPrefix := "validator: spec.id_basename_mismatch: spec/requirements/REQ-CORE-00001.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
	if !strings.Contains(stderr, "REQ-CORE-00002") {
		t.Fatalf("stderr %q omitted in-record id", stderr)
	}
}

func TestRunRejectsInterfaceBasenameMismatch(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/interfaces/service.yaml", "name: billing-api\nkind: http\ndescription: Billing API.\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	wantPrefix := "validator: layout.invalid_basename: spec/interfaces/service.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
	if !strings.Contains(stderr, "billing-api") {
		t.Fatalf("stderr %q omitted declared name", stderr)
	}
}

func TestRunAcceptsHyphenatedRequirementScopeBasename(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-CORE-X-00001.yaml", requirementYAML("REQ-CORE-X-00001"))
	writeSpecFile(t, target, "spec/interfaces/billing-api.yaml", "name: billing-api\nkind: http\ndescription: Billing API.\n")
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "spec")

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunRejectsMalformedRequirementID(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/bad.yaml", `id: REQ-api-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "bad.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "REQ-api-00001") {
		t.Fatalf("stderr %q omitted malformed id", stderr)
	}
}

func TestRunRejectsDuplicateRequirementIDs(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	writeSpecFile(t, target, "spec/requirements/copy.yaml", requirementYAML("REQ-API-00001"))

	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001.yaml") || !strings.Contains(stderr, "copy.yaml") {
		t.Fatalf("stderr %q omitted conflicting paths", stderr)
	}
}

func TestRunRejectsDeletedRequirementID(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	if err := os.Remove(filepath.Join(target, "spec", "requirements", "REQ-API-00001.yaml")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001") {
		t.Fatalf("stderr %q omitted deleted id", stderr)
	}
	if !strings.Contains(strings.ToLower(stderr), "deleted") && !strings.Contains(stderr, "Retire") {
		t.Fatalf("stderr %q omitted deletion guidance", stderr)
	}
}

func TestRunAllowsRetiredRequirementID(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: retired
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunRejectsActiveSupersedesTarget(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: ubiquitous
text: The service shall log requests.
applies_to:
  scopes:
    - api
relationships:
  - type: supersedes
    target: REQ-API-00001
status: active
`)
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "spec")

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	wantPrefix := "validator: relationship.superseded_requirement_active: spec/requirements/REQ-API-00002.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
	if !strings.Contains(stderr, "REQ-API-00001") {
		t.Fatalf("stderr %q omitted predecessor id", stderr)
	}
}

func TestRunAcceptsReplacementThatRetiresPredecessor(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001", "retired"))
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: ubiquitous
text: The service shall log requests.
applies_to:
  scopes:
    - api
relationships:
  - type: supersedes
    target: REQ-API-00001
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunRejectsReplacementOfAlreadyRetiredPredecessor(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001", "retired"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: ubiquitous
text: The service shall log requests.
applies_to:
  scopes:
    - api
relationships:
  - type: supersedes
    target: REQ-API-00001
status: active
`)

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	wantPrefix := "validator: relationship.superseded_requirement_not_in_change: spec/requirements/REQ-API-00002.yaml:"
	if !strings.Contains(stderr, wantPrefix) {
		t.Fatalf("stderr %q omitted %q", stderr, wantPrefix)
	}
	if !strings.Contains(stderr, "REQ-API-00001") {
		t.Fatalf("stderr %q omitted predecessor id", stderr)
	}
}

func TestRunRejectsReusedRequirementID(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "base")
	if err := os.Remove(filepath.Join(target, "spec", "requirements", "REQ-API-00001.yaml")); err != nil {
		t.Fatal(err)
	}
	writeSpecFile(t, target, "spec/requirements/other.yaml", requirementYAML("REQ-API-00001"))

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "other.yaml") {
		t.Fatalf("stderr %q omitted reused path", stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001") {
		t.Fatalf("stderr %q omitted reused id", stderr)
	}
}

func TestRunUnknownBaseFails(t *testing.T) {
	target := gitRepo(t)
	code, stdout, stderr := runCLI("--target", target, "--base", "not-a-ref")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "not-a-ref") {
		t.Fatalf("stderr %q omitted unknown base", stderr)
	}
}

func TestRunRepairedBaseMalformedYAMLExitsZero(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-FIX-00001.yaml", "id: [\n")
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "commit with malformed base yaml")

	writeSpecFile(t, target, "spec/requirements/REQ-FIX-00001.yaml", requirementYAML("REQ-FIX-00001"))

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitOK {
		t.Fatalf("code=%d, want %d; stderr=%q", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
	if !strings.Contains(stderr, "comparison base") {
		t.Fatalf("stderr %q omitted comparison base prefix for historical malformed file", stderr)
	}
}

func TestRunDeletedBaseMalformedYAMLFailsClosed(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/broken.yaml", "id: [\n")
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "commit with malformed base yaml")

	if err := os.Remove(filepath.Join(target, "spec", "requirements", "broken.yaml")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "broken.yaml") || !strings.Contains(stderr, "removed") {
		t.Fatalf("stderr %q omitted deletion diagnostic for unparsed base file", stderr)
	}
}

func TestRunRepairedBaseInvalidAndDuplicateIDsExitsZero(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-FIX-00001.yaml", requirementYAML("not-an-id"))
	writeSpecFile(t, target, "spec/requirements/REQ-DUP-00001.yaml", requirementYAML("REQ-DUP-00001"))
	writeSpecFile(t, target, "spec/requirements/REQ-DUP-00002.yaml", requirementYAML("REQ-DUP-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "commit with bad and duplicate ids")

	writeSpecFile(t, target, "spec/requirements/REQ-FIX-00001.yaml", requirementYAML("REQ-FIX-00001"))
	if err := os.Remove(filepath.Join(target, "spec", "requirements", "REQ-DUP-00002.yaml")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitOK {
		t.Fatalf("code=%d, want %d; stderr=%q", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("stdout %q does not report ok", stdout)
	}
}

func TestRunDeletedBaseInvalidIDFails(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/bad.yaml", requirementYAML("REQ-api-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "commit with invalid base id")

	if err := os.Remove(filepath.Join(target, "spec", "requirements", "bad.yaml")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "bad.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "REQ-api-00001") {
		t.Fatalf("stderr %q omitted deleted invalid id", stderr)
	}
	if !strings.Contains(strings.ToLower(stderr), "deleted") && !strings.Contains(stderr, "Retire") {
		t.Fatalf("stderr %q omitted deletion guidance", stderr)
	}
}

func TestRunMalformedTargetFileDoesNotEmitRequirementDeleted(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "commit valid requirement")

	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", "id: [\n")

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001.yaml") {
		t.Fatalf("stderr %q omitted parse failure path", stderr)
	}
	if strings.Contains(stderr, "was deleted") || strings.Contains(stderr, "Retire the record") {
		t.Fatalf("stderr %q emitted false-positive deletion diagnostic for malformed file", stderr)
	}
}

func TestRunInPlaceIDEditRejectsDeletion(t *testing.T) {
	target := gitRepo(t)
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "commit valid requirement")

	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00002"))

	code, stdout, stderr := runCLI("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001.yaml") {
		t.Fatalf("stderr %q omitted offending path", stderr)
	}
	if !strings.Contains(stderr, "REQ-API-00001") {
		t.Fatalf("stderr %q omitted deleted id", stderr)
	}
	if !strings.Contains(strings.ToLower(stderr), "deleted") && !strings.Contains(stderr, "Retire") {
		t.Fatalf("stderr %q omitted deletion guidance", stderr)
	}
}

func TestRunNonGitCheckoutFails(t *testing.T) {
	target := t.TempDir()
	code, stdout, stderr := runCLI("--target", target, "--base", "main")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "git checkout") {
		t.Fatalf("stderr %q omitted git checkout diagnostic", stderr)
	}
}

func completeRequirementYAML() string {
	return `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships: []
status: active
`
}

func completeInterfaceYAML() string {
	return "name: cli\nkind: cli\ndescription: Validator command line.\n"
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

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	initGitRepo(t, root)
	return root
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Test User")
	git(t, root, "config", "commit.gpgsign", "false")
	git(t, root, "commit", "--allow-empty", "-m", "init")
}

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	command := gitcmd.Command(append([]string{"-C", root}, args...)...)
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func requirementYAML(id string, status ...string) string {
	st := "active"
	if len(status) > 0 && status[0] != "" {
		st = status[0]
	}
	return "id: " + id + "\ntype: ubiquitous\ntext: The service shall exist.\napplies_to:\n  scopes:\n    - api\nrelationships: []\nstatus: " + st + "\n"
}

func requirementYAMLWithText(id, text string) string {
	return "id: " + id + "\ntype: ubiquitous\ntext: " + text + "\napplies_to:\n  scopes:\n    - api\nrelationships: []\nstatus: active\n"
}
