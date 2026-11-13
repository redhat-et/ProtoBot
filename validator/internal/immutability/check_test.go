package immutability

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestCheckUnchangedTextPasses(t *testing.T) {
	target := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
	)
	base := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
	)
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestCheckYAMLScalarReformattingPasses(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := t.TempDir()
	writeFile(t, baseDir, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: The service shall exist.
status: active
`)
	writeFile(t, targetDir, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: ubiquitous
text: |-
  The service shall exist.
status: active
`)

	base, baseDiags := layout.Read(baseDir)
	target, targetDiags := layout.Read(targetDir)
	if len(baseDiags) != 0 || len(targetDiags) != 0 {
		t.Fatalf("layout diagnostics base=%v target=%v", baseDiags, targetDiags)
	}
	if target.Requirements[0].Record.Text != base.Requirements[0].Record.Text {
		t.Fatalf("parsed text diverged: target=%q base=%q", target.Requirements[0].Record.Text, base.Requirements[0].Record.Text)
	}
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestCheckFoldedScalarEquivalentTextPasses(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := t.TempDir()
	writeFile(t, baseDir, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: event-driven
text: When a client sends GET /health, the service shall return HTTP 200.
status: active
`)
	writeFile(t, targetDir, "spec/requirements/REQ-API-00001.yaml", `id: REQ-API-00001
type: event-driven
text: >-
  When a client sends GET /health, the service
  shall return HTTP 200.
status: active
`)

	base, baseDiags := layout.Read(baseDir)
	target, targetDiags := layout.Read(targetDir)
	if len(baseDiags) != 0 || len(targetDiags) != 0 {
		t.Fatalf("layout diagnostics base=%v target=%v", baseDiags, targetDiags)
	}
	if target.Requirements[0].Record.Text != base.Requirements[0].Record.Text {
		t.Fatalf("parsed text diverged: target=%q base=%q", target.Requirements[0].Record.Text, base.Requirements[0].Record.Text)
	}
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestCheckModifiedTextFails(t *testing.T) {
	target := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist now.", records.StatusActive),
	)
	base := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
	)
	diagnostics := Check(target, base)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeTextImmutable {
		t.Fatalf("code=%q", got.Code)
	}
	if got.Path != "spec/requirements/REQ-API-00001.yaml" {
		t.Fatalf("path=%q", got.Path)
	}
	if got.Message == "" {
		t.Fatal("message is empty")
	}
}

func TestCheckMetadataOnlyTransitionPasses(t *testing.T) {
	target := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusRetired),
	)
	target.Requirements[0].Record.Relationships = []records.Relationship{{
		Type:   records.RelSupersedes,
		Target: "REQ-API-00000",
	}}
	base := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
	)
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestCheckNewIDsPass(t *testing.T) {
	target := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
		document("spec/requirements/REQ-API-00002.yaml", "REQ-API-00002", "The service shall log health checks.", records.StatusActive),
	)
	base := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
	)
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func TestCheckReportsOnlyChangedIDs(t *testing.T) {
	target := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
		document("spec/requirements/REQ-Z-00001.yaml", "REQ-Z-00001", "The service shall change.", records.StatusActive),
		document("spec/requirements/REQ-A-00001.yaml", "REQ-A-00001", "The service shall also change.", records.StatusActive),
	)
	base := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
		document("spec/requirements/REQ-Z-00001.yaml", "REQ-Z-00001", "The service shall stay.", records.StatusActive),
		document("spec/requirements/REQ-A-00001.yaml", "REQ-A-00001", "The service shall remain.", records.StatusActive),
	)
	diagnostics := Check(target, base)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if diagnostics[0].Path != "spec/requirements/REQ-A-00001.yaml" {
		t.Fatalf("first path=%q", diagnostics[0].Path)
	}
	if diagnostics[1].Path != "spec/requirements/REQ-Z-00001.yaml" {
		t.Fatalf("second path=%q", diagnostics[1].Path)
	}
}

func TestCheckDeletedIDsAreNotThisCheck(t *testing.T) {
	target := snapshot()
	base := snapshot(
		document("spec/requirements/REQ-API-00001.yaml", "REQ-API-00001", "The service shall exist.", records.StatusActive),
	)
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("deletion is out of scope for this check: %v", diagnostics)
	}
}

func TestCheckSkipsEmptyIDs(t *testing.T) {
	target := snapshot(
		document("spec/requirements/missing-id.yaml", "", "The service shall exist.", records.StatusActive),
	)
	base := snapshot(
		document("spec/requirements/missing-id.yaml", "", "The service shall change.", records.StatusActive),
	)
	if diagnostics := Check(target, base); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
}

func snapshot(documents ...layout.Document[records.Requirement]) layout.Snapshot {
	return layout.Snapshot{Requirements: documents}
}

func document(path, id, text, status string) layout.Document[records.Requirement] {
	return layout.Document[records.Requirement]{
		Path: path,
		Record: records.Requirement{
			ID:     id,
			Text:   text,
			Status: status,
		},
	}
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
