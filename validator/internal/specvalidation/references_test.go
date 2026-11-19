package specvalidation

import (
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
	"github.com/redhat-et/protobot/validator/internal/testutil"
)

func TestValidateResolvesCompleteSpecification(t *testing.T) {
	snapshot := layout.Snapshot{
		Interfaces: []layout.Document[records.InterfaceRecord]{
			testutil.ValidInterfaceDocument(),
		},
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00001"),
				testutil.WithStatus(records.StatusRetired),
			),
			testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00003"),
			),
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"health-api"},
					Scopes:     []string{"api"},
				}),
				testutil.WithRelationships(
					records.Relationship{Type: records.RelDependsOn, Target: "REQ-API-00001"},
					records.Relationship{Type: records.RelRelatedTo, Target: "REQ-API-00003"},
				),
			),
		},
	}
	if diagnostics := Validate(snapshot); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none", diagnostics)
	}
}

func TestValidateResolvesRetiredRelationshipTarget(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00000"),
				testutil.WithStatus(records.StatusRetired),
			),
			testutil.ValidRequirementDocument(
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelSupersedes,
					Target: "REQ-API-00000",
				}),
			),
		},
	}
	if diagnostics := Validate(snapshot); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none", diagnostics)
	}
}

func TestValidateRejectsDanglingInterfaceReference(t *testing.T) {
	const path = "spec/requirements/REQ-API-00002.yaml"
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"undeclared-api"},
				}),
			),
		},
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeReferenceNotFound {
		t.Errorf("code=%q, want %q", got.Code, CodeReferenceNotFound)
	}
	if got.Path != path {
		t.Errorf("path=%q, want %q", got.Path, path)
	}
	if got.Field != "applies_to.interfaces[0]" {
		t.Errorf("field=%q, want applies_to.interfaces[0]", got.Field)
	}
	if !identifiesType(got.Message, "undeclared-api") {
		t.Errorf("message %q does not identify undeclared-api", got.Message)
	}
}

func TestValidateRejectsDanglingRelationshipTarget(t *testing.T) {
	const path = "spec/requirements/REQ-API-00002.yaml"
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelDependsOn,
					Target: "REQ-MISSING-00001",
				}),
			),
		},
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeReferenceNotFound {
		t.Errorf("code=%q, want %q", got.Code, CodeReferenceNotFound)
	}
	if got.Path != path {
		t.Errorf("path=%q, want %q", got.Path, path)
	}
	if got.Field != "relationships[0].target" {
		t.Errorf("field=%q, want relationships[0].target", got.Field)
	}
	if !identifiesType(got.Message, "REQ-MISSING-00001") {
		t.Errorf("message %q does not identify REQ-MISSING-00001", got.Message)
	}
}

func TestValidateRejectsEmptyInterfaceAndTargetReferences(t *testing.T) {
	document := testutil.ValidRequirementDocument(
		testutil.WithAppliesTo(records.Applicability{
			Interfaces: []string{""},
			Scopes:     []string{"api"},
		}),
		testutil.WithRelationships(records.Relationship{
			Type:   records.RelRelatedTo,
			Target: "",
		}),
	)
	snapshot := layout.Snapshot{
		Interfaces:   []layout.Document[records.InterfaceRecord]{testutil.ValidInterfaceDocument()},
		Requirements: []layout.Document[records.Requirement]{document},
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2", diagnostics)
	}
	if diagnostics[0].Field != "applies_to.interfaces[0]" {
		t.Errorf("first field=%q, want applies_to.interfaces[0]", diagnostics[0].Field)
	}
	if diagnostics[1].Field != "relationships[0].target" {
		t.Errorf("second field=%q, want relationships[0].target", diagnostics[1].Field)
	}
	for _, got := range diagnostics {
		if got.Code != CodeRequirementMissingField {
			t.Errorf("code=%q, want %q", got.Code, CodeRequirementMissingField)
		}
		if got.Path != document.Path {
			t.Errorf("path=%q, want %q", got.Path, document.Path)
		}
		if !strings.Contains(got.Message, got.Field) {
			t.Errorf("message %q does not identify field %q", got.Message, got.Field)
		}
	}
}

func TestValidateResolvesInterfaceByNameNotFilename(t *testing.T) {
	snapshot := layout.Snapshot{
		Interfaces: []layout.Document[records.InterfaceRecord]{
			testutil.ValidInterfaceDocument(
				testutil.WithName("health-api"),
				testutil.WithPath("spec/interfaces/foo.yaml"),
			),
		},
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"health-api"},
				}),
			),
		},
	}
	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	if diagnostics[0].Code != CodeInterfaceNameBasenameMismatch {
		t.Errorf("code=%q, want %q", diagnostics[0].Code, CodeInterfaceNameBasenameMismatch)
	}
	if diagnostics[0].Path != "spec/interfaces/foo.yaml" {
		t.Errorf("path=%q, want spec/interfaces/foo.yaml", diagnostics[0].Path)
	}

	snapshot.Requirements[0].Record.AppliesTo.Interfaces = []string{"foo"}
	diagnostics = Validate(snapshot)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2", diagnostics)
	}
	if diagnostics[0].Code != CodeInterfaceNameBasenameMismatch || diagnostics[0].Path != "spec/interfaces/foo.yaml" {
		t.Errorf("diagnostic[0]=%+v, want %s for spec/interfaces/foo.yaml", diagnostics[0], CodeInterfaceNameBasenameMismatch)
	}
	if diagnostics[1].Code != CodeReferenceNotFound {
		t.Errorf("diagnostic[1].code=%q, want %q", diagnostics[1].Code, CodeReferenceNotFound)
	}
	if diagnostics[1].Field != "applies_to.interfaces[0]" || !identifiesType(diagnostics[1].Message, "foo") {
		t.Fatalf("diagnostic[1]=%+v", diagnostics[1])
	}
}

func TestValidateInterfaceReferenceIsCaseSensitive(t *testing.T) {
	snapshot := layout.Snapshot{
		Interfaces: []layout.Document[records.InterfaceRecord]{
			testutil.ValidInterfaceDocument(),
		},
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"Health-api"},
				}),
			),
		},
	}
	diagnostics := Validate(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	if !identifiesType(diagnostics[0].Message, "Health-api") {
		t.Errorf("message %q does not identify Health-api", diagnostics[0].Message)
	}
}

func TestValidateReferenceResolutionIgnoresScopes(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Scopes: []string{"undeclared-scope"},
				}),
			),
		},
	}
	if diagnostics := Validate(snapshot); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%v, want none", diagnostics)
	}
}

func TestValidateReferenceResolutionIsDeterministic(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"undeclared-api", "also-missing"},
					Scopes:     []string{"api"},
				}),
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelDependsOn,
					Target: "REQ-MISSING-00001",
				}),
			),
			testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00001"),
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelRelatedTo,
					Target: "REQ-MISSING-00002",
				}),
			),
		},
	}

	first := Validate(snapshot)
	second := Validate(snapshot)
	if len(first) != 4 {
		t.Fatalf("diagnostics=%v, want 4", first)
	}
	if len(first) != len(second) {
		t.Fatalf("repeated validation changed diagnostic count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("repeated validation changed diagnostics: %+v vs %+v", first, second)
		}
	}
	if first[0].Path != "spec/requirements/REQ-API-00001.yaml" {
		t.Errorf("first path=%q, want REQ-API-00001.yaml", first[0].Path)
	}
	if first[1].Field != "applies_to.interfaces[0]" || first[2].Field != "applies_to.interfaces[1]" {
		t.Errorf("interface fields=%q, %q", first[1].Field, first[2].Field)
	}
	if first[3].Field != "relationships[0].target" {
		t.Errorf("last field=%q, want relationships[0].target", first[3].Field)
	}
}

func TestValidateReferenceResolutionAfterLayoutRead(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "spec/interfaces/health-api.yaml", testutil.ValidInterfaceYAML())
	writeFile(t, target, "spec/requirements/REQ-API-00001.yaml", testutil.ValidRequirementYAML(
		testutil.WithID("REQ-API-00001"),
		testutil.WithStatus(records.StatusRetired),
	))
	writeFile(t, target, "spec/requirements/REQ-API-00002.yaml", testutil.ValidRequirementYAML(
		testutil.WithAppliesTo(records.Applicability{
			Interfaces: []string{"health-api", "undeclared-api"},
		}),
		testutil.WithRelationships(
			records.Relationship{Type: records.RelDependsOn, Target: "REQ-API-00001"},
			records.Relationship{Type: records.RelRelatedTo, Target: "REQ-MISSING-00001"},
		),
	))

	snapshot, layoutDiagnostics := layout.Read(target)
	if len(layoutDiagnostics) != 0 {
		t.Fatalf("layout diagnostics=%v", layoutDiagnostics)
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2", diagnostics)
	}
	if diagnostics[0].Code != CodeReferenceNotFound || diagnostics[0].Field != "applies_to.interfaces[1]" {
		t.Errorf("first diagnostic=%+v", diagnostics[0])
	}
	if !identifiesType(diagnostics[0].Message, "undeclared-api") {
		t.Errorf("first message %q omitted undeclared-api", diagnostics[0].Message)
	}
	if diagnostics[1].Code != CodeReferenceNotFound || diagnostics[1].Field != "relationships[1].target" {
		t.Errorf("second diagnostic=%+v", diagnostics[1])
	}
	if !identifiesType(diagnostics[1].Message, "REQ-MISSING-00001") {
		t.Errorf("second message %q omitted REQ-MISSING-00001", diagnostics[1].Message)
	}
}

func TestValidateSuppressesCascadingReferenceDiagnosticsOnParseFailure(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"health-api", "genuinely-missing-api"},
				}),
				testutil.WithRelationships(
					records.Relationship{Type: records.RelDependsOn, Target: "REQ-API-00001"},
					records.Relationship{Type: records.RelRelatedTo, Target: "REQ-API-00099"},
				),
			),
		},
	}
	layoutDiagnostics := []layout.Diagnostic{
		{
			Code:    layout.CodeParseFailed,
			Path:    "spec/requirements/REQ-API-00001.yaml",
			Message: "parse YAML: yaml: line 1: did not find expected node content",
		},
		{
			Code:    layout.CodeParseFailed,
			Path:    "spec/interfaces/health-api.yaml",
			Message: "parse YAML: yaml: line 1: did not find expected node content",
		},
	}

	diagnostics := Validate(snapshot, layoutDiagnostics...)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics=%v, want 2 (only genuinely missing targets)", diagnostics)
	}
	if diagnostics[0].Field != "applies_to.interfaces[1]" || !identifiesType(diagnostics[0].Message, "genuinely-missing-api") {
		t.Errorf("first diagnostic=%+v", diagnostics[0])
	}
	if diagnostics[1].Field != "relationships[1].target" || !identifiesType(diagnostics[1].Message, "REQ-API-00099") {
		t.Errorf("second diagnostic=%+v", diagnostics[1])
	}
}

func TestValidateCrossStoreParseFailureDoesNotSuppressReferenceDiagnostic(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"health-api"},
				}),
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelDependsOn,
					Target: "REQ-API-00001",
				}),
			),
		},
	}
	layoutDiagnostics := []layout.Diagnostic{
		{
			Code:    layout.CodeParseFailed,
			Path:    "spec/requirements/health-api.yaml",
			Message: "parse YAML: yaml: line 1: did not find expected node content",
		},
		{
			Code:    layout.CodeParseFailed,
			Path:    "spec/requirements/REQ-API-00001.yaml",
			Message: "parse YAML: yaml: line 1: did not find expected node content",
		},
	}

	diagnostics := Validate(snapshot, layoutDiagnostics...)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics=%v, want 1", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != CodeReferenceNotFound {
		t.Errorf("code=%q, want %q", got.Code, CodeReferenceNotFound)
	}
	if got.Field != "applies_to.interfaces[0]" {
		t.Errorf("field=%q, want applies_to.interfaces[0]", got.Field)
	}
	if !identifiesType(got.Message, "health-api") {
		t.Errorf("message %q does not identify health-api", got.Message)
	}
}

func TestValidateRejectsAmbiguousInterfaceReference(t *testing.T) {
	snapshot := layout.Snapshot{
		Interfaces: []layout.Document[records.InterfaceRecord]{
			testutil.ValidInterfaceDocument(
				testutil.WithName("health-api"),
				testutil.WithPath("spec/interfaces/api-a.yaml"),
			),
			testutil.ValidInterfaceDocument(
				testutil.WithName("health-api"),
				testutil.WithPath("spec/interfaces/api-b.yaml"),
			),
		},
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"health-api"},
				}),
			),
		},
	}

	diagnostics := Validate(snapshot)
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics=%v, want 3", diagnostics)
	}
	if diagnostics[0].Code != CodeInterfaceNameBasenameMismatch || diagnostics[0].Path != "spec/interfaces/api-a.yaml" {
		t.Errorf("diagnostic[0]=%+v, want %s for spec/interfaces/api-a.yaml", diagnostics[0], CodeInterfaceNameBasenameMismatch)
	}
	if diagnostics[1].Code != CodeInterfaceNameBasenameMismatch || diagnostics[1].Path != "spec/interfaces/api-b.yaml" {
		t.Errorf("diagnostic[1]=%+v, want %s for spec/interfaces/api-b.yaml", diagnostics[1], CodeInterfaceNameBasenameMismatch)
	}
	got := diagnostics[2]
	if got.Code != CodeReferenceAmbiguous {
		t.Errorf("code=%q, want %q", got.Code, CodeReferenceAmbiguous)
	}
	if got.Field != "applies_to.interfaces[0]" {
		t.Errorf("field=%q, want applies_to.interfaces[0]", got.Field)
	}
	if !identifiesType(got.Message, "health-api") || !identifiesType(got.Message, "ambiguous") {
		t.Errorf("message %q does not identify ambiguous health-api", got.Message)
	}
}

func TestValidateDuplicateRequirementIDDoesNotEmitReferenceAmbiguous(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00001"),
				testutil.WithPath("spec/requirements/REQ-API-00001-a.yaml"),
			),
			testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00001"),
				testutil.WithPath("spec/requirements/REQ-API-00001-b.yaml"),
				testutil.WithStatus(records.StatusRetired),
			),
			testutil.ValidRequirementDocument(
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelDependsOn,
					Target: "REQ-API-00001",
				}),
			),
		},
	}

	diagnostics := Validate(snapshot)
	var sawDuplicate bool
	for _, d := range diagnostics {
		if d.Code == CodeReferenceAmbiguous {
			t.Fatalf("diagnostics=%v, did not want %s for a duplicate requirement ID", diagnostics, CodeReferenceAmbiguous)
		}
		if d.Code == CodeDuplicateID {
			sawDuplicate = true
		}
	}
	if !sawDuplicate {
		t.Fatalf("diagnostics=%v, want %s", diagnostics, CodeDuplicateID)
	}
}

func TestValidatePathTraversalDoesNotSuppressReferenceNotFound(t *testing.T) {
	const reqPath = "spec/requirements/REQ-API-00002.yaml"
	const traversal = "../../spec/requirements/REQ-BROKEN"
	const backslash = `..\..\spec\requirements\REQ-BROKEN`
	layoutDiagnostics := []layout.Diagnostic{{
		Code:    layout.CodeParseFailed,
		Path:    "spec/requirements/REQ-BROKEN.yaml",
		Message: "parse YAML: failed",
	}}

	cases := []struct {
		name  string
		req   records.Requirement
		field string
		ident string
	}{
		{
			name: "interface slash traversal",
			req: testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00002"),
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{traversal},
				}),
			).Record,
			field: "applies_to.interfaces[0]",
			ident: traversal,
		},
		{
			name: "interface backslash traversal",
			req: testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00002"),
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{backslash},
				}),
			).Record,
			field: "applies_to.interfaces[0]",
			ident: "REQ-BROKEN",
		},
		{
			name: "relationship slash traversal",
			req: testutil.ValidRequirementDocument(
				testutil.WithID("REQ-API-00002"),
				testutil.WithRelationships(records.Relationship{
					Type:   records.RelDependsOn,
					Target: traversal,
				}),
			).Record,
			field: "relationships[0].target",
			ident: traversal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := layout.Snapshot{
				Requirements: []layout.Document[records.Requirement]{{
					Path:   reqPath,
					Record: tc.req,
				}},
			}
			diagnostics := Validate(snapshot, layoutDiagnostics...)
			if len(diagnostics) != 1 || diagnostics[0].Code != CodeReferenceNotFound {
				t.Fatalf("expected reference.not_found diagnostic, got %+v", diagnostics)
			}
			got := diagnostics[0]
			if got.Path != reqPath {
				t.Errorf("path=%q, want %q", got.Path, reqPath)
			}
			if got.Field != tc.field {
				t.Errorf("field=%q, want %q", got.Field, tc.field)
			}
			if !identifiesType(got.Message, tc.ident) {
				t.Errorf("message %q does not identify %q", got.Message, tc.ident)
			}
		})
	}
}

func TestValidateInterfaceNameMismatchParseFailureSuppression(t *testing.T) {
	const path = "spec/requirements/REQ-API-00002.yaml"
	// Option B: suppression keys off <stem>.yaml, so a parse failure of
	// spec/interfaces/api.yaml does not suppress a reference to health-api.
	// Basename matching (#463) enforces stem == name on successfully parsed
	// interface records.
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"health-api"},
				}),
			),
		},
	}
	layoutDiagnostics := []layout.Diagnostic{{
		Code:    layout.CodeParseFailed,
		Path:    "spec/interfaces/api.yaml",
		Message: "parse YAML: failed",
	}}

	diagnostics := Validate(snapshot, layoutDiagnostics...)
	if len(diagnostics) != 1 || diagnostics[0].Code != CodeReferenceNotFound {
		t.Fatalf("expected reference.not_found when interface stem differs from name, got %+v", diagnostics)
	}
	got := diagnostics[0]
	if got.Path != path {
		t.Errorf("path=%q, want %q", got.Path, path)
	}
	if got.Field != "applies_to.interfaces[0]" {
		t.Errorf("field=%q, want applies_to.interfaces[0]", got.Field)
	}
	if !identifiesType(got.Message, "health-api") {
		t.Errorf("message %q does not identify health-api", got.Message)
	}
}

func TestValidateInterfaceWithDotDotSuppressesReferenceNotFound(t *testing.T) {
	snapshot := layout.Snapshot{
		Requirements: []layout.Document[records.Requirement]{
			testutil.ValidRequirementDocument(
				testutil.WithAppliesTo(records.Applicability{
					Interfaces: []string{"service..api"},
				}),
			),
		},
	}
	layoutDiagnostics := []layout.Diagnostic{{
		Code:    layout.CodeParseFailed,
		Path:    "spec/interfaces/service..api.yaml",
		Message: "parse YAML: failed",
	}}

	diagnostics := Validate(snapshot, layoutDiagnostics...)
	if len(diagnostics) != 0 {
		t.Fatalf("expected reference.not_found to be suppressed for service..api, got %+v", diagnostics)
	}
}
