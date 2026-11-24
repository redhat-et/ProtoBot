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
	CodeInterfaceInvalidBasename      = "interface.invalid_basename"
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
				"Requirement file basename %q is invalid. Use REQ-<SCOPE>-<NNNNN>.yaml with an uppercase scope of hyphen-separated alphanumeric segments and a five-digit sequence.",
				base,
			),
		}}
	}
	id := document.Record.ID
	if strings.TrimSpace(id) == "" || id == base {
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
	if err := records.ValidateInterfaceName(base); err != nil {
		return []Diagnostic{{
			Code: CodeInterfaceInvalidBasename,
			Path: document.Path,
			Message: fmt.Sprintf(
				"Interface file basename %q is invalid. %s",
				base,
				interfaceNameRemediation,
			),
		}}
	}

	name := document.Record.Name
	if strings.TrimSpace(name) == "" || name == base {
		return nil
	}
	message := fmt.Sprintf(
		"Interface name %q does not match file basename %q.",
		name,
		base,
	)
	if strings.Contains(name, "/") || strings.Contains(name, `\`) || path.Base(name) != name {
		message = fmt.Sprintf(
			"Interface name %q does not match file basename %q; interface name cannot contain path separators and must be a single path segment.",
			name,
			base,
		)
	}
	return []Diagnostic{{
		Code:    CodeInterfaceNameBasenameMismatch,
		Path:    document.Path,
		Field:   "name",
		Message: message,
	}}
}

func yamlBasename(relPath string) string {
	return strings.TrimSuffix(path.Base(relPath), ".yaml")
}
