package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// teamWaitMax is the longest wait the daemon serves on a meeting or contest in one request.
const teamWaitMax = 1500

// consultPoll is how often consult --wait looks at the run.
const consultPoll = time.Second

// pollConsult waits up to wait seconds for a consult to leave running.
func pollConsult(cmd *cobra.Command, c *client.Client, taskID string, rec api.Consult, wait int) (api.Consult, error) {
	ctx := cmd.Context()
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	if wait > 0 && rec.Status == "running" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Consult %s started; waiting up to %d s for the verdict.\n", rec.ID, wait)
	}
	for rec.Status == "running" && time.Now().Before(deadline) {
		t := time.NewTimer(consultPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return rec, ctx.Err()
		case <-t.C:
		}
		d, err := c.Task(ctx, taskID)
		if err != nil {
			return rec, err
		}
		for i := range d.Consults {
			if d.Consults[i].ID == rec.ID {
				rec = d.Consults[i]
			}
		}
	}
	return rec, nil
}

// waitAndPrint waits for a task's report after a send and prints it.
func waitAndPrint(cmd *cobra.Command, d deps, _ *client.Client, id string, wait int, head string) error {
	c, cn, err := d.daemonClient(cmd)
	if err != nil {
		return err
	}
	res, err := waitReports(cmd.Context(), c, cn, ops.Wait{IDs: []string{id}, Seconds: wait, WakeOnBlocked: true})
	if err != nil {
		return fmt.Errorf("sent, but reading the report failed: %w", err)
	}
	r := res.Reports[0]
	return emit(cmd, r, func(w io.Writer) {
		_, _ = fmt.Fprintln(w, head)
		printReport(w, r, res.Woke)
	})
}

// ---- meeting ----------------------------------------------------------------------------------------------------

const meetingUsage = "seelegent meeting open|list|status|wait|post|decide|act|cancel|dissent-outcome ..."

func newMeetingCommand(d deps) *cobra.Command {
	var (
		cwdFlag, title, sealed, question string
		options, readFirst               []string
		attendees                        []string
		rounds, openWait                 int
		estimate                         bool
	)
	openUsage := "seelegent meeting open --attendee <id>[:rank] ... [--option text] ... <question|@file>"
	open := leaf(d, "open --attendee M ... <question|@file>", "Open a meeting of department members", "meeting",
		"open", func(_ *cobra.Command, args []string) error {
			if len(args) == 0 && question == "" {
				return usagef("usage: %s", openUsage)
			}
			return onlyOne(args, question, "question")
		},
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			cwd, err := absDir(cwdFlag)
			if err != nil {
				return err
			}
			if cwd == "" {
				if cwd, err = absDir("."); err != nil {
					return err
				}
			}
			q, err := argOrFlag(args, question, "question", cwd)
			if err != nil {
				return err
			}
			seal, err := fileArg(sealed, cwd)
			if err != nil {
				return err
			}
			req := api.MeetingRequest{Question: q, Title: title, Options: options, Context: splitList(readFirst),
				Cwd: cwd, Sealed: seal, MaxRounds: rounds}
			for _, a := range splitList(attendees) {
				id, rank, _ := strings.Cut(a, ":")
				req.Attendees = append(req.Attendees, api.AttendeeRef{Member: id, Rank: rank})
			}
			if estimate {
				est, err := c.EstimateMeeting(cmd.Context(), req)
				if err != nil {
					return err
				}
				return emit(cmd, est, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "The meeting takes at most %d worker run(s).\n", est.Runs)
				})
			}
			m, err := c.OpenMeeting(cmd.Context(), req)
			if err != nil {
				return err
			}
			if openWait > 0 {
				if m, err = c.WaitMeeting(cmd.Context(), m.ID, min(openWait, teamWaitMax)); err != nil {
					return err
				}
			}
			return emitMeeting(cmd, &m)
		})
	fs := open.Flags()
	fs.StringVar(&cwdFlag, "cwd", "", "repository the meeting is about (default: the current folder)")
	fs.StringVar(&title, "title", "", "short title")
	fs.StringArrayVar(&options, "option", nil, "a candidate answer (repeat, 2-6; labeled A, B, ...)")
	fs.StringSliceVar(&readFirst, "context", nil, "repository files attendees read first")
	fs.StringSliceVar(&attendees, "attendee", nil, "member id, or id:rank (repeat or comma separated)")
	fs.StringVar(&sealed, "sealed", "", "your own position (or @file), sealed and shown anonymously in round 2")
	fs.IntVar(&rounds, "rounds", 0, "1-5 rounds (default 3)")
	fs.IntVar(&openWait, "wait", 0, "seconds to wait until the meeting needs you")
	fs.BoolVar(&estimate, "estimate", false, "only print how many runs it takes")
	// The Node version's spelling: --question instead of the argument, --seal for --sealed.
	fs.StringVar(&question, "question", "", "the question (or @file), instead of the argument")
	fs.StringVar(&sealed, "seal", "", "same as --sealed")
	hiddenFlag(fs, "question", "seal")

	list := leaf(d, "list", "The meetings, newest first", "meeting", "list", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			ms, err := c.Meetings(cmd.Context(), 0)
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(ms), func(w io.Writer) {
				if len(ms) == 0 {
					_, _ = fmt.Fprintln(w, "No meetings.")
				}
				for i := range ms {
					m := &ms[i]
					_, _ = fmt.Fprintf(w, "%-12s %-12s round %d/%d  %s\n", m.ID, m.Status, m.Round, m.MaxRounds, m.Title)
				}
			})
		})
	status := leaf(d, "status <id>", "A meeting's state and minutes", "meeting", "status",
		exactArgs(1, "seelegent meeting status <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			m, err := c.Meeting(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emitMeeting(cmd, &m)
		})
	var wait int
	waitCmd := leaf(d, "wait <id> [--seconds N]", "Wait until the meeting needs you or ends", "meeting", "wait",
		exactArgs(1, "seelegent meeting wait <id> [--seconds N]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			if wait < 0 || wait > teamWaitMax {
				return usagef("--seconds must be 0..%d", teamWaitMax)
			}
			m, err := c.WaitMeeting(cmd.Context(), args[0], wait)
			if err != nil {
				return err
			}
			return emitMeeting(cmd, &m)
		})
	waitCmd.Flags().IntVar(&wait, "seconds", defaultWait, "seconds to wait (max 1500)")
	var answers []string
	var note string
	post := leaf(d, "post <id> --answer q1=text ... [--note T]", "Answer the attendees' questions", "meeting", "post",
		checked(exactArgs(1, "seelegent meeting post <id> --answer <qid>=<text> ... [--note text]"),
			func(*cobra.Command, []string) error {
				for _, a := range answers {
					if !strings.Contains(a, "=") {
						return usagef("--answer must be <question id>=<text> (got %q)", a)
					}
				}
				return nil
			}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			req := api.MeetingPostRequest{Note: note}
			for _, a := range answers {
				id, text, _ := strings.Cut(a, "=")
				req.Answers = append(req.Answers, api.QuestionAnswer{ID: strings.TrimSpace(id), Answer: text})
			}
			m, err := c.PostMeeting(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return emitMeeting(cmd, &m)
		})
	post.Flags().StringArrayVar(&answers, "answer", nil, "an answer, <question id>=<text> (repeat)")
	post.Flags().StringVar(&note, "note", "", "a note shared with every attendee")
	var rationale, option string
	decide := leaf(d, "decide <id> [--option K] [--rationale T] <decision|@file>", "Record your decision", "meeting",
		"decide", minArgs("seelegent meeting decide <id> [--option key] [--rationale text] <decision|@file>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			text, err := fileArg(strings.Join(args[1:], " "), "")
			if err != nil {
				return err
			}
			m, err := c.DecideMeeting(cmd.Context(), args[0], api.MeetingDecideRequest{Decision: text,
				Rationale: rationale, Option: option})
			if err != nil {
				return err
			}
			return emitMeeting(cmd, &m)
		})
	decide.Flags().StringVar(&rationale, "rationale", "", "why")
	decide.Flags().StringVar(&option, "option", "", "the option key you chose (A-F)")
	var actMember, actBackend, actModel, actCwd string
	var actItems []string
	actUsage := "seelegent meeting act <id> [--member id] [--backend b] [--model m] [--cwd dir] <brief|@file>"
	act := leaf(d, "act <id> [--member M] <task brief|@file>", "Delegate a task from the decision", "meeting", "act",
		func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0, len(args) == 1 && len(actItems) == 0:
				return usagef("usage: %s", actUsage)
			case len(args) > 1 && len(actItems) > 0:
				return usagef("give the brief once: as the argument or with --item")
			}
			return nil
		},
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			// The old form gives one or more briefs with --item, each a task with the same options.
			briefs := actItems
			if len(briefs) == 0 {
				briefs = []string{strings.Join(args[1:], " ")}
			}
			cwd, err := absDir(actCwd)
			if err != nil {
				return err
			}
			var req api.MeetingActRequest
			for _, b := range briefs {
				text, err := fileArg(b, "")
				if err != nil {
					return err
				}
				req.Items = append(req.Items, api.ActItem{Text: text, Member: actMember, Backend: actBackend,
					Model: actModel, Cwd: cwd})
			}
			res, err := c.ActMeeting(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return emit(cmd, res, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "Delegated: %s\n", strings.Join(res.Tasks, ", "))
			})
		})
	act.Flags().StringVar(&actMember, "member", "", "member for the task")
	act.Flags().StringVar(&actBackend, "backend", "", "backend for the task")
	act.Flags().StringVar(&actModel, "model", "", "model for the task")
	act.Flags().StringVar(&actCwd, "cwd", "", "folder of the task (default: the meeting's)")
	act.Flags().StringArrayVar(&actItems, "item", nil, "a task brief (or @file), instead of the argument (repeat)")
	hiddenFlag(act.Flags(), "item")
	cancel := leaf(d, "cancel <id>", "Cancel a meeting", "meeting", "cancel", exactArgs(1, "seelegent meeting cancel <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			m, err := c.CancelMeeting(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emitMeeting(cmd, &m)
		})
	outcome := leaf(d, "dissent-outcome <id> <dissent-id> vindicated|dismissed", "Confirm or dismiss a dissent later",
		"meeting", "dissent_outcome", exactArgs(3, "seelegent meeting dissent-outcome <id> <dissent-id> vindicated|dismissed"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			m, err := c.DissentOutcome(cmd.Context(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return emitMeeting(cmd, &m)
		})
	// dissent is the Node version's name.
	outcome.Aliases = []string{"dissent_outcome", "dissent"}
	return group("meeting <command>", "Structured meetings of department members before a decision", meetingUsage,
		open, list, status, waitCmd, post, decide, act, cancel, outcome)
}

// emitMeeting prints a meeting: its state, the pending questions and the minutes.
func emitMeeting(cmd *cobra.Command, m *api.Meeting) error {
	return emit(cmd, m, func(w io.Writer) {
		_, _ = fmt.Fprintf(w, "Meeting %s [%s], round %d of %d\n", m.ID, m.Status, m.Round, m.MaxRounds)
		for _, q := range m.LeadQA {
			if q.AnsweredAt == "" {
				_, _ = fmt.Fprintf(w, "Question %s from %s: %s\n", q.ID, q.Label, q.Question)
			}
		}
		if m.Error != "" {
			_, _ = fmt.Fprintf(w, "Error: %s\n", m.Error)
		}
		if m.Minutes != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimRight(m.Minutes, "\n"))
		}
	})
}

// ---- contest ----------------------------------------------------------------------------------------------------

const contestUsage = "seelegent contest list|status|wait|diff|pick|cancel ..."

func newContestCommand(d deps) *cobra.Command {
	list := leaf(d, "list", "The contests, newest first", "contest", "list", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			cs, err := c.Contests(cmd.Context(), 0)
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(cs), func(w io.Writer) {
				if len(cs) == 0 {
					_, _ = fmt.Fprintln(w, "No contests.")
				}
				for i := range cs {
					k := &cs[i]
					_, _ = fmt.Fprintf(w, "%-12s %-10s %d variants  %s\n", k.ID, k.Status, k.Variants, k.Title)
				}
			})
		})
	var full bool
	status := leaf(d, "status <id>", "A contest's scoreboard, judge and proposal", "contest", "status",
		exactArgs(1, "seelegent contest status <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			v, err := c.Contest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if full {
				return printJSON(cmd.OutOrStdout(), v)
			}
			return emitContest(cmd, &v)
		})
	// The Node version's --full printed the raw record: the whole contest as JSON, as --json does now.
	status.Flags().BoolVar(&full, "full", false, "the whole record as JSON (same as --json)")
	hiddenFlag(status.Flags(), "full")
	var wait int
	waitCmd := leaf(d, "wait <id> [--seconds N]", "Wait until the contest is ready or ended", "contest", "wait",
		exactArgs(1, "seelegent contest wait <id> [--seconds N]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			if wait < 0 {
				return usagef("--seconds must be 0 or more")
			}
			v, err := waitContest(cmd.Context(), c, args[0], wait)
			if err != nil {
				return err
			}
			return emitContest(cmd, &v)
		})
	waitCmd.Flags().IntVar(&wait, "seconds", defaultWait, "seconds to wait")
	diff := leaf(d, "diff <id> <variant>", "A variant's diff against the contest's base", "contest", "diff",
		exactArgs(2, "seelegent contest diff <id> <variant>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			v, err := c.ContestDiff(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return emit(cmd, v, func(w io.Writer) { _, _ = fmt.Fprint(w, v.Diff) })
		})
	var winner string
	pick := leaf(d, "pick <id> [--winner V2]", "Apply the winner to your working tree", "contest", "pick",
		exactArgs(1, "seelegent contest pick <id> [--winner <variant>]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			v, err := c.PickContest(cmd.Context(), args[0], api.ContestPickRequest{Winner: winner, By: "lead"})
			if err != nil {
				return err
			}
			return emitContest(cmd, &v)
		})
	pick.Flags().StringVar(&winner, "winner", "", "variant label (default: the proposal)")
	cancel := leaf(d, "cancel <id>", "Stop every variant and remove the worktrees", "contest", "cancel",
		exactArgs(1, "seelegent contest cancel <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			v, err := c.CancelContest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emitContest(cmd, &v)
		})
	return group("contest <command>", "Contests: one brief run as variants in separate worktrees", contestUsage,
		list, status, waitCmd, diff, pick, cancel)
}

// waitContest waits up to seconds for a contest, in waits the daemon serves.
func waitContest(ctx context.Context, c *client.Client, id string, seconds int) (api.Contest, error) {
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		left := int(time.Until(deadline).Seconds())
		v, err := c.WaitContest(ctx, id, max(0, min(left, teamWaitMax)))
		if err != nil || left <= teamWaitMax {
			return v, err
		}
		switch v.Status {
		case api.ContestStatusReady, api.ContestStatusApplied, api.ContestStatusCancelled, api.ContestStatusFailed:
			return v, nil
		}
	}
}

// emitContest prints a contest: the scoreboard, the judge and the proposal.
func emitContest(cmd *cobra.Command, v *api.Contest) error {
	return emit(cmd, v, func(w io.Writer) {
		_, _ = fmt.Fprintf(w, "Contest %s [%s]: %s\n", v.ID, v.Status, v.Title)
		for i := range v.Scoreboard {
			r := &v.Scoreboard[i]
			dq := ""
			if r.Disqualified != "" {
				dq = "  disqualified: " + r.Disqualified
			}
			_, _ = fmt.Fprintf(w, "  %-3s %-10s %-6s gate %-6s files %d +%d -%d tokens %d%s\n", r.Label, r.Status,
				r.Backend, r.Gate, r.Files, r.Added, r.Removed, r.Tokens, dq)
		}
		if len(v.Scoreboard) == 0 {
			for i := range v.Variants {
				vr := &v.Variants[i]
				_, _ = fmt.Fprintf(w, "  %-3s %-10s %s %s\n", vr.Label, vr.Status, vr.TaskID, vr.Backend)
			}
		}
		if j := v.JudgeRun; j != nil {
			_, _ = fmt.Fprintf(w, "Judge %s [%s]: winner %s, %d verified citation(s)\n", j.Member, j.Status,
				j.WinnerLabel, j.Verified)
		}
		if p := v.Proposal; p != nil {
			_, _ = fmt.Fprintf(w, "Proposed winner: %s (%s)\n", p.Winner, p.Source)
		}
		if v.Applied != nil {
			_, _ = fmt.Fprintf(w, "Applied %s: %s\n", v.Winner, strings.Join(v.Applied.Files, ", "))
		}
		if v.LastPickError != nil {
			_, _ = fmt.Fprintf(w, "Last pick of %s did not apply: %s\n", v.LastPickError.Winner, v.LastPickError.Detail)
		}
		for _, warn := range v.Warnings {
			_, _ = fmt.Fprintf(w, "Warning: %s\n", warn)
		}
		if v.Error != "" {
			_, _ = fmt.Fprintf(w, "Error: %s\n", v.Error)
		}
		if v.Status == api.ContestStatusReady {
			_, _ = fmt.Fprintf(w, "Read the proposed winner, then `seelegent contest pick %s`.\n", v.ID)
		}
	})
}

// contestFlags are the contest options of delegate and run.
type contestFlags struct {
	variants int
	variant  []string
	judge    string
	noJudge  bool
	deadline int
}

func (f *contestFlags) add(fs *pflag.FlagSet) {
	fs.IntVar(&f.variants, "variants", 0, "run the brief as a contest of 2-5 variants in separate worktrees")
	fs.StringArrayVar(&f.variant, "variant", nil, "a variant's spec: backend[:model], or member=id, provider=id, "+
		"backend=b, model=m comma separated (repeat; V1, V2, ...)")
	fs.StringVar(&f.judge, "judge", "", "contest judge (member id; default: the first peer)")
	fs.BoolVar(&f.noJudge, "no-judge", false, "contest without judge (machine scoring only)")
	fs.IntVar(&f.deadline, "deadline", 0, "contest deadline in minutes (default 60)")
}

// set reports whether any contest flag was given.
func (f *contestFlags) set(fs *pflag.FlagSet) bool {
	for _, name := range []string{"variants", "variant", "judge", "no-judge", "deadline"} {
		if fs.Changed(name) {
			return true
		}
	}
	return false
}

// request builds the contest request around req.
func (f *contestFlags) request(req api.DelegateRequest) (api.ContestRequest, error) {
	if len(req.After) > 0 {
		return api.ContestRequest{}, usagef("a contest cannot wait for other tasks (--after)")
	}
	if f.judge != "" && f.noJudge {
		return api.ContestRequest{}, usagef("--judge cannot go with --no-judge")
	}
	out := api.ContestRequest{DelegateRequest: req, Variants: f.variants, Judge: f.judge, NoJudge: f.noJudge,
		DeadlineMinutes: f.deadline}
	for _, raw := range f.variant {
		var s api.VariantSpec
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				s.Backend, s.Model, _ = strings.Cut(part, ":")
				continue
			}
			switch k {
			case "backend":
				s.Backend = v
			case "model":
				s.Model = v
			case "member":
				s.Member = v
			case "provider":
				s.Provider = v
			default:
				return api.ContestRequest{}, usagef("--variant: unknown key %q (backend, model, member, provider)", k)
			}
		}
		out.VariantSpecs = append(out.VariantSpecs, s)
	}
	return out, nil
}

// runContest creates the contest and waits up to wait seconds for it.
func runContest(cmd *cobra.Command, d deps, req api.ContestRequest, wait int) (api.Contest, error) {
	c, _, err := d.daemonClient(cmd)
	if err != nil {
		return api.Contest{}, err
	}
	v, err := c.CreateContest(cmd.Context(), req)
	if err != nil {
		return api.Contest{}, err
	}
	if wait <= 0 {
		return v, nil
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Contest %s started with %d variants; waiting up to %d s.\n", v.ID,
		len(v.Variants), wait)
	id := v.ID
	if v, err = waitContest(cmd.Context(), c, id, wait); err != nil {
		return api.Contest{}, fmt.Errorf("contest %s was created, but waiting for it failed: %w", id, err)
	}
	return v, nil
}
