package pool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
)

type waitResult struct {
	lease  *Lease
	choice Choice
	err    error
}

// startWait runs Wait in the background and records the reasons it reports.
func (f *fixture) startWait(ctx context.Context, req Request) (done <-chan waitResult, seen func() []*Unavailable) {
	var mu sync.Mutex
	var reasons []*Unavailable
	ch := make(chan waitResult, 1)
	go func() {
		l, c, err := f.p.Wait(ctx, req, func(u *Unavailable) {
			mu.Lock()
			reasons = append(reasons, u)
			mu.Unlock()
		})
		ch <- waitResult{l, c, err}
	}()
	return ch, func() []*Unavailable {
		mu.Lock()
		defer mu.Unlock()
		return append([]*Unavailable(nil), reasons...)
	}
}

func receive(t *testing.T, ch <-chan waitResult) waitResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return")
		return waitResult{}
	}
}

func (f *fixture) waitBlocked() {
	f.t.Helper()
	if !f.clk.WaitTimers(1, 5*time.Second) {
		f.t.Fatal("Wait did not start sleeping")
	}
}

func TestWaitWakesOnRelease(t *testing.T) {
	f := newFixture(t)
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount = 1 })
	held, _ := f.acquire(gemini("t1"))
	done, seen := f.startWait(t.Context(), gemini("t2"))
	f.waitBlocked()
	held.Release()
	r := receive(t, done)
	if r.err != nil || r.lease.AccountID() != a.ID || r.lease.TaskID() != "t2" {
		t.Fatalf("wait result %+v", r)
	}
	r.lease.Release()
	if s := seen(); len(s) != 1 || s[0].Reason != ReasonBusy {
		t.Fatalf("onWait calls %+v", s)
	}
}

func TestWaitSleepsUntilReset(t *testing.T) {
	f := newFixture(t)
	a := f.agy(0)
	reset := f.clk.Now().Add(time.Hour)
	f.read(a.ID, "agy", 0, reset)
	done, seen := f.startWait(t.Context(), gemini("t"))
	f.waitBlocked()
	// A Notify that changes nothing wakes it; it looks again and goes back to sleep without a second onWait.
	f.p.Notify()
	f.waitBlocked()
	f.clk.Advance(time.Hour)
	r := receive(t, done)
	if r.err != nil || r.choice.Remaining != 1 {
		t.Fatalf("wait result %+v", r)
	}
	r.lease.Release()
	if s := seen(); len(s) != 1 || s[0].Reason != ReasonResting || !s[0].Until.Equal(reset) {
		t.Fatalf("onWait calls %+v", s)
	}
}

func TestWaitCancelAndNoAccount(t *testing.T) {
	f := newFixture(t)
	// No account at all: Wait does not wait.
	_, _, err := f.p.Wait(t.Context(), gemini("t"), nil)
	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("no account: %v", err)
	}
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount = 1 })
	held, _ := f.acquire(gemini("t1"))
	defer held.Release()
	ctx, cancel := context.WithCancel(t.Context())
	done, _ := f.startWait(ctx, gemini("t2"))
	f.waitBlocked()
	cancel()
	r := receive(t, done)
	if !errors.Is(r.err, context.Canceled) || !errors.Is(r.err, ErrAllBusy) || r.lease != nil {
		t.Fatalf("canceled wait: %+v", r)
	}
	if f.p.Busy(a.ID) != 1 {
		t.Fatalf("canceled wait leaked a slot: busy %d", f.p.Busy(a.ID))
	}
	// Load errors end the wait.
	f.p.o.Accounts = failingAccounts{}
	if _, _, err := f.p.Wait(t.Context(), gemini("t3"), nil); err == nil || errors.Is(err, ErrNoAccount) {
		t.Fatalf("load error: %v", err)
	}
}

func TestWaitStep(t *testing.T) {
	now := t0
	tests := []struct {
		until time.Time
		want  time.Duration
	}{
		{time.Time{}, defaultWaitStep},
		{now.Add(time.Second), minWaitStep},
		{now.Add(-time.Hour), minWaitStep},
		{now.Add(2 * time.Minute), 2 * time.Minute},
		{now.Add(5 * time.Hour), maxWaitStep},
	}
	for _, tt := range tests {
		if got := waitStep(&Unavailable{Until: tt.until}, now); got != tt.want {
			t.Errorf("waitStep(%s) = %s, want %s", tt.until, got, tt.want)
		}
	}
}

func TestHandover(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.agy(0)
	b := f.agy(0)
	f.read(a.ID, "agy", 0.9, time.Time{})
	f.read(b.ID, "agy", 0.5, time.Time{})
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount = 1 })
	hand := func(cur string, class event.Class) Handover {
		t.Helper()
		h, err := f.p.Handover(ctx, HandoverInput{Current: cur, Class: class, Request: gemini("t")})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	if h := hand(a.ID, ""); h.Action != ActionStay {
		t.Fatalf("healthy account: %+v", h)
	}
	// Its own running turn does not make it leave.
	l, _ := f.acquire(Request{TaskID: "t", Backend: "agy", Group: "Gemini Models", Account: a.ID})
	if h := hand(a.ID, event.ClassTransient); h.Action != ActionStay {
		t.Fatalf("busy own account: %+v", h)
	}
	l.Release()
	if h := hand(a.ID, event.ClassAuthRequired); h.Action != ActionMove || h.To.Account.ID != b.ID {
		t.Fatalf("auth required: %+v", h)
	}
	if h := hand("acc404", ""); h.Action != ActionMove || h.To.Account.ID != a.ID {
		t.Fatalf("removed account: %+v", h)
	}
	// Resting: move to b; when b is busy, wait (and count a's rest).
	until := f.clk.Now().Add(2 * time.Hour)
	if _, err := f.p.MarkExhausted(ctx, a.ID, Exhaustion{Group: "Gemini Models", ResetAt: until}); err != nil {
		t.Fatal(err)
	}
	if h := hand(a.ID, event.ClassQuotaExhausted); h.Action != ActionMove || h.To.Account.ID != b.ID || h.Why == "" {
		t.Fatalf("resting: %+v", h)
	}
	lb, _ := f.acquire(Request{TaskID: "other", Backend: "agy", Group: "Gemini Models", Account: b.ID})
	h := hand(a.ID, event.ClassQuotaExhausted)
	if h.Action != ActionWait || h.Wait.Reason != ReasonBusy || h.Wait.Resting != 1 || !h.Wait.Until.Equal(until) {
		t.Fatalf("resting, other busy: %+v %+v", h, h.Wait)
	}
	lb.Release()
	f.disable(b.ID)
	h = hand(a.ID, event.ClassQuotaExhausted)
	if h.Action != ActionWait || h.Wait.Reason != ReasonResting || !h.Wait.Until.Equal(until) || h.Wait.Candidates != 1 {
		t.Fatalf("resting alone: %+v %+v", h, h.Wait)
	}
	// Disabled current account and nothing else: nothing can run the task.
	f.disable(a.ID)
	h = hand(a.ID, "")
	if h.Action != ActionWait || h.Wait.Reason != ReasonNoAccount {
		t.Fatalf("disabled alone: %+v %+v", h, h.Wait)
	}
	// A never-read current account waits for its first read.
	c := f.agy(0)
	h = hand(c.ID, "")
	if h.Action != ActionWait || h.Wait.Reason != ReasonResting {
		t.Fatalf("pending: %+v %+v", h, h.Wait)
	}
}
