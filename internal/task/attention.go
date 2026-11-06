package task

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// Attention reasons.
const (
	ReasonDepsBlocked    = "deps_blocked"
	ReasonBackgroundWait = "background_wait"
	ReasonStalled        = "stalled"
	ReasonNoAccounts     = "no_accounts"
	ReasonNoResponse     = "no_response"
	ReasonQuestion       = "question"
	ReasonReview         = "review"
	ReasonFailed         = "failed"
)

// Who needs to act.
const (
	WhoLead = "lead"
	WhoUser = "user"
)

// Stall thresholds that are not settings.
const (
	// noInitAfter flags a worker that has not started its conversation this long after it was launched.
	noInitAfter = 3 * time.Minute
	// attentionTick is how often running tasks are checked for stalls.
	attentionTick = 30 * time.Second
	// attentionSweep is how often finished tasks are checked for attention that has decayed.
	attentionSweep = 10 * time.Minute
	// backgroundWaitDetail explains a background wait to the lead.
	backgroundWaitDetail = "the worker CLI waits for a command the worker started in the background; it is quiet " +
		"but not stuck"
)

// liveState is what the engine knows about a worker it follows right now.
type liveState struct {
	running bool
	// started is when the attempt was launched; sawInit is set once the CLI reported its conversation.
	started time.Time
	sawInit bool
	// lastEvent is when the worker last produced an event.
	lastEvent time.Time
	// tool is the tool call still open (empty when none), command its shell command.
	tool, command string
	toolSince     time.Time
	// waiting is when the CLI started waiting for a background command (zero when it does not).
	waiting time.Time
	// step is the label of what the worker does now.
	step string
	// account and conversation are the account of the attempt and the conversation the CLI reported.
	account, conversation string
}

// computeAttention returns what a task needs from someone, or nil (the rules of the old attention.js, mapped onto
// the states blocked, question, stalled, background_wait and info). It is pure; prev keeps Since of an unchanged
// flag.
func computeAttention(t *Task, live liveState, now time.Time, set config.Settings) *api.Attention {
	att := func(state, reason, who, detail string) *api.Attention {
		since := api.FormatTime(now)
		if p := t.Attention; p != nil && p.State == state && p.Reason == reason && p.Since != "" {
			since = p.Since
		}
		return &api.Attention{State: state, Reason: reason, Who: who, Detail: detail, Since: since}
	}
	if t.Kind != KindNormal && t.Kind != "" {
		return nil
	}
	if d := t.AttentionDismissedAt; !d.IsZero() {
		// Only something new from the worker or a new turn brings a dismissed flag back.
		newer := live.lastEvent.After(d)
		if tu := t.LastTurn(); tu != nil && (tu.StartedAt.After(d) || tu.EndedAt.After(d)) {
			newer = true
		}
		if !newer {
			return nil
		}
	}
	if t.Review != nil || t.Status == StatusCancelled || t.Manual {
		return nil
	}
	if (t.Status == StatusDone || t.Status == StatusFailed) && set.AttentionDecayHours > 0 {
		end := t.UpdatedAt
		if tu := t.LastTurn(); tu != nil && !tu.EndedAt.IsZero() {
			end = tu.EndedAt
		}
		if now.Sub(end) >= time.Duration(set.AttentionDecayHours)*time.Hour {
			return nil
		}
	}
	tu := t.LastTurn()
	switch t.Status {
	case StatusBlockedDeps:
		return att(api.AttentionBlocked, ReasonDepsBlocked, WhoLead, t.DepsReason)
	case StatusRunning:
		if !live.running {
			return nil
		}
		if !live.waiting.IsZero() {
			return att(api.AttentionBackgroundWait, ReasonBackgroundWait, WhoLead, backgroundWaitDetail)
		}
		if !live.sawInit && t.ConversationID == "" && now.Sub(live.started) >= noInitAfter {
			return att(api.AttentionStalled, ReasonStalled, WhoLead,
				fmt.Sprintf("the worker has not started %d min after launch", int(now.Sub(live.started).Minutes())))
		}
		last := live.lastEvent
		if last.IsZero() {
			last = live.started
		}
		idle := now.Sub(last)
		mins := int(idle.Minutes())
		if live.tool != "" {
			if set.StallToolMinutes > 0 && idle >= time.Duration(set.StallToolMinutes)*time.Minute {
				step := live.tool
				if live.command != "" {
					step += fmt.Sprintf(" %q", live.command)
				}
				return att(api.AttentionStalled, ReasonStalled, WhoLead,
					fmt.Sprintf("no output for %d min; last step %s", mins, step))
			}
		} else if set.StallMinutes > 0 && idle >= time.Duration(set.StallMinutes)*time.Minute {
			return att(api.AttentionStalled, ReasonStalled, WhoLead, fmt.Sprintf("no output for %d min", mins))
		}
		return nil
	case StatusWaitingQuota:
		if t.Waiting == string(pool.ReasonNoAccount) {
			return att(api.AttentionBlocked, ReasonNoAccounts, WhoUser, t.WaitDetail)
		}
		return nil
	case StatusDone:
		if tu == nil || strings.TrimSpace(tu.Response) == "" {
			detail := ""
			if tu != nil {
				detail = strings.Join(tu.DeniedActions, ", ")
			}
			return att(api.AttentionBlocked, ReasonNoResponse, WhoLead, detail)
		}
		if q := parseBlocked(tu.Response); q != nil {
			return att(api.AttentionQuestion, ReasonQuestion, WhoLead, q.Text)
		}
		return att(api.AttentionInfo, ReasonReview, WhoLead, "")
	case StatusFailed:
		return att(api.AttentionBlocked, ReasonFailed, WhoLead, t.Error)
	}
	return nil
}

// needsLead reports whether the task waits for the lead (or the user): a blocking flag other than a failure, a
// question, or a finished turn without a verdict.
func needsLead(t *Task) bool {
	if t.Status == StatusWaitingDeps || t.Status == StatusBlockedDeps {
		return false
	}
	if a := t.Attention; a != nil && (a.State == api.AttentionBlocked && a.Reason != ReasonFailed ||
		a.State == api.AttentionQuestion) {
		return true
	}
	return t.Status == StatusDone && t.Review == nil
}

// syncAttention brings the task's attention flag and needsSince up to date and adds an attention event when the
// flag changed. It reports whether anything changed.
func (s *Service) syncAttention(t *Task, evs *[]NewEvent) bool {
	now := s.now()
	next := computeAttention(t, s.live(t.ID), now, s.settings())
	changed := false
	if !sameAttention(t.Attention, next) {
		t.Attention = next
		changed = true
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		var data any = api.AttentionData{State: api.AttentionCleared}
		if next != nil {
			data = *next
		}
		*evs = append(*evs, NewEvent{Type: api.EventAttention, Turn: turn, Data: data})
	}
	since := t.NeedsSince
	switch {
	case !needsLead(t):
		since = time.Time{}
	case since.IsZero():
		since = now
		if a := t.Attention; a != nil && a.State == api.AttentionBlocked {
			if at, err := api.ParseTime(a.Since); err == nil {
				since = at
			}
		} else if tu := t.LastTurn(); tu != nil && !tu.EndedAt.IsZero() {
			since = tu.EndedAt
		}
	}
	if !since.Equal(t.NeedsSince) {
		t.NeedsSince = since
		changed = true
	}
	return changed
}

func sameAttention(a, b *api.Attention) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// watchAttention re-checks running tasks for stalls every 30 s and finished ones for decayed flags every 10 min. It
// only raises and clears flags; it never stops a worker.
func (s *Service) watchAttention() {
	lastSweep := s.clk.Now()
	for {
		t := s.clk.NewTimer(attentionTick)
		select {
		case <-s.root.Done():
			t.Stop()
			return
		case <-t.C():
		}
		s.mu.Lock()
		ids := make([]string, 0, len(s.runs))
		for id := range s.runs {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		if now := s.clk.Now(); now.Sub(lastSweep) >= attentionSweep {
			lastSweep = now
			done, err := s.repo.IDs(s.root, StatusDone, StatusFailed)
			if err != nil {
				s.log.Warn("task: attention sweep", "err", err)
			}
			ids = append(ids, done...)
		}
		for _, id := range ids {
			s.refreshAttention(id)
		}
	}
}

// refreshAttention recomputes one task's attention and saves it when it changed.
func (s *Service) refreshAttention(id string) {
	if _, err := s.mutate(s.root, id, func(*Task) ([]NewEvent, error) { return nil, errNoChange }); err != nil &&
		s.root.Err() == nil {
		s.log.Warn("task: refresh attention", "task", id, "err", err)
	}
}

// blockedHeading finds the "## Blocked" heading of a worker report.
var (
	blockedHeading = regexp.MustCompile(`(?m)^##\s*Blocked\s*$`)
	nextHeading    = regexp.MustCompile(`(?m)^##\s+`)
	listItem       = regexp.MustCompile(`^\s*[-*]\s+(.+)$`)
)

// parseBlocked returns the question of a report's "## Blocked" section and the options listed under it, or nil.
func parseBlocked(text string) *api.ReportQuestion {
	loc := blockedHeading.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	section := text[loc[1]:]
	if m := nextHeading.FindStringIndex(section); m != nil {
		section = section[:m[0]]
	}
	var lines []string
	options := []string{}
	inOptions := false
	for _, line := range strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n") {
		if m := listItem.FindStringSubmatch(line); m != nil {
			inOptions = true
			options = append(options, strings.TrimSpace(m[1]))
		} else if !inOptions {
			lines = append(lines, line)
		}
	}
	q := strings.TrimSpace(strings.Join(lines, "\n"))
	if q == "" && len(options) == 0 {
		return nil
	}
	return &api.ReportQuestion{Text: q, Options: options}
}

// background is a context for engine work that must not end with a request.
func (s *Service) background() context.Context { return context.WithoutCancel(s.root) }
