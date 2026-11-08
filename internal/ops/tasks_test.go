package ops

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// CancelRecorded is true only for a daemon_stopping answer that says the cancel was recorded (M18 review G5).
func TestCancelRecorded(t *testing.T) {
	stop := func(d map[string]any) error {
		return &client.Error{Status: http.StatusServiceUnavailable, Code: api.CodeDaemonStopping, Details: d}
	}
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"recorded":   {fmt.Errorf("cancel: %w", stop(map[string]any{"cancelRecorded": true})), true},
		"not yet":    {stop(nil), false},
		"other code": {&client.Error{Code: api.CodeInternal, Details: map[string]any{"cancelRecorded": true}}, false},
		"no error":   {nil, false},
	} {
		if got := CancelRecorded(c.err); got != c.want {
			t.Errorf("%s: CancelRecorded = %v", name, got)
		}
	}
}

// Every flag that wakes a lead's wait counts as waiting for the lead: ls --blocked, the ATTENTION column and
// status list a question or a stall too (M18 hunt H16).
func TestBlockedCoversEveryFlagForTheLead(t *testing.T) {
	for state, want := range map[string]bool{
		api.AttentionBlocked:        true,
		api.AttentionQuestion:       true,
		api.AttentionStalled:        true,
		api.AttentionBackgroundWait: false,
		api.AttentionInfo:           false,
	} {
		if got := Blocked(api.TaskSummary{Attention: &api.Attention{State: state}}); got != want {
			t.Errorf("Blocked(%s) = %v, want %v", state, got, want)
		}
	}
	if Blocked(api.TaskSummary{}) {
		t.Error("a task without attention is blocked")
	}
}

// The one-line summary names the turn's cost when the report has one (a task on the official Anthropic API).
func TestReportSummaryCost(t *testing.T) {
	r := api.TaskReport{TaskID: "t1", Status: "done", Turn: 2, FilesChanged: []string{"a.go"}, TurnCostUSD: 0.003}
	if got, want := ReportSummary(r), "Task t1: done, turn 2, 1 file changed, turn cost ≈ $0.003."; got != want {
		t.Errorf("ReportSummary = %q, want %q", got, want)
	}
	r.TurnCostUSD = 0
	if got := ReportSummary(r); got != "Task t1: done, turn 2, 1 file changed." {
		t.Errorf("without a cost: %q", got)
	}
}
