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
	if existing, exists := a.idx.semanticRequests[semanticKey]; exists {
		return rejectedResult(call.Operation, wmsRejection(
			adapter.CodeDuplicateRequest,
			"A semantically equivalent request already exists.",
			map[string]any{"request_id": existing},
			validation.RetryQuery,
		))
	}
	request := adapter.RequestRecord{
		ID:                 a.nextRequestIDLocked(),
		Intent:             strings.TrimSpace(payload.Intent),
		Rationale:          strings.TrimSpace(payload.Rationale),
		CreatedBy:          authorization.Subject,
		AffectedInterfaces: sortedUnique(payload.AffectedInterfaces),
		AffectedScopes:     sortedUnique(payload.AffectedScopes),
		RefinementState:    "unrefined",
		Revision:           1,
	}
	issue, err := a.persistRequestIssue(request)
	if err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	a.idx.requestIssue[request.ID] = issue.Number
	a.idx.semanticRequests[semanticKey] = request.ID

	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	return result
}

func (a *Adapter) refineRequestLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
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
	priorSemanticKey := requestSemanticKeyFor(request.Intent, request.AffectedInterfaces, request.AffectedScopes)
	approvalID := payload.HumanApprovalID
	if call.HumanApprovalID != "" {
		approvalID = call.HumanApprovalID
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
	if err := a.updateRequestIssue(number, refined); err != nil {
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
	request, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
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
			return rejectedResult(call.Operation, validationNotFound("work-item"))
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
	if err := a.updateRequestIssue(number, request); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	_ = authorization
	result := newResult(call.Operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.LinkedChangeSetPriority = changeSetPriority
	result.LinkedWorkItemPriority = workItemPriority
	return result
}

func (a *Adapter) linkChangeSetLocked(call adapter.CallRequest, authorization validation.AuthorizationContext) adapter.Result {
	request, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
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
	if err := a.updateRequestIssue(number, request); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	_ = authorization
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
	request, number, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	var payload workItemLinkPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	item, issueNumber, err := a.loadWorkItem(payload.BuildWorkItemID)
	if err != nil {
		return rejectedResult(call.Operation, validationNotFound("work-item"))
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
	if err := a.updateRequestIssue(number, request); err != nil {
		if errorsIsTransient(err) {
			return unknownMutationResult(call.Operation, "GitHub write result could not be established.")
		}
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	_ = authorization
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
	request, _, err := a.loadRequest(call.RequestID)
	if err != nil {
		return rejectedResult(call.Operation, validationNotFound("request"))
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
		request, _, err := a.loadRequest(id)
		if err != nil {
			continue
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

func (a *Adapter) persistRequestIssue(request adapter.RequestRecord) (Issue, error) {
	body, err := encodeBody("ProtoBot request backlog record.", storedDocument{
		Kind:      kindRequest,
		ProjectID: a.projectID,
		Request:   &request,
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

func (a *Adapter) updateRequestIssue(number int, request adapter.RequestRecord) error {
	body, err := encodeBody("ProtoBot request backlog record.", storedDocument{
		Kind:      kindRequest,
		ProjectID: a.projectID,
		Request:   &request,
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

func (a *Adapter) loadRequest(id string) (adapter.RequestRecord, int, error) {
	number, ok := a.idx.requestIssue[id]
	if !ok {
		return adapter.RequestRecord{}, 0, ErrNotFound
	}
	issue, err := a.client.GetIssue(a.ctx, number)
	if err != nil {
		return adapter.RequestRecord{}, 0, err
	}
	doc, err := decodeBody(issue.Body)
	if err != nil || doc.Kind != kindRequest || doc.Request == nil {
		return adapter.RequestRecord{}, 0, fmt.Errorf("invalid request document")
	}
	if doc.ProjectID != a.projectID || doc.Request.ID != id {
		return adapter.RequestRecord{}, 0, ErrNotFound
	}
	return cloneRequest(*doc.Request), number, nil
}

func validateRequestRevision(call adapter.CallRequest, request adapter.RequestRecord) *validation.Rejection {
	if call.ExpectedRequestRevision == nil {
		return invalidRequest("expected_request_revision", "request mutations require an expected revision")
	}
	if *call.ExpectedRequestRevision != request.Revision {
		return wmsRejection(
			adapter.CodeStaleRequestRevision,
			"The request revision does not match the expected revision.",
			map[string]any{
				"expected_request_revision": *call.ExpectedRequestRevision,
				"current_request_revision":  request.Revision,
			},
			validation.RetryRefresh,
		)
	}
	return nil
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

func requestSemanticKeyFor(intent string, interfaces, scopes []string) string {
	return strings.ToLower(strings.TrimSpace(intent)) + "\x00" +
		strings.Join(sortedUnique(interfaces), ",") + "\x00" +
		strings.Join(sortedUnique(scopes), ",")
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

func validClassification(value string) bool {
	switch value {
	case "undefined", "changes", "contradicts", "clarification":
		return true
	default:
		return false
	}
}

func validRefinementState(value string) bool {
	switch value {
	case "unrefined", "refining", "ready", "linked":
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

func hasRelationship(relationships []adapter.RequestRelationship, relationshipType string) bool {
	return slices.ContainsFunc(relationships, func(relationship adapter.RequestRelationship) bool {
		return relationship.Type == relationshipType
	})
}

func errorsIsTransient(err error) bool {
	return errors.Is(err, ErrTransient)
}
