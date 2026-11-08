package quota

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/clock"
)

// Spend service cadence. The service of an API-key account (account.Provider.SpendService: OpenRouter, DeepSeek,
// Moonshot) is read a little after each turn that ran on its key, and otherwise at most once per
// DefaultServiceInterval.
const (
	// DefaultServiceInterval is the longest time between two reads of one account's spend service.
	DefaultServiceInterval = 60 * time.Minute
	// DefaultServiceDebounce is how long after a turn ended the service is read: turns that end meanwhile share the
	// read, and the service has time to count the turn.
	DefaultServiceDebounce = 30 * time.Second
	// ServiceStaleAfter is the age at which a reading counts as stale although no read failed (missed reads, a
	// stopped daemon).
	ServiceStaleAfter = 2 * DefaultServiceInterval
)

// ServiceAccounts is what the service refresher needs of the accounts; *account.Manager satisfies it.
type ServiceAccounts interface {
	List(ctx context.Context) ([]account.Account, error)
	GetProvider(ctx context.Context, id string) (account.Provider, error)
	Credentials(ctx context.Context, a account.Account) (account.Provider, string, error)
	RecordService(ctx context.Context, id string, started time.Time, reading *account.ServiceReading,
		readErr *account.ServiceError, marked bool) (account.ServiceSpend, []account.BudgetCrossing, bool, error)
}

// ServiceReadFunc reads a provider's spend service with a key (backend.ReadServiceSpend). A failure is an
// *account.ServiceError.
type ServiceReadFunc func(ctx context.Context, p account.Provider, key string) (account.ServiceReading, error)

// ServiceOptions configure a ServiceRefresher. Accounts and Read are required.
type ServiceOptions struct {
	Accounts ServiceAccounts
	Read     ServiceReadFunc
	// Interval and Debounce default to DefaultServiceInterval and DefaultServiceDebounce.
	Interval time.Duration
	Debounce time.Duration
	// Clock is the time source (the system clock when nil).
	Clock clock.Clock
	// Logger receives a line when an account's read starts failing, fails differently or recovers (discarded when
	// nil). It never receives the key or a response body.
	Logger *slog.Logger
	// NoCredit holds an account whose service says it has no credit left (pool.MarkNoCredit). It is called when a
	// read finds no credit while the account was not held for it yet, or was lifted since (a key test, an edit).
	NoCredit func(ctx context.Context, accountID, message string) error
	// OnUpdate is called after a read was stored, with the budget crossings it caused (log them, wake the pool).
	OnUpdate func(ctx context.Context, accountID string, crossings []account.BudgetCrossing)
}

// ServiceRefresher reads the spend services of API-key accounts: after each turn (debounced) and at most once per
// interval otherwise. A failed read keeps the last values and records the failure, which marks them stale. Reads
// run one at a time, each with the reader's own time limit.
type ServiceRefresher struct {
	o    ServiceOptions
	wake chan struct{}
	// readMu runs reads one at a time, so two reads of one account never overlap.
	readMu sync.Mutex

	mu  sync.Mutex
	due map[string]time.Time // debounced reads asked for after a turn, by account
	// failing remembers the last failure reason per account, so a lasting failure is logged once.
	failing map[string]string
}

// NewServiceRefresher returns a refresher.
func NewServiceRefresher(o ServiceOptions) (*ServiceRefresher, error) {
	if o.Accounts == nil || o.Read == nil {
		return nil, errors.New("service refresher: accounts and read are required")
	}
	if o.Interval <= 0 {
		o.Interval = DefaultServiceInterval
	}
	if o.Debounce <= 0 {
		o.Debounce = DefaultServiceDebounce
	}
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &ServiceRefresher{o: o, wake: make(chan struct{}, 1), due: map[string]time.Time{},
		failing: map[string]string{}}, nil
}

// AfterTurn asks for a read of the account's service a little after a turn on it ended (Debounce). Turns that end
// before the read share it. It never blocks; an account without a spend service is skipped when the read is due.
func (r *ServiceRefresher) AfterTurn(id string) {
	r.mu.Lock()
	if _, ok := r.due[id]; !ok {
		r.due[id] = r.o.Clock.Now().Add(r.o.Debounce)
	}
	r.mu.Unlock()
	r.signal()
}

// Request asks for a read of the account's service as soon as possible (a key was just stored). It never blocks.
func (r *ServiceRefresher) Request(id string) {
	r.mu.Lock()
	r.due[id] = r.o.Clock.Now()
	r.mu.Unlock()
	r.signal()
}

func (r *ServiceRefresher) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run reads the accounts that are due (asked for, or not read for Interval) until ctx ends, then sleeps until the
// next one is due. Start it in its own goroutine; it never fails.
func (r *ServiceRefresher) Run(ctx context.Context) {
	for {
		next := r.Cycle(ctx)
		wait := next.Sub(r.o.Clock.Now())
		t := r.o.Clock.NewTimer(max(wait, 0))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		case <-r.wake:
			t.Stop()
		}
	}
}

// Cycle reads every account that is due now and returns when the next read is due (at most Interval away).
func (r *ServiceRefresher) Cycle(ctx context.Context) time.Time {
	now := r.o.Clock.Now()
	next := now.Add(r.o.Interval)
	accounts, err := r.o.Accounts.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.o.Logger.Warn("spend service: list accounts", "err", err)
		}
		return next
	}
	r.mu.Lock()
	asked := maps.Clone(r.due)
	r.mu.Unlock()
	providers := map[string]*account.Provider{}
	gone := make(map[string]bool, len(asked))
	for id := range asked {
		gone[id] = true
	}
	for i := range accounts {
		delete(gone, accounts[i].ID)
	}
	for id := range gone {
		r.forget(id) // asked for, but removed since
	}
	for i := range accounts {
		a := &accounts[i]
		p := r.serviceProvider(ctx, a, providers)
		if p == nil {
			r.forget(a.ID)
			continue
		}
		due := a.Service.LastAttempt().Add(r.o.Interval)
		if a.Service == nil {
			due = now
		}
		if t, ok := asked[a.ID]; ok && t.Before(due) {
			due = t
		}
		if due.After(now) {
			if due.Before(next) {
				next = due
			}
			continue
		}
		r.forget(a.ID)
		if _, err := r.readAccount(ctx, a); err != nil && ctx.Err() != nil {
			return next
		}
	}
	// Asked-for accounts that are gone or have no service are dropped above (forget).
	return next
}

// forget drops a pending debounced read.
func (r *ServiceRefresher) forget(id string) {
	r.mu.Lock()
	delete(r.due, id)
	r.mu.Unlock()
}

// serviceProvider returns the account's provider when the account is an enabled API-key account with a key whose
// provider has a spend service (nil otherwise). providers caches the providers of one cycle.
func (r *ServiceRefresher) serviceProvider(ctx context.Context, a *account.Account,
	providers map[string]*account.Provider) *account.Provider {
	if a.Kind != account.KindAPIKey || !a.Enabled || a.KeyHint == "" || a.Provider == "" {
		return nil
	}
	p, ok := providers[a.Provider]
	if !ok {
		got, err := r.o.Accounts.GetProvider(ctx, a.Provider)
		if err == nil {
			p = &got
		}
		providers[a.Provider] = p
	}
	if p == nil || !p.Enabled {
		return nil
	}
	if s, _ := p.SpendService(); s == "" {
		return nil
	}
	return p
}

// Refresh reads the service of one account now and returns what is stored (the last good values when the read
// failed, with the failure). It fails with account.ErrNotFound for an unknown id and ErrPassive for an account
// without a spend service.
func (r *ServiceRefresher) Refresh(ctx context.Context, id string) (account.ServiceSpend, error) {
	accounts, err := r.o.Accounts.List(ctx)
	if err != nil {
		return account.ServiceSpend{}, fmt.Errorf("read spend service of %s: %w", id, err)
	}
	for i := range accounts {
		a := &accounts[i]
		if a.ID != id {
			continue
		}
		if r.serviceProvider(ctx, a, map[string]*account.Provider{}) == nil {
			return account.ServiceSpend{}, ErrPassive
		}
		r.forget(id)
		return r.readAccount(ctx, a)
	}
	return account.ServiceSpend{}, fmt.Errorf("read spend service of %s: %w", id, account.ErrNotFound)
}

// readAccount reads one account's service and stores the result. The error is the read's (*account.ServiceError)
// or the store's.
func (r *ServiceRefresher) readAccount(ctx context.Context, a *account.Account) (account.ServiceSpend, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	started := r.o.Clock.Now()
	p, key, err := r.o.Accounts.Credentials(ctx, *a)
	if err != nil {
		return account.ServiceSpend{}, err
	}
	reading, readErr := r.read(ctx, p, key)
	if ctx.Err() != nil {
		return account.ServiceSpend{}, ctx.Err() // the daemon is stopping: an interrupted read says nothing
	}
	var (
		good    *account.ServiceReading
		failure *account.ServiceError
		message string
	)
	if readErr != nil {
		if !errors.As(readErr, &failure) {
			failure = &account.ServiceError{Reason: account.ServiceFailed, Message: readErr.Error()}
		}
		if failure.Reason == account.ServiceNoCredit {
			message = failure.Message
		}
	} else {
		good = &reading
		if reading.NoCredit {
			message = "the service says there is no credit left on this key's account"
		}
	}
	marked := false
	if message != "" && r.o.NoCredit != nil && needsHold(a) {
		if err := r.o.NoCredit(ctx, a.ID, message); err != nil {
			r.o.Logger.Warn("spend service: hold an account without credit", "account", a.ID, "err", err)
		} else {
			marked = true
		}
	}
	svc, crossings, stored, err := r.o.Accounts.RecordService(ctx, a.ID, started, good, failure, marked)
	if err != nil {
		r.o.Logger.Warn("spend service: store", "account", a.ID, "err", err)
		return account.ServiceSpend{}, err
	}
	r.logRead(a, failure)
	if stored && r.o.OnUpdate != nil {
		r.o.OnUpdate(ctx, a.ID, crossings)
	}
	if failure != nil {
		return svc, failure
	}
	return svc, nil
}

// needsHold reports whether a no-credit read should hold the account: it was not held by a read yet, or the user
// acted on it since (a passing key test or an edit lift a hold; see pool's noCredit).
func needsHold(a *account.Account) bool {
	if a.Service == nil || a.Service.NoCreditAt.IsZero() {
		return true
	}
	at := a.Service.NoCreditAt
	return a.UpdatedAt.After(at) || a.KeySetAt.After(at) ||
		(a.TestState() == account.TestOK && a.TestedAt.After(at))
}

// read runs the reader, turning a panic into an error.
func (r *ServiceRefresher) read(ctx context.Context, p account.Provider, key string) (q account.ServiceReading,
	err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &account.ServiceError{Reason: account.ServiceFailed, Message: fmt.Sprintf("the reader panicked: %v", v)}
		}
	}()
	return r.o.Read(ctx, p, key)
}

// logRead logs a failure once while it lasts (Warn when it starts or changes reason, Debug while it repeats) and
// one Info line when the account reads again. Messages hold the host and the failure, never the key or the body.
func (r *ServiceRefresher) logRead(a *account.Account, failure *account.ServiceError) {
	r.mu.Lock()
	was := r.failing[a.ID]
	if failure == nil {
		delete(r.failing, a.ID)
	} else {
		r.failing[a.ID] = failure.Reason
	}
	r.mu.Unlock()
	switch {
	case failure == nil && was != "":
		r.o.Logger.Info("spend service read recovered", "account", a.ID, "provider", a.Provider, "was", was)
	case failure == nil:
	case failure.Reason == was:
		r.o.Logger.Debug("spend service read failed again", "account", a.ID, "provider", a.Provider,
			"reason", failure.Reason)
	default:
		r.o.Logger.Warn("spend service read failed", "account", a.ID, "provider", a.Provider, "reason", failure.Reason,
			"err", failure.Message)
	}
}
