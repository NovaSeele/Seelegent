// Package cli defines the seelegent command line. Commands stay thin: they parse flags, call the daemon client (or
// what internal/ops shares with the MCP server) and print the result. docs/cli.md describes every command.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// Exit codes returned by Run.
const (
	ExitOK = 0
	// ExitError: a runtime or API error, a refusal inside a worker, or doctor with a failed check.
	ExitError = 1
	// ExitUsage: unknown command or flag, wrong arguments.
	ExitUsage = 2
	// ExitDaemonLocked: `seelegent daemon` found another daemon holding the data directory's lock, or its port taken.
	ExitDaemonLocked = client.ExitDaemonLocked
)

// annOp is the command annotation naming the operation the worker guard checks (ops.Mutating).
const annOp = "seelegent.op"

// Execute runs the command line with the process arguments and exits with its status code.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	if code, ok := shimMain(ctx); ok {
		stop()
		os.Exit(code)
	}
	code := Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// Run executes the command line with the given arguments and output streams and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, defaultDeps(), args, stdout, stderr)
}

// run is Run with the outside dependencies passed in (tests replace them).
func run(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == guard.ShimSubcommand {
		// The git shim passes git's arguments untouched, so it never goes through cobra's flag parsing.
		dir, rest := gitShimArgs(args[1:])
		return runGitShim(ctx, dir, rest, os.Stdin, stdout, stderr)
	}
	root := newRoot(buildinfo.Get(), d)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := guardWorker(root, args)
	if err == nil {
		err = root.ExecuteContext(ctx)
	}
	if err == nil {
		return ExitOK
	}
	return report(err, wantsJSON(args), stdout, stderr)
}

// NewRootCommand builds the seelegent command tree for the given build.
func NewRootCommand(info buildinfo.Info) *cobra.Command { return newRoot(info, defaultDeps()) }

func newRoot(info buildinfo.Info, d deps) *cobra.Command {
	root := &cobra.Command{
		Use:   "seelegent",
		Short: "Hand coding tasks from a lead agent to worker CLIs on your own accounts",
		Long: "Seelegent runs a local daemon that lets a lead coding agent delegate tasks to worker coding CLIs\n" +
			"on your own accounts, with an MCP server for the lead and a web dashboard.",
		Version:       info.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// The root takes arguments only to name an unknown command as a usage error (exit 2).
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
			if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
				msg += "\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
			}
			return usagef("%s", msg)
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newUpdateCommand(info, d))
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err: err} })
	root.PersistentFlags().Bool("json", false, "print the result (or the error) as JSON on stdout")
	root.AddCommand(newVersionCommand(info), newDaemonCommand(info), newMCPCommand(info, d), newLaunchCommand(),
		newUICommand(d), newStopCommand(d), newRestartCommand(d), newStatusCommand(d), newDoctorCommand(info, d),
		newDelegateCommand(d), newRunCommand(d), newFollowupCommand(d), newWaitCommand(d), newReviewCommand(d),
		newCancelCommand(d), newTaskCommand(d), newLsCommand(d), newMemoryCommand(d), newRestoreCommand(d), newProofCommand(d))
	root.AddCommand(newMembersCommand(d), newConsultCommand(d), newNotesCommand(d), newMeetingCommand(d),
		newContestCommand(d), newHooksCommand(d), newIssuesCommand(d), newAutomationsCommand(d))
	root.AddCommand(newSetupCommand(info), newSkillCommand(info), newMigrateCommand(), newHookCommand(),
		newGuardCommand())
	return root
}

// guardWorker refuses, inside a Seelegent worker (SEELEGENT_WORKER=1), the commands that change something or
// control the daemon, before anything is parsed or called (old B12). Read-only commands stay allowed.
func guardWorker(root *cobra.Command, args []string) error {
	if !ops.IsWorker(os.Getenv) {
		return nil
	}
	cmd, rest, err := root.Find(args)
	if err != nil || cmd == nil {
		return nil
	}
	op := cmd.Annotations[annOp]
	if op == "" {
		return nil
	}
	action := cmd.Annotations[annAction]
	if op == "memory" {
		// Only `memory read` is allowed; anything that does not parse is refused too (fail closed).
		if cmd.ParseFlags(rest) == nil && len(cmd.Flags().Args()) > 0 {
			action = cmd.Flags().Args()[0]
		}
	}
	if ops.Mutating(op, action) {
		return ops.WorkerError{}
	}
	return nil
}

// wantsJSON reports whether --json is among the arguments (before a "--"), so errors are printed as JSON even when
// they happen before the flags are parsed.
func wantsJSON(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "--json", "--json=true", "--json=1":
			return true
		}
	}
	return false
}

// jsonFlag is the --json flag of the running command.
func jsonFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}
