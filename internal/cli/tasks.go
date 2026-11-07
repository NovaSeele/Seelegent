package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// Wait limits (seconds), as the MCP tools have them.
const (
	defaultWait   = 600
	defaultCancel = 15
	maxCancel     = 60
	// runWait is how long the legacy `run` waits.
	runWait = 3600
)

// session is the lead session the command line sends: SEELEGENT_SESSION and SEELEGENT_LEAD, else lead "cli".
func session() *api.Session { return ops.CLISession(os.Getenv) }

// daemonClient finds (or starts, unless SEELEGENT_NO_AUTO_START=1) the daemon for a task command.
func (d deps) daemonClient(cmd *cobra.Command) (*client.Client, *conn, error) {
	cn, err := d.connect(startAuto, cliLogger(cmd.ErrOrStderr()))
	if err != nil {
		return nil, nil, err
	}
	if err := cn.settingsOK(); err != nil {
		return nil, nil, err
	}
	c, err := cn.daemon.Ensure(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return c, cn, nil
}

// waitReports waits for ids with ops.WaitReports, finding the daemon again if it restarts during the wait (and
// starting it again only when it went away without saying it was stopping).
func waitReports(ctx context.Context, c *client.Client, cn *conn, w ops.Wait) (api.WaitResult, error) {
	connect := func(ctx context.Context, how ops.Lookup) (*client.Client, error) {
		var found *client.Client
		var err error
		switch how {
		case ops.Reuse:
			return c, nil
		case ops.FindOnly:
			found, err = cn.daemon.Find(ctx)
		default:
			found, err = cn.daemon.Ensure(ctx)
		}
		if err != nil {
			return nil, err
		}
		c = found
		return c, nil
	}
	w.Logger = cn.log
	w.StopGrace = cn.stopGrace
	return ops.WaitReports(ctx, connect, w)
}

func newFollowupCommand(d deps) *cobra.Command {
	var (
		by, model, effort, task, profile, member string
		allowGit, force                          bool
		gates                                    gateFlags
		wait                                     int
	)
	cmd := &cobra.Command{
		Use:         "followup <id> [flags] <message|@file>",
		Short:       "Send the next instruction on a task and wait for the report",
		Args:        minArgs("seelegent followup <id> [flags] <message|@file>"),
		Annotations: map[string]string{annOp: "followup"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			g, err := gates.options(cmd.Flags())
			if err != nil {
				return err
			}
			msg, err := fileArg(strings.Join(args[1:], " "), "")
			if err != nil {
				return err
			}
			if strings.TrimSpace(msg) == "" && !force {
				return usagef("the message is empty (it may be left out only with --force)")
			}
			if wait < 0 {
				return usagef("--wait must be 0 or more")
			}
			req := api.FollowupRequest{Message: msg, By: by, Model: model, Effort: effort, TaskIDs: splitList([]string{task}),
				Profile: profile, Gate: g.gate, GateCommands: g.commands, Lane: g.lane, NoLane: g.noLane,
				ForceLane: g.forceLane, Verify: g.verify, Force: force, Member: member}
			if cmd.Flags().Changed("allow-git") {
				req.AllowGit = &allowGit
			}
			c, cn, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			if _, err := c.Followup(cmd.Context(), id, req); err != nil {
				return err
			}
			res, err := waitReports(cmd.Context(), c, cn, ops.Wait{IDs: []string{id}, Seconds: wait, WakeOnBlocked: true})
			if err != nil {
				return fmt.Errorf("the followup was sent, but reading the report failed: %w", err)
			}
			r := res.Reports[0]
			return emit(cmd, r, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "Followup sent to task %s.\n", id)
				if wait > 0 {
					printReport(w, r, res.Woke)
				}
			})
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&by, "by", "", "lead (default) or user")
	fs.StringVar(&model, "model", "", "model or tier for this turn")
	fs.StringVar(&effort, "effort", "", "reasoning effort")
	fs.StringVar(&task, "task", "", "Spec Kit task ids of this turn, comma separated")
	fs.StringVar(&profile, "profile", "", "worker profile (role)")
	fs.StringVar(&member, "member", "", "move the task to this department member from this turn on")
	fs.BoolVar(&allowGit, "allow-git", false, "let the worker commit")
	fs.BoolVar(&force, "force", false, "start a task that waits for its prerequisites anyway")
	fs.IntVar(&wait, "wait", defaultWait, "seconds to wait for the report (0: return at once)")
	gates.add(fs)
	return cmd
}

func newWaitCommand(d deps) *cobra.Command {
	var (
		seconds int
		anyDone bool
		noWake  bool
	)
	cmd := &cobra.Command{
		Use:   "wait <id...>",
		Short: "Wait for tasks and print their reports",
		Args:  minArgs("seelegent wait <id...> [--any] [--seconds n] [--no-wake]"),
		RunE: func(cmd *cobra.Command, ids []string) error {
			if seconds < 0 {
				return usagef("--seconds must be 0 or more")
			}
			c, cn, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			res, err := waitReports(cmd.Context(), c, cn, ops.Wait{IDs: ids, Seconds: seconds, Any: anyDone,
				WakeOnBlocked: !noWake})
			if err != nil {
				return err
			}
			res.Tasks = api.NonNil(res.Tasks)
			return emit(cmd, res, func(w io.Writer) { printWait(w, res) })
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&seconds, "seconds", defaultWait, "seconds to wait (0: the current reports at once)")
	fs.IntVar(&seconds, "wait", defaultWait, "same as --seconds")
	_ = fs.MarkHidden("wait")
	fs.BoolVar(&anyDone, "any", false, "return when the first task ends")
	fs.BoolVar(&noWake, "no-wake", false, "do not return early when a task needs the lead")
	return cmd
}

// printWait is the text of a wait (the old layout).
func printWait(w io.Writer, res api.WaitResult) {
	_, _ = fmt.Fprintf(w, "Wait finished (woke: %s, done: %v). Reports: %d\n", res.Woke, res.Done, len(res.Reports))
	titles := map[string]string{}
	for i := range res.Tasks {
		titles[res.Tasks[i].ID] = res.Tasks[i].Title
	}
	for i := range res.Reports {
		r := &res.Reports[i]
		tag := ""
		if r.Attention != nil {
			tag = " [BLOCKED: " + r.Attention.Reason + "]"
		}
		_, _ = fmt.Fprintf(w, "  %-19s %-14s %s%s\n", r.TaskID, r.Status, titles[r.TaskID], tag)
	}
}

func newReviewCommand(d deps) *cobra.Command {
	var notes, objection string
	cmd := &cobra.Command{
		Use:   "review <id> accepted|rejected",
		Short: "Record your verdict on a task",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 || (args[1] != "accepted" && args[1] != "rejected") {
				return usagef("usage: seelegent review <id> accepted|rejected [--notes text] [--answer-objection text|@file]")
			}
			return nil
		},
		Annotations: map[string]string{annOp: "review"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, verdict := args[0], args[1]
			answer := ""
			if cmd.Flags().Changed("answer-objection") {
				text, err := fileArg(objection, "")
				if err != nil {
					return err
				}
				answer = strings.TrimSpace(text)
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			sum, err := c.Review(cmd.Context(), id, api.ReviewRequest{Verdict: verdict, Notes: notes, AnswerObjection: answer})
			if err != nil {
				return err
			}
			return emit(cmd, sum, func(w io.Writer) { _, _ = fmt.Fprintf(w, "Task %s reviewed: %s\n", id, verdict) })
		},
	}
	cmd.Flags().StringVar(&notes, "notes", "", "review notes")
	cmd.Flags().StringVar(&objection, "answer-objection", "", "answer to a peer member's objection (text or @file)")
	return cmd
}

func newCancelCommand(d deps) *cobra.Command {
	var wait int
	cmd := &cobra.Command{
		Use:         "cancel <id>",
		Short:       "Cancel a task",
		Args:        rangeArgs(1, 1, "seelegent cancel <id> [--wait seconds]"),
		Annotations: map[string]string{annOp: "cancel"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if wait < 0 || wait > maxCancel {
				return usagef("--wait must be 0..%d", maxCancel)
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			_, stopped, err := c.Cancel(cmd.Context(), id, api.CancelRequest{WaitSeconds: wait})
			if err != nil {
				return err
			}
			r, err := c.Report(cmd.Context(), id, "")
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) {
				if !stopped {
					_, _ = fmt.Fprintf(w, "Task %s is still stopping (status cancelling); check it again with: "+
						"seelegent task %s\n", id, id)
					return
				}
				_, _ = fmt.Fprintf(w, "Task %s cancelled.\n", id)
			})
		},
	}
	cmd.Flags().IntVar(&wait, "wait", defaultCancel, "seconds to wait for the worker to stop (0..60)")
	return cmd
}

func newTaskCommand(d deps) *cobra.Command {
	var (
		steps int
		full  bool
	)
	cmd := &cobra.Command{
		Use:   "task <id>",
		Short: "Show a task's report (or, with --full, the whole task)",
		Args:  rangeArgs(1, 1, "seelegent task <id> [--steps n] [--full]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, ctx := args[0], cmd.Context()
			if steps < 0 {
				return usagef("--steps must be 0 or more")
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			var recent []ops.Step
			if steps > 0 {
				if recent, err = ops.RecentSteps(ctx, c, id, steps); err != nil {
					return err
				}
			}
			if full {
				t, err := c.Task(ctx, id)
				if err != nil {
					return err
				}
				return emit(cmd, struct {
					api.TaskDetail
					RecentSteps []ops.Step `json:"recent_steps,omitempty"`
				}{t, recent}, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "Task %s [%s] %s\n", t.ID, t.Status, titleOf(t.Title))
					_, _ = fmt.Fprintf(w, "Model: %s, Effort: %s, Turns: %d\n", dash(t.Model), dash(t.Effort), len(t.Turns))
					printSteps(w, recent)
				})
			}
			r, err := c.Report(ctx, id, "")
			if err != nil {
				return err
			}
			return emit(cmd, struct {
				api.TaskReport
				RecentSteps []ops.Step `json:"recent_steps,omitempty"`
			}{r, recent}, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "Task %s [%s]\n", r.TaskID, r.Status)
				_, _ = fmt.Fprintf(w, "Model: %s, Effort: %s, Turns: %d\n", dash(r.Model), dash(r.Effort), r.Turn)
				if r.Response != "" {
					_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimRight(r.Response, "\n"))
				}
				printSteps(w, recent)
			})
		},
	}
	cmd.Flags().IntVar(&steps, "steps", 0, "also show the last n worker tool steps")
	cmd.Flags().BoolVar(&full, "full", false, "the whole task with its turns")
	return cmd
}

func printSteps(w io.Writer, steps []ops.Step) {
	if len(steps) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nRecent steps (%d):\n", len(steps))
	for _, s := range steps {
		params := string(s.Params)
		if params == "" {
			params = "{}"
		}
		failed := ""
		if s.Failed {
			failed = " (failed)"
		}
		_, _ = fmt.Fprintf(w, "  - %s: %s%s\n", s.Tool, params, failed)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newLsCommand(d deps) *cobra.Command {
	var (
		active, blocked bool
		project         string
		limit           int
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tasks, newest first",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 200 {
				return usagef("--limit must be 1..200")
			}
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			q := client.TaskQuery{Project: project, Limit: limit}
			if active {
				q.Statuses = []api.TaskStatus{api.StatusQueued, api.StatusRunning, api.StatusWaitingQuota,
					api.StatusWaitingDeps, api.StatusBlockedDeps}
			}
			page, err := c.Tasks(cmd.Context(), q)
			if err != nil {
				return err
			}
			if blocked {
				kept := page.Items[:0]
				for i := range page.Items {
					if ops.Blocked(page.Items[i]) {
						kept = append(kept, page.Items[i])
					}
				}
				page.Items = kept
			}
			page.Items = api.NonNil(page.Items)
			return emit(cmd, page, func(w io.Writer) { printTasks(w, page.Items) })
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&active, "active", false, "only tasks that are not finished")
	fs.BoolVar(&blocked, "blocked", false, "only tasks that wait for the lead")
	fs.StringVar(&project, "project", "", "only this project (its key or folder)")
	fs.IntVar(&limit, "limit", 50, "how many tasks (1..200)")
	return cmd
}

// printTasks is the text of ls (the old layout).
func printTasks(w io.Writer, tasks []api.TaskSummary) {
	if len(tasks) == 0 {
		_, _ = fmt.Fprintln(w, "No tasks found.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-19s %-14s %-14s %-12s %s\n", "ID", "STATUS", "PROJECT", "ATTENTION", "TITLE")
	for i := range tasks {
		t := &tasks[i]
		att := ""
		if ops.Blocked(*t) {
			att = "blocked(" + t.Attention.Reason + ")"
		}
		_, _ = fmt.Fprintf(w, "%-19s %-14s %-14s %-12s %s\n", t.ID, t.Status, t.ProjectName, att, t.Title)
	}
}
