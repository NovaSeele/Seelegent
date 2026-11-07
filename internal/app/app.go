// Package app is the composition root: it builds the daemon's services from the data directory and the settings,
// wires the concrete types together and owns their lifecycle (Start, Close). Only this package knows every
// concrete type; the domain packages declare the interfaces they need.
//
// So far it wires the task engine with everything it uses: the store, accounts, quota cache and refresher, the
// account pool, usage history, project memory, roles, the worker backends and takeover consoles. The HTTP API,
// MCP server and the other features join here as they are built.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// launchCommand is the hidden command that runs the detached launcher (internal/cli).
const launchCommand = "__launch"

// Options configure an App. Paths is required.
type Options struct {
	// Paths is the data directory.
	Paths config.Paths
	// Settings are used as given when set; otherwise they are loaded from Paths.Config.
	Settings *config.Settings
	// Env is the daemon's environment: where the CLIs are found and what workers inherit (os.Environ when nil).
	Env []string
	// RealHome is the user's home folder (os.UserHomeDir when empty).
	RealHome string
	// Launcher runs the detached launcher ({os.Executable(), "__launch"} when nil).
	Launcher []string
	// Bins sets the CLI of a backend (tests); backends not listed are looked up with backend.Find.
	Bins map[string]string
	// NoQuotaReaders builds the backends without quota readers (tests): every account is then passive.
	NoQuotaReaders bool
	// Consoles opens takeover consoles (terminal windows through platform.OpenTerminal when nil).
	Consoles task.Consoles
	// Clock is the time source (the system clock when nil).
	Clock clock.Clock
	// Logger receives the logs (discarded when nil). Use a logx logger so secrets are masked.
	Logger *slog.Logger
}

// App holds the daemon's services.
type App struct {
	Paths     config.Paths
	DB        *store.Store
	Accounts  *account.Manager
	Quotas    *quota.Store
	Refresher *quota.Refresher
	Pool      *pool.Pool
	Usage     *usage.Store
	Memory    *memory.Store
	Roles     *role.Store
	Backends  map[string]backend.Backend
	Tasks     *task.Service

	settings atomic.Pointer[config.Settings]
	log      *slog.Logger
	env      []string

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	closed  bool
}

// New builds the services. Nothing runs until Start.
func New(ctx context.Context, o Options) (*App, error) {
	if o.Paths.Home == "" {
		return nil, errors.New("app: no data directory")
	}
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.RealHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("app: home folder: %w", err)
		}
		o.RealHome = home
	}
	if o.Launcher == nil {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("app: own executable: %w", err)
		}
		o.Launcher = []string{exe, launchCommand}
	}
	a := &App{Paths: o.Paths, log: o.Logger, env: o.Env}
	if o.Settings != nil {
		s := *o.Settings
		a.settings.Store(&s)
	} else {
		c, err := config.Load(o.Paths.Config)
		if err != nil {
			return nil, err
		}
		a.settings.Store(&c.Settings)
	}
	if err := o.Paths.EnsureDirs(); err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, o.Paths.DB)
	if err != nil {
		return nil, err
	}
	a.DB = db
	if err := a.build(o); err != nil {
		_ = db.Close()
		return nil, err
	}
	return a, nil
}

// build wires the services on the open store.
func (a *App) build(o Options) error {
	now := o.Clock.Now
	a.Accounts = account.NewManager(a.DB, account.NewProfiles(o.Paths.Profiles, o.RealHome), now)
	a.Quotas = quota.NewStore(a.DB)
	a.Usage = usage.NewStore(a.DB, o.Clock, time.Local)
	a.Memory = memory.New(a.DB, now)
	a.Roles = role.New(role.Options{DB: a.DB, Home: o.RealHome, Now: now})

	a.Backends = map[string]backend.Backend{}
	readers := map[string]backend.QuotaReader{}
	for _, name := range backend.Names() {
		bin, ok := o.Bins[name]
		if !ok {
			found, err := backend.Find(name, o.Env, o.RealHome)
			if err != nil && !errors.Is(err, backend.ErrNotInstalled) {
				a.log.Warn("app: find the CLI", "backend", name, "err", err)
			}
			bin = found
		}
		var opts []backend.Option
		if !o.NoQuotaReaders {
			if r := quotaReader(name, bin, now); r != nil {
				opts = append(opts, backend.WithQuotaReader(r))
				readers[name] = r
			}
		}
		b, err := backend.New(name, bin, opts...)
		if err != nil {
			return err
		}
		a.Backends[name] = b
	}

	var err error
	var p *pool.Pool
	a.Refresher, err = quota.NewRefresher(quota.RefresherOptions{
		Accounts: a.Accounts, Store: a.Quotas, Readers: readers, Clock: o.Clock, Logger: a.log,
		Env: func(ctx context.Context, acc account.Account) (account.Profile, []string, error) {
			b := a.Backends[acc.Backend]
			if b == nil {
				return account.Profile{}, nil, fmt.Errorf("unknown backend %q", acc.Backend)
			}
			env, err := a.Accounts.Env(ctx, acc, b, account.EnvOptions{Base: a.env})
			return env.Profile, env.Vars, err
		},
		Interval: func() time.Duration {
			return time.Duration(a.Settings().QuotaRefreshMinutes) * time.Minute
		},
		OnUpdate: func(quota.Snapshot) {
			if p != nil {
				p.Notify()
			}
		},
	})
	if err != nil {
		return err
	}
	p, err = pool.New(pool.Options{
		Accounts: a.Accounts, Quotas: a.Quotas, DB: a.DB, Settings: a.Settings, Refresh: a.Refresher.Refresh,
		Confirm: a.Refresher.Request, Clock: o.Clock, Logger: a.log,
		ActiveQuota: func(name string) bool { return readers[name] != nil },
	})
	if err != nil {
		return err
	}
	a.Pool = p

	consoles := o.Consoles
	if consoles == nil {
		consoles = terminals{}
	}
	a.Tasks, err = task.New(task.Options{
		DB: a.DB, Accounts: a.Accounts, Pool: p, Backends: a.Backends, Settings: a.Settings,
		TasksDir: o.Paths.Tasks, Launcher: o.Launcher, Usage: a.Usage, QuotaRefresh: a.Refresher.Request,
		Memory: a.Memory, Roles: a.Roles, Consoles: consoles, Clock: o.Clock, Logger: a.log,
		BaseEnv:   func() []string { return a.env },
		Dashboard: a.dashboardLink,
	})
	return err
}

// quotaReader returns the quota reader of a backend, or nil.
func quotaReader(name, bin string, now func() time.Time) backend.QuotaReader {
	switch name {
	case config.BackendAgy:
		if bin != "" {
			return quota.NewAgyReader(bin, nil)
		}
	case config.BackendCodex:
		if bin != "" {
			return quota.NewCodexReader(bin, nil, nil, now)
		}
	case config.BackendClaude:
		return quota.NewClaudeReader(nil, "", now)
	}
	return nil
}

// Settings returns the current settings.
func (a *App) Settings() config.Settings { return *a.settings.Load() }

// dashboardLink is the deep link to a task in the dashboard.
func (a *App) dashboardLink(id string) string {
	port, err := config.ResolvePort(a.Settings(), func(k string) string { v, _ := account.Lookup(a.env, k); return v })
	if err != nil {
		port = a.Settings().Port
	}
	return fmt.Sprintf("http://127.0.0.1:%d/tasks/%s", port, id)
}

// Start starts the background work: the quota refresher and the task engine (which recovers what an earlier
// daemon left).
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.started || a.closed {
		a.mu.Unlock()
		return errors.New("app: already started or closed")
	}
	a.started = true
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.cancel = cancel
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.Refresher.Run(runCtx)
	}()
	if err := a.Tasks.Start(ctx); err != nil {
		cancel()
		a.wg.Wait()
		return err
	}
	return nil
}

// Close stops the background work and closes the store. Workers keep running under their launchers; the next
// daemon attaches to them again.
func (a *App) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	cancel := a.cancel
	a.mu.Unlock()
	errs := []error{a.Tasks.Close(ctx)}
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("app: close: %w", ctx.Err()))
	}
	errs = append(errs, a.DB.Close())
	return errors.Join(errs...)
}

// terminals opens takeover consoles as terminal windows.
type terminals struct{}

func (terminals) Open(ctx context.Context, spec task.ConsoleSpec) (task.OpenConsole, error) {
	term, err := platform.OpenTerminal(ctx, platform.TerminalSpec{Title: spec.Title, Banner: spec.Banner,
		Argv: spec.Argv, Dir: spec.Dir, Env: spec.Env, ScriptDir: spec.ScriptDir, Name: spec.Name})
	if err != nil {
		return task.OpenConsole{}, err
	}
	return task.OpenConsole{Wait: term.Wait, Identity: func() (task.ConsoleProcess, bool) {
		pid, start, ok := term.Identity()
		return task.ConsoleProcess{Pid: pid, Start: start}, ok
	}}, nil
}

func (terminals) Running(p task.ConsoleProcess) bool { return platform.ProcessRunning(p.Pid, p.Start) }

func (terminals) Find(exitFile string) (task.ConsoleProcess, bool) {
	pid, start, ok := platform.AttachTerminal(exitFile).Identity()
	return task.ConsoleProcess{Pid: pid, Start: start}, ok
}
