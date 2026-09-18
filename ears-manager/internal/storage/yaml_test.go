package storage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
)

func TestDecodeRejectsUnsafeYAML(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "malformed",
			data: "id: [unterminated\n",
		},
		{
			name: "duplicate key",
			data: "id: REQ-AUTH-00001\nid: REQ-AUTH-00002\n",
		},
		{
			name: "alias",
			data: "id: &id REQ-AUTH-00001\ntype: ubiquitous\ntext: *id\n",
		},
		{
			name: "custom tag",
			data: "id: !secret REQ-AUTH-00001\n",
		},
		{
			name: "merge key",
			data: "defaults: &defaults\n  id: REQ-AUTH-00001\n<<: *defaults\n",
		},
		{
			name: "multiple documents",
			data: "id: REQ-AUTH-00001\n---\nid: REQ-AUTH-00002\n",
		},
		{
			name: "unknown field",
			data: "id: REQ-AUTH-00001\nunknown: value\n",
		},
		{
			name: "BOM",
			data: "\ufeffid: REQ-AUTH-00001\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value records.Requirement
			if err := Decode([]byte(test.data), &value); err == nil {
				t.Fatal("Decode accepted unsafe or malformed YAML")
			}
		})
	}
}

func TestDecodeFieldsPreservesNestedFieldPresence(t *testing.T) {
	data := []byte("id: CS-00001\nverification:\n  rationale: why\n")
	var value struct {
		ID           string `yaml:"id"`
		Verification struct {
			Mode      string `yaml:"mode"`
			Rationale string `yaml:"rationale"`
		} `yaml:"verification"`
	}
	fields, err := DecodeFields(data, &value)
	if err != nil {
		t.Fatalf("DecodeFields returned error: %v", err)
	}
	for _, field := range []string{"id", "verification", "verification.rationale"} {
		if !fields[field] {
			t.Errorf("DecodeFields did not report %q", field)
		}
	}
	if fields["verification.mode"] {
		t.Fatal("DecodeFields reported omitted verification.mode")
	}
}

func TestDecodeRejectsExplicitNullValues(t *testing.T) {
	for _, data := range []string{
		"verification: null\n",
		"operations: null\n",
		"implementation_required: null\n",
	} {
		var value map[string]any
		if _, err := DecodeFields([]byte(data), &value); err == nil {
			t.Fatalf("DecodeFields accepted explicit null: %q", data)
		}
	}
}

func TestCanonicalEncodingIsStable(t *testing.T) {
	first := records.Requirement{
		ID:        "REQ-AUTH-00001",
		Type:      records.EARSEventDriven,
		Text:      "When a user logs in, the system shall issue a token.",
		AppliesTo: records.Applicability{Interfaces: []string{"api-gateway", "cli"}, Scopes: []string{"security", "authentication"}},
		Verification: records.Verification{
			Rationale: "",
		},
		Provenance: records.ProvenanceUserAuthored,
		Created:    "2026-09-15T10:00:00Z",
		Relationships: []records.Relationship{
			{Type: "related-to", Target: "REQ-Z-00001"},
			{Type: "depends-on", Target: "REQ-A-00001"},
		},
	}
	second := first
	second.AppliesTo.Interfaces = []string{"cli", "api-gateway"}
	second.AppliesTo.Scopes = []string{"authentication", "security"}
	second.Relationships = []records.Relationship{
		{Type: "depends-on", Target: "REQ-A-00001"},
		{Type: "related-to", Target: "REQ-Z-00001"},
	}
	second.Verification.Mode = records.VerificationIsolatedInterface
	second.Status = records.StatusActive

	left, err := Encode(first)
	if err != nil {
		t.Fatalf("Encode(first) returned error: %v", err)
	}
	right, err := Encode(second)
	if err != nil {
		t.Fatalf("Encode(second) returned error: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("canonical encodings differ:\n%s\n---\n%s", left, right)
	}
	if !bytes.HasPrefix(left, []byte(managedHeader)) {
		t.Fatalf("canonical encoding lacks managed header: %q", left)
	}
	if !bytes.HasSuffix(left, []byte("\n")) || bytes.HasSuffix(left, []byte("\n\n")) {
		t.Fatalf("canonical encoding has invalid final newline: %q", left)
	}
	if !strings.Contains(string(left), "status: active") {
		t.Fatalf("canonical encoding lacks default status: %q", left)
	}

	var decoded records.Requirement
	if err := Decode(left, &decoded); err != nil {
		t.Fatalf("Decode(canonical output) returned error: %v", err)
	}
	if decoded.ID != first.ID || decoded.Text != first.Text {
		t.Fatalf("decoded record = %#v, want ID %q and text %q", decoded, first.ID, first.Text)
	}
}

func TestCanonicalEncodingNormalizesAllRecordTypes(t *testing.T) {
	interfaceFirst := records.InterfaceRecord{
		ID:      "api-gateway",
		Name:    "API Gateway",
		Type:    records.InterfaceNetworkService,
		Created: "2026-09-15T10:00:00Z",
	}
	interfaceSecond := interfaceFirst
	interfaceSecond.Status = records.StatusActive
	left, err := Encode(interfaceFirst)
	if err != nil {
		t.Fatalf("Encode(interfaceFirst) returned error: %v", err)
	}
	right, err := Encode(interfaceSecond)
	if err != nil {
		t.Fatalf("Encode(interfaceSecond) returned error: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("interface records with equivalent defaults did not canonicalize identically")
	}

	changeSetFirst := records.ChangeSet{
		ID:                     "CS-00001",
		BaseCommit:             "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Intent:                 "Add requirements",
		Operations:             []records.RequirementOperation{{Action: "revise", RequirementID: "REQ-Z-00001"}, {Action: "add", RequirementID: "REQ-A-00001"}},
		AffectedInterfaces:     []string{"cli-main", "api-gateway"},
		ImplementationRequired: true,
		Created:                "2026-09-15T10:00:00Z",
	}
	changeSetSecond := changeSetFirst
	changeSetSecond.Operations = []records.RequirementOperation{{Action: "add", RequirementID: "REQ-A-00001"}, {Action: "revise", RequirementID: "REQ-Z-00001"}}
	changeSetSecond.AffectedInterfaces = []string{"api-gateway", "cli-main"}
	left, err = Encode(changeSetFirst)
	if err != nil {
		t.Fatalf("Encode(changeSetFirst) returned error: %v", err)
	}
	right, err = Encode(changeSetSecond)
	if err != nil {
		t.Fatalf("Encode(changeSetSecond) returned error: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("change-set records with reordered lists did not canonicalize identically")
	}
	collisionFirst := changeSetFirst
	collisionFirst.Operations = []records.RequirementOperation{
		{Action: "add", RequirementID: "REQ-A-00001", Rationale: "z"},
		{Action: "add", RequirementID: "REQ-A-00001", Rationale: "a"},
	}
	collisionFirst.InterfaceOperations = []records.InterfaceOperation{
		{Action: "add", InterfaceID: "api-gateway", Rationale: "z"},
		{Action: "add", InterfaceID: "api-gateway", Rationale: "a"},
	}
	collisionFirst.ArtifactOperations = []records.ArtifactOperation{
		{Action: "add", ArtifactID: "architecture", Rationale: "z"},
		{Action: "add", ArtifactID: "architecture", Rationale: "a"},
	}
	collisionFirst.ImpactAssessment = []records.ImpactAssessment{
		{RequirementID: "REQ-A-00001", Disposition: "applicable", Rationale: "z", Origin: "semantic"},
		{RequirementID: "REQ-A-00001", Disposition: "applicable", Rationale: "a", Origin: "mechanical"},
	}
	collisionSecond := collisionFirst
	collisionSecond.Operations = append([]records.RequirementOperation(nil), collisionFirst.Operations[1], collisionFirst.Operations[0])
	collisionSecond.InterfaceOperations = append([]records.InterfaceOperation(nil), collisionFirst.InterfaceOperations[1], collisionFirst.InterfaceOperations[0])
	collisionSecond.ArtifactOperations = append([]records.ArtifactOperation(nil), collisionFirst.ArtifactOperations[1], collisionFirst.ArtifactOperations[0])
	collisionSecond.ImpactAssessment = append([]records.ImpactAssessment(nil), collisionFirst.ImpactAssessment[1], collisionFirst.ImpactAssessment[0])
	left, err = Encode(collisionFirst)
	if err != nil {
		t.Fatalf("Encode(collisionFirst) returned error: %v", err)
	}
	right, err = Encode(collisionSecond)
	if err != nil {
		t.Fatalf("Encode(collisionSecond) returned error: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("change-set records with colliding primary keys did not canonicalize identically")
	}

	projectFirst := records.ProjectConfig{
		SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
		Artifacts: []records.ArtifactEntry{
			{ID: "vision", Kind: records.ArtifactVision, Path: "docs/vision.md", Digest: "sha256:e06dbbb451a2eeaa837b763f4f15e991a056fdef2c4f3aae9ee65de002c2a39f", Owner: "user"},
			{ID: "architecture", Kind: records.ArtifactArchitecture, Path: "docs/architecture.md", Digest: "sha256:e1bc4fc7df69cdced24b2a22486b16eca8b1aa4be49b3bcf1094d4cc9cd1cff5", Owner: "user"},
		},
	}
	projectSecond := projectFirst
	projectSecond.Artifacts = []records.ArtifactEntry{projectFirst.Artifacts[1], projectFirst.Artifacts[0]}
	left, err = Encode(projectFirst)
	if err != nil {
		t.Fatalf("Encode(projectFirst) returned error: %v", err)
	}
	right, err = Encode(projectSecond)
	if err != nil {
		t.Fatalf("Encode(projectSecond) returned error: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("project configs with reordered artifacts did not canonicalize identically")
	}
	projectFirst.Artifacts = []records.ArtifactEntry{
		{ID: "vision", Kind: records.ArtifactVision, Path: "docs/z.md", Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000001", Owner: "user"},
		{ID: "vision", Kind: records.ArtifactVision, Path: "docs/a.md", Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000002", Owner: "user"},
	}
	projectSecond.Artifacts = append([]records.ArtifactEntry(nil), projectFirst.Artifacts[1], projectFirst.Artifacts[0])
	left, err = Encode(projectFirst)
	if err != nil {
		t.Fatalf("Encode(projectFirst collision) returned error: %v", err)
	}
	right, err = Encode(projectSecond)
	if err != nil {
		t.Fatalf("Encode(projectSecond collision) returned error: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("project configs with colliding artifact IDs did not canonicalize identically")
	}
}
