// Package cli is the command line of validator:
//
//	validator --target PATH --base REF
//	validator --help
//	validator -h
//
// The process is deterministic and network-free. It reads the checkout at
// --target and records --base as the comparison argument. It does not
// contact a tracker, open a network connection, or start a daemon.
package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/ears"
	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/specvalidation"
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
  validator -h

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

	// Leave opts.target as given. filepath.Clean is lexical and can
	// rewrite ".." through a symlink; later check groups can
	// canonicalize internally if they need a stable path.
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
	findings := make([]finding, 0, len(diagnostics)+len(snapshot.Requirements))
	for _, diagnostic := range diagnostics {
		findings = append(findings, finding{path: diagnostic.Path, code: diagnostic.Code, message: diagnostic.Message})
	}
	for _, diagnostic := range ears.CheckAll(requirementStatements(snapshot)) {
		findings = append(findings, finding{path: diagnostic.Path, code: diagnostic.Code, message: diagnostic.Message})
	}
	failed := len(findings) > 0
	if len(findings) > 0 {
		sortFindings(findings)
		for _, item := range findings {
			_, _ = fmt.Fprintf(stderr, "validator: %s: %s\n", item.path, item.message)
		}
	}
	for _, diagnostic := range specvalidation.Validate(snapshot) {
		failed = true
		_, _ = fmt.Fprintf(stderr, "validator: %s: %s\n", diagnostic.Path, diagnostic.Message)
	}
	for _, finding := range testtags.Check(opts.target, requirementIDs(snapshot)) {
		failed = true
		_, _ = io.WriteString(stderr, formatTagDiagnostic(finding))
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
		if strings.HasPrefix(args[i+1], "--") {
			return "", i + 1, false
		}
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

type finding struct {
	path    string
	code    string
	message string
}

func requirementStatements(snapshot layout.Snapshot) []ears.Statement {
	statements := make([]ears.Statement, 0, len(snapshot.Requirements))
	for _, document := range snapshot.Requirements {
		statements = append(statements, ears.Statement{
			Path: document.Path,
			Type: document.Record.Type,
			Text: document.Record.Text,
		})
	}
	return statements
}

func sortFindings(findings []finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.path != right.path {
			return left.path < right.path
		}
		if left.code != right.code {
			return left.code < right.code
		}
		return left.message < right.message
	})
}
