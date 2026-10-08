package github

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

const (
	bodyBegin = "<!-- protobot-wms-v1 -->"
	bodyEnd   = "<!-- /protobot-wms-v1 -->"
)

// storedDocument is the durable ProtoBot record embedded in a GitHub issue body.
type storedDocument struct {
	Kind               string                 `json:"kind"`
	ProjectID          string                 `json:"project_id"`
	Request            *adapter.RequestRecord `json:"request,omitempty"`
	WorkItem           *validation.WorkItem   `json:"work_item,omitempty"`
	MaterializationKey string                 `json:"materialization_key,omitempty"`
	SourceFingerprint  string                 `json:"source_fingerprint,omitempty"`
	Omitted            bool                   `json:"omitted,omitempty"`
	LinkedRequestID    string                 `json:"linked_request_id,omitempty"`
	LinkedChangeSetID  string                 `json:"linked_change_set_id,omitempty"`
	LinkedPriority     string                 `json:"linked_priority,omitempty"`
	// CreateIdempotencyKey and CreateFingerprint bind a request issue to the
	// request.create call that produced it so a restarted adapter can replay it.
	CreateIdempotencyKey string `json:"create_idempotency_key,omitempty"`
	CreateFingerprint    string `json:"create_fingerprint,omitempty"`
	// ConsumedApprovalIDs durably records every Gate approval ID already
	// consumed by a refinement of this request, written in the same body
	// update as the request revision it authorized. A restarted adapter's
	// in-process approval index loses consumed status, so this field is the
	// durable source of truth that a replayed approval ID cannot authorize
	// another refinement (docs/architecture/drafting-table-wms.md, "the same
	// durable write as the request revision").
	ConsumedApprovalIDs []string `json:"consumed_approval_ids,omitempty"`
	// LastMutation binds the most recent non-create request mutation's
	// idempotency key and fingerprint to the revision it produced. A retry
	// that arrives after an ambiguous (exhausted-transient) write can match
	// this binding and reconcile to the already-applied result instead of a
	// spurious STALE_REQUEST_REVISION.
	LastMutation *lastMutationRecord `json:"last_mutation,omitempty"`
	// LastClaim binds the most recent applied claim's idempotency key and
	// fingerprint to the lease it issued, in the same body write as the
	// lease. A retry after an ambiguous (exhausted-transient) UpdateIssue
	// can match this binding and reconcile to the applied claim result
	// instead of a determinate DUPLICATE_CLAIM frozen under the claim key.
	LastClaim *lastMutationRecord `json:"last_claim,omitempty"`
}

const (
	kindRequest  = "request"
	kindWorkItem = "work-item"
)

func encodeBody(titleNote string, doc storedDocument) (string, error) {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if titleNote != "" {
		b.WriteString(titleNote)
		b.WriteString("\n\n")
	}
	b.WriteString(bodyBegin)
	b.WriteByte('\n')
	b.Write(encoded)
	b.WriteByte('\n')
	b.WriteString(bodyEnd)
	b.WriteByte('\n')
	return b.String(), nil
}

func decodeBody(body string) (storedDocument, error) {
	start := strings.Index(body, bodyBegin)
	end := strings.Index(body, bodyEnd)
	if start < 0 || end < 0 || end <= start {
		return storedDocument{}, fmt.Errorf("protobot wms body markers missing")
	}
	payload := strings.TrimSpace(body[start+len(bodyBegin) : end])
	var doc storedDocument
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return storedDocument{}, err
	}
	return doc, nil
}

func requestTitle(request adapter.RequestRecord) string {
	intent := strings.TrimSpace(request.Intent)
	if len(intent) > 80 {
		intent = intent[:80]
	}
	return fmt.Sprintf("[protobot request] %s (%s)", intent, request.ID)
}

func workItemTitle(item validation.WorkItem) string {
	return fmt.Sprintf("[protobot work-item] %s (%s)", item.ID, item.State)
}
