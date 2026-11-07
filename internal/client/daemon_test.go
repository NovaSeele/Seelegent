package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// healthHandler answers GET /api/v1/health like a daemon of the given API version.
func healthHandler(version int, home string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, api.Health{OK: true, Version: "test", APIVersion: version,
			HomeID: config.HomeID(home)})
	})
	return mux
}

// listenOn serves h on 127.0.0.1:port until the test ends.
func listenOn(t *testing.T, port int, h http.Handler) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
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
	listenOn(t, port, healthHandler(api.APIVersion, d.o.Paths.Home))
	c, err := d.Find(t.Context())
	if err != nil || c.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("running = %v, %v", c, err)
	}
}

// TestFindOtherHome: a Seelegent daemon of another data directory on this home's port (or one that names no home)
// is refused, never adopted: Find and Ensure report the conflict with its pid and start nothing.
func TestFindOtherHome(t *testing.T) {
	for name, home := range map[string]string{"other home": filepath.Join(t.TempDir(), "a"), "no home id": ""} {
		t.Run(name, func(t *testing.T) {
			port := freePort(t)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
				h := api.Health{OK: true, Version: "test", APIVersion: api.APIVersion, PID: 4321}
				if home != "" {
					h.HomeID = config.HomeID(home)
				}
				writeJSON(w, http.StatusOK, h)
			})
			listenOn(t, port, mux)
			started := false
			d := newTestDaemon(t, port, func(context.Context) (<-chan error, error) { started = true; return nil, nil })
			for _, look := range []func(context.Context) (*Client, error){d.Find, d.Ensure} {
				_, err := look(t.Context())
				var fe *ForeignError
				if !errors.As(err, &fe) || !fe.OtherHome || fe.PID != 4321 || started ||
					!strings.Contains(err.Error(), "another data directory (pid 4321)") {
					t.Fatalf("other home = %v (started %v)", err, started)
				}
			}
		})
	}
}

// TestHomeID: every spelling of one folder has one id; another folder another id; the id never shows the path.
func TestHomeID(t *testing.T) {
	dir := t.TempDir()
	a := config.HomeID(dir)
	if b := config.HomeID(dir + string(filepath.Separator) + "."); a != b {
		t.Fatalf("ids %s and %s for one folder", a, b)
	}
	if c := config.HomeID(filepath.Join(dir, "other")); c == a || len(a) != 16 || strings.Contains(a, "Temp") {
		t.Fatalf("ids %s and %s", a, c)
	}
}

func TestFindForeign(t *testing.T) {
	cases := map[string]http.Handler{
		"old daemon": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}),
		"api v2":     healthHandler(2, ""),
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
			listenOn(t, port, healthHandler(api.APIVersion, paths.Home))
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

// writePortFile records port in the data directory's port file, as a running daemon does.
func writePortFile(t *testing.T, paths config.Paths, port int) {
	t.Helper()
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PortFile, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// holdLock holds the data directory's daemon lock until the test ends, as a running daemon does.
func holdLock(t *testing.T, paths config.Paths) {
	t.Helper()
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	l, err := fsutil.TryLock(paths.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Unlock() })
}

// TestFindThroughPortFile: a daemon of this home that runs on another port than the configured one (port changed
// in the settings, started with --port) is found through its port file, and no second daemon is started.
func TestFindThroughPortFile(t *testing.T) {
	paths := testPaths(t)
	running, configured := freePort(t), freePort(t)
	listenOn(t, running, healthHandler(api.APIVersion, paths.Home))
	writePortFile(t, paths, running)
	holdLock(t, paths)
	started := false
	d := NewDaemon(DaemonOptions{Paths: paths, Port: configured, ProbeTimeout: 500 * time.Millisecond,
		Start: func(context.Context) (<-chan error, error) { started = true; return nil, nil }})
	c, err := d.Ensure(t.Context())
	if err != nil || started || c.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(running) {
		t.Fatalf("ensure = %v, %v (started %v)", c, err, started)
	}
}

// TestStalePortFile: a port file left by a daemon that crashed never blocks a start, whether its port is free or
// taken by another program. While something holds the lock the start waits, and starts once the lock is free.
func TestStalePortFile(t *testing.T) {
	foreign := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	cases := []struct {
		name          string
		other, locked bool
	}{
		{"nobody listens", false, false},
		{"another program", true, false},
		{"another program, lock held", true, true},
		{"nobody listens, lock held", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			paths := testPaths(t)
			stale, configured := freePort(t), freePort(t)
			if c.other {
				listenOn(t, stale, foreign)
			}
			writePortFile(t, paths, stale)
			var released atomic.Bool
			if c.locked {
				if err := paths.EnsureDirs(); err != nil {
					t.Fatal(err)
				}
				l, err := fsutil.TryLock(paths.LockFile)
				if err != nil {
					t.Fatal(err)
				}
				time.AfterFunc(200*time.Millisecond, func() { released.Store(true); _ = l.Unlock() })
			}
			var starts atomic.Int32
			var early atomic.Bool
			d := NewDaemon(DaemonOptions{Paths: paths, Port: configured, ProbeTimeout: 500 * time.Millisecond,
				StartTimeout: 5 * time.Second, PollInterval: 20 * time.Millisecond,
				Start: func(context.Context) (<-chan error, error) {
					starts.Add(1)
					early.Store(c.locked && !released.Load())
					listenOn(t, configured, healthHandler(api.APIVersion, paths.Home))
					return make(chan error), nil
				}})
			c2, err := d.Ensure(t.Context())
			if err != nil || starts.Load() != 1 || early.Load() ||
				c2.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(configured) {
				t.Fatalf("ensure = %v, %v (starts %d, while locked %v)", c2, err, starts.Load(), early.Load())
			}
		})
	}
}

// TestStartWaitsForLockHolder: while another daemon of this home holds its lock but does not answer yet, nothing
// is started; that daemon is used as soon as it answers, and a holder that never answers ends the start with an
// error after StartTimeout.
func TestStartWaitsForLockHolder(t *testing.T) {
	t.Run("holder answers", func(t *testing.T) {
		paths := testPaths(t)
		port := freePort(t)
		holdLock(t, paths)
		var starts atomic.Int32
		d := NewDaemon(DaemonOptions{Paths: paths, Port: port, ProbeTimeout: 500 * time.Millisecond,
			StartTimeout: 5 * time.Second, PollInterval: 20 * time.Millisecond,
			Start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil }})
		time.AfterFunc(300*time.Millisecond, func() { listenOn(t, port, healthHandler(api.APIVersion, paths.Home)) })
		begun := time.Now()
		c, err := d.Ensure(t.Context())
		if err != nil || starts.Load() != 0 || c.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(port) {
			t.Fatalf("ensure = %v, %v (starts %d)", c, err, starts.Load())
		}
		if waited := time.Since(begun); waited < 250*time.Millisecond || waited > 4*time.Second {
			t.Fatalf("returned after %s, want soon after the holder answered", waited)
		}
	})
	t.Run("holder never answers", func(t *testing.T) {
		paths := testPaths(t)
		port := freePort(t)
		holdLock(t, paths)
		var starts atomic.Int32
		d := NewDaemon(DaemonOptions{Paths: paths, Port: port, ProbeTimeout: 200 * time.Millisecond,
			StartTimeout: 300 * time.Millisecond, PollInterval: 20 * time.Millisecond,
			Start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil }})
		_, err := d.Ensure(t.Context())
		if !errors.Is(err, ErrNotRunning) || starts.Load() != 0 ||
			!strings.Contains(err.Error(), "no Seelegent daemon answered within 300ms") ||
			!strings.Contains(err.Error(), "holds its lock") || !strings.Contains(err.Error(), paths.Home) {
			t.Fatalf("ensure = %v (starts %d)", err, starts.Load())
		}
	})
}

// TestStartOneDeadline: a start that first waits for another process holding the start lock, and then for a
// daemon that holds its lock without answering, stays within one StartTimeout instead of adding the two waits.
func TestStartOneDeadline(t *testing.T) {
	paths := testPaths(t)
	holdLock(t, paths)
	other, err := fsutil.TryLock(filepath.Join(paths.Run, startLockName))
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(500*time.Millisecond, func() { _ = other.Unlock() })
	var starts atomic.Int32
	d := NewDaemon(DaemonOptions{Paths: paths, Port: freePort(t), ProbeTimeout: 200 * time.Millisecond,
		StartTimeout: time.Second, PollInterval: 20 * time.Millisecond,
		Start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil }})
	begun := time.Now()
	_, err = d.Ensure(t.Context())
	took := time.Since(begun)
	if !errors.Is(err, ErrNotRunning) || starts.Load() != 0 || !strings.Contains(err.Error(), "holds its lock") {
		t.Fatalf("ensure = %v (starts %d)", err, starts.Load())
	}
	if took < 900*time.Millisecond || took > 1400*time.Millisecond {
		t.Fatalf("the start took %s, want about the 1s StartTimeout", took)
	}
}

// TestStartLockHeldTooLong: another process that holds the start lock for the whole StartTimeout ends the start
// with an error matching ErrNotRunning that says where the daemon is, and nothing is started; a cancelled caller
// still gets its own context error.
func TestStartLockHeldTooLong(t *testing.T) {
	setup := func(t *testing.T, daemonLock bool, timeout time.Duration) (*Daemon, config.Paths, *atomic.Int32) {
		paths := testPaths(t)
		if daemonLock {
			holdLock(t, paths)
		}
		if err := paths.EnsureDirs(); err != nil {
			t.Fatal(err)
		}
		other, err := fsutil.TryLock(filepath.Join(paths.Run, startLockName))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = other.Unlock() })
		starts := &atomic.Int32{}
		d := NewDaemon(DaemonOptions{Paths: paths, Port: freePort(t), ProbeTimeout: 200 * time.Millisecond,
			StartTimeout: timeout, PollInterval: 20 * time.Millisecond,
			Start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil }})
		return d, paths, starts
	}
	for _, c := range []struct {
		name       string
		daemonLock bool
		want       string
	}{
		{"no daemon yet", false, "no daemon holds the lock"},
		{"daemon holds its lock", true, "holds its lock"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, paths, starts := setup(t, c.daemonLock, 300*time.Millisecond)
			_, err := d.Ensure(t.Context())
			if !errors.Is(err, ErrNotRunning) || starts.Load() != 0 ||
				!strings.Contains(err.Error(), "another process has been starting the Seelegent daemon for 300ms") ||
				!strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), paths.Home) {
				t.Fatalf("ensure = %v (starts %d)", err, starts.Load())
			}
		})
	}
	t.Run("caller cancelled", func(t *testing.T) {
		d, _, starts := setup(t, false, 5*time.Second)
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		_, err := d.Ensure(ctx)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrNotRunning) || starts.Load() != 0 {
			t.Fatalf("ensure = %v (starts %d)", err, starts.Load())
		}
	})
}

// TestProbeTimeout: a port that accepts but never answers is reported as busy or hung, and nothing is started.
func TestProbeTimeout(t *testing.T) {
	port := freePort(t)
	block := make(chan struct{})
	listenOn(t, port, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer close(block)
	started := false
	d := NewDaemon(DaemonOptions{Paths: testPaths(t), Port: port, ProbeTimeout: 200 * time.Millisecond,
		Start: func(context.Context) (<-chan error, error) { started = true; return nil, nil }})
	_, err := d.Ensure(t.Context())
	if !TimedOut(err) || started || !strings.Contains(err.Error(), "did not answer within 200ms (a busy or hung") {
		t.Fatalf("ensure = %v (started %v)", err, started)
	}
}

func TestNoStartMessage(t *testing.T) {
	d := NewDaemon(DaemonOptions{Paths: testPaths(t), Port: freePort(t), NoStart: "the daemon running this worker " +
		"cannot be reached"})
	if _, err := d.Ensure(t.Context()); !errors.Is(err, ErrNotRunning) || err.Error() != "the daemon running this "+
		"worker cannot be reached" {
		t.Fatalf("err = %v", err)
	}
}

func TestWithEnv(t *testing.T) {
	got := withEnv([]string{"A=1", "SEELEGENT_HOME=/old", "B=2"}, "SEELEGENT_HOME", "/new")
	if strings.Join(got, ",") != "A=1,B=2,SEELEGENT_HOME=/new" {
		t.Fatalf("env = %v", got)
	}
}

// TestEnsureOneDeadlineInProcess (old F11): a call that arrives while another call of this process is starting the
// daemon waits for it at most until its own StartTimeout ends, instead of waiting without a bound and then taking a
// fresh StartTimeout.
func TestEnsureOneDeadlineInProcess(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var starts atomic.Int32
	d := NewDaemon(DaemonOptions{Paths: testPaths(t), Port: freePort(t), ProbeTimeout: 200 * time.Millisecond,
		StartTimeout: time.Second, PollInterval: 20 * time.Millisecond,
		Start: func(ctx context.Context) (<-chan error, error) {
			starts.Add(1)
			select { // a start that hangs, holding the in-process semaphore
			case <-release:
			case <-ctx.Done():
			}
			return make(chan error), nil
		}})
	go func() { _, _ = d.Ensure(t.Context()) }()
	for starts.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	begun := time.Now()
	_, err := d.Ensure(t.Context())
	took := time.Since(begun)
	if !errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "another start of the Seelegent daemon in "+
		"this process") || starts.Load() != 1 {
		t.Fatalf("second ensure = %v (starts %d)", err, starts.Load())
	}
	if took < 900*time.Millisecond || took > 1500*time.Millisecond {
		t.Fatalf("the second call took %s, want about the 1s StartTimeout", took)
	}
}

// TestSpawnNearDeadline (old F12): when the start lock comes free just before the deadline, the daemon spawned then
// still gets a few poll intervals to answer; once the deadline has passed nothing is spawned.
func TestSpawnNearDeadline(t *testing.T) {
	t.Run("spawn gets a floor", func(t *testing.T) {
		paths := testPaths(t)
		if err := paths.EnsureDirs(); err != nil {
			t.Fatal(err)
		}
		other, err := fsutil.TryLock(filepath.Join(paths.Run, startLockName))
		if err != nil {
			t.Fatal(err)
		}
		time.AfterFunc(900*time.Millisecond, func() { _ = other.Unlock() })
		port := freePort(t)
		var starts atomic.Int32
		d := NewDaemon(DaemonOptions{Paths: paths, Port: port, ProbeTimeout: 200 * time.Millisecond,
			StartTimeout: time.Second, PollInterval: 50 * time.Millisecond,
			Start: func(context.Context) (<-chan error, error) {
				starts.Add(1)
				time.AfterFunc(250*time.Millisecond, func() { listenOn(t, port, healthHandler(api.APIVersion, paths.Home)) })
				return make(chan error), nil
			}})
		c, err := d.Ensure(t.Context())
		if err != nil || starts.Load() != 1 || c.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(port) {
			t.Fatalf("ensure = %v, %v (starts %d)", c, err, starts.Load())
		}
	})
	t.Run("no spawn after the deadline", func(t *testing.T) {
		var starts atomic.Int32
		d := NewDaemon(DaemonOptions{Paths: testPaths(t), Port: freePort(t), ProbeTimeout: 200 * time.Millisecond,
			StartTimeout: time.Second, PollInterval: 20 * time.Millisecond,
			Start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil }})
		_, err := d.start(t.Context(), time.Now().Add(-time.Millisecond))
		if !errors.Is(err, ErrNotRunning) || starts.Load() != 0 || !strings.Contains(err.Error(), "ran out") {
			t.Fatalf("start = %v (starts %d)", err, starts.Load())
		}
	})
}
