package memory

import (
	"github.com/redhat-et/protobot/wms/validation"
)

type blockedResolutionPayload struct {
	ResolutionKind           string `json:"resolution_kind"`
	ChangeSetID              string `json:"change_set_id,omitempty"`
	ApprovalResolutionDigest string `json:"approval_resolution_digest"`
	Reason                   string `json:"reason,omitempty"`
}

func (m *Memory) submitResolutionLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	item, exists := m.workItems[call.WorkItemID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("work-item"))
	}
	if rejection := validateBlockedExpected(call, item); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	var payload blockedResolutionPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if payload.ResolutionKind != "add-requirement" && payload.ResolutionKind != "out-of-scope" && payload.ResolutionKind != "impact-amendment" {
		return rejectedResult(call.Operation, invalidRequest("resolution_kind", "unsupported lifecycle resolution kind"))
	}
	if payload.ApprovalResolutionDigest == "" || call.HumanApprovalID == "" {
		return rejectedResult(call.Operation, approvalRejected(call.Operation, authorization, "work-item"))
	}
	if payload.ResolutionKind == "add-requirement" || payload.ResolutionKind == "impact-amendment" {
		if _, exists := m.changeSets[payload.ChangeSetID]; !exists {
			return rejectedResult(call.Operation, validationNotFound("change-set"))
		}
	}
	approval, exists := m.approvals[call.HumanApprovalID]
	if !exists || m.materializerSubject == "" {
		return rejectedResult(call.Operation, approvalRejected(call.Operation, authorization, "work-item"))
	}
	requirement := validation.ApprovalRequirement{
		Action:                  validation.OperationResolveBlock,
		DelegatedPrincipal:      m.materializerSubject,
		ProjectID:               m.projectID,
		WorkItemID:              call.WorkItemID,
		ResolutionKind:          payload.ResolutionKind,
		Digest:                  payload.ApprovalResolutionDigest,
		ExpectedState:           call.ExpectedState,
		ExpectedContractVersion: call.ExpectedContractVersion,
		PolicyVersion:           authorization.PolicyVersion,
	}
	if rejection := validation.ValidateApproval(approval, requirement, m.now()); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}

	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.Submission = "accepted"
	result.ApprovalStatus = validation.ApprovalStatusUnused
	result.WorkItemID = item.ID
	result.WorkItemState = item.State
	result.ContractVersion = item.ContractVersion
	if payload.ResolutionKind == "add-requirement" {
		dependencyStatus := "incomplete"
		if m.plannedDependencyComplete(payload.ChangeSetID) {
			dependencyStatus = "complete"
		}
		result.PlannedDependency = &PlannedDependency{ChangeSetID: payload.ChangeSetID, Status: dependencyStatus}
	}
	if priorID := m.activeSubmissions[item.ID]; priorID != "" {
		prior := m.submissions[priorID]
		if prior.Status == validation.ResolutionSubmissionStatusPending {
			prior.Status = validation.ResolutionSubmissionStatusSuperseded
			m.submissions[priorID] = prior
			result.PriorResolutionSubmissionID = priorID
			result.PriorSubmissionStatus = validation.ResolutionSubmissionStatusSuperseded
			if prior.ApprovalID == call.HumanApprovalID {
				result.PriorApprovalStatus = m.approvals[prior.ApprovalID].Status
			} else {
				priorApproval := m.approvals[prior.ApprovalID]
				priorApproval.ID = prior.ApprovalID
				priorApproval.Status = validation.ApprovalStatusRevoked
				m.approvals[prior.ApprovalID] = priorApproval
				result.PriorApprovalStatus = validation.ApprovalStatusRevoked
			}
		}
	}
	m.resolutionRevisions[item.ID]++
	revision := m.resolutionRevisions[item.ID]
	submissionID := m.nextSubmissionIDLocked(false)
	submission := Submission{
		ResolutionSubmission: validation.ResolutionSubmission{
			ID:                   submissionID,
			WorkItemID:           item.ID,
			Kind:                 payload.ResolutionKind,
			ChangeSetID:          payload.ChangeSetID,
			ApprovalID:           call.HumanApprovalID,
			ApprovalDigest:       payload.ApprovalResolutionDigest,
			ApprovedHumanSubject: approval.ApprovedSubject,
			Status:               validation.ResolutionSubmissionStatusPending,
		},
		Revision: revision,
	}
	m.submissions[submissionID] = submission
	m.activeSubmissions[item.ID] = submissionID
	result.ResolutionSubmissionID = submissionID
	result.ResolutionSubmissionRevision = revision
	m.events = append(m.events, AuditEvent{
		Operation:              call.Operation,
		Subject:                authorization.Subject,
		AuthorizedHumanSubject: approval.ApprovedSubject,
		WorkItemID:             item.ID,
		PolicyVersion:          authorization.PolicyVersion,
	})
	return result
}

func (m *Memory) acknowledgeBlockedWorkLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	item, exists := m.workItems[call.WorkItemID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("work-item"))
	}
	if rejection := validateBlockedExpected(call, item); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	var payload blockedResolutionPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if isBlank(payload.Reason) || payload.ApprovalResolutionDigest == "" || call.HumanApprovalID == "" {
		return rejectedResult(call.Operation, approvalRejected(call.Operation, authorization, "work-item"))
	}
	approval, exists := m.approvals[call.HumanApprovalID]
	if !exists {
		return rejectedResult(call.Operation, approvalRejected(call.Operation, authorization, "work-item"))
	}
	requirement := validation.ApprovalRequirement{
		Action:                  validation.Operation("blocked-work.acknowledge"),
		DelegatedPrincipal:      authorization.Subject,
		ProjectID:               m.projectID,
		WorkItemID:              call.WorkItemID,
		Digest:                  payload.ApprovalResolutionDigest,
		ExpectedState:           call.ExpectedState,
		ExpectedContractVersion: call.ExpectedContractVersion,
		PolicyVersion:           authorization.PolicyVersion,
	}
	if rejection := validation.ValidateApproval(approval, requirement, m.now()); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	approval.ID = call.HumanApprovalID
	approval.Status = validation.ApprovalStatusConsumed
	m.approvals[call.HumanApprovalID] = approval
	submissionID := m.nextSubmissionIDLocked(true)
	submission := Submission{
		ResolutionSubmission: validation.ResolutionSubmission{
			ID:             submissionID,
			WorkItemID:     item.ID,
			Kind:           "acknowledge",
			ApprovalID:     call.HumanApprovalID,
			ApprovalDigest: payload.ApprovalResolutionDigest,
			Status:         validation.ResolutionSubmissionStatusConsumed,
		},
		Revision: 1,
	}
	m.submissions[submissionID] = submission
	m.events = append(m.events, AuditEvent{
		Operation:              call.Operation,
		Subject:                authorization.Subject,
		AuthorizedHumanSubject: approval.ApprovedSubject,
		WorkItemID:             item.ID,
		PolicyVersion:          authorization.PolicyVersion,
	})
	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.ApprovalStatus = validation.ApprovalStatusConsumed
	result.WorkItemID = item.ID
	result.WorkItemState = item.State
	result.ContractVersion = item.ContractVersion
	result.ResolutionSubmissionID = submissionID
	result.ResolutionSubmissionRevision = 1
	return result
}

func validateBlockedExpected(call CallRequest, item validation.WorkItem) *validation.Rejection {
	if call.ExpectedState != item.State {
		return &validation.Rejection{
			Code:    validation.CodeStaleState,
			Message: "The work item state changed after this operation was prepared.",
			Details: map[string]any{"expected_state": call.ExpectedState, "current_state": item.State},
			Retry:   validation.RetryRefresh,
		}
	}
	if call.ExpectedContractVersion == nil || *call.ExpectedContractVersion != item.ContractVersion {
		var expected any
		if call.ExpectedContractVersion != nil {
			expected = *call.ExpectedContractVersion
		}
		return &validation.Rejection{
			Code:    validation.CodeStaleContractVersion,
			Message: "The work item changed after this operation was prepared.",
			Details: map[string]any{"expected_contract_version": expected, "current_contract_version": item.ContractVersion},
			Retry:   validation.RetryRefresh,
		}
	}
	if item.State != validation.StateBlocked {
		return &validation.Rejection{
			Code:    validation.CodeInvalidTransition,
			Message: "Blocked-work resolution requires a blocked work item.",
			Details: map[string]any{"operation": "blocked-work.submit-resolution", "current_state": item.State, "allowed_operations": []string{"blocked-work.submit-resolution", "blocked-work.acknowledge"}},
			Retry:   validation.RetryRefresh,
		}
	}
	return nil
}
