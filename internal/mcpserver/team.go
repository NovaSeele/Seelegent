package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// The team tools (milestone 14): members, meeting, consult, notes, contest, and delegate with variants. Their
// results keep the old server's slim shapes (inventory 02 §1.17-1.23); full: true returns the daemon's record.

const (
	// teamWaitMax is the longest single wait the daemon serves on a meeting or contest; longer waits loop.
	teamWaitMax = 1500
	// consultPoll is how often consult looks at its run while it waits.
	consultPoll = time.Second
)

// slimMember is a member in the members tool.
type slimMember struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Backend  string  `json:"backend"`
	Model    *string `json:"model"`
	Tier     *string `json:"tier"`
	Rank     string  `json:"rank"`
	Provider *string `json:"provider"`
	Role     *string `json:"role"`
	Enabled  bool    `json:"enabled"`
	Invalid  string  `json:"invalid,omitempty"`
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Server) members(ctx context.Context, k *call) (result, error) {
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	list, err := c.Members(ctx)
	if err != nil {
		return result{}, err
	}
	out := make([]slimMember, 0, len(list))
	on := 0
	for i := range list {
		m := &list[i]
		out = append(out, slimMember{ID: m.ID, Name: m.Name, Backend: m.Backend, Model: strOrNil(m.Model),
			Tier: strOrNil(m.Tier), Rank: m.Rank, Provider: strOrNil(m.Provider), Role: strOrNil(m.Role),
			Enabled: m.Enabled, Invalid: m.Invalid})
		if m.Enabled && m.Invalid == "" {
			on++
		}
	}
	return result{value: map[string]any{"members": out},
		summary: fmt.Sprintf("%d member%s, %d usable.", len(out), plural(len(out)), on)}, nil
}

// ---- consult ----------------------------------------------------------------------------------------------------

type consultArgs struct {
	TaskID      string `json:"task_id"`
	Member      string `json:"member"`
	Focus       string `json:"focus"`
	WaitSeconds optNum `json:"wait_seconds"`
}

func (s *Server) consult(ctx context.Context, k *call) (result, error) {
	var a consultArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" || a.Member == "" {
		return result{}, errors.New("task_id and member are required")
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	rec, err := c.Consult(ctx, a.TaskID, api.ConsultRequest{Member: a.Member, Focus: a.Focus})
	if err != nil {
		return result{}, err
	}
	wait := time.Duration(clampWait(a.WaitSeconds, defaultWait, maxWait)) * time.Second
	deadline := time.Now().Add(wait)
	for rec.Status == "running" && time.Now().Before(deadline) {
		t := time.NewTimer(consultPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return result{}, ctx.Err()
		case <-t.C:
		}
		d, err := c.Task(ctx, a.TaskID)
		if err != nil {
			return result{}, err
		}
		for i := range d.Consults {
			if d.Consults[i].ID == rec.ID {
				rec = d.Consults[i]
			}
		}
	}
	return result{value: rec, summary: consultSummary(&rec)}, nil
}

func consultSummary(c *api.Consult) string {
	switch c.Status {
	case "running":
		return fmt.Sprintf("Consult %s by %s is still running: call consult's task with task, or wait again later.",
			c.ID, c.Member)
	case "failed":
		return fmt.Sprintf("Consult %s by %s failed: %s", c.ID, c.Member, c.Error)
	}
	msg := fmt.Sprintf("Consult %s by %s (%s): %s, %d finding%s.", c.ID, c.Member, c.Rank, c.Verdict,
		len(c.Findings), plural(len(c.Findings)))
	if c.Verdict == api.ConsultObject && c.Rank == api.RankPeer {
		msg += " A peer objects: review accepted needs answer_objection."
	}
	return msg
}

// ---- notes ------------------------------------------------------------------------------------------------------

type notesArgs struct {
	Action        string  `json:"action"`
	TaskID        string  `json:"task_id"`
	NoteID        string  `json:"note_id"`
	File          string  `json:"file"`
	Line          optNum  `json:"line"`
	EndLine       optNum  `json:"end_line"`
	Side          string  `json:"side"`
	Body          string  `json:"body"`
	Extra         string  `json:"extra"`
	WaitSeconds   optNum  `json:"wait_seconds"`
	WakeOnBlocked optBool `json:"wake_on_blocked"`
}

// slimNote is a note in the notes tool.
type slimNote struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line,omitempty"`
	Side    string `json:"side,omitempty"`
	State   string `json:"state"`
	Status  string `json:"status"`
	By      string `json:"by"`
	Body    string `json:"body"`
	Anchor  string `json:"anchor,omitempty"`
}

func slimNotes(list []api.Note) []slimNote {
	out := make([]slimNote, 0, len(list))
	for i := range list {
		n := &list[i]
		sn := slimNote{ID: n.ID, File: n.File, Line: n.Line, EndLine: n.EndLine, State: n.State, Status: n.Status,
			By: n.By, Body: n.Body, Anchor: n.AnchorText}
		if n.Side == "old" {
			sn.Side = "old"
		}
		if r := []rune(sn.Anchor); len(r) > 200 {
			sn.Anchor = string(r[:200])
		}
		out = append(out, sn)
	}
	return out
}

func (s *Server) notes(ctx context.Context, k *call) (result, error) {
	var a notesArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" {
		return result{}, errors.New("task_id is required")
	}
	switch a.Action {
	case "list", "send":
	case "add":
		if a.File == "" || !a.Line.set || strings.TrimSpace(a.Body) == "" {
			return result{}, errors.New("add needs file, line and body")
		}
	case "resolve", "reopen", "delete":
		if a.NoteID == "" {
			return result{}, fmt.Errorf("note_id is required for %s", a.Action)
		}
	default:
		return result{}, fmt.Errorf("unknown notes action: %s (list, add, resolve, reopen, delete or send)", a.Action)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "list":
		list, err := c.Notes(ctx, a.TaskID)
		if err != nil {
			return result{}, err
		}
		return result{value: slimNotes(list), summary: fmt.Sprintf("%d note%s on task %s.", len(list),
			plural(len(list)), a.TaskID)}, nil
	case "add":
		n, err := c.AddNote(ctx, a.TaskID, api.NoteInput{File: a.File, Line: int(math.Round(a.Line.v)),
			EndLine: int(math.Round(a.EndLine.or(0))), Side: a.Side, Body: a.Body, By: "lead"})
		if err != nil {
			return result{}, err
		}
		return result{value: slimNotes([]api.Note{n})[0], summary: fmt.Sprintf("Note %s added at %s:%d.", n.ID,
			n.File, n.Line)}, nil
	case "resolve", "reopen":
		state := api.NoteResolved
		if a.Action == "reopen" {
			state = api.NoteOpen
		}
		n, err := c.PatchNote(ctx, a.TaskID, a.NoteID, api.NotePatch{State: state})
		if err != nil {
			return result{}, err
		}
		return result{value: slimNotes([]api.Note{n})[0], summary: fmt.Sprintf("Note %s: %s.", n.ID, n.Status)}, nil
	case "delete":
		if err := c.DeleteNote(ctx, a.TaskID, a.NoteID); err != nil {
			return result{}, err
		}
		return result{value: map[string]any{"deleted": true, "note_id": a.NoteID},
			summary: "Note " + a.NoteID + " deleted."}, nil
	}
	sent, err := c.SendNotes(ctx, a.TaskID, api.NotesSendRequest{Extra: a.Extra, By: "lead"})
	if err != nil {
		return result{}, err
	}
	secs := clampWait(a.WaitSeconds, defaultWait, maxWait)
	if secs == 0 {
		return result{value: sent, summary: fmt.Sprintf("%d note%s sent to task %s.", len(sent.Sent),
			plural(len(sent.Sent)), a.TaskID)}, nil
	}
	res, err := s.waitReports(ctx, k, []string{a.TaskID}, secs, false, a.WakeOnBlocked.or(true), "", k.started)
	if err != nil {
		return result{}, fmt.Errorf("the notes were sent, but reading the report failed: %w", err)
	}
	r := reportResult(res)
	return result{value: struct {
		api.TaskReport
		NotesSent []string `json:"notes_sent"`
	}{res.Reports[0], api.NonNil(sent.Sent)}, summary: r.summary}, nil
}

// ---- meeting ----------------------------------------------------------------------------------------------------

type meetingArgs struct {
	Action       string               `json:"action"`
	MeetingID    string               `json:"meeting_id"`
	Question     string               `json:"question"`
	Title        string               `json:"title"`
	Options      strList              `json:"options"`
	Context      strList              `json:"context"`
	Cwd          string               `json:"cwd"`
	Attendees    []api.AttendeeRef    `json:"attendees"`
	Sealed       string               `json:"sealed"`
	MaxRounds    optNum               `json:"max_rounds"`
	EstimateOnly optBool              `json:"estimate_only"`
	WaitSeconds  optNum               `json:"wait_seconds"`
	Answers      []api.QuestionAnswer `json:"answers"`
	Note         string               `json:"note"`
	Decision     string               `json:"decision"`
	Rationale    string               `json:"rationale"`
	Option       string               `json:"option"`
	Items        []api.ActItem        `json:"items"`
	DissentID    string               `json:"dissent_id"`
	Outcome      string               `json:"outcome"`
	Full         optBool              `json:"full"`
}

// slimMeeting is the old server's slim meeting view.
type slimMeeting struct {
	ID               string               `json:"id"`
	Status           string               `json:"status"`
	Round            int                  `json:"round"`
	MaxRounds        int                  `json:"maxRounds"`
	PendingQuestions []slimQuestion       `json:"pendingQuestions,omitempty"`
	Minutes          *string              `json:"minutes"`
	Estimate         *api.RunEstimate     `json:"estimate,omitempty"`
	Error            string               `json:"error,omitempty"`
	Dissents         []api.Dissent        `json:"dissents,omitempty"`
	Decision         *api.MeetingDecision `json:"decision,omitempty"`
}

type slimQuestion struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Question string `json:"question"`
}

func slimMeetingOf(m *api.Meeting) slimMeeting {
	out := slimMeeting{ID: m.ID, Status: m.Status, Round: m.Round, MaxRounds: m.MaxRounds,
		Minutes: strOrNil(m.Minutes), Error: m.Error}
	if m.Status == api.MeetingStatusWaitingLead {
		for _, q := range m.LeadQA {
			if q.AnsweredAt == "" {
				out.PendingQuestions = append(out.PendingQuestions, slimQuestion{ID: q.ID, Label: q.Label,
					Question: q.Question})
			}
		}
	}
	return out
}

func meetingSummary(m *api.Meeting) string {
	msg := fmt.Sprintf("Meeting %s: %s (round %d of %d).", m.ID, m.Status, m.Round, m.MaxRounds)
	switch m.Status {
	case api.MeetingStatusWaitingLead:
		msg += " Answer the pending questions with post."
	case api.MeetingStatusDeciding:
		msg += " Record your decision with decide."
	case api.MeetingStatusClosed:
		msg += " Turn action items into tasks with act."
	case api.MeetingStatusRound, api.MeetingStatusDissent:
		msg += " Call wait."
	}
	return msg
}

func (s *Server) meeting(ctx context.Context, k *call) (result, error) {
	var a meetingArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	switch a.Action {
	case "open", "list":
	case "status", "wait", "post", "decide", "act", "cancel", "dissent_outcome":
		if a.MeetingID == "" {
			return result{}, fmt.Errorf("meeting_id is required for %s", a.Action)
		}
		if a.Action == "dissent_outcome" && a.DissentID == "" {
			return result{}, errors.New("dissent_id is required for dissent_outcome")
		}
	default:
		return result{}, fmt.Errorf("unknown meeting action: %s (open, status, wait, list, post, decide, act, "+
			"cancel or dissent_outcome)", a.Action)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	var m api.Meeting
	switch a.Action {
	case "list":
		list, err := c.Meetings(ctx, 0)
		if err != nil {
			return result{}, err
		}
		return result{value: api.NonNil(list), summary: fmt.Sprintf("%d meeting%s.", len(list), plural(len(list)))},
			nil
	case "open":
		cwd, err := s.absPath(a.Cwd)
		if err != nil {
			return result{}, err
		}
		req := api.MeetingRequest{Question: a.Question, Title: a.Title, Options: a.Options, Context: a.Context,
			Cwd: cwd, Attendees: a.Attendees, Sealed: a.Sealed, MaxRounds: int(math.Round(a.MaxRounds.or(0)))}
		if a.EstimateOnly.or(false) {
			est, err := c.EstimateMeeting(ctx, req)
			if err != nil {
				return result{}, err
			}
			return result{value: map[string]any{"estimate": est},
				summary: fmt.Sprintf("The meeting takes at most %d worker run%s.", est.Runs, plural(est.Runs))}, nil
		}
		if m, err = c.OpenMeeting(ctx, req); err != nil {
			return result{}, err
		}
		k.announce(ctx, "Seelegent opened meeting "+m.ID+".", map[string]any{"meeting_id": m.ID})
		if secs := clampWait(a.WaitSeconds, 0, teamWaitMax); secs > 0 {
			if m, err = s.waitMeeting(ctx, k, m.ID, secs); err != nil {
				return result{}, err
			}
		}
	case "status":
		m, err = c.Meeting(ctx, a.MeetingID)
	case "wait":
		m, err = s.waitMeeting(ctx, k, a.MeetingID, clampWait(a.WaitSeconds, defaultWait, teamWaitMax))
	case "post":
		m, err = c.PostMeeting(ctx, a.MeetingID, api.MeetingPostRequest{Answers: a.Answers, Note: a.Note})
	case "decide":
		m, err = c.DecideMeeting(ctx, a.MeetingID, api.MeetingDecideRequest{Decision: a.Decision,
			Rationale: a.Rationale, Option: a.Option})
	case "cancel":
		m, err = c.CancelMeeting(ctx, a.MeetingID)
	case "dissent_outcome":
		m, err = c.DissentOutcome(ctx, a.MeetingID, a.DissentID, a.Outcome)
	case "act":
		for i := range a.Items {
			if a.Items[i].Cwd, err = s.absPath(a.Items[i].Cwd); err != nil {
				return result{}, err
			}
		}
		res, err := c.ActMeeting(ctx, a.MeetingID, api.MeetingActRequest{Items: a.Items})
		if err != nil {
			return result{}, err
		}
		sum := fmt.Sprintf("%d task%s delegated from meeting %s: %s.", len(res.Tasks), plural(len(res.Tasks)),
			a.MeetingID, strings.Join(res.Tasks, ", "))
		if a.Full.or(false) {
			return result{value: res, summary: sum}, nil
		}
		return result{value: map[string]any{"meeting_id": a.MeetingID, "tasks": api.NonNil(res.Tasks)},
			summary: sum}, nil
	}
	if err != nil {
		return result{}, err
	}
	if a.Full.or(false) {
		return result{value: m, summary: meetingSummary(&m)}, nil
	}
	v := slimMeetingOf(&m)
	if a.Action == "open" {
		v.Estimate = m.Estimate
	}
	return result{value: v, summary: meetingSummary(&m)}, nil
}

// ---- contest ----------------------------------------------------------------------------------------------------

type contestArgs struct {
	Action      string  `json:"action"`
	ContestID   string  `json:"contest_id"`
	Winner      string  `json:"winner"`
	WaitSeconds optNum  `json:"wait_seconds"`
	Full        optBool `json:"full"`
}

func (s *Server) contest(ctx context.Context, k *call) (result, error) {
	var a contestArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	switch a.Action {
	case "status":
	case "wait", "pick", "cancel":
		if a.ContestID == "" {
			return result{}, fmt.Errorf("contest_id is required for %s", a.Action)
		}
	default:
		return result{}, fmt.Errorf("unknown contest action: %s (status, wait, pick or cancel)", a.Action)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	var v api.Contest
	switch a.Action {
	case "status":
		if a.ContestID == "" {
			list, err := c.Contests(ctx, 0)
			if err != nil {
				return result{}, err
			}
			return result{value: api.NonNil(list), summary: fmt.Sprintf("%d contest%s.", len(list),
				plural(len(list)))}, nil
		}
		v, err = c.Contest(ctx, a.ContestID)
	case "wait":
		v, err = s.waitContest(ctx, k, a.ContestID, clampWait(a.WaitSeconds, defaultWait, maxWait))
	case "pick":
		v, err = c.PickContest(ctx, a.ContestID, api.ContestPickRequest{Winner: a.Winner, By: "lead"})
	case "cancel":
		v, err = c.CancelContest(ctx, a.ContestID)
	}
	if err != nil {
		return result{}, err
	}
	return contestResult(&v, a.Full.or(false)), nil
}

// waitMeeting waits up to seconds (at most teamWaitMax) for a meeting. A daemon that stops during the wait is
// handled as in task waits (ops.WaitDaemon): looked for, never started again, and the wait ends with
// daemon_stopped; a crash is ridden out.
func (s *Server) waitMeeting(ctx context.Context, k *call, id string, seconds int) (api.Meeting, error) {
	return ops.WaitDaemon(ctx, s.connector(k), s.waitOpts(nil, seconds, false, false, ""),
		func(ctx context.Context, c *client.Client, deadline time.Time) (api.Meeting, error) {
			return c.WaitMeeting(ctx, id, max(0, min(int(time.Until(deadline).Seconds()), teamWaitMax)))
		})
}

// waitContest waits up to seconds for a contest, in waits the daemon serves, with the stop handling of
// waitMeeting.
func (s *Server) waitContest(ctx context.Context, k *call, id string, seconds int) (api.Contest, error) {
	return ops.WaitDaemon(ctx, s.connector(k), s.waitOpts(nil, seconds, false, false, ""),
		func(ctx context.Context, c *client.Client, deadline time.Time) (api.Contest, error) {
			for {
				left := int(time.Until(deadline).Seconds())
				v, err := c.WaitContest(ctx, id, max(0, min(left, teamWaitMax)))
				if err != nil || left <= teamWaitMax || contestSettled(v.Status) {
					return v, err
				}
			}
		})
}

func contestSettled(status string) bool {
	switch status {
	case api.ContestStatusReady, api.ContestStatusApplied, api.ContestStatusCancelled, api.ContestStatusFailed:
		return true
	}
	return false
}

func contestResult(v *api.Contest, full bool) result {
	sum := fmt.Sprintf("Contest %s: %s, %d variant%s.", v.ID, v.Status, len(v.Variants), plural(len(v.Variants)))
	switch {
	case v.Status == api.ContestStatusReady && v.Proposal != nil:
		sum += fmt.Sprintf(" Proposed winner %s (%s).", v.Proposal.Winner, v.Proposal.Source)
	case v.Status == api.ContestStatusApplied:
		sum += " Winner " + v.Winner + " applied."
	}
	if full {
		return result{value: v, summary: sum}
	}
	return result{value: slimContest(v), summary: sum}
}

// slimContest is the old server's slim contest view.
func slimContest(v *api.Contest) map[string]any {
	out := map[string]any{"contest_id": v.ID, "status": v.Status, "title": v.Title}
	if v.Estimate != nil {
		est := map[string]any{"requested": v.Estimate.Requested, "granted": v.Estimate.Granted}
		if len(v.Estimate.Dropped) > 0 {
			est["dropped"] = v.Estimate.Dropped
		}
		out["estimate"] = est
	}
	rows := map[string]*api.ScoreRow{}
	for i := range v.Scoreboard {
		rows[v.Scoreboard[i].Label] = &v.Scoreboard[i]
	}
	variants := make([]map[string]any, 0, len(v.Variants))
	for i := range v.Variants {
		vr := &v.Variants[i]
		e := map[string]any{"label": vr.Label, "task_id": vr.TaskID, "status": vr.Status, "backend": vr.Backend,
			"model": vr.Model}
		if vr.Member != "" {
			e["member"] = vr.Member
		}
		if r := rows[vr.Label]; r != nil {
			e["gate"], e["auto_fix_turns"], e["files"] = r.Gate, r.AutoFixTurns, r.Files
			e["added"], e["removed"], e["out_of_lane"], e["tokens"] = r.Added, r.Removed, r.OutOfLane, r.Tokens
			if r.DurationMs != nil {
				e["duration_s"] = math.Round(float64(*r.DurationMs) / 1000)
			}
			if r.Verifier != "" {
				e["verifier"] = r.Verifier
			}
			if r.Disqualified != "" {
				e["disqualified"] = r.Disqualified
			}
		}
		variants = append(variants, e)
	}
	out["variants"] = variants
	if len(v.MachineRanking) > 0 {
		out["machine_ranking"] = v.MachineRanking
	}
	if j := v.JudgeRun; j != nil {
		jv := map[string]any{"status": j.Status, "member": j.Member, "winner": j.WinnerLabel,
			"ranking": j.RankingLabels, "verified_citations": j.Verified}
		var reasons []map[string]any
		for i := range j.Reasons {
			if i >= 10 {
				break
			}
			cites := make([]string, 0, len(j.Reasons[i].Cites))
			for _, ct := range j.Reasons[i].Cites {
				loc := fmt.Sprintf("%s:%d", ct.Path, ct.Line)
				if ct.EndLine > ct.Line {
					loc += fmt.Sprintf("-%d", ct.EndLine)
				}
				cites = append(cites, strings.TrimSpace(ct.Variant+" "+loc+" "+ct.State))
			}
			reasons = append(reasons, map[string]any{"text": j.Reasons[i].Text, "cites": cites})
		}
		jv["reasons"] = api.NonNil(reasons)
		if j.Error != "" {
			jv["error"] = j.Error
		}
		out["judge"] = jv
	}
	if v.Proposal != nil {
		out["proposal"] = v.Proposal
	}
	if v.Winner != "" {
		out["winner"] = v.Winner
	}
	if v.Applied != nil {
		out["applied"] = map[string]any{"files": v.Applied.Files, "at": v.Applied.At}
	}
	if v.LastPickError != nil {
		out["last_pick_error"] = v.LastPickError
	}
	if v.DeadlineAt != "" && (v.Status == api.ContestStatusRunning || v.Status == api.ContestStatusJudging) {
		out["deadline_at"] = v.DeadlineAt
	}
	if len(v.Warnings) > 0 {
		out["warnings"] = v.Warnings
	}
	if v.Error != "" {
		out["error"] = v.Error
	}
	if v.Status == api.ContestStatusReady {
		out["next"] = "Read the proposed winner (task_id), then contest pick (or followup a variant first)."
	}
	return out
}

// judgeArg is delegate's judge: a member id, or false for machine scoring only.
type judgeArg struct {
	member string
	off    bool
}

func (j *judgeArg) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch s {
	case "null", `""`:
		*j = judgeArg{}
		return nil
	case "false", `"false"`:
		*j = judgeArg{off: true}
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return errors.New("judge is a member id or false")
	}
	*j = judgeArg{member: strings.TrimSpace(str)}
	return nil
}

// startContest is delegate with variants: POST /contests, then an optional wait.
func (s *Server) startContest(ctx context.Context, k *call, a *delegateArgs, req api.DelegateRequest) (result,
	error) {
	if len(a.After) > 0 {
		return result{}, errors.New("a contest cannot wait for other tasks (after); delegate without variants")
	}
	creq := api.ContestRequest{DelegateRequest: req, DeadlineMinutes: int(math.Round(a.DeadlineMinutes.or(0))),
		Judge: a.Judge.member, NoJudge: a.Judge.off}
	if a.Variants.set {
		creq.Variants = int(math.Round(a.Variants.v))
	}
	if given(a.VariantSpecs) {
		if err := decodeArgs(a.VariantSpecs, &creq.VariantSpecs); err != nil {
			return result{}, fmt.Errorf("variant_specs: %w", err)
		}
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	v, err := c.CreateContest(ctx, creq)
	if err != nil {
		return result{}, err
	}
	id := v.ID
	k.announce(ctx, "Seelegent started contest "+id+" with "+fmt.Sprint(len(v.Variants))+" variants.",
		map[string]any{"contest_id": id})
	if secs := clampWait(a.WaitSeconds, defaultWait, maxWait); secs > 0 {
		if v, err = s.waitContest(ctx, k, id, secs); err != nil {
			return result{}, fmt.Errorf("contest %s was created, but waiting for it failed: %w", id, err)
		}
	}
	return contestResult(&v, false), nil
}
