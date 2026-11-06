package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cognixws/picode-devflow/internal/agentsmd"
	"github.com/cognixws/picode-devflow/internal/contract"
	"github.com/cognixws/picode-devflow/internal/gitx"
	"github.com/cognixws/picode-devflow/internal/goaffected"
	"github.com/cognixws/picode-devflow/internal/reuse"
	"github.com/cognixws/picode-devflow/internal/runner"
	"github.com/cognixws/picode-devflow/internal/scope"
	"github.com/cognixws/picode-devflow/internal/validate"
)

// session is one checkout with its contract and its change against main.
type session struct {
	repo     *gitx.Repo
	c        *contract.Contract
	branch   string
	mainRev  string
	base     string
	head     string
	ahead    int
	behind   int
	dirty    []string
	changed  []string // base..head
	selected scope.Selection
}

func repoRoot(dir string) (string, error) {
	r, err := gitx.Open(dir)
	if err != nil {
		return "", err
	}
	return r.Root, nil
}

func openSession(withUncommitted bool) (*session, error) {
	r, err := gitx.Open(".")
	if err != nil {
		return nil, err
	}
	c, err := contract.Load(r.Root)
	if err != nil {
		return nil, err
	}
	s := &session{repo: r, c: c, branch: r.Branch()}
	if s.head, err = r.Head(); err != nil {
		return nil, fmt.Errorf("no commit yet: %w", err)
	}
	if s.mainRev, err = r.Resolve(c.Main); err != nil || s.mainRev == "" {
		return nil, fmt.Errorf("the main branch %q was not found (set \"main\" in the contract)", c.Main)
	}
	if s.base, err = r.MergeBase(s.mainRev, s.head); err != nil {
		return nil, err
	}
	s.ahead, s.behind, _ = r.AheadBehind(s.mainRev, s.head)
	if s.dirty, err = r.Uncommitted(); err != nil {
		return nil, err
	}
	if s.changed, err = r.Changed(s.base, s.head); err != nil {
		return nil, err
	}
	paths := s.changed
	if withUncommitted {
		paths = append(append([]string{}, s.changed...), s.dirty...)
	}
	s.selected = scope.Select(c, paths)
	if len(paths) == 0 {
		// Nothing changed is not a reason to run everything while iterating.
		s.selected = scope.Selection{Checks: []string{}, Reasons: []string{"no change against " + c.Main}}
	}
	return s, nil
}

func (s *session) dataDir() string { return filepath.Join(s.repo.CommonDir, "picode-devflow") }

func (s *session) slug() string {
	if s.branch == "" {
		return "detached"
	}
	return validate.Slug(s.branch)
}

// command resolves a check name ("full" included) to its command line.
func (s *session) command(name string) (contract.Check, bool) {
	if name == "full" && s.c.Full != "" {
		return contract.Check{Run: s.c.Full}, true
	}
	ch, ok := s.c.Checks[name]
	return ch, ok
}

type checkOut struct {
	runner.Result
	Skipped string `json:"skipped,omitempty"`
}

// runChecks runs names in order. Reuse applies only to a clean checkout.
func (s *session) runChecks(names []string, allowReuse bool, changed []string) []checkOut {
	clean := len(s.dirty) == 0
	store := reuse.Open(s.repo.CommonDir)
	quar := map[string]bool{}
	if s.c.Quarantine != "" {
		quar = runner.ParseQuarantine(filepath.Join(s.repo.Root, s.c.Quarantine))
	}
	listFile := filepath.Join(s.dataDir(), "changed-"+s.slug()+".txt")
	_ = os.MkdirAll(s.dataDir(), 0o755)
	_ = os.WriteFile(listFile, []byte(strings.Join(changed, "\n")+"\n"), 0o644)
	var out []checkOut
	for _, name := range names {
		ch, ok := s.command(name)
		if !ok {
			out = append(out, checkOut{Result: runner.Result{Name: name}, Skipped: "not declared"})
			continue
		}
		var key string
		if clean {
			var patterns []string
			if name != "full" {
				patterns = ch.When
			}
			if ck, err := s.repo.ContentKey(s.head, patterns); err == nil {
				key = reuse.Key(name, ch.Run, s.c.Digest(), ck)
			}
		}
		if allowReuse && key != "" {
			if st, ok := store.Find(key); ok {
				out = append(out, checkOut{Result: runner.Result{Name: name, OK: true, Reused: true, Tail: "passed on the same content at " + short(st.Rev) + ", " + st.At.Format(time.RFC3339)}})
				continue
			}
		}
		cmdline, err := s.expand(ch, changed)
		if err != nil {
			out = append(out, checkOut{Result: runner.Result{Name: name, Tail: err.Error()}})
			continue
		}
		timeout := 45 * time.Minute
		if ch.TimeoutMin > 0 {
			timeout = time.Duration(ch.TimeoutMin) * time.Minute
		}
		res := runner.Run(context.Background(), runner.Spec{
			Name: name, Command: cmdline, Dir: s.repo.Root,
			LogPath: filepath.Join(s.dataDir(), "logs", s.slug(), name+".log"),
			JUnit:   ch.JUnit, Timeout: timeout, Quarantine: quar,
			Env: []string{"FLOW_BASE=" + s.base, "FLOW_HEAD=" + s.head, "FLOW_CHANGED_FILE=" + listFile},
		})
		if res.OK && key != "" {
			_ = store.Record(reuse.Stamp{Key: key, Check: name, Rev: s.head, At: time.Now().UTC()})
		}
		if len(res.Flaky) > 0 {
			s.recordFlakes(name, res.Flaky)
		}
		out = append(out, checkOut{Result: res})
	}
	return out
}

// expand fills {affected} with the output of the check's affected command,
// which reads the changed paths on stdin; "@go-importers" instead answers
// the changed Go packages and every package of the module importing them.
func (s *session) expand(ch contract.Check, changed []string) (string, error) {
	if !strings.Contains(ch.Run, "{affected}") {
		return ch.Run, nil
	}
	if ch.Affected == "" {
		return strings.ReplaceAll(ch.Run, "{affected}", ""), nil
	}
	if ch.Affected == goaffected.Keyword {
		pkgs, err := goaffected.Packages(s.repo.Root, changed)
		if err != nil {
			return "", fmt.Errorf("affected packages: %w", err)
		}
		return strings.ReplaceAll(ch.Run, "{affected}", strings.Join(pkgs, " ")), nil
	}
	cmd := exec.Command("/bin/sh", "-c", ch.Affected)
	cmd.Dir = s.repo.Root
	cmd.Stdin = strings.NewReader(strings.Join(changed, "\n") + "\n")
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("affected command failed: %w", err)
	}
	return strings.ReplaceAll(ch.Run, "{affected}", strings.Join(strings.Fields(string(b)), " ")), nil
}

func (s *session) recordFlakes(check string, tests []string) {
	f, err := os.OpenFile(filepath.Join(s.dataDir(), "flakes.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"at": time.Now().UTC(), "branch": s.branch, "check": check, "tests": tests})
	f.Write(append(b, '\n'))
}

func (s *session) findings(withGenerated bool) ([]validate.Finding, error) {
	fs, err := validate.Run(validate.Input{
		Repo: s.repo, Contract: s.c, Base: s.base, Head: s.head, Branch: s.branch,
		Date: time.Now().Format("2006-01-02"),
	}, withGenerated)
	if err != nil {
		return nil, err
	}
	// Git protection turned on here and reset by a tool since (husky's
	// prepare does it on npm install): say so on every status and close.
	if st, herr := readHooks(s.repo); herr == nil && st.Reset {
		fs = append(fs, validate.Finding{Level: validate.Warn, Code: "hooks-reset", Message: "Git protection was turned on in this clone, but core.hooksPath now points elsewhere", Fix: "run `flow hooks on` again, or turn it on from the Dev flow page"})
	}
	if f, ok := agentsFinding(s.repo.Root); ok {
		fs = append(fs, f)
	}
	return fs, nil
}

func short(rev string) string {
	if len(rev) > 9 {
		return rev[:9]
	}
	return rev
}

func countLevel(fs []validate.Finding, level string) int {
	n := 0
	for _, f := range fs {
		if f.Level == level {
			n++
		}
	}
	return n
}

func sortedFindings(fs []validate.Finding) []validate.Finding {
	rank := map[string]int{validate.Fail: 0, validate.Owed: 1, validate.Warn: 2}
	sort.SliceStable(fs, func(i, j int) bool { return rank[fs[i].Level] < rank[fs[j].Level] })
	return fs
}

// agentsFinding says, never refusing, when AGENTS.md lacks the Dev flow
// block, holds an older one, or holds one a person edited.
func agentsFinding(root string) (validate.Finding, bool) {
	b, err := os.ReadFile(filepath.Join(root, agentsmd.File))
	if err != nil && !os.IsNotExist(err) {
		return validate.Finding{}, false
	}
	f := validate.Finding{Level: validate.Warn, Code: "agents-md"}
	switch agentsmd.Inspect(string(b)).State {
	case agentsmd.Missing:
		f.Message, f.Fix = "AGENTS.md has no Dev flow block, so agent CLIs don't read how work flows here", "call the agents tool with write=true (flow agents --write), then commit AGENTS.md"
	case agentsmd.Older:
		f.Message, f.Fix = "AGENTS.md holds an older Dev flow block", "call the agents tool with write=true (flow agents --write) to replace it, then commit AGENTS.md"
	case agentsmd.Edited:
		f.Message, f.Fix = "The Dev flow block in AGENTS.md was edited by hand, so the flow won't replace it", "keep it and mark it yours (begin marker `custom`), or replace it with write=true and force=true"
	case agentsmd.Broken:
		f.Message, f.Fix = "The Dev flow block markers in AGENTS.md are broken", "leave one begin and one end marker"
	default:
		return f, false
	}
	return f, true
}
