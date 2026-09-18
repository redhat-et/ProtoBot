package specvalidation

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

const (
	relationshipDependsOn     = "depends-on"
	relationshipConflictsWith = "conflicts-with"
	relationshipSupersedes    = "supersedes"
	relationshipRelatedTo     = "related-to"
)

var (
	baseCommitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	timestampPattern  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	timestampLayout   = "2006-01-02T15:04:05Z"
	earsPatterns      = map[records.EARSStyle]*regexp.Regexp{
		records.EARSUbiquitous:       regexp.MustCompile(`(?is)^The .+ shall .+$`),
		records.EARSEventDriven:      regexp.MustCompile(`(?is)^When .+, the .+ shall .+$`),
		records.EARSStateDriven:      regexp.MustCompile(`(?is)^While .+, the .+ shall .+$`),
		records.EARSUnwantedBehavior: regexp.MustCompile(`(?is)^If .+, then the .+ shall .+$`),
		records.EARSOptionalFeature:  regexp.MustCompile(`(?is)^Where .+, the .+ shall .+$`),
	}
	complexShallPattern = regexp.MustCompile(`(?i)\bshall\b`)
	complexTriggers     = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bwhen\b`),
		regexp.MustCompile(`(?i)\bwhile\b`),
		regexp.MustCompile(`(?i)\bwhere\b`),
		regexp.MustCompile(`(?is)\bif\b.*\bthen\b`),
	}
	interfaceTypes = map[records.InterfaceType]bool{
		records.InterfaceNetworkService:  true,
		records.InterfaceCLI:             true,
		records.InterfaceREPL:            true,
		records.InterfaceLinkableLibrary: true,
		records.InterfaceWebGUI:          true,
		records.InterfaceNativeGUI:       true,
		records.InterfacePersistentState: true,
		records.InterfacePackageSource:   true,
	}
	earsTypes = map[records.EARSStyle]bool{
		records.EARSUbiquitous:       true,
		records.EARSEventDriven:      true,
		records.EARSStateDriven:      true,
		records.EARSUnwantedBehavior: true,
		records.EARSOptionalFeature:  true,
		records.EARSComplex:          true,
	}
	provenanceValues = map[records.Provenance]bool{
		records.ProvenanceUserAuthored:   true,
		records.ProvenanceAgentSuggested: true,
		records.ProvenanceKitImported:    true,
	}
	verificationModes = map[records.VerificationMode]bool{
		records.VerificationIsolatedInterface:   true,
		records.VerificationImplementationAware: true,
	}
	recordStatuses = map[records.RecordStatus]bool{
		records.StatusActive:  true,
		records.StatusRetired: true,
	}
	relationshipTypes = map[string]bool{
		relationshipDependsOn:     true,
		relationshipConflictsWith: true,
		relationshipSupersedes:    true,
		relationshipRelatedTo:     true,
	}
	changeSetActions = map[string]bool{
		"add":    true,
		"revise": true,
		"retire": true,
	}
	interfaceActions = map[string]bool{
		"add":    true,
		"revise": true,
	}
	artifactActions = map[string]bool{
		"add":    true,
		"revise": true,
	}
	impactDispositions = map[string]bool{
		"applicable":     true,
		"not-applicable": true,
	}
	impactOrigins = map[string]bool{
		"mechanical": true,
		"semantic":   true,
	}
	repositoryReviewModes = map[string]bool{
		"single-player": true,
		"multi-player":  true,
	}
)

const (
	defaultRepositoryBranch = "main"
	defaultBranchPrefix     = "cs/"
	reservedBranchPrefix    = "wi/"
)

// Validate checks the complete snapshot without reading or modifying the
// working tree. Callers that load YAML should provide the field sets returned
// by storage.DecodeFields so omitted required values remain observable.
func Validate(snapshot Snapshot) Result {
	result := Result{}
	config := records.CanonicalProjectConfig(snapshot.Config)
	projectPath := projectConfigPath(snapshot.ConfigPath)

	validateProject(&result, snapshot, config)

	interfaces := indexInterfaces(&result, snapshot.Interfaces)
	requirements := indexRequirements(&result, snapshot.Requirements)
	artifacts := indexArtifacts(&result, projectPath, config.Artifacts)

	validateInterfaces(&result, snapshot.Interfaces)
	validateRequirements(&result, snapshot.Requirements, interfaces)
	validateRelationships(&result, snapshot.Requirements, requirements)
	validateChangeSets(&result, snapshot.ChangeSets, snapshot.Requirements, requirements, interfaces, artifacts)

	result.finish()
	return result
}

func validateProject(result *Result, snapshot Snapshot, config records.ProjectConfig) {
	path := projectConfigPath(snapshot.ConfigPath)
	validateProjectMetadata(result, snapshot.ConfigFields, path, config)
	validateStorePaths(result, snapshot.Root, path, config.Stores)
	validateArtifacts(result, snapshot, path, config.Stores, config.Artifacts)
}

func validateProjectMetadata(result *Result, fields map[string]bool, path string, config records.ProjectConfig) {
	validateRequiredString(result, fields, path, "", "project.id", config.Project.ID, "project.missing_field")
	validateRequiredString(result, fields, path, "", "project.name", config.Project.Name, "project.missing_field")
	validateRepositoryConfig(result, fields, path, config.Repository)
	validateSchemaVersion(result, fields, path, "project", config.SchemaVersions.Project, records.CurrentProjectSchemaVersion)
	validateSchemaVersion(result, fields, path, "specification", config.SchemaVersions.Specification, records.CurrentSpecificationSchemaVersion)
}

func validateStorePaths(result *Result, root, path string, stores records.StorePaths) {
	paths := map[string]string{
		"change_sets":  stores.ChangeSets,
		"interfaces":   stores.Interfaces,
		"requirements": stores.Requirements,
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		validateStorePath(result, root, path, name, paths[name])
	}
}

func validateStorePath(result *Result, root, projectPath, name, value string) {
	if value == "" {
		return
	}
	field := "stores." + name
	canonical, err := canonicalStorePath(value)
	if err != nil {
		result.add(diagnostic("project.invalid_path", projectPath, "", field, "Store path must use slash-separated project-relative form.", "Use a relative path inside the project root."))
		return
	}
	if root == "" {
		return
	}
	resolved, err := storage.ValidatePathWithin(root, filepath.FromSlash(canonical))
	if err != nil {
		result.add(diagnostic("project.invalid_path", projectPath, "", field, "Store path cannot be resolved inside the project root.", "Use a relative path that resolves inside the project root."))
		return
	}
	if info, statErr := os.Stat(resolved); statErr == nil && !info.IsDir() {
		result.add(diagnostic("project.invalid_path", projectPath, "", field, "Store path is not a directory.", "Point the store at a directory."))
	}
}

func validateRepositoryConfig(result *Result, fields map[string]bool, path string, repository records.RepositoryConfig) {
	validateRequiredString(result, fields, path, "", "repository.canonical_remote", repository.CanonicalRemote, "project.missing_field")
	if strings.TrimSpace(repository.CanonicalRemote) != "" {
		validateCanonicalRemote(result, path, repository.CanonicalRemote)
	}
	validateRequiredString(result, fields, path, "", "repository.review_mode", repository.ReviewMode, "project.missing_field")
	if strings.TrimSpace(repository.ReviewMode) != "" && !repositoryReviewModes[repository.ReviewMode] {
		result.add(diagnostic("project.invalid_configuration", path, "", "repository.review_mode", "Repository review mode is unsupported.", "Use single-player or multi-player."))
	}

	defaultBranch := repository.DefaultBranch
	if defaultBranch == "" && !fieldPresent(fields, "repository.default_branch", false) {
		defaultBranch = defaultRepositoryBranch
	}
	if !validGitRefName(defaultBranch) {
		result.add(diagnostic("project.invalid_configuration", path, "", "repository.default_branch", "Repository default branch is not a valid Git branch name.", "Use a valid branch name such as main."))
	}

	branchPrefix := repository.BranchPrefix
	if branchPrefix == "" && !fieldPresent(fields, "repository.branch_prefix", false) {
		branchPrefix = defaultBranchPrefix
	}
	if !strings.HasSuffix(branchPrefix, "/") || !validGitRefName(strings.TrimSuffix(branchPrefix, "/")) || strings.HasPrefix(branchPrefix, reservedBranchPrefix) {
		result.add(diagnostic("project.invalid_configuration", path, "", "repository.branch_prefix", "Repository branch prefix is not allowed.", "Use a slash-terminated non-reserved branch prefix."))
	}
}

func validateCanonicalRemote(result *Result, path, remote string) {
	if strings.TrimSpace(remote) != remote || strings.ContainsAny(remote, " \t\r\n") {
		addInvalidRemoteDiagnostic(result, path, "project.invalid_configuration", "Canonical repository remote must be a credential-free supported remote.")
		return
	}
	if strings.Contains(remote, "://") {
		parsed, err := url.Parse(remote)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			addInvalidRemoteDiagnostic(result, path, "project.invalid_configuration", "Canonical repository remote must be a credential-free https:// or ssh:// URL.")
			return
		}
		if parsed.User != nil {
			addInvalidRemoteDiagnostic(result, path, "project.remote_credentials", "Canonical repository remote must not contain credentials.")
		}
		return
	}
	if validSCPRemote(remote) {
		return
	}
	if colon := strings.IndexByte(remote, ':'); colon >= 0 && strings.Contains(remote[:colon], "@") {
		addInvalidRemoteDiagnostic(result, path, "project.remote_credentials", "Canonical repository remote must not contain credentials.")
		return
	}
	addInvalidRemoteDiagnostic(result, path, "project.invalid_configuration", "Canonical repository remote must be a credential-free https://, ssh://, or git@host:path remote.")
}

func addInvalidRemoteDiagnostic(result *Result, path, code, message string) {
	result.add(diagnostic(code, path, "", "repository.canonical_remote", message, "Use a credential-free supported repository remote."))
}

func validSCPRemote(remote string) bool {
	colon := strings.IndexByte(remote, ':')
	if colon <= len("git@") || !strings.HasPrefix(remote, "git@") {
		return false
	}
	host := remote[len("git@"):colon]
	path := remote[colon+1:]
	return host != "" && path != "" && !strings.ContainsAny(host, "/\\@") && !strings.ContainsAny(path, "\x00\r\n")
}

func validGitRefName(name string) bool {
	if name == "" || name == "@" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") || strings.Contains(name, "..") || strings.Contains(name, "@{") {
		return false
	}
	for _, character := range name {
		if unicode.IsSpace(character) || character < 0x20 || character == 0x7f || strings.ContainsRune("~^:?*[\\", character) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func canonicalStorePath(path string) (string, error) {
	canonical, err := canonicalProjectPath(path)
	if err != nil {
		return "", err
	}
	if isReservedStorePath(canonical) {
		return "", fmt.Errorf("store path uses a reserved project path")
	}
	return canonical, nil
}

func validateSchemaVersion(result *Result, fields map[string]bool, path, store string, found, supported int) {
	field := "schema_versions." + store
	if fields != nil && !fields[field] {
		result.add(diagnostic("schema.unsupported_version", path, "", field, fmt.Sprintf("The %s schema version is missing; supported version is %d.", store, supported), "Set the store schema version to the supported version or run an explicit migration."))
		return
	}
	if found != supported {
		result.add(diagnostic("schema.unsupported_version", path, "", field, fmt.Sprintf("Unsupported %s schema version %d; supported version is %d.", store, found, supported), "Upgrade the validator or run a reviewed schema migration."))
	}
}

func indexRequirements(result *Result, documents []Document[records.Requirement]) map[string]records.Requirement {
	index := make(map[string]records.Requirement, len(documents))
	ordered := sortRequirementDocuments(documents)
	for _, document := range ordered {
		value := records.CanonicalRequirement(document.Value)
		if value.ID == "" || records.ValidateRequirementID(value.ID) != nil {
			continue
		}
		if _, exists := index[value.ID]; exists {
			result.add(diagnostic("requirement.duplicate_id", safePath(document.Path), value.ID, "id", fmt.Sprintf("Requirement ID %q is declared more than once.", value.ID), "Use a unique stable requirement ID."))
			continue
		}
		index[value.ID] = value
	}
	return index
}

func indexInterfaces(result *Result, documents []Document[records.InterfaceRecord]) map[string]records.InterfaceRecord {
	index := make(map[string]records.InterfaceRecord, len(documents))
	ordered := sortInterfaceDocuments(documents)
	for _, document := range ordered {
		value := records.CanonicalInterface(document.Value)
		if value.ID == "" || records.ValidateInterfaceID(value.ID) != nil {
			continue
		}
		if _, exists := index[value.ID]; exists {
			result.add(diagnostic("interface.duplicate_id", safePath(document.Path), value.ID, "id", fmt.Sprintf("Interface ID %q is declared more than once.", value.ID), "Use a unique stable interface ID."))
			continue
		}
		index[value.ID] = value
	}
	return index
}

func indexArtifacts(result *Result, projectPath string, artifacts []records.ArtifactEntry) map[string]records.ArtifactEntry {
	index := make(map[string]records.ArtifactEntry, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID == "" || records.ValidateArtifactID(artifact.ID) != nil {
			continue
		}
		if _, exists := index[artifact.ID]; exists {
			result.add(diagnostic("artifact.duplicate_id", projectPath, artifact.ID, "artifacts", fmt.Sprintf("Artifact ID %q is declared more than once.", artifact.ID), "Use a unique stable artifact ID."))
			continue
		}
		index[artifact.ID] = artifact
	}
	return index
}

func validateInterfaces(result *Result, documents []Document[records.InterfaceRecord]) {
	for _, document := range sortInterfaceDocuments(documents) {
		validateInterface(result, document)
	}
}

func validateInterface(result *Result, document Document[records.InterfaceRecord]) {
	value := records.CanonicalInterface(document.Value)
	path := safePath(document.Path)
	validateRecordPath(result, document.Path, records.InterfaceStore, value.ID)
	validateRequiredString(result, document.Fields, path, value.ID, "id", value.ID, "interface.missing_field")
	if value.ID != "" {
		if err := records.ValidateInterfaceID(value.ID); err != nil {
			result.add(diagnostic("interface.invalid_id", path, value.ID, "id", err.Error(), "Use lowercase kebab-case for interface IDs."))
		}
	}
	validateRequiredString(result, document.Fields, path, value.ID, "name", value.Name, "interface.missing_field")
	validateRequiredString(result, document.Fields, path, value.ID, "type", string(value.Type), "interface.missing_field")
	if value.Type == "" || !interfaceTypes[value.Type] {
		result.add(diagnostic("interface.invalid_type", path, value.ID, "type", fmt.Sprintf("Unsupported interface type %q.", value.Type), "Use one of the interface types defined by ADR-0002."))
	}
	validateCreated(result, path, interfaceKind, value.ID, "created", value.Created)
	if value.Status != "" && !recordStatuses[value.Status] {
		result.add(diagnostic("interface.invalid_status", path, value.ID, "status", fmt.Sprintf("Unsupported interface status %q.", value.Status), "Use active or retired."))
	}
}

func validateRequirements(result *Result, documents []Document[records.Requirement], interfaces map[string]records.InterfaceRecord) {
	for _, document := range sortRequirementDocuments(documents) {
		validateRequirement(result, document, interfaces)
	}
}

func validateRequirement(result *Result, document Document[records.Requirement], interfaces map[string]records.InterfaceRecord) {
	rawValue := document.Value
	value := records.CanonicalRequirement(rawValue)
	path := safePath(document.Path)
	validateRecordPath(result, document.Path, records.RequirementStore, value.ID)
	validateRequiredString(result, document.Fields, path, value.ID, "id", value.ID, "requirement.missing_field")
	if value.ID != "" {
		if err := records.ValidateRequirementID(value.ID); err != nil {
			result.add(diagnostic("requirement.invalid_id", path, value.ID, "id", err.Error(), "Use REQ-<SCOPE>-<NNNNN> with an uppercase scope and five-digit sequence."))
		}
	}
	validateRequiredString(result, document.Fields, path, value.ID, "type", string(value.Type), "requirement.missing_field")
	if value.Type == "" || !earsTypes[value.Type] {
		result.add(diagnostic("requirement.invalid_type", path, value.ID, "type", fmt.Sprintf("Unsupported EARS pattern %q.", value.Type), "Use one of the six EARS pattern types."))
	}
	validateRequiredString(result, document.Fields, path, value.ID, "text", value.Text, "requirement.missing_field")
	if !validEARS(value.Type, value.Text) {
		result.add(diagnostic("requirement.ears_pattern_mismatch", path, value.ID, "text", fmt.Sprintf("Text does not match the declared %s EARS pattern.", value.Type), earsHint(value.Type)))
	}
	validateApplicability(result, document, value, interfaces)
	validateVerification(result, document, rawValue)
	validateRequiredString(result, document.Fields, path, value.ID, "provenance", string(value.Provenance), "requirement.missing_field")
	if value.Provenance == "" || !provenanceValues[value.Provenance] {
		result.add(diagnostic("requirement.invalid_provenance", path, value.ID, "provenance", fmt.Sprintf("Unsupported provenance %q.", value.Provenance), "Use user-authored, agent-suggested, or kit-imported."))
	}
	validateCreated(result, path, requirementKind, value.ID, "created", value.Created)
	if value.Status != "" && !recordStatuses[value.Status] {
		result.add(diagnostic("requirement.invalid_status", path, value.ID, "status", fmt.Sprintf("Unsupported requirement status %q.", value.Status), "Use active or retired."))
	}
	validateRelationshipsInRecord(result, document, rawValue)
}

func validateApplicability(result *Result, document Document[records.Requirement], value records.Requirement, interfaces map[string]records.InterfaceRecord) {
	path := safePath(document.Path)
	if !fieldPresent(document.Fields, "applies_to", value.AppliesTo.Interfaces != nil || value.AppliesTo.Scopes != nil) {
		result.add(diagnostic("requirement.missing_field", path, value.ID, "applies_to", "Required field \"applies_to\" is missing.", "Provide at least one applicability selector."))
	}
	if len(value.AppliesTo.Interfaces) == 0 && len(value.AppliesTo.Scopes) == 0 {
		result.add(diagnostic("requirement.invalid_applicability", path, value.ID, "applies_to", "At least one interface or scope selector is required.", "Add a registered interface ID or a non-empty scope."))
	}
	for _, interfaceID := range value.AppliesTo.Interfaces {
		validateInterfaceSelector(result, path, value.ID, interfaceID, interfaces)
	}
	validateScopeSelectors(result, path, value.ID, value.AppliesTo.Scopes)
}

func validateInterfaceSelector(result *Result, path, recordID, interfaceID string, interfaces map[string]records.InterfaceRecord) {
	if interfaceID == "" {
		result.add(diagnostic("requirement.invalid_applicability", path, recordID, "applies_to.interfaces", "Applicability interface IDs must not be empty.", "Use a registered interface ID."))
		return
	}
	if err := records.ValidateInterfaceID(interfaceID); err != nil {
		result.add(diagnostic("requirement.invalid_applicability", path, recordID, "applies_to.interfaces", err.Error(), "Use a valid registered interface ID."))
		return
	}
	if _, exists := interfaces[interfaceID]; !exists {
		result.add(diagnostic("reference.not_found", path, recordID, "applies_to.interfaces", fmt.Sprintf("Interface %q is not registered.", interfaceID), "Register the interface or remove the selector."))
	}
}

func validateScopeSelectors(result *Result, path, recordID string, scopes []string) {
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == "" {
			result.add(diagnostic("requirement.invalid_applicability", path, recordID, "applies_to.scopes", "Applicability scopes must not be empty.", "Use a non-empty project-defined scope."))
			continue
		}
		if seen[scope] {
			result.add(diagnostic("requirement.duplicate_selector", path, recordID, "applies_to.scopes", fmt.Sprintf("Scope selector %q is repeated.", scope), "Keep each applicability selector once."))
		}
		seen[scope] = true
	}
}

func validateVerification(result *Result, document Document[records.Requirement], value records.Requirement) {
	path := safePath(document.Path)
	if !fieldPresent(document.Fields, "verification", value.Verification.Mode != "" || value.Verification.Rationale != "") {
		result.add(diagnostic("requirement.missing_field", path, value.ID, "verification", "Required field \"verification\" is missing.", "Provide verification metadata."))
	}
	if value.Verification.Mode == "" {
		value.Verification.Mode = records.VerificationIsolatedInterface
	}
	if !verificationModes[value.Verification.Mode] {
		result.add(diagnostic("requirement.invalid_verification", path, value.ID, "verification.mode", fmt.Sprintf("Unsupported verification mode %q.", value.Verification.Mode), "Use isolated-interface or implementation-aware."))
	}
	if value.Verification.Mode == records.VerificationImplementationAware && strings.TrimSpace(value.Verification.Rationale) == "" {
		result.add(diagnostic("requirement.missing_field", path, value.ID, "verification.rationale", "Implementation-aware verification requires a rationale.", "Explain why isolated-interface verification is insufficient."))
	}
}

func validateCreated(result *Result, path string, kind recordKind, recordID, field, value string) {
	if strings.TrimSpace(value) == "" {
		result.add(diagnostic(missingFieldCode(kind), path, recordID, field, fmt.Sprintf("Required field %q is missing.", field), fmt.Sprintf("Provide an ISO 8601 UTC timestamp in %s format.", timestampLayout)))
		return
	}
	if !timestampPattern.MatchString(value) {
		result.add(diagnostic("record.invalid_timestamp", path, recordID, field, fmt.Sprintf("Timestamp %q is not in %s format.", value, timestampLayout), "Use YYYY-MM-DDTHH:MM:SSZ."))
		return
	}
	if _, err := time.Parse(timestampLayout, value); err != nil {
		result.add(diagnostic("record.invalid_timestamp", path, recordID, field, fmt.Sprintf("Timestamp %q is not in %s format.", value, timestampLayout), "Use YYYY-MM-DDTHH:MM:SSZ."))
	}
}

func validateRequiredString(result *Result, fields map[string]bool, path, recordID, field, value, code string) {
	if !fieldPresent(fields, field, strings.TrimSpace(value) != "") || strings.TrimSpace(value) == "" {
		result.add(diagnostic(code, path, recordID, field, fmt.Sprintf("Required field %q is missing.", field), fmt.Sprintf("Provide %q.", field)))
	}
}

func fieldPresent(fields map[string]bool, field string, inferred bool) bool {
	if fields == nil {
		return inferred
	}
	return fields[field]
}

func validEARS(style records.EARSStyle, text string) bool {
	if style == records.EARSComplex {
		if !complexShallPattern.MatchString(text) {
			return false
		}
		count := 0
		for _, pattern := range complexTriggers {
			if pattern.MatchString(text) {
				count++
			}
		}
		return count >= 2
	}
	pattern, exists := earsPatterns[style]
	return exists && pattern.MatchString(text)
}

func earsHint(style records.EARSStyle) string {
	switch style {
	case records.EARSUbiquitous:
		return "Start the statement with 'The ... shall ...'."
	case records.EARSEventDriven:
		return "Start the statement with 'When ...'."
	case records.EARSStateDriven:
		return "Start the statement with 'While ...'."
	case records.EARSUnwantedBehavior:
		return "Use the 'If ..., then ... shall ...' form."
	case records.EARSOptionalFeature:
		return "Start the statement with 'Where ...'."
	case records.EARSComplex:
		return "Include 'shall' and at least two distinct EARS trigger forms."
	default:
		return "Declare a supported EARS pattern before validating the text."
	}
}

func validateRecordPath(result *Result, path string, kind records.StoreKind, id string) {
	if path == "" || id == "" {
		return
	}
	expected, err := records.FilenameFor(kind, id)
	if err != nil || filepath.Base(filepath.FromSlash(path)) == expected {
		return
	}
	result.add(diagnostic("record.filename_mismatch", safePath(path), id, "id", fmt.Sprintf("Record ID %q does not match filename %q.", id, filepath.Base(filepath.FromSlash(path))), fmt.Sprintf("Use filename %q.", expected)))
}

func safePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func projectConfigPath(path string) string {
	if path == "" {
		return filepath.ToSlash(filepath.Join(".protobot", "project.yaml"))
	}
	return safePath(path)
}

func sortRequirementDocuments(documents []Document[records.Requirement]) []Document[records.Requirement] {
	result := append([]Document[records.Requirement](nil), documents...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := safePath(result[i].Path), safePath(result[j].Path)
		if left == right {
			return result[i].Value.ID < result[j].Value.ID
		}
		return left < right
	})
	return result
}

func sortInterfaceDocuments(documents []Document[records.InterfaceRecord]) []Document[records.InterfaceRecord] {
	result := append([]Document[records.InterfaceRecord](nil), documents...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := safePath(result[i].Path), safePath(result[j].Path)
		if left == right {
			return result[i].Value.ID < result[j].Value.ID
		}
		return left < right
	})
	return result
}

func sortChangeSetDocuments(documents []Document[records.ChangeSet]) []Document[records.ChangeSet] {
	result := append([]Document[records.ChangeSet](nil), documents...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := safePath(result[i].Path), safePath(result[j].Path)
		if left == right {
			return result[i].Value.ID < result[j].Value.ID
		}
		return left < right
	})
	return result
}
