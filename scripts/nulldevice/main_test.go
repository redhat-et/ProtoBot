package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindViolations(t *testing.T) {
	const pkg = "package main\n"
	tests := []struct {
		name string
		src  string
		want []int // violating line numbers
	}{
		{"env assignment", pkg + "var _ = append(env, \"GIT_CONFIG_GLOBAL=/dev/null\")\n", []int{2}},
		{"bare argument", pkg + "var _ = []string{\"--\", \"/dev/null\"}\n", []int{2}},
		{"hooks path flag", pkg + "var _ = \"core.hooksPath=/dev/null\"\n", []int{2}},
		{"os.DevNull concatenation", pkg + "var _ = \"GIT_CONFIG_GLOBAL=\" + os.DevNull\n", nil},
		{"raw string", pkg + "var _ = `GIT_CONFIG_GLOBAL=/dev/null`\n", []int{2}},
		{"bare raw string", pkg + "var _ = `/dev/null`\n", []int{2}},
		{"== rhs", pkg + "var _ = p == \"/dev/null\"\n", nil},
		{"!= rhs", pkg + "var _ = p != \"/dev/null\"\n", nil},
		{"== lhs", pkg + "var _ = \"/dev/null\" == p\n", nil},
		{"!= lhs", pkg + "var _ = \"/dev/null\" != p\n", nil},
		{"raw comparison", pkg + "var _ = p == `/dev/null`\n", nil},
		{"parenthesized comparison", pkg + "var _ = (\"/dev/null\") == p\n", nil},
		{"multiline ==", pkg + "var _ = p ==\n\t\"/dev/null\"\n", nil},
		{"multiline lhs ==", pkg + "var _ = \"/dev/null\" ==\n\tp\n", nil},
		{"line comment", pkg + "// use \"/dev/null\" here\n", nil},
		{"block comment", pkg + "/*\n \"/dev/null\"\n*/\n", nil},
		{"switch case", pkg + "func f(p string) { switch p {\ncase \"/dev/null\":\n} }\n", nil},
		{"switch case multiple", pkg + "func f(p string) { switch p {\ncase \"a:b\", \"/dev/null\":\n} }\n", nil},
		{"switch case multiline", pkg + "func f(p string) { switch p {\ncase\n\t\"/dev/null\":\n} }\n", nil},
		{"select send", pkg + "func f(ch chan string) { select {\ncase ch <- \"/dev/null\":\n} }\n", []int{3}},
		{"larger path", pkg + "var _ = \"/tmp/dev/null\"\n", nil},
		{"larger raw path", pkg + "var _ = `/tmp/dev/null`\n", nil},
		{"trailing slash", pkg + "var _ = \"/dev/null/x\"\n", []int{2}},
		{"trailing dash", pkg + "var _ = \"/dev/null-2\"\n", []int{2}},
		{"identifier continuation", pkg + "var _ = \"/dev/nullable\"\n", nil},
		{"flag attached", pkg + "var _ = []string{\"-o/dev/null\"}\n", []int{2}},
		{"text matching call", pkg + "var _ = strings.HasSuffix(p, \"/dev/null\")\n", []int{2}},
		{"pragma same line", pkg + "var _ = \"a=/dev/null\" // portability:allow-null-device\n", nil},
		{"pragma preceding line", pkg + "// portability:allow-null-device\nvar _ = \"a=/dev/null\"\n", nil},
		{"pragma does not leak", pkg + "// portability:allow-null-device\n\nvar _ = \"a=/dev/null\"\n", []int{4}},
		{"comparison plus violation", pkg + "var _ = p == \"/dev/null\"\nvar _ = \"a=/dev/null\"\n", []int{3}},
		{"no literal", pkg + "var _ = \"hello\"\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findViolations("x.go", []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			var lines []int
			for _, v := range got {
				lines = append(lines, v.line)
			}
			if len(lines) != len(tc.want) {
				t.Fatalf("got lines %v, want %v", lines, tc.want)
			}
			for i := range lines {
				if lines[i] != tc.want[i] {
					t.Fatalf("got lines %v, want %v", lines, tc.want)
				}
			}
		})
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	bad := "package main\nvar _ = \"a=/dev/null\"\n"
	tests := []struct {
		name  string
		files []string
		want  int
	}{
		{"no files", nil, 0},
		{"non-go ignored", []string{write("n.txt", bad)}, 0},
		{"test file exempt", []string{write("a_test.go", bad)}, 0},
		{"violation fails", []string{write("b.go", bad)}, 1},
		{"conforming passes", []string{write("c.go", "package main\nvar _ = os.DevNull\n")}, 0},
		{"missing file fails", []string{filepath.Join(dir, "missing.go")}, 1},
		{"parse error fails", []string{write("d.go", "not go")}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := check(tc.files); got != tc.want {
				t.Fatalf("check() = %d, want %d", got, tc.want)
			}
		})
	}
}
