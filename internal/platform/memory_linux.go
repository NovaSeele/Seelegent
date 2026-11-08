//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// cgroupRoot is where cgroup v2 is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// memoryStatus reads MemAvailable and MemTotal from /proc/meminfo and lowers them to what the cgroup v2 memory
// limits of this process (its own group and every parent, inside a container or a systemd slice with MemoryMax)
// allow. Commit stays unknown (0).
func memoryStatus() MemoryStatus {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryStatus{}
	}
	m := parseMeminfo(data)
	if !m.Known {
		return m
	}
	room := cgroupMemory()
	if room.limited && room.free < m.AvailPhysMB {
		m.AvailPhysMB = room.free
	}
	if room.limited && room.limit < m.TotalPhysMB {
		m.TotalPhysMB = room.limit
	}
	return m
}

// cgroupRoom is what the cgroup v2 memory limits of this process allow: the smallest limit and the smallest room
// left under one, in MiB (limited false when no level has a limit).
type cgroupRoom struct {
	free, limit int64
	limited     bool
}

// cgroupMemory walks this process's cgroup and its parents: each level's memory.max, and the room left under it
// against its own working set (cgroupFreeMB).
func cgroupMemory() cgroupRoom {
	var r cgroupRoom
	for _, dir := range cgroupDirs() {
		limit, err1 := os.ReadFile(filepath.Join(dir, "memory.max"))
		cur, err2 := os.ReadFile(filepath.Join(dir, "memory.current"))
		if err1 != nil || err2 != nil {
			continue
		}
		stat, _ := os.ReadFile(filepath.Join(dir, "memory.stat")) // unreadable: count all of memory.current
		free, ok := cgroupFreeMB(limit, cur, stat)
		total, ok2 := cgroupLimitMB(limit)
		if !ok || !ok2 {
			continue
		}
		if !r.limited || free < r.free {
			r.free = free
		}
		if !r.limited || total < r.limit {
			r.limit = total
		}
		r.limited = true
	}
	return r
}

// cgroupCPUs is the smallest cgroup v2 cpu.max quota of this process's cgroup and its parents, in whole CPUs.
func cgroupCPUs() (int, bool) {
	least, found := 0, false
	for _, dir := range cgroupDirs() {
		data, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
		if err != nil {
			continue
		}
		if n, ok := parseCPUMax(data); ok && (!found || n < least) {
			least, found = n, true
		}
	}
	return least, found
}

// cgroupDirs are the cgroup v2 folders of this process, its own first, up to the root; none without cgroup v2.
func cgroupDirs() []string {
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil
	}
	p, ok := cgroupV2Path(self)
	if !ok {
		return nil
	}
	var dirs []string
	for dir := filepath.Join(cgroupRoot, p); strings.HasPrefix(dir, cgroupRoot); dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == cgroupRoot {
			break
		}
	}
	return dirs
}
