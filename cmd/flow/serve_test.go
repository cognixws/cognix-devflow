package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the server's child runs reuse the test binary as the CLI.
func TestMain(m *testing.M) {
	if os.Getenv("FLOW_TEST_AS_CLI") == "1" {
		os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func toolCall(t *testing.T, data, dir, tool string, args map[string]any) toolResult {
	t.Helper()
	req := map[string]any{"protocol": "picode-tools/1", "requestId": "r1", "capability": "flow", "tool": tool, "arguments": args,
		"context": map[string]any{"agent": "a1", "workspace": "w1", "workDir": dir, "dataDir": data}}
	in, _ := json.Marshal(req)
	old, _ := os.Getwd()
	defer os.Chdir(old)
	var out bytes.Buffer
	if err := cmdToolsExec(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var res toolResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%s", out.String())
	}
	return res
}

func TestToolsThroughTheProcess(t *testing.T) {
	t.Setenv("FLOW_TEST_AS_CLI", "1")
	dir := repo(t)
	git(t, dir, "switch", "-qc", "feat/tools")
	write(t, dir, map[string]string{"src/a.go": "package a\n"})
	git(t, dir, "add", "src/a.go")
	git(t, dir, "commit", "-qm", "a")
	data := t.TempDir()

	// Without the process, long tools say how to run them in a shell.
	if r := toolCall(t, data, dir, "check", map[string]any{}); !r.IsError || !strings.Contains(r.Content[0].Text, "not running") {
		t.Fatalf("down: %+v", r)
	}
	go cmdServe([]string{"--port=0", "--data=" + data}, &bytes.Buffer{})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(data, "serve.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := toolCall(t, data, dir, "check", map[string]any{})
	if r.IsError || !strings.Contains(r.Content[0].Text, "✓ unit") {
		t.Fatalf("check: %+v", r)
	}
	r = toolCall(t, data, dir, "close", map[string]any{})
	if r.IsError || !strings.Contains(r.Content[0].Text, "no handoff note") || r.StructuredContent["run"] == nil {
		t.Fatalf("close: %+v", r)
	}
	st := toolCall(t, data, dir, "status", map[string]any{})
	if st.IsError || st.StructuredContent["branch"] != "feat/tools" || !strings.Contains(st.Content[0].Text, "Next:") {
		t.Fatalf("status: %+v", st)
	}
	d := toolCall(t, data, dir, "debt", map[string]any{"topic": "cart", "text": "Refunds keep the payment id"})
	if d.IsError || !strings.Contains(d.Content[0].Text, "docs/handoff/open/cart.md") {
		t.Fatalf("debt: %+v", d)
	}
	g := toolCall(t, data, dir, "guide", map[string]any{"topic": "handoff-note"})
	if g.IsError || !strings.Contains(g.Content[0].Text, "# Handoff note") {
		t.Fatalf("guide: %+v", g)
	}
	l := toolCall(t, data, dir, "land", map[string]any{})
	if !l.IsError || !strings.Contains(l.Content[0].Text, "uncommitted") {
		t.Fatalf("land with the debt file uncommitted: %+v", l)
	}
}

func TestStillRunningAnswersWithTheRun(t *testing.T) {
	t.Setenv("FLOW_TEST_AS_CLI", "1")
	old := toolWait
	toolWait = 1
	defer func() { toolWait = old }()
	dir := repo(t)
	write(t, dir, map[string]string{".picode.json": strings.Replace(contractFile, `echo unit >> runs.log; test ! -f src/fail`, `sleep 3`, 1)})
	git(t, dir, "add", ".picode.json")
	git(t, dir, "commit", "-qm", "slow")
	git(t, dir, "switch", "-qc", "feat/slow")
	write(t, dir, map[string]string{"src/a.go": "x"})
	git(t, dir, "add", "src/a.go")
	git(t, dir, "commit", "-qm", "a")
	data := t.TempDir()
	go cmdServe([]string{"--port=0", "--data=" + data}, &bytes.Buffer{})
	for i := 0; i < 250; i++ {
		if _, err := os.Stat(filepath.Join(data, "serve.json")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := toolCall(t, data, dir, "check", map[string]any{})
	id, _ := r.StructuredContent["run"].(string)
	if r.IsError || id == "" || !strings.Contains(r.Content[0].Text, "Still running") {
		t.Fatalf("%+v", r)
	}
	toolWait = 10
	r = toolCall(t, data, dir, "wait", map[string]any{"run": id})
	if r.IsError || !strings.Contains(r.Content[0].Text, "✓ unit") {
		t.Fatalf("wait: %+v", r)
	}
}
