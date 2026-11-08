package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// Key test reasons (ProbeResult.Reason). They are also what account.Account.TestResult stores.
const (
	// ProbeOK: the service accepted the key and listed its models.
	ProbeOK = account.TestOK
	// ProbeRefused: 401 or 403, the service refused the key.
	ProbeRefused = "refused"
	// ProbeNotFound: 404 on a hand-made base URL: the address is probably wrong.
	ProbeNotFound = "not_found"
	// ProbeNoModelsEndpoint: 404 or 405 on a preset's own address, or 405 anywhere: the service has no models
	// endpoint, so the key cannot be checked without spending tokens (it is not).
	ProbeNoModelsEndpoint = "no_models_endpoint"
	// ProbeUnreachable: DNS, connection, TLS or timeout.
	ProbeUnreachable = "unreachable"
	// ProbeRedirected: the service redirected to another host; the request was not followed, so the key was not
	// sent there.
	ProbeRedirected = "redirected"
	// ProbeError: any other answer (5xx, 429, a body that is not a model list, ...).
	ProbeError = "error"
)

// ProbeReasons lists every reason, for the API's type and tests.
func ProbeReasons() []string {
	return []string{ProbeOK, ProbeRefused, ProbeNotFound, ProbeNoModelsEndpoint, ProbeUnreachable, ProbeRedirected,
		ProbeError}
}

// Probe limits.
const (
	// ProbeTimeout bounds the whole test call.
	ProbeTimeout = 10 * time.Second
	// probeBodyLimit caps the response body read.
	probeBodyLimit = 1 << 20
	// probeMaxRedirects caps redirects on the same host.
	probeMaxRedirects = 3
	// anthropicVersion is the API version header every Anthropic-style request carries.
	anthropicVersion = "2023-06-01"
	// AnthropicAPI is the official Anthropic API, which Claude Code uses when no base URL is set.
	AnthropicAPI = "https://api.anthropic.com"
)

// ProbeOptions configure ProbeKey; the zero value is the production set-up.
type ProbeOptions struct {
	// Transport sends the request (http.DefaultTransport when nil).
	Transport http.RoundTripper
	// AnthropicURL replaces AnthropicAPI for the official provider (tests).
	AnthropicURL string
	// Timeout replaces ProbeTimeout (tests).
	Timeout time.Duration
}

// ProbeResult is the answer of a key test. Message never holds the key.
type ProbeResult struct {
	OK      bool
	Status  int
	Reason  string
	Message string
	Models  []string
	Latency time.Duration
}

// ProbeKey checks key against the provider with one cheap call that spends no tokens: GET {base}/v1/models for
// claude providers, GET {base}/models for codex ones. The key is sent the way the backend's environment rules send
// it to the CLI (KeyEnv): x-api-key for the official Anthropic API, a bearer token for every other provider, plus
// the anthropic-version header for claude. The call has a 10 s timeout and reads at most 1 MB; it follows
// redirects only on the same scheme and host, so the key never reaches another host. The provider must pass
// validation (Provider.Check), which also cleans its base URL.
func ProbeKey(ctx context.Context, p account.Provider, key string, o ProbeOptions) ProbeResult {
	req, err := probeRequest(ctx, p, key, o)
	if err != nil {
		return ProbeResult{Reason: ProbeError, Message: err.Error()}
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = ProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(ctx)
	transport := o.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > probeMaxRedirects {
			return errors.New("too many redirects")
		}
		if first := via[0].URL; next.URL.Scheme != first.Scheme || !strings.EqualFold(next.URL.Host, first.Host) {
			return http.ErrUseLastResponse // the key stays with the host it was meant for
		}
		return nil
	}}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return ProbeResult{Reason: ProbeUnreachable, Latency: latency, Message: unreachableMessage(req.URL, err, timeout)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
	out := ProbeResult{Status: resp.StatusCode, Latency: latency}
	switch code := resp.StatusCode; {
	case code >= 300 && code < 400:
		out.Reason = ProbeRedirected
		out.Message = fmt.Sprintf("%s redirected to another address; the key was not sent there", req.URL.Host)
		if loc, err := resp.Location(); err == nil {
			out.Message = fmt.Sprintf("%s redirected to %s; the key was not sent there", req.URL.Host, loc.Host)
		}
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		out.Reason, out.Message = ProbeRefused, fmt.Sprintf("the service refused this key (HTTP %d)", code)
	case code == http.StatusMethodNotAllowed || (code == http.StatusNotFound && presetAddress(p)):
		out.Reason = ProbeNoModelsEndpoint
		out.Message = "key not verified: this service has no model list to check it against; the first turn will test it"
	case code == http.StatusNotFound:
		out.Reason = ProbeNotFound
		out.Message = fmt.Sprintf("wrong address: %s answered 404 (%s)", redactedURL(req.URL), notFoundHint(p))
	case code >= 200 && code < 300:
		models, err := parseModels(body)
		if err != nil {
			out.Reason, out.Message = ProbeError, "the service answered, but not with a model list: "+err.Error()
			break
		}
		out.OK, out.Reason, out.Models = true, ProbeOK, models
		out.Message = fmt.Sprintf("the key works; %d models available", len(models))
	default:
		out.Reason, out.Message = ProbeError, fmt.Sprintf("the service answered HTTP %d", code)
	}
	return out
}

// probeRequest builds the test request from the backend's environment rules.
func probeRequest(ctx context.Context, p account.Provider, key string, o ProbeOptions) (*http.Request, error) {
	var (
		rules account.EnvRules
		path  string
	)
	switch p.Backend {
	case config.BackendClaude:
		rules, path = &Claude{}, "/v1/models"
	case config.BackendCodex:
		rules, path = &Codex{}, "/models"
	default:
		return nil, fmt.Errorf("provider backend %q cannot be tested", p.Backend)
	}
	env, err := rules.KeyEnv(p, key, "")
	if err != nil {
		return nil, fmt.Errorf("provider %s cannot be tested: %w", p.ID, err)
	}
	header := http.Header{}
	base := p.BaseURL
	switch {
	case env["ANTHROPIC_API_KEY"] != "":
		header.Set("x-api-key", env["ANTHROPIC_API_KEY"])
		base = AnthropicAPI
		if o.AnthropicURL != "" {
			base = o.AnthropicURL
		}
	case env["ANTHROPIC_AUTH_TOKEN"] != "":
		header.Set("Authorization", "Bearer "+env["ANTHROPIC_AUTH_TOKEN"])
		base = env["ANTHROPIC_BASE_URL"]
	case env[account.EnvProviderKey] != "":
		header.Set("Authorization", "Bearer "+env[account.EnvProviderKey])
	default:
		return nil, fmt.Errorf("provider %s cannot be tested: no key variable", p.ID)
	}
	if p.Backend == config.BackendClaude {
		header.Set("anthropic-version", anthropicVersion)
	}
	header.Set("Accept", "application/json")
	if base == "" {
		return nil, fmt.Errorf("provider %s has no base URL", p.ID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("provider %s: bad base URL", p.ID)
	}
	req.Header = header
	return req, nil
}

// presetAddress reports a provider whose base URL is its preset's own (a known service, so a 404 means "no models
// endpoint", not "wrong address").
func presetAddress(p account.Provider) bool {
	if p.Official {
		return true
	}
	pr, ok := account.Presets()[p.Preset]
	return ok && pr.BaseURL != "" && strings.EqualFold(strings.TrimRight(pr.BaseURL, "/"), strings.TrimRight(p.BaseURL, "/"))
}

// notFoundHint says what to check after a 404.
func notFoundHint(p account.Provider) string {
	if p.Backend == config.BackendCodex {
		return "OpenAI-compatible addresses usually end in /v1"
	}
	return "check the base URL; Claude Code adds /v1/messages itself, so it must not end in /v1"
}

// redactedURL is the URL without user info or query (neither should hold anything, but never print them).
func redactedURL(u *url.URL) string {
	c := *u
	c.User, c.RawQuery, c.Fragment = nil, "", ""
	return c.String()
}

// unreachableMessage describes a failed call by its host and the kind of failure.
func unreachableMessage(u *url.URL, err error, timeout time.Duration) string {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("could not reach %s: no answer within %s", u.Host, timeout)
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("could not reach %s: the name does not resolve", u.Host)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // url.Error repeats the URL
	}
	return fmt.Sprintf("could not reach %s: %v", u.Host, err)
}

// parseModels reads a model list: {"data": [{"id": ...}, ...]} (Anthropic and OpenAI alike).
func parseModels(body []byte) ([]string, error) {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errors.New("not JSON")
	}
	if list.Data == nil {
		return nil, errors.New("no data list")
	}
	out := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if id := strings.TrimSpace(m.ID); id != "" && len(id) <= 200 {
			out = append(out, id)
		}
	}
	return out, nil
}
