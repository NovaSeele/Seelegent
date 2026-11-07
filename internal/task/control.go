package task

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/memory"
)

// maxCancelWait caps how long Cancel waits for the worker to stop.
const maxCancelWait = 60

// Review records the lead's verdict (accepted or rejected) on a task whose turn has ended, adds it to the project's
// review history and re-evaluates the tasks that wait for it.
//
// Accepting a task with an unanswered peer objection (a consult) needs req.AnswerObjection: the answer is stored
// on the objection and added to the review notes.
func (s *Service) Review(ctx context.Context, id string, req api.ReviewRequest) (api.TaskSummary, error) {
	return s.ReviewWith(ctx, id, req, ReviewOptions{})
}

// ReviewOptions are the engine's own review switches.
type ReviewOptions struct {
	// SkipHistory leaves the project's review history alone (contest losers: losing is not a defect).
	SkipHistory bool
}

// ReviewWith is Review for the daemon's own features.
func (s *Service) ReviewWith(ctx context.Context, id string, req api.ReviewRequest, o ReviewOptions) (
	api.TaskSummary, error) {
	verdict := strings.TrimSpace(req.Verdict)
	if verdict != api.VerdictAccepted && verdict != api.VerdictRejected {
		return api.TaskSummary{}, invalidf("verdict %q must be accepted or rejected", req.Verdict)
	}
	notes := strings.TrimSpace(req.Notes)
	answerText := strings.TrimSpace(req.AnswerObjection)
	t, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		if Hidden(t.Kind) {
			return nil, ErrNotFound
		}
		if Active(t.Status) || t.Manual || t.Status == StatusWaitingDeps || t.Status == StatusBlockedDeps {
			return nil, badStatef("task %s is %s; review it after its turn ended", id, t.Status)
		}
		notes := notes
		if open := unansweredObjections(t); verdict == api.VerdictAccepted && len(open) > 0 {
			if answerText == "" {
				return nil, &PeerObjectionError{Consults: open}
			}
			at := api.FormatTime(s.now())
			for i := range t.Consults {
				if c := &t.Consults[i]; c.AnsweredObjection == nil && slices.Contains(open, c.ID) {
					c.AnsweredObjection = &api.ObjectionAnswer{Text: answerText, At: at}
				}
			}
			notes = strings.TrimSpace(notes + "\n\nAnswer to peer objection: " + answerText)
		}
		t.Review = &api.Review{Verdict: verdict, Notes: notes, At: api.FormatTime(s.now())}
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		return []NewEvent{{Type: api.EventReview, Turn: turn, Data: api.ReviewData{Verdict: verdict, Notes: notes}}}, nil
	})
	if err != nil {
		return api.TaskSummary{}, err
	}
	if s.o.Memory != nil && !o.SkipHistory {
		if err := s.o.Memory.AddVerdict(ctx, memory.Verdict{Cwd: t.projectDir(), TaskID: t.ID, Title: t.Title,
			Verdict: verdict, Notes: t.Review.Notes}); err != nil {
			s.log.Warn("task: review history", "task", id, "err", err)
		}
	}
	s.checkDeps(s.background())
	return s.Summary(ctx, id)
}

// Cancel stops a task. A task without a running worker is cancelled at once; a running worker is asked to stop
// (its launcher stops the whole process tree, old B1) and Cancel waits up to waitSeconds for it. stopped reports
// whether the task had stopped when Cancel returned. A finished task is left as it is.
func (s *Service) Cancel(ctx context.Context, id string, req api.CancelRequest) (sum api.TaskSummary, stopped bool,
	err error) {
	if req.WaitSeconds < 0 || req.WaitSeconds > maxCancelWait {
		return api.TaskSummary{}, false, invalidf("waitSeconds must be 0..%d", maxCancelWait)
	}
	direct := false
	_, err = s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		direct = false
		if Finished(t.Status) && !t.Manual {
			return nil, errNoChange
		}
		if t.CancelRequested {
			return nil, errNoChange
		}
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		evs := []NewEvent{{Type: api.EventCancel, Turn: turn, Data: api.EmptyData{}}}
		if t.Manual {
			// The console belongs to the user; the task just stops waiting for it.
			t.Manual, t.Console = false, nil
			if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
				evs = append(evs, s.endTurn(t, tu, TurnCancelled, backend.Outcome{}))
			}
			t.Status = StatusCancelled
			direct = true
			return evs, nil
		}
		if t.Status == StatusWaitingDeps || t.Status == StatusBlockedDeps {
			if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
				evs = append(evs, s.endTurn(t, tu, TurnCancelled, backend.Outcome{}))
			}
			t.Status, t.DepsReason = StatusCancelled, ""
			direct = true
			return evs, nil
		}
		t.CancelRequested = true
		return evs, nil
	})
	if err != nil {
		return api.TaskSummary{}, false, err
	}
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	switch {
	case direct:
		s.releaseConsole(id) // a cancelled takeover no longer counts on the account
		s.checkDeps(s.background())
	case r != nil:
		r.requestStop()
		if req.WaitSeconds > 0 {
			t := s.clk.NewTimer(time.Duration(req.WaitSeconds) * time.Second)
			select {
			case <-r.done:
			case <-t.C():
			case <-ctx.Done():
			}
			t.Stop()
		}
	default:
		// No run (it ended in between, or the daemon is recovering): finish the cancel here.
		if t, err := s.repo.Get(ctx, id); err == nil && t.CancelRequested && !s.hasRun(id) {
			if a := lastOpenAttempt(t); a == nil {
				s.finishCancel(id)
			} else {
				s.startRun(id) // a recovered attempt: the run attaches, cancels and follows it
			}
		}
	}
	sum, err = s.Summary(ctx, id)
	if err != nil {
		return api.TaskSummary{}, false, err
	}
	return sum, !Active(sum.Status) && !sum.Manual, nil
}

// DismissAttention hides the task's attention flag until something new happens.
func (s *Service) DismissAttention(ctx context.Context, id string) (api.TaskSummary, error) {
	_, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		if t.Attention == nil {
			return nil, errNoChange
		}
		t.AttentionDismissedAt = s.now()
		return nil, nil
	})
	if err != nil {
		return api.TaskSummary{}, err
	}
	return s.Summary(ctx, id)
}

func (s *Service) hasRun(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id] != nil
}

func lastOpenAttempt(t *Task) *Attempt {
	if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
		if a := tu.LastAttempt(); a != nil && a.Status == AttemptRunning {
			return a
		}
	}
	return nil
}
