package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/event"
)

var update = flag.Bool("update", false, "rewrite the event fixtures")

// eventSamples has one sample event per type. The fixtures under testdata/events are what the dashboard's drift
// check (web/src/api) loads: adding a type without a fixture fails here, and a fixture without a union member
// fails there.
func eventSamples() map[EventType]any {
	remaining := 0.425
	code := 0
	return map[EventType]any{
		EventWorker: event.Event{Kind: event.KindToolCall, Step: 2, Tool: &event.Tool{ID: "c1", Name: "shell",
			State: event.ToolCalling, Command: "go test ./..."}},
		EventLeadMessage: LeadMessageData{Message: "Add a flag", By: "lead", Model: "gemini-3.8-flash", Tier: "standard"},
		EventRoute: RouteData{AccountID: "acc1", AccountLabel: "agy 1", Model: "gemini-3.8-flash", Reason: "most quota",
			Remaining: &remaining},
		EventWorkerPrompt:   WorkerPromptData{Length: 1200, Preview: "You are a coding worker."},
		EventAttemptEnd:     AttemptEndData{Status: "quota", Error: "quota exhausted"},
		EventTurnEnd:        TurnEndData{Status: "ok", Response: "Done.", Files: []string{"main.go"}, Usage: &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		EventQuotaExhausted: AccountProblemData{AccountID: "acc1", Group: "gemini", Until: "2026-11-07T10:00:00.000Z", Error: "quota exhausted"},
		EventAuthFailed:     AccountProblemData{AccountID: "acc2", Error: "signed out"},
		EventWaitingQuota:   WaitingQuotaData{Until: "2026-11-07T10:00:00.000Z"},
		EventRetry:          RetryData{DelayMs: 15000, Error: "connection reset"},
		EventHandoff:        HandoffData{From: "acc1", To: "acc2", Reason: "quota"},
		EventReattach:       RecoveryData{AccountID: "acc1", Offset: 2048},
		EventResumed:        RecoveryData{AccountID: "acc1"},
		EventAttention: Attention{State: AttentionQuestion, Reason: "The worker asked a question", Who: "lead",
			Detail: "Which port?", Since: "2026-11-07T09:00:00.000Z"},
		EventDeps:     DepsData{State: DepsWaiting, After: []string{"t1"}},
		EventTakeover: TakeoverData{State: "end", AccountID: "acc1", DurationMs: 60000, ExitCode: &code},
		EventHandback: HandbackData{Turn: 3, Response: "The user renamed a flag."},
		EventReview:   ReviewData{Verdict: VerdictAccepted, Notes: "good"},
		EventCancel:   EmptyData{},
		EventWorkAtRisk: WorkAtRiskData{Files: []string{"main.go"},
			Snapshot: "refs/seelegent/snap/t1"},
		EventError: ErrorData{Message: "the worker CLI is missing"},
	}
}

// extraSamples are further fixtures for variants of a type the dashboard must handle, by file name.
func extraSamples() map[string]struct {
	typ  EventType
	data any
} {
	return map[string]struct {
		typ  EventType
		data any
	}{
		"attention-cleared": {EventAttention, Attention{State: AttentionCleared, Reason: "The worker went on",
			Since: "2026-11-07T09:05:00.000Z"}},
	}
}

func TestEventFixtures(t *testing.T) {
	samples := eventSamples()
	if len(samples) != len(EventTypes()) {
		t.Fatalf("%d samples for %d types", len(samples), len(EventTypes()))
	}
	type fixture struct {
		name string
		typ  EventType
		data any
	}
	var fixtures []fixture
	for _, typ := range EventTypes() {
		data, ok := samples[typ]
		if !ok {
			t.Errorf("no sample for %s", typ)
			continue
		}
		fixtures = append(fixtures, fixture{string(typ), typ, data})
	}
	for name, x := range extraSamples() {
		fixtures = append(fixtures, fixture{name, x.typ, x.data})
	}
	dir := filepath.Join("testdata", "events")
	for _, f := range fixtures {
		typ := f.typ
		ev := TaskEvent{Seq: 7, TS: "2026-11-07T09:00:00.000Z", TaskID: "t261107090000-ab12", Turn: 1, Attempt: 1,
			Type: typ, Data: f.data}
		got, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join(dir, f.name+".json")
		if *update {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run go test ./internal/api -update)", err)
		}
		if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
			t.Errorf("%s differs from the fixture (run go test ./internal/api -update):\n%s", f.name, got)
		}
	}
}
