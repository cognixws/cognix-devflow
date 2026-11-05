package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cognixws/picode-devflow/internal/contract"
	"github.com/cognixws/picode-devflow/internal/hostapi"
)

// The Dev flow page (door D) reads through the daemon's relay, which signs
// each call with the proxy secret. The page only reads: nothing it asks runs
// a project's command.

func (s *server) page(h func(w http.ResponseWriter, r *http.Request, ws pageWorkspace)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := os.Getenv("PICODE_EXT_PROXY_SECRET")
		if want == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-PiCode-Proxy-Secret")), []byte(want)) != 1 {
			pageErr(w, http.StatusForbidden, "Use the COGNIX[WS] page relay.")
			return
		}
		host := hostapi.FromEnv()
		if host == nil {
			pageErr(w, http.StatusServiceUnavailable, "The Dev flow is not running inside COGNIX[WS].")
			return
		}
		id := r.URL.Query().Get("workspace")
		wk, err := host.Workspace(id)
		if err != nil {
			pageErr(w, http.StatusNotFound, "This workspace is not one the Dev flow can read.")
			return
		}
		h(w, r, pageWorkspace{Workspace: wk, host: host})
	}
}

// pageWorkspaces lists the workspaces the Dev flow is on in, for a page
// opened without one (its Apps tile).
func (s *server) pageWorkspaces(w http.ResponseWriter, r *http.Request) {
	want := os.Getenv("PICODE_EXT_PROXY_SECRET")
	if want == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-PiCode-Proxy-Secret")), []byte(want)) != 1 {
		pageErr(w, http.StatusForbidden, "Use the COGNIX[WS] page relay.")
		return
	}
	host := hostapi.FromEnv()
	if host == nil {
		pageErr(w, http.StatusServiceUnavailable, "The Dev flow is not running inside COGNIX[WS].")
		return
	}
	list, err := host.Workspaces()
	if err != nil {
		pageErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := []map[string]string{}
	for _, wk := range list {
		if wk.ExtensionOn {
			out = append(out, map[string]string{"id": wk.ID, "name": wk.Name})
		}
	}
	writeJSON(w, map[string]any{"workspaces": out})
}

type pageWorkspace struct {
	hostapi.Workspace
	host *hostapi.Client
}

func pageErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// gate is what decides whether the page can show the project: a trusted
// contract, or the reason it cannot.
type gate struct {
	State    string             `json:"state"` // ready | no-contract | untrusted | invalid | not-git
	Message  string             `json:"message,omitempty"`
	Contract *contract.Contract `json:"-"`
}

func readGate(ws pageWorkspace) gate {
	if out, err := exec.Command("git", "-C", ws.Path, "rev-parse", "--show-toplevel").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		return gate{State: "not-git"}
	}
	p, err := ws.host.Project(ws.ID, "")
	if err != nil {
		return gate{State: "invalid", Message: err.Error()}
	}
	switch {
	case !p.Found:
		return gate{State: "no-contract"}
	case p.Error != "":
		return gate{State: "invalid", Message: p.Error}
	case !p.Trusted:
		return gate{State: "untrusted"}
	case len(p.Section) == 0 || string(p.Section) == "null":
		return gate{State: "no-contract"}
	}
	c, err := contract.Parse(p.Section)
	if err != nil {
		return gate{State: "invalid", Message: err.Error()}
	}
	return gate{State: "ready", Contract: c}
}

type worktree struct {
	Path   string
	Branch string
	Head   string
}

func worktrees(repo string) []worktree {
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var list []worktree
	var cur *worktree
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			list = append(list, worktree{Path: strings.TrimPrefix(l, "worktree ")})
			cur = &list[len(list)-1]
		case cur != nil && strings.HasPrefix(l, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(l, "branch "), "refs/heads/")
		case cur != nil && strings.HasPrefix(l, "HEAD "):
			cur.Head = strings.TrimPrefix(l, "HEAD ")
		}
	}
	return list
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	b, _ := cmd.Output()
	return strings.TrimSpace(string(b))
}

type rowOut struct {
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	Agent       string `json:"agent,omitempty"`
	AgentCLI    string `json:"agentCli,omitempty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	Uncommitted int    `json:"uncommitted"`
	LastCommit  int64  `json:"lastCommit"` // unix seconds
	Checks      state  `json:"checks"`
	Close       state  `json:"close"`
	Land        state  `json:"land"`
	Stalled     bool   `json:"stalled"`
}

// state is one column of a row: a word the page maps to a chip, and its text.
type state struct {
	Is    string `json:"is"` // none | running | passed | failed | owed | closed | landing | landed
	Text  string `json:"text,omitempty"`
	Title string `json:"title,omitempty"` // what is owed or failed, for a tooltip
}

type topicOut struct {
	Name string   `json:"name"`
	Open int      `json:"open"`
	Next []string `json:"next"`
}

type noteOut struct {
	File  string `json:"file"`
	Title string `json:"title"`
	Date  string `json:"date"`
}

type boardOut struct {
	gate
	Main     string     `json:"main"`
	Checks   int        `json:"checks"`
	Full     string     `json:"full,omitempty"`
	InFlight []rowOut   `json:"inFlight"`
	Topics   []topicOut `json:"topics"`
	Notes    []noteOut  `json:"notes"`
}

func (s *server) pageBoard(w http.ResponseWriter, r *http.Request, ws pageWorkspace) {
	out := boardOut{gate: readGate(ws), InFlight: []rowOut{}, Topics: []topicOut{}, Notes: []noteOut{}}
	if out.State != "ready" {
		writeJSON(w, out)
		return
	}
	c := out.Contract
	out.Main, out.Checks, out.Full = c.Main, len(c.Checks), c.Full
	agents, _ := ws.host.Agents(ws.ID)
	trees := worktrees(ws.Path)
	runs := s.runsByDir()
	rows := make([]rowOut, len(trees))
	var wg sync.WaitGroup
	for i, t := range trees {
		if t.Branch == "" || t.Branch == c.Main {
			continue
		}
		wg.Add(1)
		go func(i int, t worktree) {
			defer wg.Done()
			rows[i] = s.row(t, c.Main, agents, runs[t.Path])
		}(i, t)
	}
	wg.Wait()
	for _, row := range rows {
		if row.Branch != "" {
			out.InFlight = append(out.InFlight, row)
		}
	}
	sort.SliceStable(out.InFlight, func(i, j int) bool { return out.InFlight[i].LastCommit > out.InFlight[j].LastCommit })
	main := ws.Path
	if len(trees) > 0 {
		main = trees[0].Path
	}
	out.Topics = topics(filepath.Join(main, c.Docs.Topics))
	out.Notes = notes(filepath.Join(main, c.Docs.Notes))
	writeJSON(w, out)
}

func (s *server) row(t worktree, mainRef string, agents []hostapi.Agent, runs []*Run) rowOut {
	row := rowOut{Path: t.Path, Branch: t.Branch, Checks: state{Is: "none"}, Close: state{Is: "none"}, Land: state{Is: "none"}}
	if f := strings.Fields(gitOut(t.Path, "rev-list", "--left-right", "--count", mainRef+"...HEAD")); len(f) == 2 {
		row.Behind, _ = strconv.Atoi(f[0])
		row.Ahead, _ = strconv.Atoi(f[1])
	}
	if st := gitOut(t.Path, "status", "--porcelain=v1", "--untracked-files=normal"); st != "" {
		row.Uncommitted = len(strings.Split(st, "\n"))
	}
	row.LastCommit, _ = strconv.ParseInt(gitOut(t.Path, "log", "-1", "--format=%ct"), 10, 64)
	row.Stalled = row.Ahead > 0 && row.LastCommit > 0 && time.Since(time.Unix(row.LastCommit, 0)) > 14*24*time.Hour
	for _, a := range agents {
		if a.WorkDir != "" && filepath.Clean(a.WorkDir) == filepath.Clean(t.Path) {
			row.Agent, row.AgentCLI = a.Name, a.CLI
			break
		}
	}
	for _, run := range runs { // newest first
		switch run.Kind {
		case "check", "close":
			if row.Checks.Is == "none" {
				row.Checks = checkState(run)
			}
			if run.Kind == "close" && row.Close.Is == "none" {
				row.Close = closeState(run)
			}
		case "land":
			if row.Land.Is == "none" {
				switch {
				case run.State == "running":
					row.Land = state{Is: "landing", Text: "Landing"}
				case run.ExitCode == 0:
					row.Land = state{Is: "landed", Text: "Landed"}
				default:
					row.Land = state{Is: "failed", Text: "Land stopped"}
				}
			}
		}
	}
	return row
}

func checkState(run *Run) state {
	if run.State == "running" {
		return state{Is: "running", Text: "Running " + run.Kind + "…"}
	}
	var res struct {
		Checks []checkOut `json:"checks"`
	}
	_ = json.Unmarshal(run.Result, &res)
	n, failed := 0, []string{}
	for _, c := range res.Checks {
		n++
		if !c.OK {
			failed = append(failed, c.Name)
		}
	}
	switch {
	case n == 0 && run.ExitCode == 0:
		return state{Is: "none", Text: "Nothing to check"}
	case len(failed) > 0:
		return state{Is: "failed", Text: strings.Join(failed, ", ") + " failed"}
	case run.ExitCode != 0 && run.Kind == "check":
		return state{Is: "failed", Text: "Failed"}
	}
	return state{Is: "passed", Text: "Passed " + strconv.Itoa(n) + " of " + strconv.Itoa(n)}
}

func closeState(run *Run) state {
	if run.State == "running" {
		return state{Is: "running", Text: "Closing…"}
	}
	var res closeOut
	if json.Unmarshal(run.Result, &res) != nil {
		return state{Is: "none"}
	}
	if res.Refused != "" {
		return state{Is: "failed", Text: "Not closed"}
	}
	owed, fails := 0, 0
	var what []string
	for _, f := range res.Findings {
		switch f.Level {
		case "owed":
			owed++
			what = append(what, f.Message)
		case "fail":
			fails++
			what = append(what, f.Message)
		}
	}
	switch {
	case fails > 0 || !allOK(res.Checks):
		return state{Is: "failed", Text: "Close failed", Title: strings.Join(what, "\n")}
	case owed > 0:
		return state{Is: "owed", Text: strconv.Itoa(owed) + " to write", Title: strings.Join(what, "\n")}
	}
	return state{Is: "closed", Text: "Closed"}
}

// runsByDir indexes the process's runs by checkout, newest first.
func (s *server) runsByDir() map[string][]*Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]*Run{}
	for _, r := range s.runs {
		out[r.WorkDir] = append(out[r.WorkDir], r)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].StartedAt.After(out[k][j].StartedAt) })
	}
	return out
}

var openRe = regexp.MustCompile(`^\s*- \[ \] (.+)$`)

func topics(dir string) []topicOut {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return []topicOut{}
	}
	type ft struct {
		t   topicOut
		mod time.Time
	}
	var all []ft
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		t := topicOut{Name: strings.TrimSuffix(e.Name(), ".md"), Next: []string{}}
		sec := ""
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<16), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "## ") {
				sec = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "## ")))
				continue
			}
			if m := openRe.FindStringSubmatch(l); m != nil {
				t.Open++
				if strings.HasPrefix(sec, "next") && len(t.Next) < 2 {
					t.Next = append(t.Next, clip(m[1], 160))
				}
			}
		}
		f.Close()
		if t.Open == 0 {
			continue
		}
		info, _ := e.Info()
		all = append(all, ft{t, info.ModTime()})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	out := []topicOut{}
	for i, x := range all {
		if i == 6 {
			break
		}
		out = append(out, x.t)
	}
	return out
}

var noteName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(.+)\.md$`)

func notes(dir string) []noteOut {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return []noteOut{}
	}
	cutoff := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	var out []noteOut
	for _, e := range ents {
		m := noteName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil || m[1] < cutoff {
			continue
		}
		title := m[2]
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if t := strings.TrimSpace(strings.TrimLeft(l, "# ")); t != "" {
					title = clip(t, 140)
					break
				}
			}
		}
		out = append(out, noteOut{File: e.Name(), Title: title, Date: m[1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File > out[j].File })
	if len(out) > 5 {
		out = out[:5]
	}
	if out == nil {
		out = []noteOut{}
	}
	return out
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}

type checkRow struct {
	Name  string   `json:"name"`
	Run   string   `json:"run"`
	When  []string `json:"when"`
	Only  string   `json:"only,omitempty"`
	Last  *lastRun `json:"last,omitempty"`
	Flaky int      `json:"flaky"`
}

type lastRun struct {
	OK       bool   `json:"ok"`
	Seconds  int    `json:"seconds"`
	At       string `json:"at"`
	Branch   string `json:"branch,omitempty"`
	Reused   bool   `json:"reused,omitempty"`
	TimedOut bool   `json:"timedOut,omitempty"`
}

type checksPageOut struct {
	gate
	Checks []checkRow `json:"checks"`
	Rules  int        `json:"rules"`
}

func (s *server) pageChecks(w http.ResponseWriter, r *http.Request, ws pageWorkspace) {
	out := checksPageOut{gate: readGate(ws), Checks: []checkRow{}}
	if out.State != "ready" {
		writeJSON(w, out)
		return
	}
	c := out.Contract
	out.Rules = len(c.Rules)
	flakes := s.flakeCounts(ws.Path)
	runs := s.allRuns()
	names := c.CheckNames()
	if c.Full != "" {
		names = append(names, "full")
	}
	for _, n := range names {
		row := checkRow{Name: n, When: []string{}, Flaky: flakes[n]}
		if n == "full" {
			row.Run, row.Only = c.Full, "full"
		} else {
			ch := c.Checks[n]
			row.Run, row.Only = ch.Run, ch.Only
			if ch.When != nil {
				row.When = ch.When
			}
		}
		row.Last = lastFor(runs, n)
		out.Checks = append(out.Checks, row)
	}
	writeJSON(w, out)
}

func (s *server) allRuns() []*Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Run, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// lastFor is the newest run that ran (not reused) a check by that name.
func lastFor(runs []*Run, name string) *lastRun {
	for _, r := range runs {
		if r.State != "done" || (r.Kind != "check" && r.Kind != "close") {
			continue
		}
		var res struct {
			Checks []checkOut `json:"checks"`
		}
		if json.Unmarshal(r.Result, &res) != nil {
			continue
		}
		for _, c := range res.Checks {
			if c.Name == name && !c.Reused && c.Skipped == "" {
				at := ""
				if r.EndedAt != nil {
					at = r.EndedAt.Format(time.RFC3339)
				}
				return &lastRun{OK: c.OK, Seconds: int(c.Duration.Seconds()), At: at, Branch: r.Branch, TimedOut: c.TimedOut}
			}
		}
	}
	return nil
}

// flakeCounts counts the checks' flaky tests over 14 days, from the
// repository's flakes log.
func (s *server) flakeCounts(repo string) map[string]int {
	common := gitOut(repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	out := map[string]int{}
	f, err := os.Open(filepath.Join(common, "picode-devflow", "flakes.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	cutoff := time.Now().AddDate(0, 0, -14)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			At    time.Time `json:"at"`
			Check string    `json:"check"`
			Tests []string  `json:"tests"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.At.After(cutoff) {
			out[e.Check] += len(e.Tests)
		}
	}
	return out
}

type activityOut struct {
	gate
	Runs []activityRow `json:"runs"`
}

type activityRow struct {
	Is       string `json:"is"`
	Title    string `json:"title,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Branch   string `json:"branch"`
	State    string `json:"state"`
	OK       bool   `json:"ok"`
	Summary  string `json:"summary"`
	Started  string `json:"started"`
	Seconds  int    `json:"seconds"`
	Worktree string `json:"worktree"`
}

func (s *server) pageActivity(w http.ResponseWriter, r *http.Request, ws pageWorkspace) {
	out := activityOut{gate: readGate(ws), Runs: []activityRow{}}
	if out.State == "not-git" {
		writeJSON(w, out)
		return
	}
	dirs := map[string]bool{}
	for _, t := range worktrees(ws.Path) {
		dirs[filepath.Clean(t.Path)] = true
	}
	for _, r := range s.allRuns() {
		if !dirs[filepath.Clean(r.WorkDir)] {
			continue
		}
		row := activityRow{ID: r.ID, Kind: r.Kind, Branch: r.Branch, State: r.State, OK: r.ExitCode == 0, Started: r.StartedAt.Format(time.RFC3339), Worktree: filepath.Base(r.WorkDir)}
		if r.EndedAt != nil {
			row.Seconds = int(r.EndedAt.Sub(r.StartedAt).Seconds())
		}
		switch {
		case r.State == "running":
			row.Summary, row.Is = "Running", "running"
		case r.State == "interrupted":
			row.Summary, row.Is = "Cut short by a restart", "owed"
		case r.Kind == "land":
			if row.OK {
				row.Summary, row.Is = "Landed", "landed"
			} else {
				row.Summary, row.Is = "Land stopped", "failed"
			}
		case r.Kind == "close":
			st := closeState(r)
			row.Summary, row.Is, row.Title = st.Text, st.Is, st.Title
		default:
			st := checkState(r)
			row.Summary, row.Is = st.Text, st.Is
		}
		out.Runs = append(out.Runs, row)
		if len(out.Runs) == 30 {
			break
		}
	}
	writeJSON(w, out)
}

func branchOf(dir string) string {
	return gitOut(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
}
