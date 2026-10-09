// Package memory provides a deterministic in-memory WMS for Validation Rules
// and Drafting Table conformance tests.
package memory

import (
	"errors"
	"sync"
	"time"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

const (
	CodeInvalidRequest       = adapter.CodeInvalidRequest
	CodeWMSUnavailable       = adapter.CodeWMSUnavailable
	CodeDuplicateRequest     = adapter.CodeDuplicateRequest
	CodeStaleRequestRevision = adapter.CodeStaleRequestRevision
)

// Re-export backend-neutral wire types so existing memory tests keep using
// package-local names.
type (
	Gate                = adapter.Gate
	StaticGate          = adapter.StaticGate
	CallRequest         = adapter.CallRequest
	Diagnostic          = adapter.Diagnostic
	Outcome             = adapter.Outcome
	Mutation            = adapter.Mutation
	Idempotency         = adapter.Idempotency
	Result              = adapter.Result
	RequestRecord       = adapter.RequestRecord
	RequestRelationship = adapter.RequestRelationship
	WorkItemProjection  = adapter.WorkItemProjection
	PlannedDependency   = adapter.PlannedDependency
	ChangeSet           = adapter.ChangeSet
	Submission          = adapter.Submission
	AuditEvent          = adapter.AuditEvent
)

const (
	OutcomeRead     = adapter.OutcomeRead
	OutcomeApplied  = adapter.OutcomeApplied
	OutcomeReplayed = adapter.OutcomeReplayed
	OutcomeRejected = adapter.OutcomeRejected

	MutationNone          = adapter.MutationNone
	MutationApplied       = adapter.MutationApplied
	MutationUnknown       = adapter.MutationUnknown
	MutationNotApplicable = adapter.MutationNotApplicable

	IdempotencyNew      = adapter.IdempotencyNew
	IdempotencyReplayed = adapter.IdempotencyReplayed
)

// Config supplies the trusted project, Gate, clock, and local lease policy.
type Config struct {
	ProjectID           string
	Gate                Gate
	Now                 func() time.Time
	LeaseDuration       time.Duration
	MaterializerSubject string
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
	observedReadiness          map[string]validation.Readiness
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
		observedReadiness:   make(map[string]validation.Readiness),
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
