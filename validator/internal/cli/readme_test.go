package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redhat-et/protobot/validator/internal/revision"
)

// readmeCodeBlocks returns the contents, one entry per fenced code block, of
// every fenced code block in path whose language tag is "sh", "bash", or
// "text".
func readmeCodeBlocks(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var blocks []string
	var current []string
	inBlock := false
	keep := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if !strings.HasPrefix(trimmed, "```") {
				continue
			}
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			inBlock = true
			keep = lang == "sh" || lang == "bash" || lang == "text"
			current = nil
			continue
		}
		if trimmed == "```" {
			inBlock = false
			if keep {
				blocks = append(blocks, strings.Join(current, "\n"))
			}
			continue
		}
		current = append(current, line)
	}
	if inBlock {
		t.Fatalf("%s has an unterminated fenced code block", path)
	}
	return blocks
}

// readmeCommandLines extracts validator invocation lines from blocks: lines
// whose first whitespace-delimited field is exactly "validator" or
// "./validator". Diagnostic-output examples such as
// "validator: <code>: <message>" and option-description lines such as
// "  --target PATH   checkout directory to validate" do not match, because
// their first field is "validator:" or "--target" respectively.
func readmeCommandLines(blocks []string) []string {
	var lines []string
	for _, block := range blocks {
		for _, line := range strings.Split(block, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if fields[0] == "validator" || fields[0] == "./validator" {
				lines = append(lines, strings.TrimSpace(line))
			}
		}
	}
	return lines
}

// synopsisArgs tokenizes a documented command line into the arguments that
// follow the program name, stripping the synopsis bracket notation used for
// optional flags (e.g. "[--allow-self-comparison]") so the result can be fed
// to parseArgs the same way a shell would pass argv.
func synopsisArgs(line string) []string {
	fields := strings.Fields(line)[1:]
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimPrefix(field, "[")
		field = strings.TrimSuffix(field, "]")
		if field == "" {
			continue
		}
		args = append(args, field)
	}
	return args
}

// TestReadmeCommandExamples parses validator/README.md and checks that every
// documented "validator"/"./validator" command line uses only flags
// recognized by cli.Run's argument parser, then executes the documented
// quickstart invocation against a Git fixture to confirm it still succeeds,
// and that the documented self-comparison rejection still holds.
func TestReadmeCommandExamples(t *testing.T) {
	readmePath := filepath.Join(moduleRoot(t), "README.md")
	blocks := readmeCodeBlocks(t, readmePath)
	commandLines := readmeCommandLines(blocks)
	if len(commandLines) == 0 {
		t.Fatalf("%s: no validator command lines found in sh/bash/text code blocks", readmePath)
	}

	var quickstart string
	for _, line := range commandLines {
		args := synopsisArgs(line)
		if _, err := parseArgs(args); err != nil {
			t.Errorf("README command %q does not parse as valid flags: %v", line, err)
		}
		if strings.HasPrefix(line, "./validator --target") {
			quickstart = line
		}
	}
	if quickstart == "" {
		t.Fatalf("%s: no \"./validator --target ...\" quickstart example found", readmePath)
	}

	target := gitRepo(t)
	git(t, target, "branch", "origin/main")
	writeSpecFile(t, target, "spec/requirements/REQ-API-00001.yaml", requirementYAML("REQ-API-00001"))
	git(t, target, "add", ".")
	git(t, target, "commit", "-m", "advance HEAD past origin/main")

	args := synopsisArgs(quickstart)
	for i, arg := range args {
		if arg == "/path/to/checkout" {
			args[i] = target
		}
	}

	code, stdout, stderr := runCLIRaw(args...)
	if code != exitOK {
		t.Fatalf("quickstart command %q failed: code=%d stdout=%q stderr=%q", quickstart, code, stdout, stderr)
	}
	if !strings.Contains(stdout, "validator: ok") {
		t.Fatalf("quickstart command %q stdout %q does not report ok", quickstart, stdout)
	}

	code, stdout, stderr = runCLIRaw("--target", target, "--base", "HEAD")
	if code != exitFailure || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, revision.CodeSelfComparison) {
		t.Fatalf("stderr %q omitted %s", stderr, revision.CodeSelfComparison)
	}
}
