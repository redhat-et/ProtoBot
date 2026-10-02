package github

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

var testTime = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func TestRequestCreateMapsWithoutGitHubLeak(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)

	result := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-key-1",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Sample Request",
			"rationale": "Need a tracked backlog item",
		}),
	})
	if !result.OK || result.Outcome != adapter.OutcomeApplied {
		t.Fatalf("create result = %#v", result)
	}
	request, ok := result.Resource.(adapter.RequestRecord)
	if !ok {
		t.Fatalf("resource type = %T, want RequestRecord", result.Resource)
	}
	if request.ID == "" || request.Intent != "Sample Request" {
		t.Fatalf("request = %#v", request)
	}
	assertNoGitHubLeak(t, result.Resource)

	number, ok := wms.RequestIssueNumber(request.ID)
	if !ok {
		t.Fatal("expected github issue for request")
	}
	issue, ok := fake.GetIssueRaw(number)
	if !ok {
		t.Fatal("missing github issue")
	}
	doc, err := decodeBody(issue.Body)
	if err != nil || doc.Request == nil {
		t.Fatalf("body decode = %#v err=%v", doc, err)
	}
	if doc.Request.ID != request.ID {
		t.Fatalf("stored request id = %q, want %q", doc.Request.ID, request.ID)
	}
}

func TestMaterializeIdempotentUnderStableKey(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-001", validation.StateInitial, 0)

	first := wms.Execute(materializeCall(candidate, "idemp-1", "mat-key-1"))
	if !first.OK || first.Decision == nil || first.Decision.Outcome != validation.OutcomeAllowed {
		t.Fatalf("first materialize = %#v", first)
	}
	item1, ok := first.Resource.(validation.WorkItem)
	if !ok {
		t.Fatalf("resource = %T", first.Resource)
	}
	assertNoGitHubLeak(t, first.Resource)

	second := wms.Execute(materializeCall(candidate, "idemp-2", "mat-key-1"))
	if second.Outcome != adapter.OutcomeReplayed || second.Mutation != adapter.MutationNone {
		t.Fatalf("replay = %#v", second)
	}
	item2, ok := second.Resource.(validation.WorkItem)
	if !ok {
		t.Fatalf("replay resource = %T", second.Resource)
	}
	if item1.ID != item2.ID {
		t.Fatalf("work item ids %q vs %q", item1.ID, item2.ID)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls = %d, want 1", fake.CallCount("create"))
	}

	changed := candidate
	changed.Readiness.PolicyCompatible = false
	conflict := wms.Execute(materializeCall(changed, "idemp-3", "mat-key-1"))
	if conflict.OK || conflict.Error == nil || conflict.Error.Code != validation.CodeIdempotencyConflict {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func TestClaimStaleContractVersionRejected(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-claim", validation.StateInitial, 0)
	materialized := wms.Execute(materializeCall(candidate, "mat-claim-1", "mat-claim-key"))
	if !materialized.OK {
		t.Fatalf("materialize = %#v", materialized)
	}
	item, ok := wms.LoadWorkItemForTest("wi-claim")
	if !ok {
		t.Fatal("missing work item")
	}
	beforeBody := mustIssueBody(t, fake, wms, item.ID)

	stale := item.ContractVersion - 1
	if item.ContractVersion == 0 {
		stale = 0
		// Force a mismatch against ready-for-building version 1 by using 0 after materialize.
	}
	result := wms.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           validation.StateReadyForBuilding,
		ExpectedContractVersion: &stale,
		IdempotencyKey:          "claim-stale",
	})
	if result.OK || result.Error == nil || result.Error.Code != validation.CodeStaleContractVersion {
		t.Fatalf("stale claim = %#v", result)
	}
	afterBody := mustIssueBody(t, fake, wms, item.ID)
	if beforeBody != afterBody {
		t.Fatal("stale claim mutated github issue body")
	}
	unchanged, _ := wms.LoadWorkItemForTest(item.ID)
	if unchanged.State != item.State || unchanged.ContractVersion != item.ContractVersion {
		t.Fatalf("item mutated: %#v", unchanged)
	}
}

func TestTransientGetRetriesThenSucceeds(t *testing.T) {
	fake := NewFakeClient()
	retries := 0
	client := &RetryingClient{
		Inner:       fake,
		MaxAttempts: 3,
		Backoff:     time.Nanosecond,
		Sleep:       func(time.Duration) { retries++ },
	}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-retry",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Retry Request",
			"rationale": "exercise transient get",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)

	fake.InjectTransient("get", 2)
	got := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestGet),
		ActorContextRef: "drafting-table",
		RequestID:       request.ID,
	})
	if !got.OK {
		t.Fatalf("get after transient = %#v", got)
	}
	if fake.CallCount("get") != 3 {
		t.Fatalf("get calls = %d, want 3", fake.CallCount("get"))
	}
	if retries != 2 {
		t.Fatalf("sleeps = %d, want 2", retries)
	}
}

func TestCreateExhaustedTransientReturnsUnknownMutation(t *testing.T) {
	fake := NewFakeClient()
	client := &RetryingClient{
		Inner:       fake,
		MaxAttempts: 3,
		Backoff:     time.Nanosecond,
		Sleep:       func(time.Duration) {},
	}
	wms := newTestAdapterWithClient(t, client)

	fake.InjectTransient("create", 3)
	result := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-unknown-mutation",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Unknown Mutation Request",
			"rationale": "exhaust retries on create",
		}),
	})
	if result.OK {
		t.Fatalf("create = %#v, want rejection", result)
	}
	if result.Mutation != adapter.MutationUnknown {
		t.Fatalf("mutation = %q, want %q", result.Mutation, adapter.MutationUnknown)
	}
	if result.Error == nil || result.Error.Code != adapter.CodeUnknownMutation {
		t.Fatalf("error = %#v, want %s", result.Error, adapter.CodeUnknownMutation)
	}
	if result.Error.Retry != validation.RetryReconcile {
		t.Fatalf("retry = %q, want %q", result.Error.Retry, validation.RetryReconcile)
	}
	if fake.CallCount("create") != 3 {
		t.Fatalf("create calls = %d, want 3", fake.CallCount("create"))
	}
	if fake.IssueCount() != 0 {
		t.Fatalf("issues = %d, want 0 after exhausted create", fake.IssueCount())
	}
}

func TestUnauthorizedMaterializeRejected(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-unauth", validation.StateInitial, 0)
	call := materializeCall(candidate, "unauth-1", "unauth-key")
	call.ActorContextRef = "drafting-table"
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != validation.CodeUnauthorizedAction {
		t.Fatalf("unauthorized materialize = %#v", result)
	}
}

func TestWorkItemAndBlockedQueries(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	ready := testWorkItem("wi-ready", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(ready, "q-1", "q-key-1")); !result.OK {
		t.Fatalf("materialize ready = %#v", result)
	}

	blockedCandidate := testWorkItem("wi-blocked", validation.StateInitial, 0)
	blockedCandidate.Readiness.ImpactDispositioned = false
	blockedResult := wms.Execute(materializeCall(blockedCandidate, "q-2", "q-key-2"))
	if !blockedResult.OK {
		t.Fatalf("materialize blocked = %#v", blockedResult)
	}

	query := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationWorkItemQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{"state": "ready-for-building"}),
	})
	if !query.OK || len(query.Items) != 1 || query.Items[0].ID != "wi-ready" {
		t.Fatalf("ready query = %#v", query)
	}
	assertNoGitHubLeak(t, query.Items[0])

	blocked := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationBlockedWorkQuery),
		ActorContextRef: "drafting-table",
	})
	if !blocked.OK || len(blocked.Items) != 1 || blocked.Items[0].ID != "wi-blocked" {
		t.Fatalf("blocked query = %#v", blocked)
	}
}

func newTestAdapter(t *testing.T, client Client) *Adapter {
	t.Helper()
	return newTestAdapterWithClient(t, client)
}

func newTestAdapterWithClient(t *testing.T, client Client) *Adapter {
	t.Helper()
	gate := adapter.StaticGate{
		"drafting-table": testAuthorization("drafting-agent", validation.RoleDraftingTable,
			validation.OperationRequestCreate,
			validation.OperationRequestRefine,
			validation.OperationRequestLinkChangeSet,
			validation.OperationRequestLinkBuildWorkItem,
			validation.OperationRequestGet,
			validation.OperationRequestQuery,
			validation.OperationWorkItemGet,
			validation.OperationWorkItemQuery,
			validation.OperationBlockedWorkQuery,
		),
		"human-maintainer": testAuthorization("maintainer", validation.RoleHumanMaintainer,
			validation.OperationRequestUpdatePriority,
			validation.OperationRequestLinkChangeSet,
		),
		"materializer": testAuthorization("materializer-1", validation.RoleMaterializer,
			validation.OperationMaterialize,
		),
		"job-site": testAuthorization("job-site", validation.RoleJobSite,
			validation.OperationClaim,
		),
	}
	for ref, context := range gate {
		context.AllowedRefs = append(context.AllowedRefs, "main")
		gate[ref] = context
	}
	wms, err := New(Config{
		ProjectID:           "fixture-project",
		Gate:                gate,
		Client:              client,
		Now:                 func() time.Time { return testTime },
		LeaseDuration:       15 * time.Minute,
		MaterializerSubject: "materializer-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return wms
}

func testAuthorization(subject string, role validation.Role, operations ...validation.Operation) validation.AuthorizationContext {
	return validation.AuthorizationContext{
		Subject:        subject,
		Role:           role,
		ProjectID:      "fixture-project",
		AllowedActions: append([]validation.Operation(nil), operations...),
		AllowedRefs:    []string{"project:fixture-project"},
		ExpiresAt:      testTime.Add(time.Hour),
		PolicyVersion:  "wms-policy/v1",
	}
}

func testWorkItem(id string, state validation.State, version uint64) validation.WorkItem {
	return validation.WorkItem{
		ID:                     id,
		ProjectID:              "fixture-project",
		State:                  state,
		ContractVersion:        version,
		ChangeType:             "undefined",
		ImplementationRequired: true,
		Readiness: validation.Readiness{
			ContractComplete:           true,
			SourceImmutable:            true,
			SpecificationValidated:     true,
			RequirementReferencesValid: true,
			PipelineEntrySatisfied:     true,
			ImpactDispositioned:        true,
			PolicyCompatible:           true,
		},
	}
}

func materializeCall(item validation.WorkItem, idempotencyKey, materializationKey string) adapter.CallRequest {
	version := uint64(0)
	return adapter.CallRequest{
		Operation:               string(validation.OperationMaterialize),
		ActorContextRef:         "materializer",
		ExpectedState:           validation.StateInitial,
		ExpectedContractVersion: &version,
		IdempotencyKey:          idempotencyKey,
		MaterializationKey:      materializationKey,
		Payload:                 mustJSON(validation.Payload{WorkItem: &item}),
	}
}

func jsonPayload(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func assertNoGitHubLeak(t *testing.T, resource any) {
	t.Helper()
	value := reflect.ValueOf(resource)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return
	}
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		switch name {
		case "Number", "NodeID", "HTMLURL", "URL", "GithubNodeID", "IssueNumber":
			t.Fatalf("github field leaked on resource: %s", name)
		}
	}
}

func mustIssueBody(t *testing.T, fake *FakeClient, wms *Adapter, workItemID string) string {
	t.Helper()
	number, ok := wms.WorkItemIssueNumber(workItemID)
	if !ok {
		t.Fatal("missing issue number")
	}
	issue, ok := fake.GetIssueRaw(number)
	if !ok {
		t.Fatal("missing issue")
	}
	return issue.Body
}
