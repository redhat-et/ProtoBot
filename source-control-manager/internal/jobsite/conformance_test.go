package jobsite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// NewConformanceEnv builds the suite fixture in a temporary directory.
func NewConformanceEnv(t testing.TB) *ConformanceEnv {
	t.Helper()
	env, err := SetupConformanceEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestLocalAdapterConformance(t *testing.T) {
	env := NewConformanceEnv(t)
	report := RunSandboxConformance(context.Background(), NewLocalAdapter(), env)
	if !report.Passed {
		for _, check := range report.FailedChecks() {
			t.Errorf("%s %s: %s", check.ID, check.Name, check.Detail)
		}
		t.Fatalf("local adapter conformance failed")
	}
	if !report.AutonomousExecution {
		t.Fatalf("local adapter disabled autonomous execution: %s", report.FailClosedReason)
	}
	if report.ContractVersion != ContractVersion {
		t.Fatalf("contract version = %s", report.ContractVersion)
	}
	if report.Backend.Name != LocalBackendName {
		t.Fatalf("backend = %#v", report.Backend)
	}
	if report.AuditHead == "" {
		t.Fatal("missing audit head")
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatal(err)
	}
}

func TestFailClosedAdapterDisablesAutonomousExecution(t *testing.T) {
	env := NewConformanceEnv(t)
	adapter := FailClosedAdapter{Inner: NewLocalAdapter(), Missing: CapNetworkPolicy}
	report := RunSandboxConformance(context.Background(), adapter, env)
	if report.Passed {
		t.Fatal("fail-closed adapter should not have report.Passed = true")
	}
	if report.AutonomousExecution {
		t.Fatal("fail-closed adapter still allowed autonomous execution")
	}
	if report.FailClosedReason == "" {
		t.Fatal("missing fail-closed reason")
	}
	if !strings.Contains(report.FailClosedReason, "autonomous execution disabled") {
		t.Fatalf("reason = %q", report.FailClosedReason)
	}
	if len(report.Checks) == 0 {
		t.Fatal("fail-closed report has no checks")
	}
}

func TestUnsupportedPlatformFailsClosed(t *testing.T) {
	env := NewConformanceEnv(t)
	adapter := FailClosedAdapter{Inner: NewLocalAdapter(), Platform: true}
	report := RunSandboxConformance(context.Background(), adapter, env)
	if report.Passed {
		t.Fatal("unsupported platform should not have report.Passed = true")
	}
	if report.AutonomousExecution {
		t.Fatal("unsupported platform allowed autonomous execution")
	}
	if !strings.Contains(report.FailClosedReason, "autonomous execution disabled") {
		t.Fatalf("reason = %q", report.FailClosedReason)
	}
}

func TestLocalAdapterOpensWithoutCluster(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if session.Role() != RoleWorkerA {
		t.Fatalf("role = %s", session.Role())
	}
	if session.Root() == env.Export.WorkerA.Path {
		t.Fatal("session reused the export working tree")
	}
}

func TestVerifyAuditChainRejectsReorderedEvents(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if err := session.WritePath(context.Background(), currentTestPath, []byte("x\n"), ModeFile); err != nil {
		t.Fatal(err)
	}
	events := session.Audit()
	if len(events) < 2 {
		t.Fatalf("events = %d", len(events))
	}
	events[0], events[1] = events[1], events[0]
	if err := VerifyAuditChain(events); err == nil {
		t.Fatal("reordered chain verified")
	}
}

func TestBrokerRejectsUnauthenticatedCaller(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	local := session.(*localSession)
	resp, err := http.Get(local.broker.URL + AllowedNetworkPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated broker request status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestInvalidResourceLimitsFailClosed(t *testing.T) {
	env := NewConformanceEnv(t)
	req := env.OpenRequest(RoleWorkerA)
	req.Limits.MemoryBytes = -1
	_, err := NewLocalAdapter().Open(context.Background(), req)
	if errorCode(err) != CodeSandboxFailClosed {
		t.Fatalf("expected %s for negative limits, got %v", CodeSandboxFailClosed, err)
	}
}

func TestGitArgValidation(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	forbiddenCalls := [][]string{
		{"diff", "--no-index", "--", "/etc/passwd", "/dev/null"},
		{"log", "--output=/tmp/leak.txt"},
		{"log", "--output", "/tmp/leak.txt"},
		{"--config-env=foo=BAR", "status"},
		{"diff", "/etc/passwd"},
		{"diff", "../outside"},
		{"cat-file", "-e", "/etc/passwd"},
		{"status", "/etc"},
		{"remote", "add", "origin", "http://evil.com"},
	}
	for _, args := range forbiddenCalls {
		res, err := session.Git(context.Background(), args...)
		if errorCode(err) != CodeSandboxDenied && !res.Denied {
			t.Fatalf("Git(%v) should have been denied, got err=%v, res=%+v", args, err, res)
		}
	}
}

func TestGitLifecycleCancellation(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = session.Git(ctx, "status")
	if errorCode(err) != CodeSandboxCancelled && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected %s or context.Canceled, got %v", CodeSandboxCancelled, err)
	}

	if err := session.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = session.Git(context.Background(), "status")
	if errorCode(err) != CodeSandboxCancelled {
		t.Fatalf("expected %s after Cancel(), got %v", CodeSandboxCancelled, err)
	}

	// In-flight cancellation test
	sessionInFlight, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sessionInFlight.Close() }()

	gitCtx, gitCancel := context.WithCancel(context.Background())
	gitDone := make(chan error, 1)
	go func() {
		_, err := sessionInFlight.Git(gitCtx, "cat-file", "--batch")
		gitDone <- err
	}()
	time.Sleep(50 * time.Millisecond)

	locSession := sessionInFlight.(*localSession)
	locSession.mu.Lock()
	if len(locSession.cmds) != 1 {
		locSession.mu.Unlock()
		t.Fatalf("expected 1 in-flight command, got %d", len(locSession.cmds))
	}
	runningCmd := locSession.cmds[0].Cmd
	pid := runningCmd.Process.Pid
	locSession.mu.Unlock()

	if pid <= 0 {
		t.Fatalf("expected valid child PID, got %d", pid)
	}

	gitCancel()

	select {
	case err := <-gitDone:
		if err == nil {
			t.Fatalf("expected error after git cancel, got nil")
		}
		if errorCode(err) != CodeSandboxCancelled && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected %s or context.Canceled, got %v", CodeSandboxCancelled, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("in-flight git command did not terminate after cancel")
	}

	// Assert the still-running child was terminated/killed, not merely that Git returned an error
	locSession.mu.Lock()
	if len(locSession.cmds) != 0 {
		t.Errorf("expected 0 active commands after cancel and wait, got %d", len(locSession.cmds))
	}
	locSession.mu.Unlock()

	assertProcessKilled(t, runningCmd)

	// Wait-first test: verify that when Git finishes before cancellation,
	// the real git status is returned even if ctx is cancelled afterwards.
	sessionWaitFirst, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sessionWaitFirst.Close() }()

	waitCtx, waitCancel := context.WithCancel(context.Background())
	res, err := sessionWaitFirst.Git(waitCtx, "status")
	waitCancel()
	if err != nil {
		t.Fatalf("expected successful git status when wait returns first, got err=%v", err)
	}
	if res.Status != 0 {
		t.Fatalf("expected status 0, got %d", res.Status)
	}
}

func TestProcessArgsExcludeSentinel(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	_, err = session.Execute(context.Background(), ExecRequest{Executable: "sleep", Args: []string{env.Sentinel}})
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s when sentinel is in Args, got %v", CodeSandboxDenied, err)
	}
	_, err = session.Git(context.Background(), "status", env.Sentinel)
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s when sentinel is in Git args, got %v", CodeSandboxDenied, err)
	}

	if _, err := session.Execute(context.Background(), ExecRequest{Executable: "sleep", Args: []string{"0.001"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Git(context.Background(), "status"); err != nil {
		t.Fatal(err)
	}

	local := session.(*localSession)
	for _, cmd := range local.cmds {
		for _, arg := range cmd.Args {
			if strings.Contains(arg, env.Sentinel) {
				t.Fatalf("process arg %q contains sentinel %q", arg, env.Sentinel)
			}
		}
	}
}

func TestSequentialProcessExecutionDoesNotExhaustLimit(t *testing.T) {
	env := NewConformanceEnv(t)
	req := env.OpenRequest(RoleWorkerA)
	req.Limits.ProcessCount = 2
	session, err := NewLocalAdapter().Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	for i := 0; i < 5; i++ {
		res, err := session.Git(context.Background(), "status")
		if err != nil || res.Denied {
			t.Fatalf("sequential git status %d failed: err=%v, res=%+v", i, err, res)
		}
	}
}
