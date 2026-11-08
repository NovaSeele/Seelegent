package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// The price table of a provider through the API: none at first (spend source none, tokens counted), a bad row
// refused with its field, the table stored (spend source prices), the spend of a run counted at those prices, and
// the table cleared with [].
func TestProviderPrices(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.must(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"backends": map[string]any{"claude": map[string]any{
		"enabled": true}}}, nil), http.StatusOK)
	var created api.AccountCreated
	e.must(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "zai"},
		"apiKey": "zai-test-0123456789abcdef", "budgetUsd": 1}), http.StatusCreated).json(t, &created)
	if created.NewProvider == nil || created.SpendSource != api.SpendNone || created.BudgetState != api.BudgetUnknown {
		t.Fatalf("created = %+v", created)
	}
	prov := created.NewProvider
	if prov.SpendSource != api.SpendNone || prov.PricingURL == "" || prov.Prices == nil || len(prov.Prices) != 0 {
		t.Fatalf("provider = %+v", prov)
	}
	path := "/api/v1/providers/" + prov.ID + "/prices"
	var table api.PriceTable
	e.must(e.get(path), http.StatusOK).json(t, &table)
	if table.Provider != prov.ID || len(table.Prices) != 0 || table.SpendSource != api.SpendNone ||
		table.PricingURL != prov.PricingURL {
		t.Fatalf("empty table = %+v", table)
	}
	e.wantError(e.req(http.MethodGet, "/api/v1/providers/nope/prices", nil, nil), http.StatusNotFound,
		api.CodeNotFound, "")
	e.wantError(e.req(http.MethodPut, "/api/v1/providers/nope/prices", map[string]any{"prices": []any{}}, nil),
		http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.req(http.MethodPut, path, map[string]any{}, nil), http.StatusBadRequest, api.CodeInvalidArgument,
		"prices")
	e.wantError(e.req(http.MethodPut, path, map[string]any{"prices": []any{map[string]any{"model": "*",
		"inputUsd": -1, "outputUsd": 1}}}, nil), http.StatusBadRequest, api.CodeInvalidArgument, "prices[0].inputUsd")

	e.must(e.req(http.MethodPut, path, map[string]any{"prices": []any{
		map[string]any{"model": "*", "inputUsd": 1, "outputUsd": 4},
		map[string]any{"model": "glm-5", "inputUsd": 2, "cachedInputUsd": 0.5, "outputUsd": 8},
	}}, nil), http.StatusOK).json(t, &table)
	if table.SpendSource != api.SpendPrices || len(table.Prices) != 2 || table.Prices[0].Model != "*" ||
		table.Prices[1].CachedInputUSD == nil || *table.Prices[1].CachedInputUSD != 0.5 || table.Prices[1].UpdatedAt == "" {
		t.Fatalf("stored table = %+v", table)
	}
	var list api.ProviderList
	e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &list)
	found := false
	for _, p := range list.Items {
		if p.ID == prov.ID {
			found = p.SpendSource == api.SpendPrices && len(p.Prices) == 2
		}
	}
	if !found {
		t.Fatalf("provider list = %+v", list.Items)
	}

	// A run counted at the prices: glm-5, 1M input of which 200k cached, 100k output = 0.8×2 + 0.2×0.5 + 0.1×8.
	now := time.Now()
	if err := e.a.Usage.RecordAttempt(ctx, usage.Attempt{TaskID: "t-prices", Turn: 1, Attempt: 1,
		AccountID: created.ID, Backend: "claude", Model: "glm-5", StartedAt: now, EndedAt: now.Add(time.Second),
		Usage: event.Usage{InputTokens: 800_000, CachedTokens: 200_000, OutputTokens: 100_000}}); err != nil {
		t.Fatal(err)
	}
	var accounts []api.Account
	e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &accounts)
	seen := false
	for _, a := range accounts {
		if a.ID != created.ID {
			continue
		}
		seen = true
		if a.SpendSource != api.SpendPrices || a.SpentUSD != 2.5 || a.BudgetState != api.BudgetReached ||
			a.SpentUnpricedTokens != 0 {
			t.Fatalf("account after a priced run = %+v", a)
		}
	}
	if !seen {
		t.Fatal("account missing")
	}

	e.must(e.req(http.MethodPut, path, map[string]any{"prices": []any{}}, nil), http.StatusOK).json(t, &table)
	if table.SpendSource != api.SpendNone || len(table.Prices) != 0 {
		t.Fatalf("cleared table = %+v", table)
	}
}
