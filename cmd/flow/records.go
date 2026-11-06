package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cognixws/cognix-devflow/internal/contract"
	"github.com/cognixws/cognix-devflow/internal/docsops"
	"github.com/cognixws/cognix-devflow/internal/gitx"
	"github.com/cognixws/cognix-devflow/internal/hostapi"
	"github.com/cognixws/cognix-devflow/internal/scope"
	"github.com/cognixws/cognix-devflow/skills"
)

func cmdDebt(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("debt", flag.ContinueOnError)
	topic := fs.String("topic", "", "the topic file's name (lower-case, dashes)")
	pay := fs.Bool("pay", false, "mark the open item that starts with the text as paid")
	note := fs.String("note", "", "with --pay: how it was paid")
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	s, err := openSession(false)
	if err != nil {
		return err
	}
	var rel string
	if *pay {
		rel, err = docsops.PayDebt(s.repo.Root, s.c, *topic, text, *note)
	} else {
		rel, err = docsops.AddDebt(s.repo.Root, s.c, *topic, text)
	}
	if err != nil {
		return err
	}
	verb := "added to"
	if *pay {
		verb = "marked paid in"
	}
	return emit(w, *asJSON, map[string]any{"file": rel, "paid": *pay}, fmt.Sprintf("Debt %s %s. Commit it with your change.", verb, rel))
}

func cmdADR(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("adr", flag.ContinueOnError)
	title := fs.String("title", "", "the record's title")
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: flow adr [--title t] <name>")
	}
	s, err := openSession(false)
	if err != nil {
		return err
	}
	rel, err := docsops.NewDecision(s.repo, s.c, fs.Arg(0), *title)
	if err != nil {
		return err
	}
	return emit(w, *asJSON, map[string]any{"file": rel}, "Decision record seeded: "+rel+". Fill the Boundary line first (decision-record skill).")
}

// Guides lists the skills the guide command can print.
func guides() []string {
	var out []string
	_ = fs.WalkDir(skills.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) == "SKILL.md" {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func cmdGuide(args []string, w io.Writer) error {
	if len(args) != 1 {
		fmt.Fprintf(w, "flow guide <topic>: %s\n", strings.Join(guides(), ", "))
		return nil
	}
	b, err := skills.FS.ReadFile(args[0] + "/SKILL.md")
	if err != nil {
		return fmt.Errorf("no guide %q (%s)", args[0], strings.Join(guides(), ", "))
	}
	_, err = w.Write(b)
	return err
}

type landOut struct {
	OK      bool   `json:"ok"`
	Branch  string `json:"branch"`
	Command string `json:"command,omitempty"`
	Tail    string `json:"tail,omitempty"`
	Next    string `json:"next"`
}

// cmdLand runs the project's own land command from the main checkout. A
// project without one lands through COGNIX[WS]'s Merge.
func cmdLand(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("land", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(false)
	if err != nil {
		return err
	}
	out := landOut{Branch: s.branch}
	switch {
	case s.branch == "" || s.branch == s.c.Main:
		out.Next = "land a change's branch, not " + or(s.branch, "a detached HEAD")
	case len(s.dirty) > 0:
		out.Next = "commit or discard the uncommitted paths first"
	}
	if out.Next != "" {
		return emitLand(w, *asJSON, out, 1)
	}
	// ADR-0401 F4d: the checks that decide the land are the target's — a
	// branch that changes its own checks does not decide its own merge.
	if msg, tail := gateOnTarget(s); msg != "" {
		out.Next, out.Tail = msg, tail
		return emitLand(w, *asJSON, out, 1)
	}
	if s.c.Land == "" {
		return landThroughMerge(w, *asJSON, out)
	}
	root, err := mainCheckout(s.repo)
	if err != nil {
		return err
	}
	out.Command = strings.ReplaceAll(s.c.Land, "{branch}", s.branch)
	cmd := exec.Command("/bin/sh", "-c", out.Command)
	cmd.Dir = root
	cmd.Env = os.Environ()
	b, err := cmd.CombinedOutput()
	out.Tail = strings.Join(lastLines(strings.TrimRight(string(b), "\n"), 15), "\n")
	out.OK = err == nil
	if out.OK {
		out.Next = "landed: " + s.branch + " is on " + s.c.Main
		return emitLand(w, *asJSON, out, 0)
	}
	out.Next = "the land stopped; read the tail, fix, close again and retry"
	return emitLand(w, *asJSON, out, 1)
}

func emitLand(w io.Writer, asJSON bool, out landOut, code int) error {
	if asJSON {
		json.NewEncoder(w).Encode(out)
	} else {
		if out.Tail != "" {
			fmt.Fprintln(w, out.Tail)
		}
		fmt.Fprintln(w, "Next:", out.Next)
	}
	if code != 0 {
		return exitError{code}
	}
	return nil
}

// mainCheckout is the first entry of git worktree list: the primary checkout.
func mainCheckout(r *gitx.Repo) (string, error) {
	out, err := r.Git("worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			return p, nil
		}
	}
	return r.Root, nil
}

func emit(w io.Writer, asJSON bool, v any, text string) error {
	if asJSON {
		return json.NewEncoder(w).Encode(v)
	}
	_, err := fmt.Fprintln(w, text)
	return err
}

// landThroughMerge asks COGNIX[WS]'s Merge for the agent's change (Who
// merges, ADR-0401 F4c) and waits for the outcome. Outside COGNIX[WS], or
// where the person merges, it says so.
func landThroughMerge(w io.Writer, asJSON bool, out landOut) error {
	host, agent := hostapi.FromEnv(), os.Getenv("FLOW_AGENT")
	if host == nil || agent == "" {
		out.Next = "this project lands through Merge in COGNIX[WS]: the change is ready when close passes; ask the person to merge it from the Merge tab"
		return emitLand(w, asJSON, out, 1)
	}
	e, err := host.RequestMerge(agent)
	if err != nil {
		var st hostapi.ErrStatus
		if errors.As(err, &st) && st.Msg != "" {
			out.Next = st.Msg
		} else {
			out.Next = "Merge could not be asked: " + err.Error()
		}
		return emitLand(w, asJSON, out, 1)
	}
	deadline := time.Now().Add(50 * time.Minute)
	for (e.State == "authorized" || e.State == "running" || e.State == "waiting") && time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if next, err := host.MergeState(agent, e.ID); err == nil {
			e = next
		}
	}
	out.Tail = e.Note
	switch e.State {
	case "done":
		out.OK, out.Next = true, "landed: "+out.Branch+" is on "+or(e.Target, "the target")
		return emitLand(w, asJSON, out, 0)
	case "failed":
		out.Next = "Merge stopped: " + e.Note
	default:
		out.Next = "still merging after 50 minutes; the Merge tab shows where it is"
	}
	return emitLand(w, asJSON, out, 1)
}

// targetContract is the contract as it stands on the target branch. Inside
// COGNIX[WS] it is read through the Host API, which hands the section only
// when the person trusted those bytes; elsewhere from git. A target with no
// contract yet (this branch brings it) gates on the branch's own.
func targetContract(s *session) (*contract.Contract, string, error) {
	branchOwn := "this branch's contract (" + s.c.Main + " has none yet)"
	if host, ws := hostapi.FromEnv(), os.Getenv("FLOW_WORKSPACE"); host != nil && ws != "" {
		p, err := host.Project(ws, s.c.Main)
		switch {
		case err != nil:
			return nil, "", fmt.Errorf("the contract on %s could not be read from COGNIX[WS]: %v", s.c.Main, err)
		case !p.Found:
			return s.c, branchOwn, nil
		case !p.Trusted:
			return nil, "", fmt.Errorf("the Dev flow contract on %s is not trusted yet: the person reviews .picode.json in the workspace's Settings › Scripts, then land again", s.c.Main)
		case len(p.Section) == 0 || string(p.Section) == "null":
			return s.c, branchOwn, nil
		}
		c, err := contract.Parse(p.Section)
		if err != nil {
			return nil, "", fmt.Errorf("the contract on %s does not parse: %v", s.c.Main, err)
		}
		return c, "the contract on " + s.c.Main, nil
	}
	b, err := s.repo.Git("show", s.mainRev+":"+contract.File)
	if err != nil {
		return s.c, branchOwn, nil
	}
	c, err := contract.FromProjectFile([]byte(b))
	if errors.Is(err, contract.ErrNoContract) {
		return s.c, branchOwn, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("the contract on %s does not parse: %v", s.c.Main, err)
	}
	return c, "the contract on " + s.c.Main, nil
}

// gateOnTarget runs the checks the target's contract selects for this
// change (a green run of the same content is reused). It answers why the
// land must stop, or "".
func gateOnTarget(s *session) (string, string) {
	tc, src, err := targetContract(s)
	if err != nil {
		return err.Error(), ""
	}
	gate := *s
	gate.c = tc
	gate.selected = scope.Select(tc, s.changed)
	for _, r := range gate.runChecks(gate.selected.Checks, true, s.changed) {
		if !r.OK && r.Skipped == "" {
			return "check " + r.Name + " failed under " + src + "; fix it, close again and land", r.Tail
		}
	}
	return "", ""
}
