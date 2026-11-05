package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*.md", "README.md", true},
		{"*.md", "docs/a.md", false},
		{"**/*.md", "docs/a.md", true},
		{"**/*.md", "README.md", true},
		{"web/**", "web/a/b.js", true},
		{"web/**", "web", true},
		{"web/**", "webx/a", false},
		{"internal/**/x.go", "internal/x.go", true},
		{"internal/**/x.go", "internal/a/b/x.go", true},
		{"scripts/docs-*.mjs", "scripts/docs-check.mjs", true},
		{"scripts/docs-*.mjs", "scripts/lib/docs-a.mjs", false},
		{"go.mod", "go.mod", true},
		{"go.mod", "sub/go.mod", false},
		{"a/?.txt", "a/b.txt", true},
		{"a/[bc].txt", "a/d.txt", false},
	}
	for _, c := range cases {
		if got := Match(c.pat, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, p := range []string{"a/**/b", "*.go", "web/**"} {
		if !Valid(p) {
			t.Errorf("Valid(%q) = false", p)
		}
	}
	for _, p := range []string{"", "/abs", "a**b", "a/[x"} {
		if Valid(p) {
			t.Errorf("Valid(%q) = true", p)
		}
	}
}
