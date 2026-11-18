package testutil

import (
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestValidRequirementDocumentDefaults(t *testing.T) {
	document := ValidRequirementDocument()
	if document.Path != "spec/requirements/REQ-API-00002.yaml" {
		t.Errorf("path=%q", document.Path)
	}
	got := document.Record
	if got.ID != defaultRequirementID {
		t.Errorf("id=%q, want %q", got.ID, defaultRequirementID)
	}
	if got.Type != records.TypeEventDriven {
		t.Errorf("type=%q, want %q", got.Type, records.TypeEventDriven)
	}
	if got.Text != defaultRequirementText {
		t.Errorf("text=%q", got.Text)
	}
	if got.Status != records.StatusActive {
		t.Errorf("status=%q, want %q", got.Status, records.StatusActive)
	}
	if len(got.AppliesTo.Scopes) != 1 || got.AppliesTo.Scopes[0] != defaultScope {
		t.Errorf("applies_to.scopes=%v, want [%s]", got.AppliesTo.Scopes, defaultScope)
	}
	if got.Relationships == nil {
		t.Fatal("relationships is nil, want empty list")
	}
	if document.Fields == nil {
		t.Fatal("Fields is nil, want presence map")
	}
	for _, field := range []string{
		"id",
		"type",
		"text",
		"applies_to",
		"applies_to.interfaces",
		"applies_to.scopes",
		"relationships",
		"status",
	} {
		if !document.Fields[field] {
			t.Errorf("Fields[%q] missing", field)
		}
	}

	ifaceDoc := ValidInterfaceDocument()
	if ifaceDoc.Fields == nil {
		t.Fatal("ifaceDoc.Fields is nil, want presence map")
	}
	for _, field := range []string{"name", "kind", "description"} {
		if !ifaceDoc.Fields[field] {
			t.Errorf("ifaceDoc.Fields[%q] missing", field)
		}
	}
}

func TestValidRequirementDocumentOptions(t *testing.T) {
	rels := []records.Relationship{
		{Type: records.RelDependsOn, Target: "REQ-API-00001"},
	}
	document := ValidRequirementDocument(
		WithID("REQ-CLI-00001"),
		WithStatus(records.StatusRetired),
		WithType(records.TypeUbiquitous),
		WithText("The service shall exist."),
		WithAppliesTo(records.Applicability{Interfaces: []string{"health-api"}}),
		WithRelationships(rels...),
	)
	if document.Path != "spec/requirements/REQ-CLI-00001.yaml" {
		t.Errorf("path=%q, want canonical path for new id", document.Path)
	}
	got := document.Record
	if got.ID != "REQ-CLI-00001" || got.Status != records.StatusRetired {
		t.Fatalf("options failed to mutate document: %+v", got)
	}
	if got.Type != records.TypeUbiquitous || got.Text != "The service shall exist." {
		t.Errorf("type/text not mutated: %+v", got)
	}
	if len(got.AppliesTo.Interfaces) != 1 || got.AppliesTo.Interfaces[0] != "health-api" {
		t.Errorf("applies_to=%+v", got.AppliesTo)
	}
	if len(got.Relationships) != 1 || got.Relationships[0] != rels[0] {
		t.Errorf("relationships=%v", got.Relationships)
	}
	if !document.Fields["relationships[0].target"] {
		t.Errorf("Fields omitted relationship children: %v", document.Fields)
	}
	if !document.Fields["applies_to.interfaces[0]"] {
		t.Errorf("Fields omitted applies_to.interfaces[0]: %v", document.Fields)
	}
}

func TestWithPathOverridesCanonicalIDPath(t *testing.T) {
	document := ValidRequirementDocument(
		WithID("REQ-API-00001"),
		WithPath("spec/requirements/copy.yaml"),
	)
	if document.Path != "spec/requirements/copy.yaml" {
		t.Errorf("path=%q, want explicit path", document.Path)
	}
	if document.Record.ID != "REQ-API-00001" {
		t.Errorf("id=%q", document.Record.ID)
	}
}

func TestWithoutFieldsClearsPresenceMap(t *testing.T) {
	document := ValidRequirementDocument(WithoutFields())
	if document.Fields != nil {
		t.Errorf("Fields=%v, want nil", document.Fields)
	}
}

func TestValidInterfaceDocumentDefaultsAndOptions(t *testing.T) {
	document := ValidInterfaceDocument()
	if document.Path != "spec/interfaces/health-api.yaml" {
		t.Errorf("path=%q", document.Path)
	}
	if document.Record.Name != defaultInterfaceName || document.Record.Kind != defaultInterfaceKind {
		t.Errorf("record=%+v", document.Record)
	}
	if document.Fields == nil || !document.Fields["name"] || !document.Fields["kind"] || !document.Fields["description"] {
		t.Errorf("Fields=%v", document.Fields)
	}

	custom := ValidInterfaceDocument(
		WithName("cli"),
		WithKind("cli"),
		WithDescription("Validator command line."),
	)
	if custom.Path != "spec/interfaces/cli.yaml" {
		t.Errorf("path=%q, want canonical path for new name", custom.Path)
	}
	if custom.Record.Name != "cli" || custom.Record.Kind != "cli" || custom.Record.Description != "Validator command line." {
		t.Errorf("record=%+v", custom.Record)
	}

	mismatched := ValidInterfaceDocument(
		WithName("health-api"),
		WithPath("spec/interfaces/foo.yaml"),
	)
	if mismatched.Path != "spec/interfaces/foo.yaml" {
		t.Errorf("path=%q, want explicit path", mismatched.Path)
	}
	if mismatched.Record.Name != "health-api" {
		t.Errorf("name=%q", mismatched.Record.Name)
	}
}

func TestValidRequirementYAMLContainsRequiredKeys(t *testing.T) {
	yamlText := ValidRequirementYAML()
	for _, key := range []string{"id:", "type:", "text:", "applies_to:", "relationships:", "status:"} {
		if !strings.Contains(yamlText, key) {
			t.Errorf("YAML omitted %s:\n%s", key, yamlText)
		}
	}
	custom := ValidRequirementYAML(WithID("REQ-CLI-00001"), WithStatus(records.StatusRetired))
	if !strings.Contains(custom, "REQ-CLI-00001") {
		t.Errorf("YAML omitted custom id:\n%s", custom)
	}
	if !strings.Contains(custom, records.StatusRetired) {
		t.Errorf("YAML omitted custom status:\n%s", custom)
	}
}

func TestValidInterfaceYAMLContainsRequiredKeys(t *testing.T) {
	yamlText := ValidInterfaceYAML()
	for _, key := range []string{"name:", "kind:", "description:"} {
		if !strings.Contains(yamlText, key) {
			t.Errorf("YAML omitted %s:\n%s", key, yamlText)
		}
	}
}

func TestValidRequirementDocumentDoesNotAliasSlices(t *testing.T) {
	applies := records.Applicability{Scopes: []string{"api"}}
	rels := []records.Relationship{{Type: records.RelRelatedTo, Target: "REQ-API-00001"}}
	document := ValidRequirementDocument(WithAppliesTo(applies), WithRelationships(rels...))
	applies.Scopes[0] = "mutated"
	rels[0].Target = "REQ-MUTATED-00001"
	if document.Record.AppliesTo.Scopes[0] == "mutated" {
		t.Fatal("AppliesTo aliased caller slice")
	}
	if document.Record.Relationships[0].Target == "REQ-MUTATED-00001" {
		t.Fatal("Relationships aliased caller slice")
	}
}

func TestWithRelationshipsZeroArgumentsYieldsEmptySlice(t *testing.T) {
	document := ValidRequirementDocument(WithRelationships())
	if document.Record.Relationships == nil {
		t.Fatal("relationships is nil, want empty non-nil slice")
	}
	if len(document.Record.Relationships) != 0 {
		t.Fatalf("relationships len=%d, want 0", len(document.Record.Relationships))
	}
}

func TestWithIDOnInterfaceDocumentDoesNotRewritePath(t *testing.T) {
	document := ValidInterfaceDocument(WithID("REQ-API-00001"))
	if document.Path != "spec/interfaces/health-api.yaml" {
		t.Errorf("path=%q, want interface path preserved", document.Path)
	}
}
