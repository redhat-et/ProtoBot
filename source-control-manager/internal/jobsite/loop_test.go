package jobsite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redhat-et/protobot/wms/validation"
)

const loopPrivateSentinel = "peer-source assertion expected=43 actual=41 raw-Inspector-finding"

// The fixture executes a real shell implementation against independently
// supplied interface tests. Fault labels are known fixture ground truth,
// never an attribution inferred by copying a test log into Worker feedback.
func TestWorkerTriageFixtureOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		faults   []Fault
		retries  int
		terminal string
		state    validation.State
		roles    [][]string
	}{
		{"pass", []Fault{FaultNone}, 2, LoopPassed, validation.StateInspecting, [][]string{{RoleWorkerA, RoleWorkerB}}},
		{"tests", []Fault{FaultTest, FaultNone}, 2, LoopPassed, validation.StateInspecting, [][]string{{RoleWorkerA, RoleWorkerB}, {RoleWorkerA}}},
		{"implementation", []Fault{FaultImplementation, FaultNone}, 2, LoopPassed, validation.StateInspecting, [][]string{{RoleWorkerA, RoleWorkerB}, {RoleWorkerB}}},
		{"both-partial-fix-still-fails", []Fault{FaultBoth, FaultImplementation, FaultNone}, 2, LoopPassed, validation.StateInspecting, [][]string{{RoleWorkerA, RoleWorkerB}, {RoleWorkerA, RoleWorkerB}, {RoleWorkerB}}},
		{"specification-gap", []Fault{FaultSpecificationGap}, 2, LoopSpecGap, validation.StateBlocked, [][]string{{RoleWorkerA, RoleWorkerB}}},
		{"retry-exhausted", []Fault{FaultImplementation, FaultImplementation, FaultImplementation}, 2, LoopRetryExhausted, validation.StateBuilding, [][]string{{RoleWorkerA, RoleWorkerB}, {RoleWorkerB}, {RoleWorkerB}}},
		{"no-retries", []Fault{FaultTest}, 0, LoopRetryExhausted, validation.StateBuilding, [][]string{{RoleWorkerA, RoleWorkerB}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, tasks := loopTestRequest(t, tc.faults, tc.retries)
			report, err := RunFixtureLoop(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if report.Terminal != tc.terminal || report.State != tc.state || len(report.Cycles) != len(tc.faults) {
				t.Fatalf("unexpected fixture result: %#v", report)
			}
			item, _ := req.WMS.(interface {
				WorkItem(string) (validation.WorkItem, bool)
			}).WorkItem(req.WorkItem)
			if item.State != report.State || item.ContractVersion != report.ContractVersion || item.InspectionRunSealed {
				t.Fatalf("report diverges from WMS or invented inspection: %#v", item)
			}
			if tc.terminal == LoopSpecGap && (item.Lease != nil || report.Escalation != "clarify-specification") {
				t.Fatal("specification gap did not release ownership and escalate")
			}
			if tc.terminal == LoopRetryExhausted && report.Escalation != "retry-budget-exhausted" {
				t.Fatal("exhaustion lacks terminal escalation evidence")
			}
			for i, cycle := range report.Cycles {
				if cycle.Stage != "complete" || cycle.Triage.Fault != tc.faults[i] {
					t.Fatalf("cycle %d is incomplete or misclassified: %#v", i+1, cycle)
				}
				roles := []string{}
				for _, worker := range cycle.Workers {
					roles = append(roles, worker.Role)
					if err := VerifyAuditChain(worker.Audit); err != nil {
						t.Fatal(err)
					}
					if worker.Audit[len(worker.Audit)-1].EventType != EventSandboxCleanup {
						t.Fatal("Worker cleanup is absent from the cycle audit")
					}
				}
				if !reflect.DeepEqual(roles, tc.roles[i]) {
					t.Fatalf("cycle %d dispatch = %v, want %v", i+1, roles, tc.roles[i])
				}
				if !strings.Contains(cycle.Triage.RawLogs, loopPrivateSentinel) || len(cycle.Redactions) == 0 {
					t.Fatal("private raw input or sanitizer decision is missing")
				}
				for _, feedback := range cycle.Feedback {
					if feedback.RetriesRemaining != tc.retries-i {
						t.Fatalf("retry budget = %d, want %d", feedback.RetriesRemaining, tc.retries-i)
					}
				}
			}
			for _, task := range *tasks {
				encoded, err := json.Marshal(task)
				if err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range []string{loopPrivateSentinel, "expected=", "actual=", "src/answer.sh", "tests/canonical/check.sh", "raw-Inspector"} {
					if bytes.Contains(encoded, []byte(forbidden)) {
						t.Fatalf("Worker assignment leaked %q: %s", forbidden, encoded)
					}
				}
				if task.Cycle > 1 && (task.Feedback == nil || task.Feedback.Role != task.Role) {
					t.Fatal("retry Worker did not receive its own sanitized feedback")
				}
			}
			verifyLoopTestEvidence(t, req.OutputDir, report)
			// The canonical source is immutable across every local cycle.
			source, err := openGit(req.SourceRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			head, err := source.head()
			if err != nil || head != req.SourceCommit {
				t.Fatal("fixture loop mutated its canonical source")
			}
		})
	}
}

func TestWorkerTriageFixtureFailClosed(t *testing.T) {
	for _, stage := range []string{"worker", "runner", "triage", "inconsistent-pass", "unapproved-requirement", "stale-wms"} {
		t.Run(stage, func(t *testing.T) {
			req, _ := loopTestRequest(t, []Fault{FaultNone}, 1)
			wantStage := stage
			switch stage {
			case "worker":
				wantStage = "workers"
				req.Workers[RoleWorkerA] = func(ctx context.Context, session Session, _ WorkerTask) error {
					return session.WritePath(ctx, "src/answer.sh", []byte("peer overwrite"), ModeFile)
				}
			case "runner":
				wantStage = "test"
				req.Test = func(context.Context, Repository, int) (TestRun, error) {
					return TestRun{}, errors.New("private runner unavailable")
				}
			case "triage":
				req.Triage = func(context.Context, TestRun, int) (TriageInput, error) {
					return TriageInput{}, errors.New("private triage unavailable")
				}
			case "inconsistent-pass":
				wantStage = "triage"
				req.Triage = func(context.Context, TestRun, int) (TriageInput, error) {
					return TriageInput{Outcome: "fail", Fault: FaultImplementation, RequirementIDs: []string{"REQ-1"}}, nil
				}
			case "unapproved-requirement":
				wantStage = "sanitize"
				req.Triage = func(context.Context, TestRun, int) (TriageInput, error) {
					return TriageInput{Outcome: "pass", Fault: FaultNone, RequirementIDs: []string{"REQ-secret"}}, nil
				}
			case "stale-wms":
				req.ContractVersion--
			}
			report, err := RunFixtureLoop(context.Background(), req)
			if err == nil || report.Terminal != LoopFailed || report.State == validation.StateInspecting {
				t.Fatalf("failure did not stop the pipeline: report=%#v error=%v", report, err)
			}
			if stage == "stale-wms" {
				if len(report.Cycles) != 0 || report.State != validation.StateReadyForBuilding {
					t.Fatal("stale WMS version dispatched a Worker")
				}
				return
			}
			if len(report.Cycles) != 1 || report.Cycles[0].Stage != wantStage || report.Cycles[0].Error == "" {
				t.Fatalf("failure cycle evidence = %#v", report.Cycles)
			}
			verifyLoopTestEvidence(t, req.OutputDir, report)
		})
	}
}

func TestWorkerTriageFixtureCancellationDoesNotAdvance(t *testing.T) {
	for _, at := range []string{"worker", "test", "triage"} {
		t.Run(at, func(t *testing.T) {
			req, _ := loopTestRequest(t, []Fault{FaultNone}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch at {
			case "worker":
				worker := req.Workers[RoleWorkerA]
				req.Workers[RoleWorkerA] = func(ctx context.Context, session Session, task WorkerTask) error {
					cancel()
					return worker(ctx, session, task)
				}
			case "test":
				runner := req.Test
				req.Test = func(ctx context.Context, repo Repository, cycle int) (TestRun, error) {
					result, err := runner(ctx, repo, cycle)
					cancel()
					return result, err
				}
			case "triage":
				triage := req.Triage
				req.Triage = func(ctx context.Context, run TestRun, cycle int) (TriageInput, error) {
					result, err := triage(ctx, run, cycle)
					cancel()
					return result, err
				}
			}
			report, err := RunFixtureLoop(ctx, req)
			if !errors.Is(err, context.Canceled) || report.Terminal != LoopCancelled || report.State != validation.StateBuilding || len(report.Cycles) != 1 {
				t.Fatalf("cancelled run advanced: %#v; error=%v", report, err)
			}
			verifyLoopTestEvidence(t, req.OutputDir, report)
		})
	}
}

func TestWorkerTriageFixtureEvidenceIsDeterministic(t *testing.T) {
	req, _ := loopTestRequest(t, []Fault{FaultImplementation, FaultNone}, 1)
	first, err := RunFixtureLoop(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.OutputDir = filepath.Join(t.TempDir(), "second")
	req.WMS = lifecycleTestWMS(t)
	second, err := RunFixtureLoop(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		left, _ := json.MarshalIndent(first, "", "  ")
		right, _ := json.MarshalIndent(second, "", "  ")
		t.Fatalf("identical fixture inputs changed evidence:\n%s\n%s", left, right)
	}
	second.Cycles[0].Feedback[0].Reason = "tampered"
	if VerifyCycleEvidence(second.Cycles) == nil {
		t.Fatal("evidence tampering was accepted")
	}
}

func TestWorkerTriageFixtureRejectsReusedOutputBeforeClaim(t *testing.T) {
	req, _ := loopTestRequest(t, []Fault{FaultNone}, 0)
	if err := os.Mkdir(req.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RunFixtureLoop(context.Background(), req); err == nil {
		t.Fatal("existing evidence directory was accepted")
	}
	item, _ := req.WMS.(interface {
		WorkItem(string) (validation.WorkItem, bool)
	}).WorkItem(req.WorkItem)
	if item.State != validation.StateReadyForBuilding {
		t.Fatal("existing output claimed work")
	}
}

func loopTestRequest(t *testing.T, faults []Fault, retries int) (LoopRequest, *[]WorkerTask) {
	t.Helper()
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".protobot/projection.yaml":         string(fixturePolicy),
		".protobot/requirements/REQ-1.yaml": "id: REQ-1\nstatement: The answer command shall print 42.\n",
		"src/answer.sh":                     "printf '42\\n'\n",
		"tests/canonical/check.sh":          "test \"$(sh src/answer.sh)\" = 42\n",
	}
	for name, content := range files {
		full := filepath.Join(sourceRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	source, err := initRepo(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	commit, err := commitAll(source, "Worker/Triage shell fixture")
	if err != nil {
		t.Fatal(err)
	}
	tasks := []WorkerTask{}
	worker := func(ctx context.Context, session Session, task WorkerTask) error {
		tasks = append(tasks, task)
		if task.Cycle > len(faults) {
			return errors.New("fixture dispatched beyond its scripted retry limit")
		}
		fault := faults[task.Cycle-1]
		name, content := "src/answer.sh", "printf '42\\n'\n"
		if task.Role == RoleWorkerA {
			name, content = "tests/canonical/check.sh", "test \"$(sh src/answer.sh)\" = 42\n"
			if fault == FaultTest || fault == FaultBoth {
				content = "test \"$(sh src/answer.sh)\" = 43\n"
			}
			if fault == FaultSpecificationGap {
				content = "exit 1 # undefined interface case\n"
			}
		} else if fault == FaultImplementation || fault == FaultBoth {
			content = "printf '41\\n'\n"
		}
		return session.WritePath(ctx, name, []byte(content), ModeFile)
	}
	return LoopRequest{
		SourceRoot: sourceRoot, SourceCommit: commit, OutputDir: filepath.Join(root, "evidence"),
		WorkItem: "WI-69", RunID: "fixture-69", RequirementIDs: []string{"REQ-1"}, MaxRetries: retries,
		Adapter: NewLocalAdapter(),
		Limits:  ResourceLimits{WallClock: time.Minute, CPU: time.Minute, MemoryBytes: 8 << 20, ProcessCount: 4, WritableBytes: 1 << 20, WritableFiles: 10},
		Workers: map[string]FixtureWorker{RoleWorkerA: worker, RoleWorkerB: worker},
		Test:    runLoopShellTest,
		Triage: func(_ context.Context, run TestRun, cycle int) (TriageInput, error) {
			fault := faults[cycle-1]
			outcome := "fail"
			switch fault {
			case FaultNone:
				outcome = "pass"
			case FaultSpecificationGap:
				outcome = "blocked"
			}
			return TriageInput{Outcome: outcome, Fault: fault, RequirementIDs: []string{"REQ-1"}, Reason: loopPrivateSentinel, RawLogs: run.Stdout + run.Stderr + loopPrivateSentinel, InspectorFindings: loopPrivateSentinel}, nil
		},
		WMS: lifecycleTestWMS(t), ActorContextRef: "loop-actor", ContractVersion: 4,
	}, &tasks
}

func runLoopShellTest(ctx context.Context, integration Repository, _ int) (TestRun, error) {
	result := TestRun{Command: []string{"sh", "tests/canonical/check.sh"}}
	command := exec.CommandContext(ctx, result.Command[0], result.Command[1:]...)
	command.Dir = integration.Path
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.Status = exit.ExitCode()
		return result, nil
	}
	return result, err
}

func verifyLoopTestEvidence(t *testing.T, dir string, report *LoopReport) {
	t.Helper()
	if err := VerifyCycleEvidence(report.Cycles); err != nil {
		t.Fatal(err)
	}
	for _, cycle := range report.Cycles {
		path := filepath.Join(dir, fmt.Sprintf("cycle-%04d.json", cycle.Cycle))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var recorded CycleEvidence
		if err := json.Unmarshal(data, &recorded); err != nil {
			t.Fatal(err)
		}
		if recorded.EventDigest != cycle.EventDigest {
			t.Fatal("persisted cycle digest differs from report")
		}
		if err := VerifyCycleEvidence(append(append([]CycleEvidence{}, report.Cycles[:cycle.Cycle-1]...), recorded)); err != nil {
			t.Fatalf("persisted evidence is not independently verifiable: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("cycle evidence permissions are not private")
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded LoopReport
	if err := json.Unmarshal(data, &recorded); err != nil || recorded.Terminal != report.Terminal || recorded.State != report.State || recorded.ContractVersion != report.ContractVersion {
		t.Fatal("persisted report differs from result")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("fixture output directory is not private")
	}
}
