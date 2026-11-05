// Package glob matches slash-separated repository paths against patterns
// anchored at the repository root. A segment "**" matches zero or more
// segments; every other segment follows path.Match (*, ?, [...]), which
// never crosses a slash. "*.md" therefore matches only root files and
// "**/*.md" matches at any depth.
package glob

import (
	"path"
	"strings"
)

// Match reports whether name matches pattern. A malformed pattern never
// matches; Valid reports it.
func Match(pattern, name string) bool {
	return match(split(pattern), split(name))
}

// Valid reports whether every segment of pattern is well formed.
func Valid(pattern string) bool {
	if pattern == "" || strings.HasPrefix(pattern, "/") {
		return false
	}
	for _, seg := range split(pattern) {
		if seg == "**" {
			continue
		}
		if strings.Contains(seg, "**") {
			return false
		}
		if _, err := path.Match(seg, ""); err != nil {
			return false
		}
	}
	return true
}

// Any reports whether name matches one of patterns.
func Any(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

func split(s string) []string {
	s = strings.Trim(strings.ReplaceAll(s, "\\", "/"), "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func match(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if match(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
