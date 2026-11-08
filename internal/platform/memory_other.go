//go:build !windows && !linux && !darwin

package platform

// memoryStatus has no reading on the BSDs and other systems: unknown, which never holds anything back.
func memoryStatus() MemoryStatus { return MemoryStatus{} }

// cgroupCPUs: no cgroups here.
func cgroupCPUs() (int, bool) { return 0, false }
