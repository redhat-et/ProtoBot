package jobsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ContractVersion identifies the version-1 Job Site sandbox contract.
const ContractVersion = "jobsite-sandbox/v1"

// Local backend identity.
const (
	LocalBackendName    = "local-test-adapter"
	LocalBackendVersion = ContractVersion
)

// Required v1 capabilities. A backend that cannot support every name
// must fail closed rather than skip the corresponding checks.
const (
	CapFilesystemIsolation = "filesystem-isolation"
	CapGitIsolation        = "git-isolation"
	CapNetworkPolicy       = "network-policy"
	CapCredentialBroker    = "credential-broker"
	CapLifecycleCleanup    = "lifecycle-cleanup"
	CapResourceLimits      = "resource-limits"
	CapAuditChain          = "audit-chain"
)

// Sandbox failure codes. A failed check disables autonomous execution;
// it must not fall back to a weaker profile.
const (
	CodeSandboxDenied     = "sandbox.denied"
	CodeSandboxCapability = "sandbox.capability"
	CodeSandboxLimit      = "sandbox.limit"
	CodeSandboxCancelled  = "sandbox.cancelled"
	CodeSandboxFailClosed = "sandbox.fail_closed"
)

// Sandbox audit event types.
const (
	EventSandboxOpen    = "sandbox.open"
	EventSandboxExec    = "sandbox.exec"
	EventSandboxRead    = "sandbox.read"
	EventSandboxWrite   = "sandbox.write"
	EventSandboxGit     = "sandbox.git"
	EventSandboxNetwork = "sandbox.network"
	EventSandboxDeny    = "sandbox.deny"
	EventSandboxLimit   = "sandbox.limit"
	EventSandboxCancel  = "sandbox.cancel"
	EventSandboxCleanup = "sandbox.cleanup"
)

// Approved local-adapter network identity. The executable is a named
// in-process client; the digest pins that name and contract version.
const (
	ApprovedFetchName   = "approved-fetch"
	AllowedDestination  = "registry.example.invalid"
	AllowedProtocol     = "https"
	AllowedMethod       = "GET"
	AllowedNetworkPath  = "/v2/allowed"
	sandboxBrokerToken  = "PROTOBOT-SANDBOX-BROKER-TOKEN"
	sandboxBrokerHeader = "Authorization"
)

// ApprovedFetchDigest is the v1 digest of the local approved-fetch client.
var ApprovedFetchDigest = digestBytes([]byte(ApprovedFetchName + "@" + ContractVersion))

// RequiredCapabilities is the closed v1 capability set.
var RequiredCapabilities = []string{
	CapFilesystemIsolation,
	CapGitIsolation,
	CapNetworkPolicy,
	CapCredentialBroker,
	CapLifecycleCleanup,
	CapResourceLimits,
	CapAuditChain,
}

// BackendIdentity names an execution backend that implements the contract.
type BackendIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capability is one named sandbox capability and whether the backend
// supports it on the current platform.
type Capability struct {
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
	Detail    string `json:"detail,omitempty"`
}

// ResourceLimits are the v1 limits every backend must enforce and record.
type ResourceLimits struct {
	WallClock     time.Duration `json:"wall_clock"`
	CPU           time.Duration `json:"cpu"`
	MemoryBytes   int64         `json:"memory_bytes"`
	ProcessCount  int           `json:"process_count"`
	WritableBytes int64         `json:"writable_bytes"`
	WritableFiles int           `json:"writable_files"`
}

// NetworkRule is one allowlisted executable, destination, protocol, method,
// and L7 path. The default policy is deny.
type NetworkRule struct {
	Executable  string `json:"executable"`
	Digest      string `json:"digest"`
	Destination string `json:"destination"`
	Protocol    string `json:"protocol"`
	Method      string `json:"method"`
	Path        string `json:"path"`
}

// OpenRequest is the control-plane input that creates one sandbox session.
type OpenRequest struct {
	Role            string
	WorkItem        string
	Cycle           int
	Projection      Repository
	Policy          Policy
	SourceCommit    string
	WorkerRoot      string
	IntegrationBase string
	Limits          ResourceLimits
	NetworkAllow    []NetworkRule
	TraceID         string
	Lease           time.Duration
}

// ExecRequest is one sandbox command. Network, when set, is an outbound
// request that must pass per-executable and per-destination L7 policy.
type ExecRequest struct {
	Executable string
	Digest     string
	Args       []string
	Network    *NetworkRequest
}

// NetworkRequest is an outbound request observed at the sandbox boundary.
type NetworkRequest struct {
	Destination string
	Protocol    string
	Method      string
	Path        string
}

// ExecResult is the observable outcome of one sandbox command.
type ExecResult struct {
	Status int
	Stdout []byte
	Stderr []byte
	Denied bool
}

// Adapter is the backend-neutral sandbox seam. Local, Fullsend/OpenShell,
// and future backends implement this interface. ProtoBot's control plane
// does not depend on a backend product name.
type Adapter interface {
	Identity() BackendIdentity
	Capabilities() []Capability
	Open(ctx context.Context, req OpenRequest) (Session, error)
}

// Session is one isolated Worker sandbox. Close releases every ephemeral
// resource. After Close, no recoverable state remains for the next session.
type Session interface {
	Role() string
	Root() string
	Execute(ctx context.Context, req ExecRequest) (ExecResult, error)
	ReadPath(ctx context.Context, projectPath string) ([]byte, error)
	WritePath(ctx context.Context, projectPath string, content []byte, mode string) error
	Git(ctx context.Context, args ...string) (ExecResult, error)
	CollectPatch(ctx context.Context) (PatchBundle, error)
	Cancel(ctx context.Context) error
	Close() error
	Audit() []SandboxAuditEvent
}

// SandboxAuditEvent is one tamper-evident private audit record. v1 requires
// a SHA-256 hash chain: EventDigest covers the canonical payload with
// EventDigest itself empty, and PreviousDigest is the prior EventDigest
// (or the zero digest for the first event).
type SandboxAuditEvent struct {
	Sequence         int    `json:"sequence"`
	EventType        string `json:"event_type"`
	ContractVersion  string `json:"contract_version"`
	BackendName      string `json:"backend_name"`
	BackendVersion   string `json:"backend_version"`
	PolicyVersion    int    `json:"policy_version"`
	PolicyDigest     string `json:"policy_digest"`
	Executable       string `json:"executable,omitempty"`
	ExecutableDigest string `json:"executable_digest,omitempty"`
	Decision         string `json:"decision"`
	RejectionReason  string `json:"rejection_reason,omitempty"`
	WorkItem         string `json:"work_item"`
	Role             string `json:"role"`
	TraceID          string `json:"trace_id"`
	Cycle            int    `json:"cycle"`
	Action           string `json:"action,omitempty"`
	Target           string `json:"target,omitempty"`
	LimitName        string `json:"limit_name,omitempty"`
	LimitConfigured  string `json:"limit_configured,omitempty"`
	CleanupResult    string `json:"cleanup_result,omitempty"`
	PreviousDigest   string `json:"previous_digest"`
	EventDigest      string `json:"event_digest,omitempty"`
}

// CheckResult is one conformance check outcome.
type CheckResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ConformanceReport is the structured capability and conformance report
// a backend must emit. Downstream issues invoke the same suite and compare
// this report; they do not trust a product name.
type ConformanceReport struct {
	ContractVersion     string            `json:"contract_version"`
	Backend             BackendIdentity   `json:"backend"`
	PolicyVersion       int               `json:"policy_version"`
	PolicyDigest        string            `json:"policy_digest"`
	ExecutableDigests   map[string]string `json:"executable_digests"`
	ResourceLimits      ResourceLimits    `json:"resource_limits"`
	Checks              []CheckResult     `json:"checks"`
	Passed              bool              `json:"passed"`
	AutonomousExecution bool              `json:"autonomous_execution"`
	FailClosedReason    string            `json:"fail_closed_reason,omitempty"`
	AuditHead           string            `json:"audit_head,omitempty"`
}

// VerifyAuditChain reports whether events form a valid v1 hash chain.
func VerifyAuditChain(events []SandboxAuditEvent) error {
	prev := zeroDigest()
	for i, ev := range events {
		if ev.PreviousDigest != prev {
			return fmt.Errorf("audit chain break at sequence %d", ev.Sequence)
		}
		clone := ev
		clone.EventDigest = ""
		payload, err := json.Marshal(clone)
		if err != nil {
			return err
		}
		if digestBytes(payload) != ev.EventDigest {
			return fmt.Errorf("audit event %d digest does not match the canonical payload", i)
		}
		if ev.Sequence != i+1 {
			return fmt.Errorf("audit sequence %d, want %d", ev.Sequence, i+1)
		}
		prev = ev.EventDigest
	}
	return nil
}

func zeroDigest() string {
	sum := sha256.Sum256(nil)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestSandboxEvent(ev SandboxAuditEvent) (string, error) {
	ev.EventDigest = ""
	payload, err := json.Marshal(ev)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func requiredCapabilitySet(supported bool, detail string) []Capability {
	out := make([]Capability, 0, len(RequiredCapabilities))
	for _, name := range RequiredCapabilities {
		out = append(out, Capability{Name: name, Supported: supported, Detail: detail})
	}
	return out
}

func missingRequiredCapabilities(caps []Capability) []string {
	have := map[string]bool{}
	for _, cap := range caps {
		have[cap.Name] = cap.Supported
	}
	var missing []string
	for _, name := range RequiredCapabilities {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func containsSentinel(values ...string) bool {
	for _, value := range values {
		if strings.Contains(value, sandboxBrokerToken) || strings.Contains(value, credentialSentinel) {
			return true
		}
	}
	return false
}

func containsSentinelBytes(values ...[]byte) bool {
	for _, value := range values {
		if strings.Contains(string(value), sandboxBrokerToken) || strings.Contains(string(value), credentialSentinel) {
			return true
		}
	}
	return false
}
