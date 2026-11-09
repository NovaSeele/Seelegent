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

// staleErrorOf is the error text a backend's rules class as transient.
func staleErrorOf(name string) string {
	if name == config.BackendCodex {
		return "stream disconnected before completion: error sending request for url " +
			"(https://api.example.test/v1/responses) from 10.0.0.2:55383"
	}
	return staleErr
}

// startStale delegates a task whose first attempt fails with errText in conversation conv and waits for its retry.
func startStale(h *harness, b staleRun, conv string) api.TaskSummary {
	h.t.Helper()
	h.queue.Push(h.t, b.failed(conv, "", staleErrorOf(b.backend)))
	sum := h.delegate(api.DelegateRequest{Backend: b.backend, Model: b.model})
	h.waitEvent(sum.ID, api.EventRetry)
	return sum
}

// TestStaleReplayedErrorFails: a resumed attempt that reports the byte-identical error of the attempt before it in
// the same conversation, with no response, replays a stale error. The turn fails at once with a message that says
// where the work may be: no third attempt and no handoff to another account that would redo the work. Every
// backend the same way.
func TestStaleReplayedErrorFails(t *testing.T) {
	for _, b := range staleRuns() {
		t.Run(b.backend, func(t *testing.T) {
			h := newHarness(t)
			b.setup(h)
			conv := "conv-stale-" + b.backend
			sum := startStale(h, b, conv)
			h.queue.Push(t, b.failed(conv, "", staleErrorOf(b.backend)))
			h.queue.Push(t, b.ok(conv, "must not run"))
			h.waitFor("the resumed attempt", func() bool {
				h.clk.Advance(5 * time.Second)
				return h.queue.Left(t) == 1
			})
			tk := h.waitStatus(sum.ID, StatusFailed)
			if !strings.Contains(tk.Error, b.backend+" repeats a stale error from its conversation") ||
				!strings.Contains(tk.Error, "check the diff and delegate a new task") ||
				!strings.Contains(tk.Error, staleErrorOf(b.backend)) {
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

// TestStaleReplayedErrorWithResponse: the same replay with the worker's response (it repeated its report) is a
// success, with a warning the lead and the dashboard see.
func TestStaleReplayedErrorWithResponse(t *testing.T) {
	for _, b := range staleRuns() {
		t.Run(b.backend, func(t *testing.T) {
			h := newHarness(t)
			b.setup(h)
			conv := "conv-stale-ok-" + b.backend
			sum := startStale(h, b, conv)
			h.queue.Push(t, b.failed(conv, "## Report\nDone already.", staleErrorOf(b.backend)))
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
			if !strings.Contains(tk.LastTurn().Response, "Done already.") {
				t.Errorf("response = %q", tk.LastTurn().Response)
			}
			var end api.AttemptEndData
			ends := eventsOf(h.events(sum.ID), api.EventAttemptEnd)
			decode(t, ends[len(ends)-1], &end)
			if end.Status != AttemptOK || end.Warning == "" {
				t.Errorf("attempt_end = %+v", end)
			}
			rep, err := h.s.Report(t.Context(), sum.ID, "")
			if err != nil || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], staleErrorOf(b.backend)) {
				t.Errorf("report warnings = %q, %v", rep.Warnings, err)
			}
		})
	}
}

// TestStaleReplayNeedsSameConversation: a resumed attempt whose CLI reports another conversation is no replay,
// even with the same error text: the retries go on as before.
func TestStaleReplayNeedsSameConversation(t *testing.T) {
	h := newHarness(t)
	b := agyRun()
	b.setup(h)
	sum := startStale(h, b, "conv-a")
	h.queue.Push(t, b.failed("conv-b", "", staleErr))
	h.queue.Push(t, okScript("conv-b", "ok"))
	h.waitFor("the third attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	tk := h.waitStatus(sum.ID, StatusDone)
	if at := tk.LastTurn().Attempts; len(at) != 3 || at[1].Status != AttemptTransient {
		t.Fatalf("attempts = %+v", at)
	}
}

// TestStaleReplayRule: what counts as a replayed error, for any backend: the attempt resumed the conversation of
// the attempt before it, that one ended transient, the CLI itself reported the whole error text again byte for
// byte, and the process ended by itself.
func TestStaleReplayRule(t *testing.T) {
	turn := func() *Turn {
		return &Turn{N: 1, Attempts: []*Attempt{
			{N: 1, Status: AttemptTransient, ConversationID: "c1", Outcome: &backend.Outcome{Error: staleErr}},
			{N: 2, Status: AttemptRunning, Continuing: true, ConversationID: "c1"},
		}}
	}
	exited := attemptResult{exit: launch.Exit{Reason: launch.ReasonExited, Code: 1}}
	same := backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassTransient, Error: staleErr,
		ConversationID: "c1"}
	if !staleReplay(turn(), 2, same, exited) {
		t.Fatal("the replay was not seen")
	}
	for name, c := range map[string]struct {
		tu  func() *Turn
		o   backend.Outcome
		res attemptResult
	}{
		"other port": {turn, backend.Outcome{Error: strings.Replace(staleErr, "55383", "55384", 1)}, exited},
		"empty error": {func() *Turn {
			tu := turn()
			tu.Attempts[0].Outcome.Error = ""
			return tu
		}, backend.Outcome{}, exited},
		"other conversation reported": {turn, backend.Outcome{Error: staleErr, ConversationID: "c2"}, exited},
		"other conversation resumed": {func() *Turn {
			tu := turn()
			tu.Attempts[1].ConversationID = "c2"
			return tu
		}, same, exited},
		"fresh conversation": {func() *Turn {
			tu := turn()
			tu.Attempts[1].Continuing = false
			return tu
		}, same, exited},
		"previous not transient": {func() *Turn {
			tu := turn()
			tu.Attempts[0].Status = AttemptError
			return tu
		}, same, exited},
		"first attempt": {func() *Turn { return &Turn{Attempts: turn().Attempts[1:]} }, same, exited},
		"launcher lost": {turn, same, attemptResult{exit: launch.Exit{Reason: launch.ReasonLost}}},
		"interrupted":   {turn, same, attemptResult{exit: exited.exit, interrupted: true}},
		"made up from stderr": {turn, same, attemptResult{exit: exited.exit, finish: []event.Event{{
			Kind: event.KindReport, Report: &event.Report{Status: event.StatusError, Error: staleErr,
				Synthesized: true}}}}},
	} {
		tu := c.tu()
		if staleReplay(tu, len(tu.Attempts), c.o, c.res) {
			t.Errorf("%s: seen as a replay", name)
		}
	}
	if staleReplay(nil, 2, same, exited) {
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
