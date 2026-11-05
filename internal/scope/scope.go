// Package scope turns a change set into the checks it needs. It fails safe:
// an empty change set, a path in "everything", or a path no check covers
// selects the full gate.
package scope

import (
	"sort"
	"strconv"
	"strings"

	"github.com/cognixws/picode-devflow/internal/contract"
	"github.com/cognixws/picode-devflow/internal/glob"
)

// Selection is what a change set needs.
type Selection struct {
	Full      bool     `json:"full"`
	Reasons   []string `json:"reasons,omitempty"`
	Checks    []string `json:"checks"`
	Uncovered []string `json:"uncovered,omitempty"`
	Paths     int      `json:"paths"`
}

// Select classifies paths (slash-separated, relative to the repository root).
func Select(c *contract.Contract, paths []string) Selection {
	paths = clean(paths)
	s := Selection{Paths: len(paths), Checks: []string{}}
	if len(paths) == 0 {
		s.Full = true
		s.Reasons = append(s.Reasons, "empty change set")
		s.Checks = Full(c)
		return s
	}
	picked := map[string]bool{}
	for _, p := range paths {
		if glob.Any(c.Everything, p) {
			s.Full = true
			s.Reasons = append(s.Reasons, p+" is in everything")
			continue
		}
		covered := false
		for name, ch := range c.Checks {
			if ch.FullOnly() {
				continue
			}
			if len(ch.When) == 0 || glob.Any(ch.When, p) {
				picked[name] = true
				covered = true
			}
		}
		if !covered {
			s.Uncovered = append(s.Uncovered, p)
		}
	}
	if len(s.Uncovered) > 0 {
		s.Full = true
		s.Reasons = append(s.Reasons, plural(len(s.Uncovered), "path no check covers", "paths no check covers"))
	}
	if s.Full {
		s.Checks = Full(c)
		return s
	}
	for n := range picked {
		s.Checks = append(s.Checks, n)
	}
	sort.Strings(s.Checks)
	return s
}

// Full lists what the full gate runs: the "full" command when declared
// (named "full"), else every check.
func Full(c *contract.Contract) []string {
	if c.Full != "" {
		return []string{"full"}
	}
	return c.CheckNames()
}

func clean(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		p = strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(p, "\\", "/")), "./")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
