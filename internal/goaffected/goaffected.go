// Package goaffected turns changed paths into the Go packages a test run
// needs: the packages that hold them and every package of the module that
// imports one of those, directly or not, its tests included. A contract
// asks for it with "affected": "@go-importers".
package goaffected

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// Keyword is the value of checks.<name>.affected that selects this package
// instead of a shell command.
const Keyword = "@go-importers"

// Packages answers the import paths to test for changed paths
// (slash-separated, relative to root), sorted. Nothing resolved answers
// none: the check's own fallback (often ./...) then decides.
func Packages(root string, changed []string) ([]string, error) {
	start := map[string]bool{}
	for _, p := range changed {
		p = strings.TrimPrefix(strings.TrimSpace(p), "./")
		if p == "" {
			continue
		}
		// Walk up from the path's folder: a changed embedded file (an SQL
		// migration, a template) belongs to the nearest package above it.
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			if pkg, ok := list(root, "./"+d); ok {
				start[pkg] = true
				break
			}
		}
		if path.Dir(p) == "." && strings.HasSuffix(p, ".go") {
			if pkg, ok := list(root, "."); ok {
				start[pkg] = true
			}
		}
	}
	if len(start) == 0 {
		return nil, nil
	}
	cmd := exec.Command("go", "list", "-e", "-f",
		"{{.ImportPath}} {{join .Deps \" \"}} {{join .TestImports \" \"}} {{join .XTestImports \" \"}}", "./...")
	cmd.Dir = root
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	picked := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		for _, x := range f {
			if start[x] {
				picked[f[0]] = true
				break
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	pkgs := make([]string, 0, len(picked))
	for p := range picked {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

func list(root, pattern string) (string, bool) {
	cmd := exec.Command("go", "list", pattern)
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	return s, s != "" && !strings.Contains(s, "\n")
}
