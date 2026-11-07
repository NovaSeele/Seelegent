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
			"review history, worker roles, department members, lead hooks and meeting records of the old\n" +
			"Seelegent into this version. The old folder is never changed. Running it again reports\n" +
			"\"unchanged\" for what was imported before; data this version already holds under the same name\n" +
			"is kept. Tasks (and their review notes), contests, automations and proof records are not\n" +
			"imported. Stop the daemon first.",
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
