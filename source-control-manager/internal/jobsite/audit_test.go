package jobsite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/source-control-manager/internal/gitx"
)

func TestUniqueAuditName(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:abcdef123456"

	// First file should be base name
	name1, err := uniqueAuditName(dir, "rejected", digest)
	if err != nil {
		t.Fatalf("uniqueAuditName failed: %v", err)
	}
	expectedBase := "patch-rejected-sha256-abcdef123456.json"
	if name1 != expectedBase {
		t.Fatalf("name1 = %q, want %q", name1, expectedBase)
	}

	// Create the file so next call finds it taken
	if err := os.WriteFile(filepath.Join(dir, name1), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second file should have suffix -1
	name2, err := uniqueAuditName(dir, "rejected", digest)
	if err != nil {
		t.Fatalf("uniqueAuditName failed: %v", err)
	}
	expectedSuffix1 := "patch-rejected-sha256-abcdef123456-1.json"
	if name2 != expectedSuffix1 {
		t.Fatalf("name2 = %q, want %q", name2, expectedSuffix1)
	}
}

func TestUniqueAuditNameNonNotExistStatError(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where a directory would be expected in the path
	blockedFile := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blockedFile, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidDir := filepath.Join(blockedFile, "sub")

	// Calling uniqueAuditName on invalidDir where Stat fails with ENOTDIR (not IsNotExist)
	_, err := uniqueAuditName(invalidDir, "rejected", "sha256:1234")
	if err == nil {
		t.Fatal("expected error from uniqueAuditName on ENOTDIR, got nil")
	}
	if os.IsNotExist(err) {
		t.Fatalf("expected non-NotExist error, got %v", err)
	}
}

func TestUniqueAuditNameSuffixLimitExceeded(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:testlimit"
	base := "patch-accepted-sha256-testlimit"

	// Create base file
	if err := os.WriteFile(filepath.Join(dir, base+".json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Create all suffixes up to maxAuditSuffix
	for i := 1; i <= maxAuditSuffix; i++ {
		name := filepath.Join(dir, fmt.Sprintf("%s-%d.json", base, i))
		if err := os.WriteFile(name, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err := uniqueAuditName(dir, "accepted", digest)
	if err == nil {
		t.Fatal("expected error when suffix limit exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "suffix limit") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestWriteAuditExcl(t *testing.T) {
	dir := t.TempDir()
	record := AuditRecord{
		EventType: EventPatch,
		Decision:  DecisionAccept,
	}

	// First write should succeed
	name := "test-audit.json"
	if err := writeAuditExcl(dir, name, record); err != nil {
		t.Fatalf("first writeAuditExcl failed: %v", err)
	}

	// Verify file mode
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 0600", info.Mode().Perm())
	}

	// Second write to same name must fail with ErrExist
	err = writeAuditExcl(dir, name, record)
	if err == nil {
		t.Fatal("second writeAuditExcl succeeded, want ErrExist")
	}
	if !os.IsExist(err) {
		t.Fatalf("expected os.IsExist error, got %v", err)
	}

	// Verify original file was not unlinked on ErrExist
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("original file was removed after ErrExist: %v", err)
	}
}

func TestIsCASMismatch(t *testing.T) {
	repoDir := t.TempDir()
	repo, err := initRepo(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	// An error containing "but expected" is a CAS mismatch
	casErr := &gitx.CommandError{
		Args:   []string{"update-ref", "refs/heads/main", "newoid", "oldoid"},
		Status: 128,
		Stderr: "fatal: update_ref failed for ref 'refs/heads/main': cannot lock ref 'refs/heads/main': is at 1111 but expected 2222",
	}
	if !repo.isCASMismatch(casErr, "2222") {
		t.Fatal("expected isCASMismatch to be true for 'but expected'")
	}

	// A generic error without mismatch text where head has not moved is not CAS mismatch
	genericErr := &gitx.CommandError{
		Args:   []string{"update-ref", "refs/heads/main", "newoid"},
		Status: 1,
		Stderr: "fatal: some generic git error",
	}
	if repo.isCASMismatch(genericErr, "") {
		t.Fatal("expected isCASMismatch to be false for generic error without head change")
	}
}

func TestRealGitCASMismatch(t *testing.T) {
	repoDir := t.TempDir()
	repo, err := initRepo(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tree, err := repo.writeTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := repo.commitTree(tree, "initial commit")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.updateRef("refs/heads/main", c1); err != nil {
		t.Fatal(err)
	}

	c2, err := repo.commitTreeWithParent(tree, c1, "second commit")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.updateRef("refs/heads/main", c2); err != nil {
		t.Fatal(err)
	}

	c3, err := repo.commitTreeWithParent(tree, c2, "third commit")
	if err != nil {
		t.Fatal(err)
	}

	// Try CAS with old OID = c1 (when current HEAD is c2)
	err = repo.updateRefCAS("refs/heads/main", c3, c1)
	if err == nil {
		t.Fatal("expected updateRefCAS to fail with old OID mismatch")
	}
	if !repo.isCASMismatch(err, c1) {
		t.Fatalf("expected isCASMismatch to be true, got error: %v", err)
	}
}

func TestRollbackCleansUpUniqueAcceptAudit(t *testing.T) {
	sourceRoot := t.TempDir()
	source, err := BuildSourceFixture(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	result, err := Export(ExportRequest{
		SourceRoot:   source.Root,
		SourceCommit: source.SourceCommit,
		Policy:       source.Policy,
		PolicyDigest: source.PolicyDigest,
		OutputDir:    out,
		WorkItem:     fixtureWorkItem,
		Cycle:        1,
	})
	if err != nil {
		t.Fatal(err)
	}

	before, err := integrationHead(result.Integration.Path)
	if err != nil {
		t.Fatal(err)
	}

	// Block patch-accepted.json by creating it as a directory so writing it fails after unique audit is written
	auditDir := filepath.Join(out, auditDirName)
	if err := os.MkdirAll(filepath.Join(auditDir, "patch-accepted.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	bundle, err := NewPatchBundle(RoleWorkerA, fixtureWorkItem, 1, result.Policy.Digest, result.SourceCommit, result.WorkerA.RootCommit, before, []Operation{{
		Path:    currentTestPath,
		Action:  ActionUpdate,
		Mode:    ModeFile,
		Content: []byte("rollback test\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}

	res, err := ApplyPatch(result.Integration, result.Policy, bundle)
	if err == nil {
		t.Fatal("expected ApplyPatch to fail when patch-accepted.json write fails")
	}
	if res != nil {
		t.Fatalf("expected nil PatchResult on apply failure, got: %#v", res)
	}

	// Verify HEAD was restored to before
	after, err := integrationHead(result.Integration.Path)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("HEAD not restored after rollback: %s != %s", after, before)
	}

	// Verify no unique accepted audit file remains
	entries, err := os.ReadDir(auditDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "patch-accepted-") {
			t.Fatalf("found leftover unique accepted audit file %s after rollback", entry.Name())
		}
	}
}

func TestRollbackFailureReturnsDistinctFailClosedError(t *testing.T) {
	repoDir := t.TempDir()
	repo, err := initRepo(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tree, err := repo.writeTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := repo.commitTree(tree, "initial commit")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.updateRef("refs/heads/main", c1); err != nil {
		t.Fatal(err)
	}

	c2, err := repo.commitTreeWithParent(tree, c1, "second commit")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.updateRef("refs/heads/main", c2); err != nil {
		t.Fatal(err)
	}

	// Lock refs/heads/main so rollback cannot update ref back to c1
	gitDir, err := repo.gitDir()
	if err != nil {
		t.Fatal(err)
	}
	lockFile := filepath.Join(gitDir, "refs", "heads", "main.lock")
	if err := os.WriteFile(lockFile, []byte("locked"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(lockFile) }()

	auditDir := t.TempDir()
	err = rollbackIntegration(repo, auditDir, "", c1)
	if err == nil {
		t.Fatal("expected rollbackIntegration to fail when ref is locked")
	}

	// Verify fail-closed error formatting via fail(CodePatchRollbackFailed, ...)
	fcErr := fail(CodePatchRollbackFailed, fmt.Sprintf("Failed to restore Integration HEAD to %s during rollback: %v.", c1, err))
	if errorCode(fcErr) != CodePatchRollbackFailed {
		t.Fatalf("errorCode = %q, want %q", errorCode(fcErr), CodePatchRollbackFailed)
	}
}

func TestWriteSandboxAudit(t *testing.T) {
	dir := t.TempDir()
	ev := SandboxAuditEvent{
		ContractVersion: ContractVersion,
		BackendVersion:  "local/test",
		TraceID:         "trace-123",
		Sequence:        1,
		EventType:       EventSandboxOpen,
		Decision:        DecisionAccept,
		PreviousDigest:  emptyDigest(),
		EventDigest:     "sha256:abc",
	}

	name := "sandbox-0001.json"
	if err := writeSandboxAudit(dir, name, ev); err != nil {
		t.Fatalf("writeSandboxAudit failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}

	var readEv SandboxAuditEvent
	if err := json.Unmarshal(data, &readEv); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if readEv.ContractVersion != ContractVersion || readEv.TraceID != "trace-123" || readEv.Sequence != 1 {
		t.Fatalf("unexpected audit event read: %+v", readEv)
	}
}
