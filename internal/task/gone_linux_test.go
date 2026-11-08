package task

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// A launch directory removed mid-turn (Linux lets it go while the launcher runs): the task keeps its lane until
// the launcher and the worker tree have ended, so the next task on the same files never runs beside a worker that
// is still writing (M18 review N2).
func TestRemovedLaunchDirEndsTheWorkerBeforeTheLaneIsFree(t *testing.T) {
	h := newHarness(t)
	if err := mkdir(filepath.Join(h.cwd, "src")); err != nil {
		t.Fatal(err)
	}
	h.initRepo(map[string]string{"src/x.go": "package x\n"})
	h.agy("only", 0)
	h.queue.Push(t, hangScript("conv-g"))
	sum := h.delegate(api.DelegateRequest{Title: "A", Lane: []string{"src"}})
	h.runningWorker(sum.ID)
	trees := h.s.WorkerTrees()
	if len(trees) != 1 {
		t.Fatalf("worker trees = %+v", trees)
	}
	pids, err := trees[0].PIDs()
	if err != nil || len(pids) < 2 { // the launcher and the worker
		t.Fatalf("worker tree pids = %v, %v", pids, err)
	}
	starts := make([]uint64, len(pids))
	for i, p := range pids {
		starts[i], _ = platform.ProcessStartTime(p)
	}
	alive := func() []int {
		var out []int
		for i, p := range pids {
			if platform.ProcessRunning(p, starts[i]) {
				out = append(out, p)
			}
		}
		return out
	}
	t.Cleanup(func() { // after a failure: never leave the test's processes running
		for _, p := range alive() {
			_ = platform.KillTree(t.Context(), p)
		}
	})
	claimed := func() bool {
		view, err := h.s.Lanes(t.Context(), h.cwd)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(view.Claims, func(c api.LaneClaim) bool { return c.TaskID == sum.ID })
	}
	if !claimed() {
		t.Fatal("the running task holds no lane")
	}

	if err := os.RemoveAll(h.task(sum.ID).LastTurn().Attempts[0].LaunchDir); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(waitLimit)
	for {
		// The claim first, then the processes: a claim gone while a process still runs is the bug.
		held := claimed()
		left := alive()
		if !held && len(left) > 0 {
			t.Fatalf("the lane was freed while %v still run (task %s)", left, h.task(sum.ID).Status)
		}
		if !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task still %s with %v running", h.task(sum.ID).Status, left)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tk := h.task(sum.ID); Pending(tk.Status) {
		t.Fatalf("task = %s", tk.Status)
	}
}
