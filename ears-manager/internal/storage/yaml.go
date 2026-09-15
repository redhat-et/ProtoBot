package storage

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"gopkg.in/yaml.v3"
)

const managedHeader = "# managed by ears-manager; do not edit by hand\n"

func Decode(data []byte, target any) error {
	if target == nil {
		return fmt.Errorf("YAML decode target must not be nil")
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("YAML input is not valid UTF-8")
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return fmt.Errorf("YAML input must not contain a UTF-8 BOM")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return fmt.Errorf("YAML document is empty")
	}
	if err := inspectNode(document.Content[0], "$"); err != nil {
		return err
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("YAML input contains more than one document")
		}
		return fmt.Errorf("read YAML document boundary: %w", err)
	}

	strictDecoder := yaml.NewDecoder(bytes.NewReader(data))
	strictDecoder.KnownFields(true)
	if err := strictDecoder.Decode(target); err != nil {
		return fmt.Errorf("decode YAML record: %w", err)
	}
	return nil
}

func Encode(value any) ([]byte, error) {
	value = canonicalValue(value)
	raw, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode YAML record: %w", err)
	}
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	raw = bytes.TrimRight(raw, "\n")
	if len(raw) == 0 {
		return nil, fmt.Errorf("cannot encode an empty YAML record")
	}
	return append([]byte(managedHeader), append(raw, '\n')...), nil
}

func ReadFile(path string, target any) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(absolute)
	if err := ensureRecordPath(root, name); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := Decode(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func WriteFile(path string, value any) error {
	data, err := Encode(value)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = root.Close() }()
	if err := atomicWrite(root, ".", filepath.Base(absolute), data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func inspectNode(node *yaml.Node, location string) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("unsafe YAML alias at %s", location)
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

func canonicalValue(value any) any {
	switch typed := value.(type) {
	case records.Requirement:
		return records.CanonicalRequirement(typed)
	case *records.Requirement:
		copy := records.CanonicalRequirement(*typed)
		return &copy
	case records.InterfaceRecord:
		return records.CanonicalInterface(typed)
	case *records.InterfaceRecord:
		copy := records.CanonicalInterface(*typed)
		return &copy
	case records.ChangeSet:
		return records.CanonicalChangeSet(typed)
	case *records.ChangeSet:
		copy := records.CanonicalChangeSet(*typed)
		return &copy
	case records.ProjectConfig:
		return records.CanonicalProjectConfig(typed)
	case *records.ProjectConfig:
		copy := records.CanonicalProjectConfig(*typed)
		return &copy
	default:
		return value
	}
}

var temporarySequence uint64

func atomicWrite(root *os.Root, directory, filename string, data []byte) error {
	if err := ensureDirectoryPath(root, directory); err != nil {
		return err
	}
	if err := ensureRecordPath(root, filepath.Join(directory, filename)); err != nil {
		return err
	}

	var temporary *os.File
	var temporaryName string
	for attempt := 0; attempt < 10; attempt++ {
		temporaryName = filepath.Join(directory, fmt.Sprintf(".ears-manager-%d-%d", os.Getpid(), atomic.AddUint64(&temporarySequence, 1)))
		var err error
		temporary, err = root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return err
		}
	}
	if temporary == nil {
		return fmt.Errorf("could not create a unique temporary file")
	}
	defer func() { _ = root.Remove(temporaryName) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := root.Rename(temporaryName, filepath.Join(directory, filename)); err != nil {
		return err
	}
	return syncDirectory(root, directory)
}
