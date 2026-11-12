// Package records holds the ProtoBot V2 requirement and interface
// structures parsed from spec/requirements and spec/interfaces.
//
// These types omit V1 metadata (verification, provenance, created).
// The layout reader does not require those fields.
package records

// EARS type values from the V2 requirement record.
const (
	TypeUbiquitous       = "ubiquitous"
	TypeEventDriven      = "event-driven"
	TypeStateDriven      = "state-driven"
	TypeUnwantedBehavior = "unwanted-behavior"
	TypeOptionalFeature  = "optional-feature"
	TypeComplex          = "complex"
)

// Requirement lifecycle values.
const (
	StatusActive  = "active"
	StatusRetired = "retired"
)

// Relationship type values accepted by later validation.
const (
	RelDependsOn  = "depends-on"
	RelSupersedes = "supersedes"
	RelRelatedTo  = "related-to"
)

// Requirement is one V2 requirement record.
type Requirement struct {
	ID            string         `yaml:"id"`
	Type          string         `yaml:"type"`
	Text          string         `yaml:"text"`
	AppliesTo     Applicability  `yaml:"applies_to"`
	Relationships []Relationship `yaml:"relationships"`
	Status        string         `yaml:"status"`
}

// Applicability names the interfaces and scopes a requirement applies to.
type Applicability struct {
	Interfaces []string `yaml:"interfaces"`
	Scopes     []string `yaml:"scopes"`
}

// Relationship is a typed link from one requirement to another.
type Relationship struct {
	Type   string `yaml:"type"`
	Target string `yaml:"target"`
}

// InterfaceRecord is one V2 named external interface record.
type InterfaceRecord struct {
	Name        string `yaml:"name"`
	Kind        string `yaml:"kind"`
	Description string `yaml:"description"`
}
