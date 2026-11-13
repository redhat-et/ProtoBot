package specvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestValidateRelationshipTypes(t *testing.T) {
	const path = "spec/requirements/REQ-API-00002.yaml"
	cases := []struct {
		name          string
		relationships []records.Relationship
		wantTypes     []string
		wantFields    []string
	}{
		{
			name: "allowed types",
			relationships: []records.Relationship{
				{Type: records.RelDependsOn, Target: "REQ-API-00001"},
				{Type: records.RelSupersedes, Target: "REQ-API-00000"},
				{Type: records.RelRelatedTo, Target: "REQ-API-00003"},
			},
		},
		{
			name:          "empty list",
			relationships: []records.Relationship{},
		},
		{
			name:          "omitted list",
			relationships: nil,
		},
		{
			name: "legacy conflicts-with",
			relationships: []records.Relationship{
				{Type: "conflicts-with", Target: "REQ-API-00001"},
			},
			wantTypes:  []string{"conflicts-with"},
			wantFields: []string{"relationships[0].type"},
		},
		{
			name: "undeclared type",
			relationships: []records.Relationship{
				{Type: "blocks", Target: "REQ-API-00001"},
			},
			wantTypes:  []string{"blocks"},
			wantFields: []string{"relationships[0].type"},
		},
		{
			name: "empty type",
			relationships: []records.Relationship{
				{Type: "", Target: "REQ-API-00001"},
			},
			wantTypes:  []string{""},
			wantFields: []string{"relationships[0].type"},
		},
		{
			name: "mixed valid and invalid",
			relationships: []records.Relationship{
				{Type: records.RelDependsOn, Target: "REQ-API-00001"},
				{Type: "conflicts-with", Target: "REQ-API-00004"},
				{Type: records.RelRelatedTo, Target: "REQ-API-00003"},
				{Type: "derived-from", Target: "REQ-API-00005"},
			},
			wantTypes:  []string{"conflicts-with", "derived-from"},
			wantFields: []string{"relationships[1].type", "relationships[3].type"},
		},
		{
			name: "case mismatch is rejected",
			relationships: []records.Relationship{
				{Type: "Depends-On", Target: "REQ-API-00001"},
			},
			wantTypes:  []string{"Depends-On"},
			wantFields: []string{"relationships[0].type"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := Validate(requirementSnapshot(path, "REQ-API-00002", tc.relationships))
			if len(tc.wantTypes) == 0 {
				if len(diagnostics) != 0 {
					t.Fatalf("diagnostics=%v, want none", diagnostics)
				}
				return
			}
			if len(diagnostics) != len(tc.wantTypes) {
				t.Fatalf("diagnostics=%v, want %d", diagnostics, len(tc.wantTypes))
			}
			for i, wantType := range tc.wantTypes {
				got := diagnostics[i]
				if got.Code != CodeInvalidType {
					t.Errorf("code=%q, want %q", got.Code, CodeInvalidType)
				}
				if got.Path != path {
					t.Errorf("path=%q, want %q", got.Path, path)
				}
				if !identifiesType(got.Message, wantType) {
					t.Errorf("message %q does not identify type %q", got.Message, wantType)
				}
				if got.Field != tc.wantFields[i] {
					t.Errorf("field=%q, want %q", got.Field, tc.wantFields[i])
				}
			}
		})
	}
}

func TestValidateRelationshipTypesReportsEveryInvalidEntry(t *testing.T) {
	first := "spec/requirements/REQ-API-00001.yaml"
	second := "spec/requirements/REQ-API-00002.yaml"
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			requirementDocument(second, "REQ-API-00002", []records.Relationship{
				{Type: "blocks", Target: "REQ-API-00003"},
			}),
			requirementDocument(first, "REQ-API-00001", []records.Relationship{
				{Type: "conflicts-with", Target: "REQ-API-00002"},
				{Type: records.RelRelatedTo, Target: "REQ-API-00004"},
			}),
		},
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2", diagnostics)
	}
	if diagnostics[0].Path != first || !identifiesType(diagnostics[0].Message, "conflicts-with") {
		t.Fatalf("first diagnostic=%+v", diagnostics[0])
	}
	if diagnostics[1].Path != second || !identifiesType(diagnostics[1].Message, "blocks") {
		t.Fatalf("second diagnostic=%+v", diagnostics[1])
	}
}

func TestValidateRelationshipTypesIgnoresInterfaces(t *testing.T) {
	snapshot := layout.Snapshot{
		Interfaces: []layout.Document[records.InterfaceRecord]{{
			Path:   "spec/interfaces/health-api.yaml",
			Record: records.InterfaceRecord{Name: "health-api", Kind: "http"},
		}},
		Requirements: []layout.Document[records.Requirement]{
			requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", nil),
		},
	}
	if diagnostics := Validate(snapshot); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none", diagnostics)
	}
}

func TestValidateRelationshipTypesAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: related-to
    target: REQ-API-00001
  - type: conflicts-with
    target: REQ-API-00003
status: active
`)

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("requirements=%d, want 1", len(snapshot.Requirements))
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeInvalidType {
		t.Errorf("code=%q, want %q", got.Code, CodeInvalidType)
	}
	if got.Path != "spec/requirements/REQ-API-00002.yaml" {
		t.Errorf("path=%q", got.Path)
	}
	if got.Field != "relationships[1].type" {
		t.Errorf("field=%q, want relationships[1].type", got.Field)
	}
	if !identifiesType(got.Message, "conflicts-with") {
		t.Errorf("message %q omitted conflicts-with", got.Message)
	}
}

func TestValidateRelationshipTypesIsDeterministic(t *testing.T) {
	snapshot := requirementSnapshot("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
		{Type: "conflicts-with", Target: "REQ-API-00001"},
		{Type: "blocks", Target: "REQ-API-00003"},
	})
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

func requirementSnapshot(path, id string, relationships []records.Relationship) layout.Snapshot {
	return layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			requirementDocument(path, id, relationships),
		},
	}
}

func requirementDocument(path, id string, relationships []records.Relationship) layout.Document[records.Requirement] {
	return layout.Document[records.Requirement]{
		Path: path,
		Record: records.Requirement{
			ID:            id,
			Relationships: relationships,
		},
	}
}

func identifiesType(message, relationshipType string) bool {
	if relationshipType == "" {
		return strings.Contains(message, `""`)
	}
	return strings.Contains(message, relationshipType)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
