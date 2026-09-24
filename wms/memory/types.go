// Package memory provides a deterministic in-memory WMS for Validation Rules
// and Drafting Table conformance tests.
package memory

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redhat-et/protobot/wms/validation"
)

const (
	CodeInvalidRequest       = "INVALID_REQUEST"
	CodeWMSUnavailable       = "WMS_UNAVAILABLE"
	CodeDuplicateRequest     = "DUPLICATE_REQUEST"
	CodeStaleRequestRevision = "STALE_REQUEST_REVISION"
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

// Config supplies the trusted project, Gate, clock, and local lease policy.
type Config struct {
	ProjectID           string
	Gate                Gate
	Now                 func() time.Time
	LeaseDuration       time.Duration
	MaterializerSubject string
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
	MutationNone    Mutation = "none"
	MutationApplied Mutation = "applied"
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

// ChangeSet is the minimal in-memory record needed to exercise authorized
// request linking and priority snapshots.
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

// AuditEvent records an accepted WMS mutation in the in-memory adapter.
type AuditEvent struct {
	Operation              string
	Subject                string
	AuthorizedHumanSubject string
	RequestID              string
	WorkItemID             string
	RuleVersion            string
	PolicyVersion          string
	Before                 *validation.StateVersion
	After                  *validation.StateVersion
}

type idempotencyEntry struct {
	fingerprint string
	result      Result
}

type materializationEntry struct {
	fingerprint string
	result      Result
}

// Memory is an atomic, project-scoped WMS test adapter. It stores requests,
// lifecycle records, Gate approvals, and idempotency results in memory.
type Memory struct {
	mu                         sync.RWMutex
	projectID                  string
	gate                       Gate
	now                        func() time.Time
	leaseDuration              time.Duration
	materializerSubject        string
	workItems                  map[string]validation.WorkItem
	requests                   map[string]RequestRecord
	changeSets                 map[string]ChangeSet
	approvals                  map[string]validation.ApprovalRecord
	submissions                map[string]Submission
	activeSubmissions          map[string]string
	idempotency                map[string]idempotencyEntry
	materializations           map[string]materializationEntry
	semanticRequests           map[string]string
	events                     []AuditEvent
	nextRequestID              uint64
	nextResolutionSubmissionID uint64
	nextAcknowledgementID      uint64
	resolutionRevisions        map[string]uint64
	nextFencingToken           uint64
}

// New creates an empty, project-scoped WMS adapter.
func New(config Config) (*Memory, error) {
	if config.ProjectID == "" {
		return nil, errors.New("project ID must not be empty")
	}
	if config.Gate == nil {
		return nil, errors.New("trusted Gate must not be nil")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 15 * time.Minute
	}
	return &Memory{
		projectID:           config.ProjectID,
		gate:                config.Gate,
		now:                 config.Now,
		leaseDuration:       config.LeaseDuration,
		materializerSubject: config.MaterializerSubject,
		workItems:           make(map[string]validation.WorkItem),
		requests:            make(map[string]RequestRecord),
		changeSets:          make(map[string]ChangeSet),
		approvals:           make(map[string]validation.ApprovalRecord),
		submissions:         make(map[string]Submission),
		activeSubmissions:   make(map[string]string),
		idempotency:         make(map[string]idempotencyEntry),
		materializations:    make(map[string]materializationEntry),
		semanticRequests:    make(map[string]string),
		resolutionRevisions: make(map[string]uint64),
	}, nil
}

// SeedWorkItem installs trusted fixture state before operations execute.
func (m *Memory) SeedWorkItem(item validation.WorkItem) error {
	if item.ID == "" || item.ProjectID != m.projectID {
		return errors.New("seed work item must have an ID in the configured project")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.workItems[item.ID]; exists {
		return errors.New("work item is already seeded")
	}
	m.workItems[item.ID] = cloneWorkItem(item)
	return nil
}

// SeedChangeSet installs the minimal trusted change-set state used by request
// linking tests.
func (m *Memory) SeedChangeSet(changeSet ChangeSet) error {
	if changeSet.ID == "" {
		return errors.New("seed change set must have an ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.changeSets[changeSet.ID]; exists {
		return errors.New("change set is already seeded")
	}
	m.changeSets[changeSet.ID] = changeSet
	return nil
}

// SeedApproval installs trusted Gate approval state for deterministic tests.
func (m *Memory) SeedApproval(approval validation.ApprovalRecord) error {
	if approval.ID == "" {
		return errors.New("seed approval must have an ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.approvals[approval.ID]; exists {
		return errors.New("approval is already seeded")
	}
	m.approvals[approval.ID] = cloneApproval(approval)
	return nil
}

// WorkItem returns a detached copy of the current in-memory record.
func (m *Memory) WorkItem(id string) (validation.WorkItem, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.workItems[id]
	return cloneWorkItem(item), ok
}

// Request returns a detached copy of the current backlog record.
func (m *Memory) Request(id string) (RequestRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	request, ok := m.requests[id]
	return cloneRequest(request), ok
}

// Submission returns a detached copy of one blocked-work submission.
func (m *Memory) Submission(id string) (Submission, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	submission, ok := m.submissions[id]
	return cloneSubmission(submission), ok
}

// Approval returns the trusted Gate approval state for test assertions.
func (m *Memory) Approval(id string) (validation.ApprovalRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	approval, ok := m.approvals[id]
	return cloneApproval(approval), ok
}

// Events returns the immutable audit events recorded so far.
func (m *Memory) Events() []AuditEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]AuditEvent(nil), m.events...)
}
