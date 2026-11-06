//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// treeOS holds the Job Object of a tree.
type treeOS struct {
	job windows.Handle
}

// start creates a kill-on-close job, starts cmd suspended and hidden, puts it into the job and only then lets it
// run, so that not even the first child the root starts can escape the job.
func (t *treeOS) start(cmd *exec.Cmd) (err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create job object: %w", err)
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
	t.job = job
	return nil
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

func (t *treeOS) close() error {
	if t.job == 0 {
		return nil
	}
	// Kill-on-close: closing the last handle stops anything still in the job.
	err := windows.CloseHandle(t.job)
	t.job = 0
	if err != nil {
		return fmt.Errorf("close job: %w", err)
	}
	return nil
}
