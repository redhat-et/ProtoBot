package jobsite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redhat-et/protobot/source-control-manager/internal/gitx"
)

// LocalAdapter is the deterministic in-process sandbox backend. It passes
// the v1 conformance suite without a cluster, external network, or real
// credentials. Enforcement is the contract's observable behavior: a hosted
// backend must produce the same denials under kernel isolation.
type LocalAdapter struct{}

// NewLocalAdapter returns the local test adapter.
func NewLocalAdapter() *LocalAdapter {
	return &LocalAdapter{}
}

// Identity returns the local backend identity.
func (a *LocalAdapter) Identity() BackendIdentity {
	return BackendIdentity{Name: LocalBackendName, Version: LocalBackendVersion}
}

// Capabilities reports every v1 capability as supported. The local adapter
// enforces the contract in-process on every GOOS the fixture already runs
// on; it does not skip checks.
func (a *LocalAdapter) Capabilities() []Capability {
	return requiredCapabilitySet(true, "")
}

// Open copies the role projection into an ephemeral workspace and starts a
// per-session credential broker. Missing capabilities fail closed.
func (a *LocalAdapter) Open(ctx context.Context, req OpenRequest) (Session, error) {
	if missing := missingRequiredCapabilities(a.Capabilities()); len(missing) > 0 {
		return nil, fail(CodeSandboxFailClosed, "missing capabilities "+strings.Join(missing, ", ")+"; autonomous execution disabled")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Role != RoleWorkerA && req.Role != RoleWorkerB {
		return nil, fail(CodeSandboxDenied, "Sandbox role is not a Worker role.")
	}
	if req.Projection.Path == "" {
		return nil, fail(CodeSandboxDenied, "Sandbox projection path is missing.")
	}
	if req.Limits.WallClock < 0 || req.Limits.CPU < 0 || req.Limits.ProcessCount < 0 ||
		req.Limits.WritableFiles < 0 || req.Limits.WritableBytes < 0 || req.Limits.MemoryBytes < 0 {
		return nil, fail(CodeSandboxFailClosed, "Sandbox resource limits are invalid; autonomous execution disabled")
	}

	scratch, err := os.MkdirTemp("", "jobsite-sandbox-")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(scratch, "workspace")
	if err := copyTree(req.Projection.Path, root); err != nil {
		_ = os.RemoveAll(scratch)
		return nil, err
	}
	env := sandboxEnv(scratch)
	repo, err := openGitWithEnv(root, env)
	if err != nil {
		_ = os.RemoveAll(scratch)
		return nil, err
	}
	if err := repo.scrubMeta(); err != nil {
		repo.Close()
		_ = os.RemoveAll(scratch)
		return nil, err
	}

	if req.WorkerRoot == "" {
		req.WorkerRoot = req.Projection.RootCommit
	}
	now := time.Now()
	var wallDeadline, leaseDeadline time.Time
	if req.Limits.WallClock > 0 {
		wallDeadline = now.Add(req.Limits.WallClock)
	}
	if req.Lease > 0 {
		leaseDeadline = now.Add(req.Lease)
	}
	deadline := wallDeadline
	if !leaseDeadline.IsZero() && (deadline.IsZero() || leaseDeadline.Before(deadline)) {
		deadline = leaseDeadline
	}
	var sessionCtx context.Context
	var cancel context.CancelFunc
	if !deadline.IsZero() {
		sessionCtx, cancel = context.WithDeadline(ctx, deadline)
	} else {
		sessionCtx, cancel = context.WithCancel(ctx)
	}

	brokerSecret := "session-secret-" + digestBytes([]byte(req.TraceID+"/"+req.Role+"/"+req.SourceCommit)) // pragma: allowlist secret

	session := &localSession{
		identity:      a.Identity(),
		req:           req,
		scratch:       scratch,
		root:          root,
		repo:          repo,
		cancel:        cancel,
		ctx:           sessionCtx,
		opened:        time.Now(),
		writes:        map[string]localWrite{},
		env:           env,
		allow:         append([]NetworkRule(nil), req.NetworkAllow...),
		auditDir:      filepath.Join(scratch, auditDirName),
		brokerSecret:  brokerSecret,
		limitCPU:      req.Limits.CPU,
		limitMemory:   req.Limits.MemoryBytes,
		limitProcs:    req.Limits.ProcessCount,
		limitBytes:    req.Limits.WritableBytes,
		limitFiles:    req.Limits.WritableFiles,
		limitWall:     req.Limits.WallClock,
		limitLease:    req.Lease,
		wallDeadline:  wallDeadline,
		leaseDeadline: leaseDeadline,
	}
	session.startBroker()
	session.appendAudit(SandboxAuditEvent{
		EventType: EventSandboxOpen,
		Decision:  DecisionAccept,
		Action:    "open",
		Target:    req.Role,
	})
	return session, nil
}

type localWrite struct {
	action  string
	mode    string
	content []byte
}

type trackedProcess struct {
	*exec.Cmd
	done chan struct{}
}

type localSession struct {
	mu            sync.Mutex
	identity      BackendIdentity
	req           OpenRequest
	scratch       string
	root          string
	repo          *gitRepo
	cancel        context.CancelFunc
	ctx           context.Context
	opened        time.Time
	closed        bool
	cancelled     bool
	writes        map[string]localWrite
	env           []string
	allow         []NetworkRule
	cmds          []*trackedProcess
	argvLog       [][]string
	writtenBytes  int64
	writtenFiles  int
	cpuUsed       time.Duration
	audit         []SandboxAuditEvent
	auditDir      string
	upstream      *httptest.Server
	broker        *httptest.Server
	brokerSecret  string
	limitCPU      time.Duration
	limitMemory   int64
	limitProcs    int
	limitBytes    int64
	limitFiles    int
	limitWall     time.Duration
	limitLease    time.Duration
	wallDeadline  time.Time
	leaseDeadline time.Time
}

func (s *localSession) Role() string { return s.req.Role }

func (s *localSession) Root() string { return s.root }

func (s *localSession) EphemeralDirs() []string {
	return []string{s.scratch}
}

func (s *localSession) Environ() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.env))
	copy(out, s.env)
	return out
}

func (s *localSession) Audit() []SandboxAuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SandboxAuditEvent, len(s.audit))
	copy(out, s.audit)
	return out
}

func (s *localSession) Execute(ctx context.Context, req ExecRequest) (ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(ctx); err != nil {
		return ExecResult{}, err
	}
	if containsSentinel(req.Args...) {
		return s.denyLocked(EventSandboxExec, req.Executable, "Process argument contains credential sentinel.")
	}
	if req.Network != nil {
		return s.networkLocked(ctx, req)
	}
	if req.Executable == ApprovedFetchName {
		return s.denyLocked(EventSandboxExec, req.Executable, "Network request is missing from approved-fetch.")
	}
	if req.Executable == BurnName || req.Executable == "burn" {
		return s.burnLocked(ctx, req)
	}
	if req.Executable == "sleep" {
		return s.sleepLocked(ctx, req)
	}
	return s.denyLocked(EventSandboxExec, req.Executable, "Executable is not on the sandbox allowlist.")
}

func (s *localSession) ReadPath(ctx context.Context, projectPath string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(ctx); err != nil {
		return nil, err
	}
	canonical, err := s.canonicalInRoot(projectPath)
	if err != nil {
		_, denyErr := s.denyLocked(EventSandboxRead, projectPath, "Sandbox path is outside the role projection.")
		return nil, denyErr
	}
	if !s.req.Policy.VisibleTo(s.req.Role, canonical) {
		_, denyErr := s.denyLocked(EventSandboxRead, canonical, "Sandbox path is not visible to the Worker role.")
		return nil, denyErr
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(canonical)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fail(CodeSandboxDenied, "Sandbox path does not exist in the projection.")
		}
		return nil, err
	}
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxRead, Decision: DecisionAccept, Action: "read", Target: canonical})
	return data, nil
}

func (s *localSession) WritePath(ctx context.Context, projectPath string, content []byte, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(ctx); err != nil {
		return err
	}
	canonical, err := s.canonicalInRoot(projectPath)
	if err != nil {
		_, denyErr := s.denyLocked(EventSandboxWrite, projectPath, "Sandbox path is outside the role projection.")
		return denyErr
	}
	if !s.req.Policy.WritableBy(s.req.Role, canonical) {
		reason := "Sandbox path is outside the Worker role allowlist."
		switch s.req.Policy.Classify(canonical) {
		case ClassShared:
			reason = "Sandbox path is shared and read-only to Workers."
		case ClassUnclassified:
			reason = "Sandbox path is unclassified and denied by default."
		}
		_, denyErr := s.denyLocked(EventSandboxWrite, canonical, reason)
		return denyErr
	}
	if mode != ModeFile && mode != ModeExec {
		_, denyErr := s.denyLocked(EventSandboxWrite, canonical, "Sandbox file mode must be 100644 or 100755.")
		return denyErr
	}
	size := int64(len(content))
	if size > s.limitMemory {
		return s.limitLocked("memory", fmt.Sprintf("%d", s.limitMemory), "memory limit exceeded")
	}
	if s.writtenBytes+size > s.limitBytes {
		return s.limitLocked("writable-disk", fmt.Sprintf("%d", s.limitBytes), "writable disk limit exceeded")
	}
	full := filepath.Join(s.root, filepath.FromSlash(canonical))
	_, existsErr := os.Lstat(full)
	exists := existsErr == nil
	if !exists && s.writtenFiles+1 > s.limitFiles {
		return s.limitLocked("writable-files", fmt.Sprintf("%d", s.limitFiles), "writable file limit exceeded")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode == ModeExec {
		perm = 0o755
	}
	if err := os.WriteFile(full, content, perm); err != nil {
		return err
	}
	action := ActionUpdate
	if !exists {
		action = ActionCreate
		s.writtenFiles++
	}
	s.writtenBytes += size
	s.writes[canonical] = localWrite{action: action, mode: mode, content: append([]byte(nil), content...)}
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxWrite, Decision: DecisionAccept, Action: action, Target: canonical})
	return nil
}

func (s *localSession) Git(ctx context.Context, args ...string) (ExecResult, error) {
	s.mu.Lock()
	if err := s.guardLocked(ctx); err != nil {
		s.mu.Unlock()
		return ExecResult{}, err
	}
	if len(args) == 0 {
		res, err := s.denyLocked(EventSandboxGit, "git", "git command is missing")
		s.mu.Unlock()
		return res, err
	}
	if containsSentinel(args...) {
		res, denyErr := s.denyLocked(EventSandboxGit, "git", "Git argument contains credential sentinel.")
		s.mu.Unlock()
		return res, denyErr
	}
	if len(s.cmds)+1 > s.limitProcs {
		err := s.limitLocked("process-count", fmt.Sprintf("%d", s.limitProcs), "process-count limit exceeded")
		s.mu.Unlock()
		return ExecResult{Denied: true, Status: -1}, err
	}
	cleanArgs, err := classifyAndPrepareGitArgs(args)
	if err != nil {
		res, denyErr := s.denyLocked(EventSandboxGit, strings.Join(args, " "), err.Error())
		s.mu.Unlock()
		return res, denyErr
	}
	cmd := s.repo.runner.CommandContext(context.Background(), cleanArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(nil)
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return ExecResult{}, err
	}
	tp := &trackedProcess{Cmd: cmd, done: make(chan struct{})}
	s.cmds = append(s.cmds, tp)
	s.argvLog = append(s.argvLog, append([]string(nil), cmd.Args...))
	sessionCtx := s.ctx
	s.mu.Unlock()

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(tp.done)
	}()
	var waitErr error
	var callerCancelled bool
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		killCmdProcess(cmd)
		<-done
		callerCancelled = true
	case <-sessionCtx.Done():
		killCmdProcess(cmd)
		<-done
	}
	elapsed := time.Since(start)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeCmdLocked(tp)
	s.cpuUsed += elapsed

	if sessionCtx.Err() != nil {
		if limitName, limitConfig, ok := s.expiredLimit(); ok {
			return ExecResult{Denied: true, Status: -1}, s.limitLocked(limitName, limitConfig, limitName+" limit exceeded")
		}
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if s.cancelled || s.closed {
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if callerCancelled || ctx.Err() != nil {
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Call context cancelled.")
	}
	status := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			status = exitErr.ExitCode()
		} else {
			return ExecResult{}, fmt.Errorf("git wait: %w", waitErr)
		}
	}
	out := ExecResult{Status: status, Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if containsSentinelBytes(out.Stdout, out.Stderr) {
		return s.denyLocked(EventSandboxGit, strings.Join(args, " "), "Git output contained a credential sentinel.")
	}
	decision := DecisionAccept
	if status != 0 {
		decision = DecisionReject
	}
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxGit, Decision: decision, Action: args[0], Target: strings.Join(args, " ")})
	return out, nil
}

func (s *localSession) CollectPatch(ctx context.Context) (PatchBundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(ctx); err != nil {
		return PatchBundle{}, err
	}
	if len(s.writes) == 0 {
		return PatchBundle{}, fail(CodePatchRejected, "PatchBundle must contain at least one operation.")
	}
	ops := make([]Operation, 0, len(s.writes))
	for path, write := range s.writes {
		ops = append(ops, Operation{Path: path, Action: write.action, Mode: write.mode, Content: append([]byte(nil), write.content...)})
	}
	return NewPatchBundle(s.req.Role, s.req.WorkItem, s.req.Cycle, s.req.Policy.Digest, s.req.SourceCommit, s.req.WorkerRoot, s.req.IntegrationBase, ops)
}

func (s *localSession) Cancel(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fail(CodeSandboxCancelled, "Sandbox session is closed.")
	}
	s.cancelled = true
	if s.cancel != nil {
		s.cancel()
	}
	s.killAllLocked()
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxCancel, Decision: DecisionAccept, Action: "cancel"})
	return ctx.Err()
}

func (s *localSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancelled = true
	if s.cancel != nil {
		s.cancel()
	}
	s.killAllLocked()
	if s.broker != nil {
		s.broker.Close()
		s.broker = nil
	}
	if s.upstream != nil {
		s.upstream.Close()
		s.upstream = nil
	}
	if s.repo != nil {
		s.repo.Close()
		s.repo = nil
	}
	err := os.RemoveAll(s.scratch)
	result := DecisionAccept
	detail := "removed"
	if err != nil {
		result = DecisionReject
		detail = err.Error()
	}
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxCleanup, Decision: result, Action: "cleanup", CleanupResult: detail})
	return err
}

func (s *localSession) expiredLimit() (string, string, bool) {
	now := time.Now()
	leaseExpired := !s.leaseDeadline.IsZero() && !now.Before(s.leaseDeadline)
	wallExpired := !s.wallDeadline.IsZero() && !now.Before(s.wallDeadline)
	if leaseExpired && wallExpired {
		if s.leaseDeadline.Before(s.wallDeadline) {
			return "lease", s.limitLease.String(), true
		}
		return "wall-clock", s.limitWall.String(), true
	}
	if leaseExpired {
		return "lease", s.limitLease.String(), true
	}
	if wallExpired {
		return "wall-clock", s.limitWall.String(), true
	}
	return "", "", false
}

func (s *localSession) guardLocked(ctx context.Context) error {
	if s.closed {
		return fail(CodeSandboxCancelled, "Sandbox session is closed.")
	}
	if s.cancelled {
		return fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if err := s.ctx.Err(); err != nil {
		s.cancelled = true
		if limitName, limitConfig, ok := s.expiredLimit(); ok {
			_ = s.limitLocked(limitName, limitConfig, limitName+" limit exceeded")
			return fail(CodeSandboxLimit, limitName+" limit exceeded")
		}
		return fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if err := ctx.Err(); err != nil {
		return fail(CodeSandboxCancelled, "Call context cancelled.")
	}
	if s.cpuUsed >= s.limitCPU {
		return s.limitLocked("cpu", s.limitCPU.String(), "cpu limit exceeded")
	}
	return nil
}

func (s *localSession) sleepLocked(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if len(s.cmds)+1 > s.limitProcs {
		return ExecResult{}, s.limitLocked("process-count", fmt.Sprintf("%d", s.limitProcs), "process-count limit exceeded")
	}
	args := req.Args
	if len(args) == 0 {
		args = []string{"1"}
	}
	cmd := exec.Command("sleep", args...)
	cmd.Dir = s.root
	gitx.Detach(cmd)
	cmd.Env = append([]string(nil), s.env...)
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	tp := &trackedProcess{Cmd: cmd, done: make(chan struct{})}
	s.cmds = append(s.cmds, tp)
	s.argvLog = append(s.argvLog, append([]string(nil), cmd.Args...))
	sessionCtx := s.ctx
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(tp.done)
	}()
	var waitErr error
	var callerCancelled bool
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		killCmdProcess(cmd)
		<-done
		callerCancelled = true
	case <-sessionCtx.Done():
		killCmdProcess(cmd)
		<-done
	}
	s.mu.Lock()
	s.removeCmdLocked(tp)

	if sessionCtx.Err() != nil {
		if limitName, limitConfig, ok := s.expiredLimit(); ok {
			return ExecResult{Denied: true, Status: -1}, s.limitLocked(limitName, limitConfig, limitName+" limit exceeded")
		}
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if s.cancelled || s.closed {
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if callerCancelled || ctx.Err() != nil {
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Call context cancelled.")
	}
	status := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			status = exitErr.ExitCode()
		} else {
			return ExecResult{}, waitErr
		}
	}
	if status != 0 {
		s.appendAudit(SandboxAuditEvent{EventType: EventSandboxExec, Decision: DecisionReject, Action: "sleep", Executable: req.Executable})
		return ExecResult{Status: status}, fmt.Errorf("sleep exited with status %d", status)
	}
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxExec, Decision: DecisionAccept, Action: "sleep", Executable: req.Executable})
	return ExecResult{Status: status}, nil
}

func (s *localSession) burnLocked(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if req.Digest != "" && req.Digest != BurnDigest {
		return s.denyLocked(EventSandboxExec, req.Executable, "Executable digest does not match allowlist.")
	}
	burnDur := 30 * time.Millisecond
	if len(req.Args) > 0 {
		if d, err := time.ParseDuration(req.Args[0]); err == nil {
			burnDur = d
		} else if f, err := strconv.ParseFloat(req.Args[0], 64); err == nil {
			burnDur = time.Duration(f * float64(time.Second))
		}
	}
	sessionCtx := s.ctx
	s.mu.Unlock()
	start := time.Now()
	target := start.Add(burnDur)
	var callerCancelled bool
burnLoop:
	for time.Now().Before(target) {
		select {
		case <-ctx.Done():
			callerCancelled = true
			break burnLoop
		case <-sessionCtx.Done():
			break burnLoop
		default:
			for i := 0; i < 50000; i++ {
				_ = i * i
			}
		}
	}
	elapsed := time.Since(start)
	s.mu.Lock()
	s.cpuUsed += elapsed
	if sessionCtx.Err() != nil {
		if limitName, limitConfig, ok := s.expiredLimit(); ok {
			return ExecResult{Denied: true, Status: -1}, s.limitLocked(limitName, limitConfig, limitName+" limit exceeded")
		}
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if s.cancelled || s.closed {
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if callerCancelled || ctx.Err() != nil {
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Call context cancelled.")
	}
	s.appendAudit(SandboxAuditEvent{
		EventType:  EventSandboxExec,
		Decision:   DecisionAccept,
		Action:     "burn",
		Executable: req.Executable,
	})
	return ExecResult{Status: 0, Stdout: []byte("burned")}, nil
}

func (s *localSession) networkLocked(ctx context.Context, req ExecRequest) (ExecResult, error) {
	netReq := req.Network
	if req.Executable != ApprovedFetchName || req.Digest != ApprovedFetchDigest {
		return s.denyLocked(EventSandboxNetwork, req.Executable, "Network executable is not approved.")
	}
	if netReq.Protocol != AllowedProtocol || netReq.Method != AllowedMethod {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination, "Network protocol or method is not approved.")
	}
	if looksLikeIP(netReq.Destination) {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination, "Direct IP destinations are denied.")
	}
	if strings.Contains(strings.ToLower(netReq.Protocol), "dns") || strings.HasPrefix(netReq.Destination, "dns-") {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination, "DNS tunneling is denied.")
	}
	if netReq.Protocol == "tcp" || netReq.Protocol == "udp" || netReq.Protocol == "raw" {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination, "Raw sockets are denied.")
	}
	if strings.Contains(netReq.Destination, ":") && !strings.HasSuffix(netReq.Destination, ":443") {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination, "Alternate ports are denied.")
	}
	if netReq.Protocol == "http" || strings.EqualFold(netReq.Path, "/tls-bypass") {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination, "TLS bypass is denied.")
	}
	if !s.ruleMatch(req.Executable, req.Digest, netReq) {
		return s.denyLocked(EventSandboxNetwork, netReq.Destination+netReq.Path, "Network destination is denied by default.")
	}
	if s.broker == nil {
		return ExecResult{}, fail(CodeSandboxFailClosed, "Credential broker is unavailable; autonomous execution disabled")
	}

	sessionCtx := s.ctx
	brokerURL := s.broker.URL
	sessionToken := s.brokerSecret

	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	stopWatcher := make(chan struct{})
	defer close(stopWatcher)
	go func() {
		select {
		case <-sessionCtx.Done():
			cancelReq()
		case <-stopWatcher:
		}
	}()

	httpReq, err := http.NewRequestWithContext(reqCtx, netReq.Method, brokerURL+netReq.Path, nil)
	if err != nil {
		return ExecResult{}, err
	}
	httpReq.Header.Set("X-Sandbox-Session-Token", sessionToken)
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirectDenied
		},
	}

	s.mu.Unlock()
	resp, doErr := client.Do(httpReq)
	var body []byte
	var bodyErr error
	if doErr == nil {
		body, bodyErr = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	s.mu.Lock()

	if sessionCtx.Err() != nil {
		if limitName, limitConfig, ok := s.expiredLimit(); ok {
			return ExecResult{Denied: true, Status: -1}, s.limitLocked(limitName, limitConfig, limitName+" limit exceeded")
		}
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if s.cancelled || s.closed {
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if ctx.Err() != nil {
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Call context cancelled.")
	}

	if doErr != nil {
		if errors.Is(doErr, errRedirectDenied) || errors.Is(errors.Unwrap(doErr), errRedirectDenied) {
			return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
		}
		var urlErr interface{ Unwrap() error }
		if errors.As(doErr, &urlErr) && errors.Is(urlErr.Unwrap(), errRedirectDenied) {
			return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
		}
		if strings.Contains(doErr.Error(), errRedirectDenied.Error()) {
			return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
		}
		return ExecResult{}, doErr
	}
	if bodyErr != nil {
		return ExecResult{}, bodyErr
	}

	if containsSentinel(resp.Header.Get(sandboxBrokerHeader), resp.Header.Get("X-Echo-Authorization")) || containsSentinelBytes(body) {
		return s.denyLocked(EventSandboxNetwork, netReq.Path, "Credential sentinel leaked through the broker response.")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
	}
	s.appendAudit(SandboxAuditEvent{
		EventType:        EventSandboxNetwork,
		Decision:         DecisionAccept,
		Action:           netReq.Method,
		Target:           netReq.Destination + netReq.Path,
		Executable:       req.Executable,
		ExecutableDigest: req.Digest,
	})
	return ExecResult{Status: resp.StatusCode, Stdout: body}, nil
}

var errRedirectDenied = errors.New("sandbox redirect denied")

func (s *localSession) ruleMatch(executable, digest string, netReq *NetworkRequest) bool {
	dest := netReq.Destination
	if host, _, ok := strings.Cut(dest, ":"); ok && strings.HasSuffix(dest, ":443") {
		dest = host
	}
	for _, rule := range s.allow {
		if rule.Executable == executable && rule.Digest == digest &&
			rule.Destination == dest && rule.Protocol == netReq.Protocol &&
			rule.Method == netReq.Method && rule.Path == netReq.Path {
			return true
		}
	}
	return false
}

func (s *localSession) startBroker() {
	s.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sandboxBrokerHeader) != "Bearer "+sandboxBrokerToken {
			http.Error(w, "missing broker credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Echo-Authorization", r.Header.Get(sandboxBrokerHeader))
		if r.URL.Path == "/v2/redirect" {
			http.Redirect(w, r, "/v2/secret", http.StatusFound)
			return
		}
		if r.URL.Path != AllowedNetworkPath {
			http.Error(w, "not allowlisted", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("ok-payload"))
	}))
	upstream := s.upstream.URL
	s.broker = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sandbox-Session-Token") != s.brokerSecret {
			http.Error(w, "unauthorized broker caller", http.StatusUnauthorized)
			return
		}
		if r.Header.Get(sandboxBrokerHeader) != "" {
			http.Error(w, "sandbox must not supply credentials", http.StatusForbidden)
			return
		}
		fwd, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+r.URL.Path, r.Body)
		if err != nil {
			http.Error(w, "broker error", http.StatusBadGateway)
			return
		}
		fwd.Header.Set(sandboxBrokerHeader, "Bearer "+sandboxBrokerToken)
		brokerClient := &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := brokerClient.Do(fwd)
		if err != nil {
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "upstream body error", http.StatusBadGateway)
			return
		}
		for key, values := range resp.Header {
			if strings.EqualFold(key, sandboxBrokerHeader) || strings.EqualFold(key, "X-Echo-Authorization") {
				continue
			}
			for _, value := range values {
				if containsSentinel(value) {
					continue
				}
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bytes.ReplaceAll(body, []byte(sandboxBrokerToken), nil))
	}))
}

func (s *localSession) denyLocked(eventType, target, reason string) (ExecResult, error) {
	s.appendAudit(SandboxAuditEvent{EventType: eventType, Decision: DecisionReject, RejectionReason: reason, Action: EventSandboxDeny, Target: target})
	return ExecResult{Denied: true, Status: -1, Stderr: []byte(reason)}, fail(CodeSandboxDenied, reason)
}

func (s *localSession) limitLocked(name, configured, reason string) error {
	s.appendAudit(SandboxAuditEvent{
		EventType:       EventSandboxLimit,
		Decision:        DecisionReject,
		RejectionReason: reason,
		LimitName:       name,
		LimitConfigured: configured,
		Target:          name,
	})
	return fail(CodeSandboxLimit, reason)
}

func (s *localSession) appendAudit(ev SandboxAuditEvent) {
	ev.ContractVersion = ContractVersion
	ev.BackendName = s.identity.Name
	ev.BackendVersion = s.identity.Version
	ev.PolicyVersion = s.req.Policy.Version
	ev.PolicyDigest = s.req.Policy.Digest
	ev.WorkItem = s.req.WorkItem
	ev.Role = s.req.Role
	ev.TraceID = s.req.TraceID
	ev.Cycle = s.req.Cycle
	ev.Sequence = len(s.audit) + 1
	if len(s.audit) == 0 {
		ev.PreviousDigest = zeroDigest()
	} else {
		ev.PreviousDigest = s.audit[len(s.audit)-1].EventDigest
	}
	digest, err := digestSandboxEvent(ev)
	if err != nil {
		return
	}
	ev.EventDigest = digest
	s.audit = append(s.audit, ev)
	if s.closed {
		return
	}
	_ = writeAudit(s.auditDir, fmt.Sprintf("sandbox-%04d.json", ev.Sequence), AuditRecord{
		EventType:       ev.EventType,
		PolicyVersion:   ev.PolicyVersion,
		PolicyDigest:    ev.PolicyDigest,
		SourceCommit:    s.req.SourceCommit,
		Role:            ev.Role,
		Cycle:           ev.Cycle,
		WorkItem:        ev.WorkItem,
		Decision:        ev.Decision,
		RejectionReason: ev.RejectionReason,
		FixtureVersion:  FixtureVersion,
		BackendVersion:  ev.BackendVersion,
	})
}

func (s *localSession) canonicalInRoot(projectPath string) (string, error) {
	canonical, err := canonicalPath(projectPath)
	if err != nil || hasGitComponent(canonical) {
		return "", fail(CodeSandboxDenied, "Sandbox path is not a project-relative path.")
	}
	full := filepath.Join(s.root, filepath.FromSlash(canonical))
	rel, err := filepath.Rel(s.root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fail(CodeSandboxDenied, "Sandbox path is outside the role projection.")
	}
	return canonical, nil
}

func (s *localSession) killAllLocked() {
	var waitList []chan struct{}
	for _, tp := range s.cmds {
		killCmdProcess(tp.Cmd)
		waitList = append(waitList, tp.done)
	}
	for _, ch := range waitList {
		<-ch
	}
}

func (s *localSession) removeCmdLocked(tp *trackedProcess) {
	for i, c := range s.cmds {
		if c == tp {
			s.cmds = append(s.cmds[:i], s.cmds[i+1:]...)
			break
		}
	}
}

func sandboxEnv(scratch string) []string {
	var env []string
	if path, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+path)
	} else {
		env = append(env, "PATH=/usr/local/bin:/usr/bin:/bin")
	}
	if lang, ok := os.LookupEnv("LANG"); ok {
		env = append(env, "LANG="+lang)
	} else {
		env = append(env, "LANG=C.UTF-8")
	}
	if tz, ok := os.LookupEnv("TZ"); ok {
		env = append(env, "TZ="+tz)
	} else {
		env = append(env, "TZ=UTC")
	}
	env = append(env, "HOME="+scratch)
	env = append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	env = append(env, fixtureIdentity...)
	return env
}

func classifyAndPrepareGitArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("git command is missing")
	}
	first := args[0]
	if strings.HasPrefix(first, "-") {
		return nil, fmt.Errorf("git flag %q is forbidden in the sandbox", first)
	}
	subcmd := first
	subcmdAllowed := false
	switch subcmd {
	case "cat-file", "rev-parse", "status", "log", "show", "diff", "remote":
		subcmdAllowed = true
	}
	if !subcmdAllowed {
		return nil, fmt.Errorf("git subcommand %q is forbidden in the sandbox", subcmd)
	}

	if subcmd == "remote" {
		for _, remArg := range args[1:] {
			if remArg != "-v" && remArg != "--verbose" {
				return nil, fmt.Errorf("git remote subcommand %q is forbidden in the sandbox", remArg)
			}
		}
		return append([]string{"remote"}, args[1:]...), nil
	}

	var flags []string
	var operands []string
	hasDashDash := false
	hasEndOfOptions := false

	for i := 1; i < len(args); i++ {
		arg := args[i]
		if hasDashDash {
			if err := validateGitOperand(arg); err != nil {
				return nil, err
			}
			operands = append(operands, arg)
			continue
		}
		if arg == "--" {
			hasDashDash = true
			operands = append(operands, arg)
			continue
		}
		if arg == "--end-of-options" {
			hasEndOfOptions = true
			flags = append(flags, arg)
			continue
		}

		if arg == "--no-index" ||
			arg == "--output" || strings.HasPrefix(arg, "--output=") || arg == "-o" ||
			arg == "--config-env" || strings.HasPrefix(arg, "--config-env=") ||
			arg == "--git-dir" || strings.HasPrefix(arg, "--git-dir=") ||
			arg == "--work-tree" || strings.HasPrefix(arg, "--work-tree=") ||
			arg == "-C" || (strings.HasPrefix(arg, "-C") && !strings.HasPrefix(arg, "--")) ||
			arg == "-c" || (strings.HasPrefix(arg, "-c") && !strings.HasPrefix(arg, "--")) ||
			arg == "--exec-path" || strings.HasPrefix(arg, "--exec-path=") ||
			arg == "--namespace" || strings.HasPrefix(arg, "--namespace=") {
			return nil, fmt.Errorf("git flag %q is forbidden in the sandbox", arg)
		}

		if strings.HasPrefix(arg, "-") {
			if err := checkSubcommandFlag(subcmd, arg); err != nil {
				return nil, err
			}
			flags = append(flags, arg)
			if flagTakesArg(subcmd, arg) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				nextArg := args[i]
				if err := validateFlagArg(subcmd, arg, nextArg); err != nil {
					return nil, err
				}
				flags = append(flags, nextArg)
			}
		} else {
			if err := validateGitOperand(arg); err != nil {
				return nil, err
			}
			operands = append(operands, arg)
		}
	}

	result := []string{subcmd}
	result = append(result, flags...)
	if !hasEndOfOptions && subcmd != "remote" {
		result = append(result, "--end-of-options")
	}
	result = append(result, operands...)
	return result, nil
}

func checkSubcommandFlag(subcmd, flag string) error {
	switch subcmd {
	case "remote":
		if flag == "-v" || flag == "--verbose" {
			return nil
		}
		return fmt.Errorf("git remote subcommand %q is forbidden in the sandbox", flag)
	case "cat-file":
		switch flag {
		case "-e", "-p", "-t", "-s":
			return nil
		}
	case "rev-parse":
		switch flag {
		case "-q", "--quiet", "--verify", "--short", "--abbrev-ref", "--symbolic-full-name":
			return nil
		}
	case "status":
		switch flag {
		case "-s", "--short", "-b", "--branch", "--porcelain", "--porcelain=v1", "--porcelain=v2", "--ignored", "-u", "-unormal", "-uall", "-uno":
			return nil
		}
	case "log":
		switch flag {
		case "-n", "--oneline", "--stat", "--graph", "--decorate", "--no-decorate", "-p", "-u", "--patch":
			return nil
		}
		if strings.HasPrefix(flag, "--max-count=") || strings.HasPrefix(flag, "--format=") || strings.HasPrefix(flag, "--pretty=") {
			return nil
		}
		if len(flag) > 1 && flag[0] == '-' {
			if _, err := strconv.Atoi(flag[1:]); err == nil {
				return nil
			}
		}
	case "show":
		switch flag {
		case "-s", "--stat", "--oneline", "-p", "-u", "--patch":
			return nil
		}
		if strings.HasPrefix(flag, "--format=") || strings.HasPrefix(flag, "--pretty=") {
			return nil
		}
	case "diff":
		switch flag {
		case "-p", "-u", "--patch", "--stat", "--numstat", "--shortstat", "--name-only", "--name-status", "--cached", "--staged":
			return nil
		}
	}
	return fmt.Errorf("git flag %q is forbidden in the sandbox", flag)
}

func flagTakesArg(subcmd, flag string) bool {
	if subcmd == "log" && flag == "-n" {
		return true
	}
	return false
}

func validateFlagArg(subcmd, flag, val string) error {
	if subcmd == "log" && flag == "-n" {
		if _, err := strconv.Atoi(val); err != nil {
			return fmt.Errorf("git flag %q requires numeric argument", flag)
		}
		return nil
	}
	return nil
}

func validateGitOperand(op string) error {
	if op == "--" || op == "--end-of-options" {
		return nil
	}
	if strings.HasPrefix(op, "/") || strings.HasPrefix(op, "~") || filepath.IsAbs(op) {
		return fmt.Errorf("git operand %q is outside sandbox root", op)
	}
	if strings.ContainsAny(op, "\\\x00") {
		return fmt.Errorf("git operand %q contains invalid characters", op)
	}
	pathPart := op
	if _, path, ok := strings.Cut(op, ":"); ok {
		pathPart = path
	}
	if strings.HasPrefix(pathPart, "/") || strings.HasPrefix(pathPart, "~") || filepath.IsAbs(pathPart) {
		return fmt.Errorf("git operand %q is outside sandbox root", op)
	}
	if _, err := canonicalPath(pathPart); err != nil || hasGitComponent(pathPart) {
		return fmt.Errorf("git operand %q contains invalid path: %w", op, err)
	}
	cleaned := filepath.Clean(pathPart)
	if strings.HasPrefix(cleaned, "..") || strings.HasPrefix(pathPart, "../") || pathPart == ".." || strings.Contains(pathPart, "/../") || strings.HasSuffix(pathPart, "/..") {
		return fmt.Errorf("git operand %q is outside sandbox root", op)
	}
	return nil
}

func looksLikeIP(dest string) bool {
	host := dest
	if h, _, err := net.SplitHostPort(dest); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 1 && len(parts) <= 4 {
		allNumeric := true
		for _, part := range parts {
			if part == "" {
				allNumeric = false
				break
			}
			if _, err := strconv.ParseUint(part, 0, 64); err != nil {
				allNumeric = false
				break
			}
		}
		if allNumeric {
			return true
		}
	}
	return strings.Contains(host, ":")
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fail(CodeExportUnsafe, "Sandbox copy refused a symlink.")
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

// FailClosedAdapter reports a missing required capability and refuses Open.
// The suite uses it to prove a failed backend disables autonomous execution.
type FailClosedAdapter struct {
	Inner    Adapter
	Missing  string
	Platform bool
}

// Identity delegates to the inner adapter when present.
func (a FailClosedAdapter) Identity() BackendIdentity {
	if a.Inner != nil {
		return a.Inner.Identity()
	}
	return BackendIdentity{Name: "fail-closed", Version: ContractVersion}
}

// Capabilities reports the named capability as unsupported.
func (a FailClosedAdapter) Capabilities() []Capability {
	detail := "capability unavailable"
	if a.Platform {
		detail = "platform unsupported"
	}
	caps := requiredCapabilitySet(true, "")
	if a.Inner != nil {
		caps = a.Inner.Capabilities()
	}
	out := make([]Capability, len(caps))
	copy(out, caps)
	found := false
	for i := range out {
		if out[i].Name == a.Missing || a.Missing == "" {
			out[i].Supported = false
			out[i].Detail = detail
			found = true
			if a.Missing != "" {
				break
			}
		}
	}
	if !found && a.Missing != "" {
		out = append(out, Capability{Name: a.Missing, Supported: false, Detail: detail})
	}
	return out
}

// Open always fails closed when a required capability is missing.
func (a FailClosedAdapter) Open(context.Context, OpenRequest) (Session, error) {
	missing := missingRequiredCapabilities(a.Capabilities())
	reason := "missing capabilities " + strings.Join(missing, ", ") + "; autonomous execution disabled"
	if a.Platform {
		reason = "platform unsupported; autonomous execution disabled"
	}
	return nil, fail(CodeSandboxFailClosed, reason)
}
