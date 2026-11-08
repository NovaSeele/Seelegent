package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// gateFlags are the gate, lane and verifier options of delegate, run and followup (docs/gates.md).
type gateFlags struct {
	gate      []string
	detect    bool
	noGate    bool
	lane      []string
	noLane    bool
	forceLane bool
	verify    bool
}

func (o *gateFlags) add(fs *pflag.FlagSet) {
	fs.StringArrayVar(&o.gate, "gate", nil, "gate command to run after the report (repeatable; replaces the spec's)")
	fs.BoolVar(&o.detect, "gate-detect", false, "add the repository's detected test, lint and build commands")
	fs.BoolVar(&o.noGate, "no-gate", false, "run no gate commands (lane and hygiene checks still run)")
	fs.StringSliceVar(&o.lane, "lane", nil, "files and globs the worker may change, relative to the repository root")
	fs.BoolVar(&o.noLane, "no-lane", false, "drop the lane a spec would give")
	fs.BoolVar(&o.forceLane, "force-lane", false, "claim the lane even where another task holds it")
	fs.BoolVar(&o.verify, "verify", false, "run the second-opinion verifier once the gate is green")
}

// gateOptions are the request fields the gate flags set.
type gateOptions struct {
	gate      *bool
	commands  []string
	lane      []string
	noLane    bool
	forceLane bool
	verify    *bool
}

// options checks the flags against each other and returns the request fields. verify is sent only when the flag
// was given, so the verifier setting decides otherwise.
func (o *gateFlags) options(fs *pflag.FlagSet) (gateOptions, error) {
	switch {
	case o.noGate && (len(o.gate) > 0 || o.detect):
		return gateOptions{}, usagef("--no-gate cannot go with --gate or --gate-detect")
	case o.noLane && len(o.lane) > 0:
		return gateOptions{}, usagef("--no-lane cannot go with --lane")
	}
	g := gateOptions{commands: o.gate, lane: splitList(o.lane), noLane: o.noLane, forceLane: o.forceLane}
	switch {
	case o.noGate:
		f := false
		g.gate = &f
	case o.detect:
		t := true
		g.gate = &t
	}
	if fs.Changed("verify") {
		v := o.verify
		g.verify = &v
	}
	return g, nil
}

// delegateFlags are the options of delegate and run (the old names).
type delegateFlags struct {
	cwd, title, member, backend, provider, model, effort, mode, profile, spec, task, afterMode string

	after    []string
	allowGit bool
	gates    gateFlags
	wait     int
	contest  contestFlags
}

func (f *delegateFlags) add(fs *pflag.FlagSet, withWait bool) {
	fs.StringVar(&f.cwd, "cwd", "", "project folder (default: the current folder)")
	fs.StringVar(&f.title, "title", "", "task title")
	fs.StringVar(&f.backend, "backend", "", "worker backend: agy, claude or codex")
	fs.StringVar(&f.provider, "provider", "", "API-key provider")
	fs.StringVar(&f.model, "model", "", "model or tier (fast, standard, strong, max)")
	fs.StringVar(&f.effort, "effort", "", "reasoning effort")
	fs.StringVar(&f.mode, "mode", "", "full (default) or plan")
	fs.StringVar(&f.profile, "profile", "", "worker profile (role)")
	fs.StringVar(&f.spec, "spec", "", "Spec Kit feature folder")
	fs.StringVar(&f.task, "task", "", "Spec Kit task ids, comma separated (needs --spec)")
	fs.StringSliceVar(&f.after, "after", nil, "start after these tasks (comma separated)")
	fs.StringVar(&f.afterMode, "after-mode", "", "accepted (default) or done")
	fs.BoolVar(&f.allowGit, "allow-git", false, "let the worker commit")
	fs.StringVar(&f.member, "member", "", "department member (its backend, model or tier and role)")
	f.gates.add(fs)
	if withWait {
		fs.IntVar(&f.wait, "wait", defaultWait, "seconds to wait for the report (0: return at once)")
	}
	f.contest.add(fs)
}

// request builds the delegate request from the flags and the prompt words.
func (f *delegateFlags) request(fs *pflag.FlagSet, words []string) (api.DelegateRequest, error) {
	g, err := f.gates.options(fs)
	if err != nil {
		return api.DelegateRequest{}, err
	}
	mode, err := ops.TaskMode(f.mode)
	if err != nil {
		return api.DelegateRequest{}, usagef("--mode: %v", err)
	}
	cwd, err := absDir(f.cwd)
	if err != nil {
		return api.DelegateRequest{}, err
	}
	if cwd == "" {
		if cwd, err = absDir("."); err != nil {
			return api.DelegateRequest{}, err
		}
	}
	prompt, err := fileArg(strings.Join(words, " "), cwd)
	if err != nil {
		return api.DelegateRequest{}, err
	}
	if strings.TrimSpace(prompt) == "" {
		return api.DelegateRequest{}, usagef("the prompt is empty")
	}
	req := api.DelegateRequest{Prompt: prompt, Title: f.title, Cwd: cwd, Mode: mode, Model: f.model, Effort: f.effort,
		Backend: f.backend, Provider: f.provider, Member: f.member, Profile: f.profile, After: splitList(f.after),
		AfterMode: f.afterMode,
		AllowGit:  f.allowGit, Gate: g.gate, GateCommands: g.commands, Lane: g.lane, NoLane: g.noLane,
		ForceLane: g.forceLane, Verify: g.verify, Session: session()}
	ids := splitList([]string{f.task})
	switch {
	case f.spec != "":
		req.Spec = &api.SpecRequest{Dir: f.spec, TaskIDs: ids}
	case len(ids) > 0:
		return api.DelegateRequest{}, usagef("--task needs --spec (the Spec Kit feature folder the ids come from)")
	}
	return req, nil
}

// splitList splits comma-separated values and drops empty ones.
func splitList(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// optList points at vals when the flag was given (`--gate ""` clears the lead's commands), nil when it was not.
func optList(vals []string) *[]string {
	if vals == nil {
		return nil
	}
	return &vals
}

const delegateUsage = "seelegent delegate [flags] <prompt|@file>"

func newDelegateCommand(d deps) *cobra.Command {
	var f delegateFlags
	cmd := &cobra.Command{
		Use:   "delegate [flags] <prompt|@file>",
		Short: "Create a task for a worker and wait for its report",
		Long: "Create a task and wait (--wait seconds, default 600; 0 returns at once) for its report. @file reads\n" +
			"the prompt from a file relative to --cwd. A task with --after does not wait unless --wait is given.",
		Args:        minArgs(delegateUsage),
		Annotations: map[string]string{annOp: "delegate"},
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := f.request(cmd.Flags(), args)
			if err != nil {
				return err
			}
			if f.contest.set(cmd.Flags()) {
				creq, err := f.contest.request(req)
				if err != nil {
					return err
				}
				if f.wait < 0 {
					return usagef("--wait must be 0 or more")
				}
				v, err := runContest(cmd, d, creq, f.wait)
				if err != nil {
					return err
				}
				return emitContest(cmd, &v)
			}
			wait := f.wait
			if len(req.After) > 0 && !cmd.Flags().Changed("wait") {
				wait = 0 // a dependent task usually waits for others
			}
			if wait < 0 {
				return usagef("--wait must be 0 or more")
			}
			r, res, err := delegate(cmd, d, req, wait)
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) {
				if wait == 0 || r.Status == string(api.StatusWaitingDeps) || r.Status == string(api.StatusBlockedDeps) {
					_, _ = fmt.Fprintf(w, "Task %s delegated [%s].\n", r.TaskID, r.Status)
					return
				}
				printReport(w, r, res.Woke)
			})
		},
	}
	f.add(cmd.Flags(), true)
	return cmd
}

// delegate creates the task and waits up to wait seconds for its report.
func delegate(cmd *cobra.Command, d deps, req api.DelegateRequest, wait int) (api.TaskReport, api.WaitResult, error) {
	c, cn, err := d.daemonClient(cmd)
	if err != nil {
		return api.TaskReport{}, api.WaitResult{}, err
	}
	// A flag raised from here on wakes the wait, so a task blocked as soon as it is created (no account can run it)
	// does not hold the caller for the whole wait (final verification N2).
	mark := time.Now()
	sum, err := c.Delegate(cmd.Context(), req)
	if err != nil {
		return api.TaskReport{}, api.WaitResult{}, err
	}
	if wait > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Task %s created; waiting up to %d s for its report.\n", sum.ID, wait)
	}
	res, err := waitReports(cmd.Context(), c, cn, ops.Wait{IDs: []string{sum.ID}, Seconds: wait, WakeOnBlocked: true,
		Cwd: req.Cwd, Since: mark})
	if err != nil {
		return api.TaskReport{}, api.WaitResult{}, fmt.Errorf("task %s was created, but reading its report failed: %w",
			sum.ID, err)
	}
	return res.Reports[0], res, nil
}

func newRunCommand(d deps) *cobra.Command {
	var f delegateFlags
	cmd := &cobra.Command{
		Use:   "run [flags] <prompt|@file>",
		Short: "Legacy: delegate and wait up to an hour, then print the report as JSON",
		Long: "The old `run`: the same as `seelegent delegate --wait 3600`, printing the report JSON whatever\n" +
			"--json says. Flags are parsed like every other command (unknown flags are an error).",
		Args:        minArgs("seelegent run [flags] <prompt|@file>"),
		Annotations: map[string]string{annOp: "delegate"},
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := f.request(cmd.Flags(), args)
			if err != nil {
				return err
			}
			if f.contest.set(cmd.Flags()) {
				creq, err := f.contest.request(req)
				if err != nil {
					return err
				}
				v, err := runContest(cmd, d, creq, runWait)
				if err != nil {
					return err
				}
				return printJSON(cmd.OutOrStdout(), v)
			}
			r, _, err := delegate(cmd, d, req, runWait)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), r)
		},
	}
	f.add(cmd.Flags(), false)
	return cmd
}

// printReport is the text of a report.
func printReport(w io.Writer, r api.TaskReport, woke string) {
	head := fmt.Sprintf("Task %s [%s]", r.TaskID, r.Status)
	if r.Turn > 0 {
		head += fmt.Sprintf(", turn %d", r.Turn)
	}
	_, _ = fmt.Fprintln(w, head)
	printReportBody(w, r, woke)
}

// printReportBody is a report after its head line: the response, the files, the attention, the question, the gate,
// the error and the dashboard link.
func printReportBody(w io.Writer, r api.TaskReport, woke string) {
	if r.Response != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimRight(r.Response, "\n"))
	}
	if len(r.FilesChanged) > 0 {
		_, _ = fmt.Fprintf(w, "\nFiles changed: %s\n", strings.Join(r.FilesChanged, ", "))
	}
	if r.Attention != nil {
		_, _ = fmt.Fprintf(w, "\nAttention: %s (%s)", attentionLabel(r.Attention.State), r.Attention.Reason)
		if r.Attention.Detail != "" {
			_, _ = fmt.Fprintf(w, " - %s", r.Attention.Detail)
		}
		_, _ = fmt.Fprintln(w)
	}
	if r.Question != nil {
		_, _ = fmt.Fprintf(w, "Question: %s\n", r.Question.Text)
		for _, o := range r.Question.Options {
			_, _ = fmt.Fprintf(w, "  - %s\n", o)
		}
	}
	printGate(w, r)
	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "Error: %s\n", r.Error)
	}
	if woke == api.WokeTimeout && ops.Pending(r.Status) {
		_, _ = fmt.Fprintf(w, "\nThe wait ended before the task did: run `seelegent wait %s`.\n", r.TaskID)
	}
	if r.Dashboard != "" {
		_, _ = fmt.Fprintf(w, "Dashboard: %s\n", r.Dashboard)
	}
}

// attentionLabel is the label of a report's attention state (M18 hunt H15: every state read BLOCKED).
func attentionLabel(state string) string {
	switch state {
	case api.AttentionQuestion:
		return "QUESTION"
	case api.AttentionStalled:
		return "STALLED"
	case api.AttentionBackgroundWait:
		return "WAITING ON BACKGROUND COMMANDS"
	case api.AttentionBlocked, "":
		return "BLOCKED"
	}
	return strings.ToUpper(state)
}
