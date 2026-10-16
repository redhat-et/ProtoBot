package guard

// openCodeVocabulary is this module's single harness row from the adapter
// contract. Tool names remain row data; the guard policy is shared logic.
var openCodeVocabulary = toolVocabulary{
	harness:       "opencode",
	version:       "1.18.30",
	writeTools:    []string{"edit", "write", "apply_patch"},
	shellTool:     "bash",
	readTools:     map[string]string{"read": "filePath"},
	searchTools:   map[string]string{"glob": "path", "grep": "path"},
	skillTool:     "skill",
	roleTools:     []string{"question", "todowrite"},
	blockedTools:  []string{"task", "webfetch", "websearch"},
	mcpNameFormat: "<server>_<tool>",
}

type toolVocabulary struct {
	harness       string
	version       string
	writeTools    []string
	shellTool     string
	readTools     map[string]string
	searchTools   map[string]string
	skillTool     string
	roleTools     []string
	blockedTools  []string
	mcpNameFormat string
}

func isReadOrSearchTool(tool string) bool {
	_, read := openCodeVocabulary.readTools[tool]
	_, search := openCodeVocabulary.searchTools[tool]
	return read || search
}
