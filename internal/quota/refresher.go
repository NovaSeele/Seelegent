package quota

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
)

// Refresher defaults.
const (
	// DefaultInterval is the refresh interval when none is configured (quotaRefreshMinutes defaults to 10).
	DefaultInterval = 10 * time.Minute
	// DefaultTimeout bounds the read of one account (the readers have shorter limits of their own).
	DefaultTimeout = 2 * time.Minute
	// DefaultParallel is how many accounts are read at the same time.
	DefaultParallel = 4
	// jitterFraction spreads the cycles: each wait is the interval ±10 %.
	jitterFraction = 0.1
	// NeedsLoginRecheck is how often RefreshAll still reads an account that is known to be signed out. One
	// misread answer must not drop an account for good, but a really signed-out account should not run a CLI every
	// cycle either: every 6 hours is cheap and bounds the damage of a wrong "signed out".
	NeedsLoginRecheck = 6 * time.Hour
)

// Accounts lists the accounts; *account.Manager satisfies it.
type Accounts interface {
	List(ctx context.Context) ([]account.Account, error)
}

// EnvFunc returns the profile and run environment a reader uses for an account.
type EnvFunc func(ctx context.Context, a account.Account) (account.Profile, []string, error)

// RefresherOptions configure a Refresher. Accounts, Env, Store and Readers are required.
type RefresherOptions struct {
	Accounts Accounts
	Env      EnvFunc
	Store    *Store
	// Readers are the quota readers by backend name.
	Readers map[string]backend.QuotaReader
	// Interval returns the time between cycles; it is asked before every wait, so a changed quotaRefreshMinutes
	// applies from the next cycle. Nil or a non-positive result means DefaultInterval.
	Interval func() time.Duration
	// Timeout bounds the read of one account (DefaultTimeout when zero).
	Timeout time.Duration
	// Parallel is how many accounts are read at once, by cycles and requests together (DefaultParallel when
	// zero). Refresh called directly is not limited.
	Parallel int
	// Clock is the time source (the system clock when nil).
	Clock clock.Clock
	// Jitter returns a random number in [0, n) (math/rand/v2 when nil).
	Jitter func(n int64) int64
	// Logger receives one line per failed read (discarded when nil).
	Logger *slog.Logger
	// OnUpdate is called with every stored snapshot (the pool uses it to wake waiting tasks).
	OnUpdate func(Snapshot)

	// onWait is called when a refresh starts waiting for a read of the account already in flight (tests).
	onWait func(accountID string)
	// onWoken is called when a fresh refresh woke from an older read and is about to look again (tests).
	onWoken func(accountID string)
}

// Result is the outcome of reading one account.
type Result struct {
	AccountID string
	Snapshot  Snapshot
	// Err is the read or store error; the snapshot then keeps the last good numbers and records the error.
	Err error
}

// Refresher reads the quota of the accounts on a timer and stores it. A failing or slow account never holds up
// the others or the caller: each runs in its own goroutine with a time limit, panics are recovered into errors,
// and the first cycle starts in the background (Run). An account is never read twice at the same time: a cycle
// shares a read already in flight, while an on-demand Refresh waits for it and then reads again, because a read
// that started before the request (for example before a sign-in) may answer for an older state.
type Refresher struct {
	o       RefresherOptions
	kick    chan struct{}
	request chan struct{}
	sem     chan struct{} // Parallel slots, shared by cycles and requests

	mu       sync.Mutex
	inflight map[string]*refreshCall
	last     map[string]*refreshCall // the last finished read per account
	gen      uint64                  // number of reads started so far
	pending  map[string]bool         // accounts asked for with Request, not yet read
}

type refreshCall struct {
	gen  uint64 // r.gen when the read started
	done chan struct{}
	res  Result
	// aborted: the context of the caller that started the read ended, so res says nothing about the account (a
	// reader's own time limit is not this: its error is a real result, shared and kept like any other).
	aborted bool
}

// NewRefresher returns a refresher.
func NewRefresher(o RefresherOptions) (*Refresher, error) {
	if o.Accounts == nil || o.Env == nil || o.Store == nil {
		return nil, errors.New("quota refresher: accounts, env and store are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Parallel <= 0 {
		o.Parallel = DefaultParallel
	}
	if o.Jitter == nil {
		o.Jitter = rand.Int64N
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Refresher{o: o, kick: make(chan struct{}, 1), request: make(chan struct{}, 1),
		sem: make(chan struct{}, o.Parallel), inflight: map[string]*refreshCall{}, last: map[string]*refreshCall{},
		pending: map[string]bool{}}, nil
}

// Run refreshes every account at once and then once per interval (±10 % jitter) until ctx ends, and serves
// Request meanwhile. Start it in its own goroutine; it never fails.
func (r *Refresher) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.serveRequests(ctx)
	}()
	defer wg.Wait()
	for {
		r.RefreshAll(ctx)
		t := r.o.Clock.NewTimer(r.nextWait())
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-r.kick:
			t.Stop()
		case <-t.C():
		}
	}
}

// Request asks Run to read one account soon, in the background (for example to confirm that a run refused for its
// sign-in really means the account is signed out). It never blocks; repeated requests for an account that has not
// been read yet are merged. The read has the semantics of Refresh.
func (r *Refresher) Request(id string) {
	r.mu.Lock()
	r.pending[id] = true
	r.mu.Unlock()
	select {
	case r.request <- struct{}{}:
	default:
	}
}

// serveRequests reads the accounts asked for with Request until ctx ends.
func (r *Refresher) serveRequests(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.request:
		}
		r.mu.Lock()
		ids := make([]string, 0, len(r.pending))
		for id := range r.pending {
			ids = append(ids, id)
		}
		clear(r.pending)
		r.mu.Unlock()
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case r.sem <- struct{}{}: // requests share the Parallel limit with the cycles
				case <-ctx.Done():
					return
				}
				defer func() { <-r.sem }()
				if _, err := r.Refresh(ctx, id); err != nil && ctx.Err() == nil {
					r.o.Logger.Debug("quota refresh on request", "account", id, "err", err)
				}
			}()
		}
	}
}

// Kick starts the next cycle of Run now (for example after quotaRefreshMinutes changed).
func (r *Refresher) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// nextWait returns the interval with jitter.
func (r *Refresher) nextWait() time.Duration {
	d := DefaultInterval
	if r.o.Interval != nil {
		if v := r.o.Interval(); v > 0 {
			d = v
		}
	}
	span := int64(float64(d) * jitterFraction)
	if span > 0 {
		d += time.Duration(r.o.Jitter(2*span+1) - span)
	}
	return d
}

// RefreshAll reads every account that has active quota: enabled, signed in through the CLI (not an API key) and
// with a reader for its backend. An account known to be signed out is read only when its last read is
// NeedsLoginRecheck old (a sign-in, Refresh or Request reads it at once). It returns one result per account read,
// in the order of Accounts.List.
func (r *Refresher) RefreshAll(ctx context.Context) []Result {
	accounts, err := r.o.Accounts.List(ctx)
	if err != nil {
		r.o.Logger.Warn("quota refresh: list accounts", "err", err)
		return nil
	}
	cached, err := r.o.Store.All(ctx)
	if err != nil {
		r.o.Logger.Warn("quota refresh: read cache", "err", err)
		cached = map[string]Snapshot{}
	}
	var todo []account.Account
	for i := range accounts {
		a := &accounts[i]
		if !a.Enabled || a.Kind == account.KindAPIKey || r.o.Readers[a.Backend] == nil {
			continue
		}
		if snap, ok := cached[a.ID]; ok && snap.ErrorKind() == KindNeedsLogin &&
			r.o.Clock.Now().Sub(snap.Error.At) < NeedsLoginRecheck {
			continue
		}
		todo = append(todo, *a)
	}
	results := make([]Result, len(todo))
	var wg sync.WaitGroup
	for i := range todo {
		a := todo[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case r.sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = Result{AccountID: a.ID, Err: ctx.Err()}
				return
			}
			defer func() { <-r.sem }()
			results[i] = r.refresh(ctx, a, false)
		}()
	}
	wg.Wait()
	return results
}

// Refresh reads one account now, whatever its last state (use it after a sign-in). The answer always comes from a
// read that started after the call: a read already in flight is waited for, not shared. It fails with
// account.ErrNotFound for an unknown id and ErrPassive for an API-key account.
func (r *Refresher) Refresh(ctx context.Context, id string) (Snapshot, error) {
	accounts, err := r.o.Accounts.List(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("refresh quota of %s: %w", id, err)
	}
	for i := range accounts {
		if a := accounts[i]; a.ID == id {
			if a.Kind == account.KindAPIKey {
				return Snapshot{}, ErrPassive
			}
			res := r.refresh(ctx, a, true)
			return res.Snapshot, res.Err
		}
	}
	return Snapshot{}, fmt.Errorf("refresh quota of %s: %w", id, account.ErrNotFound)
}

// refresh reads one account; reads of one account never overlap. Without fresh it shares a read already in
// flight. With fresh it shares only a read that started after this call (another fresh request that came later
// while it waited); an older read is waited for and then a new one is started.
func (r *Refresher) refresh(ctx context.Context, a account.Account, fresh bool) Result {
	r.mu.Lock()
	after := r.gen
	for {
		// A read that started after this call and has already finished answers it too: a fresh request woken
		// together with another one must not read a third time just because the other's read ended first.
		if l := r.last[a.ID]; fresh && l != nil && l.gen > after {
			r.mu.Unlock()
			return l.res
		}
		c, ok := r.inflight[a.ID]
		if !ok {
			break
		}
		join := !fresh || c.gen > after
		r.mu.Unlock()
		if r.o.onWait != nil {
			r.o.onWait(a.ID)
		}
		select {
		case <-c.done:
		case <-ctx.Done():
			return Result{AccountID: a.ID, Err: ctx.Err()}
		}
		if join && (!c.aborted || ctx.Err() != nil) {
			return c.res
		}
		if r.o.onWoken != nil {
			r.o.onWoken(a.ID)
		}
		r.mu.Lock()
		if join {
			// The shared read ended only because its own caller went away: read again for this one, sharing
			// anything that starts from now on.
			after = c.gen
		}
	}
	r.gen++
	c := &refreshCall{gen: r.gen, done: make(chan struct{})}
	r.inflight[a.ID] = c
	r.mu.Unlock()

	c.res = r.readAndStore(ctx, a)
	c.aborted = ctx.Err() != nil

	r.mu.Lock()
	delete(r.inflight, a.ID)
	if !c.aborted {
		r.last[a.ID] = c
	}
	r.mu.Unlock()
	close(c.done)
	return c.res
}

// readAndStore reads one account and stores the merged snapshot. A failure is recorded on that account only.
func (r *Refresher) readAndStore(ctx context.Context, a account.Account) Result {
	res := Result{AccountID: a.ID}
	q, readErr := r.read(ctx, a)
	if ctx.Err() != nil {
		// The daemon is stopping: an interrupted read says nothing about the account.
		res.Err = ctx.Err()
		return res
	}
	prev, found, err := r.o.Store.Get(ctx, a.ID)
	if err != nil {
		res.Err = err
		return res
	}
	now := r.o.Clock.Now().UTC()
	snap := Merge(prev, found, a, q, readErr, now)
	if err := r.o.Store.Put(ctx, snap, now); err != nil {
		res.Err = errors.Join(readErr, err)
		r.o.Logger.Warn("quota refresh: store", "account", a.ID, "err", err)
		return res
	}
	res.Snapshot, res.Err = snap, readErr
	if readErr != nil {
		r.o.Logger.Warn("quota refresh failed", "account", a.ID, "backend", a.Backend, "kind", KindOf(readErr),
			"err", snap.Error.Message)
	}
	if r.o.OnUpdate != nil {
		r.o.OnUpdate(snap)
	}
	return res
}

// read runs the account's reader with the time limit, turning a panic into an error.
func (r *Refresher) read(ctx context.Context, a account.Account) (q backend.Quota, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("quota reader of %s panicked: %v", a.Backend, p)
		}
	}()
	reader := r.o.Readers[a.Backend]
	if reader == nil {
		return backend.Quota{}, ErrNoReader
	}
	prof, env, err := r.o.Env(ctx, a)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("prepare quota read: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, r.o.Timeout)
	defer cancel()
	return reader.ReadQuota(ctx, prof, env)
}

// Merge returns the new snapshot of account a after a read at now: the reader's answer when it worked; otherwise
// the previous snapshot (found reports whether there was one) with the error recorded, so the account keeps its
// last numbers and a no-plan lock is lifted only by a successful read.
func Merge(prev Snapshot, found bool, a account.Account, q backend.Quota, readErr error, now time.Time) Snapshot {
	if readErr == nil {
		return FromBackend(a.ID, a.Backend, q, now)
	}
	snap := prev
	if !found {
		snap = Snapshot{Groups: []Group{}}
	}
	snap.AccountID, snap.Backend = a.ID, a.Backend
	if errors.Is(readErr, ErrPassive) {
		snap.Source, snap.Error = SourcePassive, nil
		return snap
	}
	if snap.Source == "" {
		snap.Source = SourceActive
	}
	snap.Error = newError(readErr, now)
	return snap
}
