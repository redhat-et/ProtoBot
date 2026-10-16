package guard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type guardVector struct {
	Name                  string         `json:"name"`
	Role                  string         `json:"role"`
	ToolName              string         `json:"tool_name"`
	ToolInput             map[string]any `json:"tool_input"`
	Allow                 bool           `json:"allow"`
	Reason                string         `json:"reason"`
	NoProject             bool           `json:"no_project"`
	RequirementsStore     string         `json:"requirements_store"`
	FilesFromStoreSymlink bool           `json:"files_from_store_symlink"`
}

func TestGuardVectors(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	data, err := os.ReadFile("testdata/guard-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []guardVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			fixture := newFixture(t, !vector.NoProject, false)
			setupShellWriterVectorFiles(t, fixture)
			if vector.RequirementsStore != "" {
				setRequirementsStore(t, fixture, vector.RequirementsStore)
			}
			if vector.FilesFromStoreSymlink {
				setupRsyncFilesFromStoreSymlink(t, fixture, vector.RequirementsStore)
			}
			result := fixture.call(vector.Role, vector.ToolName, vector.ToolInput)
			if result.allow != vector.Allow {
				t.Fatalf("decision = %+v; allow=%v", result, vector.Allow)
			}
			if vector.Reason != "" && !strings.Contains(result.reason, vector.Reason) {
				t.Fatalf("reason %q does not contain %q", result.reason, vector.Reason)
			}
		})
	}
}

func setupShellWriterVectorFiles(t *testing.T, fixture fixture) {
	t.Helper()
	for _, directory := range []string{"out", "dir", "src/nested", "hlink-tree/nested"} {
		if err := os.MkdirAll(filepath.Join(fixture.root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"dir/payload", "src/nested/payload"} {
		if err := os.WriteFile(filepath.Join(fixture.root, path), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../../.git/HEAD", filepath.Join(fixture.root, "hlink-tree", "nested", "protected-link")); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"dirlink":            "docs",
		"flink":              "docs/vision.md",
		"protected-dirlink":  "../.git",
		"protected-filelink": "../.git/HEAD",
		"file-dest-link":     ".git/HEAD",
	} {
		if err := os.Symlink(target, filepath.Join(fixture.root, link)); err != nil {
			t.Fatal(err)
		}
	}
}

func setRequirementsStore(t *testing.T, fixture fixture, path string) {
	t.Helper()
	projectPath := filepath.Join(fixture.root, ".protobot", "project.yaml")
	project, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	project = []byte(strings.Replace(string(project), "requirements: .protobot/requirements", "requirements: "+path, 1))
	if err := os.MkdirAll(filepath.Join(fixture.root, filepath.Dir(path)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, project, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setupRsyncFilesFromStoreSymlink(t *testing.T, fixture fixture, storePath string) {
	t.Helper()
	store := filepath.Join(fixture.root, storePath)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "member.yaml"), []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(fixture.root, "src", "requirements")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "member.yaml"), []byte("overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(fixture.root, "out", "requirements")
	target, err := filepath.Rel(filepath.Dir(destination), store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "entries"), []byte("requirements/member.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEarsManagerRemainsGovernedWhenManifestOmitsCommand(t *testing.T) {
	fixture := newFixture(t, true, false)
	manifest := strings.Replace(manifestYAML, "  - ears-manager\n", "", 1)
	if err := os.WriteFile(filepath.Join(fixture.root, ".agents", "drafting-table.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	result := fixture.call(roleOther, "bash", map[string]any{"command": "ears-manager requirement add"})
	if result.allow || !strings.Contains(result.reason, "ears-manager writes are reserved") {
		t.Fatalf("manifest omission bypassed governed command protection: %+v", result)
	}
}

func TestApplyPatchUsesOpenCodeToolNameAndTargets(t *testing.T) {
	fixture := newFixture(t, true, false)
	patch := "*** Begin Patch\n*** Update File: docs/vision.md\n@@\n-old\n+new\n*** End Patch"
	result := fixture.call(roleOther, "apply_patch", map[string]any{"patchText": patch})
	if result.allow || !strings.Contains(result.reason, "registered artifact") {
		t.Fatalf("decision = %+v", result)
	}
	patch = "*** Begin Patch\n*** Add File: notes/new.txt\n+note\n*** End Patch"
	if result := fixture.call(roleOther, "apply_patch", map[string]any{"patchText": patch}); !result.allow {
		t.Fatalf("unregistered patch decision = %+v", result)
	}
	if result := fixture.call(roleDraftingTable, "apply_patch", map[string]any{"patchText": patch}); result.allow {
		t.Fatalf("Drafting Table patch decision = %+v", result)
	}
}

func TestApplyPatchPathsUseProjectRootFromSubdirectory(t *testing.T) {
	fixture := newFixture(t, true, false)
	subdirectory := filepath.Join(fixture.root, "packages", "nested")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"patchText": "*** Begin Patch\n*** Update File: docs/vision.md\n@@\n-old\n+new\n*** End Patch"}
	result := fixture.callAt(subdirectory, roleOther, "apply_patch", patch)
	if result.allow || !strings.Contains(result.reason, "registered artifact") {
		t.Fatalf("subdirectory apply_patch decision = %+v", result)
	}
	result = fixture.callAt(subdirectory, roleOther, "write", map[string]any{"filePath": "docs/vision.md"})
	if !result.allow {
		t.Fatalf("cwd-relative write decision = %+v", result)
	}

	// artifact list succeeds, then change-set list fails. The known registered
	// artifact must remain guarded for other roles during fail-closed handling.
	t.Setenv("FAKE_EARS_CHANGESET_FAIL", "1")
	result = fixture.callAt(subdirectory, roleOther, "apply_patch", patch)
	if result.allow || !strings.Contains(result.reason, "registered artifact") {
		t.Fatalf("inspection-failure apply_patch decision = %+v", result)
	}
}

func TestQuotedTildeRedirectionFailsClosedForRegisteredRelativePath(t *testing.T) {
	fixture := newFixture(t, true, false)
	t.Setenv("FAKE_ARTIFACT_TILDE", "1")
	registered := filepath.Join(fixture.root, "~", "docs")
	if err := os.MkdirAll(registered, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registered, "vision.md"), []byte("vision"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := fixture.call(roleOther, "bash", map[string]any{"command": "echo x > '~/docs/vision.md'"})
	if result.allow || !strings.Contains(result.reason, "redirection target could not be inspected safely") {
		t.Fatalf("quoted tilde redirect decision = %+v", result)
	}
}

func TestEarsManagerArtifactPathCannotEscapeThroughSymlink(t *testing.T) {
	fixture := newFixture(t, true, false)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(fixture.root, "docs", "escape")); err != nil {
		t.Fatal(err)
	}
	command := "ears-manager --output json artifact put --change-set CS-00003 --id vision --kind vision --path docs/escape/content.md --owner user --content-stdin <<'EOF'\ncontent\nEOF"
	result := fixture.call(roleDraftingTable, "bash", map[string]any{"command": command})
	if result.allow || !strings.Contains(result.reason, "escape the project root") {
		t.Fatalf("artifact symlink escape decision = %+v", result)
	}
}

func TestRegisteredDirectoryProtectsDescendants(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "specification")
	state := projectState{
		root:      root,
		artifacts: []artifact{{ID: "bundle", Path: directory, IsDirectory: true}},
	}
	if !guardedWritePath(state, filepath.Join(directory, "nested", "file.md")) {
		t.Fatal("registered directory descendant was not guarded")
	}
}

func TestOtherRoleShellInspectionLimitsFailClosed(t *testing.T) {
	fixture := newFixture(t, true, false)
	oversized := strings.Repeat("x", maxShellCommandBytes+1)
	result := fixture.call(roleOther, "bash", map[string]any{"command": oversized})
	if result.allow || !strings.Contains(result.reason, "1 MiB") {
		t.Fatalf("oversized shell command decision = %+v", result)
	}

	nested := "echo safe"
	for range maxShellAnalysisDepth + 1 {
		nested = "bash -c " + shellSingleQuote(nested)
	}
	result = fixture.call(roleOther, "bash", map[string]any{"command": nested})
	if result.allow {
		t.Fatalf("over-depth nested shell decision = %+v", result)
	}

	continued := strings.Join([]string{"echo safe;", "ears-manager requirement add"}, "\\\n")
	result = fixture.call(roleOther, "bash", map[string]any{"command": continued})
	if result.allow {
		t.Fatalf("backslash-newline continuation decision = %+v", result)
	}
}

func TestLinkedWorktreeGitMetadataPathsAreProtected(t *testing.T) {
	fixture := newFixture(t, true, false)
	worktree := filepath.Join(t.TempDir(), "linked-worktree")
	command := exec.Command("git", "-C", fixture.root, "worktree", "add", "--detach", worktree, "HEAD")
	command.Env = safeCommandEnv()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create linked worktree: %v: %s", err, output)
	}

	state := inspectProject(worktree)
	if state.resolutionErr != nil || state.gitDir == "" || state.gitCommonDir == "" {
		t.Fatalf("linked-worktree Git metadata inspection = %+v", state)
	}
	if within(state.root, state.gitCommonDir) {
		t.Fatalf("common Git directory %q unexpectedly remains under worktree %q", state.gitCommonDir, state.root)
	}
	for _, target := range []string{
		filepath.Join(state.gitCommonDir, "config"),
		filepath.Join(state.gitDir, "config.worktree"),
	} {
		result := fixture.callAt(worktree, roleOther, "write", map[string]any{"filePath": target})
		if result.allow {
			t.Errorf("file writer allowed Git metadata path %q: %+v", target, result)
		}
		result = fixture.callAt(worktree, roleOther, "bash", map[string]any{"command": "printf x > " + shellSingleQuote(target)})
		if result.allow {
			t.Errorf("shell redirection allowed Git metadata path %q: %+v", target, result)
		}
	}
	result := fixture.callAt(worktree, roleOther, "bash", map[string]any{"command": "git config color.ui always"})
	if result.allow {
		t.Fatalf("local Git config write was allowed in linked worktree: %+v", result)
	}
}

func TestUserScopeManifestPathActuallyLoadedIsProtected(t *testing.T) {
	fixture := newFixture(t, false, false)
	if err := os.Remove(filepath.Join(fixture.root, ".agents", "drafting-table.yaml")); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(fixture.home, ".agents", "drafting-table.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifestYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	state := inspectProject(fixture.root)
	if filepath.Clean(state.manifestPath) != filepath.Clean(manifestPath) {
		t.Fatalf("loaded manifest path = %q, want %q", state.manifestPath, manifestPath)
	}
	result := fixture.call(roleOther, "write", map[string]any{"filePath": manifestPath})
	if result.allow {
		t.Fatalf("file writer allowed loaded user manifest: %+v", result)
	}
	result = fixture.call(roleOther, "bash", map[string]any{"command": "printf x > " + shellSingleQuote(manifestPath)})
	if result.allow {
		t.Fatalf("shell writer allowed loaded user manifest: %+v", result)
	}
}

func TestCredentialsSymlinkEscapeAndSearch(t *testing.T) {
	fixture := newFixture(t, true, false)
	for _, path := range []string{".env", ".env.local", ".netrc", ".npmrc", ".pypirc", ".git-credentials", "id_ed25519", "a.pem", "a.key", "a.p12", "a.pfx", "a.jks", "a-secret", ".aws/credentials", ".kube/config", ".docker/config.json", ".ssh/config", ".gnupg/private"} {
		full := filepath.Join(fixture.root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := fixture.call(roleDraftingTable, "read", map[string]any{"filePath": path})
		if result.allow || !strings.Contains(result.reason, "credential") {
			t.Errorf("%s: %+v", path, result)
		}
	}
	outside := t.TempDir()
	external := filepath.Join(outside, "ordinary.txt")
	if err := os.WriteFile(external, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(fixture.root, "docs", "outside")); err != nil {
		t.Fatal(err)
	}
	result := fixture.call(roleDraftingTable, "read", map[string]any{"filePath": "docs/outside"})
	if result.allow || !strings.Contains(result.reason, "outside the project") {
		t.Fatalf("escape = %+v", result)
	}
	for _, path := range []string{".git/config", ".protobot/change-sets/cs-00002.yaml"} {
		result = fixture.call(roleDraftingTable, "read", map[string]any{"filePath": path})
		if result.allow {
			t.Errorf("denied project read %q = %+v", path, result)
		}
	}
	result = fixture.call(roleDraftingTable, "glob", map[string]any{"path": fixture.root, "pattern": "**/*"})
	if result.allow {
		t.Fatalf("search at project root = %+v", result)
	}
	ghCredentials := filepath.Join(fixture.home, ".config", "gh")
	if err := os.MkdirAll(ghCredentials, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghCredentials, "hosts.yml"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = fixture.call(roleDraftingTable, "read", map[string]any{"filePath": "~/.config/gh/hosts.yml"})
	if result.allow || !strings.Contains(result.reason, "outside the project") {
		t.Fatalf("Git host credential path = %+v", result)
	}
	if err := os.Symlink(filepath.Join(fixture.root, "docs", "vision.md"), filepath.Join(fixture.root, "alias")); err != nil {
		t.Fatal(err)
	}
	result = fixture.call(roleOther, "write", map[string]any{"filePath": "alias"})
	if result.allow || !strings.Contains(result.reason, "registered artifact") {
		t.Fatalf("alias write = %+v", result)
	}
	if err := os.MkdirAll(filepath.Join(fixture.root, "docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.root, "docs", "sub"), filepath.Join(fixture.root, "aliasdir")); err != nil {
		t.Fatal(err)
	}
	result = fixture.call(roleOther, "write", map[string]any{"filePath": "aliasdir/../vision.md"})
	if result.allow || !strings.Contains(result.reason, "registered artifact") {
		t.Fatalf("symlink/parent write = %+v", result)
	}
	result = fixture.call(roleOther, "write", map[string]any{"filePath": "docs/vision.md/../notes.txt"})
	if result.allow {
		t.Fatalf("non-directory traversal write = %+v", result)
	}
	if err := os.MkdirAll(filepath.Join(fixture.root, "docs", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "docs", "nested", ".netrc"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = fixture.call(roleDraftingTable, "glob", map[string]any{"path": "docs", "pattern": "**/*.md"})
	if result.allow {
		t.Fatalf("sensitive search = %+v", result)
	}
	unknownSkill := filepath.Join(fixture.root, ".agents", "skills", "pull-request")
	if err := os.MkdirAll(unknownSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknownSkill, "SKILL.md"), []byte("not a Toolkit skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = fixture.call(roleDraftingTable, "glob", map[string]any{"path": filepath.Join(fixture.root, ".agents", "skills"), "pattern": "**/*"})
	if result.allow {
		t.Fatalf("search including an unlisted skill = %+v", result)
	}
}

func TestInspectionFailureClosesWriteAndShellRoutes(t *testing.T) {
	fixture := newFixture(t, true, true)
	result := fixture.call(roleDraftingTable, "bash", map[string]any{"command": "ears-manager --version"})
	if result.allow || !strings.Contains(result.reason, "project inspection failed") {
		t.Fatalf("shell = %+v", result)
	}
	result = fixture.call(roleDraftingTable, "apply_patch", map[string]any{"patchText": "*** Begin Patch\n*** Add File: notes/new.txt\n+x\n*** End Patch"})
	if result.allow || !strings.Contains(result.reason, "project inspection failed") {
		t.Fatalf("write = %+v", result)
	}
	result = fixture.call(roleOther, "write", map[string]any{"filePath": ".protobot/project.yaml"})
	if result.allow || !strings.Contains(result.reason, ".protobot/") {
		t.Fatalf("control write = %+v", result)
	}
	if result := fixture.call(roleOther, "write", map[string]any{"filePath": "notes.txt"}); !result.allow {
		t.Fatalf("unregistered write = %+v", result)
	}
	malformed := newFixture(t, true, false)
	if err := os.Remove(filepath.Join(malformed.root, ".protobot", "project.yaml")); err != nil {
		t.Fatal(err)
	}
	result = malformed.call(roleDraftingTable, "write", map[string]any{"filePath": "notes.txt"})
	if result.allow || !strings.Contains(result.reason, "project inspection failed") {
		t.Fatalf("missing project.yaml write decision = %+v", result)
	}
}

func TestGitInspectionErrorFailsClosedInsteadOfAssumingNoProject(t *testing.T) {
	fixture := newFixture(t, true, false)
	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)
	result := fixture.call(roleOther, "write", map[string]any{"filePath": "notes.txt"})
	if result.allow || !strings.Contains(result.reason, "Git working-tree root could not be inspected") {
		t.Fatalf("Git inspection failure decision = %+v", result)
	}
}

func TestMultilineStorePathAndUserManifestFallback(t *testing.T) {
	fixture := newFixture(t, true, false)
	project := strings.Join([]string{
		"project:", "  id: fixture-project", "repository:", "  canonical_remote: https://example.invalid/repo.git",
		"  default_branch: main", "  branch_prefix: cs/", "  review_mode: single-player", "stores:",
		"    requirements: >-", "      records/requirements", "    interfaces: records/interfaces",
		"    change_sets: records/changes", "artifacts:", "  - id: vision", "    path: docs/vision.md", "",
	}, "\n")
	if err := os.MkdirAll(filepath.Join(fixture.root, "records"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.root, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"requirements", "notes"} {
		if err := os.WriteFile(filepath.Join(fixture.root, "x", name), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(fixture.root, "ordinary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "ordinary", "requirements"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, ".protobot", "project.yaml"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	result := fixture.call(roleOther, "write", map[string]any{"filePath": "records/requirements/new.yaml"})
	if result.allow || !strings.Contains(result.reason, "configured store") {
		t.Fatalf("store write = %+v", result)
	}
	for _, command := range []string{
		"cp payload records/requirements/new.yaml",
		"tee records/requirements/new.yaml",
		"sort -o records/requirements/new.yaml input",
		"uniq input records/requirements/new.yaml",
		"git config --file records/requirements/new.yaml color.ui always",
		"rm -r records/requirements",
		"echo x > records/requirements/new.yaml",
	} {
		result := fixture.call(roleOther, "bash", map[string]any{"command": command})
		if result.allow {
			t.Errorf("shell writer allowed configured store target for %q: %+v", command, result)
		}
	}
	ordinaryDirectory := filepath.Join(fixture.root, "ordinary")
	ordinaryOutputDirectory := filepath.Join(fixture.root, "ordinary-output")
	if err := os.MkdirAll(ordinaryDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ordinaryOutputDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ordinaryDirectory, "requirements"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"ln -s ordinary/requirements records",
		"ln -s ordinary/requirements records/",
		"ln --symbolic --target-directory records ordinary/requirements",
		"cp -s ordinary/requirements records",
		"cp --symbolic-link --target-directory records ordinary/requirements",
		"cp -l ordinary/requirements records",
		"cp --link --target-directory=records ordinary/requirements",
	} {
		result := fixture.call(roleOther, "bash", map[string]any{"command": command})
		if result.allow {
			t.Errorf("link writer allowed mapped destination in a configured store for %q: %+v", command, result)
		}
	}
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "ln -s ordinary/requirements records/"}); result.allow {
		t.Fatalf("trailing-slash link destination was allowed to overwrite a configured store member: %+v", result)
	}
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "ln -s ordinary/notes records/"}); !result.allow {
		t.Fatalf("trailing-slash link destination refused a benign mapped name: %+v", result)
	}
	for _, command := range []string{
		"cp x/requirements records",
		"cp -d x/requirements records",
		"cp -t records x/requirements",
		"cp --target-directory=records x/requirements",
		"mv x/requirements records",
		"mv -t records x/requirements",
		"mv --target-directory=records x/requirements",
		"install x/requirements records",
		"install -t records x/requirements",
		"install --target-directory=records x/requirements",
	} {
		if result := fixture.call(roleOther, "bash", map[string]any{"command": command}); result.allow {
			t.Errorf("copy writer allowed mapped configured store destination for %q: %+v", command, result)
		}
	}
	for _, command := range []string{
		"cp x/notes records",
		"cp -d x/notes records",
		"cp -t records x/notes",
		"cp --target-directory=records x/notes",
		"mv x/notes records",
		"mv -t records x/notes",
		"mv --target-directory=records x/notes",
		"install x/notes records",
		"install -t records x/notes",
		"install --target-directory=records x/notes",
	} {
		if result := fixture.call(roleOther, "bash", map[string]any{"command": command}); !result.allow {
			t.Errorf("copy writer refused benign mapped destination for %q: %+v", command, result)
		}
	}
	for _, command := range []string{
		"ln -s ordinary/requirements ordinary-output",
		"ln --symbolic --target-directory=ordinary-output ordinary/requirements",
		"cp -s ordinary/requirements ordinary-output",
		"cp --symbolic-link --target-directory=ordinary-output ordinary/requirements",
		"cp -l ordinary/requirements ordinary-output",
		"cp --link --target-directory ordinary-output ordinary/requirements",
	} {
		result := fixture.call(roleOther, "bash", map[string]any{"command": command})
		if !result.allow {
			t.Errorf("link writer refused ordinary mapped destination for %q: %+v", command, result)
		}
	}
	for _, command := range []string{
		"cp -s --parents ordinary/requirements ordinary-output",
		"cp -l --parents ordinary/requirements ordinary-output",
	} {
		result := fixture.call(roleOther, "bash", map[string]any{"command": command})
		if result.allow {
			t.Errorf("link writer allowed ambiguous --parents output mapping for %q: %+v", command, result)
		}
	}
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "cp -r ordinary/. records"}); result.allow {
		t.Fatalf("directory copy into a configured store ancestor was allowed: %+v", result)
	}
	for _, command := range []string{"rm -r records", "mv records /tmp/backup", "rm -r ordinary", "mv ordinary /tmp/backup"} {
		result := fixture.call(roleOther, "bash", map[string]any{"command": command})
		blocked := strings.Contains(command, "records")
		if result.allow == blocked {
			t.Errorf("recursive directory writer decision for %q = %+v; blocked=%v", command, result, blocked)
		}
	}

	userFixture := newFixture(t, false, false)
	manifestPath := filepath.Join(userFixture.home, ".agents", "drafting-table.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifestYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := userFixture.call(roleDraftingTable, "scm_repo_state", map[string]any{}); !result.allow {
		t.Fatalf("fallback = %+v", result)
	}
	skillRoot := filepath.Join(userFixture.home, ".agents", "skills", "drafting-specifications")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = userFixture.call(roleDraftingTable, "read", map[string]any{"filePath": filepath.Join(skillRoot, "SKILL.md")})
	if !result.allow {
		t.Fatalf("user-scope Toolkit skill read = %+v", result)
	}
	result = userFixture.call(roleDraftingTable, "glob", map[string]any{"path": skillRoot, "pattern": "**/*"})
	if !result.allow {
		t.Fatalf("user-scope Toolkit skill search = %+v", result)
	}
}

func TestPreservedSymlinksAreCheckedAtTheirCopyOrMoveDestination(t *testing.T) {
	fixture := newFixture(t, true, false)
	sourceParent := t.TempDir()
	dangerousTree := filepath.Join(sourceParent, "evil")
	if err := os.MkdirAll(dangerousTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceParent, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceParent, ".git", "config"), []byte("ordinary source target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.git/config", filepath.Join(dangerousTree, "tree-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.git/config", filepath.Join(dangerousTree, "rsync-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dangerousTree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dangerousTree, ".git", "config"), []byte("ordinary source target"), 0o600); err != nil {
		t.Fatal(err)
	}
	singleLink := filepath.Join(dangerousTree, "single-link")
	if err := os.Symlink(".git/config", singleLink); err != nil {
		t.Fatal(err)
	}
	absoluteProtectedLink := filepath.Join(dangerousTree, "absolute-protected-link")
	if err := os.Symlink(filepath.Join(fixture.root, ".git", "config"), absoluteProtectedLink); err != nil {
		t.Fatal(err)
	}
	directory := shellSingleQuote(dangerousTree)
	link := shellSingleQuote(singleLink)
	absLink := shellSingleQuote(absoluteProtectedLink)
	for _, command := range []string{
		"cp -r " + directory + " copied-recursive",
		"cp -R " + directory + " copied-uppercase-recursive",
		"cp -R -L -H " + directory + " copied-conflicting-dereference",
		"cp -a " + directory + " copied-archive",
		"cp -d " + link + " copied-no-dereference",
		"cp -P " + link + " copied-preserved",
		"cp -P " + absLink + " copied-absolute-protected",
		"mv " + directory + " moved-tree",
		"mv " + link + " moved-link",
		"rsync -a " + directory + " copied-rsync",
		"rsync -a " + shellSingleQuote(dangerousTree+string(filepath.Separator)) + " copied-rsync-contents",
	} {
		if result := fixture.call(roleOther, "bash", map[string]any{"command": command}); result.allow {
			t.Errorf("writer allowed a preserved symlink re-anchored into Git metadata for %q: %+v", command, result)
		}
	}
	missing := shellSingleQuote(filepath.Join(t.TempDir(), "missing-link"))
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "cp -P " + missing + " missing-copy"}); result.allow {
		t.Fatalf("copy with an uninspectable preserved source was allowed: %+v", result)
	}

	plainTree := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plainTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plainTree, "payload"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "cp -r " + shellSingleQuote(plainTree) + " copied-plain"}); !result.allow {
		t.Fatalf("recursive copy without symlinks was refused: %+v", result)
	}

	safeTree := filepath.Join(t.TempDir(), "safe")
	if err := os.MkdirAll(safeTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(safeTree, "target"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(safeTree, "relative-link")); err != nil {
		t.Fatal(err)
	}
	absoluteTarget := filepath.Join(t.TempDir(), "ordinary-target")
	if err := os.WriteFile(absoluteTarget, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(absoluteTarget, filepath.Join(safeTree, "absolute-link")); err != nil {
		t.Fatal(err)
	}
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "cp -r " + shellSingleQuote(safeTree) + " copied-safe"}); !result.allow {
		t.Fatalf("relative symlink to an ordinary path was refused: %+v", result)
	}
	if result := fixture.call(roleOther, "bash", map[string]any{"command": "cp --preserve=links " + shellSingleQuote(filepath.Join(safeTree, "relative-link")) + " copied-preserve-links"}); !result.allow {
		t.Fatalf("hard-link preservation mode refused a benign symlink operand: %+v", result)
	}
}

func TestPreservedSymlinkWalkFailsClosedAtEntryLimit(t *testing.T) {
	fixture := newFixture(t, true, false)
	tree := filepath.Join(t.TempDir(), "large")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxSymlinkWalkEntries; index++ {
		path := filepath.Join(tree, fmt.Sprintf("entry-%05d", index))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := "cp -r " + shellSingleQuote(tree) + " copied-large"
	if result := fixture.call(roleOther, "bash", map[string]any{"command": command}); result.allow {
		t.Fatalf("copy tree exceeding the symlink-inspection limit was allowed: %+v", result)
	}
}

func TestDirectoryCopyToProtectedAncestorAndOrdinaryTargets(t *testing.T) {
	fixture := newFixture(t, true, false)
	notes := filepath.Join(fixture.root, "notes.md")
	if err := os.WriteFile(notes, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	externalPayload := filepath.Join(t.TempDir(), "payload")
	if err := os.MkdirAll(externalPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalPayload, "payload.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	projectPayload := filepath.Join(fixture.root, "payload")
	outputDir := filepath.Join(fixture.root, "output-dir")
	for _, directory := range []string{projectPayload, outputDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectPayload, "payload.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	payloadContents := externalPayload + string(filepath.Separator) + "."
	for _, test := range []struct {
		command   string
		wantAllow bool
	}{
		{command: "cp -r " + shellSingleQuote(payloadContents) + " ."},
		{command: "cp -t . " + shellSingleQuote(payloadContents)},
		{command: "cp -r payload .", wantAllow: true},
		{command: "cp notes.md .", wantAllow: true},
		{command: "cp -r payload/. output-dir", wantAllow: true},
	} {
		command := test.command
		result := fixture.call(roleOther, "bash", map[string]any{"command": command})
		if result.allow != test.wantAllow {
			t.Errorf("directory copy decision for %q = %+v; want allow=%v", command, result, test.wantAllow)
		}
	}
}

func TestReservedHarnessBindingPathCannotEscapeThroughSymlink(t *testing.T) {
	fixture := newFixture(t, true, false)
	if err := os.Symlink(t.TempDir(), filepath.Join(fixture.root, ".claude")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tool  string
		input map[string]any
	}{
		{tool: "write", input: map[string]any{"filePath": ".claude/settings.json"}},
		{tool: "bash", input: map[string]any{"command": "cp payload .claude/settings.json"}},
		{tool: "bash", input: map[string]any{"command": "echo x > .claude/settings.json"}},
	} {
		if result := fixture.call(roleOther, test.tool, test.input); result.allow {
			t.Errorf("symlinked harness path was writable with %s: %+v", test.tool, result)
		}
	}
}

func TestDirectorySourceContainsConfiguredStoresAndFailsClosed(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"records/requirements", "ordinary"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := projectState{root: root, storePaths: []string{"records/requirements"}}
	if !directorySourceContainsProtectedMetadata(state, root, "records") {
		t.Fatal("directory containing a configured store was not protected")
	}
	if directorySourceContainsProtectedMetadata(state, root, "ordinary") {
		t.Fatal("ordinary directory was incorrectly protected")
	}

	state.storePaths = []string{"missing/../records/requirements"}
	if !directorySourceContainsProtectedMetadata(state, root, "ordinary") {
		t.Fatal("unresolvable configured store path did not fail closed")
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}
	if !directorySourceContainsProtectedMetadata(state, root, "broken") {
		t.Fatal("unresolvable source path did not fail closed")
	}
}

func TestNoProjectRoleAndMCPRules(t *testing.T) {
	fixture := newFixture(t, false, false)
	if result := fixture.call(roleDraftingTable, "scm_branch_init", map[string]any{}); !result.allow {
		t.Fatalf("branch_init = %+v", result)
	}
	if result := fixture.call(roleDraftingTable, "bash", map[string]any{"command": "true"}); result.allow {
		t.Fatalf("true = %+v", result)
	}
	if err := runGit(fixture.root, "branch", "-M", "cs/00001-project-init"); err != nil {
		t.Fatal(err)
	}
	command := "ears-manager --output json project init --id fixture --name Fixture --canonical-remote https://example.invalid/repo.git --review-mode single-player"
	if result := fixture.call(roleDraftingTable, "bash", map[string]any{"command": command}); !result.allow {
		t.Fatalf("init = %+v", result)
	}

	fixture = newFixture(t, true, false)
	if result := fixture.call(roleDraftingTable, "scm_repo_state", map[string]any{}); !result.allow {
		t.Fatalf("listed SCM = %+v", result)
	}
	if result := fixture.call(roleDraftingTable, "scm_publish", map[string]any{}); result.allow {
		t.Fatalf("unlisted SCM = %+v", result)
	}
	if result := fixture.call(roleOther, "wms_request_query", map[string]any{}); result.allow {
		t.Fatalf("outside-role WMS = %+v", result)
	}
}

func TestRunStatusAndSingleLineContract(t *testing.T) {
	fixture := newFixture(t, false, false)
	input := `{"hook_event_name":"PreToolUse","session_id":"test","cwd":"` + fixture.root + `","tool_name":"bash","tool_input":{"command":"true"}}`
	var stderr strings.Builder
	if status := Run([]string{"--harness", "opencode", "--role", roleDraftingTable}, strings.NewReader(input), &stderr); status != 2 || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("refusal: status=%d stderr=%q", status, stderr.String())
	}
	stderr.Reset()
	input = strings.Replace(input, `"command":"true"`, `"command":"date -u +%Y-%m-%dT%H:%M:%SZ"`, 1)
	if status := Run([]string{"--harness", "opencode", "--role", roleDraftingTable}, strings.NewReader(input), &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("allow: status=%d stderr=%q", status, stderr.String())
	}
}

func TestGuardExecutableRefusesControlFlowGovernedWrite(t *testing.T) {
	fixture := newFixture(t, true, false)
	binary := filepath.Join(t.TempDir(), "drafting-table-guard")
	build := exec.Command("go", "build", "-o", binary, "./cmd/drafting-table-guard")
	build.Env = safeCommandEnv()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guard executable: %v: %s", err, output)
	}
	input, err := json.Marshal(hookInput{
		HookEventName: "PreToolUse",
		SessionID:     "fixture-session",
		CWD:           fixture.root,
		ToolName:      "bash",
		ToolInput: map[string]any{
			"command": "bash -c 'for i in x; do ears-manager --output json requirement add --change-set CS-00001; done'",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "--harness", "opencode", "--role", roleOther)
	command.Env = safeCommandEnv()
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if err == nil {
		t.Fatal("guard executable allowed the control-flow governed write")
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 2 {
		t.Fatalf("guard exit error = %v; stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), "ears-manager writes are reserved") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	input, err = json.Marshal(hookInput{
		HookEventName: "PreToolUse",
		SessionID:     "fixture-session",
		CWD:           fixture.root,
		ToolName:      "bash",
		ToolInput: map[string]any{
			"command": "mkdir 1 && echo x >&1/../docs/vision.md",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command = exec.Command(binary, "--harness", "opencode", "--role", roleOther)
	command.Env = safeCommandEnv()
	command.Stdin = bytes.NewReader(input)
	stdout.Reset()
	stderr.Reset()
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if err == nil {
		t.Fatal("guard executable allowed the digit-prefixed combined redirection")
	}
	exitError, ok = err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 2 {
		t.Fatalf("guard exit error = %v; stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), "output redirection targets") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type fixture struct{ root, home string }

const manifestYAML = "adapter_layout: 1\nentry_point: drafting-table\ntoolkit_skills:\n  - drafting-specifications\n  - eliciting-requirements\ngoverned_commands:\n  - ears-manager\ngoverned_mcp_servers:\n  wms:\n    tools:\n      - request_create\n      - request_query\n  scm:\n    tools:\n      - repo_state\n      - branch_init\n"

func newFixture(t *testing.T, initialized, earsFail bool) fixture {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FAKE_EARS_FAIL", "0")
	t.Setenv("FAKE_EARS_CHANGESET_FAIL", "0")
	t.Setenv("FAKE_ARTIFACT_TILDE", "0")
	if earsFail {
		t.Setenv("FAKE_EARS_FAIL", "1")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "ears-manager"), []byte(fakeEarsManager), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+filepath.Dir(git)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := runGit(root, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "branch", "-M", "cs/00003-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents", "drafting-table.yaml"), []byte(manifestYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "vision.md"), []byte("vision"), 0o600); err != nil {
		t.Fatal(err)
	}
	if initialized {
		if err := os.MkdirAll(filepath.Join(root, ".protobot", "change-sets"), 0o755); err != nil {
			t.Fatal(err)
		}
		project := "project:\n  id: fixture-project\nrepository:\n  canonical_remote: https://example.invalid/repo.git\n  default_branch: main\n  branch_prefix: cs/\n  review_mode: single-player\nstores:\n  requirements: .protobot/requirements\n  interfaces: .protobot/interfaces\n  change_sets: .protobot/change-sets\nartifacts:\n  - id: vision\n    path: docs/vision.md\n"
		if err := os.WriteFile(filepath.Join(root, ".protobot", "project.yaml"), []byte(project), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGit(root, "config", "user.name", "Guard Fixture"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "config", "user.email", "guard-fixture@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(root, "commit", "-qm", "fixture"); err != nil {
		t.Fatal(err)
	}
	return fixture{root: root, home: home}
}

func (f fixture) call(role, tool string, toolInput map[string]any) decision {
	return f.callAt(f.root, role, tool, toolInput)
}

func (f fixture) callAt(cwd, role, tool string, toolInput map[string]any) decision {
	return evaluate("opencode", role, hookInput{HookEventName: "PreToolUse", SessionID: "fixture", CWD: cwd, ToolName: tool, ToolInput: toolInput})
}

func runGit(root string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = safeCommandEnv()
	if output, err := command.CombinedOutput(); err != nil {
		return &gitTestError{output: string(output), err: err}
	}
	return nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type gitTestError struct {
	output string
	err    error
}

func (err *gitTestError) Error() string { return err.err.Error() + ": " + err.output }

const fakeEarsManager = "#!/bin/sh\nif [ \"$FAKE_EARS_FAIL\" = \"1\" ]; then exit 9; fi\ncase \"$3:$4\" in\n  artifact:list) if [ \"$FAKE_ARTIFACT_TILDE\" = \"1\" ]; then printf '%s\\n' '{\"schema_version\":1,\"ok\":true,\"command\":\"artifact list\",\"data\":{\"artifacts\":[{\"id\":\"vision\",\"path\":\"~/docs/vision.md\"}]},\"diagnostics\":[],\"mutation\":{\"applied\":false,\"paths\":[]}}'; else printf '%s\\n' '{\"schema_version\":1,\"ok\":true,\"command\":\"artifact list\",\"data\":{\"artifacts\":[{\"id\":\"vision\",\"path\":\"docs/vision.md\"}]},\"diagnostics\":[],\"mutation\":{\"applied\":false,\"paths\":[]}}'; fi ;;\n  change-set:list) if [ \"$FAKE_EARS_CHANGESET_FAIL\" = \"1\" ]; then exit 7; fi; printf '%s\\n' '{\"schema_version\":1,\"ok\":true,\"command\":\"change-set list\",\"data\":{\"change_sets\":[{\"id\":\"CS-00003\",\"status\":\"proposed\"}]},\"diagnostics\":[],\"mutation\":{\"applied\":false,\"paths\":[]}}' ;;\n  *) exit 8 ;;\nesac\n"
