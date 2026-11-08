package ops

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

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
