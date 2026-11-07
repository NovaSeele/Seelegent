package httpapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestProvidersChangedOnAccounts: Provider.accounts counts the accounts, so adding or removing an account of a
// provider sends providers.changed, through the handlers and through the account feed (changes it only sees).
func TestProvidersChangedOnAccounts(t *testing.T) {
	e := newEnv(t)
	e.must(e.post("/api/v1/providers", map[string]any{"id": "ds", "preset": "deepseek"}), http.StatusCreated)
	var boot api.BootState
	e.must(e.get("/api/v1/state"), http.StatusOK).json(t, &boot)
	stream := e.stream(boot.LastEventID)
	count := func(want int) {
		t.Helper()
		var pl api.ProviderList
		e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &pl)
		if len(pl.Items) != 1 || pl.Items[0].Accounts != want {
			t.Fatalf("providers = %+v, want %d account(s)", pl.Items, want)
		}
	}
	// next returns the next account or provider frame, skipping the rest.
	next := func() frame {
		t.Helper()
		for {
			f := stream.next()
			if strings.HasPrefix(f.event, "account.") || f.event == "providers.changed" {
				return f
			}
		}
	}
	expect := func(events ...string) {
		t.Helper()
		for _, want := range events {
			if f := next(); f.event != want {
				t.Fatalf("frame %s %s, want %s", f.event, f.data, want)
			}
		}
	}

	// An account without a provider changes no provider: its account.updated comes alone, and the next frame
	// belongs to the API-key account.
	var plain, keyed api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "agy", "kind": "isolated", "label": "P",
		"enabled": false}), http.StatusCreated).json(t, &plain)
	expect("account.updated")
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "claude", "kind": "api-key", "label": "K",
		"provider": "ds"}), http.StatusCreated).json(t, &keyed)
	expect("account.updated", "providers.changed")
	count(1)
	e.must(e.req(http.MethodDelete, "/api/v1/accounts/"+keyed.ID, nil, nil), http.StatusNoContent)
	expect("account.deleted", "providers.changed")
	count(0)

	// Changes the handlers never saw reach the clients through the feed.
	a, _, err := e.a.Accounts.Add(t.Context(), account.New{Backend: "claude", Kind: account.KindAPIKey, Label: "D",
		Provider: "ds"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.a.Pool.Notify()
	expect("account.updated", "providers.changed")
	if err := e.a.Accounts.Remove(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	e.a.Pool.Notify()
	expect("account.deleted", "providers.changed")
	count(0)
}

// TestProviderAllowList:Provider.models is the allow-list exactly as set (never the tier models), allowedModels the
// pattern enforced; a hand-written pattern answers models []; models [] clears both (any model); the tier models
// stay allowed on top of the list.
func TestProviderAllowList(t *testing.T) {
	e := newEnv(t)
	var prov api.Provider
	// The example from the first live run.
	e.must(e.post("/api/v1/providers", map[string]any{"id": "anthropic", "name": "Anthropic", "backend": "claude",
		"baseUrl": "", "enabled": true, "models": []string{"claude-sonnet-5", "claude-opus-5"}, "preset": "anthropic"}),
		http.StatusCreated).json(t, &prov)
	if !slices.Equal(prov.Models, []string{"claude-sonnet-5", "claude-opus-5"}) ||
		prov.AllowedModels != `^(?:claude-sonnet-5|claude-opus-5)$` {
		t.Fatalf("created = %+v", prov)
	}

	e.must(e.post("/api/v1/providers", map[string]any{"id": "ds", "preset": "deepseek",
		"tiers": map[string]string{"fast": "deepseek-chat"}, "models": []string{"deepseek-v4", "deepseek-r2"}}),
		http.StatusCreated).json(t, &prov)
	want := []string{"deepseek-v4", "deepseek-r2"}
	if !slices.Equal(prov.Models, want) || prov.Tiers.Fast != "deepseek-chat" {
		t.Fatalf("created with tiers = %+v", prov)
	}
	get := func(id string) api.Provider {
		t.Helper()
		var pl api.ProviderList
		e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &pl)
		for _, p := range pl.Items {
			if p.ID == id {
				return p
			}
		}
		t.Fatalf("provider %s missing from %+v", id, pl.Items)
		return api.Provider{}
	}
	if p := get("ds"); !slices.Equal(p.Models, want) || p.AllowedModels != `^(?:deepseek-v4|deepseek-r2)$` {
		t.Fatalf("GET = %+v", p)
	}
	// An API-key account of the provider may run the tier model and the listed ones.
	var acc api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "claude", "kind": "api-key", "label": "K",
		"provider": "ds"}), http.StatusCreated).json(t, &acc)
	if !slices.Equal(acc.Models, []string{"deepseek-chat", "deepseek-v4", "deepseek-r2"}) {
		t.Fatalf("account models = %v", acc.Models)
	}
	// Tiers alone leave the list as it is.
	e.must(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"tiers": map[string]string{
		"fast": "deepseek-chat", "max": "deepseek-reasoner"}}, nil), http.StatusOK).json(t, &prov)
	if !slices.Equal(prov.Models, want) || prov.Tiers.Max != "deepseek-reasoner" {
		t.Fatalf("tiers patch = %+v", prov)
	}
	// models [] with the echo of the list's own pattern clears the list (a UI that sends the whole provider back).
	e.must(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"models": []string{},
		"allowedModels": prov.AllowedModels}, nil), http.StatusOK).json(t, &prov)
	if len(prov.Models) != 0 || prov.Models == nil || prov.AllowedModels != "" || prov.Tiers.Fast != "deepseek-chat" {
		t.Fatalf("cleared = %+v", prov)
	}
	// A hand-written pattern: models [] and the pattern kept, also when sent back with models [].
	for _, body := range []map[string]any{{"allowedModels": "^glm-"}, {"models": []string{}, "allowedModels": "^glm-"}} {
		e.must(e.req(http.MethodPatch, "/api/v1/providers/ds", body, nil), http.StatusOK).json(t, &prov)
		if len(prov.Models) != 0 || prov.AllowedModels != "^glm-" {
			t.Fatalf("pattern %v = %+v", body, prov)
		}
	}
	// A list replaces the pattern; models [] alone clears it.
	e.must(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"models": []string{"glm-5"}}, nil),
		http.StatusOK).json(t, &prov)
	if !slices.Equal(prov.Models, []string{"glm-5"}) || prov.AllowedModels != `^(?:glm-5)$` {
		t.Fatalf("list after pattern = %+v", prov)
	}
	e.must(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"models": []string{}}, nil),
		http.StatusOK).json(t, &prov)
	if len(prov.Models) != 0 || prov.AllowedModels != "" {
		t.Fatalf("cleared again = %+v", prov)
	}
	e.wantError(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"models": []string{"a b"}}, nil),
		http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"allowedModels": "("}, nil),
		http.StatusBadRequest, api.CodeInvalidArgument, "")
}
