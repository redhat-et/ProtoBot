package specvalidation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestValidateRecordSchema(t *testing.T) {
	reqPath := "spec/requirements/REQ-API-00002.yaml"
	ifacePath := "spec/interfaces/health-api.yaml"

	cases := []struct {
		name       string
		snapshot   layout.Snapshot
		wantCodes  []string
		wantFields []string
	}{
		{
			name:     "valid records",
			snapshot: schemaSnapshot(validRequirementDocument(), validInterfaceDocument()),
		},
		{
			name:     "retired status is allowed",
			snapshot: schemaSnapshot(requirementWithStatus(records.StatusRetired), validInterfaceDocument()),
		},
		{
			name:     "empty relationships list",
			snapshot: schemaSnapshot(requirementWithRelationships(nil), validInterfaceDocument()),
		},
		{
			name:       "empty applies_to mapping",
			snapshot:   schemaSnapshot(requirementWithEmptyAppliesTo(), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"applies_to"},
		},
		{
			name:       "empty applies_to lists",
			snapshot:   schemaSnapshot(requirementWithEmptyAppliesToLists(), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"applies_to"},
		},
		{
			name:       "blank applies_to selectors",
			snapshot:   schemaSnapshot(requirementWithBlankAppliesToSelectors(), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"applies_to"},
		},
		{
			name:     "applies_to with only interfaces",
			snapshot: schemaSnapshot(requirementWithOnlyInterfaces(), validInterfaceDocument()),
		},
		{
			name:     "applies_to with only scopes",
			snapshot: schemaSnapshot(requirementWithOnlyScopes(), validInterfaceDocument()),
		},
		{
			name:       "missing requirement id",
			snapshot:   schemaSnapshot(requirementOmitting("id"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"id"},
		},
		{
			name:       "missing requirement type",
			snapshot:   schemaSnapshot(requirementOmitting("type"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"type"},
		},
		{
			name:       "missing requirement text",
			snapshot:   schemaSnapshot(requirementOmitting("text"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"text"},
		},
		{
			name:       "missing applies_to",
			snapshot:   schemaSnapshot(requirementOmitting("applies_to"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"applies_to"},
		},
		{
			name:       "missing relationships",
			snapshot:   schemaSnapshot(requirementOmitting("relationships"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"relationships"},
		},
		{
			name:       "missing requirement status",
			snapshot:   schemaSnapshot(requirementOmitting("status"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"status"},
		},
		{
			name:       "blank requirement id",
			snapshot:   schemaSnapshot(requirementBlankString("id"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"id"},
		},
		{
			name:       "missing interface name",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceOmitting("name")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"name"},
		},
		{
			name:       "missing interface kind",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceOmitting("kind")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"kind"},
		},
		{
			name:       "missing interface description",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceOmitting("description")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"description"},
		},
		{
			name:       "blank interface description",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceBlankString("description")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"description"},
		},
		{
			name:       "invalid status draft",
			snapshot:   schemaSnapshot(requirementWithStatus("draft"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
		},
		{
			name:       "invalid status implemented",
			snapshot:   schemaSnapshot(requirementWithStatus("implemented"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
		},
		{
			name:       "invalid status pending",
			snapshot:   schemaSnapshot(requirementWithStatus("pending"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
		},
		{
			name:       "status case mismatch",
			snapshot:   schemaSnapshot(requirementWithStatus("Active"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
		},
		{
			name:       "legacy verification field",
			snapshot:   schemaSnapshot(requirementWithExtra("verification", "verification.mode"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"verification"},
		},
		{
			name:       "legacy provenance field",
			snapshot:   schemaSnapshot(requirementWithExtra("provenance"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"provenance"},
		},
		{
			name:       "legacy created field",
			snapshot:   schemaSnapshot(requirementWithExtra("created"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"created"},
		},
		{
			name:       "unknown requirement key",
			snapshot:   schemaSnapshot(requirementWithExtra("owner"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"owner"},
		},
		{
			name:       "unknown applies_to key",
			snapshot:   schemaSnapshot(requirementWithExtra("applies_to.owner"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"applies_to.owner"},
		},
		{
			name:       "unknown relationship key",
			snapshot:   schemaSnapshot(requirementWithExtra("relationships[0].rationale"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"relationships[0].rationale"},
		},
		{
			name:       "legacy interface fields",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceWithExtra("id", "type", "created", "status")),
			wantCodes:  []string{CodeInterfaceUnexpectedField, CodeInterfaceUnexpectedField, CodeInterfaceUnexpectedField, CodeInterfaceUnexpectedField},
			wantFields: []string{"created", "id", "status", "type"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := schemaDiagnostics(Validate(tc.snapshot))
			if len(tc.wantCodes) == 0 {
				if len(diagnostics) != 0 {
					t.Fatalf("diagnostics=%v, want none", diagnostics)
				}
				return
			}
			if len(diagnostics) != len(tc.wantCodes) {
				t.Fatalf("diagnostics=%v, want %d", diagnostics, len(tc.wantCodes))
			}
			for i, wantCode := range tc.wantCodes {
				got := diagnostics[i]
				if got.Code != wantCode {
					t.Errorf("code=%q, want %q", got.Code, wantCode)
				}
				if got.Path != reqPath && got.Path != ifacePath {
					t.Errorf("path=%q, want requirement or interface path", got.Path)
				}
				if got.Field != tc.wantFields[i] {
					t.Errorf("field=%q, want %q", got.Field, tc.wantFields[i])
				}
				if !strings.Contains(got.Message, tc.wantFields[i]) {
					t.Errorf("message %q does not identify field %q", got.Message, tc.wantFields[i])
				}
			}
		})
	}
}

func TestValidateRecordSchemaReportsEveryMissingField(t *testing.T) {
	document := validRequirementDocument()
	document.Record.ID = ""
	document.Record.Type = ""
	document.Record.Text = ""
	document.Record.AppliesTo = records.Applicability{}
	document.Record.Relationships = nil
	document.Record.Status = ""
	document.Fields = map[string]bool{}

	diagnostics := schemaDiagnostics(Validate(layout.Snapshot{Requirements: []layout.Document[records.Requirement]{document}}))
	want := []string{"applies_to", "id", "relationships", "status", "text", "type"}
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics=%v, want fields %v", diagnostics, want)
	}
	for i, field := range want {
		if diagnostics[i].Code != CodeRequirementMissingField {
			t.Errorf("code=%q, want %q", diagnostics[i].Code, CodeRequirementMissingField)
		}
		if diagnostics[i].Field != field {
			t.Errorf("field=%q, want %q", diagnostics[i].Field, field)
		}
		if diagnostics[i].Path != document.Path {
			t.Errorf("path=%q, want %q", diagnostics[i].Path, document.Path)
		}
	}
}

func TestValidateRecordSchemaAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships: []
status: draft
verification:
  mode: isolated-interface
created: "2026-01-01T00:00:00Z"
`)
	writeFile(t, target, "spec/interfaces/health-api.yaml", "name: health-api\n")

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}

	diagnostics := Validate(snapshot)
	if !hasSchemaDiagnostic(diagnostics, CodeRequirementInvalidStatus, "spec/requirements/REQ-API-00002.yaml", "status") {
		t.Fatalf("missing invalid status diagnostic: %v", diagnostics)
	}
	if !hasSchemaDiagnostic(diagnostics, CodeRequirementUnexpectedField, "spec/requirements/REQ-API-00002.yaml", "verification") {
		t.Fatalf("missing verification diagnostic: %v", diagnostics)
	}
	if !hasSchemaDiagnostic(diagnostics, CodeRequirementUnexpectedField, "spec/requirements/REQ-API-00002.yaml", "created") {
		t.Fatalf("missing created diagnostic: %v", diagnostics)
	}
	if hasSchemaDiagnostic(diagnostics, CodeRequirementUnexpectedField, "spec/requirements/REQ-API-00002.yaml", "verification.mode") {
		t.Fatalf("nested legacy field was reported separately: %v", diagnostics)
	}
	if !hasSchemaDiagnostic(diagnostics, CodeInterfaceMissingField, "spec/interfaces/health-api.yaml", "kind") {
		t.Fatalf("missing interface kind diagnostic: %v", diagnostics)
	}
	if !hasSchemaDiagnostic(diagnostics, CodeInterfaceMissingField, "spec/interfaces/health-api.yaml", "description") {
		t.Fatalf("missing interface description diagnostic: %v", diagnostics)
	}
}

func TestValidateRecordSchemaIsDeterministic(t *testing.T) {
	snapshot := schemaSnapshot(requirementWithExtra("verification", "provenance", "created"), interfaceOmitting("kind"))
	first := Validate(snapshot)
	second := Validate(snapshot)
	if len(first) != len(second) {
		t.Fatalf("repeated validation changed diagnostic count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("repeated validation changed diagnostics: %+v vs %+v", first, second)
		}
	}
}

func schemaSnapshot(requirement layout.Document[records.Requirement], iface layout.Document[records.InterfaceRecord]) layout.Snapshot {
	return layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{requirement},
		Interfaces:   []layout.Document[records.InterfaceRecord]{iface},
	}
}

func validRequirementDocument() layout.Document[records.Requirement] {
	return layout.Document[records.Requirement]{
		Path: "spec/requirements/REQ-API-00002.yaml",
		Record: records.Requirement{
			ID:   "REQ-API-00002",
			Type: records.TypeEventDriven,
			Text: "When a client sends GET /health, the service shall return HTTP 200.",
			AppliesTo: records.Applicability{
				Interfaces: []string{"health-api"},
				Scopes:     []string{"api"},
			},
			Relationships: []records.Relationship{
				{Type: records.RelRelatedTo, Target: "REQ-API-00001"},
			},
			Status: records.StatusActive,
		},
		Fields: map[string]bool{
			"id":                       true,
			"type":                     true,
			"text":                     true,
			"applies_to":               true,
			"applies_to.interfaces":    true,
			"applies_to.interfaces[0]": true,
			"applies_to.scopes":        true,
			"applies_to.scopes[0]":     true,
			"relationships":            true,
			"relationships[0]":         true,
			"relationships[0].type":    true,
			"relationships[0].target":  true,
			"status":                   true,
		},
	}
}

func validInterfaceDocument() layout.Document[records.InterfaceRecord] {
	return layout.Document[records.InterfaceRecord]{
		Path: "spec/interfaces/health-api.yaml",
		Record: records.InterfaceRecord{
			Name:        "health-api",
			Kind:        "http",
			Description: "Health check HTTP API used by unauthenticated clients.",
		},
		Fields: map[string]bool{
			"name":        true,
			"kind":        true,
			"description": true,
		},
	}
}

func requirementOmitting(field string) layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Fields = omitField(document.Fields, field)
	switch field {
	case "id":
		document.Record.ID = ""
	case "type":
		document.Record.Type = ""
	case "text":
		document.Record.Text = ""
	case "applies_to":
		document.Record.AppliesTo = records.Applicability{}
	case "relationships":
		document.Record.Relationships = nil
	case "status":
		document.Record.Status = ""
	}
	return document
}

func requirementBlankString(field string) layout.Document[records.Requirement] {
	document := validRequirementDocument()
	switch field {
	case "id":
		document.Record.ID = "   "
	case "type":
		document.Record.Type = "   "
	case "text":
		document.Record.Text = "   "
	case "status":
		document.Record.Status = "   "
	}
	return document
}

func requirementWithStatus(status string) layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.Status = status
	return document
}

func requirementWithRelationships(relationships []records.Relationship) layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.Relationships = relationships
	document.Fields = omitField(document.Fields, "relationships")
	document.Fields["relationships"] = true
	for i := range relationships {
		document.Fields[fmtRelationship(i, "")] = true
		document.Fields[fmtRelationship(i, "type")] = true
		document.Fields[fmtRelationship(i, "target")] = true
	}
	return document
}

func requirementWithEmptyAppliesTo() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{}
	document.Fields = omitField(document.Fields, "applies_to")
	document.Fields["applies_to"] = true
	return document
}

func requirementWithEmptyAppliesToLists() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{
		Interfaces: []string{},
		Scopes:     []string{},
	}
	document.Fields = omitField(document.Fields, "applies_to")
	document.Fields["applies_to"] = true
	document.Fields["applies_to.interfaces"] = true
	document.Fields["applies_to.scopes"] = true
	return document
}

func requirementWithBlankAppliesToSelectors() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{
		Interfaces: []string{"   "},
		Scopes:     []string{"\t", ""},
	}
	document.Fields = omitField(document.Fields, "applies_to")
	document.Fields["applies_to"] = true
	document.Fields["applies_to.interfaces"] = true
	document.Fields["applies_to.interfaces[0]"] = true
	document.Fields["applies_to.scopes"] = true
	document.Fields["applies_to.scopes[0]"] = true
	document.Fields["applies_to.scopes[1]"] = true
	return document
}

func requirementWithOnlyInterfaces() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{
		Interfaces: []string{"health-api"},
	}
	document.Fields = omitField(document.Fields, "applies_to")
	document.Fields["applies_to"] = true
	document.Fields["applies_to.interfaces"] = true
	document.Fields["applies_to.interfaces[0]"] = true
	return document
}

func requirementWithOnlyScopes() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{
		Scopes: []string{"api"},
	}
	document.Fields = omitField(document.Fields, "applies_to")
	document.Fields["applies_to"] = true
	document.Fields["applies_to.scopes"] = true
	document.Fields["applies_to.scopes[0]"] = true
	return document
}

func requirementWithExtra(fields ...string) layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Fields = copyFields(document.Fields)
	for _, field := range fields {
		document.Fields[field] = true
	}
	return document
}

func interfaceOmitting(field string) layout.Document[records.InterfaceRecord] {
	document := validInterfaceDocument()
	document.Fields = omitField(document.Fields, field)
	switch field {
	case "name":
		document.Record.Name = ""
	case "kind":
		document.Record.Kind = ""
	case "description":
		document.Record.Description = ""
	}
	return document
}

func interfaceBlankString(field string) layout.Document[records.InterfaceRecord] {
	document := validInterfaceDocument()
	switch field {
	case "name":
		document.Record.Name = "   "
	case "kind":
		document.Record.Kind = "   "
	case "description":
		document.Record.Description = "   "
	}
	return document
}

func interfaceWithExtra(fields ...string) layout.Document[records.InterfaceRecord] {
	document := validInterfaceDocument()
	document.Fields = copyFields(document.Fields)
	for _, field := range fields {
		document.Fields[field] = true
	}
	return document
}

func omitField(fields map[string]bool, field string) map[string]bool {
	out := copyFields(fields)
	delete(out, field)
	dotPrefix := field + "."
	indexPrefix := field + "["
	for key := range out {
		if strings.HasPrefix(key, dotPrefix) || strings.HasPrefix(key, indexPrefix) {
			delete(out, key)
		}
	}
	return out
}

func copyFields(fields map[string]bool) map[string]bool {
	out := make(map[string]bool, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func schemaDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	var filtered []Diagnostic
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case CodeRequirementMissingField, CodeRequirementInvalidStatus, CodeRequirementUnexpectedField, CodeInterfaceMissingField, CodeInterfaceUnexpectedField:
			filtered = append(filtered, diagnostic)
		}
	}
	return filtered
}

func hasSchemaDiagnostic(diagnostics []Diagnostic, code, path, field string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Path == path && diagnostic.Field == field {
			return true
		}
	}
	return false
}

func fmtRelationship(index int, child string) string {
	if child == "" {
		return fmt.Sprintf("relationships[%d]", index)
	}
	return fmt.Sprintf("relationships[%d].%s", index, child)
}
