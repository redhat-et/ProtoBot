// Package specvalidation applies deterministic ProtoBot V2 specification
// checks to records loaded from the spec/ directory layout.
package specvalidation

import (
	"sort"

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
// Layout diagnostics remain the caller's responsibility.
func Validate(snapshot layout.Snapshot) []Diagnostic {
	diagnostics := validateRelationshipTypes(snapshot.Requirements)
	diagnostics = append(diagnostics, validateRelationshipAcyclicity(snapshot.Requirements)...)
	sortDiagnostics(diagnostics)
	return diagnostics
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Field != right.Field {
			return left.Field < right.Field
		}
		return left.Message < right.Message
	})
}
