package validation

import (
	"crypto/sha3"
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"
)

// RequestFingerprint returns the canonical project-scoped idempotency
// fingerprint. Authorization expiry is intentionally excluded so an exact
// retry may use a refreshed Gate context with the same principal and scope.
func RequestFingerprint(request Request) string {
	request.IdempotencyKey = ""
	request.Authorization.ExpiresAt = time.Time{}
	request.Authorization.AllowedActions = slices.Clone(request.Authorization.AllowedActions)
	slices.Sort(request.Authorization.AllowedActions)
	request.Authorization.AllowedRefs = slices.Clone(request.Authorization.AllowedRefs)
	slices.Sort(request.Authorization.AllowedRefs)
	request.References = slices.Clone(request.References)
	slices.Sort(request.References)
	return fingerprint(struct {
		RuleVersion string
		Request     Request
	}{RuleVersion: RuleVersion, Request: request})
}

// SourceFingerprint identifies the complete source contract bound to a
// materialization key. Mutable lifecycle state and lease fields are excluded.
func SourceFingerprint(item WorkItem) string {
	item.State = ""
	item.ContractVersion = 0
	item.MaterializationKey = ""
	item.Priority = ""
	item.Owner = ""
	item.Lease = nil
	item.BlockReason = ""
	item.ActiveResolutionSubmissionID = ""
	item.InspectionRunSealed = false
	item.FindingsTerminal = false
	item.FinalTestsPassed = false
	item.ExpectedMerge = nil
	item.Reconciliation = ReconciliationEvidence{}
	return fingerprint(item)
}

// CanonicalMaterializationSource applies the payload-level change-type
// fallback used by materialization before a source contract is fingerprinted.
func CanonicalMaterializationSource(item WorkItem, fallbackChangeType string) WorkItem {
	if item.ChangeType == "" {
		item.ChangeType = fallbackChangeType
	}
	return item
}

func fingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		// All values used here are closed structs without unsupported JSON
		// members, so this branch indicates a programmer error rather than
		// untrusted request input.
		panic(err)
	}
	stream := sha3.New256()
	if _, err := stream.Write(encoded); err != nil {
		panic(err)
	}
	return "sha3-256:" + base64.RawURLEncoding.EncodeToString(stream.Sum(nil))
}
