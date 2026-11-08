//go:build windows

package platform

import "unsafe"

// procGlobalMemoryStatusEx is not wrapped by golang.org/x/sys/windows.
var procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX; the fields Seelegent does not read are blank.
type memoryStatusEx struct {
	length        uint32
	_             uint32 // dwMemoryLoad
	totalPhys     uint64
	availPhys     uint64
	totalPageFile uint64
	availPageFile uint64
	_             [3]uint64 // ullTotalVirtual, ullAvailVirtual, ullAvailExtendedVirtual
}

// memoryStatus reads GlobalMemoryStatusEx: ullAvailPhys and ullAvailPageFile (the commit still available: RAM
// plus page file, despite the name), and their totals.
func memoryStatus() MemoryStatus {
	m := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return MemoryStatus{}
	}
	return MemoryStatus{AvailPhysMB: mib(m.availPhys), AvailCommitMB: mib(m.availPageFile),
		TotalPhysMB: mib(m.totalPhys), TotalCommitMB: mib(m.totalPageFile), Known: true}
}

// cgroupCPUs: Windows has no cgroups.
func cgroupCPUs() (int, bool) { return 0, false }
