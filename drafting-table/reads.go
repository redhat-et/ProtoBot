package guard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func decideRead(tool string, input hookInput, state projectState) decision {
	value := ""
	if tool == "read" {
		var ok bool
		value, ok = stringField(input.ToolInput, "filePath")
		if !ok {
			return refuse("rule 5", "read requires a filePath that can be inspected")
		}
	} else {
		if raw, exists := input.ToolInput["path"]; exists {
			var ok bool
			value, ok = raw.(string)
			if !ok || value == "" {
				return refuse("rule 5", "search path must be a string that can be inspected")
			}
		} else {
			value = input.CWD
		}
		if raw, exists := input.ToolInput["pattern"]; tool == "glob" && exists {
			pattern, ok := raw.(string)
			if !ok || unsafeSearchPattern(pattern) {
				return refuse("rule 5", "search pattern may escape its resolved path")
			}
		}
	}
	resolved, err := resolveProjectPath(state.root, input.CWD, value)
	if err != nil {
		return refuse("rule 5", "read path could not be resolved safely")
	}
	if reason := readPathRefusal(state, resolved, value); reason != "" {
		return refuse("rule 5", reason)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return refuse("rule 5", "read or search path could not be inspected")
	}
	if info.IsDir() {
		if err := scanSearchTree(state, resolved); err != nil {
			return refuse("rule 5", "read or search includes a denied path or an unresolved symbolic link")
		}
	}
	return allow()
}

func readPathRefusal(state projectState, resolved, requested string) string {
	if credentialPath(state.root, resolved, requested) {
		return "credential files are not readable by the drafting-table role"
	}
	if containsGitPath(state.root, resolved) || state.root == "" && hasPathComponent(resolved, ".git") {
		return "reads under .git/ are not available to the drafting-table role"
	}
	projectConfigPath := ""
	if state.root != "" {
		projectConfigPath = filepath.Join(state.root, ".protobot", "project.yaml")
	}
	if isControlPath(state.root, resolved) && filepath.Clean(resolved) != filepath.Clean(projectConfigPath) {
		return "reads under .protobot/ are unavailable except for project.yaml"
	}
	if state.root == "" || !within(state.root, resolved) {
		if state.manifestErr != nil {
			return "the adapter manifest could not be read; user-scope Toolkit skills are unavailable"
		}
		if globalSkill, name := globalToolkitSkill(resolved); !globalSkill || !slices.Contains(state.manifest.ToolkitSkills, name) {
			return "reads outside the project are unavailable except for user-scope Toolkit skills"
		}
	}
	if name, ok := projectSkillName(state.root, resolved); ok {
		if state.manifestErr != nil || !slices.Contains(state.manifest.ToolkitSkills, name) {
			return "only skills listed in toolkit_skills may be loaded"
		}
	}
	return ""
}

func hasPathComponent(value, name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(value)), "/") {
		if part == name {
			return true
		}
	}
	return false
}

func containsGitPath(root, target string) bool {
	if root == "" || !within(root, target) {
		return false
	}
	relative, _ := filepath.Rel(root, target)
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func projectSkillName(root, target string) (string, bool) {
	if root == "" || !within(root, target) {
		return "", false
	}
	relative := filepath.ToSlash(relativeTo(root, target))
	parts := strings.Split(relative, "/")
	if len(parts) < 3 {
		return "", false
	}
	for i := 0; i+2 < len(parts); i++ {
		if (parts[i] == ".agents" || parts[i] == ".claude" || parts[i] == ".opencode") && parts[i+1] == "skills" {
			return parts[i+2], true
		}
	}
	return "", false
}

func globalToolkitSkill(target string) (bool, string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false, ""
	}
	roots := []string{
		filepath.Join(home, ".config", "opencode", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	for _, root := range roots {
		resolvedRoot, resolveErr := resolveProjectPath("", home, root)
		if resolveErr != nil || !within(home, resolvedRoot) || !within(resolvedRoot, target) {
			continue
		}
		relative, _ := filepath.Rel(resolvedRoot, target)
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) >= 1 {
			return true, parts[0]
		}
	}
	return false, ""
}

func unsafeSearchPattern(pattern string) bool {
	if filepath.IsAbs(pattern) || strings.ContainsRune(pattern, '\x00') {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(pattern), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func scanSearchTree(state projectState, root string) error {
	count := 0
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 10000 {
			return errors.New("search tree is too large to inspect")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("search tree contains a symbolic link")
		}
		resolved, resolveErr := resolveProjectPath(state.root, state.root, path)
		globalSkill, globalName := globalToolkitSkill(resolved)
		projectPath := state.root != "" && within(state.root, resolved)
		if resolveErr != nil || (!projectPath && (state.manifestErr != nil || !globalSkill || !slices.Contains(state.manifest.ToolkitSkills, globalName))) {
			return errors.New("search tree path cannot be resolved")
		}
		if credentialPath(state.root, resolved, path) || containsGitPath(state.root, resolved) {
			return errors.New("search tree contains a denied path")
		}
		if isControlPath(state.root, resolved) && filepath.Clean(resolved) != filepath.Join(state.root, ".protobot", "project.yaml") {
			return errors.New("search tree contains .protobot")
		}
		if skillName, isSkill := projectSkillName(state.root, resolved); isSkill && !slices.Contains(state.manifest.ToolkitSkills, skillName) {
			return errors.New("search tree contains a skill outside toolkit_skills")
		}
		return nil
	})
}
