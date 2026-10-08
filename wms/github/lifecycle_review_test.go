package github

import (
	"testing"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

// TestRequestOperationsAfterRestartServeDurableRecords covers the fix for
// request reads and mutations trusting only the in-process requestIssue map.
// Previously, after a restart, request.get/query/refine and the link
// operations returned NOT_FOUND (or an empty query result) for durable
// requests until some unrelated request.create happened to hydrate the
// index. The fix hydrates the request indexes from the durable store at the
// start of every request operation.
func TestRequestOperationsAfterRestartServeDurableRecords(t *testing.T) {
	fake := NewFakeClient()
	first := newTestAdapter(t, fake)
	request := createGithubTestRequest(t, first, "Restart Serve Request", "durable after restart")

	// Fresh adapter process over the same durable store.
	second := newTestAdapter(t, fake)

	got := second.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestGet),
		ActorContextRef: "drafting-table",
		RequestID:       request.ID,
	})
	if !got.OK || got.RequestID != request.ID {
		t.Fatalf("request.get after restart = %#v, want durable request", got)
	}

	query := second.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{}),
	})
	if !query.OK || len(query.Requests) != 1 || query.Requests[0].ID != request.ID {
		t.Fatalf("request.query after restart = %#v, want the durable request, not an empty set", query)
	}

	// A mutating operation must not freeze a spurious NOT_FOUND under its
	// idempotency key on a restarted adapter.
	revision := request.Revision
	changeSet := adapter.ChangeSet{ID: "CS-Restart", Revision: "rev-1"}
	if err := second.SeedChangeSet(changeSet); err != nil {
		t.Fatal(err)
	}
	link := second.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkChangeSet),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          "link-restart-1",
		Payload:                 jsonPayload(t, map[string]any{"change_set_id": "CS-Restart", "target_revision": "rev-1"}),
	})
	if !link.OK {
		t.Fatalf("request link after restart = %#v, want applied", link)
	}
}

// TestWorkItemQueriesAfterRestartServeDurableRecords covers the work-item
// analogue: work-item.get and work-item.query previously iterated only the
// in-process workItemIssue map, so a restarted adapter returned an empty
// success instead of the durable set. The fix hydrates the work-item
// indexes from durable issues before serving queries.
func TestWorkItemQueriesAfterRestartServeDurableRecords(t *testing.T) {
	fake := NewFakeClient()
	first := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-restart-query", validation.StateInitial, 0)
	if result := first.Execute(materializeCall(candidate, "mat-restart-query", "mat-restart-query-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}

	// Fresh adapter process over the same durable store.
	second := newTestAdapter(t, fake)

	got := second.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationWorkItemGet),
		ActorContextRef: "drafting-table",
		WorkItemID:      candidate.ID,
	})
	if !got.OK || got.WorkItemID != candidate.ID {
		t.Fatalf("work-item.get after restart = %#v, want durable work item", got)
	}

	query := second.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationWorkItemQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{}),
	})
	if !query.OK || len(query.Items) != 1 || query.Items[0].ID != candidate.ID {
		t.Fatalf("work-item.query after restart = %#v, want the durable work item, not an empty set", query)
	}
}

// TestClaimAfterRestartServesDurableWorkItem covers a claim issued by a
// restarted adapter against a work item materialized by a previous process:
// previously the in-process index miss made observeTarget treat the durable
// item as missing and could freeze that rejection under the claim key.
func TestClaimAfterRestartServesDurableWorkItem(t *testing.T) {
	fake := NewFakeClient()
	first := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-restart-claim", validation.StateInitial, 0)
	if result := first.Execute(materializeCall(candidate, "mat-restart-claim", "mat-restart-claim-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}
	item, ok := first.LoadWorkItemForTest(candidate.ID)
	if !ok {
		t.Fatal("missing work item")
	}

	second := newTestAdapter(t, fake)
	version := item.ContractVersion
	result := second.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           item.State,
		ExpectedContractVersion: &version,
		IdempotencyKey:          "claim-restart-1",
	})
	if !result.OK || result.Outcome != adapter.OutcomeApplied {
		t.Fatalf("claim after restart = %#v, want applied", result)
	}
	if result.WorkItemState != validation.StateBuilding {
		t.Fatalf("claim state after restart = %q, want building", result.WorkItemState)
	}
}

// TestClaimLostResponseReconcilesToAppliedClaim covers the fix for an
// ambiguous (exhausted-transient) UpdateIssue during claim: GitHub applies
// the lease, but the adapter returns UNKNOWN_MUTATION with no durable
// binding. A retry under the same key previously loaded the already-claimed
// item, was rejected with DUPLICATE_CLAIM, and that determinate rejection
// was frozen under the claim key. The fix persists a last-claim binding in
// the same body write as the lease so the retry reconciles.
func TestClaimLostResponseReconcilesToAppliedClaim(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	candidate := testWorkItem("wi-claim-lost", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-claim-lost", "mat-claim-lost-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}
	item, ok := wms.LoadWorkItemForTest(candidate.ID)
	if !ok {
		t.Fatal("missing work item")
	}

	version := item.ContractVersion
	call := adapter.CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           item.State,
		ExpectedContractVersion: &version,
		IdempotencyKey:          "claim-lost-1",
	}

	fake.InjectUpdateSucceedThenTransient(1)
	ambiguous := wms.Execute(call)
	if ambiguous.Mutation != adapter.MutationUnknown {
		t.Fatalf("ambiguous claim = %#v, want UNKNOWN_MUTATION", ambiguous)
	}

	retry := wms.Execute(call)
	if !retry.OK || retry.Outcome != adapter.OutcomeReplayed {
		t.Fatalf("claim retry after ambiguous update = %#v, want replayed applied result", retry)
	}
	if retry.Error != nil {
		t.Fatalf("claim retry error = %#v, want none", retry.Error)
	}
	if retry.WorkItemState != validation.StateBuilding {
		t.Fatalf("claim retry state = %q, want building", retry.WorkItemState)
	}
	if retry.Decision == nil || retry.Decision.FencingTokenIssued == "" {
		t.Fatalf("claim retry decision = %#v, want the originally issued fencing token", retry.Decision)
	}
	if retry.Decision.Before.State != item.State || retry.Decision.Before.ContractVersion != item.ContractVersion {
		t.Fatalf("claim retry before = %#v, want the pre-claim snapshot", retry.Decision.Before)
	}
}

// TestClaimPreservesRequestAndChangeSetLinks covers the fix for
// updateWorkItemIssue always writing LinkedRequestID and LinkedChangeSetID
// from its arguments: a claim after request.link-build-work-item previously
// blanked the durable links, and subsequent work-item projections omitted
// request_id and change_set_id. The fix preserves the stored links unless a
// caller replaces them.
func TestClaimPreservesRequestAndChangeSetLinks(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)
	request := createGithubTestRequest(t, wms, "Link Preservation Request", "claim must not blank links")
	changeSet := adapter.ChangeSet{ID: "CS-Preserve", Revision: "rev-1", BusinessPriority: "high"}
	if err := wms.SeedChangeSet(changeSet); err != nil {
		t.Fatal(err)
	}

	candidate := testWorkItem("wi-preserve-links", validation.StateInitial, 0)
	if result := wms.Execute(materializeCall(candidate, "mat-preserve-links", "mat-preserve-links-key")); !result.OK {
		t.Fatalf("materialize = %#v", result)
	}

	// Link the change set to the request first so the work item's durable
	// body carries both a request link and a change-set link before claim.
	linkChangeSet := wms.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkChangeSet),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &request.Revision,
		IdempotencyKey:          "link-preserve-cs-1",
		Payload:                 jsonPayload(t, map[string]any{"change_set_id": "CS-Preserve", "target_revision": "rev-1"}),
	})
	if !linkChangeSet.OK {
		t.Fatalf("link change set = %#v", linkChangeSet)
	}
	linkedRequest, ok := linkChangeSet.Resource.(adapter.RequestRecord)
	if !ok {
		t.Fatalf("link change set resource = %T", linkChangeSet.Resource)
	}

	link := wms.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationRequestLinkBuildWorkItem),
		ActorContextRef:         "drafting-table",
		RequestID:               request.ID,
		ExpectedRequestRevision: &linkedRequest.Revision,
		IdempotencyKey:          "link-preserve-1",
		Payload:                 jsonPayload(t, map[string]any{"build_work_item_id": candidate.ID}),
	})
	if !link.OK {
		t.Fatalf("link work item = %#v", link)
	}

	item, ok := wms.LoadWorkItemForTest(candidate.ID)
	if !ok {
		t.Fatal("missing work item")
	}
	version := item.ContractVersion
	claim := wms.Execute(adapter.CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           item.State,
		ExpectedContractVersion: &version,
		IdempotencyKey:          "claim-preserve-1",
	})
	if !claim.OK {
		t.Fatalf("claim after link = %#v", claim)
	}

	number, ok := wms.WorkItemIssueNumber(candidate.ID)
	if !ok {
		t.Fatal("missing work item issue number")
	}
	issue, ok := fake.GetIssueRaw(number)
	if !ok {
		t.Fatal("missing issue")
	}
	doc, err := decodeBody(issue.Body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.LinkedRequestID != request.ID {
		t.Fatalf("linked_request_id after claim = %q, want %q", doc.LinkedRequestID, request.ID)
	}
	if doc.LinkedChangeSetID != "CS-Preserve" {
		t.Fatalf("linked_change_set_id after claim = %q, want CS-Preserve", doc.LinkedChangeSetID)
	}
	if doc.SourceFingerprint == "" {
		t.Fatal("source fingerprint was blanked by claim")
	}

	got := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationWorkItemGet),
		ActorContextRef: "drafting-table",
		WorkItemID:      candidate.ID,
	})
	if !got.OK {
		t.Fatalf("work-item.get after claim = %#v", got)
	}
	projection, ok := got.Resource.(adapter.WorkItemProjection)
	if !ok {
		t.Fatalf("resource type = %T, want WorkItemProjection", got.Resource)
	}
	if projection.RequestID != request.ID || projection.ChangeSetID != "CS-Preserve" {
		t.Fatalf("projection after claim = %#v, want links preserved", projection)
	}
}

// TestBlockedWorkQueryMatchesGoldenFixtureShape covers the fix for the
// blocked-work.query projection diverging from the memory adapter and the
// golden fixture: ReasonKind must be the sanitized reason class (not
// "blocked"), NextAction must be "review-resolution" (a Drafting Table
// review operation, not the Materializer's "resolve-block"), and the
// resolution options must include "defer" and "acknowledge" for every
// blocked item, not only those with a non-empty BlockReason.
func TestBlockedWorkQueryMatchesGoldenFixtureShape(t *testing.T) {
	fake := NewFakeClient()
	wms := newTestAdapter(t, fake)

	// An unresolved reason containing "undefined" sanitizes to the
	// undefined-behavior class; a readiness blocker without it sanitizes
	// to unresolved-precondition.
	undefinedBlocked := testWorkItem("wi-blocked-undefined", validation.StateInitial, 0)
	undefinedBlocked.Readiness.UnresolvedReasons = []string{"undefined behavior observed in the source contract"}
	if result := wms.Execute(materializeCall(undefinedBlocked, "mat-blocked-undefined", "mat-blocked-undefined-key")); !result.OK {
		t.Fatalf("materialize undefined-behavior blocked = %#v", result)
	}
	preconditionBlocked := testWorkItem("wi-blocked-precondition", validation.StateInitial, 0)
	preconditionBlocked.Readiness.ImpactDispositioned = false
	if result := wms.Execute(materializeCall(preconditionBlocked, "mat-blocked-precondition", "mat-blocked-precondition-key")); !result.OK {
		t.Fatalf("materialize unresolved-precondition blocked = %#v", result)
	}

	query := wms.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationBlockedWorkQuery),
		ActorContextRef: "drafting-table",
	})
	if !query.OK {
		t.Fatalf("blocked-work query = %#v", query)
	}
	if len(query.Items) != 2 {
		t.Fatalf("blocked items = %d, want 2", len(query.Items))
	}
	byID := map[string]adapter.WorkItemProjection{}
	for _, item := range query.Items {
		byID[item.ID] = item
	}
	undefined := byID["wi-blocked-undefined"]
	if undefined.ReasonKind != "undefined-behavior" {
		t.Fatalf("reason kind = %q, want undefined-behavior", undefined.ReasonKind)
	}
	precondition := byID["wi-blocked-precondition"]
	if precondition.ReasonKind != "unresolved-precondition" {
		t.Fatalf("reason kind = %q, want unresolved-precondition", precondition.ReasonKind)
	}
	for _, item := range query.Items {
		if item.NextAction != "review-resolution" {
			t.Fatalf("next action for %s = %q, want review-resolution", item.ID, item.NextAction)
		}
		wantOptions := []string{"add-requirement", "out-of-scope", "impact-amendment", "defer", "acknowledge"}
		if len(item.ResolutionOptions) != len(wantOptions) {
			t.Fatalf("resolution options for %s = %v, want %v", item.ID, item.ResolutionOptions, wantOptions)
		}
		for i, option := range wantOptions {
			if item.ResolutionOptions[i] != option {
				t.Fatalf("resolution option %d for %s = %q, want %q", i, item.ID, item.ResolutionOptions[i], option)
			}
		}
	}
}

// TestMaterializeScanFailureDoesNotCreateDuplicate covers the fix for
// loadMaterializationEntryLocked treating every non-transient scan error as
// "no binding": a permanent ListIssues failure is inconclusive, and
// proceeding to CreateIssue could orphan a second work-item issue under the
// same materialization_key. The fix only treats a definitive ErrNotFound as
// a clean no-match; every other scan error rejects WMS_UNAVAILABLE.
func TestMaterializeScanFailureDoesNotCreateDuplicate(t *testing.T) {
	fake := NewFakeClient()
	client := &scanFailOnceAfterClient{inner: fake, skip: 0}
	wms := newTestAdapterWithClient(t, client)
	candidate := testWorkItem("wi-scan-failure", validation.StateInitial, 0)

	result := wms.Execute(materializeCall(candidate, "mat-scan-failure-1", "mat-scan-failure-key"))
	if result.OK {
		t.Fatalf("materialize during scan failure = %#v, want rejection", result)
	}
	if result.Error == nil || result.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("materialize scan failure error = %#v, want WMS_UNAVAILABLE", result.Error)
	}
	if fake.IssueCount() != 0 {
		t.Fatalf("issues after scan failure = %d, want 0 (must not create a duplicate)", fake.IssueCount())
	}

	// The rejection must not be frozen: after the scan recovers, the same
	// call must apply normally.
	retry := wms.Execute(materializeCall(candidate, "mat-scan-failure-2", "mat-scan-failure-key"))
	if !retry.OK || retry.Outcome != adapter.OutcomeApplied {
		t.Fatalf("materialize after scan recovery = %#v, want applied", retry)
	}
	if fake.IssueCount() != 1 {
		t.Fatalf("issues after recovery = %d, want 1", fake.IssueCount())
	}
}

// TestRequestOperationsHydrationScanFailureReturnsWMSUnavailable verifies a
// failed hydration scan is reported as WMS_UNAVAILABLE, never served as an
// empty success or a NOT_FOUND frozen under a mutation's idempotency key.
func TestRequestOperationsHydrationScanFailureReturnsWMSUnavailable(t *testing.T) {
	fake := NewFakeClient()
	first := newTestAdapter(t, fake)
	createGithubTestRequest(t, first, "Hydration Failure Request", "scan must not look empty")

	client := &scanFailOnceAfterClient{inner: fake, skip: 0}
	second := newTestAdapterWithClient(t, client)

	query := second.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{}),
	})
	if query.OK || query.Error == nil || query.Error.Code != adapter.CodeWMSUnavailable {
		t.Fatalf("request query during scan failure = %#v, want WMS_UNAVAILABLE (not an empty list)", query)
	}

	// Recovery: the same adapter serves the durable set once the scan works.
	recovered := second.Execute(adapter.CallRequest{
		Operation:       string(validation.OperationRequestQuery),
		ActorContextRef: "drafting-table",
		Payload:         jsonPayload(t, map[string]any{}),
	})
	if !recovered.OK || len(recovered.Requests) != 1 {
		t.Fatalf("request query after scan recovery = %#v, want the durable request", recovered)
	}
}
