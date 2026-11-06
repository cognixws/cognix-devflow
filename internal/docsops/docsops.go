// Package docsops writes the flow's records: a debt added to or paid in a
// topic file, and a new decision record with the next free number. It
// writes files only; committing them stays with whoever made the change.
package docsops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cognixws/cognix-devflow/internal/contract"
	"github.com/cognixws/cognix-devflow/internal/gitx"
)

var topicName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// AddDebt appends an open item under "## Debts" in topics/<topic>.md,
// creating the file or the heading when missing. It returns the file.
func AddDebt(root string, c *contract.Contract, topic, text string) (string, error) {
	if !topicName.MatchString(topic) {
		return "", fmt.Errorf("topic %q: use lower-case letters, digits and dashes", topic)
	}
	text = oneLine(text)
	if text == "" {
		return "", errors.New("the debt needs a sentence")
	}
	rel := filepath.ToSlash(filepath.Join(c.Docs.Topics, topic+".md"))
	full := filepath.Join(root, rel)
	b, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		b = []byte("# " + topic + "\n\n## Next\n\n## Debts\n")
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	s := string(b)
	item := "- [ ] " + text
	if strings.Contains(s, item) {
		return rel, nil
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	at := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## Debts" {
			at = i
		}
	}
	if at < 0 {
		lines = append(lines, "", "## Debts", "", item)
	} else {
		end := len(lines)
		for i := at + 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "## ") {
				end = i
				break
			}
		}
		// insert after the last non-blank line of the section
		ins := end
		for ins > at+1 && strings.TrimSpace(lines[ins-1]) == "" {
			ins--
		}
		if ins == at+1 {
			lines = insert(lines, ins, "", item)
		} else {
			lines = insert(lines, ins, item)
		}
	}
	return rel, os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// PayDebt marks the open item that starts with text as paid, keeping the
// line. It refuses when none or several match.
func PayDebt(root string, c *contract.Contract, topic, text, note string) (string, error) {
	if !topicName.MatchString(topic) {
		return "", fmt.Errorf("topic %q: use lower-case letters, digits and dashes", topic)
	}
	text = oneLine(text)
	rel := filepath.ToSlash(filepath.Join(c.Docs.Topics, topic+".md"))
	full := filepath.Join(root, rel)
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(b), "\n")
	hit := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "- [ ] ") && strings.HasPrefix(strings.TrimPrefix(t, "- [ ] "), text) {
			if hit >= 0 {
				return "", fmt.Errorf("more than one open item starts with %q; quote more of it", text)
			}
			hit = i
		}
	}
	if hit < 0 {
		return "", fmt.Errorf("no open item in %s starts with %q", rel, text)
	}
	paid := "paid " + time.Now().Format("2006-01-02")
	if n := oneLine(note); n != "" {
		paid += ": " + n
	}
	lines[hit] = strings.Replace(lines[hit], "- [ ] ", "- [x] ", 1) + " — " + paid
	return rel, os.WriteFile(full, []byte(strings.Join(lines, "\n")), 0o644)
}

var adrNum = regexp.MustCompile(`^(\d{4})-`)
var adrName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// NewDecision seeds decisions/NNNN-<name>.md with the next number free in
// this checkout, every worktree of the repository and the main branch, and
// appends its row to the index when the folder has a README.md.
func NewDecision(r *gitx.Repo, c *contract.Contract, name, title string) (string, error) {
	if !adrName.MatchString(name) {
		return "", fmt.Errorf("name %q: use lower-case letters, digits and dashes", name)
	}
	dir := strings.TrimSuffix(c.Docs.Decisions, "/")
	used := map[int]bool{}
	scan := func(root string) {
		ents, _ := os.ReadDir(filepath.Join(root, dir))
		for _, e := range ents {
			if m := adrNum.FindStringSubmatch(e.Name()); m != nil {
				n, _ := strconv.Atoi(m[1])
				used[n] = true
			}
		}
	}
	scan(r.Root)
	if out, err := r.Git("worktree", "list", "--porcelain"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if p, ok := strings.CutPrefix(l, "worktree "); ok {
				scan(p)
			}
		}
	}
	if out, err := r.Git("ls-tree", "--name-only", c.Main, dir+"/"); err == nil {
		for _, f := range strings.Split(out, "\n") {
			if m := adrNum.FindStringSubmatch(filepath.Base(f)); m != nil {
				n, _ := strconv.Atoi(m[1])
				used[n] = true
			}
		}
	}
	next := 1
	nums := make([]int, 0, len(used))
	for n := range used {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	if len(nums) > 0 {
		next = nums[len(nums)-1] + 1
	}
	num := fmt.Sprintf("%04d", next)
	file := num + "-" + name + ".md"
	rel := dir + "/" + file
	if title = oneLine(title); title == "" {
		title = strings.ReplaceAll(name, "-", " ")
	}
	body := template(r.Root, dir)
	body = strings.ReplaceAll(body, "ADR-NNNN", "ADR-"+num)
	body = strings.Replace(body, "ADR-"+num+": Title", "ADR-"+num+": "+title, 1)
	body = strings.ReplaceAll(body, "YYYY-MM-DD", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(filepath.Join(r.Root, dir), 0o755); err != nil {
		return "", err
	}
	full := filepath.Join(r.Root, rel)
	if _, err := os.Stat(full); err == nil {
		return "", fmt.Errorf("%s already exists", rel)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		return "", err
	}
	idx := filepath.Join(r.Root, dir, "README.md")
	if b, err := os.ReadFile(idx); err == nil {
		row := fmt.Sprintf("| [%s](%s) | %s | proposed |\n", num, file, title)
		s := strings.TrimRight(string(b), "\n") + "\n" + row
		if err := os.WriteFile(idx, []byte(s), 0o644); err != nil {
			return "", err
		}
	}
	return rel, nil
}

const defaultTemplate = `# ADR-NNNN: Title

- **Status**: proposed
- **Date**: YYYY-MM-DD
- **Boundary**: protocol | persistence | security model | process — which one, and what crosses it

## Context

What forces are in play? What problem demands a decision? Facts, options,
constraints — not opinions.

## Decision

What we will do, stated in the present tense, one paragraph, no hedging.

## Consequences

What becomes easier, what becomes harder, what we accept as cost.

## Alternatives considered

Each alternative and the specific reason it lost.
`

// template uses the project's own template.md (the part below its first
// "---" rule when it has one), else the built-in one.
func template(root, dir string) string {
	b, err := os.ReadFile(filepath.Join(root, dir, "template.md"))
	if err != nil {
		return defaultTemplate
	}
	s := string(b)
	if i := strings.Index(s, "\n---\n"); i >= 0 {
		s = strings.TrimLeft(s[i+5:], "\n")
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func insert(s []string, at int, items ...string) []string {
	out := append([]string{}, s[:at]...)
	out = append(out, items...)
	return append(out, s[at:]...)
}
