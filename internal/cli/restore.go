package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// newRestoreCommand is `seelegent restore <id> --turn N [--file=<path>]... [--force]`: it puts back the content
// files had when a turn started (the command a report's work_at_risk names, never with --force).
func newRestoreCommand(d deps) *cobra.Command {
	var (
		turn  int
		files []string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "restore <id> --turn N [--file=<path>]... [--force]",
		Short: "Put back files a task's turn may have lost (default: its files at risk)",
		Long: "Writes back the content the files had when the turn started, from the snapshot Seelegent took then. " +
			"Paths are relative to the repository root. Without --file every work-at-risk file of the turn that was " +
			"not restored yet is restored. A file someone changed after the turn ended is skipped unless --force. " +
			"The current content is kept in a backup snapshot first; its name is printed.",
		Args:        rangeArgs(1, 1, "seelegent restore <id> --turn N [--file=<path>]... [--force]"),
		Annotations: map[string]string{annOp: "restore"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if turn < 1 {
				return usagef("--turn must be a turn number from 1")
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			res, err := c.Restore(cmd.Context(), args[0], turn, api.RestoreRequest{Files: files, Force: force})
			if err != nil {
				return err
			}
			return emit(cmd, res, func(w io.Writer) {
				for _, p := range res.Restored {
					_, _ = fmt.Fprintf(w, "restored  %s\n", p)
				}
				changed := false
				for _, s := range res.Skipped {
					_, _ = fmt.Fprintf(w, "skipped   %s: %s\n", s.Path, s.Error)
					changed = changed || s.Code == api.RestoreChangedSinceTurn
				}
				if res.Backup != "" {
					_, _ = fmt.Fprintf(w, "backup    %s (what the restored files held before)\n", res.Backup)
				}
				if changed {
					_, _ = fmt.Fprintln(w, "Add --force to overwrite the files changed since the turn ended.")
				}
				if len(res.Restored) == 0 && len(res.Skipped) == 0 {
					_, _ = fmt.Fprintln(w, "Nothing to restore.")
				}
			})
		},
	}
	cmd.Flags().IntVar(&turn, "turn", 0, "the turn whose start state to restore (required)")
	cmd.Flags().StringArrayVar(&files, "file", nil, "a file to restore, relative to the repository root (repeatable)")
	cmd.Flags().BoolVar(&force, "force", false, "also overwrite files that changed after the turn ended")
	return cmd
}
