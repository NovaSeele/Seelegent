package ticket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
)

// checksSig is what makes two check results different.
func checksSig(c *api.TicketChecks) string {
	if c == nil {
		return ""
	}
	type run struct{ N, S, C string }
	runs := make([]run, 0, len(c.Runs))
	for _, r := range c.Runs {
		runs = append(runs, run{r.Name, r.Status, r.Conclusion})
	}
	b, _ := json.Marshal([]any{c.State, c.HeadSha, runs})
	return string(b)
}

// readChecks reads the checks of the open pull request's head (gh or token tier). It returns nil when there is
// nothing to read; a failed read keeps an earlier result for the same head.
func (s *Service) readChecks(ctx context.Context, t *ticketRec, c *github.Client, access github.Access) (
	*api.TicketChecks, bool) {
	if !access.CanWrite() || c == nil {
		return nil, t.Checks != nil
	}
	if t.Status != api.TicketPROpen || t.PR == nil || t.PR.State != "open" || t.PR.HeadSha == "" {
		return t.Checks, false
	}
	sha := t.PR.HeadSha
	next, err := c.Checks(ctx, sha)
	if err != nil {
		if t.Checks != nil && t.Checks.HeadSha == sha && t.Checks.Error == "" {
			return t.Checks, false
		}
		return &api.TicketChecks{State: api.ChecksUnknown, Runs: []api.CheckRun{}, HeadSha: sha,
			At: api.FormatTime(s.o.Now()), Error: s.redactErr(err, 300)}, true
	}
	next.At = api.FormatTime(s.o.Now())
	changed := checksSig(t.Checks) != checksSig(&next) || t.Checks != nil && t.Checks.Error != ""
	return &next, changed
}

// failingNames are the names of the failing runs.
func failingNames(c *api.TicketChecks) []string {
	out := []string{}
	if c == nil {
		return out
	}
	for _, r := range c.Runs {
		if github.IsFailing(r) && len(out) < 20 {
			out = append(out, r.Name)
		}
	}
	return out
}

// Poll updates the pull requests (state, head, checks) of the open tickets and syncs the inboxes. The manual tier
// syncs only an inbox that synced fine before (public repositories).
func (s *Service) Poll(ctx context.Context) error {
	if !s.pollMu.TryLock() {
		return nil
	}
	defer s.pollMu.Unlock()
	access := s.o.GitHub.Access(ctx, false)
	var tickets []*ticketRec
	var repos []*repoRec
	if err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		if tickets, err = listTicketRecs(ctx, tx, "", ""); err != nil {
			return err
		}
		repos, err = listRepoRecs(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	byID := map[string]*repoRec{}
	for _, r := range repos {
		byID[r.ID] = r
	}
	var errs []error
	for _, t := range tickets {
		if finished(t.Status) {
			continue
		}
		r := byID[t.RepoID]
		hasNumber := t.PR != nil && t.PR.Number > 0
		if r == nil || !s.gh(r).ok() || !hasNumber && t.Status != api.TicketPROpen {
			continue
		}
		if err := s.pollTicket(ctx, t.ID, r, access); err != nil {
			errs = append(errs, err)
		}
	}
	for _, r := range repos {
		if !s.gh(r).ok() || access.Tier == api.TierManual && (r.LastSyncAt.IsZero() || r.SyncError != "") {
			continue
		}
		if _, err := s.Sync(ctx, r.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// pollTicket updates one ticket's pull request and checks.
func (s *Service) pollTicket(ctx context.Context, id string, r *repoRec, access github.Access) error {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return err
	}
	defer unlock()
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return err
	}
	g := s.gh(r)
	c, err := s.o.GitHub.Client(ctx, access, g.host, g.owner, g.name)
	if err != nil {
		return err
	}
	if t.PR == nil || t.PR.Number == 0 {
		found, err := c.FindPR(ctx, t.Branch)
		if err != nil || found == nil {
			return err
		}
		_, err = s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
			if t.PR != nil {
				found.ManualURL = t.PR.ManualURL
			}
			t.PR = found
			if found.State == "merged" || found.State == "closed" {
				t.Status = api.TicketDone
			}
			s.logStep(ctx, tx, id, "pr_found", map[string]any{"number": found.Number, "state": found.State})
			return nil
		})
		return err
	}
	st, perr := c.PRState(ctx, t.PR.Number)
	if perr != nil {
		msg := s.redactErr(perr, 300)
		_, err := s.updateTicket(ctx, id, func(_ *sql.Tx, t *ticketRec) error {
			if t.Error == msg {
				return errUnchanged
			}
			t.Error = msg
			return nil
		})
		return errors.Join(perr, err)
	}
	probe := *t
	probe.PR = &api.TicketPR{Number: t.PR.Number, State: st.State, HeadSha: st.HeadSha}
	if probe.PR.HeadSha == "" {
		probe.PR.HeadSha = t.PR.HeadSha
	}
	checks, checksChanged := s.readChecks(ctx, &probe, c, access)
	_, err = s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		if t.PR == nil {
			return errUnchanged
		}
		changed := st.State != t.PR.State || st.Draft != t.PR.Draft || st.HeadSha != "" && st.HeadSha != t.PR.HeadSha
		t.PR.State, t.PR.Draft = st.State, st.Draft
		if st.HeadSha != "" {
			t.PR.HeadSha = st.HeadSha
		}
		if st.URL != "" {
			t.PR.URL = st.URL
		}
		if (st.State == "merged" || st.State == "closed") && t.Status != api.TicketDone {
			t.Status = api.TicketDone
			s.logStep(ctx, tx, id, "status", map[string]any{"status": api.TicketDone, "pr": st.State})
			changed = true
		}
		if t.Error != "" {
			t.Error = ""
			changed = true
		}
		if checksChanged {
			t.Checks = checks
			changed = true
			if checks != nil && checks.State == api.ChecksFailure {
				s.logStep(ctx, tx, id, "checks_failed", map[string]any{"headSha": checks.HeadSha,
					"failing": failingNames(checks)})
			}
		}
		if !changed {
			return errUnchanged
		}
		return nil
	})
	return err
}

// FixChecks starts a task in the ticket's worktree that fixes the failing CI checks of its pull request, with
// each failing check's name, link and (GitHub Actions) log tail as fenced untrusted text. Refused unless the
// checks failed (read again first when the tier allows).
func (s *Service) FixChecks(ctx context.Context, id string, req api.FixChecksRequest) (api.TicketTaskResult, error) {
	unlock, err := s.locks.lock(ctx, "ticket:"+id)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	defer unlock()
	t, err := s.getTicket(ctx, id)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	if finished(t.Status) {
		return api.TicketTaskResult{}, badStatef("ticket %s is %s", id, t.Status)
	}
	if t.Checks == nil || t.Checks.State != api.ChecksFailure {
		return api.TicketTaskResult{}, badStatef("checks are %s; fix_checks needs failing checks", checksWord(t.Checks))
	}
	if busy := s.taskPending(ctx, t); len(busy) > 0 {
		return api.TicketTaskResult{}, badStatef("ticket %s has a task running (%s)", id, strings.Join(busy, ", "))
	}
	if !t.worktreeOK() {
		return api.TicketTaskResult{}, invalidf("the ticket's worktree is missing")
	}
	r, err := s.getRepo(ctx, t.RepoID)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	c, access, err := s.client(ctx, r, false)
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	if c != nil && access.CanWrite() && t.Status == api.TicketPROpen {
		checks, changed := s.readChecks(ctx, t, c, access)
		if changed {
			t, err = s.updateTicket(ctx, id, func(_ *sql.Tx, t *ticketRec) error {
				t.Checks = checks
				return nil
			})
			if err != nil {
				return api.TicketTaskResult{}, err
			}
		}
		if t.Checks == nil || t.Checks.State != api.ChecksFailure {
			return api.TicketTaskResult{}, badStatef("checks are now %s; nothing to fix", checksWord(t.Checks))
		}
	}
	checks := t.Checks
	var failing []api.CheckRun
	for _, run := range checks.Runs {
		if github.IsFailing(run) && len(failing) < maxFixRuns {
			failing = append(failing, run)
		}
	}
	items := make([]fixItem, 0, len(failing))
	for _, run := range failing {
		if run.JobID > 0 && (run.App == "github-actions" || run.RunID > 0) {
			if c == nil || !access.CanWrite() {
				items = append(items, fixItem{run: run, log: run.Summary, kind: "summary",
					note: "job log not available: needs gh or a token"})
				continue
			}
			text, err := c.JobLog(ctx, run.JobID)
			if err != nil {
				items = append(items, fixItem{run: run, log: run.Summary, kind: "summary",
					note: "job log not available: " + s.redactErr(err, 200)})
				continue
			}
			tail := github.LogTail(s.redact(text), github.LogTailMax)
			it := fixItem{run: run, log: tail, kind: "log"}
			if tail == "" {
				it.note = "the job log was empty"
			}
			items = append(items, it)
			continue
		}
		it := fixItem{run: run, log: clip(run.Summary, github.LogTailMax), kind: "summary"}
		if run.Summary == "" {
			it.note = "no log for this check provider; open the link"
		}
		items = append(items, it)
	}
	prompt := fixChecksPrompt(checks, items, req.Extra)
	member, backend := strings.TrimSpace(req.Member), req.Backend
	if member == "" && len(t.TaskIDs) > 0 {
		if last, err := s.o.Tasks.Summary(ctx, t.TaskIDs[len(t.TaskIDs)-1]); err == nil {
			member = last.Member
			if member == "" && backend == "" {
				backend = last.Backend
			}
		}
	}
	issue := s.issueOf(ctx, t)
	taskID, err := s.startTask(ctx, t, issue, prompt, api.TicketTaskOptions{Member: member, Backend: backend,
		Model: req.Model}, clip("fix checks "+issueLabel(issue)+" "+oneLine(issue.Title, 100), 80))
	if err != nil {
		return api.TicketTaskResult{}, err
	}
	names := make([]string, 0, len(failing))
	for _, run := range failing {
		names = append(names, run.Name)
	}
	if _, err := s.updateTicket(ctx, id, func(tx *sql.Tx, t *ticketRec) error {
		t.ChecksFix = &api.ChecksFix{TaskID: taskID, HeadSha: checks.HeadSha, Runs: names,
			At: api.FormatTime(s.o.Now())}
		s.logStep(ctx, tx, id, "fix_checks", map[string]any{"taskId": taskID, "headSha": checks.HeadSha,
			"runs": names})
		return nil
	}); err != nil {
		return api.TicketTaskResult{}, err
	}
	return api.TicketTaskResult{TicketID: id, TaskID: taskID}, nil
}

func checksWord(c *api.TicketChecks) string {
	if c == nil || c.State == "" {
		return "not known"
	}
	return c.State
}
