package specvalidation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

type loadError struct {
	Path string
	Err  error
}

func (e loadError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("unable to load %s", displayLoadPath(e.Path))
	}
	return fmt.Sprintf("unable to load %s: %s", displayLoadPath(e.Path), stableLoadCause(e.Err))
}

func (e loadError) Unwrap() error {
	return e.Err
}

func stableLoadCause(err error) string {
	if nested, ok := err.(loadError); ok && nested.Err != nil {
		return stableLoadCause(nested.Err)
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "yaml"), strings.Contains(lower, "decode"), strings.Contains(lower, "parse"):
		return "YAML decoding failed"
	case strings.Contains(lower, "unexpected file"):
		return "unexpected file in record store"
	case strings.Contains(lower, "does not match filename"):
		return "record filename does not match its ID"
	case strings.Contains(lower, "store path"), strings.Contains(lower, "record store"):
		return "record store path is invalid"
	default:
		return "filesystem or project data access failed"
	}
}

func displayLoadPath(path string) string {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "..") || strings.ContainsAny(path, "\\\x00") {
		return "configured project path"
	}
	return filepath.ToSlash(path)
}

// Load reads a complete project snapshot using the storage layer's safe YAML
// decoder. It performs no semantic validation and never writes files.
func Load(root string) (Snapshot, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Snapshot{}, loadError{Path: ".protobot/project.yaml", Err: err}
	}
	rootHandle, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return Snapshot{}, loadError{Path: ".protobot/project.yaml", Err: err}
	}
	defer func() { _ = rootHandle.Close() }()
	controlInfo, err := rootHandle.Lstat(".protobot")
	if err != nil {
		return Snapshot{}, loadError{Path: ".protobot", Err: err}
	}
	if controlInfo.Mode()&os.ModeSymlink != 0 || !controlInfo.IsDir() {
		return Snapshot{}, loadError{Path: ".protobot", Err: fmt.Errorf("control namespace must be a directory")}
	}
	configRelative := filepath.FromSlash(".protobot/project.yaml")
	configInfo, err := rootHandle.Lstat(configRelative)
	if err != nil {
		return Snapshot{}, loadError{Path: ".protobot/project.yaml", Err: err}
	}
	if configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.Mode().IsRegular() {
		return Snapshot{}, loadError{Path: ".protobot/project.yaml", Err: fmt.Errorf("project configuration must be a regular file")}
	}
	data, err := rootHandle.ReadFile(configRelative)
	if err != nil {
		return Snapshot{}, loadError{Path: ".protobot/project.yaml", Err: err}
	}
	var config records.ProjectConfig
	fields, err := storage.DecodeFields(data, &config)
	if err != nil {
		return Snapshot{}, loadError{Path: ".protobot/project.yaml", Err: err}
	}
	snapshot := Snapshot{
		Root:         absoluteRoot,
		Config:       config,
		ConfigPath:   ".protobot/project.yaml",
		ConfigFields: fields,
	}
	paths := config.Stores.WithDefaults()
	if pathErr := validateLoadStorePaths(paths); pathErr != nil {
		return Snapshot{}, loadError{Path: pathErr.Path, Err: pathErr.Err}
	}
	snapshot.Requirements, err = loadDocuments(absoluteRoot, rootHandle, paths.Requirements, records.RequirementStore, func(data []byte) (records.Requirement, map[string]bool, error) {
		var value records.Requirement
		fields, err := storage.DecodeFields(data, &value)
		return value, fields, err
	})
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Interfaces, err = loadDocuments(absoluteRoot, rootHandle, paths.Interfaces, records.InterfaceStore, func(data []byte) (records.InterfaceRecord, map[string]bool, error) {
		var value records.InterfaceRecord
		fields, err := storage.DecodeFields(data, &value)
		return value, fields, err
	})
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.ChangeSets, err = loadDocuments(absoluteRoot, rootHandle, paths.ChangeSets, records.ChangeSetStore, func(data []byte) (records.ChangeSet, map[string]bool, error) {
		var value records.ChangeSet
		fields, err := storage.DecodeFields(data, &value)
		return value, fields, err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func validateLoadStorePaths(paths records.StorePaths) *loadError {
	for _, path := range []string{paths.Requirements, paths.Interfaces, paths.ChangeSets} {
		if _, pathErr := canonicalStorePath(path); pathErr != nil {
			return &loadError{Path: path, Err: pathErr}
		}
	}
	return nil
}

// ValidateProject loads and validates a project in read-only mode.
func ValidateProject(root string) Result {
	snapshot, err := Load(root)
	if err != nil {
		var loadErr loadError
		path := ""
		if errors.As(err, &loadErr) {
			path = displayLoadPath(loadErr.Path)
		}
		result := Result{}
		result.add(diagnostic("storage.decode_failed", path, "", "", err.Error(), "Fix the reported file without modifying it through another route."))
		result.finish()
		return result
	}
	return Validate(snapshot)
}

func loadDocuments[T any](root string, rootHandle *os.Root, relativeDirectory string, kind records.StoreKind, decode func([]byte) (T, map[string]bool, error)) ([]Document[T], error) {
	canonical, err := canonicalProjectPath(relativeDirectory)
	if err != nil {
		return nil, loadError{Path: relativeDirectory, Err: err}
	}
	if _, err := storage.ValidatePathWithin(root, filepath.FromSlash(canonical)); err != nil {
		return nil, loadError{Path: canonical, Err: err}
	}
	storeInfo, err := rootHandle.Lstat(filepath.FromSlash(canonical))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Document[T]{}, nil
		}
		return nil, loadError{Path: canonical, Err: err}
	}
	if storeInfo.Mode()&os.ModeSymlink != 0 || !storeInfo.IsDir() {
		return nil, loadError{Path: canonical, Err: fmt.Errorf("record store path must be a directory")}
	}
	directory, err := rootHandle.Open(filepath.FromSlash(canonical))
	if err != nil {
		return nil, loadError{Path: canonical, Err: err}
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, loadError{Path: canonical, Err: err}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	result := make([]Document[T], 0, len(entries))
	for _, entry := range entries {
		document, include, err := loadDocumentEntry(rootHandle, canonical, entry.Name(), kind, decode)
		if err != nil {
			return nil, err
		}
		if include {
			result = append(result, document)
		}
	}
	return result, nil
}

func loadDocumentEntry[T any](rootHandle *os.Root, relativeDirectory, name string, kind records.StoreKind, decode func([]byte) (T, map[string]bool, error)) (Document[T], bool, error) {
	if strings.HasPrefix(name, ".") {
		return Document[T]{}, false, nil
	}
	relativePath := filepath.ToSlash(filepath.Join(relativeDirectory, name))
	entryPath := filepath.FromSlash(relativePath)
	info, err := rootHandle.Lstat(entryPath)
	if err != nil {
		return Document[T]{}, false, loadError{Path: relativePath, Err: err}
	}
	if info.IsDir() {
		return Document[T]{}, false, loadError{Path: relativePath, Err: fmt.Errorf("unexpected file in record store")}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Document[T]{}, false, loadError{Path: relativePath, Err: fmt.Errorf("record path must be a regular file")}
	}
	if filepath.Ext(name) != ".yaml" {
		return Document[T]{}, false, loadError{Path: relativePath, Err: fmt.Errorf("unexpected file in record store")}
	}
	data, err := rootHandle.ReadFile(entryPath)
	if err != nil {
		return Document[T]{}, false, loadError{Path: relativePath, Err: err}
	}
	value, fields, err := decode(data)
	if err != nil {
		return Document[T]{}, false, loadError{Path: relativePath, Err: err}
	}
	id := documentID(value)
	if expected, mapErr := records.FilenameFor(kind, id); mapErr == nil && expected != name {
		return Document[T]{}, false, loadError{Path: relativePath, Err: fmt.Errorf("record ID %q does not match filename %q", id, name)}
	}
	return Document[T]{Path: relativePath, Value: value, Fields: fields}, true, nil
}

func documentID[T any](value T) string {
	switch typed := any(value).(type) {
	case records.Requirement:
		return typed.ID
	case records.InterfaceRecord:
		return typed.ID
	case records.ChangeSet:
		return typed.ID
	default:
		return ""
	}
}
