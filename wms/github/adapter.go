package github

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

// Config supplies project identity, Gate, GitHub client, and lease policy.
type Config struct {
	ProjectID           string
	Gate                adapter.Gate
	Client              Client
	Coordinator         Coordinator
	Now                 func() time.Time
	LeaseDuration       time.Duration
	MaterializerSubject string
	Context             context.Context
}

// Adapter is the GitHub Issues backend translator for the #68 first slice.
type Adapter struct {
	projectID           string
	gate                adapter.Gate
	client              Client
	coord               Coordinator
	now                 func() time.Time
	leaseDuration       time.Duration
	materializerSubject string
	ctx                 context.Context
	idx                 *indexes
}

// New creates a project-scoped GitHub WMS adapter.
func New(config Config) (*Adapter, error) {
	if config.ProjectID == "" {
		return nil, errors.New("project ID must not be empty")
	}
	if config.Gate == nil {
		return nil, errors.New("trusted Gate must not be nil")
	}
	if config.Client == nil {
		return nil, errors.New("github client must not be nil")
	}
	if config.Coordinator == nil {
		config.Coordinator = &InProcessCoordinator{}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 15 * time.Minute
	}
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return &Adapter{
		projectID:           config.ProjectID,
		gate:                config.Gate,
		client:              config.Client,
		coord:               config.Coordinator,
		now:                 config.Now,
		leaseDuration:       config.LeaseDuration,
		materializerSubject: config.MaterializerSubject,
		ctx:                 ctx,
		idx:                 newIndexes(),
	}, nil
}

// SeedChangeSet installs change-set state used by request linking tests.
func (a *Adapter) SeedChangeSet(changeSet adapter.ChangeSet) error {
	if changeSet.ID == "" {
		return errors.New("seed change set must have an ID")
	}
	var err error
	a.coord.WithLock(func() {
		if _, exists := a.idx.changeSets[changeSet.ID]; exists {
			err = errors.New("change set is already seeded")
			return
		}
		a.idx.changeSets[changeSet.ID] = changeSet
	})
	return err
}

// SeedApproval installs Gate approval state for deterministic tests.
func (a *Adapter) SeedApproval(approval validation.ApprovalRecord) error {
	if approval.ID == "" {
		return errors.New("seed approval must have an ID")
	}
	var err error
	a.coord.WithLock(func() {
		if _, exists := a.idx.approvals[approval.ID]; exists {
			err = errors.New("approval is already seeded")
			return
		}
		a.idx.approvals[approval.ID] = approval
	})
	return err
}

// Execute runs one backend-neutral WMS operation against GitHub Issues.
func (a *Adapter) Execute(call adapter.CallRequest) adapter.Result {
	var result adapter.Result
	a.coord.WithLock(func() {
		result = a.executeLocked(call)
	})
	return result
}

func (a *Adapter) executeLocked(call adapter.CallRequest) adapter.Result {
	operation := validation.Operation(call.Operation)
	if !adapter.IsGitHubSubsetOperation(operation) {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, ""))
	}
	authorization, ok := a.gate.Resolve(call.ActorContextRef)
	if !ok {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, ""))
	}
	if !adapter.RoleHasWMSOperation(authorization.Role, operation) {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
	}
	if rejection := validation.AuthorizeContext(
		authorization,
		operation,
		a.projectID,
		call.WorkItemID,
		"",
		[]string{"project:" + a.projectID},
		a.now(),
	); rejection != nil {
		return rejectedResult(call.Operation, rejection)
	}
	if call.PolicyVersion != "" && call.PolicyVersion != authorization.PolicyVersion {
		return rejectedResult(call.Operation, unauthorizedRejection(call.Operation, call.WorkItemID, authorization.PolicyVersion))
	}

	switch operation {
	case validation.OperationMaterialize, validation.OperationClaim:
		return a.executeLifecycleLocked(call, authorization)
	default:
		return a.executeRequestLocked(call, authorization)
	}
}

func (a *Adapter) idempotencyScope(key string) string {
	return a.projectID + "\x00" + key
}

func (a *Adapter) checkIdempotencyLocked(key, fingerprint string) (*adapter.Result, *validation.Rejection) {
	entry, exists := a.idx.idempotency[a.idempotencyScope(key)]
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
	result := markReplayed(cloneResult(entry.result))
	return &result, nil
}

func (a *Adapter) rememberIdempotencyLocked(key, fingerprint string, result adapter.Result) {
	a.idx.idempotency[a.idempotencyScope(key)] = idempotencyEntry{
		fingerprint: fingerprint,
		result:      cloneResult(result),
	}
}

func (a *Adapter) nextFenceTokenLocked() string {
	a.idx.nextFenceSeq++
	return fmt.Sprintf("fence-%d", a.idx.nextFenceSeq)
}

func (a *Adapter) nextRequestIDLocked() string {
	a.idx.nextRequestSeq++
	return fmt.Sprintf("REQ-%05d", a.idx.nextRequestSeq)
}
