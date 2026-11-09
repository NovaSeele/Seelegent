package backend

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
)

const spendKey = "sk-or-v1-secret-key-0123456789"

// spendProvider returns a provider of the preset on its official address.
func spendProvider(t *testing.T, preset string) account.Provider {
	t.Helper()
	p, err := account.FromPreset(preset, preset)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

// spendServer answers the service's endpoint with status and body after checking the request: GET on path, the
// key as a bearer token.
func spendServer(t *testing.T, path string, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("request %s %s, want GET %s", r.Method, r.URL.Path, path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+spendKey {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readSpend(t *testing.T, p account.Provider, srv *httptest.Server, timeout time.Duration) (account.ServiceReading,
	*account.ServiceError) {
	t.Helper()
	_, endpoint := p.SpendService()
	host := strings.TrimPrefix(endpoint, "https://")
	host, _, _ = strings.Cut(host, "/")
	got, err := ReadServiceSpend(t.Context(), p, spendKey, ProbeOptions{Timeout: timeout,
		ServiceBase: map[string]string{host: srv.URL}})
	if err == nil {
		return got, nil
	}
	var se *account.ServiceError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a ServiceError", err)
	}
	if strings.Contains(se.Message, spendKey) || strings.Contains(se.Message, "secret") {
		t.Fatalf("the key leaked into the error: %q", se.Message)
	}
	return got, se
}

func TestReadServiceSpendOpenRouter(t *testing.T) {
	p := spendProvider(t, "openrouter")
	srv := spendServer(t, "/api/v1/key", http.StatusOK, `{"data":{"label":"sk-or-v1-abc","usage":12.4,
		"usage_daily":0.5,"usage_weekly":3,"usage_monthly":10.25,"limit":200,"limit_remaining":187.6,
		"limit_reset":"monthly","is_free_tier":false}}`)
	got, se := readSpend(t, p, srv, 0)
	if se != nil {
		t.Fatal(se)
	}
	u := got.Usage
	if u == nil || u.TotalUSD != 12.4 || u.DailyUSD != 0.5 || u.WeeklyUSD != 3 || u.MonthlyUSD != 10.25 ||
		u.LimitUSD == nil || *u.LimitUSD != 200 || *u.LimitRemainingUSD != 187.6 || u.LimitReset != "monthly" {
		t.Fatalf("usage %+v", u)
	}
	if b := got.Balance; b == nil || b.Amount != 187.6 || b.Currency != "USD" || b.AccountWide || !b.Available {
		t.Fatalf("balance %+v", b)
	}
	if got.NoCredit {
		t.Fatal("no credit on a key with limit left")
	}

	// No limit: usage only, no balance.
	srv = spendServer(t, "/api/v1/key", http.StatusOK, `{"data":{"usage":"0.75","limit":null,"limit_remaining":null}}`)
	got, se = readSpend(t, p, srv, 0)
	if se != nil || got.Usage == nil || got.Usage.TotalUSD != 0.75 || got.Usage.LimitUSD != nil || got.Balance != nil {
		t.Fatalf("no limit: %+v %v", got, se)
	}
}

func TestReadServiceSpendDeepSeek(t *testing.T) {
	p := spendProvider(t, "deepseek")
	srv := spendServer(t, "/user/balance", http.StatusOK, `{"is_available":true,"balance_infos":[
		{"currency":"USD","total_balance":"0.00","granted_balance":"0.00","topped_up_balance":"0.00"},
		{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`)
	got, se := readSpend(t, p, srv, 0)
	if se != nil {
		t.Fatal(se)
	}
	b := got.Balance
	if b == nil || b.Amount != 110 || b.Currency != "CNY" || !b.AccountWide || !b.Available || *b.Granted != 10 ||
		*b.Paid != 100 || len(b.Other) != 1 || b.Other[0] != (account.Money{Currency: "USD"}) || got.NoCredit {
		t.Fatalf("deepseek: %+v %+v", got, b)
	}

	// is_available false: a reading that says there is no credit.
	srv = spendServer(t, "/user/balance", http.StatusOK, `{"is_available":false,"balance_infos":[
		{"currency":"CNY","total_balance":"0.00","granted_balance":"0.00","topped_up_balance":"0.00"}]}`)
	got, se = readSpend(t, p, srv, 0)
	if se != nil || !got.NoCredit || got.Balance == nil || got.Balance.Available || got.Balance.Currency != "CNY" {
		t.Fatalf("not available: %+v %v", got, se)
	}
}

func TestReadServiceSpendMoonshot(t *testing.T) {
	p := spendProvider(t, "moonshot")
	srv := spendServer(t, "/v1/users/me/balance", http.StatusOK, `{"code":0,"data":{"available_balance":49.58894,
		"voucher_balance":46.58893,"cash_balance":3.00001},"scode":"0x0","status":true}`)
	got, se := readSpend(t, p, srv, 0)
	if se != nil {
		t.Fatal(se)
	}
	if b := got.Balance; b == nil || b.Amount != 49.58894 || b.Currency != "USD" || !b.AccountWide ||
		*b.Granted != 46.58893 || *b.Paid != 3.00001 || got.NoCredit {
		t.Fatalf("moonshot .ai: %+v", b)
	}
	// api.moonshot.cn reports CNY; a balance at zero cannot pay.
	cn := p
	cn.BaseURL = "https://api.moonshot.cn/anthropic"
	srv = spendServer(t, "/v1/users/me/balance", http.StatusOK, `{"data":{"available_balance":0,"voucher_balance":0,
		"cash_balance":0}}`)
	got, se = readSpend(t, cn, srv, 0)
	if se != nil || got.Balance == nil || got.Balance.Currency != "CNY" || !got.NoCredit || got.Balance.Available {
		t.Fatalf("moonshot .cn: %+v %v", got, se)
	}
}

func TestReadServiceSpendFailures(t *testing.T) {
	p := spendProvider(t, "openrouter")
	for _, c := range []struct {
		name, body string
		status     int
		reason     string
	}{
		{"refused", `{"error":{"message":"bad key sk-or-v1-secret-key-0123456789"}}`, 401, account.ServiceRefused},
		{"forbidden", `{}`, 403, account.ServiceRefused},
		{"no credit", `{"error":{"message":"Insufficient credits"}}`, 402, account.ServiceNoCredit},
		{"server", `oops`, 500, account.ServiceFailed},
		{"rate limited", `{}`, 429, account.ServiceFailed},
		{"malformed", `{"data":`, 200, account.ServiceFailed},
		{"not the shape", `{"data":{"label":"x"}}`, 200, account.ServiceFailed},
		{"not a number", `{"data":{"usage":"lots"}}`, 200, account.ServiceFailed},
		{"oversize", `{"data":{"usage":1,"pad":"` + strings.Repeat("x", probeBodyLimit) + `"}}`, 200,
			account.ServiceFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := spendServer(t, "/api/v1/key", c.status, c.body)
			_, se := readSpend(t, p, srv, 0)
			if se == nil || se.Reason != c.reason {
				t.Fatalf("got %+v, want reason %s", se, c.reason)
			}
			if strings.Contains(se.Message, "Insufficient") || strings.Contains(se.Message, "oops") {
				t.Fatalf("the body leaked into the error: %q", se.Message)
			}
		})
	}
	// DeepSeek without is_available, Moonshot without data.
	srv := spendServer(t, "/user/balance", http.StatusOK, `{"balance_infos":[]}`)
	if _, se := readSpend(t, spendProvider(t, "deepseek"), srv, 0); se == nil || se.Reason != account.ServiceFailed {
		t.Fatalf("deepseek shape: %+v", se)
	}
	srv = spendServer(t, "/v1/users/me/balance", http.StatusOK, `{"code":0}`)
	if _, se := readSpend(t, spendProvider(t, "moonshot"), srv, 0); se == nil || se.Reason != account.ServiceFailed {
		t.Fatalf("moonshot shape: %+v", se)
	}
}

// A redirect to another host is not followed: the key is never sent there.
func TestReadServiceSpendRedirectElsewhere(t *testing.T) {
	var reached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the key reached the other host")
		}
		_, _ = w.Write([]byte(`{"data":{"usage":1}}`))
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/v1/key", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	_, se := readSpend(t, spendProvider(t, "openrouter"), srv, 0)
	if se == nil || se.Reason != account.ServiceRedirected || reached.Load() != 0 {
		t.Fatalf("redirect: %+v, other host reached %d times", se, reached.Load())
	}

	// A redirect on the same host is followed.
	var hops atomic.Int32
	same := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/key" {
			hops.Add(1)
			http.Redirect(w, r, "/api/v1/key/", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"usage":2}}`))
	}))
	t.Cleanup(same.Close)
	got, se := readSpend(t, spendProvider(t, "openrouter"), same, 0)
	if se != nil || got.Usage == nil || got.Usage.TotalUSD != 2 || hops.Load() != 1 {
		t.Fatalf("same-host redirect: %+v %v", got, se)
	}
}

func TestReadServiceSpendTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	start := time.Now()
	_, se := readSpend(t, spendProvider(t, "deepseek"), srv, 100*time.Millisecond)
	if se == nil || se.Reason != account.ServiceUnreachable || !strings.Contains(se.Message, "no answer within") {
		t.Fatalf("timeout: %+v", se)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the timeout did not bound the read")
	}
}

// Only the official hosts on https have a spend service; anything else is never called.
func TestSpendServiceHosts(t *testing.T) {
	for _, c := range []struct {
		backend, base, want string
	}{
		{config.BackendClaude, "https://openrouter.ai/api", account.ServiceOpenRouter},
		{config.BackendCodex, "https://openrouter.ai/api/v1", account.ServiceOpenRouter},
		{config.BackendClaude, "https://api.deepseek.com/anthropic", account.ServiceDeepSeek},
		{config.BackendClaude, "https://api.moonshot.ai/anthropic", account.ServiceMoonshot},
		{config.BackendClaude, "https://API.MOONSHOT.CN/anthropic", account.ServiceMoonshot},
		{config.BackendClaude, "https://openrouter.ai:8443/api", ""},
		{config.BackendClaude, "https://evil.example/openrouter.ai", ""},
		{config.BackendClaude, "https://openrouter.ai.evil.example/api", ""},
		{config.BackendClaude, "https://api.z.ai/api/anthropic", ""},
		{config.BackendCodex, "http://localhost:11434/v1", ""},
	} {
		p := account.Provider{ID: "p", Backend: c.backend, BaseURL: c.base}
		if got, _ := p.SpendService(); got != c.want {
			t.Errorf("%s: %q, want %q", c.base, got, c.want)
		}
	}
	if _, err := ReadServiceSpend(t.Context(), account.Provider{ID: "z", Backend: config.BackendClaude,
		BaseURL: "https://api.z.ai/api/anthropic"}, spendKey, ProbeOptions{}); err == nil {
		t.Fatal("a provider without a spend service was read")
	}
}
