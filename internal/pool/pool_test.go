package pool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/store"
)

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

type fixture struct {
	t        *testing.T
	db       *store.Store
	m        *account.Manager
	quotas   *quota.Store
	clk      *clock.Fake
	settings atomic.Pointer[config.Settings]
	refresh  func(ctx context.Context, id string) (quota.Snapshot, error)
	p        *Pool

	mu       sync.Mutex
	confirms []string
	noReader map[string]bool // backends without a quota reader
}

func (f *fixture) confirmed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.confirms...)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: db, quotas: quota.NewStore(db), clk: clock.NewFake(t0)}
	// Account times come from the fake clock too, so "created earlier" is under the test's control.
	f.m = account.NewManager(db, account.NewProfiles(profiles, filepath.Join(dir, "real")), f.clk.Now)
	s := config.Defaults()
	f.settings.Store(&s)
	p, err := New(Options{
		Accounts: f.m, Quotas: f.quotas, DB: db, Clock: f.clk,
		Settings: func() config.Settings { return *f.settings.Load() },
		Refresh: func(ctx context.Context, id string) (quota.Snapshot, error) {
			if f.refresh == nil {
				return quota.Snapshot{}, errors.New("no refresher")
			}
			return f.refresh(ctx, id)
		},
		Confirm: func(id string) {
			f.mu.Lock()
			f.confirms = append(f.confirms, id)
			f.mu.Unlock()
		},
		ActiveQuota: func(backend string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return !f.noReader[backend]
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.p = p
	return f
}

func (f *fixture) setting(fn func(*config.Settings)) {
	s := *f.settings.Load()
	fn(&s)
	f.settings.Store(&s)
}

// add creates an enabled account; later accounts are created a second later.
func (f *fixture) add(n account.New) account.Account {
	f.t.Helper()
	n.Enabled = true
	a, _, err := f.m.Add(f.t.Context(), n, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.clk.Advance(time.Second)
	return a
}

func (f *fixture) agy(priority int) account.Account {
	f.t.Helper()
	return f.add(account.New{Backend: "agy", Kind: account.KindIsolated, Priority: priority})
}

// read stores a successful quota read of the group "Gemini Models" (or the given groups).
func (f *fixture) read(id, name string, remaining float64, reset time.Time, mutate ...func(*backend.Quota)) {
	f.t.Helper()
	q := backend.Quota{Entitled: true, Groups: []backend.QuotaGroup{{Name: "Gemini Models", Buckets: []backend.QuotaBucket{
		{Window: "5h", UsedFraction: 1 - remaining, ResetAt: reset},
	}}}}
	for _, m := range mutate {
		m(&q)
	}
	if err := f.quotas.Put(f.t.Context(), quota.FromBackend(id, name, q, f.clk.Now()), f.clk.Now()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) disable(id string) {
	f.t.Helper()
	off := false
	if _, err := f.m.Update(f.t.Context(), id, account.Patch{Enabled: &off}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) apiKeyAccount(provider, key string) account.Account {
	f.t.Helper()
	if _, err := f.m.GetProvider(f.t.Context(), provider); errors.Is(err, account.ErrNotFound) {
		p, err := account.FromPreset(provider, "openai")
		if err != nil {
			f.t.Fatal(err)
		}
		p.Tiers.Standard = "gpt-x"
		if _, err := f.m.CreateProvider(f.t.Context(), p); err != nil {
			f.t.Fatal(err)
		}
	}
	a := f.add(account.New{Backend: "codex", Kind: account.KindAPIKey, Provider: provider})
	if key != "" {
		var err error
		if a, err = f.m.SetKey(f.t.Context(), a.ID, key); err != nil {
			f.t.Fatal(err)
		}
	}
	return a
}

func gemini(task string) Request {
	return Request{TaskID: task, Backend: "agy", Group: "Gemini Models", Model: "gemini-3.8-flash-medium"}
}

func (f *fixture) acquire(req Request) (*Lease, Choice) {
	f.t.Helper()
	l, c, err := f.p.Acquire(f.t.Context(), req)
	if err != nil {
		f.t.Fatalf("Acquire(%s): %v", req.TaskID, err)
	}
	return l, c
}

func TestNewNeedsDependencies(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without dependencies")
	}
}

func TestSelectionOrder(t *testing.T) {
	f := newFixture(t)
	a1 := f.agy(0)
	a2 := f.agy(0)
	a3 := f.agy(5)
	a4 := f.agy(0)
	reset := t0.Add(time.Hour)
	f.read(a1.ID, "agy", 0.4, reset)
	f.read(a2.ID, "agy", 0.9, reset)
	f.read(a3.ID, "agy", 0.1, reset)
	f.read(a4.ID, "agy", 0.9, reset)
	ctx := t.Context()

	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"priority first", gemini("t"), a3.ID},
		{"preferred accounts first, whatever their priority", Request{Backend: "agy", Group: "Gemini Models", Prefer: []string{a1.ID}}, a1.ID},
		{"preferred subset ordered by remaining", Request{Backend: "agy", Group: "Gemini Models", Prefer: []string{a1.ID, a4.ID}}, a4.ID},
		{"preference order breaks ties", Request{Backend: "agy", Group: "Gemini Models", Prefer: []string{a4.ID, a2.ID}}, a4.ID},
		{"older account breaks ties", Request{Backend: "agy", Group: "Gemini Models", Exclude: []string{a3.ID}}, a2.ID},
		{"restricted to one account", Request{Backend: "agy", Group: "Gemini Models", Account: a1.ID}, a1.ID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := f.p.Select(ctx, tt.req)
			if err != nil {
				t.Fatal(err)
			}
			if c.Account.ID != tt.want {
				t.Fatalf("chose %s, want %s", c.Account.ID, tt.want)
			}
		})
	}
	// Fewer running turns beat more remaining quota.
	l, _ := f.acquire(Request{TaskID: "busy", Backend: "agy", Group: "Gemini Models", Account: a2.ID})
	defer l.Release()
	c, err := f.p.Select(ctx, Request{Backend: "agy", Group: "Gemini Models", Exclude: []string{a3.ID}})
	if err != nil || c.Account.ID != a4.ID || c.Remaining != 0.9 || !c.RemainingKnown {
		t.Fatalf("chose %+v, %v; want %s", c, err, a4.ID)
	}
}

func TestSelectionFilters(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	reset := t0.Add(time.Hour)
	ok := f.agy(0)
	f.read(ok.ID, "agy", 0.5, reset)
	disabled := f.agy(9)
	f.read(disabled.ID, "agy", 1, reset)
	f.disable(disabled.ID)
	signedOut := f.agy(9)
	f.read(signedOut.ID, "agy", 1, reset)
	if err := f.quotas.MarkNeedsLogin(ctx, signedOut.ID, "agy", "", t0); err != nil {
		t.Fatal(err)
	}
	noPlan := f.agy(9)
	f.read(noPlan.ID, "agy", 1, reset, func(q *backend.Quota) { q.Entitled = false })
	twin := f.agy(9)
	f.read(ok.ID, "agy", 0.5, reset, func(q *backend.Quota) { q.Email = "same@example.com" })
	f.read(twin.ID, "agy", 1, reset, func(q *backend.Quota) { q.Email = "SAME@example.com" })
	readFailed := f.agy(0)
	f.read(readFailed.ID, "agy", 0.2, reset)
	snaps, _ := f.quotas.All(ctx)
	broken := quota.Merge(snaps[readFailed.ID], true, readFailed, backend.Quota{}, quota.ErrFormat, t0)
	if err := f.quotas.Put(ctx, broken, t0); err != nil {
		t.Fatal(err)
	}

	states, err := f.p.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{ok.ID: StatusOK, disabled.ID: StatusDisabled, signedOut.ID: StatusNeedsLogin,
		noPlan.ID: StatusNoPlan, twin.ID: StatusTwin, readFailed.ID: StatusError}
	for _, s := range states {
		if s.Status != want[s.Account.ID] {
			t.Errorf("%s: status %s, want %s", s.Account.ID, s.Status, want[s.Account.ID])
		}
	}
	for _, s := range states {
		if s.Account.ID == twin.ID && s.TwinOf != ok.ID {
			t.Errorf("twin of %q, want %s", s.TwinOf, ok.ID)
		}
	}
	// Only ok (0.5) and readFailed (0.2, last numbers) can be chosen.
	c, err := f.p.Select(ctx, gemini("t"))
	if err != nil || c.Account.ID != ok.ID {
		t.Fatalf("chose %+v, %v", c, err)
	}
	c, err = f.p.Select(ctx, Request{Backend: "agy", Group: "Gemini Models", Exclude: []string{ok.ID}})
	if err != nil || c.Account.ID != readFailed.ID {
		t.Fatalf("chose %+v, %v", c, err)
	}
	if _, err := f.p.Select(ctx, Request{Backend: "agy", Group: "Gemini Models", Exclude: []string{ok.ID, readFailed.ID}}); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.p.Select(ctx, Request{Backend: "claude", Group: "Claude"}); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("other backend = %v", err)
	}
}

func TestProviderIsolationAndModelFit(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	login := f.add(account.New{Backend: "codex", Kind: account.KindIsolated})
	f.read(login.ID, "codex", 0.8, time.Time{}, func(q *backend.Quota) {
		q.Groups[0].Name = "Codex"
		q.Models = []string{"gpt-6-terra", "gpt-6-luna"}
	})
	keyed := f.apiKeyAccount("openai", "sk-test-0123456789abcdef")
	noKey := f.apiKeyAccount("openai", "")
	other := f.apiKeyAccount("groq", "gsk-test-0123456789abcdef")

	c, err := f.p.Select(ctx, Request{Backend: "codex", Group: "Codex", Model: "gpt-6-sol"})
	if err != nil || c.Account.ID != login.ID || c.Model != "gpt-6-terra" {
		t.Fatalf("sign-in request chose %+v, %v", c, err)
	}
	// Nothing the account has is allowed: not a candidate.
	_, err = f.p.Select(ctx, Request{Backend: "codex", Group: "Codex", Model: "gpt-6-sol",
		Allowed: func(string) bool { return false }})
	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("no allowed model = %v", err)
	}
	c, err = f.p.Select(ctx, Request{Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "openai"})
	if err != nil || c.Account.ID != keyed.ID || c.Model != "gpt-x" || c.RemainingKnown || c.Remaining != quota.UnknownRemaining {
		t.Fatalf("provider request chose %+v, %v", c, err)
	}
	c, err = f.p.Select(ctx, Request{Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "groq"})
	if err != nil || c.Account.ID != other.ID {
		t.Fatalf("groq request chose %+v, %v", c, err)
	}
	states, _ := f.p.States(ctx)
	for _, s := range states {
		if s.Account.ID == noKey.ID && s.Status != StatusNoKey {
			t.Fatalf("no key status = %s", s.Status)
		}
	}
}

func TestMinRemainingAndReasons(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	reset1, reset2 := t0.Add(2*time.Hour), t0.Add(time.Hour)
	a := f.agy(0)
	b := f.agy(0)
	f.read(a.ID, "agy", 0.02, reset1) // below minRemaining: resting
	f.read(b.ID, "agy", 0.01, reset2)
	_, err := f.p.Select(ctx, gemini("t"))
	var u *Unavailable
	if !errors.As(err, &u) || u.Reason != ReasonResting || !u.Until.Equal(reset2) || u.Resting != 2 ||
		!errors.Is(err, ErrAllResting) {
		t.Fatalf("err = %v", err)
	}
	if u.Error() == "" {
		t.Fatal("empty message")
	}
	// minRemaining 0 makes a usable again.
	f.setting(func(s *config.Settings) { s.MinRemaining = 0 })
	if c, err := f.p.Select(ctx, gemini("t")); err != nil || c.Account.ID != a.ID {
		t.Fatalf("chose %+v, %v", c, err)
	}
	f.setting(func(s *config.Settings) { s.MinRemaining = 0.03 })
	// After the reset time the window counts as full.
	f.clk.Advance(time.Hour)
	if c, err := f.p.Select(ctx, gemini("t")); err != nil || c.Account.ID != b.ID || c.Remaining != 1 {
		t.Fatalf("after reset chose %+v, %v", c, err)
	}
	// Busy: the free account is full.
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount = 1 })
	l, _ := f.acquire(gemini("t1"))
	defer l.Release()
	_, err = f.p.Select(ctx, gemini("t2"))
	if !errors.As(err, &u) || u.Reason != ReasonBusy || u.Busy != 1 || u.Resting != 1 || !u.Until.Equal(reset1) ||
		!errors.Is(err, ErrAllBusy) {
		t.Fatalf("err = %v", err)
	}
	// A never-read account waits rather than failing.
	c := f.agy(0)
	f.disable(a.ID)
	_, err = f.p.Select(ctx, Request{Backend: "agy", Group: "Gemini Models", Exclude: []string{b.ID}})
	if !errors.As(err, &u) || u.Reason != ReasonResting || !u.Until.IsZero() {
		t.Fatalf("new account: %v", err)
	}
	_ = c
	for _, r := range []*Unavailable{{Reason: ReasonNoAccount, Backend: "agy"}, {Reason: ReasonBusy, Backend: "agy", Group: "G"}} {
		if r.Error() == "" || errors.Is(r, ErrAllResting) {
			t.Fatalf("%+v", r)
		}
	}
	if (&Unavailable{Reason: "odd"}).Is(ErrAllBusy) {
		t.Fatal("unknown reason matched")
	}
}

func TestLeaseLifecycle(t *testing.T) {
	f := newFixture(t)
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	l1, _ := f.acquire(gemini("t1"))
	l2, _ := f.acquire(gemini("t2"))
	if f.p.Busy(a.ID) != 2 || l1.AccountID() != a.ID || l1.TaskID() != "t1" || !l1.AcquiredAt().Equal(f.clk.Now()) {
		t.Fatalf("busy = %d", f.p.Busy(a.ID))
	}
	if _, _, err := f.p.Acquire(t.Context(), gemini("t3")); !errors.Is(err, ErrAllBusy) {
		t.Fatalf("over the cap: %v", err)
	}
	// Release twice: the second call does nothing (it must not free t2's slot).
	l1.Release()
	l1.Release()
	if f.p.Busy(a.ID) != 1 {
		t.Fatalf("busy after double release = %d", f.p.Busy(a.ID))
	}
	// Release after an error: the deferred release runs on every path.
	runTurn := func() (err error) {
		l, _, err := f.p.Acquire(t.Context(), gemini("t4"))
		if err != nil {
			return err
		}
		defer l.Release()
		return errors.New("env: no API key") // fails between acquire and launch (bug B3b)
	}
	if err := runTurn(); err == nil {
		t.Fatal("expected the turn error")
	}
	if f.p.Busy(a.ID) != 1 {
		t.Fatalf("busy after a failed turn = %d", f.p.Busy(a.ID))
	}
	// The same task cannot take the account twice.
	if _, _, err := f.p.Acquire(t.Context(), gemini("t2")); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("second acquire = %v", err)
	}
	l2.Release()
	// Leases without a task id are distinct.
	x, _ := f.acquire(Request{Backend: "agy", Group: "Gemini Models"})
	y, _ := f.acquire(Request{Backend: "agy", Group: "Gemini Models"})
	if x.TaskID() == y.TaskID() || f.p.Busy(a.ID) != 2 {
		t.Fatalf("anonymous leases %s, %s", x.TaskID(), y.TaskID())
	}
	x.Release()
	y.Release()
	var nilLease *Lease
	nilLease.Release()
	(&Lease{}).Release()
	if f.p.Busy(a.ID) != 0 {
		t.Fatalf("busy = %d", f.p.Busy(a.ID))
	}
}

func TestReattach(t *testing.T) {
	f := newFixture(t)
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount = 1 })
	// A restart finds two workers on the account (cap 1): both are counted, each once.
	r1 := f.p.Reattach(a.ID, "t1")
	if again := f.p.Reattach(a.ID, "t1"); again != r1 {
		t.Fatal("a second reattach of the same task made a second lease")
	}
	r2 := f.p.Reattach(a.ID, "t2")
	if f.p.Busy(a.ID) != 2 {
		t.Fatalf("busy = %d, want 2", f.p.Busy(a.ID))
	}
	// The recovered task must not acquire again on top of its reattached lease (bug B3a).
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount = 5 })
	if _, _, err := f.p.Acquire(t.Context(), gemini("t1")); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("acquire after reattach = %v", err)
	}
	r1.Release()
	r1.Release()
	r2.Release()
	if f.p.Busy(a.ID) != 0 {
		t.Fatalf("busy = %d", f.p.Busy(a.ID))
	}
	// A worker of an account removed meanwhile is still counted and released without errors.
	g := f.p.Reattach("acc404", "t9")
	if f.p.Busy("acc404") != 1 {
		t.Fatal("unknown account not counted")
	}
	g.Release()
}

func TestConcurrentAcquireNeverOverfills(t *testing.T) {
	f := newFixture(t)
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	var wg sync.WaitGroup
	var got atomic.Int32
	leases := make(chan *Lease, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, _, err := f.p.Acquire(context.Background(), gemini("t"+string(rune('a'+i))))
			if err == nil {
				got.Add(1)
				leases <- l
			}
		}()
	}
	wg.Wait()
	close(leases)
	if got.Load() != 2 || f.p.Busy(a.ID) != 2 {
		t.Fatalf("acquired %d, busy %d; want 2", got.Load(), f.p.Busy(a.ID))
	}
	for l := range leases {
		l.Release()
	}
}

func TestLoadErrors(t *testing.T) {
	f := newFixture(t)
	f.p.o.Accounts = failingAccounts{}
	if _, err := f.p.Select(t.Context(), gemini("t")); err == nil {
		t.Fatal("Select with a failing account list")
	}
	if _, _, err := f.p.Acquire(t.Context(), gemini("t")); err == nil {
		t.Fatal("Acquire with a failing account list")
	}
	if _, err := f.p.States(t.Context()); err == nil {
		t.Fatal("States with a failing account list")
	}
	if _, err := f.p.Handover(t.Context(), HandoverInput{}); err == nil {
		t.Fatal("Handover with a failing account list")
	}
	if _, err := f.p.MarkExhausted(t.Context(), "acc1", Exhaustion{}); err == nil {
		t.Fatal("MarkExhausted with a failing account list")
	}
	if err := f.p.MarkAuthFailed(t.Context(), "acc1", ""); err == nil {
		t.Fatal("MarkAuthFailed with a failing account list")
	}
	f.p.o.Accounts = f.m
	f.p.o.Quotas = failingQuotas{}
	if _, err := f.p.Select(t.Context(), gemini("t")); err == nil {
		t.Fatal("Select with a failing quota cache")
	}
}

type failingAccounts struct{}

func (failingAccounts) List(context.Context) ([]account.Account, error) {
	return nil, errors.New("db down")
}

type failingQuotas struct{}

func (failingQuotas) All(context.Context) (map[string]quota.Snapshot, error) {
	return nil, errors.New("db down")
}

func (failingQuotas) MarkNeedsLogin(context.Context, string, string, string, time.Time) error {
	return errors.New("db down")
}

// A sign-in account of a backend without a quota reader never gets a read: it must be usable with unknown quota,
// not wait as "new" forever (review finding G), and rest passively after a quota error.
func TestBackendWithoutQuotaReader(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.noReader = map[string]bool{"claude": true}
	c := f.add(account.New{Backend: "claude", Kind: account.KindIsolated})
	req := Request{Backend: "claude", Group: "Claude", Model: "claude-x"}
	ch, err := f.p.Select(ctx, req)
	if err != nil || ch.Account.ID != c.ID || ch.RemainingKnown || ch.Remaining != quota.UnknownRemaining {
		t.Fatalf("chose %+v, %v", ch, err)
	}
	states, _ := f.p.States(ctx)
	if states[0].Status != StatusOK || states[0].HasQuota {
		t.Fatalf("state %+v", states[0])
	}
	cd, err := f.p.MarkExhausted(ctx, c.ID, Exhaustion{Group: "Claude"})
	if err != nil || !cd.Passive || !cd.Until.Equal(f.clk.Now().Add(60*time.Minute)) {
		t.Fatalf("cooldown %+v, %v", cd, err)
	}
	// With a reader the same account waits for its first read.
	f.mu.Lock()
	f.noReader = nil
	f.mu.Unlock()
	if err := f.p.ClearCooldown(ctx, c.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Select(ctx, req); !errors.Is(err, ErrAllResting) {
		t.Fatalf("with a reader: %v", err)
	}
}
