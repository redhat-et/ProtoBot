package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

func TestSelfHostingFixturePassesCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
	if !jsonBool(t, stdout, "data", "valid") {
		t.Fatalf("check did not report valid: %s", stdout)
	}
	if jsonBool(t, stdout, "mutation", "applied") {
		t.Fatalf("check mutated the store: %s", stdout)
	}
	var result struct {
		Data struct {
			RecordCounts struct {
				Requirements int `json:"requirements"`
				Interfaces   int `json:"interfaces"`
				ChangeSets   int `json:"change_sets"`
				Artifacts    int `json:"artifacts"`
			} `json:"record_counts"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode check result: %v", err)
	}
	source := selfHostingRoot(t)
	csRels := selfHostingChangeSetRelPaths(t, source)
	reqRels, ifaceRels := referencedRecordsForChangeSets(t, source, csRels)
	wantChangeSets := len(csRels)
	wantRequirements := len(reqRels)
	wantInterfaces := len(ifaceRels)
	if got := result.Data.RecordCounts; got.Requirements != wantRequirements || got.Interfaces != wantInterfaces || got.ChangeSets != wantChangeSets || got.Artifacts != 2 {
		t.Fatalf("seeded record counts = %#v, want %d requirement(s), %d interface(s), %d change set(s), and two artifacts", got, wantRequirements, wantInterfaces, wantChangeSets)
	}
}

func TestSelfHostingProposedChangeSetBehindDefaultBranchFailsCheck(t *testing.T) {
	root := newSelfHostingSeedProposedCheckout(t)
	t.Chdir(root)
	baseCommit := gitOutput(t, root, "rev-parse", "refs/heads/main")
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")

	git(t, root, "checkout", "main")
	writeTestFile(t, root, "unrelated.txt", "default branch moved\n")
	commitAll(t, root, "unrelated default-branch change")
	defaultHead := gitOutput(t, root, "rev-parse", "HEAD")
	git(t, root, "checkout", branch)

	// CI fetches the default branch, detaches HEAD at the proposed commit,
	// and points the local default-branch ref at the fetched head.
	git(t, root, "checkout", "--detach", "HEAD")
	git(t, root, "update-ref", "refs/heads/main", defaultHead)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 5 || stderr != "" || jsonString(t, stdout, "error", "code") != "DEFAULT_MOVED" {
		t.Fatalf("stale proposed base = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "repo_state") ||
		!strings.Contains(stdout, "Continue only when remote.reachable is true and default_branch.local is current or fast-forwarded") ||
		!strings.Contains(stdout, "default_branch.head") || !strings.Contains(stdout, "base_commit_update") ||
		!strings.Contains(stdout, "--base-commit") {
		t.Fatalf("DEFAULT_MOVED hint omitted the repo_state freshness gate: %s", stdout)
	}
	if !hasDiagnostic(t, stdout, "DEFAULT_MOVED", "base_commit") {
		t.Fatalf("check omitted DEFAULT_MOVED on base_commit: %s", stdout)
	}
	if !strings.Contains(stdout, baseCommit) || !strings.Contains(stdout, defaultHead) {
		t.Fatalf("diagnostic did not name recorded base %s and default head %s: %s", baseCommit, defaultHead, stdout)
	}

	humanCode, _, humanStderr := runCLI(nil, "check")
	if humanCode != 5 || !strings.Contains(humanStderr, "error[DEFAULT_MOVED]:") {
		t.Fatalf("human check = exit %d stderr %q", humanCode, humanStderr)
	}

	git(t, root, "checkout", branch)
	git(t, root, "merge", "--no-ff", "--no-edit", "main")
	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	if code != 5 || stderr != "" || jsonString(t, stdout, "error", "code") != "BASE_COMMIT_STALE" {
		t.Fatalf("merged stale base = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "repo_state") ||
		!strings.Contains(stdout, "Continue only when remote.reachable is true and default_branch.local is current or fast-forwarded") ||
		!strings.Contains(stdout, "default_branch.head") || !strings.Contains(stdout, "base_commit_update") ||
		!strings.Contains(stdout, "--base-commit") {
		t.Fatalf("BASE_COMMIT_STALE hint omitted the repo_state freshness gate: %s", stdout)
	}
	if !hasDiagnostic(t, stdout, "BASE_COMMIT_STALE", "base_commit") {
		t.Fatalf("check omitted BASE_COMMIT_STALE on base_commit: %s", stdout)
	}
	if !strings.Contains(stdout, baseCommit) || !strings.Contains(stdout, defaultHead) {
		t.Fatalf("BASE_COMMIT_STALE did not name recorded base %s and default head %s: %s", baseCommit, defaultHead, stdout)
	}

	// Additional proposed change sets copied from the seed would also be
	// stale after the merge. Pin them so this test can exercise CS-00001's
	// refresh workflow in isolation.
	pinSelfHostingChangeSetsExcept(t, root, "CS-00001")

	code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "update", "--change-set", "CS-00001", "--base-commit", defaultHead)
	assertSuccess(t, code, stdout, stderr)
	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	if code != 5 || stderr != "" || jsonString(t, stdout, "error", "code") != "change_set.assessment_incomplete" {
		t.Fatalf("base-only update = code %d stdout %s stderr %s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI([]byte("[]"), "--output", "json", "change-set", "update", "--change-set", "CS-00001", "--impact-file", "-")
	assertSuccess(t, code, stdout, stderr)
	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--change-set", "CS-00001")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingProposedChangeSetBaseNotOnDefaultFailsCheck(t *testing.T) {
	root := newSelfHostingSeedProposedCheckout(t)
	t.Chdir(root)
	head := gitOutput(t, root, "rev-parse", "HEAD")
	defaultHead := gitOutput(t, root, "rev-parse", "refs/heads/main")
	path := filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml")
	var changeSet records.ChangeSet
	if err := storage.ReadFile(path, &changeSet); err != nil {
		t.Fatal(err)
	}
	changeSet.BaseCommit = head
	changeSet.ImpactAssessmentBaseCommit = head
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "BASE_NOT_ON_DEFAULT" {
		t.Fatalf("base not on default = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "BASE_NOT_ON_DEFAULT", "base_commit") {
		t.Fatalf("check omitted BASE_NOT_ON_DEFAULT on base_commit: %s", stdout)
	}
	if !strings.Contains(stdout, head) || !strings.Contains(stdout, defaultHead) {
		t.Fatalf("diagnostic did not name recorded base %s and default head %s: %s", head, defaultHead, stdout)
	}
	if !strings.Contains(stdout, "No command repairs a base that the default branch does not contain") {
		t.Fatalf("base absent from local and canonical remote defaults did not retain its reconciliation hint: %s", stdout)
	}
}

func TestSelfHostingProposedBaseOnCanonicalRemoteSuggestsRepoState(t *testing.T) {
	root := newSelfHostingSeedProposedCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	localHead := gitOutput(t, root, "rev-parse", "refs/heads/main")

	git(t, root, "checkout", "main")
	writeTestFile(t, root, "remote-only.txt", "canonical default branch advanced\n")
	commitAll(t, root, "advance canonical default branch")
	remoteHead := gitOutput(t, root, "rev-parse", "HEAD")
	git(t, root, "checkout", branch)
	git(t, root, "update-ref", "refs/heads/main", localHead)
	git(t, root, "remote", "add", "origin", "https://github.com/redhat-et/ProtoBot.git")
	git(t, root, "update-ref", "refs/remotes/origin/main", remoteHead)

	path := filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml")
	var changeSet records.ChangeSet
	if err := storage.ReadFile(path, &changeSet); err != nil {
		t.Fatal(err)
	}
	changeSet.BaseCommit = remoteHead
	changeSet.ImpactAssessmentBaseCommit = remoteHead
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "BASE_NOT_ON_DEFAULT" {
		t.Fatalf("base only on remote default = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "BASE_NOT_ON_DEFAULT", "base_commit") {
		t.Fatalf("check omitted BASE_NOT_ON_DEFAULT on base_commit: %s", stdout)
	}
	if !strings.Contains(stdout, "local default branch is behind") ||
		!strings.Contains(stdout, "repo_state") ||
		!strings.Contains(stdout, "Continue only when remote.reachable is true and default_branch.local is current or fast-forwarded") ||
		!strings.Contains(stdout, "retry check only after this predicate passes") {
		t.Fatalf("BASE_NOT_ON_DEFAULT hint omitted local-ref recovery: %s", stdout)
	}
}

func TestSelfHostingProposedBaseOnDivergedCanonicalRemoteFailsCheck(t *testing.T) {
	root := newSelfHostingSeedProposedCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	commonHead := gitOutput(t, root, "rev-parse", "refs/heads/main")

	git(t, root, "checkout", "main")
	writeTestFile(t, root, "local-only.txt", "local default branch diverged\n")
	commitAll(t, root, "advance local default branch")
	localHead := gitOutput(t, root, "rev-parse", "HEAD")

	git(t, root, "checkout", "--detach", commonHead)
	writeTestFile(t, root, "remote-only.txt", "canonical default branch diverged\n")
	commitAll(t, root, "advance canonical default branch")
	remoteHead := gitOutput(t, root, "rev-parse", "HEAD")

	git(t, root, "checkout", branch)
	git(t, root, "remote", "add", "origin", "https://github.com/redhat-et/ProtoBot.git")
	// Seed origin/main locally; check must not fetch from the network.
	git(t, root, "update-ref", "refs/remotes/origin/main", remoteHead)
	if got := gitOutput(t, root, "merge-base", "refs/heads/main", "refs/remotes/origin/main"); got != commonHead {
		t.Fatalf("local main and origin/main merge at %s, want shared head %s", got, commonHead)
	}
	for _, commit := range strings.Fields(gitOutput(t, root, "rev-list", "refs/heads/main")) {
		if commit == remoteHead {
			t.Fatalf("proposed base %s is unexpectedly reachable from local main", remoteHead)
		}
	}

	path := filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml")
	var changeSet records.ChangeSet
	if err := storage.ReadFile(path, &changeSet); err != nil {
		t.Fatal(err)
	}
	changeSet.BaseCommit = remoteHead
	changeSet.ImpactAssessmentBaseCommit = remoteHead
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "BASE_NOT_ON_DEFAULT" {
		t.Fatalf("base only on diverged canonical default = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "BASE_NOT_ON_DEFAULT", "base_commit") {
		t.Fatalf("check omitted BASE_NOT_ON_DEFAULT on base_commit: %s", stdout)
	}
	if !strings.Contains(stdout, remoteHead) || !strings.Contains(stdout, localHead) {
		t.Fatalf("diagnostic did not name remote base %s and local default head %s: %s", remoteHead, localHead, stdout)
	}
	if !strings.Contains(stdout, "The local default branch has diverged") ||
		!strings.Contains(stdout, "repo_state") ||
		!strings.Contains(stdout, "resolve the local default-branch state") ||
		!strings.Contains(stdout, "Continue only when remote.reachable is true and default_branch.local is current or fast-forwarded") ||
		!strings.Contains(stdout, "retry check only after this predicate passes") {
		t.Fatalf("BASE_NOT_ON_DEFAULT omitted the diverged recovery hint: %s", stdout)
	}
}

func TestSelfHostingApprovedChangeSetIgnoresStaleBase(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	writeTestFile(t, root, "unrelated.txt", "default branch moved after approval\n")
	commitAll(t, root, "unrelated default-branch change")
	git(t, root, "checkout", "-b", "pr/unrelated")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingHandEditedRegisteredArtifactFailsCheckWithoutOperation(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	rewriteRegisteredArtifact(t, root, "architecture", "# Hand-edited Architecture\n")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "artifact.operation_missing" {
		t.Fatalf("hand-edited artifact = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "artifact.operation_missing", "artifacts[id=architecture]") {
		t.Fatalf("check omitted artifact.operation_missing for architecture: %s", stdout)
	}
	if !strings.Contains(stdout, "architecture") {
		t.Fatalf("diagnostic did not name artifact architecture: %s", stdout)
	}

	scopedCode, scopedStdout, scopedStderr := runCLI(nil, "--output", "json", "check", "--change-set", "CS-00001")
	if scopedCode != 4 || scopedStderr != "" || jsonString(t, scopedStdout, "error", "code") != "artifact.operation_missing" {
		t.Fatalf("scoped check hand-edited artifact = code %d stdout %s stderr %s", scopedCode, scopedStdout, scopedStderr)
	}
	if !hasDiagnostic(t, scopedStdout, "artifact.operation_missing", "artifacts[id=architecture]") {
		t.Fatalf("scoped check omitted artifact.operation_missing for architecture: %s", scopedStdout)
	}

	humanCode, _, humanStderr := runCLI(nil, "check")
	if humanCode != 4 || !strings.Contains(humanStderr, "error[artifact.operation_missing]:") {
		t.Fatalf("human check = exit %d stderr %q", humanCode, humanStderr)
	}
}

func TestSelfHostingArtifactPutPassesCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	content := readTestFile(t, root, "docs/architecture.md") + "\nRevised through artifact put.\n"
	code, stdout, stderr := runCLI([]byte(content), "--output", "json", "artifact", "put",
		"--change-set", "CS-00001",
		"--id", "architecture",
		"--kind", "architecture",
		"--path", "docs/architecture.md",
		"--owner", "user",
		"--content-stdin")
	assertSuccess(t, code, stdout, stderr)

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingProposedArtifactOperationPassesCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	rewriteRegisteredArtifact(t, root, "architecture", "# Revised Architecture\n")
	addProposedArtifactOperation(t, root, "CS-00001", "revise", "architecture")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--change-set", "CS-00001")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingRegisteredArtifactPathChangeRequiresOperation(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	content := readTestFile(t, root, "docs/architecture.md")
	writeTestFile(t, root, "docs/architecture-copy.md", content)
	git(t, root, "add", "docs/architecture-copy.md")
	git(t, root, "commit", "-m", "commit architecture copy on main")

	git(t, root, "checkout", "-b", "pr/retarget-path")

	code, stdout, stderr := runCLI(nil, "--output", "json", "change-set", "create",
		"--intent", "Retarget architecture path",
		"--implementation-required", "false",
		"--implementation-rationale", "Path retarget.",
		"--created", "2026-10-14T20:00:00Z",
	)
	assertSuccess(t, code, stdout, stderr)

	code, stdout, stderr = runCLI([]byte("[]"), "--output", "json", "change-set", "update",
		"--change-set", "CS-00002",
		"--impact-file", "-",
	)
	assertSuccess(t, code, stdout, stderr)

	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		for index, artifact := range config.Artifacts {
			if artifact.ID == "architecture" {
				config.Artifacts[index].Path = "docs/architecture-copy.md"
			}
		}
	})
	appendSharedProjection(t, root, "docs/architecture-copy.md")

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "artifact.operation_missing" {
		t.Fatalf("path change without operation = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "artifact.operation_missing", "artifacts[id=architecture]") {
		t.Fatalf("check omitted artifact.operation_missing for architecture: %s", stdout)
	}

	addProposedArtifactOperation(t, root, "CS-00002", "revise", "architecture")
	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingRetargetedDefaultBranchFailsCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)
	git(t, root, "checkout", "-b", "evil")
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "evil"
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 3 || stderr != "" || jsonString(t, stdout, "error", "code") != "project.invalid_configuration" {
		t.Fatalf("retargeted default branch = code %d stdout %s stderr %s", code, stdout, stderr)
	}
}

func TestSelfHostingForgedBaseCommitDefaultBranchFailsCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	git(t, root, "checkout", "-b", "evil")
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "evil"
	})
	git(t, root, "add", ".protobot/project.yaml")
	git(t, root, "commit", "-m", "forged default branch commit")
	forgedBase := gitOutput(t, root, "rev-parse", "HEAD")

	rewriteRegisteredArtifact(t, root, "architecture", "# Forged Architecture\n")
	manifestPath := filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml")
	var changeSet records.ChangeSet
	if err := storage.ReadFile(manifestPath, &changeSet); err != nil {
		t.Fatal(err)
	}
	changeSet.BaseCommit = forgedBase
	changeSet.ImpactAssessmentBaseCommit = forgedBase
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 3 || stderr != "" || jsonString(t, stdout, "error", "code") != "project.invalid_configuration" {
		t.Fatalf("forged base_commit retarget = code %d stdout %s stderr %s", code, stdout, stderr)
	}
}

func TestSelfHostingRetargetedDefaultBranchDetachedHeadFailsCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	git(t, root, "checkout", "-b", "evil")
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "evil"
	})
	git(t, root, "add", ".protobot/project.yaml")
	git(t, root, "commit", "-m", "evil branch commit")

	git(t, root, "checkout", "-b", "third")
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "evil"
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 3 || stderr != "" || jsonString(t, stdout, "error", "code") != "project.invalid_configuration" {
		t.Fatalf("retargeted default branch on third branch = code %d stdout %s stderr %s", code, stdout, stderr)
	}

	git(t, root, "checkout", "--detach", "HEAD")
	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	if code != 3 || stderr != "" || jsonString(t, stdout, "error", "code") != "project.invalid_configuration" {
		t.Fatalf("retargeted default branch with detached HEAD = code %d stdout %s stderr %s", code, stdout, stderr)
	}
}

func TestSelfHostingRetargetedDefaultBranchOriginRefFailsCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	git(t, root, "remote", "add", "origin", "https://github.com/redhat-et/ProtoBot.git")
	mainCommit := gitOutput(t, root, "rev-parse", "refs/heads/main")
	git(t, root, "update-ref", "refs/remotes/origin/main", mainCommit)

	git(t, root, "checkout", "-b", "evil")
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "evil"
	})
	git(t, root, "add", ".protobot/project.yaml")
	git(t, root, "commit", "-m", "evil commit")
	evilCommit := gitOutput(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/remotes/origin/evil", evilCommit)

	git(t, root, "checkout", "-b", "third")
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "evil"
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 3 || stderr != "" || jsonString(t, stdout, "error", "code") != "project.invalid_configuration" {
		t.Fatalf("retargeted origin default branch = code %d stdout %s stderr %s", code, stdout, stderr)
	}
}

func TestSelfHostingCanonicalRemoteMergeHeadRetargetedDefaultBranchFailsCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	git(t, root, "remote", "add", "origin", "https://github.com/redhat-et/ProtoBot.git")
	mainCommit := gitOutput(t, root, "rev-parse", "refs/heads/main")
	git(t, root, "update-ref", "refs/remotes/origin/main", mainCommit)

	git(t, root, "checkout", "-b", "pr/feature")
	git(t, root, "commit", "--allow-empty", "-m", "feature commit")
	prCommit := gitOutput(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/remotes/origin/pr/feature", prCommit)

	git(t, root, "checkout", "-b", "merge-branch")
	git(t, root, "merge", "--no-ff", "--no-edit", "main")

	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Repository.DefaultBranch = "pr/feature"
	})
	rewriteRegisteredArtifact(t, root, "architecture", "# Hand Edited Architecture\n")
	refreshProjectDigests(t, root)

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	errorCode := jsonString(t, stdout, "error", "code")
	if (code != 3 && code != 4) || stderr != "" || (errorCode != "project.invalid_configuration" && errorCode != "artifact.operation_missing") {
		t.Fatalf("canonical remote merge head retarget = code %d stdout %s stderr %s", code, stdout, stderr)
	}
}

func TestSelfHostingApprovedRepositoryConfigReadErrorFailsClosed(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)

	blob := gitOutput(t, root, "rev-parse", "refs/heads/main:.protobot/project.yaml")
	objectPath := filepath.Join(root, ".git", "objects", blob[:2], blob[2:])
	if err := os.Chmod(objectPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objectPath, []byte("corrupt-object"), 0o644); err != nil {
		t.Fatal(err)
	}

	git(t, root, "checkout", "-b", "pr/feature")
	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	errorCode := jsonString(t, stdout, "error", "code")
	if code == 0 || (errorCode != "git.read_failed" && errorCode != "revision.read_failed") {
		t.Fatalf("read error on first candidate must fail closed: code %d stdout %s stderr %s", code, stdout, stderr)
	}
}

func TestSelfHostingApprovedRegistryMatchesDefaultBranchWithoutOperations(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)
	git(t, root, "checkout", "-b", "pr/no-artifact-change")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingArtifactUnregistrationFailsCheck(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)
	git(t, root, "checkout", "-b", "pr/unregister-architecture")

	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		kept := config.Artifacts[:0]
		for _, artifact := range config.Artifacts {
			if artifact.ID != "architecture" {
				kept = append(kept, artifact)
			}
		}
		config.Artifacts = kept
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "artifact.unregistered" {
		t.Fatalf("unregistered artifact = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "artifact.unregistered", "artifacts[id=architecture]") {
		t.Fatalf("check omitted artifact.unregistered for architecture: %s", stdout)
	}
}

func TestSelfHostingMetadataOnlyArtifactRegistryEditDoesNotRequireOperation(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	mergeIntoMain(t, root, branch)
	git(t, root, "checkout", "-b", "pr/metadata-only")

	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		for index, artifact := range config.Artifacts {
			if artifact.ID == "architecture" {
				config.Artifacts[index].Validator = "markdownlint"
			}
		}
	})

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingNewArtifactPathRequiresOperation(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	writeTestFile(t, root, "docs/extra.md", "# Extra\n")
	digest, err := specvalidation.CanonicalTextDigest([]byte("# Extra\n"))
	if err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		config.Artifacts = append(config.Artifacts, records.ArtifactEntry{
			ID: "extra", Kind: records.ArtifactInterfaceProse, Path: "docs/extra.md",
			Digest: digest, Owner: "user",
		})
	})
	appendSharedProjection(t, root, "docs/extra.md")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	if code != 4 || stderr != "" || jsonString(t, stdout, "error", "code") != "artifact.operation_missing" {
		t.Fatalf("new artifact path = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "artifact.operation_missing", "artifacts[id=extra]") {
		t.Fatalf("check omitted artifact.operation_missing for extra: %s", stdout)
	}

	addProposedArtifactOperation(t, root, "CS-00001", "add", "extra")
	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func TestSelfHostingFixtureDetectsDirectArtifactEditWithoutStaging(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	writeTestFile(t, root, "unrelated.txt", "not a specification record\n")
	writeTestFile(t, root, "docs/vision.md", "# Edited outside ears-manager\n")
	before := gitOutput(t, root, "status", "--porcelain", "--untracked-files=all")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	if !strings.Contains(stdout, `"code":"artifact.digest_mismatch"`) {
		t.Fatalf("check omitted artifact.digest_mismatch: %s", stdout)
	}
	if jsonString(t, stdout, "error", "mutation") != "none" {
		t.Fatalf("check claimed a mutation: %s", stdout)
	}

	after := gitOutput(t, root, "status", "--porcelain", "--untracked-files=all")
	if after != before {
		t.Fatalf("check changed git status\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(after, "?? unrelated.txt") {
		t.Fatalf("unrelated untracked file was staged or removed: %q", after)
	}
	if staged := gitOutput(t, root, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("check staged files: %q", staged)
	}
}

func TestSelfHostingFixtureHumanAndJSONShareDiagnosticCode(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	writeTestFile(t, root, "docs/vision.md", "# Edited outside ears-manager\n")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	if jsonString(t, stdout, "error", "code") != "validation.failed" || jsonString(t, stdout, "error", "retry") != "revise-request" {
		t.Fatalf("JSON envelope drifted: %s", stdout)
	}

	humanCode, humanStdout, humanStderr := runCLI(nil, "check")
	if humanCode != 4 || humanStdout != "" {
		t.Fatalf("human check = exit %d stdout %q stderr %q", humanCode, humanStdout, humanStderr)
	}
	if !strings.Contains(humanStderr, "error[validation.failed]:") {
		t.Fatalf("human diagnostic omitted the JSON error code: %s", humanStderr)
	}
	if !strings.Contains(humanStderr, "artifacts[id=vision].digest") || !strings.Contains(humanStderr, "retry: revise-request") {
		t.Fatalf("human diagnostic omitted the path or retry: %s", humanStderr)
	}
}

func TestSelfHostingFixtureDetectsUngovernedStoreEdit(t *testing.T) {
	root := newSelfHostingCheckout(t)
	t.Chdir(root)
	writeTestFile(t, root, ".protobot/requirements/REQ-FIX-00001.yaml", "id: REQ-FIX-00001\n")

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	if !hasDiagnostic(t, stdout, "project.store_digest_mismatch", "store_digests.requirements") {
		t.Fatalf("check omitted the store digest mismatch: %s", stdout)
	}
}

func TestSelfHostingSeedMatchesGovernedInitialization(t *testing.T) {
	source := selfHostingRoot(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"docs/vision.md", "docs/architecture.md"} {
		writeTestFile(t, root, rel, readTestFile(t, source, rel))
	}
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Test User")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "existing repository")
	git(t, root, "checkout", "-b", "cs/00001-project-init")
	t.Chdir(root)

	initializeSelfHostingProject(t)

	gotProjection := readTestFile(t, root, ".protobot/projection.yaml")
	wantProjection := readTestFile(t, source, ".protobot/projection.yaml")
	if gotProjection != wantProjection {
		t.Fatalf("projection.yaml from project init did not match the committed seed\ngot:\n%s\nwant:\n%s", gotProjection, wantProjection)
	}

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)

	var gotProject, wantProject records.ProjectConfig
	if err := storage.ReadFile(filepath.Join(root, ".protobot", "project.yaml"), &gotProject); err != nil {
		t.Fatal(err)
	}
	if err := storage.ReadFile(filepath.Join(source, ".protobot", "project.yaml"), &wantProject); err != nil {
		t.Fatal(err)
	}
	// Store digests change when later proposed change sets are added to
	// the committed seed. Differing base commits also make store digests
	// differ: the committed seed's CS-00001 records base commits in
	// .protobot/change-sets/cs-00001.yaml while governed initialization
	// creates a temporary-repository base. Store digests include canonical
	// manifest contents, so they can differ even when the store contains
	// only the seed manifest. Governed initialization still has to reproduce
	// CS-00001 and the rest of the project configuration.
	gotProject = isolateSeedProject(gotProject)
	wantProject = isolateSeedProject(wantProject)
	if !reflect.DeepEqual(gotProject, wantProject) {
		t.Fatalf("project configuration from governed init differs:\ngot: %#v\nwant: %#v", gotProject, wantProject)
	}

	assertSeedChangeSetMatches(t,
		filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml"),
		filepath.Join(source, ".protobot", "change-sets", "cs-00001.yaml"),
		"change-set record from governed writes differs",
	)
	for _, rel := range []string{
		"interfaces/protobot-cli.yaml",
		"requirements/REQ-CLI-00001.yaml",
	} {
		path := filepath.Join(".protobot", rel)
		if got, want := readTestFile(t, root, path), readTestFile(t, source, path); got != want {
			t.Fatalf("governed record %s differs from the committed seed\ngot:\n%s\nwant:\n%s", rel, got, want)
		}
	}

	committed := readTestFile(t, source, ".protobot/project.yaml")
	for _, rel := range []string{"docs/vision.md", "docs/architecture.md"} {
		digest, err := specvalidation.CanonicalTextDigest([]byte(readTestFile(t, source, rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(committed, digest) || !strings.Contains(readTestFile(t, root, ".protobot/project.yaml"), digest) {
			t.Fatalf("artifact digest for %s is missing from the seed or from a fresh init: %s", rel, digest)
		}
	}
}

func TestCommittedSeedDigestsMatchRegisteredArtifacts(t *testing.T) {
	source := selfHostingRoot(t)
	projectYAML := readTestFile(t, source, ".protobot/project.yaml")
	for _, rel := range []string{"docs/vision.md", "docs/architecture.md"} {
		digest, err := specvalidation.CanonicalTextDigest([]byte(readTestFile(t, source, rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(projectYAML, digest) {
			t.Fatalf("committed project.yaml does not record the digest of %s (%s)", rel, digest)
		}
	}
}

func TestSelfHostingSeedMatchesWorkflowTrustAnchor(t *testing.T) {
	source := selfHostingRoot(t)
	var project records.ProjectConfig
	if err := storage.ReadFile(filepath.Join(source, ".protobot", "project.yaml"), &project); err != nil {
		t.Fatal(err)
	}
	if project.Repository.CanonicalRemote != "https://github.com/redhat-et/ProtoBot.git" || project.Repository.DefaultBranch != "main" {
		t.Fatalf("workflow fetch target and project configuration diverged: remote=%q branch=%q", project.Repository.CanonicalRemote, project.Repository.DefaultBranch)
	}
	storePath := project.Stores.WithDefaults().ChangeSets
	if storePath != ".protobot/change-sets" {
		t.Fatalf("workflow manifest path and configured store diverged: %q", storePath)
	}
	projectYAML := readTestFile(t, source, ".protobot/project.yaml")
	if !strings.Contains(projectYAML, "\n    change_sets: "+storePath+"\n") {
		t.Fatalf("project.yaml store encoding no longer matches the CI parser for %q", storePath)
	}
	workflow := readTestFile(t, source, ".github/workflows/ci-workflow.yaml")
	fetch := "git fetch --no-tags " + project.Repository.CanonicalRemote + " refs/heads/" + project.Repository.DefaultBranch
	if !strings.Contains(workflow, fetch) || !strings.Contains(workflow, "main_store="+storePath) {
		t.Fatalf("workflow trust anchor does not match project configuration: fetch=%q store=%q", fetch, storePath)
	}
}

func TestSelfHostingMultipleProposedChangeSets(t *testing.T) {
	source, extra := selfHostingSourceWithExtraChangeSet(t)
	root := newSelfHostingCheckoutFrom(t, source)
	t.Chdir(root)

	// Approved seed manifests remain on the fixture's default branch and
	// preserve approved historical status.
	code, stdout, stderr := runCLI(nil, "--output", "json", "change-set", "show", "--change-set", "CS-00001")
	assertSuccess(t, code, stdout, stderr)
	if status := jsonString(t, stdout, "data", "status"); status != "approved" {
		t.Fatalf("CS-00001 status = %q, want approved", status)
	}

	var extraCS records.ChangeSet
	if err := storage.ReadFile(filepath.Join(root, filepath.FromSlash(extra)), &extraCS); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "show", "--change-set", extraCS.ID)
	assertSuccess(t, code, stdout, stderr)
	if status := jsonString(t, stdout, "data", "status"); status != "proposed" {
		t.Fatalf("%s status = %q, want proposed", extraCS.ID, status)
	}

	copied := selfHostingChangeSetRelPaths(t, root)
	if !containsString(copied, extra) {
		t.Fatalf("newSelfHostingCheckout omitted %s from %v", extra, copied)
	}
	if len(copied) < 2 {
		t.Fatalf("copied change-set manifests = %v, want at least two change sets", copied)
	}

	// Active proposals have base commits pinned to the fixture default branch head.
	base := gitOutput(t, root, "rev-parse", "refs/heads/main")
	if extraCS.BaseCommit != base || extraCS.ImpactAssessmentBaseCommit != base {
		t.Fatalf("%s base_commit=%q impact_assessment_base_commit=%q, want both %q", extra, extraCS.BaseCommit, extraCS.ImpactAssessmentBaseCommit, base)
	}

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
	var result struct {
		Data struct {
			RecordCounts struct {
				Requirements int `json:"requirements"`
				Interfaces   int `json:"interfaces"`
				ChangeSets   int `json:"change_sets"`
				Artifacts    int `json:"artifacts"`
			} `json:"record_counts"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode check result: %v", err)
	}
	reqRels, ifaceRels := referencedRecordsForChangeSets(t, source, copied)
	if got := result.Data.RecordCounts; got.Requirements != len(reqRels) || got.Interfaces != len(ifaceRels) || got.ChangeSets != len(copied) || got.Artifacts != 2 {
		t.Fatalf("check record counts = %#v, want %d requirement(s), %d interface(s), %d change set(s), and two artifacts", got, len(reqRels), len(ifaceRels), len(copied))
	}

	assertSeedChangeSetMatches(t,
		filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml"),
		filepath.Join(selfHostingRoot(t), ".protobot", "change-sets", "cs-00001.yaml"),
		"CS-00001 from a multi-change-set checkout differs from the committed seed",
	)
}

func TestSelfHostingCheckoutWithoutDefaultBranchRefs(t *testing.T) {
	// Simulate an environment where default-branch refs are absent in the source checkout.
	// When default-branch refs cannot be resolved, approval cannot be inferred
	// from branch refs, so the fallback preserves the full approved set from
	// source git tracking while explicit partitioning remains supported.
	source, approvedRels, proposedRels, approvedSyntheticID, proposedSyntheticID := selfHostingSourceWithApprovedAndProposedChangeSets(t)

	// Use environment-isolated Git runner for destructive ref deletion.
	isolatedGit(t, source, "update-ref", "-d", "refs/heads/main")
	if hasAnyRef(source, "refs/heads/main", "refs/remotes/origin/main") {
		t.Fatal("source still has default branch refs")
	}

	// Dynamic resolution on this source must fail loudly rather than guessing.
	if _, err := resolveSourceDefaultBranchRef(source); err == nil {
		t.Fatal("resolveSourceDefaultBranchRef succeeded on a source without default-branch refs")
	}

	// Without explicit partition, building a checkout from a ref-less source
	// falls back to preserving the full tracked approved history rather than
	// fataling or demoting later approved history to proposed.
	fallbackRoot := newSelfHostingCheckoutFrom(t, source)
	t.Chdir(fallbackRoot)
	assertSelfHostingProposalBasesPinned(t, fallbackRoot, proposedRels)

	for _, rel := range approvedRels {
		id := changeSetIDFromRelPath(t, source, rel)
		code, stdout, stderr := runCLI(nil, "--output", "json", "change-set", "show", "--change-set", id)
		assertSuccess(t, code, stdout, stderr)
		if status := jsonString(t, stdout, "data", "status"); status != "approved" {
			t.Fatalf("fallback %s status = %q, want approved", id, status)
		}
	}
	code, stdout, stderr := runCLI(nil, "--output", "json", "change-set", "show", "--change-set", approvedSyntheticID)
	assertSuccess(t, code, stdout, stderr)
	if status := jsonString(t, stdout, "data", "status"); status != "approved" {
		t.Fatalf("fallback %s status = %q, want approved", approvedSyntheticID, status)
	}

	for _, rel := range proposedRels {
		id := changeSetIDFromRelPath(t, source, rel)
		code, stdout, stderr := runCLI(nil, "--output", "json", "change-set", "show", "--change-set", id)
		assertSuccess(t, code, stdout, stderr)
		if status := jsonString(t, stdout, "data", "status"); status != "proposed" {
			t.Fatalf("fallback %s status = %q, want proposed", id, status)
		}
	}
	code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "show", "--change-set", proposedSyntheticID)
	assertSuccess(t, code, stdout, stderr)
	if status := jsonString(t, stdout, "data", "status"); status != "proposed" {
		t.Fatalf("fallback %s status = %q, want proposed", proposedSyntheticID, status)
	}

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)

	// Explicit partition with the derived approved/proposed lists also works.
	root := buildSelfHostingCheckoutWithPartition(t, source, approvedRels, proposedRels)
	t.Chdir(root)
	assertSelfHostingProposalBasesPinned(t, root, proposedRels)

	for _, rel := range approvedRels {
		id := changeSetIDFromRelPath(t, source, rel)
		code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "show", "--change-set", id)
		assertSuccess(t, code, stdout, stderr)
		if status := jsonString(t, stdout, "data", "status"); status != "approved" {
			t.Fatalf("explicit partition %s status = %q, want approved", id, status)
		}
	}

	for _, rel := range proposedRels {
		id := changeSetIDFromRelPath(t, source, rel)
		code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "show", "--change-set", id)
		assertSuccess(t, code, stdout, stderr)
		if status := jsonString(t, stdout, "data", "status"); status != "proposed" {
			t.Fatalf("explicit partition %s status = %q, want proposed", id, status)
		}
	}

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertSuccess(t, code, stdout, stderr)
}

func changeSetIDFromRelPath(t *testing.T, root, rel string) string {
	t.Helper()
	var cs records.ChangeSet
	if err := storage.ReadFile(filepath.Join(root, filepath.FromSlash(rel)), &cs); err != nil {
		t.Fatal(err)
	}
	return cs.ID
}

func assertSelfHostingProposalBasesPinned(t *testing.T, root string, proposedRels []string) {
	t.Helper()
	base := isolatedGitOutput(t, root, "rev-parse", "refs/heads/main")
	for _, rel := range proposedRels {
		var cs records.ChangeSet
		if err := storage.ReadFile(filepath.Join(root, filepath.FromSlash(rel)), &cs); err != nil {
			t.Fatal(err)
		}
		if cs.BaseCommit != base || cs.ImpactAssessmentBaseCommit != base {
			t.Fatalf("%s base_commit=%q impact_assessment_base_commit=%q, want both %q", rel, cs.BaseCommit, cs.ImpactAssessmentBaseCommit, base)
		}
	}
}

func isolatedGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := gitCommand(append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func isolatedGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := gitCommand(append([]string{"-C", root}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func newSelfHostingCheckout(t *testing.T) string {
	t.Helper()
	return newSelfHostingCheckoutFrom(t, hermeticSelfHostingSource(t))
}

func newSelfHostingCheckoutFrom(t *testing.T, source string) string {
	t.Helper()
	return buildSelfHostingCheckout(t, source, selfHostingChangeSetRelPaths(t, source), true)
}

func newSelfHostingSeedProposedCheckout(t *testing.T) string {
	t.Helper()
	return buildSelfHostingCheckout(t, selfHostingRoot(t), []string{".protobot/change-sets/cs-00001.yaml"}, false)
}

func hermeticSelfHostingSource(t *testing.T) string {
	t.Helper()
	origin := selfHostingRoot(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	isolatedGit(t, root, "init", "-b", "main")
	isolatedGit(t, root, "config", "user.email", "test@example.invalid")
	isolatedGit(t, root, "config", "user.name", "Test User")

	for _, rel := range []string{"docs/vision.md", "docs/architecture.md"} {
		writeTestFile(t, root, rel, readTestFile(t, origin, rel))
	}
	writeTestFile(t, root, ".protobot/project.yaml", readTestFile(t, origin, ".protobot/project.yaml"))
	writeTestFile(t, root, ".protobot/projection.yaml", readTestFile(t, origin, ".protobot/projection.yaml"))

	allManifests := selfHostingChangeSetRelPaths(t, origin)
	approvedManifests, proposedManifests := partitionChangeSets(origin, allManifests)

	if len(approvedManifests) > 0 {
		appReqs, appIfaces := referencedRecordsForChangeSets(t, origin, approvedManifests)
		for _, rel := range append(append(approvedManifests, appReqs...), appIfaces...) {
			writeTestFile(t, root, rel, readTestFile(t, origin, rel))
		}
		refreshProjectDigests(t, root)
		isolatedGit(t, root, "add", ".")
		isolatedGit(t, root, "commit", "-m", "approved specification records")
	}

	if len(proposedManifests) > 0 {
		propReqs, propIfaces := referencedRecordsForChangeSets(t, origin, proposedManifests)
		for _, rel := range append(append(proposedManifests, propReqs...), propIfaces...) {
			writeTestFile(t, root, rel, readTestFile(t, origin, rel))
		}
		refreshProjectDigests(t, root)
	}
	return root
}

func buildSelfHostingCheckout(t *testing.T, source string, manifests []string, splitApproved bool) string {
	t.Helper()
	var approvedManifests, proposedManifests []string
	if splitApproved {
		approvedManifests, proposedManifests = partitionChangeSets(source, manifests)
	} else {
		// Keep requested change sets on the proposed PR branch so lifecycle
		// tests can exercise their proposal workflow.
		proposedManifests = manifests
	}
	return buildSelfHostingCheckoutWithPartition(t, source, approvedManifests, proposedManifests)
}

func partitionChangeSets(source string, manifests []string) ([]string, []string) {
	ref, err := resolveSourceDefaultBranchRef(source)
	var approved, proposed []string
	if err == nil {
		for _, rel := range manifests {
			if isChangeSetApprovedInTree(source, ref, rel) {
				approved = append(approved, rel)
			} else {
				proposed = append(proposed, rel)
			}
		}
	} else {
		for _, rel := range manifests {
			if isChangeSetTracked(source, rel) {
				approved = append(approved, rel)
			} else {
				proposed = append(proposed, rel)
			}
		}
	}
	return approved, proposed
}

func buildSelfHostingCheckoutWithPartition(t *testing.T, source string, approvedManifests, proposedManifests []string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"docs/vision.md", "docs/architecture.md"} {
		writeTestFile(t, root, rel, readTestFile(t, source, rel))
	}
	isolatedGit(t, root, "init", "-b", "main")
	isolatedGit(t, root, "config", "user.email", "test@example.invalid")
	isolatedGit(t, root, "config", "user.name", "Test User")
	isolatedGit(t, root, "add", ".")
	isolatedGit(t, root, "commit", "-m", "registered artifacts")

	// Write project and projection configs
	for _, rel := range []string{".protobot/project.yaml", ".protobot/projection.yaml"} {
		writeTestFile(t, root, rel, readTestFile(t, source, rel))
	}

	if len(approvedManifests) > 0 {
		appReqs, appIfaces := referencedRecordsForChangeSets(t, source, approvedManifests)
		for _, rel := range append(append(approvedManifests, appReqs...), appIfaces...) {
			writeTestFile(t, root, rel, readTestFile(t, source, rel))
		}
		refreshProjectDigests(t, root)
		isolatedGit(t, root, "add", ".protobot")
		isolatedGit(t, root, "commit", "-m", "approved specification records")
	}

	isolatedGit(t, root, "checkout", "-b", "pr/self-hosting-seed")
	if len(proposedManifests) > 0 {
		propReqs, propIfaces := referencedRecordsForChangeSets(t, source, proposedManifests)
		for _, rel := range append(append(proposedManifests, propReqs...), propIfaces...) {
			writeTestFile(t, root, rel, readTestFile(t, source, rel))
		}
		pinSelfHostingSeedBase(t, root)
		isolatedGit(t, root, "add", ".protobot")
		isolatedGit(t, root, "commit", "-m", "propose self-hosting change sets")
	}
	return root
}

func resolveSourceDefaultBranchRef(source string) (string, error) {
	if hasAnyRef(source, "refs/heads/main") {
		return "refs/heads/main", nil
	}
	if hasAnyRef(source, "refs/remotes/origin/main") {
		return "refs/remotes/origin/main", nil
	}
	// Check canonical remote tracking branch if configured in project.yaml
	var config records.ProjectConfig
	if err := storage.ReadFile(filepath.Join(source, ".protobot", "project.yaml"), &config); err == nil {
		if branch := strings.TrimSpace(config.Repository.DefaultBranch); branch != "" {
			if remotes, err := canonicalRemoteNames(source, config.Repository.CanonicalRemote); err == nil {
				for _, remote := range remotes {
					ref := "refs/remotes/" + remote + "/" + branch
					if hasAnyRef(source, ref) {
						return ref, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("repository default branch could not be resolved in %s", source)
}

func hasAnyRef(root string, refs ...string) bool {
	for _, ref := range refs {
		cmd := gitCommand("-C", root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

func referencedRecordsForChangeSets(t *testing.T, source string, changeSetRels []string) ([]string, []string) {
	t.Helper()
	seenReqs := make(map[string]bool)
	seenIfaces := make(map[string]bool)
	reqRels := make([]string, 0)
	ifaceRels := make([]string, 0)

	for _, rel := range changeSetRels {
		manifestPath := filepath.Join(source, filepath.FromSlash(rel))
		var cs records.ChangeSet
		if err := storage.ReadFile(manifestPath, &cs); err != nil {
			t.Fatal(err)
		}
		for _, op := range cs.Operations {
			if op.RequirementID != "" && !seenReqs[op.RequirementID] {
				seenReqs[op.RequirementID] = true
				filename, err := records.FilenameFor(records.RequirementStore, op.RequirementID)
				if err != nil {
					t.Fatal(err)
				}
				p := filepath.ToSlash(filepath.Join(".protobot", "requirements", filename))
				if _, err := os.Stat(filepath.Join(source, filepath.FromSlash(p))); err == nil {
					reqRels = append(reqRels, p)
				}
			}
		}
		for _, op := range cs.InterfaceOperations {
			if op.InterfaceID != "" && !seenIfaces[op.InterfaceID] {
				seenIfaces[op.InterfaceID] = true
				filename, err := records.FilenameFor(records.InterfaceStore, op.InterfaceID)
				if err != nil {
					t.Fatal(err)
				}
				p := filepath.ToSlash(filepath.Join(".protobot", "interfaces", filename))
				if _, err := os.Stat(filepath.Join(source, filepath.FromSlash(p))); err == nil {
					ifaceRels = append(ifaceRels, p)
				}
			}
		}
		for _, ifaceID := range cs.AffectedInterfaces {
			if ifaceID != "" && !seenIfaces[ifaceID] {
				seenIfaces[ifaceID] = true
				filename, err := records.FilenameFor(records.InterfaceStore, ifaceID)
				if err != nil {
					t.Fatal(err)
				}
				p := filepath.ToSlash(filepath.Join(".protobot", "interfaces", filename))
				if _, err := os.Stat(filepath.Join(source, filepath.FromSlash(p))); err == nil {
					ifaceRels = append(ifaceRels, p)
				}
			}
		}
	}
	sort.Strings(reqRels)
	sort.Strings(ifaceRels)
	return reqRels, ifaceRels
}

func assertSeedChangeSetMatches(t *testing.T, gotPath, wantPath, failureContext string) {
	t.Helper()
	var gotChangeSet, wantChangeSet records.ChangeSet
	if err := storage.ReadFile(gotPath, &gotChangeSet); err != nil {
		t.Fatal(err)
	}
	if err := storage.ReadFile(wantPath, &wantChangeSet); err != nil {
		t.Fatal(err)
	}
	gotChangeSet.BaseCommit, wantChangeSet.BaseCommit = "", ""
	gotChangeSet.ImpactAssessmentBaseCommit, wantChangeSet.ImpactAssessmentBaseCommit = "", ""
	if !reflect.DeepEqual(gotChangeSet, wantChangeSet) {
		t.Fatalf("%s:\ngot: %#v\nwant: %#v", failureContext, gotChangeSet, wantChangeSet)
	}
}

func selfHostingChangeSetRelPaths(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".protobot", "change-sets"))
	if err != nil {
		t.Fatal(err)
	}
	relPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".yaml" {
			continue
		}
		relPaths = append(relPaths, filepath.ToSlash(filepath.Join(".protobot", "change-sets", name)))
	}
	if len(relPaths) == 0 {
		t.Fatal("no change-set manifests matching .protobot/change-sets/*.yaml")
	}
	sort.Strings(relPaths)
	return relPaths
}

// pinSelfHostingSeedBase rewrites every copied proposed change-set
// base_commit and impact_assessment_base_commit to the local
// default-branch head so check can evaluate Git freshness in the temp repo.
func pinSelfHostingSeedBase(t *testing.T, root string) {
	t.Helper()
	pinSelfHostingChangeSetsExcept(t, root)
}

func pinSelfHostingChangeSetsExcept(t *testing.T, root string, skipIDs ...string) {
	t.Helper()
	base := isolatedGitOutput(t, root, "rev-parse", "refs/heads/main")
	skip := make(map[string]bool, len(skipIDs))
	for _, id := range skipIDs {
		skip[id] = true
	}
	pinned := false
	for _, rel := range selfHostingChangeSetRelPaths(t, root) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		var changeSet records.ChangeSet
		if err := storage.ReadFile(path, &changeSet); err != nil {
			t.Fatal(err)
		}
		if skip[changeSet.ID] {
			continue
		}
		if isChangeSetApprovedInTree(root, "refs/heads/main", rel) {
			continue
		}
		changeSet.BaseCommit = base
		changeSet.ImpactAssessmentBaseCommit = base
		data, err := storage.Encode(changeSet)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		pinned = true
	}
	if pinned {
		refreshProjectDigests(t, root)
	}
}

func isChangeSetApprovedInTree(root, ref, relPath string) bool {
	cmd := gitCommand("-C", root, "cat-file", "-e", ref+":"+filepath.ToSlash(relPath))
	return cmd.Run() == nil
}

func isChangeSetTracked(root, relPath string) bool {
	slashRel := filepath.ToSlash(relPath)
	cmd := gitCommand("-C", root, "ls-files", "--error-unmatch", slashRel)
	if cmd.Run() == nil {
		return true
	}
	cmd = gitCommand("-C", root, "cat-file", "-e", "HEAD:"+slashRel)
	return cmd.Run() == nil
}

func selfHostingSourceWithExtraChangeSet(t *testing.T) (string, string) {
	t.Helper()
	root := hermeticSelfHostingSource(t)

	id, rel := nextSelfHostingChangeSetRel(t, root)
	reqID, reqRel := nextSelfHostingRequirementRel(t, root)
	req := records.Requirement{
		ID:   reqID,
		Type: "event-driven",
		Text: "When an engineer runs the self-hosting test suite, the CLI shall support multiple proposed change sets.",
		AppliesTo: records.Applicability{
			Interfaces: []string{},
			Scopes:     []string{"ears-manager"},
		},
		Verification: records.Verification{
			Mode: "isolated-interface",
		},
		Provenance: "user-authored",
		Created:    "2026-10-13T16:01:00Z",
		Status:     records.StatusActive,
	}
	reqData, err := storage.Encode(req)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, reqRel, string(reqData))

	changeSet := records.ChangeSet{
		ID:         id,
		BaseCommit: strings.Repeat("a", 40),
		Intent:     "Document an additional proposed change set",
		Operations: []records.RequirementOperation{
			{
				Action:        "add",
				RequirementID: reqID,
			},
		},
		AffectedInterfaces:         []string{},
		AffectedScopes:             []string{},
		ImplementationRequired:     false,
		ImplementationRationale:    "The additional change set exists only to exercise self-hosting fixtures.",
		ImpactAssessmentBaseCommit: strings.Repeat("a", 40),
		Created:                    "2026-10-13T16:00:00Z",
	}
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, rel, string(data))
	refreshProjectDigests(t, root)

	return root, rel
}

func selfHostingSourceWithApprovedAndProposedChangeSets(t *testing.T) (string, []string, []string, string, string) {
	t.Helper()
	root, preexistingProposal := selfHostingSourceWithExtraChangeSet(t)
	approvedRels, proposedRels := partitionChangeSets(root, selfHostingChangeSetRelPaths(t, root))
	if !containsString(proposedRels, preexistingProposal) {
		t.Fatalf("pre-existing proposal %s was not partitioned as proposed: %v", preexistingProposal, proposedRels)
	}
	if isChangeSetApprovedInTree(root, "refs/heads/main", preexistingProposal) || isChangeSetTracked(root, preexistingProposal) {
		t.Fatalf("pre-existing proposal %s is not an untracked proposal on the source", preexistingProposal)
	}

	// Add a synthetic change set as later approved history on main.
	approvedID, approvedRel := nextSelfHostingChangeSetRel(t, root)
	approvedRequirementID, approvedRequirementRel := nextSelfHostingRequirementRel(t, root)
	proposalReqs, proposalIfaces := referencedRecordsForChangeSets(t, root, proposedRels)
	proposalFiles := append(append(append([]string{}, proposedRels...), proposalReqs...), proposalIfaces...)
	// Keep existing untracked proposals out of the approved commit and its store digests.
	type hiddenFile struct {
		rel  string
		data []byte
	}
	var hiddenProposals []hiddenFile
	for _, rel := range proposalFiles {
		if isChangeSetTracked(root, rel) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		hiddenProposals = append(hiddenProposals, hiddenFile{rel: rel, data: data})
	}

	approvedRequirement := records.Requirement{
		ID:   approvedRequirementID,
		Type: "event-driven",
		Text: "When an engineer inspects specification status, the CLI shall report approved change sets.",
		AppliesTo: records.Applicability{
			Interfaces: []string{},
			Scopes:     []string{"ears-manager"},
		},
		Verification: records.Verification{
			Mode: "isolated-interface",
		},
		Provenance: "user-authored",
		Created:    "2026-10-13T16:01:00Z",
		Status:     records.StatusActive,
	}
	approvedRequirementData, err := storage.Encode(approvedRequirement)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, approvedRequirementRel, string(approvedRequirementData))

	approvedChangeSet := records.ChangeSet{
		ID:         approvedID,
		BaseCommit: strings.Repeat("a", 40),
		Intent:     "Document a previously approved historical change set",
		Operations: []records.RequirementOperation{
			{
				Action:        "add",
				RequirementID: approvedRequirementID,
			},
		},
		AffectedInterfaces:         []string{},
		AffectedScopes:             []string{},
		ImplementationRequired:     false,
		ImplementationRationale:    "This change set exercises later approved history in self-hosting fixtures.",
		ImpactAssessmentBaseCommit: strings.Repeat("a", 40),
		Created:                    "2026-10-13T16:00:00Z",
	}
	approvedChangeSetData, err := storage.Encode(approvedChangeSet)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, approvedRel, string(approvedChangeSetData))
	refreshProjectDigests(t, root)

	// Commit the synthetic set to main so it becomes approved history.
	isolatedGit(t, root, "add", ".protobot")
	isolatedGit(t, root, "commit", "-m", "approved specification records for "+approvedID)
	approvedRels = append(approvedRels, approvedRel)
	for _, file := range hiddenProposals {
		path := filepath.Join(root, filepath.FromSlash(file.rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refreshProjectDigests(t, root)

	// Add a synthetic active proposed change set in the working tree.
	proposedID, proposedRel := nextSelfHostingChangeSetRel(t, root)
	proposedRequirementID, proposedRequirementRel := nextSelfHostingRequirementRel(t, root)
	proposedRequirement := records.Requirement{
		ID:   proposedRequirementID,
		Type: "event-driven",
		Text: "When an engineer adds a proposed change set, the CLI shall support multiple proposed change sets.",
		AppliesTo: records.Applicability{
			Interfaces: []string{},
			Scopes:     []string{"ears-manager"},
		},
		Verification: records.Verification{
			Mode: "isolated-interface",
		},
		Provenance: "user-authored",
		Created:    "2026-10-13T16:03:00Z",
		Status:     records.StatusActive,
	}
	proposedRequirementData, err := storage.Encode(proposedRequirement)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, proposedRequirementRel, string(proposedRequirementData))

	proposedChangeSet := records.ChangeSet{
		ID:         proposedID,
		BaseCommit: strings.Repeat("a", 40),
		Intent:     "Document an active proposed change set",
		Operations: []records.RequirementOperation{
			{
				Action:        "add",
				RequirementID: proposedRequirementID,
			},
		},
		AffectedInterfaces:         []string{},
		AffectedScopes:             []string{},
		ImplementationRequired:     false,
		ImplementationRationale:    "This change set exercises active proposals in self-hosting fixtures.",
		ImpactAssessmentBaseCommit: strings.Repeat("a", 40),
		Created:                    "2026-10-13T16:02:00Z",
	}
	proposedChangeSetData, err := storage.Encode(proposedChangeSet)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, proposedRel, string(proposedChangeSetData))
	refreshProjectDigests(t, root)
	proposedRels = append(proposedRels, proposedRel)
	pinSelfHostingChangeSetsExcept(t, root)

	return root, approvedRels, proposedRels, approvedID, proposedID
}

func nextSelfHostingRequirementRel(t *testing.T, root string) (string, string) {
	t.Helper()
	used := make(map[string]bool)
	matches, err := filepath.Glob(filepath.Join(root, ".protobot", "requirements", "REQ-CLI-*.yaml"))
	if err == nil {
		for _, match := range matches {
			used[filepath.Base(match)] = true
		}
	}
	for number := 2; number <= lastChangeSetNumber; number++ {
		id := fmt.Sprintf("REQ-CLI-%05d", number)
		filename, err := records.FilenameFor(records.RequirementStore, id)
		if err != nil {
			t.Fatal(err)
		}
		if used[filename] {
			continue
		}
		return id, filepath.ToSlash(filepath.Join(".protobot", "requirements", filename))
	}
	t.Fatal("no requirement sequence number is available")
	return "", ""
}

func nextSelfHostingChangeSetRel(t *testing.T, root string) (string, string) {
	t.Helper()
	used := make(map[string]bool)
	for _, rel := range selfHostingChangeSetRelPaths(t, root) {
		used[filepath.Base(rel)] = true
	}
	for number := 2; number <= lastChangeSetNumber; number++ {
		id := fmt.Sprintf("CS-%05d", number)
		filename, err := records.FilenameFor(records.ChangeSetStore, id)
		if err != nil {
			t.Fatal(err)
		}
		if used[filename] {
			continue
		}
		return id, filepath.ToSlash(filepath.Join(".protobot", "change-sets", filename))
	}
	t.Fatal("no change-set sequence number is available")
	return "", ""
}

func isolateSeedProject(config records.ProjectConfig) records.ProjectConfig {
	config.StoreDigests = records.StoreDigests{}
	return config
}

func initializeSelfHostingProject(t *testing.T) {
	t.Helper()

	code, stdout, stderr := runCLI(nil, "--output", "json", "project", "init",
		"--id", "protobot",
		"--name", "ProtoBot",
		"--canonical-remote", "https://github.com/redhat-et/ProtoBot.git",
		"--review-mode", "multi-player",
	)
	assertSuccess(t, code, stdout, stderr)

	code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "create",
		"--intent", "Seed ProtoBot's initial specification records",
		"--affected-scope", "ears-manager",
		"--implementation-required", "false",
		"--implementation-rationale", "This initial record set specifies the existing CLI boundary and does not change implementation behavior.",
		"--created", "2026-10-05T16:00:00Z",
	)
	assertSuccess(t, code, stdout, stderr)

	code, stdout, stderr = runCLI(nil, "--output", "json", "interface", "add",
		"--change-set", "CS-00001",
		"--id", "protobot-cli",
		"--name", "ProtoBot CLI",
		"--type", "cli",
		"--spec-approach", "prose",
		"--description", "Command-line interface for project governance and specification validation.",
		"--created", "2026-10-05T16:01:00Z",
	)
	assertSuccess(t, code, stdout, stderr)

	code, stdout, stderr = runCLI(nil, "--output", "json", "change-set", "update",
		"--change-set", "CS-00001",
		"--affected-interface", "protobot-cli",
		"--implementation-required", "false",
		"--implementation-rationale", "This initial record set specifies the existing CLI boundary and does not change implementation behavior.",
	)
	assertSuccess(t, code, stdout, stderr)

	code, stdout, stderr = runCLI(nil, "--output", "json", "requirement", "add",
		"--change-set", "CS-00001",
		"--id", "REQ-CLI-00001",
		"--type", "event-driven",
		"--text", "When a contributor requests a specification check, the CLI shall validate the registered artifacts and structured records.",
		"--interface", "protobot-cli",
		"--scope", "ears-manager",
		"--verification-mode", "isolated-interface",
		"--provenance", "user-authored",
		"--created", "2026-10-05T16:02:00Z",
	)
	assertSuccess(t, code, stdout, stderr)
}

func selfHostingRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func rewriteRegisteredArtifact(t *testing.T, root, id, content string) {
	t.Helper()
	digest, err := specvalidation.CanonicalTextDigest([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	var path string
	rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
		for index, artifact := range config.Artifacts {
			if artifact.ID != id {
				continue
			}
			path = artifact.Path
			config.Artifacts[index].Digest = digest
			return
		}
		t.Fatalf("artifact %s is not registered", id)
	})
	writeTestFile(t, root, path, content)
}

func addProposedArtifactOperation(t *testing.T, root, changeSetID, action, artifactID string) {
	t.Helper()
	filename, err := records.FilenameFor(records.ChangeSetStore, changeSetID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".protobot", "change-sets", filename)
	var changeSet records.ChangeSet
	if err := storage.ReadFile(path, &changeSet); err != nil {
		t.Fatal(err)
	}
	changeSet.ArtifactOperations = append(changeSet.ArtifactOperations, records.ArtifactOperation{
		Action: action, ArtifactID: artifactID,
	})
	data, err := storage.Encode(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshProjectDigests(t, root)
}

func appendSharedProjection(t *testing.T, root, path string) {
	t.Helper()
	current := specvalidation.ParseProjection([]byte(readTestFile(t, root, specvalidation.ProjectionPath)))
	data, changed, err := specvalidation.AddSharedClassifications(current.Data, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		return
	}
	if err := os.WriteFile(filepath.Join(root, specvalidation.ProjectionPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
