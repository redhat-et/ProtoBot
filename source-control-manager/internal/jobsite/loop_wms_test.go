package jobsite

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/memory"
	"github.com/redhat-et/protobot/wms/validation"
)

var lifecycleTestTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func lifecycleTestWMS(t *testing.T) *memory.Memory {
	t.Helper()
	wms, err := memory.New(memory.Config{
		ProjectID: "loop-project",
		Gate: adapter.StaticGate{
			"loop-actor": {
				Subject:        "fixture-job-site",
				Role:           validation.RoleJobSite,
				ProjectID:      "loop-project",
				WorkItemID:     "WI-69",
				AllowedActions: []validation.Operation{validation.OperationClaim, validation.OperationRenewLease, validation.OperationTestsPass, validation.OperationRaiseSpecQuestion},
				AllowedRefs:    []string{"project:loop-project"},
				ExpiresAt:      lifecycleTestTime.Add(time.Hour),
				PolicyVersion:  "fixture-policy/v1",
			},
		},
		Now:           func() time.Time { return lifecycleTestTime },
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wms.SeedWorkItem(validation.WorkItem{
		ID:                     "WI-69",
		ProjectID:              "loop-project",
		State:                  validation.StateReadyForBuilding,
		ContractVersion:        4,
		ImplementationRequired: true,
		Readiness: validation.Readiness{
			ContractComplete:           true,
			SourceImmutable:            true,
			SpecificationValidated:     true,
			RequirementReferencesValid: true,
			PipelineEntrySatisfied:     true,
			ImpactDispositioned:        true,
			PolicyCompatible:           true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return wms
}

func TestLoopLifecycleClaimRenewTestsPass(t *testing.T) {
	wms := lifecycleTestWMS(t)
	lifecycle := newLoopLifecycle(wms, "loop-actor", "WI-69", "run-69", 4)
	if err := lifecycle.claim(); err != nil {
		t.Fatal(err)
	}
	fence := lifecycle.fence
	if fence == "" || lifecycle.state != validation.StateBuilding || lifecycle.version != 5 {
		t.Fatalf("claim state = %s v%d fence=%q", lifecycle.state, lifecycle.version, fence)
	}
	if err := lifecycle.renew(1); err != nil {
		t.Fatal(err)
	}
	if lifecycle.fence != fence || lifecycle.version != 6 {
		t.Fatalf("renew changed fence or did not advance version: %q v%d", lifecycle.fence, lifecycle.version)
	}
	if err := lifecycle.testsPass(1); err != nil {
		t.Fatal(err)
	}
	item, _ := wms.WorkItem("WI-69")
	if lifecycle.state != validation.StateInspecting || lifecycle.version != 7 || item.State != lifecycle.state || item.ContractVersion != lifecycle.version || item.Lease == nil || item.Lease.FencingToken != fence {
		t.Fatalf("passing gate: lifecycle=%#v item=%#v", lifecycle, item)
	}
	if item.InspectionRunSealed || item.FindingsTerminal || item.FinalTestsPassed {
		t.Fatal("Building fixture fabricated independent inspection evidence")
	}
	want := []validation.State{validation.StateBuilding, validation.StateBuilding, validation.StateInspecting}
	for i, transition := range lifecycle.transitions {
		if transition.After != want[i] || transition.ContractVersion != uint64(i+5) || !transition.Decision.OK {
			t.Fatalf("transition %d = %#v", i, transition)
		}
	}
	if len(lifecycle.transitions) != len(want) || len(wms.Events()) != len(want) {
		t.Fatalf("transition/event count = %d/%d", len(lifecycle.transitions), len(wms.Events()))
	}
}

func TestLoopLifecycleSpecificationGapBlocksAndReleasesLease(t *testing.T) {
	wms := lifecycleTestWMS(t)
	lifecycle := newLoopLifecycle(wms, "loop-actor", "WI-69", "run-69", 4)
	if err := lifecycle.claim(); err != nil {
		t.Fatal(err)
	}
	question := "REQ-1 does not define this interface case."
	if err := lifecycle.block(1, question); err != nil {
		t.Fatal(err)
	}
	item, _ := wms.WorkItem("WI-69")
	if item.State != validation.StateBlocked || item.BlockReason != question || item.Lease != nil || item.Owner != "" || lifecycle.fence != "" {
		t.Fatalf("specification gap did not release execution ownership: %#v", item)
	}
	if lifecycle.transitions[1].Operation != string(validation.OperationRaiseSpecQuestion) || lifecycle.transitions[1].After != validation.StateBlocked {
		t.Fatalf("block transition = %#v", lifecycle.transitions[1])
	}
}

func TestLoopLifecycleRejectsStaleVersionAndFence(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		wms := lifecycleTestWMS(t)
		before, _ := wms.WorkItem("WI-69")
		lifecycle := newLoopLifecycle(wms, "loop-actor", "WI-69", "stale-run", 3)
		err := lifecycle.claim()
		if err == nil || !strings.Contains(err.Error(), validation.CodeStaleContractVersion) {
			t.Fatalf("stale claim error = %v", err)
		}
		after, _ := wms.WorkItem("WI-69")
		if !reflect.DeepEqual(before, after) || lifecycle.state != validation.StateReadyForBuilding || lifecycle.version != 3 || len(wms.Events()) != 0 {
			t.Fatal("stale version mutated lifecycle state")
		}
		if len(lifecycle.transitions) != 1 || lifecycle.transitions[0].Decision.OK || lifecycle.transitions[0].Before != lifecycle.transitions[0].After {
			t.Fatalf("rejection audit = %#v", lifecycle.transitions)
		}
	})
	t.Run("fence", func(t *testing.T) {
		wms := lifecycleTestWMS(t)
		lifecycle := newLoopLifecycle(wms, "loop-actor", "WI-69", "stale-run", 4)
		if err := lifecycle.claim(); err != nil {
			t.Fatal(err)
		}
		before, _ := wms.WorkItem("WI-69")
		lifecycle.fence = "forged-fence"
		err := lifecycle.testsPass(1)
		if err == nil || !strings.Contains(err.Error(), validation.CodeStaleFencingToken) {
			t.Fatalf("stale tests-pass error = %v", err)
		}
		after, _ := wms.WorkItem("WI-69")
		if !reflect.DeepEqual(before, after) || lifecycle.state != validation.StateBuilding || lifecycle.version != 5 || len(wms.Events()) != 1 {
			t.Fatal("stale fence advanced the Building gate")
		}
	})
}

func TestLoopLifecycleRejectsReplayedClaimWithoutPromotingOwnership(t *testing.T) {
	wms := lifecycleTestWMS(t)
	first := newLoopLifecycle(wms, "loop-actor", "WI-69", "run-lost-response", 4)
	if err := first.claim(); err != nil {
		t.Fatal(err)
	}
	before, _ := wms.WorkItem("WI-69")
	// The exact request key replays the historical claim. This fixture
	// cannot reconcile current ownership, so it must refuse resumption.
	retry := newLoopLifecycle(wms, "loop-actor", "WI-69", "run-lost-response", 4)
	if err := retry.claim(); err == nil || !strings.Contains(err.Error(), "run identity was already claimed") {
		t.Fatalf("replayed claim error = %v", err)
	}
	after, _ := wms.WorkItem("WI-69")
	if retry.fence != "" || retry.state != validation.StateReadyForBuilding || retry.version != 4 || len(wms.Events()) != 1 || !reflect.DeepEqual(before, after) {
		t.Fatalf("claim replay promoted historical ownership or mutated the WMS: %#v", retry)
	}
	if len(retry.transitions) != 1 || !retry.transitions[0].Decision.Decision.Replayed || retry.transitions[0].After != validation.StateReadyForBuilding || retry.transitions[0].ContractVersion != 4 {
		t.Fatalf("replayed claim audit = %#v", retry.transitions)
	}
	if err := first.testsPass(1); err != nil {
		t.Fatal(err)
	}
	if len(wms.Events()) != 2 {
		t.Fatal("original claim did not authorize the one Building gate")
	}
}

func TestLoopLifecycleUnknownGateReferenceCannotClaim(t *testing.T) {
	wms := lifecycleTestWMS(t)
	lifecycle := newLoopLifecycle(wms, "untrusted-actor", "WI-69", "run-69", 4)
	if err := lifecycle.claim(); err == nil || !strings.Contains(err.Error(), validation.CodeUnauthorizedAction) {
		t.Fatalf("untrusted Gate reference error = %v", err)
	}
	item, _ := wms.WorkItem("WI-69")
	if item.State != validation.StateReadyForBuilding || item.Lease != nil || len(wms.Events()) != 0 {
		t.Fatal("untrusted Gate reference claimed work")
	}
}
