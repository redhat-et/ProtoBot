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
			diagnostics := Validate(requirementSnapshot(path, "REQ-API-00002", tc.relationships, relationshipTargets(tc.relationships)...))
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
			requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", nil),
			requirementDocument("spec/requirements/REQ-API-00004.yaml", "REQ-API-00004", nil),
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
	writeFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
status: active
`)
	writeFile(t, target, "spec/requirements/REQ-API-00003.yaml", `id: REQ-API-00003
type: ubiquitous
text: The service shall log health checks.
status: active
`)
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
	if len(snapshot.Requirements) != 3 {
		t.Fatalf("requirements=%d, want 3", len(snapshot.Requirements))
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
	}, "REQ-API-00001", "REQ-API-00003")
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

func TestValidateRelationshipAcyclicity(t *testing.T) {
	cases := []struct {
		name         string
		snapshot     layout.Snapshot
		wantCount    int
		wantCode     string
		wantRelation string
		wantIDs      []string
	}{
		{
			name: "acyclic depends-on chain",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00003"},
				}),
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", nil),
			),
		},
		{
			name: "diamond depends-on dag",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00002"},
					{Type: records.RelDependsOn, Target: "REQ-API-00003"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00004"},
				}),
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00004"},
				}),
				requirementDocument("spec/requirements/REQ-API-00004.yaml", "REQ-API-00004", nil),
			),
		},
		{
			name: "acyclic supersedes chain",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}),
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil),
			),
		},
		{
			name: "two-node depends-on cycle",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00001"},
				}),
			),
			wantCount:    1,
			wantCode:     CodeCycle,
			wantRelation: records.RelDependsOn,
			wantIDs:      []string{"REQ-API-00001", "REQ-API-00002"},
		},
		{
			name: "multi-node depends-on cycle",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00003"},
				}),
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00001"},
				}),
			),
			wantCount:    1,
			wantCode:     CodeCycle,
			wantRelation: records.RelDependsOn,
			wantIDs:      []string{"REQ-API-00001", "REQ-API-00002", "REQ-API-00003"},
		},
		{
			name: "self-referential depends-on loop",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00001"},
				}),
			),
			wantCount:    1,
			wantCode:     CodeCycle,
			wantRelation: records.RelDependsOn,
			wantIDs:      []string{"REQ-API-00001"},
		},
		{
			name: "two-node supersedes cycle",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}),
			),
			wantCount:    1,
			wantCode:     CodeCycle,
			wantRelation: records.RelSupersedes,
			wantIDs:      []string{"REQ-API-00001", "REQ-API-00002"},
		},
		{
			name: "related-to cycle is exempt",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelRelatedTo, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelRelatedTo, Target: "REQ-API-00001"},
				}),
			),
		},
		{
			name: "dangling depends-on target is not a cycle",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00099"},
				}),
			),
		},
		{
			name: "related-to back edge does not cycle depends-on",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00002"},
				}),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelRelatedTo, Target: "REQ-API-00001"},
				}),
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := cycleDiagnostics(Validate(tc.snapshot))
			if len(diagnostics) != tc.wantCount {
				t.Fatalf("diagnostics=%v, want %d", diagnostics, tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			got := diagnostics[0]
			if got.Code != tc.wantCode {
				t.Errorf("code=%q, want %q", got.Code, tc.wantCode)
			}
			if got.Field != "relationships" {
				t.Errorf("field=%q, want relationships", got.Field)
			}
			if !strings.Contains(got.Message, tc.wantRelation) {
				t.Errorf("message %q does not identify relation %q", got.Message, tc.wantRelation)
			}
			if !identifiesIDs(got.Message, tc.wantIDs...) {
				t.Errorf("message %q does not identify IDs %v", got.Message, tc.wantIDs)
			}
			if got.Path == "" {
				t.Errorf("path is empty, want a participating requirement file")
			}
		})
	}
}

func TestValidateRelationshipAcyclicityReportsIndependentGraphs(t *testing.T) {
	snapshot := snapshotFromDocs(
		requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
			{Type: records.RelDependsOn, Target: "REQ-API-00002"},
			{Type: records.RelSupersedes, Target: "REQ-API-00002"},
		}),
		requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
			{Type: records.RelDependsOn, Target: "REQ-API-00003"},
			{Type: records.RelSupersedes, Target: "REQ-API-00001"},
		}),
		requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", nil),
	)

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1 supersedes cycle", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeCycle {
		t.Errorf("code=%q, want %q", got.Code, CodeCycle)
	}
	if !strings.Contains(got.Message, records.RelSupersedes) {
		t.Errorf("message %q does not identify supersedes", got.Message)
	}
	if strings.Contains(got.Message, records.RelDependsOn) {
		t.Errorf("message %q unexpectedly mentions depends-on", got.Message)
	}
	if !identifiesIDs(got.Message, "REQ-API-00001", "REQ-API-00002") {
		t.Errorf("message %q omitted participating IDs", got.Message)
	}
}

func TestValidateRelationshipAcyclicityIsDeterministic(t *testing.T) {
	snapshot := snapshotFromDocs(
		requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
			{Type: records.RelDependsOn, Target: "REQ-API-00001"},
		}),
		requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
			{Type: records.RelDependsOn, Target: "REQ-API-00002"},
		}),
	)
	first := Validate(snapshot)
	reversed := snapshotFromDocs(snapshot.Requirements[1], snapshot.Requirements[0])
	second := Validate(reversed)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("diagnostics first=%v second=%v, want 1 each", first, second)
	}
	if first[0] != second[0] {
		t.Fatalf("document order changed diagnostics: %+v vs %+v", first[0], second[0])
	}
}

func TestValidateRelationshipAcyclicityAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00002
status: active
`)
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: event-driven
text: When a client sends GET /ready, the service shall return HTTP 200.
relationships:
  - type: depends-on
    target: REQ-API-00001
status: active
`)

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeCycle {
		t.Errorf("code=%q, want %q", got.Code, CodeCycle)
	}
	if !strings.Contains(got.Message, records.RelDependsOn) {
		t.Errorf("message %q does not identify depends-on", got.Message)
	}
	if !identifiesIDs(got.Message, "REQ-API-00001", "REQ-API-00002") {
		t.Errorf("message %q omitted participating IDs", got.Message)
	}
}

func cycleDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	var filtered []Diagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == CodeCycle {
			filtered = append(filtered, diagnostic)
		}
	}
	return filtered
}

func snapshotFromDocs(docs ...layout.Document[records.Requirement]) layout.Snapshot {
	return layout.Snapshot{Requirements: docs}
}

func identifiesIDs(message string, ids ...string) bool {
	for _, id := range ids {
		if !strings.Contains(message, id) {
			return false
		}
	}
	return true
}

func requirementSnapshot(path, id string, relationships []records.Relationship, extraIDs ...string) layout.Snapshot {
	requirements := []layout.Document[records.Requirement]{
		requirementDocument(path, id, relationships),
	}
	seen := map[string]bool{id: true}
	for _, extraID := range extraIDs {
		if extraID == "" || seen[extraID] {
			continue
		}
		seen[extraID] = true
		requirements = append(requirements, requirementDocument(
			"spec/requirements/"+extraID+".yaml",
			extraID,
			nil,
		))
	}
	return layout.Snapshot{Requirements: requirements}
}

func relationshipTargets(relationships []records.Relationship) []string {
	seen := map[string]bool{}
	var ids []string
	for _, relationship := range relationships {
		if relationship.Target == "" || seen[relationship.Target] {
			continue
		}
		seen[relationship.Target] = true
		ids = append(ids, relationship.Target)
	}
	return ids
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
