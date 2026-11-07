package platform

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// runOSRole runs the Windows-only helper roles; there are none.
func runOSRole(string) bool { return false }

// TestStaleOwner: the owner of a client socket is trusted only while it runs and when it started before the
// connection was accepted (a reused process id is a different process).
func TestStaleOwner(t *testing.T) {
	now := time.Now()
	if staleOwner(os.Getpid(), now) {
		t.Fatal("this process started before now but counts as stale")
	}
	if !staleOwner(os.Getpid(), now.Add(-24*365*time.Hour)) {
		t.Fatal("an owner that started after the accept time must be stale")
	}
	if staleOwner(os.Getpid(), time.Time{}) {
		t.Fatal("a zero accept time checks only that the owner runs")
	}
	if !staleOwner(0x7ffffff0, now) {
		t.Fatal("an owner that does not run must be stale")
	}
}

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

// otherSession fakes a launcher in another session than the daemon. In these tests both are this process, so the
// fake goes by call order: sameSession asks for the root's session first, then for its own.
func otherSession() func(uint32) (uint32, error) {
	calls := uint32(0)
	return func(uint32) (uint32, error) {
		calls++
		return calls % 2, nil
	}
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
	// The job is gone while its root (the owner: this process) still runs, as when a launcher closes its job just
	// before it exits. In the same session that is an empty tree; seen from another session (or when the session
	// cannot be read) the job may exist out of sight, which is an error.
	if wt.RootPID != os.Getpid() || wt.RootStart == 0 {
		t.Fatalf("worker tree root %d/%d, want this process", wt.RootPID, wt.RootStart)
	}
	if left, err := wt.PIDs(); err != nil || len(left) != 0 {
		t.Fatalf("closed job, root in this session: %v, %v", left, err)
	}
	unreadable := func(uint32) (uint32, error) { return 0, errors.New("no session") }
	for _, sessionOf := range []func(uint32) (uint32, error){otherSession(), unreadable} {
		var unavailable *JobUnavailableError
		if left, err := wt.pidsWith(sessionOf); !errors.As(err, &unavailable) || unavailable.Job != wt.Job || unavailable.Tree != wt.ID {
			t.Fatalf("missing job, root in another session: %v, %v; want a JobUnavailableError", left, err)
		}
	}
	if left, err := (&WorkerTree{ID: "ended", Job: wt.Job, RootPID: pids[0], RootStart: 1}).PIDs(); err != nil || len(left) != 0 {
		t.Fatalf("closed job with an ended root: %v, %v", left, err)
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

// TestSocketOwnerUnavailableJob: a launcher that has not created its job yet (this process, in the daemon's
// session) holds no worker, so the lead is not refused. The same tree seen from another session makes the lookup
// fail closed, without a task ID, and the typed error survives for the caller to log.
func TestSocketOwnerUnavailableJob(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	dialer := helper("dial", filepath.Join(t.TempDir(), "dial"), ln.Addr().String())
	HideWindow(dialer)
	if err := dialer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dialer.Process.Kill(); _ = dialer.Wait() }()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	local, remote := addrPorts(t, conn)
	start, err := ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	name, _ := NewTreeName()
	unavailable := &WorkerTree{ID: "t1", Job: name, RootPID: os.Getpid(), RootStart: start}
	if id, isWorker, err := SocketOwnerInTrees(local, remote, []*WorkerTree{unavailable}); err != nil || isWorker || id != "" {
		t.Fatalf("job not created yet, same session: id %q, worker %v, err %v", id, isWorker, err)
	}
	id, isWorker, err := socketOwner(local, remote, []*WorkerTree{unavailable}, lookupOptions{sessionOf: otherSession()})
	var jobErr *JobUnavailableError
	if id != "" || !isWorker || !errors.As(err, &jobErr) || jobErr.Job != name {
		t.Fatalf("lookup: id %q, worker %v, err %v", id, isWorker, err)
	}
}
