package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// toolRequest is the picode-tools/1 request on stdin (ADR-0240).
type toolRequest struct {
	Protocol   string          `json:"protocol"`
	RequestID  string          `json:"requestId"`
	Capability string          `json:"capability"`
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	Settings   map[string]any  `json:"settings,omitempty"`
	Context    struct {
		Agent        string `json:"agent"`
		Workspace    string `json:"workspace"`
		WorkDir      string `json:"workDir"`
		ExtensionDir string `json:"extensionDir"`
		DataDir      string `json:"dataDir"`
	} `json:"context"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content           []toolContent  `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

// toolWait is how long a tool call waits for a run before answering
// "still running": below the host's 60-second bound on a call.
var toolWait = 40

func cmdToolsExec(stdin io.Reader, stdout io.Writer) error {
	var req toolRequest
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&req); err != nil {
		return writeTool(stdout, toolError("Malformed request: "+err.Error()))
	}
	if req.Protocol != "picode-tools/1" || req.Capability != "flow" {
		return writeTool(stdout, toolError("Unknown tool."))
	}
	dir := req.Context.WorkDir
	if dir == "" || !filepath.IsAbs(dir) {
		return writeTool(stdout, toolError("This agent has no working folder, so the Dev flow has nothing to look at."))
	}
	if err := os.Chdir(dir); err != nil {
		return writeTool(stdout, toolError("The agent's folder is gone: "+dir))
	}
	var a map[string]any
	_ = json.Unmarshal(req.Arguments, &a)
	str := func(k string) string { s, _ := a[k].(string); return s }
	boolean := func(k string) bool { b, _ := a[k].(bool); return b }
	switch req.Tool {
	case "status":
		return writeTool(stdout, local(func(w io.Writer) error { return cmdStatus(nil, w) }, func(w io.Writer) error { return cmdStatus([]string{"--json"}, w) }))
	case "check":
		var args []string
		if boolean("full") {
			args = append(args, "--full")
		}
		if o := str("only"); o != "" {
			args = append(args, "--only="+o)
		}
		return writeTool(stdout, viaProcess(req.Context.DataDir, "check", dir, args))
	case "close":
		var args []string
		if boolean("strict") {
			args = append(args, "--strict")
		}
		return writeTool(stdout, viaProcess(req.Context.DataDir, "close", dir, args))
	case "land":
		return writeTool(stdout, viaProcessAs(req.Context.DataDir, "land", dir, nil, req.Context.Agent, req.Context.Workspace))
	case "wait":
		return writeTool(stdout, waitRun(req.Context.DataDir, str("run")))
	case "debt":
		args := []string{"--topic=" + str("topic")}
		if boolean("pay") {
			args = append(args, "--pay", "--note="+str("note"))
		}
		args = append(args, str("text"))
		return writeTool(stdout, local(func(w io.Writer) error { return cmdDebt(args, w) }, nil))
	case "adr":
		args := []string{}
		if t := str("title"); t != "" {
			args = append(args, "--title="+t)
		}
		args = append(args, str("name"))
		return writeTool(stdout, local(func(w io.Writer) error { return cmdADR(args, w) }, nil))
	case "agents":
		args := []string{}
		if boolean("write") {
			args = append(args, "--write")
		}
		if boolean("force") {
			args = append(args, "--force")
		}
		var structured func(io.Writer) error
		if len(args) == 0 { // a write runs once: no second run for the JSON
			structured = func(w io.Writer) error { return cmdAgents([]string{"--json"}, w) }
		}
		return writeTool(stdout, local(func(w io.Writer) error { return cmdAgents(args, w) }, structured))
	case "guide":
		return writeTool(stdout, local(func(w io.Writer) error { return cmdGuide([]string{str("topic")}, w) }, nil))
	}
	return writeTool(stdout, toolError("Unknown tool "+req.Tool+"."))
}

// local runs a command in this process: text for the agent, JSON beside it.
func local(text func(io.Writer) error, structured func(io.Writer) error) toolResult {
	var b bytes.Buffer
	err := text(&b)
	var ee exitError
	if err != nil && !errors.As(err, &ee) {
		return toolError(err.Error())
	}
	res := toolResult{Content: []toolContent{{Type: "text", Text: strings.TrimSpace(b.String())}}, IsError: err != nil}
	if structured != nil {
		var j bytes.Buffer
		if structured(&j) == nil || j.Len() > 0 {
			var m map[string]any
			if json.Unmarshal(j.Bytes(), &m) == nil {
				res.StructuredContent = m
			}
		}
	}
	return res
}

func readServe(dataDir string) (serveInfo, error) {
	var info serveInfo
	b, err := os.ReadFile(filepath.Join(dataDir, "serve.json"))
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(b, &info)
	return info, err
}

func processDown(kind string) toolResult {
	exe, _ := os.Executable()
	return toolError(fmt.Sprintf("The Dev flow's background process is not running here, so it cannot run %s for you. Run it in your shell instead: %s %s", kind, exe, kind))
}

func call(info serveInfo, method, path string, body any) (*Run, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(info.Port)+path, rd)
	req.Header.Set("X-Flow-Token", info.Token)
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: time.Duration(toolWait+10) * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	var run Run
	return &run, json.NewDecoder(resp.Body).Decode(&run)
}

func viaProcess(dataDir, kind, dir string, args []string) toolResult {
	return viaProcessAs(dataDir, kind, dir, args, "", "")
}

func viaProcessAs(dataDir, kind, dir string, args []string, agent, workspace string) toolResult {
	info, err := readServe(dataDir)
	if err != nil {
		return processDown(kind)
	}
	run, err := call(info, "POST", "/runs", startReq{WorkDir: dir, Kind: kind, Args: args, Agent: agent, Workspace: workspace})
	if err != nil {
		return processDown(kind)
	}
	run, err = call(info, "GET", "/runs/"+run.ID+"?wait="+strconv.Itoa(toolWait), nil)
	if err != nil {
		return toolError(err.Error())
	}
	return runResult(run)
}

func waitRun(dataDir, id string) toolResult {
	if id == "" {
		return toolError("Name the run to wait for (the id a check, close or land call returned).")
	}
	info, err := readServe(dataDir)
	if err != nil {
		return processDown("that run")
	}
	run, err := call(info, "GET", "/runs/"+id+"?wait="+strconv.Itoa(toolWait), nil)
	if err != nil {
		return toolError(err.Error())
	}
	return runResult(run)
}

// runResult renders a run the way the CLI would print it.
func runResult(run *Run) toolResult {
	if run.State == "running" {
		return toolResult{
			Content:           []toolContent{{Type: "text", Text: fmt.Sprintf("Still running %s (run %s, started %s ago). Call wait with run %q to keep waiting; do other work meanwhile.", run.Kind, run.ID, time.Since(run.StartedAt).Round(time.Second), run.ID)}},
			StructuredContent: map[string]any{"run": run.ID, "state": run.State},
		}
	}
	if run.State == "interrupted" {
		return toolError("Run " + run.ID + " was cut short by a restart; start it again.")
	}
	var b bytes.Buffer
	switch run.Kind {
	case "check":
		var out checkResultOut
		if json.Unmarshal(run.Result, &out) == nil {
			if len(out.Plan) == 0 {
				fmt.Fprintf(&b, "No checks to run: %s\n", strings.Join(out.Why, "; "))
			}
			printChecks(&b, out.Checks)
		}
	case "close":
		var out closeOut
		if json.Unmarshal(run.Result, &out) == nil {
			finishClose(&b, out, false, 0)
		}
	case "land":
		var out landOut
		if json.Unmarshal(run.Result, &out) == nil {
			emitLand(&b, false, out, 0)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		text = strings.TrimSpace(run.Stderr)
	}
	var m map[string]any
	_ = json.Unmarshal(run.Result, &m)
	if m == nil {
		m = map[string]any{}
	}
	m["run"] = run.ID
	return toolResult{Content: []toolContent{{Type: "text", Text: text}}, StructuredContent: m, IsError: run.ExitCode != 0}
}

func toolError(msg string) toolResult {
	return toolResult{Content: []toolContent{{Type: "text", Text: msg}}, IsError: true}
}

func writeTool(w io.Writer, r toolResult) error {
	if r.Content == nil {
		r.Content = []toolContent{}
	}
	return json.NewEncoder(w).Encode(r)
}
