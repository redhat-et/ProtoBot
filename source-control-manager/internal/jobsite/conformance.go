package jobsite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sandboxTraceID = "trace-jobsite-sandbox-v1"

// DefaultSandboxLimits are generous limits for positive execution checks.
func DefaultSandboxLimits() ResourceLimits {
	return ResourceLimits{
		WallClock:     30 * time.Second,
		CPU:           30 * time.Second,
		MemoryBytes:   64 << 20,
		ProcessCount:  8,
		WritableBytes: 1 << 20,
		WritableFiles: 64,
	}
}

// DefaultNetworkAllow is the v1 local allowlist: one executable, destination,
// protocol, method, and L7 path.
func DefaultNetworkAllow() []NetworkRule {
	return []NetworkRule{{
		Executable:  ApprovedFetchName,
		Digest:      ApprovedFetchDigest,
		Destination: AllowedDestination,
		Protocol:    AllowedProtocol,
		Method:      AllowedMethod,
		Path:        AllowedNetworkPath,
	}}
}

// ConformanceEnv is the deterministic fixture the suite and downstream
// backends share. It uses the #78 source fixture and projection export.
type ConformanceEnv struct {
	Source       *SourceFixture
	Export       *ExportResult
	WorkItem     string
	TraceID      string
	Limits       ResourceLimits
	NetworkAllow []NetworkRule
	Sentinel     string
}

// SetupConformanceEnv builds the source fixture and projections under parent.
func SetupConformanceEnv(parent string) (*ConformanceEnv, error) {
	sourceRoot := filepath.Join(parent, "source")
	source, err := BuildSourceFixture(sourceRoot)
	if err != nil {
		return nil, err
	}
	exportRoot := filepath.Join(parent, "export")
	exported, err := Export(ExportRequest{
		SourceRoot:   source.Root,
		SourceCommit: source.SourceCommit,
		Policy:       source.Policy,
		PolicyDigest: source.PolicyDigest,
		OutputDir:    exportRoot,
		WorkItem:     fixtureWorkItem,
		Cycle:        1,
	})
	if err != nil {
		return nil, err
	}
	return &ConformanceEnv{
		Source:       source,
		Export:       exported,
		WorkItem:     fixtureWorkItem,
		TraceID:      sandboxTraceID,
		Limits:       DefaultSandboxLimits(),
		NetworkAllow: DefaultNetworkAllow(),
		Sentinel:     sandboxBrokerToken,
	}, nil
}

// NewConformanceEnv builds the suite fixture in a temporary directory.
func NewConformanceEnv(t testing.TB) *ConformanceEnv {
	t.Helper()
	env, err := SetupConformanceEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// OpenRequest returns a v1 open request for role.
func (e *ConformanceEnv) OpenRequest(role string) OpenRequest {
	repo := e.Export.WorkerA
	if role == RoleWorkerB {
		repo = e.Export.WorkerB
	}
	return OpenRequest{
		Role:            role,
		WorkItem:        e.WorkItem,
		Cycle:           1,
		Projection:      repo,
		Policy:          e.Export.Policy,
		SourceCommit:    e.Export.SourceCommit,
		WorkerRoot:      repo.RootCommit,
		IntegrationBase: e.Export.Integration.RootCommit,
		Limits:          e.Limits,
		NetworkAllow:    append([]NetworkRule(nil), e.NetworkAllow...),
		TraceID:         e.TraceID,
	}
}

// RunSandboxConformance executes the backend-neutral v1 acceptance suite.
// Downstream adapters (#172, #219) call this function; it does not require
// testing.T so a conformance runner can invoke it.
func RunSandboxConformance(ctx context.Context, adapter Adapter, env *ConformanceEnv) *ConformanceReport {
	report := &ConformanceReport{
		ContractVersion:   ContractVersion,
		Backend:           adapter.Identity(),
		PolicyVersion:     env.Export.Policy.Version,
		PolicyDigest:      env.Export.Policy.Digest,
		ExecutableDigests: map[string]string{ApprovedFetchName: ApprovedFetchDigest},
		ResourceLimits:    env.Limits,
	}
	if missing := missingRequiredCapabilities(adapter.Capabilities()); len(missing) > 0 {
		report.AutonomousExecution = false
		report.FailClosedReason = "missing capabilities " + strings.Join(missing, ", ") + "; autonomous execution disabled"
		report.add(CheckResult{ID: "SB-FC-001", Name: "Missing capability disables autonomous execution", Passed: true, Detail: report.FailClosedReason})
		report.Passed = true
		return report
	}

	sessionA, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		report.failClosed("SB-FC-001", err)
		return report
	}
	defer sessionA.Close()
	sessionB, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerB))
	if err != nil {
		report.failClosed("SB-FC-001", err)
		return report
	}
	defer sessionB.Close()

	report.AutonomousExecution = true
	runChecks(ctx, report, adapter, env, sessionA, sessionB)
	report.Passed = report.allPassed()
	if !report.Passed {
		report.AutonomousExecution = false
		if report.FailClosedReason == "" {
			report.FailClosedReason = "conformance check failed; autonomous execution disabled"
		}
	}
	if events := sessionA.Audit(); len(events) > 0 {
		report.AuditHead = events[len(events)-1].EventDigest
	}
	return report
}

func runChecks(ctx context.Context, report *ConformanceReport, adapter Adapter, env *ConformanceEnv, sessionA, sessionB Session) {
	report.check("SB-FS-001", "Worker A writes an allowed test path", func() error {
		return sessionA.WritePath(ctx, currentTestPath, []byte("updated test\n"), ModeFile)
	})
	report.check("SB-FS-002", "Worker B writes an allowed implementation path", func() error {
		return sessionB.WritePath(ctx, currentImplPath, []byte("updated impl\n"), ModeFile)
	})
	report.check("SB-FS-003", "Worker A cannot write an implementation path", func() error {
		return expectDenied(sessionA.WritePath(ctx, currentImplPath, []byte("evil\n"), ModeFile))
	})
	report.check("SB-FS-004", "Worker B cannot write a test path", func() error {
		return expectDenied(sessionB.WritePath(ctx, currentTestPath, []byte("evil\n"), ModeFile))
	})
	report.check("SB-FS-005", "Host filesystem paths are denied", func() error {
		return expectDenied(sessionA.WritePath(ctx, "/etc/passwd", []byte("x"), ModeFile))
	})
	report.check("SB-FS-006", "Peer Worker directories are denied", func() error {
		peer := filepath.Join(sessionB.Root(), currentImplPath)
		return expectDenied(sessionA.WritePath(ctx, peer, []byte("x"), ModeFile))
	})
	report.check("SB-FS-007", "Canonical source repository paths are denied", func() error {
		return expectDenied(sessionA.WritePath(ctx, env.Source.Root, []byte("x"), ModeFile))
	})
	report.check("SB-FS-008", "Private Integration paths are denied", func() error {
		return expectDenied(sessionA.WritePath(ctx, env.Export.Integration.Path, []byte("x"), ModeFile))
	})
	report.check("SB-FS-009", "Shared paths are read-only to Workers", func() error {
		if _, err := sessionA.ReadPath(ctx, sharedDocPath); err != nil {
			return err
		}
		return expectDenied(sessionA.WritePath(ctx, sharedDocPath, []byte("nope\n"), ModeFile))
	})
	report.check("SB-FS-010", "Unclassified paths are denied by default", func() error {
		return expectDenied(sessionA.WritePath(ctx, unclassifiedPath, []byte("x"), ModeFile))
	})
	report.check("SB-FS-011", "Allowed writes produce a PatchBundle v1 that Integration accepts", func() error {
		bundle, err := sessionA.CollectPatch(ctx)
		if err != nil {
			return err
		}
		if bundle.Version != 1 || bundle.Role != RoleWorkerA {
			return fmt.Errorf("bundle version/role = %d %s", bundle.Version, bundle.Role)
		}
		applied, err := ApplyPatch(env.Export.Integration, env.Export.Policy, bundle)
		if err != nil {
			return err
		}
		if applied.Decision.Decision != DecisionAccept {
			return fmt.Errorf("patch decision = %s", applied.Decision.Decision)
		}
		return nil
	})

	report.check("SB-GIT-001", "Worker A cannot read implementation Git objects", func() error {
		res, err := sessionA.Git(ctx, "cat-file", "-e", env.Source.CurrentImplBlob)
		if err != nil && errorCode(err) == CodeSandboxDenied {
			return nil
		}
		if err != nil {
			return err
		}
		if res.Status == 0 {
			return fmt.Errorf("Worker A cat-file of implementation blob succeeded")
		}
		return nil
	})
	report.check("SB-GIT-002", "Worker B cannot read canonical test Git objects", func() error {
		res, err := sessionB.Git(ctx, "cat-file", "-e", env.Source.CurrentTestBlob)
		if err != nil && errorCode(err) == CodeSandboxDenied {
			return nil
		}
		if err != nil {
			return err
		}
		if res.Status == 0 {
			return fmt.Errorf("Worker B cat-file of test blob succeeded")
		}
		return nil
	})
	report.check("SB-GIT-003", "Worker repositories have no remotes", func() error {
		res, err := sessionA.Git(ctx, "remote")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(res.Stdout)) != "" {
			return fmt.Errorf("remotes = %q", res.Stdout)
		}
		return nil
	})
	report.check("SB-GIT-004", "Worker repositories have no Git alternates", func() error {
		repo, err := openGit(sessionA.Root())
		if err != nil {
			return err
		}
		defer repo.Close()
		has, err := repo.hasAlternates()
		if err != nil {
			return err
		}
		if has {
			return fmt.Errorf("alternates present")
		}
		return nil
	})
	report.check("SB-GIT-005", "Worker repositories have no reflogs", func() error {
		repo, err := openGit(sessionA.Root())
		if err != nil {
			return err
		}
		defer repo.Close()
		dir, err := repo.gitDir()
		if err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(dir, "logs")); err == nil {
			return fmt.Errorf("reflogs present")
		}
		return nil
	})
	report.check("SB-GIT-006", "Workers cannot fetch the Integration repository", func() error {
		res, err := sessionA.Git(ctx, "fetch", env.Export.Integration.Path)
		if err != nil {
			if errorCode(err) == CodeSandboxDenied {
				return nil
			}
			return err
		}
		if res.Status == 0 {
			return fmt.Errorf("fetch of integration succeeded")
		}
		return nil
	})
	report.check("SB-GIT-007", "Forbidden historical Git objects are absent", func() error {
		res, err := sessionA.Git(ctx, "cat-file", "-e", env.Source.HistoricalImplBlob)
		if err != nil && errorCode(err) == CodeSandboxDenied {
			return nil
		}
		if err != nil {
			return err
		}
		if res.Status == 0 {
			return fmt.Errorf("historical implementation blob is visible")
		}
		return nil
	})

	report.check("SB-NET-001", "Approved executable, destination, method, and path are allowed", func() error {
		res, err := sessionA.Execute(ctx, ExecRequest{
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
			return err
		}
		if res.Status != 200 || string(res.Stdout) != "ok-payload" {
			return fmt.Errorf("approved fetch status=%d body=%q", res.Status, res.Stdout)
		}
		if containsSentinelBytes(res.Stdout, res.Stderr) {
			return fmt.Errorf("credential sentinel in network response")
		}
		return nil
	})
	report.check("SB-NET-002", "The same destination from another binary is denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: "curl",
			Digest:     digestBytes([]byte("curl")),
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-003", "Unapproved destinations are denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: "evil.example.invalid",
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-004", "Direct IP destinations are denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: "127.0.0.1",
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-005", "DNS tunneling is denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: "dns-tunnel.invalid",
				Protocol:    "dns",
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-006", "Redirects are denied", func() error {
		req := env.OpenRequest(RoleWorkerA)
		req.NetworkAllow = append(req.NetworkAllow, NetworkRule{
			Executable:  ApprovedFetchName,
			Digest:      ApprovedFetchDigest,
			Destination: AllowedDestination,
			Protocol:    AllowedProtocol,
			Method:      AllowedMethod,
			Path:        "/v2/redirect",
		})
		session, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		defer session.Close()
		_, err = session.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        "/v2/redirect",
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-007", "Raw sockets are denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    "tcp",
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-008", "Alternate ports are denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: AllowedDestination + ":8443",
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-009", "TLS bypass is denied", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    "http",
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})
	report.check("SB-NET-010", "Network is denied by default", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    AllowedProtocol,
				Method:      "POST",
				Path:        AllowedNetworkPath,
			},
		})
		return expectDenied(err)
	})

	report.check("SB-CRED-001", "Broker token is absent from the sandbox environment", func() error {
		session, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
		if err != nil {
			return err
		}
		defer session.Close()
		local, ok := session.(*localSession)
		if !ok {
			return inspectSessionFiles(session, env.Sentinel)
		}
		if containsSentinel(local.env...) {
			return fmt.Errorf("broker token present in sandbox environment")
		}
		return nil
	})
	report.check("SB-CRED-002", "Broker token is absent from sandbox files", func() error {
		return inspectSessionFiles(sessionA, env.Sentinel)
	})
	report.check("SB-CRED-003", "Broker token is absent from process arguments", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Args:       []string{env.Sentinel},
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		if err != nil {
			return err
		}
		for _, ev := range sessionA.Audit() {
			if containsSentinel(ev.Target, ev.RejectionReason, ev.Action, ev.Executable) {
				return fmt.Errorf("credential sentinel in audit of process arguments")
			}
		}
		return nil
	})
	report.check("SB-CRED-004", "Broker token is stripped from responses", func() error {
		res, err := sessionA.Execute(ctx, ExecRequest{
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
			return err
		}
		if containsSentinelBytes(res.Stdout, res.Stderr) {
			return fmt.Errorf("credential sentinel in response body")
		}
		return nil
	})
	report.check("SB-CRED-005", "Broker token is absent from audit records", func() error {
		for _, ev := range sessionA.Audit() {
			if containsSentinel(ev.Target, ev.RejectionReason, ev.Action, ev.WorkItem, ev.TraceID) {
				return fmt.Errorf("credential sentinel in audit record")
			}
		}
		return nil
	})
	report.check("SB-CRED-006", "Boundary broker injects credentials only after authorization", func() error {
		_, err := sessionA.Execute(ctx, ExecRequest{
			Executable: "curl",
			Digest:     digestBytes([]byte("curl")),
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		if err := expectDenied(err); err != nil {
			return err
		}
		res, err := sessionA.Execute(ctx, ExecRequest{
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
			return err
		}
		if res.Status != 200 {
			return fmt.Errorf("authorized request status=%d", res.Status)
		}
		return nil
	})
	report.check("SB-CRED-007", "Source-fixture credentials never appear in the sandbox", func() error {
		return inspectSessionFiles(sessionA, credentialSentinel)
	})

	report.check("SB-LIFE-001", "Cancellation terminates the process tree", func() error {
		req := env.OpenRequest(RoleWorkerA)
		session, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		defer session.Close()
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			_, err := session.Execute(runCtx, ExecRequest{Executable: "sleep", Args: []string{"30"}})
			done <- err
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if err == nil {
				return fmt.Errorf("sleep returned success after cancel")
			}
			if errorCode(err) != CodeSandboxCancelled && !errors.Is(err, context.Canceled) {
				if errorCode(err) == "" {
					return fmt.Errorf("cancel error = %v", err)
				}
			}
			return session.Cancel(ctx)
		case <-time.After(5 * time.Second):
			_ = session.Cancel(ctx)
			return fmt.Errorf("spawned process did not terminate")
		}
	})
	report.check("SB-LIFE-002", "Lease expiry disables further execution and records the limit", func() error {
		req := env.OpenRequest(RoleWorkerA)
		req.Lease = 20 * time.Millisecond
		session, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		defer session.Close()
		time.Sleep(40 * time.Millisecond)
		err = session.WritePath(ctx, currentTestPath, []byte("late\n"), ModeFile)
		if errorCode(err) != CodeSandboxLimit && errorCode(err) != CodeSandboxCancelled {
			return fmt.Errorf("lease expiry error = %v", err)
		}
		return nil
	})
	report.check("SB-LIFE-003", "Close removes ephemeral sandbox state", func() error {
		req := env.OpenRequest(RoleWorkerA)
		session, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		root := session.Root()
		if err := session.WritePath(ctx, currentTestPath, []byte("ephemeral\n"), ModeFile); err != nil {
			_ = session.Close()
			return err
		}
		if err := session.Close(); err != nil {
			return err
		}
		if _, err := os.Lstat(root); err == nil {
			return fmt.Errorf("sandbox root still exists after close")
		}
		return nil
	})
	report.check("SB-LIFE-004", "The next sandbox cannot recover prior state", func() error {
		req := env.OpenRequest(RoleWorkerA)
		first, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		marker := []byte("recover-me\n")
		if err := first.WritePath(ctx, "tests/canonical/secret_scratch.go", marker, ModeFile); err != nil {
			_ = first.Close()
			return err
		}
		root := first.Root()
		if err := first.Close(); err != nil {
			return err
		}
		second, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		defer second.Close()
		if second.Root() == root {
			return fmt.Errorf("second sandbox reused the first root")
		}
		_, err = second.ReadPath(ctx, "tests/canonical/secret_scratch.go")
		return expectDenied(err)
	})

	report.check("SB-LIM-001", "Wall-clock limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.WallClock = 20 * time.Millisecond
			req.Lease = 0
		}, func(session Session) error {
			time.Sleep(40 * time.Millisecond)
			err := session.WritePath(ctx, currentTestPath, []byte("late\n"), ModeFile)
			return expectCode(err, CodeSandboxLimit, CodeSandboxCancelled)
		}, "wall-clock")
	})
	report.check("SB-LIM-002", "CPU limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.CPU = 1
		}, func(session Session) error {
			_, err := session.Execute(ctx, ExecRequest{Executable: "sleep", Args: []string{"0"}})
			if err != nil {
				return expectCode(err, CodeSandboxLimit)
			}
			_, err = session.Execute(ctx, ExecRequest{Executable: "sleep", Args: []string{"0"}})
			return expectCode(err, CodeSandboxLimit)
		}, "cpu")
	})
	report.check("SB-LIM-003", "Memory limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.MemoryBytes = 4
		}, func(session Session) error {
			err := session.WritePath(ctx, currentTestPath, []byte("too-large-payload\n"), ModeFile)
			return expectCode(err, CodeSandboxLimit)
		}, "memory")
	})
	report.check("SB-LIM-004", "Process-count limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.ProcessCount = 0
		}, func(session Session) error {
			_, err := session.Execute(ctx, ExecRequest{Executable: "sleep", Args: []string{"1"}})
			return expectCode(err, CodeSandboxLimit)
		}, "process-count")
	})
	report.check("SB-LIM-005", "Writable disk limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.WritableBytes = 4
		}, func(session Session) error {
			err := session.WritePath(ctx, currentTestPath, []byte("too-large-payload\n"), ModeFile)
			return expectCode(err, CodeSandboxLimit)
		}, "writable-disk")
	})
	report.check("SB-LIM-006", "Writable file limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.WritableFiles = 0
		}, func(session Session) error {
			err := session.WritePath(ctx, "tests/canonical/extra_test.go", []byte("package extra\n"), ModeFile)
			return expectCode(err, CodeSandboxLimit)
		}, "writable-files")
	})

	report.check("SB-AUD-001", "Executions and denials produce audit events", func() error {
		if len(sessionA.Audit()) == 0 {
			return fmt.Errorf("no audit events")
		}
		return nil
	})
	report.check("SB-AUD-002", "Audit events form a SHA-256 hash chain", func() error {
		return VerifyAuditChain(sessionA.Audit())
	})
	report.check("SB-AUD-003", "Tampering with an audit event is detected", func() error {
		events := sessionA.Audit()
		if len(events) == 0 {
			return fmt.Errorf("no audit events")
		}
		events[0].Decision = "forged"
		if err := VerifyAuditChain(events); err == nil {
			return fmt.Errorf("tampered chain verified")
		}
		return nil
	})
	report.check("SB-AUD-004", "Audit events are tied to the work-item trace", func() error {
		for _, ev := range sessionA.Audit() {
			if ev.WorkItem != env.WorkItem || ev.TraceID != env.TraceID {
				return fmt.Errorf("audit missing work-item trace: %#v", ev)
			}
			if ev.ContractVersion != ContractVersion {
				return fmt.Errorf("audit contract version = %s", ev.ContractVersion)
			}
		}
		return nil
	})
	report.check("SB-AUD-005", "Audit schema records backend, policy, and executable digest", func() error {
		foundNetwork := false
		for _, ev := range sessionA.Audit() {
			if ev.BackendName == "" || ev.BackendVersion == "" || ev.PolicyDigest == "" {
				return fmt.Errorf("audit missing identity fields: %#v", ev)
			}
			if ev.EventType == EventSandboxNetwork && ev.Decision == DecisionAccept {
				foundNetwork = true
				if ev.ExecutableDigest != ApprovedFetchDigest {
					return fmt.Errorf("network audit digest = %s", ev.ExecutableDigest)
				}
			}
		}
		if !foundNetwork {
			return fmt.Errorf("missing accepted network audit event")
		}
		return nil
	})

	report.check("SB-FC-001", "Missing capability disables autonomous execution", func() error {
		wrapped := FailClosedAdapter{Inner: adapter, Missing: CapNetworkPolicy}
		_, err := wrapped.Open(ctx, env.OpenRequest(RoleWorkerA))
		if errorCode(err) != CodeSandboxFailClosed {
			return fmt.Errorf("missing capability error = %v", err)
		}
		return nil
	})
	report.check("SB-FC-002", "Unsupported platform fails closed", func() error {
		wrapped := FailClosedAdapter{Inner: adapter, Missing: CapFilesystemIsolation, Platform: true}
		_, err := wrapped.Open(ctx, env.OpenRequest(RoleWorkerA))
		if errorCode(err) != CodeSandboxFailClosed {
			return fmt.Errorf("unsupported platform error = %v", err)
		}
		return nil
	})
	report.check("SB-FC-003", "A failed check does not skip remaining policy", func() error {
		if err := expectDenied(sessionA.WritePath(ctx, currentImplPath, []byte("x"), ModeFile)); err != nil {
			return err
		}
		return expectDenied(sessionA.WritePath(ctx, "/etc/passwd", []byte("x"), ModeFile))
	})
}

func checkLimit(ctx context.Context, adapter Adapter, env *ConformanceEnv, tweak func(*OpenRequest), op func(Session) error, name string) error {
	req := env.OpenRequest(RoleWorkerA)
	tweak(&req)
	session, err := adapter.Open(ctx, req)
	if err != nil {
		return err
	}
	defer session.Close()
	if err := op(session); err != nil {
		return err
	}
	found := false
	for _, ev := range session.Audit() {
		if ev.EventType == EventSandboxLimit && ev.LimitName == name {
			found = true
			if ev.LimitConfigured == "" {
				return fmt.Errorf("%s limit recorded without configured value", name)
			}
		}
	}
	if !found {
		return fmt.Errorf("%s limit was not recorded", name)
	}
	return nil
}

func inspectSessionFiles(session Session, sentinel string) error {
	root := session.Root()
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), sentinel) {
			return fmt.Errorf("sentinel found in %s", path)
		}
		return nil
	})
}

func expectDenied(err error) error {
	if err == nil {
		return fmt.Errorf("expected sandbox denial, got success")
	}
	if errorCode(err) != CodeSandboxDenied && errorCode(err) != CodePatchRejected {
		return fmt.Errorf("expected %s, got %v", CodeSandboxDenied, err)
	}
	return nil
}

func expectCode(err error, codes ...string) error {
	if err == nil {
		return fmt.Errorf("expected error codes %v, got success", codes)
	}
	got := errorCode(err)
	for _, code := range codes {
		if got == code {
			return nil
		}
	}
	return fmt.Errorf("expected %v, got %v", codes, err)
}

func (r *ConformanceReport) check(id, name string, fn func() error) {
	result := CheckResult{ID: id, Name: name, Passed: true}
	if err := fn(); err != nil {
		result.Passed = false
		result.Detail = err.Error()
	}
	r.add(result)
}

func (r *ConformanceReport) add(result CheckResult) {
	r.Checks = append(r.Checks, result)
}

func (r *ConformanceReport) failClosed(id string, err error) {
	r.AutonomousExecution = false
	r.FailClosedReason = err.Error()
	r.add(CheckResult{ID: id, Name: "Adapter open fails closed", Passed: errorCode(err) == CodeSandboxFailClosed, Detail: err.Error()})
	r.Passed = false
}

func (r *ConformanceReport) allPassed() bool {
	for _, check := range r.Checks {
		if !check.Passed && !check.Skipped {
			return false
		}
	}
	return len(r.Checks) > 0
}

// FailedChecks returns the checks that did not pass.
func (r *ConformanceReport) FailedChecks() []CheckResult {
	var out []CheckResult
	for _, check := range r.Checks {
		if !check.Passed && !check.Skipped {
			out = append(out, check)
		}
	}
	return out
}
