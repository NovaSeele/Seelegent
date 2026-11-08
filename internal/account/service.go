package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Spend services: the services whose own endpoint tells, with the stored key, what the key spent or what the
// account has left (Provider.SpendService). Only their official hosts are called, and only with https.
const (
	// ServiceOpenRouter: GET https://openrouter.ai/api/v1/key, the key's own usage (total, daily, weekly, monthly
	// in USD), limit and limit_remaining. Per key and authoritative.
	ServiceOpenRouter = "openrouter"
	// ServiceDeepSeek: GET https://api.deepseek.com/user/balance, the account's balance (CNY or USD), shared by
	// every key of the account.
	ServiceDeepSeek = "deepseek"
	// ServiceMoonshot: GET https://api.moonshot.ai/v1/users/me/balance (USD; api.moonshot.cn: CNY), the account's
	// balance, shared by every key of the account.
	ServiceMoonshot = "moonshot"
)

// What a spend service reads (Provider.ServiceReading).
const (
	// ReadingKeyUsage: what this key spent (OpenRouter). It is the account's spend (spend source SpendService).
	ReadingKeyUsage = "key_usage"
	// ReadingAccountBalance: what the service account has left, shared by all its keys (DeepSeek, Moonshot). It is
	// shown next to the spend; it cannot tell what this key spent.
	ReadingAccountBalance = "account_balance"
)

// Reasons of a failed service read (ServiceError.Reason).
const (
	// ServiceRefused: 401 or 403, the service refused the key.
	ServiceRefused = "refused"
	// ServiceNoCredit: 402, the key or its account has no credit left.
	ServiceNoCredit = "no_credit"
	// ServiceUnreachable: DNS, connection, TLS or timeout.
	ServiceUnreachable = "unreachable"
	// ServiceRedirected: the service redirected to another host; the key was not sent there.
	ServiceRedirected = "redirected"
	// ServiceFailed: any other answer (5xx, 429, a body that is not the expected JSON, a body over the limit).
	ServiceFailed = "error"
)

// ServiceReasons lists the reasons of a failed service read.
func ServiceReasons() []string {
	return []string{ServiceRefused, ServiceNoCredit, ServiceUnreachable, ServiceRedirected, ServiceFailed}
}

// SpendService returns the spend service of the provider and the endpoint to read: decided by the host of its
// base URL, which must be the service's official host on https (any preset: a custom provider on openrouter.ai is
// OpenRouter). It returns "" for every other provider, so a key is never sent to an address the user did not
// give as the provider's own.
func (p Provider) SpendService() (service, endpoint string) {
	if p.Official {
		return "", ""
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return "", ""
	}
	switch host := strings.ToLower(u.Hostname()); host {
	case "openrouter.ai":
		return ServiceOpenRouter, "https://openrouter.ai/api/v1/key"
	case "api.deepseek.com":
		return ServiceDeepSeek, "https://api.deepseek.com/user/balance"
	case "api.moonshot.ai", "api.moonshot.cn":
		return ServiceMoonshot, "https://" + host + "/v1/users/me/balance"
	}
	return "", ""
}

// ServiceReading says what the provider's spend service reads: ReadingKeyUsage, ReadingAccountBalance, or "".
func (p Provider) ServiceReading() string {
	switch s, _ := p.SpendService(); s {
	case ServiceOpenRouter:
		return ReadingKeyUsage
	case ServiceDeepSeek, ServiceMoonshot:
		return ReadingAccountBalance
	}
	return ""
}

// Money is an amount in a currency the service named ("USD", "CNY"); it is never converted.
type Money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// ServiceBalance is what a service account has left.
type ServiceBalance struct {
	// Amount and Currency are the balance that can be spent (DeepSeek total_balance, Moonshot available_balance,
	// OpenRouter limit_remaining).
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	// AccountWide: the balance is shared by every key of the service account (DeepSeek, Moonshot), not this key's.
	AccountWide bool `json:"accountWide"`
	// Available is false when the service says the balance cannot pay for a request.
	Available bool `json:"available"`
	// Granted is the part given by the service (DeepSeek granted_balance, Moonshot voucher_balance); Paid the part
	// paid for (DeepSeek topped_up_balance, Moonshot cash_balance). Nil when not reported.
	Granted *float64 `json:"granted,omitempty"`
	Paid    *float64 `json:"paid,omitempty"`
	// Other are the balances in other currencies (DeepSeek reports one per currency).
	Other []Money `json:"other,omitempty"`
}

// ServiceUsage is what the key spent, as the service counts it (OpenRouter), in US dollars. It counts the key's
// whole life, also use outside Seelegent.
type ServiceUsage struct {
	TotalUSD   float64 `json:"totalUsd"`
	DailyUSD   float64 `json:"dailyUsd"`
	WeeklyUSD  float64 `json:"weeklyUsd"`
	MonthlyUSD float64 `json:"monthlyUsd"`
	// LimitUSD is the key's spending limit set at the service (nil: none); LimitRemainingUSD what is left of it.
	// LimitReset is the period after which the limit starts again ("daily", "weekly", "monthly"; "" never).
	LimitUSD          *float64 `json:"limitUsd,omitempty"`
	LimitRemainingUSD *float64 `json:"limitRemainingUsd,omitempty"`
	LimitReset        string   `json:"limitReset,omitempty"`
}

// ServiceReading is one good read of a spend service.
type ServiceReading struct {
	Balance *ServiceBalance
	Usage   *ServiceUsage
	// NoCredit: the service says the key cannot pay for a request (DeepSeek is_available false, Moonshot a balance
	// at or below zero).
	NoCredit bool
}

// ServiceError is a failed read of a spend service. Message names the host and the failure; it never holds the
// key or the response body.
type ServiceError struct {
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
	At      time.Time `json:"at,omitzero"`
}

func (e *ServiceError) Error() string { return e.Message }

// ServiceSpend is what the account knows from its spend service: the last good read (kept when a later read fails)
// and the last failure after it.
type ServiceSpend struct {
	// ReadAt is the time of the last good read (zero when none).
	ReadAt  time.Time       `json:"readAt,omitzero"`
	Balance *ServiceBalance `json:"balance,omitempty"`
	Usage   *ServiceUsage   `json:"usage,omitempty"`
	// Error is the last failed read, when it came after ReadAt: the values above are stale then.
	Error *ServiceError `json:"error,omitempty"`
	// NoCreditAt is when a read last held the account for lack of credit (pool.MarkNoCredit); zero once a read
	// shows credit again.
	NoCreditAt time.Time `json:"noCreditAt,omitzero"`
}

// LastAttempt is the time of the last read, good or failed (zero when never read).
func (s *ServiceSpend) LastAttempt() time.Time {
	if s == nil {
		return time.Time{}
	}
	if s.Error != nil && s.Error.At.After(s.ReadAt) {
		return s.Error.At
	}
	return s.ReadAt
}

// Stale reports values that are not current at now: a later read failed, or the last good read is older than
// maxAge.
func (s *ServiceSpend) Stale(now time.Time, maxAge time.Duration) bool {
	if s == nil || s.ReadAt.IsZero() {
		return false
	}
	return (s.Error != nil && s.Error.At.After(s.ReadAt)) || now.Sub(s.ReadAt) > maxAge
}

// RecordService stores a read of the account's spend service that started at started: the reading, or the failure
// readErr (the last good values are kept). marked says the read just held the account for lack of credit. A read
// that started before the key was set or its provider's endpoint changed is dropped (stored false). The budget is
// judged again (an OpenRouter usage is the account's spend); crossings are the rises to warn or reached.
func (r *Repo) RecordService(ctx context.Context, id string, started time.Time, reading *ServiceReading,
	readErr *ServiceError, marked bool) (svc ServiceSpend, crossings []BudgetCrossing, stored bool, err error) {
	now := r.stamp()
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		var raw, kind, resetAt, hint string
		err := tx.QueryRowContext(ctx, `SELECT kind, key_hint, test_reset_at, service_spend FROM accounts WHERE id = ?`,
			id).Scan(&kind, &hint, &resetAt, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if Kind(kind) != KindAPIKey || hint == "" || resetAt > formatTime(started) {
			return nil
		}
		prev, err := parseServiceSpend(raw)
		if err != nil {
			return fmt.Errorf("account %s: %w", id, err)
		}
		if prev != nil {
			svc = *prev
		}
		switch {
		case reading != nil:
			svc.ReadAt, svc.Balance, svc.Usage, svc.Error = now, reading.Balance, reading.Usage, nil
			if !reading.NoCredit {
				svc.NoCreditAt = time.Time{}
			}
		case readErr != nil:
			e := *readErr
			e.At = now
			svc.Error = &e
		}
		if marked {
			svc.NoCreditAt = now
		}
		data, err := json.Marshal(svc)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET service_spend = ? WHERE id = ?", string(data),
			id); err != nil {
			return err
		}
		stored = true
		crossings, err = judgeBudgetTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return ServiceSpend{}, nil, false, wrap("record service spend", err)
	}
	return svc, crossings, stored, nil
}

// RecordService stores a read of the account's spend service; see Repo.RecordService.
func (m *Manager) RecordService(ctx context.Context, id string, started time.Time, reading *ServiceReading,
	readErr *ServiceError, marked bool) (ServiceSpend, []BudgetCrossing, bool, error) {
	return m.repo.RecordService(ctx, id, started, reading, readErr, marked)
}

// parseServiceSpend reads the stored service spend (nil when empty). A value that is not valid JSON is an error, never
// silently dropped.
func parseServiceSpend(raw string) (*ServiceSpend, error) {
	if raw == "" {
		return nil, nil
	}
	var s ServiceSpend
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("service spend: %w", err)
	}
	return &s, nil
}
