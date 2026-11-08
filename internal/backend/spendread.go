package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// ReadServiceSpend reads what the provider's spend service (account.Provider.SpendService) knows with key:
// OpenRouter's usage of the key, DeepSeek's or Moonshot's account balance. It has the key test's safety: one GET
// on the service's official host with the key as a bearer token, a 10 s timeout, at most 1 MiB of answer, and
// redirects only on the same scheme and host, so the key never reaches another host. A failure is an
// *account.ServiceError whose message names the host and the failure, never the key or the body. A 402 is
// account.ServiceNoCredit; DeepSeek's is_available false and a Moonshot balance at or below zero come back as a
// reading with NoCredit. Amounts are what the service sent, in its currency.
func ReadServiceSpend(ctx context.Context, p account.Provider, key string, o ProbeOptions) (account.ServiceReading,
	error) {
	service, endpoint := p.SpendService()
	if service == "" {
		return account.ServiceReading{}, &account.ServiceError{Reason: account.ServiceFailed,
			Message: fmt.Sprintf("provider %s has no spend service to read", p.ID)}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return account.ServiceReading{}, &account.ServiceError{Reason: account.ServiceFailed, Message: "bad endpoint"}
	}
	official := u.Host
	if base, ok := o.ServiceBase[official]; ok {
		b, err := url.Parse(base)
		if err != nil {
			return account.ServiceReading{}, &account.ServiceError{Reason: account.ServiceFailed, Message: "bad endpoint"}
		}
		u.Scheme, u.Host = b.Scheme, b.Host
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = ProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return account.ServiceReading{}, &account.ServiceError{Reason: account.ServiceFailed, Message: "bad endpoint"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := keyClient(o.Transport).Do(req)
	if err != nil {
		return account.ServiceReading{}, &account.ServiceError{Reason: account.ServiceUnreachable,
			Message: unreachableMessage(req.URL, err, timeout)}
	}
	defer func() { _ = resp.Body.Close() }()
	fail := func(reason, format string, args ...any) (account.ServiceReading, error) {
		return account.ServiceReading{}, &account.ServiceError{Reason: reason,
			Message: official + ": " + fmt.Sprintf(format, args...)}
	}
	switch code := resp.StatusCode; {
	case code >= 300 && code < 400:
		if loc, err := resp.Location(); err == nil {
			return fail(account.ServiceRedirected, "redirected to %s; the key was not sent there", loc.Host)
		}
		return fail(account.ServiceRedirected, "redirected to another address; the key was not sent there")
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fail(account.ServiceRefused, "the service refused this key (HTTP %d)", code)
	case code == http.StatusPaymentRequired:
		return fail(account.ServiceNoCredit, "no credit left (HTTP 402)")
	case code < 200 || code >= 300:
		return fail(account.ServiceFailed, "the service answered HTTP %d", code)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit+1))
	switch {
	case err != nil:
		return fail(account.ServiceFailed, "the answer could not be read")
	case len(body) > probeBodyLimit:
		return fail(account.ServiceFailed, "the answer is larger than %d bytes", probeBodyLimit)
	}
	var reading account.ServiceReading
	switch service {
	case account.ServiceOpenRouter:
		reading, err = parseOpenRouterKey(body)
	case account.ServiceDeepSeek:
		reading, err = parseDeepSeekBalance(body)
	default:
		currency := "USD"
		if strings.HasSuffix(official, ".cn") {
			currency = "CNY"
		}
		reading, err = parseMoonshotBalance(body, currency)
	}
	if err != nil {
		return fail(account.ServiceFailed, "the answer is not the expected %s JSON (%v)", service, err)
	}
	return reading, nil
}

// errShape is a JSON answer without the fields a reader needs.
var errShape = errors.New("missing fields")

// parseOpenRouterKey reads GET /api/v1/key: {"data": {"usage", "usage_daily", "usage_weekly", "usage_monthly",
// "limit", "limit_remaining", "limit_reset"}} in US dollars. A limit gives a per-key balance (limit_remaining).
func parseOpenRouterKey(body []byte) (account.ServiceReading, error) {
	var v struct {
		Data *struct {
			Usage          *flexNum `json:"usage"`
			UsageDaily     *flexNum `json:"usage_daily"`
			UsageWeekly    *flexNum `json:"usage_weekly"`
			UsageMonthly   *flexNum `json:"usage_monthly"`
			Limit          *flexNum `json:"limit"`
			LimitRemaining *flexNum `json:"limit_remaining"`
			LimitReset     *string  `json:"limit_reset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return account.ServiceReading{}, errors.New("not JSON")
	}
	d := v.Data
	if d == nil || d.Usage == nil {
		return account.ServiceReading{}, errShape
	}
	u := &account.ServiceUsage{TotalUSD: d.Usage.value(), DailyUSD: d.UsageDaily.value(),
		WeeklyUSD: d.UsageWeekly.value(), MonthlyUSD: d.UsageMonthly.value()}
	if d.LimitReset != nil {
		u.LimitReset = strings.TrimSpace(*d.LimitReset)
		if len(u.LimitReset) > 32 {
			u.LimitReset = ""
		}
	}
	out := account.ServiceReading{Usage: u}
	if d.Limit != nil {
		limit := d.Limit.value()
		remaining := limit - u.TotalUSD
		if d.LimitRemaining != nil {
			remaining = d.LimitRemaining.value()
		}
		u.LimitUSD, u.LimitRemainingUSD = &limit, &remaining
		out.Balance = &account.ServiceBalance{Amount: remaining, Currency: "USD", Available: remaining > 0}
	}
	return out, nil
}

// parseDeepSeekBalance reads GET /user/balance: {"is_available", "balance_infos": [{"currency", "total_balance",
// "granted_balance", "topped_up_balance"}]}, amounts as strings or numbers. The balance shown is the first
// currency with money left (else the first); the others go to Other.
func parseDeepSeekBalance(body []byte) (account.ServiceReading, error) {
	var v struct {
		IsAvailable  *bool `json:"is_available"`
		BalanceInfos []struct {
			Currency string   `json:"currency"`
			Total    *flexNum `json:"total_balance"`
			Granted  *flexNum `json:"granted_balance"`
			ToppedUp *flexNum `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return account.ServiceReading{}, errors.New("not JSON")
	}
	if v.IsAvailable == nil {
		return account.ServiceReading{}, errShape
	}
	out := account.ServiceReading{NoCredit: !*v.IsAvailable}
	primary := -1
	for i, b := range v.BalanceInfos {
		if b.Total == nil || !validCurrency(b.Currency) {
			return account.ServiceReading{}, errShape
		}
		if primary < 0 && b.Total.value() > 0 {
			primary = i
		}
	}
	if len(v.BalanceInfos) == 0 {
		return out, nil
	}
	primary = max(primary, 0)
	b := v.BalanceInfos[primary]
	bal := &account.ServiceBalance{Amount: b.Total.value(), Currency: strings.ToUpper(b.Currency), AccountWide: true,
		Available: *v.IsAvailable, Granted: b.Granted.ptr(), Paid: b.ToppedUp.ptr()}
	for i, o := range v.BalanceInfos {
		if i != primary {
			bal.Other = append(bal.Other, account.Money{Amount: o.Total.value(), Currency: strings.ToUpper(o.Currency)})
		}
	}
	out.Balance = bal
	return out, nil
}

// parseMoonshotBalance reads GET /v1/users/me/balance: {"data": {"available_balance", "voucher_balance",
// "cash_balance"}} in currency (USD on api.moonshot.ai, CNY on api.moonshot.cn). A balance at or below zero cannot
// pay for a request.
func parseMoonshotBalance(body []byte, currency string) (account.ServiceReading, error) {
	var v struct {
		Data *struct {
			Available *flexNum `json:"available_balance"`
			Voucher   *flexNum `json:"voucher_balance"`
			Cash      *flexNum `json:"cash_balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return account.ServiceReading{}, errors.New("not JSON")
	}
	if v.Data == nil || v.Data.Available == nil {
		return account.ServiceReading{}, errShape
	}
	amount := v.Data.Available.value()
	return account.ServiceReading{NoCredit: amount <= 0, Balance: &account.ServiceBalance{Amount: amount,
		Currency: currency, AccountWide: true, Available: amount > 0, Granted: v.Data.Voucher.ptr(),
		Paid: v.Data.Cash.ptr()}}, nil
}

// validCurrency accepts a currency code as services send it ("CNY", "USD").
func validCurrency(c string) bool {
	if len(c) < 2 || len(c) > 8 {
		return false
	}
	for _, r := range c {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

// flexNum is a JSON number that may come as a string ("110.00"); null counts as absent. Only finite numbers are
// accepted.
type flexNum float64

// UnmarshalJSON accepts 1.5 and "1.5".
func (n *flexNum) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		b = []byte(strings.TrimSpace(s))
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return errors.New("not a number")
	}
	*n = flexNum(v)
	return nil
}

// value is the number (0 when absent).
func (n *flexNum) value() float64 {
	if n == nil {
		return 0
	}
	return float64(*n)
}

// ptr is the number as a pointer (nil when absent).
func (n *flexNum) ptr() *float64 {
	if n == nil {
		return nil
	}
	v := float64(*n)
	return &v
}

// ServiceReader reads spend services with fixed options (ProbeOptions; the zero value in production).
type ServiceReader struct {
	Options ProbeOptions
}

// Read reads the provider's spend service with key; see ReadServiceSpend.
func (r ServiceReader) Read(ctx context.Context, p account.Provider, key string) (account.ServiceReading, error) {
	return ReadServiceSpend(ctx, p, key, r.Options)
}
