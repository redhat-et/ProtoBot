package jobsite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeCyclePreservesIndependentContributionsAndMergeHistory(t *testing.T) {
	exported := newCycleExport(t)
	input := newCycleInput(t, exported, 1)
	merged, err := MergeCycle(exported.Integration, exported.Policy, input)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := openGit(exported.Integration.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if head, err := repo.head(); err != nil || head != merged.Commit {
		t.Fatalf("HEAD = %q, %v, want %s", head, err, merged.Commit)
	}
	parents, err := repo.text("show", "-s", "--format=%P", merged.Commit)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(parents)
	if len(parts) != 2 || parts[1] != merged.WorkerBCommit {
		t.Fatalf("final merge parents = %q", parents)
	}
	firstParents, err := repo.text("show", "-s", "--format=%P", parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if firstParents != merged.Base+" "+merged.WorkerACommit {
		t.Fatalf("first merge parents = %q", firstParents)
	}
	for _, check := range []struct {
		commit, path, want string
	}{
		{merged.WorkerACommit, currentTestPath, "cycle test\n"},
		{merged.WorkerACommit, currentImplPath, currentImplSentinel + "\n"},
		{merged.WorkerBCommit, currentTestPath, currentTestSentinel + "\n"},
		{merged.WorkerBCommit, currentImplPath, "cycle implementation\n"},
		{merged.Commit, currentTestPath, "cycle test\n"},
		{merged.Commit, currentImplPath, "cycle implementation\n"},
	} {
		content, err := repo.readFileAt(check.commit, check.path)
		if err != nil || string(content) != check.want {
			t.Errorf("%s:%s = %q, %v, want %q", check.commit, check.path, content, err, check.want)
		}
	}
	if len(merged.Patches) != 2 || merged.Patches[0].ResultingCommit != merged.WorkerACommit ||
		merged.Patches[1].ResultingCommit != merged.WorkerBCommit {
		t.Fatalf("patch audits = %#v", merged.Patches)
	}
	for index, bundle := range []PatchBundle{input.WorkerA, input.WorkerB} {
		message, err := repo.text("show", "-s", "--format=%B", []string{merged.WorkerACommit, merged.WorkerBCommit}[index])
		if err != nil {
			t.Fatal(err)
		}
		for _, trailer := range []string{"Worker-Root: " + bundle.WorkerRoot, "Patch-Digest: " + bundle.BundleDigest, "Cycle: 1"} {
			if !strings.Contains(message, trailer) {
				t.Errorf("contribution message %q lacks %q", message, trailer)
			}
		}
	}
	// Integration receives bytes, not Worker history or credentials.
	assertObject(t, exported.Integration.Path, exported.WorkerA.RootCommit, false)
	assertObject(t, exported.Integration.Path, exported.WorkerB.RootCommit, false)
	assertIsolatedGit(t, exported.Integration.Path)
}

func TestMergeCycleRejectsCompletePairWithoutPublishingPartialContribution(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*CycleImport)
		code string
	}{
		{"peer path", func(in *CycleImport) {
			in.WorkerB.Operations[0].Path = currentTestPath
			in.WorkerB.BundleDigest, _ = bundleDigest(in.WorkerB)
		}, CodePatchRejected},
		{"tampered bytes", func(in *CycleImport) { in.WorkerB.Operations[0].Content = []byte("tampered") }, CodePatchTampered},
		{"stale base", func(in *CycleImport) {
			in.WorkerA.IntegrationBase = strings.Repeat("0", 40)
			in.WorkerB.IntegrationBase = in.WorkerA.IntegrationBase
			in.WorkerA.BundleDigest, _ = bundleDigest(in.WorkerA)
			in.WorkerB.BundleDigest, _ = bundleDigest(in.WorkerB)
		}, CodePatchStale},
		{"different cycle", func(in *CycleImport) { in.WorkerB.Cycle++ }, CodePatchRejected},
		{"no contributions", func(in *CycleImport) { *in = CycleImport{} }, CodePatchRejected},
		{"wrong role", func(in *CycleImport) { in.WorkerB.Role = RoleWorkerA }, CodePatchRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			exported := newCycleExport(t)
			input := newCycleInput(t, exported, 1)
			test.edit(&input)
			merged, err := MergeCycle(exported.Integration, exported.Policy, input)
			if errorCode(err) != test.code || merged == nil || len(merged.Patches) != 1 || merged.Patches[0].Decision != DecisionReject {
				t.Fatalf("merge = %#v, error = %v, want private rejection %s", merged, err, test.code)
			}
			repo, err := openGit(exported.Integration.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if head, err := repo.head(); err != nil || head != exported.Integration.RootCommit {
				t.Fatalf("rejected merge changed HEAD = %s, %v", head, err)
			}
			refs, err := repo.text("for-each-ref", "--format=%(refname)", "refs/heads/integration/")
			if err != nil || refs != "" {
				t.Fatalf("rejected merge published refs %q, %v", refs, err)
			}
			audits, err := filepath.Glob(filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName, "patch-rejected-*.json"))
			if err != nil || len(audits) != 1 {
				t.Fatalf("rejection audits = %v, %v", audits, err)
			}
			data, err := os.ReadFile(audits[0])
			if err != nil {
				t.Fatal(err)
			}
			var audit AuditRecord
			if err := json.Unmarshal(data, &audit); err != nil || audit.Decision != DecisionReject {
				t.Fatalf("rejection audit = %s, %v", data, err)
			}
		})
	}
}

func TestMergeCycleCanImportOnlyRoutedRoleOnRetry(t *testing.T) {
	for _, role := range []string{RoleWorkerA, RoleWorkerB} {
		t.Run(role, func(t *testing.T) {
			exported := newCycleExport(t)
			first, err := MergeCycle(exported.Integration, exported.Policy, newCycleInput(t, exported, 1))
			if err != nil {
				t.Fatal(err)
			}
			exported.Integration.RootCommit = first.Commit
			input := newCycleInput(t, exported, 2)
			if role == RoleWorkerA {
				input.WorkerB = PatchBundle{}
			} else {
				input.WorkerA = PatchBundle{}
			}
			second, err := MergeCycle(exported.Integration, exported.Policy, input)
			if err != nil {
				t.Fatal(err)
			}
			if second.Base != first.Commit || len(second.Patches) != 1 || second.Patches[0].Role != role {
				t.Fatalf("single-role retry = %#v", second)
			}
			if (role == RoleWorkerA && second.WorkerBCommit != "") || (role == RoleWorkerB && second.WorkerACommit != "") {
				t.Fatalf("undispatched role has a new contribution: %#v", second)
			}
		})
	}
}

func TestMergeCycleRejectsInvalidAuditDirectoryBeforePublication(t *testing.T) {
	exported := newCycleExport(t)
	auditDir := filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName)
	if err := os.RemoveAll(auditDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auditDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeCycle(exported.Integration, exported.Policy, newCycleInput(t, exported, 1))
	if err == nil || merged == nil || merged.Commit != "" {
		t.Fatalf("merge with failed audit = %#v, %v", merged, err)
	}
	repo, err := openGit(exported.Integration.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if head, err := repo.head(); err != nil || head != exported.Integration.RootCommit {
		t.Fatalf("audit failure left HEAD = %q, %v", head, err)
	}
	refs, err := repo.text("for-each-ref", "--format=%(refname)", "refs/heads/integration/")
	if err != nil || refs != "" {
		t.Fatalf("audit failure left contribution refs = %q, %v", refs, err)
	}
}

func newCycleExport(t *testing.T) *ExportResult {
	t.Helper()
	source, err := BuildSourceFixture(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exported, err := Export(ExportRequest{
		SourceRoot: source.Root, SourceCommit: source.SourceCommit, Policy: source.Policy,
		OutputDir: t.TempDir(), WorkItem: "WI-69", Cycle: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return exported
}

func newCycleInput(t *testing.T, exported *ExportResult, cycle int) CycleImport {
	t.Helper()
	a, err := NewPatchBundle(RoleWorkerA, "WI-69", cycle, exported.Policy.Digest, exported.SourceCommit,
		exported.WorkerA.RootCommit, exported.Integration.RootCommit,
		[]Operation{{Path: currentTestPath, Action: ActionUpdate, Mode: ModeFile, Content: []byte("cycle test\n")}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPatchBundle(RoleWorkerB, "WI-69", cycle, exported.Policy.Digest, exported.SourceCommit,
		exported.WorkerB.RootCommit, exported.Integration.RootCommit,
		[]Operation{{Path: currentImplPath, Action: ActionUpdate, Mode: ModeFile, Content: []byte("cycle implementation\n")}})
	if err != nil {
		t.Fatal(err)
	}
	return CycleImport{WorkerA: a, WorkerB: b}
}
