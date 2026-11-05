package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cognixws/picode-devflow/internal/contract"
	"github.com/cognixws/picode-devflow/internal/gitx"
	"github.com/cognixws/picode-devflow/internal/glob"
)

// Git protection (ADR-0401 D6): per clone, outside the repository. `flow
// hooks on` points core.hooksPath at the package's hooks and remembers the
// previous value; every hook applies the flow's rules, then runs the hook
// the project had. `off` restores the previous value. `check` says whether
// protection is still in place (a project tool such as husky's prepare can
// reset core.hooksPath).

const prevKey = "devflow.previousHooksPath"

// hookNames are the hooks the package carries.
var hookNames = []string{"pre-commit", "reference-transaction"}

func hooksDir() (string, error) {
	if d := os.Getenv("FLOW_HOOKS_DIR"); d != "" {
		return filepath.Abs(d)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	// bin/flow-<os>-<arch> → ../hooks; a cached build has no package: refuse.
	d := filepath.Join(filepath.Dir(filepath.Dir(exe)), "hooks")
	if _, err := os.Stat(filepath.Join(d, "pre-commit")); err != nil {
		return "", errors.New("this flow binary is not inside the Dev flow package, so it has no hooks to install")
	}
	return d, nil
}

type hooksState struct {
	On       bool   `json:"on"`
	Path     string `json:"path"`
	Ours     string `json:"ours"`
	Previous string `json:"previous,omitempty"`
	Reset    bool   `json:"reset"` // turned on once, then something changed core.hooksPath
}

func readHooks(r *gitx.Repo) (hooksState, error) {
	ours, err := hooksDir()
	if err != nil {
		return hooksState{}, err
	}
	st := hooksState{Path: r.Config("core.hooksPath"), Ours: ours}
	_, hadPrev := configHas(r, prevKey)
	st.Previous = r.Config(prevKey)
	st.On = st.Path == ours
	st.Reset = hadPrev && !st.On
	return st, nil
}

func configHas(r *gitx.Repo, key string) (string, bool) {
	out, err := r.Git("config", "--get", key)
	return strings.TrimSpace(out), err == nil
}

func cmdHooks(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("hooks", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	verb := fs.Arg(0)
	r, err := gitx.Open(".")
	if err != nil {
		return err
	}
	st, err := readHooks(r)
	if err != nil {
		return err
	}
	switch verb {
	case "on":
		if !st.On {
			if _, has := configHas(r, prevKey); !has {
				if _, err := r.Git("config", prevKey, st.Path); err != nil {
					return err
				}
			}
			if _, err := r.Git("config", "core.hooksPath", st.Ours); err != nil {
				return err
			}
		}
	case "off":
		if _, has := configHas(r, prevKey); has {
			if st.Previous == "" {
				r.Git("config", "--unset", "core.hooksPath")
			} else if _, err := r.Git("config", "core.hooksPath", st.Previous); err != nil {
				return err
			}
			r.Git("config", "--unset", prevKey)
		} else if st.On {
			r.Git("config", "--unset", "core.hooksPath")
		}
	case "check", "":
	default:
		return fmt.Errorf("usage: flow hooks on|off|check")
	}
	if st, err = readHooks(r); err != nil {
		return err
	}
	text := "Git protection is off in this clone."
	switch {
	case st.On:
		text = "Git protection is on in this clone (core.hooksPath → the Dev flow's hooks)."
	case st.Reset:
		text = "Git protection was turned on here, but core.hooksPath now points elsewhere (" + or(st.Path, "unset") + "): a tool reset it. Run `flow hooks on` again."
	}
	if err := emit(w, *asJSON, st, text); err != nil {
		return err
	}
	if verb == "check" && st.Reset {
		return exitError{1}
	}
	return nil
}

// cmdHook is one git hook: the flow's rules, then the project's own hook.
func cmdHook(args []string, stdin io.Reader, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: flow hook <name> [args]")
	}
	name, rest := args[0], args[1:]
	r, err := gitx.Open(".")
	if err != nil {
		return err
	}
	c, cerr := contract.Load(r.Root)
	main := "main"
	if cerr == nil {
		main = c.Main
	}
	var input []byte
	if name == "reference-transaction" {
		input, _ = io.ReadAll(stdin)
	}
	var refusal string
	switch name {
	case "pre-commit":
		refusal = preCommit(r, c, main)
	case "reference-transaction":
		if len(rest) > 0 && rest[0] == "prepared" {
			refusal = refTransaction(r, main, string(input))
		}
	}
	if refusal != "" {
		fmt.Fprintln(stderr, "Dev flow: "+refusal)
		return exitError{1}
	}
	return chain(r, name, rest, input, stderr)
}

func allowed(env string) bool { return os.Getenv(env) == "1" }

func preCommit(r *gitx.Repo, c *contract.Contract, main string) string {
	branch := r.Branch()
	if r.IsMainCheckout() && branch != "" && branch != main && !allowed("FLOW_ALLOW_SWITCH") {
		return fmt.Sprintf("the main checkout stays on %s; make this change in a worktree of its own (git worktree add). FLOW_ALLOW_SWITCH=1 overrides once.", main)
	}
	staged, _ := r.Git("diff", "--cached", "--name-only", "--no-renames")
	paths := lines(staged)
	if out, err := r.Git("diff", "--cached", "--check"); err != nil {
		first := strings.SplitN(strings.TrimSpace(out), "\n", 4)
		return "whitespace errors or conflict markers in what you are committing:\n  " + strings.Join(first, "\n  ")
	}
	if c != nil && c.HasModule("changelog") && !allowed("FLOW_ALLOW_CHANGELOG") {
		for _, p := range paths {
			if p == "CHANGELOG.md" {
				return "CHANGELOG.md is assembled from fragments; write " + c.Docs.Changelog + "/<branch>.md instead. FLOW_ALLOW_CHANGELOG=1 overrides once."
			}
		}
	}
	if r.IsMainCheckout() && branch == main && c != nil && !allowed("FLOW_ALLOW_MAIN") {
		docs := []string{"docs/**", "*.md"}
		for _, p := range paths {
			if !glob.Any(docs, p) {
				return fmt.Sprintf("%s is code on %s in the main checkout; work in a branch of its own and land it. Small docs may go straight to %s. FLOW_ALLOW_MAIN=1 overrides once.", p, main, main)
			}
		}
	}
	return ""
}

// refTransaction refuses moving main to a commit that does not contain it.
func refTransaction(r *gitx.Repo, main, input string) string {
	if allowed("FLOW_ALLOW_MAIN_REWIND") {
		return ""
	}
	sc := bufio.NewScanner(strings.NewReader(input))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || f[2] != "refs/heads/"+main {
			continue
		}
		old, nw := f[0], f[1]
		if strings.Trim(nw, "0") == "" {
			continue // deleting the branch is not a rewind
		}
		if strings.Trim(old, "0") == "" {
			// No expected old value (git update-ref without one): the
			// branch's tip before the change, if it exists.
			cur, err := r.Resolve("refs/heads/" + main)
			if err != nil || cur == "" {
				continue
			}
			old = cur
		}
		if !r.IsAncestor(old, nw) {
			return fmt.Sprintf("refusing to rewind %s (the new tip does not contain %s). FLOW_ALLOW_MAIN_REWIND=1 overrides once.", main, short(old))
		}
	}
	return ""
}

// chain runs the hook the project had before the flow took core.hooksPath:
// the previous hooksPath (relative to this checkout's top) or .git/hooks.
func chain(r *gitx.Repo, name string, args []string, input []byte, stderr io.Writer) error {
	prev := r.Config(prevKey)
	var dir string
	switch {
	case prev == "":
		dir = filepath.Join(r.CommonDir, "hooks")
	case filepath.IsAbs(prev):
		dir = prev
	default:
		dir = filepath.Join(r.Root, prev)
	}
	hook := filepath.Join(dir, name)
	if fi, err := os.Stat(hook); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
		return nil
	}
	cmd := exec.Command(hook, args...)
	cmd.Dir = r.Root
	cmd.Stdin = strings.NewReader(string(input))
	cmd.Stdout, cmd.Stderr = os.Stdout, stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return exitError{ee.ExitCode()}
		}
		return err
	}
	return nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
