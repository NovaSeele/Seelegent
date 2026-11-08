package httpapi_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

const directSecret = "sk-direct-SECRET-0123456789"

// noSecretInData fails when the key is in any file of the data folder other than the key files themselves
// (daemon log, database, task files). The git shim under guard/bin is a copy of this test binary, which holds the
// key as a constant, so it is skipped.
func (e *env) noSecretInData(secret string) {
	e.t.Helper()
	root := filepath.Dir(e.paths.Logs)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.Name() == "seelegent-key" {
			return err
		}
		if d.IsDir() {
			if d.Name() == "bin" && filepath.Base(filepath.Dir(path)) == "guard" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr // a file held open by the daemon is skipped
		}
		if strings.Contains(string(data), secret) {
			e.t.Errorf("the key is in %s", path)
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func noticeCodes(ns []api.Notice) map[string]string {
	out := map[string]string{}
	for _, n := range ns {
		out[n.Code] = n.Field
	}
	return out
}

// TestDirectAdd covers POST /accounts with newProvider and apiKey, the notices of providers and keys, and the
// errors with field.
func TestDirectAdd(t *testing.T) {
	e := newEnv(t)

	// Provider saves: the base URL rules with their notices; a provider without tiers is "model per task".
	var prov api.Provider
	e.must(e.post("/api/v1/providers", map[string]any{"id": "x", "preset": "custom-anthropic",
		"baseUrl": "https://x.example/v1/"}), http.StatusCreated).json(t, &prov)
	if prov.BaseURL != "https://x.example" || !prov.ModelPerTask || noticeCodes(prov.Notices)["base_url_trimmed"] != "baseUrl" {
		t.Fatalf("trimmed provider = %+v", prov)
	}
	e.must(e.post("/api/v1/providers", map[string]any{"id": "anth", "preset": "custom-anthropic",
		"baseUrl": "https://api.anthropic.com/v1/"}), http.StatusCreated).json(t, &prov)
	if !prov.Official || prov.Preset != "anthropic" || prov.BaseURL != "" || noticeCodes(prov.Notices)["official_preset"] == "" {
		t.Fatalf("switched provider = %+v", prov)
	}
	// Tiers are set through PATCH; the notices are only in save answers.
	prov = api.Provider{}
	e.must(e.req(http.MethodPatch, "/api/v1/providers/x", map[string]any{"tiers": map[string]string{"standard": "glm-5"}},
		nil), http.StatusOK).json(t, &prov)
	if prov.Tiers.Standard != "glm-5" || prov.ModelPerTask || prov.Notices != nil {
		t.Fatalf("tiers = %+v", prov)
	}
	var pl api.ProviderList
	e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &pl)
	for _, p := range pl.Items {
		if p.Notices != nil {
			t.Errorf("GET /providers has notices: %+v", p)
		}
	}

	// One call: a new provider (created), the account and its key.
	var created api.AccountCreated
	r := e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "custom-anthropic",
		"baseUrl": "https://y.example/v1/messages"}, "apiKey": "LLM_API_KEY=" + directSecret})
	e.must(r, http.StatusCreated).json(t, &created)
	if !created.ProviderCreated || created.NewProvider == nil || created.NewProvider.ID != "custom-anthropic" ||
		created.NewProvider.BaseURL != "https://y.example" || created.Provider != "custom-anthropic" ||
		created.Backend != "claude" || created.Kind != "api-key" || created.Label != "Custom (Anthropic API) 1" ||
		created.KeyHint != "…6789" || created.TestResult != api.TestNotTested || !created.ModelPerTask ||
		created.Status != api.AccountOK {
		t.Fatalf("direct add = %s", r.body)
	}
	codes := noticeCodes(created.Notices)
	if codes["key_name_removed"] != "apiKey" || codes["base_url_trimmed"] != "baseUrl" {
		t.Errorf("notices = %+v", created.Notices)
	}
	// The same service again: reused, never a second provider.
	created = api.AccountCreated{}
	e.must(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "custom-anthropic",
		"baseUrl": "https://y.example"}, "apiKey": directSecret}), http.StatusCreated).json(t, &created)
	if created.ProviderCreated || created.NewProvider == nil || created.NewProvider.ID != "custom-anthropic" ||
		created.NewProvider.Accounts != 2 || noticeCodes(created.Notices)["provider_reused"] == "" {
		t.Fatalf("reuse = %+v", created)
	}
	// An existing provider by id, with a key; backend and kind may be left out.
	created = api.AccountCreated{}
	e.must(e.post("/api/v1/accounts", map[string]any{"provider": "anth", "apiKey": "sk-wrong-prefix-1234"}),
		http.StatusCreated).json(t, &created)
	if created.Backend != "claude" || created.NewProvider != nil || noticeCodes(created.Notices)["key_prefix"] != "apiKey" {
		t.Fatalf("existing provider = %+v", created)
	}

	// Errors name the field and leave nothing behind.
	var before []api.Account
	e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &before)
	e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &pl)
	providersBefore := len(pl.Items)
	got := e.wantError(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "deepseek"},
		"apiKey": "https://api.deepseek.com/" + directSecret}), http.StatusBadRequest, api.CodeInvalidArgument, "apiKey")
	if got.Details["reason"] != "url" || strings.Contains(got.Message, directSecret) {
		t.Errorf("bad key error = %+v", got)
	}
	e.wantError(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "custom-anthropic",
		"baseUrl": "http://evil.example"}, "apiKey": directSecret}), http.StatusBadRequest, api.CodeInvalidArgument,
		"newProvider.baseUrl")
	e.wantError(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "nope"}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "newProvider.preset")
	e.wantError(e.post("/api/v1/accounts", map[string]any{"provider": "nope", "apiKey": directSecret}),
		http.StatusBadRequest, api.CodeInvalidArgument, "provider")
	e.wantError(e.post("/api/v1/accounts", map[string]any{"backend": "codex", "newProvider": map[string]any{
		"preset": "deepseek"}}), http.StatusBadRequest, api.CodeInvalidArgument, "backend")
	var after []api.Account
	e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &after)
	e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &pl)
	if len(after) != len(before) || len(pl.Items) != providersBefore {
		t.Fatalf("a failed add left %d accounts (was %d) and %d providers (was %d)", len(after), len(before),
			len(pl.Items), providersBefore)
	}

	// PUT key cleans the key the same way; its notices and errors name the field "key".
	var keyed api.Account
	e.must(e.req(http.MethodPut, "/api/v1/accounts/"+created.ID+"/key", map[string]any{"key": `"` + directSecret + `"`},
		nil), http.StatusOK).json(t, &keyed)
	if noticeCodes(keyed.Notices)["key_quotes_removed"] != "key" || keyed.KeyHint != "…6789" {
		t.Fatalf("PUT key = %+v", keyed)
	}
	got = e.wantError(e.req(http.MethodPut, "/api/v1/accounts/"+created.ID+"/key", map[string]any{
		"key": "sk-ant abc"}, nil), http.StatusBadRequest, api.CodeInvalidArgument, "key")
	if got.Details["reason"] != "whitespace" {
		t.Errorf("whitespace key error = %+v", got)
	}

	// A delegate on a model-per-task provider without a model is refused at once with a readable message.
	e.must(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"backends": map[string]any{"claude": map[string]any{
		"enabled": true}}}, nil), http.StatusOK)
	got = e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "hi", "cwd": e.cwd, "backend": "claude",
		"provider": "anth"}), http.StatusBadRequest, api.CodeInvalidArgument, "")
	if !strings.Contains(got.Message, "provider anth has no default model: pass model") ||
		got.Details["reason"] != "no_default_model" {
		t.Errorf("delegate without model = %+v", got)
	}
	got = e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "hi", "cwd": e.cwd, "backend": "claude",
		"provider": "anth", "tier": "strong"}), http.StatusBadRequest, api.CodeInvalidArgument, "")
	if !strings.Contains(got.Message, "has no default model") {
		t.Errorf("delegate with only a tier = %+v", got)
	}
	e.noSecretInData(directSecret)
}

// TestKeyTestEndpoint covers POST /providers/test against a local service.
func TestKeyTestEndpoint(t *testing.T) {
	e := newEnv(t)
	var status atomic.Int32
	status.Store(http.StatusOK)
	var otherReached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherReached.Add(1)
	}))
	defer other.Close()
	var lastAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth.Store(r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(int(status.Load()))
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-5"},{"id":"glm-5-air"}]}`))
		case "/away/v1/models":
			http.Redirect(w, r, other.URL+"/v1/models", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	newProvider := map[string]any{"preset": "custom-anthropic", "baseUrl": srv.URL + "/v1/"}

	var res api.KeyTestResult
	r := e.post("/api/v1/providers/test", map[string]any{"newProvider": newProvider, "apiKey": directSecret})
	e.must(r, http.StatusOK).json(t, &res)
	if !res.OK || res.Reason != api.TestOK || res.Status != 200 || len(res.Models) != 2 ||
		lastAuth.Load() != "Bearer "+directSecret || noticeCodes(res.Notices)["base_url_trimmed"] == "" || res.Saved {
		t.Fatalf("200 = %s", r.body)
	}
	status.Store(http.StatusUnauthorized)
	r = e.post("/api/v1/providers/test", map[string]any{"newProvider": newProvider, "apiKey": directSecret})
	e.must(r, http.StatusOK).json(t, &res)
	if res.OK || res.Reason != api.TestRefused || res.Status != 401 || len(res.Models) != 0 || res.Models == nil {
		t.Fatalf("401 = %s", r.body)
	}
	r = e.post("/api/v1/providers/test", map[string]any{"newProvider": map[string]any{"preset": "custom-anthropic",
		"baseUrl": srv.URL + "/wrong"}, "apiKey": directSecret})
	e.must(r, http.StatusOK).json(t, &res)
	if res.Reason != api.TestNotFound {
		t.Fatalf("404 = %s", r.body)
	}
	r = e.post("/api/v1/providers/test", map[string]any{"newProvider": map[string]any{"preset": "custom-anthropic",
		"baseUrl": srv.URL + "/away"}, "apiKey": directSecret})
	e.must(r, http.StatusOK).json(t, &res)
	if res.Reason != api.TestRedirected || otherReached.Load() != 0 {
		t.Fatalf("cross-host redirect = %s (other host reached %d times)", r.body, otherReached.Load())
	}

	// With accountId the stored key is used and the result saved on the account.
	var created api.AccountCreated
	e.must(e.post("/api/v1/accounts", map[string]any{"newProvider": newProvider, "apiKey": directSecret}),
		http.StatusCreated).json(t, &created)
	status.Store(http.StatusOK)
	r = e.post("/api/v1/providers/test", map[string]any{"accountId": created.ID})
	e.must(r, http.StatusOK).json(t, &res)
	if !res.OK || !res.Saved {
		t.Fatalf("account test = %s", r.body)
	}
	var accounts []api.Account
	e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &accounts)
	for _, a := range accounts {
		if a.ID == created.ID && (a.TestResult != api.TestOK || a.TestedAt == "") {
			t.Fatalf("saved test = %+v", a)
		}
	}
	status.Store(http.StatusForbidden)
	e.must(e.post("/api/v1/providers/test", map[string]any{"accountId": created.ID}), http.StatusOK).json(t, &res)
	e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &accounts)
	for _, a := range accounts {
		if a.ID == created.ID && a.TestResult != api.TestRefused {
			t.Fatalf("refused test = %+v", a)
		}
	}
	// A stored key goes only to the account's own provider: never to a newProvider or another provider's address.
	var attackerReached atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerReached.Add(1)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer attacker.Close()
	e.must(e.post("/api/v1/providers", map[string]any{"id": "elsewhere", "name": "Elsewhere", "backend": "claude",
		"preset": "custom-anthropic", "baseUrl": attacker.URL}), http.StatusCreated)
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"accountId": created.ID, "newProvider": map[string]any{
		"preset": "custom-anthropic", "baseUrl": attacker.URL}}), http.StatusBadRequest, api.CodeInvalidArgument,
		"newProvider")
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"accountId": created.ID, "provider": "elsewhere"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "provider")
	if attackerReached.Load() != 0 {
		t.Fatalf("the stored key was sent to another address (%d calls)", attackerReached.Load())
	}
	// Naming the account's own provider is allowed.
	status.Store(http.StatusOK)
	r = e.post("/api/v1/providers/test", map[string]any{"accountId": created.ID, "provider": created.Provider})
	e.must(r, http.StatusOK).json(t, &res)
	if !res.OK || !res.Saved {
		t.Fatalf("own provider = %s", r.body)
	}

	// Bad requests.
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"provider": created.Provider}),
		http.StatusBadRequest, api.CodeInvalidArgument, "apiKey")
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"provider": created.Provider, "apiKey": directSecret,
		"accountId": created.ID}), http.StatusBadRequest, api.CodeInvalidArgument, "apiKey")
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"provider": "nope", "apiKey": directSecret}),
		http.StatusBadRequest, api.CodeInvalidArgument, "provider")
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"newProvider": map[string]any{"preset": "custom-anthropic",
		"baseUrl": "http://evil.example"}, "apiKey": directSecret}), http.StatusBadRequest, api.CodeInvalidArgument,
		"newProvider.baseUrl")
	got := e.wantError(e.post("/api/v1/providers/test", map[string]any{"provider": created.Provider,
		"apiKey": "line one\n" + directSecret}), http.StatusBadRequest, api.CodeInvalidArgument, "apiKey")
	if strings.Contains(got.Message, directSecret) {
		t.Errorf("the key is in an error: %+v", got)
	}
	// Mutating-request rules: the X-Seelegent header is required, and workers may not call it.
	e.wantError(e.req(http.MethodPost, "/api/v1/providers/test", map[string]any{"accountId": created.ID},
		map[string]string{"X-Seelegent": ""}), http.StatusForbidden, api.CodeForbidden, "")
	e.client.Transport = &http.Transport{DisableKeepAlives: true} // the caller is cached per connection
	e.who.set(&httpapi.Caller{Worker: true})
	e.wantError(e.post("/api/v1/providers/test", map[string]any{"accountId": created.ID}), http.StatusForbidden,
		api.CodeForbidden, "")
	e.who.set(nil)
	e.noSecretInData(directSecret)
}
