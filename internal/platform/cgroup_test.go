package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeRoot writes files under a temp root: path (slash separated, relative to the root) to content.
func fakeRoot(t *testing.T, files map[string]string) cgroupFS {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cgroupFS{root: root}
}

const (
	gib       = "1073741824"
	unlimited = "9223372036854771712"
)

func TestCgroupFSV2(t *testing.T) {
	c := fakeRoot(t, map[string]string{
		"proc/self/cgroup": "0::/user.slice/a.scope\n",
		// The process's own group has no limit; its parent allows 4 GiB with 1 GiB used, 512 MiB of it inactive
		// page cache.
		"sys/fs/cgroup/user.slice/a.scope/memory.max":     "max\n",
		"sys/fs/cgroup/user.slice/a.scope/memory.current": "1000\n",
		"sys/fs/cgroup/user.slice/a.scope/cpu.max":        "150000 100000\n",
		"sys/fs/cgroup/user.slice/memory.max":             "4294967296\n",
		"sys/fs/cgroup/user.slice/memory.current":         gib + "\n",
		"sys/fs/cgroup/user.slice/memory.stat":            "anon 1\ninactive_file 536870912\n",
		"sys/fs/cgroup/user.slice/cpu.max":                "max 100000\n",
		// v1 files on the same host are never read while v2 gives a reading.
		"sys/fs/cgroup/memory/memory.limit_in_bytes": "1048576\n",
		"sys/fs/cgroup/memory/memory.usage_in_bytes": "0\n",
	})
	if r := c.memory(); r != (cgroupRoom{free: 3584, limit: 4096, limited: true}) {
		t.Errorf("memory = %+v", r)
	}
	if n, ok := c.cpus(); !ok || n != 2 {
		t.Errorf("cpus = %d, %v", n, ok)
	}
}

func TestCgroupFSV1(t *testing.T) {
	// A hybrid host: the v2 entry has no controllers, so the v1 memory and cpu hierarchies hold the limits.
	c := fakeRoot(t, map[string]string{
		"proc/self/cgroup": "12:memory:/docker/abc\n4:cpu,cpuacct:/docker/abc\n1:name=systemd:/docker/abc\n0::/\n",
		"sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes": "2147483648\n",
		"sys/fs/cgroup/memory/docker/abc/memory.usage_in_bytes": "1610612736\n",
		"sys/fs/cgroup/memory/docker/abc/memory.stat":           "cache 9\ninactive_file 1\ntotal_inactive_file 536870912\n",
		"sys/fs/cgroup/memory/memory.limit_in_bytes":            unlimited + "\n", // "unlimited": no limit
		"sys/fs/cgroup/memory/memory.usage_in_bytes":            gib + "\n",
		// No folder for the group itself (a container without its own cgroup namespace sees it at the mount).
		"sys/fs/cgroup/cpu,cpuacct/cpu.cfs_quota_us":  "250000\n",
		"sys/fs/cgroup/cpu,cpuacct/cpu.cfs_period_us": "100000\n",
	})
	if r := c.memory(); r != (cgroupRoom{free: 1024, limit: 2048, limited: true, v1: true}) {
		t.Errorf("memory = %+v", r)
	}
	if n, ok := c.cpus(); !ok || n != 3 {
		t.Errorf("cpus = %d, %v", n, ok)
	}
}

func TestCgroupFSV1NoLimits(t *testing.T) {
	c := fakeRoot(t, map[string]string{
		"proc/self/cgroup":                           "5:memory:/\n3:cpu:/\n",
		"sys/fs/cgroup/memory/memory.limit_in_bytes": unlimited + "\n",
		"sys/fs/cgroup/memory/memory.usage_in_bytes": gib + "\n",
		"sys/fs/cgroup/cpu/cpu.cfs_quota_us":         "-1\n",
		"sys/fs/cgroup/cpu/cpu.cfs_period_us":        "100000\n",
	})
	if r := c.memory(); r.limited {
		t.Errorf("memory = %+v, want no limit", r)
	}
	if n, ok := c.cpus(); ok {
		t.Errorf("cpus = %d, want no quota", n)
	}
	// No cgroup file at all (macOS, a minimal container): nothing.
	if r := (cgroupFS{root: t.TempDir()}).memory(); r.limited {
		t.Errorf("empty root: %+v", r)
	}
}

func TestCgroupV1Parsers(t *testing.T) {
	// The working set over the limit leaves 0; without total_inactive_file, inactive_file counts.
	if free, limit, ok := cgroupV1FreeMB([]byte("1048576"), []byte("2097152"), []byte("inactive_file 0")); !ok ||
		free != 0 || limit != 1 {
		t.Errorf("over the limit: %d %d %v", free, limit, ok)
	}
	if free, _, ok := cgroupV1FreeMB([]byte("2097152"), []byte("2097152"), []byte("inactive_file 1048576")); !ok ||
		free != 1 {
		t.Errorf("inactive_file: %d %v", free, ok)
	}
	for _, bad := range [][2]string{{"x", "1"}, {"1", ""}, {unlimited, "1"}} {
		if _, _, ok := cgroupV1FreeMB([]byte(bad[0]), []byte(bad[1]), nil); ok {
			t.Errorf("%q: a reading", bad)
		}
	}
	for _, c := range []struct {
		quota, period string
		want          int
		ok            bool
	}{{"100000", "100000", 1, true}, {"50000", "100000", 1, true}, {"-1", "100000", 0, false},
		{"100000", "0", 0, false}, {"x", "100000", 0, false}} {
		if n, ok := parseCFSQuota([]byte(c.quota), []byte(c.period)); n != c.want || ok != c.ok {
			t.Errorf("parseCFSQuota(%s, %s) = %d, %v", c.quota, c.period, n, ok)
		}
	}
	if ctrls, p, ok := cgroupV1Path([]byte("4:cpu,cpuacct:/a\n0::/b\n"), "cpuacct"); !ok || ctrls != "cpu,cpuacct" ||
		p != "/a" {
		t.Errorf("cgroupV1Path = %q %q %v", ctrls, p, ok)
	}
	if _, _, ok := cgroupV1Path([]byte("0::/b\n"), "memory"); ok {
		t.Error("a v2-only host has no v1 path")
	}
}
