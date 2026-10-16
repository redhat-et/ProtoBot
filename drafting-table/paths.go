package guard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func resolveProjectPath(root, cwd, value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') {
		return "", errors.New("empty or invalid path")
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		value = strings.TrimRight(home, string(filepath.Separator)) + string(filepath.Separator) + strings.TrimPrefix(value, "~/")
	} else if value == "~" {
		value, _ = os.UserHomeDir()
		if value == "" {
			return "", errors.New("home directory is unavailable")
		}
	}
	if !filepath.IsAbs(value) {
		base := cwd
		if base == "" {
			base = root
		}
		if base == "" {
			return "", errors.New("cannot resolve a relative path without a working directory")
		}
		resolvedBase, err := filepath.EvalSymlinks(base)
		if err != nil {
			return "", errors.New("working directory cannot be resolved")
		}
		value = strings.TrimRight(resolvedBase, string(filepath.Separator)) + string(filepath.Separator) + value
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("path is not absolute after resolution")
	}
	current := string(filepath.Separator)
	currentIsDirectory := true
	missing := false
	for _, component := range strings.Split(filepath.ToSlash(value), "/") {
		switch component {
		case "":
			continue
		case ".":
			if !missing && !currentIsDirectory {
				return "", errors.New("path traverses through a non-directory")
			}
			continue
		case "..":
			if missing || !currentIsDirectory {
				return "", errors.New("path traverses above a missing or non-directory component")
			}
			current = filepath.Dir(current)
			continue
		}
		if !missing && !currentIsDirectory {
			return "", errors.New("path traverses through a non-directory")
		}
		candidate := filepath.Join(current, filepath.FromSlash(component))
		info, err := os.Lstat(candidate)
		if err == nil {
			if missing {
				current = candidate
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, evalErr := filepath.EvalSymlinks(candidate)
				if evalErr != nil {
					return "", errors.New("symbolic-link resolution failed")
				}
				current = filepath.Clean(resolved)
				targetInfo, statErr := os.Stat(current)
				if statErr != nil {
					return "", errors.New("symbolic-link target cannot be inspected")
				}
				currentIsDirectory = targetInfo.IsDir()
			} else {
				current = candidate
				currentIsDirectory = info.IsDir()
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", errors.New("path cannot be inspected")
		}
		missing = true
		current = candidate
		currentIsDirectory = false
	}
	return filepath.Clean(current), nil
}

func within(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func relativeTo(root, target string) string {
	if !within(root, target) {
		return ""
	}
	relative, _ := filepath.Rel(root, target)
	return filepath.ToSlash(relative)
}

func isControlPath(root, target string) bool {
	relative := relativeTo(root, target)
	if relative == "" {
		return false
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".protobot" {
			return true
		}
	}
	return false
}

func isLexicalControlPath(root, cwd, value string) bool {
	if root == "" || value == "" {
		return false
	}
	base := cwd
	if base == "" {
		base = root
	}
	lexical := value
	if !filepath.IsAbs(lexical) {
		lexical = filepath.Join(base, filepath.FromSlash(lexical))
	}
	lexical, err := filepath.Abs(filepath.Clean(lexical))
	if err != nil {
		return true
	}
	return isControlPath(root, lexical)
}

func isProtectedPathValue(state projectState, cwd, value string) bool {
	lexical, err := lexicalProjectPath(state.root, cwd, value)
	if err != nil || protectedStatePath(state, lexical) {
		return true
	}
	resolved, err := resolveProjectPath(state.root, cwd, value)
	return err != nil || protectedStatePath(state, resolved)
}

func lexicalProjectPath(root, cwd, value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') {
		return "", errors.New("empty or invalid path")
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	if !filepath.IsAbs(value) {
		base := cwd
		if base == "" {
			base = root
		}
		if base == "" {
			return "", errors.New("cannot resolve a relative path without a working directory")
		}
		value = filepath.Join(base, value)
	}
	return filepath.Abs(filepath.Clean(value))
}

func protectedProjectPath(root, target string) bool {
	relative := relativeTo(root, target)
	if relative == "" {
		return false
	}
	// The project manifest and loadable Toolkit skills are control inputs.
	if relative == filepath.ToSlash(filepath.Join(".agents", "drafting-table.yaml")) ||
		relative == filepath.ToSlash(filepath.Join(".agents", "skills")) ||
		strings.HasPrefix(relative, filepath.ToSlash(filepath.Join(".agents", "skills"))+"/") {
		return true
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func protectedStatePath(state projectState, target string) bool {
	if reservedBindingPath(state.root, state.root, target) || isControlPath(state.root, target) ||
		protectedProjectPath(state.root, target) ||
		withinOrEqual(state.gitDir, target) || withinOrEqual(state.gitCommonDir, target) {
		return true
	}
	// Protect the selected manifest's directory: directory writers can replace its contents indirectly.
	if state.manifestPath != "" && withinOrEqual(filepath.Dir(state.manifestPath), target) {
		return true
	}
	for _, store := range state.storePaths {
		resolved, err := resolveProjectPath(state.root, state.root, store)
		if err != nil || withinOrEqual(resolved, target) {
			return true
		}
	}
	return false
}

func withinOrEqual(root, target string) bool {
	return root != "" && target != "" && (filepath.Clean(root) == filepath.Clean(target) || within(root, target))
}

func guardedWritePath(state projectState, target string) bool {
	if protectedStatePath(state, target) {
		return true
	}
	for _, registered := range state.artifacts {
		if filepath.Clean(target) == filepath.Clean(registered.Path) || registered.IsDirectory && within(registered.Path, target) {
			return true
		}
	}
	return false
}

func reservedBindingPath(root, cwd, value string) bool {
	// Check before following symlinks so a reserved binding cannot be escaped through an outside link.
	lexical, err := lexicalProjectPath(root, cwd, value)
	if err != nil || reservedBindingProjectPath(root, lexical) {
		return true
	}
	resolved, err := resolveProjectPath(root, cwd, value)
	return err != nil || reservedBindingProjectPath(root, resolved)
}

func reservedBindingProjectPath(root, target string) bool {
	relative := relativeTo(root, target)
	if relative == "" {
		return false
	}
	parts := strings.Split(relative, "/")
	for _, part := range parts {
		switch part {
		case ".agents", ".claude", ".codex", ".opencode", ".github", ".git":
			return true
		}
	}
	switch relative {
	case "opencode.json", "AGENTS.md", "CLAUDE.md":
		return true
	default:
		return false
	}
}

func credentialPath(root, target, requested string) bool {
	if credentialName(requested) || credentialName(target) {
		return true
	}
	for _, value := range []string{target, requested} {
		for current := filepath.Clean(value); ; current = filepath.Dir(current) {
			base := strings.ToLower(filepath.Base(current))
			switch base {
			case ".ssh", ".gnupg", ".aws", ".kube", ".docker":
				return true
			}
			parent := filepath.Dir(current)
			if parent == current || (root != "" && current == root) {
				break
			}
		}
	}
	return false
}

func credentialName(value string) bool {
	base := strings.ToLower(filepath.Base(value))
	switch base {
	case ".env", ".netrc", ".npmrc", ".pypirc", ".git-credentials":
		return true
	}
	if strings.HasPrefix(base, ".env.") || strings.HasPrefix(base, "id_") {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return strings.Contains(base, "credential") || strings.Contains(base, "secret")
}
