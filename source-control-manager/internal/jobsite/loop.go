package jobsite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/redhat-et/protobot/wms/validation"
)

// LoopVersion identifies the backend-neutral Worker/Triage fixture pipeline.
const LoopVersion = "jobsite-worker-triage/v1"

// Terminal fixture loop outcomes.
const (
	LoopPassed         = "passed"
	LoopSpecGap        = "specification-gap"
	LoopRetryExhausted = "retry-exhausted"
	LoopFailed         = "error"
	LoopCancelled      = "cancelled"
)

// WorkerTask contains only the recipient's own assignment and sanitized
// feedback. Integration repositories and private evidence never enter it.
type WorkerTask struct {
	Role     string          `json:"role"`
	Cycle    int             `json:"cycle"`
	Feedback *WorkerFeedback `json:"feedback,omitempty"`
}

// FixtureWorker supplies deterministic edits through the sandbox contract.
// Real model execution and integration-level backend isolation are separate
// milestones; a callback is not an operating-system security boundary.
type FixtureWorker func(context.Context, Session, WorkerTask) error

// TestRun is private evidence returned by the trusted fixture test runner.
// An ordinary failing test has a nonzero Status and a nil runner error.
type TestRun struct {
	Command []string `json:"command"`
	Status  int      `json:"status"`
	Stdout  string   `json:"stdout,omitempty"`
	Stderr  string   `json:"stderr,omitempty"`
}

// IntegrationTest runs only in the private Integration environment.
type IntegrationTest func(context.Context, Repository, int) (TestRun, error)

// FixtureTriage attributes failures using deterministic fixture ground truth.
// It receives private evidence; only SanitizeFeedback output reaches Workers.
type FixtureTriage func(context.Context, TestRun, int) (TriageInput, error)

// LoopRequest pins the fixture's source, approved requirement identifiers,
// retry policy, sandbox adapter, and authoritative WMS identity/version.
// OutputDir must not exist. MaxRetries excludes the initial attempt.
type LoopRequest struct {
	SourceRoot      string
	SourceCommit    string
	Policy          []byte
	PolicyDigest    string
	OutputDir       string
	WorkItem        string
	RunID           string
	RequirementIDs  []string
	MaxRetries      int
	Adapter         Adapter
	Limits          ResourceLimits
	Workers         map[string]FixtureWorker
	Test            IntegrationTest
	Triage          FixtureTriage
	WMS             LoopWMS
	ActorContextRef string
	ContractVersion uint64
}

// WorkerCycleEvidence retains patch provenance and cleanup audit privately.
type WorkerCycleEvidence struct {
	Role  string              `json:"role"`
	Patch PatchBundle         `json:"patch"`
	Audit []SandboxAuditEvent `json:"audit"`
}

// CycleEvidence is an immutable, hash-linked private record of every
// attempted cycle, including incomplete cycles and fail-closed decisions.
type CycleEvidence struct {
	Cycle           int                   `json:"cycle"`
	Stage           string                `json:"stage"`
	SourceCommit    string                `json:"source_commit"`
	Exports         []AuditRecord         `json:"exports,omitempty"`
	Workers         []WorkerCycleEvidence `json:"workers,omitempty"`
	Merge           *CycleMerge           `json:"merge,omitempty"`
	Tests           *TestRun              `json:"tests,omitempty"`
	Triage          *TriageInput          `json:"triage,omitempty"`
	Feedback        []WorkerFeedback      `json:"feedback,omitempty"`
	Redactions      []RedactionDecision   `json:"redactions,omitempty"`
	Transitions     []LoopTransition      `json:"transitions,omitempty"`
	State           validation.State      `json:"state"`
	ContractVersion uint64                `json:"contract_version"`
	Error           string                `json:"error,omitempty"`
	PreviousDigest  string                `json:"previous_digest"`
	EventDigest     string                `json:"event_digest,omitempty"`
}

// LoopReport is control-plane evidence, not a Worker response. Passed means
// ready for Inspecting, never completed. A terminal fixture escalation does
// not invent a WMS transition or authorize abandonment.
// State and ContractVersion reflect the last applied decision, or the
// requested pre-claim snapshot when no claim was applied.
type LoopReport struct {
	Version           string           `json:"version"`
	WorkItem          string           `json:"work_item"`
	RunID             string           `json:"run_id"`
	InitialCommit     string           `json:"initial_commit"`
	PolicyDigest      string           `json:"policy_digest"`
	MaxRetries        int              `json:"max_retries"`
	Terminal          string           `json:"terminal"`
	Escalation        string           `json:"escalation,omitempty"`
	State             validation.State `json:"state"`
	ContractVersion   uint64           `json:"contract_version"`
	IntegrationCommit string           `json:"integration_commit,omitempty"`
	Transitions       []LoopTransition `json:"transitions"`
	Cycles            []CycleEvidence  `json:"cycles"`
}

// RunFixtureLoop exports fresh isolated projections each cycle, dispatches
// only the routed Workers, imports role-restricted patches, runs private
// tests, sanitizes Triage, and advances the WMS through its existing API.
// Neither callbacks nor the local adapter enable autonomous model execution.
// All output is private (directory 0700, evidence 0600) and never published.
func RunFixtureLoop(ctx context.Context, req LoopRequest) (report *LoopReport, resultErr error) {
	if err := validateLoopRequest(req); err != nil {
		return nil, err
	}
	if err := os.Mkdir(req.OutputDir, 0o700); err != nil {
		return nil, err
	}
	lifecycle := newLoopLifecycle(req.WMS, req.ActorContextRef, req.WorkItem, req.RunID, req.ContractVersion)
	report = &LoopReport{
		Version: LoopVersion, WorkItem: req.WorkItem, RunID: req.RunID,
		InitialCommit: req.SourceCommit, MaxRetries: req.MaxRetries,
		Terminal: LoopFailed, Cycles: []CycleEvidence{},
	}
	defer func() {
		if resultErr != nil {
			report.Terminal = LoopFailed
		}
		if errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			report.Terminal = LoopCancelled
		}
		report.State, report.ContractVersion = lifecycle.state, lifecycle.version
		report.Transitions = append([]LoopTransition{}, lifecycle.transitions...)
		if err := writeLoopEvidence(filepath.Join(req.OutputDir, "report.json"), report); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := lifecycle.claim(); err != nil {
		return report, err
	}
	sourceRoot, sourceCommit := req.SourceRoot, req.SourceCommit
	feedback := []WorkerFeedback{{Role: RoleWorkerA}, {Role: RoleWorkerB}}
	for cycle := 1; ; cycle++ {
		evidence := CycleEvidence{Cycle: cycle, SourceCommit: sourceCommit}
		transitionStart := len(lifecycle.transitions)
		var nextRoot, nextCommit string
		var nextFeedback []WorkerFeedback
		err := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			evidence.Stage = "renew-lease"
			if err := lifecycle.renew(cycle); err != nil {
				return err
			}
			evidence.Stage = "export"
			exported, err := Export(ExportRequest{
				SourceRoot: sourceRoot, SourceCommit: sourceCommit, Policy: req.Policy,
				PolicyDigest: req.PolicyDigest, OutputDir: filepath.Join(req.OutputDir, fmt.Sprintf("cycle-%04d", cycle)),
				WorkItem: req.WorkItem, Cycle: cycle,
			})
			if err != nil {
				return err
			}
			report.PolicyDigest = exported.Policy.Digest
			evidence.SourceCommit, evidence.Exports = exported.SourceCommit, exported.Audits
			report.IntegrationCommit = exported.Integration.RootCommit
			evidence.Stage = "workers"
			input := CycleImport{}
			for _, item := range feedback {
				projection := exported.WorkerA
				if item.Role == RoleWorkerB {
					projection = exported.WorkerB
				}
				task := WorkerTask{Role: item.Role, Cycle: cycle}
				if cycle > 1 {
					copyFeedback := item
					copyFeedback.RequirementIDs = append([]string{}, item.RequirementIDs...)
					task.Feedback = &copyFeedback
				}
				workerEvidence, err := runFixtureWorker(ctx, req, exported, projection, task)
				evidence.Workers = append(evidence.Workers, workerEvidence)
				if err != nil {
					return err
				}
				if item.Role == RoleWorkerA {
					input.WorkerA = workerEvidence.Patch
				} else {
					input.WorkerB = workerEvidence.Patch
				}
			}
			evidence.Stage = "import"
			if err := ctx.Err(); err != nil {
				return err
			}
			evidence.Merge, err = MergeCycle(exported.Integration, exported.Policy, input)
			if err != nil {
				return err
			}
			report.IntegrationCommit = evidence.Merge.Commit
			evidence.Stage = "test"
			tests, err := req.Test(ctx, exported.Integration, cycle)
			evidence.Tests = &tests
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			evidence.Stage = "triage"
			triage, err := req.Triage(ctx, tests, cycle)
			evidence.Triage = &triage
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if tests.Status < 0 || (tests.Status == 0) != (triage.Outcome == "pass") {
				return fail(CodeTriageInvalid, "Triage outcome does not match fixture test evidence.")
			}
			remaining := req.MaxRetries - (cycle - 1)
			evidence.Stage = "sanitize"
			evidence.Feedback, evidence.Redactions, err = SanitizeFeedback(triage, req.RequirementIDs, remaining)
			if err != nil {
				return err
			}
			evidence.Stage = "transition"
			if err := ctx.Err(); err != nil {
				return err
			}
			switch triage.Outcome {
			case "pass":
				if err := lifecycle.testsPass(cycle); err != nil {
					return err
				}
				report.Terminal = LoopPassed
			case "blocked":
				if err := lifecycle.block(cycle, "Clarify the approved requirements or interface contracts for "+strings.Join(evidence.Feedback[0].RequirementIDs, ", ")+"."); err != nil {
					return err
				}
				report.Terminal, report.Escalation = LoopSpecGap, "clarify-specification"
			case "fail":
				if remaining == 0 {
					report.Terminal, report.Escalation = LoopRetryExhausted, "retry-budget-exhausted"
				}
			}
			evidence.Stage = "complete"
			nextRoot, nextCommit = exported.Integration.Path, evidence.Merge.Commit
			nextFeedback = evidence.Feedback
			return nil
		}()
		evidence.State, evidence.ContractVersion = lifecycle.state, lifecycle.version
		evidence.Transitions = append([]LoopTransition{}, lifecycle.transitions[transitionStart:]...)
		if err != nil {
			err = errors.Join(err, ctx.Err())
			evidence.Error = err.Error()
		}
		if recordErr := appendCycleEvidence(req.OutputDir, report, evidence); recordErr != nil {
			return report, errors.Join(err, recordErr)
		}
		if err != nil {
			return report, err
		}
		if report.Terminal != LoopFailed {
			return report, nil
		}
		sourceRoot, sourceCommit, feedback = nextRoot, nextCommit, nextFeedback
	}
}

func validateLoopRequest(req LoopRequest) error {
	if req.SourceRoot == "" || req.SourceCommit == "" || req.OutputDir == "" ||
		strings.TrimSpace(req.WorkItem) == "" || strings.TrimSpace(req.RunID) == "" ||
		req.MaxRetries < 0 || req.Adapter == nil || req.Test == nil || req.Triage == nil ||
		req.WMS == nil || req.ActorContextRef == "" || req.ContractVersion == 0 ||
		req.Workers[RoleWorkerA] == nil || req.Workers[RoleWorkerB] == nil || len(req.RequirementIDs) == 0 {
		return fmt.Errorf("worker/triage fixture configuration is incomplete or invalid")
	}
	if missing := missingRequiredCapabilities(req.Adapter.Capabilities()); len(missing) > 0 {
		return fail(CodeSandboxFailClosed, "Fixture adapter lacks required sandbox capabilities.")
	}
	_, _, err := SanitizeFeedback(TriageInput{Outcome: "pass", Fault: FaultNone}, req.RequirementIDs, 0)
	return err
}

func runFixtureWorker(ctx context.Context, req LoopRequest, exported *ExportResult, projection Repository, task WorkerTask) (evidence WorkerCycleEvidence, resultErr error) {
	evidence.Role = task.Role
	session, err := req.Adapter.Open(ctx, OpenRequest{
		Role: task.Role, WorkItem: req.WorkItem, Cycle: task.Cycle,
		Projection: projection, Policy: exported.Policy, SourceCommit: exported.SourceCommit,
		WorkerRoot: projection.RootCommit, IntegrationBase: exported.Integration.RootCommit,
		Limits: req.Limits, TraceID: req.RunID,
	})
	if err != nil {
		return evidence, err
	}
	if session == nil {
		return evidence, fail(CodeSandboxFailClosed, "Fixture adapter returned no sandbox session.")
	}
	defer func() {
		resultErr = errors.Join(resultErr, session.Close())
		evidence.Audit = session.Audit()
		resultErr = errors.Join(resultErr, VerifyAuditChain(evidence.Audit))
	}()
	if err := req.Workers[task.Role](ctx, session, task); err != nil {
		return evidence, err
	}
	evidence.Patch, err = session.CollectPatch(ctx)
	if err != nil {
		return evidence, err
	}
	bundle := evidence.Patch
	if bundle.Role != task.Role || bundle.WorkItem != req.WorkItem || bundle.Cycle != task.Cycle ||
		bundle.PolicyDigest != exported.Policy.Digest || bundle.SourceCommit != exported.SourceCommit ||
		bundle.WorkerRoot != projection.RootCommit || bundle.IntegrationBase != exported.Integration.RootCommit {
		return evidence, fail(CodePatchTampered, "Worker patch does not match the dispatched projection and cycle.")
	}
	return evidence, nil
}

func appendCycleEvidence(dir string, report *LoopReport, evidence CycleEvidence) error {
	evidence.PreviousDigest = emptyDigest()
	if len(report.Cycles) > 0 {
		evidence.PreviousDigest = report.Cycles[len(report.Cycles)-1].EventDigest
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	// Snapshot callback-owned slices and adapter response pointers before
	// hashing. Later callbacks may reuse their buffers without changing an
	// earlier cycle's immutable record.
	var snapshot CycleEvidence
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	evidence = snapshot
	data, err = json.Marshal(evidence)
	if err != nil {
		return err
	}
	evidence.EventDigest = digestBytes(data)
	if err := writeLoopEvidence(filepath.Join(dir, fmt.Sprintf("cycle-%04d.json", evidence.Cycle)), evidence); err != nil {
		return err
	}
	report.Cycles = append(report.Cycles, evidence)
	return nil
}

// VerifyCycleEvidence verifies record order and the canonical JSON hash chain.
func VerifyCycleEvidence(cycles []CycleEvidence) error {
	previous := emptyDigest()
	for i, cycle := range cycles {
		if cycle.Cycle != i+1 || cycle.PreviousDigest != previous {
			return fmt.Errorf("fixture cycle evidence chain break at cycle %d", i+1)
		}
		want := cycle.EventDigest
		cycle.EventDigest = ""
		data, err := json.Marshal(cycle)
		if err != nil {
			return err
		}
		if want != digestBytes(data) {
			return fmt.Errorf("fixture cycle evidence digest mismatch at cycle %d", i+1)
		}
		previous = want
	}
	return nil
}

func writeLoopEvidence(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Close())
}
