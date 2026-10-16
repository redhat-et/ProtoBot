package guard

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// These checks deliberately recognize common execution boundaries, not
// program contents. Unknown command prefixes also pass through the literal
// governed-write backstop; script-file execution and renamed binaries remain
// documented residual limits rather than a claim of general containment.
func isCodeInterpreter(name string, args []string) bool {
	name = strings.ToLower(name)
	family := ""
	switch {
	case name == "python" || isVersionedInterpreter(name, "python") || name == "pypy" || isVersionedInterpreter(name, "pypy"):
		family = "python"
	case name == "perl" || isVersionedInterpreter(name, "perl"):
		family = "perl"
	case name == "ruby" || isVersionedInterpreter(name, "ruby"):
		family = "ruby"
	case name == "node" || name == "nodejs":
		family = "node"
	case name == "osascript":
		family = "osascript"
	case name == "awk" || name == "gawk" || name == "mawk":
		family = "awk"
	case name == "php":
		family = "php"
	case name == "lua" || strings.HasPrefix(name, "lua5."):
		family = "lua"
	case name == "rscript":
		family = "rscript"
	case name == "gdb":
		family = "gdb"
	case name == "expect":
		family = "expect"
	case name == "pwsh" || name == "powershell":
		family = "powershell"
	case name == "guile":
		family = "guile"
	case name == "clisp":
		family = "clisp"
	case name == "sed":
		family = "sed"
	case slices.Contains([]string{"r", "tclsh", "erl", "elixir", "octave", "jshell", "fish", "csh", "tcsh"}, name):
		family = "opaque"
	default:
		return false
	}
	if family == "opaque" {
		return !safeOpaqueInterpreterInfo(name, args[1:])
	}
	if family == "sed" {
		return sedExecutionOpaque(args[1:])
	}
	return interpreterExecutesOpaqueCode(family, args[1:])
}

func safeOpaqueInterpreterInfo(name string, args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch name {
	case "r":
		return slices.Contains([]string{"--help", "--version", "-h", "-v", "-V"}, args[0])
	case "erl":
		return slices.Contains([]string{"-help", "-version"}, args[0])
	case "elixir", "octave", "jshell", "fish":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, args[0])
	default:
		return false
	}
}

func interpreterExecutesOpaqueCode(family string, args []string) bool {
	if len(args) == 1 && interpreterInfoOption(family, args[0]) {
		return false
	}
	if family == "awk" && len(args) == 2 && args[0] == "-W" && args[1] == "version" {
		return false
	}
	sourceFile := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-" {
			return true
		}
		if arg == "--" {
			if i+1 == len(args) {
				return true
			}
			return args[i+1] == "-" || family == "awk"
		}
		if !strings.HasPrefix(arg, "-") {
			if family == "awk" {
				return true // The first positional awk argument is source code.
			}
			if family == "python" {
				// shortcut: Python script-file contents remain an accepted residual boundary; upgrade to source inspection if that boundary changes.
				return false
			}
			return false // Ordinary script/source-file forms remain available.
		}
		if inlineInterpreterOption(family, arg) {
			return true
		}
		skip, known, scriptFile := interpreterOption(family, arg)
		if !known {
			return true // Unknown flags may select inline or stdin execution.
		}
		if skip && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			if scriptFile {
				if family == "python" || family == "awk" || args[i] == "-" {
					return true
				}
				sourceFile = true
			}
		}
	}
	// With no file or code option, these interpreters consume code from stdin.
	return !sourceFile
}

func interpreterInfoOption(family, arg string) bool {
	switch family {
	case "python":
		return slices.Contains([]string{"--help", "-h", "--version", "-V", "--help-env", "--help-xoptions", "--help-all"}, arg)
	case "perl":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "ruby":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "node":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "osascript":
		return arg == "-h"
	case "awk":
		return slices.Contains([]string{"--help", "-h", "--version"}, arg)
	case "php":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "lua":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "rscript":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "gdb":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "expect":
		return slices.Contains([]string{"--help", "-h", "--version", "-v", "-V"}, arg)
	case "powershell":
		return slices.Contains([]string{"-help", "-?", "-version"}, arg)
	case "guile":
		return slices.Contains([]string{"--help", "-h", "--version"}, arg)
	case "clisp":
		return slices.Contains([]string{"--help", "-h", "--version"}, arg)
	default:
		return false
	}
}

func inlineInterpreterOption(family, arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch family {
	case "python":
		return inlinePythonOption(name, arg)
	case "perl":
		return perlOptionExecutesCode(arg)
	case "ruby":
		return inlineRubyOption(name, arg)
	case "node":
		return inlineNodeOption(name, arg)
	}
	return inlineOtherInterpreterOption(family, name, arg)
}

func inlinePythonOption(name, arg string) bool {
	if strings.HasPrefix(arg, "-m") && len(arg) > 2 {
		return true
	}
	if strings.HasPrefix(arg, "--") {
		return name == "--command"
	}
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	for _, flag := range strings.TrimPrefix(arg, "-") {
		if flag == 'c' {
			return true
		}
		if flag == 'W' || flag == 'X' {
			return false
		}
	}
	return false
}

func inlineRubyOption(name, arg string) bool {
	if name == "--eval" {
		return true
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	for _, flag := range strings.TrimPrefix(arg, "-") {
		if flag == 'e' || flag == 'r' {
			return true
		}
		if strings.ContainsRune("IEFKC", flag) {
			return false
		}
	}
	return false
}

func inlineNodeOption(name, arg string) bool {
	return name == "--eval" || name == "--print" ||
		slices.Contains([]string{"--run", "--test", "--require", "--import", "--loader", "--experimental-loader"}, name) ||
		strings.HasPrefix(arg, "-e") || strings.HasPrefix(arg, "-p") || strings.HasPrefix(arg, "-r")
}

func inlineOtherInterpreterOption(family, name, arg string) bool {
	switch family {
	case "osascript":
		return strings.HasPrefix(arg, "-e")
	case "awk":
		return strings.HasPrefix(arg, "-e") || name == "--source" || strings.HasPrefix(arg, "--source=")
	case "php":
		return strings.HasPrefix(arg, "-r") || strings.HasPrefix(arg, "-R") || strings.HasPrefix(arg, "-B")
	case "lua", "rscript":
		return inlineLuaOrRscriptOption(family, name, arg)
	default:
		return inlineRuntimeInterpreterOption(family, arg)
	}
}

func inlineRuntimeInterpreterOption(family, arg string) bool {
	switch family {
	case "gdb":
		return inlineGDBOption(arg)
	case "expect":
		return strings.HasPrefix(arg, "-c")
	case "powershell":
		return inlinePowerShellOption(arg)
	case "guile":
		return inlineGuileOption(arg)
	case "clisp":
		return inlineCLISPOption(arg)
	default:
		return false
	}
}

func inlineGDBOption(arg string) bool {
	return strings.HasPrefix(arg, "-ex") || strings.HasPrefix(arg, "--ex=") || arg == "--ex" ||
		arg == "-eval-command" || strings.HasPrefix(arg, "--eval-command")
}

func inlineGuileOption(arg string) bool {
	return strings.HasPrefix(arg, "-c") || strings.HasPrefix(arg, "-e") ||
		arg == "--command" || arg == "--eval" || strings.HasPrefix(arg, "--command=") ||
		strings.HasPrefix(arg, "--eval=")
}

func inlineCLISPOption(arg string) bool {
	return strings.HasPrefix(arg, "-x") || arg == "-eval" || arg == "-execute" ||
		strings.HasPrefix(arg, "-eval=") || strings.HasPrefix(arg, "-execute=")
}

func inlineLuaOrRscriptOption(family, name, arg string) bool {
	return strings.HasPrefix(arg, "-e") ||
		(family == "lua" && name == "--execute") || (family == "rscript" && name == "--expression")
}

func inlinePowerShellOption(arg string) bool {
	lower := strings.ToLower(arg)
	return lower == "-c" || strings.HasPrefix(lower, "-command") || strings.HasPrefix(lower, "-c:")
}

// interpreterOption returns (takes following argv, recognized, is script file).
func interpreterOption(family, arg string) (bool, bool, bool) {
	name, _, hasValue := strings.Cut(arg, "=")
	if hasValue {
		return false, true, false
	}
	switch family {
	case "python":
		if slices.Contains([]string{"-W", "-X", "--check-hash-based-pycs"}, arg) {
			return true, true, false
		}
		if arg == "-m" {
			return true, true, true
		}
		if strings.HasPrefix(arg, "-m") && len(arg) > 2 {
			return false, true, true
		}
		if strings.HasPrefix(arg, "--") {
			return slices.Contains([]string{"--help", "--help-env", "--help-xoptions", "--help-all", "--version", "--check-hash-based-pycs"}, arg), isPythonLongOption(arg), false
		}
		if strings.HasPrefix(arg, "-") {
			flags := strings.TrimPrefix(arg, "-")
			for index, flag := range flags {
				if strings.ContainsRune("WX", flag) {
					return index == len(flags)-1, true, false
				}
				if !strings.ContainsRune("abBdiEhiIOqsu vV", flag) {
					return false, false, false
				}
			}
			return false, true, false
		}
	case "perl":
		if slices.Contains([]string{"-I", "-M", "-m", "-F"}, arg) {
			return true, true, false
		}
		return false, perlOptionKnown(arg), false
	case "ruby":
		if slices.Contains([]string{"-I", "-r", "-E", "--encoding", "-F", "-K", "-C"}, arg) {
			return true, true, false
		}
		return false, rubyOptionKnown(arg), false
	case "node":
		if nodeValueOption(name) {
			return true, true, false
		}
		return false, nodeOptionKnown(name), false
	case "osascript":
		return arg == "-l", arg == "-l" || arg == "-e", false
	case "awk":
		if slices.Contains([]string{"-F", "-v"}, arg) {
			return true, true, false
		}
		if arg == "-f" {
			return true, true, true
		}
		if arg == "-W" {
			return true, true, false
		}
		return false, awkOptionKnown(arg), false
	case "php":
		if slices.Contains([]string{"-d", "-z", "-c"}, arg) {
			return true, true, false
		}
		if arg == "-f" {
			return true, true, true
		}
		return false, phpOptionKnown(arg), false
	case "lua":
		if arg == "-l" {
			return true, true, false
		}
		return false, slices.Contains([]string{"-i", "-v", "--version"}, arg), false
	case "rscript":
		if arg == "-e" || arg == "--expression" {
			return true, true, false
		}
		if arg == "-f" {
			return true, true, true
		}
		return false, slices.Contains([]string{"--vanilla", "--default-packages", "--save", "--no-save", "--no-restore", "--verbose", "--quiet", "--slave", "--interactive"}, arg) || strings.HasPrefix(arg, "--default-packages="), false
	case "gdb":
		if gdbOptionTakesValue(arg) {
			return true, true, strings.Contains(arg, "command") && arg != "-ex" && arg != "--ex" && arg != "-eval-command" && arg != "--eval-command"
		}
		return false, strings.HasPrefix(arg, "-"), false
	case "expect":
		if arg == "-f" {
			return true, true, true
		}
		return false, slices.Contains([]string{"-c", "-d", "-i", "-n", "-N", "-v"}, arg), false
	case "powershell":
		if powershellTakesValue(arg) {
			return true, true, strings.EqualFold(arg, "-File")
		}
		return false, strings.HasPrefix(arg, "-"), false
	case "guile":
		if slices.Contains([]string{"-l", "--load-path", "-L"}, arg) {
			return true, true, false
		}
		if arg == "-s" {
			return true, true, true
		}
		return false, strings.HasPrefix(arg, "-"), false
	case "clisp":
		if arg == "-i" {
			return true, true, true
		}
		return false, strings.HasPrefix(arg, "-"), false
	}
	return false, false, false
}

func sedExecutionOpaque(args []string) bool {
	if len(args) == 1 && slices.Contains([]string{"--help", "-h", "--version"}, args[0]) {
		return false
	}
	var scripts []string
	scriptSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-f" || arg == "--file" || strings.HasPrefix(arg, "-f") && !strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "--file="):
			return true
		case arg == "-e" || arg == "--expression":
			if i+1 >= len(args) {
				return true
			}
			i++
			scripts = append(scripts, args[i])
			scriptSeen = true
		case strings.HasPrefix(arg, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(arg, "--expression="))
			scriptSeen = true
		case strings.HasPrefix(arg, "-e") && len(arg) > 2:
			scripts = append(scripts, arg[2:])
			scriptSeen = true
		case arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") || strings.HasPrefix(arg, "-i") && !strings.HasPrefix(arg, "--"):
		case slices.Contains([]string{"-n", "--quiet", "--silent", "-E", "-r", "--regexp-extended", "--posix", "--sandbox", "--follow-symlinks", "--debug", "-s", "--separate", "-u", "--unbuffered", "-z", "--null-data"}, arg):
		case arg == "--":
			if !scriptSeen {
				if i+1 >= len(args) {
					return true
				}
				scripts = append(scripts, args[i+1])
				scriptSeen = true
			}
			i = len(args)
		case strings.HasPrefix(arg, "-"):
			return true
		default:
			if !scriptSeen {
				scripts = append(scripts, arg)
				scriptSeen = true
			}
		}
	}
	if len(scripts) == 0 {
		return true // No script means sed reads its program from stdin.
	}
	for _, script := range scripts {
		if !safeSedScript(script) {
			return true
		}
	}
	return false
}

func safeSedScript(script string) bool {
	if slices.Contains([]string{"p", "q", "d"}, script) {
		return true
	}
	if len(script) < 4 || script[0] != 's' {
		return false
	}
	delimiter := script[1]
	if delimiter == '\n' || delimiter == '\\' || delimiter >= '0' && delimiter <= '9' || delimiter == ' ' || delimiter == '\t' {
		return false
	}
	index := 2
	for part := 0; part < 2; part++ {
		for index < len(script) {
			if script[index] == '\\' {
				index += 2
				continue
			}
			if script[index] == delimiter {
				index++
				break
			}
			index++
		}
		if index > len(script) || index == len(script) && (len(script) == 0 || script[len(script)-1] != delimiter) {
			return false
		}
	}
	for _, flag := range script[index:] {
		if !strings.ContainsRune("gIp0123456789", flag) {
			return false
		}
	}
	return true
}

func isPythonLongOption(arg string) bool {
	return slices.Contains([]string{"--help", "--help-env", "--help-xoptions", "--help-all", "--version", "--check-hash-based-pycs"}, arg)
}

func perlOptionExecutesCode(arg string) bool {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	flags := arg[1:]
	for i := 0; i < len(flags); i++ {
		flag := flags[i]
		if flag == 'e' || flag == 'E' {
			return true
		}
		if flag == 'M' || flag == 'm' {
			return true
		}
		if flag == '0' {
			for i+1 < len(flags) && flags[i+1] >= '0' && flags[i+1] <= '7' {
				i++
			}
			continue
		}
		if strings.ContainsRune("IMmFDi x", rune(flag)) {
			return false
		}
		if !strings.ContainsRune("acdl npsStTuUvVwWx", rune(flag)) {
			return false
		}
	}
	return false
}

func perlOptionKnown(arg string) bool {
	if strings.HasPrefix(arg, "-0") {
		return true
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	for _, flag := range arg[1:] {
		if !strings.ContainsRune("acdl npsStTuUvVwWx", flag) {
			return false
		}
	}
	return true
}

func rubyOptionKnown(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	for _, flag := range arg[1:] {
		if !strings.ContainsRune("acdlnpsSTvwWx", flag) {
			return false
		}
	}
	return true
}

func nodeValueOption(option string) bool {
	return slices.Contains([]string{"-r", "--require", "--import", "--loader", "--experimental-loader", "--conditions", "--inspect-port", "--icu-data-dir", "--openssl-config", "--redirect-warnings", "--title", "--max-old-space-size", "--max-semi-space-size", "--stack-size", "--diagnostic-dir", "--heapsnapshot-signal", "--watch-path", "--trace-event-categories", "--trace-event-file-pattern", "--experimental-policy", "--policy-integrity", "--input-type"}, option)
}

func nodeOptionKnown(option string) bool {
	if strings.HasPrefix(option, "--") {
		return slices.Contains([]string{"--help", "--version", "--v8-options", "--check", "--preserve-symlinks", "--preserve-symlinks-main", "--no-warnings", "--trace-warnings", "--throw-deprecation", "--no-deprecation", "--pending-deprecation", "--experimental-modules", "--input-type", "--watch", "--test", "--run", "--enable-source-maps", "--experimental-strip-types"}, option) || nodeValueOption(option)
	}
	return slices.Contains([]string{"-h", "-v", "-V", "-p", "-i", "-r"}, option)
}

func awkOptionKnown(arg string) bool {
	return slices.Contains([]string{"-b", "-c", "-d", "-g", "-h", "-L", "-M", "-o", "-p", "-s", "-t", "-V", "--help", "--version", "--lint", "--posix", "--traditional"}, arg) || strings.HasPrefix(arg, "--lint=")
}

func phpOptionKnown(arg string) bool {
	return slices.Contains([]string{"-a", "-c", "-d", "-f", "-h", "-i", "-l", "-m", "-n", "-q", "-s", "-v", "-w", "-z", "--help", "--version"}, arg)
}

func gdbOptionTakesValue(arg string) bool {
	return slices.Contains([]string{"-x", "--command", "-ex", "--ex", "-eval-command", "--eval-command", "-ix", "--init-command", "-iex", "--init-eval-command", "--directory", "-d", "--data-directory", "-cd", "--cd", "-p", "--pid", "-b", "--baud"}, arg)
}

func powershellTakesValue(arg string) bool {
	switch strings.ToLower(arg) {
	case "-file", "-executionpolicy", "-inputformat", "-outputformat", "-configurationname", "-workingdirectory", "-windowstyle", "-encodedcommand", "-encodedarguments", "-version":
		return true
	default:
		return false
	}
}

func isVersionedInterpreter(name, base string) bool {
	suffix := strings.TrimPrefix(name, base)
	if suffix == name || suffix == "" {
		return false
	}
	suffix = strings.TrimSuffix(suffix, "t")
	if suffix == "" || suffix[0] < '0' || suffix[0] > '9' {
		return false
	}
	for _, char := range suffix {
		if (char < '0' || char > '9') && char != '.' {
			return false
		}
	}
	return true
}

func isRecipeRunner(name string) bool {
	// This list is defense-in-depth, not an exhaustive command allowlist; the
	// literal governed-command argv backstop handles unknown prefixes.
	switch name {
	case "make", "gmake", "rake", "just", "task", "npm", "npx", "yarn", "pnpm", "bun", "bunx", "gradle", "gradlew", "ant", "mvn", "ninja", "cargo", "uv", "uvx", "tox", "poetry", "pipenv", "pip", "pipx", "cmake", "deno":
		return true
	default:
		return false
	}
}

func isInformationalCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	for _, arg := range args[1:] {
		if !slices.Contains([]string{"--help", "--version", "-h", "-v", "-V"}, arg) {
			return false
		}
	}
	return true
}

func goExecutionOpaque(args []string) bool {
	command, index, ok := goSubcommand(args)
	if !ok {
		return false
	}
	for _, arg := range args[index+1:] {
		option, _, _ := strings.Cut(arg, "=")
		option = strings.TrimLeft(option, "-") // Go accepts one or two leading dashes.
		if (command == "build" && option == "toolexec") || (command == "vet" && option == "vettool") || (command == "env" && option == "w") {
			return true
		}
	}
	return slices.Contains([]string{"test", "run", "generate", "tool"}, command)
}

func goSubcommand(args []string) (string, int, bool) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-C" {
			if i+1 >= len(args) {
				return "", 0, false
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "-C=") || strings.HasPrefix(arg, "-") {
			continue
		}
		return arg, i, true
	}
	return "", 0, false
}

func isOpaqueExecutionWrapper(name string) bool {
	return slices.Contains([]string{"flock", "watch", "script", "tmux", "screen", "parallel", "su", "ssh", "strace", "valgrind", "taskset", "runuser", "doas", "ionice", "ltrace", "chrt", "unshare", "perf", "nsenter", "systemd-run", "docker", "podman", "at", "batch", "chronic", "runcon"}, name)
}

func isOpaqueWrapperInvocation(name string, args []string) bool {
	if isInformationalCommand(args) {
		return false
	}
	switch name {
	case "flock":
		return flockHasCommand(args[1:])
	case "watch", "script", "parallel", "su", "ssh":
		return true
	case "tmux":
		return containsAny(args[1:], "new", "new-session", "neww", "new-window", "respawn-pane", "respawn-window", "run-shell", "pipe-pane", "send-keys")
	case "screen":
		return containsAny(args[1:], "-X", "-c", "-dm", "-dmS", "-D", "-S")
	case "strace":
		return straceCommandPresent(args[1:])
	case "valgrind", "taskset", "runuser", "doas", "ionice", "ltrace", "chrt", "unshare", "perf", "nsenter", "systemd-run", "docker", "podman", "at", "batch", "chronic", "runcon":
		return true
	default:
		return false
	}
}

func flockHasCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-c" || arg == "--command" || strings.HasPrefix(arg, "--command=") {
			return true
		}
		if arg == "--" {
			return i+2 < len(args)
		}
		if strings.HasPrefix(arg, "-") {
			if slices.Contains([]string{"-w", "-E", "--wait", "--conflict-exit-code"}, arg) {
				i++
			}
			continue
		}
		return i+1 < len(args) // The first positional is the lock file.
	}
	return false
}

func straceCommandPresent(args []string) bool {
	valueOptions := map[string]bool{"-e": true, "--expr": true, "--expression": true, "-o": true, "--output": true, "-p": true, "--attach": true, "-s": true, "--string-limit": true, "-u": true, "--user": true, "-E": true, "--env": true, "-P": true, "--status": true, "-O": true, "--summary-sort-by": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return i+1 < len(args)
		}
		if !strings.HasPrefix(arg, "-") {
			return true
		}
		option, _, hasValue := strings.Cut(arg, "=")
		if valueOptions[option] && !hasValue {
			i++
		}
	}
	return false
}

func containsAny(values []string, wanted ...string) bool {
	for _, value := range values {
		if slices.Contains(wanted, value) {
			return true
		}
	}
	return false
}

func hasCommandExecutionEnvironmentOverride(words []string) bool {
	// Execution-related assignments anywhere in the segment make a wrapped
	// child's behavior uninspectable.
	for _, word := range words {
		if commandExecutionEnvironmentAssignment(word) {
			return true
		}
	}
	return false
}

func commandExecutionEnvironmentAssignment(word string) bool {
	key, _, assignment := strings.Cut(word, "=")
	return assignment && commandExecutionEnvironmentKey(key)
}

func gitInvocationOpaque(args []string) bool {
	if len(args) == 2 && slices.Contains([]string{"--help", "--version", "-h", "-v", "-V"}, args[1]) {
		return false
	}
	command, index, ok := gitSubcommand(args)
	if !ok {
		return true
	}
	if gitConfiguresExecutableConfig(args, index) {
		return true
	}
	if command == "config" && gitConfigCommandWritesExecutableConfig(args[index+1:]) {
		return true
	}
	if gitSubcommandCanExecuteCommands(command, args[index+1:]) {
		return true
	}
	return !gitBuiltins[command]
}

func gitSubcommandCanExecuteCommands(command string, args []string) bool {
	switch command {
	case "ls-remote":
		return gitLsRemoteInvocationIsOpaque(args)
	case "bisect":
		return slices.Contains(args, "run")
	case "archive":
		return true
	case "bundle":
		return gitBundleCreates(args)
	case "grep":
		return gitGrepOpensFilesInPager(args)
	case "checkout-index", "fast-import", "format-patch", "instaweb", "mailsplit":
		return true
	case "difftool", "mergetool", "credential":
		return true
	case "branch", "remote", "tag", "worktree", "stash", "submodule":
		return !gitReadOnlyMetadataCommand(command, args)
	case "reflog":
		return !gitReflogShowCommand(args)
	case "add", "am", "apply", "checkout", "cherry-pick", "clean", "clone", "commit", "fetch", "fetch-pack", "filter-branch", "init", "merge", "mv", "notes", "pull", "push", "rebase", "reset", "restore", "revert", "rm", "send-pack", "switch", "update-index", "update-ref", "gc", "maintenance", "repack", "prune", "symbolic-ref":
		return true
	default:
		return false
	}
}

func gitLsRemoteInvocationIsOpaque(args []string) bool {
	// Upload-pack command overrides can execute an arbitrary program even when
	// the selected remote uses an otherwise ordinary transport. Skip values of
	// the other value-taking options while checking the whole option list.
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		switch {
		case arg == "--upload-pack", arg == "--exec",
			strings.HasPrefix(arg, "--upload-pack="), strings.HasPrefix(arg, "--exec="):
			return true
		case arg == "--server-option", arg == "-o", arg == "--sort":
			index++
		}
	}
	return gitLsRemoteHasHelperRemote(args)
}

func gitLsRemoteHasHelperRemote(args []string) bool {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--":
			return index+1 < len(args) && gitRemoteHelperURL(args[index+1])
		case arg == "--server-option", arg == "-o", arg == "--sort":
			if index+1 >= len(args) {
				return true
			}
			index++
		case strings.HasPrefix(arg, "--server-option="), strings.HasPrefix(arg, "--sort="):
			continue
		case slices.Contains([]string{"-q", "--quiet", "--exit-code", "--get-url", "--symref", "--heads", "--branches", "-b", "-h", "-t", "--tags", "--refs"}, arg):
			continue
		case gitLsRemoteShortOptionCluster(arg):
			continue
		case strings.HasPrefix(arg, "-"):
			// shortcut: unknown ls-remote options are denied; add newly supported
			// value-taking forms when the CLI contract requires them.
			return true
		default:
			return gitRemoteHelperURL(arg)
		}
	}
	return false
}

func gitLsRemoteShortOptionCluster(arg string) bool {
	if len(arg) < 3 || arg[0] != '-' || arg[1] == '-' {
		return false
	}
	for _, flag := range arg[1:] {
		if !strings.ContainsRune("qbht", flag) {
			return false
		}
	}
	return true
}

func gitRemoteHelperURL(value string) bool {
	separator := strings.Index(value, "::")
	if separator <= 0 {
		return false
	}
	// A standard URL may contain a double colon in an IPv6 address. It is a
	// helper URL only when the helper separator comes before any URL scheme.
	scheme := strings.Index(value, "://")
	return scheme < 0 || scheme > separator
}

func gitBundleCreates(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return arg == "create"
		}
	}
	return false
}

func gitGrepOpensFilesInPager(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--open-files-in-pager" || strings.HasPrefix(arg, "--open-files-in-pager=") {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "O") {
			return true
		}
	}
	return false
}

func ripgrepCanExecuteCommands(args []string) bool {
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return false
		}
		if optionSet(arg, "--pre") || optionSet(arg, "--pager") || ripgrepPagingUsesPager(arg) {
			return true
		}
		if arg == "--paging" {
			if index+1 >= len(args) {
				return true
			}
			index++
			if args[index] != "never" {
				return true
			}
		}
	}
	return false
}

func ripgrepPagingUsesPager(arg string) bool {
	if !strings.HasPrefix(arg, "--paging=") {
		return false
	}
	return strings.TrimPrefix(arg, "--paging=") != "never"
}

func optionSet(arg, option string) bool {
	return arg == option || strings.HasPrefix(arg, option+"=")
}

func gitReadOnlyMetadataCommand(command string, args []string) bool {
	switch command {
	case "branch":
		return gitBranchReadOnlyCommand(args)
	case "remote":
		return gitRemoteReadOnlyCommand(args)
	case "tag":
		return gitTagReadOnlyCommand(args)
	case "worktree":
		return len(args) == 1 && args[0] == "list"
	case "stash":
		return len(args) == 1 && args[0] == "list"
	case "submodule":
		return len(args) > 0 && args[0] == "status"
	default:
		return false
	}
}

func gitBranchReadOnlyCommand(args []string) bool {
	return len(args) == 0 || len(args) == 1 && slices.Contains([]string{"--list", "-l", "--show-current", "--show-ref"}, args[0])
}

func gitRemoteReadOnlyCommand(args []string) bool {
	return len(args) == 0 || len(args) == 1 && slices.Contains([]string{"-v", "--verbose"}, args[0]) || len(args) == 2 && args[0] == "get-url"
}

func gitTagReadOnlyCommand(args []string) bool {
	return len(args) == 0 || len(args) == 1 && slices.Contains([]string{"-l", "--list"}, args[0])
}

func gitReflogShowCommand(args []string) bool {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--output" {
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "show"
	}
	return true
}

func gitSubcommand(args []string) (string, int, bool) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1], i + 1, true
			}
			return "", 0, false
		}
		if !strings.HasPrefix(arg, "-") {
			return arg, i, true
		}
		option, _, hasValue := strings.Cut(arg, "=")
		if gitGlobalValueOption(option) && !hasValue {
			i++
			if i >= len(args) {
				return "", 0, false
			}
			continue
		}
		if gitGlobalFlag(option) || hasValue || (strings.HasPrefix(arg, "-c") && len(arg) > 2) || (strings.HasPrefix(arg, "-C") && len(arg) > 2) {
			continue
		}
		return "", 0, false
	}
	return "", 0, false
}

func gitGlobalValueOption(option string) bool {
	return slices.Contains([]string{"-C", "-c", "--config-env", "--exec-path", "--git-dir", "--work-tree", "--namespace", "--super-prefix"}, option)
}

func gitGlobalFlag(option string) bool {
	return slices.Contains([]string{"--no-pager", "--paginate", "--no-optional-locks", "--bare", "--literal-pathspecs", "--glob-pathspecs", "--noglob-pathspecs", "--no-replace-objects", "--no-lazy-fetch", "--html-path", "--man-path", "--info-path", "--help", "--version"}, option)
}

func gitConfiguresExecutableConfig(args []string, commandIndex int) bool {
	for i := 1; i < commandIndex; i++ {
		arg := args[i]
		var config string
		configEnv := false
		switch {
		case arg == "-c" || arg == "--config-env":
			configEnv = arg == "--config-env"
			i++
			if i >= commandIndex {
				return true
			}
			config = args[i]
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			config = arg[2:]
		case strings.HasPrefix(arg, "--config-env="):
			configEnv = true
			config = strings.TrimPrefix(arg, "--config-env=")
		}
		key, value, hasValue := strings.Cut(config, "=")
		if gitExecutionConfigKey(key) || gitRemoteHelperURLConfig(key, value, hasValue, configEnv) {
			return true
		}
	}
	return false
}

type gitConfigWriteState struct {
	positionals     []string
	write           bool
	readOnly        bool
	sectionMutation bool
	setsValue       bool
	deletesValue    bool
}

func gitConfigCommandWritesExecutableConfig(args []string) bool {
	state, safe := scanGitConfigWriteOptions(args)
	if !safe {
		return true
	}
	switch gitConfigActionSubcommand(state.positionals) {
	case "get", "list":
		state.readOnly = true
	case "set":
		state.write, state.setsValue = true, true
	case "unset":
		state.write, state.deletesValue = true, true
	case "rename-section", "remove-section", "edit":
		// Modern section-mutation actions can touch arbitrary config, and edit
		// launches an editor. Fail closed rather than infer their full effect.
		return true
	}
	for _, value := range state.positionals {
		// Key names can resemble subsection names (for example alias.run).
		if state.sectionMutation && gitExecutionConfigSection(value) {
			return true
		}
		if gitExecutionConfigKey(value) && (state.write || !state.readOnly && len(state.positionals) > 1) {
			return true
		}
	}
	return gitConfigWritesRemoteHelperURL(state)
}

func gitConfigWritesRemoteHelperURL(state gitConfigWriteState) bool {
	if state.readOnly || state.deletesValue && !state.setsValue || !state.write && len(state.positionals) < 2 {
		return false
	}
	keyIndex, valueIndex := 0, 1
	switch gitConfigActionSubcommand(state.positionals) {
	case "set", "unset":
		keyIndex, valueIndex = 1, 2
	}
	if valueIndex >= len(state.positionals) {
		return false
	}
	return gitRemoteHelperURLConfig(state.positionals[keyIndex], state.positionals[valueIndex], true, false)
}

func scanGitConfigWriteOptions(args []string) (gitConfigWriteState, bool) {
	var state gitConfigWriteState
	for index := 0; index < len(args); {
		arg := args[index]
		if arg == "--" {
			state.positionals = append(state.positionals, args[index+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			next, safe := inspectGitConfigWriteOption(args, index, &state)
			if !safe {
				return state, false
			}
			index = next
			continue
		}
		state.positionals = append(state.positionals, arg)
		index++
	}
	return state, true
}

func inspectGitConfigWriteOption(args []string, index int, state *gitConfigWriteState) (int, bool) {
	arg := args[index]
	option, value, hasValue := strings.Cut(arg, "=")
	switch option {
	case "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list", "-l", "--name-only", "--show-origin", "--show-scope":
		state.readOnly = true
	case "--unset-regexp":
		// Git uses its own regular-expression engine for this write form. A
		// pattern that looks benign may still match an execution-sensitive key.
		return index, false
	case "--rename-section", "--remove-section":
		state.write, state.sectionMutation = true, true
		if hasValue {
			state.positionals = append(state.positionals, value)
		}
	case "--add", "--replace-all", "--unset", "--unset-all", "--edit", "-e":
		return inspectGitConfigMutation(index, arg, option, hasValue, state)
	case "--file", "-f", "--type":
		return consumeGitConfigOptionValue(args, index, hasValue)
	case "--bool", "--int", "--bool-or-int", "--path", "--expiry-date":
		return parseGitConfigFlagOption(index, hasValue)
	case "--fixed-value", "--local", "--global", "--system", "--worktree", "--includes", "--no-includes", "--null", "-z":
	default:
		// Unknown config options that mention an execution key are not safe to infer.
		if gitExecutionConfigPattern(arg) {
			return index, false
		}
	}
	return index + 1, true
}

func inspectGitConfigMutation(index int, arg, option string, hasValue bool, state *gitConfigWriteState) (int, bool) {
	if option == "--edit" || option == "-e" || hasValue && strings.Contains(strings.ToLower(arg), "alias") {
		return index, false
	}
	state.write = true
	switch option {
	case "--add", "--replace-all":
		state.setsValue = true
	case "--unset", "--unset-all":
		state.deletesValue = true
	}
	return index + 1, true
}

func consumeGitConfigOptionValue(args []string, index int, hasValue bool) (int, bool) {
	if hasValue {
		return index + 1, true
	}
	if index+1 < len(args) {
		return index + 2, true
	}
	return index, false
}

func gitConfigOptionValue(args []string, index int, value string, hasValue bool) (string, int, bool) {
	if hasValue {
		return value, index + 1, true
	}
	if index+1 >= len(args) {
		return "", index, false
	}
	return args[index+1], index + 2, true
}

var gitExecutionConfigPrefixes = []string{
	"alias.", "pager.", "difftool.", "mergetool.", "filter.", "credential.", "diff.", "man.", "browser.", "url.",
}

var gitExecutionConfigExactKeys = []string{
	"core.pager", "core.editor", "core.hookspath", "core.fsmonitor", "core.sshcommand", "core.askpass", "core.gitproxy",
	"sequence.editor", "interactive.difffilter", "uploadpack.packobjectshook", "include.path", "help.browser",
	"web.browser", "instaweb.httpd", "core.alternaterefscommand", "http.extraheader", "http.proxy",
	"protocol.allow",
}

func gitExecutionConfigKey(key string) bool {
	key = strings.ToLower(key)
	return gitExecutionConfigHasPrefix(key) || gitExecutionConfigParameterizedKey(key) || slices.Contains(gitExecutionConfigExactKeys, key)
}

func gitExecutionConfigHasPrefix(key string) bool {
	for _, prefix := range gitExecutionConfigPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func gitExecutionConfigParameterizedKey(key string) bool {
	return gitConfigKeyHasPrefixAndSuffix(key, "merge.", ".driver") ||
		gitConfigKeyHasPrefixAndSuffix(key, "gpg.", ".program") || gitRemoteExecutionConfigKey(key) ||
		gitConfigKeyHasPrefixAndSuffix(key, "protocol.", ".allow") ||
		gitConfigKeyHasPrefixAndSuffix(key, "includeif.", ".path") ||
		gitConfigKeyHasPrefixAndSuffix(key, "sendemail.", ".smtpserver") ||
		gitConfigKeyHasPrefixAndSuffix(key, "submodule.", ".update") ||
		gitConfigKeyHasPrefixAndSuffix(key, "trailer.", ".cmd") ||
		gitConfigKeyHasPrefixAndSuffix(key, "trailer.", ".command") ||
		gitConfigKeyHasPrefixAndSuffix(key, "http.", ".extraheader") ||
		gitConfigKeyHasPrefixAndSuffix(key, "http.", ".proxy")
}

func gitConfigKeyHasPrefixAndSuffix(key, prefix, suffix string) bool {
	return strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix)
}

func gitRemoteExecutionConfigKey(key string) bool {
	return strings.HasPrefix(key, "remote.") &&
		(strings.HasSuffix(key, ".uploadpack") || strings.HasSuffix(key, ".receivepack") ||
			strings.HasSuffix(key, ".vcs"))
}

func gitRemoteHelperURLConfig(key, value string, hasValue, configEnv bool) bool {
	key = strings.ToLower(key)
	if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".url") {
		return false
	}
	return configEnv || hasValue && gitRemoteHelperURL(value)
}

func gitExecutionConfigSection(section string) bool {
	section = strings.ToLower(section)
	if slices.Contains([]string{"alias", "pager", "difftool", "mergetool", "filter", "credential", "diff", "merge", "gpg", "remote", "uploadpack", "include", "includeif", "sendemail", "submodule", "core", "sequence", "interactive", "man", "browser", "help", "web", "instaweb", "trailer", "url", "http", "protocol"}, section) {
		return true
	}
	// Section mutations cannot select individual keys. HTTP subsections may
	// contain proxy or credential-bearing settings, and protocol subsections
	// control which transports Git may invoke.
	for _, prefix := range []string{"alias.", "pager.", "difftool.", "mergetool.", "filter.", "credential.", "diff.", "merge.", "gpg.", "remote.", "includeif.", "sendemail.", "submodule.", "man.", "browser.", "web.", "instaweb.", "trailer.", "url.", "http.", "protocol."} {
		if strings.HasPrefix(section, prefix) {
			return true
		}
	}
	return false
}

func gitExecutionConfigPattern(pattern string) bool {
	pattern = strings.ToLower(pattern)
	if gitURLExecutionConfigPattern(pattern) || gitHTTPExecutionConfigPattern(pattern) {
		return true
	}
	// Trailer tokens are arbitrary, so a pattern naming this family can hide a
	// cmd/command key under a token not visible to the scanner.
	if strings.Contains(pattern, "trailer") {
		return true
	}
	for _, name := range []string{"alias", "pager", "difftool", "mergetool", "filter", "credential", "diff", "merge", "gpg", "remote", "uploadpack", "receivepack", "include", "sendemail", "submodule", "core", "sequence", "interactive", "man", "browser", "help", "web", "instaweb", "protocol"} {
		if strings.Contains(pattern, name) {
			return true
		}
	}
	return false
}

func gitURLExecutionConfigPattern(pattern string) bool {
	if gitConfigPatternMatchesAny(pattern, "url.https://example.invalid/.insteadof", "url.https://example.invalid/.pushinsteadof") {
		return true
	}
	return gitConfigPatternHasSectionPrefix(strings.ReplaceAll(pattern, `\.`, "."), "url.")
}

func gitHTTPExecutionConfigPattern(pattern string) bool {
	if gitConfigPatternMatchesAny(
		pattern,
		"http.extraheader",
		"http.proxy",
		"http.https://example.invalid/.extraheader",
		"http.https://example.invalid/.proxy",
	) {
		return true
	}
	if !strings.Contains(pattern, "http") {
		return false
	}
	pattern = strings.ReplaceAll(pattern, `\.`, ".")
	if gitConfigPatternHasSuffix(pattern, ".extraheader") || gitConfigPatternHasSuffix(pattern, ".proxy") {
		return true
	}
	// shortcut: generic regex intersection is not modeled; allow only a literal
	// final key suffix and fail closed on other HTTP patterns until a broader
	// regex analysis is needed.
	return !gitConfigPatternHasLiteralKeySuffix(pattern)
}

func gitConfigPatternMatchesAny(pattern string, keys ...string) bool {
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	for _, key := range keys {
		if matcher.MatchString(key) {
			return true
		}
	}
	return false
}

func gitConfigPatternHasSuffix(pattern, suffix string) bool {
	marker := suffix
	for start := 0; start < len(pattern); {
		index := strings.Index(pattern[start:], marker)
		if index < 0 {
			return false
		}
		end := start + index + len(marker)
		if end == len(pattern) || strings.ContainsRune("$)|?*+{,]", rune(pattern[end])) {
			return true
		}
		start = end
	}
	return false
}

func gitConfigPatternHasSectionPrefix(pattern, prefix string) bool {
	for start := 0; start < len(pattern); {
		index := strings.Index(pattern[start:], prefix)
		if index < 0 {
			return false
		}
		position := start + index
		if position == 0 || strings.ContainsRune("^|([{", rune(pattern[position-1])) {
			return true
		}
		start = position + len(prefix)
	}
	return false
}

func gitConfigPatternHasLiteralKeySuffix(pattern string) bool {
	pattern = strings.TrimSuffix(pattern, "$")
	separator := strings.LastIndex(pattern, ".")
	if separator < 0 || separator == len(pattern)-1 {
		return false
	}
	for _, char := range pattern[separator+1:] {
		if !isGitConfigKeyCharacter(char) {
			return false
		}
	}
	return true
}

func isGitConfigKeyCharacter(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-'
}

func shellExportsCommandExecutionEnvironment(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch filepath.Base(words[0]) {
	case "export", "declare", "typeset", "readonly":
	default:
		return false
	}
	for _, word := range words[1:] {
		key, _, _ := strings.Cut(strings.TrimLeft(word, "-"), "=")
		if commandExecutionEnvironmentKey(key) {
			return true
		}
	}
	return false
}

func commandExecutionEnvironmentKey(key string) bool {
	key = strings.ToUpper(key)
	return strings.HasPrefix(key, "GIT_CONFIG_") ||
		(strings.HasPrefix(key, "BASH_FUNC_") && strings.HasSuffix(key, "%%")) ||
		slices.Contains([]string{
			"GIT_PAGER", "PAGER", "GIT_EDITOR", "EDITOR", "VISUAL", "GIT_SEQUENCE_EDITOR",
			"GIT_EXTERNAL_DIFF", "GIT_EXTERNAL_DIFF_TRUST_EXIT_CODE", "RIPGREP_CONFIG_PATH",
			"GIT_SSH_COMMAND", "GIT_SSH", "GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE",
			"GIT_ALLOW_PROTOCOL", "GIT_PROXY_COMMAND",
			"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
			"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_TEMPLATE_DIR", "GIT_CEILING_DIRECTORIES",
		}, key)
}

var gitBuiltins = func() map[string]bool {
	result := make(map[string]bool)
	for _, command := range strings.Fields(`add am annotate apply archive bisect blame branch bugreport bundle cat-file check-attr check-ignore check-mailmap check-ref-format checkout checkout-index cherry cherry-pick citool clean clone column commit commit-tree config count-objects credential credential-cache credential-store describe diagnose diff diff-files diff-index diff-tree difftool fast-export fast-import fetch fetch-pack filter-branch fmt-merge-msg for-each-ref format-patch fsck gc get-tar-commit-id grep hash-object help http-backend http-fetch http-push imap-send index-pack init instaweb interpret-trailers log ls-files ls-remote ls-tree mailinfo mailsplit merge merge-base merge-file merge-index merge-ours merge-recursive merge-resolve merge-subtree mergetool mktag mktree mv name-rev notes pack-objects pack-redundant pack-refs patch-id prune prune-packed pull push quiltimport read-tree rebase receive-pack reflog remote repack replace request-pull rerere reset restore rev-list rev-parse revert rm send-pack shortlog show show-branch show-index show-ref sparse-checkout stage stash status stripspace submodule switch symbolic-ref tag unpack-file unpack-objects update-index update-ref update-server-info upload-archive upload-pack var verify-commit verify-pack verify-tag version whatchanged worktree write-tree maintenance range-diff scalar`) {
		result[command] = true
	}
	return result
}()
