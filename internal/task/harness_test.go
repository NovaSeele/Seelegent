package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/member"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// TestMain runs the tests, the fake worker CLI, or the fake gate command (gate-check, gate_test.go).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "gate-check" {
		os.Exit(gateCheck(os.Args[2:]))
	}
	fakecli.Main(m)
}

// waitLimit bounds every wait on real processes.
const waitLimit = 60 * time.Second

// harness is one daemon's engine on a temp data directory, with the real launcher, the real pool and account
// manager, the agy backend whose binary is the fake CLI, and a fake clock.
type harness struct {
	t        *testing.T
	dir      string
	cwd      string
	db       *store.Store
	clk      *clock.Fake
	accounts *account.Manager
	queue    *fakecli.Queue
	settings atomic.Pointer[config.Settings]
	usage    *usage.Store
	memory   *memory.Store
	roles    *role.Store
	members  *member.Store
	consoles *fakeConsoles
	pool     *pool.Pool
	s        *Service

	mu        sync.Mutex
	refreshes []string
	// contestStatus is the status of each contest for Options.ContestEnded (guarded by mu).
	contestStatus map[string]string
}

var harnessEpoch = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

// newHarness builds and starts an engine. Accounts added before Start are seen by recovery.
func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cwd := filepath.Join(dir, "project")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, dir: dir, cwd: cwd, db: db, clk: clock.NewFake(harnessEpoch), queue: fakecli.NewQueue(t),
		consoles: &fakeConsoles{}}
	h.accounts = account.NewManager(db, account.NewProfiles(profiles, filepath.Join(dir, "real")), h.clk.Now)
	set := config.Defaults()
	// No automatic fix turns unless a test asks for them: each would need a scripted worker.
	set.GateMaxAutoFixes = 0
	set.GateSettleMinutes = 0 // the fake clock never moves on its own
	h.settings.Store(&set)
	h.usage = usage.NewStore(db, h.clk, time.UTC)
	h.memory = memory.New(db, h.clk.Now)
	h.roles = role.New(role.Options{DB: db, Home: filepath.Join(dir, "home"), Now: h.clk.Now})
	h.members = member.New(member.Options{DB: db, Providers: h.accounts, Now: h.clk.Now})
	t.Cleanup(h.killWorkers) // runs after the engine is closed
	h.start()
	return h
}

// killWorkers stops every worker a test left running, so its folders can be removed.
func (h *harness) killWorkers() {
	ids, err := h.s.repo.IDs(context.Background(), Statuses()...)
	if err != nil {
		h.t.Error(err)
		return
	}
	for _, id := range ids {
		tk, err := h.s.repo.Get(context.Background(), id)
		if err != nil {
			continue
		}
		for _, tu := range tk.Turns {
			for _, a := range tu.Attempts {
				hd, err := launch.Attach(a.LaunchDir)
				if err != nil {
					continue
				}
				if _, ended, _ := hd.Exited(); ended {
					continue
				}
				_ = hd.Cancel()
				ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
				_, _ = hd.Wait(ctx)
				cancel()
			}
		}
	}
}

// start opens a new engine (a new daemon) on the same data.
func (h *harness) start() {
	t := h.t
	t.Helper()
	p, err := pool.New(pool.Options{Accounts: h.accounts, Quotas: quota.NewStore(h.db), DB: h.db, Clock: h.clk,
		Settings: h.config, ActiveQuota: func(string) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	agy, err := backend.New(config.BackendAgy, fakecli.Exe(t))
	if err != nil {
		t.Fatal(err)
	}
	repos := gitx.New(h.clk.Now, nil)
	s, err := New(Options{
		DB: h.db, Accounts: h.accounts, Pool: p, Backends: map[string]backend.Backend{config.BackendAgy: agy},
		Settings: h.config, TasksDir: filepath.Join(h.dir, "tasks"), Launcher: fakecli.Launcher(t),
		Usage: h.usage, Memory: h.memory, Roles: h.roles, Members: h.members, Consoles: h.consoles, Clock: h.clk,
		QuotaRefresh: func(id string) {
			h.mu.Lock()
			h.refreshes = append(h.refreshes, id)
			h.mu.Unlock()
		},
		BaseEnv:       func() []string { return h.queue.Env(os.Environ()) },
		Dashboard:     func(id string) string { return "http://127.0.0.1:47831/tasks/" + id },
		Snapshots:     snapshot.New(filepath.Join(h.dir, "snapshots"), repos, h.clk.Now),
		Repos:         repos,
		AccountStates: p.States,
		Ledger:        proof.NewLedger(h.db, h.clk.Now),
		ContestEnded: func(_ context.Context, id string) (string, bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			st := h.contestStatus[id]
			return st, st == api.ContestStatusApplied || st == api.ContestStatusCancelled ||
				st == api.ContestStatusFailed, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.pool, h.s = p, s
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.stop() })
}

// stop closes the engine; workers keep running (a daemon restart).
func (h *harness) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.s.Close(ctx); err != nil {
		h.t.Error(err)
	}
}

func (h *harness) config() config.Settings { return *h.settings.Load() }

// agy adds an enabled agy sign-in account; accounts with a higher priority are chosen first.
func (h *harness) agy(label string, priority int) account.Account {
	h.t.Helper()
	a, _, err := h.accounts.Add(h.t.Context(), account.New{Backend: config.BackendAgy, Kind: account.KindIsolated,
		Label: label, Priority: priority, Enabled: true}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	return a
}

// delegate creates a task in the harness project.
func (h *harness) delegate(req api.DelegateRequest) api.TaskSummary {
	h.t.Helper()
	if req.Cwd == "" {
		req.Cwd = h.cwd
	}
	if req.Prompt == "" {
		req.Prompt = "Fix the handler."
	}
	sum, err := h.s.Delegate(h.t.Context(), req)
	if err != nil {
		h.t.Fatal(err)
	}
	return sum
}

// waitFor polls cond (real time) until it holds.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *harness) task(id string) *Task {
	h.t.Helper()
	t, err := h.s.repo.Get(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return t
}

// waitStatus waits until task id has status and no run is going.
func (h *harness) waitStatus(id string, want Status) *Task {
	h.t.Helper()
	h.waitFor(fmt.Sprintf("%s to be %s", id, want), func() bool {
		return h.task(id).Status == want && !h.s.hasRun(id)
	})
	return h.task(id)
}

// waitEvent waits until task id has an event of type typ and returns the events.
func (h *harness) waitEvent(id string, typ api.EventType) []api.TaskEvent {
	h.t.Helper()
	var evs []api.TaskEvent
	h.waitFor(fmt.Sprintf("a %s event of %s", typ, id), func() bool {
		evs = h.events(id)
		for _, e := range evs {
			if e.Type == typ {
				return true
			}
		}
		return false
	})
	return evs
}

func (h *harness) events(id string) []api.TaskEvent {
	h.t.Helper()
	evs, err := h.s.repo.Events(context.Background(), id, 0, 100000)
	if err != nil {
		h.t.Fatal(err)
	}
	return evs
}

// eventsOf filters events by type.
func eventsOf(evs []api.TaskEvent, typ api.EventType) []api.TaskEvent {
	var out []api.TaskEvent
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// decode reads an event's data into v.
func decode(t *testing.T, e api.TaskEvent, v any) {
	t.Helper()
	data, err := json.Marshal(e.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

// runningWorker waits until task id has a live worker that has started its conversation.
func (h *harness) runningWorker(id string) {
	h.t.Helper()
	h.waitFor("a live worker of "+id, func() bool {
		l := h.s.live(id)
		return l.running && l.sawInit
	})
}

// fakeConsoles opens takeover consoles that end when the test says so.
// Console i runs as process 1000+i until it exits or is marked gone. Consoles opened while silent is set never
// start their command (no process is ever known), like a terminal that could not run the script.
type fakeConsoles struct {
	mu     sync.Mutex
	specs  []ConsoleSpec
	exits  []chan int
	gone   map[int]bool
	silent map[int]bool
	fail   error
	quiet  bool
	// waiting counts the Wait calls still running.
	waiting atomic.Int32
}

func (f *fakeConsoles) Open(_ context.Context, spec ConsoleSpec) (OpenConsole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return OpenConsole{}, f.fail
	}
	ch := make(chan int, 1)
	pid := 1000 + len(f.specs)
	f.specs = append(f.specs, spec)
	f.exits = append(f.exits, ch)
	if f.quiet {
		if f.silent == nil {
			f.silent = map[int]bool{}
		}
		f.silent[pid] = true
	}
	identity := func() (ConsoleProcess, bool) { return f.identity(pid) }
	return OpenConsole{
		Wait: func(ctx context.Context) (int, error) {
			f.waiting.Add(1)
			defer f.waiting.Add(-1)
			select {
			case code := <-ch:
				return code, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		},
		Identity: identity,
	}, nil
}

func (f *fakeConsoles) identity(pid int) (ConsoleProcess, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.silent[pid] {
		return ConsoleProcess{}, false
	}
	return ConsoleProcess{Pid: pid, Start: 1}, true
}

// Find finds a console by its exit file, as a restarted daemon does.
func (f *fakeConsoles) Find(exitFile string) (ConsoleProcess, bool) {
	f.mu.Lock()
	var pid int
	for i := range f.specs {
		if samePath(filepath.Join(f.specs[i].ScriptDir, f.specs[i].Name+".exit"), exitFile) {
			pid = 1000 + i
		}
	}
	f.mu.Unlock()
	if pid == 0 {
		return ConsoleProcess{}, false
	}
	return f.identity(pid)
}

func (f *fakeConsoles) Running(p ConsoleProcess) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.gone[p.Pid]
}

// close marks console i's process gone without an exit code (a closed window).
func (f *fakeConsoles) close(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone == nil {
		f.gone = map[int]bool{}
	}
	f.gone[1000+i] = true
}

// exit ends console i with code.
func (f *fakeConsoles) exit(i, code int) {
	f.mu.Lock()
	ch := f.exits[i]
	f.mu.Unlock()
	ch <- code
}

func (f *fakeConsoles) opened() []ConsoleSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ConsoleSpec(nil), f.specs...)
}

// Scripts in the agy stream format.

func jsonLine(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func agyInit(conv string) string {
	return jsonLine(map[string]any{"event": "init", "conversation_id": conv,
		"init": map[string]any{"model": "gemini-3.8-flash-medium"}})
}

func agyDelta(step int, text string) string {
	return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
		"step_type": "agent_response", "state": "RUNNING", "text_delta": text}})
}

func agyTool(step int, state, command string) string {
	return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
		"step_type": "tool", "state": state, "tool_name": "run_command",
		"tool_info": map[string]any{"parameters": map[string]any{"CommandLine": command}, "output": "ok"}}})
}

func agyEdit(step int, file string) string {
	return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
		"step_type": "tool", "state": "DONE", "tool_name": "replace_file_content",
		"tool_info": map[string]any{"parameters": map[string]any{"TargetFile": file}, "output": "edited"}}})
}

func agyResult(conv, response string) string {
	return jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS",
		"response": response, "conversation_id": conv,
		"usage": map[string]any{"input_tokens": 1000, "output_tokens": 200, "total_tokens": 1200}}})
}

// script joins lines into a fake CLI script.
func script(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// okScript is a whole successful turn.
func okScript(conv, response string) string {
	return script(agyInit(conv), agyDelta(1, response), agyResult(conv, response))
}

func quotaScript(conv string) string {
	return script(agyInit(conv), jsonLine(map[string]any{"event": "result", "result": map[string]any{
		"status": "ERROR", "conversation_id": conv,
		"error": "RESOURCE_EXHAUSTED: You have exhausted your capacity on this model. Your quota will reset after 3h."}}),
		"#!exit 1")
}

func authScript() string {
	return script("#!stderr Error: authentication required. Please sign in again with `agy`.", "#!exit 1")
}

func transientScript() string {
	return script("#!stderr Error: 503 UNAVAILABLE: the service is busy", "#!exit 1")
}

func fatalScript(conv string) string {
	return script(agyInit(conv), jsonLine(map[string]any{"event": "result", "result": map[string]any{
		"status": "ERROR", "error": "model returned an empty response"}}), "#!exit 1")
}

// hangScript starts a conversation and runs until cancelled.
func hangScript(conv string) string { return script(agyInit(conv), agyDelta(1, "working"), "#!hang") }

func mkdir(dir string) error { return os.MkdirAll(dir, 0o700) }

func writeText(path, text string) error { return os.WriteFile(path, []byte(text), 0o600) }
