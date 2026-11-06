package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cognixws/cognix-devflow/internal/scope"
	"github.com/cognixws/cognix-devflow/internal/validate"
)

type statusOut struct {
	Root         string             `json:"root"`
	MainCheckout bool               `json:"mainCheckout"`
	Branch       string             `json:"branch"`
	Main         string             `json:"main"`
	Ahead        int                `json:"ahead"`
	Behind       int                `json:"behind"`
	Uncommitted  []string           `json:"uncommitted"`
	Selection    scope.Selection    `json:"selection"`
	Findings     []validate.Finding `json:"findings"`
	Next         string             `json:"next"`
}

func cmdStatus(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(true)
	if err != nil {
		return err
	}
	findings, err := s.findings(false)
	if err != nil {
		return err
	}
	out := statusOut{
		Root: s.repo.Root, MainCheckout: s.repo.IsMainCheckout(), Branch: s.branch, Main: s.c.Main,
		Ahead: s.ahead, Behind: s.behind, Uncommitted: nonNil(s.dirty), Selection: s.selected,
		Findings: sortedFindings(nonNilF(findings)),
	}
	out.Next = nextStep(s, findings)
	if *asJSON {
		return json.NewEncoder(w).Encode(out)
	}
	where := "worktree"
	if out.MainCheckout {
		where = "main checkout"
	}
	branch := out.Branch
	if branch == "" {
		branch = "(detached)"
	}
	fmt.Fprintf(w, "Dev flow · %s · %s · %d ahead, %d behind %s · %d uncommitted\n", branch, where, out.Ahead, out.Behind, out.Main, len(out.Uncommitted))
	fmt.Fprintf(w, "Checks this change needs: %s\n", describeSelection(out.Selection))
	printFindings(w, out.Findings)
	fmt.Fprintf(w, "Next: %s\n", out.Next)
	return nil
}

func nextStep(s *session, fs []validate.Finding) string {
	switch {
	case s.branch == "" || s.branch == s.c.Main:
		return fmt.Sprintf("start a branch in a worktree of its own (%s<name>); never commit on %s", s.c.BranchPrefix, s.c.Main)
	case len(s.dirty) > 0:
		return "commit your work in explicit paths (never git add -A); check runs the checks it needs"
	case s.ahead == 0:
		return "nothing to land yet: make the change and commit it"
	case countLevel(fs, validate.Fail) > 0:
		return "fix the failures above, commit, then close"
	default:
		return "close the change; then write what it lists as owed, commit, and land"
	}
}

func describeSelection(sel scope.Selection) string {
	if len(sel.Checks) == 0 {
		if len(sel.Reasons) > 0 {
			return "none (" + strings.Join(sel.Reasons, "; ") + ")"
		}
		return "none"
	}
	d := strings.Join(sel.Checks, ", ")
	if sel.Full {
		d = "the full gate (" + d + ") because " + strings.Join(sel.Reasons, "; ")
	}
	return d
}

func printFindings(w io.Writer, fs []validate.Finding) {
	for _, f := range fs {
		mark := map[string]string{validate.Fail: "✗", validate.Owed: "!", validate.Warn: "·"}[f.Level]
		fmt.Fprintf(w, "%s %s\n", mark, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "    → %s\n", f.Fix)
		}
	}
}

type checkResultOut struct {
	OK     bool       `json:"ok"`
	Checks []checkOut `json:"checks"`
	Plan   []string   `json:"plan"`
	Why    []string   `json:"why,omitempty"`
}

func cmdCheck(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	full := fs.Bool("full", false, "run the full gate")
	only := fs.String("only", "", "run one named check")
	fresh := fs.Bool("fresh", false, "never reuse an earlier pass")
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(true)
	if err != nil {
		return err
	}
	plan := s.selected.Checks
	why := s.selected.Reasons
	switch {
	case *only != "":
		plan, why = []string{*only}, nil
	case *full:
		plan, why = scope.Full(s.c), []string{"asked for the full gate"}
	}
	changed := append(append([]string{}, s.changed...), s.dirty...)
	res := s.runChecks(plan, !*fresh, changed)
	out := checkResultOut{OK: allOK(res), Checks: res, Plan: nonNil(plan), Why: why}
	if *asJSON {
		if err := json.NewEncoder(w).Encode(out); err != nil {
			return err
		}
	} else {
		if len(plan) == 0 {
			fmt.Fprintf(w, "No checks to run: %s\n", strings.Join(why, "; "))
		}
		printChecks(w, res)
	}
	if !out.OK {
		return exitError{1}
	}
	return nil
}

func allOK(rs []checkOut) bool {
	for _, r := range rs {
		if !r.OK {
			return false
		}
	}
	return true
}

func printChecks(w io.Writer, rs []checkOut) {
	for _, r := range rs {
		switch {
		case r.Skipped != "":
			fmt.Fprintf(w, "✗ %-12s %s\n", r.Name, r.Skipped)
		case r.Reused:
			fmt.Fprintf(w, "✓ %-12s reused: %s\n", r.Name, r.Tail)
		case r.OK:
			extra := ""
			if len(r.Flaky) > 0 {
				extra = "   (passed on the retry; flaky: " + strings.Join(r.Flaky, ", ") + ")"
			}
			if len(r.Quarantined) > 0 {
				extra += "   (quarantined failures: " + strings.Join(r.Quarantined, ", ") + ")"
			}
			fmt.Fprintf(w, "✓ %-12s %s%s\n", r.Name, r.Duration.Round(time.Second), extra)
		default:
			what := "failed"
			if r.TimedOut {
				what = "timed out"
			}
			if len(r.Failed) > 0 {
				what += ": " + strings.Join(r.Failed, ", ")
			}
			fmt.Fprintf(w, "✗ %-12s %s\n    log: %s\n", r.Name, what, r.LogPath)
			if r.Tail != "" {
				for _, l := range lastLines(r.Tail, 8) {
					fmt.Fprintf(w, "    | %s\n", l)
				}
			}
		}
	}
}

func lastLines(s string, n int) []string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return ls
}

type closeOut struct {
	OK            bool               `json:"ok"`
	Refused       string             `json:"refused,omitempty"`
	Branch        string             `json:"branch"`
	Base          string             `json:"base"`
	Head          string             `json:"head"`
	Ahead         int                `json:"ahead"`
	Behind        int                `json:"behind"`
	Commits       []string           `json:"commits"`
	ChangedFiles  []string           `json:"changedFiles"`
	Selection     scope.Selection    `json:"selection"`
	Checks        []checkOut         `json:"checks"`
	Findings      []validate.Finding `json:"findings"`
	TopicsTouched []string           `json:"topicsTouched"`
	Next          string             `json:"next"`
}

func cmdClose(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	strict := fs.Bool("strict", false, "owed documents fail the close (used before landing)")
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(false)
	if err != nil {
		return err
	}
	out := closeOut{Branch: s.branch, Base: s.base, Head: s.head, Ahead: s.ahead, Behind: s.behind,
		Commits: []string{}, ChangedFiles: nonNil(s.changed), Selection: s.selected, Checks: []checkOut{}, Findings: []validate.Finding{}, TopicsTouched: []string{}}
	switch {
	case s.branch == "" || s.branch == s.c.Main:
		out.Refused = fmt.Sprintf("close runs on a change's branch, not on %s", or(s.branch, "a detached HEAD"))
	case len(s.dirty) > 0:
		out.Refused = fmt.Sprintf("%d uncommitted path(s): commit or discard them first", len(s.dirty))
	case s.ahead == 0:
		out.Refused = "this branch has no commit that " + s.c.Main + " lacks"
	}
	if out.Refused != "" {
		out.Next = out.Refused
		return finishClose(w, out, *asJSON, 1)
	}
	out.Commits, _ = s.repo.Log(s.base, s.head)
	out.Checks = s.runChecks(s.selected.Checks, true, s.changed)
	findings, err := s.findings(true)
	if err != nil {
		return err
	}
	out.Findings = sortedFindings(nonNilF(findings))
	topics := strings.TrimSuffix(s.c.Docs.Topics, "/") + "/"
	for _, p := range s.changed {
		if strings.HasPrefix(p, topics) {
			out.TopicsTouched = append(out.TopicsTouched, strings.TrimSuffix(strings.TrimPrefix(p, topics), ".md"))
		}
	}
	fails := countLevel(out.Findings, validate.Fail)
	owed := countLevel(out.Findings, validate.Owed)
	code := 0
	switch {
	case !allOK(out.Checks) || fails > 0:
		code = 1
		out.Next = "fix what failed, commit, and close again"
	case owed > 0 && *strict:
		code = 3
		out.Next = "write what is owed, commit, and close again"
	case owed > 0:
		out.Next = "write what is owed (below), commit, then land"
	default:
		out.Next = "ready to land"
	}
	out.OK = code == 0
	return finishClose(w, out, *asJSON, code)
}

func finishClose(w io.Writer, out closeOut, asJSON bool, code int) error {
	if asJSON {
		if err := json.NewEncoder(w).Encode(out); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "Dev flow close · %s · %d ahead, %d behind\n", or(out.Branch, "(detached)"), out.Ahead, out.Behind)
		if out.Refused != "" {
			fmt.Fprintf(w, "✗ %s\n", out.Refused)
		} else {
			printChecks(w, out.Checks)
			if len(out.Findings) == 0 {
				fmt.Fprintln(w, "✓ process      notes, debts, rules, records and generated files")
			}
			printFindings(w, out.Findings)
			fmt.Fprintf(w, "\nSummary: %d commit(s) · %d file(s) · checks: %s", len(out.Commits), len(out.ChangedFiles), describeSelection(out.Selection))
			if len(out.TopicsTouched) > 0 {
				fmt.Fprintf(w, " · topics: %s", strings.Join(out.TopicsTouched, ", "))
			}
			fmt.Fprintln(w)
			for _, c := range out.Commits {
				fmt.Fprintf(w, "  %s\n", c)
			}
		}
		fmt.Fprintf(w, "Next: %s\n", out.Next)
	}
	if code != 0 {
		return exitError{code}
	}
	return nil
}

func cmdValidate(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openSession(false)
	if err != nil {
		return err
	}
	findings, err := s.findings(true)
	if err != nil {
		return err
	}
	findings = sortedFindings(nonNilF(findings))
	if *asJSON {
		if err := json.NewEncoder(w).Encode(findings); err != nil {
			return err
		}
	} else if len(findings) == 0 {
		fmt.Fprintln(w, "✓ process rules hold")
	} else {
		printFindings(w, findings)
	}
	if countLevel(findings, validate.Fail) > 0 {
		return exitError{1}
	}
	return nil
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilF(s []validate.Finding) []validate.Finding {
	if s == nil {
		return []validate.Finding{}
	}
	return s
}
