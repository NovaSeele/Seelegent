package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
)

const memoryUsage = "seelegent memory read|append|replace --cwd <dir> [text|@file]"

func newMemoryCommand(d deps) *cobra.Command {
	var cwdFlag string
	cmd := &cobra.Command{
		Use:   "memory read|append|replace --cwd <dir> [text|@file]",
		Short: "Read or change a project's notes for workers",
		Long: "Project notes are added to every worker brief of that project. append and replace need text;\n" +
			"@file reads it from a file relative to --cwd.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usagef("usage: %s", memoryUsage)
			}
			switch args[0] {
			case "read":
				if len(args) > 1 {
					return usagef("memory read takes no text; usage: %s", memoryUsage)
				}
			case "append", "replace":
			default:
				return usagef("unknown memory action %q; usage: %s", args[0], memoryUsage)
			}
			return nil
		},
		Annotations: map[string]string{annOp: "memory"},
		RunE: func(cmd *cobra.Command, args []string) error {
			action := args[0]
			if strings.TrimSpace(cwdFlag) == "" {
				return usagef("missing required --cwd <dir>")
			}
			cwd, err := absDir(cwdFlag)
			if err != nil {
				return err
			}
			text, err := fileArg(strings.Join(args[1:], " "), cwd)
			if err != nil {
				return err
			}
			if action != "read" && strings.TrimSpace(text) == "" {
				return usagef("missing text for memory %s", action)
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			var m api.Memory
			switch action {
			case "read":
				m, err = c.Memory(cmd.Context(), cwd)
			case "append":
				m, err = c.AppendMemory(cmd.Context(), cwd, text)
			default:
				m, err = c.ReplaceMemory(cmd.Context(), cwd, text)
			}
			if err != nil {
				return err
			}
			return emit(cmd, m, func(w io.Writer) {
				if action == "read" {
					_, _ = fmt.Fprintln(w, strings.TrimRight(m.Text, "\n"))
					return
				}
				_, _ = fmt.Fprintf(w, "Memory updated for %s\n", cwd)
			})
		},
	}
	cmd.Flags().StringVar(&cwdFlag, "cwd", "", "project folder (required)")
	return cmd
}
