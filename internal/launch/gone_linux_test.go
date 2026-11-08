package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// removeRunningLaunch starts a worker with a child, removes the launch directory while both run (Linux allows
// that: the launcher keeps its lock on the unlinked file), and returns the handle with the launcher, worker and
// child process ids.
func removeRunningLaunch(t *testing.T, env ...string) (h *Handle, pids []int) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "a1")
	out := t.TempDir()
	opts := testOptions(dir, "pid:"+filepath.Join(out, "worker"), "spawn:"+filepath.Join(out, "child"), "sleep:60000")
	opts.Env = append(opts.Env, env...)
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r, _ := h.Ready()
	pids = []int{r.LauncherPID, readPid(t, filepath.Join(out, "worker")), readPid(t, filepath.Join(out, "child"))}
	starts := make([]uint64, len(pids))
	for i, p := range pids {
		starts[i], _ = platform.ProcessStartTime(p)
	}
	t.Cleanup(func() { // after a failure: never leave the test's processes running
		for i, p := range pids {
			if platform.ProcessRunning(p, starts[i]) {
				_ = forceStop(p)
			}
		}
	})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range pids {
		if !platform.ProcessAlive(p) {
			t.Fatalf("process %d ended before the test looked", p)
		}
	}
	return h, pids
}

// waitGone waits for the handle to report ErrGone and checks that, by then, the launcher and the whole worker tree
// have ended: a caller that frees the task's lane on that error never leaves a worker writing (M18 review N2).
func waitGone(t *testing.T, h *Handle, pids []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := h.Wait(ctx); !errors.Is(err, ErrGone) {
		t.Fatalf("Wait = %v, want ErrGone", err)
	}
	for _, p := range pids {
		if platform.ProcessAlive(p) {
			t.Errorf("process %d still runs after Wait reported the launch gone", p)
		}
	}
}

// A launch directory removed while the worker runs: the launcher is asked to stop (SIGTERM, which `seelegent
// __launch` turns into stopping the worker tree), and Wait reports ErrGone only once the tree has ended.
func TestRemovedLaunchDirStopsTheWorkerTree(t *testing.T) {
	h, pids := removeRunningLaunch(t)
	waitGone(t, h, pids)
}

// A launcher that dies on SIGTERM without stopping its tree: the processes the handle listed while the launcher
// ran are killed before Wait reports ErrGone.
func TestRemovedLaunchDirKillsTheTreeOfALauncherThatDies(t *testing.T) {
	h, pids := removeRunningLaunch(t, envPlainLauncher+"=1")
	waitGone(t, h, pids)
}

// A launcher known only from a record that did not check out is never signaled: while the process the record
// names runs, the launch counts as running, and nothing is reported gone.
func TestRemovedLaunchDirNeverSignalsAnUncheckedRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "launch")
	f := FilesIn(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()
	start, err := platform.ProcessStartTime(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(f.Spec, specFile{Job: "a"}); err != nil {
		t.Fatal(err)
	}
	// The job names differ, so the record does not check out although it names a live process.
	if err := writeJSON(f.Ready, Ready{LauncherPID: self, LauncherStart: start, Job: "b"}); err != nil {
		t.Fatal(err)
	}
	lock, err := fsutil.TryLock(f.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	h, err := Attach(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ended, err := h.Exited(); ended || err != nil {
		t.Fatalf("Exited = %v, %v while the launcher holds the lock", ended, err)
	}
	if tree, _ := h.Tree(); tree.RootPID != 0 {
		t.Fatalf("the unchecked record was kept as the tree: %+v", tree)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * livenessInterval)
	for range 5 {
		if _, ended, err := h.Exited(); ended || err != nil {
			t.Fatalf("Exited = %v, %v while the recorded process runs", ended, err)
		}
		time.Sleep(pollInterval)
	}
}
