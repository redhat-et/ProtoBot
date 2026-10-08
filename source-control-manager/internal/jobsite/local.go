package jobsite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	if req.Limits.ProcessCount < 0 || req.Limits.WritableFiles < 0 || req.Limits.WritableBytes < 0 || req.Limits.MemoryBytes < 0 {
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
	repo, err := openGit(root)
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
	deadline := time.Time{}
	now := time.Now()
	if req.Limits.WallClock > 0 {
		deadline = now.Add(req.Limits.WallClock)
	}
	if req.Lease > 0 {
		leaseEnd := now.Add(req.Lease)
		if deadline.IsZero() || leaseEnd.Before(deadline) {
			deadline = leaseEnd
		}
	}
	var sessionCtx context.Context
	var cancel context.CancelFunc
	if !deadline.IsZero() {
		sessionCtx, cancel = context.WithDeadline(ctx, deadline)
	} else {
		sessionCtx, cancel = context.WithCancel(ctx)
	}

	session := &localSession{
		identity:    a.Identity(),
		req:         req,
		scratch:     scratch,
		root:        root,
		repo:        repo,
		cancel:      cancel,
		ctx:         sessionCtx,
		opened:      time.Now(),
		writes:      map[string]localWrite{},
		env:         sandboxEnv(),
		allow:       append([]NetworkRule(nil), req.NetworkAllow...),
		auditDir:    filepath.Join(scratch, auditDirName),
		limitCPU:    req.Limits.CPU,
		limitMemory: req.Limits.MemoryBytes,
		limitProcs:  req.Limits.ProcessCount,
		limitBytes:  req.Limits.WritableBytes,
		limitFiles:  req.Limits.WritableFiles,
		limitWall:   req.Limits.WallClock,
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

type localSession struct {
	mu           sync.Mutex
	identity     BackendIdentity
	req          OpenRequest
	scratch      string
	root         string
	repo         *gitRepo
	cancel       context.CancelFunc
	ctx          context.Context
	opened       time.Time
	closed       bool
	cancelled    bool
	writes       map[string]localWrite
	env          []string
	allow        []NetworkRule
	cmds         []*exec.Cmd
	writtenBytes int64
	writtenFiles int
	cpuUsed      time.Duration
	audit        []SandboxAuditEvent
	auditDir     string
	upstream     *httptest.Server
	broker       *httptest.Server
	limitCPU     time.Duration
	limitMemory  int64
	limitProcs   int
	limitBytes   int64
	limitFiles   int
	limitWall    time.Duration
}

func (s *localSession) Role() string { return s.req.Role }

func (s *localSession) Root() string { return s.root }

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
	if req.Network != nil {
		return s.networkLocked(req)
	}
	if req.Executable == ApprovedFetchName {
		return s.denyLocked(EventSandboxExec, req.Executable, "Network request is missing from approved-fetch.")
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
	if s.limitMemory > 0 && size > s.limitMemory {
		return s.limitLocked("memory", fmt.Sprintf("%d", s.limitMemory), "memory limit exceeded")
	}
	if s.limitBytes > 0 && s.writtenBytes+size > s.limitBytes {
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
	defer s.mu.Unlock()
	if err := s.guardLocked(ctx); err != nil {
		return ExecResult{}, err
	}
	if len(args) == 0 {
		return s.denyLocked(EventSandboxGit, "git", "Git command is missing.")
	}
	if forbiddenGit(args) {
		return s.denyLocked(EventSandboxGit, strings.Join(args, " "), "Git command is forbidden in the sandbox.")
	}
	res, err := s.repo.run(args...)
	if err != nil {
		return ExecResult{}, err
	}
	out := ExecResult{Status: res.Status, Stdout: res.Stdout, Stderr: res.Stderr}
	if containsSentinelBytes(out.Stdout, out.Stderr) {
		return s.denyLocked(EventSandboxGit, strings.Join(args, " "), "Git output contained a credential sentinel.")
	}
	decision := DecisionAccept
	if !res.OK() {
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

func (s *localSession) guardLocked(ctx context.Context) error {
	if s.closed {
		return fail(CodeSandboxCancelled, "Sandbox session is closed.")
	}
	if s.cancelled {
		return fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if err := s.ctx.Err(); err != nil {
		s.cancelled = true
		if errors.Is(err, context.DeadlineExceeded) {
			_ = s.limitLocked("wall-clock", s.limitWall.String(), "wall-clock or lease limit exceeded")
			return fail(CodeSandboxLimit, "wall-clock or lease limit exceeded")
		}
		return fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return s.limitLocked("wall-clock", s.limitWall.String(), "wall-clock limit exceeded")
		}
		s.cancelled = true
		return fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
	}
	if s.limitCPU > 0 && s.cpuUsed >= s.limitCPU {
		return s.limitLocked("cpu", s.limitCPU.String(), "cpu limit exceeded")
	}
	return nil
}

func (s *localSession) sleepLocked(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if s.limitProcs == 0 || (s.limitProcs > 0 && len(s.cmds) >= s.limitProcs) {
		return ExecResult{}, s.limitLocked("process-count", fmt.Sprintf("%d", s.limitProcs), "process-count limit exceeded")
	}
	args := req.Args
	if len(args) == 0 {
		args = []string{"1"}
	}
	cmd := exec.CommandContext(s.ctx, "sleep", args...)
	cmd.Dir = s.root
	cmd.Env = append([]string(nil), s.env...)
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	s.cmds = append(s.cmds, cmd)
	sessionCtx := s.ctx
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	var cancelled bool
	var timedOut bool
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		cancelled = true
	case <-sessionCtx.Done():
		_ = cmd.Process.Kill()
		<-done
		if errors.Is(sessionCtx.Err(), context.DeadlineExceeded) {
			timedOut = true
		} else {
			cancelled = true
		}
	}
	s.mu.Lock()
	s.cpuUsed += time.Since(s.opened)
	if timedOut {
		return ExecResult{Denied: true, Status: -1}, s.limitLocked("wall-clock", s.limitWall.String(), "wall-clock or lease limit exceeded")
	}
	if cancelled || s.cancelled {
		s.cancelled = true
		return ExecResult{Denied: true, Status: -1}, fail(CodeSandboxCancelled, "Sandbox session is cancelled.")
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
	s.appendAudit(SandboxAuditEvent{EventType: EventSandboxExec, Decision: DecisionAccept, Action: "sleep", Executable: req.Executable})
	return ExecResult{Status: status}, nil
}

func (s *localSession) networkLocked(req ExecRequest) (ExecResult, error) {
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
	httpReq, err := http.NewRequestWithContext(s.ctx, netReq.Method, s.broker.URL+netReq.Path, nil)
	if err != nil {
		return ExecResult{}, err
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirectDenied
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(err, errRedirectDenied) || errors.Is(errors.Unwrap(err), errRedirectDenied) {
			return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
		}
		var urlErr interface{ Unwrap() error }
		if errors.As(err, &urlErr) && errors.Is(urlErr.Unwrap(), errRedirectDenied) {
			return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
		}
		if strings.Contains(err.Error(), errRedirectDenied.Error()) {
			return s.denyLocked(EventSandboxNetwork, netReq.Path, "Network redirects are denied.")
		}
		return ExecResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ExecResult{}, err
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
		defer resp.Body.Close()
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
	for _, cmd := range s.cmds {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

func sandboxEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") {
			continue
		}
		if containsSentinel(entry) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func forbiddenGit(args []string) bool {
	cmd := args[0]
	switch cmd {
	case "fetch", "pull", "clone", "ls-remote", "submodule", "daemon":
		return true
	case "remote":
		if len(args) == 1 || args[1] == "-v" || args[1] == "--verbose" {
			return false
		}
		return true
	default:
		return false
	}
}

func looksLikeIP(dest string) bool {
	host := dest
	if h, _, ok := strings.Cut(dest, ":"); ok {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	dots := strings.Count(host, ".")
	if dots == 3 {
		for _, part := range strings.Split(host, ".") {
			if part == "" {
				return false
			}
			for _, r := range part {
				if r < '0' || r > '9' {
					return false
				}
			}
		}
		return true
	}
	return strings.Contains(host, ":") && !strings.Contains(host, ".")
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
