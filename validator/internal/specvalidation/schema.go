package specvalidation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic codes for record schema failures.
const (
	CodeRequirementMissingField    = "requirement.missing_field"
	CodeRequirementInvalidStatus   = "requirement.invalid_status"
	CodeRequirementUnexpectedField = "requirement.unexpected_field"
	CodeInterfaceMissingField      = "interface.missing_field"
	CodeInterfaceUnexpectedField   = "interface.unexpected_field"
)

var (
	allowedStatuses = map[string]bool{
		records.StatusActive:  true,
		records.StatusRetired: true,
	}
	requiredRequirementKeys = []string{
		"id", "type", "text", "applies_to", "relationships", "status",
	}
	requiredRequirementStrings = []string{"id", "type", "text", "status"}
	requiredInterfaceStrings   = []string{"name", "kind", "description"}
)

func validateRecordSchema(snapshot layout.Snapshot) []Diagnostic {
	var diagnostics []Diagnostic
	for _, document := range snapshot.Requirements {
		diagnostics = append(diagnostics, validateRequirementSchema(document)...)
	}
	for _, document := range snapshot.Interfaces {
		diagnostics = append(diagnostics, validateInterfaceSchema(document)...)
	}
	return diagnostics
}

func validateRequirementSchema(document layout.Document[records.Requirement]) []Diagnostic {
	var diagnostics []Diagnostic
	record := document.Record
	values := map[string]string{
		"id":     record.ID,
		"type":   record.Type,
		"text":   record.Text,
		"status": record.Status,
	}
	for _, field := range requiredRequirementKeys {
		if fieldPresent(document.Fields, field) {
			continue
		}
		diagnostics = append(diagnostics, missingFieldDiagnostic(CodeRequirementMissingField, document.Path, field))
	}
	for _, field := range requiredRequirementStrings {
		if !fieldPresent(document.Fields, field) {
			continue
		}
		if strings.TrimSpace(values[field]) != "" {
			continue
		}
		diagnostics = append(diagnostics, missingFieldDiagnostic(CodeRequirementMissingField, document.Path, field))
	}
	if fieldPresent(document.Fields, "applies_to") && !hasApplicableSelector(record.AppliesTo) {
		diagnostics = append(diagnostics, missingFieldDiagnostic(CodeRequirementMissingField, document.Path, "applies_to"))
	}
	if fieldPresent(document.Fields, "status") && strings.TrimSpace(record.Status) != "" && !allowedStatuses[record.Status] {
		diagnostics = append(diagnostics, Diagnostic{
			Code:  CodeRequirementInvalidStatus,
			Path:  document.Path,
			Field: "status",
			Message: fmt.Sprintf(
				"Unsupported status %q. Use active or retired.",
				record.Status,
			),
		})
	}
	for _, field := range unexpectedFields(document.Fields, allowedRequirementField) {
		diagnostics = append(diagnostics, Diagnostic{
			Code:  CodeRequirementUnexpectedField,
			Path:  document.Path,
			Field: field,
			Message: fmt.Sprintf(
				"Unparsed field %q is not part of the V2 requirement schema.",
				field,
			),
		})
	}
	return diagnostics
}

func validateInterfaceSchema(document layout.Document[records.InterfaceRecord]) []Diagnostic {
	var diagnostics []Diagnostic
	record := document.Record
	values := map[string]string{
		"name":        record.Name,
		"kind":        record.Kind,
		"description": record.Description,
	}
	for _, field := range requiredInterfaceStrings {
		if fieldPresent(document.Fields, field) && strings.TrimSpace(values[field]) != "" {
			continue
		}
		diagnostics = append(diagnostics, missingFieldDiagnostic(CodeInterfaceMissingField, document.Path, field))
	}
	for _, field := range unexpectedFields(document.Fields, allowedInterfaceField) {
		diagnostics = append(diagnostics, Diagnostic{
			Code:  CodeInterfaceUnexpectedField,
			Path:  document.Path,
			Field: field,
			Message: fmt.Sprintf(
				"Unparsed field %q is not part of the V2 interface schema.",
				field,
			),
		})
	}
	return diagnostics
}

func missingFieldDiagnostic(code, path, field string) Diagnostic {
	return Diagnostic{
		Code:    code,
		Path:    path,
		Field:   field,
		Message: fmt.Sprintf("Required field %q is missing.", field),
	}
}

func fieldPresent(fields map[string]bool, field string) bool {
	if fields == nil {
		return false
	}
	return fields[field]
}

func hasApplicableSelector(appliesTo records.Applicability) bool {
	return hasNonBlankSelector(appliesTo.Interfaces) || hasNonBlankSelector(appliesTo.Scopes)
}

func hasNonBlankSelector(selectors []string) bool {
	for _, selector := range selectors {
		if strings.TrimSpace(selector) != "" {
			return true
		}
	}
	return false
}

func unexpectedFields(fields map[string]bool, allowed func(string) bool) []string {
	if fields == nil {
		return nil
	}
	var unexpected []string
	for field := range fields {
		if allowed(field) || hasDisallowedAncestor(field, fields, allowed) {
			continue
		}
		unexpected = append(unexpected, field)
	}
	sort.Strings(unexpected)
	return unexpected
}

func hasDisallowedAncestor(field string, fields map[string]bool, allowed func(string) bool) bool {
	for parent := parentField(field); parent != ""; parent = parentField(parent) {
		if !allowed(parent) && fields[parent] {
			return true
		}
	}
	return false
}

func parentField(field string) string {
	dot := strings.LastIndex(field, ".")
	bracket := strings.LastIndex(field, "[")
	if dot > bracket {
		return field[:dot]
	}
	if bracket > 0 {
		return field[:bracket]
	}
	return ""
}

func allowedRequirementField(field string) bool {
	switch field {
	case "id", "type", "text", "applies_to", "applies_to.interfaces", "applies_to.scopes", "relationships", "status":
		return true
	}
	return indexedField("applies_to.interfaces", field) ||
		indexedField("applies_to.scopes", field) ||
		indexedField("relationships", field) ||
		indexedChild("relationships", "type", field) ||
		indexedChild("relationships", "target", field)
}

func allowedInterfaceField(field string) bool {
	switch field {
	case "name", "kind", "description":
		return true
	}
	return false
}

func indexedField(prefix, field string) bool {
	rest, ok := strings.CutPrefix(field, prefix+"[")
	if !ok {
		return false
	}
	end := strings.IndexByte(rest, ']')
	if end < 0 || end != len(rest)-1 {
		return false
	}
	return decimalIndex(rest[:end])
}

func indexedChild(prefix, child, field string) bool {
	rest, ok := strings.CutPrefix(field, prefix+"[")
	if !ok {
		return false
	}
	end := strings.IndexByte(rest, ']')
	if end < 0 || rest[end:] != "]."+child {
		return false
	}
	return decimalIndex(rest[:end])
}

func decimalIndex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
