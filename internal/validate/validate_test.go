package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognixws/cognix-devflow/internal/contract"
	"github.com/cognixws/cognix-devflow/internal/gitx"
)

type fixture struct {
	t    *testing.T
	dir  string
	repo *gitx.Repo
	base string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func newFixture(t *testing.T, files map[string]string) *fixture {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	f := &fixture{t: t, dir: dir}
	f.write(files)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base")
	f.base = git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "switch", "-qc", "feat/x")
	r, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.repo = r
	return f
}

func (f *fixture) write(files map[string]string) {
	for p, s := range files {
		full := filepath.Join(f.dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if s == "<delete>" {
			os.Remove(full)
			continue
		}
		os.WriteFile(full, []byte(s), 0o644)
	}
}

func (f *fixture) commit(files map[string]string) {
	f.write(files)
	git(f.t, f.dir, "add", "-A")
	git(f.t, f.dir, "commit", "-qm", "change")
}

func (f *fixture) run(section string) []Finding {
	c, err := contract.Parse([]byte(section))
	if err != nil {
		f.t.Fatal(err)
	}
	head := git(f.t, f.dir, "rev-parse", "HEAD")
	out, err := Run(Input{Repo: f.repo, Contract: c, Base: f.base, Head: head, Branch: "feat/x", Date: "2026-11-05"}, true)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func codes(fs []Finding) string {
	var c []string
	for _, f := range fs {
		c = append(c, f.Level+":"+f.Code)
	}
	return strings.Join(c, ",")
}

func TestRules(t *testing.T) {
	sec := `{"flow":1,"rules":[{"when":["db/**"],"need":["docs/db.md"],"why":"documented"},{"refuse":["**/*.xlsx"],"unless":["fixtures/**"],"why":"no client data"}]}`
	f := newFixture(t, map[string]string{"db/a.sql": "a", "docs/db.md": "d"})
	f.commit(map[string]string{"db/a.sql": "b", "x/client.xlsx": "secret", "fixtures/ok.xlsx": "ok"})
	if got := codes(f.run(sec)); got != "owed:rule-need,fail:rule-refuse" {
		t.Fatalf("got %s", got)
	}
	f.commit(map[string]string{"docs/db.md": "updated", "x/client.xlsx": "<delete>"})
	// The file is gone at head but still in the branch's history: deletions
	// are not writes, and the head diff no longer adds it.
	if got := codes(f.run(sec)); got != "" {
		t.Fatalf("got %s", got)
	}
}

func TestNoteAndDebts(t *testing.T) {
	sec := `{"flow":1,"modules":["handoff"],"noteMaxLines":3}`
	f := newFixture(t, map[string]string{"docs/handoff/open/t.md": "# T\n\n- [ ] first debt to pay later\n- [ ] second debt stays\n"})
	f.commit(map[string]string{"src/a.go": "x"})
	if got := codes(f.run(sec)); got != "owed:note" {
		t.Fatalf("got %s", got)
	}
	f.commit(map[string]string{
		"docs/handoff/2026-11-05-feat-x.md": "a\nb\nc\nd\n",
		"docs/handoff/open/t.md":            "# T\n\n- [x] first debt to pay later — paid\n",
	})
	if got := codes(f.run(sec)); got != "fail:note-length,fail:debt-deleted" {
		t.Fatalf("got %s", got)
	}
	f.commit(map[string]string{
		"docs/handoff/2026-11-05-feat-x.md": "a\nb\n",
		"docs/handoff/open/t.md":            "# T\n\n- [x] first debt to pay later — paid\n- [ ] second debt stays\n",
	})
	if got := codes(f.run(sec)); got != "" {
		t.Fatalf("got %s", got)
	}
}

func TestChangelogAndDecisions(t *testing.T) {
	sec := `{"flow":1,"modules":["changelog","decisions"]}`
	f := newFixture(t, map[string]string{"CHANGELOG.md": "# c\n", "docs/decisions/README.md": "| 0001 |\n", "docs/decisions/0001-a.md": "x"})
	f.commit(map[string]string{
		"CHANGELOG.md":             "# c\n- edited\n",
		"docs/decisions/0002-b.md": "# ADR\n- **Boundary**: protocol | persistence | security model | process — which one\n",
		"docs/decisions/0001-c.md": "# ADR\n- **Boundary**: process — x\n",
	})
	got := codes(f.run(sec))
	for _, want := range []string{"fail:changelog-direct", "fail:adr-boundary", "owed:adr-index", "fail:adr-number"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestDiffCheckAndGenerated(t *testing.T) {
	sec := `{"flow":1,"generated":[{"run":"echo v2 > gen.txt","files":["gen.txt"]}]}`
	f := newFixture(t, map[string]string{"gen.txt": "v1\n"})
	f.commit(map[string]string{"a.txt": "<<<<<<< HEAD\nx\n"})
	got := codes(f.run(sec))
	if got != "fail:diff-check,fail:generated-stale" {
		t.Fatalf("got %s", got)
	}
}
