//go:build darwin

package platform

import "golang.org/x/sys/unix"

// memoryStatus is macOS's best effort: hw.memsize × kern.memorystatus_level / 100; commit stays unknown (0).
func memoryStatus() MemoryStatus {
	memsize, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return MemoryStatus{}
	}
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil {
		return MemoryStatus{}
	}
	return fromMemorystatusLevel(memsize, level)
}

// cgroupCPUs: macOS has no cgroups.
func cgroupCPUs() (int, bool) { return 0, false }
