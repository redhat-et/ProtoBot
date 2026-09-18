package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/redhat-et/protobot/ears-manager/internal/specvalidation"
)

const (
	outputHuman = "human"
	outputJSON  = "json"
	version     = "dev"
)

type optionSpec struct {
	takesValue bool
}

type options struct {
	values map[string][]string
}

func (o options) has(name string) bool {
	return len(o.values[name]) > 0
}

func (o options) one(name string) string {
	values := o.values[name]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func (o options) list(name string) []string {
	return append([]string(nil), o.values[name]...)
}

type globalOptions struct {
	output  string
	help    bool
	version bool
	rest    []string
}

type Mutation struct {
	Applied bool     `json:"applied"`
	Paths   []string `json:"paths"`
}

type successEnvelope struct {
	SchemaVersion int                         `json:"schema_version"`
	OK            bool                        `json:"ok"`
	Command       string                      `json:"command"`
	Data          any                         `json:"data"`
	Diagnostics   []specvalidation.Diagnostic `json:"diagnostics"`
	Mutation      Mutation                    `json:"mutation"`
}

type failureEnvelope struct {
	SchemaVersion int         `json:"schema_version"`
	OK            bool        `json:"ok"`
	Command       string      `json:"command"`
	Error         failureJSON `json:"error"`
}

type failureJSON struct {
	Code        string                      `json:"code"`
	Message     string                      `json:"message"`
	ExitCode    int                         `json:"exit_code"`
	Diagnostics []specvalidation.Diagnostic `json:"diagnostics"`
	Mutation    string                      `json:"mutation"`
	Retry       string                      `json:"retry"`
}

type commandFailure struct {
	Code        string
	Message     string
	ExitCode    int
	Diagnostics []specvalidation.Diagnostic
	Mutation    string
	Retry       string
}

func (e *commandFailure) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func usageFailure(message string) *commandFailure {
	return &commandFailure{
		Code:     "usage.invalid_request",
		Message:  message,
		ExitCode: 2,
		Mutation: "none",
		Retry:    "correct-request",
	}
}

func projectFailure(code, message string) *commandFailure {
	return &commandFailure{
		Code:     code,
		Message:  message,
		ExitCode: 3,
		Mutation: "none",
		Retry:    "select-or-upgrade-project",
	}
}

func validationFailure(code, message string, diagnostics []specvalidation.Diagnostic) *commandFailure {
	return &commandFailure{
		Code:        code,
		Message:     message,
		ExitCode:    4,
		Diagnostics: append([]specvalidation.Diagnostic(nil), diagnostics...),
		Mutation:    "none",
		Retry:       "revise-request",
	}
}

func conflictFailure(code, message string, diagnostics []specvalidation.Diagnostic) *commandFailure {
	return &commandFailure{
		Code:        code,
		Message:     message,
		ExitCode:    5,
		Diagnostics: append([]specvalidation.Diagnostic(nil), diagnostics...),
		Mutation:    "none",
		Retry:       "refresh-and-review",
	}
}

func ioFailure(code, message string) *commandFailure {
	return &commandFailure{
		Code:     code,
		Message:  message,
		ExitCode: 6,
		Mutation: "unknown",
		Retry:    "reconcile-before-retry",
	}
}

func internalFailure(message string) *commandFailure {
	return &commandFailure{
		Code:     "internal.failure",
		Message:  message,
		ExitCode: 70,
		Mutation: "none",
		Retry:    "retain-diagnostic",
	}
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	global, err := parseGlobal(args)
	if err != nil {
		return writeFailure(stdout, stderr, outputHuman, commandName(args), err)
	}

	if global.version {
		return writeSuccess(stdout, global.output, "version", map[string]string{"version": version}, Mutation{})
	}
	if global.help || len(global.rest) == 0 {
		return writeHelp(stdout, global.output, global.rest)
	}

	command := commandName(global.rest)
	data, mutation, failure := dispatch(global.rest, stdin)
	if failure != nil {
		return writeFailure(stdout, stderr, global.output, command, failure)
	}
	return writeSuccess(stdout, global.output, command, data, mutation)
}

func parseGlobal(args []string) (globalOptions, *commandFailure) {
	result := globalOptions{output: outputHuman}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--help":
			result.help = true
		case arg == "--version":
			result.version = true
		case arg == "--output":
			if index+1 >= len(args) {
				return result, usageFailure("option --output requires a value")
			}
			index++
			if failure := setOutput(&result.output, args[index]); failure != nil {
				return result, failure
			}
		case strings.HasPrefix(arg, "--output="):
			if failure := setOutput(&result.output, strings.TrimPrefix(arg, "--output=")); failure != nil {
				return result, failure
			}
		default:
			result.rest = append(result.rest, arg)
		}
	}
	return result, nil
}

func setOutput(output *string, value string) *commandFailure {
	if value != outputHuman && value != outputJSON {
		return usageFailure(fmt.Sprintf("unsupported output format %q", value))
	}
	*output = value
	return nil
}

func parseOptions(args []string, specs map[string]optionSpec) (options, *commandFailure) {
	result := options{values: make(map[string][]string)}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			return result, usageFailure(fmt.Sprintf("unexpected argument %q", arg))
		}
		nameValue := strings.TrimPrefix(arg, "--")
		name := nameValue
		value := ""
		if equal := strings.IndexByte(nameValue, '='); equal >= 0 {
			name = nameValue[:equal]
			value = nameValue[equal+1:]
		}
		spec, exists := specs[name]
		if !exists {
			return result, usageFailure(fmt.Sprintf("unknown option --%s", name))
		}
		if spec.takesValue {
			if equal := strings.IndexByte(nameValue, '='); equal < 0 {
				if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
					return result, usageFailure(fmt.Sprintf("option --%s requires a value", name))
				}
				index++
				value = args[index]
			}
			if value == "" {
				return result, usageFailure(fmt.Sprintf("option --%s requires a non-empty value", name))
			}
		} else if strings.Contains(nameValue, "=") {
			return result, usageFailure(fmt.Sprintf("option --%s does not take a value", name))
		} else {
			value = "true"
		}
		result.values[name] = append(result.values[name], value)
	}
	return result, nil
}

func commandName(args []string) string {
	if len(args) == 0 {
		return "ears-manager"
	}
	if args[0] == "check" {
		return "check"
	}
	if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
		return args[0] + " " + args[1]
	}
	return args[0]
}

func writeSuccess(stdout io.Writer, output, command string, data any, mutation Mutation) int {
	if output == outputJSON {
		return writeJSON(stdout, successEnvelope{
			SchemaVersion: 1,
			OK:            true,
			Command:       command,
			Data:          data,
			Diagnostics:   []specvalidation.Diagnostic{},
			Mutation:      normalizedMutation(mutation),
		})
	}
	if data == nil {
		if _, err := fmt.Fprintf(stdout, "%s: ok\n", command); err != nil {
			return 6
		}
		return 0
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return 70
	}
	if _, err := fmt.Fprintf(stdout, "%s: ok\n%s\n", command, encoded); err != nil {
		return 6
	}
	return 0
}

func writeFailure(stdout, stderr io.Writer, output, command string, failure *commandFailure) int {
	if failure == nil {
		failure = internalFailure("command failed without a diagnostic")
	}
	failure.Mutation = normalizeMutation(failure.Mutation)
	if output == outputJSON {
		if outputFailure := writeJSON(stdout, failureEnvelope{
			SchemaVersion: 1,
			OK:            false,
			Command:       command,
			Error: failureJSON{
				Code:        failure.Code,
				Message:     failure.Message,
				ExitCode:    failure.ExitCode,
				Diagnostics: normalizedDiagnostics(failure.Diagnostics),
				Mutation:    failure.Mutation,
				Retry:       failure.Retry,
			},
		}); outputFailure != 0 {
			return outputFailure
		}
		return failure.ExitCode
	}
	if _, err := fmt.Fprintf(stderr, "error[%s]: %s\n", failure.Code, failure.Message); err != nil {
		return 6
	}
	for _, diagnostic := range failure.Diagnostics {
		location := diagnostic.Path
		if diagnostic.Field != "" {
			if location == "" {
				location = diagnostic.Field
			} else {
				location += ":" + diagnostic.Field
			}
		}
		if location != "" {
			if _, err := fmt.Fprintf(stderr, "at %s: %s\n", location, diagnostic.Message); err != nil {
				return 6
			}
		} else {
			if _, err := fmt.Fprintf(stderr, "%s\n", diagnostic.Message); err != nil {
				return 6
			}
		}
		if diagnostic.Hint != "" {
			if _, err := fmt.Fprintf(stderr, "hint: %s\n", diagnostic.Hint); err != nil {
				return 6
			}
		}
	}
	if failure.Mutation != "" {
		if _, err := fmt.Fprintf(stderr, "mutation: %s\n", failure.Mutation); err != nil {
			return 6
		}
	}
	if failure.Retry != "" {
		if _, err := fmt.Fprintf(stderr, "retry: %s\n", failure.Retry); err != nil {
			return 6
		}
	}
	return failure.ExitCode
}

func normalizedDiagnostics(diagnostics []specvalidation.Diagnostic) []specvalidation.Diagnostic {
	if diagnostics == nil {
		return []specvalidation.Diagnostic{}
	}
	return append([]specvalidation.Diagnostic{}, diagnostics...)
}

func writeJSON(writer io.Writer, value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 70
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return 6
	}
	return 0
}

func normalizedMutation(mutation Mutation) Mutation {
	mutation.Paths = append([]string(nil), mutation.Paths...)
	if mutation.Paths == nil {
		mutation.Paths = []string{}
	}
	return mutation
}

func normalizeMutation(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func writeHelp(stdout io.Writer, output string, args []string) int {
	text := helpText(args)
	if output == outputJSON {
		return writeSuccess(stdout, output, "help", map[string]string{"text": text}, Mutation{})
	}
	if _, err := io.WriteString(stdout, text); err != nil {
		return 6
	}
	return 0
}

func helpText(args []string) string {
	path := strings.Join(args, " ")
	switch path {
	case "requirement":
		return "Usage: ears-manager requirement <add|list|show|update|retire> [options]\n"
	case "interface":
		return "Usage: ears-manager interface <add|list|show> [options]\n"
	case "artifact":
		return "Usage: ears-manager artifact <get|put> [options]\n"
	case "change-set":
		return "Usage: ears-manager change-set create [options]\n"
	case "check":
		return "Usage: ears-manager check [--change-set CS-ID]\n"
	case "requirement add":
		return "Usage: ears-manager requirement add --change-set CS-ID --id REQ-ID --type TYPE --text TEXT --verification-mode MODE --provenance PROVENANCE --created ISO8601 [--interface ID] [--scope SCOPE]\n"
	case "requirement list":
		return "Usage: ears-manager requirement list [--interface ID] [--scope SCOPE] [--type TYPE] [--status STATUS] [--relationship TYPE]\n"
	case "requirement show":
		return "Usage: ears-manager requirement show --id REQ-ID\n"
	case "requirement update":
		return "Usage: ears-manager requirement update --change-set CS-ID --id REQ-ID [record fields...]\n"
	case "requirement retire":
		return "Usage: ears-manager requirement retire --change-set CS-ID --id REQ-ID\n"
	case "interface add":
		return "Usage: ears-manager interface add --change-set CS-ID --id ID --name NAME --type TYPE --created ISO8601 [--spec-approach TEXT] [--description TEXT]\n"
	case "interface list":
		return "Usage: ears-manager interface list [--type TYPE] [--status STATUS]\n"
	case "interface show":
		return "Usage: ears-manager interface show --id ID\n"
	case "artifact get":
		return "Usage: ears-manager artifact get (--id ID | --kind KIND)\n"
	case "artifact put":
		return "Usage: ears-manager artifact put --change-set CS-ID --id ID --kind KIND --path PATH --owner OWNER (--content-file PATH | --content-stdin)\n"
	case "change-set create":
		return "Usage: ears-manager change-set create --intent TEXT --implementation-required true|false --created ISO8601 [--affected-interface ID] [--affected-scope SCOPE]\n"
	default:
		return "Usage: ears-manager [--output human|json] <command> [<subcommand>] [options]\n\nCommands:\n  check\n  requirement add|list|show|update|retire\n  interface add|list|show\n  artifact get|put\n  change-set create\n\nUse --help after a command for command-specific usage.\n"
	}
}

func requireOption(parsed options, name string) (string, *commandFailure) {
	value := strings.TrimSpace(parsed.one(name))
	if value == "" {
		return "", usageFailure(fmt.Sprintf("option --%s is required", name))
	}
	return value, nil
}
