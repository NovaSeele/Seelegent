package quota

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
)

// runFixture starts Run on a refresher with a 10 minute interval and no jitter, and returns a helper that waits
// for n reads of account a.
func runFixture(t *testing.T, busy func(context.Context) bool) (*refFixture, account.Account, func(n int)) {
	t.Helper()
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) { return okQuota(0.5), nil })
	f.r.o.Interval = func() time.Duration { return 10 * time.Minute }
	f.r.o.Jitter = func(n int64) int64 { return n / 2 } // no jitter
	f.r.o.Busy = busy
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.r.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitReads := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for f.reader.count(a.ID) < n {
			if time.Now().After(deadline) {
				t.Fatalf("reads = %d, want %d", f.reader.count(a.ID), n)
			}
			time.Sleep(time.Millisecond)
		}
		if !f.clk.WaitTimers(1, 5*time.Second) {
			t.Fatal("no timer after the read")
		}
	}
	return f, a, waitReads
}

// step advances the clock by d and checks that no read came (Run looks again and re-arms its timer).
func step(t *testing.T, f *refFixture, a account.Account, d time.Duration) {
	t.Helper()
	before := f.reader.count(a.ID)
	f.clk.Advance(d)
	time.Sleep(20 * time.Millisecond)
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no timer")
	}
	if n := f.reader.count(a.ID); n != before {
		t.Fatalf("reads = %d after %s, want %d (stretched)", n, d, before)
	}
}

// The quota idle policy: with nothing busy and no call for IdleAfter, the cycles stretch from 10 to 60 minutes;
// a call during the stretch reads at once; a busy daemon keeps the interval whatever the last call.
func TestIdleStretchesCycles(t *testing.T) {
	var busy atomic.Bool
	f, a, waitReads := runFixture(t, func(context.Context) bool { return busy.Load() })
	waitReads(1) // t0
	f.clk.Advance(10 * time.Minute)
	waitReads(2) // t0+10: the last call (the start) is 10 min old
	f.clk.Advance(10 * time.Minute)
	waitReads(3) // t0+20
	for range 5 {
		step(t, f, a, 10*time.Minute) // t0+30 .. t0+70: idle, held back
	}
	f.clk.Advance(10 * time.Minute)
	waitReads(4) // t0+80: 60 min after the last cycle

	// A call while a due cycle is held back reads at once: the quota it sees is never older than the interval.
	step(t, f, a, 10*time.Minute) // t0+90: due, idle, held back
	f.clk.Advance(5 * time.Minute)
	f.r.Touch() // t0+95
	waitReads(5)

	// A call when no cycle is held back changes nothing; the interval runs on.
	f.clk.Advance(2 * time.Minute)
	f.r.Touch()
	step(t, f, a, 7*time.Minute) // t0+104
	f.clk.Advance(time.Minute)
	waitReads(6) // t0+105

	// Busy (a pending task, a dashboard) keeps the interval long after the last call.
	busy.Store(true)
	for i := range 4 {
		f.clk.Advance(10 * time.Minute)
		waitReads(7 + i) // t0+115 .. t0+145
	}
	busy.Store(false)
	step(t, f, a, 10*time.Minute) // t0+155: idle again
	// Busy again during the stretch: seen at the next look (every interval).
	busy.Store(true)
	f.clk.Advance(10 * time.Minute)
	waitReads(11) // t0+165
}

// Without Busy the policy is off: the cycles keep the interval however long nobody calls.
func TestIdlePolicyOffWithoutBusy(t *testing.T) {
	f, _, waitReads := runFixture(t, nil)
	waitReads(1)
	for i := range 8 {
		f.clk.Advance(10 * time.Minute)
		waitReads(2 + i)
	}
}

// A run that went well reads the account's quota only when it is older than AfterRunFresh; a failed run always
// reads it, and so does a run on an account whose last read failed.
func TestAfterRunSkipsFreshQuota(t *testing.T) {
	var fail atomic.Bool
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		if fail.Load() {
			return backend.Quota{}, ErrUnavailable
		}
		return okQuota(0.5), nil
	})
	f.r.o.Interval = func() time.Duration { return time.Hour }
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.r.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	waitReads := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for f.reader.count(a.ID) < n {
			if time.Now().After(deadline) {
				t.Fatalf("reads = %d, want %d", f.reader.count(a.ID), n)
			}
			time.Sleep(time.Millisecond)
		}
	}
	quiet := func(want int) {
		t.Helper()
		time.Sleep(50 * time.Millisecond)
		if n := f.reader.count(a.ID); n != want {
			t.Fatalf("reads = %d, want %d", n, want)
		}
	}
	waitReads(1) // the first cycle
	f.clk.Advance(time.Minute)
	f.r.AfterRun(a.ID, false)
	quiet(1) // one minute old: kept
	f.r.AfterRun(a.ID, true)
	waitReads(2) // a failed run reads whatever the age
	f.clk.Advance(AfterRunFresh)
	f.r.AfterRun(a.ID, false)
	waitReads(3) // two minutes old: read

	fail.Store(true)
	f.r.Request(a.ID)
	waitReads(4) // this read fails
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, _, err := f.store.Get(ctx, a.ID)
		if err == nil && snap.Error != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the failed read was not stored")
		}
		time.Sleep(time.Millisecond)
	}
	f.r.AfterRun(a.ID, false)
	waitReads(5) // the cache holds an error: read even though the last good read is recent
}
