package backend

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// The cost of Claude Code's success.jsonl at Sonnet-like prices ($3 input, $0.30 cache read, $15 output; cache
// writes by Anthropic's rule, $3.75): input 2150, cache writes 4100, cache reads 18000, output 920 =
// 0.00645 + 0.015375 + 0.0054 + 0.0138 = 0.041025. Without the cache writes it was 0.02565.
func TestClaudeFixtureCost(t *testing.T) {
	_, o := replay(t, mustBackend(t, "claude"), "success.jsonl")
	u := o.Usage
	p := account.Provider{Backend: config.BackendClaude, Prices: []account.Price{{Model: account.AnyModel,
		InputUSD: 3, CachedInputUSD: new(0.3), OutputUSD: 15}}}
	source, usd := account.AttemptCost(&p, config.BackendClaude, o.Model, account.Tokens{Input: u.InputTokens,
		Cached: u.CachedTokens, CacheWrite: u.CacheWriteTokens, CacheWrite1h: u.CacheWrite1hTokens,
		Output: u.OutputTokens}, u.CostUSD)
	if source != account.SpendPrices || usd != 0.041025 {
		t.Fatalf("fixture cost: %s %v", source, usd)
	}
}
