package golden_test

// The driver of the repository fixture against the SCM
// (docs/architecture/source-control-manager.md#repository-fixture-against-the-scm).
// It starts `source-control-manager serve --face drafting-table` in the
// clone and calls its tools as an MCP client, with no harness and no model.
// It starts ears-manager and the registration command with argument lists.
// No step runs a shell; a file edit is the driver standing in for the
// user's text editor.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	canonical   = "https://github.com/protobot-fixture/fixture.git"
	repoID      = "github.com/protobot-fixture/fixture"
	projectID   = "fixture"
	modernMCP   = "2026-07-28"
	legacyMCP   = "2025-11-25"
	fixtureFile = "../../../docs/architecture/fixtures/source-control-manager-golden.jsonl"
)

// realEarsEnv names a real ears-manager binary. When it is set, the replay
// runs against that binary instead of the stub, and the checks that need
// the stub's own controls are skipped.
const realEarsEnv = "SCM_FIXTURE_EARS_MANAGER"

// binaries are built once for the whole test binary.
var binaries struct {
	dir      string
	scm      string
	err      error
	realEars bool
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "scm-golden-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binaries.dir = dir
	targets := []struct{ pkg, name string }{
		{"../../cmd/source-control-manager", "source-control-manager"},
		{"../testing/ghstub", "gh"},
	}
	if real := os.Getenv(realEarsEnv); real != "" {
		abs, err := filepath.Abs(real)
		if err == nil {
			err = os.Symlink(abs, filepath.Join(dir, "ears-manager"))
		}
		if err != nil {
			binaries.err = fmt.Errorf("use the ears-manager of %s: %w", realEarsEnv, err)
		}
		binaries.realEars = true
	} else {
		targets = append(targets, struct{ pkg, name string }{"../testing/earsstub", "ears-manager"})
	}
	for _, target := range targets {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, target.name), target.pkg)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			binaries.err = fmt.Errorf("build %s: %w", target.pkg, err)
			break
		}
	}
	binaries.scm = filepath.Join(dir, "source-control-manager")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// golden holds the fixture's steps by name.
type golden map[string]map[string]any

func loadGolden(t *testing.T) golden {
	t.Helper()
	file, err := os.Open(fixtureFile)
	if err != nil {
		t.Fatalf("open the golden fixture: %v", err)
	}
	defer func() { _ = file.Close() }()
	steps := golden{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<22)
	for scanner.Scan() {
		var step map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &step); err != nil {
			t.Fatalf("parse the golden fixture: %v", err)
		}
		steps[step["step"].(string)] = step
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read the golden fixture: %v", err)
	}
	return steps
}

// driver is the fixture. Every scenario works in the same directory, so
// the absolute paths in the clones' configuration stay valid when a
// snapshot is restored.
type driver struct {
	t      *testing.T
	golden golden
	shas   map[string]string
	base   string
	work   string

	session *mcp.ClientSession
	outputs []string
	// reviewed is the impact file of step 7: a disposition for every
	// candidate that 7-impact returned.
	reviewed []byte
}

// stubOnly skips a check that drives the ears-manager stub's own
// controls, when the replay runs against a real ears-manager.
func (d *driver) stubOnly() {
	d.t.Helper()
	if binaries.realEars {
		d.t.Skip("this check drives the ears-manager stub; " + realEarsEnv + " names a real ears-manager")
	}
}

func (d *driver) origin() string { return filepath.Join(d.work, "origin.git") }
func (d *driver) clone() string  { return filepath.Join(d.work, "clone") }
func (d *driver) second() string { return filepath.Join(d.work, "second") }
func (d *driver) ghState() string {
	return filepath.Join(d.work, "gh.json")
}
func (d *driver) earsControl() string { return filepath.Join(d.work, "ears-control.json") }
func (d *driver) markers() string     { return filepath.Join(d.work, "markers") }

func (d *driver) env() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", "HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "SCM_GH_STUB_STATE", "EARS_STUB_CONTROL",
			"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GH_REPO", "GH_HOST":
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"PATH="+binaries.dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+filepath.Join(d.work, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(d.work, "home", ".config"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(d.work, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"SCM_GH_STUB_STATE="+d.ghState(),
		"EARS_STUB_CONTROL="+d.earsControl(),
	)
}

// run runs a program with an argument list and returns its stdout and
// exit status.
func (d *driver) run(dir string, stdin []byte, name string, args ...string) (string, int) {
	d.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = d.env()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	status := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			d.t.Fatalf("run %s %v: %v", name, args, err)
		}
		status = exitErr.ExitCode()
	}
	return stdout.String(), status
}

// git runs a driver git command that must succeed.
func (d *driver) git(dir string, args ...string) string {
	d.t.Helper()
	return d.gitWith(dir, nil, args...)
}

// inspect runs a driver read with no hook and no fsmonitor, so a check of
// the state never runs a planted repository program itself.
func (d *driver) inspect(dir string, args ...string) string {
	d.t.Helper()
	return d.gitWith(dir, []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)
}

func (d *driver) gitWith(dir string, options []string, args ...string) string {
	d.t.Helper()
	cmd := exec.Command("git", append(options, args...)...)
	cmd.Dir = dir
	cmd.Env = d.env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (d *driver) rev(dir, rev string) string {
	d.t.Helper()
	return d.git(dir, "rev-parse", "--verify", rev)
}

func (d *driver) write(rel, content string) {
	d.t.Helper()
	path := filepath.Join(d.clone(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *driver) appendLine(rel, line string) {
	d.t.Helper()
	path := filepath.Join(d.clone(), filepath.FromSlash(rel))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		d.t.Fatal(err)
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

func (d *driver) read(rel string) string {
	d.t.Helper()
	data, err := os.ReadFile(filepath.Join(d.clone(), filepath.FromSlash(rel)))
	if err != nil {
		d.t.Fatal(err)
	}
	return string(data)
}

// setup builds #34's base state: a bare origin with one commit on main,
// a clone whose canonical remote an insteadOf rule rewrites to origin,
// and a second clone that stands in for the host and other people.
func (d *driver) setup() {
	d.t.Helper()
	for _, dir := range []string{d.work, filepath.Join(d.work, "home"), d.markers()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			d.t.Fatal(err)
		}
	}
	gitconfig := "[init]\n\tdefaultBranch = main\n[commit]\n\tgpgSign = false\n[advice]\n\tdetachedHead = false\n"
	if err := os.WriteFile(filepath.Join(d.work, "gitconfig"), []byte(gitconfig), 0o644); err != nil {
		d.t.Fatal(err)
	}
	d.saveJSON(d.ghState(), map[string]any{"down": false, "next_number": 1, "pulls": []any{}, "recorded": []any{}})
	d.saveJSON(d.earsControl(), map[string]any{})
	d.saveJSON(filepath.Join(d.work, "registration.json"), map[string]any{"calls": []any{}})

	seed := filepath.Join(d.work, "seed")
	d.git(d.work, "init", "--quiet", "--bare", "origin.git")
	d.git(d.work, "init", "--quiet", "seed")
	d.identity(seed, "Seed", "seed@fixture.example")
	for rel, content := range map[string]string{
		"README.md":            "# Fixture\n",
		"docs/vision.md":       "# Existing Vision\n",
		"docs/architecture.md": "# Existing Architecture\n",
	} {
		path := filepath.Join(seed, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			d.t.Fatal(err)
		}
	}
	d.git(seed, "add", "--", "README.md", "docs/vision.md", "docs/architecture.md")
	d.git(seed, "commit", "--quiet", "-m", "Initial commit")
	d.git(seed, "push", "--quiet", d.origin(), "main")
	_ = os.RemoveAll(seed)

	d.git(d.work, "clone", "--quiet", d.origin(), "clone")
	d.identity(d.clone(), "Fixture User", "user@fixture.example")
	d.git(d.clone(), "remote", "set-url", "origin", canonical)
	d.git(d.clone(), "config", "url."+d.origin()+".insteadOf", canonical)

	d.git(d.work, "clone", "--quiet", d.origin(), "second")
	d.identity(d.second(), "Other Contributor", "other@fixture.example")
}

func (d *driver) identity(dir, name, email string) {
	d.git(dir, "config", "user.name", name)
	d.git(dir, "config", "user.email", email)
}

func (d *driver) saveJSON(path string, v any) {
	d.t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *driver) loadJSON(path string, v any) {
	d.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		d.t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		d.t.Fatal(err)
	}
}

// snapshot copies the work directory; restore puts a copy back in the
// same place.
func (d *driver) snapshot(name string) {
	d.t.Helper()
	d.closeSession()
	if err := copyTree(d.work, filepath.Join(d.base, "snapshots", name)); err != nil {
		d.t.Fatal(err)
	}
}

func (d *driver) restore(name string) {
	d.t.Helper()
	d.closeSession()
	if err := os.RemoveAll(d.work); err != nil {
		d.t.Fatal(err)
	}
	if err := copyTree(filepath.Join(d.base, "snapshots", name), d.work); err != nil {
		d.t.Fatal(err)
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm()|0o200)
		}
	})
}

// connect starts the SCM's MCP server in dir and opens a client session
// at a protocol revision.
func (d *driver) connect(dir, version string) *mcp.ClientSession {
	d.t.Helper()
	if binaries.err != nil {
		d.t.Fatal(binaries.err)
	}
	cmd := exec.Command(binaries.scm, "serve", "--face", "drafting-table")
	cmd.Dir = dir
	cmd.Env = d.env()
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture-driver", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		d.t.Fatalf("connect to the SCM: %v", err)
	}
	return session
}

func (d *driver) closeSession() {
	if d.session != nil {
		_ = d.session.Close()
		d.session = nil
	}
}

// call calls a tool in the clone and returns the result document.
func (d *driver) call(tool string, args map[string]any) []byte {
	d.t.Helper()
	if d.session == nil {
		d.session = d.connect(d.clone(), modernMCP)
	}
	return d.callOn(d.session, tool, args)
}

func (d *driver) callOn(session *mcp.ClientSession, tool string, args map[string]any) []byte {
	d.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		d.t.Fatalf("call %s: %v", tool, err)
	}
	if len(res.Content) != 1 {
		d.t.Fatalf("call %s: %d content items", tool, len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		d.t.Fatalf("call %s: the content is not text", tool)
	}
	var doc struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(text.Text), &doc); err != nil {
		d.t.Fatalf("call %s: the result is not JSON: %v", tool, err)
	}
	if res.IsError == doc.OK {
		d.t.Fatalf("call %s: isError is %v but ok is %v", tool, res.IsError, doc.OK)
	}
	d.outputs = append(d.outputs, text.Text)
	return []byte(text.Text)
}

// cli runs the SCM's CLI in the clone.
func (d *driver) cli(args ...string) ([]byte, int) {
	d.t.Helper()
	out, status := d.run(d.clone(), nil, binaries.scm, args...)
	d.outputs = append(d.outputs, out)
	return []byte(out), status
}

// ears runs ears-manager with an argument list.
func (d *driver) ears(argv []string, stdin []byte) (map[string]any, int) {
	d.t.Helper()
	out, status := d.run(d.clone(), stdin, filepath.Join(binaries.dir, "ears-manager"), argv...)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		d.t.Fatalf("ears-manager %v: %v\n%s", argv, err, out)
	}
	return doc, status
}

// earsStep runs the ears-manager step of the fixture and checks its exit
// status.
func (d *driver) earsStep(step string) map[string]any {
	d.t.Helper()
	s := d.step(step)
	em := s["ears_manager"].(map[string]any)
	var argv []string
	for _, arg := range em["argv"].([]any) {
		argv = append(argv, d.subst(arg.(string)))
	}
	var stdin []byte
	if text, ok := s["stdin"].(string); ok {
		stdin = []byte(text)
	}
	if text, ok := em["stdin"].(string); ok {
		stdin = []byte(text)
		if strings.HasPrefix(text, "the reviewed dispositions") {
			stdin = d.reviewed
		}
	}
	doc, status := d.ears(argv, stdin)
	if want := int(em["exit"].(float64)); status != want {
		d.t.Fatalf("%s: ears-manager exited %d, want %d: %v", step, status, want, doc)
	}
	if want, ok := s["mutation_paths"]; ok {
		wantPaths, ok := want.([]any)
		if !ok {
			d.t.Fatalf("%s: mutation_paths must be an array, got %T", step, want)
		}
		mutation, ok := doc["mutation"].(map[string]any)
		if !ok {
			d.t.Fatalf("%s: expected mutation object, got %T", step, doc["mutation"])
		}
		gotPaths, ok := mutation["paths"].([]any)
		if !ok {
			d.t.Fatalf("%s: expected mutation.paths array, got %T", step, mutation["paths"])
		}
		sortedPaths := func(name string, paths []any) []any {
			values := make([]string, len(paths))
			for i, path := range paths {
				value, ok := path.(string)
				if !ok {
					d.t.Fatalf("%s: %s[%d] must be a string, got %T", step, name, i, path)
				}
				values[i] = value
			}
			sort.Strings(values)
			sorted := make([]any, len(values))
			for i, value := range values {
				sorted[i] = value
			}
			return sorted
		}
		wantPaths = sortedPaths("mutation_paths", wantPaths)
		gotPaths = sortedPaths("mutation.paths", gotPaths)
		if err := d.match(step+".mutation.paths", wantPaths, gotPaths); err != nil {
			d.t.Fatalf("%v: %v", err, doc)
		}
	}
	return doc
}

// checkStep runs a failing ears-manager check step and compares the
// code, path, record_id, and field of each diagnostic with the step's.
func (d *driver) checkStep(step string) {
	d.t.Helper()
	doc := d.earsStep(step)
	got := []any{}
	failure, _ := doc["error"].(map[string]any)
	diagnostics, _ := failure["diagnostics"].([]any)
	for _, item := range diagnostics {
		diagnostic, _ := item.(map[string]any)
		kept := map[string]any{}
		for _, key := range []string{"code", "path", "record_id", "field"} {
			if value, ok := diagnostic[key]; ok {
				kept[key] = value
			}
		}
		got = append(got, kept)
	}
	if err := d.match(step+".diagnostics", d.step(step)["diagnostics"], got); err != nil {
		d.t.Fatalf("%v: %v", err, doc)
	}
}

// review records a disposition for every candidate of an impact result,
// standing in for the user's review.
func (d *driver) review(impact map[string]any) {
	d.t.Helper()
	list := []map[string]string{}
	data, _ := impact["data"].(map[string]any)
	candidates, _ := data["candidates"].([]any)
	for _, item := range candidates {
		c, _ := item.(map[string]any)
		id, _ := c["requirement_id"].(string)
		origin, _ := c["origin"].(string)
		list = append(list, map[string]string{
			"requirement_id": id, "disposition": "applicable",
			"rationale": "Reviewed in the repository fixture.", "origin": origin,
		})
	}
	reviewed, err := json.Marshal(list)
	if err != nil {
		d.t.Fatal(err)
	}
	d.reviewed = reviewed
}

func (d *driver) step(name string) map[string]any {
	d.t.Helper()
	s, ok := d.golden[name]
	if !ok {
		d.t.Fatalf("the golden fixture has no step %s", name)
	}
	return s
}

// callStep calls the tool of a fixture step with its arguments and
// compares the result with the step's expected result.
func (d *driver) callStep(name string) []byte {
	d.t.Helper()
	s := d.step(name)
	callSpec := s["call"].(map[string]any)
	args, _ := callSpec["arguments"].(map[string]any)
	before := d.stateDigest()
	gh := d.ghRecorded()
	got := d.call(callSpec["tool"].(string), args)
	d.expect(name, s["result"], got)
	d.checkFailureLeftState(name, got, before)
	if want, ok := s["gh_stub"].(map[string]any); ok {
		d.expectRecorded(name, want["recorded"], gh)
	}
	return got
}

// expect compares a result with the fixture's expected result.
func (d *driver) expect(step string, want any, got []byte) {
	d.t.Helper()
	var doc any
	if err := json.Unmarshal(got, &doc); err != nil {
		d.t.Fatalf("%s: %v", step, err)
	}
	if err := d.match("result", want, doc); err != nil {
		d.t.Fatalf("%s: %v\ngot: %s", step, err, got)
	}
}

var shaPattern = regexp.MustCompile(`^<sha:([A-Za-z0-9]+)>$`)
var hexSha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// match compares an expected value with an actual one. <sha:NAME> binds
// a full commit hash on first sight and compares it on every later sight;
// <computed> and <rendered:...> match any value.
func (d *driver) match(path string, want, got any) error {
	switch w := want.(type) {
	case string:
		if w == "<computed>" || strings.HasPrefix(w, "<rendered:") {
			return nil
		}
		if m := shaPattern.FindStringSubmatch(w); m != nil {
			s, ok := got.(string)
			if !ok || !hexSha.MatchString(s) {
				return fmt.Errorf("%s: want a commit hash for %s, got %v", path, w, got)
			}
			if bound, ok := d.shas[m[1]]; ok && bound != s {
				return fmt.Errorf("%s: %s is %s, got %s", path, w, bound, s)
			}
			d.shas[m[1]] = s
			return nil
		}
		if got != w {
			return fmt.Errorf("%s: want %q, got %v", path, w, got)
		}
		return nil
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object, got %v", path, got)
		}
		for key := range g {
			if _, ok := w[key]; !ok {
				return fmt.Errorf("%s: unexpected field %q", path, key)
			}
		}
		keys := make([]string, 0, len(w))
		for key := range w {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, ok := g[key]
			if !ok {
				return fmt.Errorf("%s: missing field %q", path, key)
			}
			if err := d.match(path+"."+key, w[key], value); err != nil {
				return err
			}
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Errorf("%s: want %d items, got %v", path, len(w), got)
		}
		for i := range w {
			if err := d.match(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); err != nil {
				return err
			}
		}
		return nil
	default:
		if fmt.Sprint(want) != fmt.Sprint(got) {
			return fmt.Errorf("%s: want %v, got %v", path, want, got)
		}
		return nil
	}
}

// bind binds a name to a value, or checks it.
func (d *driver) bind(name, value string) {
	d.t.Helper()
	if bound, ok := d.shas[name]; ok && bound != value {
		d.t.Fatalf("<sha:%s> is %s, got %s", name, bound, value)
	}
	d.shas[name] = value
}

func (d *driver) sha(name string) string {
	d.t.Helper()
	value, ok := d.shas[name]
	if !ok {
		d.t.Fatalf("<sha:%s> is not bound", name)
	}
	return value
}

// subst replaces a <sha:NAME> argument with its bound hash.
func (d *driver) subst(arg string) string {
	if m := shaPattern.FindStringSubmatch(arg); m != nil {
		return d.sha(m[1])
	}
	return arg
}

type ghCall struct {
	Argv  []string `json:"argv"`
	Stdin string   `json:"stdin"`
}

type ghPull struct {
	Repo                string          `json:"repo"`
	Number              int             `json:"number"`
	State               string          `json:"state"`
	URL                 string          `json:"url"`
	Title               string          `json:"title"`
	Body                string          `json:"body"`
	HeadRefName         string          `json:"headRefName"`
	BaseRefName         string          `json:"baseRefName"`
	IsCrossRepository   bool            `json:"isCrossRepository"`
	HeadRepository      map[string]any  `json:"headRepository"`
	HeadRepositoryOwner map[string]any  `json:"headRepositoryOwner"`
	MergeCommit         *map[string]any `json:"mergeCommit"`
}

type ghState struct {
	Down     bool              `json:"down"`
	Next     int               `json:"next_number"`
	Pulls    []ghPull          `json:"pulls"`
	Recorded []ghCall          `json:"recorded"`
	Calls    int               `json:"calls"`
	Fail     map[string]string `json:"fail,omitempty"`
}

func (d *driver) gh() ghState {
	var state ghState
	d.loadJSON(d.ghState(), &state)
	return state
}

func (d *driver) setGH(update func(*ghState)) {
	state := d.gh()
	update(&state)
	d.saveJSON(d.ghState(), state)
}

func (d *driver) ghRecorded() int { return len(d.gh().Recorded) }

// expectRecorded compares the gh calls recorded since since with the
// fixture's list.
func (d *driver) expectRecorded(step string, want any, since int) {
	d.t.Helper()
	recorded := d.gh().Recorded[since:]
	var got []any
	for _, call := range recorded {
		argv := make([]any, len(call.Argv))
		for i, a := range call.Argv {
			argv[i] = a
		}
		got = append(got, map[string]any{"argv": argv, "stdin": call.Stdin})
	}
	if got == nil {
		got = []any{}
	}
	if err := d.match("gh_stub.recorded", want, got); err != nil {
		d.t.Fatalf("%s: %v", step, err)
	}
}

// lastBody returns the body of the last recorded gh call.
func (d *driver) lastBody() string {
	recorded := d.gh().Recorded
	if len(recorded) == 0 {
		d.t.Fatal("no gh call was recorded")
	}
	return recorded[len(recorded)-1].Stdin
}

// hostMerge merges a change-set branch into main of origin with a merge
// commit in the second clone, standing in for the host merge button, and
// marks its pull request merged in the gh stub.
func (d *driver) hostMerge(branch string, number int) string {
	d.t.Helper()
	d.git(d.second(), "fetch", "--quiet", "origin")
	d.git(d.second(), "checkout", "--quiet", "main")
	d.git(d.second(), "merge", "--quiet", "--ff-only", "origin/main")
	d.git(d.second(), "merge", "--quiet", "--no-ff", "-m", fmt.Sprintf("Merge pull request #%d", number), "origin/"+branch)
	d.git(d.second(), "push", "--quiet", "origin", "main")
	merge := d.rev(d.second(), "HEAD")
	d.setGH(func(s *ghState) {
		for i := range s.Pulls {
			if s.Pulls[i].Number == number {
				s.Pulls[i].State = "MERGED"
				s.Pulls[i].MergeCommit = &map[string]any{"oid": merge}
			}
		}
	})
	return merge
}

// upstreamCommit pushes a commit to main of origin from the second clone.
func (d *driver) upstreamCommit(rel, content, message string) string {
	d.t.Helper()
	d.git(d.second(), "fetch", "--quiet", "origin")
	d.git(d.second(), "checkout", "--quiet", "main")
	d.git(d.second(), "merge", "--quiet", "--ff-only", "origin/main")
	path := filepath.Join(d.second(), filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		d.t.Fatal(err)
	}
	d.git(d.second(), "add", "--", rel)
	d.git(d.second(), "commit", "--quiet", "-m", message)
	d.git(d.second(), "push", "--quiet", "origin", "main")
	return d.rev(d.second(), "HEAD")
}

type registration struct {
	ChangeSetID        string `json:"change_set_id"`
	MergeCommit        string `json:"merge_commit"`
	MaterializationKey string `json:"materialization_key"`
	IdempotencyKey     string `json:"idempotency_key"`
}

func (d *driver) registrations() []registration {
	var log struct {
		Calls []registration `json:"calls"`
	}
	d.loadJSON(filepath.Join(d.work, "registration.json"), &log)
	return log.Calls
}

func keyOf(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// record is the registration stub: one idempotent call per change set;
// a different merge commit for the same change set is rejected for
// reconciliation.
func (d *driver) record(changeSet, merge string) bool {
	calls := d.registrations()
	for _, call := range calls {
		if call.ChangeSetID == changeSet {
			return call.MergeCommit == merge
		}
	}
	calls = append(calls, registration{
		ChangeSetID: changeSet, MergeCommit: merge,
		MaterializationKey: keyOf(projectID, changeSet, merge),
		IdempotencyKey:     keyOf("register-approved-change-set", projectID, changeSet, merge),
	})
	d.saveJSON(filepath.Join(d.work, "registration.json"), map[string]any{"calls": calls})
	return true
}

// register stands in for register-approved-change-set: it takes the
// change-set ID only, reads the merge commit through approved_merge, and
// calls the registration stub.
func (d *driver) register(changeSet string) ([]byte, int) {
	d.t.Helper()
	out, status := d.cli("--output", "json", "approved-merge", "--change-set", changeSet)
	if status != 0 {
		return out, 1
	}
	var doc struct {
		Data struct {
			MergeCommit string `json:"merge_commit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		d.t.Fatal(err)
	}
	if !d.record(changeSet, doc.Data.MergeCommit) {
		return out, 1
	}
	return out, 0
}

func (d *driver) registrationsOf(changeSet string) []registration {
	var out []registration
	for _, call := range d.registrations() {
		if call.ChangeSetID == changeSet {
			out = append(out, call)
		}
	}
	return out
}

// stateDigest captures local branches, the index, the working tree, the
// remote, and the host.
func (d *driver) stateDigest() string {
	d.t.Helper()
	var b strings.Builder
	if _, err := os.Stat(d.clone()); err != nil {
		return ""
	}
	b.WriteString(d.inspect(d.clone(), "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"))
	b.WriteString("\n--\n" + d.inspect(d.clone(), "symbolic-ref", "-q", "HEAD"))
	b.WriteString("\n--\n" + d.inspect(d.clone(), "ls-files", "-s"))
	_ = filepath.WalkDir(d.clone(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			data, _ := os.ReadFile(path)
			sum := sha256.Sum256(data)
			rel, _ := filepath.Rel(d.clone(), path)
			fmt.Fprintf(&b, "\n%s %x", rel, sum[:8])
		}
		return nil
	})
	b.WriteString("\n--\n" + d.inspect(d.origin(), "for-each-ref", "--format=%(refname) %(objectname)"))
	state := d.gh()
	pulls, _ := json.Marshal(state.Pulls)
	b.Write(pulls)
	return b.String()
}

// checkFailureLeftState checks that a failed call left local branches,
// the index, the working tree, the remote, and the host as they were,
// unless it reports a partial or unknown mutation.
func (d *driver) checkFailureLeftState(step string, got []byte, before string) {
	d.t.Helper()
	var doc struct {
		OK    bool `json:"ok"`
		Error struct {
			Mutation string `json:"mutation"`
		} `json:"error"`
	}
	_ = json.Unmarshal(got, &doc)
	if doc.OK || doc.Error.Mutation != "none" || skipStateCheck[step] {
		return
	}
	if after := d.stateDigest(); after != before {
		d.t.Fatalf("%s: the failed call changed state\nbefore:\n%s\nafter:\n%s", step, before, after)
	}
}

// skipStateCheck lists failures whose state another writer changed during
// the call, on purpose.
var skipStateCheck = map[string]bool{"scm-38-commit": true}
