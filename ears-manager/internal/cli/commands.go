package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
)

func dispatch(args []string, stdin io.Reader) (any, Mutation, *commandFailure) {
	if len(args) == 0 {
		return nil, Mutation{}, usageFailure("a command is required")
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:])
	case "requirement":
		if len(args) < 2 {
			return nil, Mutation{}, usageFailure("a requirement subcommand is required")
		}
		switch args[1] {
		case "add":
			return runRequirementAdd(args[2:])
		case "list":
			return runRequirementList(args[2:])
		case "show":
			return runRequirementShow(args[2:])
		case "update":
			return runRequirementUpdate(args[2:])
		case "retire":
			return runRequirementRetire(args[2:])
		default:
			return nil, Mutation{}, usageFailure(fmt.Sprintf("unsupported requirement subcommand %q", args[1]))
		}
	case "interface":
		if len(args) < 2 {
			return nil, Mutation{}, usageFailure("an interface subcommand is required")
		}
		switch args[1] {
		case "add":
			return runInterfaceAdd(args[2:])
		case "list":
			return runInterfaceList(args[2:])
		case "show":
			return runInterfaceShow(args[2:])
		default:
			return nil, Mutation{}, usageFailure(fmt.Sprintf("unsupported interface subcommand %q", args[1]))
		}
	case "artifact":
		if len(args) < 2 {
			return nil, Mutation{}, usageFailure("an artifact subcommand is required")
		}
		switch args[1] {
		case "list":
			return runArtifactList(args[2:])
		case "get":
			return runArtifactGet(args[2:])
		case "put":
			return runArtifactPut(args[2:], stdin)
		default:
			return nil, Mutation{}, usageFailure(fmt.Sprintf("unsupported artifact subcommand %q", args[1]))
		}
	case "change-set":
		if len(args) < 2 {
			return nil, Mutation{}, usageFailure("a change-set subcommand is required")
		}
		switch args[1] {
		case "create":
			return runChangeSetCreate(args[2:])
		case "list":
			return runChangeSetList(args[2:])
		case "show":
			return runChangeSetShow(args[2:])
		case "update":
			return runChangeSetUpdate(args[2:], stdin)
		case "compare":
			return runChangeSetCompare(args[2:])
		default:
			return nil, Mutation{}, usageFailure(fmt.Sprintf("unsupported change-set subcommand %q", args[1]))
		}
	case "impact":
		return runImpact(args[1:])
	case "project":
		if len(args) < 2 {
			return nil, Mutation{}, usageFailure("a project subcommand is required")
		}
		switch args[1] {
		case "init":
			return runProjectInit(args[2:])
		default:
			return nil, Mutation{}, usageFailure(fmt.Sprintf("unsupported project subcommand %q", args[1]))
		}
	default:
		return nil, Mutation{}, usageFailure(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func valueOptions(names ...string) map[string]optionSpec {
	result := make(map[string]optionSpec, len(names))
	for _, name := range names {
		result[name] = optionSpec{takesValue: true}
	}
	return result
}

func runCheck(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	var state projectState
	if parsed.has("change-set") {
		state, failure = loadState()
		if failure != nil {
			return nil, Mutation{}, failure
		}
		index, _, exists := findChangeSet(state.snapshot, parsed.one("change-set"))
		if !exists {
			return nil, Mutation{}, validationFailure("change_set.not_found", fmt.Sprintf("Change set %s was not found.", parsed.one("change-set")), nil)
		}
		if failure := validateScopedCheck(state.root, state.snapshot, index); failure != nil {
			return nil, Mutation{}, failure
		}
	} else {
		state, failure = checkState()
		if failure != nil {
			return nil, Mutation{}, failure
		}
	}
	data := checkData{
		Valid:        true,
		CheckedPaths: checkedPaths(state.snapshot),
		RecordCounts: recordCounts{
			Requirements: len(state.snapshot.Requirements),
			Interfaces:   len(state.snapshot.Interfaces),
			ChangeSets:   len(state.snapshot.ChangeSets),
			Artifacts:    len(state.snapshot.Config.Artifacts),
		},
	}
	return data, Mutation{diagnostics: append([]specvalidation.Diagnostic(nil), state.diagnostics...)}, nil
}

type checkData struct {
	Valid        bool         `json:"valid"`
	CheckedPaths []string     `json:"checked_paths"`
	RecordCounts recordCounts `json:"record_counts"`
}

type recordCounts struct {
	Requirements int `json:"requirements"`
	Interfaces   int `json:"interfaces"`
	ChangeSets   int `json:"change_sets"`
	Artifacts    int `json:"artifacts"`
}

func checkedPaths(snapshot specvalidation.Snapshot) []string {
	paths := []string{snapshot.ConfigPath}
	for _, document := range snapshot.Requirements {
		paths = append(paths, document.Path)
	}
	for _, document := range snapshot.Interfaces {
		paths = append(paths, document.Path)
	}
	for _, document := range snapshot.ChangeSets {
		paths = append(paths, document.Path)
	}
	for _, artifact := range snapshot.Config.Artifacts {
		paths = append(paths, artifact.Path)
	}
	if snapshot.Projection != nil && snapshot.Projection.Present {
		paths = append(paths, specvalidation.ProjectionPath)
	}
	sort.Strings(paths)
	return uniqueStrings(paths)
}

func runRequirementAdd(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions(
		"change-set", "id", "type", "text", "interface", "scope", "verification-mode",
		"verification-rationale", "provenance", "created", "relationship",
	))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	changeSetID, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateRequirementID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("requirement.invalid_id", "Requirement", id)
	}
	requirementType, failure := requireOption(parsed, "type")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	text, failure := requireOption(parsed, "text")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	verificationMode, failure := requireOption(parsed, "verification-mode")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	provenance, failure := requireOption(parsed, "provenance")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	created, failure := requireOption(parsed, "created")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	relationships, failure := parseRelationships(parsed.list("relationship"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if _, _, exists := findRequirement(state.snapshot, id); exists {
		return nil, Mutation{}, conflictFailure("requirement.duplicate_id", fmt.Sprintf("Requirement %s already exists.", id), nil)
	}
	changeSetIndex, changeSet, failure := proposedChangeSet(state, changeSetID)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	operation, failure := requirementOperation(&changeSet, "add", id)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	requirement := records.Requirement{
		ID:   id,
		Type: records.EARSStyle(requirementType),
		Text: text,
		AppliesTo: records.Applicability{
			Interfaces: parsed.list("interface"),
			Scopes:     parsed.list("scope"),
		},
		Verification: records.Verification{
			Mode:      records.VerificationMode(verificationMode),
			Rationale: parsed.one("verification-rationale"),
		},
		Provenance:    records.Provenance(provenance),
		Created:       created,
		Relationships: relationships,
		Status:        records.StatusActive,
	}
	staged := cloneSnapshot(state.snapshot)
	requirementPath, err := upsertRequirement(&staged, requirement)
	if err != nil {
		return nil, Mutation{}, internalFailure("the requirement path could not be determined")
	}
	targetIndexes, failure := synchronizeSymmetricRelationships(&staged, &changeSet, id, records.Requirement{}, requirement)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	staged.ChangeSets[changeSetIndex].Value = changeSet
	changeSetPath := staged.ChangeSets[changeSetIndex].Path
	if changeSetPath == "" {
		changeSetPath, err = upsertChangeSet(&staged, changeSet)
		if err != nil {
			return nil, Mutation{}, internalFailure("the change-set path could not be determined")
		}
	}
	if failure := validateCandidateForChangeSet(staged, changeSetID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	if err := addRequirementWrites(&writes, &staged, requirementPath, requirement, targetIndexes); err != nil {
		return nil, Mutation{}, internalFailure("the requirement could not be serialized")
	}
	if err := addWrite(&writes, changeSetPath, changeSet); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, changeSetID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return requirementMutationData{Requirement: requirementJSONPointer(toRequirementJSON(requirement)), Operation: toRequirementOperationJSON(operation)}, mutation, nil
}

type requirementMutationData struct {
	Requirement *requirementJSON         `json:"requirement,omitempty"`
	Before      *requirementJSON         `json:"before,omitempty"`
	After       *requirementJSON         `json:"after,omitempty"`
	Operation   requirementOperationJSON `json:"operation"`
}

func requirementJSONPointer(value requirementJSON) *requirementJSON {
	return &value
}

func runRequirementList(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("interface", "scope", "type", "status", "relationship"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if parsed.has("interface") {
		if err := records.ValidateInterfaceID(parsed.one("interface")); err != nil {
			return nil, Mutation{}, invalidIDFailure("interface.invalid_id", "Interface", parsed.one("interface"))
		}
	}
	if parsed.has("type") && !validEARSStyle(parsed.one("type")) {
		return nil, Mutation{}, validationFailure("requirement.invalid_type", fmt.Sprintf("Unsupported EARS pattern %q.", parsed.one("type")), nil)
	}
	if parsed.has("status") && !validRecordStatus(parsed.one("status")) {
		return nil, Mutation{}, validationFailure("requirement.invalid_status", fmt.Sprintf("Unsupported record status %q.", parsed.one("status")), nil)
	}
	if parsed.has("relationship") && !validRelationshipType(parsed.one("relationship")) {
		return nil, Mutation{}, validationFailure("relationship.invalid_type", fmt.Sprintf("Unsupported relationship type %q.", parsed.one("relationship")), nil)
	}
	state, failure := loadReadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	items := make([]requirementJSON, 0)
	for _, document := range state.snapshot.Requirements {
		value := records.CanonicalRequirement(document.Value)
		if parsed.has("interface") && !slices.Contains(value.AppliesTo.Interfaces, parsed.one("interface")) {
			continue
		}
		if parsed.has("scope") && !slices.Contains(value.AppliesTo.Scopes, parsed.one("scope")) {
			continue
		}
		if parsed.has("type") && string(value.Type) != parsed.one("type") {
			continue
		}
		if parsed.has("status") && string(value.Status) != parsed.one("status") {
			continue
		}
		if parsed.has("relationship") && !hasRelationship(value.Relationships, parsed.one("relationship")) {
			continue
		}
		items = append(items, toRequirementJSON(value))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return struct {
		Requirements []requirementJSON `json:"requirements"`
	}{Requirements: items}, Mutation{}, nil
}

func runRequirementShow(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("id"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateRequirementID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("requirement.invalid_id", "Requirement", id)
	}
	state, failure := loadReadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	_, value, exists := findRequirement(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, validationFailure("requirement.not_found", fmt.Sprintf("Requirement %s was not found.", id), nil)
	}
	return struct {
		Requirement requirementJSON `json:"requirement"`
	}{Requirement: toRequirementJSON(value)}, Mutation{}, nil
}

func runRequirementUpdate(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions(
		"change-set", "id", "type", "text", "interface", "scope", "verification-mode",
		"verification-rationale", "provenance", "relationship", "status",
	))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	changeSetID, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateRequirementID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("requirement.invalid_id", "Requirement", id)
	}
	if !hasRecordUpdate(parsed) {
		return nil, Mutation{}, usageFailure("requirement update requires at least one record field")
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	requirementIndex, current, exists := findRequirement(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, validationFailure("requirement.not_found", fmt.Sprintf("Requirement %s was not found.", id), nil)
	}
	changeSetIndex, changeSet, failure := proposedChangeSet(state, changeSetID)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	before := records.CanonicalRequirement(current)
	updated := cloneRequirement(before)
	if parsed.has("type") {
		updated.Type = records.EARSStyle(parsed.one("type"))
	}
	if parsed.has("text") {
		updated.Text = parsed.one("text")
	}
	if parsed.has("interface") {
		updated.AppliesTo.Interfaces = parsed.list("interface")
	}
	if parsed.has("scope") {
		updated.AppliesTo.Scopes = parsed.list("scope")
	}
	if parsed.has("verification-mode") {
		updated.Verification.Mode = records.VerificationMode(parsed.one("verification-mode"))
	}
	if parsed.has("verification-rationale") {
		updated.Verification.Rationale = parsed.one("verification-rationale")
	}
	if parsed.has("provenance") {
		updated.Provenance = records.Provenance(parsed.one("provenance"))
	}
	if parsed.has("relationship") {
		updated.Relationships, failure = parseRelationships(parsed.list("relationship"))
		if failure != nil {
			return nil, Mutation{}, failure
		}
	}
	if parsed.has("status") {
		requestedStatus := records.RecordStatus(parsed.one("status"))
		if requestedStatus == records.StatusRetired && records.CanonicalRequirement(current).Status != records.StatusRetired {
			return nil, Mutation{}, validationFailure("requirement.use_retire", "Active requirements must be retired with requirement retire.", nil)
		}
		if requestedStatus == records.StatusActive && records.CanonicalRequirement(current).Status == records.StatusRetired {
			return nil, Mutation{}, conflictFailure("requirement.already_retired", "Retired requirements cannot be reactivated.", nil)
		}
		if !validRecordStatus(string(requestedStatus)) {
			return nil, Mutation{}, validationFailure("requirement.invalid_status", fmt.Sprintf("Unsupported record status %q.", requestedStatus), nil)
		}
		updated.Status = requestedStatus
	}
	operation, failure := requirementOperation(&changeSet, "revise", id)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	staged := cloneSnapshot(state.snapshot)
	staged.Requirements[requirementIndex].Value = updated
	targetIndexes, failure := synchronizeSymmetricRelationships(&staged, &changeSet, id, before, updated)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	staged.ChangeSets[changeSetIndex].Value = changeSet
	if failure := validateCandidateForChangeSet(staged, changeSetID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	requirementPath := staged.Requirements[requirementIndex].Path
	changeSetPath := staged.ChangeSets[changeSetIndex].Path
	writes := []fileWrite{}
	if err := addRequirementWrites(&writes, &staged, requirementPath, updated, targetIndexes); err != nil {
		return nil, Mutation{}, internalFailure("the requirement could not be serialized")
	}
	if err := addWrite(&writes, changeSetPath, changeSet); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, changeSetID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return requirementMutationData{Before: requirementJSONPointer(toRequirementJSON(before)), After: requirementJSONPointer(toRequirementJSON(updated)), Operation: toRequirementOperationJSON(operation)}, mutation, nil
}

func runRequirementRetire(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set", "id"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	changeSetID, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateRequirementID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("requirement.invalid_id", "Requirement", id)
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	requirementIndex, current, exists := findRequirement(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, validationFailure("requirement.not_found", fmt.Sprintf("Requirement %s was not found.", id), nil)
	}
	current = records.CanonicalRequirement(current)
	if current.Status == records.StatusRetired {
		return nil, Mutation{}, conflictFailure("requirement.already_retired", fmt.Sprintf("Requirement %s is already retired.", id), nil)
	}
	before := current
	changeSetIndex, changeSet, failure := proposedChangeSet(state, changeSetID)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	operation, failure := requirementOperation(&changeSet, "retire", id)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	current.Status = records.StatusRetired
	staged := cloneSnapshot(state.snapshot)
	staged.Requirements[requirementIndex].Value = current
	staged.ChangeSets[changeSetIndex].Value = changeSet
	if failure := validateCandidateForChangeSet(staged, changeSetID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	if err := addWrite(&writes, staged.Requirements[requirementIndex].Path, current); err != nil {
		return nil, Mutation{}, internalFailure("the retired requirement could not be serialized")
	}
	if err := addWrite(&writes, staged.ChangeSets[changeSetIndex].Path, changeSet); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, changeSetID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return requirementMutationData{Before: requirementJSONPointer(toRequirementJSON(before)), After: requirementJSONPointer(toRequirementJSON(current)), Operation: toRequirementOperationJSON(operation)}, mutation, nil
}

func hasRecordUpdate(parsed options) bool {
	for _, name := range []string{"type", "text", "interface", "scope", "verification-mode", "verification-rationale", "provenance", "relationship", "status"} {
		if parsed.has(name) {
			return true
		}
	}
	return false
}

func parseRelationships(values []string) ([]records.Relationship, *commandFailure) {
	if values == nil {
		return nil, nil
	}
	result := make([]records.Relationship, 0, len(values))
	for _, value := range values {
		separator := strings.IndexByte(value, '=')
		if separator <= 0 || separator == len(value)-1 {
			return nil, usageFailure(fmt.Sprintf("relationship %q must use TYPE=REQ-ID", value))
		}
		result = append(result, records.Relationship{Type: value[:separator], Target: value[separator+1:]})
	}
	return result, nil
}

type symmetricRelationship struct {
	typeName string
	target   string
}

func synchronizeSymmetricRelationships(snapshot *specvalidation.Snapshot, changeSet *records.ChangeSet, sourceID string, before, after records.Requirement) ([]int, *commandFailure) {
	beforeEdges := symmetricRelationships(before.Relationships)
	afterEdges := symmetricRelationships(after.Relationships)
	edges := make(map[symmetricRelationship]bool, len(beforeEdges)+len(afterEdges))
	for edge := range beforeEdges {
		edges[edge] = true
	}
	for edge := range afterEdges {
		edges[edge] = true
	}
	edgeList := make([]symmetricRelationship, 0, len(edges))
	for edge := range edges {
		edgeList = append(edgeList, edge)
	}
	sort.Slice(edgeList, func(i, j int) bool {
		if edgeList[i].typeName == edgeList[j].typeName {
			return edgeList[i].target < edgeList[j].target
		}
		return edgeList[i].typeName < edgeList[j].typeName
	})

	changed := make([]int, 0, len(edges))
	seen := make(map[int]bool)
	for _, edge := range edgeList {
		if edge.target == sourceID || records.ValidateRequirementID(edge.target) != nil {
			continue
		}
		targetIndex, target, exists := findRequirement(*snapshot, edge.target)
		if !exists {
			continue
		}
		target = cloneRequirement(target)
		wantInverse := afterEdges[edge]
		relationships, modified := setSymmetricInverse(target.Relationships, edge.typeName, sourceID, wantInverse)
		if !modified {
			continue
		}
		target.Relationships = relationships
		snapshot.Requirements[targetIndex].Value = target
		if _, failure := requirementOperation(changeSet, "revise", target.ID); failure != nil {
			return nil, failure
		}
		if !seen[targetIndex] {
			changed = append(changed, targetIndex)
			seen[targetIndex] = true
		}
	}
	sort.Ints(changed)
	return changed, nil
}

func symmetricRelationships(values []records.Relationship) map[symmetricRelationship]bool {
	result := make(map[symmetricRelationship]bool)
	for _, relationship := range values {
		if relationship.Type == "conflicts-with" || relationship.Type == "related-to" {
			result[symmetricRelationship{typeName: relationship.Type, target: relationship.Target}] = true
		}
	}
	return result
}

func setSymmetricInverse(values []records.Relationship, typeName, target string, wanted bool) ([]records.Relationship, bool) {
	result := make([]records.Relationship, 0, len(values)+1)
	found := false
	modified := false
	for _, relationship := range values {
		if relationship.Type == typeName && relationship.Target == target {
			found = true
			if !wanted {
				modified = true
				continue
			}
		}
		result = append(result, relationship)
	}
	if wanted && !found {
		result = append(result, records.Relationship{Type: typeName, Target: target})
		modified = true
	}
	return result, modified
}

func hasRelationship(values []records.Relationship, relationshipType string) bool {
	return slices.ContainsFunc(values, func(relationship records.Relationship) bool {
		return relationship.Type == relationshipType
	})
}

func requirementOperation(changeSet *records.ChangeSet, action, id string) (records.RequirementOperation, *commandFailure) {
	for index, operation := range changeSet.Operations {
		if operation.RequirementID != id {
			continue
		}
		if operation.Action == "add" && action == "revise" {
			return operation, nil
		}
		if operation.Action == action {
			return operation, nil
		}
		if operation.Action == "revise" && action == "retire" {
			operation.Action = action
			changeSet.Operations[index] = operation
			return operation, nil
		}
		if operation.Action == "add" && action == "retire" {
			operation.Action = action
			changeSet.Operations[index] = operation
			return operation, nil
		}
		return records.RequirementOperation{}, conflictFailure("change_set.duplicate_operation", fmt.Sprintf("Requirement %s is already present in the change set.", id), nil)
	}
	operation := records.RequirementOperation{Action: action, RequirementID: id}
	changeSet.Operations = append(changeSet.Operations, operation)
	return operation, nil
}

func runInterfaceAdd(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set", "id", "name", "type", "created", "spec-approach", "description"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	changeSetID, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateInterfaceID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("interface.invalid_id", "Interface", id)
	}
	name, failure := requireOption(parsed, "name")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	interfaceType, failure := requireOption(parsed, "type")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	created, failure := requireOption(parsed, "created")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if _, _, exists := findInterface(state.snapshot, id); exists {
		return nil, Mutation{}, conflictFailure("interface.duplicate_id", fmt.Sprintf("Interface %s already exists.", id), nil)
	}
	changeSetIndex, changeSet, failure := proposedChangeSet(state, changeSetID)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	operation, failure := interfaceOperation(&changeSet, "add", id)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	value := records.InterfaceRecord{
		ID:           id,
		Name:         name,
		Type:         records.InterfaceType(interfaceType),
		SpecApproach: parsed.one("spec-approach"),
		Description:  parsed.one("description"),
		Created:      created,
		Status:       records.StatusActive,
	}
	staged := cloneSnapshot(state.snapshot)
	interfacePath, err := upsertInterface(&staged, value)
	if err != nil {
		return nil, Mutation{}, internalFailure("the interface path could not be determined")
	}
	staged.ChangeSets[changeSetIndex].Value = changeSet
	if failure := validateCandidateForChangeSet(staged, changeSetID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	if err := addWrite(&writes, interfacePath, value); err != nil {
		return nil, Mutation{}, internalFailure("the interface could not be serialized")
	}
	if err := addWrite(&writes, staged.ChangeSets[changeSetIndex].Path, changeSet); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, changeSetID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return interfaceMutationData{Interface: toInterfaceJSON(value), Operation: toInterfaceOperationJSON(operation)}, mutation, nil
}

type interfaceMutationData struct {
	Interface interfaceJSON          `json:"interface"`
	Operation interfaceOperationJSON `json:"operation"`
}

func runInterfaceList(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("type", "status"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if parsed.has("type") && !validInterfaceType(parsed.one("type")) {
		return nil, Mutation{}, validationFailure("interface.invalid_type", fmt.Sprintf("Unsupported interface type %q.", parsed.one("type")), nil)
	}
	if parsed.has("status") && !validRecordStatus(parsed.one("status")) {
		return nil, Mutation{}, validationFailure("interface.invalid_status", fmt.Sprintf("Unsupported record status %q.", parsed.one("status")), nil)
	}
	state, failure := loadReadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	items := make([]interfaceJSON, 0)
	for _, document := range state.snapshot.Interfaces {
		value := records.CanonicalInterface(document.Value)
		if parsed.has("type") && string(value.Type) != parsed.one("type") {
			continue
		}
		if parsed.has("status") && string(value.Status) != parsed.one("status") {
			continue
		}
		items = append(items, toInterfaceJSON(value))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return struct {
		Interfaces []interfaceJSON `json:"interfaces"`
	}{Interfaces: items}, Mutation{}, nil
}

func runInterfaceShow(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("id"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateInterfaceID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("interface.invalid_id", "Interface", id)
	}
	state, failure := loadReadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	_, value, exists := findInterface(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, validationFailure("interface.not_found", fmt.Sprintf("Interface %s was not found.", id), nil)
	}
	return struct {
		Interface interfaceJSON `json:"interface"`
	}{Interface: toInterfaceJSON(value)}, Mutation{}, nil
}

func interfaceOperation(changeSet *records.ChangeSet, action, id string) (records.InterfaceOperation, *commandFailure) {
	for _, operation := range changeSet.InterfaceOperations {
		if operation.InterfaceID != id {
			continue
		}
		return records.InterfaceOperation{}, conflictFailure("change_set.duplicate_operation", fmt.Sprintf("Interface %s is already present in the change set.", id), nil)
	}
	operation := records.InterfaceOperation{Action: action, InterfaceID: id}
	changeSet.InterfaceOperations = append(changeSet.InterfaceOperations, operation)
	return operation, nil
}

func runArtifactGet(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("id", "kind"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if parsed.has("id") == parsed.has("kind") {
		return nil, Mutation{}, usageFailure("artifact get requires exactly one of --id or --kind")
	}
	if parsed.has("id") {
		if err := records.ValidateArtifactID(parsed.one("id")); err != nil {
			return nil, Mutation{}, invalidIDFailure("artifact.invalid_id", "Artifact", parsed.one("id"))
		}
	}
	if parsed.has("kind") && !validArtifactKind(parsed.one("kind")) {
		return nil, Mutation{}, validationFailure("artifact.unknown_kind", fmt.Sprintf("Unsupported artifact kind %q.", parsed.one("kind")), nil)
	}
	state, failure := loadReadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	artifacts := append([]records.ArtifactEntry(nil), state.snapshot.Config.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	var selected records.ArtifactEntry
	count := 0
	for _, artifact := range artifacts {
		if parsed.has("id") && artifact.ID != parsed.one("id") {
			continue
		}
		if parsed.has("kind") && string(artifact.Kind) != parsed.one("kind") {
			continue
		}
		selected = artifact
		count++
	}
	if count == 0 {
		return nil, Mutation{}, validationFailure("artifact.not_found", "The requested artifact was not found.", nil)
	}
	if count > 1 {
		return nil, Mutation{}, conflictFailure("artifact.ambiguous", "The artifact selector matched more than one artifact.", nil)
	}
	content, failure := readRegularFile(state.root, selected.Path)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return struct {
		Artifact artifactJSON `json:"artifact"`
		Content  string       `json:"content"`
	}{Artifact: toArtifactJSON(selected), Content: string(content)}, Mutation{}, nil
}

func runArtifactList(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("kind", "owner", "at"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if parsed.has("kind") && !validArtifactKind(parsed.one("kind")) {
		return nil, Mutation{}, validationFailure("artifact.unknown_kind", fmt.Sprintf("Unsupported artifact kind %q.", parsed.one("kind")), nil)
	}
	if parsed.has("owner") && !validArtifactOwner(parsed.one("owner")) {
		return nil, Mutation{}, validationFailure("artifact.invalid_owner", fmt.Sprintf("Unsupported artifact owner %q.", parsed.one("owner")), nil)
	}

	var state projectState
	if parsed.has("at") {
		commit, failure := parseRevisionOption(parsed.one("at"))
		if failure != nil {
			return nil, Mutation{}, failure
		}
		root, failure := resolveRoot()
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if failure := requireRevisionCommit(root, commit); failure != nil {
			return nil, Mutation{}, failure
		}
		var cleanup func()
		state, cleanup, failure = loadRevisionState(root, commit)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		defer cleanup()
		if failure := validateCandidate(state.snapshot, true); failure != nil {
			return nil, Mutation{}, artifactListReadFailure(failure)
		}
	} else {
		state, failure = loadReadState()
		if failure != nil {
			return nil, Mutation{}, artifactListReadFailure(failure)
		}
	}

	items := make([]artifactJSON, 0, len(state.snapshot.Config.Artifacts))
	for _, artifact := range state.snapshot.Config.Artifacts {
		if parsed.has("kind") && string(artifact.Kind) != parsed.one("kind") {
			continue
		}
		if parsed.has("owner") && artifact.Owner != parsed.one("owner") {
			continue
		}
		items = append(items, toArtifactJSON(artifact))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return struct {
		Artifacts []artifactJSON `json:"artifacts"`
	}{Artifacts: items}, Mutation{}, nil
}

func artifactListReadFailure(failure *commandFailure) *commandFailure {
	if failure == nil || failure.Code != "validation.failed" {
		return failure
	}
	for _, diagnostic := range failure.Diagnostics {
		if diagnostic.Path == revisionConfigPath && diagnostic.Code == "storage.decode_failed" {
			configurationFailure := projectFailure("project.invalid_configuration", "The project configuration is invalid.")
			configurationFailure.Diagnostics = append([]specvalidation.Diagnostic(nil), failure.Diagnostics...)
			return configurationFailure
		}
		switch diagnostic.Code {
		case "artifact.path_not_found":
			return validationFailure("artifact.read_failed", "A registered artifact was not found.", nil)
		case "artifact.path_unreadable":
			return ioFailure("artifact.read_failed", "A registered artifact could not be read.")
		}
	}
	return failure
}

func runArtifactPut(args []string, stdin io.Reader) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, map[string]optionSpec{
		"change-set":    {takesValue: true},
		"id":            {takesValue: true},
		"kind":          {takesValue: true},
		"path":          {takesValue: true},
		"owner":         {takesValue: true},
		"validator":     {takesValue: true},
		"content-file":  {takesValue: true},
		"content-stdin": {takesValue: false},
	})
	if failure != nil {
		return nil, Mutation{}, failure
	}
	changeSetID, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "id")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateArtifactID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("artifact.invalid_id", "Artifact", id)
	}
	kind, failure := requireOption(parsed, "kind")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	path, failure := requireOption(parsed, "path")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	owner, failure := requireOption(parsed, "owner")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if parsed.has("content-file") == parsed.has("content-stdin") {
		return nil, Mutation{}, usageFailure("artifact put requires exactly one of --content-file or --content-stdin")
	}
	content, failure := readContentSource(parsed, stdin)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	canonicalPath, err := specvalidation.CanonicalProjectPath(path)
	if err != nil {
		return nil, Mutation{}, validationFailure("artifact.invalid_path", "Artifact paths must be slash-separated project-relative paths.", nil)
	}
	canonicalContent, err := specvalidation.CanonicalText(content)
	if err != nil {
		return nil, Mutation{}, artifactContentFailure(err)
	}
	digest, err := specvalidation.CanonicalTextDigest(canonicalContent)
	if err != nil {
		return nil, Mutation{}, artifactContentFailure(err)
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	observeWritePath(&state, canonicalPath)
	changeSetIndex, changeSet, failure := proposedChangeSet(state, changeSetID)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	artifact := records.ArtifactEntry{ID: id, Kind: records.ArtifactKind(kind), Path: canonicalPath, Digest: digest, Owner: owner, Validator: parsed.one("validator")}
	action := "add"
	for _, existing := range state.snapshot.Config.Artifacts {
		if existing.ID == id {
			if existing.Owner != owner {
				return nil, Mutation{}, conflictFailure("artifact.owner_immutable", fmt.Sprintf("Artifact %s is owned by %s and cannot be reassigned during a revision.", id, existing.Owner), nil)
			}
			action = "revise"
			artifact = records.ArtifactEntry{ID: id, Kind: records.ArtifactKind(kind), Path: canonicalPath, Digest: digest, Owner: existing.Owner, Validator: parsed.one("validator")}
			break
		}
	}
	operation, failure := artifactOperation(&changeSet, action, id)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	staged := cloneSnapshot(state.snapshot)
	staged.Config.Artifacts = replaceArtifact(staged.Config.Artifacts, artifact)
	if staged.ArtifactContents == nil {
		staged.ArtifactContents = make(map[string][]byte)
	}
	staged.ArtifactContents[canonicalPath] = append([]byte(nil), canonicalContent...)
	staged.ChangeSets[changeSetIndex].Value = changeSet
	projectionData, projectionChanged, failure := stageSharedClassification(&staged, canonicalPath)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if failure := validateCandidateForChangeSet(staged, changeSetID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	addRawWrite(&writes, canonicalPath, canonicalContent)
	if err := addWrite(&writes, staged.ChangeSets[changeSetIndex].Path, changeSet); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	if projectionChanged {
		addRawWrite(&writes, specvalidation.ProjectionPath, projectionData)
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, changeSetID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return artifactMutationData{Artifact: toArtifactJSON(artifact), Operation: toArtifactOperationJSON(operation)}, mutation, nil
}

// stageSharedClassification adds the shared projection entry for the one
// specification path that artifact put registers to the staged snapshot. It
// returns the replacement manifest bytes and whether the manifest changed.
// ears-manager writes no other projection entry: a missing entry for another
// registered path or a store directory is restored through a reviewed policy
// edit, which the Source Control Manager keeps apart from the change set.
func stageSharedClassification(staged *specvalidation.Snapshot, path string) ([]byte, bool, *commandFailure) {
	current := specvalidation.Projection{}
	if staged.Projection != nil {
		current = staged.Projection.Clone()
	}
	data, changed, err := specvalidation.AddSharedClassifications(current.Data, []string{path})
	if len(current.Diagnostics) > 0 || errors.Is(err, specvalidation.ErrProjectionInvalid) {
		diagnostics := append([]specvalidation.Diagnostic(nil), current.Diagnostics...)
		return nil, false, validationFailure("validation.failed", "The projection manifest must be corrected before a path can be registered.", diagnostics)
	}
	if err != nil {
		return nil, false, internalFailure("the projection manifest could not be updated")
	}
	if !changed {
		return nil, false, nil
	}
	updated := specvalidation.ParseProjection(data)
	staged.Projection = &updated
	return data, true, nil
}

type artifactMutationData struct {
	Artifact  artifactJSON          `json:"artifact"`
	Operation artifactOperationJSON `json:"operation"`
}

func readContentSource(parsed options, stdin io.Reader) ([]byte, *commandFailure) {
	if parsed.has("content-stdin") {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, validationFailure("input.invalid_source", "Artifact content could not be read from stdin.", nil)
		}
		return data, nil
	}
	path := parsed.one("content-file")
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, validationFailure("input.invalid_source", "Artifact content could not be read from stdin.", nil)
		}
		return data, nil
	}
	data, err := openAndReadRegularFile(func() (*os.File, error) {
		return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, validationFailure("input.invalid_source", "The artifact content source must be an existing regular file.", nil)
	}
	if errors.Is(err, errNotRegularFile) {
		return nil, validationFailure("input.invalid_source", "The artifact content source must be an existing regular file.", nil)
	}
	if err != nil {
		return nil, validationFailure("input.invalid_source", "The artifact content source could not be read.", nil)
	}
	return data, nil
}

func replaceArtifact(artifacts []records.ArtifactEntry, value records.ArtifactEntry) []records.ArtifactEntry {
	result := append([]records.ArtifactEntry(nil), artifacts...)
	for index := range result {
		if result[index].ID == value.ID {
			result[index] = value
			return result
		}
	}
	return append(result, value)
}

func artifactOperation(changeSet *records.ChangeSet, action, id string) (records.ArtifactOperation, *commandFailure) {
	for _, operation := range changeSet.ArtifactOperations {
		if operation.ArtifactID != id {
			continue
		}
		if (operation.Action == "add" && action == "revise") || operation.Action == action {
			return operation, nil
		}
		return records.ArtifactOperation{}, conflictFailure("change_set.duplicate_operation", fmt.Sprintf("Artifact %s is already present in the change set.", id), nil)
	}
	operation := records.ArtifactOperation{Action: action, ArtifactID: id}
	changeSet.ArtifactOperations = append(changeSet.ArtifactOperations, operation)
	return operation, nil
}

func runImpact(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateChangeSetID(id); err != nil {
		return nil, Mutation{}, invalidIDFailure("change_set.invalid_id", "Change-set", id)
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	_, value, exists := findChangeSet(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, validationFailure("change_set.not_found", fmt.Sprintf("Change set %s was not found.", id), nil)
	}
	if strings.TrimSpace(value.BaseCommit) == "" || !commitExists(state.root, value.BaseCommit) {
		return nil, Mutation{}, validationFailure("change_set.invalid_base", "The comparison base commit is not present in the local repository.", nil)
	}
	return specvalidation.ImpactForChangeSet(value, value.BaseCommit, state.snapshot), Mutation{}, nil
}

func parseBoolOption(parsed options, name string) (bool, *commandFailure) {
	value, failure := requireOption(parsed, name)
	if failure != nil {
		return false, failure
	}
	if value != "true" && value != "false" {
		return false, usageFailure(fmt.Sprintf("option --%s must be true or false", name))
	}
	return value == "true", nil
}

func invalidIDFailure(code, kind, id string) *commandFailure {
	return validationFailure(code, fmt.Sprintf("%s ID %q is invalid.", kind, id), nil)
}

func artifactContentFailure(err error) *commandFailure {
	detail := strings.TrimRight(err.Error(), ".")
	return validationFailure("artifact.invalid_content", fmt.Sprintf("Artifact content is invalid: %s.", detail), nil)
}

func currentCommit(root string) (string, *commandFailure) {
	output, err := gitCommand("-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", conflictFailure("change_set.no_base", "The repository does not have a usable base commit.", nil)
	}
	commit := strings.TrimSpace(string(output))
	if len(commit) != 40 {
		return "", conflictFailure("change_set.no_base", "The repository base commit is not a full object ID.", nil)
	}
	for _, character := range commit {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return "", conflictFailure("change_set.no_base", "The repository base commit is not a valid object ID.", nil)
		}
	}
	return strings.ToLower(commit), nil
}

func validEARSStyle(value string) bool {
	switch records.EARSStyle(value) {
	case records.EARSUbiquitous, records.EARSEventDriven, records.EARSStateDriven,
		records.EARSUnwantedBehavior, records.EARSOptionalFeature, records.EARSComplex:
		return true
	default:
		return false
	}
}

func validRecordStatus(value string) bool {
	return value == string(records.StatusActive) || value == string(records.StatusRetired)
}

func validInterfaceType(value string) bool {
	switch records.InterfaceType(value) {
	case records.InterfaceNetworkService, records.InterfaceCLI, records.InterfaceREPL,
		records.InterfaceLinkableLibrary, records.InterfaceWebGUI, records.InterfaceNativeGUI,
		records.InterfacePersistentState, records.InterfacePackageSource:
		return true
	default:
		return false
	}
}

func validArtifactKind(value string) bool {
	switch records.ArtifactKind(value) {
	case records.ArtifactVision, records.ArtifactArchitecture, records.ArtifactInterfaceIDL, records.ArtifactInterfaceProse:
		return true
	default:
		return false
	}
}

func validArtifactOwner(value string) bool {
	return value == "user" || value == "ears-manager"
}

func validRelationshipType(value string) bool {
	switch value {
	case "depends-on", "conflicts-with", "supersedes", "related-to":
		return true
	default:
		return false
	}
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
