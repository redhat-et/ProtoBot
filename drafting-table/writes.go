package guard

import (
	"errors"
	"strings"
)

func writeTargets(tool string, input map[string]any) ([]string, error) {
	switch tool {
	case "write", "edit":
		path, ok := stringField(input, "filePath")
		if !ok {
			return nil, errors.New("filePath is missing")
		}
		return []string{path}, nil
	case "apply_patch":
		patch, ok := stringField(input, "patchText")
		if !ok {
			return nil, errors.New("patchText is missing")
		}
		return parsePatchTargets(patch)
	default:
		return nil, errors.New("unknown writer")
	}
}

func parsePatchTargets(patch string) ([]string, error) {
	if len(patch) > 2<<20 || !strings.Contains(patch, "*** Begin Patch") || !strings.Contains(patch, "*** End Patch") {
		return nil, errors.New("patch format is not recognized")
	}
	var targets []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSuffix(line, "\r")
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: ", "--- a/", "+++ b/"} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if path == "" || path == "/dev/null" {
				continue
			}
			if strings.ContainsAny(path, "\x00\r\n\t") || strings.HasPrefix(path, "\"") || strings.HasSuffix(path, "\"") {
				return nil, errors.New("patch target is ambiguous")
			}
			targets = append(targets, path)
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("patch has no inspectable file target")
	}
	return targets, nil
}
