package platform

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// cgroupFS reads the cgroup limits of this process under a root folder: "/" on Linux, a folder of fake files in
// tests. It reads root/proc/self/cgroup and the groups under root/sys/fs/cgroup. cgroup v2 comes first; the v1
// files are read only when no v2 level gave a reading (a v1-only or hybrid host).
type cgroupFS struct {
	root string
}

// cgroupRoom is what the cgroup memory limits of this process allow: the smallest limit and the smallest room left
// under one, in MiB (limited false when no level has a limit); v1 when they came from cgroup v1.
type cgroupRoom struct {
	free, limit int64
	limited     bool
	v1          bool
}

// add counts one level's limit and room.
func (r *cgroupRoom) add(free, limit int64) {
	if !r.limited || free < r.free {
		r.free = free
	}
	if !r.limited || limit < r.limit {
		r.limit = limit
	}
	r.limited = true
}

func (c cgroupFS) read(rel ...string) ([]byte, error) {
	return os.ReadFile(filepath.Join(append([]string{c.root}, rel...)...))
}

// base is the folder cgroups are mounted under.
func (c cgroupFS) base() string { return filepath.Join(c.root, "sys", "fs", "cgroup") }

// memory walks this process's cgroup and its parents: each v2 level's memory.max and the room left under it
// (cgroupFreeMB), or, without any v2 reading, each v1 level's memory.limit_in_bytes and the room left under it
// (cgroupV1FreeMB).
func (c cgroupFS) memory() cgroupRoom {
	var r cgroupRoom
	for _, dir := range c.v2Dirs() {
		limit, err1 := os.ReadFile(filepath.Join(dir, "memory.max"))
		cur, err2 := os.ReadFile(filepath.Join(dir, "memory.current"))
		if err1 != nil || err2 != nil {
			continue
		}
		stat, _ := os.ReadFile(filepath.Join(dir, "memory.stat")) // unreadable: count all of memory.current
		free, ok := cgroupFreeMB(limit, cur, stat)
		total, ok2 := cgroupLimitMB(limit)
		if ok && ok2 {
			r.add(free, total)
		}
	}
	if r.limited {
		return r
	}
	for _, dir := range c.v1Dirs("memory") {
		limit, err1 := os.ReadFile(filepath.Join(dir, "memory.limit_in_bytes"))
		usage, err2 := os.ReadFile(filepath.Join(dir, "memory.usage_in_bytes"))
		if err1 != nil || err2 != nil {
			continue
		}
		stat, _ := os.ReadFile(filepath.Join(dir, "memory.stat"))
		if free, total, ok := cgroupV1FreeMB(limit, usage, stat); ok {
			r.add(free, total)
			r.v1 = true
		}
	}
	return r
}

// cpus is the smallest CPU quota of this process's cgroup and its parents, in whole CPUs: cgroup v2 cpu.max, or,
// without any v2 quota file, cgroup v1 cpu.cfs_quota_us / cpu.cfs_period_us.
func (c cgroupFS) cpus() (int, bool) {
	least, found, v2Files := 0, false, false
	note := func(n int) {
		if !found || n < least {
			least, found = n, true
		}
	}
	for _, dir := range c.v2Dirs() {
		data, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
		if err != nil {
			continue
		}
		v2Files = true
		if n, ok := parseCPUMax(data); ok {
			note(n)
		}
	}
	if v2Files {
		return least, found
	}
	for _, dir := range c.v1Dirs("cpu") {
		quota, err1 := os.ReadFile(filepath.Join(dir, "cpu.cfs_quota_us"))
		period, err2 := os.ReadFile(filepath.Join(dir, "cpu.cfs_period_us"))
		if err1 != nil || err2 != nil {
			continue
		}
		if n, ok := parseCFSQuota(quota, period); ok {
			note(n)
		}
	}
	return least, found
}

// v2Dirs are the cgroup v2 folders of this process, its own first, up to the mount; none without cgroup v2.
func (c cgroupFS) v2Dirs() []string {
	self, err := c.read("proc", "self", "cgroup")
	if err != nil {
		return nil
	}
	p, ok := cgroupV2Path(self)
	if !ok {
		return nil
	}
	return upTo(c.base(), p)
}

// v1Dirs are the cgroup v1 folders of this process for one controller (memory, cpu), its own first, up to the
// controller's mount (sys/fs/cgroup/<controllers as listed, "cpu,cpuacct">, else sys/fs/cgroup/<controller>).
// Folders that do not exist (a container without its own cgroup namespace sees its group at the mount itself) are
// skipped by the readers.
func (c cgroupFS) v1Dirs(controller string) []string {
	self, err := c.read("proc", "self", "cgroup")
	if err != nil {
		return nil
	}
	ctrls, p, ok := cgroupV1Path(self, controller)
	if !ok {
		return nil
	}
	mount := filepath.Join(c.base(), ctrls)
	if _, err := os.Stat(mount); err != nil {
		mount = filepath.Join(c.base(), controller)
	}
	return upTo(mount, p)
}

// upTo is the folder mount/p and its parents up to mount itself.
func upTo(mount, p string) []string {
	var dirs []string
	for dir := filepath.Join(mount, p); strings.HasPrefix(dir, mount); dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == mount {
			break
		}
	}
	return dirs
}

// cgroupV1Path returns the controllers field ("cpu,cpuacct") and the path of a cgroup v1 hierarchy that holds the
// controller, from a /proc/<pid>/cgroup ("4:memory:/docker/abc"); false without one.
func cgroupV1Path(data []byte, controller string) (ctrls, path string, ok bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(f) != 3 || f[0] == "0" || !strings.HasPrefix(f[2], "/") {
			continue
		}
		if slices.Contains(strings.Split(f[1], ","), controller) {
			return f[1], f[2], true
		}
	}
	return "", "", false
}

// cgroupV1Unlimited is the smallest memory.limit_in_bytes read as no limit: the kernel writes a value near 2^63
// (PAGE_COUNTER_MAX pages) for "unlimited".
const cgroupV1Unlimited = 1 << 60

// cgroupV1FreeMB is the memory a cgroup v1 level still allows and its limit, in MiB: memory.limit_in_bytes minus
// the working set, at least 0. The working set is memory.usage_in_bytes minus total_inactive_file of memory.stat
// (inactive_file when the total is not there), as for v2 (cgroupFreeMB). False without a limit (the huge
// "unlimited" value) or when the files do not parse.
func cgroupV1FreeMB(limitData, usageData, statData []byte) (free, limit int64, ok bool) {
	maxBytes, err1 := strconv.ParseUint(strings.TrimSpace(string(limitData)), 10, 64)
	used, err2 := strconv.ParseUint(strings.TrimSpace(string(usageData)), 10, 64)
	if err1 != nil || err2 != nil || maxBytes >= cgroupV1Unlimited {
		return 0, 0, false
	}
	inactive, found := statValue(statData, "total_inactive_file")
	if !found {
		inactive, _ = statValue(statData, "inactive_file")
	}
	used -= min(used, inactive)
	if used >= maxBytes {
		return 0, mib(maxBytes), true
	}
	return mib(maxBytes - used), mib(maxBytes), true
}

// parseCFSQuota reads cgroup v1 cpu.cfs_quota_us and cpu.cfs_period_us as whole CPUs, rounded up; false without a
// quota (-1) or when they do not parse.
func parseCFSQuota(quotaData, periodData []byte) (int, bool) {
	// Both fit in 31 bits so the sum and the result fit any int (the kernel keeps them far below).
	quota, err1 := strconv.ParseInt(strings.TrimSpace(string(quotaData)), 10, 32)
	period, err2 := strconv.ParseInt(strings.TrimSpace(string(periodData)), 10, 32)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return int((quota + period - 1) / period), true
}
