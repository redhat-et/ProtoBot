// Package adapter defines the backend-neutral WMS wire types and operation
// surface shared by in-memory and backend translators.
package adapter

import (
	"encoding/json"

	"github.com/redhat-et/protobot/wms/validation"
)

const (
	CodeInvalidRequest       = "INVALID_REQUEST"
	CodeWMSUnavailable       = "WMS_UNAVAILABLE"
	CodeDuplicateRequest     = "DUPLICATE_REQUEST"
	CodeStaleRequestRevision = "STALE_REQUEST_REVISION"
	CodeUnknownMutation      = "UNKNOWN_MUTATION"
)

// Gate resolves an opaque transport reference to trusted authorization
// context. A request body cannot provide or widen the returned claims.
type Gate interface {
	Resolve(actorContextRef string) (validation.AuthorizationContext, bool)
}

// StaticGate is a test Gate backed by a fixed set of named contexts.
type StaticGate map[string]validation.AuthorizationContext

// Resolve returns a trusted context registered under actorContextRef.
func (g StaticGate) Resolve(actorContextRef string) (validation.AuthorizationContext, bool) {
	context, ok := g[actorContextRef]
	if !ok {
		return validation.AuthorizationContext{}, false
	}
	context.AllowedActions = append([]validation.Operation(nil), context.AllowedActions...)
	context.AllowedRefs = append([]string(nil), context.AllowedRefs...)
	return context, true
}

// CallRequest is the backend-neutral WMS operation envelope. Actor claims are
// intentionally absent; ActorContextRef is resolved only by the configured
// trusted Gate.
type CallRequest struct {
	Operation       string `json:"operation"`
	ActorContextRef string `json:"actor_context_ref"`
	// PolicyVersion is an untrusted caller assertion. The adapter only
	// accepts an exact echo of the Gate-issued version and never uses it to
	// select or weaken policy.
	PolicyVersion           string           `json:"policy_version,omitempty"`
	RequestID               string           `json:"request_id,omitempty"`
	WorkItemID              string           `json:"work_item_id,omitempty"`
	ExpectedRequestRevision *uint64          `json:"expected_request_revision,omitempty"`
	ExpectedState           validation.State `json:"expected_state,omitempty"`
	ExpectedContractVersion *uint64          `json:"expected_contract_version,omitempty"`
	FencingToken            string           `json:"fencing_token,omitempty"`
	HumanApprovalID         string           `json:"human_approval_id,omitempty"`
	IdempotencyKey          string           `json:"idempotency_key,omitempty"`
	MaterializationKey      string           `json:"materialization_key,omitempty"`
	Payload                 json.RawMessage  `json:"payload,omitempty"`
}

// Diagnostic is a structured WMS diagnostic. Stable failures are returned in
// Error; this slice remains available for non-fatal advisory findings.
type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// Outcome is the stable result outcome for one WMS operation.
type Outcome string

const (
	OutcomeRead     Outcome = "read"
	OutcomeApplied  Outcome = "applied"
	OutcomeReplayed Outcome = "replayed"
	OutcomeRejected Outcome = "rejected"
)

// Mutation reports whether one WMS operation changed adapter state.
type Mutation string

const (
	MutationNone          Mutation = "none"
	MutationApplied       Mutation = "applied"
	MutationUnknown       Mutation = "unknown"
	MutationNotApplicable Mutation = "not-applicable"
)

// Idempotency reports whether a mutating request is new or a replay.
type Idempotency string

const (
	IdempotencyNew      Idempotency = "new"
	IdempotencyReplayed Idempotency = "replayed"
)

// Result is the stable result envelope for one WMS operation.
type Result struct {
	OK                           bool                                  `json:"ok"`
	Operation                    string                                `json:"operation"`
	Outcome                      Outcome                               `json:"outcome"`
	Resource                     any                                   `json:"resource,omitempty"`
	Diagnostics                  []Diagnostic                          `json:"diagnostics"`
	Mutation                     Mutation                              `json:"mutation"`
	Idempotency                  Idempotency                           `json:"idempotency,omitempty"`
	Error                        *validation.Rejection                 `json:"error,omitempty"`
	Decision                     *validation.Decision                  `json:"decision,omitempty"`
	RequestID                    string                                `json:"request_id,omitempty"`
	WorkItemID                   string                                `json:"work_item_id,omitempty"`
	RequestRevision              uint64                                `json:"request_revision,omitempty"`
	ApprovalStatus               validation.ApprovalStatus             `json:"approval_status,omitempty"`
	AuditEvent                   string                                `json:"audit_event,omitempty"`
	Link                         map[string]string                     `json:"link,omitempty"`
	LinkedChangeSetPriority      string                                `json:"linked_change_set_priority,omitempty"`
	LinkedWorkItemPriority       string                                `json:"linked_work_item_priority,omitempty"`
	WorkItemState                validation.State                      `json:"work_item_state,omitempty"`
	ContractVersion              uint64                                `json:"contract_version,omitempty"`
	Requests                     []RequestRecord                       `json:"requests,omitempty"`
	Items                        []WorkItemProjection                  `json:"items,omitempty"`
	Submission                   string                                `json:"submission,omitempty"`
	ResolutionSubmissionID       string                                `json:"resolution_submission_id,omitempty"`
	ResolutionSubmissionRevision uint64                                `json:"resolution_submission_revision,omitempty"`
	PriorResolutionSubmissionID  string                                `json:"prior_resolution_submission_id,omitempty"`
	PriorSubmissionStatus        validation.ResolutionSubmissionStatus `json:"prior_submission_status,omitempty"`
	PriorApprovalStatus          validation.ApprovalStatus             `json:"prior_approval_status,omitempty"`
	PlannedDependency            *PlannedDependency                    `json:"planned_dependency,omitempty"`
}

// RequestRecord is the WMS-owned mutable request backlog entry.
type RequestRecord struct {
	ID                 string                `json:"request_id"`
	Intent             string                `json:"intent"`
	Rationale          string                `json:"rationale"`
	CreatedBy          string                `json:"created_by"`
	Owner              string                `json:"owner,omitempty"`
	AffectedInterfaces []string              `json:"affected_interfaces,omitempty"`
	AffectedScopes     []string              `json:"affected_scopes,omitempty"`
	Classification     string                `json:"classification,omitempty"`
	RefinementState    string                `json:"refinement_state"`
	BusinessPriority   string                `json:"business_priority,omitempty"`
	Relationships      []RequestRelationship `json:"relationships,omitempty"`
	ChangeSetID        string                `json:"change_set_id,omitempty"`
	BuildWorkItemID    string                `json:"build_work_item_id,omitempty"`
	Revision           uint64                `json:"request_revision"`
}

// RequestRelationship is a typed relationship between backlog requests.
type RequestRelationship struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

// WorkItemProjection is the sanitized Drafting Table view of a work item.
type WorkItemProjection struct {
	ID                string           `json:"id"`
	State             validation.State `json:"state"`
	Owner             string           `json:"owner,omitempty"`
	Dependencies      []string         `json:"dependencies"`
	Priority          string           `json:"priority,omitempty"`
	ContractVersion   uint64           `json:"contract_version"`
	RequestID         string           `json:"request_id,omitempty"`
	ChangeSetID       string           `json:"change_set_id,omitempty"`
	ReasonKind        string           `json:"reason_kind,omitempty"`
	NextAction        string           `json:"next_action,omitempty"`
	ResolutionOptions []string         `json:"resolution_options,omitempty"`
}

// PlannedDependency describes the separate dependency recorded on a
// resolution submission, not on the work item.
type PlannedDependency struct {
	ChangeSetID string `json:"change_set_id"`
	Status      string `json:"status"`
}

// ChangeSet is the minimal record needed to exercise authorized request
// linking and priority snapshots.
type ChangeSet struct {
	ID               string `json:"change_set_id"`
	Revision         string `json:"revision"`
	BusinessPriority string `json:"business_priority,omitempty"`
	BuildWorkItemID  string `json:"build_work_item_id,omitempty"`
}

// Submission is one durable blocked-work resolution or acknowledgement.
type Submission struct {
	validation.ResolutionSubmission
	Revision uint64 `json:"resolution_submission_revision"`
}

// AuditEvent records an accepted WMS mutation.
type AuditEvent struct {
	Operation              string
	Subject                string
	AuthorizedHumanSubject string
	RequestID              string
	WorkItemID             string
	IdempotencyKey         string
	Outcome                Outcome
	RuleVersion            string
	PolicyVersion          string
	Before                 *validation.StateVersion
	After                  *validation.StateVersion
}
