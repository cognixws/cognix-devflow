package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cognixws/picode-devflow/internal/agentsmd"
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
	write(t, dir, map[string]string{".picode.json": contractFile, "README.md": "r\n", ".gitignore": "runs.log\n", "AGENTS.md": "# Agents\n\n" + agentsmd.Block()})
	git(t, dir, "add", ".picode.json", "README.md", ".gitignore", "AGENTS.md")
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

// Decision table (module ui-review): a change to a screen owes two verdict
// lines in the handoff note.
//
//	# | changed            | note says                               | close
//	1 | src/app.css        | no verdict lines                        | owes uiux-review and visual-review
//	2 | src/app.css        | uiux-review: PASS, visual-review: PASS  | nothing owed for the UI
//	3 | src/app.css        | visual-review: FAIL                     | fails
//	4 | src/cart.go only   | nothing                                 | nothing owed for the UI
func TestUIReviewModule(t *testing.T) {
	file := strings.Replace(contractFile, `"modules":["handoff"]`, `"modules":["handoff","ui-review"]`, 1)
	run := func(t *testing.T, files map[string]string) (int, string) {
		dir := t.TempDir()
		git(t, dir, "init", "-q", "-b", "main")
		write(t, dir, map[string]string{".picode.json": file, "README.md": "r\n", ".gitignore": "runs.log\n"})
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "base")
		git(t, dir, "switch", "-qc", "feat/ui")
		write(t, dir, files)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "change")
		return flow(t, dir, "close", "--json")
	}
	notePath := "docs/handoff/" + time.Now().Format("2006-01-02") + "-feat-ui.md"
	count := func(out, code string) int { return strings.Count(out, `"code":"`+code+`"`) }
	if _, out := run(t, map[string]string{"src/app.css": "a{}\n", notePath: "# note\n"}); count(out, "ui-review") != 2 {
		t.Fatalf("row 1: %s", out)
	}
	if _, out := run(t, map[string]string{"src/app.css": "a{}\n", notePath: "# note\nuiux-review: PASS (x)\nvisual-review: PASS (y)\n"}); count(out, "ui-review") != 0 {
		t.Fatalf("row 2: %s", out)
	}
	if code, out := run(t, map[string]string{"src/app.css": "a{}\n", notePath: "# note\nuiux-review: PASS\nvisual-review: FAIL (clipped menu)\n"}); count(out, "ui-review") != 1 || code == 0 {
		t.Fatalf("row 3: %d %s", code, out)
	}
	if _, out := run(t, map[string]string{"src/cart.go": "package cart\n", notePath: "# note\n"}); count(out, "ui-review") != 0 {
		t.Fatalf("row 4: %s", out)
	}
}

// Decision table (the contract's failure section): a change to a layer's
// code owes a failure-review line, backed by a changed test of the layer.
//
//	# | changed                    | note says                         | close            | --strict
//	1 | api code                   | nothing                           | owes the line    | refuses
//	2 | api code + api test        | api PASS (TestDown)               | nothing owed     | passes
//	3 | api code                   | api PASS (TestDown)               | fails (no test)  | refuses
//	4 | api code                   | api n/a — renames a field         | nothing owed     | passes
//	5 | api code                   | api n/a                           | owes the reason  | refuses
//	6 | api code + api test        | api FAIL (panics escape)          | fails            | refuses
//	7 | api code + api test        | api PASS                          | owes the test    | refuses
//	8 | ui code only               | api PASS (TestDown)               | owes ui          | refuses
//	9 | api test only              | nothing                           | nothing owed     | passes
//	10| README only                | nothing                           | nothing owed     | passes
//	11| no failure section, api    | nothing                           | nothing owed     | passes
//	12| api code, layer check fails| api n/a — x                       | check fails      | refuses
func TestFailureReview(t *testing.T) {
	section := `"modules":["handoff"],
 "failure":{"layers":[
  {"name":"api","paths":["api/**"],"tests":["api/**/*_test.go"],"boundary":"errors become a status","check":"test ! -f api/raw"},
  {"name":"ui","paths":["ui/**"],"tests":["ui/**/*.test.js"],"boundary":"a route renders inside its boundary"}]}`
	with := strings.Replace(contractFile, `"modules":["handoff"]`, section, 1)
	run := func(t *testing.T, file string, files map[string]string, strict bool) (int, string) {
		dir := t.TempDir()
		git(t, dir, "init", "-q", "-b", "main")
		write(t, dir, map[string]string{".picode.json": file, "README.md": "r\n", ".gitignore": "runs.log\n"})
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "base")
		git(t, dir, "switch", "-qc", "feat/fr")
		write(t, dir, files)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "change")
		args := []string{"close", "--json"}
		if strict {
			args = append(args, "--strict")
		}
		return flow(t, dir, args...)
	}
	note := "docs/handoff/" + time.Now().Format("2006-01-02") + "-feat-fr.md"
	count := func(out string) int { return strings.Count(out, `"code":"failure-review"`) }
	code := map[string]string{"api/h.go": "package api\n"}
	plus := func(a map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range a {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	rows := []struct {
		name   string
		file   string
		files  map[string]string
		want   int  // failure-review findings
		passes bool // close --strict exits 0
		has    string
	}{
		{"1 missing", with, plus(code, note, "# n\n"), 1, false, "owes"},
		{"2 pass with test", with, plus(code, "api/h_test.go", "package api\n", note, "# n\nfailure-review: api PASS (TestDown)\n"), 0, true, ""},
		{"3 pass without test", with, plus(code, note, "# n\nfailure-review: api PASS (TestDown)\n"), 1, false, "no test of the api layer changed"},
		{"4 n/a with reason", with, plus(code, note, "# n\nfailure-review: api n/a — renames a field\n"), 0, true, ""},
		{"5 n/a without reason", with, plus(code, note, "# n\nfailure-review: api n/a\n"), 1, false, "needs its reason"},
		{"6 fail", with, plus(code, "api/h_test.go", "package api\n", note, "# n\nfailure-review: api FAIL (panics escape)\n"), 1, false, "does not land"},
		{"7 pass without test name", with, plus(code, "api/h_test.go", "package api\n", note, "# n\nfailure-review: api PASS\n"), 1, false, "must name the test"},
		{"8 other layer", with, map[string]string{"ui/app.js": "x\n", note: "# n\nfailure-review: api PASS (TestDown)\n"}, 1, false, "the ui layer"},
		{"9 test only", with, map[string]string{"api/h_test.go": "package api\n", note: "# n\n"}, 0, true, ""},
		{"10 untouched", with, map[string]string{"README.md": "r2\n", note: "# n\n"}, 0, true, ""},
		{"11 no section", contractFile, plus(code, note, "# n\n"), 0, true, ""},
		{"12 layer check", with, plus(code, "api/raw", "x", note, "# n\nfailure-review: api n/a — x\n"), 0, false, "failure-api"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, out := run(t, r.file, r.files, false)
			if count(out) != r.want || (r.has != "" && !strings.Contains(out, r.has)) {
				t.Fatalf("close: %s", out)
			}
			sc, sout := run(t, r.file, r.files, true)
			if (sc == 0) != r.passes {
				t.Fatalf("close --strict exit %d: %s", sc, sout)
			}
		})
	}
}

func TestGoImportersFillsAffected(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, map[string]string{
		".picode.json": `{"extensions":{"picode-devflow":{"flow":1,
 "checks":{"go":{"run":"P='{affected}'; echo \"${P:-./...}\" > pkgs.log","when":["**/*.go"],"affected":"@go-importers"}}}}}`,
		".gitignore": "pkgs.log\n",
		"go.mod":     "module example.com/m\n\ngo 1.22\n",
		"a/a.go":     "package a\n",
		"b/b.go":     "package b\n\nimport _ \"example.com/m/a\"\n",
		"c/c.go":     "package c\n",
	})
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")
	git(t, dir, "switch", "-qc", "feat/a")
	write(t, dir, map[string]string{"a/a.go": "package a\n\nconst X = 1\n"})
	if code, out := flow(t, dir, "check", "--fresh"); code != 0 {
		t.Fatalf("check: %d %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pkgs.log"))
	if got := strings.TrimSpace(string(b)); got != "example.com/m/a example.com/m/b" {
		t.Fatalf("affected = %q", got)
	}
}

// The agents command and the status warning, end to end.
func TestAgentsBlock(t *testing.T) {
	dir := repo(t)
	git(t, dir, "switch", "-qc", "feat/agents")
	hasWarn := func() bool {
		_, out := flow(t, dir, "status", "--json")
		var st statusOut
		json.Unmarshal([]byte(out), &st)
		for _, f := range st.Findings {
			if f.Code == "agents-md" {
				return f.Level == "warn"
			}
		}
		return false
	}
	if hasWarn() {
		t.Fatal("the current block warns")
	}
	// An older v1 block, as the setup wrote it: status warns, write replaces.
	v1 := "<!-- picode-devflow:begin v1 -->\n## How work flows here (Dev flow)\n\n- One worktree per agent; never commit on the main branch in the main checkout.\n- While you work, the Dev flow check tool runs the checks your change needs.\n- At the end, call close and do what it lists as owed; then land.\n- Stage explicit paths, never `git add -A`; never merge by hand.\n- A change that crosses a boundary (protocol, persistence, security model, process) gets a decision record (adr tool).\n- Debts go to docs/handoff/open/<topic>.md with the debt tool; pay them by marking, never by deleting.\n<!-- picode-devflow:end -->\n"
	write(t, dir, map[string]string{"AGENTS.md": "# Agents\n\n" + v1 + "\nTail.\n"})
	if !hasWarn() {
		t.Fatal("an older block must warn")
	}
	if code, out := flow(t, dir, "agents", "--write"); code != 0 || !strings.Contains(out, "Wrote") {
		t.Fatalf("write: %d %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if string(b) != "# Agents\n\n"+agentsmd.Block()+"\nTail.\n" {
		t.Fatalf("replaced:\n%s", b)
	}
	// Edited by hand: reported, never overwritten without force.
	edited := strings.Replace(string(b), "never merge by hand", "merge on Fridays", 1)
	write(t, dir, map[string]string{"AGENTS.md": edited})
	if code, out := flow(t, dir, "agents", "--write"); code != 1 || !strings.Contains(out, "edited by hand") {
		t.Fatalf("edited: %d %s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); string(b) != edited {
		t.Fatal("an edited block was overwritten")
	}
	if code, _ := flow(t, dir, "agents", "--write", "--force"); code != 0 {
		t.Fatal("force must replace")
	}
	if hasWarn() {
		t.Fatal("still warns after force")
	}
}
