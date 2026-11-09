package pool

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/quota"
)

// view is what one decision is based on: accounts, quota, cooldowns and settings at one instant.
type view struct {
	now       time.Time
	settings  config.Settings
	accounts  []account.Account
	snaps     map[string]quota.Snapshot
	cooldowns map[string]map[string]Cooldown // account -> group -> cooldown
	status    map[string]Status
	twinOf    map[string]string
	passive   map[string]bool // accounts whose quota cannot be read (API key, or no reader for the backend)
}

// load reads the state a decision needs. Busy counts are not part of it: they are read under the pool's lock.
func (p *Pool) load(ctx context.Context) (*view, error) {
	accounts, err := p.o.Accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	snaps, err := p.o.Quotas.All(ctx)
	if err != nil {
		return nil, err
	}
	cds, err := p.listCooldowns(ctx)
	if err != nil {
		return nil, err
	}
	v := &view{
		now: p.o.Clock.Now().UTC(), settings: p.o.Settings(), accounts: accounts, snaps: snaps,
		cooldowns: map[string]map[string]Cooldown{}, status: map[string]Status{}, twinOf: map[string]string{},
		passive: map[string]bool{},
	}
	for _, c := range cds {
		if v.cooldowns[c.AccountID] == nil {
			v.cooldowns[c.AccountID] = map[string]Cooldown{}
		}
		v.cooldowns[c.AccountID][c.Group] = c
	}
	for i := range accounts {
		a := &accounts[i]
		snap, found := snaps[a.ID]
		v.passive[a.ID] = p.passive(a)
		v.status[a.ID] = statusOf(a, snap, found, v.passive[a.ID])
	}
	v.markTwins()
	return v, nil
}

// passive reports whether the account's quota cannot be read: an API-key account, or a sign-in account of a
// backend without a quota reader.
func (p *Pool) passive(a *account.Account) bool {
	return a.Kind == account.KindAPIKey || !p.o.ActiveQuota(a.Backend)
}

// statusOf derives an account's status from its record and its last quota read. passive marks an account whose
// quota is never read: without a read it is usable, not new.
func statusOf(a *account.Account, snap quota.Snapshot, found, passive bool) Status {
	switch {
	case !a.Enabled:
		return StatusDisabled
	case a.Kind == account.KindAPIKey:
		switch {
		case a.KeyHint == "":
			return StatusNoKey
		case found && snap.ErrorKind() == quota.KindNeedsLogin && !snap.Error.At.Before(a.KeySetAt):
			return StatusNeedsLogin // the provider refused the key; only a newer key lifts it
		}
		return StatusOK
	case !found && passive:
		return StatusOK
	case !found:
		return StatusNew
	case snap.NoPlan:
		return StatusNoPlan
	}
	switch snap.ErrorKind() {
	case "", quota.KindTokenExpired:
		return StatusOK
	case quota.KindNeedsLogin:
		return StatusNeedsLogin
	}
	return StatusError
}

// markTwins gives every usable account signed in to the same identity as an older usable account of the same
// backend the status twin. The oldest account (then the smallest id) keeps the work, whatever order the quota
// reads finished in (the old version disabled whichever twin it read second).
func (v *view) markTwins() {
	type key struct{ backend, identity string }
	first := map[key]*account.Account{}
	order := make([]*account.Account, 0, len(v.accounts))
	for i := range v.accounts {
		order = append(order, &v.accounts[i])
	}
	sort.SliceStable(order, func(i, j int) bool { return older(order[i], order[j]) })
	for _, a := range order {
		id := v.snaps[a.ID].Identity
		if id == "" || a.Kind == account.KindAPIKey || !v.status[a.ID].Usable() {
			continue
		}
		k := key{a.Backend, id}
		if f, ok := first[k]; ok {
			v.status[a.ID] = StatusTwin
			v.twinOf[a.ID] = f.ID
			continue
		}
		first[k] = a
	}
}

// older orders accounts by creation time, then id.
func older(a, b *account.Account) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// cooldown returns the rest of an account for a group that is in force at now. A rest ends early when a quota
// read made after it was set shows the group usable again (the old version dropped such cooldowns on refresh);
// passive rests end with time, or when a new key is stored (KeySetAt after the rest began).
func (v *view) cooldown(id, group string) (Cooldown, bool) {
	c, ok := v.cooldowns[id][group]
	if !ok || !c.Until.After(v.now) {
		return Cooldown{}, false
	}
	var a *account.Account
	for i := range v.accounts {
		if v.accounts[i].ID == id {
			a = &v.accounts[i]
			break
		}
	}
	if group == creditsGroup {
		if a == nil {
			return Cooldown{}, false
		}
		return v.noCredit(a)
	}
	if c.Passive && a != nil && a.KeySetAt.After(c.CreatedAt) {
		return Cooldown{}, false // a key stored since: the limit was the old key's
	}
	if snap, found := v.snaps[id]; !c.Passive && found && snap.FetchedAt.After(c.CreatedAt) {
		if _, exhausted := snap.Exhausted(v.now, group, "", v.settings.MinRemaining); !exhausted {
			return Cooldown{}, false
		}
	}
	return c, true
}

// noCredit returns account a's rest for a credit balance that ran out (MarkNoCredit), while the user has not acted
// on the account since: a new key (KeySetAt), any edit of the account (UpdatedAt) or a key test that passed
// (TestedAt with TestOK) lifts it. Spend updates never touch UpdatedAt, so a turn's cost does not lift it.
func (v *view) noCredit(a *account.Account) (Cooldown, bool) {
	c, ok := v.cooldowns[a.ID][creditsGroup]
	if !ok || a.KeySetAt.After(c.CreatedAt) || a.UpdatedAt.After(c.CreatedAt) ||
		(a.TestState() == account.TestOK && a.TestedAt.After(c.CreatedAt)) {
		return Cooldown{}, false
	}
	return c, true
}

// Rest reasons: why an account rests until the user acts (AccountState.RestReason, the account's restReason in the
// API). No time ends such a rest, so it is never shown as a date. Not to be confused with Hold (a sign-out).
const (
	// RestCredits: a run reported that the account's credit balance or billing ran out (MarkNoCredit) and the user
	// has not acted on the account since (noCredit). A request with nothing else fails at once.
	RestCredits = "credits"
	// RestBudget: an API-key account reached its budget with budgetStop on (account.Account.BudgetStopped). It
	// rests: a request waits (ReasonBudget) until the budget is raised, the stop is switched off or the key changes.
	RestBudget = "budget_reached"
)

// restReason returns why account a rests until the user acts (RestCredits first), or "". It is the one test of
// both: the selection, the account state and the delegate's refusal (ProviderResting) all use it.
func (v *view) restReason(a *account.Account) string {
	if _, ok := v.noCredit(a); ok {
		return RestCredits
	}
	if a.BudgetStopped() {
		return RestBudget
	}
	return ""
}

// state is what an account can do for a request.
type state int

const (
	stateIneligible state = iota // not a candidate at all
	statePending                 // a candidate that is not ready yet (status new)
	stateResting
	stateBusy
	stateFree
)

// eval is the evaluation of one account for a request.
type eval struct {
	a         *account.Account
	state     state
	until     time.Time // resting: when it is back (zero when unknown)
	model     string
	remaining float64
	known     bool
	busy      int
	// noCredit: ineligible because its credit ran out (MarkNoCredit), not resting: no time brings it back.
	noCredit bool
	// budget: resting because it reached its budget (RestBudget); no time brings it back either.
	budget bool
}

// evaluate decides what account a can do for req; busy is its number of running turns.
func (v *view) evaluate(a *account.Account, req *Request, busy int) eval {
	e := eval{a: a, busy: busy}
	if a.Backend != req.Backend || slices.Contains(req.Exclude, a.ID) {
		return e
	}
	st := v.status[a.ID]
	if !st.Usable() && st != StatusNew {
		return e
	}
	if req.Provider == "" {
		if a.Kind == account.KindAPIKey {
			return e
		}
	} else if a.Kind != account.KindAPIKey || a.Provider != req.Provider {
		return e
	}
	why := v.restReason(a)
	if why == RestCredits {
		e.noCredit = true
		return e
	}
	snap, found := v.snaps[a.ID]
	model, ok := backend.FitModel(a.Backend, req.Model, snap.Models, req.Allowed)
	if !ok {
		return e
	}
	e.model = model
	if st == StatusNew {
		e.state = statePending
		return e
	}
	if why == RestBudget {
		// Rests until the user raises the budget, switches the stop off or replaces the key (not an error).
		e.state, e.budget = stateResting, true
		return e
	}
	if c, ok := v.cooldown(a.ID, req.Group); ok {
		e.state, e.until = stateResting, c.Until
		return e
	}
	passive := v.passive[a.ID]
	if found && !passive {
		if until, exhausted := snap.Exhausted(v.now, req.Group, req.Model, v.settings.MinRemaining); exhausted {
			if until.IsZero() {
				until = v.now.Add(quota.DefaultRest)
			}
			e.state, e.until = stateResting, until
			return e
		}
	}
	if found && !passive {
		e.remaining, e.known = snap.Remaining(v.now, req.Group, req.Model)
	} else {
		e.remaining = quota.UnknownRemaining
	}
	if busy >= maxConcurrent(v.settings) {
		e.state = stateBusy
		return e
	}
	e.state = stateFree
	return e
}

// maxConcurrent returns maxConcurrentPerAccount, at least 1.
func maxConcurrent(s config.Settings) int {
	return max(1, s.MaxConcurrentPerAccount)
}

// choose picks the account for req, or says why none is free. The caller holds p.mu.
func (p *Pool) choose(v *view, req *Request) (Choice, *Unavailable) {
	u := &Unavailable{Backend: req.Backend, Group: req.Group}
	var free []eval
	for i := range v.accounts {
		a := &v.accounts[i]
		if req.Account != "" && a.ID != req.Account {
			continue
		}
		running := len(p.leases[a.ID])
		e := v.evaluate(a, req, running)
		held := p.held[a.ID] > 0 && e.state != stateIneligible
		if held {
			e.state = stateBusy // held by a sign-out (Hold)
		}
		switch e.state {
		case stateIneligible:
			if e.noCredit {
				u.NoCredit = append(u.NoCredit, NoCredit{AccountID: a.ID, Label: a.Label, Provider: a.Provider})
			}
			continue
		case statePending:
			u.Resting++
		case stateResting:
			u.Resting++
			if e.budget {
				u.BudgetReached = append(u.BudgetReached, budgetHold(a))
			}
			if !e.until.IsZero() && (u.Until.IsZero() || e.until.Before(u.Until)) {
				u.Until = e.until
			}
		case stateBusy:
			u.Busy++
			u.Slots = append(u.Slots, BusySlot{AccountID: a.ID, Running: running, Limit: maxConcurrent(v.settings),
				Held: held})
		case stateFree:
			free = append(free, e)
		}
		u.Candidates++
	}
	if limit, running := v.settings.MaxConcurrentWorkers, p.runningLocked(); limit > 0 && len(free) > 0 &&
		running >= limit {
		// maxConcurrentWorkers: every free account counts as busy until a running turn ends (a release wakes the
		// waiters). Lowering the cap stops nothing.
		u.Busy += len(free)
		u.Capped, u.Running = limit, running
		free = nil
	}
	if len(free) == 0 {
		switch {
		case u.Candidates == 0:
			u.Reason = ReasonNoAccount
		case u.Busy > 0:
			u.Reason = ReasonBusy
		default:
			u.Reason = u.restingReason()
		}
		if u.Reason != ReasonNoAccount && u.Capped == 0 {
			u.KeyProviders = p.freeKeyProviders(v, req)
		}
		return Choice{}, u
	}
	best := pick(free, req.Prefer)
	return Choice{Account: *best.a, Model: best.model, Remaining: best.remaining, RemainingKnown: best.known}, nil
}

// freeKeyProviders lists the providers whose API-key accounts of the request's backend would be free for it with
// provider set, when the request names no provider and no account. The caller holds p.mu.
func (p *Pool) freeKeyProviders(v *view, req *Request) []KeyProvider {
	if req.Provider != "" || req.Account != "" {
		return nil
	}
	var out []KeyProvider
	for i := range v.accounts {
		a := &v.accounts[i]
		if a.Kind != account.KindAPIKey || a.Provider == "" || p.held[a.ID] > 0 ||
			slices.ContainsFunc(out, func(k KeyProvider) bool { return k.ID == a.Provider }) {
			continue
		}
		with := *req
		with.Provider = a.Provider
		if v.evaluate(a, &with, len(p.leases[a.ID])).state == stateFree {
			out = append(out, KeyProvider{ID: a.Provider})
		}
	}
	return out
}

// runningLocked counts the turns running on every account (leases, takeover consoles included). The caller holds
// p.mu.
func (p *Pool) runningLocked() int {
	n := 0
	for _, held := range p.leases {
		n += len(held)
	}
	return n
}

// pick returns the best free account: preferred accounts first (if any is free), then priority, running turns,
// remaining quota, preference order, age.
func pick(free []eval, prefer []string) eval {
	rank := func(id string) int {
		if i := slices.Index(prefer, id); i >= 0 {
			return i
		}
		return len(prefer)
	}
	list := free
	var preferred []eval
	for _, e := range free {
		if rank(e.a.ID) < len(prefer) {
			preferred = append(preferred, e)
		}
	}
	if len(preferred) > 0 {
		list = preferred
	}
	sort.SliceStable(list, func(i, j int) bool {
		x, y := list[i], list[j]
		switch {
		case x.a.Priority != y.a.Priority:
			return x.a.Priority > y.a.Priority
		case x.busy != y.busy:
			return x.busy < y.busy
		case x.remaining != y.remaining:
			return x.remaining > y.remaining
		case rank(x.a.ID) != rank(y.a.ID):
			return rank(x.a.ID) < rank(y.a.ID)
		}
		return older(x.a, y.a)
	})
	return list[0]
}

// Select returns the account Acquire would choose now, without taking a slot.
func (p *Pool) Select(ctx context.Context, req Request) (Choice, error) {
	v, err := p.load(ctx)
	if err != nil {
		return Choice{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c, u := p.choose(v, &req)
	if u != nil {
		return Choice{}, u
	}
	return c, nil
}

// AccountState is what the pool knows about one account (for the dashboard and the usage snapshots).
type AccountState struct {
	Account account.Account `json:"account"`
	Status  Status          `json:"status"`
	// TwinOf is the account that takes the work when Status is twin.
	TwinOf string `json:"twinOf,omitempty"`
	// Busy is the number of running turns.
	Busy int `json:"busy"`
	// Quota is the last quota snapshot (HasQuota is false when there is none).
	Quota    quota.Snapshot `json:"quota"`
	HasQuota bool           `json:"hasQuota"`
	// Cooldowns are the rests in force that time ends (RestReason's rests are not among them).
	Cooldowns []Cooldown `json:"cooldowns"`
	// RestReason is why the account rests until the user acts (RestCredits, RestBudget), "" when it does not.
	RestReason string `json:"restReason,omitempty"`
}

// States returns the state of every account, in the order of Accounts.List.
func (p *Pool) States(ctx context.Context) ([]AccountState, error) {
	v, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]AccountState, 0, len(v.accounts))
	for i := range v.accounts {
		a := &v.accounts[i]
		snap, found := v.snaps[a.ID]
		st := AccountState{Account: *a, Status: v.status[a.ID], TwinOf: v.twinOf[a.ID], Busy: len(p.leases[a.ID]),
			Quota: snap, HasQuota: found, Cooldowns: []Cooldown{}, RestReason: v.restReason(a)}
		groups := make([]string, 0, len(v.cooldowns[a.ID]))
		for g := range v.cooldowns[a.ID] {
			if g != creditsGroup { // no end: RestReason says it
				groups = append(groups, g)
			}
		}
		sort.Strings(groups)
		for _, g := range groups {
			if c, ok := v.cooldown(a.ID, g); ok {
				st.Cooldowns = append(st.Cooldowns, c)
			}
		}
		out = append(out, st)
	}
	return out, nil
}
