package jobsite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

// LoopWMS is the backend-neutral lifecycle boundary used by the fixture
// loop. Authorization remains the responsibility of the WMS's trusted Gate.
type LoopWMS interface {
	Execute(adapter.CallRequest) adapter.Result
}

// LoopTransition records one lifecycle request and the authoritative WMS
// response. Rejected requests and replayed claims retain the prior state/version.
// This evidence is private control-plane data, never Worker feedback.
type LoopTransition struct {
	Operation       string           `json:"operation"`
	Before          validation.State `json:"before"`
	After           validation.State `json:"after"`
	ContractVersion uint64           `json:"contract_version"`
	Decision        adapter.Result   `json:"decision"`
}

type loopLifecycle struct {
	wms         LoopWMS
	actorRef    string
	workItem    string
	runID       string
	state       validation.State
	version     uint64
	fence       string
	transitions []LoopTransition
}

func newLoopLifecycle(wms LoopWMS, actorRef, workItem, runID string, version uint64) *loopLifecycle {
	return &loopLifecycle{
		wms:      wms,
		actorRef: actorRef,
		workItem: workItem,
		runID:    runID,
		state:    validation.StateReadyForBuilding,
		version:  version,
	}
}

func (l *loopLifecycle) claim() error {
	return l.call(validation.OperationClaim, 0, validation.Payload{}, validation.StateBuilding)
}

func (l *loopLifecycle) testsPass(cycle int) error {
	return l.call(validation.OperationTestsPass, cycle, validation.Payload{BuildTestsPassed: true}, validation.StateInspecting)
}

// block is reserved for specification gaps. Exhausting a retry budget does
// not manufacture a specification question or grant abandonment authority;
// that terminal fixture outcome leaves the work item in Building.
func (l *loopLifecycle) block(cycle int, question string) error {
	return l.call(validation.OperationRaiseSpecQuestion, cycle, validation.Payload{Question: question}, validation.StateBlocked)
}

func (l *loopLifecycle) renew(cycle int) error {
	return l.call(validation.OperationRenewLease, cycle, validation.Payload{}, l.state)
}

func (l *loopLifecycle) call(operation validation.Operation, cycle int, payload validation.Payload, target validation.State) error {
	if l.wms == nil || strings.TrimSpace(l.actorRef) == "" || strings.TrimSpace(l.workItem) == "" || strings.TrimSpace(l.runID) == "" || l.version == 0 || cycle < 0 {
		return fmt.Errorf("worker/triage lifecycle configuration is invalid")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	key, err := json.Marshal(struct {
		RunID     string               `json:"run_id"`
		WorkItem  string               `json:"work_item"`
		Cycle     int                  `json:"cycle"`
		Operation validation.Operation `json:"operation"`
	}{l.runID, l.workItem, cycle, operation})
	if err != nil {
		return err
	}
	version := l.version
	result := l.wms.Execute(adapter.CallRequest{
		Operation:               string(operation),
		ActorContextRef:         l.actorRef,
		WorkItemID:              l.workItem,
		ExpectedState:           l.state,
		ExpectedContractVersion: &version,
		FencingToken:            l.fence,
		IdempotencyKey:          "worker-triage:" + digestBytes(key),
		Payload:                 data,
	})
	// A backend may reuse response buffers after Execute returns. Keep an
	// independent wire snapshot for the immutable lifecycle audit.
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("worker/triage lifecycle %s returned invalid wire evidence", operation)
	}
	var snapshot adapter.Result
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return fmt.Errorf("worker/triage lifecycle %s returned invalid wire evidence", operation)
	}
	result = snapshot
	transition := LoopTransition{
		Operation:       string(operation),
		Before:          l.state,
		After:           l.state,
		ContractVersion: l.version,
		Decision:        result,
	}
	defer func() { l.transitions = append(l.transitions, transition) }()
	if !result.OK {
		code := "WMS_REJECTED"
		if result.Error != nil {
			code = result.Error.Code
		}
		return fmt.Errorf("worker/triage lifecycle %s rejected: %s", operation, code)
	}
	decision := result.Decision
	if decision == nil || decision.Authority != validation.AuthorityAuthoritative || decision.Outcome != validation.OutcomeAllowed ||
		decision.Operation != operation || decision.Before == nil || decision.Before.State != l.state || decision.Before.ContractVersion != l.version ||
		decision.After == nil || decision.After.State != target || decision.After.ContractVersion != l.version+1 ||
		(operation == validation.OperationClaim && decision.FencingTokenIssued == "") {
		return fmt.Errorf("worker/triage lifecycle %s returned invalid authoritative evidence", operation)
	}
	// A fixture run is single-use. A historical claim cannot establish
	// current ownership without reconciliation. Keep its raw decision in
	// the audit, but do not promote its state, version, or fencing token.
	if operation == validation.OperationClaim && decision.Replayed {
		return fmt.Errorf("worker/triage fixture run identity was already claimed; reconcile before starting another run")
	}
	l.state = decision.After.State
	l.version = decision.After.ContractVersion
	if operation == validation.OperationClaim {
		l.fence = decision.FencingTokenIssued
	}
	if l.state == validation.StateBlocked {
		l.fence = ""
	}
	transition.After = l.state
	transition.ContractVersion = l.version
	return nil
}
