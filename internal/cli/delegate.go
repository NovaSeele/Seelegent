package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// offFlags are the options this version refuses unless they are off (gate commands, lanes, the verifier).
type offFlags struct {
	gate      []string
	noGate    bool
	lane      []string
	noLane    bool
	forceLane bool
	verify    bool
}

func (o *offFlags) add(fs *pflag.FlagSet) {
	fs.StringArrayVar(&o.gate, "gate", nil, "gate command (not available in this version; --no-gate is)")
	fs.BoolVar(&o.noGate, "no-gate", false, "run no gate")
	fs.StringSliceVar(&o.lane, "lane", nil, "lane paths (not available in this version; --no-lane is)")
	fs.BoolVar(&o.noLane, "no-lane", false, "no lane")
	fs.BoolVar(&o.forceLane, "force-lane", false, "force the lane (not available in this version)")
	fs.BoolVar(&o.verify, "verify", false, "run the verifier (not available in this version)")
}

// check refuses what is not available and returns the gate flag (false with --no-gate).
func (o *offFlags) check() (*bool, error) {
	switch {
	case len(o.gate) > 0 && !o.noGate:
		return nil, &ops.UnavailableError{What: "running gate commands (--gate)", Detail: "leave --gate out or pass --no-gate"}
	case len(o.lane) > 0 && !o.noLane:
		return nil, &ops.UnavailableError{What: "lane enforcement (--lane)", Detail: "leave --lane out or pass --no-lane"}
	case o.forceLane:
		return nil, &ops.UnavailableError{What: "--force-lane", Detail: "leave it out; lanes are not enforced yet"}
	case o.verify:
		return nil, &ops.UnavailableError{What: "the verifier (--verify)", Detail: "leave --verify out"}
	}
	if o.noGate {
		f := false
		return &f, nil
	}
	return nil, nil
}

// delegateFlags are the options of delegate and run (the old names).
type delegateFlags struct {
	cwd, title, member, backend, provider, model, effort, mode, profile, spec, task, afterMode string

	after    []string
	allowGit bool
	off      offFlags
	wait     int
	// Contest options: refused.
	variants int
	variant  []string
	judge    string
	noJudge  bool
	deadline int
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
	fs.StringVar(&f.member, "member", "", "department member (not available in this version)")
	f.off.add(fs)
	if withWait {
		fs.IntVar(&f.wait, "wait", defaultWait, "seconds to wait for the report (0: return at once)")
	}
	fs.IntVar(&f.variants, "variants", 0, "contest variants (not available in this version)")
	fs.StringArrayVar(&f.variant, "variant", nil, "contest variant (not available in this version)")
	fs.StringVar(&f.judge, "judge", "", "contest judge (not available in this version)")
	fs.BoolVar(&f.noJudge, "no-judge", false, "contest without judge (not available in this version)")
	fs.IntVar(&f.deadline, "deadline", 0, "contest deadline in minutes (not available in this version)")
}

// request builds the delegate request from the flags and the prompt words, refusing what is not available first.
func (f *delegateFlags) request(fs *pflag.FlagSet, words []string) (api.DelegateRequest, error) {
	for _, name := range []string{"variants", "variant", "judge", "no-judge", "deadline"} {
		if fs.Changed(name) {
			return api.DelegateRequest{}, &ops.UnavailableError{What: "contests (--" + name + ")",
				Detail: "delegate one task per variant instead"}
		}
	}
	if f.member != "" {
		return api.DelegateRequest{}, &ops.UnavailableError{What: "department members (--member)",
			Detail: "leave --member out and pass --backend and --model instead"}
	}
	gate, err := f.off.check()
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
		Backend: f.backend, Provider: f.provider, Profile: f.profile, After: splitList(f.after), AfterMode: f.afterMode,
		AllowGit: f.allowGit, Gate: gate, Session: session()}
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
	sum, err := c.Delegate(cmd.Context(), req)
	if err != nil {
		return api.TaskReport{}, api.WaitResult{}, err
	}
	if wait > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Task %s created; waiting up to %d s for its report.\n", sum.ID, wait)
	}
	res, err := waitReports(cmd.Context(), c, cn, ops.Wait{IDs: []string{sum.ID}, Seconds: wait, WakeOnBlocked: true,
		Cwd: req.Cwd})
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
	if r.Response != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimRight(r.Response, "\n"))
	}
	if len(r.FilesChanged) > 0 {
		_, _ = fmt.Fprintf(w, "\nFiles changed: %s\n", strings.Join(r.FilesChanged, ", "))
	}
	if r.Attention != nil {
		_, _ = fmt.Fprintf(w, "\nAttention: BLOCKED (%s)", r.Attention.Reason)
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
