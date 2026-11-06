package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cognixws/cognix-devflow/internal/contract"
)

func age(n int64) *int64 { return &n }

// TestQueueRows is the decision table for what a person may do to a ticket:
// position × state × batch claim → moves and stop.
func TestQueueRows(t *testing.T) {
	claimed := &struct {
		State string `json:"state"`
	}{State: "claimed"}
	cases := []struct {
		name    string
		tickets []queueTicket
		at      int
		want    []string
		is      string
	}{
		{"landing head neither moves nor stops", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "landing"}, {Ticket: "b", Branch: "feat/b", State: "ready"}}, 0, []string{}, "landing"},
		{"right behind a landing head: only later or stop", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "landing"}, {Ticket: "b", Branch: "feat/b", State: "ready"}, {Ticket: "c", Branch: "feat/c", State: "ready"}}, 1, []string{"later", "stop"}, "closed"},
		{"last behind a landing head: earlier, first, stop", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "landing"}, {Ticket: "b", Branch: "feat/b", State: "ready"}, {Ticket: "c", Branch: "feat/c", State: "ready"}}, 2, []string{"earlier", "first", "stop"}, "closed"},
		{"first with nobody landing: later and stop", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "ready"}, {Ticket: "b", Branch: "feat/b", State: "ready"}}, 0, []string{"later", "stop"}, "closed"},
		{"preparing (closing) may stop", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "ready"}, {Ticket: "b", Branch: "feat/b", State: "preparing"}}, 1, []string{"earlier", "first", "stop"}, "running"},
		{"claimed by a batch: no stop", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "ready"}, {Ticket: "b", Branch: "feat/b", State: "ready", Claim: claimed}}, 1, []string{"earlier", "first"}, "running"},
		{"an odd state moves but does not stop", []queueTicket{{Ticket: "a", Branch: "feat/a", State: "ready"}, {Ticket: "b", Branch: "feat/b", State: "verifying"}}, 1, []string{"earlier", "first"}, "none"},
	}
	for _, c := range cases {
		rows := queueRows(c.tickets, "main")
		got := rows[c.at]
		if strings.Join(got.Actions, ",") != strings.Join(c.want, ",") || got.Is != c.is {
			t.Errorf("%s: actions %v is %q, want %v %q", c.name, got.Actions, got.Is, c.want, c.is)
		}
	}
	rows := queueRows([]queueTicket{{Ticket: "bad id; rm", Branch: "x"}, {Ticket: "ok", Branch: "", State: "ready"}, {Ticket: "t1", Branch: "feat/a", State: "ready", AgeSeconds: age(600)}}, "main")
	if len(rows) != 1 || rows[0].Ticket != "t1" || rows[0].Age != "10 min" {
		t.Fatalf("unsafe or empty tickets must be dropped: %+v", rows)
	}
}

func TestMoveArgs(t *testing.T) {
	landing := queueRows([]queueTicket{{Ticket: "a", Branch: "feat/a", State: "landing"}, {Ticket: "b", Branch: "feat/b", State: "ready"}, {Ticket: "c", Branch: "feat/c", State: "ready"}}, "main")
	free := queueRows([]queueTicket{{Ticket: "a", Branch: "feat/a", State: "ready"}, {Ticket: "b", Branch: "feat/b", State: "ready"}}, "main")
	cases := []struct {
		rows        []queueRow
		ticket, how string
		want        string
	}{
		{landing, "c", "earlier", "--before b"},
		{landing, "c", "first", "--after a"},
		{landing, "b", "later", "--after c"},
		{landing, "b", "earlier", ""},
		{landing, "a", "later", ""},
		{free, "b", "first", "--front"},
		{free, "zz", "first", ""},
	}
	for _, c := range cases {
		if got := moveArgs(c.rows, c.ticket, c.how); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.ticket, c.how, got, c.want)
		}
	}
}

func TestContractQueue(t *testing.T) {
	base := `{"flow":1,"queue":%s}`
	cases := []struct {
		q  string
		ok bool
	}{
		{`{"list":"node q.mjs list","move":"node q.mjs move {ticket} {move}","drop":"node q.mjs drop {ticket}","red":"var/land-red"}`, true},
		{`{"list":"node q.mjs list"}`, true},
		{`{"move":"x {ticket} {move}"}`, false},
		{`{"list":"l","move":"x {ticket}"}`, false},
		{`{"list":"l","drop":"x"}`, false},
		{`{"list":"l","red":"/tmp/red"}`, false},
		{`{"list":"l","red":"../red"}`, false},
		{`{"list":"l","other":"x"}`, false},
	}
	for _, c := range cases {
		_, err := contract.Parse([]byte(strings.Replace(base, "%s", c.q, 1)))
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok=%v", c.q, err, c.ok)
		}
	}
}

// queueContract binds a queue the test controls: the list is a file in the
// main checkout, moves and stops append to a log.
const queueContract = `{"setup":[],"extensions":{"picode-devflow":{"flow":1,
 "checks":{"unit":{"run":"echo unit >> runs.log","when":["src/**"]}},
 "land":"echo landed {branch} >> lands.log",
 "queue":{"list":"cat queue.json","move":"echo move {ticket} {move} >> moves.log","drop":"echo drop {ticket} >> moves.log","red":"red.json"}}}}`

func TestQueuePageThroughTheRelay(t *testing.T) {
	t.Setenv("FLOW_TEST_AS_CLI", "1")
	dir := repo(t)
	write(t, dir, map[string]string{".picode.json": queueContract, ".gitignore": "runs.log\nqueue.json\nmoves.log\nred.json\nlands.log\n.worktrees/\n"})
	git(t, dir, "add", ".picode.json", ".gitignore")
	git(t, dir, "commit", "-qm", "queue")
	wt := filepath.Join(dir, ".worktrees", "b")
	git(t, dir, "worktree", "add", "-q", "-b", "feat/b", wt)
	write(t, wt, map[string]string{"src/b.go": "package b\n"})
	git(t, wt, "add", "src/b.go")
	git(t, wt, "commit", "-qm", "b")
	write(t, dir, map[string]string{"queue.json": `{"tickets":[
		{"ticket":"t1","branch":"feat/a","state":"landing","ageSeconds":600},
		{"ticket":"t2","branch":"feat/b","state":"ready","ageSeconds":240},
		{"ticket":"t3","branch":"feat/c","state":"ready","ageSeconds":60}]}`})

	var section json.RawMessage
	{
		var top struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		}
		json.Unmarshal([]byte(queueContract), &top)
		section = top.Extensions["picode-devflow"]
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ext/v1/workspaces":
			writeJSON(w, map[string]any{"workspaces": []map[string]any{{"id": "w1", "name": "W", "path": dir, "extensionOn": true}}})
		case r.URL.Path == "/api/ext/v1/workspaces/w1/project":
			writeJSON(w, map[string]any{"found": true, "trusted": true, "section": section})
		case r.URL.Path == "/api/ext/v1/agents":
			writeJSON(w, map[string]any{"agents": []map[string]any{{"id": "ag-b", "name": "bee", "cli": "pi", "workDir": wt}}})
		default:
			w.WriteHeader(204)
		}
	}))
	defer host.Close()
	t.Setenv("PICODE_URL", host.URL)
	t.Setenv("PICODE_EXT_TOKEN", "tok")
	t.Setenv("PICODE_EXT_PROXY_SECRET", "sec")
	exe, _ := os.Executable()
	s := &server{data: t.TempDir(), exe: exe, sem: make(chan struct{}, 2), runs: map[string]*Run{}, done: map[string]chan struct{}{}}
	call := func(h func(http.ResponseWriter, *http.Request, pageWorkspace), method, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, "/page/x?workspace=w1", bytes.NewBufferString(body))
		req.Header.Set("X-PiCode-Proxy-Secret", "sec")
		rec := httptest.NewRecorder()
		s.page(h)(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, q := call(s.pageQueue, "GET", "")
	rows, _ := q["rows"].([]any)
	if code != 200 || len(rows) != 3 || q["bound"] != true || q["red"] != nil {
		t.Fatalf("queue: %d %v", code, q)
	}
	if r1 := rows[1].(map[string]any); r1["agent"] != "bee" || r1["age"] != "4 min" {
		t.Fatalf("row 2 names its agent and age: %v", r1)
	}
	if code, _ := call(s.pageQueueChange, "POST", `{"ticket":"t3","action":"earlier"}`); code != 200 {
		t.Fatalf("move: %d", code)
	}
	if code, _ := call(s.pageQueueChange, "POST", `{"ticket":"t1","action":"stop"}`); code != http.StatusConflict {
		t.Fatalf("a landing ticket must not stop: %d", code)
	}
	if code, _ := call(s.pageQueueChange, "POST", `{"ticket":"t2","action":"first"}`); code != http.StatusConflict {
		t.Fatalf("nothing passes a landing head: %d", code)
	}
	if code, _ := call(s.pageQueueChange, "POST", `{"ticket":"t2; touch x","action":"stop"}`); code != http.StatusBadRequest {
		t.Fatalf("an unsafe ticket must be refused: %d", code)
	}
	if code, _ := call(s.pageQueueChange, "POST", `{"ticket":"t2","action":"stop"}`); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "moves.log"))
	if string(log) != "move t3 --before t2\ndrop t2\n" {
		t.Fatalf("moves.log: %q", log)
	}

	write(t, dir, map[string]string{"red.json": `{"branch":"feat/z"}`})
	if _, q := call(s.pageQueue, "GET", ""); !strings.Contains(q["red"].(string), "main is red since feat/z") {
		t.Fatalf("red: %v", q["red"])
	}
	write(t, dir, map[string]string{"red.json": `{"pending":true}`})
	if _, q := call(s.pageQueue, "GET", ""); q["red"] != nil {
		t.Fatalf("a pending marker is the landing row's business: %v", q["red"])
	}

	_, b := call(s.pageBoard, "GET", "")
	inFlight, _ := b["inFlight"].([]any)
	if len(inFlight) != 1 || inFlight[0].(map[string]any)["land"].(map[string]any)["text"] != "In queue · 2nd" {
		t.Fatalf("board land column: %v", b["inFlight"])
	}

	if code, _ := call(s.pageRun, "POST", `{"path":"`+dir+`","kind":"check"}`); code != http.StatusNotFound {
		t.Fatalf("the main checkout is not a branch in flight: %d", code)
	}
	code, run := call(s.pageRun, "POST", `{"path":"`+wt+`","kind":"check"}`)
	if code != 200 || run["kind"] != "check" || run["workspace"] != "w1" {
		t.Fatalf("run: %d %v", code, run)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, q := call(s.pageQueue, "GET", "")
		if v, _ := q["verdicts"].([]any); len(v) == 1 {
			if v[0].(map[string]any)["kind"] != "check" {
				t.Fatalf("verdict: %v", v[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the check never ended")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestQueueFromMerge: a contract without land reads COGNIX[WS]'s Merge queue,
// read only, and lists its ended entries as verdicts.
func TestQueueFromMerge(t *testing.T) {
	dir := repo(t)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ext/v1/workspaces":
			writeJSON(w, map[string]any{"workspaces": []map[string]any{{"id": "w1", "name": "W", "path": dir, "extensionOn": true}}})
		case "/api/ext/v1/workspaces/w1/project":
			writeJSON(w, map[string]any{"found": true, "trusted": true, "section": json.RawMessage(`{"flow":1}`)})
		case "/api/ext/v1/merge/queue":
			writeJSON(w, map[string]any{
				"entries": []map[string]any{{"id": "m1", "branch": "feat/a", "state": "running", "createdAt": time.Now().Add(-3 * time.Minute).Format(time.RFC3339Nano)}, {"id": "m2", "branch": "feat/b", "state": "waiting"}},
				"ended":   []map[string]any{{"id": "m0", "branch": "feat/z", "state": "failed", "note": "checks failed"}},
			})
		default:
			writeJSON(w, map[string]any{"agents": []any{}})
		}
	}))
	defer host.Close()
	t.Setenv("PICODE_URL", host.URL)
	t.Setenv("PICODE_EXT_TOKEN", "tok")
	t.Setenv("PICODE_EXT_PROXY_SECRET", "sec")
	s := &server{data: t.TempDir(), sem: make(chan struct{}, 1), runs: map[string]*Run{}, done: map[string]chan struct{}{}}
	req := httptest.NewRequest("GET", "/page/queue?workspace=w1", nil)
	req.Header.Set("X-PiCode-Proxy-Secret", "sec")
	rec := httptest.NewRecorder()
	s.page(s.pageQueue)(rec, req)
	var q queueOut
	json.Unmarshal(rec.Body.Bytes(), &q)
	if !q.Merge || len(q.Rows) != 2 || q.Rows[0].Text != "Landing" || q.Rows[0].Age != "3 min" || q.Rows[1].Text != "Waiting for you" || len(q.Rows[1].Actions) != 0 {
		t.Fatalf("rows: %+v", q)
	}
	if len(q.Verdicts) != 1 || q.Verdicts[0].Kind != "merge" || q.Verdicts[0].Is != "failed" || q.Verdicts[0].Title != "checks failed" {
		t.Fatalf("verdicts: %+v", q.Verdicts)
	}
	req = httptest.NewRequest("POST", "/page/queue?workspace=w1", strings.NewReader(`{"ticket":"m2","action":"stop"}`))
	req.Header.Set("X-PiCode-Proxy-Secret", "sec")
	rec = httptest.NewRecorder()
	s.page(s.pageQueueChange)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("Merge's queue is read only here: %d", rec.Code)
	}
}

// Decision table: which verdicts the Queue tab keeps.
func TestPickVerdicts(t *testing.T) {
	run := func(id, ended string) queueRun { return queueRun{ID: id, Kind: "check", Ended: ended} }
	merge := func(id, ended string) queueRun { return queueRun{ID: id, Kind: "merge", Ended: ended} }
	var tenRuns []queueRun
	for i := 0; i < 10; i++ {
		tenRuns = append(tenRuns, run(fmt.Sprintf("r%d", i), fmt.Sprintf("2026-11-06T12:%02d:00Z", 30+i)))
	}
	rows := []struct {
		name   string
		runs   []queueRun
		merges []queueRun
		want   string
	}{
		{"few: all, newest first", []queueRun{run("r1", "2026-11-06T10:00:00Z")}, []queueRun{merge("m1", "2026-11-06T11:00:00Z")}, "m1 r1"},
		{"many checks, old merges: the floor keeps three", tenRuns, []queueRun{merge("m1", "2026-11-06T09:00:00Z"), merge("m2", "2026-11-06T08:00:00Z"), merge("m3", "2026-11-06T07:00:00Z"), merge("m4", "2026-11-06T06:00:00Z")}, "r9 r8 r7 r6 r5 m1 m2 m3"},
		{"one old merge: kept", tenRuns, []queueRun{merge("m1", "2026-11-06T09:00:00Z")}, "r9 r8 r7 r6 r5 r4 r3 m1"},
		{"newest merges beat the floor", tenRuns[:2], []queueRun{merge("m1", "2026-11-06T13:00:00Z"), merge("m2", "2026-11-06T13:01:00Z"), merge("m3", "2026-11-06T13:02:00Z"), merge("m4", "2026-11-06T13:03:00Z"), merge("m5", "2026-11-06T13:04:00Z"), merge("m6", "2026-11-06T13:05:00Z"), merge("m7", "2026-11-06T13:06:00Z")}, "m7 m6 m5 m4 m3 m2 m1 r1"},
		{"no merges: newest eight runs", tenRuns, nil, "r9 r8 r7 r6 r5 r4 r3 r2"},
		{"a running row sorts by its start", []queueRun{{ID: "x", Kind: "check", Started: "2026-11-06T14:00:00Z"}}, []queueRun{merge("m1", "2026-11-06T13:00:00Z")}, "x m1"},
	}
	for _, r := range rows {
		var got []string
		for _, q := range pickVerdicts(r.runs, r.merges, verdictRoom, mergeVerdictFloor) {
			got = append(got, q.ID)
		}
		if strings.Join(got, " ") != r.want {
			t.Errorf("%s: got %v, want %s", r.name, got, r.want)
		}
	}
}
