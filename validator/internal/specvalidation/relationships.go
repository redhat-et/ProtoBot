package specvalidation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic codes for requirement relationship checks.
const (
	CodeInvalidType = "relationship.invalid_type"
	CodeCycle       = "relationship.cycle"
)

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

func validateRelationshipAcyclicity(requirements []layout.Document[records.Requirement]) []Diagnostic {
	ordered := orderedRequirements(requirements)

	paths := make(map[string]string, len(ordered))
	graphs := map[string]map[string][]string{
		records.RelDependsOn:  make(map[string][]string),
		records.RelSupersedes: make(map[string][]string),
	}

	for _, document := range ordered {
		id := document.Record.ID
		if id == "" {
			continue
		}
		if _, exists := paths[id]; !exists {
			paths[id] = document.Path
		}
		for _, relationship := range document.Record.Relationships {
			switch relationship.Type {
			case records.RelDependsOn, records.RelSupersedes:
				if relationship.Target == "" {
					continue
				}
				graphs[relationship.Type][id] = append(graphs[relationship.Type][id], relationship.Target)
			}
		}
	}

	var diagnostics []Diagnostic
	for _, relationType := range []string{records.RelDependsOn, records.RelSupersedes} {
		diagnostics = append(diagnostics, validateAcyclicGraph(relationType, graphs[relationType], paths)...)
	}
	return diagnostics
}

func validateAcyclicGraph(relationType string, graph map[string][]string, paths map[string]string) []Diagnostic {
	validator := graphValidator{
		relationType: relationType,
		graph:        graph,
		paths:        paths,
	}
	validator.run()
	return validator.diagnostics
}

type graphValidator struct {
	relationType string
	graph        map[string][]string
	paths        map[string]string
	diagnostics  []Diagnostic
}

func (v *graphValidator) run() {
	for _, component := range stronglyConnectedComponents(v.graph, v.collectVertices()) {
		if !v.componentHasCycle(component) {
			continue
		}
		v.reportCycle(component)
	}
}

func (v *graphValidator) collectVertices() []string {
	for source, targets := range v.graph {
		sort.Strings(targets)
		v.graph[source] = targets
	}

	missingTargets := make(map[string]bool)
	vertices := make([]string, 0, len(v.graph))
	for source := range v.graph {
		vertices = append(vertices, source)
		for _, target := range v.graph[source] {
			if _, exists := v.graph[target]; !exists {
				missingTargets[target] = true
			}
		}
	}
	for target := range missingTargets {
		v.graph[target] = nil
		vertices = append(vertices, target)
	}
	sort.Strings(vertices)
	return vertices
}

func (v *graphValidator) componentHasCycle(component []string) bool {
	if len(component) > 1 {
		return true
	}
	if len(component) == 0 {
		return false
	}
	vertex := component[0]
	for _, target := range v.graph[vertex] {
		if target == vertex {
			return true
		}
	}
	return false
}

func (v *graphValidator) reportCycle(component []string) {
	members := append([]string(nil), component...)
	sort.Strings(members)
	path := ""
	for _, id := range members {
		if candidate := v.paths[id]; candidate != "" {
			path = candidate
			break
		}
	}
	v.diagnostics = append(v.diagnostics, Diagnostic{
		Code:    CodeCycle,
		Path:    path,
		Field:   "relationships",
		Message: fmt.Sprintf("The %s relationship graph contains a cycle: %s.", v.relationType, strings.Join(members, ", ")),
	})
}

func stronglyConnectedComponents(graph map[string][]string, vertices []string) [][]string {
	indexOf := make(map[string]int, len(vertices))
	lowlink := make(map[string]int, len(vertices))
	onStack := make(map[string]bool, len(vertices))
	var stack []string
	var components [][]string
	index := 0

	var visit func(string)
	visit = func(vertex string) {
		indexOf[vertex] = index
		lowlink[vertex] = index
		index++
		stack = append(stack, vertex)
		onStack[vertex] = true

		for _, target := range graph[vertex] {
			if _, seen := indexOf[target]; !seen {
				visit(target)
				if lowlink[target] < lowlink[vertex] {
					lowlink[vertex] = lowlink[target]
				}
				continue
			}
			if onStack[target] && indexOf[target] < lowlink[vertex] {
				lowlink[vertex] = indexOf[target]
			}
		}

		if lowlink[vertex] != indexOf[vertex] {
			return
		}
		start := len(stack) - 1
		for start > 0 && stack[start] != vertex {
			start--
		}
		component := append([]string(nil), stack[start:]...)
		stack = stack[:start]
		for _, item := range component {
			onStack[item] = false
		}
		sort.Strings(component)
		components = append(components, component)
	}

	for _, vertex := range vertices {
		if _, seen := indexOf[vertex]; !seen {
			visit(vertex)
		}
	}
	sort.Slice(components, func(i, j int) bool {
		return components[i][0] < components[j][0]
	})
	return components
}
