// Package gitx is the little of git the flow needs, through the git binary
// with prompts and optional locks off.
package gitx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cognixws/picode-devflow/internal/glob"
)

// Repo is a checkout: the main one or a worktree.
type Repo struct {
	Root      string // top level of this checkout
	CommonDir string // the shared .git directory
	GitDir    string // this checkout's git directory
}

// Open finds the checkout that holds dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel", "--git-common-dir", "--git-dir")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		return nil, errors.New("git rev-parse: unexpected output")
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Clean(filepath.Join(dir, p))
	}
	root := lines[0]
	return &Repo{Root: root, CommonDir: abs(lines[1]), GitDir: abs(lines[2])}, nil
}

// Git runs git in the checkout and returns stdout.
func (r *Repo) Git(args ...string) (string, error) { return run(r.Root, args...) }

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// IsMainCheckout reports whether this is the primary checkout, not a worktree.
func (r *Repo) IsMainCheckout() bool { return r.GitDir == r.CommonDir }

// Branch is the current branch, empty when detached.
func (r *Repo) Branch() string {
	out, err := r.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Head is HEAD's commit id.
func (r *Repo) Head() (string, error) {
	out, err := r.Git("rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Resolve resolves a ref to a commit id.
func (r *Repo) Resolve(ref string) (string, error) {
	out, err := r.Git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return strings.TrimSpace(out), err
}

// MergeBase is the merge base of a and b.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := r.Git("merge-base", a, b)
	return strings.TrimSpace(out), err
}

// AheadBehind counts commits of head not in base, and of base not in head.
func (r *Repo) AheadBehind(base, head string) (ahead, behind int, err error) {
	out, err := r.Git("rev-list", "--left-right", "--count", base+"..."+head)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, errors.New("rev-list: unexpected output")
	}
	behind, _ = strconv.Atoi(f[0])
	ahead, _ = strconv.Atoi(f[1])
	return ahead, behind, nil
}

// IsAncestor reports whether a is an ancestor of b.
func (r *Repo) IsAncestor(a, b string) bool {
	_, err := r.Git("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// Changed lists paths that differ between two commits.
func (r *Repo) Changed(from, to string) ([]string, error) {
	out, err := r.Git("diff", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// Uncommitted lists tracked paths changed in the work tree or the index, and
// untracked paths that are not ignored.
func (r *Repo) Uncommitted() ([]string, error) {
	out, err := r.Git("status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if len(e) < 4 {
			continue
		}
		paths = append(paths, e[3:])
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the source path follows a rename or copy
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// AddedLines returns the lines a diff adds per path, and the lines it removes.
func (r *Repo) DiffLines(from, to string, paths ...string) (added, removed map[string][]string, err error) {
	args := []string{"diff", "--no-color", "--unified=0", "--no-renames", from, to}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := r.Git(args...)
	if err != nil {
		return nil, nil, err
	}
	added, removed = map[string][]string{}, map[string][]string{}
	file := ""
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "@@"), strings.HasPrefix(l, "index "):
		case strings.HasPrefix(l, "+"):
			added[file] = append(added[file], l[1:])
		case strings.HasPrefix(l, "-"):
			removed[file] = append(removed[file], l[1:])
		}
	}
	return added, removed, nil
}

// ContentKey hashes the blob ids of every file at rev matching patterns (all
// files when patterns is empty). Equal keys mean equal content.
func (r *Repo) ContentKey(rev string, patterns []string) (string, error) {
	out, err := r.Git("ls-tree", "-r", "--full-tree", rev)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, l := range lines(out) {
		tab := strings.IndexByte(l, '\t')
		if tab < 0 {
			continue
		}
		name := l[tab+1:]
		if len(patterns) > 0 && !glob.Any(patterns, name) {
			continue
		}
		fmt.Fprintln(h, l)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Show returns a file's content at rev.
func (r *Repo) Show(rev, path string) ([]byte, error) {
	out, err := r.Git("show", rev+":"+path)
	return []byte(out), err
}

// Log lists one-line commits in base..head, oldest first.
func (r *Repo) Log(base, head string) ([]string, error) {
	out, err := r.Git("log", "--reverse", "--format=%h %s", base+".."+head)
	return lines(out), err
}

// DiffCheck runs git diff --check (whitespace errors, conflict markers).
func (r *Repo) DiffCheck(from, to string) (string, bool) {
	out, err := r.Git("diff", "--check", from, to)
	return strings.TrimSpace(out), err == nil
}

// Config reads a config value, empty when unset.
func (r *Repo) Config(key string) string {
	out, _ := r.Git("config", "--get", key)
	return strings.TrimSpace(out)
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}
