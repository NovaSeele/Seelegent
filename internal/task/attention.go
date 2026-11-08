package task

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
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
	ReasonAuth           = "auth"
	ReasonLooping        = "looping"
	ReasonWorkAtRisk     = "work_at_risk"
	ReasonHygiene        = "hygiene"
	// ReasonGateRed: the gate is still red (or error) after the automatic fix turns.
	ReasonGateRed = "gate_red"
	// ReasonVerifierRejected: the verifier rejected the work and no fix turn is left.
	ReasonVerifierRejected = "verifier_rejected"
	// ReasonPeerObjection: a peer member objected in a consult; the lead answers it before accepting.
	ReasonPeerObjection = "peer_objection"
)

// objectionDetail names the peers who object and the first change each requires (or finding).
func objectionDetail(t *Task, open []string) string {
	var parts []string
	for i := range t.Consults {
		c := &t.Consults[i]
		if !slices.Contains(open, c.ID) {
			continue
		}
		p := c.Member + " objects"
		first := ""
		if len(c.Required) > 0 {
			first = c.Required[0]
		} else if len(c.Findings) > 0 {
			first = c.Findings[0].Text
		}
		if first = strings.TrimSpace(first); first != "" {
			p += ": " + first
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}

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
	// steps are the worker's last finished tool calls (at most loopWindow), for the loop check.
	steps []loopStep
	// pending is the key of the tool call still open.
	pending string
	// edited are the files the worker's edit tools changed so far in this attempt (as reported).
	edited []string
}

// computeAttention returns what a task needs from someone, or nil (the rules of the old attention.js, mapped onto
// the states blocked, question, stalled, background_wait and info). It is pure; prev keeps Since of an unchanged
// flag. acc is what is known about the accounts of the task's backend.
func computeAttention(t *Task, live liveState, acc accountHealth, now time.Time, set config.Settings) *api.Attention {
	att := func(state, reason, who, detail string, params ...*api.AttentionParams) *api.Attention {
		since := api.FormatTime(now)
		if p := t.Attention; p != nil && p.State == state && p.Reason == reason && p.Since != "" {
			since = p.Since
		}
		a := &api.Attention{State: state, Reason: reason, Who: who, Detail: detail, Since: since}
		if len(params) > 0 {
			a.Params = params[0]
		}
		return a
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
	noAccounts := func(detail string) *api.Attention {
		return att(api.AttentionBlocked, ReasonNoAccounts, WhoUser, detail, &api.AttentionParams{Backend: t.Backend})
	}
	tu := t.LastTurn()
	switch t.Status {
	case StatusBlockedDeps:
		return att(api.AttentionBlocked, ReasonDepsBlocked, WhoLead, t.DepsReason, t.DepsBlock)
	case StatusRunning:
		if !live.running {
			return nil
		}
		if !live.waiting.IsZero() {
			return att(api.AttentionBackgroundWait, ReasonBackgroundWait, WhoLead, backgroundWaitDetail)
		}
		if !live.sawInit && t.ConversationID == "" && now.Sub(live.started) >= noInitAfter {
			mins := int(now.Sub(live.started).Minutes())
			return att(api.AttentionStalled, ReasonStalled, WhoLead,
				fmt.Sprintf("the worker has not started %d min after launch", mins),
				&api.AttentionParams{Kind: api.AttentionNotStarted, Minutes: mins})
		}
		if loop, detail, params := detectLoop(live.steps); loop {
			return att(api.AttentionBlocked, ReasonLooping, WhoLead, detail, params)
		}
		if acc.known && acc.total > 0 && acc.usable == 0 {
			return noAccounts(noAccountDetail(t.Backend))
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
					fmt.Sprintf("no output for %d min; last step %s", mins, step),
					&api.AttentionParams{Kind: api.AttentionToolQuiet, Minutes: mins, Tool: live.tool,
						Command: live.command})
			}
		} else if set.StallMinutes > 0 && idle >= time.Duration(set.StallMinutes)*time.Minute {
			return att(api.AttentionStalled, ReasonStalled, WhoLead, fmt.Sprintf("no output for %d min", mins),
				&api.AttentionParams{Kind: api.AttentionNoOutput, Minutes: mins})
		}
		return nil
	case StatusQueued:
		if acc.known && acc.usable == 0 {
			return noAccounts(noAccountDetail(t.Backend))
		}
		return nil
	case StatusWaitingQuota:
		if acc.known && acc.enabled > 0 && acc.needsLogin == acc.enabled {
			return att(api.AttentionBlocked, ReasonAuth, WhoUser,
				fmt.Sprintf("every enabled %s account needs a sign-in", t.Backend),
				&api.AttentionParams{Backend: t.Backend})
		}
		if acc.known && acc.usable == 0 {
			return noAccounts(noAccountDetail(t.Backend))
		}
		if t.Waiting == string(pool.ReasonNoAccount) {
			return noAccounts(t.WaitDetail)
		}
		return nil
	case StatusDone:
		if tu == nil || strings.TrimSpace(tu.Response) == "" {
			detail := ""
			var params *api.AttentionParams
			if tu != nil && len(tu.DeniedActions) > 0 {
				detail = strings.Join(tu.DeniedActions, ", ")
				params = &api.AttentionParams{Denied: slices.Clone(tu.DeniedActions)}
			}
			return att(api.AttentionBlocked, ReasonNoResponse, WhoLead, detail, params)
		}
		if q := parseBlocked(tu.Response); q != nil {
			return att(api.AttentionQuestion, ReasonQuestion, WhoLead, q.Text)
		}
		if g := tu.Gate; g != nil && (g.State == api.GateRed || g.State == api.GateError) && !hygieneOnly(g) {
			return att(api.AttentionBlocked, ReasonGateRed, WhoLead, gateDetail(g))
		}
		if v := tu.Verifier; v != nil && v.Verdict == api.VerdictReject {
			detail := ""
			if len(v.Reasons) > 0 {
				detail = v.Reasons[0]
			}
			return att(api.AttentionBlocked, ReasonVerifierRejected, WhoLead, detail)
		}
		if open := unansweredObjections(t); len(open) > 0 {
			return att(api.AttentionBlocked, ReasonPeerObjection, WhoLead, objectionDetail(t, open))
		}
		red := redFindings(tu.Hygiene)
		if g := tu.Gate; g != nil && hygieneOnly(g) {
			// The gate carries the findings of earlier turns that are still open.
			for i := range g.Steps {
				s := &g.Steps[i]
				if s.Kind == api.StepHygiene {
					red = max(red, len(s.Files))
				}
			}
		}
		if red > 0 {
			return att(api.AttentionBlocked, ReasonHygiene, WhoLead, fmt.Sprintf("%d hygiene finding(s)", red),
				&api.AttentionParams{Count: red})
		}
		if n := atRisk(t); n > 0 {
			return att(api.AttentionBlocked, ReasonWorkAtRisk, WhoLead, fmt.Sprintf("%d file(s) at risk", n),
				&api.AttentionParams{Count: n})
		}
		return att(api.AttentionInfo, ReasonReview, WhoLead, "")
	case StatusFailed:
		return att(api.AttentionBlocked, ReasonFailed, WhoLead, t.Error, failedParams(t.Error))
	}
	return nil
}

// failedParams are the params of a failed flag whose error says how the worker process ended (backend.exitText):
// its kind, backend and exit code, which the dashboard words in the reader's language. nil for any other error,
// which the dashboard shows as the daemon said it.
func failedParams(msg string) *api.AttentionParams {
	f, ok := backend.ReadExitText(msg)
	if !ok {
		return nil
	}
	p := &api.AttentionParams{Kind: f.Kind, Backend: f.Backend}
	if f.Kind == backend.ExitError || f.Kind == backend.ExitNoResult {
		code := f.Code
		p.ExitCode = &code
	}
	return p
}

// gateDetail says in plain words which steps of a gate failed ("command failed: npm test; syntax check failed:
// a.json"), and which other tasks a foreign failure may be caused by. The dashboard builds its own text from the
// turn's steps; this one is for the lead (MCP, CLI).
func gateDetail(g *api.GateResult) string {
	var parts []string
	for i := range g.Steps {
		s := &g.Steps[i]
		if s.State != api.StepRed && s.State != api.StepError {
			continue
		}
		part := stepFailure(s)
		if s.Foreign {
			causes := make([]string, 0, len(s.CausedBy))
			for _, c := range s.CausedBy {
				causes = append(causes, causeName(c))
			}
			part += " (may be caused by " + strings.Join(causes, ", ") + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// causeName names another task for the lead: "t2 (API rename)", the same form as the fix prompt.
func causeName(c api.GateCause) string {
	if c.Title == "" {
		return c.TaskID
	}
	return c.TaskID + " (" + c.Title + ")"
}

// stepFailure is one failing step in plain words.
func stepFailure(s *api.GateStep) string {
	switch s.Kind {
	case api.StepLane:
		return withList("files outside the lane", s.Files)
	case api.StepHygiene:
		files := make([]string, 0, len(s.Findings))
		for _, f := range s.Findings {
			files = append(files, f.Path)
		}
		return withList("hygiene findings", files)
	case api.StepAuto:
		files := make([]string, 0, len(s.Failures))
		for _, f := range s.Failures {
			files = append(files, f.File)
		}
		return withList("syntax check failed", files)
	case api.StepDoctrine:
		if s.Cmd == "" {
			return "doctrine file refused: " + s.Label
		}
		return "doctrine check " + s.Label + " failed: " + s.Cmd
	default:
		cmd := s.Cmd
		if s.Dir != "" {
			cmd = "cd " + s.Dir + " && " + cmd
		}
		if s.State == api.StepError {
			return "command could not run: " + cmd
		}
		return "command failed: " + cmd
	}
}

// withList puts at most three names after head, then says how many more there are.
func withList(head string, names []string) string {
	const most = 3
	switch {
	case len(names) == 0:
		return head
	case len(names) <= most:
		return head + ": " + strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s: %s and %d more", head, strings.Join(names[:most], ", "), len(names)-most)
}

// hygieneOnly reports whether the hygiene step is the gate's only failure (the hygiene flag says more).
func hygieneOnly(g *api.GateResult) bool {
	found := false
	for i := range g.Steps {
		s := &g.Steps[i]
		if s.State == api.StepRed || s.State == api.StepError {
			if s.Kind != api.StepHygiene {
				return false
			}
			found = true
		}
	}
	return found
}

// noAccountDetail explains a no_accounts flag.
func noAccountDetail(backendName string) string {
	return fmt.Sprintf("no %s account can run work: sign one in or enable one", backendName)
}

// redFindings counts the red findings of a check.
func redFindings(fs []api.Finding) int {
	n := 0
	for _, f := range fs {
		if f.Severity == api.SeverityRed {
			n++
		}
	}
	return n
}

// atRisk counts the files at risk of every turn that were not restored.
func atRisk(t *Task) int {
	n := 0
	for _, tu := range t.Turns {
		for _, r := range tu.WorkAtRisk {
			if !r.Restored {
				n++
			}
		}
	}
	return n
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
	next := computeAttention(t, s.live(t.ID), s.healthOf(t.Backend), now, s.settings())
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
	return a.State == b.State && a.Reason == b.Reason && a.Who == b.Who && a.Detail == b.Detail &&
		a.Since == b.Since && sameParams(a.Params, b.Params)
}

func sameParams(a, b *api.AttentionParams) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Minutes == b.Minutes && a.Count == b.Count && a.Steps == b.Steps &&
		a.Tool == b.Tool && a.Command == b.Command && a.Backend == b.Backend && a.Task == b.Task &&
		a.Title == b.Title && slices.Equal(a.Denied, b.Denied) &&
		(a.ExitCode == nil) == (b.ExitCode == nil) && (a.ExitCode == nil || *a.ExitCode == *b.ExitCode)
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
		s.refreshHealth(s.root)
		if now := s.clk.Now(); now.Sub(lastSweep) >= attentionSweep {
			lastSweep = now
			// Only a raised flag can decay on a finished task; the others need no look.
			done, err := s.repo.AttentionIDs(s.root, StatusDone, StatusFailed)
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
