package specvalidation

import (
	"fmt"
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
	CodeReused      = "requirement.id_reused"
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

// CompareBase checks identifier format and uniqueness on the comparison
// base, then rejects deletion or reuse of requirement IDs that already
// exist there. Format and uniqueness of the target snapshot are the
// caller's responsibility via Validate.
func CompareBase(target, base layout.Snapshot) []Diagnostic {
	diagnostics := validateRequirementIDs(base.Requirements)
	diagnostics = append(diagnostics, validateDeletionAndReuse(target.Requirements, base.Requirements)...)
	sortDiagnostics(diagnostics)
	return diagnostics
}

func validateDeletionAndReuse(target, base []layout.Document[records.Requirement]) []Diagnostic {
	targetPaths := validIDPaths(target)
	basePaths := validIDPaths(base)

	var diagnostics []Diagnostic
	for _, id := range sortedKeys(basePaths) {
		current, exists := targetPaths[id]
		if !exists {
			for _, path := range basePaths[id] {
				diagnostics = append(diagnostics, Diagnostic{
					Code:  CodeDeleted,
					Path:  path,
					Field: "id",
					Message: fmt.Sprintf(
						"Requirement ID %q was deleted. Retire the record instead of removing it.",
						id,
					),
				})
			}
			continue
		}

		baseSet := make(map[string]bool, len(basePaths[id]))
		for _, path := range basePaths[id] {
			baseSet[path] = true
		}
		joined := strings.Join(basePaths[id], ", ")
		for _, path := range current {
			if baseSet[path] {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeReused,
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
