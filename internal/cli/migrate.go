package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/migrate"
)

func newMigrateCommand() *cobra.Command {
	var (
		opts   migrate.Options
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Import the data of the old (Node) Seelegent",
		Long: "Import settings, providers, accounts (with their profile folders and API keys), project memory,\n" +
			"review history and worker roles of the old Seelegent into this version. The old folder is never\n" +
			"changed. Running it again reports \"unchanged\" for what was imported before; data this version\n" +
			"already holds under the same name is kept. Tasks, members, notes, meetings, contests,\n" +
			"automations, hooks and proof records are not imported. Stop the daemon first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Getenv(account.EnvWorker) == "1" {
				return errSetupInWorker
			}
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
			if err := printSetupReport(cmd.OutOrStdout(), asJSON, rep, rep.WriteText); err != nil {
				return err
			}
			if rep.Failed() {
				return errors.New("some items could not be imported")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.From, "from", "", "the old data folder (default: the data folder, $SEELEGENT_HOME or ~/.seelegent)")
	f.BoolVar(&opts.DryRun, "dry-run", false, "show what would be imported without writing anything")
	f.BoolVar(&asJSON, "json", false, "print the report as JSON")
	return cmd
}
