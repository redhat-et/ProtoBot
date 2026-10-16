package jobsite

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/source-control-manager/internal/gitx"
)

const (
	objectFormatSHA1 = "sha1"
	branchMain       = "main"
	fixtureAuthor    = "ProtoBot Fixture"
	fixtureEmail     = "protobot-fixture@invalid"
	fixtureDate      = "2020-01-01T00:00:00+0000"
)

var fixtureIdentity = []string{
	"GIT_AUTHOR_NAME=" + fixtureAuthor,
	"GIT_AUTHOR_EMAIL=" + fixtureEmail,
	"GIT_AUTHOR_DATE=" + fixtureDate,
	"GIT_COMMITTER_NAME=" + fixtureAuthor,
	"GIT_COMMITTER_EMAIL=" + fixtureEmail,
	"GIT_COMMITTER_DATE=" + fixtureDate,
}

type gitRepo struct {
	path   string
	runner *gitx.Runner
}

type blobEntry struct {
	Path string
	Mode string
	OID  string
}

func openGitWithEnv(path string, env []string) (*gitRepo, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	runner, err := gitx.NewWithEnv(path, env)
	if err != nil {
		return nil, err
	}
	return &gitRepo{path: path, runner: runner}, nil
}

func openGit(path string) (*gitRepo, error) {
	return openGitWithEnv(path, os.Environ())
}

func (g *gitRepo) Close() {
	if g != nil && g.runner != nil {
		g.runner.Close()
	}
}

func (g *gitRepo) run(args ...string) (gitx.Result, error) {
	return g.runner.Run(gitx.Opts{}, args...)
}

func (g *gitRepo) check(res gitx.Result, err error, args ...string) error {
	if err != nil {
		return err
	}
	if !res.OK() {
		return &gitx.CommandError{Args: args, Status: res.Status, Stderr: string(res.Stderr)}
	}
	return nil
}

func (g *gitRepo) text(args ...string) (string, error) {
	return g.runner.Read(args...)
}

func initRepo(path string) (*gitRepo, error) {
	repo, err := openGit(path)
	if err != nil {
		return nil, err
	}
	res, err := repo.run("init", "--quiet", "--object-format="+objectFormatSHA1, "-b", branchMain)
	if err := repo.check(res, err, "init"); err != nil {
		repo.Close()
		return nil, err
	}
	if err := repo.configureIsolation(); err != nil {
		repo.Close()
		return nil, err
	}
	return repo, nil
}

func (g *gitRepo) configureIsolation() error {
	commands := [][]string{
		{"config", "core.logAllRefUpdates", "false"},
		{"config", "core.autocrlf", "false"},
		{"config", "commit.gpgsign", "false"},
		{"config", "tag.gpgsign", "false"},
		{"config", "user.name", fixtureAuthor},
		{"config", "user.email", fixtureEmail},
		{"config", "protocol.file.allow", "never"},
		{"config", "protocol.ext.allow", "never"},
		{"config", "core.protectNTFS", "true"},
		{"config", "core.protectHFS", "true"},
	}
	for _, args := range commands {
		res, err := g.run(args...)
		if err := g.check(res, err, args...); err != nil {
			return err
		}
	}
	return nil
}

func (g *gitRepo) resolveCommit(rev string) (string, error) {
	out, err := g.text("rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fail(CodeExportSource, "Source commit could not be resolved.")
	}
	return out, nil
}

func (g *gitRepo) listBlobs(rev string) ([]blobEntry, error) {
	out, err := g.runner.ReadRaw(gitx.Opts{}, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var entries []blobEntry
	for _, line := range gitx.SplitZ(out) {
		meta, projectPath, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		parts := strings.Fields(meta)
		if len(parts) != 3 {
			continue
		}
		entries = append(entries, blobEntry{Path: projectPath, Mode: parts[0], OID: parts[2]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func (g *gitRepo) readBlob(oid string) ([]byte, error) {
	return g.runner.ReadRaw(gitx.Opts{}, "cat-file", "blob", oid)
}

func (g *gitRepo) writeBlob(content []byte) (string, error) {
	res, err := g.runner.Run(gitx.Opts{Stdin: content}, "hash-object", "-w", "--stdin")
	if err := g.check(res, err, "hash-object"); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (g *gitRepo) catExists(oid string) (bool, error) {
	res, err := g.run("cat-file", "-e", oid)
	if err != nil {
		return false, err
	}
	return res.OK(), nil
}

func (g *gitRepo) head() (string, error) {
	return g.text("rev-parse", "HEAD")
}

func (g *gitRepo) remotes() ([]string, error) {
	res, err := g.run("remote")
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, &gitx.CommandError{Args: []string{"remote"}, Status: res.Status, Stderr: string(res.Stderr)}
	}
	text := strings.TrimSpace(string(res.Stdout))
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func (g *gitRepo) configList() (string, error) {
	res, err := g.run("config", "--local", "--list")
	if err != nil {
		return "", err
	}
	if res.Status == 1 {
		return "", nil
	}
	if !res.OK() {
		return "", &gitx.CommandError{Args: []string{"config", "--local", "--list"}, Status: res.Status, Stderr: string(res.Stderr)}
	}
	return string(res.Stdout), nil
}

func (g *gitRepo) gitDir() (string, error) {
	return g.text("rev-parse", "--absolute-git-dir")
}

func (g *gitRepo) hasAlternates() (bool, error) {
	dir, err := g.gitDir()
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(filepath.Join(dir, "objects", "info", "alternates"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (g *gitRepo) scrubMeta() error {
	dir, err := g.gitDir()
	if err != nil {
		return err
	}
	for _, name := range []string{"FETCH_HEAD", "ORIG_HEAD", "COMMIT_EDITMSG"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	_ = os.RemoveAll(filepath.Join(dir, "logs"))
	return nil
}

func (g *gitRepo) checkout(ref string) error {
	res, err := g.run("checkout", "-f", "--quiet", ref)
	return g.check(res, err, "checkout")
}

func (g *gitRepo) updateRef(ref, oid string) error {
	res, err := g.run("update-ref", ref, oid)
	return g.check(res, err, "update-ref")
}

func (g *gitRepo) updateRefCAS(ref, newOID, oldOID string) error {
	res, err := g.run("update-ref", ref, newOID, oldOID)
	return g.check(res, err, "update-ref")
}

func (g *gitRepo) isCASMismatch(err error, expectedOld string) bool {
	var cmdErr *gitx.CommandError
	if errors.As(err, &cmdErr) {
		if strings.Contains(cmdErr.Stderr, "but expected") || strings.Contains(cmdErr.Stderr, "is at") {
			return true
		}
	}
	if currentHead, headErr := g.head(); headErr == nil && currentHead != expectedOld {
		return true
	}
	return false
}

func (g *gitRepo) commitTree(tree, message string) (string, error) {
	return g.commitTreeWithParent(tree, "", message)
}

func (g *gitRepo) commitTreeWithParent(tree, parent, message string) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	res, err := g.runner.Run(gitx.Opts{Env: fixtureIdentity}, args...)
	if err := g.check(res, err, "commit-tree"); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (g *gitRepo) writeTree(entries []blobEntry) (string, error) {
	root := newTreeNode()
	for _, entry := range entries {
		if err := root.insert(entry.Path, entry.Mode, entry.OID); err != nil {
			return "", err
		}
	}
	return g.storeTree(root)
}

type treeNode struct {
	files map[string]blobEntry
	dirs  map[string]*treeNode
}

func newTreeNode() *treeNode {
	return &treeNode{files: map[string]blobEntry{}, dirs: map[string]*treeNode{}}
}

func (n *treeNode) insert(projectPath, mode, oid string) error {
	parts := strings.Split(projectPath, "/")
	if len(parts) == 1 {
		for dirName := range n.dirs {
			if strings.EqualFold(dirName, parts[0]) {
				return fmt.Errorf("path component %q is already a directory", parts[0])
			}
		}
		for fileName := range n.files {
			if strings.EqualFold(fileName, parts[0]) {
				return fmt.Errorf("path component %q case-collides with existing file %q", parts[0], fileName)
			}
		}
		n.files[parts[0]] = blobEntry{Path: parts[0], Mode: mode, OID: oid}
		return nil
	}
	for fileName := range n.files {
		if strings.EqualFold(fileName, parts[0]) {
			return fmt.Errorf("path component %q is already a file", parts[0])
		}
	}
	child, ok := n.dirs[parts[0]]
	if !ok {
		for dirName := range n.dirs {
			if strings.EqualFold(dirName, parts[0]) {
				return fmt.Errorf("path component %q case-collides with existing directory %q", parts[0], dirName)
			}
		}
		child = newTreeNode()
		n.dirs[parts[0]] = child
	}
	return child.insert(strings.Join(parts[1:], "/"), mode, oid)
}

func (g *gitRepo) storeTree(node *treeNode) (string, error) {
	type line struct {
		name string
		body string
	}
	var lines []line
	for name, child := range node.dirs {
		oid, err := g.storeTree(child)
		if err != nil {
			return "", err
		}
		lines = append(lines, line{name: name, body: fmt.Sprintf("040000 tree %s\t%s", oid, name)})
	}
	for name, file := range node.files {
		lines = append(lines, line{name: name, body: fmt.Sprintf("%s blob %s\t%s", file.Mode, file.OID, name)})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	var buf bytes.Buffer
	for _, item := range lines {
		buf.WriteString(item.body)
		buf.WriteByte(0)
	}
	res, err := g.runner.Run(gitx.Opts{Stdin: buf.Bytes()}, "mktree", "-z")
	if err := g.check(res, err, "mktree"); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (g *gitRepo) fetchCommit(sourcePath, commit, ref string) error {
	res, err := g.run("-c", "protocol.file.allow=always", "fetch", "--no-tags", "--update-head-ok", "--", sourcePath, commit+":"+ref)
	return g.check(res, err, "fetch")
}

func (g *gitRepo) objectFormat() (string, error) {
	return g.runner.ObjectFormat()
}

func (g *gitRepo) readFileAt(rev, projectPath string) ([]byte, error) {
	return g.runner.ReadRaw(gitx.Opts{}, "show", rev+":"+projectPath)
}
