package schema

import (
	"fmt"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
)

type VersionError struct {
	Store     string
	Found     int
	Supported int
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("unsupported %s schema version %d; supported version is %d", e.Store, e.Found, e.Supported)
}

func Validate(versions records.SchemaVersions) error {
	if err := validateVersion("project", versions.Project, records.CurrentProjectSchemaVersion); err != nil {
		return err
	}
	return validateVersion("specification", versions.Specification, records.CurrentSpecificationSchemaVersion)
}

func validateVersion(store string, found, supported int) error {
	if found == supported {
		return nil
	}
	return &VersionError{Store: store, Found: found, Supported: supported}
}
