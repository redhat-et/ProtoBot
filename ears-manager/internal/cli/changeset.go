package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
)

func runChangeSetCreate(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("intent", "affected-interface", "affected-scope", "implementation-required", "implementation-rationale", "created"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	intent, failure := requireOption(parsed, "intent")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	implementationRequired, failure := parseBoolOption(parsed, "implementation-required")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	created, failure := requireOption(parsed, "created")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if !implementationRequired && strings.TrimSpace(parsed.one("implementation-rationale")) == "" {
		return nil, Mutation{}, usageFailure("option --implementation-rationale is required when implementation is false")
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	branch, failure := planChangeSetBranch(state, intent)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	changeSet := records.ChangeSet{
		ID:                      branch.id,
		BaseCommit:              branch.base,
		Intent:                  intent,
		Operations:              []records.RequirementOperation{},
		AffectedInterfaces:      append([]string{}, parsed.list("affected-interface")...),
		AffectedScopes:          append([]string(nil), parsed.list("affected-scope")...),
		ImplementationRequired:  implementationRequired,
		ImplementationRationale: parsed.one("implementation-rationale"),
		// A new change set has no reviewed entry yet. Its empty
		// assessment is recorded against the base it starts from.
		ImpactAssessmentBaseCommit: branch.base,
		Created:                    created,
	}
	if !branch.cut {
		return writeNewChangeSet(state, branch, changeSet)
	}
	if failure := cutChangeSetBranch(state.root, state.snapshot.Config.Repository, branch); failure != nil {
		return nil, Mutation{}, failure
	}
	// The working tree is now the default-branch head, so the manifest is
	// written into the project as it is there.
	cut, failure := loadState()
	if failure == nil && cut.head != branch.base {
		failure = conflictFailure("change_set.base_mismatch", "The repository advanced while the command was preparing its write.", nil)
	}
	if failure == nil {
		if _, _, exists := findChangeSet(cut.snapshot, branch.id); exists {
			failure = conflictFailure("change_set.concurrent_update", "The project changed while the command was preparing its write.", nil)
		}
	}
	if failure != nil {
		return nil, Mutation{}, restoreOriginalBranch(state.root, branch, failure)
	}
	data, mutation, failure := writeNewChangeSet(cut, branch, changeSet)
	if failure != nil {
		return nil, Mutation{}, restoreOriginalBranch(state.root, branch, failure)
	}
	return data, mutation, nil
}

// writeNewChangeSet writes the manifest of a new change set and the project
// configuration with its new store digest, on the branch that is checked out.
func writeNewChangeSet(state projectState, branch changeSetBranch, changeSet records.ChangeSet) (any, Mutation, *commandFailure) {
	staged := cloneSnapshot(state.snapshot)
	changeSetPath, err := upsertChangeSet(&staged, changeSet)
	if err != nil {
		return nil, Mutation{}, internalFailure("the change-set path could not be determined")
	}
	if failure := validateCandidateForChangeSet(staged, changeSet.ID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	if err := addWrite(&writes, changeSetPath, changeSet); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, changeSet.ID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return changeSetCreateData{ChangeSet: changeSetCreateRecord{
		ID: changeSet.ID, BaseCommit: branch.base, Branch: branch.name, ManifestPath: changeSetPath,
	}}, mutation, nil
}

type changeSetCreateData struct {
	ChangeSet changeSetCreateRecord `json:"change_set"`
}

type changeSetCreateRecord struct {
	ID           string `json:"id"`
	BaseCommit   string `json:"base_commit"`
	Branch       string `json:"branch"`
	ManifestPath string `json:"manifest_path"`
}

func runChangeSetList(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("status", "interface", "scope"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if parsed.has("status") && !validChangeSetStatus(parsed.one("status")) {
		return nil, Mutation{}, validationFailure("change_set.invalid_status", fmt.Sprintf("Unsupported change-set status %q.", parsed.one("status")), nil)
	}
	if parsed.has("interface") {
		if err := records.ValidateInterfaceID(parsed.one("interface")); err != nil {
			return nil, Mutation{}, invalidIDFailure("interface.invalid_id", "Interface", parsed.one("interface"))
		}
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	items := make([]changeSetListItem, 0)
	var refs []string
	for _, document := range state.snapshot.ChangeSets {
		value := records.CanonicalChangeSet(document.Value)
		if refs == nil {
			if refs, failure = resolveDefaultBranchRefs(state.root, state.snapshot.Config.Repository); failure != nil {
				return nil, Mutation{}, failure
			}
		}
		status, failure := derivedChangeSetStatus(state, refs, document.Path)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if parsed.has("status") && status != parsed.one("status") {
			continue
		}
		if parsed.has("interface") && !slices.Contains(value.AffectedInterfaces, parsed.one("interface")) {
			continue
		}
		if parsed.has("scope") && !slices.Contains(value.AffectedScopes, parsed.one("scope")) {
			continue
		}
		item := changeSetListItem{changeSetJSON: toChangeSetJSON(value), Status: status}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return struct {
		ChangeSets []changeSetListItem `json:"change_sets"`
	}{ChangeSets: items}, Mutation{}, nil
}

type changeSetListItem struct {
	changeSetJSON
	Status string `json:"status"`
}

func runChangeSetShow(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set", "at"))
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
	if parsed.has("at") {
		return runChangeSetShowAt(id, parsed.one("at"))
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	index, _, exists := findChangeSet(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, validationFailure("change_set.not_found", fmt.Sprintf("Change set %s was not found.", id), nil)
	}
	refs, failure := resolveDefaultBranchRefs(state.root, state.snapshot.Config.Repository)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	status, failure := derivedChangeSetStatus(state, refs, state.snapshot.ChangeSets[index].Path)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return changeSetShowResult(state.snapshot, index, status), Mutation{}, nil
}

// runChangeSetShowAt reads the manifest from the tree of one commit. The
// working tree is not read, so an uncommitted edit does not change the result.
// A commit without the project configuration holds no manifest either.
func runChangeSetShowAt(id, at string) (any, Mutation, *commandFailure) {
	commit, failure := parseRevisionOption("at", at)
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
	notFound := validationFailure("change_set.not_found", fmt.Sprintf("Change set %s was not found at commit %s.", id, commit), nil)
	state, cleanup, failure := loadRevisionState(root, commit)
	if failure != nil {
		if failure.Code == "project.not_initialized" {
			return nil, Mutation{}, notFound
		}
		return nil, Mutation{}, failure
	}
	defer cleanup()
	index, _, exists := findChangeSet(state.snapshot, id)
	if !exists {
		return nil, Mutation{}, notFound
	}
	status := "proposed"
	approved, failure := commitOnDefaultBranch(root, state.snapshot.Config.Repository, commit)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if approved {
		status = "approved"
	}
	return changeSetShowResult(state.snapshot, index, status), Mutation{}, nil
}

func changeSetShowResult(snapshot specvalidation.Snapshot, index int, status string) changeSetShowData {
	value := records.CanonicalChangeSet(snapshot.ChangeSets[index].Value)
	manifestPath := snapshot.ChangeSets[index].Path
	return changeSetShowData{
		ChangeSet:       toChangeSetJSON(value),
		Status:          status,
		ChangedCount:    len(value.Operations) + len(value.InterfaceOperations) + len(value.ArtifactOperations),
		ApplicableCount: applicableCount(value.ImpactAssessment),
		ManifestPath:    manifestPath,
		Paths:           changeSetTouchedPaths(snapshot, value, manifestPath),
	}
}

type changeSetShowData struct {
	ChangeSet       changeSetJSON `json:"change_set"`
	Status          string        `json:"status"`
	ChangedCount    int           `json:"changed_count"`
	ApplicableCount int           `json:"applicable_count"`
	ManifestPath    string        `json:"manifest_path"`
	Paths           []string      `json:"paths"`
}

func runChangeSetUpdate(args []string, stdin io.Reader) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions(
		"change-set", "intent", "affected-interface", "affected-scope", "base-commit",
		"implementation-required", "implementation-rationale", "impact-file",
	))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	id, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if !hasChangeSetUpdate(parsed) {
		return nil, Mutation{}, usageFailure("change-set update requires at least one field to update")
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	var baseCommit string
	if parsed.has("base-commit") {
		baseCommit, failure = parseCommitOption(parsed.one("base-commit"))
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if !commitExists(state.root, baseCommit) {
			return nil, Mutation{}, validationFailure("change_set.invalid_base", "The base commit must be the full ID of a commit in the local repository, not of a tag or another object.", nil)
		}
		onDefault, failure := commitOnDefaultBranch(state.root, state.snapshot.Config.Repository, baseCommit)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if onDefault {
			collides, failure := changeSetIDExistsAtCommit(state.root, baseCommit, id)
			if failure != nil {
				return nil, Mutation{}, failure
			}
			if collides {
				manifestPath, err := storePath(state.snapshot.Config, records.ChangeSetStore, id)
				if err != nil {
					return nil, Mutation{}, internalFailure("the change-set path could not be determined")
				}
				diagnostic := specvalidation.Diagnostic{
					Code:     "change_set.id_collision",
					Severity: "error",
					Path:     manifestPath,
					RecordID: id,
					Field:    "id",
					Message:  fmt.Sprintf("Change set %s already exists in the tree of commit %s.", id, baseCommit),
					Hint:     fmt.Sprintf("Run ears-manager change-set renumber --change-set %s, then refresh the base commit and reviewed impact assessment.", id),
				}
				return nil, Mutation{}, conflictFailure("change_set.id_collision", fmt.Sprintf("Change set %s collides with the target base commit %s.", id, baseCommit), []specvalidation.Diagnostic{diagnostic})
			}
		}
	}
	index, current, failure := proposedChangeSetForUpdate(state, id, parsed.has("base-commit"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	before := records.CanonicalChangeSet(current)
	updated := cloneChangeSet(before)
	if parsed.has("intent") {
		updated.Intent = parsed.one("intent")
	}
	if parsed.has("affected-interface") {
		updated.AffectedInterfaces = append([]string{}, parsed.list("affected-interface")...)
	}
	if parsed.has("affected-scope") {
		updated.AffectedScopes = append([]string{}, parsed.list("affected-scope")...)
	}
	if parsed.has("base-commit") {
		onHead, failure := isAncestor(state.root, baseCommit, state.head)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if !onHead {
			return nil, Mutation{}, conflictFailure("change_set.base_mismatch", fmt.Sprintf("Change set %s cannot refresh to %s, because the working tree at %s does not descend from it.", id, baseCommit, state.head), nil)
		}
		forward, failure := isAncestor(state.root, before.BaseCommit, baseCommit)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if !forward {
			return nil, Mutation{}, conflictFailure("change_set.base_mismatch", fmt.Sprintf("Change set %s cannot refresh to %s, because it does not descend from the recorded base %s.", id, baseCommit, before.BaseCommit), nil)
		}
		updated.BaseCommit = baseCommit
	}
	if parsed.has("implementation-required") {
		required, failure := parseBoolOption(parsed, "implementation-required")
		if failure != nil {
			return nil, Mutation{}, failure
		}
		updated.ImplementationRequired = required
	}
	if parsed.has("implementation-rationale") {
		updated.ImplementationRationale = parsed.one("implementation-rationale")
	}
	if parsed.has("impact-file") {
		assessment, failure := readImpactFile(parsed.one("impact-file"), stdin)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		updated.ImpactAssessment = assessment
		// The reviewed assessment holds for the base commit that this
		// update leaves in the manifest.
		updated.ImpactAssessmentBaseCommit = updated.BaseCommit
	}
	updated = records.CanonicalChangeSet(updated)
	staged := cloneSnapshot(state.snapshot)
	staged.ChangeSets[index].Value = updated
	allowDraft := !parsed.has("impact-file")
	if failure := validateCandidateForChangeSet(staged, id, allowDraft); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	if err := addWrite(&writes, staged.ChangeSets[index].Path, updated); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, allowDraft, id) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	beforeStatus := specvalidation.ImpactForChangeSet(before, before.BaseCommit, staged).AssessmentStatus
	afterStatus := specvalidation.ImpactForChangeSet(updated, updated.BaseCommit, staged).AssessmentStatus
	changedPaths := append([]string(nil), mutation.Paths...)
	sort.Strings(changedPaths)
	return changeSetUpdateData{
		ChangeSetID:      id,
		Before:           changeSetUpdateSummaryJSON(before, beforeStatus, parsed),
		After:            changeSetUpdateSummaryJSON(updated, afterStatus, parsed),
		AssessmentStatus: afterStatus,
		ChangedPaths:     changedPaths,
	}, mutation, nil
}

func runChangeSetRenumber(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set", "to"))
	if failure != nil {
		return nil, Mutation{}, failure
	}
	oldID, failure := requireOption(parsed, "change-set")
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if err := records.ValidateChangeSetID(oldID); err != nil {
		return nil, Mutation{}, invalidIDFailure("change_set.invalid_id", "Change-set", oldID)
	}
	state, failure := loadState()
	if failure != nil {
		return nil, Mutation{}, failure
	}
	index, before, failure := proposedChangeSetForRenumber(state, oldID)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	prefix, failure := changeSetBranchPrefix(state.snapshot.Config.Repository.BranchPrefix)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	var newID string
	if parsed.has("to") {
		newID = parsed.one("to")
		if err := records.ValidateChangeSetID(newID); err != nil {
			return nil, Mutation{}, invalidIDFailure("change_set.invalid_id", "Change-set", newID)
		}
		if newID == oldID {
			return nil, Mutation{}, conflictFailure("change_set.id_collision", fmt.Sprintf("Change set %s cannot be renumbered to its current ID.", oldID), nil)
		}
	} else {
		highest, failure := highestChangeSetNumber(state, prefix)
		if failure != nil {
			return nil, Mutation{}, failure
		}
		if highest >= lastChangeSetNumber {
			return nil, Mutation{}, conflictFailure("change_set.sequence_exhausted", "No change-set sequence number is available.", nil)
		}
		newID = fmt.Sprintf("CS-%05d", highest+1)
	}
	newNumber, _ := changeSetNumber(newID)
	inUse, failure := changeSetNumberInUse(state, prefix, newNumber)
	if failure != nil {
		return nil, Mutation{}, failure
	}
	if inUse {
		return nil, Mutation{}, conflictFailure("change_set.id_collision", fmt.Sprintf("Change-set ID %s is already in use.", newID), nil)
	}

	updated := cloneChangeSet(before)
	updated.ID = newID
	staged := cloneSnapshot(state.snapshot)
	oldPath := staged.ChangeSets[index].Path
	oldFields := staged.ChangeSets[index].Fields
	staged.ChangeSets = append(staged.ChangeSets[:index], staged.ChangeSets[index+1:]...)
	newPath, err := upsertChangeSet(&staged, updated)
	if err != nil {
		return nil, Mutation{}, internalFailure("the change-set path could not be determined")
	}
	staged.ChangeSets[len(staged.ChangeSets)-1].Fields = oldFields
	if failure := validateCandidateForChangeSet(staged, newID, true); failure != nil {
		return nil, Mutation{}, failure
	}
	writes := []fileWrite{}
	if err := addWrite(&writes, newPath, updated); err != nil {
		return nil, Mutation{}, internalFailure("the change set could not be serialized")
	}
	addDelete(&writes, oldPath)
	if err := addConfigWrite(state.root, &staged, &writes, state.observed); err != nil {
		return nil, Mutation{}, configWriteFailure(err, "the project configuration could not be serialized")
	}
	mutation, failure := applyStateTransaction(state, writes, func() *commandFailure { return persistedValidation(state.root, true, newID) })
	if failure != nil {
		return nil, Mutation{}, failure
	}
	return changeSetRenumberData{
		From:         oldID,
		To:           newID,
		ManifestPath: newPath,
	}, mutation, nil
}

func changeSetIDExistsAtCommit(root, commit, id string) (bool, *commandFailure) {
	_, exists, failure := changeSetAtCommit(root, commit, id)
	return exists, failure
}

func changeSetAtCommit(root, commit, id string) (records.ChangeSet, bool, *commandFailure) {
	state, cleanup, failure := loadRevisionState(root, commit)
	if failure != nil {
		if failure.Code == "project.not_initialized" {
			return records.ChangeSet{}, false, nil
		}
		return records.ChangeSet{}, false, failure
	}
	defer cleanup()
	_, value, exists := findChangeSet(state.snapshot, id)
	return value, exists, nil
}

func proposedChangeSetForRenumber(state projectState, id string) (int, records.ChangeSet, *commandFailure) {
	index, value, exists := findChangeSet(state.snapshot, id)
	if !exists {
		return -1, records.ChangeSet{}, validationFailure("change_set.not_proposed", fmt.Sprintf("Change set %s was not found.", id), nil)
	}
	refs, failure := resolveDefaultBranchRefs(state.root, state.snapshot.Config.Repository)
	if failure != nil {
		return -1, records.ChangeSet{}, failure
	}
	approved, failure := changeSetApprovedAt(state.root, refs, state.snapshot.ChangeSets[index].Path)
	if failure != nil {
		return -1, records.ChangeSet{}, failure
	}
	if !approved {
		return proposedChangeSet(state, id)
	}
	defaultHead, failure := defaultBranchHead(state.root, state.snapshot.Config.Repository)
	if failure != nil {
		return -1, records.ChangeSet{}, failure
	}
	defaultValue, exists, failure := changeSetAtCommit(state.root, defaultHead, id)
	if failure != nil {
		return -1, records.ChangeSet{}, failure
	}
	if !exists || reflect.DeepEqual(records.CanonicalChangeSet(defaultValue), records.CanonicalChangeSet(value)) {
		return -1, records.ChangeSet{}, conflictFailure("change_set.not_proposed", fmt.Sprintf("Change set %s is approved and immutable.", id), nil)
	}
	if failure := checkRecordedBase(state.root, id, value.BaseCommit); failure != nil {
		return -1, records.ChangeSet{}, failure
	}
	descends, failure := isAncestor(state.root, value.BaseCommit, state.head)
	if failure != nil {
		return -1, records.ChangeSet{}, failure
	}
	if !descends {
		return -1, records.ChangeSet{}, conflictFailure("change_set.base_mismatch", fmt.Sprintf("Change set %s is based on %s, but the working tree at %s does not descend from it.", id, value.BaseCommit, state.head), nil)
	}
	return index, cloneChangeSet(value), nil
}

type changeSetRenumberData struct {
	From         string `json:"from"`
	To           string `json:"to"`
	ManifestPath string `json:"manifest_path"`
}

type changeSetUpdateData struct {
	ChangeSetID      string                 `json:"change_set_id"`
	Before           changeSetUpdateSummary `json:"before"`
	After            changeSetUpdateSummary `json:"after"`
	AssessmentStatus string                 `json:"assessment_status"`
	ChangedPaths     []string               `json:"changed_paths"`
}

type changeSetUpdateSummary struct {
	Intent                  string                 `json:"intent,omitempty"`
	BaseCommit              string                 `json:"base_commit,omitempty"`
	AffectedInterfaces      []string               `json:"affected_interfaces"`
	AffectedScopes          []string               `json:"affected_scopes"`
	ImplementationRequired  *bool                  `json:"implementation_required,omitempty"`
	ImplementationRationale string                 `json:"implementation_rationale,omitempty"`
	AssessmentStatus        string                 `json:"assessment_status"`
	ImpactAssessment        []impactAssessmentJSON `json:"impact_assessment,omitempty"`
}

func runChangeSetCompare(args []string) (any, Mutation, *commandFailure) {
	parsed, failure := parseOptions(args, valueOptions("change-set", "against"))
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
	if parsed.has("against") {
		return nil, Mutation{}, validationFailure("change_set.invalid_base", "Comparison against arbitrary revisions via --against is not supported.", nil)
	}
	if strings.TrimSpace(value.BaseCommit) == "" || !commitExists(state.root, value.BaseCommit) {
		return nil, Mutation{}, validationFailure("change_set.invalid_base", "The comparison base commit is not present in the local repository.", nil)
	}
	return specvalidation.CompareChangeSet(value, value.BaseCommit, state.snapshot), Mutation{}, nil
}

func hasChangeSetUpdate(parsed options) bool {
	for _, name := range []string{"intent", "affected-interface", "affected-scope", "base-commit", "implementation-required", "implementation-rationale", "impact-file"} {
		if parsed.has(name) {
			return true
		}
	}
	return false
}

func readImpactFile(path string, stdin io.Reader) ([]records.ImpactAssessment, *commandFailure) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
		if err != nil {
			return nil, validationFailure("input.invalid_source", "The impact file could not be read from stdin.", nil)
		}
	} else {
		data, err = openAndReadRegularFile(func() (*os.File, error) {
			return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		})
		if err != nil {
			return nil, validationFailure("input.invalid_source", "The impact file must be an existing regular file.", nil)
		}
	}
	var entries []impactAssessmentJSON
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, validationFailure("change_set.invalid_impact", "The impact file is not a JSON list of complete impact assessments.", nil)
	}
	result := make([]records.ImpactAssessment, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.RequirementID) == "" || strings.TrimSpace(entry.Disposition) == "" || strings.TrimSpace(entry.Rationale) == "" || strings.TrimSpace(entry.Origin) == "" {
			return nil, validationFailure("change_set.invalid_impact", "Each impact assessment requires requirement_id, disposition, rationale, and origin.", nil)
		}
		result = append(result, records.ImpactAssessment{
			RequirementID: entry.RequirementID,
			Disposition:   entry.Disposition,
			Rationale:     entry.Rationale,
			Origin:        entry.Origin,
		})
	}
	return result, nil
}

func changeSetUpdateSummaryJSON(value records.ChangeSet, status string, parsed options) changeSetUpdateSummary {
	summary := changeSetUpdateSummary{
		AffectedInterfaces: append([]string{}, value.AffectedInterfaces...),
		AffectedScopes:     append([]string{}, value.AffectedScopes...),
		AssessmentStatus:   status,
	}
	if summary.AffectedInterfaces == nil {
		summary.AffectedInterfaces = []string{}
	}
	if summary.AffectedScopes == nil {
		summary.AffectedScopes = []string{}
	}
	if parsed.has("intent") {
		summary.Intent = value.Intent
	}
	if parsed.has("base-commit") {
		summary.BaseCommit = value.BaseCommit
	}
	if parsed.has("implementation-required") {
		required := value.ImplementationRequired
		summary.ImplementationRequired = &required
	}
	if parsed.has("implementation-rationale") {
		summary.ImplementationRationale = value.ImplementationRationale
	}
	if parsed.has("impact-file") || len(value.ImpactAssessment) > 0 {
		summary.ImpactAssessment = make([]impactAssessmentJSON, 0, len(value.ImpactAssessment))
		for _, assessment := range value.ImpactAssessment {
			summary.ImpactAssessment = append(summary.ImpactAssessment, toImpactAssessmentJSON(assessment))
		}
	}
	return summary
}

func derivedChangeSetStatus(state projectState, refs []string, manifestPath string) (string, *commandFailure) {
	approved, failure := changeSetApprovedAt(state.root, refs, manifestPath)
	if failure != nil {
		return "", failure
	}
	if approved {
		return "approved", nil
	}
	return "proposed", nil
}

func changeSetTouchedPaths(snapshot specvalidation.Snapshot, changeSet records.ChangeSet, manifestPath string) []string {
	paths := []string{manifestPath}
	for _, operation := range changeSet.Operations {
		path, err := storePath(snapshot.Config, records.RequirementStore, operation.RequirementID)
		if err == nil {
			paths = append(paths, path)
		}
	}
	for _, operation := range changeSet.InterfaceOperations {
		path, err := storePath(snapshot.Config, records.InterfaceStore, operation.InterfaceID)
		if err == nil {
			paths = append(paths, path)
		}
	}
	artifacts := make(map[string]string, len(snapshot.Config.Artifacts))
	for _, artifact := range snapshot.Config.Artifacts {
		artifacts[artifact.ID] = artifact.Path
	}
	for _, operation := range changeSet.ArtifactOperations {
		if path := artifacts[operation.ArtifactID]; path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return uniqueStrings(paths)
}

func applicableCount(values []records.ImpactAssessment) int {
	count := 0
	for _, assessment := range values {
		if assessment.Disposition == "applicable" {
			count++
		}
	}
	return count
}

func parseCommitOption(value string) (string, *commandFailure) {
	value = strings.TrimSpace(value)
	if len(value) != 40 {
		return "", validationFailure("change_set.invalid_base", "Comparison commits must be a full 40-character hexadecimal Git object ID.", nil)
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return "", validationFailure("change_set.invalid_base", "Comparison commits must be a full 40-character hexadecimal Git object ID.", nil)
		}
	}
	return strings.ToLower(value), nil
}

func validChangeSetStatus(value string) bool {
	return value == "proposed" || value == "approved"
}
