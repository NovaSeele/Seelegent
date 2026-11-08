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
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// SafetyGit is the git access of the post-turn checks; *gitx.Repos satisfies it.
type SafetyGit interface {
	Blob(ctx context.Context, root, rev, rel string, limit int, filters bool) ([]byte, bool, error)
	DiffContent(ctx context.Context, rel string, before, after []byte, beforeMissing, afterMissing bool) (
		api.FileDiff, error)
	Tracked(ctx context.Context, root, suffix string, n int) ([]string, error)
	Normalizes(ctx context.Context, root, rel string, data []byte) (bool, error)
	Lock(ctx context.Context, root string) (func(), error)
}

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
	MarkNoCredit(ctx context.Context, accountID, message string) error
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

// Members finds department members; *member.Store satisfies it.
type Members interface {
	// Get returns a member as it is (it may be turned off).
	Get(ctx context.Context, id string) (api.Member, error)
	// Usable returns a member for new work: it fails when the member is unknown, invalid or turned off.
	Usable(ctx context.Context, id string) (api.Member, error)
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

// Toolchains returns the extra variables (RUSTUP_TOOLCHAIN) for commands run in cwd with environment env; an
// empty map when nothing is pinned. It never fails.
type Toolchains interface {
	Env(ctx context.Context, cwd string, env []string) map[string]string
}

// Resources is the machine's free memory as the resource checks see it (M19): a sampler that caches
// platform.Memory for a short while, so callers may ask often; a fake in tests.
type Resources interface {
	// Sample returns the latest reading. A reading with Known false never holds anything back.
	Sample() platform.MemoryStatus
	// Changed is signaled (without blocking, coalesced) when memory may have been freed or the thresholds
	// changed: a waiting start checks again.
	Changed() <-chan struct{}
}

// GateSlots limits the gate commands that run at the same time (gateConcurrency); *gate.Slots satisfies it.
type GateSlots interface {
	// Acquire waits for a slot in FIFO order and returns the function that gives it back; onWait gets the
	// caller's place in the queue (from 1) while it waits. It fails with ctx's error when ctx ends first, but a slot
	// handed over as ctx ends can still come back with a nil error: defer release() before checking ctx.
	Acquire(ctx context.Context, onWait func(pos int)) (release func(), err error)
	// Limit is the current number of slots (0 = unlimited), for the queued gate event.
	Limit() int
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
	// QuotaRefresh asks for a background quota read of an account after an attempt on it ended, saying whether the
	// attempt failed (quota.Refresher.AfterRun: a run that went well skips the read when the quota is fresh);
	// it must not block.
	QuotaRefresh func(accountID string, failed bool)
	Memory       Memory
	Roles        Roles
	Consoles     Consoles
	// Members are the department members (delegate and followup by member, consults, backend fallback); nil
	// refuses member.
	Members Members
	// ContestEnded reports whether the contest with id was applied, cancelled or failed, and its status
	// (contest.Service.Ended). A followup on a variant task of such a contest is refused: the contest stored the
	// variant's changes once, when it ended, and the views keep showing that copy. It must not take the contest's
	// lock (a pick holds it while it reviews the variant tasks). nil allows every followup.
	ContestEnded func(ctx context.Context, id string) (status string, ended bool, err error)

	// BaseEnv returns the environment workers inherit (os.Environ when nil).
	BaseEnv func() []string
	// Toolchains pins RUSTUP_TOOLCHAIN for worker turns and gate commands in Rust repositories
	// (account.Toolchains, one per daemon so both share its cache); nil pins nothing.
	Toolchains Toolchains
	// Dashboard returns the dashboard link of a task (for reports); nil leaves it empty.
	Dashboard func(taskID string) string
	// Git is the daemon's git executable, for the baseline (gitx.NewBinary over BaseEnv when nil).
	Git *gitx.Binary
	// GitShim is the folder of the git shim (guard.InstallShim) put first on PATH for workers without a pre-tool
	// hook while the gitGuard setting is on; "" leaves PATH alone.
	GitShim string
	// Snapshots records the worktree before and after each turn (work at risk, restore, outside diff); nil turns
	// the post-turn checks off. Repos is its git access for diffs, hygiene and the per-repo lock; both or none.
	Snapshots *snapshot.Store
	Repos     SafetyGit
	// ScopeAllowed are folders outside the repository a worker may write to without a scope warning (temp
	// folders, worker profiles); the task's folder and repository are always allowed.
	ScopeAllowed []string
	// AccountStates returns the pool's view of every account, for the attention rules that depend on accounts
	// (pool.Pool.States); nil leaves those rules out.
	AccountStates func(ctx context.Context) ([]pool.AccountState, error)
	// Ledger records the gate results of spec tasks and counts lane decisions (milestone 13); nil leaves them out.
	Ledger Ledger
	// Resources is the memory reading of the resource checks (minFreeMemoryMB, minFreeCommitMB,
	// criticalFreeMemoryMB); nil turns the checks off.
	Resources Resources
	// GateSlots is shared with the proof regression runs (gateConcurrency); nil runs every gate command at once.
	GateSlots GateSlots
	// OnLowMemory is told when the watchdog starts (low) or stops holding every new start, with the reading that
	// flipped it (the resources.changed frame); nil tells nobody. It must not block.
	OnLowMemory func(low bool, m platform.MemoryStatus)
	// Machine is the machine profile the auto resource settings are computed from (config.EffectiveResources);
	// nil is an unknown machine (the auto values at their low ends).
	Machine func() config.Machine
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
	// claimMu makes a lane claim check and the task's save one step (old bug D25a).
	//
	// Lock order: claimMu first, then a task's lock (mutate). Code that holds a task's lock must never call Delegate,
	// Followup or anything else that takes claimMu: that is the reverse order and can deadlock with a delegate
	// waiting on the same task. A child task (verifier, team member) is therefore created only after the parent's
	// lock has been released.
	claimMu sync.Mutex
	// laneChecked, when set (tests), runs after a followup's lane check and before its save.
	laneChecked func()

	// health is the accounts' state the attention rules read, refreshed by the attention watcher.
	healthMu sync.Mutex
	health   map[string]accountHealth
	// overlaps caches the contention view of each repository for a few seconds.
	overlapMu sync.Mutex
	overlaps  map[string]overlapCache

	// resMu guards the resource state (resources.go): the watchdog's hold (low), whether it stopped a gate in
	// this low-memory episode (stopped), the heavy commands in flight (newest last), the last spaced start, lowWake,
	// closed and replaced whenever low flips, and activity, closed and replaced when a run or a heavy command starts
	// (it wakes an idle watchdog), and slotHolders, the tasks whose gate holds a gate slot now (with how many).
	resMu       sync.Mutex
	low         bool
	stopped     bool
	gateRuns    []*gateRun
	slotHolders map[string]int
	lastAdmit   time.Time
	lowWake     chan struct{}
	activity    chan struct{}
}

// toolEnv is the toolchain pin for commands run in dir with the daemon's environment.
func (s *Service) toolEnv(ctx context.Context, dir string) map[string]string {
	if s.o.Toolchains == nil {
		return nil
	}
	return s.o.Toolchains.Env(ctx, dir, s.o.BaseEnv())
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
	case (o.Snapshots == nil) != (o.Repos == nil):
		return nil, errors.New("task: snapshots and repos go together")
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
		root: root, cancel: cancel, overlaps: map[string]overlapCache{}, lowWake: make(chan struct{}),
		activity: make(chan struct{}),
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
	if err := s.resumeConsults(ctx); err != nil {
		return err
	}
	if err := s.stopOrphanConsults(ctx); err != nil {
		s.log.Warn("task: stop consult runs nobody reads", "err", err)
	}
	s.refreshHealth(ctx)
	s.spawn("attention", "", s.watchAttention)
	s.spawn("sweep", "", s.watchSweep)
	if s.o.Resources != nil {
		s.spawn("memory", "", s.watchMemory)
	}
	return nil
}

// backfillPause is the rest between two batches of the digest backfill, so that it yields to the daemon's work.
const backfillPause = 50 * time.Millisecond

// BackfillDigests stores, in the background, the list digest of the tasks written before the digest columns existed
// (Repo.BackfillDigests): the daemon calls it once it listens, so a large database after an upgrade never delays the
// start. It stops when the engine closes; a task that cannot be read is logged and keeps loading its turns when
// listed. Lists are right before, during and after it.
func (s *Service) BackfillDigests() {
	s.spawn("digests", "", func() {
		ctx := s.root
		pause := func(ctx context.Context) error {
			t := s.clk.NewTimer(backfillPause)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C():
				return nil
			}
		}
		skip := func(id string, err error) {
			s.log.Warn("task: list digest not stored; the task's turns cannot be read", "task", id, "err", err)
		}
		n, err := s.repo.BackfillDigests(ctx, pause, skip)
		switch {
		case err != nil && ctx.Err() == nil:
			s.log.Warn("task: list digest backfill stopped", "stored", n, "err", err)
		case n > 0:
			s.log.Info("task: list digests stored", "tasks", n)
		}
	})
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
