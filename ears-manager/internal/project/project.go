package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/schema"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

type GitRootFunc func(string) (string, error)

type Project struct {
	Root       string
	ConfigPath string
	Config     records.ProjectConfig
}

func Discover(start string) (Project, error) {
	return DiscoverWithGitRoot(start, systemGitRoot)
}

func DiscoverWithGitRoot(start string, gitRoot GitRootFunc) (Project, error) {
	if gitRoot == nil {
		return Project{}, fmt.Errorf("git root resolver is nil")
	}
	current, err := existingDirectory(start)
	if err != nil {
		return Project{}, err
	}
	for {
		configPath := filepath.Join(current, ".protobot", "project.yaml")
		if _, statErr := os.Stat(configPath); statErr == nil {
			if _, pathErr := storage.ValidatePathWithin(current, filepath.Join(".protobot", "project.yaml")); pathErr != nil {
				return Project{}, fmt.Errorf("project configuration: %w", pathErr)
			}
			root, rootErr := gitRoot(current)
			if rootErr != nil {
				return Project{}, fmt.Errorf("resolve Git working-tree root: %w", rootErr)
			}
			root, rootErr = canonicalDirectory(root)
			if rootErr != nil {
				return Project{}, fmt.Errorf("resolve Git working-tree root: %w", rootErr)
			}
			if root != current {
				return Project{}, fmt.Errorf("project configuration is at %s, but Git working-tree root is %s", current, root)
			}
			config, loadErr := loadConfig(configPath, root)
			if loadErr != nil {
				return Project{}, loadErr
			}
			return Project{Root: root, ConfigPath: configPath, Config: config}, nil
		} else if !os.IsNotExist(statErr) {
			return Project{}, fmt.Errorf("inspect project configuration: %w", statErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return Project{}, fmt.Errorf("no .protobot/project.yaml found from %s upward", start)
		}
		current = parent
	}
}

func (p Project) RequirementStore() (*storage.Store[records.Requirement], error) {
	return newRecordStore(p.Root, p.Config.Stores.Requirements, records.RequirementStore, func(value records.Requirement) string { return value.ID })
}

func (p Project) InterfaceStore() (*storage.Store[records.InterfaceRecord], error) {
	return newRecordStore(p.Root, p.Config.Stores.Interfaces, records.InterfaceStore, func(value records.InterfaceRecord) string { return value.ID })
}

func (p Project) ChangeSetStore() (*storage.Store[records.ChangeSet], error) {
	return newRecordStore(p.Root, p.Config.Stores.ChangeSets, records.ChangeSetStore, func(value records.ChangeSet) string { return value.ID })
}

func (p Project) Artifact(id string) (records.ArtifactEntry, error) {
	if err := records.ValidateArtifactID(id); err != nil {
		return records.ArtifactEntry{}, err
	}
	for _, artifact := range p.Config.Artifacts {
		if artifact.ID == id {
			return artifact, nil
		}
	}
	return records.ArtifactEntry{}, fmt.Errorf("artifact %q was not found", id)
}

func (p *Project) SaveConfig(config records.ProjectConfig) error {
	config = records.CanonicalProjectConfig(config)
	if err := schema.Validate(config.SchemaVersions); err != nil {
		return err
	}
	if err := validateConfigPaths(p.Root, config); err != nil {
		return err
	}
	if err := storage.WriteFile(p.ConfigPath, config); err != nil {
		return err
	}
	p.Config = config
	return nil
}

func (p *Project) SaveArtifact(artifact records.ArtifactEntry) error {
	if err := records.ValidateArtifactID(artifact.ID); err != nil {
		return err
	}
	config := p.Config
	config.Artifacts = append([]records.ArtifactEntry(nil), p.Config.Artifacts...)
	found := false
	for i := range config.Artifacts {
		if config.Artifacts[i].ID == artifact.ID {
			config.Artifacts[i] = artifact
			found = true
			break
		}
	}
	if !found {
		config.Artifacts = append(config.Artifacts, artifact)
	}
	return p.SaveConfig(config)
}

func systemGitRoot(path string) (string, error) {
	command := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func existingDirectory(start string) (string, error) {
	if start == "" {
		return "", fmt.Errorf("start path must not be empty")
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve start path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("inspect start path: %w", err)
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	return canonicalDirectory(abs)
}

func canonicalDirectory(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

func loadConfig(path, root string) (records.ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return records.ProjectConfig{}, fmt.Errorf("read project configuration: %w", err)
	}
	var config records.ProjectConfig
	if err := storage.Decode(data, &config); err != nil {
		return records.ProjectConfig{}, fmt.Errorf("decode project configuration: %w", err)
	}
	config = records.CanonicalProjectConfig(config)
	if err := schema.Validate(config.SchemaVersions); err != nil {
		return records.ProjectConfig{}, err
	}
	if err := validateConfigPaths(root, config); err != nil {
		return records.ProjectConfig{}, err
	}
	return config, nil
}

func validateConfigPaths(root string, config records.ProjectConfig) error {
	for name, path := range map[string]string{
		"requirements": config.Stores.Requirements,
		"interfaces":   config.Stores.Interfaces,
		"change_sets":  config.Stores.ChangeSets,
	} {
		resolved, err := storage.ValidatePathWithin(root, path)
		if err != nil {
			return fmt.Errorf("%s store path: %w", name, err)
		}
		if info, statErr := os.Stat(resolved); statErr == nil && !info.IsDir() {
			return fmt.Errorf("%s store path %q is not a directory", name, path)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect %s store path: %w", name, statErr)
		}
	}
	seen := make(map[string]struct{}, len(config.Artifacts))
	for _, artifact := range config.Artifacts {
		if err := records.ValidateArtifactID(artifact.ID); err != nil {
			return err
		}
		if _, exists := seen[artifact.ID]; exists {
			return fmt.Errorf("duplicate artifact id %q", artifact.ID)
		}
		seen[artifact.ID] = struct{}{}
		if _, err := storage.ValidatePathWithin(root, artifact.Path); err != nil {
			return fmt.Errorf("artifact %q path: %w", artifact.ID, err)
		}
	}
	return nil
}

func newRecordStore[T any](root, directory string, kind records.StoreKind, id func(T) string) (*storage.Store[T], error) {
	return storage.New(root, directory, func(value string) (string, error) {
		return records.FilenameFor(kind, value)
	}, storage.Codec[T]{
		ID: id,
		Decode: func(data []byte) (T, error) {
			var value T
			if err := storage.Decode(data, &value); err != nil {
				return value, err
			}
			return value, nil
		},
		Encode: func(value T) ([]byte, error) { return storage.Encode(value) },
	})
}
