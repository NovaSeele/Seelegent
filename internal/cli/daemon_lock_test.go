package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// freeTestPort returns a port nothing listens on (it was free a moment ago).
func freeTestPort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// testHome points SEELEGENT_HOME at a new temp data directory and returns its paths.
func testHome(t *testing.T) config.Paths {
	t.Helper()
	paths, err := config.NewPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvHome, paths.Home)
	return paths
}

// TestDaemonLockedOpensNothing: while the lock is held, `seelegent daemon` exits with ExitDaemonLocked and the
// running daemon's port, without opening its log, the store or recovery.
func TestDaemonLockedOpensNothing(t *testing.T) {
	paths := testHome(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	held, err := fsutil.TryLock(paths.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Unlock() }()
	if err := os.WriteFile(paths.PortFile, []byte("47999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := Run(t.Context(), []string{"daemon", "--port", strconv.Itoa(freeTestPort(t))}, &stdout, &stderr)
	if code != ExitDaemonLocked {
		t.Fatalf("exit code %d, stderr %s", code, stderr.String())
	}
	want := "another Seelegent daemon is already running for " + paths.Home + " (port 47999)"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if d := time.Since(start); d < daemonLockWait-100*time.Millisecond || d > daemonLockWait+5*time.Second {
		t.Errorf("gave up after %v, want about %v", d, daemonLockWait)
	}
	for _, p := range []string{paths.DB, paths.Logs + "/daemon.log"} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists (err %v): the second daemon opened it", p, err)
		}
	}
}

// TestTwoDaemonsOneHome runs a real daemon, then a second one on the same home and another port: the second
// refuses, naming the first's port. The first records its port while it runs and removes it when it stops.
func TestTwoDaemonsOneHome(t *testing.T) {
	paths := testHome(t)
	portA := freeTestPort(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, testInfo, daemonFlags{port: portA}, io.Discard) }()

	deadline := time.Now().Add(60 * time.Second)
	for {
		if data, err := os.ReadFile(paths.PortFile); err == nil {
			if strings.TrimSpace(string(data)) != strconv.Itoa(portA) {
				t.Fatalf("port file = %q, want %d", data, portA)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("daemon A ended: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon A wrote no port file")
		}
		time.Sleep(50 * time.Millisecond)
	}

	err := runDaemon(t.Context(), testInfo, daemonFlags{port: freeTestPort(t)}, &bytes.Buffer{})
	var running *daemonRunningError
	if !errors.As(err, &running) || running.port != portA || running.home != paths.Home {
		t.Fatalf("second daemon = %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("daemon A did not stop")
	}
	if _, err := os.Stat(paths.PortFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("port file left behind: %v", err)
	}
	l, err := fsutil.TryLock(paths.LockFile)
	if err != nil {
		t.Fatalf("lock still held after the daemon stopped: %v", err)
	}
	_ = l.Unlock()
}
