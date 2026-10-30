package jobsite

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeFeedbackRouting(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome string
		fault   Fault
		roles   []string
	}{
		{"pass", "pass", FaultNone, []string{RoleWorkerA, RoleWorkerB}},
		{"test", "fail", FaultTest, []string{RoleWorkerA}},
		{"implementation", "fail", FaultImplementation, []string{RoleWorkerB}},
		{"both", "fail", FaultBoth, []string{RoleWorkerA, RoleWorkerB}},
		{"specification gap", "blocked", FaultSpecificationGap, []string{RoleWorkerA, RoleWorkerB}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := TriageInput{Outcome: test.outcome, Fault: test.fault, RequirementIDs: []string{"REQ-2", "REQ-1", "REQ-2"}}
			feedback, redactions, err := SanitizeFeedback(input, []string{"REQ-1", "REQ-2"}, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(feedback) != len(test.roles) || len(redactions) != len(test.roles) {
				t.Fatalf("outputs = %#v, %#v, want roles %#v", feedback, redactions, test.roles)
			}
			for i, role := range test.roles {
				got := feedback[i]
				if got.Role != role || got.Outcome != test.outcome || got.Fault != test.fault || got.RetriesRemaining != 2 {
					t.Errorf("feedback = %#v", got)
				}
				if !reflect.DeepEqual(got.RequirementIDs, []string{"REQ-1", "REQ-2"}) {
					t.Errorf("requirement IDs = %#v", got.RequirementIDs)
				}
				if test.outcome == "fail" && got.Request == "" {
					t.Error("failed Worker has no revision request")
				}
				if test.outcome != "fail" && got.Request != "" {
					t.Errorf("terminal outcome requests another revision: %q", got.Request)
				}
				if redactions[i].Role != role || !redactions[i].GenericReason {
					t.Errorf("redaction = %#v", redactions[i])
				}
			}
			if !reflect.DeepEqual(input.RequirementIDs, []string{"REQ-2", "REQ-1", "REQ-2"}) {
				t.Fatal("sanitizer mutated private input")
			}
		})
	}
}

func TestSanitizeFeedbackDropsAllFreeText(t *testing.T) {
	input := TriageInput{
		Outcome: "fail", Fault: FaultBoth, RequirementIDs: []string{"REQ-CLI-00001"},
		Reason:            "peer/src/secret.go asserts expected=oracle actual=observed deadbeef",
		RawLogs:           "raw-log-sentinel stack trace implementation-behavior mutation-details",
		InspectorFindings: "raw-inspector-sentinel canonical assertion source",
	}
	feedback, redactions, err := SanitizeFeedback(input, []string{"REQ-CLI-00001"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(feedback)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"peer/", "secret.go", "asserts", "oracle", "observed", "deadbeef", "raw-log-sentinel", "stack trace", "implementation-behavior", "mutation-details", "raw-inspector-sentinel", "canonical assertion source"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("feedback leaks %q: %s", forbidden, encoded)
		}
	}
	for _, redaction := range redactions {
		if !reflect.DeepEqual(redaction.DiscardedFields, []string{"inspector_findings", "raw_logs", "reason"}) {
			t.Errorf("discarded fields = %#v", redaction.DiscardedFields)
		}
	}
	// Private evidence remains available to the audit writer.
	if !strings.Contains(input.RawLogs, "raw-log-sentinel") {
		t.Fatal("raw evidence was lost")
	}
	// Recipient records cannot affect each other after construction.
	feedback[0].RequirementIDs[0] = "REQ-CHANGED"
	redactions[0].DiscardedFields[0] = "changed"
	if feedback[1].RequirementIDs[0] != "REQ-CLI-00001" || redactions[1].DiscardedFields[0] != "inspector_findings" {
		t.Fatal("recipient records share mutable slices")
	}
}

func TestSanitizeFeedbackRejectsUnsafeMetadata(t *testing.T) {
	valid := TriageInput{Outcome: "fail", Fault: FaultTest, RequirementIDs: []string{"REQ-1"}}
	for _, test := range []struct {
		name     string
		input    TriageInput
		approved []string
		budget   int
	}{
		{"unknown outcome", TriageInput{Outcome: "peer-secret", Fault: FaultTest}, []string{"REQ-1"}, 1},
		{"unknown fault", TriageInput{Outcome: "fail", Fault: "peer-secret"}, []string{"REQ-1"}, 1},
		{"passing fault", TriageInput{Outcome: "pass", Fault: FaultTest}, []string{"REQ-1"}, 1},
		{"failing no fault", TriageInput{Outcome: "fail", Fault: FaultNone}, []string{"REQ-1"}, 1},
		{"gap not blocked", TriageInput{Outcome: "fail", Fault: FaultSpecificationGap}, []string{"REQ-1"}, 1},
		{"blocked test", TriageInput{Outcome: "blocked", Fault: FaultTest}, []string{"REQ-1"}, 1},
		{"unknown requirement", TriageInput{Outcome: "fail", Fault: FaultTest, RequirementIDs: []string{"REQ-peer-secret"}}, []string{"REQ-1"}, 1},
		{"unsafe approved", valid, []string{"REQ-1", "REQ-peer-secret\nexpected=42"}, 1},
		{"empty affected", TriageInput{Outcome: "fail", Fault: FaultTest}, []string{"REQ-1"}, 1},
		{"negative budget", valid, []string{"REQ-1"}, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			feedback, redactions, err := SanitizeFeedback(test.input, test.approved, test.budget)
			if err == nil || errorCode(err) != CodeTriageInvalid || feedback != nil || redactions != nil {
				t.Fatalf("got %#v, %#v, %v; want fail-closed rejection", feedback, redactions, err)
			}
			if strings.Contains(err.Error(), "peer-secret") {
				t.Fatalf("error quotes untrusted input: %v", err)
			}
		})
	}
}

func TestSanitizeFeedbackDeterministicExhaustion(t *testing.T) {
	input := TriageInput{Outcome: "fail", Fault: FaultBoth, RequirementIDs: []string{"REQ-1"}, Reason: "private"}
	first, firstRedactions, err := SanitizeFeedback(input, []string{"REQ-1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, secondRedactions, err := SanitizeFeedback(input, []string{"REQ-1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstRedactions, secondRedactions) {
		t.Fatal("same input did not produce identical outputs")
	}
	for _, feedback := range first {
		if feedback.RetriesRemaining != 0 || feedback.Request != "Stop; the retry budget is exhausted." {
			t.Errorf("exhausted feedback = %#v", feedback)
		}
	}
}
