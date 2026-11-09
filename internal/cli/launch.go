package cli

import (
	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// launchCommandName is the hidden subcommand the daemon starts, detached, to run one worker turn (see package
// launch). It is not meant to be typed by people.
const launchCommandName = launch.Subcommand

// newLaunchCommand is the launcher as a cobra command, so that help hides it and tests can run it through Run. The
// process the daemon starts never gets here: Execute hands `seelegent __launch` to launch.Main first, which runs
// the same code as the companion executable (cmd/seelegent-launch) with the launcher's runtime limits.
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
