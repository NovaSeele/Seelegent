// Package cli defines the seelegent command line. Commands stay thin: they parse flags, call the daemon client or
// the app, and print the result.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
	// ExitDaemonLocked: `seelegent daemon` found another daemon holding the data directory's lock.
	ExitDaemonLocked = client.ExitDaemonLocked
)

// Execute runs the command line with the process arguments and exits with its status code.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// Run executes the command line with the given arguments and output streams and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand(buildinfo.Get())
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		if _, running := errors.AsType[*daemonRunningError](err); running {
			return ExitDaemonLocked
		}
		return ExitError
	}
	return ExitOK
}

// NewRootCommand builds the seelegent command tree for the given build.
func NewRootCommand(info buildinfo.Info) *cobra.Command {
	root := &cobra.Command{
		Use:   "seelegent",
		Short: "Hand coding tasks from a lead agent to worker CLIs on your own accounts",
		Long: "Seelegent runs a local daemon that lets a lead coding agent delegate tasks to worker coding CLIs\n" +
			"on your own accounts, with an MCP server for the lead and a web dashboard.",
		Version:       info.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newVersionCommand(info), newDaemonCommand(info), newMCPCommand(info), newLaunchCommand())
	return root
}
