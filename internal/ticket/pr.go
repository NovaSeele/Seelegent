package ticket

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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

// refused records why a pull request was refused before anything was committed or pushed, and returns err. A
// refusal (unsafe files, a scan that could not run, the remote's state) is kept structured for the dashboard; any
// other failure is the ticket's error.
func (s *Service) refused(ctx context.Context, id string, err error) error {
	msg := s.redactErr(err, 600)
	_, _ = s.updateTicket(context.WithoutCancel(ctx), id, func(tx *sql.Tx, t *ticketRec) error {
		s.setRefusal(ctx, tx, t, err, msg)
		return nil
	})
	return err
}

// adopt records the remote branch planPush took over as the commit pushed before. A branch the previous version
// pushed held the worker's history, public already: it is scanned once, and what looks like a secret or junk is
// kept on the ticket as a warning (the keys should be rotated; removing them now would not unpublish them).
func (s *Service) adopt(ctx context.Context, t *ticketRec, plan *pushPlan) error {
	var exposed []api.ScanFinding
	if plan.adopted == "legacy" {
		rev := plan.prev
		if t.BaseSha != "" && s.isAncestor(ctx, t.Worktree, t.BaseSha, plan.prev) {
			rev = t.BaseSha + ".." + plan.prev
		}
		objs, err := s.historyObjects(ctx, t.Worktree, rev)
		if err == nil {
			exposed, err = s.scanObjects(ctx, t.Worktree, objs)
		}
		if err != nil {
			s.log.Warn("ticket: scan of the adopted branch", "ticket", t.ID, "err", err)
		}
		sort.Slice(exposed, func(i, j int) bool { return exposed[i].Path < exposed[j].Path })
	}
	wctx := context.WithoutCancel(ctx)
	_, err := s.updateTicket(wctx, t.ID, func(tx *sql.Tx, r *ticketRec) error {
		r.PushedSha, r.PushedBase = plan.prev, plan.prevBase
		data := map[string]any{"sha": plan.prev, "from": plan.adopted}
		if len(exposed) > 0 {
			r.Exposed = exposed
			data["exposed"] = len(exposed)
		}
		s.logStep(wctx, tx, r.ID, "push_adopted", data)
		return nil
	})
	if err == nil {
		t.PushedSha, t.PushedBase = plan.prev, plan.prevBase
	}
	return err
}

// setRefusal records err on t: as its refusal when it is one, else as its error.
func (s *Service) setRefusal(ctx context.Context, tx *sql.Tx, t *ticketRec, err error, msg string) {
	rf := refusalOf(err, msg, api.FormatTime(s.o.Now()))
	if rf == nil {
		t.Error = msg
		s.logStep(ctx, tx, t.ID, "pr_refused", map[string]any{"error": msg})
		return
	}
	t.Refusal, t.Error = rf, ""
	data := map[string]any{"reason": rf.Reason}
	if len(rf.Findings) > 0 {
		data["findings"] = len(rf.Findings)
	}
	s.logStep(ctx, tx, t.ID, "pr_refused", data)
}

// client returns the GitHub client of a ticket's repository, or nil when it is not on GitHub.
func (s *Service) client(ctx context.Context, r *repoRec) (*github.Client, github.Access, error) {
	access := s.o.GitHub.Access(ctx, false)
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
		var pushed *[2]string
		switch {
		case e.Kind == api.OutboxCommit && wtOK:
			if d, err := s.dirty(ctx, t.Worktree); err == nil && !d {
				h, _ := s.head(ctx, t.Worktree)
				result = map[string]any{"sha": h}
			}
		case e.Kind == api.OutboxPush && wtOK:
			// The push sends a commit made before it (args.sha): it happened when the remote branch holds it.
			if sha, _ := e.Args["sha"].(string); sha != "" && s.remoteSha(ctx, t) == sha {
				base, _ := e.Args["base"].(string)
				result = map[string]any{"sha": sha, "base": base}
				pushed = &[2]string{sha, base}
			}
		case e.Kind == api.OutboxOpenPR:
			c, _, err := s.client(ctx, r)
			if err == nil && c != nil {
				if found, err := c.FindPR(ctx, t.Branch); err == nil && found != nil {
					pr = found
					result = map[string]any{"adopted": true, "number": found.Number, "url": found.URL}
				}
			}
		case e.Kind == api.OutboxComment && t.Issue.Number > 0:
			// A comment carries its entry's marker (G9): found on the issue, it was posted.
			c, _, err := s.client(ctx, r)
			if err == nil && c != nil {
				if u, err := c.FindComment(ctx, t.Issue.Number, commentMarker(e.ID), commentSince(e.At)); err == nil &&
					u != "" {
					result = map[string]any{"url": u}
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
			if pushed != nil {
				t.PushedSha, t.PushedBase = pushed[0], pushed[1]
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
// compare link to open by hand. Every step is written ahead in the outbox. The push sends one commit Seelegent
// makes of the worktree's tree, never the worker's history (push.go). Before anything is committed or pushed the
// blobs that commit adds are scanned (old bug D5): secrets, keys, junk and large files refuse the whole request.
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
		if t.Error == "" && t.Refusal == nil {
			return errUnchanged
		}
		t.Error, t.Refusal = "", nil
		return nil
	}); err != nil {
		return api.Ticket{}, err
	}

	unlockRepo, err := s.o.Git.Lock(ctx, t.Worktree)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlockRepo()

	// 0. What the push sends: one commit of Seelegent's own on top of the commit pushed before (or the base),
	// never the worker's history; the remote branch and the base are checked first.
	plan, err := s.planPush(ctx, t)
	var pe *PushError
	if errors.As(err, &pe) && pe.Reason == "branch_diverged" {
		// The remote changed under the ticket: recorded as a failed push, never built on or forced. A compare link
		// shows the lead the commits Seelegent did not push before anything is undone.
		if c, _, cerr := s.client(ctx, r); cerr == nil && c != nil {
			from, _ := pe.Details["pushed"].(string)
			if from == "" {
				from = t.Base
			}
			if remote, _ := pe.Details["remote"].(string); remote != "" {
				pe.Details["compareUrl"] = c.CommitsURL(from, remote)
			}
		}
		perr := err
		_ = s.runStep(ctx, id, api.OutboxPush, map[string]any{"branch": t.Branch, "remote": pe.Details["remote"]},
			func() (map[string]any, error) { return nil, perr })
		return api.Ticket{}, perr
	}
	if err != nil {
		return api.Ticket{}, s.refused(ctx, id, err)
	}
	if plan.adopted != "" {
		if err := s.adopt(ctx, t, plan); err != nil {
			return api.Ticket{}, err
		}
	}
	stage, err := s.uncommitted(ctx, t.Worktree)
	if err == nil {
		err = s.snapshot(ctx, t, plan)
	}
	if err != nil {
		return api.Ticket{}, s.refused(ctx, id, err)
	}
	baseTree, err := s.treeOf(ctx, t.Worktree, plan.parents[0])
	if err != nil {
		return api.Ticket{}, s.refused(ctx, id, s.scanErr(err))
	}
	if plan.tree == baseTree && len(plan.parents) == 1 {
		if plan.prev == "" {
			return api.Ticket{}, invalidf("the ticket branch has no changes beyond its base")
		}
		plan = nil // nothing new since the last push
	}

	// The scan: every blob the pushed commit adds.
	if plan != nil {
		objs, err := s.added(ctx, t.Worktree, plan)
		var findings []api.ScanFinding
		if err == nil {
			findings, err = s.scanObjects(ctx, t.Worktree, objs)
		}
		if err != nil {
			return api.Ticket{}, s.refused(ctx, id, err)
		}
		if len(findings) > 0 {
			sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
			serr := &ScanError{Findings: findings}
			_, _ = s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
				t.Refusal = refusalOf(serr, clip(serr.Error(), 600), api.FormatTime(s.o.Now()))
				t.Error = ""
				s.logStep(ctx, tx, id, "scan_refused", map[string]any{"findings": findings})
				return nil
			})
			return api.Ticket{}, serr
		}
	}

	// 1. Commit, only when something is uncommitted: exactly the listed files.
	if len(stage) > 0 {
		message := strings.TrimSpace(req.CommitMessage)
		if message == "" {
			message = defaultTitle(t)
		}
		message = clip(message, 5000)
		if err := s.runStep(ctx, id, api.OutboxCommit, map[string]any{"message": clip(message, 200),
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

	// 2. Push Seelegent's commit of exactly the scanned tree, never with force.
	pushed := t.PushedSha
	if pushed == "" && plan == nil {
		pushed = s.remoteSha(ctx, t)
	}
	if plan != nil {
		if headTree, err := s.treeOf(ctx, t.Worktree, "HEAD"); err != nil || headTree != plan.tree {
			return api.Ticket{}, s.refused(ctx, id, &PushError{Reason: api.RefusalScanFailed,
				Details: map[string]any{"cause": "changed"},
				Message: "the worktree changed while Seelegent scanned it; nothing was pushed, call pr again"})
		}
		title := oneLine(req.Title, 250)
		if title == "" {
			title = defaultTitle(t)
		}
		sha, err := s.commitTree(ctx, t.Worktree, plan, s.redact(title))
		if err != nil {
			return api.Ticket{}, s.refused(ctx, id, err)
		}
		if err := s.runStep(ctx, id, api.OutboxPush, map[string]any{"branch": t.Branch, "sha": sha,
			"parents": plan.parents, "base": plan.base, "headSha": head}, func() (map[string]any, error) {
			// The push runs to its end even when the caller goes away: a push cut off half way leaves GitHub and the
			// ticket disagreeing. Only a daemon stop or the timeout ends it.
			pctx, cancel := context.WithTimeout(s.ctx, pushTimeout)
			defer cancel()
			// The remote branch must still be what the plan saw: a change since is never built on or forced.
			if tip, err := s.remoteRef(pctx, t.Worktree, plan.target, t.Branch); err != nil {
				return nil, err
			} else if tip != plan.tip {
				return nil, divergedErr(t.Branch, plan.prev, tip)
			}
			_, err := s.git(pctx, t.Worktree, gitx.ExecOptions{PushBranch: t.Branch, Timeout: pushTimeout},
				"push", "--porcelain", "origin", sha+":refs/heads/"+t.Branch)
			// runStepWith records sha and base as the ticket's pushed commit in the transaction that finishes the step.
			return map[string]any{"sha": sha, "base": plan.base}, err
		}); err != nil {
			return api.Ticket{}, err
		}
		pushed = sha
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
		err := s.runStep(ctx, id, api.OutboxOpenPR, map[string]any{"branch": t.Branch, "base": t.Base,
			"draft": draft, "title": title}, func() (map[string]any, error) {
			c, access, err := s.client(ctx, r)
			if err != nil {
				return nil, err
			}
			if c == nil {
				newPR = &api.TicketPR{Draft: draft, HeadSha: pushed}
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
				newPR = &api.TicketPR{Draft: draft, HeadSha: pushed, ManualURL: u}
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
			t.PR.HeadSha = pushed
		}
		if t.Checks != nil && t.Checks.HeadSha != pushed {
			// A new head runs CI again: earlier results do not apply until the next poll.
			t.Checks = &api.TicketChecks{State: api.ChecksPending, Runs: []api.CheckRun{}, HeadSha: pushed,
				At: api.FormatTime(s.o.Now())}
		}
		t.Status = api.TicketPROpen
		if t.PR != nil && (t.PR.State == "merged" || t.PR.State == "closed") {
			t.Status = api.TicketDone
		}
		data := map[string]any{"status": t.Status}
		if next := t.needsAttention(); next != "" {
			data["phase"] = next // what the header says (a branch pushed for the lead to open its PR by hand)
		}
		s.logStep(ctx, tx, id, "status", data)
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
	c, access, err := s.client(ctx, r)
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
	c, access, err := s.client(ctx, r)
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
	sum := sha256.Sum256([]byte(body))
	args := map[string]any{"issue": t.Issue.Number, "preview": clip(oneLine(body, 200), 120),
		"hash": hex.EncodeToString(sum[:8])}
	if err := s.runStepWith(ctx, id, api.OutboxComment, args, func(e stepEntry) (map[string]any, error) {
		// The hidden marker names the entry (G9): a retry of an attempt a stop cut off looks for it first, so the
		// comment is never posted twice.
		marker := commentMarker(e.ID)
		if e.Prev == api.OutboxUnknown {
			if u, err := c.FindComment(ctx, t.Issue.Number, marker, commentSince(e.PrevAt)); err != nil {
				return nil, err
			} else if u != "" {
				return map[string]any{"url": u, "recovered": true}, nil
			}
		}
		u, err := c.Comment(ctx, t.Issue.Number, body+"\n\n"+marker)
		return map[string]any{"url": u}, err
	}); err != nil {
		return api.Ticket{}, err
	}
	return s.Ticket(ctx, id)
}

// commentMarker is the hidden line a comment carries: the outbox entry that posted it.
func commentMarker(entryID int64) string {
	return fmt.Sprintf("<!-- seelegent:%d -->", entryID)
}

// commentSince is where the search for a comment of an attempt made at `at` starts (a minute early for clock skew).
func commentSince(at string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}
	}
	return t.Add(-time.Minute)
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
