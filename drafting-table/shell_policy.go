package guard

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type cliForm struct {
	operation    string
	write        bool
	changeSet    string
	branchPrefix string
	pathValues   []string
	stdinInput   bool
}

func decideNoProjectTool(input hookInput, state projectState) decision {
	switch {
	case isFileWriter(input.ToolName):
		return refuse("rule 5", "file-writing tools are unavailable to the drafting-table role")
	case isReadOrSearchTool(input.ToolName):
		return decideRead(input.ToolName, input, state)
	case input.ToolName == openCodeVocabulary.skillTool:
		name, ok := stringField(input.ToolInput, "name")
		if state.manifestErr != nil || !ok || !slices.Contains(state.manifest.ToolkitSkills, name) {
			return refuse("rule 5", "only skills listed in toolkit_skills may be loaded")
		}
		return allow()
	case input.ToolName == openCodeVocabulary.shellTool:
		command, ok := stringField(input.ToolInput, "command")
		if !ok {
			return refuse("rule 6", "bash requires a command string")
		}
		parsed, err := parseSimpleShell(command)
		if err != nil {
			return shellValueError(err)
		}
		if len(parsed.words) == 2 && parsed.words[0] == "ears-manager" && parsed.words[1] == "--version" && !parsed.heredoc {
			return allow()
		}
		if isHelpForm(parsed.words) && !parsed.heredoc {
			return allow()
		}
		if len(parsed.words) == 3 && parsed.words[0] == "date" && parsed.words[1] == "-u" && parsed.words[2] == "+%Y-%m-%dT%H:%M:%SZ" && !parsed.heredoc {
			return allow()
		}
		if parsed.words[0] == "ears-manager" && len(parsed.words) >= 3 && parsed.words[1] == "--output" && parsed.words[2] == "json" {
			form, err := parseCLIForm(parsed.words)
			if err == nil && form.operation == "project init" && form.write && !state.projectFound && !parsed.heredoc {
				if err := validateInitBranch(state.root, form); err != nil {
					return refuse("rule 6", err.Error())
				}
				if err := validateReservedPaths(state, state.root, form.pathValues); err != nil {
					return refuse("rule 6", err.Error())
				}
				return allow()
			}
		}
		return refuse("rule 6", "before project initialization, only project init, ears-manager help/version, and the UTC clock are allowed")
	case slices.Contains(openCodeVocabulary.roleTools, input.ToolName):
		return allow()
	case slices.Contains(openCodeVocabulary.blockedTools, input.ToolName):
		return refuse("rule 5", "subagents, web fetch, and web search are outside the drafting-table role")
	default:
		if governed, listed := governedToolDecision(input.ToolName, state.manifest); governed {
			if state.manifestErr != nil {
				return refuse("rule 4", "the adapter manifest could not be read; verify .agents/drafting-table.yaml")
			}
			if listed {
				return allow()
			}
			return refuse("rule 4", "this tool is not listed for its governed MCP server")
		}
		return refuse("rule 5", "the tool is not listed in the OpenCode drafting-table vocabulary")
	}
}

func decideDraftingShell(input hookInput, state projectState) decision {
	commandText, ok := stringField(input.ToolInput, "command")
	if !ok {
		return refuse("rule 6", "bash requires a command string")
	}
	parsed, err := parseSimpleShell(commandText)
	if err != nil {
		return shellValueError(err)
	}
	words := parsed.words
	if len(words) == 0 {
		return refuse("rule 6", "the shell command is empty")
	}
	if state.projectFound && isInitializationBranchSwitch(state, words) {
		return refuse("rule 1", "Project already initialized")
	}
	if isHelpForm(words) && !parsed.heredoc {
		return allow()
	}
	if len(words) == 2 && words[0] == "ears-manager" && words[1] == "--version" && !parsed.heredoc {
		return allow()
	}
	if len(words) == 3 && words[0] == "date" && words[1] == "-u" && words[2] == "+%Y-%m-%dT%H:%M:%SZ" && !parsed.heredoc {
		return allow()
	}
	if words[0] == "ears-manager" {
		form, err := parseCLIForm(words)
		if err != nil {
			if strings.Contains(err.Error(), "must be -") {
				return refuse("rule 6", "--content-file and --impact-file must be -; file content must come from quoted standard input")
			}
			return refuse("rule 6", "not an allowed ears-manager command form")
		}
		if form.operation == "project init" {
			return refuse("rule 1", "Project already initialized; project init is only allowed before .protobot/ exists")
		}
		if form.stdinInput != parsed.heredoc {
			return refuse("rule 6", "artifact content and impact input must use one quoted here-document")
		}
		if err := validateReservedPaths(state, state.root, form.pathValues); err != nil {
			return refuse("rule 6", err.Error())
		}
		if form.write {
			if !validateChangeSetForm(state, form) {
				return refuse("rule 6", "the write must target the change set of the current branch")
			}
		}
		return allow()
	}
	if words[0] == "register-approved-change-set" {
		form, err := parseRegistration(words)
		if err != nil || parsed.heredoc {
			return refuse("rule 6", "registration must use register-approved-change-set --change-set CS-<nnnnn>")
		}
		if state.config.Repository.ReviewMode != "single-player" {
			return refuse("rule 6", "registration is only available in single-player mode")
		}
		if !currentChangeSetMatches(state, form.changeSet) {
			return refuse("rule 6", "the registration change set is not the change set of the current branch")
		}
		return allow()
	}
	return refuse("rule 6", "not an allowed shell operation; Git and Git host operations use scm tools")
}

func isHelpForm(words []string) bool {
	if len(words) == 2 && words[0] == "ears-manager" && words[1] == "--help" {
		return true
	}
	if len(words) >= 3 && words[0] == "ears-manager" && words[len(words)-1] == "--help" {
		for _, word := range words[1 : len(words)-1] {
			if strings.HasPrefix(word, "-") {
				return false
			}
		}
		return true
	}
	return false
}

func decideOtherShell(input hookInput, state projectState) decision {
	command, ok := stringField(input.ToolInput, "command")
	if !ok || strings.TrimSpace(command) == "" {
		return refuse("rule 4", "command must be a non-empty string to check for governed writes")
	}
	if len(command) > maxShellCommandBytes {
		return refuse("rule 4", "command exceeds the 1 MiB inspection limit")
	}
	switch invokesGovernedWrite(command) {
	case governedExecutionExplicitWrite:
		return refuse("rule 4", "ears-manager writes are reserved for the drafting-table role")
	case governedExecutionOpaque:
		return refuse("rule 4", "command class is refused because execution cannot be inspected for governed writes")
	}
	if shellWritesProtectedPath(command, state, input.CWD, 0) {
		return refuse("rule 7", "shell writer targets protected control paths or has an ambiguous relative target after a working-directory change")
	}
	targets, changesDirectory, unsafe := inspectOtherShell(command, 0)
	if unsafe {
		return refuse("rule 7", "output redirection target could not be inspected safely")
	}
	if len(targets) > 0 && changesDirectory {
		return refuse("rule 7", "output redirection target is ambiguous after a working-directory change")
	}
	for _, target := range targets {
		if isLexicalControlPath(state.root, input.CWD, target) || isProtectedPathValue(state, input.CWD, target) {
			return refuse("rule 7", "output redirection targets a protected control path, registered artifact, or configured store")
		}
		resolved, err := resolveProjectPath(state.root, input.CWD, target)
		if err != nil {
			return refuse("rule 7", "output redirection targets could not be resolved safely")
		}
		if guardedWritePath(state, resolved) {
			return refuse("rule 7", "output redirection targets a protected control path, registered artifact, or configured store")
		}
	}
	return allow()
}

func parseRegistration(words []string) (cliForm, error) {
	if len(words) != 3 || words[1] != "--change-set" || !validChangeSetID(words[2]) {
		return cliForm{}, errors.New("invalid registration arguments")
	}
	return cliForm{operation: "register-approved-change-set", changeSet: words[2]}, nil
}

var changeSetIDPattern = regexp.MustCompile(`^CS-[0-9]{5}$`)

func validChangeSetID(value string) bool { return changeSetIDPattern.MatchString(value) }

func currentChangeSetMatches(state projectState, requested string) bool {
	if state.root == "" || !validChangeSetID(requested) || !state.changeSets[requested] {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", state.root, "rev-parse", "--abbrev-ref", "HEAD")
	command.Env = safeCommandEnv()
	output, err := command.Output()
	if err != nil {
		return false
	}
	branch := strings.TrimSpace(string(output))
	if branch == "" || branch == "HEAD" {
		return false
	}
	prefix := state.config.Repository.BranchPrefix
	if !strings.HasPrefix(branch, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(branch, prefix)
	if len(remainder) < 7 || remainder[5] != '-' {
		return false
	}
	for _, char := range remainder[:5] {
		if char < '0' || char > '9' {
			return false
		}
	}
	if !validBranchSlug(remainder[6:]) {
		return false
	}
	return requested == "CS-"+remainder[:5]
}

func validBranchSlug(slug string) bool {
	if slug == "" || slug[0] == '-' || slug[len(slug)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, char := range slug {
		hyphen := char == '-'
		if !hyphen && !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') {
			return false
		}
		if hyphen && previousHyphen {
			return false
		}
		previousHyphen = hyphen
	}
	return true
}

func validateInitBranch(root string, form cliForm) error {
	if root == "" {
		return errors.New("project init requires a Git working tree")
	}
	prefix := form.branchPrefix
	if prefix == "" {
		prefix = "cs/"
	}
	branch, err := currentBranch(root)
	if err != nil || branch != prefix+"00001-project-init" {
		return errors.New("project init requires the initialization branch <prefix>00001-project-init")
	}
	return nil
}

func currentBranch(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD")
	command.Env = safeCommandEnv()
	output, err := command.Output()
	if err != nil {
		return "", errors.New("current branch could not be read")
	}
	return strings.TrimSpace(string(output)), nil
}

func validateReservedPaths(state projectState, cwd string, values []string) error {
	for _, value := range values {
		if filepath.IsAbs(value) {
			return errors.New("artifact destinations must be relative to the project root")
		}
		resolved, err := resolveProjectPath(state.root, cwd, value)
		if err != nil || !within(state.root, resolved) {
			return errors.New("artifact destination cannot escape the project root")
		}
		if reservedBindingPath(state.root, cwd, value) {
			return errors.New("adapter, binding, and instruction paths cannot be registered as artifacts")
		}
	}
	return nil
}

func isInitializationBranchSwitch(state projectState, words []string) bool {
	return len(words) == 5 && words[0] == "git" && words[1] == "switch" && words[2] == "-c" &&
		words[3] == state.config.Repository.BranchPrefix+"00001-project-init" &&
		words[4] == state.config.Repository.DefaultBranch
}

// parseCLIForm accepts only the option vocabulary in the ears-manager CLI
// contract. Values remain data; no value is interpreted as shell syntax.
func parseCLIForm(words []string) (cliForm, error) {
	if len(words) < 4 || words[0] != "ears-manager" || words[1] != "--output" || words[2] != "json" {
		return cliForm{}, errors.New("missing JSON output mode")
	}
	operation, args, ok := commandName(words[3:])
	if !ok {
		return cliForm{}, errors.New("unknown command")
	}
	spec, ok := commandSpecs[operation]
	if !ok {
		return cliForm{}, errors.New("unknown command")
	}
	parsed, err := parseOptionList(args, spec)
	if err != nil {
		return cliForm{}, err
	}
	for _, required := range spec.required {
		if parsed.values[required] == 0 {
			return cliForm{}, errors.New("required option missing")
		}
	}
	for _, exclusive := range spec.exclusive {
		if parsed.values[exclusive[0]]+parsed.values[exclusive[1]] != 1 {
			return cliForm{}, errors.New("exactly one input option is required")
		}
	}
	form := cliForm{operation: operation, write: spec.write}
	form.changeSet = parsed.first("change-set")
	form.branchPrefix = parsed.first("branch-prefix")
	form.stdinInput = parsed.has("content-stdin") || (parsed.first("content-file") == "-" && parsed.has("content-file")) || (parsed.first("impact-file") == "-" && parsed.has("impact-file"))
	for _, key := range []string{"path", "vision", "architecture"} {
		if value := parsed.first(key); value != "" {
			form.pathValues = append(form.pathValues, value)
		}
	}
	if parsed.has("content-file") && parsed.first("content-file") != "-" {
		return cliForm{}, errors.New("--content-file must be -")
	}
	if parsed.has("impact-file") && parsed.first("impact-file") != "-" {
		return cliForm{}, errors.New("--impact-file must be -")
	}
	return form, nil
}

type optionSpec struct {
	allowed   []string
	required  []string
	repeated  map[string]bool
	exclusive [][2]string
	write     bool
}

type optionValues struct {
	values map[string]int
	items  map[string][]string
}

func (values optionValues) first(key string) string {
	if len(values.items[key]) == 0 {
		return ""
	}
	return values.items[key][0]
}

func (values optionValues) has(key string) bool { return values.values[key] > 0 }

func parseOptionList(args []string, spec optionSpec) (optionValues, error) {
	result := optionValues{values: make(map[string]int), items: make(map[string][]string)}
	ranks := make(map[string]int, len(spec.allowed))
	for i, name := range spec.allowed {
		ranks[name] = i
	}
	lastRank := -1
	for index := 0; index < len(args); {
		name := strings.TrimPrefix(args[index], "--")
		rank, allowed := ranks[name]
		if !strings.HasPrefix(args[index], "--") || !allowed || strings.Contains(args[index], "=") || rank < lastRank {
			return optionValues{}, errors.New("unknown or out-of-order option")
		}
		lastRank = rank
		result.values[name]++
		if result.values[name] > 1 && !spec.repeated[name] {
			return optionValues{}, errors.New("option repeated")
		}
		index++
		if name == "content-stdin" {
			result.items[name] = append(result.items[name], "")
			continue
		}
		if index >= len(args) || strings.HasPrefix(args[index], "--") {
			return optionValues{}, errors.New("option value missing")
		}
		result.items[name] = append(result.items[name], args[index])
		index++
	}
	return result, nil
}

func commandName(words []string) (string, []string, bool) {
	if len(words) == 0 {
		return "", nil, false
	}
	if len(words) >= 2 {
		pair := words[0] + " " + words[1]
		if _, ok := commandSpecs[pair]; ok {
			return pair, words[2:], true
		}
	}
	if _, ok := commandSpecs[words[0]]; ok {
		return words[0], words[1:], true
	}
	return "", nil, false
}

var commandSpecs = map[string]optionSpec{
	"project init":       {allowed: []string{"id", "name", "canonical-remote", "review-mode", "default-branch", "branch-prefix", "vision", "architecture"}, required: []string{"id", "name", "canonical-remote", "review-mode"}, write: true},
	"artifact put":       {allowed: []string{"change-set", "id", "kind", "path", "owner", "validator", "content-file", "content-stdin"}, required: []string{"change-set", "id", "kind", "path", "owner"}, exclusive: [][2]string{{"content-file", "content-stdin"}}, write: true},
	"artifact get":       {allowed: []string{"id", "kind", "at"}, exclusive: [][2]string{{"id", "kind"}}},
	"artifact list":      {allowed: []string{"kind", "owner", "at"}},
	"interface add":      {allowed: []string{"change-set", "id", "name", "type", "created", "spec-approach", "description"}, required: []string{"change-set", "id", "name", "type", "created"}, write: true},
	"interface list":     {allowed: []string{"type", "status", "at"}},
	"interface show":     {allowed: []string{"id", "at"}, required: []string{"id"}},
	"interface update":   {allowed: []string{"change-set", "id", "name", "type", "spec-approach", "description", "status"}, required: []string{"change-set", "id"}, write: true},
	"requirement add":    {allowed: []string{"change-set", "id", "type", "text", "interface", "scope", "verification-mode", "verification-rationale", "provenance", "created", "relationship"}, required: []string{"change-set", "id", "type", "text", "verification-mode", "provenance", "created"}, repeated: map[string]bool{"interface": true, "scope": true, "relationship": true}, write: true},
	"requirement list":   {allowed: []string{"interface", "scope", "type", "status", "relationship", "at"}, repeated: map[string]bool{"interface": true, "scope": true, "relationship": true}},
	"requirement show":   {allowed: []string{"id", "at"}, required: []string{"id"}},
	"requirement update": {allowed: []string{"change-set", "id", "type", "text", "interface", "scope", "verification-mode", "verification-rationale", "provenance", "relationship", "status"}, required: []string{"change-set", "id"}, repeated: map[string]bool{"interface": true, "scope": true, "relationship": true}, write: true},
	"requirement retire": {allowed: []string{"change-set", "id"}, required: []string{"change-set", "id"}, write: true},
	"change-set create":  {allowed: []string{"intent", "affected-interface", "affected-scope", "implementation-required", "implementation-rationale", "created"}, required: []string{"intent", "implementation-required", "created"}, repeated: map[string]bool{"affected-interface": true, "affected-scope": true}, write: true},
	"change-set list":    {allowed: []string{"status", "interface", "scope", "at"}},
	"change-set show":    {allowed: []string{"change-set", "at"}, required: []string{"change-set"}},
	"change-set update":  {allowed: []string{"change-set", "intent", "affected-interface", "affected-scope", "base-commit", "implementation-required", "implementation-rationale", "impact-file"}, required: []string{"change-set"}, repeated: map[string]bool{"affected-interface": true, "affected-scope": true}, write: true},
	"change-set compare": {allowed: []string{"change-set", "against"}, required: []string{"change-set"}},
	"check":              {allowed: []string{"at", "change-set"}},
	"impact":             {allowed: []string{"change-set", "at"}, required: []string{"change-set"}},
}

func (form cliForm) requiredChangeSet() bool {
	return form.write && form.operation != "change-set create" && form.operation != "project init"
}

func validateChangeSetForm(state projectState, form cliForm) bool {
	if !form.requiredChangeSet() {
		return form.operation == "change-set create" || form.operation == "project init"
	}
	return currentChangeSetMatches(state, form.changeSet)
}
