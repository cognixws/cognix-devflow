package goaffected

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A module: b imports a, c imports b (an importer of an importer), d's
// external test imports a, e stands alone, and a embeds a data file.
func module(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/m\n\ngo 1.22\n",
		"a/a.go":          "package a\n\nimport _ \"embed\"\n\n//go:embed data/q.sql\nvar Q string\n",
		"a/data/q.sql":    "select 1;\n",
		"b/b.go":          "package b\n\nimport _ \"example.com/m/a\"\n",
		"c/c.go":          "package c\n\nimport _ \"example.com/m/b\"\n",
		"d/d.go":          "package d\n",
		"d/d_test.go":     "package d_test\n\nimport (\n\t\"testing\"\n\t_ \"example.com/m/a\"\n)\n\nfunc TestD(t *testing.T) {}\n",
		"e/e.go":          "package e\n",
		"main.go":         "package main\n\nfunc main() {}\n",
		"docs/readme.txt": "x\n",
	}
	for p, s := range files {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackages(t *testing.T) {
	dir := module(t)
	cases := []struct {
		name    string
		changed []string
		want    string
	}{
		{"a change reaches its importers and their importers", []string{"a/a.go"}, "example.com/m/a example.com/m/b example.com/m/c example.com/m/d"},
		{"an embedded file belongs to the package above it", []string{"a/data/q.sql"}, "example.com/m/a example.com/m/b example.com/m/c example.com/m/d"},
		{"a leaf touches only itself", []string{"c/c.go"}, "example.com/m/c"},
		{"a package nobody imports", []string{"e/e.go"}, "example.com/m/e"},
		{"the root package", []string{"main.go"}, "example.com/m"},
		{"a path in no package resolves nothing", []string{"docs/readme.txt"}, ""},
		{"a deleted package resolves nothing", []string{"gone/x.go"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Packages(dir, tc.changed)
			if err != nil {
				t.Fatal(err)
			}
			if s := strings.Join(got, " "); s != tc.want {
				t.Fatalf("got %q, want %q", s, tc.want)
			}
		})
	}
}
