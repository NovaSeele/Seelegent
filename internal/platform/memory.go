package platform

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// MemoryStatus is the free memory of the machine. Known is false when the system gave no reading; the numbers are
// 0 then, and a caller must never hold work back on an unknown reading.
type MemoryStatus struct {
	// AvailPhysMB is the physical memory available to new processes, in MiB (Windows: ullAvailPhys of
	// GlobalMemoryStatusEx; Linux: MemAvailable of /proc/meminfo, or less when the cgroup v2 memory limit of the
	// daemon leaves less; macOS: best effort).
	AvailPhysMB int64
	// AvailCommitMB is the commit still available (RAM plus page file), in MiB: Windows only (ullAvailPageFile of
	// GlobalMemoryStatusEx); 0 elsewhere, where commit is unknown. A known reading with AvailCommitMB 0 therefore
	// means "commit unknown", never "no commit left": callers check commit only when it is above 0.
	AvailCommitMB int64
	// Known is true when the reading succeeded.
	Known bool
}

// Memory reads the free memory of the machine. It is cheap (one system call or one small file read) but not
// free: callers that check often cache it.
func Memory() MemoryStatus { return memoryStatus() }

// mib converts bytes to MiB.
func mib(n uint64) int64 { return int64(n >> 20) }

// parseMeminfo reads MemAvailable from the text of /proc/meminfo ("MemAvailable:   12345678 kB"). Kernels before
// 3.14 have no MemAvailable: the reading is unknown then (an estimate from MemFree would hold work back for page
// cache the kernel can drop). Commit is unknown on Linux (CommitLimit only applies under overcommit mode 2).
func parseMeminfo(data []byte) MemoryStatus {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || name != "MemAvailable" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return MemoryStatus{}
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return MemoryStatus{}
		}
		if len(f) > 1 && f[1] != "kB" {
			return MemoryStatus{} // the kernel has always written kB; anything else is not understood
		}
		return MemoryStatus{AvailPhysMB: int64(kb >> 10), Known: true}
	}
	return MemoryStatus{}
}

// cgroupV2Path returns the cgroup v2 path of a process from its /proc/<pid>/cgroup ("0::/user.slice/x.scope"); false
// without a v2 entry (cgroup v1 only).
func cgroupV2Path(data []byte) (string, bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok && strings.HasPrefix(p, "/") {
			return p, true
		}
	}
	return "", false
}

// cgroupFreeMB is the memory a cgroup v2 still allows, in MiB: memory.max minus the group's working set, at least
// 0. The working set is memory.current minus the inactive_file of memory.stat, as Kubernetes and `docker stats`
// count it: memory.current includes page cache, which a group under no pressure keeps, so after a day of clones
// and builds it sits near memory.max while most of it could be dropped at once. A missing or unreadable
// memory.stat (statData nil or without inactive_file) counts the whole of memory.current. False when there is no
// limit ("max") or memory.max or memory.current does not parse.
func cgroupFreeMB(maxData, currentData, statData []byte) (int64, bool) {
	limit := strings.TrimSpace(string(maxData))
	if limit == "" || limit == "max" {
		return 0, false
	}
	maxBytes, err1 := strconv.ParseUint(limit, 10, 64)
	used, err2 := strconv.ParseUint(strings.TrimSpace(string(currentData)), 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	used -= min(used, inactiveFile(statData))
	if used >= maxBytes {
		return 0, true
	}
	return mib(maxBytes - used), true
}

// inactiveFile reads the inactive_file bytes of a cgroup v2 memory.stat; 0 when it is not there.
func inactiveFile(stat []byte) uint64 {
	for line := range strings.SplitSeq(string(stat), "\n") {
		if v, ok := strings.CutPrefix(line, "inactive_file "); ok {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
				return n
			}
			return 0
		}
	}
	return 0
}

// fromMemorystatusLevel is macOS's best-effort reading: hw.memsize (bytes of RAM) times kern.memorystatus_level
// (the percentage of memory the kernel counts as available, the same figure as Activity Monitor's memory
// pressure). A level outside 1-100 or no RAM size is unknown.
func fromMemorystatusLevel(memsize uint64, level uint32) MemoryStatus {
	if memsize == 0 || level == 0 || level > 100 {
		return MemoryStatus{}
	}
	return MemoryStatus{AvailPhysMB: mib(memsize * uint64(level) / 100), Known: true}
}
