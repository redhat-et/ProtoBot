// Package ears checks the grammatical shape of EARS requirement statements.
//
// Every statement must contain exactly one "shall" and match the syntax
// pattern of its declared type. The check does not judge vagueness,
// banned words, semantic quality, or test adequacy.
package ears

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/redhat-et/protobot/validator/internal/records"
)

// Diagnostic codes for EARS shape failures.
const (
	CodeShallCount      = "ears.shall_count"
	CodePatternMismatch = "ears.pattern_mismatch"
	CodeInvalidType     = "ears.invalid_type"
)

// Diagnostic is a deterministic finding about one requirement statement.
type Diagnostic struct {
	Code    string
	Path    string
	Message string
}

// Statement is the EARS fields used for shape checking.
type Statement struct {
	Path string
	Type string
	Text string
}

var (
	shallPattern = regexp.MustCompile(`\bshall\b`)
	// Simple patterns follow the ADR-0002 templates with case-sensitive
	// keywords so state-driven statements require "While" rather than
	// "while" or undeclared aliases such as "During". (?s) lets a
	// folded or literal YAML block that still contains newlines match.
	earsPatterns = map[string]*regexp.Regexp{
		records.TypeUbiquitous:       regexp.MustCompile(`(?s)^The .+ shall .+$`),
		records.TypeEventDriven:      regexp.MustCompile(`(?s)^When .+, the .+ shall .+$`),
		records.TypeStateDriven:      regexp.MustCompile(`(?s)^While .+, the .+ shall .+$`),
		records.TypeUnwantedBehavior: regexp.MustCompile(`(?s)^If .+, then the .+ shall .+$`),
		records.TypeOptionalFeature:  regexp.MustCompile(`(?s)^Where .+, the .+ shall .+$`),
	}
	// Complex trigger forms are case-insensitive: the canonical complex
	// template places "when" mid-sentence. Each form counts once.
	complexTriggers = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bwhen\b`),
		regexp.MustCompile(`(?i)\bwhile\b`),
		regexp.MustCompile(`(?i)\bwhere\b`),
		regexp.MustCompile(`(?is)\bif\b.*\bthen\b`),
	}
)

// Check reports shape diagnostics for one requirement statement.
func Check(path, declaredType, text string) []Diagnostic {
	var diagnostics []Diagnostic
	if count := shallCount(text); count != 1 {
		diagnostics = append(diagnostics, Diagnostic{
			Code:    CodeShallCount,
			Path:    path,
			Message: fmt.Sprintf("requirement text contains %d occurrences of \"shall\", want exactly 1", count),
		})
	}
	if !knownType(declaredType) {
		message := fmt.Sprintf("unrecognized EARS type %q", declaredType)
		if declaredType == "" {
			message = "undeclared EARS type"
		}
		diagnostics = append(diagnostics, Diagnostic{
			Code:    CodeInvalidType,
			Path:    path,
			Message: message,
		})
		return diagnostics
	}
	if !matchesPattern(declaredType, text) {
		diagnostics = append(diagnostics, Diagnostic{
			Code:    CodePatternMismatch,
			Path:    path,
			Message: fmt.Sprintf("requirement text does not match the declared %s pattern", declaredType),
		})
	}
	return diagnostics
}

// CheckAll reports shape diagnostics for every statement, sorted by
// path, code, and message.
func CheckAll(statements []Statement) []Diagnostic {
	var diagnostics []Diagnostic
	for _, statement := range statements {
		diagnostics = append(diagnostics, Check(statement.Path, statement.Type, statement.Text)...)
	}
	sortDiagnostics(diagnostics)
	return diagnostics
}

func shallCount(text string) int {
	return len(shallPattern.FindAllString(text, -1))
}

func knownType(declaredType string) bool {
	switch declaredType {
	case records.TypeUbiquitous, records.TypeEventDriven, records.TypeStateDriven,
		records.TypeUnwantedBehavior, records.TypeOptionalFeature, records.TypeComplex:
		return true
	default:
		return false
	}
}

func matchesPattern(declaredType, text string) bool {
	if declaredType == records.TypeComplex {
		count := 0
		for _, pattern := range complexTriggers {
			if pattern.MatchString(text) {
				count++
			}
		}
		return count >= 2
	}
	pattern, exists := earsPatterns[declaredType]
	return exists && pattern.MatchString(text)
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}
