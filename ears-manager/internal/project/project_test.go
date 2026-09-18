package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/schema"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

func TestDiscoverFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".protobot")
	nested := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll(configDirectory) returned error: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(nested) returned error: %v", err)
	}
	config := records.ProjectConfig{
		Project: records.ProjectIdentity{ID: "protobot", Name: "ProtoBot"},
		SchemaVersions: records.SchemaVersions{
			Project:       records.CurrentProjectSchemaVersion,
			Specification: records.CurrentSpecificationSchemaVersion,
		},
	}
	data, err := storage.Encode(config)
	if err != nil {
		t.Fatalf("Encode(config) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "project.yaml"), data, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	project, err := DiscoverWithGitRoot(nested, func(string) (string, error) { return root, nil })
	if err != nil {
		t.Fatalf("DiscoverWithGitRoot returned error: %v", err)
	}
	if project.Root != root {
		t.Fatalf("Project.Root = %q, want %q", project.Root, root)
	}
	if project.Config.Stores.Requirements != ".protobot/requirements" {
		t.Fatalf("default requirements store = %q", project.Config.Stores.Requirements)
	}
}

func TestDiscoverRejectsMissingAndMisplacedConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		configRoot string
		start      string
		wantText   string
	}{
		{name: "missing", configRoot: "", start: "", wantText: "no .protobot/project.yaml"},
		{name: "misplaced", configRoot: "nested", start: "nested", wantText: "Git working-tree root"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			start := root
			if test.start != "" {
				start = filepath.Join(root, test.start)
				if err := os.MkdirAll(start, 0o755); err != nil {
					t.Fatalf("MkdirAll(start) returned error: %v", err)
				}
			}
			if test.configRoot != "" {
				configDirectory := filepath.Join(root, test.configRoot, ".protobot")
				if err := os.MkdirAll(configDirectory, 0o755); err != nil {
					t.Fatalf("MkdirAll(configDirectory) returned error: %v", err)
				}
				data, err := storage.Encode(validConfig())
				if err != nil {
					t.Fatalf("Encode(config) returned error: %v", err)
				}
				if err := os.WriteFile(filepath.Join(configDirectory, "project.yaml"), data, 0o644); err != nil {
					t.Fatalf("WriteFile returned error: %v", err)
				}
			}
			_, err := DiscoverWithGitRoot(start, func(string) (string, error) { return root, nil })
			if err == nil || !contains(err.Error(), test.wantText) {
				t.Fatalf("DiscoverWithGitRoot error = %v, want text %q", err, test.wantText)
			}
		})
	}
}

func TestDiscoverRejectsOutOfTreeStorePath(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".protobot")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	config := validConfig()
	config.Stores.Requirements = "../outside"
	data, err := storage.Encode(config)
	if err != nil {
		t.Fatalf("Encode(config) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "project.yaml"), data, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	_, err = DiscoverWithGitRoot(root, func(string) (string, error) { return root, nil })
	if err == nil {
		t.Fatal("DiscoverWithGitRoot accepted an out-of-tree store path")
	}
}

func TestDiscoverRejectsNewerSchema(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".protobot")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	config := validConfig()
	config.SchemaVersions.Specification++
	data, err := storage.Encode(config)
	if err != nil {
		t.Fatalf("Encode(config) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "project.yaml"), data, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	_, err = DiscoverWithGitRoot(root, func(string) (string, error) { return root, nil })
	var versionError *schema.VersionError
	if !errors.As(err, &versionError) {
		t.Fatalf("DiscoverWithGitRoot error = %v, want VersionError", err)
	}
	if versionError.Store != "specification" {
		t.Fatalf("VersionError.Store = %q, want specification", versionError.Store)
	}
}

func TestProjectArtifactRegistryIsTypedAndCanonical(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".protobot")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	config := validConfig()
	data, err := storage.Encode(config)
	if err != nil {
		t.Fatalf("Encode(config) returned error: %v", err)
	}
	configPath := filepath.Join(configDirectory, "project.yaml")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	project, err := DiscoverWithGitRoot(root, func(string) (string, error) { return root, nil })
	if err != nil {
		t.Fatalf("DiscoverWithGitRoot returned error: %v", err)
	}
	artifact := records.ArtifactEntry{
		ID:     "vision",
		Kind:   records.ArtifactVision,
		Path:   "docs/vision.md",
		Digest: "sha256:e06dbbb451a2eeaa837b763f4f15e991a056fdef2c4f3aae9ee65de002c2a39f",
		Owner:  "user",
	}
	if err := project.SaveArtifact(artifact); err != nil {
		t.Fatalf("SaveArtifact returned error: %v", err)
	}
	loaded, err := project.Artifact("vision")
	if err != nil {
		t.Fatalf("Artifact returned error: %v", err)
	}
	if loaded != artifact {
		t.Fatalf("Artifact = %#v, want %#v", loaded, artifact)
	}
	var configOnDisk records.ProjectConfig
	if err := storage.ReadFile(configPath, &configOnDisk); err != nil {
		t.Fatalf("ReadFile(project.yaml) returned error: %v", err)
	}
	if len(configOnDisk.Artifacts) != 1 || configOnDisk.Artifacts[0].ID != "vision" {
		t.Fatalf("project artifacts = %#v, want vision entry", configOnDisk.Artifacts)
	}
}

func TestProjectProvidesTypedInterfaceAndChangeSetStores(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".protobot")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	configPath := filepath.Join(configDirectory, "project.yaml")
	data, err := storage.Encode(validConfig())
	if err != nil {
		t.Fatalf("Encode(config) returned error: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	project, err := DiscoverWithGitRoot(root, func(string) (string, error) { return root, nil })
	if err != nil {
		t.Fatalf("DiscoverWithGitRoot returned error: %v", err)
	}
	interfaces, err := project.InterfaceStore()
	if err != nil {
		t.Fatalf("InterfaceStore returned error: %v", err)
	}
	defer func() { _ = interfaces.Close() }()
	interfaceRecord := records.InterfaceRecord{ID: "api-gateway", Name: "API Gateway", Type: records.InterfaceNetworkService, Created: "2026-09-15T10:00:00Z"}
	if err := interfaces.Save(interfaceRecord); err != nil {
		t.Fatalf("InterfaceStore.Save returned error: %v", err)
	}
	interfacePath, err := interfaces.PathForID(interfaceRecord.ID)
	if err != nil {
		t.Fatalf("InterfaceStore.PathForID returned error: %v", err)
	}
	if filepath.Base(interfacePath) != "api-gateway.yaml" {
		t.Fatalf("interface filename = %q, want api-gateway.yaml", filepath.Base(interfacePath))
	}
	changeSets, err := project.ChangeSetStore()
	if err != nil {
		t.Fatalf("ChangeSetStore returned error: %v", err)
	}
	defer func() { _ = changeSets.Close() }()
	changeSet := records.ChangeSet{
		ID:                     "CS-00001",
		BaseCommit:             "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Intent:                 "Initial change set",
		Operations:             []records.RequirementOperation{},
		AffectedInterfaces:     []string{"api-gateway"},
		ImplementationRequired: true,
		Created:                "2026-09-15T10:00:00Z",
	}
	if err := changeSets.Save(changeSet); err != nil {
		t.Fatalf("ChangeSetStore.Save returned error: %v", err)
	}
	changeSetPath, err := changeSets.PathForID(changeSet.ID)
	if err != nil {
		t.Fatalf("ChangeSetStore.PathForID returned error: %v", err)
	}
	if filepath.Base(changeSetPath) != "cs-00001.yaml" {
		t.Fatalf("change-set filename = %q, want cs-00001.yaml", filepath.Base(changeSetPath))
	}
}

func validConfig() records.ProjectConfig {
	return records.ProjectConfig{
		Project: records.ProjectIdentity{ID: "protobot", Name: "ProtoBot"},
		SchemaVersions: records.SchemaVersions{
			Project:       records.CurrentProjectSchemaVersion,
			Specification: records.CurrentSpecificationSchemaVersion,
		},
	}
}

func contains(value, want string) bool {
	return len(value) >= len(want) && strings.Contains(value, want)
}
