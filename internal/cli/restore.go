package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// newRestoreCommand is `seelegent restore <id> --turn N [--file=<path>]...`: it puts back the content files had
// when a turn started (the command a report's work_at_risk names).
func newRestoreCommand(d deps) *cobra.Command {
	var (
		turn  int
		files []string
	)
	cmd := &cobra.Command{
		Use:   "restore <id> --turn N [--file=<path>]...",
		Short: "Put back files a task's turn may have lost (default: its files at risk)",
		Long: "Writes back the content the files had when the turn started, from the snapshot Seelegent took then. " +
			"Paths are relative to the repository root. Without --file every work-at-risk file of the turn that was " +
			"not restored yet is restored. The current content is kept in a backup snapshot first.",
		Args:        rangeArgs(1, 1, "seelegent restore <id> --turn N [--file=<path>]..."),
		Annotations: map[string]string{annOp: "restore"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if turn < 1 {
				return usagef("--turn must be a turn number from 1")
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.Restore(cmd.Context(), args[0], turn, api.RestoreRequest{Files: files})
			if err != nil {
				return err
			}
			return emit(cmd, res, func(w io.Writer) {
				for _, p := range res.Restored {
					_, _ = fmt.Fprintf(w, "restored  %s\n", p)
				}
				for _, s := range res.Skipped {
					_, _ = fmt.Fprintf(w, "skipped   %s: %s\n", s.Path, s.Error)
				}
				if len(res.Restored) == 0 && len(res.Skipped) == 0 {
					_, _ = fmt.Fprintln(w, "Nothing to restore.")
				}
			})
		},
	}
	cmd.Flags().IntVar(&turn, "turn", 0, "the turn whose start state to restore (required)")
	cmd.Flags().StringArrayVar(&files, "file", nil, "a file to restore, relative to the repository root (repeatable)")
	return cmd
}
