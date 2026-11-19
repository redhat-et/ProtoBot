package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
)

// forkAndAdvanceDefault cuts a pr/fork branch from the current main of root,
// runs branchCommit on the PR branch, and advances the default branch with
// defaultCommit past the fork point. HEAD ends detached at the branch tip
// with refs/heads/main at the advanced default head, the shape the CI spec
// job checks. It returns the merge base, which equals the fork point.
func forkAndAdvanceDefault(t *testing.T, root string, branchCommit, defaultCommit func(root string)) string {
	t.Helper()
	git(t, root, "checkout", "-b", "pr/fork")
	forkPoint := gitOutput(t, root, "rev-parse", "refs/heads/main")

	branchCommit(root)
	git(t, root, "checkout", "main")
	defaultCommit(root)
	defaultHead := gitOutput(t, root, "rev-parse", "refs/heads/main")
	git(t, root, "checkout", "pr/fork")

	// CI detaches HEAD at the PR head and points the local default branch
	// at the fetched tip.
	git(t, root, "checkout", "--detach", "HEAD")
	git(t, root, "update-ref", "refs/heads/main", defaultHead)
	base := gitOutput(t, root, "merge-base", "refs/heads/main", "HEAD")
	if base != forkPoint {
		t.Fatalf("merge base %s, want the fork point %s", base, forkPoint)
	}
	return base
}

// newFixtureProjectWithApprovedChangeSet returns a CLI fixture checkout on
// main with one change set merged into the default branch, so scoped checks
// have an approved target that freshness does not constrain.
func newFixtureProjectWithApprovedChangeSet(t *testing.T) (string, string) {
	t.Helper()
	root := newFixtureProject(t)
	t.Chdir(root)
	changeSetID := createProposedArtifactChangeSet(t, root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	git(t, root, "add", ".protobot")
	git(t, root, "commit", "-m", "record the change set")
	mergeIntoMain(t, root, branch)
	return root, changeSetID
}

func TestCheckBaseRefComparesArtifactDeltaWithMergeBase(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	base := forkAndAdvanceDefault(t, root,
		func(root string) {
			writeTestFile(t, root, "unrelated.txt", "branch-local change\n")
			commitAll(t, root, "unrelated branch change")
		},
		func(root string) {
			// The default branch revises the registered artifact the way an
			// artifact put commit would: content and registry digest move
			// together.
			writeArtifactContent(t, root, "vision", []byte("# Advanced Fixture Vision\n"))
			commitAll(t, root, "advance the registered vision artifact")
		},
	)

	// Without the option, the branch is blamed for the artifact change that
	// the default branch made after the fork.
	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", "vision")
	assertDiagnosticText(t, stdout, "artifact.operation_missing", "artifacts[id=vision].operation",
		`Registered artifact "vision" differs from the default branch but has no operation in a proposed change set.`,
		`Add an artifact operation for "vision" to a proposed change set. If the operation is already approved on the canonical remote, reconcile the local default branch with the Source Control Manager's repo_state, then rerun check.`)

	// The branch forked before the artifact change, so the merge base holds
	// the artifact content the branch still registers.
	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--base-ref", base)
	assertSuccess(t, code, stdout, stderr)
}

func TestCheckBaseRefStillRequiresOperationForGenuineArtifactEdit(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	base := forkAndAdvanceDefault(t, root,
		func(root string) {
			writeArtifactContent(t, root, "vision", []byte("# Genuinely revised vision\n"))
			commitAll(t, root, "revise the vision artifact without an operation")
		},
		func(root string) {
			writeTestFile(t, root, "unrelated.txt", "default branch moved\n")
			commitAll(t, root, "unrelated default-branch change")
		},
	)

	// The branch itself changed the registered artifact, so the delta
	// against the merge base is real and the option must not hide it.
	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--base-ref", base)
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", "vision")
	assertDiagnosticText(t, stdout, "artifact.operation_missing", "artifacts[id=vision].operation",
		`Registered artifact "vision" differs from the base commit `+base+` but has no operation in a proposed change set.`,
		`Add an artifact operation for "vision" to a proposed change set, or rerun check without --base-ref when the delta comes from the selected base commit `+base+`.`)
}

func TestCheckBaseRefDoesNotRelaxProposedBaseFreshness(t *testing.T) {
	root := newSelfHostingSeedProposedCheckout(t)
	t.Chdir(root)
	branch := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	forkPoint := gitOutput(t, root, "rev-parse", "refs/heads/main")

	git(t, root, "checkout", "main")
	writeTestFile(t, root, "unrelated.txt", "default branch moved\n")
	commitAll(t, root, "unrelated default-branch change")
	defaultHead := gitOutput(t, root, "rev-parse", "refs/heads/main")
	git(t, root, "checkout", branch)
	git(t, root, "checkout", "--detach", "HEAD")
	git(t, root, "update-ref", "refs/heads/main", defaultHead)
	base := gitOutput(t, root, "merge-base", "refs/heads/main", "HEAD")
	if base != forkPoint {
		t.Fatalf("merge base %s, want the fork point %s", base, forkPoint)
	}

	// Freshness reads the default-branch tip, not the artifact base, so the
	// stale proposed change set still fails with the option given.
	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--base-ref", base)
	if code != 5 || stderr != "" || jsonString(t, stdout, "error", "code") != "DEFAULT_MOVED" {
		t.Fatalf("stale proposed base with --base-ref = code %d stdout %s stderr %s", code, stdout, stderr)
	}
	if !hasDiagnostic(t, stdout, "DEFAULT_MOVED", "base_commit") {
		t.Fatalf("check omitted DEFAULT_MOVED on base_commit: %s", stdout)
	}
}

func TestCheckBaseRefRefusesInvalidValues(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	head := gitOutput(t, root, "rev-parse", "HEAD")
	treeObject := gitOutput(t, root, "rev-parse", "HEAD^{tree}")
	before := gitOutput(t, root, "status", "--porcelain", "--untracked-files=all")

	for _, test := range []struct {
		name  string
		value string
		code  string
	}{
		{name: "short hash", value: head[:12], code: "revision.invalid"},
		{name: "branch name", value: "main", code: "revision.invalid"},
		// A value that begins with "-" is consumed as the option value and
		// must be refused by the full-hash check, never passed to Git.
		{name: "option-like value", value: "-x", code: "revision.invalid"},
		{name: "unknown commit", value: strings.Repeat("f", 40), code: "revision.not_found"},
		{name: "tree object", value: treeObject, code: "revision.not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--base-ref", test.value)
			assertFailureCode(t, code, stdout, stderr, 4, test.code)
			if jsonString(t, stdout, "error", "mutation") != "none" {
				t.Fatalf("check claimed a mutation: %s", stdout)
			}
		})
	}

	if after := gitOutput(t, root, "status", "--porcelain", "--untracked-files=all"); after != before {
		t.Fatalf("check changed git status\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCheckBaseRefCombinesWithChangeSetScope(t *testing.T) {
	// --change-set narrows only impact and freshness, so the scoped check
	// keeps the full artifact delta against the named base.
	root, changeSetID := newFixtureProjectWithApprovedChangeSet(t)
	base := forkAndAdvanceDefault(t, root,
		func(root string) {
			writeTestFile(t, root, "unrelated.txt", "branch-local change\n")
			commitAll(t, root, "unrelated branch change")
		},
		func(root string) {
			writeArtifactContent(t, root, "vision", []byte("# Advanced Fixture Vision\n"))
			commitAll(t, root, "advance the registered vision artifact")
		},
	)
	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--change-set", changeSetID, "--base-ref", base)
	assertSuccess(t, code, stdout, stderr)

	root, changeSetID = newFixtureProjectWithApprovedChangeSet(t)
	base = forkAndAdvanceDefault(t, root,
		func(root string) {
			writeArtifactContent(t, root, "vision", []byte("# Genuinely revised vision\n"))
			commitAll(t, root, "revise the vision artifact without an operation")
		},
		func(root string) {
			writeTestFile(t, root, "unrelated.txt", "default branch moved\n")
			commitAll(t, root, "unrelated default-branch change")
		},
	)
	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--change-set", changeSetID, "--base-ref", base)
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.operation_missing", "vision")
}

func TestCheckBaseRefStillRequiresResolvableDefaultBranch(t *testing.T) {
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
	head := gitOutput(t, root, "rev-parse", "HEAD")

	// The named base commit exists, but every check still requires a
	// resolvable default-branch head, exactly as the bare check does.
	code, stdout, stderr := runCLI(nil, "--output", "json", "check", "--base-ref", head)
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_branch_unresolved")

	code, stdout, stderr = runCLI(nil, "--output", "json", "check")
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_branch_unresolved")
}

func TestCheckBaseRefNamesTheBaseCommitInConfigurationFailures(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	originalConfig := readTestFile(t, root, ".protobot/project.yaml")

	// The default branch replaces its project configuration with a
	// directory before the fork, so the merge-base tree cannot supply a
	// base registry.
	git(t, root, "checkout", "main")
	if err := os.Remove(filepath.Join(root, ".protobot", "project.yaml")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ".protobot/project.yaml/marker", "not a project config\n")
	commitAll(t, root, "replace the default project configuration with a directory")
	brokenHead := gitOutput(t, root, "rev-parse", "refs/heads/main")

	base := forkAndAdvanceDefault(t, root,
		func(root string) {
			// The branch restores a regular project configuration.
			if err := os.RemoveAll(filepath.Join(root, ".protobot", "project.yaml")); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, root, ".protobot/project.yaml", originalConfig)
			commitAll(t, root, "restore a regular project configuration")
		},
		func(root string) {
			writeTestFile(t, root, "unrelated.txt", "default branch moved\n")
			commitAll(t, root, "unrelated default-branch change")
		},
	)
	if base != brokenHead {
		t.Fatalf("merge base %s, want the broken configuration commit %s", base, brokenHead)
	}

	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_configuration_invalid")
	if message := jsonString(t, stdout, "error", "message"); message != "The default-branch project configuration is not a regular file." {
		t.Fatalf("default-branch configuration message = %q", message)
	}

	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--base-ref", base)
	assertFailureCode(t, code, stdout, stderr, 3, "project.default_configuration_invalid")
	if message, want := jsonString(t, stdout, "error", "message"), "The project configuration of the base commit "+base+" is not a regular file."; message != want {
		t.Fatalf("base-commit configuration message = %q, want %q", message, want)
	}
}

func TestCheckBaseRefComparesUnregisteredArtifactsWithMergeBase(t *testing.T) {
	root := newFixtureProject(t)
	t.Chdir(root)
	base := forkAndAdvanceDefault(t, root,
		func(root string) {
			writeTestFile(t, root, "unrelated.txt", "branch-local change\n")
			commitAll(t, root, "unrelated branch change")
		},
		func(root string) {
			// The default branch registers a new artifact after the fork;
			// the branch removes nothing.
			content := "# Default-branch roadmap\n"
			writeTestFile(t, root, "docs/roadmap.md", content)
			digest, err := specvalidation.CanonicalTextDigest([]byte(content))
			if err != nil {
				t.Fatal(err)
			}
			rewriteProjectConfig(t, root, func(config *records.ProjectConfig) {
				config.Artifacts = append(config.Artifacts, records.ArtifactEntry{
					ID:     "roadmap",
					Kind:   records.ArtifactVision,
					Path:   "docs/roadmap.md",
					Digest: digest,
					Owner:  "user",
				})
			})
			commitAll(t, root, "register a new artifact on the default branch")
		},
	)

	// Without the option, the continued-registration check reads the
	// default-branch registry and demands the artifact the branch never
	// removed.
	code, stdout, stderr := runCLI(nil, "--output", "json", "check")
	assertValidationFailure(t, code, stdout, stderr)
	assertArtifactDiagnostic(t, stdout, "artifact.unregistered", "roadmap")
	assertDiagnosticText(t, stdout, "artifact.unregistered", "artifacts[id=roadmap]",
		`Artifact "roadmap" is registered on the default branch but is absent from the current registry.`,
		`Restore the artifact registration; registered artifacts cannot be unregistered.`)

	// The merge base predates the registration, so the branch registry is
	// complete against it.
	code, stdout, stderr = runCLI(nil, "--output", "json", "check", "--base-ref", base)
	assertSuccess(t, code, stdout, stderr)
}

// assertDiagnosticText locks the exact message and hint of one diagnostic,
// so the no-flag output stays byte-identical and the --base-ref output
// keeps its selected-base wording.
func assertDiagnosticText(t *testing.T, stdout, code, field, message, hint string) {
	t.Helper()
	var envelope failureEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout, err)
	}
	for _, diagnostic := range envelope.Error.Diagnostics {
		if diagnostic.Code != code || diagnostic.Field != field {
			continue
		}
		if diagnostic.Message != message {
			t.Fatalf("%s message = %q, want %q", code, diagnostic.Message, message)
		}
		if diagnostic.Hint != hint {
			t.Fatalf("%s hint = %q, want %q", code, diagnostic.Hint, hint)
		}
		return
	}
	t.Fatalf("check omitted %s on %s: %s", code, field, stdout)
}
