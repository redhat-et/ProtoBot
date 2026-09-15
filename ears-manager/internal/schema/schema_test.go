package schema

import (
	"errors"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
)

func TestValidateAcceptsCurrentVersions(t *testing.T) {
	err := Validate(records.SchemaVersions{
		Project:       records.CurrentProjectSchemaVersion,
		Specification: records.CurrentSpecificationSchemaVersion,
	})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateRejectsUnsupportedVersions(t *testing.T) {
	tests := []struct {
		name  string
		input records.SchemaVersions
		store string
	}{
		{
			name:  "newer project",
			input: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion + 1, Specification: records.CurrentSpecificationSchemaVersion},
			store: "project",
		},
		{
			name:  "newer specification",
			input: records.SchemaVersions{Project: records.CurrentProjectSchemaVersion, Specification: records.CurrentSpecificationSchemaVersion + 1},
			store: "specification",
		},
		{
			name:  "missing project",
			input: records.SchemaVersions{Specification: records.CurrentSpecificationSchemaVersion},
			store: "project",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var versionError *VersionError
			if err := Validate(test.input); !errors.As(err, &versionError) {
				t.Fatalf("Validate error = %v, want VersionError", err)
			}
			if versionError.Store != test.store {
				t.Fatalf("VersionError.Store = %q, want %q", versionError.Store, test.store)
			}
		})
	}
}
