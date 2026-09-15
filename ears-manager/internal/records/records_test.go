package records

import "testing"

func TestFilenameFor(t *testing.T) {
	tests := []struct {
		name string
		kind StoreKind
		id   string
		want string
	}{
		{name: "requirement", kind: RequirementStore, id: "REQ-AUTH-00001", want: "REQ-AUTH-00001.yaml"},
		{name: "interface", kind: InterfaceStore, id: "api-gateway", want: "api-gateway.yaml"},
		{name: "change set", kind: ChangeSetStore, id: "CS-00007", want: "cs-00007.yaml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := FilenameFor(test.kind, test.id)
			if err != nil {
				t.Fatalf("FilenameFor returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("FilenameFor = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFilenameForRejectsUnsafeIDs(t *testing.T) {
	tests := []struct {
		name string
		kind StoreKind
		id   string
	}{
		{name: "requirement traversal", kind: RequirementStore, id: "REQ-../001"},
		{name: "interface traversal", kind: InterfaceStore, id: "../outside"},
		{name: "change set wrong case", kind: ChangeSetStore, id: "cs-00001"},
		{name: "unknown store", kind: StoreKind("unknown"), id: "value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := FilenameFor(test.kind, test.id); err == nil {
				t.Fatal("FilenameFor accepted an unsafe identifier")
			}
		})
	}
}
