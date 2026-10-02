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
