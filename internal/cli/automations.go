package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// The automations command (milestone 15): saved delegations on a schedule (docs/automations.md). An automation is
// created disabled; `enable` only asks, and the user confirms it in the dashboard.

const automationsUsage = "seelegent automations list|show|create|update|enable|disable|delete|run|runs|preview ..."

// automationFlags are the fields of create and update; update sends only the flags given.
type automationFlags struct {
	name, cwd, every, at, cron, timezone, member, backend, model, precheck string
	weekday, grace                                                         int
	minRemaining                                                           float64
	beforeReset                                                            bool
	gates                                                                  gateFlags
}

func (f *automationFlags) add(fs *pflag.FlagSet) {
	f.addSchedule(fs)
	fs.StringVar(&f.name, "name", "", "name")
	fs.StringVar(&f.cwd, "cwd", "", "project folder (default: the current folder)")
	fs.StringVar(&f.member, "member", "", "department member")
	fs.StringVar(&f.backend, "backend", "", "agy, claude or codex")
	fs.StringVar(&f.model, "model", "", "model id or tier")
	fs.StringVar(&f.precheck, "precheck", "", "shell command run in the folder first; a non-zero exit skips the run")
	fs.Float64Var(&f.minRemaining, "min-remaining", 20, "skip when the best account has less than this % left")
	fs.BoolVar(&f.beforeReset, "use-before-reset", false, "also run once when a weekly quota resets within 24 h "+
		"with at least 40% left")
	fs.IntVar(&f.grace, "grace", 60, "minutes a run missed while the daemon was off may be late")
	fs.StringArrayVar(&f.gates.gate, "gate", nil, "gate command (repeatable)")
	fs.BoolVar(&f.gates.detect, "gate-detect", false, "add the repository's detected test, lint and build commands")
	fs.BoolVar(&f.gates.noGate, "no-gate", false, "run no gate commands")
	fs.StringSliceVar(&f.gates.lane, "lane", nil, "files and globs the worker may change")
	fs.BoolVar(&f.gates.noLane, "no-lane", false, "no lane")
}

func (f *automationFlags) addSchedule(fs *pflag.FlagSet) {
	fs.StringVar(&f.every, "every", "", "hourly, daily, weekdays or weekly")
	fs.StringVar(&f.at, "at", "", "HH:MM (hourly uses only the minutes)")
	fs.IntVar(&f.weekday, "weekday", 0, "weekly: 0-6, 0 = Sunday")
	fs.StringVar(&f.cron, "cron", "", `5-field cron instead of --every/--at, e.g. "30 9 * * 1-5"`)
	fs.StringVar(&f.timezone, "timezone", "", "IANA zone, e.g. Asia/Ho_Chi_Minh (default: local)")
}

// schedule is the schedule the flags give, or nil when none of its flags was given.
func (f *automationFlags) schedule(fs *pflag.FlagSet) *api.Schedule {
	if !fs.Changed("every") && !fs.Changed("at") && !fs.Changed("weekday") && !fs.Changed("cron") {
		return nil
	}
	sc := &api.Schedule{Every: f.every, At: f.at, Cron: f.cron}
	if fs.Changed("weekday") {
		w := f.weekday
		sc.Weekday = &w
	}
	return sc
}

func (f *automationFlags) input(fs *pflag.FlagSet, prompt string) (api.AutomationInput, error) {
	g, err := f.gates.options(fs)
	if err != nil {
		return api.AutomationInput{}, err
	}
	cwd, err := absDir(f.cwd)
	if err == nil && cwd == "" {
		cwd, err = absDir(".")
	}
	if err != nil {
		return api.AutomationInput{}, err
	}
	in := api.AutomationInput{Name: f.name, Prompt: prompt, Cwd: cwd, Timezone: f.timezone, Member: f.member,
		Backend: f.backend, Model: f.model, Gate: g.gate, GateCommands: g.commands, Lane: g.lane, NoLane: g.noLane,
		Precheck: f.precheck, Quota: &api.AutomationQuota{MinRemaining: f.minRemaining, UseBeforeReset: f.beforeReset}}
	if sc := f.schedule(fs); sc != nil {
		in.Schedule = *sc
	}
	if fs.Changed("grace") {
		gr := f.grace
		in.GraceMinutes = &gr
	}
	return in, nil
}

func (f *automationFlags) patch(fs *pflag.FlagSet, cur *api.Automation, prompt *string) (api.AutomationPatch, error) {
	g, err := f.gates.options(fs)
	if err != nil {
		return api.AutomationPatch{}, err
	}
	p := api.AutomationPatch{Prompt: prompt, Schedule: f.schedule(fs)}
	str := func(flag string, v string) *string {
		if !fs.Changed(flag) {
			return nil
		}
		return &v
	}
	p.Name, p.Timezone, p.Member = str("name", f.name), str("timezone", f.timezone), str("member", f.member)
	p.Backend, p.Model, p.Precheck = str("backend", f.backend), str("model", f.model), str("precheck", f.precheck)
	if fs.Changed("cwd") {
		cwd, err := absDir(f.cwd)
		if err != nil {
			return api.AutomationPatch{}, err
		}
		p.Cwd = &cwd
	}
	if fs.Changed("min-remaining") || fs.Changed("use-before-reset") {
		q := cur.Quota
		if fs.Changed("min-remaining") {
			q.MinRemaining = f.minRemaining
		}
		if fs.Changed("use-before-reset") {
			q.UseBeforeReset = f.beforeReset
		}
		p.Quota = &q
	}
	if fs.Changed("grace") {
		gr := f.grace
		p.GraceMinutes = &gr
	}
	switch {
	case g.gate != nil:
		none := []string{}
		p.Gate, p.GateCommands = g.gate, &none
		if len(g.commands) > 0 {
			p.GateCommands = &g.commands
		}
	case fs.Changed("gate"):
		p.GateCommands = &g.commands
	}
	if fs.Changed("lane") || fs.Changed("no-lane") {
		lane := api.NonNil(g.lane)
		p.Lane, p.NoLane = &lane, &g.noLane
	}
	return p, nil
}

func printAutomation(w io.Writer, x *api.Automation) {
	state := "disabled"
	switch {
	case x.Enabled:
		state = "enabled"
	case x.EnableRequested:
		state = "waiting for confirmation in the dashboard"
	}
	when := x.Schedule.Cron
	if when == "" {
		when = strings.TrimSpace(x.Schedule.Every + " " + x.Schedule.At)
	}
	line := fmt.Sprintf("%s  %-20s %s (%s), %s", x.ID, x.Name, when, x.Timezone, state)
	if x.NextRun != "" && x.Enabled {
		line += ", next " + x.NextRun
	}
	if x.Running {
		line += ", running " + x.ActiveTaskID
	}
	_, _ = fmt.Fprintln(w, line)
}

func printAutoRun(w io.Writer, r *api.AutomationRun) {
	line := fmt.Sprintf("%s  %-12s %s", r.At, r.Trigger, r.Outcome)
	if r.TaskID != "" {
		line += "  task " + r.TaskID
	}
	if r.Detail != "" {
		line += "  " + r.Detail
	}
	_, _ = fmt.Fprintln(w, line)
}

// automationLeaf is an action on one automation that prints it.
func automationLeaf(d deps, name, short, action string,
	run func(cmd *cobra.Command, c *client.Client, id string) (api.Automation, error)) *cobra.Command {
	return leaf(d, name+" <id>", short, "automations", action, exactArgs(1, "seelegent automations "+name+" <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			x, err := run(cmd, c, args[0])
			if err != nil {
				return err
			}
			return emit(cmd, x, func(w io.Writer) { printAutomation(w, &x) })
		})
}

func newAutomationsCommand(d deps) *cobra.Command {
	list := leaf(d, "list", "The automations", "automations", "list", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			xs, err := c.Automations(cmd.Context())
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(xs), func(w io.Writer) {
				for i := range xs {
					printAutomation(w, &xs[i])
				}
			})
		})
	show := automationLeaf(d, "show", "One automation", "list",
		func(cmd *cobra.Command, c *client.Client, id string) (api.Automation, error) {
			return c.Automation(cmd.Context(), id)
		})
	var cf automationFlags
	create := leaf(d, "create --every daily --at 09:00 [flags] <prompt|@file>", "Save a delegation on a schedule "+
		"(created disabled)", "automations", "create", minArgs("seelegent automations create [flags] <prompt|@file>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			prompt, err := fileArg(strings.Join(args, " "), "")
			if err != nil {
				return err
			}
			in, err := cf.input(cmd.Flags(), prompt)
			if err != nil {
				return err
			}
			x, err := c.CreateAutomation(cmd.Context(), in)
			if err != nil {
				return err
			}
			return emit(cmd, x, func(w io.Writer) {
				printAutomation(w, &x)
				_, _ = fmt.Fprintln(w, "It is disabled: `seelegent automations enable "+x.ID+
					"` asks to enable it, and you confirm in the dashboard.")
			})
		})
	cf.add(create.Flags())
	var uf automationFlags
	update := leaf(d, "update <id> [flags] [prompt|@file]", "Change an automation (only the flags given)",
		"automations", "update", minArgs("seelegent automations update <id> [flags] [prompt|@file]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			cur, err := c.Automation(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var prompt *string
			if len(args) > 1 {
				p, err := fileArg(strings.Join(args[1:], " "), "")
				if err != nil {
					return err
				}
				prompt = &p
			}
			p, err := uf.patch(cmd.Flags(), &cur, prompt)
			if err != nil {
				return err
			}
			x, err := c.UpdateAutomation(cmd.Context(), args[0], p)
			if err != nil {
				return err
			}
			return emit(cmd, x, func(w io.Writer) { printAutomation(w, &x) })
		})
	uf.add(update.Flags())
	enable := leaf(d, "enable <id>", "Ask to enable an automation; you confirm in the dashboard", "automations",
		"enable", exactArgs(1, "seelegent automations enable <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			res, err := c.EnableAutomation(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emit(cmd, res, func(w io.Writer) { _, _ = fmt.Fprintln(w, res.Message) })
		})
	disable := automationLeaf(d, "disable", "Disable an automation (and withdraw a request to enable it)", "disable",
		func(cmd *cobra.Command, c *client.Client, id string) (api.Automation, error) {
			off := false
			return c.UpdateAutomation(cmd.Context(), id, api.AutomationPatch{Enabled: &off})
		})
	del := leaf(d, "delete <id>", "Delete an automation and its run history", "automations", "delete",
		exactArgs(1, "seelegent automations delete <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			if err := c.DeleteAutomation(cmd.Context(), args[0]); err != nil {
				return err
			}
			return emit(cmd, map[string]any{"ok": true, "deleted": args[0]}, func(w io.Writer) {
				_, _ = fmt.Fprintln(w, "deleted")
			})
		})
	run := leaf(d, "run <id>", "Start one run now (the overlap, quota and precheck rules still apply)",
		"automations", "run", exactArgs(1, "seelegent automations run <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			r, err := c.RunAutomation(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) { printAutoRun(w, &r) })
		})
	var limit int
	runs := leaf(d, "runs <id> [--limit N]", "The run history, newest first", "automations", "runs",
		exactArgs(1, "seelegent automations runs <id> [--limit N]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			rs, err := c.AutomationRuns(cmd.Context(), args[0], limit)
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(rs), func(w io.Writer) {
				for i := range rs {
					printAutoRun(w, &rs[i])
				}
			})
		})
	runs.Flags().IntVar(&limit, "limit", 20, "how many (1-500)")
	return group("automations", "Saved delegations on a schedule", automationsUsage, list, show, create, update,
		enable, disable, del, run, runs, newPreviewCommand(d))
}

func newPreviewCommand(d deps) *cobra.Command {
	var f automationFlags
	var count int
	cmd := leaf(d, "preview --every E --at HH:MM | --cron C [--timezone Z] [--count N]",
		"The next times a schedule runs", "automations", "list", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			sc := f.schedule(cmd.Flags())
			if sc == nil {
				return usagef("give --every/--at or --cron")
			}
			pv, err := c.PreviewSchedule(cmd.Context(), api.SchedulePreviewRequest{Schedule: *sc, Timezone: f.timezone,
				Count: count})
			if err != nil {
				return err
			}
			return emit(cmd, pv, func(w io.Writer) {
				for i, at := range pv.Runs {
					local := ""
					if i < len(pv.Local) {
						local = "  (" + pv.Local[i] + " " + pv.Timezone + ")"
					}
					_, _ = fmt.Fprintln(w, at+local)
				}
			})
		})
	f.addSchedule(cmd.Flags())
	cmd.Flags().IntVar(&count, "count", 5, "how many runs")
	return cmd
}
