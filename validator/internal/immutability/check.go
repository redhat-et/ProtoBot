// Package immutability checks that parsed requirement text is unchanged
// for IDs that already exist in the comparison base.
package immutability

import (
	"fmt"
	"sort"

	"github.com/redhat-et/protobot/validator/internal/layout"
)

// CodeTextImmutable is reported when an existing requirement ID has a
// different parsed text value than the comparison base.
const CodeTextImmutable = "requirement.text_immutable"

// Check compares parsed requirement text for each ID present in both
// snapshots. New IDs are ignored. Comparison uses the decoded string,
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
	sortDiagnostics(diagnostics)
	return diagnostics
}

func sortDiagnostics(diagnostics []layout.Diagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}
