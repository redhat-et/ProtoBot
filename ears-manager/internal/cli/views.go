package cli

import "github.com/redhat-et/protobot/ears-manager/internal/records"

type requirementJSON struct {
	ID            string               `json:"id"`
	Type          records.EARSStyle    `json:"type"`
	Text          string               `json:"text"`
	AppliesTo     applicabilityJSON    `json:"applies_to"`
	Verification  verificationJSON     `json:"verification"`
	Provenance    records.Provenance   `json:"provenance"`
	Created       string               `json:"created"`
	Relationships []relationshipJSON   `json:"relationships,omitempty"`
	Status        records.RecordStatus `json:"status,omitempty"`
}

type applicabilityJSON struct {
	Interfaces []string `json:"interfaces,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
}

type verificationJSON struct {
	Mode      records.VerificationMode `json:"mode"`
	Rationale string                   `json:"rationale,omitempty"`
}

type relationshipJSON struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

func toRequirementJSON(value records.Requirement) requirementJSON {
	value = records.CanonicalRequirement(value)
	result := requirementJSON{
		ID:   value.ID,
		Type: value.Type,
		Text: value.Text,
		AppliesTo: applicabilityJSON{
			Interfaces: append([]string(nil), value.AppliesTo.Interfaces...),
			Scopes:     append([]string(nil), value.AppliesTo.Scopes...),
		},
		Verification: verificationJSON{Mode: value.Verification.Mode, Rationale: value.Verification.Rationale},
		Provenance:   value.Provenance,
		Created:      value.Created,
		Status:       value.Status,
	}
	for _, relationship := range value.Relationships {
		result.Relationships = append(result.Relationships, relationshipJSON{Type: relationship.Type, Target: relationship.Target})
	}
	return result
}

type interfaceJSON struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Type         records.InterfaceType `json:"type"`
	SpecApproach string                `json:"spec_approach,omitempty"`
	Description  string                `json:"description,omitempty"`
	Created      string                `json:"created"`
	Status       records.RecordStatus  `json:"status,omitempty"`
}

func toInterfaceJSON(value records.InterfaceRecord) interfaceJSON {
	value = records.CanonicalInterface(value)
	return interfaceJSON{
		ID:           value.ID,
		Name:         value.Name,
		Type:         value.Type,
		SpecApproach: value.SpecApproach,
		Description:  value.Description,
		Created:      value.Created,
		Status:       value.Status,
	}
}

type artifactJSON struct {
	ID        string               `json:"id"`
	Kind      records.ArtifactKind `json:"kind"`
	Path      string               `json:"path"`
	Owner     string               `json:"owner"`
	Digest    string               `json:"digest"`
	Validator string               `json:"validator,omitempty"`
}

func toArtifactJSON(value records.ArtifactEntry) artifactJSON {
	return artifactJSON{
		ID:        value.ID,
		Kind:      value.Kind,
		Path:      value.Path,
		Owner:     value.Owner,
		Digest:    value.Digest,
		Validator: value.Validator,
	}
}

type requirementOperationJSON struct {
	Action        string `json:"action"`
	RequirementID string `json:"requirement_id"`
	Rationale     string `json:"rationale,omitempty"`
}

func toRequirementOperationJSON(value records.RequirementOperation) requirementOperationJSON {
	return requirementOperationJSON{Action: value.Action, RequirementID: value.RequirementID, Rationale: value.Rationale}
}

type interfaceOperationJSON struct {
	Action      string `json:"action"`
	InterfaceID string `json:"interface_id"`
	Rationale   string `json:"rationale,omitempty"`
}

func toInterfaceOperationJSON(value records.InterfaceOperation) interfaceOperationJSON {
	return interfaceOperationJSON{Action: value.Action, InterfaceID: value.InterfaceID, Rationale: value.Rationale}
}

type artifactOperationJSON struct {
	Action     string `json:"action"`
	ArtifactID string `json:"artifact_id"`
	Rationale  string `json:"rationale,omitempty"`
}

func toArtifactOperationJSON(value records.ArtifactOperation) artifactOperationJSON {
	return artifactOperationJSON{Action: value.Action, ArtifactID: value.ArtifactID, Rationale: value.Rationale}
}
