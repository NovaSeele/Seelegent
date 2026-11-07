package client

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// stoppableDaemon serves health and POST /daemon/stop on port while holding the lock of paths, like a running daemon.
// With obey, a stop closes the server and releases the lock shortly after answering 202.
func stoppableDaemon(t *testing.T, d *Daemon, port, pid int, obey bool) {
	t.Helper()
	paths := d.o.Paths
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	lock, err := fsutil.TryLock(paths.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PortFile, []byte(strconv.Itoa(port)+"\n"+strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, api.Health{OK: true, Version: "test", APIVersion: api.APIVersion})
	})
	mux.HandleFunc("POST /api/v1/daemon/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Seelegent") == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no header", "code": "forbidden"})
			return
		}
		writeJSON(w, http.StatusAccepted, api.DaemonStop{Stopping: true, PID: pid})
		if obey {
			time.AfterFunc(150*time.Millisecond, func() {
				_ = os.Remove(paths.PortFile)
				_ = srv.Close()
				_ = lock.Unlock()
			})
		}
	})
	srv.Handler = mux
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = lock.Unlock() })
}

func TestStop(t *testing.T) {
	noStart := func(context.Context) (<-chan error, error) { t.Error("Stop started a daemon"); return nil, nil }

	t.Run("not running", func(t *testing.T) {
		d := newTestDaemon(t, freePort(t), noStart)
		res, err := d.Stop(t.Context(), time.Second)
		if err != nil || res.Stopped || res.StalePortFile {
			t.Fatalf("stop = %+v, %v", res, err)
		}
	})

	t.Run("stale port file", func(t *testing.T) {
		d := newTestDaemon(t, freePort(t), noStart)
		writePortFile(t, d.o.Paths, freePort(t))
		if !d.StalePortFile() {
			t.Fatal("StalePortFile = false")
		}
		res, err := d.Stop(t.Context(), time.Second)
		if err != nil || res.Stopped || !res.StalePortFile {
			t.Fatalf("stop = %+v, %v", res, err)
		}
		if _, err := os.Stat(d.o.Paths.PortFile); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("port file still there: %v", err)
		}
	})

	t.Run("lock held, no answer", func(t *testing.T) {
		d := newTestDaemon(t, freePort(t), noStart)
		holdLock(t, d.o.Paths)
		if err := os.WriteFile(d.o.Paths.PortFile, []byte(strconv.Itoa(d.o.Port)+"\n4242\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := d.Stop(t.Context(), time.Second)
		var stuck *StuckError
		if !errors.As(err, &stuck) || stuck.PID != 4242 || !strings.Contains(err.Error(), "process 4242") ||
			!strings.Contains(err.Error(), "does not answer") {
			t.Fatalf("stop = %v", err)
		}
		if _, err := os.Stat(d.o.Paths.PortFile); err != nil {
			t.Fatalf("the holder's port file was removed: %v", err)
		}
	})

	t.Run("stops", func(t *testing.T) {
		d := newTestDaemon(t, freePort(t), noStart)
		stoppableDaemon(t, d, d.o.Port, 777, true)
		res, err := d.Stop(t.Context(), 5*time.Second)
		if err != nil || !res.Stopped || res.PID != 777 || res.Port != d.o.Port {
			t.Fatalf("stop = %+v, %v", res, err)
		}
		if d.LockHeld() {
			t.Fatal("lock still held")
		}
		if _, err := d.Find(t.Context()); !errors.Is(err, ErrNotRunning) {
			t.Fatalf("find after stop = %v", err)
		}
	})

	t.Run("does not stop in time", func(t *testing.T) {
		d := newTestDaemon(t, freePort(t), noStart)
		stoppableDaemon(t, d, d.o.Port, 778, false)
		start := time.Now()
		_, err := d.Stop(t.Context(), 300*time.Millisecond)
		var stuck *StuckError
		if !errors.As(err, &stuck) || stuck.PID != 778 || !strings.Contains(err.Error(), "not finished shutting down") {
			t.Fatalf("stop = %v", err)
		}
		if el := time.Since(start); el > 3*time.Second {
			t.Fatalf("took %v", el)
		}
	})

	t.Run("foreign", func(t *testing.T) {
		port := freePort(t)
		listenOn(t, port, healthHandler(2))
		d := newTestDaemon(t, port, noStart)
		_, err := d.Stop(t.Context(), time.Second)
		var fe *ForeignError
		if !errors.As(err, &fe) {
			t.Fatalf("stop = %v", err)
		}
	})
}
