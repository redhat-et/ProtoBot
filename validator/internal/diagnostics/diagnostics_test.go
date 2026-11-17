package diagnostics

import "testing"

func TestIsControlOrDangerous(t *testing.T) {
	testCases := []struct {
		r    rune
		want bool
	}{
		{'\x00', true},   // C0 null
		{'\x1f', true},   // C0 unit separator
		{'\x7f', true},   // DEL
		{'\x80', true},   // C1 control
		{'\x9f', true},   // C1 control
		{'\u2028', true}, // line separator
		{'\u2029', true}, // paragraph separator
		{'\u200e', true}, // LRM (bidi)
		{'\u200f', true}, // RLM (bidi)
		{'\u202e', true}, // RLO (bidi)
		{'\u061c', true}, // Arabic letter mark (Cf)
		{'\u200b', true}, // zero-width space (Cf)
		{'\u2066', true}, // LRI (bidi)
		{'a', false},
		{' ', false},
		{'\t', true}, // C0 tab is a control character
		{'日', false},
	}
	for _, tc := range testCases {
		if got := IsControlOrDangerous(tc.r); got != tc.want {
			t.Errorf("IsControlOrDangerous(%U) = %v, want %v", tc.r, got, tc.want)
		}
	}
}

func TestHasControl(t *testing.T) {
	testCases := []struct {
		s    string
		want bool
	}{
		{"good-name.yaml", false},
		{"bad\x00name.yaml", true},
		{"bad\u200bname.yaml", true},
		{"bad\u2028name.yaml", true},
		{"\xffinvalid", true},
		{"", false},
	}
	for _, tc := range testCases {
		if got := HasControl(tc.s); got != tc.want {
			t.Errorf("HasControl(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestSanitizeField(t *testing.T) {
	testCases := []struct {
		in, want string
	}{
		{"plain text", "plain text"},
		{"null\x00byte", "null\\x00byte"},
		{"line\nbreak", "line\\x0abreak"},
		{"del\x7fmark", "del\\x7fmark"},
		{"c1\x85next", "c1\\x85next"},
		{"line\u2028sep", "line\\xe2\\x80\\xa8sep"},
		{"para\u2029sep", "para\\xe2\\x80\\xa9sep"},
		{"bidi\u200emark", "bidi\\xe2\\x80\\x8emark"},
		{"rlo\u202eturn", "rlo\\xe2\\x80\\xaeturn"},
		{"zwsp\u200bhid", "zwsp\\xe2\\x80\\x8bhid"},
		{"\xff", "\\xff"},
		{"日", "日"},
	}
	for _, tc := range testCases {
		if got := SanitizeField(tc.in); got != tc.want {
			t.Errorf("SanitizeField(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSortPathCodeMessage(t *testing.T) {
	diags := []Key{
		{Path: "b.yaml", Code: "rule.b", Message: "msg2"},
		{Path: "a.yaml", Code: "rule.b", Message: "msg1"},
		{Path: "a.yaml", Code: "rule.a", Message: "msg2"},
		{Path: "a.yaml", Code: "rule.a", Message: "msg1"},
	}
	Sort(diags, func(k Key) Key { return k })

	want := []Key{
		{Path: "a.yaml", Code: "rule.a", Message: "msg1"},
		{Path: "a.yaml", Code: "rule.a", Message: "msg2"},
		{Path: "a.yaml", Code: "rule.b", Message: "msg1"},
		{Path: "b.yaml", Code: "rule.b", Message: "msg2"},
	}
	for i, expected := range want {
		if diags[i] != expected {
			t.Errorf("diags[%d] = %+v, want %+v", i, diags[i], expected)
		}
	}
}

func TestSortTieBreakers(t *testing.T) {
	diags := []Key{
		{Path: "b.yaml", Code: "rule.b", Message: "msg2"},
		{Path: "a.yaml", Line: 2, Code: "rule.a", Field: "id", Message: "msg1"},
		{Path: "a.yaml", Line: 1, Code: "rule.b", Field: "id", Message: "msg1"},
		{Path: "a.yaml", Line: 1, Code: "rule.a", Field: "text", Message: "msg1"},
		{Path: "a.yaml", Line: 1, Code: "rule.a", Field: "id", Message: "msg2"},
		{Path: "a.yaml", Line: 1, Code: "rule.a", Field: "id", Message: "msg1"},
	}
	Sort(diags, func(k Key) Key { return k })

	want := []Key{
		{Path: "a.yaml", Line: 1, Code: "rule.a", Field: "id", Message: "msg1"},
		{Path: "a.yaml", Line: 1, Code: "rule.a", Field: "id", Message: "msg2"},
		{Path: "a.yaml", Line: 1, Code: "rule.a", Field: "text", Message: "msg1"},
		{Path: "a.yaml", Line: 1, Code: "rule.b", Field: "id", Message: "msg1"},
		{Path: "a.yaml", Line: 2, Code: "rule.a", Field: "id", Message: "msg1"},
		{Path: "b.yaml", Code: "rule.b", Message: "msg2"},
	}
	if len(diags) != len(want) {
		t.Fatalf("len(diags) = %d, want %d", len(diags), len(want))
	}
	for i, got := range diags {
		if got != want[i] {
			t.Errorf("diags[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestSortPreservesEqualKeyOrder(t *testing.T) {
	type item struct {
		key   Key
		stamp int
	}
	diags := []item{
		{Key{Path: "a.yaml", Code: "rule.a", Message: "same"}, 1},
		{Key{Path: "a.yaml", Code: "rule.a", Message: "same"}, 2},
		{Key{Path: "a.yaml", Code: "rule.a", Message: "same"}, 3},
	}
	Sort(diags, func(it item) Key { return it.key })
	for i, it := range diags {
		if it.stamp != i+1 {
			t.Errorf("diags[%d].stamp = %d, want %d", i, it.stamp, i+1)
		}
	}
}
