package jobsite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FixtureVersion identifies this reference fixture.
const FixtureVersion = "jobsite-projection/v1"

// Audit event types.
const (
	EventExport = "export"
	EventPatch  = "patch"
)

// Decision outcomes.
const (
	DecisionAccept = "accept"
	DecisionReject = "reject"
)

// AuditRecord is the deterministic private audit record for an export or
// patch decision. It does not carry credentials, remote URLs, peer source,
// raw Worker logs, or raw Inspector findings.
type AuditRecord struct {
	EventType         string `json:"event_type"`
	PolicyVersion     int    `json:"policy_version"`
	PolicyDigest      string `json:"policy_digest"`
	SourceCommit      string `json:"source_commit"`
	Role              string `json:"role,omitempty"`
	WorkerRoot        string `json:"worker_root,omitempty"`
	VisiblePathDigest string `json:"visible_path_digest,omitempty"`
	DeniedPathDigest  string `json:"denied_path_digest,omitempty"`
	BundleDigest      string `json:"bundle_digest,omitempty"`
	IntegrationBase   string `json:"integration_base,omitempty"`
	Cycle             int    `json:"cycle"`
	WorkItem          string `json:"work_item,omitempty"`
	Decision          string `json:"decision"`
	RejectionReason   string `json:"rejection_reason,omitempty"`
	ResultingCommit   string `json:"resulting_commit,omitempty"`
	FixtureVersion    string `json:"fixture_version"`
	BackendVersion    string `json:"backend_version"`
}

func pathDigest(paths []string) string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeAudit(dir string, name string, record AuditRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

func writeSandboxAudit(dir string, name string, ev SandboxAuditEvent) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

func writeAuditExcl(dir string, name string, record AuditRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

const maxAuditSuffix = 1000

func uniqueAuditName(dir, prefix, bundleDigest string) (string, error) {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, bundleDigest)
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "unknown"
	}
	base := fmt.Sprintf("patch-%s-%s", prefix, clean)
	name := base + ".json"
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return name, nil
		}
		return "", err
	}
	for i := 1; i <= maxAuditSuffix; i++ {
		name = fmt.Sprintf("%s-%d.json", base, i)
		path = filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return name, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("exhausted suffix limit searching for unique audit name")
}

func newExportAudit(policy Policy, sourceCommit, role, workerRoot string, visible, denied []string, cycle int, workItem, decision, reason, resulting string) AuditRecord {
	return AuditRecord{
		EventType:         EventExport,
		PolicyVersion:     policy.Version,
		PolicyDigest:      policy.Digest,
		SourceCommit:      sourceCommit,
		Role:              role,
		WorkerRoot:        workerRoot,
		VisiblePathDigest: pathDigest(visible),
		DeniedPathDigest:  pathDigest(denied),
		Cycle:             cycle,
		WorkItem:          workItem,
		Decision:          decision,
		RejectionReason:   reason,
		ResultingCommit:   resulting,
		FixtureVersion:    FixtureVersion,
		BackendVersion:    FixtureVersion,
	}
}

func newPatchAudit(policy Policy, sourceCommit, role, workerRoot, bundleDigest, integrationBase string, cycle int, workItem, decision, reason, resulting string) AuditRecord {
	return AuditRecord{
		EventType:       EventPatch,
		PolicyVersion:   policy.Version,
		PolicyDigest:    policy.Digest,
		SourceCommit:    sourceCommit,
		Role:            role,
		WorkerRoot:      workerRoot,
		BundleDigest:    bundleDigest,
		IntegrationBase: integrationBase,
		Cycle:           cycle,
		WorkItem:        workItem,
		Decision:        decision,
		RejectionReason: reason,
		ResultingCommit: resulting,
		FixtureVersion:  FixtureVersion,
		BackendVersion:  FixtureVersion,
	}
}
