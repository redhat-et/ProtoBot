package jobsite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/redhat-et/protobot/source-control-manager/internal/gitx"
)

// CycleImport contains the independently produced contributions for one
// cycle. An undispatched role has a zero-value bundle; at least one role must
// contribute. Active bundles are bound to the same private Integration baseline.
type CycleImport struct {
	WorkerA PatchBundle `json:"worker_a"`
	WorkerB PatchBundle `json:"worker_b"`
}

// CycleMerge records the full-baseline contribution commits and the final
// merge commit. These identifiers belong only in private control-plane evidence.
type CycleMerge struct {
	Base          string        `json:"base"`
	WorkerACommit string        `json:"worker_a_commit"`
	WorkerBCommit string        `json:"worker_b_commit"`
	Commit        string        `json:"commit"`
	Patches       []AuditRecord `json:"patches"`
}

// MergeCycle validates the complete pair before publishing either contribution.
// It builds full-baseline role branches and two-parent merge commits directly
// in Integration's object database; Worker Git objects are never imported.
// Rejection leaves Integration HEAD and role refs unchanged, and returns a
// non-nil result carrying the private rejection audit.
// Callers must serialize MergeCycle and RecoverCycleAudits operations for
// the same private Integration repository, as the fenced fixture owner does.
func MergeCycle(integration Repository, policy Policy, input CycleImport) (*CycleMerge, error) {
	if integration.Role != RoleIntegration {
		return nil, fail(CodePatchRejected, "Cycle imports require the private Integration repository.")
	}
	repo, err := openGit(integration.Path)
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	base, err := repo.head()
	if err != nil {
		return nil, err
	}
	result := &CycleMerge{Base: base}
	auditDir := filepath.Join(filepath.Dir(integration.Path), auditDirName)
	if err := recoverCycleAudits(repo, auditDir); err != nil {
		return result, err
	}
	reject := func(bundle PatchBundle, code, reason string) (*CycleMerge, error) {
		result.Commit = ""
		record := cyclePatchAudit(policy, bundle, DecisionReject, reason, "")
		name, err := uniqueAuditName(auditDir, "rejected", bundle.BundleDigest)
		if err != nil {
			return result, err
		}
		if err := writeAuditExcl(auditDir, name, record); err != nil {
			return result, err
		}
		result.Patches = append(result.Patches, record)
		return result, fail(code, reason)
	}
	if reason := cyclePairMismatch(input); reason != "" {
		return reject(input.WorkerB, CodePatchRejected, reason)
	}
	current, err := repo.listBlobs(base)
	if err != nil {
		return result, err
	}
	present := make(map[string]blobEntry, len(current))
	for _, blob := range current {
		present[blob.Path] = blob
	}
	bundles := activeCycleBundles(input)
	for _, bundle := range bundles {
		if code, reason := validateCycleBundle(policy, base, bundle, present); reason != "" {
			return reject(bundle, code, reason)
		}
	}
	if reason := validateCycleTree(input, present); reason != "" {
		return reject(input.WorkerB, CodePatchRejected, reason)
	}
	branch, err := repo.text("symbolic-ref", "--quiet", "HEAD")
	if err != nil || branch != "refs/heads/"+branchMain {
		return reject(input.WorkerA, CodePatchRejected, "Integration must have its work-item main branch checked out.")
	}
	status, err := repo.text("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return result, err
	}
	if status != "" {
		return reject(input.WorkerA, CodePatchRejected, "Integration must be clean before importing a cycle.")
	}
	var refs, commits []string
	var audits []cycleAudit
	var combinedOps []Operation
	result.Commit = base
	for _, bundle := range bundles {
		contribution, err := buildCommit(repo, base, bundle)
		if err != nil {
			return result, err
		}
		if bundle.Role == RoleWorkerA {
			result.WorkerACommit = contribution
		} else {
			result.WorkerBCommit = contribution
		}
		// Validated role allowlists are disjoint. The tree built from all
		// contributions so far is the exact conflict-free merge result.
		combinedOps = append(combinedOps, bundle.Operations...)
		combined := bundle
		combined.Operations = combinedOps
		combinedCommit, err := buildCommit(repo, base, combined)
		if err != nil {
			return result, err
		}
		tree, err := repo.text("rev-parse", combinedCommit+"^{tree}")
		if err != nil {
			return result, err
		}
		result.Commit, err = cycleMergeCommit(repo, tree, result.Commit, contribution, bundle)
		if err != nil {
			return result, err
		}
		refs = append(refs, fmt.Sprintf("refs/heads/integration/%s/cycle-%d", bundle.Role, bundle.Cycle))
		commits = append(commits, contribution)
		audit, err := newCycleAudit(repo, cyclePatchAudit(policy, bundle, DecisionAccept, "", contribution))
		if err != nil {
			result.Commit = ""
			return result, err
		}
		audits = append(audits, audit)
	}
	var transaction strings.Builder
	transaction.WriteString("start\n")
	for index, ref := range refs {
		fmt.Fprintf(&transaction, "create %s %s\n", ref, commits[index])
	}
	for _, audit := range audits {
		fmt.Fprintf(&transaction, "create %s %s\n", audit.Ref, audit.OID)
	}
	fmt.Fprintf(&transaction, "update refs/heads/%s %s %s\nprepare\ncommit\n", branchMain, result.Commit, base)
	if err := cycleRefTransaction(repo, transaction.String()); err != nil {
		return reject(bundles[0], CodePatchStale, "Cycle baseline or contribution refs changed before publication.")
	}
	for _, audit := range audits {
		result.Patches = append(result.Patches, audit.Record)
	}
	return result, finishCyclePublication(repo, auditDir, result, refs, commits, audits)
}

func finishCyclePublication(repo *gitRepo, auditDir string, result *CycleMerge, refs, commits []string, audits []cycleAudit) error {
	var auditNames []string
	finishErr := repo.checkout(branchMain)
	if finishErr == nil {
		finishErr = repo.scrubMeta()
	}
	if finishErr == nil {
		auditNames, finishErr = writeCycleAuditViews(auditDir, audits)
	}
	if finishErr == nil {
		return nil
	}
	// Accepted evidence remains pinned until the same atomic rollback removes
	// HEAD, contribution refs, and audit refs. Never delete views first: a
	// failed ref rollback must retain all evidence of the published cycle.
	var transaction strings.Builder
	fmt.Fprintf(&transaction, "start\nupdate refs/heads/%s %s %s\n", branchMain, result.Base, result.Commit)
	for index, ref := range refs {
		fmt.Fprintf(&transaction, "delete %s %s\n", ref, commits[index])
	}
	for _, audit := range audits {
		fmt.Fprintf(&transaction, "delete %s %s\n", audit.Ref, audit.OID)
	}
	transaction.WriteString("prepare\ncommit\n")
	if err := cycleRefTransaction(repo, transaction.String()); err != nil {
		return fail(CodePatchRollbackFailed, fmt.Sprintf("Cycle publication rollback failed: %v.", errors.Join(finishErr, err)))
	}
	result.Commit = ""
	result.Patches = nil
	rollbackErr := repo.checkout(branchMain)
	for _, name := range auditNames {
		if err := os.Remove(filepath.Join(auditDir, name)); err != nil && !os.IsNotExist(err) {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if rollbackErr != nil {
		return fail(CodePatchRollbackFailed, fmt.Sprintf("Cycle publication rollback cleanup failed: %v.", errors.Join(finishErr, rollbackErr)))
	}
	return finishErr
}

func cyclePairMismatch(input CycleImport) string {
	a, b := input.WorkerA, input.WorkerB
	if (!absentCycleBundle(a) && a.Role != RoleWorkerA) || (!absentCycleBundle(b) && b.Role != RoleWorkerB) {
		return "Cycle imports require Worker A and Worker B bundles in their named roles."
	}
	bundles := activeCycleBundles(input)
	if len(bundles) == 0 {
		return "Cycle imports require at least one Worker contribution."
	}
	for _, bundle := range bundles {
		if bundle.Cycle <= 0 || bundle.WorkItem == "" || bundle.SourceCommit == "" || bundle.WorkerRoot == "" {
			return "Cycle imports require a positive cycle and complete contribution identity."
		}
	}
	if len(bundles) == 2 && (a.WorkItem != b.WorkItem || a.Cycle != b.Cycle || a.PolicyDigest != b.PolicyDigest ||
		a.SourceCommit != b.SourceCommit || a.IntegrationBase != b.IntegrationBase) {
		return "Cycle bundles do not describe the same work item, cycle, policy, source, and Integration baseline."
	}
	return ""
}

func absentCycleBundle(bundle PatchBundle) bool {
	return bundle.Version == 0 && bundle.Role == "" && bundle.WorkItem == "" && bundle.Cycle == 0 &&
		bundle.PolicyDigest == "" && bundle.SourceCommit == "" && bundle.WorkerRoot == "" &&
		bundle.IntegrationBase == "" && len(bundle.Operations) == 0 && bundle.BundleDigest == ""
}

func activeCycleBundles(input CycleImport) []PatchBundle {
	var bundles []PatchBundle
	for _, bundle := range []PatchBundle{input.WorkerA, input.WorkerB} {
		if !absentCycleBundle(bundle) {
			bundles = append(bundles, bundle)
		}
	}
	return bundles
}

func validateCycleBundle(policy Policy, base string, bundle PatchBundle, present map[string]blobEntry) (string, string) {
	if bundle.Version != 1 {
		return CodePatchRejected, "PatchBundle version is unsupported."
	}
	if bundle.PolicyDigest != policy.Digest {
		return CodePatchTampered, "PatchBundle policy digest does not match the export policy."
	}
	digest, err := bundleDigest(bundle)
	if err != nil || digest != bundle.BundleDigest {
		return CodePatchTampered, "PatchBundle digest does not match the canonical payload."
	}
	if reason := verifyOperationContents(bundle.Operations); reason != "" {
		return CodePatchTampered, reason
	}
	if bundle.IntegrationBase != base {
		return CodePatchStale, "PatchBundle integration base is stale."
	}
	if reason := validateOperations(policy, bundle, present); reason != "" {
		return CodePatchRejected, reason
	}
	return "", ""
}

func validateCycleTree(input CycleImport, present map[string]blobEntry) string {
	ops := append(append([]Operation(nil), input.WorkerA.Operations...), input.WorkerB.Operations...)
	modified := make(map[string]bool, len(ops))
	for _, op := range ops {
		if modified[op.Path] {
			return "Cycle contributions overlap the same path."
		}
		modified[op.Path] = true
	}
	tree := newValTreeNode()
	for projectPath := range present {
		if !modified[projectPath] {
			tree.insert(projectPath, "")
		}
	}
	for _, op := range ops {
		if op.Action != ActionDelete {
			if reason := tree.insert(op.Path, op.Path); reason != "" {
				return reason
			}
		}
	}
	return ""
}

func cycleMergeCommit(repo *gitRepo, tree, firstParent, contribution string, bundle PatchBundle) (string, error) {
	message := fmt.Sprintf("Merge %s fixture contribution\n\nProjection-Role: %s\nWorker-Root: %s\nPatch-Digest: %s\nCycle: %d\nWork-Item: %s\nPolicy-Digest: %s\nSource-Commit: %s\n",
		bundle.Role, bundle.Role, bundle.WorkerRoot, bundle.BundleDigest, bundle.Cycle, bundle.WorkItem, bundle.PolicyDigest, bundle.SourceCommit)
	res, err := repo.runner.Run(gitx.Opts{Env: fixtureIdentity}, "commit-tree", tree, "-p", firstParent, "-p", contribution, "-m", message)
	if err := repo.check(res, err, "commit-tree"); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func cycleRefTransaction(repo *gitRepo, transaction string) error {
	res, err := repo.runner.Run(gitx.Opts{Stdin: []byte(transaction)}, "update-ref", "--stdin")
	return repo.check(res, err, "update-ref")
}

func cyclePatchAudit(policy Policy, bundle PatchBundle, decision, reason, commit string) AuditRecord {
	return newPatchAudit(policy, bundle.SourceCommit, bundle.Role, bundle.WorkerRoot, bundle.BundleDigest,
		bundle.IntegrationBase, bundle.Cycle, bundle.WorkItem, decision, reason, commit)
}
