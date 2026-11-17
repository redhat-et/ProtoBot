package specvalidation

import (
	"fmt"
	"path"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic codes for dangling or ambiguous references.
const (
	CodeReferenceNotFound  = "reference.not_found"
	CodeReferenceAmbiguous = "reference.ambiguous"
)

func validateReferences(snapshot layout.Snapshot, layoutDiagnostics ...layout.Diagnostic) []Diagnostic {
	failedPaths := failedDocumentPaths(layoutDiagnostics)
	knownRequirements := declaredRequirementIDs(snapshot.Requirements)
	knownInterfaces := declaredInterfaceNames(snapshot.Interfaces)
	var diagnostics []Diagnostic
	for _, document := range snapshot.Requirements {
		diagnostics = append(diagnostics, validateInterfaceAttachments(document, knownInterfaces, failedPaths)...)
		diagnostics = append(diagnostics, validateRelationshipTargets(document, knownRequirements, failedPaths)...)
	}
	return diagnostics
}

func failedDocumentPaths(diagnostics []layout.Diagnostic) map[string]bool {
	failed := make(map[string]bool, len(diagnostics))
	for _, d := range diagnostics {
		if d.Path != "" {
			failed[d.Path] = true
		}
	}
	return failed
}

func declaredRequirementIDs(requirements []layout.Document[records.Requirement]) map[string][]string {
	ids := make(map[string][]string, len(requirements))
	for _, document := range requirements {
		if document.Record.ID == "" {
			continue
		}
		ids[document.Record.ID] = append(ids[document.Record.ID], document.Path)
	}
	return ids
}

func declaredInterfaceNames(interfaces []layout.Document[records.InterfaceRecord]) map[string][]string {
	names := make(map[string][]string, len(interfaces))
	for _, document := range interfaces {
		if document.Record.Name == "" {
			continue
		}
		names[document.Record.Name] = append(names[document.Record.Name], document.Path)
	}
	return names
}

// targetFailedToLoad reports whether the store-local file for target failed
// to load. Suppression is keyed on dir/target.yaml in the same store, so
// interface attachments assume the file stem matches the declared name
// (enforced by interface.name_basename_mismatch). Targets that contain
// "/", "\", or ".." are not eligible: path.Join would otherwise clean
// ".." out of the store directory and suppress a diagnostic for a file
// in another store.
func targetFailedToLoad(dir, target string, failedPaths map[string]bool) bool {
	if len(failedPaths) == 0 || !storeLocalTarget(target) {
		return false
	}
	candidate := path.Join(dir, target+".yaml")
	if path.Dir(candidate) != dir {
		return false
	}
	return failedPaths[candidate]
}

func storeLocalTarget(target string) bool {
	if target == "" {
		return false
	}
	if strings.ContainsAny(target, `/\`) || strings.Contains(target, "..") {
		return false
	}
	return path.Base(target) == target
}

func validateInterfaceAttachments(
	document layout.Document[records.Requirement],
	declarations map[string][]string,
	failedPaths map[string]bool,
) []Diagnostic {
	var diagnostics []Diagnostic
	for index, name := range document.Record.AppliesTo.Interfaces {
		paths := declarations[name]
		if len(paths) == 1 {
			continue
		}
		field := fmt.Sprintf("applies_to.interfaces[%d]", index)
		if len(paths) > 1 {
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeReferenceAmbiguous,
				Path:  document.Path,
				Field: field,
				Message: fmt.Sprintf(
					"Interface %q at %s is ambiguous; declared in multiple interface records.",
					name,
					field,
				),
			})
			continue
		}
		if targetFailedToLoad(layout.InterfacesDir, name, failedPaths) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Code:  CodeReferenceNotFound,
			Path:  document.Path,
			Field: field,
			Message: fmt.Sprintf(
				"Interface %q at %s is not declared.",
				name,
				field,
			),
		})
	}
	return diagnostics
}

func validateRelationshipTargets(
	document layout.Document[records.Requirement],
	declarations map[string][]string,
	failedPaths map[string]bool,
) []Diagnostic {
	var diagnostics []Diagnostic
	for index, relationship := range document.Record.Relationships {
		// Duplicate requirement IDs are reported by requirement.duplicate_id.
		// A present ID, unique or not, is not a dangling reference.
		if len(declarations[relationship.Target]) > 0 {
			continue
		}
		field := fmt.Sprintf("relationships[%d].target", index)
		if targetFailedToLoad(layout.RequirementsDir, relationship.Target, failedPaths) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Code:  CodeReferenceNotFound,
			Path:  document.Path,
			Field: field,
			Message: fmt.Sprintf(
				"Requirement %q at %s is not declared.",
				relationship.Target,
				field,
			),
		})
	}
	return diagnostics
}
