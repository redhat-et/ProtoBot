package jobsite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
	if report.AutonomousExecution {
		t.Fatal("API conformance must not enable autonomous execution")
	}
	if report.IntegrationBoundaryVerified {
		t.Fatal("API conformance cannot verify the integration boundary")
	}
	if report.FailClosedReason == "" {
		t.Fatal("missing integration-boundary fail-closed reason")
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
	if len(report.AuditHeads) == 0 {
		t.Fatal("missing per-session audit heads")
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

func TestConformanceClosesEachSessionOnce(t *testing.T) {
	env := NewConformanceEnv(t)
	report := RunSandboxConformance(context.Background(), closeOnceAdapter{Adapter: NewLocalAdapter()}, env)
	if !report.Passed {
		for _, check := range report.FailedChecks() {
			t.Errorf("%s %s: %s", check.ID, check.Name, check.Detail)
		}
		t.Fatal("close-once adapter conformance failed")
	}
}

func TestConformanceEnvReusableAcrossRuns(t *testing.T) {
	env := NewConformanceEnv(t)
	adapter := NewLocalAdapter()
	for run := 1; run <= 2; run++ {
		report := RunSandboxConformance(context.Background(), adapter, env)
		if !report.Passed {
			for _, check := range report.FailedChecks() {
				t.Errorf("run %d %s %s: %s", run, check.ID, check.Name, check.Detail)
			}
			t.Fatalf("run %d failed", run)
		}
	}
}

func TestConformanceClosesSessionsWhenSecondOpenFails(t *testing.T) {
	env := NewConformanceEnv(t)
	closed := false
	adapter := failSecondOpenAdapter{Adapter: NewLocalAdapter(), closed: &closed}
	report := RunSandboxConformance(context.Background(), adapter, env)
	if report.Passed {
		t.Fatal("second Open failure should fail the conformance report")
	}
	if !closed {
		t.Fatal("first session was not closed after second Open failed")
	}
}

type failSecondOpenAdapter struct {
	Adapter
	closed *bool
}

func (a failSecondOpenAdapter) Open(ctx context.Context, request OpenRequest) (Session, error) {
	if request.Role == RoleWorkerB {
		return nil, fail(CodeSandboxFailClosed, "simulated second Open failure")
	}
	session, err := a.Adapter.Open(ctx, request)
	if err != nil {
		return nil, err
	}
	return &closeRecordingSession{Session: session, closed: a.closed}, nil
}

type closeRecordingSession struct {
	Session
	closed *bool
}

func (s *closeRecordingSession) Close() error {
	*s.closed = true
	return s.Session.Close()
}

type closeOnceAdapter struct {
	Adapter
}

func (a closeOnceAdapter) Open(ctx context.Context, request OpenRequest) (Session, error) {
	session, err := a.Adapter.Open(ctx, request)
	if err != nil {
		return nil, err
	}
	return &closeOnceSession{Session: session}, nil
}

type closeOnceSession struct {
	Session
	closed bool
}

func (s *closeOnceSession) Close() error {
	if s.closed {
		return errors.New("session closed more than once")
	}
	s.closed = true
	return s.Session.Close()
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

func TestAuditEventGoldenPayloadAndDigest(t *testing.T) {
	event := SandboxAuditEvent{
		Sequence:        1,
		EventType:       EventSandboxOpen,
		ContractVersion: ContractVersion,
		BackendName:     LocalBackendName,
		BackendVersion:  LocalBackendVersion,
		PolicyVersion:   1,
		PolicyDigest:    "sha256:policy",
		Decision:        DecisionAccept,
		WorkItem:        "work-item",
		Role:            RoleWorkerA,
		TraceID:         "trace-id",
		Cycle:           1,
		Action:          "open",
		Target:          "worker<&>",
		PreviousDigest:  emptyDigest(),
	}
	const wantPayload = `{"sequence":1,"event_type":"sandbox.open","contract_version":"jobsite-sandbox/v1","backend_name":"local-test-adapter","backend_version":"jobsite-sandbox/v1","policy_version":1,"policy_digest":"sha256:policy","decision":"accept","work_item":"work-item","role":"worker-a","trace_id":"trace-id","cycle":1,"action":"open","target":"worker\u003c\u0026\u003e","previous_digest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}`
	const wantDigest = "sha256:ea9df8a30df72dbf3b74140d7539a59284daa89a9fdfe878ff28f0201dce155b"

	payload, err := marshalSandboxAuditPayload(event)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != wantPayload {
		t.Fatalf("payload = %s\nwant %s", payload, wantPayload)
	}
	digest, err := digestSandboxEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if digest != wantDigest {
		t.Fatalf("digest = %s, want %s", digest, wantDigest)
	}
}

func TestMemoryLimitedOutputHardCap(t *testing.T) {
	output := newMemoryLimitedOutput(4)
	if _, err := output.stdoutWriter().Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-output.exceededChannel():
		t.Fatal("output marked exceeded at the limit")
	default:
	}
	if _, err := output.stderrWriter().Write([]byte("x")); err == nil {
		t.Fatal("write beyond memory limit succeeded")
	}
	select {
	case <-output.exceededChannel():
	default:
		t.Fatal("memory-limit channel was not closed")
	}
	if got := len(output.stdoutBytes()); got != 4 {
		t.Fatalf("retained stdout = %d bytes, want 4", got)
	}
	if got := len(output.stderrBytes()); got != 0 {
		t.Fatalf("retained stderr = %d bytes, want 0", got)
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

func TestOpenRejectsProjectionRoleMismatch(t *testing.T) {
	env := NewConformanceEnv(t)
	request := env.OpenRequest(RoleWorkerA)
	request.Projection = env.OpenRequest(RoleWorkerB).Projection
	_, err := NewLocalAdapter().Open(context.Background(), request)
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s for projection role mismatch, got %v", CodeSandboxDenied, err)
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
		{"show", "../outside:file"},
		{"show", ".git:config"},
		{"cat-file", "-e", "/etc/passwd"},
		{"cat-file", "--batch-all-objects"},
		{"cat-file", "--batch"},
		{"show", "HEAD:../outside"},
		{"cat-file", "-p", "HEAD:../outside"},
		{"show", "HEAD:.git/config"},
		{"show", "HEAD:\\escaped"},
		{"status", "/etc"},
		{"remote", "add", "origin", "http://evil.com"},
		{"log", "--pretty=format:%(trailers)"},
		{"log", "--format=%x00"},
		{"show", "--pretty=format:%(trailers:key=Signed-off-by)"},
		{"show", "--format=%(trailers)"},
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

func TestImmediateCloseDuringExecute(t *testing.T) {
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(context.Background(), env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := session.Execute(context.Background(), ExecRequest{
			Executable: "sleep",
			Args:       []string{"30"},
		})
		done <- err
	}()

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if errorCode(err) != CodeSandboxCancelled {
			t.Fatalf("expected %s after immediate Close(), got %v", CodeSandboxCancelled, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("immediate Close() deadlocked with Execute()")
	}
}

func TestConformanceAcceptsCPULimitOnFirstExecution(t *testing.T) {
	env := NewConformanceEnv(t)
	report := RunSandboxConformance(context.Background(), firstCPULimitAdapter{Adapter: NewLocalAdapter()}, env)
	if !report.Passed {
		for _, check := range report.FailedChecks() {
			t.Errorf("%s %s: %s", check.ID, check.Name, check.Detail)
		}
		t.Fatal("conformance should accept a CPU limit on the first execution")
	}
}

type firstCPULimitAdapter struct {
	Adapter
}

func (a firstCPULimitAdapter) Open(ctx context.Context, request OpenRequest) (Session, error) {
	session, err := a.Adapter.Open(ctx, request)
	if err != nil {
		return nil, err
	}
	local, ok := session.(*localSession)
	if !ok {
		return nil, fail(CodeSandboxFailClosed, "test requires the local session")
	}
	return &firstCPULimitSession{localSession: local}, nil
}

type firstCPULimitSession struct {
	*localSession
	limited bool
}

func (s *firstCPULimitSession) Execute(ctx context.Context, request ExecRequest) (ExecResult, error) {
	if request.Executable == BurnName && !s.limited {
		s.limited = true
		s.mu.Lock()
		defer s.mu.Unlock()
		return ExecResult{Denied: true, Status: -1}, s.limitLocked("cpu", "20ms", "CPU limit exceeded.")
	}
	return s.localSession.Execute(ctx, request)
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

func TestWallClockZeroLimitExceeded(t *testing.T) {
	env := NewConformanceEnv(t)
	req := env.OpenRequest(RoleWorkerA)
	req.Limits.WallClock = 0
	session, err := NewLocalAdapter().Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	_, err = session.Git(context.Background(), "status")
	if errorCode(err) != CodeSandboxLimit {
		t.Fatalf("expected %s on WallClock == 0, got %v", CodeSandboxLimit, err)
	}

	foundWallLimit := false
	for _, ev := range session.Audit() {
		if ev.EventType == EventSandboxLimit && ev.LimitName == "wall-clock" {
			foundWallLimit = true
			break
		}
	}
	if !foundWallLimit {
		t.Fatalf("expected EventSandboxLimit with LimitName wall-clock in audit log")
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

func TestRepeatedNewFileWritePreservesCreateAndAppliesPatch(t *testing.T) {
	ctx := context.Background()
	env := NewConformanceEnv(t)
	adapter := NewLocalAdapter()
	session, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	newPath := "tests/canonical/repeated_new_test.go"
	content1 := []byte("package canonical\n// first write\n")
	content2 := []byte("package canonical\n// second write with update\n")

	if err := session.WritePath(ctx, newPath, content1, ModeFile); err != nil {
		t.Fatalf("first WritePath failed: %v", err)
	}
	if err := session.WritePath(ctx, newPath, content2, ModeFile); err != nil {
		t.Fatalf("second WritePath failed: %v", err)
	}

	bundle, err := session.CollectPatch(ctx)
	if err != nil {
		t.Fatalf("CollectPatch failed: %v", err)
	}

	found := false
	for _, op := range bundle.Operations {
		if op.Path == newPath {
			found = true
			if op.Action != ActionCreate {
				t.Fatalf("expected action %q for repeated write of new file, got %q", ActionCreate, op.Action)
			}
			if !bytes.Equal(op.Content, content2) {
				t.Fatalf("expected content %q, got %q", string(content2), string(op.Content))
			}
		}
	}
	if !found {
		t.Fatalf("operation for %q not found in collected patch", newPath)
	}

	applied, err := ApplyPatch(env.Export.Integration, env.Export.Policy, bundle)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}
	if applied.Decision.Decision != DecisionAccept {
		t.Fatalf("expected DecisionAccept, got %q (reason: %s)", applied.Decision.Decision, applied.Decision.RejectionReason)
	}
}

func TestSymlinkReadAndWriteDenied(t *testing.T) {
	ctx := context.Background()
	env := NewConformanceEnv(t)
	adapter := NewLocalAdapter()
	session, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	// Symlink redirecting within sandbox
	targetInternal := filepath.Join(session.Root(), "tests", "canonical", "canonical_test.go")
	linkInternal := filepath.Join(session.Root(), "tests", "canonical", "internal_symlink.go")
	if err := os.Symlink(targetInternal, linkInternal); err != nil {
		t.Fatal(err)
	}

	// Symlink escaping sandbox
	linkEscape := filepath.Join(session.Root(), "tests", "canonical", "escape_symlink.go")
	if err := os.Symlink("/etc/passwd", linkEscape); err != nil {
		t.Fatal(err)
	}

	// Deny read through internal symlink
	_, err = session.ReadPath(ctx, "tests/canonical/internal_symlink.go")
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s reading internal symlink, got %v", CodeSandboxDenied, err)
	}

	// Deny write through internal symlink
	err = session.WritePath(ctx, "tests/canonical/internal_symlink.go", []byte("evil"), ModeFile)
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s writing internal symlink, got %v", CodeSandboxDenied, err)
	}

	// Deny read through escape symlink
	_, err = session.ReadPath(ctx, "tests/canonical/escape_symlink.go")
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s reading escape symlink, got %v", CodeSandboxDenied, err)
	}

	// Deny write through escape symlink
	err = session.WritePath(ctx, "tests/canonical/escape_symlink.go", []byte("evil"), ModeFile)
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected %s writing escape symlink, got %v", CodeSandboxDenied, err)
	}
}

func TestMemoryLimitsOversizeOperations(t *testing.T) {
	ctx := context.Background()
	env := NewConformanceEnv(t)
	adapter := NewLocalAdapter()

	// Oversize read
	reqRead := env.OpenRequest(RoleWorkerA)
	reqRead.Limits.MemoryBytes = 4
	sRead, err := adapter.Open(ctx, reqRead)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sRead.Close() }()
	_, err = sRead.ReadPath(ctx, sharedDocPath)
	if errorCode(err) != CodeSandboxLimit {
		t.Fatalf("expected CodeSandboxLimit on oversize read, got %v", err)
	}

	// Oversize Git output
	reqGit := env.OpenRequest(RoleWorkerA)
	reqGit.Limits.MemoryBytes = 4
	sGit, err := adapter.Open(ctx, reqGit)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sGit.Close() }()
	_, err = sGit.Git(ctx, "log", "-n", "1")
	if errorCode(err) != CodeSandboxLimit {
		t.Fatalf("expected CodeSandboxLimit on oversize Git output, got %v", err)
	}

	// Oversize network response
	reqNet := env.OpenRequest(RoleWorkerA)
	reqNet.Limits.MemoryBytes = 4
	sNet, err := adapter.Open(ctx, reqNet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sNet.Close() }()
	_, err = sNet.Execute(ctx, ExecRequest{
		Executable: ApprovedFetchName,
		Digest:     ApprovedFetchDigest,
		Network: &NetworkRequest{
			Destination: AllowedDestination,
			Protocol:    AllowedProtocol,
			Method:      AllowedMethod,
			Path:        AllowedNetworkPath,
		},
	})
	if errorCode(err) != CodeSandboxLimit {
		t.Fatalf("expected CodeSandboxLimit on oversize network response, got %v", err)
	}
}

func TestSentinelInNetworkDestinationAndPathAbsentFromAudit(t *testing.T) {
	ctx := context.Background()
	env := NewConformanceEnv(t)
	adapter := NewLocalAdapter()
	session, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	sentinelDest := "secret-dest-sentinel.example.invalid"
	sentinelPath := "/v1/secret-path-sentinel"

	_, err = session.Execute(ctx, ExecRequest{
		Executable: ApprovedFetchName,
		Digest:     ApprovedFetchDigest,
		Network: &NetworkRequest{
			Destination: AllowedDestination,
			Protocol:    AllowedProtocol,
			Method:      AllowedMethod,
			Path:        AllowedNetworkPath,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	foundAcceptedNetwork := false
	for _, event := range session.Audit() {
		if event.EventType != EventSandboxNetwork || event.Decision != DecisionAccept {
			continue
		}
		foundAcceptedNetwork = true
		if !strings.HasPrefix(event.Target, "network-rule:") ||
			strings.Contains(event.Target, AllowedDestination) ||
			strings.Contains(event.Target, AllowedNetworkPath) {
			t.Fatalf("accepted network audit target leaked rule values: %q", event.Target)
		}
	}
	if !foundAcceptedNetwork {
		t.Fatal("missing accepted network audit event")
	}

	// Call with sentinel destination
	_, _ = session.Execute(ctx, ExecRequest{
		Executable: ApprovedFetchName,
		Digest:     ApprovedFetchDigest,
		Network: &NetworkRequest{
			Destination: sentinelDest,
			Protocol:    AllowedProtocol,
			Method:      AllowedMethod,
			Path:        AllowedNetworkPath,
		},
	})

	// Call with sentinel path
	_, _ = session.Execute(ctx, ExecRequest{
		Executable: ApprovedFetchName,
		Digest:     ApprovedFetchDigest,
		Network: &NetworkRequest{
			Destination: AllowedDestination,
			Protocol:    AllowedProtocol,
			Method:      AllowedMethod,
			Path:        sentinelPath,
		},
	})

	for _, ev := range session.Audit() {
		if auditEventContainsSentinel(ev, sentinelDest) {
			t.Fatalf("sentinel destination leaked into audit field: %#v", ev)
		}
		if auditEventContainsSentinel(ev, sentinelPath) {
			t.Fatalf("sentinel path leaked into audit field: %#v", ev)
		}
	}
}

func TestSentinelInCallerControlledInputsAbsentFromAudit(t *testing.T) {
	ctx := context.Background()
	env := NewConformanceEnv(t)
	session, err := NewLocalAdapter().Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	sentinel := "audit-secret-sentinel"
	filesystemPath := "tests/canonical/" + sentinel + ".go"
	_, _ = session.ReadPath(ctx, filesystemPath)
	_ = session.WritePath(ctx, filesystemPath, []byte("x"), ModeFile)
	_, _ = session.Execute(ctx, ExecRequest{Executable: sentinel})
	_, _ = session.Git(ctx, "show", sentinel+":../outside")

	for _, event := range session.Audit() {
		if auditEventContainsSentinel(event, sentinel) {
			t.Fatalf("caller-controlled sentinel leaked into audit field: %#v", event)
		}
	}
}

func TestReadPathNonExistentRecordsAuditDenial(t *testing.T) {
	ctx := context.Background()
	env := NewConformanceEnv(t)
	adapter := NewLocalAdapter()
	session, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	nonExistentPath := "tests/canonical/nonexistent_file.go"
	_, err = session.ReadPath(ctx, nonExistentPath)
	if errorCode(err) != CodeSandboxDenied {
		t.Fatalf("expected CodeSandboxDenied for non-existent path, got %v", err)
	}

	foundDenial := false
	for _, ev := range session.Audit() {
		if ev.EventType == EventSandboxRead &&
			ev.Decision == DecisionReject &&
			ev.Action == ActionDeny &&
			ev.Target == filesystemAuditTarget(nonExistentPath) &&
			ev.RejectionReason == "Sandbox path does not exist in the projection." {
			foundDenial = true
			break
		}
	}
	if !foundDenial {
		t.Fatalf("missing audit denial event for non-existent path read")
	}
}

func TestConformanceReportAllPassedAndFailedChecks(t *testing.T) {
	report := &ConformanceReport{
		Checks: []CheckResult{
			{ID: "C1", Name: "Check 1", Passed: true},
			{ID: "C2", Name: "Check 2", Passed: false, Detail: "failed check"},
		},
	}
	if report.allPassed() {
		t.Fatal("expected allPassed() to return false when check failed")
	}
	failed := report.FailedChecks()
	if len(failed) != 1 || failed[0].ID != "C2" {
		t.Fatalf("expected FailedChecks() to return [C2], got %#v", failed)
	}

	emptyReport := &ConformanceReport{}
	if emptyReport.allPassed() {
		t.Fatal("expected allPassed() to return false for empty checks")
	}

	allPassReport := &ConformanceReport{
		Checks: []CheckResult{
			{ID: "C1", Name: "Check 1", Passed: true},
			{ID: "C2", Name: "Check 2", Passed: true},
		},
	}
	if !allPassReport.allPassed() {
		t.Fatal("expected allPassed() to return true when all checks pass")
	}
	if len(allPassReport.FailedChecks()) != 0 {
		t.Fatalf("expected no failed checks, got %#v", allPassReport.FailedChecks())
	}
}
