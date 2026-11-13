package specvalidation

import (
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestValidateRequirementIDs(t *testing.T) {
	cases := []struct {
		name      string
		ids       []idPath
		wantCodes []string
		wantIDs   []string
	}{
		{
			name: "compliant ids",
			ids: []idPath{
				{path: "spec/requirements/REQ-CORE-00001.yaml", id: "REQ-CORE-00001"},
				{path: "spec/requirements/REQ-API-00042.yaml", id: "REQ-API-00042"},
			},
		},
		{
			name: "lowercase scope",
			ids: []idPath{
				{path: "spec/requirements/bad.yaml", id: "REQ-api-00001"},
			},
			wantCodes: []string{CodeInvalidID},
			wantIDs:   []string{"REQ-api-00001"},
		},
		{
			name: "missing prefix",
			ids: []idPath{
				{path: "spec/requirements/bad.yaml", id: "API-00001"},
			},
			wantCodes: []string{CodeInvalidID},
			wantIDs:   []string{"API-00001"},
		},
		{
			name: "incorrect digit count",
			ids: []idPath{
				{path: "spec/requirements/bad.yaml", id: "REQ-API-42"},
			},
			wantCodes: []string{CodeInvalidID},
			wantIDs:   []string{"REQ-API-42"},
		},
		{
			name: "empty id",
			ids: []idPath{
				{path: "spec/requirements/empty.yaml", id: ""},
			},
			wantCodes: []string{CodeInvalidID},
			wantIDs:   []string{""},
		},
		{
			name: "duplicate ids",
			ids: []idPath{
				{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
				{path: "spec/requirements/copy.yaml", id: "REQ-API-00001"},
			},
			wantCodes: []string{CodeDuplicateID, CodeDuplicateID},
			wantIDs:   []string{"REQ-API-00001", "REQ-API-00001"},
		},
		{
			name: "invalid id is not indexed as a duplicate",
			ids: []idPath{
				{path: "spec/requirements/a.yaml", id: "bad"},
				{path: "spec/requirements/b.yaml", id: "bad"},
			},
			wantCodes: []string{CodeInvalidID, CodeInvalidID},
			wantIDs:   []string{"bad", "bad"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := Validate(snapshotFromIDs(tc.ids))
			got := identifierDiagnostics(diagnostics)
			if len(got) != len(tc.wantCodes) {
				t.Fatalf("diagnostics=%v, want %d", got, len(tc.wantCodes))
			}
			for i, code := range tc.wantCodes {
				if got[i].Code != code {
					t.Errorf("code=%q, want %q", got[i].Code, code)
				}
				if !identifiesID(got[i].Message, tc.wantIDs[i]) {
					t.Errorf("message %q does not identify %q", got[i].Message, tc.wantIDs[i])
				}
				if got[i].Field != "id" {
					t.Errorf("field=%q, want id", got[i].Field)
				}
			}
			if len(tc.wantCodes) == 2 && tc.wantCodes[0] == CodeDuplicateID {
				if !strings.Contains(got[0].Message, "REQ-API-00001.yaml") || !strings.Contains(got[0].Message, "copy.yaml") {
					t.Errorf("duplicate diagnostic %q omitted conflicting paths", got[0].Message)
				}
			}
		})
	}
}

func TestCompareBaseRejectsDeletion(t *testing.T) {
	base := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
	})
	target := snapshotFromIDs(nil)
	diagnostics := CompareBase(target, base)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeDeleted {
		t.Errorf("code=%q, want %q", got.Code, CodeDeleted)
	}
	if got.Path != "spec/requirements/REQ-API-00001.yaml" {
		t.Errorf("path=%q", got.Path)
	}
	if !identifiesID(got.Message, "REQ-API-00001") {
		t.Errorf("message %q omitted id", got.Message)
	}
}

func TestCompareBaseAllowsRetirement(t *testing.T) {
	base := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
	})
	target := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			{
				Path: "spec/requirements/REQ-API-00001.yaml",
				Record: records.Requirement{
					ID:     "REQ-API-00001",
					Status: records.StatusRetired,
				},
			},
		},
	}
	if diagnostics := CompareBase(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none", diagnostics)
	}
}

func TestCompareBaseRejectsReuse(t *testing.T) {
	base := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
	})
	target := snapshotFromIDs([]idPath{
		{path: "spec/requirements/other.yaml", id: "REQ-API-00001"},
	})
	diagnostics := CompareBase(target, base)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeReusedID {
		t.Errorf("code=%q, want %q", got.Code, CodeReusedID)
	}
	if got.Path != "spec/requirements/other.yaml" {
		t.Errorf("path=%q", got.Path)
	}
	if !identifiesID(got.Message, "REQ-API-00001") {
		t.Errorf("message %q omitted id", got.Message)
	}
	if !strings.Contains(got.Message, "REQ-API-00001.yaml") {
		t.Errorf("message %q omitted base path", got.Message)
	}
}

func TestCompareBaseAllowsNewIDs(t *testing.T) {
	base := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
	})
	target := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
		{path: "spec/requirements/REQ-API-00002.yaml", id: "REQ-API-00002"},
	})
	if diagnostics := CompareBase(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none", diagnostics)
	}
}

func TestCompareBaseValidatesBaseIDs(t *testing.T) {
	// A working-tree fix of historically invalid or duplicate base IDs must pass
	// CompareBase, while still failing deletion of valid base IDs and reuse.
	base := snapshotFromIDs([]idPath{
		{path: "spec/requirements/bad.yaml", id: "not-an-id"},
		{path: "spec/requirements/dup1.yaml", id: "REQ-DUP-00001"},
		{path: "spec/requirements/dup2.yaml", id: "REQ-DUP-00001"},
		{path: "spec/requirements/valid.yaml", id: "REQ-VAL-00001"},
	})
	// Target fixed the invalid ID, resolved duplicate to one valid ID, and kept valid.yaml
	target := snapshotFromIDs([]idPath{
		{path: "spec/requirements/bad.yaml", id: "REQ-FIX-00001"},
		{path: "spec/requirements/dup1.yaml", id: "REQ-DUP-00001"},
		{path: "spec/requirements/valid.yaml", id: "REQ-VAL-00001"},
	})
	if diagnostics := CompareBase(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none (working-tree fixes should pass CompareBase)", diagnostics)
	}

	// Deletion of a valid base ID must still fail
	targetDeleted := snapshotFromIDs([]idPath{
		{path: "spec/requirements/bad.yaml", id: "REQ-FIX-00001"},
		{path: "spec/requirements/dup1.yaml", id: "REQ-DUP-00001"},
	})
	delDiags := CompareBase(targetDeleted, base)
	if len(delDiags) != 1 || delDiags[0].Code != CodeDeleted || delDiags[0].Path != "spec/requirements/valid.yaml" {
		t.Fatalf("delDiags=%v, want CodeDeleted for valid.yaml", delDiags)
	}

	// Reuse of a valid base ID at a different path must still fail
	targetReused := snapshotFromIDs([]idPath{
		{path: "spec/requirements/other.yaml", id: "REQ-VAL-00001"},
		{path: "spec/requirements/dup1.yaml", id: "REQ-DUP-00001"},
	})
	reuseDiags := CompareBase(targetReused, base)
	if len(reuseDiags) != 1 || reuseDiags[0].Code != CodeReusedID || reuseDiags[0].Path != "spec/requirements/other.yaml" {
		t.Fatalf("reuseDiags=%v, want CodeReusedID for other.yaml", reuseDiags)
	}
}

func TestCompareBaseIsDeterministic(t *testing.T) {
	base := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-Z-00001.yaml", id: "REQ-Z-00001"},
		{path: "spec/requirements/REQ-A-00001.yaml", id: "REQ-A-00001"},
	})
	target := snapshotFromIDs(nil)
	first := CompareBase(target, base)
	second := CompareBase(target, base)
	if len(first) != len(second) {
		t.Fatalf("repeated comparison changed count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("repeated comparison changed diagnostics: %+v vs %+v", first, second)
		}
	}
}

type idPath struct {
	path string
	id   string
}

func snapshotFromIDs(ids []idPath) layout.Snapshot {
	docs := make([]layout.Document[records.Requirement], 0, len(ids))
	for _, item := range ids {
		docs = append(docs, layout.Document[records.Requirement]{
			Path: item.path,
			Record: records.Requirement{
				ID: item.id,
			},
		})
	}
	return layout.Snapshot{Requirements: docs}
}

func identifierDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	var out []Diagnostic
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case CodeInvalidID, CodeDuplicateID, CodeDeleted, CodeReusedID:
			out = append(out, diagnostic)
		}
	}
	return out
}

func identifiesID(message, id string) bool {
	if id == "" {
		return strings.Contains(message, `""`)
	}
	return strings.Contains(message, id)
}
