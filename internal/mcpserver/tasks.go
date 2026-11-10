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

// Wait limits of the old server (seconds).
const (
	defaultWait   = 600
	maxWait       = 3000
	defaultCancel = 15
	maxCancel     = 60
)

// handlers maps tool names to their functions (team.go has the milestone 14 tools, github.go the milestone 15
// ones).
func (s *Server) handlers() map[string]toolFunc {
	return map[string]toolFunc{
		"delegate": s.delegate,
		"followup": s.followup,
		"wait":     s.wait,
		"review":   s.review,
		"cancel":   s.cancel,
		"task":     s.task,
		"status":   s.status,
		"memory":   s.memory,
		"profiles": s.profiles,
		"proof":    s.proof,
		"members":  s.members,
		"consult":  s.consult,
		"notes":    s.notes,
		"meeting":  s.meeting,
		"contest":  s.contest,
		// github.go: milestone 15.
		"issues":      s.issues,
		"automations": s.automations,
	}
}

// delegateArgs are the arguments of delegate.
type delegateArgs struct {
	Prompt          string          `json:"prompt"`
	Cwd             string          `json:"cwd"`
	Title           string          `json:"title"`
	Member          string          `json:"member"`
	Backend         string          `json:"backend"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	Effort          string          `json:"effort"`
	Mode            string          `json:"mode"`
	Profile         string          `json:"profile"`
	Spec            string          `json:"spec"`
	TaskIDs         strList         `json:"task_ids"`
	Gate            gateArg         `json:"gate"`
	Lane            laneArg         `json:"lane"`
	ForceLane       optBool         `json:"force_lane"`
	AllowGit        optBool         `json:"allow_git"`
	Verify          optBool         `json:"verify"`
	After           strList         `json:"after"`
	AfterMode       string          `json:"after_mode"`
	Variants        optNum          `json:"variants"`
	VariantSpecs    json.RawMessage `json:"variant_specs"`
	WaitSeconds     optNum          `json:"wait_seconds"`
	WakeOnBlocked   optBool         `json:"wake_on_blocked"`
	DeadlineMinutes optNum          `json:"deadline_minutes"`
	Judge           judgeArg        `json:"judge"`
}

// given reports whether a raw argument was passed with a value.
func given(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func (s *Server) delegate(ctx context.Context, k *call) (result, error) {
	var a delegateArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return result{}, errors.New("prompt is required")
	}
	if strings.TrimSpace(a.Cwd) == "" {
		return result{}, errors.New("cwd is required (the absolute path of the project folder)")
	}
	cwd, err := s.absPath(a.Cwd)
	if err != nil {
		return result{}, err
	}
	mode, err := ops.TaskMode(a.Mode)
	if err != nil {
		return result{}, err
	}
	req := api.DelegateRequest{Prompt: a.Prompt, Title: a.Title, Cwd: cwd, Mode: mode, Model: a.Model, Member: a.Member,
		Effort: a.Effort, Backend: a.Backend, Provider: a.Provider, Profile: a.Profile, After: a.After,
		AfterMode: a.AfterMode, AllowGit: a.AllowGit.or(false), Session: s.session(), Verify: a.Verify.ptr(),
		ForceLane: a.ForceLane.or(false)}
	req.Gate, req.GateCommands = a.Gate.request()
	req.Lane, req.NoLane = a.Lane.paths, a.Lane.off
	switch {
	case a.Spec != "":
		req.Spec = &api.SpecRequest{Dir: a.Spec, TaskIDs: a.TaskIDs}
	case len(a.TaskIDs) > 0:
		return result{}, errors.New("task_ids needs spec (the Spec Kit feature folder they come from)")
	}
	if a.Variants.set || given(a.VariantSpecs) {
		return s.startContest(ctx, k, &a, req)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	sum, err := c.Delegate(ctx, req)
	if err != nil {
		return result{}, err
	}
	k.announce(ctx, "Seelegent created task "+sum.ID+"; waiting for its report.",
		map[string]any{"task_id": sum.ID, "status": sum.Status})
	// A dependent task usually waits for others: do not block on it unless asked.
	def := defaultWait
	if len(a.After) > 0 {
		def = 0
	}
	res, err := s.waitReports(ctx, k, []string{sum.ID}, clampWait(a.WaitSeconds, def, maxWait), false,
		a.WakeOnBlocked.or(true), cwd, k.started)
	if err != nil {
		return result{}, fmt.Errorf("task %s was created, but reading its report failed: %w", sum.ID, err)
	}
	return reportResult(res), nil
}

// session is the lead session that delegates, from the host's environment (as the old server read it), with the
// lead host the worker brief names (old F9).
func (s *Server) session() *api.Session { return ops.MCPSession(s.o.Getenv) }

// followupArgs are the arguments of followup.
type followupArgs struct {
	TaskID        string  `json:"task_id"`
	Message       string  `json:"message"`
	Profile       string  `json:"profile"`
	Model         string  `json:"model"`
	Effort        string  `json:"effort"`
	TaskIDs       strList `json:"task_ids"`
	Gate          gateArg `json:"gate"`
	Lane          laneArg `json:"lane"`
	ForceLane     optBool `json:"force_lane"`
	AllowGit      optBool `json:"allow_git"`
	Verify        optBool `json:"verify"`
	WaitSeconds   optNum  `json:"wait_seconds"`
	WakeOnBlocked optBool `json:"wake_on_blocked"`
	Provider      *string `json:"provider"`
	Force         optBool `json:"force"`
	Member        string  `json:"member"`
}

func (s *Server) followup(ctx context.Context, k *call) (result, error) {
	var a followupArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" {
		return result{}, errors.New("task_id is required")
	}
	if strings.TrimSpace(a.Message) == "" && !a.Force.or(false) {
		return result{}, errors.New("message is required (it may be left out only with force: true)")
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	if a.Provider != nil {
		d, err := c.Task(ctx, a.TaskID)
		if err != nil {
			return result{}, err
		}
		if *a.Provider != d.Provider {
			have := d.Provider
			if have == "" {
				have = "none"
			}
			return result{}, fmt.Errorf("cannot change provider in followup (task has provider %q, requested %q)",
				have, *a.Provider)
		}
	}
	req := api.FollowupRequest{Message: a.Message, Model: a.Model, Effort: a.Effort, TaskIDs: a.TaskIDs,
		Profile: a.Profile, AllowGit: a.AllowGit.ptr(), Force: a.Force.or(false), Verify: a.Verify.ptr(),
		ForceLane: a.ForceLane.or(false), Member: a.Member}
	req.Gate, req.GateCommands = a.Gate.followup()
	req.Lane, req.NoLane = a.Lane.paths, a.Lane.off
	if _, err := c.Followup(ctx, a.TaskID, req); err != nil {
		return result{}, err
	}
	res, err := s.waitReports(ctx, k, []string{a.TaskID}, clampWait(a.WaitSeconds, defaultWait, maxWait), false,
		a.WakeOnBlocked.or(true), "", k.started)
	if err != nil {
		return result{}, fmt.Errorf("the followup was sent, but reading the report failed: %w", err)
	}
	return reportResult(res), nil
}

// waitArgs are the arguments of wait.
type waitArgs struct {
	TaskIDs       strList `json:"task_ids"`
	WaitSeconds   optNum  `json:"wait_seconds"`
	Any           optBool `json:"any"`
	WakeOnBlocked optBool `json:"wake_on_blocked"`
}

func (s *Server) wait(ctx context.Context, k *call) (result, error) {
	var a waitArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if len(a.TaskIDs) == 0 {
		return result{}, errors.New("task_ids is required (one or more task ids)")
	}
	res, err := s.waitReports(ctx, k, a.TaskIDs, clampWait(a.WaitSeconds, defaultWait, maxWait), a.Any.or(false),
		a.WakeOnBlocked.or(true), "", time.Time{})
	if err != nil {
		return result{}, err
	}
	if len(a.TaskIDs) == 1 {
		return reportResult(res), nil
	}
	return result{value: api.NonNil(res.Reports), summary: ops.WaitSummary(res)}, nil
}

// waitReports waits for ids (ops.WaitReports over the call's client): reports relative to cwd when given, else to
// each task's folder, and a daemon that goes away during the wait is found or started again within the wait (found
// only, never started, after it said it was stopping).
//
// since (zero: none) is k.started when the call created or followed up the tasks: a flag raised since then wakes the
// wait although it was there when the wait began (a task blocked as soon as it is created, final verification N2).
func (s *Server) waitReports(ctx context.Context, k *call, ids []string, seconds int, anyDone, wake bool,
	cwd string, since time.Time) (api.WaitResult, error) {
	o := s.waitOpts(ids, seconds, anyDone, wake, cwd)
	o.Since = since
	return ops.WaitReports(ctx, s.connector(k), o)
}

// connector is the daemon lookup of the call's waits (ops.Connect): it reuses the call's client, finds the daemon
// again without starting it (after it said it was stopping), or finds or starts it.
func (s *Server) connector(k *call) ops.Connect {
	return func(ctx context.Context, how ops.Lookup) (*client.Client, error) {
		switch how {
		case ops.Reuse:
		case ops.FindOnly:
			c, err := s.o.Daemon.Find(ctx)
			if err != nil {
				return nil, err
			}
			k.c = c
		default:
			k.c = nil
		}
		return k.client(ctx)
	}
}

// waitOpts are the options of a wait of seconds (the ids and flags only matter to task waits).
func (s *Server) waitOpts(ids []string, seconds int, anyDone, wake bool, cwd string) ops.Wait {
	return ops.Wait{IDs: ids, Seconds: seconds, Any: anyDone, WakeOnBlocked: wake, Cwd: cwd,
		RetryPause: s.o.RetryPause, StopGrace: s.o.StopGrace, Logger: s.o.Logger.With("from", "mcp")}
}

// reportResult is the result of a call that returns one task's report.
func reportResult(res api.WaitResult) result {
	r := res.Reports[0]
	sum := ops.ReportSummary(r)
	if res.Woke == api.WokeTimeout && ops.Pending(r.Status) {
		sum += " The wait ended before the task did: call wait again."
	}
	return result{value: r, summary: sum}
}

// plural is "s" unless n is 1.
func plural(n int) string { return ops.Plural(n) }

// reviewArgs are the arguments of review.
type reviewArgs struct {
	TaskID          string  `json:"task_id"`
	Verdict         string  `json:"verdict"`
	Notes           string  `json:"notes"`
	AnswerObjection *string `json:"answer_objection"`
	Rule            string  `json:"rule"`
}

func (s *Server) review(ctx context.Context, k *call) (result, error) {
	var a reviewArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" || a.Verdict == "" {
		return result{}, errors.New("task_id and verdict (accepted or rejected) are required")
	}
	answer := ""
	if a.AnswerObjection != nil {
		answer = strings.TrimSpace(*a.AnswerObjection)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	sum, err := c.Review(ctx, a.TaskID, api.ReviewRequest{Verdict: a.Verdict, Notes: a.Notes, AnswerObjection: answer,
		Rule: strings.TrimSpace(a.Rule)})
	if err != nil {
		return result{}, err
	}
	text := fmt.Sprintf("Task %s reviewed: %s.", sum.ID, a.Verdict)
	if sum.LessonSaved {
		text += "\nRule kept in the project's notes under \"Lessons from reviews\"; every later brief carries it."
	}
	for _, w := range sum.Warnings {
		text += "\nWarning: " + w
	}
	if sum.Reminder != "" {
		text += "\nNote: " + sum.Reminder
	}
	return result{value: taskStatus{TaskID: sum.ID, Status: string(sum.Status), Note: sum.Reminder,
		Warnings: sum.Warnings, LessonSaved: sum.LessonSaved}, summary: text}, nil
}

// taskStatus is the answer of review and cancel.
type taskStatus struct {
	TaskID   string   `json:"task_id"`
	Status   string   `json:"status"`
	Note     string   `json:"note,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// LessonSaved: review's rule is in the project's notes.
	LessonSaved bool `json:"lesson_saved,omitempty"`
}

// cancelArgs are the arguments of cancel.
type cancelArgs struct {
	TaskID      string `json:"task_id"`
	WaitSeconds optNum `json:"wait_seconds"`
}

func (s *Server) cancel(ctx context.Context, k *call) (result, error) {
	var a cancelArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" {
		return result{}, errors.New("task_id is required")
	}
	secs := clampWait(a.WaitSeconds, defaultCancel, maxCancel)
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	sum, stopped, err := c.Cancel(ctx, a.TaskID, api.CancelRequest{WaitSeconds: secs})
	if ops.CancelRecorded(err) {
		return result{value: taskStatus{TaskID: a.TaskID, Status: "cancelling",
			Note: "the daemon is stopping; the cancel was recorded and is carried out when it starts again. " +
				"Do not cancel again; check with task later."},
			summary: fmt.Sprintf("Task %s: cancel recorded; the daemon is stopping.", a.TaskID)}, nil
	}
	if err != nil {
		return result{}, err
	}
	if !stopped {
		return result{value: taskStatus{TaskID: a.TaskID, Status: "cancelling",
			Note: fmt.Sprintf("the worker is still stopping after %d s; it ends as cancelled. Check with task.", secs)},
			summary: fmt.Sprintf("Task %s is still stopping.", a.TaskID)}, nil
	}
	return result{value: taskStatus{TaskID: a.TaskID, Status: string(sum.Status)},
		summary: fmt.Sprintf("Task %s: %s.", a.TaskID, sum.Status)}, nil
}

// taskArgs are the arguments of task.
type taskArgs struct {
	TaskID string  `json:"task_id"`
	Steps  optNum  `json:"steps"`
	Full   optBool `json:"full"`
}

func (s *Server) task(ctx context.Context, k *call) (result, error) {
	var a taskArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" {
		return result{}, errors.New("task_id is required")
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	var steps []ops.Step
	if n := int(math.Round(a.Steps.or(0))); n > 0 {
		if steps, err = ops.RecentSteps(ctx, c, a.TaskID, n); err != nil {
			return result{}, err
		}
	}
	if a.Full.or(false) {
		d, err := c.Task(ctx, a.TaskID)
		if err != nil {
			return result{}, err
		}
		return result{value: struct {
			api.TaskDetail
			RecentSteps []ops.Step `json:"recent_steps,omitempty"`
		}{d, steps}, summary: fmt.Sprintf("Task %s: %s, %d turn%s.", d.ID, d.Status, len(d.Turns), plural(len(d.Turns)))}, nil
	}
	r, err := c.Report(ctx, a.TaskID, "")
	if err != nil {
		return result{}, err
	}
	return result{value: struct {
		api.TaskReport
		RecentSteps []ops.Step `json:"recent_steps,omitempty"`
	}{r, steps}, summary: ops.ReportSummary(r)}, nil
}
