package specvalidation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
	"github.com/redhat-et/protobot/validator/internal/testutil"
)

func TestValidateRecordSchema(t *testing.T) {
	reqPath := "spec/requirements/REQ-API-00002.yaml"
	ifacePath := "spec/interfaces/health-api.yaml"

	cases := []struct {
		name         string
		snapshot     layout.Snapshot
		wantCodes    []string
		wantFields   []string
		wantMessages []string
		wantPath     string
	}{
		{
			name:     "valid records",
			snapshot: schemaSnapshot(validRequirementDocument(), validInterfaceDocument()),
		},
		{
			// nolint:fixtures: this case proves that a nil Fields map
			// suppresses missing-field diagnostics even when every record
			// field is empty; a canonical fixture would populate Fields
			// and record content that this edge case must leave empty.
			name: "synthetic requirement and interface with nil fields produce no missing field diagnostics",
			snapshot: layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{ // nolint:fixtures
					{
						Path:   reqPath,
						Record: records.Requirement{}, // nolint:fixtures
						Fields: nil,
					},
				},
				Interfaces: []layout.Document[records.InterfaceRecord]{ // nolint:fixtures
					{
						Path:   ifacePath,
						Record: records.InterfaceRecord{}, // nolint:fixtures
						Fields: nil,
					},
				},
			},
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
			wantPath:   reqPath,
		},
		{
			name:       "empty applies_to lists",
			snapshot:   schemaSnapshot(requirementWithEmptyAppliesToLists(), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"applies_to"},
			wantPath:   reqPath,
		},
		{
			name:     "blank applies_to selectors",
			snapshot: schemaSnapshot(requirementWithBlankAppliesToSelectors(), validInterfaceDocument()),
			wantCodes: []string{
				CodeRequirementMissingField,
				CodeRequirementMissingField,
				CodeRequirementMissingField,
				CodeRequirementMissingField,
			},
			wantFields: []string{
				"applies_to",
				"applies_to.interfaces[0]",
				"applies_to.scopes[0]",
				"applies_to.scopes[1]",
			},
			wantMessages: []string{
				"applies_to must name at least one interface or scope.",
				"applies_to.interfaces[0] must not be blank.",
				"applies_to.scopes[0] must not be blank.",
				"applies_to.scopes[1] must not be blank.",
			},
			wantPath: reqPath,
		},
		{
			name:         "mixed blank applies_to scope selector",
			snapshot:     schemaSnapshot(requirementWithMixedBlankScopeSelectors(), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"applies_to.scopes[1]"},
			wantMessages: []string{"applies_to.scopes[1] must not be blank."},
			wantPath:     reqPath,
		},
		{
			name:         "mixed blank applies_to interface selector",
			snapshot:     schemaSnapshot(requirementWithMixedBlankInterfaceSelectors(), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"applies_to.interfaces[1]"},
			wantMessages: []string{"applies_to.interfaces[1] must not be blank."},
			wantPath:     reqPath,
		},
		{
			name:         "blank relationship target",
			snapshot:     schemaSnapshot(requirementWithBlankRelationshipTarget(""), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"relationships[0].target"},
			wantMessages: []string{"relationships[0].target must not be blank."},
			wantPath:     reqPath,
		},
		{
			name:         "whitespace-only relationship target",
			snapshot:     schemaSnapshot(requirementWithBlankRelationshipTarget("   "), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"relationships[0].target"},
			wantMessages: []string{"relationships[0].target must not be blank."},
			wantPath:     reqPath,
		},
		{
			name:     "blank relationship type is not a missing field",
			snapshot: schemaSnapshot(requirementWithBlankRelationshipType(), validInterfaceDocument()),
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
			wantPath:   reqPath,
		},
		{
			name:       "missing requirement type",
			snapshot:   schemaSnapshot(requirementOmitting("type"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"type"},
			wantPath:   reqPath,
		},
		{
			name:       "missing requirement text",
			snapshot:   schemaSnapshot(requirementOmitting("text"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"text"},
			wantPath:   reqPath,
		},
		{
			name:       "missing applies_to",
			snapshot:   schemaSnapshot(requirementOmitting("applies_to"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"applies_to"},
			wantPath:   reqPath,
		},
		{
			name:       "missing relationships",
			snapshot:   schemaSnapshot(requirementOmitting("relationships"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"relationships"},
			wantPath:   reqPath,
		},
		{
			name:       "missing requirement status",
			snapshot:   schemaSnapshot(requirementOmitting("status"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"status"},
			wantPath:   reqPath,
		},
		{
			name:         "blank requirement id",
			snapshot:     schemaSnapshot(requirementBlankString("id"), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"id"},
			wantMessages: []string{`Required field "id" is missing.`},
			wantPath:     reqPath,
		},
		{
			name:         "blank requirement type",
			snapshot:     schemaSnapshot(requirementBlankString("type"), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"type"},
			wantMessages: []string{`Required field "type" is missing.`},
			wantPath:     reqPath,
		},
		{
			name:         "blank requirement text",
			snapshot:     schemaSnapshot(requirementBlankString("text"), validInterfaceDocument()),
			wantCodes:    []string{CodeRequirementMissingField},
			wantFields:   []string{"text"},
			wantMessages: []string{`Required field "text" is missing.`},
			wantPath:     reqPath,
		},
		{
			name:       "blank requirement status",
			snapshot:   schemaSnapshot(requirementBlankString("status"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementMissingField},
			wantFields: []string{"status"},
			wantPath:   reqPath,
		},
		{
			name:       "missing interface name",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceOmitting("name")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"name"},
			wantPath:   ifacePath,
		},
		{
			name:       "blank interface name",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceBlankString("name")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"name"},
			wantPath:   ifacePath,
		},
		{
			name:       "missing interface kind",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceOmitting("kind")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"kind"},
			wantPath:   ifacePath,
		},
		{
			name:       "blank interface kind",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceBlankString("kind")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"kind"},
			wantPath:   ifacePath,
		},
		{
			name:       "missing interface description",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceOmitting("description")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"description"},
			wantPath:   ifacePath,
		},
		{
			name:       "blank interface description",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceBlankString("description")),
			wantCodes:  []string{CodeInterfaceMissingField},
			wantFields: []string{"description"},
			wantPath:   ifacePath,
		},
		{
			name:       "interface name is a dot-dot segment",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceWithName("..")),
			wantCodes:  []string{CodeInterfaceInvalidName},
			wantFields: []string{"name"},
			wantPath:   ifacePath,
		},
		{
			name:       "interface name has a leading dot",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceWithName(".hidden")),
			wantCodes:  []string{CodeInterfaceInvalidName},
			wantFields: []string{"name"},
			wantPath:   ifacePath,
		},
		{
			name:       "interface name has a trailing dot",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceWithName("service.")),
			wantCodes:  []string{CodeInterfaceInvalidName},
			wantFields: []string{"name"},
			wantPath:   ifacePath,
		},
		{
			name:     "interface name with interior dots is valid",
			snapshot: schemaSnapshot(validRequirementDocument(), interfaceWithName("service..api")),
		},
		{
			name:       "invalid status draft",
			snapshot:   schemaSnapshot(requirementWithStatus("draft"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
			wantPath:   reqPath,
		},
		{
			name:       "invalid status implemented",
			snapshot:   schemaSnapshot(requirementWithStatus("implemented"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
			wantPath:   reqPath,
		},
		{
			name:       "invalid status pending",
			snapshot:   schemaSnapshot(requirementWithStatus("pending"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
			wantPath:   reqPath,
		},
		{
			name:       "status case mismatch",
			snapshot:   schemaSnapshot(requirementWithStatus("Active"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementInvalidStatus},
			wantFields: []string{"status"},
			wantPath:   reqPath,
		},
		{
			name:       "legacy verification field",
			snapshot:   schemaSnapshot(requirementWithExtra("verification", "verification.mode"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"verification"},
			wantPath:   reqPath,
		},
		{
			name:       "legacy provenance field",
			snapshot:   schemaSnapshot(requirementWithExtra("provenance"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"provenance"},
			wantPath:   reqPath,
		},
		{
			name:       "legacy created field",
			snapshot:   schemaSnapshot(requirementWithExtra("created"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"created"},
			wantPath:   reqPath,
		},
		{
			name:       "unknown requirement key",
			snapshot:   schemaSnapshot(requirementWithExtra("owner"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"owner"},
			wantPath:   reqPath,
		},
		{
			name:       "unknown applies_to key",
			snapshot:   schemaSnapshot(requirementWithExtra("applies_to.owner"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"applies_to.owner"},
			wantPath:   reqPath,
		},
		{
			name:       "unknown relationship key",
			snapshot:   schemaSnapshot(requirementWithExtra("relationships[0].rationale"), validInterfaceDocument()),
			wantCodes:  []string{CodeRequirementUnexpectedField},
			wantFields: []string{"relationships[0].rationale"},
			wantPath:   reqPath,
		},
		{
			name:       "legacy interface fields",
			snapshot:   schemaSnapshot(validRequirementDocument(), interfaceWithExtra("id", "type", "created", "status")),
			wantCodes:  []string{CodeInterfaceUnexpectedField, CodeInterfaceUnexpectedField, CodeInterfaceUnexpectedField, CodeInterfaceUnexpectedField},
			wantFields: []string{"created", "id", "status", "type"},
			wantPath:   ifacePath,
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
				if got.Path != tc.wantPath {
					t.Errorf("path=%q, want %q", got.Path, tc.wantPath)
				}
				if got.Field != tc.wantFields[i] {
					t.Errorf("field=%q, want %q", got.Field, tc.wantFields[i])
				}
				if !strings.Contains(got.Message, tc.wantFields[i]) {
					t.Errorf("message %q does not identify field %q", got.Message, tc.wantFields[i])
				}
				if i < len(tc.wantMessages) && got.Message != tc.wantMessages[i] {
					t.Errorf("message=%q, want %q", got.Message, tc.wantMessages[i])
				}
			}
		})
	}
}

func TestValidateRecordSchemaEmptyAppliesToMessage(t *testing.T) {
	cases := []struct {
		name     string
		document layout.Document[records.Requirement]
	}{
		{"empty applies_to mapping", requirementWithEmptyAppliesTo()},
		{"empty applies_to lists", requirementWithEmptyAppliesToLists()},
		{"blank applies_to selectors", requirementWithBlankAppliesToSelectors()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := schemaDiagnostics(Validate(schemaSnapshot(tc.document, validInterfaceDocument())))
			var got Diagnostic
			found := false
			for _, diagnostic := range diagnostics {
				if diagnostic.Field == "applies_to" {
					got = diagnostic
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("diagnostics=%v, want applies_to", diagnostics)
			}
			wantMsg := "applies_to must name at least one interface or scope."
			if got.Message != wantMsg {
				t.Errorf("message=%q, want %q", got.Message, wantMsg)
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

func TestValidateRecordSchemaRejectsBlankIndexedFieldsAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/interfaces/health-api.yaml", `name: health-api
kind: http
description: Health check HTTP API.
`)
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  interfaces:
    - health-api
    - " "
  scopes:
    - api
    - ""
relationships:
  - type: depends-on
    target: ""
status: active
`)

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}

	diagnostics := Validate(snapshot)
	wantFields := []string{
		"applies_to.interfaces[1]",
		"applies_to.scopes[1]",
		"relationships[0].target",
	}
	wantMessages := []string{
		"applies_to.interfaces[1] must not be blank.",
		"applies_to.scopes[1] must not be blank.",
		"relationships[0].target must not be blank.",
	}
	if len(diagnostics) != len(wantFields) {
		t.Fatalf("diagnostics=%v, want fields %v", diagnostics, wantFields)
	}
	for i, field := range wantFields {
		got := diagnostics[i]
		if got.Code != CodeRequirementMissingField {
			t.Errorf("code=%q, want %q", got.Code, CodeRequirementMissingField)
		}
		if got.Field != field {
			t.Errorf("field=%q, want %q", got.Field, field)
		}
		if got.Path != "spec/requirements/REQ-API-00002.yaml" {
			t.Errorf("path=%q, want requirement path", got.Path)
		}
		if got.Message != wantMessages[i] {
			t.Errorf("message=%q, want %q", got.Message, wantMessages[i])
		}
	}
}

func TestValidateRecordSchemaRelationshipTargetBlankVsOmitted(t *testing.T) {
	cases := []struct {
		name        string
		requirement string
		wantMessage string
	}{
		{
			name: "blank relationship target",
			requirement: `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships:
  - type: depends-on
    target: ""
status: active
`,
			wantMessage: "relationships[0].target must not be blank.",
		},
		{
			name: "whitespace-only relationship target",
			requirement: `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships:
  - type: depends-on
    target: "   "
status: active
`,
			wantMessage: "relationships[0].target must not be blank.",
		},
		{
			name: "omitted relationship target",
			requirement: `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
applies_to:
  scopes:
    - api
relationships:
  - type: depends-on
status: active
`,
			wantMessage: `Required field "relationships[0].target" is missing.`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := t.TempDir()
			writeFile(t, target, "spec/interfaces/health-api.yaml", `name: health-api
kind: http
description: Health check HTTP API.
`)
			writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", tc.requirement)

			snapshot, layoutDiagnostics := layout.Read(target)
			if len(layoutDiagnostics) != 0 {
				t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
			}

			diagnostics := Validate(snapshot)
			if !hasSchemaDiagnostic(diagnostics, CodeRequirementMissingField, "spec/requirements/REQ-API-00002.yaml", "relationships[0].target") {
				t.Fatalf("missing relationships[0].target diagnostic: %v", diagnostics)
			}
			for _, diagnostic := range schemaDiagnostics(diagnostics) {
				if diagnostic.Field != "relationships[0].target" {
					continue
				}
				if diagnostic.Message != tc.wantMessage {
					t.Errorf("message=%q, want %q", diagnostic.Message, tc.wantMessage)
				}
			}
		})
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
	return testutil.ValidRequirementDocument(
		testutil.WithAppliesTo(records.Applicability{
			Interfaces: []string{"health-api"},
			Scopes:     []string{"api"},
		}),
		testutil.WithRelationships(records.Relationship{
			Type:   records.RelRelatedTo,
			Target: "REQ-API-00001",
		}),
	)
}

func validInterfaceDocument() layout.Document[records.InterfaceRecord] {
	return testutil.ValidInterfaceDocument()
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

func requirementWithMixedBlankScopeSelectors() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{
		Interfaces: []string{"health-api"},
		Scopes:     []string{"api", ""},
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

func requirementWithMixedBlankInterfaceSelectors() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.AppliesTo = records.Applicability{
		Interfaces: []string{"health-api", "  "},
		Scopes:     []string{"api"},
	}
	document.Fields = omitField(document.Fields, "applies_to")
	document.Fields["applies_to"] = true
	document.Fields["applies_to.interfaces"] = true
	document.Fields["applies_to.interfaces[0]"] = true
	document.Fields["applies_to.interfaces[1]"] = true
	document.Fields["applies_to.scopes"] = true
	document.Fields["applies_to.scopes[0]"] = true
	return document
}

func requirementWithBlankRelationshipTarget(target string) layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.Relationships = []records.Relationship{
		{Type: records.RelDependsOn, Target: target},
	}
	return document
}

func requirementWithBlankRelationshipType() layout.Document[records.Requirement] {
	document := validRequirementDocument()
	document.Record.Relationships = []records.Relationship{
		{Type: "", Target: document.Record.ID},
	}
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

func interfaceWithName(name string) layout.Document[records.InterfaceRecord] {
	document := validInterfaceDocument()
	document.Record.Name = name
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
		case CodeRequirementMissingField, CodeRequirementInvalidStatus, CodeRequirementUnexpectedField, CodeInterfaceMissingField, CodeInterfaceUnexpectedField, CodeInterfaceInvalidName:
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
