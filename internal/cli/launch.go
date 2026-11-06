package cli

import (
	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// launchCommandName is the hidden subcommand the daemon starts, detached, to run one worker turn (see package
// launch). It is not meant to be typed by people.
const launchCommandName = "__launch"

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
