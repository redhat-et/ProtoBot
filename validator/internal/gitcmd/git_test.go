package gitcmd

import (
	"os"
	"slices"
	"testing"
)

func TestGitEnvironScrubsRedirectsAndDisablesReplaceRefs(t *testing.T) {
	env := Environ([]string{
		"PATH=/usr/bin",
		"GIT_DIR=/other",
		"GIT_WORK_TREE=/other",
		"GIT_NO_REPLACE_OBJECTS=0",
		"LC_ALL=en_US.UTF-8",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
	})
	if !slices.Contains(env, "PATH=/usr/bin") {
		t.Fatalf("PATH was stripped: %q", env)
	}
	for _, banned := range []string{
		"GIT_DIR=/other",
		"GIT_WORK_TREE=/other",
		"GIT_NO_REPLACE_OBJECTS=0",
		"LC_ALL=en_US.UTF-8",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
	} {
		if slices.Contains(env, banned) {
			t.Fatalf("env still has %s: %q", banned, env)
		}
	}
	for _, fixed := range []string{"LC_ALL=C", "LANGUAGE=", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1"} {
		if !slices.Contains(env, fixed) {
			t.Fatalf("env missing %s: %q", fixed, env)
		}
	}
}

func TestGitCommandPrependsIsolationArgs(t *testing.T) {
	cmd := Command("-C", "/repo", "status")
	want := []string{
		"git",
		"--no-replace-objects",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "submodule.recurse=false",
		"-C", "/repo",
		"status",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("Args = %q, want %q", cmd.Args, want)
	}
	if !slices.Contains(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1") {
		t.Fatalf("Env missing GIT_NO_REPLACE_OBJECTS=1: %q", cmd.Env)
	}
}
