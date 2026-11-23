package cli

import (
	"fmt"
	"sort"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

// checkArtifactOperationDeltas compares registered paths and canonical text
// with the artifact comparison base: the commit named by artifactBase, or
// the default-branch head when artifactBase is empty. A missing base config
// gives project init an empty registry, but current artifacts are still
// compared with tree files. Registry metadata other than path and text is
// intentionally outside this check. Only this delta honors --base-ref;
// change-set approval and proposed base freshness always read the default
// branch.
func checkArtifactOperationDeltas(root string, snapshot specvalidation.Snapshot, proposed map[string]bool, artifactBase string) *commandFailure {
	baseCommit, failure := artifactComparisonBase(root, snapshot.Config.Repository, artifactBase)
	if failure != nil {
		return failure
	}
	baseArtifacts, failure := baseCommitArtifacts(root, baseCommit, snapshot.ConfigPath, artifactBase)
	if failure != nil {
		return failure
	}
	currentArtifacts := make(map[string]records.ArtifactEntry, len(snapshot.Config.Artifacts))
	for _, artifact := range snapshot.Config.Artifacts {
		currentArtifacts[artifact.ID] = artifact
	}
	operations := proposedArtifactOperations(snapshot, proposed)

	diagnostics, failure := existingArtifactOperationDiagnostics(root, baseCommit, snapshot.ConfigPath, artifactBase, baseArtifacts, currentArtifacts, operations)
	if failure != nil {
		return failure
	}
	newDiagnostics, failure := newArtifactRegistrationDiagnostics(root, baseCommit, snapshot.ConfigPath, artifactBase, baseArtifacts, currentArtifacts, operations)
	if failure != nil {
		return failure
	}
	diagnostics = append(diagnostics, newDiagnostics...)
	sortArtifactOperationDiagnostics(diagnostics)
	return failureFromDiagnostics(diagnostics)
}

// artifactComparisonBase returns the commit that the artifact-operation
// delta compares against: artifactBase when check --base-ref named one,
// otherwise the default-branch head. The default-branch head is resolved
// and required either way, because every full or scoped check requires a
// resolvable default branch; with --base-ref the resolved head is simply
// not the artifact comparison base.
func artifactComparisonBase(root string, repository records.RepositoryConfig, artifactBase string) (string, *commandFailure) {
	defaultHead, failure := defaultBranchHead(root, repository)
	if failure != nil {
		return "", failure
	}
	if artifactBase != "" {
		return artifactBase, nil
	}
	return defaultHead, nil
}

// artifactBaseLabel names the artifact comparison base in diagnostics: the
// commit that check --base-ref selected, or the default branch.
func artifactBaseLabel(artifactBase string) string {
	if artifactBase != "" {
		return fmt.Sprintf("the base commit %s", artifactBase)
	}
	return "the default branch"
}

// baseConfigurationFailureMessage reports that the project configuration
// which the artifact comparison base holds could not be supplied. The
// default branch keeps its established message text.
func baseConfigurationFailureMessage(artifactBase, problem string) string {
	if artifactBase == "" {
		return "The default-branch project configuration " + problem
	}
	return fmt.Sprintf("The project configuration of the base commit %s %s", artifactBase, problem)
}

func baseCommitArtifacts(root, baseCommit, configPath, artifactBase string) (map[string]records.ArtifactEntry, *commandFailure) {
	configData, present, regular, failure := gitTreeFile(root, baseCommit, configPath)
	if failure != nil {
		return nil, failure
	}
	artifacts := make(map[string]records.ArtifactEntry)
	if !present {
		return artifacts, nil
	}
	if !regular {
		return nil, projectFailure("project.default_configuration_invalid", baseConfigurationFailureMessage(artifactBase, "is not a regular file."))
	}
	var config records.ProjectConfig
	if err := storage.Decode(configData, &config); err != nil {
		return nil, projectFailure("project.default_configuration_invalid", baseConfigurationFailureMessage(artifactBase, "could not be decoded."))
	}
	for _, artifact := range config.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	return artifacts, nil
}

func existingArtifactOperationDiagnostics(root, baseCommit, configPath, artifactBase string, baseArtifacts, currentArtifacts map[string]records.ArtifactEntry, operations map[string]bool) ([]specvalidation.Diagnostic, *commandFailure) {
	diagnostics := make([]specvalidation.Diagnostic, 0)
	baseIDs := sortedArtifactIDs(baseArtifacts)
	for _, id := range baseIDs {
		baseArtifact := baseArtifacts[id]
		currentArtifact, registered := currentArtifacts[id]
		if !registered {
			diagnostics = append(diagnostics, unregisteredArtifactDiagnostic(configPath, id, artifactBase))
			continue
		}
		changed := baseArtifact.Path != currentArtifact.Path
		if !changed {
			var failure *commandFailure
			changed, failure = artifactContentDiffersFromBase(root, baseCommit, currentArtifact)
			if failure != nil {
				return nil, failure
			}
		}
		if changed && !operations[id] {
			diagnostics = append(diagnostics, missingArtifactOperationDiagnostic(configPath, id, artifactBase))
		}
	}
	return diagnostics, nil
}

func newArtifactRegistrationDiagnostics(root, baseCommit, configPath, artifactBase string, baseArtifacts, currentArtifacts map[string]records.ArtifactEntry, operations map[string]bool) ([]specvalidation.Diagnostic, *commandFailure) {
	diagnostics := make([]specvalidation.Diagnostic, 0)
	currentIDs := sortedArtifactIDs(currentArtifacts)
	for _, id := range currentIDs {
		if _, existed := baseArtifacts[id]; existed {
			continue
		}
		// A new registry ID is not a delta if this path and content already exist unchanged.
		changed, failure := artifactContentDiffersFromBase(root, baseCommit, currentArtifacts[id])
		if failure != nil {
			return nil, failure
		}
		if changed && !operations[id] {
			diagnostics = append(diagnostics, missingArtifactOperationDiagnostic(configPath, id, artifactBase))
		}
	}
	return diagnostics, nil
}

func sortArtifactOperationDiagnostics(diagnostics []specvalidation.Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		for _, pair := range [][2]string{
			{left.Path, right.Path}, {left.Code, right.Code}, {left.RecordID, right.RecordID},
			{left.Field, right.Field}, {left.Severity, right.Severity},
			{left.Message, right.Message}, {left.Hint, right.Hint},
		} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return false
	})
}

func proposedArtifactOperations(snapshot specvalidation.Snapshot, proposed map[string]bool) map[string]bool {
	operations := make(map[string]bool)
	for _, document := range snapshot.ChangeSets {
		if !proposed[document.Value.ID] {
			continue
		}
		for _, operation := range document.Value.ArtifactOperations {
			operations[operation.ArtifactID] = true
		}
	}
	return operations
}

func artifactContentDiffersFromBase(root, baseCommit string, artifact records.ArtifactEntry) (bool, *commandFailure) {
	baseContent, present, regular, failure := gitTreeFile(root, baseCommit, artifact.Path)
	if failure != nil {
		return false, failure
	}
	if !present || !regular {
		return true, nil
	}
	baseDigest, err := specvalidation.CanonicalTextDigest(baseContent)
	if err != nil {
		return true, nil
	}
	// The current artifact digest was validated against working-tree content before this comparison.
	return artifact.Digest != baseDigest, nil
}

func gitTreeFile(root, commit, relative string) ([]byte, bool, bool, *commandFailure) {
	entries, failure := listRevisionTree(root, commit, []string{relative})
	if failure != nil {
		return nil, false, false, failure
	}
	entries = exactTreeEntries(entries, []string{relative})
	if len(entries) == 0 {
		return nil, false, false, nil
	}
	if len(entries) != 1 {
		return nil, false, false, ioFailure("revision.read_failed", fmt.Sprintf("The tree of commit %s contains duplicate entries for %s.", commit, relative))
	}
	entry := entries[0]
	if entry.mode != treeModeRegular && entry.mode != treeModeExecutable {
		return nil, true, false, nil
	}
	contents, failure := readRevisionBlobs(root, entries)
	if failure != nil {
		return nil, false, false, failure
	}
	data, found := contents[entry.object]
	if !found {
		return nil, false, false, ioFailure("revision.read_failed", fmt.Sprintf("The blob for %s in commit %s could not be read.", relative, commit))
	}
	return data, true, true, nil
}

func sortedArtifactIDs(artifacts map[string]records.ArtifactEntry) []string {
	ids := make([]string, 0, len(artifacts))
	for id := range artifacts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func missingArtifactOperationDiagnostic(path, id, artifactBase string) specvalidation.Diagnostic {
	// The default-branch hint keeps its established text byte-for-byte.
	// A named base commit cannot be cleared by reconciling the local
	// default branch, so its hint names the selected base instead.
	hint := fmt.Sprintf("Add an artifact operation for %q to a proposed change set. If the operation is already approved on the canonical remote, reconcile the local default branch with the Source Control Manager's repo_state, then rerun check.", id)
	if artifactBase != "" {
		hint = fmt.Sprintf("Add an artifact operation for %q to a proposed change set, or rerun check without --base-ref when the delta comes from the selected base commit %s.", id, artifactBase)
	}
	return specvalidation.Diagnostic{
		Code:     "artifact.operation_missing",
		Severity: "error",
		Path:     path,
		RecordID: id,
		Field:    fmt.Sprintf("artifacts[id=%s].operation", id),
		Message:  fmt.Sprintf("Registered artifact %q differs from %s but has no operation in a proposed change set.", id, artifactBaseLabel(artifactBase)),
		Hint:     hint,
	}
}

func unregisteredArtifactDiagnostic(path, id, artifactBase string) specvalidation.Diagnostic {
	return specvalidation.Diagnostic{
		Code:     "artifact.unregistered",
		Severity: "error",
		Path:     path,
		RecordID: id,
		Field:    fmt.Sprintf("artifacts[id=%s]", id),
		Message:  fmt.Sprintf("Artifact %q is registered on %s but is absent from the current registry.", id, artifactBaseLabel(artifactBase)),
		Hint:     "Restore the artifact registration; registered artifacts cannot be unregistered.",
	}
}
