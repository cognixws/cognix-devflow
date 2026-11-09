package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cognixws/cognix-devflow/skills"
)

// Every embedded guide is listed in the manifest (the guide tool's topics and
// the skills the CLIs load), and its frontmatter names its folder.
func TestGuidesAreListed(t *testing.T) {
	manifest, err := os.ReadFile("../../picode-extension.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range guides() {
		if !strings.Contains(string(manifest), `"skills/`+g+`"`) {
			t.Errorf("%s: missing from the manifest's skills", g)
		}
		if !strings.Contains(string(manifest), `"`+g+`"`) {
			t.Errorf("%s: missing from the guide tool's topics", g)
		}
		b, _ := skills.FS.ReadFile(g + "/SKILL.md")
		if !strings.HasPrefix(string(b), "---\nname: "+g+"\ndescription: ") {
			t.Errorf("%s: frontmatter does not name the guide", g)
		}
	}
}

// dev-flow-setup offers query-cost only when the repository holds a database:
// the command it carries is run on a repository with one and on ones without.
func TestSetupOffersQueryCostOnlyWithADatabase(t *testing.T) {
	b, err := skills.FS.ReadFile("dev-flow-setup/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	_, rest, ok := strings.Cut(text, "```sh db-signals\n")
	if !ok {
		t.Fatal("dev-flow-setup carries no db-signals command")
	}
	cmdline, _, _ := strings.Cut(rest, "\n  ```")
	cmdline = strings.TrimSpace(cmdline)
	for _, must := range []string{"Never add the\n  check yourself", "flow guide query-cost"} {
		if !strings.Contains(text, must) {
			t.Errorf("dev-flow-setup lost %q", must)
		}
	}
	cases := []struct {
		name  string
		files map[string]string
		want  string // what the command must list; "" = nothing
	}{
		{"1 no database: a Go service", map[string]string{"go.mod": "module x\n", "main.go": "package main\n", "README.md": "Talks to a sqlite or postgres database someday.\n"}, ""},
		{"2 empty repository", map[string]string{"README.md": "# x\n"}, ""},
		{"3 sql migration", map[string]string{"db/migrations/001_init.sql": "CREATE TABLE a(id int);\n"}, "001_init.sql"},
		{"4 sqlite driver", map[string]string{"app.py": "import sqlite3\n"}, "app.py"},
		{"5 postgres container", map[string]string{"compose.yaml": "services:\n  db:\n    image: postgres:16\n"}, "compose.yaml"},
		{"6 mysql container, quoted", map[string]string{"compose.yaml": "services:\n  db:\n    image: \"mysql:8.4\"\n"}, "compose.yaml"},
		{"7 D1 binding", map[string]string{"wrangler.toml": "[[d1_databases]]\nbinding = \"DB\"\n"}, "wrangler.toml"},
		{"8 redis only", map[string]string{"compose.yaml": "services:\n  c:\n    image: redis:7\n"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, "init", "-q")
			write(t, dir, c.files)
			git(t, dir, "add", "-A")
			cmd := exec.Command("sh", "-c", cmdline)
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if c.want == "" && got != "" {
				t.Errorf("no database here, but the command lists %q", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("a database here, but the command lists %q (want %s)", got, c.want)
			}
		})
	}
}
