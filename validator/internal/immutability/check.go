// Package immutability checks that parsed requirement text is unchanged
// for IDs that already exist in the comparison base.
package immutability

import (
	"fmt"

	diag "github.com/redhat-et/protobot/validator/internal/diagnostics"
	"github.com/redhat-et/protobot/validator/internal/layout"
)

// CodeTextImmutable is reported when an existing requirement ID has a
// different parsed text value than the comparison base.
const CodeTextImmutable = "requirement.text_immutable"

// Check compares parsed requirement text for each ID present in both
// snapshots. When the comparison base contains multiple records with the
// same requirement ID, the first record encountered is used (first-wins).
// Duplicate identifier detection is deferred to follow-on identity
// validation. New IDs are ignored. Comparison uses the decoded string,
// so YAML scalar reformatting that preserves the parsed value passes.
func Check(target, base layout.Snapshot) []layout.Diagnostic {
	baseText := make(map[string]string, len(base.Requirements))
	for _, document := range base.Requirements {
		id := document.Record.ID
		if id == "" {
			continue
		}
		if _, exists := baseText[id]; exists {
			continue
		}
		baseText[id] = document.Record.Text
	}

	var diagnostics []layout.Diagnostic
	for _, document := range target.Requirements {
		id := document.Record.ID
		if id == "" {
			continue
		}
		previous, exists := baseText[id]
		if !exists {
			continue
		}
		if document.Record.Text == previous {
			continue
		}
		diagnostics = append(diagnostics, layout.Diagnostic{
			Code:    CodeTextImmutable,
			Path:    document.Path,
			Message: fmt.Sprintf("parsed text for %q differs from the comparison base", id),
		})
	}
	diag.Sort(diagnostics, diagnosticKey)
	return diagnostics
}

func diagnosticKey(d layout.Diagnostic) diag.Key {
	return diag.Key{Path: d.Path, Code: d.Code, Message: d.Message}
}
