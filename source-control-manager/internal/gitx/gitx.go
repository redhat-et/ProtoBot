// Package gitx runs git as a child process with argument lists, never
// through a shell. Every command gets the same fixed global options: Git
// replace refs ignored, the working-tree root, literal pathspecs, an empty
// hooks directory of the SCM's own, and core.fsmonitor off. The runner
// records the commands that can change state, as the result protocol lists
// them.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner runs git in one working tree.
type Runner struct {
	// Root is the directory that git runs in, with -C.
	Root string

	gitPath  string
	hooksDir string
	env      []string
	recorded [][]string
}

// Opts are the options of one command.
type Opts struct {
	// Ctx is the execution context. If nil, context.Background() is used.
	Ctx context.Context
	// Stdin is the standard input. Nil means no input.
	Stdin []byte
	// Env holds extra environment entries, such as GIT_INDEX_FILE. The
	// result protocol leaves a command's environment out.
	Env []string
	// Record lists the command in the result's commands.
	Record bool
}

// Result is the outcome of one command that ran.
type Result struct {
	Stdout []byte
	Stderr []byte
	Status int
}

// OK reports a zero exit status.
func (r Result) OK() bool { return r.Status == 0 }

// Text returns stdout without trailing white space.
func (r Result) Text() string { return strings.TrimRight(string(r.Stdout), "\r\n\t ") }

// CommandError is a git command that exited non-zero.
type CommandError struct {
	Args   []string
	Status int
	Stderr string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.Status, strings.TrimSpace(e.Stderr))
}

// removedEnv are variables that would point git at another repository,
// index, attribute source, or set of programs than the working tree the
// SCM resolved, change how a pathspec matches, or add configuration
// behind the command line.
var removedEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_PREFIX", "GIT_QUARANTINE_PATH", "GIT_REFLOG_ACTION",
	"GIT_ATTR_SOURCE", "GIT_EXEC_PATH", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_ICASE_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_LITERAL_PATHSPECS",
}

// removedPrefixes are the numbered GIT_CONFIG_KEY_<n> and
// GIT_CONFIG_VALUE_<n> variables.
var removedPrefixes = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}

// fixedEnv makes a missing credential fail instead of prompting, keeps
// every read from writing the user's index, keeps git's messages in one
// language, never opens an editor or a pager, and ignores Git replace
// refs so a refs/replace/ entry cannot change which tree a named commit
// has.
var fixedEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"SSH_ASKPASS_REQUIRE=never",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_MERGE_AUTOEDIT=no",
	"GIT_EDITOR=false",
	"GIT_PAGER=cat",
	"PAGER=cat",
	"GIT_ADVICE=0",
	"LC_ALL=C",
	"LANGUAGE=",
	"GIT_NO_REPLACE_OBJECTS=1",
}

// Timeout bounds every child process, so a call that waits on a network or
// on a program of the user's configuration ends with a failure.
const Timeout = 10 * time.Minute

// NewWithEnv creates a runner for dir using the specified base environment.
// The caller must call Close.
func NewWithEnv(dir string, env []string) (*Runner, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("find git: %w", err)
	}
	hooksDir, err := os.MkdirTemp("", "scm-hooks-")
	if err != nil {
		return nil, fmt.Errorf("create the empty hooks directory: %w", err)
	}
	return &Runner{Root: dir, gitPath: gitPath, hooksDir: hooksDir, env: Environ(env)}, nil
}

// New creates a runner for dir. The caller must call Close.
func New(dir string) (*Runner, error) {
	return NewWithEnv(dir, os.Environ())
}

// Environ returns env without the variables that redirect git, plus the
// SCM's fixed variables.
func Environ(env []string) []string {
	out := make([]string, 0, len(env)+len(fixedEnv))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if contains(removedEnv, name) || hasFixed(name) || hasPrefix(name, removedPrefixes) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, fixedEnv...)
}

func hasPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Command builds a child process with the SCM's bounds: a deadline, no
// controlling terminal, and a short wait for pipes that a grandchild
// holds after the child exits. The caller must call the cancel function.
func Command(name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	return CommandContext(context.Background(), name, args...)
}

// CommandContext builds a child process bounded by ctx and Timeout, with no
// controlling terminal and a short wait for pipes. The caller must call cancel.
func CommandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	subCtx, cancel := context.WithTimeout(ctx, Timeout)
	cmd := exec.CommandContext(subCtx, name, args...)
	cmd.WaitDelay = 10 * time.Second
	Detach(cmd)
	return cmd, cancel
}

func hasFixed(name string) bool {
	for _, entry := range fixedEnv {
		if strings.HasPrefix(entry, name+"=") {
			return true
		}
	}
	return false
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// Close removes the runner's empty hooks directory.
func (r *Runner) Close() {
	if r.hooksDir != "" {
		_ = os.RemoveAll(r.hooksDir)
	}
}

// Recorded returns the recorded commands in the order they ran.
func (r *Runner) Recorded() [][]string {
	out := make([][]string, len(r.recorded))
	copy(out, r.recorded)
	return out
}

// Record adds a command that another client ran, such as gh, to the list.
func (r *Runner) Record(args []string) {
	r.recorded = append(r.recorded, append([]string(nil), args...))
}

// GlobalArgs are the fixed global options that precede every command:
// Git replace refs ignored, the working-tree root, literal pathspecs, the
// empty hooks directory, and core.fsmonitor off. The rest pin behavior
// that would otherwise follow the user's configuration: no fetch, push, or
// checkout ever recurses into a submodule, which is another repository
// than the canonical remote, and no merge message gets a shortlog after
// its trailer.
func (r *Runner) GlobalArgs() []string {
	return []string{
		"--no-replace-objects",
		"-C", r.Root,
		"--literal-pathspecs",
		"-c", "core.hooksPath=" + r.hooksDir,
		"-c", "core.fsmonitor=false",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "push.recurseSubmodules=no",
		"-c", "submodule.recurse=false",
		"-c", "merge.log=false",
	}
}

// Command builds an *exec.Cmd for args using the runner's global args and environment.
// Stdin is always set to an empty reader. The returned stdout and stderr buffers
// receive child output when the process runs.
func (r *Runner) Command(ctx context.Context, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	if ctx == nil {
		ctx = context.Background()
	}
	argv := append(r.GlobalArgs(), args...)
	cmd := exec.CommandContext(ctx, r.gitPath, argv...)
	cmd.WaitDelay = 10 * time.Second
	Detach(cmd)
	cmd.Env = append([]string(nil), r.env...)
	cmd.Stdin = bytes.NewReader(nil)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return cmd, &stdout, &stderr
}

// Run runs git with args. It returns an error only when git could not
// run at all; a non-zero exit is a Result with that status.
func (r *Runner) Run(opts Opts, args ...string) (Result, error) {
	if opts.Record {
		r.Record(append([]string{"git"}, args...))
	}
	argv := append(r.GlobalArgs(), args...)
	cmd, cancel := CommandContext(opts.Ctx, r.gitPath, argv...)
	defer cancel()
	cmd.Env = append(append([]string(nil), r.env...), opts.Env...)
	if opts.Stdin != nil {
		cmd.Stdin = bytes.NewReader(opts.Stdin)
	} else {
		cmd.Stdin = bytes.NewReader(nil)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.Status = exitErr.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("run git: %w", err)
	}
	return res, nil
}

// Read runs a read command that must succeed and returns its stdout
// without trailing white space.
func (r *Runner) Read(args ...string) (string, error) {
	res, err := r.Run(Opts{}, args...)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", &CommandError{Args: args, Status: res.Status, Stderr: string(res.Stderr)}
	}
	return res.Text(), nil
}

// ReadRaw runs a read command that must succeed and returns its stdout
// unchanged.
func (r *Runner) ReadRaw(opts Opts, args ...string) ([]byte, error) {
	res, err := r.Run(opts, args...)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, &CommandError{Args: args, Status: res.Status, Stderr: string(res.Stderr)}
	}
	return res.Stdout, nil
}

// SplitZ splits NUL-terminated output.
func SplitZ(out []byte) []string {
	var items []string
	for _, item := range bytes.Split(out, []byte{0}) {
		if len(item) > 0 {
			items = append(items, string(item))
		}
	}
	return items
}
