package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// healthHandler answers GET /api/v1/health like a daemon of the given API version.
func healthHandler(version int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, api.Health{OK: true, Version: "test", APIVersion: version})
	})
	return mux
}

// listenOn serves h on 127.0.0.1:port until the test ends.
func listenOn(t *testing.T, port int, h http.Handler) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	p, err := config.NewPaths(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestDaemon(t *testing.T, port int, start StartFunc) *Daemon {
	return NewDaemon(DaemonOptions{Paths: testPaths(t), Port: port, Start: start, ProbeTimeout: 500 * time.Millisecond,
		StartTimeout: 5 * time.Second, PollInterval: 20 * time.Millisecond})
}

func TestFind(t *testing.T) {
	port := freePort(t)
	d := newTestDaemon(t, port, nil)
	if _, err := d.Find(t.Context()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("nothing running = %v", err)
	}
	if _, err := d.Ensure(t.Context()); !errors.Is(err, ErrNotRunning) ||
		!strings.Contains(err.Error(), "SEELEGENT_NO_AUTO_START") {
		t.Fatalf("no autostart = %v", err)
	}
	listenOn(t, port, healthHandler(api.APIVersion))
	c, err := d.Find(t.Context())
	if err != nil || c.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("running = %v, %v", c, err)
	}
}

func TestFindForeign(t *testing.T) {
	cases := map[string]http.Handler{
		"old daemon": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}),
		"api v2":     healthHandler(2),
		"not health": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]int{}) }),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			port := freePort(t)
			listenOn(t, port, h)
			started := false
			d := newTestDaemon(t, port, func(context.Context) (<-chan error, error) { started = true; return nil, nil })
			_, err := d.Ensure(t.Context())
			var fe *ForeignError
			if !errors.As(err, &fe) || fe.Port != port || started {
				t.Fatalf("foreign = %v (started %v)", err, started)
			}
		})
	}
}

// TestEnsureStartsOnce: several "processes" (Daemons on one data directory) ask at once; the start lock lets one
// start the daemon and the others find it (old C-B6).
func TestEnsureStartsOnce(t *testing.T) {
	port := freePort(t)
	paths := testPaths(t)
	var starts atomic.Int32
	start := func(context.Context) (<-chan error, error) {
		starts.Add(1)
		go func() {
			time.Sleep(100 * time.Millisecond) // a daemon takes a moment to listen
			listenOn(t, port, healthHandler(api.APIVersion))
		}()
		return make(chan error), nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		d := NewDaemon(DaemonOptions{Paths: paths, Port: port, Start: start, StartTimeout: 10 * time.Second,
			PollInterval: 20 * time.Millisecond})
		wg.Go(func() {
			_, err := d.Ensure(t.Context())
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := starts.Load(); n != 1 {
		t.Fatalf("started %d daemons", n)
	}
}

func TestEnsureStartFails(t *testing.T) {
	port := freePort(t)
	exited := func(context.Context) (<-chan error, error) {
		ch := make(chan error, 1)
		ch <- errors.New("exit status 3")
		return ch, nil
	}
	d := newTestDaemon(t, port, exited)
	if _, err := d.Ensure(t.Context()); err == nil || !strings.Contains(err.Error(), "exited before it answered") ||
		!strings.Contains(err.Error(), "daemon.log") {
		t.Fatalf("exited = %v", err)
	}
	silent := NewDaemon(DaemonOptions{Paths: testPaths(t), Port: port, StartTimeout: 200 * time.Millisecond,
		PollInterval: 20 * time.Millisecond,
		Start:        func(context.Context) (<-chan error, error) { return make(chan error), nil }})
	if _, err := silent.Ensure(t.Context()); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("silent = %v", err)
	}
	broken := newTestDaemon(t, port, func(context.Context) (<-chan error, error) { return nil, errors.New("no exe") })
	if _, err := broken.Ensure(t.Context()); err == nil || !strings.Contains(err.Error(), "no exe") {
		t.Fatalf("start error = %v", err)
	}
}

func TestWithEnv(t *testing.T) {
	got := withEnv([]string{"A=1", "SEELEGENT_HOME=/old", "B=2"}, "SEELEGENT_HOME", "/new")
	if strings.Join(got, ",") != "A=1,B=2,SEELEGENT_HOME=/new" {
		t.Fatalf("env = %v", got)
	}
}
