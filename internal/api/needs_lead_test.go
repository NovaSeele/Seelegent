package api

import "testing"

// NeedsLead is the one place that says which attention states wait for the lead (M18 review G1).
func TestNeedsLead(t *testing.T) {
	for state, want := range map[string]bool{
		AttentionBlocked:        true,
		AttentionQuestion:       true,
		AttentionStalled:        true,
		AttentionBackgroundWait: false,
		AttentionInfo:           false,
		AttentionCleared:        false,
		"":                      false,
	} {
		if got := NeedsLead(state); got != want {
			t.Errorf("NeedsLead(%q) = %v, want %v", state, got, want)
		}
	}
}
