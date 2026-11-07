package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/answer"
	"github.com/NovaSeele/Seelegent/internal/api"
)

// Consults (docs/team.md): a senior or peer member reads a finished task and gives a second opinion. The consult
// runs as a hidden read-only child (KindConsult) on the member's backend; its answer is parsed into a verdict,
// findings with checked citations and required changes, stored on the task (Task.Consults). An unanswered peer
// objection blocks review accepted until the lead answers it (ReviewRequest.AnswerObjection).

// Consult limits.
const (
	consultFocusMax   = 4000
	consultRequestMax = 8000
	consultReportMax  = 20000
	consultDiffMax    = 40000
	consultFilesMax   = 100
	consultPoll       = 5 * time.Second
)

// Consult record states.
const (
	consultRunning = "running"
	consultDone    = "done"
	consultFailed  = "failed"
)

var (
	consultVerdictRe  = regexp.MustCompile(`^verdict`)
	consultFindingsRe = regexp.MustCompile(`^(findings|claims)`)
	consultRequiredRe = regexp.MustCompile(`^required`)
	consultWordRe     = regexp.MustCompile(`(?i)\b(approve|concerns|object)\b`)
)

// PeerObjectionError refuses review accepted while a peer objection has no answer.
type PeerObjectionError struct {
	// Consults are the ids of the unanswered peer objections.
	Consults []string
}

func (e *PeerObjectionError) Error() string {
	return "a peer member objects (" + strings.Join(e.Consults, ", ") + "); answer the objection " +
		"(answerObjection) to accept, or reject the task"
}

// Is makes errors.Is(err, ErrConflict) true.
func (e *PeerObjectionError) Is(target error) bool { return target == ErrConflict }

// APIDetails are the details of the API error.
func (e *PeerObjectionError) APIDetails() map[string]any {
	return map[string]any{"reason": "peer_objection", "consults": e.Consults}
}

// consultsAPI returns copies of the consult records with non-nil lists.
func consultsAPI(cs []api.Consult) []api.Consult {
	out := make([]api.Consult, 0, len(cs))
	for i := range cs {
		c := cs[i]
		c.Findings = api.NonNil(c.Findings)
		for i := range c.Findings {
			c.Findings[i].Cites = api.NonNil(c.Findings[i].Cites)
		}
		c.Required = api.NonNil(c.Required)
		out = append(out, c)
	}
	return out
}

// Consult asks member req.Member (rank senior or peer) for a second opinion on task id, whose turn has ended. It
// returns the new consult record; the opinion arrives when the run ends (GET /tasks/{id}).
func (s *Service) Consult(ctx context.Context, id string, req api.ConsultRequest) (api.Consult, error) {
	focus := strings.TrimSpace(req.Focus)
	if utf8.RuneCountInString(focus) > consultFocusMax {
		return api.Consult{}, invalidf("focus is longer than %d characters", consultFocusMax)
	}
	if strings.TrimSpace(req.Member) == "" {
		return api.Consult{}, invalidf("member is required")
	}
	ended := func(t *Task) error {
		if Hidden(t.Kind) {
			return ErrNotFound
		}
		if Pending(t.Status) || t.Manual || t.Status == StatusBlockedDeps {
			return badStatef("task %s is %s; consult after its turn ended", id, t.Status)
		}
		return nil
	}
	// The check and the copy the brief is made from are taken under the task's lock; the run is created after it
	// is released (lock order: claimMu, then the task), and the record is only added when no turn began meanwhile.
	t, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		if err := ended(t); err != nil {
			return nil, err
		}
		return nil, errNoChange
	})
	if err != nil {
		return api.Consult{}, err
	}
	turns := len(t.Turns)
	m, err := s.usableMember(ctx, strings.TrimSpace(req.Member))
	if err != nil {
		return api.Consult{}, err
	}
	if m.Rank != api.RankSenior && m.Rank != api.RankPeer {
		return api.Consult{}, invalidf("member %s is a %s; a consult needs a senior or a peer", m.ID, m.Rank)
	}
	prompt := s.consultBrief(ctx, t, m, focus)
	sum, err := s.DelegateWith(ctx, api.DelegateRequest{Prompt: prompt, Cwd: t.Cwd, Title: "Consult: " + t.Title,
		Member: m.ID, Mode: ModePlan, Session: t.Session}, Spawn{Kind: KindConsult, ParentID: t.ID,
		ProjectCwd: t.ProjectCwd})
	if err != nil {
		return api.Consult{}, err
	}
	var rec api.Consult
	_, err = s.mutate(s.background(), id, func(cur *Task) ([]NewEvent, error) {
		if err := ended(cur); err != nil {
			return nil, err
		}
		if len(cur.Turns) != turns {
			return nil, badStatef("task %s started a new turn; consult after it ended", id)
		}
		n := len(cur.Consults) + 1
		rec = api.Consult{ID: "c" + strconv.Itoa(n), Member: m.ID, Rank: m.Rank, RunTaskID: sum.ID,
			Status: consultRunning, Findings: []api.Claim{}, Required: []string{}, Focus: focus,
			At: api.FormatTime(s.now())}
		cur.Consults = append(cur.Consults, rec)
		return nil, nil
	})
	if err != nil {
		// The run has no record to report to: stop it and close it, so it never waits for a review.
		s.StopHiddenRun(sum.ID, "consult not stored")
		return api.Consult{}, err
	}
	s.watchConsult(id, rec.ID, sum.ID)
	return rec, nil
}

// consultRole is the first line of a consult brief.
func consultRole(m api.Member) string {
	if m.Rank == api.RankSenior {
		return "You are " + m.Name + ", a senior engineer. The lead asks for your review of a worker's finished " +
			"task; the lead decides, and gives your view extra weight."
	}
	return "You are " + m.Name + ", a peer reviewer of equal standing with the lead. The lead asks for your " +
		"independent review of a worker's finished task. If you object, the lead must answer your objection in " +
		"writing before accepting the task."
}

// consultBrief is the brief of a consult run.
func (s *Service) consultBrief(ctx context.Context, t *Task, m api.Member, focus string) string {
	var b strings.Builder
	b.WriteString(consultRole(m) + "\n\n" + answer.ReadOnlyRule + "\n\n")
	fmt.Fprintf(&b, "## The task\n%s\n\n### Original request\n%s\n\n", t.Title, answer.Clip(t.Prompt, consultRequestMax))
	if tu := t.LastTurn(); tu != nil {
		if tu.N > 1 {
			fmt.Fprintf(&b, "### Latest instruction (turn %d)\n%s\n\n", tu.N, answer.Clip(tu.Message, consultRequestMax))
		}
		if r := strings.TrimSpace(tu.Response); r != "" {
			fmt.Fprintf(&b, "### The worker's last report\n%s\n\n", answer.Clip(r, consultReportMax))
		}
	}
	files := t.FilesChanged()
	if len(files) > 0 {
		b.WriteString("## Files changed\n")
		for i, f := range files {
			if i == consultFilesMax {
				fmt.Fprintf(&b, "- … %d more\n", len(files)-i)
				break
			}
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
		probe := *t
		if probe.RepoRoot == "" {
			probe.RepoRoot = s.gitRoot(ctx, t.Cwd)
		}
		if d := s.turnDiff(ctx, &probe, files); d != "" {
			b.WriteString("## Diff against HEAD\n```diff\n" + answer.Clip(d, consultDiffMax) + "\n```\n\n")
		}
	}
	if focus != "" {
		b.WriteString("## What the lead wants you to look at\n" + focus + "\n\n")
	}
	fmt.Fprintf(&b, "Working folder: %s\n\n%s\n\n", t.Cwd, answer.CitationRule)
	b.WriteString("## Answer in exactly this format\n## Verdict\napprove, concerns or object\n\n## Findings\n" +
		"- one finding per bullet, each citing [path:line] \"exact quote\"\n\n## Required changes\n" +
		"- what must change before the task is accepted (or: none)\n\n" +
		"Object only for real defects (a bug, a requirement not met, broken or missing tests, a security problem); " +
		"use concerns for risks and improvements.")
	return b.String()
}

// parseConsult reads the answer of a consult run. A missing or unknown verdict counts as concerns.
func parseConsult(text, root string) (verdict string, findings []api.Claim, required []string) {
	secs := answer.Sections(text)
	verdict = api.ConsultConcerns
	if body, ok := answer.Pick(secs, consultVerdictRe); ok {
		if m := consultWordRe.FindStringSubmatch(body); m != nil {
			verdict = strings.ToLower(m[1])
		}
	}
	if body, ok := answer.Pick(secs, consultFindingsRe); ok {
		findings = answer.CheckClaims(root, answer.Claims(body))
	}
	if body, ok := answer.Pick(secs, consultRequiredRe); ok {
		required = answer.Questions(body)
	}
	return verdict, api.NonNil(findings), api.NonNil(required)
}

// watchConsult follows a consult run until it ends and stores the opinion.
func (s *Service) watchConsult(taskID, consultID, runID string) {
	s.whenFinished("consult", runID, func(run *Task) { s.finishConsult(taskID, consultID, run) })
}

// whenFinished calls done once the run has finished (nil: the run is gone), from its own goroutine; it gives up
// when the service stops.
func (s *Service) whenFinished(name, runID string, done func(run *Task)) {
	s.spawn(name, "", func() {
		for {
			changed, release := s.bus.TasksChanged([]string{runID})
			run, err := s.repo.Get(s.root, runID)
			if errors.Is(err, ErrNotFound) || (err == nil && Finished(run.Status)) {
				release()
				if err != nil {
					run = nil
				}
				done(run)
				return
			}
			timer := time.NewTimer(consultPoll)
			select {
			case <-changed:
			case <-timer.C:
			case <-s.root.Done():
			}
			timer.Stop()
			release()
			if s.root.Err() != nil {
				return
			}
		}
	})
}

// finishConsult stores the outcome of a consult run (nil: the run is gone).
func (s *Service) finishConsult(taskID, consultID string, run *Task) {
	ctx := s.background()
	rec := api.Consult{Status: consultFailed}
	switch {
	case run == nil:
		rec.Error = "the consult run is gone"
	case run.Status != StatusDone || run.LastTurn() == nil:
		rec.Error = "the consult run ended " + string(run.Status)
		if run.Error != "" {
			rec.Error += ": " + run.Error
		}
	default:
		parent, err := s.repo.Get(ctx, taskID)
		if err != nil {
			s.log.Warn("task: consult parent", "task", taskID, "err", err)
			return
		}
		root := parent.RepoRoot
		if root == "" {
			if root = s.gitRoot(ctx, parent.Cwd); root == "" {
				root = parent.Cwd
			}
		}
		rec.Status = consultDone
		rec.Verdict, rec.Findings, rec.Required = parseConsult(run.LastTurn().Response, root)
		if files := run.LastTurn().Files; len(files) > 0 {
			rec.Error = "the consult run changed files: " + strings.Join(files, ", ")
		}
	}
	_, err := s.mutate(ctx, taskID, func(t *Task) ([]NewEvent, error) {
		for i := range t.Consults {
			c := &t.Consults[i]
			if c.ID != consultID || c.Status != consultRunning {
				continue
			}
			c.Status, c.Verdict, c.Error, c.EndedAt = rec.Status, rec.Verdict, rec.Error, api.FormatTime(s.now())
			c.Findings, c.Required = api.NonNil(rec.Findings), api.NonNil(rec.Required)
			return nil, nil
		}
		return nil, errNoChange
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.log.Warn("task: store the consult", "task", taskID, "consult", consultID, "err", err)
	}
	if run != nil {
		s.closeHiddenRun(run.ID, "consult "+rec.Status)
	}
}

// closeHiddenRun marks a finished hidden run as reviewed by the engine, so it never waits for the lead.
func (s *Service) closeHiddenRun(id, notes string) {
	_, err := s.mutate(s.background(), id, func(c *Task) ([]NewEvent, error) {
		if !Finished(c.Status) || c.Review != nil {
			return nil, errNoChange
		}
		c.Review = &api.Review{Verdict: api.VerdictAccepted, Notes: notes, At: api.FormatTime(s.now())}
		return nil, nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.log.Warn("task: close a hidden run", "task", id, "err", err)
	}
}

// CloseHiddenRun is closeHiddenRun for meetings and contests.
func (s *Service) CloseHiddenRun(id, notes string) { s.closeHiddenRun(id, notes) }

// HiddenRun is a hidden run that has not been closed yet (no review): its id, what started it and its status.
type HiddenRun struct {
	ID        string
	ParentID  string
	MeetingID string
	Status    Status
}

// OpenHiddenRuns lists the hidden runs of kind (KindMeeting, ...) that have not been closed, so the service that
// starts them can stop the ones whose record does not exist.
func (s *Service) OpenHiddenRuns(ctx context.Context, kind string) ([]HiddenRun, error) {
	if !Hidden(kind) {
		return nil, invalidf("%q is not a hidden kind", kind)
	}
	var out []HiddenRun
	err := s.repo.db.Read(ctx, func(tx *sql.Tx) error {
		out = nil
		rows, err := tx.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks
			WHERE json_extract(data, '$.kind') = ? AND json_extract(data, '$.review') IS NULL`, kind)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				return err
			}
			out = append(out, HiddenRun{ID: t.ID, ParentID: t.ParentID, MeetingID: t.MeetingID, Status: t.Status})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("task: open hidden runs: %w", err)
	}
	return out, nil
}

// StopHiddenRun stops a hidden run whose record was never stored (nothing will read its answer) and closes it once
// it has stopped, so it never waits for the lead's review. A running worker takes a moment to stop, which a
// close right after Cancel would miss.
func (s *Service) StopHiddenRun(id, notes string) {
	if _, _, err := s.Cancel(s.background(), id, api.CancelRequest{}); err != nil && !errors.Is(err, ErrNotFound) {
		s.log.Warn("task: stop a hidden run", "task", id, "err", err)
	}
	s.whenFinished("hidden run", id, func(run *Task) {
		if run != nil {
			s.closeHiddenRun(run.ID, notes)
		}
	})
}

// resumeConsults follows the consult runs an earlier daemon left running.
func (s *Service) resumeConsults(ctx context.Context) error {
	type open struct{ task, consult, run string }
	var opens []open
	err := s.repo.db.Read(ctx, func(tx *sql.Tx) error {
		opens = nil
		rows, err := tx.QueryContext(ctx, `SELECT t.id, json_extract(c.value, '$.id'), json_extract(c.value, '$.runTaskId')
			FROM tasks t, json_each(t.data, '$.consults') c
			WHERE json_extract(c.value, '$.status') = ?`, consultRunning)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var o open
			if err := rows.Scan(&o.task, &o.consult, &o.run); err != nil {
				return err
			}
			opens = append(opens, o)
		}
		return rows.Err()
	})
	if err != nil {
		return fmt.Errorf("task: open consults: %w", err)
	}
	for _, o := range opens {
		s.watchConsult(o.task, o.consult, o.run)
	}
	return nil
}

// ConsultStats counts the consults each member gave and how many of them objected (members' track records).
func (s *Service) ConsultStats(ctx context.Context) (map[string]api.MemberTrack, error) {
	out := map[string]api.MemberTrack{}
	err := s.repo.db.Read(ctx, func(tx *sql.Tx) error {
		clear(out)
		rows, err := tx.QueryContext(ctx, `SELECT json_extract(c.value, '$.member'),
			count(*), sum(json_extract(c.value, '$.verdict') = ?)
			FROM tasks t, json_each(t.data, '$.consults') c
			WHERE json_extract(c.value, '$.status') = ? GROUP BY 1`, api.ConsultObject, consultDone)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var n, objections int
			if err := rows.Scan(&id, &n, &objections); err != nil {
				return err
			}
			out[id] = api.MemberTrack{Member: id, Consults: n, Objections: objections}
		}
		return rows.Err()
	})
	return out, err
}

// consultsReport is the consults part of the lead's report.
func consultsReport(t *Task) []api.ReportConsult {
	open := unansweredObjections(t)
	var out []api.ReportConsult
	for i := range t.Consults {
		c := &t.Consults[i]
		rc := api.ReportConsult{ID: c.ID, Member: c.Member, Rank: c.Rank, Status: c.Status, Verdict: c.Verdict,
			Required: c.Required, Unanswered: slices.Contains(open, c.ID), Error: c.Error}
		for _, f := range c.Findings {
			line := f.Text
			for _, ci := range f.Cites {
				line += " " + answer.CiteText(ci) + " " + answer.CiteTag(ci)
			}
			rc.Findings = append(rc.Findings, line)
		}
		out = append(out, rc)
	}
	return out
}

// unansweredObjections returns the ids of the peer objections on t without an answer.
func unansweredObjections(t *Task) []string {
	var ids []string
	for i := range t.Consults {
		if c := &t.Consults[i]; c.Rank == api.RankPeer && c.Status == consultDone && c.Verdict == api.ConsultObject &&
			c.AnsweredObjection == nil {
			ids = append(ids, c.ID)
		}
	}
	return ids
}
