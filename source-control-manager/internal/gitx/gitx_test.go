package gitx

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestEnvironScrubsRedirectsAndDisablesReplaceRefs(t *testing.T) {
	env := Environ([]string{
		"PATH=/usr/bin",
		"GIT_DIR=/other",
		"GIT_NO_REPLACE_OBJECTS=0",
		"LC_ALL=en_US.UTF-8",
		"GIT_CONFIG_KEY_0=core.hooksPath",
	})
	if !slices.Contains(env, "PATH=/usr/bin") {
		t.Fatalf("PATH was stripped: %q", env)
	}
	for _, banned := range []string{"GIT_DIR=/other", "GIT_NO_REPLACE_OBJECTS=0", "LC_ALL=en_US.UTF-8", "GIT_CONFIG_KEY_0=core.hooksPath"} {
		if slices.Contains(env, banned) {
			t.Fatalf("env still has %s: %q", banned, env)
		}
	}
	if !slices.Contains(env, "GIT_NO_REPLACE_OBJECTS=1") {
		t.Fatalf("env missing GIT_NO_REPLACE_OBJECTS=1: %q", env)
	}
}

func TestEnvironForwardsCallerGitConfigGlobal(t *testing.T) {
	customGlobal := "GIT_CONFIG_GLOBAL=/custom/path/.gitconfig"
	customNosystem := "GIT_CONFIG_NOSYSTEM=0"
	env := Environ([]string{
		"PATH=/usr/bin",
		customGlobal,
		customNosystem,
	})
	if !slices.Contains(env, customGlobal) {
		t.Fatalf("Environ stripped caller GIT_CONFIG_GLOBAL: %q", env)
	}
	if !slices.Contains(env, customNosystem) {
		t.Fatalf("Environ stripped caller GIT_CONFIG_NOSYSTEM: %q", env)
	}

	root := t.TempDir()
	runner, err := NewWithEnv(root, []string{customGlobal, customNosystem})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	cmd := runner.CommandContext(context.Background(), "status")
	if !slices.Contains(cmd.Env, customGlobal) {
		t.Fatalf("runner command env missing caller GIT_CONFIG_GLOBAL: %q", cmd.Env)
	}
	if !slices.Contains(cmd.Env, customNosystem) {
		t.Fatalf("runner command env missing caller GIT_CONFIG_NOSYSTEM: %q", cmd.Env)
	}
}

func TestGlobalArgsDisableReplaceRefs(t *testing.T) {
	runner := &Runner{Root: "/repo", hooksDir: "/hooks"}
	args := runner.GlobalArgs()
	if len(args) == 0 || args[0] != "--no-replace-objects" {
		t.Fatalf("GlobalArgs = %q, want --no-replace-objects first", args)
	}
}

func TestRunnerIgnoresReplaceRefs(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("canonical\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	git("commit", "--quiet", "-m", "canonical")
	original := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("replaced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	git("replace", original, git("commit-tree", git("write-tree"), "-m", "replacement"))
	git("reset", "--quiet", "--hard")

	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Close)

	got, ok, err := runner.Commit("HEAD")
	if err != nil || !ok {
		t.Fatalf("Commit(HEAD) = %q ok=%v err=%v", got, ok, err)
	}
	if got != original {
		t.Fatalf("Commit(HEAD) = %s, want canonical %s", got, original)
	}
	text, err := runner.Read("show", "HEAD:file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if text != "canonical" {
		t.Fatalf("show HEAD:file.txt = %q, want canonical", text)
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
