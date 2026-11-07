package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
)

func newVersionCommand(info buildinfo.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return emit(cmd, info, func(w io.Writer) { _, _ = fmt.Fprintln(w, info.String()) })
		},
	}
}
