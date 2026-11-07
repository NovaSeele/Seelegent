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

// Connect returns the daemon client. fresh asks for a new lookup (the daemon went away during a wait); otherwise a
// client already found may be reused.
type Connect func(ctx context.Context, fresh bool) (*client.Client, error)

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
	Logger     *slog.Logger
}

// WaitReports waits for w.IDs through POST /wait and returns the result with the reports. When the daemon goes
// away during the wait (a restart), it finds or starts it again through connect and waits for the rest of the
// time. Finding the daemon again counts against the wait's own deadline: the wait never runs past it.
func WaitReports(ctx context.Context, connect Connect, w Wait) (api.WaitResult, error) {
	if w.RetryPause <= 0 {
		w.RetryPause = time.Second
	}
	if w.Logger == nil {
		w.Logger = slog.New(slog.DiscardHandler)
	}
	deadline := time.Now().Add(time.Duration(w.Seconds) * time.Second)
	var lost error // why the daemon was lost, while reconnecting
	for reconnecting := false; ; reconnecting = true {
		c, err := waitClient(ctx, connect, reconnecting, deadline)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return api.WaitResult{}, ctx.Err()
		case !reconnecting:
			return api.WaitResult{}, err
		case !time.Now().Before(deadline):
			// The daemon did not come back before the wait ended. An error that is only the lookup's own deadline
			// says nothing: report why the daemon was lost instead.
			if errors.Is(err, context.DeadlineExceeded) {
				err = lost
			}
			return api.WaitResult{}, fmt.Errorf("the daemon did not come back before the wait ended: %w", err)
		}
		if err == nil {
			// The rest of the time, after any reconnecting: 0 returns the current reports at once.
			left := max(int(math.Ceil(time.Until(deadline).Seconds())), 0)
			wake := w.WakeOnBlocked
			var res api.WaitResult
			res, err = c.Wait(ctx, api.WaitRequest{IDs: w.IDs, TimeoutSeconds: left, Any: w.Any,
				WakeOnBlocked: &wake, Reports: true}, w.Cwd)
			if err == nil {
				if len(res.Reports) != len(w.IDs) {
					return api.WaitResult{}, fmt.Errorf("the daemon returned %d reports for %d tasks",
						len(res.Reports), len(w.IDs))
				}
				return res, nil
			}
		}
		// A restarting daemon is unreachable for a moment, not running yet, or slow to answer its first probe:
		// wait for it until the wait's own deadline.
		retry := errors.Is(err, client.ErrUnreachable) || errors.Is(err, client.ErrNotRunning) || client.TimedOut(err)
		if !retry || !time.Now().Before(deadline) {
			return api.WaitResult{}, err
		}
		lost = err
		w.Logger.Info("the daemon is gone or not answering during a wait; reconnecting", "err", err)
		pause := time.NewTimer(min(w.RetryPause, time.Until(deadline)))
		select {
		case <-ctx.Done():
			pause.Stop()
			return api.WaitResult{}, ctx.Err()
		case <-pause.C:
		}
	}
}

// waitClient is the client for WaitReports. While reconnecting, finding (or starting) the daemon again is bounded
// by the wait's deadline.
func waitClient(ctx context.Context, connect Connect, reconnecting bool, deadline time.Time) (*client.Client,
	error) {
	if !reconnecting {
		return connect(ctx, false)
	}
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return connect(dctx, true)
}
