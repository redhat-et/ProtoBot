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
		{"cat-file", "--batch-all-objects"},
		{"cat-file", "--batch"},
		{"show", "HEAD:../outside"},
		{"cat-file", "-p", "HEAD:../outside"},
		{"show", "HEAD:.git/config"},
		{"show", "HEAD:\\escaped"},
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

	// Verify cat-file --batch is denied as forbidden flag
	res, err := session.Git(context.Background(), "cat-file", "--batch")
	if errorCode(err) != CodeSandboxCancelled && errorCode(err) != CodeSandboxDenied && !res.Denied {
		t.Fatalf("expected cat-file --batch to be denied or cancelled, got err=%v, res=%+v", err, res)
	}

	// Wait-first test: verify that when Git finishes before cancellation,
	// the real git status is returned even if ctx is cancelled afterwards.
	sessionWaitFirst, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sessionWaitFirst.Close() }()

	waitCtx, waitCancel := context.WithCancel(context.Background())
	res, err = sessionWaitFirst.Git(waitCtx, "status")
	waitCancel()
	if err != nil {
		t.Fatalf("expected successful git status when wait returns first, got err=%v", err)
	}
	if res.Status != 0 {
		t.Fatalf("expected status 0, got %d", res.Status)
	}
}

func TestInFlightCommandAcrossWallClockExpiry(t *testing.T) {
	env := NewConformanceEnv(t)
	req := env.OpenRequest(RoleWorkerA)
	req.Limits.WallClock = 40 * time.Millisecond
	session, err := NewLocalAdapter().Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.Execute(context.Background(), ExecRequest{
		Executable: "sleep",
		Args:       []string{"1"},
	})
	if errorCode(err) != CodeSandboxLimit {
		t.Fatalf("expected %s across wall-clock expiry, got err=%v, res=%+v", CodeSandboxLimit, err, res)
	}
}

func TestInFlightCommandAcrossClose(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := session.Execute(context.Background(), ExecRequest{
			Executable: "sleep",
			Args:       []string{"2"},
		})
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if errorCode(err) != CodeSandboxCancelled {
			t.Fatalf("expected %s across Close(), got %v", CodeSandboxCancelled, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("in-flight sleep did not exit after Close()")
	}
}

func TestOpenShortDeadlineDoesNotRecordWallClockLimit(t *testing.T) {
	env := NewConformanceEnv(t)
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer shortCancel()
	req := env.OpenRequest(RoleWorkerA)
	req.Limits.WallClock = 10 * time.Second
	session, err := NewLocalAdapter().Open(shortCtx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	time.Sleep(30 * time.Millisecond)

	_, err = session.ReadPath(context.Background(), sharedDocPath)
	if errorCode(err) != CodeSandboxCancelled {
		t.Fatalf("expected %s on expired openCtx, got %v", CodeSandboxCancelled, err)
	}

	for _, ev := range session.Audit() {
		if ev.EventType == EventSandboxLimit && ev.LimitName == "wall-clock" {
			t.Fatalf("spurious EventSandboxLimit with LimitName wall-clock while Limits.WallClock was in future: %+v", ev)
		}
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
	local.mu.Lock()
	defer local.mu.Unlock()
	if len(local.argvLog) == 0 {
		t.Fatalf("expected logged argv from spawned processes, got none")
	}
	for _, argv := range local.argvLog {
		for _, arg := range argv {
			if strings.Contains(arg, env.Sentinel) {
				t.Fatalf("process arg %q contains sentinel %q", arg, env.Sentinel)
			}
		}
	}
}

func TestCallerDeadlineDoesNotRecordSessionWallClockLimit(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	shortCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err = session.ReadPath(shortCtx, sharedDocPath)
	if errorCode(err) != CodeSandboxCancelled && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected CodeSandboxCancelled or context.DeadlineExceeded, got %v", err)
	}

	for _, ev := range session.Audit() {
		if ev.EventType == EventSandboxLimit && ev.LimitName == "wall-clock" {
			t.Fatalf("caller deadline was spuriously audited as session wall-clock limit: %+v", ev)
		}
	}

	// Verify session remains open and operational for subsequent calls
	data, err := session.ReadPath(context.Background(), sharedDocPath)
	if err != nil {
		t.Fatalf("subsequent ReadPath failed after caller deadline: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty data from valid ReadPath")
	}
}

func TestNetworkLifecycleCancellation(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	fetchReq := ExecRequest{
		Executable: ApprovedFetchName,
		Digest:     ApprovedFetchDigest,
		Network: &NetworkRequest{
			Destination: AllowedDestination,
			Protocol:    AllowedProtocol,
			Method:      AllowedMethod,
			Path:        AllowedNetworkPath,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = session.Execute(ctx, fetchReq)
	if errorCode(err) != CodeSandboxCancelled && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected %s or context.Canceled, got %v", CodeSandboxCancelled, err)
	}

	// Verify session was not cancelled by caller context cancellation
	res, err := session.Execute(context.Background(), fetchReq)
	if err != nil {
		t.Fatalf("subsequent fetch failed after caller cancel: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("expected status 200, got %d", res.Status)
	}

	// Verify session Cancel cancels network
	if err := session.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), fetchReq)
	if errorCode(err) != CodeSandboxCancelled {
		t.Fatalf("expected %s after Cancel(), got %v", CodeSandboxCancelled, err)
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
