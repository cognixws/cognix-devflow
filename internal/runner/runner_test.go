package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func spec(t *testing.T, cmd string) Spec {
	dir := t.TempDir()
	return Spec{Name: "t", Command: cmd, Dir: dir, LogPath: filepath.Join(dir, "log", "t.log"), Timeout: 10 * time.Second}
}

func TestPassAndFail(t *testing.T) {
	if r := Run(context.Background(), spec(t, "true")); !r.OK || r.Retried {
		t.Fatalf("pass: %+v", r)
	}
	r := Run(context.Background(), spec(t, "echo boom; false"))
	if r.OK || !r.Retried || !strings.Contains(r.Tail, "boom") {
		t.Fatalf("fail: %+v", r)
	}
}

func TestFlakyPassesOnRetryAndIsNamed(t *testing.T) {
	s := spec(t, `if [ -f once ]; then cat > r.xml <<'X'
<testsuite><testcase classname="cart" name="ok"/></testsuite>
X
exit 0; fi; touch once; cat > r.xml <<'X'
<testsuites><testsuite><testcase classname="cart" name="half"><failure/></testcase></testsuite></testsuites>
X
exit 1`)
	s.JUnit = "r.xml"
	r := Run(context.Background(), s)
	if !r.OK || !r.Retried || len(r.Flaky) != 1 || r.Flaky[0] != "cart.half" {
		t.Fatalf("%+v", r)
	}
}

func TestQuarantineOnlyFailurePasses(t *testing.T) {
	s := spec(t, `cat > r.xml <<'X'
<testsuite><testcase classname="a" name="b"><error/></testcase></testsuite>
X
exit 1`)
	s.JUnit = "r.xml"
	s.Quarantine = map[string]bool{"a.b": true}
	r := Run(context.Background(), s)
	if !r.OK || len(r.Quarantined) != 1 || len(r.Failed) != 0 {
		t.Fatalf("%+v", r)
	}
	s.Quarantine = map[string]bool{"x.y": true}
	if r := Run(context.Background(), s); r.OK || len(r.Failed) != 1 {
		t.Fatalf("not quarantined must fail: %+v", r)
	}
}

func TestTimeoutKillsTheGroup(t *testing.T) {
	s := spec(t, "sleep 30 & sleep 30")
	s.Timeout = 300 * time.Millisecond
	s.NoRetry = true
	start := time.Now()
	r := Run(context.Background(), s)
	if r.OK || !r.TimedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("%+v after %s", r, time.Since(start))
	}
}

func TestParseQuarantine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "q.txt")
	os.WriteFile(p, []byte("# header\n\npkg.TestA since 2026-11-05 races\npkg.TestB\n"), 0o644)
	q := ParseQuarantine(p)
	if !q["pkg.TestA"] || !q["pkg.TestB"] || len(q) != 2 {
		t.Fatalf("%v", q)
	}
}
