package quota

import (
	"context"
	"errors"
	"fmt"
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
	cycle := make(chan struct{})
	go func() {
		defer close(cycle)
		f.r.RefreshAll(ctx)
	}()
	// The cycle and the refreshes store their snapshots after release: wait for them before the store's temp dir
	// is removed, also when the test fails before releasing them (release first, or wg.Wait hangs).
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	defer func() { releaseOnce(); wg.Wait(); <-cycle }()
	waitFor(t, "the first read", func() bool { return f.reader.count(a.ID) == 1 })
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
	releaseOnce()
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
	// Two requests before the loop wakes are served by one read. Calling Request twice cannot pin that: the loop may
	// take the first before the second comes, and then the second is a new request (CI saw 3 reads). So the first is
	// recorded without waking the loop, and the second wakes it.
	f.r.mu.Lock()
	f.r.pending[a.ID] = 0
	f.r.mu.Unlock()
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

// Review of 885c494: two fresh refreshes woken from one older read; the second looks again only after the first
// one's newer read has finished. It must take that read, not start a third one (the old "reads = 3" flake, now
// forced every time).
func TestWokenRefreshSharesFinishedRead(t *testing.T) {
	release := make(chan struct{})
	var first sync.Once
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		block := false
		first.Do(func() { block = true })
		if block {
			<-release
		}
		return okQuota(0.5), nil
	})
	ctx := t.Context()
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	oneDone := make(chan struct{})
	var once sync.Once
	var woken atomic.Int32
	f.woken = func() {
		if woken.Add(1) == 2 {
			<-oneDone // the second one looks again only after the first one's read has finished
		}
	}
	cycle := make(chan struct{})
	go func() {
		defer close(cycle)
		f.r.RefreshAll(ctx)
	}()
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	defer func() { releaseOnce(); once.Do(func() { close(oneDone) }); wg.Wait(); <-cycle }()
	waitFor(t, "the first read", func() bool { return f.reader.count(a.ID) == 1 })
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.r.Refresh(ctx, a.ID); err != nil {
				t.Error(err)
			}
			once.Do(func() { close(oneDone) })
		}()
	}
	waitFor(t, "both refreshes waiting", func() bool { return f.waiting.Load() == 2 })
	releaseOnce()
	wg.Wait()
	if n := f.reader.count(a.ID); n != 2 {
		t.Fatalf("reads = %d, want 2: the second refresh read again instead of sharing the finished one", n)
	}
}

// Review of 885c494: a read shared by two callers runs on the context of the caller that started it. When that
// caller goes away, the other one must not get its context error: it reads again.
func TestSharedReadCanceledByOtherCaller(t *testing.T) {
	var calls atomic.Int32
	f := newRefFixture(t, func(ctx context.Context, _ string) (backend.Quota, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return backend.Quota{}, ctx.Err()
		}
		return okQuota(0.7), nil
	})
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	starter, cancel := context.WithCancel(t.Context())
	started := make(chan Result, 1)
	go func() { started <- f.r.refresh(starter, a, false) }()
	waitFor(t, "the first read", func() bool { return f.reader.count(a.ID) == 1 })
	joined := make(chan Result, 1)
	go func() { joined <- f.r.refresh(t.Context(), a, false) }()
	waitFor(t, "the second caller waiting", func() bool { return f.waiting.Load() == 1 })
	cancel()
	if res := <-started; !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("starter: %v, want its own context error", res.Err)
	}
	res := <-joined
	if res.Err != nil {
		t.Fatalf("joined caller got %v, want a fresh read", res.Err)
	}
	if n := f.reader.count(a.ID); n != 2 {
		t.Fatalf("reads = %d, want 2", n)
	}
	// A failed-by-context read is never kept as the last read a later fresh refresh could take.
	f.r.mu.Lock()
	last := f.r.last[a.ID]
	f.r.mu.Unlock()
	if last == nil || last.res.Err != nil {
		t.Fatalf("last read = %+v, want the good second read", last)
	}
}

// Review of 3801679: a reader that runs out of its own time limit answers with a wrapped DeadlineExceeded while
// every caller is still live. That is a real result: it is shared, not read again by each caller.
func TestReaderTimeoutIsShared(t *testing.T) {
	timedOut := fmt.Errorf("agy quota: %w", context.DeadlineExceeded)
	release := make(chan struct{})
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		<-release
		return backend.Quota{}, timedOut
	})
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	results := make(chan Result, 3)
	for range 3 {
		go func() { results <- f.r.refresh(t.Context(), a, false) }()
	}
	waitFor(t, "two callers waiting on the first one's read", func() bool { return f.waiting.Load() == 2 })
	close(release)
	for range 3 {
		if res := <-results; !errors.Is(res.Err, context.DeadlineExceeded) {
			t.Fatalf("result = %v, want the reader's timeout", res.Err)
		}
	}
	if n := f.reader.count(a.ID); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
}

// Review of 3801679, fresh case: two fresh refreshes woken from an older read share the newer read even when that
// read ends in the reader's own timeout: 2 reads in all, not 3.
func TestFreshRefreshesShareReaderTimeout(t *testing.T) {
	timedOut := fmt.Errorf("agy quota: %w", context.DeadlineExceeded)
	release := make(chan struct{})
	var calls atomic.Int32
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		if calls.Add(1) == 1 {
			<-release
			return okQuota(0.5), nil
		}
		return backend.Quota{}, timedOut
	})
	ctx := t.Context()
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	first := make(chan Result, 1)
	go func() { first <- f.r.refresh(ctx, a, false) }()
	waitFor(t, "the first read", func() bool { return f.reader.count(a.ID) == 1 })
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := f.r.Refresh(ctx, a.ID)
			errs <- err
		}()
	}
	waitFor(t, "both refreshes waiting", func() bool { return f.waiting.Load() == 2 })
	close(release)
	<-first
	for range 2 {
		if err := <-errs; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("refresh = %v, want the reader's timeout", err)
		}
	}
	if n := f.reader.count(a.ID); n != 2 {
		t.Fatalf("reads = %d, want 2", n)
	}
}
