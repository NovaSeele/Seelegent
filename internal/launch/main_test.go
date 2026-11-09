package launch

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// TestLimitRuntime: the launcher runs with two Ps and a 16 MiB soft memory limit, unless the environment
// sets GOMAXPROCS or GOMEMLIMIT (then the runtime already follows it and nothing changes).
func TestLimitRuntime(t *testing.T) {
	procs, limit := runtime.GOMAXPROCS(0), debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(procs)
		debug.SetMemoryLimit(limit)
	})

	LimitRuntime(func(string) string { return "" })
	if got, want := runtime.GOMAXPROCS(0), min(maxProcs, runtime.NumCPU()); got != want {
		t.Errorf("GOMAXPROCS = %d, want %d", got, want)
	}
	if got := debug.SetMemoryLimit(-1); got != memoryLimit {
		t.Errorf("memory limit = %d, want %d", got, memoryLimit)
	}

	runtime.GOMAXPROCS(procs)
	debug.SetMemoryLimit(limit)
	LimitRuntime(func(k string) string {
		return map[string]string{"GOMAXPROCS": "7", "GOMEMLIMIT": "1GiB"}[k]
	})
	if got := runtime.GOMAXPROCS(0); got != procs {
		t.Errorf("GOMAXPROCS set in the environment: %d, want it left at %d", got, procs)
	}
	if got := debug.SetMemoryLimit(-1); got != limit {
		t.Errorf("GOMEMLIMIT set in the environment: limit %d, want it left at %d", got, limit)
	}
}

// TestMainExitCodes: wrong arguments are a usage error (2); a launch folder without a spec cannot run (1). Both
// say why on stderr.
func TestMainExitCodes(t *testing.T) {
	procs, limit := runtime.GOMAXPROCS(0), debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(procs)
		debug.SetMemoryLimit(limit)
	})
	for _, args := range [][]string{nil, {Subcommand}, {"launch", "dir"}, {Subcommand, ""}, {Subcommand, "a", "b"}} {
		var stderr strings.Builder
		if got := Main(args, &stderr); got != 2 || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("Main(%q) = %d, stderr %q; want 2 and the usage", args, got, stderr.String())
		}
	}
	var stderr strings.Builder
	if got := Main([]string{Subcommand, t.TempDir()}, &stderr); got != 1 || !strings.HasPrefix(stderr.String(), "Error: ") {
		t.Errorf("Main(empty folder) = %d, stderr %q; want 1 and the error", got, stderr.String())
	}
}
