package httpapi_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestProviderAllowList: Provider.models is the allow-list exactly as set (never the tier models), allowedModels the
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
