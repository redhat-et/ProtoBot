package github

import (
	"fmt"
	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

type scannedDocument struct {
	number int
	doc    storedDocument
}

func (a *Adapter) listStoredDocuments(label string) ([]scannedDocument, error) {
	issues, err := a.client.ListIssues(a.ctx, ListIssuesFilter{Labels: []string{label}})
	if err != nil {
		return nil, err
	}
	out := make([]scannedDocument, 0, len(issues))
	for _, issue := range issues {
		doc, err := decodeBody(issue.Body)
		if err != nil || doc.ProjectID != a.projectID {
			continue
		}
		out = append(out, scannedDocument{number: issue.Number, doc: doc})
	}
	return out, nil
}

// findWorkItemByMaterializationKey scans labeled work-item issues for a durable
// materialization_key binding. Used after UNKNOWN_MUTATION and on cold start
// when in-process indexes have no entry.
func (a *Adapter) findWorkItemByMaterializationKey(key string) (validation.WorkItem, storedDocument, int, error) {
	if key == "" {
		return validation.WorkItem{}, storedDocument{}, 0, ErrNotFound
	}
	docs, err := a.listStoredDocuments(LabelWorkItem)
	if err != nil {
		return validation.WorkItem{}, storedDocument{}, 0, err
	}
	for _, scanned := range docs {
		if scanned.doc.Kind != kindWorkItem || scanned.doc.WorkItem == nil {
			continue
		}
		if scanned.doc.MaterializationKey != key {
			continue
		}
		return cloneWorkItem(*scanned.doc.WorkItem), scanned.doc, scanned.number, nil
	}
	return validation.WorkItem{}, storedDocument{}, 0, ErrNotFound
}

// findWorkItemByID scans labeled work-item issues for a durable work-item id.
func (a *Adapter) findWorkItemByID(id string) (validation.WorkItem, storedDocument, int, error) {
	if id == "" {
		return validation.WorkItem{}, storedDocument{}, 0, ErrNotFound
	}
	docs, err := a.listStoredDocuments(LabelWorkItem)
	if err != nil {
		return validation.WorkItem{}, storedDocument{}, 0, err
	}
	for _, scanned := range docs {
		if scanned.doc.Kind != kindWorkItem || scanned.doc.WorkItem == nil {
			continue
		}
		if scanned.doc.WorkItem.ID != id {
			continue
		}
		return cloneWorkItem(*scanned.doc.WorkItem), scanned.doc, scanned.number, nil
	}
	return validation.WorkItem{}, storedDocument{}, 0, ErrNotFound
}

// findRequestByID scans labeled request issues for a durable request_id.
func (a *Adapter) findRequestByID(id string) (adapter.RequestRecord, int, error) {
	if id == "" {
		return adapter.RequestRecord{}, 0, ErrNotFound
	}
	docs, err := a.listStoredDocuments(LabelRequest)
	if err != nil {
		return adapter.RequestRecord{}, 0, err
	}
	for _, scanned := range docs {
		if scanned.doc.Kind != kindRequest || scanned.doc.Request == nil {
			continue
		}
		if scanned.doc.Request.ID != id {
			continue
		}
		return cloneRequest(*scanned.doc.Request), scanned.number, nil
	}
	return adapter.RequestRecord{}, 0, ErrNotFound
}

func storedSourceFingerprint(doc storedDocument) string {
	if doc.SourceFingerprint != "" {
		return doc.SourceFingerprint
	}
	if doc.WorkItem == nil {
		return ""
	}
	source := validation.CanonicalMaterializationSource(*doc.WorkItem, doc.WorkItem.ChangeType)
	return validation.SourceFingerprint(source)
}

func (a *Adapter) bindWorkItemLocked(item validation.WorkItem, doc storedDocument, number int, result adapter.Result) {
	a.idx.workItemIssue[item.ID] = number
	if doc.MaterializationKey == "" {
		return
	}
	a.idx.materializationKey[doc.MaterializationKey] = materializationEntry{
		fingerprint: storedSourceFingerprint(doc),
		result:      cloneResult(result),
		issueNumber: number,
	}
}

func (a *Adapter) bindRequestLocked(request adapter.RequestRecord, number int, semanticKey string) {
	a.idx.requestIssue[request.ID] = number
	if semanticKey != "" {
		a.idx.semanticRequests[semanticKey] = request.ID
	}
}

func materializationReplayResult(item validation.WorkItem) adapter.Result {
	result := newResult(string(validation.OperationMaterialize))
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.WorkItemID = item.ID
	result.WorkItemState = item.State
	result.ContractVersion = item.ContractVersion
	result.Resource = cloneWorkItem(item)
	return markReplayed(result)
}

func requestCreateAppliedResult(operation string, request adapter.RequestRecord) adapter.Result {
	result := newResult(operation)
	result.Outcome = adapter.OutcomeApplied
	result.Mutation = adapter.MutationApplied
	result.Idempotency = adapter.IdempotencyNew
	result.Resource = cloneRequest(request)
	result.RequestID = request.ID
	result.RequestRevision = request.Revision
	return result
}

// hydrateRequestsLocked rebuilds request indexes from durable request issues so
// a restarted adapter neither re-issues an existing request ID nor misses an
// existing semantic key. It returns the scanned request documents.
func (a *Adapter) hydrateRequestsLocked() ([]scannedDocument, error) {
	docs, err := a.listStoredDocuments(LabelRequest)
	if err != nil {
		return nil, err
	}
	for _, scanned := range docs {
		request := scanned.doc.Request
		if scanned.doc.Kind != kindRequest || request == nil {
			continue
		}
		if _, ok := a.idx.requestIssue[request.ID]; !ok {
			a.idx.requestIssue[request.ID] = scanned.number
		}
		semanticKey := requestSemanticKeyFor(request.Intent, request.AffectedInterfaces, request.AffectedScopes)
		if _, ok := a.idx.semanticRequests[semanticKey]; !ok {
			a.idx.semanticRequests[semanticKey] = request.ID
		}
		var seq uint64
		if _, err := fmt.Sscanf(request.ID, "REQ-%d", &seq); err == nil && seq > a.idx.nextRequestSeq {
			a.idx.nextRequestSeq = seq
		}
	}
	return docs, nil
}

// hydrateWorkItemsLocked rebuilds work-item indexes from durable work-item
// issues so a restarted adapter sees every durable work item instead of an
// empty in-process map. It returns an error for any scan failure: only a
// completed scan establishes the durable set, and a failed scan must not be
// served as an empty result.
func (a *Adapter) hydrateWorkItemsLocked() error {
	docs, err := a.listStoredDocuments(LabelWorkItem)
	if err != nil {
		return err
	}
	for _, scanned := range docs {
		item := scanned.doc.WorkItem
		if scanned.doc.Kind != kindWorkItem || item == nil {
			continue
		}
		if _, ok := a.idx.workItemIssue[item.ID]; !ok {
			a.idx.workItemIssue[item.ID] = scanned.number
		}
		if scanned.doc.MaterializationKey != "" {
			if _, ok := a.idx.materializationKey[scanned.doc.MaterializationKey]; !ok {
				a.idx.materializationKey[scanned.doc.MaterializationKey] = materializationEntry{
					fingerprint: storedSourceFingerprint(scanned.doc),
					issueNumber: scanned.number,
				}
			}
		}
	}
	return nil
}

// ensureRequestsHydratedLocked runs the one-time durable request scan a cold
// adapter needs before serving a request operation. After the first success
// the in-process indexes are trusted (they are updated on every write), so
// repeated operations do not rescan. A scan failure leaves the adapter
// unhydrated so a later call can retry.
func (a *Adapter) ensureRequestsHydratedLocked() error {
	if a.idx.requestsHydrated {
		return nil
	}
	if _, err := a.hydrateRequestsLocked(); err != nil {
		return err
	}
	a.idx.requestsHydrated = true
	return nil
}

// ensureWorkItemsHydratedLocked is the work-item analogue of
// ensureRequestsHydratedLocked.
func (a *Adapter) ensureWorkItemsHydratedLocked() error {
	if a.idx.workItemsHydrated {
		return nil
	}
	if err := a.hydrateWorkItemsLocked(); err != nil {
		return err
	}
	a.idx.workItemsHydrated = true
	return nil
}

// replayDurableRequestCreate replays or rejects a request.create whose
// idempotency key is already bound to a durable request issue.
func (a *Adapter) replayDurableRequestCreate(
	call adapter.CallRequest,
	authorization validation.AuthorizationContext,
	docs []scannedDocument,
) (adapter.Result, bool) {
	for _, scanned := range docs {
		doc := scanned.doc
		if doc.Kind != kindRequest || doc.Request == nil || doc.CreateIdempotencyKey != call.IdempotencyKey {
			continue
		}
		fingerprint := requestFingerprint(call, authorization)
		if doc.CreateFingerprint != fingerprint {
			return rejectedResult(call.Operation, wmsRejection(
				validation.CodeIdempotencyConflict,
				"The idempotency key was reused with a different request.",
				map[string]any{
					"conflict_kind":               "request-fingerprint",
					"key_scope":                   "project",
					"request_fingerprint_digest":  fingerprint,
					"recorded_fingerprint_digest": doc.CreateFingerprint,
				},
				validation.RetryNewKey,
			)), true
		}
		delete(a.idx.pendingRequestCreate, call.IdempotencyKey)
		return markReplayed(requestCreateAppliedResult(call.Operation, cloneRequest(*doc.Request))), true
	}
	return adapter.Result{}, false
}
