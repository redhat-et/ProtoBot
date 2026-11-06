package jobsite

import (
	"regexp"
	"slices"
	"sort"
)

// Fault is the fixture Triage classification. A specification gap is a
// blocked outcome rather than a claim that either Worker is at fault.
type Fault string

const (
	FaultNone             Fault = "none"
	FaultTest             Fault = "test"
	FaultImplementation   Fault = "implementation"
	FaultBoth             Fault = "both"
	FaultSpecificationGap Fault = "specification-gap"
)

// CodeTriageInvalid means a Triage decision cannot safely cross a Worker
// boundary. The error never quotes its untrusted input.
const CodeTriageInvalid = "triage.invalid"

// TriageInput is private control-plane evidence. Its free-text fields may
// contain peer source, assertions, logs, or Inspector findings, and must
// never be delivered directly to a Worker.
type TriageInput struct {
	Outcome           string   `json:"outcome"`
	Fault             Fault    `json:"fault"`
	RequirementIDs    []string `json:"requirement_ids"`
	Reason            string   `json:"reason,omitempty"`
	RawLogs           string   `json:"raw_logs,omitempty"`
	InspectorFindings string   `json:"inspector_findings,omitempty"`
}

// WorkerFeedback is the complete allowlist crossing a Worker boundary.
// It has no field for peer identifiers, diagnostics, or raw Triage prose.
type WorkerFeedback struct {
	Role             string   `json:"role"`
	Outcome          string   `json:"outcome"`
	Fault            Fault    `json:"fault"`
	RequirementIDs   []string `json:"requirement_ids"`
	Reason           string   `json:"reason"`
	RetriesRemaining int      `json:"retries_remaining"`
	Request          string   `json:"request"`
}

// RedactionDecision records the private sanitizer decision for each output.
// DiscardedFields contains field names, never their sensitive values.
type RedactionDecision struct {
	Role            string   `json:"role"`
	DiscardedFields []string `json:"discarded_fields"`
	GenericReason   bool     `json:"generic_reason"`
}

// These tokens also allow the short requirement IDs in the isolation
// fixture. Membership in the trusted approved set remains mandatory.
var feedbackRequirementID = regexp.MustCompile(`^REQ-[A-Za-z0-9]+(-[A-Za-z0-9]+)*$`)

// SanitizeFeedback constructs recipient-specific feedback from constants
// and approved requirement IDs. Free text is always discarded rather than
// filtered heuristically: even an apparently benign reason can encode an
// assertion, peer path, expected value, or observed implementation behavior.
// The caller records input and redactions privately before delivering only
// WorkerFeedback to a Worker.
func SanitizeFeedback(input TriageInput, approvedRequirementIDs []string, retriesRemaining int) ([]WorkerFeedback, []RedactionDecision, error) {
	if retriesRemaining < 0 {
		return nil, nil, fail(CodeTriageInvalid, "Retry budget must not be negative.")
	}
	roles, err := feedbackRoles(input.Outcome, input.Fault)
	if err != nil {
		return nil, nil, err
	}
	approved := make(map[string]bool, len(approvedRequirementIDs))
	for _, id := range approvedRequirementIDs {
		if len(id) > 128 || !feedbackRequirementID.MatchString(id) {
			return nil, nil, fail(CodeTriageInvalid, "Approved requirement IDs must be safe requirement identifiers.")
		}
		approved[id] = true
	}
	ids := append([]string{}, input.RequirementIDs...)
	for _, id := range ids {
		if !approved[id] {
			return nil, nil, fail(CodeTriageInvalid, "Triage references a requirement outside the approved contract.")
		}
	}
	sort.Strings(ids)
	ids = slices.Compact(ids)
	if input.Outcome != "pass" && len(ids) == 0 {
		return nil, nil, fail(CodeTriageInvalid, "A non-passing decision must identify affected approved requirements.")
	}
	discarded := []string{}
	if input.InspectorFindings != "" {
		discarded = append(discarded, "inspector_findings")
	}
	if input.RawLogs != "" {
		discarded = append(discarded, "raw_logs")
	}
	if input.Reason != "" {
		discarded = append(discarded, "reason")
	}
	feedback := make([]WorkerFeedback, 0, len(roles))
	redactions := make([]RedactionDecision, 0, len(roles))
	for _, role := range roles {
		reason, request := feedbackText(input.Outcome, role, retriesRemaining)
		feedback = append(feedback, WorkerFeedback{
			Role: role, Outcome: input.Outcome, Fault: input.Fault,
			RequirementIDs: append([]string{}, ids...), Reason: reason,
			RetriesRemaining: retriesRemaining, Request: request,
		})
		redactions = append(redactions, RedactionDecision{
			Role: role, DiscardedFields: append([]string{}, discarded...), GenericReason: true,
		})
	}
	return feedback, redactions, nil
}

func feedbackRoles(outcome string, fault Fault) ([]string, error) {
	switch {
	case outcome == "pass" && fault == FaultNone:
		return []string{RoleWorkerA, RoleWorkerB}, nil
	case outcome == "blocked" && fault == FaultSpecificationGap:
		return []string{RoleWorkerA, RoleWorkerB}, nil
	case outcome == "fail" && fault == FaultTest:
		return []string{RoleWorkerA}, nil
	case outcome == "fail" && fault == FaultImplementation:
		return []string{RoleWorkerB}, nil
	case outcome == "fail" && fault == FaultBoth:
		return []string{RoleWorkerA, RoleWorkerB}, nil
	default:
		return nil, fail(CodeTriageInvalid, "Outcome and fault classification are inconsistent or unsupported.")
	}
}

func feedbackText(outcome, role string, remaining int) (string, string) {
	switch outcome {
	case "pass":
		return "Validation passed.", ""
	case "blocked":
		return "The approved requirements or interface contracts need clarification.", ""
	default:
		reason := "Review the affected approved requirements or interface contracts."
		if remaining == 0 {
			return reason, "Stop; the retry budget is exhausted."
		}
		if role == RoleWorkerA {
			return reason, "Regenerate your tests from the approved requirements and interface contracts."
		}
		return reason, "Revise your implementation to satisfy the approved requirements and interface contracts."
	}
}
