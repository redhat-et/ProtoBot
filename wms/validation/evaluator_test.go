package validation

import (
	"reflect"
	"testing"
	"time"
)

var evaluationTime = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func TestLifecycleTransitionMatrix(t *testing.T) {
	tests := validTransitionCases()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := Evaluate(test.request, test.current, test.context)
			if decision.Outcome != test.outcome {
				t.Fatalf("Evaluate outcome = %q, want %q; rejection: %#v", decision.Outcome, test.outcome, decision.Rejection)
			}
			if decision.RuleVersion != RuleVersion || decision.PolicyVersion != "policy/v1" {
				t.Fatalf("decision version metadata = (%q, %q), want (%q, %q)", decision.RuleVersion, decision.PolicyVersion, RuleVersion, "policy/v1")
			}
			if test.outcome == OutcomeOmitted {
				if decision.After != nil || decision.MaterializationReservation == nil || decision.MaterializationReservation.Outcome != MaterializationOutcomeOmitted {
					t.Fatalf("omitted materialization result is incomplete: %#v", decision)
				}
				return
			}
			if decision.After == nil || decision.After.State != test.state {
				t.Fatalf("Evaluate after state = %#v, want %q", decision.After, test.state)
			}
			if test.current == nil {
				if decision.After.ContractVersion != 1 {
					t.Fatalf("materialized version = %d, want 1", decision.After.ContractVersion)
				}
			} else if decision.After.ContractVersion != test.current.ContractVersion+1 {
				t.Fatalf("after version = %d, want %d", decision.After.ContractVersion, test.current.ContractVersion+1)
			}
			if test.tokenIssued && decision.FencingTokenIssued != test.context.NextFencingToken {
				t.Fatalf("issued fence = %q, want %q", decision.FencingTokenIssued, test.context.NextFencingToken)
			}
		})
	}
}

func TestInvalidLifecycleTransitionMatrix(t *testing.T) {
	tests := invalidTransitionCases()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := Evaluate(test.request, test.current, test.context)
			if decision.Outcome != OutcomeRejected {
				t.Fatalf("Evaluate outcome = %q, want rejected: %#v", decision.Outcome, decision)
			}
			if decision.Rejection == nil || decision.Rejection.Code != test.code {
				t.Fatalf("Evaluate rejection = %#v, want code %q", decision.Rejection, test.code)
			}
			if decision.After != nil {
				t.Fatalf("rejected operation proposed a mutation: %#v", decision.After)
			}
		})
	}
}

func TestPreflightIsAdvisoryAndDoesNotMutate(t *testing.T) {
	item := readyItem(StateReadyForBuilding, 4)
	original := cloneForTest(item)
	request, _ := requestFor(OperationClaim, RoleDraftingTable, &item)
	request.Authorization.AllowedActions = []Operation{OperationLifecyclePreflight}
	context := baseEvaluation(AuthorityPreflight)
	decision := Evaluate(request, &item, context)
	if decision.Outcome != OutcomeAllowed || decision.Authority != AuthorityPreflight {
		t.Fatalf("preflight decision = %#v, want advisory allow", decision)
	}
	if decision.After == nil || decision.After.State != StateBuilding || decision.After.ContractVersion != 5 {
		t.Fatalf("preflight did not describe the proposed transition: %#v", decision.After)
	}
	if !reflect.DeepEqual(item, original) {
		t.Fatal("preflight mutated the caller's work-item snapshot")
	}
}

func TestAuthorizationFailsClosed(t *testing.T) {
	valid := authorization(RoleJobSite, OperationClaim)
	valid.WorkItemID = "wi-1"
	tests := []struct {
		name string
		auth AuthorizationContext
		now  time.Time
		refs []string
	}{
		{name: "valid", auth: valid, now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "missing subject", auth: func() AuthorizationContext { auth := valid; auth.Subject = ""; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "unknown role", auth: func() AuthorizationContext { auth := valid; auth.Role = "worker"; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "expired", auth: valid, now: valid.ExpiresAt, refs: []string{"project:fixture-project"}},
		{name: "empty actions", auth: func() AuthorizationContext { auth := valid; auth.AllowedActions = nil; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "wildcard action", auth: func() AuthorizationContext { auth := valid; auth.AllowedActions = []Operation{"all"}; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "empty refs", auth: func() AuthorizationContext { auth := valid; auth.AllowedRefs = nil; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "wildcard ref", auth: func() AuthorizationContext { auth := valid; auth.AllowedRefs = []string{"*"}; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "project mismatch", auth: func() AuthorizationContext { auth := valid; auth.ProjectID = "other"; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "work item mismatch", auth: func() AuthorizationContext { auth := valid; auth.WorkItemID = "wi-other"; return auth }(), now: evaluationTime, refs: []string{"project:fixture-project"}},
		{name: "ref outside scope", auth: valid, now: evaluationTime, refs: []string{"project:fixture-project", "branch:other"}},
		{name: "missing clock", auth: valid, now: time.Time{}, refs: []string{"project:fixture-project"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rejection := AuthorizeContext(test.auth, OperationClaim, "fixture-project", "wi-1", "", test.refs, test.now)
			if test.name == "valid" {
				if rejection != nil {
					t.Fatalf("AuthorizeContext rejected valid context: %#v", rejection)
				}
				return
			}
			if rejection == nil || rejection.Code != CodeUnauthorizedAction {
				t.Fatalf("AuthorizeContext rejection = %#v, want %s", rejection, CodeUnauthorizedAction)
			}
		})
	}
}

func TestAuthorizationRoleFamiliesAreExclusive(t *testing.T) {
	auth := authorization(RoleDraftingTable, OperationClaim)
	if rejection := AuthorizeLifecycle(auth, OperationClaim, "fixture-project", "wi-1", "", nil, evaluationTime); rejection == nil || rejection.Code != CodeUnauthorizedAction {
		t.Fatalf("Drafting Table claim authorization = %#v, want %s", rejection, CodeUnauthorizedAction)
	}
	auth.AllowedActions = []Operation{OperationLifecyclePreflight}
	if rejection := AuthorizePreflight(auth, "fixture-project", "wi-1", "", nil, evaluationTime); rejection != nil {
		t.Fatalf("Drafting Table preflight authorization = %#v, want allowed", rejection)
	}
}

type matrixCase struct {
	name        string
	request     Request
	current     *WorkItem
	context     EvaluationContext
	outcome     Outcome
	state       State
	tokenIssued bool
	code        string
}

func validTransitionCases() []matrixCase {
	var tests []matrixCase
	add := func(test matrixCase) {
		if test.current != nil {
			item := cloneForTest(*test.current)
			test.current = &item
		}
		tests = append(tests, test)
	}

	for _, test := range []struct {
		name  string
		state State
		item  WorkItem
	}{
		{name: "ready", state: StateReadyForBuilding, item: readyItem(StateInitial, 0)},
		{name: "waiting", state: StateWaiting, item: readyItem(StateInitial, 0)},
		{name: "blocked", state: StateBlocked, item: readyItem(StateInitial, 0)},
	} {
		candidate := test.item
		candidate.ID = "wi-materialized"
		candidate.ProjectID = "fixture-project"
		candidate.ChangeType = "undefined"
		candidate.ImplementationRequired = true
		candidate.State = StateInitial
		candidate.ContractVersion = 0
		switch test.state {
		case StateWaiting:
			candidate.Dependencies = []Dependency{{ID: "wi-dep", State: StateWaiting}}
		case StateBlocked:
			candidate.Readiness.ImpactDispositioned = false
		}
		request, context := materializeRequest(candidate)
		add(matrixCase{name: "materialize-" + string(test.state), request: request, context: context, outcome: OutcomeAllowed, state: test.state})
	}
	omitted := readyItem(StateInitial, 0)
	omitted.ID = "wi-omitted"
	omitted.ProjectID = "fixture-project"
	omitted.ChangeType = "changes"
	omitted.ImplementationRequired = false
	request, context := materializeRequest(omitted)
	add(matrixCase{name: "materialize-omitted", request: request, context: context, outcome: OutcomeOmitted})

	waiting := readyItem(StateWaiting, 4)
	waiting.Dependencies = []Dependency{{ID: "wi-dep", State: StateCompleted}}
	request, context = requestFor(OperationRefreshDependencies, RoleMaterializer, &waiting)
	add(matrixCase{name: "refresh-dependencies", request: request, current: &waiting, context: context, outcome: OutcomeAllowed, state: StateReadyForBuilding})

	for _, state := range []State{StateWaiting, StateReadyForBuilding} {
		item := readyItem(state, 4)
		item.Readiness.UnresolvedReasons = []string{"unresolved impact review"}
		request, context = requestFor(OperationRevalidate, RoleMaterializer, &item)
		add(matrixCase{name: "revalidate-" + string(state), request: request, current: &item, context: context, outcome: OutcomeAllowed, state: StateBlocked})
	}

	blocked := readyItem(StateBlocked, 4)
	request, context = requestFor(OperationResolveBlock, RoleMaterializer, &blocked)
	request.Payload.ResolutionKind = "add-requirement"
	request.Payload.ResolutionSubmissionID = "submission-1"
	request.Payload.HumanApprovalID = "approval-1"
	request.Payload.ApprovalDigest = "digest-1"
	version := blocked.ContractVersion
	context.Approvals = map[string]ApprovalRecord{
		"approval-1": {
			ApprovedSubject:         "human-1",
			DelegatedPrincipal:      "subject-1",
			ProjectID:               blocked.ProjectID,
			WorkItemID:              blocked.ID,
			Digest:                  "digest-1",
			Action:                  OperationResolveBlock,
			ResolutionKind:          "add-requirement",
			ExpectedState:           StateBlocked,
			ExpectedContractVersion: &version,
			PolicyVersion:           "policy/v1",
			ExpiresAt:               evaluationTime.Add(time.Hour),
			Status:                  ApprovalStatusUnused,
		},
	}
	context.ResolutionSubmissions = map[string]ResolutionSubmission{
		"submission-1": {
			ID:                        "submission-1",
			WorkItemID:                blocked.ID,
			Kind:                      "add-requirement",
			ApprovalID:                "approval-1",
			ApprovalDigest:            "digest-1",
			ApprovedHumanSubject:      "human-1",
			Status:                    ResolutionSubmissionStatusPending,
			PlannedDependencyComplete: true,
		},
	}
	blocked.ActiveResolutionSubmissionID = "submission-1"
	add(matrixCase{name: "resolve-block", request: request, current: &blocked, context: context, outcome: OutcomeAllowed, state: StateReadyForBuilding})

	ready := readyItem(StateReadyForBuilding, 4)
	request, context = requestFor(OperationClaim, RoleJobSite, &ready)
	context.NextFencingToken = "fence-new"
	add(matrixCase{name: "claim", request: request, current: &ready, context: context, outcome: OutcomeAllowed, state: StateBuilding, tokenIssued: true})

	for _, state := range []State{StateBuilding, StateInspecting} {
		item := readyItem(state, 4)
		setLiveLease(&item, "subject-1", "fence-current")
		request, context = requestFor(OperationRenewLease, RoleJobSite, &item)
		request.FencingToken = "fence-current"
		add(matrixCase{name: "renew-lease-" + string(state), request: request, current: &item, context: context, outcome: OutcomeAllowed, state: state})
	}

	building := readyItem(StateBuilding, 4)
	setLiveLease(&building, "subject-1", "fence-current")
	request, context = requestFor(OperationTestsPass, RoleJobSite, &building)
	request.FencingToken = "fence-current"
	request.Payload.BuildTestsPassed = true
	add(matrixCase{name: "tests-pass", request: request, current: &building, context: context, outcome: OutcomeAllowed, state: StateInspecting})

	for _, state := range []State{StateBuilding, StateInspecting} {
		item := readyItem(state, 4)
		setLiveLease(&item, "subject-1", "fence-current")
		request, context = requestFor(OperationRaiseSpecQuestion, RoleJobSite, &item)
		request.FencingToken = "fence-current"
		request.Payload.Question = "Clarify the expected behavior."
		add(matrixCase{name: "raise-spec-question-" + string(state), request: request, current: &item, context: context, outcome: OutcomeAllowed, state: StateBlocked})
	}

	building = readyItem(StateBuilding, 4)
	setLiveLease(&building, "subject-1", "fence-current")
	request, context = requestFor(OperationRefreshActive, RoleJobSite, &building)
	request.FencingToken = "fence-current"
	context.NextFencingToken = "fence-refreshed"
	add(matrixCase{name: "refresh-active-building", request: request, current: &building, context: context, outcome: OutcomeAllowed, state: StateBuilding, tokenIssued: true})

	inspecting := readyItem(StateInspecting, 4)
	setLiveLease(&inspecting, "subject-1", "fence-current")
	request, context = requestFor(OperationRefreshActive, RoleJobSite, &inspecting)
	request.FencingToken = "fence-current"
	context.NextFencingToken = "fence-inspection-refresh"
	add(matrixCase{name: "refresh-active-inspecting", request: request, current: &inspecting, context: context, outcome: OutcomeAllowed, state: StateBuilding, tokenIssued: true})

	inspecting = readyItem(StateInspecting, 4)
	setLiveLease(&inspecting, "subject-1", "fence-current")
	request, context = requestFor(OperationRefreshActive, RoleJobSite, &inspecting)
	request.FencingToken = "fence-current"
	context.RefreshReadiness = &Readiness{ContractComplete: true, SourceImmutable: true, SpecificationValidated: true, RequirementReferencesValid: true, PipelineEntrySatisfied: true, ImpactDispositioned: true, PolicyCompatible: false}
	add(matrixCase{name: "refresh-active-blocked", request: request, current: &inspecting, context: context, outcome: OutcomeAllowed, state: StateBlocked})

	inspecting = readyItem(StateInspecting, 4)
	setLiveLease(&inspecting, "subject-1", "fence-current")
	request, context = requestFor(OperationReturnToBuilding, RoleJobSite, &inspecting)
	request.FencingToken = "fence-current"
	request.Payload.InContractDefect = true
	context.NextFencingToken = "fence-rework"
	add(matrixCase{name: "return-to-building", request: request, current: &inspecting, context: context, outcome: OutcomeAllowed, state: StateBuilding, tokenIssued: true})

	inspecting = readyItem(StateInspecting, 4)
	inspecting.InspectionRunSealed = true
	inspecting.FindingsTerminal = true
	inspecting.FinalTestsPassed = true
	setLiveLease(&inspecting, "subject-1", "fence-current")
	request, context = requestFor(OperationBeginMerge, RoleJobSite, &inspecting)
	request.FencingToken = "fence-current"
	add(matrixCase{name: "begin-merge", request: request, current: &inspecting, context: context, outcome: OutcomeAllowed, state: StateMerging})

	merging := readyItem(StateMerging, 9)
	merging.Reconciliation = ReconciliationEvidence{Status: "conflict", GitMutation: "conflict"}
	setLiveLease(&merging, "subject-1", "fence-current")
	request, context = requestFor(OperationMergeConflict, RoleJobSite, &merging)
	request.FencingToken = "fence-current"
	context.NextFencingToken = "fence-after-conflict"
	add(matrixCase{name: "merge-conflict-job-site", request: request, current: &merging, context: context, outcome: OutcomeAllowed, state: StateBuilding, tokenIssued: true})

	merging = readyItem(StateMerging, 9)
	merging.Reconciliation = ReconciliationEvidence{Status: "conflict", GitMutation: "conflict"}
	request, context = requestFor(OperationMergeConflict, RoleReconciler, &merging)
	add(matrixCase{name: "merge-conflict-reconciler", request: request, current: &merging, context: context, outcome: OutcomeAllowed, state: StateReadyForBuilding})

	merging = readyItem(StateMerging, 9)
	merging.Reconciliation = ReconciliationEvidence{Status: "not-applied", GitMutation: "none"}
	request, context = requestFor(OperationMergeNotApplied, RoleReconciler, &merging)
	add(matrixCase{name: "merge-not-applied", request: request, current: &merging, context: context, outcome: OutcomeAllowed, state: StateReadyForBuilding})

	merge := &MergeEnvelope{
		ProductTreeDigest: "tree-digest",
		InspectionRunID:   "inspection-1",
		IntegrationHead:   "integration-head",
		Target:            "main",
		ContractVersion:   9,
	}
	actualMerge := *merge
	actualMerge.MergeCommit = "merge-commit"
	merging = readyItem(StateMerging, 9)
	merging.ExpectedMerge = merge
	setLiveLease(&merging, "subject-1", "fence-current")
	request, context = requestFor(OperationRecordMerge, RoleJobSite, &merging)
	request.FencingToken = "fence-current"
	request.Payload.MergeEnvelope = &actualMerge
	add(matrixCase{name: "record-merge-job-site", request: request, current: &merging, context: context, outcome: OutcomeAllowed, state: StateCompleted})

	merging = readyItem(StateMerging, 9)
	merging.ExpectedMerge = merge
	merging.Reconciliation = ReconciliationEvidence{Status: "merge-recorded", GitMutation: "merged", MergeEnvelope: &actualMerge}
	request, context = requestFor(OperationRecordMerge, RoleReconciler, &merging)
	add(matrixCase{name: "record-merge-reconciler", request: request, current: &merging, context: context, outcome: OutcomeAllowed, state: StateCompleted})

	for _, state := range []State{StateBuilding, StateInspecting} {
		item := readyItem(state, 9)
		item.Lease = &Lease{Owner: "job-site-1", FencingToken: "fence-expired", ExpiresAt: evaluationTime.Add(-time.Second)}
		item.Reconciliation = ReconciliationEvidence{Status: "lease-recovered", GitMutation: "none"}
		request, context = requestFor(OperationRecoverLease, RoleReconciler, &item)
		add(matrixCase{name: "recover-lease-" + string(state), request: request, current: &item, context: context, outcome: OutcomeAllowed, state: StateReadyForBuilding})
	}

	for _, state := range []State{StateWaiting, StateReadyForBuilding, StateBuilding, StateInspecting, StateBlocked} {
		item := readyItem(state, 9)
		item.Reconciliation = ReconciliationEvidence{Status: "not-integrated", GitMutation: "none"}
		request, context = requestFor(OperationAbandon, RoleHumanMaintainer, &item)
		request.Payload.CancellationConfirmed = true
		add(matrixCase{name: "abandon-" + string(state), request: request, current: &item, context: context, outcome: OutcomeAllowed, state: StateAbandoned})
	}
	return tests
}

func invalidTransitionCases() []matrixCase {
	var tests []matrixCase
	add := func(test matrixCase) {
		if test.current != nil {
			item := cloneForTest(*test.current)
			test.current = &item
		}
		tests = append(tests, test)
	}

	waiting := readyItem(StateWaiting, 4)
	request, context := requestFor(OperationClaim, RoleJobSite, &waiting)
	add(matrixCase{name: "claim-waiting-current-state", request: request, current: &waiting, context: context, code: CodeInvalidTransition})

	blocked := readyItem(StateBlocked, 4)
	request, context = requestFor(OperationClaim, RoleJobSite, &blocked)
	add(matrixCase{name: "claim-blocked-current-state", request: request, current: &blocked, context: context, code: CodeInvalidTransition})

	merging := readyItem(StateMerging, 4)
	merging.Reconciliation = ReconciliationEvidence{Status: "not-integrated", GitMutation: "none"}
	request, context = requestFor(OperationAbandon, RoleHumanMaintainer, &merging)
	request.Payload.CancellationConfirmed = true
	add(matrixCase{name: "abandon-merging", request: request, current: &merging, context: context, code: CodeInvalidTransition})

	ready := readyItem(StateReadyForBuilding, 4)
	request, context = requestFor(OperationClaim, RoleJobSite, &ready)
	request.ExpectedState = StateBlocked
	add(matrixCase{name: "stale-state", request: request, current: &ready, context: context, code: CodeStaleState})

	request, context = requestFor(OperationClaim, RoleJobSite, &ready)
	wrongVersion := ready.ContractVersion + 1
	request.ExpectedContractVersion = &wrongVersion
	add(matrixCase{name: "stale-contract-version", request: request, current: &ready, context: context, code: CodeStaleContractVersion})

	building := readyItem(StateBuilding, 4)
	setLiveLease(&building, "subject-1", "fence-current")
	request, context = requestFor(OperationClaim, RoleJobSite, &building)
	add(matrixCase{name: "duplicate-claim", request: request, current: &building, context: context, code: CodeDuplicateClaim})

	request, context = requestFor(OperationRenewLease, RoleJobSite, &building)
	request.FencingToken = "fence-old"
	add(matrixCase{name: "stale-fencing-token", request: request, current: &building, context: context, code: CodeStaleFencingToken})

	request, context = requestFor(OperationClaim, RoleDraftingTable, &ready)
	add(matrixCase{name: "unauthorized-claim", request: request, current: &ready, context: context, code: CodeUnauthorizedAction})

	completed := readyItem(StateCompleted, 9)
	request, context = requestFor(OperationAbandon, RoleHumanMaintainer, &completed)
	add(matrixCase{name: "terminal-item", request: request, current: &completed, context: context, code: CodeAlreadyTerminal})

	candidate := readyItem(StateInitial, 0)
	candidate.ID = "wi-invalid-change"
	candidate.ProjectID = "fixture-project"
	candidate.ChangeType = "future-change"
	request, context = materializeRequest(candidate)
	add(matrixCase{name: "invalid-change-type", request: request, context: context, code: CodeInvalidChangeType})

	candidate = readyItem(StateInitial, 0)
	candidate.ID = "wi-incomplete"
	candidate.ProjectID = "fixture-project"
	candidate.ChangeType = "changes"
	candidate.Readiness.ContractComplete = false
	request, context = materializeRequest(candidate)
	add(matrixCase{name: "incomplete-materialization", request: request, context: context, code: CodePreconditionFailed})

	item := readyItem(StateReadyForBuilding, 4)
	item.Readiness.PolicyCompatible = false
	request, context = requestFor(OperationClaim, RoleJobSite, &item)
	add(matrixCase{name: "claim-not-ready", request: request, current: &item, context: context, code: CodePreconditionFailed})

	item = readyItem(StateReadyForBuilding, 4)
	request, context = requestFor(OperationRevalidate, RoleMaterializer, &item)
	add(matrixCase{name: "revalidate-without-blocker", request: request, current: &item, context: context, code: CodePreconditionFailed})
	return tests
}

func authorization(role Role, operations ...Operation) AuthorizationContext {
	return AuthorizationContext{
		Subject:        "subject-1",
		Role:           role,
		ProjectID:      "fixture-project",
		AllowedActions: append([]Operation(nil), operations...),
		AllowedRefs:    []string{"project:fixture-project"},
		ExpiresAt:      evaluationTime.Add(time.Hour),
		PolicyVersion:  "policy/v1",
	}
}

func readyItem(state State, version uint64) WorkItem {
	return WorkItem{
		ID:                     "wi-1",
		ProjectID:              "fixture-project",
		State:                  state,
		ContractVersion:        version,
		ImplementationRequired: true,
		Readiness: Readiness{
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

func requestFor(operation Operation, role Role, item *WorkItem) (Request, EvaluationContext) {
	request := Request{
		Operation:     operation,
		ProjectID:     "fixture-project",
		WorkItemID:    item.ID,
		Authorization: authorization(role, operation),
	}
	request.ExpectedState = item.State
	request.ExpectedContractVersion = versionPointer(item.ContractVersion)
	if role == RoleJobSite && item.Lease != nil {
		request.FencingToken = item.Lease.FencingToken
	}
	return request, baseEvaluation(AuthorityAuthoritative)
}

func materializeRequest(item WorkItem) (Request, EvaluationContext) {
	itemCopy := item
	request := Request{
		Operation:               OperationMaterialize,
		ProjectID:               "fixture-project",
		WorkItemID:              item.ID,
		MaterializationKey:      "materialization-1",
		ExpectedState:           StateInitial,
		ExpectedContractVersion: versionPointer(0),
		Payload:                 Payload{WorkItem: &itemCopy},
		Authorization:           authorization(RoleMaterializer, OperationMaterialize),
	}
	return request, baseEvaluation(AuthorityAuthoritative)
}

func baseEvaluation(authority Authority) EvaluationContext {
	return EvaluationContext{
		Authority:        authority,
		EvaluationTime:   evaluationTime,
		LeaseDuration:    15 * time.Minute,
		NextFencingToken: "fence-issued",
	}
}

func setLiveLease(item *WorkItem, owner, token string) {
	item.Owner = owner
	item.Lease = &Lease{Owner: owner, FencingToken: token, ExpiresAt: evaluationTime.Add(time.Hour)}
}

func versionPointer(version uint64) *uint64 {
	return &version
}

func cloneForTest(item WorkItem) WorkItem {
	copy := item
	copy.Dependencies = append([]Dependency(nil), item.Dependencies...)
	copy.Readiness.UnresolvedReasons = append([]string(nil), item.Readiness.UnresolvedReasons...)
	return copy
}
