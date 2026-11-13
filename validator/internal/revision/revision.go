// Package revision reads specification records from a Git revision of a
// checkout. It does not fetch, push, or otherwise use the network.
package revision

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/gitcmd"
	"github.com/redhat-et/protobot/validator/internal/layout"
)

// Diagnostic codes for comparison-base reads.
const (
	CodeNotFound   = "revision.not_found"
	CodeReadFailed = "revision.read_failed"
)

type treeEntry struct {
	mode   string
	object string
	path   string
}

const (
	treeModeRegular    = "100644"
	treeModeExecutable = "100755"
)

// Read loads requirement records from the Git tree of ref as resolved in
// the checkout at target. The working tree is not read. Missing
// spec/requirements in that revision is treated as empty.
func Read(target, ref string) (layout.Snapshot, []layout.Diagnostic) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return layout.Snapshot{}, []layout.Diagnostic{diagnostic(CodeReadFailed, "", fmt.Sprintf("cannot resolve checkout path: %s", err))}
	}
	if err := requireCheckoutRoot(absolute); err != nil {
		return layout.Snapshot{}, []layout.Diagnostic{diagnostic(CodeReadFailed, "", err.Error())}
	}

	commit, err := resolveCommit(absolute, ref)
	if err != nil {
		return layout.Snapshot{}, []layout.Diagnostic{diagnostic(CodeNotFound, "", fmt.Sprintf("comparison base %q is not a commit in the checkout", ref))}
	}

	entries, err := listRequirements(absolute, commit)
	if err != nil {
		return layout.Snapshot{}, []layout.Diagnostic{diagnostic(CodeReadFailed, "", fmt.Sprintf("cannot read comparison base %q", ref))}
	}

	tmp, err := os.MkdirTemp("", "validator-base-")
	if err != nil {
		return layout.Snapshot{}, []layout.Diagnostic{diagnostic(CodeReadFailed, "", "cannot create a private copy of the comparison base")}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := materialize(absolute, tmp, entries); err != nil {
		return layout.Snapshot{}, []layout.Diagnostic{diagnostic(CodeReadFailed, "", fmt.Sprintf("cannot read comparison base %q", ref))}
	}
	return layout.Read(tmp)
}

func requireCheckoutRoot(target string) error {
	inside, err := gitOutput(target, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		return fmt.Errorf("target is not a git checkout")
	}
	toplevel, err := gitOutput(target, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("target is not a git checkout")
	}
	root := strings.TrimSpace(string(toplevel))
	if !samePath(target, root) {
		return fmt.Errorf("target is not the root of a git checkout")
	}
	return nil
}

func resolveCommit(target, ref string) (string, error) {
	out, err := gitOutput(target, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("not a commit")
	}
	return commit, nil
}

func listRequirements(target, commit string) ([]treeEntry, error) {
	out, err := gitOutput(target, "ls-tree", "-r", "--full-tree", "-z", commit, "--", layout.RequirementsDir)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		entry, err := parseLsTree(rec)
		if err != nil {
			return nil, err
		}
		if !safeRepoPath(entry.path) {
			return nil, fmt.Errorf("unsafe path %q", entry.path)
		}
		if path.Dir(entry.path) != layout.RequirementsDir {
			continue
		}
		if path.Ext(entry.path) != ".yaml" {
			continue
		}
		switch entry.mode {
		case treeModeRegular, treeModeExecutable:
		default:
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseLsTree(rec []byte) (treeEntry, error) {
	meta, repoPath, ok := bytes.Cut(rec, []byte{'\t'})
	if !ok {
		return treeEntry{}, fmt.Errorf("malformed ls-tree entry")
	}
	fields := bytes.Fields(meta)
	if len(fields) != 3 {
		return treeEntry{}, fmt.Errorf("malformed ls-tree entry")
	}
	return treeEntry{
		mode:   string(fields[0]),
		object: string(fields[2]),
		path:   string(repoPath),
	}, nil
}

func materialize(repo, dest string, entries []treeEntry) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	for _, entry := range entries {
		osPath := filepath.FromSlash(entry.path)
		if err := root.MkdirAll(filepath.Dir(osPath), 0o700); err != nil {
			return err
		}
		data, err := gitOutput(repo, "cat-file", "blob", entry.object)
		if err != nil {
			return err
		}
		if err := root.WriteFile(osPath, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func safeRepoPath(p string) bool {
	if p == "" || path.IsAbs(p) {
		return false
	}
	if path.Clean(p) != p {
		return false
	}
	if p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return p == layout.RequirementsDir || strings.HasPrefix(p, layout.RequirementsDir+"/")
}

func samePath(a, b string) bool {
	as, errA := os.Stat(a)
	bs, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return os.SameFile(as, bs)
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := gitcmd.Command(append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd.Output()
}

func diagnostic(code, path, message string) layout.Diagnostic {
	return layout.Diagnostic{Code: code, Path: path, Message: message}
}
