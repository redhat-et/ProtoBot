package specvalidation

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
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
func CompareBase(target, base layout.Snapshot) []Diagnostic {
	diagnostics := validateDeletionAndReuse(target, base)
	sortDiagnostics(diagnostics)
	return diagnostics
}

func validateDeletionAndReuse(target, base layout.Snapshot) []Diagnostic {
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
		current, exists := targetValidPaths[id]
		if !exists {
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
				if !targetPathExists(target, path, targetDocByPath) {
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
				// Per Finding 2: do not emit requirement.deleted; leave layout parse diagnostic as the failure.
			}
			continue
		}

		baseSet := make(map[string]bool, len(baseValidPaths[id]))
		for _, path := range baseValidPaths[id] {
			baseSet[path] = true
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

	for _, doc := range baseInvalidDocs {
		path := doc.Path
		rawID := doc.Record.ID

		if seenDeletedPaths[path] {
			continue
		}

		// If the file still exists in the target tree (e.g. fixed in-place to
		// a valid ID, left as-is, or unparseable), do not emit requirement.deleted.
		// Existing tests require in-place format fixes to pass CompareBase.
		if targetPathExists(target, path, targetDocByPath) {
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

func targetPathExists(target layout.Snapshot, relPath string, targetDocByPath map[string]layout.Document[records.Requirement]) bool {
	if _, ok := targetDocByPath[relPath]; ok {
		return true
	}
	if target.Root != "" {
		if _, err := os.Stat(filepath.Join(target.Root, filepath.FromSlash(relPath))); err == nil {
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
