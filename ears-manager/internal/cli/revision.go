package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

// A read at `--at FULL-SHA` takes the project from the tree of that commit,
// not from the working tree. The loader and the validator read a directory on
// disk, so the read copies the commit's project configuration, projection
// manifest, record stores, and registered artifacts into a private temporary
// directory and loads the snapshot from there. Git runs in the working-tree root, and nothing in the
// working tree is read or written.

const (
	revisionConfigPath = ".protobot/project.yaml"

	treeModeRegular    = "100644"
	treeModeExecutable = "100755"
	treeModeSymlink    = "120000"
	treeModeSubmodule  = "160000"
	treeModeDirectory  = "040000"
)

type treeEntry struct {
	mode   string
	object string
	path   string
}

// parseRevisionOption accepts only a full 40-character hexadecimal commit
// hash, so a short hash, a branch name, or a tag never selects a read
// revision. The hash is returned in lowercase.
func parseRevisionOption(value string) (string, *commandFailure) {
	value = strings.TrimSpace(value)
	if !fullCommitID(value) {
		return "", validationFailure("revision.invalid", "Option --at must be a full 40-character hexadecimal commit hash.", nil)
	}
	return strings.ToLower(value), nil
}

// requireRevisionCommit refuses a parsed --at hash that names no commit in
// the local repository. commitExists refuses an annotated tag too, although
// Git would peel it to the commit it tags.
func requireRevisionCommit(root, commit string) *commandFailure {
	if !commitExists(root, commit) {
		return validationFailure("revision.not_found", fmt.Sprintf("Commit %s is not present in the local repository.", commit), nil)
	}
	return nil
}

// loadRevisionState loads the project snapshot from the tree of commit. The
// caller must call the returned cleanup when it no longer needs the snapshot.
// A commit whose tree has no .protobot/project.yaml fails with
// project.not_initialized.
func loadRevisionState(root, commit string) (projectState, func(), *commandFailure) {
	copyRoot, err := os.MkdirTemp("", "ears-manager-at-")
	if err != nil {
		return projectState{}, nil, ioFailure("revision.read_failed", "A private copy of the commit could not be created.")
	}
	cleanup := func() { _ = os.RemoveAll(copyRoot) }
	initialized, failure := copyRevision(root, commit, copyRoot)
	if failure != nil {
		cleanup()
		return projectState{}, nil, failure
	}
	if !initialized {
		cleanup()
		return projectState{}, nil, projectFailure("project.not_initialized", fmt.Sprintf("Commit %s has no .protobot/project.yaml.", commit))
	}
	snapshot, err := specvalidation.Load(copyRoot)
	if err == nil {
		snapshot.Context = specvalidation.ValidationContext{}
		return projectState{root: root, snapshot: snapshot, head: commit}, cleanup, nil
	}
	result := specvalidation.ValidateProjectWithContext(copyRoot, specvalidation.ValidationContext{})
	cleanup()
	if !result.Valid || len(result.Diagnostics) > 0 {
		return projectState{}, nil, failureFromValidation(result, false)
	}
	return projectState{}, nil, projectFailure("project.load_failed", "The project specification could not be loaded.")
}

// copyRevision copies the project configuration and the projection manifest
// of commit into target, then the record stores and the registered artifacts that the configuration
// names. Every entry keeps its tree mode, so the loader refuses a symbolic
// link, a directory, or a submodule at a commit as it does in the working
// tree. It reports false when the commit has no project configuration.
func copyRevision(root, commit, target string) (bool, *commandFailure) {
	listed, failure := listRevisionTree(root, commit, []string{revisionConfigPath, specvalidation.ProjectionPath})
	if failure != nil {
		return false, failure
	}
	configEntries := exactTreeEntries(listed, []string{revisionConfigPath})
	if len(configEntries) == 0 {
		return false, nil
	}
	// The projection manifest of the commit is copied with the configuration,
	// so projection checks read the classification of that commit, not an
	// absent manifest.
	controlEntries := append(append([]treeEntry(nil), configEntries...), exactTreeEntries(listed, []string{specvalidation.ProjectionPath})...)
	contents, failure := readRevisionBlobs(root, controlEntries)
	if failure != nil {
		return false, failure
	}
	entries := controlEntries
	if config := configEntries[0]; config.mode == treeModeRegular || config.mode == treeModeExecutable {
		stores, artifacts := revisionDataPaths(contents[config.object])
		listed, failure = listRevisionTree(root, commit, append(append([]string(nil), stores...), artifacts...))
		if failure != nil {
			return false, failure
		}
		dataEntries := append(storeTreeEntries(listed, stores), exactTreeEntries(listed, artifacts)...)
		dataContents, failure := readRevisionBlobs(root, dataEntries)
		if failure != nil {
			return false, failure
		}
		for object, data := range dataContents {
			contents[object] = data
		}
		entries = append(entries, dataEntries...)
	}
	if err := writeRevisionEntries(target, entries, contents); err != nil {
		return false, ioFailure("revision.read_failed", fmt.Sprintf("The tree of commit %s could not be copied.", commit))
	}
	return true, nil
}

// revisionDataPaths returns the store directories and the artifact paths that
// a project configuration names. A path that is not a slash-separated
// project-relative path is left out; the loader and the validator report it.
func revisionDataPaths(data []byte) ([]string, []string) {
	var config records.ProjectConfig
	if _, err := storage.DecodeFields(data, &config); err != nil {
		return nil, nil
	}
	stores := config.Stores.WithDefaults()
	var storePaths []string
	for _, path := range []string{stores.Requirements, stores.Interfaces, stores.ChangeSets} {
		if canonical, err := specvalidation.CanonicalProjectPath(path); err == nil {
			storePaths = append(storePaths, canonical)
		}
	}
	var artifactPaths []string
	for _, artifact := range config.Artifacts {
		if canonical, err := specvalidation.CanonicalProjectPath(artifact.Path); err == nil {
			artifactPaths = append(artifactPaths, canonical)
		}
	}
	return storePaths, artifactPaths
}

// readCommitFile returns the blob of a regular or executable file at path in
// commit's tree. A missing path returns present=false. A symlink, directory,
// or submodule is treated as absent so callers compare regular-file content.
func readCommitFile(root, commit, relative string) ([]byte, bool, *commandFailure) {
	path := filepath.ToSlash(relative)
	listed, failure := listRevisionTree(root, commit, []string{path})
	if failure != nil {
		return nil, false, failure
	}
	entries := exactTreeEntries(listed, []string{path})
	if len(entries) == 0 {
		return nil, false, nil
	}
	entry := entries[0]
	if entry.mode != treeModeRegular && entry.mode != treeModeExecutable {
		return nil, false, nil
	}
	contents, failure := readRevisionBlobs(root, []treeEntry{entry})
	if failure != nil {
		return nil, false, failure
	}
	data, ok := contents[entry.object]
	return data, ok, nil
}

// listRevisionTree lists the tree entries of commit at and below paths. Paths
// are literal, and a path that the tree does not hold lists nothing.
func listRevisionTree(root, commit string, paths []string) ([]treeEntry, *commandFailure) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := append([]string{"-C", root, "--literal-pathspecs", "ls-tree", "-r", "-t", "-z", "--full-tree", commit, "--"}, paths...)
	output, err := gitCommand(args...).Output()
	if err != nil {
		return nil, ioFailure("revision.read_failed", fmt.Sprintf("The tree of commit %s could not be read.", commit))
	}
	var entries []treeEntry
	for _, record := range strings.Split(string(output), "\x00") {
		if record == "" {
			continue
		}
		header, path, found := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !found || len(fields) != 3 || path == "" {
			return nil, ioFailure("revision.read_failed", fmt.Sprintf("The tree of commit %s could not be read.", commit))
		}
		entries = append(entries, treeEntry{mode: fields[0], object: fields[2], path: path})
	}
	return entries, nil
}

// exactTreeEntries keeps the entries whose path is one of paths.
func exactTreeEntries(entries []treeEntry, paths []string) []treeEntry {
	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		wanted[path] = true
	}
	var result []treeEntry
	for _, entry := range entries {
		if wanted[entry.path] {
			result = append(result, entry)
		}
	}
	return result
}

// storeTreeEntries keeps the entries at or below one of the store paths.
func storeTreeEntries(entries []treeEntry, stores []string) []treeEntry {
	var result []treeEntry
	for _, entry := range entries {
		for _, store := range stores {
			if entry.path == store || strings.HasPrefix(entry.path, store+"/") {
				result = append(result, entry)
				break
			}
		}
	}
	return result
}

// readRevisionBlobs reads the content of every file and symbolic-link entry
// with one `git cat-file --batch` run, keyed by object ID.
func readRevisionBlobs(root string, entries []treeEntry) (map[string][]byte, *commandFailure) {
	contents := map[string][]byte{}
	var request bytes.Buffer
	for _, entry := range entries {
		if entry.mode == treeModeRegular || entry.mode == treeModeExecutable || entry.mode == treeModeSymlink {
			request.WriteString(entry.object + "\n")
		}
	}
	if request.Len() == 0 {
		return contents, nil
	}
	command := gitCommand("-C", root, "cat-file", "--batch")
	command.Stdin = &request
	output, err := command.Output()
	if err != nil {
		return nil, ioFailure("revision.read_failed", "The commit's project files could not be read.")
	}
	reader := bufio.NewReader(bytes.NewReader(output))
	for {
		header, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && header == "" {
			return contents, nil
		}
		fields := strings.Fields(header)
		if err != nil || len(fields) != 3 || fields[1] != "blob" {
			return nil, ioFailure("revision.read_failed", "The commit's project files could not be read.")
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 {
			return nil, ioFailure("revision.read_failed", "The commit's project files could not be read.")
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(reader, data); err != nil || data[size] != '\n' {
			return nil, ioFailure("revision.read_failed", "The commit's project files could not be read.")
		}
		contents[fields[0]] = data[:size]
	}
}

// writeRevisionEntries recreates the entries under target. A file becomes a
// regular file, a symbolic link stays a symbolic link that nothing follows,
// and a directory or a submodule becomes an empty directory.
func writeRevisionEntries(target string, entries []treeEntry, contents map[string][]byte) error {
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	written := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if written[entry.path] {
			continue
		}
		written[entry.path] = true
		name := filepath.FromSlash(entry.path)
		if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		switch entry.mode {
		case treeModeRegular, treeModeExecutable:
			err = root.WriteFile(name, contents[entry.object], 0o600)
		case treeModeSymlink:
			err = root.Symlink(string(contents[entry.object]), name)
		case treeModeDirectory, treeModeSubmodule:
			err = root.MkdirAll(name, 0o700)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// commitOnDefaultBranch reports whether commit is on the default branch: a
// resolvable default-branch ref points at commit or at a descendant of it.
// The refs are the ones defaultBranchRefs lists, and all of them count, so a
// local branch that is behind its remote-tracking branch does not hide a
// merge. isAncestor ignores replace refs, as the change-set ancestry check
// does. One ref that proves ancestry is enough, so a failure on another ref,
// such as the shallow-clone failure, counts only when no ref proves it.
func commitOnDefaultBranch(root string, repository records.RepositoryConfig, commit string) (bool, *commandFailure) {
	refs, failure := defaultBranchRefs(root, repository)
	if failure != nil {
		return false, failure
	}
	resolved := false
	var firstFailure *commandFailure
	for _, ref := range refs {
		output, err := gitCommand("-C", root, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
		if err != nil {
			continue
		}
		resolved = true
		onBranch, failure := isAncestor(root, commit, strings.TrimSpace(string(output)))
		if onBranch {
			return true, nil
		}
		if failure != nil && firstFailure == nil {
			firstFailure = failure
		}
	}
	if firstFailure != nil {
		return false, firstFailure
	}
	if !resolved {
		return false, projectFailure("project.default_branch_unresolved", fmt.Sprintf("Repository default branch %q could not be resolved.", strings.TrimSpace(repository.DefaultBranch)))
	}
	return false, nil
}
