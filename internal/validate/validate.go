// Package validate holds the process checks that need no stack: whitespace
// and conflict markers, the project's rules, the handoff note, debts
// deleted instead of paid, direct changelog edits, decision records and
// generated files. Each finding says what to do instead.
package validate

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/cognixws/picode-devflow/internal/contract"
	"github.com/cognixws/picode-devflow/internal/gitx"
	"github.com/cognixws/picode-devflow/internal/glob"
)

// Level of a finding.
const (
	Fail = "fail" // close refuses
	Owed = "owed" // still to write before landing
	Warn = "warn" // shown, never refuses
)

// Finding is one result.
type Finding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Input is what the validators look at: the branch's change, base..head.
type Input struct {
	Repo     *gitx.Repo
	Contract *contract.Contract
	Base     string // merge base with main
	Head     string
	Branch   string
	Date     string // YYYY-MM-DD for the expected note name
}

// Run runs every validator; generated runs only when withGenerated.
func Run(in Input, withGenerated bool) ([]Finding, error) {
	changed, err := in.Repo.Changed(in.Base, in.Head)
	if err != nil {
		return nil, err
	}
	written, err := writtenPaths(in.Repo, in.Base, in.Head)
	if err != nil {
		return nil, err
	}
	var out []Finding
	out = append(out, diffCheck(in)...)
	out = append(out, rules(in.Contract, changed, written)...)
	c := in.Contract
	if c.HasModule("handoff") {
		out = append(out, note(in, changed)...)
		d, err := debts(in)
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	if c.HasModule("changelog") {
		out = append(out, changelog(c, changed)...)
	}
	if c.HasModule("decisions") {
		out = append(out, decisions(in, written)...)
	}
	if withGenerated {
		out = append(out, generated(in)...)
	}
	return out, nil
}

// Slug is the branch name as it appears in file names.
func Slug(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

func writtenPaths(r *gitx.Repo, base, head string) ([]string, error) {
	out, err := r.Git("diff", "--name-only", "--no-renames", "--diff-filter=ACMRT", base, head)
	if err != nil {
		return nil, err
	}
	var ps []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ps = append(ps, l)
		}
	}
	return ps, nil
}

func diffCheck(in Input) []Finding {
	out, ok := in.Repo.DiffCheck(in.Base, in.Head)
	if ok {
		return nil
	}
	first := strings.SplitN(out, "\n", 6)
	if len(first) > 5 {
		first = first[:5]
	}
	return []Finding{{Level: Fail, Code: "diff-check", Message: "whitespace errors or conflict markers:\n" + strings.Join(first, "\n"), Fix: "fix those lines and commit"}}
}

func rules(c *contract.Contract, changed, written []string) []Finding {
	var out []Finding
	for _, r := range c.Rules {
		if len(r.Need) > 0 {
			var trig string
			for _, p := range changed {
				if glob.Any(r.When, p) {
					trig = p
					break
				}
			}
			if trig == "" {
				continue
			}
			met := false
			for _, p := range changed {
				if glob.Any(r.Need, p) {
					met = true
					break
				}
			}
			if !met {
				out = append(out, Finding{Level: Owed, Code: "rule-need", Message: fmt.Sprintf("%s changed, so the change also needs %s (%s)", trig, strings.Join(r.Need, " or "), r.Why), Fix: "update " + strings.Join(r.Need, " or ")})
			}
			continue
		}
		for _, p := range written {
			if glob.Any(r.Refuse, p) && !glob.Any(r.Unless, p) {
				out = append(out, Finding{Level: Fail, Code: "rule-refuse", Message: fmt.Sprintf("%s may not be committed: %s", p, r.Why), Fix: "remove it from the branch's history"})
			}
		}
	}
	return out
}

func note(in Input, changed []string) []Finding {
	c := in.Contract
	if in.Branch == "" || len(changed) == 0 {
		return nil // nothing to record yet
	}
	slug := Slug(in.Branch)
	dir := strings.TrimSuffix(c.Docs.Notes, "/")
	var found string
	for _, p := range changed {
		if path.Dir(p) == dir && strings.HasSuffix(p, "-"+slug+".md") {
			found = p
		}
	}
	if found == "" {
		return []Finding{{Level: Owed, Code: "note", Message: fmt.Sprintf("no handoff note: %s/%s-%s.md (%d lines at most)", dir, in.Date, slug, c.NoteMaxLines), Fix: "write it from this summary; in a subagent if your CLI has them"}}
	}
	b, err := in.Repo.Show(in.Head, found)
	if err != nil {
		return nil
	}
	n := len(strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
	if n > c.NoteMaxLines {
		return []Finding{{Level: Fail, Code: "note-length", Message: fmt.Sprintf("%s has %d lines; the limit is %d", found, n, c.NoteMaxLines), Fix: "move durable items to " + c.Docs.Topics + "/<topic>.md"}}
	}
	return nil
}

var openItem = regexp.MustCompile(`^\s*- \[ \] (.+)$`)
var anyItem = regexp.MustCompile(`^\s*- \[[ xX]\] (.+)$`)

func debts(in Input) ([]Finding, error) {
	topics := strings.TrimSuffix(in.Contract.Docs.Topics, "/")
	added, removed, err := in.Repo.DiffLines(in.Base, in.Head, topics)
	if err != nil {
		return nil, err
	}
	var out []Finding
	files := make([]string, 0, len(removed))
	for f := range removed {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		var kept []string
		for _, l := range added[f] {
			if m := anyItem.FindStringSubmatch(l); m != nil {
				kept = append(kept, strings.TrimSpace(m[1]))
			}
		}
		for _, l := range removed[f] {
			m := openItem.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			k := key(m[1])
			ok := false
			for _, a := range kept {
				if strings.HasPrefix(a, k) {
					ok = true
					break
				}
			}
			if !ok {
				out = append(out, Finding{Level: Fail, Code: "debt-deleted", Message: fmt.Sprintf("%s: an open item was deleted: %q", f, trim(m[1], 70)), Fix: "mark it paid (- [x]) instead of deleting it"})
			}
		}
	}
	return out, nil
}

// key is an item's first 40 characters: a kept item that starts with it
// matches, so a paid item that gained " — paid in …" still counts.
func key(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func changelog(c *contract.Contract, changed []string) []Finding {
	for _, p := range changed {
		if p == "CHANGELOG.md" {
			return []Finding{{Level: Fail, Code: "changelog-direct", Message: "CHANGELOG.md was edited directly", Fix: "write a fragment in " + c.Docs.Changelog + "/<branch>.md instead"}}
		}
	}
	return nil
}

var adrFile = regexp.MustCompile(`^(\d{4})-[a-z0-9-]+\.md$`)

func decisions(in Input, written []string) []Finding {
	dir := strings.TrimSuffix(in.Contract.Docs.Decisions, "/")
	var out []Finding
	for _, p := range written {
		if path.Dir(p) != dir || !adrFile.MatchString(path.Base(p)) {
			continue
		}
		if _, err := in.Repo.Show(in.Base, p); err == nil {
			continue // existing record amended, not new
		}
		b, err := in.Repo.Show(in.Head, p)
		if err != nil {
			continue
		}
		text := string(b)
		line := ""
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(l, "**Boundary**") {
				line = l
				break
			}
		}
		if line == "" || strings.Contains(line, "protocol | persistence | security model | process —") {
			out = append(out, Finding{Level: Fail, Code: "adr-boundary", Message: p + " has no Boundary line filled in", Fix: "name the boundary it crosses; no boundary means it is not a decision record"})
		}
		num := adrFile.FindStringSubmatch(path.Base(p))[1]
		if idx, err := in.Repo.Show(in.Head, dir+"/README.md"); err == nil && !strings.Contains(string(idx), path.Base(p)) {
			out = append(out, Finding{Level: Owed, Code: "adr-index", Message: p + " is not in " + dir + "/README.md", Fix: "add its row to the index"})
		}
		if ls, err := in.Repo.Git("ls-tree", "--name-only", in.Head, dir+"/"); err == nil {
			n := 0
			for _, f := range strings.Split(ls, "\n") {
				if strings.HasPrefix(path.Base(f), num+"-") {
					n++
				}
			}
			if n > 1 {
				out = append(out, Finding{Level: Fail, Code: "adr-number", Message: "two decision records use number " + num, Fix: "renumber yours to the next free number"})
			}
		}
	}
	return out
}

// generated runs each generator and reports files it changed.
func generated(in Input) []Finding {
	var out []Finding
	for _, g := range in.Contract.Generated {
		cmd := exec.Command("/bin/sh", "-c", g.Run)
		cmd.Dir = in.Repo.Root
		cmd.Env = os.Environ()
		if b, err := cmd.CombinedOutput(); err != nil {
			out = append(out, Finding{Level: Fail, Code: "generated-run", Message: fmt.Sprintf("%s failed: %s", g.Run, trim(strings.TrimSpace(string(b)), 300))})
			continue
		}
		st, err := in.Repo.Git("status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			continue
		}
		var stale []string
		for _, l := range strings.Split(st, "\n") {
			if len(l) > 3 && glob.Any(g.Files, strings.TrimSpace(l[3:])) {
				stale = append(stale, strings.TrimSpace(l[3:]))
			}
		}
		if len(stale) > 0 {
			out = append(out, Finding{Level: Fail, Code: "generated-stale", Message: fmt.Sprintf("%s changed %s", g.Run, strings.Join(stale, ", ")), Fix: "commit the regenerated files"})
		}
	}
	return out
}
