// Package layout reads requirement and interface YAML records from the
// spec/ directory of a checkout. It does not consult manifests, change
// sets, or paths outside the target directory.
package layout

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/redhat-et/protobot/validator/internal/records"
	"gopkg.in/yaml.v3"
)

// Relative store paths inside a checkout.
const (
	RequirementsDir = "spec/requirements"
	InterfacesDir   = "spec/interfaces"
)

// Diagnostic codes for layout read failures.
const (
	CodeReadFailed     = "layout.read_failed"
	CodeUnexpectedFile = "layout.unexpected_file"
	CodeParseFailed    = "layout.parse_failed"
)

// maxYAMLBytes is the maximum size of a requirement or interface YAML
// record. Larger files are rejected before the contents are parsed.
const maxYAMLBytes = 1 << 20

// Diagnostic is a deterministic finding about one path in the checkout.
type Diagnostic struct {
	Code    string
	Path    string
	Message string
}

// Document is one YAML record loaded from the spec directory layout.
type Document[T any] struct {
	Path   string
	Record T
	Fields map[string]bool
}

// Snapshot is the requirement and interface records discovered in a checkout.
type Snapshot struct {
	Root         string
	Requirements []Document[records.Requirement]
	Interfaces   []Document[records.InterfaceRecord]
}

// Read discovers and parses requirement and interface YAML records from
// spec/requirements and spec/interfaces under target. Missing directories
// are treated as empty. Traversal stays inside target; the function does
// not read manifests, change sets, or paths outside the checkout.
//
// Each diagnostic identifies the offending relative path. Successfully
// parsed records are still returned when other entries fail.
func Read(target string) (Snapshot, []Diagnostic) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return Snapshot{}, []Diagnostic{diagnostic(CodeReadFailed, target, fmt.Sprintf("cannot resolve checkout path: %s", err))}
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return Snapshot{Root: absolute}, []Diagnostic{diagnostic(CodeReadFailed, ".", fmt.Sprintf("cannot open checkout: %s", err))}
	}
	defer func() { _ = root.Close() }()

	snapshot := Snapshot{Root: absolute}
	var diagnostics []Diagnostic
	snapshot.Requirements, diagnostics = loadDir(root, RequirementsDir, decodeRecord[records.Requirement])
	interfaces, interfaceDiagnostics := loadDir(root, InterfacesDir, decodeRecord[records.InterfaceRecord])
	snapshot.Interfaces = interfaces
	diagnostics = append(diagnostics, interfaceDiagnostics...)
	sortDiagnostics(diagnostics)
	return snapshot, diagnostics
}

func loadDir[T any](root *os.Root, relative string, decode func([]byte) (T, map[string]bool, error)) ([]Document[T], []Diagnostic) {
	osPath := filepath.FromSlash(relative)
	info, err := root.Lstat(osPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []Diagnostic{diagnostic(CodeReadFailed, relative, fmt.Sprintf("cannot stat directory: %s", err))}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, []Diagnostic{diagnostic(CodeUnexpectedFile, relative, "must be a directory")}
	}

	directory, err := root.Open(osPath)
	if err != nil {
		return nil, []Diagnostic{diagnostic(CodeReadFailed, relative, fmt.Sprintf("cannot open directory: %s", err))}
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, []Diagnostic{diagnostic(CodeReadFailed, relative, fmt.Sprintf("cannot read directory: %s", err))}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var documents []Document[T]
	var diagnostics []Diagnostic
	for _, entry := range entries {
		name := entry.Name()
		entryName := name
		if hasControl(name) {
			entryName = strconv.Quote(name)
		}
		entryRel := path.Join(relative, entryName)
		if !safeEntryName(name) {
			diagnostics = append(diagnostics, diagnostic(CodeUnexpectedFile, entryRel, "has an unsafe name"))
			continue
		}
		document, failure := loadEntry(root, entryRel, decode)
		if failure != nil {
			diagnostics = append(diagnostics, *failure)
			continue
		}
		documents = append(documents, document)
	}
	return documents, diagnostics
}

func loadEntry[T any](root *os.Root, relative string, decode func([]byte) (T, map[string]bool, error)) (Document[T], *Diagnostic) {
	osPath := filepath.FromSlash(relative)
	info, err := root.Lstat(osPath)
	if err != nil {
		d := diagnostic(CodeReadFailed, relative, fmt.Sprintf("cannot stat file: %s", err))
		return Document[T]{}, &d
	}
	if info.IsDir() {
		d := diagnostic(CodeUnexpectedFile, relative, "is a directory")
		return Document[T]{}, &d
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		d := diagnostic(CodeUnexpectedFile, relative, "is not a regular file")
		return Document[T]{}, &d
	}
	if filepath.Ext(info.Name()) != ".yaml" {
		d := diagnostic(CodeUnexpectedFile, relative, "is not a YAML record")
		return Document[T]{}, &d
	}
	if info.Size() > maxYAMLBytes {
		d := diagnostic(CodeReadFailed, relative, "YAML file exceeds the 1 MiB limit")
		return Document[T]{}, &d
	}

	file, err := root.Open(osPath)
	if err != nil {
		d := diagnostic(CodeReadFailed, relative, fmt.Sprintf("cannot read file: %s", err))
		return Document[T]{}, &d
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxYAMLBytes+1))
	if err != nil {
		d := diagnostic(CodeReadFailed, relative, fmt.Sprintf("cannot read file: %s", err))
		return Document[T]{}, &d
	}
	// Guard against files that grew between stat and read, or where stat
	// reported an inaccurate size.
	if int64(len(data)) > maxYAMLBytes {
		d := diagnostic(CodeReadFailed, relative, "YAML file exceeds the 1 MiB limit")
		return Document[T]{}, &d
	}
	value, fields, err := decode(data)
	if err != nil {
		d := diagnostic(CodeParseFailed, relative, err.Error())
		return Document[T]{}, &d
	}
	if fields == nil {
		fields = map[string]bool{}
	}
	return Document[T]{Path: relative, Record: value, Fields: fields}, nil
}

func safeEntryName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, string(filepath.Separator)) {
		return false
	}
	if hasControl(name) {
		return false
	}
	return filepath.Base(name) == name
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func decodeRecord[T any](data []byte) (T, map[string]bool, error) {
	var zero T
	if !utf8.Valid(data) {
		return zero, nil, fmt.Errorf("YAML input is not valid UTF-8")
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return zero, nil, fmt.Errorf("YAML input must not contain a UTF-8 BOM")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return zero, nil, fmt.Errorf("YAML document is empty")
		}
		return zero, nil, fmt.Errorf("parse YAML: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return zero, nil, fmt.Errorf("YAML document is empty")
	}

	root := document.Content[0]
	if err := inspectNode(root, "$"); err != nil {
		return zero, nil, err
	}
	if root.Kind != yaml.MappingNode {
		return zero, nil, fmt.Errorf("YAML record root must be a mapping")
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return zero, nil, fmt.Errorf("YAML input contains more than one document")
		}
		return zero, nil, fmt.Errorf("parse YAML: %w", err)
	}

	var value T
	if err := root.Decode(&value); err != nil {
		return zero, nil, fmt.Errorf("decode YAML record: %w", err)
	}
	fields := make(map[string]bool)
	collectFields(root, "", fields)
	return value, fields, nil
}

func inspectNode(node *yaml.Node, location string) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("unsafe YAML alias at %s", location)
	}
	if node.Tag == "!!null" {
		return fmt.Errorf("YAML null values are not allowed at %s", location)
	}
	if node.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("unsafe YAML tag at %s", location)
	}
	if strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!") {
		return fmt.Errorf("unsafe YAML tag %q at %s", node.Tag, location)
	}

	switch node.Kind {
	case yaml.MappingNode:
		keys := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("YAML mapping key at %s must be a string", location)
			}
			if key.Value == "<<" {
				return fmt.Errorf("YAML merge key is not allowed at %s", location)
			}
			if _, exists := keys[key.Value]; exists {
				return fmt.Errorf("duplicate YAML key %q at %s", key.Value, location)
			}
			keys[key.Value] = struct{}{}
			if err := inspectNode(node.Content[i+1], location+"."+key.Value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			if err := inspectNode(child, fmt.Sprintf("%s[%d]", location, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectFields(node *yaml.Node, location string, fields map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			field := key
			if location != "" {
				field = location + "." + key
			}
			fields[field] = true
			collectFields(node.Content[i+1], field, fields)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			field := fmt.Sprintf("%s[%d]", location, i)
			fields[field] = true
			collectFields(child, field, fields)
		}
	}
}

var lineBreakReplacer = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

func diagnostic(code, path, message string) Diagnostic {
	if hasControl(path) {
		path = strconv.Quote(path)
	}
	return Diagnostic{
		Code:    code,
		Path:    path,
		Message: lineBreakReplacer.Replace(message),
	}
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
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
