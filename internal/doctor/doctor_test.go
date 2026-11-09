package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/launchbin"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

func TestSmallChecks(t *testing.T) {
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.ListSeparator)
	if c := pathEntries([]string{"PATH=" + abs + sep + "bin" + sep + "." + sep}, runtime.GOOS); c.Status != Warn ||
		!strings.Contains(c.Message, "bin, .") {
		t.Errorf("relative PATH = %+v", c)
	}
	if c := pathEntries([]string{"Path=" + abs}, runtime.GOOS); c.Status != OK {
		t.Errorf("absolute PATH = %+v", c)
	}
	if c := dashboard(fstest.MapFS{"index.html": {Data: []byte("x")}}); c.Status != OK {
		t.Errorf("dashboard = %+v", c)
	}
	if c := dashboard(nil); c.Status != Warn {
		t.Errorf("no dashboard = %+v", c)
	}
	if c := git(func() (string, error) { return "", errors.New("not found") }, ""); c.Status != Fail {
		t.Errorf("no git = %+v", c)
	}
	// A batch wrapper first on PATH: Seelegent uses the real git, and doctor names both and warns.
	exe := filepath.Join(abs, "Git", "cmd", "git.exe")
	wrapper := filepath.Join(abs, "ServBay", "bin", "git.cmd")
	found := func() (string, error) { return exe, nil }
	if c := git(found, ""); c.Status != OK || c.Message != "git "+exe {
		t.Errorf("plain git = %+v", c)
	}
	if c := git(found, wrapper); c.Status != Warn || !strings.Contains(c.Message, "git "+exe+";") ||
		!strings.Contains(c.Message, wrapper) || !strings.Contains(c.Fix, filepath.Dir(exe)) {
		t.Errorf("git behind a wrapper = %+v", c)
	}
	if !Failed([]Check{{Status: OK}, {Status: Fail}}) || Failed([]Check{{Status: Warn}}) {
		t.Error("Failed")
	}
}

func TestCargoCheck(t *testing.T) {
	exe := func(dir, name string) {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sep := string(filepath.ListSeparator)
	proxy, plain := t.TempDir(), t.TempDir()
	exe(proxy, "rustup")
	exe(proxy, "cargo")
	exe(plain, "cargo")
	noHome := "CARGO_HOME=" + t.TempDir()

	if c, ok := cargo([]string{"PATH=" + plain, noHome}, "", runtime.GOOS); ok {
		t.Errorf("no rustup gave a check: %+v", c)
	}
	if c, ok := cargo([]string{"PATH=" + proxy + sep + plain}, "", "linux"); !ok || c.Status != OK ||
		!strings.Contains(c.Message, "rustup proxy") {
		t.Errorf("proxy first = %+v", c)
	}
	c, ok := cargo([]string{"PATH=" + plain + sep + proxy}, "", "windows")
	if !ok || c.Status != Warn || !strings.Contains(c.Message, "E0514") ||
		!strings.Contains(c.Fix, `%USERPROFILE%\.cargo\bin`) || !strings.Contains(c.Fix, plain) {
		t.Errorf("plain first = %+v", c)
	}
	onlyRustup := t.TempDir()
	exe(onlyRustup, "rustup")
	if c, ok := cargo([]string{"PATH=" + onlyRustup}, "", "linux"); !ok || c.Status != Warn ||
		!strings.Contains(c.Fix, "~/.cargo/bin") {
		t.Errorf("no cargo = %+v", c)
	}
}

func TestDataDir(t *testing.T) {
	p, err := config.NewPaths(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p, false); c.Status != Warn {
		t.Errorf("missing = %+v", c)
	}
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p, false); c.Status != OK {
		t.Errorf("writable = %+v", c)
	}
	entries, err := os.ReadDir(p.Home)
	if err != nil || len(entries) != 0 {
		t.Errorf("the probe was left behind: %v %v", entries, err)
	}
	// While its daemon runs nothing is written: the folder's modification time stays.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p.Home, old, old); err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p, true); c.Status != OK || !strings.Contains(c.Message, "in use by its daemon") {
		t.Errorf("live = %+v", c)
	}
	if st, err := os.Stat(p.Home); err != nil || !st.ModTime().Equal(old) {
		t.Errorf("the live data directory changed: %v %v", st.ModTime(), err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p2, err := config.NewPaths(file)
	if err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p2, false); c.Status != Fail {
		t.Errorf("a file = %+v", c)
	}
}

func TestResourcesCheck(t *testing.T) {
	set := config.Defaults()
	// A 16 GB Windows machine with 24 GB of commit: auto minFreeMemoryMB 2048, minFreeCommitMB 2457.
	read := func(phys, commit int64) func() platform.MemoryStatus {
		return func() platform.MemoryStatus {
			return platform.MemoryStatus{AvailPhysMB: phys, AvailCommitMB: commit, TotalPhysMB: 16384,
				TotalCommitMB: 24576, Known: true}
		}
	}
	if _, ok := resources(Options{Settings: set}, "windows", nil); ok {
		t.Fatal("no reader: the check must be skipped")
	}
	c, _ := resources(Options{Settings: set, Memory: read(8000, 12000)}, "windows", nil)
	if c.Status != OK || !strings.Contains(c.Message, "one reading") ||
		!strings.Contains(c.Message, "minFreeMemoryMB 2048, auto for this machine") ||
		!strings.Contains(c.Message, "minFreeCommitMB 2457, auto") {
		t.Errorf("enough memory: %+v", c)
	}
	// Physical below its threshold, commit fine: a range for minFreeMemoryMB and the commit hint.
	c, _ = resources(Options{Settings: set, Memory: read(1500, 5000)}, "windows", nil)
	if c.Status != Warn || !strings.Contains(c.Message, "would wait") || !strings.Contains(c.Message, "single reading") ||
		!strings.Contains(c.Fix, "minFreeMemoryMB somewhere around 512-1024 MB") ||
		!strings.Contains(c.Fix, "stronger guard") || strings.Contains(c.Fix, "minFreeCommitMB somewhere") ||
		!strings.Contains(c.Fix, "Auto goes back") {
		t.Errorf("physical low: %+v", c)
	}
	// Both low, the commit threshold set by the user: a range for each.
	set.MinFreeCommitMB = config.Int(4096)
	c, _ = resources(Options{Settings: set, Memory: read(1500, 3000)}, "windows", nil)
	if c.Status != Warn || !strings.Contains(c.Fix, "minFreeCommitMB somewhere around 1280-2048 MB") ||
		strings.Contains(c.Fix, "stronger guard") || !strings.Contains(c.Message, "minFreeCommitMB 4096, set by you") {
		t.Errorf("both low: %+v", c)
	}
	// Commit unknown (not Windows) never warns for commit; thresholds off never warn.
	c, _ = resources(Options{Settings: config.Defaults(), Memory: read(8000, 0)}, "linux", nil)
	if c.Status != OK || strings.Contains(c.Message, "commit") {
		t.Errorf("commit unknown: %+v", c)
	}
	set.MinFreeMemoryMB, set.MinFreeCommitMB, set.CriticalFreeMemoryMB = config.Int(0), config.Int(0), config.Int(0)
	if c, _ = resources(Options{Settings: set, Memory: read(100, 100)}, "windows", nil); c.Status != OK {
		t.Errorf("thresholds off: %+v", c)
	}
	c, _ = resources(Options{Settings: set, Memory: func() platform.MemoryStatus { return platform.MemoryStatus{} }},
		"windows", nil)
	if c.Status != OK || !strings.Contains(c.Message, "cannot be read") {
		t.Errorf("unknown: %+v", c)
	}
	if lo, hi := memoryRange(300); lo != 256 || hi != 256 {
		t.Errorf("memoryRange(300) = %d, %d", lo, hi)
	}
}

// TestResourcesFromDaemon: with the daemon running, the values, sources, machine and the memory reading come from
// its health, not from the settings file or this process; a daemon that does not report them, or none, falls back
// to this process's reading and values computed here.
func TestResourcesFromDaemon(t *testing.T) {
	read := func() platform.MemoryStatus {
		return platform.MemoryStatus{AvailPhysMB: 1500, TotalPhysMB: 16384, Known: true, CgroupV1: true}
	}
	v := func(n int, source string) api.EffectiveValue { return api.EffectiveValue{Value: n, Source: source} }
	live := &api.Health{
		Resources: api.EffectiveResources{MinFreeMemoryMB: v(6000, api.ResourceDerived),
			CriticalFreeMemoryMB: v(3000, api.ResourceExplicit), MinFreeCommitMB: v(0, api.ResourceAuto),
			TestParallelism: v(2, api.ResourceAuto), GateConcurrency: v(1, api.ResourceAuto)},
		Machine: api.Machine{TotalPhysMB: 16384, CPUs: 8},
		// The daemon's own reading (its cgroup v1 limit) differs from this process's.
		Memory: api.MemoryStatus{AvailPhysMB: 5000, Known: true, CgroupV1: true},
	}
	notLocal := func() platform.MemoryStatus {
		t.Error("the local reading was taken although the daemon reports one")
		return platform.MemoryStatus{}
	}
	// The settings file says off; the daemon's values win.
	set := config.Defaults()
	set.MinFreeMemoryMB = config.Int(0)
	c, _ := resources(Options{Settings: set, Memory: notLocal}, "linux", live)
	if c.Status != Warn || !strings.Contains(c.Message, "free memory 5000 MB") ||
		!strings.Contains(c.Message, "minFreeMemoryMB 6000, derived from your criticalFreeMemoryMB") ||
		!strings.Contains(c.Message, "reading and values from the running daemon (machine: 16384 MB, 8 CPUs)") ||
		!strings.Contains(c.Message, "cgroup v1") {
		t.Errorf("from the daemon: %+v", c)
	}
	// The daemon cannot read memory: said so, never replaced by this process's reading.
	unknown := *live
	unknown.Memory = api.MemoryStatus{}
	if c, _ = resources(Options{Settings: set, Memory: notLocal}, "linux", &unknown); c.Status != OK ||
		!strings.Contains(c.Message, "the daemon cannot read free memory") {
		t.Errorf("daemon reading unknown: %+v", c)
	}
	// An older daemon without resources in its health: computed here, and said so.
	c, _ = resources(Options{Settings: set, Memory: read}, "linux", &api.Health{})
	if c.Status != OK || !strings.Contains(c.Message, "minFreeMemoryMB 0, set by you") ||
		!strings.Contains(c.Message, "computed here") || !strings.Contains(c.Message, "does not report them") {
		t.Errorf("old daemon: %+v", c)
	}
	c, _ = resources(Options{Settings: config.Defaults(), Memory: read}, "linux", nil)
	if c.Status != Warn || !strings.Contains(c.Message, "minFreeMemoryMB 2048, auto for this machine") ||
		!strings.Contains(c.Message, "the daemon is not running") {
		t.Errorf("no daemon: %+v", c)
	}
}

// TestLauncherCheck: the companion launcher is information, never a warning; the message says what is used.
func TestLauncherCheck(t *testing.T) {
	for _, c := range []struct {
		st   launchbin.Status
		want string
	}{
		{launchbin.Status{Path: "/bin/seelegent-launch", Present: true, Usable: true}, "launchers run in it"},
		{launchbin.Status{Path: "/bin/seelegent-launch"}, "is not there"},
		{launchbin.Status{Path: "/bin/seelegent-launch", Present: true, Reason: "it is not of this build: x"},
			"is not used (it is not of this build: x)"},
	} {
		got := launcher(c.st)
		if got.Name != "launcher" || got.Status != OK || !strings.Contains(got.Message, c.want) {
			t.Errorf("launcher(%+v) = %+v, want OK naming %q", c.st, got, c.want)
		}
	}
}
