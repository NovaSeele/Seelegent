package task

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// workerEvents returns the worker events of a task by kind.
func workerEvents(t *testing.T, evs []api.TaskEvent) map[event.Kind]int {
	t.Helper()
	out := map[event.Kind]int{}
	for _, e := range eventsOf(evs, api.EventWorker) {
		var ev event.Event
		decode(t, e, &ev)
		out[ev.Kind]++
	}
	return out
}

// TestRestartReattaches stops the daemon while a worker runs; the next daemon attaches to the same worker, counts
// it on its account once and logs every line once (old B3).
func TestRestartReattaches(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	release := filepath.Join(h.dir, "release")
	h.queue.Push(t, script(agyInit("conv-r"), agyDelta(1, "Before the restart."), agyTool(2, "CALLING", "go build"),
		"#!wait-file "+release, agyTool(2, "DONE", "go build"), agyDelta(3, "After."), agyResult("conv-r", "Survived.")))
	sum := h.delegate(api.DelegateRequest{})
	h.waitFor("the tool call in the log", func() bool { return workerEvents(t, h.events(sum.ID))[event.KindToolCall] > 0 })
	h.stop()
	if tk := h.task(sum.ID); tk.Status != StatusRunning {
		t.Fatalf("closing the engine changed the task: %s", tk.Status)
	}

	h.start()
	h.waitEvent(sum.ID, api.EventReattach)
	h.waitFor("the reattached worker", func() bool { return len(h.s.LiveWorkers()) == 1 })

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	if tk.LastTurn().Response != "Survived." || len(tk.LastTurn().Attempts) != 1 {
		t.Fatalf("turn = %+v", tk.LastTurn())
	}
	kinds := workerEvents(t, h.events(sum.ID))
	if kinds[event.KindSession] != 1 || kinds[event.KindToolCall] != 1 || kinds[event.KindMessage] != 2 {
		t.Fatalf("worker events = %v", kinds)
	}
	if len(h.queue.Records(t)) != 1 {
		t.Fatal("the worker was started again")
	}
}

// TestRestartAfterWorkerEnded replays a worker that finished while no daemon ran.
func TestRestartAfterWorkerEnded(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	release := filepath.Join(h.dir, "release")
	h.queue.Push(t, script(agyInit("conv-e"), agyDelta(1, "Half."), agyTool(2, "CALLING", "go vet"),
		"#!wait-file "+release, agyTool(2, "DONE", "go vet"), agyResult("conv-e", "Finished alone.")))
	sum := h.delegate(api.DelegateRequest{})
	h.waitFor("the tool call in the log", func() bool { return workerEvents(t, h.events(sum.ID))[event.KindToolCall] > 0 })
	dir := h.task(sum.ID).LastTurn().Attempts[0].LaunchDir
	h.stop()
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hd, err := launch.Attach(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	if _, err := hd.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	h.start()
	tk := h.waitStatus(sum.ID, StatusDone)
	evs := h.events(sum.ID)
	if len(eventsOf(evs, api.EventResumed)) != 1 || len(eventsOf(evs, api.EventReattach)) != 0 {
		t.Fatalf("events = %v", types(evs))
	}
	if tk.LastTurn().Response != "Finished alone." {
		t.Fatalf("response = %q", tk.LastTurn().Response)
	}
	if kinds := workerEvents(t, evs); kinds[event.KindSession] != 1 || kinds[event.KindToolCall] != 1 {
		t.Fatalf("worker events = %v", kinds)
	}
}

// TestRestartInterruptedAttempt closes an attempt whose worker never started and runs a new one with the full
// brief that says the task was interrupted.
func TestRestartInterruptedAttempt(t *testing.T) {
	h := newHarness(t)
	acc := h.agy("only", 0)
	h.stop()
	tk := &Task{ID: "t-interrupted", Title: "Interrupted", Status: StatusRunning, Kind: KindNormal, Mode: ModeEdit,
		Cwd: h.cwd, ProjectKey: "project-1", ProjectName: "project", Prompt: "Resume me.", Backend: "agy",
		Model: "gemini-3.8-flash-medium", CreatedAt: h.clk.Now(), UpdatedAt: h.clk.Now(),
		Turns: []*Turn{{N: 1, By: ByLead, Message: "Resume me.", Status: TurnRunning, Model: "gemini-3.8-flash-medium",
			StartedAt: h.clk.Now(), Attempts: []*Attempt{{N: 1, AccountID: acc.ID, Backend: "agy",
				Model: "gemini-3.8-flash-medium", StartedAt: h.clk.Now(), Status: AttemptRunning,
				LaunchDir: filepath.Join(h.dir, "never-started")}}}},
	}
	if _, err := h.s.repo.Create(t.Context(), tk, nil); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-i", "Resumed."))
	h.start()
	got := h.waitStatus(tk.ID, StatusDone)
	at := got.LastTurn().Attempts
	if len(at) != 2 || at[0].Status != AttemptTransient || !strings.Contains(at[0].Error, "interrupted") ||
		at[1].Status != AttemptOK {
		t.Fatalf("attempts = %+v %+v", at[0], at[len(at)-1])
	}
	if len(eventsOf(h.events(tk.ID), api.EventRetry)) != 0 {
		t.Error("an interrupted attempt waited for a retry delay")
	}
}

// TestTakeoverAndHandback stops nothing on a finished task, opens a console on the task's own conversation and
// account, and hands the task back to the worker when the console ends (old B4, B5).
func TestTakeoverAndHandback(t *testing.T) {
	h := newHarness(t)
	acc := h.agy("first", 10)
	h.agy("second", 0)
	h.queue.Push(t, okScript("conv-k", "First turn."))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)

	ts, err := h.s.Takeover(t.Context(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ts.Manual || ts.Status != StatusRunning {
		t.Fatalf("takeover summary = %+v", ts)
	}
	specs := h.consoles.opened()
	if len(specs) != 1 || !slices.Contains(specs[0].Argv, "conv-k") || !samePath(specs[0].Dir, h.cwd) ||
		specs[0].Name != "console" || len(specs[0].Env) == 0 {
		t.Fatalf("console = %+v", specs)
	}
	if _, err := h.s.Takeover(t.Context(), sum.ID); !errors.Is(err, ErrBadState) {
		t.Errorf("second takeover: %v", err)
	}
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "x"}); !errors.Is(err, ErrBadState) {
		t.Errorf("followup during takeover: %v", err)
	}
	if _, err := h.s.Review(t.Context(), sum.ID, api.ReviewRequest{Verdict: api.VerdictAccepted}); !errors.Is(err,
		ErrBadState) {
		t.Errorf("review during takeover: %v", err)
	}

	h.queue.Push(t, okScript("conv-k", "The user renamed the handler."))
	h.consoles.exit(0, 0)
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if len(tk.Turns) != 2 || !tu.Handback || tu.HandbackOf != "The user renamed the handler." || tu.By != ByUser ||
		tu.Attempts[0].AccountID != acc.ID || !tu.Attempts[0].Continuing || tk.Manual || tk.Console != nil {
		t.Fatalf("handback turn = %+v", tu)
	}
	evs := h.events(sum.ID)
	if len(eventsOf(evs, api.EventTakeover)) != 2 || len(eventsOf(evs, api.EventHandback)) != 1 {
		t.Fatalf("events = %v", types(evs))
	}
	var end api.TakeoverData
	decode(t, eventsOf(evs, api.EventTakeover)[1], &end)
	if end.State != "end" || end.ExitCode == nil || *end.ExitCode != 0 {
		t.Fatalf("takeover end = %+v", end)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil || !rep.Handback {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	d, err := h.s.Get(t.Context(), sum.ID)
	if err != nil || d.Turns[1].Handback != "The user renamed the handler." {
		t.Fatalf("detail handback = %q, %v", d.Turns[1].Handback, err)
	}
}

// TestTakeoverStopsRunningWorker stops the worker before the console opens; cancelling during the takeover ends
// the task without a hand-back.
func TestTakeoverStopsRunningWorker(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, hangScript("conv-h"))
	sum := h.delegate(api.DelegateRequest{})
	h.runningWorker(sum.ID)
	if _, err := h.s.Takeover(t.Context(), sum.ID); err != nil {
		t.Fatal(err)
	}
	tk := h.task(sum.ID)
	if !tk.Manual || len(h.s.LiveWorkers()) != 0 || tk.LastTurn().Status != TurnCancelled {
		t.Fatalf("task = manual %v, workers %v, turn %s", tk.Manual, h.s.LiveWorkers(), tk.LastTurn().Status)
	}
	cs, stopped, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{})
	if err != nil || !stopped || cs.Status != StatusCancelled || cs.Manual {
		t.Fatalf("cancel = %+v %v %v", cs, stopped, err)
	}
	// The console is still open: no followup may run beside it.
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "go on"}); !errors.Is(err, ErrBadState) {
		t.Errorf("followup with the console open: %v", err)
	}
	h.consoles.exit(0, 0)
	h.waitFor("the console watch to end", func() bool { return !h.s.consoleOpen(sum.ID) })
	if tk := h.task(sum.ID); tk.Status != StatusCancelled || len(tk.Turns) != 1 {
		t.Fatalf("handed back after a cancel: %s, %d turns", tk.Status, len(tk.Turns))
	}
}

// TestTakeoverRefusals covers a takeover without consoles, without a conversation and with a console that cannot
// open (the task goes back to how it was).
func TestTakeoverRefusals(t *testing.T) {
	h := newHarness(t)
	sum := h.delegate(api.DelegateRequest{}) // no account: it never gets a conversation
	if _, err := h.s.Takeover(t.Context(), sum.ID); !errors.Is(err, ErrBadState) {
		t.Errorf("without a conversation: %v", err)
	}
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-x", "ok"))
	done := h.delegate(api.DelegateRequest{})
	h.waitStatus(done.ID, StatusDone)
	h.consoles.mu.Lock()
	h.consoles.fail = errors.New("no terminal")
	h.consoles.mu.Unlock()
	if _, err := h.s.Takeover(t.Context(), done.ID); !errors.Is(err, ErrUnavailable) {
		t.Errorf("console fails: %v", err)
	}
	if tk := h.task(done.ID); tk.Status != StatusDone || tk.Manual || tk.Console != nil {
		t.Fatalf("not reverted: %s manual=%v", tk.Status, tk.Manual)
	}
	h.s.o.Consoles = nil
	if _, err := h.s.Takeover(t.Context(), done.ID); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no consoles: %v", err)
	}
}

// TestRestartDuringTakeover watches a console again after a restart through its exit file.
func TestRestartDuringTakeover(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-z", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	if _, err := h.s.Takeover(t.Context(), sum.ID); err != nil {
		t.Fatal(err)
	}
	h.stop()
	tk := h.task(sum.ID)
	if !tk.Manual || tk.Console == nil || tk.Console.ExitFile == "" {
		t.Fatalf("console = %+v", tk.Console)
	}
	if err := os.WriteFile(tk.Console.ExitFile, []byte("3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-z", "Handed back."))
	h.start()
	tk = h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || tk.LastTurn().HandbackOf != "Handed back." {
		t.Fatalf("turns = %+v", tk.LastTurn())
	}
	var end api.TakeoverData
	decode(t, eventsOf(h.events(sum.ID), api.EventTakeover)[1], &end)
	if end.ExitCode == nil || *end.ExitCode != 3 {
		t.Fatalf("takeover end = %+v", end)
	}
}

// TestRestartHonorsCancel stops a worker whose cancel the earlier daemon recorded but could not finish.
func TestRestartHonorsCancel(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, hangScript("conv-x"))
	sum := h.delegate(api.DelegateRequest{})
	h.runningWorker(sum.ID)
	h.stop()
	tk := h.task(sum.ID)
	tk.CancelRequested = true
	if _, err := h.s.repo.Save(t.Context(), tk, nil, nil); err != nil {
		t.Fatal(err)
	}
	h.start()
	tk = h.waitStatus(sum.ID, StatusCancelled)
	if tk.LastTurn().Status != TurnCancelled || tk.CancelRequested {
		t.Fatalf("turn = %s, cancelRequested = %v", tk.LastTurn().Status, tk.CancelRequested)
	}
	hd, err := launch.Attach(tk.LastTurn().Attempts[0].LaunchDir)
	if err != nil {
		t.Fatal(err)
	}
	if exit, ended, err := hd.Exited(); err != nil || !ended || exit.Reason != launch.ReasonCanceled {
		t.Fatalf("worker exit = %+v %v %v", exit, ended, err)
	}
}

// TestTakeoverWindowClosed ends a takeover whose console process is gone without an exit code (a closed window):
// the task is handed back with exit code 129 instead of staying taken over forever.
func TestTakeoverWindowClosed(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-w", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	if _, err := h.s.Takeover(t.Context(), sum.ID); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the console process", func() bool { c := h.task(sum.ID).Console; return c != nil && c.Process != nil })
	h.queue.Push(t, okScript("conv-w", "Handed back."))
	h.consoles.close(0)
	h.waitFor("the hand-back", func() bool {
		h.clk.Advance(consolePoll)
		return !h.task(sum.ID).Manual
	})
	tk := h.waitStatus(sum.ID, StatusDone)
	var end api.TakeoverData
	decode(t, eventsOf(h.events(sum.ID), api.EventTakeover)[1], &end)
	if end.ExitCode == nil || *end.ExitCode != consoleExitClosed || tk.LastTurn().HandbackOf != "Handed back." {
		t.Fatalf("takeover end = %+v, turn = %+v", end, tk.LastTurn())
	}
}

// TestRestartConsoleGone ends, after a restart, a takeover whose console process ended while no daemon ran and left
// no exit code.
func TestRestartConsoleGone(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-g", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	if _, err := h.s.Takeover(t.Context(), sum.ID); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the console process", func() bool { c := h.task(sum.ID).Console; return c != nil && c.Process != nil })
	h.stop()
	h.consoles.close(0)
	h.queue.Push(t, okScript("conv-g", "Back after the restart."))
	h.start()
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || tk.LastTurn().HandbackOf != "Back after the restart." {
		t.Fatalf("turns = %+v", tk.LastTurn())
	}
}

// TestRecoverySkipsUnreadable recovers the other tasks when one active task cannot be read, and lists it for doctor.
func TestRecoverySkipsUnreadable(t *testing.T) {
	h := newHarness(t)
	a := h.delegate(api.DelegateRequest{Prompt: "Task A"})
	b := h.delegate(api.DelegateRequest{Prompt: "Task B"})
	h.stop()
	if err := h.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE turns SET data = 'nope' WHERE task_id = ?`, a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.start()
	if got := h.s.Unreadable(); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("unreadable = %v", got)
	}
	h.queue.Push(t, okScript("conv-b", "b done"))
	h.agy("only", 0)
	h.waitFor("task B", func() bool {
		h.clk.Advance(noAccountRecheck)
		return h.task(b.ID).Status == StatusDone
	})
}
