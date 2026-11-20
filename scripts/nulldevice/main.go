// Command nulldevice fails if a non-test Go source file hardcodes the Unix
// null device.
//
// Used as the check-portable-null-device local pre-commit hook. The literal
// path "/dev/null" is not portable: Windows exposes the null device as NUL.
// Go provides os.DevNull for platform-independent null-device redirection, so
// a non-test Go file that uses the literal as an actual OS path (an
// environment variable value, a git config flag, or a command argument) must
// use os.DevNull instead.
//
// Exempt, because they match /dev/null as a cross-platform text convention
// rather than use it as an OS path: ==/!= comparisons against the literal and
// switch case values. Text-matching calls such as strings.HasSuffix are not
// exempt; suppress them with a "portability:allow-null-device" comment on the
// violating line or the line immediately preceding it.
//
// Comments and *_test.go files are never checked. The check inspects the Go
// AST, so raw strings, multi-line expressions, and select sends need no
// special handling.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	nullDevice = "/dev/null"
	pragma     = "portability:allow-null-device"
)

type violation struct {
	line int
	text string
}

// atBoundary reports whether /dev/null at idx in s is a standalone path, not
// a suffix of a longer path (/tmp/dev/null) or an identifier continuation.
// A flag-attached argument such as -o/dev/null still counts.
func atBoundary(s string, idx int) bool {
	isPath := func(c byte) bool {
		return c == '_' || c == '.' || c == '/' || c == '-' ||
			c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	if idx > 0 && isPath(s[idx-1]) {
		start := idx - 1
		for start > 0 && isPath(s[start-1]) {
			start--
		}
		token := s[start:idx]
		if s[start] != '-' || strings.Contains(token, "/") {
			return false
		}
	}
	end := idx + len(nullDevice)
	if end < len(s) {
		c := s[end]
		if c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return false
		}
	}
	return true
}

func hasNullDevice(value string) bool {
	for pos := 0; ; {
		i := strings.Index(value[pos:], nullDevice)
		if i < 0 {
			return false
		}
		if atBoundary(value, pos+i) {
			return true
		}
		pos += i + len(nullDevice)
	}
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func stringLit(e ast.Expr) *ast.BasicLit {
	if lit, ok := unparen(e).(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return lit
	}
	return nil
}

// findViolations returns the non-portable /dev/null uses in src.
func findViolations(filename string, src []byte) ([]violation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	suppressed := map[int]bool{}
	for _, group := range file.Comments {
		for _, c := range group.List {
			if strings.Contains(c.Text, pragma) {
				start, end := fset.Position(c.Pos()).Line, fset.Position(c.End()).Line
				suppressed[start] = true
				suppressed[end+1] = true
			}
		}
	}

	exempt := map[*ast.BasicLit]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op == token.EQL || n.Op == token.NEQ {
				for _, operand := range []ast.Expr{n.X, n.Y} {
					if lit := stringLit(operand); lit != nil {
						exempt[lit] = true
					}
				}
			}
		case *ast.CaseClause:
			for _, e := range n.List {
				if lit := stringLit(e); lit != nil {
					exempt[lit] = true
				}
			}
		}
		return true
	})

	lines := strings.Split(string(src), "\n")
	seen := map[int]bool{}
	var found []violation
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || exempt[lit] {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !hasNullDevice(value) {
			return true
		}
		line := fset.Position(lit.Pos()).Line
		if suppressed[line] || seen[line] {
			return true
		}
		seen[line] = true
		found = append(found, violation{line, strings.TrimSpace(lines[line-1])})
		return true
	})
	sort.Slice(found, func(i, j int) bool { return found[i].line < found[j].line })
	return found, nil
}

// check reports violations for each file to stdout and read or parse errors
// to stderr, returning the process exit code.
func check(files []string) int {
	failed := false
	for _, path := range files {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: cannot read %s: %v\n", path, err)
			failed = true
			continue
		}
		found, err := findViolations(path, src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: cannot parse %s: %v\n", path, err)
			failed = true
			continue
		}
		for _, v := range found {
			fmt.Printf("%s:%d: hardcoded \"/dev/null\" is not portable "+
				"(Windows uses \"NUL\"); use os.DevNull instead: %s\n", path, v.line, v.text)
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

func main() {
	os.Exit(check(os.Args[1:]))
}
