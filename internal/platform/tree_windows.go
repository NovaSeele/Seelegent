//go:build windows

package platform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows trees are exact: every descendant stays in the job (no breakaway).
const treesExact = true

// Functions that golang.org/x/sys/windows does not wrap (or wraps without the "already exists" result). Like the
// procs of x/sys itself, they are resolved once and never change.
var (
	modkernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procCreateJobObjectW = modkernel32.NewProc("CreateJobObjectW")
	procOpenJobObjectW   = modkernel32.NewProc("OpenJobObjectW")
)

// Job Object pieces that golang.org/x/sys/windows does not define.
const (
	// JOB_OBJECT_CPU_RATE_CONTROL_ENABLE and JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP.
	jobCPURateControlEnable  = 0x1
	jobCPURateControlHardCap = 0x4
	// JOB_OBJECT_MSG_JOB_MEMORY_LIMIT: a process of the job was refused memory because of the job memory limit.
	jobMsgJobMemoryLimit = 10
)

// memoryPeakShare is the fallback of MemoryLimitHit: a root that failed after the job's peak memory reached this
// share of the cap (percent) counts as having hit it.
const memoryPeakShare = 90

// jobCPURateControl is JOBOBJECT_CPU_RATE_CONTROL_INFORMATION with the CpuRate member of its union.
type jobCPURateControl struct {
	controlFlags uint32
	cpuRate      uint32 // in 1/100 of a percent of the whole machine: 1-10000
}

// jobAssociateCompletionPort is JOBOBJECT_ASSOCIATE_COMPLETION_PORT.
type jobAssociateCompletionPort struct {
	completionKey  uintptr
	completionPort windows.Handle
}

// treeOS holds the Job Object of a tree.
type treeOS struct {
	job   windows.Handle
	name  string // the job's name; "" for an anonymous job
	owner int    // the calling process of StartWorkerTree; 0 for StartTree

	// memCap is the job memory limit in bytes (0: none); port receives the job's notifications while there is
	// one, and memHit remembers a JOB_OBJECT_MSG_JOB_MEMORY_LIMIT read from it.
	memCap uint64
	port   windows.Handle
	memHit bool
}

// start creates a kill-on-close job (named when name is set) with the limits of opt, starts cmd suspended and
// hidden, puts it into the job and only then lets it run, so that not even the first child the root starts can
// escape the job or its limits.
func (t *treeOS) start(cmd *exec.Cmd, worker bool, name string, opt TreeOptions) (err error) {
	job, err := createJob(name)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = windows.CloseHandle(job)
		}
	}()
	memCap, err := limitJob(job, opt)
	if err != nil {
		return err
	}
	var port windows.Handle
	if memCap > 0 {
		// Without the port MemoryLimitHit falls back to the peak; the cap itself holds either way.
		port, _ = notifyPort(job)
	}
	defer func() {
		if err != nil && port != 0 {
			_ = windows.CloseHandle(port)
		}
	}()

	hideWindow(cmd)
	sysProcAttr(cmd).CreationFlags |= windows.CREATE_SUSPENDED
	if opt.Priority == PriorityLow {
		sysProcAttr(cmd).CreationFlags |= windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := uint32(cmd.Process.Pid) //nolint:gosec // a started process has a positive pid
	if err := assignAndResume(job, pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	t.job, t.name, t.memCap, t.port = job, name, memCap, port
	if worker {
		t.owner = os.Getpid()
	}
	return nil
}

// limitJob makes job kill-on-close and applies the limits of opt: the priority class (BELOW_NORMAL for every
// process of the job; a process cannot raise it), the job memory limit and the CPU rate hard cap. It returns the
// memory cap in bytes that holds (0: none). A limit Windows refuses is left out; kill-on-close never is.
func limitJob(job windows.Handle, opt TreeOptions) (memCap uint64, err error) {
	set := func(info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
		_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)))
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	plain := info
	if opt.Priority == PriorityLow {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PRIORITY_CLASS
		info.BasicLimitInformation.PriorityClass = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	if opt.MemoryLimitMB > 0 {
		memCap = uint64(opt.MemoryLimitMB) << 20
		limit := uintptr(memCap)
		if uint64(limit) != memCap { // a 32-bit system: no cap above what its address space can hold
			limit = ^uintptr(0)
		}
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = limit
	}
	if err := set(&info); err != nil {
		memCap = 0
		if info.BasicLimitInformation.LimitFlags == plain.BasicLimitInformation.LimitFlags {
			return 0, fmt.Errorf("configure job object: %w", err)
		}
		if err := set(&plain); err != nil {
			return 0, fmt.Errorf("configure job object: %w", err)
		}
	}
	if pct := min(opt.CPURatePercent, 100); pct > 0 {
		rate := jobCPURateControl{controlFlags: jobCPURateControlEnable | jobCPURateControlHardCap,
			cpuRate: uint32(pct) * 100}
		// Before Windows 8 there is no CPU rate control: the tree runs uncapped.
		_, _ = windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation,
			uintptr(unsafe.Pointer(&rate)), uint32(unsafe.Sizeof(rate)))
	}
	return memCap, nil
}

// notifyPort creates a completion port and associates it with job, so the job's notifications (among them
// JOB_OBJECT_MSG_JOB_MEMORY_LIMIT) queue on it. It must be done before the first process joins the job.
func notifyPort(job windows.Handle) (windows.Handle, error) {
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 1)
	if err != nil {
		return 0, fmt.Errorf("create completion port: %w", err)
	}
	assoc := jobAssociateCompletionPort{completionKey: uintptr(job), completionPort: port}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectAssociateCompletionPortInformation,
		uintptr(unsafe.Pointer(&assoc)), uint32(unsafe.Sizeof(assoc))); err != nil {
		_ = windows.CloseHandle(port)
		return 0, fmt.Errorf("associate completion port: %w", err)
	}
	return port, nil
}

// drainPort reads every notification queued on the job's port without waiting and remembers a memory-limit one.
func (t *treeOS) drainPort() {
	if t.port == 0 {
		return
	}
	for {
		var msg uint32
		var key uintptr
		var ov *windows.Overlapped
		if err := windows.GetQueuedCompletionStatus(t.port, &msg, &key, &ov, 0); err != nil {
			return // WAIT_TIMEOUT: nothing left
		}
		if msg == jobMsgJobMemoryLimit {
			t.memHit = true
		}
	}
}

// memoryLimitHit implements Tree.MemoryLimitHit; code is the root's exit code (-1: not exited).
func (t *treeOS) memoryLimitHit(code int) bool {
	if t.memCap == 0 || t.job == 0 {
		return false
	}
	t.drainPort()
	if t.memHit {
		return true
	}
	if code == 0 || code == -1 {
		return false
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(t.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return false
	}
	return uint64(info.PeakJobMemoryUsed)*100 >= t.memCap*memoryPeakShare
}

// createJob creates a job object. A named job must be new: if the name exists (someone created it first, or it is
// still in use) the job is refused with ErrTreeNameTaken.
func createJob(name string) (windows.Handle, error) {
	if name == "" {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return 0, fmt.Errorf("create job object: %w", err)
		}
		return job, nil
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("create job object %s: %w", name, err)
	}
	// The runtime clears the thread's last error before the call, so callErr is this call's own result.
	r, _, callErr := procCreateJobObjectW.Call(0, uintptr(unsafe.Pointer(p)))
	job := windows.Handle(r)
	if job == 0 {
		if errors.Is(callErr, windows.ERROR_ACCESS_DENIED) || errors.Is(callErr, windows.ERROR_INVALID_HANDLE) {
			// The name exists and belongs to someone we may not open, or to another kind of object.
			return 0, fmt.Errorf("create job object %s: %w", name, ErrTreeNameTaken)
		}
		return 0, fmt.Errorf("create job object %s: %w", name, callErr)
	}
	if errors.Is(callErr, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("create job object %s: %w", name, ErrTreeNameTaken)
	}
	return job, nil
}

// newTreeName returns `Local\Seelegent-` and 128 random bits in hex.
func newTreeName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("new tree name: %w", err)
	}
	return `Local\Seelegent-` + hex.EncodeToString(b[:]), nil
}

func (t *treeOS) describe(id string, root int) *WorkerTree {
	w := &WorkerTree{ID: id, RootPID: root, Job: t.name}
	if t.owner > 0 {
		w.RootPID = t.owner
	}
	w.RootStart, _ = procStart(w.RootPID)
	return w
}

func (t *treeOS) wait(cmd *exec.Cmd) error { return cmd.Wait() }

func (t *treeOS) exitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

// assignAndResume puts the suspended process pid into job and resumes its threads.
func assignAndResume(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	return resumeThreads(pid)
}

// resumeThreads resumes every thread of process pid (a process created suspended has exactly one).
func resumeThreads(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("resume process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	resumed := 0
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("resume process %d: open thread: %w", pid, err)
		}
		_, rerr := windows.ResumeThread(th)
		_ = windows.CloseHandle(th)
		if rerr != nil {
			return fmt.Errorf("resume process %d: %w", pid, rerr)
		}
		resumed++
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("resume process %d: list threads: %w", pid, err)
	}
	if resumed == 0 {
		return fmt.Errorf("resume process %d: no thread found", pid)
	}
	return nil
}

func (t *treeOS) kill(time.Duration) error {
	if t.job == 0 {
		return ErrNotStarted
	}
	if err := windows.TerminateJobObject(t.job, 1); err != nil {
		return fmt.Errorf("terminate job: %w", err)
	}
	return nil
}

func (t *treeOS) close(*exec.Cmd) error {
	if t.job == 0 {
		return nil
	}
	// Kill-on-close: closing the last handle stops anything still in the job. The daemon opens a named job only
	// for a moment (WorkerTree.PIDs), so the launcher's handle is the last one.
	err := windows.CloseHandle(t.job)
	t.job = 0
	if t.port != 0 {
		_ = windows.CloseHandle(t.port)
		t.port = 0
	}
	if err != nil {
		return fmt.Errorf("close job: %w", err)
	}
	return nil
}
