package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCloseHTTPIdleConnections: a keep-alive connection left idle after a request and a connection that never
// sent one (a browser's preconnect) do not hold the daemon's stop: Close returns promptly and without an error.
func TestCloseHTTPIdleConnections(t *testing.T) {
	d := newDaemon(t)
	addr, err := d.a.ListenHTTP(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/api/v1/health", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health: %s", res.Status)
	}
	var dialer net.Dialer
	pre, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pre.Close() }()
	waitFor(t, func() bool { return d.a.httpConns.count(http.StateNew) == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	if err := d.a.Close(ctx); err != nil {
		t.Fatalf("close with idle connections: %v", err)
	}
	if took := time.Since(start); took >= 2*time.Second {
		t.Fatalf("close took %s with idle connections (the shutdown timeout is %s)", took, httpShutdown)
	}
	// The preconnected connection was closed by the server.
	_ = pre.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := pre.Read(make([]byte, 1)); n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the preconnected connection is still open: %d %v", n, err)
	}
}

// TestShutdownHTTPForcesHungRequests: a request that does not finish within the timeout has its connection
// closed; the server stopped, so that is a warning in the log, not an error.
func TestShutdownHTTPForcesHungRequests(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hs := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		once.Do(func() { close(entered) })
		<-release
	}), ReadHeaderTimeout: time.Second}
	defer close(release)
	conns := newConnTracker()
	conns.wrap(hs)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- hs.Serve(ln) }()
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", http.NoBody)
		if err != nil {
			return
		}
		if res, err := (&http.Client{Transport: &http.Transport{}}).Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(limit):
		t.Fatal("the request never reached the handler")
	}

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	start := time.Now()
	if err := shutdownHTTP(t.Context(), hs, conns, 100*time.Millisecond, log); err != nil {
		t.Fatalf("shutdown with a hung request: %v", err)
	}
	if took := time.Since(start); took >= 2*time.Second {
		t.Fatalf("shutdown took %s", took)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "did not finish in time") {
		t.Fatalf("log = %q", logs.String())
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve = %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	end := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(end) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// count is the number of tracked connections in state st.
func (t *connTracker) count(st http.ConnState) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, s := range t.conns {
		if s == st {
			n++
		}
	}
	return n
}
