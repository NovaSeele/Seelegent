package backend

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// TestReadExitText: every message exitText writes reads back as its kind, backend and exit code, so the dashboard
// words a failed turn from a code (design review: "agy exited with code 0 and no result" was shown in English).
func TestReadExitText(t *testing.T) {
	for _, tc := range []struct {
		exit launch.Exit
		want ExitFailure
	}{
		{launch.Exit{Reason: launch.ReasonTimeout}, ExitFailure{Kind: ExitTimeout, Backend: "agy"}},
		{launch.Exit{Reason: launch.ReasonCanceled}, ExitFailure{Kind: ExitCanceled, Backend: "agy"}},
		{launch.Exit{Reason: launch.ReasonStartFailed, Error: "no such file"},
			ExitFailure{Kind: ExitStartFailed, Backend: "agy"}},
		{launch.Exit{Reason: launch.ReasonLost}, ExitFailure{Kind: ExitLauncherLost, Backend: "agy"}},
		{launch.Exit{Code: 0}, ExitFailure{Kind: ExitNoResult, Backend: "agy"}},
		{launch.Exit{Code: -1}, ExitFailure{Kind: ExitNoResult, Backend: "agy", Code: -1}},
		{launch.Exit{Code: 2, Error: "boom"}, ExitFailure{Kind: ExitError, Backend: "agy", Code: 2}},
	} {
		msg := exitText("agy", tc.exit)
		if got, ok := ReadExitText(msg); !ok || got != tc.want {
			t.Errorf("%q: got %+v %v, want %+v", msg, got, ok, tc.want)
		}
	}
	for _, msg := range []string{"", "quota exhausted", "the agy run failed with code 0 and no result"} {
		if _, ok := ReadExitText(msg); ok {
			t.Errorf("%q read as an exit", msg)
		}
	}
}

// replay feeds a recorded stream to a parser the way the task engine does (stream lines, then stderr lines, then
// Finish) and returns every event and the outcome.
func replay(t *testing.T, b Backend, file string, secrets ...string) ([]event.Event, Outcome) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "streams", b.Name(), file))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	p := b.NewParser(secrets)
	var (
		events []event.Event
		stderr []string
		code   int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#!stderr "):
			stderr = append(stderr, strings.TrimPrefix(line, "#!stderr "))
		case strings.HasPrefix(line, "#!exit "):
			code, _ = strconv.Atoi(strings.TrimPrefix(line, "#!exit "))
		case strings.HasPrefix(line, "#!"):
		default:
			events = append(events, p.Line([]byte(line))...)
		}
	}
	for _, l := range stderr {
		events = append(events, p.Stderr(l)...)
	}
	exit := launch.Exit{Reason: launch.ReasonExited, Code: code}
	events = append(events, p.Finish(exit, strings.Join(stderr, "\n"))...)
	var c Collector
	for _, ev := range events {
		c.Add(ev)
	}
	return events, c.Outcome(exit)
}

func kinds(events []event.Event) map[event.Kind]int {
	out := map[event.Kind]int{}
	for _, e := range events {
		out[e.Kind]++
	}
	return out
}

func mustBackend(t *testing.T, name string) Backend {
	t.Helper()
	b, err := New(name, "/usr/bin/"+name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAgyStreamSuccess(t *testing.T) {
	events, o := replay(t, mustBackend(t, "agy"), "success.jsonl")
	if o.Status != OutcomeOK || o.ConversationID != "c0ffee00-1111-4222-8333-444455556666" ||
		o.Model != "gemini-3.8-flash-medium" {
		t.Fatalf("outcome = %+v", o)
	}
	if !strings.HasPrefix(o.Response, "Added the nil check in handler.go") {
		t.Errorf("response = %q", o.Response)
	}
	if !slices.Equal(o.FilesEdited, []string{"/work/demo/internal/api/handler.go"}) ||
		!slices.Equal(o.FilesRead, []string{"/work/demo/internal/api/handler.go"}) {
		t.Errorf("files edited %q read %q", o.FilesEdited, o.FilesRead)
	}
	if !slices.Equal(o.Commands, []string{"go test ./internal/api/"}) ||
		!slices.Equal(o.BlockedCommands, []string{"git push origin main"}) {
		t.Errorf("commands %q blocked %q", o.Commands, o.BlockedCommands)
	}
	if !slices.Equal(o.DeniedActions, []string{"git push origin main"}) {
		t.Errorf("denied = %q", o.DeniedActions)
	}
	// The report total wins over the per-step usage.
	want := event.Usage{InputTokens: 18400, OutputTokens: 1210, CachedTokens: 12000, TotalTokens: 19610}
	if o.Usage != want || o.DurationMs != 41700 {
		t.Errorf("usage %+v duration %d", o.Usage, o.DurationMs)
	}
	k := kinds(events)
	// The duplicated DONE line of step 3 is reported once.
	if k[event.KindToolCall] != 4 || k[event.KindToolResult] != 4 || k[event.KindMessage] != 2 ||
		k[event.KindUsage] != 1 || k[event.KindReport] != 1 || k[event.KindError] != 0 {
		t.Errorf("event kinds = %v", k)
	}
}

func TestStreamClassification(t *testing.T) {
	cases := []struct {
		backend, file string
		status        OutcomeStatus
		class         event.Class
		errHas        string
	}{
		{"agy", "quota.jsonl", OutcomeFailed, event.ClassQuotaExhausted, "RESOURCE_EXHAUSTED"},
		{"agy", "auth.jsonl", OutcomeFailed, event.ClassAuthRequired, "authentication required"},
		// B8: the worker's own test output mentions "signing", "quota", 401/403 and "forbidden"; the CLI's error
		// text decides, and it says nothing about accounts.
		{"agy", "fatal.jsonl", OutcomeFailed, event.ClassFatal, "empty response"},
		{"agy", "background.jsonl", OutcomeBackgroundWait, "", ""},
		{"claude", "quota.jsonl", OutcomeFailed, event.ClassQuotaExhausted, "usage limit"},
		{"claude", "auth.jsonl", OutcomeFailed, event.ClassAuthRequired, "Invalid API key"},
		{"claude", "overloaded.jsonl", OutcomeFailed, event.ClassTransient, "overloaded"},
		{"codex", "quota.jsonl", OutcomeFailed, event.ClassQuotaExhausted, "usage limit"},
		{"codex", "transient.jsonl", OutcomeFailed, event.ClassTransient, "stream error"},
	}
	for _, c := range cases {
		t.Run(c.backend+"/"+c.file, func(t *testing.T) {
			_, o := replay(t, mustBackend(t, c.backend), c.file, "sk-ant-test-0000SECRET")
			if o.Status != c.status || o.Class != c.class || !strings.Contains(o.Error, c.errHas) {
				t.Fatalf("outcome = %+v; want %s/%s with %q", o, c.status, c.class, c.errHas)
			}
		})
	}
}

func TestAgyBackgroundWait(t *testing.T) {
	events, o := replay(t, mustBackend(t, "agy"), "background.jsonl")
	if o.Status != OutcomeBackgroundWait || !strings.Contains(o.BackgroundWait, "waiting up to 45m0s") {
		t.Fatalf("outcome = %+v", o)
	}
	if kinds(events)[event.KindBackgroundWait] != 2 {
		t.Errorf("events = %v", kinds(events))
	}
	// A stream line after the wait line means agy went on: the wait no longer counts.
	p := mustBackend(t, "agy").NewParser(nil)
	p.Stderr("root agent idle; waiting up to 45m0s for 1 background task(s) to finish")
	p.Line([]byte(`{"event":"step_update","step_update":{"step_index":9,"step_type":"agent_response","state":"RUNNING","text_delta":"done"}}`))
	fin := p.Finish(launch.Exit{Reason: launch.ReasonExited, Code: 1}, "")
	if kinds(fin)[event.KindBackgroundWait] != 0 || kinds(fin)[event.KindError] != 1 {
		t.Errorf("finish after resume = %+v", fin)
	}
	// The wait line in the result error counts too.
	p = mustBackend(t, "agy").NewParser(nil)
	p.Line([]byte(`{"event":"result","result":{"status":"ERROR","error":"root agent idle; waiting up to 10m for 2 background tasks"}}`))
	if fin := p.Finish(launch.Exit{Reason: launch.ReasonExited}, ""); kinds(fin)[event.KindBackgroundWait] != 1 {
		t.Errorf("wait in result error not seen: %+v", fin)
	}
	if mustBackend(t, "claude").NewParser(nil).Stderr("root agent idle; waiting up to 1m for 1 background task") != nil {
		t.Error("claude recognized agy's wait line")
	}
}

func TestClaudeStreamSuccess(t *testing.T) {
	events, o := replay(t, mustBackend(t, "claude"), "success.jsonl")
	if o.Status != OutcomeOK || o.ConversationID != "4f1c2b9e-5d3a-4e7f-9a10-b2c3d4e5f607" || o.Model != "claude-sonnet-5" {
		t.Fatalf("outcome = %+v", o)
	}
	if o.Response != "Load now validates the config; tests pass. I did not commit." {
		t.Errorf("response = %q", o.Response)
	}
	if !slices.Equal(o.FilesEdited, []string{"/work/demo/config/load.go"}) ||
		!slices.Equal(o.FilesRead, []string{"/work/demo/config/load.go"}) {
		t.Errorf("files edited %q read %q", o.FilesEdited, o.FilesRead)
	}
	if !slices.Equal(o.Commands, []string{"go test ./config/"}) ||
		!slices.Equal(o.BlockedCommands, []string{"git commit -am wip"}) {
		t.Errorf("commands %q blocked %q", o.Commands, o.BlockedCommands)
	}
	want := event.Usage{InputTokens: 2150, OutputTokens: 920, CachedTokens: 18000, TotalTokens: 3070, CostUSD: 0.0841}
	if o.Usage != want || o.DurationMs != 38211 {
		t.Errorf("usage %+v duration %d", o.Usage, o.DurationMs)
	}
	k := kinds(events)
	if k[event.KindToolCall] != 4 || k[event.KindToolResult] != 4 || k[event.KindMessage] != 2 || k[event.KindFile] != 2 {
		t.Errorf("event kinds = %v", k)
	}
	for _, e := range events {
		if e.Kind == event.KindToolResult && e.Tool.ID == "toolu_02" && e.Tool.Output != "The file /work/demo/config/load.go has been updated." {
			t.Errorf("block content output = %q", e.Tool.Output)
		}
	}
}

func TestCodexStreamSuccess(t *testing.T) {
	events, o := replay(t, mustBackend(t, "codex"), "success.jsonl")
	if o.Status != OutcomeOK || o.ConversationID != "0199a3f2-7c1e-7d10-9b2a-5e6f7a8b9c0d" {
		t.Fatalf("outcome = %+v", o)
	}
	if o.Response != "Updated the parser in a.ts and b.ts and added c.ts." {
		t.Errorf("response = %q", o.Response)
	}
	// Every path of the multi-file patch (B15).
	if !slices.Equal(o.FilesEdited, []string{"/work/app/src/a.ts", "/work/app/src/b.ts", "/work/app/src/c.ts"}) {
		t.Errorf("files = %q", o.FilesEdited)
	}
	if !slices.Equal(o.Commands, []string{"bash -lc 'rg -n parse src'"}) ||
		!slices.Equal(o.BlockedCommands, []string{"bash -lc 'git push'"}) {
		t.Errorf("commands %q blocked %q", o.Commands, o.BlockedCommands)
	}
	want := event.Usage{InputTokens: 24763, OutputTokens: 1150, CachedTokens: 20096, TotalTokens: 25913}
	if o.Usage != want {
		t.Errorf("usage = %+v", o.Usage)
	}
	var text strings.Builder
	for _, e := range events {
		if e.Kind == event.KindMessage {
			text.WriteString(e.Text)
		}
		if e.Kind == event.KindToolResult && e.Tool.Name == "docs.search" && e.Tool.Output == "" {
			t.Error("mcp tool result has no output")
		}
	}
	// agent_message deltas are diffed against the previous full text.
	if text.String() != o.Response {
		t.Errorf("message deltas = %q", text.String())
	}
	k := kinds(events)
	if k[event.KindLog] != 1 || k[event.KindToolCall] != 3 || k[event.KindToolResult] != 4 {
		t.Errorf("event kinds = %v", k)
	}
}

func TestSecretsMasked(t *testing.T) {
	const key = "sk-ant-test-0000SECRET"
	events, o := replay(t, mustBackend(t, "claude"), "overloaded.jsonl", key, "abc")
	if strings.Contains(o.Error, key) || !strings.Contains(o.Error, redacted) {
		t.Errorf("error not masked: %q", o.Error)
	}
	for _, e := range events {
		if e.Report != nil && strings.Contains(e.Report.Error, key) {
			t.Errorf("report leaks the key: %q", e.Report.Error)
		}
	}
	p := mustBackend(t, "codex").NewParser([]string{key})
	evs := p.Line([]byte(`{"type":"item.completed","item":{"id":"x","type":"command_execution","command":"echo ` + key + `","aggregated_output":"` + key + `","exit_code":0,"status":"completed"}}`))
	evs = append(evs, p.Line([]byte("plain "+key))...)
	evs = append(evs, p.Finish(launch.Exit{Reason: launch.ReasonExited, Code: 1}, "stderr says "+key)...)
	for _, e := range evs {
		data := e.Text
		if e.Tool != nil {
			data += e.Tool.Command + e.Tool.Output + string(e.Tool.Input)
		}
		if e.Report != nil {
			data += e.Report.Error
		}
		if e.Error != nil {
			data += e.Error.Message
		}
		if strings.Contains(data, key) {
			t.Errorf("event leaks the key: %+v", e)
		}
	}
}
