package gate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/testutil/priority"
	"github.com/NovaSeele/Seelegent/internal/testutil/raceenv"
)

// roleEnv makes the test binary a helper: "priority" writes its own priority (priority.Of) to the file named last.
const roleEnv = "GATE_TEST_ROLE"

func TestMain(m *testing.M) {
	raceenv.QuietExit()
	if os.Getenv(roleEnv) == "priority" {
		v, err := priority.Of(os.Getpid())
		if err != nil {
			os.Exit(4)
		}
		if err := os.WriteFile(os.Args[len(os.Args)-1], []byte(strconv.Itoa(v)), 0o600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// quoted quotes a path for the platform's shell.
func quoted(p string) string {
	if runtime.GOOS == "windows" {
		return `"` + p + `"`
	}
	return "'" + p + "'"
}

// TestRunnerPriority: with Runner.Tree low a gate command and what it starts (here the test binary, a grandchild of
// the tree) run at low priority; without it at the caller's.
func TestRunnerPriority(t *testing.T) {
	priority.AtNormal(t)
	self := priority.MustOf(t, os.Getpid())
	if priority.IsLow(self) {
		t.Skip("the test already runs at low priority")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		tree platform.TreeOptions
		low  bool
	}{
		{"low", platform.TreeOptions{Priority: platform.PriorityLow}, true},
		{"zero", platform.TreeOptions{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mark := filepath.Join(dir, "priority")
			steps := []api.GateStep{{Kind: api.StepTask, Label: "p", Cmd: quoted(exe) + " " + quoted(mark),
				State: api.StepPending}}
			r := Runner{Root: dir, Env: append(os.Environ(), roleEnv+"=priority"), Tree: tc.tree}
			res, ok := r.Run(context.Background(), steps, nil)
			if !ok || res.State != api.GateGreen {
				t.Fatalf("run: ok %v, %+v", ok, res)
			}
			data, err := os.ReadFile(mark)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := strconv.Atoi(string(data))
			if tc.low && !priority.IsLow(got) || !tc.low && got != self {
				t.Fatalf("command priority %#x (this test %#x), low wanted: %v", got, self, tc.low)
			}
		})
	}
}

// TestExecLowMemory: a ctx cancelled with cause ErrLowMemory stops the command's tree as a cancel does, but the
// result says Stopped, not Cancelled; any other cause is still a cancel.
func TestExecLowMemory(t *testing.T) {
	dir := t.TempDir()
	o := ExecOptions{Dir: dir, Env: os.Environ()}
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel(ErrLowMemory) }()
	began := time.Now()
	r := Exec(ctx, sleepCmd(8), o)
	if r.Stopped != api.StepStoppedLowMemory || r.Cancelled || r.Exit != nil || r.TimedOut ||
		r.Err != ErrLowMemory.Error() || time.Since(began) > 6*time.Second {
		t.Fatalf("stopped: %+v after %s", r, time.Since(began))
	}
	// Already stopped before the start: nothing runs.
	if r := Exec(ctx, "echo never", o); r.Stopped != api.StepStoppedLowMemory || r.Cancelled || r.Exit != nil {
		t.Fatalf("stopped before the start: %+v", r)
	}
	other, cancelOther := context.WithCancelCause(context.Background())
	cancelOther(errors.New("shutdown"))
	if r := Exec(other, "echo never", o); !r.Cancelled || r.Stopped != "" {
		t.Fatalf("another cause: %+v", r)
	}
}

// TestRunnerStopsOnLowMemory: a run stopped by the memory watchdog ends at the step it was at (Stopped, state
// error), skips the rest and returns ok = true; a plain cancel still returns ok = false.
func TestRunnerStopsOnLowMemory(t *testing.T) {
	dir := t.TempDir()
	steps := []api.GateStep{
		{Kind: api.StepTask, Label: "one", Cmd: "echo one", State: api.StepPending},
		{Kind: api.StepTask, Label: "two", Cmd: sleepCmd(8), State: api.StepPending},
		{Kind: api.StepTask, Label: "three", Cmd: "echo three", State: api.StepPending},
	}
	r := Runner{Root: dir, Env: os.Environ()}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var seen []string
	res, ok := r.Run(ctx, steps, func(i int, s api.GateStep) {
		seen = append(seen, s.Label+":"+s.State+":"+s.Stopped)
		if i == 1 && s.State == api.StepRunning {
			go func() { time.Sleep(300 * time.Millisecond); cancel(ErrLowMemory) }()
		}
	})
	if !ok {
		t.Fatalf("a watchdog stop must return ok: %+v", res)
	}
	got := make([]string, len(res.Steps))
	for i, s := range res.Steps {
		got[i] = s.Label + ":" + s.State + ":" + s.Stopped
	}
	want := []string{"one:green:", "two:error:low_memory", "three:not_run:"}
	if !slices.Equal(got, want) || !slices.Equal(res.NotRun, []string{"three"}) || res.State != api.GateError ||
		StoppedAt(res) != 1 {
		t.Fatalf("steps %v (want %v), notRun %v, state %s, stopped at %d", got, want, res.NotRun, res.State,
			StoppedAt(res))
	}
	if last := seen[len(seen)-1]; last != "two:error:low_memory" {
		t.Fatalf("progress %v: the stopped step must be reported", seen)
	}

	// Stopped before any step started: the first pending step is the stopped one.
	res, ok = r.Run(ctx, steps, nil)
	if !ok || StoppedAt(res) != 0 || res.Steps[0].Tail != ErrLowMemory.Error() ||
		res.Steps[1].State != api.StepNotRun || res.Steps[2].State != api.StepNotRun {
		t.Fatalf("stopped before the start: ok %v, %+v", ok, res)
	}

	// A plain cancel is still a run cut short.
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if res, ok := r.Run(cctx, steps, nil); ok || StoppedAt(res) != -1 {
		t.Fatalf("cancel: ok %v, %+v", ok, res)
	}
	if !strings.Contains(ErrLowMemory.Error(), "low on memory") {
		t.Fatalf("reason %q", ErrLowMemory)
	}
}
