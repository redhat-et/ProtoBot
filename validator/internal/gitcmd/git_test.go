package gitcmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestGitEnvironScrubsRedirectsAndDisablesReplaceRefs(t *testing.T) {
	env := Environ([]string{
		"PATH=/usr/bin",
		"GIT_DIR=/other",
		"GIT_WORK_TREE=/other",
		"GIT_NO_REPLACE_OBJECTS=0",
		"GIT_SSH=/custom/ssh",
		"GIT_SSH_COMMAND=ssh -i id_rsa",
		"LC_ALL=en_US.UTF-8",
		"GIT_CONFIG=/custom/gitconfig",
		"GIT_CONFIG_GLOBAL=/custom/global",
		"GIT_CONFIG_SYSTEM=/custom/system",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
		"GIT_SSH=/usr/bin/ssh",
		"GIT_SSH_COMMAND=ssh -v",
		"GIT_PROXY_COMMAND=none",
		"GIT_ALLOW_PROTOCOL=http",
		"GIT_ASKPASS=/usr/bin/askpass",
	})
	if !slices.Contains(env, "PATH=/usr/bin") {
		t.Fatalf("PATH was stripped: %q", env)
	}
	for _, banned := range []string{
		"GIT_DIR=/other",
		"GIT_WORK_TREE=/other",
		"GIT_NO_REPLACE_OBJECTS=0",
		"GIT_SSH=/custom/ssh",
		"GIT_SSH_COMMAND=ssh -i id_rsa",
		"LC_ALL=en_US.UTF-8",
		"GIT_CONFIG=/custom/gitconfig",
		"GIT_CONFIG_GLOBAL=/custom/global",
		"GIT_CONFIG_SYSTEM=/custom/system",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
		"GIT_SSH=/usr/bin/ssh",
		"GIT_SSH_COMMAND=ssh -v",
		"GIT_PROXY_COMMAND=none",
		"GIT_ALLOW_PROTOCOL=http",
		"GIT_ASKPASS=/usr/bin/askpass",
	} {
		if slices.Contains(env, banned) {
			t.Fatalf("env still has %s: %q", banned, env)
		}
	}
	for _, fixed := range []string{
		"LC_ALL=C",
		"LANGUAGE=",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS_REQUIRE=never",
		"GIT_CONFIG_NOSYSTEM=1",
	} {
		if !slices.Contains(env, fixed) {
			t.Fatalf("env missing %s: %q", fixed, env)
		}
	}
}

func TestEnvironStripsGitConfigAndSetsNoSystem(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"GIT_CONFIG=/custom/gitconfig",
		"GIT_CONFIG_GLOBAL=/custom/global",
		"GIT_CONFIG_SYSTEM=/custom/system",
	}
	env := Environ(input)
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_CONFIG=") ||
			strings.HasPrefix(entry, "GIT_CONFIG_GLOBAL=") ||
			strings.HasPrefix(entry, "GIT_CONFIG_SYSTEM=") {
			t.Errorf("expected %q to be stripped from environment", entry)
		}
	}
	if !slices.Contains(env, "GIT_CONFIG_NOSYSTEM=1") {
		t.Errorf("expected GIT_CONFIG_NOSYSTEM=1 to be present in environment")
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
		"-c", "protocol.file.allow=never",
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

func TestGitInvocationsUseManagedRunner(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	assertNoUnmanagedGitCommands(t, filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")))
}

func assertNoUnmanagedGitCommands(t *testing.T, root string) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", ".git":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if hasPathSegment(rel, "testing") {
			return nil
		}
		base := filepath.Base(path)
		if base == "git.go" || base == "gitx.go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || lit.Value != `"git"` {
				return true
			}
			t.Errorf("%s: unmanaged exec.%s(\"git\")", fset.Position(call.Pos()), sel.Sel.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hasPathSegment(rel, name string) bool {
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		if segment == name {
			return true
		}
	}
	return false
}
