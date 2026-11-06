package platform

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

// runOSRole runs the Windows-only helper roles; there are none.
func runOSRole(string) bool { return false }

// startDescribedTree starts cmd as a named worker tree (Windows needs the job name to list it from elsewhere).
func startDescribedTree(t *testing.T, cmd *exec.Cmd) (*Tree, *WorkerTree) {
	t.Helper()
	name, err := NewTreeName()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := StartWorkerTree(cmd, name)
	if err != nil {
		t.Fatalf("StartWorkerTree: %v", err)
	}
	wt := tree.WorkerTree("task-1")
	if wt.Job != name {
		t.Fatalf("job %q, want %q", wt.Job, name)
	}
	return tree, wt
}

// TestNamedJob: the job is listed through its name from a separate handle, and a name that exists is refused.
func TestNamedJob(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	tree, wt := startDescribedTree(t, helper("parent", pidFile))
	defer func() { _ = tree.Close() }()
	pids := readPids(t, pidFile)
	listed, err := (&WorkerTree{ID: "again", Job: wt.Job}).PIDs()
	if err != nil {
		t.Fatalf("PIDs: %v", err)
	}
	for _, p := range pids {
		if !slices.Contains(listed, p) {
			t.Fatalf("job lists %v, missing %d", listed, p)
		}
	}

	// The same name again: refused, and the command is not started.
	second := helper("sleeper", filepath.Join(t.TempDir(), "never"))
	if _, err := StartWorkerTree(second, wt.Job); !errors.Is(err, ErrTreeNameTaken) {
		t.Fatalf("second tree with the same name: %v", err)
	}
	if second.Process != nil {
		t.Fatal("the command of a refused tree must not start")
	}

	// A name somebody created first.
	name, _ := NewTreeName()
	p, _ := windows.UTF16PtrFromString(name)
	squatter, err := windows.CreateJobObject(nil, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(squatter) }()
	if _, err := StartWorkerTree(helper("sleeper", filepath.Join(t.TempDir(), "never")), name); !errors.Is(err, ErrTreeNameTaken) {
		t.Fatalf("pre-created name: %v", err)
	}

	if err := tree.Close(); err != nil {
		t.Fatal(err)
	}
	waitDead(t, pids...)
	if left, err := wt.PIDs(); err != nil || len(left) != 0 {
		t.Fatalf("closed job still listed: %v, %v", left, err)
	}
	// The name is free again once the job is gone.
	again, err := StartWorkerTree(helper("print"), wt.Job)
	if err != nil {
		t.Fatalf("reuse a freed name: %v", err)
	}
	_ = again.Wait()
	if code := again.ExitCode(); code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	_ = again.Close()
}
