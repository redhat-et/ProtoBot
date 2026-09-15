package records

import (
	"fmt"
	"regexp"
	"sort"
)

const (
	CurrentProjectSchemaVersion       = 1
	CurrentSpecificationSchemaVersion = 1
)

type StoreKind string

const (
	RequirementStore StoreKind = "requirements"
	InterfaceStore   StoreKind = "interfaces"
	ChangeSetStore   StoreKind = "change-sets"
)

type SchemaVersions struct {
	Project       int `yaml:"project"`
	Specification int `yaml:"specification"`
}

type StorePaths struct {
	Requirements string `yaml:"requirements"`
	Interfaces   string `yaml:"interfaces"`
	ChangeSets   string `yaml:"change_sets"`
}

func (p StorePaths) WithDefaults() StorePaths {
	if p.Requirements == "" {
		p.Requirements = ".protobot/requirements"
	}
	if p.Interfaces == "" {
		p.Interfaces = ".protobot/interfaces"
	}
	if p.ChangeSets == "" {
		p.ChangeSets = ".protobot/change-sets"
	}
	return p
}

type ProjectIdentity struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

type RepositoryConfig struct {
	CanonicalRemote string `yaml:"canonical_remote"`
	DefaultBranch   string `yaml:"default_branch"`
	ReviewMode      string `yaml:"review_mode"`
	BranchPrefix    string `yaml:"branch_prefix"`
}

type ArtifactKind string

const (
	ArtifactVision           ArtifactKind = "vision"
	ArtifactArchitecture     ArtifactKind = "architecture"
	ArtifactInterfaceIDL     ArtifactKind = "interface-idl"
	ArtifactInterfaceProse   ArtifactKind = "interface-prose"
	ArtifactRequirementStore ArtifactKind = "requirement-store"
	ArtifactChangeSet        ArtifactKind = "change-set"
)

type ArtifactEntry struct {
	ID        string       `yaml:"id"`
	Kind      ArtifactKind `yaml:"kind"`
	Path      string       `yaml:"path"`
	Digest    string       `yaml:"digest"`
	Owner     string       `yaml:"owner"`
	Validator string       `yaml:"validator,omitempty"`
}

type ProjectConfig struct {
	Project        ProjectIdentity  `yaml:"project"`
	Repository     RepositoryConfig `yaml:"repository"`
	SchemaVersions SchemaVersions   `yaml:"schema_versions"`
	Stores         StorePaths       `yaml:"stores"`
	Artifacts      []ArtifactEntry  `yaml:"artifacts,omitempty"`
}

type EARSStyle string

const (
	EARSUbiquitous       EARSStyle = "ubiquitous"
	EARSEventDriven      EARSStyle = "event-driven"
	EARSStateDriven      EARSStyle = "state-driven"
	EARSUnwantedBehavior EARSStyle = "unwanted-behavior"
	EARSOptionalFeature  EARSStyle = "optional-feature"
	EARSComplex          EARSStyle = "complex"
)

type Provenance string

const (
	ProvenanceUserAuthored   Provenance = "user-authored"
	ProvenanceAgentSuggested Provenance = "agent-suggested"
	ProvenanceKitImported    Provenance = "kit-imported"
)

type VerificationMode string

const (
	VerificationIsolatedInterface   VerificationMode = "isolated-interface"
	VerificationImplementationAware VerificationMode = "implementation-aware"
)

type RecordStatus string

const (
	StatusActive  RecordStatus = "active"
	StatusRetired RecordStatus = "retired"
)

type Applicability struct {
	Interfaces []string `yaml:"interfaces,omitempty"`
	Scopes     []string `yaml:"scopes,omitempty"`
}

type Verification struct {
	Mode      VerificationMode `yaml:"mode"`
	Rationale string           `yaml:"rationale,omitempty"`
}

type Relationship struct {
	Type   string `yaml:"type"`
	Target string `yaml:"target"`
}

type Requirement struct {
	ID            string         `yaml:"id"`
	Type          EARSStyle      `yaml:"type"`
	Text          string         `yaml:"text"`
	AppliesTo     Applicability  `yaml:"applies_to"`
	Verification  Verification   `yaml:"verification"`
	Provenance    Provenance     `yaml:"provenance"`
	Created       string         `yaml:"created"`
	Relationships []Relationship `yaml:"relationships,omitempty"`
	Status        RecordStatus   `yaml:"status,omitempty"`
}

type InterfaceType string

const (
	InterfaceNetworkService  InterfaceType = "network-service"
	InterfaceCLI             InterfaceType = "cli"
	InterfaceREPL            InterfaceType = "repl"
	InterfaceLinkableLibrary InterfaceType = "linkable-library"
	InterfaceWebGUI          InterfaceType = "web-gui"
	InterfaceNativeGUI       InterfaceType = "native-gui"
	InterfacePersistentState InterfaceType = "persistent-state"
	InterfacePackageSource   InterfaceType = "package-source"
)

type InterfaceRecord struct {
	ID           string        `yaml:"id"`
	Name         string        `yaml:"name"`
	Type         InterfaceType `yaml:"type"`
	SpecApproach string        `yaml:"spec_approach,omitempty"`
	Description  string        `yaml:"description,omitempty"`
	Created      string        `yaml:"created"`
	Status       RecordStatus  `yaml:"status,omitempty"`
}

type RequirementOperation struct {
	Action        string `yaml:"action"`
	RequirementID string `yaml:"requirement_id"`
	Rationale     string `yaml:"rationale,omitempty"`
}

type InterfaceOperation struct {
	Action      string `yaml:"action"`
	InterfaceID string `yaml:"interface_id"`
	Rationale   string `yaml:"rationale,omitempty"`
}

type ArtifactOperation struct {
	Action     string `yaml:"action"`
	ArtifactID string `yaml:"artifact_id"`
	Rationale  string `yaml:"rationale,omitempty"`
}

type ImpactAssessment struct {
	RequirementID string `yaml:"requirement_id"`
	Disposition   string `yaml:"disposition"`
	Rationale     string `yaml:"rationale"`
	Origin        string `yaml:"origin"`
}

type ChangeSet struct {
	ID                      string                 `yaml:"id"`
	BaseCommit              string                 `yaml:"base_commit"`
	Intent                  string                 `yaml:"intent"`
	Operations              []RequirementOperation `yaml:"operations"`
	InterfaceOperations     []InterfaceOperation   `yaml:"interface_operations,omitempty"`
	ArtifactOperations      []ArtifactOperation    `yaml:"artifact_operations,omitempty"`
	AffectedInterfaces      []string               `yaml:"affected_interfaces"`
	AffectedScopes          []string               `yaml:"affected_scopes,omitempty"`
	ImplementationRequired  bool                   `yaml:"implementation_required"`
	ImplementationRationale string                 `yaml:"implementation_rationale,omitempty"`
	ImpactAssessment        []ImpactAssessment     `yaml:"impact_assessment,omitempty"`
	Created                 string                 `yaml:"created"`
}

var (
	requirementIDPattern = regexp.MustCompile(`^REQ-[A-Z][A-Z0-9-]*-[0-9]{5}$`)
	interfaceIDPattern   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	changeSetIDPattern   = regexp.MustCompile(`^CS-[0-9]{5}$`)
	artifactIDPattern    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

func ValidateRequirementID(id string) error {
	if !requirementIDPattern.MatchString(id) {
		return fmt.Errorf("invalid requirement id %q", id)
	}
	return nil
}

func ValidateInterfaceID(id string) error {
	if !interfaceIDPattern.MatchString(id) {
		return fmt.Errorf("invalid interface id %q", id)
	}
	return nil
}

func ValidateChangeSetID(id string) error {
	if !changeSetIDPattern.MatchString(id) {
		return fmt.Errorf("invalid change-set id %q", id)
	}
	return nil
}

func ValidateArtifactID(id string) error {
	if !artifactIDPattern.MatchString(id) {
		return fmt.Errorf("invalid artifact id %q", id)
	}
	return nil
}

func FilenameFor(kind StoreKind, id string) (string, error) {
	switch kind {
	case RequirementStore:
		if err := ValidateRequirementID(id); err != nil {
			return "", err
		}
		return id + ".yaml", nil
	case InterfaceStore:
		if err := ValidateInterfaceID(id); err != nil {
			return "", err
		}
		return id + ".yaml", nil
	case ChangeSetStore:
		if err := ValidateChangeSetID(id); err != nil {
			return "", err
		}
		return "cs-" + id[len("CS-"):] + ".yaml", nil
	default:
		return "", fmt.Errorf("unknown store kind %q", kind)
	}
}

func CanonicalRequirement(value Requirement) Requirement {
	value.AppliesTo.Interfaces = sortedStrings(value.AppliesTo.Interfaces)
	value.AppliesTo.Scopes = sortedStrings(value.AppliesTo.Scopes)
	value.Relationships = append([]Relationship(nil), value.Relationships...)
	sort.Slice(value.Relationships, func(i, j int) bool {
		if value.Relationships[i].Type == value.Relationships[j].Type {
			return value.Relationships[i].Target < value.Relationships[j].Target
		}
		return value.Relationships[i].Type < value.Relationships[j].Type
	})
	if value.Verification.Mode == "" {
		value.Verification.Mode = VerificationIsolatedInterface
	}
	if value.Status == "" {
		value.Status = StatusActive
	}
	return value
}

func CanonicalInterface(value InterfaceRecord) InterfaceRecord {
	if value.Status == "" {
		value.Status = StatusActive
	}
	return value
}

func CanonicalChangeSet(value ChangeSet) ChangeSet {
	value.Operations = append([]RequirementOperation(nil), value.Operations...)
	sort.Slice(value.Operations, func(i, j int) bool {
		return lessStrings(
			value.Operations[i].Action,
			value.Operations[i].RequirementID,
			value.Operations[i].Rationale,
			value.Operations[j].Action,
			value.Operations[j].RequirementID,
			value.Operations[j].Rationale,
		)
	})
	value.InterfaceOperations = append([]InterfaceOperation(nil), value.InterfaceOperations...)
	sort.Slice(value.InterfaceOperations, func(i, j int) bool {
		return lessStrings(
			value.InterfaceOperations[i].Action,
			value.InterfaceOperations[i].InterfaceID,
			value.InterfaceOperations[i].Rationale,
			value.InterfaceOperations[j].Action,
			value.InterfaceOperations[j].InterfaceID,
			value.InterfaceOperations[j].Rationale,
		)
	})
	value.ArtifactOperations = append([]ArtifactOperation(nil), value.ArtifactOperations...)
	sort.Slice(value.ArtifactOperations, func(i, j int) bool {
		return lessStrings(
			value.ArtifactOperations[i].Action,
			value.ArtifactOperations[i].ArtifactID,
			value.ArtifactOperations[i].Rationale,
			value.ArtifactOperations[j].Action,
			value.ArtifactOperations[j].ArtifactID,
			value.ArtifactOperations[j].Rationale,
		)
	})
	value.AffectedInterfaces = sortedStrings(value.AffectedInterfaces)
	value.AffectedScopes = sortedStrings(value.AffectedScopes)
	value.ImpactAssessment = append([]ImpactAssessment(nil), value.ImpactAssessment...)
	sort.Slice(value.ImpactAssessment, func(i, j int) bool {
		return lessStrings(
			value.ImpactAssessment[i].RequirementID,
			value.ImpactAssessment[i].Disposition,
			value.ImpactAssessment[i].Rationale,
			value.ImpactAssessment[i].Origin,
			value.ImpactAssessment[j].RequirementID,
			value.ImpactAssessment[j].Disposition,
			value.ImpactAssessment[j].Rationale,
			value.ImpactAssessment[j].Origin,
		)
	})
	return value
}

func CanonicalProjectConfig(value ProjectConfig) ProjectConfig {
	value.Stores = value.Stores.WithDefaults()
	value.Artifacts = append([]ArtifactEntry(nil), value.Artifacts...)
	sort.Slice(value.Artifacts, func(i, j int) bool {
		return lessStrings(
			value.Artifacts[i].ID,
			string(value.Artifacts[i].Kind),
			value.Artifacts[i].Path,
			value.Artifacts[i].Digest,
			value.Artifacts[i].Owner,
			value.Artifacts[i].Validator,
			value.Artifacts[j].ID,
			string(value.Artifacts[j].Kind),
			value.Artifacts[j].Path,
			value.Artifacts[j].Digest,
			value.Artifacts[j].Owner,
			value.Artifacts[j].Validator,
		)
	})
	return value
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func lessStrings(values ...string) bool {
	if len(values)%2 != 0 {
		return false
	}
	half := len(values) / 2
	for i := 0; i < half; i++ {
		if values[i] == values[half+i] {
			continue
		}
		return values[i] < values[half+i]
	}
	return false
}
