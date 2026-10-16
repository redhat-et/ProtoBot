// Package guard evaluates Drafting Table tool calls and enforces the OpenCode
// tool-use policy.
package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	roleDraftingTable = "drafting-table"
	roleOther         = "other"
	commandTimeout    = 4 * time.Second
)

type hookInput struct {
	HookEventName string         `json:"hook_event_name"`
	SessionID     string         `json:"session_id"`
	CWD           string         `json:"cwd"`
	ToolName      string         `json:"tool_name"`
	ToolInput     map[string]any `json:"tool_input"`
}

type decision struct {
	allow  bool
	rule   string
	reason string
}

type projectConfig struct {
	Project struct {
		ID string `yaml:"id"`
	} `yaml:"project"`
	Repository struct {
		CanonicalRemote string `yaml:"canonical_remote"`
		DefaultBranch   string `yaml:"default_branch"`
		BranchPrefix    string `yaml:"branch_prefix"`
		ReviewMode      string `yaml:"review_mode"`
	} `yaml:"repository"`
	Stores struct {
		Requirements string `yaml:"requirements"`
		Interfaces   string `yaml:"interfaces"`
		ChangeSets   string `yaml:"change_sets"`
	} `yaml:"stores"`
}

type artifact struct {
	ID          string `json:"id" yaml:"id"`
	Path        string `json:"path" yaml:"path"`
	IsDirectory bool   `json:"-" yaml:"-"`
}

type adapterManifest struct {
	AdapterLayout      int                          `yaml:"adapter_layout"`
	EntryPoint         string                       `yaml:"entry_point"`
	ToolkitSkills      []string                     `yaml:"toolkit_skills"`
	GovernedCommands   []string                     `yaml:"governed_commands"`
	GovernedMCPServers map[string]manifestMCPServer `yaml:"governed_mcp_servers"`
}

type manifestMCPServer struct {
	Tools []string `yaml:"tools"`
}

type projectState struct {
	root          string
	gitDir        string
	gitCommonDir  string
	manifestPath  string
	projectFound  bool
	resolutionErr error
	inspection    error
	manifestErr   error
	config        projectConfig
	manifest      adapterManifest
	artifacts     []artifact
	changeSets    map[string]bool
	storePaths    []string
}

func evaluate(harness, role string, input hookInput) decision {
	if harness != openCodeVocabulary.harness {
		return refuse("input", "only the opencode vocabulary is implemented")
	}
	if !filepath.IsAbs(input.CWD) {
		return refuse("project resolution", "cwd must be an absolute directory")
	}
	state := inspectProject(input.CWD)
	if state.resolutionErr != nil {
		return refuse("project resolution", "Git working-tree root could not be inspected; verify the checkout and retry")
	}
	if !state.projectFound && role == roleOther && state.manifestPath == "" {
		return allow()
	}
	if state.inspection != nil {
		return decideWithInspectionFailure(role, input, state)
	}
	return decideTool(role, input, state)
}

func inspectProject(cwd string) projectState {
	state := projectState{changeSets: make(map[string]bool)}
	root, err := gitRoot(cwd)
	if err != nil {
		if errors.Is(err, errNotGitWorktree) {
			manifestPath, manifestErr := loadManifest("", &state.manifest)
			state.manifestPath = manifestPath
			if manifestErr != nil {
				state.manifestErr = manifestErr
			}
			return state
		}
		state.resolutionErr = err
		return state
	}
	state.root = root
	gitDir, gitCommonDir, metadataErr := gitMetadataDirs(root)
	if metadataErr != nil {
		state.resolutionErr = metadataErr
		return state
	}
	state.gitDir = gitDir
	state.gitCommonDir = gitCommonDir
	manifestPath, manifestErr := loadManifest(root, &state.manifest)
	state.manifestPath = manifestPath
	if manifestErr != nil {
		state.manifestErr = manifestErr
	}
	control := filepath.Join(root, ".protobot")
	info, err := os.Lstat(control)
	if errors.Is(err, os.ErrNotExist) {
		return state
	}
	if err != nil {
		state.projectFound = true
		state.inspection = errors.New("could not inspect the project control directory")
		return state
	}
	state.projectFound = true
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		state.inspection = errors.New("the project control path is not a real directory")
		return state
	}
	configPath := filepath.Join(control, "project.yaml")
	configInfo, err := os.Lstat(configPath)
	if err != nil || !configInfo.Mode().IsRegular() || configInfo.Mode()&os.ModeSymlink != 0 {
		state.inspection = errors.New(".protobot/project.yaml is missing or unreadable")
		return state
	}
	if err := readYAMLFile(configPath, &state.config); err != nil {
		state.inspection = errors.New(".protobot/project.yaml is malformed or unreadable")
		return state
	}
	if state.config.Repository.CanonicalRemote == "" || state.config.Repository.DefaultBranch == "" ||
		state.config.Repository.BranchPrefix == "" || state.config.Project.ID == "" ||
		(state.config.Repository.ReviewMode != "single-player" && state.config.Repository.ReviewMode != "multi-player") {
		state.inspection = errors.New(".protobot/project.yaml is missing required project or repository fields")
		return state
	}
	state.storePaths = configuredStores(state.config)
	for _, store := range state.storePaths {
		if filepath.IsAbs(store) || strings.ContainsRune(store, '\x00') {
			state.inspection = errors.New("a configured store path is not project-relative")
			return state
		}
		resolved, err := resolveProjectPath(root, root, store)
		if err != nil || !within(root, resolved) {
			state.inspection = errors.New("a configured store path cannot be resolved safely")
			return state
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	artifacts, err := earsManagerArtifacts(ctx, root)
	if err != nil {
		state.inspection = errors.New("ears-manager artifact list failed")
		return state
	}
	for _, item := range artifacts {
		resolved, err := resolveProjectPath(root, root, item.Path)
		if err != nil || !within(root, resolved) {
			state.inspection = errors.New("a registered artifact path cannot be resolved safely")
			return state
		}
		isDirectory := strings.HasSuffix(item.Path, "/")
		if info, statErr := os.Stat(resolved); statErr == nil && info.IsDir() {
			isDirectory = true
		}
		state.artifacts = append(state.artifacts, artifact{ID: item.ID, Path: resolved, IsDirectory: isDirectory})
	}
	changeSets, err := earsManagerChangeSets(ctx, root)
	if err != nil {
		state.inspection = errors.New("ears-manager change-set list failed")
		return state
	}
	for _, id := range changeSets {
		state.changeSets[id] = true
	}
	return state
}

func decideWithInspectionFailure(role string, input hookInput, state projectState) decision {
	if isFileWriter(input.ToolName) {
		if role == roleDraftingTable {
			return refuse("rule 2", "project inspection failed; verify project.yaml, the adapter manifest, and ears-manager, then retry")
		}
		if targets, err := writeTargets(input.ToolName, input.ToolInput); err != nil {
			return refuse("rule 3", "write target could not be inspected safely")
		} else {
			base := writeTargetBase(input.ToolName, state, input.CWD)
			for _, target := range targets {
				resolved, pathErr := resolveProjectPath(state.root, base, target)
				if pathErr != nil || isLexicalControlPath(state.root, base, target) || isProtectedPathValue(state, base, target) || isControlPath(state.root, resolved) {
					return refuse("rule 2", "project inspection failed; writes under .protobot/ or to other protected control paths are refused")
				}
				if guardedWritePath(state, resolved) {
					return refuse("rule 3", "the target is under a registered artifact or configured store")
				}
			}
		}
		return allow()
	}
	if input.ToolName == openCodeVocabulary.shellTool && role == roleDraftingTable {
		return refuse("rule 2", "project inspection failed; verify project.yaml, the adapter manifest, and ears-manager, then retry")
	}
	if role == roleOther {
		if input.ToolName == openCodeVocabulary.shellTool {
			return decideOtherShell(input, state)
		}
		if isGovernedTool(input.ToolName) {
			return refuse("rule 4", "governed tools are reserved for the drafting-table role")
		}
		return allow()
	}
	return decideTool(role, input, state)
}

func decideTool(role string, input hookInput, state projectState) decision {
	tool := input.ToolName
	if role == roleDraftingTable && !state.projectFound {
		return decideNoProjectTool(input, state)
	}
	if isFileWriter(tool) {
		if role == roleDraftingTable {
			return refuse("rule 5", "file-writing tools are unavailable to the drafting-table role; use ears-manager")
		}
		targets, err := writeTargets(tool, input.ToolInput)
		if err != nil || len(targets) == 0 {
			return refuse("rule 3", "write target could not be inspected safely")
		}
		base := writeTargetBase(tool, state, input.CWD)
		for _, target := range targets {
			if isLexicalControlPath(state.root, base, target) || isProtectedPathValue(state, base, target) {
				return refuse("rule 3", "the target is under a protected control path, registered artifact, or configured store")
			}
			resolved, err := resolveProjectPath(state.root, base, target)
			if err != nil {
				return refuse("rule 3", "write target could not be resolved safely")
			}
			if guardedWritePath(state, resolved) {
				return refuse("rule 3", "the target is under a protected control path, registered artifact, or configured store")
			}
		}
		return allow()
	}

	if tool == openCodeVocabulary.shellTool {
		if role == roleOther {
			return decideOtherShell(input, state)
		}
		return decideDraftingShell(input, state)
	}

	if isReadOrSearchTool(tool) {
		if role != roleDraftingTable {
			return allow()
		}
		return decideRead(tool, input, state)
	}
	if tool == openCodeVocabulary.skillTool {
		if role != roleDraftingTable {
			return allow()
		}
		name, ok := stringField(input.ToolInput, "name")
		if state.manifestErr != nil || !ok || !slices.Contains(state.manifest.ToolkitSkills, name) {
			return refuse("rule 5", "only skills listed in toolkit_skills may be loaded")
		}
		return allow()
	}

	if governed, listed := governedToolDecision(tool, state.manifest); governed {
		if role != roleDraftingTable {
			return refuse("rule 4", "governed tools are reserved for the drafting-table role")
		}
		if state.manifestErr != nil {
			return refuse("rule 4", "the adapter manifest could not be read; verify .agents/drafting-table.yaml")
		}
		if !listed {
			return refuse("rule 4", "this tool is not listed for its governed MCP server")
		}
		return allow()
	}

	if role != roleDraftingTable {
		return allow()
	}
	if slices.Contains(openCodeVocabulary.roleTools, tool) {
		return allow()
	}
	if slices.Contains(openCodeVocabulary.blockedTools, tool) {
		return refuse("rule 5", "subagents, web fetch, and web search are outside the drafting-table role")
	}
	return refuse("rule 5", "the tool is not listed in the OpenCode drafting-table vocabulary")
}

func writeTargetBase(tool string, state projectState, cwd string) string {
	if tool == "apply_patch" && state.root != "" {
		return state.root
	}
	return cwd
}

func allow() decision {
	return decision{allow: true}
}

func refuse(rule, reason string) decision {
	return decision{rule: rule, reason: reason}
}

func isFileWriter(tool string) bool {
	return slices.Contains(openCodeVocabulary.writeTools, tool)
}

func isGovernedTool(tool string) bool {
	return strings.HasPrefix(tool, "wms_") || strings.HasPrefix(tool, "scm_")
}

func governedToolDecision(tool string, manifest adapterManifest) (bool, bool) {
	for server, definition := range manifest.GovernedMCPServers {
		prefix := server + "_"
		if strings.HasPrefix(tool, prefix) {
			return true, slices.Contains(definition.Tools, strings.TrimPrefix(tool, prefix))
		}
	}
	if isGovernedTool(tool) {
		return true, false
	}
	return false, false
}

func stringField(input map[string]any, key string) (string, bool) {
	value, ok := input[key].(string)
	return value, ok && value != ""
}
