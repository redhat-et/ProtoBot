package specvalidation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
)

func validateRelationshipsInRecord(result *Result, document Document[records.Requirement], value records.Requirement) {
	path := safePath(document.Path)
	seen := make(map[string]bool, len(value.Relationships))
	for index, relationship := range value.Relationships {
		field := fmt.Sprintf("relationships[%d]", index)
		key := relationship.Type + "\x00" + relationship.Target
		if seen[key] {
			result.add(diagnostic("relationship.duplicate", path, value.ID, field, fmt.Sprintf("Relationship %q to %q is repeated.", relationship.Type, relationship.Target), "Keep each relationship edge once."))
		}
		seen[key] = true
		if !relationshipTypes[relationship.Type] {
			result.add(diagnostic("relationship.invalid_type", path, value.ID, field+".type", fmt.Sprintf("Unsupported relationship type %q.", relationship.Type), "Use depends-on, conflicts-with, supersedes, or related-to."))
		}
		if relationship.Target == "" {
			result.add(diagnostic("relationship.missing_target", path, value.ID, field+".target", "Relationship target is missing.", "Reference an existing requirement ID."))
			continue
		}
		if err := records.ValidateRequirementID(relationship.Target); err != nil {
			result.add(diagnostic("relationship.invalid_target", path, value.ID, field+".target", err.Error(), "Use a valid requirement ID."))
		}
	}
}

func validateRelationships(result *Result, documents []Document[records.Requirement], requirements map[string]records.Requirement) {
	ordered := sortRequirementDocuments(documents)
	paths := make(map[string]string, len(ordered))
	graphs := map[string]map[string][]string{
		relationshipDependsOn:  make(map[string][]string),
		relationshipSupersedes: make(map[string][]string),
	}

	for _, document := range ordered {
		rawValue := document.Value
		value := records.CanonicalRequirement(rawValue)
		if rawValue.ID == "" {
			continue
		}
		paths[value.ID] = safePath(document.Path)
		for index, relationship := range rawValue.Relationships {
			validateRelationshipEdge(result, document, rawValue, index, relationship, requirements)
			switch relationship.Type {
			case relationshipDependsOn, relationshipSupersedes:
				if _, exists := requirements[relationship.Target]; !exists {
					continue
				}
				graphs[relationship.Type][value.ID] = append(graphs[relationship.Type][value.ID], relationship.Target)
			}
		}
	}

	for relationType, graph := range graphs {
		validateAcyclicGraph(result, relationType, graph, paths)
	}
}

func validateRelationshipEdge(result *Result, document Document[records.Requirement], value records.Requirement, index int, relationship records.Relationship, requirements map[string]records.Requirement) {
	field := fmt.Sprintf("relationships[%d]", index)
	path := safePath(document.Path)
	target, exists := requirements[relationship.Target]
	if !exists {
		result.add(diagnostic("reference.not_found", path, value.ID, field+".target", fmt.Sprintf("Requirement %q is not registered.", relationship.Target), "Create the target requirement or remove the relationship."))
		return
	}
	if relationship.Type == relationshipConflictsWith || relationship.Type == relationshipRelatedTo {
		if !hasRelationship(target, relationship.Type, value.ID) {
			result.add(diagnostic("relationship.not_symmetric", path, value.ID, field, fmt.Sprintf("Relationship %q to %q is not declared by both requirements.", relationship.Type, relationship.Target), "Add the inverse relationship to the target requirement."))
		}
	}
	if relationship.Type == relationshipSupersedes && records.CanonicalRequirement(target).Status != records.StatusRetired {
		result.add(diagnostic("relationship.superseded_requirement_active", path, value.ID, field, fmt.Sprintf("Superseded requirement %q is not retired.", relationship.Target), "Retire the superseded requirement in this or an earlier change set."))
	}
}

func hasRelationship(value records.Requirement, relationType, target string) bool {
	for _, relationship := range value.Relationships {
		if relationship.Type == relationType && relationship.Target == target {
			return true
		}
	}
	return false
}

func validateAcyclicGraph(result *Result, relationType string, graph map[string][]string, paths map[string]string) {
	validator := graphValidator{
		result:       result,
		relationType: relationType,
		graph:        graph,
		paths:        paths,
		state:        make(map[string]int),
		reported:     make(map[string]bool),
	}
	validator.run()
}

type graphValidator struct {
	result       *Result
	relationType string
	graph        map[string][]string
	paths        map[string]string
	state        map[string]int
	stack        []string
	reported     map[string]bool
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
		if v.state[vertex] == 0 {
			v.visit(vertex)
		}
	}
}

func (v *graphValidator) visit(vertex string) {
	v.state[vertex] = 1
	v.stack = append(v.stack, vertex)
	for _, target := range v.graph[vertex] {
		if v.state[target] == 0 {
			v.visit(target)
			continue
		}
		if v.state[target] == 1 {
			v.reportCycle(vertex, target)
		}
	}
	v.stack = v.stack[:len(v.stack)-1]
	v.state[vertex] = 2
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
	v.result.add(diagnostic("relationship.cycle", v.paths[vertex], vertex, "relationships", fmt.Sprintf("The %s relationship graph contains a cycle: %s.", v.relationType, key), "Remove an edge so the directed relationship graph is acyclic."))
}
