package guard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func gitConfigWriteTargets(args []string, state projectState, cwd string) ([]string, bool) {
	if gitConfigHelpRequested(args) {
		return nil, true
	}
	command, index, ok := gitSubcommand(args)
	if !ok {
		return nil, true
	}
	switch {
	case command == "apply":
		return gitApplyWriteTargets(args[index+1:])
	case slices.Contains(gitOutputCommands, command):
		return gitOutputWriteTargets(args, index, state, cwd)
	case command == "config":
		if gitConfigCommandWritesExecutableConfig(args[index+1:]) {
			return nil, false
		}
		return gitConfigMutationTargets(args, index, state)
	default:
		return nil, true
	}
}

var gitOutputCommands = []string{
	"annotate", "blame", "diff", "diff-files", "diff-index", "diff-tree", "log", "range-diff",
	"reflog", "rev-list", "shortlog", "show", "whatchanged",
}

func gitConfigHelpRequested(args []string) bool {
	return len(args) >= 3 && args[1] == "config" && args[2] == "--help"
}

func gitApplyWriteTargets(args []string) ([]string, bool) {
	if containsAny(args, "--check", "--stat", "--numstat", "--summary", "--help", "-h") {
		return nil, true
	}
	return nil, false
}

func gitOutputWriteTargets(args []string, commandIndex int, state projectState, cwd string) ([]string, bool) {
	targets, safe := gitOutputTargets(args[commandIndex+1:])
	if !safe || len(targets) == 0 {
		return targets, safe
	}
	workingDirectory, safe := gitWorkingDirectory(args, commandIndex, state, cwd)
	if !safe {
		return nil, false
	}
	return resolveGitOutputTargets(targets, state, workingDirectory)
}

func resolveGitOutputTargets(targets []string, state projectState, cwd string) ([]string, bool) {
	for index, target := range targets {
		if strings.HasPrefix(target, "~") && target != "~" && !strings.HasPrefix(target, "~/") {
			return nil, false
		}
		resolved, err := resolveProjectPath(state.root, cwd, target)
		if err != nil {
			return nil, false
		}
		targets[index] = resolved
	}
	return targets, true
}

type gitConfigWriteOptions struct {
	positionals []string
	filePath    string
	scope       string
	readOnly    bool
	write       bool
}

func gitConfigActionSubcommand(positionals []string) string {
	if len(positionals) == 0 {
		return ""
	}
	switch positionals[0] {
	case "get", "list", "set", "unset", "rename-section", "remove-section", "edit":
		return positionals[0]
	default:
		return ""
	}
}

func gitConfigMutationTargets(args []string, commandIndex int, state projectState) ([]string, bool) {
	options, safe := parseGitConfigWriteOptions(args[commandIndex+1:])
	if !safe {
		return nil, false
	}
	switch gitConfigActionSubcommand(options.positionals) {
	case "get", "list":
		options.readOnly = true
	case "set", "unset", "rename-section", "remove-section", "edit":
		options.write = true
	}
	if options.readOnly || !options.write && len(options.positionals) < 2 {
		return nil, true
	}
	return gitConfigWriteLocation(options, gitConfigRedirected(args, commandIndex), state)
}

func parseGitConfigWriteOptions(args []string) (gitConfigWriteOptions, bool) {
	var options gitConfigWriteOptions
	for index := 0; index < len(args); {
		arg := args[index]
		if arg == "--" {
			options.positionals = append(options.positionals, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			options.positionals = append(options.positionals, arg)
			index++
			continue
		}
		next, safe := parseGitConfigWriteOption(args, index, &options)
		if !safe {
			return options, false
		}
		index = next
	}
	return options, true
}

func parseGitConfigWriteOption(args []string, index int, options *gitConfigWriteOptions) (int, bool) {
	option, value, hasValue := strings.Cut(args[index], "=")
	switch option {
	case "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--get-color", "--get-colorbool", "--list", "-l", "--name-only":
		options.readOnly = true
	case "--show-origin", "--show-scope":
		return index + 1, true
	case "--add", "--replace-all", "--unset", "--unset-all", "--unset-regexp", "--rename-section", "--remove-section", "--edit", "-e":
		options.write = true
	case "--file", "-f":
		return parseGitConfigFileOption(args, index, value, hasValue, options)
	case "--local", "--global", "--system", "--worktree":
		return parseGitConfigScopeOption(index, option, options)
	case "--type":
		return consumeGitConfigTypeValue(args, index, hasValue)
	case "--bool", "--int", "--bool-or-int", "--path", "--expiry-date", "--fixed-value", "--includes", "--no-includes", "--null", "-z":
		return parseGitConfigFlagOption(index, hasValue)
	default:
		return index, false
	}
	return index + 1, true
}

func parseGitConfigFileOption(args []string, index int, value string, hasValue bool, options *gitConfigWriteOptions) (int, bool) {
	value, next, safe := gitConfigOptionValue(args, index, value, hasValue)
	if safe {
		options.filePath = value
	}
	return next, safe
}

func parseGitConfigScopeOption(index int, option string, options *gitConfigWriteOptions) (int, bool) {
	if options.scope != "" {
		return index, false
	}
	options.scope = option
	return index + 1, true
}

func consumeGitConfigTypeValue(args []string, index int, hasValue bool) (int, bool) {
	if hasValue {
		return index + 1, true
	}
	if index+1 >= len(args) {
		return index, false
	}
	return index + 2, true
}

func parseGitConfigFlagOption(index int, hasValue bool) (int, bool) {
	if hasValue {
		return index, false
	}
	return index + 1, true
}

func gitConfigWriteLocation(options gitConfigWriteOptions, redirected bool, state projectState) ([]string, bool) {
	if options.filePath != "" {
		return gitConfigFileWriteLocation(options, redirected)
	}
	if options.scope == "--global" || options.scope == "--system" {
		return nil, true
	}
	if redirected {
		return nil, false
	}
	return gitConfigRepositoryWriteLocation(options.scope, state)
}

func gitConfigFileWriteLocation(options gitConfigWriteOptions, redirected bool) ([]string, bool) {
	if options.scope != "" || redirected {
		return nil, false
	}
	return []string{options.filePath}, true
}

func gitConfigRepositoryWriteLocation(scope string, state projectState) ([]string, bool) {
	if scope == "--worktree" {
		if state.gitDir == "" {
			return nil, false
		}
		return []string{filepath.Join(state.gitDir, "config.worktree")}, true
	}
	if state.gitCommonDir == "" || state.root == "" {
		return nil, false
	}
	return []string{filepath.Join(state.gitCommonDir, "config")}, true
}

func gitConfigRedirected(args []string, commandIndex int) bool {
	for index := 1; index < commandIndex; index++ {
		arg := args[index]
		option, _, hasValue := strings.Cut(arg, "=")
		if !hasValue && slices.Contains([]string{"-C", "--git-dir", "--work-tree"}, option) ||
			strings.HasPrefix(arg, "-C") && len(arg) > 2 ||
			strings.HasPrefix(arg, "--git-dir=") || strings.HasPrefix(arg, "--work-tree=") {
			return true
		}
	}
	return false
}

func gitWorkingDirectory(args []string, commandIndex int, state projectState, cwd string) (string, bool) {
	if cwd == "" {
		cwd = state.root
	}
	for index := 1; index < commandIndex; index++ {
		path, next, found, safe := gitWorkingDirectoryOption(args, index, commandIndex)
		if !safe {
			return "", false
		}
		if !found {
			continue
		}
		index = next
		cwd, safe = resolveGitWorkingDirectory(state.root, cwd, path)
		if !safe {
			return "", false
		}
	}
	return cwd, true
}

func gitWorkingDirectoryOption(args []string, index, commandIndex int) (string, int, bool, bool) {
	if args[index] == "-C" {
		if index+1 >= commandIndex {
			return "", index, false, false
		}
		return args[index+1], index + 1, true, true
	}
	if strings.HasPrefix(args[index], "-C") && len(args[index]) > 2 {
		return args[index][2:], index, true, true
	}
	return "", index, false, true
}

func resolveGitWorkingDirectory(root, cwd, path string) (string, bool) {
	resolved, err := resolveProjectPath(root, cwd, path)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return resolved, true
}

func gitOutputTargets(args []string) ([]string, bool) {
	var targets []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		option, value, hasValue := strings.Cut(arg, "=")
		if option != "--output" {
			continue
		}
		if hasValue {
			if value == "" {
				return nil, false
			}
		} else {
			if i+1 >= len(args) || args[i+1] == "--" || strings.HasPrefix(args[i+1], "-") {
				return nil, false
			}
			i++
			value = args[i]
		}
		targets = append(targets, value)
	}
	return targets, true
}
