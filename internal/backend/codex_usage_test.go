package backend

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// codexRun feeds one codex run that ends with turn.completed carrying usage to a parser that starts from base, and
// returns the usage event, the outcome and the thread total the parser hands back.
func codexRun(t *testing.T, base *event.Usage, usage string) (ev *event.Usage, o Outcome, total *event.Usage) {
	t.Helper()
	p := mustBackend(t, "codex").NewParser(nil)
	cp, ok := p.(*codexParser)
	if !ok {
		t.Fatalf("parser %T has no usage baseline", p)
	}
	if base != nil {
		cp.SetUsageBaseline(*base)
	}
	var c Collector
	evs := p.Line([]byte(`{"type":"thread.started","thread_id":"th-1"}`))
	evs = append(evs, p.Line([]byte(`{"type":"turn.completed","usage":`+usage+`}`))...)
	evs = append(evs, p.Finish(launch.Exit{Reason: launch.ReasonExited}, "")...)
	for _, e := range evs {
		c.Add(e)
		if e.Kind == event.KindUsage {
			ev = e.Usage
		}
	}
	if u, ok := cp.ThreadUsage(); ok {
		total = &u
	}
	return ev, c.Outcome(launch.Exit{Reason: launch.ReasonExited}), total
}

// TestCodexUsageIsThreadTotal: codex exec --json reports the thread's running total at turn.completed, so a resumed
// thread's run counts only what it added to the total before it; a first run is taken as is, a total below the one
// before (codex's counter started again) as is too, and a run that reported only zeros hands back no total.
func TestCodexUsageIsThreadTotal(t *testing.T) {
	first := event.Usage{InputTokens: 1000, CachedTokens: 600, OutputTokens: 100, TotalTokens: 1100}

	ev, o, total := codexRun(t, nil, `{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":100}`)
	if ev == nil || *ev != first || o.Usage != first || total == nil || *total != first {
		t.Fatalf("first run: event %+v, outcome %+v, total %+v", ev, o.Usage, total)
	}

	resumed := `{"input_tokens":1500,"cached_input_tokens":900,"output_tokens":150,"reasoning_output_tokens":20}`
	want := event.Usage{InputTokens: 500, CachedTokens: 300, OutputTokens: 50, TotalTokens: 550}
	ev, o, total = codexRun(t, &first, resumed)
	if ev == nil || *ev != want || o.Usage != want {
		t.Errorf("resumed run: event %+v, outcome %+v, want %+v", ev, o.Usage, want)
	}
	if wantTotal := (event.Usage{InputTokens: 1500, CachedTokens: 900, OutputTokens: 150, TotalTokens: 1650}); total == nil ||
		*total != wantTotal {
		t.Errorf("resumed run total = %+v, want %+v", total, wantTotal)
	}

	// A reset: the new total is below the previous one.
	reset := event.Usage{InputTokens: 200, OutputTokens: 20, TotalTokens: 220}
	ev, o, _ = codexRun(t, &event.Usage{InputTokens: 1500, CachedTokens: 900, OutputTokens: 150, TotalTokens: 1650},
		`{"input_tokens":200,"output_tokens":20}`)
	if ev == nil || *ev != reset || o.Usage != reset {
		t.Errorf("reset: event %+v, outcome %+v, want %+v", ev, o.Usage, reset)
	}

	// Zeros (no token count reached codex exec in the run): no thread total.
	if _, o, total = codexRun(t, &first, `{"input_tokens":0,"output_tokens":0}`); total != nil || !o.Usage.IsZero() {
		t.Errorf("zero run: outcome %+v, total %+v", o.Usage, total)
	}
}
