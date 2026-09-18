package specvalidation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/redhat-et/protobot/ears-manager/internal/records"
	"github.com/redhat-et/protobot/ears-manager/internal/storage"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var artifactKinds = map[records.ArtifactKind]bool{
	records.ArtifactVision:         true,
	records.ArtifactArchitecture:   true,
	records.ArtifactInterfaceIDL:   true,
	records.ArtifactInterfaceProse: true,
}

var artifactValidators = map[string]map[records.ArtifactKind]bool{
	"markdownlint": {
		records.ArtifactVision:         true,
		records.ArtifactArchitecture:   true,
		records.ArtifactInterfaceProse: true,
	},
	"openapi-lint": {
		records.ArtifactInterfaceIDL: true,
	},
	"protoc": {
		records.ArtifactInterfaceIDL: true,
	},
}

var artifactOwners = map[string]bool{
	"ears-manager": true,
	"user":         true,
}

// CanonicalTextDigest returns the v1 digest for a UTF-8 text artifact. It
// normalizes only CRLF and lone CR line endings so arbitrary repository
// checkout settings do not change the digest.
func CanonicalTextDigest(data []byte) (string, error) {
	canonical, err := canonicalText(data)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateArtifacts(result *Result, snapshot Snapshot, projectPath string, stores records.StorePaths, artifacts []records.ArtifactEntry) {
	ordered := append([]records.ArtifactEntry(nil), artifacts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ID == ordered[j].ID {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].ID < ordered[j].ID
	})
	seenPaths := make(map[string]string, len(ordered))
	for _, artifact := range ordered {
		validateArtifact(result, snapshot, projectPath, stores, artifact, seenPaths)
	}
}

func validateArtifact(result *Result, snapshot Snapshot, projectPath string, stores records.StorePaths, artifact records.ArtifactEntry, seenPaths map[string]string) {
	field := artifactField(artifact.ID, "")
	validateArtifactIdentity(result, artifact, projectPath, field)
	validateArtifactLocation(result, snapshot, stores, artifact, projectPath, field, seenPaths)
	validateArtifactDigest(result, artifact, projectPath, field)
	validateArtifactPolicy(result, artifact, projectPath, field)
}

func validateArtifactIdentity(result *Result, artifact records.ArtifactEntry, projectPath, field string) {
	if artifact.ID == "" {
		result.add(diagnostic("artifact.missing_field", projectPath, "", field+".id", "Required artifact field \"id\" is missing.", "Provide a unique lowercase-kebab artifact ID."))
	} else if err := records.ValidateArtifactID(artifact.ID); err != nil {
		result.add(diagnostic("artifact.invalid_id", projectPath, artifact.ID, field+".id", err.Error(), "Use a lowercase-kebab artifact ID."))
	}
	if artifact.Kind == "" {
		result.add(diagnostic("artifact.missing_field", projectPath, artifact.ID, field+".kind", "Required artifact field \"kind\" is missing.", "Declare an opaque specification artifact kind."))
	} else if !artifactKinds[artifact.Kind] {
		result.add(diagnostic("artifact.unknown_kind", projectPath, artifact.ID, field+".kind", fmt.Sprintf("Unsupported artifact kind %q.", artifact.Kind), "Use vision, architecture, interface-idl, or interface-prose."))
	}
}

func validateArtifactLocation(result *Result, snapshot Snapshot, stores records.StorePaths, artifact records.ArtifactEntry, projectPath, field string, seenPaths map[string]string) {
	if strings.TrimSpace(artifact.Path) == "" {
		result.add(diagnostic("artifact.missing_field", projectPath, artifact.ID, field+".path", "Required artifact field \"path\" is missing.", "Provide a relative path to a regular file."))
		return
	}
	validateArtifactPath(result, snapshot, stores, artifact, projectPath, field)
	if canonicalPath, pathErr := canonicalProjectPath(artifact.Path); pathErr == nil {
		key := strings.ToLower(canonicalPath)
		if previous, exists := seenPaths[key]; exists && previous != artifact.ID {
			result.add(diagnostic("artifact.duplicate_path", projectPath, artifact.ID, field+".path", fmt.Sprintf("Artifact path is already registered by %q.", previous), "Register each artifact path once."))
		}
		seenPaths[key] = artifact.ID
	}
}

func validateArtifactDigest(result *Result, artifact records.ArtifactEntry, projectPath, field string) {
	if strings.TrimSpace(artifact.Digest) == "" {
		result.add(diagnostic("artifact.missing_field", projectPath, artifact.ID, field+".digest", "Required artifact field \"digest\" is missing.", "Record a canonical sha256 digest."))
	} else if !digestPattern.MatchString(artifact.Digest) {
		result.add(diagnostic("artifact.invalid_digest", projectPath, artifact.ID, field+".digest", "Artifact digest must match sha256:<64 lowercase hexadecimal characters>.", "Compute the digest through ears-manager."))
	}
}

func validateArtifactPolicy(result *Result, artifact records.ArtifactEntry, projectPath, field string) {
	if strings.TrimSpace(artifact.Owner) == "" {
		result.add(diagnostic("artifact.missing_field", projectPath, artifact.ID, field+".owner", "Required artifact field \"owner\" is missing.", "Use user or ears-manager as the responsible authority."))
	} else if !artifactOwners[artifact.Owner] {
		result.add(diagnostic("artifact.invalid_owner", projectPath, artifact.ID, field+".owner", fmt.Sprintf("Unsupported artifact owner %q.", artifact.Owner), "Use user or ears-manager."))
	}
	if artifact.Validator == "" {
		return
	}
	allowedKinds, exists := artifactValidators[artifact.Validator]
	if !exists {
		result.add(diagnostic("artifact.validator_not_allowed", projectPath, artifact.ID, field+".validator", fmt.Sprintf("Validator %q is not in the built-in registry.", artifact.Validator), "Use a registered built-in validator or omit the validator."))
	} else if !allowedKinds[artifact.Kind] {
		result.add(diagnostic("artifact.validator_incompatible", projectPath, artifact.ID, field+".validator", fmt.Sprintf("Validator %q does not support artifact kind %q.", artifact.Validator, artifact.Kind), "Choose a validator compatible with the artifact format."))
	}
}

func validateArtifactPath(result *Result, snapshot Snapshot, stores records.StorePaths, artifact records.ArtifactEntry, projectPath, field string) {
	clean, pathErr := canonicalProjectPath(artifact.Path)
	if pathErr != nil || isReservedProjectPath(clean) || pathInStructuredStore(clean, stores) {
		result.add(diagnostic("artifact.invalid_path", projectPath, artifact.ID, field+".path", fmt.Sprintf("Artifact path %q is not an allowed project-relative path.", artifact.Path), "Use a regular file inside the project outside reserved control paths."))
		return
	}
	if snapshot.Root == "" {
		return
	}
	relativePath := filepath.FromSlash(clean)
	if _, err := storage.ValidatePathWithin(snapshot.Root, relativePath); err != nil {
		result.add(diagnostic("artifact.invalid_path", projectPath, artifact.ID, field+".path", "Artifact path cannot be resolved inside the project root.", "Use a path that resolves inside the project root."))
		return
	}
	rootHandle, err := os.OpenRoot(snapshot.Root)
	if err != nil {
		result.add(diagnostic("artifact.path_unreadable", projectPath, artifact.ID, field+".path", "Open project root.", "Make the project root readable."))
		return
	}
	defer func() { _ = rootHandle.Close() }()
	info, err := rootHandle.Lstat(relativePath)
	if err != nil {
		if os.IsNotExist(err) {
			result.add(diagnostic("artifact.path_not_found", projectPath, artifact.ID, field+".path", fmt.Sprintf("Registered artifact path %q does not exist.", artifact.Path), "Create the artifact through ears-manager before registering it."))
			return
		}
		result.add(diagnostic("artifact.path_unreadable", projectPath, artifact.ID, field+".path", "Inspect artifact path.", "Make the registered artifact readable."))
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		result.add(diagnostic("artifact.invalid_path", projectPath, artifact.ID, field+".path", "Registered artifact path must name a regular file, not a symlink or directory.", "Register a regular specification file."))
		return
	}
	data, err := rootHandle.ReadFile(relativePath)
	if err != nil {
		result.add(diagnostic("artifact.path_unreadable", projectPath, artifact.ID, field+".path", "Read registered artifact.", "Make the registered artifact readable."))
		return
	}
	actual, err := CanonicalTextDigest(data)
	if err != nil {
		result.add(diagnostic("artifact.invalid_content", projectPath, artifact.ID, field+".path", err.Error(), "Store valid UTF-8 text without a BOM."))
		return
	}
	if digestPattern.MatchString(artifact.Digest) && actual != artifact.Digest {
		result.add(diagnostic("artifact.digest_mismatch", projectPath, artifact.ID, field+".digest", fmt.Sprintf("Registered digest %q does not match the artifact content.", artifact.Digest), "Rewrite the artifact through ears-manager or update the registry in a reviewed change set."))
	}
}

func canonicalProjectPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if strings.ContainsAny(path, "\\\x00") || strings.HasPrefix(path, "/") || strings.Contains(path, ":") {
		return "", fmt.Errorf("path must use slash-separated project-relative form")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("path contains an invalid component")
		}
	}
	return strings.Join(parts, "/"), nil
}

func canonicalText(data []byte) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("artifact content is not valid UTF-8")
	}
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		return nil, fmt.Errorf("artifact content must not contain a UTF-8 BOM")
	}
	canonical := make([]byte, 0, len(data))
	for index := 0; index < len(data); index++ {
		if data[index] == '\r' {
			if index+1 < len(data) && data[index+1] == '\n' {
				index++
			}
			canonical = append(canonical, '\n')
			continue
		}
		canonical = append(canonical, data[index])
	}
	return canonical, nil
}

func artifactField(id, field string) string {
	if id == "" {
		return "artifacts[]" + fieldSuffix(field)
	}
	return "artifacts[id=" + id + "]" + fieldSuffix(field)
}

func fieldSuffix(field string) string {
	if field == "" {
		return ""
	}
	return "." + field
}

func isReservedProjectPath(path string) bool {
	return isReservedPath(path, false)
}

func isReservedStorePath(path string) bool {
	return isReservedPath(path, true)
}

func isReservedPath(path string, allowProtobotRoot bool) bool {
	parts := strings.Split(strings.ToLower(path), "/")
	for index, part := range parts {
		if part == ".protobot" {
			if !allowProtobotRoot || index != 0 || len(parts) == 1 {
				return true
			}
			continue
		}
		if reservedProjectDirectories[part] || isReservedProjectControlFile(parts, index) {
			return true
		}
	}
	return isReservedProjectFile(parts)
}

func isReservedProjectControlFile(parts []string, index int) bool {
	return index > 0 && parts[index-1] == ".protobot" && reservedProjectControlFiles[parts[index]]
}

func isReservedProjectFile(parts []string) bool {
	return reservedProjectFiles[strings.Join(parts, "/")] || reservedProjectFiles[parts[len(parts)-1]]
}

func pathInStructuredStore(path string, stores records.StorePaths) bool {
	path = strings.ToLower(path)
	for _, store := range []string{stores.Requirements, stores.Interfaces, stores.ChangeSets} {
		canonical, err := canonicalProjectPath(store)
		if err != nil {
			continue
		}
		canonical = strings.ToLower(canonical)
		if path == canonical || strings.HasPrefix(path, canonical+"/") {
			return true
		}
	}
	return false
}

var reservedProjectDirectories = map[string]bool{
	".agents":   true,
	".claude":   true,
	".codex":    true,
	".github":   true,
	".git":      true,
	".opencode": true,
	".protobot": true,
}

var reservedProjectControlFiles = map[string]bool{
	"attestations":       true,
	"kits.lock":          true,
	"policy.yaml":        true,
	"project.yaml":       true,
	"projection.yaml":    true,
	"test-catalog.jsonl": true,
}

var reservedProjectFiles = map[string]bool{
	".protobot/project.yaml":       true,
	".protobot/projection.yaml":    true,
	".protobot/test-catalog.jsonl": true,
	"agents.md":                    true,
	"claude.md":                    true,
	"opencode.json":                true,
}
