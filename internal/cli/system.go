package cli

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// dashboardURL is the dashboard address of a found daemon.
func dashboardURL(c *client.Client) string { return c.BaseURL() + "/" }

func newUICommand(d deps) *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:         "ui",
		Short:       "Open the dashboard in the browser (starting the daemon when needed)",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "daemon"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cn, err := d.connect(startAuto, cliLogger(cmd.ErrOrStderr()))
			if err != nil {
				return err
			}
			c, err := cn.daemon.Ensure(cmd.Context())
			if err != nil {
				return err
			}
			url := dashboardURL(c)
			if !noOpen {
				if err := d.openURL(cmd.Context(), url); err != nil {
					// The address is printed anyway: the user can open it by hand.
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Could not open the browser: %v\n", err)
				}
			}
			return emit(cmd, struct {
				Dashboard string `json:"dashboard"`
			}{url}, func(w io.Writer) { _, _ = fmt.Fprintf(w, "Dashboard: %s\n", url) })
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "only print the address")
	return cmd
}

// stopOut is the --json answer of stop.
type stopOut struct {
	OK      bool `json:"ok"`
	Stopped bool `json:"stopped"`
}

func newStopCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon of this data directory (never starts one)",
		Long: "Ask the running daemon to shut down cleanly and wait (up to about 15 s) until it is gone. Prints\n" +
			"\"stopped\", or \"not running\" when there was none. A daemon that holds its lock but does not answer is\n" +
			"reported with where it runs; Seelegent never kills a process by a recorded pid.",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "daemon"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cn, err := d.connect(startNever, cliLogger(cmd.ErrOrStderr()))
			if err != nil {
				return err
			}
			res, err := cn.daemon.Stop(cmd.Context(), d.stopTimeout)
			if err != nil {
				return err
			}
			return emit(cmd, stopOut{OK: true, Stopped: res.Stopped}, func(w io.Writer) {
				if res.Stopped {
					_, _ = fmt.Fprintln(w, "stopped")
					return
				}
				_, _ = fmt.Fprintln(w, "not running")
			})
		},
	}
}

func newRestartCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:         "restart",
		Short:       "Stop the daemon (if it runs) and start it again",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "daemon"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := cliLogger(cmd.ErrOrStderr())
			cn, err := d.connect(startNever, log)
			if err != nil {
				return err
			}
			if _, err := cn.daemon.Stop(cmd.Context(), d.stopTimeout); err != nil {
				return err
			}
			// restart always starts, even with SEELEGENT_NO_AUTO_START=1.
			cn, err = d.connect(startAlways, log)
			if err != nil {
				return err
			}
			if _, err := cn.daemon.Ensure(cmd.Context()); err != nil {
				return err
			}
			return emit(cmd, struct {
				OK        bool `json:"ok"`
				Restarted bool `json:"restarted"`
			}{true, true}, func(w io.Writer) { _, _ = fmt.Fprintln(w, "Daemon restarted.") })
		},
	}
}

// statusOut is the --json answer of status.
type statusOut struct {
	Running     bool              `json:"running"`
	Dashboard   string            `json:"dashboard,omitempty"`
	Health      *api.Health       `json:"health,omitempty"`
	Backends    []api.Backend     `json:"backends"`
	Accounts    []api.Account     `json:"accounts"`
	ActiveTasks []api.TaskSummary `json:"activeTasks"`
}

func newStatusCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the daemon, backends, accounts and active tasks (never starts the daemon)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cn, err := d.connect(startNever, cliLogger(cmd.ErrOrStderr()))
			if err != nil {
				return err
			}
			out := statusOut{Backends: []api.Backend{}, Accounts: []api.Account{}, ActiveTasks: []api.TaskSummary{}}
			c, err := cn.daemon.Find(ctx)
			switch {
			case err == nil:
			case errors.Is(err, client.ErrNotRunning) && !cn.daemon.LockHeld():
				return emit(cmd, out, func(w io.Writer) { _, _ = fmt.Fprintln(w, "not running") })
			case errors.Is(err, client.ErrNotRunning):
				return cn.daemon.NotAnswering()
			default:
				return err
			}
			h, err := c.Health(ctx)
			if err != nil {
				return err
			}
			out.Running, out.Dashboard, out.Health = true, dashboardURL(c), &h
			if out.Accounts, err = c.Accounts(ctx); err != nil {
				return err
			}
			if out.ActiveTasks, err = ops.ActiveTasks(ctx, c); err != nil {
				return err
			}
			if out.Backends, err = c.Backends(ctx); err != nil {
				return err
			}
			out.Accounts, out.Backends = api.NonNil(out.Accounts), api.NonNil(out.Backends)
			return emit(cmd, out, func(w io.Writer) { printStatus(w, out) })
		},
	}
}

// printStatus is the text of status (the old layout, with account labels only).
func printStatus(w io.Writer, s statusOut) {
	_, _ = fmt.Fprintf(w, "Seelegent %s running at %s (pid %d)\n", s.Health.Version, s.Dashboard, s.Health.PID)
	for _, b := range s.Backends {
		en := "disabled"
		if b.Enabled {
			en = "enabled"
		}
		inst := "installed: no"
		if b.Installed {
			inst = "installed: yes"
		}
		_, _ = fmt.Fprintf(w, "%-10s %-10s %-16s %-14s %d active\n", b.ID, en, inst,
			fmt.Sprintf("%d account%s", b.Accounts, ops.Plural(b.Accounts)), b.Active)
	}
	for i := range s.Accounts {
		a := &s.Accounts[i]
		state := string(a.Status)
		if !a.Enabled {
			state = "disabled"
		} else if a.Resting {
			state += " (resting)"
		}
		_, _ = fmt.Fprintf(w, "%-7s %-32s %s  %s\n", a.ID, a.Label, state, quotaText(a.Quota))
	}
	if len(s.ActiveTasks) > 0 {
		_, _ = fmt.Fprintln(w, "\nActive tasks:")
		for i := range s.ActiveTasks {
			t := &s.ActiveTasks[i]
			_, _ = fmt.Fprintf(w, "  %-19s %-14s %s%s\n", t.ID, t.Status, titleOf(t.Title), blockedTag(t.Attention))
		}
	}
}

// quotaText is "group: 5h 80% / weekly 60%; ..." for an account's quota.
func quotaText(q api.QuotaSnapshot) string {
	groups := make([]string, 0, len(q.Groups))
	for _, g := range q.Groups {
		ws := make([]string, 0, len(g.Windows))
		for _, win := range g.Windows {
			ws = append(ws, fmt.Sprintf("%s %d%%", win.Window, int(math.Round(win.Remaining*100))))
		}
		groups = append(groups, g.Name+": "+strings.Join(ws, " / "))
	}
	return strings.Join(groups, "; ")
}

func titleOf(title string) string {
	if title == "" {
		return "(no title)"
	}
	return title
}

// blockedTag is " [BLOCKED: reason]" for a task that waits for the lead.
func blockedTag(a *api.Attention) string {
	if a == nil || a.State != api.AttentionBlocked {
		return ""
	}
	return " [BLOCKED: " + a.Reason + "]"
}
