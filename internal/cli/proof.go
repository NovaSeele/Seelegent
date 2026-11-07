package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// annAction is the command annotation naming the action of annOp for the worker guard (proof run is mutating,
// proof matrix is not).
const annAction = "seelegent.action"

// proofPoll is how often proof run --wait asks for the run's state.
const proofPoll = 500 * time.Millisecond

// printGate is the gate part of a report's text.
func printGate(w io.Writer, r api.TaskReport) {
	if g := r.LastGate; g != nil {
		what := "gate " + g.State
		if g.Phase != "" {
			what = g.Phase
		}
		if g.Fixing {
			what += ", automatic fix turn at work"
		}
		_, _ = fmt.Fprintf(w, "\nGate (turn %d): %s\n", g.Turn, what)
	}
	if g := r.Gate; g != nil {
		_, _ = fmt.Fprintf(w, "\nGate: %s", g.State)
		if g.AutoFixTurns > 0 {
			_, _ = fmt.Fprintf(w, " after %d automatic fix turn(s)", g.AutoFixTurns)
		}
		_, _ = fmt.Fprintln(w)
		for _, s := range g.Steps {
			label := s.Label
			if s.Cmd != "" {
				label += ": " + s.Cmd
			}
			if s.Exit != nil {
				label += fmt.Sprintf(" (exit %d)", *s.Exit)
			}
			_, _ = fmt.Fprintf(w, "  failed %s\n", label)
			if len(s.Files) > 0 {
				_, _ = fmt.Fprintf(w, "    files: %s\n", strings.Join(s.Files, ", "))
			}
			for _, line := range strings.Split(strings.TrimRight(s.Tail, "\n"), "\n") {
				if line != "" {
					_, _ = fmt.Fprintf(w, "    | %s\n", line)
				}
			}
		}
		if len(g.NotRun) > 0 {
			_, _ = fmt.Fprintf(w, "  not run: %s\n", strings.Join(g.NotRun, ", "))
		}
		for _, m := range g.Warnings {
			_, _ = fmt.Fprintf(w, "  warning: %s\n", m)
		}
	}
	if l := r.Lane; l != nil {
		_, _ = fmt.Fprintf(w, "Lane: %s\n", strings.Join(l.Paths, ", "))
		if len(l.OutOfLane) > 0 {
			_, _ = fmt.Fprintf(w, "  changed outside the lane: %s\n", strings.Join(l.OutOfLane, ", "))
		}
		for _, m := range append(append([]string(nil), l.Warnings...), l.Forced...) {
			_, _ = fmt.Fprintf(w, "  %s\n", m)
		}
	}
	if b := r.Bar; b != nil && len(b.Changed) > 0 {
		_, _ = fmt.Fprintf(w, "Bar changed since the task started: %s\n", strings.Join(b.Changed, ", "))
	}
	if v := r.Verifier; v != nil {
		_, _ = fmt.Fprintf(w, "Verifier: %s\n", v.Verdict)
		for _, reason := range v.Reasons {
			_, _ = fmt.Fprintf(w, "  - %s\n", reason)
		}
		if v.Unverified > 0 {
			_, _ = fmt.Fprintf(w, "  %d citation(s) do not match the repository\n", v.Unverified)
		}
		if v.Detail != "" {
			_, _ = fmt.Fprintf(w, "  %s\n", v.Detail)
		}
	}
}

const proofUsage = "seelegent proof index|matrix|run|run-status|cancel-run|stats|lanes|contention [flags]"

// newProofCommand is `seelegent proof`: the proof ledger, regression runs, lane claims and gate statistics.
func newProofCommand(d deps) *cobra.Command {
	var cwdFlag string
	cmd := &cobra.Command{
		Use:   "proof <command>",
		Short: "Proof matrix, regression runs, lane claims and gate statistics",
		Long: "The proof views of a repository (default: the current folder): the requirement matrix of a Spec Kit\n" +
			"feature, the project index, regression runs of every gate command, the lane claims, the files several\n" +
			"tasks changed and the gate statistics. See docs/gates.md.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown proof command %q; usage: %s", args[0], proofUsage)
			}
			return usagef("usage: %s", proofUsage)
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.PersistentFlags().StringVar(&cwdFlag, "cwd", "", "repository folder (default: the current folder)")
	cwd := func() (string, error) {
		if strings.TrimSpace(cwdFlag) == "" {
			return absDir(".")
		}
		return absDir(cwdFlag)
	}
	cmd.AddCommand(proofIndexCommand(d, cwd), proofMatrixCommand(d, cwd), proofRunCommand(d, cwd),
		proofRunStatusCommand(d, cwd), proofCancelRunCommand(d, cwd), proofStatsCommand(d, cwd),
		proofLanesCommand(d, cwd), proofContentionCommand(d, cwd))
	return cmd
}

// proofCall resolves the folder, connects to the daemon and runs fn.
func proofCall(cmd *cobra.Command, d deps, cwd func() (string, error),
	fn func(ctx context.Context, c *client.Client, dir string) error) error {
	dir, err := cwd()
	if err != nil {
		return err
	}
	c, _, err := d.daemonClient(cmd)
	if err != nil {
		return err
	}
	return fn(cmd.Context(), c, dir)
}

func countsLine(c api.ProofCounts) string {
	return fmt.Sprintf("proven %d, unproven %d, in progress %d, failing %d, regressed %d, unmapped %d", c.Proven,
		c.Unproven, c.InProgress, c.Failing, c.Regressed, c.Unmapped)
}

func proofIndexCommand(d deps, cwd func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Every feature of the repository with its requirement counts, and the latest regression runs",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				ix, err := c.ProofIndex(ctx, dir)
				if err != nil {
					return err
				}
				return emit(cmd, ix, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "Proof of %s: %s\n", ix.RepoRoot, countsLine(ix.Totals))
					for _, f := range ix.Features {
						_, _ = fmt.Fprintf(w, "  %-32s %s; tasks %d/%d", f.Dir, countsLine(f.Counts), f.Tasks.Done,
							f.Tasks.Total)
						if f.Error != "" {
							_, _ = fmt.Fprintf(w, " (%s)", f.Error)
						}
						_, _ = fmt.Fprintln(w)
					}
					for i := range ix.Runs {
						r := &ix.Runs[i]
						_, _ = fmt.Fprintf(w, "  run %s [%s] %s\n", r.ID, r.State, r.StartedAt)
					}
				})
			})
		},
	}
}

func proofMatrixCommand(d deps, cwd func() (string, error)) *cobra.Command {
	var feature string
	cmd := &cobra.Command{
		Use:   "matrix [<feature>]",
		Short: "The requirement matrix of a Spec Kit feature",
		Args:  maxArgs(1, "seelegent proof matrix <feature> [--cwd <dir>]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				feature = args[0]
			}
			if feature == "" {
				return usagef("missing the feature folder; usage: seelegent proof matrix <feature> [--cwd <dir>]")
			}
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				m, err := c.ProofMatrix(ctx, dir, feature)
				if err != nil {
					return err
				}
				return emit(cmd, m, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "%s (%s): %s; tasks %d/%d\n", m.Feature, m.Title, countsLine(m.Counts),
						m.Tasks.Done, m.Tasks.Total)
					for _, r := range m.Rows {
						ids := make([]string, 0, len(r.Tasks))
						for _, t := range r.Tasks {
							ids = append(ids, t.ID)
						}
						_, _ = fmt.Fprintf(w, "  %-8s %-12s %s", r.ID, r.Status, r.Text)
						if len(ids) > 0 {
							_, _ = fmt.Fprintf(w, " [%s]", strings.Join(ids, ", "))
						}
						_, _ = fmt.Fprintln(w)
					}
					if len(m.UncoveredTasks) > 0 {
						_, _ = fmt.Fprintf(w, "Tasks without Covers: %s\n", strings.Join(m.UncoveredTasks, ", "))
					}
				})
			})
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "feature folder (or give it as the argument)")
	return cmd
}

func printRun(w io.Writer, r api.ProofRun) {
	feature := r.Feature
	if feature == "" {
		feature = "all features"
	}
	_, _ = fmt.Fprintf(w, "Regression run %s [%s] (%s): green %d, red %d, error %d of %d\n", r.ID, r.State, feature,
		r.Counts.Green, r.Counts.Red, r.Counts.Error, r.Total)
	if r.Noisy {
		_, _ = fmt.Fprintln(w, "  Other tasks were active: results may reflect their unfinished work.")
	}
	if r.Dirty {
		_, _ = fmt.Fprintln(w, "  The worktree had uncommitted changes.")
	}
	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", r.Error)
	}
	for _, res := range r.Results {
		exit := "-"
		if res.Exit != nil {
			exit = fmt.Sprint(*res.Exit)
		}
		_, _ = fmt.Fprintf(w, "  [%-7s] %s (%d ms, exit %s)\n", res.State, res.Cmd, res.Ms, exit)
	}
}

func proofRunCommand(d deps, cwd func() (string, error)) *cobra.Command {
	var (
		feature string
		force   bool
		wait    int
	)
	cmd := &cobra.Command{
		Use:         "run [<feature>]",
		Short:       "Run every gate command of the repository's features again (or of one feature)",
		Long:        "Starts a regression run. --wait N waits up to N seconds for it to end (0: return at once).",
		Args:        maxArgs(1, "seelegent proof run [<feature>] [--force] [--wait <seconds>] [--cwd <dir>]"),
		Annotations: map[string]string{annOp: "proof", annAction: "run"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				feature = args[0]
			}
			if wait < 0 {
				return usagef("--wait must be 0 or more")
			}
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				r, err := c.ProofRun(ctx, api.ProofRunRequest{Cwd: dir, Feature: feature, Force: force})
				if err != nil {
					return err
				}
				if r, err = pollRun(ctx, c, dir, r, time.Duration(wait)*time.Second); err != nil {
					return err
				}
				return emit(cmd, r, func(w io.Writer) { printRun(w, r) })
			})
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "only this feature folder")
	cmd.Flags().BoolVar(&force, "force", false, "run even while other tasks of the repository are active")
	cmd.Flags().IntVar(&wait, "wait", 0, "seconds to wait for the run to end")
	return cmd
}

// pollRun asks for the run's state until it ends or wait is over.
func pollRun(ctx context.Context, c *client.Client, dir string, r api.ProofRun, wait time.Duration) (api.ProofRun,
	error) {
	if wait <= 0 || r.State != api.RunRunning {
		return r, nil
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(proofPoll)
	defer tick.Stop()
	for r.State == api.RunRunning {
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-deadline.C:
			return r, nil
		case <-tick.C:
			cur, err := c.ProofRunStatus(ctx, dir, r.ID)
			if err != nil {
				return r, err
			}
			r = cur
		}
	}
	return r, nil
}

func proofRunStatusCommand(d deps, cwd func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:     "run-status <run-id>",
		Aliases: []string{"run_status"},
		Short:   "The state and results of a regression run",
		Args:    exactArgs(1, "seelegent proof run-status <run-id> [--cwd <dir>]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				r, err := c.ProofRunStatus(ctx, dir, args[0])
				if err != nil {
					return err
				}
				return emit(cmd, r, func(w io.Writer) { printRun(w, r) })
			})
		},
	}
}

func proofCancelRunCommand(d deps, cwd func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:         "cancel-run <run-id>",
		Aliases:     []string{"cancel_run"},
		Short:       "Stop a running regression run",
		Args:        exactArgs(1, "seelegent proof cancel-run <run-id> [--cwd <dir>]"),
		Annotations: map[string]string{annOp: "proof", annAction: "cancel_run"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				r, err := c.ProofCancelRun(ctx, dir, args[0])
				if err != nil {
					return err
				}
				return emit(cmd, r, func(w io.Writer) { printRun(w, r) })
			})
		},
	}
}

func proofStatsCommand(d deps, cwd func() (string, error)) *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Gate, lane and verifier statistics of the repository",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				st, err := c.ProofStats(ctx, dir, since)
				if err != nil {
					return err
				}
				return emit(cmd, st, func(w io.Writer) {
					v := st.Verifier
					_, _ = fmt.Fprintf(w, "Tasks: %d (%d gated)\n", st.Tasks, st.GatedTasks)
					_, _ = fmt.Fprintf(w, "Gate runs: %d (red on the first run: %d, caught before review: %d)\n",
						st.GateRuns, st.FirstRunRed, st.CaughtBeforeReview)
					_, _ = fmt.Fprintf(w, "Automatic fix turns: %d (budget used up: %d)\n", st.AutoFixTurns,
						st.ExhaustedBudgets)
					_, _ = fmt.Fprintf(w, "Lanes: %d refused, %d forced, %d violations caught\n", st.LaneRefusals,
						st.LaneForced, st.LaneViolationsCaught)
					_, _ = fmt.Fprintf(w, "Lead followups per task: %.2f gated, %.2f ungated\n",
						st.LeadFollowups.Gated, st.LeadFollowups.Ungated)
					_, _ = fmt.Fprintf(w, "Verifier: %d runs, %d accepted, %d rejected, %d errors, %d slipped, "+
						"%d tokens\n", v.Runs, v.Accepts, v.Rejects, v.Errors, v.Slipped, v.Tokens)
				})
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "only tasks created since this RFC 3339 time")
	return cmd
}

func proofLanesCommand(d deps, cwd func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "lanes",
		Short: "The lane claims held in the repository",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				v, err := c.Lanes(ctx, dir)
				if err != nil {
					return err
				}
				return emit(cmd, v, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "Lanes (%s) in %s\n", v.Mode, v.RepoRoot)
					if len(v.Claims) == 0 {
						_, _ = fmt.Fprintln(w, "No claims held.")
					}
					for _, cl := range v.Claims {
						held := ""
						if cl.HeldUntil != "" {
							held = ", held until " + cl.HeldUntil
						}
						_, _ = fmt.Fprintf(w, "  %-20s %-12s %s (%s%s)\n", cl.TaskID, cl.Status,
							strings.Join(cl.Paths, ", "), cl.Source, held)
					}
				})
			})
		},
	}
}

func proofContentionCommand(d deps, cwd func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "contention",
		Short: "Files that several tasks of the repository changed recently",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return proofCall(cmd, d, cwd, func(ctx context.Context, c *client.Client, dir string) error {
				v, err := c.Contention(ctx, dir)
				if err != nil {
					return err
				}
				return emit(cmd, v, func(w io.Writer) {
					if len(v.Files) == 0 {
						_, _ = fmt.Fprintf(w, "No file was changed by several tasks in the last %d hour(s).\n",
							v.WindowHours)
					}
					for _, f := range v.Files {
						ids := make([]string, 0, len(f.Tasks))
						for _, t := range f.Tasks {
							ids = append(ids, t.TaskID)
						}
						_, _ = fmt.Fprintf(w, "  %s: %s\n", f.File, strings.Join(ids, ", "))
					}
				})
			})
		},
	}
}

// maxArgs refuses more than n arguments.
func maxArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > n {
			return usagef("too many arguments; usage: %s", usage)
		}
		return nil
	}
}

// exactArgs wants exactly n arguments.
func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usagef("usage: %s", usage)
		}
		return nil
	}
}
