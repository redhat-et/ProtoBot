package memory

import (
	"github.com/redhat-et/protobot/wms/adapter"
	"github.com/redhat-et/protobot/wms/validation"
)

// DraftingTableOperations returns the exact request/query/preflight surface
// granted to the Drafting Table, projected onto the wire's operation-name
// strings.
func DraftingTableOperations() []string {
	return adapter.DraftingTableOperations()
}

// HumanMaintainerOperations returns the WMS request operations directly
// invoked by a trusted human-maintainer client, projected onto the wire's
// operation-name strings.
func HumanMaintainerOperations() []string {
	return adapter.HumanMaintainerOperations()
}

// JobSiteOperations returns the Job Site execution operation set, projected
// onto the wire's operation-name strings.
func JobSiteOperations() []string {
	return adapter.JobSiteOperations()
}

// MaterializerOperations returns the Materializer lifecycle operation set,
// projected onto the wire's operation-name strings.
func MaterializerOperations() []string {
	return adapter.MaterializerOperations()
}

// ReconcilerOperations returns the reconciliation operation set, projected
// onto the wire's operation-name strings.
func ReconcilerOperations() []string {
	return adapter.ReconcilerOperations()
}

// AdapterOperations returns the complete declared in-memory adapter API,
// projected onto the wire's operation-name strings.
func AdapterOperations() []string {
	return adapter.AdapterOperations()
}

func roleHasWMSOperation(role validation.Role, operation validation.Operation) bool {
	return adapter.RoleHasWMSOperation(role, operation)
}
