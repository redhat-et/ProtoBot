package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

func TestArtifactListFiltersAndSortsRegistryEntries(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	setArtifactListEntries(t, root, []records.ArtifactEntry{
		{ID: "vision", Kind: records.ArtifactVision, Path: "docs/vision.md", Owner: "user"},
		{ID: "zeta", Kind: records.ArtifactInterfaceProse, Path: "docs/zeta.md", Owner: "user"},
		{ID: "mango", Kind: records.ArtifactArchitecture, Path: "docs/mango.md", Owner: "user"},
		{ID: "alpha", Kind: records.ArtifactArchitecture, Path: "docs/alpha.md", Owner: "ears-manager"},
	})

	code, first, stderr := runCLI(nil, "--output", "json", "artifact", "list")
	assertSuccess(t, code, first, stderr)
	code, repeated, stderr := runCLI(nil, "--output", "json", "artifact", "list")
	assertSuccess(t, code, repeated, stderr)
	if first != repeated {
		t.Fatalf("repeated artifact list output differed:\n%s\n---\n%s", first, repeated)
	}
	assertArtifactListIDs(t, first, "alpha", "mango", "vision", "zeta")
	if strings.Contains(first, "\"content\"") || strings.Contains(first, "# Artifact ") {
		t.Fatalf("artifact list included artifact content: %s", first)
	}

	code, byKind, stderr := runCLI(nil, "--output", "json", "artifact", "list", "--kind", "architecture")
	assertSuccess(t, code, byKind, stderr)
	assertArtifactListIDs(t, byKind, "alpha", "mango")

	code, byOwner, stderr := runCLI(nil, "--output", "json", "artifact", "list", "--owner", "user")
	assertSuccess(t, code, byOwner, stderr)
	assertArtifactListIDs(t, byOwner, "mango", "vision", "zeta")

	code, combined, stderr := runCLI(nil, "--output", "json", "artifact", "list", "--kind", "architecture", "--owner", "ears-manager")
	assertSuccess(t, code, combined, stderr)
	assertArtifactListIDs(t, combined, "alpha")

	code, human, stderr := runCLI(nil, "artifact", "list", "--kind", "vision")
	if code != 0 || stderr != "" || !strings.HasPrefix(human, "artifact list: ok\n") || !strings.Contains(human, `"id": "vision"`) {
		t.Fatalf("human artifact list = code %d stdout %q stderr %q", code, human, stderr)
	}
}

func TestArtifactListAtUsesOnlyTheSelectedCommitTree(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	oldTree := gitOutput(t, root, "rev-parse", "HEAD")

	setArtifactListEntries(t, root, []records.ArtifactEntry{
		{ID: "vision", Kind: records.ArtifactVision, Path: "docs/vision.md", Owner: "user"},
		{ID: "zeta", Kind: records.ArtifactInterfaceProse, Path: "docs/zeta.md", Owner: "ears-manager"},
	})
	code, working, stderr := runCLI(nil, "--output", "json", "artifact", "list")
	assertSuccess(t, code, working, stderr)
	assertArtifactListIDs(t, working, "vision", "zeta")

	code, atTree, stderr := runCLI(nil, "--output", "json", "artifact", "list", "--at", oldTree)
	assertSuccess(t, code, atTree, stderr)
	assertArtifactListIDs(t, atTree, "vision")

	// The immutable read must remain available even when the working-tree
	// project configuration and registered specification files are absent.
	if err := os.RemoveAll(filepath.Join(root, ".protobot")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "docs", "vision.md")); err != nil {
		t.Fatal(err)
	}
	code, isolated, stderr := runCLI(nil, "--output", "json", "artifact", "list", "--at", oldTree)
	assertSuccess(t, code, isolated, stderr)
	if isolated != atTree {
		t.Fatalf("working-tree removal changed artifact list --at:\n%s\n---\n%s", atTree, isolated)
	}
}

func TestArtifactListRejectsMalformedAndInvalidOptions(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	head := gitOutput(t, root, "rev-parse", "HEAD")

	for _, test := range []struct {
		name   string
		args   []string
		status int
		code   string
	}{
		{name: "unknown option", args: []string{"--unknown"}, status: 2, code: "usage.invalid_request"},
		{name: "missing owner", args: []string{"--owner"}, status: 2, code: "usage.invalid_request"},
		{name: "unexpected argument", args: []string{"unexpected"}, status: 2, code: "usage.invalid_request"},
		{name: "unsupported kind", args: []string{"--kind", "unknown"}, status: 4, code: "artifact.unknown_kind"},
		{name: "unsupported owner", args: []string{"--owner", "unknown"}, status: 4, code: "artifact.invalid_owner"},
		{name: "short revision", args: []string{"--at", head[:12]}, status: 4, code: "revision.invalid"},
		{name: "unknown revision", args: []string{"--at", strings.Repeat("f", 40)}, status: 4, code: "revision.not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--output", "json", "artifact", "list"}, test.args...)
			code, stdout, stderr := runCLI(nil, args...)
			assertFailureCode(t, code, stdout, stderr, test.status, test.code)
		})
	}
}

func TestArtifactListReportsInvalidProjectConfiguration(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "main~1"
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "artifact", "list")
	assertFailureCode(t, code, stdout, stderr, 3, "project.invalid_configuration")

	if err := os.WriteFile(filepath.Join(root, ".protobot", "project.yaml"), []byte("project: [invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLI(nil, "--output", "json", "artifact", "list")
	assertFailureCode(t, code, stdout, stderr, 3, "project.invalid_configuration")
}

func TestArtifactListReportsUnreadableRegisteredArtifact(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	if err := os.Remove(filepath.Join(root, "docs", "vision.md")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(nil, "--output", "json", "artifact", "list")
	assertFailureCode(t, code, stdout, stderr, 4, "artifact.read_failed")
}

func setArtifactListEntries(t *testing.T, root string, entries []records.ArtifactEntry) {
	t.Helper()
	configPath := filepath.Join(root, ".protobot", "project.yaml")
	var config records.ProjectConfig
	if err := storage.ReadFile(configPath, &config); err != nil {
		t.Fatal(err)
	}
	config.Artifacts = make([]records.ArtifactEntry, 0, len(entries))
	for _, artifact := range entries {
		content := []byte("# Artifact " + artifact.ID + "\n")
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(artifact.Path)), content, 0o644); err != nil {
			t.Fatal(err)
		}
		digest, err := specvalidation.CanonicalTextDigest(content)
		if err != nil {
			t.Fatal(err)
		}
		artifact.Digest = digest
		config.Artifacts = append(config.Artifacts, artifact)
	}
	data, err := storage.Encode(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	projectionPath := filepath.Join(root, ".protobot", "projection.yaml")
	projection, err := os.ReadFile(projectionPath)
	if err != nil {
		t.Fatal(err)
	}
	var updated strings.Builder
	updated.Write(projection)
	for _, artifact := range entries {
		if artifact.Path == "docs/vision.md" {
			continue
		}
		updated.WriteString("  - path: " + artifact.Path + "\n    class: shared\n")
	}
	if err := os.WriteFile(projectionPath, []byte(updated.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertArtifactListIDs(t *testing.T, output string, want ...string) {
	t.Helper()
	items := jsonArray(t, output, "data", "artifacts")
	if len(items) != len(want) {
		t.Fatalf("artifact list returned %d entries, want %d: %s", len(items), len(want), output)
	}
	for index, item := range items {
		if got := jsonStringFromValue(t, item, "id"); got != want[index] {
			t.Fatalf("artifact list ID %d = %q, want %q: %s", index, got, want[index], output)
		}
	}
}
