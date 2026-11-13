package layout

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestReadValidRecords(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", validRequirement)
	writeFile(t, target, "spec/interfaces/health-api.yaml", validInterface)

	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if snapshot.Root != abs(t, target) {
		t.Fatalf("Root=%q, want %q", snapshot.Root, abs(t, target))
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("requirements=%d, want 1: %+v", len(snapshot.Requirements), snapshot.Requirements)
	}
	if len(snapshot.Interfaces) != 1 {
		t.Fatalf("interfaces=%d, want 1: %+v", len(snapshot.Interfaces), snapshot.Interfaces)
	}

	req := snapshot.Requirements[0]
	if req.Path != "spec/requirements/REQ-API-00002.yaml" {
		t.Fatalf("requirement path=%q", req.Path)
	}
	if req.Record.ID != "REQ-API-00002" || req.Record.Type != records.TypeEventDriven {
		t.Fatalf("requirement identity=%+v", req.Record)
	}
	if !strings.Contains(req.Record.Text, "shall return") {
		t.Fatalf("requirement text %q omitted obligation", req.Record.Text)
	}
	if got := req.Record.AppliesTo.Interfaces; len(got) != 1 || got[0] != "health-api" {
		t.Fatalf("applies_to.interfaces=%v", got)
	}
	if got := req.Record.AppliesTo.Scopes; len(got) != 1 || got[0] != "api" {
		t.Fatalf("applies_to.scopes=%v", got)
	}
	if len(req.Record.Relationships) != 1 || req.Record.Relationships[0].Type != records.RelRelatedTo || req.Record.Relationships[0].Target != "REQ-API-00001" {
		t.Fatalf("relationships=%v", req.Record.Relationships)
	}
	if req.Record.Status != records.StatusActive {
		t.Fatalf("status=%q", req.Record.Status)
	}
	for _, field := range []string{"id", "type", "text", "applies_to", "relationships", "status"} {
		if !req.Fields[field] {
			t.Fatalf("requirement fields missing %q: %v", field, req.Fields)
		}
	}

	iface := snapshot.Interfaces[0]
	if iface.Path != "spec/interfaces/health-api.yaml" {
		t.Fatalf("interface path=%q", iface.Path)
	}
	if iface.Record.Name != "health-api" || iface.Record.Kind != "http" {
		t.Fatalf("interface=%+v", iface.Record)
	}
	if iface.Record.Description == "" {
		t.Fatal("interface description is empty")
	}
	for _, field := range []string{"name", "kind", "description"} {
		if !iface.Fields[field] {
			t.Fatalf("interface fields missing %q: %v", field, iface.Fields)
		}
	}
}

func TestReadIgnoresLegacyMetadata(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", validRequirement+`
verification:
  mode: isolated-interface
  rationale: boundary-only
provenance: user-authored
created: "2026-01-01T00:00:00Z"
`)

	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("requirements=%d, want 1", len(snapshot.Requirements))
	}
	req := snapshot.Requirements[0]
	if req.Record.ID != "REQ-API-00002" || req.Record.Status != records.StatusActive {
		t.Fatalf("parsed requirement=%+v", req.Record)
	}
	if !req.Fields["verification"] || !req.Fields["provenance"] || !req.Fields["created"] {
		t.Fatalf("legacy field names not recorded: %v", req.Fields)
	}
}

func TestReadSparseRequirementWithoutLegacyFields(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", "id: REQ-API-00002\n")

	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("requirements=%d, want 1", len(snapshot.Requirements))
	}
	req := snapshot.Requirements[0]
	if req.Record.ID != "REQ-API-00002" {
		t.Fatalf("id=%q", req.Record.ID)
	}
	if req.Record.Type != "" || req.Record.Text != "" || req.Record.Status != "" {
		t.Fatalf("omitted fields should stay empty: %+v", req.Record)
	}
	if req.Fields["verification"] || req.Fields["provenance"] || req.Fields["created"] {
		t.Fatalf("legacy metadata recorded as present: %v", req.Fields)
	}
}

func TestReadMissingDirectories(t *testing.T) {
	target := t.TempDir()
	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("missing directories produced diagnostics: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 || len(snapshot.Interfaces) != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}

	writeFile(t, target, "spec/README.md", "not a record store\n")
	snapshot, diagnostics = Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("spec README produced diagnostics: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 || len(snapshot.Interfaces) != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestReadEmptyDirectories(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(RequirementsDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(InterfacesDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if snapshot.Requirements != nil || snapshot.Interfaces != nil {
		t.Fatalf("empty stores should yield nil slices, got %+v", snapshot)
	}
}

func TestReadNonYAMLEntries(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", validRequirement)
	writeFile(t, target, "spec/requirements/notes.txt", "not yaml\n")
	writeFile(t, target, "spec/requirements/draft.yml", "id: REQ-API-00003\n")
	writeFile(t, target, "spec/interfaces/readme.md", "not yaml\n")
	if err := os.MkdirAll(filepath.Join(target, "spec", "requirements", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, "spec/requirements/notes.txt") {
		t.Fatalf("missing notes.txt diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, "spec/requirements/draft.yml") {
		t.Fatalf("missing draft.yml diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, "spec/interfaces/readme.md") {
		t.Fatalf("missing readme.md diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, "spec/requirements/nested") {
		t.Fatalf("missing nested directory diagnostic: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 || snapshot.Requirements[0].Record.ID != "REQ-API-00002" {
		t.Fatalf("valid record was not loaded: %+v", snapshot.Requirements)
	}
}

func TestReadMalformedYAML(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", validRequirement)
	writeFile(t, target, "spec/requirements/bad.yaml", "id: [\n")
	writeFile(t, target, "spec/interfaces/empty.yaml", "")
	writeFile(t, target, "spec/interfaces/multi.yaml", "name: one\n---\nname: two\n")
	writeFile(t, target, "spec/requirements/dup.yaml", "id: REQ-API-00001\nid: REQ-API-00002\n")
	writeFile(t, target, "spec/requirements/merge.yaml", "defaults: &defaults\n  status: active\n<<: *defaults\nid: REQ-API-00003\n")
	writeFile(t, target, "spec/requirements/alias.yaml", "id: &id REQ-API-00004\ntext: *id\n")
	writeFile(t, target, "spec/requirements/null.yaml", "null\n")
	writeFile(t, target, "spec/interfaces/null.yaml", "~\n")
	writeFile(t, target, "spec/requirements/scalar.yaml", "scalar\n")
	writeFile(t, target, "spec/interfaces/seq.yaml", "- item\n")
	writeFile(t, target, "spec/requirements/null-field.yaml", "id: REQ-API-00005\nstatus: null\n")

	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/bad.yaml") {
		t.Fatalf("missing malformed YAML diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/interfaces/empty.yaml") {
		t.Fatalf("missing empty YAML diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/interfaces/multi.yaml") {
		t.Fatalf("missing multi-document diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/dup.yaml") {
		t.Fatalf("missing duplicate key diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/merge.yaml") {
		t.Fatalf("missing merge key diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/alias.yaml") {
		t.Fatalf("missing alias diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/null.yaml") {
		t.Fatalf("missing null document diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/interfaces/null.yaml") {
		t.Fatalf("missing null interface diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/scalar.yaml") {
		t.Fatalf("missing scalar diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/interfaces/seq.yaml") {
		t.Fatalf("missing sequence diagnostic: %v", diagnostics)
	}
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/null-field.yaml") {
		t.Fatalf("missing null field diagnostic: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("valid requirement was dropped: %+v", snapshot.Requirements)
	}

	cases := []struct {
		name     string
		filename string
		content  string
		wantMsg  string
	}{
		{
			name:     "utf8 bom",
			filename: "spec/requirements/bom.yaml",
			content:  "\xef\xbb\xbfid: REQ-API-00001\ntype: ubiquitous\ntext: The service shall exist.\nstatus: active\n",
			wantMsg:  "UTF-8 BOM",
		},
		{
			name:     "invalid utf8",
			filename: "spec/requirements/invalid-utf8.yaml",
			content:  "id: REQ-API-00001\ntype: ubiquitous\ntext: \xff\xfe invalid\nstatus: active\n",
			wantMsg:  "not valid UTF-8",
		},
		{
			name:     "non-string mapping key",
			filename: "spec/requirements/non-string-key.yaml",
			content:  "123: value\nstatus: active\n",
			wantMsg:  "must be a string",
		},
		{
			name:     "explicit yaml tag custom",
			filename: "spec/requirements/tagged-custom.yaml",
			content:  "id: !custom REQ-API-00001\nstatus: active\n",
			wantMsg:  "unsafe YAML tag",
		},
		{
			name:     "explicit yaml tag standard",
			filename: "spec/requirements/tagged-standard.yaml",
			content:  "id: !!str REQ-API-00001\nstatus: active\n",
			wantMsg:  "unsafe YAML tag",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolated := t.TempDir()
			writeFile(t, isolated, tc.filename, tc.content)
			_, diags := Read(isolated)
			if !hasDiagnostic(diags, CodeParseFailed, tc.filename) {
				t.Fatalf("missing parse_failed diagnostic for %s: %v", tc.name, diags)
			}
			if !hasDiagnosticMessage(diags, CodeParseFailed, tc.filename, tc.wantMsg) {
				t.Fatalf("diagnostic for %s does not contain %q: %v", tc.name, tc.wantMsg, diags)
			}
		})
	}
}

func TestReadOversizedFile(t *testing.T) {
	target := t.TempDir()
	oversized := make([]byte, maxYAMLBytes+1)
	for i := range oversized {
		oversized[i] = ' '
	}
	copy(oversized, []byte("id: REQ-API-00001\ntext: "))
	writeFile(t, target, "spec/requirements/REQ-API-00001.yaml", string(oversized))

	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeReadFailed, "spec/requirements/REQ-API-00001.yaml") {
		t.Fatalf("missing oversized file diagnostic: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("oversized file was parsed: %+v", snapshot.Requirements)
	}
}

func TestReadExactLimitFile(t *testing.T) {
	target := t.TempDir()
	exact := make([]byte, maxYAMLBytes)
	for i := range exact {
		exact[i] = ' '
	}
	prefix := []byte("id: REQ-API-00001\ntype: ubiquitous\ntext: \"")
	copy(exact, prefix)
	suffix := []byte("\"\nstatus: active\n")
	copy(exact[len(exact)-len(suffix):], suffix)
	writeFile(t, target, "spec/requirements/REQ-API-00001.yaml", string(exact))

	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics for exact limit file: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 {
		t.Fatalf("exact limit file was not parsed: %+v", snapshot.Requirements)
	}
}

func TestReadDuplicateKeyPreservesInternalSpaces(t *testing.T) {
	target := t.TempDir()
	content := "\"a  b\": 1\n\"a  b\": 2\nstatus: active\n"
	writeFile(t, target, "spec/requirements/dup-spaces.yaml", content)

	_, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeParseFailed, "spec/requirements/dup-spaces.yaml") {
		t.Fatalf("missing parse_failed diagnostic: %v", diagnostics)
	}
	if !hasDiagnosticMessage(diagnostics, CodeParseFailed, "spec/requirements/dup-spaces.yaml", `duplicate YAML key "a  b"`) {
		t.Fatalf("diagnostic message collapsed spaces in key: %v", diagnostics)
	}
}

func TestReadEntryWithControlCharRejected(t *testing.T) {
	target := t.TempDir()
	reqDir := filepath.Join(target, "spec", "requirements")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := "a\nb.yaml"
	if err := os.WriteFile(filepath.Join(reqDir, filename), []byte("status: active\n"), 0o600); err != nil {
		t.Skipf("filesystem does not support newlines in filenames: %v", err)
	}

	snapshot, diagnostics := Read(target)
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("file with newline in name was parsed: %+v", snapshot.Requirements)
	}
	wantPath := path.Join("spec/requirements", strconv.Quote(filename))
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, wantPath) {
		t.Fatalf("missing unexpected_file diagnostic for %q: %v", wantPath, diagnostics)
	}
	if !hasDiagnosticMessage(diagnostics, CodeUnexpectedFile, wantPath, "has an unsafe name") {
		t.Fatalf("missing unexpected_file diagnostic message %q for %q: %v", "has an unsafe name", wantPath, diagnostics)
	}
	for _, d := range diagnostics {
		if strings.Contains(d.Path, "\n") || strings.Contains(d.Path, "\r") {
			t.Fatalf("diagnostic path contains newline: %q", d.Path)
		}
		if strings.Contains(d.Message, "\n") || strings.Contains(d.Message, "\r") {
			t.Fatalf("diagnostic message contains newline: %q", d.Message)
		}
	}
}

func TestReadFieldsMatchDecodedRecord(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", validRequirement)
	writeFile(t, target, "spec/interfaces/health-api.yaml", validInterface)

	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 || len(snapshot.Interfaces) != 1 {
		t.Fatalf("unexpected snapshot size: %+v", snapshot)
	}

	req := snapshot.Requirements[0]
	expectedReqFields := map[string]bool{
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
	}
	if len(req.Fields) != len(expectedReqFields) {
		t.Fatalf("fields count = %d, want %d: %v", len(req.Fields), len(expectedReqFields), req.Fields)
	}
	for f := range expectedReqFields {
		if !req.Fields[f] {
			t.Errorf("expected field %q not found in Fields", f)
		}
	}
	if req.Record.ID != "REQ-API-00002" || !req.Fields["id"] {
		t.Errorf("ID mismatch: record=%q fields=%v", req.Record.ID, req.Fields["id"])
	}
	if req.Record.Type != records.TypeEventDriven || !req.Fields["type"] {
		t.Errorf("Type mismatch: record=%q fields=%v", req.Record.Type, req.Fields["type"])
	}
	if req.Record.Status != records.StatusActive || !req.Fields["status"] {
		t.Errorf("Status mismatch: record=%q fields=%v", req.Record.Status, req.Fields["status"])
	}

	iface := snapshot.Interfaces[0]
	expectedIfaceFields := map[string]bool{
		"name":        true,
		"kind":        true,
		"description": true,
	}
	if len(iface.Fields) != len(expectedIfaceFields) {
		t.Fatalf("iface fields count = %d, want %d: %v", len(iface.Fields), len(expectedIfaceFields), iface.Fields)
	}
	for f := range expectedIfaceFields {
		if !iface.Fields[f] {
			t.Errorf("expected interface field %q not found in Fields", f)
		}
	}
}

func TestReadUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not portable")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read mode 000 files")
	}
	target := t.TempDir()
	rel := "spec/requirements/REQ-API-00002.yaml"
	writeFile(t, target, rel, validRequirement)
	path := filepath.Join(target, filepath.FromSlash(rel))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("filesystem allows reading mode 000 files")
	}

	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeReadFailed, rel) {
		t.Fatalf("missing unreadable-file diagnostic: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("unreadable file was parsed: %+v", snapshot.Requirements)
	}
}

func TestReadDoesNotFollowOutsideSymlink(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.yaml")
	if err := os.WriteFile(outside, []byte("id: REQ-SECRET-00001\ntype: ubiquitous\ntext: leaked\nstatus: active\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", validRequirement)
	link := filepath.Join(target, "spec", "requirements", "escape.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}

	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, "spec/requirements/escape.yaml") {
		t.Fatalf("missing symlink diagnostic: %v", diagnostics)
	}
	for _, req := range snapshot.Requirements {
		if req.Record.ID == "REQ-SECRET-00001" || strings.Contains(req.Record.Text, "leaked") {
			t.Fatalf("reader followed a symlink out of the checkout: %+v", req)
		}
	}
	if len(snapshot.Requirements) != 1 || snapshot.Requirements[0].Record.ID != "REQ-API-00002" {
		t.Fatalf("in-checkout record missing: %+v", snapshot.Requirements)
	}
}

func TestReadDoesNotFollowStoreSymlink(t *testing.T) {
	outsideDir := t.TempDir()
	writeFile(t, outsideDir, "REQ-SECRET-00001.yaml", "id: REQ-SECRET-00001\ntype: ubiquitous\ntext: leaked\nstatus: active\n")

	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(target, "spec", "requirements")); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}

	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, RequirementsDir) {
		t.Fatalf("missing store-symlink diagnostic: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("reader followed a store symlink: %+v", snapshot.Requirements)
	}
}

func TestReadIgnoresPathsOutsideSpecLayout(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-NEW-00001.yaml", `id: REQ-NEW-00001
type: ubiquitous
text: The service shall exist.
applies_to:
  scopes:
    - api
relationships: []
status: active
`)
	writeFile(t, target, ".protobot/requirements/REQ-OLD-00001.yaml", `id: REQ-OLD-00001
type: ubiquitous
text: The legacy store shall not be read.
status: active
`)
	writeFile(t, target, "spec/change-sets/CS-0001.yaml", "id: CS-0001\n")
	writeFile(t, target, "README.yaml", "name: not-an-interface\n")

	snapshot, diagnostics := Read(target)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	if len(snapshot.Requirements) != 1 || snapshot.Requirements[0].Record.ID != "REQ-NEW-00001" {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
	if len(snapshot.Interfaces) != 0 {
		t.Fatalf("interfaces=%+v", snapshot.Interfaces)
	}
}

func TestReadDeterministicOrder(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements/REQ-Z-00001.yaml", requirementYAML("REQ-Z-00001"))
	writeFile(t, target, "spec/requirements/REQ-A-00001.yaml", requirementYAML("REQ-A-00001"))
	writeFile(t, target, "spec/requirements/REQ-M-00001.yaml", requirementYAML("REQ-M-00001"))
	writeFile(t, target, "spec/interfaces/zeta.yaml", interfaceYAML("zeta"))
	writeFile(t, target, "spec/interfaces/alpha.yaml", interfaceYAML("alpha"))

	first, firstDiags := Read(target)
	second, secondDiags := Read(target)
	if len(firstDiags) != 0 || len(secondDiags) != 0 {
		t.Fatalf("diagnostics first=%v second=%v", firstDiags, secondDiags)
	}
	if got := requirementIDs(first); strings.Join(got, ",") != "REQ-A-00001,REQ-M-00001,REQ-Z-00001" {
		t.Fatalf("requirement order=%v", got)
	}
	if got := interfaceNames(first); strings.Join(got, ",") != "alpha,zeta" {
		t.Fatalf("interface order=%v", got)
	}
	if strings.Join(requirementIDs(second), ",") != strings.Join(requirementIDs(first), ",") {
		t.Fatalf("repeated read changed requirement order")
	}
	if strings.Join(interfaceNames(second), ",") != strings.Join(interfaceNames(first), ",") {
		t.Fatalf("repeated read changed interface order")
	}
}

func TestReadStorePathIsFile(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/requirements", "not a directory\n")
	snapshot, diagnostics := Read(target)
	if !hasDiagnostic(diagnostics, CodeUnexpectedFile, RequirementsDir) {
		t.Fatalf("missing file-as-store diagnostic: %v", diagnostics)
	}
	if len(snapshot.Requirements) != 0 {
		t.Fatalf("requirements=%+v", snapshot.Requirements)
	}
}

const validRequirement = `id: REQ-API-00002
type: event-driven
text: When an unauthenticated client sends GET /health, the service shall return HTTP 200.
applies_to:
  interfaces:
    - health-api
  scopes:
    - api
relationships:
  - type: related-to
    target: REQ-API-00001
status: active
`

const validInterface = `name: health-api
kind: http
description: Health check HTTP API used by unauthenticated clients.
`

func requirementYAML(id string) string {
	return "id: " + id + "\ntype: ubiquitous\ntext: The service shall exist.\nstatus: active\n"
}

func interfaceYAML(name string) string {
	return "name: " + name + "\nkind: http\ndescription: Named external interface.\n"
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

func abs(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func hasDiagnostic(diagnostics []Diagnostic, code, path string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Path == path {
			return true
		}
	}
	return false
}

func hasDiagnosticMessage(diagnostics []Diagnostic, code, path, substr string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Path == path && strings.Contains(diagnostic.Message, substr) {
			return true
		}
	}
	return false
}

func requirementIDs(snapshot Snapshot) []string {
	ids := make([]string, len(snapshot.Requirements))
	for i, document := range snapshot.Requirements {
		ids[i] = document.Record.ID
	}
	return ids
}

func interfaceNames(snapshot Snapshot) []string {
	names := make([]string, len(snapshot.Interfaces))
	for i, document := range snapshot.Interfaces {
		names[i] = document.Record.Name
	}
	return names
}
