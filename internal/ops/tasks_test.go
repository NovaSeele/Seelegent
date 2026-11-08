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
