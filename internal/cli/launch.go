package cli

import (
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// launchCommandName is the hidden subcommand the daemon starts, detached, to run one worker turn (see package
// launch). It is not meant to be typed by people.
const launchCommandName = "__launch"

// Runtime limits of the launcher, which runs once per running task for the whole turn and only waits: a few
// goroutines, a timer, small files. Two Ps instead of one per CPU keep the runtime's per-P caches and idle threads
// small (measured on 24 CPUs: 13 threads and about 7 MB private working set before, 7 threads and about 6 MB
// after), and the soft memory limit makes the collector keep the heap near what the launcher uses. Both apply only
// when the environment does not set GOMAXPROCS or GOMEMLIMIT itself.
//
// Most of the rest is not the launcher's to save: about 10 MB of its working set are pages of the executable,
// shared with the daemon and the other launchers, and 32 MiB of its private bytes are the zero-filled entropy
// buffer of crypto/internal/fips140/drbg in the image's .bss, which Windows charges as commit although it is
// never touched.
const (
	launcherMaxProcs    = 2
	launcherMemoryLimit = 16 << 20
)

func newLaunchCommand() *cobra.Command {
	return &cobra.Command{
		Use:    launchCommandName + " <dir>",
		Short:  "Run one worker turn described by a launch directory (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return launch.Run(cmd.Context(), args[0])
		},
	}
}

// isLaunch reports whether args (without the program name) run the launcher, as the daemon starts it.
func isLaunch(args []string) bool { return len(args) > 0 && args[0] == launchCommandName }

// limitLauncherRuntime applies the launcher's runtime limits. Execute calls it first thing, before the runtime
// has started threads for more Ps (the first collection starts a mark worker per P). It changes the runtime, not
// the environment, which the launcher passes on to the worker unchanged.
func limitLauncherRuntime(getenv func(string) string) {
	if getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(min(launcherMaxProcs, runtime.NumCPU()))
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(launcherMemoryLimit)
	}
}
