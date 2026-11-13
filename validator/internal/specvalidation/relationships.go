package specvalidation

import (
	"fmt"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic code for an unsupported requirement relationship type.
const CodeInvalidType = "relationship.invalid_type"

var allowedRelationshipTypes = map[string]bool{
	records.RelDependsOn:  true,
	records.RelSupersedes: true,
	records.RelRelatedTo:  true,
}

func validateRelationshipTypes(requirements []layout.Document[records.Requirement]) []Diagnostic {
	var diagnostics []Diagnostic
	for _, document := range requirements {
		for index, relationship := range document.Record.Relationships {
			if allowedRelationshipTypes[relationship.Type] {
				continue
			}
			field := fmt.Sprintf("relationships[%d].type", index)
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeInvalidType,
				Path:  document.Path,
				Field: field,
				Message: fmt.Sprintf(
					"Unsupported relationship type %q at %s. Use depends-on, supersedes, or related-to.",
					relationship.Type,
					field,
				),
			})
		}
	}
	return diagnostics
}
