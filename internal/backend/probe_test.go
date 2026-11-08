package backend

import (
	"context"
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

const probeSecret = "sk-probe-SECRET-0123456789"

// probeServer answers GET <path> with status and body and records the headers it saw.
func probeServer(t *testing.T, path string, status int, body string, seen *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Header.Clone()
		}
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checkedProvider(t *testing.T, p account.Provider) account.Provider {
	t.Helper()
	if _, err := p.Check(); err != nil {
		t.Fatal(err)
	}
	return p
}

func noKeyIn(t *testing.T, res ProbeResult) {
	t.Helper()
	if strings.Contains(res.Message, probeSecret) {
		t.Errorf("the key is in the message: %q", res.Message)
	}
}

func TestProbeKeyClaudeCompatible(t *testing.T) {
	var seen http.Header
	srv := probeServer(t, "/v1/models", http.StatusOK,
		`{"data":[{"id":"glm-5"},{"id":"glm-5-air"}],"has_more":false}`, &seen)
	// The base URL as a user pastes it: Check trims /v1/, the test calls {base}/v1/models.
	p := checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, Preset: "custom-anthropic",
		BaseURL: srv.URL + "/v1/"})
	res := ProbeKey(t.Context(), p, probeSecret, ProbeOptions{})
	if !res.OK || res.Reason != ProbeOK || res.Status != 200 || len(res.Models) != 2 || res.Models[0] != "glm-5" {
		t.Fatalf("ProbeKey = %+v", res)
	}
	// Mirrors the env rules: a custom provider gets the key as a bearer token (ANTHROPIC_AUTH_TOKEN).
	if seen.Get("Authorization") != "Bearer "+probeSecret || seen.Get("x-api-key") != "" ||
		seen.Get("anthropic-version") == "" {
		t.Errorf("headers = %v", seen)
	}
	noKeyIn(t, res)
}

func TestProbeKeyOfficialAndCodex(t *testing.T) {
	var seen http.Header
	srv := probeServer(t, "/v1/models", http.StatusOK, `{"data":[{"id":"claude-haiku-5-5"}]}`, &seen)
	official, err := account.FromPreset("anthropic", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	res := ProbeKey(t.Context(), official, probeSecret, ProbeOptions{AnthropicURL: srv.URL})
	if !res.OK || seen.Get("x-api-key") != probeSecret || seen.Get("Authorization") != "" {
		t.Fatalf("official: %+v, headers %v", res, seen)
	}
	codexSrv := probeServer(t, "/v1/models", http.StatusOK, `{"object":"list","data":[{"id":"gpt-x"}]}`, &seen)
	p := checkedProvider(t, account.Provider{ID: "o", Backend: config.BackendCodex, BaseURL: codexSrv.URL + "/v1"})
	res = ProbeKey(t.Context(), p, probeSecret, ProbeOptions{})
	if !res.OK || len(res.Models) != 1 || seen.Get("Authorization") != "Bearer "+probeSecret ||
		seen.Get("anthropic-version") != "" {
		t.Fatalf("codex: %+v, headers %v", res, seen)
	}
}

func TestProbeKeyFailures(t *testing.T) {
	custom := func(url string) account.Provider {
		return checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, Preset: "custom-anthropic",
			BaseURL: url})
	}
	cases := []struct {
		name   string
		status int
		body   string
		path   string
		reason string
	}{
		{"refused", http.StatusUnauthorized, `{"error":"bad key"}`, "/v1/models", ProbeRefused},
		{"forbidden", http.StatusForbidden, ``, "/v1/models", ProbeRefused},
		{"wrong address", 0, ``, "/elsewhere", ProbeNotFound},
		{"no list", http.StatusMethodNotAllowed, ``, "/v1/models", ProbeNoModelsEndpoint},
		{"server error", http.StatusBadGateway, ``, "/v1/models", ProbeError},
		{"not a list", http.StatusOK, `<html>`, "/v1/models", ProbeError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := probeServer(t, c.path, c.status, c.body, nil)
			res := ProbeKey(t.Context(), custom(srv.URL), probeSecret, ProbeOptions{})
			if res.OK || res.Reason != c.reason {
				t.Fatalf("ProbeKey = %+v, want %s", res, c.reason)
			}
			noKeyIn(t, res)
		})
	}
	// A 404 on a preset's own address means "no models endpoint", not "wrong address".
	p := account.Provider{ID: "ds", Backend: config.BackendClaude, Preset: "deepseek", BaseURL: "https://moved.example"}
	if presetAddress(p) {
		t.Fatal("a moved base URL is not the preset's")
	}
	if !presetAddress(account.Provider{Preset: "deepseek", BaseURL: account.Presets()["deepseek"].BaseURL}) {
		t.Fatal("the preset's own base URL")
	}
}

func TestProbeKeyTimeoutAndUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	p := checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, BaseURL: srv.URL})
	start := time.Now()
	res := ProbeKey(t.Context(), p, probeSecret, ProbeOptions{Timeout: 200 * time.Millisecond})
	if res.Reason != ProbeUnreachable || res.OK || time.Since(start) > 5*time.Second ||
		!strings.Contains(res.Message, "no answer within") {
		t.Fatalf("timeout: %+v", res)
	}
	noKeyIn(t, res)
	// Nothing listens on a closed server.
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	res = ProbeKey(t.Context(), checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, BaseURL: url}),
		probeSecret, ProbeOptions{Timeout: 2 * time.Second})
	if res.Reason != ProbeUnreachable {
		t.Fatalf("closed: %+v", res)
	}
	noKeyIn(t, res)
}

// A redirect to another host is not followed: the key never reaches it. One on the same host is.
func TestProbeKeyRedirects(t *testing.T) {
	var reached atomic.Int32
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if strings.Contains(r.Header.Get("Authorization")+r.Header.Get("x-api-key"), probeSecret) {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away/v1/models":
			http.Redirect(w, r, other.URL+"/v1/models", http.StatusFound)
		case "/here/v1/models":
			http.Redirect(w, r, "/there/v1/models", http.StatusTemporaryRedirect)
		case "/there/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, BaseURL: srv.URL + "/away"})
	res := ProbeKey(t.Context(), p, probeSecret, ProbeOptions{})
	if res.OK || res.Reason != ProbeRedirected || reached.Load() != 0 || leaked.Load() {
		t.Fatalf("cross-host redirect: %+v (other host reached %d times)", res, reached.Load())
	}
	noKeyIn(t, res)
	p = checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, BaseURL: srv.URL + "/here"})
	if res := ProbeKey(t.Context(), p, probeSecret, ProbeOptions{}); !res.OK {
		t.Fatalf("same-host redirect: %+v", res)
	}
}

func TestProbeKeyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := checkedProvider(t, account.Provider{ID: "x", Backend: config.BackendClaude, BaseURL: "https://x.example"})
	if res := ProbeKey(ctx, p, probeSecret, ProbeOptions{}); res.OK || res.Reason != ProbeUnreachable {
		t.Fatalf("cancelled: %+v", res)
	}
}

// A provider without tier models refuses a turn without a model at once, with a message that says what to do.
func TestResolveNoDefaultModel(t *testing.T) {
	set := config.Defaults()
	set.Backends.Claude.Enabled = true
	p, err := account.FromPreset("anthropic", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	pol, err := PolicyFor(config.BackendClaude, set, &p)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := pol.Resolve("claude-sonnet-5-5", "", config.TierStandard); err != nil || m != "claude-sonnet-5-5" {
		t.Fatalf("explicit model = %q, %v", m, err)
	}
	for _, c := range []struct{ model, tier string }{{"", ""}, {"", config.TierStrong}, {"max", ""}} {
		_, err := pol.Resolve(c.model, c.tier, config.TierStandard)
		var nd *NoDefaultModelError
		if !errors.Is(err, ErrModel) || !errors.As(err, &nd) ||
			!strings.Contains(err.Error(), "provider anthropic has no default model: pass model") {
			t.Errorf("Resolve(%q, %q) = %v", c.model, c.tier, err)
		}
	}
	// With models known they are given as examples; with some tiers, the tiers it has are named.
	p.Models = []string{"claude-haiku-5-5", "claude-sonnet-5-5"}
	p.Tiers = config.TierModels{Fast: "claude-haiku-5-5"}
	if pol, err = PolicyFor(config.BackendClaude, set, &p); err != nil {
		t.Fatal(err)
	}
	_, err = pol.Resolve("", config.TierMax, config.TierStandard)
	if err == nil || !strings.Contains(err.Error(), "e.g. claude-haiku-5-5, claude-sonnet-5-5") ||
		!strings.Contains(err.Error(), "a tier it has (fast)") {
		t.Errorf("partial tiers: %v", err)
	}
	// A backend's own policy keeps its message.
	bpol, err := PolicyFor(config.BackendClaude, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	bpol.Tiers = config.TierModels{}
	if _, err := bpol.Resolve("", "", config.TierStandard); err == nil || !strings.Contains(err.Error(), "backends.claude") {
		t.Errorf("backend policy: %v", err)
	}
}
