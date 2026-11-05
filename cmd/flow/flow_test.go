package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func write(t *testing.T, dir string, files map[string]string) {
	for p, s := range files {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const contractFile = `{"setup":[],"extensions":{"picode-devflow":{"flow":1,
 "checks":{"unit":{"run":"echo unit >> runs.log; test ! -f src/fail","when":["src/**"]},
           "docs":{"run":"echo docs >> runs.log","when":["docs/**","*.md"]}},
 "full":"echo full >> runs.log",
 "everything":[".picode.json"],
 "modules":["handoff"]}}}`

func repo(t *testing.T) string {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, map[string]string{".picode.json": contractFile, "README.md": "r\n", ".gitignore": "runs.log\n"})
	git(t, dir, "add", ".picode.json", "README.md", ".gitignore")
	git(t, dir, "commit", "-qm", "base")
	return dir
}

func flow(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var out, errb bytes.Buffer
	code := runMain(args, strings.NewReader(""), &out, &errb)
	return code, out.String() + errb.String()
}

func TestStatusOnMainSaysStartABranch(t *testing.T) {
	dir := repo(t)
	code, out := flow(t, dir, "status", "--json")
	var st statusOut
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil {
		t.Fatalf("%d %s", code, out)
	}
	if !strings.Contains(st.Next, "start a branch") || !st.MainCheckout {
		t.Fatalf("%+v", st)
	}
}

func TestCheckCloseAndReuse(t *testing.T) {
	dir := repo(t)
	git(t, dir, "switch", "-qc", "feat/cart")
	write(t, dir, map[string]string{"src/cart.go": "package cart\n"})

	// Uncommitted work: check runs what it needs; close refuses.
	if code, out := flow(t, dir, "check"); code != 0 || !strings.Contains(out, "✓ unit") {
		t.Fatalf("check: %d %s", code, out)
	}
	if code, out := flow(t, dir, "close"); code != 1 || !strings.Contains(out, "uncommitted") {
		t.Fatalf("close dirty: %d %s", code, out)
	}
	git(t, dir, "add", "src/cart.go")
	git(t, dir, "commit", "-qm", "cart")

	code, out := flow(t, dir, "close", "--json")
	var c closeOut
	if json.Unmarshal([]byte(out), &c) != nil {
		t.Fatalf("%s", out)
	}
	if code != 0 || !c.OK || len(c.Checks) != 1 || c.Checks[0].Name != "unit" || c.Checks[0].Reused {
		t.Fatalf("close: %d %+v", code, c)
	}
	if len(c.Findings) != 1 || c.Findings[0].Code != "note" {
		t.Fatalf("the note is owed: %+v", c.Findings)
	}
	// Strict turns owed into a refusal.
	if code, _ := flow(t, dir, "close", "--strict"); code != 3 {
		t.Fatalf("strict: %d", code)
	}
	// Same content again: reused, not run.
	runs, _ := os.ReadFile(filepath.Join(dir, "runs.log"))
	before := strings.Count(string(runs), "unit")
	code, out = flow(t, dir, "close")
	runs, _ = os.ReadFile(filepath.Join(dir, "runs.log"))
	if code != 0 || !strings.Contains(out, "reused") || strings.Count(string(runs), "unit") != before {
		t.Fatalf("reuse: %d %s", code, out)
	}
	// A note makes it ready.
	write(t, dir, map[string]string{"docs/handoff/2026-11-05-feat-cart.md": "Cart.\n"})
	git(t, dir, "add", "docs/handoff/2026-11-05-feat-cart.md")
	git(t, dir, "commit", "-qm", "note")
	if code, out := flow(t, dir, "close", "--strict"); code != 0 || !strings.Contains(out, "ready to land") {
		t.Fatalf("ready: %d %s", code, out)
	}
}

func TestCloseFailsOnRedCheckAndContractChangeRunsFull(t *testing.T) {
	dir := repo(t)
	git(t, dir, "switch", "-qc", "feat/red")
	write(t, dir, map[string]string{"src/a.go": "x", "src/fail": "1"})
	git(t, dir, "add", "src/a.go")
	git(t, dir, "commit", "-qm", "a")
	if code, out := flow(t, dir, "close"); code != 1 || !strings.Contains(out, "uncommitted") {
		t.Fatalf("untracked file counts as uncommitted: %d %s", code, out)
	}
	git(t, dir, "add", "src/fail")
	git(t, dir, "commit", "-qm", "fail marker")
	if code, out := flow(t, dir, "close"); code != 1 || !strings.Contains(out, "✗ unit") {
		t.Fatalf("red: %d %s", code, out)
	}
	write(t, dir, map[string]string{".picode.json": strings.Replace(contractFile, `"modules":["handoff"]`, `"modules":[]`, 1)})
	code, out := flow(t, dir, "status", "--json")
	var st statusOut
	json.Unmarshal([]byte(out), &st)
	if code != 0 || !st.Selection.Full || st.Selection.Checks[0] != "full" {
		t.Fatalf("contract change is full: %d %+v", code, st.Selection)
	}
}

func TestScopeBatch(t *testing.T) {
	dir := repo(t)
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	var out bytes.Buffer
	code := runMain([]string{"scope", "--batch"}, strings.NewReader(`["src/a.go"]`+"\n"+`["x/unknown"]`+"\n"), &out, &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if code != 0 || len(lines) != 2 || !strings.Contains(lines[0], `"checks":["unit"]`) || !strings.Contains(lines[1], `"full":true`) {
		t.Fatalf("%d %s", code, out.String())
	}
}

// Decision table (ADR-0401 F4d): land gates on the target's contract.
//
//	# | contract on main        | branch's contract           | branch's change       | outcome
//	1 | strict unit check       | drops the unit check        | breaks src            | refused: unit failed under the contract on main; land command never runs
//	2 | strict unit check       | same                        | sound                 | land command runs
//	3 | none                    | brings one                  | sound                 | gates on the branch's own; land command runs
//	4 | does not parse          | sound                       | sound                 | refused, names main's contract
func TestLandGatesOnTheTargetContract(t *testing.T) {
	landing := func(c string) string {
		return strings.Replace(c, `"modules":["handoff"]`, `"modules":[],"land":"echo landed {branch} >> lands.log"`, 1)
	}
	setup := func(t *testing.T, mainFile string) string {
		dir := t.TempDir()
		git(t, dir, "init", "-q", "-b", "main")
		files := map[string]string{"README.md": "r\n", ".gitignore": "runs.log\nlands.log\n"}
		if mainFile != "" {
			files[".picode.json"] = mainFile
		}
		write(t, dir, files)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "base")
		git(t, dir, "switch", "-qc", "feat/x")
		return dir
	}
	landed := func(dir string) bool { _, err := os.Stat(filepath.Join(dir, "lands.log")); return err == nil }
	commit := func(t *testing.T, dir string, files map[string]string) {
		write(t, dir, files)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "change")
	}
	strict := strings.Replace(landing(contractFile), `"full":"echo full >> runs.log"`, `"full":"test ! -f src/fail"`, 1)
	weak := strings.Replace(strings.Replace(strict, `"unit":{"run":"echo unit >> runs.log; test ! -f src/fail","when":["src/**"]},`, ``, 1), `"full":"test ! -f src/fail"`, `"full":"true"`, 1)

	t.Run("1 a branch that drops its check", func(t *testing.T) {
		dir := setup(t, strict)
		commit(t, dir, map[string]string{".picode.json": weak, "src/fail": "x"})
		code, out := flow(t, dir, "land")
		if code == 0 || !strings.Contains(out, "failed under the contract on main") || landed(dir) {
			t.Fatalf("%d %s", code, out)
		}
	})
	t.Run("2 a sound change lands", func(t *testing.T) {
		dir := setup(t, strict)
		commit(t, dir, map[string]string{"src/ok.go": "package ok\n"})
		if code, out := flow(t, dir, "land"); code != 0 || !landed(dir) {
			t.Fatalf("%d %s", code, out)
		}
	})
	t.Run("3 the branch brings the first contract", func(t *testing.T) {
		dir := setup(t, "")
		commit(t, dir, map[string]string{".picode.json": strict, "src/ok.go": "package ok\n"})
		if code, out := flow(t, dir, "land"); code != 0 || !landed(dir) {
			t.Fatalf("%d %s", code, out)
		}
	})
	t.Run("4 main's contract does not parse", func(t *testing.T) {
		dir := setup(t, `{"extensions":{"picode-devflow":{"flow":1,"nope":true}}}`)
		commit(t, dir, map[string]string{".picode.json": strict, "src/ok.go": "package ok\n"})
		if code, out := flow(t, dir, "land"); code == 0 || !strings.Contains(out, "contract on main does not parse") || landed(dir) {
			t.Fatalf("%d %s", code, out)
		}
	})
}
