package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/memory"
)

// TestDelegateDoneFollowupReview runs a task through a turn, a followup in the same conversation and a review.
func TestDelegateDoneFollowupReview(t *testing.T) {
	h := newHarness(t)
	acc := h.agy("main", 0)
	file := filepath.Join(h.cwd, "internal", "api", "handler.go")
	h.queue.Push(t, script(agyInit("conv-1"), agyDelta(1, "Hello "), agyDelta(1, "wor"), agyDelta(1, "ld."),
		agyEdit(2, file), agyTool(3, "CALLING", "go test ./..."), agyTool(3, "DONE", "go test ./..."),
		agyResult("conv-1", "Added the check.")))
	sum := h.delegate(api.DelegateRequest{Prompt: "Add a nil check.\nKeep it small."})
	if sum.Title != "Add a nil check." || sum.Backend != "agy" || sum.TurnCount != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if tu.Response != "Added the check." || !slices.Equal(tu.Files, []string{"internal/api/handler.go"}) ||
		tk.ConversationID != "conv-1" || tk.AccountID != acc.ID {
		t.Fatalf("task = %+v turn = %+v", tk, tu)
	}
	if a := tk.Attention; a == nil || a.State != api.AttentionInfo || a.Reason != ReasonReview {
		t.Fatalf("attention = %+v", tk.Attention)
	}
	if tk.NeedsSince.IsZero() {
		t.Fatal("needsSince not set for a finished, unreviewed task")
	}

	// The three message deltas of step 1 are one event (coalescing), and seq grows by one.
	evs := h.events(sum.ID)
	var messages []string
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
		if e.Type == api.EventWorker {
			var ev event.Event
			decode(t, e, &ev)
			if ev.Kind == event.KindMessage {
				messages = append(messages, ev.Text)
			}
		}
	}
	if !slices.Equal(messages, []string{"Hello world."}) {
		t.Fatalf("messages = %q", messages)
	}
	for _, typ := range []api.EventType{api.EventLeadMessage, api.EventRoute, api.EventWorkerPrompt,
		api.EventAttemptEnd, api.EventTurnEnd, api.EventAttention} {
		if len(eventsOf(evs, typ)) == 0 {
			t.Errorf("no %s event", typ)
		}
	}
	recs := h.queue.Records(t)
	if len(recs) != 1 || !slices.Contains(recs[0].Args, "-p") || !strings.Contains(strings.Join(recs[0].Args, " "),
		"Add a nil check.") {
		t.Fatalf("records = %+v", recs)
	}
	if !samePath(recs[0].Dir, h.cwd) {
		t.Errorf("worker ran in %s, want %s", recs[0].Dir, h.cwd)
	}
	prompt, err := h.s.Prompt(t.Context(), sum.ID, 1, 0)
	if err != nil || !strings.Contains(prompt, "Add a nil check.") {
		t.Fatalf("prompt = %q, %v", prompt, err)
	}
	h.mu.Lock()
	refreshed := slices.Clone(h.refreshes)
	h.mu.Unlock()
	if !slices.Equal(refreshed, []string{acc.ID}) {
		t.Errorf("quota refreshes = %v", refreshed)
	}

	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "done" || rep.Response != "Added the check." || rep.TokensUsed != 1200 ||
		!slices.Equal(rep.FilesChanged, []string{"internal/api/handler.go"}) || rep.Worker == "" ||
		rep.Dashboard == "" || len(rep.Attempts) != 0 || !slices.Equal(rep.CommandsRun, []string{"go test ./..."}) {
		t.Fatalf("report = %+v", rep)
	}

	// Followup: a second turn in the same conversation on the same account.
	h.queue.Push(t, okScript("conv-1", "Renamed it."))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Rename it."}); err != nil {
		t.Fatal(err)
	}
	tk = h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || tk.LastTurn().Response != "Renamed it." || !tk.LastTurn().Attempts[0].Continuing {
		t.Fatalf("turns = %+v", tk.Turns)
	}
	recs = h.queue.Records(t)
	if args := strings.Join(recs[1].Args, " "); !strings.Contains(args, "conv-1") {
		t.Errorf("followup did not resume the conversation: %v", recs[1].Args)
	}
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: " "}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty followup: %v", err)
	}

	// Review.
	if _, err := h.s.Review(t.Context(), sum.ID, api.ReviewRequest{Verdict: "maybe"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad verdict: %v", err)
	}
	rs, err := h.s.Review(t.Context(), sum.ID, api.ReviewRequest{Verdict: api.VerdictAccepted, Notes: "good"})
	if err != nil {
		t.Fatal(err)
	}
	if rs.Review == nil || rs.Review.Verdict != api.VerdictAccepted || rs.Attention != nil || rs.NeedsSince != "" {
		t.Fatalf("after review = %+v", rs)
	}
	hist, err := h.memory.History(t.Context(), h.cwd, memory.HistoryShown)
	if err != nil || len(hist) != 1 || !strings.Contains(hist[0], "accepted") {
		t.Fatalf("history = %q, %v", hist, err)
	}

	// Queries.
	d, err := h.s.Get(t.Context(), sum.ID)
	if err != nil || len(d.Turns) != 2 || d.Turns[0].Attempts[0].AccountID != acc.ID {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	page, err := h.s.List(t.Context(), Query{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != sum.ID {
		t.Fatalf("list = %+v, %v", page, err)
	}
	tail, err := h.s.Events(t.Context(), sum.ID, 0, 0, 3)
	if err != nil || len(tail.Items) != 3 || tail.Items[2].Seq != tail.LastSeq {
		t.Fatalf("tail = %+v, %v", tail, err)
	}
	if _, err := h.s.Events(t.Context(), sum.ID, 0, 5000, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("limit 5000: %v", err)
	}
	if got := h.s.LiveWorkers(); len(got) != 0 {
		t.Errorf("live workers after the end = %+v", got)
	}
}

func samePath(a, b string) bool {
	ca, err1 := filepath.EvalSymlinks(a)
	cb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}

// TestQuotaHandsOff moves a turn whose account ran out of quota to another account, with the full brief.
func TestQuotaHandsOff(t *testing.T) {
	h := newHarness(t)
	first := h.agy("first", 10)
	second := h.agy("second", 0)
	h.queue.Push(t, quotaScript("conv-q"))
	h.queue.Push(t, okScript("conv-2", "Done on the second."))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusDone)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[0].AccountID != first.ID || at[0].Status != AttemptQuota ||
		at[1].AccountID != second.ID || at[1].Status != AttemptOK || at[1].Continuing {
		t.Fatalf("attempts = %+v %+v", at[0], at[len(at)-1])
	}
	if tk.AccountID != second.ID || tk.ConversationID != "conv-2" {
		t.Fatalf("owner = %s %s", tk.AccountID, tk.ConversationID)
	}
	evs := h.events(sum.ID)
	if len(eventsOf(evs, api.EventQuotaExhausted)) != 1 || len(eventsOf(evs, api.EventHandoff)) != 1 {
		t.Fatalf("events = %v", types(evs))
	}
	// The second worker got the full brief, saying it continues an interrupted task (old B7).
	p, err := h.s.Prompt(t.Context(), sum.ID, 1, 2)
	if err != nil || !strings.Contains(p, "Fix the handler.") {
		t.Fatalf("second prompt = %q, %v", p, err)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil || len(rep.Attempts) != 2 || rep.Attempts[0].Result != AttemptQuota {
		t.Fatalf("report attempts = %+v, %v", rep.Attempts, err)
	}
}

func types(evs []api.TaskEvent) []api.EventType {
	out := make([]api.EventType, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

// TestAuthMovesOn moves a turn refused for its sign-in to the next account and never back to the first.
func TestAuthMovesOn(t *testing.T) {
	h := newHarness(t)
	first := h.agy("first", 10)
	second := h.agy("second", 0)
	h.queue.Push(t, authScript())
	h.queue.Push(t, okScript("conv-a", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusDone)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[0].AccountID != first.ID || at[0].Status != AttemptAuth || at[1].AccountID != second.ID {
		t.Fatalf("attempts = %+v", at)
	}
	if len(eventsOf(h.events(sum.ID), api.EventAuthFailed)) != 1 {
		t.Fatal("no auth_failed event")
	}
}

// TestAuthWithoutOtherAccountFails gives up when the only account needs a sign-in.
func TestAuthWithoutOtherAccountFails(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, authScript())
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusFailed)
	if !strings.Contains(tk.Error, "no other account") {
		t.Fatalf("error = %q", tk.Error)
	}
	if a := tk.Attention; a == nil || a.State != api.AttentionBlocked || a.Reason != ReasonFailed {
		t.Fatalf("attention = %+v", tk.Attention)
	}
}

// TestTransientRetriesAfterDelay retries on the same account after the first delay of the fake clock.
func TestTransientRetriesAfterDelay(t *testing.T) {
	h := newHarness(t)
	acc := h.agy("only", 0)
	h.queue.Push(t, transientScript())
	h.queue.Push(t, okScript("conv-t", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	evs := h.waitEvent(sum.ID, api.EventRetry)
	var rd api.RetryData
	decode(t, eventsOf(evs, api.EventRetry)[0], &rd)
	if rd.DelayMs != 15000 {
		t.Fatalf("retry = %+v", rd)
	}
	if h.queue.Left(t) != 1 {
		t.Fatal("retried before the delay")
	}
	h.waitFor("the second attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	tk := h.waitStatus(sum.ID, StatusDone)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[0].Status != AttemptTransient || at[1].AccountID != acc.ID || at[1].Status != AttemptOK {
		t.Fatalf("attempts = %+v", at)
	}
}

// TestBackgroundWaitContinuesOnce continues a turn whose CLI stopped on a background wait, once.
func TestBackgroundWaitContinuesOnce(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	bg := script(agyInit("conv-b"), agyDelta(1, "The dev server is running."),
		"#!stderr [agy] root agent idle; waiting up to 45m0s for 1 background task(s) to finish", "#!exit 0")
	h.queue.Push(t, bg)
	h.queue.Push(t, bg)
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusDone)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[0].Status != AttemptBackgroundWait || at[1].Status != AttemptBackgroundWait {
		t.Fatalf("attempts = %+v", at)
	}
	if p, err := h.s.Prompt(t.Context(), sum.ID, 1, 2); err != nil || p != brief.BackgroundWait {
		t.Fatalf("second prompt = %q, %v", p, err)
	}
}

// TestFatalFails ends the task as failed on an error that is not about the account.
func TestFatalFails(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, fatalScript("conv-f"))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusFailed)
	if tk.Error == "" || tk.LastTurn().Status != TurnFailed {
		t.Fatalf("task = %+v", tk)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil || rep.Error == "" || len(rep.Attempts) != 1 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

// TestCancelStopsWorker cancels a running worker through its launcher and waits for it (old B1).
func TestCancelStopsWorker(t *testing.T) {
	h := newHarness(t)
	acc := h.agy("only", 0)
	h.queue.Push(t, hangScript("conv-c"))
	sum := h.delegate(api.DelegateRequest{})
	h.runningWorker(sum.ID)
	live := h.s.LiveWorkers()
	if len(live) != 1 || live[0].TaskID != sum.ID || live[0].AccountID != acc.ID || live[0].LaunchDir == "" {
		t.Fatalf("live workers = %+v", live)
	}
	running, err := h.s.Summary(t.Context(), sum.ID)
	if err != nil || running.ActiveStep == nil {
		t.Fatalf("running summary = %+v, %v", running, err)
	}
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "more"}); !errors.Is(err, ErrBadState) {
		t.Errorf("followup while running: %v", err)
	}
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 99}); !errors.Is(err, ErrInvalid) {
		t.Errorf("waitSeconds 99: %v", err)
	}
	cs, stopped, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 60})
	if err != nil || !stopped || cs.Status != StatusCancelled {
		t.Fatalf("cancel = %+v %v %v", cs, stopped, err)
	}
	tk := h.task(sum.ID)
	if tk.LastTurn().Status != TurnCancelled || tk.LastTurn().Attempts[0].Status != AttemptCancelled ||
		tk.Attention != nil {
		t.Fatalf("task = %+v", tk.LastTurn())
	}
	if len(h.s.LiveWorkers()) != 0 {
		t.Fatal("worker still followed")
	}
	// Cancelling a finished task changes nothing.
	if _, stopped, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{}); err != nil || !stopped {
		t.Fatalf("second cancel: %v %v", stopped, err)
	}
}

// TestWaitCancelLeavesTask ends a wait by its context without touching the task (old C-B4).
func TestWaitCancelLeavesTask(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, hangScript("conv-w"))
	sum := h.delegate(api.DelegateRequest{})
	h.runningWorker(sum.ID)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan api.WaitResult, 1)
	go func() {
		res, err := h.s.Wait(ctx, api.WaitRequest{IDs: []string{sum.ID}, TimeoutSeconds: 600}, "")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	res := <-done
	if res.Woke != api.WokeCancelled || res.Done {
		t.Fatalf("wait = %+v", res)
	}
	time.Sleep(100 * time.Millisecond)
	if tk := h.task(sum.ID); tk.Status != StatusRunning || tk.CancelRequested || !h.s.live(sum.ID).running {
		t.Fatalf("the wait's cancel touched the task: %s", tk.Status)
	}
	if _, err := h.s.Wait(t.Context(), api.WaitRequest{IDs: []string{"nope"}}, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := h.s.Wait(t.Context(), api.WaitRequest{IDs: []string{sum.ID}, TimeoutSeconds: 4000}, ""); !errors.Is(err,
		ErrInvalid) {
		t.Errorf("timeout 4000: %v", err)
	}
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 60}); err != nil {
		t.Fatal(err)
	}
}

// TestWaitWakesOnStallAndDone flags a quiet worker as stalled (never killing it), which wakes a wait; the next
// wait ends when the task is done.
func TestWaitWakesOnStallAndDone(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	release := filepath.Join(h.dir, "release")
	h.queue.Push(t, script(agyInit("conv-s"), agyTool(1, "CALLING", "npm test"), "#!wait-file "+release,
		agyTool(1, "DONE", "npm test"), agyResult("conv-s", "Tests pass.")))
	sum := h.delegate(api.DelegateRequest{})
	h.waitFor("the open tool call", func() bool { return h.s.live(sum.ID).tool != "" })
	done := make(chan api.WaitResult, 1)
	go func() {
		res, err := h.s.Wait(t.Context(), api.WaitRequest{IDs: []string{sum.ID}, TimeoutSeconds: 3000, Reports: true}, "")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	time.Sleep(50 * time.Millisecond)
	h.clk.Advance(time.Duration(h.config().StallToolMinutes+1) * time.Minute)
	h.s.refreshAttention(sum.ID)
	res := <-done
	if res.Woke != api.WokeBlocked || res.Done || res.Tasks[0].Attention == nil ||
		res.Tasks[0].Attention.State != api.AttentionStalled || len(res.Reports) != 1 {
		t.Fatalf("wait = %+v", res)
	}
	if tk := h.task(sum.ID); tk.Status != StatusRunning {
		t.Fatalf("a stalled task changed status: %s", tk.Status)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := h.s.Wait(t.Context(), api.WaitRequest{IDs: []string{sum.ID}, TimeoutSeconds: 3000}, "")
	if err != nil || !res.Done || res.Woke != api.WokeDone || res.Tasks[0].Status != StatusDone {
		t.Fatalf("second wait = %+v, %v", res, err)
	}
	// A wait on finished tasks returns at once; the timeout of the fake clock ends a wait on a pending one.
	res, err = h.s.Wait(t.Context(), api.WaitRequest{IDs: []string{sum.ID}, TimeoutSeconds: 0, Any: true}, "")
	if err != nil || !res.Done {
		t.Fatalf("third wait = %+v, %v", res, err)
	}
}

// TestDependencies starts a dependent when its prerequisite is accepted and blocks one whose prerequisite was
// rejected.
func TestDependencies(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-a", "A is done."))
	a := h.delegate(api.DelegateRequest{Prompt: "Task A"})
	b := h.delegate(api.DelegateRequest{Prompt: "Task B", After: []string{a.ID}})
	c := h.delegate(api.DelegateRequest{Prompt: "Task C", After: []string{a.ID}, AfterMode: AfterDone})
	if b.Status != StatusWaitingDeps || len(b.After) != 1 {
		t.Fatalf("b = %+v", b)
	}
	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, After: []string{"nope"}}); !errors.Is(
		err, ErrInvalid) {
		t.Errorf("unknown prerequisite: %v", err)
	}
	h.queue.Push(t, okScript("conv-c", "C is done."))
	h.waitStatus(a.ID, StatusDone)
	h.waitStatus(c.ID, StatusDone) // afterMode done: starts when A is done
	if tk := h.task(b.ID); tk.Status != StatusWaitingDeps {
		t.Fatalf("b started before the review: %s", tk.Status)
	}
	h.queue.Push(t, okScript("conv-b", "B is done."))
	if _, err := h.s.Review(t.Context(), a.ID, api.ReviewRequest{Verdict: api.VerdictAccepted}); err != nil {
		t.Fatal(err)
	}
	tb := h.waitStatus(b.ID, StatusDone)
	if !strings.Contains(tb.DepsContext, "A is done.") {
		t.Fatalf("deps context = %q", tb.DepsContext)
	}
	if p, _ := h.s.Prompt(t.Context(), b.ID, 1, 1); !strings.Contains(p, "A is done.") {
		t.Fatalf("b's brief lacks the prerequisite: %q", p)
	}

	// A rejected prerequisite blocks; force starts anyway with the extra instructions.
	h.queue.Push(t, okScript("conv-d", "D is done."))
	d := h.delegate(api.DelegateRequest{Prompt: "Task D"})
	e := h.delegate(api.DelegateRequest{Prompt: "Task E", After: []string{d.ID}})
	h.waitStatus(d.ID, StatusDone)
	if _, err := h.s.Review(t.Context(), d.ID, api.ReviewRequest{Verdict: api.VerdictRejected}); err != nil {
		t.Fatal(err)
	}
	te := h.waitStatus(e.ID, StatusBlockedDeps)
	if a := te.Attention; a == nil || a.Reason != ReasonDepsBlocked {
		t.Fatalf("attention = %+v", te.Attention)
	}
	if _, err := h.s.Followup(t.Context(), e.ID, api.FollowupRequest{Message: "go"}); !errors.Is(err, ErrBadState) {
		t.Errorf("followup without force: %v", err)
	}
	h.queue.Push(t, okScript("conv-e", "E is done."))
	if _, err := h.s.Followup(t.Context(), e.ID, api.FollowupRequest{Message: "Do it anyway.", Force: true}); err != nil {
		t.Fatal(err)
	}
	te = h.waitStatus(e.ID, StatusDone)
	if !strings.Contains(te.Turns[0].Message, "Do it anyway.") || len(te.Turns) != 1 {
		t.Fatalf("turns = %+v", te.Turns[0])
	}

	// Cancelling a waiting dependent is immediate.
	f := h.delegate(api.DelegateRequest{Prompt: "Task F", After: []string{e.ID, d.ID}})
	if _, stopped, err := h.s.Cancel(t.Context(), f.ID, api.CancelRequest{}); err != nil || !stopped {
		t.Fatalf("cancel waiting: %v %v", stopped, err)
	}
}

// TestNoAccountWaitsAndBlocks shows a task no account can run as waiting with a blocked flag for the user, and
// starts it once an account is added.
func TestNoAccountWaitsAndBlocks(t *testing.T) {
	h := newHarness(t)
	sum := h.delegate(api.DelegateRequest{})
	h.waitFor("waiting_quota", func() bool { return h.task(sum.ID).Status == StatusWaitingQuota })
	tk := h.task(sum.ID)
	if tk.Waiting != "no_account" || tk.Attention == nil || tk.Attention.Reason != ReasonNoAccounts ||
		tk.Attention.Who != WhoUser {
		t.Fatalf("task = %s %s %+v", tk.Status, tk.Waiting, tk.Attention)
	}
	h.queue.Push(t, okScript("conv-n", "ok"))
	h.agy("late", 0)
	h.waitFor("the task to start", func() bool {
		h.clk.Advance(noAccountRecheck)
		return h.task(sum.ID).Status == StatusDone
	})
}

// TestAnswerQuestion turns a "## Blocked" report into a question that the user answers with a new turn.
func TestAnswerQuestion(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-q", "## Blocked\n\nWhich database?\n\n- SQLite\n- Postgres"))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusDone)
	if a := tk.Attention; a == nil || a.State != api.AttentionQuestion || a.Detail != "Which database?" {
		t.Fatalf("attention = %+v", tk.Attention)
	}
	rep, _ := h.s.Report(t.Context(), sum.ID, "")
	if rep.Question == nil || !slices.Equal(rep.Question.Options, []string{"SQLite", "Postgres"}) ||
		rep.Attention == nil {
		t.Fatalf("report question = %+v", rep.Question)
	}
	h.queue.Push(t, okScript("conv-q", "Used SQLite."))
	if _, err := h.s.Answer(t.Context(), sum.ID, api.AnswerRequest{Answer: "SQLite"}); err != nil {
		t.Fatal(err)
	}
	tk = h.waitStatus(sum.ID, StatusDone)
	if tu := tk.LastTurn(); tu.By != ByUser || tu.Response != "Used SQLite." {
		t.Fatalf("turn = %+v", tu)
	}
	if _, err := h.s.Answer(t.Context(), sum.ID, api.AnswerRequest{Answer: "again"}); !errors.Is(err, ErrBadState) {
		t.Errorf("answer without a question: %v", err)
	}
	rep, _ = h.s.Report(t.Context(), sum.ID, "")
	if rep.AnsweredBy != ByUser {
		t.Errorf("answeredBy = %q", rep.AnsweredBy)
	}
	// Dismissing hides the flag until something new happens.
	if ds, err := h.s.DismissAttention(t.Context(), sum.ID); err != nil || ds.Attention != nil {
		t.Fatalf("dismiss = %+v, %v", ds.Attention, err)
	}
}
