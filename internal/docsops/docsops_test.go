package docsops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognixws/cognix-devflow/internal/contract"
	"github.com/cognixws/cognix-devflow/internal/gitx"
)

func c(t *testing.T) *contract.Contract {
	ct, err := contract.Parse([]byte(`{"flow":1}`))
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func TestDebtAddAndPay(t *testing.T) {
	root := t.TempDir()
	ct := c(t)
	rel, err := AddDebt(root, ct, "cart", "Coupons stack with the Pix discount")
	if err != nil || rel != "docs/handoff/open/cart.md" {
		t.Fatal(rel, err)
	}
	AddDebt(root, ct, "cart", "Refunds keep the payment id")
	AddDebt(root, ct, "cart", "Refunds keep the payment id") // idempotent
	b, _ := os.ReadFile(filepath.Join(root, rel))
	if strings.Count(string(b), "- [ ]") != 2 || !strings.Contains(string(b), "## Debts\n\n- [ ] Coupons") {
		t.Fatalf("%s", b)
	}
	if _, err := PayDebt(root, ct, "cart", "Refunds", "fixed in feat/refunds"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, rel))
	if !strings.Contains(string(b), "- [x] Refunds keep the payment id — paid ") || !strings.Contains(string(b), "fixed in feat/refunds") {
		t.Fatalf("%s", b)
	}
	if _, err := PayDebt(root, ct, "cart", "Nothing like this", ""); err == nil {
		t.Fatal("paid a missing item")
	}
	if _, err := AddDebt(root, ct, "Bad Topic", "x"); err == nil {
		t.Fatal("bad topic accepted")
	}
}

func TestDebtUnderExistingSection(t *testing.T) {
	root := t.TempDir()
	ct := c(t)
	p := filepath.Join(root, "docs/handoff/open/t.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# T\n\n## Debts\n\n- [ ] one\n\n## Later\n\n- x\n"), 0o644)
	AddDebt(root, ct, "t", "two")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "- [ ] one\n- [ ] two\n\n## Later") {
		t.Fatalf("%s", b)
	}
}

func TestNewDecisionNumbersAcrossWorktrees(t *testing.T) {
	root := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, b)
		}
	}
	run(root, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(root, "docs/decisions"), 0o755)
	os.WriteFile(filepath.Join(root, "docs/decisions/0001-a.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "docs/decisions/README.md"), []byte("| # |\n"), 0o644)
	run(root, "add", "-A")
	run(root, "commit", "-qm", "a")
	wt := filepath.Join(t.TempDir(), "wt")
	run(root, "worktree", "add", "-q", "-b", "feat/b", wt)
	os.WriteFile(filepath.Join(wt, "docs/decisions/0002-b.md"), []byte("x"), 0o644) // in flight elsewhere
	r, err := gitx.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := NewDecision(r, c(t), "cache-policy", "Cache policy")
	if err != nil || rel != "docs/decisions/0003-cache-policy.md" {
		t.Fatal(rel, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, rel))
	if !strings.HasPrefix(string(b), "# ADR-0003: Cache policy") || !strings.Contains(string(b), "**Boundary**") {
		t.Fatalf("%s", b)
	}
	idx, _ := os.ReadFile(filepath.Join(root, "docs/decisions/README.md"))
	if !strings.Contains(string(idx), "| [0003](0003-cache-policy.md) | Cache policy | proposed |") {
		t.Fatalf("%s", idx)
	}
}
