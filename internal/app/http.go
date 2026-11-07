package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

// httpShutdown bounds the graceful end of open requests at Close.
const httpShutdown = 5 * time.Second

// Port returns the port the daemon listens on: $SEELEGENT_PORT (from the daemon's environment) or the settings.
func (a *App) Port() (int, error) {
	return config.ResolvePort(a.Settings(), func(k string) string { v, _ := account.Lookup(a.env, k); return v })
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
	a.settings.Store(&out)
	a.Pool.Notify()
	return out, nil
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
		DevOrigins: a.devOrigins,
	})
	if err != nil {
		_ = ln.Close()
		return "", err
	}
	hs := srv.HTTPServer(ln.Addr().String())
	a.mu.Lock()
	a.api, a.httpSrv = srv, hs
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
	srv, hs := a.api, a.httpSrv
	a.mu.Unlock()
	if hs == nil {
		return nil
	}
	srv.Close()
	sctx, cancel := context.WithTimeout(ctx, httpShutdown)
	defer cancel()
	if err := hs.Shutdown(sctx); err != nil {
		_ = hs.Close()
		return fmt.Errorf("http: shutdown: %w", err)
	}
	return nil
}
