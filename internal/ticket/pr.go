package ticket

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// pushTimeout is the time limit of a push.
const pushTimeout = 5 * time.Minute

// head is the commit HEAD of the worktree.
func (s *Service) head(ctx context.Context, wt string) (string, error) {
	return s.git(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "rev-parse", "HEAD")
}

// dirty reports whether the worktree has anything uncommitted that git does not ignore.
func (s *Service) dirty(ctx context.Context, wt string) (bool, error) {
	out, err := s.git(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "status", "--porcelain")
	return out != "", err
}

// remoteSha is the commit origin's push URL holds for the ticket branch ("" when none or unreachable).
func (s *Service) remoteSha(ctx context.Context, t *ticketRec) string {
	target := "origin"
	if u, ok := s.gitOK(ctx, t.Worktree, "remote", "get-url", "--push", "origin"); ok && u != "" &&
		!strings.HasPrefix(u, "-") && !strings.Contains(u, "::") {
		target = u
	}
	out, err := s.git(ctx, t.Worktree, gitx.ExecOptions{Timeout: time.Minute}, "ls-remote", "--heads", target,
		"refs/heads/"+t.Branch)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == "refs/heads/"+t.Branch {
			return f[0]
		}
	}
	return ""
}

// client returns the GitHub client of a ticket's repository, or nil when it is not on GitHub.
func (s *Service) client(ctx context.Context, r *repoRec, force bool) (*github.Client, github.Access, error) {
	access := s.o.GitHub.Access(ctx, force)
	g := s.gh(r)
	if !g.ok() {
		return nil, access, nil
	}
	c, err := s.o.GitHub.Client(ctx, access, g.host, g.owner, g.name)
	return c, access, err
}

// Recheck checks the outbox entries an earlier daemon left unknown against git and GitHub, and marks done those
// that had happened.
func (s *Service) Recheck(ctx context.Context, id string) error {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return err
	}
	defer unlock()
	return s.recheck(ctx, id)
}

func (s *Service) recheck(ctx context.Context, id string) error {
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return err
	}
	var unknown []api.OutboxEntry
	if err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		list, err := listEntries(ctx, tx, scopeTicket, id)
		for _, e := range list {
			if e.Status == api.OutboxUnknown {
				unknown = append(unknown, e)
			}
		}
		return err
	}); err != nil || len(unknown) == 0 {
		return err
	}
	r, err := s.getRepo(ctx, t.RepoID)
	if err != nil {
		return err
	}
	wtOK := t.worktreeOK()
	for _, e := range unknown {
		var result map[string]any
		var pr *api.TicketPR
		switch {
		case e.Kind == api.OutboxCommit && wtOK:
			if d, err := s.dirty(ctx, t.Worktree); err == nil && !d {
				h, _ := s.head(ctx, t.Worktree)
				result = map[string]any{"sha": h}
			}
		case e.Kind == api.OutboxPush && wtOK:
			h, err := s.head(ctx, t.Worktree)
			if rs := s.remoteSha(ctx, t); err == nil && rs != "" && rs == h {
				result = map[string]any{"headSha": h}
			}
		case e.Kind == api.OutboxOpenPR:
			c, _, err := s.client(ctx, r, false)
			if err == nil && c != nil {
				if found, err := c.FindPR(ctx, t.Branch); err == nil && found != nil {
					pr = found
					result = map[string]any{"adopted": true, "number": found.Number, "url": found.URL}
				}
			}
		}
		if result == nil {
			continue
		}
		if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
			if err := markRecovered(ctx, tx, scopeTicket, id, e, result, api.FormatTime(s.o.Now())); err != nil {
				return err
			}
			if pr != nil {
				if t.PR != nil {
					pr.ManualURL = t.PR.ManualURL
				}
				t.PR = pr
				if derived(t.Status) {
					t.Status = api.TicketPROpen
				}
			}
			s.logStep(ctx, tx, id, "outbox_recovered", map[string]any{"id": e.ID, "kind": e.Kind})
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// prInputs gathers what the generated pull request body is made of.
func (s *Service) prInputs(ctx context.Context, t *ticketRec, issue api.Issue, repoPath string) prInputs {
	in := prInputs{ticket: t, issue: issue, repo: repoPath, home: s.o.UserHome, files: s.files(ctx, t)}
	var details []api.TaskDetail
	for _, tid := range t.TaskIDs {
		if d, err := s.o.Tasks.Get(ctx, tid); err == nil {
			details = append(details, d)
		}
	}
	for i := len(details) - 1; i >= 0 && in.task == nil; i-- {
		if d := details[i]; d.Review != nil && d.Review.Verdict == api.VerdictAccepted {
			in.task = &details[i]
		}
	}
	if in.task == nil && len(details) > 0 {
		in.task = &details[len(details)-1]
	}
	if in.task != nil {
		for i := len(in.task.Turns) - 1; i >= 0; i-- {
			if g := in.task.Turns[i].Gate; g != nil {
				in.gate = g
				break
			}
		}
	}
	if t.MeetingID != "" && s.o.Meetings != nil {
		if m, err := s.o.Meetings.Get(ctx, t.MeetingID); err == nil {
			in.meeting = &m
		}
	}
	return in
}

// PR commits the ticket's worktree (when it has changes), pushes the ticket branch (never with force) and opens a
// pull request, a draft by default; an existing pull request of the branch is adopted. The manual tier gets a
// compare link to open by hand. Every step is written ahead in the outbox. Before anything is committed or pushed
// the files are scanned (old bug D5): secrets, keys, junk and large files refuse the whole request.
func (s *Service) PR(ctx context.Context, id string, req api.PRRequest) (api.Ticket, error) {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlock()
	s.derive(ctx, id, nil)
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.Ticket{}, err
	}
	if t.Status != api.TicketReady && t.Status != api.TicketPROpen {
		return api.Ticket{}, badStatef("ticket %s is %s; pr needs ready or pr_open", id, t.Status)
	}
	r, err := s.getRepo(ctx, t.RepoID)
	if err != nil {
		return api.Ticket{}, err
	}
	if !r.HasOrigin {
		return api.Ticket{}, invalidf("the repository has no origin remote; add one or create the GitHub repository first")
	}
	if !t.worktreeOK() {
		return api.Ticket{}, invalidf("the ticket's worktree is missing")
	}
	if !strings.HasPrefix(t.Branch, gitx.BranchPrefix) || t.Branch == r.DefaultBranch || !gitx.ValidBranch(t.Branch) {
		return api.Ticket{}, invalidf("refusing to push branch %s", t.Branch)
	}
	draft := req.Draft == nil || *req.Draft
	if err := s.recheck(ctx, id); err != nil {
		return api.Ticket{}, err
	}
	issue := s.issueOf(ctx, t)
	if _, err := s.updateTicket(ctx, id, func(_ *sql.Tx, t *ticketRec) error {
		if t.Error == "" {
			return errUnchanged
		}
		t.Error = ""
		return nil
	}); err != nil {
		return api.Ticket{}, err
	}

	// 0. The scan: everything that would leave the machine.
	stage, committed, err := s.scanPaths(ctx, t.Worktree, t.BaseSha)
	if err != nil {
		return api.Ticket{}, err
	}
	if findings := s.scan(ctx, t.Worktree, append(append([]string{}, stage...), committed...)); len(findings) > 0 {
		serr := &ScanError{Findings: findings}
		_, _ = s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
			t.Error = clip(serr.Error(), 600)
			s.logStep(ctx, tx, id, "scan_refused", map[string]any{"findings": findings})
			return nil
		})
		return api.Ticket{}, serr
	}

	unlockRepo, err := s.o.Git.Lock(ctx, t.Worktree)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlockRepo()

	// 1. Commit, only when something is uncommitted: exactly the listed files.
	if len(stage) > 0 {
		message := strings.TrimSpace(req.CommitMessage)
		if message == "" {
			message = defaultTitle(t)
		}
		message = clip(message, 5000)
		if _, err := s.runStep(ctx, id, api.OutboxCommit, map[string]any{"message": clip(message, 200),
			"files": len(stage)}, func() (map[string]any, error) {
			owned := gitx.ExecOptions{Owned: true}
			for i := 0; i < len(stage); i += 200 {
				chunk := stage[i:min(i+200, len(stage))]
				if _, err := s.git(ctx, t.Worktree, owned, append([]string{"add", "--"}, chunk...)...); err != nil {
					return nil, err
				}
			}
			if _, err := s.git(ctx, t.Worktree, owned, "commit", "-q", "-m", message); err != nil {
				return nil, err
			}
			h, err := s.head(ctx, t.Worktree)
			return map[string]any{"sha": h}, err
		}); err != nil {
			return api.Ticket{}, err
		}
	}
	head, err := s.head(ctx, t.Worktree)
	if err != nil {
		return api.Ticket{}, err
	}
	if n, ok := s.gitOK(ctx, t.Worktree, "rev-list", "--count", t.BaseSha+"..HEAD"); ok && n == "0" {
		return api.Ticket{}, invalidf("the ticket branch has no changes beyond its base")
	}

	// 2. Push the ticket branch, never with force.
	if s.remoteSha(ctx, t) != head {
		if _, err := s.runStep(ctx, id, api.OutboxPush, map[string]any{"branch": t.Branch, "headSha": head},
			func() (map[string]any, error) {
				_, err := s.git(ctx, t.Worktree, gitx.ExecOptions{PushBranch: t.Branch, Timeout: pushTimeout},
					"push", "-u", "--porcelain", "origin", t.Branch)
				return map[string]any{"headSha": head}, err
			}); err != nil {
			return api.Ticket{}, err
		}
	}

	// 3. Open the pull request (adopt one that exists; the manual tier gets a compare link).
	t, err = s.getTicket(ctx, id)
	if err != nil {
		return api.Ticket{}, err
	}
	var newPR *api.TicketPR
	if t.PR == nil || t.PR.Number == 0 {
		title := oneLine(req.Title, 250)
		if title == "" {
			title = defaultTitle(t)
		}
		body := req.Body
		if strings.TrimSpace(body) == "" {
			body = prBody(s.prInputs(ctx, t, issue, r.Path))
		} else {
			body = clip(scrubLocal(body, []string{t.Worktree, r.Path}, s.o.UserHome), maxPRBody)
		}
		title, body = s.redact(title), s.redact(body)
		_, err := s.runStep(ctx, id, api.OutboxOpenPR, map[string]any{"branch": t.Branch, "base": t.Base,
			"draft": draft, "title": title}, func() (map[string]any, error) {
			c, access, err := s.client(ctx, r, false)
			if err != nil {
				return nil, err
			}
			if c == nil {
				newPR = &api.TicketPR{Draft: draft, HeadSha: head}
				return map[string]any{"skipped": "no_github"}, nil
			}
			existing, err := c.FindPR(ctx, t.Branch)
			if err != nil && access.CanWrite() {
				return nil, err
			}
			if existing != nil {
				newPR = existing
				return map[string]any{"adopted": true, "number": existing.Number, "url": existing.URL}, nil
			}
			if !access.CanWrite() {
				u := c.CompareURL(t.Base, t.Branch, title, body)
				newPR = &api.TicketPR{Draft: draft, HeadSha: head, ManualURL: u}
				return map[string]any{"manualUrl": u}, nil
			}
			created, err := c.CreatePR(ctx, github.PRInput{Title: title, Body: body, Head: t.Branch, Base: t.Base,
				Draft: draft})
			if err != nil {
				return nil, err
			}
			newPR = &created
			return map[string]any{"number": created.Number, "url": created.URL}, nil
		})
		if err != nil {
			return api.Ticket{}, err
		}
	}
	if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		if newPR != nil {
			t.PR = newPR
		} else if t.PR != nil {
			t.PR.HeadSha = head
		}
		if t.Checks != nil && t.Checks.HeadSha != head {
			// A new head runs CI again: earlier results do not apply until the next poll.
			t.Checks = &api.TicketChecks{State: api.ChecksPending, Runs: []api.CheckRun{}, HeadSha: head,
				At: api.FormatTime(s.o.Now())}
		}
		t.Status = api.TicketPROpen
		if t.PR != nil && (t.PR.State == "merged" || t.PR.State == "closed") {
			t.Status = api.TicketDone
		}
		s.logStep(ctx, tx, id, "status", map[string]any{"status": t.Status})
		return nil
	}); err != nil {
		return api.Ticket{}, err
	}
	s.o.IssuesChanged(t.RepoID)
	return s.Ticket(ctx, id)
}

// LinkPR records a pull request the user opened by hand.
func (s *Service) LinkPR(ctx context.Context, id string, req api.LinkPRRequest) (api.Ticket, error) {
	if req.Number <= 0 {
		return api.Ticket{}, invalidf("number must be a positive integer")
	}
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlock()
	s.derive(ctx, id, nil)
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.Ticket{}, err
	}
	if t.Status != api.TicketReady && t.Status != api.TicketPROpen {
		return api.Ticket{}, badStatef("ticket %s is %s; link_pr needs ready or pr_open", id, t.Status)
	}
	r, err := s.getRepo(ctx, t.RepoID)
	if err != nil {
		return api.Ticket{}, err
	}
	c, access, err := s.client(ctx, r, false)
	if err != nil {
		return api.Ticket{}, err
	}
	if c == nil {
		return api.Ticket{}, invalidf("the repository is not on GitHub")
	}
	st, err := c.PRState(ctx, req.Number)
	if err != nil && access.CanWrite() {
		return api.Ticket{}, err
	}
	if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		pr := api.TicketPR{Number: req.Number, URL: c.PRURL(req.Number), State: "open"}
		if t.PR != nil {
			pr.Draft, pr.HeadSha, pr.ManualURL = t.PR.Draft, t.PR.HeadSha, t.PR.ManualURL
		}
		if err == nil {
			pr.State, pr.Draft = st.State, st.Draft
			if st.URL != "" {
				pr.URL = st.URL
			}
			if st.HeadSha != "" {
				pr.HeadSha = st.HeadSha
			}
		}
		t.PR = &pr
		t.Status = api.TicketPROpen
		if pr.State == "merged" || pr.State == "closed" {
			t.Status = api.TicketDone
		}
		s.logStep(ctx, tx, id, "link_pr", map[string]any{"number": req.Number, "state": pr.State})
		return nil
	}); err != nil {
		return api.Ticket{}, err
	}
	return s.Ticket(ctx, id)
}

// Comment comments on the ticket's GitHub issue (gh or token tier).
func (s *Service) Comment(ctx context.Context, id string, req api.CommentRequest) (api.Ticket, error) {
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return api.Ticket{}, invalidf("body is required")
	}
	if len(body) > maxComment {
		return api.Ticket{}, invalidf("body is longer than %d characters", maxComment)
	}
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlock()
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.Ticket{}, err
	}
	if strings.HasPrefix(t.IssueKey, "local#") || t.Issue.Number <= 0 {
		return api.Ticket{}, invalidf("comment needs a GitHub issue")
	}
	r, err := s.getRepo(ctx, t.RepoID)
	if err != nil {
		return api.Ticket{}, err
	}
	c, access, err := s.client(ctx, r, false)
	if err != nil {
		return api.Ticket{}, err
	}
	if c == nil {
		return api.Ticket{}, invalidf("the repository is not on GitHub")
	}
	if !access.CanWrite() {
		return api.Ticket{}, github.ErrManual
	}
	body = s.redact(scrubLocal(body, []string{t.Worktree, r.Path}, s.o.UserHome))
	if _, err := s.runStep(ctx, id, api.OutboxComment, map[string]any{"issue": t.Issue.Number,
		"preview": clip(oneLine(body, 200), 120)}, func() (map[string]any, error) {
		u, err := c.Comment(ctx, t.Issue.Number, body)
		return map[string]any{"url": u}, err
	}); err != nil {
		return api.Ticket{}, err
	}
	return s.Ticket(ctx, id)
}

// taskPending reports whether one of the ticket's tasks has work going on.
func (s *Service) taskPending(ctx context.Context, t *ticketRec) []string {
	var busy []string
	for _, tid := range t.TaskIDs {
		if sum, err := s.o.Tasks.Summary(ctx, tid); err == nil && task.Active(sum.Status) {
			busy = append(busy, tid)
		}
	}
	return busy
}

// errText is err's text for a log, or "".
func errText(err error) string {
	if err == nil {
		return ""
	}
	var ge *github.Error
	if errors.As(err, &ge) {
		return ge.Message
	}
	return err.Error()
}
