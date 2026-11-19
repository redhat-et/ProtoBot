package specvalidation

import (
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
	"github.com/redhat-et/protobot/validator/internal/testutil"
)

func TestCanonicalFixtureBuilders(t *testing.T) {
	reqDoc := testutil.ValidRequirementDocument()
	if errs := Validate(layout.Snapshot{Requirements: []layout.Document[records.Requirement]{reqDoc}}); len(errs) != 0 {
		t.Fatalf("ValidRequirementDocument() failed baseline validation: %v", errs)
	}

	customReq := testutil.ValidRequirementDocument(
		testutil.WithID("REQ-CLI-00001"),
		testutil.WithStatus(records.StatusRetired),
	)
	if customReq.Record.ID != "REQ-CLI-00001" || customReq.Record.Status != records.StatusRetired {
		t.Fatalf("Functional options failed to mutate document: %+v", customReq.Record)
	}
	if errs := Validate(layout.Snapshot{Requirements: []layout.Document[records.Requirement]{customReq}}); len(errs) != 0 {
		t.Fatalf("mutated ValidRequirementDocument() failed validation: %v", errs)
	}

	ifaceDoc := testutil.ValidInterfaceDocument()
	if errs := Validate(layout.Snapshot{Interfaces: []layout.Document[records.InterfaceRecord]{ifaceDoc}}); len(errs) != 0 {
		t.Fatalf("ValidInterfaceDocument() failed baseline validation: %v", errs)
	}
}

func TestCanonicalFixtureYAMLRoundTrip(t *testing.T) {
	target := t.TempDir()
	reqDoc := testutil.ValidRequirementDocument()
	ifaceDoc := testutil.ValidInterfaceDocument()
	writeFile(t, target, reqDoc.Path, testutil.ValidRequirementYAML())
	writeFile(t, target, ifaceDoc.Path, testutil.ValidInterfaceYAML())

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("len(snapshot.Requirements)=%d, want 1", len(snapshot.Requirements))
	}
	if len(snapshot.Interfaces) != 1 {
		t.Fatalf("len(snapshot.Interfaces)=%d, want 1", len(snapshot.Interfaces))
	}
	if errs := Validate(snapshot); len(errs) != 0 {
		t.Fatalf("round-tripped fixtures failed validation: %v", errs)
	}
}
