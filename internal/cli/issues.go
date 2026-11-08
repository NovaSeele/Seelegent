package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// The issues command (milestone 15): GitHub issues to tickets to draft pull requests, as the MCP tool issues
// (docs/github.md). There is no command for the GitHub token: only the dashboard stores it.

const issuesUsage = "seelegent issues repos|connect|init|create-remote|sync|list|add|suggest|triage|tickets|ticket|" +
	"add-task|pr|link-pr|comment|drop|cleanup|shared-dirs|fix-checks ..."

// repoArg resolves a repo id or folder (relative to the process's folder).
func repoArg(cmd *cobra.Command, c *client.Client, repo string) (string, error) {
	abs, err := absDir(repo)
	if err != nil {
		return "", err
	}
	return ops.RepoID(cmd.Context(), c, repo, abs)
}

// workFlags are the worker options of triage assign and add-task: the gate and lane flags of delegate without
// --force-lane and --verify.
type workFlags struct {
	member, backend, model string
	gates                  gateFlags
}

func (f *workFlags) add(fs *pflag.FlagSet) {
	fs.StringVar(&f.member, "member", "", "department member")
	fs.StringVar(&f.backend, "backend", "", "agy, claude or codex")
	fs.StringVar(&f.model, "model", "", "model id or tier")
	fs.StringArrayVar(&f.gates.gate, "gate", nil, "gate command to run after the report (repeatable)")
	fs.BoolVar(&f.gates.detect, "gate-detect", false, "add the repository's detected test, lint and build commands")
	fs.BoolVar(&f.gates.noGate, "no-gate", false, "run no gate commands (lane and hygiene checks still run)")
	fs.StringSliceVar(&f.gates.lane, "lane", nil, "files and globs the worker may change (default: the whole worktree)")
	fs.BoolVar(&f.gates.noLane, "no-lane", false, "no lane")
}

func (f *workFlags) options(fs *pflag.FlagSet) (api.TicketTaskOptions, error) {
	g, err := f.gates.options(fs)
	if err != nil {
		return api.TicketTaskOptions{}, err
	}
	return api.TicketTaskOptions{Member: f.member, Backend: f.backend, Model: f.model, Gate: g.gate,
		GateCommands: g.commands, Lane: g.lane, NoLane: g.noLane}, nil
}

// waitTicketTask waits up to wait seconds for a task a ticket action started and prints its report. since is a mark
// taken before the action, so a flag the new task has from the start wakes the wait (N2).
func waitTicketTask(cmd *cobra.Command, d deps, taskID string, wait int, since time.Time, v any, head string) error {
	if wait <= 0 || taskID == "" {
		return emit(cmd, v, func(w io.Writer) { _, _ = fmt.Fprintln(w, head) })
	}
	return waitAndPrint(cmd, d, nil, taskID, min(wait, 3000), since, head)
}

// listState is the state issues list asks for: --state, or the Node version's --all and --closed (one of them).
func listState(state string, all, closed bool) (string, error) {
	given := 0
	for _, on := range []bool{state != "", all, closed} {
		if on {
			given++
		}
	}
	switch {
	case given > 1:
		return "", usagef("give one of --state, --all and --closed")
	case all:
		return "all", nil
	case closed:
		return "closed", nil
	}
	return state, nil
}

func printIssue(w io.Writer, i *api.Issue) {
	line := fmt.Sprintf("%-10s %s", i.Key, i.Title)
	if i.State != "open" {
		line += " [" + i.State + "]"
	}
	if i.Decision != nil {
		line += " — " + i.Decision.Action
	} else if i.Suggestion != nil {
		line += " — suggested " + i.Suggestion.Action
	}
	if i.Ticket != nil {
		line += fmt.Sprintf(" (ticket %s, %s)", i.Ticket.ID, i.Ticket.Status)
	}
	_, _ = fmt.Fprintln(w, line)
}

func printTicket(w io.Writer, t *api.TicketSummary) {
	line := fmt.Sprintf("%s  %-8s %-8s %s", t.ID, t.IssueKey, t.Status, t.Issue.Title)
	if t.PR != nil && t.PR.URL != "" {
		line += "  " + t.PR.URL
	} else if t.PR != nil && t.PR.ManualURL != "" {
		line += "  open by hand: " + t.PR.ManualURL
	}
	if t.NeedsAttention != "" {
		line += "  [" + t.NeedsAttention + "]"
	}
	if t.Error != "" {
		line += "  error: " + t.Error
	}
	_, _ = fmt.Fprintln(w, line)
}

func printRepo(w io.Writer, r *api.Repo) {
	where := "no GitHub remote"
	switch {
	case r.GitHub:
		where = r.Owner + "/" + r.Repo
	case r.NotGitHub != "":
		where = "not GitHub: " + r.NotGitHub
	}
	_, _ = fmt.Fprintf(w, "%s  %s  (%s; inbox %d, working %d, ready for a PR %d, sent %d)\n", r.ID, r.Path, where,
		r.Counts.Inbox, r.Counts.Working, r.Counts.WaitingPr, r.Counts.Sent)
}

func newIssuesCommand(d deps) *cobra.Command {
	return group("issues", "GitHub issues to tickets and draft pull requests", issuesUsage,
		append(issueRepoCommands(d), issueTicketCommands(d)...)...)
}

func issueRepoCommands(d deps) []*cobra.Command {
	repos := leaf(d, "repos", "The connected repos and the GitHub access tier", "issues", "repos", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			list, err := c.Repos(cmd.Context())
			if err != nil {
				return err
			}
			acc, err := c.GitHubAccess(cmd.Context(), false)
			if err != nil {
				return err
			}
			return emit(cmd, map[string]any{"access": acc, "repos": api.NonNil(list)}, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "GitHub access: %s\n", acc.Tier)
				for i := range list {
					printRepo(w, &list[i])
				}
			})
		})
	connect := leaf(d, "connect <path>", "Connect a local repository", "issues", "connect",
		exactArgs(1, "seelegent issues connect <path>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			p, err := absDir(args[0])
			if err != nil {
				return err
			}
			r, err := c.ConnectRepo(cmd.Context(), api.RepoConnectRequest{Path: p})
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) { printRepo(w, &r) })
		})
	var initName string
	var noFirst bool
	initCmd := leaf(d, "init <path> [--name N] [--no-first-commit]", "Make a git repository and connect it", "issues",
		"init", exactArgs(1, "seelegent issues init <path> [--name N] [--no-first-commit]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			p, err := absDir(args[0])
			if err != nil {
				return err
			}
			first := !noFirst
			r, err := c.ConnectRepo(cmd.Context(), api.RepoConnectRequest{Path: p, Init: true, Name: initName,
				FirstCommit: &first})
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) { printRepo(w, &r) })
		})
	initCmd.Flags().StringVar(&initName, "name", "", "repository name (default: the folder name)")
	initCmd.Flags().BoolVar(&noFirst, "no-first-commit", false, "no empty first commit")
	var remoteName string
	var public bool
	remote := leaf(d, "create-remote <repo> [--name N] [--public]", "Create the GitHub repository (gh or token tier)",
		"issues", "create_remote", exactArgs(1, "seelegent issues create-remote <repo> [--name N] [--public]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			private := !public
			r, err := c.CreateRemote(cmd.Context(), id, api.RemoteRequest{Name: remoteName, Private: &private})
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) { printRepo(w, &r) })
		})
	remote.Flags().StringVar(&remoteName, "name", "", "repository name (default: the folder name)")
	remote.Flags().BoolVar(&public, "public", false, "a public repository (default private)")
	sync := leaf(d, "sync <repo>", "Refresh the inbox from GitHub", "issues", "sync",
		exactArgs(1, "seelegent issues sync <repo>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			res, err := c.SyncRepo(cmd.Context(), id)
			if err != nil {
				return err
			}
			return emit(cmd, res, func(w io.Writer) {
				if res.Skipped != "" {
					_, _ = fmt.Fprintln(w, "Not synced: "+res.Skipped)
					return
				}
				_, _ = fmt.Fprintf(w, "%d added, %d updated, %d closed\n", res.Added, res.Updated, res.Closed)
			})
		})
	var state string
	var all, closed bool
	list := leaf(d, "list <repo> [--state open|closed|all]", "The inbox", "issues", "list",
		checked(exactArgs(1, "seelegent issues list <repo> [--state open|closed|all]"),
			func(*cobra.Command, []string) error {
				_, err := listState(state, all, closed)
				return err
			}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			st, err := listState(state, all, closed)
			if err != nil {
				return err
			}
			items, err := c.Issues(cmd.Context(), id, st)
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(items), func(w io.Writer) {
				for i := range items {
					printIssue(w, &items[i])
				}
			})
		})
	list.Flags().StringVar(&state, "state", "", "open (default), closed or all")
	list.Flags().BoolVar(&all, "all", false, "same as --state all")
	list.Flags().BoolVar(&closed, "closed", false, "same as --state closed")
	hiddenFlag(list.Flags(), "all", "closed")
	var title, bodyFlag string
	add := leaf(d, "add <repo> --title T [body|@file]", "Add a local issue", "issues", "add",
		checked(minArgs("seelegent issues add <repo> --title T [body|@file]"),
			func(_ *cobra.Command, args []string) error {
				switch {
				case len(args) > 2:
					return usagef("usage: seelegent issues add <repo> --title T [body|@file] (quote the body)")
				case strings.TrimSpace(title) == "":
					return usagef("--title is required")
				}
				return onlyOne(args[1:], bodyFlag, "body")
			}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			body, err := argOrFlag(args[1:], bodyFlag, "body", "")
			if err != nil {
				return err
			}
			i, err := c.AddIssue(cmd.Context(), id, api.IssueInput{Title: title, Body: body})
			if err != nil {
				return err
			}
			return emit(cmd, i, func(w io.Writer) { printIssue(w, &i) })
		})
	add.Flags().StringVar(&title, "title", "", "issue title")
	add.Flags().StringVar(&bodyFlag, "body", "", "the body (or @file), instead of the argument")
	hiddenFlag(add.Flags(), "body")
	var sugMember, sugReason string
	suggest := leaf(d, "suggest <repo> <key> assign|meeting|skip [--member M] [--reason T]",
		"Record a suggestion (no side effects)", "issues", "suggest",
		exactArgs(3, "seelegent issues suggest <repo> <key> assign|meeting|skip [--member M] [--reason T]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			i, err := c.SuggestIssue(cmd.Context(), id, ops.IssueKey(args[1]), api.SuggestRequest{Action: args[2],
				Member: sugMember, Reason: sugReason})
			if err != nil {
				return err
			}
			return emit(cmd, i, func(w io.Writer) { printIssue(w, &i) })
		})
	suggest.Flags().StringVar(&sugMember, "member", "", "member for assign")
	suggest.Flags().StringVar(&sugReason, "reason", "", "one-line reason")
	sharedClear, noShared := false, false
	var sharedFlag []string
	// sharedDirs are the folders given: the arguments and the old --shared-dir.
	sharedDirs := func(args []string) []string { return splitList(append(slices.Clone(args[1:]), sharedFlag...)) }
	shared := leaf(d, "shared-dirs <repo> [dir...] [--clear]",
		"Gitignored folders linked into new ticket worktrees (none given: print them)", "issues", "repo_settings",
		checked(minArgs("seelegent issues shared-dirs <repo> [dir...] [--clear]"),
			func(_ *cobra.Command, args []string) error {
				if (sharedClear || noShared) && len(sharedDirs(args)) > 0 {
					return usagef("--clear links no folders; give folders or --clear, not both")
				}
				return nil
			}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			dirs := sharedDirs(args)
			var r api.Repo
			if len(dirs) == 0 && !sharedClear && !noShared {
				list, err := c.Repos(cmd.Context())
				if err != nil {
					return err
				}
				for i := range list {
					if list[i].ID == id {
						r = list[i]
					}
				}
				if r.ID == "" {
					return fmt.Errorf("%w: %s", ops.ErrRepoNotConnected, args[0])
				}
			} else if r, err = c.UpdateRepo(cmd.Context(), id, api.RepoPatch{SharedDirs: api.NonNil(dirs)}); err != nil {
				return err
			}
			now := api.NonNil(r.SharedDirs)
			return emit(cmd, map[string]any{"id": r.ID, "path": r.Path, "sharedDirs": now}, func(w io.Writer) {
				for _, s := range now {
					_, _ = fmt.Fprintln(w, s)
				}
			})
		})
	shared.Flags().BoolVar(&sharedClear, "clear", false, "link no folders")
	// The Node version's form: issues repo-settings <repo> [--shared-dir D]... [--no-shared-dirs].
	shared.Aliases = []string{"repo-settings"}
	shared.Flags().StringArrayVar(&sharedFlag, "shared-dir", nil, "a folder to link (repeat or comma separated)")
	shared.Flags().BoolVar(&noShared, "no-shared-dirs", false, "same as --clear")
	hiddenFlag(shared.Flags(), "shared-dir", "no-shared-dirs")
	return []*cobra.Command{repos, connect, initCmd, remote, sync, list, add, suggest, newTriageCommand(d), shared}
}

func newTriageCommand(d deps) *cobra.Command {
	var (
		f                        workFlags
		reason, prompt, question string
		attendees, options, ctxs []string
		wait                     int
	)
	usage := "seelegent issues triage <repo> <key> assign|meeting|skip [--reason T] [--prompt T|@file] [flags]"
	cmd := leaf(d, "triage <repo> <key> assign|meeting|skip [flags]",
		"Decide an issue: assign makes a ticket with a task, meeting opens a meeting, skip records it", "issues",
		// The Node version took the brief as a fourth argument (`... assign @brief.md`); it still does.
		"triage", checked(func(_ *cobra.Command, args []string) error {
			if len(args) < 3 {
				return usagef("usage: %s", usage)
			}
			return nil
		}, func(_ *cobra.Command, args []string) error { return onlyOne(args[3:], prompt, "prompt") }),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			opt, err := f.options(cmd.Flags())
			if err != nil {
				return err
			}
			id, err := repoArg(cmd, c, args[0])
			if err != nil {
				return err
			}
			brief, err := argOrFlag(args[3:], prompt, "prompt", "")
			if err != nil {
				return err
			}
			req := api.TriageRequest{Action: args[2], By: "lead", Reason: reason, Prompt: brief, Question: question,
				Options: options, Context: splitList(ctxs), TicketTaskOptions: opt}
			for _, a := range splitList(attendees) {
				m, rank, _ := strings.Cut(a, ":")
				req.Attendees = append(req.Attendees, api.AttendeeRef{Member: m, Rank: rank})
			}
			mark := time.Now()
			res, err := c.TriageIssue(cmd.Context(), id, ops.IssueKey(args[1]), req)
			if err != nil {
				return err
			}
			head := "Triaged " + res.Issue.Key + ": " + args[2] + "."
			switch {
			case res.Ticket != nil && res.TaskID != "":
				head = fmt.Sprintf("Ticket %s in %s; task %s started.", res.Ticket.ID, res.Ticket.Worktree, res.TaskID)
			case res.Ticket != nil && res.MeetingID != "":
				head = fmt.Sprintf("Ticket %s; meeting %s opened.", res.Ticket.ID, res.MeetingID)
			}
			return waitTicketTask(cmd, d, res.TaskID, wait, mark, res, head)
		})
	fs := cmd.Flags()
	f.add(fs)
	fs.StringVar(&reason, "reason", "", "one-line reason (required for skip)")
	fs.StringVar(&prompt, "prompt", "", "assign: the brief (or @file); the issue is appended as untrusted text")
	fs.StringVar(&question, "question", "", "meeting: the question (default: the issue title)")
	fs.StringSliceVar(&attendees, "attendee", nil, "meeting: attendee id[:rank] (repeatable)")
	fs.StringArrayVar(&options, "option", nil, "meeting: a candidate answer (repeatable)")
	fs.StringSliceVar(&ctxs, "context", nil, "meeting: repo-relative files attendees read first")
	fs.IntVar(&wait, "wait", 0, "assign: seconds to wait for the task's report (default 0)")
	return cmd
}

func issueTicketCommands(d deps) []*cobra.Command {
	var repoFlag string
	var statuses []string
	tickets := leaf(d, "tickets [--repo R] [--status s,...]", "The tickets", "issues", "tickets", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			id := ""
			if repoFlag != "" {
				var err error
				if id, err = repoArg(cmd, c, repoFlag); err != nil {
					return err
				}
			}
			list, err := c.Tickets(cmd.Context(), id, splitList(statuses))
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(list), func(w io.Writer) {
				for i := range list {
					printTicket(w, &list[i])
				}
			})
		})
	tickets.Flags().StringVar(&repoFlag, "repo", "", "only this repo (id or path)")
	tickets.Flags().StringSliceVar(&statuses, "status", nil, "only these statuses")
	show := ticketLeaf(d, "ticket", "One ticket with its tasks, gates, files and log", "ticket",
		func(cmd *cobra.Command, c *client.Client, id string, _ []string) (api.Ticket, error) {
			return c.Ticket(cmd.Context(), id)
		})
	var f workFlags
	var addWait int
	addTask := leaf(d, "add-task <ticket> [flags] <prompt|@file>", "Another task in the ticket's worktree", "issues",
		"add_task", minArgs("seelegent issues add-task <ticket> [flags] <prompt|@file>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			if len(args) < 2 {
				return usagef("usage: seelegent issues add-task <ticket> [flags] <prompt|@file>")
			}
			opt, err := f.options(cmd.Flags())
			if err != nil {
				return err
			}
			brief, err := fileArg(strings.Join(args[1:], " "), "")
			if err != nil {
				return err
			}
			mark := time.Now()
			res, err := c.TicketTask(cmd.Context(), args[0], api.TicketTaskRequest{Prompt: brief, TicketTaskOptions: opt})
			if err != nil {
				return err
			}
			return waitTicketTask(cmd, d, res.TaskID, addWait, mark, res, "Task "+res.TaskID+" started.")
		})
	f.add(addTask.Flags())
	addTask.Flags().IntVar(&addWait, "wait", 0, "seconds to wait for the report (default 0)")
	var rf workFlags
	var retryWait int
	retry := leaf(d, "retry <ticket> [flags] [prompt|@file]", "Start the task of a failed ticket again", "issues",
		"retry", minArgs("seelegent issues retry <ticket> [flags] [prompt|@file]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			opt, err := rf.options(cmd.Flags())
			if err != nil {
				return err
			}
			brief, err := fileArg(strings.Join(args[1:], " "), "")
			if err != nil {
				return err
			}
			mark := time.Now()
			res, err := c.RetryTicket(cmd.Context(), args[0], api.TicketTaskRequest{Prompt: brief, TicketTaskOptions: opt})
			if err != nil {
				return err
			}
			return waitTicketTask(cmd, d, res.TaskID, retryWait, mark, res, "Task "+res.TaskID+" started.")
		})
	rf.add(retry.Flags())
	retry.Flags().IntVar(&retryWait, "wait", 0, "seconds to wait for the report (default 0)")
	var prTitle, prBody, commitMsg string
	var ready bool
	pr := ticketLeaf(d, "pr", "Commit, push and open a draft pull request", "pr",
		func(cmd *cobra.Command, c *client.Client, id string, _ []string) (api.Ticket, error) {
			body, err := fileArg(prBody, "")
			if err != nil {
				return api.Ticket{}, err
			}
			draft := !ready
			return c.TicketPR(cmd.Context(), id, api.PRRequest{Title: prTitle, Body: body, Draft: &draft,
				CommitMessage: commitMsg})
		})
	pr.Flags().StringVar(&prTitle, "title", "", `title (default "#n: <issue title>")`)
	pr.Flags().StringVar(&prBody, "body", "", "body or @file (default: a generated summary)")
	pr.Flags().StringVar(&commitMsg, "commit-message", "", "message for uncommitted changes")
	pr.Flags().BoolVar(&ready, "ready", false, "open it ready for review instead of as a draft")
	link := leaf(d, "link-pr <ticket> <number>", "Record a pull request opened by hand", "issues", "link_pr",
		exactArgs(2, "seelegent issues link-pr <ticket> <number>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			n, err := strconv.Atoi(strings.TrimPrefix(args[1], "#"))
			if err != nil || n < 1 {
				return usagef("the pull request number must be 1 or more")
			}
			t, err := c.LinkPR(cmd.Context(), args[0], api.LinkPRRequest{Number: n})
			if err != nil {
				return err
			}
			return emit(cmd, t, func(w io.Writer) { printTicket(w, &t.TicketSummary) })
		})
	comment := ticketLeaf(d, "comment", "Comment on the ticket's issue", "comment",
		func(cmd *cobra.Command, c *client.Client, id string, rest []string) (api.Ticket, error) {
			body, err := fileArg(strings.Join(rest, " "), "")
			if err != nil {
				return api.Ticket{}, err
			}
			if strings.TrimSpace(body) == "" {
				return api.Ticket{}, usagef("usage: seelegent issues comment <ticket> <body|@file>")
			}
			return c.TicketComment(cmd.Context(), id, api.CommentRequest{Body: body})
		})
	comment.Use, comment.Args = "comment <ticket> <body|@file>", minArgs("seelegent issues comment <ticket> <body|@file>")
	var dropReason string
	drop := ticketLeaf(d, "drop", "End a ticket without a pull request", "drop",
		func(cmd *cobra.Command, c *client.Client, id string, _ []string) (api.Ticket, error) {
			return c.DropTicket(cmd.Context(), id, api.DropRequest{Reason: dropReason})
		})
	drop.Flags().StringVar(&dropReason, "reason", "", "one-line reason")
	cleanup := ticketLeaf(d, "cleanup", "Remove a done or dropped ticket's clean worktree (the branch stays)",
		"cleanup", func(cmd *cobra.Command, c *client.Client, id string, _ []string) (api.Ticket, error) {
			return c.CleanupTicket(cmd.Context(), id)
		})
	return []*cobra.Command{tickets, show, addTask, retry, pr, link, comment, drop, cleanup, newFixChecksCommand(d)}
}

// ticketLeaf is a ticket action that takes the ticket id and prints the ticket.
func ticketLeaf(d deps, name, short, action string,
	run func(cmd *cobra.Command, c *client.Client, id string, rest []string) (api.Ticket, error)) *cobra.Command {
	usage := "seelegent issues " + name + " <ticket>"
	return leaf(d, name+" <ticket>", short, "issues", action, exactArgs(1, usage),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			t, err := run(cmd, c, args[0], args[1:])
			if err != nil {
				return err
			}
			return emit(cmd, t, func(w io.Writer) {
				printTicket(w, &t.TicketSummary)
				if t.PR != nil && t.PR.ManualURL != "" {
					_, _ = fmt.Fprintln(w, "Open the pull request with the link above, then: seelegent issues link-pr "+
						t.ID+" <number>")
				}
			})
		})
}

func newFixChecksCommand(d deps) *cobra.Command {
	var member, extra, backend, model string
	var wait int
	cmd := leaf(d, "fix-checks <ticket> [--member M] [--extra T|@file] [--wait N]",
		"Start a task with the failing CI checks of the ticket's pull request", "issues", "fix_checks",
		exactArgs(1, "seelegent issues fix-checks <ticket> [--member M] [--extra T|@file] [--wait N]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			more, err := fileArg(extra, "")
			if err != nil {
				return err
			}
			mark := time.Now()
			res, err := c.FixChecks(cmd.Context(), args[0], api.FixChecksRequest{Member: member, Extra: more,
				Backend: backend, Model: model})
			if err != nil {
				return err
			}
			return waitTicketTask(cmd, d, res.TaskID, wait, mark, res, "Task "+res.TaskID+" started.")
		})
	fs := cmd.Flags()
	fs.StringVar(&member, "member", "", "department member (default: the last task's)")
	fs.StringVar(&extra, "extra", "", "extra instructions for the worker (or @file)")
	fs.StringVar(&backend, "backend", "", "agy, claude or codex")
	fs.StringVar(&model, "model", "", "model id or tier")
	fs.IntVar(&wait, "wait", 0, "seconds to wait for the report (default 0)")
	return cmd
}
