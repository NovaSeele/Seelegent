//go:build windows

package platform

import "unsafe"

// procGlobalMemoryStatusEx is not wrapped by golang.org/x/sys/windows.
var procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX; the fields Seelegent does not read are blank.
type memoryStatusEx struct {
	length        uint32
	_             uint32 // dwMemoryLoad
	_             uint64 // ullTotalPhys
	availPhys     uint64
	_             uint64 // ullTotalPageFile
	availPageFile uint64
	_             [3]uint64 // ullTotalVirtual, ullAvailVirtual, ullAvailExtendedVirtual
}

// memoryStatus reads GlobalMemoryStatusEx: ullAvailPhys and ullAvailPageFile (the commit still available: RAM
// plus page file, despite the name).
func memoryStatus() MemoryStatus {
	m := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return MemoryStatus{}
	}
	return MemoryStatus{AvailPhysMB: mib(m.availPhys), AvailCommitMB: mib(m.availPageFile), Known: true}
}
