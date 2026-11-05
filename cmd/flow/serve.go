package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Run is one long command the background process ran for an agent or a
// person: check, close or land in a checkout.
type Run struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	WorkDir   string          `json:"workDir"`
	Args      []string        `json:"args"`
	State     string          `json:"state"` // running | done | interrupted
	ExitCode  int             `json:"exitCode"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Stderr    string          `json:"stderr,omitempty"`
}

var runKinds = map[string]bool{"check": true, "close": true, "land": true}

type server struct {
	data  string
	token string
	exe   string
	sem   chan struct{}
	mu    sync.Mutex
	runs  map[string]*Run
	done  map[string]chan struct{}
}

// serveInfo is what tool calls read to reach the process.
type serveInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func cmdServe(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", envInt("PICODE_EXT_PORT", 0), "loopback port (default: PICODE_EXT_PORT or any free port)")
	data := fs.String("data", os.Getenv("PICODE_EXT_DATA"), "data folder (default: PICODE_EXT_DATA)")
	parallel := fs.Int("parallel", 2, "runs at once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" {
		return errors.New("serve needs --data or PICODE_EXT_DATA")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	s := &server{data: *data, exe: exe, sem: make(chan struct{}, max(1, *parallel)), runs: map[string]*Run{}, done: map[string]chan struct{}{}}
	if err := os.MkdirAll(s.runDir(), 0o755); err != nil {
		return err
	}
	s.load()
	tok := make([]byte, 32)
	rand.Read(tok)
	s.token = hex.EncodeToString(tok)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		return err
	}
	info := serveInfo{Port: ln.Addr().(*net.TCPAddr).Port, Token: s.token, PID: os.Getpid()}
	b, _ := json.Marshal(info)
	if err := writeAtomic(filepath.Join(s.data, "serve.json"), b, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "flow serve on 127.0.0.1:%d\n", info.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", s.auth(s.start))
	mux.HandleFunc("GET /runs/{id}", s.auth(s.get))
	mux.HandleFunc("GET /runs", s.auth(s.list))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *server) runDir() string { return filepath.Join(s.data, "runs") }

// load reads earlier runs; one still marked running was cut by a restart.
func (s *server) load() {
	ents, _ := os.ReadDir(s.runDir())
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(s.runDir(), e.Name()))
		if err != nil {
			continue
		}
		var r Run
		if json.Unmarshal(b, &r) != nil || r.ID == "" {
			continue
		}
		if r.State == "running" {
			r.State = "interrupted"
			now := time.Now().UTC()
			r.EndedAt = &now
			s.save(&r)
		}
		s.runs[r.ID] = &r
	}
}

func (s *server) save(r *Run) {
	b, _ := json.Marshal(r)
	_ = writeAtomic(filepath.Join(s.runDir(), r.ID+".json"), b, 0o644)
}

func (s *server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Flow-Token")), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

type startReq struct {
	WorkDir string   `json:"workDir"`
	Kind    string   `json:"kind"`
	Args    []string `json:"args"`
}

func (s *server) start(w http.ResponseWriter, r *http.Request) {
	var req startReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || !runKinds[req.Kind] || !filepath.IsAbs(req.WorkDir) {
		http.Error(w, "need an absolute workDir and kind check, close or land", http.StatusBadRequest)
		return
	}
	for _, a := range req.Args {
		if !strings.HasPrefix(a, "--") || strings.ContainsAny(a, " \n") {
			http.Error(w, "args must be flags", http.StatusBadRequest)
			return
		}
	}
	if st, err := os.Stat(req.WorkDir); err != nil || !st.IsDir() {
		http.Error(w, "workDir is not a folder", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	for _, x := range s.runs {
		if x.State == "running" && x.WorkDir == req.WorkDir && x.Kind == req.Kind && strings.Join(x.Args, " ") == strings.Join(req.Args, " ") {
			s.mu.Unlock()
			writeJSON(w, x)
			return
		}
	}
	id := time.Now().UTC().Format("20060102T150405") + "-" + randHex(3)
	run := &Run{ID: id, Kind: req.Kind, WorkDir: req.WorkDir, Args: append([]string{}, req.Args...), State: "running", StartedAt: time.Now().UTC()}
	s.runs[id] = run
	done := make(chan struct{})
	s.done[id] = done
	s.save(run)
	s.mu.Unlock()
	go s.execute(run, done)
	writeJSON(w, run)
}

func (s *server) execute(run *Run, done chan struct{}) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	cmd := exec.Command(s.exe, append([]string{run.Kind, "--json"}, run.Args...)...)
	cmd.Dir = run.WorkDir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		code = 1
	}
	s.mu.Lock()
	now := time.Now().UTC()
	run.State, run.ExitCode, run.EndedAt = "done", code, &now
	if out := bytes.TrimSpace(stdout.Bytes()); json.Valid(out) && len(out) > 0 {
		run.Result = out
	}
	run.Stderr = strings.Join(lastLines(strings.TrimSpace(stderr.String()), 20), "\n")
	s.save(run)
	delete(s.done, run.ID)
	s.mu.Unlock()
	close(done)
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if wait > 50 {
		wait = 50
	}
	s.mu.Lock()
	run, ok := s.runs[id]
	done := s.done[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "no such run", http.StatusNotFound)
		return
	}
	if done != nil && wait > 0 {
		select {
		case <-done:
		case <-time.After(time.Duration(wait) * time.Second):
		case <-r.Context().Done():
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, run)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("workDir")
	s.mu.Lock()
	out := []*Run{}
	for _, x := range s.runs {
		if dir == "" || x.WorkDir == dir {
			out = append(out, x)
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp-" + randHex(4)
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
