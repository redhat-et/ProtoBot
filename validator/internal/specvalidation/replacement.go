package specvalidation

import (
	"fmt"

	diag "github.com/redhat-et/protobot/validator/internal/diagnostics"
	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic codes for replacement and retirement checks.
const (
	CodeSupersededRequirementActive             = "relationship.superseded_requirement_active"
	CodeSupersededRequirementNotRetiredInChange = "relationship.superseded_requirement_not_retired_in_change"
)

func validateReplacement(snapshot layout.Snapshot) []Diagnostic {
	unique := uniqueRequirements(snapshot.Requirements)
	var diagnostics []Diagnostic
	for _, document := range orderedRequirements(snapshot.Requirements) {
		for index, relationship := range document.Record.Relationships {
			if relationship.Type != records.RelSupersedes || relationship.Target == "" {
				continue
			}
			target, ok := unique[relationship.Target]
			if !ok {
				continue
			}
			if target.Record.Status == records.StatusRetired {
				continue
			}
			field := fmt.Sprintf("relationships[%d].target", index)
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeSupersededRequirementActive,
				Path:  document.Path,
				Field: field,
				Message: fmt.Sprintf(
					"Superseded requirement %q is not retired. Retire it in the same specification change.",
					relationship.Target,
				),
			})
		}
	}
	return diagnostics
}

// CompareReplacement rejects a newly introduced supersedes relationship
// whose predecessor was not retired in this specification change.
// A predecessor is retired in this change when it is retired in target
// and was not already retired in the comparison base. Historical
// supersedes edges are left to validateReplacement. Missing or duplicate
// targets are left to reference and identifier checks.
func CompareReplacement(target, base layout.Snapshot) []Diagnostic {
	uniqueTarget := uniqueRequirements(target.Requirements)
	baseByID := firstRequirementByID(base.Requirements)
	baseEdges := supersedesEdges(base.Requirements)
	var diagnostics []Diagnostic
	for _, document := range orderedRequirements(target.Requirements) {
		sourceID := document.Record.ID
		for index, relationship := range document.Record.Relationships {
			if relationship.Type != records.RelSupersedes || relationship.Target == "" {
				continue
			}
			if baseEdges[sourceID][relationship.Target] {
				continue
			}
			predecessor, ok := uniqueTarget[relationship.Target]
			if !ok {
				continue
			}
			if predecessor.Record.Status != records.StatusRetired {
				continue
			}
			if retiredInThisChange(predecessor.Record, baseByID) {
				continue
			}
			field := fmt.Sprintf("relationships[%d].target", index)
			diagnostics = append(diagnostics, Diagnostic{
				Code:  CodeSupersededRequirementNotRetiredInChange,
				Path:  document.Path,
				Field: field,
				Message: fmt.Sprintf(
					"Superseded requirement %q was not retired in this specification change.",
					relationship.Target,
				),
			})
		}
	}
	diag.Sort(diagnostics, diagnosticKey)
	return diagnostics
}

func retiredInThisChange(current records.Requirement, baseByID map[string]records.Requirement) bool {
	if current.Status != records.StatusRetired {
		return false
	}
	previous, exists := baseByID[current.ID]
	if !exists {
		return true
	}
	return previous.Status != records.StatusRetired
}

func uniqueRequirements(requirements []layout.Document[records.Requirement]) map[string]layout.Document[records.Requirement] {
	counts := make(map[string]int)
	first := make(map[string]layout.Document[records.Requirement])
	for _, document := range requirements {
		id := document.Record.ID
		if id == "" {
			continue
		}
		counts[id]++
		if _, exists := first[id]; !exists {
			first[id] = document
		}
	}
	unique := make(map[string]layout.Document[records.Requirement], len(first))
	for id, document := range first {
		if counts[id] == 1 {
			unique[id] = document
		}
	}
	return unique
}

func firstRequirementByID(requirements []layout.Document[records.Requirement]) map[string]records.Requirement {
	byID := make(map[string]records.Requirement)
	for _, document := range requirements {
		id := document.Record.ID
		if id == "" {
			continue
		}
		if _, exists := byID[id]; exists {
			continue
		}
		byID[id] = document.Record
	}
	return byID
}

func supersedesEdges(requirements []layout.Document[records.Requirement]) map[string]map[string]bool {
	edges := make(map[string]map[string]bool)
	for _, document := range requirements {
		id := document.Record.ID
		if id == "" {
			continue
		}
		for _, relationship := range document.Record.Relationships {
			if relationship.Type != records.RelSupersedes || relationship.Target == "" {
				continue
			}
			if edges[id] == nil {
				edges[id] = make(map[string]bool)
			}
			edges[id][relationship.Target] = true
		}
	}
	return edges
}
