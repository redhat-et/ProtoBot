// Package diagnostics provides shared diagnostic sorting and character
// sanitization helpers used by validator check groups.
package diagnostics

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Key is the canonical sort key for validator diagnostics. Comparison
// order is Path, Line, Code, Field, then Message, all ascending. Callers
// whose diagnostic type omits Line or Field leave those fields zero so
// they do not affect order.
type Key struct {
	Path    string
	Line    int
	Code    string
	Field   string
	Message string
}

// Compare reports the canonical diagnostic order of a and b.
func Compare(a, b Key) int {
	if c := cmp.Compare(a.Path, b.Path); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Line, b.Line); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Code, b.Code); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Field, b.Field); c != 0 {
		return c
	}
	return cmp.Compare(a.Message, b.Message)
}

// Sort orders diags in place by the canonical diagnostic key.
func Sort[T any](diags []T, key func(T) Key) {
	slices.SortStableFunc(diags, func(a, b T) int {
		return Compare(key(a), key(b))
	})
}

// IsControlOrDangerous reports whether r is a C0, DEL, or C1 control, a
// line or paragraph separator (U+2028, U+2029), or a Unicode format
// character (Cf), including bidirectional controls.
func IsControlOrDangerous(r rune) bool {
	if unicode.IsControl(r) {
		return true
	}
	if r == 0x2028 || r == 0x2029 {
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// HasControl reports whether s contains an invalid UTF-8 byte or a
// control or dangerous rune.
func HasControl(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if IsControlOrDangerous(r) {
			return true
		}
		i += size
	}
	return false
}

// SanitizeField replaces control and dangerous characters in s with
// printable \xNN escapes so CLI output cannot inject control sequences.
func SanitizeField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, "\\x%02x", s[i])
			i++
			continue
		}
		if IsControlOrDangerous(r) {
			if r < 0x100 {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				for _, bt := range []byte(string(r)) {
					fmt.Fprintf(&b, "\\x%02x", bt)
				}
			}
		} else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
