// Package app is the composition root: it builds the daemon's services from the data directory and the settings,
// wires the concrete types together and owns their lifecycle (Start, Close). Only this package knows every
// concrete type; the domain packages declare the interfaces they need.
//
// It wires the task engine with everything it uses (the store, accounts, quota cache and refresher, the account
// pool, usage history, project memory, roles, department members, the worker backends and takeover consoles), git,
// account sign-in, the proof ledger, the team services (review notes, meetings, contests, hooks; team.go) and the
// HTTP API (ListenHTTP).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/automation"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/contest"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/hook"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/internal/meeting"
	"github.com/NovaSeele/Seelegent/internal/member"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/notes"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/signin"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/ticket"
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
	// GuardExe is the seelegent executable the git guard hooks and the git shim run (os.Executable() when empty).
	GuardExe string
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

	// Info identifies the build (Health, hello).
	Info buildinfo.Info
	// Web is the built dashboard the HTTP API serves (none when nil).
	Web fs.FS
	// SignInOpener opens sign-in windows (terminal windows when nil).
	SignInOpener signin.Opener
	// Classifier decides who calls the HTTP API (the worker process trees when nil).
	Classifier httpapi.Classifier
	// DevOrigins are extra loopback browser origins the HTTP API accepts (the Vite dev server); see httpapi.Options.
	DevOrigins []string
	// ImageWriteTimeout is httpapi.Options.ImageWriteTimeout (30 s when 0); tests shorten it.
	ImageWriteTimeout time.Duration
	// Gh is the gh CLI (looked up on Env's PATH when empty); NoGh means there is none (tests: the real gh must
	// never run).
	Gh   string
	NoGh bool
	// ReadMachine reads the machine profile the auto resource settings are computed from (the system's, from
	// platform.Memory, platform.CPUs and runtime.GOOS, when nil). It is read at start and when the settings change.
	ReadMachine func() config.Machine
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
	Proof     *proof.Service
	Git       *gitx.Repos
	SignIn    *signin.Service
	Members   *member.Store
	Notes     *notes.Store
	Meetings  *meeting.Service
	Contests  *contest.Service
	Hooks     *hook.Service
	// GitHub, Tickets and Automations are milestone 15 (github.go).
	GitHub      *github.Driver
	Tickets     *ticket.Service
	Automations *automation.Service

	hookLog io.Closer

	settings atomic.Pointer[config.Settings]
	log      *slog.Logger
	env      []string
	// toolchains pins RUSTUP_TOOLCHAIN for workers, gates and regression runs in Rust repositories.
	toolchains *account.Toolchains

	info       buildinfo.Info
	web        fs.FS
	classifier httpapi.Classifier
	devOrigins []string
	redact     func(string) string
	started0   time.Time
	pid        int
	patchMu    sync.Mutex
	// imageWriteTimeout is Options.ImageWriteTimeout.
	imageWriteTimeout time.Duration
	// rescheduler re-arms the quota refresher when quotaRefreshMinutes changes (the Refresher; a fake in tests).
	rescheduler interface{ Reschedule() }
	// sampler is the cached memory reading the task engine and GET /health share; slots are the gate slots
	// (gateConcurrency) of task gates and regression runs.
	sampler *task.Sampler
	slots   *gate.Slots
	// machine is the machine profile (readMachine at start and on each settings change), never read per sample, so
	// the auto thresholds hold still between readings.
	machine     atomic.Pointer[config.Machine]
	readMachine func() config.Machine
	// setMemoryLimit is debug.SetMemoryLimit (a fake in tests); memoryLimitSet says daemonMemoryLimitMB set it.
	setMemoryLimit func(int64) int64
	memoryLimitMu  sync.Mutex
	memoryLimitSet bool
	// stopCh is closed (once) when someone asked the daemon to stop through the API.
	stopCh   chan struct{}
	stopOnce sync.Once

	mu      sync.Mutex
	api     *httpapi.Server
	httpSrv *http.Server
	// httpConns are the open connections of httpSrv, so closeHTTP can drop the ones that never sent a request.
	httpConns *connTracker
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
	closed    bool
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
	if o.GuardExe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("app: own executable: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		o.GuardExe = exe
	}
	a := &App{Paths: o.Paths, log: o.Logger, env: o.Env, info: o.Info, web: o.Web, classifier: o.Classifier,
		devOrigins: o.DevOrigins, imageWriteTimeout: o.ImageWriteTimeout,
		redact: logx.NewRedactor().String, started0: o.Clock.Now(), pid: os.Getpid(), stopCh: make(chan struct{}),
		setMemoryLimit: debug.SetMemoryLimit, readMachine: o.ReadMachine}
	if a.readMachine == nil {
		a.readMachine = systemMachine
	}
	a.refreshMachine()
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
	a.Members = member.New(member.Options{DB: a.DB, Providers: a.Accounts, Now: now,
		Changed: func() { a.withAPI(func(s *httpapi.Server) { s.PublishMembers() }) }})

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
		opts := []backend.Option{backend.WithGuard(backend.Guard{Exe: o.GuardExe,
			Enabled: func() bool { return a.Settings().GitGuard }})}
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
		// The idle policy: a pending task or an open dashboard keeps the cycles at quotaRefreshMinutes.
		Busy: func(ctx context.Context) bool {
			clients := 0
			a.withAPI(func(s *httpapi.Server) { clients = s.Hub().Clients() })
			return clients > 0 || a.Tasks != nil && a.Tasks.Busy(ctx)
		},
	})
	if err != nil {
		return err
	}
	a.rescheduler = a.Refresher
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
	// One git executable for the daemon, found through its PATH (never a Windows batch shim), shared by the task
	// engine's baseline and the git views.
	gitBin := gitx.NewBinary(func() []string { return a.env }, a.log)
	// The git shim guards workers whose CLI has no pre-tool hook (codex, system accounts). Without it they keep
	// only their sandbox and the work-at-risk check.
	shim := installShim(a.log, o.Paths.GuardBin, o.GuardExe)
	a.Git = gitx.New(now, gitBin)
	ledger := proof.NewLedger(a.DB, now)
	// One toolchain probe for the daemon: worker turns, task gates and regression runs get the same pin (cached
	// per folder).
	a.toolchains = account.NewToolchains(o.RealHome, nil, now)
	a.sampler = task.NewSampler(platform.Memory, o.Clock, a.Limits)
	a.slots = gate.NewSlots(func() int { return a.Limits().GateConcurrency })
	a.Tasks, err = task.New(task.Options{
		DB: a.DB, Accounts: a.Accounts, Pool: p, Backends: a.Backends, Settings: a.Settings,
		TasksDir: o.Paths.Tasks, DataDir: o.Paths.Home, Launcher: o.Launcher, Usage: a.Usage,
		QuotaRefresh: a.Refresher.AfterRun, Memory: a.Memory, Roles: a.Roles, Consoles: consoles, Clock: o.Clock,
		Logger:        a.log,
		BaseEnv:       func() []string { return a.env },
		Toolchains:    a.toolchains,
		Dashboard:     a.dashboardLink,
		Git:           gitBin,
		GitShim:       shim,
		Snapshots:     snapshot.New(o.Paths.Snapshots, a.Git, now),
		Repos:         a.Git,
		ScopeAllowed:  scopeAllowed(o.Paths.Profiles, a.env),
		AccountStates: p.States,
		Ledger:        ledger,
		Members:       a.Members,
		ContestEnded:  a.contestEnded,
		Resources:     a.sampler,
		GateSlots:     a.slots,
		Machine:       a.Machine,
		OnLowMemory: func(low bool, m platform.MemoryStatus) {
			a.withAPI(func(s *httpapi.Server) { s.PublishResourcesChanged(low, m) })
		},
	})
	if err != nil {
		return err
	}
	a.Proof = proof.New(proof.Options{Ledger: ledger, Git: proofGit{a.Git}, Exec: a.proofExec, Active: a.Tasks.ActiveIn,
		HoldsGateSlot: a.Tasks.HoldsGateSlot, TaskFeatures: a.Tasks.SpecFeatures, Publish: a.publishProofRun,
		LogDir: o.Paths.Logs, Now: now, Logger: a.log})
	if err := a.buildTeam(now); err != nil {
		return err
	}
	a.buildGitHub(o)
	a.SignIn = signin.New(signin.Options{
		Accounts: a.Accounts,
		Backend: func(name string) (backend.Backend, bool) {
			b, ok := a.Backends[name]
			return b, ok
		},
		Links:   func() []string { return a.Settings().LinkIntoProfiles },
		Hold:    p.Hold,
		Opener:  o.SignInOpener,
		BaseEnv: func() []string { return a.env },
		Publish: a.publishLogin,
		OnDone: func(id string, ok bool) {
			p.Notify()
			if ok {
				a.Refresher.Request(id)
			}
		},
		Logger: a.log,
	})
	return nil
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

// installShim installs the git shim into dir and returns dir, or "" when workers without a pre-tool hook get no
// shim. When installing fails but the shim of an earlier run is still there (a reattached worker running the old
// git.exe blocks the rename over it), that one keeps guarding them: an older build's guard beats none.
func installShim(log *slog.Logger, dir, exe string) string {
	err := guard.InstallShim(dir, exe)
	if err == nil {
		return dir
	}
	if rerr := guard.ShimReady(dir); rerr == nil {
		log.Warn("app: install the git shim; keeping the one already installed", "err", err)
		return dir
	}
	log.Warn("app: install the git shim; workers without a pre-tool hook run unguarded", "err", err)
	return ""
}

// scopeAllowed lists the folders outside a task's repository a worker may write to without a scope warning: the
// temp folders, the worker profiles and the CLI homes of system accounts.
func scopeAllowed(profiles string, env []string) []string {
	out := []string{profiles, os.TempDir()}
	for _, name := range []string{"TEMP", "TMP", "TMPDIR"} {
		if v, ok := envx.Lookup(env, name); ok && v != "" {
			out = append(out, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, d := range []string{".gemini", ".claude", ".codex"} {
			out = append(out, filepath.Join(home, d))
		}
	}
	return out
}

// Settings returns the current settings.
func (a *App) Settings() config.Settings { return *a.settings.Load() }

// Machine is the machine profile the auto resource settings are computed from.
func (a *App) Machine() config.Machine { return *a.machine.Load() }

// Limits are the effective values of the machine-sized settings (config.EffectiveResources).
func (a *App) Limits() config.Resources { return config.EffectiveResources(a.Settings(), a.Machine()) }

// refreshMachine reads the machine profile again.
func (a *App) refreshMachine() {
	m := a.readMachine()
	a.machine.Store(&m)
}

// systemMachine is this machine's profile: total memory (the cgroup limit when lower), total commit (Windows) and
// the CPUs the daemon may use.
func systemMachine() config.Machine {
	m := platform.Memory()
	return config.NewMachine(m.TotalPhysMB, m.TotalCommitMB, platform.CPUs(), runtime.GOOS)
}

// dashboardLink is the deep link to a task in the dashboard.
func (a *App) dashboardLink(id string) string {
	port, err := config.ResolvePort(a.Settings(), func(k string) string { v, _ := envx.Lookup(a.env, k); return v })
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
	a.applyMemoryLimit(a.Settings().DaemonMemoryLimitMB)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.Refresher.Run(runCtx)
	}()
	if err := a.Proof.Start(ctx); err != nil {
		a.log.Warn("app: abandon stale proof runs", "err", err)
	}
	if err := a.Tasks.Start(ctx); err != nil {
		cancel()
		a.wg.Wait()
		return err
	}
	a.startTeam(ctx, runCtx)
	a.startGitHub(ctx)
	return nil
}

// applyMemoryLimit sets the daemon's soft memory limit to mb MiB (daemonMemoryLimitMB), or lifts the one it set
// when mb is 0. A GOMEMLIMIT in the daemon's environment wins: the setting is not applied then.
func (a *App) applyMemoryLimit(mb int) {
	if v, ok := envx.Lookup(a.env, "GOMEMLIMIT"); ok && v != "" {
		return
	}
	a.memoryLimitMu.Lock()
	defer a.memoryLimitMu.Unlock()
	switch {
	case mb > 0:
		a.setMemoryLimit(int64(mb) << 20)
		a.memoryLimitSet = true
	case a.memoryLimitSet:
		a.setMemoryLimit(math.MaxInt64) // the runtime's default: no limit
		a.memoryLimitSet = false
	}
}

// RequestStop asks the process that runs the App to shut it down (POST /daemon/stop). It only signals: the owner
// (`seelegent daemon`) sees StopRequested and closes the App as on Ctrl+C.
func (a *App) RequestStop() {
	a.stopOnce.Do(func() {
		a.log.Info("app: stop requested")
		close(a.stopCh)
	})
}

// StopRequested is closed once RequestStop was called.
func (a *App) StopRequested() <-chan struct{} { return a.stopCh }

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
	errs := []error{a.closeHTTP(ctx)}
	a.SignIn.Close()
	a.Proof.Close()
	a.closeGitHub()
	a.closeTeam(ctx)
	errs = append(errs, a.Tasks.Close(ctx))
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
