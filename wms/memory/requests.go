package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/redhat-et/protobot/wms/validation"
)

type createRequestPayload struct {
	Intent             string   `json:"intent"`
	Rationale          string   `json:"rationale"`
	AffectedInterfaces []string `json:"affected_interfaces,omitempty"`
	AffectedScopes     []string `json:"affected_scopes,omitempty"`
}

type refineRequestPayload struct {
	Intent                   string                `json:"intent,omitempty"`
	Rationale                string                `json:"rationale,omitempty"`
	Owner                    string                `json:"owner,omitempty"`
	AffectedInterfaces       []string              `json:"affected_interfaces,omitempty"`
	AffectedScopes           []string              `json:"affected_scopes,omitempty"`
	Relationships            []RequestRelationship `json:"relationships,omitempty"`
	Classification           string                `json:"classification"`
	RefinementState          string                `json:"refinement_state"`
	HumanApprovalID          string                `json:"human_approval_id,omitempty"`
	ApprovalRefinementDigest string                `json:"approval_refinement_digest,omitempty"`
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

type workItemQueryPayload struct {
	State      validation.State `json:"state,omitempty"`
	Owner      string           `json:"owner,omitempty"`
	Dependency string           `json:"dependency,omitempty"`
	Priority   string           `json:"priority,omitempty"`
}

func (m *Memory) executeRequestLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	mutating := isRequestMutation(call.Operation)
	if mutating && call.IdempotencyKey == "" {
		return rejectedResult(call.Operation, invalidRequest("idempotency_key", "request mutations require an idempotency key"))
	}
	fingerprint := requestFingerprint(call, authorization)
	if mutating {
		if replay, conflict := m.checkIdempotencyLocked(call.Operation, call.IdempotencyKey, fingerprint); replay != nil {
			return *replay
		} else if conflict != nil {
			return rejectedResult(call.Operation, conflict)
		}
	}

	var result Result
	switch call.Operation {
	case "request.create":
		result = m.createRequestLocked(call, authorization)
	case "request.refine":
		result = m.refineRequestLocked(call, authorization)
	case "request.update-priority":
		result = m.updatePriorityLocked(call, authorization)
	case "request.link-change-set":
		result = m.linkChangeSetLocked(call, authorization)
	case "request.link-build-work-item":
		result = m.linkWorkItemLocked(call, authorization)
	case "request.get":
		result = m.getRequestLocked(call)
	case "request.query":
		result = m.queryRequestsLocked(call)
	case "work-item.get":
		result = m.getWorkItemLocked(call)
	case "work-item.query":
		result = m.queryWorkItemsLocked(call)
	case "blocked-work.query":
		result = m.queryBlockedWorkLocked(call)
	case "blocked-work.submit-resolution":
		result = m.submitResolutionLocked(call, authorization)
	case "blocked-work.acknowledge":
		result = m.acknowledgeBlockedWorkLocked(call, authorization)
	default:
		result = rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
	}
	if mutating {
		m.rememberIdempotencyLocked(call.IdempotencyKey, fingerprint, result)
	}
	return result
}

func (m *Memory) createRequestLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	var payload createRequestPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if isBlank(payload.Intent) || isBlank(payload.Rationale) {
		return rejectedResult(call.Operation, invalidRequest("intent/rationale", "intent and rationale are required"))
	}
	semanticKey := requestSemanticKey(payload)
	if _, exists := m.semanticRequests[semanticKey]; exists {
		return rejectedResult(call.Operation, wmsRejection(
			CodeDuplicateRequest,
			"A semantically equivalent request already exists.",
			map[string]any{"request_id": m.semanticRequests[semanticKey]},
			validation.RetryQuery,
		))
	}
	request := RequestRecord{
		ID:                 m.nextRequestIDLocked(),
		Intent:             strings.TrimSpace(payload.Intent),
		Rationale:          strings.TrimSpace(payload.Rationale),
		CreatedBy:          authorization.Subject,
		AffectedInterfaces: sortedUnique(payload.AffectedInterfaces),
		AffectedScopes:     sortedUnique(payload.AffectedScopes),
		RefinementState:    "unrefined",
		Revision:           1,
	}
	m.requests[request.ID] = request
	m.semanticRequests[semanticKey] = request.ID
	m.events = append(m.events, AuditEvent{
		Operation:     call.Operation,
		Subject:       authorization.Subject,
		RequestID:     request.ID,
		PolicyVersion: authorization.PolicyVersion,
	})
	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	return result
}

func (m *Memory) refineRequestLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	request, exists := m.requests[call.RequestID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	var payload refineRequestPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if !validClassification(payload.Classification) || !validRefinementState(payload.RefinementState) {
		return rejectedResult(call.Operation, invalidRequest("classification/refinement_state", "the value is outside the request vocabulary"))
	}
	if (payload.Intent != "" && isBlank(payload.Intent)) || (payload.Rationale != "" && isBlank(payload.Rationale)) {
		return rejectedResult(call.Operation, invalidRequest("intent/rationale", "supplied intent and rationale must not be blank"))
	}
	priorSemanticKey := requestSemanticKeyFor(request.Intent, request.AffectedInterfaces, request.AffectedScopes)
	approvalID := payload.HumanApprovalID
	if call.HumanApprovalID != "" {
		approvalID = call.HumanApprovalID
	}
	approval, exists := m.approvals[approvalID]
	if !exists {
		return rejectedResult(call.Operation, approvalRejected(call.Operation, authorization, "request"))
	}
	requirement := validation.ApprovalRequirement{
		Action:             validation.Operation("request.refine"),
		DelegatedPrincipal: authorization.Subject,
		ProjectID:          m.projectID,
		RequestID:          request.ID,
		Digest:             payload.ApprovalRefinementDigest,
		PolicyVersion:      authorization.PolicyVersion,
	}
	if rejection := validation.ValidateApproval(approval, requirement, m.now()); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	if payload.Intent != "" {
		request.Intent = strings.TrimSpace(payload.Intent)
	}
	if payload.Rationale != "" {
		request.Rationale = strings.TrimSpace(payload.Rationale)
	}
	if payload.Owner != "" {
		request.Owner = payload.Owner
	}
	if payload.AffectedInterfaces != nil {
		request.AffectedInterfaces = sortedUnique(payload.AffectedInterfaces)
	}
	if payload.AffectedScopes != nil {
		request.AffectedScopes = sortedUnique(payload.AffectedScopes)
	}
	if payload.Relationships != nil {
		if rejection := validateRelationships(payload.Relationships, m.requests, request.ID); rejection != nil {
			return rejectedResult(call.Operation, rejection)
		}
		request.Relationships = append([]RequestRelationship(nil), payload.Relationships...)
	}
	request.Classification = payload.Classification
	request.RefinementState = payload.RefinementState
	nextSemanticKey := requestSemanticKeyFor(request.Intent, request.AffectedInterfaces, request.AffectedScopes)
	if duplicateID, exists := m.semanticRequests[nextSemanticKey]; exists && duplicateID != request.ID {
		return rejectedResult(call.Operation, wmsRejection(
			CodeDuplicateRequest,
			"A semantically equivalent request already exists.",
			map[string]any{"request_id": duplicateID},
			validation.RetryQuery,
		))
	}
	request.Revision++
	m.requests[request.ID] = request
	if priorSemanticKey != nextSemanticKey {
		delete(m.semanticRequests, priorSemanticKey)
	}
	m.semanticRequests[nextSemanticKey] = request.ID
	approval.Status = validation.ApprovalStatusConsumed
	m.approvals[approvalID] = approval
	m.events = append(m.events, AuditEvent{
		Operation:              call.Operation,
		Subject:                authorization.Subject,
		AuthorizedHumanSubject: approval.ApprovedSubject,
		RequestID:              request.ID,
		PolicyVersion:          authorization.PolicyVersion,
	})
	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.ApprovalStatus = validation.ApprovalStatusConsumed
	return result
}

func (m *Memory) updatePriorityLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	request, exists := m.requests[call.RequestID]
	if !exists {
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
		if _, exists := m.changeSets[request.ChangeSetID]; !exists {
			return rejectedResult(call.Operation, validationNotFound("change-set"))
		}
	}
	if request.BuildWorkItemID != "" {
		if _, exists := m.workItems[request.BuildWorkItemID]; !exists {
			return rejectedResult(call.Operation, validationNotFound("work-item"))
		}
	}
	request.BusinessPriority = payload.BusinessPriority
	request.Revision++
	m.requests[request.ID] = request
	if request.ChangeSetID != "" {
		changeSet := m.changeSets[request.ChangeSetID]
		changeSet.BusinessPriority = payload.BusinessPriority
		m.changeSets[changeSet.ID] = changeSet
		changeSetPriority = changeSet.BusinessPriority
	}
	if request.BuildWorkItemID != "" {
		workItem := m.workItems[request.BuildWorkItemID]
		workItem.Priority = payload.BusinessPriority
		m.workItems[workItem.ID] = workItem
		workItemPriority = workItem.Priority
	}
	m.events = append(m.events, AuditEvent{
		Operation:     call.Operation,
		Subject:       authorization.Subject,
		RequestID:     request.ID,
		WorkItemID:    request.BuildWorkItemID,
		PolicyVersion: authorization.PolicyVersion,
	})
	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.AuditEvent = "priority-updated"
	result.LinkedChangeSetPriority = changeSetPriority
	result.LinkedWorkItemPriority = workItemPriority
	if workItem, exists := m.workItems[request.BuildWorkItemID]; exists {
		result.WorkItemState = workItem.State
		result.ContractVersion = workItem.ContractVersion
	}
	return result
}

func (m *Memory) linkChangeSetLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	request, exists := m.requests[call.RequestID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	var payload changeSetLinkPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	changeSet, exists := m.changeSets[payload.ChangeSetID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("change-set"))
	}
	if payload.ChangeSetID == "" || request.ChangeSetID == payload.ChangeSetID ||
		(payload.TargetRevision != "" && changeSet.Revision != payload.TargetRevision) ||
		(changeSet.Revision != "proposed" && changeSet.Revision != "approved") {
		return rejectedResult(call.Operation, invalidRequest("change_set_id/target_revision", "the link is duplicate or the target revision is not mutable"))
	}
	request.ChangeSetID = payload.ChangeSetID
	request.Revision++
	if request.BusinessPriority != "" {
		changeSet.BusinessPriority = request.BusinessPriority
	}
	m.changeSets[changeSet.ID] = changeSet
	m.requests[request.ID] = request
	m.events = append(m.events, AuditEvent{
		Operation:     call.Operation,
		Subject:       authorization.Subject,
		RequestID:     request.ID,
		PolicyVersion: authorization.PolicyVersion,
	})
	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.Link = map[string]string{"change_set_id": payload.ChangeSetID}
	return result
}

func (m *Memory) linkWorkItemLocked(call CallRequest, authorization validation.AuthorizationContext) Result {
	request, exists := m.requests[call.RequestID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	if rejection := validateRequestRevision(call, request); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	var payload workItemLinkPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	workItem, exists := m.workItems[payload.BuildWorkItemID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("work-item"))
	}
	if payload.BuildWorkItemID == "" || request.BuildWorkItemID == payload.BuildWorkItemID {
		return rejectedResult(call.Operation, invalidRequest("build_work_item_id", "the work-item link is empty or duplicate"))
	}
	request.BuildWorkItemID = payload.BuildWorkItemID
	request.Revision++
	if request.BusinessPriority != "" {
		workItem.Priority = request.BusinessPriority
	}
	m.workItems[workItem.ID] = workItem
	m.requests[request.ID] = request
	m.events = append(m.events, AuditEvent{
		Operation:     call.Operation,
		Subject:       authorization.Subject,
		RequestID:     request.ID,
		WorkItemID:    payload.BuildWorkItemID,
		PolicyVersion: authorization.PolicyVersion,
	})
	result := newResult(call.Operation)
	result.Outcome = outcomeApplied
	result.Mutation = mutationApplied
	result.Idempotency = idempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	result.Link = map[string]string{"build_work_item_id": payload.BuildWorkItemID}
	return result
}

func (m *Memory) getRequestLocked(call CallRequest) Result {
	request, exists := m.requests[call.RequestID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("request"))
	}
	result := newResult(call.Operation)
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	return result
}

func (m *Memory) queryRequestsLocked(call CallRequest) Result {
	var payload requestQueryPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	ids := make([]string, 0, len(m.requests))
	for id := range m.requests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := newResult(call.Operation)
	for _, id := range ids {
		request := m.requests[id]
		if matchesRequestQuery(request, payload) {
			result.Requests = append(result.Requests, cloneRequest(request))
		}
	}
	result.Resource = result.Requests
	return result
}

func (m *Memory) getWorkItemLocked(call CallRequest) Result {
	item, exists := m.workItems[call.WorkItemID]
	if !exists {
		return rejectedResult(call.Operation, validationNotFound("work-item"))
	}
	projection := projectWorkItem(item)
	result := newResult(call.Operation)
	result.Resource = projection
	result.Items = []WorkItemProjection{projection}
	return result
}

func (m *Memory) queryWorkItemsLocked(call CallRequest) Result {
	var payload workItemQueryPayload
	if err := decodePayload(call.Payload, &payload); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	ids := make([]string, 0, len(m.workItems))
	for id := range m.workItems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := newResult(call.Operation)
	for _, id := range ids {
		item := m.workItems[id]
		projection := projectWorkItem(item)
		if matchesWorkItemQuery(projection, payload) {
			result.Items = append(result.Items, projection)
		}
	}
	result.Resource = result.Items
	return result
}

func (m *Memory) queryBlockedWorkLocked(call CallRequest) Result {
	ids := make([]string, 0, len(m.workItems))
	for id := range m.workItems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := newResult(call.Operation)
	for _, id := range ids {
		item := m.workItems[id]
		if item.State != validation.StateBlocked {
			continue
		}
		projection := projectWorkItem(item)
		projection.ReasonKind = sanitizedReasonKind(item.BlockReason)
		projection.NextAction = "review-resolution"
		projection.ResolutionOptions = []string{
			"add-requirement",
			"out-of-scope",
			"impact-amendment",
			"defer",
			"acknowledge",
		}
		result.Items = append(result.Items, projection)
	}
	result.Resource = result.Items
	return result
}

func validateRequestRevision(call CallRequest, request RequestRecord) *validation.Rejection {
	if call.ExpectedRequestRevision == nil || *call.ExpectedRequestRevision != request.Revision {
		var expected any
		if call.ExpectedRequestRevision != nil {
			expected = *call.ExpectedRequestRevision
		}
		return wmsRejection(
			CodeStaleRequestRevision,
			"The request changed after this operation was prepared.",
			map[string]any{"expected_request_revision": expected, "current_request_revision": request.Revision},
			validation.RetryRefresh,
		)
	}
	return nil
}

func requestSemanticKey(payload createRequestPayload) string {
	return requestSemanticKeyFor(payload.Intent, payload.AffectedInterfaces, payload.AffectedScopes)
}

func requestSemanticKeyFor(intent string, affectedInterfaces, affectedScopes []string) string {
	interfaces := sortedUnique(affectedInterfaces)
	scopes := sortedUnique(affectedScopes)
	value := struct {
		Intent     string
		Interfaces []string
		Scopes     []string
	}{strings.ToLower(strings.TrimSpace(intent)), interfaces, scopes}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func requestFingerprint(call CallRequest, authorization validation.AuthorizationContext) string {
	call.ActorContextRef = ""
	call.IdempotencyKey = ""
	var payload any
	if len(call.Payload) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(call.Payload))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			payload = string(call.Payload)
		}
	}
	call.Payload = nil
	authorization.ExpiresAt = time.Time{}
	authorization.AllowedActions = slices.Clone(authorization.AllowedActions)
	slices.Sort(authorization.AllowedActions)
	authorization.AllowedRefs = slices.Clone(authorization.AllowedRefs)
	slices.Sort(authorization.AllowedRefs)
	canonical := struct {
		Request       CallRequest
		Payload       any
		Authorization validation.AuthorizationContext
	}{call, payload, authorization}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func isRequestMutation(operation string) bool {
	switch operation {
	case "request.create", "request.refine", "request.update-priority",
		"request.link-change-set", "request.link-build-work-item",
		"blocked-work.submit-resolution", "blocked-work.acknowledge":
		return true
	default:
		return false
	}
}

func matchesRequestQuery(request RequestRecord, query requestQueryPayload) bool {
	if query.RefinementState != "" && request.RefinementState != query.RefinementState {
		return false
	}
	if query.Owner != "" && request.Owner != query.Owner {
		return false
	}
	if query.BusinessPriority != "" && request.BusinessPriority != query.BusinessPriority {
		return false
	}
	if query.Interface != "" && !slices.Contains(request.AffectedInterfaces, query.Interface) {
		return false
	}
	if query.Scope != "" && !slices.Contains(request.AffectedScopes, query.Scope) {
		return false
	}
	if query.Relationship != "" && !containsRelationship(request.Relationships, query.Relationship) {
		return false
	}
	return true
}

func matchesWorkItemQuery(item WorkItemProjection, query workItemQueryPayload) bool {
	if query.State != "" && item.State != query.State {
		return false
	}
	if query.Owner != "" && item.Owner != query.Owner {
		return false
	}
	if query.Dependency != "" && !slices.Contains(item.Dependencies, query.Dependency) {
		return false
	}
	return query.Priority == "" || item.Priority == query.Priority
}

func projectWorkItem(item validation.WorkItem) WorkItemProjection {
	dependencies := make([]string, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		dependencies = append(dependencies, dependency.ID)
	}
	sort.Strings(dependencies)
	return WorkItemProjection{
		ID:              item.ID,
		State:           item.State,
		Owner:           item.Owner,
		Dependencies:    dependencies,
		Priority:        item.Priority,
		ContractVersion: item.ContractVersion,
	}
}

func sanitizedReasonKind(reason string) string {
	if strings.Contains(strings.ToLower(reason), "undefined") {
		return "undefined-behavior"
	}
	if reason == "" {
		return "unresolved-precondition"
	}
	return "unresolved-precondition"
}

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

func validateRelationships(relationships []RequestRelationship, requests map[string]RequestRecord, requestID string) *validation.Rejection {
	seen := make(map[string]struct{}, len(relationships))
	for _, relationship := range relationships {
		if relationship.Target == "" || relationship.Target == requestID || !validRelationshipType(relationship.Type) {
			return invalidRequest("relationships", "relationship type or target is invalid")
		}
		if _, exists := requests[relationship.Target]; !exists {
			return validationNotFound("request")
		}
		key := relationship.Type + "\x00" + relationship.Target
		if _, exists := seen[key]; exists {
			return invalidRequest("relationships", "duplicate relationship")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validRelationshipType(value string) bool {
	switch value {
	case "depends-on", "conflicts-with", "supersedes", "related-to":
		return true
	default:
		return false
	}
}

func containsRelationship(relationships []RequestRelationship, relationshipType string) bool {
	return slices.ContainsFunc(relationships, func(relationship RequestRelationship) bool {
		return relationship.Type == relationshipType
	})
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func approvalRejected(action string, authorization validation.AuthorizationContext, targetType string) *validation.Rejection {
	return wmsRejection(
		validation.CodeUnauthorizedAction,
		"A valid, unused Gate approval bound to this operation is required.",
		map[string]any{
			"required_action": action,
			"target_type":     targetType,
			"policy_version":  authorization.PolicyVersion,
		},
		validation.RetryAuthorize,
	)
}
