package jobsite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

func TestCycleEvidenceSnapshotsBackendOwnedResults(t *testing.T) {
	// Deliberately use a struct whose field order differs from the sorted
	// map order produced when unmarshalling an interface-valued resource.
	resource := &struct {
		Z string `json:"z"`
		A string `json:"a"`
	}{Z: "last", A: "first"}
	decision := &validation.Decision{After: &validation.StateVersion{State: validation.StateBuilding, ContractVersion: 5}}
	report := &LoopReport{}
	err := appendCycleEvidence(t.TempDir(), report, CycleEvidence{
		Cycle: 1,
		Transitions: []LoopTransition{{
			Decision: adapter.Result{Resource: resource, Decision: decision},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCycleEvidence(report.Cycles); err != nil {
		t.Fatalf("resource normalization changed the stored hash: %v", err)
	}
	resource.Z = "reused"
	decision.After.State = validation.StateBlocked
	if err := VerifyCycleEvidence(report.Cycles); err != nil {
		t.Fatalf("backend-owned response pointers changed immutable evidence: %v", err)
	}
}

func TestWorkerTriageFixtureSnapshotsReusedCallbackBuffers(t *testing.T) {
	req, _ := loopTestRequest(t, []Fault{FaultImplementation, FaultNone}, 1)
	req.RequirementIDs = []string{"REQ-1", "REQ-2"}
	commandBuffer := []string{"fixture-command", ""}
	requirementBuffer := []string{""}
	runner, triage := req.Test, req.Triage
	req.Test = func(ctx context.Context, repository Repository, cycle int) (TestRun, error) {
		result, err := runner(ctx, repository, cycle)
		commandBuffer[1] = fmt.Sprintf("cycle-%d", cycle)
		result.Command = commandBuffer
		return result, err
	}
	req.Triage = func(ctx context.Context, run TestRun, cycle int) (TriageInput, error) {
		result, err := triage(ctx, run, cycle)
		requirementBuffer[0] = fmt.Sprintf("REQ-%d", cycle)
		result.RequirementIDs = requirementBuffer
		return result, err
	}
	report, err := RunFixtureLoop(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Terminal != LoopPassed || len(report.Cycles) != 2 {
		t.Fatalf("unexpected fixture result: %#v", report)
	}
	for index, cycle := range report.Cycles {
		if !reflect.DeepEqual(cycle.Tests.Command, []string{"fixture-command", fmt.Sprintf("cycle-%d", index+1)}) ||
			!reflect.DeepEqual(cycle.Triage.RequirementIDs, []string{fmt.Sprintf("REQ-%d", index+1)}) {
			t.Fatalf("cycle %d retained a reused callback buffer: command=%v requirements=%v", index+1, cycle.Tests.Command, cycle.Triage.RequirementIDs)
		}
	}
	// The caller may also reuse its buffers after the run has returned.
	commandBuffer[0], requirementBuffer[0] = "reused-command", "REQ-REUSED"
	verifyLoopTestEvidence(t, req.OutputDir, report)
}

func TestWorkerTriageFixtureRejectsReplayedRunIdentity(t *testing.T) {
	req, tasks := loopTestRequest(t, []Fault{FaultNone}, 0)
	prior := newLoopLifecycle(req.WMS, req.ActorContextRef, req.WorkItem, req.RunID, req.ContractVersion)
	if err := prior.claim(); err != nil {
		t.Fatal(err)
	}
	if err := prior.renew(1); err != nil {
		t.Fatal(err)
	}
	if err := prior.testsPass(1); err != nil {
		t.Fatal(err)
	}
	wms := req.WMS.(interface {
		WorkItem(string) (validation.WorkItem, bool)
	})
	before, _ := wms.WorkItem(req.WorkItem)
	// A fresh evidence directory cannot authorize a second dispatch using a
	// historical claim response, even when every lifecycle key can replay.
	report, err := RunFixtureLoop(context.Background(), req)
	if err == nil || report == nil || report.Terminal != LoopFailed || len(report.Cycles) != 0 || len(*tasks) != 0 {
		t.Fatalf("historical run identity dispatched fresh work: report=%#v error=%v", report, err)
	}
	after, _ := wms.WorkItem(req.WorkItem)
	if before.State != validation.StateInspecting || !reflect.DeepEqual(before, after) {
		t.Fatal("historical claim changed the current Inspecting work item")
	}
	if len(report.Transitions) != 1 || !report.Transitions[0].Decision.Decision.Replayed {
		t.Fatalf("replayed claim is absent from private evidence: %#v", report.Transitions)
	}
	if report.State != validation.StateReadyForBuilding || report.ContractVersion != req.ContractVersion {
		t.Fatalf("replayed claim replaced the requested pre-claim snapshot: %s v%d", report.State, report.ContractVersion)
	}
	transition := report.Transitions[0]
	if transition.After != transition.Before || transition.ContractVersion != req.ContractVersion || transition.Decision.Decision.After.State != validation.StateBuilding {
		t.Fatalf("historical decision was promoted or omitted from evidence: %#v", transition)
	}
	verifyLoopTestEvidence(t, req.OutputDir, report)
}

type loopReviewAdapter struct {
	Adapter
	tamper      func(*PatchBundle)
	unsupported bool
	opens       int
}

func (a *loopReviewAdapter) Capabilities() []Capability {
	if a.unsupported {
		return nil
	}
	return a.Adapter.Capabilities()
}

func (a *loopReviewAdapter) Open(ctx context.Context, request OpenRequest) (Session, error) {
	a.opens++
	session, err := a.Adapter.Open(ctx, request)
	if err != nil || a.tamper == nil {
		return session, err
	}
	return &loopReviewSession{Session: session, tamper: a.tamper}, nil
}

type loopReviewSession struct {
	Session
	tamper func(*PatchBundle)
}

func (s *loopReviewSession) CollectPatch(ctx context.Context) (PatchBundle, error) {
	bundle, err := s.Session.CollectPatch(ctx)
	if err != nil {
		return bundle, err
	}
	s.tamper(&bundle)
	// Recompute integrity metadata: a self-consistent bundle is still
	// rejected when it does not belong to this dispatched assignment.
	return NewPatchBundle(bundle.Role, bundle.WorkItem, bundle.Cycle, bundle.PolicyDigest,
		bundle.SourceCommit, bundle.WorkerRoot, bundle.IntegrationBase, bundle.Operations)
}

func TestWorkerTriageFixtureRejectsMisboundSandboxPatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(*PatchBundle)
	}{
		{"role", func(bundle *PatchBundle) { bundle.Role = RoleWorkerB }},
		{"work-item", func(bundle *PatchBundle) { bundle.WorkItem = "WI-OTHER" }},
		{"cycle", func(bundle *PatchBundle) { bundle.Cycle++ }},
		{"policy", func(bundle *PatchBundle) { bundle.PolicyDigest = "sha256:other-policy" }},
		{"source", func(bundle *PatchBundle) { bundle.SourceCommit = "other-source" }},
		{"worker-root", func(bundle *PatchBundle) { bundle.WorkerRoot = "other-projection" }},
		{"integration-base", func(bundle *PatchBundle) { bundle.IntegrationBase = "other-baseline" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, tasks := loopTestRequest(t, []Fault{FaultNone}, 1)
			adapter := &loopReviewAdapter{Adapter: req.Adapter, tamper: test.tamper}
			req.Adapter = adapter
			req.Test = func(context.Context, Repository, int) (TestRun, error) {
				t.Error("misbound Worker patch reached test execution")
				return TestRun{}, nil
			}
			report, err := RunFixtureLoop(context.Background(), req)
			if err == nil || errorCode(err) != CodePatchTampered || report.Terminal != LoopFailed || report.State != validation.StateBuilding {
				t.Fatalf("misbound patch advanced the fixture: report=%#v error=%v", report, err)
			}
			if len(report.Cycles) != 1 || report.Cycles[0].Stage != "workers" || report.Cycles[0].Merge != nil || adapter.opens != 1 || len(*tasks) != 1 {
				t.Fatalf("misbound patch did not stop before peer dispatch/import: %#v", report.Cycles)
			}
			workers := report.Cycles[0].Workers
			if len(workers) != 1 || len(workers[0].Audit) == 0 || workers[0].Audit[len(workers[0].Audit)-1].EventType != EventSandboxCleanup {
				t.Fatal("misbound patch failure omitted sandbox cleanup evidence")
			}
			integration, err := openGit(filepath.Join(req.OutputDir, "cycle-0001", integrationDir))
			if err != nil {
				t.Fatal(err)
			}
			defer integration.Close()
			head, err := integration.head()
			if err != nil || head != req.SourceCommit {
				t.Fatal("misbound patch changed Integration HEAD")
			}
			verifyLoopTestEvidence(t, req.OutputDir, report)
		})
	}
}

func TestWorkerTriageFixtureRejectsInvalidConfigurationBeforeClaim(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*LoopRequest)
	}{
		{"nil-adapter", func(req *LoopRequest) { req.Adapter = nil }},
		{"unsupported-adapter", func(req *LoopRequest) {
			req.Adapter = &loopReviewAdapter{Adapter: req.Adapter, unsupported: true}
		}},
		{"nil-workers", func(req *LoopRequest) { req.Workers = nil }},
		{"nil-wms", func(req *LoopRequest) { req.WMS = nil }},
		{"negative-retries", func(req *LoopRequest) { req.MaxRetries = -1 }},
		{"empty-requirements", func(req *LoopRequest) { req.RequirementIDs = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, tasks := loopTestRequest(t, []Fault{FaultNone}, 1)
			wms := req.WMS.(interface {
				WorkItem(string) (validation.WorkItem, bool)
			})
			test.change(&req)
			report, err := RunFixtureLoop(context.Background(), req)
			if err == nil || report != nil || len(*tasks) != 0 {
				t.Fatalf("invalid configuration dispatched work: report=%#v error=%v", report, err)
			}
			if _, err := os.Lstat(req.OutputDir); !os.IsNotExist(err) {
				t.Fatal("invalid configuration created the output directory")
			}
			item, ok := wms.WorkItem(req.WorkItem)
			if !ok || item.State != validation.StateReadyForBuilding || item.Lease != nil || item.ContractVersion != req.ContractVersion {
				t.Fatal("invalid configuration claimed WMS ownership")
			}
			if adapter, ok := req.Adapter.(*loopReviewAdapter); ok && adapter.opens != 0 {
				t.Fatal("unsupported adapter was opened")
			}
		})
	}
}
