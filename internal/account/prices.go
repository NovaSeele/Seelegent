package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// Price is what a provider charges for one model, in US dollars per million tokens, as the user set it: Seelegent
// ships no prices (they change too often). CachedInputUSD is the price of input served from a prompt cache; nil
// means the input price.
type Price struct {
	// Model is the model id, or AnyModel for every model of the provider without a row of its own.
	Model          string    `json:"model"`
	InputUSD       float64   `json:"inputUsd"`
	CachedInputUSD *float64  `json:"cachedInputUsd,omitempty"`
	OutputUSD      float64   `json:"outputUsd"`
	UpdatedAt      time.Time `json:"updatedAt,omitzero"`
}

// Price table limits.
const (
	// AnyModel is the model of a price that covers every model without its own price.
	AnyModel = "*"
	// MaxPrices bounds the rows of one provider's price table.
	MaxPrices = 200
	// MaxPriceUSD bounds one price, in US dollars per million tokens.
	MaxPriceUSD = 10000
	// maxPriceModel bounds a model id in the price table.
	maxPriceModel = 200
)

// Cost returns what tokens cost at this price. input, cached and output are the usage the CLI of backend reports:
// Claude Code counts cache reads apart from input_tokens, Codex counts them inside input_tokens. Cache writes
// (Claude Code's cache_creation_input_tokens) are not reported to Seelegent and cost nothing here.
func (pr Price) Cost(backend string, input, cached, output int64) float64 {
	input, cached, output = max(0, input), max(0, cached), max(0, output)
	uncached := input
	if backend != config.BackendClaude {
		uncached = max(0, input-cached)
	}
	cachedRate := pr.InputUSD
	if pr.CachedInputUSD != nil {
		cachedRate = *pr.CachedInputUSD
	}
	return roundUSD((float64(uncached)*pr.InputUSD + float64(cached)*cachedRate + float64(output)*pr.OutputUSD) / 1e6)
}

// PriceFor returns the provider's price for model: its own row (case does not matter), else the AnyModel row.
func (p *Provider) PriceFor(model string) (Price, bool) {
	model = strings.TrimSpace(model)
	var fallback Price
	found := false
	for _, pr := range p.Prices {
		switch {
		case model != "" && strings.EqualFold(pr.Model, model):
			return pr, true
		case pr.Model == AnyModel:
			fallback, found = pr, true
		}
	}
	return fallback, found
}

// CheckPrices cleans a price table (trimmed model ids, sorted) and checks it. Its errors match ErrInvalid and name
// the field as "prices[i].model", "prices[i].inputUsd", ... (FieldError).
func CheckPrices(in []Price) ([]Price, error) {
	if len(in) > MaxPrices {
		return nil, withField("prices", invalidf("at most %d prices per service", MaxPrices))
	}
	out := make([]Price, 0, len(in))
	for i, pr := range in {
		field := func(name string) string { return fmt.Sprintf("prices[%d].%s", i, name) }
		pr.Model = strings.TrimSpace(pr.Model)
		switch {
		case pr.Model == "":
			return nil, withField(field("model"), invalidf("a price needs a model id (or %q for any model)", AnyModel))
		case len(pr.Model) > maxPriceModel:
			return nil, withField(field("model"), invalidf("model id is longer than %d characters", maxPriceModel))
		case strings.ContainsFunc(pr.Model, func(r rune) bool { return r <= ' ' }):
			return nil, withField(field("model"), invalidf("model id %q must not contain spaces", pr.Model))
		}
		if slices.ContainsFunc(out, func(o Price) bool { return strings.EqualFold(o.Model, pr.Model) }) {
			return nil, withField(field("model"), invalidf("model %q has two prices", pr.Model))
		}
		if err := checkPrice(pr.InputUSD); err != nil {
			return nil, withField(field("inputUsd"), err)
		}
		if err := checkPrice(pr.OutputUSD); err != nil {
			return nil, withField(field("outputUsd"), err)
		}
		if pr.CachedInputUSD != nil {
			if err := checkPrice(*pr.CachedInputUSD); err != nil {
				return nil, withField(field("cachedInputUsd"), err)
			}
			v := *pr.CachedInputUSD
			pr.CachedInputUSD = &v
		}
		pr.UpdatedAt = time.Time{}
		out = append(out, pr)
	}
	slices.SortFunc(out, func(a, b Price) int { return strings.Compare(a.Model, b.Model) })
	return out, nil
}

func checkPrice(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxPriceUSD {
		return invalidf("a price is US dollars per million tokens, from 0 to %d", MaxPriceUSD)
	}
	return nil
}

// SetPrices replaces the price table of a provider and returns the provider with it. The budgets of its accounts
// are judged again; the spend already counted keeps the prices it was counted at.
func (r *Repo) SetPrices(ctx context.Context, providerID string, prices []Price) (Provider, error) {
	prices, err := CheckPrices(prices)
	if err != nil {
		return Provider{}, err
	}
	var p Provider
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := getProvider(ctx, tx, providerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM provider_prices WHERE provider = ?", providerID); err != nil {
			return err
		}
		now := formatTime(r.stamp())
		for _, pr := range prices {
			var cached any
			if pr.CachedInputUSD != nil {
				cached = *pr.CachedInputUSD
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO provider_prices (provider, model, input_usd, cached_input_usd,
				output_usd, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, providerID, pr.Model, pr.InputUSD, cached,
				pr.OutputUSD, now); err != nil {
				return err
			}
		}
		if err := refreshProviderBudgetsTx(ctx, tx, providerID); err != nil {
			return err
		}
		var err error
		p, err = getProvider(ctx, tx, providerID)
		return err
	})
	if err != nil {
		return Provider{}, wrap("set prices", err)
	}
	return p, nil
}

// SetPrices replaces a provider's price table; see Repo.SetPrices.
func (m *Manager) SetPrices(ctx context.Context, providerID string, prices []Price) (Provider, error) {
	return m.repo.SetPrices(ctx, providerID, prices)
}

// loadPricesTx returns the price tables of the providers, by provider id (only the named one when id is not "").
func loadPricesTx(ctx context.Context, tx *sql.Tx, id string) (map[string][]Price, error) {
	query := `SELECT provider, model, input_usd, cached_input_usd, output_usd, updated_at FROM provider_prices`
	var args []any
	if id != "" {
		query += " WHERE provider = ?"
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, query+" ORDER BY provider, model", args...)
	if err != nil {
		return nil, err
	}
	out := map[string][]Price{}
	for rows.Next() {
		var (
			provider, updated string
			pr                Price
			cached            sql.NullFloat64
		)
		if err := rows.Scan(&provider, &pr.Model, &pr.InputUSD, &cached, &pr.OutputUSD, &updated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if cached.Valid {
			v := cached.Float64
			pr.CachedInputUSD = &v
		}
		if pr.UpdatedAt, err = parseTime(updated); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("price of %s on %s: %w", pr.Model, provider, err)
		}
		out[provider] = append(out[provider], pr)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

// PricingURL returns the page where the provider's service publishes its prices ("" when unknown: a custom
// service, or a local one, which is free). The prices form links it.
func (p Provider) PricingURL() string {
	switch host := providerHost(&p); host {
	case "api.moonshot.cn":
		return "https://platform.moonshot.cn/docs/pricing/chat"
	case anthropicHost:
		return PresetPricingURL("anthropic")
	case "api.deepseek.com":
		return PresetPricingURL("deepseek")
	case "api.moonshot.ai":
		return PresetPricingURL("moonshot")
	case "api.z.ai":
		return PresetPricingURL("zai")
	case "openrouter.ai":
		return PresetPricingURL("openrouter")
	case "api.openai.com":
		return PresetPricingURL("openai")
	case "api.groq.com":
		return PresetPricingURL("groq")
	}
	return PresetPricingURL(p.Preset)
}

// PresetPricingURL returns the official pricing page of a preset's service ("" when it has none).
func PresetPricingURL(preset string) string {
	switch preset {
	case "anthropic":
		return "https://www.anthropic.com/pricing#api"
	case "deepseek":
		return "https://api-docs.deepseek.com/quick_start/pricing"
	case "moonshot":
		return "https://platform.moonshot.ai/docs/pricing/chat"
	case "zai":
		return "https://docs.z.ai/guides/overview/pricing"
	case "openrouter":
		return "https://openrouter.ai/models"
	case "openai":
		return "https://openai.com/api/pricing/"
	case "groq":
		return "https://groq.com/pricing"
	}
	return ""
}

// providerHost is the lower-case host of the provider's base URL (api.anthropic.com for the official provider), ""
// when it has none.
func providerHost(p *Provider) string {
	if p.Official {
		return anthropicHost
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
