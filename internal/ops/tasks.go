package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

const (
	// recentTasks is how many of the newest tasks ActiveTasks looks through for tasks that need the lead.
	recentTasks = 50
	// stepOutput caps the output of one recent step.
	stepOutput = 800
	// stepEvents is how many of the newest events are searched for tool steps.
	stepEvents = 2000
)

// ActiveTasks are the pending tasks (and those blocked on prerequisites) plus recent tasks that need the lead,
// those first.
func ActiveTasks(ctx context.Context, c *client.Client) ([]api.TaskSummary, error) {
	pending, err := c.Tasks(ctx, client.TaskQuery{Statuses: []api.TaskStatus{api.StatusQueued, api.StatusRunning,
		api.StatusWaitingQuota, api.StatusWaitingDeps, api.StatusBlockedDeps}, Limit: 200})
	if err != nil {
		return nil, err
	}
	recent, err := c.Tasks(ctx, client.TaskQuery{Limit: recentTasks})
	if err != nil {
		return nil, err
	}
	out := pending.Items
	for i := range recent.Items {
		t := &recent.Items[i]
		if Blocked(*t) && !slices.ContainsFunc(out, func(x api.TaskSummary) bool { return x.ID == t.ID }) {
			out = append(out, *t)
		}
	}
	slices.SortStableFunc(out, func(a, b api.TaskSummary) int {
		switch {
		case Blocked(a) && !Blocked(b):
			return -1
		case Blocked(b) && !Blocked(a):
			return 1
		}
		return 0
	})
	return api.NonNil(out), nil
}

// Blocked reports whether a task waits for the lead: it is blocked, asks a question or stalled, the flags that wake
// a lead's wait (M18 hunt H16: questions and stalls were left out).
func Blocked(t api.TaskSummary) bool {
	return t.Attention != nil && api.NeedsLead(t.Attention.State)
}

// CancelRecorded reports whether err is the daemon_stopping answer of a cancel whose wait the shutdown cut short
// after the cancel was recorded: the task is cancelled when the daemon starts again (M18 review G5).
func CancelRecorded(err error) bool {
	var apiErr *client.Error
	return errors.As(err, &apiErr) && apiErr.Code == api.CodeDaemonStopping && apiErr.Details["cancelRecorded"] == true
}

// TaskReason says why a task needs attention, or nil.
func TaskReason(t api.TaskSummary) *string {
	var r string
	switch {
	case t.Attention != nil && t.Attention.State != api.AttentionInfo:
		r = t.Attention.Reason
	case t.Status == api.StatusBlockedDeps && t.DepsReason != "":
		r = t.DepsReason
	default:
		return nil
	}
	return &r
}

// Step is one recent worker tool step (task steps=N).
type Step struct {
	Tool   string          `json:"tool"`
	Params json.RawMessage `json:"params,omitempty"`
	Output string          `json:"output,omitempty"`
	Failed bool            `json:"failed,omitempty"`
}

// RecentSteps returns the last n finished tool steps of the task, newest last (old B6: the old server showed the
// oldest ones).
func RecentSteps(ctx context.Context, c *client.Client, id string, n int) ([]Step, error) {
	page, err := c.TailEvents(ctx, id, stepEvents)
	if err != nil {
		return nil, err
	}
	var out []Step
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
		st := Step{Tool: w.Tool.Name, Params: w.Tool.Input, Output: Truncate(w.Tool.Output, stepOutput),
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

// TaskMode maps the old mode (full, plan) onto the API's (edit, plan).
func TaskMode(mode string) (string, error) {
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

// Pending reports whether a task status is not final.
func Pending(status string) bool {
	switch api.TaskStatus(status) {
	case api.StatusQueued, api.StatusRunning, api.StatusWaitingQuota, api.StatusWaitingDeps:
		return true
	}
	return false
}

// ReportSummary is the one-line summary of a report.
func ReportSummary(r api.TaskReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s: %s", r.TaskID, r.Status)
	if r.Turn > 0 {
		fmt.Fprintf(&b, ", turn %d", r.Turn)
	}
	if n := len(r.FilesChanged); n > 0 {
		fmt.Fprintf(&b, ", %d file%s changed", n, Plural(n))
	}
	if r.Attention != nil {
		fmt.Fprintf(&b, ", needs you: %s", r.Attention.Reason)
	}
	if r.Error != "" {
		b.WriteString(", with an error")
	}
	if r.WaitDetail != "" {
		fmt.Fprintf(&b, ", waiting: %s", r.WaitDetail)
	}
	b.WriteString(".")
	return b.String()
}

// WaitSummary is the one-line summary of a wait for several tasks.
func WaitSummary(res api.WaitResult) string {
	parts := make([]string, 0, len(res.Reports))
	for i := range res.Reports {
		parts = append(parts, res.Reports[i].TaskID+" "+res.Reports[i].Status)
	}
	return fmt.Sprintf("Woke: %s. %s.", res.Woke, strings.Join(parts, ", "))
}

// Plural is "s" unless n is 1.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Truncate keeps the first n runes of s.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
