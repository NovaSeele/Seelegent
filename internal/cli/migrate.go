package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/migrate"
)

func newMigrateCommand() *cobra.Command {
	var opts migrate.Options
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Import the data of the old (Node) Seelegent",
		Long: "Import settings, providers, accounts (with their profile folders and API keys), project memory,\n" +
			"review history, worker roles, department members, lead hooks, meeting records and automations\n" +
			"(every one switched off: turn each on from the dashboard) of the old Seelegent into this version.\n" +
			"The old folder is never changed. Running it again reports \"unchanged\" for what was imported\n" +
			"before; data this version already holds under the same name is kept. Not imported: tasks (and\n" +
			"their review notes), contests, the run history of automations, the GitHub folder (repositories,\n" +
			"issues, tickets and the token), proof records, usage history and quota caches. Stop the daemon\n" +
			"first.",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "migrate"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return err
			}
			opts.Paths = paths
			if opts.From == "" {
				opts.From = paths.Home // the old version used the same folder
			}
			if opts.RealHome, err = os.UserHomeDir(); err != nil {
				return err
			}
			rep, err := migrate.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return printSetupReport(cmd, rep, rep.WriteText, rep.Failed(), "some items could not be imported")
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.From, "from", "", "the old data folder (default: the data folder, $SEELEGENT_HOME or ~/.seelegent)")
	f.BoolVar(&opts.DryRun, "dry-run", false, "show what would be imported without writing anything")
	return cmd
}
