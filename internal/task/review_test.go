package task

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// TestFollowupResentAfterEarlyTransient retries a followup whose first attempt failed before the worker reported a
// session with the followup itself, not the bare retry prompt that would drop the lead's message.
func TestFollowupResentAfterEarlyTransient(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-1", "first done"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	h.queue.Push(t, transientScript())
	h.queue.Push(t, okScript("conv-1", "tests added"))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Now add the tests XYZZY."}); err != nil {
		t.Fatal(err)
	}
	h.waitEvent(sum.ID, api.EventRetry)
	h.waitFor("the second attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	h.waitStatus(sum.ID, StatusDone)
	p, err := h.s.Prompt(t.Context(), sum.ID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p == brief.Retry || !strings.Contains(p, "XYZZY") || strings.Contains(p, brief.Retry) {
		t.Fatalf("retry prompt = %q", p)
	}
}

// TestFollowupRetryCarriesRequest retries a followup whose failed attempt had already reported a session with the
// retry prompt plus the followup, for the case the request never reached the worker.
func TestFollowupRetryCarriesRequest(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-2", "first done"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	h.queue.Push(t, script(agyInit("conv-2"), "#!stderr Error: 503 UNAVAILABLE: the service is busy", "#!exit 1"))
	h.queue.Push(t, okScript("conv-2", "tests added"))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Now add the tests PLUGH."}); err != nil {
		t.Fatal(err)
	}
	h.waitEvent(sum.ID, api.EventRetry)
	h.waitFor("the second attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	h.waitStatus(sum.ID, StatusDone)
	p, err := h.s.Prompt(t.Context(), sum.ID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, brief.Retry) || !strings.Contains(p, "PLUGH") {
		t.Fatalf("retry prompt = %q", p)
	}
}

// TestShutdownDuringLaunchKeepsTask stops the daemon while a worker launch is starting: the task is not cancelled,
// and the next start recovers it to done.
func TestShutdownDuringLaunchKeepsTask(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-s", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	caught := false
	deadline := time.Now().Add(30 * time.Second)
	for !caught && time.Now().Before(deadline) {
		if a := lastOpenAttempt(h.task(sum.ID)); a != nil && a.LaunchDir != "" {
			f := launch.FilesIn(a.LaunchDir)
			if _, err := os.Stat(f.Spec); err == nil {
				if _, err := os.Stat(f.Ready); err != nil {
					caught = true
				}
			}
		}
		if !caught {
			time.Sleep(time.Millisecond)
		}
	}
	if !caught {
		t.Skip("did not catch the launch window")
	}
	h.stop()
	if tk := h.task(sum.ID); tk.Status == StatusCancelled || tk.LastTurn().Status == TurnCancelled {
		t.Fatalf("after the shutdown: task %s, turn %s", tk.Status, tk.LastTurn().Status)
	}
	h.start()
	if tk := h.waitStatus(sum.ID, StatusDone); tk.LastTurn().Response != "ok" {
		t.Fatalf("turn = %+v", tk.LastTurn())
	}
}

// TestCancelledTakeoverFreesSlot cancels a task whose takeover console never reports an exit: the account slot is
// released at once, so another task can run on the only account.
func TestCancelledTakeoverFreesSlot(t *testing.T) {
	h := newHarness(t)
	set := h.config()
	set.MaxConcurrentPerAccount = 1
	h.settings.Store(&set)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-a", "a done"))
	a := h.delegate(api.DelegateRequest{})
	h.waitStatus(a.ID, StatusDone)
	if _, err := h.s.Takeover(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Cancel(t.Context(), a.ID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-b", "b done"))
	b := h.delegate(api.DelegateRequest{Prompt: "Another task."})
	h.waitStatus(b.ID, StatusDone)
	h.consoles.exit(0, 0)
}
