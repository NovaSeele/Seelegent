package quota

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
)

// scriptReader answers per account id.
type scriptReader struct {
	mu    sync.Mutex
	calls map[string]int
	fn    func(ctx context.Context, id string) (backend.Quota, error)
}

func (s *scriptReader) ReadQuota(ctx context.Context, prof account.Profile, _ []string) (backend.Quota, error) {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[prof.AccountID]++
	s.mu.Unlock()
	return s.fn(ctx, prof.AccountID)
}

func (s *scriptReader) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[id]
}

func okQuota(remaining float64) backend.Quota {
	return backend.Quota{Entitled: true, Groups: []backend.QuotaGroup{{Name: "Gemini Models", Buckets: []backend.QuotaBucket{
		{Window: "5h", UsedFraction: 1 - remaining},
	}}}}
}

type refFixture struct {
	*fixture
	store   *Store
	clk     *clock.Fake
	reader  *scriptReader
	updates atomic.Int32
	r       *Refresher
}

func newRefFixture(t *testing.T, fn func(ctx context.Context, id string) (backend.Quota, error)) *refFixture {
	t.Helper()
	f := &refFixture{fixture: newFixture(t), clk: clock.NewFake(t0), reader: &scriptReader{fn: fn}}
	f.store = NewStore(f.db)
	env := func(_ context.Context, a account.Account) (account.Profile, []string, error) {
		return account.Profile{AccountID: a.ID, Kind: a.Kind}, nil, nil
	}
	r, err := NewRefresher(RefresherOptions{
		Accounts: f.m, Env: env, Store: f.store, Clock: f.clk,
		Readers:  map[string]backend.QuotaReader{"agy": f.reader, "codex": f.reader},
		Jitter:   func(int64) int64 { return 0 },
		OnUpdate: func(Snapshot) { f.updates.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.r = r
	return f
}

func TestRefreshAllIsolatesFailures(t *testing.T) {
	f := newRefFixture(t, func(_ context.Context, id string) (backend.Quota, error) {
		switch id {
		case "acc1":
			return okQuota(0.8), nil
		case "acc2":
			return backend.Quota{}, errors.New("unrecognized: " + ErrFormat.Error())
		case "acc3":
			panic("reader bug")
		}
		return okQuota(0.5), nil
	})
	ctx := t.Context()
	good := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	broken := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	panics := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	disabled := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated})
	noReader := f.add(t, account.New{Backend: "claude", Kind: account.KindIsolated, Enabled: true})
	p, _ := account.FromPreset("openai", "openai")
	p.Tiers.Standard = "gpt-x"
	if _, err := f.m.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	apiKey := f.add(t, account.New{Backend: "codex", Kind: account.KindAPIKey, Provider: "openai", Enabled: true})

	results := f.r.RefreshAll(ctx)
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	byID := map[string]Result{}
	for _, res := range results {
		byID[res.AccountID] = res
	}
	if byID[good.ID].Err != nil || byID[broken.ID].Err == nil || byID[panics.ID].Err == nil {
		t.Fatalf("results = %+v", results)
	}
	all, err := f.store.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s := all[good.ID]; !s.Known() || s.Error != nil {
		t.Fatalf("good = %+v", s)
	}
	if s := all[broken.ID]; s.Known() || s.ErrorKind() != KindFailed {
		t.Fatalf("broken = %+v", s)
	}
	if s := all[panics.ID]; s.ErrorKind() != KindFailed || s.Error.Message == "" {
		t.Fatalf("panicking reader = %+v", s)
	}
	for _, id := range []string{disabled.ID, noReader.ID, apiKey.ID} {
		if _, ok := all[id]; ok || f.reader.count(id) != 0 {
			t.Fatalf("%s should not be read", id)
		}
	}
	if f.updates.Load() != 3 {
		t.Fatalf("updates = %d", f.updates.Load())
	}
}

func TestRefreshKeepsLastNumbersAndSkipsSignedOut(t *testing.T) {
	var fail errBox
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		if err := fail.Load(); err != nil {
			return backend.Quota{}, err
		}
		q := okQuota(0.6)
		q.Entitled = false
		return q, nil
	})
	ctx := t.Context()
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	if _, err := f.r.Refresh(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// A format change keeps the last numbers and the no-plan lock.
	fail.Store(ErrFormat)
	f.clk.Advance(time.Minute)
	snap, err := f.r.Refresh(ctx, a.ID)
	if !errors.Is(err, ErrFormat) || !snap.Known() || !snap.FetchedAt.Equal(t0) || !snap.NoPlan ||
		snap.ErrorKind() != KindFormat || len(snap.Groups) != 1 {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	// Signed out: recorded, and RefreshAll leaves the account alone afterwards.
	fail.Store(ErrNeedsLogin)
	if _, err := f.r.Refresh(ctx, a.ID); !errors.Is(err, ErrNeedsLogin) {
		t.Fatal(err)
	}
	before := f.reader.count(a.ID)
	if res := f.r.RefreshAll(ctx); len(res) != 0 || f.reader.count(a.ID) != before {
		t.Fatalf("signed-out account was read: %+v", res)
	}
	// After a sign-in an explicit refresh works again.
	fail.Store(nil)
	snap, err = f.r.Refresh(ctx, a.ID)
	if err != nil || snap.Error != nil || !snap.FetchedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	if _, err := f.r.Refresh(ctx, "acc99"); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
}

func TestRefreshTimeoutPerAccount(t *testing.T) {
	f := newRefFixture(t, func(ctx context.Context, id string) (backend.Quota, error) {
		if id == "acc1" {
			<-ctx.Done() // a reader stuck until its time limit
			return backend.Quota{}, ctx.Err()
		}
		return okQuota(0.9), nil
	})
	f.r.o.Timeout = 50 * time.Millisecond
	f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	res := f.r.RefreshAll(t.Context())
	if len(res) != 2 || KindOf(res[0].Err) != KindUnavailable || res[1].Err != nil {
		t.Fatalf("results = %+v", res)
	}
}

func TestRefreshSharesInflightRead(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) {
		started <- struct{}{}
		<-release
		return okQuota(0.7), nil
	})
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.r.Refresh(t.Context(), a.ID)
			errs <- err
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.reader.count(a.ID); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
	// A waiter whose context ends stops waiting.
	f2 := newRefFixture(t, func(ctx context.Context, _ string) (backend.Quota, error) {
		<-ctx.Done()
		return backend.Quota{}, ctx.Err()
	})
	b := f2.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	go func() { _, _ = f2.r.Refresh(t.Context(), b.ID) }()
	for f2.reader.count(b.ID) == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f2.r.Refresh(ctx, b.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCyclesWithIntervalAndKick(t *testing.T) {
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) { return okQuota(0.5), nil })
	var interval atomic.Int64
	interval.Store(int64(10 * time.Minute))
	f.r.o.Interval = func() time.Duration { return time.Duration(interval.Load()) }
	f.r.o.Jitter = func(n int64) int64 { return n - 1 } // the longest wait: +10 %
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.r.Run(ctx)
		close(done)
	}()
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
	waitReads(1) // the first cycle runs at once
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no timer")
	}
	f.clk.Advance(10 * time.Minute)
	time.Sleep(20 * time.Millisecond)
	if f.reader.count(a.ID) != 1 {
		t.Fatal("cycle ran before interval + jitter")
	}
	f.clk.Advance(time.Minute)
	waitReads(2)
	// A shorter interval applies to the next wait; Kick runs a cycle at once.
	interval.Store(int64(time.Minute))
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no timer")
	}
	f.r.Kick()
	waitReads(3)
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no timer")
	}
	f.clk.Advance(66 * time.Second)
	waitReads(4)
	cancel()
	<-done
}

func TestMergeAndNewRefresher(t *testing.T) {
	a := account.Account{ID: "acc1", Backend: "agy", Kind: account.KindIsolated}
	s := Merge(Snapshot{}, false, a, backend.Quota{}, ErrPassive, t0)
	if s.Source != SourcePassive || s.Error != nil || s.AccountID != "acc1" {
		t.Fatalf("passive = %+v", s)
	}
	s = Merge(Snapshot{}, false, a, backend.Quota{}, ErrTokenExpired, t0)
	if s.Source != SourceActive || s.ErrorKind() != KindTokenExpired || s.Groups == nil {
		t.Fatalf("expired = %+v", s)
	}
	if _, err := NewRefresher(RefresherOptions{}); err == nil {
		t.Fatal("NewRefresher without dependencies")
	}
	r, err := NewRefresher(RefresherOptions{Accounts: listErr{}, Env: func(context.Context, account.Account) (account.Profile, []string, error) {
		return account.Profile{}, nil, nil
	}, Store: NewStore(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if r.RefreshAll(t.Context()) != nil {
		t.Fatal("RefreshAll with a failing account list")
	}
	if _, err := r.Refresh(t.Context(), "x"); err == nil {
		t.Fatal("Refresh with a failing account list")
	}
	if d := r.nextWait(); d < 9*time.Minute || d > 11*time.Minute {
		t.Fatalf("default wait = %v", d)
	}
}

// errBox holds an error for concurrent use.
type errBox struct {
	mu  sync.Mutex
	err error
}

func (b *errBox) Store(err error) { b.mu.Lock(); b.err = err; b.mu.Unlock() }

func (b *errBox) Load() error { b.mu.Lock(); defer b.mu.Unlock(); return b.err }

type listErr struct{}

func (listErr) List(context.Context) ([]account.Account, error) { return nil, errors.New("db down") }

func TestRefreshEnvFailureAndPassive(t *testing.T) {
	f := newRefFixture(t, func(context.Context, string) (backend.Quota, error) { return okQuota(1), nil })
	f.r.o.Env = func(context.Context, account.Account) (account.Profile, []string, error) {
		return account.Profile{}, nil, account.ErrNoKey
	}
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	if _, err := f.r.Refresh(t.Context(), a.ID); !errors.Is(err, account.ErrNoKey) {
		t.Fatalf("env failure = %v", err)
	}
	p, _ := account.FromPreset("openai", "openai")
	p.Tiers.Standard = "gpt-x"
	if _, err := f.m.CreateProvider(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	k := f.add(t, account.New{Backend: "codex", Kind: account.KindAPIKey, Provider: "openai", Enabled: true})
	if _, err := f.r.Refresh(t.Context(), k.ID); !errors.Is(err, ErrPassive) {
		t.Fatalf("api key = %v", err)
	}
	f.r.o.Readers = map[string]backend.QuotaReader{}
	if _, err := f.r.Refresh(t.Context(), a.ID); !errors.Is(err, ErrNoReader) {
		t.Fatalf("no reader = %v", err)
	}
}

func TestStore(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := NewStore(f.db)
	a := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	if _, found, err := s.Get(ctx, a.ID); found || err != nil {
		t.Fatalf("Get empty = %v, %v", found, err)
	}
	snap := FromBackend(a.ID, "agy", okQuota(0.3), t0)
	if err := s.Put(ctx, snap, t0); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Get(ctx, a.ID)
	if err != nil || !found || !got.FetchedAt.Equal(t0) || got.Groups[0].Windows[0].Remaining < 0.29 {
		t.Fatalf("Get = %+v, %v, %v", got, found, err)
	}
	// A quota row for an unknown account is refused (foreign key).
	if err := s.Put(ctx, Snapshot{AccountID: "ghost"}, t0); err == nil {
		t.Fatal("Put for an unknown account succeeded")
	}
	// A damaged row affects only its account and is not read as full quota.
	b := f.add(t, account.New{Backend: "agy", Kind: account.KindIsolated, Enabled: true})
	err = f.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO quota_cache (account_id, data, updated_at) VALUES (?, '{broken', 'x')", b.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.All(ctx)
	if err != nil || len(all) != 2 || all[b.ID].ErrorKind() != KindFormat || all[b.ID].Known() || !all[a.ID].Known() {
		t.Fatalf("All = %+v, %v", all, err)
	}
	// Removing the account removes its quota.
	if err := f.m.Remove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Get(ctx, a.ID); found {
		t.Fatal("quota of a removed account is still there")
	}
}
