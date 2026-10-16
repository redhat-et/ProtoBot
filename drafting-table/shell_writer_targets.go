package guard

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type writerOptionSpec struct {
	shortFlags             string
	shortValues            string
	shortTargets           string
	longFlags              []string
	longValues             []string
	longTargets            []string
	longOptionalValues     map[string][]string
	longOptionalValueLists map[string][]string
	nonEmptyLongValues     []string
	shortTargetDir         bool
	longTargetDir          bool
	trackedShortFlags      string
	trackedLongFlags       []string
}

type writerArgv struct {
	operands   []string
	targetDirs []string
	targets    []string
	tracked    map[string]bool
}

func protectedShellWriterTargets(args []string, state projectState, cwd string) ([]string, bool) {
	if len(args) == 0 {
		return nil, true
	}
	// shortcut: this inventory is not a general shell sandbox; add a writer
	// when review identifies a protected-path bypass.
	name := filepath.Base(args[0])
	if isInformationalCommand(args) && name != "ed" {
		return nil, true
	}
	return protectedShellWriterTargetsForCommand(name, args, state, cwd)
}

func protectedShellWriterTargetsForCommand(name string, args []string, state projectState, cwd string) ([]string, bool) {
	switch name {
	case "tee":
		return teeTargets(args[1:])
	case "mkdir":
		return mkdirTargets(args[1:])
	case "cp", "mv", "install", "ln":
		return copyLikeShellWriterTargets(name, args[1:], state, cwd)
	case "touch", "dd", "truncate", "rm", "rsync", "find":
		return fileShellWriterTargets(name, args[1:], state, cwd)
	case "sort", "uniq":
		return lineShellWriterTargets(name, args[1:])
	case "git":
		return gitConfigWriteTargets(args, state, cwd)
	case "sed":
		return sedInPlaceTargets(args[1:])
	case "patch", "ed":
		return nil, false
	default:
		return nil, true
	}
}

func copyLikeShellWriterTargets(name string, args []string, state projectState, cwd string) ([]string, bool) {
	switch name {
	case "cp":
		return cpTargets(args, state, cwd)
	case "mv":
		return copyLikeTargets(name, args, state, cwd, mvWriterOptions, true)
	case "install":
		return copyLikeTargets(name, args, state, cwd, installWriterOptions, false)
	case "ln":
		return lnTargets(args, state, cwd)
	default:
		return nil, false
	}
}

func fileShellWriterTargets(name string, args []string, state projectState, cwd string) ([]string, bool) {
	switch name {
	case "touch":
		return touchTargets(args)
	case "dd":
		return ddTargets(args)
	case "truncate":
		return truncateTargets(args)
	case "rm":
		return rmTargets(args, state, cwd)
	case "rsync":
		return rsyncTargets(args, state, cwd)
	case "find":
		return findFileTargets(args)
	default:
		return nil, false
	}
}

func lineShellWriterTargets(name string, args []string) ([]string, bool) {
	switch name {
	case "sort":
		return sortTargets(args)
	case "uniq":
		return uniqTargets(args)
	default:
		return nil, false
	}
}

func teeTargets(args []string) ([]string, bool) {
	var targets []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--":
			return append(targets, args[i+1:]...), true
		case "-a", "--append", "-i", "--ignore-interrupts":
			continue
		default:
			if strings.HasPrefix(arg, "-") && arg != "-" {
				return nil, false
			}
			targets = append(targets, arg)
		}
	}
	return targets, true
}

func mkdirTargets(args []string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, writerOptionSpec{
		shortFlags:         "pvZ",
		shortValues:        "m",
		longFlags:          []string{"--parents", "--verbose"},
		longValues:         []string{"--mode"},
		longOptionalValues: map[string][]string{"--context": nil},
	})
	return parsed.operands, safe && len(parsed.operands) > 0
}

var cpWriterOptions = writerOptionSpec{
	shortFlags:             "abdfHilLnpPRrsTuvxZ",
	shortValues:            "S",
	longFlags:              []string{"--archive", "--attributes-only", "--dereference", "--force", "--interactive", "--link", "--no-clobber", "--no-dereference", "--no-target-directory", "--parents", "--recursive", "--remove-destination", "--symbolic-link", "--verbose", "--one-file-system"},
	longValues:             []string{"--sparse", "--suffix"},
	longOptionalValues:     map[string][]string{"--backup": {"none", "off", "numbered", "t", "existing", "nil", "simple", "never"}, "--context": nil, "--preserve": nil, "--reflink": {"always", "auto", "never"}, "--update": {"all", "none", "none-fail", "older"}},
	longOptionalValueLists: map[string][]string{"--preserve": {"mode", "ownership", "timestamps", "links", "xattr", "context", "all"}},
	shortTargetDir:         true,
	longTargetDir:          true,
	trackedShortFlags:      "adHLPrRlsT",
	trackedLongFlags:       []string{"--archive", "--dereference", "--link", "--no-dereference", "--no-target-directory", "--parents", "--recursive", "--symbolic-link"},
}

var mvWriterOptions = writerOptionSpec{
	shortFlags:         "finuvbTZ",
	shortValues:        "S",
	longFlags:          []string{"--exchange", "--force", "--interactive", "--no-clobber", "--no-target-directory", "--verbose", "--whiteout"},
	longValues:         []string{"--suffix"},
	longOptionalValues: map[string][]string{"--backup": {"none", "off", "numbered", "t", "existing", "nil", "simple", "never"}, "--context": nil, "--update": {"all", "none", "none-fail", "older"}},
	shortTargetDir:     true,
	longTargetDir:      true,
}

var installWriterOptions = writerOptionSpec{
	shortFlags:         "bCDdpZsvT",
	shortValues:        "mogS",
	longFlags:          []string{"--compare", "--directory", "--preserve-context", "--preserve-timestamps", "--strip", "--verbose", "--no-target-directory"},
	longValues:         []string{"--group", "--mode", "--owner", "--suffix"},
	longOptionalValues: map[string][]string{"--backup": {"none", "off", "numbered", "t", "existing", "nil", "simple", "never"}, "--context": nil},
	shortTargetDir:     true,
	longTargetDir:      true,
	trackedShortFlags:  "d",
	trackedLongFlags:   []string{"--directory"},
}

var lnWriterOptions = writerOptionSpec{
	shortFlags:         "bdfFinPrsvTL",
	shortValues:        "S",
	longFlags:          []string{"--directory", "--force", "--interactive", "--logical", "--no-dereference", "--no-target-directory", "--physical", "--relative", "--symbolic", "--verbose"},
	longValues:         []string{"--suffix"},
	longOptionalValues: map[string][]string{"--backup": {"none", "off", "numbered", "t", "existing", "nil", "simple", "never"}},
	shortTargetDir:     true,
	longTargetDir:      true,
	trackedShortFlags:  "nsT",
	trackedLongFlags:   []string{"--no-dereference", "--no-target-directory", "--symbolic"},
}

func parseWriterArgv(args []string, spec writerOptionSpec) (writerArgv, bool) {
	parsed := writerArgv{tracked: make(map[string]bool)}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			parsed.operands = append(parsed.operands, args[i+1:]...)
			return parsed, true
		}
		if strings.HasPrefix(arg, "--") {
			next, safe := parseWriterLongOption(args, i, spec, &parsed)
			if !safe {
				return writerArgv{}, false
			}
			i = next - 1
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			next, safe := parseWriterShortOptions(args, i, spec, &parsed)
			if !safe {
				return writerArgv{}, false
			}
			i = next - 1
			continue
		}
		parsed.operands = append(parsed.operands, arg)
	}
	return parsed, true
}

func parseWriterLongOption(args []string, index int, spec writerOptionSpec, parsed *writerArgv) (int, bool) {
	option, value, hasValue := strings.Cut(args[index], "=")
	if next, safe, optional := parseOptionalWriterLongValue(index, option, value, hasValue, spec); optional {
		return next, safe
	}
	if spec.longTargetDir && option == "--target-directory" {
		return parseWriterLongTarget(args, index, value, hasValue, false, parsed)
	}
	if slices.Contains(spec.longTargets, option) {
		return parseWriterLongTarget(args, index, value, hasValue, true, parsed)
	}
	if slices.Contains(spec.longValues, option) {
		return parseWriterLongValue(args, index, option, value, hasValue, spec)
	}
	if slices.Contains(spec.longFlags, option) {
		return parseWriterLongFlag(index, option, hasValue, spec, parsed)
	}
	return index, false
}

func parseWriterLongTarget(args []string, index int, value string, hasValue, outputTarget bool, parsed *writerArgv) (int, bool) {
	value, next, safe := writerLongOptionValue(args, index, value, hasValue, outputTarget)
	if !safe {
		return index, false
	}
	if outputTarget {
		parsed.targets = append(parsed.targets, value)
	} else {
		parsed.targetDirs = append(parsed.targetDirs, value)
	}
	return next, true
}

func parseWriterLongValue(args []string, index int, option, value string, hasValue bool, spec writerOptionSpec) (int, bool) {
	if hasValue {
		return index + 1, !slices.Contains(spec.nonEmptyLongValues, option) || value != ""
	}
	if index+1 < len(args) {
		return index + 2, true
	}
	return index, false
}

func parseWriterLongFlag(index int, option string, hasValue bool, spec writerOptionSpec, parsed *writerArgv) (int, bool) {
	if hasValue {
		return index, false
	}
	if slices.Contains(spec.trackedLongFlags, option) {
		parsed.tracked[option] = true
	}
	return index + 1, true
}

func parseOptionalWriterLongValue(index int, option, value string, hasValue bool, spec writerOptionSpec) (int, bool, bool) {
	allowed, optional := spec.longOptionalValues[option]
	// Optional values are inline only; a nil allowlist accepts any non-empty value.
	safe := !hasValue || value != "" && (len(allowed) == 0 || slices.Contains(allowed, value))
	if hasValue && optional {
		if tokens, isList := spec.longOptionalValueLists[option]; isList {
			safe = validWriterLongValueList(value, tokens)
		}
	}
	return index + 1, safe, optional
}

func validWriterLongValueList(value string, allowed []string) bool {
	if value == "" {
		return false
	}
	for _, token := range strings.Split(value, ",") {
		if !slices.Contains(allowed, token) {
			return false
		}
	}
	return true
}

func writerLongOptionValue(args []string, index int, value string, hasValue, requireValue bool) (string, int, bool) {
	if hasValue {
		return value, index + 1, !requireValue || value != ""
	}
	if index+1 >= len(args) {
		return "", index, false
	}
	return args[index+1], index + 2, !requireValue || args[index+1] != ""
}

func parseWriterShortOptions(args []string, index int, spec writerOptionSpec, parsed *writerArgv) (int, bool) {
	flags := args[index][1:]
	for flagIndex, flag := range flags {
		if spec.shortTargetDir && flag == 't' {
			return parseWriterShortTarget(args, index, flagIndex, false, parsed)
		}
		if strings.ContainsRune(spec.shortTargets, flag) {
			return parseWriterShortTarget(args, index, flagIndex, true, parsed)
		}
		if strings.ContainsRune(spec.shortValues, flag) {
			return consumeWriterShortValue(args, index, flagIndex, len(flags))
		}
		if !strings.ContainsRune(spec.shortFlags, flag) {
			return index, false
		}
		if strings.ContainsRune(spec.trackedShortFlags, flag) {
			parsed.tracked[string(flag)] = true
		}
	}
	return index + 1, true
}

func parseWriterShortTarget(args []string, index, flagIndex int, outputTarget bool, parsed *writerArgv) (int, bool) {
	value, next, safe := writerShortOptionValue(args, index, flagIndex)
	if !safe {
		return index, false
	}
	if outputTarget {
		parsed.targets = append(parsed.targets, value)
	} else {
		parsed.targetDirs = append(parsed.targetDirs, value)
	}
	return next, true
}

func consumeWriterShortValue(args []string, index, flagIndex, flagCount int) (int, bool) {
	if flagIndex+1 == flagCount {
		if index+1 >= len(args) {
			return index, false
		}
		index++
	}
	return index + 1, true
}

func writerShortOptionValue(args []string, index, flagIndex int) (string, int, bool) {
	value := args[index][flagIndex+2:]
	if value != "" {
		return value, index + 1, true
	}
	if index+1 >= len(args) || args[index+1] == "" {
		return "", index, false
	}
	return args[index+1], index + 2, true
}

func copyLikeTargets(name string, args []string, state projectState, cwd string, spec writerOptionSpec, includeSources bool) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, spec)
	if !safe || !copyLikeHasEnoughOperands(name, parsed) {
		return nil, false
	}
	targets, safe := copyLikeParsedTargets(name, parsed, state, cwd, includeSources)
	if !safe {
		return nil, false
	}
	if name == "mv" {
		linkTargets, safe := mvPreservedSymlinkTargets(parsed, state, cwd)
		if !safe {
			return nil, false
		}
		targets = append(targets, linkTargets...)
	}
	return targets, true
}

func copyLikeHasEnoughOperands(name string, parsed writerArgv) bool {
	if name == "install" && (parsed.tracked["d"] || parsed.tracked["--directory"]) {
		return len(parsed.operands)+len(parsed.targetDirs) > 0
	}
	if len(parsed.targetDirs) > 0 {
		return len(parsed.operands) > 0
	}
	return len(parsed.operands) >= 2
}

func cpTargets(args []string, state projectState, cwd string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, cpWriterOptions)
	if !safe || parsed.tracked["--parents"] || !copyLikeHasEnoughOperands("cp", parsed) || cpDirectoryCopyTargetsProtected(parsed, state, cwd) {
		return nil, false
	}
	targets, safe := copyLikeParsedTargets("cp", parsed, state, cwd, false)
	if !safe {
		return nil, false
	}
	linkTargets, safe := cpOutputLinkTargets(parsed, state, cwd)
	if !safe {
		return nil, false
	}
	return append(targets, linkTargets...), true
}

func cpOutputLinkTargets(parsed writerArgv, state projectState, cwd string) ([]string, bool) {
	link := parsed.tracked["l"] || parsed.tracked["--link"]
	symbolic := parsed.tracked["s"] || parsed.tracked["--symbolic-link"]
	if link && symbolic {
		return nil, false
	}
	if !link && !symbolic {
		return cpPreservedSymlinkTargets(parsed, state, cwd)
	}
	return linkCreationTargets(
		parsed, state, cwd, symbolic, false,
		parsed.tracked["T"] || parsed.tracked["--no-target-directory"],
	)
}

func cpPreservedSymlinkTargets(parsed writerArgv, state projectState, cwd string) ([]string, bool) {
	preserveSource := cpPreservesTopLevelSourceLinks(parsed)
	preserveTree := cpPreservesTreeMemberLinks(parsed, preserveSource)
	return preservedSymlinkTargets(parsed, state, cwd, preserveSource, preserveTree, "cp")
}

func cpPreservesTopLevelSourceLinks(parsed writerArgv) bool {
	return cpUsesArchiveMode(parsed) || parsed.tracked["d"] || parsed.tracked["P"] || parsed.tracked["--no-dereference"] ||
		cpUsesRecursiveCopy(parsed) && !parsed.tracked["H"] && !cpDereferencesAll(parsed)
}

func cpPreservesTreeMemberLinks(parsed writerArgv, preserveSource bool) bool {
	// shortcut: conflicting link modes are treated as preserving; track option order if false positives become costly.
	return cpUsesRecursiveCopy(parsed) && (!cpDereferencesAll(parsed) || preserveSource || parsed.tracked["H"])
}

func cpUsesArchiveMode(parsed writerArgv) bool {
	return parsed.tracked["a"] || parsed.tracked["--archive"]
}

func cpUsesRecursiveCopy(parsed writerArgv) bool {
	return cpUsesArchiveMode(parsed) || parsed.tracked["r"] || parsed.tracked["R"] || parsed.tracked["--recursive"]
}

func cpDereferencesAll(parsed writerArgv) bool {
	return parsed.tracked["L"] || parsed.tracked["--dereference"]
}

func mvPreservedSymlinkTargets(parsed writerArgv, state projectState, cwd string) ([]string, bool) {
	return preservedSymlinkTargets(parsed, state, cwd, true, true, "mv")
}

func rsyncPreservedSymlinkTargets(parsed writerArgv, state projectState, cwd string) ([]string, bool) {
	archiveOrLinks := parsed.tracked["a"] || parsed.tracked["l"] ||
		parsed.tracked["--archive"] || parsed.tracked["--links"]
	recursive := parsed.tracked["a"] || parsed.tracked["r"] ||
		parsed.tracked["--archive"] || parsed.tracked["--recursive"]
	// shortcut: conflicting link modes are treated as preserving; track option order if false positives become costly.
	preserveTree := archiveOrLinks && recursive
	return preservedSymlinkTargets(parsed, state, cwd, archiveOrLinks, preserveTree, "rsync")
}

// shortcut: source-tree symlink inspection is capped at 10,000 entries; raise
// the bound if legitimate copy trees need broader coverage.
const maxSymlinkWalkEntries = 10_000

var errSymlinkWalkEntryLimit = errors.New("symlink walk entry limit exceeded")

func preservedSymlinkTargets(parsed writerArgv, state projectState, cwd string, preserveSource, preserveTree bool, writer string) ([]string, bool) {
	if !preserveSource && !preserveTree {
		return nil, true
	}
	sources, destinations := copyLikeSourcesAndDestinations(parsed)
	if len(sources) == 0 || len(destinations) == 0 {
		return nil, true
	}
	if len(destinations) != 1 || len(parsed.targetDirs) > 1 {
		return nil, false
	}
	destination := destinations[0]
	destinationIsDirectory, safe := copyLikeDestinationIsDirectory(parsed, state, cwd, destination)
	if !safe || len(sources) > 1 && !destinationIsDirectory {
		return nil, false
	}
	var targets []string
	for _, source := range sources {
		linkTargets, safe := preservedSourceSymlinkTargets(source, destination, destinationIsDirectory, state, cwd, preserveSource, preserveTree, writer)
		if !safe {
			return nil, false
		}
		targets = append(targets, linkTargets...)
	}
	return targets, true
}

func preservedSourceSymlinkTargets(source, destination string, destinationIsDirectory bool, state projectState, cwd string, preserveSource, preserveTree bool, writer string) ([]string, bool) {
	sourcePath, err := resolvePathWithoutFollowingFinalSymlink(state.root, cwd, source)
	if err != nil {
		return nil, false
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return preservedSymlinkOperandTargets(sourcePath, source, destination, destinationIsDirectory, state, cwd, preserveSource, preserveTree, writer)
	}
	if !info.IsDir() || !preserveTree {
		return nil, true
	}
	return walkMappedSourceTree(sourcePath, source, destination, destinationIsDirectory, state, cwd, writer)
}

func preservedSymlinkOperandTargets(sourcePath, source, destination string, destinationIsDirectory bool, state projectState, cwd string, preserveSource, preserveTree bool, writer string) ([]string, bool) {
	if preserveSource {
		destinationRoot, safe := mappedCopyDestinationRoot(source, destination, destinationIsDirectory, false, state, cwd, writer)
		if !safe {
			return nil, false
		}
		target, safe := reanchoredSourceSymlinkTarget(sourcePath, destinationRoot)
		if !safe {
			return nil, false
		}
		return []string{target}, true
	}
	if !preserveTree {
		return nil, true
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, false
	}
	if !info.IsDir() {
		return nil, true
	}
	sourceRoot, err := resolveProjectPath(state.root, cwd, source)
	if err != nil {
		return nil, false
	}
	return walkMappedSourceTree(sourceRoot, source, destination, destinationIsDirectory, state, cwd, writer)
}

func walkMappedSourceTree(sourceRoot, source, destination string, destinationIsDirectory bool, state projectState, cwd, writer string) ([]string, bool) {
	destinationRoot, safe := mappedCopyDestinationRoot(source, destination, destinationIsDirectory, true, state, cwd, writer)
	if !safe {
		return nil, false
	}
	return walkPreservedSymlinkTargets(sourceRoot, destinationRoot)
}

func walkPreservedSymlinkTargets(sourceRoot, destinationRoot string) ([]string, bool) {
	var targets []string
	entries := 0
	err := filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxSymlinkWalkEntries {
			return errSymlinkWalkEntryLimit
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		linkText, err := os.Readlink(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		linkDestination := destinationRoot
		if relative != "." {
			linkDestination = filepath.Join(destinationRoot, relative)
		}
		targets = append(targets, reanchoredSymlinkTarget(linkDestination, linkText))
		return nil
	})
	return targets, err == nil
}

func reanchoredSourceSymlinkTarget(source, destination string) (string, bool) {
	linkText, err := os.Readlink(source)
	if err != nil {
		return "", false
	}
	return reanchoredSymlinkTarget(destination, linkText), true
}

func reanchoredSymlinkTarget(destination, linkText string) string {
	if filepath.IsAbs(linkText) {
		return filepath.Clean(linkText)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(destination), linkText))
}

func mappedCopyDestinationRoot(source, destination string, destinationIsDirectory, sourceIsDirectory bool, state projectState, cwd, writer string) (string, bool) {
	resolvedDestination, err := resolveProjectPath(state.root, cwd, destination)
	if err != nil {
		return "", false
	}
	if copySourceMapsContents(writer, source, sourceIsDirectory) {
		return resolvedDestination, true
	}
	if writer == "rsync" && sourceIsDirectory {
		base := shellOperandBase(source)
		if base == "" {
			return "", false
		}
		return filepath.Join(resolvedDestination, base), true
	}
	if !destinationIsDirectory {
		return resolvedDestination, true
	}
	base := shellOperandBase(source)
	if base == "" {
		return "", false
	}
	return filepath.Join(resolvedDestination, base), true
}

func copySourceMapsContents(writer, source string, sourceIsDirectory bool) bool {
	if writer == "rsync" && sourceIsDirectory && hasTrailingSeparator(source) {
		return true
	}
	trimmed := strings.TrimRight(source, "/"+string(filepath.Separator))
	return trimmed == "." || strings.HasSuffix(trimmed, string(filepath.Separator)+".") || strings.HasSuffix(trimmed, "/.")
}

func copyLikeDestinationIsDirectory(parsed writerArgv, state projectState, cwd, destination string) (bool, bool) {
	noTargetDirectory := parsed.tracked["T"] || parsed.tracked["--no-target-directory"]
	if len(parsed.targetDirs) > 0 {
		if len(parsed.targetDirs) != 1 || noTargetDirectory {
			return false, false
		}
		return true, true
	}
	if noTargetDirectory {
		return false, !hasTrailingSeparator(destination)
	}
	return shellPathIsDirectory(state, cwd, destination)
}

func resolvePathWithoutFollowingFinalSymlink(root, cwd, value string) (string, error) {
	if hasTrailingSeparator(value) {
		return resolveProjectPath(root, cwd, value)
	}
	separator := string(filepath.Separator)
	index := strings.LastIndex(value, separator)
	parent, base := ".", value
	if index >= 0 {
		parent, base = value[:index], value[index+1:]
		if parent == "" {
			parent = separator
		}
	}
	if base == "" || base == "." || base == ".." {
		return resolveProjectPath(root, cwd, value)
	}
	resolvedParent, err := resolveProjectPath(root, cwd, parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, base), nil
}

func shellOperandBase(path string) string {
	base := filepath.Base(filepath.Clean(path))
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func hasTrailingSeparator(path string) bool {
	return strings.HasSuffix(path, string(filepath.Separator)) || filepath.Separator != '/' && strings.HasSuffix(path, "/")
}

func lnTargets(args []string, state projectState, cwd string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, lnWriterOptions)
	if !safe || len(parsed.operands) == 0 {
		return nil, false
	}
	symbolic := parsed.tracked["s"] || parsed.tracked["--symbolic"]
	if len(parsed.operands) == 1 && len(parsed.targetDirs) == 0 {
		name := shellOperandBase(parsed.operands[0])
		if name == "" {
			return nil, false
		}
		parsed.operands = append(parsed.operands, name)
		parsed.tracked["T"] = true
	}
	targets, safe := copyLikeParsedTargets("ln", parsed, state, cwd, false)
	if !safe {
		return nil, false
	}
	linkTargets, safe := linkCreationTargets(
		parsed,
		state,
		cwd,
		symbolic,
		parsed.tracked["n"] || parsed.tracked["--no-dereference"],
		parsed.tracked["T"] || parsed.tracked["--no-target-directory"],
	)
	if !safe {
		return nil, false
	}
	targets = append(targets, linkTargets...)
	return targets, true
}

func linkCreationTargets(parsed writerArgv, state projectState, cwd string, symbolic, noDereference, noTargetDirectory bool) ([]string, bool) {
	sources, linkParent, destinationIsDirectory, safe := linkSourcesAndDestinationParent(parsed, state, cwd, noDereference, noTargetDirectory)
	if !safe {
		return nil, false
	}
	var targets []string
	for _, source := range sources {
		if source == "" {
			return nil, false
		}
		paths := []string{source}
		if symbolic && !filepath.IsAbs(source) {
			paths = append(paths, filepath.Join(linkParent, source))
		}
		for _, path := range paths {
			if directorySourceContainsProtectedMetadata(state, cwd, path) {
				return nil, false
			}
			targets = append(targets, path)
		}
	}
	if destinationIsDirectory {
		for _, source := range sources {
			name := filepath.Base(source)
			if name == "." || name == ".." || name == string(filepath.Separator) {
				return nil, false
			}
			target := filepath.Join(linkParent, name)
			resolved, err := resolveProjectPath(state.root, cwd, target)
			if err != nil || directoryContainsProtectedState(state, resolved) {
				return nil, false
			}
			targets = append(targets, target)
		}
	}
	return targets, true
}

func linkSourcesAndDestinationParent(parsed writerArgv, state projectState, cwd string, noDereference, noTargetDirectory bool) ([]string, string, bool, bool) {
	if len(parsed.targetDirs) > 0 {
		if len(parsed.targetDirs) != 1 || len(parsed.operands) == 0 || noTargetDirectory {
			return nil, "", false, false
		}
		isDirectory, safe := shellPathIsDirectory(state, cwd, parsed.targetDirs[0])
		if !safe || !isDirectory {
			return nil, "", false, false
		}
		parent, err := resolveProjectPath(state.root, cwd, parsed.targetDirs[0])
		return parsed.operands, parent, true, err == nil
	}
	if len(parsed.operands) < 2 {
		return nil, "", false, false
	}
	sources := parsed.operands[:len(parsed.operands)-1]
	destination := parsed.operands[len(parsed.operands)-1]
	isDirectory, safe := shellLinkDestinationIsDirectory(state, cwd, destination, noTargetDirectory, noDereference)
	if !safe {
		return nil, "", false, false
	}
	if len(sources) > 1 && !isDirectory {
		return nil, "", false, false
	}
	parentPath := filepath.Dir(destination)
	if isDirectory {
		parentPath = destination
	}
	parent, err := resolveProjectPath(state.root, cwd, parentPath)
	return sources, parent, isDirectory, err == nil
}

func shellLinkDestinationIsDirectory(state projectState, cwd, path string, noTargetDirectory, noDereference bool) (bool, bool) {
	if noTargetDirectory {
		return false, true
	}
	if hasTrailingSeparator(path) {
		_, err := resolveProjectPath(state.root, cwd, path)
		return true, err == nil
	}
	parent, err := resolveProjectPath(state.root, cwd, filepath.Dir(path))
	if err != nil {
		return false, false
	}
	destination := filepath.Join(parent, filepath.Base(path))
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	if noDereference && info.Mode()&os.ModeSymlink != 0 {
		return false, true
	}
	info, err = os.Stat(destination)
	if os.IsNotExist(err) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	return info.IsDir(), true
}

func copyLikeParsedTargets(name string, parsed writerArgv, state projectState, cwd string, includeSources bool) ([]string, bool) {
	if name == "install" && (parsed.tracked["d"] || parsed.tracked["--directory"]) {
		return copyLikeDirectoryTargets(parsed), true
	}
	targets := copyLikeDestinationTargets(parsed, state, cwd)
	mappedTargets, safe := copyLikeMappedDirectoryTargets(parsed, state, cwd)
	if !safe {
		return nil, false
	}
	targets = append(targets, mappedTargets...)
	if includeSources {
		targets = append(targets, copyLikeSourceTargets(parsed, state, cwd)...)
	}
	return targets, true
}

func copyLikeDirectoryTargets(parsed writerArgv) []string {
	// install -d creates every operand as a directory.
	targets := append([]string(nil), parsed.operands...)
	return append(targets, parsed.targetDirs...)
}

func copyLikeDestinationTargets(parsed writerArgv, state projectState, cwd string) []string {
	var targets []string
	for _, targetDir := range parsed.targetDirs {
		targets = append(targets, targetDir)
		if manifest := adapterManifestTarget(state, cwd, targetDir); manifest != "" {
			targets = append(targets, manifest)
		}
	}
	if len(parsed.targetDirs) == 0 && len(parsed.operands) >= 2 {
		target := parsed.operands[len(parsed.operands)-1]
		targets = append(targets, target)
		if manifest := adapterManifestTarget(state, cwd, target); manifest != "" {
			targets = append(targets, manifest)
		}
	}
	return targets
}

func copyLikeMappedDirectoryTargets(parsed writerArgv, state projectState, cwd string) ([]string, bool) {
	sources, destinations := copyLikeSourcesAndDestinations(parsed)
	if len(sources) == 0 || len(destinations) == 0 {
		return nil, true
	}
	if len(destinations) != 1 || len(parsed.targetDirs) > 1 {
		return nil, false
	}
	destinationIsDirectory, safe := copyLikeDestinationIsDirectory(parsed, state, cwd, destinations[0])
	if !safe || !destinationIsDirectory {
		return nil, safe
	}
	resolvedDestination, err := resolveProjectPath(state.root, cwd, destinations[0])
	if err != nil {
		return nil, false
	}
	targets := make([]string, 0, len(sources))
	for _, source := range sources {
		base := shellOperandBase(source)
		if base == "" {
			targets = append(targets, resolvedDestination)
			continue
		}
		targets = append(targets, filepath.Join(resolvedDestination, base))
	}
	return targets, true
}

func copyLikeSourceTargets(parsed writerArgv, state projectState, cwd string) []string {
	sources, _ := copyLikeSourcesAndDestinations(parsed)
	var targets []string
	for _, source := range sources {
		if directorySourceContainsProtectedMetadata(state, cwd, source) {
			targets = append(targets, state.manifestPath, state.gitCommonDir)
		}
		targets = append(targets, source)
	}
	return targets
}

func cpDirectoryCopyTargetsProtected(parsed writerArgv, state projectState, cwd string) bool {
	sources, destinations := copyLikeSourcesAndDestinations(parsed)
	for _, source := range sources {
		if !copySourceMapsContents("cp", source, true) {
			continue
		}
		isDirectory, safe := shellPathIsDirectory(state, cwd, source)
		if !safe {
			return true
		}
		if !isDirectory {
			continue
		}
		for _, destination := range destinations {
			resolved, err := resolveProjectPath(state.root, cwd, destination)
			if err != nil || directoryContainsProtectedState(state, resolved) {
				return true
			}
		}
	}
	return false
}

func copyLikeSourcesAndDestinations(parsed writerArgv) ([]string, []string) {
	if len(parsed.targetDirs) > 0 {
		return parsed.operands, parsed.targetDirs
	}
	if len(parsed.operands) < 2 {
		return parsed.operands, nil
	}
	last := len(parsed.operands) - 1
	return parsed.operands[:last], []string{parsed.operands[last]}
}

func shellPathIsDirectory(state projectState, cwd, path string) (bool, bool) {
	resolved, err := resolveProjectPath(state.root, cwd, path)
	if err != nil {
		return false, false
	}
	if hasTrailingSeparator(path) {
		return true, true
	}
	info, err := os.Stat(resolved)
	if os.IsNotExist(err) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	return info.IsDir(), true
}

func adapterManifestTarget(state projectState, cwd, directory string) string {
	if state.root != "" {
		lexical, err := lexicalProjectPath(state.root, cwd, directory)
		if err == nil && filepath.Clean(lexical) == filepath.Join(state.root, ".agents") {
			return filepath.Join(state.root, ".agents", "drafting-table.yaml")
		}
	}
	resolved, err := resolveProjectPath(state.root, cwd, directory)
	if err != nil {
		return ""
	}
	if state.manifestPath != "" && filepath.Clean(resolved) == filepath.Clean(filepath.Dir(state.manifestPath)) {
		return state.manifestPath
	}
	projectAgents := filepath.Join(state.root, ".agents")
	if state.root != "" && filepath.Clean(resolved) == filepath.Clean(projectAgents) {
		return filepath.Join(projectAgents, "drafting-table.yaml")
	}
	return ""
}

func directorySourceContainsProtectedMetadata(state projectState, cwd, source string) bool {
	resolved, err := resolveProjectPath(state.root, cwd, source)
	if err != nil {
		return true
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return false
	}
	return directoryContainsProtectedState(state, resolved)
}

func directoryContainsProtectedState(state projectState, directory string) bool {
	if directory == "" {
		return true
	}
	// A directory-content copy can replace protected entries anywhere below its destination.
	protectedPaths := []string{state.gitDir, state.gitCommonDir, state.manifestPath}
	if state.root != "" {
		protectedPaths = append(protectedPaths,
			filepath.Join(state.root, ".git"),
			filepath.Join(state.root, ".protobot"),
			filepath.Join(state.root, ".agents", "drafting-table.yaml"),
			filepath.Join(state.root, ".agents", "skills"),
		)
	}
	if state.manifestPath != "" {
		protectedPaths = append(protectedPaths, filepath.Join(filepath.Dir(state.manifestPath), "skills"))
	}
	for _, protected := range protectedPaths {
		if withinOrEqual(directory, protected) {
			return true
		}
	}
	for _, store := range state.storePaths {
		resolved, err := resolveProjectPath(state.root, state.root, store)
		if err != nil || withinOrEqual(directory, resolved) {
			return true
		}
	}
	return false
}

func ddTargets(args []string) ([]string, bool) {
	var targets []string
	for _, arg := range args {
		if !strings.Contains(arg, "=") {
			if arg == "--help" || arg == "--version" {
				return nil, true
			}
			return nil, false
		}
		key, value, _ := strings.Cut(arg, "=")
		switch key {
		case "of":
			if value == "" {
				return nil, false
			}
			targets = append(targets, value)
		case "if", "bs", "ibs", "obs", "count", "skip", "seek", "status", "conv", "iflag", "oflag", "cbs", "fullblock", "count_bytes", "skip_bytes", "seek_bytes":
		default:
			return nil, false
		}
	}
	return targets, true
}

func touchTargets(args []string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, writerOptionSpec{
		shortFlags:  "acfmh",
		shortValues: "drt",
		longFlags:   []string{"--no-create", "--no-dereference"},
		longValues:  []string{"--date", "--reference", "--time"},
	})
	return parsed.operands, safe && len(parsed.operands) > 0
}

func truncateTargets(args []string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, writerOptionSpec{
		shortFlags:  "co",
		shortValues: "sr",
		longFlags:   []string{"--no-create", "--io-blocks"},
		longValues:  []string{"--reference", "--size"},
	})
	return parsed.operands, safe && len(parsed.operands) > 0
}

func rmTargets(args []string, state projectState, cwd string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, writerOptionSpec{
		shortFlags:        "dfiIRrv",
		longFlags:         []string{"--dir", "--force", "--interactive", "--no-preserve-root", "--one-file-system", "--preserve-root", "--recursive", "--verbose"},
		trackedShortFlags: "rR",
		trackedLongFlags:  []string{"--recursive"},
	})
	if !safe {
		return nil, false
	}
	if len(parsed.operands) == 0 {
		return nil, false
	}
	targets := append([]string(nil), parsed.operands...)
	if parsed.tracked["r"] || parsed.tracked["R"] || parsed.tracked["--recursive"] {
		for _, source := range parsed.operands {
			if directorySourceContainsProtectedMetadata(state, cwd, source) {
				targets = append(targets, state.manifestPath, state.gitCommonDir)
			}
		}
	}
	return targets, true
}

func rsyncTargets(args []string, state projectState, cwd string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, rsyncWriterOptions)
	if !safe || len(parsed.operands) < 2 || parsed.tracked["R"] || parsed.tracked["--relative"] {
		return nil, false
	}
	for _, path := range parsed.operands {
		if strings.Contains(path, ":") || strings.HasPrefix(path, "rsync://") {
			return nil, false
		}
	}
	destination := parsed.operands[len(parsed.operands)-1]
	targets := []string{destination}
	if adapterManifestTarget(state, cwd, destination) != "" || directorySourceContainsProtectedMetadata(state, cwd, destination) {
		targets = append(targets, adapterManifestTarget(state, cwd, destination), state.manifestPath, state.gitCommonDir)
	}
	linkTargets, safe := rsyncPreservedSymlinkTargets(parsed, state, cwd)
	if !safe {
		return nil, false
	}
	targets = append(targets, linkTargets...)
	return targets, true
}

var rsyncWriterOptions = writerOptionSpec{
	shortFlags:  "avzrlptgoDAXHhFnqviIcLPRS", // pragma: allowlist secret
	shortValues: "fM",
	longFlags:   []string{"--archive", "--backup", "--checksum", "--compress", "--copy-links", "--copy-unsafe-links", "--delete", "--delete-after", "--delete-before", "--delete-during", "--delete-excluded", "--delete-missing-args", "--dirs", "--executability", "--existing", "--group", "--hard-links", "--human-readable", "--ignore-existing", "--ignore-missing-args", "--ignore-errors", "--inplace", "--itemize-changes", "--links", "--mkpath", "--no-implied-dirs", "--omit-dir-times", "--owner", "--perms", "--recursive", "--relative", "--safe-links", "--size-only", "--specials", "--stats", "--times", "--update", "--verbose", "--whole-file"},
	// Omit unmodeled write paths, file-list mappings, remote commands, --link-dest hard-link staging, and --remove-source-files source deletion so the parser fails closed.
	longValues:        []string{"--chmod", "--compare-dest", "--copy-dest", "--exclude", "--exclude-from", "--filter", "--include", "--include-from", "--max-size", "--min-size", "--modify-window", "--out-format", "--password-file", "--suffix", "--timeout"},
	trackedShortFlags: "alrR",
	trackedLongFlags:  []string{"--archive", "--links", "--recursive", "--relative"},
}

func findFileTargets(args []string) ([]string, bool) {
	var targets []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-delete":
			return nil, false
		case "-fprint", "-fprint0", "-fls", "-fprintf":
			if i+1 >= len(args) {
				return nil, false
			}
			targets = append(targets, args[i+1])
			i++
			if args[i-1] == "-fprintf" {
				if i+1 >= len(args) {
					return nil, false
				}
				i++
			}
		}
	}
	return targets, true
}

func uniqTargets(args []string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, uniqWriterOptions)
	if !safe || len(parsed.operands) > 2 {
		return nil, false
	}
	if len(parsed.operands) < 2 {
		return nil, true
	}
	return []string{parsed.operands[1]}, true
}

var uniqWriterOptions = writerOptionSpec{
	shortFlags:  "cdiuzD",
	shortValues: "fsw",
	longFlags: []string{
		"--count", "--repeated", "--ignore-case", "--unique", "--zero-terminated",
	},
	longValues: []string{"--skip-fields", "--skip-chars", "--check-chars"},
	longOptionalValues: map[string][]string{
		"--all-repeated": {"none", "separate", "prepend"},
		"--group":        {"none", "prepend", "append", "separate", "both"},
	},
}

func sortTargets(args []string) ([]string, bool) {
	parsed, safe := parseWriterArgv(args, sortWriterOptions)
	return parsed.targets, safe
}

var sortWriterOptions = writerOptionSpec{
	shortFlags:         "bdfghiMmnRrsuVcCz",
	shortValues:        "kSt",
	shortTargets:       "oT",
	longFlags:          sortLongFlags,
	longValues:         sortLongValueOptions,
	longTargets:        []string{"--output", "--temporary-directory"},
	longOptionalValues: map[string][]string{"--check": {"diagnose-first", "quiet", "silent"}},
	nonEmptyLongValues: sortLongValueOptions,
}

var sortLongFlags = []string{
	"--ignore-leading-blanks", "--dictionary-order", "--ignore-case", "--general-numeric-sort",
	"--ignore-nonprinting", "--month-sort", "--human-numeric-sort", "--numeric-sort",
	"--random-sort", "--reverse", "--stable", "--unique", "--version-sort",
	"--zero-terminated", "--debug",
}

var sortLongValueOptions = []string{
	"--key", "--field-separator", "--buffer-size", "--parallel", "--files0-from", "--random-source", "--sort",
}

func sedInPlaceTargets(args []string) ([]string, bool) {
	inPlace, expressionOption := false, false
	var files []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		if arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") ||
			(strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "i")) {
			inPlace = true
			continue
		}
		if arg == "-e" || arg == "--expression" || arg == "-f" || arg == "--file" {
			expressionOption = true
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--expression=") || strings.HasPrefix(arg, "--file=") ||
			(strings.HasPrefix(arg, "-e") && len(arg) > 2) || (strings.HasPrefix(arg, "-f") && len(arg) > 2) {
			expressionOption = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		files = append(files, arg)
	}
	if !inPlace {
		return nil, true
	}
	if !expressionOption && len(files) > 0 {
		files = files[1:] // The first positional is the sed script.
	}
	return files, len(files) > 0
}
