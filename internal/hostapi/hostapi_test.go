package hostapi

import (
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// countingListener counts the connections still open on the fake host's
// side. The leak's unit is one open socket per abandoned transport, so the
// count is what the fix must hold bounded.
type countingListener struct {
	net.Listener
	mu   sync.Mutex
	open int
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.open++
	l.mu.Unlock()
	return &countingConn{Conn: c, onClosed: func() { l.mu.Lock(); l.open--; l.mu.Unlock() }}, nil
}

func (l *countingListener) openConns() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.open
}

type countingConn struct {
	net.Conn
	once     sync.Once
	onClosed func()
}

func (c *countingConn) Close() error {
	c.once.Do(c.onClosed)
	return c.Conn.Close()
}

// TestFromEnvHoldsOneClientForTheProcess mirrors what flow serve does all
// day: announce per run event and the page handlers call FromEnv per call.
// Each of those must not leave a connection open: one client serves the
// process, its idle connections stay bounded.
func TestFromEnvHoldsOneClientForTheProcess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingListener{Listener: ln}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ext/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"workspaces":[{"id":"w1","name":"W","path":"/tmp","extensionOn":true}]}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(counting)
	t.Cleanup(func() { srv.Close() })

	t.Setenv("PICODE_URL", "http://"+counting.Addr().String())
	t.Setenv("PICODE_EXT_TOKEN", "tok")

	const calls = 60
	for i := 0; i < calls; i++ {
		c := FromEnv()
		if c == nil {
			t.Fatal("FromEnv returned nil inside COGNIX[WS]")
		}
		if _, err := c.Workspaces(); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	// Every call has been answered, so every connection the process opened
	// is now idle: what is open now is what leaks.
	deadline := time.Now().Add(2 * time.Second)
	open := counting.openConns()
	for time.Now().Before(deadline) && open > calls {
		time.Sleep(20 * time.Millisecond)
		open = counting.openConns()
	}
	if open > 4 {
		t.Fatalf("after %d calls the process holds %d open connections to the host, want at most 4", calls, open)
	}
}
