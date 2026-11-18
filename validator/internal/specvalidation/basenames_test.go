package specvalidation

import (
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestValidateBasenames(t *testing.T) {
	cases := []struct {
		name      string
		snapshot  layout.Snapshot
		wantCode  string
		wantPath  string
		wantField string
		wantInMsg []string
	}{
		{
			name: "conforming requirement and interface",
			snapshot: layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{{
					Path:   "spec/requirements/REQ-CORE-00001.yaml",
					Record: records.Requirement{ID: "REQ-CORE-00001"},
				}},
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/billing-api.yaml",
					Record: records.InterfaceRecord{Name: "billing-api"},
				}},
			},
		},
		{
			name: "hyphenated requirement scope matches shared identifier pattern",
			snapshot: layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{{
					Path:   "spec/requirements/REQ-CORE-X-00001.yaml",
					Record: records.Requirement{ID: "REQ-CORE-X-00001"},
				}},
			},
		},
		{
			name: "malformed requirement basename",
			snapshot: layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{{
					Path:   "spec/requirements/notes.yaml",
					Record: records.Requirement{ID: "REQ-CORE-00001"},
				}},
			},
			wantCode:  CodeRequirementInvalidBasename,
			wantPath:  "spec/requirements/notes.yaml",
			wantInMsg: []string{"notes", "REQ-<SCOPE>-<NNNNN>.yaml"},
		},
		{
			name: "requirement id differs from valid basename",
			snapshot: layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{{
					Path:   "spec/requirements/REQ-CORE-00001.yaml",
					Record: records.Requirement{ID: "REQ-CORE-00002"},
				}},
			},
			wantCode:  CodeRequirementIDBasenameMismatch,
			wantPath:  "spec/requirements/REQ-CORE-00001.yaml",
			wantField: "id",
			wantInMsg: []string{"REQ-CORE-00001", "REQ-CORE-00002"},
		},
		{
			name: "interface name differs from basename",
			snapshot: layout.Snapshot{
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/service.yaml",
					Record: records.InterfaceRecord{Name: "billing-api"},
				}},
			},
			wantCode:  CodeInterfaceNameBasenameMismatch,
			wantPath:  "spec/interfaces/service.yaml",
			wantField: "name",
			wantInMsg: []string{"service", "billing-api"},
		},
		{
			name: "interface name cannot be one path segment",
			snapshot: layout.Snapshot{
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/service.yaml",
					Record: records.InterfaceRecord{Name: "billing/api"},
				}},
			},
			wantCode:  CodeInterfaceNameBasenameMismatch,
			wantPath:  "spec/interfaces/service.yaml",
			wantField: "name",
			wantInMsg: []string{"service", "billing/api", "path separator"},
		},
		{
			name: "degenerate basename matching a degenerate name is still rejected",
			snapshot: layout.Snapshot{
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/...yaml",
					Record: records.InterfaceRecord{Name: ".."},
				}},
			},
			wantCode:  CodeInterfaceInvalidBasename,
			wantPath:  "spec/interfaces/...yaml",
			wantInMsg: []string{".."},
		},
		{
			name: "trailing dot basename matching a trailing dot name is still rejected",
			snapshot: layout.Snapshot{
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/service..yaml",
					Record: records.InterfaceRecord{Name: "service."},
				}},
			},
			wantCode:  CodeInterfaceInvalidBasename,
			wantPath:  "spec/interfaces/service..yaml",
			wantInMsg: []string{"service."},
		},
		{
			name: "invalid interface basename does not emit spurious name mismatch when name differs",
			snapshot: layout.Snapshot{
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/...yaml",
					Record: records.InterfaceRecord{Name: "billing-api"},
				}},
			},
			wantCode:  CodeInterfaceInvalidBasename,
			wantPath:  "spec/interfaces/...yaml",
			wantInMsg: []string{".."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := basenameDiagnostics(Validate(tc.snapshot))
			if tc.wantCode == "" {
				if len(got) != 0 {
					t.Fatalf("diagnostics=%v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("diagnostics=%v, want 1", got)
			}
			if got[0].Code != tc.wantCode {
				t.Errorf("code=%q, want %q", got[0].Code, tc.wantCode)
			}
			if got[0].Path != tc.wantPath {
				t.Errorf("path=%q, want %q", got[0].Path, tc.wantPath)
			}
			if got[0].Field != tc.wantField {
				t.Errorf("field=%q, want %q", got[0].Field, tc.wantField)
			}
			for _, fragment := range tc.wantInMsg {
				if !strings.Contains(got[0].Message, fragment) {
					t.Errorf("message %q does not contain %q", got[0].Message, fragment)
				}
			}
		})
	}
}

func TestValidateBasenamesSkipsEmptyIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reqID     string
		ifaceName string
	}{
		{name: "empty strings", reqID: "", ifaceName: ""},
		{name: "spaces only", reqID: "   ", ifaceName: "   "},
		{name: "tabs and newlines", reqID: "\t\n", ifaceName: "\t\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{{
					Path:   "spec/requirements/REQ-CORE-00001.yaml",
					Record: records.Requirement{ID: tc.reqID},
				}},
				Interfaces: []layout.Document[records.InterfaceRecord]{{
					Path:   "spec/interfaces/billing-api.yaml",
					Record: records.InterfaceRecord{Name: tc.ifaceName},
				}},
			}
			got := basenameDiagnostics(Validate(snapshot))
			if len(got) != 0 {
				t.Fatalf("diagnostics=%v, want none when id or name is empty or whitespace", got)
			}
		})
	}
}

func TestValidateBasenamesDoesNotDropRecords(t *testing.T) {
	snapshot := snapshotFromIDs([]idPath{
		{path: "spec/requirements/REQ-API-00001.yaml", id: "REQ-API-00001"},
		{path: "spec/requirements/copy.yaml", id: "REQ-API-00001"},
	})

	diagnostics := Validate(snapshot)
	gotBasename := basenameDiagnostics(diagnostics)
	if len(gotBasename) != 1 || gotBasename[0].Code != CodeRequirementInvalidBasename {
		t.Fatalf("basename diagnostics=%v, want requirement.invalid_basename for copy.yaml", gotBasename)
	}
	if gotBasename[0].Path != "spec/requirements/copy.yaml" {
		t.Errorf("path=%q, want spec/requirements/copy.yaml", gotBasename[0].Path)
	}

	gotIDs := identifierDiagnostics(diagnostics)
	if len(gotIDs) != 2 {
		t.Fatalf("identifier diagnostics=%v, want duplicate_id for both paths", gotIDs)
	}
	for _, diagnostic := range gotIDs {
		if diagnostic.Code != CodeDuplicateID {
			t.Errorf("code=%q, want %q", diagnostic.Code, CodeDuplicateID)
		}
	}
}

func TestValidateBasenamesAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/notes.yaml", `id: REQ-CORE-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeFile(t, target, "spec/requirements/REQ-CORE-00002.yaml", `id: REQ-CORE-00003
type: ubiquitous
text: The service shall log requests.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeFile(t, target, "spec/interfaces/service.yaml", `name: billing-api
kind: http
description: Billing API.
`)

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}
	if len(snapshot.Requirements) != 2 {
		t.Fatalf("requirements=%d, want 2", len(snapshot.Requirements))
	}
	if len(snapshot.Interfaces) != 1 {
		t.Fatalf("interfaces=%d, want 1", len(snapshot.Interfaces))
	}

	got := basenameDiagnostics(Validate(snapshot))
	if len(got) != 3 {
		t.Fatalf("diagnostics=%v, want 3", got)
	}
	if got[0].Code != CodeInterfaceNameBasenameMismatch || got[0].Path != "spec/interfaces/service.yaml" {
		t.Errorf("first diagnostic=%+v", got[0])
	}
	if got[1].Code != CodeRequirementIDBasenameMismatch || got[1].Path != "spec/requirements/REQ-CORE-00002.yaml" {
		t.Errorf("second diagnostic=%+v", got[1])
	}
	if got[2].Code != CodeRequirementInvalidBasename || got[2].Path != "spec/requirements/notes.yaml" {
		t.Errorf("third diagnostic=%+v", got[2])
	}
}

func TestValidateBasenamesRejectsDegenerateInterfaceFixtureAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/interfaces/...yaml", `name: ".."
kind: http
description: Degenerate interface name.
`)

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}
	if len(snapshot.Interfaces) != 1 {
		t.Fatalf("interfaces=%d, want 1", len(snapshot.Interfaces))
	}

	diagnostics := Validate(snapshot)
	var sawInvalidName, sawInvalidBasename bool
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case CodeInterfaceInvalidName:
			sawInvalidName = true
		case CodeInterfaceInvalidBasename:
			sawInvalidBasename = true
		}
	}
	if !sawInvalidName || !sawInvalidBasename {
		t.Fatalf("diagnostics=%v, want both %s and %s for a degenerate interface name", diagnostics, CodeInterfaceInvalidName, CodeInterfaceInvalidBasename)
	}
}

func basenameDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	var out []Diagnostic
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case CodeRequirementInvalidBasename, CodeRequirementIDBasenameMismatch, CodeInterfaceInvalidBasename, CodeInterfaceNameBasenameMismatch:
			out = append(out, diagnostic)
		}
	}
	return out
}
