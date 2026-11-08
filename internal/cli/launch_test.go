package cli

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// TestLimitLauncherRuntime: the launcher runs with two Ps and a 16 MiB soft memory limit, unless the environment
// sets GOMAXPROCS or GOMEMLIMIT (then the runtime already follows it and nothing changes).
func TestLimitLauncherRuntime(t *testing.T) {
	procs, limit := runtime.GOMAXPROCS(0), debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(procs)
		debug.SetMemoryLimit(limit)
	})

	limitLauncherRuntime(func(string) string { return "" })
	if got, want := runtime.GOMAXPROCS(0), min(launcherMaxProcs, runtime.NumCPU()); got != want {
		t.Errorf("GOMAXPROCS = %d, want %d", got, want)
	}
	if got := debug.SetMemoryLimit(-1); got != launcherMemoryLimit {
		t.Errorf("memory limit = %d, want %d", got, launcherMemoryLimit)
	}

	runtime.GOMAXPROCS(procs)
	debug.SetMemoryLimit(limit)
	limitLauncherRuntime(func(k string) string {
		return map[string]string{"GOMAXPROCS": "7", "GOMEMLIMIT": "1GiB"}[k]
	})
	if got := runtime.GOMAXPROCS(0); got != procs {
		t.Errorf("GOMAXPROCS set in the environment: %d, want it left at %d", got, procs)
	}
	if got := debug.SetMemoryLimit(-1); got != limit {
		t.Errorf("GOMEMLIMIT set in the environment: limit %d, want it left at %d", got, limit)
	}
}
