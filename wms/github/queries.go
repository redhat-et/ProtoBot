package github

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

type workItemQueryPayload struct {
	State      validation.State `json:"state,omitempty"`
	Owner      string           `json:"owner,omitempty"`
	Dependency string           `json:"dependency,omitempty"`
	Priority   string           `json:"priority,omitempty"`
}

func (a *Adapter) getWorkItemLocked(call adapter.CallRequest) adapter.Result {
	item, doc, _, err := a.loadWorkItemDocument(call.WorkItemID)
	if err != nil {
		return rejectedResult(call.Operation, backendLoadRejection("work-item", err))
	}
	result := newResult(call.Operation)
	result.WorkItemID = item.ID
	result.WorkItemState = item.State
	result.ContractVersion = item.ContractVersion
	result.Resource = projectWorkItem(item, doc)
	return result
}

func (a *Adapter) queryWorkItemsLocked(call adapter.CallRequest) adapter.Result {
	var query workItemQueryPayload
	if err := decodePayload(call.Payload, &query); err != nil {
		return rejectedResult(call.Operation, invalidRequest("payload", err.Error()))
	}
	if err := a.ensureWorkItemsHydratedLocked(); err != nil {
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	items := make([]adapter.WorkItemProjection, 0)
	for id := range a.idx.workItemIssue {
		item, doc, _, err := a.loadWorkItemDocument(id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
		}
		if query.State != "" && item.State != query.State {
			continue
		}
		if query.Owner != "" && item.Owner != query.Owner {
			continue
		}
		if query.Priority != "" && item.Priority != query.Priority {
			continue
		}
		if query.Dependency != "" && !hasDependency(item.Dependencies, query.Dependency) {
			continue
		}
		items = append(items, projectWorkItem(item, doc))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	result := newResult(call.Operation)
	result.Items = items
	result.Resource = items
	return result
}

func (a *Adapter) queryBlockedWorkLocked(call adapter.CallRequest) adapter.Result {
	if err := a.ensureWorkItemsHydratedLocked(); err != nil {
		return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
	}
	items := make([]adapter.WorkItemProjection, 0)
	for id := range a.idx.workItemIssue {
		item, doc, _, err := a.loadWorkItemDocument(id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return rejectedResult(call.Operation, wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh))
		}
		if item.State != validation.StateBlocked {
			continue
		}
		projection := projectWorkItem(item, doc)
		projection.ReasonKind = sanitizedReasonKind(item.BlockReason)
		projection.NextAction = "review-resolution"
		projection.ResolutionOptions = []string{
			"add-requirement",
			"out-of-scope",
			"impact-amendment",
			"defer",
			"acknowledge",
		}
		items = append(items, projection)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	result := newResult(call.Operation)
	result.Items = items
	result.Resource = items
	return result
}

func (a *Adapter) loadWorkItem(id string) (validation.WorkItem, int, error) {
	item, _, number, err := a.loadWorkItemDocument(id)
	return item, number, err
}

func (a *Adapter) loadWorkItemDocument(id string) (validation.WorkItem, storedDocument, int, error) {
	number, ok := a.idx.workItemIssue[id]
	if !ok {
		// In-process index miss: fall back to the durable store so a
		// restarted adapter does not report a durable work item as
		// NOT_FOUND. A scan failure is returned as an error so callers
		// report WMS_UNAVAILABLE, never a false NOT_FOUND.
		item, doc, number, err := a.findWorkItemByID(id)
		if err != nil {
			return validation.WorkItem{}, storedDocument{}, 0, err
		}
		a.idx.workItemIssue[id] = number
		return item, doc, number, nil
	}
	issue, err := a.client.GetIssue(a.ctx, number)
	if err != nil {
		return validation.WorkItem{}, storedDocument{}, 0, err
	}
	doc, err := decodeBody(issue.Body)
	if err != nil || doc.Kind != kindWorkItem || doc.WorkItem == nil {
		return validation.WorkItem{}, storedDocument{}, 0, fmt.Errorf("invalid work-item document")
	}
	if doc.ProjectID != a.projectID || doc.WorkItem.ID != id {
		return validation.WorkItem{}, storedDocument{}, 0, ErrNotFound
	}
	return cloneWorkItem(*doc.WorkItem), doc, number, nil
}

// workItemIssueUpdateOptions carries the durable bookkeeping an
// updateWorkItemIssue call must persist in the same write as the work-item
// body it applies. Omitted linked IDs and the mutation record preserve the
// values already stored on the issue, so an update cannot blank them.
type workItemIssueUpdateOptions struct {
	mutation    *lastMutationRecord
	requestID   string
	changeSetID string
	priority    string
}

// updateWorkItemIssue persists item to its GitHub issue, preserving the
// durable request/change-set links, source fingerprint, and last-claim
// binding already stored on the issue unless opts replaces them. Writing the
// lease and the last-claim binding together in one GitHub API call is what
// makes claim reconciliation atomic with the lease itself.
func (a *Adapter) updateWorkItemIssue(number int, item validation.WorkItem, opts workItemIssueUpdateOptions) error {
	if opts.priority != "" {
		item.Priority = opts.priority
	}
	current, err := a.client.GetIssue(a.ctx, number)
	if err != nil {
		return err
	}
	prior, err := decodeBody(current.Body)
	if err != nil {
		return err
	}
	requestID := opts.requestID
	if requestID == "" {
		requestID = prior.LinkedRequestID
	}
	changeSetID := opts.changeSetID
	if changeSetID == "" {
		changeSetID = prior.LinkedChangeSetID
	}
	// The prior source fingerprint survives every update: it is bound at
	// materialization time to the source contract this issue was created
	// from, and no work-item mutation changes that contract. The
	// in-process materialization index is only a cache for it; the durable
	// issue body is authoritative.
	sourceFingerprint := prior.SourceFingerprint
	if sourceFingerprint == "" {
		if entry, ok := a.idx.materializationKey[item.MaterializationKey]; ok {
			sourceFingerprint = entry.fingerprint
		}
	}
	lastClaim := prior.LastClaim
	if opts.mutation != nil {
		lastClaim = opts.mutation
	}
	body, err := encodeBody("ProtoBot build work-item record.", storedDocument{
		Kind:               kindWorkItem,
		ProjectID:          a.projectID,
		WorkItem:           &item,
		MaterializationKey: item.MaterializationKey,
		SourceFingerprint:  sourceFingerprint,
		LinkedRequestID:    requestID,
		LinkedChangeSetID:  changeSetID,
		LinkedPriority:     item.Priority,
		LastClaim:          lastClaim,
	})
	if err != nil {
		return err
	}
	title := workItemTitle(item)
	_, err = a.client.UpdateIssue(a.ctx, number, UpdateIssueInput{
		Title:  &title,
		Body:   &body,
		Labels: []string{LabelWorkItem},
	})
	return err
}

func projectWorkItem(item validation.WorkItem, doc storedDocument) adapter.WorkItemProjection {
	owner := item.Owner
	deps := make([]string, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		deps = append(deps, dependency.ID)
	}
	return adapter.WorkItemProjection{
		ID:              item.ID,
		State:           item.State,
		Owner:           owner,
		Dependencies:    deps,
		Priority:        firstNonEmpty(doc.LinkedPriority, item.Priority),
		ContractVersion: item.ContractVersion,
		RequestID:       doc.LinkedRequestID,
		ChangeSetID:     doc.LinkedChangeSetID,
	}
}

func hasDependency(dependencies []validation.Dependency, id string) bool {
	for _, dependency := range dependencies {
		if dependency.ID == id {
			return true
		}
	}
	return false
}

// sanitizedReasonKind maps a stored block reason to the sanitized reason
// class the blocked-work.query projection requires, matching the memory
// adapter (wms/memory/requests.go) and the golden fixture.
func sanitizedReasonKind(reason string) string {
	if strings.Contains(strings.ToLower(reason), "undefined") {
		return "undefined-behavior"
	}
	return "unresolved-precondition"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
