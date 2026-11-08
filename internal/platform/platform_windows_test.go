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
	"unsafe"

	"golang.org/x/sys/windows"
)

// runOSRole runs the Windows-only helper roles:
//   - console: writes 1 to the file named last when the process has a console window, else 0.
func runOSRole(role string) bool {
	if role != "console" {
		return false
	}
	win, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	mark := "0"
	if win != 0 {
		mark = "1"
	}
	if err := os.WriteFile(os.Args[len(os.Args)-1], []byte(mark), 0o600); err != nil {
		os.Exit(3)
	}
	return true
}

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

// TestHiddenStartsHaveNoConsoleWindow: every way Seelegent starts a hidden process (HideWindow, the detached
// launcher, a process tree) sets CREATE_NO_WINDOW, and the process really has no console window, so no window
// flashes when a worker or a git call starts (old L14 and L19).
func TestHiddenStartsHaveNoConsoleWindow(t *testing.T) {
	dir := t.TempDir()
	check := func(name string, cmd *exec.Cmd, wait func() error, want uint32) {
		t.Helper()
		attr := cmd.SysProcAttr
		if attr == nil || !attr.HideWindow || attr.CreationFlags&want != want {
			t.Errorf("%s: SysProcAttr %+v, want HideWindow and flags %#x", name, attr, want)
		}
		if err := wait(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mark, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(mark) != "0" {
			t.Errorf("%s: console window %q, %v", name, mark, err)
		}
	}

	hidden := helper("console", filepath.Join(dir, "hidden"))
	HideWindow(hidden)
	if err := hidden.Start(); err != nil {
		t.Fatal(err)
	}
	check("hidden", hidden, hidden.Wait, windows.CREATE_NO_WINDOW)

	detached, err := StartDetached(func() *exec.Cmd { return helper("console", filepath.Join(dir, "detached")) })
	if err != nil {
		t.Fatal(err)
	}
	check("detached", detached, detached.Wait, windows.CREATE_NO_WINDOW|windows.CREATE_NEW_PROCESS_GROUP)

	cmd := helper("console", filepath.Join(dir, "tree"))
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tree.Close() })
	check("tree", cmd, tree.Wait, windows.CREATE_NO_WINDOW)
}

// priorityOf returns the priority class of process pid.
func priorityOf(t *testing.T, pid int) int {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	class, err := windows.GetPriorityClass(h)
	if err != nil {
		t.Fatalf("priority class of %d: %v", pid, err)
	}
	return int(class)
}

// isLow reports whether a priority class is the low one of a tree.
func isLow(class int) bool { return class == windows.BELOW_NORMAL_PRIORITY_CLASS }

// TestTreeMemoryCap: a tree that needs more than its job memory cap fails, and MemoryLimitHit says why; one that
// stays under it, or has no cap, reports nothing.
func TestTreeMemoryCap(t *testing.T) {
	run := func(mb string, opt TreeOptions) (code int, hit bool) {
		t.Helper()
		tree, err := StartTreeWith(helper("alloc", mb), opt)
		if err != nil {
			t.Fatalf("StartTreeWith: %v", err)
		}
		_ = tree.Wait()
		code, hit = tree.ExitCode(), tree.MemoryLimitHit()
		if err := tree.Close(); err != nil {
			t.Fatal(err)
		}
		if tree.MemoryLimitHit() {
			t.Fatal("MemoryLimitHit after Close must be false")
		}
		return code, hit
	}
	if code, hit := run("200", TreeOptions{MemoryLimitMB: 64}); code == 0 || !hit {
		t.Fatalf("200 MB under a 64 MB cap: exit %d, limit hit %v; want a failure and a hit", code, hit)
	}
	if code, hit := run("8", TreeOptions{MemoryLimitMB: 256}); code != 0 || hit {
		t.Fatalf("8 MB under a 256 MB cap: exit %d, limit hit %v", code, hit)
	}
	if code, hit := run("8", TreeOptions{}); code != 0 || hit {
		t.Fatalf("no cap: exit %d, limit hit %v", code, hit)
	}
}

// TestTreeJobLimits: the job holds what the options ask for (read back from the job).
func TestTreeJobLimits(t *testing.T) {
	tree, err := StartTreeWith(helper("sleeper", filepath.Join(t.TempDir(), "pid")),
		TreeOptions{Priority: PriorityLow, MemoryLimitMB: 512, CPURatePercent: 25})
	if err != nil {
		t.Fatalf("StartTreeWith: %v", err)
	}
	defer func() { _ = tree.Close() }()
	var rate jobCPURateControl
	if err := windows.QueryInformationJobObject(tree.os.job, windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&rate)), uint32(unsafe.Sizeof(rate)), nil); err != nil {
		t.Fatalf("query CPU rate: %v", err)
	}
	if rate.controlFlags != jobCPURateControlEnable|jobCPURateControlHardCap || rate.cpuRate != 2500 {
		t.Errorf("CPU rate control %+v, want enabled hard cap at 2500 (25%%)", rate)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(tree.os.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		t.Fatalf("query limits: %v", err)
	}
	want := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_PRIORITY_CLASS |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY)
	b := info.BasicLimitInformation
	if b.LimitFlags != want || b.PriorityClass != windows.BELOW_NORMAL_PRIORITY_CLASS || info.JobMemoryLimit != 512<<20 {
		t.Errorf("limits: flags %#x (want %#x), class %#x, memory %d", b.LimitFlags, want, b.PriorityClass,
			info.JobMemoryLimit)
	}
	if tree.os.port == 0 {
		t.Error("a memory cap needs the job's completion port")
	}

	// No options: kill-on-close only, no CPU rate control, no port.
	plain, err := StartTreeWith(helper("sleeper", filepath.Join(t.TempDir(), "pid")), TreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	rate = jobCPURateControl{}
	if err := windows.QueryInformationJobObject(plain.os.job, windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&rate)), uint32(unsafe.Sizeof(rate)), nil); err != nil {
		t.Fatalf("query CPU rate: %v", err)
	}
	if rate.controlFlags != 0 || plain.os.port != 0 {
		t.Errorf("plain tree: CPU rate %+v, port %v", rate, plain.os.port)
	}
}

// atNormalPriority runs the test at normal priority for its duration: a test started at below normal (a gentle
// `go test` on a busy machine) could not tell a lowered tree from an inherited priority.
func atNormalPriority(t *testing.T) {
	t.Helper()
	self := windows.CurrentProcess()
	class, err := windows.GetPriorityClass(self)
	if err != nil || class == windows.NORMAL_PRIORITY_CLASS {
		return
	}
	if err := windows.SetPriorityClass(self, windows.NORMAL_PRIORITY_CLASS); err != nil {
		t.Logf("cannot run at normal priority: %v", err)
		return
	}
	t.Cleanup(func() { _ = windows.SetPriorityClass(self, class) })
}
