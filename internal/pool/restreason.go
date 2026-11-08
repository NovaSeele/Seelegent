package pool

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// ProviderRestError says that every usable key of a provider rests until the user acts (RestCredits, RestBudget): a
// task on the provider would fail at once or wait for the user. Delegate and followup refuse with it.
type ProviderRestError struct {
	Provider string
	// Credits: at least one key has no credit left. Budgets are the budgets of the keys at their budget, each
	// once, lowest first.
	Credits bool
	Budgets []float64
}

// Error is the message the lead reads: "anthropic keys reached their budget ($200); raise it in Accounts", or "the
// anthropic key has no credit left; top up or change the key".
func (e *ProviderRestError) Error() string {
	amounts := make([]string, 0, len(e.Budgets))
	for _, b := range e.Budgets {
		amounts = append(amounts, account.FormatUSD(b))
	}
	switch {
	case !e.Credits:
		return fmt.Sprintf("%s keys reached their budget (%s); raise it in Accounts", e.Provider,
			strings.Join(amounts, ", "))
	case len(amounts) == 0:
		return fmt.Sprintf("the %s key has no credit left; top up or change the key", e.Provider)
	}
	return fmt.Sprintf("the %s keys have no credit left or reached their budget (%s); top up, raise the budget or "+
		"change the key", e.Provider, strings.Join(amounts, ", "))
}

// APIDetails are the error's details for the API: reason credits (some key has no credit left) or budget_reached,
// the provider, and the budgets reached (when any).
func (e *ProviderRestError) APIDetails() map[string]any {
	d := map[string]any{"reason": RestBudget, "provider": e.Provider}
	if e.Credits {
		d["reason"] = RestCredits
	}
	if len(e.Budgets) > 0 {
		d["budgets"] = e.Budgets
	}
	return d
}

// ProviderResting returns a *ProviderRestError when the provider has enabled API-key accounts with a key and every
// one of them rests until the user acts (restReason); nil otherwise, also when it has none (the pick says so then).
func (p *Pool) ProviderResting(ctx context.Context, providerID string) error {
	v, err := p.load(ctx)
	if err != nil {
		return err
	}
	e := &ProviderRestError{Provider: providerID}
	usable := 0
	for i := range v.accounts {
		a := &v.accounts[i]
		if a.Kind != account.KindAPIKey || a.Provider != providerID || !a.Enabled || a.KeyHint == "" {
			continue
		}
		usable++
		switch v.restReason(a) {
		case "":
			return nil
		case RestCredits:
			e.Credits = true
		case RestBudget:
			if !slices.Contains(e.Budgets, *a.BudgetUSD) {
				e.Budgets = append(e.Budgets, *a.BudgetUSD)
			}
		}
	}
	if usable == 0 {
		return nil
	}
	slices.Sort(e.Budgets)
	return e
}
