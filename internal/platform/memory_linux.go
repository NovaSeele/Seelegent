//go:build linux

package platform

import "os"

// memoryStatus reads MemAvailable and MemTotal from /proc/meminfo and lowers them to what the cgroup memory limits
// of this process (its own group and every parent, inside a container or a systemd slice with MemoryMax; cgroup
// v2, else v1) allow. Commit stays unknown (0).
func memoryStatus() MemoryStatus {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryStatus{}
	}
	m := parseMeminfo(data)
	if !m.Known {
		return m
	}
	room := cgroupFS{root: "/"}.memory()
	if room.limited && room.free < m.AvailPhysMB {
		m.AvailPhysMB, m.CgroupV1 = room.free, room.v1
	}
	if room.limited && room.limit < m.TotalPhysMB {
		m.TotalPhysMB, m.CgroupV1 = room.limit, room.v1
	}
	return m
}

// cgroupCPUs is the smallest cgroup CPU quota of this process's cgroup and its parents, in whole CPUs.
func cgroupCPUs() (int, bool) { return cgroupFS{root: "/"}.cpus() }
