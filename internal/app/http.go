package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

// httpShutdown bounds the graceful end of open requests at Close.
const httpShutdown = 5 * time.Second

// Port returns the port the daemon listens on: $SEELEGENT_PORT (from the daemon's environment) or the settings.
func (a *App) Port() (int, error) {
	return config.ResolvePort(a.Settings(), func(k string) string { v, _ := envx.Lookup(a.env, k); return v })
}

// PatchSettings applies a JSON merge patch (config.MergePatch) to the running settings and writes them to
// config.json under its lock, keeping the file's unknown keys. All or nothing: an invalid patch changes neither.
func (a *App) PatchSettings(ctx context.Context, patch []byte) (config.Settings, error) {
	a.patchMu.Lock()
	defer a.patchMu.Unlock()
	var out config.Settings
	_, err := config.Update(ctx, a.Paths.Config, func(s *config.Settings) error {
		next, err := config.MergePatch(a.Settings(), patch)
		if err != nil {
			return err
		}
		*s, out = next, next
		return nil
	})
	if err != nil {
		return config.Settings{}, err
	}
	prev := a.settings.Swap(&out)
	a.settingsChanged(*prev, out)
	return out, nil
}

// settingsChanged tells the services that read a setting only when they arm a timer or wait: the pool (limits,
// maxConcurrentWorkers and rest periods), the quota refresher (quotaRefreshMinutes, re-armed from its last cycle),
// the gate slots (gateConcurrency), the starts held for memory (the thresholds) and the daemon's memory limit.
func (a *App) settingsChanged(prev, next config.Settings) {
	a.Pool.Notify()
	if prev.QuotaRefreshMinutes != next.QuotaRefreshMinutes {
		a.rescheduler.Reschedule()
	}
	a.refreshMachine()
	a.slots.Notify()
	a.sampler.Kick()
	if prev.DaemonMemoryLimitMB != next.DaemonMemoryLimitMB {
		a.applyMemoryLimit(next.DaemonMemoryLimitMB)
	}
}

// settingsStore adapts the App to httpapi.Settings.
type settingsStore struct{ a *App }

func (s settingsStore) Settings() config.Settings { return s.a.Settings() }

func (s settingsStore) PatchSettings(ctx context.Context, patch []byte) (config.Settings, error) {
	return s.a.PatchSettings(ctx, patch)
}

// ListenHTTP starts the HTTP API on 127.0.0.1:port (port 0 picks a free port, for tests) and returns the address
// it listens on. Close stops it.
func (a *App) ListenHTTP(ctx context.Context, port int) (string, error) {
	a.mu.Lock()
	if a.closed || a.httpSrv != nil {
		a.mu.Unlock()
		return "", errors.New("app: closed or already listening")
	}
	a.mu.Unlock()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("listen on 127.0.0.1:%d: %w", port, err)
	}
	actual := ln.Addr().(*net.TCPAddr).Port
	srv, err := httpapi.New(httpapi.Options{
		Port: actual, Version: a.info.Version, Commit: a.info.Commit, StartedAt: a.started0, PID: a.pid,
		Tasks: a.Tasks, Accounts: a.Accounts, Pool: a.Pool, Quota: a.Refresher, Usage: a.Usage, Memory: a.Memory,
		Roles: a.Roles, Settings: settingsStore{a}, SignIn: a.SignIn, Git: a.Git, Backends: a.Backends,
		LogDir: a.Paths.Logs, Redact: a.redact, Web: a.web, Classifier: a.classifier, Logger: a.log,
		DevOrigins: a.devOrigins, Stop: a.RequestStop, Proof: a.Proof, Gates: a.Tasks, HomeID: config.HomeID(a.Paths.Home),
		Members: a.Members, Consults: a.Tasks, Notes: a.Notes, Meetings: a.Meetings, Contests: a.Contests,
		Hooks: a.Hooks, Tickets: a.Tickets, Automations: a.Automations, FreeMemory: a.sampler.Sample,
		LowMemory: a.Tasks.LowMemory, Machine: a.Machine, Activity: a.Refresher.Touch,
	})
	if err != nil {
		_ = ln.Close()
		return "", err
	}
	hs := srv.HTTPServer(ln.Addr().String())
	conns := newConnTracker()
	conns.wrap(hs)
	a.mu.Lock()
	a.api, a.httpSrv, a.httpConns = srv, hs, conns
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Error("http: serve", "err", err)
		}
	}()
	a.log.Info("http: listening", "addr", ln.Addr().String())
	return ln.Addr().String(), nil
}

// publishLogin forwards sign-in progress to the SSE stream once the HTTP API runs.
func (a *App) publishLogin(l api.LoginSession) {
	a.mu.Lock()
	srv := a.api
	a.mu.Unlock()
	if srv != nil {
		srv.PublishLogin(l)
	}
}

// closeHTTP ends the SSE streams, then lets open requests finish (bounded) and stops the listener.
func (a *App) closeHTTP(ctx context.Context) error {
	a.mu.Lock()
	srv, hs, conns := a.api, a.httpSrv, a.httpConns
	a.mu.Unlock()
	if hs == nil {
		return nil
	}
	srv.Close()
	return shutdownHTTP(ctx, hs, conns, httpShutdown, a.log)
}

// shutdownHTTP stops hs: connections that never sent a request are closed at once (a browser opens spare ones
// ahead of time, and http.Server.Shutdown waits up to five seconds for each before it counts it idle), idle
// keep-alive connections are closed by Shutdown, and open requests get timeout to finish. When they do not, the
// remaining connections are closed: that is a warning, not an error, since the server did stop.
func shutdownHTTP(ctx context.Context, hs *http.Server, conns *connTracker, timeout time.Duration,
	log *slog.Logger) error {
	conns.closeNew()
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := hs.Shutdown(sctx)
	if err == nil {
		return nil
	}
	if cerr := hs.Close(); cerr != nil {
		return fmt.Errorf("http: shutdown: %w", errors.Join(err, cerr))
	}
	log.Warn("http: open requests did not finish in time; their connections were closed", "timeout", timeout,
		"err", err)
	return nil
}

// connTracker follows the connections of an http.Server through its ConnState hook.
type connTracker struct {
	mu    sync.Mutex
	conns map[net.Conn]http.ConnState
}

func newConnTracker() *connTracker { return &connTracker{conns: map[net.Conn]http.ConnState{}} }

// wrap installs the tracker as hs.ConnState, keeping a hook already set.
func (t *connTracker) wrap(hs *http.Server) {
	prev := hs.ConnState
	hs.ConnState = func(c net.Conn, st http.ConnState) {
		t.mu.Lock()
		if st == http.StateClosed || st == http.StateHijacked {
			delete(t.conns, c)
		} else {
			t.conns[c] = st
		}
		t.mu.Unlock()
		if prev != nil {
			prev(c, st)
		}
	}
}

// closeNew closes the connections that have not started a request yet.
func (t *connTracker) closeNew() {
	if t == nil {
		return
	}
	t.mu.Lock()
	var fresh []net.Conn
	for c, st := range t.conns {
		if st == http.StateNew {
			fresh = append(fresh, c)
		}
	}
	t.mu.Unlock()
	for _, c := range fresh {
		_ = c.Close()
	}
}
