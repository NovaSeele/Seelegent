package ticket

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/github"
)

// NormalizeKey reads an issue key: "gh#12", "local#3", "#12" or "12" (a GitHub issue).
func NormalizeKey(key string) (string, error) {
	k := strings.TrimSpace(key)
	switch {
	case strings.HasPrefix(k, "gh#"), strings.HasPrefix(k, "local#"):
		_, n, _ := strings.Cut(k, "#")
		if v, err := strconv.Atoi(n); err == nil && v > 0 {
			return k[:strings.Index(k, "#")+1] + strconv.Itoa(v), nil
		}
	default:
		if v, err := strconv.Atoi(strings.TrimPrefix(k, "#")); err == nil && v > 0 {
			return "gh#" + strconv.Itoa(v), nil
		}
	}
	return "", invalidf("issue key %q must be gh#<n>, local#<n> or a number", key)
}

// Sync refreshes the inbox from GitHub's open issues. A repository not on GitHub is skipped (no_github).
func (s *Service) Sync(ctx context.Context, id string) (api.SyncResult, error) {
	unlock, err := s.locks.lock(ctx, "sync:"+id)
	if err != nil {
		return api.SyncResult{}, err
	}
	defer unlock()
	r, err := s.getRepo(ctx, id)
	if err != nil {
		return api.SyncResult{}, err
	}
	g := s.gh(r)
	if !g.ok() {
		return api.SyncResult{Skipped: "no_github"}, nil
	}
	access := s.o.GitHub.Access(ctx, false)
	client, err := s.o.GitHub.Client(ctx, access, g.host, g.owner, g.name)
	var fetched []githubIssue
	if err == nil {
		var list []githubIssue
		list, err = listGitHubIssues(ctx, client)
		fetched = list
	}
	if err != nil {
		msg := s.redactErr(err, 300)
		_ = s.o.DB.Write(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
			cur, lerr := loadRepo(ctx, tx, id)
			if lerr != nil {
				return lerr
			}
			key, kerr := fsutil.Key(cur.Path)
			if kerr != nil {
				return kerr
			}
			cur.SyncError = msg
			return saveRepo(ctx, tx, cur, key)
		})
		s.o.IssuesChanged(id)
		return api.SyncResult{}, err
	}
	res := api.SyncResult{}
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		res = api.SyncResult{}
		cur, err := loadRepo(ctx, tx, id)
		if err != nil {
			return err
		}
		items, err := listIssueRecs(ctx, tx, id)
		if err != nil {
			return err
		}
		byKey := map[string]*api.Issue{}
		for i := range items {
			byKey[items[i].Key] = &items[i]
		}
		seen := map[string]bool{}
		now := api.FormatTime(s.o.Now())
		for ix := range fetched {
			f := &fetched[ix]
			key := "gh#" + strconv.Itoa(f.Number)
			seen[key] = true
			updated := f.UpdatedAt
			if updated == "" {
				updated = now
			}
			it, ok := byKey[key]
			if !ok {
				n := api.Issue{Key: key, Number: f.Number, Source: "github", Title: f.Title, Body: f.Body,
					Labels: api.NonNil(f.Labels), Author: f.Author, URL: f.URL, State: f.State, UpdatedAt: updated}
				if err := saveIssue(ctx, tx, id, n); err != nil {
					return err
				}
				res.Added++
				continue
			}
			changed := it.UpdatedAt != updated || it.State != f.State || it.Title != f.Title
			it.Title, it.Body, it.Labels, it.Author, it.URL, it.State, it.UpdatedAt = f.Title, f.Body,
				api.NonNil(f.Labels), f.Author, f.URL, f.State, updated
			if err := saveIssue(ctx, tx, id, *it); err != nil {
				return err
			}
			if changed {
				res.Updated++
			}
		}
		if len(fetched) < syncLimit {
			// Everything open came back: an issue not in the answer was closed (or moved). Closed issues without a
			// ticket leave the inbox.
			for ix := range items {
				it := &items[ix]
				if it.Source != "github" || seen[it.Key] {
					continue
				}
				tickets, err := listTicketRecs(ctx, tx, id, it.Key)
				if err != nil {
					return err
				}
				if it.State == "open" {
					res.Closed++
				}
				if len(tickets) == 0 {
					if _, err := tx.ExecContext(ctx, `DELETE FROM github_issues WHERE repo_id = ? AND key = ?`, id,
						it.Key); err != nil {
						return err
					}
					continue
				}
				if it.State == "open" {
					it.State = "closed"
					if err := saveIssue(ctx, tx, id, *it); err != nil {
						return err
					}
				}
			}
		}
		key, err := fsutil.Key(cur.Path)
		if err != nil {
			return err
		}
		cur.LastSyncAt, cur.SyncError = s.o.Now(), ""
		return saveRepo(ctx, tx, cur, key)
	})
	if err != nil {
		return api.SyncResult{}, err
	}
	s.o.IssuesChanged(id)
	return res, nil
}

// githubIssue is an issue as fetched.
type githubIssue struct {
	Number    int
	Title     string
	Body      string
	Labels    []string
	Author    string
	URL       string
	State     string
	UpdatedAt string
}

func listGitHubIssues(ctx context.Context, c *github.Client) ([]githubIssue, error) {
	list, err := c.ListIssues(ctx, "open", syncLimit)
	if err != nil {
		return nil, err
	}
	out := make([]githubIssue, 0, len(list))
	for ix := range list {
		d := &list[ix]
		if d.Number <= 0 {
			continue
		}
		out = append(out, githubIssue{Number: d.Number, Title: oneLine(d.Title, maxTitle), Body: clip(d.Body, maxBody),
			Labels: d.Labels, Author: d.Author, URL: d.URL, State: d.State, UpdatedAt: d.UpdatedAt})
	}
	return out, nil
}

// withTickets adds each issue's latest ticket.
func withTickets(ctx context.Context, tx *sql.Tx, repoID string, items []api.Issue) ([]api.Issue, error) {
	tickets, err := listTicketRecs(ctx, tx, repoID, "")
	if err != nil {
		return nil, err
	}
	latest := map[string]*ticketRec{}
	for _, t := range tickets {
		if cur := latest[t.IssueKey]; cur == nil || t.CreatedAt.After(cur.CreatedAt) {
			latest[t.IssueKey] = t
		}
	}
	for i := range items {
		if t := latest[items[i].Key]; t != nil {
			sum := t.summary()
			items[i].Ticket = &sum
		}
	}
	return items, nil
}

// Issues lists the inbox: state open (default), closed or all; newest change first.
func (s *Service) Issues(ctx context.Context, id, state string) ([]api.Issue, error) {
	if state == "" {
		state = "open"
	}
	if !slices.Contains([]string{"open", "closed", "all"}, state) {
		return nil, invalidf("state must be open, closed or all")
	}
	out := []api.Issue{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		if _, err := loadRepo(ctx, tx, id); err != nil {
			return err
		}
		items, err := listIssueRecs(ctx, tx, id)
		if err != nil {
			return err
		}
		out = out[:0]
		for ix := range items {
			it := &items[ix]
			if state == "all" || it.State == state {
				out = append(out, *it)
			}
		}
		out, err = withTickets(ctx, tx, id, out)
		return err
	})
	return out, err
}

// Issue returns one inbox entry with its latest ticket.
func (s *Service) Issue(ctx context.Context, id, key string) (api.Issue, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return api.Issue{}, err
	}
	var it api.Issue
	err = s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		if it, err = loadIssue(ctx, tx, id, k); err != nil {
			return err
		}
		list, err := withTickets(ctx, tx, id, []api.Issue{it})
		if err == nil {
			it = list[0]
		}
		return err
	})
	return it, err
}

// AddIssue adds a local issue to the inbox.
func (s *Service) AddIssue(ctx context.Context, id string, in api.IssueInput) (api.Issue, error) {
	title := oneLine(in.Title, maxTitle)
	if title == "" {
		return api.Issue{}, invalidf("title is required")
	}
	var it api.Issue
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadRepo(ctx, tx, id)
		if err != nil {
			return err
		}
		n := max(r.NextLocalID, 1)
		items, err := listIssueRecs(ctx, tx, id)
		if err != nil {
			return err
		}
		for ix := range items {
			x := &items[ix]
			if x.LocalID >= n {
				n = x.LocalID + 1
			}
		}
		r.NextLocalID = n + 1
		key, err := fsutil.Key(r.Path)
		if err != nil {
			return err
		}
		if err := saveRepo(ctx, tx, r, key); err != nil {
			return err
		}
		it = api.Issue{Key: "local#" + strconv.Itoa(n), LocalID: n, Source: "local", Title: title,
			Body: clip(in.Body, maxBody), Labels: []string{}, Author: "local", State: "open",
			UpdatedAt: api.FormatTime(s.o.Now())}
		return saveIssue(ctx, tx, id, it)
	})
	if err != nil {
		return api.Issue{}, err
	}
	s.o.IssuesChanged(id)
	return it, nil
}

func validAction(a string) bool {
	return a == api.TriageAssign || a == api.TriageMeeting || a == api.TriageSkip
}

// Suggest records the lead's suggestion for an issue (no side effects).
func (s *Service) Suggest(ctx context.Context, id, key string, req api.SuggestRequest) (api.Issue, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return api.Issue{}, err
	}
	if !validAction(req.Action) {
		return api.Issue{}, invalidf("action must be assign, meeting or skip")
	}
	why := oneLine(req.Reason, maxReason)
	if why == "" {
		return api.Issue{}, invalidf("reason is required")
	}
	var it api.Issue
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if it, err = loadIssue(ctx, tx, id, k); err != nil {
			return err
		}
		sg := &api.IssueSuggestion{Action: req.Action, Reason: why, At: api.FormatTime(s.o.Now())}
		if req.Action == api.TriageAssign {
			sg.Member = strings.TrimSpace(req.Member)
		}
		it.Suggestion = sg
		return saveIssue(ctx, tx, id, it)
	})
	if err != nil {
		return api.Issue{}, err
	}
	s.o.IssuesChanged(id)
	return it, nil
}

// Triage decides an issue: skip records the decision; assign makes a ticket and starts a task in its worktree;
// meeting makes a ticket and opens a meeting in its worktree.
func (s *Service) Triage(ctx context.Context, id, key string, req api.TriageRequest) (api.TriageResult, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return api.TriageResult{}, err
	}
	unlock, err := s.locks.lock(ctx, "issue:"+id+":"+k)
	if err != nil {
		return api.TriageResult{}, err
	}
	defer unlock()
	if !validAction(req.Action) {
		return api.TriageResult{}, invalidf("action must be assign, meeting or skip")
	}
	by := req.By
	if by == "" {
		by = "lead"
	}
	if by != "lead" && by != "user" {
		return api.TriageResult{}, invalidf("by must be lead or user")
	}
	why := oneLine(req.Reason, maxReason)
	if req.Action == api.TriageMeeting && s.o.Meetings == nil {
		return api.TriageResult{}, invalidf("meetings are not available")
	}
	var (
		repo  *repoRec
		issue api.Issue
	)
	err = s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		if repo, err = loadRepo(ctx, tx, id); err != nil {
			return err
		}
		if issue, err = loadIssue(ctx, tx, id, k); err != nil {
			return err
		}
		tickets, err := listTicketRecs(ctx, tx, id, k)
		if err != nil {
			return err
		}
		for _, t := range tickets {
			if !finished(t.Status) {
				return badStatef("issue %s already has ticket %s (%s)", k, t.ID, t.Status)
			}
		}
		return nil
	})
	if err != nil {
		return api.TriageResult{}, err
	}
	if issue.State != "open" {
		return api.TriageResult{}, badStatef("issue %s is %s", k, issue.State)
	}
	decide := func(ticketID string) error {
		return s.o.DB.Write(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
			it, err := loadIssue(ctx, tx, id, k)
			if err != nil {
				return err
			}
			it.Decision = &api.IssueDecision{Action: req.Action, By: by, Reason: why, At: api.FormatTime(s.o.Now()),
				TicketID: ticketID}
			issue = it
			return saveIssue(ctx, tx, id, it)
		})
	}
	if req.Action == api.TriageSkip {
		if why == "" {
			return api.TriageResult{}, invalidf("reason is required to skip")
		}
		if err := decide(""); err != nil {
			return api.TriageResult{}, err
		}
		s.o.IssuesChanged(id)
		return api.TriageResult{Issue: issue}, nil
	}
	t, err := s.createTicket(ctx, repo, issue)
	if err != nil {
		return api.TriageResult{}, err
	}
	if err := decide(t.ID); err != nil {
		return api.TriageResult{}, err
	}
	s.o.IssuesChanged(id)
	res := api.TriageResult{Issue: issue}
	if t.Status == api.TicketFailed {
		sum := t.summary()
		res.Ticket = &sum
		return res, nil
	}
	title := clip(issueLabel(issue)+" "+oneLine(issue.Title, 120), 80)
	if req.Action == api.TriageAssign {
		taskID, err := s.startTask(ctx, t, issue, req.Prompt, req.TicketTaskOptions, title)
		if err != nil {
			s.failTicket(ctx, t.ID, "task_failed", err)
			return api.TriageResult{}, err
		}
		res.TaskID = taskID
	} else {
		head := strings.TrimSpace(req.Question)
		if head == "" {
			head = oneLine(issue.Title, 300)
		}
		room := max(500, maxQuestion-len(head)-900)
		q := head + "\n\n" + issueBlock(issue, room) + "\n\nThe outcome will be implemented in the git worktree on " +
			"branch " + t.Branch + "."
		m, err := s.o.Meetings.Open(ctx, api.MeetingRequest{Cwd: t.Worktree, Title: title, Question: clip(q, maxQuestion),
			Attendees: req.Attendees, Options: req.Options, Context: req.Context})
		if err != nil {
			s.failTicket(ctx, t.ID, "meeting_failed", err)
			return api.TriageResult{}, err
		}
		if _, err := s.updateTicket(ctx, t.ID, func(tx *sql.Tx, cur *ticketRec) error {
			cur.MeetingID = m.ID
			s.logStep(ctx, tx, cur.ID, "meeting", map[string]any{"meetingId": m.ID})
			return nil
		}); err != nil {
			return api.TriageResult{}, err
		}
		res.MeetingID = m.ID
	}
	cur, err := s.getTicket(ctx, t.ID)
	if err != nil {
		return api.TriageResult{}, err
	}
	sum := cur.summary()
	res.Ticket = &sum
	return res, nil
}

// failureOf is the known cause of err (a member that is missing or turned off), or nil.
func failureOf(err error) *api.TicketFailure {
	var detailed interface{ APIDetails() map[string]any }
	if !errors.As(err, &detailed) {
		return nil
	}
	d := detailed.APIDetails()
	reason, _ := d["reason"].(string)
	member, _ := d["member"].(string)
	switch reason {
	case api.FailureMemberMissing, api.FailureMemberDisabled:
		return &api.TicketFailure{Reason: reason, Member: member}
	}
	return nil
}

// failTicket marks a ticket failed with a redacted error.
func (s *Service) failTicket(ctx context.Context, id, step string, cause error) {
	msg := s.redactErr(cause, 600)
	_, err := s.updateTicket(context.WithoutCancel(ctx), id, func(tx *sql.Tx, t *ticketRec) error {
		t.Status, t.Error, t.Failure = api.TicketFailed, msg, failureOf(cause)
		s.logStep(ctx, tx, id, step, map[string]any{"error": msg})
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.log.Warn("ticket: mark failed", "ticket", id, "err", err)
	}
}
