package guard

import (
	"path/filepath"
	"slices"
	"strings"
)

func shellWritesProtectedPath(command string, state projectState, cwd string, depth int) bool {
	return shellWritesProtectedPathAfterDirectoryChange(command, state, cwd, depth, false)
}

// shellWritesProtectedPathAfterDirectoryChange carries the working-directory
// change into nested child scripts and find/xargs child argv: once the
// directory has changed, a relative writer target is ambiguous and refused,
// exactly as a relative redirection target is.
func shellWritesProtectedPathAfterDirectoryChange(command string, state projectState, cwd string, depth int, directoryChanged bool) bool {
	if depth > maxShellAnalysisDepth || len(command) > maxShellCommandBytes || !otherShellSyntaxInspectable(command) {
		return true
	}
	changesDirectory := directoryChanged
	for _, words := range shellCommandSegments(command) {
		if opaqueShellCommand(words) {
			return true
		}
		if wrapperChangesDirectory(words) {
			changesDirectory = true
		}
		args := unwrapCommand(words)
		if len(args) == 0 {
			continue
		}
		if slices.Contains([]string{"cd", "pushd", "popd", "source", "."}, filepath.Base(args[0])) {
			changesDirectory = true
		}
		if shellWriterArgvProtected(args, state, cwd, depth, changesDirectory) {
			return true
		}
		nested, safe := nestedShellCommandsAtDepth(words, depth)
		if !safe {
			return true
		}
		for _, child := range nested {
			if child.execDir && shellScriptHasRelativeWriter(child.script, state, cwd, depth+1) {
				return true
			}
			if shellWritesProtectedPathAfterDirectoryChange(child.script, state, cwd, depth+1, changesDirectory) {
				return true
			}
		}
	}
	return false
}

func shellWriterArgvProtected(args []string, state projectState, cwd string, depth int, execDir bool) bool {
	if depth > maxShellAnalysisDepth {
		return true
	}
	args = unwrapCommand(args)
	if len(args) == 0 {
		return false
	}
	targets, safe := protectedShellWriterTargets(args, state, cwd)
	if !safe {
		return true
	}
	for _, target := range targets {
		if (execDir && !filepath.IsAbs(target)) || protectedShellTarget(state, cwd, target) {
			return true
		}
	}
	switch filepath.Base(args[0]) {
	case "find":
		actions, safe := findActions(args)
		if !safe {
			return true
		}
		for _, action := range actions {
			if shellWriterArgvProtected(action.args, state, cwd, depth+1, action.execDir || execDir) {
				return true
			}
		}
	case "xargs":
		child, found, safe := xargsCommand(args)
		if !safe {
			return true
		}
		if found && shellWriterArgvProtected(child, state, cwd, depth+1, execDir) {
			return true
		}
	}
	return false
}

func shellScriptHasRelativeWriter(script string, state projectState, cwd string, depth int) bool {
	if depth > maxShellAnalysisDepth || len(script) > maxShellCommandBytes {
		return true
	}
	for _, words := range shellCommandSegments(script) {
		args := unwrapCommand(words)
		if len(args) == 0 {
			continue
		}
		targets, safe := protectedShellWriterTargets(args, state, cwd)
		if !safe {
			return true
		}
		for _, target := range targets {
			if !filepath.IsAbs(target) {
				return true
			}
		}
	}
	return false
}

func protectedShellTarget(state projectState, cwd, target string) bool {
	if target == "" || strings.ContainsAny(target, "*?[]{}") {
		return true
	}
	if strings.HasPrefix(target, "~") && target != "~" && !strings.HasPrefix(target, "~/") {
		return true
	}
	return isLexicalControlPath(state.root, cwd, target) || isProtectedPathValue(state, cwd, target)
}
