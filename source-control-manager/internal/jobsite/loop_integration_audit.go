package jobsite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const cycleAuditRefPrefix = "refs/protobot/cycle-audits/"

var (
	cycleAuditRefPattern  = regexp.MustCompile(`^refs/protobot/cycle-audits/(worker-a|worker-b)/cycle-([1-9][0-9]*)$`)
	cycleAuditViewPattern = regexp.MustCompile(`^cycle-accepted-[0-9a-f]{40}\.json$`)
	cycleAuditOIDPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	cycleAuditHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// A cycleAudit is authoritative only while its blob is pinned by Ref in the
// same transaction as the corresponding contribution ref and Integration HEAD.
// The JSON file outside Git is an immutable, rebuildable view of that blob.
type cycleAudit struct {
	Ref     string
	OID     string
	Record  AuditRecord
	Content []byte
}

func newCycleAudit(repo *gitRepo, record AuditRecord) (cycleAudit, error) {
	if !validCycleAuditRecord(record, record.Role, record.Cycle) ||
		(record.Role != RoleWorkerA && record.Role != RoleWorkerB) || record.Cycle <= 0 {
		return cycleAudit{}, fail(CodePatchRejected, "Cycle contribution identity cannot form a valid audit record.")
	}
	content, err := cycleAuditContent(record)
	if err != nil {
		return cycleAudit{}, err
	}
	oid, err := repo.writeBlob(content)
	if err != nil {
		return cycleAudit{}, err
	}
	return cycleAudit{
		Ref: fmt.Sprintf("%s%s/cycle-%d", cycleAuditRefPrefix, record.Role, record.Cycle),
		OID: oid, Record: record, Content: content,
	}, nil
}

func cycleAuditContent(record AuditRecord) ([]byte, error) {
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func (audit cycleAudit) viewName() string {
	return "cycle-accepted-" + audit.OID + ".json"
}

// RecoverCycleAudits validates the authoritative private Git audit refs,
// regenerates missing accepted-audit JSON views, and removes stale views in
// its own namespace. Existing views with different content fail closed rather
// than being overwritten. ApplyPatch's patch-accepted-* files are untouched.
// This reconciles audit views only; it does not resume a Worker/Triage run.
// Callers must serialize recovery and MergeCycle operations for the same
// private Integration repository, as the fenced fixture owner does.
func RecoverCycleAudits(integration Repository) error {
	if integration.Role != RoleIntegration {
		return fail(CodePatchRejected, "Cycle audit recovery requires the private Integration repository.")
	}
	repo, err := openGit(integration.Path)
	if err != nil {
		return err
	}
	defer repo.Close()
	return recoverCycleAudits(repo, filepath.Join(filepath.Dir(integration.Path), auditDirName))
}

func recoverCycleAudits(repo *gitRepo, auditDir string) error {
	audits, err := readCycleAudits(repo)
	if err != nil {
		return err
	}
	if _, err := writeCycleAuditViews(auditDir, audits); err != nil {
		return err
	}
	desired := make(map[string]bool, len(audits))
	for _, audit := range audits {
		desired[audit.viewName()] = true
	}
	entries, err := os.ReadDir(auditDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if cycleAuditViewPattern.MatchString(entry.Name()) && !desired[entry.Name()] {
			if !entry.Type().IsRegular() {
				return fail(CodePatchTampered, "A stale cycle audit view is not a regular file.")
			}
			if err := os.Remove(filepath.Join(auditDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func readCycleAudits(repo *gitRepo) ([]cycleAudit, error) {
	refs, err := repo.text("for-each-ref", "--format=%(refname)%09%(objectname)%09%(objecttype)", cycleAuditRefPrefix)
	if err != nil {
		return nil, err
	}
	contributions, err := repo.text("for-each-ref", "--format=%(refname)%09%(objectname)", "refs/heads/integration/")
	if err != nil {
		return nil, err
	}
	contributionRefs := map[string]string{}
	for _, line := range strings.Split(contributions, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 || !cycleAuditRefPattern.MatchString(strings.Replace(parts[0], "refs/heads/integration/", cycleAuditRefPrefix, 1)) {
			return nil, fail(CodePatchTampered, "A managed cycle contribution ref has an invalid name.")
		}
		contributionRefs[parts[0]] = parts[1]
	}
	audits := []cycleAudit{}
	for _, line := range strings.Split(refs, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[2] != "blob" {
			return nil, fail(CodePatchTampered, "A cycle audit ref does not identify an audit blob.")
		}
		matches := cycleAuditRefPattern.FindStringSubmatch(parts[0])
		if matches == nil || !cycleAuditOIDPattern.MatchString(parts[1]) {
			return nil, fail(CodePatchTampered, "A cycle audit ref has invalid metadata.")
		}
		cycle, err := strconv.Atoi(matches[2])
		if err != nil {
			return nil, fail(CodePatchTampered, "A cycle audit ref has an invalid cycle.")
		}
		content, err := repo.readBlob(parts[1])
		if err != nil {
			return nil, err
		}
		var record AuditRecord
		if err := json.Unmarshal(content, &record); err != nil {
			return nil, fail(CodePatchTampered, "A cycle audit blob is not an audit record.")
		}
		canonical, err := cycleAuditContent(record)
		if err != nil || !bytes.Equal(content, canonical) || !validCycleAuditRecord(record, matches[1], cycle) {
			return nil, fail(CodePatchTampered, "A cycle audit blob has invalid canonical metadata.")
		}
		ref := strings.Replace(parts[0], cycleAuditRefPrefix, "refs/heads/integration/", 1)
		if contributionRefs[ref] != record.ResultingCommit {
			return nil, fail(CodePatchTampered, "A cycle audit does not match its contribution ref.")
		}
		delete(contributionRefs, ref)
		if err := validateCycleAuditCommit(repo, record); err != nil {
			return nil, err
		}
		audits = append(audits, cycleAudit{Ref: parts[0], OID: parts[1], Record: record, Content: content})
	}
	if len(contributionRefs) != 0 {
		return nil, fail(CodePatchTampered, "A cycle contribution is missing its authoritative audit ref.")
	}
	return audits, nil
}

func validCycleAuditRecord(record AuditRecord, role string, cycle int) bool {
	return record.EventType == EventPatch && record.Decision == DecisionAccept && record.Role == role && record.Cycle == cycle &&
		record.PolicyVersion == 1 && record.FixtureVersion == FixtureVersion && record.BackendVersion == FixtureVersion &&
		record.WorkItem != "" && !strings.ContainsAny(record.WorkItem, "\r\n\x00") &&
		record.RejectionReason == "" && record.VisiblePathDigest == "" && record.DeniedPathDigest == "" &&
		cycleAuditOIDPattern.MatchString(record.SourceCommit) && cycleAuditOIDPattern.MatchString(record.WorkerRoot) &&
		cycleAuditOIDPattern.MatchString(record.IntegrationBase) && cycleAuditOIDPattern.MatchString(record.ResultingCommit) &&
		cycleAuditHashPattern.MatchString(record.PolicyDigest) && cycleAuditHashPattern.MatchString(record.BundleDigest)
}

func validateCycleAuditCommit(repo *gitRepo, record AuditRecord) error {
	parents, err := repo.text("show", "-s", "--format=%P", record.ResultingCommit)
	if err != nil {
		return err
	}
	message, err := repo.text("show", "-s", "--format=%B", record.ResultingCommit)
	if err != nil {
		return err
	}
	expected := fmt.Sprintf("Apply %s patch bundle\n\nProjection-Role: %s\nWorker-Root: %s\nPatch-Digest: %s\nCycle: %d\nWork-Item: %s\nPolicy-Digest: %s\nSource-Commit: %s",
		record.Role, record.Role, record.WorkerRoot, record.BundleDigest, record.Cycle, record.WorkItem, record.PolicyDigest, record.SourceCommit)
	if parents != record.IntegrationBase || message != expected {
		return fail(CodePatchTampered, "A cycle audit does not match its contribution commit.")
	}
	res, err := repo.run("merge-base", "--is-ancestor", record.ResultingCommit, "refs/heads/"+branchMain)
	if err != nil {
		return err
	}
	if !res.OK() {
		return fail(CodePatchTampered, "A cycle audit contribution is not in Integration history.")
	}
	return nil
}

func writeCycleAuditViews(auditDir string, audits []cycleAudit) ([]string, error) {
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(auditDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fail(CodePatchTampered, "The cycle audit view directory is not a directory.")
	}
	var names []string
	for _, audit := range audits {
		name := audit.viewName()
		path := filepath.Join(auditDir, name)
		owned, err := ensureCycleAuditView(path, audit.Content)
		if owned {
			names = append(names, name)
		}
		if err != nil {
			return names, err
		}
	}
	return names, nil
}

func ensureCycleAuditView(path string, content []byte) (bool, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return false, fail(CodePatchTampered, "An existing cycle audit view is not a private regular file.")
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(existing, content) {
			return false, fail(CodePatchTampered, "An existing cycle audit view differs from its authoritative blob.")
		}
		return true, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	return createCycleAuditView(path, content)
}

func createCycleAuditView(path string, content []byte) (published bool, resultErr error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".cycle-audit-")
	if err != nil {
		return false, err
	}
	temporary := file.Name()
	defer func() { resultErr = errors.Join(resultErr, os.Remove(temporary)) }()
	if _, err := file.Write(content); err != nil {
		return false, errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return false, errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	// Linking a complete temporary file publishes the view without replacing
	// an existing immutable view and without exposing a partial JSON write.
	if err := os.Link(temporary, path); err != nil {
		if os.IsExist(err) {
			return ensureCycleAuditView(path, content)
		}
		return false, err
	}
	return true, nil
}
