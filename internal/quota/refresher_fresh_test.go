package quota

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
)

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A read that started before a sign-in answers "signed out"; a Refresh asked for after the sign-in must not take
// that answer (review finding B).
func TestRefreshAfterSignInDoesNotJoinStaleRead(t *testing.T) {
	var signedIn atomic.Bool
	release := make(chan struct{})
	var first sync.Once
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		stale := false
		first.Do(func() { stale = true })
		if stale {
			<-release
			return backend.Quota{}, ErrNeedsLogin // what the CLI said before the sign-in
		}
		if !signedIn.Load() {
			return backend.Quota{}, ErrNeedsLogin
		}
		return okQuota(0.7), nil
	})
	ctx := t.Context()
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	cycle := make(chan []Result, 1)
	go func() { cycle <- f.r.RefreshAll(ctx) }()
	waitFor(t, "the first read", func() bool { return f.reader.count(a.ID) == 1 })

	signedIn.Store(true)
	type answer struct {
		snap Snapshot
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		snap, err := f.r.Refresh(ctx, a.ID)
		got <- answer{snap, err}
	}()
	waitFor(t, "Refresh waiting for the stale read", func() bool { return f.waiting.Load() == 1 })
	close(release)

	if res := <-cycle; len(res) != 1 || !errors.Is(res[0].Err, ErrNeedsLogin) {
		t.Fatalf("cycle = %+v", res)
	}
	ans := <-got
	if ans.err != nil || ans.snap.Error != nil || !ans.snap.Known() {
		t.Fatalf("Refresh after sign-in = %+v, %v", ans.snap, ans.err)
	}
	if n := f.reader.count(a.ID); n != 2 {
		t.Fatalf("reads = %d, want 2", n)
	}
	snap, _, err := f.store.Get(ctx, a.ID)
	if err != nil || snap.ErrorKind() != "" {
		t.Fatalf("stored = %+v, %v", snap, err)
	}
	for i := range 3 {
		if res := f.r.RefreshAll(ctx); len(res) != 1 || res[0].Err != nil {
			t.Fatalf("cycle %d = %+v", i, res)
		}
	}
	if n := f.reader.count(a.ID); n != 5 {
		t.Fatalf("reads after 3 cycles = %d, want 5", n)
	}
}

// Reads of one account never overlap; Refresh calls that arrive during an older read share the next one.
func TestRefreshWaitsForOlderReadAndSharesNewer(t *testing.T) {
	var active, most atomic.Int32
	release := make(chan struct{})
	var first sync.Once
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		block := false
		first.Do(func() { block = true })
		if block {
			<-release
		}
		return okQuota(0.5), nil
	})
	ctx := t.Context()
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	go func() { f.r.RefreshAll(ctx) }()
	waitFor(t, "the first read", func() bool { return f.reader.count(a.ID) == 1 })
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.r.Refresh(ctx, a.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	// Both requests must be queued behind the first read before it ends; otherwise a late one would rightly read a
	// third time.
	waitFor(t, "both refreshes waiting", func() bool { return f.waiting.Load() == 2 })
	close(release)
	wg.Wait()
	if n := f.reader.count(a.ID); n != 2 || most.Load() != 1 {
		t.Fatalf("reads = %d (want 2), most at once = %d (want 1)", n, most.Load())
	}
}

func TestSignedOutAccountIsReadAgainSparsely(t *testing.T) {
	var fail errBox
	fail.Store(ErrNeedsLogin)
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		if err := fail.Load(); err != nil {
			return backend.Quota{}, err
		}
		return okQuota(0.4), nil
	})
	ctx := t.Context()
	f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	if res := f.r.RefreshAll(ctx); len(res) != 1 || !errors.Is(res[0].Err, ErrNeedsLogin) {
		t.Fatalf("first cycle = %+v", res)
	}
	f.clk.Advance(NeedsLoginRecheck - time.Minute)
	if res := f.r.RefreshAll(ctx); len(res) != 0 {
		t.Fatalf("signed-out account read before the recheck time: %+v", res)
	}
	// A wrong "signed out" is corrected at the next recheck.
	fail.Store(nil)
	f.clk.Advance(time.Minute)
	if res := f.r.RefreshAll(ctx); len(res) != 1 || res[0].Err != nil || res[0].Snapshot.Error != nil {
		t.Fatalf("recheck = %+v", res)
	}
}

func TestRequestReadsInBackground(t *testing.T) {
	var fail errBox
	fail.Store(ErrNeedsLogin)
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		if err := fail.Load(); err != nil {
			return backend.Quota{}, err
		}
		return okQuota(0.4), nil
	})
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	if _, err := f.r.Refresh(t.Context(), a.ID); !errors.Is(err, ErrNeedsLogin) {
		t.Fatal(err)
	}
	fail.Store(nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.r.Run(ctx)
		close(done)
	}()
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("Run did not finish its first cycle")
	}
	if n := f.reader.count(a.ID); n != 1 {
		t.Fatalf("the cycle read a signed-out account: %d reads", n)
	}
	f.r.Request(a.ID)
	f.r.Request(a.ID)
	waitFor(t, "the requested read", func() bool {
		snap, _, err := f.store.Get(t.Context(), a.ID)
		return err == nil && snap.Error == nil && snap.Known()
	})
	cancel()
	<-done
	if n := f.reader.count(a.ID); n != 2 {
		t.Fatalf("reads = %d, want 2", n)
	}
}

// Requested reads share the Parallel limit with the cycles (re-review finding 3).
func TestRequestsShareParallelLimit(t *testing.T) {
	release := make(chan struct{})
	var signedOut atomic.Bool
	signedOut.Store(true)
	f := newRefFixture(t, func(_ context.Context, id string) (backend.Quota, error) {
		if id == "acc1" {
			<-release
			return okQuota(0.5), nil
		}
		if signedOut.Load() {
			return backend.Quota{}, ErrNeedsLogin
		}
		return okQuota(0.5), nil
	})
	f.r.sem = make(chan struct{}, 1) // Parallel 1
	a1 := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	a2 := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	if _, err := f.r.Refresh(t.Context(), a2.ID); !errors.Is(err, ErrNeedsLogin) {
		t.Fatal(err)
	}
	signedOut.Store(false)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.r.Run(ctx) // its cycle reads acc1 only (acc2 is signed out) and holds the one slot
		close(done)
	}()
	waitFor(t, "the cycle's read", func() bool { return f.reader.count(a1.ID) == 1 })
	f.r.Request(a2.ID)
	time.Sleep(50 * time.Millisecond)
	if n := f.reader.count(a2.ID); n != 1 {
		t.Fatalf("requested read ran beside the cycle's read: %d reads", n)
	}
	close(release)
	waitFor(t, "the requested read", func() bool { return f.reader.count(a2.ID) == 2 })
	cancel()
	<-done
}
