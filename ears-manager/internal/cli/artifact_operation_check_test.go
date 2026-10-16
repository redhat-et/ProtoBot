package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

func TestCheckRequiresProposedArtifactOperationForDigestUpdatedHandEdit(t *testing.T) {
	root, artifact, _ := newArtifactOperationFixture(t)
	updated := writeArtifactContent(t, root, artifact.ID, []byte("# Hand-edited artifact\n"))
	if updated.Digest == artifact.Digest {
		t.Fatal("test content did not change the registered digest")
	}
	before := gitOutput(t, root, "status", "--porcelain", "--untracked-files=all")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", artifact.ID)
	if strings.Contains(stdout, `"code":"artifact.digest_mismatch"`) {
		t.Fatalf("test did not update the artifact digest: %s", stdout)
	}
	for _, guidance := range []string{"proposed change set", "canonical remote", "repo_state", "rerun check"} {
		if !strings.Contains(stdout, guidance) {
			t.Fatalf("operation diagnostic omitted %q guidance: %s", guidance, stdout)
		}
	}
	if after := gitOutput(t, root, "status", "--porcelain", "--untracked-files=all"); after != before {
		t.Fatalf("check changed git status\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCheckAcceptsMatchingProposedArtifactOperation(t *testing.T) {
	root, artifact, proposedID := newArtifactOperationFixture(t)
	writeArtifactContent(t, root, artifact.ID, []byte("# Revised artifact\n"))
	writeArtifactOperation(t, root, proposedID, artifact.ID)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestCheckDoesNotCountApprovedArtifactOperation(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	git(t, root, "checkout", "main")

	snapshot, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := snapshot.Config.Artifacts[0]
	approvedID := approvedChangeSetID(t, root, snapshot)
	writeArtifactOperation(t, root, approvedID, artifact.ID)
	approvedPath := changeSetPath(t, root, approvedID)
	git(t, root, "add", ".protobot/project.yaml", approvedPath)
	git(t, root, "commit", "-m", "record approved artifact operation")

	proposedID := createProposedArtifactChangeSet(t, root)
	writeArtifactContent(t, root, artifact.ID, []byte("# Revised artifact\n"))

	current, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	proposed, failure := proposedChangeSetIDs(root, current)
	if failure != nil {
		t.Fatalf("resolve proposed change sets: %v", failure)
	}
	approvedMatch, proposedMatch := false, false
	for _, document := range current.ChangeSets {
		for _, operation := range document.Value.ArtifactOperations {
			if operation.ArtifactID != artifact.ID {
				continue
			}
			if proposed[document.Value.ID] {
				proposedMatch = true
			} else {
				approvedMatch = true
			}
		}
	}
	if !approvedMatch || proposedMatch || !proposed[proposedID] {
		t.Fatalf("fixture operation status: approved match=%t, proposed match=%t, proposal %s proposed=%t", approvedMatch, proposedMatch, proposedID, proposed[proposedID])
	}

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", artifact.ID)
	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--change-set", approvedID)
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", artifact.ID)
}

func TestCheckAllowsNewRegistrationForUnchangedDefaultBranchFile(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	path := "docs/previously-unregistered.md"
	baseContent := []byte("# Existing unregistered content\r\n")
	currentContent := []byte("# Existing unregistered content\n")
	writeTestFile(t, root, path, string(baseContent))
	git(t, root, "add", path)
	git(t, root, "commit", "-m", "track the unregistered artifact file")
	writeTestFile(t, root, path, string(currentContent))

	digest, err := specvalidation.CanonicalTextDigest(currentContent)
	if err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Artifacts = append(config.Artifacts, records.ArtifactEntry{
			ID:     "previously-unregistered",
			Kind:   records.ArtifactInterfaceProse,
			Path:   path,
			Digest: digest,
			Owner:  "user",
		})
	})
	addSharedProjectionPath(t, root, path)
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestCheckRequiresOperationForNewArtifactFile(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	path := "docs/new-artifact.md"
	content := []byte("# New artifact\n")
	writeTestFile(t, root, path, string(content))
	digest, err := specvalidation.CanonicalTextDigest(content)
	if err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Artifacts = append(config.Artifacts, records.ArtifactEntry{
			ID:     "new-artifact",
			Kind:   records.ArtifactInterfaceProse,
			Path:   path,
			Digest: digest,
			Owner:  "user",
		})
	})
	addSharedProjectionPath(t, root, path)
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", "new-artifact")
}

func TestCheckRequiresOperationForArtifactPathChangeWithIdenticalContent(t *testing.T) {
	root, artifact, _ := newArtifactOperationFixture(t)
	content := readTestFile(t, root, artifact.Path)
	newPath := filepath.ToSlash(filepath.Join(filepath.Dir(artifact.Path), "renamed-"+filepath.Base(artifact.Path)))
	writeTestFile(t, root, newPath, content)
	addSharedProjectionPath(t, root, newPath)
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		for index := range config.Artifacts {
			if config.Artifacts[index].ID == artifact.ID {
				config.Artifacts[index].Path = newPath
				return
			}
		}
		t.Fatalf("artifact %s disappeared from project config", artifact.ID)
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", artifact.ID)
}

func TestCheckRejectsArtifactUnregistration(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	artifactID := "vision"
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		kept := config.Artifacts[:0]
		for _, current := range config.Artifacts {
			if current.ID != artifactID {
				kept = append(kept, current)
			}
		}
		config.Artifacts = kept
	})
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.unregistered", artifactID)
}

func TestScopedCheckRequiresProposedArtifactOperation(t *testing.T) {
	root, artifact, proposedID := newArtifactOperationFixture(t)
	writeArtifactContent(t, root, artifact.ID, []byte("# Revised artifact\n"))

	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--change-set", proposedID)
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", artifact.ID)
}

func TestScopedCheckReportsStaleTargetBeforeArtifactOperationMissing(t *testing.T) {
	root, artifact, proposedID := newArtifactOperationFixture(t)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	git(t, root, "add", ".protobot")
	git(t, root, "commit", "-m", "record proposed change set")

	writeArtifactContent(t, root, artifact.ID, []byte("# Proposed artifact revision\n"))
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "revise artifact in proposal")

	git(t, root, "checkout", "main")
	writeArtifactContent(t, root, artifact.ID, []byte("# Default artifact revision\n"))
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "advance default artifact")
	git(t, root, "checkout", branch)
	t.Chdir(root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--change-set", proposedID)
	assertFailureCode(t, code, stdout, stderr, 5, "DEFAULT_MOVED")
	if !hasDiagnostic(t, stdout, "DEFAULT_MOVED", "base_commit") {
		t.Fatalf("scoped check omitted the stale target freshness diagnostic: %s", stdout)
	}
	if hasDiagnostic(t, stdout, "artifact.operation_missing", fmt.Sprintf("artifacts[id=%s].operation", artifact.ID)) {
		t.Fatalf("scoped check reported operation mismatch before target freshness: %s", stdout)
	}
}

func TestCheckIgnoresMetadataOnlyArtifactChanges(t *testing.T) {
	root, artifact, _ := newArtifactOperationFixture(t)
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		for index := range config.Artifacts {
			if config.Artifacts[index].ID == artifact.ID {
				config.Artifacts[index].Owner = "ears-manager"
				return
			}
		}
		t.Fatalf("artifact %s disappeared from project config", artifact.ID)
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestCheckRequiresResolvableDefaultBranchWithoutChangeSets(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	snapshot, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Config.Artifacts) == 0 || len(snapshot.ChangeSets) != 0 {
		t.Fatalf("fixture has %d artifact(s) and %d change set(s), want registered artifacts and no change sets", len(snapshot.Config.Artifacts), len(snapshot.ChangeSets))
	}

	git(t, root, "branch", "-m", "main", "trunk")
	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_branch_unresolved")
}

func TestProjectInitBranchRequiresOperationForNewArtifactPathWithoutBaseConfig(t *testing.T) {
	root := newUninitializedRepository(t)
	git(t, root, "checkout", "-b", "cs/00001-project-init")
	t.Chdir(root)
	code, stdout, stderr := runCLI(nil, initArgs...)
	assertSuccess(t, code, stdout, stderr)

	newPath := "docs/proposed-vision.md"
	content := []byte("# Proposed Vision\n")
	writeTestFile(t, root, newPath, string(content))
	digest, err := specvalidation.CanonicalTextDigest(content)
	if err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		for index := range config.Artifacts {
			if config.Artifacts[index].ID == "vision" {
				config.Artifacts[index].Path = newPath
				config.Artifacts[index].Digest = digest
				return
			}
		}
		t.Fatal("project init did not register Vision")
	})
	addSharedProjectionPath(t, root, newPath)

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", "vision")
}

func TestCheckFailsClosedForDefaultProjectConfigDirectory(t *testing.T) {
	root := newUninitializedRepository(t)
	git(t, root, "checkout", "-b", "cs/00001-project-init")
	t.Chdir(root)
	code, stdout, stderr := runCLI(nil, initArgs...)
	assertSuccess(t, code, stdout, stderr)
	git(t, root, "add", ".protobot")
	git(t, root, "commit", "-m", "initialize project")

	git(t, root, "checkout", "main")
	writeTestFile(t, root, ".protobot/project.yaml/marker", "not a project config\n")
	git(t, root, "add", ".protobot")
	git(t, root, "commit", "-m", "replace default config path with a directory")
	git(t, root, "checkout", "cs/00001-project-init")

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_configuration_invalid")
}

func TestCheckTreatsDefaultArtifactSymlinkAsChanged(t *testing.T) {
	root := newFixtureProject(t)
	git(t, root, "checkout", "-b", "feature")
	git(t, root, "checkout", "main")
	visionPath := filepath.Join(root, "docs", "vision.md")
	if err := os.Remove(visionPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("# Fixture Vision\n", visionPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	git(t, root, "add", "-A", "--", "docs/vision.md")
	git(t, root, "commit", "-m", "replace default artifact with a symlink")
	git(t, root, "checkout", "feature")
	t.Chdir(root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", "vision")
}

func TestGitTreeFileDistinguishesAbsenceFromReadFailure(t *testing.T) {
	root := newFixtureProject(t)
	head := gitOutput(t, root, "rev-parse", "main")
	_, present, regular, failure := gitTreeFile(root, head, "docs/not-registered.md")
	if failure != nil || present || regular {
		t.Fatalf("missing tree path = present %t, regular %t, failure %#v", present, regular, failure)
	}
	_, present, regular, failure = gitTreeFile(root, "not-a-commit", "docs/not-registered.md")
	if failure == nil || failure.Code != "revision.read_failed" || present || regular {
		t.Fatalf("invalid revision = present %t, regular %t, failure %#v", present, regular, failure)
	}
	git(t, root, "update-index", "--chmod=+x", "docs/vision.md")
	git(t, root, "commit", "-m", "mark artifact executable")
	head = gitOutput(t, root, "rev-parse", "main")
	_, present, regular, failure = gitTreeFile(root, head, "docs/vision.md")
	if failure != nil || !present || !regular {
		t.Fatalf("executable tree path = present %t, regular %t, failure %#v", present, regular, failure)
	}
}

func TestScopedCheckAcceptsOperationInAnotherProposedChangeSet(t *testing.T) {
	source, _, _, _, _ := selfHostingSourceWithApprovedAndProposedChangeSets(t)
	root := newSelfHostingCheckoutFrom(t, source)
	t.Chdir(root)
	snapshot, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	proposed, failure := proposedChangeSetIDs(root, snapshot)
	if failure != nil {
		t.Fatalf("resolve proposed change sets: %v", failure)
	}
	var proposedIDs []string
	for _, document := range snapshot.ChangeSets {
		if proposed[document.Value.ID] {
			proposedIDs = append(proposedIDs, document.Value.ID)
		}
	}
	if len(proposedIDs) < 2 {
		t.Fatalf("fixture has %d proposed change set(s), want at least two", len(proposedIDs))
	}
	artifact := snapshot.Config.Artifacts[0]
	for _, id := range proposedIDs {
		removeArtifactOperation(t, root, id, artifact.ID)
	}
	writeArtifactContent(t, root, artifact.ID, []byte("# Revised artifact\n"))
	writeArtifactOperation(t, root, proposedIDs[1], artifact.ID)

	updated, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	targetHasOperation, otherHasOperation := false, false
	for _, document := range updated.ChangeSets {
		for _, operation := range document.Value.ArtifactOperations {
			if operation.ArtifactID != artifact.ID {
				continue
			}
			targetHasOperation = targetHasOperation || document.Value.ID == proposedIDs[0]
			otherHasOperation = otherHasOperation || document.Value.ID == proposedIDs[1]
		}
	}
	if targetHasOperation || !otherHasOperation {
		t.Fatalf("fixture operation placement: target=%t, other proposed=%t", targetHasOperation, otherHasOperation)
	}

	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--change-set", proposedIDs[0])
	assertSuccess(t, code, stdout, stderr)
}

func TestCheckRejectsUndecodableDefaultBranchProjectConfig(t *testing.T) {
	root := newFixtureProject(t)
	git(t, root, "checkout", "-b", "feature")
	git(t, root, "checkout", "main")
	writeTestFile(t, root, ".protobot/project.yaml", "project: [\n")
	git(t, root, "add", ".protobot/project.yaml")
	git(t, root, "commit", "-m", "write undecodable default config")
	git(t, root, "checkout", "feature")
	t.Chdir(root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_configuration_invalid")
}

func newArtifactOperationFixture(t *testing.T) (string, records.ArtifactEntry, string) {
	t.Helper()
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	git(t, root, "checkout", "main")
	proposedID := createProposedArtifactChangeSet(t, root)
	snapshot, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Config.Artifacts) == 0 {
		t.Fatal("self-hosting fixture has no registered artifacts")
	}
	return root, snapshot.Config.Artifacts[0], proposedID
}

func createProposedArtifactChangeSet(t *testing.T, root string) string {
	t.Helper()
	t.Chdir(root)
	code, stdout, stderr := runCLI(nil, "--output", "json", "change-set", "create",
		"--intent", "Revise a registered artifact",
		"--implementation-required", "false",
		"--implementation-rationale", "The test change updates a registered artifact.",
		"--created", "2026-10-16T12:00:00Z",
	)
	assertSuccess(t, code, stdout, stderr)
	id := jsonString(t, stdout, "data", "change_set", "id")

	snapshot, err := specvalidation.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	proposed, failure := proposedChangeSetIDs(root, snapshot)
	if failure != nil {
		t.Fatalf("resolve proposed change sets: %v", failure)
	}
	if !proposed[id] {
		t.Fatalf("new change set %s is not proposed", id)
	}
	code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "show", "--change-set", id)
	assertSuccess(t, code, stdout, stderr)
	if status := jsonString(t, stdout, "data", "status"); status != "proposed" {
		t.Fatalf("change set %s status = %q, want proposed", id, status)
	}
	return id
}

func approvedChangeSetID(t *testing.T, root string, snapshot specvalidation.Snapshot) string {
	t.Helper()
	for _, document := range snapshot.ChangeSets {
		approved, failure := changeSetApprovedAt(root, []string{"refs/heads/main"}, document.Path)
		if failure != nil {
			t.Fatalf("inspect change set %s: %v", document.Value.ID, failure)
		}
		if approved {
			return document.Value.ID
		}
	}
	t.Fatal("self-hosting fixture has no approved change set")
	return ""
}

func writeArtifactOperation(t *testing.T, root, changeSetID, artifactID string) {
	t.Helper()
	path := changeSetPath(t, root, changeSetID)
	var changeSet records.ChangeSet
	if err := storage.ReadFile(path, &changeSet); err != nil {
		t.Fatal(err)
	}
	changeSet.ArtifactOperations = []records.ArtifactOperation{{Action: "revise", ArtifactID: artifactID}}
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshProjectDigests(t, root)
}

func removeArtifactOperation(t *testing.T, root, changeSetID, artifactID string) {
	t.Helper()
	path := changeSetPath(t, root, changeSetID)
	var changeSet records.ChangeSet
	if err := storage.ReadFile(path, &changeSet); err != nil {
		t.Fatal(err)
	}
	operations := changeSet.ArtifactOperations[:0]
	for _, operation := range changeSet.ArtifactOperations {
		if operation.ArtifactID != artifactID {
			operations = append(operations, operation)
		}
	}
	if len(operations) == len(changeSet.ArtifactOperations) {
		return
	}
	changeSet.ArtifactOperations = operations
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshProjectDigests(t, root)
}

func changeSetPath(t *testing.T, root, changeSetID string) string {
	t.Helper()
	filename, err := records.FilenameFor(records.ChangeSetStore, changeSetID)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, ".protobot", "change-sets", filename)
}

func writeArtifactContent(t *testing.T, root, artifactID string, content []byte) records.ArtifactEntry {
	t.Helper()
	path := filepath.Join(root, ".protobot", "project.yaml")
	var config records.ProjectConfig
	if err := storage.ReadFile(path, &config); err != nil {
		t.Fatal(err)
	}
	for index := range config.Artifacts {
		artifact := &config.Artifacts[index]
		if artifact.ID != artifactID {
			continue
		}
		writeTestFile(t, root, artifact.Path, string(content))
		digest, err := specvalidation.CanonicalTextDigest(content)
		if err != nil {
			t.Fatal(err)
		}
		artifact.Digest = digest
		data, err := storage.Encode(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		refreshProjectDigests(t, root)
		return *artifact
	}
	t.Fatalf("artifact %s was not registered", artifactID)
	return records.ArtifactEntry{}
}

func addSharedProjectionPath(t *testing.T, root, path string) {
	t.Helper()
	projection := readTestFile(t, root, specvalidation.ProjectionPath)
	if strings.Contains(projection, "- path: "+path+"\n") {
		return
	}
	projection = strings.TrimRight(projection, "\n") + "\n  - path: " + path + "\n    class: shared\n"
	writeTestFile(t, root, specvalidation.ProjectionPath, projection)
}

func assertArtifactDiagnostic(t *testing.T, stdout, code, artifactID string) {
	t.Helper()
	field := fmt.Sprintf("artifacts[id=%s]", artifactID)
	if code == "artifact.operation_missing" {
		field += ".operation"
	}
	if !hasDiagnostic(t, stdout, code, field) {
		t.Fatalf("check omitted %s for artifact %s: %s", code, artifactID, stdout)
	}
}
