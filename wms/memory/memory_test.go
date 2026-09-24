package memory

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/redhat-et/protobot/wms/validation"
)

var memoryTestTime = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func TestConcurrentClaimsOnlyCommitOnce(t *testing.T) {
	gate := StaticGate{
		"job-site-a": testAuthorization("job-site-a", validation.RoleJobSite, validation.OperationClaim),
		"job-site-b": testAuthorization("job-site-b", validation.RoleJobSite, validation.OperationClaim),
	}
	memory := newTestMemory(t, gate, "")
	item := testWorkItem("wi-claim", validation.StateReadyForBuilding, 4)
	if err := memory.SeedWorkItem(item); err != nil {
		t.Fatal(err)
	}
	version := uint64(4)
	results := make(chan Result, 2)
	var group sync.WaitGroup
	for _, actor := range []string{"job-site-a", "job-site-b"} {
		group.Add(1)
		go func(actor string) {
			defer group.Done()
			results <- memory.Execute(CallRequest{
				Operation:               string(validation.OperationClaim),
				ActorContextRef:         actor,
				WorkItemID:              item.ID,
				ExpectedState:           validation.StateReadyForBuilding,
				ExpectedContractVersion: &version,
				IdempotencyKey:          "claim-" + actor,
			})
		}(actor)
	}
	group.Wait()
	close(results)

	var allowed, duplicate int
	for result := range results {
		if result.OK {
			allowed++
			if result.Mutation != mutationApplied {
				t.Errorf("successful claim mutation = %q, want applied", result.Mutation)
			}
			continue
		}
		if result.Error == nil || result.Error.Code != validation.CodeDuplicateClaim {
			t.Errorf("losing claim result = %#v, want DUPLICATE_CLAIM", result)
		}
		if result.Mutation != mutationNone {
			t.Errorf("rejected claim mutation = %q, want none", result.Mutation)
		}
		duplicate++
	}
	if allowed != 1 || duplicate != 1 {
		t.Fatalf("claim results: allowed=%d duplicate=%d, want 1/1", allowed, duplicate)
	}
	stored, ok := memory.WorkItem(item.ID)
	if !ok || stored.State != validation.StateBuilding || stored.ContractVersion != 5 || stored.Lease == nil {
		t.Fatalf("stored work item after concurrent claims = %#v, want one building lease at version 5", stored)
	}
	if len(memory.Events()) != 1 {
		t.Fatalf("audit events = %d, want exactly one accepted claim", len(memory.Events()))
	}
}

func TestStaleLifecycleWriteDoesNotMutateWorkItem(t *testing.T) {
	gate := StaticGate{
		"job-site": testAuthorization("job-site", validation.RoleJobSite, validation.OperationClaim),
	}
	memory := newTestMemory(t, gate, "")
	item := testWorkItem("wi-stale", validation.StateReadyForBuilding, 5)
	if err := memory.SeedWorkItem(item); err != nil {
		t.Fatal(err)
	}
	staleVersion := uint64(4)
	result := memory.Execute(CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           validation.StateReadyForBuilding,
		ExpectedContractVersion: &staleVersion,
		IdempotencyKey:          "stale-claim",
	})
	if result.OK || result.Error == nil || result.Error.Code != validation.CodeStaleContractVersion {
		t.Fatalf("stale write result = %#v, want STALE_CONTRACT_VERSION", result)
	}
	stored, _ := memory.WorkItem(item.ID)
	if stored.State != item.State || stored.ContractVersion != item.ContractVersion || stored.Lease != nil {
		t.Fatalf("stale write mutated work item: %#v", stored)
	}
	if len(memory.Events()) != 0 {
		t.Fatalf("stale write recorded %d lifecycle events, want none", len(memory.Events()))
	}
}

func TestLifecycleTargetVisibilityPrecedesOperationAuthorization(t *testing.T) {
	gate := StaticGate{
		"drafting-table": testAuthorization(
			"drafting-agent",
			validation.RoleDraftingTable,
			validation.OperationLifecyclePreflight,
		),
	}
	memory := newTestMemory(t, gate, "")
	version := uint64(4)
	result := memory.Execute(CallRequest{
		Operation:               string(validation.OperationClaim),
		ActorContextRef:         "drafting-table",
		WorkItemID:              "not-visible",
		ExpectedState:           validation.StateReadyForBuilding,
		ExpectedContractVersion: &version,
		IdempotencyKey:          "missing-target-claim",
	})
	if result.OK || result.Error == nil || result.Error.Code != validation.CodeNotFound || result.Decision == nil {
		t.Fatalf("missing target result = %#v, want a structured NOT_FOUND decision", result)
	}
	if result.Decision.Authority != validation.AuthorityAuthoritative ||
		result.Decision.RuleVersion != validation.RuleVersion ||
		result.Decision.PolicyVersion != "wms-policy/v1" {
		t.Fatalf("missing target decision metadata = %#v", result.Decision)
	}
	if len(memory.Events()) != 0 {
		t.Fatalf("missing target recorded %d lifecycle events, want none", len(memory.Events()))
	}
}

func TestMaterializationReplaysOriginalResultAndRejectsSourceConflict(t *testing.T) {
	gate := StaticGate{
		"materializer": testAuthorization("materializer", validation.RoleMaterializer, validation.OperationMaterialize),
	}
	memory := newTestMemory(t, gate, "materializer")
	candidate := testWorkItem("wi-materialize", validation.StateInitial, 0)
	candidate.ChangeType = "undefined"
	call := materializeCall(candidate, "materialize-command-1", "logical-work-1")

	first := memory.Execute(call)
	if !first.OK || first.Outcome != outcomeApplied || first.ContractVersion != 1 || first.WorkItemState != validation.StateReadyForBuilding {
		t.Fatalf("materialization result = %#v, want applied ready item at version 1", first)
	}
	firstItem, _ := memory.WorkItem(candidate.ID)

	replay := memory.Execute(call)
	if !replay.OK || replay.Outcome != outcomeReplayed || replay.Mutation != mutationNone || replay.Decision == nil || !replay.Decision.Replayed {
		t.Fatalf("same-key materialization replay = %#v, want original result replay", replay)
	}
	if replay.WorkItemState != first.WorkItemState || replay.ContractVersion != first.ContractVersion {
		t.Fatalf("replay result = (%q, %d), want original (%q, %d)", replay.WorkItemState, replay.ContractVersion, first.WorkItemState, first.ContractVersion)
	}

	newCommand := materializeCall(candidate, "materialize-command-2", "logical-work-1")
	logicalReplay := memory.Execute(newCommand)
	if !logicalReplay.OK || logicalReplay.Outcome != outcomeReplayed || logicalReplay.Decision == nil || !logicalReplay.Decision.Replayed {
		t.Fatalf("same-source materialization replay = %#v, want create-or-return replay", logicalReplay)
	}
	if len(memory.Events()) != 1 {
		t.Fatalf("materialization replays recorded %d events, want one", len(memory.Events()))
	}

	changedSource := candidate
	changedSource.Readiness.PolicyCompatible = false
	conflict := memory.Execute(materializeCall(changedSource, "materialize-command-3", "logical-work-1"))
	if conflict.OK || conflict.Error == nil || conflict.Error.Code != validation.CodeIdempotencyConflict || conflict.Mutation != mutationNone {
		t.Fatalf("different source reuse = %#v, want mutation-free IDEMPOTENCY_CONFLICT", conflict)
	}
	after, _ := memory.WorkItem(candidate.ID)
	if after.ContractVersion != firstItem.ContractVersion || after.State != firstItem.State || len(memory.Events()) != 1 {
		t.Fatalf("source conflict mutated the materialized item: %#v", after)
	}
}

func TestMaterializationCannotReplaceExistingWorkItemID(t *testing.T) {
	for _, state := range []validation.State{validation.StateBuilding, validation.StateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			gate := StaticGate{
				"materializer": testAuthorization("materializer", validation.RoleMaterializer, validation.OperationMaterialize),
			}
			memory := newTestMemory(t, gate, "materializer")
			existing := testWorkItem("wi-existing", state, 12)
			if state == validation.StateBuilding {
				setConformanceLease(&existing, "job-site", "fence-existing", memoryTestTime.Add(time.Hour))
			}
			if err := memory.SeedWorkItem(existing); err != nil {
				t.Fatal(err)
			}

			candidate := testWorkItem(existing.ID, validation.StateInitial, 0)
			candidate.ChangeType = "undefined"
			result := memory.Execute(materializeCall(candidate, "new-materialization-command", "new-materialization-key"))
			assertRejectedDecision(t, result, validation.AuthorityAuthoritative, validation.CodeIdempotencyConflict)
			assertItemUnchanged(t, memory, existing)
			assertEventCount(t, memory, 0)
		})
	}
}

func TestSupersedingResolutionWithSameApprovalKeepsApprovalUsable(t *testing.T) {
	memory, _ := newConformanceMemory(t)
	item := testWorkItem("wi-same-approval", validation.StateBlocked, 7)
	seedConformanceItem(t, memory, item)
	seedCompletedDependencyAndChangeSet(t, memory, "wi-same-approval-dependency", "CS-same-approval")
	approval := conformanceApproval(item, "same-approval", "human-same", "materializer-1", "add-requirement")
	if err := memory.SeedApproval(approval); err != nil {
		t.Fatal(err)
	}

	first := submitConformanceResolution(t, memory, item, approval.ID, approval.Digest, "CS-same-approval", "submit-same-approval-1")
	second := submitConformanceResolution(t, memory, item, approval.ID, approval.Digest, "CS-same-approval", "submit-same-approval-2")
	if !first.OK || !second.OK || first.ResolutionSubmissionID == second.ResolutionSubmissionID {
		t.Fatalf("same-approval submissions = %#v / %#v, want two accepted submissions", first, second)
	}
	if second.PriorSubmissionStatus != validation.ResolutionSubmissionStatusSuperseded || second.PriorApprovalStatus != validation.ApprovalStatusUnused {
		t.Fatalf("supersession result = %#v, want prior submission superseded and shared approval unused", second)
	}
	prior, _ := memory.Submission(first.ResolutionSubmissionID)
	current, _ := memory.Submission(second.ResolutionSubmissionID)
	storedApproval, _ := memory.Approval(approval.ID)
	if prior.Status != validation.ResolutionSubmissionStatusSuperseded || current.Status != validation.ResolutionSubmissionStatusPending || storedApproval.Status != validation.ApprovalStatusUnused {
		t.Fatalf("supersession state: prior=%#v current=%#v approval=%#v", prior, current, storedApproval)
	}

	resolved := memory.Execute(conformanceResolveCall(t, item, approval.ID, second.ResolutionSubmissionID, approval.Digest, "resolve-same-approval"))
	decision := assertAllowedDecision(t, resolved, validation.AuthorityAuthoritative)
	if decision.After.State != validation.StateReadyForBuilding || resolved.ApprovalStatus != validation.ApprovalStatusConsumed {
		t.Fatalf("same-approval resolve result = %#v, want ready and consumed", resolved)
	}
	assertEventCount(t, memory, 3)
}

func TestResolveBlockRefreshesLiveDependenciesAndPersistsSnapshot(t *testing.T) {
	memory, gate := newConformanceMemory(t)
	dependency := testWorkItem("wi-live-dependency", validation.StateMerging, 9)
	expectedMerge := &validation.MergeEnvelope{
		ProductTreeDigest: "tree-live-dependency",
		InspectionRunID:   "inspection-live-dependency",
		IntegrationHead:   "integration-live-dependency",
		Target:            "main",
		ContractVersion:   9,
	}
	dependency.ExpectedMerge = expectedMerge
	setConformanceLease(&dependency, "job-site", "fence-live-dependency", memoryTestTime.Add(time.Hour))
	if err := memory.SeedWorkItem(dependency); err != nil {
		t.Fatal(err)
	}

	blocked := testWorkItem("wi-live-dependency-blocked", validation.StateBlocked, 7)
	blocked.Dependencies = []validation.Dependency{{ID: dependency.ID, State: validation.StateWaiting}}
	seedConformanceItem(t, memory, blocked)
	seedCompletedDependencyAndChangeSet(t, memory, "wi-live-planned-dependency", "CS-live-dependency")
	approval := conformanceApproval(blocked, "live-dependency-approval", "human-live-dependency", "materializer-1", "add-requirement")
	if err := memory.SeedApproval(approval); err != nil {
		t.Fatal(err)
	}
	submitted := submitConformanceResolution(t, memory, blocked, approval.ID, approval.Digest, "CS-live-dependency", "live-dependency-submit")
	if !submitted.OK {
		t.Fatalf("resolution submission = %#v, want accepted", submitted)
	}

	firstResolve := memory.Execute(conformanceResolveCall(t, blocked, approval.ID, submitted.ResolutionSubmissionID, approval.Digest, "live-dependency-resolve-before"))
	assertRejectedDecision(t, firstResolve, validation.AuthorityAuthoritative, validation.CodePreconditionFailed)
	assertItemUnchanged(t, memory, blocked)
	approvalAfterFailure, _ := memory.Approval(approval.ID)
	if approvalAfterFailure.Status != validation.ApprovalStatusUnused {
		t.Fatalf("failed resolve consumed approval: %#v", approvalAfterFailure)
	}

	merge := *expectedMerge
	merge.MergeCommit = "merge-live-dependency"
	setConformanceAllowedRefs(gate, "job-site", merge.Target, merge.IntegrationHead, merge.MergeCommit, merge.InspectionRunID)
	completeDependency := conformanceCall(validation.OperationRecordMerge, "job-site", dependency, "live-dependency-complete")
	completeDependency.FencingToken = "fence-live-dependency"
	completeDependency.Payload = jsonPayload(t, validation.Payload{MergeEnvelope: &merge})
	completed := memory.Execute(completeDependency)
	assertAllowedDecision(t, completed, validation.AuthorityAuthoritative)

	preflightPayload := validation.Payload{
		ResolutionKind:         "add-requirement",
		ChangeSetID:            "CS-live-dependency",
		HumanApprovalID:        approval.ID,
		ApprovalDigest:         approval.Digest,
		ResolutionSubmissionID: submitted.ResolutionSubmissionID,
	}
	preflight := memory.Execute(preflightLifecycleCall(t, "materializer", blocked, validation.OperationResolveBlock, "", preflightPayload))
	preflightDecision := assertPreflightDecision(t, preflight, validation.OutcomeAllowed)

	retry := conformanceResolveCall(t, blocked, approval.ID, submitted.ResolutionSubmissionID, approval.Digest, "live-dependency-resolve-after")
	resolved := memory.Execute(retry)
	decision := assertAllowedDecision(t, resolved, validation.AuthorityAuthoritative)
	if !reflect.DeepEqual(preflightDecision.After, decision.After) {
		t.Fatalf("resolve-block preflight after = %#v, authoritative after = %#v", preflightDecision.After, decision.After)
	}
	stored, _ := memory.WorkItem(blocked.ID)
	if decision.After.State != validation.StateReadyForBuilding || len(stored.Dependencies) != 1 || stored.Dependencies[0].State != validation.StateCompleted {
		t.Fatalf("resolve-block result=%#v stored dependencies=%#v, want ready with completed dependency", resolved, stored.Dependencies)
	}

	claim := memory.Execute(conformanceCall(validation.OperationClaim, "job-site", stored, "live-dependency-claim"))
	claimDecision := assertAllowedDecision(t, claim, validation.AuthorityAuthoritative)
	if claimDecision.After.State != validation.StateBuilding {
		t.Fatalf("claim after resolve-block = %#v, want building", claimDecision.After)
	}
	assertEventCount(t, memory, 4)
}

func TestOutOfScopeResolutionUsesObservedInspectorConfirmation(t *testing.T) {
	memory, _ := newConformanceMemory(t)
	item := testWorkItem("wi-out-of-scope-confirmation", validation.StateBlocked, 7)
	seedConformanceItem(t, memory, item)
	approval := conformanceApproval(item, "out-of-scope-approval", "human-out-of-scope", "materializer-1", "out-of-scope")
	if err := memory.SeedApproval(approval); err != nil {
		t.Fatal(err)
	}

	version := item.ContractVersion
	submitted := memory.Execute(CallRequest{
		Operation:               "blocked-work.submit-resolution",
		ActorContextRef:         "drafting-table",
		WorkItemID:              item.ID,
		ExpectedState:           validation.StateBlocked,
		ExpectedContractVersion: &version,
		HumanApprovalID:         approval.ID,
		IdempotencyKey:          "out-of-scope-submit",
		Payload: jsonPayload(t, blockedResolutionPayload{
			ResolutionKind:           "out-of-scope",
			ApprovalResolutionDigest: approval.Digest,
		}),
	})
	if !submitted.OK || submitted.ResolutionSubmissionID == "" {
		t.Fatalf("out-of-scope submission = %#v, want accepted submission", submitted)
	}

	forged := conformanceCall(validation.OperationResolveBlock, "materializer", item, "out-of-scope-forged-confirmation")
	forged.HumanApprovalID = approval.ID
	forged.Payload = jsonPayload(t, map[string]any{
		"resolution_kind":                 "out-of-scope",
		"human_approval_id":               approval.ID,
		"approval_resolution_digest":      approval.Digest,
		"resolution_submission_id":        submitted.ResolutionSubmissionID,
		"independent_inspector_confirmed": true,
	})
	assertRequestRejection(t, memory.Execute(forged), CodeInvalidRequest)

	resolve := conformanceCall(validation.OperationResolveBlock, "materializer", item, "out-of-scope-resolve-before-confirmation")
	resolve.HumanApprovalID = approval.ID
	resolve.Payload = jsonPayload(t, validation.Payload{
		ResolutionKind:         "out-of-scope",
		HumanApprovalID:        approval.ID,
		ApprovalDigest:         approval.Digest,
		ResolutionSubmissionID: submitted.ResolutionSubmissionID,
	})
	firstResolve := memory.Execute(resolve)
	decision := assertRejectedDecision(t, firstResolve, validation.AuthorityAuthoritative, validation.CodePreconditionFailed)
	if decision.Rejection.Details["required_evidence"] != "independent-inspector-confirmation" {
		t.Fatalf("out-of-scope rejection = %#v, want missing Inspector confirmation", decision.Rejection)
	}
	assertItemUnchanged(t, memory, item)
	approvalAfterFailure, _ := memory.Approval(approval.ID)
	if approvalAfterFailure.Status != validation.ApprovalStatusUnused {
		t.Fatalf("failed out-of-scope resolve consumed approval: %#v", approvalAfterFailure)
	}

	preflightPayload := validation.Payload{ResolutionSubmissionID: submitted.ResolutionSubmissionID}
	preflight := memory.Execute(preflightLifecycleCall(t, "materializer", item, validation.OperationResolveBlock, "", preflightPayload))
	preflightDecision := assertPreflightDecision(t, preflight, validation.OutcomeRejected)
	if preflightDecision.Rejection == nil || preflightDecision.Rejection.Details["required_evidence"] != "independent-inspector-confirmation" {
		t.Fatalf("unconfirmed out-of-scope preflight = %#v, want missing Inspector confirmation", preflightDecision.Rejection)
	}

	const confirmationID = "finding-event-out-of-scope-confirmed"
	if err := memory.ObserveIndependentInspectorConfirmation(submitted.ResolutionSubmissionID, confirmationID); err != nil {
		t.Fatal(err)
	}
	if err := memory.ObserveIndependentInspectorConfirmation(submitted.ResolutionSubmissionID, confirmationID); err != nil {
		t.Fatalf("replaying confirmation observation: %v", err)
	}
	storedSubmission, _ := memory.Submission(submitted.ResolutionSubmissionID)
	if !storedSubmission.IndependentInspectorConfirmed || storedSubmission.IndependentInspectorConfirmationID != confirmationID || storedSubmission.Revision != 2 {
		t.Fatalf("observed submission = %#v, want recorded confirmation at revision 2", storedSubmission)
	}

	preflight = memory.Execute(preflightLifecycleCall(t, "materializer", item, validation.OperationResolveBlock, "", preflightPayload))
	preflightDecision = assertPreflightDecision(t, preflight, validation.OutcomeAllowed)

	resolve.IdempotencyKey = "out-of-scope-resolve-after-confirmation"
	resolved := memory.Execute(resolve)
	resolvedDecision := assertAllowedDecision(t, resolved, validation.AuthorityAuthoritative)
	if !reflect.DeepEqual(preflightDecision.After, resolvedDecision.After) || resolved.ApprovalStatus != validation.ApprovalStatusConsumed {
		t.Fatalf("confirmed out-of-scope resolve = %#v preflight after=%#v, want matching allowed result and consumed approval", resolved, preflightDecision.After)
	}
	assertEventCount(t, memory, 2)
}

func TestWMSFailureCodesAndRetryGuidanceUseStableValues(t *testing.T) {
	gate := conformanceGate()
	jobSite := gate["job-site"]
	jobSite.AllowedActions = append(jobSite.AllowedActions, validation.Operation("finding.create"))
	gate["job-site"] = jobSite
	memory := newConformanceMemoryWithGate(t, gate)

	invalid := memory.Execute(CallRequest{
		Operation:       "request.create",
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "invalid-request",
		Payload:         jsonPayload(t, createRequestPayload{Rationale: "Missing intent."}),
	})
	if invalid.Error == nil || invalid.Error.Code != CodeInvalidRequest || invalid.Error.Retry != validation.RetryNewKey {
		t.Fatalf("invalid request rejection = %#v, want INVALID_REQUEST with new-key retry", invalid.Error)
	}

	unavailable := memory.Execute(CallRequest{Operation: "finding.create", ActorContextRef: "job-site"})
	if unavailable.Error == nil || unavailable.Error.Code != CodeWMSUnavailable || unavailable.Error.Retry != validation.RetryRefresh {
		t.Fatalf("unavailable WMS rejection = %#v, want WMS_UNAVAILABLE with refresh retry", unavailable.Error)
	}

	requestPayload := createRequestPayload{Intent: "Record a request", Rationale: "Exercise stable WMS errors."}
	created := memory.Execute(CallRequest{
		Operation:       "request.create",
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "stable-error-create",
		Payload:         jsonPayload(t, requestPayload),
	})
	if !created.OK {
		t.Fatalf("create request result = %#v, want success", created)
	}
	duplicate := memory.Execute(CallRequest{
		Operation:       "request.create",
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "stable-error-duplicate",
		Payload:         jsonPayload(t, requestPayload),
	})
	if duplicate.Error == nil || duplicate.Error.Code != CodeDuplicateRequest || duplicate.Error.Retry != validation.RetryQuery {
		t.Fatalf("duplicate request rejection = %#v, want DUPLICATE_REQUEST with query retry", duplicate.Error)
	}

	staleRevision := uint64(0)
	stale := memory.Execute(CallRequest{
		Operation:               "request.link-change-set",
		ActorContextRef:         "drafting-table",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &staleRevision,
		IdempotencyKey:          "stable-error-stale-revision",
	})
	if stale.Error == nil || stale.Error.Code != CodeStaleRequestRevision || stale.Error.Retry != validation.RetryRefresh {
		t.Fatalf("stale request rejection = %#v, want STALE_REQUEST_REVISION with refresh retry", stale.Error)
	}
}

func TestRequestLinkAuditIncludesActorAndPolicyVersion(t *testing.T) {
	memory, _ := newConformanceMemory(t)
	if err := memory.SeedChangeSet(ChangeSet{ID: "CS-audit", Revision: "proposed"}); err != nil {
		t.Fatal(err)
	}
	workItem := testWorkItem("wi-audit", validation.StateReadyForBuilding, 1)
	if err := memory.SeedWorkItem(workItem); err != nil {
		t.Fatal(err)
	}
	created := memory.Execute(CallRequest{
		Operation:       "request.create",
		ActorContextRef: "drafting-table",
		IdempotencyKey:  "audit-request-create",
		Payload:         jsonPayload(t, createRequestPayload{Intent: "Record a WMS request", Rationale: "Exercise link auditing."}),
	})
	if !created.OK {
		t.Fatalf("request create result = %#v", created)
	}

	requestRevision := created.RequestRevision
	changeSetLink := memory.Execute(CallRequest{
		Operation:               "request.link-change-set",
		ActorContextRef:         "drafting-table",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &requestRevision,
		IdempotencyKey:          "audit-link-change-set",
		Payload: jsonPayload(t, changeSetLinkPayload{
			ChangeSetID:    "CS-audit",
			TargetRevision: "proposed",
		}),
	})
	if !changeSetLink.OK {
		t.Fatalf("change-set link result = %#v", changeSetLink)
	}
	requestRevision = changeSetLink.RequestRevision
	workItemLink := memory.Execute(CallRequest{
		Operation:               "request.link-build-work-item",
		ActorContextRef:         "drafting-table",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &requestRevision,
		IdempotencyKey:          "audit-link-work-item",
		Payload:                 jsonPayload(t, workItemLinkPayload{BuildWorkItemID: workItem.ID}),
	})
	if !workItemLink.OK {
		t.Fatalf("work-item link result = %#v", workItemLink)
	}

	events := memory.Events()
	if len(events) != 3 {
		t.Fatalf("request and link audit events = %#v, want three events", events)
	}
	for _, index := range []int{1, 2} {
		if events[index].Subject != "drafting-agent" || events[index].PolicyVersion != "wms-policy/v1" || events[index].RequestID != created.RequestID {
			t.Errorf("link audit event %d = %#v, want subject, policy, and request identity", index, events[index])
		}
	}
	if events[1].Operation != "request.link-change-set" || events[2].Operation != "request.link-build-work-item" || events[2].WorkItemID != workItem.ID {
		t.Fatalf("link audit event identities = %#v / %#v", events[1], events[2])
	}
}

func TestRefinementRejectsWhitespaceOnlyIntentAndRationale(t *testing.T) {
	for _, field := range []string{"intent", "rationale"} {
		t.Run(field, func(t *testing.T) {
			memory, _ := newConformanceMemory(t)
			created := createTestRequest(t, memory, "Keep this request", "Keep this rationale", nil, nil, "blank-refine-create")
			if !created.OK {
				t.Fatalf("request create result = %#v", created)
			}
			before, _ := memory.Request(created.RequestID)
			approval := seedRefinementApproval(t, memory, "blank-refine-approval", created.RequestID, "blank-refine-digest")

			payload := refineRequestPayload{
				Classification:           "changes",
				RefinementState:          "ready-for-dimensioning",
				HumanApprovalID:          approval.ID,
				ApprovalRefinementDigest: approval.Digest,
			}
			if field == "intent" {
				payload.Intent = "   "
			} else {
				payload.Rationale = "   "
			}
			result := memory.Execute(refineTestRequest(t, created.RequestID, created.RequestRevision, "blank-refine-"+field, payload))
			if result.OK || result.Error == nil || result.Error.Code != CodeInvalidRequest || result.Mutation != mutationNone {
				t.Fatalf("blank %s refinement = %#v, want mutation-free INVALID_REQUEST", field, result)
			}
			after, _ := memory.Request(created.RequestID)
			approvalAfter, _ := memory.Approval(approval.ID)
			if !reflect.DeepEqual(after, before) || approvalAfter.Status != validation.ApprovalStatusUnused {
				t.Fatalf("blank refinement changed request or consumed approval: request=%#v approval=%#v", after, approvalAfter)
			}
			if len(memory.Events()) != 1 {
				t.Fatalf("blank refinement recorded %d events, want only request creation", len(memory.Events()))
			}
		})
	}
}

func TestRefinementMaintainsSemanticDuplicateIndex(t *testing.T) {
	memory, _ := newConformanceMemory(t)
	first := createTestRequest(t, memory, "Request alpha", "Alpha rationale", []string{"interface-a"}, []string{"scope-a"}, "semantic-create-alpha")
	second := createTestRequest(t, memory, "Request beta", "Beta rationale", []string{"interface-b"}, []string{"scope-b"}, "semantic-create-beta")
	if !first.OK || !second.OK {
		t.Fatalf("request creates = %#v / %#v", first, second)
	}

	firstBefore, _ := memory.Request(first.RequestID)
	duplicateApproval := seedRefinementApproval(t, memory, "semantic-duplicate-approval", first.RequestID, "semantic-duplicate-digest")
	duplicatePayload := refineRequestPayload{
		Intent:                   "Request beta",
		AffectedInterfaces:       []string{"interface-b"},
		AffectedScopes:           []string{"scope-b"},
		Classification:           "changes",
		RefinementState:          "ready-for-dimensioning",
		HumanApprovalID:          duplicateApproval.ID,
		ApprovalRefinementDigest: duplicateApproval.Digest,
	}
	duplicate := memory.Execute(refineTestRequest(t, first.RequestID, first.RequestRevision, "semantic-refine-duplicate", duplicatePayload))
	if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != CodeDuplicateRequest || duplicate.Mutation != mutationNone {
		t.Fatalf("duplicate refinement = %#v, want mutation-free DUPLICATE_REQUEST", duplicate)
	}
	unchanged, _ := memory.Request(first.RequestID)
	approvalAfterDuplicate, _ := memory.Approval(duplicateApproval.ID)
	if !reflect.DeepEqual(unchanged, firstBefore) || approvalAfterDuplicate.Status != validation.ApprovalStatusUnused {
		t.Fatalf("duplicate refinement changed request or consumed approval: request=%#v approval=%#v", unchanged, approvalAfterDuplicate)
	}

	refinementApproval := seedRefinementApproval(t, memory, "semantic-refinement-approval", first.RequestID, "semantic-refinement-digest")
	refinement := refineRequestPayload{
		Intent:                   "Request gamma",
		AffectedInterfaces:       []string{"interface-c"},
		AffectedScopes:           []string{"scope-c"},
		Classification:           "changes",
		RefinementState:          "ready-for-dimensioning",
		HumanApprovalID:          refinementApproval.ID,
		ApprovalRefinementDigest: refinementApproval.Digest,
	}
	updated := memory.Execute(refineTestRequest(t, first.RequestID, first.RequestRevision, "semantic-refine-gamma", refinement))
	if !updated.OK {
		t.Fatalf("unique refinement = %#v, want success", updated)
	}

	oldContent := createTestRequest(t, memory, "Request alpha", "A second alpha request", []string{"interface-a"}, []string{"scope-a"}, "semantic-create-old-content")
	if !oldContent.OK {
		t.Fatalf("create using refined-away semantic key = %#v, want success", oldContent)
	}
	newDuplicate := createTestRequest(t, memory, "Request gamma", "Another gamma request", []string{"interface-c"}, []string{"scope-c"}, "semantic-create-new-duplicate")
	if newDuplicate.OK || newDuplicate.Error == nil || newDuplicate.Error.Code != CodeDuplicateRequest {
		t.Fatalf("create using current refined semantic key = %#v, want DUPLICATE_REQUEST", newDuplicate)
	}
	if got := newDuplicate.Error.Details["request_id"]; got != first.RequestID {
		t.Fatalf("duplicate request_id = %#v, want refined request %q", got, first.RequestID)
	}
	if len(memory.Events()) != 4 {
		t.Fatalf("semantic refinement events = %d, want two creates, one refine, and old-key create", len(memory.Events()))
	}
}

func TestRequestLinkBackfillsPrioritySetBeforeLinking(t *testing.T) {
	memory, _ := newConformanceMemory(t)
	if err := memory.SeedChangeSet(ChangeSet{ID: "CS-priority", Revision: "proposed"}); err != nil {
		t.Fatal(err)
	}
	workItem := testWorkItem("wi-priority", validation.StateReadyForBuilding, 1)
	if err := memory.SeedWorkItem(workItem); err != nil {
		t.Fatal(err)
	}
	created := createTestRequest(t, memory, "Prioritize the linked work", "Exercise priority snapshots.", nil, nil, "priority-link-create")
	if !created.OK {
		t.Fatalf("request create result = %#v", created)
	}

	priority := memory.Execute(CallRequest{
		Operation:               "request.update-priority",
		ActorContextRef:         "human-maintainer",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &created.RequestRevision,
		IdempotencyKey:          "priority-before-link",
		Payload:                 jsonPayload(t, priorityPayload{BusinessPriority: "high"}),
	})
	if !priority.OK {
		t.Fatalf("priority update before linking = %#v", priority)
	}

	requestRevision := priority.RequestRevision
	changeSetLink := memory.Execute(CallRequest{
		Operation:               "request.link-change-set",
		ActorContextRef:         "drafting-table",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &requestRevision,
		IdempotencyKey:          "priority-link-change-set",
		Payload: jsonPayload(t, changeSetLinkPayload{
			ChangeSetID:    "CS-priority",
			TargetRevision: "proposed",
		}),
	})
	if !changeSetLink.OK {
		t.Fatalf("change-set link = %#v", changeSetLink)
	}
	requestRevision = changeSetLink.RequestRevision
	workItemLink := memory.Execute(CallRequest{
		Operation:               "request.link-build-work-item",
		ActorContextRef:         "drafting-table",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &requestRevision,
		IdempotencyKey:          "priority-link-work-item",
		Payload:                 jsonPayload(t, workItemLinkPayload{BuildWorkItemID: workItem.ID}),
	})
	if !workItemLink.OK {
		t.Fatalf("work-item link = %#v", workItemLink)
	}
	if got := memory.changeSets["CS-priority"].BusinessPriority; got != "high" {
		t.Fatalf("linked change-set priority = %q, want high", got)
	}
	linkedWorkItem, _ := memory.WorkItem(workItem.ID)
	if linkedWorkItem.Priority != "high" {
		t.Fatalf("linked work-item priority = %q, want high", linkedWorkItem.Priority)
	}

	requestRevision = workItemLink.RequestRevision
	updatedPriority := memory.Execute(CallRequest{
		Operation:               "request.update-priority",
		ActorContextRef:         "human-maintainer",
		RequestID:               created.RequestID,
		ExpectedRequestRevision: &requestRevision,
		IdempotencyKey:          "priority-after-link",
		Payload:                 jsonPayload(t, priorityPayload{BusinessPriority: "urgent"}),
	})
	if !updatedPriority.OK {
		t.Fatalf("priority update after linking = %#v", updatedPriority)
	}
	if got := memory.changeSets["CS-priority"].BusinessPriority; got != "urgent" {
		t.Fatalf("post-link change-set priority = %q, want urgent", got)
	}
	linkedWorkItem, _ = memory.WorkItem(workItem.ID)
	if linkedWorkItem.Priority != "urgent" {
		t.Fatalf("post-link work-item priority = %q, want urgent", linkedWorkItem.Priority)
	}
	if len(memory.Events()) != 5 {
		t.Fatalf("priority/link events = %d, want five accepted mutations", len(memory.Events()))
	}
}

func createTestRequest(
	t *testing.T,
	memory *Memory,
	intent, rationale string,
	affectedInterfaces, affectedScopes []string,
	key string,
) Result {
	t.Helper()
	return memory.Execute(CallRequest{
		Operation:       "request.create",
		ActorContextRef: "drafting-table",
		IdempotencyKey:  key,
		Payload: jsonPayload(t, createRequestPayload{
			Intent:             intent,
			Rationale:          rationale,
			AffectedInterfaces: affectedInterfaces,
			AffectedScopes:     affectedScopes,
		}),
	})
}

func seedRefinementApproval(t *testing.T, memory *Memory, id, requestID, digest string) validation.ApprovalRecord {
	t.Helper()
	approval := validation.ApprovalRecord{
		ID:                 id,
		ApprovedSubject:    "human-reviewer",
		DelegatedPrincipal: "drafting-agent",
		ProjectID:          "fixture-project",
		RequestID:          requestID,
		Digest:             digest,
		Action:             validation.Operation("request.refine"),
		PolicyVersion:      "wms-policy/v1",
		ExpiresAt:          memoryTestTime.Add(time.Hour),
		Status:             validation.ApprovalStatusUnused,
	}
	if err := memory.SeedApproval(approval); err != nil {
		t.Fatal(err)
	}
	return approval
}

func refineTestRequest(t *testing.T, requestID string, revision uint64, key string, payload refineRequestPayload) CallRequest {
	t.Helper()
	return CallRequest{
		Operation:               "request.refine",
		ActorContextRef:         "drafting-table",
		RequestID:               requestID,
		ExpectedRequestRevision: &revision,
		IdempotencyKey:          key,
		Payload:                 jsonPayload(t, payload),
	}
}

func TestCompletionReplayReturnsOriginalResult(t *testing.T) {
	jobSiteAuthorization := testAuthorization("job-site", validation.RoleJobSite, validation.OperationRecordMerge)
	jobSiteAuthorization.AllowedRefs = append(jobSiteAuthorization.AllowedRefs, "main", "integration-head", "merge-commit-1", "inspection-1")
	gate := StaticGate{
		"job-site": jobSiteAuthorization,
	}
	memory := newTestMemory(t, gate, "")
	expected := &validation.MergeEnvelope{
		ProductTreeDigest: "tree-digest",
		InspectionRunID:   "inspection-1",
		IntegrationHead:   "integration-head",
		Target:            "main",
		ContractVersion:   9,
	}
	item := testWorkItem("wi-complete", validation.StateMerging, 9)
	item.ExpectedMerge = expected
	item.InspectionRunSealed = true
	item.FindingsTerminal = true
	item.FinalTestsPassed = true
	item.Owner = "job-site"
	item.Lease = &validation.Lease{Owner: "job-site", FencingToken: "fence-9", ExpiresAt: memoryTestTime.Add(time.Hour)}
	if err := memory.SeedWorkItem(item); err != nil {
		t.Fatal(err)
	}
	version := uint64(9)
	merged := *expected
	merged.MergeCommit = "merge-commit-1"
	call := CallRequest{
		Operation:               string(validation.OperationRecordMerge),
		ActorContextRef:         "job-site",
		WorkItemID:              item.ID,
		ExpectedState:           validation.StateMerging,
		ExpectedContractVersion: &version,
		FencingToken:            "fence-9",
		IdempotencyKey:          "complete-1",
		Payload:                 jsonPayload(t, validation.Payload{MergeEnvelope: &merged}),
	}

	first := memory.Execute(call)
	if !first.OK || first.Outcome != outcomeApplied || first.WorkItemState != validation.StateCompleted || first.ContractVersion != 10 {
		t.Fatalf("completion result = %#v, want completed version 10", first)
	}
	replay := memory.Execute(call)
	if !replay.OK || replay.Outcome != outcomeReplayed || replay.Mutation != mutationNone || replay.Decision == nil || !replay.Decision.Replayed {
		t.Fatalf("completion replay = %#v, want original completion result", replay)
	}
	if replay.WorkItemState != first.WorkItemState || replay.ContractVersion != first.ContractVersion {
		t.Fatalf("completion replay = (%q, %d), want original (%q, %d)", replay.WorkItemState, replay.ContractVersion, first.WorkItemState, first.ContractVersion)
	}
	if len(memory.Events()) != 1 {
		t.Fatalf("completion replay recorded %d events, want one", len(memory.Events()))
	}
}

func TestDraftingTableOperationSetIsDisjointFromJobSiteExecution(t *testing.T) {
	drafting := stringSet(DraftingTableOperations())
	adapter := stringSet(AdapterOperations())
	jobSite := stringSet(JobSiteOperations())
	if len(drafting) == 0 || len(drafting) >= len(adapter) {
		t.Fatalf("Drafting Table set size=%d adapter set size=%d, want a proper subset", len(drafting), len(adapter))
	}
	for operation := range drafting {
		if _, exists := adapter[operation]; !exists {
			t.Errorf("Drafting Table operation %q is absent from the adapter API", operation)
		}
		if _, exists := jobSite[operation]; exists {
			t.Errorf("Drafting Table operation %q overlaps Job Site execution", operation)
		}
	}
	if _, exists := drafting[string(validation.OperationClaim)]; exists {
		t.Fatal("Drafting Table operation set contains claim")
	}
	if _, exists := drafting["finding.create"]; exists {
		t.Fatal("Drafting Table operation set contains Finding Ledger mutation")
	}
}

func newTestMemory(t *testing.T, gate Gate, materializer string) *Memory {
	t.Helper()
	memory, err := New(Config{
		ProjectID:           "fixture-project",
		Gate:                gate,
		Now:                 func() time.Time { return memoryTestTime },
		LeaseDuration:       15 * time.Minute,
		MaterializerSubject: materializer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return memory
}

func testAuthorization(subject string, role validation.Role, operations ...validation.Operation) validation.AuthorizationContext {
	return validation.AuthorizationContext{
		Subject:        subject,
		Role:           role,
		ProjectID:      "fixture-project",
		AllowedActions: append([]validation.Operation(nil), operations...),
		AllowedRefs:    []string{"project:fixture-project"},
		ExpiresAt:      memoryTestTime.Add(time.Hour),
		PolicyVersion:  "wms-policy/v1",
	}
}

func testWorkItem(id string, state validation.State, version uint64) validation.WorkItem {
	return validation.WorkItem{
		ID:                     id,
		ProjectID:              "fixture-project",
		State:                  state,
		ContractVersion:        version,
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

func materializeCall(item validation.WorkItem, idempotencyKey, materializationKey string) CallRequest {
	version := uint64(0)
	return CallRequest{
		Operation:               string(validation.OperationMaterialize),
		ActorContextRef:         "materializer",
		ExpectedState:           validation.StateInitial,
		ExpectedContractVersion: &version,
		IdempotencyKey:          idempotencyKey,
		MaterializationKey:      materializationKey,
		Payload:                 jsonPayloadWithoutError(validation.Payload{WorkItem: &item}),
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

func jsonPayloadWithoutError(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
