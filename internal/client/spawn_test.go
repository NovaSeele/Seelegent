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
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/testutil/raceenv"
)

// envFakeDaemon makes the test binary, started as "<exe> daemon --port <n>", a fake daemon: it answers the health
// check until a file named stop appears in SEELEGENT_HOME (or 30 s pass), and records its folder and home.
const envFakeDaemon = "CLIENT_TEST_FAKE_DAEMON"

func TestMain(m *testing.M) {
	raceenv.QuietExit()
	if len(os.Args) == 4 && os.Args[1] == "daemon" && os.Args[2] == "--port" {
		switch os.Getenv(envFakeDaemon) {
		case "1":
			os.Exit(fakeDaemon(os.Args[3]))
		case "locked": // another daemon holds the data directory's lock
			os.Exit(ExitDaemonLocked)
		}
	}
	os.Exit(m.Run())
}

func fakeDaemon(port string) int {
	home := os.Getenv(config.EnvHome)
	wd, _ := os.Getwd()
	if err := os.WriteFile(filepath.Join(home, "started.txt"), []byte(wd+"\n"+home), 0o600); err != nil {
		return 2
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return 3
	}
	srv := &http.Server{Handler: healthHandler(api.APIVersion, home), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(home, "stop")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = srv.Close()
	return 0
}

// TestSpawnedDaemonLocked: a started daemon that exits for the lock is reported by where the running daemon is,
// not as a failed start. While the lock is held nothing is started; the wait for the holder ends with the same
// account of where it is.
func TestSpawnedDaemonLocked(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		hold      bool
		portFile  int
		timeout   time.Duration
		wantParts []string
	}{
		{"still shutting down", false, 0, 20 * time.Second, []string{"previous Seelegent daemon", "still shutting down"}},
		{"starting", true, 0, time.Second, []string{"holds its lock", "try again shortly"}},
		{"on another port", true, 1, time.Second, []string{"runs on port", "this MCP looked on port"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			paths := testPaths(t)
			if err := paths.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			if c.hold {
				l, err := fsutil.TryLock(paths.LockFile)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = l.Unlock() }()
			}
			if c.portFile != 0 {
				writePortFile(t, paths, freePort(t))
			}
			port := freePort(t)
			start := SpawnDaemon(exe, paths, port, append(os.Environ(), envFakeDaemon+"=locked"))
			d := NewDaemon(DaemonOptions{Paths: paths, Port: port, Start: start, StartTimeout: c.timeout,
				ProbeTimeout: 500 * time.Millisecond, PollInterval: 50 * time.Millisecond})
			_, err := d.Ensure(t.Context())
			if !errors.Is(err, ErrNotRunning) {
				t.Fatalf("err = %v", err)
			}
			for _, p := range append(c.wantParts, paths.Home) {
				if !strings.Contains(err.Error(), p) {
					t.Errorf("err = %v, want %q", err, p)
				}
			}
		})
	}
}

// TestSpawnDaemon starts the test binary as a detached daemon through SpawnDaemon and finds it.
func TestSpawnDaemon(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := testPaths(t)
	port := freePort(t)
	env := append(os.Environ(), envFakeDaemon+"=1", config.EnvHome+"=/somewhere/else")
	spawn := SpawnDaemon(exe, paths, port, env)
	var exited <-chan error
	start := func(ctx context.Context) (<-chan error, error) {
		ch, err := spawn(ctx)
		exited = ch
		return ch, err
	}
	d := NewDaemon(DaemonOptions{Paths: paths, Port: port, Start: start, StartTimeout: 20 * time.Second,
		PollInterval: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	c, err := d.Ensure(ctx)
	t.Cleanup(func() {
		// Stop the fake daemon and wait for it, so its folder (the temp dir) can be removed.
		_ = os.WriteFile(filepath.Join(paths.Home, "stop"), nil, 0o600)
		if exited != nil {
			select {
			case <-exited: //nolint:errcheck // any exit will do; the folder only has to be released
			case <-time.After(30 * time.Second):
				t.Error("the fake daemon did not stop")
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("client = %s", c.BaseURL())
	}
	data, err := os.ReadFile(filepath.Join(paths.Home, "started.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// The folder is compared as a location: the system may report the working directory with symbolic links
	// resolved (macOS: /var is /private/var), while the home is passed on exactly as given.
	wd, home, _ := strings.Cut(string(data), "\n")
	if same, err := fsutil.SamePath(wd, paths.Home); err != nil || !same || home != paths.Home {
		t.Fatalf("daemon folder and home = %q, want %q twice", data, paths.Home)
	}
}
