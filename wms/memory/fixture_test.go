package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redhat-et/protobot/wms/validation"
)

type wmsFixtureRecord struct {
	Step                    string                                     `json:"step"`
	ProjectID               string                                     `json:"project_id"`
	WorkItems               []wmsFixtureWorkItem                       `json:"work_items"`
	FakeGateContexts        map[string]validation.AuthorizationContext `json:"fake_gate_contexts"`
	ApprovalRecords         map[string]validation.ApprovalRecord       `json:"approval_records"`
	EvaluationTime          string                                     `json:"evaluation_time"`
	ReplaceBase             bool                                       `json:"replaces_base_approval_records"`
	WMSTools                []string                                   `json:"wms_tools"`
	DraftingTable           []string                                   `json:"drafting_table"`
	HumanMaintainer         []string                                   `json:"human_maintainer"`
	AdapterAPI              []string                                   `json:"adapter_api"`
	JobSite                 []string                                   `json:"job_site"`
	Materializer            []string                                   `json:"materializer"`
	Reconciler              []string                                   `json:"reconciler"`
	Assert                  wmsFixtureAssertions                       `json:"assert"`
	Operation               string                                     `json:"operation"`
	ActorContextRef         string                                     `json:"actor_context_ref"`
	RequestID               string                                     `json:"request_id"`
	WorkItemID              string                                     `json:"work_item_id"`
	ExpectedRequestRevision *uint64                                    `json:"expected_request_revision"`
	ExpectedState           validation.State                           `json:"expected_state"`
	ExpectedContractVersion *uint64                                    `json:"expected_contract_version"`
	HumanApprovalID         string                                     `json:"human_approval_id"`
	IdempotencyKey          string                                     `json:"idempotency_key"`
	Payload                 json.RawMessage                            `json:"payload"`
	AdapterCall             *bool                                      `json:"adapter_call"`
	Result                  json.RawMessage                            `json:"result"`
}

type wmsFixtureWorkItem struct {
	ID              string           `json:"id"`
	State           validation.State `json:"state"`
	ContractVersion uint64           `json:"contract_version"`
	Dependencies    []string         `json:"dependencies"`
}

type wmsFixtureAssertions struct {
	DraftingTableSubsetOfAdapter     bool     `json:"drafting_table_subset_of_adapter"`
	DraftingTableJobSiteIntersection []string `json:"drafting_table_job_site_intersection"`
	UnknownOperations                []string `json:"unknown_operations"`
}

type wmsFixtureExpected struct {
	OK                           *bool               `json:"ok"`
	Outcome                      string              `json:"outcome"`
	Mutation                     string              `json:"mutation"`
	Error                        *wmsFixtureError    `json:"error"`
	Decision                     *wmsFixtureDecision `json:"decision"`
	RequestID                    string              `json:"request_id"`
	RequestRevision              uint64              `json:"request_revision"`
	ApprovalStatus               string              `json:"approval_status"`
	AuditEvent                   string              `json:"audit_event"`
	LinkedChangeSetPriority      string              `json:"linked_change_set_priority"`
	LinkedWorkItemPriority       string              `json:"linked_work_item_priority"`
	WorkItemState                validation.State    `json:"work_item_state"`
	ContractVersion              uint64              `json:"contract_version"`
	Submission                   string              `json:"submission"`
	ResolutionSubmissionID       string              `json:"resolution_submission_id"`
	ResolutionSubmissionRevision uint64              `json:"resolution_submission_revision"`
	PriorResolutionSubmissionID  string              `json:"prior_resolution_submission_id"`
	PriorSubmissionStatus        string              `json:"prior_submission_status"`
	PriorApprovalStatus          string              `json:"prior_approval_status"`
	Items                        []wmsFixtureItem    `json:"items"`
	PlannedDependency            *PlannedDependency  `json:"planned_dependency"`
}

type wmsFixtureError struct {
	Code string `json:"code"`
}

type wmsFixtureDecision struct {
	Outcome     validation.Outcome   `json:"outcome"`
	Authority   validation.Authority `json:"authority"`
	Operation   validation.Operation `json:"operation"`
	RuleVersion string               `json:"rule_version"`
	Replayed    bool                 `json:"replayed"`
	Rejection   *struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"rejection"`
}

type wmsFixtureItem struct {
	ID                string           `json:"id"`
	State             validation.State `json:"state"`
	ContractVersion   uint64           `json:"contract_version"`
	Dependencies      []string         `json:"dependencies"`
	ReasonKind        string           `json:"reason_kind"`
	ResolutionOptions []string         `json:"resolution_options"`
}

func TestDraftingTableWMSGoldenFixture(t *testing.T) {
	records := loadWMSFixture(t)
	assertFixtureDeclarations(t, records)
	memory := newFixtureMemory(t, records)
	runWMSFixture(t, memory, records)
	assertFixtureFinalState(t, memory)
}

func assertFixtureDeclarations(t *testing.T, records []wmsFixtureRecord) {
	t.Helper()
	for _, record := range records {
		switch record.Step {
		case "wms-tool-manifest":
			want := harnessToolNames(DraftingTableOperations())
			if !reflect.DeepEqual(record.WMSTools, want) {
				t.Fatalf("WMS tool manifest = %#v, want %#v", record.WMSTools, want)
			}
		case "operation-partition":
			assertOperationPartition(t, record)
		}
	}
}

func newFixtureMemory(t *testing.T, records []wmsFixtureRecord) *Memory {
	t.Helper()
	base := fixtureRecord(t, records, "base-state")
	approvalState := fixtureRecord(t, records, "approval-validation-state")
	evalTime, err := time.Parse(time.RFC3339, approvalState.EvaluationTime)
	if err != nil {
		t.Fatalf("parse fixture evaluation time: %v", err)
	}
	contexts := base.FakeGateContexts
	approvals := base.ApprovalRecords
	if approvalState.ReplaceBase {
		approvals = approvalState.ApprovalRecords
	}
	memory := newFixtureAdapter(t, base.ProjectID, contexts, approvals, evalTime)
	seedFixtureWorkItems(t, memory, base.ProjectID, base.WorkItems)
	if err := memory.SeedChangeSet(ChangeSet{ID: "CS-00001", Revision: "proposed"}); err != nil {
		t.Fatal(err)
	}
	return memory
}

func newFixtureAdapter(
	t *testing.T,
	projectID string,
	contexts map[string]validation.AuthorizationContext,
	approvals map[string]validation.ApprovalRecord,
	evalTime time.Time,
) *Memory {
	t.Helper()
	// The golden fixture stores its trusted project binding once on the base
	// state; materialize it into each Gate context before constructing StaticGate.
	for name, context := range contexts {
		if context.ProjectID == "" {
			context.ProjectID = projectID
		}
		contexts[name] = context
	}
	materializer := ""
	for _, context := range contexts {
		if context.Role == validation.RoleMaterializer {
			materializer = context.Subject
			break
		}
	}
	memory, err := New(Config{
		ProjectID:           projectID,
		Gate:                StaticGate(contexts),
		Now:                 func() time.Time { return evalTime },
		LeaseDuration:       15 * time.Minute,
		MaterializerSubject: materializer,
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, approval := range approvals {
		approval.ID = id
		if approval.ProjectID == "" {
			approval.ProjectID = projectID
		}
		if err := memory.SeedApproval(approval); err != nil {
			t.Fatalf("seed approval %s: %v", id, err)
		}
	}
	return memory
}

func seedFixtureWorkItems(t *testing.T, memory *Memory, projectID string, items []wmsFixtureWorkItem) {
	t.Helper()
	states := make(map[string]validation.State, len(items))
	for _, item := range items {
		states[item.ID] = item.State
	}
	for _, item := range items {
		dependencies := make([]validation.Dependency, 0, len(item.Dependencies))
		for _, dependencyID := range item.Dependencies {
			state, exists := states[dependencyID]
			if !exists {
				state = validation.StateWaiting
			}
			dependencies = append(dependencies, validation.Dependency{ID: dependencyID, State: state})
		}
		workItem := validation.WorkItem{
			ID:              item.ID,
			ProjectID:       projectID,
			State:           item.State,
			ContractVersion: item.ContractVersion,
			Dependencies:    dependencies,
		}
		if item.ID == "wi-001" {
			workItem.BlockReason = "undefined-behavior"
		}
		if err := memory.SeedWorkItem(workItem); err != nil {
			t.Fatalf("seed %s: %v", item.ID, err)
		}
	}
}

func runWMSFixture(t *testing.T, memory *Memory, records []wmsFixtureRecord) {
	t.Helper()
	for _, record := range records {
		if record.Step == "defer-session-local" {
			if record.AdapterCall == nil || *record.AdapterCall {
				t.Errorf("defer decision unexpectedly called the adapter: %#v", record.AdapterCall)
			}
			continue
		}
		if len(record.Result) == 0 {
			continue
		}
		result := memory.Execute(fixtureCall(record))
		assertGoldenResult(t, record.Step, result, record.Result)
	}
}

func fixtureCall(record wmsFixtureRecord) CallRequest {
	return CallRequest{
		Operation:               record.Operation,
		ActorContextRef:         record.ActorContextRef,
		RequestID:               record.RequestID,
		WorkItemID:              record.WorkItemID,
		ExpectedRequestRevision: record.ExpectedRequestRevision,
		ExpectedState:           record.ExpectedState,
		ExpectedContractVersion: record.ExpectedContractVersion,
		HumanApprovalID:         record.HumanApprovalID,
		IdempotencyKey:          record.IdempotencyKey,
		Payload:                 record.Payload,
	}
}

func assertFixtureFinalState(t *testing.T, memory *Memory) {
	t.Helper()
	request, exists := memory.Request("request-001")
	if !exists || request.Revision != 6 || request.BusinessPriority != "urgent" || request.ChangeSetID != "CS-00001" || request.BuildWorkItemID != "wi-001" {
		t.Fatalf("final request state = %#v, want revision 6 with linked priority and work item", request)
	}
	superseded, exists := memory.Submission("resolution-submission-001")
	if !exists || superseded.Status != validation.ResolutionSubmissionStatusSuperseded {
		t.Fatalf("superseded resolution = %#v, want superseded", superseded)
	}
	acknowledgement, exists := memory.Submission("acknowledgement-001")
	if !exists || acknowledgement.Status != validation.ResolutionSubmissionStatusConsumed {
		t.Fatalf("acknowledgement = %#v, want consumed", acknowledgement)
	}
	for id, want := range map[string]string{
		"refine-approval-001":     "consumed",
		"resolution-approval-001": "revoked",
		"ack-approval-001":        "consumed",
	} {
		approval, exists := memory.Approval(id)
		if !exists || string(approval.Status) != want {
			t.Errorf("approval %s = %#v, want status %q", id, approval, want)
		}
	}
}

func fixtureRecord(t *testing.T, records []wmsFixtureRecord, step string) wmsFixtureRecord {
	t.Helper()
	for _, record := range records {
		if record.Step == step {
			return record
		}
	}
	t.Fatalf("fixture record %q is missing", step)
	return wmsFixtureRecord{}
}

func loadWMSFixture(t *testing.T) []wmsFixtureRecord {
	t.Helper()
	file, err := os.Open("../../docs/architecture/fixtures/drafting-table-wms-golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close WMS fixture: %v", err)
		}
	}()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var records []wmsFixtureRecord
	for line := 1; scanner.Scan(); line++ {
		var record wmsFixtureRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode fixture line %d: %v", line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func assertOperationPartition(t *testing.T, fixture wmsFixtureRecord) {
	t.Helper()
	if !reflect.DeepEqual(fixture.DraftingTable, DraftingTableOperations()) {
		t.Fatalf("Drafting Table operations = %#v, want %#v", fixture.DraftingTable, DraftingTableOperations())
	}
	if !reflect.DeepEqual(fixture.HumanMaintainer, HumanMaintainerOperations()) {
		t.Fatalf("human-maintainer operations = %#v, want %#v", fixture.HumanMaintainer, HumanMaintainerOperations())
	}
	if !reflect.DeepEqual(fixture.AdapterAPI, AdapterOperations()) {
		t.Fatalf("adapter API = %#v, want %#v", fixture.AdapterAPI, AdapterOperations())
	}
	if !reflect.DeepEqual(fixture.JobSite, JobSiteOperations()) {
		t.Fatalf("Job Site operations = %#v, want %#v", fixture.JobSite, JobSiteOperations())
	}
	if !reflect.DeepEqual(fixture.Materializer, MaterializerOperations()) {
		t.Fatalf("Materializer operations = %#v, want %#v", fixture.Materializer, MaterializerOperations())
	}
	if !reflect.DeepEqual(fixture.Reconciler, ReconcilerOperations()) {
		t.Fatalf("Reconciler operations = %#v, want %#v", fixture.Reconciler, ReconcilerOperations())
	}
	if !fixture.Assert.DraftingTableSubsetOfAdapter {
		t.Fatal("fixture does not assert Drafting Table is a subset of the adapter API")
	}
	if len(fixture.Assert.DraftingTableJobSiteIntersection) != 0 {
		t.Fatalf("fixture Drafting Table/Job Site intersection = %#v, want empty", fixture.Assert.DraftingTableJobSiteIntersection)
	}
	for _, unknown := range fixture.Assert.UnknownOperations {
		if slices.Contains(AdapterOperations(), unknown) {
			t.Errorf("unknown operation %q appears in the adapter API", unknown)
		}
	}
	for _, operation := range fixture.DraftingTable {
		if !slices.Contains(fixture.AdapterAPI, operation) {
			t.Errorf("Drafting Table operation %q is not in adapter API", operation)
		}
		if slices.Contains(fixture.JobSite, operation) {
			t.Errorf("Drafting Table operation %q overlaps Job Site execution", operation)
		}
	}
}

func assertGoldenResult(t *testing.T, step string, actual Result, encodedExpected json.RawMessage) {
	t.Helper()
	var expected wmsFixtureExpected
	if err := json.Unmarshal(encodedExpected, &expected); err != nil {
		t.Fatalf("%s expected result JSON: %v", step, err)
	}
	assertGoldenStatus(t, step, actual, expected)
	assertGoldenFields(t, step, actual, expected)
	assertGoldenDecision(t, step, actual.Decision, expected.Decision)
	assertGoldenItems(t, step, actual, expected.Items)
}

func assertGoldenStatus(t *testing.T, step string, actual Result, expected wmsFixtureExpected) {
	t.Helper()
	if expected.OK != nil && actual.OK != *expected.OK {
		t.Errorf("%s ok = %t, want %t; result=%s", step, actual.OK, *expected.OK, marshalForTest(t, actual))
	}
	if expected.Outcome != "" && string(actual.Outcome) != expected.Outcome {
		t.Errorf("%s outcome = %q, want %q", step, actual.Outcome, expected.Outcome)
	}
	if expected.Mutation != "" && string(actual.Mutation) != expected.Mutation {
		t.Errorf("%s mutation = %q, want %q", step, actual.Mutation, expected.Mutation)
	}
	if expected.Error != nil {
		if actual.Error == nil || actual.Error.Code != expected.Error.Code {
			t.Errorf("%s error = %#v, want code %q", step, actual.Error, expected.Error.Code)
		}
	} else if expected.OK != nil && *expected.OK && actual.Error != nil {
		t.Errorf("%s unexpected error = %#v", step, actual.Error)
	}
}

func assertGoldenFields(t *testing.T, step string, actual Result, expected wmsFixtureExpected) {
	t.Helper()
	checkGoldenString(t, step, "request_id", expected.RequestID, actual.RequestID)
	checkGoldenUint(t, step, "request_revision", expected.RequestRevision, actual.RequestRevision)
	checkGoldenString(t, step, "approval_status", expected.ApprovalStatus, string(actual.ApprovalStatus))
	checkGoldenString(t, step, "audit_event", expected.AuditEvent, actual.AuditEvent)
	checkGoldenString(t, step, "linked_change_set_priority", expected.LinkedChangeSetPriority, actual.LinkedChangeSetPriority)
	checkGoldenString(t, step, "linked_work_item_priority", expected.LinkedWorkItemPriority, actual.LinkedWorkItemPriority)
	checkGoldenString(t, step, "submission", expected.Submission, actual.Submission)
	checkGoldenString(t, step, "resolution_submission_id", expected.ResolutionSubmissionID, actual.ResolutionSubmissionID)
	checkGoldenUint(t, step, "resolution_submission_revision", expected.ResolutionSubmissionRevision, actual.ResolutionSubmissionRevision)
	checkGoldenString(t, step, "prior_resolution_submission_id", expected.PriorResolutionSubmissionID, actual.PriorResolutionSubmissionID)
	checkGoldenString(t, step, "prior_submission_status", expected.PriorSubmissionStatus, string(actual.PriorSubmissionStatus))
	checkGoldenString(t, step, "prior_approval_status", expected.PriorApprovalStatus, string(actual.PriorApprovalStatus))
	if expected.WorkItemState != "" && actual.WorkItemState != expected.WorkItemState {
		t.Errorf("%s work_item_state = %q, want %q", step, actual.WorkItemState, expected.WorkItemState)
	}
	checkGoldenUint(t, step, "contract_version", expected.ContractVersion, actual.ContractVersion)
	if expected.PlannedDependency != nil && !reflect.DeepEqual(expected.PlannedDependency, actual.PlannedDependency) {
		t.Errorf("%s planned dependency = %#v, want %#v", step, actual.PlannedDependency, expected.PlannedDependency)
	}
}

func assertGoldenDecision(t *testing.T, step string, actual *validation.Decision, expected *wmsFixtureDecision) {
	t.Helper()
	if expected == nil {
		return
	}
	if actual == nil {
		t.Errorf("%s decision is missing", step)
		return
	}
	checkGoldenString(t, step, "decision.outcome", string(expected.Outcome), string(actual.Outcome))
	checkGoldenString(t, step, "decision.authority", string(expected.Authority), string(actual.Authority))
	checkGoldenString(t, step, "decision.operation", string(expected.Operation), string(actual.Operation))
	checkGoldenString(t, step, "decision.rule_version", expected.RuleVersion, actual.RuleVersion)
	if expected.Rejection != nil && (actual.Rejection == nil || actual.Rejection.Code != expected.Rejection.Code) {
		t.Errorf("%s decision rejection = %#v, want %q", step, actual.Rejection, expected.Rejection.Code)
	}
}

func assertGoldenItems(t *testing.T, step string, actual Result, expected []wmsFixtureItem) {
	t.Helper()
	if len(expected) == 0 {
		return
	}
	encodedActual := marshalForTest(t, actual)
	var actualItems struct {
		Items []wmsFixtureItem `json:"items"`
	}
	if err := json.Unmarshal(encodedActual, &actualItems); err != nil {
		t.Fatalf("%s actual item result: %v", step, err)
	}
	if len(actualItems.Items) != len(expected) {
		t.Errorf("%s item count = %d, want %d", step, len(actualItems.Items), len(expected))
		return
	}
	for index, want := range expected {
		assertGoldenItem(t, step, index, actualItems.Items[index], want)
	}
}

func assertGoldenItem(t *testing.T, step string, index int, got, want wmsFixtureItem) {
	t.Helper()
	if got.ID != want.ID || got.State != want.State || got.ContractVersion != want.ContractVersion {
		t.Errorf("%s item %d = %#v, want identity/state/version %#v", step, index, got, want)
	}
	if want.Dependencies != nil && !reflect.DeepEqual(got.Dependencies, want.Dependencies) {
		t.Errorf("%s item %d dependencies = %#v, want %#v", step, index, got.Dependencies, want.Dependencies)
	}
	checkGoldenString(t, step, fmt.Sprintf("item[%d].reason_kind", index), want.ReasonKind, got.ReasonKind)
	if want.ResolutionOptions != nil && !reflect.DeepEqual(got.ResolutionOptions, want.ResolutionOptions) {
		t.Errorf("%s item %d resolution options = %#v, want %#v", step, index, got.ResolutionOptions, want.ResolutionOptions)
	}
}

func checkGoldenString(t *testing.T, step, field, want, got string) {
	t.Helper()
	if want != "" && want != got {
		t.Errorf("%s %s = %q, want %q", step, field, got, want)
	}
}

func checkGoldenUint(t *testing.T, step, field string, want, got uint64) {
	t.Helper()
	if want != 0 && want != got {
		t.Errorf("%s %s = %d, want %d", step, field, got, want)
	}
}

func marshalForTest(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func harnessToolNames(operations []string) []string {
	replacer := strings.NewReplacer(".", "_", "-", "_")
	tools := make([]string, len(operations))
	for index, operation := range operations {
		tools[index] = replacer.Replace(operation)
	}
	return tools
}
