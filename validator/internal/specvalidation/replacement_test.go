package specvalidation

import (
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestValidateReplacement(t *testing.T) {
	cases := []struct {
		name      string
		snapshot  layout.Snapshot
		wantCount int
		wantCode  string
		wantPath  string
		wantField string
		wantIDs   []string
	}{
		{
			name: "supersedes retired predecessor",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}),
				withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
			),
		},
		{
			name: "retirement without successor",
			snapshot: snapshotFromDocs(
				withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
			),
		},
		{
			name: "supersedes active predecessor",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}),
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil),
			),
			wantCount: 1,
			wantCode:  CodeSupersededRequirementActive,
			wantPath:  "spec/requirements/REQ-API-00002.yaml",
			wantField: "relationships[0].target",
			wantIDs:   []string{"REQ-API-00001"},
		},
		{
			name: "mixed retired and active predecessors",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
					{Type: records.RelSupersedes, Target: "REQ-API-00002"},
				}),
				withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", nil),
			),
			wantCount: 1,
			wantCode:  CodeSupersededRequirementActive,
			wantPath:  "spec/requirements/REQ-API-00003.yaml",
			wantField: "relationships[1].target",
			wantIDs:   []string{"REQ-API-00002"},
		},
		{
			name: "dangling supersedes is not a replacement failure",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-MISSING-00001"},
				}),
			),
		},
		{
			name: "empty supersedes target is not a replacement failure",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: ""},
				}),
			),
		},
		{
			name: "duplicate predecessor id is skipped",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}),
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil),
				requirementDocument("spec/requirements/copy.yaml", "REQ-API-00001", nil),
			),
		},
		{
			name: "depends-on active target is allowed",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelDependsOn, Target: "REQ-API-00001"},
				}),
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil),
			),
		},
		{
			name: "related-to active target is allowed",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelRelatedTo, Target: "REQ-API-00001"},
				}),
				requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil),
			),
		},
		{
			name: "supersedes chain with retired predecessors",
			snapshot: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00002"},
				}),
				withStatus(requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}), records.StatusRetired),
				withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := replacementDiagnostics(Validate(tc.snapshot))
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
			if got.Path != tc.wantPath {
				t.Errorf("path=%q, want %q", got.Path, tc.wantPath)
			}
			if got.Field != tc.wantField {
				t.Errorf("field=%q, want %q", got.Field, tc.wantField)
			}
			if !identifiesIDs(got.Message, tc.wantIDs...) {
				t.Errorf("message %q omitted IDs %v", got.Message, tc.wantIDs)
			}
		})
	}
}

func TestValidateReplacementIsDeterministic(t *testing.T) {
	snapshot := snapshotFromDocs(
		requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
			{Type: records.RelSupersedes, Target: "REQ-API-00001"},
		}),
		requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil),
	)
	first := Validate(snapshot)
	reversed := snapshotFromDocs(snapshot.Requirements[1], snapshot.Requirements[0])
	second := Validate(reversed)
	gotFirst := replacementDiagnostics(first)
	gotSecond := replacementDiagnostics(second)
	if len(gotFirst) != 1 || len(gotSecond) != 1 {
		t.Fatalf("diagnostics first=%v second=%v, want 1 each", gotFirst, gotSecond)
	}
	if gotFirst[0] != gotSecond[0] {
		t.Fatalf("document order changed diagnostics: %+v vs %+v", gotFirst[0], gotSecond[0])
	}
}

func TestValidateReplacementSelfSupersedes(t *testing.T) {
	snapshot := snapshotFromDocs(
		requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", []records.Relationship{
			{Type: records.RelSupersedes, Target: "REQ-API-00001"},
		}),
	)
	diagnostics := Validate(snapshot)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2", diagnostics)
	}
	if diagnostics[0].Code != CodeCycle {
		t.Errorf("first code=%q, want %q", diagnostics[0].Code, CodeCycle)
	}
	if diagnostics[0].Field != "relationships" {
		t.Errorf("first field=%q, want relationships", diagnostics[0].Field)
	}
	if !identifiesIDs(diagnostics[0].Message, "REQ-API-00001") {
		t.Errorf("cycle message %q omitted ID", diagnostics[0].Message)
	}
	if diagnostics[1].Code != CodeSupersededRequirementActive {
		t.Errorf("second code=%q, want %q", diagnostics[1].Code, CodeSupersededRequirementActive)
	}
	if diagnostics[1].Field != "relationships[0].target" {
		t.Errorf("second field=%q, want relationships[0].target", diagnostics[1].Field)
	}
	if !identifiesIDs(diagnostics[1].Message, "REQ-API-00001") {
		t.Errorf("replacement message %q omitted ID", diagnostics[1].Message)
	}
	const path = "spec/requirements/REQ-API-00001.yaml"
	if diagnostics[0].Path != path || diagnostics[1].Path != path {
		t.Errorf("paths=%q %q, want %q", diagnostics[0].Path, diagnostics[1].Path, path)
	}
}

func TestValidateReplacementAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", `id: REQ-API-00002
type: ubiquitous
text: The service shall log requests.
applies_to:
  scopes:
    - api
relationships:
  - type: supersedes
    target: REQ-API-00001
status: active
`)

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}

	diagnostics := replacementDiagnostics(Validate(snapshot))
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeSupersededRequirementActive {
		t.Errorf("code=%q, want %q", got.Code, CodeSupersededRequirementActive)
	}
	if got.Path != "spec/requirements/REQ-API-00002.yaml" {
		t.Errorf("path=%q", got.Path)
	}
	if got.Field != "relationships[0].target" {
		t.Errorf("field=%q, want relationships[0].target", got.Field)
	}
	if !identifiesIDs(got.Message, "REQ-API-00001") {
		t.Errorf("message %q omitted predecessor id", got.Message)
	}
}

func TestCompareReplacement(t *testing.T) {
	retiredPredecessor := withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired)
	activePredecessor := requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil)
	replacement := requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
		{Type: records.RelSupersedes, Target: "REQ-API-00001"},
	})
	historicalReplacement := requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
		{Type: records.RelSupersedes, Target: "REQ-API-00001"},
	})

	cases := []struct {
		name      string
		target    layout.Snapshot
		base      layout.Snapshot
		wantCount int
		wantCode  string
		wantPath  string
		wantIDs   []string
	}{
		{
			name:   "new replacement retires predecessor in this change",
			target: snapshotFromDocs(replacement, retiredPredecessor),
			base:   snapshotFromDocs(activePredecessor),
		},
		{
			name:   "new predecessor retired with replacement",
			target: snapshotFromDocs(replacement, retiredPredecessor),
			base:   snapshotFromDocs(),
		},
		{
			name:   "retirement without replacement",
			target: snapshotFromDocs(retiredPredecessor),
			base:   snapshotFromDocs(activePredecessor),
		},
		{
			name:   "historical supersedes remains valid",
			target: snapshotFromDocs(historicalReplacement, retiredPredecessor),
			base:   snapshotFromDocs(historicalReplacement, retiredPredecessor),
		},
		{
			name:      "new supersedes of already retired predecessor",
			target:    snapshotFromDocs(replacement, retiredPredecessor),
			base:      snapshotFromDocs(retiredPredecessor),
			wantCount: 1,
			wantCode:  CodeSupersededRequirementNotRetiredInChange,
			wantPath:  "spec/requirements/REQ-API-00002.yaml",
			wantIDs:   []string{"REQ-API-00001"},
		},
		{
			name:   "active predecessor is not a same-change diagnostic",
			target: snapshotFromDocs(replacement, activePredecessor),
			base:   snapshotFromDocs(activePredecessor),
		},
		{
			name: "existing id gains supersedes of predecessor retired in this change",
			target: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}),
				retiredPredecessor,
			),
			base: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", nil),
				activePredecessor,
			),
		},
		{
			name: "chain retired in one change",
			target: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00002"},
				}),
				withStatus(requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-API-00001"},
				}), records.StatusRetired),
				retiredPredecessor,
			),
			base: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", nil),
				activePredecessor,
			),
		},
		{
			name: "dangling new supersedes is skipped",
			target: snapshotFromDocs(
				requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
					{Type: records.RelSupersedes, Target: "REQ-MISSING-00001"},
				}),
			),
			base: snapshotFromDocs(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := CompareReplacement(tc.target, tc.base)
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
			if got.Path != tc.wantPath {
				t.Errorf("path=%q, want %q", got.Path, tc.wantPath)
			}
			if got.Field != "relationships[0].target" {
				t.Errorf("field=%q, want relationships[0].target", got.Field)
			}
			if !identifiesIDs(got.Message, tc.wantIDs...) {
				t.Errorf("message %q omitted IDs %v", got.Message, tc.wantIDs)
			}
			if !strings.Contains(got.Message, "this specification change") {
				t.Errorf("message %q omitted same-change guidance", got.Message)
			}
		})
	}
}

func TestCompareReplacementIsDeterministic(t *testing.T) {
	target := snapshotFromDocs(
		requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", []records.Relationship{
			{Type: records.RelSupersedes, Target: "REQ-API-00001"},
		}),
		withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
	)
	base := snapshotFromDocs(
		withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
	)
	first := CompareReplacement(target, base)
	reversed := snapshotFromDocs(target.Requirements[1], target.Requirements[0])
	second := CompareReplacement(reversed, base)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("diagnostics first=%v second=%v, want 1 each", first, second)
	}
	if first[0] != second[0] {
		t.Fatalf("document order changed diagnostics: %+v vs %+v", first[0], second[0])
	}
}

func TestCompareReplacementReportsEveryNewEdge(t *testing.T) {
	target := snapshotFromDocs(
		requirementDocument("spec/requirements/REQ-API-00003.yaml", "REQ-API-00003", []records.Relationship{
			{Type: records.RelSupersedes, Target: "REQ-API-00001"},
			{Type: records.RelSupersedes, Target: "REQ-API-00002"},
		}),
		withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
		withStatus(requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", nil), records.StatusRetired),
	)
	base := snapshotFromDocs(
		withStatus(requirementDocument("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", nil), records.StatusRetired),
		withStatus(requirementDocument("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", nil), records.StatusRetired),
	)

	diagnostics := CompareReplacement(target, base)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2", diagnostics)
	}
	if diagnostics[0].Field != "relationships[0].target" || diagnostics[1].Field != "relationships[1].target" {
		t.Fatalf("fields=%q %q, want relationships[0].target then relationships[1].target", diagnostics[0].Field, diagnostics[1].Field)
	}
	if !identifiesIDs(diagnostics[0].Message, "REQ-API-00001") {
		t.Errorf("first message %q omitted REQ-API-00001", diagnostics[0].Message)
	}
	if !identifiesIDs(diagnostics[1].Message, "REQ-API-00002") {
		t.Errorf("second message %q omitted REQ-API-00002", diagnostics[1].Message)
	}
}

func replacementDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	var filtered []Diagnostic
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case CodeSupersededRequirementActive, CodeSupersededRequirementNotRetiredInChange:
			filtered = append(filtered, diagnostic)
		}
	}
	return filtered
}
