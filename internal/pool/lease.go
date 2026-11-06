package pool

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Lease is one running turn's slot on an account. Release it exactly when the turn's process has ended; extra
// Release calls do nothing, so `defer lease.Release()` is always safe.
type Lease struct {
	p          *Pool
	accountID  string
	taskID     string
	acquiredAt time.Time
}

// AccountID returns the leased account.
func (l *Lease) AccountID() string { return l.accountID }

// TaskID returns the task that holds the lease.
func (l *Lease) TaskID() string { return l.taskID }

// AcquiredAt returns when the lease was taken.
func (l *Lease) AcquiredAt() time.Time { return l.acquiredAt }

// Release frees the slot and wakes waiting tasks. It is idempotent and safe on a nil lease.
func (l *Lease) Release() {
	if l == nil || l.p == nil {
		return
	}
	p := l.p
	p.mu.Lock()
	defer p.mu.Unlock()
	held := p.leases[l.accountID]
	if held[l.taskID] != l {
		return
	}
	delete(held, l.taskID)
	if len(held) == 0 {
		delete(p.leases, l.accountID)
	}
	p.o.Logger.Debug("pool: slot released", "account", l.accountID, "task", l.taskID)
	p.notifyLocked()
}

// Acquire selects an account for req and takes one of its slots, in one step. It returns *Unavailable when no
// account is free and ErrAlreadyHeld when the task already holds the chosen account.
func (p *Pool) Acquire(ctx context.Context, req Request) (*Lease, Choice, error) {
	v, err := p.load(ctx)
	if err != nil {
		return nil, Choice{}, fmt.Errorf("acquire an account: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c, u := p.choose(v, &req)
	if u != nil {
		return nil, Choice{}, u
	}
	taskID := req.TaskID
	if taskID == "" {
		p.anon++
		taskID = "anonymous-" + strconv.Itoa(p.anon)
	}
	if _, held := p.leases[c.Account.ID][taskID]; held {
		return nil, Choice{}, fmt.Errorf("task %s on account %s: %w", taskID, c.Account.ID, ErrAlreadyHeld)
	}
	l := p.addLocked(c.Account.ID, taskID, v.now)
	return l, c, nil
}

// Reattach counts a worker that is still running after a daemon restart: it returns the task's lease on the
// account, creating it when the task holds none. Calling it again for the same task returns the same lease, so a
// task is never counted twice. It never fails: the worker runs whatever the account's state or slot count (even
// for an account removed in the meantime), so it always takes its slot.
func (p *Pool) Reattach(accountID, taskID string) *Lease {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.leases[accountID][taskID]; ok {
		return l
	}
	return p.addLocked(accountID, taskID, p.o.Clock.Now().UTC())
}

// addLocked records a new lease. The caller holds p.mu.
func (p *Pool) addLocked(accountID, taskID string, now time.Time) *Lease {
	l := &Lease{p: p, accountID: accountID, taskID: taskID, acquiredAt: now}
	if p.leases[accountID] == nil {
		p.leases[accountID] = map[string]*Lease{}
	}
	p.leases[accountID][taskID] = l
	p.o.Logger.Debug("pool: slot taken", "account", accountID, "task", taskID, "busy", len(p.leases[accountID]))
	return l
}
