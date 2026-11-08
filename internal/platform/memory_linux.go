//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// cgroupRoot is where cgroup v2 is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// memoryStatus reads MemAvailable from /proc/meminfo and lowers it to what the cgroup v2 memory limits of this
// process (its own group and every parent, inside a container or a systemd slice with MemoryMax) still allow.
// Commit stays unknown (0).
func memoryStatus() MemoryStatus {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryStatus{}
	}
	m := parseMeminfo(data)
	if free, ok := cgroupFree(); ok && m.Known && free < m.AvailPhysMB {
		m.AvailPhysMB = free
	}
	return m
}

// cgroupFree is the smallest room left under the memory.max of this process's cgroup and its parents, in MiB;
// false when none of them has a limit.
func cgroupFree() (int64, bool) {
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0, false
	}
	p, ok := cgroupV2Path(self)
	if !ok {
		return 0, false
	}
	var least int64
	found := false
	for dir := filepath.Join(cgroupRoot, p); strings.HasPrefix(dir, cgroupRoot); dir = filepath.Dir(dir) {
		limit, err1 := os.ReadFile(filepath.Join(dir, "memory.max"))
		cur, err2 := os.ReadFile(filepath.Join(dir, "memory.current"))
		if err1 == nil && err2 == nil {
			if free, ok := cgroupFreeMB(limit, cur); ok && (!found || free < least) {
				least, found = free, true
			}
		}
		if dir == cgroupRoot {
			break
		}
	}
	return least, found
}
