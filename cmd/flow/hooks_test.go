package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitE runs git and returns its error output, for refusals.
func gitE(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e"), env...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// Decision table (ADR-0401 D6):
//
//	# | where                    | change                          | outcome
//	1 | main checkout on main    | code                            | refused
//	2 | main checkout on main    | docs only                       | allowed; the project's own hook still runs
//	3 | worktree on a branch     | code                            | allowed; the project's hook runs
//	4 | main checkout off main   | anything                        | refused
//	5 | any                      | main moved to a non-descendant  | refused (FLOW_ALLOW_MAIN_REWIND=1 allows)
//	6 | changelog module on      | CHANGELOG.md staged             | refused
//	7 | off                      | —                               | previous core.hooksPath restored
//	8 | on, then a tool resets   | core.hooksPath                  | check reports it (exit 1)
//	9 | on again after a reset   | the tool wrote a new path       | protection on; the tool's hooks chain from the new path
func TestGitProtection(t *testing.T) {
	exe, _ := os.Executable()
	hooks := t.TempDir()
	for _, h := range hookNames {
		script := "#!/bin/sh\nFLOW_TEST_AS_CLI=1 exec '" + exe + "' hook " + h + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(hooks, h), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FLOW_HOOKS_DIR", hooks)
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, map[string]string{
		".picode.json":        `{"extensions":{"picode-devflow":{"flow":1,"modules":["changelog"]}}}`,
		"README.md":           "r\n",
		"CHANGELOG.md":        "# c\n",
		".husky/_/pre-commit": "#!/bin/sh\necho ran >> \"$(git rev-parse --show-toplevel)/husky.log\"\n",
	})
	os.Chmod(filepath.Join(dir, ".husky/_/pre-commit"), 0o755)
	git(t, dir, "add", ".picode.json", "README.md", "CHANGELOG.md", ".husky/_/pre-commit")
	git(t, dir, "commit", "-qm", "base")
	git(t, dir, "config", "core.hooksPath", ".husky/_")

	if code, out := flow(t, dir, "hooks", "on"); code != 0 || !strings.Contains(out, "is on") {
		t.Fatalf("on: %d %s", code, out)
	}
	write(t, dir, map[string]string{"src/a.go": "package a\n"})
	git(t, dir, "add", "src/a.go")
	if out, err := gitE(t, dir, nil, "commit", "-qm", "code on main"); err == nil || !strings.Contains(out, "is code on main") { // row 1
		t.Fatalf("row 1: %v %s", err, out)
	}
	git(t, dir, "reset", "-q", "src/a.go")
	write(t, dir, map[string]string{"docs/x.md": "x\n"})
	git(t, dir, "add", "docs/x.md")
	if out, err := gitE(t, dir, nil, "commit", "-qm", "docs"); err != nil { // row 2
		t.Fatalf("row 2: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "husky.log")); !strings.Contains(string(b), "ran") {
		t.Fatal("row 2: the project's own hook did not run")
	}

	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "feat/a", wt)
	write(t, wt, map[string]string{"src/a.go": "package a\n"})
	git(t, wt, "add", "src/a.go")
	if out, err := gitE(t, wt, nil, "commit", "-qm", "code in a worktree"); err != nil { // row 3
		t.Fatalf("row 3: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "husky.log")); !strings.Contains(string(b), "ran") {
		t.Fatal("row 3: the project's hook did not run in the worktree")
	}

	git(t, dir, "switch", "-qc", "side")
	write(t, dir, map[string]string{"docs/y.md": "y\n"})
	git(t, dir, "add", "docs/y.md")
	if out, err := gitE(t, dir, nil, "commit", "-qm", "off main"); err == nil || !strings.Contains(out, "stays on main") { // row 4
		t.Fatalf("row 4: %v %s", err, out)
	}
	git(t, dir, "reset", "-q", "docs/y.md")
	git(t, dir, "switch", "-q", "main")

	if out, err := gitE(t, dir, nil, "update-ref", "refs/heads/main", "HEAD~1"); err == nil || !strings.Contains(out, "rewind main") { // row 5
		t.Fatalf("row 5: %v %s", err, out)
	}
	if out, err := gitE(t, dir, []string{"FLOW_ALLOW_MAIN_REWIND=1"}, "update-ref", "refs/heads/main", "HEAD~1"); err != nil {
		t.Fatalf("row 5 override: %v %s", err, out)
	}
	git(t, dir, "reset", "-q", "--hard", "HEAD@{1}")

	write(t, dir, map[string]string{"CHANGELOG.md": "# c\n- edit\n"})
	git(t, dir, "add", "CHANGELOG.md")
	if out, err := gitE(t, dir, nil, "commit", "-qm", "changelog"); err == nil || !strings.Contains(out, "assembled from fragments") { // row 6
		t.Fatalf("row 6: %v %s", err, out)
	}
	git(t, dir, "reset", "-q", "CHANGELOG.md")

	git(t, dir, "config", "core.hooksPath", ".husky/_")                                                      // a tool resets it
	if code, out := flow(t, dir, "hooks", "check"); code != 1 || !strings.Contains(out, "a tool reset it") { // row 8
		t.Fatalf("row 8: %d %s", code, out)
	}
	flow(t, dir, "hooks", "on")
	if code, _ := flow(t, dir, "hooks", "off"); code != 0 { // row 7
		t.Fatal("off failed")
	}
	if got := git(t, dir, "config", "core.hooksPath"); got != ".husky/_" {
		t.Fatalf("row 7: hooksPath = %q", got)
	}
	if _, err := gitE(t, dir, nil, "config", "--get", prevKey); err == nil {
		t.Fatal("row 7: the remembered path stays behind")
	}

	// Row 9: protection on, then a tool moves core.hooksPath to a new
	// folder; turning it on again chains the hooks from the new folder.
	flow(t, dir, "hooks", "on")
	write(t, dir, map[string]string{".hooks2/pre-commit": "#!/bin/sh\necho two >> \"$(git rev-parse --show-toplevel)/two.log\"\n"})
	os.Chmod(filepath.Join(dir, ".hooks2/pre-commit"), 0o755)
	git(t, dir, "config", "core.hooksPath", ".hooks2")
	if code, out := flow(t, dir, "hooks", "on"); code != 0 || !strings.Contains(out, "is on") {
		t.Fatalf("row 9 on: %d %s", code, out)
	}
	if got := git(t, dir, "config", prevKey); got != ".hooks2" {
		t.Fatalf("row 9: remembered %q", got)
	}
	write(t, dir, map[string]string{"docs/z.md": "z\n"})
	git(t, dir, "add", "docs/z.md")
	if out, err := gitE(t, dir, nil, "commit", "-qm", "docs again"); err != nil {
		t.Fatalf("row 9 commit: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "two.log")); !strings.Contains(string(b), "two") {
		t.Fatal("row 9: the tool's hook from its new folder did not run")
	}
}
