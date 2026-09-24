package validation

import (
	"sort"
	"strings"
	"time"
)

type transitionRule struct {
	operation Operation
	from      State
	to        State
}

type transitionIndex struct {
	byState map[transitionKey]struct{}
	known   map[Operation]struct{}
}

type transitionKey struct {
	operation Operation
	state     State
}

// v1Transitions is the declarative state/command portion of validation-rules/v1.
// Dynamic destinations are selected by evaluateTransition after these pairs
// have established that the command is legal from the current state.
var v1Transitions = []transitionRule{
	{OperationMaterialize, StateInitial, StateReadyForBuilding},
	{OperationRefreshDependencies, StateWaiting, StateReadyForBuilding},
	{OperationRevalidate, StateWaiting, StateBlocked},
	{OperationRevalidate, StateReadyForBuilding, StateBlocked},
	{OperationResolveBlock, StateBlocked, StateReadyForBuilding},
	{OperationClaim, StateReadyForBuilding, StateBuilding},
	{OperationRenewLease, StateBuilding, StateBuilding},
	{OperationRenewLease, StateInspecting, StateInspecting},
	{OperationTestsPass, StateBuilding, StateInspecting},
	{OperationRaiseSpecQuestion, StateBuilding, StateBlocked},
	{OperationRaiseSpecQuestion, StateInspecting, StateBlocked},
	{OperationRefreshActive, StateBuilding, StateBuilding},
	{OperationRefreshActive, StateInspecting, StateBuilding},
	{OperationReturnToBuilding, StateInspecting, StateBuilding},
	{OperationBeginMerge, StateInspecting, StateMerging},
	{OperationMergeConflict, StateMerging, StateBuilding},
	{OperationMergeNotApplied, StateMerging, StateReadyForBuilding},
	{OperationRecordMerge, StateMerging, StateCompleted},
	{OperationRecoverLease, StateBuilding, StateReadyForBuilding},
	{OperationRecoverLease, StateInspecting, StateReadyForBuilding},
	{OperationAbandon, StateWaiting, StateAbandoned},
	{OperationAbandon, StateReadyForBuilding, StateAbandoned},
	{OperationAbandon, StateBuilding, StateAbandoned},
	{OperationAbandon, StateInspecting, StateAbandoned},
	{OperationAbandon, StateBlocked, StateAbandoned},
}

var v1TransitionIndex = indexTransitionRules(v1Transitions)

// Evaluate applies validation-rules/v1 to a supplied record and trusted
// evaluation context. It never mutates the record or queries a store.
func Evaluate(request Request, current *WorkItem, evaluation EvaluationContext) Decision {
	authority := evaluation.Authority
	if authority == "" {
		authority = AuthorityPreflight
	}
	evaluation.Authority = authority
	decision := Decision{
		Outcome:       OutcomeRejected,
		Authority:     authority,
		RuleVersion:   RuleVersion,
		PolicyVersion: request.Authorization.PolicyVersion,
		Operation:     request.Operation,
	}
	if authority != AuthorityPreflight && authority != AuthorityAuthoritative {
		return rejectDecision(decision, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID))
	}
	if !knownLifecycleOperation(request.Operation) {
		return rejectDecision(decision, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID))
	}
	authorizationAction := request.Operation
	if authority == AuthorityPreflight {
		authorizationAction = OperationLifecyclePreflight
	}
	if rejection := ValidateAuthorizationContext(
		request.Authorization,
		authorizationAction,
		request.ProjectID,
		request.WorkItemID,
		request.Payload.ChangeSetID,
		evaluation.EvaluationTime,
	); rejection != nil {
		return rejectDecision(decision, rejection)
	}
	if rejection := observeTarget(request, current, &decision); rejection != nil {
		return rejectDecision(decision, rejection)
	}
	if rejection := authorizeRequest(request, evaluation); rejection != nil {
		return rejectDecision(decision, rejection)
	}
	if rejection := validateMutationPreconditions(request, current, decision.Before, evaluation); rejection != nil {
		return rejectDecision(decision, rejection)
	}
	return decideTransition(request, current, evaluation, decision)
}

// RejectionDecision wraps an authoritative rejection produced by the WMS
// transaction (for example, an idempotency-key conflict) in the shared
// Validation Rules decision envelope.
func RejectionDecision(request Request, authority Authority, rejection *Rejection) Decision {
	if authority == "" {
		authority = AuthorityAuthoritative
	}
	return Decision{
		Outcome:       OutcomeRejected,
		Authority:     authority,
		RuleVersion:   RuleVersion,
		PolicyVersion: request.Authorization.PolicyVersion,
		Operation:     request.Operation,
		Rejection:     rejection,
	}
}

func authorizeRequest(request Request, evaluation EvaluationContext) *Rejection {
	if evaluation.Authority == AuthorityPreflight {
		return AuthorizePreflight(
			request.Authorization,
			request.ProjectID,
			request.WorkItemID,
			request.Payload.ChangeSetID,
			request.References,
			evaluation.EvaluationTime,
		)
	}
	return AuthorizeLifecycle(
		request.Authorization,
		request.Operation,
		request.ProjectID,
		request.WorkItemID,
		request.Payload.ChangeSetID,
		request.References,
		evaluation.EvaluationTime,
	)
}

func observeTarget(request Request, current *WorkItem, decision *Decision) *Rejection {
	if request.Operation == OperationMaterialize {
		decision.Before = &StateVersion{State: StateInitial, ContractVersion: 0}
		if current != nil {
			return idempotencyConflict("materialization-source", request.MaterializationKey, "")
		}
		return nil
	}
	if current == nil || current.ProjectID != request.ProjectID || current.ID != request.WorkItemID {
		return notFound("work-item")
	}
	decision.Before = stateVersion(current)
	return nil
}

func validateMutationPreconditions(request Request, current *WorkItem, before *StateVersion, evaluation EvaluationContext) *Rejection {
	if request.Operation == OperationResolveBlock && evaluation.Authority == AuthorityAuthoritative {
		if rejection := validateResolutionBinding(request, current, evaluation); rejection != nil {
			return rejection
		}
	}
	if current == nil {
		if rejection := validateExpectedState(request, before); rejection != nil {
			return rejection
		}
		return validateExpectedVersion(request, before)
	}
	if isTerminal(current.State) {
		return terminalRejection(current)
	}
	if request.Operation == OperationClaim && hasActiveLease(current, evaluation.EvaluationTime) {
		return duplicateClaimRejection(current)
	}
	if rejection := validateExpectedState(request, before); rejection != nil {
		return rejection
	}
	if rejection := validateExpectedVersion(request, before); rejection != nil {
		return rejection
	}
	if requiresLiveFence(request.Operation, request.Authorization.Role) {
		return validateLiveFence(request, current, evaluation.EvaluationTime)
	}
	return nil
}

func terminalRejection(current *WorkItem) *Rejection {
	return &Rejection{
		Code:    CodeAlreadyTerminal,
		Message: "The work item is terminal and cannot be changed.",
		Details: map[string]any{
			"terminal_state":           current.State,
			"current_contract_version": current.ContractVersion,
		},
		Retry: RetryNever,
	}
}

func duplicateClaimRejection(current *WorkItem) *Rejection {
	return &Rejection{
		Code:    CodeDuplicateClaim,
		Message: "The work item already has an active execution lease.",
		Details: map[string]any{
			"current_state":            current.State,
			"current_contract_version": current.ContractVersion,
			"claim_status":             "active",
		},
		Retry: RetryQuery,
	}
}

func decideTransition(request Request, current *WorkItem, evaluation EvaluationContext, decision Decision) Decision {
	if current != nil && !transitionAllowed(request.Operation, current.State) {
		return rejectDecision(decision, invalidTransition(request.Operation, current.State))
	}
	to, outcome, reservation, rejection := evaluateTransition(request, current, evaluation)
	if rejection != nil {
		return rejectDecision(decision, rejection)
	}
	decision.MaterializationReservation = reservation
	if outcome == OutcomeOmitted {
		decision.Outcome = OutcomeOmitted
		return decision
	}
	if evaluation.Authority == AuthorityAuthoritative && issuesFencingToken(request.Operation, request.Authorization.Role, to) && evaluation.NextFencingToken == "" {
		return rejectDecision(decision, preconditionFailed("the WMS did not provide a new fencing token", "fencing_token"))
	}
	decision.Outcome = OutcomeAllowed
	decision.After = resultingVersion(current, to)
	if evaluation.Authority == AuthorityAuthoritative && issuesFencingToken(request.Operation, request.Authorization.Role, to) {
		decision.FencingTokenIssued = evaluation.NextFencingToken
	}
	return decision
}

func resultingVersion(current *WorkItem, state State) *StateVersion {
	version := uint64(1)
	if current != nil {
		version = current.ContractVersion + 1
	}
	return &StateVersion{State: state, ContractVersion: version}
}

func evaluateTransition(request Request, current *WorkItem, evaluation EvaluationContext) (State, Outcome, *MaterializationReservation, *Rejection) {
	switch request.Operation {
	case OperationMaterialize:
		return evaluateMaterialization(request)
	case OperationRefreshDependencies, OperationRevalidate, OperationResolveBlock:
		return evaluatePlanningTransition(request, current, evaluation)
	case OperationClaim, OperationRenewLease, OperationRefreshActive, OperationReturnToBuilding:
		return evaluateLeaseTransition(request, current, evaluation)
	case OperationTestsPass, OperationRaiseSpecQuestion, OperationBeginMerge:
		return evaluateExecutionTransition(request, current)
	case OperationMergeConflict, OperationMergeNotApplied, OperationRecordMerge:
		return evaluateMergeTransition(request, current)
	case OperationRecoverLease, OperationAbandon:
		return evaluateRecoveryTransition(request, current, evaluation)
	default:
		return "", OutcomeRejected, nil, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID)
	}
}

func evaluatePlanningTransition(request Request, current *WorkItem, evaluation EvaluationContext) (State, Outcome, *MaterializationReservation, *Rejection) {
	switch request.Operation {
	case OperationRefreshDependencies:
		readiness := effectiveReadiness(current, evaluation)
		if reason := readinessContractFailure(readiness); reason != "" {
			return "", OutcomeRejected, nil, preconditionFailed(reason, "complete-work-item-contract")
		}
		if reason := readinessBlocker(readiness); reason != "" {
			return "", OutcomeRejected, nil, preconditionFailed(reason, "readiness-evidence")
		}
		if dependency, incomplete := firstIncompleteDependency(effectiveDependencies(current, evaluation)); incomplete {
			return "", OutcomeRejected, nil, dependencyFailed(dependency)
		}
		return StateReadyForBuilding, OutcomeAllowed, nil, nil
	case OperationRevalidate:
		if !readinessBlocks(effectiveReadiness(current, evaluation)) {
			return "", OutcomeRejected, nil, preconditionFailed("revalidation found no unresolved blocker", "blocking-condition")
		}
		return StateBlocked, OutcomeAllowed, nil, nil
	case OperationResolveBlock:
		if rejection := validateResolutionPreconditions(request, current, evaluation); rejection != nil {
			return "", OutcomeRejected, nil, rejection
		}
		if reason := readinessContractFailure(effectiveReadiness(current, evaluation)); reason != "" {
			return "", OutcomeRejected, nil, preconditionFailed(reason, "complete-work-item-contract")
		}
		if reason := readinessBlocker(effectiveReadiness(current, evaluation)); reason != "" {
			return "", OutcomeRejected, nil, preconditionFailed(reason, "readiness-evidence")
		}
		if dependency, incomplete := firstIncompleteDependency(effectiveDependencies(current, evaluation)); incomplete {
			return "", OutcomeRejected, nil, dependencyFailed(dependency)
		}
		return StateReadyForBuilding, OutcomeAllowed, nil, nil
	default:
		return "", OutcomeRejected, nil, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID)
	}
}

func evaluateLeaseTransition(request Request, current *WorkItem, evaluation EvaluationContext) (State, Outcome, *MaterializationReservation, *Rejection) {
	switch request.Operation {
	case OperationClaim:
		if reason := readinessContractFailure(current.Readiness); reason != "" {
			return "", OutcomeRejected, nil, preconditionFailed(reason, "complete-work-item-contract")
		}
		if reason := readinessBlocker(current.Readiness); reason != "" {
			return "", OutcomeRejected, nil, preconditionFailed(reason, "readiness-evidence")
		}
		if dependency, incomplete := firstIncompleteDependency(current.Dependencies); incomplete {
			return "", OutcomeRejected, nil, dependencyFailed(dependency)
		}
		return StateBuilding, OutcomeAllowed, nil, nil
	case OperationRenewLease:
		return current.State, OutcomeAllowed, nil, nil
	case OperationRefreshActive:
		_, incompleteDependency := firstIncompleteDependency(effectiveDependencies(current, evaluation))
		if readinessContractFailure(effectiveReadiness(current, evaluation)) != "" ||
			readinessBlocker(effectiveReadiness(current, evaluation)) != "" ||
			incompleteDependency {
			return StateBlocked, OutcomeAllowed, nil, nil
		}
		return StateBuilding, OutcomeAllowed, nil, nil
	case OperationReturnToBuilding:
		if !request.Payload.InContractDefect && !request.Payload.FinalTestsFailed {
			return "", OutcomeRejected, nil, preconditionFailed("no in-contract defect or failed final test was recorded", "rework-reason")
		}
		return StateBuilding, OutcomeAllowed, nil, nil
	default:
		return "", OutcomeRejected, nil, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID)
	}
}

func evaluateExecutionTransition(request Request, current *WorkItem) (State, Outcome, *MaterializationReservation, *Rejection) {
	switch request.Operation {
	case OperationTestsPass:
		if !request.Payload.BuildTestsPassed {
			return "", OutcomeRejected, nil, preconditionFailed("the Building test gate has not passed", "building-test-gate")
		}
		return StateInspecting, OutcomeAllowed, nil, nil
	case OperationRaiseSpecQuestion:
		if strings.TrimSpace(request.Payload.Question) == "" {
			return "", OutcomeRejected, nil, preconditionFailed("the specification question is missing", "specification-question")
		}
		return StateBlocked, OutcomeAllowed, nil, nil
	case OperationBeginMerge:
		if !current.InspectionRunSealed || !current.FindingsTerminal || !current.FinalTestsPassed {
			return "", OutcomeRejected, nil, preconditionFailed("inspection and final test gates are incomplete", "sealed-inspection-and-final-tests")
		}
		return StateMerging, OutcomeAllowed, nil, nil
	default:
		return "", OutcomeRejected, nil, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID)
	}
}

func evaluateMergeTransition(request Request, current *WorkItem) (State, Outcome, *MaterializationReservation, *Rejection) {
	switch request.Operation {
	case OperationMergeConflict:
		if !reconciliationMatches(current.Reconciliation, "conflict", "conflict") {
			return "", OutcomeRejected, nil, preconditionFailed("the WMS has no matching Git conflict observation", "reconciliation-conflict-evidence")
		}
		if request.Authorization.Role == RoleReconciler {
			return StateReadyForBuilding, OutcomeAllowed, nil, nil
		}
		return StateBuilding, OutcomeAllowed, nil, nil
	case OperationMergeNotApplied:
		if !reconciliationMatches(current.Reconciliation, "not-applied", "none") {
			return "", OutcomeRejected, nil, preconditionFailed("the WMS has no matching not-applied observation", "reconciliation-not-applied-evidence")
		}
		return StateReadyForBuilding, OutcomeAllowed, nil, nil
	case OperationRecordMerge:
		if request.Authorization.Role == RoleReconciler {
			if !reconciliationMatches(current.Reconciliation, "merge-recorded", "merged") ||
				!mergeEnvelopeMatches(current.ExpectedMerge, current.Reconciliation.MergeEnvelope, current.ContractVersion, false) {
				return "", OutcomeRejected, nil, preconditionFailed("the WMS merge observation does not match the work-item contract", "reconciliation-merge-envelope")
			}
		} else if !mergeEnvelopeMatches(current.ExpectedMerge, request.Payload.MergeEnvelope, current.ContractVersion, false) {
			return "", OutcomeRejected, nil, preconditionFailed("the tested candidate and merge result do not match", "tested-merge-envelope")
		}
		return StateCompleted, OutcomeAllowed, nil, nil
	default:
		return "", OutcomeRejected, nil, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID)
	}
}

func evaluateRecoveryTransition(request Request, current *WorkItem, evaluation EvaluationContext) (State, Outcome, *MaterializationReservation, *Rejection) {
	switch request.Operation {
	case OperationRecoverLease:
		if current.Lease != nil && current.Lease.ExpiresAt.After(evaluation.EvaluationTime) {
			return "", OutcomeRejected, nil, preconditionFailed("the execution lease has not expired", "expired-lease")
		}
		if !reconciliationMatches(current.Reconciliation, "lease-recovered", "none") {
			return "", OutcomeRejected, nil, preconditionFailed("lease recovery has no matching WMS reconciliation evidence", "lease-recovery-evidence")
		}
		return StateReadyForBuilding, OutcomeAllowed, nil, nil
	case OperationAbandon:
		if !request.Payload.CancellationConfirmed || !reconciliationMatches(current.Reconciliation, "not-integrated", "none") {
			return "", OutcomeRejected, nil, preconditionFailed("cancellation is not confirmed or Git integration is not ruled out", "not-integrated-reconciliation")
		}
		return StateAbandoned, OutcomeAllowed, nil, nil
	default:
		return "", OutcomeRejected, nil, unauthorizedFor(request.Authorization, request.Operation, request.WorkItemID)
	}
}

func evaluateMaterialization(request Request) (State, Outcome, *MaterializationReservation, *Rejection) {
	if request.MaterializationKey == "" {
		return "", OutcomeRejected, nil, preconditionFailed("materialization_key is required", "materialization_key")
	}
	item := request.Payload.WorkItem
	if item == nil || item.ID == "" || item.ID != request.WorkItemID || item.ProjectID != request.ProjectID {
		return "", OutcomeRejected, nil, preconditionFailed("a complete work-item contract for the trusted project is required", "complete-work-item-contract")
	}
	candidate := CanonicalMaterializationSource(*item, request.Payload.ChangeType)
	changeType := candidate.ChangeType
	if !validChangeType(changeType) {
		return "", OutcomeRejected, nil, &Rejection{
			Code:    CodeInvalidChangeType,
			Message: "The materialization change type is not part of the MVP vocabulary.",
			Details: map[string]any{
				"provided_type": changeType,
				"allowed_types": []string{"undefined", "changes", "contradicts"},
			},
			Retry: RetryNewKey,
		}
	}
	if reason := readinessContractFailure(candidate.Readiness); reason != "" {
		return "", OutcomeRejected, nil, preconditionFailed(reason, "complete-work-item-contract")
	}

	fingerprint := SourceFingerprint(candidate)
	if !candidate.ImplementationRequired {
		return "", OutcomeOmitted, &MaterializationReservation{
			Key:               request.MaterializationKey,
			SourceFingerprint: fingerprint,
			Outcome:           MaterializationOutcomeOmitted,
		}, nil
	}
	if reason := readinessBlocker(candidate.Readiness); reason != "" {
		return StateBlocked, OutcomeAllowed, &MaterializationReservation{
			Key:               request.MaterializationKey,
			SourceFingerprint: fingerprint,
			Outcome:           MaterializationOutcomeBlocked,
		}, nil
	}
	if _, incomplete := firstIncompleteDependency(candidate.Dependencies); incomplete {
		return StateWaiting, OutcomeAllowed, &MaterializationReservation{
			Key:               request.MaterializationKey,
			SourceFingerprint: fingerprint,
			Outcome:           MaterializationOutcomeWaiting,
		}, nil
	}
	return StateReadyForBuilding, OutcomeAllowed, &MaterializationReservation{
		Key:               request.MaterializationKey,
		SourceFingerprint: fingerprint,
		Outcome:           MaterializationOutcomeReadyForBuilding,
	}, nil
}

func validateResolutionBinding(request Request, current *WorkItem, evaluation EvaluationContext) *Rejection {
	submissionID := request.Payload.ResolutionSubmissionID
	if submissionID == "" || request.Payload.HumanApprovalID == "" || request.Payload.ApprovalDigest == "" ||
		current.ActiveResolutionSubmissionID != submissionID {
		return unauthorizedFor(request.Authorization, OperationResolveBlock, request.WorkItemID)
	}
	submission, ok := evaluation.ResolutionSubmissions[submissionID]
	if !ok || submission.Status != ResolutionSubmissionStatusPending || submission.ID != current.ActiveResolutionSubmissionID ||
		submission.WorkItemID != request.WorkItemID || submission.Kind != request.Payload.ResolutionKind ||
		submission.ApprovalID != request.Payload.HumanApprovalID || submission.ApprovalDigest != request.Payload.ApprovalDigest ||
		submission.ApprovedHumanSubject == "" {
		return unauthorizedFor(request.Authorization, OperationResolveBlock, request.WorkItemID)
	}
	approval, ok := evaluation.Approvals[request.Payload.HumanApprovalID]
	if !ok {
		return unauthorizedFor(request.Authorization, OperationResolveBlock, request.WorkItemID)
	}
	if approval.ApprovedSubject != submission.ApprovedHumanSubject {
		return unauthorizedFor(request.Authorization, OperationResolveBlock, request.WorkItemID)
	}
	requirement := ApprovalRequirement{
		Action:                  OperationResolveBlock,
		DelegatedPrincipal:      request.Authorization.Subject,
		ProjectID:               request.ProjectID,
		WorkItemID:              request.WorkItemID,
		ResolutionKind:          request.Payload.ResolutionKind,
		Digest:                  request.Payload.ApprovalDigest,
		ExpectedState:           request.ExpectedState,
		ExpectedContractVersion: request.ExpectedContractVersion,
		PolicyVersion:           request.Authorization.PolicyVersion,
	}
	return ValidateApproval(approval, requirement, evaluation.EvaluationTime)
}

func validateResolutionPreconditions(request Request, current *WorkItem, evaluation EvaluationContext) *Rejection {
	if evaluation.Authority == AuthorityPreflight {
		preview := evaluation.PreviewResolution
		if preview == nil {
			if request.Payload.ResolutionKind == "" {
				return preconditionFailed("the blocked-work resolution kind is missing", "resolution-kind")
			}
			preview = &ResolutionPreview{
				Kind:        request.Payload.ResolutionKind,
				ChangeSetID: request.Payload.ChangeSetID,
			}
		}
		return validateResolutionRefresh(preview.Kind, preview.ChangeSetID, preview.PlannedDependencyComplete, preview.InspectorConfirmed)
	}
	submission := evaluation.ResolutionSubmissions[request.Payload.ResolutionSubmissionID]
	return validateResolutionRefresh(
		submission.Kind,
		submission.ChangeSetID,
		submission.PlannedDependencyComplete,
		submission.IndependentInspectorConfirmed,
	)
}

func validateResolutionRefresh(kind, changeSetID string, plannedDependencyComplete, inspectorConfirmed bool) *Rejection {
	switch kind {
	case "add-requirement":
		if !plannedDependencyComplete {
			if changeSetID == "" {
				changeSetID = "planned change-set"
			}
			return dependencyFailed(changeSetID)
		}
	case "out-of-scope":
		if !inspectorConfirmed {
			return preconditionFailed("independent Inspector confirmation is missing", "independent-inspector-confirmation")
		}
	case "impact-amendment":
		return nil
	default:
		return preconditionFailed("the resolution kind is not supported for lifecycle unblock", "resolution-kind")
	}
	return nil
}

func validateExpectedState(request Request, before *StateVersion) *Rejection {
	if before == nil || request.ExpectedState != before.State {
		current := State("")
		if before != nil {
			current = before.State
		}
		return &Rejection{
			Code:    CodeStaleState,
			Message: "The work item state changed after this command was prepared.",
			Details: map[string]any{"expected_state": request.ExpectedState, "current_state": current},
			Retry:   RetryRefresh,
		}
	}
	return nil
}

func validateExpectedVersion(request Request, before *StateVersion) *Rejection {
	if before == nil || request.ExpectedContractVersion == nil || *request.ExpectedContractVersion != before.ContractVersion {
		var expected any
		if request.ExpectedContractVersion != nil {
			expected = *request.ExpectedContractVersion
		}
		var current any
		if before != nil {
			current = before.ContractVersion
		}
		return &Rejection{
			Code:    CodeStaleContractVersion,
			Message: "The work item changed after this command was prepared.",
			Details: map[string]any{
				"expected_contract_version": expected,
				"current_contract_version":  current,
			},
			Retry: RetryRefresh,
		}
	}
	return nil
}

func validateLiveFence(request Request, current *WorkItem, now time.Time) *Rejection {
	status := "missing"
	if current.Lease != nil {
		switch {
		case !current.Lease.ExpiresAt.After(now):
			status = "expired"
		case current.Lease.Owner != request.Authorization.Subject:
			status = "owner-mismatch"
		case request.FencingToken == "" || request.FencingToken != current.Lease.FencingToken:
			status = "stale"
		default:
			return nil
		}
	}
	return &Rejection{
		Code:    CodeStaleFencingToken,
		Message: "The caller does not hold the current live execution fence.",
		Details: map[string]any{
			"fence_status":             status,
			"current_contract_version": current.ContractVersion,
		},
		Retry: RetryReconcile,
	}
}

func transitionAllowed(operation Operation, state State) bool {
	_, ok := v1TransitionIndex.byState[transitionKey{operation: operation, state: state}]
	return ok
}

func knownLifecycleOperation(operation Operation) bool {
	_, ok := v1TransitionIndex.known[operation]
	return ok
}

func indexTransitionRules(rules []transitionRule) transitionIndex {
	index := transitionIndex{
		byState: make(map[transitionKey]struct{}, len(rules)),
		known:   make(map[Operation]struct{}),
	}
	for _, rule := range rules {
		index.byState[transitionKey{operation: rule.operation, state: rule.from}] = struct{}{}
		index.known[rule.operation] = struct{}{}
	}
	return index
}

func invalidTransition(operation Operation, state State) *Rejection {
	return &Rejection{
		Code:    CodeInvalidTransition,
		Message: "The lifecycle command is not valid from the current state.",
		Details: map[string]any{
			"operation":          operation,
			"current_state":      state,
			"allowed_operations": allowedOperations(state),
		},
		Retry: RetryRefresh,
	}
}

func allowedOperations(state State) []string {
	seen := make(map[Operation]struct{})
	for _, rule := range v1Transitions {
		if rule.from == state {
			seen[rule.operation] = struct{}{}
		}
	}
	operations := make([]string, 0, len(seen))
	for operation := range seen {
		operations = append(operations, string(operation))
	}
	sort.Strings(operations)
	return operations
}

func stateVersion(item *WorkItem) *StateVersion {
	return &StateVersion{State: item.State, ContractVersion: item.ContractVersion}
}

func isTerminal(state State) bool {
	return state == StateCompleted || state == StateAbandoned
}

func hasActiveLease(item *WorkItem, now time.Time) bool {
	return item != nil && item.Lease != nil && item.Lease.ExpiresAt.After(now)
}

func requiresLiveFence(operation Operation, role Role) bool {
	if role != RoleJobSite {
		return false
	}
	switch operation {
	case OperationRenewLease, OperationTestsPass, OperationRaiseSpecQuestion,
		OperationRefreshActive, OperationReturnToBuilding, OperationBeginMerge,
		OperationMergeConflict, OperationRecordMerge:
		return true
	default:
		return false
	}
}

func issuesFencingToken(operation Operation, role Role, target State) bool {
	if role != RoleJobSite || target != StateBuilding {
		return false
	}
	switch operation {
	case OperationClaim, OperationRefreshActive, OperationReturnToBuilding, OperationMergeConflict:
		return true
	default:
		return false
	}
}

func readinessContractFailure(readiness Readiness) string {
	switch {
	case !readiness.ContractComplete:
		return "the complete work-item contract is missing"
	case !readiness.SourceImmutable:
		return "the source specification commit is not immutable and resolvable"
	case !readiness.SpecificationValidated:
		return "ears-manager has not validated the registered specification artifacts"
	case !readiness.RequirementReferencesValid:
		return "changed or applicable requirement references do not resolve"
	case !readiness.PipelineEntrySatisfied:
		return "the change-type pipeline entry requirements are not satisfied"
	default:
		return ""
	}
}

func readinessBlocker(readiness Readiness) string {
	if !readiness.ImpactDispositioned {
		return "impact candidates remain undispositioned"
	}
	if !readiness.PolicyCompatible {
		return "the work item is not compatible with current project policy"
	}
	if len(readiness.UnresolvedReasons) > 0 {
		reasons := append([]string(nil), readiness.UnresolvedReasons...)
		sort.Strings(reasons)
		return reasons[0]
	}
	return ""
}

func effectiveReadiness(current *WorkItem, evaluation EvaluationContext) Readiness {
	if evaluation.RefreshReadiness != nil {
		return *evaluation.RefreshReadiness
	}
	return current.Readiness
}

func effectiveDependencies(current *WorkItem, evaluation EvaluationContext) []Dependency {
	if evaluation.RefreshDependencies != nil {
		return *evaluation.RefreshDependencies
	}
	return current.Dependencies
}

func firstIncompleteDependency(dependencies []Dependency) (string, bool) {
	blocking := ""
	found := false
	for _, dependency := range dependencies {
		if dependency.State != StateCompleted && (!found || dependency.ID < blocking) {
			blocking = dependency.ID
			found = true
		}
	}
	return blocking, found
}

func dependencyFailed(dependency string) *Rejection {
	return &Rejection{
		Code:    CodePreconditionFailed,
		Message: "A required work-item dependency is not complete.",
		Details: map[string]any{
			"failed_precondition": "all required dependencies must be completed",
			"blocking_dependency": dependency,
		},
		Retry: RetryRefresh,
	}
}

func preconditionFailed(reason, evidence string) *Rejection {
	return &Rejection{
		Code:    CodePreconditionFailed,
		Message: "A required lifecycle precondition is not satisfied.",
		Details: map[string]any{
			"failed_precondition": reason,
			"required_evidence":   evidence,
		},
		Retry: RetryRefresh,
	}
}

func reconciliationMatches(evidence ReconciliationEvidence, status, mutation string) bool {
	return evidence.Status == status && evidence.GitMutation == mutation
}

func mergeEnvelopeMatches(expected, actual *MergeEnvelope, contractVersion uint64, requireCommitMatch bool) bool {
	if expected == nil || actual == nil || actual.ProductTreeDigest == "" ||
		actual.InspectionRunID == "" || actual.IntegrationHead == "" ||
		actual.Target == "" || actual.MergeCommit == "" ||
		actual.ContractVersion != contractVersion {
		return false
	}
	if expected.ProductTreeDigest != actual.ProductTreeDigest ||
		expected.InspectionRunID != actual.InspectionRunID ||
		expected.IntegrationHead != actual.IntegrationHead ||
		expected.Target != actual.Target ||
		expected.ContractVersion != actual.ContractVersion {
		return false
	}
	return !requireCommitMatch || expected.MergeCommit == actual.MergeCommit
}

func validChangeType(changeType string) bool {
	switch changeType {
	case "undefined", "changes", "contradicts":
		return true
	default:
		return false
	}
}

func notFound(targetType string) *Rejection {
	return &Rejection{
		Code:    CodeNotFound,
		Message: "The target is not visible in the trusted project context.",
		Details: map[string]any{"target_type": targetType, "visibility_scope": "project"},
		Retry:   RetryRefresh,
	}
}

func idempotencyConflict(kind, key, recordedDigest string) *Rejection {
	details := map[string]any{"conflict_kind": kind, "key_scope": "project"}
	if kind == "materialization-source" {
		details["materialization_key"] = key
		details["recorded_source_digest"] = recordedDigest
		return &Rejection{
			Code:    CodeIdempotencyConflict,
			Message: "The materialization key is already bound to a different source contract.",
			Details: details,
			Retry:   RetryReconcile,
		}
	}
	return &Rejection{
		Code:    CodeIdempotencyConflict,
		Message: "The idempotency key was reused with a different request.",
		Details: details,
		Retry:   RetryNewKey,
	}
}

func rejectDecision(decision Decision, rejection *Rejection) Decision {
	decision.Outcome = OutcomeRejected
	decision.Rejection = rejection
	decision.After = nil
	return decision
}

func readinessBlocks(readiness Readiness) bool {
	return readinessContractFailure(readiness) != "" || readinessBlocker(readiness) != ""
}
