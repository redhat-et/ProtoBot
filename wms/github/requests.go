package github

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

type createRequestPayload struct {
	Intent             string   `json:"intent"`
	Rationale          string   `json:"rationale"`
	AffectedInterfaces []string `json:"affected_interfaces,omitempty"`
	AffectedScopes     []string `json:"affected_scopes,omitempty"`
}

type refineRequestPayload struct {
	Intent                   string                        `json:"intent,omitempty"`
	Rationale                string                        `json:"rationale,omitempty"`
	Owner                    string                        `json:"owner,omitempty"`
	AffectedInterfaces       []string                      `json:"affected_interfaces,omitempty"`
	AffectedScopes           []string                      `json:"affected_scopes,omitempty"`
	Relationships            []adapter.RequestRelationship `json:"relationships,omitempty"`
	Classification           string                        `json:"classification"`
	RefinementState          string                        `json:"refinement_state"`
	HumanApprovalID          string                        `json:"human_approval_id,omitempty"`
	ApprovalRefinementDigest string                        `json:"approval_refinement_digest,omitempty"`
}

type priorityPayload struct {
	BusinessPriority string `json:"business_priority"`
}

type changeSetLinkPayload struct {
	ChangeSetID    string `json:"change_set_id"`
	TargetRevision string `json:"target_revision"`
}

type workItemLinkPayload struct {
	BuildWorkItemID string `json:"build_work_item_id"`
}

type requestQueryPayload struct {
	RefinementState  string `json:"refinement_state,omitempty"`
	Owner            string `json:"owner,omitempty"`
	BusinessPriority string `json:"business_priority,omitempty"`
	Interface        string `json:"interface,omitempty"`
	Scope            string `json:"scope,omitempty"`
	Relationship     string `json:"relationship,omitempty"`
}

// lastMutationRecord durably binds one applied non-create request mutation
// to the revision it produced, so a lost-response retry under the same
// idempotency key can reconcile to the original applied result instead of a
// spurious STALE_REQUEST_REVISION. It also carries the result fields each
// mutation reports, so the reconciled result matches what the original call
// would have returned.
type lastMutationRecord struct {
	Operation               string            `json:"operation"`
	IdempotencyKey          string            `json:"idempotency_key"`
	Fingerprint             string            `json:"fingerprint"`
	ApprovalStatus          string            `json:"approval_status,omitempty"`
	Link                    map[string]string `json:"link,omitempty"`
	LinkedChangeSetPriority string            `json:"linked_change_set_priority,omitempty"`
	LinkedWorkItemPriority  string            `json:"linked_work_item_priority,omitempty"`
	AuditEvent              string            `json:"audit_event,omitempty"`
}

// requestIssueUpdateOptions carries the durable bookkeeping an
// updateRequestIssue call must persist in the same write as the request
// revision it applies.
type requestIssueUpdateOptions struct {
	mutation           *lastMutationRecord
	consumedApprovalID string
}

func (a *Adapter) executeRequestLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	operation := validation.Operation(call.Operation)
	mutating := isRequestMutation(operation)
	if mutating && call.IdempotencyKey == "" {
		return rejectedResult(call.Operation, invalidRequest("idempotency_key", "request mutations require an idempotency key"))
	}
	fingerprint := requestFingerprint(call, authorization)
	if mutating {
		if replay, conflict := a.checkIdempotencyLocked(call.IdempotencyKey, fingerprint); replay != nil {
			return *replay
		} else if conflict != nil {
			return rejectedResult(call.Operation, conflict)
		}
	}

	var result adapter.Result
	switch operation {
	case validation.OperationRequestCreate:
		result = a.createRequestLocked(call, authorization)
	case validation.OperationRequestRefine:
		result = a.refineRequestLocked(call, authorization)
	case validation.OperationRequestUpdatePriority:
		result = a.updatePriorityLocked(call, authorization)
	case validation.OperationRequestLinkChangeSet:
		result = a.linkChangeSetLocked(call, authorization)
	case validation.OperationRequestLinkBuildWorkItem:
		result = a.linkWorkItemLocked(call, authorization)
	case validation.OperationRequestGet:
		result = a.getRequestLocked(call)
	case validation.OperationRequestQuery:
		result = a.queryRequestsLocked(call)
	case validation.OperationWorkItemGet:
		result = a.getWorkItemLocked(call)
	case validation.OperationWorkItemQuery:
		result = a.queryWorkItemsLocked(call)
	case validation.OperationBlockedWorkQuery:
		result = a.queryBlockedWorkLocked(call)
	default:
		result = rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
	}
	if mutating && shouldRememberIdempotency(result) {
		a.rememberIdempotencyLocked(call.IdempotencyKey, fingerprint, result)
	}
	return result
}

// shouldRememberIdempotency reports whether a mutating request result may be
// frozen under its idempotency key. UNKNOWN_MUTATION and WMS_UNAVAILABLE must
// not be recorded so a lost-response retry can reconcile and reuse the same
// key. Applied results and determinate rejections remain replayable.
func shouldRememberIdempotency(result adapter.Result) bool {
	if result.Mutation == adapter.MutationUnknown {
		return false
	}
	if result.Error != nil && result.Error.Code == adapter.CodeWMSUnavailable {
		return false
	}
	return true
}

func isRequestMutation(operation validation.Operation) bool {
	switch operation {
	case validation.OperationRequestCreate, validation.OperationRequestRefine,
		validation.OperationRequestUpdatePriority, validation.OperationRequestLinkChangeSet,
		validation.OperationRequestLinkBuildWorkItem:
		return true
	default:
		return false
	}
}

func (a *Adapter) createRequestLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	var payload createRequestPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if isBlank(payload.Intent) || isBlank(payload.Rationale) {
		return rejectedResult(call.Operation, invalidRequest("intent/rationale", "intent and rationale are required"))
	}
	semanticKey := requestSemanticKey(payload)
	docs, err := a.hydrateRequestsLocked()
	if err != nil {
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	if result, handled := a.replayDurableRequestCreate(call, authorization, docs); handled {
		return result
	}
	if result, handled := a.reconcilePendingRequestCreate(call, semanticKey); handled {
		return result
	}
	if existing, exists := a.idx.semanticRequests[semanticKey]; exists {
		return rejectedResult(call.Operation, wmsRejection(
			adapter.CodeDuplicateRequest,
			"A semantically equivalent request already exists.",
			map[string]any{"request_id": existing},
			validation.RetryQuery,
		))
	}
	return a.persistNewRequestLocked(call, authorization, payload, semanticKey)
}

// reconcilePendingRequestCreate looks up a request_id reserved after a prior
// UNKNOWN_MUTATION for this idempotency key before creating again. Only a
// definitive ErrNotFound establishes that the first issue is actually
// absent; any other scan error (transient or not) is inconclusive and must
// not be treated as a clean no-match, or the caller could create a second
// issue under the same reserved request ID.
func (a *Adapter) reconcilePendingRequestCreate(call adapter.CallRequest, semanticKey string) (adapter.Result, bool) {
	pendingID, ok := a.idx.pendingRequestCreate[call.IdempotencyKey]
	if !ok {
		return adapter.Result{}, false
	}
	existing, number, err := a.findRequestByID(pendingID)
	if err == nil {
		delete(a.idx.pendingRequestCreate, call.IdempotencyKey)
		a.bindRequestLocked(existing, number, semanticKey)
		return requestCreateAppliedResult(call.Operation, existing), true
	}
	if errors.Is(err, ErrNotFound) {
		return adapter.Result{}, false
	}
	return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh)), true
}

func (a *Adapter) persistNewRequestLocked(
	call adapter.CallRequest,
	authorization validation.AuthorizationContext,
	payload createRequestPayload,
	semanticKey string,
) adapter.Result {
	requestID := a.idx.pendingRequestCreate[call.IdempotencyKey]
	if requestID == "" {
		requestID = a.nextRequestIDLocked()
	}
	request := adapter.RequestRecord{
		ID:                 requestID,
		Intent:             strings.TrimSpace(payload.Intent),
		Rationale:          strings.TrimSpace(payload.Rationale),
		CreatedBy:          authorization.Subject,
		AffectedInterfaces: sortedUnique(payload.AffectedInterfaces),
		AffectedScopes:     sortedUnique(payload.AffectedScopes),
		RefinementState:    "unrefined",
		Revision:           1,
	}
	issue, err := a.persistRequestIssue(request, call.IdempotencyKey, requestFingerprint(call, authorization))
	if err != nil {
		if !errorsIsTransient(err) {
			return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
		}
		a.idx.pendingRequestCreate[call.IdempotencyKey] = request.ID
		if existing, number, findErr := a.findRequestByID(request.ID); findErr == nil {
			delete(a.idx.pendingRequestCreate, call.IdempotencyKey)
			a.bindRequestLocked(existing, number, semanticKey)
			return requestCreateAppliedResult(call.Operation, existing)
		}
		return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
	}
	delete(a.idx.pendingRequestCreate, call.IdempotencyKey)
	a.bindRequestLocked(request, issue.Number, semanticKey)
	return requestCreateAppliedResult(call.Operation, request)
}

func (a *Adapter) refineRequestLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, doc, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("request", err))
	}
	if result, handled := resolveRequestRevision(call, authorization, request, doc); handled {
		return result
	}
	var payload refineRequestPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if payload.Classification != "" && !validClassification(payload.Classification) {
		return rejectedResult(call.Operation, invalidRequest("classification", "the value is outside the request vocabulary"))
	}
	if !validRefinementState(payload.RefinementState) {
		return rejectedResult(call.Operation, invalidRequest("refinement_state", "the value is outside the request vocabulary"))
	}
	if (payload.Intent != "" && isBlank(payload.Intent)) || (payload.Rationale != "" && isBlank(payload.Rationale)) {
		return rejectedResult(call.Operation, invalidRequest("intent/rationale", "supplied intent and rationale must not be blank"))
	}
	priorSemanticKey := requestSemanticKeyFor(request.Intent, request.AffectedInterfaces, request.AffectedScopes)
	approvalID := payload.HumanApprovalID
	if call.HumanApprovalID != "" {
		approvalID = call.HumanApprovalID
	}
	if slices.Contains(doc.ConsumedApprovalIDs, approvalID) {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, "", authorization.PolicyVersion))
	}
	approval, exists := a.idx.approvals[approvalID]
	if !exists {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, "", authorization.PolicyVersion))
	}
	requirement := validation.ApprovalRequirement{
		Action:             validation.OperationRequestRefine,
		DelegatedPrincipal: authorization.Subject,
		ProjectID:          a.projectID,
		RequestID:          request.ID,
		Digest:             payload.ApprovalRefinementDigest,
		PolicyVersion:      authorization.PolicyVersion,
	}
	if rejection := validation.ValidateApproval(approval, requirement, a.now()); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}

	refined := cloneRequest(request)
	if payload.Intent != "" {
		refined.Intent = strings.TrimSpace(payload.Intent)
	}
	if payload.Rationale != "" {
		refined.Rationale = strings.TrimSpace(payload.Rationale)
	}
	if payload.Owner != "" {
		refined.Owner = payload.Owner
	}
	if payload.AffectedInterfaces != nil {
		refined.AffectedInterfaces = sortedUnique(payload.AffectedInterfaces)
	}
	if payload.AffectedScopes != nil {
		refined.AffectedScopes = sortedUnique(payload.AffectedScopes)
	}
	if payload.Relationships != nil {
		if rejection := a.validateRelationshipsLocked(payload.Relationships, refined.ID); rejection != nil {
			return rejectedResult(call.Operation, rejection)
		}
		refined.Relationships = append([]adapter.RequestRelationship(nil), payload.Relationships...)
	}
	if payload.Classification != "" {
		refined.Classification = payload.Classification
	}
	refined.RefinementState = payload.RefinementState

	nextSemanticKey := requestSemanticKeyFor(refined.Intent, refined.AffectedInterfaces, refined.AffectedScopes)
	if duplicateID, exists := a.idx.semanticRequests[nextSemanticKey]; exists && duplicateID != refined.ID {
		return rejectedResult(call.Operation, wmsRejection(
			adapter.CodeDuplicateRequest,
			"A semantically equivalent request already exists.",
			map[string]any{"request_id": duplicateID},
			validation.RetryQuery,
		))
	}
	relationships := make([]validation.RefinementRelationship, 0, len(refined.Relationships))
	for _, relationship := range refined.Relationships {
		relationships = append(relationships, validation.RefinementRelationship{
			Type:   relationship.Type,
			Target: relationship.Target,
		})
	}
	digest := validation.RefinementDigest(validation.RefinementContent{
		Intent:             refined.Intent,
		Rationale:          refined.Rationale,
		Owner:              refined.Owner,
		AffectedInterfaces: refined.AffectedInterfaces,
		AffectedScopes:     refined.AffectedScopes,
		Relationships:      relationships,
		Classification:     refined.Classification,
		RefinementState:    refined.RefinementState,
	})
	if digest != approval.Digest {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, "", authorization.PolicyVersion))
	}

	refined.Revision++
	mutation := &lastMutationRecord{
		Operation:      call.Operation,
		IdempotencyKey: call.IdempotencyKey,
		Fingerprint:    requestFingerprint(call, authorization),
		ApprovalStatus: string(validation.ApprovalStatusConsumed),
	}
	if err := a.updateRequestIssue(number, refined, requestIssueUpdateOptions{mutation: mutation, consumedApprovalID: approvalID}); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	if priorSemanticKey != nextSemanticKey {
		delete(a.idx.semanticRequests, priorSemanticKey)
	}
	a.idx.semanticRequests[nextSemanticKey] = refined.ID
	approval.Status = validation.ApprovalStatusConsumed
	a.idx.approvals[approvalID] = approval

	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(refined)
	result.RequestID = refined.ID
	result.RequestRevision = refined.Revision
	result.ApprovalStatus = validation.ApprovalStatusConsumed
	return result
}

func (a *Adapter) updatePriorityLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, doc, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("request", err))
	}
	if result, handled := resolveRequestRevision(call, authorization, request, doc); handled {
		return result
	}
	var payload priorityPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if !validPriority(payload.BusinessPriority) {
		return rejectedResult(call.Operation, invalidRequest("business_priority", "priority must be low, normal, high, or urgent"))
	}
	var changeSetPriority, workItemPriority string
	if request.ChangeSetID != "" {
		changeSet, exists := a.idx.changeSets[request.ChangeSetID]
		if !exists {
			return rejectedResult(call.Operation, validationNotFound("change-set"))
		}
		changeSet.BusinessPriority = payload.BusinessPriority
		a.idx.changeSets[request.ChangeSetID] = changeSet
		changeSetPriority = payload.BusinessPriority
	}
	if request.BuildWorkItemID != "" {
		item, issueNumber, loadErr := a.loadWorkItem(request.BuildWorkItemID)
		if loadErr != nil {
			return rejectedResult(call.Operation, backendLoadRejection("work-item", loadErr))
		}
		item.Priority = payload.BusinessPriority
		if err := a.updateWorkItemIssue(issueNumber, item, request.ID, request.ChangeSetID, payload.BusinessPriority); err != nil {
			if errorsIsTransient(err) {
				return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
			}
			return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
		}
		workItemPriority = payload.BusinessPriority
	}
	request.BusinessPriority = payload.BusinessPriority
	request.Revision++
	mutation := &lastMutationRecord{
		Operation:               call.Operation,
		IdempotencyKey:          call.IdempotencyKey,
		Fingerprint:             requestFingerprint(call, authorization),
		LinkedChangeSetPriority: changeSetPriority,
		LinkedWorkItemPriority:  workItemPriority,
		AuditEvent:              "priority-updated",
	}
	if err := a.updateRequestIssue(number, request, requestIssueUpdateOptions{mutation: mutation}); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.LinkedChangeSetPriority = changeSetPriority
	result.LinkedWorkItemPriority = workItemPriority
	result.AuditEvent = "priority-updated"
	return result
}

func (a *Adapter) linkChangeSetLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, doc, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("request", err))
	}
	if result, handled := resolveRequestRevision(call, authorization, request, doc); handled {
		return result
	}
	var payload changeSetLinkPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	changeSet, exists := a.idx.changeSets[payload.ChangeSetID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("change-set"))
	}
	if payload.TargetRevision != "" && payload.TargetRevision != changeSet.Revision {
		return rejectedResult(call.Operation, invalidRequest("target_revision", "does not match the change-set revision"))
	}
	request.ChangeSetID = changeSet.ID
	if changeSet.BusinessPriority != "" {
		request.BusinessPriority = changeSet.BusinessPriority
	}
	request.Revision++
	mutation := &lastMutationRecord{
		Operation:               call.Operation,
		IdempotencyKey:          call.IdempotencyKey,
		Fingerprint:             requestFingerprint(call, authorization),
		Link:                    map[string]string{"change_set_id": changeSet.ID},
		LinkedChangeSetPriority: changeSet.BusinessPriority,
	}
	if err := a.updateRequestIssue(number, request, requestIssueUpdateOptions{mutation: mutation}); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.Link = map[string]string{"change_set_id": changeSet.ID}
	result.LinkedChangeSetPriority = changeSet.BusinessPriority
	return result
}

func (a *Adapter) linkWorkItemLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, doc, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("request", err))
	}
	if result, handled := resolveRequestRevision(call, authorization, request, doc); handled {
		return result
	}
	var payload workItemLinkPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	item, issueNumber, err := a.loadWorkItem(payload.BuildWorkItemID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("work-item", err))
	}
	request.BuildWorkItemID = item.ID
	if request.BusinessPriority != "" {
		item.Priority = request.BusinessPriority
		if err := a.updateWorkItemIssue(issueNumber, item, request.ID, request.ChangeSetID, request.BusinessPriority); err != nil {
			if errorsIsTransient(err) {
				return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
			}
			return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
		}
	} else if err := a.updateWorkItemIssue(issueNumber, item, request.ID, request.ChangeSetID, item.Priority); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	request.Revision++
	mutation := &lastMutationRecord{
		Operation:              call.Operation,
		IdempotencyKey:         call.IdempotencyKey,
		Fingerprint:            requestFingerprint(call, authorization),
		Link:                   map[string]string{"build_work_item_id": item.ID},
		LinkedWorkItemPriority: item.Priority,
	}
	if err := a.updateRequestIssue(number, request, requestIssueUpdateOptions{mutation: mutation}); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.Link = map[string]string{"build_work_item_id": item.ID}
	result.LinkedWorkItemPriority = item.Priority
	return result
}

func (a *Adapter) getRequestLocked(call adapter.CallRequest) adapter.Result {
	request, _, _, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("request", err))
	}
	result := newResult(call.Operation)
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	return result
}

func (a *Adapter) queryRequestsLocked(call adapter.CallRequest) adapter.Result {
	var query requestQueryPayload
	if err := decodePayload(call.Payload, &query); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	requests := make([]adapter.RequestRecord, 0)
	for id := range a.idx.requestIssue {
		request, _, _, err := a.loadRequest(id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
		}
		if query.RefinementState != "" && request.RefinementState != query.RefinementState {
			continue
		}
		if query.Owner != "" && request.Owner != query.Owner {
			continue
		}
		if query.BusinessPriority != "" && request.BusinessPriority != query.BusinessPriority {
			continue
		}
		if query.Interface != "" && !slices.Contains(request.AffectedInterfaces, query.Interface) {
			continue
		}
		if query.Scope != "" && !slices.Contains(request.AffectedScopes, query.Scope) {
			continue
		}
		if query.Relationship != "" && !hasRelationship(request.Relationships, query.Relationship) {
			continue
		}
		requests = append(requests, cloneRequest(request))
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].ID < requests[j].ID })
	result := newResult(call.Operation)
	result.Requests = requests
	result.Resource = requests
	return result
}

func (a *Adapter) persistRequestIssue(request adapter.RequestRecord, idempotencyKey, fingerprint string) (Issue, error) {
	body, err := encodeBody("ProtoBot request backlog record.", storedDocument{
		Kind:                 kindRequest,
		ProjectID:            a.projectID,
		Request:              &request,
		CreateIdempotencyKey: idempotencyKey,
		CreateFingerprint:    fingerprint,
	})
	if err != nil {
		return Issue{}, err
	}
	return a.client.CreateIssue(a.ctx, CreateIssueInput{
		Title:  requestTitle(request),
		Body:   body,
		Labels: []string{LabelRequest},
	})
}

// updateRequestIssue persists request to its GitHub issue, along with any
// durable mutation bookkeeping in opts, in the single body write that
// updates the issue. Writing the request revision, the consumed-approval
// record, and the last-mutation binding together in one GitHub API call is
// what makes approval consumption and mutation reconciliation atomic with
// the revision bump.
func (a *Adapter) updateRequestIssue(number int, request adapter.RequestRecord, opts requestIssueUpdateOptions) error {
	current, err := a.client.GetIssue(a.ctx, number)
	if err != nil {
		return err
	}
	prior, err := decodeBody(current.Body)
	if err != nil {
		return err
	}
	consumedApprovalIDs := prior.ConsumedApprovalIDs
	if opts.consumedApprovalID != "" && !slices.Contains(consumedApprovalIDs, opts.consumedApprovalID) {
		consumedApprovalIDs = append(append([]string(nil), consumedApprovalIDs...), opts.consumedApprovalID)
	}
	lastMutation := prior.LastMutation
	if opts.mutation != nil {
		lastMutation = opts.mutation
	}
	body, err := encodeBody("ProtoBot request backlog record.", storedDocument{
		Kind:                 kindRequest,
		ProjectID:            a.projectID,
		Request:              &request,
		CreateIdempotencyKey: prior.CreateIdempotencyKey,
		CreateFingerprint:    prior.CreateFingerprint,
		ConsumedApprovalIDs:  consumedApprovalIDs,
		LastMutation:         lastMutation,
	})
	if err != nil {
		return err
	}
	title := requestTitle(request)
	_, err = a.client.UpdateIssue(a.ctx, number, UpdateIssueInput{
		Title:  &title,
		Body:   &body,
		Labels: []string{LabelRequest},
	})
	return err
}

func (a *Adapter) loadRequest(id string) (adapter.RequestRecord, storedDocument, int, error) {
	number, ok := a.idx.requestIssue[id]
	if !ok {
		return adapter.RequestRecord{}, storedDocument{}, 0, ErrNotFound
	}
	issue, err := a.client.GetIssue(a.ctx, number)
	if err != nil {
		return adapter.RequestRecord{}, storedDocument{}, 0, err
	}
	doc, err := decodeBody(issue.Body)
	if err != nil || doc.Kind != kindRequest || doc.Request == nil {
		return adapter.RequestRecord{}, storedDocument{}, 0, fmt.Errorf("invalid request document")
	}
	if doc.ProjectID != a.projectID || doc.Request.ID != id {
		return adapter.RequestRecord{}, storedDocument{}, 0, ErrNotFound
	}
	return cloneRequest(*doc.Request), doc, number, nil
}

// resolveRequestRevision checks the expected request revision against the
// loaded request's current revision. A mismatch is ordinarily a genuine
// stale write, but when the call's idempotency key and fingerprint match
// this request's durably recorded LastMutation, the mismatch is instead a
// lost-response retry of a mutation that already applied: the retry
// reconciles to that original applied result rather than freezing a
// spurious STALE_REQUEST_REVISION under the key (handled is true in both
// cases; the caller returns the result immediately).
func resolveRequestRevision(
	call adapter.CallRequest,
	authorization validation.AuthorizationContext,
	request adapter.RequestRecord,
	doc storedDocument,
) (adapter.Result, bool) {
	if call.ExpectedRequestRevision == nil {
		return rejectedResult(call.Operation, invalidRequest("expected_request_revision", "request mutations require an expected revision")), true
	}
	if *call.ExpectedRequestRevision == request.Revision {
		return adapter.Result{}, false
	}
	if reconciled, ok := reconcileStaleRequestMutation(call, authorization, request, doc); ok {
		return reconciled, true
	}
	return rejectedResult(call.Operation, wmsRejection(
		adapter.CodeStaleRequestRevision,
		"The request revision does not match the expected revision.",
		map[string]any{
			"expected_request_revision": *call.ExpectedRequestRevision,
			"current_request_revision":  request.Revision,
		},
		validation.RetryRefresh,
	)), true
}

// reconcileStaleRequestMutation rebuilds the applied result a mutation must
// have returned the first time, from the durable LastMutation binding on
// doc, when call is an exact idempotency-key-and-fingerprint retry of it.
func reconcileStaleRequestMutation(
	call adapter.CallRequest,
	authorization validation.AuthorizationContext,
	request adapter.RequestRecord,
	doc storedDocument,
) (adapter.Result, bool) {
	mutation := doc.LastMutation
	if mutation == nil || call.IdempotencyKey == "" ||
		mutation.IdempotencyKey != call.IdempotencyKey || mutation.Operation != call.Operation {
		return adapter.Result{}, false
	}
	if mutation.Fingerprint != requestFingerprint(call, authorization) {
		return adapter.Result{}, false
	}
	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	if mutation.ApprovalStatus != "" {
		result.ApprovalStatus = validation.ApprovalStatus(mutation.ApprovalStatus)
	}
	if mutation.Link != nil {
		result.Link = map[string]string{}
		for key, value := range mutation.Link {
			result.Link[key] = value
		}
	}
	result.LinkedChangeSetPriority = mutation.LinkedChangeSetPriority
	result.LinkedWorkItemPriority = mutation.LinkedWorkItemPriority
	result.AuditEvent = mutation.AuditEvent
	return markReplayed(result), true
}

func requestFingerprint(call adapter.CallRequest, authorization validation.AuthorizationContext) string {
	payload := struct {
		Operation               string
		Actor                   string
		Role                    string
		RequestID               string
		WorkItemID              string
		ExpectedRequestRevision *uint64
		ExpectedState           validation.State
		ExpectedContractVersion *uint64
		HumanApprovalID         string
		MaterializationKey      string
		Payload                 json.RawMessage
	}{
		Operation:               call.Operation,
		Actor:                   authorization.Subject,
		Role:                    string(authorization.Role),
		RequestID:               call.RequestID,
		WorkItemID:              call.WorkItemID,
		ExpectedRequestRevision: call.ExpectedRequestRevision,
		ExpectedState:           call.ExpectedState,
		ExpectedContractVersion: call.ExpectedContractVersion,
		HumanApprovalID:         call.HumanApprovalID,
		MaterializationKey:      call.MaterializationKey,
		Payload:                 call.Payload,
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func requestSemanticKey(payload createRequestPayload) string {
	return requestSemanticKeyFor(payload.Intent, payload.AffectedInterfaces, payload.AffectedScopes)
}

// requestSemanticKeyFor hashes a canonical structured value (not a
// delimiter-joined string) so that distinct interface/scope slices can never
// collapse onto the same key through an ambiguous separator, and so a
// value containing the delimiter cannot shift field boundaries.
func requestSemanticKeyFor(intent string, interfaces, scopes []string) string {
	value := struct {
		Intent     string
		Interfaces []string
		Scopes     []string
	}{
		strings.ToLower(strings.TrimSpace(intent)),
		sortedUnique(interfaces),
		sortedUnique(scopes),
	}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func sortedUnique(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// validClassification and validRefinementState use exactly the request
// vocabulary defined by the contract
// (docs/architecture/drafting-table-wms.md:171-172), matching the memory
// adapter (wms/memory/requests.go) so both backends accept and reject the
// same values.
func validClassification(value string) bool {
	switch value {
	case "undefined", "changes", "contradicts":
		return true
	default:
		return false
	}
}

func validRefinementState(value string) bool {
	switch value {
	case "unrefined", "refining", "ready-for-dimensioning", "closed":
		return true
	default:
		return false
	}
}

func validPriority(value string) bool {
	switch value {
	case "low", "normal", "high", "urgent":
		return true
	default:
		return false
	}
}

// validRelationshipType restricts relationships to the ADR-0002 relationship
// vocabulary, matching the memory adapter (wms/memory/requests.go).
func validRelationshipType(value string) bool {
	switch value {
	case "depends-on", "conflicts-with", "supersedes", "related-to":
		return true
	default:
		return false
	}
}

// validateRelationshipsLocked checks relationship type, target existence,
// self-reference, and duplicates, mirroring the memory adapter's refine
// contract (wms/memory/requests.go) so the GitHub backend rejects the same
// invalid relationships (docs/architecture/drafting-table-wms.md:232).
func (a *Adapter) validateRelationshipsLocked(relationships []adapter.RequestRelationship, requestID string) *validation.Rejection {
	seen := make(map[string]struct{}, len(relationships))
	for _, relationship := range relationships {
		if relationship.Target == "" || relationship.Target == requestID || !validRelationshipType(relationship.Type) {
			return invalidRequest("relationships", "relationship type or target is invalid")
		}
		if _, _, _, err := a.loadRequest(relationship.Target); err != nil {
			return backendLoadRejection("request", err)
		}
		key := relationship.Type + "\x00" + relationship.Target
		if _, exists := seen[key]; exists {
			return invalidRequest("relationships", "duplicate relationship")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func hasRelationship(relationships []adapter.RequestRelationship, relationshipType string) bool {
	return slices.ContainsFunc(relationships, func(relationship adapter.RequestRelationship) bool {
		return relationship.Type == relationshipType
	})
}

func errorsIsTransient(err error) bool {
	return errors.Is(err, ErrTransient)
}
