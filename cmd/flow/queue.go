package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cognixws/picode-devflow/internal/contract"
	"github.com/cognixws/picode-devflow/internal/hostapi"
)

// The Queue tab (ADR-0401 amendment, 2026-11-06): the project's own land
// queue, read and ordered through the contract's queue commands, beside
// the Dev flow's own runs. The person moves or stops a ticket here, and
// runs a branch's check, close or land from the Board's row menu.

// queueTicket is one entry of queue.list's {"tickets": [...]}, in order.
type queueTicket struct {
	Ticket     string `json:"ticket"`
	Branch     string `json:"branch"`
	State      string `json:"state"`
	AgeSeconds *int64 `json:"ageSeconds"`
	PID        int    `json:"pid"`
	Claim      *struct {
		State string `json:"state"`
	} `json:"claim"`
}

// queueRow is one row the page draws.
type queueRow struct {
	Ticket  string   `json:"ticket"`
	Branch  string   `json:"branch"`
	Agent   string   `json:"agent,omitempty"`
	State   string   `json:"state"`
	Is      string   `json:"is"`
	Text    string   `json:"text"`
	Reason  string   `json:"reason,omitempty"`
	Age     string   `json:"age"`
	Actions []string `json:"actions"`
}

var ticketID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// stoppable are the states a person may take out of the queue: waiting,
// not yet holding the target.
var stoppable = map[string]bool{"preparing": true, "ready": true, "waiting": true, "queued": true}

// queueRows turns the listed tickets into rows. A landing row holds the
// target: it neither moves nor stops, and nothing moves in front of it.
func queueRows(tickets []queueTicket, target string) []queueRow {
	live := []queueTicket{}
	for _, t := range tickets {
		if ticketID.MatchString(t.Ticket) && t.Branch != "" {
			live = append(live, t)
		}
	}
	head := 0
	if len(live) > 0 && live[0].State == "landing" {
		head = 1
	}
	rows := make([]queueRow, 0, len(live))
	for i, t := range live {
		claim := ""
		if t.Claim != nil {
			claim = t.Claim.State
		}
		r := queueRow{Ticket: t.Ticket, Branch: t.Branch, State: t.State, Actions: []string{}}
		if t.AgeSeconds != nil {
			r.Age = ageLabel(*t.AgeSeconds)
		}
		switch {
		case t.State == "landing":
			r.Is, r.Text, r.Reason = "landing", "Landing", "Holding "+target+". Wait for the verdict."
		case claim == "claimed":
			r.Is, r.Text, r.Reason = "running", "In a batch", "Stopping it does not take it out of its batch."
		case t.State == "preparing":
			r.Is, r.Text = "running", "Closing"
		case t.State == "ready" || t.State == "waiting" || t.State == "queued":
			r.Is, r.Text = "closed", "Ready"
		default:
			r.Is, r.Text = "none", t.State
		}
		if t.State != "landing" {
			if i > head {
				r.Actions = append(r.Actions, "earlier", "first")
			}
			if i < len(live)-1 {
				r.Actions = append(r.Actions, "later")
			}
			if stoppable[t.State] && claim != "claimed" && claim != "done" {
				r.Actions = append(r.Actions, "stop")
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// moveArgs is what {move} becomes for a person's move, or "" when the move
// does not apply there. First goes right behind a landing head.
func moveArgs(rows []queueRow, ticket, how string) string {
	i := -1
	for k, r := range rows {
		if r.Ticket == ticket {
			i = k
		}
	}
	if i < 0 || !contains(rows[i].Actions, how) {
		return ""
	}
	switch how {
	case "earlier":
		return "--before " + rows[i-1].Ticket
	case "later":
		return "--after " + rows[i+1].Ticket
	case "first":
		if rows[0].State == "landing" {
			return "--after " + rows[0].Ticket
		}
		return "--front"
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func ageLabel(s int64) string {
	switch {
	case s < 60:
		return "just now"
	case s < 3600:
		return strconv.FormatInt(s/60, 10) + " min"
	case s < 86400:
		return strconv.FormatInt(s/3600, 10) + " h"
	}
	return strconv.FormatInt(s/86400, 10) + " days"
}

// queueCommand runs one of the contract's queue commands from the main
// checkout and answers its stdout; stderr's last line is the error.
func queueCommand(dir, line string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if l := lastLines(strings.TrimSpace(string(ee.Stderr)), 1); len(l) > 0 && l[0] != "" {
				return out, errors.New(clip(l[0], 200))
			}
		}
		if ctx.Err() != nil {
			return out, errors.New("the queue command took too long")
		}
		return out, errors.New("the queue command failed")
	}
	return out, nil
}

// readQueue lists the project's queue; nil rows and no error when the
// contract binds none.
func readQueue(c *contract.Contract, mainDir string) ([]queueRow, error) {
	if c.Queue == nil {
		return nil, nil
	}
	out, err := queueCommand(mainDir, c.Queue.List, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		Tickets []queueTicket `json:"tickets"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, errors.New("queue.list did not print {\"tickets\": [...]}")
	}
	return queueRows(res.Tickets, c.Main), nil
}

// redLine is the banner when the contract's red file exists and is not a
// pending marker (a fast-forward in progress, which its landing row shows).
func redLine(c *contract.Contract, mainDir string) string {
	if c.Queue == nil || c.Queue.Red == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(mainDir, c.Queue.Red))
	if err != nil {
		return ""
	}
	var m struct {
		Pending bool   `json:"pending"`
		Branch  string `json:"branch"`
	}
	if json.Unmarshal(b, &m) == nil && m.Pending {
		return ""
	}
	if m.Branch != "" {
		return c.Main + " is red since " + m.Branch + " landed: the queue waits until a green run on " + c.Main + " clears it."
	}
	return c.Main + " is red: the queue waits until a green run on " + c.Main + " clears it."
}

type queueRun struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Branch  string `json:"branch"`
	Is      string `json:"is"`
	Text    string `json:"text"`
	Title   string `json:"title,omitempty"`
	Started string `json:"started"`
	Ended   string `json:"ended,omitempty"`
	Seconds int    `json:"seconds"`
}

type queueOut struct {
	gate
	Main     string     `json:"main"`
	Land     string     `json:"land,omitempty"`
	Bound    bool       `json:"bound"`
	Merge    bool       `json:"merge,omitempty"` // the queue is COGNIX[WS]'s Merge: read only here
	Error    string     `json:"error,omitempty"`
	Red      string     `json:"red,omitempty"`
	Rows     []queueRow `json:"rows"`
	Running  []queueRun `json:"running"`
	Verdicts []queueRun `json:"verdicts"`
}

func mainDir(ws pageWorkspace) string {
	if t := worktrees(ws.Path); len(t) > 0 {
		return t[0].Path
	}
	return ws.Path
}

func (s *server) pageQueue(w http.ResponseWriter, r *http.Request, ws pageWorkspace) {
	out := queueOut{gate: readGate(ws), Rows: []queueRow{}, Running: []queueRun{}, Verdicts: []queueRun{}}
	if out.State != "ready" {
		writeJSON(w, out)
		return
	}
	c := out.Contract
	main := mainDir(ws)
	out.Main, out.Land, out.Bound = c.Main, c.Land, c.Queue != nil
	var mergeEnded []hostapi.QueueEntry
	agents, _ := ws.host.Agents(ws.ID)
	agentOf := map[string]string{}
	for _, t := range worktrees(ws.Path) {
		for _, a := range agents {
			if a.WorkDir != "" && filepath.Clean(a.WorkDir) == filepath.Clean(t.Path) && t.Branch != "" {
				agentOf[t.Branch] = a.Name
			}
		}
	}
	if rows, err := readQueue(c, main); err != nil {
		out.Error = err.Error()
	} else if rows != nil {
		for i := range rows {
			rows[i].Agent = agentOf[rows[i].Branch]
		}
		out.Rows = rows
	}
	if c.Queue == nil && c.Land == "" {
		out.Bound, out.Merge = true, true
		active, ended, err := ws.host.MergeQueue(ws.ID)
		if err != nil {
			out.Error = err.Error()
		}
		out.Rows = mergeRows(active, c.Main, agentOf)
		mergeEnded = ended
	}
	out.Red = redLine(c, main)
	dirs := map[string]bool{}
	for _, t := range worktrees(ws.Path) {
		dirs[filepath.Clean(t.Path)] = true
	}
	for _, run := range s.allRuns() {
		if !dirs[filepath.Clean(run.WorkDir)] {
			continue
		}
		q := queueRun{ID: run.ID, Kind: run.Kind, Branch: run.Branch, Started: run.StartedAt.Format(time.RFC3339)}
		if run.EndedAt != nil {
			q.Ended = run.EndedAt.Format(time.RFC3339)
			q.Seconds = int(run.EndedAt.Sub(run.StartedAt).Seconds())
		}
		switch {
		case run.State == "running":
			q.Is, q.Text = "running", "Running"
			out.Running = append(out.Running, q)
			continue
		case run.State == "interrupted":
			q.Is, q.Text = "owed", "Cut short by a restart"
		case run.Kind == "land" && run.ExitCode == 0:
			q.Is, q.Text = "landed", "Landed"
		case run.Kind == "land":
			q.Is, q.Text = "failed", "Land stopped"
		case run.Kind == "close":
			st := closeState(run)
			q.Is, q.Text, q.Title = st.Is, st.Text, st.Title
		default:
			st := checkState(run)
			q.Is, q.Text = st.Is, st.Text
		}
		if len(out.Verdicts) < 8 {
			out.Verdicts = append(out.Verdicts, q)
		}
	}
	out.Verdicts = append(out.Verdicts, mergeVerdicts(mergeEnded, 8-len(out.Verdicts))...)
	writeJSON(w, out)
}

// pageQueueChange moves or stops one ticket through the contract.
func (s *server) pageQueueChange(w http.ResponseWriter, r *http.Request, ws pageWorkspace) {
	var req struct {
		Ticket string `json:"ticket"`
		Action string `json:"action"` // earlier | later | first | stop
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || !ticketID.MatchString(req.Ticket) {
		pageErr(w, http.StatusBadRequest, "Name a ticket and a move.")
		return
	}
	g := readGate(ws)
	if g.State != "ready" || g.Contract.Queue == nil {
		pageErr(w, http.StatusConflict, "This project's contract binds no queue.")
		return
	}
	q, main := g.Contract.Queue, mainDir(ws)
	rows, err := readQueue(g.Contract, main)
	if err != nil {
		pageErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var line string
	switch req.Action {
	case "earlier", "later", "first":
		if q.Move == "" {
			pageErr(w, http.StatusConflict, "This project's contract does not say how to move a ticket.")
			return
		}
		args := moveArgs(rows, req.Ticket, req.Action)
		if args == "" {
			pageErr(w, http.StatusConflict, "That ticket can't move there now.")
			return
		}
		line = strings.ReplaceAll(strings.ReplaceAll(q.Move, "{ticket}", req.Ticket), "{move}", args)
	case "stop":
		if q.Drop == "" {
			pageErr(w, http.StatusConflict, "This project's contract does not say how to stop a ticket.")
			return
		}
		ok := false
		for _, row := range rows {
			ok = ok || (row.Ticket == req.Ticket && contains(row.Actions, "stop"))
		}
		if !ok {
			pageErr(w, http.StatusConflict, "That ticket can't be stopped now.")
			return
		}
		line = strings.ReplaceAll(q.Drop, "{ticket}", req.Ticket)
	default:
		pageErr(w, http.StatusBadRequest, "The move is earlier, later, first or stop.")
		return
	}
	if _, err := queueCommand(main, line, 20*time.Second); err != nil {
		pageErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.announce(&Run{ID: "queue", Kind: "queue", State: "done", WorkDir: main})
	writeJSON(w, map[string]any{"ok": true})
}

// pageRun starts a check, close or land of one of the workspace's
// branches for the person, the same run an agent's tool starts.
func (s *server) pageRun(w http.ResponseWriter, r *http.Request, ws pageWorkspace) {
	var req struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || !runKinds[req.Kind] {
		pageErr(w, http.StatusBadRequest, "Name a branch's folder and check, close or land.")
		return
	}
	g := readGate(ws)
	if g.State != "ready" {
		pageErr(w, http.StatusConflict, "The Dev flow can't run in this project yet.")
		return
	}
	trees := worktrees(ws.Path)
	var t *worktree
	for i := range trees {
		if i > 0 && filepath.Clean(trees[i].Path) == filepath.Clean(req.Path) && trees[i].Branch != "" && trees[i].Branch != g.Contract.Main {
			t = &trees[i]
		}
	}
	if t == nil {
		pageErr(w, http.StatusNotFound, "That branch is not in flight in this workspace.")
		return
	}
	if req.Kind == "land" && g.Contract.Land == "" {
		pageErr(w, http.StatusConflict, "This project lands through Merge: ask the branch's agent to land, or use its Merge card.")
		return
	}
	run := s.startRun(startReq{WorkDir: t.Path, Kind: req.Kind, Args: []string{}, Workspace: ws.ID})
	writeJSON(w, run)
}

// mergeRows shows Merge's waiting and running entries; moving and stopping
// them stays on their Merge cards, so they carry no actions here.
func mergeRows(entries []hostapi.QueueEntry, target string, agentOf map[string]string) []queueRow {
	rows := []queueRow{}
	for _, e := range entries {
		r := queueRow{Ticket: e.ID, Branch: e.Branch, Agent: agentOf[e.Branch], State: e.State, Actions: []string{}}
		switch e.State {
		case "running":
			r.Is, r.Text, r.Reason = "landing", "Landing", "Holding "+target+". Wait for the verdict."
		case "authorized":
			r.Is, r.Text = "closed", "Ready"
		default:
			r.Is, r.Text, r.Reason = "owed", "Waiting for you", "Merge it from its Merge card."
		}
		if t, err := time.Parse(time.RFC3339Nano, e.CreatedAt); err == nil {
			r.Age = ageLabel(int64(time.Since(t).Seconds()))
		}
		rows = append(rows, r)
	}
	return rows
}

func mergeVerdicts(ended []hostapi.QueueEntry, room int) []queueRun {
	out := []queueRun{}
	for _, e := range ended {
		if len(out) >= room {
			break
		}
		q := queueRun{ID: e.ID, Kind: "merge", Branch: e.Branch, Started: e.CreatedAt, Ended: e.UpdatedAt}
		switch e.State {
		case "done":
			q.Is, q.Text = "landed", "Merged"
		case "failed":
			q.Is, q.Text, q.Title = "failed", "Merge failed", e.Note
		default:
			q.Is, q.Text = "none", "Withdrawn"
		}
		out = append(out, q)
	}
	return out
}
