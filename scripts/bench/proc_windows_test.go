//go:build bench

package bench

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	benchKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procK32GetProcessMemInfo  = benchKernel32.NewProc("K32GetProcessMemoryInfo")
	procGetProcessHandleCount = benchKernel32.NewProc("GetProcessHandleCount")
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
