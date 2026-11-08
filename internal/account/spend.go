package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// Spend sources (Provider.SpendSource): where an API-key account's spentUsd comes from. The set is open: a later
// version adds "service" (the service's own balance or usage endpoint) and "prices" (tokens times prices the user
// sets), so clients treat an unknown value like SpendNone.
const (
	// SpendCLI: the cost the CLI reports per run (Claude Code total_cost_usd). Only the official Anthropic API: the
	// CLI's price table does not know the models of other services.
	SpendCLI = "cli"
	// SpendFree: a local service (Ollama, a localhost base URL); nothing is spent.
	SpendFree = "free"
	// SpendNone: no trusted cost; only tokens are counted.
	SpendNone = "none"
)

// Budget states (Account.BudgetState).
const (
	// BudgetOK: under 80% of the budget.
	BudgetOK = "ok"
	// BudgetWarn: 80% of the budget or more.
	BudgetWarn = "warn"
	// BudgetReached: the budget is used up; with BudgetStop the pool rests the account.
	BudgetReached = "reached"
	// BudgetUnknown: a budget is set but the provider's spend source gives no cost, so it cannot be judged.
	BudgetUnknown = "unknown"
)

// budgetWarnShare is the share of the budget at which the state turns BudgetWarn.
const budgetWarnShare = 0.8

// MaxBudgetUSD bounds a budget.
const MaxBudgetUSD = 1e7

// SpendSource says where the spend of the provider's accounts comes from: SpendCLI for the official Anthropic API
// (also a legacy row that points at api.anthropic.com), SpendFree for Ollama and local base URLs, SpendNone
// otherwise. It is the one place that decides; Spend.add is the one place that adds.
func (p Provider) SpendSource() string {
	q := p
	if q.Official || isAnthropicHost(q.BaseURL) {
		q.normalizeEndpoint()
	}
	switch {
	case q.Backend == config.BackendClaude && q.Official:
		return SpendCLI
	case q.Preset == "ollama" || isLocalURL(q.BaseURL):
		return SpendFree
	}
	return SpendNone
}

// isLocalURL reports a base URL on this computer (localhost, a loopback address).
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Spend is what an API-key account spent on its current key.
type Spend struct {
	// USD is the cost counted from the spend source (0 when the source gives none).
	USD float64 `json:"usd"`
	// Tokens is every token the CLI reported; Input and Output are its parts when reported.
	Tokens int64 `json:"tokens"`
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

// PreviousKey is what the key before the current one spent, kept when the key is replaced or removed.
type PreviousKey struct {
	KeyHint   string    `json:"keyHint,omitempty"`
	Since     time.Time `json:"since,omitzero"`
	Until     time.Time `json:"until"`
	Spent     Spend     `json:"spent"`
	BudgetUSD *float64  `json:"budgetUsd,omitempty"`
}

// BudgetLevel returns the budget state of spent against budget for a spend source: "" without a budget,
// BudgetUnknown when the source gives no cost, else BudgetOK, BudgetWarn or BudgetReached.
func BudgetLevel(source string, spentUSD float64, budget *float64) string {
	switch {
	case budget == nil:
		return ""
	case source != SpendCLI:
		return BudgetUnknown
	case spentUSD >= *budget:
		return BudgetReached
	case spentUSD >= budgetWarnShare**budget:
		return BudgetWarn
	}
	return BudgetOK
}

// BudgetStopped reports an API-key account the pool must not use: its budget is reached (as last judged with its
// provider's spend source) and BudgetStop is on.
func (a *Account) BudgetStopped() bool {
	return a.Kind == KindAPIKey && a.BudgetStop && a.BudgetUSD != nil && a.budgetLevel == BudgetReached
}

// validateBudget checks a budget in US dollars: positive, finite and at most MaxBudgetUSD.
func validateBudget(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > MaxBudgetUSD {
		return withField("budgetUsd", invalidf("a budget is a positive amount of US dollars up to %.0f", MaxBudgetUSD))
	}
	return nil
}

// SpendRow is one attempt's usage as the usage table stores it (StartedAt in the stored time format).
type SpendRow struct {
	AccountID string
	StartedAt string
	CostUSD   float64
	Input     int64
	Output    int64
	Total     int64
}

// BudgetCrossing is an account whose budget state rose to BudgetWarn or BudgetReached.
type BudgetCrossing struct {
	AccountID string
	Label     string
	Provider  string
	State     string
	SpentUSD  float64
	BudgetUSD float64
}

// Message is the notice text: "acc9 (anthropic) has used 80% of its $200 budget ($160.00 spent)".
func (c BudgetCrossing) Message() string {
	name := c.AccountID
	if c.Label != "" {
		name = fmt.Sprintf("%s (%s)", c.Label, c.AccountID)
	}
	if c.State == BudgetReached {
		return fmt.Sprintf("%s reached its %s budget on provider %s (%s spent)", name, FormatUSD(c.BudgetUSD),
			c.Provider, FormatUSD(c.SpentUSD))
	}
	return fmt.Sprintf("%s has used %.0f%% of its %s budget on provider %s (%s spent)", name,
		math.Floor(100*c.SpentUSD/c.BudgetUSD+1e-9), FormatUSD(c.BudgetUSD), c.Provider, FormatUSD(c.SpentUSD))
}

// FormatUSD writes an amount of dollars for messages: whole dollars without cents ("$200"), small amounts with
// enough digits to show ("$0.155"), others with cents ("$12.40").
func FormatUSD(v float64) string {
	switch {
	case v == math.Trunc(v) && v < 1e12:
		return fmt.Sprintf("$%.0f", v)
	case v < 1:
		s := strings.TrimRight(fmt.Sprintf("%.3f", v), "0")
		if strings.HasSuffix(s, ".") || len(s)-strings.Index(s, ".") < 3 {
			s = fmt.Sprintf("%.2f", v)
		}
		return "$" + s
	}
	return fmt.Sprintf("$%.2f", v)
}

// roundUSD rounds a running total to a billionth of a dollar, so repeated deltas do not drift.
func roundUSD(v float64) float64 { return math.Round(v*1e9) / 1e9 }

// spendState is what ApplySpendTx and refreshBudgetTx read of an account.
type spendState struct {
	kind     Kind
	label    string
	provider string
	keySetAt string
	spent    Spend
	budget   *float64
	level    string
}

func readSpendState(ctx context.Context, tx *sql.Tx, id string) (spendState, bool, error) {
	var (
		st       spendState
		kind     string
		provider sql.NullString
		budget   sql.NullFloat64
	)
	err := tx.QueryRowContext(ctx, `SELECT kind, label, provider, key_set_at, spent_usd, spent_tokens,
		spent_input_tokens, spent_output_tokens, budget_usd, budget_level FROM accounts WHERE id = ?`, id).Scan(
		&kind, &st.label, &provider, &st.keySetAt, &st.spent.USD, &st.spent.Tokens, &st.spent.Input,
		&st.spent.Output, &budget, &st.level)
	if errors.Is(err, sql.ErrNoRows) {
		return spendState{}, false, nil
	}
	if err != nil {
		return spendState{}, false, err
	}
	st.kind, st.provider = Kind(kind), provider.String
	if budget.Valid {
		v := budget.Float64
		st.budget = &v
	}
	return st, true, nil
}

// spendSourceTx returns the spend source of an account's provider (SpendNone when it cannot be read).
func spendSourceTx(ctx context.Context, tx *sql.Tx, providerID string) (string, error) {
	if providerID == "" {
		return SpendNone, nil
	}
	p, err := getProvider(ctx, tx, providerID)
	if errors.Is(err, ErrNotFound) {
		return SpendNone, nil
	}
	if err != nil {
		return "", err
	}
	return p.SpendSource(), nil
}

// add returns the spend with what row adds for an account whose source is given: the cost only from SpendCLI,
// tokens always. sign is +1 or -1 (taking back an attempt's earlier record).
func (s Spend) add(row SpendRow, source string, sign int64) Spend {
	if source == SpendCLI {
		s.USD = roundUSD(max(0, s.USD+float64(sign)*row.CostUSD))
	}
	s.Tokens = max(0, s.Tokens+sign*row.Total)
	s.Input = max(0, s.Input+sign*row.Input)
	s.Output = max(0, s.Output+sign*row.Output)
	return s
}

// ApplySpendTx adds an attempt's usage to the spend of its API-key account, in the transaction that records the
// attempt. prev is the attempt's earlier record (nil when it is new): what it added is taken back first, so an
// attempt recorded twice counts once, also when it moved to another account. Only usage of attempts that started
// on the current key counts (started at or after the key was set). It returns the accounts whose budget state rose
// to BudgetWarn or BudgetReached.
func ApplySpendTx(ctx context.Context, tx *sql.Tx, prev *SpendRow, next SpendRow) ([]BudgetCrossing, error) {
	ids := []string{next.AccountID}
	if prev != nil && prev.AccountID != next.AccountID {
		ids = append(ids, prev.AccountID)
	}
	var out []BudgetCrossing
	for _, id := range ids {
		if id == "" {
			continue
		}
		st, found, err := readSpendState(ctx, tx, id)
		if err != nil || !found || st.kind != KindAPIKey {
			if err != nil {
				return nil, fmt.Errorf("spend of %s: %w", id, err)
			}
			continue
		}
		source, err := spendSourceTx(ctx, tx, st.provider)
		if err != nil {
			return nil, fmt.Errorf("spend of %s: %w", id, err)
		}
		spent := st.spent
		if prev != nil && prev.AccountID == id && prev.StartedAt >= st.keySetAt {
			spent = spent.add(*prev, source, -1)
		}
		if next.AccountID == id && next.StartedAt >= st.keySetAt {
			spent = spent.add(next, source, 1)
		}
		if spent == st.spent {
			continue
		}
		level := BudgetLevel(source, spent.USD, st.budget)
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET spent_usd = ?, spent_tokens = ?, spent_input_tokens = ?,
			spent_output_tokens = ?, budget_level = ? WHERE id = ?`, spent.USD, spent.Tokens, spent.Input, spent.Output,
			level, id); err != nil {
			return nil, fmt.Errorf("spend of %s: %w", id, err)
		}
		if budgetRank(level) > budgetRank(st.level) {
			out = append(out, BudgetCrossing{AccountID: id, Label: st.label, Provider: st.provider, State: level,
				SpentUSD: spent.USD, BudgetUSD: *st.budget})
		}
	}
	return out, nil
}

// budgetRank orders the states that can rise: warn below reached; the others are 0.
func budgetRank(level string) int {
	switch level {
	case BudgetWarn:
		return 1
	case BudgetReached:
		return 2
	}
	return 0
}

// refreshBudgetTx judges the account's budget again (after the budget, the key or the provider changed).
func refreshBudgetTx(ctx context.Context, tx *sql.Tx, id string) error {
	st, found, err := readSpendState(ctx, tx, id)
	if err != nil || !found {
		return err
	}
	source, err := spendSourceTx(ctx, tx, st.provider)
	if err != nil {
		return err
	}
	level := BudgetLevel(source, st.spent.USD, st.budget)
	if level == st.level {
		return nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE accounts SET budget_level = ? WHERE id = ?", level, id)
	return err
}

// archiveSpendTx keeps what the current key spent as the account's previous key and starts the count again, when
// the key is replaced or removed. It returns the archived record (nil when nothing was spent).
func archiveSpendTx(ctx context.Context, tx *sql.Tx, id, oldHint string, until time.Time) (*PreviousKey, error) {
	st, found, err := readSpendState(ctx, tx, id)
	if err != nil || !found {
		return nil, err
	}
	var prev *PreviousKey
	if st.spent != (Spend{}) {
		prev = &PreviousKey{KeyHint: oldHint, Until: until, Spent: st.spent, BudgetUSD: st.budget}
		if st.keySetAt != "" {
			if prev.Since, err = parseTime(st.keySetAt); err != nil {
				return nil, err
			}
		}
		data, err := json.Marshal(prev)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET previous_key = ? WHERE id = ?", string(data),
			id); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET spent_usd = 0, spent_tokens = 0, spent_input_tokens = 0,
		spent_output_tokens = 0 WHERE id = ?`, id)
	return prev, err
}

// setBudgetTx sets the budget (nil: none) and, when stop is given, BudgetStop of an account, and judges it again.
func setBudgetTx(ctx context.Context, tx *sql.Tx, id string, budget *float64, stop *bool) error {
	var v any
	if budget != nil {
		v = *budget
	}
	if _, err := tx.ExecContext(ctx, "UPDATE accounts SET budget_usd = ? WHERE id = ?", v, id); err != nil {
		return err
	}
	if stop != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET budget_stop = ? WHERE id = ?", *stop, id); err != nil {
			return err
		}
	}
	return refreshBudgetTx(ctx, tx, id)
}

// refreshProviderBudgetsTx judges the budgets of the provider's accounts again.
func refreshProviderBudgetsTx(ctx context.Context, tx *sql.Tx, providerID string) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM accounts WHERE provider = ?", providerID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		if err := refreshBudgetTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
