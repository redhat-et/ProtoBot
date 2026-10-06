package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// CreateIssue must not be retried: one ambiguous failure surfaces UNKNOWN_MUTATION.
	fake.InjectTransient("create", 1)
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
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls = %d, want 1 (no CreateIssue retries)", fake.CallCount("create"))
	}
	if fake.IssueCount() != 0 {
		t.Fatalf("issues = %d, want 0 after failed create", fake.IssueCount())
	}

	// UNKNOWN_MUTATION must not freeze the key: the same key may retry after reconcile.
	retry := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-unknown-mutation",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Unknown Mutation Request",
			"rationale": "exhaust retries on create",
		}),
	})
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after UNKNOWN_MUTATION = %#v, want applied", retry)
	}
	if retry.Idempotency == adapter.IdempotencyReplayed {
		t.Fatal("retry after UNKNOWN_MUTATION must not replay the frozen failure")
	}
	if fake.CallCount("create") != 2 {
		t.Fatalf("create calls after retry = %d, want 2", fake.CallCount("create"))
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues after retry = %d, want 1", fake.IssueCount())
	}
}

func TestRetryingClientCreateIssueDoesNotRetry(t *testing.T) {
	fake := NewFakeClient()
	client := &RetryingClient{
		Inner:       fake,
		MaxAttempts: 5,
		Backoff:     time.Nanosecond,
		Sleep:       func(time.Duration) {},
	}
	fake.InjectTransient("create", 3)
	_, err := client.CreateIssue(context.Background(), CreateIssueInput{Title: "t", Body: "b"})
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient", err)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls = %d, want 1", fake.CallCount("create"))
	}
}

func TestMaterializeLostCreateResponseRebindsWithoutSecondIssue(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-lost", validation.StateInitial, 0)
	fake.InjectCreateSucceedThenTransient(1)

	first := wms.Execute(materializeCall(candidate, "lost-1", "mat-lost-1"))
	if !first.OK || first.Outcome != adapter.OutcomeApplied {
		t.Fatalf("materialize after lost response = %#v, want applied via reconcile", first)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls = %d, want 1", fake.CallCount("create"))
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues = %d, want 1", fake.IssueCount())
	}
	if fake.CallCount("list") < 1 {
		t.Fatal("expected ListIssues reconcile after lost create response")
	}

	number, ok := wms.WorkItemIssueNumber("wi-lost")
	if !ok {
		t.Fatal("expected rebound work-item index")
	}
	issue, ok := fake.GetIssueRaw(number)
	if !ok {
		t.Fatal("missing github issue")
	}
	doc, err := decodeBody(issue.Body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.MaterializationKey != "mat-lost-1" {
		t.Fatalf("materialization_key = %q", doc.MaterializationKey)
	}
	if doc.SourceFingerprint == "" {
		t.Fatal("expected persisted source_fingerprint")
	}

	second := wms.Execute(materializeCall(candidate, "lost-2", "mat-lost-1"))
	if second.Outcome != adapter.OutcomeReplayed || second.Mutation != adapter.MutationNone {
		t.Fatalf("replay = %#v", second)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls after replay = %d, want 1", fake.CallCount("create"))
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues after replay = %d, want 1", fake.IssueCount())
	}
}

func TestMaterializeColdStartRebindsFromDurableFingerprint(t *testing.T) {
	fake := NewFakeClient()
	first := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-cold", validation.StateInitial, 0)
	created := first.Execute(materializeCall(candidate, "cold-1", "mat-cold-1"))
	if !created.OK {
		t.Fatalf("first materialize = %#v", created)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls = %d, want 1", fake.CallCount("create"))
	}

	// New adapter process: empty in-process indexes, same durable GitHub issues.
	second := newTestAdapter(t, fake)
	replay := second.Execute(materializeCall(candidate, "cold-2", "mat-cold-1"))
	if replay.Outcome != adapter.OutcomeReplayed || replay.Mutation != adapter.MutationNone {
		t.Fatalf("cold-start replay = %#v", replay)
	}
	item, ok := replay.Resource.(validation.WorkItem)
	if !ok || item.ID != "wi-cold" {
		t.Fatalf("replay resource = %#v", replay.Resource)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls after cold start = %d, want 1", fake.CallCount("create"))
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues = %d, want 1", fake.IssueCount())
	}

	changed := candidate
	changed.Readiness.PolicyCompatible = false
	conflict := second.Execute(materializeCall(changed, "cold-3", "mat-cold-1"))
	if conflict.OK || conflict.Error == nil || conflict.Error.Code != validation.CodeIdempotencyConflict {
		t.Fatalf("cold-start conflict = %#v", conflict)
	}
}

func TestRequestCreateLostResponseRebindsByRequestID(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	fake.InjectCreateSucceedThenTransient(1)

	call := adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-lost-1",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Lost Response Request",
			"rationale": "create applied but response lost",
		}),
	}
	first := wms.Execute(call)
	if !first.OK || first.Outcome != adapter.OutcomeApplied {
		t.Fatalf("create after lost response = %#v, want applied via reconcile", first)
	}
	request, ok := first.Resource.(adapter.RequestRecord)
	if !ok || request.ID == "" {
		t.Fatalf("resource = %#v", first.Resource)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls = %d, want 1", fake.CallCount("create"))
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues = %d, want 1", fake.IssueCount())
	}

	// Same idempotency key must not create a second issue.
	retry := wms.Execute(call)
	if retry.Outcome != adapter.OutcomeReplayed {
		t.Fatalf("idempotent retry = %#v, want replayed", retry)
	}
	if fake.CallCount("create") != 1 {
		t.Fatalf("create calls after retry = %d, want 1", fake.CallCount("create"))
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues after retry = %d, want 1", fake.IssueCount())
	}
}

// listBudgetClient lets the first N ListIssues calls through and then fails
// every later list with ErrTransient, so a create reconcile cannot complete.
type listBudgetClient struct {
	inner     Client
	remaining int
}

func (c *listBudgetClient) CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error) {
	return c.inner.CreateIssue(ctx, input)
}

func (c *listBudgetClient) GetIssue(ctx context.Context, number int) (Issue, error) {
	return c.inner.GetIssue(ctx, number)
}

func (c *listBudgetClient) UpdateIssue(ctx context.Context, number int, input UpdateIssueInput) (Issue, error) {
	return c.inner.UpdateIssue(ctx, number, input)
}

func (c *listBudgetClient) ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error) {
	if c.remaining <= 0 {
		return nil, fmt.Errorf("%w: list budget exhausted", ErrTransient)
	}
	c.remaining--
	return c.inner.ListIssues(ctx, filter)
}

func TestRequestCreateRestartAfterUnknownMutationDoesNotDuplicate(t *testing.T) {
	fake := NewFakeClient()
	fake.InjectCreateSucceedThenTransient(1)
	first := newTestAdapterWithClient(t, &listBudgetClient{inner: fake, remaining: 1})

	call := adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-restart-1",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Restart Request",
			"rationale": "process dies before reconcile",
		}),
	}
	unknown := first.Execute(call)
	if unknown.Mutation != adapter.MutationUnknown {
		t.Fatalf("first create = %#v, want UNKNOWN_MUTATION", unknown)
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues after lost response = %d, want 1", fake.IssueCount())
	}

	// New adapter process: no pendingRequestCreate, no request sequence.
	second := newTestAdapter(t, fake)
	replay := second.Execute(call)
	if !replay.OK || replay.Outcome != adapter.OutcomeReplayed {
		t.Fatalf("retry after restart = %#v, want replayed", replay)
	}
	if fake.CallCount("create") != 1 || fake.IssueCount() != 1 {
		t.Fatalf("creates=%d issues=%d, want 1 and 1", fake.CallCount("create"), fake.IssueCount())
	}
	replayed, ok := replay.Resource.(adapter.RequestRecord)
	if !ok || replayed.ID != "REQ-00001" {
		t.Fatalf("replayed resource = %#v", replay.Resource)
	}

	reused := call
	reused.Payload = jsonPayload(t, map[string]any{
		"intent":    "Different Request",
		"rationale": "same key, different payload",
	})
	conflict := newTestAdapter(t, fake).Execute(reused)
	if conflict.OK || conflict.Error == nil || conflict.Error.Code != validation.CodeIdempotencyConflict {
		t.Fatalf("key reuse after restart = %#v, want IDEMPOTENCY_CONFLICT", conflict)
	}
}

func TestRequestCreateAfterRestartGetsFreshIDAndKeepsSemanticDedup(t *testing.T) {
	fake := NewFakeClient()
	first := newTestAdapter(t, fake)
	create := func(w *Adapter, key, intent string) adapter.Result {
		return w.Execute(adapter.CallRequest{
			Operation:       string(validation.OperationRequestCreate),
			ActorContextRef: "drafting-table",
			IdempotencyKey:  key,
			Payload: jsonPayload(t, map[string]any{
				"intent":    intent,
				"rationale": "restart coverage",
			}),
		})
	}
	if res := create(first, "k1", "Alpha"); !res.OK {
		t.Fatalf("first create = %#v", res)
	}

	restarted := newTestAdapter(t, fake)
	dup := create(restarted, "k2", "alpha")
	if dup.OK || dup.Error == nil || dup.Error.Code != adapter.CodeDuplicateRequest {
		t.Fatalf("semantic duplicate after restart = %#v, want DUPLICATE_REQUEST", dup)
	}
	next := create(restarted, "k3", "Beta")
	if !next.OK {
		t.Fatalf("second request = %#v", next)
	}
	request, ok := next.Resource.(adapter.RequestRecord)
	if !ok || request.ID != "REQ-00002" {
		t.Fatalf("request after restart = %#v, want REQ-00002", next.Resource)
	}
	if fake.IssueCount() != 2 {
		t.Fatalf("issues = %d, want 2", fake.IssueCount())
	}
}

func TestRequestCreateWMSUnavailableDoesNotFreezeIdempotencyKey(t *testing.T) {
	fake := NewFakeClient()
	client := &hardFailCreateClient{inner: fake, remaining: 1}
	wms := newTestAdapterWithClient(t, client)

	call := adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-wms-unavailable",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Unavailable Request",
			"rationale": "non-transient create failure",
		}),
	}
	first := wms.Execute(call)
	if first.OK || first.Error == nil || first.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("first create = %#v, want WMS_UNAVAILABLE", first)
	}

	second := wms.Execute(call)
	if !second.OK || second.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied", second)
	}
	if second.Idempotency == adapter.IdempotencyReplayed {
		t.Fatal("retry after WMS_UNAVAILABLE must not replay the frozen failure")
	}
}

func TestShouldRememberIdempotency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		result adapter.Result
		want   bool
	}{
		{
			name:   "applied",
			result: adapter.Result{OK: true, Outcome: adapter.OutcomeApplied, Mutation: adapter.MutationApplied},
			want:   true,
		},
		{
			name:   "not-found",
			result: rejectedResult(string(validation.OperationRequestRefine), validationNotFound("request")),
			want:   true,
		},
		{
			name:   "invalid-request",
			result: rejectedResult(string(validation.OperationRequestCreate), invalidRequest("payload", "bad")),
			want:   true,
		},
		{
			name:   "unknown-mutation",
			result: unknownMutationResult(string(validation.OperationRequestCreate), "ambiguous"),
			want:   false,
		},
		{
			name: "wms-unavailable",
			result: rejectedResult(string(validation.OperationRequestCreate), wmsRejection(
				adapter.CodeWMSUnavailable, "backend down", map[string]any{}, validation.RetryRefresh,
			)),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRememberIdempotency(tc.result); got != tc.want {
				t.Fatalf("shouldRememberIdempotency() = %v, want %v", got, tc.want)
			}
		})
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

// toggledFailClient fails exactly one GetIssue call for a specific issue
// number once armed, then passes through. It lets tests target a transient
// backend failure at one specific load site without disturbing unrelated
// GetIssue calls made earlier in the same operation.
type toggledFailClient struct {
	inner      Client
	failNumber int
	armed      bool
}

func (c *toggledFailClient) arm(number int) {
	c.failNumber = number
	c.armed = true
}

func (c *toggledFailClient) CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error) {
	return c.inner.CreateIssue(ctx, input)
}

func (c *toggledFailClient) GetIssue(ctx context.Context, number int) (Issue, error) {
	if c.armed && number == c.failNumber {
		c.armed = false
		return Issue{}, fmt.Errorf("%w: injected failure for issue %d", ErrTransient, number)
	}
	return c.inner.GetIssue(ctx, number)
}

func (c *toggledFailClient) UpdateIssue(ctx context.Context, number int, input UpdateIssueInput) (Issue, error) {
	return c.inner.UpdateIssue(ctx, number, input)
}

func (c *toggledFailClient) ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error) {
	return c.inner.ListIssues(ctx, filter)
}

func TestRefineRequestTransientLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-refine-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Refine Transient Request",
			"rationale": "refine transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)
	number, ok := wms.RequestIssueNumber(request.ID)
	if !ok {
		t.Fatal("missing request issue number")
	}

	revision := request.Revision
	call := adapter.CallRequest{
		Operation:               string(validation.OperationRequestRefine),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          "refine-key-1",
		Payload: jsonPayload(t, map[string]any{
			"refinement_state": "refining",
			"classification":   "undefined",
		}),
	}

	client.arm(number)
	result := wms.Execute(call)
	if result.OK || result.Error == nil {
		t.Fatalf("refine with transient load = %#v, want rejection", result)
	}
	if result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("error code = %q, want %q (transient load must not collapse to NOT_FOUND)", result.Error.Code, adapter.CodeWMSUnavailable)
	}

	retry := wms.Execute(call)
	if retry.Idempotency == adapter.IdempotencyReplayed {
		t.Fatal("retry after WMS_UNAVAILABLE must not replay a frozen result")
	}
	if retry.OK || retry.Error == nil || retry.Error.Code == validation.CodeNotFound {
		t.Fatalf("retry after transient load = %#v, want a fresh non-NOT_FOUND evaluation", retry)
	}
}

func TestUpdatePriorityTransientRequestLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-priority-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Priority Transient Request",
			"rationale": "priority transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)
	number, ok := wms.RequestIssueNumber(request.ID)
	if !ok {
		t.Fatal("missing request issue number")
	}

	revision := request.Revision
	call := adapter.CallRequest{
		Operation:               string(validation.OperationRequestUpdatePriority),
		ActorContextRef:         "human-maintainer",
		RequestID:               request.ID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          "priority-key-1",
		Payload:                 jsonPayload(t, map[string]any{"business_priority": "high"}),
	}

	client.arm(number)
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("update-priority with transient request load = %#v, want WMS_UNAVAILABLE", result)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied (key must not be frozen)", retry)
	}
}

func TestUpdatePriorityTransientWorkItemLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-wi-priority-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Linked Priority Request",
			"rationale": "linked priority transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)

	candidate := testWorkItem("wi-priority-transient", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-priority-transient", "mat-priority-transient-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}

	linkRevision := request.Revision
	link := wms.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkBuildWorkItem),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &linkRevision,
		IdempotencyKey:          "link-key-1",
		Payload:                 jsonPayload(t, map[string]any{"build_work_item_id": candidate.ID}),
	})
	if !link.OK {
		t.Fatalf("link = %#v", link)
	}
	linkedRequest := link.Resource.(adapter.RequestRecord)

	workItemNumber, ok := wms.WorkItemIssueNumber(candidate.ID)
	if !ok {
		t.Fatal("missing work item issue number")
	}

	priorityRevision := linkedRequest.Revision
	call := adapter.CallRequest{
		Operation:               string(validation.OperationRequestUpdatePriority),
		ActorContextRef:         "human-maintainer",
		RequestID:               linkedRequest.ID,
		ExpectedRequestRevision: &priorityRevision,
		IdempotencyKey:          "priority-key-2",
		Payload:                 jsonPayload(t, map[string]any{"business_priority": "urgent"}),
	}

	client.arm(workItemNumber)
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("update-priority with transient work-item load = %#v, want WMS_UNAVAILABLE", result)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied (key must not be frozen)", retry)
	}
}

func TestLinkChangeSetTransientRequestLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	if err := wms.SeedChangeSet(adapter.ChangeSet{ID: "cs-transient", Revision: "r1", BusinessPriority: "normal"}); err != nil {
		t.Fatalf("seed change set: %v", err)
	}

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-link-cs-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Link Change Set Transient Request",
			"rationale": "link change-set transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)
	number, ok := wms.RequestIssueNumber(request.ID)
	if !ok {
		t.Fatal("missing request issue number")
	}

	revision := request.Revision
	call := adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkChangeSet),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          "link-cs-key-1",
		Payload:                 jsonPayload(t, map[string]any{"change_set_id": "cs-transient"}),
	}

	client.arm(number)
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("link-change-set with transient request load = %#v, want WMS_UNAVAILABLE", result)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied (key must not be frozen)", retry)
	}
}

func TestLinkWorkItemTransientRequestLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-link-wi-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Link Work Item Transient Request",
			"rationale": "link work-item transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)
	number, ok := wms.RequestIssueNumber(request.ID)
	if !ok {
		t.Fatal("missing request issue number")
	}

	candidate := testWorkItem("wi-link-req-transient", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-link-req-transient", "mat-link-req-transient-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}

	revision := request.Revision
	call := adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkBuildWorkItem),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          "link-wi-key-1",
		Payload:                 jsonPayload(t, map[string]any{"build_work_item_id": candidate.ID}),
	}

	client.arm(number)
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("link-work-item with transient request load = %#v, want WMS_UNAVAILABLE", result)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied (key must not be frozen)", retry)
	}
}

func TestLinkWorkItemTransientWorkItemLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-link-wi-load-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Link Work Item Load Transient Request",
			"rationale": "link work-item load transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)

	candidate := testWorkItem("wi-link-load-transient", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-link-load-transient", "mat-link-load-transient-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}
	workItemNumber, ok := wms.WorkItemIssueNumber(candidate.ID)
	if !ok {
		t.Fatal("missing work item issue number")
	}

	revision := request.Revision
	call := adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkBuildWorkItem),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          "link-wi-key-2",
		Payload:                 jsonPayload(t, map[string]any{"build_work_item_id": candidate.ID}),
	}

	client.arm(workItemNumber)
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("link-work-item with transient work-item load = %#v, want WMS_UNAVAILABLE", result)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied (key must not be frozen)", retry)
	}
}

func TestClaimTransientWorkItemLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	candidate := testWorkItem("wi-claim-transient", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-claim-transient", "mat-claim-transient-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}
	item, ok := wms.LoadWorkItemForTest(candidate.ID)
	if !ok {
		t.Fatal("missing work item")
	}
	number, ok := wms.WorkItemIssueNumber(candidate.ID)
	if !ok {
		t.Fatal("missing work item issue number")
	}

	version := item.ContractVersion
	call := adapter.CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           item.State,
		ExpectedContractVersion: &version,
		IdempotencyKey:          "claim-transient",
	}

	client.arm(number)
	result := wms.Execute(call)
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("claim with transient work-item load = %#v, want WMS_UNAVAILABLE (not treated as missing target)", result)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("retry after WMS_UNAVAILABLE = %#v, want applied (key must not be frozen)", retry)
	}
}

func TestRequestQueryTransientLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	created := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestCreate),
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "req-query-transient",
		Payload: jsonPayload(t, map[string]any{
			"intent":    "Query Transient Request",
			"rationale": "query transient coverage",
		}),
	})
	if !created.OK {
		t.Fatalf("create = %#v", created)
	}
	request := created.Resource.(adapter.RequestRecord)
	number, ok := wms.RequestIssueNumber(request.ID)
	if !ok {
		t.Fatal("missing request issue number")
	}

	client.arm(number)
	result := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{}),
	})
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("request query with transient load = %#v, want WMS_UNAVAILABLE (not a silently partial list)", result)
	}
}

func TestWorkItemQueryTransientLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	candidate := testWorkItem("wi-query-transient", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-query-transient", "mat-query-transient-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}
	number, ok := wms.WorkItemIssueNumber(candidate.ID)
	if !ok {
		t.Fatal("missing work item issue number")
	}

	client.arm(number)
	result := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationWorkItemQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{}),
	})
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("work-item query with transient load = %#v, want WMS_UNAVAILABLE (not a silently partial list)", result)
	}
}

func TestBlockedWorkQueryTransientLoadReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	client := &toggledFailClient{inner: fake}
	wms := newTestAdapterWithClient(t, client)

	candidate := testWorkItem("wi-blocked-transient", validation.StateInitial, 0)
	candidate.Readiness.ImpactDispositioned = false
	if result := wms.Execute(materializeCall(candidate, "mat-blocked-transient", "mat-blocked-transient-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}
	number, ok := wms.WorkItemIssueNumber(candidate.ID)
	if !ok {
		t.Fatal("missing work item issue number")
	}

	client.arm(number)
	result := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationBlockedWorkQuery),
		ActorContextRef: "drafting-table",
	})
	if result.OK || result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("blocked-work query with transient load = %#v, want WMS_UNAVAILABLE (not a silently partial list)", result)
	}
}

func newTestAdapter(t *testing.T, client Client) *Adapter {
	t.Helper()
	return newTestAdapterWithClient(t, client)
}

// hardFailCreateClient fails the first N CreateIssue calls with a non-transient
// error so the adapter surfaces WMS_UNAVAILABLE, then delegates.
type hardFailCreateClient struct {
	inner     Client
	remaining int
}

func (c *hardFailCreateClient) CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error) {
	if c.remaining > 0 {
		c.remaining--
		return Issue{}, errors.New("github create permanently unavailable")
	}
	return c.inner.CreateIssue(ctx, input)
}

func (c *hardFailCreateClient) GetIssue(ctx context.Context, number int) (Issue, error) {
	return c.inner.GetIssue(ctx, number)
}

func (c *hardFailCreateClient) UpdateIssue(ctx context.Context, number int, input UpdateIssueInput) (Issue, error) {
	return c.inner.UpdateIssue(ctx, number, input)
}

func (c *hardFailCreateClient) ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error) {
	return c.inner.ListIssues(ctx, filter)
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
