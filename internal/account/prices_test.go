package account

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

func ptr(v float64) *float64 { return &v }

func TestCheckPrices(t *testing.T) {
	got, err := CheckPrices([]Price{{Model: " glm-5 ", InputUSD: 1, OutputUSD: 3.2},
		{Model: AnyModel, InputUSD: 0.5, CachedInputUSD: ptr(0.1), OutputUSD: 2}})
	if err != nil || len(got) != 2 || got[0].Model != AnyModel || got[1].Model != "glm-5" {
		t.Fatalf("cleaned: %+v, %v", got, err)
	}
	for _, c := range []struct {
		in    []Price
		field string
	}{
		{[]Price{{Model: ""}}, "prices[0].model"},
		{[]Price{{Model: "a b"}}, "prices[0].model"},
		{[]Price{{Model: "m"}, {Model: "M"}}, "prices[1].model"},
		{[]Price{{Model: "m", InputUSD: -1}}, "prices[0].inputUsd"},
		{[]Price{{Model: "m", OutputUSD: MaxPriceUSD + 1}}, "prices[0].outputUsd"},
		{[]Price{{Model: "m", CachedInputUSD: ptr(-0.1)}}, "prices[0].cachedInputUsd"},
		{make([]Price, MaxPrices+1), "prices"},
	} {
		_, err := CheckPrices(c.in)
		var fe *FieldError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%+v: %v (want field %s)", c.in, err, c.field)
		}
	}
}

// Claude Code counts cache reads apart from input_tokens; Codex counts them inside input_tokens. A missing cached
// price is the input price.
func TestPriceCost(t *testing.T) {
	pr := Price{InputUSD: 3, CachedInputUSD: ptr(0.3), OutputUSD: 15}
	// claude: 1M input + 2M cache reads + 0.1M output = 3 + 0.6 + 1.5
	if got := pr.Cost(config.BackendClaude, 1_000_000, 2_000_000, 100_000); got != 5.1 {
		t.Errorf("claude cost = %v", got)
	}
	// codex: 3M input of which 2M cached + 0.1M output = 3 + 0.6 + 1.5
	if got := pr.Cost(config.BackendCodex, 3_000_000, 2_000_000, 100_000); got != 5.1 {
		t.Errorf("codex cost = %v", got)
	}
	noCache := Price{InputUSD: 2, OutputUSD: 8}
	if got := noCache.Cost(config.BackendCodex, 1000, 400, 500); got != 0.006 {
		t.Errorf("cached at the input price = %v", got)
	}
	p := Provider{Prices: []Price{{Model: "glm-5", InputUSD: 1}, {Model: AnyModel, InputUSD: 9}}}
	if got, ok := p.PriceFor("GLM-5"); !ok || got.InputUSD != 1 {
		t.Errorf("exact (any case): %+v", got)
	}
	if got, ok := p.PriceFor("other"); !ok || got.InputUSD != 9 {
		t.Errorf("fallback: %+v", got)
	}
	if _, ok := (&Provider{Prices: []Price{{Model: "a"}}}).PriceFor("b"); ok {
		t.Error("a price without a match")
	}
}

// The spend source per provider: the service first, local, then prices, then the CLI.
func TestSpendSourceWithPricesAndService(t *testing.T) {
	preset := func(id string) Provider {
		p, err := FromPreset(id, id)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	priced := func(p Provider) Provider {
		p.Prices = []Price{{Model: AnyModel, InputUSD: 1, OutputUSD: 1}}
		return p
	}
	for _, c := range []struct {
		name    string
		p       Provider
		want    string
		reading string
	}{
		{"openrouter", preset("openrouter"), SpendService, ReadingKeyUsage},
		{"openrouter priced", priced(preset("openrouter")), SpendService, ReadingKeyUsage},
		{"deepseek", preset("deepseek"), SpendNone, ReadingAccountBalance},
		{"deepseek priced", priced(preset("deepseek")), SpendPrices, ReadingAccountBalance},
		{"moonshot", preset("moonshot"), SpendNone, ReadingAccountBalance},
		{"zai priced", priced(preset("zai")), SpendPrices, ""},
		{"anthropic", preset("anthropic"), SpendCLI, ""},
		{"anthropic priced", priced(preset("anthropic")), SpendPrices, ""},
		{"ollama priced", priced(preset("ollama")), SpendFree, ""},
		{"localhost", Provider{Backend: config.BackendClaude, BaseURL: "http://localhost:4000"}, SpendFree, ""},
		{"groq", preset("groq"), SpendNone, ""},
	} {
		if got := c.p.SpendSource(); got != c.want {
			t.Errorf("%s: source %q, want %q", c.name, got, c.want)
		}
		if got := c.p.ServiceReading(); got != c.reading {
			t.Errorf("%s: reading %q, want %q", c.name, got, c.reading)
		}
	}
	if preset("deepseek").PricingURL() == "" || preset("custom-anthropic").PricingURL() != "" ||
		(&Provider{BaseURL: "https://api.moonshot.cn/anthropic"}).PricingURL() == "" {
		t.Error("pricing URLs")
	}
}

// Prices are stored per provider, listed with it, kept by a provider update and removed with the provider.
func TestSetPrices(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	a := spendFixture(t, f, "zai")
	if _, err := f.m.SetPrices(ctx, "missing", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown provider: %v", err)
	}
	p, err := f.m.SetPrices(ctx, a.Provider, []Price{{Model: "glm-5", InputUSD: 1, CachedInputUSD: ptr(0.2),
		OutputUSD: 3.2}})
	if err != nil || len(p.Prices) != 1 || p.Prices[0].UpdatedAt.IsZero() || *p.Prices[0].CachedInputUSD != 0.2 ||
		p.SpendSource() != SpendPrices {
		t.Fatalf("set: %+v, %v", p, err)
	}
	list, err := f.m.ListProviders(ctx)
	if err != nil || len(list) != 1 || len(list[0].Prices) != 1 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	p.Name = "Z.ai renamed"
	p.Prices = nil
	if _, err := f.m.UpdateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.m.GetProvider(ctx, p.ID); len(got.Prices) != 1 {
		t.Fatalf("an update dropped the prices: %+v", got)
	}
	if _, err := f.m.SetPrices(ctx, p.ID, []Price{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.m.GetProvider(ctx, p.ID); len(got.Prices) != 0 || got.SpendSource() != SpendNone {
		t.Fatalf("cleared: %+v", got)
	}
	if _, err := f.m.SetPrices(ctx, p.ID, []Price{{Model: "glm-5", InputUSD: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Remove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.DeleteProvider(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM provider_prices").Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("prices left after the provider: %d, %v", n, err)
	}
}

// Tokens × prices, cached input included; prices set on the official Anthropic API replace the CLI's estimate for
// the models they cover; a local service spends nothing; an attempt recorded again takes back exactly what it
// added, even when the prices changed in between.
func TestApplySpendWithPrices(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	at := formatTime(f.clock.t.Add(time.Minute))

	z := spendFixture(t, f, "zai")
	if _, err := f.m.SetPrices(ctx, z.Provider, []Price{{Model: "glm-5", InputUSD: 1, CachedInputUSD: ptr(0.1),
		OutputUSD: 4}}); err != nil {
		t.Fatal(err)
	}
	row := SpendRow{AccountID: z.ID, StartedAt: at, Backend: config.BackendClaude, Model: "glm-5", CostUSD: 9,
		Input: 200_000, Cached: 1_000_000, Output: 50_000, Total: 250_000}
	first, _ := f.record(t, nil, row) // 0.2 + 0.1 + 0.2
	if first.Source != SpendPrices || first.USD != 0.5 {
		t.Fatalf("counted %q %v", first.Source, first.USD)
	}
	if got := f.get(t, z.ID); got.Spent.USD != 0.5 || got.Spent.Unpriced != 0 {
		t.Fatalf("priced spend: %+v", got.Spent)
	}
	// The prices double; the same attempt recorded again takes back 0.5 and adds the new cost.
	if _, err := f.m.SetPrices(ctx, z.Provider, []Price{{Model: "glm-5", InputUSD: 2, CachedInputUSD: ptr(0.2),
		OutputUSD: 8}}); err != nil {
		t.Fatal(err)
	}
	again, _ := f.record(t, &first, row)
	if again.USD != 1 || f.get(t, z.ID).Spent.USD != 1 || f.get(t, z.ID).Spent.Tokens != 250_000 {
		t.Fatalf("recorded again: %+v, %+v", again, f.get(t, z.ID).Spent)
	}
	// A model without a price counts its tokens unpriced.
	other := row
	other.StartedAt, other.Model = at+"b", "glm-4"
	if c, _ := f.record(t, nil, other); c.Source != SpendNone || c.USD != 0 {
		t.Fatalf("unpriced model: %+v", c)
	}
	if got := f.get(t, z.ID).Spent; got.USD != 1 || got.Unpriced != 250_000 || got.Tokens != 500_000 {
		t.Fatalf("with an unpriced model: %+v", got)
	}

	// Official Anthropic: prices win for the models they cover, the CLI's estimate counts for the others.
	a := spendFixture(t, f, "anthropic")
	if _, err := f.m.SetPrices(ctx, a.Provider, []Price{{Model: "claude-sonnet-5-5", InputUSD: 3,
		OutputUSD: 15}}); err != nil {
		t.Fatal(err)
	}
	priced, _ := f.record(t, nil, SpendRow{AccountID: a.ID, StartedAt: at, Backend: config.BackendClaude,
		Model: "claude-sonnet-5-5", CostUSD: 0.9, Input: 100_000, Output: 10_000, Total: 110_000})
	cli, _ := f.record(t, nil, SpendRow{AccountID: a.ID, StartedAt: at + "b", Backend: config.BackendClaude,
		Model: "claude-haiku-5-5", CostUSD: 0.02, Input: 1000, Output: 100, Total: 1100})
	if priced.Source != SpendPrices || priced.USD != 0.45 || cli.Source != SpendCLI || cli.USD != 0.02 {
		t.Fatalf("anthropic: %+v %+v", priced, cli)
	}
	got := f.get(t, a.ID)
	if src, usd := got.SpendOf(providerOf(t, f, a.Provider)); src != SpendPrices || usd != 0.47 {
		t.Fatalf("anthropic spend: %s %v", src, usd)
	}

	// Local: free, nothing unpriced.
	o := spendFixture(t, f, "ollama")
	if c, _ := f.record(t, nil, SpendRow{AccountID: o.ID, StartedAt: at, Backend: config.BackendCodex,
		Model: "qwen", CostUSD: 1, Input: 10, Output: 10, Total: 20}); c.Source != SpendFree || c.USD != 0 {
		t.Fatalf("ollama: %+v", c)
	}
	if got := f.get(t, o.ID).Spent; got.USD != 0 || got.Unpriced != 0 || got.Tokens != 20 {
		t.Fatalf("ollama spend: %+v", got)
	}
}

// A budget is judged with prices: it warns, is reached and stops the account.
func TestBudgetWithPrices(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	d := spendFixture(t, f, "deepseek")
	budget := 1.0
	if _, err := f.m.Repo().Update(ctx, d.ID, Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	at := formatTime(f.clock.t.Add(time.Minute))
	row := SpendRow{AccountID: d.ID, StartedAt: at, Backend: config.BackendClaude, Model: "deepseek-chat",
		Input: 900_000, Total: 900_000}
	if c := f.apply(t, nil, row); len(c) != 0 {
		t.Fatalf("no prices: %+v", c)
	}
	// Prices set afterwards: the budget is judged again, but the spend counted keeps the prices of its time.
	if _, err := f.m.SetPrices(ctx, d.Provider, []Price{{Model: AnyModel, InputUSD: 1, OutputUSD: 2}}); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, d.ID); got.Spent.USD != 0 || got.budgetLevel != BudgetOK {
		t.Fatalf("after setting prices: %+v %q", got.Spent, got.budgetLevel)
	}
	row.StartedAt = at + "b"
	c := f.apply(t, nil, row)
	if len(c) != 1 || c[0].State != BudgetWarn || c[0].SpentUSD != 0.9 {
		t.Fatalf("warn: %+v", c)
	}
	row.StartedAt = at + "c"
	c = f.apply(t, nil, row)
	if got := f.get(t, d.ID); len(c) != 1 || c[0].State != BudgetReached || !got.BudgetStopped() {
		t.Fatalf("reached: %+v", c)
	}
}

func providerOf(t *testing.T, f *fixture, id string) *Provider {
	t.Helper()
	p, err := f.m.GetProvider(ctxT(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return &p
}
