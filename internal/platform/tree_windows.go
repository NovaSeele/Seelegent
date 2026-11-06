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

// treeOS holds the Job Object of a tree.
type treeOS struct {
	job   windows.Handle
	name  string // the job's name; "" for an anonymous job
	owner int    // the calling process of StartWorkerTree; 0 for StartTree
}

// start creates a kill-on-close job (named when name is set), starts cmd suspended and hidden, puts it into the
// job and only then lets it run, so that not even the first child the root starts can escape the job.
func (t *treeOS) start(cmd *exec.Cmd, worker bool, name string) (err error) {
	job, err := createJob(name)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = windows.CloseHandle(job)
		}
	}()
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("configure job object: %w", err)
	}

	hideWindow(cmd)
	sysProcAttr(cmd).CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := uint32(cmd.Process.Pid) //nolint:gosec // a started process has a positive pid
	if err := assignAndResume(job, pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	t.job, t.name = job, name
	if worker {
		t.owner = os.Getpid()
	}
	return nil
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
	if err != nil {
		return fmt.Errorf("close job: %w", err)
	}
	return nil
}
