//go:build bench

package bench

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	benchKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procK32GetProcessMemInfo  = benchKernel32.NewProc("K32GetProcessMemoryInfo")
	procGetProcessHandleCount = benchKernel32.NewProc("GetProcessHandleCount")
	procGlobalMemoryStatusEx  = benchKernel32.NewProc("GlobalMemoryStatusEx")
)

// processMemoryCountersEx is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

// listProcs lists every process of the machine with a toolhelp snapshot.
func listProcs() (map[int]procEntry, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	out := map[int]procEntry{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		out[int(e.ProcessID)] = procEntry{PID: int(e.ProcessID), PPID: int(e.ParentProcessID),
			Name: windows.UTF16ToString(e.ExeFile[:]), Threads: int(e.Threads)}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return out, nil
}

func filetime(f windows.Filetime) time.Duration {
	return time.Duration(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) * 100
}

// startTime returns when a process started (to tell a reused pid from its parent's child).
func startTime(pid int) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var c, x, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, c.Nanoseconds()), nil
}

// procStats reads the counters of one process.
func procStats(pid int) (procStat, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return procStat{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return handleStats(h)
}

func handleStats(h windows.Handle) (procStat, error) {
	var st procStat
	var c, x, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		return st, err
	}
	st.CPU = filetime(k) + filetime(u)
	var m processMemoryCountersEx
	m.CB = uint32(unsafe.Sizeof(m))
	if r, _, err := procK32GetProcessMemInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.CB)); r == 0 {
		return st, err
	}
	st.WS, st.PeakWS, st.Private = uint64(m.WorkingSetSize), uint64(m.PeakWorkingSetSize), uint64(m.PrivateUsage)
	var n uint32
	if r, _, _ := procGetProcessHandleCount.Call(uintptr(h), uintptr(unsafe.Pointer(&n))); r != 0 {
		st.Handles = int(n)
	}
	return st, nil
}

// procHandle keeps a process object open, so its final counters can be read after it exited.
type procHandle struct{ h windows.Handle }

func openProc(pid int) (*procHandle, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	return &procHandle{h: h}, nil
}

func (p *procHandle) stats() (procStat, error) { return handleStats(p.h) }

func (p *procHandle) close() { _ = windows.CloseHandle(p.h) }

// selfStats reads this process's own counters (the fake quota reader records them as it exits).
func selfStats() (procStat, error) { return handleStats(windows.CurrentProcess()) }

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// machineMemory reads the machine's physical memory and commit (the page file total is the commit limit).
func machineMemory() (machineStat, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return machineStat{}, err
	}
	return machineStat{TotalPhys: m.TotalPhys, AvailPhys: m.AvailPhys, CommitLimit: m.TotalPageFile,
		CommitAvail: m.AvailPageFile}, nil
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// treeJob is a Job Object around the daemon: Windows accounts the CPU of every process of the tree in it, the
// short-lived ones included, and counts the processes started. Its own jobs (StartTree, the launchers' worker jobs)
// nest inside it. The launchers ask to break away; the job does not allow it, so they start inside it (the
// launcher's own fallback). Kill-on-close: nothing of the tree outlives the bench.
type treeJob struct{ h windows.Handle }

// newTreeJob puts process pid into a new job. Processes the daemon started before this call are outside it (the
// daemon starts none in its first moments).
func newTreeJob(pid int) (*treeJob, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(p) }()
	if err := windows.AssignProcessToJobObject(h, p); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	return &treeJob{h: h}, nil
}

// stats reads the job's accounting: CPU of every process that ran in it, processes started and alive, and the peak
// commit of the whole job.
func (j *treeJob) stats() (jobStat, error) {
	var a jobAccounting
	if err := windows.QueryInformationJobObject(j.h, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil); err != nil {
		return jobStat{}, err
	}
	st := jobStat{CPU: time.Duration(a.TotalUserTime+a.TotalKernelTime) * 100, Started: int(a.TotalProcesses),
		Active: int(a.ActiveProcesses)}
	var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(j.h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil); err == nil {
		st.PeakCommit = uint64(ext.PeakJobMemoryUsed)
	}
	return st, nil
}

// close closes the job, which kills what is left in it.
func (j *treeJob) close() { _ = windows.CloseHandle(j.h) }

// osVersion is the Windows version and build.
func osVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
