package jobsite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

type trackingSession struct {
	name string
	Session
	closed bool
}

type trackingAdapter struct {
	Adapter
	mu       sync.Mutex
	sessions []*trackingSession
	counts   map[string]int
}

func newTrackingAdapter(inner Adapter) *trackingAdapter {
	return &trackingAdapter{
		Adapter: inner,
		counts:  make(map[string]int),
	}
}

func (t *trackingAdapter) Open(ctx context.Context, req OpenRequest) (Session, error) {
	s, err := t.Adapter.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[req.Role]++
	name := fmt.Sprintf("%s-%d", req.Role, t.counts[req.Role])
	tracked := &trackingSession{name: name, Session: s}
	t.sessions = append(t.sessions, tracked)
	return tracked, nil
}

func (s *trackingSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.Session.Close()
}

func aggregateAuditHead(heads map[string]string) string {
	if len(heads) == 0 {
		return ""
	}
	keys := make([]string, 0, len(heads))
	for k := range heads {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(heads[k])
		b.WriteString("\n")
	}
	return digestBytes([]byte(b.String()))
}

func verifySessionAudit(s Session) error {
	events := s.Audit()
	if len(events) == 0 {
		return fmt.Errorf("session has no audit events")
	}
	if err := VerifyAuditChain(events); err != nil {
		return fmt.Errorf("audit chain invalid: %w", err)
	}
	for _, ev := range events {
		if ev.ContractVersion != ContractVersion {
			return fmt.Errorf("audit contract_version %q != %q", ev.ContractVersion, ContractVersion)
		}
		if ev.BackendName == "" {
			return fmt.Errorf("audit missing backend_name")
		}
		if ev.BackendVersion == "" {
			return fmt.Errorf("audit missing backend_version")
		}
		if ev.PolicyVersion == 0 {
			return fmt.Errorf("audit missing policy_version")
		}
		if ev.PolicyDigest == "" {
			return fmt.Errorf("audit missing policy_digest")
		}
		if ev.WorkItem == "" {
			return fmt.Errorf("audit missing work_item")
		}
		if ev.Role == "" {
			return fmt.Errorf("audit missing role")
		}
		if ev.TraceID == "" {
			return fmt.Errorf("audit missing trace_id")
		}
		if ev.Sequence <= 0 {
			return fmt.Errorf("audit invalid sequence %d", ev.Sequence)
		}
		if ev.PreviousDigest == "" {
			return fmt.Errorf("audit missing previous_digest")
		}
		if ev.EventDigest == "" {
			return fmt.Errorf("audit missing event_digest")
		}
		if ev.EventType == "" {
			return fmt.Errorf("audit missing event_type")
		}
		if ev.Decision == "" {
			return fmt.Errorf("audit missing decision")
		}
		if ev.Action == "" {
			return fmt.Errorf("audit missing action")
		}
		if ev.Target == "" {
			return fmt.Errorf("audit missing target")
		}
	}
	return nil
}

// RunSandboxConformance executes the backend-neutral v1 sandbox conformance suite.
// A conformance runner in this module calls it; it does not require
// testing.T so a command-line runner can invoke it.
// This suite validates sandbox adapter behavior at the API boundary; this suite
// alone is insufficient to enable autonomous execution until an integration-level
// check exercises host/peer access from inside the Worker execution boundary.
// Reports leave integration_boundary_verified and autonomous_execution false.
func RunSandboxConformance(ctx context.Context, adapter Adapter, env *ConformanceEnv) *ConformanceReport {
	report := &ConformanceReport{
		ContractVersion:   ContractVersion,
		Backend:           adapter.Identity(),
		PolicyVersion:     env.Export.Policy.Version,
		PolicyDigest:      env.Export.Policy.Digest,
		ExecutableDigests: map[string]string{ApprovedFetchName: ApprovedFetchDigest, BurnName: BurnDigest},
		ResourceLimits:    env.Limits,
	}
	if missing := missingRequiredCapabilities(adapter.Capabilities()); len(missing) > 0 {
		report.AutonomousExecution = false
		report.FailClosedReason = fmt.Sprintf("Missing capabilities %s; autonomous execution disabled.", strings.Join(missing, ", "))
		report.add(CheckResult{ID: "SB-FC-001", Name: "Missing capability disables autonomous execution", Passed: true, Detail: report.FailClosedReason})
		report.Passed = false
		return report
	}

	tracker := newTrackingAdapter(adapter)
	sessionA, err := tracker.Open(ctx, env.OpenRequest(RoleWorkerA))
	if err != nil {
		report.failClosed("SB-FC-001", err)
		return report
	}
	sessionB, err := tracker.Open(ctx, env.OpenRequest(RoleWorkerB))
	if err != nil {
		closeTrackedSessions(report, tracker)
		report.failClosed("SB-FC-001", err)
		return report
	}

	runChecks(ctx, report, tracker, env, sessionA, sessionB)

	closeTrackedSessions(report, tracker)

	// Verify every session chain and required field, and report per-session heads and aggregate head.
	report.AuditHeads = make(map[string]string)
	for _, ts := range tracker.sessions {
		if err := verifySessionAudit(ts); err != nil {
			report.add(CheckResult{ID: "SB-AUD-002", Name: "Session audit chain: " + ts.name, Passed: false, Detail: err.Error()})
		}
		events := ts.Audit()
		if len(events) > 0 {
			report.AuditHeads[ts.name] = events[len(events)-1].EventDigest
		}
	}
	report.AuditHead = aggregateAuditHead(report.AuditHeads)

	report.Passed = report.allPassed()
	report.IntegrationBoundaryVerified = false
	report.AutonomousExecution = report.Passed && report.IntegrationBoundaryVerified
	if !report.Passed {
		report.AutonomousExecution = false
		if report.FailClosedReason == "" {
			report.FailClosedReason = "Conformance check failed; autonomous execution disabled."
		}
	} else {
		report.FailClosedReason = "Integration-level Worker boundary checks have not run; autonomous execution disabled."
	}
	return report
}

func closeTrackedSessions(report *ConformanceReport, tracker *trackingAdapter) {
	for _, ts := range tracker.sessions {
		if err := ts.Close(); err != nil {
			report.add(CheckResult{ID: "SB-LIFE-003", Name: "Session cleanup: " + ts.name, Passed: false, Detail: err.Error()})
		}
	}
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
	report.check("SB-FS-012", "Relative traversal paths are denied", func() error {
		targets := []string{
			filepath.Join(sessionB.Root(), currentImplPath),
			env.Source.Root,
			env.Export.Integration.Path,
		}
		for _, target := range targets {
			relative, err := filepath.Rel(sessionA.Root(), target)
			if err != nil {
				return err
			}
			_, readErr := sessionA.ReadPath(ctx, relative)
			if err := expectDenied(readErr); err != nil {
				return fmt.Errorf("read %s: %w", relative, err)
			}
			if err := expectDenied(sessionA.WritePath(ctx, relative, []byte("x"), ModeFile)); err != nil {
				return fmt.Errorf("write %s: %w", relative, err)
			}
		}
		return nil
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
		integrationParent, err := os.MkdirTemp("", "jobsite-sandbox-integration-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(integrationParent) }()
		integration, err := exportIntegration(env.Source.Root, env.Export.SourceCommit, filepath.Join(integrationParent, integrationDir))
		if err != nil {
			return err
		}
		applied, err := ApplyPatch(*integration, env.Export.Policy, bundle)
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
			return fmt.Errorf("worker A cat-file of implementation blob succeeded")
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
			return fmt.Errorf("worker B cat-file of test blob succeeded")
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
		integrationGitDir := filepath.Join(env.Export.Integration.Path, ".git")
		probes := [][]string{
			{"fetch", env.Export.Integration.Path},
			{"-c", "protocol.file.allow=always", "fetch", env.Export.Integration.Path},
			{"push", env.Export.Integration.Path, "main"},
			{"-c", "protocol.file.allow=always", "push", env.Export.Integration.Path, "main"},
			{"pull", env.Export.Integration.Path},
			{"clone", env.Export.Integration.Path},
			{"ls-remote", env.Export.Integration.Path},
			{"remote", "add", "origin", env.Export.Integration.Path},
			{"--git-dir=" + integrationGitDir, "cat-file", "-e", env.Source.HistoricalImplBlob},
			{"--git-dir", integrationGitDir, "cat-file", "-e", env.Source.HistoricalImplBlob},
			{"diff", "--no-index", "--", "/etc/passwd", "/dev/null"},
			{"log", "--output=/tmp/leak.txt"},
			{"log", "--output", "/tmp/leak.txt"},
			{"--config-env=foo=BAR", "status"},
			{"cat-file", "--batch-all-objects"},
			{"cat-file", "--batch"},
			{"cat-file", "-p", "HEAD:../outside"},
		}
		for _, args := range probes {
			res, err := sessionA.Git(ctx, args...)
			if errorCode(err) != CodeSandboxDenied && !res.Denied {
				return fmt.Errorf("isolation probe %v was not denied: err=%v, res=%+v", args, err, res)
			}
			if bytes.Contains(res.Stdout, []byte("root:")) {
				return fmt.Errorf("isolation probe %v leaked host file contents", args)
			}
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
		if res.Status != 200 {
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
		return expectNetworkDenial(sessionA, err, "network:unapproved-executable")
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
		return expectNetworkDenial(sessionA, err, "network:default-deny")
	})
	report.check("SB-NET-004", "Direct IP destinations are denied", func() error {
		for _, dest := range []string{"127.0.0.1", "::1", "[::1]", "127.1"} {
			_, err := sessionA.Execute(ctx, ExecRequest{
				Executable: ApprovedFetchName,
				Digest:     ApprovedFetchDigest,
				Network: &NetworkRequest{
					Destination: dest,
					Protocol:    AllowedProtocol,
					Method:      AllowedMethod,
					Path:        AllowedNetworkPath,
				},
			})
			if err := expectNetworkDenial(sessionA, err, "network:ip-literal"); err != nil {
				return fmt.Errorf("direct IP %q was not denied: %w", dest, err)
			}
		}
		return nil
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
		return expectNetworkDenial(sessionA, err, "network:dns-tunneling")
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
		defer func() { _ = session.Close() }()
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
		return expectNetworkDenial(session, err, "network:redirect")
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
		return expectNetworkDenial(sessionA, err, "network:raw-socket")
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
		return expectNetworkDenial(sessionA, err, "network:alternate-port")
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
		return expectNetworkDenial(sessionA, err, "network:tls-bypass")
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
		return expectNetworkDenial(sessionA, err, "network:default-deny")
	})

	report.check("SB-CRED-001", "Broker token is absent from the sandbox environment", func() error {
		session, err := adapter.Open(ctx, env.OpenRequest(RoleWorkerA))
		if err != nil {
			return err
		}
		defer func() { _ = session.Close() }()
		envVars := session.Environ()
		if envVars == nil {
			return fmt.Errorf("environment inspection is unavailable on adapter")
		}
		if containsSentinel(envVars...) {
			return fmt.Errorf("broker token present in sandbox environment")
		}
		for _, entry := range envVars {
			key, _, _ := strings.Cut(entry, "=")
			if !isAllowlistedEnvVar(key) {
				return fmt.Errorf("disallowed environment variable %q present in sandbox environment", key)
			}
		}
		return nil
	})
	report.check("SB-CRED-002", "Broker token is absent from sandbox files", func() error {
		return inspectSessionFiles(sessionA, env.Sentinel)
	})
	report.check("SB-CRED-003", "Sentinel argument is absent from audit", func() error {
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
		if err != nil && errorCode(err) != CodeSandboxDenied {
			return err
		}
		for _, ev := range sessionA.Audit() {
			if containsSentinel(ev.Target, ev.RejectionReason, ev.Action, ev.Executable) {
				return fmt.Errorf("credential sentinel in audit of process arguments")
			}
		}
		_, err = sessionA.Execute(ctx, ExecRequest{
			Executable: "sleep",
			Args:       []string{env.Sentinel},
		})
		if err == nil {
			return fmt.Errorf("expected denial when sentinel is passed in process arguments")
		}
		if errorCode(err) != CodeSandboxDenied {
			return fmt.Errorf("expected %s for sentinel process argument, got %v", CodeSandboxDenied, err)
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
		// Probe with sentinel in destination
		_, _ = sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: env.Sentinel,
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        AllowedNetworkPath,
			},
		})
		// Probe with sentinel in path
		_, _ = sessionA.Execute(ctx, ExecRequest{
			Executable: ApprovedFetchName,
			Digest:     ApprovedFetchDigest,
			Network: &NetworkRequest{
				Destination: AllowedDestination,
				Protocol:    AllowedProtocol,
				Method:      AllowedMethod,
				Path:        "/v1/" + env.Sentinel,
			},
		})
		for _, ev := range sessionA.Audit() {
			if auditEventContainsSentinel(ev, env.Sentinel) || auditEventContainsSentinel(ev, sandboxBrokerToken) {
				return fmt.Errorf("credential sentinel in audit record field: %#v", ev)
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
		defer func() { _ = session.Close() }()
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
				return fmt.Errorf("expected %s or context.Canceled, got %v", CodeSandboxCancelled, err)
			}
		case <-time.After(5 * time.Second):
			_ = session.Cancel(ctx)
			return fmt.Errorf("spawned process did not terminate")
		}
		return session.Cancel(ctx)
	})
	report.check("SB-LIFE-002", "Lease expiry disables further execution and records the limit", func() error {
		req := env.OpenRequest(RoleWorkerA)
		req.Lease = 20 * time.Millisecond
		session, err := adapter.Open(ctx, req)
		if err != nil {
			return err
		}
		defer func() { _ = session.Close() }()
		time.Sleep(40 * time.Millisecond)
		err = session.WritePath(ctx, currentTestPath, []byte("late\n"), ModeFile)
		if errorCode(err) != CodeSandboxLimit && errorCode(err) != CodeSandboxCancelled {
			return fmt.Errorf("lease expiry error = %v", err)
		}
		if _, err := session.Git(ctx, "status"); errorCode(err) != CodeSandboxLimit && errorCode(err) != CodeSandboxCancelled {
			return fmt.Errorf("lease expiry git error = %v", err)
		}
		found := false
		for _, ev := range session.Audit() {
			if ev.EventType == EventSandboxLimit && ev.LimitName == "lease" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("lease limit was not recorded in audit")
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
		defer func() { _ = second.Close() }()
		if second.Root() == root {
			return fmt.Errorf("second sandbox reused the first root")
		}
		_, err = second.ReadPath(ctx, "tests/canonical/secret_scratch.go")
		if err == nil {
			return fmt.Errorf("prior scratch file read succeeded unexpectedly")
		}
		if errorCode(err) != CodeSandboxDenied && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("expected not-found or sandbox.denied, got: %w", err)
		}
		return nil
	})

	report.check("SB-LIM-001", "Wall-clock limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.WallClock = 20 * time.Millisecond
			req.Lease = 0
		}, func(session Session) error {
			time.Sleep(40 * time.Millisecond)
			err := session.WritePath(ctx, currentTestPath, []byte("late\n"), ModeFile)
			if err := expectCode(err, CodeSandboxLimit, CodeSandboxCancelled); err != nil {
				return err
			}
			_, err = session.Git(ctx, "status")
			return expectCode(err, CodeSandboxLimit, CodeSandboxCancelled)
		}, "wall-clock")
	})
	report.check("SB-LIM-002", "CPU limit is enforced and recorded", func() error {
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.CPU = 20 * time.Millisecond
		}, func(session Session) error {
			res, err := session.Execute(ctx, ExecRequest{Executable: BurnName, Digest: BurnDigest, Args: []string{"30ms"}})
			if errorCode(err) == CodeSandboxLimit {
				return nil
			}
			if err != nil {
				return fmt.Errorf("first command failed: %w", err)
			}
			if res.Status != 0 {
				return fmt.Errorf("first command status = %d", res.Status)
			}
			_, err = session.Execute(ctx, ExecRequest{Executable: BurnName, Digest: BurnDigest, Args: []string{"0"}})
			if err := expectCode(err, CodeSandboxLimit); err != nil {
				return err
			}
			return nil
		}, "cpu")
	})
	report.check("SB-LIM-003", "Memory limit is enforced and recorded", func() error {
		// Oversize write
		if err := checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.MemoryBytes = 4
		}, func(session Session) error {
			err := session.WritePath(ctx, currentTestPath, []byte("too-large-payload\n"), ModeFile)
			return expectCode(err, CodeSandboxLimit)
		}, "memory"); err != nil {
			return err
		}
		// Oversize read
		if err := checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.MemoryBytes = 4
		}, func(session Session) error {
			_, err := session.ReadPath(ctx, sharedDocPath)
			return expectCode(err, CodeSandboxLimit)
		}, "memory"); err != nil {
			return err
		}
		// Oversize Git output
		if err := checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.MemoryBytes = 4
		}, func(session Session) error {
			_, err := session.Git(ctx, "log", "-n", "1")
			return expectCode(err, CodeSandboxLimit)
		}, "memory"); err != nil {
			return err
		}
		// Oversize network response
		return checkLimit(ctx, adapter, env, func(req *OpenRequest) {
			req.Limits.MemoryBytes = 4
		}, func(session Session) error {
			_, err := session.Execute(ctx, ExecRequest{
				Executable: ApprovedFetchName,
				Digest:     ApprovedFetchDigest,
				Network: &NetworkRequest{
					Destination: AllowedDestination,
					Protocol:    AllowedProtocol,
					Method:      AllowedMethod,
					Path:        AllowedNetworkPath,
				},
			})
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
	report.check("SB-FC-004", "Projection role must match the session role", func() error {
		request := env.OpenRequest(RoleWorkerA)
		request.Projection = env.OpenRequest(RoleWorkerB).Projection
		_, err := adapter.Open(ctx, request)
		return expectDenied(err)
	})
}

func auditEventContainsSentinel(ev SandboxAuditEvent, sentinel string) bool {
	fields := []string{
		ev.ContractVersion, ev.BackendName, ev.BackendVersion,
		ev.PolicyDigest, ev.WorkItem, ev.Role, ev.TraceID,
		ev.PreviousDigest, ev.EventDigest,
		ev.EventType, ev.Decision, ev.Action, ev.Target,
		ev.RejectionReason, ev.Executable, ev.ExecutableDigest,
		ev.LimitName, ev.LimitConfigured, ev.CleanupResult,
	}
	for _, f := range fields {
		if strings.Contains(f, sentinel) {
			return true
		}
	}
	return false
}

func checkLimit(ctx context.Context, adapter Adapter, env *ConformanceEnv, tweak func(*OpenRequest), op func(Session) error, name string) error {
	req := env.OpenRequest(RoleWorkerA)
	tweak(&req)
	session, err := adapter.Open(ctx, req)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
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
	seen := make(map[string]bool)
	dirs := append([]string{session.Root()}, session.EphemeralDirs()...)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if seen[path] {
				return nil
			}
			seen[path] = true
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), sentinel) {
				return fmt.Errorf("sentinel found in %s", path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func expectDenied(err error) error {
	if err == nil {
		return fmt.Errorf("expected sandbox denial, got success")
	}
	if errorCode(err) != CodeSandboxDenied {
		return fmt.Errorf("expected %s, got %v", CodeSandboxDenied, err)
	}
	return nil
}

func expectNetworkDenial(session Session, err error, target string) error {
	if err := expectDenied(err); err != nil {
		return err
	}
	events := session.Audit()
	if len(events) == 0 {
		return fmt.Errorf("network denial %q was not recorded in audit", target)
	}
	event := events[len(events)-1]
	if event.EventType != EventSandboxNetwork || event.Decision != DecisionReject || event.Target != target {
		return fmt.Errorf("expected network denial %q, got event %#v", target, event)
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
		if !check.Passed {
			return false
		}
	}
	return len(r.Checks) > 0
}

// FailedChecks returns the checks that did not pass.
func (r *ConformanceReport) FailedChecks() []CheckResult {
	var out []CheckResult
	for _, check := range r.Checks {
		if !check.Passed {
			out = append(out, check)
		}
	}
	return out
}

func isAllowlistedEnvVar(key string) bool {
	switch key {
	case "PATH", "LANG", "TZ", "HOME",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM",
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE":
		return true
	default:
		return false
	}
}
