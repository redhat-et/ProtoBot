// Package testtags checks that @req:<ID> association tags in Gherkin
// test files resolve to known requirement records.
//
// Extraction is structural: it reads Gherkin tag lines in .feature files
// under tests/ and does not judge coverage adequacy. Active and retired
// requirement IDs both count as known. Unknown or misspelled IDs fail.
package testtags

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// TestsDir is the checkout-relative directory that holds derived tests.
const TestsDir = "tests"

// Diagnostic codes for test-tag reference checks.
const (
	CodeReadFailed     = "testtags.read_failed"
	CodeUnexpectedFile = "testtags.unexpected_file"
	CodeUnresolved     = "testtags.unresolved"
)

// Diagnostic is a deterministic finding about one path in the checkout.
type Diagnostic struct {
	Code    string
	Path    string
	Line    int
	Message string
}

// Check scans Gherkin .feature files under tests/ in target for @req:<ID>
// association tags and reports tags that do not resolve to a known
// requirement ID. Retired and active IDs both count as known. Missing
// tests/ is treated as empty. Traversal stays inside target and does
// not follow symlinks.
func Check(target string, knownIDs map[string]struct{}) []Diagnostic {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return []Diagnostic{diagnostic(CodeReadFailed, target, 0, fmt.Sprintf("cannot resolve checkout path: %s", err))}
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return []Diagnostic{diagnostic(CodeReadFailed, ".", 0, fmt.Sprintf("cannot open checkout: %s", err))}
	}
	defer func() { _ = root.Close() }()

	diagnostics := walk(root, TestsDir, knownIDs, true)
	sortDiagnostics(diagnostics)
	return diagnostics
}

func walk(root *os.Root, relative string, knownIDs map[string]struct{}, top bool) []Diagnostic {
	osPath := filepath.FromSlash(relative)
	info, err := root.Lstat(osPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && top {
			return nil
		}
		return []Diagnostic{diagnostic(CodeReadFailed, relative, 0, fmt.Sprintf("cannot stat path: %s", err))}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if top {
			return []Diagnostic{diagnostic(CodeUnexpectedFile, relative, 0, "must be a directory")}
		}
		if filepath.Ext(info.Name()) == ".feature" {
			return []Diagnostic{diagnostic(CodeUnexpectedFile, relative, 0, "is not a regular file")}
		}
		return nil
	}
	if info.IsDir() {
		return walkDir(root, relative, knownIDs)
	}
	if top {
		return []Diagnostic{diagnostic(CodeUnexpectedFile, relative, 0, "must be a directory")}
	}
	if filepath.Ext(info.Name()) != ".feature" {
		return nil
	}
	if !info.Mode().IsRegular() {
		return []Diagnostic{diagnostic(CodeUnexpectedFile, relative, 0, "is not a regular file")}
	}
	return scanFeature(root, relative, knownIDs)
}

func walkDir(root *os.Root, relative string, knownIDs map[string]struct{}) []Diagnostic {
	osPath := filepath.FromSlash(relative)
	directory, err := root.Open(osPath)
	if err != nil {
		return []Diagnostic{diagnostic(CodeReadFailed, relative, 0, fmt.Sprintf("cannot open directory: %s", err))}
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return []Diagnostic{diagnostic(CodeReadFailed, relative, 0, fmt.Sprintf("cannot read directory: %s", err))}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var diagnostics []Diagnostic
	for _, entry := range entries {
		name := entry.Name()
		if !safeEntryName(name) {
			continue
		}
		child := path.Join(relative, name)
		diagnostics = append(diagnostics, walk(root, child, knownIDs, false)...)
	}
	return diagnostics
}

func scanFeature(root *os.Root, relative string, knownIDs map[string]struct{}) []Diagnostic {
	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return []Diagnostic{diagnostic(CodeReadFailed, relative, 0, fmt.Sprintf("cannot read file: %s", err))}
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	var diagnostics []Diagnostic
	line := 0
	for scanner.Scan() {
		line++
		diagnostics = append(diagnostics, tagsOnLine(relative, line, scanner.Text(), knownIDs)...)
	}
	if err := scanner.Err(); err != nil {
		return []Diagnostic{diagnostic(CodeReadFailed, relative, 0, fmt.Sprintf("cannot scan file: %s", err))}
	}
	return diagnostics
}

func tagsOnLine(relative string, line int, text string, knownIDs map[string]struct{}) []Diagnostic {
	if line == 1 {
		text = strings.TrimPrefix(text, "\ufeff")
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, "@") {
		return nil
	}

	var diagnostics []Diagnostic
	for _, field := range strings.Fields(trimmed) {
		if strings.HasPrefix(field, "#") {
			break
		}
		id, ok := strings.CutPrefix(field, "@req:")
		if !ok {
			continue
		}
		_, known := knownIDs[id]
		if id != "" && known {
			continue
		}
		diagnostics = append(diagnostics, diagnostic(
			CodeUnresolved,
			relative,
			line,
			fmt.Sprintf("@req tag references unknown requirement ID %q", id),
		))
	}
	return diagnostics
}

func safeEntryName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, string(filepath.Separator)) {
		return false
	}
	return filepath.Base(name) == name
}

func diagnostic(code, path string, line int, message string) Diagnostic {
	return Diagnostic{Code: code, Path: path, Line: line, Message: message}
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}
