package task

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// Field report 4: a turn's start snapshot and the task's baseline are taken right before the turn's first launch,
// and a turn no worker ever ran in has no post-turn checks.

// A task waits for an account while someone else changes a file that already had uncommitted work and leaves a
// junk file; then it is cancelled. No worker ran, so nothing is at risk, there are no hygiene findings, no restore
// is offered and the turn changed no files.
func TestNeverLaunchedTurnHasNoChecks(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.write("a.txt", "one\nmine\n")
	sum := h.delegate(api.DelegateRequest{Title: "never ran"})
	h.waitFor("waiting_quota", func() bool { return h.task(sum.ID).Status == StatusWaitingQuota })
	h.write("a.txt", "one\n")
	h.write("x.orig", "leftover\n")
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusCancelled)
	tu := tk.LastTurn()
	if len(tu.Attempts) != 0 || len(tu.WorkAtRisk) != 0 || len(tu.Hygiene) != 0 || len(tu.Changed) != 0 ||
		len(tu.Files) != 0 || tk.BaselineTaken {
		t.Fatalf("turn: attempts %d, risk %+v, hygiene %+v, changed %v, files %v, baseline %v", len(tu.Attempts),
			tu.WorkAtRisk, tu.Hygiene, tu.Changed, tu.Files, tk.BaselineTaken)
	}
	if len(eventsOf(h.events(sum.ID), api.EventWorkAtRisk)) != 0 {
		t.Error("a work_at_risk event for a turn no worker ran in")
	}
	if a := tk.Attention; a != nil && a.Reason == ReasonWorkAtRisk {
		t.Errorf("attention = %+v", a)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.WorkAtRisk != nil || len(rep.Hygiene) != 0 || len(rep.FilesChanged) != 0 {
		t.Fatalf("report: risk %+v, hygiene %+v, files %v", rep.WorkAtRisk, rep.Hygiene, rep.FilesChanged)
	}
}

// workerRan: an attempt whose launch never got the worker running (no ready record, no outcome) does not count; the
// launcher's ready record, or an outcome with a conversation, an edit or a command, does.
func TestWorkerRan(t *testing.T) {
	dir := t.TempDir()
	tu := &Turn{N: 1, Attempts: []*Attempt{{N: 1, LaunchDir: dir}}}
	if workerRan(tu, nil) || workerRan(&Turn{N: 1}, &backend.Outcome{}) {
		t.Fatal("a launch that never started counts as a worker run")
	}
	for _, o := range []*backend.Outcome{{ConversationID: "c"}, {FilesEdited: []string{"a"}},
		{Commands: []string{"ls"}}} {
		if !workerRan(&Turn{N: 1}, o) || !workerRan(&Turn{N: 1, Attempts: []*Attempt{{N: 1, Outcome: o}}}, nil) {
			t.Errorf("outcome %+v does not count", o)
		}
	}
	if err := os.WriteFile(launch.FilesIn(dir).Ready, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !workerRan(tu, nil) {
		t.Fatal("the ready record does not count")
	}
}

// A task with a lane waits for an account while someone else changes a committed file and adds one, both outside
// the lane. Then the worker runs and edits a file in its lane. The turn's start snapshot and the baseline are taken
// at the launch, after the wait: the outside changes are neither the turn's changes nor in the lane check, and the
// baseline lists the changed file as work that was already there.
func TestStartSnapshotAtFirstLaunch(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	sum := h.delegate(api.DelegateRequest{Title: "waited", Lane: []string{"src/x"}})
	h.waitFor("waiting_quota", func() bool { return h.task(sum.ID).Status == StatusWaitingQuota })
	if tk := h.task(sum.ID); tk.LastTurn().Snap != nil || tk.BaselineTaken {
		t.Fatalf("snapshot or baseline taken before the launch: %+v, %v", tk.LastTurn().Snap, tk.BaselineTaken)
	}
	h.clk.Advance(noAccountRecheck)
	h.write("b.txt", "outside\n")
	h.write("src/y.ts", "outside\n")
	in := filepath.Join(h.cwd, "src", "x", "a.vue")
	h.queue.Push(t, editScript("conv-w", in, "b\n", "Changed a.vue."))
	h.agy("late", 0)
	h.waitFor("the task to finish", func() bool {
		h.clk.Advance(noAccountRecheck)
		return h.task(sum.ID).Status == StatusDone
	})
	tk := h.task(sum.ID)
	tu := tk.LastTurn()
	if !slices.Equal(tu.Changed, []string{"src/x/a.vue"}) || len(tu.WorkAtRisk) != 0 {
		t.Fatalf("changed %v, risk %+v", tu.Changed, tu.WorkAtRisk)
	}
	if st := h.laneOf(tk, 1); st.State != api.StepGreen || len(st.Files) != 0 || len(st.Unseen) != 0 {
		t.Fatalf("lane = %+v", st)
	}
	if !slices.ContainsFunc(tk.Baseline, func(p string) bool { return filepath.Base(p) == "b.txt" }) {
		t.Fatalf("baseline %v does not hold b.txt", tk.Baseline)
	}
}
