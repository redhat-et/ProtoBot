package github

import (
	"sync"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

// Coordinator provides compare-and-swap and idempotency for backends that
// lack native conditional updates (GitHub Issues).
type Coordinator interface {
	WithLock(fn func())
}

// InProcessCoordinator is a process-local mutex coordinator.
type InProcessCoordinator struct {
	mu sync.Mutex
}

// WithLock runs fn under the coordinator lock.
func (c *InProcessCoordinator) WithLock(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}

type idempotencyEntry struct {
	fingerprint string
	result      adapter.Result
}

type materializationEntry struct {
	fingerprint string
	result      adapter.Result
	issueNumber int
}

type indexes struct {
	requestIssue         map[string]int // request id -> issue number
	workItemIssue        map[string]int // work item id -> issue number
	materializationKey   map[string]materializationEntry
	idempotency          map[string]idempotencyEntry
	semanticRequests     map[string]string
	pendingRequestCreate map[string]string // idempotency key -> request id after UNKNOWN_MUTATION
	changeSets           map[string]adapter.ChangeSet
	approvals            map[string]validation.ApprovalRecord
	nextRequestSeq       uint64
	nextFenceSeq         uint64
}

func newIndexes() *indexes {
	return &indexes{
		requestIssue:         make(map[string]int),
		workItemIssue:        make(map[string]int),
		materializationKey:   make(map[string]materializationEntry),
		idempotency:          make(map[string]idempotencyEntry),
		semanticRequests:     make(map[string]string),
		pendingRequestCreate: make(map[string]string),
		changeSets:           make(map[string]adapter.ChangeSet),
		approvals:            make(map[string]validation.ApprovalRecord),
	}
}
