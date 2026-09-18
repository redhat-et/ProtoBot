package specvalidation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

func TestValidateAcceptsCompleteSnapshot(t *testing.T) {
	snapshot := validSnapshot(t)
	result := Validate(snapshot)
	if !result.Valid {
		t.Fatalf("Validate returned diagnostics: %#v", result.Diagnostics)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("valid snapshot returned diagnostics: %#v", result.Diagnostics)
	}
	if result.Err() != nil {
		t.Fatal("valid result returned an error")
	}
}

func TestValidateRejectsRequiredFieldsAndEARSForms(t *testing.T) {
	root := t.TempDir()
	missing := validRequirement("REQ-BAD-00001", records.EARSEventDriven, "When a user acts, the system shall respond.")
	missing.Verification = records.Verification{}
	result := Validate(Snapshot{
		Root: root,
		Config: records.ProjectConfig{
			Project:        records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
			SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
		},
		Requirements: []Document[records.Requirement]{
			{
				Path:  ".protobot/requirements/REQ-BAD-00001.yaml",
				Value: missing,
				Fields: map[string]bool{
					"id": true, "type": true, "text": true, "applies_to": true,
					"provenance": true, "created": true,
				},
			},
		},
	})
	if !hasDiagnostic(result, "requirement.missing_field", "verification") {
		t.Fatalf("missing verification diagnostic absent: %#v", result.Diagnostics)
	}
	if result.Err() == nil {
		t.Fatal("invalid result returned no error")
	}
	var validationError *ValidationError
	if !errors.As(result.Err(), &validationError) {
		t.Fatal("invalid result error did not preserve ValidationError type")
	}

	badPatterns := []struct {
		name string
		kind records.EARSStyle
		text string
	}{
		{name: "ubiquitous", kind: records.EARSUbiquitous, text: "When a user acts, the system shall respond."},
		{name: "event", kind: records.EARSEventDriven, text: "The system shall respond."},
		{name: "state", kind: records.EARSStateDriven, text: "When active, the system shall respond."},
		{name: "unwanted", kind: records.EARSUnwantedBehavior, text: "If active, the system shall respond."},
		{name: "optional", kind: records.EARSOptionalFeature, text: "The system shall respond."},
		{name: "complex", kind: records.EARSComplex, text: "When active, the system shall respond."},
	}
	for index, test := range badPatterns {
		requirement := validRequirement(fmtRequirementID(index+10), test.kind, test.text)
		result := Validate(Snapshot{
			Config:       records.ProjectConfig{Project: records.ProjectIdentity{ID: "fixture", Name: "Fixture"}, SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion}},
			Requirements: []Document[records.Requirement]{{Path: ".protobot/requirements/" + requirement.ID + ".yaml", Value: requirement}},
		})
		if !hasDiagnostic(result, "requirement.ears_pattern_mismatch", "text") {
			t.Errorf("%s pattern mismatch diagnostic absent: %#v", test.name, result.Diagnostics)
		}
	}
}

func TestValidateRejectsUnsupportedSchemaVersions(t *testing.T) {
	result := Validate(Snapshot{
		Config: records.ProjectConfig{
			Project:        records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
			SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion + 1, Specification: records.CurrentSpecificationSchemaVersion - 1},
		},
	})
	if !hasDiagnosticForField(result, "schema.unsupported_version", "schema_versions.project") {
		t.Fatalf("project schema diagnostic absent: %#v", result.Diagnostics)
	}
	if !hasDiagnosticForField(result, "schema.unsupported_version", "schema_versions.specification") {
		t.Fatalf("specification schema diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateRejectsReferencesSymmetryAndCycles(t *testing.T) {
	first := validRequirement("REQ-GRAPH-00001", records.EARSUbiquitous, "The system shall provide the first behavior.")
	second := validRequirement("REQ-GRAPH-00002", records.EARSUbiquitous, "The system shall provide the second behavior.")
	third := validRequirement("REQ-GRAPH-00003", records.EARSUbiquitous, "The system shall provide the third behavior.")
	fourth := validRequirement("REQ-GRAPH-00004", records.EARSUbiquitous, "The system shall provide the fourth behavior.")
	fifth := validRequirement("REQ-GRAPH-00005", records.EARSUbiquitous, "The system shall provide the fifth behavior.")
	sixth := validRequirement("REQ-GRAPH-00006", records.EARSUbiquitous, "The system shall provide the sixth behavior.")
	first.Relationships = []records.Relationship{{Type: relationshipDependsOn, Target: second.ID}}
	second.Relationships = []records.Relationship{{Type: relationshipDependsOn, Target: first.ID}}
	third.Relationships = []records.Relationship{{Type: relationshipConflictsWith, Target: fourth.ID}}
	fifth.Relationships = []records.Relationship{{Type: relationshipSupersedes, Target: sixth.ID}}
	sixth.Relationships = []records.Relationship{{Type: relationshipSupersedes, Target: fifth.ID}}
	requirements := []Document[records.Requirement]{
		{Path: ".protobot/requirements/" + first.ID + ".yaml", Value: first},
		{Path: ".protobot/requirements/" + second.ID + ".yaml", Value: second},
		{Path: ".protobot/requirements/" + third.ID + ".yaml", Value: third},
		{Path: ".protobot/requirements/" + fourth.ID + ".yaml", Value: fourth},
		{Path: ".protobot/requirements/" + fifth.ID + ".yaml", Value: fifth},
		{Path: ".protobot/requirements/" + sixth.ID + ".yaml", Value: sixth},
	}
	result := Validate(Snapshot{
		Config:       records.ProjectConfig{Project: records.ProjectIdentity{ID: "fixture", Name: "Fixture"}, SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion}},
		Requirements: requirements,
	})
	for _, code := range []string{"relationship.not_symmetric", "relationship.cycle", "relationship.superseded_requirement_active"} {
		if !hasDiagnosticCode(result, code) {
			t.Errorf("diagnostic %q absent: %#v", code, result.Diagnostics)
		}
	}
}

func TestValidateRejectsInvalidEnumsAndDanglingReferences(t *testing.T) {
	badRequirement := validRequirement("REQ-ENUM-00001", records.EARSStyle("unknown"), "The system shall respond.")
	badRequirement.Provenance = records.Provenance("unknown")
	badRequirement.AppliesTo.Interfaces = []string{"missing-interface"}
	badRequirement.Relationships = []records.Relationship{{Type: "unknown", Target: "REQ-MISSING-00001"}}
	badInterface := records.InterfaceRecord{ID: "bad-interface", Name: "Bad", Type: records.InterfaceType("unknown"), Created: "2026-09-15T10:00:00Z"}
	badChangeSet := records.ChangeSet{
		ID:                     "CS-00001",
		BaseCommit:             strings.Repeat("a", 40),
		Intent:                 "Invalid operations",
		Operations:             []records.RequirementOperation{{Action: "unknown", RequirementID: "REQ-MISSING-00001"}},
		AffectedInterfaces:     []string{"missing-interface"},
		ImplementationRequired: true,
		Created:                "2026-09-15T10:00:00Z",
	}
	result := Validate(Snapshot{
		Config:       records.ProjectConfig{Project: records.ProjectIdentity{ID: "fixture", Name: "Fixture"}, SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion}},
		Interfaces:   []Document[records.InterfaceRecord]{{Path: ".protobot/interfaces/bad-interface.yaml", Value: badInterface}},
		Requirements: []Document[records.Requirement]{{Path: ".protobot/requirements/" + badRequirement.ID + ".yaml", Value: badRequirement}},
		ChangeSets:   []Document[records.ChangeSet]{{Path: ".protobot/change-sets/cs-00001.yaml", Value: badChangeSet}},
	})
	for _, code := range []string{
		"requirement.invalid_type",
		"requirement.invalid_provenance",
		"interface.invalid_type",
		"relationship.invalid_type",
		"reference.not_found",
		"change_set.invalid_operation",
	} {
		if !hasDiagnosticCode(result, code) {
			t.Errorf("diagnostic %q absent: %#v", code, result.Diagnostics)
		}
	}
}

func TestValidateRejectsInvalidArtifactsAndNormalizesLineEndings(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docs/good.md", "# Good\r\n")
	writeTestFile(t, root, "docs/mismatch.md", "# Mismatch\n")
	if err := os.MkdirAll(filepath.Join(root, "docs", "directory"), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	goodDigest, err := CanonicalTextDigest([]byte("# Good\n"))
	if err != nil {
		t.Fatalf("CanonicalTextDigest returned error: %v", err)
	}
	artifacts := []records.ArtifactEntry{
		{ID: "good", Kind: records.ArtifactVision, Path: "docs/good.md", Digest: goodDigest, Owner: "user", Validator: "markdownlint"},
		{ID: "bad-digest", Kind: records.ArtifactVision, Path: "docs/mismatch.md", Digest: "sha256:example", Owner: "user"},
		{ID: "mismatch", Kind: records.ArtifactVision, Path: "docs/mismatch.md", Digest: "sha256:" + strings.Repeat("0", 64), Owner: "user"},
		{ID: "bad-owner", Kind: records.ArtifactVision, Path: "docs/good.md", Digest: goodDigest, Owner: "kit"},
		{ID: "bad-validator", Kind: records.ArtifactVision, Path: "docs/good.md", Digest: goodDigest, Owner: "user", Validator: "protoc"},
		{ID: "bad-kind", Kind: records.ArtifactKind("requirement-store"), Path: "docs/good.md", Digest: goodDigest, Owner: "user"},
		{ID: "directory", Kind: records.ArtifactVision, Path: "docs/directory", Digest: goodDigest, Owner: "user"},
		{ID: "outside", Kind: records.ArtifactVision, Path: "../outside.md", Digest: goodDigest, Owner: "user"},
		{ID: "windows-path", Kind: records.ArtifactVision, Path: `docs\bad.md`, Digest: goodDigest, Owner: "user"},
	}
	config := validSnapshot(t).Config
	config.Artifacts = artifacts
	result := Validate(Snapshot{Root: root, Config: config})
	for _, code := range []string{
		"artifact.invalid_digest",
		"artifact.invalid_owner",
		"artifact.validator_incompatible",
		"artifact.unknown_kind",
		"artifact.invalid_path",
	} {
		if !hasDiagnosticCode(result, code) {
			t.Errorf("diagnostic %q absent: %#v", code, result.Diagnostics)
		}
	}
	if hasDiagnosticFor(result, "artifact.digest_mismatch", "good") {
		t.Fatal("CRLF-only representation change produced a digest mismatch")
	}
	if !hasDiagnosticFor(result, "artifact.digest_mismatch", "mismatch") {
		t.Fatalf("content digest mismatch diagnostic absent: %#v", result.Diagnostics)
	}
	lfDigest, err := CanonicalTextDigest([]byte("# Good\n"))
	if err != nil {
		t.Fatalf("CanonicalTextDigest(LF) returned error: %v", err)
	}
	crlfDigest, err := CanonicalTextDigest([]byte("# Good\r\n"))
	if err != nil {
		t.Fatalf("CanonicalTextDigest(CRLF) returned error: %v", err)
	}
	if lfDigest != crlfDigest {
		t.Fatalf("line-ending normalization differs: LF=%q CRLF=%q", lfDigest, crlfDigest)
	}
}

func TestValidateDiagnosticsAreDeterministic(t *testing.T) {
	first := validRequirement("REQ-DET-00001", records.EARSEventDriven, "The system shall respond.")
	second := validRequirement("REQ-DET-00002", records.EARSUbiquitous, "The system shall respond.")
	one := Snapshot{
		Config:       records.ProjectConfig{Project: records.ProjectIdentity{ID: "fixture", Name: "Fixture"}, SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion}},
		Requirements: []Document[records.Requirement]{{Path: "b.yaml", Value: first}, {Path: "a.yaml", Value: second}},
	}
	two := one
	two.Requirements = []Document[records.Requirement]{{Path: "a.yaml", Value: second}, {Path: "b.yaml", Value: first}}
	left, err := json.Marshal(Validate(one))
	if err != nil {
		t.Fatalf("Marshal(left) returned error: %v", err)
	}
	right, err := json.Marshal(Validate(two))
	if err != nil {
		t.Fatalf("Marshal(right) returned error: %v", err)
	}
	if string(left) != string(right) {
		t.Fatalf("diagnostics are not deterministic:\n%s\n---\n%s", left, right)
	}
}

func TestValidateDoesNotMutateSnapshot(t *testing.T) {
	snapshot := validSnapshot(t)
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal(before) returned error: %v", err)
	}
	_ = Validate(snapshot)
	after, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal(after) returned error: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("Validate mutated the supplied snapshot")
	}
}

func TestValidateArtifactDiagnosticsUseConfiguredProjectPath(t *testing.T) {
	digest := "sha256:" + strings.Repeat("0", 64)
	result := Validate(Snapshot{
		ConfigPath: "control/project.yaml",
		Config: records.ProjectConfig{
			Project:        records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
			SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
			Artifacts: []records.ArtifactEntry{
				{ID: "vision", Kind: records.ArtifactVision, Path: "docs/vision.md", Digest: digest, Owner: "user"},
				{ID: "vision", Kind: records.ArtifactVision, Path: "docs/other.md", Digest: digest, Owner: "user"},
			},
		},
	})
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "artifact.duplicate_id" {
			if diagnostic.Path != "control/project.yaml" {
				t.Fatalf("duplicate artifact path = %q, want control/project.yaml", diagnostic.Path)
			}
			return
		}
	}
	t.Fatalf("duplicate artifact diagnostic absent: %#v", result.Diagnostics)
}

func TestValidateRejectsReservedPathsCaseInsensitively(t *testing.T) {
	result := Validate(Snapshot{
		Config: records.ProjectConfig{
			Project:        records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
			SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
			Stores:         records.StorePaths{Requirements: ".protobot/project.yaml"},
			Artifacts: []records.ArtifactEntry{{
				ID: "reserved", Kind: records.ArtifactVision, Path: "vendor/.Git/config", Digest: "sha256:" + strings.Repeat("0", 64), Owner: "user",
			}},
		},
	})
	if !hasDiagnosticForField(result, "project.invalid_path", "stores.requirements") {
		t.Fatalf("reserved store path diagnostic absent: %#v", result.Diagnostics)
	}
	if !hasDiagnosticForField(result, "artifact.invalid_path", "artifacts[id=reserved].path") {
		t.Fatalf("reserved artifact path diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateRejectsArtifactsInsideStructuredStores(t *testing.T) {
	config := validSnapshot(t).Config
	config.Stores = records.StorePaths{Requirements: "records/requirements", Interfaces: "records/interfaces", ChangeSets: "records/change-sets"}
	config.Artifacts = []records.ArtifactEntry{{
		ID: "store-record", Kind: records.ArtifactVision, Path: "records/requirements/REQ-A-00001.yaml", Digest: "sha256:" + strings.Repeat("0", 64), Owner: "user",
	}}
	result := Validate(Snapshot{Config: config})
	if !hasDiagnosticForField(result, "artifact.invalid_path", "artifacts[id=store-record].path") {
		t.Fatalf("structured-store artifact path diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateRejectsCaseFoldedArtifactAliases(t *testing.T) {
	config := validSnapshot(t).Config
	config.Artifacts = []records.ArtifactEntry{
		{ID: "api-upper", Kind: records.ArtifactVision, Path: "docs/API.yaml", Digest: "sha256:" + strings.Repeat("0", 64), Owner: "user"},
		{ID: "api-lower", Kind: records.ArtifactVision, Path: "docs/api.yaml", Digest: "sha256:" + strings.Repeat("0", 64), Owner: "user"},
	}
	result := Validate(Snapshot{Config: config})
	if !hasDiagnosticCode(result, "artifact.duplicate_path") {
		t.Fatalf("case-folded artifact alias diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateRejectsInvalidRepositoryConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*records.RepositoryConfig)
		code  string
		field string
	}{
		{
			name: "credentials",
			setup: func(repository *records.RepositoryConfig) {
				user, credential := "alice", "review-only-value"
				repository.CanonicalRemote = "https://" + user + ":" + credential + "@example.com/protobot.git"
			},
			code:  "project.remote_credentials",
			field: "repository.canonical_remote",
		},
		{
			name: "review mode",
			setup: func(repository *records.RepositoryConfig) {
				repository.ReviewMode = "unknown"
			},
			code:  "project.invalid_configuration",
			field: "repository.review_mode",
		},
		{
			name: "remote query",
			setup: func(repository *records.RepositoryConfig) {
				repository.CanonicalRemote = "https://example.com/protobot.git?token=not-a-credential"
			},
			code:  "project.invalid_configuration",
			field: "repository.canonical_remote",
		},
		{
			name: "remote fragment",
			setup: func(repository *records.RepositoryConfig) {
				repository.CanonicalRemote = "https://example.com/protobot.git#default"
			},
			code:  "project.invalid_configuration",
			field: "repository.canonical_remote",
		},
		{
			name: "default branch",
			setup: func(repository *records.RepositoryConfig) {
				repository.DefaultBranch = "main..broken"
			},
			code:  "project.invalid_configuration",
			field: "repository.default_branch",
		},
		{
			name: "reserved prefix",
			setup: func(repository *records.RepositoryConfig) {
				repository.BranchPrefix = "wi/"
			},
			code:  "project.invalid_configuration",
			field: "repository.branch_prefix",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := records.RepositoryConfig{
				CanonicalRemote: "https://example.com/protobot.git",
				DefaultBranch:   "main",
				ReviewMode:      "single-player",
				BranchPrefix:    "cs/",
			}
			test.setup(&repository)
			config := validSnapshot(t).Config
			config.Repository = repository
			result := Validate(Snapshot{Config: config})
			if !hasDiagnosticForField(result, test.code, test.field) {
				t.Fatalf("repository diagnostic absent: %#v", result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Code == test.code && diagnostic.Field == test.field && strings.Contains(diagnostic.Message, "review-only-value") {
					t.Fatal("repository diagnostic exposed credential text")
				}
			}
		})
	}
}

func TestValidateRequiresCompleteMechanicalImpactAssessment(t *testing.T) {
	snapshot := validSnapshot(t)
	snapshot.ChangeSets[0].Value.ImpactAssessment = nil
	result := Validate(snapshot)
	if !hasDiagnosticForField(result, "change_set.missing_field", "impact_assessment") {
		t.Fatalf("incomplete impact assessment diagnostic absent: %#v", result.Diagnostics)
	}

	snapshot = validSnapshot(t)
	snapshot.Requirements[1].Value.Status = records.StatusRetired
	result = Validate(snapshot)
	if !hasDiagnosticCode(result, "change_set.invalid_impact") {
		t.Fatalf("retired impact target diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateRequiresRetireOperationToBeApplied(t *testing.T) {
	snapshot := validSnapshot(t)
	snapshot.ChangeSets[0].Value.Operations = []records.RequirementOperation{{Action: "retire", RequirementID: "REQ-A-00001"}}
	result := Validate(snapshot)
	if !hasDiagnosticForField(result, "change_set.invalid_operation", "operations[0].requirement_id") {
		t.Fatalf("active retire target diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateRejectsFractionalCreatedTimestamp(t *testing.T) {
	requirement := validRequirement("REQ-TIME-00001", records.EARSUbiquitous, "The system shall provide an exact timestamp.")
	requirement.Created = "2026-09-15T10:00:00.123Z"
	result := Validate(Snapshot{Config: records.ProjectConfig{
		Project:        records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
		SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
	}, Requirements: []Document[records.Requirement]{{
		Path:  ".protobot/requirements/" + requirement.ID + ".yaml",
		Value: requirement,
	}}})
	if !hasDiagnosticForField(result, "record.invalid_timestamp", "created") {
		t.Fatalf("fractional timestamp diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateProjectLoadsPresenceAwareRecords(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{
		".protobot/requirements",
		".protobot/interfaces",
		".protobot/change-sets",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) returned error: %v", directory, err)
		}
	}
	config := records.ProjectConfig{
		Project:        records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
		SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
	}
	writeYAML(t, filepath.Join(root, ".protobot", "project.yaml"), config)
	interfaceRecord := records.InterfaceRecord{
		ID: "api-gateway", Name: "API Gateway", Type: records.InterfaceNetworkService, Created: "2026-09-15T10:00:00Z",
	}
	writeYAML(t, filepath.Join(root, ".protobot", "interfaces", "api-gateway.yaml"), interfaceRecord)
	requirement := validRequirement("REQ-LOAD-00001", records.EARSUbiquitous, "The system shall provide a loaded requirement.")
	writeYAML(t, filepath.Join(root, ".protobot", "requirements", requirement.ID+".yaml"), requirement)
	changeSet := []byte("id: CS-00001\nbase_commit: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nintent: Documentation-only change\noperations: []\naffected_interfaces: []\nimplementation_rationale: Documentation only\ncreated: \"2026-09-15T10:00:00Z\"\n")
	if err := os.WriteFile(filepath.Join(root, ".protobot", "change-sets", "cs-00001.yaml"), changeSet, 0o644); err != nil {
		t.Fatalf("WriteFile(change set) returned error: %v", err)
	}

	result := ValidateProject(root)
	if !hasDiagnostic(result, "change_set.missing_field", "implementation_required") {
		t.Fatalf("presence-aware missing field diagnostic absent: %#v", result.Diagnostics)
	}
}

func TestValidateProjectRejectsSymlinkedControlNamespace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".protobot")); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}
	result := ValidateProject(root)
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "storage.decode_failed" && diagnostic.Path == ".protobot" {
			return
		}
	}
	t.Fatalf("symlinked control namespace was not rejected: %#v", result.Diagnostics)
}

func TestValidateProjectRejectsReservedStoreBeforeReading(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".protobot", "policy.yaml"), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".protobot", "policy.yaml", "bad.yaml"), []byte("id: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	config := records.ProjectConfig{
		Project: records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
		Repository: records.RepositoryConfig{
			CanonicalRemote: "https://example.com/protobot.git",
			DefaultBranch:   "main",
			ReviewMode:      "single-player",
			BranchPrefix:    "cs/",
		},
		SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
		Stores:         records.StorePaths{Requirements: ".protobot/policy.yaml"},
	}
	if err := os.MkdirAll(filepath.Join(root, ".protobot"), 0o755); err != nil {
		t.Fatalf("MkdirAll control namespace returned error: %v", err)
	}
	writeYAML(t, filepath.Join(root, ".protobot", "project.yaml"), config)

	result := ValidateProject(root)
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "storage.decode_failed" && diagnostic.Path == ".protobot/policy.yaml" {
			return
		}
	}
	t.Fatalf("reserved store was not rejected before reading: %#v", result.Diagnostics)
}

func TestValidateProjectReportsStableLoadCause(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{".protobot/requirements", ".protobot/interfaces", ".protobot/change-sets"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) returned error: %v", directory, err)
		}
	}
	config := validSnapshot(t).Config
	writeYAML(t, filepath.Join(root, ".protobot", "project.yaml"), config)
	if err := os.WriteFile(filepath.Join(root, ".protobot", "requirements", "bad.yaml"), []byte("id: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	result := ValidateProject(root)
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "storage.decode_failed" {
			if !strings.Contains(diagnostic.Message, "YAML decoding failed") {
				t.Fatalf("load diagnostic = %q, want stable YAML cause", diagnostic.Message)
			}
			if strings.Contains(diagnostic.Message, root) {
				t.Fatalf("load diagnostic exposed absolute root: %q", diagnostic.Message)
			}
			return
		}
	}
	t.Fatalf("stable load diagnostic absent: %#v", result.Diagnostics)
}

func TestValidateProjectRejectsNestedStoreDirectories(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{".protobot/requirements/nested", ".protobot/interfaces", ".protobot/change-sets"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) returned error: %v", directory, err)
		}
	}
	config := validSnapshot(t).Config
	writeYAML(t, filepath.Join(root, ".protobot", "project.yaml"), config)

	result := ValidateProject(root)
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "storage.decode_failed" && diagnostic.Path == ".protobot/requirements/nested" {
			return
		}
	}
	t.Fatalf("nested store directory was ignored: %#v", result.Diagnostics)
}

func validSnapshot(t *testing.T) Snapshot {
	t.Helper()
	root := t.TempDir()
	artifacts := []records.ArtifactEntry{
		writeArtifact(t, root, "vision", records.ArtifactVision, "docs/vision.md", "# Vision\n", "markdownlint"),
		writeArtifact(t, root, "architecture", records.ArtifactArchitecture, "docs/architecture.md", "# Architecture\n", "markdownlint"),
		writeArtifact(t, root, "api-prose", records.ArtifactInterfaceProse, "docs/api.md", "# API\n", "markdownlint"),
		writeArtifact(t, root, "api-openapi", records.ArtifactInterfaceIDL, "specs/api.yaml", "openapi: 3.1.0\n", "openapi-lint"),
	}
	first := validRequirement("REQ-A-00001", records.EARSUbiquitous, "The system shall provide authentication.")
	second := validRequirement("REQ-B-00001", records.EARSEventDriven, "When a user logs in, the system shall issue a token.")
	third := validRequirement("REQ-C-00001", records.EARSStateDriven, "While maintenance mode is active, the system shall reject writes.")
	fourth := validRequirement("REQ-D-00001", records.EARSUnwantedBehavior, "If a token expires, then the system shall reject the request.")
	fifth := validRequirement("REQ-E-00001", records.EARSOptionalFeature, "Where caching is enabled, the system shall use a cache.")
	sixth := validRequirement("REQ-F-00001", records.EARSComplex, "While maintenance mode is active, when a user writes, the system shall reject the request.")
	first.Relationships = []records.Relationship{{Type: relationshipDependsOn, Target: second.ID}}
	third.Relationships = []records.Relationship{{Type: relationshipConflictsWith, Target: fourth.ID}}
	fourth.Relationships = []records.Relationship{{Type: relationshipConflictsWith, Target: third.ID}}
	fifth.Relationships = []records.Relationship{{Type: relationshipSupersedes, Target: sixth.ID}}
	sixth.Status = records.StatusRetired
	config := records.ProjectConfig{
		Project: records.ProjectIdentity{ID: "fixture", Name: "Fixture"},
		Repository: records.RepositoryConfig{
			CanonicalRemote: "https://example.com/protobot.git",
			DefaultBranch:   "main",
			ReviewMode:      "single-player",
			BranchPrefix:    "cs/",
		},
		SchemaVersions: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion},
	}
	config.Stores = records.StorePaths{Requirements: ".protobot/requirements", Interfaces: ".protobot/interfaces", ChangeSets: ".protobot/change-sets"}
	config.Artifacts = artifacts
	return Snapshot{
		Root:       root,
		ConfigPath: ".protobot/project.yaml",
		Config:     config,
		Interfaces: []Document[records.InterfaceRecord]{
			{Path: ".protobot/interfaces/api-gateway.yaml", Value: records.InterfaceRecord{ID: "api-gateway", Name: "API Gateway", Type: records.InterfaceNetworkService, Created: "2026-09-15T10:00:00Z"}},
		},
		Requirements: []Document[records.Requirement]{
			{Path: ".protobot/requirements/" + first.ID + ".yaml", Value: first},
			{Path: ".protobot/requirements/" + second.ID + ".yaml", Value: second},
			{Path: ".protobot/requirements/" + third.ID + ".yaml", Value: third},
			{Path: ".protobot/requirements/" + fourth.ID + ".yaml", Value: fourth},
			{Path: ".protobot/requirements/" + fifth.ID + ".yaml", Value: fifth},
			{Path: ".protobot/requirements/" + sixth.ID + ".yaml", Value: sixth},
		},
		ChangeSets: []Document[records.ChangeSet]{
			{Path: ".protobot/change-sets/cs-00001.yaml", Value: records.ChangeSet{
				ID:                     "CS-00001",
				BaseCommit:             strings.Repeat("a", 40),
				Intent:                 "Add authentication requirements",
				Operations:             []records.RequirementOperation{{Action: "add", RequirementID: first.ID}},
				AffectedInterfaces:     []string{"api-gateway"},
				ImplementationRequired: true,
				ImpactAssessment: []records.ImpactAssessment{
					{RequirementID: second.ID, Disposition: "applicable", Rationale: "The login behavior is part of the changed interface.", Origin: "mechanical"},
					{RequirementID: third.ID, Disposition: "not-applicable", Rationale: "Maintenance-mode behavior is unrelated to authentication.", Origin: "mechanical"},
					{RequirementID: fourth.ID, Disposition: "not-applicable", Rationale: "Token expiry behavior is unrelated to this change.", Origin: "mechanical"},
					{RequirementID: fifth.ID, Disposition: "not-applicable", Rationale: "Caching behavior is unrelated to authentication.", Origin: "mechanical"},
				},
				Created: "2026-09-15T10:00:00Z",
			}},
		},
	}
}

func validRequirement(id string, kind records.EARSStyle, text string) records.Requirement {
	return records.Requirement{
		ID:           id,
		Type:         kind,
		Text:         text,
		AppliesTo:    records.Applicability{Interfaces: []string{"api-gateway"}},
		Verification: records.Verification{Mode: records.VerificationIsolatedInterface},
		Provenance:   records.ProvenanceUserAuthored,
		Created:      "2026-09-15T10:00:00Z",
		Status:       records.StatusActive,
	}
}

func writeArtifact(t *testing.T, root, id string, kind records.ArtifactKind, relativePath, content, validator string) records.ArtifactEntry {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) returned error: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) returned error: %v", path, err)
	}
	digest, err := CanonicalTextDigest([]byte(content))
	if err != nil {
		t.Fatalf("CanonicalTextDigest(%q) returned error: %v", relativePath, err)
	}
	return records.ArtifactEntry{ID: id, Kind: kind, Path: relativePath, Digest: digest, Owner: "user", Validator: validator}
}

func writeTestFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) returned error: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) returned error: %v", path, err)
	}
}

func writeYAML(t *testing.T, path string, value any) {
	t.Helper()
	data, err := storage.Encode(value)
	if err != nil {
		t.Fatalf("Encode(%q) returned error: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) returned error: %v", path, err)
	}
}

func hasDiagnostic(result Result, code, field string) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code && diagnostic.Field == field {
			return true
		}
	}
	return false
}

func hasDiagnosticCode(result Result, code string) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func hasDiagnosticFor(result Result, code, recordID string) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code && diagnostic.RecordID == recordID {
			return true
		}
	}
	return false
}

func hasDiagnosticForField(result Result, code, field string) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code && diagnostic.Field == field {
			return true
		}
	}
	return false
}

func fmtRequirementID(number int) string {
	return fmt.Sprintf("REQ-BAD-%05d", number)
}
