// Package specvalidation applies deterministic ProtoBot V2 specification
// checks to records loaded from the spec/ directory layout.
package specvalidation

import (
	"sort"

	diag "github.com/redhat-et/protobot/validator/internal/diagnostics"
	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
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

func orderedRequirements(requirements []layout.Document[records.Requirement]) []layout.Document[records.Requirement] {
	ordered := append([]layout.Document[records.Requirement](nil), requirements...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Path != ordered[j].Path {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Record.ID < ordered[j].Record.ID
	})
	return ordered
}
