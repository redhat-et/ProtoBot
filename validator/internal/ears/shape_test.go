package ears

import (
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/records"
)

func TestCheckAcceptsValidPatterns(t *testing.T) {
	cases := []struct {
		name string
		kind string
		text string
	}{
		{name: "ubiquitous", kind: records.TypeUbiquitous, text: "The system shall provide authentication."},
		{name: "event-driven", kind: records.TypeEventDriven, text: "When a user logs in, the system shall issue a token."},
		{name: "state-driven", kind: records.TypeStateDriven, text: "While maintenance mode is active, the system shall reject writes."},
		{name: "unwanted-behavior", kind: records.TypeUnwantedBehavior, text: "If a token expires, then the system shall reject the request."},
		{name: "optional-feature", kind: records.TypeOptionalFeature, text: "Where caching is enabled, the system shall use a cache."},
		{name: "complex while-when", kind: records.TypeComplex, text: "While maintenance mode is active, when a user writes, the system shall reject the request."},
		{name: "complex where-if", kind: records.TypeComplex, text: "Where auditing is enabled, if a token expires, then the service shall reject the request."},
		{name: "event-driven folded newline", kind: records.TypeEventDriven, text: "When an unauthenticated client sends GET /health, the service shall return\nHTTP 200 with a JSON body whose status field is \"ok\"."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := Check("spec/requirements/REQ-API-00001.yaml", tc.kind, tc.text)
			if len(diagnostics) != 0 {
				t.Fatalf("valid %s produced diagnostics: %+v", tc.kind, diagnostics)
			}
		})
	}
}

func TestCheckRejectsShallCount(t *testing.T) {
	cases := []struct {
		name string
		kind string
		text string
		want int
	}{
		{
			name: "zero",
			kind: records.TypeUbiquitous,
			text: "The system must provide authentication.",
			want: 0,
		},
		{
			name: "zero event-driven",
			kind: records.TypeEventDriven,
			text: "When a user logs in, the system will issue a token.",
			want: 0,
		},
		{
			name: "multiple compound obligations",
			kind: records.TypeUbiquitous,
			text: "The system shall start and shall stop.",
			want: 2,
		},
		{
			name: "capitalized Shall does not count",
			kind: records.TypeUbiquitous,
			text: "The system Shall provide authentication.",
			want: 0,
		},
		{
			name: "shallot is not shall",
			kind: records.TypeUbiquitous,
			text: "The kitchen shallot remain available.",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "spec/requirements/REQ-SHALL-00001.yaml"
			diagnostics := Check(path, tc.kind, tc.text)
			finding := lookup(diagnostics, CodeShallCount)
			if finding == nil {
				t.Fatalf("missing %s diagnostic: %+v", CodeShallCount, diagnostics)
			}
			if finding.Path != path {
				t.Fatalf("path=%q, want %q", finding.Path, path)
			}
			if !strings.Contains(finding.Message, "want exactly 1") {
				t.Fatalf("message %q omitted expected count", finding.Message)
			}
			if got := shallCount(tc.text); got != tc.want {
				t.Fatalf("shall count=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestCheckRejectsTypeMismatch(t *testing.T) {
	cases := []struct {
		name string
		kind string
		text string
	}{
		{name: "ubiquitous uses event-driven text", kind: records.TypeUbiquitous, text: "When a user acts, the system shall respond."},
		{name: "event-driven uses ubiquitous text", kind: records.TypeEventDriven, text: "The system shall respond."},
		{name: "state-driven uses event-driven text", kind: records.TypeStateDriven, text: "When active, the system shall respond."},
		{name: "unwanted-behavior missing then", kind: records.TypeUnwantedBehavior, text: "If active, the system shall respond."},
		{name: "optional-feature uses ubiquitous text", kind: records.TypeOptionalFeature, text: "The system shall respond."},
		{name: "complex has only one trigger form", kind: records.TypeComplex, text: "When active, the system shall respond."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "spec/requirements/REQ-MISMATCH-00001.yaml"
			diagnostics := Check(path, tc.kind, tc.text)
			finding := lookup(diagnostics, CodePatternMismatch)
			if finding == nil {
				t.Fatalf("missing %s diagnostic: %+v", CodePatternMismatch, diagnostics)
			}
			if finding.Path != path {
				t.Fatalf("path=%q, want %q", finding.Path, path)
			}
			if !strings.Contains(finding.Message, tc.kind) {
				t.Fatalf("message %q omitted declared type", finding.Message)
			}
		})
	}
}

func TestCheckStateDrivenRequiresWhile(t *testing.T) {
	path := "spec/requirements/REQ-STATE-00001.yaml"
	valid := "While maintenance mode is active, the system shall reject writes."
	if diagnostics := Check(path, records.TypeStateDriven, valid); len(diagnostics) != 0 {
		t.Fatalf("canonical While form produced diagnostics: %+v", diagnostics)
	}

	cases := []struct {
		name string
		text string
	}{
		{name: "During alias", text: "During maintenance mode, the system shall reject writes."},
		{name: "lowercase while", text: "while maintenance mode is active, the system shall reject writes."},
		{name: "WHEN alias of While", text: "When maintenance mode is active, the system shall reject writes."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := Check(path, records.TypeStateDriven, tc.text)
			if lookup(diagnostics, CodePatternMismatch) == nil {
				t.Fatalf("alias %q was accepted: %+v", tc.text, diagnostics)
			}
		})
	}
}

func TestCheckRejectsUnrecognizedType(t *testing.T) {
	path := "spec/requirements/REQ-TYPE-00001.yaml"
	text := "The system shall provide authentication."

	undeclared := Check(path, "", text)
	if lookup(undeclared, CodeInvalidType) == nil {
		t.Fatalf("empty type produced no invalid-type diagnostic: %+v", undeclared)
	}
	if lookup(undeclared, CodePatternMismatch) != nil {
		t.Fatalf("undeclared type also reported a pattern mismatch: %+v", undeclared)
	}
	if got := lookup(undeclared, CodeInvalidType); got != nil && !strings.Contains(got.Message, "undeclared") {
		t.Fatalf("empty type message %q omitted undeclared", got.Message)
	}

	unknown := Check(path, "feature", text)
	finding := lookup(unknown, CodeInvalidType)
	if finding == nil {
		t.Fatalf("unknown type produced no invalid-type diagnostic: %+v", unknown)
	}
	if !strings.Contains(finding.Message, "feature") {
		t.Fatalf("message %q omitted the type value", finding.Message)
	}
	if lookup(unknown, CodePatternMismatch) != nil {
		t.Fatalf("unknown type also reported a pattern mismatch: %+v", unknown)
	}
}

func TestCheckSimplePatternsAreCaseSensitive(t *testing.T) {
	cases := []struct {
		name string
		kind string
		text string
	}{
		{name: "lowercase the", kind: records.TypeUbiquitous, text: "the system shall provide authentication."},
		{name: "lowercase when", kind: records.TypeEventDriven, text: "when a user logs in, the system shall issue a token."},
		{name: "lowercase if", kind: records.TypeUnwantedBehavior, text: "if a token expires, then the system shall reject the request."},
		{name: "lowercase where", kind: records.TypeOptionalFeature, text: "where caching is enabled, the system shall use a cache."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := Check("spec/requirements/REQ-CASE-00001.yaml", tc.kind, tc.text)
			if lookup(diagnostics, CodePatternMismatch) == nil {
				t.Fatalf("lowercase keyword was accepted: %+v", diagnostics)
			}
		})
	}
}

func TestCheckDoesNotUseBannedWordHeuristics(t *testing.T) {
	text := "The service shall return maybe approximately 200 when ready."
	diagnostics := Check("spec/requirements/REQ-API-00001.yaml", records.TypeUbiquitous, text)
	if len(diagnostics) != 0 {
		t.Fatalf("banned-word heuristic rejected a single-shall ubiquitous statement: %+v", diagnostics)
	}
}

func TestCheckReportsShallCountAndPatternIndependently(t *testing.T) {
	path := "spec/requirements/REQ-BOTH-00001.yaml"
	text := "The system shall start and shall stop."
	diagnostics := Check(path, records.TypeEventDriven, text)
	if lookup(diagnostics, CodeShallCount) == nil {
		t.Fatalf("missing shall-count diagnostic: %+v", diagnostics)
	}
	if lookup(diagnostics, CodePatternMismatch) == nil {
		t.Fatalf("missing pattern-mismatch diagnostic: %+v", diagnostics)
	}
}

func TestCheckAllSortsDeterministically(t *testing.T) {
	statements := []Statement{
		{Path: "spec/requirements/REQ-Z-00001.yaml", Type: records.TypeUbiquitous, Text: "When a user acts, the system shall respond."},
		{Path: "spec/requirements/REQ-A-00001.yaml", Type: "unknown", Text: "no obligation"},
		{Path: "spec/requirements/REQ-A-00001.yaml", Type: "unknown", Text: "no obligation"},
	}
	first := CheckAll(statements)
	second := CheckAll(statements)
	if len(first) == 0 {
		t.Fatal("expected diagnostics")
	}
	if len(first) != len(second) {
		t.Fatalf("repeated CheckAll changed length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("repeated CheckAll changed order at %d: %+v vs %+v", i, first[i], second[i])
		}
		if i == 0 {
			continue
		}
		if first[i-1].Path > first[i].Path {
			t.Fatalf("diagnostics are not sorted by path: %+v", first)
		}
	}
	if first[0].Path != "spec/requirements/REQ-A-00001.yaml" {
		t.Fatalf("first path=%q, want REQ-A", first[0].Path)
	}
}

func TestCheckAllEmpty(t *testing.T) {
	if diagnostics := CheckAll(nil); diagnostics != nil {
		t.Fatalf("empty input produced diagnostics: %+v", diagnostics)
	}
}

func lookup(diagnostics []Diagnostic, code string) *Diagnostic {
	for i := range diagnostics {
		if diagnostics[i].Code == code {
			return &diagnostics[i]
		}
	}
	return nil
}
