package specvalidation

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
	"github.com/redhat-et/protobot/validator/internal/revision"
)

// Diagnostic codes for requirement identifier checks.
const (
	CodeInvalidID   = "requirement.invalid_id"
	CodeDuplicateID = "requirement.duplicate_id"
	CodeDeleted     = "requirement.deleted"
	CodeReusedID    = "requirement.reused_id"
)

func validateRequirementIDs(requirements []layout.Document[records.Requirement]) []Diagnostic {
	var diagnostics []Diagnostic
	byID := make(map[string][]string)
	for _, document := range requirements {
		id := document.Record.ID
		if err := records.ValidateRequirementID(id); err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeInvalidID,
				Path:  document.Path,
				Field: "id",
				Message: fmt.Sprintf(
					"Requirement ID %q is invalid. Use REQ-<SCOPE>-<NNNNN> with an uppercase scope and five-digit sequence.",
					id,
				),
			})
			continue
		}
		byID[id] = append(byID[id], document.Path)
	}

	for _, id := range sortedKeys(byID) {
		paths := uniqueSorted(byID[id])
		if len(paths) < 2 {
			continue
		}
		joined := strings.Join(paths, ", ")
		for _, path := range paths {
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeDuplicateID,
				Path:  path,
				Field: "id",
				Message: fmt.Sprintf(
					"Requirement ID %q is declared more than once (%s).",
					id,
					joined,
				),
			})
		}
	}
	return diagnostics
}

// CompareBase rejects deletion or reuse of requirement IDs that already
// exist in the comparison base. Format and uniqueness of the target
// snapshot are the caller's responsibility via Validate.
// Target layout diagnostics are used to determine whether an unparsed file
// still exists on disk without host I/O.
// Base diagnostics represent comparison-base files that could not be parsed;
// if any such file is absent from the target, a deletion diagnostic is emitted.
func CompareBase(target, base layout.Snapshot, targetDiagnostics []layout.Diagnostic, baseDiagnostics ...layout.Diagnostic) []Diagnostic {
	repaired := repairedBaseRequirements(target, base, targetDiagnostics, baseDiagnostics)
	diagnostics := validateDeletion(target, base, targetDiagnostics, repaired)
	diagnostics = append(diagnostics, validateReuse(target, base)...)
	diagnostics = append(diagnostics, validateUnparsedBaseDeletions(target, targetDiagnostics, baseDiagnostics, repaired)...)
	sortDiagnostics(diagnostics)
	return diagnostics
}

func validateDeletion(target, base layout.Snapshot, targetDiagnostics []layout.Diagnostic, repaired map[string]bool) []Diagnostic {
	targetDocByPath := make(map[string]layout.Document[records.Requirement], len(target.Requirements))
	targetRawIDs := make(map[string][]string, len(target.Requirements))
	for _, doc := range target.Requirements {
		targetDocByPath[doc.Path] = doc
		targetRawIDs[doc.Record.ID] = append(targetRawIDs[doc.Record.ID], doc.Path)
	}

	targetValidPaths := validIDPaths(target.Requirements)
	baseValidPaths := validIDPaths(base.Requirements)

	var baseInvalidDocs []layout.Document[records.Requirement]
	for _, doc := range base.Requirements {
		if records.ValidateRequirementID(doc.Record.ID) != nil {
			baseInvalidDocs = append(baseInvalidDocs, doc)
		}
	}
	sort.Slice(baseInvalidDocs, func(i, j int) bool {
		return baseInvalidDocs[i].Path < baseInvalidDocs[j].Path
	})

	seenDeletedPaths := make(map[string]bool)
	var diagnostics []Diagnostic
	for _, id := range sortedKeys(baseValidPaths) {
		if _, exists := targetValidPaths[id]; exists {
			continue
		}
		for _, path := range baseValidPaths[id] {
			targetDoc, hasTargetDoc := targetDocByPath[path]
			if hasTargetDoc && records.ValidateRequirementID(targetDoc.Record.ID) == nil && targetDoc.Record.ID != id {
				// In-place ID change: path exists and parsed with a different valid ID.
				seenDeletedPaths[path] = true
				diagnostics = append(diagnostics, Diagnostic{
					Code:  CodeDeleted,
					Path:  path,
					Field: "id",
					Message: fmt.Sprintf(
						"Requirement ID %q was deleted. Retire the record instead of removing it.",
						id,
					),
				})
				continue
			}
			if !TargetPathExists(target, targetDiagnostics, path) {
				seenDeletedPaths[path] = true
				diagnostics = append(diagnostics, Diagnostic{
					Code:  CodeDeleted,
					Path:  path,
					Field: "id",
					Message: fmt.Sprintf(
						"Requirement ID %q was deleted. Retire the record instead of removing it.",
						id,
					),
				})
				continue
			}
			// Otherwise: path still exists in target tree (parsed or not),
			// but did not parse with a different valid ID.
			// If path exists in target tree, suppress requirement.deleted and let layout parse diagnostic report the error.
		}
	}

	for _, doc := range baseInvalidDocs {
		path := doc.Path
		rawID := doc.Record.ID

		if seenDeletedPaths[path] {
			continue
		}

		if repaired[path] {
			continue
		}

		// If the file still exists in the target tree (e.g. fixed in-place to
		// a valid ID, left as-is, or unparseable), do not emit requirement.deleted.
		// Existing tests require in-place format fixes to pass CompareBase.
		if TargetPathExists(target, targetDiagnostics, path) {
			continue
		}

		// If rawID is still present elsewhere in target, path was not deleted without rawID.
		if len(targetRawIDs[rawID]) > 0 {
			continue
		}

		seenDeletedPaths[path] = true
		diagnostics = append(diagnostics, Diagnostic{
			Code:  CodeDeleted,
			Path:  path,
			Field: "id",
			Message: fmt.Sprintf(
				"Requirement ID %q was deleted. Retire the record instead of removing it.",
				rawID,
			),
		})
	}

	return diagnostics
}

func validateReuse(target, base layout.Snapshot) []Diagnostic {
	targetValidPaths := validIDPaths(target.Requirements)
	baseValidPaths := validIDPaths(base.Requirements)

	var diagnostics []Diagnostic
	for _, id := range sortedKeys(baseValidPaths) {
		current, exists := targetValidPaths[id]
		if !exists {
			continue
		}

		baseSet := make(map[string]bool, len(baseValidPaths[id]))
		for _, path := range baseValidPaths[id] {
			baseSet[path] = true
		}
		if isBasenameCorrection(id, current, baseSet) {
			continue
		}
		joined := strings.Join(baseValidPaths[id], ", ")
		for _, path := range current {
			if baseSet[path] {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeReusedID,
				Path:  path,
				Field: "id",
				Message: fmt.Sprintf(
					"Requirement ID %q was reused for a different record; comparison base has it at %s.",
					id,
					joined,
				),
			})
		}
	}
	return diagnostics
}

func isBasenameCorrection(id string, current []string, baseSet map[string]bool) bool {
	canonical := canonicalRequirementPath(id)
	return len(current) == 1 && current[0] == canonical && !baseSet[canonical]
}

func canonicalRequirementPath(id string) string {
	return path.Join(layout.RequirementsDir, id+".yaml")
}

// IsCanonicalRequirementPath reports whether relPath matches
// spec/requirements/<ID>.yaml where ID is a valid requirement ID.
func IsCanonicalRequirementPath(relPath string) bool {
	if path.Dir(relPath) != layout.RequirementsDir {
		return false
	}
	base := path.Base(relPath)
	if !strings.HasSuffix(base, ".yaml") {
		return false
	}
	id := strings.TrimSuffix(base, ".yaml")
	return records.ValidateRequirementID(id) == nil
}

func isCanonicalRequirementPath(relPath string) bool {
	return IsCanonicalRequirementPath(relPath)
}

// BasePathExists reports whether relPath is present in base requirements
// or in base diagnostics.
func BasePathExists(base layout.Snapshot, baseDiagnostics []layout.Diagnostic, relPath string) bool {
	for _, doc := range base.Requirements {
		if doc.Path == relPath {
			return true
		}
	}
	for _, diag := range baseDiagnostics {
		if diag.Path == relPath {
			return true
		}
	}
	return false
}

func repairedBaseRequirements(target, base layout.Snapshot, targetDiagnostics []layout.Diagnostic, baseDiagnostics []layout.Diagnostic) map[string]bool {
	targetRawIDs := make(map[string][]string, len(target.Requirements))
	for _, doc := range target.Requirements {
		targetRawIDs[doc.Record.ID] = append(targetRawIDs[doc.Record.ID], doc.Path)
	}

	baseValidPaths := validIDPaths(base.Requirements)

	var candidateReplacements []string
	for _, doc := range target.Requirements {
		if !isCanonicalRequirementPath(doc.Path) {
			continue
		}
		if doc.Path != canonicalRequirementPath(doc.Record.ID) {
			continue
		}
		if records.ValidateRequirementID(doc.Record.ID) != nil {
			continue
		}
		if _, inBaseValid := baseValidPaths[doc.Record.ID]; inBaseValid {
			continue
		}
		if BasePathExists(base, baseDiagnostics, doc.Path) {
			continue
		}
		candidateReplacements = append(candidateReplacements, doc.Path)
	}
	candidateReplacements = uniqueSorted(candidateReplacements)
	if len(candidateReplacements) == 0 {
		return nil
	}

	var removedNonCanonical []string
	seen := make(map[string]bool)
	for _, diag := range baseDiagnostics {
		if diag.Path == "" || seen[diag.Path] {
			continue
		}
		if !isUnreadableRequirementCode(diag.Code) {
			continue
		}
		seen[diag.Path] = true
		if path.Dir(diag.Path) == layout.RequirementsDir && !isCanonicalRequirementPath(diag.Path) && !TargetPathExists(target, targetDiagnostics, diag.Path) {
			removedNonCanonical = append(removedNonCanonical, diag.Path)
		}
	}

	for _, doc := range base.Requirements {
		if seen[doc.Path] {
			continue
		}
		seen[doc.Path] = true
		if records.ValidateRequirementID(doc.Record.ID) == nil {
			continue
		}
		if path.Dir(doc.Path) == layout.RequirementsDir && !isCanonicalRequirementPath(doc.Path) && !TargetPathExists(target, targetDiagnostics, doc.Path) && len(targetRawIDs[doc.Record.ID]) == 0 {
			removedNonCanonical = append(removedNonCanonical, doc.Path)
		}
	}

	removedNonCanonical = uniqueSorted(removedNonCanonical)
	if len(removedNonCanonical) != 1 || len(candidateReplacements) != 1 {
		return nil
	}

	return map[string]bool{removedNonCanonical[0]: true}
}

// IsRepairedBaseRequirement reports whether a non-canonical base requirement path
// was repaired by being replaced by a canonical requirement record in target.
func IsRepairedBaseRequirement(relPath string, target, base layout.Snapshot, targetDiagnostics []layout.Diagnostic, baseDiagnostics ...layout.Diagnostic) bool {
	repaired := repairedBaseRequirements(target, base, targetDiagnostics, baseDiagnostics)
	return repaired[relPath]
}

func validateUnparsedBaseDeletions(target layout.Snapshot, targetDiagnostics []layout.Diagnostic, baseDiagnostics []layout.Diagnostic, repaired map[string]bool) []Diagnostic {
	if len(baseDiagnostics) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var diagnostics []Diagnostic
	for _, diag := range baseDiagnostics {
		if diag.Path == "" || seen[diag.Path] {
			continue
		}
		if repaired[diag.Path] {
			continue
		}
		if !TargetPathExists(target, targetDiagnostics, diag.Path) {
			seen[diag.Path] = true
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeDeleted,
				Path:  diag.Path,
				Field: "id",
				Message: fmt.Sprintf(
					"Requirement ID could not be read from comparison base and file was removed (%s).",
					diag.Message,
				),
			})
		}
	}
	return diagnostics
}

// TargetPathExists reports whether relPath is present in target requirements
// or in target layout diagnostics.
func TargetPathExists(target layout.Snapshot, targetDiagnostics []layout.Diagnostic, relPath string) bool {
	for _, doc := range target.Requirements {
		if doc.Path == relPath {
			return true
		}
	}
	for _, diag := range targetDiagnostics {
		if diag.Path == relPath {
			return true
		}
	}
	return false
}

func validIDPaths(requirements []layout.Document[records.Requirement]) map[string][]string {
	byID := make(map[string][]string)
	for _, document := range requirements {
		id := document.Record.ID
		if records.ValidateRequirementID(id) != nil {
			continue
		}
		byID[id] = append(byID[id], document.Path)
	}
	for id, paths := range byID {
		byID[id] = uniqueSorted(paths)
	}
	return byID
}

func sortedKeys(byID map[string][]string) []string {
	keys := make([]string, 0, len(byID))
	for id := range byID {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	return keys
}

func uniqueSorted(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	out := sorted[:0]
	var previous string
	for i, path := range sorted {
		if i > 0 && path == previous {
			continue
		}
		out = append(out, path)
		previous = path
	}
	return out
}

func isUnreadableRequirementCode(code string) bool {
	switch code {
	case layout.CodeReadFailed, layout.CodeParseFailed,
		revision.CodeReadFailed, revision.CodeParseFailed:
		return true
	default:
		return false
	}
}
