package launch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Subcommand is the argument that runs the launcher: `seelegent __launch <dir>` and `seelegent-launch __launch
// <dir>` (the companion executable, see Main). It is not meant to be typed by people.
const Subcommand = "__launch"

// Runtime limits of the launcher, which runs once per running task for the whole turn and only waits: a few
// goroutines, a timer, small files. Two Ps instead of one per CPU keep the runtime's per-P caches and idle threads
// small (measured on 24 CPUs: 13 threads and about 7 MB private working set before, 7 threads and about 6 MB
// after), and the soft memory limit makes the collector keep the heap near what the launcher uses. Both apply only
// when the environment does not set GOMAXPROCS or GOMEMLIMIT itself.
//
// The launcher also returns the garbage of the package initializers (about 2 MB allocated when it runs as the full
// seelegent binary) to the system right away (LimitRuntime), instead of keeping those heap pages committed for the
// turn.
//
// What the full binary costs on top (measured on Windows, about 51 MB private per launcher): 32 MiB are the
// zero-filled entropy buffer of crypto/internal/fips140/drbg in the image's .bss, which Windows charges as commit
// although it is never touched, and which every binary that imports any crypto package links (drbg's initializer
// refers to it). The companion executable cmd/seelegent-launch links no crypto package and runs the launcher in
// about 12 MB private; the daemon uses it when it sits next to the daemon's executable and is of the same build
// (package launchbin). The rest is the Go runtime: 8 MiB of heap arena index, which every Go program on Windows
// commits, and the heap and the runtime's own structures.
const (
	maxProcs    = 2
	memoryLimit = 16 << 20
)

// LimitRuntime applies the launcher's runtime limits and then returns the initializers' garbage to the system (a
// collection and a scavenge, a millisecond or two). Main calls it first thing, before the runtime has started
// threads for more Ps (the first collection starts a mark worker per P). It changes the runtime, not the
// environment, which the launcher passes on to the worker unchanged.
func LimitRuntime(getenv func(string) string) {
	if getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(min(maxProcs, runtime.NumCPU()))
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(memoryLimit)
	}
	debug.FreeOSMemory()
}

// Main is the launcher process: args are its arguments without the program name, Subcommand and the launch
// directory. `seelegent __launch` and `seelegent-launch __launch` both run it, so the two run the same code. It
// applies the runtime limits, runs the launch (Run) until it ends or the process gets a stop signal (then the
// worker tree is stopped and the exit record written) and returns the exit code: 0 when the launch ran (how the
// worker ended is in the exit record), 1 when it could not run, 2 for wrong arguments.
func Main(args []string, stderr io.Writer) int {
	LimitRuntime(os.Getenv)
	if len(args) != 2 || args[0] != Subcommand || args[1] == "" {
		_, _ = fmt.Fprintf(stderr, "usage: %s <launch directory> (started by the Seelegent daemon)\n", Subcommand)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), platform.StopSignals()...)
	defer stop()
	if err := Run(ctx, args[1]); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %s\n", err)
		return 1
	}
	return 0
}
