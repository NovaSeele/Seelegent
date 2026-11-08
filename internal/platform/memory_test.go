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
		if !m.Known || m.AvailPhysMB == 0 || m.AvailCommitMB == 0 {
			t.Fatalf("Windows reads physical memory and commit: %+v", m)
		}
	case "linux":
		if !m.Known || m.AvailPhysMB == 0 || m.AvailCommitMB != 0 {
			t.Fatalf("Linux reads MemAvailable, commit unknown: %+v", m)
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
		{"fixture", meminfoFixture, MemoryStatus{AvailPhysMB: 15317136 / 1024, Known: true}},
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
	if got := fromMemorystatusLevel(32*gb, 25); got != (MemoryStatus{AvailPhysMB: 8192, Known: true}) {
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
	for _, tc := range []struct {
		name, max, cur string
		free           int64
		ok             bool
	}{
		{"limited", "4294967296\n", "1073741824\n", 3072, true},
		{"no limit", "max\n", "1073741824\n", 0, false},
		{"over", "1073741824\n", "2147483648\n", 0, true},
		{"bad max", "lots\n", "1\n", 0, false},
		{"bad current", "4294967296\n", "\n", 0, false},
		{"empty", "", "", 0, false},
	} {
		if free, ok := cgroupFreeMB([]byte(tc.max), []byte(tc.cur)); free != tc.free || ok != tc.ok {
			t.Errorf("%s: %d, %v; want %d, %v", tc.name, free, ok, tc.free, tc.ok)
		}
	}
}
