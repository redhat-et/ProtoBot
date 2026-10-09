package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

const (
	outcomeRead     = OutcomeRead
	outcomeApplied  = OutcomeApplied
	outcomeReplayed = OutcomeReplayed
	outcomeRejected = OutcomeRejected

	mutationNone    = MutationNone
	mutationApplied = MutationApplied

	idempotencyNew      = IdempotencyNew
	idempotencyReplayed = IdempotencyReplayed
)

// Execute runs one operation against the in-memory WMS. Gate identity comes
// from actorContextRef; authorization claims are never accepted from call.
func (m *Memory) Execute(call CallRequest) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	// The wire carries operation names as strings; they enter the typed
	// Operation vocabulary here, once, at the adapter boundary.
	operation := validation.Operation(call.Operation)
	if !adapter.IsAdapterOperation(operation) {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, ""))
	}
	authorization, ok := m.gate.Resolve(call.ActorContextRef)
	if !ok {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, ""))
	}
	if operation == validation.OperationLifecyclePreflight {
		return m.executePreflightLocked(call, authorization)
	}
	if isLifecycleOperation(operation) {
		return m.executeLifecycleLocked(call, authorization)
	}
	if !roleHasWMSOperation(authorization.Role, operation) {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
	}
	if rejection := validation.AuthorizeContext(
		authorization,
		operation,
		m.projectID,
		call.WorkItemID,
		"",
		[]string{"project:" + m.projectID},
		m.now(),
	); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	if call.PolicyVersion != "" && call.PolicyVersion != authorization.PolicyVersion {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
	}
	if operation == validation.OperationFindingCreate {
		return rejectedResult(call.Operation, wmsRejection(
			CodeWMSUnavailable,
			"The in-memory Drafting Table adapter does not implement the Finding Ledger.",
			map[string]any{},
			validation.RetryRefresh,
		))
	}
	return m.executeRequestLocked(call, authorization)
}

// ObserveIndependentInspectorConfirmation records a confirmation event that
// the trusted WMS Finding Ledger has already validated. This fixture hook is
// separate from CallRequest so a caller cannot assert Inspector confirmation
// in a lifecycle payload.
func (m *Memory) ObserveIndependentInspectorConfirmation(submissionID, confirmationID string) error {
	if isBlank(submissionID) || isBlank(confirmationID) {
		return errors.New("resolution submission and confirmation IDs must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	submission, exists := m.submissions[submissionID]
	if !exists {
		return errors.New("resolution submission does not exist")
	}
	if submission.IndependentInspectorConfirmationID == confirmationID {
		return nil
	}
	if submission.Kind != "out-of-scope" || submission.Status != validation.ResolutionSubmissionStatusPending ||
		m.activeSubmissions[submission.WorkItemID] != submissionID {
		return errors.New("confirmation requires the active out-of-scope resolution submission")
	}
	if submission.IndependentInspectorConfirmationID != "" {
		return errors.New("resolution submission already has a different Inspector confirmation")
	}

	submission.IndependentInspectorConfirmationID = confirmationID
	submission.IndependentInspectorConfirmed = true
	m.resolutionRevisions[submission.WorkItemID]++
	submission.Revision = m.resolutionRevisions[submission.WorkItemID]
	m.submissions[submissionID] = submission
	return nil
}

// ObserveReadiness records trusted readiness evidence observed by the WMS
// itself. This fixture hook is separate from CallRequest so a caller cannot
// assert readiness evidence in a lifecycle payload. The observation feeds
// refresh-dependencies, revalidate, refresh-active, and resolve-block
// evaluation and is persisted when such a transition commits.
func (m *Memory) ObserveReadiness(workItemID string, readiness validation.Readiness) error {
	if isBlank(workItemID) {
		return errors.New("work item ID must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.workItems[workItemID]; !exists {
		return errors.New("work item does not exist")
	}
	m.observedReadiness[workItemID] = readiness
	return nil
}

// ObserveInspection records trusted inspection evidence observed by the WMS
// itself. This fixture hook is separate from CallRequest so a caller cannot
// assert a sealed Inspection Run, terminal findings, or the final-test gate
// in a lifecycle payload. The observation is persisted on the inspecting
// work item so begin-merge evaluates the WMS-owned current record.
func (m *Memory) ObserveInspection(workItemID string, evidence validation.InspectionEvidence) error {
	if isBlank(workItemID) {
		return errors.New("work item ID must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	item, exists := m.workItems[workItemID]
	if !exists {
		return errors.New("work item does not exist")
	}
	if item.State != validation.StateInspecting {
		return errors.New("inspection evidence can be recorded only while inspecting")
	}
	item = cloneWorkItem(item)
	item.InspectionRunSealed = evidence.InspectionRunSealed
	item.FindingsTerminal = evidence.FindingsTerminal
	item.FinalTestsPassed = evidence.FinalTestsPassed
	m.workItems[workItemID] = item
	return nil
}

// ObserveReconciliation records trusted Git/WMS reconciliation evidence
// observed by the WMS itself. This fixture hook is separate from CallRequest
// so a caller cannot assert reconciliation evidence in a lifecycle payload.
// The observation is persisted on the work item so reconciler operations
// evaluate the WMS-owned current record.
func (m *Memory) ObserveReconciliation(workItemID string, evidence validation.ReconciliationEvidence) error {
	if isBlank(workItemID) {
		return errors.New("work item ID must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	item, exists := m.workItems[workItemID]
	if !exists {
		return errors.New("work item does not exist")
	}
	item = cloneWorkItem(item)
	item.Reconciliation = validation.ReconciliationEvidence{
		Status:        evidence.Status,
		GitMutation:   evidence.GitMutation,
		MergeEnvelope: cloneMergeEnvelope(evidence.MergeEnvelope),
	}
	m.workItems[workItemID] = item
	return nil
}

func (m *Memory) executePreflightLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	var payload struct {
		Operation validation.Operation `json:"operation"`
		validation.Payload
	}
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if call.HumanApprovalID != "" {
		payload.HumanApprovalID = call.HumanApprovalID
	}
	workItemID := call.WorkItemID
	if payload.Operation == validation.OperationMaterialize && workItemID == "" && payload.WorkItem != nil {
		workItemID = payload.WorkItem.ID
	}
	item, exists := m.workItems[workItemID]
	var current *validation.WorkItem
	if exists {
		copy := cloneWorkItem(item)
		current = &copy
	}
	request := validation.Request{
		Operation:               payload.Operation,
		ProjectID:               m.projectID,
		WorkItemID:              workItemID,
		MaterializationKey:      requestMaterializationKey(call, payload.Payload),
		References:              lifecycleReferences(payload.Operation, authorization.Role, payload.Payload),
		ExpectedState:           call.ExpectedState,
		ExpectedContractVersion: call.ExpectedContractVersion,
		FencingToken:            call.FencingToken,
		Payload:                 payload.Payload,
		Authorization:           authorization,
	}
	preview := &validation.ResolutionPreview{
		Kind:        payload.ResolutionKind,
		ChangeSetID: payload.ChangeSetID,
	}
	if submission, exists := m.submissions[payload.ResolutionSubmissionID]; payload.ResolutionSubmissionID != "" && exists &&
		submission.WorkItemID == workItemID && submission.Status == validation.ResolutionSubmissionStatusPending &&
		m.activeSubmissions[workItemID] == payload.ResolutionSubmissionID {
		preview.Kind = submission.Kind
		preview.ChangeSetID = submission.ChangeSetID
		preview.InspectorConfirmed = submission.IndependentInspectorConfirmationID != ""
	}
	if preview.Kind == "add-requirement" {
		preview.PlannedDependencyComplete = m.plannedDependencyComplete(preview.ChangeSetID)
	}
	evaluation := validation.EvaluationContext{
		Authority:         validation.AuthorityPreflight,
		EvaluationTime:    m.now(),
		PreviewResolution: preview,
	}
	if payload.Operation == validation.OperationMaterialize && payload.WorkItem != nil {
		dependencies := m.liveDependenciesLocked(*payload.WorkItem)
		evaluation.RefreshDependencies = &dependencies
	}
	if current != nil && (payload.Operation == validation.OperationRefreshDependencies || payload.Operation == validation.OperationResolveBlock ||
		payload.Operation == validation.OperationRevalidate || payload.Operation == validation.OperationRefreshActive) {
		dependencies := m.liveDependenciesLocked(*current)
		evaluation.RefreshDependencies = &dependencies
	}
	if current != nil && (payload.Operation == validation.OperationRefreshDependencies || payload.Operation == validation.OperationResolveBlock ||
		payload.Operation == validation.OperationRevalidate || payload.Operation == validation.OperationRefreshActive) {
		if readiness, observed := m.observedReadiness[workItemID]; observed {
			evaluation.RefreshReadiness = &readiness
		}
	}
	decision := validation.Evaluate(request, current, evaluation)
	if call.PolicyVersion != "" && call.PolicyVersion != authorization.PolicyVersion &&
		validation.AuthorizePreflight(
			authorization,
			m.projectID,
			workItemID,
			payload.ChangeSetID,
			request.References,
			evaluation.EvaluationTime,
		) == nil {
		decision = validation.RejectionDecision(request, validation.AuthorityPreflight,
			unauthorizedRejection(string(payload.Operation), workItemID, authorization.PolicyVersion))
	}
	return resultFromDecision(call.Operation, decision)
}

func (m *Memory) executeLifecycleLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	request, rejection := m.normalizeLifecycleRequest(call, authorization)
	if rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	evaluation := validation.EvaluationContext{
		Authority:             validation.AuthorityAuthoritative,
		EvaluationTime:        m.now(),
		LeaseDuration:         m.leaseDuration,
		NextFencingToken:      fmt.Sprintf("fence-%d", m.nextFencingToken+1),
		Approvals:             m.approvalSnapshotLocked(),
		ResolutionSubmissions: m.resolutionSnapshotLocked(),
	}
	if validation.ValidateAuthorizationContext(
		request.Authorization,
		request.Operation,
		request.ProjectID,
		request.WorkItemID,
		request.Payload.ChangeSetID,
		evaluation.EvaluationTime,
	) != nil {
		return resultFromDecision(call.Operation, validation.Evaluate(request, nil, evaluation))
	}
	var current *validation.WorkItem
	item, exists := m.workItems[request.WorkItemID]
	if request.Operation == validation.OperationMaterialize {
		if exists {
			copy := cloneWorkItem(item)
			current = &copy
		}
		// A new materialization has no stored record yet, but the dependency
		// states must still come from the WMS's own observation of the
		// referenced items, never from the caller's payload copy.
		if request.Payload.WorkItem != nil {
			dependencies := m.liveDependenciesLocked(*request.Payload.WorkItem)
			evaluation.RefreshDependencies = &dependencies
		}
	} else {
		if !exists {
			// A missing target is a rejected mutating request: if the caller
			// is authorized to perform this lifecycle mutation and the policy
			// version matches, record the NOT_FOUND under the idempotency key
			// so a later appearance of the target cannot re-evaluate that key.
			// An unauthorized caller receives the visibility-safe NOT_FOUND
			// without reading or writing the project-scoped idempotency map.
			authorized := validation.AuthorizeLifecycle(
				request.Authorization,
				request.Operation,
				request.ProjectID,
				request.WorkItemID,
				request.Payload.ChangeSetID,
				request.References,
				evaluation.EvaluationTime,
			) == nil && (call.PolicyVersion == "" || call.PolicyVersion == authorization.PolicyVersion)

			if authorized && request.IdempotencyKey != "" {
				fingerprint := validation.RequestFingerprint(request)
				if result, handled := m.replayLifecycleRequest(call, request, fingerprint); handled {
					return result
				}
				result := resultFromDecision(call.Operation, validation.Evaluate(request, nil, evaluation))
				m.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, result)
				return result
			}
			return resultFromDecision(call.Operation, validation.Evaluate(request, nil, evaluation))
		}
		copy := cloneWorkItem(item)
		copy.ActiveResolutionSubmissionID = m.activeSubmissions[request.WorkItemID]
		current = &copy
	}
	if validation.AuthorizeLifecycle(
		request.Authorization,
		request.Operation,
		request.ProjectID,
		request.WorkItemID,
		request.Payload.ChangeSetID,
		request.References,
		evaluation.EvaluationTime,
	) != nil {
		return resultFromDecision(call.Operation, validation.Evaluate(request, current, evaluation))
	}
	if call.PolicyVersion != "" && call.PolicyVersion != authorization.PolicyVersion {
		decision := validation.RejectionDecision(request, validation.AuthorityAuthoritative,
			unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
		return resultFromDecision(call.Operation, decision)
	}
	if request.IdempotencyKey == "" {
		return rejectedResult(call.Operation, invalidRequest("idempotency_key", "authoritative mutations require an idempotency key"))
	}
	fingerprint := validation.RequestFingerprint(request)
	if result, handled := m.replayLifecycleRequest(call, request, fingerprint); handled {
		return result
	}
	if current != nil && (request.Operation == validation.OperationRefreshDependencies || request.Operation == validation.OperationResolveBlock ||
		request.Operation == validation.OperationRevalidate || request.Operation == validation.OperationRefreshActive ||
		request.Operation == validation.OperationMaterialize) {
		dependencies := m.liveDependenciesLocked(*current)
		evaluation.RefreshDependencies = &dependencies
	}
	if current != nil && (request.Operation == validation.OperationRefreshDependencies || request.Operation == validation.OperationResolveBlock ||
		request.Operation == validation.OperationRevalidate || request.Operation == validation.OperationRefreshActive) {
		if readiness, observed := m.observedReadiness[request.WorkItemID]; observed {
			evaluation.RefreshReadiness = &readiness
		}
	}
	return m.applyLifecycleRequest(call.Operation, request, current, authorization, fingerprint, evaluation)
}

func (m *Memory) normalizeLifecycleRequest(call CallRequest, authorization validation.AuthorizationContext) (validation.Request, *validation.Rejection) {
	var payload validation.Payload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return validation.Request{}, invalidRequest("payload", err.Error())
	}
	operation := validation.Operation(call.Operation)
	workItemID := call.WorkItemID
	if operation == validation.OperationMaterialize && workItemID == "" && payload.WorkItem != nil {
		workItemID = payload.WorkItem.ID
	}
	if payload.ResolutionSubmissionID != "" && payload.ChangeSetID == "" {
		if submission, exists := m.submissions[payload.ResolutionSubmissionID]; exists {
			payload.ChangeSetID = submission.ChangeSetID
		}
	}
	if call.HumanApprovalID != "" {
		payload.HumanApprovalID = call.HumanApprovalID
	}
	request := validation.Request{
		Operation:               operation,
		ProjectID:               m.projectID,
		WorkItemID:              workItemID,
		MaterializationKey:      requestMaterializationKey(call, payload),
		IdempotencyKey:          call.IdempotencyKey,
		ExpectedState:           call.ExpectedState,
		ExpectedContractVersion: call.ExpectedContractVersion,
		FencingToken:            call.FencingToken,
		References:              lifecycleReferences(operation, authorization.Role, payload),
		Payload:                 payload,
		Authorization:           authorization,
	}
	return request, nil
}

// lifecycleReferences returns the Git/resource references consumed from this
// operation's caller payload: the merge target of a materialization source,
// and the tested-candidate or merge-result envelope of a Job Site
// begin-merge/record-merge. Reconciler record-merge uses WMS-observed evidence.
func lifecycleReferences(operation validation.Operation, role validation.Role, payload validation.Payload) []string {
	var envelope *validation.MergeEnvelope
	switch operation {
	case validation.OperationMaterialize:
		if payload.WorkItem != nil {
			envelope = payload.WorkItem.ExpectedMerge
		}
	case validation.OperationBeginMerge, validation.OperationRecordMerge:
		if role != validation.RoleReconciler {
			envelope = payload.MergeEnvelope
		}
	}
	if envelope == nil {
		return nil
	}

	refs := make([]string, 0, 4)
	for _, ref := range []string{envelope.Target, envelope.IntegrationHead, envelope.MergeCommit, envelope.InspectionRunID} {
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (m *Memory) replayLifecycleRequest(call CallRequest, request validation.Request, fingerprint string) (Result, bool) {
	if replay, conflict := m.checkIdempotencyLocked(call.Operation, request.IdempotencyKey, fingerprint); replay != nil {
		return *replay, true
	} else if conflict != nil {
		decision := validation.RejectionDecision(request, validation.AuthorityAuthoritative, conflict)
		return resultFromDecision(call.Operation, decision), true
	}
	if request.Operation == validation.OperationMaterialize {
		if replay, rejection := m.checkMaterializationLocked(request); replay != nil {
			m.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, *replay)
			return *replay, true
		} else if rejection != nil {
			decision := validation.RejectionDecision(request, validation.AuthorityAuthoritative, rejection)
			result := resultFromDecision(call.Operation, decision)
			m.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, result)
			return result, true
		}
	}
	return Result{}, false
}

func (m *Memory) applyLifecycleRequest(
	operation string,
	request validation.Request,
	current *validation.WorkItem,
	authorization validation.AuthorizationContext,
	fingerprint string,
	evaluation validation.EvaluationContext,
) Result {
	decision := validation.Evaluate(request, current, evaluation)
	result := resultFromDecision(operation, decision)
	switch decision.Outcome {
	case validation.OutcomeAllowed:
		if request.Operation == validation.OperationMaterialize {
			item := cloneWorkItem(validation.CanonicalMaterializationSource(*request.Payload.WorkItem, request.Payload.ChangeType))
			item.State = decision.After.State
			item.ContractVersion = decision.After.ContractVersion
			item.MaterializationKey = request.MaterializationKey
			item.Owner = ""
			item.Lease = nil
			// The Materializer's payload copy is caller evidence. The stored
			// record keeps the dependency states the WMS itself observed.
			if evaluation.RefreshDependencies != nil {
				item.Dependencies = append([]validation.Dependency(nil), (*evaluation.RefreshDependencies)...)
			}
			// Observed execution evidence belongs to Building/Inspecting
			// phases; a materialized record starts with none.
			item.Reconciliation = validation.ReconciliationEvidence{}
			item.InspectionRunSealed = false
			item.FindingsTerminal = false
			item.FinalTestsPassed = false
			if item.State == validation.StateBlocked {
				item.BlockReason = validation.ReadinessFailure(item.Readiness)
			}
			m.workItems[item.ID] = item
			result.WorkItemID = item.ID
			result.WorkItemState = item.State
			result.ContractVersion = item.ContractVersion
			result.Resource = cloneWorkItem(item)
		} else {
			updated := cloneWorkItem(*current)
			m.applyLifecycleMutationLocked(&updated, request, authorization, decision, evaluation)
			m.workItems[updated.ID] = updated
			result.WorkItemID = updated.ID
			result.WorkItemState = updated.State
			result.ContractVersion = updated.ContractVersion
		}
		m.nextFencingToken += boolToUint64(decision.FencingTokenIssued != "")
		m.events = append(m.events, AuditEvent{
			Operation:              string(request.Operation),
			Subject:                authorization.Subject,
			AuthorizedHumanSubject: m.approvalSubject(request.Payload.HumanApprovalID, request.Operation),
			WorkItemID:             request.WorkItemID,
			IdempotencyKey:         request.IdempotencyKey,
			Outcome:                result.Outcome,
			RuleVersion:            decision.RuleVersion,
			PolicyVersion:          decision.PolicyVersion,
			Before:                 cloneStateVersion(decision.Before),
			After:                  cloneStateVersion(decision.After),
		})
	case validation.OutcomeOmitted:
		result.Submission = "omitted"
		m.events = append(m.events, AuditEvent{
			Operation:              string(request.Operation),
			Subject:                authorization.Subject,
			AuthorizedHumanSubject: m.approvalSubject(request.Payload.HumanApprovalID, request.Operation),
			WorkItemID:             request.WorkItemID,
			IdempotencyKey:         request.IdempotencyKey,
			Outcome:                result.Outcome,
			RuleVersion:            decision.RuleVersion,
			PolicyVersion:          decision.PolicyVersion,
			Before:                 cloneStateVersion(decision.Before),
		})
	}
	if request.Operation == validation.OperationResolveBlock && decision.Outcome == validation.OutcomeAllowed {
		m.consumeResolutionApprovalLocked(request)
		result.ApprovalStatus = validation.ApprovalStatusConsumed
	} else if request.Operation == validation.OperationResolveBlock && request.Payload.HumanApprovalID != "" {
		if approval, exists := m.approvals[request.Payload.HumanApprovalID]; exists {
			result.ApprovalStatus = approval.Status
		}
	}
	if request.Operation == validation.OperationMaterialize && decision.Outcome != validation.OutcomeRejected {
		m.materializations[request.MaterializationKey] = materializationEntry{
			fingerprint: decision.MaterializationReservation.SourceFingerprint,
			result:      cloneResult(result),
		}
	}
	m.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, result)
	return result
}

func isLifecycleOperation(operation validation.Operation) bool {
	switch operation {
	case validation.OperationMaterialize, validation.OperationRefreshDependencies,
		validation.OperationRevalidate, validation.OperationResolveBlock,
		validation.OperationClaim, validation.OperationRenewLease,
		validation.OperationTestsPass, validation.OperationRaiseSpecQuestion,
		validation.OperationRefreshActive, validation.OperationReturnToBuilding,
		validation.OperationBeginMerge, validation.OperationMergeConflict,
		validation.OperationMergeNotApplied, validation.OperationRecordMerge,
		validation.OperationRecoverLease, validation.OperationAbandon:
		return true
	default:
		return false
	}
}

func requestMaterializationKey(call CallRequest, payload validation.Payload) string {
	if call.MaterializationKey != "" {
		return call.MaterializationKey
	}
	if payload.MaterializationKey != "" {
		return payload.MaterializationKey
	}
	if payload.WorkItem != nil {
		return payload.WorkItem.MaterializationKey
	}
	return ""
}

func (m *Memory) plannedDependencyComplete(changeSetID string) bool {
	changeSet, exists := m.changeSets[changeSetID]
	if !exists || changeSet.BuildWorkItemID == "" {
		return false
	}
	workItem, exists := m.workItems[changeSet.BuildWorkItemID]
	return exists && workItem.State == validation.StateCompleted
}

func (m *Memory) liveDependenciesLocked(item validation.WorkItem) []validation.Dependency {
	dependencies := make([]validation.Dependency, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		state := validation.StateInitial
		if observed, exists := m.workItems[dependency.ID]; exists && observed.ProjectID == m.projectID {
			state = observed.State
		}
		dependencies = append(dependencies, validation.Dependency{ID: dependency.ID, State: state})
	}
	return dependencies
}

func decodePayload(data []byte, target any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("payload contains more than one JSON value")
		}
		return err
	}
	return nil
}

func newResult(operation string) Result {
	return Result{
		OK:          true,
		Operation:   operation,
		Outcome:     outcomeRead,
		Diagnostics: []Diagnostic{},
		Mutation:    mutationNone,
	}
}

func rejectedResult(operation string, rejection *validation.Rejection) Result {
	return Result{
		OK:          false,
		Operation:   operation,
		Outcome:     outcomeRejected,
		Diagnostics: []Diagnostic{},
		Mutation:    mutationNone,
		Error:       rejection,
	}
}

func resultFromDecision(operation string, decision validation.Decision) Result {
	result := newResult(operation)
	result.Decision = &decision
	if decision.Authority == validation.AuthorityPreflight {
		return result
	}
	switch decision.Outcome {
	case validation.OutcomeRejected:
		result.OK = false
		result.Outcome = outcomeRejected
		result.Error = decision.Rejection
	case validation.OutcomeOmitted:
		result.Outcome = outcomeApplied
		result.Mutation = mutationApplied
		result.Idempotency = idempotencyNew
	default:
		result.Outcome = outcomeApplied
		result.Mutation = mutationApplied
		result.Idempotency = idempotencyNew
	}
	return result
}

func (m *Memory) approvalSubject(approvalID string, operation validation.Operation) string {
	switch operation {
	case validation.OperationResolveBlock, validation.OperationBlockedWorkSubmitResolution,
		validation.OperationBlockedWorkAcknowledge, validation.OperationRequestRefine:
		if approval, exists := m.approvals[approvalID]; exists {
			return approval.ApprovedSubject
		}
	}
	return ""
}

func invalidRequest(field, message string) *validation.Rejection {
	return wmsRejection(
		CodeInvalidRequest,
		"The request has a field that is missing or invalid.",
		map[string]any{"field": field, "reason": message},
		validation.RetryNewKey,
	)
}

func wmsRejection(code, message string, details map[string]any, retry string) *validation.Rejection {
	return &validation.Rejection{Code: code, Message: message, Details: details, Retry: retry}
}

func unauthorizedRejection(operation, workItemID, policyVersion string) *validation.Rejection {
	targetType := "project"
	if workItemID != "" {
		targetType = "work-item"
	}
	return wmsRejection(
		validation.CodeUnauthorizedAction,
		"The trusted authorization context does not permit this action or target.",
		map[string]any{
			"required_action": operation,
			"target_type":     targetType,
			"policy_version":  policyVersion,
		},
		validation.RetryAuthorize,
	)
}

func validationNotFound(targetType string) *validation.Rejection {
	return wmsRejection(
		validation.CodeNotFound,
		"The target is not visible in the trusted project context.",
		map[string]any{"target_type": targetType, "visibility_scope": "project"},
		validation.RetryRefresh,
	)
}

func (m *Memory) idempotencyScope(key string) string {
	return m.projectID + "\x00" + key
}

func (m *Memory) checkIdempotencyLocked(operation, key, fingerprint string) (*Result, *validation.Rejection) {
	entry, exists := m.idempotency[m.idempotencyScope(key)]
	if !exists {
		return nil, nil
	}
	if entry.fingerprint != fingerprint {
		return nil, wmsRejection(
			validation.CodeIdempotencyConflict,
			"The idempotency key was reused with a different request.",
			map[string]any{
				"conflict_kind":               "request-fingerprint",
				"key_scope":                   "project",
				"request_fingerprint_digest":  fingerprint,
				"recorded_fingerprint_digest": entry.fingerprint,
			},
			validation.RetryNewKey,
		)
	}
	result := cloneResult(entry.result)
	result.Outcome = outcomeReplayed
	result.Mutation = mutationNone
	result.Idempotency = idempotencyReplayed
	if result.Decision != nil {
		result.Decision.Replayed = true
	}
	return &result, nil
}

func (m *Memory) checkMaterializationLocked(request validation.Request) (*Result, *validation.Rejection) {
	if request.Payload.WorkItem == nil {
		return nil, nil
	}
	entry, exists := m.materializations[request.MaterializationKey]
	if !exists {
		return nil, nil
	}
	// The evaluator's target check must also gate the create-or-return
	// replay: a request naming another work item or project must not
	// receive the record this key replays.
	if rejection := validation.MaterializationTargetRejection(request, request.Payload.WorkItem); rejection != nil {
		return nil, rejection
	}
	source := validation.CanonicalMaterializationSource(*request.Payload.WorkItem, request.Payload.ChangeType)
	// The evaluator's source-contract check must also gate the create-or-
	// return replay: SourceFingerprint binds only the merge target, so an
	// invalid source asserting tested-candidate fields would otherwise
	// fingerprint like the valid original and replay its success.
	if rejection := validation.MaterializationSourceMergeRejection(source); rejection != nil {
		return nil, rejection
	}
	fingerprint := validation.SourceFingerprint(source)
	if fingerprint != entry.fingerprint {
		return nil, wmsRejection(
			validation.CodeIdempotencyConflict,
			"The materialization key is already bound to a different source contract.",
			map[string]any{
				"conflict_kind":          "materialization-source",
				"key_scope":              "project",
				"materialization_key":    request.MaterializationKey,
				"recorded_source_digest": entry.fingerprint,
			},
			validation.RetryReconcile,
		)
	}
	result := cloneResult(entry.result)
	result.Outcome = outcomeReplayed
	result.Mutation = mutationNone
	result.Idempotency = idempotencyReplayed
	if result.Decision != nil {
		result.Decision.Replayed = true
	}
	return &result, nil
}

func (m *Memory) rememberIdempotencyLocked(key, fingerprint string, result Result) {
	m.idempotency[m.idempotencyScope(key)] = idempotencyEntry{
		fingerprint: fingerprint,
		result:      cloneResult(result),
	}
}

func (m *Memory) approvalSnapshotLocked() map[string]validation.ApprovalRecord {
	result := make(map[string]validation.ApprovalRecord, len(m.approvals))
	for id, approval := range m.approvals {
		result[id] = cloneApproval(approval)
	}
	return result
}

func (m *Memory) resolutionSnapshotLocked() map[string]validation.ResolutionSubmission {
	result := make(map[string]validation.ResolutionSubmission, len(m.submissions))
	for id, submission := range m.submissions {
		resolution := submission.ResolutionSubmission
		resolution.IndependentInspectorConfirmed = resolution.IndependentInspectorConfirmationID != ""
		if resolution.Kind == "add-requirement" {
			resolution.PlannedDependencyComplete = m.plannedDependencyComplete(resolution.ChangeSetID)
		}
		result[id] = resolution
	}
	return result
}

func (m *Memory) consumeResolutionApprovalLocked(request validation.Request) {
	approval := m.approvals[request.Payload.HumanApprovalID]
	approval.ID = request.Payload.HumanApprovalID
	approval.Status = validation.ApprovalStatusConsumed
	m.approvals[approval.ID] = approval
	submission := m.submissions[request.Payload.ResolutionSubmissionID]
	submission.Status = validation.ResolutionSubmissionStatusConsumed
	m.submissions[submission.ID] = submission
	m.activeSubmissions[request.WorkItemID] = ""
}

func cloneWorkItem(item validation.WorkItem) validation.WorkItem {
	item.Dependencies = append([]validation.Dependency(nil), item.Dependencies...)
	item.Readiness.UnresolvedReasons = append([]string(nil), item.Readiness.UnresolvedReasons...)
	if item.Lease != nil {
		lease := *item.Lease
		item.Lease = &lease
	}
	item.ExpectedMerge = cloneMergeEnvelope(item.ExpectedMerge)
	item.Reconciliation.MergeEnvelope = cloneMergeEnvelope(item.Reconciliation.MergeEnvelope)
	return item
}

func cloneMergeEnvelope(envelope *validation.MergeEnvelope) *validation.MergeEnvelope {
	if envelope == nil {
		return nil
	}
	copy := *envelope
	return &copy
}

func cloneApproval(approval validation.ApprovalRecord) validation.ApprovalRecord {
	if approval.ExpectedContractVersion != nil {
		version := *approval.ExpectedContractVersion
		approval.ExpectedContractVersion = &version
	}
	return approval
}

func cloneRequest(request RequestRecord) RequestRecord {
	request.AffectedInterfaces = append([]string(nil), request.AffectedInterfaces...)
	request.AffectedScopes = append([]string(nil), request.AffectedScopes...)
	request.Relationships = append([]RequestRelationship(nil), request.Relationships...)
	return request
}

func cloneSubmission(submission Submission) Submission {
	return submission
}

func cloneResult(result Result) Result {
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	var copy Result
	if err := json.Unmarshal(encoded, &copy); err != nil {
		panic(err)
	}
	return copy
}

func cloneStateVersion(version *validation.StateVersion) *validation.StateVersion {
	if version == nil {
		return nil
	}
	copy := *version
	return &copy
}

func boolToUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func (m *Memory) nextRequestIDLocked() string {
	m.nextRequestID++
	return fmt.Sprintf("request-%03d", m.nextRequestID)
}

func (m *Memory) nextSubmissionIDLocked(acknowledgement bool) string {
	prefix := "resolution-submission"
	if acknowledgement {
		prefix = "acknowledgement"
		m.nextAcknowledgementID++
		return fmt.Sprintf("%s-%03d", prefix, m.nextAcknowledgementID)
	}
	m.nextResolutionSubmissionID++
	return fmt.Sprintf("%s-%03d", prefix, m.nextResolutionSubmissionID)
}

func isBlank(value string) bool {
	return strings.TrimSpace(value) == ""
}
