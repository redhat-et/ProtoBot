// Package cli is the command line of validator:
//
//	validator --target PATH --base REF
//	validator --help
//
// The process is deterministic and network-free. It reads the checkout at
// --target and records --base as the comparison argument. It does not
// contact a tracker, open a network connection, or start a daemon.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/testtags"
)

// Exit statuses.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `usage:
  validator --target PATH --base REF
  validator --help

  --target PATH   checkout directory to validate
  --base REF      comparison base for identity and immutability checks
`

type options struct {
	target string
	base   string
	help   bool
}

// Run executes the validator command line and returns the exit status.
func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	if opts.help {
		_, _ = io.WriteString(stdout, usage)
		return exitOK
	}
	if opts.target == "" {
		return usageError(stderr, "--target is required")
	}
	if opts.base == "" {
		return usageError(stderr, "--base is required")
	}

	info, err := os.Stat(opts.target)
	if err != nil {
		if os.IsNotExist(err) {
			return fail(stderr, fmt.Sprintf("target path %q does not exist", opts.target))
		}
		return fail(stderr, fmt.Sprintf("cannot stat target path %q: %s", opts.target, err))
	}
	if !info.IsDir() {
		return fail(stderr, fmt.Sprintf("target path %q is not a directory", opts.target))
	}

	snapshot, diagnostics := layout.Read(opts.target)
	failed := false
	if len(diagnostics) > 0 {
		for _, diagnostic := range diagnostics {
			_, _ = fmt.Fprintf(stderr, "validator: %s: %s\n", diagnostic.Path, diagnostic.Message)
		}
		failed = true
	}

	for _, finding := range testtags.Check(opts.target, requirementIDs(snapshot)) {
		_, _ = io.WriteString(stderr, formatTagDiagnostic(finding))
		failed = true
	}
	if failed {
		return exitFailure
	}

	_, _ = fmt.Fprintf(stdout, "validator: ok\ntarget: %s\nbase: %s\n", opts.target, opts.base)
	return exitOK
}

func parseArgs(args []string) (options, error) {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return options{help: true}, nil
		}
	}

	var opts options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isOption(arg, "--target"):
			if opts.target != "" {
				return options{}, fmt.Errorf("--target may be specified once")
			}
			value, next, ok := optionValue(args, i)
			if !ok || value == "" {
				return options{}, fmt.Errorf("--target takes a directory path")
			}
			opts.target = value
			i = next - 1
		case isOption(arg, "--base"):
			if opts.base != "" {
				return options{}, fmt.Errorf("--base may be specified once")
			}
			value, next, ok := optionValue(args, i)
			if !ok || value == "" {
				return options{}, fmt.Errorf("--base takes a comparison-base argument")
			}
			opts.base = value
			i = next - 1
		default:
			return options{}, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	return opts, nil
}

func isOption(arg, name string) bool {
	return arg == name || strings.HasPrefix(arg, name+"=")
}

func optionValue(args []string, i int) (string, int, bool) {
	if _, value, ok := strings.Cut(args[i], "="); ok {
		return value, i + 1, true
	}
	if i+1 < len(args) {
		return args[i+1], i + 2, true
	}
	return "", i + 1, false
}

func usageError(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "validator: %s\n\n%s", message, usage)
	return exitUsage
}

func fail(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "validator: %s\n", message)
	return exitFailure
}

func requirementIDs(snapshot layout.Snapshot) map[string]struct{} {
	ids := make(map[string]struct{}, len(snapshot.Requirements))
	for _, document := range snapshot.Requirements {
		if document.Record.ID == "" {
			continue
		}
		ids[document.Record.ID] = struct{}{}
	}
	return ids
}

func formatTagDiagnostic(finding testtags.Diagnostic) string {
	if finding.Line > 0 {
		return fmt.Sprintf("validator: %s:%d: %s\n", finding.Path, finding.Line, finding.Message)
	}
	return fmt.Sprintf("validator: %s: %s\n", finding.Path, finding.Message)
}
