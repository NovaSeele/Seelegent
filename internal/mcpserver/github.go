package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// The issues tool (GitHub issues to draft pull requests) and the automations tool (milestone 15), as the old
// server's runIssues and runAutomations: the same slim views and argument names over the HTTP API v1. The token
// and the confirmation of an automation are dashboard-only; no tool reaches them.

// repoIDPattern is a repo id (r + 8 hex).
var repoIDPattern = regexp.MustCompile(`^r[0-9a-fA-F]{8}$`)

// issueKeyNumber is a bare issue number ("12" or "#12"), which means a GitHub issue.
var issueKeyNumber = regexp.MustCompile(`^#?\d+$`)

// issuesArgs are the arguments of issues.
type issuesArgs struct {
	Action        string            `json:"action"`
	Repo          string            `json:"repo"`
	Path          string            `json:"path"`
	Name          *string           `json:"name"`
	FirstCommit   optBool           `json:"first_commit"`
	Private       optBool           `json:"private"`
	Key           json.RawMessage   `json:"key"`
	State         string            `json:"state"`
	Title         string            `json:"title"`
	Body          string            `json:"body"`
	Decision      string            `json:"decision"`
	Reason        string            `json:"reason"`
	Member        string            `json:"member"`
	Backend       string            `json:"backend"`
	Model         string            `json:"model"`
	Prompt        string            `json:"prompt"`
	Gate          gateArg           `json:"gate"`
	Lane          laneArg           `json:"lane"`
	Question      string            `json:"question"`
	Attendees     []api.AttendeeRef `json:"attendees"`
	Options       strList           `json:"options"`
	Context       strList           `json:"context"`
	TicketID      string            `json:"ticket_id"`
	Status        strList           `json:"status"`
	Draft         optBool           `json:"draft"`
	CommitMessage string            `json:"commit_message"`
	Number        optNum            `json:"number"`
	SharedDirs    json.RawMessage   `json:"shared_dirs"`
	Extra         string            `json:"extra"`
	WaitSeconds   optNum            `json:"wait_seconds"`
	WakeOnBlocked optBool           `json:"wake_on_blocked"`
	Full          optBool           `json:"full"`
}

// slimIssue is the old inbox row: no body, the ticket only as id and status.
type slimIssue struct {
	Key        string               `json:"key"`
	Title      string               `json:"title"`
	Labels     []string             `json:"labels"`
	Suggestion *api.IssueSuggestion `json:"suggestion"`
	Decision   *api.IssueDecision   `json:"decision"`
	Ticket     *slimTicketRef       `json:"ticket"`
	State      string               `json:"state,omitempty"`
}

type slimTicketRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func slimIssueOf(i *api.Issue) slimIssue {
	out := slimIssue{Key: i.Key, Title: i.Title, Labels: api.NonNil(i.Labels), Suggestion: i.Suggestion,
		Decision: i.Decision}
	if i.Ticket != nil {
		out.Ticket = &slimTicketRef{ID: i.Ticket.ID, Status: i.Ticket.Status}
	}
	if i.State != "" && i.State != "open" {
		out.State = i.State
	}
	return out
}

// slimTicket is the old ticket summary: the checks only as their state and the failing check names.
type slimTicket struct {
	ID             string          `json:"id"`
	RepoID         string          `json:"repoId"`
	IssueKey       string          `json:"issueKey"`
	Issue          api.TicketIssue `json:"issue"`
	Branch         string          `json:"branch"`
	Worktree       string          `json:"worktree"`
	Status         string          `json:"status"`
	TaskIDs        []string        `json:"taskIds"`
	MeetingID      string          `json:"meetingId,omitempty"`
	PR             *api.TicketPR   `json:"pr,omitempty"`
	UpdatedAt      string          `json:"updatedAt"`
	Error          string          `json:"error,omitempty"`
	Checks         *slimChecks     `json:"checks,omitempty"`
	NeedsAttention string          `json:"needsAttention,omitempty"`
}

type slimChecks struct {
	State   string   `json:"state"`
	HeadSha *string  `json:"headSha"`
	Failing []string `json:"failing"`
}

// failingConclusion are the check conclusions that count as failed.
var failingConclusion = map[string]bool{"failure": true, "timed_out": true, "startup_failure": true, "error": true}

func slimTicketOf(t *api.TicketSummary) *slimTicket {
	if t == nil {
		return nil
	}
	out := &slimTicket{ID: t.ID, RepoID: t.RepoID, IssueKey: t.IssueKey, Issue: t.Issue, Branch: t.Branch,
		Worktree: t.Worktree, Status: t.Status, TaskIDs: api.NonNil(t.TaskIDs), MeetingID: t.MeetingID, PR: t.PR,
		UpdatedAt: t.UpdatedAt, Error: t.Error, NeedsAttention: t.NeedsAttention}
	if c := t.Checks; c != nil {
		sc := &slimChecks{State: c.State, HeadSha: strOrNil(c.HeadSha), Failing: []string{}}
		for ix := range c.Runs {
			r := &c.Runs[ix]
			if failingConclusion[r.Conclusion] {
				sc.Failing = append(sc.Failing, r.Name)
			}
		}
		out.Checks = sc
	}
	return out
}

// ticketSummary is a ticket's slim summary.
func ticketSummary(t *api.Ticket) string {
	s := fmt.Sprintf("Ticket %s (%s) is %s.", t.ID, t.IssueKey, t.Status)
	if t.PR != nil {
		switch {
		case t.PR.URL != "":
			s += " Pull request: " + t.PR.URL
		case t.PR.ManualURL != "":
			s += " Open the pull request by hand: " + t.PR.ManualURL + " (then call link_pr with its number)."
		}
	}
	if t.Error != "" {
		s += " Error: " + t.Error
	}
	return s
}

// ticketResult is the result of an action that returns a ticket.
func ticketResult(t *api.Ticket, full bool) result {
	if full {
		return result{value: t, summary: ticketSummary(t)}
	}
	return result{value: slimTicketOf(&t.TicketSummary), summary: ticketSummary(t)}
}

// issueKey is the key as given; a bare number means a GitHub issue.
func issueKey(raw json.RawMessage) string {
	if !given(raw) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		s = string(raw)
	}
	s = strings.TrimSpace(s)
	if issueKeyNumber.MatchString(s) {
		return "gh#" + strings.TrimPrefix(s, "#")
	}
	return s
}

// issueActions are the actions of issues.
var issueActions = []string{"repos", "connect", "init", "create_remote", "sync", "list", "add", "suggest", "triage",
	"tickets", "ticket", "add_task", "pr", "link_pr", "comment", "drop", "cleanup", "repo_settings", "fix_checks"}

func (s *Server) issues(ctx context.Context, k *call) (result, error) {
	var a issuesArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	known := false
	for _, x := range issueActions {
		known = known || x == a.Action
	}
	if !known {
		return result{}, fmt.Errorf("unknown issues action: %s (%s)", a.Action, strings.Join(issueActions, ", "))
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "repos", "connect", "init", "create_remote", "sync", "repo_settings":
		return s.issueRepos(ctx, c, &a)
	case "list", "add", "suggest", "triage":
		return s.issueInbox(ctx, k, c, &a)
	}
	return s.issueTickets(ctx, k, c, &a)
}

// repoID resolves the repo argument: an id, or the path of a connected repo.
func (s *Server) repoID(ctx context.Context, c *client.Client, a *issuesArgs) (string, error) {
	repo := strings.TrimSpace(a.Repo)
	if repo == "" {
		return "", fmt.Errorf("repo is required for %s (repo id or path)", a.Action)
	}
	if repoIDPattern.MatchString(repo) {
		return strings.ToLower(repo), nil
	}
	list, err := c.Repos(ctx)
	if err != nil {
		return "", err
	}
	p, err := s.absPath(repo)
	if err != nil {
		return "", err
	}
	want, err := fsutil.Key(p)
	if err != nil {
		return "", err
	}
	for ix := range list {
		r := &list[ix]
		if r.ID == repo {
			return r.ID, nil
		}
		if got, err := fsutil.Key(r.Path); err == nil && got == want {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("repo not connected: %s (call issues with action connect first)", repo)
}

// repoSettings is the repo_settings view.
type repoSettings struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	SharedDirs []string `json:"sharedDirs"`
}

func (s *Server) issueRepos(ctx context.Context, c *client.Client, a *issuesArgs) (result, error) {
	switch a.Action {
	case "repos":
		list, err := c.Repos(ctx)
		if err != nil {
			return result{}, err
		}
		sum := fmt.Sprintf("%d connected repo%s.", len(list), plural(len(list)))
		if acc, err := c.GitHubAccess(ctx, false); err == nil {
			sum += " GitHub access: " + acc.Tier + "."
		}
		return result{value: api.NonNil(list), summary: sum}, nil
	case "connect", "init":
		p := a.Path
		if p == "" {
			p = a.Repo
		}
		if strings.TrimSpace(p) == "" {
			return result{}, fmt.Errorf("path is required for %s", a.Action)
		}
		abs, err := s.absPath(p)
		if err != nil {
			return result{}, err
		}
		req := api.RepoConnectRequest{Path: abs}
		if a.Action == "init" {
			req.Init, req.FirstCommit = true, a.FirstCommit.ptr()
			if a.Name != nil {
				req.Name = *a.Name
			}
		}
		r, err := c.ConnectRepo(ctx, req)
		if err != nil {
			return result{}, err
		}
		return result{value: r, summary: repoSummary(&r)}, nil
	}
	id, err := s.repoID(ctx, c, a)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "create_remote":
		req := api.RemoteRequest{Private: a.Private.ptr()}
		if a.Name != nil {
			req.Name = *a.Name
		}
		r, err := c.CreateRemote(ctx, id, req)
		if err != nil {
			return result{}, err
		}
		return result{value: r, summary: repoSummary(&r)}, nil
	case "sync":
		res, err := c.SyncRepo(ctx, id)
		if err != nil {
			return result{}, err
		}
		sum := fmt.Sprintf("Synced: %d added, %d updated, %d closed.", res.Added, res.Updated, res.Closed)
		if res.Skipped != "" {
			sum = "Not synced: " + res.Skipped
		}
		return result{value: res, summary: sum}, nil
	}
	// repo_settings
	if !given(a.SharedDirs) {
		list, err := c.Repos(ctx)
		if err != nil {
			return result{}, err
		}
		for ix := range list {
			r := &list[ix]
			if r.ID == id {
				return result{value: repoSettings{ID: r.ID, Path: r.Path, SharedDirs: api.NonNil(r.SharedDirs)},
					summary: fmt.Sprintf("%d shared folder%s.", len(r.SharedDirs), plural(len(r.SharedDirs)))}, nil
			}
		}
		return result{}, fmt.Errorf("repo not connected: %s", a.Repo)
	}
	dirs, err := sharedDirs(a.SharedDirs)
	if err != nil {
		return result{}, err
	}
	r, err := c.UpdateRepo(ctx, id, api.RepoPatch{SharedDirs: dirs})
	if err != nil {
		return result{}, err
	}
	sum := fmt.Sprintf("%d shared folder%s.", len(r.SharedDirs), plural(len(r.SharedDirs)))
	if a.Full.or(false) {
		return result{value: r, summary: sum}, nil
	}
	return result{value: repoSettings{ID: r.ID, Path: r.Path, SharedDirs: api.NonNil(r.SharedDirs)}, summary: sum},
		nil
}

// sharedDirs reads shared_dirs: a list, or one comma-separated string.
func sharedDirs(raw json.RawMessage) ([]string, error) {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		out := []string{}
		for _, p := range strings.Split(one, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errors.New("shared_dirs must be an array of repo-relative folder paths")
	}
	return api.NonNil(list), nil
}

// repoSummary is a repo's one-line summary.
func repoSummary(r *api.Repo) string {
	s := fmt.Sprintf("Repo %s at %s", r.ID, r.Path)
	switch {
	case r.GitHub:
		s += fmt.Sprintf(" (GitHub %s/%s).", r.Owner, r.Repo)
	case r.NotGitHub != "":
		s += " (not GitHub: " + r.NotGitHub + ")."
	default:
		s += " (no GitHub remote yet)."
	}
	return s
}

func (s *Server) issueInbox(ctx context.Context, k *call, c *client.Client, a *issuesArgs) (result, error) {
	full := a.Full.or(false)
	id, err := s.repoID(ctx, c, a)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "list":
		list, err := c.Issues(ctx, id, a.State)
		if err != nil {
			return result{}, err
		}
		sum := fmt.Sprintf("%d issue%s.", len(list), plural(len(list)))
		if full {
			return result{value: api.NonNil(list), summary: sum}, nil
		}
		slim := make([]slimIssue, 0, len(list))
		for i := range list {
			slim = append(slim, slimIssueOf(&list[i]))
		}
		return result{value: slim, summary: sum}, nil
	case "add":
		if strings.TrimSpace(a.Title) == "" {
			return result{}, errors.New("title is required for add")
		}
		i, err := c.AddIssue(ctx, id, api.IssueInput{Title: a.Title, Body: a.Body})
		if err != nil {
			return result{}, err
		}
		return issueResult(&i, full, "Added issue "+i.Key+"."), nil
	}
	key := issueKey(a.Key)
	if key == "" {
		return result{}, fmt.Errorf("key is required for %s (gh#12 or local#3)", a.Action)
	}
	if a.Decision == "" {
		return result{}, fmt.Errorf("decision is required for %s (assign, meeting or skip)", a.Action)
	}
	if a.Action == "suggest" {
		i, err := c.SuggestIssue(ctx, id, key, api.SuggestRequest{Action: a.Decision, Member: a.Member,
			Reason: a.Reason})
		if err != nil {
			return result{}, err
		}
		return issueResult(&i, full, "Suggested "+a.Decision+" for "+i.Key+" (no side effects)."), nil
	}
	req := api.TriageRequest{Action: a.Decision, By: "lead", Reason: a.Reason, Prompt: a.Prompt,
		Question: a.Question, Attendees: a.Attendees, Options: a.Options, Context: a.Context,
		TicketTaskOptions: a.taskOptions()}
	res, err := c.TriageIssue(ctx, id, key, req)
	if err != nil {
		return result{}, err
	}
	sum := "Triaged " + res.Issue.Key + ": " + a.Decision + "."
	switch {
	case res.Ticket != nil && res.TaskID != "":
		sum = fmt.Sprintf("Ticket %s in %s; task %s started.", res.Ticket.ID, res.Ticket.Worktree, res.TaskID)
	case res.Ticket != nil && res.MeetingID != "":
		sum = fmt.Sprintf("Ticket %s; meeting %s opened in %s.", res.Ticket.ID, res.MeetingID, res.Ticket.Worktree)
	}
	if full {
		return result{value: res, summary: sum}, nil
	}
	out := map[string]any{"issue": slimIssueOf(&res.Issue), "ticket": slimTicketOf(res.Ticket),
		"taskId": strOrNil(res.TaskID), "meetingId": strOrNil(res.MeetingID)}
	return s.withTicketTask(ctx, k, a, res.TaskID, out, sum)
}

// issueResult is the result of an action that returns one issue.
func issueResult(i *api.Issue, full bool, sum string) result {
	if full {
		return result{value: i, summary: sum}
	}
	return result{value: slimIssueOf(i), summary: sum}
}

// taskOptions are the worker options of triage assign and add_task.
func (a *issuesArgs) taskOptions() api.TicketTaskOptions {
	o := api.TicketTaskOptions{Member: a.Member, Backend: a.Backend, Model: a.Model, Lane: a.Lane.paths,
		NoLane: a.Lane.off}
	o.Gate, o.GateCommands = a.Gate.request()
	return o
}

// withTicketTask waits for a task triage, add_task or fix_checks started when wait_seconds asks for it (default
// 0: return at once), and adds its report as "task".
func (s *Server) withTicketTask(ctx context.Context, k *call, a *issuesArgs, taskID string, out map[string]any,
	sum string) (result, error) {
	seconds := clampWait(a.WaitSeconds, 0, maxWait)
	if taskID == "" || seconds == 0 {
		return result{value: out, summary: sum}, nil
	}
	res, err := s.waitReports(ctx, k, []string{taskID}, seconds, false, a.WakeOnBlocked.or(true), "")
	if err != nil {
		return result{}, fmt.Errorf("task %s was started, but reading its report failed: %w", taskID, err)
	}
	rep := reportResult(res)
	out["task"] = rep.value
	return result{value: out, summary: sum + " " + rep.summary}, nil
}

func (s *Server) issueTickets(ctx context.Context, k *call, c *client.Client, a *issuesArgs) (result, error) {
	full := a.Full.or(false)
	if a.Action == "tickets" {
		repo := ""
		if strings.TrimSpace(a.Repo) != "" {
			id, err := s.repoID(ctx, c, a)
			if err != nil {
				return result{}, err
			}
			repo = id
		}
		list, err := c.Tickets(ctx, repo, a.Status)
		if err != nil {
			return result{}, err
		}
		sum := fmt.Sprintf("%d ticket%s.", len(list), plural(len(list)))
		if full {
			return result{value: api.NonNil(list), summary: sum}, nil
		}
		slim := make([]*slimTicket, 0, len(list))
		for i := range list {
			slim = append(slim, slimTicketOf(&list[i]))
		}
		return result{value: slim, summary: sum}, nil
	}
	id := strings.TrimSpace(a.TicketID)
	if id == "" {
		return result{}, fmt.Errorf("ticket_id is required for %s", a.Action)
	}
	var t api.Ticket
	var err error
	switch a.Action {
	case "ticket":
		if t, err = c.Ticket(ctx, id); err != nil {
			return result{}, err
		}
		return result{value: t, summary: ticketSummary(&t)}, nil
	case "add_task", "fix_checks":
		var res api.TicketTaskResult
		if a.Action == "add_task" {
			if strings.TrimSpace(a.Prompt) == "" {
				return result{}, errors.New("prompt is required for add_task")
			}
			res, err = c.TicketTask(ctx, id, api.TicketTaskRequest{Prompt: a.Prompt, TicketTaskOptions: a.taskOptions()})
		} else {
			res, err = c.FixChecks(ctx, id, api.FixChecksRequest{Member: a.Member, Extra: a.Extra, Backend: a.Backend,
				Model: a.Model})
		}
		if err != nil {
			return result{}, err
		}
		out := map[string]any{"ticket_id": id, "taskId": res.TaskID}
		return s.withTicketTask(ctx, k, a, res.TaskID, out, "Task "+res.TaskID+" started in ticket "+id+".")
	case "pr":
		t, err = c.TicketPR(ctx, id, api.PRRequest{Title: a.Title, Body: a.Body, Draft: a.Draft.ptr(),
			CommitMessage: a.CommitMessage})
	case "link_pr":
		if !a.Number.set {
			return result{}, errors.New("number is required for link_pr")
		}
		n := a.Number.or(0)
		if n != math.Trunc(n) || n < 1 || n > math.MaxInt32 {
			return result{}, errors.New("number must be a pull request number (1 or more)")
		}
		t, err = c.LinkPR(ctx, id, api.LinkPRRequest{Number: int(n)})
	case "comment":
		if strings.TrimSpace(a.Body) == "" {
			return result{}, errors.New("body is required for comment")
		}
		t, err = c.TicketComment(ctx, id, api.CommentRequest{Body: a.Body})
	case "drop":
		t, err = c.DropTicket(ctx, id, api.DropRequest{Reason: a.Reason})
	case "cleanup":
		t, err = c.CleanupTicket(ctx, id)
	}
	if err != nil {
		return result{}, err
	}
	return ticketResult(&t, full), nil
}

// automationsArgs are the arguments of automations.
type automationsArgs struct {
	Action         string  `json:"action"`
	ID             string  `json:"id"`
	Name           *string `json:"name"`
	Cwd            *string `json:"cwd"`
	Prompt         *string `json:"prompt"`
	Every          *string `json:"every"`
	At             *string `json:"at"`
	Weekday        optNum  `json:"weekday"`
	Cron           *string `json:"cron"`
	Timezone       *string `json:"timezone"`
	Member         *string `json:"member"`
	Backend        *string `json:"backend"`
	Model          *string `json:"model"`
	Gate           gateArg `json:"gate"`
	Lane           laneArg `json:"lane"`
	Precheck       *string `json:"precheck"`
	MinRemaining   optNum  `json:"min_remaining"`
	UseBeforeReset optBool `json:"use_before_reset"`
	GraceMinutes   optNum  `json:"grace_minutes"`
	Limit          optNum  `json:"limit"`
}

// slimAutomation is the old automation view.
type slimAutomation struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Enabled         bool                `json:"enabled"`
	EnableRequested bool                `json:"enableRequested,omitempty"`
	Running         bool                `json:"running"`
	Schedule        api.Schedule        `json:"schedule"`
	Timezone        string              `json:"timezone"`
	Cwd             string              `json:"cwd"`
	Member          string              `json:"member,omitempty"`
	Backend         string              `json:"backend,omitempty"`
	Model           string              `json:"model,omitempty"`
	Precheck        string              `json:"precheck,omitempty"`
	Quota           api.AutomationQuota `json:"quota"`
	NextRun         string              `json:"nextRun,omitempty"`
	LastRun         *api.AutomationRun  `json:"lastRun,omitempty"`
}

func slimAutomationOf(x *api.Automation) slimAutomation {
	return slimAutomation{ID: x.ID, Name: x.Name, Enabled: x.Enabled, EnableRequested: x.EnableRequested,
		Running: x.Running, Schedule: x.Schedule, Timezone: x.Timezone, Cwd: x.Cwd, Member: x.Member,
		Backend: x.Backend, Model: x.Model, Precheck: x.Precheck, Quota: x.Quota, NextRun: x.NextRun,
		LastRun: x.LastRun}
}

// schedule is the schedule the arguments give, or nil when they give none.
func (a *automationsArgs) schedule() (*api.Schedule, error) {
	if a.Every == nil && a.At == nil && !a.Weekday.set && a.Cron == nil {
		return nil, nil
	}
	sc := &api.Schedule{}
	if a.Every != nil {
		sc.Every = *a.Every
	}
	if a.At != nil {
		sc.At = *a.At
	}
	if a.Cron != nil {
		sc.Cron = *a.Cron
	}
	if a.Weekday.set {
		w := a.Weekday.or(0)
		if w != math.Trunc(w) || w < 0 || w > 6 {
			return nil, errors.New("weekday is 0-6 (0 = Sunday)")
		}
		n := int(w)
		sc.Weekday = &n
	}
	return sc, nil
}

// quota is the quota rules the arguments give over base, or nil when they give none.
func (a *automationsArgs) quota(base api.AutomationQuota) *api.AutomationQuota {
	if !a.MinRemaining.set && !a.UseBeforeReset.set {
		return nil
	}
	q := base
	if a.MinRemaining.set {
		q.MinRemaining = a.MinRemaining.or(0)
	}
	if a.UseBeforeReset.set {
		q.UseBeforeReset = a.UseBeforeReset.or(false)
	}
	return &q
}

// grace is grace_minutes, or nil.
func (a *automationsArgs) grace() (*int, error) {
	if !a.GraceMinutes.set {
		return nil, nil
	}
	g := a.GraceMinutes.or(0)
	if g != math.Trunc(g) || g < 0 || g > math.MaxInt32 {
		return nil, errors.New("grace_minutes is a whole number of minutes")
	}
	n := int(g)
	return &n, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// enableHint is said after create and update: enabling is the user's call, confirmed in the dashboard.
const enableHint = " It is disabled: enable it only when the user asked for it (action enable; the user then " +
	"confirms in the dashboard)."

func (s *Server) automations(ctx context.Context, k *call) (result, error) {
	var a automationsArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	switch a.Action {
	case "list", "create":
	case "update", "enable", "disable", "delete", "run", "runs":
		if strings.TrimSpace(a.ID) == "" {
			return result{}, fmt.Errorf("id is required for %s", a.Action)
		}
	default:
		return result{}, fmt.Errorf("unknown automations action: %s (list, create, update, enable, disable, "+
			"delete, run or runs)", a.Action)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "list":
		list, err := c.Automations(ctx)
		if err != nil {
			return result{}, err
		}
		slim := make([]slimAutomation, 0, len(list))
		for i := range list {
			slim = append(slim, slimAutomationOf(&list[i]))
		}
		return result{value: slim, summary: fmt.Sprintf("%d automation%s.", len(list), plural(len(list)))}, nil
	case "create", "update":
		return s.saveAutomation(ctx, c, &a)
	case "enable":
		res, err := c.EnableAutomation(ctx, a.ID)
		if err != nil {
			return result{}, err
		}
		v := map[string]any{"automation": slimAutomationOf(&res.Automation), "confirm": res.Confirm,
			"confirmUrl": res.ConfirmURL, "message": res.Message}
		return result{value: v, summary: res.Message}, nil
	case "disable":
		off := false
		x, err := c.UpdateAutomation(ctx, a.ID, api.AutomationPatch{Enabled: &off})
		if err != nil {
			return result{}, err
		}
		return result{value: slimAutomationOf(&x), summary: "Automation " + x.ID + " is disabled."}, nil
	case "delete":
		if err := c.DeleteAutomation(ctx, a.ID); err != nil {
			return result{}, err
		}
		return result{value: map[string]any{"id": a.ID, "deleted": true}, summary: "Deleted automation " + a.ID + "."},
			nil
	case "run":
		run, err := c.RunAutomation(ctx, a.ID)
		if err != nil {
			return result{}, err
		}
		sum := "Run " + run.Outcome + "."
		if run.TaskID != "" {
			sum = "Run started task " + run.TaskID + "."
		} else if run.Detail != "" {
			sum = "Run " + run.Outcome + ": " + run.Detail
		}
		return result{value: run, summary: sum}, nil
	}
	n := int(math.Round(math.Min(math.Max(a.Limit.or(20), 1), 200)))
	runs, err := c.AutomationRuns(ctx, a.ID, n)
	if err != nil {
		return result{}, err
	}
	return result{value: api.NonNil(runs), summary: fmt.Sprintf("%d run%s.", len(runs), plural(len(runs)))}, nil
}

// saveAutomation is create and update. Neither enables: an automation is created disabled, and enabling is the
// separate enable action the user confirms in the dashboard.
func (s *Server) saveAutomation(ctx context.Context, c *client.Client, a *automationsArgs) (result, error) {
	sc, err := a.schedule()
	if err != nil {
		return result{}, err
	}
	grace, err := a.grace()
	if err != nil {
		return result{}, err
	}
	var cwd *string
	if a.Cwd != nil {
		p, err := s.absPath(*a.Cwd)
		if err != nil {
			return result{}, err
		}
		cwd = &p
	}
	if a.Action == "create" {
		in := api.AutomationInput{Name: deref(a.Name), Prompt: deref(a.Prompt), Cwd: deref(cwd),
			Timezone: deref(a.Timezone), Member: deref(a.Member), Backend: deref(a.Backend), Model: deref(a.Model),
			Lane: a.Lane.paths, NoLane: a.Lane.off, Precheck: deref(a.Precheck), GraceMinutes: grace,
			Quota: a.quota(api.AutomationQuota{MinRemaining: 20})}
		in.Gate, in.GateCommands = a.Gate.request()
		if sc != nil {
			in.Schedule = *sc
		}
		x, err := c.CreateAutomation(ctx, in)
		if err != nil {
			return result{}, err
		}
		return result{value: slimAutomationOf(&x), summary: "Created automation " + x.ID + "." + enableHint}, nil
	}
	cur, err := c.Automation(ctx, a.ID)
	if err != nil {
		return result{}, err
	}
	p := api.AutomationPatch{Name: a.Name, Prompt: a.Prompt, Cwd: cwd, Schedule: sc, Timezone: a.Timezone,
		Member: a.Member, Backend: a.Backend, Model: a.Model, Precheck: a.Precheck, GraceMinutes: grace,
		Quota: a.quota(cur.Quota)}
	switch {
	case a.Gate.off || a.Gate.on:
		g := a.Gate.on
		none := []string{}
		p.Gate, p.GateCommands = &g, &none
	case a.Gate.set:
		cmds := a.Gate.cmds
		p.GateCommands = &cmds
	}
	if a.Lane.set {
		paths, off := api.NonNil(a.Lane.paths), a.Lane.off
		p.Lane, p.NoLane = &paths, &off
	}
	x, err := c.UpdateAutomation(ctx, a.ID, p)
	if err != nil {
		return result{}, err
	}
	sum := "Updated automation " + x.ID + "."
	if !x.Enabled {
		sum += enableHint
	}
	return result{value: slimAutomationOf(&x), summary: sum}, nil
}
