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
)

// Wait limits of the old server (seconds).
const (
	defaultWait   = 600
	maxWait       = 3000
	defaultCancel = 15
	maxCancel     = 60
	// stepOutput caps the output of one recent step (task steps=N).
	stepOutput = 800
	// stepEvents is how many of the newest events are searched for tool steps.
	stepEvents = 2000
)

// handlers maps tool names to their functions (tools a later milestone builds are not listed).
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
	Variants        json.RawMessage `json:"variants"`
	VariantSpecs    json.RawMessage `json:"variant_specs"`
	WaitSeconds     optNum          `json:"wait_seconds"`
	WakeOnBlocked   optBool         `json:"wake_on_blocked"`
	DeadlineMinutes json.RawMessage `json:"deadline_minutes"`
	Judge           json.RawMessage `json:"judge"`
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
	if given(a.Variants) || given(a.VariantSpecs) {
		return result{}, &unavailableError{what: "contests (delegate with variants)"}
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
	mode, err := taskMode(a.Mode)
	if err != nil {
		return result{}, err
	}
	gate, err := gateFlag(a.Gate)
	if err != nil {
		return result{}, err
	}
	if err := unsupported(a.Lane, a.Verify, a.ForceLane); err != nil {
		return result{}, err
	}
	if a.Member != "" {
		return result{}, &unavailableError{what: "department members (member)",
			detail: "leave member out and pass backend and model instead"}
	}
	req := api.DelegateRequest{Prompt: a.Prompt, Title: a.Title, Cwd: cwd, Mode: mode, Model: a.Model,
		Effort: a.Effort, Backend: a.Backend, Provider: a.Provider, Profile: a.Profile, After: a.After,
		AfterMode: a.AfterMode, AllowGit: a.AllowGit.or(false), Gate: gate, Session: s.session()}
	switch {
	case a.Spec != "":
		req.Spec = &api.SpecRequest{Dir: a.Spec, TaskIDs: a.TaskIDs}
	case len(a.TaskIDs) > 0:
		return result{}, errors.New("task_ids needs spec (the Spec Kit feature folder they come from)")
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	sum, err := c.Delegate(ctx, req)
	if err != nil {
		return result{}, err
	}
	// A dependent task usually waits for others: do not block on it unless asked.
	def := defaultWait
	if len(a.After) > 0 {
		def = 0
	}
	res, err := s.waitReports(ctx, k, []string{sum.ID}, clampWait(a.WaitSeconds, def, maxWait), false,
		a.WakeOnBlocked.or(true), cwd)
	if err != nil {
		return result{}, fmt.Errorf("task %s was created, but reading its report failed: %w", sum.ID, err)
	}
	return reportResult(res), nil
}

// taskMode maps the old mode (full, plan) onto the API's (edit, plan).
func taskMode(mode string) (string, error) {
	switch mode {
	case "":
		return "", nil
	case "full", "edit":
		return "edit", nil
	case "plan":
		return "plan", nil
	}
	return "", fmt.Errorf("mode %q must be full or plan", mode)
}

// gateFlag maps the gate argument: false turns gates off; commands need the proof gate milestone.
func gateFlag(g gateArg) (*bool, error) {
	switch {
	case g.off:
		f := false
		return &f, nil
	case len(g.cmds) > 0:
		return nil, &unavailableError{what: "running gate commands", detail: "leave gate out or pass false"}
	}
	return nil, nil
}

// unsupported refuses, before any call, the delegate and followup options this version does not have yet (the
// daemon would refuse them too, with a less helpful error). lane: false and verify/force_lane false mean "off" and
// pass.
func unsupported(lane laneArg, verify, forceLane optBool) error {
	switch {
	case len(lane.paths) > 0:
		return &unavailableError{what: "lane enforcement (lane)", detail: "leave lane out or pass false"}
	case verify.or(false):
		return &unavailableError{what: "the verifier (verify: true)", detail: "leave verify out"}
	case forceLane.or(false):
		return &unavailableError{what: "force_lane", detail: "leave force_lane out; lanes are not enforced yet"}
	}
	return nil
}

// session is the lead session that delegates, from the host's environment (as the old server read it).
func (s *Server) session() *api.Session {
	id := s.o.Getenv("SEELEGENT_SESSION")
	if id == "" {
		id = s.o.Getenv("CLAUDE_CODE_SESSION_ID")
	}
	host := s.o.Getenv("CLAUDE_CODE_HOST_SESSION_ID")
	if id == "" && host == "" {
		return nil
	}
	return &api.Session{ID: id, HostID: host}
}

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
	gate, err := gateFlag(a.Gate)
	if err != nil {
		return result{}, err
	}
	if err := unsupported(a.Lane, a.Verify, a.ForceLane); err != nil {
		return result{}, err
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
		Profile: a.Profile, AllowGit: a.AllowGit.ptr(), Gate: gate, Force: a.Force.or(false)}
	if _, err := c.Followup(ctx, a.TaskID, req); err != nil {
		return result{}, err
	}
	res, err := s.waitReports(ctx, k, []string{a.TaskID}, clampWait(a.WaitSeconds, defaultWait, maxWait), false,
		a.WakeOnBlocked.or(true), "")
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
		a.WakeOnBlocked.or(true), "")
	if err != nil {
		return result{}, err
	}
	if len(a.TaskIDs) == 1 {
		return reportResult(res), nil
	}
	return result{value: api.NonNil(res.Reports), summary: waitSummary(res)}, nil
}

// waitReports waits for ids through POST /wait and returns the result with the reports (relative to cwd when
// given, else to each task's folder). When the daemon goes away during the wait (a restart), it finds or starts
// it again and waits for the rest of the time.
func (s *Server) waitReports(ctx context.Context, k *call, ids []string, seconds int, anyDone, wake bool,
	cwd string) (api.WaitResult, error) {
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	left := seconds
	for reconnecting := false; ; reconnecting = true {
		c, err := k.client(ctx)
		if err != nil && !reconnecting {
			return api.WaitResult{}, err
		}
		if err == nil {
			var res api.WaitResult
			res, err = c.Wait(ctx, api.WaitRequest{IDs: ids, TimeoutSeconds: left, Any: anyDone,
				WakeOnBlocked: &wake, Reports: true}, cwd)
			if err == nil {
				if len(res.Reports) != len(ids) {
					return api.WaitResult{}, fmt.Errorf("the daemon returned %d reports for %d tasks",
						len(res.Reports), len(ids))
				}
				return res, nil
			}
		}
		// A restarting daemon is unreachable for a moment, or slow to answer its first probe: wait for it until
		// the wait's own deadline.
		retry := errors.Is(err, client.ErrUnreachable) || client.TimedOut(err)
		if !retry || !time.Now().Before(deadline) {
			return api.WaitResult{}, err
		}
		s.o.Logger.Info("mcp: the daemon is gone or not answering during a wait; reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return api.WaitResult{}, ctx.Err()
		case <-time.After(s.o.RetryPause):
		}
		k.c = nil
		left = max(int(math.Ceil(time.Until(deadline).Seconds())), 0)
	}
}

// reportResult is the result of a call that returns one task's report.
func reportResult(res api.WaitResult) result {
	r := res.Reports[0]
	sum := reportSummary(r)
	if res.Woke == api.WokeTimeout && pending(r.Status) {
		sum += " The wait ended before the task did: call wait again."
	}
	return result{value: r, summary: sum}
}

// pending reports whether a task status is not final.
func pending(status string) bool {
	switch api.TaskStatus(status) {
	case api.StatusQueued, api.StatusRunning, api.StatusWaitingQuota, api.StatusWaitingDeps:
		return true
	}
	return false
}

// reportSummary is the one-line summary of a report.
func reportSummary(r api.TaskReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s: %s", r.TaskID, r.Status)
	if r.Turn > 0 {
		fmt.Fprintf(&b, ", turn %d", r.Turn)
	}
	if n := len(r.FilesChanged); n > 0 {
		fmt.Fprintf(&b, ", %d file%s changed", n, plural(n))
	}
	if r.Attention != nil {
		fmt.Fprintf(&b, ", needs you: %s", r.Attention.Reason)
	}
	if r.Error != "" {
		b.WriteString(", with an error")
	}
	b.WriteString(".")
	return b.String()
}

// waitSummary is the one-line summary of a wait for several tasks.
func waitSummary(res api.WaitResult) string {
	parts := make([]string, 0, len(res.Reports))
	for i := range res.Reports {
		parts = append(parts, res.Reports[i].TaskID+" "+res.Reports[i].Status)
	}
	return fmt.Sprintf("Woke: %s. %s.", res.Woke, strings.Join(parts, ", "))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// reviewArgs are the arguments of review.
type reviewArgs struct {
	TaskID          string  `json:"task_id"`
	Verdict         string  `json:"verdict"`
	Notes           string  `json:"notes"`
	AnswerObjection *string `json:"answer_objection"`
}

func (s *Server) review(ctx context.Context, k *call) (result, error) {
	var a reviewArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	if a.TaskID == "" || a.Verdict == "" {
		return result{}, errors.New("task_id and verdict (accepted or rejected) are required")
	}
	notes := a.Notes
	if a.AnswerObjection != nil && strings.TrimSpace(*a.AnswerObjection) != "" {
		notes = strings.TrimSpace(notes + "\n\nAnswer to the objection: " + *a.AnswerObjection)
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	sum, err := c.Review(ctx, a.TaskID, api.ReviewRequest{Verdict: a.Verdict, Notes: notes})
	if err != nil {
		return result{}, err
	}
	return result{value: taskStatus{TaskID: sum.ID, Status: string(sum.Status)},
		summary: fmt.Sprintf("Task %s reviewed: %s.", sum.ID, a.Verdict)}, nil
}

// taskStatus is the answer of review and cancel.
type taskStatus struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
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

// step is one recent worker tool step (task steps=N).
type step struct {
	Tool   string          `json:"tool"`
	Params json.RawMessage `json:"params,omitempty"`
	Output string          `json:"output,omitempty"`
	Failed bool            `json:"failed,omitempty"`
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
	var steps []step
	if n := int(math.Round(a.Steps.or(0))); n > 0 {
		if steps, err = recentSteps(ctx, c, a.TaskID, n); err != nil {
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
			RecentSteps []step `json:"recent_steps,omitempty"`
		}{d, steps}, summary: fmt.Sprintf("Task %s: %s, %d turn%s.", d.ID, d.Status, len(d.Turns), plural(len(d.Turns)))}, nil
	}
	r, err := c.Report(ctx, a.TaskID, "")
	if err != nil {
		return result{}, err
	}
	return result{value: struct {
		api.TaskReport
		RecentSteps []step `json:"recent_steps,omitempty"`
	}{r, steps}, summary: reportSummary(r)}, nil
}

// recentSteps returns the last n finished tool steps of the task, newest last (old B6: the old server showed the
// oldest ones).
func recentSteps(ctx context.Context, c *client.Client, id string, n int) ([]step, error) {
	page, err := c.TailEvents(ctx, id, stepEvents)
	if err != nil {
		return nil, err
	}
	var out []step
	for _, e := range page.Items {
		if e.Type != api.EventWorker {
			continue
		}
		var w struct {
			Kind string `json:"kind"`
			Tool *struct {
				Name    string          `json:"name"`
				State   string          `json:"state"`
				Input   json.RawMessage `json:"input"`
				Command string          `json:"command"`
				Output  string          `json:"output"`
			} `json:"tool"`
		}
		if json.Unmarshal(e.Data, &w) != nil || w.Kind != "tool_result" || w.Tool == nil {
			continue
		}
		st := step{Tool: w.Tool.Name, Params: w.Tool.Input, Output: truncate(w.Tool.Output, stepOutput),
			Failed: w.Tool.State == "failed"}
		if len(st.Params) == 0 && w.Tool.Command != "" {
			st.Params, _ = json.Marshal(map[string]string{"command": w.Tool.Command})
		}
		out = append(out, st)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// truncate keeps the first n runes of s.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
