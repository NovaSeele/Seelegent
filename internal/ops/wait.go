package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// Lookup says how Connect gets the daemon client.
type Lookup int

const (
	// Reuse may return a client already found (the first call of a wait).
	Reuse Lookup = iota
	// Ensure looks again and starts the daemon when nothing answers: the connection was lost without a word (a
	// crash), so the daemon is brought back as on any other call.
	Ensure
	// FindOnly looks again but never starts a daemon: the daemon said it was stopping, so only a restart (or the
	// user) brings one back.
	FindOnly
)

// Connect returns the daemon client, looked up as how says.
type Connect func(ctx context.Context, how Lookup) (*client.Client, error)

// DefaultStopGrace is how long a wait whose daemon said it was stopping looks (without starting one) for a daemon
// that comes back, as after `seelegent restart`: its stop takes a few seconds and the start up to 20 s.
const DefaultStopGrace = 40 * time.Second

// CodeDaemonStopped is the error code of StoppedError (the CLI's --json and the MCP tools).
const CodeDaemonStopped = "daemon_stopped"

// StoppedError ends a wait whose daemon was stopped (POST /daemon/stop or Ctrl+C) and did not come back within the
// grace. The tasks were not touched: they keep running and are waited for again once a daemon runs.
type StoppedError struct{}

func (StoppedError) Error() string {
	return "the Seelegent daemon was stopped during the wait; the tasks keep running: wait again later (any task " +
		"command, or seelegent ui, starts the daemon again)"
}

// Wait is one wait for task reports.
type Wait struct {
	IDs []string
	// Seconds is the whole wait; 0 returns the current reports at once.
	Seconds int
	// Any wakes on the first task that ends; WakeOnBlocked when a task needs the lead.
	Any, WakeOnBlocked bool
	// Cwd makes the reports' paths relative to it (else to each task's folder).
	Cwd string
	// RetryPause is the pause before looking for the daemon again (default 1 s).
	RetryPause time.Duration
	// StopGrace is how long to look for a daemon that comes back after it said it was stopping (DefaultStopGrace).
	StopGrace time.Duration
	Logger    *slog.Logger
}

// WaitReports waits for w.IDs through POST /wait and returns the result with the reports. Finding the daemon again
// counts against the wait's own deadline: the wait never runs past it.
//
// When the connection is lost without a word (a crash, a killed daemon), it finds or starts the daemon again
// through connect (Ensure) and waits for the rest of the time. When the daemon answers daemon_stopping (it was
// asked to stop, or Ctrl+C), it only looks for a daemon that comes back (FindOnly, never starting one) for
// StopGrace: a restart carries the wait on (and from then on the normal rules apply again), and after a plain stop
// the wait ends with StoppedError instead of starting the daemon that was just stopped.
func WaitReports(ctx context.Context, connect Connect, w Wait) (api.WaitResult, error) {
	if w.RetryPause <= 0 {
		w.RetryPause = time.Second
	}
	if w.StopGrace <= 0 {
		w.StopGrace = DefaultStopGrace
	}
	if w.Logger == nil {
		w.Logger = slog.New(slog.DiscardHandler)
	}
	deadline := time.Now().Add(time.Duration(w.Seconds) * time.Second)
	var (
		lost    error     // why the daemon was lost, while reconnecting
		stopped time.Time // when the daemon last said it was stopping (zero: never during this wait)
	)
	// end is how long the current reconnection may take.
	end := func() time.Time {
		if stopped.IsZero() {
			return deadline
		}
		return minTime(deadline, stopped.Add(w.StopGrace))
	}
	for how := Reuse; ; {
		c, err := lookup(ctx, connect, how, end())
		switch {
		case err == nil && how == FindOnly:
			// A daemon is back (a restart): the normal rules apply again. A later crash is ridden out through
			// Ensure, and a later stop answers daemon_stopping again, which starts a new grace.
			stopped = time.Time{}
		case err == nil:
		case ctx.Err() != nil:
			return api.WaitResult{}, ctx.Err()
		case how == Reuse:
			return api.WaitResult{}, err
		case !time.Now().Before(end()):
			if !stopped.IsZero() {
				return api.WaitResult{}, StoppedError{}
			}
			// The daemon did not come back before the wait ended. An error that is only the lookup's own deadline
			// says nothing: report why the daemon was lost instead.
			if errors.Is(err, context.DeadlineExceeded) {
				err = lost
			}
			return api.WaitResult{}, fmt.Errorf("the daemon did not come back before the wait ended: %w", err)
		}
		if err == nil {
			var res api.WaitResult
			if res, err = waitOn(ctx, c, w, deadline); err == nil {
				return res, nil
			}
		}
		if stopping(err) {
			stopped = time.Now()
			w.Logger.Info("the daemon is stopping during a wait; looking for one that comes back (a restart)")
		} else {
			// A restarting daemon is unreachable for a moment, not running yet, or slow to answer its first probe:
			// wait for it until the wait's own deadline (or the stop's grace).
			retry := errors.Is(err, client.ErrUnreachable) || errors.Is(err, client.ErrNotRunning) ||
				client.TimedOut(err)
			if !retry || !time.Now().Before(end()) {
				if retry && !stopped.IsZero() {
					return api.WaitResult{}, StoppedError{}
				}
				return api.WaitResult{}, err
			}
			w.Logger.Info("the daemon is gone or not answering during a wait; reconnecting", "err", err)
		}
		lost = err
		// After the daemon said it was stopping, nothing in this wait starts one until a daemon is found again.
		how = Ensure
		if !stopped.IsZero() {
			how = FindOnly
		}
		pause := time.NewTimer(max(min(w.RetryPause, time.Until(end())), 0))
		select {
		case <-ctx.Done():
			pause.Stop()
			return api.WaitResult{}, ctx.Err()
		case <-pause.C:
		}
	}
}

// stopping reports whether err is the daemon's daemon_stopping answer.
func stopping(err error) bool {
	var apiErr *client.Error
	return errors.As(err, &apiErr) && apiErr.Code == api.CodeDaemonStopping
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// MaxWaitCall is the longest wait one POST /wait accepts (contract §3); longer waits are several calls.
const MaxWaitCall = 3000

// waitOn waits on c for the rest of the time (0 returns the current reports at once), in calls of at most
// MaxWaitCall seconds.
func waitOn(ctx context.Context, c *client.Client, w Wait, deadline time.Time) (api.WaitResult, error) {
	wake := w.WakeOnBlocked
	for {
		left := max(int(math.Ceil(time.Until(deadline).Seconds())), 0)
		call := min(left, MaxWaitCall)
		res, err := c.Wait(ctx, api.WaitRequest{IDs: w.IDs, TimeoutSeconds: call, Any: w.Any,
			WakeOnBlocked: &wake, Reports: true}, w.Cwd)
		if err != nil {
			return api.WaitResult{}, err
		}
		if len(res.Reports) != len(w.IDs) {
			return api.WaitResult{}, fmt.Errorf("the daemon returned %d reports for %d tasks", len(res.Reports),
				len(w.IDs))
		}
		if res.Woke != api.WokeTimeout || left <= call {
			return res, nil
		}
	}
}

// lookup is the client for WaitReports. While reconnecting, finding (or starting) the daemon again is bounded by
// end.
func lookup(ctx context.Context, connect Connect, how Lookup, end time.Time) (*client.Client, error) {
	if how == Reuse {
		return connect(ctx, Reuse)
	}
	dctx, cancel := context.WithDeadline(ctx, end)
	defer cancel()
	return connect(dctx, how)
}
