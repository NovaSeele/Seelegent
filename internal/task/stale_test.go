package task

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// staleErr is a network error as agy keeps it in a conversation (a real run's, with its host and IPs replaced): the
// same text, source port included, comes back on every resume.
const staleErr = `API error (attempt 2): request failed: Post "https://api.example.test/v1internal:` +
	`streamGenerateContent?alt=sse": read tcp 10.0.0.2:55383->203.0.113.4:443: wsarecv: An existing connection ` +
	`was forcibly closed by the remote host.`

// withClaude adds the claude backend and one sign-in account of it.
func (h *harness) withClaude() {
	h.t.Helper()
	h.backends = append(h.backends, config.BackendClaude)
	cl, err := backend.New(config.BackendClaude, fakecli.Exe(h.t))
	if err != nil {
		h.t.Fatal(err)
	}
	h.s.o.Backends[config.BackendClaude] = cl
	set := h.config()
	set.Backends.Claude.Enabled = true
	set.Backends.Claude.Tiers = config.TierModels{Fast: "claude-sonnet-5", Standard: "claude-sonnet-5",
		Strong: "claude-sonnet-5", Max: "claude-sonnet-5"}
	h.settings.Store(&set)
	if _, _, err := h.accounts.Add(h.t.Context(), account.New{Backend: config.BackendClaude,
		Kind: account.KindIsolated, Label: "cl", Enabled: true}, nil); err != nil {
		h.t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	h.pool.Notify()
}

// staleRun is how one backend's CLI writes the runs of these tests.
type staleRun struct {
	backend, model string
	setup          func(h *harness)
	// failed is a run in conversation conv that ends with the CLI's own report of errText; with text, the agent
	// wrote that much before (and went on with a tool step, so it is no final answer).
	failed func(conv, text, errText string) string
	ok     func(conv, text string) string
	// final is a run whose agent ends with text as its final answer (no tool step after it), then the CLI reports
	// errText.
	final func(conv, text, errText string) string
}

func agyRun() staleRun {
	return staleRun{backend: config.BackendAgy, setup: func(h *harness) { h.agy("only", 0) },
		failed: func(conv, text, errText string) string {
			lines := []string{agyInit(conv)}
			if text != "" {
				lines = append(lines, agyDelta(1, text))
			}
			lines = append(lines, agyTool(2, "CALLING", "go test ./..."),
				jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "ERROR",
					"error": errText, "conversation_id": conv}}), "#!exit 0")
			return script(lines...)
		},
		ok: okScript,
		final: func(conv, text, errText string) string {
			return script(agyInit(conv), agyDelta(1, text),
				jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": 1,
					"step_type": "agent_response", "state": "DONE"}}),
				jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "ERROR", "error": errText,
					"conversation_id": conv, "usage": map[string]any{"input_tokens": 1000, "output_tokens": 200,
						"total_tokens": 1200}}}), "#!exit 0")
		}}
}

func claudeRun() staleRun {
	return staleRun{backend: config.BackendClaude, model: "claude-sonnet-5", setup: (*harness).withClaude,
		failed: func(conv, text, errText string) string {
			content := []any{}
			if text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			content = append(content, map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash",
				"input": map[string]any{"command": "go test ./..."}})
			return script(
				jsonLine(map[string]any{"type": "system", "subtype": "init", "session_id": conv,
					"model": "claude-sonnet-5"}),
				jsonLine(map[string]any{"type": "assistant", "session_id": conv, "message": map[string]any{
					"id": "m1", "type": "message", "role": "assistant", "model": "claude-sonnet-5",
					"content": content}}),
				jsonLine(map[string]any{"type": "result", "subtype": "success", "is_error": true,
					"result": errText, "session_id": conv}),
				"#!exit 1")
		},
		ok: func(conv, text string) string {
			return script(
				jsonLine(map[string]any{"type": "system", "subtype": "init", "session_id": conv,
					"model": "claude-sonnet-5"}),
				claudeText(conv, text),
				jsonLine(map[string]any{"type": "result", "subtype": "success", "is_error": false,
					"result": text, "session_id": conv}))
		},
		final: func(conv, text, errText string) string {
			return script(
				jsonLine(map[string]any{"type": "system", "subtype": "init", "session_id": conv,
					"model": "claude-sonnet-5"}),
				claudeText(conv, text),
				jsonLine(map[string]any{"type": "result", "subtype": "success", "is_error": true,
					"result": errText, "session_id": conv,
					"usage": map[string]any{"input_tokens": 1000, "output_tokens": 200}}),
				"#!exit 1")
		}}
}

func codexRun() staleRun {
	return staleRun{backend: config.BackendCodex, model: "gpt-6-luna", setup: (*harness).withCodex,
		failed: func(conv, text, errText string) string {
			lines := []string{jsonLine(map[string]any{"type": "thread.started", "thread_id": conv}),
				jsonLine(map[string]any{"type": "turn.started"})}
			if text != "" {
				lines = append(lines, jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{
					"id": "m", "type": "agent_message", "text": text}}))
			}
			lines = append(lines,
				jsonLine(map[string]any{"type": "item.started", "item": map[string]any{"id": "c",
					"type": "command_execution", "command": "go test ./...", "status": "in_progress"}}),
				jsonLine(map[string]any{"type": "turn.failed", "error": map[string]any{"message": errText}}),
				"#!exit 1")
			return script(lines...)
		},
		ok: codexOK,
		final: func(conv, text, errText string) string {
			return script(jsonLine(map[string]any{"type": "thread.started", "thread_id": conv}),
				jsonLine(map[string]any{"type": "turn.started"}),
				jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "m",
					"type": "agent_message", "text": text}}),
				jsonLine(map[string]any{"type": "turn.failed", "error": map[string]any{"message": errText}}), "#!exit 1")
		}}
}

func staleRuns() []staleRun { return []staleRun{agyRun(), claudeRun(), codexRun()} }

// staleErrorOf is the error text a backend's rules class as transient, in the CLI's own words: agy's carries the
// source port, claude's and codex's read the same on every outage.
func staleErrorOf(name string) string {
	switch name {
	case config.BackendCodex:
		return "stream disconnected before completion: error sending request for url " +
			"(https://api.example.test/v1/responses)"
	case config.BackendClaude:
		return "API Error: Connection error."
	}
	return staleErr
}

// agyReplayed is a resumed agy run that reports its conversation's error again: final, when set, is the agent's
// final answer of the run (DONE, no tool step after it); durSec is the duration agy reports, the whole
// conversation's on a replay.
func agyReplayed(conv, final string, durSec float64) string {
	lines := []string{agyInit(conv)}
	if final != "" {
		lines = append(lines, agyDelta(1, final), jsonLine(map[string]any{"event": "step_update",
			"step_update": map[string]any{"step_index": 1, "step_type": "agent_response", "state": "DONE"}}))
	} else {
		lines = append(lines, agyTool(1, "CALLING", "go test ./..."))
	}
	res := map[string]any{"status": "ERROR", "error": staleErr, "conversation_id": conv}
	if durSec > 0 {
		res["duration_seconds"] = durSec
	}
	return script(append(lines, jsonLine(map[string]any{"event": "result", "result": res}), "#!exit 0")...)
}

// startStale delegates a task whose first attempt fails with errText in conversation conv and waits for its retry.
func startStale(h *harness, b staleRun, conv string) api.TaskSummary {
	h.t.Helper()
	h.queue.Push(h.t, b.failed(conv, "", staleErrorOf(b.backend)))
	sum := h.delegate(api.DelegateRequest{Backend: b.backend, Model: b.model})
	h.waitEvent(sum.ID, api.EventRetry)
	return sum
}

// TestStaleReplayedErrorFails: a resumed agy attempt that reports the byte-identical error of the attempt before it
// in the same conversation, with the conversation's duration (555 s for a run of a moment), replays a stale error.
// Without a complete report (nothing, or a progress line) the turn fails at once with a message that says where the
// work may be: no third attempt and no handoff to another account that would redo the work.
func TestStaleReplayedErrorFails(t *testing.T) {
	for name, final := range map[string]string{"silent": "", "progress": "The tests are still running."} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			b := agyRun()
			b.setup(h)
			conv := "conv-stale-" + name
			sum := startStale(h, b, conv)
			h.queue.Push(t, agyReplayed(conv, final, 555))
			h.queue.Push(t, b.ok(conv, "must not run"))
			h.waitFor("the resumed attempt", func() bool {
				h.clk.Advance(5 * time.Second)
				return h.queue.Left(t) == 1
			})
			tk := h.waitStatus(sum.ID, StatusFailed)
			if !strings.Contains(tk.Error, "agy repeats a stale error from its conversation") ||
				!strings.Contains(tk.Error, "check the diff and delegate a new task") ||
				!strings.Contains(tk.Error, staleErr) {
				t.Errorf("error = %q", tk.Error)
			}
			at := tk.LastTurn().Attempts
			if len(at) != 2 || at[0].Status != AttemptTransient || at[1].Status != AttemptError || !at[1].Continuing ||
				at[1].ConversationID != conv {
				t.Fatalf("attempts = %+v", at)
			}
			h.clk.Advance(time.Minute)
			time.Sleep(100 * time.Millisecond)
			evs := h.events(sum.ID)
			if h.queue.Left(t) != 1 || len(eventsOf(evs, api.EventHandoff)) != 0 ||
				len(eventsOf(evs, api.EventRetry)) != 1 {
				t.Errorf("a third attempt or a handoff followed: %d scripts left, events %+v", h.queue.Left(t), evs)
			}
		})
	}
}

// TestStaleReplayedErrorWithReport: the same replay after the agent's complete report (it repeated it) is a
// success, with a warning the lead and the dashboard see.
func TestStaleReplayedErrorWithReport(t *testing.T) {
	h := newHarness(t)
	b := agyRun()
	b.setup(h)
	sum := startStale(h, b, "conv-stale-ok")
	h.queue.Push(t, agyReplayed("conv-stale-ok", fullReport, 612))
	h.waitFor("the resumed attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	tk := h.waitStatus(sum.ID, StatusDone)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[1].Status != AttemptOK || at[1].Outcome == nil ||
		!strings.Contains(at[1].Outcome.Warning, "repeated the error of its previous attempt") {
		t.Fatalf("attempts = %+v", at)
	}
	if tk.LastTurn().Response != fullReport {
		t.Errorf("response = %q", tk.LastTurn().Response)
	}
	var end api.AttemptEndData
	ends := eventsOf(h.events(sum.ID), api.EventAttemptEnd)
	decode(t, ends[len(ends)-1], &end)
	if end.Status != AttemptOK || end.Warning == "" {
		t.Errorf("attempt_end = %+v", end)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], staleErr) {
		t.Errorf("report warnings = %q, %v", rep.Warnings, err)
	}
}

// TestStaleReplayNeedsEvidence: the same error text in the same agy conversation without the conversation's duration
// (a real outage that went on), or in another conversation, is no replay: the retries go on as before.
func TestStaleReplayNeedsEvidence(t *testing.T) {
	for name, second := range map[string]string{
		"no-duration":        agyReplayed("conv-a", "", 0),
		"run-duration":       agyReplayed("conv-a", "", 5),
		"other-conversation": agyReplayed("conv-b", "", 555),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			b := agyRun()
			b.setup(h)
			sum := startStale(h, b, "conv-a")
			h.queue.Push(t, second)
			h.queue.Push(t, okScript("conv-a", "ok"))
			h.waitFor("the third attempt", func() bool {
				h.clk.Advance(5 * time.Second)
				return h.queue.Left(t) == 0
			})
			tk := h.waitStatus(sum.ID, StatusDone)
			if at := tk.LastTurn().Attempts; len(at) != 3 || at[1].Status != AttemptTransient {
				t.Fatalf("attempts = %+v", at)
			}
		})
	}
}

// TestRealOutageKeepsRetrying: claude and codex word a real outage the same way every time and do not replay
// errors. Identical errors in the same conversation, the later ones after a progress line, stay transient: the 45 s
// retry and the handoff to another account go on, with no success and no early failure.
func TestRealOutageKeepsRetrying(t *testing.T) {
	for _, b := range []staleRun{claudeRun(), codexRun()} {
		t.Run(b.backend, func(t *testing.T) {
			h := newHarness(t)
			b.setup(h)
			if _, _, err := h.accounts.Add(t.Context(), account.New{Backend: b.backend, Kind: account.KindIsolated,
				Label: "second", Enabled: true}, nil); err != nil {
				t.Fatal(err)
			}
			conv, errText := "conv-outage-"+b.backend, staleErrorOf(b.backend)
			progress := "I have started the full test suite and am waiting for it."
			sum := startStale(h, b, conv)
			h.queue.Push(t, b.final(conv, progress, errText))
			h.queue.Push(t, b.final(conv, progress, errText))
			h.queue.Push(t, b.ok("conv-other", "All done."))
			h.waitFor("the handoff", func() bool {
				h.clk.Advance(5 * time.Second)
				return h.queue.Left(t) == 0
			})
			tk := h.waitStatus(sum.ID, StatusDone)
			at := tk.LastTurn().Attempts
			if len(at) != 4 || at[1].Status != AttemptTransient || at[2].Status != AttemptTransient ||
				!at[1].Continuing || at[1].ConversationID != conv || at[3].Status != AttemptOK ||
				at[3].AccountID == at[0].AccountID {
				t.Fatalf("attempts = %+v", at)
			}
			evs := h.events(sum.ID)
			retries := eventsOf(evs, api.EventRetry)
			if len(retries) != 2 {
				t.Fatalf("retries = %+v", retries)
			}
			var second api.RetryData
			decode(t, retries[1], &second)
			if second.DelayMs != 45000 || len(eventsOf(evs, api.EventHandoff)) != 1 {
				t.Errorf("second retry = %+v, handoffs %d", second, len(eventsOf(evs, api.EventHandoff)))
			}
		})
	}
}

// TestStaleReplayRule: what counts as a replayed error: a backend that replays errors, a reported duration far
// beyond the run's wall time, the attempt resumed the conversation of the attempt before it, that one ended
// transient, the CLI itself reported the whole error text again byte for byte, and the process ended by itself.
func TestStaleReplayRule(t *testing.T) {
	turn := func() *Turn {
		return &Turn{N: 1, Attempts: []*Attempt{
			{N: 1, Status: AttemptTransient, ConversationID: "c1", Outcome: &backend.Outcome{Error: staleErr}},
			{N: 2, Status: AttemptRunning, Continuing: true, ConversationID: "c1"},
		}}
	}
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	exited := attemptResult{exit: launch.Exit{Reason: launch.ReasonExited, Code: 1, StartedAt: start,
		EndedAt: start.Add(15 * time.Second)}}
	same := backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassTransient, Error: staleErr,
		ConversationID: "c1", DurationMs: 555_000}
	if !staleReplay(turn(), 2, same, exited, true) {
		t.Fatal("the replay was not seen")
	}
	with := func(f func(o *backend.Outcome)) backend.Outcome {
		o := same
		f(&o)
		return o
	}
	for name, c := range map[string]struct {
		tu      func() *Turn
		o       backend.Outcome
		res     attemptResult
		replays bool
	}{
		"backend does not replay": {turn, same, exited, false},
		"no duration":             {turn, with(func(o *backend.Outcome) { o.DurationMs = 0 }), exited, true},
		"duration within slack":   {turn, with(func(o *backend.Outcome) { o.DurationMs = 40_000 }), exited, true},
		"no start time": {turn, same, attemptResult{exit: launch.Exit{Reason: launch.ReasonExited,
			EndedAt: start}}, true},
		"other port": {turn, with(func(o *backend.Outcome) {
			o.Error = strings.Replace(staleErr, "55383", "55384", 1)
		}), exited, true},
		"empty error": {func() *Turn {
			tu := turn()
			tu.Attempts[0].Outcome.Error = ""
			return tu
		}, with(func(o *backend.Outcome) { o.Error = "" }), exited, true},
		"other conversation reported": {turn, with(func(o *backend.Outcome) { o.ConversationID = "c2" }), exited,
			true},
		"other conversation resumed": {func() *Turn {
			tu := turn()
			tu.Attempts[1].ConversationID = "c2"
			return tu
		}, same, exited, true},
		"fresh conversation": {func() *Turn {
			tu := turn()
			tu.Attempts[1].Continuing = false
			return tu
		}, same, exited, true},
		"previous not transient": {func() *Turn {
			tu := turn()
			tu.Attempts[0].Status = AttemptError
			return tu
		}, same, exited, true},
		"first attempt": {func() *Turn { return &Turn{Attempts: turn().Attempts[1:]} }, same, exited, true},
		"launcher lost": {turn, same, attemptResult{exit: launch.Exit{Reason: launch.ReasonLost, StartedAt: start,
			EndedAt: start.Add(time.Second)}}, true},
		"interrupted": {turn, same, attemptResult{exit: exited.exit, interrupted: true}, true},
		"made up from stderr": {turn, same, attemptResult{exit: exited.exit, finish: []event.Event{{
			Kind: event.KindReport, Report: &event.Report{Status: event.StatusError, Error: staleErr,
				Synthesized: true}}}}, true},
	} {
		tu := c.tu()
		if staleReplay(tu, len(tu.Attempts), c.o, c.res, c.replays) {
			t.Errorf("%s: seen as a replay", name)
		}
	}
	if staleReplay(nil, 2, same, exited, true) {
		t.Error("nil turn")
	}
}

// TestCancelDuringRetryKeepsResponse: a cancel while the turn waits to retry ends the turn with the response and
// the files of the attempt that gave them, so the lead sees the work that is on disk.
func TestCancelDuringRetryKeepsResponse(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	file := filepath.Join(h.cwd, "store.go")
	h.queue.Push(t, script(agyInit("conv-cancel"), agyDelta(1, "Closed the rows in store.go."),
		"#!write "+file+"\t"+strconv.Quote("package store\n"), agyEdit(2, file),
		jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "ERROR",
			"error": staleErr, "conversation_id": "conv-cancel"}}), "#!exit 1"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitEvent(sum.ID, api.EventRetry)
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusCancelled)
	tu := tk.LastTurn()
	if tu.Status != TurnCancelled || tu.Response != "Closed the rows in store.go." ||
		!slices.Contains(tu.Files, "store.go") {
		t.Fatalf("turn = %+v", tu)
	}
	var end api.TurnEndData
	ends := eventsOf(h.events(sum.ID), api.EventTurnEnd)
	if len(ends) != 1 {
		t.Fatalf("turn_end events = %+v", ends)
	}
	decode(t, ends[0], &end)
	if end.Status != TurnCancelled || end.Response != "Closed the rows in store.go." ||
		!slices.Contains(end.Files, "store.go") {
		t.Errorf("turn_end = %+v", end)
	}
}

// TestResumedTurnListsEarlierEdits: a turn that ends ok on a resumed attempt lists the files the earlier attempts
// of the turn edited too, and a fresh conversation after them is told what they left.
func TestResumedTurnListsEarlierEdits(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	first, second := filepath.Join(h.cwd, "a.go"), filepath.Join(h.cwd, "b.go")
	h.queue.Push(t, script(agyInit("conv-files"), "#!write "+first+"\t"+strconv.Quote("package a\n"),
		agyEdit(1, first), jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "ERROR",
			"error": staleErr, "conversation_id": "conv-files"}}), "#!exit 1"))
	h.queue.Push(t, script(agyInit("conv-files"), "#!write "+second+"\t"+strconv.Quote("package b\n"),
		agyEdit(1, second), agyDelta(2, "Both done."), agyResult("conv-files", "Both done.")))
	sum := h.delegate(api.DelegateRequest{})
	h.waitEvent(sum.ID, api.EventRetry)
	h.waitFor("the resumed attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	tk := h.waitStatus(sum.ID, StatusDone)
	if tu := tk.LastTurn(); !slices.Contains(tu.Files, "a.go") || !slices.Contains(tu.Files, "b.go") {
		t.Fatalf("files = %q", tu.Files)
	}

	// The brief of a fresh conversation after those attempts names their files and the last report.
	tu := tk.LastTurn()
	tu.Attempts[1].Outcome.Response = "Both done."
	in := h.s.prompt(t.Context(), &Task{Cwd: h.cwd, Turns: []*Turn{tu}}, tu, false, false)
	if !strings.Contains(in, "Files it edited: a.go, b.go") || !strings.Contains(in, "Both done.") ||
		!strings.Contains(in, "Do not redo work that is already there.") {
		t.Errorf("brief:\n%s", in)
	}
}

// fullReport is a final answer in the shape the worker brief asks for.
const fullReport = "## Summary\nClosed the rows in store.go.\n## Files changed\nstore.go: close the rows\n" +
	"## Verification\ngo test ./internal/store/ passes\n## Open issues\nnone"

// TestRecoveredAfterFinalReport: the CLI reports a network error after the agent's complete report (no tool step
// after it). For every backend the attempt counts as done with that report and a warning, and its usage is
// recorded as usual.
func TestRecoveredAfterFinalReport(t *testing.T) {
	for _, b := range staleRuns() {
		t.Run(b.backend, func(t *testing.T) {
			h := newHarness(t)
			b.setup(h)
			h.queue.Push(t, b.final("conv-rec-"+b.backend, fullReport, staleErrorOf(b.backend)))
			h.queue.Push(t, b.ok("conv-rec-"+b.backend, "must not run"))
			sum := h.delegate(api.DelegateRequest{Backend: b.backend, Model: b.model})
			tk := h.waitStatus(sum.ID, StatusDone)
			at := tk.LastTurn().Attempts
			want := b.backend + " reported a network error after its final answer: " + staleErrorOf(b.backend)
			if len(at) != 1 || at[0].Status != AttemptOK || at[0].Error != "" || at[0].Outcome == nil ||
				at[0].Outcome.Warning != want {
				t.Fatalf("attempts = %+v", at)
			}
			if b.backend != config.BackendCodex && at[0].Usage == nil {
				t.Error("the recovered attempt recorded no usage")
			}
			if tk.LastTurn().Response != fullReport || h.queue.Left(t) != 1 {
				t.Errorf("response = %q, %d scripts left", tk.LastTurn().Response, h.queue.Left(t))
			}
			rep, err := h.s.Report(t.Context(), sum.ID, "")
			if err != nil || len(rep.Warnings) != 1 || rep.Warnings[0] != want {
				t.Errorf("report warnings = %q, %v", rep.Warnings, err)
			}
			var end api.AttemptEndData
			decode(t, eventsOf(h.events(sum.ID), api.EventAttemptEnd)[0], &end)
			if end.Status != AttemptOK || end.Warning != want {
				t.Errorf("attempt_end = %+v", end)
			}
		})
	}
}

// TestNoRecoveryWithoutReport: the same network error after a progress line, or after a final answer without the
// report's sections, stays a transient failure on every backend: the turn retries as before.
func TestNoRecoveryWithoutReport(t *testing.T) {
	for name, text := range map[string]string{
		"progress":   "I have initiated the full test suite and am waiting for it to finish.",
		"nosections": "## Report\nClosed the rows in store.go.",
	} {
		for _, b := range staleRuns() {
			t.Run(name+"-"+b.backend, func(t *testing.T) {
				h := newHarness(t)
				b.setup(h)
				h.queue.Push(t, b.final("conv-prog-"+b.backend, text, staleErrorOf(b.backend)))
				sum := h.delegate(api.DelegateRequest{Backend: b.backend, Model: b.model})
				evs := h.waitEvent(sum.ID, api.EventRetry)
				at := h.task(sum.ID).LastTurn().Attempts
				if len(at) != 1 || at[0].Status != AttemptTransient || at[0].Outcome == nil ||
					at[0].Outcome.Warning != "" {
					t.Fatalf("attempts = %+v", at)
				}
				var end api.AttemptEndData
				decode(t, eventsOf(evs, api.EventAttemptEnd)[0], &end)
				if end.Status != AttemptTransient || end.Warning != "" {
					t.Errorf("attempt_end = %+v", end)
				}
			})
		}
	}
}

// TestNoRecoveryOnBudgetStop: a cancel (a budget stop cancels the task the same way) while the worker runs never
// becomes a recovered attempt, whatever the worker wrote before.
func TestNoRecoveryOnBudgetStop(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, script(agyInit("conv-stop"), agyDelta(1, fullReport),
		jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": 1,
			"step_type": "agent_response", "state": "DONE"}}), "#!hang"))
	sum := h.delegate(api.DelegateRequest{})
	h.runningWorker(sum.ID)
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusCancelled)
	if at := tk.LastTurn().Attempts; len(at) != 1 || at[0].Status != AttemptCancelled || at[0].Outcome == nil ||
		at[0].Outcome.Warning != "" || at[0].Outcome.RecoveryAnswer != "" {
		t.Fatalf("attempts = %+v", at)
	}
}

// TestNoRecoveryAfterEarlierReport: agy's result response holds the whole run's text. A complete report, then more
// tool steps, then a progress line and a network error: the final answer is the progress line, so the attempt stays
// transient although the response has the report's sections.
func TestNoRecoveryAfterEarlierReport(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	progress := "The tests are running; I am waiting for them."
	done := func(step int) string {
		return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
			"step_type": "agent_response", "state": "DONE"}})
	}
	h.queue.Push(t, script(agyInit("conv-n1"), agyDelta(1, fullReport), done(1), agyTool(2, "DONE", "go test ./..."),
		agyDelta(3, progress), done(3),
		jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "ERROR", "error": staleErr,
			"response": fullReport + "\n" + progress, "conversation_id": "conv-n1"}}), "#!exit 0"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitEvent(sum.ID, api.EventRetry)
	at := h.task(sum.ID).LastTurn().Attempts
	if len(at) != 1 || at[0].Status != AttemptTransient || at[0].Outcome == nil || at[0].Outcome.Warning != "" ||
		at[0].Outcome.RecoveryFinal != progress {
		t.Fatalf("attempts = %+v", at)
	}
}
