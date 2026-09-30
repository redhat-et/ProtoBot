package adapter

import (
	"slices"

	"github.com/redhat-et/protobot/wms/validation"
)

var draftingTableOperations = []validation.Operation{
	validation.OperationRequestCreate,
	validation.OperationRequestRefine,
	validation.OperationRequestLinkChangeSet,
	validation.OperationRequestLinkBuildWorkItem,
	validation.OperationRequestGet,
	validation.OperationRequestQuery,
	validation.OperationWorkItemGet,
	validation.OperationWorkItemQuery,
	validation.OperationBlockedWorkQuery,
	validation.OperationLifecyclePreflight,
	validation.OperationBlockedWorkSubmitResolution,
	validation.OperationBlockedWorkAcknowledge,
}

var humanMaintainerOperations = []validation.Operation{
	validation.OperationRequestUpdatePriority,
	validation.OperationRequestLinkChangeSet,
}

var jobSiteOperations = []validation.Operation{
	validation.OperationClaim,
	validation.OperationRenewLease,
	validation.OperationTestsPass,
	validation.OperationRaiseSpecQuestion,
	validation.OperationRefreshActive,
	validation.OperationReturnToBuilding,
	validation.OperationBeginMerge,
	validation.OperationMergeConflict,
	validation.OperationRecordMerge,
	validation.OperationFindingCreate,
}

var materializerOperations = []validation.Operation{
	validation.OperationMaterialize,
	validation.OperationRefreshDependencies,
	validation.OperationRevalidate,
	validation.OperationResolveBlock,
}

var reconcilerOperations = []validation.Operation{
	validation.OperationRecoverLease,
	validation.OperationMergeConflict,
	validation.OperationMergeNotApplied,
	validation.OperationRecordMerge,
}

var adapterOperations = []validation.Operation{
	validation.OperationRequestCreate,
	validation.OperationRequestRefine,
	validation.OperationRequestUpdatePriority,
	validation.OperationRequestLinkChangeSet,
	validation.OperationRequestLinkBuildWorkItem,
	validation.OperationRequestGet,
	validation.OperationRequestQuery,
	validation.OperationWorkItemGet,
	validation.OperationWorkItemQuery,
	validation.OperationBlockedWorkQuery,
	validation.OperationLifecyclePreflight,
	validation.OperationBlockedWorkSubmitResolution,
	validation.OperationBlockedWorkAcknowledge,
	validation.OperationMaterialize,
	validation.OperationRefreshDependencies,
	validation.OperationRevalidate,
	validation.OperationResolveBlock,
	validation.OperationClaim,
	validation.OperationRenewLease,
	validation.OperationTestsPass,
	validation.OperationRaiseSpecQuestion,
	validation.OperationRefreshActive,
	validation.OperationReturnToBuilding,
	validation.OperationBeginMerge,
	validation.OperationMergeConflict,
	validation.OperationMergeNotApplied,
	validation.OperationRecordMerge,
	validation.OperationRecoverLease,
	validation.OperationAbandon,
	validation.OperationFindingCreate,
}

// GitHubSubsetOperations is the #68 first-slice surface implemented by the
// GitHub translator: request reads/writes, materialization, status queries,
// and claim for compare-and-swap coverage.
var GitHubSubsetOperations = []validation.Operation{
	validation.OperationRequestCreate,
	validation.OperationRequestRefine,
	validation.OperationRequestUpdatePriority,
	validation.OperationRequestLinkChangeSet,
	validation.OperationRequestLinkBuildWorkItem,
	validation.OperationRequestGet,
	validation.OperationRequestQuery,
	validation.OperationWorkItemGet,
	validation.OperationWorkItemQuery,
	validation.OperationBlockedWorkQuery,
	validation.OperationMaterialize,
	validation.OperationClaim,
}

// DraftingTableOperations returns the exact request/query/preflight surface
// granted to the Drafting Table, projected onto the wire's operation-name
// strings.
func DraftingTableOperations() []string {
	return operationNames(draftingTableOperations)
}

// HumanMaintainerOperations returns the WMS request operations directly
// invoked by a trusted human-maintainer client, projected onto the wire's
// operation-name strings.
func HumanMaintainerOperations() []string {
	return operationNames(humanMaintainerOperations)
}

// JobSiteOperations returns the Job Site execution operation set, projected
// onto the wire's operation-name strings.
func JobSiteOperations() []string {
	return operationNames(jobSiteOperations)
}

// MaterializerOperations returns the Materializer lifecycle operation set,
// projected onto the wire's operation-name strings.
func MaterializerOperations() []string {
	return operationNames(materializerOperations)
}

// ReconcilerOperations returns the reconciliation operation set, projected
// onto the wire's operation-name strings.
func ReconcilerOperations() []string {
	return operationNames(reconcilerOperations)
}

// AdapterOperations returns the complete declared adapter API, projected onto
// the wire's operation-name strings.
func AdapterOperations() []string {
	return operationNames(adapterOperations)
}

// IsAdapterOperation reports whether operation is part of the declared API.
func IsAdapterOperation(operation validation.Operation) bool {
	return slices.Contains(adapterOperations, operation)
}

// IsGitHubSubsetOperation reports whether operation is in the #68 first slice.
func IsGitHubSubsetOperation(operation validation.Operation) bool {
	return slices.Contains(GitHubSubsetOperations, operation)
}

// RoleHasWMSOperation reports whether role may invoke operation at the adapter
// surface (before Gate AuthorizeContext).
func RoleHasWMSOperation(role validation.Role, operation validation.Operation) bool {
	switch role {
	case validation.RoleDraftingTable:
		return slices.Contains(draftingTableOperations, operation)
	case validation.RoleHumanMaintainer:
		return slices.Contains(humanMaintainerOperations, operation)
	case validation.RoleJobSite:
		return slices.Contains(jobSiteOperations, operation)
	case validation.RoleMaterializer:
		return slices.Contains(materializerOperations, operation)
	case validation.RoleReconciler:
		return slices.Contains(reconcilerOperations, operation)
	default:
		return false
	}
}

func operationNames(operations []validation.Operation) []string {
	names := make([]string, len(operations))
	for index, operation := range operations {
		names[index] = string(operation)
	}
	return names
}
