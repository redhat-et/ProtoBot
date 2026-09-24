package validation

import (
	"slices"
	"strings"
	"time"
)

var roleOperations = map[Role]map[Operation]struct{}{
	RoleHumanMaintainer: operationSet(OperationAbandon),
	RoleDraftingTable:   operationSet(OperationLifecyclePreflight),
	RoleMaterializer: operationSet(
		OperationMaterialize,
		OperationRefreshDependencies,
		OperationRevalidate,
		OperationResolveBlock,
	),
	RoleJobSite: operationSet(
		OperationClaim,
		OperationRenewLease,
		OperationTestsPass,
		OperationRaiseSpecQuestion,
		OperationRefreshActive,
		OperationReturnToBuilding,
		OperationBeginMerge,
		OperationMergeConflict,
		OperationRecordMerge,
	),
	RoleReconciler: operationSet(
		OperationRecoverLease,
		OperationMergeConflict,
		OperationMergeNotApplied,
		OperationRecordMerge,
	),
}

func operationSet(operations ...Operation) map[Operation]struct{} {
	result := make(map[Operation]struct{}, len(operations))
	for _, operation := range operations {
		result[operation] = struct{}{}
	}
	return result
}

// KnownRole reports whether role is part of the closed MVP authorization
// vocabulary.
func KnownRole(role Role) bool {
	_, ok := roleOperations[role]
	return ok
}

// RoleAllows reports whether role may request the authoritative operation.
func RoleAllows(role Role, operation Operation) bool {
	_, ok := roleOperations[role][operation]
	return ok
}

// ValidateAuthorizationContext checks the complete trusted Gate context
// before a WMS target is inspected. Action-family and payload-ref checks are
// deliberately left to AuthorizeContext, after target visibility is known.
func ValidateAuthorizationContext(auth AuthorizationContext, action Operation, projectID, workItemID, changeSetID string, now time.Time) *Rejection {
	if now.IsZero() || auth.Subject == "" || !KnownRole(auth.Role) || auth.ProjectID == "" ||
		auth.ProjectID != projectID || auth.PolicyVersion == "" ||
		auth.ExpiresAt.IsZero() || !now.Before(auth.ExpiresAt) ||
		len(auth.AllowedActions) == 0 || len(auth.AllowedRefs) == 0 ||
		hasWildcardAction(auth.AllowedActions) || hasWildcardRef(auth.AllowedRefs) {
		return unauthorizedFor(auth, action, workItemID)
	}
	if auth.WorkItemID != "" && auth.WorkItemID != workItemID {
		return unauthorizedFor(auth, action, workItemID)
	}
	if auth.ChangeSetID != "" && auth.ChangeSetID != changeSetID {
		return unauthorizedFor(auth, action, workItemID)
	}
	return nil
}

// AuthorizeContext validates the trusted context, requested action, and
// payload-reference scope. Operation-specific role families are checked by
// AuthorizeLifecycle.
func AuthorizeContext(auth AuthorizationContext, action Operation, projectID, workItemID, changeSetID string, refs []string, now time.Time) *Rejection {
	if rejection := ValidateAuthorizationContext(auth, action, projectID, workItemID, changeSetID, now); rejection != nil {
		return rejection
	}
	if !slices.Contains(auth.AllowedActions, action) {
		return unauthorizedFor(auth, action, workItemID)
	}

	requiredRefs := append([]string{"project:" + projectID}, refs...)
	for _, ref := range requiredRefs {
		if !slices.Contains(auth.AllowedRefs, ref) {
			return unauthorizedFor(auth, action, workItemID)
		}
	}
	return nil
}

// AuthorizeLifecycle validates a lifecycle command's action scope and
// exclusive role family. A WMS boundary calls it after target visibility is
// established and before consulting idempotency state.
func AuthorizeLifecycle(auth AuthorizationContext, operation Operation, projectID, workItemID, changeSetID string, refs []string, now time.Time) *Rejection {
	if rejection := AuthorizeContext(auth, operation, projectID, workItemID, changeSetID, refs, now); rejection != nil {
		return rejection
	}
	if !RoleAllows(auth.Role, operation) {
		return unauthorizedFor(auth, operation, workItemID)
	}
	return nil
}

// AuthorizePreflight permits a caller with the read-only preflight action to
// evaluate a proposed lifecycle operation without granting that mutation.
func AuthorizePreflight(auth AuthorizationContext, projectID, workItemID, changeSetID string, refs []string, now time.Time) *Rejection {
	if rejection := AuthorizeContext(auth, OperationLifecyclePreflight, projectID, workItemID, changeSetID, refs, now); rejection != nil {
		return rejection
	}
	switch auth.Role {
	case RoleDraftingTable, RoleJobSite, RoleMaterializer:
		return nil
	default:
		return unauthorizedFor(auth, OperationLifecyclePreflight, workItemID)
	}
}

// ValidateApproval checks every Gate binding that makes an approval safe to
// consume for one mutation.
func ValidateApproval(approval ApprovalRecord, requirement ApprovalRequirement, now time.Time) *Rejection {
	valid := approval.ApprovedSubject != "" &&
		approval.DelegatedPrincipal != "" &&
		approval.DelegatedPrincipal == requirement.DelegatedPrincipal &&
		approval.Action == requirement.Action &&
		approval.Digest != "" && approval.Digest == requirement.Digest &&
		approval.Status == ApprovalStatusUnused &&
		!approval.ExpiresAt.IsZero() && now.Before(approval.ExpiresAt)
	bindingsMatch := approvalBindingMatches(approval, requirement)
	valid = valid && bindingsMatch
	if valid {
		return nil
	}
	return &Rejection{
		Code:    CodeUnauthorizedAction,
		Message: "The approval is missing, expired, consumed, revoked, or bound to a different action.",
		Details: map[string]any{
			"required_action": requirement.Action,
			"target_type":     approvalTargetType(requirement),
			"policy_version":  requirement.PolicyVersion,
		},
		Retry: RetryAuthorize,
	}
}

func approvalBindingMatches(approval ApprovalRecord, requirement ApprovalRequirement) bool {
	return matchesOptionalBinding(approval.ProjectID, requirement.ProjectID) &&
		matchesOptionalBinding(approval.WorkItemID, requirement.WorkItemID) &&
		matchesOptionalBinding(approval.RequestID, requirement.RequestID) &&
		matchesOptionalBinding(approval.ResolutionKind, requirement.ResolutionKind) &&
		matchesOptionalBinding(string(approval.ExpectedState), string(requirement.ExpectedState)) &&
		matchesApprovalVersion(approval.ExpectedContractVersion, requirement.ExpectedContractVersion) &&
		matchesOptionalBinding(approval.PolicyVersion, requirement.PolicyVersion)
}

func matchesOptionalBinding(actual, required string) bool {
	if required == "" {
		return true
	}
	return actual == required
}

func matchesApprovalVersion(actual, required *uint64) bool {
	return required == nil || actual != nil && *actual == *required
}

func unauthorizedFor(auth AuthorizationContext, action Operation, workItemID string) *Rejection {
	targetType := "project"
	if workItemID != "" {
		targetType = "work-item"
	}
	return &Rejection{
		Code:    CodeUnauthorizedAction,
		Message: "The trusted authorization context does not permit this action or target.",
		Details: map[string]any{
			"required_action": action,
			"target_type":     targetType,
			"policy_version":  auth.PolicyVersion,
		},
		Retry: RetryAuthorize,
	}
}

func approvalTargetType(requirement ApprovalRequirement) string {
	if requirement.WorkItemID != "" {
		return "work-item"
	}
	if requirement.RequestID != "" {
		return "request"
	}
	return "project"
}

func hasWildcardAction(actions []Operation) bool {
	return slices.ContainsFunc(actions, func(action Operation) bool {
		return isWildcard(string(action))
	})
}

func hasWildcardRef(refs []string) bool {
	return slices.ContainsFunc(refs, isWildcard)
}

func isWildcard(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "*", "all":
		return true
	default:
		return false
	}
}
