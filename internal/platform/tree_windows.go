//go:build windows

package platform

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
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
	// NtResumeProcess is not documented, but it has been part of ntdll since Windows XP with the same signature
	// (NTSTATUS NtResumeProcess(HANDLE)), and debuggers and Process Explorer rely on it.
	procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
)

// Job Object pieces that golang.org/x/sys/windows does not define.
const (
	// JOB_OBJECT_CPU_RATE_CONTROL_ENABLE and JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP.
	jobCPURateControlEnable  = 0x1
	jobCPURateControlHardCap = 0x4
	// JOB_OBJECT_MSG_JOB_MEMORY_LIMIT: a process of the job was refused memory because of the job memory limit.
	jobMsgJobMemoryLimit = 10
)

// memoryPeakShare is the fallback of MemoryLimitHit when the job has no completion port: a root that failed by
// itself after the job's peak memory reached this share of the cap (percent) counts as having hit it.
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
	// killed is set once kill terminated the job: its exit code is then ours, not a failure of the tree.
	killed bool
	// unapplied names the limits that were asked for and that Windows refused.
	unapplied []string
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
	memCap, unapplied, err := limitJob(job, opt)
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

	if opt.ShareConsole && !worker {
		hideWindowShared(cmd)
	} else {
		hideWindow(cmd)
	}
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
	t.job, t.name, t.memCap, t.port, t.unapplied = job, name, memCap, port, unapplied
	if worker {
		t.owner = os.Getpid()
	}
	return nil
}

// limitJob makes job kill-on-close and applies the limits of opt: the priority class (BELOW_NORMAL for every
// process of the job; a process cannot raise it), the job memory limit and the CPU rate hard cap. It returns the
// memory cap in bytes that holds (0: none) and the names of the limits Windows refused, which are left out:
// priority and memory cap together, else priority alone, else neither. Kill-on-close is never left out.
func limitJob(job windows.Handle, opt TreeOptions) (memCap uint64, unapplied []string, err error) {
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
	priorityOnly := info
	if opt.MemoryLimitMB > 0 {
		memCap = uint64(opt.MemoryLimitMB) << 20
		limit := uintptr(memCap)
		if uint64(limit) != memCap { // a 32-bit system: no cap above what its address space can hold
			limit = ^uintptr(0)
		}
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = limit
	}
	flags := func(i windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) uint32 { return i.BasicLimitInformation.LimitFlags }
	if err := set(&info); err != nil {
		if flags(info) == flags(plain) {
			return 0, nil, fmt.Errorf("configure job object: %w", err)
		}
		memCap = 0
		if opt.MemoryLimitMB > 0 {
			unapplied = append(unapplied, "memory cap")
		}
		if flags(priorityOnly) == flags(plain) || flags(priorityOnly) == flags(info) || set(&priorityOnly) != nil {
			if opt.Priority == PriorityLow {
				unapplied = append(unapplied, "low priority")
			}
			if err := set(&plain); err != nil {
				return 0, nil, fmt.Errorf("configure job object: %w", err)
			}
		}
	}
	if pct := min(opt.CPURatePercent, 100); pct > 0 {
		rate := jobCPURateControl{controlFlags: jobCPURateControlEnable | jobCPURateControlHardCap,
			cpuRate: uint32(pct) * 100}
		// Before Windows 8 there is no CPU rate control: the tree runs uncapped.
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation,
			uintptr(unsafe.Pointer(&rate)), uint32(unsafe.Sizeof(rate))); err != nil {
			unapplied = append(unapplied, "CPU rate cap")
		}
	}
	return memCap, unapplied, nil
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

// poll implements Tree.Poll: it drains the port, so its packets (one per process start and exit) do not pile up in
// kernel memory over a long turn, and reports whether the job reported its memory limit so far.
func (t *treeOS) poll() bool {
	t.drainPort()
	return t.memHit
}

// memoryLimitHit implements Tree.MemoryLimitHit; code is the root's exit code (-1: not exited). The job's own
// report on its port decides. Only a job without a port falls back to its peak memory, and never after kill
// terminated it (its exit code 1 is then ours, and a peak near the cap proves nothing).
func (t *treeOS) memoryLimitHit(code int) bool {
	if t.memCap == 0 || t.job == 0 {
		return false
	}
	t.drainPort()
	if t.memHit {
		return true
	}
	if t.port != 0 || t.killed || code == 0 || code == -1 {
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

// newTreeName returns `Local\Seelegent-` and 128 random bits in hex. The bits come from math/rand/v2, whose
// generator the runtime seeds from the system's random source in every process: the names only have to be unique,
// and a name that is taken anyway is refused by StartWorkerTree (ErrTreeNameTaken). crypto/rand is not used
// because the launcher (cmd/seelegent-launch), which names the trees of launches written before M19, must not
// link any crypto package.
func newTreeName() (string, error) {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], rand.Uint64()) //nolint:gosec // unique, not secret (see above)
	binary.LittleEndian.PutUint64(b[8:], rand.Uint64()) //nolint:gosec // unique, not secret (see above)
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

// assignAndResume puts the suspended process pid into job and resumes it.
//
// The root is created suspended (its only thread does not run yet) so that it joins the job before it can start
// anything. It is resumed through the process handle with NtResumeProcess. syscall.StartProcess closes the thread
// handle of the new process, and finding the thread again through a toolhelp snapshot walks every thread of the
// machine: about 65 ms with 10k threads, on every git, gh, quota, hook and gate command (M19 bench). Creating the
// process inside the job (PROC_THREAD_ATTRIBUTE_JOB_LIST) would need no suspension, but exec.Cmd cannot pass
// that attribute.
func assignAndResume(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, pid)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	return resumeProcess(h, pid)
}

// resumeProcess resumes every thread of the suspended process h (one, for a process created suspended).
func resumeProcess(h windows.Handle, pid uint32) error {
	if err := procNtResumeProcess.Find(); err != nil {
		return fmt.Errorf("resume process %d: %w", pid, err)
	}
	r, _, _ := procNtResumeProcess.Call(uintptr(h))
	if status := windows.NTStatus(r); status != windows.STATUS_SUCCESS { //nolint:gosec // an NTSTATUS is 32 bits
		return fmt.Errorf("resume process %d: %w", pid, status)
	}
	return nil
}

func (t *treeOS) kill(time.Duration) error {
	if t.job == 0 {
		return ErrNotStarted
	}
	t.killed = true
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

// unappliedLimits implements Tree.Unapplied.
func (t *treeOS) unappliedLimits() []string { return t.unapplied }
