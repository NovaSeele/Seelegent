package task

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The report's attention carries its state, and a stalled task shows it too: a lead woken by the stall would
// otherwise read a report without a reason (M18 hunt H15).
func TestReportAttentionHasItsState(t *testing.T) {
	s := &Service{o: Options{}}
	for _, a := range []api.Attention{
		{State: api.AttentionBlocked, Reason: ReasonLooping},
		{State: api.AttentionQuestion, Reason: ReasonQuestion, Detail: "Which file?"},
		{State: api.AttentionStalled, Reason: ReasonStalled, Detail: "no output for 12 min"},
		{State: api.AttentionBackgroundWait, Reason: ReasonBackgroundWait},
	} {
		tk := &Task{ID: "t1", Status: StatusRunning, Cwd: t.TempDir(), Turns: []*Turn{{N: 1}}, Attention: &a}
		rep := s.report(t.Context(), tk, "")
		if rep.Attention == nil || rep.Attention.State != a.State || rep.Attention.Reason != a.Reason ||
			rep.Attention.Detail != a.Detail {
			t.Errorf("%s: report attention = %+v", a.State, rep.Attention)
		}
	}
	tk := &Task{ID: "t1", Status: StatusDone, Cwd: t.TempDir(), Turns: []*Turn{{N: 1}},
		Attention: &api.Attention{State: api.AttentionInfo, Reason: ReasonReview}}
	if rep := s.report(t.Context(), tk, ""); rep.Attention != nil {
		t.Errorf("info: report attention = %+v", rep.Attention)
	}
}
