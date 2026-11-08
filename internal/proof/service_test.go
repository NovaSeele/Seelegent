package proof

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/store"
)

type fakeGit struct{ root string }

func (g fakeGit) Root(context.Context, string) (string, error) { return g.root, nil }
func (g fakeGit) Head(context.Context, string) (string, bool)  { return "abc123", true }

func newService(t *testing.T, root string, exec func(cmd string) CmdResult, active []string) (*Service, *[]api.ProofRun) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var mu sync.Mutex
	var published []api.ProofRun
	s := New(Options{Ledger: NewLedger(st, nil), Git: fakeGit{root: root},
		Exec: func(_ context.Context, cmd, dir, _, _ string) CmdResult {
			if same, _ := fsutil.SamePath(dir, root); !same {
				t.Errorf("dir = %s", dir)
			}
			return exec(cmd)
		},
		Active:  func(context.Context, string) ([]string, error) { return active, nil },
		Publish: func(r api.ProofRun) { mu.Lock(); published = append(published, r); mu.Unlock() },
		LogDir:  t.TempDir()})
	t.Cleanup(s.Close)
	return s, &published
}

func code(n int) *int { return &n }

// A run asked for once the service closes is refused with ErrStopping, which the API answers as 503 (M18 hunt).
func TestStartRunWhileStopping(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "spec.md"), specMD)
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	s, _ := newService(t, root, func(string) CmdResult { return CmdResult{Exit: code(0)} }, nil)
	s.Close()
	if _, err := s.StartRun(context.Background(), api.ProofRunRequest{Cwd: root}); !errors.Is(err, ErrStopping) {
		t.Fatalf("StartRun = %v, want ErrStopping", err)
	}
}

func waitRun(t *testing.T, s *Service, cwd, id string) api.ProofRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.RunStatus(context.Background(), cwd, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != api.RunRunning {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the run did not end")
	return api.ProofRun{}
}

func TestRegressionRunAndMatrix(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "spec.md"), specMD)
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	write(t, filepath.Join(root, ".seelegent", "doctrine.json"), `{"version":1,"checks":[{"id":"d","run":"doctrine-cmd"}]}`)
	var mu sync.Mutex
	var ran []string
	s, published := newService(t, root, func(cmd string) CmdResult {
		mu.Lock()
		ran = append(ran, cmd)
		mu.Unlock()
		if cmd == "go build ./..." {
			return CmdResult{Exit: code(2), Tail: "build failed"}
		}
		return CmdResult{Exit: code(0)}
	}, nil)
	ctx := context.Background()
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, root, run.ID)
	if run.State != api.RunRed || run.Counts.Red != 1 || run.Counts.Green != 4 || run.Total != 5 || !run.Dirty ||
		run.GitHead != "abc123" {
		t.Fatalf("run = %+v", run)
	}
	if strings.Join(ran, "|") != "go test ./internal/p|go build ./...|echo ok|go vet ./...|doctrine-cmd" {
		t.Errorf("ran %v", ran)
	}
	if len(*published) < 3 {
		t.Errorf("published %d", len(*published))
	}
	m, err := s.Matrix(ctx, root, "specs/001-p")
	if err != nil {
		t.Fatal(err)
	}
	// T001 has a red command and no green before: failing.
	if m.Rows[0].ID != "FR-001" || m.Rows[0].Status != api.ProofFailing || m.Rows[0].Tasks[0].Source != SourceRegression {
		t.Errorf("row = %+v", m.Rows[0])
	}
	idx, err := s.Index(ctx, root)
	if err != nil || len(idx.Features) != 1 || idx.Features[0].Dir != "specs/001-p" || idx.Totals.Failing != 2 ||
		len(idx.Runs) != 1 {
		t.Fatalf("index = %+v, %v", idx, err)
	}
	// The same folder spelled differently is the same feature (old bug D12).
	if _, err := s.Matrix(ctx, root, "./specs/001-p/"); err != nil {
		t.Error(err)
	}
	if _, err := s.Matrix(ctx, root, "specs/none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing feature: %v", err)
	}
	if _, err := s.Matrix(ctx, root, "../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("outside feature: %v", err)
	}
}

func TestRunRefusals(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	release := make(chan struct{})
	s, _ := newService(t, root, func(string) CmdResult { <-release; return CmdResult{Exit: code(0)} }, nil)
	ctx := context.Background()
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root, Feature: "specs/001-p"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.StartRun(ctx, api.ProofRunRequest{Cwd: root})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Details["runId"] != run.ID {
		t.Fatalf("second run: %v", err)
	}
	close(release)
	if r := waitRun(t, s, root, run.ID); r.State != api.RunGreen || r.Feature != "specs/001-p" {
		t.Errorf("run = %+v", r)
	}
	s2, _ := newService(t, root, func(string) CmdResult { return CmdResult{Exit: code(0)} }, []string{"t1"})
	_, err = s2.StartRun(ctx, api.ProofRunRequest{Cwd: root})
	if !errors.As(err, &ce) || ce.Details["reason"] != "active_tasks" {
		t.Fatalf("active tasks: %v", err)
	}
	r, err := s2.StartRun(ctx, api.ProofRunRequest{Cwd: root, Force: true})
	if err != nil || !r.Noisy {
		t.Fatalf("forced run = %+v, %v", r, err)
	}
	waitRun(t, s2, root, r.ID)
}

func TestCancelRun(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	started := make(chan struct{}, 10)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(Options{Ledger: NewLedger(st, nil), Git: fakeGit{root: root}, LogDir: t.TempDir(),
		Exec: func(ctx context.Context, _, _, _, _ string) CmdResult {
			started <- struct{}{}
			<-ctx.Done()
			return CmdResult{}
		}})
	t.Cleanup(s.Close)
	ctx := context.Background()
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	r, err := s.CancelRun(ctx, root, run.ID)
	if err != nil || r.State != api.RunCancelled || len(r.Results) != 0 {
		t.Fatalf("cancel = %+v, %v", r, err)
	}
	if _, err := s.RunStatus(ctx, root, "r-none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown run: %v", err)
	}
}

// A run whose record cannot be written is not left registered (old bug D11).
func TestRunNotStuckOnWriteError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := &failingDB{DB: st, fail: true}
	s := New(Options{Ledger: NewLedger(db, nil), Git: fakeGit{root: root}, LogDir: t.TempDir(),
		Exec: func(context.Context, string, string, string, string) CmdResult { return CmdResult{Exit: code(0)} }})
	t.Cleanup(s.Close)
	ctx := context.Background()
	if _, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root}); err == nil {
		t.Fatal("the run started without its record")
	}
	db.fail = false
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root})
	if err != nil {
		t.Fatalf("the project is stuck: %v", err)
	}
	waitRun(t, s, root, run.ID)
}

// gateService is a proof service whose repository has the active task t-gate; holds says whether its gate holds
// a gate slot now.
func gateService(t *testing.T, root string, holds *atomic.Bool,
	exec func(ctx context.Context, gateTask string) CmdResult) *Service {
	t.Helper()
	write(t, filepath.Join(root, "specs", "001-p", "spec.md"), specMD)
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(Options{Ledger: NewLedger(st, nil), Git: fakeGit{root: root}, LogDir: t.TempDir(),
		Active:        func(context.Context, string) ([]string, error) { return []string{"t-gate"}, nil },
		HoldsGateSlot: func(id string) bool { return id == "t-gate" && holds.Load() },
		Exec: func(ctx context.Context, _, _, _, gateTask string) CmdResult {
			return exec(ctx, gateTask)
		}})
	t.Cleanup(s.Close)
	return s
}

// TestRunPassesGateTask: a run asked for from a gate that holds a gate slot does not count the gate's own task as
// active, and the gate's task id reaches every command's Exec, so the daemon lets it share that slot.
func TestRunPassesGateTask(t *testing.T) {
	root := t.TempDir()
	var holds atomic.Bool
	holds.Store(true)
	got := make(chan string, 10)
	s := gateService(t, root, &holds, func(_ context.Context, gateTask string) CmdResult {
		got <- gateTask
		return CmdResult{Exit: code(0)}
	})
	run, err := s.StartRun(context.Background(), api.ProofRunRequest{Cwd: root, GateTask: "t-gate"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Noisy {
		t.Error("the asking gate's own task made the run noisy")
	}
	waitRun(t, s, root, run.ID)
	if n := len(got); n == 0 {
		t.Fatal("no command ran")
	}
	for range len(got) {
		if id := <-got; id != "t-gate" {
			t.Errorf("Exec got gateTask %q", id)
		}
	}
}

// TestGateTaskWithoutSlotConflicts: a gate task id only counts as the asking gate while that gate holds a slot: the
// id of a running task whose gate holds none (or an unknown id) is refused like any other active task.
func TestGateTaskWithoutSlotConflicts(t *testing.T) {
	root := t.TempDir()
	var holds atomic.Bool // the gate holds no slot
	s := gateService(t, root, &holds, func(context.Context, string) CmdResult { return CmdResult{Exit: code(0)} })
	for _, id := range []string{"t-gate", "t-other", ""} {
		var conflict *ConflictError
		if _, err := s.StartRun(context.Background(), api.ProofRunRequest{Cwd: root, GateTask: id}); !errors.As(err,
			&conflict) {
			t.Errorf("gateTask %q: %v, want a conflict for the active task", id, err)
		}
	}
}

// TestRunOutlivesItsGate: a gate starts a run without waiting for it (proof run without --wait) and ends; the run
// is not tied to the gate: it completes and its results are recorded in the ledger.
func TestRunOutlivesItsGate(t *testing.T) {
	root := t.TempDir()
	var holds atomic.Bool
	holds.Store(true)
	gateEnded := make(chan struct{})
	var once sync.Once
	s := gateService(t, root, &holds, func(ctx context.Context, _ string) CmdResult {
		once.Do(func() {
			<-gateEnded // the first command is still running when the gate ends
		})
		if ctx.Err() != nil {
			return CmdResult{}
		}
		return CmdResult{Exit: code(0)}
	})
	ctx := context.Background()
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root, GateTask: "t-gate"})
	if err != nil {
		t.Fatal(err)
	}
	holds.Store(false) // the gate gives its slot back and ends
	close(gateEnded)
	run = waitRun(t, s, root, run.ID)
	if run.State != api.RunGreen || run.Counts.Green != run.Total || run.Total == 0 {
		t.Fatalf("run after its gate ended = %+v", run)
	}
	m, err := s.Matrix(ctx, root, "specs/001-p")
	if err != nil {
		t.Fatal(err)
	}
	recorded := 0
	for _, row := range m.Rows {
		for _, tk := range row.Tasks {
			if tk.Source == SourceRegression {
				recorded++
			}
		}
	}
	if recorded == 0 {
		t.Fatalf("nothing recorded in the ledger: %+v", m.Rows)
	}
}

type failingDB struct {
	DB
	fail bool
}

func (f *failingDB) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	if f.fail {
		return errors.New("disk full")
	}
	return f.DB.Write(ctx, fn)
}

func TestLedgerRecord(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	l := NewLedger(st, func() time.Time { at = at.Add(time.Minute); return at })
	ctx := context.Background()
	f := Feature{Key: "k", Display: "specs/001"}
	for _, state := range []string{api.GateGreen, api.GateRed} {
		if err := l.Record(ctx, "p", f, []Result{{TaskID: "T1", State: state, Source: SourceTask, Ref: "t"}}); err != nil {
			t.Fatal(err)
		}
	}
	e, err := l.Entries(ctx, "p", "k")
	if err != nil || e["T1"].State != api.GateRed || e["T1"].LastGreenAt.IsZero() {
		t.Fatalf("entries = %+v, %v", e, err)
	}
	if fs, err := l.Features(ctx, "p"); err != nil || len(fs) != 1 || fs[0].Display != "specs/001" {
		t.Errorf("features = %v, %v", fs, err)
	}
	for range 2 {
		if err := l.Bump(ctx, "p", CounterLaneRefusals); err != nil {
			t.Fatal(err)
		}
	}
	if c, err := l.Counters(ctx, "p"); err != nil || c[CounterLaneRefusals] != 2 {
		t.Errorf("counters = %v, %v", c, err)
	}
	if err := l.SaveRun(ctx, "p", api.ProofRun{ID: "r1", State: api.RunRunning, StartedAt: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := l.AbandonRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := l.Run(ctx, "p", "r1"); err != nil || r.State != api.RunError {
		t.Errorf("abandoned run = %+v, %v", r, err)
	}
}

// TestStoppedCommandRunsAgain: a command the memory watchdog stopped is no result: it runs again, and only the
// run that ended by itself counts and reaches the ledger.
func TestStoppedCommandRunsAgain(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "spec.md"), specMD)
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	var mu sync.Mutex
	ran := map[string]int{}
	s, _ := newService(t, root, func(cmd string) CmdResult {
		mu.Lock()
		defer mu.Unlock()
		ran[cmd]++
		if cmd == "go test ./internal/p" && ran[cmd] == 1 {
			return CmdResult{Stopped: true, Tail: "stopped: machine low on memory"}
		}
		return CmdResult{Exit: code(0)}
	}, nil)
	ctx := context.Background()
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root, Feature: "specs/001-p"})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, root, run.ID)
	if run.State != api.RunGreen || run.Counts.Green != run.Total || run.Counts.Error != 0 ||
		len(run.Results) != run.Total || ran["go test ./internal/p"] != 2 {
		t.Fatalf("run = %+v, ran %v", run, ran)
	}
	m, err := s.Matrix(ctx, root, "specs/001-p")
	if err != nil {
		t.Fatal(err)
	}
	if r := m.Rows[0]; r.ID != "FR-001" || r.Status != api.ProofProven || r.Tasks[0].Source != SourceRegression {
		t.Fatalf("row = %+v", r)
	}
}

// TestStoppedCommandGivesUp: a command stopped api.GateLowMemoryStopsMax times ends the run as cancelled with the
// reason, records nothing for its spec task and runs nothing after it.
func TestStoppedCommandGivesUp(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "specs", "001-p", "spec.md"), specMD)
	write(t, filepath.Join(root, "specs", "001-p", "tasks.md"), tasksMD)
	var mu sync.Mutex
	ran := map[string]int{}
	s, _ := newService(t, root, func(cmd string) CmdResult {
		mu.Lock()
		defer mu.Unlock()
		ran[cmd]++
		if cmd == "go build ./..." {
			return CmdResult{Stopped: true, Tail: "stopped: machine low on memory"}
		}
		return CmdResult{Exit: code(0)}
	}, nil)
	ctx := context.Background()
	run, err := s.StartRun(ctx, api.ProofRunRequest{Cwd: root, Feature: "specs/001-p"})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, root, run.ID)
	if run.State != api.RunCancelled || run.Counts.Error != 0 || run.Counts.Green != 1 || len(run.Results) != 1 ||
		!strings.Contains(run.Error, "out of memory") {
		t.Fatalf("run = %+v", run)
	}
	if ran["go build ./..."] != api.GateLowMemoryStopsMax || ran["echo ok"] != 0 || ran["go vet ./..."] != 0 {
		t.Fatalf("ran %v", ran)
	}
	m, err := s.Matrix(ctx, root, "specs/001-p")
	if err != nil {
		t.Fatal(err)
	}
	if r := m.Rows[0]; r.ID != "FR-001" || r.Status == api.ProofFailing || r.Tasks[0].Source == SourceRegression {
		t.Fatalf("T001 was recorded: %+v", r)
	}
}
