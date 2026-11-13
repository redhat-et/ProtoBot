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

const (
	vertexUnvisited = iota
	vertexVisiting
	vertexVisited
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
	ordered := append([]layout.Document[records.Requirement](nil), requirements...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Path != ordered[j].Path {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Record.ID < ordered[j].Record.ID
	})

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
		state:        make(map[string]int),
		reported:     make(map[string]bool),
	}
	validator.run()
	return validator.diagnostics
}

type graphValidator struct {
	relationType string
	graph        map[string][]string
	paths        map[string]string
	state        map[string]int
	stack        []string
	reported     map[string]bool
	diagnostics  []Diagnostic
}

func (v *graphValidator) run() {
	for source, targets := range v.graph {
		sort.Strings(targets)
		v.graph[source] = targets
	}

	vertices := make([]string, 0, len(v.graph))
	for source := range v.graph {
		vertices = append(vertices, source)
		for _, target := range v.graph[source] {
			if _, exists := v.graph[target]; !exists {
				v.graph[target] = nil
				vertices = append(vertices, target)
			}
		}
	}
	sort.Strings(vertices)

	for _, vertex := range vertices {
		if v.state[vertex] == vertexUnvisited {
			v.visit(vertex)
		}
	}
}

func (v *graphValidator) visit(vertex string) {
	v.state[vertex] = vertexVisiting
	v.stack = append(v.stack, vertex)
	for _, target := range v.graph[vertex] {
		switch v.state[target] {
		case vertexUnvisited:
			v.visit(target)
		case vertexVisiting:
			v.reportCycle(vertex, target)
		}
	}
	v.stack = v.stack[:len(v.stack)-1]
	v.state[vertex] = vertexVisited
}

func (v *graphValidator) reportCycle(vertex, target string) {
	start := 0
	for index, item := range v.stack {
		if item == target {
			start = index
			break
		}
	}
	cycle := append([]string(nil), v.stack[start:]...)
	cycle = append(cycle, target)
	key := strings.Join(cycle, "->")
	if v.reported[key] {
		return
	}
	v.reported[key] = true
	v.diagnostics = append(v.diagnostics, Diagnostic{
		Code:    CodeCycle,
		Path:    v.paths[vertex],
		Field:   "relationships",
		Message: fmt.Sprintf("The %s relationship graph contains a cycle: %s.", v.relationType, key),
	})
}
