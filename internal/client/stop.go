package client

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// DefaultStopTimeout bounds the wait for a daemon to finish shutting down after POST /daemon/stop.
const DefaultStopTimeout = 15 * time.Second

// StopResult is what Stop did.
type StopResult struct {
	// Stopped is true when a daemon answered and has shut down; false when none was running.
	Stopped bool
	// PID and Port identify the stopped daemon (0 when unknown).
	PID, Port int
	// StalePortFile is true when a port file left by a daemon that is gone was removed.
	StalePortFile bool
}

// StuckError says that a daemon of this data directory holds its lock but does not answer, or did not finish
// shutting down in time. Seelegent never kills a process by an id read from a file (old B3/B14): the message says
// where the daemon is so the user can look.
type StuckError struct {
	Home string
	Port int
	// PID is the process that holds the lock, when the port file (or, on Linux, the lock) records it.
	PID    int
	Reason string
}

func (e *StuckError) Error() string {
	where := fmt.Sprintf("the Seelegent daemon for %s", e.Home)
	if e.PID > 0 {
		where += fmt.Sprintf(" (process %d)", e.PID)
	}
	return fmt.Sprintf("%s %s; try again shortly or look at %s. Seelegent does not kill it: if it hangs, end that "+
		"process yourself", where, e.Reason, filepath.Join(e.Home, "logs", "daemon.log"))
}

// Stop asks the running daemon of this data directory to shut down (POST /daemon/stop) and waits up to timeout
// until its lock is free and its port no longer answers. It never starts a daemon and never kills a process. With
// no daemon running it removes a port file left behind by one that is gone and reports Stopped false. A daemon that
// holds the lock without answering, or that is still shutting down at the timeout, is a *StuckError; something else
// on the port is a *ForeignError.
func (d *Daemon) Stop(ctx context.Context, timeout time.Duration) (StopResult, error) {
	if timeout <= 0 {
		timeout = DefaultStopTimeout
	}
	c, err := d.Find(ctx)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return StopResult{}, ctx.Err()
	case errors.Is(err, ErrNotRunning), TimedOut(err):
		if d.held() {
			return StopResult{}, d.NotAnswering()
		}
		if TimedOut(err) {
			return StopResult{}, err // not ours: no daemon of this home holds the lock
		}
		return StopResult{StalePortFile: d.removeStalePortFile()}, nil
	default:
		return StopResult{}, err
	}
	port := clientPort(c)
	// Find only returns a daemon whose health names this home; stopping also needs this home's own evidence of it
	// (the lock held, or its port file), so a daemon is never stopped on its word alone.
	if _, err := os.Stat(d.o.Paths.PortFile); err != nil && !d.held() {
		return StopResult{}, fmt.Errorf("the daemon on port %d says it serves %s, but no daemon holds that data "+
			"directory's lock and it has no port file: nothing was stopped", port, d.o.Paths.Home)
	}
	res := StopResult{Stopped: true, Port: port, PID: d.holderPID()}
	var ans api.DaemonStop
	if _, err := c.Do(ctx, http.MethodPost, "/daemon/stop", struct{}{}, &ans); err != nil {
		// A daemon that went away while answering is what was asked for; anything else is an error.
		if !errors.Is(err, ErrUnreachable) {
			return StopResult{}, err
		}
	} else if ans.PID > 0 {
		res.PID = ans.PID
	}
	end := time.Now().Add(timeout)
	for {
		if !d.held() && !d.answers(ctx, port) {
			d.removeStalePortFile()
			return res, nil
		}
		if ctx.Err() != nil {
			return StopResult{}, ctx.Err()
		}
		if !time.Now().Before(end) {
			return StopResult{}, &StuckError{Home: d.o.Paths.Home, Port: port, PID: res.PID,
				Reason: fmt.Sprintf("accepted the stop but has not finished shutting down after %s", timeout)}
		}
		t := time.NewTimer(d.o.PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return StopResult{}, ctx.Err()
		case <-t.C:
		}
	}
}

// NotAnswering is the *StuckError for a daemon that holds the lock of this data directory but does not answer
// (call it after Find failed with ErrNotRunning while LockHeld is true).
func (d *Daemon) NotAnswering() *StuckError {
	port := d.lockedPort()
	return &StuckError{Home: d.o.Paths.Home, Port: port, PID: d.holderPID(),
		Reason: fmt.Sprintf("holds its lock but does not answer on port %d (still starting or shutting down?)", port)}
}

// answers reports whether a daemon (or anything slow to answer) still serves port.
func (d *Daemon) answers(ctx context.Context, port int) bool {
	_, err := d.probe(ctx, port)
	return err == nil || TimedOut(err)
}

// LockHeld reports whether a daemon holds the data directory's single-instance lock (it runs, or is starting or
// shutting down).
func (d *Daemon) LockHeld() bool { return d.held() }

// held is lockHeld for callers that must not mistake a missing data directory for a holder: no lock file means
// no daemon of this home has ever run, so none holds it.
func (d *Daemon) held() bool {
	if _, err := os.Stat(d.o.Paths.LockFile); errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return d.lockHeld()
}

// lockedPort is the port the lock holder recorded, else the configured one.
func (d *Daemon) lockedPort() int {
	if port, ok := ReadPortFile(d.o.Paths); ok {
		return port
	}
	return d.o.Port
}

// holderPID is the process id of the daemon that holds the lock, from its port file or (Linux) the lock itself;
// 0 when unknown.
func (d *Daemon) holderPID() int {
	if pid, ok := ReadPortFilePID(d.o.Paths); ok {
		return pid
	}
	if pids, err := platform.LockHolders(d.o.Paths.LockFile); err == nil && len(pids) == 1 {
		return pids[0]
	}
	return 0
}

// removeStalePortFile removes the port file when no daemon holds the lock, holding the lock meanwhile so a daemon
// that starts right now cannot lose its fresh file. It reports whether a file was removed.
func (d *Daemon) removeStalePortFile() bool {
	if _, err := os.Stat(d.o.Paths.PortFile); err != nil {
		return false
	}
	l, err := fsutil.TryLock(d.o.Paths.LockFile)
	if err != nil {
		return false
	}
	defer func() { _ = l.Unlock() }()
	err = os.Remove(d.o.Paths.PortFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		d.o.Logger.Warn("daemon: remove the stale port file", "err", err)
	}
	return err == nil
}

// StalePortFile reports whether a port file exists while no daemon holds the lock (a daemon that crashed left it;
// discovery ignores it).
func (d *Daemon) StalePortFile() bool {
	if _, err := os.Stat(d.o.Paths.PortFile); err != nil {
		return false
	}
	return !d.held()
}

// clientPort is the port of c's address (0 when it has none).
func clientPort(c *Client) int {
	u, err := url.Parse(c.BaseURL())
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	return port
}
