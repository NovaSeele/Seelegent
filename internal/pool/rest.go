package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/quota"
)

// timeFormat is how times are stored: UTC, sortable as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// refreshTimeout bounds the quota read MarkExhausted does before it rests an account.
const refreshTimeout = 45 * time.Second

// Cooldown is a rest of one account for one quota group.
type Cooldown struct {
	AccountID string    `json:"accountId"`
	Group     string    `json:"group"`
	Until     time.Time `json:"until"`
	Reason    string    `json:"reason,omitempty"`
	// Passive marks the rest of an account whose quota cannot be read; only time ends it.
	Passive   bool      `json:"passive"`
	CreatedAt time.Time `json:"createdAt"`
}

// Exhaustion is what a run reported when it ran out of quota.
type Exhaustion struct {
	// Group and Model are the quota group and model of the turn.
	Group string
	Model string
	// ResetAt is the reset time the CLI or provider gave, when it gave one.
	ResetAt time.Time
	// Reason is shown to the user (the CLI's message).
	Reason string
}

// MarkExhausted rests an account for a quota group after a run reported quota_exhausted, and returns the rest.
// An account with active quota is read first (when Options.Refresh is set) and rests until its exhausted windows
// reset, until the hint, or quota.DefaultRest; an API-key account rests until the hint or for
// passiveCooldownMinutes. An existing longer rest is kept. Unknown accounts give an error matching
// account.ErrNotFound (never a panic).
func (p *Pool) MarkExhausted(ctx context.Context, accountID string, ex Exhaustion) (Cooldown, error) {
	accounts, err := p.o.Accounts.List(ctx)
	if err != nil {
		return Cooldown{}, fmt.Errorf("rest account %s: %w", accountID, err)
	}
	var a *account.Account
	for i := range accounts {
		if accounts[i].ID == accountID {
			a = &accounts[i]
		}
	}
	if a == nil {
		return Cooldown{}, fmt.Errorf("rest account %s: %w", accountID, account.ErrNotFound)
	}
	s := p.o.Settings()
	c := Cooldown{AccountID: a.ID, Group: ex.Group, Reason: ex.Reason}
	if p.passive(a) {
		c.Passive = true
		c.CreatedAt = p.o.Clock.Now().UTC()
		c.Until = quota.PassiveRest(c.CreatedAt, ex.ResetAt, s.PassiveCooldownMinutes)
	} else {
		snap := p.freshSnapshot(ctx, a.ID)
		c.CreatedAt = p.o.Clock.Now().UTC()
		c.Until = quota.ActiveRest(snap, c.CreatedAt, ex.Group, ex.Model, s.MinRemaining, ex.ResetAt)
	}
	err = p.o.DB.Write(ctx, func(tx *sql.Tx) error {
		var until string
		err := tx.QueryRowContext(ctx, "SELECT until FROM cooldowns WHERE account_id = ? AND quota_group = ?",
			c.AccountID, c.Group).Scan(&until)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			if prev, perr := time.Parse(time.RFC3339Nano, until); perr == nil && prev.After(c.Until) {
				c.Until = prev.UTC()
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM cooldowns WHERE until <= ?", formatTime(c.CreatedAt)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cooldowns (account_id, quota_group, until, reason, passive, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, quota_group) DO UPDATE SET until = excluded.until, reason = excluded.reason,
				passive = excluded.passive, created_at = excluded.created_at`,
			c.AccountID, c.Group, formatTime(c.Until), c.Reason, boolInt(c.Passive), formatTime(c.CreatedAt))
		return err
	})
	if err != nil {
		return Cooldown{}, fmt.Errorf("rest account %s: %w", accountID, err)
	}
	p.o.Logger.Info("pool: account resting", "account", c.AccountID, "group", c.Group, "until", c.Until,
		"passive", c.Passive)
	return c, nil
}

// MarkAuthFailed records that a run on the account was refused for its sign-in or API key (auth_required): the
// account is not selected again until it signs in (a successful quota read) or gets a new key. Unknown accounts
// give an error matching account.ErrNotFound.
func (p *Pool) MarkAuthFailed(ctx context.Context, accountID, message string) error {
	accounts, err := p.o.Accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("mark %s signed out: %w", accountID, err)
	}
	for i := range accounts {
		if a := &accounts[i]; a.ID == accountID {
			if err := p.o.Quotas.MarkNeedsLogin(ctx, a.ID, a.Backend, message, p.o.Clock.Now().UTC()); err != nil {
				return fmt.Errorf("mark %s signed out: %w", accountID, err)
			}
			p.o.Logger.Info("pool: account needs sign-in", "account", a.ID)
			if p.o.Confirm != nil && !p.passive(a) {
				// One refused run may be a hiccup: read the quota to confirm; a good read clears the mark.
				p.o.Confirm(a.ID)
			}
			return nil
		}
	}
	return fmt.Errorf("mark %s signed out: %w", accountID, account.ErrNotFound)
}

// freshSnapshot reads the account's quota now when possible, else returns the cached snapshot (zero when none).
func (p *Pool) freshSnapshot(ctx context.Context, id string) quota.Snapshot {
	if p.o.Refresh != nil {
		rctx, cancel := context.WithTimeout(ctx, refreshTimeout)
		snap, err := p.o.Refresh(rctx, id)
		cancel()
		if err == nil {
			return snap
		}
		p.o.Logger.Debug("pool: quota read before rest failed", "account", id, "err", err)
	}
	snaps, err := p.o.Quotas.All(ctx)
	if err != nil {
		return quota.Snapshot{}
	}
	return snaps[id]
}

// ClearCooldown ends the rest of an account for a group (all groups when group is "") and wakes waiting tasks.
func (p *Pool) ClearCooldown(ctx context.Context, accountID, group string) error {
	err := p.o.DB.Write(ctx, func(tx *sql.Tx) error {
		if group == "" {
			_, err := tx.ExecContext(ctx, "DELETE FROM cooldowns WHERE account_id = ?", accountID)
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM cooldowns WHERE account_id = ? AND quota_group = ?", accountID, group)
		return err
	})
	if err != nil {
		return fmt.Errorf("clear rest of %s: %w", accountID, err)
	}
	p.Notify()
	return nil
}

// listCooldowns returns every stored cooldown. A row whose time cannot be read is skipped (it cannot rest
// anything) rather than failing every selection.
func (p *Pool) listCooldowns(ctx context.Context) ([]Cooldown, error) {
	var out []Cooldown
	err := p.o.DB.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT account_id, quota_group, until, reason, passive, created_at FROM cooldowns")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var (
				c              Cooldown
				until, created string
				passive        int
			)
			if err := rows.Scan(&c.AccountID, &c.Group, &until, &c.Reason, &passive, &created); err != nil {
				return err
			}
			u, err1 := time.Parse(time.RFC3339Nano, until)
			cr, err2 := time.Parse(time.RFC3339Nano, created)
			if err1 != nil || err2 != nil {
				p.o.Logger.Warn("pool: unreadable cooldown skipped", "account", c.AccountID, "group", c.Group)
				continue
			}
			c.Until, c.CreatedAt, c.Passive = u.UTC(), cr.UTC(), passive != 0
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("read cooldowns: %w", err)
	}
	return out, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
