//go:build linux

package platform

import "os"

// memoryStatus reads MemAvailable from /proc/meminfo; commit stays unknown (0).
func memoryStatus() MemoryStatus {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryStatus{}
	}
	return parseMeminfo(data)
}
