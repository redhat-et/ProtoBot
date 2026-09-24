package memory

import (
	"slices"

	"github.com/redhat-et/protobot/wms/validation"
)

var draftingTableOperations = []string{
	"request.create",
	"request.refine",
	"request.link-change-set",
	"request.link-build-work-item",
	"request.get",
	"request.query",
	"work-item.get",
	"work-item.query",
	"blocked-work.query",
	"lifecycle.preflight",
	"blocked-work.submit-resolution",
	"blocked-work.acknowledge",
}

var humanMaintainerOperations = []string{"request.update-priority"}

var jobSiteOperations = []string{
	"claim",
	"renew-lease",
	"tests-pass",
	"raise-spec-question",
	"refresh-active",
	"return-to-building",
	"begin-merge",
	"merge-conflict",
	"record-merge",
	"finding.create",
}

var materializerOperations = []string{
	"materialize",
	"refresh-dependencies",
	"revalidate",
	"resolve-block",
}

var reconcilerOperations = []string{
	"recover-lease",
	"merge-conflict",
	"merge-not-applied",
	"record-merge",
}

var adapterOperations = []string{
	"request.create",
	"request.refine",
	"request.update-priority",
	"request.link-change-set",
	"request.link-build-work-item",
	"request.get",
	"request.query",
	"work-item.get",
	"work-item.query",
	"blocked-work.query",
	"lifecycle.preflight",
	"blocked-work.submit-resolution",
	"blocked-work.acknowledge",
	"materialize",
	"refresh-dependencies",
	"revalidate",
	"resolve-block",
	"claim",
	"renew-lease",
	"tests-pass",
	"raise-spec-question",
	"refresh-active",
	"return-to-building",
	"begin-merge",
	"merge-conflict",
	"merge-not-applied",
	"record-merge",
	"recover-lease",
	"abandon",
	"finding.create",
}

// DraftingTableOperations returns the exact request/query/preflight surface
// granted to the Drafting Table.
func DraftingTableOperations() []string {
	return append([]string(nil), draftingTableOperations...)
}

// HumanMaintainerOperations returns the WMS request operations directly
// invoked by a trusted human-maintainer client.
func HumanMaintainerOperations() []string {
	return append([]string(nil), humanMaintainerOperations...)
}

// JobSiteOperations returns the Job Site execution operation set.
func JobSiteOperations() []string {
	return append([]string(nil), jobSiteOperations...)
}

// MaterializerOperations returns the Materializer lifecycle operation set.
func MaterializerOperations() []string {
	return append([]string(nil), materializerOperations...)
}

// ReconcilerOperations returns the reconciliation operation set.
func ReconcilerOperations() []string {
	return append([]string(nil), reconcilerOperations...)
}

// AdapterOperations returns the complete declared in-memory adapter API.
func AdapterOperations() []string {
	return append([]string(nil), adapterOperations...)
}

func roleHasWMSOperation(role validation.Role, operation string) bool {
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
