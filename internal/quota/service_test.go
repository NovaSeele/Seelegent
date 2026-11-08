package quota

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/clock"
)

// fakeServiceAccounts keeps accounts in memory and records what RecordService stores, as account.Repo does: a good
// read sets ReadAt and the values and clears the error, a failure records the error with its time, a hold sets
// NoCreditAt.
type fakeServiceAccounts struct {
	clk       *clock.Fake
	mu        sync.Mutex
	accounts  []account.Account
	providers map[string]account.Provider
}

func (f *fakeServiceAccounts) List(context.Context) ([]account.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]account.Account, len(f.accounts))
	copy(out, f.accounts)
	return out, nil
}

func (f *fakeServiceAccounts) GetProvider(_ context.Context, id string) (account.Provider, error) {
	p, ok := f.providers[id]
	if !ok {
		return account.Provider{}, account.ErrNotFound
	}
	return p, nil
}

func (f *fakeServiceAccounts) Credentials(ctx context.Context, a account.Account) (account.Provider, string, error) {
	p, err := f.GetProvider(ctx, a.Provider)
	return p, "key-of-" + a.ID, err
}

func (f *fakeServiceAccounts) RecordService(_ context.Context, id string, _ time.Time,
	reading *account.ServiceReading, readErr *account.ServiceError, marked bool,
) (account.ServiceSpend, []account.BudgetCrossing, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.accounts {
		a := &f.accounts[i]
		if a.ID != id {
			continue
		}
		svc := account.ServiceSpend{}
		if a.Service != nil {
			svc = *a.Service
		}
		now := f.clk.Now()
		if reading != nil {
			svc.ReadAt, svc.Balance, svc.Usage, svc.Error = now, reading.Balance, reading.Usage, nil
			if !reading.NoCredit {
				svc.NoCreditAt = time.Time{}
			}
		} else {
			e := *readErr
			e.At = now
			svc.Error = &e
		}
		if marked {
			svc.NoCreditAt = now
		}
		a.Service = &svc
		return svc, nil, true, nil
	}
	return account.ServiceSpend{}, nil, false, account.ErrNotFound
}

func (f *fakeServiceAccounts) get(id string) account.Account {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.accounts {
		if f.accounts[i].ID == id {
			return f.accounts[i]
		}
	}
	return account.Account{}
}

func (f *fakeServiceAccounts) touch(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.accounts {
		if f.accounts[i].ID == id {
			f.accounts[i].UpdatedAt = f.clk.Now()
		}
	}
}

// serviceFixture: "or" on OpenRouter, "ds" on DeepSeek (no credit when noCredit is set), "z" on Z.ai (no spend
// service), "off" disabled on OpenRouter, "nokey" on OpenRouter without a key.
type serviceFixture struct {
	clk      *clock.Fake
	accounts *fakeServiceAccounts
	r        *ServiceRefresher

	mu       sync.Mutex
	reads    map[string]int
	fail     map[string]*account.ServiceError
	noCredit bool
	held     []string
	updates  int
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC))
	f := &serviceFixture{clk: clk, reads: map[string]int{}, fail: map[string]*account.ServiceError{}}
	f.accounts = &fakeServiceAccounts{clk: clk, providers: map[string]account.Provider{
		"openrouter": {ID: "openrouter", Enabled: true, BaseURL: "https://openrouter.ai/api"},
		"deepseek":   {ID: "deepseek", Enabled: true, BaseURL: "https://api.deepseek.com/anthropic"},
		"zai":        {ID: "zai", Enabled: true, BaseURL: "https://api.z.ai/api/anthropic"},
	}}
	key := func(id, provider string, enabled bool, hint string) account.Account {
		return account.Account{ID: id, Kind: account.KindAPIKey, Provider: provider, Enabled: enabled, KeyHint: hint}
	}
	f.accounts.accounts = []account.Account{
		key("or", "openrouter", true, "…abcd"), key("ds", "deepseek", true, "…abcd"), key("z", "zai", true, "…abcd"),
		key("off", "openrouter", false, "…abcd"), key("nokey", "openrouter", true, ""),
		{ID: "iso", Kind: account.KindIsolated, Enabled: true},
	}
	r, err := NewServiceRefresher(ServiceOptions{
		Accounts: f.accounts, Clock: clk,
		Read: func(_ context.Context, p account.Provider, key string) (account.ServiceReading, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			id := key[len("key-of-"):]
			f.reads[id]++
			if e := f.fail[id]; e != nil {
				return account.ServiceReading{}, e
			}
			if p.ID == "deepseek" {
				return account.ServiceReading{NoCredit: f.noCredit, Balance: &account.ServiceBalance{Currency: "CNY",
					AccountWide: true, Available: !f.noCredit}}, nil
			}
			return account.ServiceReading{Usage: &account.ServiceUsage{TotalUSD: 1.5}}, nil
		},
		NoCredit: func(_ context.Context, id, _ string) error {
			f.mu.Lock()
			f.held = append(f.held, id)
			f.mu.Unlock()
			return nil
		},
		OnUpdate: func(context.Context, string, []account.BudgetCrossing) {
			f.mu.Lock()
			f.updates++
			f.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.r = r
	return f
}

func (f *serviceFixture) readsOf(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[id]
}

// Only enabled API-key accounts with a key on a provider with a spend service are read: at once the first time,
// then once per interval. A turn asks for a read after the debounce; turns meanwhile share it.
func TestServiceCadence(t *testing.T) {
	f := newServiceFixture(t)
	ctx := t.Context()
	t0 := f.clk.Now()
	if next := f.r.Cycle(ctx); !next.Equal(t0.Add(DefaultServiceInterval)) {
		t.Fatalf("next after the first cycle: %v", next.Sub(t0))
	}
	for id, want := range map[string]int{"or": 1, "ds": 1, "z": 0, "off": 0, "nokey": 0, "iso": 0} {
		if got := f.readsOf(id); got != want {
			t.Errorf("%s read %d times, want %d", id, got, want)
		}
	}
	if f.updates != 2 {
		t.Fatalf("updates: %d", f.updates)
	}

	// Nothing before the interval.
	f.clk.Advance(DefaultServiceInterval - time.Minute)
	f.r.Cycle(ctx)
	if f.readsOf("or") != 1 {
		t.Fatal("read before the interval")
	}

	// A turn: read after the debounce, not before; several turns share one read.
	f.r.AfterTurn("or")
	f.clk.Advance(10 * time.Second)
	f.r.AfterTurn("or")
	if next := f.r.Cycle(ctx); !next.Equal(f.clk.Now().Add(DefaultServiceDebounce - 10*time.Second)) {
		t.Fatalf("next while a turn waits: %v", next.Sub(f.clk.Now()))
	}
	if f.readsOf("or") != 1 {
		t.Fatal("read before the debounce")
	}
	f.clk.Advance(DefaultServiceDebounce)
	f.r.Cycle(ctx)
	if f.readsOf("or") != 2 || f.readsOf("ds") != 1 {
		t.Fatalf("after the debounce: or %d, ds %d", f.readsOf("or"), f.readsOf("ds"))
	}
	f.r.Cycle(ctx)
	if f.readsOf("or") != 2 {
		t.Fatal("a turn's read ran twice")
	}

	// The interval counts from each account's last read: ds is due, or is not.
	f.clk.Advance(time.Minute)
	f.r.Cycle(ctx)
	if f.readsOf("or") != 2 || f.readsOf("ds") != 2 {
		t.Fatalf("at the interval: or %d, ds %d", f.readsOf("or"), f.readsOf("ds"))
	}

	// A turn on an account without a spend service is dropped; Request reads at once.
	f.r.AfterTurn("z")
	f.r.AfterTurn("gone")
	f.r.Request("or")
	f.r.Cycle(ctx)
	f.clk.Advance(DefaultServiceDebounce)
	f.r.Cycle(ctx)
	if f.readsOf("or") != 3 || f.readsOf("z") != 0 {
		t.Fatalf("request: or %d, z %d", f.readsOf("or"), f.readsOf("z"))
	}
	f.r.mu.Lock()
	pending := len(f.r.due)
	f.r.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending reads left: %d", pending)
	}
}

// A failed read keeps the last values and records the failure (stale); the next read is an interval after the
// failure, and a good read clears it.
func TestServiceFailureStale(t *testing.T) {
	f := newServiceFixture(t)
	ctx := t.Context()
	f.r.Cycle(ctx)
	read := f.accounts.get("or").Service
	if read == nil || read.Usage.TotalUSD != 1.5 || read.Stale(f.clk.Now(), ServiceStaleAfter) {
		t.Fatalf("first read: %+v", read)
	}
	f.mu.Lock()
	f.fail["or"] = &account.ServiceError{Reason: account.ServiceUnreachable, Message: "openrouter.ai: no answer"}
	f.mu.Unlock()
	f.clk.Advance(DefaultServiceInterval)
	f.r.Cycle(ctx)
	svc := f.accounts.get("or").Service
	if svc.Error == nil || svc.Error.Reason != account.ServiceUnreachable || svc.Usage.TotalUSD != 1.5 ||
		!svc.Stale(f.clk.Now(), ServiceStaleAfter) {
		t.Fatalf("after a failure: %+v", svc)
	}
	f.clk.Advance(DefaultServiceInterval - time.Second)
	f.r.Cycle(ctx)
	if f.readsOf("or") != 2 {
		t.Fatal("read again before an interval after the failure")
	}
	f.mu.Lock()
	delete(f.fail, "or")
	f.mu.Unlock()
	f.clk.Advance(time.Second)
	f.r.Cycle(ctx)
	if svc := f.accounts.get("or").Service; svc.Error != nil || svc.Stale(f.clk.Now(), ServiceStaleAfter) {
		t.Fatalf("recovered: %+v", svc)
	}
	// An old reading is stale without a failure.
	if svc := f.accounts.get("or").Service; !svc.Stale(f.clk.Now().Add(ServiceStaleAfter+time.Second),
		ServiceStaleAfter) {
		t.Fatal("an old reading is not stale")
	}
}

// No credit holds the account once; after the user acts on it, the next no-credit read holds it again. A 402 holds
// it too.
func TestServiceNoCredit(t *testing.T) {
	f := newServiceFixture(t)
	ctx := t.Context()
	f.noCredit = true
	f.r.Cycle(ctx)
	if len(f.held) != 1 || f.held[0] != "ds" || f.accounts.get("ds").Service.NoCreditAt.IsZero() {
		t.Fatalf("held: %v", f.held)
	}
	f.clk.Advance(DefaultServiceInterval)
	f.r.Cycle(ctx)
	if len(f.held) != 1 {
		t.Fatalf("held again without the user acting: %v", f.held)
	}
	f.clk.Advance(time.Minute)
	f.accounts.touch("ds")
	f.r.Request("ds")
	f.r.Cycle(ctx)
	if len(f.held) != 2 {
		t.Fatalf("not held again after an edit: %v", f.held)
	}
	f.mu.Lock()
	f.fail["or"] = &account.ServiceError{Reason: account.ServiceNoCredit, Message: "openrouter.ai: 402"}
	f.mu.Unlock()
	if _, err := f.r.Refresh(ctx, "or"); err == nil {
		t.Fatal("a 402 refreshed without an error")
	}
	if len(f.held) != 3 || f.held[2] != "or" {
		t.Fatalf("a 402 did not hold: %v", f.held)
	}
}

func TestServiceRefresh(t *testing.T) {
	f := newServiceFixture(t)
	ctx := t.Context()
	svc, err := f.r.Refresh(ctx, "or")
	if err != nil || svc.Usage == nil || f.readsOf("or") != 1 {
		t.Fatalf("refresh: %+v, %v", svc, err)
	}
	if _, err := f.r.Refresh(ctx, "z"); !errors.Is(err, ErrPassive) {
		t.Fatalf("no service: %v", err)
	}
	if _, err := f.r.Refresh(ctx, "nope"); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// A panicking reader is a failure, not a crash.
	f.r.o.Read = func(context.Context, account.Provider, string) (account.ServiceReading, error) {
		panic("boom")
	}
	var se *account.ServiceError
	if _, err := f.r.Refresh(ctx, "or"); !errors.As(err, &se) || se.Reason != account.ServiceFailed {
		t.Fatalf("panic: %v", err)
	}
}

// Run reads at once, then sleeps until the next read is due, and wakes for a turn.
func TestServiceRun(t *testing.T) {
	f := newServiceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.r.Run(ctx); close(done) }()
	if !f.clk.WaitTimers(1, 5*time.Second) || f.readsOf("or") != 1 {
		t.Fatalf("first cycle: %d reads", f.readsOf("or"))
	}
	f.r.AfterTurn("or")
	// The wake stops the old timer and sets one for the debounce.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if f.clk.WaitTimers(1, 5*time.Second) {
			f.clk.Advance(DefaultServiceDebounce)
		}
		if f.readsOf("or") == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.readsOf("or") != 2 {
		t.Fatalf("after a turn: %d reads", f.readsOf("or"))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
