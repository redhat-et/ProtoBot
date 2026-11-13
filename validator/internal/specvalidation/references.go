package specvalidation

import (
	"fmt"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic code for a dangling interface or requirement reference.
const CodeReferenceNotFound = "reference.not_found"

func validateReferences(snapshot layout.Snapshot) []Diagnostic {
	knownRequirements := declaredRequirementIDs(snapshot.Requirements)
	knownInterfaces := declaredInterfaceNames(snapshot.Interfaces)
	var diagnostics []Diagnostic
	for _, document := range snapshot.Requirements {
		diagnostics = append(diagnostics, validateInterfaceAttachments(document, knownInterfaces)...)
		diagnostics = append(diagnostics, validateRelationshipTargets(document, knownRequirements)...)
	}
	return diagnostics
}

func declaredRequirementIDs(requirements []layout.Document[records.Requirement]) map[string]bool {
	ids := make(map[string]bool, len(requirements))
	for _, document := range requirements {
		if document.Record.ID == "" {
			continue
		}
		ids[document.Record.ID] = true
	}
	return ids
}

func declaredInterfaceNames(interfaces []layout.Document[records.InterfaceRecord]) map[string]bool {
	names := make(map[string]bool, len(interfaces))
	for _, document := range interfaces {
		if document.Record.Name == "" {
			continue
		}
		names[document.Record.Name] = true
	}
	return names
}

func validateInterfaceAttachments(document layout.Document[records.Requirement], known map[string]bool) []Diagnostic {
	var diagnostics []Diagnostic
	for index, name := range document.Record.AppliesTo.Interfaces {
		if known[name] {
			continue
		}
		field := fmt.Sprintf("applies_to.interfaces[%d]", index)
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

func validateRelationshipTargets(document layout.Document[records.Requirement], known map[string]bool) []Diagnostic {
	var diagnostics []Diagnostic
	for index, relationship := range document.Record.Relationships {
		if known[relationship.Target] {
			continue
		}
		field := fmt.Sprintf("relationships[%d].target", index)
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
