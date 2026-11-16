// Package revision reads specification records from a Git revision of a
// checkout. It does not fetch, push, or otherwise use the network.
package revision

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/gitcmd"
	"github.com/redhat-et/protobot/validator/internal/layout"
)

// Diagnostic codes for comparison-base reads.
const (
	CodeNotFound         = "revision.not_found"
	CodeReadFailed       = "revision.read_failed"
	CodeUnsupportedEntry = "revision.unsupported_entry"
	CodeUnexpectedFile   = "revision.unexpected_file"
	CodeParseFailed      = "revision.parse_failed"
)

// Error represents a revision-level failure preventing reading the base tree.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

type treeEntry struct {
	mode   string
	object string
	path   string
	size   int64
}

const (
	treeModeRegular    = "100644"
	treeModeExecutable = "100755"
	treeModeTree       = "040000"
)

// Read loads requirement records from the Git tree of ref as resolved in
// the checkout at target. The working tree is not read. Missing
// spec/requirements in that revision is treated as empty.
//
// Revision-level failures (such as missing ref or unreadable checkout)
// are returned as error (with type *Error). Per-file parse problems
// within the materialized base tree are returned as layout diagnostics.
func Read(target, ref string) (layout.Snapshot, []layout.Diagnostic, error) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return layout.Snapshot{}, nil, &Error{Code: CodeReadFailed, Message: fmt.Sprintf("cannot resolve checkout path: %s", err)}
	}
	if err := requireCheckoutRoot(absolute); err != nil {
		return layout.Snapshot{}, nil, &Error{Code: CodeReadFailed, Message: err.Error()}
	}

	commit, err := resolveCommit(absolute, ref)
	if err != nil {
		return layout.Snapshot{}, nil, &Error{Code: CodeNotFound, Message: fmt.Sprintf("comparison base %q is not a commit in the checkout", ref)}
	}

	exists, diags := checkTreeContainer(absolute, commit, ref, "spec")
	if len(diags) > 0 {
		return layout.Snapshot{}, diags, nil
	}
	if !exists {
		return layout.Snapshot{Root: absolute}, nil, nil
	}

	exists, diags = checkTreeContainer(absolute, commit, ref, layout.RequirementsDir)
	if len(diags) > 0 {
		return layout.Snapshot{}, diags, nil
	}
	if !exists {
		return layout.Snapshot{Root: absolute}, nil, nil
	}

	entries, listDiags, err := listRequirements(absolute, commit, ref)
	if err != nil {
		return layout.Snapshot{}, nil, &Error{Code: CodeReadFailed, Message: fmt.Sprintf("cannot read comparison base %q", ref)}
	}

	tmp, err := os.MkdirTemp("", "validator-base-")
	if err != nil {
		return layout.Snapshot{}, nil, &Error{Code: CodeReadFailed, Message: "cannot create a private copy of the comparison base"}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := materialize(absolute, tmp, entries); err != nil {
		return layout.Snapshot{}, nil, &Error{Code: CodeReadFailed, Message: fmt.Sprintf("cannot read comparison base %q", ref)}
	}
	snapshot, layoutDiags := layout.Read(tmp)
	snapshot.Root = absolute

	var parseDiagnostics []layout.Diagnostic
	parseDiagnostics = append(parseDiagnostics, listDiags...)
	for _, d := range layoutDiags {
		code := d.Code
		if strings.HasPrefix(code, "layout.") {
			code = "revision." + strings.TrimPrefix(code, "layout.")
		} else if !strings.HasPrefix(code, "revision.") {
			code = "revision." + code
		}
		parseDiagnostics = append(parseDiagnostics, layout.Diagnostic{
			Code:    code,
			Path:    d.Path,
			Message: fmt.Sprintf("comparison base %q: %s", ref, d.Message),
		})
	}
	return snapshot, parseDiagnostics, nil
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

func checkTreeContainer(target, commit, ref, repoPath string) (bool, []layout.Diagnostic) {
	out, err := gitOutput(target, "ls-tree", "-z", commit, repoPath)
	if err != nil {
		return false, []layout.Diagnostic{{Code: CodeReadFailed, Message: fmt.Sprintf("cannot read comparison base %q: %s", ref, err)}}
	}
	trimmed := bytes.TrimRight(out, "\x00")
	if len(trimmed) == 0 {
		return false, nil
	}
	entry, err := parseLsTree(trimmed)
	if err != nil {
		return false, []layout.Diagnostic{{Code: CodeReadFailed, Message: fmt.Sprintf("cannot read comparison base %q: %s", ref, err)}}
	}
	if entry.mode != treeModeTree {
		return false, []layout.Diagnostic{{Code: CodeUnsupportedEntry, Path: entry.path, Message: fmt.Sprintf("comparison base %q: unsupported entry mode %s", ref, entry.mode)}}
	}
	return true, nil
}

func listRequirements(target, commit, ref string) ([]treeEntry, []layout.Diagnostic, error) {
	out, err := gitOutput(target, "ls-tree", "-r", "--full-tree", "-z", "-l", commit, "--", layout.RequirementsDir)
	if err != nil {
		return nil, nil, err
	}
	var entries []treeEntry
	var diags []layout.Diagnostic
	seenDirs := make(map[string]struct{})
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		entry, err := parseLsTree(rec)
		if err != nil {
			return nil, nil, err
		}
		if !safeRepoPath(entry.path) {
			return nil, []layout.Diagnostic{{Code: CodeReadFailed, Message: fmt.Sprintf("cannot read comparison base %q: unsafe path %q", ref, entry.path)}}, nil
		}
		dir := path.Dir(entry.path)
		if dir != layout.RequirementsDir {
			rel := strings.TrimPrefix(dir, layout.RequirementsDir+"/")
			firstPart := strings.Split(rel, "/")[0]
			nestedDir := path.Join(layout.RequirementsDir, firstPart)
			if _, seen := seenDirs[nestedDir]; !seen {
				seenDirs[nestedDir] = struct{}{}
				diags = append(diags, layout.Diagnostic{
					Code:    CodeUnexpectedFile,
					Path:    nestedDir,
					Message: fmt.Sprintf("comparison base %q: is a directory", ref),
				})
			}
			continue
		}
		switch entry.mode {
		case treeModeRegular, treeModeExecutable:
		default:
			diags = append(diags, layout.Diagnostic{
				Code:    CodeUnsupportedEntry,
				Path:    entry.path,
				Message: fmt.Sprintf("comparison base %q: unsupported entry mode %s", ref, entry.mode),
			})
			continue
		}
		if path.Ext(entry.path) != ".yaml" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, diags, nil
}

func parseLsTree(rec []byte) (treeEntry, error) {
	meta, repoPath, ok := bytes.Cut(rec, []byte{'\t'})
	if !ok {
		return treeEntry{}, fmt.Errorf("malformed ls-tree entry")
	}
	fields := bytes.Fields(meta)
	if len(fields) < 3 {
		return treeEntry{}, fmt.Errorf("malformed ls-tree entry")
	}
	var size int64
	if len(fields) >= 4 && string(fields[3]) != "-" {
		var err error
		size, err = strconv.ParseInt(string(fields[3]), 10, 64)
		if err != nil {
			return treeEntry{}, fmt.Errorf("malformed ls-tree entry size: %w", err)
		}
	}
	return treeEntry{
		mode:   string(fields[0]),
		object: string(fields[2]),
		path:   string(repoPath),
		size:   size,
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
		if entry.size > layout.MaxYAMLBytes {
			f, err := root.Create(osPath)
			if err != nil {
				return err
			}
			if err := f.Truncate(layout.MaxYAMLBytes + 1); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			continue
		}
		data, err := catBlob(repo, entry.object)
		if err != nil {
			return err
		}
		if int64(len(data)) > layout.MaxYAMLBytes {
			f, err := root.Create(osPath)
			if err != nil {
				return err
			}
			if err := f.Truncate(layout.MaxYAMLBytes + 1); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			continue
		}
		if err := root.WriteFile(osPath, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func catBlob(repo, object string) ([]byte, error) {
	cmd := gitcmd.Command("-C", repo, "cat-file", "blob", object)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(stdout, layout.MaxYAMLBytes+1))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	if int64(len(data)) > layout.MaxYAMLBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return data, nil
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		return nil, fmt.Errorf("git cat-file failed: %s (%w)", stderr.String(), waitErr)
	}
	return data, nil
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
