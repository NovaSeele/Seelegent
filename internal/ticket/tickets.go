package ticket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/worktree"
)

// notifyBuf is the room of the task follower's subscription.
const notifyBuf = 256

// Start recovers the outbox (entries left running become unknown and are re-checked), brings every ticket's
// status up to date with its tasks, and starts the task follower and the poller.
func (s *Service) Start(ctx context.Context) error {
	var tickets []string
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		tickets, _, err = recoverRunning(ctx, tx)
		if err != nil {
			return err
		}
		for _, id := range tickets {
			s.logStep(ctx, tx, id, "outbox_unknown", nil)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var all []*ticketRec
	if err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		all, err = listTicketRecs(ctx, tx, "", "")
		return err
	}); err != nil {
		return err
	}
	for _, t := range all {
		if derived(t.Status) {
			s.derive(ctx, t.ID, nil)
		}
	}
	s.wg.Add(3)
	go func() { defer s.wg.Done(); s.follow() }()
	go func() { defer s.wg.Done(); s.pollLoop() }()
	go func() {
		defer s.wg.Done()
		for _, id := range tickets {
			if err := s.Recheck(s.ctx, id); err != nil && s.ctx.Err() == nil {
				s.log.Warn("ticket: recheck outbox", "ticket", id, "err", s.redactErr(err, 300))
			}
		}
	}()
	return nil
}

// Close stops the follower and the poller.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// follow keeps ticket statuses in step with their tasks.
func (s *Service) follow() {
	bus := s.o.Tasks.Bus()
	sub := bus.Subscribe(notifyBuf)
	defer func() { sub.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case n, open := <-sub.C():
			if !open {
				if s.ctx.Err() != nil {
					return
				}
				sub = bus.Subscribe(notifyBuf)
				s.resyncAll()
				continue
			}
			if n.Kind != task.NoticeTask {
				continue
			}
			s.onTask(n.TaskID)
		}
	}
}

// resyncAll derives every open ticket again (after the follower missed notices).
func (s *Service) resyncAll() {
	var all []*ticketRec
	if err := s.o.DB.Read(s.ctx, func(tx *sql.Tx) error {
		var err error
		all, err = listTicketRecs(s.ctx, tx, "", "")
		return err
	}); err != nil {
		return
	}
	for _, t := range all {
		if derived(t.Status) || t.Status == api.TicketPROpen {
			s.derive(s.ctx, t.ID, nil)
		}
	}
}

// onTask links a task to its ticket (a ticket task, or a task a ticket's meeting started) and updates the status.
func (s *Service) onTask(taskID string) {
	sum, err := s.o.Tasks.Summary(s.ctx, taskID)
	if err != nil || sum.Kind != task.KindNormal {
		return
	}
	ticketID := sum.TicketID
	if ticketID == "" && sum.MeetingID != "" {
		_ = s.o.DB.Read(s.ctx, func(tx *sql.Tx) error {
			all, err := listTicketRecs(s.ctx, tx, "", "")
			if err != nil {
				return err
			}
			for _, t := range all {
				if t.MeetingID == sum.MeetingID && !finished(t.Status) {
					ticketID = t.ID
					return nil
				}
			}
			return nil
		})
	}
	if ticketID == "" {
		return
	}
	s.derive(s.ctx, ticketID, &sum)
}

// deriveStatus is a ticket's status from its tasks: working while one is pending, ready when the last one was
// accepted, review otherwise.
func deriveStatus(tasks []api.TaskSummary) string {
	if len(tasks) == 0 {
		return api.TicketWorking
	}
	for ix := range tasks {
		t := &tasks[ix]
		if task.Pending(t.Status) {
			return api.TicketWorking
		}
	}
	// An accepted task makes the ticket ready, a failed one too: the user took its work as it is (as before).
	last := tasks[len(tasks)-1]
	if (last.Status == task.StatusDone || last.Status == api.StatusFailed) && last.Review != nil &&
		last.Review.Verdict == api.VerdictAccepted {
		return api.TicketReady
	}
	return api.TicketReview
}

// derive adds the task sum (when given) to the ticket and works its status out again.
func (s *Service) derive(ctx context.Context, id string, sum *api.TaskSummary) {
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return
	}
	ids := slices.Clone(t.TaskIDs)
	if sum != nil && !slices.Contains(ids, sum.ID) {
		ids = append(ids, sum.ID)
	}
	sums := map[string]api.TaskSummary{}
	for _, tid := range ids {
		if sum != nil && tid == sum.ID {
			sums[tid] = *sum
			continue
		}
		if x, err := s.o.Tasks.Summary(ctx, tid); err == nil {
			sums[tid] = x
		}
	}
	_, err = s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		if finished(t.Status) {
			return errUnchanged
		}
		changed := false
		if sum != nil && !slices.Contains(t.TaskIDs, sum.ID) {
			// New work changes the worktree: an earlier refusal no longer says what the next pr will find.
			t.TaskIDs = append(t.TaskIDs, sum.ID)
			t.dropRefusal()
			s.logStep(ctx, tx, t.ID, "task", map[string]any{"taskId": sum.ID})
			changed = true
		}
		if t.Status == api.TicketPROpen {
			if sum == nil || !task.Pending(sum.Status) {
				if changed {
					return nil
				}
				return errUnchanged
			}
			// New work after the pull request opened: back to working; pr pushes it to the same pull request.
			t.Status = api.TicketWorking
			changed = true
		}
		var list []api.TaskSummary
		for _, tid := range t.TaskIDs {
			if x, ok := sums[tid]; ok {
				list = append(list, x)
			}
		}
		if next := deriveStatus(list); next != t.Status {
			t.Status = next
			s.logStep(ctx, tx, t.ID, "status", map[string]any{"status": next})
			changed = true
		}
		// A task of the ticket runs again (a followup on it): the worktree changes, so a refusal goes too.
		if t.Status == api.TicketWorking && (t.Refusal != nil || t.Error != "") {
			t.dropRefusal()
			changed = true
		}
		if !changed {
			return errUnchanged
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
		s.log.Warn("ticket: derive status", "ticket", id, "err", err)
	}
}

// createTicket makes the ticket of an issue: a new branch seelegent/<n>-<slug> and its worktree at the default
// branch, with the repository's environment. A failure leaves a failed ticket (returned without error).
func (s *Service) createTicket(ctx context.Context, r *repoRec, issue api.Issue) (*ticketRec, error) {
	now := s.o.Now()
	n := strconv.Itoa(issue.Number)
	if issue.Number <= 0 {
		n = "local-" + strconv.Itoa(issue.LocalID)
	}
	slug := slugify(issue.Title)
	if slug == "" {
		slug = "issue"
	}
	parent := filepath.Join(s.o.WorktreeBase, r.ID)
	t := &ticketRec{ID: newTicketID(now), RepoID: r.ID, IssueKey: issue.Key,
		Issue: api.TicketIssue{Number: issue.Number, Title: issue.Title, URL: issue.URL}, Slug: slug,
		Base: r.DefaultBranch, Status: api.TicketWorking, TaskIDs: []string{}, CreatedAt: now, UpdatedAt: now}
	for i := 1; i <= 50; i++ {
		stem := n + "-" + slug
		if i > 1 {
			stem += "-" + strconv.Itoa(i)
		}
		b := gitx.BranchPrefix + stem
		w := filepath.Join(parent, stem)
		if _, exists := s.gitOK(ctx, r.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); exists {
			continue
		}
		if _, err := os.Lstat(w); err == nil {
			continue
		}
		t.Branch, t.Worktree = b, w
		break
	}
	fail := func(err error) {
		t.Status, t.Error, t.ErrorCause = api.TicketFailed, s.redactErr(err, 600), ""
	}
	var env *api.WorktreeEnv
	if t.Branch == "" {
		t.Branch, t.Worktree = gitx.BranchPrefix+n+"-"+slug, filepath.Join(parent, n+"-"+slug)
		fail(invalidf("could not find a free branch name"))
	} else if err := s.addWorktree(ctx, r, t); err != nil {
		fail(err)
	} else {
		e := (&worktree.Trees{Git: s.o.Git, Base: s.o.WorktreeBase}).Setup(ctx, r.Path, t.Worktree, r.SharedDirs)
		env = &e
		t.Env = env
	}
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := saveTicket(ctx, tx, t); err != nil {
			return err
		}
		if t.Status == api.TicketFailed {
			s.logStep(ctx, tx, t.ID, "worktree_failed", map[string]any{"error": t.Error})
			return nil
		}
		s.logStep(ctx, tx, t.ID, "worktree", map[string]any{"branch": t.Branch, "worktree": t.Worktree,
			"baseSha": t.BaseSha})
		if env != nil && len(env.Copied)+len(env.Linked)+len(env.Skipped) > 0 {
			s.logStep(ctx, tx, t.ID, "env", map[string]any{"copied": env.Copied, "linked": env.Linked,
				"skipped": env.Skipped})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.o.TicketChanged(t.summary())
	return t, nil
}

// addWorktree finds the base commit and adds the ticket's worktree on its new branch.
func (s *Service) addWorktree(ctx context.Context, r *repoRec, t *ticketRec) error {
	sha, ok := s.gitOK(ctx, r.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+t.Base+"^{commit}")
	if !ok || sha == "" {
		sha, ok = s.gitOK(ctx, r.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+t.Base+"^{commit}")
	}
	if !ok || sha == "" {
		return invalidf("the base branch %s has no commit", t.Base)
	}
	t.BaseSha = sha
	unlock, err := s.o.Git.Lock(ctx, r.Path)
	if err != nil {
		return err
	}
	defer unlock()
	trees := &worktree.Trees{Git: s.o.Git, Base: s.o.WorktreeBase}
	wt, err := trees.AddBranch(ctx, r.Path, t.Worktree, t.Branch, sha)
	if err != nil {
		var ge *gitx.Error
		if errors.As(err, &ge) {
			return &GitError{Op: "worktree", Message: oneLine(s.redact(ge.Error()), 600)}
		}
		return err
	}
	t.Worktree = wt
	return nil
}

// laneFor is a ticket task's lane: the whole worktree unless the lead gives one or none.
func laneFor(o api.TicketTaskOptions) []string {
	if o.NoLane {
		return nil
	}
	if len(o.Lane) > 0 {
		return o.Lane
	}
	return []string{"**"}
}

// startTask delegates a task in the ticket's worktree and links it. Without a member or a backend it goes to whoever
// the ticket's last task was asked for ("as before"); who it is asked for is kept for the next one.
func (s *Service) startTask(ctx context.Context, t *ticketRec, issue api.Issue, prompt string,
	o api.TicketTaskOptions, title string) (string, error) {
	if o.Member == "" && o.Backend == "" && t.Asked != nil {
		o.Member, o.Backend = t.Asked.Member, t.Asked.Backend
		if o.Model == "" {
			o.Model = t.Asked.Model
		}
	}
	ask := &api.TicketAsk{Member: o.Member, Backend: o.Backend, Model: o.Model}
	if _, err := s.updateTicket(ctx, t.ID, func(_ *sql.Tx, r *ticketRec) error {
		r.Asked = ask
		return nil
	}); err != nil {
		return "", err
	}
	t.Asked = ask
	req := api.DelegateRequest{Prompt: ticketBrief(issue, t.Branch, prompt), Cwd: t.Worktree, Title: title,
		Member: o.Member, Backend: o.Backend, Model: o.Model, Gate: o.Gate, GateCommands: o.GateCommands,
		Lane: laneFor(o), NoLane: o.NoLane}
	sum, err := s.o.Tasks.DelegateWith(ctx, req, task.Spawn{TicketID: t.ID})
	if err != nil {
		return "", err
	}
	s.derive(context.WithoutCancel(ctx), t.ID, &sum)
	return sum.ID, nil
}

// issueOf is the inbox entry of a ticket's issue (or what the ticket remembers of it).
func (s *Service) issueOf(ctx context.Context, t *ticketRec) api.Issue {
	var it api.Issue
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		it, err = loadIssue(ctx, tx, t.RepoID, t.IssueKey)
		return err
	})
	if err == nil {
		return it
	}
	src := "github"
	if strings.HasPrefix(t.IssueKey, "local#") {
		src = "local"
	}
	return api.Issue{Key: t.IssueKey, Number: t.Issue.Number, Source: src, Title: t.Issue.Title, URL: t.Issue.URL,
		Labels: []string{}}
}

// worktreeOK reports whether the ticket's worktree is there.
func (t *ticketRec) worktreeOK() bool {
	if t.Worktree == "" || t.WorktreeRemoved {
		return false
	}
	fi, err := os.Stat(t.Worktree)
	return err == nil && fi.IsDir()
}

// AddTask starts another task in a ticket's worktree.
func (s *Service) AddTask(ctx context.Context, id string, req api.TicketTaskRequest) (api.TicketTaskResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return api.TicketTaskResult{}, invalidf("prompt is required")
	}
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	if finished(t.Status) {
		return api.TicketTaskResult{}, badStatef("ticket %s is %s", id, t.Status)
	}
	if !t.worktreeOK() {
		return api.TicketTaskResult{}, invalidf("the ticket's worktree is missing")
	}
	issue := s.issueOf(ctx, t)
	taskID, err := s.startTask(ctx, t, issue, req.Prompt, req.TicketTaskOptions,
		clip(issueLabel(issue)+" "+oneLine(issue.Title, 120), 80))
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	return api.TicketTaskResult{TicketID: id, TaskID: taskID}, nil
}

// Retry starts a task again in a failed ticket whose worktree is there: its first task or meeting could not start
// (an unknown member, no usable account). The lead or the user may pick another member, backend or model; an empty
// prompt works on the issue as written. The ticket is working again.
func (s *Service) Retry(ctx context.Context, id string, req api.TicketTaskRequest) (api.TicketTaskResult, error) {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	defer unlock()
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	if t.Status != api.TicketFailed {
		return api.TicketTaskResult{}, badStatef("ticket %s is %s; retry needs failed", id, t.Status)
	}
	if !t.worktreeOK() {
		return api.TicketTaskResult{}, badStatef("the ticket's worktree is missing; drop it and decide the issue again")
	}
	// "As before" asks the member that is gone or turned off again: refused before anything changes (F69).
	if f := t.Failure; f != nil && f.Member != "" && req.Member == "" && req.Backend == "" {
		return api.TicketTaskResult{}, &FailureError{Failure: *f}
	}
	if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		t.Status, t.Error, t.Failure = api.TicketWorking, "", nil
		s.logStep(ctx, tx, id, "retry", map[string]any{"member": req.Member, "backend": req.Backend})
		return nil
	}); err != nil {
		return api.TicketTaskResult{}, err
	}
	issue := s.issueOf(ctx, t)
	taskID, err := s.startTask(ctx, t, issue, req.Prompt, req.TicketTaskOptions,
		clip(issueLabel(issue)+" "+oneLine(issue.Title, 120), 80))
	if err != nil {
		s.failTicket(ctx, id, "task_failed", err)
		return api.TicketTaskResult{}, err
	}
	return api.TicketTaskResult{TicketID: id, TaskID: taskID}, nil
}

// Tickets lists tickets, newest change first; repoID and statuses filter.
func (s *Service) Tickets(ctx context.Context, repoID string, statuses []string) ([]api.TicketSummary, error) {
	for _, st := range statuses {
		if !slices.Contains([]string{api.TicketWorking, api.TicketReview, api.TicketReady, api.TicketPROpen,
			api.TicketDone, api.TicketDropped, api.TicketFailed}, st) {
			return nil, invalidf("unknown ticket status %q", st)
		}
	}
	out := []api.TicketSummary{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		list, err := listTicketRecs(ctx, tx, repoID, "")
		if err != nil {
			return err
		}
		out = out[:0]
		for _, t := range list {
			if len(statuses) == 0 || slices.Contains(statuses, t.Status) {
				out = append(out, t.summary())
			}
		}
		return nil
	})
	return out, err
}

// Ticket returns the full ticket: its tasks, the last gates, the files the branch changes and its log.
func (s *Service) Ticket(ctx context.Context, id string) (api.Ticket, error) {
	var (
		t      *ticketRec
		outbox []api.OutboxEntry
		logs   []api.TicketLogEntry
	)
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		if t, err = loadTicket(ctx, tx, id); err != nil {
			return err
		}
		if outbox, err = listEntries(ctx, tx, scopeTicket, id); err != nil {
			return err
		}
		logs, err = ticketLog(ctx, tx, id)
		return err
	})
	if err != nil {
		return api.Ticket{}, err
	}
	out := api.Ticket{TicketSummary: t.summary(), Slug: t.Slug, Base: t.Base, BaseSha: t.BaseSha, Env: t.Env,
		Outbox: outbox, ChecksFix: t.ChecksFix, DropReason: t.DropReason, WorktreeRemoved: t.WorktreeRemoved,
		CleanedAt: api.FormatTime(t.CleanedAt), Exposed: t.Exposed, Tasks: []api.TicketTask{}, Gates: []api.TicketGate{},
		Log: logs}
	for _, tid := range t.TaskIDs {
		d, err := s.o.Tasks.Get(ctx, tid)
		if err != nil {
			continue // deleted with the task history
		}
		tt := api.TicketTask{ID: d.ID, Title: d.Title, Status: d.Status, Member: d.Member, Turns: len(d.Turns),
			UpdatedAt: d.UpdatedAt}
		if d.Review != nil {
			tt.Verdict = d.Review.Verdict
		}
		out.Tasks = append(out.Tasks, tt)
		for ix := range d.Turns {
			tu := &d.Turns[ix]
			if tu.Gate == nil || tu.Gate.State == "" {
				continue
			}
			at := tu.Gate.EndedAt
			if at == "" {
				at = tu.Gate.StartedAt
			}
			out.Gates = append(out.Gates, api.TicketGate{TaskID: d.ID, Turn: tu.N, State: tu.Gate.State, At: at,
				Steps: api.NonNil(tu.Gate.Steps)})
		}
	}
	slices.SortStableFunc(out.Gates, func(a, b api.TicketGate) int { return strings.Compare(b.At, a.At) })
	if len(out.Gates) > 4 {
		out.Gates = out.Gates[:4]
	}
	out.Files = s.files(ctx, t)
	return out, nil
}

// files lists what the ticket's branch and worktree change against the base.
func (s *Service) files(ctx context.Context, t *ticketRec) []api.TicketFile {
	files := []api.TicketFile{}
	if !t.worktreeOK() || t.BaseSha == "" {
		return files
	}
	out, err := s.o.Git.Exec(ctx, t.Worktree, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "diff", "--name-status",
		"-M", "-z", t.BaseSha)
	if err == nil {
		parts := strings.Split(string(out), "\x00")
		for i := 0; i < len(parts); i++ {
			st := parts[i]
			if st == "" {
				continue
			}
			if (st[0] == 'R' || st[0] == 'C') && i+2 < len(parts) {
				files = append(files, api.TicketFile{Status: st[:1], From: parts[i+1], Path: parts[i+2]})
				i += 2
				continue
			}
			if i+1 < len(parts) {
				files = append(files, api.TicketFile{Status: st[:1], Path: parts[i+1]})
				i++
			}
		}
	}
	if u, err := s.o.Git.Exec(ctx, t.Worktree, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "ls-files", "--others",
		"--exclude-standard", "-z"); err == nil {
		for _, p := range strings.Split(string(u), "\x00") {
			if p != "" {
				files = append(files, api.TicketFile{Status: "?", Path: p})
			}
		}
	}
	return files
}

// ticketLog reads the last steps of a ticket's log.
func ticketLog(ctx context.Context, tx *sql.Tx, id string) ([]api.TicketLogEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, at, type, data FROM (SELECT id, at, type, data FROM ticket_log
		WHERE ticket_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, id, maxLogEntries)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []api.TicketLogEntry{}
	for rows.Next() {
		var e api.TicketLogEntry
		var raw string
		if err := rows.Scan(&e.ID, &e.At, &e.Type, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &e.Data); err != nil {
			return nil, err
		}
		if e.Data == nil {
			e.Data = map[string]any{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Drop ends a ticket without a pull request: its running tasks are cancelled; the worktree stays until cleanup.
func (s *Service) Drop(ctx context.Context, id string, req api.DropRequest) (api.Ticket, error) {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlock()
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.Ticket{}, err
	}
	if t.Status == api.TicketDone || t.Status == api.TicketDropped {
		return api.Ticket{}, badStatef("ticket %s is already %s", id, t.Status)
	}
	for _, tid := range t.TaskIDs {
		if sum, err := s.o.Tasks.Summary(ctx, tid); err == nil && task.Pending(sum.Status) {
			if _, _, err := s.o.Tasks.Cancel(ctx, tid, api.CancelRequest{}); err != nil {
				s.log.Warn("ticket: cancel task on drop", "ticket", id, "task", tid, "err", err)
			}
		}
	}
	reason := oneLine(req.Reason, maxReason)
	if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		t.Status, t.DropReason = api.TicketDropped, reason
		t.dropRefusal()
		s.logStep(ctx, tx, id, "drop", map[string]any{"reason": reason})
		return nil
	}); err != nil {
		return api.Ticket{}, err
	}
	return s.Ticket(ctx, id)
}

// Cleanup removes a finished ticket's clean worktree (the branch is kept). A dirty worktree is refused.
func (s *Service) Cleanup(ctx context.Context, id string) (api.Ticket, error) {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.Ticket{}, err
	}
	defer unlock()
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.Ticket{}, err
	}
	if !finished(t.Status) {
		return api.Ticket{}, badStatef("ticket %s is %s; cleanup needs done, dropped or failed", id, t.Status)
	}
	if t.WorktreeRemoved {
		return s.Ticket(ctx, id)
	}
	r, err := s.getRepo(ctx, t.RepoID)
	if err != nil {
		return api.Ticket{}, err
	}
	if t.Worktree != "" && t.BaseSha != "" {
		unlockRepo, err := s.o.Git.Lock(ctx, r.Path)
		if err != nil {
			return api.Ticket{}, err
		}
		trees := &worktree.Trees{Git: s.o.Git, Base: s.o.WorktreeBase}
		err = trees.RemoveClean(ctx, r.Path, t.Worktree, t.Env)
		unlockRepo()
		if errors.Is(err, worktree.ErrDirty) {
			return api.Ticket{}, badStatef("%v", err)
		}
		if err != nil {
			var ge *gitx.Error
			if errors.As(err, &ge) {
				return api.Ticket{}, &GitError{Op: "worktree", Message: oneLine(s.redact(ge.Error()), 600)}
			}
			return api.Ticket{}, err
		}
		trees.RemoveEmpty(filepath.Dir(t.Worktree))
	}
	if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		t.WorktreeRemoved, t.CleanedAt = true, s.o.Now()
		s.logStep(ctx, tx, id, "cleanup", map[string]any{"worktree": t.Worktree, "branchKept": t.Branch})
		return nil
	}); err != nil {
		return api.Ticket{}, err
	}
	return s.Ticket(ctx, id)
}

// pollLoop polls pull requests, checks and inboxes every githubPollMinutes (re-read each round; 0 waits).
func (s *Service) pollLoop() {
	for {
		minutes := s.o.PollMinutes()
		wait := time.Duration(minutes) * time.Minute
		if minutes <= 0 {
			wait = time.Minute
		}
		timer := time.NewTimer(wait)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if s.o.PollMinutes() > 0 {
			if err := s.Poll(s.ctx); err != nil && s.ctx.Err() == nil {
				s.log.Warn("github: poll", "err", s.redactErr(err, 300))
			}
		}
	}
}
