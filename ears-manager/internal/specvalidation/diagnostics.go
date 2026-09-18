package specvalidation

import (
	"fmt"
	"sort"
)

// Diagnostic is the stable machine-readable validation finding shared with
// the ears-manager CLI contract.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	RecordID string `json:"record_id,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// Result is the complete result of validating one specification snapshot.
type Result struct {
	Valid       bool         `json:"valid"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ValidationError adapts an invalid Result to Go's error contract without
// discarding the structured diagnostics.
type ValidationError struct {
	Diagnostics []Diagnostic
}

func (e *ValidationError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "specification validation failed"
	}
	return fmt.Sprintf("specification validation failed: %s", e.Diagnostics[0].Message)
}

func (r Result) Err() error {
	if r.Valid {
		return nil
	}
	return &ValidationError{Diagnostics: append([]Diagnostic(nil), r.Diagnostics...)}
}

func (r *Result) add(diagnostic Diagnostic) {
	r.Diagnostics = append(r.Diagnostics, diagnostic)
}

func (r *Result) finish() {
	sort.SliceStable(r.Diagnostics, func(i, j int) bool {
		left, right := r.Diagnostics[i], r.Diagnostics[j]
		return compareDiagnostics(left, right) < 0
	})

	deduplicated := r.Diagnostics[:0]
	for _, diagnostic := range r.Diagnostics {
		if len(deduplicated) > 0 && equalDiagnostics(deduplicated[len(deduplicated)-1], diagnostic) {
			continue
		}
		deduplicated = append(deduplicated, diagnostic)
	}
	r.Diagnostics = deduplicated
	r.Valid = len(r.Diagnostics) == 0
}

func compareDiagnostics(left, right Diagnostic) int {
	for _, pair := range [][2]string{
		{left.Path, right.Path},
		{left.RecordID, right.RecordID},
		{left.Field, right.Field},
		{left.Code, right.Code},
		{left.Severity, right.Severity},
		{left.Message, right.Message},
		{left.Hint, right.Hint},
	} {
		if pair[0] == pair[1] {
			continue
		}
		if pair[0] < pair[1] {
			return -1
		}
		return 1
	}
	return 0
}

func equalDiagnostics(left, right Diagnostic) bool {
	return compareDiagnostics(left, right) == 0
}

func diagnostic(code, path, recordID, field, message, hint string) Diagnostic {
	return Diagnostic{
		Code:     code,
		Severity: "error",
		Path:     path,
		RecordID: recordID,
		Field:    field,
		Message:  message,
		Hint:     hint,
	}
}

type recordKind string

const (
	requirementKind recordKind = "requirement"
	interfaceKind   recordKind = "interface"
	changeSetKind   recordKind = "change_set"
)

func missingFieldCode(kind recordKind) string {
	switch kind {
	case requirementKind:
		return "requirement.missing_field"
	case changeSetKind:
		return "change_set.missing_field"
	case interfaceKind:
		return "interface.missing_field"
	}
	return "project.missing_field"
}
