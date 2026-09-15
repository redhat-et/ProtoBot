package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
)

func TestStoreSaveLoadAndList(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, "records", func(id string) (string, error) {
		return records.FilenameFor(records.RequirementStore, id)
	}, recordsRequirementCodec())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer func() { _ = store.Close() }()
	first := records.Requirement{ID: "REQ-Z-00001", Type: records.EARSUbiquitous, Text: "The system shall work.", Provenance: records.ProvenanceUserAuthored, Created: "2026-09-15T10:00:00Z"}
	second := records.Requirement{ID: "REQ-A-00001", Type: records.EARSUbiquitous, Text: "The system shall work.", Provenance: records.ProvenanceUserAuthored, Created: "2026-09-15T10:00:00Z"}
	if err := store.Save(first); err != nil {
		t.Fatalf("Save(first) returned error: %v", err)
	}
	if err := store.Save(second); err != nil {
		t.Fatalf("Save(second) returned error: %v", err)
	}
	path, err := store.PathForID(first.ID)
	if err != nil {
		t.Fatalf("PathForID returned error: %v", err)
	}
	if filepath.Base(path) != "REQ-Z-00001.yaml" {
		t.Fatalf("PathForID = %q, want stable filename", path)
	}
	loaded, err := store.Load(first.ID)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded.ID != first.ID || loaded.Text != first.Text {
		t.Fatalf("Load = %#v, want %#v", loaded, first)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("List IDs = %#v, want [%q %q]", []string{list[0].ID, list[1].ID}, second.ID, first.ID)
	}
}

func TestValidatePathWithinRejectsEscapesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if _, err := ValidatePathWithin(root, "../outside"); err == nil {
		t.Fatal("ValidatePathWithin accepted a lexical escape")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink returned error: %v", err)
	}
	if _, err := ValidatePathWithin(root, "link/record.yaml"); err == nil {
		t.Fatal("ValidatePathWithin accepted a symlink escape")
	}
}

func TestStoreListRejectsRecordSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, "records", func(id string) (string, error) {
		return records.FilenameFor(records.RequirementStore, id)
	}, recordsRequirementCodec())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer func() { _ = store.Close() }()
	value := records.Requirement{ID: "REQ-A-00001", Type: records.EARSUbiquitous, Text: "The system shall work.", Provenance: records.ProvenanceUserAuthored, Created: "2026-09-15T10:00:00Z"}
	if err := store.Save(value); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	recordPath, err := store.PathForID(value.ID)
	if err != nil {
		t.Fatalf("PathForID returned error: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "record.yaml")
	data, err := Encode(value)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if err := os.WriteFile(outside, data, 0o644); err != nil {
		t.Fatalf("WriteFile(outside) returned error: %v", err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("Remove(recordPath) returned error: %v", err)
	}
	if err := os.Symlink(outside, recordPath); err != nil {
		t.Fatalf("Symlink(recordPath) returned error: %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List accepted a record symlink escape")
	}
}

func recordsRequirementCodec() Codec[records.Requirement] {
	return Codec[records.Requirement]{
		ID: func(value records.Requirement) string { return value.ID },
		Decode: func(data []byte) (records.Requirement, error) {
			var value records.Requirement
			if err := Decode(data, &value); err != nil {
				return records.Requirement{}, err
			}
			return value, nil
		},
		Encode: func(value records.Requirement) ([]byte, error) { return Encode(value) },
	}
}
