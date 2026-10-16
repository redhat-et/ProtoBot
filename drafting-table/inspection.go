package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var errNotGitWorktree = errors.New("directory is not in a Git working tree")

func gitRoot(cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel")
	command.Env = safeCommandEnv()
	output, err := command.Output()
	if err != nil {
		if ctx.Err() == nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				message := string(exitError.Stderr)
				if strings.Contains(message, "not a git repository") || strings.Contains(message, "must be run in a work tree") {
					return "", errNotGitWorktree
				}
			}
		}
		return "", errors.New("Git working-tree root could not be read")
	}
	root := strings.TrimSpace(string(output))
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("Git returned an invalid working-tree root")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("Git working-tree root could not be resolved")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("Git working-tree root is not a directory")
	}
	return filepath.Clean(root), nil
}

func gitMetadataDirs(root string) (string, string, error) {
	resolve := func(option string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		command := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", option)
		command.Env = safeCommandEnv()
		output, err := command.Output()
		if err != nil {
			return "", errors.New("Git metadata directory could not be read")
		}
		path := strings.TrimSpace(string(output))
		if path == "" {
			return "", errors.New("Git returned an empty metadata directory")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", errors.New("Git metadata directory could not be resolved")
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return "", errors.New("Git metadata path is not a directory")
		}
		return filepath.Clean(path), nil
	}
	gitDir, err := resolve("--absolute-git-dir")
	if err != nil {
		return "", "", err
	}
	commonDir, err := resolve("--git-common-dir")
	if err != nil {
		return "", "", err
	}
	return gitDir, commonDir, nil
}

func safeCommandEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		// Git honors many environment variables that can redirect repository
		// state, invoke helpers, or write traces. The guard needs none of them.
		if !ok || strings.HasPrefix(key, "GIT_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	return env
}

func readYAMLFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("YAML file exceeds the 1 MiB limit")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(false)
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("YAML file must contain one document")
	}
	return nil
}

func loadManifest(root string, manifest *adapterManifest) (string, error) {
	var path string
	var info os.FileInfo
	var err error
	if root != "" {
		path = filepath.Join(root, ".agents", "drafting-table.yaml")
		info, err = os.Lstat(path)
	}
	userScope := root == ""
	if root == "" || errors.Is(err, os.ErrNotExist) {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || home == "" {
			return "", errors.New("project and user-scope adapter manifests are unavailable")
		}
		path = filepath.Join(home, ".agents", "drafting-table.yaml")
		userScope = true
		info, err = os.Lstat(path)
		if err != nil {
			return "", errors.New("project and user-scope adapter manifests are unavailable")
		}
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return filepath.Clean(path), errors.New("the adapter manifest is missing or unreadable")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path), errors.New("the adapter manifest path could not be resolved")
	}
	if userScope {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || !within(home, resolved) {
			return filepath.Clean(resolved), errors.New("the user-scope adapter manifest resolves outside the user home")
		}
	} else if !within(root, resolved) {
		return filepath.Clean(resolved), errors.New("the project adapter manifest resolves outside the project root")
	}
	if err := readYAMLFile(path, manifest); err != nil {
		return filepath.Clean(resolved), errors.New("the adapter manifest is malformed or unreadable")
	}
	if manifest.AdapterLayout != 1 || manifest.EntryPoint != "drafting-table" || len(manifest.ToolkitSkills) == 0 ||
		len(manifest.GovernedCommands) != 1 || manifest.GovernedCommands[0] != "ears-manager" ||
		len(manifest.GovernedMCPServers) != 2 || len(manifest.GovernedMCPServers["wms"].Tools) == 0 || len(manifest.GovernedMCPServers["scm"].Tools) == 0 {
		return filepath.Clean(resolved), errors.New(".agents/drafting-table.yaml does not define adapter layout 1")
	}
	skillName := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	for _, name := range manifest.ToolkitSkills {
		if len(name) > 64 || !skillName.MatchString(name) {
			return filepath.Clean(resolved), errors.New(".agents/drafting-table.yaml contains an invalid skill name")
		}
	}
	return filepath.Clean(resolved), nil
}

func configuredStores(config projectConfig) []string {
	stores := config.Stores
	if stores.Requirements == "" {
		stores.Requirements = ".protobot/requirements"
	}
	if stores.Interfaces == "" {
		stores.Interfaces = ".protobot/interfaces"
	}
	if stores.ChangeSets == "" {
		stores.ChangeSets = ".protobot/change-sets"
	}
	return []string{stores.Requirements, stores.Interfaces, stores.ChangeSets}
}

func earsManagerArtifacts(ctx context.Context, root string) ([]artifact, error) {
	data, err := runEarsManager(ctx, root, "artifact", "list")
	if err != nil {
		return nil, err
	}
	var result struct {
		Artifacts []artifact `json:"artifacts"`
	}
	if err := decodeCLIData(data, &result); err != nil {
		return nil, err
	}
	if result.Artifacts == nil {
		return nil, errors.New("artifact list omitted its artifacts array")
	}
	seen := make(map[string]bool, len(result.Artifacts))
	for _, item := range result.Artifacts {
		if item.ID == "" || item.Path == "" || seen[item.ID] {
			return nil, errors.New("artifact list omitted an id or path")
		}
		seen[item.ID] = true
	}
	return result.Artifacts, nil
}

func earsManagerChangeSets(ctx context.Context, root string) ([]string, error) {
	data, err := runEarsManager(ctx, root, "change-set", "list")
	if err != nil {
		return nil, err
	}
	var result struct {
		ChangeSets []struct {
			ID string `json:"id"`
		} `json:"change_sets"`
	}
	if err := decodeCLIData(data, &result); err != nil {
		return nil, err
	}
	if result.ChangeSets == nil {
		return nil, errors.New("change-set list omitted its change_sets array")
	}
	ids := make([]string, 0, len(result.ChangeSets))
	seen := make(map[string]bool, len(result.ChangeSets))
	for _, item := range result.ChangeSets {
		if item.ID == "" || seen[item.ID] {
			return nil, errors.New("change-set list omitted an id")
		}
		seen[item.ID] = true
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func runEarsManager(ctx context.Context, root string, args ...string) (json.RawMessage, error) {
	path, err := exec.LookPath("ears-manager")
	if err != nil {
		return nil, errors.New("ears-manager is not on PATH")
	}
	commandArgs := append([]string{"--output", "json"}, args...)
	command := exec.CommandContext(ctx, path, commandArgs...)
	command.Dir = root
	command.Env = safeCommandEnv()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	const maxOutput = 8 << 20
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxOutput+1))
	if readErr != nil || len(output) > maxOutput {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, errors.New("ears-manager output could not be read within the size limit")
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("ears-manager read failed: %w", err)
	}
	return output, nil
}

func decodeCLIData(data []byte, destination any) error {
	var envelope struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || !envelope.OK || len(envelope.Data) == 0 {
		return errors.New("ears-manager returned an invalid JSON result")
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		return errors.New("ears-manager result has an invalid data shape")
	}
	return nil
}
