// Package runner runs one check: its command through /bin/sh in the
// checkout, bounded in time, retried once when it fails. With a JUnit XML
// report it names the failing tests, so a test that passes on the retry is
// recorded as flaky instead of hidden, and a failure made only of
// quarantined tests is reported without failing the check.
package runner

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Spec is what to run.
type Spec struct {
	Name       string
	Command    string
	Dir        string
	LogPath    string
	JUnit      string // glob relative to Dir, optional
	Timeout    time.Duration
	Env        []string
	NoRetry    bool
	Quarantine map[string]bool // test names that do not fail the check
}

// Result is what happened.
type Result struct {
	Name        string        `json:"name"`
	OK          bool          `json:"ok"`
	Duration    time.Duration `json:"durationNs"`
	LogPath     string        `json:"log"`
	Tail        string        `json:"tail,omitempty"`
	Retried     bool          `json:"retried,omitempty"`
	Flaky       []string      `json:"flaky,omitempty"`
	Failed      []string      `json:"failedTests,omitempty"`
	Quarantined []string      `json:"quarantined,omitempty"`
	TimedOut    bool          `json:"timedOut,omitempty"`
	Reused      bool          `json:"reused,omitempty"`
}

// Run runs the spec.
func Run(ctx context.Context, s Spec) Result {
	start := time.Now()
	if s.Timeout <= 0 {
		s.Timeout = 45 * time.Minute
	}
	res := Result{Name: s.Name, LogPath: s.LogPath}
	if err := os.MkdirAll(filepath.Dir(s.LogPath), 0o755); err != nil {
		res.Tail = err.Error()
		return res
	}
	log, err := os.Create(s.LogPath)
	if err != nil {
		res.Tail = err.Error()
		return res
	}
	defer log.Close()

	ok, timedOut := once(ctx, s, log, 1)
	first := failedTests(s)
	if !ok && !timedOut && !s.NoRetry {
		res.Retried = true
		fmt.Fprintf(log, "\n--- flow: %s failed; running it once more ---\n", s.Name)
		ok, timedOut = once(ctx, s, log, 2)
		second := failedTests(s)
		if ok {
			res.Flaky = first
		} else {
			res.Failed = second
		}
	} else if !ok {
		res.Failed = first
	}
	res.TimedOut = timedOut
	if !ok && len(res.Failed) > 0 && len(s.Quarantine) > 0 {
		var rest []string
		for _, t := range res.Failed {
			if s.Quarantine[t] {
				res.Quarantined = append(res.Quarantined, t)
			} else {
				rest = append(rest, t)
			}
		}
		if len(rest) == 0 && !timedOut {
			ok = true
		}
		res.Failed = rest
	}
	res.OK = ok
	res.Duration = time.Since(start)
	res.Tail = tail(s.LogPath, 30)
	return res
}

func once(ctx context.Context, s Spec, log io.Writer, attempt int) (ok, timedOut bool) {
	if s.JUnit != "" {
		if m, _ := filepath.Glob(filepath.Join(s.Dir, s.JUnit)); len(m) > 0 {
			for _, f := range m {
				_ = os.Remove(f)
			}
		}
	}
	cctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", s.Command)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	fmt.Fprintf(log, "$ %s   (attempt %d)\n", s.Command, attempt)
	err := cmd.Run()
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(log, "\n--- flow: %s stopped after %s ---\n", s.Name, s.Timeout)
		return false, true
	}
	return err == nil, false
}

// failedTests reads the JUnit report(s) and names the failing test cases as
// "classname.name".
func failedTests(s Spec) []string {
	if s.JUnit == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, s.JUnit))
	seen := map[string]bool{}
	for _, f := range files {
		for _, t := range parseJUnit(f) {
			seen[t] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

type junitCase struct {
	Class   string    `xml:"classname,attr"`
	Name    string    `xml:"name,attr"`
	Failure *struct{} `xml:"failure"`
	Error   *struct{} `xml:"error"`
}

// parseJUnit streams any document of testsuites/testsuite/testcase.
func parseJUnit(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	var out []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "testcase" {
			continue
		}
		var c junitCase
		if dec.DecodeElement(&c, &se) != nil {
			continue
		}
		if c.Failure != nil || c.Error != nil {
			name := c.Name
			if c.Class != "" {
				name = c.Class + "." + c.Name
			}
			out = append(out, name)
		}
	}
}

// ParseQuarantine reads a quarantine file: one test name per line, "#"
// comments and blank lines ignored, anything after the first space is the
// reason.
func ParseQuarantine(path string) map[string]bool {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out[strings.Fields(l)[0]] = true
	}
	return out
}

func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	ls := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
