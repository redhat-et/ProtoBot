package jobsite

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

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
