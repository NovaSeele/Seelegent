package platform

import (
	"context"
	"errors"
	"fmt"
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
//   - console: writes consoleState (whether the process has a console, and its window) to the file named last;
//   - console-children: does what console does, then starts two console helpers, one with HideWindow and one as
//     a StartTree root, which write their state to the files named last with ".hidden" and ".tree" appended, and
//     waits for them (it is started without a console: the case where HideWindow must give them a hidden console
//     of their own).
func runOSRole(role string) bool {
	out := os.Args[len(os.Args)-1]
	switch role {
	case "console":
		if err := os.WriteFile(out, []byte(consoleState()), 0o600); err != nil {
			os.Exit(3)
		}
		return true
	case "console-children":
		if err := os.WriteFile(out, []byte(consoleState()), 0o600); err != nil {
			os.Exit(3)
		}
		env := func(cmd *exec.Cmd) *exec.Cmd {
			cmd.Env = append(os.Environ(), roleEnv+"=console")
			return cmd
		}
		hidden := env(exec.Command(selfExe(), out+".hidden")) //nolint:noctx // a test helper's own child
		HideWindow(hidden)
		if err := hidden.Run(); err != nil {
			os.Exit(4)
		}
		tree, err := StartTree(env(exec.Command(selfExe(), out+".tree"))) //nolint:noctx // a test helper's own child
		if err != nil {
			os.Exit(5)
		}
		werr := tree.Wait()
		_ = tree.Close()
		if werr != nil {
			os.Exit(6)
		}
		return true
	}
	return false
}

// consoleState is "none" for a process without a console, else "console" and its window handle (0: hidden).
func consoleState() string {
	if !hasConsole() {
		return "none"
	}
	win, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	return fmt.Sprintf("console %#x", win)
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

// TestHiddenStartsHaveNoConsoleWindow: no way Seelegent starts a hidden process (HideWindow, the detached daemon
// and launcher, a process tree) opens a console window, so no window flashes when a worker or a git call starts
// (old L14 and L19). A child of a process with a console shares that console (no console host of its own); a
// child of a process without one gets a hidden console; the launcher (StartDetachedNoConsole) gets none.
func TestHiddenStartsHaveNoConsoleWindow(t *testing.T) {
	dir := t.TempDir()
	state := func(name string) string {
		t.Helper()
		mark, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(mark)
	}
	check := func(name string, cmd *exec.Cmd, wait func() error, want uint32, wantState string) {
		t.Helper()
		attr := cmd.SysProcAttr
		if attr == nil || !attr.HideWindow || attr.CreationFlags&want != want {
			t.Errorf("%s: SysProcAttr %+v, want HideWindow and flags %#x", name, attr, want)
		}
		if err := wait(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := state(name); got != wantState {
			t.Errorf("%s: console %q, want %q", name, got, wantState)
		}
	}
	// A hidden start shares this process's console (whose window, if any, is already there) or, without one,
	// gets a hidden console.
	inherited, ownFlag := consoleState(), uint32(0)
	t.Logf("this process: %s", inherited)
	if inherited == "none" {
		inherited, ownFlag = "console 0x0", windows.CREATE_NO_WINDOW
	}

	hidden := helper("console", filepath.Join(dir, "hidden"))
	HideWindow(hidden)
	if err := hidden.Start(); err != nil {
		t.Fatal(err)
	}
	check("hidden", hidden, hidden.Wait, ownFlag, inherited)

	detached, err := StartDetached(func() *exec.Cmd { return helper("console", filepath.Join(dir, "detached")) })
	if err != nil {
		t.Fatal(err)
	}
	check("detached", detached, detached.Wait, windows.CREATE_NO_WINDOW|windows.CREATE_NEW_PROCESS_GROUP, "console 0x0")

	cmd := helper("console", filepath.Join(dir, "tree"))
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tree.Close() })
	check("tree", cmd, tree.Wait, ownFlag, inherited)

	// The launcher has no console, and what it starts hidden gets a hidden console of its own.
	launcher, err := StartDetachedNoConsole(func() *exec.Cmd {
		return helper("console-children", filepath.Join(dir, "children"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if attr := launcher.SysProcAttr; attr.CreationFlags&windows.CREATE_NO_WINDOW != 0 {
		t.Errorf("no console: flags %#x, want no CREATE_NO_WINDOW", attr.CreationFlags)
	}
	check("children", launcher, launcher.Wait, windows.DETACHED_PROCESS|windows.CREATE_NEW_PROCESS_GROUP, "none")
	for _, name := range []string{"children.hidden", "children.tree"} {
		if got := state(name); got != "console 0x0" {
			t.Errorf("%s: console %q, want a hidden console of its own", name, got)
		}
	}
}

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

// TestTreeMemoryLimitPeak: a tree whose peak memory reached 93% of its cap but was never refused memory reports
// no hit: not when its owner killed it (exit code 1 is the kill's; review N1), and not when it failed by itself while
// the job's port works (the port's report decides). Only without a port does a failure near the cap count.
func TestTreeMemoryLimitPeak(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kill, withPort bool
		want           bool
	}{
		{"killed, port", true, true, false},
		{"killed, no port", true, false, false},
		{"failed, port", false, true, false},
		{"failed, no port", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			tree, err := StartTreeWith(helper("hold", "64", pidFile, "3"), TreeOptions{MemoryLimitMB: 1024})
			if err != nil {
				t.Fatalf("StartTreeWith: %v", err)
			}
			defer func() { _ = tree.Close() }()
			if !tc.withPort {
				dropPort(tree)
			}
			readPids(t, pidFile)
			capAbovePeak(t, tree, 93)
			if tc.kill {
				if err := tree.Kill(0); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(pidFile+".go", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			_ = tree.Wait()
			if code, hit := tree.ExitCode(), tree.MemoryLimitHit(); code == 0 || hit != tc.want {
				t.Fatalf("exit %d, limit hit %v; want a failure and hit %v", code, hit, tc.want)
			}
		})
	}
}

// TestTreePollSeesLimit: Poll drains the job's port and reports a refused allocation.
func TestTreePollSeesLimit(t *testing.T) {
	tree, err := StartTreeWith(helper("alloc", "200"), TreeOptions{MemoryLimitMB: 64})
	if err != nil {
		t.Fatalf("StartTreeWith: %v", err)
	}
	_ = tree.Wait()
	if !tree.Poll() || !tree.MemoryLimitHit() {
		t.Error("the job's report must reach Poll and MemoryLimitHit")
	}
	_ = tree.Close()
	if tree.Poll() {
		t.Error("Poll after Close must be false")
	}
}

// capAbovePeak moves the job's memory cap so that its peak so far is pct percent of it.
func capAbovePeak(t *testing.T, tree *Tree, pct uint64) {
	t.Helper()
	tree.mu.Lock()
	defer tree.mu.Unlock()
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(tree.os.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		t.Fatal(err)
	}
	limit := uint64(info.PeakJobMemoryUsed) * 100 / pct
	info.JobMemoryLimit = uintptr(limit)
	if _, err := windows.SetInformationJobObject(tree.os.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	tree.os.memCap = limit
	t.Logf("peak %d MiB, cap %d MiB", info.PeakJobMemoryUsed>>20, limit>>20)
}

// dropPort closes the job's completion port, as if it could not be set up.
func dropPort(tree *Tree) {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	_ = windows.CloseHandle(tree.os.port)
	tree.os.port = 0
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
	if refused := tree.Unapplied(); refused != nil {
		t.Errorf("refused limits: %v", refused)
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
