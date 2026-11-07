package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Defaults of DaemonOptions.
const (
	DefaultProbeTimeout = 1500 * time.Millisecond
	DefaultStartTimeout = 20 * time.Second
	defaultPollInterval = 250 * time.Millisecond
)

// startLockName is the lock file (in the data directory's run folder) that lets one process at a time start the
// daemon, so several lead sessions opening at once do not start several daemons (old C-B6).
const startLockName = "start.lock"

// ExitDaemonLocked is the exit code of `seelegent daemon` when another daemon already holds the data directory's
// single-instance lock (run/daemon.lock). A starter that sees it knows a daemon of that home runs (or is still
// starting or shutting down) instead of reporting a failed start.
const ExitDaemonLocked = 3

// ErrNotRunning is matched when no daemon answers and none was started (autostart off).
var ErrNotRunning = errors.New("the Seelegent daemon is not running")

// notRunningError is ErrNotRunning with a message that says why no daemon was started.
type notRunningError struct{ msg string }

func (e *notRunningError) Error() string { return e.msg }
func (e *notRunningError) Unwrap() error { return ErrNotRunning }

// ForeignError says that something answers on the daemon's port but it is not a daemon this client can use: another
// program, an older Seelegent (API v0 under /api) or a daemon with another API version. Starting a daemon would
// fail on the taken port, so discovery stops with this error. Timeout set means the port accepted the connection
// but nothing answered within the probe time (a busy or hung daemon, or another program).
type ForeignError struct {
	Port int
	// APIVersion is the version the answer named (0 when it was not a Seelegent v1 health answer).
	APIVersion int
	Detail     string
	Timeout    time.Duration
}

// TimedOut reports whether err is a probe that got no answer in time.
func TimedOut(err error) bool {
	var fe *ForeignError
	return errors.As(err, &fe) && fe.Timeout > 0
}

func (e *ForeignError) Error() string {
	if e.Timeout > 0 {
		return fmt.Sprintf("port %d did not answer within %s (a busy or hung Seelegent daemon, or another program): "+
			"try again shortly, or check logs/daemon.log", e.Port, e.Timeout)
	}
	if e.APIVersion != 0 {
		return fmt.Sprintf("the daemon on port %d speaks API v%d but this seelegent speaks v%d: stop that daemon "+
			"(or upgrade this seelegent) so they match", e.Port, e.APIVersion, api.APIVersion)
	}
	return fmt.Sprintf("port %d is used by another program, not a Seelegent v%d daemon (%s): stop it, or choose "+
		"another port with SEELEGENT_PORT or the port setting", e.Port, api.APIVersion, e.Detail)
}

// StartFunc starts the daemon in the background. The channel gets the process's exit (or is closed without a value
// when that cannot be watched); a daemon that exits before it answers made the start fail.
type StartFunc func(ctx context.Context) (exited <-chan error, err error)

// DaemonOptions configure a Daemon.
type DaemonOptions struct {
	// Paths is the data directory whose daemon is wanted.
	Paths config.Paths
	// Port is the port of that daemon (config.ResolvePort).
	Port int
	// Start starts the daemon; nil never starts one (SEELEGENT_NO_AUTO_START=1, or inside a worker).
	Start StartFunc
	// NoStart, with Start nil, is the error text when no daemon answers (default: autostart is off).
	NoStart string
	// HTTPClient is used for every call (http.DefaultClient when nil). It must not have an overall timeout.
	HTTPClient *http.Client
	// ProbeTimeout bounds one health probe; StartTimeout how long a started daemon has to answer.
	ProbeTimeout, StartTimeout time.Duration
	// PollInterval is the pause between health probes while a daemon starts.
	PollInterval time.Duration
	Logger       *slog.Logger
}

// Daemon finds the daemon of one data directory and starts it when it is not running.
//
// Discovery is a health probe (GET /api/v1/health answering ok with this API version). A running daemon holds
// run/daemon.lock and records its port in run/daemon.port, so that port is probed first, then the configured
// one (DaemonOptions.Port): a daemon started on another port is found instead of a second daemon being started on
// the same data directory. A port file whose daemon is gone (the lock is free) is ignored, and one whose port does
// not answer, or answers as something else, falls back to the configured port.
type Daemon struct {
	o DaemonOptions
	// sem serializes starts within this process (the lock file does it across processes).
	sem chan struct{}
}

// NewDaemon returns the discovery for o.
func NewDaemon(o DaemonOptions) *Daemon {
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = DefaultProbeTimeout
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = DefaultStartTimeout
	}
	if o.PollInterval <= 0 {
		o.PollInterval = defaultPollInterval
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Daemon{o: o, sem: make(chan struct{}, 1)}
}

// Port is the configured port the daemon is looked for on (after the port file's).
func (d *Daemon) Port() int { return d.o.Port }

// ReadPortFile returns the port recorded in paths.PortFile by the running daemon (false when there is none or it
// is not a port).
func ReadPortFile(paths config.Paths) (int, bool) {
	data, err := os.ReadFile(paths.PortFile)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// lockHeld reports whether a daemon holds the data directory's single-instance lock. Taking the lock without
// waiting and releasing it at once is the check; an error other than "locked" counts as held, so a port file is
// still tried.
func (d *Daemon) lockHeld() bool {
	l, err := fsutil.TryLock(d.o.Paths.LockFile)
	if err != nil {
		return true
	}
	_ = l.Unlock()
	return false
}

// filePort is the port of the daemon that holds the lock, from its port file (false when no daemon of this home
// runs or it recorded no port).
func (d *Daemon) filePort() (int, bool) {
	port, ok := ReadPortFile(d.o.Paths)
	if !ok || !d.lockHeld() {
		return 0, false
	}
	return port, true
}

// Find returns a client of the running daemon, or an error matching ErrNotRunning when nothing answers, or a
// *ForeignError when something else answers on the configured port.
func (d *Daemon) Find(ctx context.Context) (*Client, error) {
	if port, ok := d.filePort(); ok && port != d.o.Port {
		c, err := d.probe(ctx, port)
		if err == nil {
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A stale or reused port: the configured port decides.
		d.o.Logger.Debug("daemon: the port file's port did not answer as the daemon", "port", port, "err", err)
	}
	return d.probe(ctx, d.o.Port)
}

// probe checks the daemon on port.
func (d *Daemon) probe(ctx context.Context, port int) (*Client, error) {
	c := ForPort(port, d.o.HTTPClient)
	pctx, cancel := context.WithTimeout(ctx, d.o.ProbeTimeout)
	defer cancel()
	h, err := c.Health(pctx)
	switch {
	case err == nil && h.OK && h.APIVersion == api.APIVersion:
		return c, nil
	case err == nil && h.APIVersion != 0:
		return nil, &ForeignError{Port: port, APIVersion: h.APIVersion}
	case err == nil:
		return nil, &ForeignError{Port: port, Detail: "its health answer is not Seelegent's"}
	case errors.Is(err, ErrUnreachable):
		return nil, fmt.Errorf("%w (nothing answers on 127.0.0.1:%d)", ErrNotRunning, port)
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case pctx.Err() != nil:
		return nil, &ForeignError{Port: port, Timeout: d.o.ProbeTimeout}
	}
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Code == api.CodeMisdirected {
		// Our own daemon would accept 127.0.0.1:<port>; a 421 means it listens on another port setting.
		return nil, &ForeignError{Port: port, Detail: apiErr.Message}
	}
	return nil, &ForeignError{Port: port, Detail: err.Error()}
}

// Ensure returns a client of the running daemon, starting the daemon when nothing answers on its port. Several
// processes may call it at once: the start lock lets one of them start the daemon while the others wait for it.
func (d *Daemon) Ensure(ctx context.Context) (*Client, error) {
	c, err := d.Find(ctx)
	if err == nil || !errors.Is(err, ErrNotRunning) {
		return c, err
	}
	if d.o.Start == nil {
		if d.o.NoStart != "" {
			return nil, &notRunningError{msg: d.o.NoStart}
		}
		return nil, fmt.Errorf("%w on port %d and autostart is off (SEELEGENT_NO_AUTO_START=1): run "+
			"`seelegent daemon`", ErrNotRunning, d.o.Port)
	}
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-d.sem }()
	return d.start(ctx)
}

// start starts the daemon under the start lock unless another process did meanwhile, then waits until it answers.
func (d *Daemon) start(ctx context.Context) (*Client, error) {
	if err := d.o.Paths.EnsureDirs(); err != nil {
		return nil, err
	}
	lctx, cancel := context.WithTimeout(ctx, d.o.StartTimeout)
	defer cancel()
	lock, err := fsutil.Lock(lctx, filepath.Join(d.o.Paths.Run, startLockName))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("wait for another process starting the daemon: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	if c, err := d.Find(ctx); err == nil || !errors.Is(err, ErrNotRunning) {
		return c, err
	}
	d.o.Logger.Info("daemon: starting", "port", d.o.Port, "home", d.o.Paths.Home)
	exited, err := d.o.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("start the Seelegent daemon: %w", err)
	}
	deadline := time.NewTimer(d.o.StartTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(d.o.PollInterval)
	defer tick.Stop()
	logHint := filepath.Join(d.o.Paths.Logs, "daemon.log")
	for {
		c, err := d.Find(ctx)
		if err == nil {
			d.o.Logger.Info("daemon: started", "port", d.o.Port)
			return c, nil
		}
		if !errors.Is(err, ErrNotRunning) && !TimedOut(err) { // a starting daemon may be slow to answer
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case xerr, ok := <-exited:
			if exitCode(xerr) == ExitDaemonLocked {
				return d.locked(ctx)
			}
			if ok || xerr != nil {
				return nil, fmt.Errorf("the Seelegent daemon exited before it answered (%w); see %s", xerr, logHint)
			}
			exited = nil // not watchable; keep polling until the deadline
		case <-deadline.C:
			return nil, fmt.Errorf("the Seelegent daemon did not answer on port %d within %s; see %s", d.o.Port,
				d.o.StartTimeout, logHint)
		case <-tick.C:
		}
	}
}

// locked handles a started daemon that exited because another daemon of this home holds the lock: that daemon is
// used when it answers, else the error says where it is.
func (d *Daemon) locked(ctx context.Context) (*Client, error) {
	if c, err := d.Find(ctx); err == nil {
		return c, nil
	}
	home := d.o.Paths.Home
	if !d.lockHeld() {
		return nil, &notRunningError{msg: fmt.Sprintf("the previous Seelegent daemon for %s was still shutting "+
			"down; try again shortly", home)}
	}
	if port, ok := ReadPortFile(d.o.Paths); ok && port != d.o.Port {
		return nil, &notRunningError{msg: fmt.Sprintf("the Seelegent daemon for %s runs on port %d (it does not "+
			"answer there now); this MCP looked on port %d: try again shortly, or stop that daemon", home, port,
			d.o.Port)}
	}
	return nil, &notRunningError{msg: fmt.Sprintf("another Seelegent daemon for %s holds its lock but does not "+
		"answer on port %d (still starting or shutting down?): try again shortly", home, d.o.Port)}
}

// exitCode is the exit code in err from exec.Cmd.Wait (-1 when there is none).
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// SpawnDaemon returns a StartFunc that runs `<exe> daemon --port <port>` detached from this process (it outlives
// the lead host; on Windows it breaks away from the host's job when allowed and never shows a console window),
// with this process's environment plus SEELEGENT_HOME=<home>, in the data directory (so it never holds the host's
// folder open).
func SpawnDaemon(exe string, paths config.Paths, port int, env []string) StartFunc {
	return func(context.Context) (<-chan error, error) {
		cmd, err := platform.StartDetached(func() *exec.Cmd {
			//nolint:gosec,noctx // our own executable; the daemon outlives every context of this process
			cmd := exec.Command(exe, "daemon", "--port", strconv.Itoa(port))
			cmd.Dir = paths.Home
			cmd.Env = withEnv(env, config.EnvHome, paths.Home)
			return cmd
		})
		if err != nil {
			return nil, err
		}
		exited := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			if err == nil {
				err = errors.New("exit status 0")
			}
			exited <- err
		}()
		return exited, nil
	}
}

// withEnv returns env with key set to value (replacing every earlier entry for key, case-insensitively on
// Windows).
func withEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == key || (os.PathSeparator == '\\' && strings.EqualFold(k, key)) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
}
