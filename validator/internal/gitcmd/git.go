// Package gitcmd runs Git as a child process with argument lists, never
// through a shell. Isolation environment and global options keep the
// command inside the intended checkout.
package gitcmd

import (
	"os"
	"os/exec"
	"slices"
	"strings"
)

// removedEnv are variables that would point Git at another repository,
// index, attribute source, or set of programs than the working tree that
// the command resolved.
var removedEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_PREFIX", "GIT_QUARANTINE_PATH", "GIT_REFLOG_ACTION",
	"GIT_ATTR_SOURCE", "GIT_EXEC_PATH", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_ICASE_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_LITERAL_PATHSPECS",
}

var removedEnvPrefixes = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}

var fixedEnv = []string{"LC_ALL=C", "LANGUAGE=", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1"}

func isolationArgs() []string {
	return []string{
		"--no-replace-objects",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "submodule.recurse=false",
	}
}

// Command builds a Git child process with the isolated environment and
// global options. Every Git command of the validator runs through it.
func Command(args ...string) *exec.Cmd {
	command := exec.Command("git", append(isolationArgs(), args...)...)
	command.Env = Environ(os.Environ())
	return command
}

// Environ returns env without the variables that redirect Git, plus the
// fixed isolation variables.
func Environ(env []string) []string {
	result := make([]string, 0, len(env)+len(fixedEnv))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if slices.Contains(removedEnv, name) || slices.ContainsFunc(removedEnvPrefixes, func(prefix string) bool {
			return strings.HasPrefix(name, prefix)
		}) || slices.ContainsFunc(fixedEnv, func(fixed string) bool {
			return strings.HasPrefix(fixed, name+"=")
		}) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, fixedEnv...)
}
