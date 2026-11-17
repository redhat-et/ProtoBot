// Package specvalidation applies deterministic ProtoBot V2 specification
// checks to records loaded from the spec/ directory layout.
package specvalidation

import (
	diag "github.com/redhat-et/protobot/validator/internal/diagnostics"
	"github.com/redhat-et/protobot/validator/internal/layout"
)

// Diagnostic is a deterministic finding about one record in the checkout.
type Diagnostic struct {
	Code    string
	Path    string
	Field   string
	Message string
}

// Validate runs specification checks on successfully parsed records.
// Layout diagnostics remain the caller's responsibility; passing them
// suppresses cascading reference diagnostics for target records that
// failed to load. Suppression is keyed on <store>/<target>.yaml and
// assumes an interface file stem matches the declared name.
func Validate(snapshot layout.Snapshot, layoutDiagnostics ...layout.Diagnostic) []Diagnostic {
	diagnostics := validateRecordSchema(snapshot)
	diagnostics = append(diagnostics, validateRequirementIDs(snapshot.Requirements)...)
	diagnostics = append(diagnostics, validateBasenames(snapshot)...)
	diagnostics = append(diagnostics, validateRelationshipTypes(snapshot.Requirements)...)
	diagnostics = append(diagnostics, validateRelationshipAcyclicity(snapshot.Requirements)...)
	diagnostics = append(diagnostics, validateReplacement(snapshot)...)
	diagnostics = append(diagnostics, validateReferences(snapshot, layoutDiagnostics...)...)
	diag.Sort(diagnostics, diagnosticKey)
	return diagnostics
}

func diagnosticKey(d Diagnostic) diag.Key {
	return diag.Key{Path: d.Path, Code: d.Code, Field: d.Field, Message: d.Message}
}
