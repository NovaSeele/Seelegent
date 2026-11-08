package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestTreePriority: a low-priority tree runs its root and its grandchildren at low priority (Windows: the job's
// BELOW_NORMAL priority class; Unix: nice 10 through `nice`), and a normal or zero one at the caller's.
func TestTreePriority(t *testing.T) {
	atNormalPriority(t)
	self := priorityOf(t, os.Getpid())
	if isLow(self) {
		t.Skip("the test already runs at low priority")
	}
	for _, tc := range []struct {
		name string
		opt  TreeOptions
		low  bool
	}{
		{"low", TreeOptions{Priority: PriorityLow}, true},
		{"normal", TreeOptions{Priority: PriorityNormal}, false},
		{"zero", TreeOptions{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			tree, err := StartTreeWith(helper("parent", pidFile), tc.opt)
			if err != nil {
				t.Fatalf("StartTreeWith: %v", err)
			}
			defer func() { _ = tree.Close() }()
			pids := readPids(t, pidFile)
			if len(pids) != 2 || pids[0] != tree.Pid() {
				t.Fatalf("pids = %v, root %d", pids, tree.Pid())
			}
			for _, p := range pids {
				got := priorityOf(t, p)
				if tc.low && !isLow(got) || !tc.low && got != self {
					t.Errorf("process %d: priority %#x (this test %#x), low wanted: %v", p, got, self, tc.low)
				}
			}
			if err := tree.Close(); err != nil {
				t.Fatal(err)
			}
			_ = tree.Wait() // reap the root: a Unix zombie still counts as alive
			waitDead(t, pids...)
		})
	}
}

// TestWorkerTreePriority: a low-priority worker tree runs its root and grandchildren at low priority, the way the
// launcher starts a worker. On Windows the owner itself keeps its priority.
func TestWorkerTreePriority(t *testing.T) {
	atNormalPriority(t)
	self := priorityOf(t, os.Getpid())
	if isLow(self) {
		t.Skip("the test already runs at low priority")
	}
	pidFile := filepath.Join(t.TempDir(), "pids")
	owner := helper("low-owner", pidFile)
	HideWindow(owner)
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- owner.Wait() }()
	release := func() { _ = os.WriteFile(pidFile+".done", nil, 0o600) }
	defer release()
	pids := readPids(t, pidFile)
	if len(pids) != 3 || pids[0] != owner.Process.Pid {
		t.Fatalf("pids = %v, owner %d", pids, owner.Process.Pid)
	}
	for _, p := range pids[1:] {
		if got := priorityOf(t, p); !isLow(got) {
			t.Errorf("worker tree process %d: priority %#x, want low", p, got)
		}
	}
	if runtime.GOOS == "windows" {
		if got := priorityOf(t, pids[0]); got != self {
			t.Errorf("owner: priority %#x, want %#x (only the job is lowered)", got, self)
		}
	}
	release()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("owner: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = owner.Process.Kill()
		t.Fatal("owner did not exit")
	}
	waitDead(t, pids[1:]...)
}
