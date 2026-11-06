package jobsite

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeCyclePinsAcceptedAuditsWithContributionRefs(t *testing.T) {
	exported, repo, merged, audits := publishedCycleAuditTest(t)
	if len(audits) != 2 || len(merged.Patches) != 2 {
		t.Fatalf("authoritative audit count = %d, result count = %d", len(audits), len(merged.Patches))
	}
	auditDir := filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName)
	for index, audit := range audits {
		if audit.Record != merged.Patches[index] {
			t.Fatal("result does not retain the authoritative audit record")
		}
		view, err := os.ReadFile(filepath.Join(auditDir, audit.viewName()))
		if err != nil || !bytes.Equal(view, audit.Content) {
			t.Fatalf("audit view differs from its pinned blob: %q, %v", view, err)
		}
	}
	// The accepted audit blobs remain reachable even though they are not in
	// the product tree or commit-parent DAG.
	res, err := repo.run("gc", "--prune=now")
	if err := repo.check(res, err, "gc"); err != nil {
		t.Fatal(err)
	}
	if _, err := readCycleAudits(repo); err != nil {
		t.Fatalf("Git garbage collection lost accepted evidence: %v", err)
	}
}

func TestMergeCycleAuditRefFailureDoesNotPublishOtherRefs(t *testing.T) {
	exported := newCycleExport(t)
	repo, err := openGit(exported.Integration.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	gitDir, err := repo.gitDir()
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(gitDir, "refs", "protobot", "cycle-audits", RoleWorkerB, "cycle-1.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeCycle(exported.Integration, exported.Policy, newCycleInput(t, exported, 1))
	if errorCode(err) != CodePatchStale || merged.Commit != "" {
		t.Fatalf("audit ref lock did not reject publication: %#v, %v", merged, err)
	}
	assertCyclePublicationRolledBack(t, repo, exported.Integration.RootCommit)
}

func TestRecoverCycleAuditsRebuildsMissingViewsAndRemovesOnlyOwnedStaleFiles(t *testing.T) {
	exported, _, _, audits := publishedCycleAuditTest(t)
	auditDir := filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName)
	for _, audit := range audits {
		if err := os.Remove(filepath.Join(auditDir, audit.viewName())); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(auditDir, "cycle-accepted-"+strings.Repeat("0", 40)+".json")
	legacy := filepath.Join(auditDir, "patch-accepted-legacy.json")
	for _, name := range []string{stale, legacy} {
		if err := os.WriteFile(name, []byte("retained only if unmanaged"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := RecoverCycleAudits(exported.Integration); err != nil {
			t.Fatal(err)
		}
		for _, audit := range audits {
			path := filepath.Join(auditDir, audit.viewName())
			content, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(content, audit.Content) {
				t.Fatalf("recovered view = %q, %v", content, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("recovered audit view is not private")
			}
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale managed view was not removed: %v", err)
	}
	if content, err := os.ReadFile(legacy); err != nil || string(content) != "retained only if unmanaged" {
		t.Fatal("recovery changed legacy ApplyPatch evidence")
	}
}

func TestRecoverCycleAuditsDoesNotOverwriteChangedViews(t *testing.T) {
	exported, repo, merged, audits := publishedCycleAuditTest(t)
	path := filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName, audits[0].viewName())
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverCycleAudits(exported.Integration); errorCode(err) != CodePatchTampered {
		t.Fatalf("changed view was accepted: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "tampered" {
		t.Fatal("recovery overwrote an existing immutable view")
	}
	exported.Integration.RootCommit = merged.Commit
	if _, err := MergeCycle(exported.Integration, exported.Policy, newCycleInput(t, exported, 2)); errorCode(err) != CodePatchTampered {
		t.Fatalf("new cycle ignored an invalid existing audit view: %v", err)
	}
	if head, err := repo.head(); err != nil || head != merged.Commit {
		t.Fatal("failed recovery changed Integration HEAD")
	}
}

func TestRecoverCycleAuditsRejectsMalformedAuthoritativeMetadata(t *testing.T) {
	for _, mutation := range []string{"wrong-record-cycle", "wrong-record-commit", "invalid-ref-name", "non-blob-ref", "missing-audit-ref"} {
		t.Run(mutation, func(t *testing.T) {
			exported, repo, _, audits := publishedCycleAuditTest(t)
			audit := audits[0]
			switch mutation {
			case "wrong-record-cycle", "wrong-record-commit":
				record := audit.Record
				if mutation == "wrong-record-cycle" {
					record.Cycle++
				} else {
					record.WorkItem = "WI-other"
				}
				changed, err := newCycleAudit(repo, record)
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.updateRef(audit.Ref, changed.OID); err != nil {
					t.Fatal(err)
				}
			case "invalid-ref-name":
				if err := repo.updateRef(cycleAuditRefPrefix+RoleWorkerA+"/cycle-0", audit.OID); err != nil {
					t.Fatal(err)
				}
			case "non-blob-ref":
				if err := repo.updateRef(audit.Ref, audit.Record.ResultingCommit); err != nil {
					t.Fatal(err)
				}
			case "missing-audit-ref":
				res, err := repo.run("update-ref", "-d", audit.Ref)
				if err := repo.check(res, err, "update-ref"); err != nil {
					t.Fatal(err)
				}
			}
			if err := RecoverCycleAudits(exported.Integration); errorCode(err) != CodePatchTampered {
				t.Fatalf("invalid authoritative metadata was accepted: %v", err)
			}
		})
	}
}

func TestCyclePartialAuditViewFailureRollsBackRefsBeforeRemovingViews(t *testing.T) {
	exported, repo, merged, audits := publishedCycleAuditTest(t)
	auditDir := filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName)
	for _, audit := range audits {
		if err := os.Remove(filepath.Join(auditDir, audit.viewName())); err != nil {
			t.Fatal(err)
		}
	}
	// Model an I/O failure appearing after the atomic ref transaction but
	// before all derived views have been published.
	if err := os.Mkdir(filepath.Join(auditDir, audits[1].viewName()), 0o700); err != nil {
		t.Fatal(err)
	}
	refs, commits := cycleAuditTestContributionRefs(audits)
	if err := finishCyclePublication(repo, auditDir, merged, refs, commits, audits); errorCode(err) != CodePatchTampered {
		t.Fatalf("partial view failure = %v", err)
	}
	if merged.Commit != "" || len(merged.Patches) != 0 {
		t.Fatal("rolled-back publication still claims accepted evidence")
	}
	assertCyclePublicationRolledBack(t, repo, exported.Integration.RootCommit)
	if _, err := os.Stat(filepath.Join(auditDir, audits[0].viewName())); !os.IsNotExist(err) {
		t.Fatalf("rolled-back cycle retained its newly created first view: %v", err)
	}
}

func TestCycleFailedRefRollbackPreservesAcceptedAuditEvidence(t *testing.T) {
	exported, repo, merged, audits := publishedCycleAuditTest(t)
	head := merged.Commit
	auditDir := filepath.Join(filepath.Dir(exported.Integration.Path), auditDirName)
	blockedView := filepath.Join(auditDir, audits[1].viewName())
	if err := os.Remove(blockedView); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blockedView, 0o700); err != nil {
		t.Fatal(err)
	}
	gitDir, err := repo.gitDir()
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(gitDir, "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(lock) })
	refs, commits := cycleAuditTestContributionRefs(audits)
	if err := finishCyclePublication(repo, auditDir, merged, refs, commits, audits); errorCode(err) != CodePatchRollbackFailed {
		t.Fatalf("locked rollback did not fail closed: %v", err)
	}
	if merged.Commit != head || len(merged.Patches) != len(audits) {
		t.Fatal("failed ref rollback discarded authoritative accepted result evidence")
	}
	if current, err := repo.head(); err != nil || current != head {
		t.Fatal("failed atomic rollback partially changed HEAD")
	}
	if content, err := os.ReadFile(filepath.Join(auditDir, audits[0].viewName())); err != nil || !bytes.Equal(content, audits[0].Content) {
		t.Fatal("failed ref rollback removed accepted sidecar evidence")
	}
	if recorded, err := readCycleAudits(repo); err != nil || len(recorded) != len(audits) {
		t.Fatalf("failed ref rollback removed pinned audit evidence: %v", err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blockedView); err != nil {
		t.Fatal(err)
	}
	if err := RecoverCycleAudits(exported.Integration); err != nil {
		t.Fatalf("accepted cycle audit views could not be recovered after rollback failure: %v", err)
	}
}

func publishedCycleAuditTest(t *testing.T) (*ExportResult, *gitRepo, *CycleMerge, []cycleAudit) {
	t.Helper()
	exported := newCycleExport(t)
	merged, err := MergeCycle(exported.Integration, exported.Policy, newCycleInput(t, exported, 1))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := openGit(exported.Integration.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	audits, err := readCycleAudits(repo)
	if err != nil {
		t.Fatal(err)
	}
	return exported, repo, merged, audits
}

func cycleAuditTestContributionRefs(audits []cycleAudit) ([]string, []string) {
	var refs, commits []string
	for _, audit := range audits {
		refs = append(refs, strings.Replace(audit.Ref, cycleAuditRefPrefix, "refs/heads/integration/", 1))
		commits = append(commits, audit.Record.ResultingCommit)
	}
	return refs, commits
}

func assertCyclePublicationRolledBack(t *testing.T, repo *gitRepo, base string) {
	t.Helper()
	if head, err := repo.head(); err != nil || head != base {
		t.Fatalf("cycle publication left HEAD = %q, %v, want %s", head, err, base)
	}
	refs, err := repo.text("for-each-ref", "--format=%(refname)", "refs/heads/integration/", cycleAuditRefPrefix)
	if err != nil || refs != "" {
		t.Fatalf("cycle publication left contribution or audit refs = %q, %v", refs, err)
	}
}
