package task

import (
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// codexTurn is a whole successful codex run whose turn.completed reports the thread total in, cached and out.
func codexTurn(thread, text string, in, cached, out int) string {
	return script(
		jsonLine(map[string]any{"type": "thread.started", "thread_id": thread}),
		jsonLine(map[string]any{"type": "turn.started"}),
		jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "m", "type": "agent_message",
			"text": text}}),
		jsonLine(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": in,
			"cached_input_tokens": cached, "output_tokens": out}}))
}

// TestCodexFollowupCountsItsOwnUsage: codex reports the thread's running total at turn.completed, so a followup
// that resumes the thread records only what it added: the attempt, the turn and the usage history count 500 input
// tokens for the second turn, not the 1500 codex reported.
func TestCodexFollowupCountsItsOwnUsage(t *testing.T) {
	h := newHarness(t)
	h.withCodex()
	h.queue.Push(t, codexTurn("th-usage", "Added it.", 1000, 600, 100))
	sum := h.delegate(api.DelegateRequest{Backend: config.BackendCodex, Model: "gpt-6-luna"})
	h.waitStatus(sum.ID, StatusDone)

	h.queue.Push(t, codexTurn("th-usage", "Renamed it.", 1500, 900, 150))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Rename it."}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the followup ends", func() bool {
		tk := h.task(sum.ID)
		return len(tk.Turns) == 2 && tk.Status == StatusDone
	})
	tk := h.task(sum.ID)
	if recs := h.queue.Records(t); len(recs) != 2 || !strings.Contains(strings.Join(recs[1].Args, " "),
		"resume th-usage") {
		t.Fatalf("the followup did not resume the thread: %+v", recs)
	}

	first := event.Usage{InputTokens: 1000, CachedTokens: 600, OutputTokens: 100, TotalTokens: 1100}
	second := event.Usage{InputTokens: 500, CachedTokens: 300, OutputTokens: 50, TotalTokens: 550}
	a1, a2 := tk.Turns[0].Attempts[0], tk.Turns[1].Attempts[0]
	if a1.Usage == nil || *a1.Usage != first || a1.Outcome == nil || a1.Outcome.ThreadUsage == nil ||
		*a1.Outcome.ThreadUsage != first {
		t.Errorf("first attempt usage %+v, outcome %+v", a1.Usage, a1.Outcome)
	}
	if !a2.Continuing || a2.Usage == nil || *a2.Usage != second || a2.Outcome == nil ||
		a2.Outcome.ThreadUsage == nil || a2.Outcome.ThreadUsage.InputTokens != 1500 {
		t.Errorf("resumed attempt usage %+v, outcome %+v", a2.Usage, a2.Outcome)
	}
	if u := tk.Turns[1].Usage; u == nil || *u != second {
		t.Errorf("second turn usage = %+v, want %+v", u, second)
	}
	var live []event.Usage
	for _, ev := range eventsOf(h.events(sum.ID), api.EventWorker) {
		var w event.Event
		decode(t, ev, &w)
		if ev.Turn == 2 && w.Kind == event.KindUsage && w.Usage != nil {
			live = append(live, *w.Usage)
		}
	}
	if len(live) != 1 || live[0] != second {
		t.Errorf("second turn usage events = %+v, want one of %+v", live, second)
	}

	sumUsage, err := h.usage.Summary(t.Context(), 1, usage.Lookups{})
	if err != nil {
		t.Fatal(err)
	}
	if sumUsage.Totals.Tokens != first.TotalTokens+second.TotalTokens || sumUsage.Totals.Attempts != 2 {
		t.Errorf("usage summary totals = %+v, want %d tokens over two attempts", sumUsage.Totals,
			first.TotalTokens+second.TotalTokens)
	}
}

// TestUsageBaselineOf: the total before an attempt is the latest one reported earlier in the same conversation; an
// attempt that starts a conversation starts from zero, and an attempt recorded before outcomes kept the total gives
// its usage (codex's total then).
func TestUsageBaselineOf(t *testing.T) {
	u := func(in int64) *event.Usage { return &event.Usage{InputTokens: in, TotalTokens: in} }
	tk := &Task{Turns: []*Turn{
		{N: 1, Attempts: []*Attempt{
			{N: 1, ConversationID: "th-a", Usage: u(100), Outcome: &backend.Outcome{ThreadUsage: u(100)}},
		}},
		{N: 2, Attempts: []*Attempt{
			{N: 1, ConversationID: "th-a", Continuing: true, Usage: u(50), Outcome: &backend.Outcome{ThreadUsage: u(150)}},
			{N: 2, ConversationID: "th-a", Continuing: true, Outcome: &backend.Outcome{}}, // failed: no usage
			{N: 3, ConversationID: "th-b", Usage: u(70), Outcome: &backend.Outcome{ThreadUsage: u(70)}},
		}},
		{N: 3, Attempts: []*Attempt{
			{N: 1, ConversationID: "th-a", Continuing: true},
			{N: 2, ConversationID: "th-b", Continuing: true},
			{N: 3, ConversationID: "th-c"},
		}},
		{N: 4, Attempts: []*Attempt{
			{N: 1, ConversationID: "th-old", Usage: u(400), Outcome: &backend.Outcome{}}, // before ThreadUsage
			{N: 2, ConversationID: "th-old", Continuing: true},
		}},
	}}
	for _, c := range []struct {
		turn, n int
		want    int64
	}{
		{1, 1, 0},   // first run of th-a
		{2, 2, 150}, // after the resumed run
		{3, 1, 150}, // the failed attempt reported nothing
		{3, 2, 70},
		{3, 3, 0}, // not continuing
		{4, 2, 400},
		{5, 1, 0}, // no such turn
	} {
		if got := usageBaselineOf(tk, c.turn, c.n); got.InputTokens != c.want {
			t.Errorf("turn %d attempt %d: baseline %+v, want %d input tokens", c.turn, c.n, got, c.want)
		}
	}
}
