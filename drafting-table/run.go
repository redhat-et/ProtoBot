package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Run evaluates one PreToolUse call and writes a refusal line only when it
// returns exit status 2.
func Run(args []string, stdin io.Reader, stderr io.Writer) int {
	harness, role, err := parseArgs(args)
	if err != nil {
		writeFailure(stderr, "input", err.Error())
		return 2
	}
	input, err := decodeInput(stdin)
	if err != nil {
		writeFailure(stderr, "input", err.Error())
		return 2
	}
	decision := evaluate(harness, role, input)
	if decision.allow {
		return 0
	}
	writeFailure(stderr, decision.rule, decision.reason)
	return 2
}

func parseArgs(args []string) (string, string, error) {
	var harness, role string
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return "", "", errors.New("each option requires a value")
		}
		switch args[i] {
		case "--harness":
			if harness != "" {
				return "", "", errors.New("--harness may be specified once")
			}
			harness = args[i+1]
		case "--role":
			if role != "" {
				return "", "", errors.New("--role may be specified once")
			}
			role = args[i+1]
		default:
			return "", "", errors.New("only --harness and --role are supported")
		}
		i++
	}
	if harness != "opencode" {
		return "", "", errors.New("--harness must be opencode")
	}
	if role != roleDraftingTable && role != roleOther {
		return "", "", errors.New("--role must be drafting-table or other")
	}
	return harness, role, nil
}

func decodeInput(reader io.Reader) (hookInput, error) {
	const maxInput = 2 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxInput+1))
	if err != nil {
		return hookInput{}, errors.New("could not read PreToolUse input")
	}
	if len(data) > maxInput {
		return hookInput{}, errors.New("PreToolUse input exceeds the 2 MiB limit")
	}
	var input hookInput
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return hookInput{}, errors.New("input must be a PreToolUse JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return hookInput{}, errors.New("input must contain exactly one JSON object")
	}
	if input.HookEventName != "PreToolUse" || input.SessionID == "" || input.CWD == "" || input.ToolName == "" || input.ToolInput == nil {
		return hookInput{}, errors.New("PreToolUse input requires hook_event_name, session_id, cwd, tool_name, and tool_input")
	}
	return input, nil
}

func writeFailure(writer io.Writer, rule, reason string) {
	reason = strings.NewReplacer("\n", " ", "\r", " ", "\x00", " ").Replace(reason)
	_, _ = fmt.Fprintf(writer, "drafting-table-guard: %s refused: %s\n", rule, reason)
}
