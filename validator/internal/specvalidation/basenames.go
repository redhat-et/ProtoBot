package specvalidation

import (
	"fmt"
	"path"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic codes for requirement and interface file basename checks.
const (
	CodeRequirementInvalidBasename    = "requirement.invalid_basename"
	CodeRequirementIDBasenameMismatch = "requirement.id_basename_mismatch"
	CodeInterfaceNameBasenameMismatch = "interface.name_basename_mismatch"
)

func validateBasenames(snapshot layout.Snapshot) []Diagnostic {
	var diagnostics []Diagnostic
	for _, document := range snapshot.Requirements {
		diagnostics = append(diagnostics, validateRequirementBasename(document)...)
	}
	for _, document := range snapshot.Interfaces {
		diagnostics = append(diagnostics, validateInterfaceBasename(document)...)
	}
	return diagnostics
}

func validateRequirementBasename(document layout.Document[records.Requirement]) []Diagnostic {
	base := yamlBasename(document.Path)
	if err := records.ValidateRequirementID(base); err != nil {
		return []Diagnostic{{
			Code: CodeRequirementInvalidBasename,
			Path: document.Path,
			Message: fmt.Sprintf(
				"Requirement file basename %q is invalid. Use REQ-<SCOPE>-<NNNNN>.yaml with an uppercase scope and five-digit sequence.",
				base,
			),
		}}
	}
	id := document.Record.ID
	if id == "" || id == base {
		return nil
	}
	return []Diagnostic{{
		Code:  CodeRequirementIDBasenameMismatch,
		Path:  document.Path,
		Field: "id",
		Message: fmt.Sprintf(
			"Requirement ID %q does not match file basename %q.",
			id,
			base,
		),
	}}
}

func validateInterfaceBasename(document layout.Document[records.InterfaceRecord]) []Diagnostic {
	base := yamlBasename(document.Path)
	name := document.Record.Name
	if name == "" || name == base {
		return nil
	}
	return []Diagnostic{{
		Code:  CodeInterfaceNameBasenameMismatch,
		Path:  document.Path,
		Field: "name",
		Message: fmt.Sprintf(
			"Interface name %q does not match file basename %q.",
			name,
			base,
		),
	}}
}

func yamlBasename(relPath string) string {
	return strings.TrimSuffix(path.Base(relPath), ".yaml")
}
