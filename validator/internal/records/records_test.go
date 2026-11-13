package records

import "testing"

func TestValidateRequirementID(t *testing.T) {
	valid := []string{
		"REQ-CORE-00001",
		"REQ-API-00042",
		"REQ-A-00000",
		"REQ-A1-00001",
		"REQ-A-B-00001",
	}
	for _, id := range valid {
		if err := ValidateRequirementID(id); err != nil {
			t.Errorf("ValidateRequirementID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",
		"REQ-api-00001",
		"REQ-API-1",
		"REQ-API-0001",
		"REQ-API-000001",
		"API-00001",
		"req-API-00001",
		"REQ--00001",
		"REQ-00001",
		"REQ-API-0000A",
		"REQ-API-00001.yaml",
		"REQ-API-00001 ",
	}
	for _, id := range invalid {
		if err := ValidateRequirementID(id); err == nil {
			t.Errorf("ValidateRequirementID(%q) = nil, want error", id)
		}
	}
}
