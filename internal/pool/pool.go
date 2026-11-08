// Package pool decides which worker account runs a turn and keeps count of the turns running on each account.
//
// # Selection
//
// For a request (backend, quota group, model, provider, preferred and excluded accounts) an account is a candidate
// when it belongs to the backend, is enabled and usable (signed in, has its key, its plan allows the backend, it is
// not the twin of another account), is not excluded, matches the provider (a request without a provider only gets
// sign-in accounts, a request with one only that provider's API-key accounts) and can run the model (codex
// sign-ins with a known catalog get the closest model they have). A candidate is resting while a cooldown of the
// group is in force or, for accounts with active quota, while a window of the group is at or below minRemaining;
// it is busy while it runs maxConcurrentPerAccount turns. Among the free candidates the preferred ones are tried
// first; each list is ordered by priority (high first), then running turns (few first), then remaining quota
// (most first), then age, so the choice is deterministic.
//
// # Leases
//
// A turn holds its account through a Lease from Acquire (or Wait). Release is idempotent, so callers write
// `defer lease.Release()` right after acquiring and no error path can leak a slot (bug B3b of the old version).
// After a daemon restart a still-running worker is counted again with Reattach, which never counts a task twice
// (bug B3a); Acquire refuses an account the task already holds.
//
// # Resting
//
// When a turn reports quota_exhausted, MarkExhausted rests the account for that quota group until the governing
// windows reset (read fresh when possible), the provider's hint, or a default; accounts whose quota cannot be read
// (API keys, backends without a quota reader) rest for passiveCooldownMinutes. A rest ends by itself, or earlier
// when a later quota read shows the group usable again. A run refused for its sign-in marks the account signed
// out (MarkAuthFailed) and asks for a background quota read to confirm. Any confirming read that does not itself
// say "signed out" clears the mark: a good read, and also an inconclusive one (timeout, HTTP 5xx, format change),
// because quota.Merge replaces the previous error with the new one. That is deliberate: a mark that only a clean
// read could lift would stick while the quota source is down. A new key clears the mark of an API-key account.
package pool

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/quota"
)

// Accounts lists the accounts and records runs that proved an API key; *account.Manager satisfies it.
type Accounts interface {
	List(ctx context.Context) ([]account.Account, error)
	// MarkRunOK records that a turn that started at started ended well on the account (account.Repo.MarkRunOK).
	MarkRunOK(ctx context.Context, id string, started time.Time) (bool, error)
}

// Quotas is the quota cache; *quota.Store satisfies it.
type Quotas interface {
	// All returns the cached snapshots by account id.
	All(ctx context.Context) (map[string]quota.Snapshot, error)
	// MarkNeedsLogin records that a run of the account was refused for its sign-in or key.
	MarkNeedsLogin(ctx context.Context, id, backend, message string, at time.Time) error
}

// RefreshFunc reads one account's quota now (quota.Refresher.Refresh).
type RefreshFunc func(ctx context.Context, id string) (quota.Snapshot, error)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Options configure a Pool. Accounts, Quotas, DB and Settings are required.
type Options struct {
	Accounts Accounts
	Quotas   Quotas
	DB       DB
	// Settings returns the current settings (maxConcurrentPerAccount, minRemaining, passiveCooldownMinutes); it is
	// asked on every decision, so changes apply at once.
	Settings func() config.Settings
	// Refresh, when set, reads an account's quota before it is rested, so the rest ends when its windows reset.
	Refresh RefreshFunc
	// Confirm, when set, asks for a background quota read of a sign-in account that a run reported as signed out
	// (quota.Refresher.Request); a successful read clears the mark. It must not block.
	Confirm func(accountID string)
	// ActiveQuota reports whether a backend has a quota reader. Sign-in accounts of a backend without one are
	// treated like API-key accounts (passive): usable with unknown quota, resting only after a quota error, never
	// waiting for a first read that cannot come. Nil means every backend has a reader.
	ActiveQuota func(backend string) bool
	// Clock is the time source (the system clock when nil).
	Clock clock.Clock
	// Logger receives slot and rest events (discarded when nil).
	Logger *slog.Logger
}

// Pool selects accounts and counts their running turns. It is safe for concurrent use; selection and taking the
// slot happen in one critical section, so two tasks never take the last slot of an account together.
type Pool struct {
	o Options

	mu     sync.Mutex
	leases map[string]map[string]*Lease // account id -> task id -> lease
	held   map[string]int               // account id -> holds (Hold): no new lease while > 0
	signal chan struct{}                // closed and replaced whenever something may have become available
	anon   int                          // counter for leases without a task id
}

// New returns a pool.
func New(o Options) (*Pool, error) {
	if o.Accounts == nil || o.Quotas == nil || o.DB == nil || o.Settings == nil {
		return nil, errors.New("pool: accounts, quotas, db and settings are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.ActiveQuota == nil {
		o.ActiveQuota = func(string) bool { return true }
	}
	return &Pool{o: o, leases: map[string]map[string]*Lease{}, held: map[string]int{}, signal: make(chan struct{})},
		nil
}

// Status says whether an account can take work.
type Status string

// Account statuses.
const (
	// StatusOK: usable.
	StatusOK Status = "ok"
	// StatusError: the last quota read failed; the account stays usable with its last known numbers.
	StatusError Status = "error"
	// StatusNew: a sign-in account whose quota was never read; it becomes usable (or needs_login) after the first
	// read. Waiting tasks wait for it.
	StatusNew Status = "new"
	// StatusNeedsLogin: signed out; it must sign in again.
	StatusNeedsLogin Status = "needs_login"
	// StatusNoKey: an API-key account without a stored key.
	StatusNoKey Status = "no_key"
	// StatusNoPlan: the plan cannot use the backend; only a later successful quota read lifts it.
	StatusNoPlan Status = "no_plan"
	// StatusTwin: signed in to the same identity as an older enabled account, which takes the work (two accounts
	// of one identity share one quota).
	StatusTwin Status = "twin"
	// StatusDisabled: switched off by the user.
	StatusDisabled Status = "disabled"
)

// Usable reports whether an account with this status may be selected.
func (s Status) Usable() bool { return s == StatusOK || s == StatusError }

// Request describes the turn an account is wanted for.
type Request struct {
	// TaskID owns the lease (one lease per task and account).
	TaskID string
	// Backend is the worker CLI.
	Backend string
	// Group is the quota group of the model (backend.Backend.QuotaGroup).
	Group string
	// Model is the resolved model id.
	Model string
	// Provider selects API-key accounts of that provider; empty selects sign-in accounts only.
	Provider string
	// Account, when set, restricts the choice to that account (the owner of the task's conversation).
	Account string
	// Prefer lists accounts tried first, in this order when everything else is equal.
	Prefer []string
	// Exclude lists accounts that must not be chosen (for example those that failed this turn).
	Exclude []string
	// Allowed filters the models an account with a known catalog may fall back to (nil allows all).
	Allowed func(model string) bool
}

// Choice is the account selected for a request.
type Choice struct {
	Account account.Account
	// Model is the model to run: the requested one, or the closest one the account has (codex).
	Model string
	// Remaining is the fraction of the group's quota left (quota.UnknownRemaining when not known).
	Remaining      float64
	RemainingKnown bool
}

// Reason says why no account is free.
type Reason string

// Reasons.
const (
	// ReasonNoAccount: no account can ever take the request (none enabled, signed in and matching).
	ReasonNoAccount Reason = "no_account"
	// ReasonResting: every matching account rests (cooldown or quota used up).
	ReasonResting Reason = "resting"
	// ReasonBusy: every matching account that is not resting runs its maximum of turns.
	ReasonBusy Reason = "busy"
	// ReasonBudget: every matching account rests because it reached its budget (RestBudget). It matches
	// ErrAllResting: the request waits, until the user raises a budget, switches its stop off or changes a key.
	ReasonBudget Reason = "budget_reached"
)

// Errors matched by Unavailable; use errors.Is.
var (
	ErrNoAccount  = errors.New("no account can run this")
	ErrAllResting = errors.New("every account is resting")
	ErrAllBusy    = errors.New("every account is busy")
	// ErrAlreadyHeld: the task already holds a lease on the chosen account (release it first).
	ErrAlreadyHeld = errors.New("the task already holds this account")
)

// Unavailable is returned when no account is free. It matches ErrNoAccount, ErrAllResting or ErrAllBusy.
type Unavailable struct {
	Reason Reason `json:"reason"`
	// Until is when the first resting account is back (zero when unknown or when nothing rests).
	Until time.Time `json:"until,omitzero"`
	// Candidates, Resting and Busy count the matching accounts.
	Candidates int    `json:"candidates"`
	Resting    int    `json:"resting"`
	Busy       int    `json:"busy"`
	Backend    string `json:"backend"`
	Group      string `json:"group,omitempty"`
	// Capped is the maxConcurrentWorkers cap that holds the request although an account is free (0 when none does);
	// Running is how many worker turns run then.
	Capped  int `json:"capped,omitempty"`
	Running int `json:"running,omitempty"`
	// Slots are the busy accounts with their running turns, in the order of the accounts (ReasonBusy).
	Slots []BusySlot `json:"slots,omitempty"`
	// KeyProviders are the providers whose API-key accounts of the backend are free now, for a request that names
	// no provider and waits on its sign-in accounts (resting or busy). Such a request never takes them (keys cost
	// money); Error names them so the lead can pass provider. In account order, each once.
	KeyProviders []KeyProvider `json:"keyProviders,omitempty"`
	// NoCredit are the matching accounts left out because their credit ran out (MarkNoCredit). They do not count
	// as candidates: with nothing else the reason is no_account, and the caller fails rather than waits.
	NoCredit []NoCredit `json:"noCredit,omitempty"`
	// BudgetReached are the matching accounts that rest on their budget (RestBudget), counted in Resting.
	BudgetReached []BudgetHold `json:"budgetReached,omitempty"`
}

// BudgetHold is an API-key account that rests on its budget (Unavailable.BudgetReached).
type BudgetHold struct {
	AccountID string  `json:"accountId"`
	Label     string  `json:"label,omitempty"`
	Provider  string  `json:"provider"`
	BudgetUSD float64 `json:"budgetUsd"`
	SpentUSD  float64 `json:"spentUsd"`
}

// budgetHold describes account a, which rests on its budget.
func budgetHold(a *account.Account) BudgetHold {
	h := BudgetHold{AccountID: a.ID, Label: a.Label, Provider: a.Provider, SpentUSD: a.Spent.USD}
	if a.BudgetUSD != nil {
		h.BudgetUSD = *a.BudgetUSD
	}
	return h
}

// restingReason is the reason of a request that only waits on resting accounts: ReasonBudget when each of them
// rests on its budget, else ReasonResting.
func (u *Unavailable) restingReason() Reason {
	if u.Resting > 0 && len(u.BudgetReached) == u.Resting {
		return ReasonBudget
	}
	return ReasonResting
}

// NoCredit is an account whose credit ran out (Unavailable.NoCredit).
type NoCredit struct {
	AccountID string `json:"accountId"`
	Label     string `json:"label,omitempty"`
	// Provider is the API-key account's provider ("" for a sign-in account).
	Provider string `json:"provider,omitempty"`
}

// KeyProvider is a provider with a free API-key account (Unavailable.KeyProviders).
type KeyProvider struct {
	ID string `json:"id"`
	// NeedsModel: the provider maps no tier to a model, so a task on it needs an explicit model. The pool does
	// not read providers: the caller that does sets it before Error.
	NeedsModel bool `json:"needsModel,omitempty"`
}

// BusySlot is a matching account that has no free slot: Running of Limit (maxConcurrentPerAccount) turns run on
// it, or a sign-out holds it (Held).
type BusySlot struct {
	AccountID string `json:"accountId"`
	Running   int    `json:"running"`
	Limit     int    `json:"limit"`
	Held      bool   `json:"held,omitempty"`
}

// maxSlotsShown is how many busy accounts Unavailable.Error names, and maxKeyProvidersShown how many providers.
const (
	maxSlotsShown        = 3
	maxKeyProvidersShown = 3
)

// Error implements error.
func (u *Unavailable) Error() string {
	var b strings.Builder
	switch u.Reason {
	case ReasonNoAccount:
		if len(u.NoCredit) > 0 {
			// The only thing to say: the lead can do nothing else, and the generic text would hide it.
			u.writeNoCredit(&b)
			return b.String()
		}
		fmt.Fprintf(&b, "no enabled, signed-in %s account can run this", u.Backend)
	case ReasonBudget:
		// What the user must do is all there is to say: no time ends the wait.
		u.writeBudget(&b)
		if len(u.NoCredit) > 0 {
			b.WriteString("; ")
			u.writeNoCredit(&b)
		}
		return b.String()
	case ReasonResting:
		fmt.Fprintf(&b, "all %d %s accounts are resting", u.Resting, u.Backend)
		if !u.Until.IsZero() {
			fmt.Fprintf(&b, " until %s", u.Until.UTC().Format(time.RFC3339))
		}
	default:
		if u.Capped > 0 {
			fmt.Fprintf(&b, "%d worker turns run already (maxConcurrentWorkers %d)", u.Running, u.Capped)
			break
		}
		if len(u.Slots) == 0 {
			fmt.Fprintf(&b, "all %s accounts are busy (%d busy, %d resting)", u.Backend, u.Busy, u.Resting)
			break
		}
		// "acc6: 2 of 2 running": which accounts are full, the reason a task waits although quota is left.
		for i, sl := range u.Slots {
			if i == maxSlotsShown {
				fmt.Fprintf(&b, "; %d more busy", len(u.Slots)-i)
				break
			}
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%s: %d of %d running", sl.AccountID, sl.Running, sl.Limit)
			if sl.Held {
				b.WriteString(", signing out")
			}
		}
		if u.Resting > 0 {
			fmt.Fprintf(&b, "; %d resting", u.Resting)
		}
	}
	if u.Group != "" {
		fmt.Fprintf(&b, " (quota group %s)", u.Group)
	}
	u.writeKeyProviders(&b)
	if len(u.BudgetReached) > 0 {
		b.WriteString("; ")
		u.writeBudget(&b)
	}
	if len(u.NoCredit) > 0 {
		b.WriteString("; ")
		u.writeNoCredit(&b)
	}
	return b.String()
}

// writeBudget names the accounts at their budget: "acc9 reached its $200 budget: raise the budget or change the
// key".
func (u *Unavailable) writeBudget(b *strings.Builder) {
	if len(u.BudgetReached) == 1 {
		h := u.BudgetReached[0]
		fmt.Fprintf(b, "%s reached its %s budget: raise the budget or change the key", h.AccountID,
			account.FormatUSD(h.BudgetUSD))
		return
	}
	for i, h := range u.BudgetReached {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s (%s)", h.AccountID, account.FormatUSD(h.BudgetUSD))
	}
	b.WriteString(" reached their budgets: raise the budgets or change the keys")
}

// writeNoCredit names the accounts without credit, each provider once, and what to do: a key is topped up or
// changed ("the anthropic key has no credit left; top up or change the key"); a sign-in account has no key, it is
// switched off and on after a top-up ("the claude account work has no credit left; top up, then switch the account
// off and on").
func (u *Unavailable) writeNoCredit(b *strings.Builder) {
	var names []string
	keys, signIns := false, false
	for _, nc := range u.NoCredit {
		name := "the " + nc.Provider + " key"
		if nc.Provider == "" {
			name = "the " + u.Backend + " account " + cmp.Or(nc.Label, nc.AccountID)
		}
		if slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
		if nc.Provider == "" {
			signIns = true
		} else {
			keys = true
		}
	}
	one := len(names) == 1
	verb, todo := "have", ""
	if one {
		verb = "has"
	}
	switch {
	case keys && signIns:
		todo = "top up, then change the keys or switch the accounts off and on"
	case signIns && one:
		todo = "top up, then switch the account off and on"
	case signIns:
		todo = "top up, then switch the accounts off and on"
	case one:
		todo = "top up or change the key"
	default:
		todo = "top up or change the keys"
	}
	fmt.Fprintf(b, "%s %s no credit left; %s", strings.Join(names, ", "), verb, todo)
}

// writeKeyProviders adds the free API-key accounts: "; free API-key accounts: pass provider: anthropic and a
// model, or provider: zai".
func (u *Unavailable) writeKeyProviders(b *strings.Builder) {
	if len(u.KeyProviders) == 0 {
		return
	}
	b.WriteString("; free API-key accounts: pass ")
	for i, kp := range u.KeyProviders {
		if i == maxKeyProvidersShown {
			fmt.Fprintf(b, ", or %d more", len(u.KeyProviders)-i)
			break
		}
		if i > 0 {
			b.WriteString(", or ")
		}
		fmt.Fprintf(b, "provider: %s", kp.ID)
		if kp.NeedsModel {
			b.WriteString(" and a model")
		}
	}
}

// Is matches the reason's sentinel error.
func (u *Unavailable) Is(target error) bool {
	switch u.Reason {
	case ReasonNoAccount:
		return target == ErrNoAccount
	case ReasonResting, ReasonBudget:
		return target == ErrAllResting
	case ReasonBusy:
		return target == ErrAllBusy
	}
	return false
}

// Notify wakes the tasks waiting for an account (call it when accounts, quota or settings change).
func (p *Pool) Notify() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notifyLocked()
}

func (p *Pool) notifyLocked() {
	close(p.signal)
	p.signal = make(chan struct{})
}

// Changed returns a channel that is closed at the next Notify or release: a task waiting for an account to be added
// or signed in waits on it.
func (p *Pool) Changed() <-chan struct{} { return p.changed() }

// changed returns a channel that is closed at the next Notify.
func (p *Pool) changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.signal
}

// Hold keeps an idle account from new leases until release runs: Acquire, Select, Wait and Handover count it as
// busy. It fails (ok false) when the account runs a turn now. The check and the hold are one critical section,
// so nothing can start on the account between them; a sign-out holds the account while it replaces the profile.
// release is idempotent and wakes waiting tasks.
func (p *Pool) Hold(accountID string) (release func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.leases[accountID]) > 0 {
		return func() {}, false
	}
	p.held[accountID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.held[accountID]--
			if p.held[accountID] <= 0 {
				delete(p.held, accountID)
			}
			p.notifyLocked()
		})
	}, true
}

// Busy returns the number of turns running on an account.
func (p *Pool) Busy(accountID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.leases[accountID])
}
