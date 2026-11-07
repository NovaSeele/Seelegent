package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// versionOut is the --json answer of `seelegent version`.
type versionOut struct {
	buildinfo.Info
	// SchemaVersion is the number of database migrations in this build: the newest database it can open. The
	// updater and the installers refuse to install a build that knows fewer migrations than the database has.
	SchemaVersion int `json:"schemaVersion"`
	// DatabaseSchemaVersion is the data folder's database schema, with --check-database (0 when there is no
	// database yet).
	DatabaseSchemaVersion *int `json:"databaseSchemaVersion,omitempty"`
}

func newVersionCommand(info buildinfo.Info) *cobra.Command {
	var checkDB bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Long: "Print the version, commit and build date. --json adds schemaVersion, the number of database\n" +
			"migrations this build has. --check-database also reads the data folder's database (read-only) and fails\n" +
			"with code schema_too_new when this build cannot open it (the installers use it before installing).",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			known, err := store.KnownSchemaVersion()
			if err != nil {
				return err
			}
			out := versionOut{Info: info, SchemaVersion: known}
			if checkDB {
				db, err := databaseSchema(cmd)
				if err != nil {
					return err
				}
				out.DatabaseSchemaVersion = &db
				if db > known {
					return &codedError{code: codeSchemaTooNew, err: fmt.Errorf("%w: the database is at version %d, "+
						"this build (%s) knows %d", store.ErrSchemaTooNew, db, info.Version, known)}
				}
			}
			return emit(cmd, out, func(w io.Writer) {
				_, _ = fmt.Fprintln(w, info.String())
				if out.DatabaseSchemaVersion != nil {
					_, _ = fmt.Fprintf(w, "database schema %d (this build knows %d)\n", *out.DatabaseSchemaVersion, known)
				}
			})
		},
	}
	cmd.Flags().BoolVar(&checkDB, "check-database", false,
		"also check that this build can open the data folder's database (read-only)")
	return cmd
}

// databaseSchema reads the schema version of the data folder's database without changing it (0 when there is no
// database yet).
func databaseSchema(cmd *cobra.Command) (int, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return 0, err
	}
	in, err := store.Inspect(cmd.Context(), paths.DB)
	switch {
	case store.IsNotExist(err):
		return 0, nil
	case err != nil && !errors.Is(err, store.ErrSchemaTooNew):
		return 0, err
	}
	return in.Version, nil
}
