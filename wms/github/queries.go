package github

import (
	"fmt"
	"sort"

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
		return rejectedResult(call.Operation, validationNotFound("work-item"))
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
	items := make([]adapter.WorkItemProjection, 0)
	for id := range a.idx.workItemIssue {
		item, doc, _, err := a.loadWorkItemDocument(id)
		if err != nil {
			continue
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
	items := make([]adapter.WorkItemProjection, 0)
	for id := range a.idx.workItemIssue {
		item, doc, _, err := a.loadWorkItemDocument(id)
		if err != nil || item.State != validation.StateBlocked {
			continue
		}
		projection := projectWorkItem(item, doc)
		projection.ReasonKind = "blocked"
		if item.BlockReason != "" {
			projection.NextAction = "resolve-block"
			projection.ResolutionOptions = []string{"add-requirement", "out-of-scope", "impact-amendment"}
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
		return validation.WorkItem{}, storedDocument{}, 0, ErrNotFound
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

func (a *Adapter) updateWorkItemIssue(number int, item validation.WorkItem, requestID, changeSetID, priority string) error {
	if priority != "" {
		item.Priority = priority
	}
	sourceFingerprint := ""
	if entry, ok := a.idx.materializationKey[item.MaterializationKey]; ok {
		sourceFingerprint = entry.fingerprint
	} else if issue, err := a.client.GetIssue(a.ctx, number); err == nil {
		if doc, err := decodeBody(issue.Body); err == nil {
			sourceFingerprint = storedSourceFingerprint(doc)
		}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
