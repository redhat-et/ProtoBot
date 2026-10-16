package cli

import (
	"fmt"
	"sort"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

// checkArtifactOperationDeltas compares registered paths and canonical text
// with the default-branch tree. A missing base config gives project init an
// empty registry, but current artifacts are still compared with tree files.
// Registry metadata other than path and text is intentionally outside this check.
func checkArtifactOperationDeltas(root string, snapshot specvalidation.Snapshot, proposed map[string]bool) *commandFailure {
	defaultHead, failure := defaultBranchHead(root, snapshot.Config.Repository)
	if failure != nil {
		return failure
	}
	baseArtifacts, failure := defaultBranchArtifacts(root, defaultHead, snapshot.ConfigPath)
	if failure != nil {
		return failure
	}
	currentArtifacts := make(map[string]records.ArtifactEntry, len(snapshot.Config.Artifacts))
	for _, artifact := range snapshot.Config.Artifacts {
		currentArtifacts[artifact.ID] = artifact
	}
	operations := proposedArtifactOperations(snapshot, proposed)

	diagnostics, failure := existingArtifactOperationDiagnostics(root, defaultHead, snapshot.ConfigPath, baseArtifacts, currentArtifacts, operations)
	if failure != nil {
		return failure
	}
	newDiagnostics, failure := newArtifactRegistrationDiagnostics(root, defaultHead, snapshot.ConfigPath, baseArtifacts, currentArtifacts, operations)
	if failure != nil {
		return failure
	}
	diagnostics = append(diagnostics, newDiagnostics...)
	sortArtifactOperationDiagnostics(diagnostics)
	return failureFromDiagnostics(diagnostics)
}

func defaultBranchArtifacts(root, defaultHead, configPath string) (map[string]records.ArtifactEntry, *commandFailure) {
	configData, present, regular, failure := gitTreeFile(root, defaultHead, configPath)
	if failure != nil {
		return nil, failure
	}
	artifacts := make(map[string]records.ArtifactEntry)
	if !present {
		return artifacts, nil
	}
	if !regular {
		return nil, projectFailure("project.default_configuration_invalid", "The default-branch project configuration is not a regular file.")
	}
	var config records.ProjectConfig
	if err := storage.Decode(configData, &config); err != nil {
		return nil, projectFailure("project.default_configuration_invalid", "The default-branch project configuration could not be decoded.")
	}
	for _, artifact := range config.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	return artifacts, nil
}

func existingArtifactOperationDiagnostics(root, defaultHead, configPath string, baseArtifacts, currentArtifacts map[string]records.ArtifactEntry, operations map[string]bool) ([]specvalidation.Diagnostic, *commandFailure) {
	diagnostics := make([]specvalidation.Diagnostic, 0)
	baseIDs := sortedArtifactIDs(baseArtifacts)
	for _, id := range baseIDs {
		baseArtifact := baseArtifacts[id]
		currentArtifact, registered := currentArtifacts[id]
		if !registered {
			diagnostics = append(diagnostics, unregisteredArtifactDiagnostic(configPath, id))
			continue
		}
		changed := baseArtifact.Path != currentArtifact.Path
		if !changed {
			var failure *commandFailure
			changed, failure = artifactContentDiffersFromDefault(root, defaultHead, currentArtifact)
			if failure != nil {
				return nil, failure
			}
		}
		if changed && !operations[id] {
			diagnostics = append(diagnostics, missingArtifactOperationDiagnostic(configPath, id))
		}
	}
	return diagnostics, nil
}

func newArtifactRegistrationDiagnostics(root, defaultHead, configPath string, baseArtifacts, currentArtifacts map[string]records.ArtifactEntry, operations map[string]bool) ([]specvalidation.Diagnostic, *commandFailure) {
	diagnostics := make([]specvalidation.Diagnostic, 0)
	currentIDs := sortedArtifactIDs(currentArtifacts)
	for _, id := range currentIDs {
		if _, existed := baseArtifacts[id]; existed {
			continue
		}
		// A new registry ID is not a delta if this path and content already exist unchanged.
		changed, failure := artifactContentDiffersFromDefault(root, defaultHead, currentArtifacts[id])
		if failure != nil {
			return nil, failure
		}
		if changed && !operations[id] {
			diagnostics = append(diagnostics, missingArtifactOperationDiagnostic(configPath, id))
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

func artifactContentDiffersFromDefault(root, defaultHead string, artifact records.ArtifactEntry) (bool, *commandFailure) {
	baseContent, present, regular, failure := gitTreeFile(root, defaultHead, artifact.Path)
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

func missingArtifactOperationDiagnostic(path, id string) specvalidation.Diagnostic {
	return specvalidation.Diagnostic{
		Code:     "artifact.operation_missing",
		Severity: "error",
		Path:     path,
		RecordID: id,
		Field:    fmt.Sprintf("artifacts[id=%s].operation", id),
		Message:  fmt.Sprintf("Registered artifact %q differs from the default branch but has no operation in a proposed change set.", id),
		Hint:     fmt.Sprintf("Add an artifact operation for %q to a proposed change set. If the operation is already approved on the canonical remote, reconcile the local default branch with the Source Control Manager's repo_state, then rerun check.", id),
	}
}

func unregisteredArtifactDiagnostic(path, id string) specvalidation.Diagnostic {
	return specvalidation.Diagnostic{
		Code:     "artifact.unregistered",
		Severity: "error",
		Path:     path,
		RecordID: id,
		Field:    fmt.Sprintf("artifacts[id=%s]", id),
		Message:  fmt.Sprintf("Artifact %q is registered on the default branch but is absent from the current registry.", id),
		Hint:     "Restore the artifact registration; registered artifacts cannot be unregistered.",
	}
}
