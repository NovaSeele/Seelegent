package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// Accounts reads accounts and builds the environment of a turn; *account.Manager satisfies it.
type Accounts interface {
	Get(ctx context.Context, id string) (account.Account, error)
	List(ctx context.Context) ([]account.Account, error)
	GetProvider(ctx context.Context, id string) (account.Provider, error)
	Env(ctx context.Context, a account.Account, rules account.EnvRules, opts account.EnvOptions) (account.RunEnv, error)
}

// Pool hands out accounts; *pool.Pool satisfies it.
type Pool interface {
	Wait(ctx context.Context, req pool.Request, onWait func(*pool.Unavailable)) (*pool.Lease, pool.Choice, error)
	Reattach(accountID, taskID string) *pool.Lease
	Handover(ctx context.Context, in pool.HandoverInput) (pool.Handover, error)
	MarkExhausted(ctx context.Context, accountID string, ex pool.Exhaustion) (pool.Cooldown, error)
	MarkAuthFailed(ctx context.Context, accountID, message string) error
	Changed() <-chan struct{}
}

// Usage records the usage of attempts; *usage.Store satisfies it.
type Usage interface {
	RecordAttempt(ctx context.Context, a usage.Attempt) error
}

// Memory gives briefs the project notes and the lead's verdicts and records new verdicts; *memory.Store
// satisfies it.
type Memory interface {
	Get(ctx context.Context, cwd string) (memory.Note, error)
	History(ctx context.Context, cwd string, n int) ([]string, error)
	AddVerdict(ctx context.Context, v memory.Verdict) error
}

// Roles finds the role of a task; *role.Store satisfies it.
type Roles interface {
	Get(ctx context.Context, id, cwd string) (role.Role, error)
	Brief(r role.Role, cwd string) *brief.Role
}

// ConsoleSpec is a takeover console: the task's conversation resumed in a terminal window the user drives.
type ConsoleSpec struct {
	Title  string
	Banner []string
	// Argv resumes the conversation with the task's own backend.
	Argv []string
	Dir  string
	// Env is the account's environment for the CLI (it may hold an API key).
	Env []string
	// ScriptDir and Name place the window's files: when the command ends, its exit code is written to
	// ScriptDir/Name.exit, which the engine also reads after a daemon restart.
	ScriptDir string
	Name      string
}

// ConsoleWait waits until the console's command ends and returns its exit code; it fails when the window was
// closed first.
type ConsoleWait func(ctx context.Context) (int, error)

// ConsoleProcess identifies the process that runs a console's command (its id and start time), so a restarted
// daemon can tell whether the console still runs.
type ConsoleProcess struct {
	Pid   int    `json:"pid"`
	Start uint64 `json:"start,omitempty"`
}

// OpenConsole is a console that was opened.
type OpenConsole struct {
	// Wait follows the console until its command ends.
	Wait ConsoleWait
	// Identity returns the console's process once it is known (ok is false before); nil when it cannot be known.
	Identity func() (p ConsoleProcess, ok bool)
}

// Consoles opens takeover consoles (platform.OpenTerminal in the daemon).
type Consoles interface {
	Open(ctx context.Context, spec ConsoleSpec) (OpenConsole, error)
	// Running reports whether the console process p still runs (false once it ended, even without an exit code).
	Running(p ConsoleProcess) bool
	// Find returns the process of a console an earlier daemon opened, from its exit file (ScriptDir/Name.exit), once
	// the console's command has started (ok is false before, and when it cannot be known).
	Find(exitFile string) (p ConsoleProcess, ok bool)
}

// Options configure a Service. DB, Accounts, Pool, Backends, Settings, TasksDir and Launcher are required.
type Options struct {
	DB       DB
	Accounts Accounts
	Pool     Pool
	// Backends are the worker CLIs by name.
	Backends map[string]backend.Backend
	// Settings returns the current settings.
	Settings func() config.Settings
	// TasksDir holds one folder per task with its attempt folders (config.Paths.Tasks).
	TasksDir string
	// Launcher is the command that runs the detached launcher ({os.Executable(), "__launch"}).
	Launcher []string

	// Usage, QuotaRefresh, Memory, Roles and Consoles are optional.
	Usage Usage
	// QuotaRefresh asks for a background quota read of an account after a turn on it (quota.Refresher.Request);
	// it must not block.
	QuotaRefresh func(accountID string)
	Memory       Memory
	Roles        Roles
	Consoles     Consoles

	// BaseEnv returns the environment workers inherit (os.Environ when nil).
	BaseEnv func() []string
	// Dashboard returns the dashboard link of a task (for reports); nil leaves it empty.
	Dashboard func(taskID string) string
	// Git is the daemon's git executable, for the baseline (gitx.NewBinary over BaseEnv when nil).
	Git *gitx.Binary
	// GitShim is the folder of the git shim (guard.InstallShim) put first on PATH for workers without a pre-tool
	// hook while the gitGuard setting is on; "" leaves PATH alone.
	GitShim string
	// Clock is the time source (the system clock when nil).
	Clock clock.Clock
	// Logger receives engine logs (discarded when nil). Use a logx logger so secrets are masked.
	Logger *slog.Logger
}

// Service is the task engine. It is safe for concurrent use.
type Service struct {
	o    Options
	repo *Repo
	bus  *Bus
	clk  clock.Clock
	log  *slog.Logger

	mu      sync.Mutex
	locks   map[string]*taskLock
	runs    map[string]*run
	watches map[string]*consoleWatch
	// unreadable are the active tasks recovery skipped (Unreadable).
	unreadable []string
	root       context.Context
	cancel     context.CancelFunc
	started    bool
	closed     bool
	wg         sync.WaitGroup

	depsMu sync.Mutex
}

// New returns a service. Call Start to recover the tasks of an earlier daemon and start the background work.
func New(o Options) (*Service, error) {
	switch {
	case o.DB == nil || o.Accounts == nil || o.Pool == nil || o.Settings == nil:
		return nil, errors.New("task: db, accounts, pool and settings are required")
	case len(o.Backends) == 0:
		return nil, errors.New("task: no backends")
	case o.TasksDir == "" || len(o.Launcher) == 0:
		return nil, errors.New("task: tasks directory and launcher are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.BaseEnv == nil {
		o.BaseEnv = os.Environ
	}
	if o.Git == nil {
		o.Git = gitx.NewBinary(o.BaseEnv, o.Logger)
	}
	root, cancel := context.WithCancel(context.Background())
	return &Service{
		o: o, repo: NewRepo(o.DB, o.Clock.Now), bus: NewBus(), clk: o.Clock, log: o.Logger,
		locks: map[string]*taskLock{}, runs: map[string]*run{}, watches: map[string]*consoleWatch{},
		root: root, cancel: cancel,
	}, nil
}

// Bus returns the engine's notifier (the SSE stream subscribes to it).
func (s *Service) Bus() *Bus { return s.bus }

// Repo returns the engine's repository (read access for other features).
func (s *Service) Repo() *Repo { return s.repo }

// Start recovers the tasks an earlier daemon left (see recovery.go) and starts the attention watcher. It returns
// once recovery has been scheduled; the recovered tasks continue in the background.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return errors.New("task: already started or closed")
	}
	s.started = true
	s.mu.Unlock()
	if err := s.repo.BackfillFold(ctx); err != nil {
		return fmt.Errorf("task: search titles: %w", err)
	}
	if err := s.recoverTasks(ctx); err != nil {
		return err
	}
	s.spawn("attention", "", s.watchAttention)
	return nil
}

// Close stops the engine: it stops following workers and waits (until ctx ends) for its goroutines. Workers keep
// running under their launchers; the next daemon attaches to them again.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("task: close: %w", ctx.Err())
	}
}

// spawn runs fn in a goroutine that the service waits for on Close. A panic is logged and, when taskID is set,
// fails that task instead of crashing the daemon (old bug B2).
func (s *Service) spawn(name, taskID string, fn func()) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("task: panic", "goroutine", name, "task", taskID, "panic", fmt.Sprint(v),
					"stack", string(debug.Stack()))
				if taskID != "" {
					s.fail(taskID, fmt.Sprintf("internal error: %v", v))
				}
			}
		}()
		fn()
	}()
	return true
}

// lock takes the lock of one task and returns its release. Every change to a task (record, turns, attempts,
// offsets) happens under it.
func (s *Service) lock(id string) (unlock func()) {
	s.mu.Lock()
	l := s.locks[id]
	if l == nil {
		l = &taskLock{}
		s.locks[id] = l
	}
	l.refs++
	s.mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		s.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, id) // nobody holds or waits for it: the map keeps only tasks in use
		}
		s.mu.Unlock()
	}
}

// taskLock serializes the changes of one task; refs counts its holders and waiters.
type taskLock struct {
	sync.Mutex
	refs int
}

// errNoChange makes mutate skip the save.
var errNoChange = errors.New("no change")

// mutate loads task id, lets fn change it and returns the events describing the change, then saves the task (all
// its turns) and appends the events in one transaction, and publishes them. Attention and needsSince are brought
// up to date on every change. fn returning errNoChange (with no events) leaves the task untouched.
func (s *Service) mutate(ctx context.Context, id string, fn func(t *Task) ([]NewEvent, error)) (*Task, error) {
	defer s.lock(id)()
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	evs, err := fn(t)
	switch {
	case errors.Is(err, errNoChange) && len(evs) == 0:
		if !s.syncAttention(t, &evs) {
			return t, nil
		}
	case err != nil:
		return nil, err
	default:
		s.syncAttention(t, &evs)
	}
	t.UpdatedAt = s.clk.Now().UTC()
	out, err := s.repo.Save(ctx, t, nil, evs)
	if err != nil {
		return nil, err
	}
	s.publish(t.ID, out)
	return t, nil
}

// appendEvents appends events (and commits offsets) under the task's lock and publishes them.
func (s *Service) appendEvents(ctx context.Context, id string, evs []NewEvent, off *Offsets) error {
	if len(evs) == 0 && off == nil {
		return nil
	}
	defer s.lock(id)()
	out, err := s.repo.Append(ctx, id, evs, off)
	if err != nil {
		return err
	}
	if len(out) > 0 {
		ns := make([]Notice, len(out))
		for i := range out {
			ns[i] = Notice{Kind: NoticeEvent, TaskID: id, Event: &out[i]}
		}
		s.bus.Publish(ns...)
	}
	return nil
}

func (s *Service) publish(id string, evs []api.TaskEvent) { s.publishNotices(id, false, evs) }

// publishNotices publishes the change of task id (created marks a new task) and its new events.
func (s *Service) publishNotices(id string, created bool, evs []api.TaskEvent) {
	ns := []Notice{{Kind: NoticeTask, TaskID: id, Created: created}}
	for i := range evs {
		ns = append(ns, Notice{Kind: NoticeEvent, TaskID: id, Event: &evs[i]})
	}
	s.bus.Publish(ns...)
}

// fail ends a task as failed with message (used for unexpected errors).
func (s *Service) fail(id, message string) {
	ctx := context.WithoutCancel(s.root)
	_, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		if Finished(t.Status) {
			return nil, errNoChange
		}
		var evs []NewEvent
		turnN := 0
		if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
			now := s.clk.Now().UTC()
			for _, a := range tu.Attempts {
				if a.Status == AttemptRunning {
					a.Status, a.EndedAt, a.Error = AttemptError, now, message
				}
			}
			tu.Status, tu.EndedAt = TurnFailed, now
			turnN = tu.N
			evs = append(evs, NewEvent{Type: api.EventTurnEnd, Turn: tu.N,
				Data: api.TurnEndData{Status: TurnFailed, Files: api.NonNil(tu.Files), Usage: api.UsageOf(tu.Usage)}})
		}
		t.Status, t.Error, t.Manual, t.Console, t.CancelRequested = StatusFailed, message, false, nil, false
		evs = append([]NewEvent{{Type: api.EventError, Turn: turnN, Data: api.ErrorData{Message: message}}}, evs...)
		return evs, nil
	})
	if err != nil {
		s.log.Error("task: cannot record the failure", "task", id, "err", err)
		return
	}
	s.releaseConsole(id)
	s.checkDeps(ctx)
}

// newID returns a fresh task id: t<yyMMddHHmmss>-<4 hex>.
func (s *Service) newID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "t" + s.clk.Now().UTC().Format("060102150405") + "-" + hex.EncodeToString(b[:])
}

func (s *Service) settings() config.Settings { return s.o.Settings() }

func (s *Service) backend(name string) (backend.Backend, error) {
	b, ok := s.o.Backends[name]
	if !ok || b == nil {
		return nil, invalidf("unknown backend %q", name)
	}
	return b, nil
}

func (s *Service) now() time.Time { return s.clk.Now().UTC() }
