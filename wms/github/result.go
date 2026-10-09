package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

func newResult(operation string) adapter.Result {
	return adapter.Result{
		OK:          true,
		Operation:   operation,
		Outcome:     adapter.OutcomeRead,
		Diagnostics: []adapter.Diagnostic{},
		Mutation:    adapter.MutationNone,
	}
}

func rejectedResult(operation string, rejection *validation.Rejection) adapter.Result {
	return adapter.Result{
		OK:          false,
		Operation:   operation,
		Outcome:     adapter.OutcomeRejected,
		Diagnostics: []adapter.Diagnostic{},
		Mutation:    adapter.MutationNone,
		Error:       rejection,
	}
}

func unknownMutationResult(operation string, message string) adapter.Result {
	return adapter.Result{
		OK:          false,
		Operation:   operation,
		Outcome:     adapter.OutcomeRejected,
		Diagnostics: []adapter.Diagnostic{},
		Mutation:    adapter.MutationUnknown,
		Error: &validation.Rejection{
			Code:    adapter.CodeUnknownMutation,
			Message: message,
			Details: map[string]any{},
			Retry:   validation.RetryReconcile,
		},
	}
}

func resultFromDecision(operation string, decision validation.Decision) adapter.Result {
	result := newResult(operation)
	copied := decision
	result.Decision = &copied
	if decision.Authority == validation.AuthorityPreflight {
		return result
	}
	switch decision.Outcome {
	case validation.OutcomeRejected:
		result.OK = false
		result.Outcome = adapter.OutcomeRejected
		result.Error = decision.Rejection
	case validation.OutcomeOmitted:
		result.Outcome = adapter.OutcomeApplied
		result.Mutation = adapter.MutationApplied
		result.Idempotency = adapter.IdempotencyNew
	default:
		result.Outcome = adapter.OutcomeApplied
		result.Mutation = adapter.MutationApplied
		result.Idempotency = adapter.IdempotencyNew
	}
	return result
}

func invalidRequest(field, message string) *validation.Rejection {
	return &validation.Rejection{
		Code:    adapter.CodeInvalidRequest,
		Message: "The request has a field that is missing or invalid.",
		Details: map[string]any{"field": field, "reason": message},
		Retry:   validation.RetryNewKey,
	}
}

func wmsRejection(code, message string, details map[string]any, retry string) *validation.Rejection {
	return &validation.Rejection{Code: code, Message: message, Details: details, Retry: retry}
}

func unauthorizedRejection(operation, workItemID, policyVersion string) *validation.Rejection {
	targetType := "project"
	if workItemID != "" {
		targetType = "work-item"
	}
	return wmsRejection(
		validation.CodeUnauthorizedAction,
		"The trusted authorization context does not permit this action or target.",
		map[string]any{
			"required_action": operation,
			"target_type":     targetType,
			"policy_version":  policyVersion,
		},
		validation.RetryAuthorize,
	)
}

func validationNotFound(targetType string) *validation.Rejection {
	return wmsRejection(
		validation.CodeNotFound,
		"The target is not visible in the trusted project context.",
		map[string]any{"target_type": targetType, "visibility_scope": "project"},
		validation.RetryRefresh,
	)
}

// backendLoadRejection classifies a load failure. ErrNotFound is a
// visibility-safe NOT_FOUND; any other error (a transient GitHub failure, or a
// corrupt/undecodable document) must not be reported as NOT_FOUND, since that
// would be remembered as a determinate outcome under a mutation's idempotency
// key even after the record becomes readable again. It is reported as
// WMS_UNAVAILABLE instead, which shouldRememberIdempotency never freezes.
func backendLoadRejection(targetType string, err error) *validation.Rejection {
	if errors.Is(err, ErrNotFound) {
		return validationNotFound(targetType)
	}
	return wmsRejection(adapter.CodeWMSUnavailable, err.Error(), map[string]any{}, validation.RetryRefresh)
}

func decodePayload(data []byte, target any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("payload contains more than one JSON value")
		}
		return err
	}
	return nil
}

func cloneRequest(request adapter.RequestRecord) adapter.RequestRecord {
	copied := request
	copied.AffectedInterfaces = append([]string(nil), request.AffectedInterfaces...)
	copied.AffectedScopes = append([]string(nil), request.AffectedScopes...)
	copied.Relationships = append([]adapter.RequestRelationship(nil), request.Relationships...)
	return copied
}

func cloneWorkItem(item validation.WorkItem) validation.WorkItem {
	copied := item
	copied.Dependencies = append([]validation.Dependency(nil), item.Dependencies...)
	copied.Readiness.UnresolvedReasons = append([]string(nil), item.Readiness.UnresolvedReasons...)
	if item.Lease != nil {
		lease := *item.Lease
		copied.Lease = &lease
	}
	if item.ExpectedMerge != nil {
		merge := *item.ExpectedMerge
		copied.ExpectedMerge = &merge
	}
	if item.Reconciliation.MergeEnvelope != nil {
		merge := *item.Reconciliation.MergeEnvelope
		copied.Reconciliation.MergeEnvelope = &merge
	}
	return copied
}

func cloneResult(result adapter.Result) adapter.Result {
	copied := result
	copied.Diagnostics = append([]adapter.Diagnostic(nil), result.Diagnostics...)
	if result.Error != nil {
		rejection := *result.Error
		if result.Error.Details != nil {
			rejection.Details = map[string]any{}
			for key, value := range result.Error.Details {
				rejection.Details[key] = value
			}
		}
		copied.Error = &rejection
	}
	if result.Decision != nil {
		decision := *result.Decision
		copied.Decision = &decision
	}
	if result.Link != nil {
		copied.Link = map[string]string{}
		for key, value := range result.Link {
			copied.Link[key] = value
		}
	}
	copied.Requests = append([]adapter.RequestRecord(nil), result.Requests...)
	copied.Items = append([]adapter.WorkItemProjection(nil), result.Items...)
	if result.PlannedDependency != nil {
		planned := *result.PlannedDependency
		copied.PlannedDependency = &planned
	}
	copied.Resource = cloneResource(result.Resource)
	return copied
}

func cloneResource(resource any) any {
	if resource == nil {
		return nil
	}
	switch value := resource.(type) {
	case adapter.RequestRecord:
		return cloneRequest(value)
	case validation.WorkItem:
		return cloneWorkItem(value)
	case adapter.WorkItemProjection:
		return value
	default:
		return reflect.ValueOf(resource).Interface()
	}
}

func markReplayed(result adapter.Result) adapter.Result {
	result.Outcome = adapter.OutcomeReplayed
	result.Mutation = adapter.MutationNone
	result.Idempotency = adapter.IdempotencyReplayed
	if result.Decision != nil {
		result.Decision.Replayed = true
	}
	return result
}

func isBlank(value string) bool {
	return len(bytes.TrimSpace([]byte(value))) == 0
}
