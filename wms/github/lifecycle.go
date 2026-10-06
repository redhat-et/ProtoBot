package github

import (
	"errors"
	"time"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

func (a *Adapter) executeLifecycleLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, rejection := a.normalizeLifecycleRequest(call, authorization)
	if rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	evaluation := validation.EvaluationContext{
		Authority:        validation.AuthorityAuthoritative,
		EvaluationTime:   a.now(),
		LeaseDuration:    a.leaseDuration,
		NextFencingToken: a.nextFenceTokenLocked(),
		Approvals:        a.approvalSnapshotLocked(),
	}

	var current *validation.WorkItem
	item, existsErr := a.tryLoadWorkItem(request.WorkItemID)
	if existsErr != nil && !errors.Is(existsErr, ErrNotFound) {
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, existsErr.Error(), map[string]any{}, validation.RetryRefresh))
	}
	exists := existsErr == nil
	if request.Operation == validation.OperationMaterialize {
		if exists {
			copy := cloneWorkItem(item)
			current = &copy
		}
		if request.Payload.WorkItem != nil {
			dependencies := a.liveDependenciesLocked(*request.Payload.WorkItem)
			evaluation.RefreshDependencies = &dependencies
		}
	} else {
		if !exists {
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
				if result, handled := a.replayLifecycleRequest(call, request, fingerprint); handled {
					return result
				}
				result := resultFromDecision(call.Operation, validation.Evaluate(request, nil, evaluation))
				if shouldRememberIdempotency(result) {
					a.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, result)
				}
				return result
			}
			return resultFromDecision(call.Operation, validation.Evaluate(request, nil, evaluation))
		}
		copy := cloneWorkItem(item)
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
	if result, handled := a.replayLifecycleRequest(call, request, fingerprint); handled {
		return result
	}
	if current != nil && request.Operation == validation.OperationMaterialize {
		dependencies := a.liveDependenciesLocked(*current)
		evaluation.RefreshDependencies = &dependencies
	}
	return a.applyLifecycleRequest(call.Operation, request, current, authorization, fingerprint, evaluation)
}

func (a *Adapter) normalizeLifecycleRequest(call adapter.CallRequest, authorization validation.AuthorizationContext) (validation.Request, *validation.Rejection) {
	var payload validation.Payload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return validation.Request{}, invalidRequest("payload", err.Error())
	}
	operation := validation.Operation(call.Operation)
	workItemID := call.WorkItemID
	if operation == validation.OperationMaterialize && workItemID == "" && payload.WorkItem != nil {
		workItemID = payload.WorkItem.ID
	}
	if call.HumanApprovalID != "" {
		payload.HumanApprovalID = call.HumanApprovalID
	}
	return validation.Request{
		Operation:               operation,
		ProjectID:               a.projectID,
		WorkItemID:              workItemID,
		MaterializationKey:      requestMaterializationKey(call, payload),
		IdempotencyKey:          call.IdempotencyKey,
		ExpectedState:           call.ExpectedState,
		ExpectedContractVersion: call.ExpectedContractVersion,
		FencingToken:            call.FencingToken,
		References:              lifecycleReferences(operation, payload),
		Payload:                 payload,
		Authorization:           authorization,
	}, nil
}

func lifecycleReferences(operation validation.Operation, payload validation.Payload) []string {
	var envelope *validation.MergeEnvelope
	if operation == validation.OperationMaterialize && payload.WorkItem != nil {
		envelope = payload.WorkItem.ExpectedMerge
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

func requestMaterializationKey(call adapter.CallRequest, payload validation.Payload) string {
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

func (a *Adapter) replayLifecycleRequest(call adapter.CallRequest, request validation.Request, fingerprint string) (adapter.Result, bool) {
	if replay, conflict := a.checkIdempotencyLocked(request.IdempotencyKey, fingerprint); replay != nil {
		return *replay, true
	} else if conflict != nil {
		decision := validation.RejectionDecision(request, validation.AuthorityAuthoritative, conflict)
		return resultFromDecision(call.Operation, decision), true
	}
	if request.Operation == validation.OperationMaterialize {
		if replay, rejection := a.checkMaterializationLocked(request); replay != nil {
			a.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, *replay)
			return *replay, true
		} else if rejection != nil {
			decision := validation.RejectionDecision(request, validation.AuthorityAuthoritative, rejection)
			result := resultFromDecision(call.Operation, decision)
			a.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, result)
			return result, true
		}
	}
	return adapter.Result{}, false
}

func (a *Adapter) checkMaterializationLocked(request validation.Request) (*adapter.Result, *validation.Rejection) {
	if request.Payload.WorkItem == nil {
		return nil, nil
	}
	entry, rejection := a.loadMaterializationEntryLocked(request.MaterializationKey)
	if rejection != nil {
		return nil, rejection
	}
	if entry == nil {
		return nil, nil
	}
	if rejection := validation.MaterializationTargetRejection(request, request.Payload.WorkItem); rejection != nil {
		return nil, rejection
	}
	source := validation.CanonicalMaterializationSource(*request.Payload.WorkItem, request.Payload.ChangeType)
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
	result := markReplayed(cloneResult(entry.result))
	return &result, nil
}

// loadMaterializationEntryLocked returns the in-process binding, or rebinds from
// a durable GitHub scan when the process-local index has no entry.
func (a *Adapter) loadMaterializationEntryLocked(key string) (*materializationEntry, *validation.Rejection) {
	if entry, ok := a.idx.materializationKey[key]; ok {
		copied := entry
		return &copied, nil
	}
	item, doc, number, err := a.findWorkItemByMaterializationKey(key)
	if err != nil {
		if errorsIsTransient(err) {
			return nil, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh)
		}
		return nil, nil
	}
	replay := materializationReplayResult(item)
	a.bindWorkItemLocked(item, doc, number, replay)
	entry := a.idx.materializationKey[key]
	return &entry, nil
}

func (a *Adapter) applyLifecycleRequest(
	operation string,
	request validation.Request,
	current *validation.WorkItem,
	authorization validation.AuthorizationContext,
	fingerprint string,
	evaluation validation.EvaluationContext,
) adapter.Result {
	decision := validation.Evaluate(request, current, evaluation)
	result := resultFromDecision(operation, decision)
	switch decision.Outcome {
	case validation.OutcomeAllowed:
		switch request.Operation {
		case validation.OperationMaterialize:
			item := cloneWorkItem(validation.CanonicalMaterializationSource(*request.Payload.WorkItem, request.Payload.ChangeType))
			item.State = decision.After.State
			item.ContractVersion = decision.After.ContractVersion
			item.MaterializationKey = request.MaterializationKey
			item.Owner = ""
			item.Lease = nil
			if evaluation.RefreshDependencies != nil {
				item.Dependencies = append([]validation.Dependency(nil), (*evaluation.RefreshDependencies)...)
			}
			item.Reconciliation = validation.ReconciliationEvidence{}
			item.InspectionRunSealed = false
			item.FindingsTerminal = false
			item.FinalTestsPassed = false
			if item.State == validation.StateBlocked {
				item.BlockReason = validation.ReadinessFailure(item.Readiness)
			}
			sourceFingerprint := ""
			if decision.MaterializationReservation != nil {
				sourceFingerprint = decision.MaterializationReservation.SourceFingerprint
			}
			issue, err := a.persistWorkItemIssue(item, sourceFingerprint)
			if err != nil {
				if errorsIsTransient(err) {
					if !a.rebindMaterializationAfterUnknown(request.MaterializationKey, sourceFingerprint, &result) {
						return unknownMutationResult(operation, "GitHub write result could not be established.")
					}
					break
				}
				return rejectedResult(operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
			}
			result.WorkItemID = item.ID
			result.WorkItemState = item.State
			result.ContractVersion = item.ContractVersion
			result.Resource = cloneWorkItem(item)
			a.bindWorkItemLocked(item, storedDocument{
				Kind:               kindWorkItem,
				ProjectID:          a.projectID,
				WorkItem:           &item,
				MaterializationKey: request.MaterializationKey,
				SourceFingerprint:  sourceFingerprint,
			}, issue.Number, result)
		case validation.OperationClaim:
			updated := cloneWorkItem(*current)
			// CAS: expected version already enforced by Evaluate; coordinator lock
			// serializes concurrent claims against the same GitHub-backed item.
			updated.State = decision.After.State
			updated.ContractVersion = decision.After.ContractVersion
			updated.Reconciliation = validation.ReconciliationEvidence{}
			a.startLease(&updated, authorization.Subject, decision.FencingTokenIssued, evaluation.EvaluationTime)
			number := a.idx.workItemIssue[updated.ID]
			if err := a.updateWorkItemIssue(number, updated, "", "", updated.Priority); err != nil {
				if errorsIsTransient(err) {
					return unknownMutationResult(operation, "GitHub write result could not be established.")
				}
				return rejectedResult(operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
			}
			result.WorkItemID = updated.ID
			result.WorkItemState = updated.State
			result.ContractVersion = updated.ContractVersion
			result.Resource = cloneWorkItem(updated)
		}
	case validation.OutcomeOmitted:
		result.Submission = "omitted"
		if request.Payload.WorkItem != nil {
			source := validation.CanonicalMaterializationSource(*request.Payload.WorkItem, request.Payload.ChangeType)
			a.idx.materializationKey[request.MaterializationKey] = materializationEntry{
				fingerprint: validation.SourceFingerprint(source),
				result:      cloneResult(result),
			}
		}
	}
	if shouldRememberIdempotency(result) {
		a.rememberIdempotencyLocked(request.IdempotencyKey, fingerprint, result)
	}
	return result
}

func (a *Adapter) persistWorkItemIssue(item validation.WorkItem, sourceFingerprint string) (Issue, error) {
	body, err := encodeBody("ProtoBot build work-item record.", storedDocument{
		Kind:               kindWorkItem,
		ProjectID:          a.projectID,
		WorkItem:           &item,
		MaterializationKey: item.MaterializationKey,
		SourceFingerprint:  sourceFingerprint,
	})
	if err != nil {
		return Issue{}, err
	}
	return a.client.CreateIssue(a.ctx, CreateIssueInput{
		Title:  workItemTitle(item),
		Body:   body,
		Labels: []string{LabelWorkItem},
	})
}

// rebindMaterializationAfterUnknown scans for a durable issue that may have been
// created despite a transient CreateIssue failure. On match with the expected
// source fingerprint it binds indexes and fills result; returns false when no
// matching reservation is found.
func (a *Adapter) rebindMaterializationAfterUnknown(key, wantFingerprint string, result *adapter.Result) bool {
	item, doc, number, err := a.findWorkItemByMaterializationKey(key)
	if err != nil {
		return false
	}
	stored := storedSourceFingerprint(doc)
	if wantFingerprint != "" && stored != "" && wantFingerprint != stored {
		return false
	}
	result.WorkItemID = item.ID
	result.WorkItemState = item.State
	result.ContractVersion = item.ContractVersion
	result.Resource = cloneWorkItem(item)
	a.bindWorkItemLocked(item, doc, number, *result)
	return true
}

func (a *Adapter) tryLoadWorkItem(id string) (validation.WorkItem, error) {
	if id == "" {
		return validation.WorkItem{}, ErrNotFound
	}
	item, _, err := a.loadWorkItem(id)
	return item, err
}

func (a *Adapter) liveDependenciesLocked(item validation.WorkItem) []validation.Dependency {
	dependencies := make([]validation.Dependency, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		state := validation.StateInitial
		if observed, err := a.tryLoadWorkItem(dependency.ID); err == nil && observed.ProjectID == a.projectID {
			state = observed.State
		}
		dependencies = append(dependencies, validation.Dependency{ID: dependency.ID, State: state})
	}
	return dependencies
}

func (a *Adapter) approvalSnapshotLocked() map[string]validation.ApprovalRecord {
	result := make(map[string]validation.ApprovalRecord, len(a.idx.approvals))
	for id, approval := range a.idx.approvals {
		result[id] = approval
	}
	return result
}

func (a *Adapter) startLease(item *validation.WorkItem, owner, token string, now time.Time) {
	item.Owner = owner
	item.Lease = &validation.Lease{
		Owner:        owner,
		FencingToken: token,
		ExpiresAt:    now.Add(a.leaseDuration),
	}
}

// WorkItemIssueNumber returns the GitHub issue number for tests.
func (a *Adapter) WorkItemIssueNumber(id string) (int, bool) {
	var number int
	var ok bool
	a.coord.WithLock(func() {
		number, ok = a.idx.workItemIssue[id]
	})
	return number, ok
}

// RequestIssueNumber returns the GitHub issue number for tests.
func (a *Adapter) RequestIssueNumber(id string) (int, bool) {
	var number int
	var ok bool
	a.coord.WithLock(func() {
		number, ok = a.idx.requestIssue[id]
	})
	return number, ok
}

// LoadWorkItemForTest returns the durable work item for assertions.
func (a *Adapter) LoadWorkItemForTest(id string) (validation.WorkItem, bool) {
	var item validation.WorkItem
	var ok bool
	a.coord.WithLock(func() {
		loaded, err := a.tryLoadWorkItem(id)
		if err != nil {
			return
		}
		item = loaded
		ok = true
	})
	return item, ok
}
