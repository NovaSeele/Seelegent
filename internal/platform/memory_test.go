package platform

import (
	"runtime"
	"testing"
)

func TestMemoryReading(t *testing.T) {
	m := Memory()
	if m.AvailPhysMB < 0 || m.AvailCommitMB < 0 {
		t.Fatalf("negative reading: %+v", m)
	}
	if !m.Known && (m.AvailPhysMB != 0 || m.AvailCommitMB != 0) {
		t.Fatalf("an unknown reading must be all zero: %+v", m)
	}
	switch runtime.GOOS {
	case "windows":
		if !m.Known || m.AvailPhysMB == 0 || m.AvailCommitMB == 0 || m.TotalPhysMB < m.AvailPhysMB ||
			m.TotalCommitMB < m.AvailCommitMB {
			t.Fatalf("Windows reads physical memory and commit with their totals: %+v", m)
		}
	case "linux":
		if !m.Known || m.AvailPhysMB == 0 || m.AvailCommitMB != 0 || m.TotalPhysMB == 0 || m.TotalCommitMB != 0 {
			t.Fatalf("Linux reads MemAvailable and MemTotal, commit unknown: %+v", m)
		}
	}
	t.Logf("memory: %+v", m)
}

// A /proc/meminfo of a 16 GB machine (Linux 6.6, trimmed).
const meminfoFixture = `MemTotal:       16080628 kB
MemFree:        14858304 kB
MemAvailable:   15317136 kB
Buffers:           34012 kB
Cached:           607932 kB
SwapCached:            0 kB
CommitLimit:    12234456 kB
Committed_AS:     998316 kB
`

func TestParseMeminfo(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want MemoryStatus
	}{
		{"fixture", meminfoFixture, MemoryStatus{AvailPhysMB: 15317136 / 1024, TotalPhysMB: 16080628 / 1024,
			Known: true}},
		{"bad MemTotal", "MemTotal: lots kB\nMemAvailable: 2097152 kB\n", MemoryStatus{}},
		{"no MemAvailable (before Linux 3.14)", "MemTotal: 1000 kB\nMemFree: 500 kB\n", MemoryStatus{}},
		{"empty", "", MemoryStatus{}},
		{"no number", "MemAvailable:\n", MemoryStatus{}},
		{"not a number", "MemAvailable: lots kB\n", MemoryStatus{}},
		{"other unit", "MemAvailable: 1000 MB\n", MemoryStatus{}},
		{"no unit", "MemAvailable: 2097152\n", MemoryStatus{AvailPhysMB: 2048, Known: true}},
		{"prefix only", "MemAvailableX: 2097152 kB\n", MemoryStatus{}},
	} {
		if got := parseMeminfo([]byte(tc.text)); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestMemorystatusLevel(t *testing.T) {
	const gb = 1 << 30
	if got := fromMemorystatusLevel(32*gb, 25); got != (MemoryStatus{AvailPhysMB: 8192, TotalPhysMB: 32768,
		Known: true}) {
		t.Fatalf("32 GB at 25%%: %+v", got)
	}
	for _, bad := range []struct {
		size  uint64
		level uint32
	}{{0, 50}, {32 * gb, 0}, {32 * gb, 101}} {
		if got := fromMemorystatusLevel(bad.size, bad.level); got.Known {
			t.Fatalf("%+v must be unknown: %+v", bad, got)
		}
	}
}

func TestCgroupV2(t *testing.T) {
	const self = "12:pids:/user.slice\n1:name=systemd:/user.slice/x.scope\n0::/user.slice/user-1000.slice/x.scope\n"
	if p, ok := cgroupV2Path([]byte(self)); !ok || p != "/user.slice/user-1000.slice/x.scope" {
		t.Fatalf("path %q, %v", p, ok)
	}
	if p, ok := cgroupV2Path([]byte("0::/\n")); !ok || p != "/" {
		t.Fatalf("container root: %q, %v", p, ok)
	}
	if _, ok := cgroupV2Path([]byte("4:memory:/docker/abc\n")); ok {
		t.Fatal("cgroup v1 only has no v2 path")
	}
	// docker -m 8g after a day of clones and builds: memory.current is 7.9 GB, 6 GiB of it inactive page cache.
	const stat = "anon 1073741824\nfile 6979321856\nactive_file 536870912\ninactive_file 6442450944\n"
	for _, tc := range []struct {
		name, max, cur, stat string
		free                 int64
		ok                   bool
	}{
		{"limited, no stat", "4294967296\n", "1073741824\n", "", 3072, true},
		// used = 8482560000 - 6442450944 = 2040109056 bytes; free = 8589934592 - used = 6549825536 bytes = 6246 MiB.
		{"cache is free", "8589934592\n", "8482560000\n", stat, 6246, true},
		{"cache counted without stat", "8589934592\n", "8482560000\n", "", 102, true},
		{"stat without inactive_file", "4294967296\n", "1073741824\n", "anon 1\nfile 2\n", 3072, true},
		{"unparsable inactive_file", "4294967296\n", "1073741824\n", "inactive_file lots\n", 3072, true},
		{"cache above current", "4294967296\n", "1073741824\n", "inactive_file 2147483648\n", 4096, true},
		{"no limit", "max\n", "1073741824\n", stat, 0, false},
		{"over", "1073741824\n", "2147483648\n", "", 0, true},
		{"over even without cache", "1073741824\n", "8482560000\n", stat, 0, true},
		{"bad max", "lots\n", "1\n", "", 0, false},
		{"bad current", "4294967296\n", "\n", "", 0, false},
		{"empty", "", "", "", 0, false},
	} {
		var statData []byte
		if tc.stat != "" {
			statData = []byte(tc.stat)
		}
		if free, ok := cgroupFreeMB([]byte(tc.max), []byte(tc.cur), statData); free != tc.free || ok != tc.ok {
			t.Errorf("%s: %d, %v; want %d, %v", tc.name, free, ok, tc.free, tc.ok)
		}
	}
}

func TestCPUs(t *testing.T) {
	if n := CPUs(); n < 1 || n > runtime.NumCPU() {
		t.Fatalf("CPUs = %d (NumCPU %d)", n, runtime.NumCPU())
	}
	for _, tc := range []struct {
		data string
		n    int
		ok   bool
	}{
		{"200000 100000\n", 2, true},
		{"150000 100000", 2, true}, // 1.5 CPUs round up
		{"50000 100000", 1, true},
		{"max 100000\n", 0, false},
		{"", 0, false},
		{"lots 100000", 0, false},
		{"100000 0", 0, false},
	} {
		if n, ok := parseCPUMax([]byte(tc.data)); n != tc.n || ok != tc.ok {
			t.Errorf("parseCPUMax(%q) = %d, %v; want %d, %v", tc.data, n, ok, tc.n, tc.ok)
		}
	}
	for _, tc := range []struct {
		data string
		mb   int64
		ok   bool
	}{{"4294967296\n", 4096, true}, {"max\n", 0, false}, {"x", 0, false}} {
		if mb, ok := cgroupLimitMB([]byte(tc.data)); mb != tc.mb || ok != tc.ok {
			t.Errorf("cgroupLimitMB(%q) = %d, %v", tc.data, mb, ok)
		}
	}
}
